// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrStopDeadline is what `Stop` returns when the interval expired before every thread reached a
// safepoint. Contract §3 SP-1 asks for arrival *"within a bounded, configurable interval"*, so the
// bound is a promise the engine can fail to keep and must therefore be able to report failing.
//
// **A distinct error and not a bool, because the two outcomes are not "worked" and "did not".** A
// deadline expiry leaves the world in a stated state — the request is still set, the threads that did
// arrive are still parked — and a caller that reads a `false` has no way to know that. `Resume` is
// still the correct next call either way, which is why this is an error and not a panic.
var ErrStopDeadline = errors.New("burroughs: stop deadline expired before every thread reached a safepoint")

// world is the engine's stop-the-world state: contract §3's SP-1 arrival protocol.
//
// **Its extent is one `Instance`, and that is a named limit rather than the intended end state.** A
// shared memory spans instances — [ADR 0052]'s own reason for making the §4 boundary edge a
// package-level counter — so a stop that covers one instance does not cover every thread that can
// touch a given memory.
//
// This paragraph used to say the scope was right *today* because `Spawn` was parked, so "every thread
// of this instance" and "every thread that can reach this memory" named the same set. **`Spawn` has
// landed and that sentence is now false**, which is why it is replaced rather than annotated: an
// instance can have N threads, and a second instance sharing the memory has its own world. What
// [ADR 0068] does about it is refuse the case it cannot express — a spawn whose entry resolves into
// another instance (`ErrForeignEntry`) — so a *reachable* thread is still always a member of the world
// that would stop it. What stays out of reach is a thread of an instance that reached the same shared
// memory by importing it, which was already true before spawn and is #515's own SP-4 work.
//
// [ADR 0052]: ../../docs/decisions/0052-the-4-boundary-edge-is-one-package-level-sequentially-consistent-counter-because-a-shared-memory-spans-instances.md
type world struct {
	// mu guards every field below. It is taken by `Stop`, by `Resume`, and by a thread that has
	// *already* decided to park — never on the poll's fast path, which reads `thread.stopReq`
	// atomically and touches nothing here.
	mu sync.Mutex

	// resume is non-nil exactly while a stop is in progress, and is closed to release the parked
	// threads. A fresh channel per round rather than a reusable flag: a thread that arrives late
	// must be released by the round it arrived for, and a closed channel is the only release that
	// cannot be missed by a thread that started waiting after the close.
	resume chan struct{}

	// arrived carries one send per thread that reaches a safepoint. Buffered to the thread count so
	// a parking thread never blocks on the send — a thread that blocked here would be *at* a
	// safepoint and unable to say so, which is the one deadlock this protocol can have.
	arrived chan ThreadID

	// live is every thread of this instance that has not exited: the instantiation-time thread plus
	// one per `Spawn` that is still running. The slice rather than a single field is what let SP-4's
	// N-thread case change the population and not the protocol.
	//
	// **It shrinks now, and the sentence it replaces is the leak T-5 was filed to close.** This field
	// was `members` and *only grew*: a terminated thread stayed, reading as at a safepoint on
	// `blocked == callers == 0` — never wrong, since a dead thread cannot touch guest memory, and
	// never bounded either. **51 members after 50 completed spawns** is the measurement, and every
	// `Stop` walked them all. `retire` is the reaper contract §10.3 had no lifecycle to hook one to;
	// T-5.2 (ADR 0071) is the lifecycle.
	live []*thread

	// exited is T-5.2's `tid`→terminal-status record: what a `Join` answers for a thread that has
	// already gone. Nil until the first exit, so an instance that never spawns carries no map.
	//
	// **A `nil` value is a clean exit and absence is "no record", which is why the reader tests the
	// second return.** Those are different answers to a join — *"it returned"* against *"nothing here
	// knows"* — and an `error`-valued map collapses them if the presence bit is dropped.
	//
	// **Bounded by consumption, which is the stamped half of T-5.2** (*"consumed by a join, or dropped
	// at `Close`"* — Scott, on #12). Retention is what makes join-after-exit answerable at all, so the
	// bound cannot be "do not retain"; it has to be a rule about who takes the entry out. A record
	// nobody joins is dropped by `Close`, and until then a spawn loop with no joins holds one `error`
	// and one map slot per exited thread rather than one whole `thread` — the leak's shape, an order of
	// magnitude smaller, and stated rather than left to be discovered as fact 3 wearing a smaller
	// struct.
	exited map[ThreadID]error

	// fault is T-5.3's retained trap: the first trap that ended any thread of this instance, wrapped
	// in `ErrThreadFault`, readable through `Instance.Fault` forever and reported once through the next
	// `Invoke`.
	//
	// **Sticky and never consumed, where `exited` above is consumed, and the asymmetry is the two
	// channels T-5.3 asks for.** If the `Invoke` channel took the value away, the retrieval channel
	// would answer nil to an embedder that happened to make a host entry first — two channels racing
	// over one payload, which is one channel with a lottery attached. So `faultReported` gates only the
	// report and the value stays.
	//
	// **First trap wins.** A later thread's trap is not overwritten in, because the second report
	// would displace the first cause with a consequence — and it is still joinable by `tid` through
	// `exited`, which is where per-thread detail lives.
	//
	// **A shutdown-induced termination is not a fault.** `retire` records `ErrTerminated` in `exited`
	// and never here: an embedder that calls `Close` and then reads `Fault` must not be told its guest
	// trapped when what happened is that it shut the guest down.
	fault         error
	faultReported bool

	// closed is `Instance.Close`'s terminal mark — contract §5 H-3, [ADR 0069][0069].
	//
	// **Terminal, and therefore not a second `resume`.** `Stop`/`Resume` are a *pause* and this is a
	// *teardown*, which is the distinction that dissolved the apparent SP-4-versus-H-3 conflict
	// (*"A pause must not disturb a blocked host call; a teardown must interrupt it"* — Scott, on the
	// #646 review, recorded at #602). So nothing here is cleared, there is no `Reopen`, and this flag
	// does not participate in the arrival protocol at all: its only readers refuse rather than wait.
	closed bool

	// hostCalls counts host calls currently inside an embedder's function, and idle is closed when the
	// instance reaches quiescence with a `Close` waiting.
	//
	// **`idle` was `hostIdle` and the widening is T-5.4's *"and waits"*.** The old channel answered one
	// question — is any embedder function still running — because that was all §5 H-3's teardown had to
	// wait for. T-5.4 asks for more: *"no guest instruction of the instance executes after shutdown
	// returns"*, which also covers a thread executing guest code and a caller inside `Invoke`. So the
	// predicate moved to `quiescentLocked` and the three sites that can satisfy it — `endHostCall`,
	// `leaveCall`, `retire` — each release the channel. Renamed rather than joined by a second channel,
	// because two release channels for one wait is two places for the release to be missed.
	//
	// **A count plus a channel rather than a `sync.Cond`, because a `world` is used as a zero value.**
	// `sync.Cond` must be built with `sync.NewCond(&w.mu)`, so it would need a lazy initialisation
	// under the mutex at every touch — where a nil channel created once by whoever waits is the idiom
	// `resume` above already uses, for the reason stated there: *a closed channel is the only release
	// that cannot be missed by a thread that started waiting after the close.*
	//
	// **The count is of calls and not of threads**, for `thread.blocked`'s reason one level out: an
	// embedder may drive N concurrent `Invoke`s through one instance, each of which can be inside a
	// host call, and `Close` must wait for all of them. Guarded by `mu` like everything else here, so
	// that the "is anyone still inside" question and the answer to it cannot interleave.
	//
	// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
	hostCalls int
	idle      chan struct{}
}

// quiescentLocked reports whether no guest instruction of this instance can execute before something
// enters it again — T-5.4's *"Shutdown MUST wait for the resulting unwinds, so that no guest instruction
// of the instance executes after shutdown returns."* `mu` held.
//
// Three conditions, one per way guest code can be in flight, and each is the reason the corresponding
// release site exists:
//
//   - **No embedder function is running** (`hostCalls == 0`), which is §5 H-3's original predicate and
//     `endHostCall`'s release.
//   - **No caller is inside `Invoke`** (`callers == 0` on every live thread), which is `leaveCall`'s.
//     Without it, `Close` could return while a `Stop`-less `Invoke` was mid-loop on the instantiation
//     thread — the measured fact 4 (**Close returned in 39µs while a spawned counter ran on to 23008**)
//     is that hole on the spawned side.
//   - **Every spawned thread has retired** (`done == nil` on every live thread), which is `retire`'s.
//     A thread whose goroutine has left `runEntry` but has not yet been retired has `callers == 0`
//     already, so the caller condition alone would call it quiescent while its unwind was still
//     running. Waiting for the retirement is strictly stronger and needs no argument about how much of
//     the engine a departing thread still touches.
//
// The instantiation thread is `done == nil` by construction — it *"does not terminate"* (`thread.done`)
// — so the second condition is what covers it and the third does not exclude it.
func (w *world) quiescentLocked() bool {
	if w.hostCalls > 0 {
		return false
	}
	for _, t := range w.live {
		if t.done != nil || t.callers > 0 {
			return false
		}
	}
	return true
}

// soleAgentLocked reports whether `self` is the only agent of this world that could be executing guest
// code — ADR 0073's predicate, read by `memory.relocate` to decide whether abandoning an image can strand
// anybody. `mu` held.
//
// # It counts callers, and `len(live) == 1` is the unsound answer it replaces
//
// The plausible predicate is *"this world has one live thread"*, and it is **wrong**: an embedder calling
// `Invoke` from two goroutines has two agents on **one** thread object, because `link.go` builds `in.host`
// by literal and registers that single value. So the count that answers the question is `thread.callers` —
// which is decision 0067's subject one level over (*SP-2's predicate is about callers and a thread is not
// one*), and it is the same lesson because it is the same mistake available in the same place.
//
// # Self is excluded by identity, and by identity rather than by arithmetic
//
// The grower is one of the callers it is counting, so it must be discounted — but *"total callers == 1"* is
// not the way to do it. `build`'s start function and `runConst` run guest code **outside** any
// `enterCall`/`leaveCall` pair, deliberately (see `enterCall`), so a grow from a start function has
// `callers == 0` on `in.host`: a total of 1 would read instantiation as a second agent and refuse, and a
// subtraction would underflow. Identity answers both, and `self.callers <= 1` is what still catches the
// two-concurrent-`Invoke` case, where the second agent is on `self` itself.
//
// A nil `self` matches no member, so every live caller counts against it. That is the conservative reading
// and the right one: a caller with no thread is in no world, so it is not the agent any of these counts is
// about.
//
// **The same reading is what makes this correct when `relocate` asks it of several worlds.** A memory in two
// index spaces has `relocate` call this once per world, and `self` is a member of at most one of them — so in
// every *other* world the discount does not apply and one caller is enough to refuse. That is the answer the
// question wants: an agent inside `Invoke` on an instance that imported this memory can reach these bytes,
// and it is not the grower.
//
// # What it deliberately does not consult
//
// **`hostCalls` adds nothing.** A host call is made *from* guest code, so its thread is already inside a
// counted call and the walk below has refused on its account before `hostCalls` could be asked. A retained
// `Caller` used off-thread is the agent no count here can see, and it is excluded by `growMu` at the
// boundary accessors instead ([ADR 0073][0073], decision 6) — stated because the omission looks like the
// hole and the actual hole is elsewhere.
//
// **`done` and `blocked` add nothing either.** A spawned thread that has been admitted but has not reached
// `enterCall` holds no image — it has executed no memory instruction — and cannot acquire one during a
// relocation, because `enterCall` needs the mutex the relocation holds. A thread parked in a futex wait has
// `callers >= 1` and is refused, which is over-broad on purpose: the engine cannot tell a sibling that is
// *mid-access* from one that merely could be without putting an indicator on every guest access.
//
// [0073]: ../../docs/decisions/0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md
func (w *world) soleAgentLocked(self *thread) bool {
	for _, t := range w.live {
		limit := 0
		if t == self {
			limit = 1
		}
		if t.callers > limit {
			return false
		}
	}
	return true
}

// releaseIfQuiescent claims the waiting `Close`'s channel when the instance has reached quiescence, and
// returns it for the caller to close **outside** `mu`. `mu` held; nil when there is nothing to release.
//
// **The claim-under-the-lock-and-close-outside split is contract §4 B-MM-3**, whose control
// (`TestNoEngineLockIsHeldAcrossAChannelOperation`) already failed `endHostCall`'s first draft for
// closing inside the critical section. Nil'ing the field under the lock is what makes the close safe to
// do outside it: exactly one caller can observe a non-nil channel, so a double close is impossible by
// construction rather than by an argument about who got there first.
//
// The guard is `w.idle != nil` first, so the walk in `quiescentLocked` costs nothing on the ordinary
// path — no `Close` is waiting, so there is no question to answer.
func (w *world) releaseIfQuiescent() chan struct{} {
	if w.idle == nil || !w.quiescentLocked() {
		return nil
	}
	idle := w.idle
	w.idle = nil
	return idle
}

// isClosed reports whether `Close` has run. Advisory by construction and used only where advisory is the
// right strength: `invokeIndex`'s refusal, which has a correct second answer if it loses the race.
//
// **It is deliberately not the shape `beginHostCall` and `admit` have.** Those two must decide *and* act
// under one acquisition, because a call or a member admitted after `Close`'s wait completed is outside a
// wait that has already returned — the indivisibility argument stated at both. Nothing here is admitted:
// a `Close` landing just after this reads true finds the thread through the ordinary terminal marks and
// the caller is told `ErrTerminated` instead of `ErrClosed`. Two right answers about one instant, which
// is what makes a separate load legitimate here and not there.
func (w *world) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.closed
}

// beginHostCall admits one host call, or refuses because the instance is closed.
//
// **The check and the increment are one critical section, and it is `admit`'s argument one subject
// over.** `Close` reads `hostCalls` under this mutex and then waits for it to reach zero; a call that
// tested `closed` and incremented later could slip in *after* that wait completed, so `Close` would
// have returned while an embedder's function was still running — a teardown reporting a completion it
// does not have. Split into two operations, the refusal would be advisory in exactly the way `admit`'s
// would.
//
// **No thread argument, because the thread is what selects the receiver.** `callHost` resolves the world
// from `st.t` rather than from the instance that declared the import, and the reason is at
// `thread.world`: the two differ on any cross-instance call, and a counter in one world with a
// cancellation in another is a `Close` that waits on something it cannot interrupt.
func (w *world) beginHostCall() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("%w: a host call cannot begin on a closed instance (contract §5 H-3)", ErrClosed)
	}
	w.hostCalls++
	return nil
}

// endHostCall retires one host call and releases a waiting `Close` when it was the last.
//
// # The claim, and the state under the lock, and the close outside it
//
// **The channel is claimed under the mutex and closed after it, which is contract §4 B-MM-3 and not a
// preference.** The first draft closed it inside the critical section and
// `TestNoEngineLockIsHeldAcrossAChannelOperation` failed it on sight — an engine-internal lock must not
// be held across a guest resume, and a `close` on a release channel *is* the resume. The repair is the
// one that control's own message prescribes: clear the guarded state under the lock, act on it after.
//
// Nil'ing the field is what makes the close safe to do outside: exactly one caller can observe a
// non-nil `hostIdle`, so a double close is impossible by construction rather than by a happens-before
// argument about who got there first. Nothing can create a second channel in the window either —
// `Close` only makes one while `hostCalls > 0`, and it has already set `closed`, so `beginHostCall`
// refuses every new call and the count can only fall.
//
// A second `Close` racing this one returns as soon as it sees `hostCalls == 0`, possibly before this
// `close` executes, and that is the correct reading rather than a gap: the predicate H-3 waits on is
// *"no embedder function is still running"*, and a count of zero is exactly that. The channel is how a
// waiter is woken, not what the waiting is about.
func (w *world) endHostCall() {
	w.mu.Lock()
	w.hostCalls--
	idle := w.releaseIfQuiescent()
	w.mu.Unlock()

	if idle != nil {
		close(idle)
	}
}

// retire moves a thread out of the live set and records its terminal status — T-5.2's reaper and
// T-5.3's fault channel, [ADR 0071][0071].
//
// **Called before `done` closes, and that ordering is what makes `Join` answerable.** A joiner waits on
// `done` and then reads the record; if the record were written after the close, the wake would race the
// write and a join could find nothing for a thread it had just watched finish. `spawn`'s goroutine
// therefore holds both in one `defer`, in this order.
//
// **It does not send an arrival, and the omission is deliberate rather than an oversight.** A thread that
// `Stop` counted as a sender and that exits before its next safepoint never announces, so that round
// waits out its whole deadline and reports a false expiry — measured, **`ErrStopDeadline` after
// 2.0011575s for a thread that had already exited**. Sending here would repair that arm and break the
// other: `Stop` counts *receives*, not identities, so an arrival on behalf of a thread the round did not
// await satisfies a still-running thread's slot instead. Both directions are one defect in the arrival
// protocol, they are filed together with their witnesses as **[#656]**, and the repair is that issue's
// rather than this one's: live-only membership neither creates nor worsens either arm.
//
// [0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
// [#656]: https://github.com/scttfrdmn/burroughs/issues/656
func (w *world) retire(t *thread, err error) {
	w.mu.Lock()
	for i, m := range w.live {
		if m != t {
			continue
		}
		w.live = append(w.live[:i], w.live[i+1:]...)
		break
	}
	if w.exited == nil {
		w.exited = make(map[ThreadID]error)
	}
	w.exited[t.id] = err
	// T-5.3's retention, and the two exclusions are both load-bearing: a shutdown-induced termination
	// is not a guest fault (see `world.fault`), and a later trap does not displace the first cause.
	if err != nil && w.fault == nil && !errors.Is(err, ErrTerminated) {
		// Two `%w` verbs, so the reported error matches `ErrThreadFault` *and* the trap it carries —
		// an embedder testing for either gets the same answer from `Fault` and from `Invoke`, which is
		// what makes T-5.3's two channels two views of one value.
		w.fault = fmt.Errorf("%w: %s: %w", ErrThreadFault, t, err)
	}
	idle := w.releaseIfQuiescent()
	w.mu.Unlock()

	if idle != nil {
		close(idle)
	}
}

// takeFaultReport answers the retained fault the *first* time it is asked and nil thereafter — T-5.3's
// host-entry channel, consumed once. `Instance.Invoke`'s pre-emption.
//
// **What is consumed is the report and not the value**, which is the asymmetry `world.fault` documents: an
// instance whose spawned thread trapped once must not fail every subsequent `Invoke` with the same news,
// and an embedder that reads `Instance.Fault` after the report must still be told what happened.
func (w *world) takeFaultReport() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fault == nil || w.faultReported {
		return nil
	}
	w.faultReported = true
	return w.fault
}

// faultValue is the retained fault with no consumption at all — T-5.3's retrieval channel, behind
// `Instance.Fault`.
func (w *world) faultValue() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fault
}

// register adds the instantiation-time thread to the world. Called once, from `link.go`, and it
// cannot fail: no stop can be in progress on an instance no caller has been handed yet.
func (w *world) register(t *thread) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addLocked(t)
}

// admit adds a spawned thread to the world, or refuses because a stop is in progress.
//
// **The check and the append are one critical section, and that indivisibility is the whole
// soundness argument** ([ADR 0068][0068]). `Stop` sizes `arrived` to the membership it observed under
// this same mutex, so a member appended mid-round is an (N+1)th potential sender into N slots, and a
// thread that blocks on that send is *at a safepoint and unable to say so* — see `arrived`. Split
// into a read and a later append, the refusal would be advisory: a `Stop` could begin between them
// and the new thread would join a round that has already counted its senders.
//
// The complementary direction is `Stop`'s own `w.resume != nil` guard, so the two orderings are the
// only two: either this refuses, or `Stop` observes the new member from the start.
//
// [0068]: ../../docs/decisions/0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md
func (w *world) admit(t *thread) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		// **§5 H-3's teardown refuses a new thread, where SP-1's pause merely delays one.** The two
		// refusals sit together and are not the same refusal: `ErrStopInProgress` below is about a
		// round's arrival slots and is transient — the caller retries after `Resume` — while this one
		// is terminal, because `Close` has already cancelled every member and returned. A thread
		// admitted after that would be a member no `Close` will ever wait for, running guest code on
		// an instance the embedder believes is torn down. Checked here rather than only in `spawn` so
		// that the guarantee belongs to the same critical section as membership itself.
		return fmt.Errorf("%w: this instance is closed, so a new thread would outlive the teardown "+
			"that has already completed (contract §5 H-3)", ErrClosed)
	}
	if w.resume != nil {
		return fmt.Errorf("%w: this instance has %d threads at or heading for a safepoint, and a "+
			"member admitted mid-round would have no slot to announce itself in", ErrStopInProgress,
			len(w.live))
	}
	w.addLocked(t)
	return nil
}

// addLocked is the one place membership, `t.w` and `t.ctx` are established, so the invariant that a
// member's `w` points back at the world holding it — and that the world can cancel it — cannot be
// half-kept by one of two callers. `w.mu` held.
//
// **The context is created here rather than at either creation site, and that is a correction to
// [ADR 0069][0069]'s third choice, which named `newThread`.** `newThread` is `Spawn`'s alone, and the
// *ordinary* host call runs on `in.host`, which link.go builds by literal and hands to `register`. A
// context created in `newThread` would be nil on exactly the thread every host call in the tree today
// runs on, so `Instance.Close` would cancel nothing while looking correct — a teardown that reports
// completion having interrupted no one. This function is where the membership invariant already lives,
// so it is where the cancellation invariant belongs too.
//
// **A `Close` that has already run cancels the new thread immediately**, rather than handing it a live
// context a closed world would never cancel. `admit` refuses a spawn during a *stop*, and nothing
// refuses one on a closed world here — `spawn` does that, above this layer — so this is the belt to
// that braces: a member added to a closed world is born cancelled.
//
// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
func (w *world) addLocked(t *thread) {
	w.live = append(w.live, t)
	t.w = w
	t.ctx, t.cancel = context.WithCancel(context.Background())
	if w.closed {
		t.cancel()
		// **Born cancelled *and* born terminal**, which is the T-5.4 half of the same belt. A closed
		// world's `Close` has already chosen its live set and may already have returned, so a thread
		// added here would be one nothing waits for; the cancellation ends it if it enters a host call
		// or a wait, and this ends it at its first safepoint if it enters neither. `spawn` refuses
		// above this layer, so the reachable caller is `register` on an instance closed mid-build.
		t.exitReq.Store(true)
		t.stopReq.Store(true)
	}
}

// Stop is contract §3 SP-1's host request: bring every guest thread of this instance to a safepoint,
// within `deadline`.
//
// Returns nil when every thread has arrived, `ErrStopDeadline` when the interval expired first. On
// either outcome the world is *stopped* and `Resume` must be called: a partial stop still has threads
// parked, and leaving them parked is a hang rather than a degraded mode.
//
// **The wait is on the arrival signal and not on a timer that is then re-checked.** Each running thread
// sends once as it parks, and this loop receives exactly as many times as there are such threads, with
// the deadline as the competing case. Polling an arrival counter with a sleep in between would report
// the stop's completion at the granularity of the sleep rather than of the event, and would make the
// bound this clause promises a property of the poll interval instead of the engine.
//
// # SP-2 inverts the protocol for a thread that is already parked, and SP-4 is why
//
// §3 SP-2 makes a thread suspended in `memory.atomic.wait` count as *at a safepoint*, and SP-4 requires
// that a stop *"with N threads parked in host calls completes without waking them."* Together they
// forbid the obvious implementation: SP-1's protocol has the *thread* announce its arrival, a thread
// already blocked in a wait cannot announce anything, and SP-4 forbids waking it to ask. So the
// direction inverts — **this function counts a blocked thread as arrived itself**, reading `blocked`
// under the same mutex the transition takes so that the two cannot interleave. Decision 0060's third
// choice, and `futex.go`'s `wait` is the other half.
//
// `stopReq` is still set on a blocked thread, which is what makes its wake path park: it clears
// `blocked` and polls before it pushes anything.
func (in *Instance) Stop(deadline time.Duration) error {
	w := &in.world

	w.mu.Lock()
	if w.resume != nil {
		w.mu.Unlock()
		return errors.New("burroughs: Stop called while a stop is already in progress")
	}
	// **A stop over a closed world would report a verdict about nothing**, which is worse than
	// refusing: `Close` has set every live thread terminal, so each one either has already unwound or
	// is about to, and both of this function's outcomes would then be meaningless — a `nil` claiming a
	// stopped world that is a *torn-down* world, or an expiry blaming threads for not arriving at a
	// safepoint they are dying instead of reaching. `admit` already treats `closed` as terminal for the
	// other direction (a new member), on the same reasoning one clause over.
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("%w: this instance is closed, so a stop has no world to bring to a "+
			"safepoint (contract §2 T-5.4, §5 H-3)", ErrClosed)
	}
	w.resume = make(chan struct{})
	w.arrived = make(chan ThreadID, len(w.live))
	// Captured under the lock and read from the local below. Reading `w.arrived` after the unlock
	// would be a plain read of a field `Resume` nils, which is a data race on the field itself even
	// though every *channel* operation on it is safe — the distinction that makes `-race` the
	// authority here rather than "channels are concurrency-safe".
	//
	// `want` is the number of threads that will *send*, which is no longer the member count: a thread
	// already blocked in a wait is at a safepoint by SP-2 and must not be waited for. The buffer stays
	// sized to the full membership, because a counted-as-arrived thread still sends once when it wakes
	// into a stop that is in progress, and that send must not block a thread that is at a safepoint.
	arrived, want, atSafepoint := w.arrived, 0, 0
	for _, t := range w.live {
		t.stopReq.Store(true)
		// Cleared here because the flag belongs to *this* round: the round is what `w.resume` names,
		// and installing a new one is the only moment at which a previous round's arrivals stop
		// counting. Grave #593.
		t.reported = false
		// **`blocked == callers` and not `blocked > 0`**, which is decision 0067's mechanism and the
		// repair for #592. `blocked` counts *callers* that are suspended, and the old predicate read
		// it as a fact about the *thread* — asking "is some caller here suspended" in place of "is no
		// caller here running". One `thread` per instance against an ungated exported `Invoke` makes
		// those different: with one caller suspended in a wait and another executing a loop, the old
		// form counted the thread as arrived and this function returned `nil` while guest code ran,
		// which is SP-2 failing on its own terms.
		//
		// The panic is not defensive noise. `blocked <= callers` holds by construction — the only
		// route into `enterBlocked` is guest code, which runs inside a call that has already been
		// counted — and if it were ever violated the equality would be unsatisfiable, so every `Stop`
		// would wait out its whole deadline for an arrival that cannot come. That failure is worse
		// than the one being fixed and it is silent, so the invariant the predicate rests on is
		// asserted where it is relied upon.
		if t.blocked > t.callers {
			w.mu.Unlock()
			panic(fmt.Sprintf("burroughs: %s has %d blocked callers of %d — a suspended caller is "+
				"by construction inside a counted call, so this is an unpaired enterBlocked or "+
				"leaveCall (decision 0067)", t, t.blocked, t.callers))
		}
		if t.blocked == t.callers {
			atSafepoint++
			continue
		}
		want++
	}
	total := len(w.live)
	w.mu.Unlock()

	timer := time.NewTimer(deadline)
	defer timer.Stop()
	// `i` and not `len(arrived)`: a channel's length is its buffer's current occupancy, which this
	// loop has been *draining*, so `len` would report what is still waiting rather than what has
	// arrived — plausibly, and wrong in the direction that under-reports a partial stop. The loop
	// index counts completed receives, which is the quantity the message claims.
	//
	// The message counts against `total` rather than against `want`, and adds the threads that were
	// already at a safepoint: a caller reading *"1 of 3"* is asking how much of the world is stopped,
	// and reporting the number of *senders* would answer a question about this function's protocol.
	for i := range want {
		select {
		case <-arrived:
		case <-timer.C:
			return fmt.Errorf("%w: %d of %d arrived within %s",
				ErrStopDeadline, atSafepoint+i, total, deadline)
		}
	}
	return nil
}

// enterBlocked marks this thread as at a safepoint for the duration of a suspension it is about to
// enter, and parks first if a stop is already in progress. Contract §3 SP-2's guest half for
// `memory.atomic.wait`; decision 0060's third choice, called from `futex.go`'s `wait`.
//
// **The mark and the stop check are one critical section, which is what makes the three-way race a
// two-way one.** Either this runs first, and the `Stop` that follows reads the mark and counts this
// thread as arrived without waiting for it; or `Stop` runs first, and this thread finds a round in
// progress and announces itself through the ordinary park before suspending. The outcome that must not
// exist is the third: a `Stop` that neither observes the mark nor receives an arrival, which is a
// deadline expiry reported for a thread that is by definition not running.
//
// Parking *before* the suspension rather than during it is what keeps SP-4's promise: this thread
// reaches its safepoint by the existing route, and no round ever needs to wake a waiter to complete.
//
// A nil thread or a nil world is a no-op, on the same ground as `poll`: a thread with no world has no
// `Stop` that could be walking it.
func (t *thread) enterBlocked() {
	if t == nil || t.w == nil {
		return
	}
	w := t.w

	w.mu.Lock()
	t.blocked++
	stopping := w.resume != nil
	w.mu.Unlock()

	if stopping {
		t.parkAtSafepoint()
	}
}

// leaveBlocked clears the mark and polls, in that order, before the caller pushes anything.
//
// **The poll is what SP-2's second half asks for**: a thread that leaves a wait *"cannot touch guest
// memory until it re-enters through a boundary that observes the stop"*, and the wake is that boundary.
// A stop in progress at this moment parks this thread here — where `Stop` may already have counted it
// as arrived, which is why the arrival channel is buffered to the full membership rather than to the
// number of expected senders.
//
// Clearing before polling and not after: with the mark still set, a `Stop` racing this would count the
// thread as being at a safepoint it has just left.
//
// **The two halves are separately callable, and only this one is the normal path.** `unmarkBlocked`
// below is the clear on its own, for `callHost`'s unwinding path, which must drop the mark and must not
// park (ADR 0070). Composed here rather than duplicated so that the order — clear, then poll — has one
// site to be wrong at.
func (t *thread) leaveBlocked() {
	t.unmarkBlocked()
	t.poll()
}

// unmarkBlocked clears the mark without polling — `leaveBlocked`'s first half, split out for the one
// caller that must not park: `callHost`'s panic path (ADR 0070).
//
// **The poll is omitted there because SP-2's second half has no subject on an unwinding thread.** The
// clause the poll serves is *"a thread that leaves a wait cannot touch guest memory until it re-enters
// through a boundary that observes the stop"*, and a thread carrying a panic out of an embedder's
// function touches no guest memory on its way: it runs `defer`s and returns frames until something
// above `Invoke` recovers it or the process dies. What it must not do is *park* — a `Stop` in flight
// would hold a panicking goroutine inside its round, so the stop's completion would depend on an
// embedder's `recover` running, which is a dependency SP-4 does not have and cannot state.
//
// **And the next entry into the guest polls anyway**, which is what makes the omission safe rather than
// merely convenient: an embedder that recovers and `Invoke`s again reaches `run` → `enterFrame`, whose
// first statement is `st.t.poll()`, before one guest instruction executes. Same argument `enterCall`
// makes for not parking, one caller over.
//
// A nil thread or a nil world is a no-op, on `poll`'s ground.
func (t *thread) unmarkBlocked() {
	if t == nil || t.w == nil {
		return
	}
	w := t.w

	w.mu.Lock()
	t.blocked--
	w.mu.Unlock()
}

// enterCall counts one caller as executing on this thread, and leaveCall uncounts it. Decision 0067's
// mechanism, the repair for **#592**, and the pair that gives `Stop`'s predicate its denominator.
//
// **The pair exists because `blocked` alone cannot express the mixed state.** `blocked` counts callers
// that are suspended; `Stop` was reading it as a fact about the thread, which is *"is some caller here
// suspended"* asked in place of *"is no caller here running"*. Those coincide only while a thread has one
// caller, and one `thread` per instance against an exported `Invoke` that nothing gates means it does
// not. `Stop` now asks `blocked == callers`, and this is where the right-hand side comes from.
//
// # Three things it deliberately does not do
//
// **It does not park.** `enterBlocked` parks, because a thread about to suspend must announce itself
// before it becomes unable to; a thread about to *run* is already covered — `run` calls `enterFrame`,
// whose first statement is `st.t.poll()`, so a caller that arrives while a stop is in flight parks at
// frame entry before executing one guest instruction. Adding a park here would fence twice for one edge
// and put a second announcement site on the `Invoke` path.
//
// **It does not wrap the whole of `invokeIndex`, and the placement is load-bearing rather than tidy.**
// `invokeIndex` delegates for a re-exported import by returning `ext.owner.invokeIndex(...)`, so exactly
// one `in.run` executes per chain — but the *delegating* instance has its own `thread`. Counting on entry
// to `invokeIndex` would leave that thread with a caller counted and no guest code running on it, which
// makes `blocked == callers` false for a thread that will never poll and never arrive: every `Stop` on
// that instance would wait out its full deadline. So the pair wraps `in.run` and nothing wider.
//
// **It does not wrap instantiate-time execution.** `build`'s start function and `runConst` run before
// `InstantiateLinked` returns, so no external reference to the instance exists and no `Stop` can be in
// flight against it. That is the only guest code that runs outside `Invoke`, and it is named here rather
// than left as a silent asymmetry between this pair and the `enterGuest`/`leaveGuest` one.
//
// A nil thread or a nil world is a no-op, on `poll`'s ground: a thread with no world has no `Stop` that
// could be walking it.
func (t *thread) enterCall() {
	if t == nil || t.w == nil {
		return
	}
	w := t.w

	w.mu.Lock()
	t.callers++
	w.mu.Unlock()
}

// leaveCall uncounts a caller that has finished executing guest code. See `enterCall`.
//
// No poll, and unlike `leaveBlocked` that is not an omission: `leaveBlocked`'s poll is SP-2's second half
// — a thread leaving a wait must not touch guest memory before observing a stop — and this function is
// reached when the caller has *stopped* touching guest memory and is on its way back across the boundary
// to host code. There is nothing left for a stop to protect from it.
//
// **It is one of the three release sites for T-5.4's quiescence wait** (`quiescentLocked`), because a
// caller inside `Invoke` is one of the three ways guest code can be in flight. Costs the ordinary path a
// nil test: `releaseIfQuiescent` returns immediately unless a `Close` is actually waiting.
func (t *thread) leaveCall() {
	if t == nil || t.w == nil {
		return
	}
	w := t.w

	w.mu.Lock()
	t.callers--
	idle := w.releaseIfQuiescent()
	w.mu.Unlock()

	if idle != nil {
		close(idle)
	}
}

// Resume releases every thread parked by `Stop` and clears the request.
//
// Safe to call after a `Stop` that returned `ErrStopDeadline`: the threads that did arrive are
// released and the ones that had not yet seen the request stop seeing it. Calling it without a stop in
// progress is a no-op rather than an error, because the deadline case makes "did the stop succeed"
// the wrong question for a caller to have to answer before cleaning up.
// **The release happens after the unlock, and that ordering is contract §4 B-MM-3 rather than a
// preference.** B-MM-3: *"The engine MUST NOT hold engine-internal locks across a guest resume."*
// `close` **is** the guest resume — it is the operation that puts parked threads back on the
// interpreter's dispatch loop — so closing under `mu` would be precisely the prohibited shape. It was
// written that way first, with a `defer w.mu.Unlock()`, and
// `TestNoEngineLockIsHeldAcrossAChannelOperation` is the control that now makes the ordering checked
// rather than remembered.
//
// The state is cleared *before* the unlock, which is what makes the split safe: a thread released here
// finds `stopReq` already false and `w.resume` already nil, so it cannot re-park for the round it was
// just released from. A `Stop` that wins the mutex in the window between the unlock and the close
// installs a fresh channel and is unaffected — `release` is a local, so the close still releases
// exactly the round it belongs to.
func (in *Instance) Resume() {
	w := &in.world

	w.mu.Lock()
	release := w.resume
	if release == nil {
		w.mu.Unlock()
		return
	}
	for _, t := range w.live {
		t.stopReq.Store(false)
	}
	w.resume = nil
	w.arrived = nil
	w.mu.Unlock()

	close(release)
}

// parkAtSafepoint reports arrival and blocks until the world resumes. Contract §3 SP-1's guest half.
//
// Called only from `poll`, and only once `stopReq` has been observed set. The re-read of `w.resume`
// under the lock is the race this function exists to close: `Resume` can run between the atomic load
// in `poll` and the lock here, in which case there is nothing left to park for and returning is
// correct rather than a missed stop.
//
// **What "at a safepoint" means here is that guest memory is untouched for the duration**, which is
// SP-2's guarantee stated for the back-edge case: this returns to `jumpTo`, which returns to the
// dispatch loop, and no guest instruction executes between the send below and the release.
//
// # B-MM-1's edge on this transition, and why it is not `enterGuest`
//
// §4 B-MM-1 names *"async wake"* among the host→guest transitions that MUST constitute *"an acquire
// edge over the entire shared address space for the resuming agent"*, and a safepoint resume is that
// shape: the host stops the world precisely in order to look at or change something, so a thread that
// resumed without the edge could carry on against a stale view of whatever the host wrote.
//
// **The edge is the channel pair, and it is an edge over everything rather than over one word.** A
// receive from a closed channel synchronizes with the close, so this thread observes every write
// `Resume`'s caller made before calling it; the send above synchronizes the other direction, so the
// host that observes an arrival observes the guest's writes before it. That is B-MM-1 in both
// directions, and it is worth noticing that the clause exists because the *browser* host's
// `Atomics.notify` gives the narrow version — an edge for the notified word only (D20, the gap this
// whole section descends from). Go's channel is the wide version, so the engine gets the clause for
// free here and must not be read as having got it by luck:
// `TestAResumedGuestSeesAHostWriteFromTheStop` is the behavioural check, and `-race` is the authority
// that a plain flag substituted for the channel would not pass.
//
// **`enterGuest`/`leaveGuest` are deliberately not called**, which is a narrower claim than it looks.
// `boundary.go`'s counter is B-MM-1 at *this engine's* radius — a host Go caller entering
// `internal/interp` and returning — and a parking thread never leaves: it is already inside the
// crossing `Invoke` opened, and the host's `Stop`/`Resume` are Go calls that enter no interpreter at
// all. Adding a pair here would fence a second time for an edge the channel already gives, and would
// put guest-thread traffic on a counter whose per-case exact rows `TestEveryBoundaryCrossingIsPaired`
// asserts.
//
// **No error return, and that is a decision rather than an omission** — see `poll`.
//
// # The terminal check runs twice, and the first one is not redundant
//
// T-5.4's *"shutdown ends every thread"* arrives here: `Close` sets `exitReq` and `stopReq` on every
// live thread, so the next poll lands in this function and `terminate` panics the sentinel out of it
// ([ADR 0071]). Both checks are needed because the two callers reach this function in different world
// states:
//
//   - **Before the `release == nil` early return**, because `Close` nils `w.resume` on its way out. A
//     thread that polls after that returns from the check above without ever reading `exitReq`, and runs
//     on — which is fact 4 (`Close` returned in 39µs while a spawned counter ran to 23008) reproduced by
//     the very mechanism meant to fix it.
//   - **After `<-release`**, for the thread that was already parked in a `Stop` round when `Close` ran.
//     Its `exitReq` was set while it sat on the receive, so the pre-park read observed nothing to do and
//     the post-release read is the only one that can see the mark.
//
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
func (t *thread) parkAtSafepoint() {
	w := t.w
	if w == nil {
		return
	}

	if t.exitReq.Load() {
		t.terminate()
	}

	w.mu.Lock()
	release, arrived := w.resume, w.arrived
	if release == nil {
		w.mu.Unlock()
		return
	}
	// One arrival per thread per round, decided here because `mu` is the only place it can be
	// decided — grave #593, and the sentence this replaces is the reason it is a grave rather than a
	// refinement. That sentence argued the send could not block because *"the buffer is
	// `len(w.members)` and each thread sends once per round"*, and deferred the hazard to SP-4's
	// dynamic membership. It was already reachable: the sender is a **caller**, not a thread, one
	// `thread` serves N concurrent `Invoke` calls (#592), and the third caller of three parked by one
	// `Stop` blocked on this send forever with `Resume` unable to free it.
	//
	// A non-blocking send would also not hang, and is worse: it drops arrivals, and a dropped arrival
	// lets `Stop` count two from one member and report a stopped world with another member running.
	// A hang is visible; a wrong verdict is not.
	report := !t.reported
	t.reported = true
	w.mu.Unlock()

	// Both channel operations are outside the lock, for the two halves of B-MM-3. The receive is the
	// obvious one: blocking on a resume while holding an engine lock is the clause's own hazard, and
	// `Resume` needs `mu` to run at all, so it would be a deadlock and not merely a violation. The
	// send is outside for the same reason it is now deduplicated: its safety must not rest on a count
	// of anything, and under `mu` a send that blocked for any reason would take `Resume` with it.
	//
	// Both are read from locals captured above: `Resume` nils the fields, so reading `w.arrived` here
	// would be a plain read of a word another goroutine writes — a race on the *field*, even though
	// every channel operation on the value is safe.
	if report {
		arrived <- t.id
	}
	<-release

	if t.exitReq.Load() {
		t.terminate()
	}
}

// poll is contract §3 SP-1's safepoint check — [ADR 0059]'s chosen mechanism, and the first reader of
// anything on `thread` besides its id.
//
// **The fast path is one atomic load and no lock**, which is the whole shape of the decision: a stop
// is rare and a back-edge is not, so the cost paid per back-edge must be the cheapest thing that is
// still sound. It cannot be a *plain* load — `Stop` writes `stopReq` from another goroutine, and a
// non-atomic read of a word another goroutine writes is a data race, which is undefined behaviour
// rather than a fast option.
//
// A nil receiver is a no-op. `stack.t` is documented nil-legal for stacks the host builds for its own
// bookkeeping, and a stack with no thread context has nothing that could have been asked to stop.
//
// # No error return, and 0059's own consequence list said there would be one
//
// That paragraph forecast an error path through every one of the fourteen arms, on the argument that
// *"retrofitting one into fourteen arms later is the change this ADR's own diff shape argues
// against"*. It is withdrawn, because §3 has no clause a poll can fail: SP-1 stops and resumes, SP-2
// widens what counts as stopped, SP-3 is a timer channel and SP-4 is composition — none of them asks
// a back-edge to abort a guest. So the error would have been **always nil**, which is the shape this
// project has a linter enabled for and a grave already dug on (`unparam`, and *an always-nil error
// return is a missing check wearing a disguise*, grave 0003). Suppressing that to hold a return path
// open for a clause that does not exist is speculative scaffolding paid for on the hot path, in a
// branch that can never be taken.
//
// The retrofit argument is answered by the control rather than by the signature: the population of
// `pc` assignments is *derived from the source*, so adding an error to fourteen arms later is a
// mechanical rewrite of a set an instrument can already enumerate. What made a retrofit expensive was
// not knowing where the arms were.
//
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
func (t *thread) poll() {
	if t == nil || !t.stopReq.Load() {
		return
	}
	t.parkAtSafepoint()
}

// jumpTo returns `target` for the dispatch loop to adopt as its next `pc`, polling first when the
// jump is a back-edge. [ADR 0059]'s option B, and the reason every `pc` assignment in `runFrame` goes
// through it rather than assigning directly.
//
// **`target < pc` is the back-edge test, and it is a comparison rather than a claim about the
// grammar.** The same assignment site goes backwards or forwards depending on the label resolved — a
// `br` to a `loop` continues it, a `br` to a `block` leaves it — so which sites *can* be back-edges is
// a fact about the grammar that would need its own authority and could be wrong in the direction no
// vector sees. Comparing the two numbers reads the execution that actually happened, which is the
// property that makes a predicate over a real value unable to answer wrongly about it.
//
// **Straight-line code never reaches here**, which is the cost argument: a body that does not branch
// pays no compare, no load, and holds no extra register, because it never assigns `pc` at all.
//
// **Returns `target` rather than assigning through a `*int`**, which is the shape half of option B:
// `pc` stays an ordinary local the compiler can keep in a register, where a pointer parameter would
// force it to memory and pay option A's tax by another route while claiming to avoid it.
//
// No error result — see `poll` for why the one 0059 forecast is withdrawn, and note the second reason
// it matters here: a `pc, err = …` form would put a never-taken branch at every one of the fourteen
// sites, on the path this whole decision exists to keep cheap.
//
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
func (t *thread) jumpTo(target, pc int) int {
	if target < pc {
		t.poll()
	}
	return target
}
