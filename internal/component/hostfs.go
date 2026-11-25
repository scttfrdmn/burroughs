// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// WASI 0.3's filesystem (#1374's E1a, ADR 0133): `wasi:filesystem/preopens@0.3`'s `get-directories` and
// `wasi:filesystem/types@0.3`'s `descriptor`, with `open-at`, `stat`, `stat-at`, `get-flags`, `get-type`,
// `read-via-stream` and `write-via-stream`. Every other method of `descriptor` meets the refusing stub, by name:
// `read-directory` is E1b's, the mutations E1c's.
//
// # Confinement is os.Root's
//
// Every grant is opened as an `os.Root`, read-only ones too, and every path a guest passes is handed to it as
// written, so `..` and symlinks are resolved by `openat` one step at a time and cannot leave the base directory.
// The one path this host refuses itself is an absolute one, before any call. Read-only is the descriptor's flags:
// `open-at` refuses a write, a create or a truncate on a base without `mutate-directory`, as the WIT says.
//
// # Files are shared, and counted
//
// A file descriptor and every stream read from or written to it share one `*os.File`, which closes when the last
// of them goes (`fsFile`, sockConn's shape). The instance's files are in its registry (`hostFS`), so Close
// reaches one nobody finished. A directory descriptor holds a nested `os.Root`, which its destructor closes.
//
// # Locks
//
// `asyncHandles.mu` → `hostFS.mu`, a leaf. A resource lend is taken and released with neither held.

// Preopen is one directory a component is granted (ADR 0133): Host on the host, Guest the name get-directories
// gives it, and Writable for `mutate-directory`. A Guest left empty is the host path, as preview 1's is.
type Preopen struct {
	Host     string
	Guest    string
	Writable bool
}

// descriptor-flags' bits, in the WIT's order, which is the flags' flat encoding.
const (
	fsFlagRead      = 1 << 0
	fsFlagWrite     = 1 << 1
	fsFlagMutateDir = 1 << 5
	fsFlagsKnown    = 1<<6 - 1
)

// path-flags' and open-flags' bits.
const (
	pathFollow    = 1 << 0
	openCreate    = 1 << 0
	openDirectory = 1 << 1
	openExclusive = 1 << 2
	openTruncate  = 1 << 3
)

// fsEscapeMessage is the text of Go's unexported `errPathEscapes`, which os.Root returns for a path that leaves
// its root. It is matched by string because the error is not exported; TestTheEscapeErrorIsTheToolchainsOwn pins it
// against the toolchain in go.mod.
const fsEscapeMessage = "path escapes from parent"

// checkPreopens refuses a grant that does not name an existing directory, at instantiate, as preview 1's initFDs
// fails the run: the embedder named something the guest cannot be given.
//
// NEUTER, run: return nil here, and TestAPreopenThatIsNotADirectoryIsRefusedAtInstantiate's file and absent preopens
// both instantiate.
func (h *Host) checkPreopens() error {
	for _, p := range h.Preopens {
		info, err := os.Stat(p.Host)
		if err != nil {
			return fmt.Errorf("component: preopen %q: %w", p.Host, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("component: preopen %q is not a directory", p.Host)
		}
	}
	return nil
}

// fsFile is one open file and its reference count, guarded by the registry's lock.
type fsFile struct {
	f      *os.File
	refs   int
	closed bool
	// writers counts write-via-stream writer goroutines live on the file. leftToWriter is Close's stop having left
	// the file's close to them, for after their flush (ADR 0127's reason, ADR 0133).
	writers      int
	leftToWriter bool
}

// hostFS is one component instance's open files and its file goroutines (ADR 0133), in its hostResources beside
// the sockets, so a nested instance has its own.
type hostFS struct {
	mu      sync.Mutex
	files   map[*fsFile]struct{}
	stopped bool
	wg      sync.WaitGroup
}

func newHostFS() *hostFS { return &hostFS{files: map[*fsFile]struct{}{}} }

// adopt registers a newly opened file with one reference, its descriptor's. After stop it is closed instead.
func (r *hostFS) adopt(f *os.File) (*fsFile, bool) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		_ = f.Close() // the instance is closing; nobody reads the close's error
		return nil, false
	}
	ff := &fsFile{f: f, refs: 1}
	r.files[ff] = struct{}{}
	r.mu.Unlock()
	return ff, true
}

// retain takes another reference to a live file, a stream's.
func (r *hostFS) retain(ff *fsFile) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ff.closed {
		return false
	}
	ff.refs++
	return true
}

// release drops one reference, and the last closes the file, with the lock released.
//
// NEUTER, run: close at every release, and TestAStreamOutlivesItsDroppedDescriptor's read after the descriptor's drop
// reads "file already closed"; TestFSDescriptorMatchesWasmtime's write-at reads 0x0000f000, nothing read back once the
// write's stream has released the file, and write-content and create fail alike.
func (r *hostFS) release(ff *fsFile) {
	r.mu.Lock()
	ff.refs--
	last := ff.refs <= 0 && !ff.closed
	if last {
		ff.closed = true
		delete(r.files, ff)
	}
	r.mu.Unlock()
	if last {
		_ = ff.f.Close() // a close's error has no reader: every holder has gone
	}
}

// track counts a file goroutine about to start, a read-via-stream producer's Read, and returns the call that marks it
// ended. After stop none is counted and none may start.
func (r *hostFS) track() (ended func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil, false
	}
	r.wg.Add(1)
	return r.wg.Done, true
}

// trackWriter is track for a write-via-stream writer over ff, which also marks the file as having a live writer, so
// stop leaves its close to the writer. The call it returns ends the goroutine, closing the file first if stop left
// it so. The caller may hold asyncHandles.mu, the permitted order.
func (r *hostFS) trackWriter(ff *fsFile) (ended func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil, false
	}
	r.wg.Add(1)
	ff.writers++
	return func() {
		r.mu.Lock()
		ff.writers--
		closeNow := ff.leftToWriter && ff.writers == 0
		r.mu.Unlock()
		if closeNow {
			_ = ff.f.Close() // the instance is closing, and the flush has ended; nobody reads the close's error
		}
		r.wg.Done()
	}, true
}

// stop is Close's: no file goroutine starts after it, and every file without a live writer is closed, which ends a
// Read in flight on a pollable one. A file with a writer is closed by that writer once it has written what its
// buffer holds. wait then waits them out.
//
// NEUTER, run: return before closing the files, and TestCloseClosesTheFilesTheGuestLeftOpen's held file still answers
// Stat after the stop. Every fixture row passes under it: its descriptors are destroyed by closeAll.
func (r *hostFS) stop() {
	r.mu.Lock()
	r.stopped = true
	shut := make([]*fsFile, 0, len(r.files))
	for ff := range r.files {
		ff.closed = true
		if ff.writers > 0 {
			ff.leftToWriter = true
			continue
		}
		shut = append(shut, ff)
	}
	r.files = map[*fsFile]struct{}{}
	r.mu.Unlock()
	for _, ff := range shut {
		_ = ff.f.Close() // the instance is closing
	}
}

// wait returns once every file goroutine has ended.
func (r *hostFS) wait() { r.wg.Wait() }

// fsDescriptor is the object behind a `descriptor` handle: a directory's nested root or a file, its flags, and
// where it is, for E1c's rename-at and link-at, which resolve across two descriptors of one grant.
type fsDescriptor struct {
	fs    *hostFS
	grant int    // the index of its preopen in Host.Preopens
	path  string // its path from the grant's root, slash-separated; "." is the grant itself
	flags uint8
	dir   *os.Root // a directory's, or nil
	file  *fsFile  // a file's, or nil
}

// destroyFSDescriptor is the handle's destructor (ADR 0133): a directory's root is closed, and a file's reference
// released, which closes it unless a stream holds it.
//
// NEUTER, run: return nil without either, and TestADroppedDescriptorClosesItsFile finds the file and the directory's
// root still open after their drops and the file still in the registry; TestAStreamOutlivesItsDroppedDescriptor's
// last release leaves the file open, the descriptor's reference never released.
func destroyFSDescriptor(obj any) error {
	d, ok := obj.(*fsDescriptor)
	if !ok {
		return fmt.Errorf("component: a descriptor handle held a %T", obj)
	}
	if d.dir != nil {
		return d.dir.Close()
	}
	if d.file != nil {
		d.fs.release(d.file)
	}
	return nil
}

// The WIT's types, built through the codec's constructors.

// fsErrorCodeLabels are `wasi:filesystem/types@0.3`'s `error-code` cases, in order; the last, `other`, carries an
// `option<string>`.
var fsErrorCodeLabels = []string{
	"access", "already", "bad-descriptor", "busy", "deadlock", "quota", "exist", "file-too-large",
	"illegal-byte-sequence", "in-progress", "interrupted", "invalid", "io", "is-directory", "loop", "too-many-links",
	"message-size", "name-too-long", "no-device", "no-entry", "no-lock", "insufficient-memory", "insufficient-space",
	"not-directory", "not-empty", "not-recoverable", "unsupported", "no-tty", "no-such-device", "overflow",
	"not-permitted", "pipe", "read-only", "invalid-seek", "text-file-busy", "cross-device", "other",
}

// fsLabelledVariant is a variant of labels whose last case, `other`, carries an option<string>: error-code's shape,
// and descriptor-type's.
func fsLabelledVariant(labels []string) canon.Type {
	other := canon.OptionType(canon.Type{Kind: canon.KindString})
	cases := make([]canon.Case, len(labels))
	for i, l := range labels {
		cases[i] = canon.Case{Name: l}
	}
	cases[len(cases)-1].Type = &other
	return canon.VariantType(cases...)
}

func fsErrorCodeType() canon.Type { return fsLabelledVariant(fsErrorCodeLabels) }

// fsResultType is `result<ok, error-code>`; nil ok is `result<_, error-code>`, the streams' future payload.
func fsResultType(ok *canon.Type) canon.Type {
	ec := fsErrorCodeType()
	return canon.ResultType(ok, &ec)
}

func fsFuturePayloadType() (canon.Type, error) { return fsResultType(nil), nil }

// fsLabelledValue is label's value at a fsLabelledVariant, `other` carrying none: a string would be lowered through
// the guest's realloc, and a result store may run where none may be called (ADR 0133).
func fsLabelledValue(t canon.Type, label string) canon.Value {
	var v canon.Value
	var err error
	if label == "other" {
		none, nerr := canon.None(canon.OptionType(canon.Type{Kind: canon.KindString}))
		if nerr != nil {
			panic(nerr) // unreachable: a constant option type
		}
		v, err = canon.Variant(t, "other", &none)
	} else {
		v, err = canon.Variant(t, label, nil)
	}
	if err != nil {
		panic(err) // unreachable: every label passed is one of the type's
	}
	return v
}

// fsErr is `result::err(code)` at `result<ok, error-code>`.
func fsErr(ok *canon.Type, label string) canon.Value {
	code := fsLabelledValue(fsErrorCodeType(), label)
	v, err := canon.Variant(fsResultType(ok), "error", &code)
	if err != nil {
		panic(err) // unreachable: a constant type
	}
	return v
}

// fsOK is `result::ok` at `result<ok, error-code>` with payload v, or none for `result<_, error-code>`.
func fsOK(ok *canon.Type, v *canon.Value) canon.Value {
	r, err := canon.Variant(fsResultType(ok), "ok", v)
	if err != nil {
		panic(err) // unreachable: every caller passes a payload of its ok type
	}
	return r
}

// fsErrResult is a failed Read's or Write's future value.
func fsErrResult(err error) canon.Value { return fsErr(nil, fsErrorLabel(err)) }

// fsTypeLabels are `descriptor-type`'s cases, in order.
var fsTypeLabels = []string{
	"block-device", "character-device", "directory", "fifo", "symbolic-link", "regular-file", "socket", "other",
}

func fsDescriptorTypeType() canon.Type { return fsLabelledVariant(fsTypeLabels) }

// fsTypeLabel is a file mode's `descriptor-type` case. `other` is never answered with a string.
func fsTypeLabel(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "directory"
	case m&fs.ModeSymlink != 0:
		return "symbolic-link"
	case m.IsRegular():
		return "regular-file"
	case m&fs.ModeNamedPipe != 0:
		return "fifo"
	case m&fs.ModeSocket != 0:
		return "socket"
	case m&fs.ModeCharDevice != 0:
		return "character-device"
	case m&fs.ModeDevice != 0:
		return "block-device"
	default:
		return "other"
	}
}

var fsFlagLabels = []string{
	"read", "write", "file-integrity-sync", "data-integrity-sync", "requested-write-sync", "mutate-directory",
}

func fsDescriptorFlagsType() canon.Type {
	t, err := canon.FlagsType(fsFlagLabels...)
	if err != nil {
		panic(err) // unreachable: constant labels
	}
	return t
}

func fsStatType() canon.Type {
	u64 := canon.Type{Kind: canon.KindU64}
	ts := canon.OptionType(systemInstantType())
	return canon.RecordType(
		canon.Field{Name: "type", Type: fsDescriptorTypeType()},
		canon.Field{Name: "link-count", Type: u64},
		canon.Field{Name: "size", Type: u64},
		canon.Field{Name: "data-access-timestamp", Type: ts},
		canon.Field{Name: "data-modification-timestamp", Type: ts},
		canon.Field{Name: "status-change-timestamp", Type: ts},
	)
}

// fsInstant is `some(instant)` for t, or `none` for the zero time.
func fsInstant(t time.Time) (canon.Value, error) {
	ot := canon.OptionType(systemInstantType())
	if t.IsZero() {
		return canon.None(ot)
	}
	inst, err := canon.Record(systemInstantType(), map[string]canon.Value{
		"seconds": canon.S64(t.Unix()), "nanoseconds": canon.U32(uint32(t.Nanosecond())),
	})
	if err != nil {
		return canon.Value{}, err
	}
	return canon.Some(ot, inst)
}

// fsStatValue is a `descriptor-stat` for fi. The modification time is ModTime; the access and status-change times
// and the link count are the platform's stat record's where statExtra reads one, and none, none and 1 elsewhere.
func fsStatValue(fi os.FileInfo) (canon.Value, error) {
	atime, ctime, nlink, ok := statExtra(fi)
	if !ok {
		nlink = 1
	}
	fields := map[string]canon.Value{
		"type":       fsLabelledValue(fsDescriptorTypeType(), fsTypeLabel(fi.Mode())),
		"link-count": canon.U64(nlink),
		"size":       canon.U64(uint64(max(fi.Size(), 0))),
	}
	for name, t := range map[string]time.Time{
		"data-access-timestamp": atime, "data-modification-timestamp": fi.ModTime(), "status-change-timestamp": ctime,
	} {
		v, err := fsInstant(t)
		if err != nil {
			return canon.Value{}, err
		}
		fields[name] = v
	}
	return canon.Record(fsStatType(), fields)
}

// fsErrorLabel maps a Go filesystem error to its `error-code` case (ADR 0133), in sockErrorLabel's style: an escape
// from the base directory first, then the errno table, then the os sentinels, and `other` for anything else.
//
// NEUTER, run: drop the escape arm, and TestFSDescriptorMatchesWasmtime's escapes row reads 0xa4a49ea4, `other` (0xa4)
// for the three paths os.Root refuses and not-permitted only for /etc, which fsAbsolute refuses first; its nested row
// reads 0x02a40000; TestTheEscapeErrorIsTheToolchainsOwn's label is `other`.
func fsErrorLabel(err error) string {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if e.Error() == fsEscapeMessage {
			return "not-permitted"
		}
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if l, ok := fsErrnoLabels[errno]; ok {
			return l
		}
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "no-entry"
	case errors.Is(err, os.ErrExist):
		return "exist"
	case errors.Is(err, os.ErrPermission):
		return "access"
	case errors.Is(err, os.ErrClosed):
		return "bad-descriptor"
	default:
		return "other"
	}
}

// fsErrnoLabels is the POSIX errnos' mapping onto error-code. Windows reports most of these through its own codes,
// which the os sentinels in fsErrorLabel catch.
var fsErrnoLabels = map[syscall.Errno]string{
	syscall.EACCES: "access", syscall.EALREADY: "already", syscall.EBADF: "bad-descriptor", syscall.EBUSY: "busy",
	syscall.EDEADLK: "deadlock", syscall.EDQUOT: "quota", syscall.EEXIST: "exist", syscall.EFBIG: "file-too-large",
	syscall.EILSEQ: "illegal-byte-sequence", syscall.EINPROGRESS: "in-progress", syscall.EINTR: "interrupted",
	syscall.EINVAL: "invalid", syscall.EIO: "io", syscall.EISDIR: "is-directory", syscall.ELOOP: "loop",
	syscall.EMLINK: "too-many-links", syscall.EMSGSIZE: "message-size", syscall.ENAMETOOLONG: "name-too-long",
	syscall.ENODEV: "no-device", syscall.ENOENT: "no-entry", syscall.ENOLCK: "no-lock",
	syscall.ENOMEM: "insufficient-memory", syscall.ENOSPC: "insufficient-space", syscall.ENOTDIR: "not-directory",
	syscall.ENOTEMPTY: "not-empty", syscall.ENOTSUP: "unsupported", syscall.ENOTTY: "no-tty",
	syscall.ENXIO: "no-such-device", syscall.EOVERFLOW: "overflow", syscall.EPERM: "not-permitted",
	syscall.EPIPE: "pipe", syscall.EROFS: "read-only", syscall.ESPIPE: "invalid-seek", syscall.ETXTBSY: "text-file-busy",
	syscall.EXDEV: "cross-device",
}

// fsAbsolute reports a path the WIT refuses before resolving it: one that starts with a separator, or names a
// volume (ADR 0133). os.Root refuses these too, with its unexported escape error; this host refuses them itself.
//
// NEUTER, run: return false, and TestAnAbsolutePathIsRefusedBeforeTheCall's / and /etc rows fail. Every fixture row
// passes under it, since os.Root refuses /etc with the escape error, which maps to the same not-permitted.
func fsAbsolute(p string) bool {
	return p != "" && (os.IsPathSeparator(p[0]) || filepath.IsAbs(p) || filepath.VolumeName(p) != "")
}

// liftPath lifts a path argument's (ptr, len) words as a string; a malformed one is the guest's trap.
func liftPath(c *interp.CanonCaller, op string, ptr, n interp.Value) (string, error) {
	sv, err := canon.LoadStringFromRange(guestHeap{c}, int(uint32(ptr.Bits)), int(uint32(n.Bits)))
	if err != nil {
		return "", &interp.Trap{Reason: fmt.Sprintf("descriptor.%s: lifting the path: %v", op, err)}
	}
	s, _ := sv.Str() // LoadStringFromRange returns a string, so the kind check cannot fail
	return s, nil
}

// openAt is `open-at` on d (ADR 0133): the read-only rule on d's flags, the absolute-path refusal, the unfollowed
// symlink's `loop`, and then os.Root. It returns the new descriptor, or the error-code case.
func (d *fsDescriptor) openAt(pathFlags uint32, p string, oflags uint32, flags uint8) (opened *fsDescriptor, label string) {
	if d.dir == nil {
		return nil, "not-directory" // openat on a file descriptor is ENOTDIR
	}
	// NEUTER, run: drop this rule, and TestFSDescriptorMatchesWasmtime's read-only-opens row reads 0x00000001 on a
	// read-only grant, its three opens ok and new.txt created on the host; read-only-truncate reads 0 and empties
	// the host's hello.txt. **This rule is the whole of read-only**: the grant's os.Root confines, and does not refuse
	// a write.
	if (flags&(fsFlagWrite|fsFlagMutateDir) != 0 || oflags&(openCreate|openTruncate) != 0) &&
		d.flags&fsFlagMutateDir == 0 {
		return nil, "read-only"
	}
	if fsAbsolute(p) {
		return nil, "not-permitted"
	}
	if pathFlags&pathFollow == 0 {
		// os.Root follows a final symlink always, so whether to follow is asked first. Confinement is still os.Root's:
		// a swap in between can make an in-grant symlink be followed, never one outside it reachable.
		//
		// NEUTER, run: drop this check, and TestFSDescriptorMatchesWasmtime's symlinks row reads 0x0004009e: the
		// unfollowed open of out-link is followed and refused as an escape, not-permitted where wasmtime reads loop.
		if fi, err := d.dir.Lstat(p); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return nil, "loop"
		}
	}
	mode := os.O_RDONLY
	switch {
	case flags&fsFlagRead != 0 && flags&fsFlagWrite != 0:
		mode = os.O_RDWR
	case flags&fsFlagWrite != 0:
		mode = os.O_WRONLY
	}
	if oflags&openCreate != 0 {
		mode |= os.O_CREATE
	}
	if oflags&openExclusive != 0 {
		mode |= os.O_EXCL
	}
	if oflags&openTruncate != 0 {
		mode |= os.O_TRUNC
	}
	// NEUTER, run: open filepath.Join(d.dir.Name(), p) with os.OpenFile instead, path arithmetic for os.Root, and
	// TestFSDescriptorMatchesWasmtime's escapes row reads 0x9e939e93, ../x and sub/../../x reaching outside the grant
	// and finding nothing there (no-entry); its nested row reads 0x02000000, ../hello.txt opened from sub.
	f, err := d.dir.OpenFile(p, mode, 0o666)
	if err != nil {
		return nil, fsErrorLabel(err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close() // the open is being refused; nobody reads the close's error
		return nil, fsErrorLabel(err)
	}
	child := &fsDescriptor{fs: d.fs, grant: d.grant, path: path.Join(d.path, filepath.ToSlash(p)), flags: flags & fsFlagsKnown}
	if fi.IsDir() {
		// A directory is a nested root, opened from this one, so it can reach nothing this one could not.
		_ = f.Close() // the probe's handle; nobody reads the close's error
		r, rerr := d.dir.OpenRoot(p)
		if rerr != nil {
			return nil, fsErrorLabel(rerr)
		}
		child.dir = r
		return child, ""
	}
	if oflags&openDirectory != 0 {
		_ = f.Close() // refused; nobody reads the close's error
		return nil, "not-directory"
	}
	ff, ok := d.fs.adopt(f)
	if !ok {
		return nil, "io" // the instance is closing
	}
	child.file = ff
	return child, ""
}

// stat is `stat` of d: its directory's root, or its file.
func (d *fsDescriptor) stat() (os.FileInfo, error) {
	if d.dir != nil {
		return d.dir.Stat(".")
	}
	return d.file.f.Stat()
}

// statAt is `stat-at` of p beneath d, following a final symlink only when asked.
func (d *fsDescriptor) statAt(pathFlags uint32, p string) (fi os.FileInfo, label string) {
	if d.dir == nil {
		return nil, "not-directory"
	}
	if fsAbsolute(p) {
		return nil, "not-permitted"
	}
	var err error
	if pathFlags&pathFollow != 0 {
		fi, err = d.dir.Stat(p)
	} else {
		fi, err = d.dir.Lstat(p)
	}
	if err != nil {
		return nil, fsErrorLabel(err)
	}
	return fi, ""
}

// fsMethod lends an async method's self for the call and resolves it inline (ADR 0133): the impl does its work on
// the guest's goroutine and calls onResolve before it returns, so the async lower returns RETURNED and the sync
// lower (ADR 0126) finds the subtask resolved. want is the flat parameter count, self's included.
func fsMethod(name string, want int, f func(c *interp.CanonCaller, res *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error)) asyncImplFor {
	return func(_ *asyncHandles, res *hostResources) asyncLowerImpl {
		return func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			args := onStart()
			if len(args) != want {
				return nil, fmt.Errorf("component: descriptor.%s: got %d flat parameters, want %d", name, len(args), want)
			}
			d, release, err := lendAs[*fsDescriptor](res, descriptorResource, args[0].Int32())
			if err != nil {
				return nil, err
			}
			v, err := f(c, res, d, args[1:])
			release()
			if err != nil {
				return nil, err
			}
			// NEUTER, run: `go onResolve(v)` instead, and TestFSDescriptorMatchesWasmtime's stat, stat-at and get-type
			// rows read their async lower's status byte as 0x31, 0x11 and 0x11, STARTED (1) with a subtask index, where
			// wasmtime returns RETURNED (2) inline.
			onResolve(v)
			return nil, nil
		}
	}
}

// fsOpenAt is `[method]descriptor.open-at: async func(path-flags, path: string, open-flags, flags: descriptor-flags)
// -> result<descriptor, error-code>`. Its six flat parameters spill for the async lower, and the lower loads them
// flat (ADR 0123): self, path-flags, the path's two words, open-flags, flags.
func (h *Host) fsOpenAt() asyncImplFor {
	return fsMethod("open-at", 6, func(c *interp.CanonCaller, res *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		p, err := liftPath(c, "open-at", rest[1], rest[2])
		if err != nil {
			return canon.Value{}, err
		}
		own := canon.OwnType(descriptorResource.rt)
		child, label := d.openAt(uint32(rest[0].Bits), p, uint32(rest[3].Bits), uint8(rest[4].Bits))
		if label != "" {
			return fsErr(&own, label), nil
		}
		handle := canon.Own(descriptorResource.rt, uint32(res.mint(descriptorResource, child)))
		return fsOK(&own, &handle), nil
	})
}

// fsStat is `[method]descriptor.stat: async func() -> result<descriptor-stat, error-code>`.
func (h *Host) fsStat() asyncImplFor {
	return fsMethod("stat", 1, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, _ []interp.Value) (canon.Value, error) {
		st := fsStatType()
		fi, err := d.stat()
		if err != nil {
			return fsErr(&st, fsErrorLabel(err)), nil
		}
		v, err := fsStatValue(fi)
		if err != nil {
			return canon.Value{}, fmt.Errorf("component: descriptor.stat: %w", err)
		}
		return fsOK(&st, &v), nil
	})
}

// fsStatAt is `[method]descriptor.stat-at: async func(path-flags, path: string) -> result<descriptor-stat,
// error-code>`: self, path-flags and the path's two words.
func (h *Host) fsStatAt() asyncImplFor {
	return fsMethod("stat-at", 4, func(c *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		p, err := liftPath(c, "stat-at", rest[1], rest[2])
		if err != nil {
			return canon.Value{}, err
		}
		st := fsStatType()
		fi, label := d.statAt(uint32(rest[0].Bits), p)
		if label != "" {
			return fsErr(&st, label), nil
		}
		v, err := fsStatValue(fi)
		if err != nil {
			return canon.Value{}, fmt.Errorf("component: descriptor.stat-at: %w", err)
		}
		return fsOK(&st, &v), nil
	})
}

// fsGetFlags is `[method]descriptor.get-flags: async func() -> result<descriptor-flags, error-code>`: the flags the
// descriptor was granted or opened with.
func (h *Host) fsGetFlags() asyncImplFor {
	return fsMethod("get-flags", 1, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, _ []interp.Value) (canon.Value, error) {
		ft := fsDescriptorFlagsType()
		var set []string
		for i, l := range fsFlagLabels {
			if d.flags&(1<<i) != 0 {
				set = append(set, l)
			}
		}
		v, err := canon.Flags(ft, set...)
		if err != nil {
			return canon.Value{}, fmt.Errorf("component: descriptor.get-flags: %w", err)
		}
		return fsOK(&ft, &v), nil
	})
}

// fsGetType is `[method]descriptor.get-type: async func() -> result<descriptor-type, error-code>`.
func (h *Host) fsGetType() asyncImplFor {
	return fsMethod("get-type", 1, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, _ []interp.Value) (canon.Value, error) {
		tt := fsDescriptorTypeType()
		fi, err := d.stat()
		if err != nil {
			return fsErr(&tt, fsErrorLabel(err)), nil
		}
		v := fsLabelledValue(tt, fsTypeLabel(fi.Mode()))
		return fsOK(&tt, &v), nil
	})
}

// getDirectories is `wasi:filesystem/preopens@0.3::get-directories: func() -> list<tuple<descriptor, string>>`,
// flattened to (ret). Each call mints one fresh own per preopen, each over an os.Root of its own, so two calls give
// two descriptors, each closed by its own drop. With no preopens the list is empty.
//
// NEUTER, run: answer an empty list whatever the grant, as before E1a, and TestFSDescriptorMatchesWasmtime's preopens
// row reads 0, and every row that opens traps lending descriptor handle 0, which is not live.
func (h *Host) getDirectories(res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("component: get-directories: got %d core args, want 1 (ret)", len(args))
		}
		pair := canon.TupleType(canon.OwnType(descriptorResource.rt), canon.Type{Kind: canon.KindString})
		elems := make([]canon.Value, 0, len(h.Preopens))
		for i, p := range h.Preopens {
			root, err := os.OpenRoot(p.Host)
			if err != nil {
				return nil, fmt.Errorf("component: get-directories: preopen %q: %w", p.Host, err)
			}
			flags := uint8(fsFlagRead)
			// NEUTER, run: grant mutate-directory whatever Writable says, and TestFSDescriptorMatchesWasmtime's
			// read-only rows read wasmtime's own values on its read|mutate grant, 33 and 0, creating new.txt and
			// emptying hello.txt on the host.
			if p.Writable {
				flags |= fsFlagMutateDir
			}
			idx := res.mint(descriptorResource, &fsDescriptor{fs: res.fs, grant: i, path: ".", flags: flags, dir: root})
			guest := p.Guest
			if guest == "" {
				guest = p.Host
			}
			v, err := canon.Record(pair, map[string]canon.Value{
				"0": canon.Own(descriptorResource.rt, uint32(idx)), "1": canon.Str(guest),
			})
			if err != nil {
				return nil, fmt.Errorf("component: get-directories: %w", err)
			}
			elems = append(elems, v)
		}
		list, err := canon.List(pair, elems...)
		if err != nil {
			return nil, fmt.Errorf("component: get-directories: %w", err)
		}
		return nil, canon.StoreVia(guestHeap{c}, list, int(uint32(args[0].Bits)))
	}
}

// fsReadViaStream is `[method]descriptor.read-via-stream: func(offset: filesize) -> tuple<stream<u8>, future<result<_,
// error-code>>>`, flattened to (self, offset, ret): ADR 0122's producer over an io.SectionReader of the file from the
// offset, its goroutine counted in the file registry. The stream holds a reference to the file, so it outlives the
// descriptor. A descriptor without `read` is a finished stream and `err(bad-descriptor)`, and a directory
// `err(is-directory)`.
func (h *Host) fsReadViaStream(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("component: descriptor.read-via-stream: got %d core args, want 3 (self, offset, ret)", len(args))
		}
		d, release, err := lendAs[*fsDescriptor](res, descriptorResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		defer release()
		off := args[1].Bits
		p := &streamProducer{h: async, ok: fsOK(nil, nil), mapErr: fsErrResult}
		label := ""
		switch {
		case d.file == nil:
			label = "is-directory"
		case d.flags&fsFlagRead == 0:
			label = "bad-descriptor"
		case off > math.MaxInt64:
			label = "invalid"
		case !res.fs.retain(d.file):
			label = "bad-descriptor"
		}
		if label != "" {
			bad := fsErr(nil, label)
			p.final = &bad
		} else {
			ff, reg := d.file, res.fs
			p.src = io.NewSectionReader(ff.f, int64(off), math.MaxInt64-int64(off))
			p.track = reg.track
			p.onDone = func(bool) { reg.release(ff) }
		}
		async.mu.Lock()
		ri, fi := p.mintLocked()
		async.mu.Unlock()
		var buf [8]byte
		binary.LittleEndian.PutUint32(buf[0:], uint32(ri))
		binary.LittleEndian.PutUint32(buf[4:], uint32(fi))
		if werr := c.Write(uint64(uint32(args[2].Bits)), buf[:]); werr != nil {
			return nil, fmt.Errorf("component: descriptor.read-via-stream: writing the stream and future handles: %w", werr)
		}
		return nil, nil
	}
}

// fsWriteBufCap bounds the bytes a write-via-stream holds that the guest has written and the file has not yet taken.
const fsWriteBufCap = 64 << 10

// fsSend is one write-via-stream: the guest's stream, read by the host into a bounded buffer, and the writer
// goroutine that writes the buffer to the file at an advancing offset (ADR 0133, sockSend's shape). Every field is
// guarded by h.mu.
type fsSend struct {
	h      *asyncHandles
	fs     *hostFS
	file   *fsFile
	off    int64
	reader *readableStreamEnd
	future *readableFutureEnd
	buf    []byte
	// inFlight is the bytes a write has taken and not returned from. closing is the guest's writer having dropped:
	// drain, then resolve. done is the write finished, or stopped.
	inFlight int
	closing  bool
	done     bool
}

func (s *fsSend) free() int { return fsWriteBufCap - len(s.buf) - s.inFlight }

// armLocked re-arms the host's read of the guest's stream at the room the buffer has, sockSend.armLocked's rule:
// with room and a guest write parked, the copy runs now, so the caller holds the run slot as well as h.mu.
func (s *fsSend) armLocked() error {
	re := s.reader
	re.resolved, re.delivered, re.progress = false, false, 0
	room := s.free()
	if room <= 0 {
		re.hasRead = false
		return nil
	}
	re.readN, re.hasRead = uint32(room), true
	if w := re.conn; w != nil && w.hasWrite && !w.resolved {
		progress, err := driveStreamCopyLocked(re, w)
		if err != nil {
			return err
		}
		w.completeWriteLocked(progress, copyCompleted)
		re.armLocked(progress, copyCompleted)
	}
	return nil
}

// fsWriteViaStream is `[method]descriptor.write-via-stream: func(data: stream<u8>, offset: filesize) ->
// future<result<_, error-code>>`, flattened to (self, data, offset) -> future: tcp-socket.send's shape (ADR 0123)
// over a file. The future resolves `ok` once the guest's writer has dropped and every byte is written. A descriptor
// without `write`, a directory included, resolves `err(bad-descriptor)` inline and unlinks the stream, so the
// guest's write completes DROPPED with 0 bytes, wasmtime 49.0.3's reading.
//
// The writer goroutine is authorised by ADR 0133 in the census. It runs no guest code: it waits for bytes on the
// instance's host-I/O condition, writes them with no lock held, and delivers what the write freed under ADR 0101's
// discipline. It ends when the stream is finished, a write fails, or Close stops it, and Close waits it out.
//
// NEUTER, run: drop the write-flag test, and TestFSDescriptorMatchesWasmtime's write-read-only-fd row reads 0x00825000:
// the guest's write COMPLETED with 5, and only WriteAt's EBADF on the read-only file makes the future
// err(bad-descriptor), where wasmtime drops the stream with 0 bytes taken.
func (h *Host) fsWriteViaStream(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("component: descriptor.write-via-stream: got %d core args, want 3 (self, data, offset)", len(args))
		}
		d, release, err := lendAs[*fsDescriptor](res, descriptorResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		defer release()
		ri, off := uint32(args[1].Bits), args[2].Bits
		label := ""
		var ff *fsFile
		switch {
		case d.file == nil || d.flags&fsFlagWrite == 0:
			label = "bad-descriptor"
		case off > math.MaxInt64:
			label = "invalid"
		case !res.fs.retain(d.file):
			label = "bad-descriptor"
		default:
			ff = d.file
		}
		async.mu.Lock()
		defer async.mu.Unlock()
		re, ok := handleAt[*readableStreamEnd](async, ri)
		if !ok {
			if ff != nil {
				res.fs.release(ff) // under h.mu, the permitted order
			}
			return nil, fmt.Errorf("%w: descriptor.write-via-stream on handle %d that is not a readable stream end", ErrLinkRefused, ri)
		}
		if re.conn != nil && re.conn.producer != nil {
			if ff != nil {
				res.fs.release(ff)
			}
			return nil, fmt.Errorf("%w: descriptor.write-via-stream of a stream the host produces; a host-to-host copy "+
				"is not built (ADR 0122)", ErrUnsupportedForm)
		}
		okv := fsOK(nil, nil)
		// Typed by its payload (#1282 item 1), as tcpSend's write future is: an untyped end fails the guest's
		// future.read with "a future of a different type". E1a (#1374) and D1 (#1373) were built in parallel, and this
		// site is where they met.
		fe := &readableFutureEnd{payload: &okv, elem: elemOfPayload(&okv)}
		fe.index = async.addLocked(fe)
		var ended func()
		if ff != nil {
			if e, tok := res.fs.trackWriter(ff); tok {
				ended = e
			} else {
				res.fs.release(ff)
				ff, label = nil, "bad-descriptor" // Close has stopped the instance's files
			}
		}
		if ff == nil {
			bad := fsErr(nil, label)
			fe.payload = &bad
			unlinkHostReadLocked(re)
			if cerr := fe.completeLocked(copyCompleted); cerr != nil {
				return nil, cerr
			}
			return []interp.Value{interp.I32(int32(fe.index))}, nil
		}
		if async.hostIO == nil {
			async.hostIO = sync.NewCond(&async.mu)
		}
		s := &fsSend{h: async, fs: res.fs, file: ff, off: int64(off), reader: re, future: fe}
		if re.writerDropped {
			s.closing = true // the guest dropped its writer before writing: nothing to write
		} else {
			re.hostSink = func(b []byte) error {
				s.buf = append(s.buf, b...)
				async.hostIO.Broadcast()
				return nil
			}
			re.onHostComplete = func(r copyResult) {
				if r == copyCompleted {
					re.resolved, re.delivered, re.progress = false, false, 0
					if room := s.free(); room > 0 {
						re.readN = uint32(room)
					} else {
						re.hasRead = false
					}
					return
				}
				s.closing = true // DROPPED: the guest's writer is gone
				async.hostIO.Broadcast()
			}
			re.caller, re.state = c, copyStateCopying
			if err := s.armLocked(); err != nil {
				ended()
				res.fs.release(ff)
				return nil, err
			}
		}
		go func() {
			defer ended()
			s.run()
		}()
		return []interp.Value{interp.I32(int32(fe.index))}, nil
	}
}

// run is the writer goroutine's body: wait for bytes, write them at the offset with no lock held, deliver what the
// write freed, and at the writer's drop resolve the future. At Close's stop it writes what the buffer holds, since
// the guest was told those writes were complete (ADR 0127's reason).
func (s *fsSend) run() {
	h := s.h
	for {
		h.mu.Lock()
		for len(s.buf) == 0 && !s.closing && !h.producersStopped {
			h.hostIO.Wait()
		}
		if h.producersStopped {
			pending, off := s.buf, s.off
			s.buf = nil
			h.mu.Unlock()
			if len(pending) > 0 {
				_, _ = s.file.f.WriteAt(pending, off) //nolint:errcheck // the guest has gone: nobody is left to tell of a loss
			}
			return
		}
		if len(s.buf) == 0 { // closing, and drained
			h.filePending++ // a waker until the future's resolution is stored
			h.mu.Unlock()
			s.deliver(func() { s.finishLocked(nil) })
			return
		}
		chunk, off := s.buf, s.off
		s.buf, s.inFlight = nil, len(chunk)
		h.filePending++ // a waker while the write is in flight: its delivery may complete a parked write
		h.mu.Unlock()
		n, werr := s.file.f.WriteAt(chunk, off)
		s.deliver(func() {
			s.inFlight = 0
			s.off += int64(n)
			if werr != nil {
				s.finishLocked(werr)
				return
			}
			if err := s.armLocked(); err != nil {
				s.finishLocked(err)
			}
		})
		if werr != nil {
			return
		}
	}
}

// deliver is sockSend.deliver for a file write: queued for the run slot's holder while it is in host code, otherwise
// applied under the slot on a chain of its own, and dropped if Close has stopped the table. f runs under h.mu; the
// filePending the caller raised is lowered whether it runs or not.
func (s *fsSend) deliver(f func()) {
	h := s.h
	apply := func() {
		h.mu.Lock()
		h.filePending--
		if !h.producersStopped && !s.done {
			f()
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	if h.producersStopped {
		h.filePending--
		h.mu.Unlock()
		return
	}
	if h.window.inHost > 0 {
		h.window.hostStores = append(h.window.hostStores, apply)
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	if h.agentsRunFree {
		apply()
		return
	}
	ch := &runChain{}
	if !h.acquireRun(ch, nil, h.producerStop) {
		h.mu.Lock()
		h.filePending--
		h.mu.Unlock()
		return
	}
	defer h.releaseRunFor(ch)
	apply()
}

// finishLocked ends the write: the future resolves `ok`, or the error's code with the stream unlinked so later
// writes complete DROPPED, and the stream's reference to the file is released. The caller holds h.mu.
func (s *fsSend) finishLocked(err error) {
	s.done = true
	if err != nil {
		bad := fsErr(nil, fsErrorLabel(err))
		s.future.payload = &bad
		unlinkHostReadLocked(s.reader)
	}
	if cerr := s.future.completeLocked(copyCompleted); cerr != nil {
		s.future.completeErr = cerr // surfaced at the future's drop, as write-via-stream's is
	}
	s.fs.release(s.file)
}
