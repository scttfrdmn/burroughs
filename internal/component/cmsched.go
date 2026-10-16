// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"

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
// # Two locks, and since ADR 0101's Option A the scheduler holds one of them between Threads
//
// **`asyncHandles.entrySem` is the reference's `exclusive_thread`, and no Thread a chain resumes takes it.**
// An implicit Thread suspended mid-callee keeps it, as the reference's `exclusive_thread` stays set across
// a `block()`. The YIELD arm does NOT take it, because the reference has cleared `exclusive_thread` by then
// (`canon_lift`: `inst.exclusive_thread = None` on every callback return); until Option A
// [threadTable.yieldPoint] re-took it.
//
// **The run slot is held exactly by the Thread executing guest code**, re-entrantly for that Thread's own
// synchronous nesting (ADR 0101's Option A). Every handoff carries it:
//   - a Thread sending on `back` holds it;
//   - the scheduler holds it from the receive until it gives it to the next Thread (`giveTo`) or frees it
//     (`free`);
//   - when the slot is freed with Threads ready, the scheduler is kicked (`tick`) and acquires it itself.
//
// Before Option A the scheduler never took the slot. That was right while a parked implicit Thread held it,
// because a scheduler waiting on it could have waited on a Thread only it could resume. Under A no parked
// Thread holds the slot, so the scheduler waits only on one that is executing. And its acquire also
// receives `back`, so a Thread handing the slot over is never stuck behind it.
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
	// yield carries the YIELD arm's request to run the ready queue. The reply is sent once the chain has
	// ended, and is nil, or errThreadKilled when `stop` closed first.
	yield chan chan error
	// tick is the `Store.tick` equivalent: kicked, capacity 1 and coalescing, when the run slot is freed with
	// Threads ready (ADR 0101's Option A, point 5).
	tick chan struct{}
	stop chan struct{} // closed by `walker.close`, LAST, to end the goroutine
	done chan struct{} // closed when the goroutine has exited
}

// handoff is one `suspend(switch_to)`: who gave control up, who runs next (nil for none), and — when the
// giver is a created Thread whose entry returned — its error. **It carries the run slot** (ADR 0101's Option
// A). `detach` marks a Thread that has begun to WAIT rather than parked in `giveBack`: it is not
// resumed by a chain, and takes the slot back itself once its event arrives.
type handoff struct {
	from   *cmThread
	next   *cmThread
	exited bool
	detach bool
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
			tick:  make(chan struct{}, 1),
			stop:  make(chan struct{}),
			done:  make(chan struct{}),
		}
		tt.sched = s
		go s.loop()
	}
	return tt.sched
}

// loop is the scheduler goroutine. It is idle until one of three things happens:
//   - a Thread hands the slot over from outside any chain (`back`);
//   - the YIELD arm asks for the ready queue to run (`yield`);
//   - a freed slot finds Threads ready (`tick`).
//
// Then it drives a chain. **Every `back` carries the run slot** (ADR 0101's Option A): its sender held the
// slot when it sent, and the scheduler holds it from the receive until it gives it to a Thread or frees it.
func (s *cmScheduler) loop() {
	defer close(s.done)
	for {
		select {
		case hb := <-s.back:
			s.serve(hb)
		case reply := <-s.yield:
			s.acquireAndRun([]chan error{reply})
		case <-s.tick:
			s.acquireAndRun(nil)
		case <-s.stop:
			return
		}
	}
}

// acquireAndRun takes the run slot for the ready queue and runs it, then answers every YIELD request it was
// holding. That is the answer to a `tick` or a YIELD arm (ADR 0101's Option A, points 3 and 5).
//
// **The acquire is a `select`, not a bare channel send.** It receives:
//   - `back`, because an executing Thread may hand the slot over instead of freeing it, and a scheduler
//     blocked on the slot while that Thread blocked on `back` would deadlock;
//   - `yield`, so a YIELD request arriving meanwhile is answered by this same chain;
//   - `stop`.
//
// It takes no context and never waits on `entrySem`.
//
// **A stale tick costs one acquire and nothing else.** One that arrives after the queue was drained finds
// it empty, and `free` does not kick.
func (s *cmScheduler) acquireAndRun(replies []chan error) {
	answer := func(err error) {
		for _, r := range replies {
			r <- err // capacity 1, and each request is answered exactly once
		}
	}
	for {
		select {
		case s.h.runSlot <- struct{}{}:
			s.h.mu.Lock()
			s.h.schedHolds = true
			s.h.mu.Unlock()
			// **While a promoted task has not yet re-entered, the queue does not run** (#1012). The promote
			// was `switch_to` that task's callback, and a stale tick or a request that wins the slot first
			// must not resume the promoter ahead of it. It is answered, and the slot freed for the re-entry.
			s.tt.mu.Lock()
			awaiting := s.tt.promotedWait != nil
			s.tt.mu.Unlock()
			if awaiting {
				s.free()
				answer(nil)
				return
			}
			if s.drive(nil) {
				s.tt.syncTail(s.h) // a blocked sync task with no candidate traps here (#1005)
				s.free()
			}
			answer(nil)
			return
		case hb := <-s.back:
			s.serve(hb)
			answer(nil)
			return
		case r := <-s.yield:
			replies = append(replies, r)
		case <-s.stop:
			answer(errThreadKilled)
			return
		}
	}
}

// serve takes a handoff, and with it the run slot, and runs the chain it starts: the handoff's `next`, then
// the ready queue.
//
// The giver is one of:
//   - a Thread parked in `giveBack`;
//   - a Thread that began to wait (`detach`);
//   - a created Thread whose entry returned (`exited`).
func (s *cmScheduler) serve(hb handoff) {
	var first *cmThread
	if hb.exited && hb.err != nil {
		if s.trap(hb.from, hb.err) {
			return // the owner was resumed with the trap, and holds the slot
		}
	} else {
		first = hb.next
	}
	if !s.drive(first) {
		return
	}
	if !hb.detach && !hb.exited && hb.from.implicit && hb.from.task == nil && s.tt.claim(hb.from) {
		// **The sync branch (#970).** A SYNC lift's implicit Thread suspended, and the chain ended with
		// nothing runnable while it is still parked. The reference's sync tail is
		//
		//	while task.state != Task.State.RESOLVED:
		//	  candidates = { t for t in inst.threads if t.ready() }
		//	  trap_if(not candidates)
		//
		// so it traps. An ASYNC origin does not, because its callee simply does not progress until something
		// resumes it, which ADR 0100 already records. The claim makes the trap and a late resume mutually
		// exclusive: whichever moves the Thread to running first wins. The slot goes with the trap, and the
		// Thread unwinds through its entry's release.
		//
		// NEUTER THIS to see `sync-trap.wasm` hang instead of trapping, as an async callee would.
		s.giveTo(hb.from)
		hb.from.resume <- &interp.Trap{Reason: "cannot block a synchronous task before returning: " +
			"it suspended with no other Thread ready to run (the reference's sync branch, " +
			"trap_if(not candidates))"}
		return
	}
	s.tt.syncTail(s.h) // a blocked sync task with no candidate traps here (#1005)
	s.free()
}

// drive runs one chain starting at `first`, or at the head of the ready queue when `first` is nil, and
// continues with each handoff's `next` — the reference's `while thread is not None` — then with the ready
// queue, until nothing is runnable. The scheduler holds the run slot on entry.
//
// `idle` reports how the chain ended:
//   - **true:** nothing was runnable, and the scheduler still holds the slot for the caller to free or
//     hand on.
//   - **false:** it resumed an implicit Thread, routed a trap to a parked owner, or `stop` closed. In
//     each case the slot is no longer the scheduler's.
//
// **A created Thread's trap is routed to its OWNER, not to whoever started the chain** (ADR 0101's Option
// A, point 7). See `trap`. The chain then continues with the ready queue, because other tasks' Threads are
// not ended by it: a divergence recorded on #999.
func (s *cmScheduler) drive(first *cmThread) (idle bool) {
	cur := first
	for {
		if cur != nil && s.tt.promoteYielding(cur) {
			return true // a promote switched to a YIELDing task's callback: the chain ends here (#1012)
		}
		if cur == nil {
			cur = s.tt.popReady()
		} else if !s.tt.claim(cur) {
			cur = nil // ended by the wake between the handoff and here
			continue
		}
		if cur == nil {
			return true
		}
		s.giveTo(cur)
		cur.resume <- nil // capacity 1, and only this goroutine sends
		if cur.implicit {
			return false
		}
		var hb handoff
		select {
		case hb = <-s.back:
		case <-s.stop:
			return false
		}
		if hb.exited && hb.err != nil {
			if s.trap(hb.from, hb.err) {
				return false
			}
			cur = nil
			continue
		}
		cur = hb.next
	}
}

// trap routes a created Thread's trap to its owner (ADR 0101's Option A, point 7) and ends the owner's
// other Threads. It reports true when it resumed a parked owner with the trap, which then holds the slot.
//
// **The owner is claimed BEFORE the kill**, because a parked owner is one of the Threads the kill would end,
// and its lift would then report "ended while parked" instead of the trap that caused it.
//
// An owner that is not parked gets the trap through `routeFault`:
//   - an owner waiting has its wait aborted with the trap;
//   - an owner in its callback loop finds it at the loop's next pass;
//   - an owner whose lift has returned has it kept in `orphanTraps`.
func (s *cmScheduler) trap(from *cmThread, err error) bool {
	owner := from.owner
	if owner != nil && s.tt.claim(owner) {
		for _, k := range s.tt.killParked(owner) {
			if k.done != nil {
				<-k.done
			}
		}
		s.giveTo(owner)
		owner.resume <- err
		return true
	}
	s.tt.routeFault(owner, err)
	for _, k := range s.tt.killParked(owner) {
		if k.done != nil {
			<-k.done
		}
	}
	return false
}

// giveTo hands the scheduler's run slot to `t`, which is about to run: its saved chain and depth (its own
// chain at depth 1 on a first resume) and its task. Under `asyncHandles.mu`, with the resume sent after.
func (s *cmScheduler) giveTo(t *cmThread) {
	s.h.mu.Lock()
	ch, depth := t.saved.chain, t.saved.depth
	if ch == nil {
		ch, depth = t.chain, 1
	}
	s.h.runChain, s.h.runDepth = ch, depth
	s.h.lift = t.task
	s.h.schedHolds = false
	t.saved.chain, t.saved.depth, t.saved.lift = nil, 0, nil
	s.h.mu.Unlock()
}

// free gives up the scheduler's run slot when nothing is runnable. **It does not kick**: the scheduler has
// just drained the queue while holding the slot, so a kick here could only re-arm a tick for nothing.
func (s *cmScheduler) free() {
	s.h.mu.Lock()
	s.h.schedHolds = false
	s.h.mu.Unlock()
	<-s.h.runSlot
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
	// **The handoff carries the run slot** (ADR 0101's Option A, point 2): `self` saves the chain, depth
	// and task it holds, and the scheduler holds the slot from the receive until it gives it on. That
	// includes giving it back to `self` with exactly this state (`giveTo`), so a resume is never a contended
	// acquire: nothing runs between `switch_to(other)` and `other` running. A Thread the wake ENDS while
	// parked is never given the slot back, and unwinds holding nothing (`releaseRunFor`).
	s.h.mu.Lock()
	self.saved.chain, self.saved.depth, self.saved.lift = s.h.runChain, s.h.runDepth, s.h.lift
	s.h.runChain, s.h.runDepth = nil, 0
	s.h.schedHolds = true
	s.h.mu.Unlock()
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
		tt.mu.Unlock()
		tt.assertSlotHeld("park") // the giver still holds the slot: park runs before the handoff
		tt.mu.Lock()
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
	// **An implicit Thread waiting at its YIELD arm is ready too** (#1012). The reference's arm is
	// `wait_until(lambda: inst.exclusive_thread is None)`, so `other.ready()` holds while no task holds
	// `exclusive_thread`, which in Burroughs is a free `entrySem`. It is not in the queue, so nothing is
	// removed; the handoff that follows promotes it (`promoteYielding`).
	if t.implicit && t.yielding {
		return tt.h != nil && len(tt.h.entrySem) == 0
	}
	if t.state != threadReady {
		return false
	}
	tt.unreadyLocked(t)
	t.state = threadSuspended
	return true
}

// entering records that task Thread i is starting an entry (#1012): it is no longer at a YIELD arm, and if a
// promote was waiting for it to re-enter, the wait is over and the queue may run again.
func (tt *threadTable) entering(i uint32) {
	t := tt.get(i)
	if t == nil {
		return
	}
	tt.mu.Lock()
	defer tt.mu.Unlock()
	t.yielding = false
	if tt.promotedWait == t {
		tt.promotedWait = nil
	}
}

// promoteYielding switches to an implicit Thread that waits at its YIELD arm (#1012): it signals
// `yieldPoint` to return, and reports true. The caller ends its chain, because control now belongs to that
// task's callback, which runs before the promoter resumes. The promoter is already ready in the queue (the
// `yield-*` forms park it there), so it runs at the next scheduling point.
func (tt *threadTable) promoteYielding(t *cmThread) bool {
	tt.mu.Lock()
	if !t.implicit || !t.yielding || t.promoted == nil {
		tt.mu.Unlock()
		return false
	}
	ch := t.promoted
	// Until that task re-enters, the queue does not run: control is its callback's (`switch_to`), and a
	// stale tick that wins the slot meanwhile must not resume the promoter first. Measured: before this,
	// 45 of 100 runs of `promote-implicit` read 17 after a successful promote.
	tt.promotedWait = t
	tt.mu.Unlock()
	// The signal after the unlock (§4 B-MM-3): capacity 1 and non-blocking, so a duplicate is dropped.
	select {
	case ch <- struct{}{}:
	default:
	}
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

// yieldPoint is the callback loop's YIELD arm as a scheduling point: hand the ready queue to the scheduler,
// which takes the run slot for it, and wait for the chain to end. It takes no slot itself (ADR 0101's
// Option A, point 3; see the body).
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
func (tt *threadTable) yieldPoint(task *liftTask) error {
	tt.mu.Lock()
	s, n := tt.sched, len(tt.ready)
	tt.mu.Unlock()
	if s == nil || n == 0 {
		return nil
	}
	// **Under ADR 0101's Option A this takes neither `entrySem` nor the run slot** (point 3). The
	// reference has already cleared `exclusive_thread` by the time its YIELD arm runs — `canon_lift`'s
	// loop sets `inst.exclusive_thread = None` on every callback return, before the `match code:` — so
	// another async task may enter while this one's Threads run. Re-taking `entrySem` here was a
	// divergence. The scheduler acquires the slot on the queue's behalf, and the callback re-entry takes
	// both back through `enterAndInvoke`.
	//
	// **And it blocks until the scheduler replies**, so the re-entry cannot race the queue (the chair's item
	// 2). The reply comes once the chain has drained the queue and freed the slot. The wait also answers:
	//   - `stop`;
	//   - the task's context, because the loop's re-entry delivers the cancellation;
	//   - the task's fault, because the loop's next pass reports it.
	//
	// An abandoned wait leaves `reply` unread, which is safe: it has capacity 1, so the scheduler never
	// blocks on it.
	ctxDone := task.context().Done()
	self := tt.get(task.threadIdx)
	faulted := tt.faultChan(self)
	// **Waiting at the YIELD arm is a state a promote can see** (#1012): `yielding` makes this implicit
	// Thread ready to `takeReady`, and `promoted` is how the scheduler says a promote switched to it. A stale
	// signal from an earlier arm is drained first, so it cannot end this one. The next entry clears the
	// state (`entering`).
	var promoted chan struct{}
	if self != nil {
		tt.mu.Lock()
		if self.promoted == nil {
			self.promoted = make(chan struct{}, 1)
		}
		promoted = self.promoted
		tt.mu.Unlock()
		// Drained outside the lock (§4 B-MM-3), and BEFORE `yielding` is set: until then no promote can
		// name this Thread, so only a stale signal can be drained here.
		select {
		case <-promoted:
		default:
		}
		tt.mu.Lock()
		self.yielding = true
		tt.mu.Unlock()
	}
	reply := make(chan error, 1)
	select {
	case s.yield <- reply:
	case <-s.stop:
		return errThreadKilled
	case <-ctxDone:
		return nil
	case <-faulted:
		return nil
	case <-promoted:
		return nil
	}
	select {
	case err := <-reply:
		return err
	case <-s.stop:
		return errThreadKilled
	case <-ctxDone:
		return nil
	case <-faulted:
		return nil
	case <-promoted:
		return nil // a promote switched to this task: its callback runs now, ahead of the queue
	}
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
