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
	// Reaching the fence-skip path is itself a race: T must be popped while the parent still holds its fence. Its rate
	// is platform-dependent: 15 in 400 iterations on the darwin dev host, but 2 in 1000 on CI's macos-15 runner, where
	// this witness's first version failed its own vacuity floor. So the witness iterates until the window has been
	// held open `wantWindows` times, bounded by a time budget rather than a fixed count, and too few entries still
	// fail it rather than pass. Each iteration is a subtest, so its instance closes when it ends rather than at the
	// end of the whole test.
	const wantWindows = 3
	budget := time.Now().Add(60 * time.Second)
	widened := 0
	fenceSkipWindow = func() { widened++; time.Sleep(50 * time.Millisecond) }
	defer func() { fenceSkipWindow = nil }()

	iters := 0
	for ; time.Now().Before(budget) && widened < wantWindows; iters++ {
		t.Run("iteration", func(t *testing.T) {
			_, res, err := callBounded(t, "cancel-suspended", "run")
			wantU32(t, "cancel-suspended", res, err, 4)
		})
		if t.Failed() {
			return
		}
	}
	if widened < wantWindows {
		t.Fatalf("the fence-skip window was entered %d times in %d iterations within the budget, want %d. Too few "+
			"runs reached #1106's interleaving for this to witness it; check that the fixture still fences T behind "+
			"its caller", widened, iters, wantWindows)
	}
	t.Logf("the fence-skip window was held open %d times over %d iterations, and every run returned 4", widened, iters)
}
