// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestEveryPCAssignmentInRunFrameGoesThroughJumpTo is [ADR 0059]'s completeness obligation, and the
// control that decision names instead of a reviewer's eye over fourteen near-identical hunks.
//
// **The population is derived from the function, not listed here.** The failure this exists to catch
// is a *fifteenth* arm — a `pc = …` added by a later proposal's control-flow instruction, which would
// silently opt out of the safepoint poll and leave a guest looping through it unstoppable. A control
// scoped to today's fourteen sites would inherit today's blind spot; a control that walks `runFrame`'s
// syntax tree judges whatever is there when it runs.
//
// **A syntax tree and not a grep, because a grep measures text.** `pc = target - 1` inside a string
// literal or a comment is a hit for `grep` and not an assignment; `pc =\n\ttarget` is an assignment and
// not a hit. Neither mistake is available to `ast.Inspect`.
//
// The rule, stated as the check spells it: every assignment whose left-hand side names `pc` must have a
// call to `jumpTo` on its right, except the `for` statement's own `pc := 0` and `pc++` — which are the
// loop's forward walk and by construction never a back-edge, since `pc++` cannot decrease.
//
// **Watched die**, which is the only reason this is a control and not a comment: a fifteenth
// `pc = 0` was injected into the `opNop` arm and this failed naming exec.go and the injected line,
// with the census below still at 14; the injection was reverted. Blinding the `jumpTo` match instead
// fails all fourteen at once *and* fails the census, which is the two-sided falsification —
// *a control isn't born until it's watched die.*
//
// **The blinding row was run twice, and the first run proved nothing** (grave #589). Its restore step
// was `git checkout exec.go` on an uncommitted slice, so HEAD was `main` and the checkout reverted the
// *subject* — the fourteen routings — instead of the previous injection. A reverted subject produces
// this row's predicted board exactly, `14 raw / 0 routed`, on the same assertion with the same
// message, because either way there is no `jumpTo` to match. The board above is from the re-run
// against a committed baseline, which is now the battery's precondition.
//
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
func TestEveryPCAssignmentInRunFrameGoesThroughJumpTo(t *testing.T) {
	// The census, not a floor. Fourteen arms route through `jumpTo` today, and the number is pinned
	// exactly rather than as a `>=` because both directions are findings: fewer means the walk has
	// stopped seeing the assignments and the rule below would then pass by asking nothing, and more
	// means a new arm exists whose author should read 0059 before this number is bumped. *A floor is
	// not a census* — a floor sits safely below its population and reports nothing about a partial
	// loss.
	const routed = 14

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "exec.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing exec.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Name.Name == "runFrame" {
			fn = fd
			break
		}
	}
	if fn == nil {
		// Not a skip. A control that cannot find its subject has to fail: `runFrame` moving to
		// another file would otherwise retire this check silently, which is the failure mode
		// where an instrument's silence is mistaken for a clean bill of health.
		t.Fatalf("runFrame is not declared in exec.go — this control's whole domain is that " +
			"function's body, so it now asserts nothing. Re-point it at the file `runFrame` " +
			"moved to rather than deleting it: the risk it names is a `pc` assignment that " +
			"skips the safepoint poll, and that risk moves with the function.")
	}

	// The loop header's own `pc := 0` and `pc++` are the exempt pair, and they are identified by
	// *being* the ForStmt's Init and Post rather than by matching their text — an exemption keyed on
	// the shape of a statement would also exempt a hand-written `pc++` buried in an arm, which is a
	// forward step by luck rather than by construction.
	var exempt []ast.Node
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if f, ok := n.(*ast.ForStmt); ok {
			if f.Init != nil {
				exempt = append(exempt, f.Init)
			}
			if f.Post != nil {
				exempt = append(exempt, f.Post)
			}
		}
		return true
	})
	isExempt := func(n ast.Node) bool {
		for _, e := range exempt {
			if e == n {
				return true
			}
		}
		return false
	}

	// namesPC recognises the identifier. Three statement forms below can write a variable —
	// `=`/`:=` (AssignStmt), `pc++`/`pc--` (IncDecStmt), and a `range` clause's key or value
	// (RangeStmt) — and all three are walked, because "which forms can assign" is a fact about Go
	// rather than about this function's current shape.
	//
	// A fourth form — `&pc` handed to something that writes through the pointer — is **not** covered
	// here and cannot be, since the write happens in a callee. That blind spot is asserted by
	// TestNothingTakesTheAddressOfRunFramesPC rather than left as a caveat in a comment.
	namesPC := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == "pc"
	}

	routedCount, raw := 0, []string(nil)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if isExempt(n) {
			return false
		}
		switch s := n.(type) {
		case *ast.IncDecStmt:
			if namesPC(s.X) {
				raw = append(raw, fmt.Sprintf("exec.go:%d: pc%s",
					fset.Position(s.Pos()).Line, s.Tok))
			}
		case *ast.RangeStmt:
			// `for pc = range …` and `for pc := range …`. Not exempt: the exemption is the
			// dispatch loop's own three-clause header, and a range over `pc` is a different
			// statement that happens to write the same variable.
			for _, e := range []ast.Expr{s.Key, s.Value} {
				if e != nil && namesPC(e) {
					raw = append(raw, fmt.Sprintf("exec.go:%d: range clause assigns pc",
						fset.Position(s.Pos()).Line))
				}
			}
		case *ast.AssignStmt:
			assigns := false
			for _, lhs := range s.Lhs {
				if namesPC(lhs) {
					assigns = true
				}
			}
			if !assigns {
				return true
			}
			// The right-hand side must contain a `jumpTo` call. Containment rather than
			// "is exactly", because `st.t.jumpTo(target-1, pc)` is a call expression and
			// `pc = st.t.jumpTo(...) + 1` would also be one — and if a later arm needs the
			// second form, the poll still happens, which is what this rule is about.
			found := false
			for _, rhs := range s.Rhs {
				ast.Inspect(rhs, func(m ast.Node) bool {
					sel, ok := m.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == "jumpTo" {
						found = true
					}
					return !found
				})
			}
			if found {
				routedCount++
				return true
			}
			raw = append(raw, fmt.Sprintf("exec.go:%d: %s",
				fset.Position(s.Pos()).Line, s.Tok))
		}
		return true
	})

	if len(raw) > 0 {
		t.Errorf("%d assignment(s) to runFrame's `pc` do not go through `jumpTo`:\n\t%s\n"+
			"Contract §3 SP-1 requires a safepoint check at every loop back-edge, and a raw "+
			"assignment is a back-edge the poll cannot see — a guest looping through this arm "+
			"is unstoppable, and no spec vector will tell you, because the corpus never asks "+
			"an engine to stop.\n"+
			"The fix is `pc = st.t.jumpTo(<target>, pc)`, which polls only when the jump goes "+
			"backwards (ADR 0059, safepoint.go). Do not add this line to an exemption list: "+
			"the exemptions here are the `for` header's own Init and Post, identified by "+
			"position in the syntax tree rather than by text, precisely so a hand-written "+
			"forward step cannot join them.",
			len(raw), strings.Join(raw, "\n\t"))
	}
	if routedCount != routed {
		t.Errorf("%d pc assignments route through jumpTo, expected exactly %d.\n"+
			"Fewer means this walk has stopped seeing the assignments — and the rule above "+
			"then passes by asking nothing, which is the vacuous state this census exists to "+
			"refuse. More means a new control-flow arm landed: read ADR 0059 on why the poll "+
			"is guarded at the assignment, confirm the new arm's target can go backwards, and "+
			"bump this number in the same commit.", routedCount, routed)
	}
}

// TestNothingTakesTheAddressOfRunFramesPC pins the premise the control above depends on and cannot
// itself check.
//
// [ADR 0059] rejects a `jumpTo(target int, pc *int)` shape on cost grounds — a pointer parameter forces
// `pc` out of a register and pays option A's per-instruction tax by another route. It has a second
// consequence, which is this test's subject: a helper that writes through a pointer would assign `pc`
// at a site the syntax walk above cannot see, so the completeness control would report fourteen routed
// assignments and full coverage while a fifteenth write happened inside a callee.
//
// So the walk's blind spot is stated as an assertion rather than as a caveat in a comment. Watched die
// by adding `_ = &pc` to the `opNop` arm, which failed here and — correctly — not above.
//
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
func TestNothingTakesTheAddressOfRunFramesPC(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "exec.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing exec.go: %v", err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return true
		}
		id, ok := u.X.(*ast.Ident)
		if !ok || id.Name != "pc" {
			return true
		}
		t.Errorf("exec.go:%d takes the address of `pc`.\n"+
			"TestEveryPCAssignmentInRunFrameGoesThroughJumpTo walks assignment statements, so "+
			"a write through a pointer is invisible to it: it would report full coverage of "+
			"fourteen routed assignments while a fifteenth write happened in a callee, and a "+
			"guest looping through that path would be unstoppable (contract §3 SP-1). ADR 0059 "+
			"rejects the pointer form on cost grounds as well — `pc` stays in a register only "+
			"while its address is never taken.", fset.Position(u.Pos()).Line)
		return true
	})
	// No floor on the negative direction: the parse is asserted above by `t.Fatalf`, and that this
	// walk can see `pc` at all is what the previous test's census asserts — it fails at zero.
}

// spinModule is the guest for the arrival tests: a counter loop with one back-edge per iteration, and
// nothing else in the body that could reach a safepoint by another route.
//
// Built through `text.EncodeModule` → `binary.DecodeModule` rather than as a `binary.Module` literal,
// grave #125's reason: a hand-built module lets a test run against something the decoder never
// produced, and the shape under test here is precisely what the decoder emits for a `loop`/`br_if`
// pair. `trips` is a parameter so one module serves both a stop-me-mid-loop arm and a short arm.
func spinModule(t *testing.T) *Instance {
	t.Helper()
	const src = `(module
		(func (export "spin") (param i32) (result i32) (local i32)
			(loop
				(local.set 1 (i32.add (local.get 1) (i32.const 1)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1)))
				(br_if 0 (local.get 0)))
			(local.get 1)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encoding the spin module: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decoding the spin module: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiating the spin module: %v — it declares no memory, so a trap here is "+
			"a finding about instantiation rather than about safepoints", trap)
	}
	return in
}

// gatedSpinModule is `spinModule`'s sibling with one difference: **the loop's exit condition is a word
// the host writes, not a trip count.** `spin` runs until `release` stores into shared memory, and it
// returns the trips it took to get there. A second word is a caller-arrival count, bumped once on
// entry and readable through `entered`, so the host has an exact signal that every caller is in the
// guest before it stops the world — `awaitQueued`'s move in `futex_test.go`, for its reason: *a test's
// premise is not its assertion, and a premise that fails silently means nothing was measured.*
//
// # Grave #598, which is what a trip count buys and what it costs
//
// A counted loop makes the *duration* of the guest's work the thing a test's deadline is measured
// against, and `spinningCallers` below then used a deadline as a hang detector. Three concurrent
// 10,000,000-trip callers under `-race` came within 2.5s of that arm's 30s bound on an idle dev box at
// `GOMAXPROCS=1`, and exceeded it on a four-core CI runner sharing itself with six other packages —
// so the arm reported a wedge, in those words, over three callers that were `[runnable]` inside the
// interpreter and blocked on nothing. Raising the trip count reproduces that red verbatim, which is
// the proof the trigger cannot tell the two apart.
//
// A gated loop moves both properties the arm needs out of the scheduler's hands:
//
//   - **Every caller is inside the guest when `Stop` runs, and stays there.** The gate is zero until
//     the test stores it, so no caller can finish early, and the arrival count says all three are past
//     the loop entry rather than still queued behind a goroutine start. The counted version had
//     neither: its callers were in the loop only because the loop was long, which is the same
//     coincidence the deadline was resting on.
//   - **The post-`Resume` wait bounds wedge-freedom only.** A released caller returns in microseconds,
//     so the deadline finally sits orders of magnitude away from what it bounds, and a red means
//     blocked rather than slow. *An unasserted distance is the vacuum*, and this is the direction that
//     asserts it.
//
// **What it still does not buy is three parks in one round**, and that is worth saying because the
// gate makes it tempting to claim. `Stop` returns on the first arrival and `Resume` clears `stopReq`,
// so a caller that has not reached a back-edge by then never parks — the window is now a few
// instructions wide rather than a whole loop, but it is not zero, and *narrow is not closed*. The arm
// that closes it by construction is `expiringWaits` below, which is why there are two.
//
// What is given up is the exact trip count, and with it this arm's incidental frame-integrity check:
// a gated loop's return value is whatever the schedule made it. That claim is asserted exactly, on a
// single caller, by `TestStopBringsAGuestLoopToASafepointAndResumeLetsItFinish`'s third clause, which
// is why `spinModule` keeps its counted form and this is a second builder rather than an edit to it.
// A positive count is still checked here, for the vacuity reason every other count in this package is:
// zero trips would mean the loop never ran.
//
// The memory is `shared` and the accesses are atomic because the flag is written by one agent and read
// by three: a plain load in the loop against a plain store from the host is a data race on guest memory
// and `-race` is an authority this package answers to (`TestAtomicRmwIsNotObservablyTornAcrossThreads`
// is the same reasoning one file over). That makes the threads feature a decoder requirement here, so
// this builder configures the decoder where `spinModule` can use the default.
func gatedSpinModule(t *testing.T) *Instance {
	t.Helper()
	const src = `(module
		(memory 1 1 shared)
		(func (export "spin") (param $gate i32) (param $count i32) (result i32) (local $n i32)
			(drop (i32.atomic.rmw.add (local.get $count) (i32.const 1)))
			(loop
				(local.set $n (i32.add (local.get $n) (i32.const 1)))
				(br_if 0 (i32.eqz (i32.atomic.load (local.get $gate)))))
			(local.get $n))
		(func (export "release") (param $gate i32)
			(i32.atomic.store (local.get $gate) (i32.const 1)))
		(func (export "entered") (param $count i32) (result i32)
			(i32.atomic.load (local.get $count))))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encoding the gated spin module: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decoding the gated spin module: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiating the gated spin module: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("instantiating the gated spin module fell short: %v", derr)
	}
	return in
}

// TestStopBringsAGuestLoopToASafepointAndResumeLetsItFinish is contract §3 SP-1's own sentence, run:
// *"a host request `stop(deadline)` brings every guest thread to a safepoint within a bounded,
// configurable interval."*
//
// **Three claims, and the third is the one a weaker test would miss.** That `Stop` returns nil rather
// than its deadline error — arrival happened. That it returns *while the guest is still inside its
// loop* — the guest's own return value is the trip count, so a stop that had merely waited for the
// call to finish would be indistinguishable from one that worked, which is why the loop is long enough
// that finishing before the stop is not the plausible reading. And that after `Resume` the guest
// **finishes with the right answer**: a safepoint that corrupted the frame, the value stack or `pc`
// would still satisfy the first two.
//
// The deadline is generous on purpose. This asserts that arrival happens within *a* bound, which is
// what the clause says; the *tightness* of that bound is `loopbench`'s subject and a pre-registered
// figure, not a timing assertion in a test that has to pass on a loaded CI runner. *An unmeasured
// stability claim is not a protection.*
func TestStopBringsAGuestLoopToASafepointAndResumeLetsItFinish(t *testing.T) {
	// Sized from a measurement rather than guessed: this loop runs at ~19.7M trips/s on the dev
	// box (`darwin/arm64`), so ten million trips is about half a second of guest work. Long enough
	// that the stop — issued microseconds later, on a request that is sticky either way — lands
	// mid-loop by a margin of four orders of magnitude, and short enough that `Resume` leads to a
	// real answer instead of a goroutine spinning past the end of the test. A larger count would
	// buy no confidence and would be paid by every future run of the suite.
	const trips = 10_000_000

	in := spinModule(t)
	done := make(chan []Value, 1)
	errs := make(chan error, 1)
	go func() {
		out, err := in.Invoke("spin", I32(trips))
		if err != nil {
			errs <- err
			return
		}
		done <- out
	}()

	// **No wait for the goroutine to get going, and none is needed** — which is worth stating,
	// because a `sleep` here would look like diligence. The request is *sticky*: `Stop` sets
	// `stopReq` and then waits, so a guest that enters after the request is already set polls at its
	// first call site (`enterFrame`) and parks. Ordering the two cannot make the stop miss; it can
	// only change which poll observes it. *A duration is not a completion signal*, and here there is
	// no signal to wait for.
	if in.host.stopReq.Load() {
		t.Fatal("stopReq is set before Stop was called — `Stop` is its only writer, so this is " +
			"the field starting life wrong rather than a race with the goroutine above")
	}

	if err := in.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v — the guest is inside a loop whose every iteration crosses a "+
			"back-edge, so a deadline expiry here means the poll is not on that path at all "+
			"(contract §3 SP-1, ADR 0059)", err)
	}

	// Still inside the loop. The guest returns its trip count, so a completed call would have
	// delivered on `done`; a stop that worked cannot have let it finish 200M iterations.
	select {
	case out := <-done:
		t.Fatalf("the guest finished (%v) before the stop — impossible if the stop worked, "+
			"since arrival is reported from inside the park, so read this as the loop being "+
			"too short for the test to be about safepoints at all and raise `trips`", out)
	case err := <-errs:
		t.Fatalf("the guest failed: %v", err)
	default:
	}

	in.Resume()

	select {
	case out := <-done:
		if len(out) != 1 || out[0].Bits != trips {
			t.Errorf("after Resume the guest returned %v, want %d — the loop counts its own "+
				"trips, so a wrong count means parking and releasing at a back-edge "+
				"perturbed `pc`, the value stack or the frame. A stop that returns cleanly "+
				"and leaves a wrong answer behind is worse than one that times out",
				out, trips)
		}
	case err := <-errs:
		t.Fatalf("after Resume the guest failed: %v — nothing about SP-1 makes a stop "+
			"observable to the guest as an error", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the guest did not finish 30s after Resume — the release channel is not " +
			"reaching the parked thread, which is a hang rather than a degraded mode")
	}
}

// TestStopSucceedsWhenNoCallerIsExecuting is the arm that `TestStopReportsItsDeadlineWhenNothingPolls`
// turned into, a name that no longer exists in this package, and the rename is the point: it was a
// requirement for the deadline **error** on an idle instance, and its own doc comment pre-registered this
// replacement —
//
//	When SP-2 lands, this test **changes** — the expected outcome becomes nil, immediately — and its
//	name survives that, because it is still what `Stop` reports.
//
// The forecast was right about the expectation and wrong about the name. *A control is named after the
// rule, not the property*, and `…ReportsItsDeadlineWhenNothingPolls` states an outcome that is now false
// of the engine: nothing polls here and the deadline is not reported. So the name went with the
// expectation, and the vacuity refutation the old arm also carried moved to the arm below rather than
// being quietly dropped with it.
//
// **What landed to falsify it is [decision 0067](../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md)'s
// caller count**, whose subject was #592 and whose side effect this is. The old comment named exactly
// what was missing — *"a per-thread in-guest bit set and cleared at the boundary"* — and `callers` is
// that, as a count rather than a bit because one `thread` serves N concurrent `Invoke` calls. With it,
// `Stop`'s predicate is `blocked == callers`, an idle instance satisfies it at `0 == 0`, and the world is
// stopped the moment it is asked for.
//
// **The old behaviour was worse than a pinned limit and this is worth stating plainly: `Stop` on an idle
// instance could never succeed.** `want` counted a thread with no caller on it, no guest code existed to
// reach a poll, and every such call burned its whole deadline and returned an error. That is not a
// missing guarantee, it is a `Stop` an embedder cannot use on the one state where stopping is trivial.
//
// **The generous deadline is the assertion's mechanism, not padding.** If `Stop` waited for an arrival
// here it would wait the full 30s and then return `ErrStopDeadline`, so `err == nil` is by itself the
// proof that it did not wait — no elapsed time is measured, and none needs to be.
func TestStopSucceedsWhenNoCallerIsExecuting(t *testing.T) {
	in := spinModule(t)

	if err := in.Stop(30 * time.Second); err != nil {
		t.Fatalf("Stop on an instance with no caller executing returned %v, want nil.\n"+
			"No guest code is running, so no caller can touch guest memory before observing the "+
			"stop, which is contract §3 SP-2's condition satisfied at `blocked == callers` with "+
			"both zero. An error here — and it can only be the deadline, since this call has "+
			"30s — means `Stop` is waiting for an arrival from a thread that has no caller to "+
			"send one (decision 0067, #592)", err)
	}

	in.Resume()

	// The instance still runs afterwards, which is the half a cleanup test usually omits: a stop
	// that left `stopReq` set would park the next guest forever.
	out, err := in.Invoke("spin", I32(3))
	if err != nil {
		t.Fatalf("after a Stop and a Resume, the guest failed: %v — the request was not cleared, "+
			"so the instance is stopped with nobody to release it", err)
	}
	if len(out) != 1 || out[0].Bits != 3 {
		t.Errorf("after a Stop and a Resume the guest returned %v, want 3", out)
	}
}

// TestStopReportsItsDeadlineWhenACountedCallerNeverArrives is the vacuity refutation for
// `TestStopBringsAGuestLoopToASafepointAndResumeLetsItFinish`, re-pointed from the subject that
// dissolved above.
//
// **A `Stop` that returned nil unconditionally passes every arrival test in this file perfectly**, so
// something has to require the deadline error, or a green from the loop test distinguishes *"the world
// stopped"* from nothing at all. The old arm got that from an idle instance, which now — correctly —
// succeeds. *A tripwire whose subject dissolves is re-pointed*: the rule it was protecting is that
// `Stop`'s failure path is reachable, and that rule is untouched by 0067.
//
// # Why the state is constructed rather than driven, and what that costs
//
// The natural subject is a caller that is executing guest code and cannot reach a poll before the
// deadline. It is available and it is **not deterministic**: `poll` is reached from a back-edge in
// `jumpTo`, from `enterFrame`, and from `tailcall.go`, so an unpolled interval is exactly one
// straight-line stretch long, and holding a caller in one for a bounded wall-clock interval is a race
// between the deadline and the stretch. It would be a test whose *false* direction is a false FAIL,
// which is the flake shape rather than the conservative one.
//
// **A caller can be held unpolled indefinitely, and the mechanism for it is a blocking host call — which
// does not exist yet ([#602](https://github.com/scttfrdmn/burroughs/issues/602)).** That is the
// deterministic program-driven form of this control and it arrives with host functions; until then the
// state is built directly, which this package's tests already do with `thread` literals (see `thread`'s
// own doc comment on a nil `w`).
//
// So: a second thread joins the world with one counted caller and no goroutine. `blocked == callers` is
// `0 == 1`, it is counted in `want`, nothing will ever send its arrival, and the deadline is the only
// outcome available. Zero timing dependence, and it exercises the *mixed* accounting at the same time —
// the instance's own thread is at a safepoint while this one is not, which is what makes the message
// below read `1 of 2` rather than `0 of 1`.
func TestStopReportsItsDeadlineWhenACountedCallerNeverArrives(t *testing.T) {
	in := spinModule(t)

	// No goroutine, and that is the construction: a counted caller that cannot arrive. The id is
	// out of the way of any the engine assigns, so a failure message naming it is unambiguous.
	ghost := &thread{id: 9999}
	in.world.register(ghost)
	in.world.mu.Lock()
	ghost.callers = 1
	in.world.mu.Unlock()

	// Short, because this arm waits for a bound to *expire* rather than for work to finish. Nothing
	// here depends on how short: the arrival it waits for cannot happen at any deadline.
	const deadline = 100 * time.Millisecond

	err := in.Stop(deadline)
	if !errors.Is(err, ErrStopDeadline) {
		t.Fatalf("Stop with a counted caller that cannot arrive returned %v, want %v.\n"+
			"%s carries one caller and no goroutine, so `blocked == callers` is `0 == 1` and it "+
			"is counted in `want` — nothing exists to send its arrival. A nil here means "+
			"arrival was reported by something other than a thread reaching a safepoint, and "+
			"that would make TestStopBringsAGuestLoopToASafepointAndResumeLetsItFinish pass "+
			"without measuring anything", err, ErrStopDeadline, ghost)
	}
	// The message must carry the shortfall, or a caller learns only that something expired. Checked
	// because a failure message is a claim nothing else in this tree scans.
	//
	// `1 of 2` and not `0 of 2`: the instance's own thread has no caller executing, so it is at a
	// safepoint and counted, and this thread is the one that never arrives. A message reading `0 of
	// 2` would mean the at-safepoint thread was not credited, which is a different bug in the same
	// arithmetic.
	if got := err.Error(); !strings.Contains(got, "1 of 2 arrived") {
		t.Errorf("the deadline error reads %q, want it to name the shortfall as `1 of 2 "+
			"arrived` — a partial stop and a total one leave the world in different states, "+
			"and a caller deciding what to do next has no other channel for the difference", got)
	}

	// Resume after a failed stop is the documented cleanup, and it must not wedge: nothing is
	// parked, so this is the no-op path, and a lock held by mistake would hang here rather than
	// anywhere a reader would look.
	in.Resume()

	// And the instance still runs afterwards, which is the half a cleanup test usually omits: a
	// failed stop that left `stopReq` set would park the next guest forever.
	out, err := in.Invoke("spin", I32(3))
	if err != nil {
		t.Fatalf("after a timed-out Stop and a Resume, the guest failed: %v — the request was "+
			"not cleared, so the instance is stopped with nobody to release it", err)
	}
	if len(out) != 1 || out[0].Bits != 3 {
		t.Errorf("after a timed-out Stop and a Resume the guest returned %v, want 3", out)
	}
}

// TestStopRefusesASecondConcurrentStop asserts the one state `world` cannot represent.
//
// Two overlapping stops would share `resume` and `arrived`: the second would replace both while
// threads were parked on the first, and the release would then close a channel nobody was waiting on
// while the parked threads waited on one nothing would close. So the second call is refused rather
// than queued, and the refusal is asserted here because *the defect stated as the rule* is what a
// comment alone would be — the guard is three lines and the failure it prevents is a hang.
func TestStopRefusesASecondConcurrentStop(t *testing.T) {
	const trips = 10_000_000

	in := spinModule(t)
	done := make(chan []Value, 1)
	errs := make(chan error, 1)
	go func() {
		out, err := in.Invoke("spin", I32(trips))
		if err != nil {
			errs <- err
			return
		}
		done <- out
	}()

	if err := in.Stop(5 * time.Second); err != nil {
		t.Fatalf("the first Stop: %v", err)
	}
	if err := in.Stop(5 * time.Second); err == nil {
		t.Error("a second Stop while the world is stopped returned nil. It would have " +
			"replaced the release channel the parked thread is waiting on, so Resume would " +
			"close a channel nobody holds and the guest would never wake — a hang, not a " +
			"wrong answer")
	}
	in.Resume()

	select {
	case <-done:
	case err := <-errs:
		t.Fatalf("the guest failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the guest did not finish after Resume")
	}
}

// TestStopBringsATailCallLoopToASafepoint is the arm without which `enterFrame`'s poll could be
// deleted and nothing in this tree would notice.
//
// **A tail-recursive guest assigns `pc` nowhere.** The two functions below alternate by `return_call`
// and neither body contains a loop, so [ADR 0059]'s fourteen `pc` sites are never reached: what spins
// is `enterFrame`'s trampoline (0026/#253), an engine loop the guest cannot see and `runFrame` never
// re-enters. A poll placed only at back-edges leaves this shape running forever with a stop request
// pending, which is contract §3 SP-1's *"bounded interval"* becoming unbounded on the one program shape
// the tail-call proposal added.
//
// So this test's subject is not the guest's control flow at all — it is the engine's, and *lessons are
// indexed by shape*: the question "where are the loops" has to be asked of the engine's own code and
// not only of the instruction stream it walks. Watched die by deleting `st.t.poll()` from `enterFrame`,
// which leaves the back-edge test green and fails here at the deadline.
//
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
func TestStopBringsATailCallLoopToASafepoint(t *testing.T) {
	// A tail chain rather than a loop, and `even`/`odd` because that is `return_call.wast`'s own
	// shape — the vector whose 1M depth 0026 measured, so the arithmetic here is the corpus's rather
	// than invented for this test.
	const src = `(module
		(func $even (export "even") (param i32) (result i32)
			(if (i32.eqz (local.get 0)) (then (return (i32.const 1))))
			(return_call $odd (i32.sub (local.get 0) (i32.const 1))))
		(func $odd (param i32) (result i32)
			(if (i32.eqz (local.get 0)) (then (return (i32.const 0))))
			(return_call $even (i32.sub (local.get 0) (i32.const 1)))))`

	// `tailModule` and not `spinModule`: `return_call` is gated off in `DefaultFeatures`, so a
	// module this test builds through the default decoder is rejected at decode and the failure
	// reads as an engine defect rather than as a gate posture (`tailcall_test.go`'s `tailGate`,
	// whose header states the two-gate split). Reusing that helper inherits the posture instead of
	// restating it here, where a second copy could drift from the gate it names.
	in := tailModule(t, src)

	// Even, so the answer is 1 and a wrong parity would be visible rather than plausible.
	const depth = 4_000_000

	done := make(chan []Value, 1)
	errs := make(chan error, 1)
	go func() {
		out, err := in.Invoke("even", I32(depth))
		if err != nil {
			errs <- err
			return
		}
		done <- out
	}()

	if err := in.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v.\n"+
			"This guest crosses no back-edge — it recurses by `return_call`, so the fourteen "+
			"`pc` assignments ADR 0059 guards are never executed. A deadline expiry here means "+
			"the only safepoint on this path is missing: the unconditional poll at the top of "+
			"`enterFrame`'s trampoline loop (tailcall.go). Contract §3 SP-1 names call sites "+
			"alongside back-edges, and this is why.", err)
	}
	select {
	case out := <-done:
		t.Fatalf("the chain finished (%v) before the stop — raise `depth`", out)
	case err := <-errs:
		t.Fatalf("the guest failed: %v", err)
	default:
	}

	in.Resume()

	select {
	case out := <-done:
		if len(out) != 1 || out[0].Bits != 1 {
			t.Errorf("after Resume the chain returned %v, want 1 — parking inside the "+
				"trampoline perturbed the frame it was about to enter, which is the failure "+
				"a stop that merely *returns* cannot distinguish from a stop that worked", out)
		}
	case err := <-errs:
		t.Fatalf("after Resume the guest failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the chain did not finish 30s after Resume")
	}
}

// TestAResumedGuestSeesAHostWriteFromTheStop is contract §4 **B-MM-1** at the safepoint boundary, and
// it is the demand `TestNoEngineLockIsHeldAcrossAChannelOperation`'s predecessor made of this slice:
// *"B-MM-1's acquire edge established on any resume that follows releasing it."*
//
// B-MM-1 names *"async wake"* among the transitions that MUST constitute *"an acquire edge over the
// entire shared address space for the resuming agent"*, and a safepoint resume is that shape — the host
// stops the world in order to look at or change something, so a thread resuming without the edge could
// carry on against a stale view. The sequence run here is the clause's own: guest inside a loop, host
// stops it, host writes guest memory **while it is parked**, host resumes, guest observes the write.
//
// **The write is a plain byte store into the image and not `memory.write`**, which is the difference
// between a test with teeth and one without. [ADR
// 0054](../../docs/decisions/0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md)
// made every aligned guest access atomic, so **the guest's load is already an atomic one** — the
// falsification's own trace names `sync/atomic.LoadUint32` inside `memAccess` — and routing the host
// store through `memory.write` would make both sides atomic, at which point `-race` says nothing
// **whether or not the safepoint established any edge at all**. The host side is therefore the only one
// whose form this test controls, and a plain store is the weakest write a host can make: it is the one
// whose visibility actually depends on the boundary being an edge.
//
// **`-race` is the authority here and the assertion is the weaker half.** The returned 1 says the guest
// observed the flag, which a sufficiently lucky run could produce without any ordering; the detector
// says the host store and the guest load are *ordered*, which is the clause. So this test's verdict
// under `make check` and its verdict under `make race` are different claims, and the second is the one
// B-MM-1 asks for.
//
// Watched die: deleting the `Stop`/`Resume` pair and writing the flag straight into the running guest's
// memory — the same two accesses with no edge between them — is reported by `-race` as a data race on
// the image's byte 0, naming the guest's load and this goroutine's store. That is the falsification, and
// it is the reason the write is not routed through the engine's own atomic path.
func TestAResumedGuestSeesAHostWriteFromTheStop(t *testing.T) {
	// Large enough that the loop cannot exhaust itself before the stop lands — the sibling tests'
	// measured ~19.7M trips/s on the dev box makes this about five seconds of guest work — and
	// bounded so that a *failure* to observe the flag is a returned 0 rather than a hung test.
	const maxTrips = 100_000_000

	const src = `(module
		(memory 1)
		(func (export "waitflag") (param $max i32) (result i32) (local $i i32)
			(loop $l
				(if (i32.load (i32.const 0)) (then (return (i32.const 1))))
				(local.set $i (i32.add (local.get $i) (i32.const 1)))
				(br_if $l (i32.sub (local.get $max) (local.get $i))))
			(i32.const 0)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encoding the flag module: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decoding the flag module: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiating the flag module: %v", trap)
	}

	done := make(chan []Value, 1)
	errs := make(chan error, 1)
	go func() {
		out, ierr := in.Invoke("waitflag", Value{Type: binary.I32, Bits: maxTrips})
		if ierr != nil {
			errs <- ierr
			return
		}
		done <- out
	}()

	if serr := in.Stop(10 * time.Second); serr != nil {
		t.Fatalf("Stop before the host write: %v — the guest never reached a safepoint, so the "+
			"visibility question below was never asked", serr)
	}

	// The host write, while the world is stopped. Byte 0 of the published image, which is the word
	// the guest's `i32.load (i32.const 0)` reads.
	in.mems[0].view()[0] = 1

	in.Resume()

	select {
	case out := <-done:
		if len(out) != 1 || out[0].Bits != 1 {
			t.Errorf("the resumed guest returned %v, want a single 1. A 0 means it ran out its "+
				"%d trips without ever observing a store the host made while it was parked at a "+
				"safepoint, which is B-MM-1's acquire edge missing on the resume: the clause "+
				"requires the edge over the entire address space, not over a word the engine "+
				"chose to publish", out, maxTrips)
		}
	case rerr := <-errs:
		t.Fatalf("the guest failed after Resume: %v", rerr)
	case <-time.After(30 * time.Second):
		t.Fatal("the guest did not finish 30s after Resume, so it was neither released nor bounded")
	}
}

// TestThreeConcurrentCallersAndAStopDoNotHang is grave **#593**'s regression, named by that issue.
//
// The defect: `parkAtSafepoint` sent one arrival *per park* into a channel `Stop` sized
// `len(w.members)`, and the comment beside the send argued it could not block because *"the buffer is
// `len(w.members)` and each thread sends once per round"*. Each thread does; the **sender is a caller**.
// `link.go` registers one `thread` per instance (#592), so N concurrent `Invoke` calls are N senders
// sharing one member and one buffer slot: caller A's send is received by `Stop`, caller B's fills the
// buffer, and **caller C blocks on the send forever** — never reaching `<-release`, so `Resume` cannot
// free it, and `Resume` nils `w.arrived` so nothing ever will. A permanent hang, no gate, reachable
// through the public API.
//
// **The hang is detected rather than suffered.** Every wait here is a `select` against an interval, so a
// wedged caller is a reported failure that names how many callers came back; letting the test simply
// block would surface as the package timeout killing every other test in it, which reports the harness
// and not the engine.
//
// # Two arms, because the grave's own sequence reproduces the defect only in one direction
//
// `spinningCallers` is the sequence the issue describes: three goroutines invoking a looping export, one
// `Stop`. It used to have a false-negative window, conceded here in prose — `Stop` returns as soon as
// *one* arrival lands, and callers B and C had to reach a poll before the `Resume` that followed, which
// they did "in practice". Grave **#598** closed it, from the other end than expected: the loop is now
// gated on a word the host writes (`gatedSpinModule`), so every caller is inside it when `Stop` runs by
// construction, and the post-`Resume` deadline bounds only whether `Resume` freed them.
//
// `expiringWaits` closes that: three callers suspended in `memory.atomic.wait` whose intervals expire
// while the world is stopped. `awaitQueued` is an exact signal that all three are suspended before the
// stop begins, and each one's wake runs `leaveBlocked` → `poll` → `parkAtSafepoint`, so the round
// contains three parks by construction rather than by scheduling. With `thread.reported` removed this
// arm reports **1 of 3** callers back, and with it, three.
func TestThreeConcurrentCallersAndAStopDoNotHang(t *testing.T) {
	const callers = 3

	t.Run("spinningCallers", func(t *testing.T) {
		// Two naturally aligned words: the gate the loop watches and the count each caller bumps
		// on entry. Distinct addresses, four bytes apart, so neither access needs arithmetic in
		// the assertion's way.
		const (
			gate  = 0
			count = 4
		)

		in := gatedSpinModule(t)
		done := make(chan outcome, callers)
		for range callers {
			go func() { done <- callOffGoroutine(in, "spin", I32(gate), I32(count)) }()
		}
		back := 0

		// **Whatever happens above, no caller is left running inside the guest.** `boundaryCrossings`
		// is a package-level counter that is never reset, so an abandoned `enterGuest` is an odd
		// parity in `TestEveryBoundaryCrossingIsPaired` — a red in another test, naming a missing
		// `defer` at an engine site, caused by a leak here. That is grave **#599**, and its own
		// repair scopes that read to its own delta; this half removes the occasion. `Resume` first
		// and unconditionally, because it is a no-op on a world that is not stopped and the release
		// invoke would otherwise park behind a stop this arm failed before lifting.
		t.Cleanup(func() {
			in.Resume()
			if _, err := in.Invoke("release", I32(gate)); err != nil {
				t.Errorf("releasing the gated loop during cleanup: %v — callers may still be "+
					"inside the guest, which poisons `boundaryCrossings` for whatever runs "+
					"next (grave #599)", err)
			}
			for back < callers {
				select {
				case <-done:
					back++
				case <-time.After(30 * time.Second):
					t.Errorf("%d of %d callers were still inside the guest 30s after the flag was "+
						"set and the world resumed, so this arm is leaving crossings unpaired "+
						"(grave #599)", callers-back, callers)
					return
				}
			}
		})

		// **The premise, waited on rather than assumed.** Without this the arm is a green over
		// nothing at all: three goroutines that have not yet reached `Invoke` register no thread,
		// so `Stop` would stop an empty world, `Resume` would resume it, and the release would let
		// three callers run a loop no stop ever touched. Zero of the arm's subject, exercised.
		// *An analytic zero is not a measurement*, and 0.00s is what tipped it — the counted
		// version's eight seconds hid the same hole behind work.
		deadline := time.Now().Add(10 * time.Second)
		entered := int32(0)
		for entered < callers && time.Now().Before(deadline) {
			res, err := in.Invoke("entered", I32(count))
			if err != nil {
				t.Fatalf("reading the arrival count: %v", err)
			}
			entered = res[0].Int32()
			if entered < callers {
				time.Sleep(time.Millisecond)
			}
		}
		if entered != callers {
			t.Fatalf("only %d of %d callers entered the guest within 10s — this is the arm's "+
				"premise and not its assertion: a stop over a world none of them had joined "+
				"would measure nothing", entered, callers)
		}

		if err := in.Stop(5 * time.Second); err != nil {
			t.Fatalf("Stop with %d concurrent callers: %v", callers, err)
		}
		in.Resume()

		// The gate opens only now, so up to here every caller was held inside the loop rather than
		// racing the stop to the end of a trip count.
		if _, err := in.Invoke("release", I32(gate)); err != nil {
			t.Fatalf("releasing the gated loop: %v — this is the arm's premise and not its "+
				"assertion, since an unreleased loop never returns", err)
		}

		for back < callers {
			select {
			case r := <-done:
				back++
				// Vacuity, not integrity: a gated loop's trip count is whatever the schedule
				// made it, and zero would mean the body never ran. The exact-count claim —
				// that a safepoint left the frame, the value stack and `pc` intact — is
				// asserted on a single caller by
				// `TestStopBringsAGuestLoopToASafepointAndResumeLetsItFinish`.
				if got := r.get(t); got <= 0 {
					t.Errorf("a caller returned %d trips, want a positive count: the loop it was "+
						"released from had to execute at least once", got)
				}
			case <-time.After(30 * time.Second):
				// **What this deadline can and cannot distinguish.** The flag is set and the
				// world is resumed, so a caller that has not returned is not merely slow —
				// one trip is a load and a branch. What it does not do is say *where* it is
				// blocked, and the message must not claim to: grave #598 was this arm
				// asserting `parkAtSafepoint`'s arrival send by name over three callers that
				// were `[runnable]` in the interpreter. Run the package with `-timeout` just
				// under this interval and the runtime prints the stacks that answer it.
				t.Fatalf("%d of %d callers returned within 30s after Resume and after the gate "+
					"was opened, so at least one was not freed. The hang grave #593 names is "+
					"the arrival send inside `parkAtSafepoint`, which happens *before* "+
					"`<-release` — but this arm observes only that a caller did not return, "+
					"so read the goroutine dump before believing that", back, callers)
			}
		}
	})

	t.Run("expiringWaits", func(t *testing.T) {
		const timeout = time.Second

		in := futexModule(t)
		done := make(chan outcome, callers)
		for range callers {
			go func() {
				done <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(int64(timeout)))
			}()
		}
		awaitQueued(t, in, 0, callers)

		if err := in.Stop(2 * time.Second); err != nil {
			t.Fatalf("Stop with %d callers suspended in memory.atomic.wait: %v — contract §3 SP-2 "+
				"makes every one of them at a safepoint (see "+
				"TestAStopCompletesWithAThreadSuspendedInAWaitAndDoesNotWakeIt)", callers, err)
		}
		// The intervals expire in here, so all three parks happen inside the round.
		select {
		case r := <-done:
			t.Fatalf("a wait returned %d while the world was stopped (see "+
				"TestAWaitThatExpiresDuringAStopDoesNotReturnUntilResume)", r.res)
		case <-time.After(2 * timeout):
		}
		in.Resume()

		for i := range callers {
			select {
			case r := <-done:
				if got := r.get(t); got != waitTimedOut {
					t.Errorf("a wait returned %d, want %d (\"timed-out\")", got, waitTimedOut)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%d of %d callers returned within 30s after Resume — the arrival send is "+
					"per caller rather than per thread per round, so the buffer `Stop` sized from "+
					"the membership is full and the rest are wedged before `<-release` "+
					"(grave #593)", i, callers)
			}
		}
	})
}
