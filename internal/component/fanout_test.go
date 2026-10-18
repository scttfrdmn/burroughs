// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// fanoutIters is each callee's work: a countdown long enough that four of them overlapping or not is the
// dominant term in a run, and short enough that a benchmark iteration stays under a second.
const fanoutIters = 200_000

func instantiateFanOut(tb testing.TB) *Instantiated {
	tb.Helper()
	// gate:async on and gate:cm-threads explicitly off, as every published fan-out figure was taken before
	// the cm-threads flip made it default-on; the fixture uses no Threads, so the gate does not change its path.
	tb.Setenv(asyncGateEnv, "1")
	tb.Setenv(cmThreadsGateEnv, "0")
	wasm, err := os.ReadFile("testdata/fanout/fanout.wasm")
	if err != nil {
		tb.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		tb.Fatalf("Instantiate: %v", err)
	}
	return in
}

// TestFanOutReturnsEveryCalleesResult is the fixture's own witness, so `BenchmarkFanOut` times a run that
// is known to be right: one caller, k async callees, and the sum of their results, which wasmtime 49.0.2
// reads as k*iters for both modes (testdata/fanout/README.md).
func TestFanOutReturnsEveryCalleesResult(t *testing.T) {
	in := instantiateFanOut(t)
	defer in.Close()
	for _, mode := range []uint32{0, 1} {
		for _, k := range []uint32{1, 4} {
			res, err := in.CallValues("go", canon.U32(k), canon.U32(mode), canon.U32(1000))
			if err != nil {
				t.Fatalf("go(%d, %d, 1000): %v", k, mode, err)
			}
			if v, ok := res[0].U32(); len(res) != 1 || !ok || v != k*1000 {
				t.Errorf("go(%d, %d, 1000) = %v, want u32 %d", k, mode, res, k*1000)
			}
		}
	}
}

// BenchmarkFanOut is the parallelism a cross-component fence would give up, measured before the build (the
// chair's ruling of 2026-10-17 on ADR 0095's amendment). `first` puts each callee's work in its first
// segment, which the reference runs inside the caller's lower; `after` puts it past a YIELD, where the
// reference runs it only once the caller has suspended. k=1 against k=4 is the overlap.
func BenchmarkFanOut(b *testing.B) {
	in := instantiateFanOut(b)
	defer in.Close()
	for _, mode := range []struct {
		name string
		v    uint32
	}{{"first", 0}, {"after", 1}} {
		for _, k := range []uint32{1, 4} {
			b.Run(fmt.Sprintf("work=%s/k=%d", mode.name, k), func(b *testing.B) {
				// **Every iteration's sum is checked, not only the witness's** (the chair's ruling of
				// 2026-10-17 on the `after` rows' gain). The sum is k*iters only if every callee ran its
				// countdown and its result was stored, so an iteration that skipped work fails here rather
				// than timing as a faster one.
				want := k * fanoutIters
				for b.Loop() {
					res, err := in.CallValues("go", canon.U32(k), canon.U32(mode.v), canon.U32(fanoutIters))
					if err != nil {
						b.Fatalf("go: %v", err)
					}
					if v, ok := res[0].U32(); !ok || v != want {
						b.Fatalf("go(%d, %d, %d) = %v, want u32 %d: a callee's work or its store was skipped",
							k, mode.v, fanoutIters, res, want)
					}
				}
			})
		}
	}
}
