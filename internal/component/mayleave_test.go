package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAReallocThatCallsABuiltInTraps is #990's witness: a `cabi_realloc` runs with the reference's
// `may_leave = False`, so a canon built-in it calls traps. Here through the lift's realloc, with a control
// showing the guard does not reach an unchecked built-in.
//
// The readings are wasmtime 49.0.2's (`testdata/realloc-wait.reading`): `arg("hi")` and `res()` trap with
// *"cannot leave component instance"*, and `ctl("hi")` returns 2.
//
// **`res()` is not asserted, and the reason is #994, not may_leave.** Its realloc is the lower's, run by
// `interp.CanonCaller.Realloc`, and the guard fires there (`interp.TestMayLeaveIsFalseExactlyInsideARealloc`
// witnesses that site). But the cross-component result store runs after the lower has returned, and a
// late store's error has no reader, so Burroughs returns 0 where wasmtime traps. The row joins this test
// when #994 gives the trap a channel.
//
// **The trap is asserted by reason, not by the fact of a trap**, because the realloc's wait names set 0,
// which is no set: without the check the call still fails, on the bad handle, and a test that asserted only
// "an error" would pass on the defect.
//
// **Neuter, run:** making `interp.CanonCaller.MayLeave` return true unconditionally fails `arg` here on
// the reason, because the wait reaches its handle check instead.
func TestAReallocThatCallsABuiltInTraps(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/realloc-wait.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	_, err = in.CallValues("arg", canon.Str("hi")) // the lift's own realloc, lowering the argument
	var trap *interp.Trap
	if !errors.As(err, &trap) || !strings.Contains(trap.Reason, "may not leave") {
		t.Errorf("arg: got %v; want the may_leave trap from waitable-set.wait inside cabi_realloc "+
			"(wasmtime: \"cannot leave component instance\")", err)
	}

	res, err := in.CallValues("ctl", canon.Str("hi"))
	if err != nil {
		t.Fatalf("ctl: %v; context.set is unchecked in the reference, so a realloc may call it", err)
	}
	if n, _ := res[0].U32(); n != 2 {
		t.Fatalf("ctl returned %d; want 2, the length of \"hi\"", n)
	}
}
