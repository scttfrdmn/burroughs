package memarggen

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

func refSource(tb testing.TB) string {
	tb.Helper()
	return testenv.RequireSpecRef(tb, testenv.RefLexerMLL)
}

func extract(t *testing.T) *Table {
	t.Helper()
	tab, err := Extract(refSource(t), "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return tab
}

// TestExtractMatchesMeasuredShape pins the exact counts, **per token kind and in total**, beside
// the floors in Floors.
//
// Two instruments, not two settings of one. A floor answers "did the extraction happen at all" —
// a moved file, a renamed rule — and it demonstrably cannot answer "did it get everything":
// `Floors.Lexer` at 350 stayed green through #105's 411-of-436. The exact count is knowable here,
// so it is pinned, and the floor stays for the catastrophic case a diff would report to nobody.
//
// Per-kind, not one total, for the reason Floors gives: an aggregate absorbs an empty partition
// into a full one. The lane kinds are 4 rows each, so a total of 45 tolerates all three going to
// zero if LOAD grows by 12.
//
// The numbers were measured by running the extractor and printing every row — 45 rows, six kinds
// — and cross-checked against `grep -c 'opt a ' lexer.mll`, which finds 45 matches in the keyword
// block. Two ways of counting, because the count read off the extractor's own output is the one
// this test exists to falsify.
func TestExtractMatchesMeasuredShape(t *testing.T) {
	tab := extract(t)

	if len(tab.Rows) != 45 {
		t.Errorf("rows: got %d, want 45", len(tab.Rows))
	}
	byKind := map[string]int{}
	for _, r := range tab.Rows {
		byKind[r.Kind]++
	}
	for kind, want := range map[string]int{
		"LOAD":           19,
		"STORE":          4,
		"VEC_LOAD":       13,
		"VEC_STORE":      1,
		"VEC_LOAD_LANE":  4,
		"VEC_STORE_LANE": 4,
	} {
		if byKind[kind] != want {
			t.Errorf("%s rows: got %d, want %d", kind, byKind[kind], want)
		}
	}
	// Every kind the extractor found must be one it was looking for. The converse is the map
	// above; this direction catches a kind admitted by memargKinds and never measured.
	for kind := range byKind {
		if !memargKinds[kind] {
			t.Errorf("kind %q appears in the table and is not in memargKinds — impossible via "+
				"Extract, so the discriminator and the reader disagree", kind)
		}
	}
	t.Logf("%d rows: %v", len(tab.Rows), byKind)
}

// TestEveryFloorIsBelowItsMeasuredCount is the unasserted-distance control.
//
// *A bound far from what it bounds runs, agrees, and says nothing.* A floor above its measured
// count is a permanently red gate; a floor at zero is decoration. Both are caught by asserting
// the relationship rather than the values, which is also what keeps the two sets of numbers above
// from drifting into disagreement — the exact counts and the floors are the same measurement at
// two strictnesses, and nothing else says so.
//
// **It reads rows through extractRows, not Extract, and that is not a convenience.** Extract
// applies the floors, so with a floor set above its partition it returns ErrVacuous and this test
// died in its helper — failing on the extraction while the `floor > got` branch it exists for was
// never reached. Right verdict, dead assertion; a stillborn branch is invisible from a board
// because a control that cannot fire and one that never had to look identical when green. A
// control cannot measure a count through a gate that refuses on that count.
func TestEveryFloorIsBelowItsMeasuredCount(t *testing.T) {
	tab, err := extractRows(refSource(t), "test")
	if err != nil {
		t.Fatalf("extractRows: %v", err)
	}
	byKind := map[string]int{}
	for _, r := range tab.Rows {
		byKind[r.Kind]++
	}

	if len(Floors) == 0 {
		t.Fatal("no floors declared: a vacuity check over an empty set of floors is vacuous " +
			"itself, which is the defect one level up")
	}
	for kind, floor := range Floors {
		got := byKind[kind]
		switch {
		case floor <= 0:
			t.Errorf("%s floor is %d: a non-positive floor cannot fire and is decoration", kind, floor)
		case floor > got:
			t.Errorf("%s floor %d exceeds the measured %d: the gate is permanently red", kind, floor, got)
		}
	}
	if FloorTotal <= 0 || FloorTotal > len(tab.Rows) {
		t.Errorf("FloorTotal %d against %d measured rows", FloorTotal, len(tab.Rows))
	}
	// Every kind in the table must have a floor, or a partition can empty unwatched — the same
	// hole as one aggregate floor, reached by omission instead of by design.
	for kind := range byKind {
		if _, ok := Floors[kind]; !ok {
			t.Errorf("kind %q has rows and no floor: an unfloored partition can go to zero and "+
				"only the total would notice", kind)
		}
	}
}

// TestNarrowingStoresAreTaggedLOAD asserts an upstream oddity **as an oddity**, so it cannot be
// quietly normalized.
//
// `i32.store8`, `i32.store16`, `i64.store8`, `i64.store16` and `i64.store32` return **LOAD**, not
// STORE (lexer.mll:265-269). That is the reference as written and it is invisible in the
// reference's own behaviour, because the four scalar memarg arms of parser.mly (:592-598) parse
// identical immediates — the kind only selects which arm runs.
//
// Pinned here because the natural reading of a table row saying `i64.store32 … LOAD` is "the
// extractor mis-tagged it", and the natural repair is to correct it. This test says the authority
// says LOAD, so a future reader who "fixes" the table breaks a control instead of silently editing
// evidence. If upstream ever *does* fix it, this fails with a diff, which is the right outcome:
// the table is a transcription, and a transcription's job is to change when its source does.
func TestNarrowingStoresAreTaggedLOAD(t *testing.T) {
	tab := extract(t)
	byMnemonic := map[string]Row{}
	for _, r := range tab.Rows {
		byMnemonic[r.Mnemonic] = r
	}

	for _, m := range []string{"i32.store8", "i32.store16", "i64.store8", "i64.store16", "i64.store32"} {
		r, ok := byMnemonic[m]
		if !ok {
			t.Errorf("%s is absent from the table: it takes a memarg and needs a default", m)
			continue
		}
		if r.Kind != "LOAD" {
			t.Errorf("%s is tagged %s, want LOAD — the reference tags the narrowing stores LOAD "+
				"(lexer.mll:%d) and this table transcribes rather than corrects it; if upstream "+
				"changed, re-measure", m, r.Kind, r.Line)
		}
	}
	// And the wide stores are STORE, so this is a claim about the *narrowing* ones rather than
	// about stores generally — a partition checked against the partition, not against its labels.
	for _, m := range []string{"i32.store", "i64.store", "f32.store", "f64.store"} {
		if r := byMnemonic[m]; r.Kind != "STORE" {
			t.Errorf("%s is tagged %q, want STORE", m, r.Kind)
		}
	}
}

// TestAlignmentsAreTheAccessWidth is the accept-direction control on the *values*, and it is the
// one that matters.
//
// Everything above counts rows. A table with 45 rows, six kinds, every floor cleared and every
// oddity pinned can still hold `i64.load → 2`, and **nothing on the board would say so**: the
// flags byte is a legal alignment, the decoder accepts it, and validation only rejects
// *over*-alignment, so an under-aligned default produces a module that decodes, validates, and
// differs from its source. That is contract §9 G-3 exactly.
//
// So the values are checked against the semantics the reference is encoding: the log2 of the
// number of bytes the instruction accesses. Nine rows, one per distinct width the table uses,
// chosen so a systematic error (an off-by-one, a byte-count-instead-of-exponent) fails several.
// The widths are the spec's, not the extractor's — `i64.load32_u` reads 4 bytes and so is 2,
// `v128.load8x8_u` reads 8 and so is 3, `v128.load` reads 16 and so is 4.
//
// **Nine rows and not 45, deliberately, and the distinction is worth stating**: this is a check
// against an *independent* derivation of the same fact, so every row costs a hand-derivation and
// a hand-derivation is precisely what this package exists to avoid. The whole-table guarantee
// comes from the extraction being mechanical; this is the sample that says the mechanism reads
// the field it claims to. Widening it to 45 would reintroduce the transcription risk 0007 is
// about — seven wrong citations in twelve items — to certify against it.
func TestAlignmentsAreTheAccessWidth(t *testing.T) {
	tab := extract(t)
	byMnemonic := map[string]Row{}
	for _, r := range tab.Rows {
		byMnemonic[r.Mnemonic] = r
	}

	for _, c := range []struct {
		mnemonic string
		bytes    int // what the instruction accesses, from the spec
		align    int // log2 of it, which is what the table must hold
	}{
		{"i32.load8_u", 1, 0},
		{"i32.load16_u", 2, 1},
		{"i32.load", 4, 2},
		{"i64.load", 8, 3},
		{"i64.load32_s", 4, 2},     // a 64-bit result from a 4-byte access: the width is the access
		{"f64.store", 8, 3},        // and a store's width is the value it writes
		{"v128.load", 16, 4},       // the only 4s in the table, with v128.store
		{"v128.load8_splat", 1, 0}, // a splat reads one lane, not the vector
		{"v128.load8x8_u", 8, 3},   // eight bytes widened to eight lanes: 8, not 16
	} {
		r, ok := byMnemonic[c.mnemonic]
		if !ok {
			t.Errorf("%s absent from the table", c.mnemonic)
			continue
		}
		if 1<<c.align != c.bytes {
			t.Fatalf("this case is self-inconsistent: %s claims %d bytes and exponent %d — the "+
				"test's own arithmetic is wrong, which would make a green here meaningless",
				c.mnemonic, c.bytes, c.align)
		}
		if r.Align != c.align {
			t.Errorf("%s: table says align=%d (%d bytes), the spec's access is %d bytes (align=%d) "+
				"— lexer.mll:%d", c.mnemonic, r.Align, 1<<r.Align, c.bytes, c.align, r.Line)
		}
	}
}

// TestAMissingOptAlignIsAnErrorNotASkip falsifies the reader's loud half.
//
// A memarg arm that lost its `opt a N` must stop the extraction. Skipping it would produce a table
// short one row, and a mnemonic missing from `naturalAlign` is an instruction the encoder cannot
// write — silent where the row's *absence* is the whole failure, and 44 clears every floor here.
func TestAMissingOptAlignIsAnErrorNotASkip(t *testing.T) {
	src := refSource(t)

	// The mutation, and its anchor asserted first: *a mutation that did not apply leaves the test
	// asserting the unmodified reader's behaviour, which passes.* A skip here would not decline to
	// answer, it would answer the wrong question and score it green.
	const anchor = `| "i32.load" -> LOAD (fun x a o -> i32_load x (opt a 2) o)`
	if !strings.Contains(src, anchor) {
		t.Fatalf("mutation did not apply: anchor %q changed upstream, so this control is asserting "+
			"the unmodified reader; re-point the injection", anchor)
	}
	broken := strings.Replace(src, anchor,
		`| "i32.load" -> LOAD (fun x a o -> i32_load x a o)`, 1)

	if _, err := Extract(broken, "test"); !errors.Is(err, ErrUnrecognized) {
		t.Errorf("a LOAD arm with no `opt a N` gave err=%v, want ErrUnrecognized — an omission "+
			"here is a mnemonic the encoder silently cannot write", err)
	}
}

// TestAnEmptyBlockIsLoud is the vacuity control.
//
// A moved file or a renamed rule must error rather than yield zero rows: a drift check comparing
// an empty generated table against an empty committed table agrees perfectly, which is the defect
// class every floor in this repo is downstream of.
func TestAnEmptyBlockIsLoud(t *testing.T) {
	if _, err := Extract("let foo = 1\nlet bar = 2\n", "test"); !errors.Is(err, ErrVacuous) {
		t.Errorf("a source with no keyword block gave err=%v, want ErrVacuous", err)
	}

	// And a block that is *present* but stripped of memarg arms: the locator succeeds, the reader
	// finds nothing, and the floors are what must fire. This is the case ErrUnrecognized cannot
	// see, which is why the floors exist beside it.
	src := "  | keyword as s\n  { match s with\n  | \"nop\" -> NOP\n  | _ -> unknown lexbuf\n"
	if _, err := Extract(src, "test"); !errors.Is(err, ErrVacuous) {
		t.Errorf("a block with no memarg arms gave err=%v, want ErrVacuous — zero rows and no "+
			"error is the empty-table agreement", err)
	}
}

// TestEveryMnemonicIsAKeyword is the crossover control: this table's mnemonics must be keywords the
// reference actually lexes.
//
// **Not a tautology, and the reason is that the two readers ask different questions of the same
// substrate.** Both call `mllex`, so they agree about which lines are arms — that is no longer a
// claim. What is a claim is that every row this package *kept* names a keyword `keywordgen` also
// kept, at the same line, with the token kind the two independently read agreeing. A row whose kind
// this package read as LOAD and keywordgen read as something else would mean one of the two regexps
// is picking up a different identifier from the same body, which is a real and findable defect.
//
// The line is compared too, because the line is what a generated row cites: a row pointing at a
// continuation instead of at its head is a citation that does not resolve.
func TestEveryMnemonicIsAKeyword(t *testing.T) {
	src := refSource(t)
	tab := extract(t)

	kt, err := keywordgen.Extract(src, "test")
	if err != nil {
		t.Fatalf("keywordgen.Extract: %v", err)
	}
	kws := map[string]keywordgen.Arm{}
	for _, a := range kt.Arms {
		kws[a.Keyword] = a
	}
	if len(kws) == 0 || len(tab.Rows) == 0 {
		t.Fatalf("keywordgen found %d arms, memarggen %d rows — an agreement against an empty set "+
			"is perfect and asserts nothing", len(kws), len(tab.Rows))
	}

	for _, r := range tab.Rows {
		a, ok := kws[r.Mnemonic]
		switch {
		case !ok:
			t.Errorf("%q has a natural alignment and is not a keyword: the encoder would default "+
				"an alignment for a mnemonic the lexer does not recognize (lexer.mll:%d)",
				r.Mnemonic, r.Line)
		case a.Line != r.Line:
			t.Errorf("%q: memarggen reads it at lexer.mll:%d, keywordgen at :%d", r.Mnemonic, r.Line, a.Line)
		case string(a.Kind) != r.Kind:
			t.Errorf("%q: memarggen read kind %q, keywordgen read %q from the same body — the two "+
				"regexps are picking up different identifiers", r.Mnemonic, r.Kind, a.Kind)
		}
	}
	t.Logf("%d memarg rows, all keywords, kinds and lines agreeing with keywordgen", len(tab.Rows))
}

// TestEmittedRowsAreSortedByMnemonic pins the emitted order.
//
// **This test replaces a stillborn one, and the replacement is the finding.** What stood here
// asserted that five Extract-then-Emit runs agree, on the stated premise that "the map iteration
// order is the specific hazard, and sorting in Extract is what removes it". Both halves were
// wrong. There is no map between `mllex.Arms` and `Emit` — the scan walks lines in order and Emit
// ranges a slice — so the output is deterministic *with the sort removed*, which is how this was
// found: `slices.SortFunc` was replaced with `slices.Reverse` and the suite stayed green. Budget
// for the falsification passing; that outcome is the control being stillborn, and it is the most
// valuable result the exercise has.
//
// So the property is asserted directly instead of inferred from repetition. It is not a
// determinism claim — determinism here is structural and needs no test — it is that the committed
// file is ordered by mnemonic, which is what makes its diff localize when upstream adds a row.
// A reversal, a removal, or a re-key of the sort now fails.
func TestEmittedRowsAreSortedByMnemonic(t *testing.T) {
	tab := extract(t)

	mnemonics := make([]string, len(tab.Rows))
	for i, r := range tab.Rows {
		mnemonics[i] = r.Mnemonic
	}
	if !slices.IsSorted(mnemonics) {
		t.Errorf("rows are not sorted by mnemonic: %v", mnemonics)
	}

	// And the emitted *text* carries that order, because the order in the struct is only useful
	// if Emit preserves it — a control on the slice alone would pass an Emit that re-sorted.
	code, err := tab.Emit()
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	var found []string
	for _, m := range mnemonics {
		i := strings.Index(code, `"`+m+`":`)
		if i < 0 {
			t.Errorf("%q is a row and does not appear in the emitted table", m)
			continue
		}
		found = append(found, m)
		if len(found) > 1 && i < strings.Index(code, `"`+found[len(found)-2]+`":`) {
			t.Errorf("%q is emitted before %q, against the row order", m, found[len(found)-2])
		}
	}
	// The vacuity guard: an emitted table with no rows satisfies every ordering claim above.
	if len(found) < FloorTotal {
		t.Errorf("only %d of %d rows found in the emitted text (floor %d): an ordering assertion "+
			"over an empty sequence is perfectly satisfied", len(found), len(tab.Rows), FloorTotal)
	}
}
