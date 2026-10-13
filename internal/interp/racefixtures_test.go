package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// raceWitnessSources are the §4 battery's named module sources, whose rows need the race detector to SEE
// guest memory: every memory they define must be heap-backed (#979). The component fixtures have their own
// registry, `internal/component`'s `TestRaceWitnessFixturesAreHeapBacked`, and the same reasoning.
//
// # Why this exists
//
// On unix a linear memory is an mmap reservation, outside the race detector's shadow, unless its declared
// max equals its initial size (`reservationFor` returns 0, and `allocate` takes `make`). The battery has
// always declared `(memory 1 1 shared)`, which is why its detector reported `execMemoryFill`'s pair on
// darwin. Until #979 that was coincidence, not a rule: a source edited to `(memory 1 2 shared)` would have
// blinded its row on darwin and Linux without failing. This control makes it a rule.
//
// # What it cannot see, stated rather than inferred
//
//   - **Inline sources are not covered.** Several battery rows pass a WAT literal straight to a helper (the
//     SP-2, SP-4, SP-6, H-1 and T-6 rows among them) rather than naming a constant, and a registry can only
//     hold what has a name. They declare `max == min` today, recorded in the Makefile's `race` target, and
//     nothing here holds them to it.
//   - **A source added without registering it slips through.** A test's dependence on the detector is a
//     property of its argument, not of its code, and this control does not try to infer it.
var raceWitnessSources = []struct {
	name, src string
}{
	{"litmusWakerSrc", litmusWakerSrc},
	{"litmusWaiterSrc", litmusWaiterSrc},
	{"litmusSiblingWakerSrc", litmusSiblingWakerSrc},
	{"litmusSiblingWaiterSrc", litmusSiblingWaiterSrc},
	{"litmusTwoMemWakerSrc", litmusTwoMemWakerSrc},
	{"litmusTwoMemWaiterSrc", litmusTwoMemWaiterSrc},
	{"litmusParkSrc", litmusParkSrc},
	{"litmusFirstWaiterSrc", litmusFirstWaiterSrc},
	{"t6Module", t6Module},
}

// TestRaceWitnessSourcesAreHeapBacked encodes and decodes every registered source, and fails on any defined
// memory whose max is absent or differs from its initial size. A source that defines no memory (a waiter
// that imports one) is still checked, and contributes nothing. The vacuity guard is over the registry as a
// whole: at least one defined memory must have been checked.
//
// **Neuter, run:** changing `litmusWakerSrc`'s `(memory (export "mem") 1 1 shared)` to `1 2` makes this
// fail, naming `litmusWakerSrc`.
func TestRaceWitnessSourcesAreHeapBacked(t *testing.T) {
	if len(raceWitnessSources) == 0 {
		t.Fatal("the registry is empty, so this control checks nothing")
	}
	total := 0
	for _, s := range raceWitnessSources {
		img, err := text.EncodeModule([]byte(s.src))
		if err != nil {
			t.Errorf("%s: encode: %v", s.name, err)
			continue
		}
		// The battery's own widest set (the two-memory rows decode under MultiMemory); a control that
		// cannot decode a source would report nothing about its memories.
		m, err := (&binary.Decoder{Features: binary.Features{Threads: true, MultiMemory: true}}).DecodeModule(img)
		if err != nil {
			t.Errorf("%s: decode: %v", s.name, err)
			continue
		}
		for i, mem := range m.Memories {
			total++
			if !mem.Limits.HasMax || mem.Limits.Max != mem.Limits.Min {
				t.Errorf("%s: defined memory %d (min %d, max %d, has max %v) is not heap-backed, so on unix it "+
					"is an mmap reservation the race detector cannot see, and this source's battery row is "+
					"blind there without failing. Declare max equal to initial (#979)",
					s.name, i, mem.Limits.Min, mem.Limits.Max, mem.Limits.HasMax)
			}
		}
	}
	if total == 0 {
		t.Fatal("no registered source defines a memory, so this control checked nothing")
	}
	t.Logf("%d source(s), %d defined memories, all heap-backed unless reported above", len(raceWitnessSources), total)
}
