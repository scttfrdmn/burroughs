// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"testing"
	"time"
)

// readGo instantiates fixture and calls its no-argument `export` n times per gate state, failing on a call
// that does not return within the bound, and returns the u32 results counted.
func readGo(t *testing.T, fixture, export, gate string, n int) map[uint32]int {
	t.Helper()
	t.Setenv(asyncGateEnv, "1")
	t.Setenv(cmThreadsGateEnv, gate)
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	counts := map[uint32]int{}
	for range n {
		in, err := Instantiate(b)
		if err != nil {
			t.Fatalf("Instantiate %s: %v", fixture, err)
		}
		type out struct {
			v   uint32
			err error
		}
		ch := make(chan out, 1)
		go func() {
			res, e := in.CallValues(export)
			if e != nil {
				ch <- out{err: e}
				return
			}
			v, _ := res[0].U32()
			ch <- out{v: v}
		}()
		select {
		case o := <-ch:
			if o.err != nil {
				in.Close()
				t.Fatalf("%s %s() gate=%q: %v", fixture, export, gate, o.err)
			}
			counts[o.v]++
		case <-time.After(optionABound):
			in.Close()
			t.Fatalf("%s %s() gate=%q did not return within %s", fixture, export, gate, optionABound)
		}
		in.Close()
	}
	return counts
}

// TestACalleeResolvedInItsFirstSegmentIsReturnedInline is F0's witness (ADR 0095's amendment, #1020's
// mechanism (c)): a cross-component callee that calls `task.return` and EXITs in its first core call
// resolves before its first suspension, and the reference runs that segment inside the caller's lower, so
// the lower returns RETURNED (2). wasmtime 49.0.3 reads 2, 10 of 10, and the pin {2}
// (`first-segment-return.reading`).
//
// Before F0 the adapter returned when the callee's goroutine started, and this read 1 (STARTED), 200 of 200
// in each gate state. **Neuter, run:** returning without the first-segment wait reads 1, 200 of 200 per gate.
func TestACalleeResolvedInItsFirstSegmentIsReturnedInline(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		got := readGo(t, "testdata/first-segment-return.wasm", "go", gate, 50)
		if got[2] != 50 {
			t.Errorf("gate=%q: lower states %v, want RETURNED (2) every time: the callee resolved in its first "+
				"segment, which runs inside the lower", gate, got)
		}
	}
}

// TestACalleePastItsFirstSuspensionReentersItsCaller is the fence's re-entry rule (F1, added on the chair's
// ruling of 2026-10-17). `g` YIELDs, then from its callback calls its caller's sync `f`: the callback is a
// segment holding the caller's fence, and `f` is the caller's instance executing. The call is on the
// segment's own chain, so the fence admits it. wasmtime 49.0.3 reads 8, 10 of 10, and the pin {8}
// (`reenter-after-suspend.reading`).
//
// **Neuter, run:** a fence that ignores chain identity makes `f` wait on `g`'s own fence, and this fails on
// its bound in both gate states. `residual` hangs under that neuter too, and `thread-wait-callback` does not:
// its re-entry is inside a first segment, which F0's corollary (the caller's chain) admits.
func TestACalleePastItsFirstSuspensionReentersItsCaller(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		got := readGo(t, "testdata/reenter-after-suspend.wasm", "go", gate, 20)
		if got[8] != 20 {
			t.Errorf("gate=%q: results %v, want 8 every time", gate, got)
		}
	}
}
