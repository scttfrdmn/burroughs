// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The gate:async 2a-i-B-2 waitable-set loop witnesses. The event bytes are pinned against the oracle
// (async_lower_blocking) at the unit level through a production-faithful CanonCaller; the full round trip,
// the H-4 sibling-progress property, the SP-5 Stop-while-parked property, and H-3 Close-terminates run
// end-to-end through a real synthesized component (async-waitset-synth.wasm).

// TestWaitableSetWaitDeliversTheOracleEvent pins the event ABI: a resolved subtask joined to a set, and
// waitable-set.wait returns the SUBTASK code and stores (subtaski, state) as two u32 at the ptr — matching
// the oracle's async_lower_blocking (event_code 1, event_p1 subtaski 1, event_p2 state RETURNED 2). Uses a
// pre-resolved subtask so the wait finds the event armed and returns without a real park (the park itself
// is witnessed end-to-end by the synth tests below).
func TestWaitableSetWaitDeliversTheOracleEvent(t *testing.T) {
	for _, fx := range loadAsyncLowerBlocking(t) {
		t.Run(fx.Name, func(t *testing.T) {
			h := newAsyncHandles()
			// Register a subtask exactly as the blocking arm would (index 1), already resolved to RETURNED.
			st := &subtask{state: subtaskReturned, resolved: true}
			st.index = h.addLocked(st)
			if st.index != fx.Subtaski {
				t.Fatalf("subtaski = %d, want %d (oracle)", st.index, fx.Subtaski)
			}
			// waitable-set.new, then waitable.join(subtaski, si).
			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			siVals, err := waitableSetNew(h)(cc, nil)
			if err != nil {
				t.Fatalf("waitable-set.new: %v", err)
			}
			si := siVals[0].Int32()
			if _, jerr := waitableJoin(h)(cc, []interp.Value{interp.I32(int32(st.index)), interp.I32(si)}); jerr != nil {
				t.Fatalf("waitable.join: %v", jerr)
			}
			// waitable-set.wait(si, ptr): the event is already armed, so it returns immediately.
			const ptr = 16
			codeVals, err := waitableSetWait(h)(cc, []interp.Value{interp.I32(si), interp.I32(ptr)})
			if err != nil {
				t.Fatalf("waitable-set.wait: %v", err)
			}
			if got := codeVals[0].Int32(); got != int32(fx.EventCode()) {
				t.Errorf("event code = %d, want %d (SUBTASK)", got, fx.EventCode())
			}
			buf, err := cc.Read(ptr, 8)
			if err != nil {
				t.Fatalf("reading event payload: %v", err)
			}
			p1 := binary.LittleEndian.Uint32(buf[0:4])
			p2 := binary.LittleEndian.Uint32(buf[4:8])
			if int(p1) != fx.Subtaski || int(p2) != subtaskReturnedState {
				t.Errorf("event payload = (subtaski %d, state %d), want (subtaski %d, state %d) — oracle",
					p1, p2, fx.Subtaski, subtaskReturnedState)
			}
		})
	}
}

// subtaskReturnedState is Subtask.State.RETURNED, the state the oracle's event carries (event_p2 = 2).
const subtaskReturnedState = 2

// TestWaitableSetDeliversPerKindEventCodesNotMisrouted is the mixed-kind firing witness (gate:async
// increment 3, grown to FOUR kinds in increment 4): one waitable set holding a subtask, a readable future
// end, a writable stream end, AND a readable stream end, each resolved. Each waitable-set.wait delivers the
// member's OWN event code and payload — SUBTASK (index, state), FUTURE_READ (index, result), STREAM_WRITE
// (index, result|progress<<4), STREAM_READ (index, result|progress<<4) — so a set with four member kinds
// routes each correctly through ONE park. STREAM_READ (2) and STREAM_WRITE (3) are the adjacent codes that
// must not swap: the real p3async-hello path delivers a STREAM_READ on the readable end (the write completes
// inline and arms no event), so this end is the one the real wiring exercises, not a hypothetical. Distinct
// progress values (write 2, read 3) make the packed payloads differ, so a code-swap or a dropped-progress
// codec fails here. Four kinds through one loop is the evidence the park is kind-agnostic (2a-i-B-2).
func TestWaitableSetDeliversPerKindEventCodesNotMisrouted(t *testing.T) {
	h := newAsyncHandles()
	st := &subtask{state: subtaskReturned, resolved: true}
	st.index = h.addLocked(st)
	fe := &readableFutureEnd{resolved: true, result: copyCompleted}
	fe.index = h.addLocked(fe)
	se := &writableStreamEnd{resolved: true, result: copyCompleted, progress: 2} // partial write: 2 elements
	se.index = h.addLocked(se)
	// A readable stream end with an armed-but-untaken event — the real host-first path's live member (its
	// state stays COPYING until this wait consumes it). progress 3 distinguishes its payload from se's.
	re := &readableStreamEnd{resolved: true, result: copyCompleted, progress: 3, state: copyStateCopying}
	re.index = h.addLocked(re)

	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	siVals, err := waitableSetNew(h)(cc, nil)
	if err != nil {
		t.Fatalf("waitable-set.new: %v", err)
	}
	si := siVals[0].Int32()
	for _, wi := range []int{st.index, fe.index, se.index, re.index} {
		if _, jerr := waitableJoin(h)(cc, []interp.Value{interp.I32(int32(wi)), interp.I32(si)}); jerr != nil {
			t.Fatalf("waitable.join(%d): %v", wi, jerr)
		}
	}

	// Four ready members -> four waits, each delivering one member's event. Collect by code.
	got := map[eventCode]event{}
	for i := range 4 {
		ptr := uint32(16 + i*8)
		codeVals, werr := waitableSetWait(h)(cc, []interp.Value{interp.I32(si), interp.I32(int32(ptr))})
		if werr != nil {
			t.Fatalf("waitable-set.wait #%d: %v", i, werr)
		}
		buf, rerr := cc.Read(uint64(ptr), 8)
		if rerr != nil {
			t.Fatalf("reading event #%d: %v", i, rerr)
		}
		code := eventCode(codeVals[0].Int32())
		got[code] = event{code: code, p1: binary.LittleEndian.Uint32(buf[0:4]), p2: binary.LittleEndian.Uint32(buf[4:8])}
	}

	if len(got) != 4 {
		t.Fatalf("four member kinds delivered %d distinct event codes, want 4 — a shared code is the mis-route: %v", len(got), got)
	}
	sub, ok := got[eventSubtask]
	if !ok || sub.p1 != uint32(st.index) || sub.p2 != subtaskReturnedState {
		t.Errorf("SUBTASK event = %+v, want (p1 %d, p2 %d)", sub, st.index, subtaskReturnedState)
	}
	fut, ok := got[eventFutureRead]
	if !ok || fut.p1 != uint32(fe.index) || fut.p2 != uint32(copyCompleted) {
		t.Errorf("FUTURE_READ event = %+v, want (p1 %d, p2 %d)", fut, fe.index, copyCompleted)
	}
	// STREAM_WRITE packs result | (progress<<4): COMPLETED(0) with progress 2 -> 32. A codec that drops the
	// progress field delivers 0 here and fails — the packing hazard, witnessed in the mixed set.
	strm, ok := got[eventStreamWrite]
	if !ok || strm.p1 != uint32(se.index) || strm.p2 != uint32(copyCompleted)|(2<<4) {
		t.Errorf("STREAM_WRITE event = %+v, want (p1 %d, p2 %d)", strm, se.index, uint32(copyCompleted)|(2<<4))
	}
	// STREAM_READ (code 2) with progress 3 -> 48. Adjacent to STREAM_WRITE (3): a swap routes 48 to code 3
	// or 32 to code 2 and fails. This is the real path's delivered event.
	rd, ok := got[eventStreamRead]
	if !ok || rd.p1 != uint32(re.index) || rd.p2 != uint32(copyCompleted)|(3<<4) {
		t.Errorf("STREAM_READ event = %+v, want (p1 %d, p2 %d)", rd, re.index, uint32(copyCompleted)|(3<<4))
	}
	// The consumed read event transitioned the end out of COPYING (COMPLETED -> IDLE).
	if got := re.endStateName(); got != "IDLE" {
		t.Errorf("readable end state after consume = %s, want IDLE (COMPLETED read leaves it open)", got)
	}
}

// EventCode returns the SUBTASK event code the blocking round trip's wait delivers (definitions.py
// EventCode.SUBTASK = 1). A method on the fixture so the test reads it as the oracle's, not a literal.
func (asyncLowerBlockingFixture) EventCode() eventCode { return eventSubtask }

// waitsetSynthHost builds a Host for async-waitset-synth.wasm whose `op` impl STARTS but defers resolution,
// capturing onResolve so the test resolves the subtask when it chooses (the guest parks in
// waitable-set.wait until then). `resolvers` receives the captured resolver on each call; `entered`
// counts calls to `op` (the lower ran).
func waitsetSynthHost() (*Host, chan func(canon.Value), *int32) {
	h := NewHost(io.Discard, io.Discard, nil)
	resolvers := make(chan func(canon.Value), 4)
	var entered int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			atomic.AddInt32(&entered, 1)
			resolvers <- onResolve // defer: the test decides when to resolve
			return func() {}, nil
		},
	}
	return h, resolvers, &entered
}

// coreInstanceWithExport returns the core instance among the component's instantiated modules that has the
// named export (the runmod, which carries `run`/`sibling`), for driving Invoke/Stop directly.
func coreInstanceWithExport(in *Instantiated, name string) *interp.Instance {
	for _, ci := range in.w.toClose {
		if _, ok := ci.Export(name); ok {
			return ci
		}
	}
	return nil
}

// TestWaitableSetSynthRoundTrip is the end-to-end witness: a real component async-lowers a blocking call,
// creates a waitable set, joins the subtask, and blocks in waitable-set.wait until the impl resolves
// (on another goroutine) — then reads the lowered result. The guest returns the result (107), proving the
// whole chain: blocking arm -> subtask registered -> real park -> wake on resolution -> event delivered ->
// result lowered to the retptr -> guest read.
func TestWaitableSetSynthRoundTrip(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-waitset-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h, resolvers, entered := waitsetSynthHost()
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "run")
	if ci == nil {
		t.Fatal("no core instance exports run")
	}
	// Resolve as soon as the lower has run (the subtask exists); the guest may already be parked in wait.
	go func() {
		resolve := <-resolvers
		resolve(canon.U32(107))
	}()
	res, err := invokeWithTimeout(t, ci, "run", 5*time.Second)
	if err != nil {
		t.Fatalf("Invoke(run): %v", err)
	}
	if len(res) != 1 || res[0].Int32() != 107 {
		t.Errorf("run = %v, want [107] — the round trip did not deliver the lowered result", res)
	}
	if atomic.LoadInt32(entered) != 1 {
		t.Errorf("op impl entered %d times, want 1", atomic.LoadInt32(entered))
	}
}

// invokeWithTimeout runs Invoke on a goroutine and fails (rather than hangs) if it does not return in time.
func invokeWithTimeout(t *testing.T, in *interp.Instance, name string, d time.Duration) ([]interp.Value, error) {
	t.Helper()
	select {
	case o := <-startInvoke(in, name):
		return o.res, o.err
	case <-time.After(d):
		t.Fatalf("Invoke(%q) did not return within %s — a park did not wake", name, d)
		return nil, errors.New("unreachable")
	}
}

type invokeOutcome struct {
	res []interp.Value
	err error
}

// startInvoke runs Invoke on its own goroutine (a distinct agent — a caller on the instance's host thread)
// and delivers its outcome on a buffered channel, so a test can drive several agents at once and check
// whether one has returned without consuming the result.
func startInvoke(in *interp.Instance, name string) chan invokeOutcome {
	ch := make(chan invokeOutcome, 1)
	go func() {
		r, e := in.Invoke(name)
		ch <- invokeOutcome{r, e}
	}()
	return ch
}

func recvWithin(t *testing.T, ch chan invokeOutcome, d time.Duration, what string) invokeOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(d):
		t.Fatalf("%s did not complete within %s", what, d)
		return invokeOutcome{}
	}
}

// TestStopCompletesWhileAgentParkedInWaitableSetWait is SP-5: an agent parked in waitable-set.wait is at a
// safepoint by its blocked mark, so Stop completes within its deadline WITHOUT waking it (definitions:
// enterBlocked records the mark, atSafepointLocked reads it). The agent is genuinely parked in the §5
// blocking excursion (CanonCaller.Blocking), not simulated — the impl defers resolution so the wait truly
// blocks. After Resume, the resolution delivers and the call returns the lowered result.
func TestStopCompletesWhileAgentParkedInWaitableSetWait(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-waitset-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h, resolvers, _ := waitsetSynthHost()
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "run")

	chA := startInvoke(ci, "run")
	resolve := <-resolvers // the lower ran; agent A is heading into waitable-set.wait

	// Stop must complete within its deadline while A is parked — A never runs to satisfy the round.
	if err := ci.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop did not complete while the agent was parked in waitable-set.wait: %v", err)
	}
	// Arm the resolution and resume; A wakes, delivers the event, and returns the lowered result.
	resolve(canon.U32(107))
	ci.Resume()
	o := recvWithin(t, chA, 5*time.Second, "the parked run after Stop/Resume")
	if o.err != nil {
		t.Fatalf("run after Stop/Resume: %v", o.err)
	}
	if len(o.res) != 1 || o.res[0].Int32() != 107 {
		t.Errorf("run = %v, want [107]", o.res)
	}
}

// TestWaitableSetWaitParksOnlyTheCallingAgentSiblingRuns is H-4: a suspension in waitable-set.wait suspends
// the calling agent ONLY — a sibling agent of the same instance stays runnable and observably completes
// while the first is parked. Agent A blocks in the wait (its subtask unresolved); agent B runs `sibling`
// to completion and returns; A has not returned. Then A's subtask is resolved and A completes too.
func TestWaitableSetWaitParksOnlyTheCallingAgentSiblingRuns(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-waitset-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h, resolvers, _ := waitsetSynthHost()
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "run")

	chA := startInvoke(ci, "run")
	resolve := <-resolvers // A ran the lower and is parking in waitable-set.wait, unresolved

	// A sibling agent runs to completion while A is parked — the H-1/H-4 property.
	chB := startInvoke(ci, "sibling")
	oB := recvWithin(t, chB, 5*time.Second, "the sibling agent")
	if oB.err != nil || len(oB.res) != 1 || oB.res[0].Int32() != 42 {
		t.Fatalf("sibling = %v (err %v), want [42] — a parked agent starved its sibling", oB.res, oB.err)
	}
	// A must still be parked (nothing has resolved its subtask).
	select {
	case o := <-chA:
		t.Fatalf("the parked agent returned (%v) before its subtask resolved", o.res)
	default:
	}
	// Resolve A's subtask; A wakes and completes.
	resolve(canon.U32(107))
	oA := recvWithin(t, chA, 5*time.Second, "the parked run after its sibling ran")
	if oA.err != nil || len(oA.res) != 1 || oA.res[0].Int32() != 107 {
		t.Errorf("run = %v (err %v), want [107]", oA.res, oA.err)
	}
}

// TestCloseTerminatesAgentParkedInWaitableSetWait is H-3/T-5.4: Close tears down an agent parked in
// waitable-set.wait — the wait's inner select watches the caller's context, so cancellation returns an
// error rather than hanging. The subtask is never resolved; only Close ends the wait.
func TestCloseTerminatesAgentParkedInWaitableSetWait(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-waitset-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h, resolvers, _ := waitsetSynthHost()
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	ci := coreInstanceWithExport(in, "run")

	chA := startInvoke(ci, "run")
	<-resolvers // A is parking in waitable-set.wait; we never resolve it

	in.Close() // cancels the agent's context; the wait's select on ctx.Done returns

	o := recvWithin(t, chA, 5*time.Second, "the parked run under Close")
	if o.err == nil {
		t.Errorf("run returned %v with no error under Close — a parked agent must terminate, not complete", o.res)
	}
}

// TestWaitableSetPollReadsSameReadinessAsWait covers poll (0x21) across the same four waitable kinds the wait
// witness uses, plus the empty-set NONE case pinned by the oracle. poll is wait minus the park: it must read
// pending events through the SAME pendingEventLocked path (no second readiness notion), so a set of four
// resolved kinds drained by four polls delivers the four distinct event codes exactly as four waits would,
// and a fifth poll (nothing left ready) returns NONE without blocking. An empty set polls to NONE too.
func TestWaitableSetPollReadsSameReadinessAsWait(t *testing.T) {
	var doc struct {
		WaitableSetPoll struct {
			EmptyCode int `json:"empty_code"`
			EmptyP1   int `json:"empty_p1"`
			EmptyP2   int `json:"empty_p2"`
		} `json:"waitable_set_poll"`
	}
	loadFixtures(t, &doc)
	fx := doc.WaitableSetPoll

	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}

	poll := func(t *testing.T, h *asyncHandles, si int32, ptr uint32) event {
		t.Helper()
		codeVals, perr := waitableSetPoll(h)(cc, []interp.Value{interp.I32(si), interp.I32(int32(ptr))})
		if perr != nil {
			t.Fatalf("waitable-set.poll: %v", perr)
		}
		buf, rerr := cc.Read(uint64(ptr), 8)
		if rerr != nil {
			t.Fatalf("reading poll event: %v", rerr)
		}
		return event{code: eventCode(codeVals[0].Int32()), p1: binary.LittleEndian.Uint32(buf[0:4]), p2: binary.LittleEndian.Uint32(buf[4:8])}
	}

	// Empty set -> NONE (oracle), no park.
	h := newAsyncHandles()
	si0, err := waitableSetNew(h)(cc, nil)
	if err != nil {
		t.Fatalf("waitable-set.new: %v", err)
	}
	if got := poll(t, h, si0[0].Int32(), 8); int(got.code) != fx.EmptyCode || int(got.p1) != fx.EmptyP1 || int(got.p2) != fx.EmptyP2 {
		t.Errorf("empty poll = (code %d, p1 %d, p2 %d), want (%d, %d, %d) — NONE, no park", got.code, got.p1, got.p2, fx.EmptyCode, fx.EmptyP1, fx.EmptyP2)
	}

	// Four resolved kinds joined to one set — poll drains them, each its own code, same as wait would.
	h = newAsyncHandles()
	st := &subtask{state: subtaskReturned, resolved: true}
	st.index = h.addLocked(st)
	fe := &readableFutureEnd{resolved: true, result: copyCompleted}
	fe.index = h.addLocked(fe)
	se := &writableStreamEnd{resolved: true, result: copyCompleted, progress: 2}
	se.index = h.addLocked(se)
	re := &readableStreamEnd{resolved: true, result: copyCompleted, progress: 3, state: copyStateCopying}
	re.index = h.addLocked(re)
	siVals, err := waitableSetNew(h)(cc, nil)
	if err != nil {
		t.Fatalf("waitable-set.new: %v", err)
	}
	si := siVals[0].Int32()
	for _, wi := range []int{st.index, fe.index, se.index, re.index} {
		if _, jerr := waitableJoin(h)(cc, []interp.Value{interp.I32(int32(wi)), interp.I32(si)}); jerr != nil {
			t.Fatalf("waitable.join(%d): %v", wi, jerr)
		}
	}
	got := map[eventCode]event{}
	for i := range 4 {
		e := poll(t, h, si, uint32(16+i*8))
		got[e.code] = e
	}
	if len(got) != 4 {
		t.Fatalf("four kinds drained by poll delivered %d distinct codes, want 4 — poll must read the same readiness as wait: %v", len(got), got)
	}
	if e, ok := got[eventSubtask]; !ok || e.p2 != subtaskReturnedState {
		t.Errorf("poll SUBTASK = %+v, want p2 %d", e, subtaskReturnedState)
	}
	if e, ok := got[eventStreamRead]; !ok || e.p2 != uint32(copyCompleted)|(3<<4) {
		t.Errorf("poll STREAM_READ = %+v, want p2 %d", e, uint32(copyCompleted)|(3<<4))
	}
	// A fifth poll, nothing left ready -> NONE, no park.
	if e := poll(t, h, si, 56); e.code != eventNone {
		t.Errorf("drained poll = code %d, want NONE (0) — poll must not park", e.code)
	}
}
