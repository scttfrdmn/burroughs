package component

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestACancelRequestedWhileSuspendedIsDeliveredAtTheLoop is the chair's step 3. Cancellation is requested
// while a task's implicit Thread is suspended mid-callee, and delivered once the Thread resumes and its callee
// returns to the callback loop.
//
// The pin delivers a pending cancel only at the loop's delivery points: `canon_lift`'s loop top and YIELD
// arm, and `wait_from_callback`. `Task.request_cancellation` on a STARTED task only sets `PENDING_CANCEL`.
//
// `cmthreads/cancel-suspended.wasm`:
//   - **wasmtime 49.0.2:** 4 (`CANCELLED_BEFORE_RETURNED`), 10 of 10.
//   - **Control** (`cancel-suspended-control.wasm`, T never resumes `work`): no return on wasmtime. So the
//     cancel is never delivered to the suspended Thread itself.
//   - **Burroughs** matches both, which ADR 0101's Option A did not need to change.
//
// The control is asserted as **no return within two seconds**. That is a bounded negative: the oracle's own
// reading is a hang, and a return would be a cancel delivered to a suspended Thread, which no oracle does.
//
// **Neuter, run:** removing the callback loop's top-of-loop delivery (`deliverPendingCancelLocked`). `work`'s
// callback then sees NONE and returns 9, so the parent reads RETURNED (2).
func TestACancelRequestedWhileSuspendedIsDeliveredAtTheLoop(t *testing.T) {
	// Two subtests, so the arm that hung once (#1106) can be stressed on its own with
	// `-run …/delivered_at_the_loop -count=N`, without paying the control's two-second bounded negative on
	// every iteration.
	t.Run("delivered_at_the_loop", func(t *testing.T) {
		_, res, err := callBounded(t, "cancel-suspended", "run")
		wantU32(t, "cancel-suspended", res, err, 4)
	})
	t.Run("the_control_never_returns", testCancelSuspendedControl)
}

// testCancelSuspendedControl is the control half of TestACancelRequestedWhileSuspendedIsDeliveredAtTheLoop.
func testCancelSuspendedControl(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	b, rerr := os.ReadFile("testdata/cmthreads/cancel-suspended-control.wasm")
	if rerr != nil {
		t.Fatalf("the committed fixture is missing: %v", rerr)
	}
	in, ierr := Instantiate(b)
	if ierr != nil {
		t.Fatalf("Instantiate: %v", ierr)
	}
	done := make(chan struct{})
	go func() {
		_, _ = in.CallValues("run") // only whether it returns matters here
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the control returned: a cancel was delivered to a Thread that stayed suspended, which neither " +
			"oracle does")
	case <-time.After(2 * time.Second):
	}
	in.Close()
	select {
	case <-done:
	case <-time.After(optionABound):
		t.Fatalf("Close did not end the control's parked call within %s", optionABound)
	}
}

// TestTheCancellableSuspendIsRefusedByName holds the `0x01` refusal. The pin defines no cancellable form,
// and wasmtime 49.0.2, which accepts it, does not wake the suspended Thread on cancellation in
// `cancel-suspended-cancellable.wasm` either. So no oracle shows a behaviour to model.
//
// **Neuter, run:** decoding `0x01` as `0x00`, which makes it instantiate.
func TestTheCancellableSuspendIsRefusedByName(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/cmthreads/cancel-suspended-cancellable.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err == nil {
		in.Close()
		t.Fatal("a cancellable thread.suspend instantiated; it must be refused by name")
	}
	var tbe *threadBuiltinError
	if !errors.As(err, &tbe) || !strings.Contains(err.Error(), "cancellable") {
		t.Fatalf("refused, but not by name: %v", err)
	}
}
