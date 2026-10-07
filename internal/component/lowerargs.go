// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// Lowering an argument that does not fit in flat words (#902, ADR 0098).
//
// # Where the lowering happens, and why it is not where the flat args are built
//
// The model lowers a lift's parameters **inside the callee's task**: `canon_lift`'s thread body runs
// `enter_implicit_thread()`, then `task.start()`, then `lower_flat_values(...)`, and only then calls the
// core function (definitions.py:2097-2107). The lowering is therefore inside the callee's exclusive
// context, not before it.
//
// Burroughs' analogue of that context is the instance's **entry slot** (`asyncHandles.entrySem`), and the
// consequence is the shape of this file: a `string` argument cannot be lowered in `CallValuesCtx`, where
// the other arguments are flattened, because nothing holds the entry there. A sibling call could enter
// the instance between the allocation and the call that uses it — and the guest is entitled to reuse or
// free anything it likes on an entry it owns.
//
// So the flat argument list is built with **placeholders** for the words a pending lowering will fill,
// and the lowering itself runs inside `runLiftTask`, on the same entry as the first callee call.
//
// # Ownership: the callee owns it, and the host never frees
//
// The model allocates a lowered argument through the callee's own realloc and never releases it: a
// lowered value belongs to the callee once the call is made, and the callee's `post-return` (sync) or its
// own bookkeeping (async) is what reclaims it. `lower_flat_values` has no matching free, and there is no
// "unlower" anywhere in the model.
//
// So this engine allocates and writes, and then **does not touch the allocation again** — no free, no
// re-read, no retained pointer. Stated because the opposite instinct is strong: a host that allocated
// something usually cleans it up, and doing so here would free memory the guest still owns.

// pendingLower is one argument whose bytes must be placed in guest memory before the callee runs.
//
// It carries the bytes and the **flat slots to patch**, rather than a closure, so that the work done
// inside the entry is data a reader can see: allocate `len(bytes)`, write them, put the pointer in
// `ptrSlot` and the length in `lenSlot`.
type pendingLower struct {
	param   string // the parameter's name, for a refusal that says which argument failed
	bytes   []byte
	align   int
	ptrSlot int
	lenSlot int
}

// lowerPending performs the pending lowerings and patches `flat` in place.
//
// It must be called **holding the instance's entry slot** and before the callee is invoked. The caller
// is `runLiftTask`, which holds exactly that; this function does not acquire it, because acquiring it
// here would mean releasing it between the allocation and the call — the thing the entry exists to
// prevent.
func (f *compFunc) lowerPending(pend []pendingLower, flat []interp.Value) error {
	for i := range pend {
		p := &pend[i]
		ptr, err := f.guestAlloc(p)
		if err != nil {
			return err
		}
		// **Written through the boundary accessor**, which takes ADR 0073's growth lock and resolves the
		// image inside it. That ordering is load-bearing here rather than merely correct: `cabi_realloc`
		// may have **grown** the memory to satisfy this very allocation, so a base and length captured
		// before the call would describe an image that no longer exists.
		if werr := interp.WriteBoundaryMemory(f.mem, uint64(ptr), p.bytes); werr != nil {
			return fmt.Errorf("%w: writing argument %q's %d byte(s) at %d: %w",
				ErrUnsupportedForm, p.param, len(p.bytes), ptr, werr)
		}
		if p.ptrSlot >= len(flat) || p.lenSlot >= len(flat) {
			return fmt.Errorf("%w: argument %q names flat slots %d/%d of %d",
				ErrUnsupportedForm, p.param, p.ptrSlot, p.lenSlot, len(flat))
		}
		flat[p.ptrSlot] = interp.I32(int32(uint32(ptr)))
		flat[p.lenSlot] = interp.I32(int32(uint32(len(p.bytes))))
	}
	return nil
}

// guestAlloc calls the lift's `cabi_realloc` to obtain `len(p.bytes)` bytes in guest memory.
//
// # An ordinary guest call, deliberately
//
// Through `inst.Invoke`, which is the same path an embedder's call takes: it creates the thread and
// stack, runs the safepoint poll, and is visible to a stop-the-world. `CanonCaller.Realloc` is the other
// way to reach a guest realloc and is **not** usable here — it works off a live call's stack and thread
// (`c.st`, `c.t`), which exist only inside a guest call, and argument lowering happens before one.
//
// Synthesizing a caller to borrow that path was the alternative and is refused: it would be a second
// route into guest code that the safepoint and lock controls have never been checked against.
//
// The four arguments are the canonical ABI's `cabi_realloc(orig_ptr, orig_size, align, new_size)`, with
// the first two zero because this is a fresh allocation rather than a resize.
func (f *compFunc) guestAlloc(p *pendingLower) (int32, error) {
	if f.reallocCore.inst == nil {
		return 0, fmt.Errorf("%w: argument %q is a %d-byte value that must live in guest memory, but this "+
			"lift declares no (realloc) canonopt — there is nowhere to put it",
			ErrUnsupportedForm, p.param, len(p.bytes))
	}
	if f.mem == nil {
		return 0, fmt.Errorf("%w: argument %q must live in guest memory, but this lift declares no "+
			"(memory) canonopt", ErrUnsupportedForm, p.param)
	}
	res, err := f.reallocCore.inst.Invoke(f.reallocCore.name,
		interp.I32(0), interp.I32(0), interp.I32(int32(p.align)), interp.I32(int32(len(p.bytes))))
	if err != nil {
		// A realloc that traps is the guest refusing the allocation, and it is reported as the guest's
		// failure rather than translated: the error carries the trap, so an embedder sees what the guest
		// did and not a paraphrase of it.
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc failed for %d byte(s): %w",
			ErrUnsupportedForm, p.param, len(p.bytes), err)
	}
	if len(res) != 1 {
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc returned %d value(s), want 1",
			ErrUnsupportedForm, p.param, len(res))
	}
	ptr := res[0].Int32()
	// A realloc that answers 0 for a non-empty request has not allocated anything, and writing at 0
	// would corrupt whatever the guest keeps at the bottom of its memory. An empty request legitimately
	// gets any pointer, including 0, and writes nothing.
	if ptr == 0 && len(p.bytes) > 0 {
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc returned a null pointer for %d byte(s)",
			ErrUnsupportedForm, p.param, len(p.bytes))
	}
	if ptr < 0 {
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc returned %d, which is not an address",
			ErrUnsupportedForm, p.param, ptr)
	}
	return ptr, nil
}
