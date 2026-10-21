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
// **`res()` is asserted now, and the note that kept it out was falsified twice.** Its realloc is the lower's,
// run by `interp.CanonCaller.Realloc` (`interp.TestMayLeaveIsFalseExactlyInsideARealloc` witnesses that site).
// The note said its store runs after the lower returned, so the store's error had no reader and Burroughs
// returned 0 (#994).
//   - **#1034's F0** made this child's store run inside the lower, because the child resolves in its first
//     segment. So the store's trap became the lower's, and `res()` trapped from then on, measured on the #994
//     work. The note went on saying it returned 0.
//   - **#994's fix** gave a store that does run late a channel. That case is `realloc-wait-late.wasm`, in
//     `TestALateResultStoreThatFailsTrapsItsCaller`.
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
	// **One instance per arm, since #1040.** A trap locks the instance and the next call is refused with
	// ErrInstanceLocked wrapping that trap. On one shared instance, `res`'s refusal wrapped `arg`'s trap, whose
	// reason also says "may not leave", so `res`'s assertion passed on `arg`'s trap and asked nothing of its
	// own; `ctl` was refused outright.
	fresh := func() *Instantiated {
		in, ierr := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
		if ierr != nil {
			t.Fatalf("instantiate: %v", ierr)
		}
		t.Cleanup(in.Close)
		return in
	}

	_, err = fresh().CallValues("arg", canon.Str("hi")) // the lift's own realloc, lowering the argument
	var trap *interp.Trap
	if !errors.As(err, &trap) || !strings.Contains(trap.Reason, "may not leave") {
		t.Errorf("arg: got %v; want the may_leave trap from waitable-set.wait inside cabi_realloc "+
			"(wasmtime: \"cannot leave component instance\")", err)
	}

	_, err = fresh().CallValues("res") // the lower's realloc, lowering the child's string result
	if !errors.As(err, &trap) || !strings.Contains(trap.Reason, "may not leave") || errors.Is(err, ErrInstanceLocked) {
		t.Errorf("res: got %v; want the may_leave trap from waitable-set.wait inside the lower's realloc "+
			"(wasmtime: \"cannot leave component instance\")", err)
	}

	res, err := fresh().CallValues("ctl", canon.Str("hi"))
	if err != nil {
		t.Fatalf("ctl: %v; context.set is unchecked in the reference, so a realloc may call it", err)
	}
	if n, _ := res[0].U32(); n != 2 {
		t.Fatalf("ctl returned %d; want 2, the length of \"hi\"", n)
	}
}
