// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// stringEchoArg is the round-trip's payload. Long enough that the copies dominate a call's fixed cost, and
// short enough that a benchmark iteration stays well under a millisecond.
const stringEchoArg = "a string that crosses two component boundaries in each direction, every iteration"

func instantiateStringEcho(tb testing.TB) *Instantiated {
	tb.Helper()
	tb.Setenv(asyncGateEnv, "") // the default, which is on: the async arm needs it
	b, err := os.ReadFile("testdata/string-echo.wasm")
	if err != nil {
		tb.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		tb.Fatalf("instantiate: %v", err)
	}
	return in
}

// TestAStringEchoCrossesBothArms is the cost fixture's own witness, so `BenchmarkSyncStringRoundTrip` times
// runs known to be right. `run-sync` sends the string through ADR 0103's adapter (a sync lift's spilled result),
// and `run-async` through `task.return`. Each returns the echoed string's length, and wasmtime 49.0.3 reads
// len(s) for both.
func TestAStringEchoCrossesBothArms(t *testing.T) {
	in := instantiateStringEcho(t)
	defer in.Close()
	for _, export := range []string{"run-sync", "run-async"} {
		res, err := in.CallValues(export, canon.Str(stringEchoArg))
		if err != nil {
			t.Fatalf("%s: %v", export, err)
		}
		if v, ok := res[0].U32(); len(res) != 1 || !ok || v != uint32(len(stringEchoArg)) {
			t.Errorf("%s = %v, want u32 %d", export, res, len(stringEchoArg))
		}
	}
}

// BenchmarkSyncStringRoundTrip is ADR 0103's cost reading (the chair's ruling of 2026-10-18): a string round
// trip through the sync adapter, beside the way the same value crossed before it, an async callback lift
// returning it through `task.return`. Each iteration's length is checked, so a skipped copy fails rather than
// timing as a faster one.
//
// **There is no base arm on main for `path=sync`**, because main refused the sync path by name. So the reading
// is within one tree: `benchstat -col /path` over a janus run. As registered on #995, no direction is
// forecast. This is the path's first measurement, not a bar.
func BenchmarkSyncStringRoundTrip(b *testing.B) {
	in := instantiateStringEcho(b)
	defer in.Close()
	want := uint32(len(stringEchoArg))
	for _, arm := range []struct{ name, export string }{{"sync", "run-sync"}, {"async", "run-async"}} {
		b.Run("path="+arm.name, func(b *testing.B) {
			for b.Loop() {
				res, err := in.CallValues(arm.export, canon.Str(stringEchoArg))
				if err != nil {
					b.Fatalf("%s: %v", arm.export, err)
				}
				if v, ok := res[0].U32(); !ok || v != want {
					b.Fatalf("%s = %v, want u32 %d: a copy was skipped", arm.export, res, want)
				}
			}
		})
	}
}
