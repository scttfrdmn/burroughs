// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// A cross-component call: a `canon lower` whose callee is another component's `canon lift` (#888).
//
// # Why this is an adapter and not new subtask machinery
//
// The model's `canon_lower` ends `subtask.on_cancel = callee(on_start, on_resolve)` (definitions.py
// def:2226), where the callee is a **`FuncInst = Callable[[OnStart, OnResolve], OnCancel]`** (def:385) —
// and `Store.lift` produces exactly that from a core func (def:522-528). So the model has this same seam,
// and the lift side of it is already a function of the lower's two callbacks.
//
// **Burroughs' `asyncLowerImpl` is already that type**, with a caller threaded through. Everything
// `asyncLowerFunc` does — STARTING→STARTED on `onStart`, the cancel arm of `onResolve` picking
// CANCELLED_BEFORE_STARTED vs CANCELLED_BEFORE_RETURNED, lowering a result to the retptr, registering the
// subtask and packing `[state | subtaski<<4]` for a callee that has not resolved — is unchanged. The work
// is one adapter, and the measured recon on #888 is what established that rather than a guess.
//
// # What was actually missing, and it was not the subtask protocol
//
// `coreDef.lowerName` is a *host-impl lookup key* derived from `cf.stubName`, so it is populated only when
// the callee is an **unfilled import**. A lower whose callee was a real component func fell through to a
// bare `coreDef{stub: true}` that recorded nothing, every downstream lookup then ran with an empty key,
// and the call refused with `import ::run is not provided (stub host)` — which reads as "the composition
// did not wire the import" and was not that at all. The import resolved fine, to the child's lift; the
// **lower** could not express a non-host callee. `coreDef.lowerCallee` is that expression.

// bindCrossComponent binds a lower whose callee is a sibling component's func.
//
// # Both sorts are handled here, because the hole was sort-agnostic
//
// The fall-through this replaces did not look at `cn.Opts.Async`, so a **sync** guest-to-guest call was
// equally unreachable — and nothing exercised either, because the composed artefacts in the tree asserted
// load and instantiate only. Fixing one sort and leaving the other would leave the same defect behind a
// narrower door, so the sync arm is here too even though #888's subject is the async one.
func (w *walker) bindCrossComponent(d coreDef, m *bin.Module, mod, name string) (interp.Extern, bool) {
	callee := d.lowerCallee
	opts := interp.CanonOptions{Memory: d.lowerMem, Realloc: d.lowerRealloc}

	ft, ok := funcImportType(m, mod, name)
	if !ok {
		return interp.Extern{}, false
	}

	if !d.async {
		// A sync cross-component call: the caller blocks for the callee, which is what sync means, so no
		// goroutine and no subtask. `invokeWith` dispatches on the callee's own `async` flag, so a sync
		// lower of an ASYNC lift still runs the callback loop to completion — which is the correct
		// reading of a sync lower (the model's `canon_lower` with `async_` false asserts the subtask
		// reached RETURNED before returning, def:2227).
		return interp.CanonLowerExtern(ft, func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			res, err := callee.invokeWith(args)
			if err != nil {
				return nil, err
			}
			return res, nil
		}, opts), true
	}

	// The async arm: the flat ABI is the sync signature's params plus a packed i32 return, exactly as the
	// host-impl async arm builds it, and the subtask protocol is reused whole.
	hasResult := callee.sig != nil && callee.sig.Result != nil
	aft := bin.FuncType{Params: ft.Params, Results: []bin.ValType{bin.I32}}
	return interp.CanonLowerExtern(aft, asyncLowerFunc(liftAsAsyncImpl(callee), hasResult, w.async), opts), true
}

// liftAsAsyncImpl adapts a component func whose definition is an async (callback) lift into the
// `asyncLowerImpl` the lower side already consumes.
//
// # The child's lift runs on its own goroutine, and it must
//
// The model's `canon_lift` creates a Thread, resumes it, and returns the cancel trigger **immediately**
// for an async lift; only a sync lift is driven to RESOLVED before returning. **Burroughs'
// `invokeAsyncLiftWith` returns only when the task resolves**, so calling it inline would block the
// parent's lower inside the child's entire task — turning an async cross-component call into a
// synchronous one, which is the opposite of the capability.
//
// So it runs on a goroutine and `onResolve` fires from there. That is exactly the blocking arm's existing
// shape, which is why the substrate accepts it unchanged; what is new is only that the thing on the far
// side is a guest rather than a Go impl.
//
// **This is the second slice in a row to turn on the same engine property**, which is worth naming once
// rather than rediscovering: *Burroughs' component entry points block, so every place the model returns a
// continuation is a place that needs a goroutine.* ADR 0094 hit it from the other direction — the model
// hands the embedder a per-call `OnCancel` and `Invoke` has nowhere to put it.
//
// # Cancellation composes out of #887 rather than needing its own mechanism
//
// The adapter owes an `onCancel`, and the model's is `task.request_cancellation` — which #887 built as
// `liftTask.requestCancelLocked`, with the cancel-aware park and the top-of-loop delivery behind it. So
// the returned closure requests the child's lift-task cancellation through the child's own handle table.
func liftAsAsyncImpl(callee *compFunc) asyncLowerImpl {
	return func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		// `onStart` lifts the caller's flat params and moves the subtask STARTING→STARTED. It must run
		// **before** the goroutine, synchronously, because the model's `on_start` reads the caller's flat
		// args (`CoreValueIter(flat_args)`, def:2193/2210) and those belong to the calling frame — reading
		// them from another goroutine would be a race against the caller's own continuation, and the
		// retptr is in the same argument list.
		params := onStart()

		go func() {
			res, err := callee.invokeAsyncLiftWith(params)
			if err != nil {
				// A child that trapped or was cancelled resolves the parent's subtask as a cancellation
				// rather than a result. **The parent gets a terminal state either way**: leaving the
				// subtask unresolved would park the parent forever on a child that is already gone, which
				// is the one outcome worse than a wrong status.
				//
				// `onResolve`'s cancel arm is reached only when `cancellationRequested` is set, so a
				// child that failed WITHOUT the parent asking is a different case — see the note in
				// `crossComponentResolve`.
				crossComponentResolve(onResolve, callee.sig, nil, err)
				return
			}
			crossComponentResolve(onResolve, callee.sig, res, nil)
		}()

		return func() {
			// The parent's `subtask.cancel` reaching the child's lift task: #887's mechanism, through the
			// CHILD's handle table, which is its own (`link_component.go` gives every nested
			// instantiation its own `newAsyncHandles`). A refusal is dropped here deliberately — the model
			// gives `on_cancel` no return value (`OnCancel = Callable[[], None]`, def:384), and the only
			// refusals `requestCancelAll` produces are "nothing running" and "not started", both of which
			// mean the race is already lost and the subtask will resolve on its own.
			_ = callee.h.requestCancelAll()
		}, nil
	}
}

// crossComponentResolve is the single place a child's outcome becomes the parent's subtask resolution.
//
// Named rather than inlined so the two call sites cannot drift, and because the result lift is the one
// piece of this slice with no prior code path — the piece where a wrong implementation is **silently**
// wrong rather than loudly broken, since a mis-lifted result is a value of the right type and the wrong
// contents.
func crossComponentResolve(onResolve func(canon.Value), sig *FuncType, res []interp.Value, callErr error) {
	if callErr != nil {
		// The model's `on_resolve(None)` (def:2214) — no result. Burroughs' `onResolve` reads
		// `cancellationRequested` to choose the terminal state, so this is the cancel arm when the parent
		// asked. When it did not ask, this is a RETURNED carrying nothing, which is **not ideal and not
		// this slice's to invent**: there is no ABI status for "the callee trapped", the model traps the
		// whole store instead, and a status no reading has would be worse than a thin one.
		onResolve(canon.Value{})
		return
	}
	v, err := crossComponentValue(sig, res)
	if err != nil {
		// Refused rather than guessed. Reaching here means the child returned a shape this slice does
		// not lift, and resolving with a fabricated value would hand the parent a plausible wrong answer.
		onResolve(canon.Value{})
		return
	}
	onResolve(v)
}

// ErrCrossComponentResult refuses a cross-component result this slice cannot carry.
//
// # Why the scope is scalars, and why that is a boundary rather than laziness
//
// A child's `task.return` hands over **flat core values in the child's own ABI**. For a scalar that is
// the value itself. For anything aggregate — a string, a list, a record — the flat value is a *pointer
// into the child's memory*, which the parent cannot read: the two components have different memories and
// different allocators, so carrying one across means copying through the parent's `realloc` with the
// child's memory as the source.
//
// That copy is real work with its own witnesses, and it is **not** what #888 registered. The artefacts in
// the tree return `u32` (`receipt/`'s `run: async func(id: u32) -> u32`), so scalars are what the measured
// cases need. An aggregate refuses **by name at the resolution**, so the boundary is a message a reader
// meets rather than a silent truncation — the same discipline `ErrTaskCancelUnbuilt` applies to an
// unmeasured branch.
var ErrCrossComponentResult = fmt.Errorf("%w: cross-component result shape is not carried yet", ErrUnsupportedForm)

// crossComponentValue lifts a child lift's flat results into the component value the parent's lower
// lowers to its retptr, using the CHILD's own lift signature as the type.
//
// The signature is the child's and not the parent's import declaration on purpose: the flat values came
// out of the child's `task.return`, so the child's result type is what describes them. They agree when the
// composition is well-typed, and when they do not, the one that describes the bytes is the right oracle.
func crossComponentValue(sig *FuncType, res []interp.Value) (canon.Value, error) {
	if sig == nil || sig.Result == nil {
		// No result: `hasResult` is false at the bind site, so the lower ignores the value entirely. An
		// explicit empty is returned rather than an error, because "returns nothing" is a shape this
		// carries perfectly — grave #885's guest is exactly it.
		return canon.Value{}, nil
	}
	if len(res) != 1 {
		return canon.Value{}, fmt.Errorf("%w: child returned %d flat values for a %s result, want 1",
			ErrCrossComponentResult, len(res), valKindName(sig.Result.Kind))
	}
	bits := res[0].Bits
	switch sig.Result.Kind {
	case VBool:
		return canon.Bool(uint32(bits) != 0), nil
	case VU8:
		return canon.U8(uint8(bits)), nil
	case VU16:
		return canon.U16(uint16(bits)), nil
	case VU32:
		return canon.U32(uint32(bits)), nil
	case VU64:
		return canon.U64(bits), nil
	case VS8:
		return canon.S8(int8(bits)), nil
	case VS16:
		return canon.S16(int16(bits)), nil
	case VS32:
		return canon.S32(int32(uint32(bits))), nil
	case VS64:
		return canon.S64(int64(bits)), nil
	default:
		// Every aggregate, plus char/string/f32/f64, lands here. The refusal names the kind so a reader
		// meeting it knows which shape to build next rather than that "something" was unsupported.
		return canon.Value{}, fmt.Errorf("%w: %s — a non-scalar result is a pointer into the CHILD's "+
			"memory, so carrying it means copying through the parent's realloc (#888 scoped this out)",
			ErrCrossComponentResult, valKindName(sig.Result.Kind))
	}
}
