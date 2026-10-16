package component

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The witnesses for ADR 0101's Option A amendment, the run slot held by the Thread executing guest code
// (#978). They are lettered as the chair's build list, and each is bounded, because the defect each one
// guards against is a hang.

const optionABound = 10 * time.Second

// callBounded instantiates a cmthreads fixture with both gates on, calls `export`, and fails the test if the
// call does not return within the bound. It returns the instance, which it closes at the test's end, and
// the result.
func callBounded(t *testing.T, fixture, export string) (*Instantiated, []canon.Value, error) {
	t.Helper()
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/cmthreads/" + fixture + ".wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("Instantiate %s: %v", fixture, err)
	}
	t.Cleanup(in.Close)
	type out struct {
		res []canon.Value
		err error
	}
	ch := make(chan out, 1)
	go func() {
		r, e := in.CallValues(export)
		ch <- out{r, e}
	}()
	select {
	case o := <-ch:
		return in, o.res, o.err
	case <-time.After(optionABound):
		t.Fatalf("%s: %s() did not return within %s — the hang this witness exists to catch", fixture, export,
			optionABound)
		return nil, nil, nil
	}
}

func wantU32(t *testing.T, fixture string, res []canon.Value, err error, want uint32) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: run: %v", fixture, err)
	}
	if v, _ := res[0].U32(); v != want {
		t.Fatalf("%s: run returned %d; the oracle reads %d", fixture, v, want)
	}
}

func wantTrapContaining(t *testing.T, fixture string, err error, needle string) {
	t.Helper()
	var trap *interp.Trap
	if !errors.As(err, &trap) || !strings.Contains(err.Error(), needle) {
		t.Fatalf("%s: run returned %v; want the trap containing %q", fixture, err, needle)
	}
}

// TestASyncTypedCallEntersPastASuspendedAsyncTask is item a, #978's fixture: an async task's implicit
// Thread suspends mid-callee with nothing runnable, and a SYNC-typed call enters past its
// `exclusive_thread` to resume it. wasmtime 49.0.2 returns 8.
//
// **Neuters, run:**
//   - `giveBack` keeping the slot: it saves the state and marks the scheduler holding but does not
//     give the slot up. The sync `f` then cannot enter, and this times out.
//   - `releaseRunFor` never kicking: `f` readies `run` and returns, nothing runs the queue, and this times
//     out.
func TestASyncTypedCallEntersPastASuspendedAsyncTask(t *testing.T) {
	_, res, err := callBounded(t, "sync-enters-past-suspended", "run")
	wantU32(t, "sync-enters-past-suspended", res, err, 8)
}

// TestAThreadReadiedBeforeWAITRunsDuringTheWait is item b, #998's fixture: a Thread readied before the
// callback returns WAIT runs while the task waits. wasmtime 49.0.2 returns 5, and so does the calibrated
// reference reading.
//
// **Neuter, run:** `releaseRunFor` never kicking. The callback's entry frees the slot with T ready, nothing
// runs it, and this times out.
func TestAThreadReadiedBeforeWAITRunsDuringTheWait(t *testing.T) {
	_, res, err := callBounded(t, "wait-runs-ready", "run")
	wantU32(t, "wait-runs-ready", res, err, 5)
}

// TestATrapWhileASiblingWaitsEndsTheCallAndTheWaiter is item c. T1 waits on a subtask that only T2 would
// release, and T2 traps first. wasmtime 49.0.2 and the reference both read T2's trap. The call returns the
// trap, and T1's wait is ended, so every Thread is gone once the call has returned.
//
// The completing control (`unreachable` → `nop`) returns 1. That is the reference reading; wasmtime hits
// its own bug on that shape.
//
// **Neuters, run:**
//   - `trap` dropping the error: it returns false without `routeFault` or `killParked`. `run` then
//     re-YIELDs forever, and this times out.
//   - `killParked` skipping waiters: the call still returns the trap, but T1 stays registered, and this
//     fails on the live count.
func TestATrapWhileASiblingWaitsEndsTheCallAndTheWaiter(t *testing.T) {
	in, _, err := callBounded(t, "trap-while-waiting", "run")
	wantTrapContaining(t, "trap-while-waiting", err, "unreachable")
	deadline := time.Now().Add(optionABound)
	for in.w.threads.live() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d Thread(s) still registered after the trap — T1's wait was not ended",
				in.w.threads.live())
		}
		time.Sleep(time.Millisecond)
	}

	_, res, cerr := callBounded(t, "trap-while-waiting-control", "run")
	wantU32(t, "trap-while-waiting-control", res, cerr, 1)
}

// TestATrapUnwindsThroughASyncCrossComponentCall is item c′. A nested child's SYNC `h` readies Thread T
// and waits, and T traps. wasmtime 49.0.2 and the reference both read T's trap. The unwind runs through the
// sync cross-component arm, and the child's run slot and Thread table are left empty.
//
// The control arm (T releases instead) traps in `h`'s wait, at the sync tail, as both oracles do. The sync
// tail resumes only the waiting instance's Threads, and `g` runs in a third instance. It returned 5 until
// #1005, a conformance defect the chair classified on the Option A build's review.
//
// **Neuter, run:** `routeFault` not aborting a waiting owner. `h` is T's owner and is itself waiting, so
// the trap never reaches its wait. `killParked(owner)` still ends that wait, as one of the owner's waiters,
// so the call fails with *"ended while parked"* in place of T's trap, and this fails on the reason. The
// note first predicted a hang; the run measured the reason.
func TestATrapUnwindsThroughASyncCrossComponentCall(t *testing.T) {
	in, _, err := callBounded(t, "nested-sync-unwind", "run")
	wantTrapContaining(t, "nested-sync-unwind", err, "unreachable")
	var child *compFunc
	for _, ci := range in.w.compInstances {
		if ci.inst == nil {
			continue
		}
		if cd, ok := ci.inst.exports["h"]; ok && cd.fn != nil {
			child = cd.fn
		}
	}
	if child == nil {
		t.Fatal("the fixture's child instance exports no `h`")
	}
	deadline := time.Now().Add(optionABound)
	for len(child.h.runSlot) != 0 || child.threads.live() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("after the trap the child's run slot holds %d and its Thread table %d — the unwind "+
				"left the child's state behind", len(child.h.runSlot), child.threads.live())
		}
		time.Sleep(time.Millisecond)
	}

	_, res, cerr := callBounded(t, "nested-sync-unwind-control", "run")
	_ = res
	wantTrapContaining(t, "nested-sync-unwind-control", cerr, "trap_if(not candidates)")
}

// TestAnotherThreadOfTheChainRunsWhileOneWaits is item d, #977's residual.
//   - T1 waits on a nested child's `g`, and `g` WAITs on the parent's own async `h2`.
//   - `h2` YIELDs until a flag that only T2 sets, so T1's wait can end only if T2 runs while T1 waits.
//
// The oracle is the REFERENCE READING: 8, with the control (T2 doing nothing) reading HANG. wasmtime 49.0.2
// hits its own bug on this shape.
//
// **Neuter, run:** the wait as a bare release, with `suspending` freeing the slot instead of sending the
// `detach` handoff (the #977 amendment's behaviour). The scheduler keeps waiting on T1's handoff and never
// hears `h2`'s YIELD request, so T2 never runs, and this times out.
//
// A first shape passed under that neuter. There, `f`'s own handoff satisfied the stuck scheduler, so the
// fixture header records why the shape was replaced.
func TestAnotherThreadOfTheChainRunsWhileOneWaits(t *testing.T) {
	_, res, err := callBounded(t, "residual", "run")
	wantU32(t, "residual", res, err, 8)
}

// TestAnAsyncTaskEntersWhileAnotherYields is item e. While `run` YIELDs, its Thread's call reaches the
// parent's own async `h` through a nested child, and `h` needs the parent's `entrySem`. wasmtime 49.0.2
// returns 8.
//
// **Neuter, run, and it does NOT fail; that is a finding, not a pass.** The neuter is `yieldPoint`
// re-taking `entrySem` around its request, the pre-A arm. Under A the re-take cannot be observed:
//   - While the YIELD's chain runs, the run slot already excludes every entrant, and the chain hands the
//     slot from Thread to Thread without freeing it.
//   - T's wait ends the chain. The reply then lets `yieldPoint` release `entrySem`, and `h` enters.
//
// The re-take deadlocked only under the chain model, where the scheduler sat on the waiting Thread's
// handoff and the reply never came. So this fixture guards that deadlock, and A's removal of the re-take is
// kept because it is the reference's order, not because a guest can see it.
//
// No shape that would make the re-take observable was found. It would need an async entry that must
// happen while a Thread of the yielding task EXECUTES, without that Thread waiting. A synchronous call
// cannot reach an async lift (`bindCrossComponent` refuses it), and a sync lift's wait is the divergence
// recorded on #999.
func TestAnAsyncTaskEntersWhileAnotherYields(t *testing.T) {
	_, res, err := callBounded(t, "yield-admits-entry", "run")
	wantU32(t, "yield-admits-entry", res, err, 8)
}

// TestATrapAfterItsOwnerReturnedIsKept is item f, #999's item 2. A Thread traps after its owner's call has
// returned, so no call can surface the trap. It is kept in `threadTable.orphanTraps`, an internal field read
// here after the fact.
//
// **Neuter, run:** `routeFault` not appending to `orphanTraps`, which fails on the empty field.
func TestATrapAfterItsOwnerReturnedIsKept(t *testing.T) {
	in, res, err := callBounded(t, "orphan-trap", "run")
	wantU32(t, "orphan-trap", res, err, 1)
	pr, perr := in.CallValues("poke")
	if perr != nil {
		t.Fatalf("poke: %v — the trap belongs to run's Thread, not to poke's call", perr)
	}
	if v, _ := pr[0].U32(); v != 0 {
		t.Fatalf("poke returned %d, want 0", v)
	}
	deadline := time.Now().Add(optionABound)
	for {
		in.w.threads.mu.Lock()
		orphans := append([]error(nil), in.w.threads.orphanTraps...)
		in.w.threads.mu.Unlock()
		if len(orphans) > 0 {
			if !strings.Contains(orphans[0].Error(), "unreachable") {
				t.Fatalf("the orphaned trap is %v; want T's unreachable", orphans[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no orphaned trap recorded — a trap with no call to reach was dropped silently")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAnAppendToTheReadyQueueRequiresTheRunSlot is item g, the lost-wakeup argument as an instrument
// (point 5). In a test binary, `markReady` with the slot held succeeds, and the same call with the slot not
// held panics.
//
// **Neuter, run:** making `assertSlotHeld` return at once, which fails the panic arm.
func TestAnAppendToTheReadyQueueRequiresTheRunSlot(t *testing.T) {
	h := newAsyncHandles()
	tt := newThreadTable()
	tt.h = h
	a := &cmThread{state: threadSuspended}
	b := &cmThread{state: threadSuspended}
	ia, ib := tt.add(a), tt.add(b)

	h.runSlot <- struct{}{}
	h.mu.Lock()
	h.runChain, h.runDepth = &runChain{}, 1
	h.mu.Unlock()
	if err := tt.markReady(ia); err != nil {
		t.Fatalf("markReady with the slot held: %v", err)
	}
	h.mu.Lock()
	h.runChain, h.runDepth = nil, 0
	h.mu.Unlock()
	<-h.runSlot

	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "run slot not held") {
			t.Fatalf("markReady with the slot not held: recovered %v; want the assertion's panic", r)
		}
	}()
	_ = tt.markReady(ib) // expected to panic before it returns
}
