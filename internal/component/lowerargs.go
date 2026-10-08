// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
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

// argHeap is a [canon.Heap] over a lift's own `(realloc)` and `(memory)` canonopts, so an argument is
// lowered **by the codec** rather than by a hand path here.
//
// # Why this exists rather than serialising bytes locally
//
// A `string` argument could be lowered by hand — its bytes are its bytes. A `list<u32>` could too, by
// packing little-endian words. A `list<string>` could not, and a `list<record>` certainly not: each needs
// the element framing the codec already owns and the differential already verifies. Writing the easy
// cases by hand is how the engine would end up with two list lowerings, which is the thing
// `StoreList`'s injected element store exists to prevent.
//
// So the argument path builds this and calls `canon.StoreStringIntoRange` /
// `canon.StoreListIntoRange` — the model's `*_into_range` forms, which return the `(ptr, len)` pair for
// the flat slots instead of writing it at a pointer.
//
// **Every method runs inside the callee's entry**, because `argHeap` is only ever used from
// `lowerPending`, which `enterInvokeLowering` calls while holding the entry slot. `Realloc` enters guest
// code (an ordinary `Invoke` of `cabi_realloc`); the writes do not.
type argHeap struct {
	f     *compFunc
	param string
}

func (a argHeap) Realloc(_, _, align, newSize int) (int, error) {
	return a.f.guestAlloc(&pendingLower{param: a.param, align: align, byteLen: newSize})
}

func (a argHeap) WriteBytes(ptr int, data []byte) error {
	return interp.WriteBoundaryMemory(a.f.mem, uint64(ptr), data)
}

func (a argHeap) StoreInt(v uint64, ptr, nbytes int) error {
	buf := make([]byte, nbytes)
	for i := range nbytes {
		buf[i] = byte(v >> (8 * i))
	}
	return interp.WriteBoundaryMemory(a.f.mem, uint64(ptr), buf)
}

// pendingLower is one argument whose payload must be placed in guest memory before the callee runs.
//
// It carries the **value** and the flat slots to patch, rather than pre-serialised bytes: the codec is
// what knows how a value becomes bytes, and a `list<string>` has no single byte string to hand over.
// `byteLen` and `align` are set when the record is used as `argHeap.Realloc`'s request rather than as a
// pending argument — the two uses share the parameter name, which is the only thing a refusal needs.
type pendingLower struct {
	param   string // the parameter's name, for a refusal that says which argument failed
	val     canon.Value
	align   int
	byteLen int
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
		h := argHeap{f: f, param: p.param}

		// **The codec does the lowering.** Both arms are the model's `*_into_range` forms: they allocate
		// through the guest's realloc, write through the boundary accessor (which takes ADR 0073's growth
		// lock and resolves the image *inside* it — load-bearing, because the realloc may have grown the
		// memory to satisfy this very allocation), and hand back the pair for the flat slots.
		var (
			ptr, length int
			err         error
		)
		switch p.val.Type.Kind {
		case canon.KindString:
			s, ok := p.val.Str()
			if !ok {
				return fmt.Errorf("%w: argument %q is tagged string but does not read as one",
					ErrUnsupportedForm, p.param)
			}
			ptr, length, err = canon.StoreStringIntoRange(h, s)
		case canon.KindList:
			// The element store is **injected**, and it is `StoreVia` — the same composable store the
			// host's WASI lowerings use and the differential verifies. So `list<u32>` today and
			// `list<string>` when a guest wants one go through one framing rather than two loops.
			ptr, length, err = canon.StoreListIntoRange(h, p.val, func(e canon.Value, at int) error {
				return canon.StoreVia(h, e, at)
			})
		default:
			return fmt.Errorf("%w: argument %q is a %s, which does not need guest memory and should not "+
				"have been deferred to the entry", ErrUnsupportedForm, p.param, p.val.Type.Kind)
		}
		if err != nil {
			return fmt.Errorf("%w: lowering argument %q: %w", ErrUnsupportedForm, p.param, err)
		}

		if p.ptrSlot >= len(flat) || p.lenSlot >= len(flat) {
			return fmt.Errorf("%w: argument %q names flat slots %d/%d of %d",
				ErrUnsupportedForm, p.param, p.ptrSlot, p.lenSlot, len(flat))
		}
		flat[p.ptrSlot] = interp.I32(int32(uint32(ptr)))
		flat[p.lenSlot] = interp.I32(int32(uint32(length)))
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
// checkLowerSize refuses a payload too large to be worth asking the guest for, **before** the realloc.
//
// # This is stricter than the model, and the reason is worth stating
//
// On the **store** side the model only asserts `dst_byte_length <= REALLOC_I32_MAX` (2³²−1,
// definitions.py:1597) — an `assert`, not a `trap_if`. The 2²⁸−1 cap is the **load** side's trap
// (def:1383, `MAX_STRING_BYTE_LENGTH`), and it is the one this engine already applies in
// `canon.LoadStringFromRange`.
//
// So refusing at the load cap here declines a string the model would store. That is deliberate: a string
// past that cap **can never be read back as one** — any `load_string` of it traps — so it is unusable in
// both directions, and the model's own `assert(REALLOC_I32_MAX > 2 * MAX_STRING_BYTE_LENGTH)` (def:1361)
// is the statement that the two bounds are meant to sit in that relation. Asking a guest for a
// quarter-gigabyte allocation it is going to refuse is worse than declining before the call.
//
// **Factored so it can be called on its own**, which is what makes it testable without allocating a
// 256 MiB string: the check is about the length, so the test supplies a length.
//
// A `checkLowerCount` companion — `canon.ListByteLength` followed by this, so a `list<T>` and a `string`
// are bounded by one rule at one point — belongs beside it and is **deliberately not written yet**: the
// list *argument* lowering that would call it does not exist, and `deadcode` said so when it was written
// speculatively. The overflow guard itself is live, consumed by `canon.LoadList` on the lifting side of
// the same bound. *Decline speculative API with a consumer trigger*; the trigger is the list argument
// path.
func checkLowerSize(param string, n int) error {
	if n < 0 {
		return fmt.Errorf("%w: argument %q has a negative byte length %d", ErrUnsupportedForm, param, n)
	}
	if n > canon.MaxStringByteLength {
		return fmt.Errorf("%w: argument %q is %d bytes, past the %d-byte cap a component string can "+
			"carry; a longer one could be stored but never read back, because a load of it traps "+
			"(definitions.py:1383), so it is refused here rather than allocated for",
			ErrUnsupportedForm, param, n, canon.MaxStringByteLength)
	}
	return nil
}

func (f *compFunc) guestAlloc(p *pendingLower) (int, error) {
	// **Before the realloc**, so the guest is never asked for an allocation this engine would refuse to
	// use. The order is the point: checking after would mean a guest had already grown its memory by a
	// quarter of a gigabyte to satisfy a request about to be rejected.
	if err := checkLowerSize(p.param, p.byteLen); err != nil {
		return 0, err
	}
	if f.reallocCore.inst == nil {
		return 0, fmt.Errorf("%w: argument %q is a %d-byte value that must live in guest memory, but this "+
			"lift declares no (realloc) canonopt — there is nowhere to put it",
			ErrUnsupportedForm, p.param, p.byteLen)
	}
	if f.mem == nil {
		return 0, fmt.Errorf("%w: argument %q must live in guest memory, but this lift declares no "+
			"(memory) canonopt", ErrUnsupportedForm, p.param)
	}
	res, err := f.reallocCore.inst.Invoke(f.reallocCore.name,
		interp.I32(0), interp.I32(0), interp.I32(int32(p.align)), interp.I32(int32(p.byteLen)))
	if err != nil {
		// A realloc that traps is the guest refusing the allocation, and it is reported as the guest's
		// failure rather than translated: the error carries the trap, so an embedder sees what the guest
		// did and not a paraphrase of it.
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc failed for %d byte(s): %w",
			ErrUnsupportedForm, p.param, p.byteLen, err)
	}
	if len(res) != 1 {
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc returned %d value(s), want 1",
			ErrUnsupportedForm, p.param, len(res))
	}
	ptr := res[0].Int32()
	// A realloc that answers 0 for a non-empty request has not allocated anything, and writing at 0
	// would corrupt whatever the guest keeps at the bottom of its memory. An empty request legitimately
	// gets any pointer, including 0, and writes nothing.
	if ptr == 0 && p.byteLen > 0 {
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc returned a null pointer for %d byte(s)",
			ErrUnsupportedForm, p.param, p.byteLen)
	}
	if ptr < 0 {
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc returned %d, which is not an address",
			ErrUnsupportedForm, p.param, ptr)
	}
	// **The misalignment trap** (definitions.py:1599, `trap_if(ptr != align_to(ptr, dst_alignment))`).
	//
	// **Vacuous for a `string`**, whose alignment is 1 — every pointer satisfies it. Implemented anyway,
	// and said to be vacuous, because the first kind whose alignment is not 1 is a `list<u32>` at
	// alignment 4, and a check added at that point would be a check nothing had ever run. The model
	// states it unconditionally and so does this.
	if p.align > 0 && int(ptr)%p.align != 0 {
		return 0, fmt.Errorf("%w: argument %q: the guest's realloc returned %d, which is not aligned to "+
			"%d", ErrUnsupportedForm, p.param, ptr, p.align)
	}
	// Returned as an `int` because that is the codec's offset type, converted here at the one edge where
	// the guest's i32 becomes a host offset.
	return int(ptr), nil
}
