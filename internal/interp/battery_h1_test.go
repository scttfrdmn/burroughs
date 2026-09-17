// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestH1ABlockingHostCallDoesNotStarveSiblings is the litmus battery case
// `h1-a-parked-agent-does-not-starve-its-siblings` (contract §5 H-1): a blocking host call blocks its
// thread only — no global loop, no starvation of sibling agents.
//
// This is a STRUCTURAL guarantee, and the case witnesses the construction rather than asserting it. Agent A
// parks in a blocking host call; sibling B runs a bounded guest loop (no host call in the loop, so its
// progress is not a boundary artifact) and must complete — advance. The second arm is the evidence that
// makes the structural claim a finding rather than an argument: it constructs the STRONGEST starvation
// attempt available — GOMAXPROCS=1 with A busy-HOLDING its OS thread inside the host call, never yielding to
// the scheduler — and B must STILL complete. It does, at a ~2x slowdown (A steals cycles) but never zero
// progress; Go's async preemption schedules B regardless. The registration's forbidden is "B advances zero
// times," and a merely slow B violates nothing, so the slowdown is logged, not asserted.
//
// The "control" here is watched FAILING to die: the strongest adversary cannot produce the forbidden,
// because the guarantee is architectural (goroutine-per-agent), not a mechanism a line removes. The only
// instrument that could falsify it is a different engine — a cooperative/single-worker scheduler — which is
// what it would take to close the gap, and what this test would catch if Burroughs ever regressed to one.
// Arbiter: neither. Run under -race per the case discipline.
func TestH1ABlockingHostCallDoesNotStarveSiblings(t *testing.T) {
	// B is a bounded guest loop returning its iteration count — progress observed race-free via the return,
	// not a live read of a concurrently-written word. A parks or busy-holds in a host call.
	var spin atomic.Bool // true: A busy-holds its goroutine; false: A parks in Blocking
	var stop atomic.Bool // ends A's spin
	release := make(chan struct{})
	parked := make(chan struct{}, 1)

	hold := CanonLowerExtern(
		ft([]bin.ValType{bin.I32}, nil),
		func(c *CanonCaller, _ []Value) ([]Value, error) {
			if spin.Load() {
				parked <- struct{}{}
				for !stop.Load() { // busy-hold the OS thread — never yield to the scheduler
				}
				return nil, nil
			}
			return nil, c.Blocking(func() error {
				parked <- struct{}{}
				<-release
				return nil
			})
		},
		CanonOptions{},
	)
	in := hostLink(t, `(module
		(import "h" "hold" (func $hold (param i32)))
		(memory 1 1 shared)
		(func (export "hold") (param $a i32) (call $hold (local.get $a)))
		(func (export "count") (param $n i32) (result i32)
			(local $i i32)
			(loop $l
				(local.set $i (i32.add (local.get $i) (i32.const 1)))
				(br_if $l (i32.lt_u (local.get $i) (local.get $n))))
			(local.get $i)))`,
		bin.Features{Threads: true}, hostImports(map[string]Extern{"hold": hold}))
	closeAtEnd(t, in)

	holdEntry := exportedFuncIndex(t, in, "hold")
	const iters = int32(5_000_000)

	// invokeCount runs B (the count export) and returns whether it completed within the bound and how long.
	invokeCount := func(bound time.Duration) (done bool, count int32, took time.Duration) {
		type res struct {
			v   []Value
			err error
		}
		ch := make(chan res, 1)
		start := time.Now()
		go func() {
			v, err := in.Invoke("count", I32(iters))
			ch <- res{v, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("B (count) errored: %v", r.err)
			}
			return true, r.v[0].Int32(), time.Since(start)
		case <-bound2timer(bound):
			return false, 0, time.Since(start)
		}
	}

	// --- Positive: A parks in a blocking host call (releases its goroutine). B must advance. ---
	spin.Store(false)
	if _, err := in.Spawn(holdEntry, 0, 0); err != nil {
		t.Fatalf("Spawn A (park): %v", err)
	}
	<-parked // A confirmed inside the host call, about to block
	done, count, took := invokeCount(30 * time.Second)
	if !done || count != iters {
		t.Fatalf("POSITIVE: B did not complete while A parked (done=%v count=%d) — the witness itself is broken", done, count)
	}
	t.Logf("POSITIVE (A parks): B completed %d iterations in %v while A was parked", count, took)
	close(release) // let A finish

	// --- Attempt: GOMAXPROCS=1, A busy-HOLDS its goroutine in the host call. Does B starve? ---
	prev := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prev)
	spin.Store(true)
	if _, err := in.Spawn(holdEntry, 0, 0); err != nil {
		t.Fatalf("Spawn A (spin): %v", err)
	}
	<-parked // A confirmed spinning in the host call
	done2, count2, took2 := invokeCount(30 * time.Second)
	stop.Store(true) // release A's spin
	if !done2 || count2 != iters {
		t.Fatalf("ATTEMPT (GOMAXPROCS=1, A busy-holds its thread): B did NOT advance (done=%v count=%d) — "+
			"the forbidden (zero progress) was PRODUCED; H-1's guarantee is not structural after all, and "+
			"the case is writable with a bounded assertion. This is the finding, reported not swallowed.", done2, count2)
	}
	// B advanced under the strongest adversary — the guarantee is structural, witnessed. The slowdown is
	// logged, not asserted: a merely slow B violates nothing (registration), and timing is not a verdict.
	t.Logf("ATTEMPT (GOMAXPROCS=1, A busy-holds, never yields): B completed %d iterations REGARDLESS in %v "+
		"(positive %v; ~%.1fx slowdown) — starvation NOT produced; the guarantee is structural "+
		"(goroutine-per-agent), falsifiable only by a cooperative-scheduler engine.", count2, took2, took, float64(took2)/float64(took))
}

func bound2timer(d time.Duration) <-chan time.Time { return time.After(d) }
