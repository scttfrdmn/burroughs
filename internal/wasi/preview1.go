// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package wasi is an internal WASI preview-1 host module for [ADR 0080][0080]'s kickoff: running a
// program compiled by a third-party toolchain — first, a Go `GOOS=wasip1` guest reaching `main`,
// writing stdout, and exiting.
//
// **This is WASI preview 1, not the contract's §6 WASI 0.3**, which is a v3 deliverable. Preview 1 is
// an older, different interface; running a `wasip1` guest advances no phase and changes no contract
// text. It exists to prove the v1 engine and §5's host surface are usable, and it is §5's ([ADR
// 0069][0069]) first real consumer.
//
// **The surface it binds to is entirely `interp`'s public API**: an [interp.Imports] resolver returns
// an [interp.HostExtern] per name, each wrapping an [interp.HostFunc]; guest memory is reached through
// [interp.Caller.Read] and [interp.Caller.Write], the copying accessors 0069 chose over a view.
//
// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
// [0080]: ../../docs/decisions/0080-a-go-wasip1-guest-runs-to-main-on-the-host-surface-and-the-preview1-import-set-is-supplied-whole-because-link-refuses-a-gap.md
package wasi

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// module is the name a `wasip1` guest imports every function under.
const module = "wasi_snapshot_preview1"

// The preview-1 `errno` values this slice uses. The full enum is larger; these are the ones the
// startup path and its stubs return, named rather than spelled as bare integers at the call sites.
const (
	errSuccess uint16 = 0
	errBadf    uint16 = 8  // bad file descriptor
	errFault   uint16 = 21 // bad address — a guest pointer the accessors refuse
	errInval   uint16 = 28 // invalid argument
	errIO      uint16 = 29 // I/O error
	errNosys   uint16 = 52 // function not implemented — the honest answer for a stub
)

// exitError is `proc_exit`'s carrier. [interp.HostFunc]'s contract is that a returned error is a trap
// the guest cannot catch; `proc_exit` is the one "trap" that is a normal termination, so [Run] unwraps
// this rather than reporting it as a failure. One channel, so a normal exit cannot be spelled as a
// silent success and a real trap cannot be spelled as exit 0 (ADR 0080, decision 4).
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("wasi: proc_exit(%d)", e.code) }

// host holds the per-run configuration a preview-1 call reads: argv, environment, the stdout/stderr
// sinks, and a start instant for the monotonic clock.
type host struct {
	args   []string
	env    []string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	start  time.Time

	// fds is the per-run file-descriptor table (decision 0083): 0/1/2 are stdio, preopen dirs and
	// opened files are 3.. A guest fd indexes it and a missing key is `EBADF`. No lock guards it: Go's
	// wasip1 runtime is cooperatively scheduled on a single wasm thread (no atomics, no shared memory
	// — the reason gate:threads is not load-bearing here), so host calls are sequential. A guest that
	// used wasm threads would change that, and would be behind gate:threads, which this workload is
	// not.
	fds        map[uint32]*fdEntry
	nextFD     uint32   // the next fd path_open hands out
	preopenFDs []uint32 // preopen dir fds in discovery order (3, 4, …)
}

// entry is one import: the wasm type the linker matches against, and the Go function behind it. The
// type must match the guest's declared import exactly or `link` refuses it as `incompatible import
// type`, so the signatures below are the guest's, transcribed (ADR 0080's measured sig 0–6).
type entry struct {
	ft bin.FuncType
	fn interp.HostFunc
}

// i32/i64 abbreviate the two value types every preview-1 signature is built from.
var (
	i32 = bin.I32
	i64 = bin.I64
)

// ft builds a function type; a nil params slice is the no-parameter case, and results are variadic so
// `ft(p)` reads as "no results" (proc_exit) without an empty-slice literal.
func ft(params []bin.ValType, results ...bin.ValType) bin.FuncType {
	return bin.FuncType{Params: params, Results: results}
}

// imports is the [interp.Imports] resolver: it answers only for the `wasi_snapshot_preview1` module,
// and only for the names in the table. Every name the measured Go guest declares is present —
// `link` refuses an unsatisfied import outright (ADR 0080's central finding), so the off-path ones
// are supplied as stubs rather than omitted.
func (h *host) imports() interp.Imports {
	table := map[string]entry{
		// Startup path, implemented for real.
		"args_get":          {ft([]bin.ValType{i32, i32}, i32), h.argsGet},
		"args_sizes_get":    {ft([]bin.ValType{i32, i32}, i32), h.argsSizesGet},
		"environ_get":       {ft([]bin.ValType{i32, i32}, i32), h.environGet},
		"environ_sizes_get": {ft([]bin.ValType{i32, i32}, i32), h.environSizesGet},
		"clock_time_get":    {ft([]bin.ValType{i32, i64, i32}, i32), h.clockTimeGet},
		"random_get":        {ft([]bin.ValType{i32, i32}, i32), h.randomGet},
		"fd_write":          {ft([]bin.ValType{i32, i32, i32, i32}, i32), h.fdWrite},
		"fd_read":           {ft([]bin.ValType{i32, i32, i32, i32}, i32), h.fdRead},
		"fd_fdstat_get":     {ft([]bin.ValType{i32, i32}, i32), h.fdFdstatGet},
		"fd_prestat_get":    {ft([]bin.ValType{i32, i32}, i32), h.fdPrestatGet},
		"sched_yield":       {ft(nil, i32), h.schedYield},
		"proc_exit":         {ft([]bin.ValType{i32}), h.procExit},
		// poll_oneoff handles clock subscriptions (the timer path time.Sleep drives); its fd arms
		// remain a floor that grows when a guest polls an fd (ADR 0080).
		"poll_oneoff": {ft([]bin.ValType{i32, i32, i32, i32}, i32), h.pollOneoff},

		// Read-only filesystem (decision 0083): path_open for reading, the two stats, fd_close, and the
		// two prestat calls — no longer stubs.
		"path_open":           {ft([]bin.ValType{i32, i32, i32, i32, i32, i64, i64, i32, i32}, i32), h.pathOpen},
		"path_filestat_get":   {ft([]bin.ValType{i32, i32, i32, i32, i32}, i32), h.pathFilestatGet},
		"fd_filestat_get":     {ft([]bin.ValType{i32, i32}, i32), h.fdFilestatGet},
		"fd_close":            {ft([]bin.ValType{i32}, i32), h.fdClose},
		"fd_prestat_dir_name": {ft([]bin.ValType{i32, i32, i32}, i32), h.fdPrestatDirName},

		// Still stubbed: no guest here exercises it (decision 0083 defers writes, fd_seek, dir enum).
		"fd_fdstat_set_flags": {ft([]bin.ValType{i32, i32}, i32), h.fdFdstatSetFlags},
	}
	return func(mod, name string) (interp.Extern, bool) {
		if mod != module {
			return interp.Extern{}, false
		}
		e, ok := table[name]
		if !ok {
			return interp.Extern{}, false
		}
		return interp.HostExtern(e.ft, e.fn), true
	}
}

// --- guest-memory helpers, over the copying accessors ---
//
// Each returns a WASI `errno` (uint16), not a Go error, so a refused guest access is `EFAULT` to the
// guest rather than a trap that terminates it — a bad pointer is the guest's error to hear about. The
// error check therefore lives here, in a function with no error return, which is both the correct WASI
// shape and why the `HostFunc` bodies below carry no `if err != nil` at all.

func mRead(c *interp.Caller, ptr, n uint32) (data []byte, errno uint16) {
	b, err := c.Read(uint64(ptr), uint64(n))
	if err != nil {
		return nil, errFault
	}
	return b, errSuccess
}

func mReadU32(c *interp.Caller, ptr uint32) (v uint32, errno uint16) {
	b, e := mRead(c, ptr, 4)
	if e != errSuccess {
		return 0, e
	}
	return binary.LittleEndian.Uint32(b), errSuccess
}

func mReadU8(c *interp.Caller, ptr uint32) (v uint8, errno uint16) {
	b, e := mRead(c, ptr, 1)
	if e != errSuccess {
		return 0, e
	}
	return b[0], errSuccess
}

func mReadU16(c *interp.Caller, ptr uint32) (v, errno uint16) {
	b, e := mRead(c, ptr, 2)
	if e != errSuccess {
		return 0, e
	}
	return binary.LittleEndian.Uint16(b), errSuccess
}

func mReadU64(c *interp.Caller, ptr uint32) (v uint64, errno uint16) {
	b, e := mRead(c, ptr, 8)
	if e != errSuccess {
		return 0, e
	}
	return binary.LittleEndian.Uint64(b), errSuccess
}

func mWrite(c *interp.Caller, ptr uint32, buf []byte) uint16 {
	if err := c.Write(uint64(ptr), buf); err != nil {
		return errFault
	}
	return errSuccess
}

func mWriteU32(c *interp.Caller, ptr, v uint32) uint16 {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return mWrite(c, ptr, b[:])
}

func mWriteU64(c *interp.Caller, ptr uint32, v uint64) uint16 {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return mWrite(c, ptr, b[:])
}

// sink writes to an fd's io.Writer, returning a WASI errno so the caller carries no error check.
func sink(w io.Writer, data []byte) (n int, errno uint16) {
	n, err := w.Write(data)
	if err != nil {
		return n, errIO
	}
	return n, errSuccess
}

// ret is the single-i32 result every preview-1 function but `proc_exit` returns.
func ret(e uint16) []interp.Value { return []interp.Value{interp.I32(int32(e))} }

// u32 reads the i-th argument as an unsigned 32-bit value — WASI pointers and sizes are u32.
func u32(args []interp.Value, i int) uint32 { return uint32(args[i].Int32()) }

// --- the functions ---

// fdWrite is preview-1's write, and the one that judges §5 option A in real use: it walks a `ciovec`
// array in guest memory (each entry an 8-byte {buf u32, len u32}), reads each buffer through the
// copying accessor, writes it to the fd's sink, and writes the total back.
func (h *host) fdWrite(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	w := h.writerFor(u32(args, 0))
	if w == nil {
		return ret(errBadf), nil
	}
	iovs, iovsLen, nwrittenPtr := u32(args, 1), u32(args, 2), u32(args, 3)
	var total uint32
	for i := range iovsLen {
		base := iovs + i*8
		buf, e := mReadU32(c, base)
		if e != errSuccess {
			return ret(e), nil
		}
		n, e := mReadU32(c, base+4)
		if e != errSuccess {
			return ret(e), nil
		}
		if n == 0 {
			continue
		}
		data, e := mRead(c, buf, n)
		if e != errSuccess {
			return ret(e), nil
		}
		written, e := sink(w, data)
		total += uint32(written)
		if e != errSuccess {
			return ret(e), nil
		}
	}
	if e := mWriteU32(c, nwrittenPtr, total); e != errSuccess {
		return ret(e), nil
	}
	return ret(errSuccess), nil
}

// writerFor maps a wasm fd to its sink; only stdout (1) and stderr (2) are writable in this slice.
func (h *host) writerFor(fd uint32) io.Writer {
	if e := h.fds[fd]; e != nil {
		return e.writer
	}
	return nil
}

// fdRead is preview-1's read, into a guest `iovec` array. Only stdin (0) is readable in this slice;
// a file fd is `EBADF` because the filesystem is a later slice. It fills iovecs in order and stops at
// the first short read or EOF — fd_read may return fewer bytes than requested, which is what the Go
// runtime's stdin reader expects.
func (h *host) fdRead(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	r := h.readerFor(u32(args, 0))
	if r == nil {
		return ret(errBadf), nil
	}
	iovs, iovsLen, nreadPtr := u32(args, 1), u32(args, 2), u32(args, 3)
	var total uint32
	for i := range iovsLen {
		base := iovs + i*8
		buf, e := mReadU32(c, base)
		if e != errSuccess {
			return ret(e), nil
		}
		n, e := mReadU32(c, base+4)
		if e != errSuccess {
			return ret(e), nil
		}
		if n == 0 {
			continue
		}
		data, eof, e := readSome(r, n)
		if e != errSuccess {
			return ret(e), nil
		}
		if len(data) > 0 {
			if e = mWrite(c, buf, data); e != errSuccess {
				return ret(e), nil
			}
			total += uint32(len(data))
		}
		if eof || uint32(len(data)) < n {
			break // short read or EOF: do not block for more across the remaining iovecs
		}
	}
	return ret(mWriteU32(c, nreadPtr, total)), nil
}

// readerFor maps a wasm fd to its source; only stdin (0) is readable in this slice.
func (h *host) readerFor(fd uint32) io.Reader {
	e := h.fds[fd]
	if e == nil {
		return nil
	}
	if e.file != nil {
		return e.file
	}
	return e.reader
}

// readSome reads up to n bytes, returning a WASI errno so fdRead carries no error check. EOF is a
// value, not an error, so the caller can drain what came with it before stopping.
func readSome(r io.Reader, n uint32) (data []byte, eof bool, errno uint16) {
	tmp := make([]byte, n)
	got, err := r.Read(tmp)
	if err != nil && err != io.EOF {
		return nil, false, errIO
	}
	return tmp[:got], err == io.EOF, errSuccess
}

// procExit terminates the guest with an exit code. No results — it does not return to the guest.
func (h *host) procExit(_ *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	return nil, exitError{code: int(u32(args, 0))}
}

// argsSizesGet writes argc and the total argv-buffer size (each arg is null-terminated).
func (h *host) argsSizesGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	return ret(sizes(c, h.args, u32(args, 0), u32(args, 1))), nil
}

// argsGet writes the argv pointer array and the null-terminated argument strings.
func (h *host) argsGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	return ret(getStrings(c, h.args, u32(args, 0), u32(args, 1))), nil
}

// environSizesGet and environGet are the same shape over the environment (empty by default).
func (h *host) environSizesGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	return ret(sizes(c, h.env, u32(args, 0), u32(args, 1))), nil
}

func (h *host) environGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	return ret(getStrings(c, h.env, u32(args, 0), u32(args, 1))), nil
}

// sizes writes the count and total null-terminated byte size of a string vector — the shared body of
// `args_sizes_get` and `environ_sizes_get`.
func sizes(c *interp.Caller, ss []string, countPtr, bufSizePtr uint32) uint16 {
	var bufSize uint32
	for _, s := range ss {
		bufSize += uint32(len(s)) + 1
	}
	if e := mWriteU32(c, countPtr, uint32(len(ss))); e != errSuccess {
		return e
	}
	return mWriteU32(c, bufSizePtr, bufSize)
}

// getStrings writes a vector's pointer array and its null-terminated strings — the shared body of
// `args_get` and `environ_get`.
func getStrings(c *interp.Caller, ss []string, ptrArray, buf uint32) uint16 {
	cursor := buf
	for i, s := range ss {
		if e := mWriteU32(c, ptrArray+uint32(i)*4, cursor); e != errSuccess {
			return e
		}
		if e := mWrite(c, cursor, append([]byte(s), 0)); e != errSuccess {
			return e
		}
		cursor += uint32(len(s)) + 1
	}
	return errSuccess
}

// clockTimeGet writes nanoseconds for the realtime (0) or monotonic (1) clock. The precision argument
// is advisory and ignored.
func (h *host) clockTimeGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	var ns uint64
	switch u32(args, 0) {
	case 0:
		ns = uint64(time.Now().UnixNano())
	case 1:
		ns = uint64(time.Since(h.start).Nanoseconds())
	default:
		return ret(errInval), nil
	}
	return ret(mWriteU64(c, u32(args, 2), ns)), nil
}

// randomGet fills a guest buffer with cryptographically random bytes — what the Go runtime seeds its
// hashmaps with before `main`.
func (h *host) randomGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	b, e := randomBytes(u32(args, 1))
	if e != errSuccess {
		return ret(e), nil
	}
	return ret(mWrite(c, u32(args, 0), b)), nil
}

// randomBytes draws n random bytes, returning a WASI errno so randomGet carries no error check.
func randomBytes(n uint32) (buf []byte, errno uint16) {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return nil, errIO
	}
	return b, errSuccess
}

// schedYield is a no-op on a cooperatively-scheduled single wasm thread: there is no sibling to yield
// to. It reports success rather than `ENOSYS` because yielding to nobody genuinely succeeds.
func (h *host) schedYield(_ *interp.Caller, _ []interp.Value) ([]interp.Value, error) {
	return ret(errSuccess), nil
}

// fdFdstatGet reports stdio (0,1,2) as character devices with generous rights, which is what the Go
// runtime probes at startup to classify its standard streams. A non-stdio fd is `EBADF`.
func (h *host) fdFdstatGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	e := h.fds[u32(args, 0)]
	if e == nil {
		return ret(errBadf), nil
	}
	var ft uint8
	switch {
	case e.preopen != nil:
		ft = filetypeDirectory
	case e.file != nil:
		ft = filetypeRegular
	default:
		ft = filetypeCharDevice // stdio
	}
	// fdstat: fs_filetype u8 @0, fs_flags u16 @2, fs_rights_base u64 @8, fs_rights_inheriting u64 @16.
	var b [24]byte
	b[0] = ft
	binary.LittleEndian.PutUint64(b[8:], ^uint64(0))
	binary.LittleEndian.PutUint64(b[16:], ^uint64(0))
	return ret(mWrite(c, u32(args, 1), b[:])), nil
}

// fdPrestatGet reports no preopens by returning `EBADF` for every fd, which terminates the Go
// runtime's preopen-discovery loop at its first probe. Filesystem is out of scope for this slice.
func (h *host) fdPrestatGet(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	e := h.fds[u32(args, 0)]
	if e == nil || e.preopen == nil {
		// Not a preopen — `EBADF` ends the runtime's discovery loop past the last granted directory.
		return ret(errBadf), nil
	}
	// prestat: tag u8 @0 (0 = preopentype_dir), pr_name_len u32 @4.
	var b [8]byte
	binary.LittleEndian.PutUint32(b[4:], uint32(len(e.preopen.guestName)))
	return ret(mWrite(c, u32(args, 1), b[:])), nil
}

// pollOneoff waits on a set of subscriptions and reports the events that fired. This slice handles
// **clock** subscriptions — the timer path `time.Sleep` drives through the Go runtime — and reports a
// non-clock (fd) subscription as `ENOSYS` rather than hanging, so a guest that polls an fd learns it
// here (ADR 0080's floor). The wait is a `select` on the earliest clock and `Caller.Context().Done()`,
// which is §5 H-3's *"interruptible by engine shutdown"*: a `Close` during a long sleep returns rather
// than hanging.
//
// The subscription is 48 bytes {userdata u64 @0, tag u8 @8, and for a clock: id u32 @16, timeout u64
// @24, precision u64 @32, flags u16 @40}; the event is 32 bytes {userdata u64 @0, error u16 @8, type u8
// @10, fd_readwrite @16}. Events are written before the wait, which is correct for the single-clock
// case the runtime's timer uses; multiple simultaneous clock subscriptions are all reported fired,
// which a later slice with a guest that needs finer resolution can sharpen.
func (h *host) pollOneoff(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	inPtr, outPtr, nsub, neventsPtr := u32(args, 0), u32(args, 1), u32(args, 2), u32(args, 3)
	var events uint32
	var wait time.Duration
	haveClock := false
	for i := range nsub {
		base := inPtr + i*48
		userdata, e := mReadU64(c, base)
		if e != errSuccess {
			return ret(e), nil
		}
		tag, e := mReadU8(c, base+8)
		if e != errSuccess {
			return ret(e), nil
		}
		ev := outPtr + events*32
		if tag != 0 {
			if e = writeEvent(c, ev, userdata, errNosys, tag); e != errSuccess {
				return ret(e), nil
			}
			events++
			continue
		}
		clockID, e := mReadU32(c, base+16)
		if e != errSuccess {
			return ret(e), nil
		}
		timeout, e := mReadU64(c, base+24)
		if e != errSuccess {
			return ret(e), nil
		}
		flags, e := mReadU16(c, base+40)
		if e != errSuccess {
			return ret(e), nil
		}
		if d := h.clockDelay(clockID, timeout, flags&1 != 0); !haveClock || d < wait {
			wait, haveClock = d, true
		}
		if e = writeEvent(c, ev, userdata, errSuccess, 0); e != errSuccess {
			return ret(e), nil
		}
		events++
	}
	if haveClock && wait > 0 {
		select {
		case <-time.After(wait):
		case <-c.Context().Done():
		}
	}
	return ret(mWriteU32(c, neventsPtr, events)), nil
}

// clockDelay converts a clock subscription's timeout to a duration to wait: a relative timeout is the
// duration itself; an absolute one is its distance from now on the named clock (0 if already past).
func (h *host) clockDelay(clockID uint32, timeout uint64, abs bool) time.Duration {
	if !abs {
		return time.Duration(timeout)
	}
	var now uint64
	if clockID == 1 { // monotonic
		now = uint64(time.Since(h.start).Nanoseconds())
	} else { // realtime and others
		now = uint64(time.Now().UnixNano())
	}
	if timeout <= now {
		return 0
	}
	return time.Duration(timeout - now)
}

// writeEvent writes a 32-byte poll_oneoff event. The fd_readwrite tail (bytes 16..) is left zero,
// which is correct for a clock event and a benign zero for the unsupported-fd case.
func writeEvent(c *interp.Caller, ptr uint32, userdata uint64, errno uint16, evType uint8) uint16 {
	var b [32]byte
	binary.LittleEndian.PutUint64(b[0:], userdata)
	binary.LittleEndian.PutUint16(b[8:], errno)
	b[10] = evType
	return mWrite(c, ptr, b[:])
}

// --- stubs: supplied so the module links, not exercised on either guest's active path (ADR 0080) ---

func (h *host) fdClose(_ *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	fd := u32(args, 0)
	e := h.fds[fd]
	if e == nil {
		return ret(errBadf), nil
	}
	if e.file != nil {
		_ = e.file.Close()
		delete(h.fds, fd)
	}
	// stdio and preopens: closing the guest's view succeeds without touching the host stream or dir.
	return ret(errSuccess), nil
}

func (h *host) fdFdstatSetFlags(_ *interp.Caller, _ []interp.Value) ([]interp.Value, error) {
	return ret(errSuccess), nil
}

func (h *host) fdPrestatDirName(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	fd, pathPtr, pathLen := u32(args, 0), u32(args, 1), u32(args, 2)
	e := h.fds[fd]
	if e == nil || e.preopen == nil {
		return ret(errBadf), nil
	}
	name := []byte(e.preopen.guestName)
	if uint32(len(name)) > pathLen {
		name = name[:pathLen]
	}
	return ret(mWrite(c, pathPtr, name)), nil
}
