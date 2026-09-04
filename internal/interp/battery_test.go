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

// litmusSiblingWakerSrc and litmusSiblingWaiterSrc are `b-mm-2-sibling-field-after-wake`'s agents, and
// the shape of the waker is the case's whole argument — see
// TestAResumedAgentSeesASiblingFieldWrittenBeforeTheNotify for why the sibling write is a
// `memory.fill` and why nothing here stores to the futex word.
const litmusSiblingWakerSrc = `(module
  (memory (export "mem") 1 1 shared)
  (func (export "publish") (param $sib i32) (param $futex i32) (result i32)
    (memory.fill (local.get $sib) (i32.const 1) (i32.const 4))
    (memory.atomic.notify (local.get $futex) (i32.const 1))))`

// The waiter packs two observations into one i32 because `callOffGoroutine` carries exactly one — it
// errors on any other arity (`futex_test.go:callOffGoroutine`). The wait's result goes in bits 4 and
// up, and bit 0 is whether the sibling read non-zero. Not the sibling's *value*: a 4-byte extent
// filled with byte `1` loads as `0x01010101`, which overflowed the first draft's one-byte field and
// is the reason the low bit is a predicate rather than a payload.
const litmusSiblingWaiterSrc = `(module
  (import "m" "mem" (memory 1 1 shared))
  (func (export "await") (param $sib i32) (param $futex i32) (param $timeout i64) (result i32)
    (i32.or
      (i32.shl (memory.atomic.wait32 (local.get $futex) (i32.const 0) (local.get $timeout))
               (i32.const 4))
      (i32.ne (i32.load (local.get $sib)) (i32.const 0)))))`

// litmusTwoMemWakerSrc and litmusTwoMemWaiterSrc are
// `b-mm-2-the-sibling-lives-in-a-second-shared-memory`'s agents: the pair above with the sibling extent
// moved into a **second** shared memory, which is the whole of what that case asks (#631).
//
// **The futex word stays in memory 0 and only the sibling moves**, which keeps every explicit memory
// index on the bulk and plain-load forms — `(memory.fill 1 …)`, `(i32.load 1 …)` — and off the atomic
// ones. Not a style choice: an atomic instruction's memory index rides its memarg's 0x40 bit, so putting
// the futex in memory 1 would make the case's front end depend on a second encoding question that has
// nothing to do with B-MM-2. The clause is about a write in one memory being visible after a wake
// arranged through another, and this arrangement is the one that tests it with the fewest unrelated
// premises.
//
// Both memories are exported and both are imported by name, so the two index spaces line up: imported
// memories come first in the importer's index space and in import order, so `mem` is 0 and `sib` is 1 in
// both agents. `litmusAgentsUnder` asserts that rather than trusting it, index by index.
const litmusTwoMemWakerSrc = `(module
  (memory (export "mem") 1 1 shared)
  (memory (export "sib") 1 1 shared)
  (func (export "publish") (param $sib i32) (param $futex i32) (result i32)
    (memory.fill 1 (local.get $sib) (i32.const 1) (i32.const 4))
    (memory.atomic.notify (local.get $futex) (i32.const 1))))`

// The packing is litmusSiblingWaiterSrc's and for the same reason — one i32 out, the wait result in bits
// 4 and up, bit 0 the sibling predicate — with the load's memory index the only difference.
const litmusTwoMemWaiterSrc = `(module
  (import "m" "mem" (memory 1 1 shared))
  (import "m" "sib" (memory 1 1 shared))
  (func (export "await") (param $sib i32) (param $futex i32) (param $timeout i64) (result i32)
    (i32.or
      (i32.shl (memory.atomic.wait32 (local.get $futex) (i32.const 0) (local.get $timeout))
               (i32.const 4))
      (i32.ne (i32.load 1 (local.get $sib)) (i32.const 0)))))`

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
	return litmusAgentsFrom(t, litmusWakerSrc, litmusWaiterSrc)
}

// litmusAgentsFrom is litmusAgents over a named pair of module sources, which is what a second case
// with a second witness needs. It is `litmusAgentsUnder` at the battery's default feature set and at
// one memory, which is every case on the vehicle except `b-mm-2-the-sibling-lives-in-a-second-shared-memory`.
func litmusAgentsFrom(t *testing.T, wakerSrc, waiterSrc string) (waker, waiter *Instance, mem *memory) {
	t.Helper()
	waker, waiter, mems := litmusAgentsUnder(t, binary.Features{Threads: true}, wakerSrc, waiterSrc)
	return waker, waiter, mems[0]
}

// litmusAgentsUnder builds the two agents under a named feature set and hands back **every** memory
// they share, in index order.
//
// **The features are a parameter because they were a literal, and a literal is what grave #630 was.**
// The row for the two-memory case read `blocked — the multiple-memories gate` for as long as this
// function hard-coded `Features{Threads: true}`: a gate's *default* governs what ships on, and a test's
// `Features` literal governs what the harness can build. Only the second can block a case, and it is an
// edit rather than a decision. Widening it here — rather than at the one call site that needs
// `MultiMemory` — would re-create the confusion in the other direction, by making every case in the
// battery run under a gate its clause never mentioned.
//
// # Three assertions about the memories, and the second and third are #631's
//
// The vehicle *is* the shared object: if the linker ever copied a memory on import, every case in this
// battery would run two agents over two address spaces and pass by never racing. So:
//
//   - **Every index is the same object in both instances**, not just index 0. A case whose sibling
//     extent lives in memory 1 while only memory 0 is shared runs its two accesses over two address
//     spaces — the exact failure the index-0 assertion was written against, one index over, and
//     invisible to it.
//   - **The counts agree.** Without this the loop below is scoped to the shorter slice and a waiter
//     holding one memory against a waker's two passes on the prefix.
//   - **The memories are pairwise distinct objects.** Vacuous at one memory, and at two its subject is
//     the **allocator** rather than the resolver — which is worth stating, because the resolver's version
//     of this defect (both import names answered with one memory) is already caught index by index above,
//     and a control whose stated subject is the covered half is a control nobody can watch die. An
//     `allocate` that handed one `*memory` to two declared memories satisfies both checks above — the
//     agents agree at every index — and collapses this case into the one-memory case it exists to be
//     different from, passing with its subject deleted. Watched die by exactly that injection, in
//     `Instance.allocate`.
func litmusAgentsUnder(t *testing.T, feats binary.Features, wakerSrc, waiterSrc string) (waker, waiter *Instance, mems []*memory) {
	t.Helper()

	build := func(src string, imp Imports) *Instance {
		t.Helper()
		img, err := text.EncodeModule([]byte(src))
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		m, derr := (&binary.Decoder{Features: feats}).DecodeModule(img)
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

	waker = build(wakerSrc, nil)
	waiter = build(waiterSrc, exportsOf(waker))

	if len(waker.mems) != len(waiter.mems) {
		t.Fatalf("the waker holds %d memories and the waiter %d — the import side did not bind what the "+
			"export side declared, and an index-by-index comparison would run over the shorter of the two",
			len(waker.mems), len(waiter.mems))
	}
	for i := range waker.mems {
		if waker.mems[i] != waiter.mems[i] {
			t.Fatalf("memory %d is a different object in the two agents (%p, %p) — the import copied "+
				"rather than shared, so accesses to it would not be a race at all",
				i, waker.mems[i], waiter.mems[i])
		}
	}
	for i := range waker.mems {
		for j := i + 1; j < len(waker.mems); j++ {
			if waker.mems[i] == waker.mems[j] {
				t.Fatalf("memories %d and %d are the same object (%p) — a case placing its two accesses "+
					"in different memories would be placing them in one, and would pass by being a "+
					"different case", i, j, waker.mems[i])
			}
		}
	}
	return waker, waiter, waker.mems
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

// TestAResumedAgentSeesASiblingFieldWrittenBeforeTheNotify is the battery's case
// `b-mm-2-sibling-field-after-wake`, discharging contract §4 B-MM-2:
//
//	A wake MUST synchronize all writes the notifying agent made before the notify, not only the
//	write to the futex word.
//
// This is the case §4 names by hand — B-MM-5 requires the conformance suite to contain it — and D20
// is its provenance: on the browser host `Atomics.notify` establishes happens-before for the notified
// word only, so a sibling field's store can lag the woken agent's resume even when the read happens
// under a freshly acquired lock.
//
// The pre-registered set, quoted so the reading can be checked rather than trusted: **allowed** is *no
// race report naming the sibling extent*; **forbidden** is *a report whose two halves are A's sibling
// write and B's post-wake sibling read, at an address inside the sibling extent*; the **witness** is A
// filling a naturally-aligned 4-byte sibling extent and then notifying, with a fresh 16-byte-aligned
// pair per round and `R = 1000`; the **floor** is *every round reports `r == 0`, and every round reads
// the published sibling value*; the **arbiter** is *both architectures, and the arbiter is Go's memory
// model via `-race`*.
//
// # The oracle is the race detector, and that is an amendment
//
// The registered witness used to have B compare the sibling's *value* and forbid the stale one. That
// case was stillborn (#603): after [ADR 0054] a guest has no plain aligned typed store, so A's release
// is sequentially consistent, and the interleaving where B's read lands between the notify and the
// write measured 6 × 10⁻⁷ per round at *zero* distance — against microseconds of interpreter dispatch
// per round, which no `R` closes. The detector's happens-before check answers **per round and
// deterministically**, so `R` fell from 100 000 to 1000 and is now about schedule diversity rather than
// about hitting a window.
//
// **A detector needs one non-atomic side to have anything to say**, which is why the sibling write is a
// `memory.fill`: after 0054 the bulk family is the only guest-reachable plain write left (#627). A race
// between a plain write and a sequentially consistent atomic read is still a race, so B's `i32.load` —
// atomic since 0054 — does not remove it.
//
// This mechanism was already one clause up in the pre-registration:
// `TestAResumedGuestSeesAHostWriteFromTheStop` writes plainly on purpose, for the same stated reason,
// and reads its verdict from `-race`. Recorded because re-deriving it rather than reading the
// neighbouring clause cost a two-million-round run.
//
// # The verdict lives in CI's `race` step, not in `make check`
//
// `make check` does not pass `-race`, so **a green from it says nothing about this case's subject** —
// what it exercises then is the floor alone. `make race` passes it, and CI reaches it from a step named
// `race` inside the `build` job (`.github/workflows/ci.yml`), which is a two-architecture matrix, so
// the verdict is two readings. It is a *step* and not a job: a reader looking for `race` in a run's job
// list finds fuzz-smoke, lint, conformance, citations, build twice and vuln, and cannot tell a skipped
// verdict from a misnamed one. Stated for `TestANumericGlobalIsNotWrittenAndReadWithoutSynchronisation`'s
// reason — **a verdict channel named wrongly is worse than one left unnamed**, because the wrong name
// reads as though somebody had checked it.
//
// The detector's report *is* the failure here: `go test -race` fails a test during which a race is
// reported, so there is no verdict for this function to compute. What it can do — and does — is refuse
// to be vacuous.
//
// # The floor is a vacuity guard, and the forbidden outcome is located rather than counted
//
// A detector reports nothing when the two accesses never overlap, and it reports nothing when they
// never landed on the same address either. So every round asserts the wait returned woken *and* that
// the sibling read non-zero: the published value is what establishes that A's write and B's read were
// about one extent, which is the premise the detector's silence is only informative under.
//
// Fresh addresses per round are load-bearing rather than tidy. With one reused pair, round *i*'s fill
// races round *i−1*'s read and the detector reports the **harness's** race — a red that says nothing
// about the engine.
//
// # Watched die
//
// Run against an injected engine before it was trusted: `notify`'s channel send and `wait`'s receive
// replaced by a plain unsynchronised `bool`, which is exactly the missing-edge defect B-MM-2 forbids.
// The detector reported the pair — the write in `internal/interp/bulk.go:execMemoryFill` against the
// woken agent's load in `internal/interp/memory.go:memAccess`, one address, two goroutines. It also
// produced a **second** report, on the injected flag itself, which is why the pre-registration forbids
// a *located* report rather than any report at all: *a report with no located pair is the instrument's
// noise, not the engine's finding*.
//
// # The carrier survives on a ruling, and that is a debt this case owes rather than a property it has
//
// The plain side is `memory.fill` only because the bulk family is plain at every alignment, which was
// [#627] — the open question of whether those paths join 0054's atomic regime. **It is closed and they do
// not**: [ADR 0064] keeps the region plain, so this carrier is not going away in the next slice.
//
// **That is a use for the gap and not an argument for it**, which is Scott's phrasing on the ruling and
// the reason this paragraph is now pointed the other way round: *"B-MM-2's carrier surviving is a use for
// the gap, not an argument for it — record that if the gap closes the case needs a new plain side, so the
// carrier never becomes a reason to keep it open."* So the debt is stated here as a standing obligation on
// **whatever slice ever closes the region**, not as a reason the region should stay open:
//
//   - **If the plain region closes, that slice owes this case a new plain side in the same slice.** Two
//     atomics leave the detector no report to make and the floor above still holds, so the case would keep
//     passing with nothing to detect.
//   - **The only replacement in hand is an unaligned typed store**, since 0054's Consequences record that
//     the unaligned path has no atomic mechanism at all. That re-points this oracle at *that* gap rather
//     than rescuing it, and couples the case to that gap staying open.
//   - **A complete repair of every plain path leaves this case with no `-race` oracle in the tree.** There
//     is no third arbiter: the clause would have to be re-registered against an ordering assertion a
//     passing run cannot distinguish from a lucky one, or its `Status:` goes back to blocked.
//
// Two places now carry the mirror of this so a diff cannot walk past it:
// `internal/interp/bulk.go:execMemoryFill`, and — machine-checked rather than written down —
// `TestNoGuestMemoryAccessSiteJoinsWithoutAClassification`, which fails if `execMemoryFill` starts calling
// a synchronisation helper while ADR 0064's enumeration still classifies it `plain`.
//
// [ADR 0054]: ../../docs/decisions/0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md
// [ADR 0064]: ../../docs/decisions/0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md
// [#627]: https://github.com/scttfrdmn/burroughs/issues/627
func TestAResumedAgentSeesASiblingFieldWrittenBeforeTheNotify(t *testing.T) {
	const (
		rounds  = 1000 // R, pre-registered
		timeout = int64(5 * time.Second)
	)

	waker, waiter, mem := litmusAgentsFrom(t, litmusSiblingWakerSrc, litmusSiblingWaiterSrc)

	type pair struct{ sib, futex int32 }
	pairs := make(chan pair)
	stamped := make(chan outcome, 1)
	go func() {
		for p := range pairs {
			stamped <- callOffGoroutine(waiter, "await", I32(p.sib), I32(p.futex), I64(timeout))
		}
	}()
	defer close(pairs)

	for i := range rounds {
		// A fresh 16-byte-aligned futex word and a fresh 4-byte-aligned sibling extent 8 bytes above
		// it, per round. 1000 rounds at a stride of 16 stay inside the one page this module declares.
		p := pair{futex: int32(16 * (i + 1))}
		p.sib = p.futex + 8
		pairs <- p

		if !litmusArmed(mem, uint64(p.futex), time.Now().Add(10*time.Second)) {
			t.Fatalf("round %d: the waiting agent never reached the queue at address %d within 10s — "+
				"this is the case's premise and not its verdict: no pair was formed", i, p.futex)
		}

		// A never stores to the futex word: the fill publishes the sibling and the notify is the
		// release. A fresh word already holds 0, which is the value B waits for.
		if woke := call(t, waker, "publish", I32(p.sib), I32(p.futex)); woke != 1 {
			t.Fatalf("round %d: memory.atomic.notify woke %d waiters at address %d, want 1 — the agent "+
				"was queued when this round armed, so the wake was counted against an empty queue",
				i, woke, p.futex)
		}

		got := (<-stamped).get(t)
		res, published := got>>4, got&1
		if res != waitWoken {
			t.Fatalf("round %d: memory.atomic.wait32 returned %d, want %d (woken) — a notify claimed "+
				"this agent and it was told something else happened. The floor is every round woken, "+
				"and under an infinite-in-practice timeout a non-wake is an instrument fault rather "+
				"than a verdict about B-MM-2", i, res, waitWoken)
		}
		if published != 1 {
			t.Fatalf("round %d: the woken agent read 0 from the sibling extent at address %d, which A "+
				"filled with 1 before notifying. This is the floor and not the verdict: it says A's "+
				"write and B's read did not land on one extent, so the race detector's silence about "+
				"the pair would be silence about nothing", i, p.sib)
		}
	}

	// Reported on every run, because the verdict channel is invisible from here: this line says what was
	// exercised, and a reader who ran `make check` needs to know it was the floor alone.
	t.Logf("b-mm-2-sibling-field-after-wake: %d rounds, every round woken and every round reading the "+
		"published sibling value. The verdict is the race detector's silence about (A's fill, B's load) "+
		"and is only taken under `-race` — `make check` does not pass it; CI's `race` step inside the "+
		"two-architecture `build` job does", rounds)
}

// TestAResumedAgentSeesASiblingFieldInASecondSharedMemory is the battery's case
// `b-mm-2-the-sibling-lives-in-a-second-shared-memory` (#631), the second of the two B-MM-2 registered:
//
//	B-MM-2 · A wake makes **all** of A's prior writes visible to B, not only the futex word's.
//
// Its registered witness is *"identical to the named case as amended"* over a module with two shared
// memories, the sibling extent in the second. Everything the neighbour above documents is inherited and
// is not restated here: the `-race` oracle and why the plain side is a `memory.fill` (#627), the verdict
// channel's location in CI's `race` step rather than in `make check`, the located-rather-than-counted
// forbidden outcome, the two-half floor, `R = 1000`, fresh addresses per round. Read that function first;
// this one is about the word *all*.
//
// # What a second memory adds, and why one clause needs two cases
//
// An engine can establish a per-memory edge. Nothing in the shape of the passing named case rules that
// out: an implementation whose wake publishes the notified memory's writes and no others satisfies
// `b-mm-2-sibling-field-after-wake` on every round and still loses a write to a second shared memory —
// which is the clause's word `all` failing on exactly the configuration the clause does not mention.
// Two cases for one clause is therefore a coverage claim rather than duplication, and the pre-registration
// carries both rows for that reason.
//
// This engine's edge is not per-memory — `notify`'s channel send orders everything before it — so the
// expectation is a pass. **A case whose result is foreseen is still worth landing when the alternative
// is an unasserted premise**: what is foreseen is the reading of the current mechanism, and the case is
// what makes a future mechanism that narrows the edge per memory fail rather than pass quietly.
//
// # The two addresses coincide, on purpose
//
// The futex word and the sibling extent sit at the *same offset*, one in each memory, which is a
// statement of what a second memory is: the same number naming two locations. It is not a control. A
// resolver that answered both import names with one memory would put A's fill directly on B's futex word
// and this case would still pass — the waiter has already matched 0 and queued by then, so the fill's
// `1` arrives too late to be seen as a mismatch and is read back as the published value. That
// degeneration is caught in `litmusAgentsUnder`, by the pairwise-distinctness assertion, and it is caught
// there precisely because no arithmetic here can catch it.
//
// # The front end, and the two questions #631 said were open
//
// Both were answered by running it rather than by reading the linker. The features are a parameter now
// (`litmusAgentsUnder`), so this case decodes under `MultiMemory` while every other case on the vehicle
// keeps the feature set its clause implies. And the import side does bind two shared memories in index
// order — asserted index by index, not inferred from the module text.
//
// # Watched die, three times, and each injection fired one assertion
//
// Against a committed baseline, reverted after each:
//
//   - **The verdict channel.** `notify`'s channel send and `wait`'s receive replaced by a plain
//     unsynchronised bool, the wake preserved by a `Gosched` spin — the missing-edge defect B-MM-2
//     forbids. The detector reported the located pair, `execMemoryFill`'s write against `memAccess`'s
//     `sync/atomic.LoadUint32`, one address, goroutines 7 and 8, plus a second report on the injected
//     flag. That second report is why the registration forbids a *located* pair rather than any report.
//   - **The per-index identity assertion.** The waiter's second import re-pointed at `"mem"`, so both
//     agents hold two memories, agree at index 0, and differ at index 1. It fires at index 1 and the
//     distinctness assertion stays quiet.
//   - **The distinctness assertion.** `Instance.allocate` made to hand `mems[0]` to every declared
//     memory. Both agents then agree at every index and the counts match, so this is the only assertion
//     that fires — which is the sense in which its subject is the allocator and not the resolver.
func TestAResumedAgentSeesASiblingFieldInASecondSharedMemory(t *testing.T) {
	const (
		rounds  = 1000 // R, pre-registered — the named case's, inherited
		timeout = int64(5 * time.Second)
	)

	feats := binary.Features{Threads: true, MultiMemory: true}
	waker, waiter, mems := litmusAgentsUnder(t, feats, litmusTwoMemWakerSrc, litmusTwoMemWaiterSrc)
	if len(mems) != 2 {
		t.Fatalf("the vehicle bound %d memories, want 2 — this case's subject is the second one", len(mems))
	}

	type round struct{ sib, futex int32 }
	rs := make(chan round)
	stamped := make(chan outcome, 1)
	go func() {
		for r := range rs {
			stamped <- callOffGoroutine(waiter, "await", I32(r.sib), I32(r.futex), I64(timeout))
		}
	}()
	defer close(rs)

	for i := range rounds {
		// One fresh 16-byte-aligned offset per round, used in both memories. 1000 rounds at a stride of
		// 16 stay inside the one page each memory declares.
		r := round{futex: int32(16 * (i + 1))}
		r.sib = r.futex
		rs <- r

		// The queue is memory 0's: the wait is there, and `litmusArmed` reads the mechanism of the
		// memory the wait was issued against. Handing it `mems[1]` would spin to the deadline on an
		// empty queue and report the premise as failed.
		if !litmusArmed(mems[0], uint64(r.futex), time.Now().Add(10*time.Second)) {
			t.Fatalf("round %d: the waiting agent never reached memory 0's queue at address %d within "+
				"10s — this is the case's premise and not its verdict: no pair was formed", i, r.futex)
		}

		if woke := call(t, waker, "publish", I32(r.sib), I32(r.futex)); woke != 1 {
			t.Fatalf("round %d: memory.atomic.notify woke %d waiters at address %d, want 1 — the agent "+
				"was queued when this round armed, so the wake was counted against an empty queue",
				i, woke, r.futex)
		}

		got := (<-stamped).get(t)
		res, published := got>>4, got&1
		if res != waitWoken {
			t.Fatalf("round %d: memory.atomic.wait32 returned %d, want %d (woken) — a notify claimed this "+
				"agent and it was told something else happened. Under an infinite-in-practice timeout a "+
				"non-wake is an instrument fault rather than a verdict about B-MM-2", i, res, waitWoken)
		}
		if published != 1 {
			t.Fatalf("round %d: the woken agent read 0 from memory 1 at address %d, which A filled with 1 "+
				"before notifying on memory 0. This is the floor and not the verdict: it says A's write "+
				"and B's read did not land on one extent, so the detector's silence about the pair would "+
				"be silence about nothing", i, r.sib)
		}

		// **Which memory the fill landed in, read out of both.** The two offsets coincide, so every
		// assertion above holds unchanged if the memory index were dropped somewhere between the text and
		// `execMemoryFill` — the fill and the load would agree with each other in memory 0 and this case
		// would be the named case wearing a second memory's name. So the premise is read rather than
		// argued: memory 1 holds the published byte and memory 0's word is still the 0 the waiter matched,
		// which nothing in this case ever writes.
		//
		// Plain reads, and they are not a race: the fill happened on this goroutine through `call`, and
		// the waiter's load is ordered before this line by the receive from `stamped` above.
		if got := mems[1].view()[r.sib]; got != 1 {
			t.Fatalf("round %d: memory 1 holds %d at address %d, want 1 — A's `(memory.fill 1 …)` did not "+
				"land in the second memory, so this case's subject is absent and its pass is the named "+
				"case's", i, got, r.sib)
		}
		if got := mems[0].view()[r.futex]; got != 0 {
			t.Fatalf("round %d: memory 0 holds %d at address %d, want 0 — the fill reached the memory "+
				"holding the futex word, so the two accesses under test were in one address space",
				i, got, r.futex)
		}
	}

	t.Logf("b-mm-2-the-sibling-lives-in-a-second-shared-memory: %d rounds over two shared memories, "+
		"every round woken and every round reading the value published in memory 1. The verdict is the "+
		"race detector's silence about (A's fill in memory 1, B's load from memory 1) and is only taken "+
		"under `-race` — `make check` does not pass it; CI's `race` step inside the two-architecture "+
		"`build` job does", rounds)
}
