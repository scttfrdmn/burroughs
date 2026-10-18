// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
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
// def:canon_lower:"subtask.on_cancel = callee(on_start, on_resolve)"), where the callee is a **`FuncInst = Callable[[OnStart, OnResolve], OnCancel]`** (def:FuncInst) —
// and `Store.lift` produces exactly that from a core func (def:Store.lift). So the model has this same seam,
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
		// reached RETURNED before returning, def:canon_lower:"assert(ft.async_ or subtask.state == Subtask.State.RETURNED)").
		// # A sync lower of an ASYNC lift is refused by name, and there is deliberately no excursion here
		//
		// The transitive half of the `c.Blocking` audit (§5 H-1, grave #892's slice) flagged this arm: a
		// lexical scan finds no `select`, but `invokeWith` dispatches on the **callee's** `async` flag, so
		// a sync lower of an async lift would run the whole callback loop — `awaitEvent`'s park included,
		// up to `liftParkBound` — on this goroutine with the agent inside guest execution and unmarked.
		//
		// **Checking the premise retired the finding rather than confirming it.** A sync `canon lower`
		// cannot target an async lift in a well-formed component: the lower must match the import's
		// asyncness, which is recorded as a measured refusal in `testdata/compose/parent-supplied.wat`
		// (*"The `canon lower` must carry **`async`** to match"*). So the path is unreachable upstream.
		//
		// **And wrapping it in `c.Blocking` would have been wrong, not merely unnecessary.** A sync
		// cross-component call runs *guest* code; `Blocking` means "this agent is in host code", and the
		// mark tells a concurrent `Stop` the agent is at a safepoint. Marking it while a callee guest
		// executes would let a stop round report a stopped world over running guest code — the opposite
		// of the property the audit was protecting. A first attempt did add the wrapper here, on the
		// strength of the finding and before checking either of these.
		//
		// So the arm **refuses an async callee by name** instead. That is cheaper than an excursion and
		// strictly more honest: it says the component is ill-formed rather than running a loop on a
		// goroutine that must not park. Refused at call rather than at bind because the resolver has no
		// error channel — the same shape `refuse` uses.
		// **A spilled signature is refused by name** (#995, the chair's interim ruling of 2026-10-18). This arm
		// hands the lower's flat core words to the callee and returns the callee's, which is right only when
		// neither side spills. A result past MAX_FLAT_RESULTS (one word) or parameters past MAX_FLAT_PARAMS
		// (16) cross through a pointer into the side's OWN memory. Copying one needs the reference's
		// memory-to-memory adapter, held in an ADR draft for chair review. Without it the call failed on arity
		// (*"interp: "get" takes 0 arguments, got 1"*), which names no cause. Refused at call rather than at
		// bind, because the resolver has no error channel; the same shape as the async-callee refusal below.
		//
		// NEUTER THIS by skipping the refusal, and `TestASyncCrossComponentSpillIsRefusedByName` reads the
		// arity error again.
		if form, spills := syncSpill(callee.sig); spills {
			return interp.CanonLowerExtern(ft, func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
				return nil, fmt.Errorf("%w: a sync cross-component call with this %s is not carried yet: its "+
					"flat words point into one component's memory, and carrying it to the other needs the "+
					"memory-to-memory adapter (#995). Passing the words through would have the other side read "+
					"its own memory at that address", ErrUnsupportedForm, form)
			}, opts), true
		}
		if callee.async {
			return interp.CanonLowerExtern(ft, func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
				return nil, fmt.Errorf("%w: a sync canon lower cannot target an async lift — the lower "+
					"must match its import's asyncness, and running the callee's callback loop here "+
					"would park an agent that is inside guest execution (§5 H-1)", ErrUnsupportedForm)
			}, opts), true
		}
		return interp.CanonLowerExtern(ft, mayLeaveGuard("cross-component lower", func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			// A sync callee: `invokeWith` reaches `inst.Invoke` directly, with no loop and no park. This
			// is guest-calling-guest on one goroutine, which is what a sync call means, so there is
			// nothing to mark and nothing to wait on.
			//
			// **No context is propagated to the child, and the claim that one was is withdrawn.**
			//
			// This passed `c.Context()` with a comment saying a child inherits its parent's cancellation,
			// so an embedder cancelling the outer call cancels the chain. **That was false.** Searched:
			// `CanonCaller`'s context comes from `newCanonCaller(t.context(), …)`, and a thread's context
			// is created in `world.addLocked` as `context.WithCancel(context.Background())` — it is the
			// thread's **lifetime** context, cancelled by `Instance.Close` (`thread.cancelCtx`, ADR
			// 0069). It is not the embedder's per-call context, which lives on the parent's `liftTask`
			// and has no route to here.
			//
			// Cancellation still reaches the child, by **the model's own path**: the parent receives
			// `TASK_CANCELLED`, its drop code issues `subtask.cancel`, and that reaches the child through
			// ADR 0095's `onCancel`. One route, witnessed by effect, rather than two of which one was
			// imaginary.
			// The sync arm hands back flat core values: `bindCrossComponent` refuses an async callee on
			// this arm by name, so there is never an eagerly-lifted value to carry here.
			//
			// **The caller's run chain IS propagated**, and that one is not imaginary: the callee's entry
			// reads it from the context (`runChainFrom`), so a call back into an instance this chain already
			// holds re-enters its run slot. Without it, parent → child → parent through a lift the parent
			// passed its child self-deadlocked after #976 (`reenter.wasm`; wasmtime returns 8).
			res, cerr := callee.invokeWith(w.callerChainContext(c), args)
			return res.flat, cerr
		}), opts), true
	}

	// The async arm: the flat ABI is the sync signature's params plus a packed i32 return, exactly as the
	// host-impl async arm builds it, and the subtask protocol is reused whole.
	hasResult := callee.sig != nil && callee.sig.Result != nil
	aft := bin.FuncType{Params: ft.Params, Results: []bin.ValType{bin.I32}}
	if !callee.async {
		// **An async lower of a SYNC lift runs the callee inline** (#1050). `liftAsAsyncImpl` drives a callback
		// lift's loop, and it read a sync callee's return value as a packed callback code: 42 decoded to
		// code 10, a trap, and the adapter resolved the subtask with nothing, so the lower returned RETURNED
		// with no result stored. Measured: 200 where wasmtime 49.0.3 reads 242
		// (`sync-callee-async-lower.reading`).
		//
		// The reference's `canon_lift` runs a sync callee to completion inside `thread.resume()`, calling
		// `on_resolve` with its lifted results before the lower returns. So this impl calls it here, on the
		// caller's chain as the sync arm does, and the resolution is queued for `leaveHost` like any other
		// first-segment one. A callee that fails makes the LOWER trap, which is the reference's trap at the
		// same point, rather than resolving with no value.
		//
		// NEUTER THIS by dropping the `!callee.async` arm, and `TestAnAsyncLowerOfASyncLiftStoresItsResult`
		// reads 200.
		return interp.CanonLowerExtern(aft, mayLeaveGuard("cross-component async lower of a sync lift",
			asyncLowerFunc(func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
				params := onStart()
				res, cerr := callee.invokeWith(w.callerChainContext(c), params)
				if cerr != nil {
					return nil, cerr
				}
				v, verr := crossComponentValue(callee.sig, res)
				if verr != nil {
					return nil, verr
				}
				onResolve(v)
				return func() {}, nil // resolved before the lower returns, so nothing is left to cancel
			}, hasResult, w.async)), opts), true
	}
	return interp.CanonLowerExtern(aft, mayLeaveGuard("cross-component async lower",
		asyncLowerFuncFor(func(st *subtask) asyncLowerImpl { return liftAsAsyncImpl(callee, st, w.async) }, hasResult,
			w.async)), opts), true
}

// callerChainContext is the context a guest-to-guest call made inline carries to its callee: the calling
// Thread's run chain, so a call back into an instance the chain already holds re-enters its run slot
// rather than waiting for it (`runChainFrom`). It carries no cancellation, for the reason the sync arm's
// comment gives.
func (w *walker) callerChainContext(c *interp.CanonCaller) context.Context {
	ctx := context.Background()
	if rec := w.threads.current(c); rec != nil {
		w.async.mu.Lock()
		ch := rec.chain
		w.async.mu.Unlock()
		ctx = withRunChain(ctx, ch)
	}
	return ctx
}

// The Canonical ABI's flat limits (def:MAX_FLAT_PARAMS, def:MAX_FLAT_RESULTS): past them a signature's
// values cross through a pointer into memory rather than in flat words.
const (
	maxFlatParams  = 16
	maxFlatResults = 1
)

// syncSpill reports whether a sync call with this signature cannot cross as flat words, and which part
// cannot, as a phrase for the refusal. Two things stop it:
//
//   - **a value that carries a pointer**, a `string` or a `list` anywhere in a parameter or the result. Its
//     words point into the side's own memory, so passing them through has the callee read its memory at the
//     caller's address. **This one is silent.** Measured on `sync-string-param.wasm`, where the callee returns
//     its string argument's first byte: wasmtime reads 97 ('a', the caller's string) and Burroughs read 120
//     ('x', the byte at that address in the child's memory). No spill is needed for it: a string is two words.
//   - **a spill**: a result past MAX_FLAT_RESULTS, or parameters past MAX_FLAT_PARAMS, which crosses through
//     a pointer whatever its types.
//
// A signature whose width cannot be counted (an unresolved reference) is reported as crossing. That keeps the
// passthrough it had, so the refusal cannot regress a call that works.
func syncSpill(sig *FuncType) (string, bool) {
	if sig == nil {
		return "", false
	}
	for _, p := range sig.Params {
		if carriesPointer(p.Type) {
			return fmt.Sprintf("parameter %q (a %s, which carries a pointer into the caller's memory)",
				p.Name, valKindName(p.Type.Kind)), true
		}
	}
	if sig.Result != nil && carriesPointer(*sig.Result) {
		return valKindName(sig.Result.Kind) + " result (which carries a pointer into the callee's memory)", true
	}
	if sig.Result != nil {
		if n, ok := flatWords(*sig.Result); ok && n > maxFlatResults {
			return fmt.Sprintf("%s result (%d flat values, past MAX_FLAT_RESULTS %d)",
				valKindName(sig.Result.Kind), n, maxFlatResults), true
		}
	}
	total := 0
	for _, p := range sig.Params {
		n, ok := flatWords(p.Type)
		if !ok {
			return "", false
		}
		total += n
	}
	if total > maxFlatParams {
		return fmt.Sprintf("parameter list (%d flat values, past MAX_FLAT_PARAMS %d)", total, maxFlatParams), true
	}
	return "", false
}

// carriesPointer reports whether a value of this type flattens to words that include a pointer into linear
// memory: a `string` or a `list`, anywhere inside it. Handles (own, borrow, stream, future, error-context) are
// table indices, not pointers, so they are not counted here.
func carriesPointer(vt ValType) bool {
	switch vt.Kind {
	case VString, VList:
		return true
	case VRecord:
		for _, f := range vt.Fields {
			if carriesPointer(f.Type) {
				return true
			}
		}
	case VTuple:
		for _, e := range vt.Elems {
			if carriesPointer(e) {
				return true
			}
		}
	case VOption:
		return vt.Elem != nil && carriesPointer(*vt.Elem)
	case VResult:
		return (vt.Ok != nil && carriesPointer(*vt.Ok)) || (vt.Err != nil && carriesPointer(*vt.Err))
	case VVariant:
		for _, c := range vt.Cases {
			if c.Type != nil && carriesPointer(*c.Type) {
				return true
			}
		}
	case VBool, VS8, VU8, VS16, VU16, VS32, VU32, VS64, VU64, VF32, VF64, VChar, VErrorContext, VFlags, VEnum,
		VOwn, VBorrow, VStream, VFuture:
		// Scalars, and handles, which are table indices rather than pointers.
	case VRef, VUnresolvedAlias:
		// Uncounted, so syncSpill keeps the passthrough these had.
	}
	return false
}

// flatWords is how many flat core values a value type flattens to: the length of the Canonical ABI's
// `flatten_type` (def:flatten_type). A variant-like type is one discriminant plus its widest case, because
// `flatten_variant` joins the cases' flat types position by position. It reports false for a type it
// cannot count.
func flatWords(vt ValType) (int, bool) {
	switch vt.Kind {
	case VBool, VS8, VU8, VS16, VU16, VS32, VU32, VS64, VU64, VF32, VF64, VChar,
		VOwn, VBorrow, VErrorContext, VStream, VFuture, VEnum:
		return 1, true
	case VString, VList:
		return 2, true
	case VFlags:
		return (len(vt.Labels) + 31) / 32, true
	case VRecord:
		n := 0
		for _, f := range vt.Fields {
			w, ok := flatWords(f.Type)
			if !ok {
				return 0, false
			}
			n += w
		}
		return n, true
	case VTuple:
		n := 0
		for _, e := range vt.Elems {
			w, ok := flatWords(e)
			if !ok {
				return 0, false
			}
			n += w
		}
		return n, true
	case VOption:
		if vt.Elem == nil {
			return 1, true
		}
		w, ok := flatWords(*vt.Elem)
		return 1 + w, ok
	case VResult:
		widest := 0
		for _, p := range []*ValType{vt.Ok, vt.Err} {
			if p == nil {
				continue
			}
			w, ok := flatWords(*p)
			if !ok {
				return 0, false
			}
			widest = max(widest, w)
		}
		return 1 + widest, true
	case VVariant:
		widest := 0
		for _, c := range vt.Cases {
			if c.Type == nil {
				continue
			}
			w, ok := flatWords(*c.Type)
			if !ok {
				return 0, false
			}
			widest = max(widest, w)
		}
		return 1 + widest, true
	default:
		return 0, false
	}
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
//
// `st` is the parent's subtask for this call, given its `cancelTransfer`: after `onCancel`, the child task's
// #726 hand-back, if the request armed one.
func liftAsAsyncImpl(callee *compFunc, st *subtask, caller *asyncHandles) asyncLowerImpl {
	return func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		// `onStart` lifts the caller's flat params and moves the subtask STARTING→STARTED. It must run
		// **before** the goroutine, synchronously, because the model's `on_start` reads the caller's flat
		// args (`CoreValueIter(flat_args)`, def:canon_lower:"CoreValueIter(flat_args)" and def:canon_lower.on_start) and those belong to the calling frame — reading
		// them from another goroutine would be a race against the caller's own continuation, and the
		// retptr is in the same argument list.
		params := onStart()

		// **Register the child's task BEFORE returning, not on the goroutine** (grave #892). A composed
		// parent calls its child and then promptly cancels; the parent's `subtask.cancel` reaches the
		// `onCancel` below, which asks the child's table to cancel what it hosts. If the task were
		// created on the goroutine, that would often find nothing and drop the request — measured as a
		// race, with one run cancelling in 11ms and the next timing out at 30s with the child's cancel
		// handler never invoked. Creating it here makes registration happen-before anything the caller
		// can do next, so there is no window to lose the race in.
		// **The child's task carries no context, and the claim that it carried the caller's is
		// withdrawn** — see the sync arm above for the search. `CanonCaller`'s context is the *thread's*
		// lifetime context (`world.addLocked`: `context.WithCancel(context.Background())`, cancelled by
		// `Close`), not the embedder's per-call one, so passing it propagated nothing an embedder had
		// asked for while reading as though it did.
		//
		// Cancellation reaches this child by the model's path: the parent's `subtask.cancel` invokes the
		// `onCancel` below, which requests this task's cancellation. That is one route and it is
		// witnessed by effect (`TestCancellingTheOuterCallReachesTheComposedChild`).
		task := callee.newLiftTask(context.Background())
		// Read only after onCancel, which arms the transfer (#726).
		var armed *cancelTransfer
		st.cancelTransfer = func() *cancelTransfer { return armed }

		// **F0: this lower returns when the callee's first segment ends, not when its goroutine starts**
		// (ADR 0095's amendment, #1020's mechanism (c)). The reference's `canon_lower` calls the callee, and
		// `canon_lift` runs it until it first suspends or exits (def:canon_lift:"thread.resume()"), so a callee
		// that resolves in that segment makes the lower return RETURNED. Returning at goroutine start read
		// STARTED there, 200 of 200 on `first-segment-return.wasm`, where wasmtime 49.0.3 and the pin read 2.
		//
		// The first segment runs on the caller's chain (F0's corollary), as it runs on the caller's thread in
		// the reference, so a call back into the caller's instance from it re-enters the slot the chain holds.
		// A resolution inside it arrives while this caller is in host code, so it is queued and applied at
		// `leaveHost`, before `asyncLowerFunc` re-checks `st.resolved`.
		//
		// NEUTER THIS by returning without the wait, and `TestACalleeResolvedInItsFirstSegmentIsReturnedInline`
		// reads STARTED.
		task.firstSeg = make(chan struct{})
		task.firstChain = caller.chainOf(c)
		// F1: the task's segments past its first are readers of the caller instance's fence (fence.go).
		task.fenceCaller = caller
		implicit := callee.threads.get(task.threadIdx)

		go func() {
			// However the task ends, its first segment has ended by now. The deferral runs after
			// `crossComponentResolve`, so a resolution in the first segment is queued before the lower wakes.
			defer task.endFirstSegment()
			// **The result store is the last step of the callee's last segment** (F1). An entry that returned
			// EXIT kept its reader (`fenceEntryEnd`), so the store below runs under it, on the segment's
			// chain, which the fence admits as a writer on the caller's slot; the reader is dropped after it.
			defer fenceRelease(implicit)
			// No pending lowerings: a guest-to-guest call's arguments arrive already flat, lowered by the
			// caller's own `canon lower` into the caller's memory and read across by the adapter. Nothing
			// on this path builds a value the host must place in the callee's memory.
			res, err := callee.runLiftTask(task, params, nil)
			// The store's chain is the held reader's, so the caller's fence admits it (nil when unfenced).
			st.storeChain = implicit.fenceKey
			if err != nil {
				// A child that trapped or was cancelled resolves the parent's subtask as a cancellation
				// rather than a result. **The parent gets a terminal state either way**: leaving the
				// subtask unresolved would park the parent forever on a child that is already gone, which
				// is the one outcome worse than a wrong status.
				//
				// `onResolve`'s cancel arm is reached only when `cancellationRequested` is set, so a
				// child that failed WITHOUT the parent asking is a different case — see the note in
				// `crossComponentResolve`.
				crossComponentResolve(onResolve, callee.sig, liftResult{}, err)
				return
			}
			crossComponentResolve(onResolve, callee.sig, res, nil)
		}()

		// The wait is a blocking excursion, counted as at a safepoint, and it ends with the instance's close.
		if werr := c.Blocking(func() error {
			select {
			case <-task.firstSeg:
				return nil
			case <-c.Context().Done():
				return c.Context().Err()
			}
		}); werr != nil {
			return nil, werr
		}

		return func() {
			// The parent's `subtask.cancel` reaching the child's lift task: ADR 0094's mechanism, through
			// the CHILD's handle table, which is its own (`link_component.go` gives every nested
			// instantiation its own `newAsyncHandles`).
			//
			// # The refusal has nowhere to go here, and its channel is elsewhere
			//
			// The model gives `on_cancel` no return value (`OnCancel = Callable[[], None]`, def:OnCancel), so
			// this signature has no slot for one. **That is not the same as the refusal being invisible**:
			// `subtaskCancel` re-checks `st.resolved` after invoking this and returns BLOCKED when the
			// subtask has not resolved, so the parent learns "not cancelled (yet)" whatever the reason
			// `requestCancelAll` declined. That status is the channel.
			//
			// # The window this comment used to describe is CLOSED, in two steps
			//
			// Two earlier drafts were wrong in opposite directions. The first said the refusals *"mean
			// the race is already lost and the subtask will resolve on its own"* — false. The second
			// called the loss *"a recorded gap, not a benign race"*, which was honest about the race and
			// wrong about recording being enough: it rested on the parent learning of the loss through
			// BLOCKED, and grave #892 removed BLOCKED from the sync path. The gap became a **30s hang**,
			// measured as a flaky one (150 composed runs could not all complete; 200 now finish in under
			// three seconds).
			//
			// Both halves are now fixed rather than noted:
			//
			//   1. the task is created and registered **above, synchronously**, so a prompt parent cannot
			//      find nothing to cancel (`newLiftTask`);
			//   2. a cancel landing while the task is still `liftInitial` is **recorded** and delivered
			//      when it starts (ADR 0094 amendment 2), because this adapter's own early `onStart`
			//      makes status 3 unreachable here — so honouring it claims nothing unmeasured.
			//
			// What remains unreported is a request against a task already **past** running, which
			// `ErrCancelNotRunning` names and which genuinely is the lost race: the subtask has resolved
			// and the parent will read its real status.
			//
			// **The request and the #726 hand-back are armed in one critical section**, so the child cannot
			// take the delivery between them and leave the canceller with nothing to wait for.
			callee.h.mu.Lock()
			//nolint:errcheck // No slot in the model's OnCancel signature (`OnCancel = Callable[[], None]`,
			// def:OnCancel). The only refusal left is ErrCancelNotRunning — a task already past running, whose
			// resolved status the parent reads anyway — so there is nothing here a caller could act on.
			_ = callee.h.requestCancelAllLocked()
			armed = task.armTransferLocked()
			callee.h.mu.Unlock()
			// A hand-back window opening exempts the callee from this caller's fence (F1), so a callee
			// reader waiting on it re-checks, and a callee scheduler that skipped its Thread on the fence
			// runs it now.
			caller.fenceSignal()
			callee.h.kick()
		}, nil
	}
}

// crossComponentResolve is the single place a child's outcome becomes the parent's subtask resolution.
//
// Named rather than inlined so the two call sites cannot drift, and because the result lift is the one
// piece of this slice with no prior code path — the piece where a wrong implementation is **silently**
// wrong rather than loudly broken, since a mis-lifted result is a value of the right type and the wrong
// contents.
// `res` is the whole resolution, not just its flat words, because a child whose `task.return` lifted
// eagerly has the authoritative value there — the same precedence rule `liftFlatResult` applies for a
// host caller. A sibling guest must not be handed the stale words when the host would not be.
func crossComponentResolve(onResolve func(canon.Value), sig *FuncType, res liftResult, callErr error) {
	if callErr != nil {
		// The model's `on_resolve(None)` (def:canon_lower.on_resolve:"if result is None:") — no result. Burroughs' `onResolve` reads
		// `cancellationRequested` to choose the terminal state, so this is the cancel arm when the parent
		// asked. When it did not ask, this is a RETURNED carrying nothing, which is **not ideal and not
		// this slice's to invent**: there is no ABI status for "the callee trapped", the model traps the
		// whole store instead, and a status no reading has would be worse than a thin one.
		//
		// **`NoResolution`, the model's `on_resolve(None)`, named** (#1009). This was the zero Value, which is
		// `bool` false, so the store could not tell "no result" from a result and chose by the cancel request
		// instead.
		onResolve(canon.NoResolution())
		return
	}
	v, err := crossComponentValue(sig, res)
	if err != nil {
		// Refused rather than guessed. Reaching here means the child returned a shape this slice does
		// not lift, and resolving with a fabricated value would hand the parent a plausible wrong answer.
		onResolve(canon.NoResolution())
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
// meets rather than a silent truncation — the same discipline `ErrCancelNotRunning` applies to an
// unmeasured branch.
var ErrCrossComponentResult = fmt.Errorf("%w: cross-component result shape is not carried yet", ErrUnsupportedForm)

// crossComponentValue lifts a child lift's flat results into the component value the parent's lower
// lowers to its retptr, using the CHILD's own lift signature as the type.
//
// The signature is the child's and not the parent's import declaration on purpose: the flat values came
// out of the child's `task.return`, so the child's result type is what describes them. They agree when the
// composition is well-typed, and when they do not, the one that describes the bytes is the right oracle.
func crossComponentValue(sig *FuncType, res liftResult) (canon.Value, error) {
	// An eagerly-lifted value is authoritative here for `liftFlatResult`'s reason, and the kind check is
	// the same one: a value of the wrong kind is a mis-typing whichever direction it came from.
	if res.lifted != nil {
		if sig == nil || sig.Result == nil {
			return canon.Value{}, fmt.Errorf("%w: the callee declares no result but its task.return lifted a %s",
				ErrCrossComponentResult, res.lifted.Type.Kind)
		}
		want, werr := canonTypeOf(*sig.Result)
		if werr != nil {
			return canon.Value{}, fmt.Errorf("%w: the callee's declared result cannot be carried: %w",
				ErrCrossComponentResult, werr)
		}
		if res.lifted.Type.Kind != want.Kind {
			return canon.Value{}, fmt.Errorf("%w: the callee declares a %s result but its task.return lifted a %s",
				ErrCrossComponentResult, want.Kind, res.lifted.Type.Kind)
		}
		return *res.lifted, nil
	}
	if sig == nil || sig.Result == nil {
		// No result: `hasResult` is false at the bind site, so the lower ignores the value entirely. An
		// explicit empty is returned rather than an error, because "returns nothing" is a shape this
		// carries perfectly — grave #885's guest is exactly it.
		return canon.Value{}, nil
	}
	// **These arms are the SYNC path's now** (#903). An async child resolves through `task.return`, which
	// lifts eagerly and whose value is preferred above, so the switch below is reached only by a sync
	// cross-component call — which has no `task.return` and therefore nothing lifted.
	//
	// That division is why the eager lift had to grow past `u32` in the same breath as it landed: these
	// arms used to serve the async path too, so an async child returning `bool` worked here and would have
	// begun trapping. The two sets are deliberately not merged — a sync child's result is its core
	// function's return value, with no memory and no guest resumption in between, so it genuinely is just
	// the word.
	if len(res.flat) != 1 {
		return canon.Value{}, fmt.Errorf("%w: child returned %d flat values for a %s result, want 1",
			ErrCrossComponentResult, len(res.flat), valKindName(sig.Result.Kind))
	}
	bits := res.flat[0].Bits
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
