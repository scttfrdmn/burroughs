package component

import "testing"

// TestAPromoteSwitchesToAnImplicitThreadWaitingAtItsYield is #1012's witness, the chair's step 4.
// `yield-then-promote` naming a task's implicit Thread that waits at its YIELD arm switches to it, so the
// task's callback runs before the promoter resumes.
//
// **The reference decides it, not wasmtime.** On `promote-implicit`, the pin reads {16} only (32 seeds × both
// profiles). On the control, which promotes a never-readied Thread and so falls to a plain yield, the pin
// reads {16, 17}, because `random.choice` permits either order. wasmtime 49.0.2 reads 16 on both, so it
// cannot tell the promote from its own scheduling choice.
//
// So the promote arm must read 16, and the control must read inside {16, 17}. Burroughs read 17 on the
// promote before #1012, outside the pin's set.
//
// **The arm's windows, and the neuter that measured each.** In the pin, the callback's return, the clearing
// of `exclusive_thread` and the YIELD arm's wait happen with no other Thread running, so a promote always
// finds the task waiting. Burroughs runs the promoter on another goroutine, so each place the task can look
// "not yet waiting" is a window:
//
//   - **P1, `takeReady` ignoring a YIELDing implicit Thread** (the pre-#1012 behaviour): 17, 20 of 20.
//   - **P2, `promoteYielding` not setting `promotedWait`:** a stale tick wins the freed slot and resumes
//     the promoter first. 17 in 42 of 100.
//   - **M1, no `markYield`** (marking the arm only in `yieldPoint`): with the gap before the arm widened by a
//     2 ms sleep, 17 in 30 of 30. With it, 0 of 30.
//   - **O1, the old release order** (the run slot before `entrySem`): with the gap between the two widened,
//     17 in 30 of 30.
//   - **E1, `takeReady` not counting `entrySem` held by the task's own pending re-entry:** 17 in 4 of 600
//     under `-race`. With it, 0 of 600, and 0 of 1,200 in an earlier pass.
//
// **M1 and O1 were first measured as not load-bearing, and that was wrong.** Disabled alone over 100
// macOS runs they showed 0 failures, so they were removed. Windows CI then read 17. Zero failures in N runs
// on one host's scheduling bounds the rate on that host; it does not show the window is absent. Each is now
// shown by widening its window until its absence fails deterministically.
func TestAPromoteSwitchesToAnImplicitThreadWaitingAtItsYield(t *testing.T) {
	_, res, err := callBounded(t, "promote-implicit", "run")
	wantU32(t, "promote-implicit", res, err, 16)

	_, cres, cerr := callBounded(t, "promote-implicit-control", "run")
	if cerr != nil {
		t.Fatalf("promote-implicit-control: run: %v", cerr)
	}
	if v, _ := cres[0].U32(); v != 16 && v != 17 {
		t.Fatalf("promote-implicit-control returned %d; the pin's set is {16, 17}", v)
	}
}
