// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package interp

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// osThreads counts this process's OS threads, from outside the Go runtime: `/proc/self/status`'s `Threads:` on
// linux, and `ps -M`'s per-thread lines on darwin, which has no /proc. **Windows is not covered**, and the build
// tag rather than a skip is what says so: the clause holds there by the same `LockOSThread` exit, but no counter
// here reads it.
func osThreads(t *testing.T) int {
	t.Helper()

	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/self/status")
		if err != nil {
			t.Fatalf("reading /proc/self/status: %v", err)
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "Threads:"); ok {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					t.Fatalf("/proc/self/status Threads %q: %v", v, err)
				}
				return n
			}
		}
		t.Fatal("/proc/self/status has no Threads line")
	case "darwin":
		out, err := exec.Command("ps", "-M", "-p", strconv.Itoa(os.Getpid())).Output()
		if err != nil {
			t.Fatalf("ps -M: %v", err)
		}
		// A header line, then one line per thread.
		lines := strings.Count(strings.TrimRight(string(out), "\n"), "\n")
		if lines < 1 {
			t.Fatalf("ps -M listed no threads:\n%s", out)
		}
		return lines
	}
	t.Fatalf("no OS-thread counter for %s", runtime.GOOS)
	return 0
}

// TestAnUnobservedExitLeaksNoOSThread is contract §2 T-5.1's OS-thread half, measured (#1132): *"an unobserved
// exit MUST leak neither a wasm thread nor an OS thread."* TestMembershipAndStatusRecordsAreBothBounded measures
// the wasm-thread half. This half rested on Go terminating a goroutine's thread when it exits locked, which
// `Spawn` relies on and nothing counted.
//
// **Rounds, compared after the first, because Go never reaps an idle thread.** A burst of locked spawns makes the
// scheduler start threads to keep its Ps running, and those stay in the runtime's idle pool. So the first round's
// growth is the pool settling, not a leak. A leak is growth that repeats: every unobserved exit that kept its
// thread adds one per spawn, every round. So the count after the last round is compared with the count after the
// first, with slack for the runtime's own threads.
//
// Every spawn exits unobserved: nobody joins it, and its record waits in `world.exited`, which T-5.2 bounds at
// `Close`.
//
// NEUTER, run (2026-10-29, darwin/arm64): with the spawn goroutine blocking forever after `close(t.done)`, which
// holds its locked thread, the count grows by the spawns of every round after the first, and the test fails.
func TestAnUnobservedExitLeaksNoOSThread(t *testing.T) {
	const (
		rounds   = 4
		perRound = 50
		// The runtime's own thread starts between readings (a GC worker's wakeup, the pool) are noise. The largest
		// measured was +10 (janus, linux/amd64, beside a stress job; darwin/arm64 stayed within +2). A leak is
		// perRound*(rounds-1) = 150. So 32 is twice the noise and a fifth of a leak.
		slack = 32
	)

	in := t5OpenGateModule(t)
	entry := exportedFuncIndex(t, in, "entry")

	round := func(r int) {
		tids := make([]ThreadID, 0, perRound)
		for i := range perRound {
			tid, err := in.Spawn(entry, spawnBase, 0)
			if err != nil {
				t.Fatalf("round %d, Spawn %d: %v", r, i, err)
			}
			tids = append(tids, tid)
		}
		awaitGuestWord(t, in, spawnArrived, int32((r+1)*perRound))
		awaitRetired(t, in, tids)
	}

	// A thread ends after its goroutine does, and `retire` runs before the goroutine returns, so a reading straight
	// after `awaitRetired` can still count threads that are on their way out. Each reading waits for the count to
	// reach the bound or a deadline, and keeps the lowest seen.
	settle := func(limit int) int {
		low := osThreads(t)
		for deadline := time.Now().Add(10 * time.Second); low > limit && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
			low = min(low, osThreads(t))
		}
		return low
	}

	start := osThreads(t)
	round(0)
	base := settle(start + slack)
	for r := 1; r < rounds; r++ {
		round(r)
	}
	final := settle(base + slack)
	t.Logf("OS threads (%s): %d at start, %d after round 1, %d after round %d (%d spawns, every exit unobserved)",
		runtime.GOOS, start, base, final, rounds, rounds*perRound)

	if final > base+slack {
		t.Errorf("OS threads grew from %d after the first round to %d after round %d: %d more, against a slack of "+
			"%d, while the %d spawns after the first round exited unobserved. A leak of one thread per exit would "+
			"be exactly that many. Contract §2 T-5.1 forbids an unobserved exit leaking an OS thread, and `Spawn`'s "+
			"goroutine relies on exiting locked to end its thread (#1132)",
			base, final, rounds, final-base, slack, perRound*(rounds-1))
	}
}
