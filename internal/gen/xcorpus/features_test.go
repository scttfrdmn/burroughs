package xcorpus

import (
	"reflect"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestFeatureFlagsCoverTheTrackedGates is the tripwire on the generator's flag set.
//
// The corpus records the encodings of the *tracked union* (contract §9 G-2), so the flags
// wast2json runs with have to track `Features`. The failure this exists to catch is silent by
// construction: a ninth gate added to the struct would leave the generator enabling eight
// proposals, the corpus would come back a few hundred modules smaller, every floor in
// xcorpus_test.go would still pass — they are floors, and a smaller population clears them —
// and the accept-direction evidence for the new proposal would simply never exist. Nothing on
// the board would say so, because a vector for a gated feature is scored `gated`, not `fail`.
//
// Reflection rather than a list, for the reason 0006/#33 gives: an enumeration is a sample, and
// the sample's blind spot is exactly the field someone just added. The mapping's *values* are
// still testimony about wabt's vocabulary — `ExceptionHandling` is `--enable-exceptions`, not
// `--enable-exception-handling` — and no machine here can check that; what is checked is that
// the mapping is total and stays total.
func TestFeatureFlagsCoverTheTrackedGates(t *testing.T) {
	ft := reflect.TypeOf(binary.Features{})

	// The vacuity check, and it is not decoration: this test's whole method is "iterate the
	// struct's fields and look each one up". Over zero fields it agrees with an empty map
	// perfectly and reports a green (#29). A field count is knowable exactly, so it is pinned
	// exactly beside its floor — *floors bound the catastrophic case; only an exact count sees a
	// small silent loss* (grave #105).
	if ft.NumField() == 0 {
		t.Fatal("binary.Features has no fields: every assertion below is inside a loop over " +
			"them, so this test would pass by asking nothing")
	}
	if got, want := ft.NumField(), len(featureFlag); got != want {
		t.Errorf("binary.Features has %d gates and featureFlag has %d entries: the corpus "+
			"enables one proposal per tracked gate, and a gate with no entry is one whose "+
			"accept direction this corpus silently cannot speak to", got, want)
	}

	for i := range ft.NumField() {
		name := ft.Field(i).Name
		flag, ok := featureFlag[name]
		if !ok {
			t.Errorf("binary.Features.%s has no featureFlag entry: add it (or `\"\"` with the "+
				"reason, if wabt has that proposal on by default), then regenerate with "+
				"`make xcorpus` — the corpus is a snapshot, so a flag added here changes "+
				"nothing until it is re-cut", name)
			continue
		}
		if flag == "" {
			continue // recorded as on-by-default; the entry's comment carries the reason
		}
		if !strings.HasPrefix(flag, "--enable-") {
			t.Errorf("featureFlag[%q] = %q, want a --enable- flag: the generator passes these "+
				"to wast2json verbatim, and an unrecognized flag makes it fail every file, "+
				"which arrives as an empty corpus rather than as an error about a flag",
				name, flag)
		}
	}

	// And the assembled argv actually carries them. The map could be perfect while `features`
	// dropped everything — a control that checks the source of a value and not the value that
	// gets used is testing the helper, not the path.
	for name, flag := range featureFlag {
		if flag == "" {
			continue
		}
		found := false
		for _, got := range features {
			if got == flag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("featureFlag[%q] = %q but the assembled features argv is %v", name, flag, features)
		}
	}
	for _, extra := range extraFlags {
		found := false
		for _, got := range features {
			if got == extra {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("extraFlags names %q but the assembled features argv is %v", extra, features)
		}
	}

	// The omissions are the measured part of the criterion, so they are pinned: these six flags
	// exist in wabt and are deliberately off, because they are not in the tracked union and
	// three of them re-encode modules the baseline grammar already describes. If one is ever
	// added, it should be because a gate for it was added above — not by drifting back toward
	// `--enable-all`, which is what this file was written after.
	for _, off := range []string{
		"--enable-annotations", "--enable-code-metadata", "--enable-extended-const",
		"--enable-custom-page-sizes", "--enable-compact-imports", "--enable-wide-arithmetic",
	} {
		for _, got := range features {
			if got == off {
				t.Errorf("the generator passes %s, which is outside the tracked union: measured, "+
					"the six untracked flags together buy 1 module and cost 7 re-encodings of "+
					"modules the standard grammar already describes (see the features comment)", off)
			}
		}
	}

	t.Logf("generator flags: %v", features)
}
