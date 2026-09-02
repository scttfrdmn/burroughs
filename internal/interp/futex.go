// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import "time"

// The three results `memory.atomic.wait` pushes, under the reference's own names (`eval.ml:449-457`).
//
// Named constants rather than literals because the numbers are not ordered by anything — 1 is the
// *cheapest* outcome and 2 is the one that took longest — so a bare `2` at a return site reads as an
// index and means an outcome.
const (
	waitWoken    int32 = 0 // "ok": a `notify` claimed this waiter
	waitNotEqual int32 = 1 // "not-equal": the cell did not hold `expected`, so nothing was queued
	waitTimedOut int32 = 2 // "timed-out": the interval elapsed with no claim
)

// waiter is one thread's place in a memory's wait queue — [ADR 0060]'s mechanism for §2 T-3.
//
// [ADR 0060]: ../../docs/decisions/0060-the-futex-queue-hangs-off-memory-keyed-by-effective-address-because-a-pointer-key-would-borrow-its-soundness-from-another-package.md
type waiter struct {
	// ch is the wake. **Buffered to one, and a send rather than a `close`.**
	//
	// Buffered because the notifier must not block: it sends after releasing `waitMu` (§4 B-MM-3,
	// see `notify`), and a rendezvous send would make the notifying guest's progress depend on the
	// waiting guest being scheduled — a wake that waits for its own wakee.
	//
	// A send rather than a `close` because `close` panics on a second delivery, so its safety would
	// rest on the detach-under-the-lock invariant being right, where a buffered send is merely
	// wrong-and-harmless if it ever is not. `Resume` closes, and the difference is worth stating
	// rather than looking like an inconsistency: a stop round has exactly one releaser by
	// construction, and here the number of potential notifiers is the number of guest threads.
	ch chan struct{}

	// claimed is set by the `notify` that detached this waiter, under `memory.waitMu`.
	//
	// **It exists to break the woken-versus-timed-out tie, and it must break it toward the notify.**
	// A waiter whose timer fires as a notifier detaches it has both outcomes available; if it
	// reported `waitTimedOut`, that `notify`'s return count would already have claimed a wake the
	// waiting guest was told did not happen — and the count is guest-visible. So the tie is resolved
	// by which side won `waitMu`, and a claimed waiter returns `waitWoken` even though its timer
	// fired.
	claimed bool
}

// wait is `memory.atomic.wait`'s suspending half: compare, enqueue, block, and report which of the
// three things happened. [ADR 0060] decision 1.
//
// # Why holding `waitMu` across the compare and the enqueue closes the futex miss
//
// The hazard: A loads the cell and finds `expected`; B stores a new value and notifies; A enqueues and
// blocks; A sleeps through a wake that was meant for it. The reference interpreter cannot have it,
// because a single-agent executor gets `check; suspend` atomically for free. This engine has to buy it.
//
// The argument, in the only two cases there are. B's store precedes B's `notify` in program order, and
// both accesses to the cell are sequentially consistent (0054), so there is a total order over them.
// Either A's load comes after B's store in that order — and then A sees the new value and returns
// `waitNotEqual`, having queued nothing — or it comes before, in which case B's acquisition of
// `waitMu` is after A's release of it: the alternative implies B's critical section precedes A's load,
// hence B's store precedes A's load, which is the case already excluded. So A is *already enqueued*
// when B detaches, and B wakes it. There is no third case and no window.
//
// **Which is why the compare lives in `enqueueIfEqual` and not here.** This function could load the
// cell, find it equal and then enqueue; it would read as the same code and would put the race back.
//
// # SP-2's ordering, which is why a `*thread` is a parameter
//
// Contract §3 SP-2 makes a thread blocked here *"count as at a safepoint"*, and SP-4 requires a stop
// to complete *"without waking"* it. `enterBlocked`/`leaveBlocked` are that protocol — see `world`.
// The mark is taken **before** the compare, so a `Stop` racing this either observes it and counts this
// thread as arrived, or does not and is announced to by `enterBlocked`'s own park. What must not happen
// is the third thing: a `Stop` that neither sees the mark nor receives an arrival, which is
// `ErrStopDeadline` reported for a thread that is by definition not running.
//
// `leaveBlocked` is deferred, so it runs before `atomicWait` pushes the result — nothing at all happens
// between the wake and the safepoint check, which is the strongest reading of SP-2's *"cannot touch
// guest memory until it re-enters through a boundary that observes the stop"* and costs nothing to
// arrange in this order.
//
// [ADR 0060]: ../../docs/decisions/0060-the-futex-queue-hangs-off-memory-keyed-by-effective-address-because-a-pointer-key-would-borrow-its-soundness-from-another-package.md
func (m *memory) wait(t *thread, ea uint64, c atomicCell, expected uint64, timeout int64) int32 {
	t.enterBlocked()
	defer t.leaveBlocked()

	w := m.enqueueIfEqual(ea, c, expected)
	if w == nil {
		return waitNotEqual
	}

	// **A negative timeout is "never expire", and it is a nil channel rather than a second form.** A
	// receive on a nil channel blocks forever, so the `select` below has one live case. `time.Duration`
	// is already nanoseconds, which is the unit the instruction's operand is in, so the conversion is a
	// retyping and not a scaling — and a zero timeout goes through the timer like any other rather
	// than short-circuiting, because a `notify` that arrives inside a zero-nanosecond interval is
	// permitted to win and a short-circuit would be a second answer for the same case.
	var expiry <-chan time.Time
	if timeout >= 0 {
		timer := time.NewTimer(time.Duration(timeout))
		defer timer.Stop()
		expiry = timer.C
	}
	select {
	case <-w.ch:
		return waitWoken
	case <-expiry:
	}
	return m.resolveExpiry(ea, w)
}

// enqueueIfEqual compares the cell and queues a waiter under one acquisition of `waitMu`, reporting nil
// when the value had already changed. The soundness argument is on `wait`.
//
// **This is its own function because `TestNoEngineLockIsHeldAcrossAChannelOperation` computes a
// critical section from positions rather than from control flow.** Inlined into `wait`, the interval
// from the first `Lock` to the last `Unlock` would span the `select`, and the control would report a
// channel receive inside a critical section that is not held there. The control's message offers to
// have the *rule* narrowed in the PR that needs it, and this is the better answer to a conservative
// hazard control: not an exemption and not a narrowing, but a critical section small enough that a
// positional reading of it is exact. The pressure the over-report applies is toward short critical
// sections, which is where a lock this shape should be anyway.
func (m *memory) enqueueIfEqual(ea uint64, c atomicCell, expected uint64) *waiter {
	m.waitMu.Lock()
	defer m.waitMu.Unlock()

	if c.load() != expected {
		return nil
	}
	if m.waiters == nil {
		m.waiters = make(map[uint64][]*waiter)
	}
	w := &waiter{ch: make(chan struct{}, 1)}
	m.waiters[ea] = append(m.waiters[ea], w)
	return w
}

// resolveExpiry decides whether a fired timer was a timeout, which is a question only `waitMu` can
// answer: see `waiter.claimed`.
//
// Dequeuing here rather than letting the entry rot is what keeps an address that is waited on and times
// out repeatedly from accumulating departed waiters that every later `notify` would count against its
// own `count` operand — a wake budget spent on threads that have left.
func (m *memory) resolveExpiry(ea uint64, w *waiter) int32 {
	m.waitMu.Lock()
	defer m.waitMu.Unlock()

	if w.claimed {
		return waitWoken
	}
	m.dequeue(ea, w)
	return waitTimedOut
}

// dequeue removes one waiter from its address's queue. Called with `waitMu` held.
//
// The emptied queue is deleted rather than left as a zero-length slice: the map is keyed by guest
// address, so a guest that waits and times out across a large memory would otherwise grow it by one
// permanent entry per address it ever touched.
func (m *memory) dequeue(ea uint64, w *waiter) {
	q := m.waiters[ea]
	for i, other := range q {
		if other != w {
			continue
		}
		q = append(q[:i], q[i+1:]...)
		if len(q) == 0 {
			delete(m.waiters, ea)
		} else {
			m.waiters[ea] = q
		}
		return
	}
}

// notify wakes up to `count` waiters at `ea` and reports how many it detached. [ADR 0060] decision 1.
//
// **The release is outside the lock, and that is contract §4 B-MM-3 rather than a preference.**
// Releasing a waiter *is* a guest resume — it is the operation that puts a parked thread back on the
// interpreter's dispatch loop — so sending under `waitMu` would be the clause's prohibited shape
// exactly. `Resume` was corrected into this shape on #591, and
// `TestNoEngineLockIsHeldAcrossAChannelOperation` polices it here without being extended, its domain
// being every non-test file in the tree.
//
// **The detach is what makes an unlocked release safe.** Once these waiters are out of the map no
// second notifier can see them, so the sends below race with nothing: the only other party that could
// touch one is its own thread's expiry arm, which finds `claimed` set and takes the already-woken
// branch.
//
// `count == 0` returns before the lock, which is the reference's own fast path (`eval.ml:465`) rather
// than an optimization — with nothing to wake there is nothing to serialize against.
//
// [ADR 0060]: ../../docs/decisions/0060-the-futex-queue-hangs-off-memory-keyed-by-effective-address-because-a-pointer-key-would-borrow-its-soundness-from-another-package.md
func (m *memory) notify(ea uint64, count uint32) int32 {
	if count == 0 {
		return 0
	}
	woken := m.detach(ea, count)
	for _, w := range woken {
		w.ch <- struct{}{}
	}
	return int32(uint32(len(woken)))
}

// detach claims up to `count` waiters at `ea` and removes them from the queue. Its own function for the
// reason `enqueueIfEqual` is: the sends in `notify` are outside this lock, and a positional reading of
// a critical section cannot see that unless the critical section is a whole function.
func (m *memory) detach(ea uint64, count uint32) []*waiter {
	m.waitMu.Lock()
	defer m.waitMu.Unlock()

	q := m.waiters[ea]
	n := min(uint32(len(q)), count)
	woken := make([]*waiter, n)
	copy(woken, q[:n])
	for _, w := range woken {
		w.claimed = true
	}
	if rest := q[n:]; len(rest) == 0 {
		delete(m.waiters, ea)
	} else {
		m.waiters[ea] = rest
	}
	return woken
}
