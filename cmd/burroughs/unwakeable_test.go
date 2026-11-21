// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// unwakeablePeriod is how long a child must stay alive after it starts the guest. The children run at
// GOMAXPROCS=1, where the defect's Go fatal fires as soon as the wait is reached (testenv.RunHangChild says why),
// so the period is a margin over that rather than a wait for it: with the fix removed, every hang arm here ended
// 4 to 5 ms after starting the guest. It is short because every hang arm waits it out, in parallel.
const unwakeablePeriod = 2 * time.Second

// unwakeableRun is a `burroughs run` invocation of one of #1303's guests, with the committed wasmtime reading it is
// judged against and the frame a hang must be parked in.
type unwakeableRun struct {
	name    string
	args    []string
	reading string // the .reading file, read by name
	// frame is a substring of the frame a hang's goroutine dump must show. It is the function's bare name, not a
	// package-qualified one: a closure's printed name differs by toolchain. go1.27.4 printed
	// `component.waitableSetWait`, and go1.26.9, CI's, prints `component.(*walker).asyncBuiltinFunc.waitableSetWait.func2`
	// (the first `make ci` of #1303's PR read the second and failed on the first).
	frame string
}

var unwakeableRuns = []unwakeableRun{
	{
		"unwakeable-wait",
		[]string{"run", "../../internal/component/testdata/unwakeable-wait.wasm"},
		"../../internal/component/testdata/unwakeable-wait.reading", "waitableSetWait",
	},
	{
		"unwakeable-wait-woken",
		[]string{"run", "../../internal/component/testdata/unwakeable-wait-woken.wasm"},
		"../../internal/component/testdata/unwakeable-wait.reading", "",
	},
	{
		"forever",
		[]string{"run", "../../internal/interp/testdata/wait-forever.wasm", "forever"},
		"../../internal/interp/testdata/wait-forever.reading", "interp.(*memory).wait",
	},
	{
		"forever64",
		[]string{"run", "../../internal/interp/testdata/wait-forever.wasm", "forever64"},
		"../../internal/interp/testdata/wait-forever.reading", "interp.(*memory).wait",
	},
	{
		"brief",
		[]string{"run", "../../internal/interp/testdata/wait-forever.wasm", "brief"},
		"../../internal/interp/testdata/wait-forever.reading", "",
	},
}

// TestAnUnwakeableWaitHangsThroughBurroughsRun is #1303's witness at the CLI (ADR 0128). A guest that waits on
// something nothing can wake leaves `burroughs run` blocked, as wasmtime 49.0.3 does, rather than ended by Go's
// runtime fatal.
//
//   - **unwakeable-wait**: a stackful `waitable-set.wait` on an empty set. Burroughs at d8076a1 died with "fatal
//     error: all goroutines are asleep - deadlock!", exit 2.
//   - **forever** and **forever64**: `memory.atomic.wait32` and `wait64` on a shared memory, with no timeout and
//     no other agent. The same fatal, at once.
//   - **The controls**, unwakeable-wait-woken and brief: the same waits, with a timer that ends them. Each ends by
//     itself with wasmtime's reading, and the process does not outlive it. So the fix changes nothing about a
//     wait that can be woken.
//
// Each arm runs `dispatch` in a child (testenv.RunHangChild), because the fatal would end this test binary. A hang
// is read from the child's goroutine dump, which must show the guest parked in its wait, and not from the child's
// being alive alone.
//
// NEUTER: holdHostAwake returning a stopped timer (internal/interp/hostawake.go): unwakeable-wait, forever and
// forever64 each fail with "the host ended 4ms after starting the guest, exit 2: fatal error: all goroutines are
// asleep - deadlock!" (4 to 5 ms, with and without -race). The controls pass.
// NEUTER: the hold dropped from CanonCaller.Blocking only: unwakeable-wait fails that way, and forever and
// forever64 pass.
// NEUTER: the hold dropped from memory.wait only: forever and forever64 fail that way, and unwakeable-wait passes.
func TestAnUnwakeableWaitHangsThroughBurroughsRun(t *testing.T) {
	t.Parallel()
	for _, r := range unwakeableRuns {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			want := testenv.ReadingRows(t, r.reading)[r.name]
			if want == "" {
				t.Fatalf("%s has no row for %s", r.reading, r.name)
			}
			got := testenv.RunHangChild(t, "^TestAnUnwakeableWaitHangsThroughBurroughsRunChild$", r.name,
				unwakeablePeriod)
			// The CLI prints a core result typed, where wasmtime prints the bare number.
			testenv.JudgeHang(t, r.name, want, r.frame, got, "i32:"+want)
		})
	}
}

// TestAnUnwakeableWaitHangsThroughBurroughsRunChild is the child half. It does nothing unless testenv.HangChildEnv
// is set, which only RunHangChild sets: it then runs that arm's `burroughs run` with this process's streams and
// exits with its code. Not a skip: outside a child there is no arm to run, and it passes having asserted nothing.
func TestAnUnwakeableWaitHangsThroughBurroughsRunChild(t *testing.T) {
	name := testenv.HangChildCase()
	if name == "" {
		return
	}
	for _, r := range unwakeableRuns {
		if r.name == name {
			fmt.Println(testenv.HangMark)
			os.Exit(dispatch(os.Stdout, os.Stderr, r.args))
		}
	}
	t.Fatalf("no arm named %q", name)
}
