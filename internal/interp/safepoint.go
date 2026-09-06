// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// world is the engine's stop-the-world state: contract §3's SP-1 marks and the round that reads them.
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

	// stopped is closed when SP-1's promise holds for the round `resume` names: no agent of this world
	// is executing guest code. Non-nil only while a round is waiting for one — a `Stop` over a world
	// that is already at a safepoint allocates nothing.
	//
	// **It replaces a `chan ThreadID` carrying one arrival per parking thread, and the replacement is a
	// bug fix rather than a simplification** ([ADR 0074][0074]). That channel made `Stop` wait for a
	// *count of sends*, and the sender is a **caller** while the count was of threads — four reachable
	// shapes followed, two of them a `nil` returned while guest code ran, and the one that convicts the
	// design is a thread with two callers whose blocked one wakes and fills its sibling's slot. Neither a
	// token nor a `ThreadID` names a caller, so no repair that keeps the count can tell those apart. What
	// SP-1 promises is a *state*, and `atSafepointLocked` is that state read from the marks that already
	// express it; this channel is only how a waiter is woken.
	//
	// Claimed under `mu` and closed outside it, `releaseIfQuiescent`'s shape and §4 B-MM-3's requirement.
	//
	// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
	stopped chan struct{}

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
	// is not one of SP-1's marks at all: its only readers refuse rather than wait.
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

// atSafepointLocked reports whether SP-1's promise holds: no agent of this world is executing guest code.
// [ADR 0074][0074]'s predicate, and what `Stop` waits on. `mu` held.
//
// # It is a state and not a count of messages, which is the whole of 0074
//
// The protocol this replaces had `Stop` decide how many threads would *announce* an arrival and then
// receive that many times. The announcer is a **caller** — one `thread` per instance serves N concurrent
// `Invoke`s ([ADR 0067][0067]) — so the count was of one thing and the sends of another, and four shapes
// followed. The one that refutes every count-preserving repair: two callers on one thread, the blocked one
// wakes mid-round and its announcement satisfies its **sibling's** slot, measured as *"`Stop` -> nil after
// 200.9ms with the running caller's last guest instruction unrun"*. One id, one row in `live`, one
// per-thread mark — nothing that names a thread can separate those two callers.
//
// So the marks answer it instead. Each caller admitted to the guest is one unit of `callers`
// (`enterCall`), and a caller that is not executing has said so in exactly one of two ways: `blocked` for
// a suspension (SP-2, `enterBlocked`) or `parked` for a safepoint (`parkAtSafepoint`). The world is
// stopped when every live thread's callers are all accounted for that way.
//
// # `>=` rather than `==`, and the `parked` bookkeeping that makes it exact anyway
//
// `parked+blocked <= callers` holds by construction — `parkAtSafepoint`'s `counted` parameter is what
// keeps `enterBlocked`'s caller from holding two terms at once, and that argument is at the parameter. The
// comparison is still written loosely for the reason 0067's panic states one clause over: an
// unsatisfiable equality makes *every* `Stop` wait out its whole deadline, which is a worse failure than
// the one being fixed and a silent one. `>=` cannot be unsatisfiable, so a violated invariant degrades to
// an early return rather than to a hang.
//
// # What it deliberately does not consult
//
// **`hostCalls` adds nothing**, for `soleAgentLocked`'s reason: a host call is made from guest code, so
// its caller is inside a counted call *and* marked `blocked` by `callHost` (ADR 0069) — already both
// terms of this predicate. `quiescentLocked` consults it because a *teardown* must wait for the embedder's
// function to return, where a pause must not (SP-4: a stop *"with N threads parked in host calls completes
// without waking them"*).
//
// **`done` adds nothing either.** A thread whose goroutine has left but which has not been retired has
// `callers == 0` and is at a safepoint by this predicate, which is the right answer here and the wrong one
// for quiescence: it can execute no guest instruction, which is all SP-1 promises. T-5.4 asks for more, so
// `quiescentLocked` waits for the retirement and this does not.
//
// [0067]: ../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md
// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
func (w *world) atSafepointLocked() bool {
	for _, t := range w.live {
		if t.parked+t.blocked < t.callers {
			return false
		}
	}
	return true
}

// releaseIfAtSafepoint claims the waiting `Stop`'s channel when SP-1's predicate holds, and returns it for
// the caller to close **outside** `mu`. `mu` held; nil when there is nothing to release.
//
// `releaseIfQuiescent`'s shape exactly, for its two stated reasons: §4 B-MM-3 forbids holding an engine
// lock across a channel operation, and nil'ing the field under the lock makes exactly one caller able to
// observe a non-nil channel, so a double close is impossible by construction.
//
// **Four call sites, one per transition that can make the predicate newly true**, and the enumeration is
// the soundness argument rather than a list of conveniences: `parked` rises only in `parkAtSafepoint`,
// `blocked` only in `enterBlocked` (which parks immediately after, so this is evaluated on that path too),
// `callers` falls only in `leaveCall`, and `live` shrinks only in `retire`. `Stop` evaluates the predicate
// itself before installing the channel, under the same acquisition, so no site can satisfy it in the
// window before there is a channel to claim. The transitions that make it *less* true — `enterCall`,
// `unmarkBlocked`, `admit` — need no site, and `admit` is refused mid-round for its own reason.
//
// The `w.stopped != nil` guard is first, so the walk costs nothing when no `Stop` is waiting.
func (w *world) releaseIfAtSafepoint() chan struct{} {
	if w.stopped == nil || !w.atSafepointLocked() {
		return nil
	}
	stopped := w.stopped
	w.stopped = nil
	return stopped
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
// **It still sends no arrival, and it is now one of SP-1's four release sites** — [#656]'s exit arm, repaired
// by [ADR 0074][0074] without a message. The paragraph this replaces was right about the defect and right
// that it was not this function's to fix: a thread `Stop` had counted as a sender and that exited before its
// next safepoint never announced, so the round waited out its interval and reported a false expiry
// (**`ErrStopDeadline` after 2.0011575s for a thread that had already exited**), and sending here would have
// broken the other arm, because a protocol that counts *receives* lets an arrival on behalf of a thread the
// round did not await satisfy a still-running thread's slot. Both are dissolved by the wait becoming a
// predicate over the live set: the removal below **is** the transition, since a thread that has left `live`
// is no longer walked, and `releaseIfAtSafepoint` is where that is read. No arrival, no identity, and the
// two arms stop being in tension.
//
// [0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
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
	stopped := w.releaseIfAtSafepoint()
	w.mu.Unlock()

	if idle != nil {
		close(idle)
	}
	if stopped != nil {
		close(stopped)
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
// soundness argument** ([ADR 0068][0068]). `Stop` broadcasts `stopReq` over the membership it observed
// under this same mutex, so a member appended mid-round never receives the request: it would never park,
// and would run guest code inside a round the host believes is in effect — see `ErrStopInProgress`, whose
// older reason about arrival slots this replaces ([ADR 0074][0074]). Split into a read and a later append,
// the refusal would be advisory: a `Stop` could begin between them and the new thread would join a round
// whose broadcast has already happened.
//
// The complementary direction is `Stop`'s own `w.resume != nil` guard, so the two orderings are the
// only two: either this refuses, or `Stop` observes the new member from the start.
//
// [0068]: ../../docs/decisions/0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md
// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
func (w *world) admit(t *thread) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		// **§5 H-3's teardown refuses a new thread, where SP-1's pause merely delays one.** The two
		// refusals sit together and are not the same refusal: `ErrStopInProgress` below is about a
		// round's one-shot broadcast and is transient — the caller retries after `Resume` — while this one
		// is terminal, because `Close` has already cancelled every member and returned. A thread
		// admitted after that would be a member no `Close` will ever wait for, running guest code on
		// an instance the embedder believes is torn down. Checked here rather than only in `spawn` so
		// that the guarantee belongs to the same critical section as membership itself.
		return fmt.Errorf("%w: this instance is closed, so a new thread would outlive the teardown "+
			"that has already completed (contract §5 H-3)", ErrClosed)
	}
	if w.resume != nil {
		return fmt.Errorf("%w: this instance has %d threads at or heading for a safepoint, and the "+
			"round's stop request has already been broadcast, so a member admitted now would never "+
			"receive it", ErrStopInProgress, len(w.live))
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
// Returns nil when no caller of the instance is executing guest code, `ErrStopDeadline` when the interval
// expired first. On
// either outcome the world is *stopped* and `Resume` must be called: a partial stop still has threads
// parked, and leaving them parked is a hang rather than a degraded mode.
//
// **The wait is on a signal and not on a timer that is then re-checked.** The last agent to reach a
// safepoint closes `world.stopped`, and this function receives it, with the deadline as the competing case.
// Polling a counter with a sleep in between would report the stop's completion at the granularity of the
// sleep rather than of the event, and would make the bound this clause promises a property of the poll
// interval instead of the engine.
//
// **What is waited *for* is `atSafepointLocked`, not a number of announcements** — [ADR 0074][0074], the
// repair for #656. The paragraph this replaces said *"each running thread sends once as it parks, and this
// loop receives exactly as many times as there are such threads"*: both halves were true of *threads* and
// the sender is a **caller**, which made four shapes reachable, two of them a `nil` returned while guest
// code ran. The predicate is at `atSafepointLocked`; this function's part is to install the channel under
// the same acquisition that evaluates it, so that no agent can satisfy the predicate in a window where
// there is nothing to close.
//
// **A world already at a safepoint allocates neither channel nor timer**, which is the common case for an
// embedder stopping an idle instance, and is strictly cheaper than the protocol it replaces.
//
// # SP-2 is why a suspended caller is a *mark* and not a message, and SP-4 is why it is never woken
//
// §3 SP-2 makes a thread suspended in `memory.atomic.wait` count as *at a safepoint*, and SP-4 requires
// that a stop *"with N threads parked in host calls completes without waking them."* Together they forbid
// asking a suspended caller anything: it cannot answer, and waking it to make it able to is exactly what
// SP-4 rules out. So the suspension records itself on the way in — `enterBlocked` — and `atSafepointLocked`
// reads that mark under the same mutex the transition takes, so the two cannot interleave. Decision 0060's
// third choice, and `futex.go`'s `wait` is the other half.
//
// **This is the clause the token-counting protocol was reverse-engineered from, and getting it right for
// `blocked` while leaving the other terms as messages is what produced #656.** SP-2's inversion is the
// general case rather than the exception: *every* way a caller stops executing is a state the engine already
// records, so `parked` joins `blocked` and there is no direction left to invert.
//
// `stopReq` is still set on a blocked thread, which is what makes its wake path park: it clears
// `blocked` and polls before it pushes anything.
//
// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
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
	for _, t := range w.live {
		t.stopReq.Store(true)
		// Zeroed here because the count belongs to *this* round, and the round's start is the only
		// moment at which a previous round's parks stop counting — `w.resume` is what names the round,
		// and installing a new one is this line's occasion. The sentence this replaces cleared
		// `thread.reported` for the same reason (grave #593).
		//
		// **Nothing decrements it, and no other site clears it.** A caller released by `Resume` and
		// woken later could not decrement safely: the decrement would lag a `Stop` that had already
		// begun, and a stale `parked` reads as a caller at a safepoint — the false success this whole
		// decision is about, with a new cause. `Resume` and `Close` therefore leave the field alone
		// too, which is sound because `releaseIfAtSafepoint`'s `w.stopped != nil` guard means it is
		// never *read* outside a round.
		t.parked = 0
		// **The panic is 0067's and its subject is unchanged.** `blocked <= callers` holds by
		// construction — the only route into `enterBlocked` is guest code, which runs inside a call
		// that has already been counted — and the reason to assert it here is that the predicate rests
		// on it. What changed is the consequence of a violation: with `atSafepointLocked` reading
		// `>=`, a stray `blocked` no longer makes the wait unsatisfiable, so this is the check that
		// keeps an unpaired `enterBlocked`/`leaveCall` from silently *shortening* a round instead of
		// silently hanging it. Kept for the stronger of the two reasons rather than dropped with the
		// weaker one.
		if t.blocked > t.callers {
			w.mu.Unlock()
			panic(fmt.Sprintf("burroughs: %s has %d blocked callers of %d — a suspended caller is "+
				"by construction inside a counted call, so this is an unpaired enterBlocked or "+
				"leaveCall (decision 0067)", t, t.blocked, t.callers))
		}
	}
	// Evaluated and acted on under the acquisition that installed `w.resume`, which is what makes the
	// four release sites unable to miss their channel: any agent that satisfies the predicate after this
	// point must take `mu` to do it, and finds `w.stopped` already there.
	if w.atSafepointLocked() {
		w.mu.Unlock()
		return nil
	}
	w.stopped = make(chan struct{})
	// Captured under the lock and read from the local below. Reading `w.stopped` after the unlock would
	// be a plain read of a field the release sites nil, which is a data race on the field itself even
	// though every *channel* operation on the value is safe — the distinction that makes `-race` the
	// authority here rather than "channels are concurrency-safe".
	stopped := w.stopped
	w.mu.Unlock()

	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case <-stopped:
		return nil
	case <-timer.C:
	}
	// **The timer expiring is not the verdict, because both cases can be ready at once and `select`
	// picks arbitrarily between two ready cases.** A round that completed in the same instant the
	// interval ran out would otherwise be reported as an expiry — a false red of exactly the kind this
	// decision removes from the other direction. A closed channel is always ready, so this second,
	// non-blocking read is the tie-break, and it reads the *local* rather than the field: `Resume` and
	// `Close` nil `w.stopped` without closing it, so a round absorbed by either still reports the
	// expiry it reported before.
	select {
	case <-stopped:
		return nil
	default:
	}
	return w.stopExpired(deadline)
}

// stopExpired builds `ErrStopDeadline`'s diagnostic by walking the threads whose callers have not all
// reached a safepoint. Takes `mu`.
//
// **It names who is still running, where the form it replaces counted receives.** That message was
// *"%d of %d arrived within %s"*, and #656's C2 witness printed *"0 of 1 arrived within 3s"* about a caller
// that had already returned from the guest — a report whose subject did not exist, arrived at by counting
// announcements rather than by asking the world. The three marks are printed per thread because they are
// what an embedder or a bug report needs to tell the two remaining causes apart: a caller genuinely inside a
// long un-pollable stretch of guest code, against a mark left unpaired by an engine defect.
func (w *world) stopExpired(deadline time.Duration) error {
	w.mu.Lock()
	running := make([]string, 0, len(w.live))
	for _, t := range w.live {
		if t.parked+t.blocked >= t.callers {
			continue
		}
		running = append(running, fmt.Sprintf("%s has %d of %d callers at a safepoint (%d parked, "+
			"%d blocked)", t, t.parked+t.blocked, t.callers, t.parked, t.blocked))
	}
	total := len(w.live)
	w.mu.Unlock()

	if len(running) == 0 {
		// **Reachable, and it is the absorbed round rather than a lost wake.** `Resume` and `Close` nil
		// `w.stopped` without closing it, so a round either of them ends leaves this function's caller
		// waiting out its interval over a world that may by then be at a safepoint — or torn down. The
		// expiry is still the right outcome (`Stop`'s own closed-world refusal argues why a `nil` about a
		// torn-down world is worse), and saying *which* of the two happened is what keeps this from
		// reading as an engine defect with no site.
		return fmt.Errorf("%w: no caller of this instance's %d threads was executing guest code after "+
			"%s, so this round was ended by a Resume or a Close rather than by a safepoint",
			ErrStopDeadline, total, deadline)
	}
	return fmt.Errorf("%w: %d of %d threads still had a caller executing guest code after %s: %s",
		ErrStopDeadline, len(running), total, deadline, strings.Join(running, "; "))
}

// enterBlocked marks this thread as at a safepoint for the duration of a suspension it is about to
// enter, and parks first if a stop is already in progress. Contract §3 SP-2's guest half for
// `memory.atomic.wait`; decision 0060's third choice, called from `futex.go`'s `wait`.
//
// **The mark and the stop check are one critical section, which is what makes the three-way race a
// two-way one.** Either this runs first, and the `Stop` that follows reads the mark and counts this
// thread as stopped without waiting for it; or `Stop` runs first, and this thread finds a round in
// progress and parks through the ordinary route before suspending. The outcome that must not exist is the
// third: a `Stop` that observes neither the mark nor the park, which is a deadline expiry reported for a
// thread that is by definition not running.
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
		// **`counted` is true, and the parameter exists for this one call.** This caller has just added
		// itself to `blocked`, so it already holds one of its thread's terms in `atSafepointLocked`;
		// counting it in `parked` as well would let one caller satisfy two, and on a thread with a second
		// caller running guest code the sum would reach `callers` and the round would report a stopped
		// world — #656's C1 witness rebuilt out of its own repair. See `parkAtSafepoint`.
		t.parkAtSafepoint(true)
	}
}

// leaveBlocked clears the mark and polls, in that order, before the caller pushes anything.
//
// **The poll is what SP-2's second half asks for**: a thread that leaves a wait *"cannot touch guest
// memory until it re-enters through a boundary that observes the stop"*, and the wake is that boundary.
// A stop in progress at this moment parks this thread here, which is where the caller's term in
// `atSafepointLocked` moves from `blocked` to `parked`.
//
// Clearing before polling and not after: with the mark still set, a `Stop` racing this would count the
// thread as being at a safepoint it has just left.
//
// **The two steps are not one critical section, and the window between them is safe rather than
// tolerated.** For the instant after `unmarkBlocked` and before `parkAtSafepoint` increments `parked`, this
// caller holds no term and `atSafepointLocked` reads false for its thread. It executes no guest
// instruction there — that is exactly what this ordering exists to guarantee — so the only effect is that a
// round waiting on the predicate is not satisfied *yet*, and the park that follows satisfies it. A round
// that had already completed is unaffected: `w.stopped` is nil by then, so nothing re-reads the predicate.
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
// not. `Stop` now asks `parked+blocked >= callers`, and this is where the right-hand side comes from.
//
// # Three things it deliberately does not do
//
// **It does not park.** `enterBlocked` parks, because a thread about to suspend must reach a safepoint
// before it becomes unable to; a thread about to *run* is already covered — `run` calls `enterFrame`,
// whose first statement is `st.t.poll()`, so a caller that arrives while a stop is in flight parks at
// frame entry before executing one guest instruction. Adding a park here would fence twice for one edge
// and put a second release site on the `Invoke` path.
//
// **`enterCall` is not a release site at all, and the reason is the direction it moves the predicate.**
// It increments `callers`, which can only make `parked+blocked >= callers` *less* true — and a transition
// that cannot newly satisfy a condition has nothing to signal. [ADR 0074][0074]'s four sites are exactly
// the four that raise a left-hand term or lower the right one; this pair's other half, `leaveCall`, is one
// of them for that reason and this half is none.
//
// **It does not wrap the whole of `invokeIndex`, and the placement is load-bearing rather than tidy.**
// `invokeIndex` delegates for a re-exported import by returning `ext.owner.invokeIndex(...)`, so exactly
// one `in.run` executes per chain — but the *delegating* instance has its own `thread`. Counting on entry
// to `invokeIndex` would leave that thread with a caller counted and no guest code running on it, which
// makes `parked+blocked >= callers` false for a thread that will never poll and never park: every `Stop`
// on that instance would wait out its full deadline. So the pair wraps `in.run` and nothing wider.
//
// **It does not wrap instantiate-time execution.** `build`'s start function and `runConst` run before
// `InstantiateLinked` returns, so no external reference to the instance exists and no `Stop` can be in
// flight against it. That is the only guest code that runs outside `Invoke`, and it is named here rather
// than left as a silent asymmetry between this pair and the `enterGuest`/`leaveGuest` one.
//
// A nil thread or a nil world is a no-op, on `poll`'s ground: a thread with no world has no `Stop` that
// could be walking it.
//
// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
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
//
// **And it is one of the four release sites for SP-1's own predicate, which is #656's C2 repair** ([ADR
// 0074][0074]). A caller that runs to the end of its function reaches a safepoint by *leaving*, and the old
// arrival protocol had no message for that: one `Invoke` of an un-pollable body and one `Stop` waited out
// its whole interval and reported *"0 of 1 arrived within 3s"* about a caller that had already returned —
// measured on an unshared memory with no atomics, no `Spawn` and no gate, so it was reachable on the
// default engine. The decrement above is the transition; this is where it is read. Same nil test, same
// reason.
//
// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
func (t *thread) leaveCall() {
	if t == nil || t.w == nil {
		return
	}
	w := t.w

	w.mu.Lock()
	t.callers--
	idle := w.releaseIfQuiescent()
	stopped := w.releaseIfAtSafepoint()
	w.mu.Unlock()

	if idle != nil {
		close(idle)
	}
	if stopped != nil {
		close(stopped)
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
	// Nil'd and **not closed**, which is the difference between ending a round and completing one. A
	// `Stop` still waiting on this channel must not be told the world reached a safepoint by a `Resume`
	// that has just put every parked thread back on the dispatch loop; it reaches its deadline and says so,
	// naming this case (`stopExpired`). `thread.parked` is deliberately not cleared here either — the round
	// that installs the next channel clears it, and nothing reads it in between (`Stop`).
	w.stopped = nil
	w.mu.Unlock()

	close(release)
}

// parkAtSafepoint marks this caller as parked and blocks until the world resumes. Contract §3 SP-1's guest
// half.
//
// **`counted` says this caller's term in `atSafepointLocked` is already held by `blocked`**, which is true
// for exactly one caller — `enterBlocked`'s, which increments `blocked` and then parks. Without the
// distinction one caller would hold two of its thread's terms, and on a thread whose second caller is
// running guest code the sum would reach `callers` and the round would report a stopped world: #656's C1
// witness rebuilt out of its own repair. The parameter is what makes the predicate exact; the `>=` in it is
// a separate hedge against an unpaired mark, argued there.
//
// Called from `poll` with `counted` false, and only once `stopReq` has been observed set. The re-read of `w.resume`
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
// `Resume`'s caller made before calling it; the close of `w.stopped` synchronizes the other direction, so
// the host whose `Stop` returned observes the guest's writes before it.
//
// **The guest→host half is now the mutex chain rather than this thread's own send**, and it is the same
// edge reached by one more hop. A parking caller's writes precede its release of `w.mu`; every later
// release site acquires that same mutex, so the site that finally closes `w.stopped` — which may be another
// thread, or `leaveCall`, or `retire` — releases after every earlier parker. `Stop`'s receive synchronizes
// with that close, and transitively with all of them. Worth stating rather than assuming, because the
// protocol this replaced had the edge per sender and by inspection. That is B-MM-1 in both
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
func (t *thread) parkAtSafepoint(counted bool) {
	w := t.w
	if w == nil {
		return
	}

	if t.exitReq.Load() {
		t.terminate()
	}

	w.mu.Lock()
	release := w.resume
	if release == nil {
		w.mu.Unlock()
		return
	}
	if !counted {
		t.parked++
	}
	// **Grave #593 is dissolved here rather than guarded here**, which is why the dedup this replaces is
	// gone. That grave was *"the one deadlock this protocol can have"*: the announcement was a send on a
	// buffer sized from the thread count, the announcer is a **caller**, and the third of three concurrent
	// `Invoke`s parked by one `Stop` blocked on the send forever with `Resume` unable to free it. The
	// repair was one announcement per thread per round (`thread.reported`) — which is also what let a
	// sibling caller's announcement satisfy a runner's slot, #656's C1. There is no send now, so neither
	// the hang nor the dedup that caused the false success has a subject.
	stopped := w.releaseIfAtSafepoint()
	w.mu.Unlock()

	// Both channel operations are outside the lock, for the two halves of B-MM-3. The receive is the
	// obvious one: blocking on a resume while holding an engine lock is the clause's own hazard, and
	// `Resume` needs `mu` to run at all, so it would be a deadlock and not merely a violation. The close
	// is outside because the clause names a *guest resume* and a `Stop` returning is what precedes one —
	// and because `releaseIfAtSafepoint` claimed the channel under the lock, which is what makes exactly
	// one closer possible.
	//
	// The order is close-then-wait and not the reverse, which is the whole of this function's liveness: a
	// caller that waited first would be the last agent to reach a safepoint, holding the news of it.
	if stopped != nil {
		close(stopped)
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
	// `counted` false: a caller reaching a back-edge or a frame entry holds no `blocked` mark, so its term
	// in `atSafepointLocked` is the `parked` one this is about to take. See `parkAtSafepoint`.
	t.parkAtSafepoint(false)
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
