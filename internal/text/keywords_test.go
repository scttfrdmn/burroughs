package text

import (
	"regexp"
	"testing"
)

// The committed table's integrity checks: the questions that can be asked about
// keywords.go *without* a vendored reference.
//
// keywordgen's own suite already asserts all of this about a fresh extraction, and this is
// not a duplicate of it — the artifacts differ. `make keyword-drift` judges what the
// extractor produces today and needs third_party/spec, so it cannot live in `make check`
// (0007's consequence, this grammar). These judge the file in the tree, on a fresh clone,
// with no fetch.
//
// What that gap actually is, stated plainly: `DO NOT EDIT` is a request. A hand edit
// adding `get_local` to keywords.go passes `make check`, passes `go build`, and is caught
// only in the lane that vendors the reference — and `unused` will not object to the map
// being unread either, because `.golangci.yml` sets `exclusions.generated: lax`. Two
// automatic silences over one file. These tests are what makes the file answerable
// anyway.
//
// They are also this package's tripwire in the decision-0006 sense: the table ships one
// increment ahead of its consumer (see doc.go), and a deferral is discharged by a control,
// never by an intention.

// keywordShape is the `keyword` production the reference matches *before* its arm dispatch
// runs: `let keyword = ['a'-'z'] (letter | digit | '_' | '.' | ':')+` (lexer.mll:111).
//
// Restated here rather than imported, and the duplication is the point: keywordgen asserts
// the extraction satisfies it, this asserts the *committed file* does. A shared constant
// would make one edit able to bless both, which is the shape of a control that agrees with
// itself.
var keywordShape = regexp.MustCompile(`^[a-z][a-zA-Z0-9_.:]+$`)

// tableFloor is the vacuity floor, and it is here for the reason every floor in this repo
// is: a comparison against an empty set succeeds. Every assertion below is a loop over
// `keywords`, so an emptied or truncated table passes all of them by iterating nothing —
// green with the mechanism intact and asserting nothing.
//
// 400 against 589 committed at bdd7164, matching keywordgen.Floor by intent and not by
// import: the same number typed at two sites is normally a drift risk, but these two
// floors describe different artifacts and a future revision could legitimately move one.
const tableFloor = 400

func TestCommittedTableIsNotVacuous(t *testing.T) {
	if len(keywords) < tableFloor {
		t.Fatalf("keywords.go holds %d entries, floor is %d — every other test in this file "+
			"loops over this map, so a truncated table would pass them all by asserting nothing "+
			"(run: make keywords)", len(keywords), tableFloor)
	}
	t.Logf("%d keywords committed", len(keywords))
}

// TestCommittedTableOmitsTheObsoleteMnemonics is the assertion three of #53's
// oracle-covered vectors score against directly, pinned at the artifact a future lexer
// will actually read.
//
// Absence is the whole reject-direction contract here: the reference states it in one line
// (`| _ -> unknown lexbuf`, lexer.mll:809), so `get_local` is rejected by *not being in
// this map* rather than by any code naming it. Which makes an accidental addition
// unusually quiet — nothing would look wrong at the site of the bug, because there is no
// site.
func TestCommittedTableOmitsTheObsoleteMnemonics(t *testing.T) {
	// The same eleven as keywordgen's list, which is itself checked against
	// testdata/spec/obsolete-keywords.wast by TestObsoleteMnemonicsMatchTheVector. Cited
	// through that test rather than re-parsing the vector here: this file's premise is
	// "no corpus needed", and reading the suite would break it.
	obsolete := []string{
		"current_memory", "grow_memory", "get_local", "set_local", "tee_local",
		"anyfunc", "get_global", "set_global",
		"i32.wrap/i64", "i32.trunc_s:sat/f32", "f32x4.convert_s/i32x4",
	}
	if len(obsolete) != 11 {
		t.Fatalf("the obsolete list holds %d mnemonics, want 11 — this test's name is a claim "+
			"about eleven vectors", len(obsolete))
	}
	for _, kw := range obsolete {
		if kind, ok := keywords[kw]; ok {
			t.Errorf("committed table contains obsolete mnemonic %q (kind %s) — the suite asserts "+
				"it is not an operator, so this row would accept a module the spec calls malformed",
				kw, kind)
		}
	}
}

// TestEveryCommittedKeywordIsReachable asserts every row is a string the reference's
// `keyword` production can deliver, and every row names a token kind.
//
// Not a formatting check. An arm head outside that charset is unreachable in the reference
// and would be a row here that no input can produce — and the character that matters is
// `/`, absent from `keyword` and present in `reserved`, which is what routes
// `i32.wrap/i64` to the second `unknown operator` producer. A row like `"i32.wrap/i64"`
// appearing in this map would be simultaneously the bug the test above catches and the
// unreachable row this one does; two controls on one edit, by different mechanisms, which
// is what a partition should look like.
func TestEveryCommittedKeywordIsReachable(t *testing.T) {
	if len(keywords) < tableFloor {
		t.Fatalf("table is vacuous (%d entries); TestCommittedTableIsNotVacuous owns the "+
			"diagnosis", len(keywords))
	}
	for kw, kind := range keywords {
		if !keywordShape.MatchString(kw) {
			t.Errorf("keyword %q does not match the `keyword` production (lexer.mll:111) that "+
				"gates the reference's arm dispatch, so no input can reach this row", kw)
		}
		if kind == "" {
			t.Errorf("keyword %q has no token kind — a row saying a keyword lexes to nothing is a "+
				"narrower claim than the authority's, made silently", kw)
		}
	}
}

// TestSomeKeywordsEveryPortWillNeed is the plausibility half of the vacuity check: a floor
// bounds the count, and nothing bounds whether the count is made of the right things.
//
// A garbled extraction that produced 589 rows of the wrong strings passes every test above
// — the floor, the shape, and the eleven absences — because all three are properties of the
// *form*. This one asks about content, and it asks with a handful of mnemonics no version
// of wat has ever lacked, so it is stable against upstream churn in a way a spot-check of
// SIMD spellings would not be.
//
// Its kinds are asserted too, since a table with every keyword mapped to one kind would
// also survive the form checks. Values verified by printing the committed rows, not by
// reading them off the reference — print-don't-trust, on the half no vector covers.
func TestSomeKeywordsEveryPortWillNeed(t *testing.T) {
	want := map[string]keywordKind{
		"module":  "MODULE",
		"func":    "FUNC",
		"param":   "PARAM",
		"result":  "RESULT",
		"nop":     "NOP",
		"i32":     "NUMTYPE",
		"i32.add": "BINARY",
	}
	for kw, kind := range want {
		got, ok := keywords[kw]
		if !ok {
			t.Errorf("committed table is missing %q — a table this wrong would still pass the "+
				"floor and the shape checks, which is why this test asks about content", kw)
			continue
		}
		if got != kind {
			t.Errorf("keywords[%q] = %q, want %q", kw, got, kind)
		}
	}
}
