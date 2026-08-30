package memarggen

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

func refSource(tb testing.TB) string {
	tb.Helper()
	return testenv.RequireSpecRef(tb, testenv.RefLexerMLL)
}

// requireRef asserts the *pin set* is on the box, not just the core lexer.
//
// **A skip is not a verdict**, and after composition the population this package measures spans
// two fetched corpora: a box holding only `third_party/spec` would let every composed control skip
// while the aggregate ones passed on the core's 45 rows alone. So the domain is derived from
// `RefPins` rather than listed, and a pin set that stopped offering a second lexer is a fatal
// rather than a skip — that is the shape a floor cannot see (see BuildFromPins).
func requireRef(tb testing.TB) {
	tb.Helper()
	seen := 0
	for _, pin := range testenv.RefPins() {
		path, ok := keywordgen.LexerFor(pin)
		if !ok {
			continue
		}
		testenv.RequireSpecRef(tb, path)
		seen++
	}
	if seen < 2 {
		tb.Fatalf("only %d pin licenses a text lexer: every composed assertion below would hold "+
			"of the core's 45 rows alone", seen)
	}
}

// extract builds the composed table, which is the one the committed file is generated from.
func extract(t *testing.T) *Table {
	t.Helper()
	requireRef(t)
	tab, err := BuildFromPins()
	if err != nil {
		t.Fatalf("BuildFromPins: %v", err)
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
// The numbers were measured by running the extractor and printing every row — 111 rows, twelve
// kinds — and cross-checked against `grep -c 'opt a '`, which finds 45 matches in the core lexer
// and 111 in the threads one, of which base-wins keeps 66. Two ways of counting, because the count
// read off the extractor's own output is the one this test exists to falsify.
//
// **And per authority as well as per kind, which is the composition's own blind spot.** The
// aggregate per-kind figures are satisfied whichever pin supplied them, so a table that read the
// atomic mnemonics out of the *core* lexer would agree with every number in the first map below.
// The second map is what says which file each partition came from, and it is the only instrument
// here that can see `MEMORY_ATOMIC_NOTIFY`'s single row move pins.
func TestExtractMatchesMeasuredShape(t *testing.T) {
	tab := extract(t)

	if len(tab.Rows) != 111 {
		t.Errorf("rows: got %d, want 111", len(tab.Rows))
	}
	byKind := map[string]int{}
	for _, r := range tab.Rows {
		byKind[r.Kind]++
	}
	for kind, want := range map[string]int{
		"LOAD":                 19,
		"STORE":                4,
		"VEC_LOAD":             13,
		"VEC_STORE":            1,
		"VEC_LOAD_LANE":        4,
		"VEC_STORE_LANE":       4,
		"ATOMIC_LOAD":          2,
		"ATOMIC_STORE":         12,
		"ATOMIC_RMW":           42,
		"ATOMIC_RMW_CMPXCHG":   7,
		"MEMORY_ATOMIC_WAIT":   2,
		"MEMORY_ATOMIC_NOTIFY": 1,
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
				"ExtractFrom, so the discriminator and the reader disagree", kind)
		}
	}

	// Per authority, keyed by the path so a re-ordered pin set fails rather than passing on the
	// other pin's numbers.
	want := map[string]map[string]int{
		testenv.RefLexerMLL: {
			"LOAD": 19, "STORE": 4, "VEC_LOAD": 13, "VEC_STORE": 1,
			"VEC_LOAD_LANE": 4, "VEC_STORE_LANE": 4,
		},
		testenv.ThreadsRefLexerMLL: {
			"ATOMIC_LOAD": 2, "ATOMIC_STORE": 12, "ATOMIC_RMW": 42,
			"ATOMIC_RMW_CMPXCHG": 7, "MEMORY_ATOMIC_WAIT": 2, "MEMORY_ATOMIC_NOTIFY": 1,
		},
	}
	if len(tab.Sources) != len(want) {
		t.Fatalf("%d sources, want %d: %v", len(tab.Sources), len(want), tab.Sources)
	}
	for _, s := range tab.Sources {
		w, ok := want[s.LexerPath]
		if !ok {
			t.Errorf("unexpected authority %q", s.LexerPath)
			continue
		}
		if len(s.ByKind) != len(w) {
			t.Errorf("%s contributed %d kinds, want %d: %v", s.LexerPath, len(s.ByKind), len(w), s.ByKind)
		}
		for kind, n := range w {
			if s.ByKind[kind] != n {
				t.Errorf("%s: contributed %d %s rows, want %d", s.LexerPath, s.ByKind[kind], kind, n)
			}
		}
		// The threads lexer holds core's 45 scalar mnemonics too, at its own pre-multi-memory
		// spellings, and base-wins must take *none* of them: a `LOAD` row here would be a core
		// mnemonic citing the overlay's line numbering.
		if s.LexerPath == testenv.ThreadsRefLexerMLL && s.ByKind["LOAD"] != 0 {
			t.Errorf("%s contributed %d LOAD rows: base-wins is not holding, so a core mnemonic "+
				"cites the overlay's file", s.LexerPath, s.ByKind["LOAD"])
		}
	}
	t.Logf("%d rows: %v", len(tab.Rows), byKind)
	for _, s := range tab.Sources {
		t.Logf("%s at %s: %d rows %v", s.LexerPath, s.SHA, s.Total, s.ByKind)
	}
}

// TestEveryRowCitesItsOwnFile is grave #529's control in this table.
//
// Two files named `lexer.mll` are in the tree with unrelated numberings, so a row's line number is
// meaningless without its file — and the failure is not a crash but a citation that resolves
// against the *wrong* authority and looks right. The row-level assertion is that From is one of the
// two licensed paths' tags; the population-level one is that both tags actually appear, because a
// table whose every row cited the core lexer would satisfy the first check completely.
func TestEveryRowCitesItsOwnFile(t *testing.T) {
	tab := extract(t)

	tags := map[string]bool{}
	for _, s := range tab.Sources {
		tags[gen.SourceTag(s.LexerPath)] = true
	}
	if len(tags) < 2 {
		t.Fatalf("%d distinct source tags: a citation cannot distinguish two files it renders the "+
			"same way", len(tags))
	}
	seen := map[string]int{}
	for _, r := range tab.Rows {
		if !tags[r.From] {
			t.Errorf("%q cites %q:%d, which is no authority in this table", r.Mnemonic, r.From, r.Line)
			continue
		}
		seen[r.From]++
	}
	for tag := range tags {
		if seen[tag] == 0 {
			t.Errorf("no row cites %s: an authority in the header that named nothing in the table", tag)
		}
	}
	t.Logf("citations per authority: %v", seen)
}

// TestEmitRejectsARowWithNoFile falsifies that guard, which is the half of grave #529 a resolving
// citation cannot witness: `From` empty renders a line number against no file at all.
func TestEmitRejectsARowWithNoFile(t *testing.T) {
	tab := extract(t)
	if len(tab.Rows) == 0 {
		t.Fatal("no rows: the mutation below would have nothing to blank")
	}
	tab.Rows[0].From = ""
	if _, err := tab.Emit(); err == nil {
		t.Error("Emit accepted a row citing a line of no file; the emitted comment would read " +
			"\":195\" and resolve against whichever lexer.mll the reader opens (grave #529)")
	}
}

// TestEmitRefusesASourcelessTable is the header's own vacuity guard: a table with no Sources emits
// a file claiming no authority, which is the one property 0007's condition 3 forbids.
func TestEmitRefusesASourcelessTable(t *testing.T) {
	tab := extract(t)
	tab.Sources = nil
	if _, err := tab.Emit(); err == nil {
		t.Error("Emit accepted a table with no sources; the generated header would name no " +
			"authority and no revision")
	}
}

// TestEveryFloorIsBelowItsMeasuredCount is the unasserted-distance control.
//
// *A bound far from what it bounds runs, agrees, and says nothing.* A floor above its measured
// count is a permanently red gate; a floor at zero is decoration. Both are caught by asserting
// the relationship rather than the values, which is also what keeps the two sets of numbers above
// from drifting into disagreement — the exact counts and the floors are the same measurement at
// two strictnesses, and nothing else says so.
//
// **It reads rows through composeRows, not ExtractFrom, and that is not a convenience.**
// ExtractFrom applies the floors, so with a floor set above its partition it returns ErrVacuous and
// this test died in its helper — failing on the extraction while the `floor > got` branch it exists
// for was never reached. Right verdict, dead assertion; a stillborn branch is invisible from a board
// because a control that cannot fire and one that never had to look identical when green. A
// control cannot measure a count through a gate that refuses on that count.
func TestEveryFloorIsBelowItsMeasuredCount(t *testing.T) {
	requireRef(t)
	auths, err := pinAuthorities()
	if err != nil {
		t.Fatalf("pinAuthorities: %v", err)
	}
	tab, err := composeRows(auths)
	if err != nil {
		t.Fatalf("composeRows: %v", err)
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
	// And the other direction: a kind the discriminator admits and the table never sees is a dead
	// entry in memargKinds, which is not merely tidy — memargKinds is what the reverse check tests
	// against, so a kind misspelled *into* it silently widens the set of arms that may carry an
	// `opt a N` without being read.
	for kind := range memargKinds {
		if byKind[kind] == 0 {
			t.Errorf("kind %q is declared in memargKinds and matched no arm: the discriminator "+
				"admits a kind no authority uses, which is the reverse check's own blind spot", kind)
		}
		if _, ok := Floors[kind]; !ok {
			t.Errorf("kind %q is declared and unfloored", kind)
		}
	}

	// FloorPerAuthority is the same distance claim over the other question — see its doc. Measured
	// on rows *read*, which is what that floor bounds, so it is re-derived here rather than taken
	// from the composed table (where base-wins has already removed 45 of the overlay's).
	if FloorPerAuthority <= 0 {
		t.Errorf("FloorPerAuthority is %d: a non-positive floor cannot fire", FloorPerAuthority)
	}
	for _, a := range auths {
		rows, err := armRows(a)
		if err != nil {
			t.Fatalf("armRows(%s): %v", a.LexerPath, err)
		}
		if FloorPerAuthority > len(rows) {
			t.Errorf("FloorPerAuthority %d exceeds the %d arms read from %s: the gate is "+
				"permanently red for that pin", FloorPerAuthority, len(rows), a.LexerPath)
		}
		t.Logf("%s: %d memarg arms read, floor %d", a.LexerPath, len(rows), FloorPerAuthority)
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
	// The rows are the core pin's, and asserting that is what makes this test about the file its
	// prose cites: the threads lexer holds `i32.store8` too, so a base-wins failure would leave
	// these rows tagged LOAD and citing the wrong authority — passing every check above.
	for _, m := range []string{"i32.store8", "i32.store", "i64.store32"} {
		if got := byMnemonic[m].From; got != gen.SourceTag(testenv.RefLexerMLL) {
			t.Errorf("%s cites %q, want the core lexer: the oddity this test pins is that file's",
				m, got)
		}
	}
}

// TestNarrowingAtomicLoadsAreTaggedATOMIC_STORE is the same oddity mirrored, in the overlay.
//
// The threads lexer tags its five narrowing atomic *loads* — `i32.atomic.load8_u`,
// `i32.atomic.load16_u`, `i64.atomic.load8_u`, `i64.atomic.load16_u`, `i64.atomic.load32_u` —
// **ATOMIC_STORE**, leaving ATOMIC_LOAD with exactly the two wide loads. Invisible in the
// reference's own behaviour for the reason the core's mistagging is: `spec-threads/parser.mly:456`
// and `:457` are the same production twice (`offset_opt align_opt`), so the kind only selects which
// identical arm runs.
//
// Pinned as an oddity for the same reason as its core twin, and pinned *separately* rather than
// folded into that test: the two are different files at different revisions, and a single test
// asserting "narrowing accesses are mistagged" would let one upstream fix hide behind the other.
// If upstream corrects either, that test fails with a diff and this table is re-measured — which is
// the right outcome, a transcription's job being to change when its source does.
func TestNarrowingAtomicLoadsAreTaggedATOMIC_STORE(t *testing.T) {
	tab := extract(t)
	byMnemonic := map[string]Row{}
	for _, r := range tab.Rows {
		byMnemonic[r.Mnemonic] = r
	}

	for _, m := range []string{
		"i32.atomic.load8_u", "i32.atomic.load16_u",
		"i64.atomic.load8_u", "i64.atomic.load16_u", "i64.atomic.load32_u",
	} {
		r, ok := byMnemonic[m]
		if !ok {
			t.Errorf("%s is absent from the table: an atomic access with no natural alignment is "+
				"an instruction the encoder cannot write, natural being the only legal one", m)
			continue
		}
		if r.Kind != "ATOMIC_STORE" {
			t.Errorf("%s is tagged %s, want ATOMIC_STORE — the threads reference tags the narrowing "+
				"atomic loads ATOMIC_STORE (%s:%d) and this table transcribes rather than corrects "+
				"it; if upstream changed, re-measure", m, r.Kind, r.From, r.Line)
		}
	}
	// And the two wide atomic loads are ATOMIC_LOAD, so the claim is about the *narrowing* ones.
	// Two is the whole partition, which is why ATOMIC_LOAD's floor is 1 and not 5.
	for _, m := range []string{"i32.atomic.load", "i64.atomic.load"} {
		if r := byMnemonic[m]; r.Kind != "ATOMIC_LOAD" {
			t.Errorf("%s is tagged %q, want ATOMIC_LOAD", m, r.Kind)
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

		// The overlay's, and for these the default is also the only legal alignment — an atomic
		// access must be naturally aligned, so a wrong row here is a module the validator rejects
		// rather than one it merely runs slowly. Six rows, one per distinct width the atomic
		// families use, spread across five of the six kinds.
		{"i32.atomic.load", 4, 2},
		{"i64.atomic.load", 8, 3},
		{"i32.atomic.load8_u", 1, 0},     // tagged ATOMIC_STORE upstream; the width is still the access
		{"i32.atomic.rmw16.and_u", 2, 1}, // rmw16 reads 2 bytes into an i32
		{"i64.atomic.rmw32.cmpxchg_u", 4, 2},
		{"memory.atomic.wait64", 8, 3}, // the waited-on value's width, not the address's
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

// TestAnUndeclaredKindWithAnAlignmentIsAnErrorNotASkip falsifies the reader's *quiet* half, which
// is the one composition created.
//
// The forward check above is loud by construction: it fires on a kind already declared. The
// complement is the one that was silently wrong for the whole of this package's single-pin life —
// an arm stating `opt a N` under a kind nothing declared hit the `!memargKinds[kind]` branch and was
// dropped as "takes no memarg", which is the reader contradicting the line it just read. That is
// precisely how the 66 atomic mnemonics would have vanished from a composed table: 111 rows in the
// overlay's lexer, 45 read, 66 skipped in silence, and every floor and count in this file green on
// the core's contribution alone.
//
// The mutation removes a kind from the discriminator rather than editing the reference, because the
// defect being falsified is the *discriminator's* incompleteness — which is what a new proposal pin
// presents. **Its floor goes with it, and choosing which kind took a measurement.** The first draft
// undeclared ATOMIC_RMW, 42 rows, on the reasoning that the largest partition made the most
// expensive silent loss; that mutation was then caught by ATOMIC_RMW's own *floor* rather than by the
// reverse check, so the control died when the mechanism was neutered but for the wrong reason — a
// pass that would have survived deleting the thing it exists to test. A new pin's kind has no floor
// either, so undeclaring one and leaving its floor in place is not the shape being modelled.
//
// So the subject is MEMORY_ATOMIC_NOTIFY, one row, kind and floor both removed — the case where
// nothing else in this file can see the loss. The second half asserts that: the floors are run over
// the table with those rows taken out and must **pass**, which is what makes the reverse check the
// only instrument covering this and keeps the claim honest if someone later tightens FloorTotal.
func TestAnUndeclaredKindWithAnAlignmentIsAnErrorNotASkip(t *testing.T) {
	requireRef(t)
	const kind = "MEMORY_ATOMIC_NOTIFY"
	if !memargKinds[kind] {
		t.Fatalf("%s is not declared, so removing it below changes nothing and this control is "+
			"asserting the unmodified reader", kind)
	}
	if _, floored := Floors[kind]; !floored {
		t.Fatalf("%s has no floor, so the mutation below does not model a new pin's kind", kind)
	}

	// First: the loss this mutation causes is invisible to every other instrument here. Measured
	// rather than argued — a claim that a control is load-bearing is a claim about what the other
	// controls miss, and that is checkable.
	full := extract(t)
	reduced := &Table{Sources: full.Sources}
	for _, r := range full.Rows {
		if r.Kind != kind {
			reduced.Rows = append(reduced.Rows, r)
		}
	}
	if len(reduced.Rows) == len(full.Rows) {
		t.Fatalf("no %s rows to remove: the silence below would be the absence of a subject", kind)
	}
	withoutFloor := map[string]int{}
	for k, v := range Floors {
		if k != kind {
			withoutFloor[k] = v
		}
	}
	defer func(orig map[string]int) { Floors = orig }(Floors)
	Floors = withoutFloor
	if err := reduced.checkFloors(); err != nil {
		t.Errorf("the floors caught %d rows becoming %d: %v — then the reverse check is not the only "+
			"thing covering this kind, and this control's justification needs re-measuring",
			len(full.Rows), len(reduced.Rows), err)
	}
	if err := reduced.checkContributions(); err != nil {
		t.Errorf("checkContributions caught the loss: %v — same re-measurement", err)
	}

	// Second: with the kind undeclared, the reader must refuse rather than drop those arms.
	delete(memargKinds, kind)
	t.Cleanup(func() { memargKinds[kind] = true })
	if _, err := BuildFromPins(); !errors.Is(err, ErrUndeclaredKind) {
		t.Errorf("undeclaring %s gave err=%v, want ErrUndeclaredKind — the arm that names it states "+
			"a natural alignment, and dropping it as \"takes no memarg\" is the reader declining to "+
			"read the line it is looking at", kind, err)
	}
}

// TestEveryAlignmentArmIsRead is the reverse check's *level*, beside its direction.
//
// The test above proves the check fires. It cannot prove the check is currently satisfied for a
// reason other than luck, and there is a specific way to be wrong here: `reOptAlign` searches an
// arm's whole body, so an arm of some unrelated kind that happened to contain the token sequence
// `opt a 3` would make the reverse check a permanent false positive — and the repair a reader would
// reach for is to widen memargKinds, which silences it for a kind that takes no memarg.
//
// So the population is counted, both ways, from the authorities themselves: every arm carrying an
// `opt a N` is a row, and every row carries one. `grep -c 'opt a '` finds 45 in the core lexer and
// 111 in the threads one; the reader's own count must match, or one of the two is reading past its
// grammar.
func TestEveryAlignmentArmIsRead(t *testing.T) {
	requireRef(t)
	auths, err := pinAuthorities()
	if err != nil {
		t.Fatalf("pinAuthorities: %v", err)
	}
	for _, a := range auths {
		rows, err := armRows(a)
		if err != nil {
			t.Fatalf("armRows(%s): %v", a.LexerPath, err)
		}
		// The independent count: `opt a` occurrences in the file, which is the grep this package's
		// doc cites. Compared against the rows rather than trusted, because the two are different
		// mechanisms over the same substrate — the arm reader and a substring scan.
		occurrences := strings.Count(a.Lexer, "opt a ")
		if occurrences == 0 {
			t.Fatalf("%s contains no `opt a `: the comparison below is empty against empty", a.LexerPath)
		}
		if len(rows) != occurrences {
			t.Errorf("%s: %d rows against %d `opt a ` occurrences — either an arm states an "+
				"alignment under a kind memargKinds does not declare (and the reverse check should "+
				"have fired), or the reader is finding one where the reference writes none",
				a.LexerPath, len(rows), occurrences)
		}
		t.Logf("%s: %d rows, %d `opt a ` occurrences", a.LexerPath, len(rows), occurrences)
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
// continuation instead of at its head is a citation that does not resolve. **And the file, which
// composition made a separate claim**: two pins hold an `i32.load` arm each, so agreeing on the
// keyword, the kind and the line while disagreeing on the authority is now possible — and it is
// exactly what a base-wins failure in one generator and not the other produces.
//
// Both sides are the *composed* tables, which is not a detail. Against `keywordgen.Extract` over the
// core lexer alone this would report 66 atomic mnemonics as "not a keyword" — a red board blaming
// the wrong generator, which is the disagreement-between-two-compositions failure BuildFromPins
// exists to prevent, arriving through a control instead of through the command.
func TestEveryMnemonicIsAKeyword(t *testing.T) {
	tab := extract(t)

	kt, err := keywordgen.BuildFromPins()
	if err != nil {
		t.Fatalf("keywordgen.BuildFromPins: %v", err)
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
		case a.From != r.From:
			t.Errorf("%q: memarggen read it from %s, keywordgen from %s — the two generators "+
				"composed the pin set differently, so one of the two tables cites a file that does "+
				"not hold the row", r.Mnemonic, r.From, a.From)
		case a.Line != r.Line:
			t.Errorf("%q: memarggen reads it at %s:%d, keywordgen at :%d", r.Mnemonic, r.From, r.Line, a.Line)
		case string(a.Kind) != r.Kind:
			t.Errorf("%q: memarggen read kind %q, keywordgen read %q from the same body — the two "+
				"regexps are picking up different identifiers", r.Mnemonic, r.Kind, a.Kind)
		}
	}
	t.Logf("%d memarg rows, all keywords, kinds/lines/files agreeing with keywordgen", len(tab.Rows))
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
