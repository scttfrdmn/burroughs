// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// h1Guard is how long the witness waits for B's first iteration before it reports zero progress. It is a hang guard
// and not a measurement: a B that advances at all crosses it in its first iteration, so only a B that is never
// scheduled reaches it. Scaled under `-race` (raceGuardScale).
const h1Guard = 30 * time.Second * raceGuardScale

// The words of the fixture's memory that the host reads and writes: B's counter, which its loop stores after every
// iteration, and the stop word its loop reads.
const (
	h1CounterAddr = 0
	h1StopAddr    = 4
)

// TestH1ABlockingHostCallDoesNotStarveSiblings is the litmus battery case
// `h1-a-parked-agent-does-not-starve-its-siblings` (contract §5 H-1): a blocking host call blocks its
// thread only — no global loop, no starvation of sibling agents.
//
// This is a STRUCTURAL guarantee, and the case witnesses the construction rather than asserting it. Agent A
// parks in a blocking host call; sibling B runs a guest loop (no host call in the loop, so its progress is not a
// boundary artifact) and must advance. The second arm is the evidence that makes the structural claim a finding
// rather than an argument: it constructs the STRONGEST starvation attempt available — GOMAXPROCS=1 with A
// busy-HOLDING its OS thread inside the host call, never yielding to the scheduler — and B must STILL advance. Go's
// async preemption schedules B regardless.
//
// # What is asserted, and where it is read
//
// **The registration's forbidden is "B advances zero times", so the witness reads B's progress at a place**: B's
// counter, a word of shared memory its loop stores atomically after every iteration, which the host samples through
// the same word operation. An arm passes when the counter reads above zero while A holds, and B is then stopped
// through the stop word and joined. A merely slow B violates nothing, so its rate is logged, never asserted. That is
// the preregistered witness (`docs/litmus-battery-preregistration.md`): a progress counter sampled after A is
// confirmed parked. Its held interval, T = 100 ms, is logged rather than asserted: under GOMAXPROCS=1 the sampler is
// scheduled by the same preemption as B, so the time of the first reading measures the runner.
//
// It asked a duration until #1402: whether B finished 5,000,000 iterations within 30 s, with a miss reported as zero
// progress and a count read only at completion. #1401's race run on ubuntu-24.04 took 13.34 s for the positive arm,
// and the attempt arm, at about twice that, missed the bound and printed "B did NOT advance (count=0)"; the re-run on
// the same SHA passed. The witness turned a slow runner into H-1's forbidden. A miss now prints the count the
// counter actually reached, and B's place.
//
// The "control" here is watched FAILING to die: the strongest adversary cannot produce the forbidden,
// because the guarantee is architectural (goroutine-per-agent), not a mechanism a line removes. The only
// instrument that could falsify it is a different engine — a cooperative/single-worker scheduler — which is
// what it would take to close the gap, and what this test would catch if Burroughs ever regressed to one.
// Arbiter: neither. Run under -race per the case discipline.
//
// NEUTER, run: B blocked before its loop (`count` led by a `memory.atomic.wait32` nobody notifies). The positive arm
// fails at h1Guard: its counter read 0, and B's place is its goroutine parked in `(*memory).wait` under `atomicWait`.
// The witness reads zero progress at its place.
// NEUTER, run: B slowed by a 200,000-iteration inner loop before every counter store, so one counted iteration took
// about 11 ms plain and 227 ms under `-race`, and the old arm's 5,000,000 would take hours. Both arms pass, plain and
// under `-race`, reading the counter at 1 and logging it: the repaired witness does not read a slow B as zero.
// NEUTER, considered and not run: A holding the only P with no preemption at all (GODEBUG=asyncpreemptoff=1, its
// spin having no call in it). That starves the whole process, the sampling goroutine and the timers included, so no
// in-process witness can read it.
func TestH1ABlockingHostCallDoesNotStarveSiblings(t *testing.T) {
	// B is a guest loop that publishes its iteration count in shared memory and runs until the stop word is set. A
	// parks or busy-holds in a host call.
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
	in := hostLink(t, fmt.Sprintf(`(module
		(import "h" "hold" (func $hold (param i32)))
		(memory 1 1 shared)
		(func (export "hold") (param $a i32) (call $hold (local.get $a)))
		(func (export "count") (result i32)
			(local $i i32)
			(loop $l
				(local.set $i (i32.add (local.get $i) (i32.const 1)))
				(i32.atomic.store (i32.const %d) (local.get $i))
				(br_if $l (i32.eqz (i32.atomic.load (i32.const %d)))))
			(local.get $i)))`, h1CounterAddr, h1StopAddr),
		bin.Features{Threads: true}, hostImports(map[string]Extern{"hold": hold}))
	closeAtEnd(t, in)

	holdEntry := exportedFuncIndex(t, in, "hold")
	counter := h1Cell(t, in, h1CounterAddr)
	stopB := h1Cell(t, in, h1StopAddr)
	// A failing arm returns with A still holding, and A's host call does not observe Close. So A is let go before
	// closeAtEnd's Close runs (cleanups run last-registered first), whichever arm the test ended in.
	var releaseOnce sync.Once
	letAGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		letAGo()
		stop.Store(true)
		stopB.store(1)
	})

	// advance runs B and reads its progress at its place: the counter, sampled until it reads above zero or the guard
	// expires. It then stops B through the stop word and joins it, and returns the first reading above zero, B's own
	// returned count, and how long the first reading took. A counter still at zero at the guard is the forbidden.
	advance := func(arm string) (first uint64, returned int32, took time.Duration) {
		t.Helper()
		counter.store(0)
		stopB.store(0)
		type res struct {
			v   []Value
			err error
		}
		ch := make(chan res, 1)
		start := time.Now()
		go func() {
			v, err := in.Invoke("count")
			ch <- res{v, err}
		}()
		deadline := start.Add(h1Guard)
		for first = counter.load(); first == 0 && time.Now().Before(deadline); first = counter.load() {
			time.Sleep(time.Millisecond) // a sample interval: the reading is the counter, not this
		}
		took = time.Since(start)
		stopB.store(1) // B leaves its loop at its next iteration, wherever that is
		if first == 0 {
			t.Fatalf("%s: B did NOT advance: its counter read 0 after %v (h1Guard), the registration's forbidden "+
				"(zero progress) PRODUCED — H-1's guarantee is not structural after all. B's place: %s",
				arm, h1Guard, h1Place())
		}
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("%s: B (count) errored after advancing to %d: %v", arm, first, r.err)
			}
			returned = r.v[0].Int32()
		case <-time.After(h1Guard):
			t.Fatalf("%s: B advanced (its counter read %d, last %d) and then did not return within %v of its stop "+
				"word being set. B's place: %s", arm, first, counter.load(), h1Guard, h1Place())
		}
		if uint64(uint32(returned)) < first {
			t.Fatalf("%s: B returned %d, below the %d its counter read while it ran", arm, returned, first)
		}
		return first, returned, took
	}

	// --- Positive: A parks in a blocking host call (releases its goroutine). B must advance. ---
	spin.Store(false)
	if _, err := in.Spawn(holdEntry, 0, 0); err != nil {
		t.Fatalf("Spawn A (park): %v", err)
	}
	<-parked // A confirmed inside the host call, about to block
	first, returned, took := advance("POSITIVE (A parks)")
	t.Logf("POSITIVE (A parks): B's counter read %d after %v, and B returned %d once stopped", first, took, returned)
	letAGo() // let A finish

	// --- Attempt: GOMAXPROCS=1, A busy-HOLDS its goroutine in the host call. Does B starve? ---
	prev := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prev)
	spin.Store(true)
	if _, err := in.Spawn(holdEntry, 0, 0); err != nil {
		t.Fatalf("Spawn A (spin): %v", err)
	}
	<-parked // A confirmed spinning in the host call, before B starts, so all of B's progress is under A's hold
	first2, returned2, took2 := advance("ATTEMPT (GOMAXPROCS=1, A busy-holds its thread)")
	stop.Store(true) // release A's spin
	// B advanced under the strongest adversary — the guarantee is structural, witnessed. The rate is logged, not
	// asserted: a merely slow B violates nothing (registration), and timing is not a verdict.
	t.Logf("ATTEMPT (GOMAXPROCS=1, A busy-holds, never yields): B's counter read %d after %v, and B returned %d "+
		"once stopped — starvation NOT produced; the guarantee is structural (goroutine-per-agent), falsifiable "+
		"only by a cooperative-scheduler engine.", first2, took2, returned2)
}

// h1Cell reaches a word of the fixture's shared memory as an `atomicCell`, so the host's reads and writes use the
// word operation the guest's atomics do (mixedCounter's form, for its reason).
func h1Cell(t *testing.T, in *Instance, addr uint64) atomicCell {
	t.Helper()
	c, err := in.mems[0].cell(addr, 0, 4)
	if err != nil {
		t.Fatalf("reaching the word at %d: %v", addr, err)
	}
	return c
}

// h1Place names where every goroutine running an interp frame is, by its status line and its four innermost interp
// frames, for a miss that must say whether B was parked, runnable or running.
func h1Place() string {
	var stacks string
	for n := 1 << 20; ; n *= 2 {
		buf := make([]byte, n)
		if k := runtime.Stack(buf, true); k < n {
			stacks = string(buf[:k])
			break
		}
	}
	var b strings.Builder
	for _, g := range strings.Split(stacks, "\n\n") {
		if !strings.Contains(g, "burroughs/internal/interp.") {
			continue
		}
		head, _, _ := strings.Cut(g, "\n")
		b.WriteString("\n  " + head)
		frames := 0
		for _, ln := range strings.Split(g, "\n") {
			if frames < 4 && strings.Contains(ln, "burroughs/internal/interp.") && !strings.HasPrefix(ln, "\t") {
				fn, _, _ := strings.Cut(ln, "(0x")
				b.WriteString("\n    " + strings.TrimPrefix(fn, "github.com/scttfrdmn/burroughs/internal/"))
				frames++
			}
		}
	}
	return b.String()
}
