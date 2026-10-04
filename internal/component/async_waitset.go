// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async slice-1 increment 2a-i-B-2: the waitable-set loop — the canon built-ins that observe a
// blocked subtask's resolution.
//
// After the blocking arm registers a subtask and returns its `subtaski` (async_lower.go), the guest:
//   waitable-set.new  -> allocates a waitable set, returns its index `si`
//   waitable.join     -> joins subtask `wi` to set `si`
//   waitable-set.wait -> BLOCKS the calling agent until a member of the set has resolved, then stores the
//                        event payload `(subtaski, state)` at a ptr and returns the event code
// The park is `CanonCaller.Blocking` (the real §5 excursion): the agent is marked blocked for the whole
// wait, so a sibling agent keeps running (H-1/H-4) and a concurrent Stop reaches its safepoint on the
// blocked mark without waking the waiter (SP-5). The wait's inner loop selects on the set's wake channel
// AND the caller's context, so Close tears the agent down (H-3). Resolution fires on the impl's own
// goroutine and is synchronized with the parked read by TWO edges, not one (the §4 B-MM-1 acquire edge at
// the async-wake crossing — corrected here after the litmus witness, as SP-6 corrected safepoint.go): the
// wake channel's close→receive (signalLocked closes it after the write; the parked select receives it) is
// the carrier when the guest genuinely parked, and asyncHandles' mutex is the carrier when the guest
// pre-finds an already-armed event on its first pendingEventLocked check. Both are load-bearing — the
// channel delivers the resume, the mutex guards the readiness state — so neither is a "redundant" edge a
// reader may delete: removing the channel means a parked guest never wakes, removing the mutex races the
// handle table. B-MM-1's async-wake acquire edge is thus IDENTICAL to the wake delivery, not merely carried
// alongside it (litmus TestBMM1AsyncWakeIsAnAcquireEdgeOverTheAddressSpace).

// eventCode mirrors definitions.py EventCode (def:696–703). Slice-1 delivered NONE and SUBTASK; the
// enum is now complete, TASK_CANCELLED having been the last gap (ADR 0094).
type eventCode uint32

const (
	eventNone        eventCode = 0
	eventSubtask     eventCode = 1
	eventStreamRead  eventCode = 2 // definitions.py EventCode.STREAM_READ (def:699)
	eventStreamWrite eventCode = 3 // definitions.py EventCode.STREAM_WRITE (def:700)
	eventFutureRead  eventCode = 4 // definitions.py EventCode.FUTURE_READ (def:701)
	eventFutureWrite eventCode = 5 // definitions.py EventCode.FUTURE_WRITE (def:702)

	// eventTaskCancelled is delivered to the LIFT's callback, not by a waitable set — the lift loop
	// synthesises it from the task's own state (def:2129/2137) rather than receiving it from a member.
	// It lives in this enum anyway because the enum is the ABI's, and a code the guest can see must be
	// named where a reader looks the codes up.
	eventTaskCancelled eventCode = 6 // definitions.py EventCode.TASK_CANCELLED (def:703)
)

// event is a waitable-set.wait result (definitions.py EventTuple / unpack_event, def:2367–2372): a code
// plus two u32 payloads. For a SUBTASK event the payload is (subtaski, subtask state).
type event struct {
	code   eventCode
	p1, p2 uint32
}

// waitable is a member of a waitable set: a thing that, once its async operation resolves, has a pending
// event for a parked waitable-set.wait to deliver. It is deliberately MINIMAL — only what the set's
// delivery loop needs (pendingEventLocked) and the join back-pointer both kinds need (joinTo). Anything
// specific to one kind (a subtask's resolution, a future end's copy) stays on that kind, reached where the
// kind is known, so a second kind cannot drift behind a method it stubs out. Implementers: *subtask
// (gate:async 2a-i-B) and *readableFutureEnd (increment 3).
type waitable interface {
	// pendingEventLocked returns this waitable's deliverable event and marks it taken, or (event{}, false)
	// if it is not yet resolved. The caller holds asyncHandles.mu.
	pendingEventLocked() (event, bool)
	// joinTo records the set this waitable belongs to, so its own resolution can wake the set's waiters.
	joinTo(s *waitableSet)
	// currentSet reports the set this waitable is joined to, or nil. Needed because `waitable.join` must
	// REMOVE the waitable from its previous set before adding it to the new one (def:737-743), and only
	// the waitable knows which set that was. The caller holds asyncHandles.mu.
	currentSet() *waitableSet
}

// waitableSet is a set of waitables a guest agent can block on. `wake` is closed-and-replaced each time a
// member resolves, so a parked waitable-set.wait selecting on it re-checks for a pending event (a condition
// variable expressed as a channel, so the same wait can also select on the caller's context for Close). All
// fields are guarded by the owning asyncHandles' mutex.
type waitableSet struct {
	members []waitable
	wake    chan struct{}
	// waiting counts the agents and lift loops currently parked on this set — the model's
	// `WaitableSet.num_waiting` (def:752), which `WaitableSet.drop` traps on (def:796).
	//
	// Both parks increment it, and that is not a detail: the model counts in `wait` (def:769-772) **and**
	// in `wait_from_callback` (def:782-786), so the stackless park is as much a waiter as the blocking
	// excursion. Counting only one of them would make `drop`'s trap depend on which park a guest happened
	// to use.
	waiting int
}

// pendingEventLocked returns the first ready member's deliverable event, or (event{}, false) if none is
// ready. The caller holds asyncHandles.mu. Mirrors WaitableSet.get_pending_event (def:761–767) — kind
// -agnostic: whichever member kind is ready supplies its own (code, p1, p2).
func (s *waitableSet) pendingEventLocked() (event, bool) {
	for _, m := range s.members {
		if e, ok := m.pendingEventLocked(); ok {
			return e, true
		}
	}
	return event{}, false
}

// signalLocked wakes any agent parked on the set. The caller holds mu and has just resolved a member (the
// member reaches its set through the joinTo back-pointer, where its own kind is known).
func (s *waitableSet) signalLocked() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// Every `waitable` implementer, asserted AT COMPILE TIME — which exists because adding `currentSet` to
// the interface broke three of these five **at runtime, not at build** (#871).
//
// `handleWaitable` reaches a waitable by dynamic type assertion, so a type that stops satisfying the
// interface does not fail to compile: it silently stops being a waitable, and `waitable.join` answers
// "handle 1 is not a waitable" about a handle that plainly is one. Six tests caught it, which was luck
// about coverage rather than a property of the code — nothing would have caught it for a kind no test
// drove. These lines turn the next such change into a build error.
var (
	_ waitable = (*subtask)(nil)
	_ waitable = (*readableFutureEnd)(nil)
	_ waitable = (*writableFutureEnd)(nil)
	_ waitable = (*readableStreamEnd)(nil)
	_ waitable = (*writableStreamEnd)(nil)
)

// removeLocked drops w from the set's members. The caller holds asyncHandles.mu.
//
// Interface values compare by identity here because every implementer is a pointer (*subtask,
// *readableFutureEnd) — which is also why a waitable cannot be a value type without this silently
// matching the wrong member.
func (s *waitableSet) removeLocked(w waitable) {
	for i, m := range s.members {
		if m == w {
			s.members = append(s.members[:i], s.members[i+1:]...)
			return
		}
	}
}

// joinWaitableLocked moves w into set, or out of any set when set is nil — the whole of the model's
// `Waitable.join` (def:737-743), which is three steps and not one:
//
//	if self.wset:  self.wset.elems.remove(self)    # leave the old set
//	self.wset = wset                               # record the new one
//	if wset:       wset.elems.append(self)         # join it
//
// # Both of the first two steps were missing (#871)
//
// `waitable.join` did only the record-and-append half. **The `si == 0` unjoin trapped** as "handle 0 is
// not a waitable set", which this engine's own comment had recorded as unexercised — and the stackless
// park is what exercises it, because a guest that parks and resumes unjoins its subtask afterwards. **And
// a re-join never left the old set**, so a waitable stayed a member of every set it had ever been in and
// its resolution would wake waiters on all of them. The second was not needed by any witness; it is
// repaired because it is the same model line, read in the same sitting, and a comment claiming to
// implement `join` while implementing a third of it is the testimony defect this corpus keeps paying for.
//
// The caller holds asyncHandles.mu.
func joinWaitableLocked(w waitable, set *waitableSet) {
	if old := w.currentSet(); old != nil {
		old.removeLocked(w)
	}
	w.joinTo(set)
	if set != nil {
		set.members = append(set.members, w)
	}
}

// waitableSetNew implements `canon waitable-set.new` (definitions.py:2351): allocate a waitable set in the
// instance table, return its index.
func waitableSetNew(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		h.mu.Lock()
		si := h.addLocked(&waitableSet{wake: make(chan struct{})})
		h.mu.Unlock()
		return []interp.Value{interp.I32(int32(si))}, nil
	}
}

// waitableJoin implements `canon waitable.join` (definitions.py:2396): join waitable `wi` to set `si`, or
// UNJOIN it when si is 0.
//
// `si == 0` is `w.join(None)` in the model (def:2402-2403), not an error — and it was refused here as
// "handle 0 is not a waitable set" until #871, whose park is what first drove a guest through it. The
// deferral was recorded in this comment ("si == 0 (unjoin) is not exercised by the blocking-arm round
// trip"), so the trigger was named before it fired; what the note could not say was that the trigger would
// be another slice's mechanism rather than a new guest.
func waitableJoin(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("component: waitable.join: got %d args, want (wi, si)", len(args))
		}
		wi, si := uint32(args[0].Bits), uint32(args[1].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		w, ok := handleWaitable(h, wi)
		if !ok {
			return nil, fmt.Errorf("component: waitable.join: handle %d is not a waitable", wi)
		}
		// si == 0 unjoins; any other index must name a real set. The order matters: a zero index is NOT
		// looked up, because handle 0 is the table's reserved nil sentinel and would fail the type check.
		var set *waitableSet
		if si != 0 {
			set, ok = handleAt[*waitableSet](h, si)
			if !ok {
				return nil, fmt.Errorf("component: waitable.join: handle %d is not a waitable set", si)
			}
		}
		joinWaitableLocked(w, set)
		return nil, nil
	}
}

// waitableSetWait implements `canon waitable-set.wait` (definitions.py:2359): block the calling agent until
// a member of set `si` has a pending event, store the event payload at `ptr`, return the event code. The
// park is a real §5 blocking excursion (CanonCaller.Blocking) — the agent is marked blocked throughout, so
// siblings run and a Stop reaches its safepoint without waking it; the inner select also watches the
// caller's context so Close terminates it.
func waitableSetWait(h *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("component: waitable-set.wait: got %d args, want (si, ptr)", len(args))
		}
		si, ptr := uint32(args[0].Bits), uint32(args[1].Bits)
		h.mu.Lock()
		set, ok := handleAt[*waitableSet](h, si)
		h.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("component: waitable-set.wait: handle %d is not a waitable set", si)
		}

		// num_waiting (def:769-772): counted for the whole park, so waitable-set.drop traps on a set this
		// agent is parked on. Decremented however the park ends, including the context-cancelled exit —
		// a leaked count would make the set permanently undroppable.
		h.mu.Lock()
		set.waiting++
		h.mu.Unlock()
		defer func() {
			h.mu.Lock()
			set.waiting--
			h.mu.Unlock()
		}()

		var ev event
		err := c.Blocking(func() error {
			for {
				h.mu.Lock()
				if e, ready := set.pendingEventLocked(); ready {
					ev = e
					h.mu.Unlock()
					return nil
				}
				wake := set.wake
				h.mu.Unlock()
				select {
				case <-wake: // a member resolved (or another waiter's cycle) — re-check
				case <-c.Context().Done(): // Close: tear the agent down (H-3)
					return c.Context().Err()
				}
			}
		})
		if err != nil {
			return nil, err
		}
		// Store the event payload (p1, p2) as two little-endian u32 at ptr (unpack_event), return the code.
		var buf [8]byte
		binary.LittleEndian.PutUint32(buf[0:4], ev.p1)
		binary.LittleEndian.PutUint32(buf[4:8], ev.p2)
		if werr := c.Write(uint64(ptr), buf[:]); werr != nil {
			return nil, fmt.Errorf("component: waitable-set.wait: storing event at ptr: %w", werr)
		}
		return []interp.Value{interp.I32(int32(ev.code))}, nil
	}
}

// waitableSetPoll implements `canon waitable-set.poll` (0x21, definitions.py:2376 → WaitableSet.poll
// def:775). It is `wait` minus the park: it reads the set's readiness through the SAME pendingEventLocked
// path wait uses (async_waitset.go's waitableSetWait, the identical `set.pendingEventLocked()` call), and
// returns a NONE event (code 0, payload 0,0) when nothing is ready rather than blocking — never a second
// readiness notion. The event-store path (two u32 at ptr, return the code) is identical to wait's, so a
// ready poll and a wait deliver byte-for-byte the same event.
func waitableSetPoll(h *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("component: waitable-set.poll: got %d args, want (si, ptr)", len(args))
		}
		si, ptr := uint32(args[0].Bits), uint32(args[1].Bits)
		h.mu.Lock()
		set, ok := handleAt[*waitableSet](h, si)
		if !ok {
			h.mu.Unlock()
			return nil, fmt.Errorf("component: waitable-set.poll: handle %d is not a waitable set", si)
		}
		ev, ready := set.pendingEventLocked() // the SAME readiness path wait uses
		h.mu.Unlock()
		if !ready {
			ev = event{} // (NONE=0, 0, 0) — nothing ready, and poll does not park
		}
		var buf [8]byte
		binary.LittleEndian.PutUint32(buf[0:4], ev.p1)
		binary.LittleEndian.PutUint32(buf[4:8], ev.p2)
		if werr := c.Write(uint64(ptr), buf[:]); werr != nil {
			return nil, fmt.Errorf("component: waitable-set.poll: storing event at ptr: %w", werr)
		}
		return []interp.Value{interp.I32(int32(ev.code))}, nil
	}
}

// waitableSetDrop implements `canon waitable-set.drop` (0x22, def:2386-2392), **rebuilt on the expiry its
// own deferral named** (#792, Scott's ruling).
//
// # The deferral, and the trigger that fired it
//
// An earlier implementation removed the table entry WITHOUT the model's two traps, so dropping a set with
// live members or waiters was silent where the spec requires a trap. The #792 close audit found it
// certified by nothing — no guest executed it, no test executed it, no fixture pinned it — and deleted it
// rather than leave dead code a reader might re-bind believing it complete. The recorded expiry was
// explicit: *"a guest that DROPS a waitable set. Then 0x22 gets the model's two traps, its oracle pin, and
// its firing witness like every other built-in."*
//
// **The committed suspending guest drops one, and #871's park is what let it get that far.** The trigger
// was named correctly; what the note could not anticipate is that the consumer would be another slice's
// mechanism rather than a new guest — which is the general shape of a deferral's trigger being a
// hypothesis about its consumer.
//
// # Both traps, and why neither is the interesting one alone
//
//	trap_if(len(self.elems) > 0)   # members still joined
//	trap_if(self.num_waiting > 0)  # someone is parked on it
//
// A members-only check passes a set being dropped out from under a parked waiter, and a waiter-only check
// passes one dropped while a subtask is still joined and may yet resolve into it. The audit's objection was
// to an impl with *neither*, so an impl with one would be the same defect at half size.
func waitableSetDrop(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: waitable-set.drop: got %d args, want (si)", len(args))
		}
		si := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		set, ok := handleAt[*waitableSet](h, si)
		if !ok {
			return nil, fmt.Errorf("component: waitable-set.drop: handle %d is not a waitable set", si)
		}
		if n := len(set.members); n > 0 {
			return nil, &interp.Trap{Reason: fmt.Sprintf(
				"waitable-set.drop: set %d still has %d joined waitable(s) (WaitableSet.drop traps on a "+
					"non-empty set; the guest must waitable.join them away first)", si, n)}
		}
		if set.waiting > 0 {
			return nil, &interp.Trap{Reason: fmt.Sprintf(
				"waitable-set.drop: set %d has %d agent(s) parked on it (WaitableSet.drop traps on a set "+
					"with waiters)", si, set.waiting)}
		}
		h.removeLocked(si)
		return nil, nil
	}
}

// asyncBuiltinFunc maps a waitable-set canon built-in opcode to its Go impl over this instance's async
// handle table. Only the four the blocking-arm round trip needs are bound (gate:async 2a-i-B-2); any other
// async built-in is refused at bind by gateAsync and never reaches here. waitable-set.wait writes its event
// through the memory bound into its CanonOptions at the call site, so no memory is threaded here.
func (w *walker) asyncBuiltinFunc(op byte, slot uint32) (interp.CanonFunc, bool) {
	switch op {
	case 0x1f: // waitable-set.new
		return waitableSetNew(w.async), true
	case 0x20: // waitable-set.wait
		return waitableSetWait(w.async), true
	case 0x21: // waitable-set.poll (gate:async increment 4) — wait minus the park, same readiness path
		return waitableSetPoll(w.async), true
	case 0x22: // waitable-set.drop — implemented on #792's named expiry (see waitableSetDrop)
		return waitableSetDrop(w.async), true
	case 0x23: // waitable.join
		return waitableJoin(w.async), true
	case 0x16: // future.read (gate:async increment 3)
		return futureRead(w.async), true
	case 0x1a: // future.drop-readable
		return futureDrop(w.async), true
	case 0x15: // future.new (2nd async guest) — mints a readable+writable future end pair
		return futureNew(w.async), true
	case 0x17: // future.write (2nd async guest) — single-value write; parks with no reader
		return futureWrite(w.async), true
	case 0x19: // future.cancel-write (2nd async guest) — the future write arm's running CANCELLED
		return futureCancelWrite(w.async), true
	case 0x06: // subtask.cancel (gate:async increment 4) — the subtask substrate's cancel path
		return subtaskCancel(w.async), true
	case 0x0d: // subtask.drop (gate:async increment 4)
		return subtaskDrop(w.async), true
	case 0x0e: // stream.new (gate:async increment 4) — mints a connected readable+writable end pair
		return streamNew(w.async), true
	case 0x10: // stream.write (gate:async increment 3, write side)
		return streamWrite(w.async), true
	case 0x11: // stream.cancel-read (gate:async increment 4) — first running production of CANCELLED
		return streamCancelRead(w.async), true
	case 0x12: // stream.cancel-write (gate:async increment 4) — cancel-read's symmetric twin
		return streamCancelWrite(w.async), true
	case 0x13: // stream.drop-readable (gate:async increment 4)
		return streamDropReadable(w.async), true
	case 0x14: // stream.drop-writable (gate:async increment 4)
		return streamDropWritable(w.async), true
	case 0x0a: // context.get (gate:async increment 4) — reads the agent's own context slot
		return contextGet(w.async, slot), true
	case 0x0b: // context.set
		return contextSet(w.async, slot), true
	case 0x09: // task.return (2nd async guest) — resolve the current async-lift task
		return taskReturn(w.async), true
	case 0x05: // task.cancel (#864) — traps unless the task was cancelled, which nothing can do yet
		return taskCancel(w.async), true
	}
	return nil, false
}

// handleWaitable returns the entry at index i as a waitable (any joinable kind — a subtask or a future
// end), or (nil, false) if out of range or not a waitable. The caller holds asyncHandles.mu.
func handleWaitable(h *asyncHandles, i uint32) (waitable, bool) {
	if i == 0 || int(i) >= len(h.entries) {
		return nil, false
	}
	w, ok := h.entries[i].(waitable)
	return w, ok
}

// handleAt returns the entry at index i as type T, or (zero, false) if out of range or a different type.
// The caller holds asyncHandles.mu.
func handleAt[T any](h *asyncHandles, i uint32) (T, bool) {
	var zero T
	if i == 0 || int(i) >= len(h.entries) {
		return zero, false
	}
	v, ok := h.entries[i].(T)
	return v, ok
}

// `refuseAtCall` lived here and is DELETED, because implementing `waitable-set.drop` above left it with no
// callers (#871). It bound an async built-in to a closure that refused by name when the guest *executed*
// it, rather than refusing the component at bind.
//
// # The design principle it carried is not deleted with it
//
// **Bound is not run.** A wit-bindgen surface imports ops whose path never executes them, so a bind-time
// refusal rejects a working guest for an op it never calls — measured on `p3async-hello`, which binds
// 0x22 and never calls it. That was Scott's #792 ruling (option (b)), and it still governs: the next
// unbuilt-but-bound op refuses at the call, not at bind. Reconstructing the helper is eight lines; what
// would be expensive to recover is the reason, which is why the reason stays on the page.
//
// Deleted rather than kept for a future caller on #792's own precedent — that audit deleted an
// uncertified `waitable-set.drop` rather than leave *"dead code a future reader might re-bind believing it
// complete"* — and because `make ci`'s deadcode gate refuses an unreachable func without a tracking issue,
// which is the same policy mechanised.
//
// # One gap it leaves dormant, recorded where it would bite
//
// While `refuseAtCall` was in use, `isBuiltAsyncBuiltin` listed 0x22 as built (it was *bound*) and the
// 47-row classification claimed the engine *executes* it. Those are different claims, and the comparator
// could not tell them apart — so the table asserted something false about drop and stayed green. With no
// bound-but-refusing op left, nothing is misclassified today. **Re-introducing one re-opens it**, so a
// future actor binding a call-time refusal must also teach the classification that bound is not executed.
// Noted at `isBuiltAsyncBuiltin` too, which is the site that would need to change.
