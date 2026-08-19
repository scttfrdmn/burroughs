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
// **Every active row below carries `Offset: c0()`, and it was not decoration when it was added.**
// The rows were written with the offset field left at nil, which cost nothing while `check_const`'s
// type half was unwritten — an absent expression is a sequence of no instructions, and the only rule
// reading it counted `global.get`s. #328 made the offset *typed*, and a typed empty expression is a
// frame that never closes, so R2 began failing in the accept direction and R6 began reporting segment
// 0 instead of segment 1. Neither is a defect in the rule these rows test; both are the fixture being
// under-specified in a field the rule does not read. A fixture is well-formed in every respect but the
// one under test, or the row moves when some *other* rule arrives.
func TestElemSegmentTableIndexResolves(t *testing.T) {
	table := []binary.Table{decodedTable(binary.FuncRef, 1)}

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
			mod: binary.Module{Elems: []binary.ElemSegment{
				{Mode: binary.ElemActive, ElemType: binary.FuncRef, Offset: c0()},
			}},
			want: ErrUnknownTable, detail: "unknown table 0",
		},
		{
			// R2: the negative control, and the reason the row list was written first. The table
			// exists, so this rule must accept and leave the vector to the deferred checks.
			name: "R2 active implicit table, module declares one",
			mod: binary.Module{
				Tables: table,
				Elems: []binary.ElemSegment{
					{Mode: binary.ElemActive, ElemType: binary.FuncRef, Offset: c0()},
				},
			},
			want: nil,
		},
		{
			name: "R3 active explicit table out of scope",
			mod: binary.Module{
				Tables: table,
				Elems: []binary.ElemSegment{
					{Mode: binary.ElemActive, ElemType: binary.FuncRef, TableIndex: 4, Offset: c0()},
				},
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
			mod: binary.Module{Elems: []binary.ElemSegment{
				{Mode: binary.ElemPassive, ElemType: binary.FuncRef},
			}},
			want: nil,
		},
		{
			// Declarative is the arm where the two segment kinds diverge: `check_elemmode` answers `()`
			// where `check_datamode` answers `assert false`.
			name: "R5 declarative segment, module declares no table",
			mod: binary.Module{Elems: []binary.ElemSegment{
				{Mode: binary.ElemDeclarative, ElemType: binary.FuncRef},
			}},
			want: nil,
		},
		{
			// R6: the phase reports the *first* offending segment, and the wrap names which one. Two
			// bad segments would otherwise be indistinguishable from one.
			name: "R6 second segment is the offender",
			mod: binary.Module{
				Tables: table,
				Elems: []binary.ElemSegment{
					{Mode: binary.ElemActive, ElemType: binary.FuncRef, Offset: c0()},
					{Mode: binary.ElemActive, ElemType: binary.FuncRef, TableIndex: 9, Offset: c0()},
				},
			},
			want: ErrUnknownTable, detail: "element segment 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := modulePre(&tc.mod, declaredFuncs(&tc.mod))
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
		{"module declares some", []binary.Table{decodedTable(binary.FuncRef, 0)}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			elemMod := &binary.Module{
				Tables: tc.tables,
				Elems:  []binary.ElemSegment{{Mode: binary.ElemActive, TableIndex: tc.idx}},
			}
			viaElem := modulePre(elemMod, declaredFuncs(elemMod))
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

// TestElemSegmentFunctionIndicesResolve is #391, riding slice 10 (ADR 0036) — the *lookup* half of
// `check_elem`'s `check_const` (`valid.ml:1097-1101`), which resolves the function indices a segment's
// `ref.func` initialisers name.
//
// # Two wire forms, and #391 named the wrong one — the assertion that was supposed to catch that
// # agreed with the bug
//
// An element segment carries its functions either as a plain index vector or as a vector of constant
// expressions. #391 recorded that the wat front end desugars the two admissions
// (`call_indirect.wast:1037`, `return_call_indirect.wast:600`, both literally
// `(module (table funcref (elem 0 0)))`) into the **index** form, and put the fix there. That account
// is **false under the reference**: the inline-table sugar takes the table's own reftype down the
// element arm (`parser.mly:1215`), so the segment's type is `funcref` = `(Null, FuncHT)`, for which
// `is_elem_kind` is false (`encode.ml:1044-1046`) and the encoder must reach for flag 4 — the
// **expression** form. Grave #401 was our parser writing `(ref func)` there instead, which is the only
// reason the account read true.
//
// The instructive part is the assertion written to guard exactly this. #391 did not cite the branch, it
// *asserted* it: `ByExpr` was read off the decoded module so that "this vector takes the
// non-expression branch" would be checked rather than trusted. It passed, because the decode it read
// was our own parser's, and our own parser was wrong in precisely the shape the account described. **A
// decode of our own text checks the front end against itself** — the authority for what wire form a
// wat spelling takes is the reference's parser and encoder, never a round trip through the two
// components under test. So the assertion below is retained, with its verdict inverted to what the
// reference says, and a second row added for the index branch: an inverted-but-still-self-referential
// assertion is worth keeping only because the *reference* is now what set its direction.
//
// Both branches keep a refusal row, because #391's fix and Rule D's `ref.func` resolution inside
// `checkConst` (ADR 0037) are two different code paths reaching one verdict, and the corpus reaches
// only the second of them.
//
// # It is not an instruction rule, which is why it is not part of slice 10's criterion
//
// The code-section walk never visits an element segment: this runs in `modulePre`, where the reference
// reaches it. Both rows live in files named for `call_indirect`, which is what made them look like a
// tail-call slice's business — *a vector's file is not its stratum.* The default lane's whole +2 for
// slice 10's PR comes from these two, since the exception-handling family is gated off there.
//
// # The accept rows are the half the board cannot score
//
// `(elem 0)` in a module whose only function is *imported* is valid, and a rule counting `len(m.Funcs)`
// alone refuses it — the function index space is imports-then-definitions, and no `assert_invalid`
// vector can catch a rule that refuses a valid module (contract §9 G-3).
func TestElemSegmentFunctionIndicesResolve(t *testing.T) {
	// Which spelling reaches which branch — the direction of both rows is set by the reference, not by
	// what our decoder happens to produce (see the header). Neither branch may be left unread: the
	// corpus reaches only the expression one, so the index row is the whole readership of the other.
	for _, c := range []struct {
		name       string
		wat        string
		wantByExpr bool
		why        string
	}{
		{
			name:       "the corpus spelling takes the expression branch",
			wat:        `(module (func) (table funcref (elem 0)))`,
			wantByExpr: true,
			why: "the inline-table sugar takes the table's `funcref` = `(Null, FuncHT)` down the " +
				"element arm (parser.mly:1215), and `is_elem_kind` is false for it " +
				"(encode.ml:1044-1046), so the encoder reaches flag 4. This is the row #391 asserted " +
				"in the opposite direction and grave #401 agreed with",
		},
		{
			name:       "the bare-offset sugar takes the index branch",
			wat:        `(module (func) (table 1 funcref) (elem (i32.const 0) 0))`,
			wantByExpr: false,
			why: "this is the arm that really does say `let rt = (NoNull, FuncHT) in` " +
				"(parser.mly:1175-1179) — the citation grave #401 quoted onto the wrong production. " +
				"`(ref func)` is `is_elem_kind`, so this one is flag 0",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := decodedModule(t, c.wat, nil)
			if len(m.Elems) != 1 {
				t.Fatalf("decoded %d element segment(s), want 1 — the rest of this row asserts nothing "+
					"about a module with no segment in it", len(m.Elems))
			}
			if m.Elems[0].ByExpr != c.wantByExpr {
				t.Errorf("%s decoded with ByExpr=%v, want %v\n%s", c.wat, m.Elems[0].ByExpr,
					c.wantByExpr, c.why)
			}
			if got := len(m.Elems[0].Funcs); (got == 1) == c.wantByExpr {
				t.Errorf("the segment carries %d entry in `Funcs`, which does not match ByExpr=%v — "+
					"the index vector is where the index form puts its functions and the expression "+
					"form leaves it empty, so a form claim nothing else reads is not a form claim",
					got, c.wantByExpr)
			}
		})
	}

	for _, c := range []struct {
		name  string
		wat   string
		msg   string
		valid bool
	}{
		{
			// Both admissions, verbatim: no functions at all, two `ref.func 0` initialisers. Not the
			// index form — see the header — so what refuses this is Rule D inside `checkConst`, and
			// #391's own lookup never runs on it.
			name:  "the corpus shape names a function the module does not have",
			wat:   `(module (table funcref (elem 0 0)))`,
			msg:   "unknown function 0 (0 in scope)",
			valid: false,
		},
		{
			// The explicit expression spelling, reaching the same branch by a different route through
			// the parser: `(item …)` rather than the inline sugar's synthesised `[RefNull ht]`.
			name:  "the expression form names a function the module does not have",
			wat:   `(module (elem funcref (item (ref.func 0))))`,
			msg:   "unknown function 0 (0 in scope)",
			valid: false,
		},
		{
			// The index branch's only refusal row, and the sole reader of `elemFuncsInScope`: neuter
			// that loop and this is the one row that fails, while the two corpus-shaped rows above go
			// on refusing through `checkConst`. Both lanes of the board stay green with the rule dead
			// — measured, which is what makes "the corpus does not reach this branch" a finding rather
			// than an inference from the header.
			name:  "the index form names a function the module does not have",
			wat:   `(module (elem func 0))`,
			msg:   "unknown function 0 (0 in scope)",
			valid: false,
		},
		{
			name:  "one past the end",
			wat:   `(module (func) (table funcref (elem 0 1)))`,
			msg:   "unknown function 1 (1 in scope)",
			valid: false,
		},
		{
			name:  "a defined function",
			wat:   `(module (func) (table funcref (elem 0)))`,
			valid: true,
		},
		{
			// The accept row that separates `ImportedFuncs() + len(m.Funcs)` from `len(m.Funcs)`.
			name:  "an imported function, which occupies index 0",
			wat:   `(module (import "m" "f" (func)) (table funcref (elem 0)))`,
			valid: true,
		},
		{
			// And the same separation on the index branch, which is a different count in different
			// code: the row above reaches it through `checkConst`. One accept row per branch, for the
			// same reason there is one refusal row per branch.
			name:  "an imported function, through the index form",
			wat:   `(module (import "m" "f" (func)) (elem func 0))`,
			valid: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, nil)
			switch {
			case c.valid && err != nil:
				t.Errorf("valid module refused: %v\n%s\nThe function index space is "+
					"imports-then-definitions, and no `assert_invalid` vector can see a rule that "+
					"refuses a valid module.", err, c.wat)
			case !c.valid && err == nil:
				t.Errorf("invalid module accepted\n%s\nThis is #391: the segment's `ref.func` "+
					"initialisers were never resolved, so the module reached the interpreter naming a "+
					"function that does not exist.", c.wat)
			case !c.valid && !errors.Is(err, ErrUnknownFunc):
				t.Errorf("refused with the wrong sentinel: want %v, got %v", ErrUnknownFunc, err)
			case !c.valid && !strings.Contains(err.Error(), c.msg):
				t.Errorf("refused, but not with %q: %v\nThe corpus matches by substring (0003), so the "+
					"category and the index are load-bearing text.", c.msg, err)
			}
		})
	}
}
