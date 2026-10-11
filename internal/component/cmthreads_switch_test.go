package component

import (
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// # ADR 0100's switching slice: `thread.suspend` and the four switching forms, on the scheduler goroutine
//
// Witnessed against `cmthreads/switch.reading`, taken with wasmtime 49.0.2 and committed before any of
// these forms was built (#960), so the engine is measured against a reading it could not have influenced.

// TestSwitchWasmReturnsTheCommittedReading is the slice's completion condition: `switch.wasm` runs through
// Burroughs and returns `0x0888887f`, the committed reading.
//
// The fixture's callee calls all five forms from the IMPLICIT Thread, inside the callee — the shape the
// second slice's `runReady` could not carry at all, since the lift's own goroutine is the one blocked in the
// canon call. Each form's body sets a marker bit and records its own `thread.index`, so the value pins:
//
//   - **every form was reached and its body ran** — the six-bit mask;
//   - **each body ran as its own Thread** — index 2, not the entry's 1;
//   - **each body unregistered before the next was created** — 2 every time, the reference's slot reuse.
//     Monotonic numbering would read 2,3,4,5,6 and set every marker bit while being wrong.
//
// **Neuter, run:** making `giveBack` return at once without handing off (the implicit Thread never parks)
// makes `run` return `0x41` — the mask's bit 0 and the entry index 1, no body having run before the result
// was packed (`indices [1 0 0 0 0 0]`). Restoring it returns the reading.
func TestSwitchWasmReturnsTheCommittedReading(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	want := readCmthreadsResult(t, "switch.reading")
	wasm, err := os.ReadFile("testdata/cmthreads/switch.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	if got := callRunBounded(t, in); got != want {
		t.Fatalf("run returned %#x; the committed wasmtime reading is %#x (mask %#x, indices %v)",
			got, want, got&0x3f, unpackSwitchIndices(got))
	}
	// Every created Thread unregistered and the implicit one with its task, so nothing leaks.
	if n := in.w.threads.live(); n != 0 {
		t.Errorf("%d Thread(s) still registered after run returned", n)
	}
}

// unpackSwitchIndices reads `switch.wat`'s six 4-bit index fields, for a failure message a reader can
// compare with the reading's INDICES block.
func unpackSwitchIndices(v uint32) [6]uint32 {
	var out [6]uint32
	for i := range out {
		out[i] = (v >> (6 + 4*uint(i))) & 0xf
	}
	return out
}

// TestAThreadThatSuspendsPartwayLetsTheYieldingCallbackContinue is the amendment's first deadlock: a
// created Thread that gives the slot up PARTWAY through its body, under a callback that keeps YIELDing.
//
// The second build slice's scheduling point waited for each Thread's goroutine to EXIT. `suspend-partway`'s
// body suspends before it exits, so that wait could end only after a resume — which only the blocked
// scheduling point could have issued. The scheduler's chain instead ends at the body's handoff, the
// callback runs, readies the body once, and the next YIELD finishes it. wasmtime 49.0.2 returns 6 on 30 of
// 30 runs (`suspend-partway.reading`).
//
// **Neuter, run:** making `drive` wait for a created Thread's EXIT rather than its next handoff — skipping
// every handoff that is not `exited` — makes `run` hang, and this fails on `callRunBounded`'s bound.
func TestAThreadThatSuspendsPartwayLetsTheYieldingCallbackContinue(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	want := readCmthreadsResult(t, "suspend-partway.reading")
	wasm, err := os.ReadFile("testdata/cmthreads/suspend-partway.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	if got := callRunBounded(t, in); got != want {
		t.Fatalf("run returned %#b; suspend-partway.reading records %#b (bit 1: ran to the suspend, "+
			"bit 2: resumed and finished)", got, want)
	}
	if n := in.w.threads.live(); n != 0 {
		t.Errorf("%d Thread(s) still registered after run returned; the body finished", n)
	}
}

// TestCloseEndsAThreadParkedInsideAHandoff is the amendment's second parked shape: a created Thread
// suspended PARTWAY, mid-guest, inside a canon call, with nothing to resume it.
//
// `suspend-parked`'s task resolves and the call returns with the body still registered — legal, and
// wasmtime 49.0.2 returns 2 on 30 of 30 runs. In this engine the body is a goroutine inside `giveBack`'s
// `select`, inside `CanonCaller.Blocking`, with guest frames beneath it. Neither `interp.ErrClosed` (needs
// a re-entry) nor `interp.ErrTerminated` (needs a safepoint in guest code) reaches it, so `Close`'s wake
// must: `killParked` closes its `kill`, `giveBack` returns errThreadKilled, the guest unwinds, and the
// goroutine exits.
//
// **Neuter, run:** making the wake unable to reach a Thread inside `giveBack` — its `select` waiting on a
// nil `kill`, which is the second slice's reach — leaves the goroutine parked and `Close` hung on it, and
// this fails on the bound.
func TestCloseEndsAThreadParkedInsideAHandoff(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	want := readCmthreadsResult(t, "suspend-parked.reading")
	wasm, err := os.ReadFile("testdata/cmthreads/suspend-parked.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if got := callRunBounded(t, in); got != want {
		t.Fatalf("run returned %#b; suspend-parked.reading records %#b", got, want)
	}
	// The call has returned and the body is parked mid-guest. Asserted, so a wake with nothing to wake
	// cannot pass: index 2 (the implicit Thread was 1 and has left), suspended, and its goroutine running.
	if n := in.w.threads.live(); n != 1 {
		t.Fatalf("%d Thread(s) registered after the call; want exactly the 1 parked body", n)
	}
	parked := in.w.threads.get(2)
	if parked == nil || parked.implicit {
		t.Fatalf("Thread 2 is not the created body this witness is about: %+v", parked)
	}
	in.w.threads.mu.Lock()
	state := parked.state
	in.w.threads.mu.Unlock()
	if state != threadSuspended {
		t.Fatalf("the body is in state %d, not suspended", state)
	}
	select {
	case <-parked.done:
		t.Fatal("the body's goroutine had already exited; it should be parked inside its handoff")
	default:
	}

	// **`Close` itself is bounded**, not only the goroutine after it: a body parked inside `Blocking` is a
	// caller the core instance's `Close` waits for, so an unreached Thread hangs `Close` rather than
	// outliving it. Measured under the neuter, which hung this test until the go test timeout before
	// the bound moved here.
	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	deadline := time.After(5 * time.Second)
	for _, ch := range []<-chan struct{}{closed, parked.done} {
		select {
		case <-ch:
		case <-deadline:
			t.Fatal("within 5s of Close, Close had not returned or the Thread parked inside its handoff " +
				"had not exited. It is in a Go select under a canon call, which neither interp.ErrClosed " +
				"nor interp.ErrTerminated reaches, so Close must wake it explicitly")
		}
	}
	if n := in.w.threads.live(); n != 0 {
		t.Errorf("%d Thread(s) still registered after Close", n)
	}
}

// TestAThreadParkedInsideAHandoffIsAtASafepoint is the amendment's SP-2 accounting, as a witness rather
// than an argument: with a created Thread parked inside `giveBack`, every core instance of the component
// stops within a short deadline.
//
// SP-2 counts a thread blocked in a host call as at a safepoint. The parked body IS a caller — it entered
// its instance through `invokeIndex` — so `Stop`'s predicate is satisfied only if the park is also counted
// blocked. A 🧵 built-in is dispatched by `interp.Instance.callAdapter`, which does not call
// `enterBlocked`; the count comes from `giveBack` parking inside `CanonCaller.Blocking`. Confirmed by
// symbol in the amendment, and held here.
//
// **Neuter, run:** calling `giveBack`'s inner function directly instead of through `c.Blocking` makes the
// body's instance report a `Stop` deadline expiry — a caller that is neither running to a safepoint nor
// counted blocked.
func TestAThreadParkedInsideAHandoffIsAtASafepoint(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	wasm, err := os.ReadFile("testdata/cmthreads/suspend-parked.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	callRunBounded(t, in)
	if n := in.w.threads.live(); n != 1 {
		t.Fatalf("%d Thread(s) registered; want the 1 body parked inside its handoff", n)
	}
	if len(in.w.toClose) == 0 {
		t.Fatal("the walker holds no core instances, so this witness stops nothing")
	}
	for i, core := range in.w.toClose {
		if serr := core.Stop(2 * time.Second); serr != nil {
			t.Errorf("core instance %d did not stop with a Thread parked inside a handoff: %v — the park "+
				"is a caller the stop predicate does not count", i, serr)
			continue
		}
		core.Resume()
	}
}

// BenchmarkSwitchWasm is the handoff's cost, measured on the fixture that exercises all five forms — the
// figure ADR 0100's amendment asks to be quoted, and not a benchmark suite.
//
// One op is one `run`: five `thread.new-indirect`s (five goroutines), five handoffs from the implicit
// Thread to the scheduler, and per form the scheduler's resume of the body, the body's exit report, and
// the resume of the implicit Thread — twenty channel handoffs between goroutines, plus the guest code.
func BenchmarkSwitchWasm(b *testing.B) {
	b.Setenv(cmThreadsGateEnv, "1")
	b.Setenv(asyncGateEnv, "1")
	wasm, err := os.ReadFile("testdata/cmthreads/switch.wasm")
	if err != nil {
		b.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		b.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := in.CallValues("run", canon.U32(0)); err != nil {
			b.Fatalf("run: %v", err)
		}
	}
}
