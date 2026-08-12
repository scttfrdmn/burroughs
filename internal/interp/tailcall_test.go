// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// tailGate is the Features every source in this file decodes with. `TailCall` governs
// `return_call`/`return_call_indirect` and **`GC` governs `return_call_ref`** — the function
// references proposal folded into GC — so a file about one mechanism needs two gates, which is
// `gatemap.go`'s own recorded split and the reason 0026 covers one mechanism under two gate names.
// `ExceptionHandling` is here for the try_table row alone.
var tailGate = binary.Features{TailCall: true, GC: true, ExceptionHandling: true}

// tailModule encodes and decodes a source with the tail-call gates on, returning the instance.
//
// Through the real front end for `instantiate1`/`link1`'s stated reason (grave #125): the immediate
// staging is part of the subject, so a hand-built `binary.Module` would assert the interpreter
// against this test's own opinion of how a `return_call`'s immediate is encoded rather than against
// the decoder's. It exists beside `instantiate1` only because that helper decodes at **default**
// features, where every opcode in this file is gated off — `runGCErr` splits for the same reason.
//
// Each of the three failure channels is named separately: a module this file could not *encode* and
// one the interpreter refused are different findings, and a single "build failed" would let an
// encoder gap read as an engine defect.
func tailModule(t *testing.T, src string) *Instance {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d := &binary.Decoder{Features: tailGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode at %+v: %v", tailGate, err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiation fell short: %v", err)
	}
	return in
}

// tailLink is `link1` with the tail-call gates on: the cross-instance row needs both a supplier and
// an importer, and `link1`'s decoder runs at default features, where `return_call` is gated off.
func tailLink(t *testing.T, src string, imp Imports) *Instance {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d := &binary.Decoder{Features: tailGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap, lerr := InstantiateLinked(m, imp)
	if lerr != nil {
		t.Fatalf("link: %v", lerr)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	return in
}

// TestTailCallCrossesTheInstanceBoundary is the control 0026 named as the one the corpus cannot
// supply, and the corpus's inability is *measured* rather than assumed.
//
// # Why the suite scores this green whichever way the engine reads it
//
// `return_call.wast:82` does contain a cross-instance tail call — `tailprint_i32_f32` tail-calls the
// `spectest.print_i32_f32` import declared at `:4` — so a grep for "does the corpus exercise this"
// answers yes. It is nonetheless **unobservable**: that import's body is empty (`wast.go`'s
// `spectestFields` declares `(func (export "print_i32_f32") (param i32 f32))` with nothing in it), so
// a callee running against the *wrong* instance executes the same zero instructions and returns the
// same nothing. The vector passes under both readings, which is §9 G-3's blind spot exactly — and it
// is the sharpest form of it, because the file looks covered.
//
// # The discriminator is state only one instance has
//
// Both modules declare a global and the supplier's function returns *its own*; the values are 11 and
// 7, distinct and arbitrary, so a callee that ran against the importer answers a plausible number
// rather than failing. That is `TestCallIndirectResolvesTheFuncrefsOriginatingInstance`'s
// construction (grave #163) pointed at the trampoline instead of at a table slot: an engine that
// resolved the tail callee correctly and then *entered* it with the wrong receiver would return 7.
//
// # Falsified
//
// `enterFrame`'s loop advances `owner` from the sentinel (`owner, fn, locals = t.inst, …`). Dropping
// `owner` from the assignment — keeping the tail-caller as receiver, which is the shape a reader
// writes when they think the sentinel only carries "what to run next" — answers **7** here. The
// mirror row (`viaLocal`, below) is what keeps that from reading as the two instances having been
// conflated in the other direction.
//
// Under that same mutation the all-on lane's 141 vectors across the three `return_call*` files stay
// **entirely green** — measured, so the paragraph above is a finding and not an expectation. This row
// is the only thing in the project that fails.
//
// Beside `TestLinkedCallCrossesIntoTheSupplierInstance`, not instead of it: that row covers the
// *non*-tail crossing through `resolveCall`, this one covers the entry, and 0026's split is precisely
// what makes them two facts rather than one.
func TestTailCallCrossesTheInstanceBoundary(t *testing.T) {
	sup := supplier(t, `(module
		(global $g i32 (i32.const 11))
		(func (export "get") (result i32) (global.get $g)))`)

	in := tailLink(t, `(module
		(import "s" "get" (func $get (result i32)))
		(global $g i32 (i32.const 7))
		(func (export "viaTailCall") (result i32) (return_call $get))
		(func (export "viaLocal") (result i32) (global.get $g)))`, exportsOf(sup))

	got, err := in.Invoke("viaTailCall")
	if err != nil {
		t.Fatalf("viaTailCall: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 11 {
		t.Errorf("viaTailCall = %v, want 11 — 7 means the tail callee ran with the tail-caller's "+
			"instance as receiver, reading the importer's global 0 instead of the supplier's", got)
	}
	// The mirror: the importer's own global is undisturbed, so the 11 above is a crossing and not
	// the two instances having been conflated in the other direction.
	got, err = in.Invoke("viaLocal")
	if err != nil {
		t.Fatalf("viaLocal: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 7 {
		t.Errorf("viaLocal = %v, want 7", got)
	}
}

// TestTailCalleeExceptionEscapesTheTryTableItSatInside pins the property the three `return_call*`
// arms' *omission* of `raiseOrCatch` exists for: a tail call discards its frame before the callee
// runs, so an exception the callee throws must escape any `try_table` the tail call was written
// inside.
//
// # Scoped to `return_call_ref`, because that is the member the corpus cannot see
//
// `try_table.wast:334-335` assert `assert_exception` for `return-call-in-try-catch` and
// `return-call-indirect-in-try-catch`, so **two of the three opcodes are oracle-covered** and this
// control would be redundant for them. There is no `return-call-ref-in-try-catch` vector, and
// `call_ref.wast` has no `try_table` at all — so the third arm's authority is the reduction alone
// (`eval.ml:288-296`), which is exactly the case a control is for. Written as a table anyway, with
// the covered members present: a partition asserted only where it is uncovered cannot show that the
// three members agree, and three arms that agree is the actual claim.
//
// # The wrong reading answers, it does not fail
//
// If the arm routed through `raiseOrCatch`, `$h` would catch, the function would fall out of the
// block, and `Invoke` would return **no error at all** — a clean pass. So the assertion is on the
// *presence* of an escaping `*Uncaught`, and the vacuity guard beside it is a non-tail `call` in the
// identical module, which must be caught: without that row, an engine whose `try_table` never caught
// anything would satisfy every assertion here.
//
// # Falsified twice, because the first mutation was a no-op
//
// The obvious mutation — wrap each arm's `tailFrom` result in the same `raiseOrCatch` block
// `opCall`/`opCallIndirect`/`opCallRef` use — was written here as the falsification and **changes
// nothing**: a sixteen-line diff, entirely plausible, and `raiseOrCatch` type-checks its argument, so
// a `*tailCall` is a same-line pass-through and every row stays green. That is the trichotomy's first
// answer (a no-op mutation, not a blind control) and it is worth recording because it says something
// about the design: the handler property is not enforced by this arm *declining* to catch, it is
// enforced by the frame's `ctrl` being a local of the `runFrame` that already returned. There is no
// small edit to this arm that breaks it.
//
// The mutation that does break it is the realistic wrong implementation — `return_call` as
// call-then-return, `target.invoke(...)` followed by `returnFrom`, with the caller's `ctrl` still live.
// Applied to `opReturnCall` alone it fails the `tail` row and leaves `tailIndirect` and `tailRef`
// passing; applied to `opReturnCallRef` alone it fails `tailRef` alone. So the three rows discriminate
// per opcode, and the `caught` row passes throughout — the two directions are independent.
//
// # What the corpus catches under that mutation, and why it is not the same thing
//
// Measured, because "the corpus cannot see this" is a claim: with `opReturnCallRef` rewritten to
// call-then-return through `callRef`, `return_call_ref.wast` goes to **35 pass / 5 fail** — and all
// five are `trap: call stack exhausted` at `:195`, `:201`, `:202`, `:207`, `:208`, the deep vectors.
// Routed instead through `invoke` directly (no budget check on the path), the whole harness dies with
// `fatal error: stack overflow`. So the corpus does detect a non-tail implementation, in both cases via
// **resource exhaustion** — never via the handler. A reader repairing five exhaustion fails reaches for
// the budget; nothing in the suite would tell them the enclosing handler is also wrong. That, and not
// sole-witness status, is this control's value: it separates the two consequences of one defect.
func TestTailCalleeExceptionEscapesTheTryTableItSatInside(t *testing.T) {
	// One module, four exports: the three tail forms and the non-tail control, each throwing the
	// same tag from the same callee through a `try_table (catch_all $h)`. `$h` is reachable only if
	// the handler catches, and falling out of the block is a normal return.
	const src = `(module
	  (tag $e)
	  (type $v (func))
	  (table 1 funcref)
	  (elem (i32.const 0) $throwVoid)
	  (elem declare func $throwVoid)
	  (func $throwVoid (throw $e))
	  (func (export "tail")
	    (block $h (try_table (catch_all $h) (return_call $throwVoid))))
	  (func (export "tailIndirect")
	    (block $h (try_table (catch_all $h) (return_call_indirect (type $v) (i32.const 0)))))
	  (func (export "tailRef")
	    (block $h (try_table (catch_all $h) (return_call_ref $v (ref.func $throwVoid)))))
	  (func (export "caught")
	    (block $h (try_table (catch_all $h) (call $throwVoid)))))`

	in := tailModule(t, src)

	for _, name := range []string{"tail", "tailIndirect", "tailRef"} {
		t.Run(name, func(t *testing.T) {
			_, err := in.Invoke(name)
			if err == nil {
				t.Fatalf("%s returned normally — the handler the tail call sat inside caught the "+
					"tail callee's exception, but that frame was gone before the callee ran", name)
			}
			var u *Uncaught
			if !errors.As(err, &u) {
				t.Fatalf("%s: got %T (%v), want an escaping *Uncaught", name, err, err)
			}
		})
	}
	// The vacuity guard, and it is the load-bearing half: a `try_table` that never caught anything
	// would pass all three rows above while asserting nothing about tail calls.
	t.Run("the non-tail call is caught", func(t *testing.T) {
		if _, err := in.Invoke("caught"); err != nil {
			t.Errorf("caught: %v, want the handler to catch — the rows above prove nothing if this "+
				"module's try_table never catches", err)
		}
	})
}

// TestTailCallConsumesNoBudgetButNestingStillDoes is the bidirectional budget control #253's
// definition of done asks for, and the two rows are chosen so that **neither of the two ways to get
// the budget wrong satisfies both**.
//
// # Why the pair, rather than one deep-recursion row
//
// A tail call must consume no budget (`eval.ml:1080` charges on `Frame` entry; a re-tagged `Invoke`
// arrives in the *parent's* instruction list, `:1072-1074`, so the frame that would have been charged
// is the one being replaced). Asserting only that leaves the opposite defect wide open: an engine that
// stopped charging *any* call would pass a deep-tail row and then never report exhaustion at all.
// `TestCallStackExhaustionIsReportedNotCrashed` covers pure non-tail recursion, so what is left — and
// what only a tail-call mechanism can get wrong — is the **mixed** chain:
//
//   - `$loop` tail-calls itself `n` times and returns. Depth is constant, so any `n` must return, and
//     `callBudget * 2` is used rather than a number near the ceiling so the row is unambiguous about
//     which side of it the answer lies on.
//   - `$a` *calls* `$b` (nesting, +1) and `$b` **tail-calls** `$a` (no charge). Each round trip nets
//     exactly one frame, so this must still exhaust — an engine that credited the tail call back, or
//     that reset `depth` on re-entry, runs forever and the row hangs rather than failing.
//
// A hang is not a fail (`br_table`'s loop-row lesson: a timeout names no row), which is why the
// mixed row's *whole point* is that a wrong engine terminates: it cannot, if the budget is broken,
// so the row is written to fail in the one direction that can be observed — the trap arriving — and
// the surviving hazard is stated here rather than left for a reader to discover. Bounded by
// `-timeout` alone, and that is the honest description of it.
//
// # Falsified, both directions
//
//   - Adding `if depth >= callBudget { return trapExhaustion }` to `opReturnCall`'s arm fails the
//     constant-depth row with `call stack exhausted`.
//   - Incrementing `depth` in `enterFrame`'s loop fails it the same way, at 10000 rather than at the
//     first call, which is the more plausible mistake and the reason the row's `n` is `callBudget * 2`
//     rather than `callBudget + 1`.
//   - Removing the `depth >= callBudget` guard from `call` makes the mixed row hang — the outcome
//     this comment says a broken budget produces, confirmed rather than predicted.
func TestTailCallConsumesNoBudgetButNestingStillDoes(t *testing.T) {
	// The tail-recursive countdown: `$loop` return_calls itself, so a million-deep chain occupies
	// one Go frame and one wasm frame's worth of budget. Shaped with `if`/`else` for
	// TestCallStackExhaustionIsReportedNotCrashed's stated reason — the recursive arm has to be the
	// one *not* taken at the bottom.
	// `$noop` at the bottom is the row's *second* purpose and it is not decoration: without an
	// ordinary call after the chain, nothing ever reads `depth`, so incrementing it once per tail
	// call is unobservable and the row passes with the trampoline charging a frame it should not.
	// Found by that mutation passing, and the corpus is blind to it too (measured: 141 vectors green
	// with `depth++` in `enterFrame`) — `return_call.wast`'s deep `even`/`odd` chains end in a
	// constant, never in a call. So the assertion here is really *two*: a long tail chain returns,
	// **and** an ordinary call is still affordable at the end of it.
	tail := tailModule(t, `(module
	  (func $noop)
	  (func $loop (param i64) (result i64)
	    (if (result i64) (i64.eqz (local.get 0))
	      (then (call $noop) (i64.const 0))
	      (else (return_call $loop (i64.sub (local.get 0) (i64.const 1))))))
	  (func (export "c") (param i64) (result i64) (call $loop (local.get 0))))`)

	t.Run("a tail chain far past the budget returns", func(t *testing.T) {
		out, err := tail.Invoke("c", Value{Type: binary.I64, Bits: callBudget * 2})
		if err != nil {
			t.Fatalf("%d tail calls were refused (budget %d): %v\n"+
				"\tthis is the accept direction — a tail call that charges the budget refuses a "+
				"program the spec requires to run, and no vector measures it: the million-deep "+
				"even/odd chains catch an implementation that *nests* (by exhausting or by "+
				"overflowing the Go stack), never one that merely miscounts",
				callBudget*2, callBudget, err)
		}
		if len(out) != 1 || out[0].Bits != 0 {
			t.Errorf("got %v, want [0]", out)
		}
	})

	// The mixed chain: `$a` *calls* `$b` and `$b` tail-calls `$a`, so one frame per round trip and
	// the budget must still bite.
	//
	// **Bounded at `callBudget * 2` rather than written as an unbounded loop**, and that is the
	// `br_table` loop-row law (a control must fail, never hang — a timeout names no row) applied to
	// a mutation whose outcome was mispredicted here. This row first read `(func $a (export "c")
	// (call $b))` with no counter, on the stated expectation that a broken budget would make it
	// *hang*; removing `call`'s `depth >= callBudget` guard instead produced `fatal error: stack
	// overflow`, which is the same defect one step worse — it kills the whole test binary and names
	// no row at all. With a bound larger than the budget, a correct engine exhausts before the
	// counter runs out and every wrong engine reaches zero and **returns 0**, which is an answer this
	// row can report: an engine that credits the tail call back (`depth--` on re-entry) nets zero per
	// round trip, and an engine with no budget at all recurses 20000 Go frames deep, well short of
	// the ~272k where the runtime gave up.
	mixed := tailModule(t, `(module
	  (func $a (param i64) (result i64)
	    (if (result i64) (i64.eqz (local.get 0))
	      (then (i64.const 0))
	      (else (call $b (i64.sub (local.get 0) (i64.const 1))))))
	  (func $b (param i64) (result i64) (return_call $a (local.get 0)))
	  (func (export "c") (param i64) (result i64) (call $a (local.get 0))))`)

	t.Run("nesting through a tail call still exhausts", func(t *testing.T) {
		out, err := mixed.Invoke("c", Value{Type: binary.I64, Bits: callBudget * 2})
		if err == nil {
			t.Fatalf("%d nested round trips returned %v instead of exhausting: the tail call is "+
				"being credited back, so a chain that adds a frame every two calls is unbounded",
				callBudget*2, out)
		}
		wantExhaustion(t, err)
	})

	// The third row exists because the first two are **both blind to a budget check on the tail
	// path** — `depth` is invariant along a tail chain, so a spurious `depth >= callBudget` in
	// `opReturnCall` is inert at depth 1 and the constant-depth row passes with the defect in
	// place. It was found by the mutation passing, and the corpus is blind to it as well (measured:
	// 141 vectors across the three files stay green), which is exactly the accept-direction hole
	// §9 G-3 says a control has to fill.
	//
	// The reachable maximum is what makes it observable: `$rec` recurses by plain `call` to the
	// deepest frame the budget permits — `callBudget-1`, the figure
	// TestCallStackExhaustionIsReportedNotCrashed pins from the other side — and *that* frame tail
	// calls. A tail call replaces its frame, so it must be legal at any depth including the last
	// one; an engine that charges for it refuses a program the spec permits, at exactly one depth,
	// which is why no smaller `n` can see it.
	edge := tailModule(t, `(module
	  (func $answer (result i64) (i64.const 7))
	  (func $rec (param i64) (result i64)
	    (if (result i64) (i64.eqz (local.get 0))
	      (then (return_call $answer))
	      (else (call $rec (i64.sub (local.get 0) (i64.const 1))))))
	  (func (export "c") (param i64) (result i64) (call $rec (local.get 0))))`)

	t.Run("a tail call from the deepest permitted frame is not refused", func(t *testing.T) {
		out, err := edge.Invoke("c", Value{Type: binary.I64, Bits: callBudget - 1})
		if err != nil {
			t.Fatalf("a tail call at the budget's edge (%d nested calls) was refused: %v\n"+
				"\ta tail call replaces its frame rather than adding one, so it consumes no "+
				"budget and is legal at every depth the nesting itself reached", callBudget-1, err)
		}
		if len(out) != 1 || out[0].Bits != 7 {
			t.Errorf("got %v, want [7]", out)
		}
	})
}

// TestTailCallInTheOutermostFrameIsTrampolinedAtTheBoundary pins the *second* of `enterFrame`'s two
// call sites, which is the one a reader is most likely to think unnecessary.
//
// A `return_call` in the function the boundary invoked has no engine-level caller to consume its
// sentinel: `Invoke` → `run` → `runFrame`, and if `run` entered `runFrame` directly the `*tailCall`
// would propagate outward as an ordinary error. The failure would be loud rather than silent — so
// this is not an accept-direction row — but it would be loud in the *wrong vocabulary*, arriving as
// an engine-bug message where the module is perfectly valid, and `return_call.wast` exports `even`
// and `odd` and invokes them directly, so the shape is the common case rather than an edge.
//
// The assertion is on the answer, and on the sentinel's message being absent from it: a row that only
// checked `err == nil` would pass if some future path swallowed the sentinel and returned no results.
//
// Falsified: reverting `run` to `return in.runFrame(fn, locals, st, results, refResults, 0)` fails
// this with the escape message — and fails **four of this file's six** tests, not one, because the
// cross-instance, try_table and operand-discard rows all tail-call from the exported function itself
// too. The prediction that they would survive was wrong, and the surviving pair is the informative
// half: only TestTailCallConsumesNoBudgetButNestingStillDoes passes under this mutation, because its
// tail calls are made from frames some `invoke` entered. So the two rows do partition `enterFrame`'s
// two call sites — just not in the direction guessed, and the partition is worth having recorded the
// right way round.
func TestTailCallInTheOutermostFrameIsTrampolinedAtTheBoundary(t *testing.T) {
	in := tailModule(t, `(module
	  (func $answer (result i32) (i32.const 42))
	  (func (export "c") (result i32) (return_call $answer)))`)

	got, err := in.Invoke("c")
	if err != nil {
		t.Fatalf("c: %v\n\ta tail call in the outermost frame needs the boundary's own trampoline: "+
			"there is no enclosing invoke to consume the sentinel", err)
	}
	if len(got) != 1 || got[0].Bits != 42 {
		t.Errorf("c = %v, want [42]", got)
	}
}

// TestTailCallDiscardsTheDyingFramesOperands pins `tailFrom`'s third step — truncate to `base`,
// keeping nothing — and it exists because **nothing else in this file can reach it.**
//
// # Found by inspection, and the inspection is the transferable part
//
// Every other row here tail-calls from a frame whose stack is *already* at its base by the time the
// opcode runs: `$get` and `$answer` take no parameters, and `$loop`'s single argument is popped by
// `buildFrame`. So a `tailFrom` that skipped the truncation entirely would pass all five, which is
// the vacuity law pointed at a mutation rather than at a comparison — the step is exercised on every
// row and *observable* on none. A control battery that only asserts what its existing rows happen to
// reach inherits their blind spot.
//
// # The junk has to be legal, and it is, for the same reason `return`'s is
//
// `return_call` is stack-polymorphic: like `return` it is an unconditional transfer, so validation
// pops the callee's parameters and then makes whatever is beneath them unreachable rather than
// requiring the frame be empty. The `(i32.const 99)` below is therefore a valid module and not a
// synthetic invalid one — which matters, because the *below-base* check (`tailFrom` step 2) guards
// the `ErrNotValidated` direction and no valid module can reach it, so it stays declared-and-tracked
// while this row covers the step that a valid module can observe.
//
// # Falsified, and the *prediction* is what the falsification killed
//
// This comment first claimed the wrong reading returns **99** — the stranded operand promoted into
// the caller's result by `returnFrom` keeping `base + results` — and called that "the
// accept-direction defect in its purest form". Deleting the three truncation lines instead produces
//
//	interp: module reached the interpreter unvalidated: "num" declares 1 numeric results and
//	left 2 values on the stack
//
// because `invoke`'s two-array arity check and the boundary's own check both compare the *delta*
// against the frame's base, so a stranded operand is caught at the nearest frame boundary every
// time — the junk is never read, it only makes a count wrong. The row is red for a reason different
// from the one predicted, which is the trichotomy's third answer (a wrong prediction, killing the
// prose rather than the code) and the reason the episode is recorded rather than tidied away: those
// arity checks are load-bearing here in a way 0026 did not notice, and a future change that relaxes
// them for a tail call's sake would silently restore the 99.
//
// So the honest statement of the defect is **not** silence — it is a valid module being told it
// reached the interpreter unvalidated, the same wrong-vocabulary failure
// TestTailCallInTheOutermostFrameIsTrampolinedAtTheBoundary names. And this row is still the only
// thing that catches it: with the truncation deleted, the all-on lane's 141 vectors across
// `return_call.wast` (37), `return_call_indirect.wast` (64) and `return_call_ref.wast` (40) stay
// **entirely green** — measured, not assumed — because no vector strands an operand across a tail
// call.
//
// The per-array halves do separate as predicted: deleting only the `st.refs`/`st.refSeq` pair leaves
// the `num` row passing and fails `ref` alone (`left 3 references on the stack`), which is why both a
// numeric and a reference operand are stranded rather than one.
func TestTailCallDiscardsTheDyingFramesOperands(t *testing.T) {
	in := tailModule(t, `(module
	  (func $answer (result i32) (i32.const 42))
	  (func (export "num") (result i32)
	    (i32.const 99)
	    (return_call $answer))
	  (func $refAnswer (result funcref) (ref.func $answer))
	  (func (export "ref") (result funcref)
	    (ref.null func)
	    (ref.func $answer)
	    (return_call $refAnswer))
	  (elem declare func $answer))`)

	got, err := in.Invoke("num")
	if err != nil {
		t.Fatalf("num: %v\n\tan `unvalidated` complaint here is a *valid* module being blamed for "+
			"the engine's own bookkeeping: the 99 this frame pushed was not discarded, so the "+
			"arity check counts it and the module is told it never passed validation", err)
	}
	if len(got) != 1 || got[0].Bits != 42 {
		t.Errorf("num = %v, want 42 — a 99 is the dying frame's own operand promoted into the "+
			"caller's result, which is what a truncation to the *wrong height* looks like once the "+
			"counts happen to agree", got)
	}
	// The reference half, because `st.refs`/`st.refSeq` truncate on their own lines: two stranded
	// refs, so a kept one arrives where the callee's `ref.func` should be. Non-nullness is the
	// discriminator — the stranded values are a null and a funcref, in that order, so keeping either
	// is visible.
	got, err = in.Invoke("ref")
	if err != nil {
		t.Fatalf("ref: %v\n\tsame reading as the `num` row: the two stranded references were kept, "+
			"so the count is wrong and a valid module is blamed for it", err)
	}
	if len(got) != 1 || got[0].Null {
		t.Errorf("ref = %+v, want the callee's non-null funcref — a null is the stranded "+
			"`ref.null func` this frame pushed before the tail call", got)
	}
}

// TestTailCallSentinelRendersAsAnEngineBug pins the one thing about `tailCall`'s `Error()` that is
// worth pinning: the message a *user* would see if the sentinel ever escaped names the engine rather
// than the module.
//
// **Not oracle-covered and never will be**, by construction: every path that produces a sentinel is
// inside a frame some `enterFrame` is driving, so a green board is exactly what "this string is
// unreachable" looks like. That is grave #36's territory — the half of a message no suite reads is
// the half nothing else will check — and the specific error it guards against is the *category*: a
// reader meeting this text must not go looking for a fault in their module. The type index is quoted
// because it is a value the engine actually used (`fn.TypeIndex`), which is the same rule that
// stopped `ErrMalformedFuncType` from reporting a byte the image never held.
//
// Deliberately *not* an assertion that the string never appears: unreachability is a grave only when
// it is silent, and this one is declared at its definition site with the reason it is an error rather
// than a panic (grave 0003 — it asserts a property of sibling functions, and a future arm could
// falsify it silently).
func TestTailCallSentinelRendersAsAnEngineBug(t *testing.T) {
	got := (&tailCall{fn: &binary.Func{TypeIndex: 3}}).Error()
	for _, want := range []string{"engine bug", "type 3", "not a fault in the module"} {
		if !strings.Contains(got, want) {
			t.Errorf("tailCall.Error() = %q\n\tmissing %q", got, want)
		}
	}
}
