package component

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// # ADR 0100's second build slice: `thread.new-indirect`, `thread.resume-later`, and the scheduling point
//
// Witnessed against `cmthreads/spawn.reading` and `cmthreads/spawn-parked.reading`, both taken with
// wasmtime 49.0.2 and committed BEFORE the engine change, so the engine is measured against readings it
// could not have influenced.

// readCmthreadsResult pulls the RESULT line out of a committed reading. A parser that found nothing
// fatals rather than returning zero, which would be an assertion against an empty oracle.
func readCmthreadsResult(t *testing.T, name string) uint32 {
	t.Helper()
	path := filepath.Join("testdata", "cmthreads", name)
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "RESULT" {
			v, perr := strconv.ParseUint(f[1], 10, 32)
			if perr != nil {
				t.Fatalf("%s: RESULT %q is not a u32: %v", path, f[1], perr)
			}
			return uint32(v)
		}
	}
	t.Fatalf("%s carries no RESULT line, so the oracle is empty", path)
	return 0
}

// callRunBounded calls `run` and fails rather than hanging. Bounded because the defect this slice
// repairs is a HANG: without the scheduling point at YIELD, `spawn.wasm`'s callback re-YIELDs forever.
func callRunBounded(t *testing.T, in *Instantiated) uint32 {
	t.Helper()
	type out struct {
		v   uint32
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := in.CallValues("run", canon.U32(0))
		if err != nil {
			ch <- out{err: err}
			return
		}
		v, _ := res[0].U32()
		ch <- out{v: v}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			t.Fatalf("run: %v", o.err)
		}
		return o.v
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s — the callback loop is re-YIELDing on a Thread that no " +
			"scheduling point lets run, which is the defect this slice repairs")
		return 0
	}
}

// TestASpawnedThreadRunsAtTheYieldAndHoldsItsOwnIndex is the differential against `spawn.reading`, and it
// discharges three of the slice's obligations in one value.
//
//   - **The two productions**: the body only runs if `thread.new-indirect` created it and
//     `thread.resume-later` readied it, so the marker bit is positive evidence for both.
//   - **Concurrent distinct indices**: the entry Thread YIELDS rather than returning, so it is still
//     registered while the body runs. The body reads 2 and the entry 1; a body reading 1 would be the
//     entry's slot freed at the yield — two Threads sharing one index.
//   - **`thread.index` under an async lift**: the entry's 1 is read from inside an async callback lift's
//     callee, which no earlier fixture could reach.
//
// **It asserts the value and no execution order.** The corrected fixture re-YIELDs until the body's
// marker is set, so either of the reference's YIELD branches reaches the same value.
//
// **Neuter, run:** removing `runReady` from the YIELD arm makes `run` never return — the callback
// re-YIELDs forever — and the deferred `Close` then reclaims it with `interp.ErrClosed` at the next
// re-entry, #963's mechanism, failing this on the bound.
func TestASpawnedThreadRunsAtTheYieldAndHoldsItsOwnIndex(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	want := readCmthreadsResult(t, "spawn.reading")

	wasm, err := os.ReadFile("testdata/cmthreads/spawn.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()

	got := callRunBounded(t, in)
	if got != want {
		t.Fatalf("run returned %d (0x%x): marker %d, entry %d, body %d; spawn.reading records %d (0x%x)",
			got, got, got&1, (got>>4)&0xf, (got>>8)&0xf, want, want)
	}
	// Decoded and asserted separately so a failure says WHICH property regressed.
	if got&1 != 1 {
		t.Error("the body's marker is not set, so new-indirect and resume-later did not run it")
	}
	if entry, body := (got>>4)&0xf, (got>>8)&0xf; entry == body {
		t.Errorf("entry and body both read %d: two live Threads share one index", entry)
	}
	// The body unregistered on return and the implicit Thread on the task's exit.
	if n := in.w.threads.live(); n != 0 {
		t.Errorf("%d Thread(s) still registered after the call; both should have unregistered", n)
	}
}

// TestCloseEndsAParkedThreadsGoroutine is the parked-Thread wake.
//
// `spawn-parked.wasm` creates a Thread and never readies it, then resolves and returns. The Thread is
// still registered — legal, since Threads outlive resolution — and in this engine it is a goroutine
// parked in a Go `select`. **Neither existing reclamation path reaches it**: `interp.ErrClosed` needs the
// goroutine to re-enter through `invokeIndex`, and `interp.ErrTerminated` needs it at a safepoint
// running guest code. So `Close` wakes it explicitly (`threadTable.killParked`), and this checks that its
// goroutine has exited within a bound and the table is empty.
//
// **Neuter, run:** deleting the `killParked` loop from `walker.close` leaves the goroutine parked, so
// `done` never closes and this fails on the bound.
func TestCloseEndsAParkedThreadsGoroutine(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	want := readCmthreadsResult(t, "spawn-parked.reading")

	wasm, err := os.ReadFile("testdata/cmthreads/spawn-parked.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	if got := callRunBounded(t, in); got != want {
		t.Fatalf("run returned %d; spawn-parked.reading records %d — the created Thread's index", got, want)
	}

	// The call has returned and the created Thread is still there, parked. Asserted, because a test of
	// the wake that never had anything to wake would pass for the wrong reason.
	if n := in.w.threads.live(); n != 1 {
		t.Fatalf("%d Thread(s) registered after the call; want exactly the 1 parked Thread", n)
	}
	parked := in.w.threads.get(want)
	if parked == nil || parked.state != threadSuspended {
		t.Fatalf("Thread %d is not the suspended Thread this witness is about: %+v", want, parked)
	}

	in.Close()

	select {
	case <-parked.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close returned and the parked Thread's goroutine had not exited within 5s. Neither " +
			"interp.ErrClosed (needs a re-entry) nor interp.ErrTerminated (needs a safepoint) can reach " +
			"a goroutine parked in a select, so Close must wake it explicitly")
	}
	if n := in.w.threads.live(); n != 0 {
		t.Errorf("%d Thread(s) still registered after Close", n)
	}
}
