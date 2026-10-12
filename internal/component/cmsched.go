// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The component-model Thread scheduler and its one handoff primitive (ADR 0100's amendment, "the handoff").
//
// # What the reference does, which this implements
//
// The unit the reference resumes is a CHAIN, not a Thread. `Thread.resume` loops
//
//	while thread is not None:
//	  (thread.cont, switch_to) = resume(cont, thread)
//	  thread = switch_to
//
// and every way of giving control up — `block()`, `switch_to(other)`, a returning function — goes through
// `suspend(switch_to)`, which hands the resumer the Thread to run next or nothing. So a resumer gets control
// back when the chain ends, not when one Thread exits.
//
// # One scheduler goroutine per instance, and why
//
// The amendment's ruling: **exactly one driver, whose identity never changes.** Every Thread, the implicit
// one included, gives control up by [cmScheduler.giveBack] and parks; the scheduler is the only goroutine
// that resumes one. The design it replaced made the implicit Thread its own driver when it suspended
// inside the callee, and could not express two implicit Threads from two tasks on one instance: a chain
// would reach a goroutine parked in ANOTHER task's driver loop.
//
// The scheduler **runs no guest code** and is not a T-1 agent, so neither T-1 nor T-6 applies to it. It is
// created lazily, on the first `thread.new-indirect` an instance executes, so an instance that never uses
// 🧵 pays for no goroutine and no channel.
//
// # The entry slot is the CHAIN's, and the scheduler never takes it
//
// Whoever starts a chain already holds `asyncHandles.entrySem`: the implicit Thread, which is inside its
// callee, or the YIELD arm, which takes the slot before handing off ([threadTable.yieldPoint]). The hold
// is the chain's for its whole run, and a resumed Thread runs under it rather than taking the slot itself.
// That is the reference's `exclusive_thread`, which stays the implicit Thread's while it is suspended
// mid-callee and is never taken by a Thread a chain resumes.
//
// **The scheduler does not acquire the slot itself, and that is a refinement of the ruling's wording, not
// a departure from its intent.** A scheduler that acquired it could block on a slot held by an implicit
// Thread parked mid-callee — which only the scheduler can resume.
//
// # Resuming an implicit Thread ENDS the chain, as far as the scheduler is concerned
//
// The implicit Thread returns to its own goroutine, which may leave the callee and go back to the
// callback loop without ever giving back again. A scheduler still waiting for its `giveBack` would then
// miss the YIELD arm's next request. So the scheduler resumes it and goes idle; if it gives back later,
// that starts a new chain from idle, which is the same thing seen from the guest.
type cmScheduler struct {
	h  *asyncHandles
	tt *threadTable

	// back carries every handoff: a Thread giving the slot up, or a created Thread's entry returning.
	// The scheduler is the only receiver, in both its idle loop and a running chain.
	back chan handoff
	// yield carries the YIELD arm's request to run the ready queue; the reply is the chain's error.
	yield chan chan error
	stop  chan struct{} // closed by `walker.close`, LAST, to end the goroutine
	done  chan struct{} // closed when the goroutine has exited
}

// handoff is one `suspend(switch_to)`: who gave control up, who runs next (nil for none), and — when the
// giver is a created Thread whose entry returned — its error.
type handoff struct {
	from   *cmThread
	next   *cmThread
	exited bool
	err    error
}

// errThreadKilled is what a parked Thread's `giveBack` returns when the wake ends it. It is terminal: the
// guest unwinds through the canon call, and a created Thread's goroutine exits without reporting back,
// because nothing is waiting for it.
var errThreadKilled = errors.New("component: the component-model Thread was ended while parked, by a trap in its task or by Close")

// scheduler returns the instance's scheduler, starting it on first use.
func (tt *threadTable) scheduler(h *asyncHandles) *cmScheduler {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if tt.sched == nil {
		s := &cmScheduler{
			h: h, tt: tt,
			back:  make(chan handoff),
			yield: make(chan chan error),
			stop:  make(chan struct{}),
			done:  make(chan struct{}),
		}
		tt.sched = s
		go s.loop()
	}
	return tt.sched
}

// loop is the scheduler goroutine: idle until a Thread gives back from outside any chain (an implicit
// Thread inside its callee) or the YIELD arm asks for the ready queue to run; then drive a chain.
func (s *cmScheduler) loop() {
	defer close(s.done)
	for {
		select {
		case hb := <-s.back:
			// A handoff with no chain running can only come from an implicit Thread: a created Thread runs
			// only when this goroutine resumes it, which is inside `drive`. So `hb.from` is the origin, and
			// it is parked in `giveBack` until a chain resumes it or the chain's error does.
			if deliver, err := s.drive(hb.from, hb.next); deliver {
				hb.from.resume <- err
			}
		case reply := <-s.yield:
			_, err := s.drive(nil, nil)
			reply <- err
		case <-s.stop:
			return
		}
	}
}

// drive runs one chain starting at `first`, or at the head of the ready queue when `first` is nil, and
// continues with each handoff's `next` — the reference's `while thread is not None` — then with the ready
// queue, until nothing is runnable. It returns early, with no error, when it resumes an implicit Thread.
//
// A created Thread whose entry TRAPPED ends the chain: its task's other parked Threads are ended, and the
// error goes back to whoever started the chain. `deliver` reports that `origin` — the implicit Thread that
// started it, nil for the YIELD arm — was claimed for that error and must be sent it.
//
// **The origin is claimed BEFORE the kill**, because when it belongs to the trapping task it is one of the
// parked Threads the kill would end, and the lift would then report "ended while parked" instead of the
// trap that caused it.
func (s *cmScheduler) drive(origin, first *cmThread) (deliver bool, err error) {
	cur := first
	for {
		if cur == nil {
			cur = s.tt.popReady()
		} else if !s.tt.claim(cur) {
			cur = nil // ended by the wake between the handoff and here
			continue
		}
		if cur == nil {
			return false, nil
		}
		s.h.mu.Lock()
		s.h.lift = cur.task
		s.h.mu.Unlock()
		cur.resume <- nil // capacity 1, and only this goroutine sends
		if cur.implicit {
			return false, nil
		}
		var hb handoff
		select {
		case hb = <-s.back:
		case <-s.stop:
			return false, nil
		}
		if hb.exited && hb.err != nil {
			deliver = origin != nil && s.tt.claim(origin)
			for _, k := range s.tt.killParked(hb.from.task) {
				if k.done != nil {
					<-k.done
				}
			}
			return deliver, hb.err
		}
		cur = hb.next
	}
}

// giveBack is the one handoff primitive: `self` gives the entry slot up, `next` runs (or the ready queue
// does, when it is nil), and `self` parks until a chain resumes it or the wake ends it.
//
// `state` is what `self` becomes — threadSuspended, or threadReady for the `yield-*` forms, which also
// put it at the back of the ready queue. Set under the table's lock BEFORE the handoff is sent, so a Thread
// the chain runs can already `resume-later` it.
//
// **The park runs inside `c.Blocking`, and SP-2 depends on it.** A 🧵 built-in is a `CanonFunc` that
// `interp.Instance.callAdapter` dispatches, and callAdapter does NOT mark its thread blocked; only
// `interp.CanonCaller.Blocking` (and `callHost`, and futex `wait`) reach `enterBlocked`. Without it a `Stop`
// would wait forever on a caller that is neither at a safepoint nor counted blocked. `waitable-set.wait`
// parks the same way.
func (s *cmScheduler) giveBack(c *interp.CanonCaller, self, next *cmThread, state threadState) error {
	s.tt.park(self, state)
	return c.Blocking(func() error {
		select {
		case s.back <- handoff{from: self, next: next}:
		case <-s.stop:
			return errThreadKilled
		}
		select {
		case err := <-self.resume:
			return err
		case <-self.kill:
			return errThreadKilled
		}
	})
}

// park records `self` giving the slot up: its state, its place in the ready queue for a yield, and the
// channels a resume or the wake reaches it by. An implicit Thread's channels are made here, on its first
// handoff, so a lift that never uses 🧵 allocates none.
func (tt *threadTable) park(self *cmThread, state threadState) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if self.resume == nil {
		self.resume = make(chan error, 1)
		self.kill = make(chan struct{})
	}
	self.state = state
	if state == threadReady {
		tt.ready = append(tt.ready, self)
	}
}

// claim moves a parked Thread to running, for the scheduler about to resume it. False when the Thread is
// no longer parked — the wake ended it first — so the scheduler never sends to a Thread nobody will read.
// A Thread claimed out of the ready queue is removed from it, by identity.
func (tt *threadTable) claim(t *cmThread) bool {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	switch t.state {
	case threadSuspended:
	case threadReady:
		tt.unreadyLocked(t)
	default:
		return false
	}
	t.state = threadRunning
	return true
}

// takeReady is `promote`'s `if other.ready(): other.stop_waiting()`: if `t` is in the ready queue, remove it
// by identity and report true, leaving it SUSPENDED so the handoff that follows resumes it like any
// suspended Thread.
func (tt *threadTable) takeReady(t *cmThread) bool {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if t.state != threadReady {
		return false
	}
	tt.unreadyLocked(t)
	t.state = threadSuspended
	return true
}

func (tt *threadTable) unreadyLocked(t *cmThread) {
	for i, r := range tt.ready {
		if r == t {
			tt.ready = append(tt.ready[:i], tt.ready[i+1:]...)
			return
		}
	}
}

// yieldPoint is the callback loop's YIELD arm as a scheduling point: take the entry slot for `task`, hand
// the ready queue to the scheduler, and wait for the chain to end.
//
// The reference's YIELD arm is
//
//	inst.exclusive_thread = None
//	thread.wait_until(lambda: inst.exclusive_thread is None)
//
// and `Thread.wait_until` either returns at once (half the time, `random.randint(0,1)`) or starts waiting
// and blocks so `Store.tick` can `random.choice` among ready candidates. **This takes the blocking branch
// every time and runs the ready queue FIFO**: both are choices the reference permits, and
// `cmthreads/spawn.wat` tolerates either branch by re-YIELDing until its body has run.
//
// It replaces the second build slice's `runReady`, which waited for each Thread's goroutine to EXIT and so
// deadlocked on the first Thread that suspended partway: its `done` could close only after a resume that
// only the blocked `runReady` could have issued.
//
// Nothing to do, and no slot taken, when no Thread is ready — including on every instance that has never
// executed `thread.new-indirect`, which has no scheduler to ask.
func (tt *threadTable) yieldPoint(h *asyncHandles, task *liftTask) error {
	tt.mu.Lock()
	s, n := tt.sched, len(tt.ready)
	tt.mu.Unlock()
	if s == nil || n == 0 {
		return nil
	}
	prev, ok := h.enterTask(task)
	if !ok {
		return fmt.Errorf("%w: the call's context ended while waiting for this instance's execution "+
			"slot", ErrCancelled)
	}
	defer h.leaveTask(prev)
	// The chain executes guest code, so it runs under the run slot too, taken after the entry slot
	// (ADR 0101). The scheduler takes neither: the hold is the chain's.
	h.acquireRun(task.threadIdx, nil)
	defer h.releaseRun()
	reply := make(chan error, 1)
	select {
	case s.yield <- reply:
	case <-s.stop:
		return errThreadKilled
	}
	return <-reply
}

// stopScheduler ends the scheduler goroutine and waits for it. Called by `walker.close` LAST — after every
// parked Thread has been woken and the core instances closed — so the scheduler is never left waiting on a
// handoff that will not come, and never outlives the instance.
func (tt *threadTable) stopScheduler() {
	tt.mu.Lock()
	s := tt.sched
	tt.mu.Unlock()
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
}
