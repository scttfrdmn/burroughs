// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build burroughs_endtable

package interp

import (
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/spec"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// TestDecodedPairingTableAgreesWithTheScan is the tagged lane's oracle test: for every function of
// every module the spec suite decodes, the table the decoder built (0048's arena, reached through
// `Instance.frameEnds`) must say exactly what `matchEnd` says.
//
// # Why `matchEnd` is the authority and not a second implementation of the same idea
//
// `matchEnd` is what the shipping lane does, so it is the behaviour the gate promises not to change.
// A hand-written depth counter here would be a *third* opinion about block structure, agreeing with
// the arena for the same reason the arena might be wrong — both would be walking `Body` with a
// stack, written the same afternoon by the same author. The decoder's pairing comes from somewhere
// genuinely different: `structural`'s own grammar recursion, which pairs a header with its END
// because it *read* them, not because it counted. Two independent mechanisms, which is what makes
// the agreement worth anything.
//
// # It checks the −1s, and that half is not decoration
//
// The table is dense over `len(Body)` and 74.4% of its slots describe no header (0048's census).
// Checking only the openers would pass on a table that was garbage everywhere else — and everywhere
// else is where a stale scratch buffer, an off-by-one base, or a body filed against the previous
// function would land. A wrong `end` at a *non*-header index is not inert either: `endOf` indexes by
// `pc` without asking what is there, so a stray non-negative slot is a branch target the interpreter
// would take.
//
// That is witnessed rather than argued, and the measurement inverted what the argument expected.
// Three bugs were injected into the mechanism and the −1 clause catches **two** of them, alone:
//
//   - deleting `decodeFuncBody`'s `beginFuncEnds()` (the stale scratch) fails at `align.wast` func 3
//     slot 14, *an `i32.const`* — and the opener half does not fire at all, because the previous
//     body's pairs land on indices that mostly hold no header;
//   - shifting the filed base by one fails the same way, on a blank slot, not on an opener.
//
// Only the third — an off-by-one on `end` itself — is what the opener half catches, and that one it
// catches 2020 times. So the half of this test that looks like bookkeeping is the half that sees the
// two bugs about *where a table lives*, which are precisely the bugs an arena introduces over a
// per-function slice. Written down because the first draft of this comment had it the other way
// round and would have been a plausible, unchecked, load-bearing sentence.
//
// # The floors
//
// Every count has one, because this test's realistic failure is not disagreement — it is asking
// nothing. A corpus that stops decoding, an arena that comes back empty, a `FuncEnds` that returns
// nil for everything: each of those makes a loop body run zero times and a comparison agree
// perfectly. The `noTable` floor is the one that is easy to leave out: it asserts that the
// `EndsOff == 0` path is *also* exercised, since 86.5% of corpus functions open no block at all and
// that population is what would hide a table filed one function late.
func TestDecodedPairingTableAgreesWithTheScan(t *testing.T) {
	testenv.RequireSuite(t, scanDistSuiteDir)

	paths, err := testenv.SuitePaths(scanDistSuiteDir)
	if err != nil {
		t.Fatalf("SuitePaths %s after RequireSuite passed: %v", scanDistSuiteDir, err)
	}

	var (
		modules   int
		funcs     int
		withTable int
		noTable   int
		openers   int
		blanks    int
	)

	for _, p := range paths {
		s, err := spec.ParseFile(filepath.Join(scanDistSuiteDir, p))
		if err != nil {
			// The harness's business, not this test's; the board scores parse failures.
			continue
		}
		for _, c := range s.Commands {
			img, ok := scanDistImage(c)
			if !ok {
				continue
			}
			m, err := binary.DecodeModule(img)
			if err != nil {
				// Expected in bulk: the suite is mostly modules that must fail to decode.
				continue
			}
			modules++
			for i := range m.Funcs {
				fn := &m.Funcs[i]
				body := fn.Body
				funcs++

				tbl := m.FuncEnds(fn)
				if len(tbl) == 0 {
					noTable++
					// A body with a header and no table would be a silently unbuilt table:
					// correct output through the fallback, and the gate doing nothing.
					for pc := range body {
						if !endPortIsHeader(body[pc]) {
							continue
						}
						t.Fatalf("%s func %d: body opens a block at %d but has no pairing table "+
							"(EndsOff=%d); the gated lane would fall back to the scan and read as green",
							p, i, pc, fn.EndsOff)
					}
					continue
				}
				withTable++

				if len(tbl) != len(body) {
					t.Fatalf("%s func %d: table is %d slots for a %d-instruction body; the extent comes "+
						"from len(Body), so a mismatch means the offset names another body's table",
						p, i, len(tbl), len(body))
				}

				for pc := range body {
					if !endPortIsHeader(body[pc]) {
						if tbl[pc] != -1 {
							t.Fatalf("%s func %d: slot %d is %d but no header opens there "+
								"(op %#02x/%#02x); endOf indexes by pc without asking, so this is a "+
								"branch target the interpreter would take",
								p, i, pc, tbl[pc], body[pc].Prefix, body[pc].Op)
						}
						blanks++
						continue
					}
					want, err := matchEnd(body, pc)
					if err != nil {
						t.Fatalf("%s func %d: matchEnd failed at %d on a decoded body: %v", p, i, pc, err)
					}
					if int(tbl[pc]) != want {
						t.Errorf("%s func %d: pairing table says %d closes the header at %d, matchEnd says %d",
							p, i, tbl[pc], pc, want)
					}
					openers++
				}
			}
		}
	}

	// The floors, sized well under what the corpus supplies (0048 measured 4216 modules, 9393
	// functions, 1267 of them with an opener, 2020 openers) so a corpus that grows does not have to
	// come back here, and one that collapses does.
	for _, f := range []struct {
		name string
		got  int
		min  int
	}{
		{"modules decoded", modules, 500},
		{"functions seen", funcs, 2000},
		{"functions with a table", withTable, 500},
		{"functions with no table", noTable, 500},
		{"openers checked", openers, 1000},
		{"blank slots checked", blanks, 2000},
	} {
		if f.got < f.min {
			t.Errorf("%s = %d, want at least %d: below this the agreement above is between two "+
				"nearly-empty sets and would hold however the mechanism were broken", f.name, f.got, f.min)
		}
	}
	t.Logf("%d modules · %d functions (%d with a table, %d without) · %d openers and %d blank slots "+
		"agree with matchEnd", modules, funcs, withTable, noTable, openers, blanks)
}

// endPortIsHeader reports whether this instruction opens a block, using the interpreter's own
// opcode constants rather than the decoder's — the two packages must agree on what a header is, and
// this test is one of the places that would notice if they stopped.
func endPortIsHeader(ins binary.Instr) bool {
	if ins.Prefix != 0x00 {
		return false
	}
	switch ins.Op {
	case opBlock, opLoop, opIf, opTryTable:
		return true
	}
	return false
}
