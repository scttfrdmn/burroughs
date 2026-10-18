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
