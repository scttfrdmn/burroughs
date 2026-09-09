// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The preview-1 filesystem errno values, from `typenames.witx`'s `errno` enum ([decision 0083][0083],
// requirement 3): named from the authority, not invented, because a wrong errno makes a guest take a
// different branch silently — the least noticeable failure in the subsystem.
//
// [0083]: ../../docs/decisions/0083-a-read-only-wasi-preview1-filesystem-a-per-run-fd-table-capability-preopens-and-path-open-scoped-to-reading.md
const (
	errAcces      uint16 = 2  // permission denied
	errExist      uint16 = 20 // file exists
	errIsdir      uint16 = 31 // is a directory
	errNoent      uint16 = 44 // no such file or directory
	errNotdir     uint16 = 54 // not a directory
	errNotcapable uint16 = 76 // capability insufficient — the escape and write-refusal channel
)

// preview-1 `filetype` values (`typenames.witx`).
const (
	filetypeCharDevice uint8 = 2
	filetypeDirectory  uint8 = 3
	filetypeRegular    uint8 = 4
)

// `oflags` bits for path_open (`typenames.witx`). Each implies creating or truncating — i.e. writing.
const (
	oflagCreat uint16 = 1 << 0
	oflagExcl  uint16 = 1 << 2
	oflagTrunc uint16 = 1 << 3
)

// fdEntry is one row of the per-run fd table. Exactly one shape is set: a stdio stream, a preopen
// directory, or an opened file. **Read-only lives in the surface, not here** (decision 0083,
// requirement 1): the entry holds an `*os.File` whatever mode it was opened in, so a later write slice
// sets `file` from an `O_RDWR` open without reshaping the table.
type fdEntry struct {
	reader  io.Reader   // stdin (fd 0)
	writer  io.Writer   // stdout / stderr (fd 1, 2)
	preopen *preopenDir // a granted directory (fd 3..)
	file    *os.File    // an opened file
}

// preopenDir is a directory the embedder granted: the name the guest sees, and the resolved host root
// it maps to. `hostRoot` is absolute with symlinks followed, because the escape check compares a
// resolved request against a resolved root (decision 0083, requirement 5).
type preopenDir struct {
	guestName string
	hostRoot  string
}

// initFDs builds the fd table: stdio at 0/1/2, then a preopen dir per granted directory at 3.. Each
// preopen's host path is resolved (made absolute, symlinks followed) up front, so the escape check
// later is resolved-against-resolved. A host path that is not a directory is a configuration error and
// fails the run — the embedder named something the guest cannot be given.
func (h *host) initFDs(preopens []Preopen) error {
	h.fds = map[uint32]*fdEntry{
		0: {reader: h.stdin},
		1: {writer: h.stdout},
		2: {writer: h.stderr},
	}
	fd := uint32(3)
	for _, p := range preopens {
		root, err := filepath.Abs(p.Host)
		if err != nil {
			return fmt.Errorf("wasi: preopen %q: %w", p.Host, err)
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return fmt.Errorf("wasi: preopen %q: %w", p.Host, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return fmt.Errorf("wasi: preopen %q: %w", p.Host, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("wasi: preopen %q is not a directory", p.Host)
		}
		guest := p.Guest
		if guest == "" {
			guest = p.Host
		}
		h.fds[fd] = &fdEntry{preopen: &preopenDir{guestName: guest, hostRoot: root}}
		h.preopenFDs = append(h.preopenFDs, fd)
		fd++
	}
	h.nextFD = fd
	return nil
}

// preopenAt returns the preopen directory a dir fd names, or nil if the fd is not a preopen.
func (h *host) preopenAt(fd uint32) *preopenDir {
	if e := h.fds[fd]; e != nil {
		return e.preopen
	}
	return nil
}

// resolveUnder resolves a guest path against a preopen's host root and enforces the capability
// boundary **on the resolved path** (decision 0083, requirement 5). The request is joined under the
// host root (an absolute or `..`-laden guest path cannot escape the join), symlinks are followed by
// `EvalSymlinks`, and the real result must lie within the resolved root. A textual `..` filter is not
// enough — a symlink pointing out, or a platform spelling, walks straight through it — so the
// containment test is on the resolved path, never the textual one.
//
// `EvalSymlinks` requires the target to exist, which is exactly right for a read: a missing file is
// `errNoent`, and only a file that exists can be checked for containment and then read.
func (h *host) resolveUnder(dir *preopenDir, guestPath string) (hostPath string, errno uint16) {
	resolved, err := resolvePath(dir.hostRoot, guestPath)
	if err != nil {
		return "", errnoForPathError(err)
	}
	if !contained(dir.hostRoot, resolved) {
		// The resolved path climbed above the granted root: an escape, refused as a missing
		// capability rather than a missing file (decision 0083, requirement 2).
		return "", errNotcapable
	}
	return resolved, errSuccess
}

// resolvePath joins a guest path under a host root and follows symlinks to a real host path — an
// absolute or `..`-laden guest path cannot escape the join, and a symlink is followed to its target.
// It does **not** enforce containment; `contained` does, and the two are separate so the capability
// check is a named step a control can witness removing (decision 0083, requirement 6).
func resolvePath(hostRoot, guestPath string) (string, error) {
	return filepath.EvalSymlinks(filepath.Join(hostRoot, guestPath))
}

// contained reports whether a resolved path lies within the resolved host root — the capability
// boundary, decided on the **resolved** path and never the textual one (decision 0083, requirement 5),
// which is why a symlink or absolute spelling that resolves outside is caught here.
func contained(hostRoot, resolved string) bool {
	rel, err := filepath.Rel(hostRoot, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// errnoForPathError maps a host OS error onto a preview-1 errno. The host's `syscall.Errno` is the
// runner's platform, and its values map onto the witx enum the guest reads; the `os.Err*` fallbacks
// cover the wrapped cases.
func errnoForPathError(err error) uint16 {
	var se syscall.Errno
	if errors.As(err, &se) {
		switch se {
		case syscall.ENOENT:
			return errNoent
		case syscall.EACCES:
			return errAcces
		case syscall.EISDIR:
			return errIsdir
		case syscall.ENOTDIR:
			return errNotdir
		case syscall.EEXIST:
			return errExist
		default:
			// Every other host errno falls to the os.Err* checks below, then errIO. Named so the
			// exhaustive switch is complete rather than open (grave-free: a new host errno lands here).
		}
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		return errNoent
	case errors.Is(err, os.ErrPermission):
		return errAcces
	default:
		return errIO
	}
}

// pathOpen opens a file under a preopen for reading. It refuses write intent as a missing capability
// (read-only surface), resolves the path with the escape check, opens `O_RDONLY`, and hands back a new
// fd. Signature (`typenames.witx` path_open): fd, dirflags, path, path_len, oflags, fs_rights_base
// (i64), fs_rights_inheriting (i64), fdflags, opened_fd_ptr → errno.
func (h *host) pathOpen(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	dir := h.preopenAt(u32(args, 0))
	if dir == nil {
		return ret(errBadf), nil
	}
	// Read-only is enforced on the *open mode*, not on `fs_rights_base`. Measured: Go's `os.Open`
	// sends `oflags=0` but a broad `fs_rights_base` (`0xff7febe`, `fd_write` included) — it requests
	// write rights speculatively even for a read — so refusing on those bits would refuse every read
	// (ADR 0083's 2026-09-09 append records this correction to requirement 1). A create/truncate/excl
	// open *is* a write and is refused here as a missing capability; a plain open is granted a
	// read-only fd, and an actual `fd_write` to it fails because a file entry carries no writer.
	if oflags := uint16(u32(args, 4)); oflags&(oflagCreat|oflagExcl|oflagTrunc) != 0 {
		return ret(errNotcapable), nil
	}
	pathBytes, e := mRead(c, u32(args, 2), u32(args, 3))
	if e != errSuccess {
		return ret(e), nil
	}
	hostPath, e := h.resolveUnder(dir, string(pathBytes))
	if e != errSuccess {
		return ret(e), nil
	}
	f, err := os.Open(hostPath) // O_RDONLY
	if err != nil {
		return ret(errnoForPathError(err)), nil
	}
	fd := h.nextFD
	h.nextFD++
	h.fds[fd] = &fdEntry{file: f}
	if e := mWriteU32(c, u32(args, 8), fd); e != errSuccess {
		_ = f.Close()
		delete(h.fds, fd)
		return ret(e), nil
	}
	return ret(errSuccess), nil
}

// pathFilestatGet stats a path under a preopen. Signature: fd, flags, path, path_len, statptr → errno.
func (h *host) pathFilestatGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	dir := h.preopenAt(u32(args, 0))
	if dir == nil {
		return ret(errBadf), nil
	}
	pathBytes, e := mRead(c, u32(args, 2), u32(args, 3))
	if e != errSuccess {
		return ret(e), nil
	}
	hostPath, e := h.resolveUnder(dir, string(pathBytes))
	if e != errSuccess {
		return ret(e), nil
	}
	info, err := os.Stat(hostPath)
	if err != nil {
		return ret(errnoForPathError(err)), nil
	}
	return ret(writeFilestat(c, u32(args, 4), info)), nil
}

// fdFilestatGet stats an open fd. Signature: fd, statptr → errno.
func (h *host) fdFilestatGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	e := h.fds[u32(args, 0)]
	if e == nil {
		return ret(errBadf), nil
	}
	statPtr := u32(args, 1)
	switch {
	case e.file != nil:
		info, err := e.file.Stat()
		if err != nil {
			return ret(errnoForPathError(err)), nil
		}
		return ret(writeFilestat(c, statPtr, info)), nil
	case e.preopen != nil:
		info, err := os.Stat(e.preopen.hostRoot)
		if err != nil {
			return ret(errnoForPathError(err)), nil
		}
		return ret(writeFilestat(c, statPtr, info)), nil
	default:
		return ret(writeCharDeviceFilestat(c, statPtr)), nil // stdio
	}
}

// writeFilestat writes the 64-byte preview-1 `filestat`: dev u64@0, ino u64@8, filetype u8@16, nlink
// u64@24, size u64@32, atim u64@40, mtim u64@48, ctim u64@56. dev/ino are left zero — the guest reads
// filetype and size, which is what `os.ReadFile` needs.
func writeFilestat(c *interp.Caller, ptr uint32, info os.FileInfo) uint16 {
	ft := filetypeRegular
	if info.IsDir() {
		ft = filetypeDirectory
	}
	var b [64]byte
	b[16] = ft
	binary.LittleEndian.PutUint64(b[24:], 1)                   // nlink
	binary.LittleEndian.PutUint64(b[32:], uint64(info.Size())) // size
	ns := uint64(info.ModTime().UnixNano())
	binary.LittleEndian.PutUint64(b[40:], ns) // atim
	binary.LittleEndian.PutUint64(b[48:], ns) // mtim
	binary.LittleEndian.PutUint64(b[56:], ns) // ctim
	return mWrite(c, ptr, b[:])
}

// writeCharDeviceFilestat writes a filestat for a stdio stream — a character device of zero size.
func writeCharDeviceFilestat(c *interp.Caller, ptr uint32) uint16 {
	var b [64]byte
	b[16] = filetypeCharDevice
	binary.LittleEndian.PutUint64(b[24:], 1) // nlink
	return mWrite(c, ptr, b[:])
}
