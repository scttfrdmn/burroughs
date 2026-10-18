// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestALateResultStoreThatFailsTrapsItsCaller is #994's witness. A cross-component result store that runs
// after the lower has returned, and fails, must reach the caller's task as a trap.
//
// The fixture (`realloc-wait-late.wasm`) has a child that YIELDs before `task.return`, so the parent's
// async lower returns STARTED and the store runs when the child resolves. The store lowers a string
// through the parent's realloc, which calls `waitable-set.wait`. A realloc may not leave, so the reference
// traps there. wasmtime 49.0.3 reads *"cannot leave component instance"*, 10 of 10
// (`realloc-wait-late.reading`).
//
// **The control** (`realloc-wait-late-ctl.wasm`) is the same late store through a realloc that does not
// wait. It returns the string's length, 3, on both engines, so the late store itself is shown to work.
//
// **The park's bound is shortened** so that the defect, a WAIT that never ends, fails in seconds rather than
// thirty. The assertion is on the reason, not the time: the defect's own error is the park expiring.
//
// **Neuter, run:** restoring `resolveErr = err` on the late-failure arm of `apply`. The subject then
// expires its park (*"produced nothing"*) and fails; the control passes.
func TestALateResultStoreThatFailsTrapsItsCaller(t *testing.T) {
	t.Setenv(asyncGateEnv, "") // the default, which is on
	restore := liftParkBound
	liftParkBound = 3 * time.Second
	defer func() { liftParkBound = restore }()

	call := func(t *testing.T, fx string) (uint32, error) {
		t.Helper()
		b, err := os.ReadFile("testdata/" + fx)
		if err != nil {
			t.Fatalf("the committed fixture is missing: %v", err)
		}
		in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
		if err != nil {
			t.Fatalf("instantiate %s: %v", fx, err)
		}
		defer in.Close()
		res, err := in.CallValues("res")
		if err != nil {
			return 0, err
		}
		n, _ := res[0].U32()
		return n, nil
	}

	t.Run("control_a_late_store_that_succeeds", func(t *testing.T) {
		n, err := call(t, "realloc-wait-late-ctl.wasm")
		if err != nil || n != 3 {
			t.Fatalf("res() = %d, %v; want 3, the length of \"abc\", as wasmtime reads", n, err)
		}
	})

	t.Run("a_late_store_that_fails", func(t *testing.T) {
		_, err := call(t, "realloc-wait-late.wasm")
		var trap *interp.Trap
		if !errors.As(err, &trap) || !strings.Contains(trap.Reason, "may not leave") {
			t.Fatalf("res() err = %v; want the may_leave trap from waitable-set.wait inside the store's "+
				"realloc (wasmtime: \"cannot leave component instance\"). An expired park here is #994: the "+
				"store's failure had no reader", err)
		}
	})
}
