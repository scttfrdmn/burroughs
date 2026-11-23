// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// E5's witnesses (#1339, discharging #1227): contract §6 R-3, *"a future reported ready MUST have its payload's writes
// visible to the polling agent without further synchronization"*, and R-4's append stamped on #1334, *"an event
// delivered to one agent observes B-MM-1 (R-3) for the payload another agent wrote"*. E4 witnessed the wake across
// agents; this witnesses what the woken agent then reads. The fixture is `testdata/cross-agent-payload.wat`, whose
// header gives its layout and patterns.
//
// # The edge, mapped
//
// The writer B and the reader A are agents of one instance, so they share its handle table (`asyncHandles`). In every
// row A arms its read first, so B's write is the copy's second arriver and the copy runs on B's goroutine, inside
// `futureWrite` (`copyFutureValueLocked`, then `readableFutureEnd.resolveLocked`) or `streamWrite`
// (`driveStreamCopyLocked`, then `readableStreamEnd.armLocked`), both under `asyncHandles.mu`. Arming the read end
// is `waitableSet.signalLocked`, in the same critical section, after the copy. A takes the event under the same
// mutex: in `waitableSetPoll`, around `waitableSet.pendingEventLocked`, and in `waitableSetWait`'s loop, around
// `waitTake.takeLocked`. So B's unlock at the end of its write is synchronized before A's lock at its take, and A's
// loads after the take return to the guest follow that lock in A's program order.
//
// **That edge covers B's plain stores to every other address, not only the copied value**, because a Go mutex is
// not an edge over the words it guards: a release publishes every write the releasing goroutine made before it. B's
// stores to its region precede B's `future.write` in B's program order, so they happen before its unlock, and so
// before A's loads. That is B-MM-1's "acquire edge over the entire shared address space" for this crossing.
//
// **The mutex is not the only edge, and the neuters say which others there are.** Each of these orders B's stores
// before A's loads on its own:
//
//   - **the wake channel** (wait only): `signalLocked` closes it after the copy, and the parked select receives it,
//     as `async_waitset.go`'s header names;
//   - **B-MM-1's package-wide counter** (ADR 0052): the copy's `Caller.Read` and `Caller.Write` cross it after B's
//     stores and after the copy's own write, and A's `storeEvent` crosses it in `Caller.Write` before A returns to the
//     guest.
//
// Two more cover only the stores B made before its call: the instance's `world.mu`, locked by interp's `callAdapter`
// in `world.beginHostCall` and `world.endHostCall` around every canon built-in, and the store lock's `mu`, locked
// by `walker.lockGuard`'s check before and after one. B takes each at its write's start, so neither orders the copy
// B makes inside the call.
//
// So the payload's visibility does not rest on one lock. This row is a witness, not a design change: no edge was
// found missing.
//
// # What the race detector can see here
//
// An aligned `i32.store` or `i32.store8` is plain to the guest and atomic in the engine (ADR 0054, and ADR 0111 for
// the unaligned and narrow arms), so a missing edge between two such accesses is invisible to `-race` by
// construction. The fixture therefore also has B write 1024 bytes per iteration with `memory.fill`, which the engine
// performs as a plain Go write (ADR 0064), and the stream row's payload reaches A's buffer through the copy's
// `Caller.Write`, also plain. Those are the accesses `-race` can see race A's loads.
//
// # Neuters, run
//
// Recorded at TestAnEventOnOneAgentCarriesThePayloadAnotherAgentWrote, on darwin/arm64 under go1.26.9.

// payloadRuns is how many fresh instances each payload row runs: 20, or 4 under `-race`. Each run is `iters`
// iterations (the fixture's constant, read through its `iters` export), each a fresh copy whose payload A checks in
// full. The iterations are not scaled, so a run under `-race` is as dense as one without; only the run count is.
// Measured on darwin/arm64, 20 runs a row took 1.7s to 3.5s plain and 21s to 53s under `-race`.
const payloadRuns = 20 / min(raceCountScale, 5) // raceCountScale is 1, or 20 under -race

// payloadCallBound converts a run that never returns into a reported place. The fixture's own join is bounded at 60s.
const payloadCallBound = 120 * time.Second

// The fixture's places (its header's layout).
const (
	plJoin      = 20 // run's join verdict
	plADone     = 24 // A's last completed iteration
	plBDone     = 28 // B's last completed iteration
	plNonePolls = 32 // A's polls that found nothing (future-poll)
	plAFaults   = 64 // A's fault count, then its first fault: iteration, kind, index, got, want
	plBFaults   = 96 // B's fault count, then its first fault: iteration, kind, got, want
)

// payloadAKinds and payloadBKinds name the fixture's fault kinds, by number.
var (
	payloadAKinds = map[uint32]string{
		1: "its read did not return BLOCKED", 2: "the event code", 3: "the event index", 4: "the event payload",
		5: "the future's offset", 6: "a region word B stored with i32.store", 7: "a byte B wrote with memory.fill",
		8: "a stream byte the copy delivered", 9: "a poll found nothing for 5s",
	}
	payloadBKinds = map[uint32]string{1: "A never published the iteration within 5s", 2: "its write's result"}
)

// payloadRows are the rows: the export, and what it witnesses.
var payloadRows = []struct{ name, what string }{
	{"stream", "a stream<u8> B filled with plain stores, read by A after waitable-set.wait"},
	{"future", "a future<u32> carrying the offset of a region B filled with plain stores, read by A after waitable-set.wait"},
	{"future-poll", "the future row, A taking the event by waitable-set.poll in a loop"},
}

// payloadRun is one run's places.
type payloadRun struct {
	joined, aDone, bDone, nonePolls uint32
	aFault                          [6]uint32 // count, iteration, kind, index, got, want
	bFault                          [5]uint32 // count, iteration, kind, got, want
}

// describe names a run's first fault on each side, or "" if neither agent recorded one.
func (r payloadRun) describe() string {
	s := ""
	if a := r.aFault; a[0] != 0 {
		s += fmt.Sprintf(" A recorded %d fault(s); the first, at iteration %d: %s, index %d, is %#x, want %#x.", a[0],
			a[1], payloadAKinds[a[2]], a[3], a[4], a[5])
	}
	if b := r.bFault; b[0] != 0 {
		s += fmt.Sprintf(" B recorded %d fault(s); the first, at iteration %d: %s is %#x, want %#x.", b[0], b[1],
			payloadBKinds[b[2]], b[3], b[4])
	}
	return s
}

// payloadCall calls a nullary or one-u32 export and returns its one u32, failing the test naming the agents' places if
// the call does not return within payloadCallBound.
func payloadCall(t *testing.T, in *Instantiated, name string, args ...canon.Value) uint32 {
	t.Helper()
	type out struct {
		res []canon.Value
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := in.CallValues(name, args...)
		ch <- out{res, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			t.Fatalf("%s: %v", name, o.err)
		}
		if len(o.res) != 1 {
			t.Fatalf("%s returned %v, want one u32", name, o.res)
		}
		v, ok := o.res[0].U32()
		if !ok {
			t.Fatalf("%s returned %v, want one u32", name, o.res)
		}
		return v
	case <-time.After(payloadCallBound):
		t.Fatalf("%s did not return within %v. %s", name, payloadCallBound, agentsPlace(goroutineStacks()))
		return 0
	}
}

// runPayloadRow runs one row on a fresh instance and reads its places.
func runPayloadRow(t *testing.T, row string) (payloadRun, uint32) {
	t.Helper()
	in := instantiateFixtureWithHost(t, "cross-agent-payload.wasm", NewHost(io.Discard, io.Discard, nil))
	defer in.Close()
	iters := payloadCall(t, in, "iters")
	var r payloadRun
	r.joined = payloadCall(t, in, row)
	if r.joined != 1 {
		// Read before the places, which are calls on an instance whose agents may still be running.
		st := agentsPlace(goroutineStacks())
		t.Fatalf("%s: an agent did not finish within the fixture's 60s join. %s", row, st)
	}
	word := func(off uint32) uint32 { return payloadCall(t, in, "word", canon.U32(off)) }
	r.aDone, r.bDone, r.nonePolls = word(plADone), word(plBDone), word(plNonePolls)
	for k := range r.aFault {
		r.aFault[k] = word(plAFaults + 4*uint32(k))
	}
	for k := range r.bFault {
		r.bFault[k] = word(plBFaults + 4*uint32(k))
	}
	return r, iters
}

// TestAnEventOnOneAgentCarriesThePayloadAnotherAgentWrote is R-3 across agents (#1339): agent B fills memory with
// plain stores and completes a stream or future write; agent A, which armed the read, takes the event by
// `waitable-set.wait` or `waitable-set.poll` and finds every byte and word B wrote. Each row runs payloadRuns fresh
// instances of `iters` iterations, and each agent records its first fault at a place the test reads, so a failure
// names the iteration, the address and both values, not only that something differed.
//
// **NEUTER, run, the delivery:** with `futureWrite`'s inline copy dropped, the future row fails its first run: A
// recorded 1000 faults, the first at iteration 1, the future's offset 0x0, want 0x1800. The root twin fails the same.
//
// **NEUTER, run, the edges.** Without `-race` no neuter below moved any row: 20 of 20 runs, every payload as B
// wrote it. That is expected rather than reassuring, because A's loads are sequentially-consistent atomics in the
// engine (ADR 0054). Under `-race`:
//
//   - **`asyncHandles.mu` removed from `waitableSetPoll`:** future-poll fails with 6 reports, every one on the
//     handle table (`waitableSet.signalLocked` against `pendingEventLocked`, `readableFutureEnd.resolveLocked`
//     against its take), none on guest memory. So the neuter is vacuous for the payload: the counter still orders it.
//   - **Only the unlock moved above the take, the lookup still locked:** 0 reports. This is vacuous for the payload
//     and the table alike, because the lookup's lock acquires B's unlock whenever A could see B's event.
//   - **The poll's mutex, the counter, `world.mu` and the store lock's checks all removed:** 6 reports, and two are
//     on guest memory. A's load of the future's value at 2048 races the copy's `memory.write`, and A's `i32.load8_u`
//     races B's `execMemoryFill`. With only the store lock's checks restored, the fill's race is gone and the
//     copy's stays; the same with only `world.mu` restored. With only the counter restored, both are gone.
//   - **`asyncHandles.mu` removed from `waitableSetWait`'s loop:** stream and future fail with 4 reports, all on the
//     handle table. With the counter, `world.mu` and the store lock's checks also removed, it is still 4 and none is
//     on guest memory. The wake channel carries the payload alone there, and its own neuter is E4's: A never wakes.
//     So the wait rows cannot be made to read an unordered payload without also removing the wake.
func TestAnEventOnOneAgentCarriesThePayloadAnotherAgentWrote(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	for _, row := range payloadRows {
		t.Run(row.name, func(t *testing.T) {
			start := time.Now()
			var iters uint32
			var nonePolls uint64
			for i := range payloadRuns {
				r, n := runPayloadRow(t, row.name)
				iters = n
				nonePolls += uint64(r.nonePolls)
				if r.aFault[0] != 0 || r.bFault[0] != 0 {
					t.Fatalf("run %d: %s:%s", i, row.what, r.describe())
				}
				if r.aDone != iters || r.bDone != iters {
					t.Fatalf("run %d: %s: A completed %d iterations and B %d, want %d each, and neither recorded a fault",
						i, row.what, r.aDone, r.bDone, iters)
				}
			}
			t.Logf("%d of %d runs, %d iterations each, every payload byte and word as B wrote it, %v", payloadRuns,
				payloadRuns, iters, time.Since(start).Round(time.Millisecond))
			if row.name == "future-poll" {
				t.Logf("A's polls that found nothing before the event: %d over %d iterations", nonePolls,
					uint64(payloadRuns)*uint64(iters))
			}
		})
	}
}
