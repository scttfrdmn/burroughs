// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// callWithin calls export on in and returns its results, failing the test if the call has not returned within
// a harness hang guard. The guard is not a measurement: no assertion here is about how long anything took.
func callWithin(t *testing.T, in *Instantiated, export string) ([]canon.Value, error) {
	t.Helper()
	type out struct {
		res []canon.Value
		err error
	}
	done := make(chan out, 1)
	go func() { r, e := in.CallValues(export); done <- out{r, e} }()
	select {
	case o := <-done:
		return o.res, o.err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s() neither returned nor trapped within the 10s hang guard", export)
		return nil, nil
	}
}

// waitCancelReading reads one export's wasmtime reading from testdata/wait-cancel.reading.
func waitCancelReading(t *testing.T, export string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/wait-cancel.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, export+": "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("wait-cancel.reading records no reading for %q", export)
	return ""
}

// TestWaitCancelMatchesWasmtime is E5's WAT witness (#1233, ADR 0121): wait-for and wait-until async-lowered by
// a guest, against wasmtime 49.0.3's reading of the same component (wait-cancel.reading), in both gate states.
//
//   - zero: `wait-for(0)`, `wait-until(0)` and `wait-until(now())` each resolve inside the lower, RETURNED (2).
//   - order: of a 10s and a 10ms wait joined to one set, the first event names the 10ms one, RETURNED, and the
//     10s one's sync cancel then reads 4. The 10s wait is cancelled, so the row takes about 10ms.
//   - cancel-sync-async-typed: a pending wait's sync cancel reads STARTED, then 4, as wasmtime does.
//
// Two rows are the divergences ADR 0121 records, asserted at the pin's value with wasmtime's reading checked to
// still be the one the ADR names. The host's cancel resolves before it returns, so the pin's
// canon_subtask_cancel returns the state at once in both forms. wasmtime's host cancel blocks instead:
//   - cancel-sync, sync-typed: Burroughs reads 20, and wasmtime traps "cannot block a synchronous task";
//   - cancel-async: Burroughs reads STARTED and 4 inline (0x01040000), and wasmtime reads BLOCKED and
//     delivers 4 as the next event (0x01FF0104).
//
// Neutered, run: `hostWait.cancel` returning at once, neither stopping the timer nor resolving. The sync
// cancels (order, cancel-sync, cancel-sync-async-typed) wait out the shortened subtaskCancelBound and fail with
// ErrSubtaskCancelExpired, and the async cancel reads BLOCKED and waits on a 1h timer until the hang guard ends
// it, in both gate states.
func TestWaitCancelMatchesWasmtime(t *testing.T) {
	b, err := os.ReadFile("testdata/wait-cancel.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	prev := subtaskCancelBound
	subtaskCancelBound = 2 * time.Second // so the neuter's expiry is seen inside the hang guard
	defer func() { subtaskCancelBound = prev }()

	const (
		divergedSync  = 20         // STARTED * 16 + CANCELLED_BEFORE_RETURNED
		divergedAsync = 0x01040000 // STARTED, then CANCELLED_BEFORE_RETURNED inline, and no event
	)
	for _, gate := range []string{"0", "1"} {
		for _, export := range []string{"zero", "order", "cancel-sync", "cancel-sync-async-typed", "cancel-async"} {
			t.Run(export+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				reading := waitCancelReading(t, export)
				var want uint32
				switch export {
				case "cancel-sync":
					if !strings.HasPrefix(reading, "trap: cannot block a synchronous task") {
						t.Fatalf("wait-cancel.reading's cancel-sync row is %q, not the trap ADR 0121 records as a "+
							"divergence; re-read the ADR before changing this witness", reading)
					}
					want = divergedSync
				case "cancel-async":
					if reading != strconv.Itoa(0x01FF0104) {
						t.Fatalf("wait-cancel.reading's cancel-async row is %q, not the BLOCKED reading ADR 0121 "+
							"records as a divergence; re-read the ADR before changing this witness", reading)
					}
					want = divergedAsync
				default:
					n, perr := strconv.ParseUint(reading, 10, 32)
					if perr != nil {
						t.Fatalf("wait-cancel.reading's %s row %q: %v", export, reading, perr)
					}
					want = uint32(n)
				}
				in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
				if err != nil {
					t.Fatalf("instantiate: %v", err)
				}
				defer in.Close()
				res, err := callWithin(t, in, export)
				if err != nil {
					t.Fatalf("%s() = %v, want %#x", export, err, want)
				}
				if got, ok := res[0].U32(); !ok || got != want {
					t.Fatalf("%s() = %v (%#x), want %#x (wasmtime reads %s)", export, res, got, want, reading)
				}
			})
		}
	}
}

// TestAHostWaitResolvesOnce is the claim's witness (ADR 0121): a wait's timer, its cancel and Close each end it,
// and only the first resolves. A timer that fired first leaves the cancel nothing to do, so the subtask is
// RETURNED (#1009's rule), and a cancel that came first leaves the timer nothing to resolve.
//
// Neutered, run: `cancel` without the claim (stop and resolve unconditionally). The fire-then-cancel order
// resolves twice, the second a cancellation of a subtask already RETURNED, and a cancel after Close's stop
// resolves once.
func TestAHostWaitResolvesOnce(t *testing.T) {
	for _, order := range []string{"fire, cancel", "cancel, fire", "stop, fire, cancel"} {
		t.Run(order, func(t *testing.T) {
			h := newAsyncHandles()
			var got []string
			resolve := func(v canon.Value) {
				if v.IsNoResolution() {
					got = append(got, "cancelled")
				} else {
					got = append(got, "returned")
				}
			}
			cancel := h.startHostWait(time.Hour, resolve)
			h.mu.Lock()
			var w *hostWait
			for p := range h.hostWaits {
				w = p
			}
			h.mu.Unlock()
			if w == nil || cancel == nil {
				t.Fatal("startHostWait registered no wait")
			}
			for _, step := range strings.Split(order, ", ") {
				switch step {
				case "fire":
					w.fire()
				case "cancel":
					cancel()
				case "stop":
					h.stopHostWaits()
				}
			}
			h.waitHostWaits()
			want := map[string][]string{
				"fire, cancel":       {"returned"},
				"cancel, fire":       {"cancelled"},
				"stop, fire, cancel": nil,
			}[order]
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s resolved %v, want %v", order, got, want)
			}
			// A forced fire is not the timer's own, which stays pending and finds nothing when it comes. Where the
			// cancel or the stop claimed the wait, they must have stopped it.
			if order != "fire, cancel" && w.timer.Stop() {
				t.Errorf("%s left the timer pending", order)
			}
		})
	}
}

// TestCloseStopsAPendingWait is the registry's witness (ADR 0121): `start` leaves a `wait-for(1h)` pending and
// returns, and Close must stop its timer, so nothing the wait started resolves after Close. The fire is forced
// rather than waited for, since the subject is what a fire after Close does, not when one would come.
// Run under -race too: the timer's callback is a goroutine Close must not race.
//
// Neutered, run: the stopAllHostWaits call dropped from walker.close. The timer is still pending after Close,
// and the forced fire resolves the subtask.
func TestCloseStopsAPendingWait(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/wait-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	res, err := callWithin(t, in, "start")
	if err != nil {
		t.Fatalf("start() = %v", err)
	}
	packed, _ := res[0].U32()
	if subtaskState(packed&0xf) != subtaskStarted {
		in.Close()
		t.Fatalf("start() = %#x; want a STARTED subtask, pending on its timer", packed)
	}
	h := in.w.async
	h.mu.Lock()
	var waits []*hostWait
	for w := range h.hostWaits {
		waits = append(waits, w)
	}
	st, _ := handleAt[*subtask](h, packed>>4)
	h.mu.Unlock()
	if len(waits) != 1 || st == nil {
		in.Close()
		t.Fatalf("after start(), %d host waits and subtask %v; want one wait and its subtask", len(waits), st)
	}

	in.Close() // returns: the hang guard is the test binary's own timeout

	if waits[0].timer.Stop() {
		t.Error("the wait's timer was still pending after Close")
	}
	waits[0].fire() // a fire after Close, forced
	h.mu.Lock()
	resolved, left := st.resolved, len(h.hostWaits)
	h.mu.Unlock()
	if resolved {
		t.Error("a fire after Close resolved the subtask of a closed instance (ADR 0071 fact 4's hazard)")
	}
	if left != 0 {
		t.Errorf("%d host waits remain registered after Close", left)
	}
}

// TestALongWaitOutlivesTheParkBound is the park bound's witness (ADR 0121): a callback-lifted guest WAITs on a
// `wait-for(300ms)` with liftParkBound shortened to 5ms. The pending wait is a waker, so the bound re-arms and
// the wait completes, SUBTASK (1) * 16 + RETURNED (2) = 18, as wasmtime 49.0.3 reads it.
//
// Neutered, run: the expiry returned unconditionally, and run() fails with ErrLiftParkExpired.
func TestALongWaitOutlivesTheParkBound(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	prev := liftParkBound
	liftParkBound = 5 * time.Millisecond
	defer func() { liftParkBound = prev }()
	b, err := os.ReadFile("testdata/wait-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	res, err := callWithin(t, in, "run")
	if err != nil {
		t.Fatalf("run() = %v; want 18: a pending wait-for is a waker, so the park bound must not end its wait", err)
	}
	if got, ok := res[0].U32(); !ok || got != 18 {
		t.Fatalf("run() = %v, want 18 (SUBTASK, RETURNED)", res)
	}
}

// TestASyncLowerOfWaitForIsRefusedAtInstantiate is ADR 0121's refusal: wait-for lowered without the async
// option is refused at instantiate, by name, where it bound the stub and reported "not provided" at call.
//
// Neutered, run: the asyncOnly refusal dropped from the walk. The fixture instantiates, and its call reaches
// the stub's "import … is not provided".
func TestASyncLowerOfWaitForIsRefusedAtInstantiate(t *testing.T) {
	b, err := os.ReadFile("testdata/sync-lower-wait-for.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err == nil {
		_, cerr := in.CallValues("go")
		in.Close()
		t.Fatalf("a sync lower of wait-for instantiated, and its call returned %v; want it refused at instantiate", cerr)
	}
	if !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "wasi:clocks/monotonic-clock@0.3.0::wait-for") ||
		!strings.Contains(err.Error(), "only as an async import") {
		t.Fatalf("instantiate = %v; want ErrUnsupportedForm naming wait-for as served only async", err)
	}
}

// TestAnAsyncTypedSyncWaitOnAHostEventBlocks is the async-typed twin of TestASyncWaitOnAnotherInstancesEventTraps
// (grave #1267, ADR 0121): the same guest, with `run` async-typed and lifted plainly, as every Rust
// wasm32-wasip3 std guest's `run` is. Its waitable-set.wait on the host's `op` has no candidate in the
// instance, and the pin's trap is inside `if not ft.async_`, so it waits until `op` resolves and returns 7, in
// both gate states. wasmtime 49.0.3 agrees on a host event: wait-cancel's `order` row, async-typed and sync-
// lifted, blocks and returns, and its sync-typed `cancel-sync` row traps (wait-cancel.reading).
//
// `op` is resolved from another goroutine once the guest is parked on the set, a place read under the table's
// lock rather than a delay.
//
// Neutered, run: the `!t.asyncTyped` test dropped from syncTail, and run() traps "trap_if(not candidates)" in
// both gate states.
func TestAnAsyncTypedSyncWaitOnAHostEventBlocks(t *testing.T) {
	b, err := os.ReadFile("testdata/async-waitset-async-typed.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			t.Setenv(cmThreadsGateEnv, gate)
			h := NewHost(io.Discard, io.Discard, nil)
			var in *Instantiated
			h.asyncImpls = map[string]asyncLowerImpl{
				"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
					onStart()
					go func() {
						a := in.w.async
						for {
							a.mu.Lock()
							parked := false
							for _, e := range a.entries {
								if s, ok := e.(*waitableSet); ok && s.waiting > 0 {
									parked = true
								}
							}
							a.mu.Unlock()
							if parked {
								onResolve(canon.U32(7))
								return
							}
							time.Sleep(time.Millisecond) // polling a place, not timing anything
						}
					}()
					return func() {}, nil
				},
			}
			in, err = InstantiateWithHost(b, h)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			res, err := callWithin(t, in, "run")
			if err != nil {
				t.Fatalf("run() = %v; an async-typed function waits for a host event rather than trapping at the sync tail", err)
			}
			if v, ok := res[0].U32(); !ok || v != 7 {
				t.Fatalf("run() = %v, want 7", res)
			}
		})
	}
}
