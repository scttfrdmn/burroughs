package text_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The UTF-8 position partition — #62's door guard, written before the parser it guards.
//
// # What it is for
//
// `utf8-invalid-encoding.wast` is 176 vectors, all `assert_malformed` with the expected
// string `malformed UTF-8 encoding`, and it is the largest bucket in the text column. It is
// also the cheapest bucket to buy for the wrong reason: every one of the 176 has the shape
// `(module quote "(func (export \"<bad bytes>\"))")`, so a wat reader that rejected any
// string token whose bytes are not valid UTF-8 would take all 176 to pass, move the board
// by 176, and be *wrong about the grammar* — which is the overfitting failure (§9 G-3) in
// its purest available form. The measurement that makes it pure:
//
//	string/var tokens in the vendored suite that decode to invalid UTF-8: 864
//	of those, inside a (module quote ...) form:                           177
//	of those 177, in a command that expects an error (reject direction):  177
//	of those 177, in a command that must succeed (accept direction):        0
//
// **The accept direction is empty.** Not small — empty. No quote-form vector in the suite
// contains a legal invalid-UTF-8 string, so a blanket byte check over string tokens is a
// mutation the oracle cannot kill: 177/177 on the reject side, nothing on the other side to
// notice, green board, wrong parser. That is the exact configuration the suite-is-the-oracle
// discipline warns about — the spec is the objective function, and here the suite samples it
// in one direction only.
//
// The 864 figure is also a correction worth keeping. It came out of the lexer; an earlier
// grep of the suite text put the number at 14, because a grep measures *text* and the
// question is about *tokens*. Measure with the instrument, not a regex.
//
// # The trap in writing this control at all
//
// A control over a parser that does not exist yet passes for the wrong reason. That defect
// has bitten this project twice (#25's data-segment test, the Fatalf-then-Skip helper), and
// a control pinning "the parser decodes UTF-8 at `name` and nowhere else" would today be
// green because *nothing* decodes UTF-8 at either place — a green that survives the bug it
// names, indistinguishable on the board from the real thing.
//
// So the control is not written against the parser. It is written against **parser.mly**,
// which exists, is vendored, and is the authority. It asserts the grammar facts a correct
// implementation must satisfy, derived from the reference's own text:
//
//   - `name` (:46-47) decodes and rejects. It is reached from a closed set of productions.
//   - `var` (:49-52) decodes and rejects, separately, for `VAR` tokens including `$"..."`.
//   - `string_list` (:342-344) is plain concatenation with **no decode at all** — the
//     accept direction, and the reason the empty accept column above is not evidence that
//     rejecting everywhere is safe. `(data "\aa")` and `(module binary "\aa")` both route
//     through `string_list`.
//
// Every one of those three is falsifiable today by editing the vendored grammar, which is
// what makes this a control rather than an aspiration. The suite half — that the 177 sit in
// exactly the positions the grammar says — is falsifiable today too.
//
// When the parser lands, TestUTF8DecodeSitedOnlyAtNameAndVar (this file, below) starts
// asserting the implementation against the same derived sets. Until then it is skipped with
// a stated reason rather than passing vacuously, because *a skip is not a verdict* and the
// alternative is worse than a skip: a pass.
//
// # Why this file and not internal/spec
//
// The grammar facts are the text package's subject. The board's counts are internal/spec's.
// Splitting them puts each assertion where its authority is.

// refPath resolves a vendored authority from this package's directory.
func refPath(rel string) string { return filepath.Join("..", "..", rel) }

// letBody returns the text of an OCaml `let <helper> s loc =` binding, bounded at the next
// top-level `let`, and reports whether the binding was found.
//
// The bound is the point. An unbounded search for a fact "somewhere after this definition"
// finds it in the *next* definition, which is how this file's first draft passed with the
// fact it was asserting deleted. A body-scoped reader makes each helper answer for itself.
func letBody(src, helper string) (string, bool) {
	head := "let " + helper + " s loc ="
	i := strings.Index(src, head)
	if i < 0 {
		return "", false
	}
	rest := src[i+len(head):]
	if j := strings.Index(rest, "\nlet "); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest), true
}

var (
	// string_list, in full: two arms, empty and concatenation, and nothing else.
	reStringList = regexp.MustCompile(`(?m)^string_list :\n((?:  \|.*\n)+)`)
	// A production *using* the `name` nonterminal. The leading `| ` and the word boundary
	// keep this off `let name`, `NameMap`, and identifiers containing "name".
	//
	// Anchored on `LPAR` rather than on "starts with a capital", and the difference was
	// found by printing the matches instead of trusting the count. A first attempt matched
	// any `  | ` arm and reported **8** where the hand count was 7; the extra was `name`'s
	// *own definition* at :340, `| STRING { name $1 $sloc }`. A definition is not a use, and
	// a floor phrased "N productions use it" that quietly counts the definer is off by one in
	// the direction that makes the floor easier to satisfy. A second attempt excluded it with
	// `(?:[A-Z]|…)` and still reported 8, because `STRING` starts with a capital too — which
	// is the lesson twice in one regex: the count was the instrument's, not the grammar's,
	// both times. Every `name` use at bdd7164 is inside a parenthesized form, so LPAR is the
	// discriminator the grammar actually offers.
	reNameUse = regexp.MustCompile(`(?m)^  \| LPAR .*\bname\b.*$`)
)

// TestReferenceDecodesUTF8AtNameAndVarOnly pins the three grammar facts #62 is built on.
//
// Falsifiable today: each assertion fails if the vendored parser.mly is edited to move,
// remove, or add a decode. That is the whole reason the control can exist before the parser
// does — its subject is the authority, which is present, not the implementation, which is
// not.
func TestReferenceDecodesUTF8AtNameAndVarOnly(t *testing.T) {
	src := testenv.RequireSpecRef(t, refPath(testenv.RefParserMLY))

	// Vacuity floor before any containment or count. parser.mly is 54523 bytes at bdd7164;
	// RequireSpecRef's own floor covers truncation, and this covers the narrower failure
	// of reading a file that is present but not this grammar.
	if n := strings.Count(src, "\n"); n < 1000 {
		t.Fatalf("parser.mly has %d lines, want >=1000 (1548 at bdd7164) — every assertion "+
			"below is a search over this text, and a search over the wrong text finds nothing "+
			"and reports agreement", n)
	}

	// Facts 1 and 2: `name` and `var` each decode and reject, in their *own* bodies.
	//
	// Scoped to one helper's body rather than matched with `(?s).*?` across the file, and
	// that is a defect this control had until it was falsified. The first draft used
	// `let name s loc =.*?Utf8\.decode s.*?"malformed UTF-8 encoding"` with `(?s)`, so the
	// span crossed newlines: deleting `name`'s decode entirely left the pattern matching
	// forward into `var`'s body four lines below, and the test **passed**. Two assertions
	// that could both be satisfied by one site — which is the shared-value partition defect
	// (#34) in a regex, and it was invisible until the fact was actually removed.
	//
	// So the body is cut at the next `let`, and each helper is asked about its own text.
	for _, f := range []struct {
		helper string
		lines  string
		why    string
	}{
		{"name", "46-47", "the reject site the 176 utf8-invalid-encoding.wast vectors are answered by"},
		{"var", "49-52", "the second reject site, reached from `idx` and `bindidx`; it answers id.wast:31 — `(func $\"\\ef\")`"},
	} {
		body, ok := letBody(src, f.helper)
		if !ok {
			t.Errorf("parser.mly no longer defines `let %s s loc` (was parser.mly:%s).\n\t%s.\n\t"+
				"If upstream moved it, #62's siting is wrong and the partition must be "+
				"re-derived — do not relax this to keep the suite green.", f.helper, f.lines, f.why)
			continue
		}
		if !strings.Contains(body, "Utf8.decode s") {
			t.Errorf("`let %s` no longer calls Utf8.decode:\n%s\n\t%s", f.helper, body, f.why)
		}
		if !strings.Contains(body, `"malformed UTF-8 encoding"`) {
			t.Errorf("`let %s` no longer errors `malformed UTF-8 encoding`:\n%s\n\t%s",
				f.helper, body, f.why)
		}
	}

	// Fact 3 — the accept direction, and the load-bearing one. `string_list` concatenates
	// and does not decode. If it ever did, a blanket byte check would be *correct* and #62's
	// whole partition would collapse; since it does not, the check is wrong in general and
	// the suite cannot say so.
	m := reStringList.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("parser.mly no longer has a `string_list :` production — the accept direction " +
			"has no authority to be read from, and the empty accept column in the suite is not " +
			"evidence of anything on its own")
	}
	if strings.Contains(m[1], "Utf8") || strings.Contains(m[1], "malformed") {
		t.Errorf("`string_list` now touches UTF-8:\n%s\n\tThe accept direction moved. Data "+
			"segments and (module binary ...) payloads route through this production, and #62 "+
			"is sited on it *not* decoding.", m[1])
	}

	// And the arm count, so "does not contain Utf8" cannot be satisfied by a production
	// that upstream replaced with something else entirely.
	if arms := strings.Count(strings.TrimSpace(m[1]), "\n") + 1; arms != 2 {
		t.Errorf("`string_list` has %d arms, want 2 (empty, and concatenation):\n%s", arms, m[1])
	}

	// The `name` use sites. A count with a floor rather than an exact list: the fact #62
	// needs is "a closed and small set of positions", and pinning the exact productions
	// would make an upstream sugar addition a failure in this file rather than a review
	// question in the parser's.
	uses := reNameUse.FindAllString(src, -1)
	if len(uses) < 5 {
		t.Errorf("found %d productions using the `name` nonterminal, want >=5 (7 at bdd7164: "+
			"IMPORT x2, EXPORT x2, INVOKE, GET, REGISTER).\n\tA regex finding too few is how "+
			"this control would silently narrow — the positions are the partition.\n\tmatches:\n%s",
			len(uses), strings.Join(uses, "\n"))
	}
	// The definition site must NOT be among them, because a use count that includes the
	// definer can be satisfied by the definer alone. Asserted rather than trusted to the
	// pattern: two drafts of the regex got this wrong in two different ways.
	for _, u := range uses {
		if strings.Contains(u, "STRING") {
			t.Errorf("reNameUse matched `name`'s own definition:\n\t%s\n\tA definition is not a "+
				"use; a floor counting the definer is satisfiable with zero real positions.", u)
		}
	}
	// Upper bound too, because the claim is that the set is *closed*. A grammar where
	// `name` appears in fifty productions is not one where siting a check at `name` is a
	// small, reviewable change.
	if len(uses) > 20 {
		t.Errorf("found %d productions using `name`, want <=20 — either upstream widened the "+
			"nonterminal's reach substantially or this regex is over-matching; both need "+
			"reading before #62's siting claim is quoted again", len(uses))
	}
	t.Logf("parser.mly at the pinned revision: `name` used by %d productions, "+
		"`string_list` has 2 non-decoding arms, two decode sites (`name`, `var`)", len(uses))
}

// TestSuiteUTF8VectorsAreAllAtNamePositions is the suite half: the 176 are one shape, and
// the shape is an export name.
//
// Why assert something the board already counts: the board counts a *bucket*, and the bucket
// is keyed by the expected spec string. That key says nothing about position, and position is
// the entire content of #62's claim. A future vector in some other position would join the
// same bucket silently and make the siting claim false while the count stayed put — bucket
// size estimates the reward, not the job.
func TestSuiteUTF8VectorsAreAllAtNamePositions(t *testing.T) {
	src := testenv.RequireSuiteFile(t, filepath.Join("..", "..", "testdata", "spec", "utf8-invalid-encoding.wast"))

	lines := strings.Split(string(src), "\n")
	var vectors, exportShaped int
	for _, ln := range lines {
		if !strings.Contains(ln, "assert_malformed") {
			continue
		}
		vectors++
		// Every vector at bdd7164 is `(module quote "(func (export \"...\"))")`. The
		// export keyword is what makes it a `name` position.
		if strings.Contains(ln, `module quote`) && strings.Contains(ln, `export`) {
			exportShaped++
		}
	}

	if vectors < 150 {
		t.Fatalf("found %d assert_malformed lines in utf8-invalid-encoding.wast, want >=150 "+
			"(176 at bdd7164) — the ratio below is over this set, and a ratio over an empty "+
			"set is 0/0", vectors)
	}
	if exportShaped != vectors {
		t.Errorf("%d of %d vectors are `(module quote ... (export ...))`; %d are some other "+
			"shape.\n\tThe siting claim in #62 is that this whole file is answered at the "+
			"`name` production. A vector in another position is answered somewhere else and "+
			"must be split out before the bucket is quoted as one job.",
			exportShaped, vectors, vectors-exportShaped)
	}
	t.Logf("utf8-invalid-encoding.wast: %d/%d vectors at an export-name position", exportShaped, vectors)
}

// TestUTF8DecodeSitedOnlyAtNameAndVar is the implementation half, and it is **skipped until
// the parser exists** rather than passing.
//
// This is the trap named in this file's header, handled explicitly. The assertions below
// would all pass today — nothing decodes UTF-8 at a parser position, so "does not reject a
// legal string" holds trivially and "rejects at name" would be the only failure. Letting the
// accept half pass while the reject half fails would put a green beside a red and invite
// exactly the fix that buys the 176 wrongly.
//
// So it skips, through the licensed door, with the reason stated: the subject does not exist.
// When ReadModule lands, delete the skip and the assertions become the real control — the
// same vectors, the same three positions, no rewriting.
func TestUTF8DecodeSitedOnlyAtNameAndVar(t *testing.T) {
	testenv.SkipUntilImplemented(t, readModule(nil), notImplemented, "#62",
		"every assertion below would pass for want of a subject: nothing decodes UTF-8 at a "+
			"parser position yet, so the accept half holds trivially and only the reject half "+
			"can fail — a green beside a red that invites buying the 176 with a blanket check")

	// The partition, one row per position. Written now so that the parser's author does not
	// get to choose the vectors after seeing which ones are inconvenient.
	for _, tc := range []struct {
		name   string
		src    string
		reject bool
		why    string
	}{
		{
			name:   "export name",
			src:    `(func (export "\ef"))`,
			reject: true,
			why:    "parser.mly:46-47 via the EXPORT production; the 176's shape",
		},
		{
			name:   "import module and field names",
			src:    `(import "\ef" "f" (func))`,
			reject: true,
			why:    "parser.mly:1251, which takes two `name`s — synthetic; the suite's UTF-8 file exercises only export",
		},
		{
			name:   "quoted identifier",
			src:    `(func $"\ef")`,
			reject: true,
			why:    "parser.mly:49-52, the `var` site — id.wast:31",
		},
		{
			name:   "data segment payload",
			src:    `(module (memory 1) (data (i32.const 0) "\ef"))`,
			reject: false,
			why:    "parser.mly:1096 routes through string_list, which does not decode — the accept direction",
		},
		{
			name:   "passive data segment payload",
			src:    `(module (data "\ef\ff\fe"))`,
			reject: false,
			why:    "same production, no offset — synthetic",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := readModule([]byte(tc.src))
			switch {
			case tc.reject && err == nil:
				t.Fatalf("%s accepted; want `malformed UTF-8 encoding` (%s)", tc.src, tc.why)
			case tc.reject && !strings.Contains(err.Error(), "malformed UTF-8 encoding"):
				t.Fatalf("%s gave %v; want `malformed UTF-8 encoding` (%s)", tc.src, err, tc.why)
			case !tc.reject && err != nil:
				t.Fatalf("%s rejected with %v; it is LEGAL (%s).\n\t"+
					"This is the failure a blanket string check produces, and the suite has no "+
					"vector that catches it: 0 accept-direction sites in 1229 quote commands.",
					tc.src, err, tc.why)
			}
		})
	}
}

// notImplemented is the sentinel SkipUntilImplemented matches on, and the mechanism by which
// this file's deferral expires without anyone remembering to expire it.
//
// It must be a *sentinel error*, never nil: a stub returning nil reads to the door as "the
// subject exists", and the control would then run its accept half against nothing and pass.
const notImplemented = "wat parser not implemented"

// readModule is the parser entry point the implementation half calls, standing in for
// text.ReadModule until it exists.
//
// The placeholder is what makes the pre-registered control *compile*, which is what makes it
// impossible to forget: it is in the build, it is in `make check`, and the day ReadModule
// lands this function is deleted and the call rewired — one edit, in a file whose assertions
// were fixed before their author could see which vectors were inconvenient.
func readModule([]byte) error {
	return fmt.Errorf("%s (#62): the control above is pre-registered, not aspirational", notImplemented)
}
