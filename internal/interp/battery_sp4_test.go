// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestSP4StopCompletesWithoutWakingParkedAgents is the litmus battery case
// `sp4-stop-completes-without-waking-parked-agents` (contract §3 SP-4): `stop()` composes with §2 —
// stopping the world with N threads parked in host calls completes WITHOUT waking them.
//
// N=4 agents park in a blocking host call; one more agent runs a hot loop, so the stop has something to
// stop (a running agent to bring to a safepoint). `Stop(deadline)` must return within the deadline — the
// hot-loop agent reaches a back-edge safepoint and the four parked agents count as at safepoints (blocked)
// — and the wake counter must read 0: no parked agent was woken.
//
// The wake counter is a MEASURED zero, not an absent one. The blocking call's select has two arms — the
// intended `release` (not a wake) and `ctx.Done` (a forced wake, the agent torn out of its park) — and the
// counter increments ONLY on the `ctx.Done` arm. A stop that composes without waking never fires that arm,
// so the counter reads 0 because nothing incremented it, not because nothing could: the control
// (documented in the PR, run by adding `t.cancelCtx()` for blocked threads to `Stop`'s loop) fires the
// ctx.Done arm and drives the counter above 0 — the defect signature. Both halves of the forbidden are
// checked: the counter (no wake) and the deadline (Stop actually returns), because an engine that woke
// everybody would otherwise pass on "completes".
//
// Arbiter: neither — a scheduling claim. Both models via CI. Run under -race per the case discipline.
func TestSP4StopCompletesWithoutWakingParkedAgents(t *testing.T) {
	const (
		n         = 4
		deadline  = 10 * time.Second
		spawnWait = 30 * time.Second
	)

	release := make(chan struct{})
	var wakes atomic.Int32 // MEASURED: incremented only when a parked agent's block returns via ctx.Done
	parked := make(chan struct{}, n)
	completed := make(chan struct{}, n)

	lower := CanonLowerExtern(
		ft([]bin.ValType{bin.I32}, nil),
		func(c *CanonCaller, _ []Value) ([]Value, error) {
			berr := c.Blocking(func() error {
				parked <- struct{}{}
				select {
				case <-release:
					return nil // the intended release — not a wake
				case <-c.Context().Done():
					wakes.Add(1) // a forced wake: the agent was torn out of its park, not released
					return c.Context().Err()
				}
			})
			completed <- struct{}{}
			return nil, berr
		},
		CanonOptions{}, // no memory: SP-4's counter is host-side, not a guest-memory write
	)

	in := hostLink(t, `(module
		(import "h" "bw" (func $bw (param i32)))
		(memory 1 1 shared)
		(func (export "block") (param $a i32) (call $bw (local.get $a)))
		(func (export "spin") (param $x i32) (loop $l (br $l))))`,
		bin.Features{Threads: true}, hostImports(map[string]Extern{"bw": lower}))
	closeAtEnd(t, in)

	blockEntry := exportedFuncIndex(t, in, "block")
	spinEntry := exportedFuncIndex(t, in, "spin")

	// The hot-loop agent — something for Stop to stop (a running agent to bring to a back-edge safepoint).
	if _, err := in.Spawn(spinEntry, 0, 0); err != nil {
		t.Fatalf("Spawn of the hot-loop agent: %v — premise, not assertion", err)
	}

	// N block agents, spawned off a goroutine (bounded — a pool that blocks its spawner would wedge, #608).
	type spawnFail struct {
		i   int
		err error
	}
	var returned atomic.Int32
	spawned := make(chan struct{})
	failed := make(chan spawnFail, 1)
	go func() {
		for i := range n {
			if _, err := in.Spawn(blockEntry, int32(i), 0); err != nil {
				failed <- spawnFail{i, err}
				return
			}
			returned.Add(1)
		}
		close(spawned)
	}()
	select {
	case <-spawned:
	case f := <-failed:
		t.Fatalf("Spawn of block agent %d of %d: %v — premise, not assertion", f.i+1, n, f.err)
	case <-time.After(spawnWait):
		t.Fatalf("only %d of %d block Spawn calls returned within %v", returned.Load(), n, spawnWait)
	}

	// Floor: all N block agents parked before the stop request.
	for range n {
		select {
		case <-parked:
		case <-time.After(spawnWait):
			t.Fatalf("not all %d agents parked in the blocking call within %v", n, spawnWait)
		}
	}

	// Stop the world. Must return within the deadline: the hot-loop agent reaches a safepoint and the four
	// parked agents count as at one. (A Stop that never returns is the other forbidden half.)
	if err := in.Stop(deadline); err != nil {
		t.Fatalf("Stop with %d agents parked in a blocking call plus one spinning: %v — SP-4's "+
			"deadline half failed", n, err)
	}

	// The measured wake counter must be 0 across the held window: SP-4 forbids a wake above 0.
	const samples = 200
	for range samples {
		if w := wakes.Load(); w != 0 {
			t.Fatalf("wake counter = %d while the stop is held — Stop woke a parked agent (SP-4 forbidden: "+
				"stop must complete WITHOUT waking parked agents)", w)
		}
		time.Sleep(100 * time.Microsecond)
	}

	// Lift the stop and release the calls: the parked agents complete via `release`, NOT via a wake, so the
	// counter stays 0 through normal completion — proving the 0 above is a live counter, not an unread one.
	in.Resume()
	close(release)
	for range n {
		select {
		case <-completed:
		case <-time.After(deadline):
			t.Fatalf("only some of %d parked agents completed after Resume+release", n)
		}
	}
	if w := wakes.Load(); w != 0 {
		t.Errorf("wake counter = %d after normal completion — a parked agent was woken (ctx.Done), not "+
			"released; the 0 during the stop would have been vacuous", w)
	}
}
