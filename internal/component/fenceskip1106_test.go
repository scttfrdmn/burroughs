// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"testing"
	"time"
)

// TestAFenceSkippedThreadIsRunWhenTheWriterLeavesBeforeTheRequeue is #1106's witness: it forces the interleaving
// the stress found once in 10,000 iterations.
//
// # The lost wakeup
//
// `drive` pops a fenced task's Thread, and `fenceAdmit` finds its caller instance executing. `fenceReaderTry`
// then registers this instance for a kick and refuses. If the caller's last writer leaves before `drive` requeues
// the Thread, `fenceWriterLeave` kicks this instance while the Thread is in nobody's queue. `kick` reads an empty
// ready queue and sends nothing. `drive` then requeues the Thread and returns idle, `serve` frees the slot, and
// `free` does not kick. The Thread waits in the ready queue for a tick that never comes.
//
// The dump from the stress shows exactly that four-way wait:
//   - the parent's lift in `awaitEvent`;
//   - `work`'s implicit Thread parked on its `resume`;
//   - the scheduler idle at `loop`'s top;
//   - T, the Thread `work` created and marked ready, never started.
//
// # Forcing it
//
// `fenceSkipWindow` holds that window open for 50ms. The parent `run` is in `subtask.cancel` (a writer on the
// fence) and finishes well inside 50ms, so whenever the path is reached, its writer leaves inside the window.
//
// NEUTER, run: the re-try after the requeue (`drive`'s second `fenceAdmit`) removed. Then every run hangs to the
// bound, with the same four-way dump.
func TestAFenceSkippedThreadIsRunWhenTheWriterLeavesBeforeTheRequeue(t *testing.T) {
	// Reaching the fence-skip path is itself a race: T must be popped while the parent still holds its fence.
	// Measured at 15 of 400 iterations. So the witness iterates until the window has been held open `wantWindows`
	// times, and treats too few entries as a failure, not a pass.
	const wantWindows, maxIters = 5, 1000
	widened := 0
	fenceSkipWindow = func() { widened++; time.Sleep(50 * time.Millisecond) }
	defer func() { fenceSkipWindow = nil }()

	iters := 0
	for ; iters < maxIters && widened < wantWindows; iters++ {
		_, res, err := callBounded(t, "cancel-suspended", "run")
		wantU32(t, "cancel-suspended", res, err, 4)
	}
	if widened < wantWindows {
		t.Fatalf("the fence-skip window was entered %d times in %d iterations, want %d. Too few runs reached "+
			"#1106's interleaving for this to witness it; check that the fixture still fences T behind its caller",
			widened, iters, wantWindows)
	}
	t.Logf("the fence-skip window was held open %d times over %d iterations, and every run returned 4", widened, iters)
}
