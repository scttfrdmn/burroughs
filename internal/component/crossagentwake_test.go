// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// E4's witnesses (#1338): contract §6 R-1 as amended on #1334. A `waitable-set.wait` blocked on one agent returns
// when another agent of the same instance makes an event ready on a waitable joined to that set, one row per event
// kind the amendment names, plus F8's netpollBreak shape and the batch; and it ends when the instance is shut down,
// as §2 T-5 ends any blocked agent. The fixtures are `testdata/cross-agent-wake.wat` and
// `testdata/cross-agent-shutdown.wat`. Their agents are started through the component spawn door (ADR 0131). Their
// memories declare max = min, so they are Go-heap backed and `-race` sees guest memory.

// wakeRuns is how many fresh instances each wake row runs. Each run's actor acts only after the test has seen the
// waiter parked, so every run exercises the wake, not a first check that finds the event already there.
const wakeRuns = 100

// wakeProbeBound bounds `await-parked`: how long the actor waits to see the waiter parked. Only a waiter that never
// parks waits it out.
const wakeProbeBound = 5 * time.Second

// closeEndsBound is how long Close may take to end an agent blocked in a wait. It is measured in microseconds; the
// bound is half `interp.closeQuiesceInterval`, so a Close that only outwaits the agent fails it.
const closeEndsBound = 5 * time.Second

// The frames a parked waiter's goroutine is in: a `waitable-set.wait`'s select (kind 0), and a
// `memory.atomic.wait32`'s (kind 1).
//
// **The wait's name is bare, without its package**, as `unwakeable_test.go`'s is. Under the pinned go1.26.9,
// `waitableSetWait` is inlined into `(*walker).asyncBuiltinFunc`, so its parked closure is named
// `component.(*walker).asyncBuiltinFunc.waitableSetWait.func2.2`, and `component.waitableSetWait` matches nothing.
// go1.27.4 does not inline it, so the qualified name passed locally and failed every row under `make ci`'s
// GOTOOLCHAIN. A frame name the compiler chooses is not one a test can qualify.
var parkFrames = [...]string{"waitableSetWait", "interp.(*memory).wait"}

// goroutineStacks is every goroutine's stack, as `runtime.Stack` writes it, in a buffer grown until it fits.
func goroutineStacks() string {
	for n := 1 << 20; ; n *= 2 {
		buf := make([]byte, n)
		if k := runtime.Stack(buf, true); k < n {
			return string(buf[:k])
		}
	}
}

// parkedIn returns the stacks of the goroutines parked in a select inside frame: the place a blocked wait is.
func parkedIn(stacks, frame string) []string {
	var out []string
	for _, g := range strings.Split(stacks, "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		if strings.Contains(head, "[select") && strings.Contains(g, frame) {
			out = append(out, g)
		}
	}
	return out
}

// awaitParked waits until at least n goroutines are parked in frame, or bound passes. It reports whether they were.
func awaitParked(frame string, n int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for {
		if len(parkedIn(goroutineStacks(), frame)) >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// placeOf names where the goroutines parked in a waitable-set.wait are, by their innermost engine frames, for a
// failure that must name its place rather than time out.
func placeOf(stacks string) string {
	parked := parkedIn(stacks, parkFrames[0])
	if len(parked) == 0 {
		return "no goroutine is parked in waitable-set.wait; " + agentsPlace(stacks)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d goroutine(s) parked in waitable-set.wait's select:", len(parked))
	for _, g := range parked {
		describeGoroutine(&b, g)
	}
	return b.String()
}

// agentsPlace names where every goroutine an engine file started is, by its status and innermost engine frames.
// It is what a failure reports when the waiter is not where the row expects it. #1338's first `make ci` failed
// every row of both packages, with "no goroutine is parked" as the whole of its evidence. Every local reproduction
// passed, because each ran on the shell's go1.27.4 and not the pinned toolchain. This listing found the waiter
// parked under the inlined name parkFrames now matches. A failure must name its place the first time it happens.
func agentsPlace(stacks string) string {
	var b strings.Builder
	n := 0
	for _, g := range strings.Split(stacks, "\n\n") {
		_, created, ok := strings.Cut(g, "\ncreated by ")
		if !ok {
			continue
		}
		lines := strings.SplitN(created, "\n", 3)
		// The creator's function line carries the module path; its file line carries the checkout's, which need not
		// be named burroughs.
		if len(lines) < 2 || !strings.HasPrefix(lines[0], "github.com/scttfrdmn/burroughs/internal/") ||
			strings.Contains(lines[1], "_test.go") {
			continue
		}
		n++
		describeGoroutine(&b, g)
	}
	return fmt.Sprintf("%d engine goroutine(s):%s", n, b.String())
}

// describeGoroutine writes g's status line, its four innermost engine frames, and its creation site.
func describeGoroutine(b *strings.Builder, g string) {
	head, _, _ := strings.Cut(g, "\n")
	b.WriteString("\n  " + head)
	frames := 0
	for _, ln := range strings.Split(g, "\n") {
		if frames < 4 && strings.Contains(ln, "burroughs/internal/") && !strings.HasPrefix(ln, "\t") {
			fn, _, _ := strings.Cut(ln, "(0x")
			b.WriteString("\n    " + strings.TrimPrefix(fn, "github.com/scttfrdmn/burroughs/internal/"))
			frames++
		}
	}
	if _, created, ok := strings.Cut(g, "\ncreated by "); ok {
		site, _, _ := strings.Cut(created, " in goroutine")
		b.WriteString("\n    created by " + strings.TrimPrefix(site, "github.com/scttfrdmn/burroughs/internal/"))
	}
}

// countingMember is a waitable that counts how often a set inspects it, for the batch's cost witness.
type countingMember struct {
	idx     uint32
	pending bool
	set     *waitableSet
	checks  *int
}

func (m *countingMember) pendingEventLocked() (event, bool) {
	*m.checks++
	if !m.pending {
		return event{}, false
	}
	m.pending = false
	return event{code: eventFutureRead, p1: m.idx}, true
}

func (m *countingMember) hasPendingEventLocked() bool {
	*m.checks++
	return m.pending
}
func (m *countingMember) joinTo(s *waitableSet)    { m.set = s }
func (m *countingMember) currentSet() *waitableSet { return m.set }

// TestDrainingASetCostsOnePerEventNotOnePerMember is R-1's "batch" at O(1) per event (#1338): a set of 4096 members,
// of which 8 become ready, is drained by its take, the one `waitable-set.poll` and `waitable-set.wait` share, in 8
// takes and a ninth that reports none. The members inspected must be at most 2 per event and none for the take that
// reports none, whatever the set's size; and the 8 come out in the order they became ready, each once. A member
// joined while ready and one that left the set after it became ready are the queue's two other entries: the first
// is taken, the second is not.
//
// **NEUTER, run:** `waitableSet.pendingEventLocked` scanning its members as before reads 36836 inspections for the
// 8 events and 4096 for the take that finds none, and delivers the 8 in join order (the 8 are the last members).
func TestDrainingASetCostsOnePerEventNotOnePerMember(t *testing.T) {
	const members, ready = 4096, 8
	checks := 0
	s := &waitableSet{wake: make(chan struct{})}
	ms := make([]*countingMember, members)
	for i := range ms {
		ms[i] = &countingMember{idx: uint32(i), checks: &checks}
		joinWaitableLocked(ms[i], s)
	}
	var want []uint32
	for i := range ready {
		m := ms[members-1-i] // the last members, readied last first, so join order is not readiness order
		m.pending = true
		s.signalLocked(m)
		want = append(want, m.idx)
	}
	checks = 0
	var got []uint32
	for {
		e, ok := s.pendingEventLocked()
		if !ok {
			break
		}
		got = append(got, e.p1)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the set's takes delivered %v, want %v: each once, in readiness order", got, want)
	}
	t.Logf("%d members, %d events: %d member inspections", members, ready, checks)
	if checks > 2*ready {
		t.Errorf("draining %d events from a set of %d members inspected %d members, want at most %d: the take "+
			"is not O(1) per event", ready, members, checks, 2*ready)
	}
	checks = 0
	if _, ok := s.pendingEventLocked(); ok || checks != 0 || s.hasPendingEventLocked() {
		t.Errorf("a drained set's take inspected %d members (want 0) or found an event", checks)
	}

	// A waitable joined while ready is taken; one that became ready and then left is not.
	early := &countingMember{idx: 9001, checks: &checks, pending: true}
	joinWaitableLocked(early, s)
	gone := ms[0]
	gone.pending = true
	s.signalLocked(gone)
	joinWaitableLocked(gone, nil)
	if e, ok := s.pendingEventLocked(); !ok || e.p1 != early.idx {
		t.Errorf("the take after a ready join read %v, %v; want the joined waitable's event (%d)", e, ok, early.idx)
	}
	if e, ok := s.pendingEventLocked(); ok {
		t.Errorf("the take delivered %v from a member that had left the set", e)
	}
}

// TestTheReadyQueueIsBoundedWhenEventsAreTakenInline is the queue's bound (#1338): a member armed and then taken
// some other way, as a cancel or an inline completion takes its own end's event, leaves its entry in a queue nobody
// polls. 10000 such events on a set of 2 members leave at most 2*2+readySlack entries.
//
// **NEUTER, run:** with `signalLocked` not compacting, the queue holds 10000 entries.
func TestTheReadyQueueIsBoundedWhenEventsAreTakenInline(t *testing.T) {
	checks := 0
	s := &waitableSet{wake: make(chan struct{})}
	a := &countingMember{idx: 1, checks: &checks}
	b := &countingMember{idx: 2, checks: &checks}
	joinWaitableLocked(a, s)
	joinWaitableLocked(b, s)
	for range 10000 {
		a.pending = true
		s.signalLocked(a)
		a.pendingEventLocked() // taken inline, not through the set
	}
	if n, bound := len(s.ready), 2*len(s.members)+readySlack; n > bound {
		t.Errorf("the ready queue holds %d entries after 10000 inline-taken events, want at most %d", n, bound)
	}
}

// wakeFixture is one instance of cross-agent-wake and the test host that serves `test:e4/ops`.
type wakeFixture struct {
	in      *Instantiated
	report  []byte                 // memory[0..4096), copied by `report` once run has joined its agents
	resolve chan func(canon.Value) // pend's resolver, handed out by its impl
}

func newWakeFixture(t *testing.T) *wakeFixture {
	t.Helper()
	f := &wakeFixture{resolve: make(chan func(canon.Value), 1)}
	h := NewHost(io.Discard, io.Discard, nil)
	h.syncImpls = map[string]interp.CanonFunc{
		"test:e4/ops::await-parked": func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			kind, n := uint32(args[0].Bits), int(uint32(args[1].Bits))
			if kind >= uint32(len(parkFrames)) {
				return nil, fmt.Errorf("await-parked: no frame of kind %d", kind)
			}
			if awaitParked(parkFrames[kind], n, wakeProbeBound) {
				return []interp.Value{interp.I32(1)}, nil
			}
			return []interp.Value{interp.I32(0)}, nil
		},
		"test:e4/ops::resolve": func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			select {
			case r := <-f.resolve:
				r(canon.U32(uint32(args[0].Bits))) // pend's result store, on the resolving agent's goroutine
				return nil, nil
			case <-time.After(wakeProbeBound):
				return nil, errors.New("resolve: pend's impl was never called")
			}
		},
		"test:e4/ops::report": func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			b, err := c.Read(uint64(uint32(args[0].Bits)), uint64(uint32(args[1].Bits)))
			f.report = b
			return nil, err
		},
	}
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:e4/ops::pend": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			f.resolve <- onResolve
			return func() {}, nil
		},
	}
	f.in = instantiateFixtureWithHost(t, "cross-agent-wake.wasm", h)
	return f
}

// run calls one row's export and returns run's join verdict (1: every agent finished within its 5s bound) and the
// stacks at its return, or fails the test naming the place if the call itself does not return within 30s.
func (f *wakeFixture) run(t *testing.T, name string) (uint32, string) {
	t.Helper()
	type out struct {
		res []canon.Value
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := f.in.CallValues(name)
		ch <- out{res, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			t.Fatalf("%s: %v", name, o.err)
		}
		v, ok := o.res[0].U32()
		if len(o.res) != 1 || !ok {
			t.Fatalf("%s returned %v, want one u32", name, o.res)
		}
		st := ""
		if v != 1 {
			st = goroutineStacks()
		}
		return v, st
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not return within 30s. %s", name, placeOf(goroutineStacks()))
		return 0, ""
	}
}

// word reads the little-endian u32 at off in the report.
func (f *wakeFixture) word(off int) uint32 { return binary.LittleEndian.Uint32(f.report[off:]) }

const (
	blockedBits  = 0xffffffff // an async built-in's BLOCKED
	wakeValue    = "e4sw"     // the value every writer writes, at 96
	resolveValue = 0x5eed1338 // what B resolves pend with
)

// wakeRows are the rows, each with the words its report must hold. `w` is the report's word at an offset.
var wakeRows = []struct {
	name string
	what string
	want func(f *wakeFixture) [][3]any // (what, got, want) triples
}{
	{"stream", "a stream write on a stream whose read end is joined to S", func(f *wakeFixture) [][3]any {
		return [][3]any{
			{"A's stream.read", f.word(40), uint32(blockedBits)},
			{"A's event code", f.word(28), uint32(eventStreamRead)},
			{"A's event index", f.word(32), f.word(16)},
			{"A's event payload (COMPLETED | 4<<4)", f.word(36), uint32(copyCompleted) | 4<<4},
			{"A's read buffer", string(f.report[64:68]), wakeValue},
			{"B's stream.write", f.word(20), uint32(copyCompleted) | 4<<4},
		}
	}},
	{"future", "a future write on a future whose read end is joined to S", func(f *wakeFixture) [][3]any {
		return [][3]any{
			{"A's future.read", f.word(40), uint32(blockedBits)},
			{"A's event code", f.word(28), uint32(eventFutureRead)},
			{"A's event index", f.word(32), f.word(16)},
			{"A's event payload", f.word(36), uint32(copyCompleted)},
			{"A's read buffer", string(f.report[64:68]), wakeValue},
			{"B's future.write", f.word(20), uint32(copyCompleted)},
		}
	}},
	{"subtask", "a subtask resolution: an async lower's result store, landed on B's goroutine", func(f *wakeFixture) [][3]any {
		return [][3]any{
			{"A's pend state", f.word(40) & 15, uint32(subtaskStarted)},
			{"A's event code", f.word(28), uint32(eventSubtask)},
			{"A's event index", f.word(32), f.word(16)},
			{"A's event payload", f.word(36), uint32(subtaskReturned)},
			{"pend's result", f.word(1024), uint32(resolveValue)},
		}
	}},
	{"join", "waitable.join of an already-ready waitable into S", func(f *wakeFixture) [][3]any {
		return [][3]any{
			{"B's future.read", f.word(40), uint32(blockedBits)},
			{"B's future.write", f.word(20), uint32(copyCompleted)},
			{"A's event code", f.word(28), uint32(eventFutureRead)},
			{"A's event index", f.word(32), f.word(16)},
			{"A's event payload", f.word(36), uint32(copyCompleted)},
			{"the read buffer", string(f.report[64:68]), wakeValue},
		}
	}},
	{"netpoll-read-first", "F8's netpollBreak, the poller's read armed first", func(f *wakeFixture) [][3]any {
		return [][3]any{
			{"the socket's stream.read", f.word(136), uint32(blockedBits)},
			{"the poller's future.read", f.word(40), uint32(blockedBits)},
			{"the poller's event code", f.word(28), uint32(eventFutureRead)},
			{"the poller's event index", f.word(32), f.word(16)},
			{"the poller's event payload", f.word(36), uint32(copyCompleted)},
			{"the poller's read buffer", string(f.report[64:68]), wakeValue},
			{"the breaker's future.write", f.word(20), uint32(copyCompleted)},
			{"the poll after the break (the socket is not ready)", f.word(140), uint32(eventNone)},
		}
	}},
	{"netpoll-write-first", "F8's netpollBreak, the break written before the poller reads (#1325)", func(f *wakeFixture) [][3]any {
		return [][3]any{
			{"the breaker's future.write", f.word(20), uint32(blockedBits)},
			{"the poller's future.read (completed inline)", f.word(40), uint32(copyCompleted)},
			{"the poller's read buffer", string(f.report[64:68]), wakeValue},
			{"the poller's wait (it must not wait)", f.word(28), uint32(0)},
			{"the breaker's event code", f.word(52), uint32(eventFutureWrite)},
			{"the breaker's event index", f.word(56), f.word(12)},
			{"the breaker's event payload", f.word(60), uint32(copyCompleted)},
		}
	}},
}

// TestAWaitOnOneAgentIsWokenByAnotherAgentsEvent is R-1's wake requirement, one row per event kind the amendment
// names (#1338): agent A blocks in `waitable-set.wait` on S, agent B, once the test has seen A parked, makes an event
// ready on a waitable joined to S, and A returns with its code, index and payload. Each row runs wakeRuns fresh
// instances. A wake that does not arrive is a verdict at run's 5s join, which names where A is parked.
//
// **NEUTER, run:** with `joinWaitableLocked` not signalling a ready waitable it joins, the join row fails in its
// first run: run's join reaches its bound and names A parked in waitableSetWait. **NEUTER, run:** with
// `waitableSet.signalLocked` not closing the wake channel, all six rows fail the same way (netpoll-write-first's
// waiter is its breaker). **NEUTER, run:** with #1325's arm dropped from `futureRead`, netpoll-write-first's poller
// reads BLOCKED and waits, and the row names two goroutines parked, the poller and the breaker.
func TestAWaitOnOneAgentIsWokenByAnotherAgentsEvent(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	for _, row := range wakeRows {
		t.Run(row.name, func(t *testing.T) {
			start := time.Now()
			for i := range wakeRuns {
				f := newWakeFixture(t)
				joined, stacks := f.run(t, row.name)
				if joined != 1 {
					f.in.Close()
					t.Fatalf("run %d: %s: the waiter did not return within run's 5s join. %s", i, row.what,
						placeOf(stacks))
				}
				if got := f.word(24); got != 1 {
					t.Fatalf("run %d: the actor's await-parked read %d: the waiter was never seen parked", i, got)
				}
				for _, c := range row.want(f) {
					if c[1] != c[2] {
						t.Errorf("run %d: %s: %s is %#v, want %#v", i, row.what, c[0], c[1], c[2])
					}
				}
				f.in.Close()
				if t.Failed() {
					return
				}
			}
			t.Logf("%d of %d runs woken, %v", wakeRuns, wakeRuns, time.Since(start).Round(time.Millisecond))
		})
	}
}

// TestPollAndWaitOnOneSetAcrossAgentsNeitherLoseNorDuplicate is R-1's batch across agents (#1338): 64 future reads
// joined to one set are drained by two agents waiting on it and one polling it, at once, while a fourth completes
// them after both waiters are parked. Every one of the 64 is taken exactly once, as (FUTURE_READ, its index,
// COMPLETED), and each read buffer holds its own write's value.
//
// **NEUTER, run:** with `waitableSet.signalLocked` not appending to the ready queue, the waiters wake to nothing,
// and run's join names both parked in waitableSetWait.
func TestPollAndWaitOnOneSetAcrossAgentsNeitherLoseNorDuplicate(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	var byWait, byPoll int
	for i := range wakeRuns {
		f := newWakeFixture(t)
		joined, stacks := f.run(t, "batch")
		if joined != 1 {
			f.in.Close()
			t.Fatalf("run %d: a consumer did not finish within run's 5s join. %s", i, placeOf(stacks))
		}
		if got := f.word(3916); got != 1 {
			t.Fatalf("run %d: the producer's await-parked read %d: both waiters were never seen parked", i, got)
		}
		if f.word(3904) != 0 || f.word(3908) != 0 {
			t.Fatalf("run %d: a batch read did not park (%d) or a write did not complete (%d)", i, f.word(3904),
				f.word(3908))
		}
		want := map[uint32]int{}
		for j := range 64 {
			want[f.word(1024+4*j)] = j
			if got := f.word(2048 + 4*j); got != uint32(j+1) {
				t.Errorf("run %d: read %d's buffer holds %d, want %d", i, j, got, j+1)
			}
		}
		taken := map[uint32]string{}
		for k, who := range []string{"waiter 1", "waiter 2", "the poller"} {
			if bits := f.word(3888 + 4*k); bits != 0 {
				t.Errorf("run %d: %s took a wrong event: bits %#x (1 a code other than FUTURE_READ, 2 a payload other "+
					"than COMPLETED, 4 more than 64 events)", i, who, bits)
			}
			n := int(f.word(3840 + 4*k))
			if k == 2 {
				byPoll += n
			} else {
				byWait += n
			}
			for e := range n {
				idx := f.word(3072 + 256*k + 4*e)
				if _, ok := want[idx]; !ok {
					t.Errorf("run %d: %s took index %d, which is none of the 64 reads", i, who, idx)
				}
				if prev, dup := taken[idx]; dup {
					t.Errorf("run %d: index %d was taken twice, by %s and by %s", i, idx, prev, who)
				}
				taken[idx] = who
			}
		}
		if len(taken) != 64 {
			var lost []int
			for idx, j := range want {
				if _, ok := taken[idx]; !ok {
					lost = append(lost, j)
				}
			}
			slices.Sort(lost)
			t.Errorf("run %d: %d of the 64 events were taken; lost: reads %v", i, len(taken), lost)
		}
		f.in.Close()
		if t.Failed() {
			return
		}
	}
	t.Logf("%d runs, 64 events each: %d taken by the two waiters' waits, %d by the poller's polls", wakeRuns, byWait,
		byPoll)
}

// TestCloseEndsAgentsBlockedInAWaitAndInAFutexWait is R-1's shutdown clause and §2 T-5.4 (#1338): an agent blocked
// in `waitable-set.wait` on an empty set, and one blocked in `memory.atomic.wait32` that nothing notifies, are each
// ADR 0128's wait that blocks for ever, and `Close` ends both. Two shapes: run has returned and left them blocked,
// and run's own agent is still in flight, itself in an unbounded `memory.atomic.wait32`. Close returns within its
// bound, the in-flight call returns an error, and no engine goroutine survives.
//
// **NEUTER, run:** with `waitableSetWait`'s select dropping its context and abort arms, Close does not return
// within closeEndsBound, and the failure names the agent still parked in waitable-set.wait. (Close does end it
// then, after `interp.Instance.Close`'s 10s `closeQuiesceInterval` expires and the store lockdown's arm fires, so
// the bound is what discriminates: Close ends a blocked agent at once, it does not outwait it.)
func TestCloseEndsAgentsBlockedInAWaitAndInAFutexWait(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	for _, c := range []struct {
		export   string
		futexes  int // goroutines parked in memory.atomic.wait32 before Close
		inFlight bool
	}{
		{"shutdown", 1, false},
		{"shutdown-inflight", 2, true},
	} {
		t.Run(c.export, func(t *testing.T) {
			for i := range 10 {
				baseline := len(engineGoroutines())
				in := instantiateFixtureWithHost(t, "cross-agent-shutdown.wasm", NewHost(io.Discard, io.Discard, nil))
				type out struct {
					res []canon.Value
					err error
				}
				done := make(chan out, 1)
				go func() {
					res, err := in.CallValues(c.export)
					done <- out{res, err}
				}()
				if !c.inFlight {
					select {
					case o := <-done:
						if o.err != nil || len(o.res) != 1 {
							in.Close()
							t.Fatalf("run %d: %s: %v, %v", i, c.export, o.res, o.err)
						}
						if v, _ := o.res[0].U32(); v != 1 {
							in.Close()
							t.Fatalf("run %d: %s returned %d: an agent never published", i, c.export, v)
						}
					case <-time.After(10 * time.Second):
						in.Close()
						t.Fatalf("run %d: %s did not return", i, c.export)
					}
				}
				if !awaitParked(parkFrames[0], 1, wakeProbeBound) || !awaitParked(parkFrames[1], c.futexes, wakeProbeBound) {
					st := goroutineStacks()
					in.Close()
					t.Fatalf("run %d: the agents never parked: %d in waitable-set.wait, %d in memory.atomic.wait32, want 1 "+
						"and %d. %s", i, len(parkedIn(st, parkFrames[0])), len(parkedIn(st, parkFrames[1])), c.futexes,
						agentsPlace(st))
				}
				start := time.Now()
				closed := make(chan struct{})
				go func() {
					in.Close()
					close(closed)
				}()
				select {
				case <-closed:
				case <-time.After(closeEndsBound):
					t.Fatalf("run %d: Close did not return within %v. %s", i, closeEndsBound, placeOf(goroutineStacks()))
				}
				took := time.Since(start)
				if c.inFlight {
					select {
					case o := <-done:
						if !errors.Is(o.err, interp.ErrTerminated) {
							t.Fatalf("run %d: the in-flight %s returned %v, %v after Close, want T-5.4's termination", i,
								c.export, o.res, o.err)
						}
						if i == 0 {
							t.Logf("the in-flight call returned: %v", o.err)
						}
					case <-time.After(10 * time.Second):
						t.Fatalf("run %d: the in-flight %s did not return after Close", i, c.export)
					}
				}
				deadline := time.Now().Add(5 * time.Second)
				for len(engineGoroutines()) > baseline && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if left := engineGoroutines(); len(left) > baseline {
					t.Fatalf("run %d: %d engine goroutines survived Close (%d before the fixture):\n%s", i, len(left),
						baseline, strings.Join(left, "\n\n"))
				}
				if i == 0 {
					t.Logf("Close returned in %v", took.Round(time.Microsecond))
				}
			}
		})
	}
}
