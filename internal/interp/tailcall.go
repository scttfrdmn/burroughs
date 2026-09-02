package interp

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// tailCall is decision 0026's fourth control-transfer value: the frame that just executed a
// `return_call*` is *finished*, and the callee it named must run **in its place** rather than above
// it.
//
// # Why a sentinel rather than a rewrite of the dispatch loop
//
// 0026 option A was to keep the frame and overwrite its `fn`/`locals`/`pc` in place, inside
// `runFrame`. It loses on the fact that a tail call is not always reached from the top of a
// `runFrame` body: `return_call` sits inside blocks, loops, and `if` arms (`return_call.wast`'s
// `even`/`odd` pair tail-calls from inside `(if (result i32) … (else …))`), so the rewrite would have
// to unwind `ctrl` and reset `pc` from an arbitrary nesting depth — the same unwinding a `return`
// already gets for free by *returning*. Returning a value is how this package already leaves a frame
// from arbitrary depth, three times over: `ErrNotValidated` for the layering debt, `*Trap`, and
// 0022's `*thrown`/`*Uncaught`. This is the fourth, and it is deliberately shaped like the third —
// an `error` that a specific `errors.As` recognizes and everything else propagates untouched.
//
// # It must never escape, and the message says so rather than apologising
//
// Every path that can produce one is inside a `runFrame` that some `enterFrame` is driving (the two
// entry points, `run` and `invoke`), so a `tailCall` reaching a user is an engine bug and not a
// module's fault. The rendering therefore names the engine, in the register `funcRefTarget`'s
// unreachable branch uses: reported as an error rather than a panic (grave 0003's rule — this
// asserts a property of *sibling* functions, and a future arm could falsify it silently), but worded
// so nobody mistakes it for a verdict about the module.
//
// The fields are what `enterFrame` needs to re-enter and nothing else. In particular there is **no
// `ft`**: `tailFrom` has already built the frame by the time it makes one of these, so the callee's
// type has been fully consumed and carrying it would be a field kept alive for a format string —
// which later reads as load-bearing. `fn.TypeIndex` is the value the engine actually used, so it is
// the value the message quotes (grave #36).
type tailCall struct {
	inst   *Instance
	fn     *binary.Func
	locals *frame
}

func (t *tailCall) Error() string {
	return fmt.Sprintf(
		"internal: a tail-call sentinel escaped its frame owner (callee is type %d of the callee's own module) — this is an engine bug, not a fault in the module",
		t.fn.TypeIndex)
}

// tailFrom performs a `return_call*`: build the callee's frame from the arguments on the stack, drop
// everything this frame put there, and hand the callee back to whoever owns the frame.
//
// `target`/`fn`/`ft` come from whichever of `resolveCall`/`resolveCallIndirect`/`resolveCallRef` the
// opcode used — the *same* resolution its non-tail sibling performs, which is 0026 property 1 and is
// the reference's own construction (`eval.ml:282-305` steps the plain opcode and re-tags the
// resulting `Invoke`). `base` is the dying frame's own entry height, captured by `runFrame`.
//
// # The three steps in this order, and the order is load-bearing
//
//  1. **Build the frame first**, which pops the arguments. They are on the stack *above* `base`, and
//     they are the one thing in this frame's region that must survive — so they have to be moved
//     into the callee's locals before anything truncates. `buildFrame` is the same code `invoke`
//     uses (0026 property 2), so the reverse-pop order, the v128 two-slot conversion (grave #243),
//     and grave #246's ref-local null fill are inherited rather than re-derived — grave #105's
//     shape, which this package has now paid for three times.
//
//  2. **Check the stack has not gone below `base`**, before truncating. This is not decoration and
//     it is not a copy of `returnFrom`'s check for symmetry's sake: `st.num[:base.num]` when
//     `len(st.num) < base.num` **re-grows the slice within its capacity** and resurrects whatever
//     stale values sat in those slots, so the unvalidated-module case would silently hand the callee
//     a caller's dead operands instead of reporting anything. Same wording discipline as
//     `returnFrom`'s (grave #251): a below-base violation names itself rather than surfacing later
//     as an arity mismatch nobody can trace.
//
//  3. **Truncate to `base`**, exactly — no results are preserved, which is the whole difference from
//     `returnFrom`. A `return` truncates *to base plus the results it keeps*; a tail call keeps
//     nothing, because the values it would have kept are the arguments, and step 1 already moved
//     them into the frame. `numSeq` is guarded by `tracking` and `refSeq` is not, matching
//     `returnFrom` for `pushRef`'s reason: `refSeq` is maintained unconditionally, `numSeq` only
//     once something multi-slot has activated tracking (0023, grave #215).
//
// The dying frame's `ctrl` is discarded by construction — it is a local of the `runFrame` that is
// returning — so a `try_table` the tail call sat *inside* is gone before the callee runs, which is
// what `try_table.wast:334` asserts: an exception thrown by the tail callee must reach the
// **caller's** handler, never the handler the tail call was written inside. That is a property of
// returning rather than something this function arranges, and it is the reason the three
// `return_call*` arms deliberately do **not** route through `raiseOrCatch`.
func tailFrom(target *Instance, fn *binary.Func, ft *binary.FuncType, st *stack, base frameBase) error {
	locals, err := buildFrame(fn, ft, st)
	if err != nil {
		return err
	}
	if len(st.num) < base.num || len(st.refs) < base.ref {
		return fmt.Errorf("%w: tail call with the stack below the frame's own base (%d/%d numeric, %d/%d reference)",
			ErrNotValidated, len(st.num), base.num, len(st.refs), base.ref)
	}
	st.num = st.num[:base.num]
	if st.tracking {
		st.numSeq = st.numSeq[:base.num]
	}
	st.refs = st.refs[:base.ref]
	st.refSeq = st.refSeq[:base.ref]
	return &tailCall{inst: target, fn: fn, locals: locals}
}

// enterFrame runs a frame and re-enters on each tail call it makes — 0026's trampoline, and the
// **one** place a `tailCall` is consumed.
//
// # One loop, both entry points
//
// `run` (the boundary and the const-expression callers) and `invoke` (every wasm-level call) are the
// only two ways a frame is entered, and both go through here (0026 property 3). A second consumer
// would be a second place that decides what a tail call means; and a *missing* one would be worse
// than a bug, because the sentinel would propagate outward as an ordinary error and a tail call
// would read to the harness as a failed call.
//
// # What does not change across an iteration, and why that is the whole design
//
// `results`, `refResults`, and `depth` are the loop's invariants:
//
//   - **`results`/`refResults` stay the *original* frame's declared arity** (0026 property 4). They
//     are what `returnFrom` truncates to, and a tail call does not change what the caller is waiting
//     for — the callee's own declared results are guaranteed to agree by validation, which is #9's
//     job and not something to re-derive here from `t.fn`. Recomputing them per iteration would be
//     the engine deciding an arity question the caller already answered.
//   - **`depth` stays put, and that is the tail call's entire point.** `eval.ml:1080` decrements the
//     budget on `Frame` entry and `:1114` checks it at `Invoke`; a re-tagged `Invoke` lands in the
//     *parent's* instruction list (`:1072-1074`), so the frame that would have been charged is the
//     one being replaced. Incrementing here does **not** make `return_call.wast`'s 1M-deep
//     `even`/`odd` exhaust — that was this comment's original claim and the mutation measured it
//     false, all 141 vectors staying green, because `depth` is invariant along a tail chain and so
//     an inflated one is never *read* until some ordinary call looks at it. The defect is real and
//     the suite is blind to it; TestTailCallConsumesNoBudgetButNestingStillDoes carries the ordinary
//     call that makes it observable.
//   - **The frame base is not here at all**, which is stronger than 0026 forecast. `runFrame`
//     captures it from `len(st.num)`/`len(st.refs)` at entry (grave #251), and `tailFrom` truncated
//     to precisely that height — so the next iteration's capture is the same base by arithmetic
//     rather than by an argument being threaded correctly. A base parameter would be one fact in two
//     places, which is the drift shape this package files tripwires against.
//
// `owner` moves and `in` does not, which is the cross-instance case: a tail call to an imported
// function must run with the *callee's* instance as receiver, or its `memory`/`global.get`/`call`
// resolve against the wrong module. See `resolveCall` on the crossing, and
// TestTailCallCrossesTheInstanceBoundary on why the corpus cannot see this — `return_call.wast:82`
// does tail-call a `spectest` import, and that import's body is **empty** (`wast.go`'s
// `spectestFields`), so the one cross-instance tail call the suite contains is unobservable by
// construction.
// # SP-1's call-site poll is here, and this loop is a back-edge `runFrame` cannot see
//
// Contract §3 SP-1 names *"loop back-edges and call sites"*, and this is the funnel every call entry
// passes through — `invoke` for `call`/`call_indirect`/`call_ref`, and `run` for the boundary. The poll
// is unconditional rather than predicated: there is no cheaper test at a call, and a call is already
// paying frame construction, so an atomic load is not the term that matters.
//
// **The more important half is the trampoline.** A guest recursing by tail call spins *this* `for`,
// not `runFrame`'s, and a tail chain assigns `pc` nowhere at all — `return_call.wast`'s 1M-deep
// `even`/`odd` is exactly that shape. So a poll placed only at [ADR 0059]'s fourteen `pc` sites would
// leave a tail-recursive guest unstoppable, which is SP-1's bound failing on the one program shape
// 0026 added. The poll at the top of the loop body covers the call *and* every re-entry.
//
// What is **not** covered here is a thread blocked in a *host* call: that is SP-2's clause, it needs a
// boundary that observes the stop rather than a poll, and it is stated as absent rather than implied
// by this one's presence.
//
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
func (in *Instance) enterFrame(fn *binary.Func, locals *frame, st *stack, results, refResults, depth int) error {
	owner := in
	for {
		st.t.poll()
		err := owner.runFrame(fn, locals, st, results, refResults, depth)
		var t *tailCall
		if !errors.As(err, &t) {
			return err
		}
		owner, fn, locals = t.inst, t.fn, t.locals
	}
}
