package component

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// raceWitnessFixtures are the fixtures whose witnesses need the race detector to SEE guest memory: every
// memory in them must be heap-backed (#979). Registered by hand, by the slice that makes a fixture one.
//
// # Why this exists
//
// On unix a linear memory is an mmap reservation, outside the race detector's shadow, unless its declared
// max equals its initial size (`interp.reservationFor` returns 0, so `interp.allocate` takes `make`). A
// race witness whose fixture lets memory grow therefore goes blind on darwin and Linux **without
// failing**, because "no race reported" is exactly what a blind detector says. #979 converted these
// fixtures; this control keeps them converted.
//
// # What it cannot see, stated rather than inferred
//
//   - **A witness added without registering its fixture slips through.** The control does not try to infer
//     which tests are race witnesses: a test's dependence on the detector is a property of its argument, not
//     of its code, and a heuristic would have to guess at it.
//   - **A fixture's memories are checked; its witness's reasoning is not.** A fixture can be heap-backed and
//     its witness still look at the wrong word.
//
// The §4 battery's sources are registered beside it, in `internal/interp`'s `TestRaceWitnessSourcesAreHeapBacked`.
var raceWitnessFixtures = []string{
	// #979's conversions
	"run-slot.wasm",
	"async-lift-yield-synth.wasm",
	"async-waitset-synth.wasm",
	"async-wake-bmm1-synth.wasm",
	// authored heap-backed for ADR 0101's witnesses
	"result-store-heap.wasm",
	"xc.wasm",
	"xc-gated.wasm",
	// authored heap-backed for ADR 0101's Option A witnesses (cmthreads_optiona_test.go), and #977's
	"cmthreads/thread-wait-callback.wasm",
	"cmthreads/sync-enters-past-suspended.wasm",
	"cmthreads/wait-runs-ready.wasm",
	"cmthreads/trap-while-waiting.wasm",
	"cmthreads/trap-while-waiting-control.wasm",
	"cmthreads/nested-sync-unwind.wasm",
	"cmthreads/nested-sync-unwind-control.wasm",
	"cmthreads/residual.wasm",
	"cmthreads/yield-admits-entry.wasm",
	"cmthreads/orphan-trap.wasm",
	"cmthreads/sync-wait-own-thread.wasm",
	"cmthreads/sync-wait-own-thread-control.wasm",
	"cmthreads/cancel-suspended.wasm",
	"cmthreads/cancel-suspended-control.wasm",
	"ignore-cancel.wasm",
	"cmthreads/promote-implicit.wasm",
	"cmthreads/promote-implicit-control.wasm",
	"context-clobber.wasm",
	"cmthreads/thread-context.wasm",
	// the callback-lift twins the run-slot and result-store witnesses moved to (ADR 0102's sync-wait row)
	"run-slot-cb.wasm",
	"result-store-heap-cb.wasm",
	// the run slot's own witness (TestASyncEntryWaitsForAnExecutingAsyncTask)
	"run-slot-sync-entry.wasm",
}

// TestRaceWitnessFixturesAreHeapBacked decodes every registered fixture, its nested components included,
// and fails on any defined memory whose max is absent or differs from its initial size.
//
// **Neuter, run:** reverting `run-slot.wat`'s memory to `(memory 1)` and re-parsing makes this fail, and the
// failure names `run-slot.wasm`.
func TestRaceWitnessFixturesAreHeapBacked(t *testing.T) {
	if len(raceWitnessFixtures) == 0 {
		t.Fatal("the registry is empty, so this control checks nothing")
	}
	total := 0
	for _, name := range raceWitnessFixtures {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Errorf("%s: registered but missing: %v", name, err)
			continue
		}
		n, bad, err := heapBackedMemories(b, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, b := range bad {
			t.Errorf("%s: %s is not heap-backed, so on unix it is an mmap reservation the race detector "+
				"cannot see, and this fixture's race witness is blind there without failing. Declare max "+
				"equal to initial, e.g. (memory 1 1) (#979)", name, b)
		}
		if n == 0 {
			t.Errorf("%s: no defined memory found, so the registration asserts nothing about it", name)
		}
		total += n
	}
	t.Logf("%d fixture(s), %d defined memories, all heap-backed unless reported above", len(raceWitnessFixtures), total)
}

// heapBackedMemories counts a component's defined memories, its nested components' included, and describes
// each one whose max is absent or differs from its initial size.
func heapBackedMemories(b []byte, where string) (n int, bad []string, err error) {
	c, err := LoadWithFeatures(b, bin.Features{Threads: true})
	if err != nil {
		return 0, nil, fmt.Errorf("load: %w", err)
	}
	for mi, m := range c.CoreModules {
		for i, mem := range m.Memories {
			n++
			if !mem.Limits.HasMax || mem.Limits.Max != mem.Limits.Min {
				maxDesc := "none"
				if mem.Limits.HasMax {
					maxDesc = strconv.FormatUint(mem.Limits.Max, 10)
				}
				bad = append(bad, fmt.Sprintf("%s core module %d memory %d (min %d, max %s)", where, mi, i,
					mem.Limits.Min, maxDesc))
			}
		}
	}
	for ni, nb := range c.NestedComponents {
		nn, nbad, nerr := heapBackedMemories(nb, fmt.Sprintf("%s nested component %d", where, ni))
		if nerr != nil {
			return n, bad, nerr
		}
		n += nn
		bad = append(bad, nbad...)
	}
	return n, bad, nil
}
