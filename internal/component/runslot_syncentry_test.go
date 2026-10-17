package component

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestASyncEntryWaitsForAnExecutingAsyncTask is the run slot's own discriminating witness (the chair's ruling
// of 2026-10-17, item 2).
//
// # Why it was needed
//
// On the callback shapes #1017 moved to, two exclusions guard a re-entry: the entry slot (`entrySem`, the
// reference's `exclusive_thread`) and the run slot. Either alone keeps the callers apart. So removing only the
// run slot (T1) passed `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce`, and nothing failed when it was gone.
//
// # The shape, where `entrySem` does not exclude
//
// `run-slot-sync-entry.wasm`:
//   - `spin` is an async callback lift whose entry calls the host's `note` and then runs a long loop, watching
//     word 4 for a change;
//   - `poke` is a SYNC export that writes word 4.
//
// A sync entry takes no `entrySem`, so while `spin` executes, only the run slot keeps `poke` out. The test
// starts `poke` when `note` fires. `spin` returns `changed << 16`, so no overlap reads 0. The bound is a true
// negative's: under the fix, `poke` cannot run until `spin`'s entry returns.
//
// **wasmtime 49.0.2 reads 2007**, 10 of 10 (`run-slot-sync-entry.reading`): `spin` RETURNED (2000), saw no
// change (0), and `poke` returned 7.
//
// **Neuters, run.** Each fails 20 of 20 on `0x10000`, `spin` seeing `poke`'s write:
//   - **T1:** the run slot's acquisition removed from the sync entry (`invokeWith`);
//   - **T1′:** the run slot removed from the async entries instead (`enterInvokeLowering`, `enterAndInvoke`).
//
// Under T1, `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce` still passes, which is why this exists.
//
// # The race detector cannot see this overlap, so the guest's value is the oracle
//
// Run 50 of 50 under `-race`, heap-backed, and clean. **That clean run proves less than it looks**: under T1
// the detector reports nothing either, while the value assertion fails. ADR 0054 makes every aligned guest
// word access a sequentially consistent atomic (`atomicLoadWord` and `atomicStoreWord`, `interp/memop.go`), so a
// guest store racing a guest load is not a data race in Go's memory model. The detector sees host code
// against guest memory, which is how `TestAResultStoreWaitsForTheExecutingTask`'s neuter is reported, and it
// cannot see guest against guest.
func TestASyncEntryWaitsForAnExecutingAsyncTask(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("gate="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			t.Setenv(cmThreadsGateEnv, gate)
			b, err := os.ReadFile("testdata/run-slot-sync-entry.wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			started := make(chan struct{}, 1)
			h := NewHost(io.Discard, io.Discard, nil)
			h.syncImpls = map[string]interp.CanonFunc{
				"note": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
					select {
					case started <- struct{}{}:
					default:
					}
					return nil, nil
				},
			}
			in, err := InstantiateWithHost(b, h)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			t.Cleanup(in.Close)

			type out struct {
				res []canon.Value
				err error
			}
			spin, poke := make(chan out, 1), make(chan out, 1)
			go func() { r, e := in.CallValues("spin"); spin <- out{r, e} }()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("spin never reached its loop")
			}
			go func() { r, e := in.CallValues("poke"); poke <- out{r, e} }()

			var s, p out
			for _, w := range []struct {
				ch   chan out
				into *out
				name string
			}{{spin, &s, "spin"}, {poke, &p, "poke"}} {
				select {
				case *w.into = <-w.ch:
				case <-time.After(30 * time.Second):
					t.Fatalf("%s did not return", w.name)
				}
				if w.into.err != nil {
					t.Fatalf("%s: %v", w.name, w.into.err)
				}
			}
			if v, ok := s.res[0].U32(); !ok || v != 0 {
				t.Fatalf("spin() = %v, want 0: the sync entry `poke` wrote guest memory while the async task "+
					"`spin` was executing. A sync entry takes no entrySem, so the run slot is the only "+
					"exclusion here, and wasmtime 49.0.2 reads no overlap (2007)", s.res)
			}
			if v, ok := p.res[0].U32(); !ok || v != 7 {
				t.Fatalf("poke() = %v, want 7", p.res)
			}
		})
	}
}
