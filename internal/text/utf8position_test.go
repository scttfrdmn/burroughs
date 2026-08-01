package text

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
// The implementation half, TestUTF8DecodeSitedOnlyAtNameAndVar below, resolves the same trap
// differently and better: **it asserts the accept direction at the lexer**, which exists. The
// first draft skipped until the parser arrived, and CI's *no test declined to answer* step
// rejected it — correctly, and the rejection produced the sharper design. The property worth
// pinning today is not "the parser rejects at `name`" but "this package does *not* reject in
// the lexer", and the second is checkable now, at the layer where the wrong fix is actually
// reachable (`emitVarString`, attempted once in PR #60). *A pre-registered control that wants
// a skip has usually not found the layer where its property is already checkable.*
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

// TestUTF8DecodeSitedOnlyAtNameAndVar is the implementation half, and it **asserts in both
// states** rather than skipping in one of them.
//
// # Why not a skip, which is what this was first written as
//
// The obvious shape for a control that precedes its subject is "skip until the subject
// exists", and that is what the first draft did, through a new licensed door. CI rejected
// it, correctly: `.github/workflows/ci.yml`'s **no test declined to answer** step greps the
// output channel for SKIP lines under `BURROUGHS_NO_SKIP=1` and fails the build on any of
// them — belt and suspenders, because a skip does not fail a test run, which is the entire
// problem. The ruling I was about to ask for had already been made and gated.
//
// It is also the better answer, and the reason is worth keeping: **there is something real to
// assert today.** The trap named in this file's header is that "the parser rejects at `name`"
// cannot be asserted before the parser — but the accept direction can, at the layer that
// exists. All five sources below must **lex clean**, and that is exactly the property a
// blanket byte check would destroy: the wrong fix for the 176 is `validUTF8` inside
// `emitVarString` or `scanAnnotBody`, this package's own code, and it has been attempted
// once already (PR #60). So the lexer-layer assertion is not a placeholder for the real
// control, it is the control aimed at the layer where the mistake is actually reachable.
//
// The probe therefore chooses *which layer's verdict* to assert, and both branches assert:
//
//   - parser absent → all five must lex clean. The accept direction, pinned where the
//     defect is reachable today.
//   - parser present → the full three-way partition, `name` and `var` rejecting and
//     `string_list` accepting.
//
// The rows never move between states; only which layer answers them does. And when the
// parser lands, nothing here needs editing — the probe stops reporting the sentinel and the
// stricter branch takes over on its own. A deferral that expires by mechanism rather than by
// memory, with no skip anywhere in it.
func TestUTF8DecodeSitedOnlyAtNameAndVar(t *testing.T) {
	// Stamped, not deduced: which subject is answering decides which assertions apply, so
	// the branch is recorded in the log rather than left for a reader to infer from a green.
	parserExists := !strings.Contains(fmt.Sprint(readModule(nil)), notImplemented)
	if parserExists {
		t.Logf("the wat parser answers: asserting the full three-way partition")
	} else {
		t.Logf("the wat parser does not exist yet (#62): asserting the accept direction at the " +
			"lexer, which is the layer where a blanket UTF-8 check is reachable today")
	}

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
			// The lexer-layer assertion, and it applies in *both* states: every one of these
			// sources is all-ASCII wat text whose high bytes arrive via `\ef` escapes, so
			// none of them is a lexical error in the reference either way. A rejection here
			// is this package claiming a verdict that is not its layer's to give — the
			// inverse wrong-layer tell, and the shape of the mistake a blanket check makes.
			if _, err := LexAll([]byte(tc.src)); err != nil {
				t.Fatalf("%s must LEX clean, got %v\n\t"+
					"%s.\n\tRejecting it in the lexer answers a parser-layer vector from the "+
					"wrong stratum — right on the 176, wrong in general, and invisible on the "+
					"board by construction. The suite cannot catch it: 0 accept-direction "+
					"sites in 1229 quote commands.", tc.src, err, tc.why)
			}

			if !parserExists {
				return
			}

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

// notImplemented is the sentinel `parserExists` matches on, and the mechanism by which this
// file's deferral expires without anyone remembering to expire it.
//
// It must be a *sentinel error*, never nil: a stub returning nil reads as "the subject
// exists", and the reject rows would then score their `err == nil` case as an acceptance
// defect — a red for a subject that does not exist, which is the falsifiability law's failure
// in the other direction. Absent must be *distinguishable* from wrong, and a sentinel is what
// makes it so.
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
