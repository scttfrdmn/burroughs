// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// sequentialReading is wasmtime's committed reading of the sequential arm — the acceptance target #857
// registered and #871 is the first slice that can reach.
const sequentialReading = "testdata/asynclift/sequential.reading"

// readingField pulls a named field out of a committed reading (`ARRIVALS  1`).
//
// It FAILS rather than defaults when the field is absent, for the reason every reader in this package
// does: a witness that falls back to a built-in expectation is checking itself.
func readingField(t *testing.T, path, field string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading %s is missing, so this witness has no oracle: %v", path, err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, field); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("the committed reading %s has no %q line; regenerate it with build.sh", path, field)
	return ""
}

// TestSequentialArmReproducesWasmtimesReading is #871's acceptance witness.
//
// # What the committed reading says, and why it is the NEGATIVE arm
//
//	MODE      sequential
//	EVENTS    enter(1) -> suspend(1) -> expire(1)
//	OUTCOME   host/task error: rendezvous expired after 5s with only 1 arrival(s)
//	ARRIVALS  1
//
// `tick` is a rendezvous that releases only on a SECOND arrival. Driving `run` sequentially — not issuing
// the second call until the first resolves — means the second arrival can never happen, so the first call
// waits out its bound with exactly one arrival. **The reading is a measurement of a guest that cannot
// finish**, and reproducing it means reproducing that, not reproducing a success.
//
// # Which facts are asserted, and the one difference that is recorded rather than forced
//
// Asserted: **exactly one arrival**, the call **did not complete**, and the failure **names the expiry**.
// Those are the reading's load-bearing content and they are read from the committed file, not restated.
//
// Recorded difference: in wasmtime the *host rendezvous* times out at 5s and the error surfaces from the
// host import; in Burroughs the host impl defers and it is the *park's bound* that expires, because an
// `asyncLowerImpl` that has already returned has no channel to report a later error through — `onResolve`
// carries a value, not a result. So the two engines end the same way for different proximate reasons.
// Matched as an outcome and the mechanism difference stated, exactly as the `compute` reading's gate-off
// arm is (wasmtime refuses at parse, Burroughs at instantiate). Forcing a match would mean reshaping a
// refusal to imitate another engine's layering.
//
// # THIS ARM CANNOT DETECT A BROKEN CALLBACK RE-ENTRY, and that is measured, not suspected
//
// #871's registered falsification was *"neuter the callback re-entry so the callback is never called
// after WAIT; the sequential arm must then fail by name, not hang."* Run: **this arm passes with the
// re-entry neutered.** It has to — it asserts an *expiry*, and an engine that never re-enters the
// callback also expires. Same verdict, different cause, so the arm is blind to the difference.
//
// It is structurally blind, not fixably so: on the sequential arm the rendezvous never releases, so **no
// event ever arrives and a correct engine never re-enters the callback either**. There is no observation
// here that separates the two engines.
//
// The arm that does separate them is the POSITIVE one —
// `TestHostCanSupplyABareWorldLevelImport/a_deferring_import_parks_and_completes_when_resolved`, which
// resolves the import and requires the lift to resume with the right value. Under the same neuter it
// fails with *"the lift never resumed after its waitable set was signalled — a park did not wake"*.
//
// So this is the acceptance target and that is the falsification target, and they are different arms.
// *Assert the arms differ, not only that the null matches*: a witness whose expected outcome is a failure
// cannot be falsified by a change that also produces a failure.
func TestSequentialArmReproducesWasmtimesReading(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}

	// The park's bound is shortened: this arm WANTS the expiry, and the engine's 30s backstop is not a
	// test's timescale. The bound is a var for exactly this.
	restore := liftParkBound
	liftParkBound = 500 * time.Millisecond
	defer func() { liftParkBound = restore }()

	h := NewHost(io.Discard, io.Discard, nil)
	var arrivals int32
	var mu sync.Mutex
	var pending []func(canon.Value)
	h.asyncImpls = map[string]asyncLowerImpl{
		// The rendezvous, mirroring the committed harness: release only once TWO calls have arrived.
		// Deferring is what makes the guest park; the release that never comes is what makes it expire.
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			params := onStart()
			arg := uint32(0)
			if len(params) > 0 {
				arg = uint32(params[0].Int32())
			}
			n := atomic.AddInt32(&arrivals, 1)
			mu.Lock()
			pending = append(pending, onResolve)
			release := n >= 2
			all := pending
			mu.Unlock()
			if release {
				// Never reached on the sequential arm — kept so the impl is a real rendezvous rather
				// than one that only ever defers, which would prove nothing about why it expired.
				for _, r := range all {
					r(canon.U32(arg))
				}
			}
			return func() {}, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	// SEQUENTIAL: the second call is not issued until the first returns, so the rendezvous cannot close.
	// That ordering is the whole arm — issuing both concurrently is #869's measurement and a different
	// reading.
	_, first := in.CallValues("run", canon.U32(1))

	if first == nil {
		t.Fatal("the first sequential call COMPLETED — the rendezvous needs a second arrival that " +
			"sequential driving cannot supply, so completing means the park resolved on something else")
	}
	if !errors.Is(first, ErrLiftParkExpired) {
		t.Errorf("the call failed, but not as an expired park: %v", first)
	}
	// The expiry must be named, not merely signalled: a bound that ends in silence reports "still
	// waiting" as though it were "nothing to report".
	if !strings.Contains(first.Error(), "produced nothing in") {
		t.Errorf("the failure does not say the park reached its bound: %v", first)
	}

	// ARRIVALS, from the committed reading rather than a literal.
	if got, want := itoa(uint32(atomic.LoadInt32(&arrivals))), readingField(t, sequentialReading, "ARRIVALS"); got != want {
		t.Errorf("arrivals = %s, wasmtime's committed reading says %s", got, want)
	}
	// And the reading's own OUTCOME must still be the expiry it was when this witness was written — if
	// the reading is ever regenerated into a SUCCESS, the assertions above are measuring the wrong thing
	// and this is what says so.
	if out := readingField(t, sequentialReading, "OUTCOME"); !strings.Contains(out, "expired") {
		t.Errorf("the committed reading no longer records an expiry (%q), so this arm's premise is "+
			"stale — re-derive it from the new reading rather than editing the assertions", out)
	}
}

// TestYieldReentersTheCallbackWithNoEvent is the YIELD arm, on hand-authored bytes.
//
// # Why a fixture rather than the committed guest
//
// The committed Rust guests never yield: `wit-bindgen`'s async codegen awaits imports, which produces
// WAIT, and nothing in its surface emits a cooperative yield. So YIELD has no real consumer, and without
// this fixture the branch would land beside two that are exercised and be the only one that is not.
//
// # The assertion is split across the host and the guest, deliberately
//
// From here: the call **completes** with the task resolved, so a YIELD routed into the park (which would
// block on handle 0, a set the guest never named) fails as an expired park instead.
//
// From inside the fixture: the callback **traps unless its event triple is all zero**. That half cannot be
// checked from out here — the host sees only the packed return — and it is the half that catches an engine
// re-entering with a fabricated or carried-over event. `unreachable` in the guest is the assertion.
func TestYieldReentersTheCallbackWithNoEvent(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/async-lift-yield-synth.wasm")
	if err != nil {
		t.Fatalf("the hand-authored yield fixture is missing (regenerate from its .wat): %v", err)
	}
	// Shortened so a YIELD misrouted into the park fails fast and legibly rather than stalling the suite
	// for the engine's 30s backstop. The distinction still holds: a misroute ends as ErrLiftParkExpired,
	// which is a different verdict from the trap a bad event triple produces.
	restore := liftParkBound
	liftParkBound = 500 * time.Millisecond
	defer func() { liftParkBound = restore }()

	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	cd, ok := in.export.exports["run"]
	if !ok || cd.fn == nil {
		t.Fatal("no run export")
	}

	resolved, err := cd.fn.invokeWith(nil) // the resolution is a return value now (#869)
	if err != nil {
		// Both falsifications were run, and they fail by DIFFERENT routes — worth naming, because a
		// reader debugging this arm needs to know which one they are looking at:
		//
		//   * YIELD routed into the park  -> trap "WAIT named handle 0, which is not a waitable set",
		//     immediately. NOT an expired park: a yield packs no set index, so the park rejects handle 0
		//     at its first lookup rather than waiting out the bound. (A first draft of this arm expected
		//     the expiry and said so; running it corrected that.)
		//   * YIELD re-entered with a fabricated event -> trap "unreachable", from the fixture's own
		//     all-zero check, which is the half the host side cannot see.
		t.Fatalf("the yielding lift did not complete: %v", err)
	}

	// The task resolved through task.return in the callback, so the loop must have re-entered the
	// callback at all — a loop that treated YIELD as EXIT would have trapped on the unresolved task
	// instead, which is why the resolution is asserted and not just the absence of an error.
	if got := resolved; len(got) != 1 {
		t.Fatalf("the resolved task carries %d values, want 1 (task.return's u32)", len(got))
	} else if got[0].Int32() != 42 {
		t.Errorf("task.return delivered %d, want 42 — the value the fixture resolves with", got[0].Int32())
	}
}

// concurrentReading is wasmtime's committed reading of the concurrent arm — #869's acceptance target,
// registered by #857 and unreachable until now.
const concurrentReading = "testdata/asynclift/concurrent.reading"

// TestConcurrentArmReproducesWasmtimesReading is #869's acceptance witness AND its crossed-result
// falsification. They coincide here, which is stated rather than relied on quietly.
//
// # The committed reading
//
//	MODE      concurrent
//	EVENTS    enter(1) -> suspend(1) -> enter(2) -> release(by 2) -> resume(1)
//	OUTCOME   concurrent: first=Ok((1,)) second=Ok((2,))
//	ARRIVALS  2
//
// `tick` is a rendezvous that releases only on a **second** arrival, and `run` passes its result through.
// So the reading records two facts at once: both tasks were in flight (or the rendezvous could never have
// closed), and **each caller got its own value**.
//
// # Why this is also the crossed-result arm
//
// #871's lesson was that a witness whose expected outcome is a *failure* cannot be falsified by a change
// that also produces a failure, so its falsification target must be a positive arm. Here the acceptance
// arm is already positive and already asserts per-caller values, so acceptance and falsification are the
// same arm — the coincidence is real, not an oversight. Spelled out because the lesson's remedy was
// "find the positive arm", and silently landing on one would look like having skipped the question.
//
// What it discriminates, which a completion-only check would not:
//
//	a shared current-task slot   -> first and second get the SAME value, or each other's
//	one task admitted at a time  -> the rendezvous never closes; both expire at the bound
//	both admitted, values right  -> the reading
//
// The second row matters because it is what the engine did before this slice: the at-most-one trap would
// refuse the second lift outright, so no amount of value-checking could have been reached.
//
// # The rendezvous is structural, not timed
//
// A second arrival cannot happen unless the first task has already parked, because one engine thread
// serves the instance and the first call holds it until it parks. So **the second arrival is the proof of
// concurrency** and no clock is consulted for the verdict — the same discipline as the committed harness.
func TestConcurrentArmReproducesWasmtimesReading(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}

	h := NewHost(io.Discard, io.Discard, nil)
	var mu sync.Mutex
	type pending struct {
		arg     uint32
		resolve func(canon.Value)
	}
	var waiting []pending
	var arrivals int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			params := onStart()
			arg := uint32(0)
			if len(params) > 0 {
				arg = uint32(params[0].Int32())
			}
			atomic.AddInt32(&arrivals, 1)
			mu.Lock()
			waiting = append(waiting, pending{arg: arg, resolve: onResolve})
			release := len(waiting) >= 2
			all := waiting
			mu.Unlock()
			if release {
				// **Each resolver gets its OWN argument back.** If the engine crossed the tasks, the
				// values would still be 1 and 2 but would arrive at the wrong callers — which is the
				// defect this arm exists to catch, and it is only catchable because the two differ.
				for _, p := range all {
					p.resolve(canon.U32(p.arg))
				}
			}
			return func() {}, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	type outcome struct {
		id  uint32
		got []canon.Value
		err error
	}
	results := make(chan outcome, 2)
	for _, id := range []uint32{1, 2} {
		go func(id uint32) {
			got, e := in.CallValues("run", canon.U32(id))
			results <- outcome{id: id, got: got, err: e}
		}(id)
	}

	byID := map[uint32]outcome{}
	for range 2 {
		select {
		case o := <-results:
			byID[o.id] = o
		case <-time.After(20 * time.Second):
			// The arrival count distinguishes the two ways this fails: 1 arrival means the second lift
			// never reached the import at all (not admitted, or blocked behind the first), while 2 means
			// it did and the resolution path is what broke.
			in.w.async.mu.Lock()
			cur, nf := in.w.async.lift, in.w.async.liftsInFlight
			in.w.async.mu.Unlock()
			t.Fatalf("only %d of 2 concurrent lifts returned (arrivals=%d, current task %v, lifts in "+
				"flight %d) — the rendezvous never closed", len(byID), atomic.LoadInt32(&arrivals), cur, nf)
		}
	}

	// ARRIVALS, from the committed reading rather than a literal. Two arrivals is the structural proof
	// that both tasks were in flight at once.
	if got, want := itoa(uint32(atomic.LoadInt32(&arrivals))), readingField(t, concurrentReading, "ARRIVALS"); got != want {
		t.Errorf("arrivals = %s, wasmtime's committed reading says %s", got, want)
	}

	// Each caller gets ITS OWN value. This is the crossed-result assertion.
	for _, id := range []uint32{1, 2} {
		o := byID[id]
		if o.err != nil {
			t.Errorf("run(%d): %v", id, o.err)
			continue
		}
		if len(o.got) != 1 {
			t.Errorf("run(%d) returned %d values, want 1", id, len(o.got))
			continue
		}
		v, ok := o.got[0].U32()
		if !ok {
			t.Errorf("run(%d) returned kind %v, want u32", id, o.got[0].Type.Kind)
			continue
		}
		if v != id {
			t.Errorf("run(%d) = %d — a caller received ANOTHER task's result. The current-task slot or "+
				"the resolution path is shared between concurrently parked lifts", id, v)
		}
	}

	// And the reading's OUTCOME must still be the two-success shape this arm was written against. If it
	// is ever regenerated into a failure, the assertions above are measuring the wrong thing.
	if out := readingField(t, concurrentReading, "OUTCOME"); !strings.Contains(out, "first=Ok") || !strings.Contains(out, "second=Ok") {
		t.Errorf("the committed reading no longer records two successes (%q), so this arm's premise is "+
			"stale — re-derive it from the new reading rather than editing the assertions", out)
	}
}

// TestConcurrentLiftsDoNotShareMutableStateOnTheCompFunc is the arm the race detector earned.
//
// # Why the value assertions above were not enough
//
// `TestConcurrentArmReproducesWasmtimesReading` asserts that each caller receives its own value, which is
// exactly the crossed-result property — and **it passed while the engine had a data race on it.** The
// resolution was stored on `compFunc.result`, a field shared by every concurrent caller of the export, so
// two callers wrote and read one slot. Whether that produces a wrong value depends on how the writes and
// reads interleave, and on an unloaded machine they interleaved favourably every time.
//
// `go test -race` reported it immediately: the write at `invokeAsyncLiftWith` against the read in
// `CallValues`. So the lesson is narrower than "assert the values": **a crossing whose symptom is
// timing-dependent needs the race detector, because a value assertion samples one interleaving.** The
// repair was to return the resolution rather than store it, which makes it per-call by construction
// instead of by luck.
//
// # What this arm adds beyond `-race` being in CI
//
// `make ci` runs the race gate, so the regression would be caught — but only as "some race somewhere in
// this package". This arm names the subject: it drives the concurrent path hard enough that a shared
// mutable field is exercised, and it asserts the per-caller values under repetition. Run under `-race` it
// localises; run without, it is still a stress of the property.
//
// It is deliberately NOT a check that `compFunc` has no `result` field — that would be a shape assertion
// about today's code rather than about the risk, and the next shared field would have a different name.
func TestConcurrentLiftsDoNotShareMutableStateOnTheCompFunc(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}

	// Several rounds, because one round is one interleaving. The defect this guards against was invisible
	// in a single pass.
	for round := range 5 {
		h := NewHost(io.Discard, io.Discard, nil)
		var mu sync.Mutex
		type pending struct {
			arg     uint32
			resolve func(canon.Value)
		}
		var waiting []pending
		h.asyncImpls = map[string]asyncLowerImpl{
			"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
				params := onStart()
				arg := uint32(0)
				if len(params) > 0 {
					arg = uint32(params[0].Int32())
				}
				mu.Lock()
				waiting = append(waiting, pending{arg: arg, resolve: onResolve})
				all, release := waiting, len(waiting) >= 2
				mu.Unlock()
				if release {
					for _, p := range all {
						p.resolve(canon.U32(p.arg))
					}
				}
				return func() {}, nil
			},
		}
		in, err := InstantiateWithHost(b, h)
		if err != nil {
			t.Fatalf("round %d: instantiate: %v", round, err)
		}

		// Distinct, round-dependent arguments: a crossing between the two callers shows up as a value
		// swap, and values that differ per round also catch a stale read from a previous round.
		a, bArg := uint32(round*10+1), uint32(round*10+2)
		type outcome struct {
			id uint32
			v  uint32
			ok bool
		}
		res := make(chan outcome, 2)
		for _, id := range []uint32{a, bArg} {
			go func(id uint32) {
				got, e := in.CallValues("run", canon.U32(id))
				if e != nil || len(got) != 1 {
					res <- outcome{id: id}
					return
				}
				v, k := got[0].U32()
				res <- outcome{id: id, v: v, ok: k}
			}(id)
		}
		for range 2 {
			select {
			case o := <-res:
				if !o.ok {
					t.Errorf("round %d: run(%d) did not return a u32", round, o.id)
					continue
				}
				if o.v != o.id {
					t.Errorf("round %d: run(%d) = %d — a caller received another task's resolution, so "+
						"some state is shared between concurrent lifts of one export", round, o.id, o.v)
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("round %d: a concurrent lift never returned", round)
			}
		}
		in.Close()
	}
}

// TestASiblingWaitsForABlockingImportAndDoesNotTrap is the chair's witness for the reverted excursion
// release (#882 review).
//
// # What it asserts, and why the ORDER is the load-bearing part
//
// Task A blocks inside a host import for far longer than any bound this engine has. Task B must:
//
//  1. **wait, not trap** — contention is the normal state of two concurrent callers; and
//  2. **not run any guest code until A's entry has finished.**
//
// (2) is the guarantee the excursion release broke, and it is why this arm records an ORDER rather than
// just two successes. The model holds `exclusive_thread` across a callback-lifted task's whole entry
// (`needs_exclusive`, def:417), and that is not a scheduling preference: it is the promise that no other
// task's guest code runs in the instance while this task's guest frame is live. **A correct guest may
// depend on it** — a Rust guest holding a `RefCell` borrow, or its executor's state, across a blocking
// import must not have another task's callback interleave. Releasing during the excursion permits exactly
// that, so a correct guest could panic here and run fine against the reference.
//
// The committed guests do not exercise it, which is why no witness caught it and why this one asserts the
// order explicitly instead of inferring it from both calls completing.
//
// # Falsification
//
// Release the slot across the excursion (the reverted design) and `B-tick` appears **before**
// `A-block-end`: B's guest ran while A's frame was live. Watched die that way.
func TestASiblingWaitsForABlockingImportAndDoesNotTrap(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}

	const blockFor = 600 * time.Millisecond

	var mu sync.Mutex
	var order []string
	say := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	h := NewHost(io.Discard, io.Discard, nil)
	var pend []func(canon.Value)
	var pendArgs []uint32
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			params := onStart()
			arg := uint32(0)
			if len(params) > 0 {
				arg = uint32(params[0].Int32())
			}
			mu.Lock()
			pend = append(pend, onResolve)
			pendArgs = append(pendArgs, arg)
			first := len(pend) == 1
			allR, allA := pend, pendArgs
			mu.Unlock()

			if first {
				say("A-tick")
				say("A-block-start")
				// A real §5 excursion: the engine thread is released, which is precisely the window the
				// reverted design handed to a sibling.
				_ = c.Blocking(func() error { time.Sleep(blockFor); return nil })
				say("A-block-end")
				return func() {}, nil
			}
			say("B-tick")
			for i, r := range allR {
				r(canon.U32(allA[i]))
			}
			return func() {}, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	type outcome struct {
		id  uint32
		v   uint32
		ok  bool
		err error
	}
	res := make(chan outcome, 2)
	start := func(id uint32) {
		go func() {
			got, e := in.CallValues("run", canon.U32(id))
			o := outcome{id: id, err: e}
			if e == nil && len(got) == 1 {
				o.v, o.ok = got[0].U32()
			}
			res <- o
		}()
	}
	start(1)
	// B is issued once A is demonstrably inside its block, so the arm measures the window it claims to.
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		started := len(order) >= 2
		mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("A never reached its blocking import")
		}
		time.Sleep(2 * time.Millisecond)
	}
	start(2)

	byID := map[uint32]outcome{}
	for range 2 {
		select {
		case o := <-res:
			byID[o.id] = o
		case <-time.After(30 * time.Second):
			mu.Lock()
			seen := append([]string(nil), order...)
			mu.Unlock()
			t.Fatalf("only %d of 2 lifts returned; order was %v", len(byID), seen)
		}
	}

	// (1) Neither trapped, and each got its own value.
	for _, id := range []uint32{1, 2} {
		o := byID[id]
		if o.err != nil {
			t.Errorf("run(%d) failed rather than waiting: %v", id, o.err)
			continue
		}
		if !o.ok || o.v != id {
			t.Errorf("run(%d) = %d (u32 %v) — want its own argument back", id, o.v, o.ok)
		}
	}

	// (2) THE ORDER. B's guest code must not have run before A's block finished.
	mu.Lock()
	seen := append([]string(nil), order...)
	mu.Unlock()
	idx := func(s string) int {
		for i, v := range seen {
			if v == s {
				return i
			}
		}
		return -1
	}
	aEnd, bTick := idx("A-block-end"), idx("B-tick")
	if aEnd < 0 || bTick < 0 {
		t.Fatalf("the arm did not observe both events it compares: order was %v", seen)
	}
	if bTick < aEnd {
		t.Errorf("B's guest code ran at position %d, BEFORE A's block ended at %d (order: %v).\n\n"+
			"The instance's exclusivity must be held across a blocking host call: it is the guarantee "+
			"that no other task's guest code runs while this task's guest frame is live, which a correct "+
			"guest may depend on (a RefCell borrow or executor state held across a blocking import).",
			bTick, aEnd, seen)
	}
}

// TestLiftParkExpiryIsNamedNotAHang is the falsification the chair registered: neuter the re-entry and the
// sequential arm must fail BY NAME rather than hang.
//
// # Why this is a separate arm rather than a comment
//
// "It would fail by name" is a forecast until the failure is produced. The neuter here is the real one —
// `awaitEvent` is called with a bound and nothing ever signals the set — so what it demonstrates is the
// property the bound exists for: **a park that cannot be satisfied ends in a verdict**. An engine whose
// park had no bound would not fail this test, it would never finish it, which is precisely why the
// `ciwatch.sh` `unfinished` principle is cited at the bound's definition.
func TestLiftParkExpiryIsNamedNotAHang(t *testing.T) {
	h := newAsyncHandles()
	s := &waitableSet{wake: make(chan struct{})}
	h.mu.Lock()
	si := uint32(h.addLocked(s))
	h.mu.Unlock()

	start := time.Now()
	// The task is `liftStarted` and uncancelled, so the park's cancel arm (ADR 0094) is inert here and the
	// bound is still the only thing that can end this wait — which is what keeps this test's subject the
	// bound rather than cancellation. A task in `liftPendingCancel` would return TASK_CANCELLED at once
	// and the expiry would never be reached, so the state is set explicitly rather than left to a zero
	// value that happens to work.
	task := &liftTask{state: liftStarted, cancelWake: make(chan struct{})}
	_, err := h.awaitEvent(task, si, 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a park on a set nothing will ever signal RETURNED — it cannot have had an event")
	}
	if !errors.Is(err, ErrLiftParkExpired) {
		t.Errorf("the park ended, but not as ErrLiftParkExpired: %v", err)
	}
	// The set index must be in the message: by the time a park expires, the packed return it came from is
	// several entries behind, so the index is what tells a reader WHICH set never produced an event.
	if !strings.Contains(err.Error(), itoa(si)) {
		t.Errorf("the expiry does not name the waitable set it waited on: %v", err)
	}
	// It must have actually waited. A bound that fires immediately would pass every assertion above while
	// turning any legitimate wait into a failure.
	if elapsed < 150*time.Millisecond {
		t.Errorf("the park returned after %s, far short of its 200ms bound — it did not wait", elapsed)
	}
	// And the waiter count must be released, or the set becomes permanently undroppable.
	h.mu.Lock()
	waiting := s.waiting
	h.mu.Unlock()
	if waiting != 0 {
		t.Errorf("after the park expired the set still counts %d waiter(s) — waitable-set.drop would "+
			"trap forever on a set nobody is parked on", waiting)
	}
}

// TestParkedLiftReleasesTheCurrentTaskSlot pins the interleaving discipline's load-bearing half: while a
// task is parked, `h.lift` is nil.
//
// # Why this matters more than the visible assertion
//
// A built-in must act on the task whose code is running. Between a WAIT return and the next re-entry
// **none of the task's code is running**, so a `task.return` arriving then is a guest error — and if the
// slot still held the parked task it would silently succeed, resolving a task that never returned. That is
// a wrong answer with no trap, which is worse than the at-most-one assertion this slice had to split in
// two to preserve.
func TestParkedLiftReleasesTheCurrentTaskSlot(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}
	restore := liftParkBound
	liftParkBound = 400 * time.Millisecond
	defer func() { liftParkBound = restore }()

	h := NewHost(io.Discard, io.Discard, nil)
	entered := make(chan struct{})
	var once sync.Once
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, _ func(canon.Value)) (func(), error) {
			onStart()
			once.Do(func() { close(entered) })
			return func() {}, nil // defer forever: the guest parks and stays parked
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	done := make(chan error, 1)
	go func() { _, e := in.CallValues("run", canon.U32(1)); done <- e }()

	select {
	case <-entered:
	case e := <-done:
		t.Fatalf("the lift finished before parking, so there is no parked state to inspect: %v", e)
	case <-time.After(5 * time.Second):
		t.Fatal("the host impl was never entered")
	}

	// The guest has returned WAIT by the time the park's bound is running. Poll briefly for the slot to
	// clear rather than assuming the ordering: the import returns, then the guest unwinds to its lift
	// return, then the loop parks, and only the last of those releases the slot.
	deadline := time.Now().Add(2 * time.Second)
	for {
		in.w.async.mu.Lock()
		cur, n := in.w.async.lift, in.w.async.liftsInFlight
		in.w.async.mu.Unlock()
		if cur == nil && n == 1 {
			break // the state the discipline requires: no current task, but one lift in flight
		}
		if time.Now().After(deadline) {
			in.w.async.mu.Lock()
			cur, n = in.w.async.lift, in.w.async.liftsInFlight
			in.w.async.mu.Unlock()
			if cur != nil {
				t.Fatal("while the task was PARKED the current-task slot still held it — a task.return " +
					"arriving now would resolve a task whose code is not running")
			}
			// The count is the bookkeeping half: it no longer gates entry (#869 permits several lifts),
			// but a park that lost its count would mean teardown is asymmetric and a leak invisible.
			if n != 1 {
				t.Fatalf("while the task was parked the in-flight count was %d, want 1 — the loop's "+
					"accounting does not survive a park", n)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	if e := <-done; !errors.Is(e, ErrLiftParkExpired) {
		t.Errorf("the parked lift ended as %v, want ErrLiftParkExpired", e)
	}
	// And after the loop exits, the in-flight marker is released — or no further lift could ever run.
	in.w.async.mu.Lock()
	n := in.w.async.liftsInFlight
	in.w.async.mu.Unlock()
	if n != 0 {
		t.Errorf("the in-flight count is %d after the loop exited, want 0 — the decrement is not paired "+
			"with the increment on every exit path, so lifts leak", n)
	}
}
