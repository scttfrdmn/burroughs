// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
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
// core function (definitions.py:canon_lift.thread_func). The lowering is therefore inside the callee's exclusive
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
	// kind is the ARGUMENT's kind, not the allocation's — a `list<string>`'s element allocations come
	// through here too. It selects which byte-length cap applies, which is a property of the argument
	// being lowered rather than of each realloc it makes: a string's cap is the load side's 2²⁸−1 (ADR
	// 0098's recorded divergence), and anything else is bounded only by `REALLOC_I32_MAX`.
	kind canon.Kind
}

// allocRequest is one `cabi_realloc` call's worth of intent.
//
// **Split out of `pendingLower` on the #924 review**, which caught that one struct was doing two jobs —
// a pending argument and an allocation request — with a comment conceding the two "share the parameter
// name". A struct whose fields mean different things depending on the caller is how a field ends up set
// for one use and read in the other, and the two uses here had already diverged: a pending argument
// carries a value and flat slots, a request carries a size and an alignment, and neither needs the
// other's fields.
type allocRequest struct {
	param   string     // the argument's name, so a refusal says which one failed
	kind    canon.Kind // the argument's kind, for the cap
	align   int
	byteLen int
}

func (a argHeap) Realloc(_, _, align, newSize int) (int, error) {
	return a.f.guestAlloc(allocRequest{param: a.param, kind: a.kind, align: align, byteLen: newSize})
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

// liftHeap is the read side of a lift's own memory, for a sync callee's spilled result (ADR 0103). The
// callee has returned by the time its result is lifted, so there is no `CanonCaller` to read through.
// The boundary accessors take ADR 0073's growth lock and copy, as `argHeap`'s writes do.
type liftHeap struct{ f *compFunc }

func (l liftHeap) ReadBytes(ptr, n int) ([]byte, error) {
	if ptr < 0 || n < 0 {
		return nil, fmt.Errorf("component: lift read of %d byte(s) at %d: a negative offset is not an address", n, ptr)
	}
	return interp.ReadBoundaryMemory(l.f.mem, uint64(uint32(ptr)), uint64(uint32(n)))
}

func (l liftHeap) CheckRange(ptr, n int) error {
	if ptr < 0 || n < 0 {
		return fmt.Errorf("component: lift range of %d byte(s) at %d: a negative offset is not an address", n, ptr)
	}
	return interp.CheckBoundaryRange(l.f.mem, uint64(uint32(ptr)), uint64(uint32(n)))
}

// pacedRead and pacedWrite are a heap whose every read and write polls the calling agent's safepoint
// first: ADR 0103's sync adapter copies, which run in host code on the caller's agent (contract §3 SP-1,
// ADR 0104). A copy through one is never more than one heap call from a poll, because the codec bounds a
// call's size (`canon.StringChunk`) and a list moves element by element.
//
// **`Realloc` does not poll here**, because the realloc it reaches is a guest frame and polls at its entry.
//
// **Only the sync adapter wraps its heaps**, and not every user of `guestHeap`. An async lower's result store
// can run on the goroutine of whichever agent holds the run slot (`apply`, async_lower.go), and a poll there
// would park the CALLER's thread from another agent's goroutine, possibly while that caller holds a blocked
// mark. That counts one caller twice in `atSafepointLocked`.
//
// NEUTER BOTH POLLS and `TestAStopDuringAnAdapterCopyArrivesWithinTheBound` misses its bound.
type pacedRead struct {
	h    canon.ReadHeap
	poll func()
}

func (p pacedRead) ReadBytes(ptr, n int) ([]byte, error) {
	p.poll()
	return p.h.ReadBytes(ptr, n)
}

func (p pacedRead) CheckRange(ptr, n int) error { return p.h.CheckRange(ptr, n) }

type pacedWrite struct {
	h    canon.Heap
	poll func()
}

func (p pacedWrite) Realloc(origPtr, origSize, align, newSize int) (int, error) {
	return p.h.Realloc(origPtr, origSize, align, newSize)
}

func (p pacedWrite) WriteBytes(ptr int, data []byte) error {
	p.poll()
	return p.h.WriteBytes(ptr, data)
}

func (p pacedWrite) StoreInt(v uint64, ptr, nbytes int) error {
	p.poll()
	return p.h.StoreInt(v, ptr, nbytes)
}

// callerPollKey carries a sync cross-component caller's safepoint poll into the callee's `invokeSync`, whose
// argument lowering and result lift run on the caller's agent (ADR 0104).
type callerPollKey struct{}

func withCallerPoll(ctx context.Context, poll func()) context.Context {
	return context.WithValue(ctx, callerPollKey{}, poll)
}

// callerPollFrom is the poll ctx carries, or nil when the call has no waiting caller agent.
func callerPollFrom(ctx context.Context) func() {
	poll, _ := ctx.Value(callerPollKey{}).(func())
	return poll
}

// pendingLower is one argument whose payload must be placed in guest memory before the callee runs.
//
// It carries the **value** and the flat slots to patch, rather than pre-serialised bytes: the codec is
// what knows how a value becomes bytes, and a `list<string>` has no single byte string to hand over.
// It carries **only** what a pending argument needs. The allocation request it eventually makes is
// [allocRequest], a separate type — see that type for why they were one and are not.
type pendingLower struct {
	param   string // the parameter's name, for a refusal that says which argument failed
	val     canon.Value
	ptrSlot int
	lenSlot int
}

// lowerPending performs the pending lowerings and patches `flat` in place.
//
// It must be called **holding the instance's exclusive region** and before the callee is invoked. There
// are two callers, each holding it:
//   - `runLiftTask` for an async lift, holding its entry;
//   - `invokeSync` for a sync lift, holding the run slot (ADR 0103).
//
// This function does not acquire the region, because acquiring it here would mean releasing it between the
// allocation and the call, the thing the region exists to prevent.
//
// `poll` is the calling agent's safepoint poll when a caller's agent is waiting on this lowering, and nil
// when none is: `invokeSync` passes the sync adapter's (ADR 0104), and an async lift's entry passes nil.
func (f *compFunc) lowerPending(pend []pendingLower, flat []interp.Value, poll func()) error {
	for i := range pend {
		p := &pend[i]
		// The heap carries the ARGUMENT's kind, so every allocation the codec makes while lowering it —
		// including a `list<string>`'s per-element string allocations — is capped by the rule that
		// belongs to the argument rather than by one guessed per realloc.
		var h canon.Heap = argHeap{f: f, param: p.param, kind: p.val.Type.Kind}
		if poll != nil {
			h = pacedWrite{h, poll}
		}

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
// definitions.py:store_string_copy:"dst_byte_length <= REALLOC_I32_MAX") — an `assert`, not a `trap_if`. The 2²⁸−1 cap is the **load** side's trap
// (def:load_string_from_range:"trap_if(byte_length > MAX_STRING_BYTE_LENGTH)", `MAX_STRING_BYTE_LENGTH`), and it is the one this engine already applies in
// `canon.LoadStringFromRange`.
//
// So refusing at the load cap here declines a string the model would store. That is deliberate: a string
// past that cap **can never be read back as one** — any `load_string` of it traps — so it is unusable in
// both directions, and the model's own module-level
// def:"assert(REALLOC_I32_MAX > 2 * MAX_STRING_BYTE_LENGTH)" is the statement that the two bounds are meant to sit in that relation. Asking a guest for a
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
// **The cap depends on the kind, which it did not until the #924 review.** A single string-shaped cap
// applied to every allocation, so a `list<u32>` of 100 million elements — 400 MB, which the model allows
// and which nothing stops a guest reading back — was refused with a message about strings.
//
//   - A **string** keeps the load-side 2²⁸−1 cap and ADR 0098's recorded divergence: a longer one could
//     be stored and never read back, because `load_string` traps past it.
//   - **Everything else** is bounded only by `REALLOC_I32_MAX`, which is the model's own bound on a
//     store (definitions.py:store_list_into_range:"assert(byte_length <= REALLOC_I32_MAX)") and is already enforced inside `canon.ListByteLength`. So a list needs
//     no second cap here, and imposing one would be this engine inventing a limit the ABI does not have.
func checkLowerSize(param string, kind canon.Kind, n int) error {
	if n < 0 {
		return fmt.Errorf("%w: argument %q has a negative byte length %d", ErrUnsupportedForm, param, n)
	}
	if kind == canon.KindString && n > canon.MaxStringByteLength {
		return fmt.Errorf("%w: argument %q is a %d-byte string, past the %d-byte cap a component string "+
			"can carry; a longer one could be stored but never read back, because a load of it traps "+
			"(definitions.py:load_string_from_range), so it is refused here rather than allocated for",
			ErrUnsupportedForm, param, n, canon.MaxStringByteLength)
	}
	if uint64(n) > canon.ReallocI32Max {
		return fmt.Errorf("%w: argument %q needs %d bytes, past the %d the Canonical ABI's 32-bit pointer "+
			"space can address", ErrUnsupportedForm, param, n, canon.ReallocI32Max)
	}
	return nil
}

func (f *compFunc) guestAlloc(p allocRequest) (int, error) {
	// **Before the realloc**, so the guest is never asked for an allocation this engine would refuse to
	// use. The order is the point: checking after would mean a guest had already grown its memory by a
	// quarter of a gigabyte to satisfy a request about to be rejected.
	if err := checkLowerSize(p.param, p.kind, p.byteLen); err != nil {
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
	// Through `InvokeMayNotLeave` (#990): the reference's `reallocate` clears `may_leave` for the call, so a
	// built-in or lowered import the realloc calls traps.
	res, err := f.reallocCore.inst.InvokeMayNotLeave(f.reallocCore.name,
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
	// **The misalignment trap** (definitions.py:LiftLowerContext.reallocate:"trap_if(ptr != align_to(ptr, alignment))", `reallocate`'s `trap_if(ptr != align_to(ptr, alignment))`;
	// at the previous pin, 2bed77e, the same check sat at each call site as `align_to(ptr, dst_alignment)`).
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
