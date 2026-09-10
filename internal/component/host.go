// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The real preview-2 host (PR C.2). The stub host (link_component.go) refuses every wasi import by name;
// this host provides the ten the guest-driven `wasi:cli/run` world reaches, marshaling core arguments
// against each import's Canonical-ABI-flattened signature (verified from p3hello's own core-module
// import declarations). A stream's `result<_, stream-error>` and `result<u64, stream-error>` flatten
// past one core value, so the guest passes a return pointer and the host writes the lifted result into
// guest memory there; the `ok` arm is written and the `err` arm is unreachable on the success path
// (its `own<error>` payload is refused at lower time — the deferral recorded on #694).
//
// A byte string crosses through the codec's `list<u8>` semantics: the guest hands a (ptr, len) pair and
// the host reads exactly those bytes from the caller's memory (canon `load_list` over u8). Stream
// handles are minted in a per-host resource table (the first guest-driven exercise of an own handle
// against a real host resource); a borrow of one resolves back to the writer it names.

// Host is a preview-2 host: the standard streams and a resource table of the stream handles it has
// minted. Exited/ExitCode record a guest `exit` call.
type Host struct {
	Stdout   io.Writer
	Stderr   io.Writer
	Stdin    io.Reader
	writers  map[int32]io.Writer // output-stream handles → the writer they name
	next     int32
	Exited   bool
	ExitCode int32
}

// NewHost builds a preview-2 host over the given streams. Handles are minted from 1 (0 is left unused so
// a zero value is never a live handle).
func NewHost(stdout, stderr io.Writer, stdin io.Reader) *Host {
	return &Host{Stdout: stdout, Stderr: stderr, Stdin: stdin, writers: map[int32]io.Writer{}, next: 1}
}

// mintWriter records a writer in the resource table and returns its handle.
func (h *Host) mintWriter(w io.Writer) int32 {
	id := h.next
	h.next++
	h.writers[id] = w
	return id
}

// wasi builds the import-name → marshaling-impl table the walk consults for a canon-lowered func. The
// keys are the "module::export" identities the loader records; a lowered func whose identity is absent
// still reaches the refusing stub (an unexercised import remains refused, by name).
func (h *Host) wasi() map[string]interp.HostFunc {
	m := map[string]interp.HostFunc{
		"wasi:cli/stdout@0.2.3::get-stdout": h.getWriter(func() io.Writer { return h.Stdout }),
		"wasi:cli/stderr@0.2.3::get-stderr": h.getWriter(func() io.Writer { return h.Stderr }),
		"wasi:cli/stdin@0.2.3::get-stdin":   h.getWriter(func() io.Writer { return io.Discard }),

		"wasi:io/streams@0.2.3::[method]output-stream.blocking-write-and-flush": h.streamWrite,
		"wasi:io/streams@0.2.3::[method]output-stream.write":                    h.streamWrite,
		"wasi:io/streams@0.2.3::[method]output-stream.check-write":              h.checkWrite,
		"wasi:io/streams@0.2.3::[method]output-stream.blocking-flush":           h.okResult(8),

		"wasi:cli/exit@0.2.3::exit": h.exit,
	}
	// The resource-drop intrinsics the world lowers are no-ops here: the table entry is left in place
	// (the streams are the process's, not the guest's to free), so a drop neither refuses nor frees.
	for _, name := range []string{
		"wasi:io/streams@0.2.3::[resource-drop]output-stream",
		"wasi:io/streams@0.2.3::[resource-drop]input-stream",
		"wasi:io/error@0.2.3::[resource-drop]error",
		"wasi:filesystem/types@0.2.3::[resource-drop]descriptor",
	} {
		m[name] = h.noop
	}
	return m
}

// getWriter returns a get-{stdout,stderr,stdin} impl: no arguments, one result — an own handle to a
// freshly minted stream resource (the codec's own-handle lower is a bare i32 handle).
func (h *Host) getWriter(pick func() io.Writer) interp.HostFunc {
	return func(_ *interp.Caller, _ []interp.Value) ([]interp.Value, error) {
		return []interp.Value{interp.I32(h.mintWriter(pick()))}, nil
	}
}

// streamWrite marshals output-stream.write / blocking-write-and-flush: (self: borrow<output-stream>,
// contents: list<u8>) -> result<_, stream-error>, flattened to (self, ptr, len, ret). It lifts the
// list<u8> from the caller's memory (canon load over u8), writes it to the stream `self` names, and
// writes the `ok` result at the return pointer.
func (h *Host) streamWrite(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
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
	if _, err := w.Write(buf); err != nil {
		return nil, fmt.Errorf("component: stream write: %w", err)
	}
	// result<_, stream-error>: discriminant i32 (0 = ok) then the payload slot; the ok arm has no payload.
	return nil, c.Write(ret, make([]byte, 8))
}

// checkWrite marshals output-stream.check-write: (self) -> result<u64, stream-error>, flattened to
// (self, ret). It reports a large writable budget (the ok arm), so the guest never blocks on capacity.
func (h *Host) checkWrite(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("component: check-write: got %d core args, want 2 (self, ret)", len(args))
	}
	ret := uint64(uint32(args[1].Bits))
	// result<u64, stream-error>: discriminant i32 @0, then the u64 payload at offset 8 (8-byte aligned).
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint64(buf[8:], 1<<30)
	return nil, c.Write(ret, buf)
}

// okResult returns an impl for a (self) -> result<_, stream-error> method (e.g. blocking-flush): it
// writes `size` zero bytes at the return pointer — the ok discriminant and its (empty) payload slot.
func (h *Host) okResult(size int) interp.HostFunc {
	return func(c *interp.Caller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("component: stream method: got %d core args, want 2 (self, ret)", len(args))
		}
		return nil, c.Write(uint64(uint32(args[1].Bits)), make([]byte, size))
	}
}

// exit marshals cli/exit.exit: (status: result<_, _>) -> (), flattened to a single discriminant i32
// (0 = ok/success, 1 = error). It records the exit rather than terminating the process.
func (h *Host) exit(_ *interp.Caller, args []interp.Value) ([]interp.Value, error) {
	h.Exited = true
	if len(args) == 1 && args[0].Int32() != 0 {
		h.ExitCode = 1
	}
	return nil, nil
}

// noop is a resource-drop intrinsic: it accepts the handle argument and returns, freeing nothing.
func (h *Host) noop(_ *interp.Caller, _ []interp.Value) ([]interp.Value, error) { return nil, nil }
