package spec

import (
	"path/filepath"
	"sort"
	"testing"
)

// TestDeclineSideEffectsAreRegistered is the half of decision 0038 that keeps `sideEffectOfDecline`
// from being scoped to today's corpus.
//
// # The two halves, and why the derivation is here rather than in the fix
//
// The registry itself is a hand-written table: `load1.wast:25-29`, the declined writer at `:10`, the
// write channel, and the third instance whose value it determined. `TestGatedVectors`' fourth
// allowance already checks that the lines it claims are exactly the lines the run gates — agreement
// between the table and the mechanism, in both directions, at slack 0.
//
// What that cannot see is a file the table *does not mention*. A new corpus vector with this shape —
// a gate-declined module whose instantiation-time write was some healthy instance's only writer —
// scores as a fail, is charged to the interpreter, and nothing says so. That is the blind spot
// #414's option 1 was charged with, and it is closed here rather than in the fix, because the
// derivation available is one the fix must not use:
//
// **A default-lane fail that is not a fail with every gate on** is the signature of this defect. It
// is also the signature of the **decoder over-gating** — refusing a construct it should accept — and
// those have opposite work plans. A mechanism that gated on this signature would absorb the second
// into the third verdict silently, which is exactly what `gatedAssertInvalid`'s "if not, the decoder
// is over-gating and hiding a failure" exists to prevent. So the machine derives *membership* and a
// human writes down *causation*: a row this test names must be explained in the registry or repaired,
// and it cannot be waved through by the code that scores it.
//
// # The vacuity this control was born with, and how it is watched dying
//
// Once the fix is in, the population this derivation would find over the ordinary board is **0** —
// the rows are gated, not failed — and a comparison against an empty set agrees with any table at
// all. So the derivation runs with decision 0038's consult **neutered** (`runWith(e, true)`), which
// scores the board as it was before the fix and keeps the population at its real size. The floor
// below asserts it is non-empty for the same reason: the two sets agreeing at zero is the shape of
// this repo's most-repeated instrument failure, and a control that cannot tell agreement from absence
// is not one.
//
// Watched die, four mutations:
//
//   - deleting `load1.wast`'s entry from `sideEffectOfDecline`: `load1.wast:25 is a fail in the
//     default lane and a pass with every gate on, and no sideEffectOfDecline entry claims it` ×5,
//     which is the arm that fires for a new corpus file.
//   - adding a line to the entry that the derivation does not find (`30`): `load1.wast:30 is
//     registered … but is not a fail in the neutered default lane`, the stale-entry direction.
//   - passing `false` instead of `true` to `runWith`, i.e. forgetting to neuter: the population
//     drops to 0 and the floor fires with `derived 0 rows … the derivation is measuring a board
//     that already has the fix in it`. This is the mutation that matters, because it is the one a
//     future edit makes by accident.
//   - pointing the registry's `Module` at a line that is not declined (`11`): the rows stop being
//     gated, `TestGatedVectors`' fourth allowance fires, and *this* test goes quiet — which is
//     correct and is why both halves exist. The registry-versus-run agreement is that test's job.
//
// The third one is the reason the floor is written as a floor and not as a comment.
func TestDeclineSideEffectsAreRegistered(t *testing.T) {
	requireSuite(t)

	_, _, allOnEngine := allOnLane(t)

	// derived[file] is the lines that fail in the default lane with 0038 neutered and do *not* fail
	// with every gate on. Any stratum, deliberately: which component a row is charged to is a
	// *consequence* of this defect — `load1.wast`'s five land in exec because the instance existed and
	// answered — and filtering on exec would scope the domain to where today's specimen happens to
	// land, which is the enumeration this whole control is the alternative to.
	derived := map[string][]int{}
	total := 0
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		e := engine()
		e.Has = EngineCapabilities()
		var candidates []int
		for _, fs := range s.runWith(e, true).Buckets {
			for _, row := range fs {
				candidates = append(candidates, row.Line)
			}
		}
		if len(candidates) == 0 {
			// The second lane runs only where the first found something, and that is not a cap on
			// the domain: the population's own definition requires a default-lane fail, so a file
			// with none cannot contribute one. The domain is every board file, every time.
			continue
		}
		allOnFail := map[int]bool{}
		for _, fs := range s.RunGated(allOnEngine()).Buckets {
			for _, row := range fs {
				allOnFail[row.Line] = true
			}
		}
		for _, line := range candidates {
			if !allOnFail[line] {
				derived[f] = append(derived[f], line)
				total++
			}
		}
	}

	// The floor, and the mutation it was written for is "forgot to neuter". A derivation that agrees
	// with the registry at 0 is reporting its own blindness.
	if total == 0 {
		t.Errorf("derived 0 rows that fail in the default lane and pass with every gate on, so this "+
			"control is comparing two empty sets and would agree with any registry at all.\n"+
			"\tEither the derivation is measuring a board that already has decision 0038's fix in it "+
			"(check that runWith's second argument is true), or the corpus no longer contains this "+
			"shape — in which case %d registered rows are stale and this control needs re-pointing at "+
			"the risk rather than closing", countSideEffectRegistry())
	}

	// Forward: a derived row nobody explained. This is the arm a new corpus file trips.
	for f, lines := range derived {
		sort.Ints(lines)
		e := sideEffectOfDecline[filepath.Base(f)]
		for _, line := range lines {
			if !containsInt(e.Lines, line) {
				t.Errorf("%s:%d is a fail in the default lane and a pass with every gate on, and no "+
					"sideEffectOfDecline entry claims it.\n"+
					"\tTwo things produce that signature and they have opposite work plans: a gate "+
					"decline whose side effect landed on a healthy instance (register it, naming the "+
					"declined module and the write channel — decision 0038), or the decoder refusing a "+
					"construct it should accept, which is a defect to repair and not to register.",
					f, line)
			}
		}
	}

	// Reverse, and it is the direction that goes stale quietly: a registered line the derivation no
	// longer finds means the fail it was covering is gone, and an entry left behind claims the fix is
	// doing work it is not.
	for file, e := range sideEffectOfDecline {
		found := map[int]bool{}
		for f, lines := range derived {
			if filepath.Base(f) != file {
				continue
			}
			for _, line := range lines {
				found[line] = true
			}
		}
		for _, line := range e.Lines {
			if !found[line] {
				t.Errorf("%s:%d is registered in sideEffectOfDecline but is not a fail in the neutered "+
					"default lane;\n\tthe row it covers is gone, so remove the entry and say in the PR "+
					"what answered it — a stale entry overstates what the registry is holding up",
					file, line)
			}
		}
	}

	t.Logf("decision 0038: %d rows derived across %d files, %d registered",
		total, len(derived), countSideEffectRegistry())
}

// countSideEffectRegistry is the registry's size, for the two messages above. A literal would be a
// second place holding one fact, which is the shape three graves in this repo share.
func countSideEffectRegistry() int {
	n := 0
	for _, e := range sideEffectOfDecline {
		n += len(e.Lines)
	}
	return n
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
