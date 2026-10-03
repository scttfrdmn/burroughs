// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// computeReading is where the oracle's answer lives. Parsed from the committed file rather than restated
// here, so the expected values cannot drift from what wasmtime actually said — a literal beside a
// committed reading is a second account of one fact.
const computeReading = "testdata/asynclift/compute.reading"

// wantFromReading pulls an expected result out of the committed wasmtime reading.
//
// It FAILS rather than defaults when the line is absent: a witness that silently falls back to a built-in
// expectation is checking itself.
func wantFromReading(t *testing.T, call string) string {
	t.Helper()
	b, err := os.ReadFile(computeReading)
	if err != nil {
		t.Fatalf("the committed wasmtime reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, call+" ->") {
			return strings.TrimSpace(strings.TrimPrefix(ln, call+" ->"))
		}
	}
	t.Fatalf("the committed reading has no %q line; regenerate it with build.sh", call)
	return ""
}

// TestCallValuesMatchesWasmtimesComputeReading is #864's parity witness.
//
// # Why two pairs, and why nothing else is asserted
//
// The two pairs alone exclude every defect a single pair would admit:
//
//	a guest returning a constant          would read the same value twice
//	a guest returning its argument        would read 5 -> 5 and 41 -> 42's input unchanged
//	a guest incrementing correctly        reads 5 -> 6 and 41 -> 42
//
// So **no separate instrumentation is added for "the argument reached the guest"** — the pair settles it,
// and a third mechanism asserting the same fact would be a second witness of one thing, which is the
// duplication rule in its executable form (recorded in #866). This paragraph exists because a later
// reader would otherwise add that instrumentation back, reasonably, for want of the argument.
func TestCallValuesMatchesWasmtimesComputeReading(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/asynclift/single/component.wasm")
	if err != nil {
		t.Fatalf("the committed single guest is missing: %v", err)
	}
	for _, c := range []struct{ in uint32 }{{5}, {41}} {
		h := NewHost(io.Discard, io.Discard, nil)
		in, err := InstantiateWithHost(b, h)
		if err != nil {
			t.Fatalf("compute(%d): instantiate: %v", c.in, err)
		}
		// The PATH form, because the guest's world exports an interface: the top-level export is the
		// instance `test:probe/ops@0.1.0` and `compute` lives inside it. A bare name is deliberately not
		// resolved by searching instances — see `resolveValueExport`.
		got, err := in.CallValues("test:probe/ops@0.1.0#compute", canon.U32(c.in))
		if err != nil {
			in.Close()
			t.Fatalf("compute(%d): %v", c.in, err)
		}
		in.Close()
		if len(got) != 1 {
			t.Fatalf("compute(%d) returned %d values, want 1", c.in, len(got))
		}
		v, ok := got[0].U32()
		if !ok {
			t.Fatalf("compute(%d) returned kind %v, want u32", c.in, got[0].Type.Kind)
		}
		// The expectation comes from the committed reading, not from a literal here.
		want := wantFromReading(t, "compute("+itoa(c.in)+")")
		if itoa(v) != want {
			t.Errorf("compute(%d) = %d, wasmtime's committed reading says %s", c.in, v, want)
		}
	}
}

// TestCallValuesRefusesComputeWithTheAsyncGateOff is the reading's third arm.
//
// # Why this arm exists
//
// Without it the two results above are consistent with the guest running on a SYNC fallback path, and a
// Burroughs result matching wasmtime's would say nothing about the async tier. wasmtime establishes the
// same thing with `-W component-model-async=n`, which refuses by name.
//
// # Matched as an outcome, not word for word — and the stage differs on purpose
//
// wasmtime refuses at PARSE (`failed to parse WebAssembly module`, caused by "`task.return` requires the
// component model async feature"). Burroughs' `gateAsync` refuses at INSTANTIATE. Both refuse before
// anything runs and both name the async feature as the reason, which is the parity that matters.
//
// Forcing the same stage would mean moving where a gate fires in order to match another engine's
// layering — a worse trade than recording the difference, which is what this comment is for.
func TestCallValuesRefusesComputeWithTheAsyncGateOff(t *testing.T) {
	// **The gate is ON by default** — `asyncEnabled()` is `os.Getenv(asyncGateEnv) != "0"`, flipped
	// 2026-09-18 — so turning it off means setting it to "0", not leaving it unset. The first version of
	// this test left it unset and got `ErrAsyncNotImplemented` ("gate:async is on"), which is a different
	// refusal entirely.
	t.Setenv(asyncGateEnv, "0")
	b, err := os.ReadFile("testdata/asynclift/single/component.wasm")
	if err != nil {
		t.Fatalf("the committed single guest is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	in, err := InstantiateWithHost(b, h)
	if err == nil {
		in.Close()
		t.Fatal("instantiate PERMITTED an async-lifted guest with gate:async off; wasmtime refuses the " +
			"same guest when the async feature is disabled, and the gate is the engine's equivalent")
	}
	// The refusal must be the GATE's, not an incidental failure: a load error for some other reason would
	// satisfy a bare `err != nil` and tell us nothing about the async tier.
	if !errors.Is(err, ErrAsyncGated) {
		t.Errorf("refused, but not with ErrAsyncGated — so not the gate's refusal: %v", err)
	}
	t.Logf("gate off: %v", err)
}

// itoa avoids importing strconv for two call sites in a file that otherwise needs none.
func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var d [10]byte
	i := len(d)
	for v > 0 {
		i--
		d[i] = byte('0' + v%10)
		v /= 10
	}
	return string(d[i:])
}
