// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// BenchmarkSP1Arrival is SP-1's measuring instrument, ADR 0104's ruling 1: *"the worst case measured by
// instruction class"*. It is not a witness. It asserts nothing about the engine, and it reports `Stop`'s
// arrival latency as the metrics `arrival-p50-ns`, `arrival-p99-ns` and `arrival-max-ns`, one sub-benchmark
// per class. Run it with `-benchtime=1x` on janus's `measured` group: each run is already `trials` stops.
//
// **A benchmark rather than a test**, so it runs only under `-bench` and needs no skip.
// `TestEverySkipSiteIsLicensed` refuses a bare `t.Skip`, which is what an env-gated test would have needed.
//
// # How a worst case is measured without a "the guest is inside X" signal
//
// One long-running guest per class is stopped `trials` times at arbitrary moments: `Stop`, time its
// return, `Resume`, and repeat. The request is sticky, so each stop lands wherever the guest happens to be,
// and the maximum over many trials estimates the longest unpolled stretch the class has. The first trial is
// discarded, because the guest may not have entered its loop yet.
//
// # The classes (contract §3 SP-1; ADR 0104's *Context*)
//
//   - **tight**: an arithmetic loop with a back-edge per trip. This is the poll's own latency.
//   - **straight**: a long straight-line body, called in a loop. It is unpolled from its frame entry to its
//     return.
//   - **fill** and **copy**: one large `memory.fill` or `memory.copy` per trip. A bulk instruction has no
//     poll inside it, so its stretch scales with its operand.
func BenchmarkSP1Arrival(b *testing.B) {
	const trials = 400
	// 1024 pages is 64 MiB: a fill of the whole memory, and a copy of half of it.
	straight := strings.Repeat("(local.set 0 (i32.add (local.get 0) (i32.const 1)))\n", 20000)
	src := `(module
		(memory 1024 1024)
		(func (export "tight") (param i32) (result i32) (local i32)
			(loop (local.set 1 (i32.add (local.get 1) (i32.const 1)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(local.get 1))
		(func $body (param i32) (result i32) ` + straight + ` (local.get 0))
		(func (export "straight") (param i32) (result i32) (local i32)
			(loop (local.set 1 (call $body (local.get 1)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(local.get 1))
		(func (export "fill") (param i32) (result i32)
			(loop (memory.fill (i32.const 0) (i32.const 7) (i32.const 67108864))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "copy") (param i32) (result i32)
			(loop (memory.copy (i32.const 0) (i32.const 33554432) (i32.const 33554432))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		b.Fatalf("encoding the measurement module: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		b.Fatalf("decoding the measurement module: %v", err)
	}

	for _, class := range []struct {
		export string
		trips  int32 // enough that the guest outlives every trial
		// gap is the longest the host lets the guest run between a `Resume` and the next `Stop`. It is a
		// uniform random draw in [0, gap), so each stop lands at an arbitrary point of the class's stretch.
		// It must exceed that stretch: with no gap, the next `Stop` found the guest still parked from the
		// last, and the first draft's p50 of 41ns measured exactly that.
		gap time.Duration
	}{
		{"tight", 2_000_000_000, 2 * time.Millisecond},
		{"straight", 2_000_000_000, 4 * time.Millisecond},
		{"fill", 1_000_000, 200 * time.Millisecond},
		{"copy", 1_000_000, 50 * time.Millisecond},
	} {
		b.Run("class="+class.export, func(b *testing.B) {
			in, trap := mustInst(b, m)
			if trap != nil {
				b.Fatalf("instantiate: %v", trap)
			}
			go func() { _, _ = in.Invoke(class.export, I32(class.trips)) }()
			defer func() { _ = in.Close() }()

			var arrivals []time.Duration
			rng := rand.New(rand.NewPCG(1, uint64(len(class.export))))
			for b.Loop() {
				arrivals = arrivals[:0]
				for i := range trials {
					// The instrument's sampling interval: the host's clock, spun rather than slept, because what is
					// being chosen is a point in the guest's run, not a wait for a signal.
					until := time.Now().Add(time.Duration(rng.Int64N(int64(class.gap))))
					for time.Now().Before(until) {
					}
					t0 := time.Now()
					if err := in.Stop(30 * time.Second); err != nil {
						b.Fatalf("trial %d: Stop: %v", i, err)
					}
					d := time.Since(t0)
					in.Resume()
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
		})
	}
}
