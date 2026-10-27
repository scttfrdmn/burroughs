// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// laneTotals is one lane's board over the board files: the sums of RunGated's counters with features f.
type laneTotals struct{ pass, fail, unsupported, gated, unimplemented, bound int }

// sum is every vector the lane scored. Every lane scores the same vectors, so sums that differ mean a counter is
// missing here, not a vector moving.
func (lt laneTotals) sum() int {
	return lt.pass + lt.fail + lt.unsupported + lt.gated + lt.unimplemented + lt.bound
}

func runLane(t *testing.T, f binary.Features, files []string) laneTotals {
	t.Helper()
	_, engineFor := gateLane(f)
	var lt laneTotals
	for _, name := range files {
		s, err := ParseFile(filepath.Join(suiteDir, name))
		if err != nil {
			t.Errorf("%s: parse: %v", name, err)
			continue
		}
		r := s.RunGated(engineFor())
		lt.pass += r.Pass
		lt.fail += r.Fail
		lt.unsupported += r.Unsupported
		lt.gated += r.Gated
		lt.unimplemented += r.Unimplemented
		lt.bound += r.Bound
	}
	return lt
}

// flippedGates is every Wasm 3.0 gate flipped on Scott's agreement (#1131), with the vectors its own lane answers
// (`TestEachDefaultOffGateOnItsOwnLane`'s reading, pre-registered on #1131). A flip adds its row here in its own PR.
var flippedGates = []struct {
	field    string // the binary.Features field
	answered int    // vectors its lane answers: the flip must move exactly these
}{
	{"ExtendedConst", 70}, // ADR 0107
	{"TailCall", 26},      // ADR 0108
	{"MultiMemory", 645},  // ADR 0109
}

// declineLane is the shipped default with multi-memory's rollback set (`BURROUGHS_MULTI_MEMORY=0`): the lane on
// which decision 0038's side-effect-of-decline mechanism still has its corpus specimens.
//
// **ADR 0109 answered every one of them on the default lane.** `load1.wast:25-29` are 0038's only rows, and
// their declined writer is a two-memory module, so with multi-memory on nothing declines and nothing is gated
// *by* the mechanism. The mechanism is still there, and still reachable: a rollback, an embedder's `Features`,
// or a future corpus file under a gate that stays off. A control comparing two empty sets would agree with
// any registry at all. So the controls whose subject is the mechanism (`TestDeclineSideEffectsAreRegistered`,
// and the registry-versus-run count beside it) run here, where it can still be watched dying, while
// `TestGatedVectors` asserts that the default lane's population is empty.
func declineLane() binary.Features {
	f := binary.DefaultFeatures()
	f.MultiMemory = false
	return f
}

// TestEachFlippedGateMovesOnlyItsOwnVectors is each flip's G1-style check (the chair's ruling of 2026-10-27, item 1:
// "a G1-style check that nothing else moves"). It compares the default board with the flipped gate on, as shipped,
// and off, as the rollback sets it, file by file:
//   - **in every file**, the only change is declined vectors becoming answered: `gated` falls, `pass + bound` rises
//     by the same number, and `fail`, `unsupported` and `unimplemented` are unchanged;
//   - **in total**, the vectors moved equal the gate's own lane reading.
//
// So a flip cannot answer a vector its forecast did not count, and cannot fail one, without this failing.
//
// **Neuter, run:** with ExtendedConst's `answered` set to 69, the total arm fails. With the field left off in
// `DefaultFeatures`, the "is it on" arm fails.
func TestEachFlippedGateMovesOnlyItsOwnVectors(t *testing.T) {
	requireSuite(t)
	files := boardFiles(t)
	on := binary.DefaultFeatures()
	ov := reflect.ValueOf(on)
	for _, g := range flippedGates {
		t.Run(g.field, func(t *testing.T) {
			fv := ov.FieldByName(g.field)
			if !fv.IsValid() || fv.Kind() != reflect.Bool {
				t.Fatalf("binary.Features has no bool field %q", g.field)
			}
			if !fv.Bool() {
				t.Fatalf("%s is listed as flipped, but DefaultFeatures leaves it off", g.field)
			}
			off := on
			reflect.ValueOf(&off).Elem().FieldByName(g.field).SetBool(false)
			_, engOn := gateLane(on)
			_, engOff := gateLane(off)
			moved := 0
			for _, name := range files {
				s, err := ParseFile(filepath.Join(suiteDir, name))
				if err != nil {
					t.Errorf("%s: parse: %v", name, err)
					continue
				}
				a, b := s.RunGated(engOn()), s.RunGated(engOff())
				if a.Fail != b.Fail || a.Unsupported != b.Unsupported || a.Unimplemented != b.Unimplemented {
					t.Errorf("%s: the flip changed more than declined vectors: fail %d→%d, unsupported %d→%d, "+
						"unimplemented %d→%d", name, b.Fail, a.Fail, b.Unsupported, a.Unsupported, b.Unimplemented,
						a.Unimplemented)
				}
				answered := b.Gated - a.Gated
				if answered < 0 || (a.Pass+a.Bound)-(b.Pass+b.Bound) != answered {
					t.Errorf("%s: gated %d→%d but pass+bound %d→%d; a flip may only turn declined vectors into "+
						"answered ones", name, b.Gated, a.Gated, b.Pass+b.Bound, a.Pass+a.Bound)
				}
				moved += max(answered, 0)
			}
			if moved != g.answered {
				t.Errorf("%s's flip moved %d vectors; its lane reading, pre-registered on #1131, is %d",
					g.field, moved, g.answered)
			}
			t.Logf("%s: the flip moved %d declined vectors to answered, and nothing else", g.field, moved)
		})
	}
}

// TestEachDefaultOffGateOnItsOwnLane is the per-gate reading a G-1 flip forecast needs (the chair's addendum of
// 2026-10-26: "the flips", read as one pre-registered forecast per gate for Scott's stamp). For every gate that
// `binary.DefaultFeatures` leaves off, it runs the board with **that gate alone** added to the defaults. Each lane
// reports how many of the default lane's declined vectors it answers, and how they answer.
//
// The all-gates-on lane (`TestAllGatesOnLeavesNothingGated`) says the gates together leave nothing failing. It
// cannot say what flipping ONE gate does, which is what a flip changes. A vector that needs two off gates stays
// `gated` in each single lane, and shows up here as the lane's residue.
//
// The gates are found by reflection over `binary.Features`, as `allFeaturesOn` finds them. A gate added later,
// default-off, therefore gets a lane without an edit here.
//
// **Asserted:**
//   - each lane fails no vector the default lane does not, which is G-1's zero-defect requirement for that gate;
//   - each lane answers at least one vector the default lane declines. A lane that answers none is a gate the
//     board cannot see, and its forecast would have no subject.
//
// The figures are logged, not pinned, because a forecast is read off a run, not off this file.
//
// **Neuter, run:** `runLane` built every lane with `binary.DefaultFeatures()` instead of its own `f`, and all six
// default-off gates failed the answered-none arm. Every lane's counters also balance against the default lane
// (`sum`). That check exists because the first draft omitted `Bound`, and six of EH's answered vectors read as
// vanished.
func TestEachDefaultOffGateOnItsOwnLane(t *testing.T) {
	requireSuite(t)
	files := boardFiles(t)
	def := binary.DefaultFeatures()
	base := runLane(t, def, files)
	t.Logf("default lane over %d files: %d pass, %d fail, %d unsupported, %d gated, %d unimplemented, %d bound",
		len(files), base.pass, base.fail, base.unsupported, base.gated, base.unimplemented, base.bound)

	dv := reflect.ValueOf(def)
	lanes := 0
	for i := range dv.NumField() {
		field := dv.Type().Field(i)
		if field.Type.Kind() != reflect.Bool {
			t.Fatalf("Features.%s is %s, not a bool: this test cannot turn it on", field.Name, field.Type.Kind())
		}
		if dv.Field(i).Bool() {
			continue // on by default: no flip to forecast
		}
		lanes++
		f := def
		reflect.ValueOf(&f).Elem().Field(i).SetBool(true)
		got := runLane(t, f, files)
		answered := base.gated - got.gated
		t.Logf("gate %s alone: %d pass (+%d), %d fail (+%d), %d unsupported, %d gated (-%d answered), "+
			"%d unimplemented, %d bound (+%d)", field.Name, got.pass, got.pass-base.pass, got.fail, got.fail-base.fail,
			got.unsupported, got.gated, answered, got.unimplemented, got.bound, got.bound-base.bound)
		if got.sum() != base.sum() {
			t.Errorf("gate %s's lane scored %d vectors and the default lane %d: a counter is missing from laneTotals",
				field.Name, got.sum(), base.sum())
		}
		if got.fail > base.fail {
			t.Errorf("gate %s alone fails %d vectors the default lane does not: a flip of it would ship them",
				field.Name, got.fail-base.fail)
		}
		if answered <= 0 {
			t.Errorf("gate %s alone answers none of the default lane's %d declined vectors: the board cannot see "+
				"this gate, so a forecast for it has no subject", field.Name, base.gated)
		}
	}
	if lanes == 0 {
		t.Fatal("no default-off gate was found, so this test read nothing")
	}
}
