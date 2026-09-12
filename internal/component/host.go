// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// guestHeap adapts a CanonCaller to canon.Heap, so the shared value lowerings (canon.StoreString, and
// the list/record lowerings as #718 extends the set) write into guest memory through the canon lower's
// bound realloc and memory — the same code the differential runs against the model heap (#719, ADR
// 0083's implement-once). Offsets are guest byte positions.
type guestHeap struct{ c *interp.CanonCaller }

func (g guestHeap) Realloc(origPtr, origSize, align, newSize int) (int, error) {
	p, err := g.c.Realloc(uint32(origPtr), uint32(origSize), uint32(align), uint32(newSize))
	return int(p), err
}

func (g guestHeap) WriteBytes(ptr int, data []byte) error {
	return g.c.Write(uint64(uint32(ptr)), data)
}

func (g guestHeap) StoreInt(v uint64, ptr, nbytes int) error {
	buf := make([]byte, nbytes)
	for i := range nbytes {
		buf[i] = byte(v >> (8 * i))
	}
	return g.c.Write(uint64(uint32(ptr)), buf)
}

// The real preview-2 host (PR C.2). The stub host (link_component.go) refuses every wasi import by name;
// this host provides the ten the guest-driven `wasi:cli/run` world reaches. Each import is a canon
// lower, so its impl is a `interp.CanonFunc` dispatched through the canonical-ABI adapter
// (`interp.CanonLowerExtern`, ADR 0084 / §5 H-2 amended): the impl marshals against the memory the
// lower's option names — bound into the `CanonCaller`, since the guest reaches these through a
// memory-less `$imports` trampoline — and, where it lowers a host-produced list, allocates its backing
// through the lower's `cabi_realloc` as agent execution (`CanonCaller.Realloc`).
//
// A stream's `result<_, stream-error>` and `result<u64, stream-error>` flatten past one core value, so
// the guest passes a return pointer and the host writes the lifted result there; the `ok` arm is
// written and the `err` arm is unreachable on the success path (its `own<error>` payload is refused at
// lower time — the deferral recorded on #694). A byte string crosses through the codec's `list<u8>`
// semantics: the guest hands a (ptr, len) pair and the host reads exactly those bytes from the bound
// memory. Stream handles are minted in a per-host resource table; a borrow of one resolves to the
// writer it names.

// Host is a preview-2 host: the standard streams and a resource table of the stream handles it has
// minted. Exited/ExitCode record a guest `exit` call.
type Host struct {
	Stdout   io.Writer
	Stderr   io.Writer
	Stdin    io.Reader
	Args     []string            // the guest's argv, lowered by get-arguments
	writers  map[int32]io.Writer // output-stream handles → the writer they name
	next     int32
	errors   map[int32]error // error handles (own<error>) minted for a stream-error's last-operation-failed
	errNext  int32
	Exited   bool
	ExitCode int32
}

// NewHost builds a preview-2 host over the given streams. Handles are minted from 1 (0 is left unused so
// a zero value is never a live handle).
func NewHost(stdout, stderr io.Writer, stdin io.Reader) *Host {
	return &Host{
		Stdout: stdout, Stderr: stderr, Stdin: stdin,
		writers: map[int32]io.Writer{}, next: 1,
		errors: map[int32]error{}, errNext: 1,
	}
}

// mintError records a host error as an own<error> handle, for a stream-error's last-operation-failed
// case; error.to-debug-string later reads it back.
func (h *Host) mintError(err error) int32 {
	id := h.errNext
	h.errNext++
	h.errors[id] = err
	return id
}

// mintWriter records a writer in the resource table and returns its handle.
func (h *Host) mintWriter(w io.Writer) int32 {
	id := h.next
	h.next++
	h.writers[id] = w
	return id
}

// wasi builds the import-name → canon-lower-impl table the walk consults. The keys are the
// "module::export" identities the loader records; a lowered func whose identity is absent still reaches
// the refusing stub (an unexercised import remains refused, by name).
// The keys are **version-stripped** "interface::export" identities (`wasi:cli/stdout::get-stdout`), not
// the full `interface@x.y.z::export` a lower names — a guest at `@0.2.6` (p3echo) and one at `@0.2.3`
// (p3hello) name the same interface at different versions, and the host serves the interface, not a
// version. `resolverFor` strips the `@version` before the lookup (walk.go, stripVersion).
func (h *Host) wasi() map[string]interp.CanonFunc {
	m := map[string]interp.CanonFunc{
		"wasi:cli/stdout::get-stdout": h.getWriter(func() io.Writer { return h.Stdout }),
		"wasi:cli/stderr::get-stderr": h.getWriter(func() io.Writer { return h.Stderr }),
		"wasi:cli/stdin::get-stdin":   h.getWriter(func() io.Writer { return io.Discard }),

		"wasi:io/streams::[method]output-stream.blocking-write-and-flush": h.streamWrite,
		"wasi:io/streams::[method]output-stream.write":                    h.streamWrite,
		"wasi:io/streams::[method]output-stream.check-write":              h.checkWrite,
		"wasi:io/streams::[method]output-stream.blocking-flush":           h.blockingFlush,
		"wasi:io/streams::[method]input-stream.blocking-read":             h.blockingRead,

		"wasi:io/error::[method]error.to-debug-string": h.errorToDebugString,

		"wasi:cli/exit::exit": h.exit,

		// get-arguments lowers the host's argv as a non-empty list<string>; get-environment stays empty.
		"wasi:cli/environment::get-arguments":       h.getArguments,
		"wasi:cli/environment::get-environment":     h.emptyList,
		"wasi:filesystem/preopens::get-directories": h.emptyList,
	}
	// The resource-drop intrinsics the world lowers are no-ops here: the table entry is left in place
	// (the streams are the process's, not the guest's to free), so a drop neither refuses nor frees.
	for _, name := range []string{
		"wasi:io/streams::[resource-drop]output-stream",
		"wasi:io/streams::[resource-drop]input-stream",
		"wasi:io/error::[resource-drop]error",
		"wasi:filesystem/types::[resource-drop]descriptor",
	} {
		m[name] = h.noop
	}
	return m
}

// getWriter returns a get-{stdout,stderr,stdin} impl: no arguments, one result — an own handle to a
// freshly minted stream resource (the codec's own-handle lower is a bare i32 handle). It touches no
// memory, so it needs neither the bound memory nor realloc.
func (h *Host) getWriter(pick func() io.Writer) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		return []interp.Value{interp.I32(h.mintWriter(pick()))}, nil
	}
}

// streamWrite marshals output-stream.write / blocking-write-and-flush: (self: borrow<output-stream>,
// contents: list<u8>) -> result<_, stream-error>, flattened to (self, ptr, len, ret). It lifts the
// list<u8> from the bound memory (canon load over u8), writes it to the stream `self` names, and writes
// the `ok` result at the return pointer. It allocates nothing (the guest owns the bytes and the ret ptr).
// errorRT is the opaque resource-type id for the own<error> handles the host mints. Only an i32 handle
// is ever lowered, so the id is unused by the guest-heap lowering; it names the resource type for the
// value's construction.
const errorRT = 0

// streamErrorType is the WIT `stream-error` variant: `last-operation-failed(own<error>)` or `closed`.
func streamErrorType() canon.Type {
	own := canon.OwnType(errorRT)
	return canon.VariantType(
		canon.Case{Name: "last-operation-failed", Type: &own},
		canon.Case{Name: "closed"},
	)
}

// streamResultType builds `result<ok, stream-error>` — `ok == nil` is `result<_, stream-error>`.
func streamResultType(ok *canon.Type) canon.Type {
	se := streamErrorType()
	return canon.ResultType(ok, &se)
}

// lowerResult lowers a fully-built result value at the guest return pointer through the codec's shared
// `StoreVia` — the same result/variant/own lowering the definitions.py fixtures verify (#724), not a
// hand path. A construction error means the value did not match its declared type — an engine bug.
func lowerResult(c *interp.CanonCaller, ret uint64, v canon.Value, buildErr error) error {
	if buildErr != nil {
		return fmt.Errorf("component: build result value: %w", buildErr)
	}
	return canon.StoreVia(guestHeap{c}, v, int(uint32(ret)))
}

// streamWrite marshals output-stream.write / blocking-write-and-flush: (self, contents: list<u8>) ->
// result<_, stream-error>, flattened to (self, ptr, len, ret). It lifts the list<u8> from guest memory,
// writes it to the stream `self` names, and lowers Ok — or, when the write fails,
// Err(last-operation-failed(own<error>)) minted from the underlying error (#694's deferred err arm).
// Both arms lower through the codec (#724), not a hand path.
func (h *Host) streamWrite(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("component: stream write: got %d core args, want 4 (self, ptr, len, ret)", len(args))
	}
	self := args[0].Int32()
	ptr := uint64(uint32(args[1].Bits))
	n := uint64(uint32(args[2].Bits))
	ret := uint64(uint32(args[3].Bits))
	w, ok := h.writers[self]
	if !ok {
		return nil, fmt.Errorf("%w: stream write on unknown handle %d", ErrLinkRefused, self)
	}
	buf, err := c.Read(ptr, n)
	if err != nil {
		return nil, fmt.Errorf("component: stream write: reading contents: %w", err)
	}
	if _, werr := w.Write(buf); werr != nil {
		own := canon.Own(errorRT, uint32(h.mintError(werr)))
		se, serr := canon.Variant(streamErrorType(), "last-operation-failed", &own)
		if serr != nil {
			return nil, serr
		}
		v, verr := canon.Variant(streamResultType(nil), "error", &se)
		return nil, lowerResult(c, ret, v, verr)
	}
	v, verr := canon.Variant(streamResultType(nil), "ok", nil)
	return nil, lowerResult(c, ret, v, verr)
}

// checkWrite marshals output-stream.check-write: (self) -> result<u64, stream-error>, flattened to
// (self, ret). It reports a large writable budget (the ok arm), so the guest never blocks on capacity.
func (h *Host) checkWrite(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("component: check-write: got %d core args, want 2 (self, ret)", len(args))
	}
	ret := uint64(uint32(args[1].Bits))
	u := canon.U64(1 << 30) // a large writable budget, so the guest never blocks on capacity
	okT := canon.Type{Kind: canon.KindU64}
	v, verr := canon.Variant(streamResultType(&okT), "ok", &u)
	return nil, lowerResult(c, ret, v, verr)
}

// blockingFlush marshals output-stream.blocking-flush: (self) -> result<_, stream-error>, always Ok.
func (h *Host) blockingFlush(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("component: blocking-flush: got %d core args, want 2 (self, ret)", len(args))
	}
	ret := uint64(uint32(args[1].Bits))
	v, verr := canon.Variant(streamResultType(nil), "ok", nil)
	return nil, lowerResult(c, ret, v, verr)
}

// exit marshals cli/exit.exit: (status: result<_, _>) -> (), flattened to a single discriminant i32
// (0 = ok/success, 1 = error). It records the exit rather than terminating the process.
func (h *Host) exit(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	h.Exited = true
	if len(args) == 1 && args[0].Int32() != 0 {
		h.ExitCode = 1
	}
	return nil, nil
}

// noop is a resource-drop intrinsic: it accepts the handle argument and returns, freeing nothing.
func (h *Host) noop(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) { return nil, nil }

// getArguments lowers the host's argv as a non-empty `list<string>` at the return pointer (its sole
// arg) — the first host-produced list with real payload. Each string's bytes are allocated in guest
// memory through the guest's realloc; the list backing is `count` string records (ptr, len) of 8 bytes.
func (h *Host) getArguments(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: get-arguments: got %d core args, want 1 (ret)", len(args))
	}
	// list<string> lowered by the codec's shared framing (canon.StoreList/StoreString via StoreVia),
	// against guest memory — the same code the definitions.py list<string> fixtures verify (#718), not a
	// hand path.
	elems := make([]canon.Value, len(h.Args))
	for i, s := range h.Args {
		elems[i] = canon.Str(s)
	}
	v, err := canon.List(canon.Type{Kind: canon.KindString}, elems...)
	if err != nil {
		return nil, err
	}
	return nil, canon.StoreVia(guestHeap{c}, v, int(uint32(args[0].Bits)))
}

// blockingRead marshals input-stream.blocking-read: (self: borrow<input-stream>, len: u64) ->
// result<list<u8>, stream-error>, flattened to (self, len, ret). It reads up to `len` bytes from the
// host stdin **through the blocking excursion** (§5 H-1/H-3, `CanonCaller.Blocking`), then — after the
// wake, never while blocked — lowers the result: Ok(list<u8>) when bytes were read, Err(closed) at EOF
// (the stream-error variant's `closed` case, no payload). The read is interruptible: Close cancels it.
func (h *Host) blockingRead(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("component: blocking-read: got %d core args, want 3 (self, len, ret)", len(args))
	}
	maxLen := args[1].Bits // u64
	ret := uint64(uint32(args[2].Bits))

	const readCap = 1 << 16
	n := maxLen
	if n > readCap {
		n = readCap
	}
	var data []byte
	if h.Stdin != nil && n > 0 {
		buf := make([]byte, n)
		var m int
		var rerr error
		// A direct blocking read inside the blocked excursion — the shape p1's `fd_read` uses (no engine
		// goroutine, so no new concurrency to authorise). enterBlocked marks the agent at a safepoint for
		// the read's duration (H-1: a Stop completes while it is parked), and the wake's poll ends it on
		// Close (H-3) once the read returns. A reader that never returns is p1's own limitation, not this
		// slice's to lift; a mid-read-cancellable read would be a goroutine site behind its own decision.
		if err := c.Blocking(func() error {
			m, rerr = h.Stdin.Read(buf)
			if rerr != nil && rerr != io.EOF {
				return rerr
			}
			return nil
		}); err != nil {
			return nil, err
		}
		data = buf[:m]
	}

	// result<list<u8>, stream-error>, lowered through the codec (#724): Ok(list<u8>) when bytes were
	// read, else Err(closed) at EOF (the stream-error variant's payloadless `closed` case).
	listU8 := canon.Type{Kind: canon.KindList, Elem: &canon.Type{Kind: canon.KindU8}}
	if len(data) > 0 {
		us := make([]canon.Value, len(data))
		for i, b := range data {
			us[i] = canon.U8(b)
		}
		lst, lerr := canon.List(canon.Type{Kind: canon.KindU8}, us...)
		if lerr != nil {
			return nil, lerr
		}
		v, verr := canon.Variant(streamResultType(&listU8), "ok", &lst)
		return nil, lowerResult(c, ret, v, verr)
	}
	closed, cerr := canon.Variant(streamErrorType(), "closed", nil)
	if cerr != nil {
		return nil, cerr
	}
	v, verr := canon.Variant(streamResultType(&listU8), "error", &closed)
	return nil, lowerResult(c, ret, v, verr)
}

// errorToDebugString marshals error.to-debug-string: (self: borrow<error>) -> string, flattened to
// (self, ret). It looks the error up in the host error table and lowers its message as a string.
func (h *Host) errorToDebugString(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("component: error.to-debug-string: got %d core args, want 2 (self, ret)", len(args))
	}
	self := args[0].Int32()
	msg := "stream error"
	if e, ok := h.errors[self]; ok {
		msg = e.Error()
	}
	// The string is lowered by the codec's one string lowering (canon.StoreString), against guest memory
	// — the same code the definitions.py string fixtures verify (#719), not a hand path.
	return nil, canon.StoreString(guestHeap{c}, msg, int(uint32(args[1].Bits)))
}

// emptyList marshals a `() -> list<T>` import that this host grants nothing (get-environment,
// get-directories): its sole core argument is the return pointer where the list header (ptr, len) goes.
//
// **It allocates through the guest's realloc even though the list is empty**, because the model does:
// `store_list_into_range` calls `cx.opts.realloc(0, 0, elem_align, byte_length)` and stores the pointer
// it returns for *any* length, zero included (definitions.py). So the header is (realloc(...), 0), not
// (0, 0). align 4 is the element alignment of these list-of-tuple-of-string results.
//
// **The model behavior is generator-pinned; this composes two verified pieces.** PR A's canon
// differential has a `list-u8-empty` case whose committed fixture stores an empty list as header (8, 0)
// — a realloc'd pointer, not zero — matching definitions.py; and the interp control
// TestCanonAdapterReallocAtLengthZeroReturnsTheGuestPointer proves `CanonCaller.Realloc(0,0,align,0)`
// invokes the guest's `cabi_realloc` and returns its pointer. This getter is their composition. p3hello
// reads a zero-length list and never
// touches the backing, so `burroughs run` is insensitive to realloc-vs-(0,0) — which is exactly why the
// fixture, not the end-to-end diff, is the witness for condition 3 (#694).
func (h *Host) emptyList(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: list getter: got %d core args, want 1 (ret)", len(args))
	}
	ptr, err := c.Realloc(0, 0, 4, 0)
	if err != nil {
		return nil, fmt.Errorf("component: empty list backing: %w", err)
	}
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[0:4], ptr) // list pointer; length stays 0
	return nil, c.Write(uint64(uint32(args[0].Bits)), buf)
}
