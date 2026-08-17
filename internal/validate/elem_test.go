// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestElemSegmentTableIndexResolves is `check_elemmode`'s Active arm (valid.ml:1086-1095) in both
// directions, and the rows are chosen from a **pre-registered list of which corpus vectors this rule
// may move** rather than from the shapes the implementation happens to have.
//
// # Why the list came first
//
// The admission bucket is keyed by *message*, and `unknown table` is a message five different
// reference rules produce — `table c x` has twelve call sites in `valid.ml`, of which this is one.
// So the bucket's size forecasts the reward and says nothing about which rows supply it, and a
// right-sized census delta assembled from the wrong rows reads exactly like a correct one. Naming the
// rows first costs nothing and makes an over-firing implementation visible in the *identity* of what
// moved instead of only in the total. (Ruling: Scott, on the #337 relay.)
//
// The family, measured with the harness before this rule existed — sixteen vectors across two keys,
// not the twelve a single-line grep finds, because four are multi-line assertions:
//
//	want                rows                                          state before
//	--------------------+---------------------------------------------+-------------
//	`unknown table`      elem.wast:721                                 admission  ← this rule's
//	`unknown table`      func_ptrs.wast:32, :33                        admission  ← this rule's
//	`unknown table`      table.wast:23, :24, :51, :52                  admission  ← this rule's
//	`unknown table`      exports.wast:154, :158, :162                  pass       (check_export)
//	`unknown table`      call_indirect.wast:790                        pass       (instruction operand)
//	`unknown table`      return_call_indirect.wast:435                 gated      (tail calls)
//	`unknown table 0`    table_init.wast:390, :404                     pass       (bulk operand)
//	`unknown table 0`    table_init64.wast:575, :589                   pass       (bulk operand)
//
// Seven predicted movers against a seven-row bucket, which is the kind of agreement that wants a
// second mechanism rather than a nod: it holds only because all nine other rows are already settled,
// and that is measured above rather than assumed.
//
// # The row list leaves one question open, and the modules close it without the oracle
//
// Six of the seven live outside `elem.wast`, which falsified the scouting claim that the file named the
// rule. But *why* the claim was false has two readings and the message oracle cannot separate them,
// because all seven want the same string: either the **file was a bad proxy** for rule ownership, or
// **this rule is over-firing** onto modules whose intended defect is elsewhere and the seven agree by
// coincidence. That is the resolution limit `authority_test.go` now documents, arriving on the first
// slice after it was written down.
//
// The closing check needs no oracle — read the modules. All seven are an active element segment naming
// table index 0 in a module that declares no table, so the answer is the first reading: the file was a
// proxy and the proxy was wrong. The four distinct module texts behind the seven vectors:
//
//	module text                                     vectors
//	------------------------------------------------+----------------------------------------------
//	(module (elem (i32.const 0)))                    func_ptrs.wast:32, table.wast:23, table.wast:51
//	(module (elem (i32.const 0) 0) (func))           func_ptrs.wast:33
//	(module (elem (i32.const 0) $f) (func $f))       table.wast:24, table.wast:52
//	(module (func $f) (elem (i32.const 0) $f))       elem.wast:721
//
// Two of the three files state the ownership in prose while filing the vector under a table or
// function-pointer heading — `;; Elem segments with no table` (`table.wast:49`) and `;; Element without
// table` (`elem.wast:719`). The suite was never ambiguous about whose rule these are; the file name was
// the only thing that ever suggested otherwise.
//
// **And this read corrected a second claim of the same shape, in the sentence that reported the
// first.** That sentence read "the seven are five distinct modules", derived by noticing that
// `table.wast:51-52` are verbatim repeats of `:23-24`. There are **four**, not five: `(module (elem
// (i32.const 0)))` appears three times, and the third is in `func_ptrs.wast`. The repeat was deduped
// *within* the file where it had been noticed and not *across* files — file-scoped reasoning again, one
// level down, inside the correction of a file-scoped error. Recorded because the recurrence is the
// lesson: a dedup is a claim about a population, and scoping it to the container it was spotted in
// inherits the container's blind spot. (Directive: Scott, PR #339 review.)
//
// # The rows below, and which are falsifiable by the board
//
// R1 and R5 are witnessed six times over, and by accident rather than by design: `(elem (i32.const
// 0))` is the shortest way to write an active segment, so the suite reaches the implicit-table form by
// economy. R2 is the one worth having — `func_ptrs.wast:35` is the same segment shape in a module
// where the table *does* exist, wanting `type mismatch` from the deferred offset check, so an
// implementation that refuses on segment shape rather than on index resolution converts a row this
// slice has no right to convert. It sits two lines below a positive row in the same file.
//
// # The battery each row was watched die under
//
// Five mutations, each run against these rows and against both lanes, because a control's green is
// worth what its falsification cost. The interesting column is the last one — whether the *board*
// could have caught it without these rows:
//
//	mutation                              rows that fail        default lane      board sees it
//	--------------------------------------+---------------------+-----------------+--------------
//	drop the mode guard                    R4, R5                60822/203         yes, 2 vectors
//	delete the loop                        R1, R3, R6 (+M6)      60817/208         yes, the 7 back
//	refuse on segment shape                R2, R6 (+M6)          60806/219         yes, 11 below
//	perturb `tableTypeAt`'s parenthetical  M6 only               60824/201         **no**
//	treat an implicit index as absent      none                  60817/208         yes, −7 both lanes
//
// Two of those rows are the whole argument for this file existing beside the board. **The
// parenthetical mutation leaves the suite entirely green** — every vector matches `unknown table` or
// `unknown table 0` by substring, so the text after the index is unconstrained by the corpus and the
// agreement test below is the only instrument that reads it. And **the implicit-index mutation fails
// no row here at all**: it is board-only, which is why its seven is quoted in the blind-spot header
// rather than asserted as a unit row. The two failure modes are disjoint, and neither instrument
// covers the other's.
//
// Deleting the loop fails M6 through its *vacuity guard* rather than its comparison — two nils agree
// perfectly about a rule neither applied, and the guard is what makes that a fatal instead of a pass.
func TestElemSegmentTableIndexResolves(t *testing.T) {
	table := []binary.Table{{Limits: binary.Limits{Min: 1}}}

	for _, tc := range []struct {
		name   string
		mod    binary.Module
		want   error  // nil for the accept direction
		detail string // substring of the message, where the corpus matches on one
	}{
		{
			// R1: the seven movers' shape. Implicit table index 0 still resolves through the lookup —
			// flags 0 means active-at-table-0, not active-at-no-table — so a module with no table at
			// all refuses here rather than silently treating the implicit index as absent.
			name: "R1 active implicit table, module declares none",
			mod:  binary.Module{Elems: []binary.ElemSegment{{Mode: binary.ElemActive}}},
			want: ErrUnknownTable, detail: "unknown table 0",
		},
		{
			// R2: the negative control, and the reason the row list was written first. The table
			// exists, so this rule must accept and leave the vector to the deferred checks.
			name: "R2 active implicit table, module declares one",
			mod:  binary.Module{Tables: table, Elems: []binary.ElemSegment{{Mode: binary.ElemActive}}},
			want: nil,
		},
		{
			name: "R3 active explicit table out of scope",
			mod: binary.Module{
				Tables: table,
				Elems:  []binary.ElemSegment{{Mode: binary.ElemActive, TableIndex: 4}},
			},
			want: ErrUnknownTable, detail: "unknown table 4",
		},
		{
			// R4/R5: the reference's other two arms are `()`. A passive segment in a module with no
			// table is legal, and its TableIndex field is 0 rather than absent — so an implementation
			// that resolved the index unconditionally would refuse a valid module on the strength of a
			// field the mode makes meaningless. This is the board-falsifiable half: the corpus has
			// passive segments in tableless modules, so dropping the mode guard breaks two passing
			// vectors rather than merely failing here — measured, and in the battery table above.
			name: "R4 passive segment, module declares no table",
			mod:  binary.Module{Elems: []binary.ElemSegment{{Mode: binary.ElemPassive}}},
			want: nil,
		},
		{
			// Declarative is the arm where the two segment kinds diverge: `check_elemmode` answers `()`
			// where `check_datamode` answers `assert false`.
			name: "R5 declarative segment, module declares no table",
			mod:  binary.Module{Elems: []binary.ElemSegment{{Mode: binary.ElemDeclarative}}},
			want: nil,
		},
		{
			// R6: the phase reports the *first* offending segment, and the wrap names which one. Two
			// bad segments would otherwise be indistinguishable from one.
			name: "R6 second segment is the offender",
			mod: binary.Module{
				Tables: table,
				Elems: []binary.ElemSegment{
					{Mode: binary.ElemActive},
					{Mode: binary.ElemActive, TableIndex: 9},
				},
			},
			want: ErrUnknownTable, detail: "element segment 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := modulePre(&tc.mod)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("modulePre refused a module this rule has no rule against: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("modulePre accepted a module whose element segment names a table it does not "+
					"declare; want %v", tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("modulePre = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("modulePre = %q, want it to contain %q — the corpus matches this message by "+
					"substring (0003), so the index and its category are load-bearing text",
					err, tc.detail)
			}
		})
	}
}

// TestElemPhaseAndExportPhaseAgreeOnUnknownTable is the M6 row one index space over.
//
// There are now two functions producing ErrUnknownTable — `tableTypeAt` (this phase and the bulk
// instructions) and `indexInScope` (the export phase) — with the parenthetical spelled separately in
// each. They agree today, which is exactly the condition under which the board cannot see that they
// are two: no vector needs both messages from one module, so a drift in either is invisible until a
// vector arrives that reads the wrong one.
//
// **Invisible is measured, not argued.** Rewriting `tableTypeAt`'s `(%d in scope)` to `(%d declared)`
// leaves the default lane at 60824/201 — an entirely green suite over a message this engine now spells
// two ways — and fails this test and `TestBulkRejectsWithTheRuleThatRefused`, nothing else. Those two
// are pinning different things and both are needed: the bulk test holds one producer's *text*, this one
// holds the two producers' *agreement*, and a matched drift in both spellings passes the first.
//
// **This test is the only reader of that mutation, so deleting it uncovers the mutation silently.** The
// board does not cover it — that is the measurement above, not a guess — and neither does any sentinel
// check, since `errors.Is` passes on both producers whatever the parenthetical says. The substring
// comparison below is therefore load-bearing rather than a loose assertion a tightening pass should
// replace with an identity check: identity is exactly the instrument that cannot see this. (Directive:
// Scott, PR #339 review — a mutation with one reader needs the reader to say so.)
//
// Asserted as text rather than by sentinel identity, because `errors.Is` passes on both while the
// parenthetical is what the substring match consumes.
func TestElemPhaseAndExportPhaseAgreeOnUnknownTable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tables []binary.Table
		idx    uint32
	}{
		{"module declares no table", nil, 0},
		{"module declares some", []binary.Table{{}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viaElem := modulePre(&binary.Module{
				Tables: tc.tables,
				Elems:  []binary.ElemSegment{{Mode: binary.ElemActive, TableIndex: tc.idx}},
			})
			viaExport := moduleExports(&binary.Module{
				Tables:  tc.tables,
				Exports: []binary.Export{{Name: "t", Kind: binary.ExternTable, Index: tc.idx}},
			})
			// Vacuity first: two nils agree perfectly about a rule neither applied.
			if viaElem == nil || viaExport == nil {
				t.Fatalf("expected both phases to refuse table %d with %d declared; elem path = %v, "+
					"export path = %v", tc.idx, len(tc.tables), viaElem, viaExport)
			}
			gotElem, gotExport := errors.Unwrap(viaElem), errors.Unwrap(viaExport)
			if gotElem == nil || gotExport == nil {
				t.Fatalf("both phases must wrap with %%w; elem unwrapped to %v, export to %v",
					gotElem, gotExport)
			}
			if gotElem.Error() != gotExport.Error() {
				t.Errorf("the same missing table reads %q through the element-segment phase and %q "+
					"through the export phase — one rule, two producers, and the corpus cannot tell "+
					"them apart", gotElem, gotExport)
			}
		})
	}
}
