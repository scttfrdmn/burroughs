// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The export phase has three properties the board cannot see, and this file is all three.
//
// The slice's own falsification bill, both lanes, `moduleExports` in place:
//
//	M1  fuse the two loops                        default 60817/208   all-on 64708/338   — no change
//	M2  drop the duplicate-name check             default 60798/227   all-on 64688/358   — -19 / -20
//	M3  exportExists never refuses                default 60805/220   all-on 64696/350   — -12 / -12
//	M4  indexInScope off by one (`<=`)            default 60808/217   all-on 64699/347   — -9  / -9
//	M5  table arm reads the func index space      default 60811/214   all-on 64702/344   — -6  / -6
//	M6  memory arm spelled again, not delegated   default 60817/208   all-on 64708/338   — no change
//	M7  tag arm always accepts                    default 60817/208   all-on 64708/338   — no change
//
// M2 through M5 are covered by the suite and need nothing here. **M1, M6 and M7 moved neither lane**,
// so each gets a unit row below — the board is not an instrument for them, and an intention is not a
// tripwire. M5's -6 is worth reading twice: three of those are the admissions, and three are *valid*
// modules the wrong index space would have rejected, so that mutation is caught from both directions
// by accident of the corpus rather than by design.

// TestExportIndexesResolveBeforeAnyNameIsCompared is the M1 row: the reference maps `check_export`
// across every export and only then calls `check_names` (valid.ml:1168-1169), so on a module with
// both defects the index wins.
//
// Fusing the two loops in moduleExports moves neither lane, which is why this exists as a unit row.
// It is the same argument as the `check_limits` order in the sibling file: an ordering the corpus
// cannot witness is still the rule, and the only way to hold it is to assert it directly.
func TestExportIndexesResolveBeforeAnyNameIsCompared(t *testing.T) {
	m := &binary.Module{
		Funcs: []binary.Func{{}},
		Exports: []binary.Export{
			{Name: "a", Kind: binary.ExternFunc, Index: 0},
			{Name: "a", Kind: binary.ExternFunc, Index: 99},
		},
	}
	err := moduleExports(m)
	switch {
	case err == nil:
		t.Fatal("moduleExports accepted a module with a duplicate export name and an out-of-scope " +
			"function index — two defects, neither reported")
	case errors.Is(err, ErrDuplicateExport):
		t.Errorf("moduleExports reported %v, want the unknown-index refusal: the name comparison "+
			"ran before every index resolved, which is the fused-loop shape the reference's "+
			"sequence forbids", err)
	case !errors.Is(err, ErrUnknownFunc):
		t.Errorf("moduleExports reported %v, want ErrUnknownFunc", err)
	}
}

// TestExportMemoryMessageIsTheDataSegmentPaths is the M6 row: the memory arm of exportExists
// delegates to memoryExists rather than spelling the lookup a second time, and replacing the
// delegation with a hand-rolled copy moves neither lane.
//
// So the board is blind to the copy *while the two agree*, which is the whole hazard — the copy that
// drifts is not the copy you wrote today. This asserts the two phases render the same rule
// identically, in both of memoryExists' branches, which is the property a second copy would break.
func TestExportMemoryMessageIsTheDataSegmentPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		mems []binary.Memory
		idx  uint32
	}{
		// memoryExists has two messages, and a copy could match one and miss the other.
		{"module declares no memory", nil, 0},
		{"module declares some", []binary.Memory{{}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viaExport := moduleExports(&binary.Module{
				Memories: tc.mems,
				Exports:  []binary.Export{{Name: "m", Kind: binary.ExternMemory, Index: tc.idx}},
			})
			viaData := modulePre(&binary.Module{
				Memories: tc.mems,
				Datas:    []binary.DataSegment{{MemIndex: tc.idx}},
			})
			// Vacuity guard first: two nils compare equal and would report agreement about a rule
			// neither path applied.
			if viaExport == nil || viaData == nil {
				t.Fatalf("expected both phases to refuse memory %d with %d declared; export path = %v, "+
					"data path = %v", tc.idx, len(tc.mems), viaExport, viaData)
			}
			// Each phase wraps with its own prefix — `export 0 ("m")` versus `data segment 0` — so the
			// comparison is on what they wrap, which is the rule's own testimony.
			gotExport, gotData := errors.Unwrap(viaExport), errors.Unwrap(viaData)
			if gotExport == nil || gotData == nil {
				t.Fatalf("both phases must wrap the lookup's error with %%w; export unwrapped to %v, "+
					"data to %v", gotExport, gotData)
			}
			if gotExport.Error() != gotData.Error() {
				t.Errorf("the same missing memory reads %q through the export phase and %q through the "+
					"data phase — one rule, two messages, which is the drift the delegation prevents",
					gotExport, gotData)
			}
		})
	}
}

// TestExportTagArmResolvesTagIndexes is the M7 row, and it is the *only* instrument for that arm:
// making it accept unconditionally moves neither lane, even with every gate on, so no vector in the
// corpus exports a tag with an out-of-scope index.
//
// That is the i64-range situation from the limits slice repeated one function over — an arm whose
// *existence* nothing samples — and it gets the same treatment: assert both directions directly
// rather than leave a branch that looks exactly like its four falsifiable siblings.
func TestExportTagArmResolvesTagIndexes(t *testing.T) {
	one := &binary.Module{Tags: []binary.Tag{{}}}
	if err := exportExists(one, binary.ExternTag, 0); err != nil {
		t.Errorf("exportExists refused tag 0 of a module declaring one tag: %v — an over-rejection, "+
			"and with the EH gate on that is a valid module the engine would turn away", err)
	}
	err := exportExists(one, binary.ExternTag, 1)
	if err == nil {
		t.Fatal("exportExists accepted tag 1 of a module declaring one tag")
	}
	if !errors.Is(err, ErrUnknownTag) {
		t.Errorf("exportExists(tag 1) = %v, want ErrUnknownTag — the reference's `lookup \"tag\"` "+
			"(valid.ml:45), whose message the sentinel is a copy of", err)
	}
}

// TestExportExistsCoversEveryExternKind checks the switch against the *kind space* rather than
// against the arms the switch happens to have.
//
// `exhaustive` already fails the build on a missing case, so this is not a second copy of that check:
// it asserts the fallthrough return is unreachable *by the enumeration*, and that every real kind
// resolves index 0 of a module that declares one of everything. A kind quietly returning nil for the
// wrong reason — the shape M7 showed the board cannot see — fails here.
func TestExportExistsCoversEveryExternKind(t *testing.T) {
	one := &binary.Module{
		Funcs:    []binary.Func{{}},
		Tables:   []binary.Table{{}},
		Memories: []binary.Memory{{}},
		Globals:  []binary.Global{{}},
		Tags:     []binary.Tag{{}},
	}
	for _, kind := range []binary.ExternKind{
		binary.ExternFunc, binary.ExternTable, binary.ExternMemory, binary.ExternGlobal, binary.ExternTag,
	} {
		if err := exportExists(one, kind, 0); err != nil {
			t.Errorf("exportExists(%v, 0) on a module declaring one of every kind = %v, want accept",
				kind, err)
		}
		if err := exportExists(one, kind, 1); err == nil {
			t.Errorf("exportExists(%v, 1) accepted an index one past the single declared entry — the "+
				"off-by-one M4 measured at nine vectors, here per kind", kind)
		}
	}
}
