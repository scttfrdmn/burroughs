// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
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
// touch a given memory. What makes this the right scope *today* is that `Spawn` is parked (see
// `thread`), so an instance has exactly one thread and "every thread of this instance" and "every
// thread that can reach this memory" name the same set. They stop naming the same set the moment
// spawn unparks across instances, which is #515's own SP-4 work and is tracked there rather than
// implied to be handled here.
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

	// members is every thread of this instance. One entry today, because spawn is parked; the slice
	// rather than a single field is what lets `Stop`'s arrival count be a count rather than a bool,
	// so SP-4's N-thread case changes the population and not the protocol.
	members []*thread
}

// register adds a thread to the world. Called once per thread at creation.
func (w *world) register(t *thread) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.members = append(w.members, t)
	t.w = w
}

// Stop is contract §3 SP-1's host request: bring every guest thread of this instance to a safepoint,
// within `deadline`.
//
// Returns nil when every thread has arrived, `ErrStopDeadline` when the interval expired first. On
// either outcome the world is *stopped* and `Resume` must be called: a partial stop still has threads
// parked, and leaving them parked is a hang rather than a degraded mode.
//
// **The wait is on the arrival signal and not on a timer that is then re-checked.** Each thread sends
// once as it parks, and this loop receives exactly as many times as there are threads, with the
// deadline as the competing case. Polling an arrival counter with a sleep in between would report the
// stop's completion at the granularity of the sleep rather than of the event, and would make the
// bound this clause promises a property of the poll interval instead of the engine.
func (in *Instance) Stop(deadline time.Duration) error {
	w := &in.world

	w.mu.Lock()
	if w.resume != nil {
		w.mu.Unlock()
		return errors.New("burroughs: Stop called while a stop is already in progress")
	}
	w.resume = make(chan struct{})
	w.arrived = make(chan ThreadID, len(w.members))
	// Captured under the lock and read from the local below. Reading `w.arrived` after the unlock
	// would be a plain read of a field `Resume` nils, which is a data race on the field itself even
	// though every *channel* operation on it is safe — the distinction that makes `-race` the
	// authority here rather than "channels are concurrency-safe".
	arrived, n := w.arrived, len(w.members)
	for _, t := range w.members {
		t.stopReq.Store(true)
	}
	w.mu.Unlock()

	timer := time.NewTimer(deadline)
	defer timer.Stop()
	// `i` and not `len(arrived)`: a channel's length is its buffer's current occupancy, which this
	// loop has been *draining*, so `len` would report what is still waiting rather than what has
	// arrived — plausibly, and wrong in the direction that under-reports a partial stop. The loop
	// index counts completed receives, which is the quantity the message claims.
	for i := range n {
		select {
		case <-arrived:
		case <-timer.C:
			return fmt.Errorf("%w: %d of %d arrived within %s", ErrStopDeadline, i, n, deadline)
		}
	}
	return nil
}

// Resume releases every thread parked by `Stop` and clears the request.
//
// Safe to call after a `Stop` that returned `ErrStopDeadline`: the threads that did arrive are
// released and the ones that had not yet seen the request stop seeing it. Calling it without a stop in
// progress is a no-op rather than an error, because the deadline case makes "did the stop succeed"
// the wrong question for a caller to have to answer before cleaning up.
func (in *Instance) Resume() {
	w := &in.world

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.resume == nil {
		return
	}
	for _, t := range w.members {
		t.stopReq.Store(false)
	}
	close(w.resume)
	w.resume = nil
	w.arrived = nil
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
// **No error return, and that is a decision rather than an omission** — see `poll`.
func (t *thread) parkAtSafepoint() {
	w := t.w
	if w == nil {
		return
	}

	w.mu.Lock()
	release := w.resume
	if release == nil {
		w.mu.Unlock()
		return
	}
	w.arrived <- t.id
	w.mu.Unlock()

	<-release
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
