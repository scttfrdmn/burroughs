// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// The §§2–5 litmus battery's first landed case, and the harness the rest of it will use.
//
// The battery is **#10**, its oracle is [ADR 0055] — contract §§2–5 read clause by clause with the
// allowed-outcome sets pre-registered in [the tables] before any of this existed — and the vehicle
// below is [ADR 0062]. Two things belong at the top of any file in this battery, both from 0055:
//
//   - **A green here means agreement with an oracle this project wrote**, which is weaker than every
//     other green in this tree. There is no upstream suite for §§2–5.
//   - **A verdict is a falsifier, never a certificate.** The verdict form is *"not observed in N runs
//     on both architectures"*; a clean run bounds nothing about the interleavings that did not occur.
//
// # Two agents, without `Spawn`
//
// Every case in the battery needs two agents, and the tables said `Instance.Spawn` was the blocker —
// *"the tree has exactly one agent"*. That premise is false (grave #606: it described the spawn
// primitive's absence and was written as though it described the tree), and 0062 records the experiment
// that falsified it: a **shared memory is imported**, so two instances can hold one `*memory` (the reason
// [ADR 0052] put §4's boundary word at package scope rather than on `Instance`), and each instance
// carries its own `thread` and its own `world`. Two goroutines driving `Invoke` on two instances that
// share one memory are therefore two agents over one address space, on two OS-visible Ps, sharing one
// futex queue — which is what a litmus case needs and all it needs.
//
// **What this vehicle must not be used for is T-1.** T-1's clause is about the spawn primitive itself
// — *"a wasm thread backed 1:1 by an OS thread"* — and its case counts N agents parked at once to rule
// out a pool or an M:N mapping. Run on this vehicle it would *pass*, because Go parks N goroutines
// happily, while the clause it claims to discharge stayed unsatisfied. A vehicle that satisfies a case
// by supplying exactly the thing the clause forbids is worse than a blocked row, so T-1 stays blocked
// on #554 and says so.
//
// [ADR 0052]: ../../docs/decisions/0052-the-4-boundary-edge-is-one-package-level-sequentially-consistent-counter-because-a-shared-memory-spans-instances.md
// [ADR 0055]: ../../docs/decisions/0055-the-2-5-litmus-batterys-oracle-is-the-contract-read-clause-by-clause-with-its-outcome-sets-pre-registered-because-no-external-engine-can-arbitrate-a-clause-written-against-one.md
// [ADR 0062]: ../../docs/decisions/0062-the-litmus-batterys-two-agents-are-two-instances-sharing-an-imported-memory-because-a-shared-memory-spans-instances-and-spawn-does-not-gate-that.md
// [the tables]: ../../docs/litmus-battery-preregistration.md
const litmusWakerSrc = `(module
  (memory (export "mem") 1 1 shared)
  (func (export "notify") (param $addr i32) (param $count i32) (result i32)
    (memory.atomic.notify (local.get $addr) (local.get $count))))`

// litmusWaiterSrc's exported function is **the wait and nothing else**, which is the case's
// measurement boundary rather than a minimalism preference — see
// `TestAWakeArrivesAtFutexLatencyAndNotEventLoopLatency` on its two endpoints.
const litmusWaiterSrc = `(module
  (import "m" "mem" (memory 1 1 shared))
  (func (export "wait") (param $addr i32) (param $timeout i64) (result i32)
    (memory.atomic.wait32 (local.get $addr) (i32.const 0) (local.get $timeout))))`

// litmusAgents builds the battery's two agents and hands back the memory they share.
//
// Through the text front end and the decoder for `futexModule`'s reason (grave #579): a hand-built
// `memory` skips `newMemory`'s base-alignment check and would let a case assert the engine against the
// harness's own idea of a memory. The `Threads` feature is set because the gate is what admits
// `shared` and the 0xfe region at all.
//
// The returned `*memory` is asserted to be the *same object* both instances hold, because that
// identity is the whole vehicle: if the linker ever copied a memory on import, every case in this
// battery would run two agents over two address spaces and pass by never racing.
func litmusAgents(t *testing.T) (waker, waiter *Instance, mem *memory) {
	t.Helper()

	build := func(src string, imp Imports) *Instance {
		t.Helper()
		img, err := text.EncodeModule([]byte(src))
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		m, derr := (&binary.Decoder{Features: binary.Features{Threads: true}}).DecodeModule(img)
		if derr != nil {
			t.Fatalf("decode: %v", derr)
		}
		in, trap, lerr := InstantiateLinked(m, imp)
		if lerr != nil {
			t.Fatalf("link: %v", lerr)
		}
		if trap != nil {
			t.Fatalf("instantiate: %v", trap)
		}
		if err := in.Deferred(); err != nil {
			t.Fatalf("instantiate fell short: %v", err)
		}
		return in
	}

	waker = build(litmusWakerSrc, nil)
	waiter = build(litmusWaiterSrc, exportsOf(waker))
	if waker.mems[0] != waiter.mems[0] {
		t.Fatalf("the two agents hold different memories (%p, %p) — the import copied rather than "+
			"shared, so nothing below would be a race at all",
			waker.mems[0], waiter.mems[0])
	}
	return waker, waiter, waker.mems[0]
}

// litmusArmed spins until a waiter is queued at `ea`, which is how a case knows its second agent has
// **reached** its wait rather than merely been asked to.
//
// A spin and not `awaitQueued`'s millisecond sleep: at K or R rounds a sleep-per-round makes the
// round count a wall-clock budget rather than a statistical one, and this battery's pre-registered
// counts run to 100_000. `runtime.Gosched` rather than a bare loop so the waiter's goroutine gets a P
// on a single-P machine.
//
// It reads `m.waiters` under `waitMu` — the mechanism, not a guest-visible signal — for the reason
// `awaitQueued` documents: a `notify` of count 0 returns 0 whether the queue is empty or full, so
// there is no guest-visible channel that reports "queued". Used for **sequencing only**, never as an
// assertion, and false here is a premise that did not hold.
//
// **It cannot supply the edge a §4 case is testing.** The arming observation happens strictly before
// the waking agent's stores, so the only ordering it establishes runs from the waiter's enqueue to
// those stores — the opposite direction from the one under test.
func litmusArmed(m *memory, ea uint64, deadline time.Time) bool {
	for {
		m.waitMu.Lock()
		queued := len(m.waiters[ea])
		m.waitMu.Unlock()
		if queued > 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		runtime.Gosched()
	}
}

// TestAWakeArrivesAtFutexLatencyAndNotEventLoopLatency is the battery's case
// `t3-wake-latency-is-microsecond-scale`, discharging contract §2 T-3:
//
//	`memory.atomic.wait` / `notify` MUST be futex-backed with OS-native wake latency, not
//	event-loop-turn latency.
//
// The pre-registered set, quoted so the reading can be checked rather than trusted: **allowed** is a
// median wake latency below 1ms over K = 1000 wake events, measured host-side from the notifying
// agent's return to the woken agent's first executed instruction; **forbidden** is a median at or
// above 1ms; the **witness** is one waiter, one waker, K rounds with the waiter re-arming between
// them, and the median is reported on every run whether or not it fails, *"because a timing case that
// prints only on failure cannot show a drift toward its own ceiling"*; the **floor** is that all K
// rounds complete; the **arbiter** is neither architecture, the ceiling being a mechanism
// discriminator rather than a performance target — an event-loop turn is milliseconds and a futex wake
// is microseconds, so 1ms separates the two mechanisms by an order of magnitude in either direction.
//
// # The two endpoints, and which way their error points
//
// *"The woken agent's first executed instruction"* is not host-observable, so this case measures the
// nearest thing that is: the return of an `Invoke` whose function body is the wait instruction alone.
// The proxy therefore includes the wait's result push and `invokeIndex`'s teardown, and **it can only
// over-report** — a real wake is at most this fast. An over-reporting instrument cannot manufacture a
// pass against a ceiling, which is the direction an unavoidable proxy has to point.
//
// The start endpoint is the notify's `Invoke` returning, per the pre-registration, and that admits a
// **negative** latency: the wake is delivered inside `notify`, so the woken agent can be running again
// before the notifying agent's own call teardown finishes. A negative median is not an instrument
// fault, it is the mechanism claim in its strongest form, and it is reported signed rather than
// clamped — clamping would hide the one number that most decisively separates a futex from a turn.
//
// # Watched die
//
// A case whose forbidden outcome nothing in the tree can produce is a green that means nothing, so
// this one was run against an injected engine before it was trusted. `notify`'s buffered send replaced
// by a flag the waiter polls on a 1ms `time.Sleep` — the event-loop-turn mechanism the clause names —
// moves the median from the figure this test logs to the far side of the ceiling and the case fails.
// The reverse injection matters too and is why the endpoint is `notify`'s return rather than a stamp
// taken before it: a mechanism that woke the agent *before* the notify was issued would be measured
// negative and pass, correctly, because that is not the failure T-3 describes.
//
// # The floor's zero is structural
//
// The pre-registration has a round whose wait returns not-equal re-run rather than discarded, with the
// re-run count reported. Each round here waits on a **fresh** address, and a fresh address holds zero,
// which is the value the wait expects — so the not-equal arm is unreachable and the re-run count is
// zero by construction rather than by measurement. It is reported anyway, said to be structural, and
// the arm it stands in for is covered by `TestAWaitWhoseCellChangedDoesNotQueue`.
func TestAWakeArrivesAtFutexLatencyAndNotEventLoopLatency(t *testing.T) {
	const (
		rounds  = 1000             // K, pre-registered
		ceiling = time.Millisecond // the forbidden median, pre-registered
		timeout = int64(5 * time.Second)
	)

	waker, waiter, mem := litmusAgents(t)

	// stamped carries the woken agent's resume time off its own goroutine. Buffered to one and read
	// every round, so the waiter never blocks on a full channel while the round loop is arming.
	type resume struct {
		at  time.Time
		res outcome
	}
	stamped := make(chan resume, 1)
	addrs := make(chan int32)
	go func() {
		for addr := range addrs {
			res := callOffGoroutine(waiter, "wait", I32(addr), I64(timeout))
			stamped <- resume{at: time.Now(), res: res}
		}
	}()
	defer close(addrs)

	latencies := make([]time.Duration, 0, rounds)
	reruns := 0
	for i := range rounds {
		// A fresh naturally-aligned word per round, which is what makes the not-equal arm
		// unreachable. 1000 rounds at a stride of 8 stay inside the one page this module declares.
		addr := int32(8 * (i + 1))
		addrs <- addr
		if !litmusArmed(mem, uint64(addr), time.Now().Add(10*time.Second)) {
			t.Fatalf("round %d: the waiting agent never reached the queue at address %d within 10s "+
				"— this is the case's premise and not its verdict: no latency was measured", i, addr)
		}

		woke := call(t, waker, "notify", I32(addr), I32(1))
		notified := time.Now()
		if woke != 1 {
			t.Fatalf("round %d: memory.atomic.notify woke %d waiters at address %d, want 1 — the "+
				"agent was queued when this round armed, so the wake was counted against an empty "+
				"queue", i, woke, addr)
		}

		got := <-stamped
		if r := got.res.get(t); r != waitWoken {
			if r == waitNotEqual {
				reruns++
				continue
			}
			t.Fatalf("round %d: memory.atomic.wait32 returned %d, want %d — a notify claimed this "+
				"agent and it was told something else happened", i, r, waitWoken)
		}
		latencies = append(latencies, got.at.Sub(notified))
	}

	if len(latencies) != rounds {
		t.Fatalf("%d of %d rounds produced a wake, and the floor is all of them", len(latencies), rounds)
	}
	slices.Sort(latencies)
	median := latencies[len(latencies)/2]

	// Reported on every run, pass or fail, and reported before the verdict: the pre-registration asks
	// for the median unconditionally so a drift toward the ceiling is visible while it is still a
	// drift. The extremes come along because a median alone cannot show a tail, and a tail is what an
	// occasional turn-scale wake would look like.
	t.Logf("t3-wake-latency-is-microsecond-scale: median %v over %d wake events (min %v, max %v), "+
		"ceiling %v; %d re-run(s), structurally zero — a fresh address per round cannot be not-equal",
		median, len(latencies), latencies[0], latencies[len(latencies)-1], ceiling, reruns)

	if median >= ceiling {
		t.Errorf("median wake latency %v is at or above the %v ceiling: T-3 asks for OS-native wake "+
			"latency and this is event-loop-turn scale. The ceiling is a mechanism discriminator, so "+
			"the reading is that the wake is no longer futex-backed — not that this machine is slow",
			median, ceiling)
	}
}
