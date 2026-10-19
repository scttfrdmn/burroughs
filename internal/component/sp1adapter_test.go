// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// adapterCopyArgLen is the string `run-sync` carries in SP-1's adapter-copy readings: 16 MiB, which each call
// copies four times in host code (testdata/adapter-copy.wat).
const adapterCopyArgLen = 16 << 20

// instantiateAdapterCopy loads the adapter-copy fixture and returns it with the core instance `run-sync`
// executes in. That instance's world is the one whose thread is inside the sync adapter during the copies,
// so it is the one a `Stop` must wait for.
func instantiateAdapterCopy(tb testing.TB) (*Instantiated, *interp.Instance) {
	tb.Helper()
	b, err := os.ReadFile("testdata/adapter-copy.wasm")
	if err != nil {
		tb.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		tb.Fatalf("instantiate: %v", err)
	}
	cd, ok := in.export.exports["run-sync"]
	if !ok || cd.fn == nil || cd.fn.core.inst == nil {
		tb.Fatal(`the fixture's "run-sync" export resolves to no core instance`)
	}
	return in, cd.fn.core.inst
}

// BenchmarkSP1AdapterArrival is SP-1's instrument for the adapter-copy class (ADR 0104, ruling 1), the
// component-level twin of interp's `BenchmarkSP1Arrival`, and like it a benchmark that asserts nothing. A
// guest calls `run-sync` with a 16 MiB string in a loop, and the host stops `run-sync`'s core instance
// `trials` times at arbitrary moments, reporting `arrival-p50-ns`, `arrival-p99-ns` and `arrival-max-ns`. Run
// it with `-benchtime=1x` on janus's `measured` group.
//
// A stop that lands while no call is inside the instance arrives at once, so the low percentiles mostly
// measure that. The adapter's stretch is in the tail, which is why p99 and max are what the forecast reads.
func BenchmarkSP1AdapterArrival(b *testing.B) {
	const trials = 400
	in, core := instantiateAdapterCopy(b)
	defer in.Close()
	arg := canon.Str(strings.Repeat("a", adapterCopyArgLen))
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := in.CallValues("run-sync", arg); err != nil {
				return
			}
		}
	}()

	var arrivals []time.Duration
	rng := rand.New(rand.NewPCG(1, 3))
	for b.Loop() {
		arrivals = arrivals[:0]
		for i := range trials {
			// The host's clock, spun: a point in the guest's run, not a wait for a signal.
			until := time.Now().Add(time.Duration(rng.Int64N(int64(20 * time.Millisecond))))
			for time.Now().Before(until) {
			}
			t0 := time.Now()
			if err := core.Stop(30 * time.Second); err != nil {
				b.Fatalf("trial %d: Stop: %v", i, err)
			}
			d := time.Since(t0)
			core.Resume()
			if i > 0 {
				arrivals = append(arrivals, d)
			}
		}
	}
	slices.Sort(arrivals)
	pct := func(p float64) time.Duration { return arrivals[int(p*float64(len(arrivals)-1))] }
	b.ReportMetric(float64(pct(0.50).Nanoseconds()), "arrival-p50-ns")
	b.ReportMetric(float64(pct(0.99).Nanoseconds()), "arrival-p99-ns")
	b.ReportMetric(float64(arrivals[len(arrivals)-1].Nanoseconds()), "arrival-max-ns")
}

// TestAnAdapterCopyCrossesItsString is the fixture's own witness, so the arrival readings time calls known to
// be right: `run-sync` returns the echoed string's length, which wasmtime 49.0.3 reads as len(s).
func TestAnAdapterCopyCrossesItsString(t *testing.T) {
	in, _ := instantiateAdapterCopy(t)
	defer in.Close()
	for _, n := range []int{12, adapterCopyArgLen} {
		t0 := time.Now()
		res, err := in.CallValues("run-sync", canon.Str(strings.Repeat("a", n)))
		if err != nil {
			t.Fatalf("run-sync of %d bytes: %v", n, err)
		}
		if v, ok := res[0].U32(); len(res) != 1 || !ok || v != uint32(n) {
			t.Errorf("run-sync of %d bytes = %v, want u32 %d", n, res, n)
		}
		t.Logf("run-sync of %d bytes: %s", n, time.Since(t0))
	}
}

// TestAStopDuringAnAdapterCopyArrivesWithinTheBound is SP-1's adapter-copy witness (contract §3, ADR 0104):
// an agent inside ADR 0103's sync adapter, copying a 24 MiB string, reaches a safepoint within `bound` of a
// `Stop`, not after the copy.
//
// **The guest's loop is the adapter and almost nothing else.** It invokes `run-sync`'s core function
// directly, over a string placed once in the parent's memory, rather than calling the export: an export call
// lowers its argument on the embedder's goroutine, outside any agent, and a stop landing there arrives at
// once. That diluted the instrument's low percentiles, and it would let a neuter pass by luck here. The
// string is placed at 1024, where the fixture's bump allocator starts. Each echo is the same bytes, so the
// allocator's wrap over that region rewrites what is already there.
//
// **The bound is 2ms, and it is set by the neuter.** Locally (darwin/arm64) one unpolled 24 MiB copy is
// about 4ms, so a 10ms bound passed under the neuter: run, and it passed 3 of 3. The polled arrivals'
// slowest was about 0.07ms in 20 runs. Under `-race` the bound scales by `raceSlowdown`.
//
// **Neuter, run:** `pollCaller` returning at once, which removes every poll the adapter's heaps make. A stop
// then waits for the copy in progress to end, and misses its 2ms deadline and fails, by trial 4 in 5 of 5
// runs. `Close` ends the guest at its next frame entry.
func TestAStopDuringAnAdapterCopyArrivesWithinTheBound(t *testing.T) {
	const n = 24 << 20
	in, core := instantiateAdapterCopy(t)
	defer in.Close()
	if res, err := in.CallValues("run-sync", canon.Str(strings.Repeat("a", n))); err != nil {
		t.Fatalf("placing the string: %v", err)
	} else if v, ok := res[0].U32(); !ok || v != n {
		t.Fatalf("placing the string: run-sync = %v, want u32 %d", res, n)
	}
	name := in.export.exports["run-sync"].fn.core.name
	go func() {
		for {
			if _, err := core.Invoke(name, interp.I32(1024), interp.I32(n)); err != nil {
				return
			}
		}
	}()
	if err := core.Stop(30 * time.Second); err != nil {
		t.Fatalf("the first Stop: %v", err)
	}
	core.Resume()

	const (
		bound  = 2 * time.Millisecond * raceSlowdown
		trials = 20
	)
	rng := rand.New(rand.NewPCG(1, 4))
	var worst time.Duration
	for i := range trials {
		// The host's clock, spun: a point in the guest's run, not a wait for a signal.
		until := time.Now().Add(time.Duration(rng.Int64N(int64(40 * time.Millisecond))))
		for time.Now().Before(until) {
		}
		t0 := time.Now()
		err := core.Stop(bound)
		d := time.Since(t0)
		core.Resume()
		if err != nil {
			t.Fatalf("trial %d: Stop(%s) on an agent inside a sync adapter copy returned %v after %s; the "+
				"adapter's heaps poll between %d-byte chunks, so a miss means a copy ran unpolled",
				i, bound, err, d, canon.StringChunk)
		}
		worst = max(worst, d)
	}
	t.Logf("the slowest of %d arrivals: %s", trials, worst)
}
