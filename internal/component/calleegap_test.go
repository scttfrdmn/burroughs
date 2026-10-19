// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The callee-execution gap's witnesses (contract §3 SP-1; ADR 0104's amendment of 2026-10-18, the chair's third
// ruling of that day, item 3). A `Stop` of a caller's instance must not wait for a sync cross-component callee to
// return. A callee is another instance, another world, and polls only its own. So the caller's agent holds a
// blocked mark across the callee's execution, and every re-entry into the caller polls before it runs.

// calleeGap loads testdata/callee-gap.wasm and returns it with the caller's core instance (the one `run-spin`
// and `run-cb` execute in) and the child's.
func calleeGap(t *testing.T) (in *Instantiated, caller, child *interp.Instance) {
	t.Helper()
	b, err := os.ReadFile("testdata/callee-gap.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err = Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	for _, c := range []struct {
		export string
		args   []canon.Value
		want   uint32
	}{
		// wasmtime 49.0.3's readings (testdata/callee-gap.wat), on a fresh instance each, as wasmtime reads them.
		{"run-spin", []canon.Value{canon.U32(3), canon.U32(10)}, 3},
	} {
		if res, err := in.CallValues(c.export, c.args...); err != nil {
			t.Fatalf("%s: %v", c.export, err)
		} else if v, ok := res[0].U32(); !ok || v != c.want {
			t.Fatalf("%s = %v, want u32 %d, wasmtime's reading", c.export, res, c.want)
		}
	}
	cd, ok := in.export.exports["run-spin"]
	if !ok || cd.fn == nil || cd.fn.core.inst == nil {
		t.Fatal(`the fixture's "run-spin" export resolves to no core instance`)
	}
	sd, ok := in.export.exports["spin"]
	if !ok || sd.fn == nil || sd.fn.core.inst == nil {
		t.Fatal(`the fixture's "spin" export resolves to no core instance`)
	}
	return in, cd.fn.core.inst, sd.fn.core.inst
}

// word reads the little-endian i32 at off in inst's exported memory "m".
func word(t *testing.T, inst *interp.Instance, off uint64) uint32 {
	t.Helper()
	m, ok := inst.Export("m")
	if !ok {
		t.Fatal(`the core instance exports no memory "m"`)
	}
	b, err := interp.ReadBoundaryMemory(&m, off, 4)
	if err != nil {
		t.Fatalf("reading word %d: %v", off, err)
	}
	return binary.LittleEndian.Uint32(b)
}

// TestAStopOfTheCallerDoesNotWaitForItsCallee: a caller whose sync callee is in a tight loop with no calls is
// stopped within the tight-loop witness's bound, not when the callee returns. Its memory is unchanged until
// `Resume`, including after the callee has returned: its agent parks at the end of the blocked span, before
// the adapter's result copy and before its own next instruction.
//
// The callee's spin is long, several hundred milliseconds of tight trips, so a stop that waited for it misses
// the bound. The child's own memory counts each finished spin, and it is read under the child's own stop, so the
// witness knows the callee returned while the caller was stopped.
//
// **Neuter, run:** no blocked mark across the callee's execution (`underCallerMark` running plainly), and the
// stop misses its bound on trial 0, in 2 of 2 runs.
func TestAStopOfTheCallerDoesNotWaitForItsCallee(t *testing.T) {
	in, caller, child := calleeGap(t)
	defer in.Close()

	// Calibrate the spin to about 300ms on this machine: long enough that a stop which waited for it misses
	// the 100ms bound, and short enough that a stop held for 1.5 spins sees the callee return.
	const probe = 1_000_000
	t0 := time.Now()
	if _, err := in.CallValues("spin", canon.U32(probe)); err != nil {
		t.Fatalf("calibrating the spin: %v", err)
	}
	per := time.Since(t0)
	trips := max(probe, int(probe*int64(300*time.Millisecond)/int64(max(per, time.Microsecond))))
	spin := time.Duration(int64(per) * int64(trips) / probe)

	childDone := func() uint32 {
		if err := child.Stop(30 * time.Second); err != nil {
			t.Fatalf("stopping the child to read it: %v", err)
		}
		defer child.Resume()
		return word(t, child, 0)
	}
	cd := in.export.exports["run-spin"]
	go func() { _, _ = caller.Invoke(cd.fn.core.name, interp.I32(1_000_000), interp.I32(int32(trips))) }()
	// Under way: one spin of the loop's has finished.
	for start := childDone(); childDone() == start; {
		time.Sleep(5 * time.Millisecond)
	}

	const bound = 100 * time.Millisecond // the tight-loop witness's: far above noise, far below one spin
	returned := 0
	for trial := range 5 {
		time.Sleep(time.Duration(trial) * spin / 5) // land at different points of a spin
		t0 := time.Now()
		err := caller.Stop(bound)
		d := time.Since(t0)
		if err != nil {
			caller.Resume()
			t.Fatalf("trial %d: Stop(%s) of a caller whose callee is spinning returned %v after %s; the "+
				"caller's agent must hold a blocked mark across the callee's execution (ADR 0104's amendment)",
				trial, bound, err, d)
		}
		progress, done := word(t, caller, 0), childDone()
		time.Sleep(spin * 3 / 2) // the callee, another world, may finish its spin meanwhile
		after, doneAfter := word(t, caller, 0), childDone()
		caller.Resume()
		if after != progress {
			t.Fatalf("trial %d: the stopped caller's progress word moved from %d to %d; its agent must park at "+
				"the end of the blocked span, before any of its code runs", trial, progress, after)
		}
		if doneAfter > done {
			returned++
		}
		t.Logf("trial %d: the stop arrived in %s; the callee finished %d spin(s) while the caller was stopped",
			trial, d, doneAfter-done)
	}
	// The case the ruling names: the callee returned while the caller was stopped, and the caller still did not
	// move. A stop that landed before the span parks the caller at its entry instead, and the callee never
	// starts, which is also correct. So this asks only that the first case happened at least once.
	if returned == 0 {
		t.Fatalf("in no trial did the callee finish a spin while the caller was stopped (spin about %s), so the "+
			"after-return half of this witness was never exercised", spin)
	}
}

// TestAStopOfTheCallerParksACallbackBeforeItRuns: a caller stopped while its callee calls back into it parks the
// callback before any of its code runs. The callback's only effect is a counter in the caller's memory, so the
// counter is frozen across the stop, while the callee, another world, keeps running and keeps calling.
//
// **Neuter, run:** no poll at frame entry (`enterFrame`), so the callback runs while the caller is stopped. The
// counter moved by about 140 in 50ms, in 3 of 3 runs.
func TestAStopOfTheCallerParksACallbackBeforeItRuns(t *testing.T) {
	in, caller, _ := calleeGap(t)
	defer in.Close()
	cd := in.export.exports["run-cb"]
	go func() { _, _ = caller.Invoke(cd.fn.core.name, interp.I32(1_000_000), interp.I32(1_000_000)) }()
	// Wait for callbacks to be arriving, read under the caller's own stop so the read is ordered.
	readCB := func() uint32 {
		if err := caller.Stop(30 * time.Second); err != nil {
			t.Fatalf("stopping the caller to read it: %v", err)
		}
		defer caller.Resume()
		return word(t, caller, 4)
	}
	for readCB() < 10 {
	}

	for trial := range 3 {
		time.Sleep(5 * time.Millisecond)
		if err := caller.Stop(time.Second); err != nil {
			caller.Resume()
			t.Fatalf("trial %d: Stop of a caller whose callee is calling back into it: %v", trial, err)
		}
		before := word(t, caller, 4)
		time.Sleep(50 * time.Millisecond) // the callee calls back every few hundred microseconds
		after := word(t, caller, 4)
		caller.Resume()
		if after != before {
			t.Fatalf("trial %d: the callback counter moved from %d to %d while the caller was stopped; a "+
				"callback into a stopped caller must park at its entry, before its code runs", trial, before, after)
		}
	}
}
