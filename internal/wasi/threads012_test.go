// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// threads012Chain is the guest's `chain`, recomputed here with stock Go. The witness's expected digest must not
// come from the guest itself, or a wrong answer would agree with itself.
func threads012Chain(seed uint64) uint64 {
	x := seed*0x9E3779B97F4A7C15 + 1
	for i := range 200000 {
		x ^= x >> 33
		x *= 0xFF51AFD7ED558CCD
		x ^= x >> 29
		x += uint64(i)
	}
	return x
}

func threads012Expected() string {
	var digest uint64
	for w := range 4 {
		digest = digest*1099511628211 ^ threads012Chain(uint64(w)+1) ^ uint64(w)
	}
	return fmt.Sprintf("digest %016x\nworkers 4\ngc VERIFIED nodes 50 finalized 0 collections 8\n", digest)
}

// TestAForkBuiltGoProgramRunsWithRealThreadsThroughThePublicPath is v0.12.0's end-to-end witness (#1122; the
// chair's ruling of 2026-10-27, item 3). `testdata/threads012/threads012.wasm`, built by the Phase 4 fork (see its
// PROVENANCE), runs through `Run`, the public `WASIP1Config.Run`'s body, with nothing supplied beyond the module.
// It must:
//   - **compute the right answer:** its stdout equals the expected output, whose digest this test recomputes with
//     stock Go, and whose GC line is fixed by construction (VERIFIED, every node, no finalizer on a reachable
//     node);
//   - **run in parallel:** at least `threads012ExecutingFloor` agents are sampled executing guest code at once.
//     The peak live-agent count is logged beside it, as the weaker figure: it shows only that the agents existed.
//
// **Neuters, run:**
//   - **The door not served** (`runModule` given a nil door): link fails with *"unknown import: "burroughs"
//     "spawn""*.
//   - **GOMAXPROCS = 1** (the same source, rebuilt with `runtime.GOMAXPROCS(1)`): the digest still matches, and the
//     executing floor fails, *"at most 2 agents executed guest code at once, below 3"*. So the floor measures
//     parallelism, not the answer.
//   - **The GC half's own neuter** is clause 3's `c3_notstopped` build (stop-the-world neutered in the fork), which
//     `TestClause3GCAcrossTwoAgents` already runs. It is not repeated here.
func TestAForkBuiltGoProgramRunsWithRealThreadsThroughThePublicPath(t *testing.T) {
	wasm, err := os.ReadFile(filepath.Join("testdata", "threads012", "threads012.wasm"))
	if err != nil {
		t.Fatalf("the committed fork-built guest is missing, so this witness has no subject: %v", err)
	}
	want := threads012Expected()
	var peak, maxExec int
	stop := make(chan struct{})
	sampled := make(chan int, 1)
	beforeStartForTest = func(in *interp.Instance) {
		go func() {
			m := 0
			tick := time.NewTicker(200 * time.Microsecond)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					sampled <- m
					return
				case <-tick.C:
					m = max(m, in.ExecutingAgents())
				}
			}
		}()
	}
	afterStartForTest = func(in *interp.Instance) {
		peak = in.PeakLiveAgents()
		close(stop)
		maxExec = <-sampled
	}
	t.Cleanup(func() { beforeStartForTest, afterStartForTest = nil, nil })

	var out, errOut strings.Builder
	code, err := Run(Config{Wasm: wasm, Args: []string{"threads012"}, Stdout: &out, Stderr: &errOut})
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, errOut.String())
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stdout %q, stderr: %s)", code, out.String(), errOut.String())
	}
	if out.String() != want {
		t.Fatalf("stdout\n%s\nwant\n%s", out.String(), want)
	}
	t.Logf("peak live agents: %d; most executing guest code at once (sampled): %d", peak, maxExec)
	if maxExec < threads012ExecutingFloor {
		t.Errorf("at most %d agents executed guest code at once, below %d: the guest's work did not run in "+
			"parallel on several agents", maxExec, threads012ExecutingFloor)
	}
}

// threads012ExecutingFloor is the fewest agents the witness requires executing guest code AT ONCE, sampled every
// 200µs during the run.
//
// **Peak live agents could not discriminate, measured.** A Go runtime keeps Ms alive for its own work at any P
// count: the GOMAXPROCS = 1 neuter build reached a peak of 3 live agents, against 4–5 for the witness. So that
// count says agents existed, not that they ran at once, and a floor on it passed under the neuter.
//
// The executing count does discriminate, measured on darwin/arm64, 2026-10-27:
//   - the witness (GOMAXPROCS = 4): 4 in 4 of 4 runs;
//   - the neuter (GOMAXPROCS = 1): 2 in 4 of 4 runs.
//
// So the floor is 3: above the neuter's 2, and below the witness's 4.
const threads012ExecutingFloor = 3
