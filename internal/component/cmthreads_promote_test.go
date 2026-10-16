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
// **Neuters, run:**
//   - **P1:** `takeReady` ignores a YIELDing implicit Thread (the pre-#1012 behaviour). The promote falls to a
//     plain yield and reads 17, 20 of 20.
//   - **P2:** `promoteYielding` does not set `promotedWait`. The promote succeeds, but a stale tick can win
//     the freed slot and resume the promoter before the promoted callback. That reads 17 in 42 of 100.
//     So this witness is also run 100 times by the PR that built it, and it passed 100 of 100.
//
// **Three further changes were tried and removed, because none moved the rate:** a requester-aware
// YIELD request, releasing `entrySem` before the run slot, and marking the YIELD at the entry's return.
// Each was disabled alone over 100 runs, with 0 failures. Only `takeReady`'s check and `promotedWait` are
// load-bearing.
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
