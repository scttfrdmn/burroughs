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
	errNotempty   uint16 = 55 // directory not empty — `path_remove_directory`'s own refusal (ADR 0091)
	errNotcapable uint16 = 76 // capability insufficient — the escape and write-refusal channel
)

// preview-1 `filetype` values (`typenames.witx`).
const (
	filetypeCharDevice uint8 = 2
	filetypeDirectory  uint8 = 3
	filetypeRegular    uint8 = 4
	filetypeSymlink    uint8 = 7 // added for fd_readdir's d_type, from the same enum
)

// `oflags` bits for path_open (`typenames.witx`). Each implies creating or truncating — i.e. writing.
const (
	oflagCreat uint16 = 1 << 0
	oflagExcl  uint16 = 1 << 2
	oflagTrunc uint16 = 1 << 3
)

// fdEntry is one row of the per-run fd table. Exactly one shape is set: a stdio stream, a preopen
// directory, or an opened file.
//
// **Decision 0083's note that "read-only lives in the surface, not here" was true until ADR 0091 and is
// now half true.** It predicted that a write slice would set `file` from an `O_RDWR` open without
// reshaping the table, and that held — `file` is still just an `*os.File`. What it did not anticipate is
// `fd_pwrite`, whose capability question is about **where the fd came from** and not about a path: there
// is no path to resolve, so the surface has nothing to check and the answer has to be recorded here.
// Hence `writable` below, which is the engine's first capability carried by an fd's provenance.
type fdEntry struct {
	reader  io.Reader   // stdin (fd 0)
	writer  io.Writer   // stdout / stderr (fd 1, 2)
	preopen *preopenDir // a granted directory (fd 3..)
	file    *os.File    // an opened file

	// dirRoot is the confinement handle for a **directory** fd opened under a scratch grant, and nil
	// otherwise (ADR 0091).
	//
	// **It exists because preview-1's `path_*` calls take any directory fd, not only a preopen's.** The
	// first implementation resolved them through `preopenAt`, which answers nil for an opened directory,
	// so every call arrived as `errBadf`. It was found by running acceptance part A: `t.TempDir()`
	// succeeded, the write succeeded, and the *cleanup* failed — Go's `RemoveAll` opens the directory
	// and then calls `unlinkat` against that fd rather than against the preopen.
	//
	// It is a nested `os.Root` from the owning grant's root, not a path joined against it, so the
	// confinement is still `openat`'s and a directory fd cannot be a wider capability than the grant it
	// came from.
	dirRoot *os.Root

	// isDir records that `file` is a directory, established by the `Stat` at open time rather than by a
	// second one per call. It is what lets a directory fd stand in for a preopen in `path_*` calls.
	isDir bool

	// writable records that `file` was opened for writing under a **scratch** grant (ADR 0091).
	//
	// **It is a fact about the descriptor, not a policy**, which is why it lives beside `file` rather
	// than being re-derived. `fd_pwrite` is the consumer: it receives an fd and no path, so asking
	// "is this write permitted" means asking where the fd came from. Deriving that by comparing
	// `hostPath` against each writable preopen's root would be the path arithmetic `os.Root` exists to
	// replace, and it would answer a slightly different question — whether the file *sits* under a
	// scratch grant, rather than whether it was *opened* for writing through one.
	writable bool

	// hostPath is the resolved host path `file` was opened from, or "" for stdio and preopens.
	//
	// **It exists so that `fd_readdir` can keep this struct IMMUTABLE.** A directory enumeration is
	// resumable — the guest passes a cookie and expects to continue — and the cheap implementation is a
	// cursor field here. That would have made entries mutable and silently invalidated the fd table's
	// map-level lock, whose sufficiency rests on *"the table is mutable and its entries are not"*
	// (see the note beside `host.mu`). So `fd_readdir` re-reads the directory by path on each call and
	// slices by the cookie instead: O(n) per call rather than O(1), paid deliberately.
	//
	// That note named this exact slice as its precondition — *"a `fd_readdir` that cached a dirent
	// cursor in an entry would invalidate this reasoning while every accessor kept compiling"* — and
	// this is the field that keeps the promise rather than discovering the problem.
	hostPath string
}

// preopenDir is a directory the embedder granted: the name the guest sees, and the resolved host root
// it maps to. `hostRoot` is absolute with symlinks followed, because the escape check compares a
// resolved request against a resolved root (decision 0083, requirement 5).
type preopenDir struct {
	guestName string
	hostRoot  string

	// root is the confinement handle for a **writable** grant, and nil for a read-only one (ADR 0091).
	//
	// **Its nil-ness is the capability**, not a separate boolean: every write path asks this field for
	// its root and refuses when there is none, so "read-only" and "no way to write" are the same fact
	// rather than two that could disagree. A `writable bool` beside it would be a second copy of the
	// same claim, and this file has already paid for one of those.
	//
	// Opened once in [host.initFDs] and closed by [host.closeRoots], because `os.Root` holds a
	// directory handle: opening one per call would leak an fd per guest write.
	root *os.Root
}

// initFDs builds the fd table: stdio at 0/1/2, then a preopen dir per granted directory at 3.. Each
//
// **It writes the table directly rather than through the accessors, and that is the one place that is
// correct.** It runs inside [Run] before `_start` is invoked, so no guest code and therefore no second
// agent exists yet; taking `h.mu` here would be a lock with no second party. Stated because it is the
// sole exemption from "the table is reached through `fd`/`addFD`/`dropFD`" — an unexplained exemption
// is how a rule stops being auditable, and `TestFDTableIsReachedThroughAccessors` names this function
// explicitly rather than allowing any construction-looking site.
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
		dir := &preopenDir{guestName: guest, hostRoot: root}
		if p.Writable {
			// Opened from the ALREADY-RESOLVED root, so the handle and the containment root are the
			// same directory: opening it from `p.Host` would resolve symlinks a second time and could
			// land somewhere `hostRoot` does not name.
			r, rerr := os.OpenRoot(root)
			if rerr != nil {
				return fmt.Errorf("wasi: scratch preopen %q: %w", p.Host, rerr)
			}
			dir.root = r
		}
		h.fds[fd] = &fdEntry{preopen: dir}
		h.preopenFDs = append(h.preopenFDs, fd)
		fd++
	}
	h.nextFD = fd
	return nil
}

// preopenAt returns the preopen directory a dir fd names, or nil if the fd is not a preopen.
func (h *host) preopenAt(fd uint32) *preopenDir {
	if e := h.fd(fd); e != nil {
		return e.preopen
	}
	return nil
}

// dirAt answers the directory a `path_*` call should resolve against: a preopen, or a **directory the
// guest opened beneath one**.
//
// **preview-1's `path_*` calls take a `dirfd`, and nothing says it must be a preopen.** Resolving them
// through [host.preopenAt] alone made every call against an opened directory answer `errBadf`, and it was
// found by running acceptance part A rather than by reading the witx: `t.TempDir()` succeeded, the write
// succeeded, and the cleanup failed because Go's `RemoveAll` descends by opening each directory and
// operating through that fd.
//
// For a directory fd the returned value is **synthetic** — its `hostRoot` is the directory's own resolved
// path and its `root` the nested confinement handle, so containment is checked against the subdirectory.
// That is no weaker than checking against the grant: a path contained by a directory inside the grant is
// contained by the grant.
func (h *host) dirAt(fd uint32) *preopenDir {
	e := h.fd(fd)
	if e == nil {
		return nil
	}
	if e.preopen != nil {
		return e.preopen
	}
	if e.isDir && e.hostPath != "" {
		return &preopenDir{hostRoot: e.hostPath, root: e.dirRoot}
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
		case syscall.ENOTEMPTY:
			// ADR 0091: `path_remove_directory` removes an EMPTY directory, so a guest that tries a
			// non-empty one gets a specific answer rather than `errIO`. Found by running the two-grant
			// witness, whose scratch arm reported *"I/O error"* for a case preview-1 names.
			return errNotempty
		case syscall.EINVAL:
			return errInval
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
	dir := h.dirAt(u32(args, 0))
	if dir == nil {
		return ret(errBadf), nil
	}
	// Read-only is enforced on the *open mode*, not on `fs_rights_base`. Measured: Go's `os.Open`
	// sends `oflags=0` but a broad `fs_rights_base` (`0xff7febe`, `fd_write` included) — it requests
	// write rights speculatively even for a read — so refusing on those bits would refuse every read
	// (ADR 0083's 2026-09-09 append records this correction to requirement 1). A create/truncate/excl
	// open *is* a write and is refused here as a missing capability; a plain open is granted a
	// read-only fd, and an actual `fd_write` to it fails because a file entry carries no writer.
	pathBytes, e := mRead(c, u32(args, 2), u32(args, 3))
	if e != errSuccess {
		return ret(e), nil
	}
	guestPath := string(pathBytes)

	// **The write branch, ADR 0091.** Its condition is unchanged from decision 0083 — write intent is
	// read from the *open mode*, never from `fs_rights_base`, because Go's `os.Open` requests write
	// rights speculatively even for a read (0083's 2026-09-09 append). What changed is the consequence:
	// a read-only grant still answers `errNotcapable` verbatim, and a scratch grant proceeds.
	//
	// It does **not** go through `resolveUnder`. That function resolves with `EvalSymlinks`, which
	// requires the target to exist, so it cannot resolve the path of a file being created — the exact
	// obstacle ADR 0091 §4 names. `os.Root` confines with `openat` instead, so the guest path is handed
	// to it unresolved and the escape check is the standard library's.
	if oflags := uint16(u32(args, 4)); oflags&(oflagCreat|oflagExcl|oflagTrunc) != 0 {
		root, re := scratchRoot(dir)
		if re != errSuccess {
			return ret(re), nil
		}
		flag := os.O_RDWR | os.O_CREATE
		if oflags&oflagTrunc != 0 {
			flag |= os.O_TRUNC
		}
		if oflags&oflagExcl != 0 {
			flag |= os.O_EXCL
		}
		f, err := root.OpenFile(guestPath, flag, 0o600)
		if err != nil {
			return ret(errnoForPathError(err)), nil
		}
		// `hostPath` is informational for a regular file (`fd_readdir` is the consumer that needs it),
		// and it is joined rather than resolved for the same reason the open was: the file may have just
		// been created, and a resolver that requires existence is the wrong tool on this path.
		fd := h.addFD(&fdEntry{file: f, hostPath: filepath.Join(dir.hostRoot, guestPath), writable: true})
		if we := mWriteU32(c, u32(args, 8), fd); we != errSuccess {
			_ = f.Close()
			h.dropFD(fd)
			return ret(we), nil
		}
		return ret(errSuccess), nil
	}

	hostPath, e := h.resolveUnder(dir, guestPath)
	if e != errSuccess {
		return ret(e), nil
	}
	f, err := os.Open(hostPath) // O_RDONLY
	if err != nil {
		return ret(errnoForPathError(err)), nil
	}
	fi, serr := f.Stat()
	entry := &fdEntry{file: f, hostPath: hostPath, isDir: serr == nil && fi.IsDir()}
	attachDirRoot(entry, dir, guestPath)
	fd := h.addFD(entry)
	if e := mWriteU32(c, u32(args, 8), fd); e != errSuccess {
		_ = f.Close()
		h.dropFD(fd)
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
	e := h.fd(u32(args, 0))
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

// fdPread reads at an absolute offset without moving the fd's own offset — preview 1's `fd_pread`.
//
// **Implemented because a guest's refusal FAILED, not because it is imported.** A released-go1.27.1
// `archive/zip` test binary calls it 38 times, and refusing it makes one of that package's own tests fail
// with `read testdata/subdir.zip: Not implemented on wasip1`. The obligation was measured per FAILURE
// rather than per call, which is what separates this from the seven functions below.
//
// The failing test is named in #816 and in ADR 0083's append, not here. A bare Test-prefixed identifier in
// this tree's Go comments means *a control in this repository*, and the citation control is right to refuse
// one that names a test in someone else's — so the cause is removed rather than the check widened. (This
// sentence originally spelled the placeholder as an identifier and tripped the control while explaining
// it, which is the second time this session that describing a pattern in the pattern's own form was
// itself an instance of it.)
//
// Read-side, so ADR 0083's read-only decision is untouched: this reads a descriptor the capability model
// already granted, at an offset, and grants nothing new.
func (h *host) fdPread(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	e := h.fd(u32(args, 0))
	if e == nil || e.file == nil {
		// stdio and preopen dirs have no absolute offset to read at. EBADF rather than ENOTCAPABLE:
		// the fd is not a seekable file, which is a property of the descriptor, not of a permission.
		return ret(errBadf), nil
	}
	iovs, iovsLen := u32(args, 1), u32(args, 2)
	offset := args[3].Int64()
	nreadPtr := u32(args, 4)
	if offset < 0 {
		return ret(errInval), nil
	}
	var total uint32
	at := offset
	for i := range iovsLen {
		base := iovs + i*8
		buf, err := mReadU32(c, base)
		if err != errSuccess {
			return ret(err), nil
		}
		n, err := mReadU32(c, base+4)
		if err != errSuccess {
			return ret(err), nil
		}
		if n == 0 {
			continue
		}
		tmp := make([]byte, n)
		got, rerr := e.file.ReadAt(tmp, at)
		if got > 0 {
			if err = mWrite(c, buf, tmp[:got]); err != errSuccess {
				return ret(err), nil
			}
			total += uint32(got)
			at += int64(got)
		}
		if rerr != nil {
			// io.EOF is not an error to the guest: a short read at or past the end is `nread` < asked
			// with ESUCCESS, which is how preview 1 spells end-of-file. Anything else is EIO.
			if errors.Is(rerr, io.EOF) {
				break
			}
			return ret(errIO), nil
		}
		if uint32(got) < n {
			break // short read: do not continue into the remaining iovecs
		}
	}
	return ret(mWriteU32(c, nreadPtr, total)), nil
}

// fdReaddir enumerates a directory — preview 1's `fd_readdir`.
//
// **Implemented because a guest's refusal FAILED**: `archive/zip`'s fuzz target dies with
// `readdirent testdata: Not implemented on wasip1` (named in #816, for the reason given at `fdPread`).
// ADR 0083 deferred "directory enumeration" by name, and this is that deferral's consumer arriving.
//
// **Stateless by construction, and that is the design point.** The cookie is an index into the
// directory's entries, re-read from the host on every call, so no cursor lives in the `fdEntry` and the
// fd table's entries stay immutable (see `fdEntry.hostPath`). The cost is re-reading per call; the
// benefit is that the lock reasoning recorded one slice earlier remains true.
//
// Read-side: it lists what the granted directory contains and opens nothing.
func (h *host) fdReaddir(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	e := h.fd(u32(args, 0))
	if e == nil {
		return ret(errBadf), nil
	}
	dir := e.hostPath
	if dir == "" && e.preopen != nil {
		dir = e.preopen.hostRoot
	}
	if dir == "" {
		return ret(errBadf), nil
	}
	buf, bufLen := u32(args, 1), u32(args, 2)
	cookie := uint64(args[3].Int64())
	bufusedPtr := u32(args, 4)

	ents, rerr := os.ReadDir(dir)
	if rerr != nil {
		return ret(errnoForPathError(rerr)), nil
	}
	// A cookie past the end is not an error: it is an exhausted enumeration, which the guest reads as
	// `bufused == 0`. Returning EINVAL here would make a correct final call look like a failure.
	if cookie > uint64(len(ents)) {
		return ret(mWriteU32(c, bufusedPtr, 0)), nil
	}

	var used uint32
	for i := int(cookie); i < len(ents); i++ {
		name := []byte(ents[i].Name())
		// dirent: d_next u64, d_ino u64, d_namlen u32, d_type u8, then 3 bytes of padding = 24 bytes,
		// followed by the name. `d_next` is the cookie the guest passes to continue AFTER this entry.
		const direntSize = 24
		if used+direntSize > bufLen {
			break // the header itself does not fit: stop, and the guest will call again
		}
		hdr := make([]byte, direntSize)
		binary.LittleEndian.PutUint64(hdr[0:8], uint64(i)+1)
		binary.LittleEndian.PutUint64(hdr[8:16], 0) // d_ino: 0, which preview 1 permits when unknown
		binary.LittleEndian.PutUint32(hdr[16:20], uint32(len(name)))
		hdr[20] = direntType(ents[i])
		if err := mWrite(c, buf+used, hdr); err != errSuccess {
			return ret(err), nil
		}
		used += direntSize
		// **A TRUNCATED NAME IS CORRECT HERE, not an error.** preview 1 lets the host write a partial
		// final entry; the guest detects `bufused == buf_len` and retries with a larger buffer. Writing
		// nothing instead would make a guest whose buffer is smaller than one name loop forever.
		write := uint32(len(name))
		if used+write > bufLen {
			write = bufLen - used
		}
		if write > 0 {
			if err := mWrite(c, buf+used, name[:write]); err != errSuccess {
				return ret(err), nil
			}
			used += write
		}
		if used >= bufLen {
			break
		}
	}
	return ret(mWriteU32(c, bufusedPtr, used)), nil
}

// direntType maps a host dir entry to preview 1's `filetype` byte, using the enum constants this file
// already names from `typenames.witx` rather than raw numbers — the file's own rule, since a wrong
// filetype makes a guest take a different branch silently.
func direntType(d os.DirEntry) byte {
	switch {
	case d.IsDir():
		return filetypeDirectory
	case d.Type()&os.ModeSymlink != 0:
		return filetypeSymlink
	case d.Type().IsRegular():
		return filetypeRegular
	default:
		return 0 // `unknown` in the enum: honest rather than guessed
	}
}

// closeRoots releases the directory handle each writable preopen holds (ADR 0091).
//
// **It is on the host rather than deferred per call**, because `os.Root` is opened once per grant in
// [host.initFDs]: a handle opened and closed around each guest write would cost an `openat` per call and
// would give a different confinement root each time, which is a worse property than the fd it saves.
//
// Errors are dropped deliberately and that is not a shrug: this runs while the instance is being torn
// down, the handles are about to be reclaimed by process exit in the CLI's case, and there is no caller
// left with a decision to make about a failed `close`. What would be wrong is dropping an error from
// the *opening* side, which `initFDs` returns.
func (h *host) closeRoots() {
	// **Through `h.fd` rather than `h.fds`**, which `TestFDTableIsReachedThroughAccessors` requires and
	// which caught the first version of this function. `initFDs` is the table's one direct-access exemption
	// and it earns that by running before any guest code exists; this runs at teardown, when a guest agent
	// may still be unwinding, so it has no such claim and takes the lock the accessor takes.
	for _, fd := range h.preopenFDs {
		e := h.fd(fd)
		if e == nil || e.preopen == nil || e.preopen.root == nil {
			continue
		}
		_ = e.preopen.root.Close()
	}
}

// scratchRoot answers the confinement handle for a write to `dir`, or a refusal.
//
// **The refusal is `ENOTCAPABLE`, never `ENOSYS`.** After ADR 0091 these functions *are* implemented, so
// a guest that reaches one under a read-only grant is being told by the capability model that its grant
// does not permit this — which is exactly the distinction ADR 0083 drew and the reason the 21 remaining
// deferrals can still honestly answer `ENOSYS`. Getting this backwards would make a policy look like a
// gap in the engine.
func scratchRoot(dir *preopenDir) (root *os.Root, errno uint16) {
	if dir == nil {
		return nil, errBadf
	}
	if dir.root == nil {
		return nil, errNotcapable
	}
	return dir.root, errSuccess
}

// pathTarget reads a `(fd, path_ptr, path_len)` triple and answers the scratch root it must act through,
// with the guest's path unresolved.
//
// **Unresolved is the point.** Every write below either creates a path that does not exist yet or removes
// one that is about to stop existing, and ADR 0083's `resolveUnder` resolves with `EvalSymlinks`, which
// requires the target to be there. So these three functions never touch it: the guest path goes to
// `os.Root` as written and the containment check is `openat`'s.
func (h *host) pathTarget(c *interp.Caller, args []interp.Value) (root *os.Root, path string, errno uint16) {
	root, e := h.dirRootAt(u32(args, 0))
	if e != errSuccess {
		return nil, "", e
	}
	pathBytes, e := mRead(c, u32(args, 1), u32(args, 2))
	if e != errSuccess {
		return nil, "", e
	}
	return root, string(pathBytes), errSuccess
}

// dirRootAt answers the scratch confinement root a directory fd acts through — a preopen's, or a nested
// one belonging to a directory the guest opened beneath it.
//
// **Both cases exist and the second was missed.** `path_*` takes a `dirfd`, and preview-1 does not say it
// must be a preopen; Go's `RemoveAll` opens a directory and unlinks through the resulting fd. Answering
// only the preopen case turned every such call into `errBadf` — the descriptor blamed for a lookup this
// function did not perform.
func (h *host) dirRootAt(fd uint32) (root *os.Root, errno uint16) {
	dir := h.dirAt(fd)
	if dir == nil {
		// Not a directory at all — stdio, or a regular file. The descriptor is genuinely wrong for a
		// `path_*` call, which is `errBadf` and not a capability question.
		return nil, errBadf
	}
	return scratchRoot(dir)
}

// pathCreateDirectory creates a directory under a scratch grant (ADR 0091). `t.TempDir()`'s first call,
// and the function whose refusal made every `os` test fail in *setup* rather than in a test body.
func (h *host) pathCreateDirectory(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	root, path, e := h.pathTarget(c, args)
	if e != errSuccess {
		return ret(e), nil
	}
	if err := root.Mkdir(path, 0o700); err != nil {
		return ret(errnoForPathError(err)), nil
	}
	return ret(errSuccess), nil
}

// pathUnlinkFile removes a **file** under a scratch grant, and refuses a directory with `errIsdir`.
//
// **The kind check is a check-then-use and its race is confined, which is why it is acceptable here.**
// `os.Root` offers one `Remove` for both kinds, while preview-1 gives the guest two calls with different
// errnos, so the kind has to be established separately. If the entry changes kind between the `Lstat` and
// the `Remove`, the consequence is that the wrong *kind* of entry is removed **inside the grant the guest
// already holds** — not an escape, because `Remove` does its own containment. Contrast
// `path_filestat_set_times`, refused precisely because its race would reach outside (ADR 0091 amendment 1).
func (h *host) pathUnlinkFile(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	root, path, e := h.pathTarget(c, args)
	if e != errSuccess {
		return ret(e), nil
	}
	if fi, err := root.Lstat(path); err == nil && fi.IsDir() {
		return ret(errIsdir), nil
	}
	if err := root.Remove(path); err != nil {
		return ret(errnoForPathError(err)), nil
	}
	return ret(errSuccess), nil
}

// pathRemoveDirectory removes an empty **directory** under a scratch grant, refusing a non-directory with
// `errNotdir`. `t.TempDir()` registers a cleanup, so this runs on every test that used one — which is why
// it is in the measured set and `path_rename` is not.
func (h *host) pathRemoveDirectory(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	root, path, e := h.pathTarget(c, args)
	if e != errSuccess {
		return ret(e), nil
	}
	fi, err := root.Lstat(path)
	if err != nil {
		return ret(errnoForPathError(err)), nil
	}
	if !fi.IsDir() {
		return ret(errNotdir), nil
	}
	// `Remove`, not `RemoveAll`: preview-1's `path_remove_directory` removes an *empty* directory, and
	// a guest that asked to remove one directory must not have a subtree deleted on its behalf.
	if err := root.Remove(path); err != nil {
		return ret(errnoForPathError(err)), nil
	}
	return ret(errSuccess), nil
}

// fdPwrite writes at an absolute offset, and it is the engine's first capability decided by an fd's
// PROVENANCE rather than by a path (ADR 0091 amendment 1).
//
// # Why there is no root here
//
// It receives an fd and an offset. There is no path to resolve, no directory to confine against, and
// `os.Root` is irrelevant: the file was already confined when it was opened. The only question left is
// whether the fd came from a writable grant, which `path_open` recorded on the entry.
//
// # The refusal is `errNotcapable`, and its arm is separate
//
// A read-only `--dir` fd, or a file opened for reading under a scratch grant, is refused as a **missing
// capability** — not `errBadf`, which would say the descriptor is wrong, and not `errNosys`, which would
// say the engine does not implement this. After this slice it does. The refusal has its own witness,
// because the permit path working is no evidence at all about it: an `fd_pwrite` that ignored `writable`
// would pass every permitted case.
func (h *host) fdPwrite(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	e := h.fd(u32(args, 0))
	if e == nil || e.file == nil {
		// stdio and preopen dirs have no absolute offset to write at — a property of the descriptor.
		return ret(errBadf), nil
	}
	if !e.writable {
		return ret(errNotcapable), nil
	}
	iovs, iovsLen := u32(args, 1), u32(args, 2)
	offset := args[3].Int64()
	nwrittenPtr := u32(args, 4)
	if offset < 0 {
		return ret(errInval), nil
	}
	var total uint32
	at := offset
	for i := range iovsLen {
		base := iovs + i*8
		buf, err := mReadU32(c, base)
		if err != errSuccess {
			return ret(err), nil
		}
		n, err := mReadU32(c, base+4)
		if err != errSuccess {
			return ret(err), nil
		}
		if n == 0 {
			continue
		}
		data, err := mRead(c, buf, n)
		if err != errSuccess {
			return ret(err), nil
		}
		// **The payload's length is read back from the write, not assumed from the request.** A short
		// write that reported success would make `nwritten` a claim the host never checked.
		wrote, werr := e.file.WriteAt(data, at)
		total += uint32(wrote)
		at += int64(wrote)
		if werr != nil {
			if total == 0 {
				return ret(errnoForPathError(werr)), nil
			}
			break
		}
	}
	if e := mWriteU32(c, nwrittenPtr, total); e != errSuccess {
		return ret(e), nil
	}
	return ret(errSuccess), nil
}

// attachDirRoot gives a newly opened DIRECTORY its own confinement handle, nested inside the grant's.
//
// # Why a directory fd needs one at all
//
// preview-1's `path_*` calls take a `dirfd`, and nothing requires it to be a preopen: Go's `RemoveAll`
// descends by opening each directory and operating through the resulting fd. Once those calls resolve
// against an arbitrary directory fd, **that fd is a confinement boundary of its own**, and it has to inherit
// the grant's rather than invent one.
//
// # It is a NESTED root, not a fresh one at the same path
//
// `dir.root.OpenRoot(guestPath)` derives the handle from the grant's own root, so the child cannot reach
// anywhere the parent could not. A fresh `os.OpenRoot(hostPath)` would confine to the same directory today
// and would be resolving that path outside any root to get there — the difference is invisible while every
// path is well-behaved and is exactly the difference a hostile path is looking for.
//
// # A failure to nest is not fatal to the open
//
// The guest asked to open a directory and that succeeded. Its later write calls then answer `errNotcapable`,
// which is the honest report: this host could not hand it a writable handle. Refusing the open instead would
// turn a missing capability into a missing file.
//
// **Extracted so that the confinement arms exercise this function rather than a copy of it.** A test that
// rebuilt the attachment inline would be asserting against its own reimplementation, which is the shape where
// a witness and its subject drift apart silently.
func attachDirRoot(entry *fdEntry, dir *preopenDir, guestPath string) {
	if dir == nil || dir.root == nil || !entry.isDir {
		return
	}
	if nested, err := dir.root.OpenRoot(guestPath); err == nil {
		entry.dirRoot = nested
	}
}

// fdCountForTest reports the number of entries in the fd table.
//
// **Its consumer is a leak assertion, not a curiosity.** A descriptor opened and never closed, or closed
// twice, does not appear as a failed call — the call that leaked it succeeded. The only channel that sees it is
// the table's size before and after, which is why this exists rather than a test counting its own opens.
func (h *host) fdCountForTest() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.fds)
}
