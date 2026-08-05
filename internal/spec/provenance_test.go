package spec

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestFixtureProvenance machine-checks the `binary.wast:N` citations in the
// engine's hand-written test fixtures against the suite itself.
//
// Why this exists: a fixture comment claimed its vectors were "verbatim from
// binary.wast" while two of them were not — one had been hand-truncated from 11
// bytes to 8, and one was a mutation of a vector the suite does not contain.
// Both survived review precisely because the comment asserted the provenance
// that the reader would otherwise have checked. A citation nobody verifies is a
// claim, not a citation.
//
// The structural fix is that a hand-typed vector must either carry a citation
// this test can confirm, or be marked as a deliberate synthetic. Silence is not
// an option — the same rule as the lint and unreachability policies.
//
// Fuzz corpora (FuzzDecodeModule, FuzzULEB) are seeded from the suite directly
// rather than from fixtures, which removes the transcription step for the bulk
// of the corpus. This test covers what remains hand-written.
func TestFixtureProvenance(t *testing.T) {
	requireSuite(t)

	suite := suiteImages(t)

	// Fixture files that carry citations, relative to this package.
	//
	// A file missing from this list is unchecked, which makes the list itself the
	// weak point: adding a new fixture file and forgetting to register it restores
	// exactly the drift this test was written to catch. TestEveryFixtureFileIsChecked
	// closes that by deriving the set from disk and comparing.
	files := []string{
		"../binary/binary_test.go",
		"../binary/sections_test.go",
		"../binary/constexpr_test.go",
	}

	// A citation is a comment of the form `// <file>.wast:<line>` anywhere on the
	// line that also holds the byte literal.
	cite := regexp.MustCompile(`//\s*([a-zA-Z0-9_.-]+\.wast):(\d+)`)
	// A byte-slice literal: {0x00, 0x61, ...}. Braces, hex bytes, commas only.
	lit := regexp.MustCompile(`\{((?:\s*0x[0-9a-fA-F]{2}\s*,?)*)\}`)

	var checked, checkedFragments, synthetic, derived int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		lines := strings.Split(string(src), "\n")
		derivedRows := derivedRowsIn(lines)
		derived += len(derivedRows)
		for i, line := range lines {
			lineNo := i + 1
			m := cite.FindStringSubmatch(line)
			if m == nil {
				// No citation. Flag lines that hold a preamble-shaped literal
				// anyway — an uncited vector is exactly what this test hunts.
				if lm := lit.FindStringSubmatch(line); lm != nil {
					b := parseHexBytes(lm[1])
					if len(b) >= 8 && !strings.Contains(line, "synthetic") && !derivedRows[lineNo] {
						if _, ok := suite[string(b)]; !ok {
							t.Errorf("%s:%d: uncited %d-byte vector % x not found in the suite;\n"+
								"\tadd a `// <file>.wast:N` citation, mark it `synthetic` with a reason, "+
								"or declare it `derived from <file>.wast:N,M` in the preceding comment",
								f, lineNo, len(b), b)
						}
					}
					if strings.Contains(line, "synthetic") {
						synthetic++
					}
				}
				continue
			}
			lm := lit.FindStringSubmatch(line)
			if lm == nil {
				continue // a citation in prose, not on a vector
			}
			want := parseHexBytes(lm[1])
			if got, ok := suiteLine(suite, m[1], m[2]); ok {
				// A whole module image: the citation names the `(module binary ...)`
				// command's line and the fixture is the assembled image.
				if !bytes.Equal(want, got) {
					t.Errorf("%s:%d: fixture disagrees with its citation %s:%s\n\tfixture: % x\n\tsuite:   % x",
						f, lineNo, m[1], m[2], want, got)
					continue
				}
				checked++
				continue
			}
			// Not a module-image line. A fixture may also cite a *fragment* — one
			// source line inside a `(module binary ...)`, e.g. the element-segment
			// encoding on elem.wast:360 — which is what a reader-level test needs when
			// the unit under test is a segment grammar rather than a whole module.
			//
			// This is checkable too, and checking it is not a formality: the .wast
			// source line holds the bytes as `"\hh"` escapes, so the fixture can be
			// compared against them directly. Two of #25's seven fragment citations
			// were off by several lines when first written, and that is the drift this
			// file exists to catch — marking them `synthetic` instead would have
			// declared the transcription unverifiable while a transcription is exactly
			// the hazard. The alternative was accepting a citation nobody could
			// confirm, which is the defect, not the fix.
			got, ok := suiteSourceLine(t, m[1], m[2])
			if !ok {
				t.Errorf("%s:%d: citation %s:%s resolves to neither a module image nor a readable suite source line",
					f, lineNo, m[1], m[2])
				continue
			}
			if !bytes.Equal(want, got) {
				t.Errorf("%s:%d: fixture disagrees with the fragment at its citation %s:%s\n\tfixture:     % x\n\tsuite line:  % x",
					f, lineNo, m[1], m[2], want, got)
				continue
			}
			checkedFragments++
		}
	}
	if checked == 0 {
		t.Fatal("no citations checked — the regexes have drifted from the fixtures")
	}
	if checkedFragments == 0 {
		t.Fatal("no fragment citations checked — the fragment path is dead, so its own control is vacuous")
	}
	// The derived exemption gets a vacuity floor for the same reason the two paths above do:
	// `derivedRowsIn` returning zero for every file would silence the category's exemption
	// *and* look identical to a clean board, because an exemption that matches nothing
	// exempts nothing and reports nothing. One is the honest floor — the count is small by
	// nature, since a derivation is rare — and it fires if the scanner stops matching.
	if derived == 0 {
		t.Fatal("no derived rows recognised — the `derived from` exemption matches nothing, so it is either dead or drifted")
	}
	t.Logf("verified %d cited module images, %d cited fragments, %d declared synthetic, %d exempt as derived",
		checked, checkedFragments, synthetic, derived)
}

// derivedRowsIn maps the line numbers a `derived from` declaration covers.
//
// **The third provenance category needed this arm and did not have it** (#37 ruled the
// category in; the uncited-literal check above predates the ruling and knew only "cited or
// synthetic"). A derived vector deliberately differs from every suite image — that is what
// makes it derived — so it can carry neither a citation nor the `synthetic` word, and an
// eight-byte derived literal was reported as an uncited fixture. *A ruling retroactively
// falsifies prose written before it*, and this is the code half of the same sweep.
//
// **The scope is the one construct the declaration introduces, not the file and not a brace
// span**, and both narrowings were forced by printing what the walker claimed rather than
// reading it. A file-wide exemption would let one honest derivation excuse every hand-typed
// vector after it — the laundering channel the category's own rules exist to prevent. And a
// naive brace walk is worse than it looks: the first version followed braces to depth zero, and
// a `derived from` in a **function's doc comment** therefore exempted the entire function body,
// 27 lines of `TestConstExprDefersTheConstVerdict` included. The tell was the count — 41 lines
// exempt from three declarations, which is *a suspiciously unclean result*, the same instrument
// reading a perfect zero would have been.
//
// So the span is the *byte-literal lines* the declaration introduces and nothing else: comment
// lines are walked through, a `func`/`type` line ends the search (a declaration above one is
// documenting the construct, not exempting a vector), and collection stops at the first
// byte-free line **after** the bytes have started.
//
// The bound on the search before the bytes start is a **line budget, not a syntactic guess**,
// and that too was measured: the first version allowed exactly one `{`-suffixed opening line,
// which gofumpt then broke by splitting a table row onto four lines — the byte literal moved two
// lines further from its comment and the exemption silently stopped covering it. A rule that a
// formatter can invalidate is not a rule, and *the formatter is not a review topic*, so the
// walker tolerates a small run of byte-free lines rather than pattern-matching the shapes it
// expects to see.
func derivedRowsIn(lines []string) map[int]bool {
	// The gap a declaration may sit above its literal, in byte-free non-comment lines. Small
	// enough that the next unrelated vector in a table cannot be reached — the neighbouring
	// row is two lines away in the tightest formatting — and large enough to survive gofumpt
	// exploding a composite-literal row.
	const maxGap = 3
	out := map[int]bool{}
	for i, line := range lines {
		if derivedPremises.FindStringIndex(line) == nil {
			continue
		}
		claiming, gap := false, 0
		for j := i + 1; j < len(lines); j++ {
			l := strings.TrimSpace(lines[j])
			if !claiming {
				if strings.HasPrefix(l, "//") || l == "" {
					continue // still in the comment block
				}
				// A declaration sitting above a func or type is documenting *it*. Nothing to
				// exempt: the vectors inside carry their own provenance, and claiming them
				// here is exactly the over-reach the count caught.
				if strings.HasPrefix(l, "func ") || strings.HasPrefix(l, "type ") {
					break
				}
			}
			if !hexByte.MatchString(l) {
				if claiming {
					break // past the literal
				}
				if gap++; gap > maxGap {
					break // the declaration introduces no literal
				}
				continue
			}
			claiming = true
			out[j+1] = true
		}
	}
	return out
}

// hexByte matches a line holding at least one `0x` byte literal — the shape a derived vector's
// lines have, and the discriminator that keeps a span from running past its literal.
var hexByte = regexp.MustCompile(`0x[0-9a-fA-F]{2}`)

// derivedPremises is a regexp over a `derived from <file>.wast:N[,N...]` declaration.
var derivedPremises = regexp.MustCompile(`derived from ([a-zA-Z0-9_.-]+\.wast):([\d,: ]+)`)

// TestDerivedFixturesStateResolvablePremises machine-checks the third provenance
// category (ruling: Scott, PR #37).
//
// A **derived** vector is one the suite *implies* but does not contain.
// TestLEBWidthIsPerField's accept half is the first: it asserts a wide-but-legal
// limits minimum decodes, which binary-leb128.wast cannot say because it only
// asserts malformedness, and which :217 and :525 jointly *bracket* — ten bytes wants
// "integer too large", eleven wants "integer representation too long", so the width
// is exactly 64 and a five-byte 2^32 is fine.
//
// *Entailment from checked facts is legitimate provenance; unstated entailment is
// just synthetic with better manners.* So the category carries two obligations, and
// this test enforces the half a machine can:
//
//  1. the row states its premises — `derived from <file>.wast:N,M` — and its
//     inference, in prose, for a reviewer;
//  2. **every premise resolves**: the line exists and carries suite content.
//
// The inference is reviewed by eyes; a premise citing a line that says something
// else is caught here, by the same mechanism that catches a drifted transcription.
// Without (2) the category would be a laundering channel — "derived" would excuse a
// vector from provenance entirely, which is the shape a suppression wears.
//
// Falsified before trusted: perturbing a premise line number to one holding prose
// fails with "premise does not resolve".
func TestDerivedFixturesStateResolvablePremises(t *testing.T) {
	requireSuite(t)

	// **Every file that writes a `derived from` declaration**, not just the binary ones.
	// The list was the three binary files while the category was binary-only, and the text
	// fixtures then adopted the syntax — eight declarations across four files, none of them
	// checked, because nobody widened the list. Found while ruling that `match_test.go`'s
	// provenance is entailment rather than transcription: the sentence "its premises are
	// checked here" was about to be written, and it was false. *A ruling retroactively
	// falsifies prose written before it* — including prose you are in the middle of writing.
	//
	// Widening this list is the second half of that ruling, and it is what makes the
	// three-category rule true for the text grammar rather than merely stated for it.
	files := []string{
		"../binary/binary_test.go",
		"../binary/sections_test.go",
		"../binary/constexpr_test.go",
		"../text/lexer_test.go",
		"../text/match_test.go",
		"../text/num_test.go",
		"../text/parser_test.go",
		"../text/instr_test.go",
		"../text/label_test.go",
		"../text/annot_test.go",
	}

	var declared, premises int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			lineNo := i + 1
			if !strings.Contains(line, "derived") {
				continue
			}
			m := derivedPremises.FindStringSubmatch(line)
			if m == nil {
				// "derived" as an English word in prose is fine; a *declaration* is the
				// word plus premises. Only flag the case that looks like a claim of
				// provenance without any: the marker sitting on a byte literal.
				if lit := regexp.MustCompile(`\{(?:\s*0x[0-9a-fA-F]{2}\s*,?)+\}`); lit.MatchString(line) {
					t.Errorf("%s:%d: a vector marked `derived` must state its premises as "+
						"`derived from <file>.wast:N,M`; a derivation with no premises is "+
						"synthetic with better manners:\n\t%s", f, lineNo, strings.TrimSpace(line))
				}
				continue
			}
			declared++
			for _, n := range strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' || r == ' ' || r == ':' }) {
				if n == "" {
					continue
				}
				// **What counts as suite content depends on the grammar, not on the checker.**
				// The first version asked `suiteSourceLine`, which returns false unless the
				// line holds `\hh` byte escapes — the right question for a binary premise and
				// the wrong one for every text premise, all seven of which failed on the widened
				// list. `annotations.wast:72` is
				// `(assert_malformed (module quote "(@)") "empty annotation id")`: as clear a
				// premise as the suite contains, and invisible to a resolver looking for hex.
				//
				// So a premise resolves if it holds *either* — escapes for a binary vector, or
				// non-comment source for a text one. Not relaxed to "the line exists": a
				// premise pointing at a blank line or a bare `;;` comment is still a citation
				// nobody can read, and that is the case the original was written to catch.
				if !premiseResolves(t, m[1], n) {
					t.Errorf("%s:%d: premise does not resolve: %s:%s holds no suite content "+
						"(prose, a blank line, or past end of file). A premise nobody can read "+
						"is not a premise.", f, lineNo, m[1], n)
					continue
				}
				premises++
			}
		}
	}

	// The category is new, so its own control must not be vacuous — the same reason
	// TestFixtureProvenance fails when it checks zero citations. If the last derived
	// fixture is ever removed, this fails and asks whether the category is still real.
	if declared == 0 {
		t.Fatal("no derived fixtures found — either the declaration syntax has drifted " +
			"from the fixtures, or the category is unused and this control is vacuous")
	}
	t.Logf("verified %d derived fixtures citing %d resolvable premises", declared, premises)
}

// premiseResolves reports whether one cited line of the suite holds content a premise could
// rest on, in either grammar: byte escapes for a binary vector, or non-comment wat/script
// source for a text one.
//
// It is deliberately not "the line exists". A premise naming a blank line or a bare `;;`
// comment is a citation nobody can read, which is the whole class this control was built for,
// and accepting one would make the check pass on any integer in range.
func premiseResolves(t *testing.T, file, line string) bool {
	t.Helper()
	if _, ok := suiteSourceLine(t, file, line); ok {
		return true
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 {
		return false
	}
	src, err := os.ReadFile(filepath.Join(suiteDir, file))
	if err != nil {
		return false
	}
	lines := strings.Split(string(src), "\n")
	if n > len(lines) {
		return false
	}
	text := lines[n-1]
	if i := strings.Index(text, ";;"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text) != ""
}

// suiteSourceLine returns the bytes encoded by the `"\hh"` escapes on one line of a
// .wast file, for citations that name a fragment inside a module rather than the
// module itself.
//
// It reads the raw source rather than going through the parser on purpose: the
// parser assembles a module from its escape strings and discards which line each
// came from, so the line-level fact is only available here. A line with no escapes
// at all returns false — that is a citation pointing at prose, which is a drifted
// citation and must fail rather than vacuously pass.
func suiteSourceLine(t *testing.T, file, line string) ([]byte, bool) {
	t.Helper()
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 {
		return nil, false
	}
	src, err := os.ReadFile(filepath.Join(suiteDir, file))
	if err != nil {
		return nil, false
	}
	lines := strings.Split(string(src), "\n")
	if n > len(lines) {
		return nil, false
	}
	// Only the quoted part: a trailing `;; comment` may hold hex-looking text.
	text := lines[n-1]
	if i := strings.Index(text, ";;"); i >= 0 {
		text = text[:i]
	}
	esc := regexp.MustCompile(`\\([0-9a-fA-F]{2})`)
	ms := esc.FindAllStringSubmatch(text, -1)
	if len(ms) == 0 {
		return nil, false
	}
	out := make([]byte, 0, len(ms))
	for _, m := range ms {
		v, err := strconv.ParseUint(m[1], 16, 8)
		if err != nil {
			return nil, false
		}
		out = append(out, byte(v))
	}
	return out, true
}

// citedRow decides whether one line of Go source is a *cited fixture row*, and returns the
// citation it carries. It is the single definition of the population the provenance rule
// covers, shared by the guard and by the text checker on purpose: two regexps for one
// concept is how a file comes to be registered with a checker that reads past it.
//
// A cited row is a fixture vector — a composite literal holding string literals, or a
// byte-slice literal — carrying a citation in one of two positions:
//
//  1. a **citation field**: a string literal that *begins* with `<file>.wast:N`, optionally
//     followed by explanatory prose. This is the text fixtures' style.
//  2. a **trailing comment**: `// <file>.wast:N`, the byte-literal fixtures' style.
//
// The `^` anchor in (1) is load-bearing. Without it a citation *mentioned* inside a row
// field is swept in, and a mention is not a transcription: `utf8position_test.go:162` is a
// row about a *grammar site* (`parser.mly:49-52`) whose prose ends "it answers id.wast:31",
// and there is nothing in it copied from the suite that could drift. Anchoring is what
// separates "this row is the vector at X" from "this row relates to X".
func citedRow(line string) (file, lineNo string, ok bool) {
	if !fixtureRow.MatchString(line) && !fixtureByteLit.MatchString(line) {
		return "", "", false
	}
	if m := citeComment.FindStringSubmatch(line); m != nil {
		return m[1], m[2], true
	}
	for _, s := range goStringLiterals(line) {
		if m := citeField.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
			return m[1], m[2], true
		}
	}
	return "", "", false
}

var (
	// A fixture row: a composite literal holding at least one interpreted string.
	fixtureRow = regexp.MustCompile(`\{.*".*".*\}`)
	// A byte-slice literal: {0x00, 0x61, ...}.
	fixtureByteLit = regexp.MustCompile(`\{(?:\s*0x[0-9a-fA-F]{2}\s*,?)+\}`)
	// A citation opening a comment.
	citeComment = regexp.MustCompile(`//\s*([a-zA-Z0-9_.-]+\.wast):(\d+)`)
	// A citation opening a row field. Anchored: see citedRow.
	citeField = regexp.MustCompile(`^([a-zA-Z0-9_.-]+\.wast):(\d+)`)
)

// TestEveryFixtureFileIsChecked guards the guard.
//
// Grave [#78](https://github.com/scttfrdmn/burroughs/issues/78) lives here: this guard's
// trigger regexp under-matched, so it ran green while 17 checkable fixture rows were
// unregistered, and it vouched for a file whose checker read past every line in it. The
// lesson is **a guard's trigger predicate is itself a claim about the space, and an
// under-matching one fails silently by construction** — breaking the assertion never finds
// a trigger that never fires, so the trigger's *coverage* over the population it claims
// gets measured (118 of 244 citations). Coverage is to a trigger what a vacuity floor is
// to a comparison. The three sections below are the full account.
//
// TestFixtureProvenance reads a hand-maintained file list, so a new fixture file
// that nobody adds to it is silently unchecked — the same failure mode as the
// drifted citations, one level up. This derives the set of files that *contain*
// citations from disk and requires the list to cover them.
//
// It is the fixture-provenance argument applied to itself: a control that depends
// on someone remembering to register their work is not a control.
// Two checkers, two fixture shapes, and the guard has to know which owns which — a file
// registered with the wrong one is unchecked while looking checked, which is worse than
// being unlisted, because the guard then vouches for it.
//
// # What triggers registration is a *fixture row*, not a citation
//
// The first version asked whether a file held a citation at all, with the citation
// regexp shared with the two checkers: `//\s*<file>.wast:\d+`, anchored at a comment's
// opening. That was the wrong question asked with a regexp that could not ask it, and the
// two defects hid each other.
//
// Wrong question: a *prose* citation — "imports.wast:62-64 uses `(type $forward)` before
// defining it" — has nothing for either checker to verify. There is no transcription, so
// there is no drift to catch; both checkers skip such lines explicitly (`a citation in
// prose, not on a vector`). Requiring registration for one means registering a file into
// a list whose mechanism reads past it, which is exactly the vouching-for-nothing failure
// the paragraph above forbids. Measured before choosing: the strongest machine check
// available for a prose citation is "the line falls inside some command's extent", and a
// span index over the whole suite covers **169427 of 178222 lines, 95%** — a check that
// passes on nearly any integer is not a check. So prose citations are reviewed by eyes,
// and this guard is scoped to what a machine can actually verify.
//
// Defective regexp: requiring `//` immediately before the citation means a citation held
// in a row *field* is invisible, and the whole text-fixture style puts it there. Measured
// across the engine's tests: 244 citations, 118 of them opening a comment, and **17 cited
// fixture rows in two files were unregistered while this guard said nothing** —
// `parser_test.go`'s nine (the block/loop/if `(param $x i32)` rows citing
// `block.wast:1475`, `loop.wast:783`, `if.wast:1513`; the `catch`/`catch_all` rows; the
// four `mismatching label` rows) and `instr_test.go`'s eight `i8x16.shuffle` lane rows.
// Every one carries a source, an expectation and a citation: the checkable shape. That is
// this guard failing at its whole job — *guard the guard, or the guard is decoration* —
// and it failed silently, because a regexp that under-matches produces no finding rather
// than a wrong one.
//
// So the trigger is a **citation field on a fixture row**: a string literal that *begins*
// with `<file>.wast:N`, or the trailing-comment form the byte-literal fixtures use. The
// anchor is what keeps it a citation rather than a mention — `utf8position_test.go:162`
// ends its `why` with "it answers id.wast:31 — `(func $\"\\ef\")`", which is prose about a
// vector inside a row about a *grammar site*, and there is no transcription in it to drift.
// Scoped to the shape the checkers can verify, not to a substring match.
//
// The definition lives in `citedRow`, shared with the text checker rather than spelled twice:
// two regexps for one concept is how a file comes to be registered with a mechanism that
// reads past it, which is what the `match_test.go` note below records happening.
//
// Falsified before trusted: dropping a registered file from `checked` fails with its exact
// row count (8 and 9); dropping the `^` anchor pulls `utf8position_test.go` in and fails;
// matching any line at all fails on the prose-only files; matching nothing trips the vacuity
// floors rather than passing green.
func TestEveryFixtureFileIsChecked(t *testing.T) {
	checked := map[string]bool{
		// Byte-literal fixtures: TestFixtureProvenance.
		"../binary/binary_test.go":    true,
		"../binary/sections_test.go":  true,
		"../binary/constexpr_test.go": true,
		// Text fixtures: TestTextFixtureProvenance. A wat fixture's vector is source text
		// plus an expected string, not a byte literal, so the image-shaped checker cannot
		// verify one — listing these there would have satisfied this guard and checked
		// nothing. **A registration is not a check.**
		//
		// `match_test.go` is *not* here, and its absence is a finding rather than an
		// oversight. It was registered from the day the text checker was written, and the
		// checker verified nothing in it: every citation it carries is prose or a `derived`
		// declaration, so the row filter skipped all of them. The registration vouched for a
		// file the mechanism read past — the exact failure this guard's own comment names —
		// and it took the `withRows` floor below to say so. Its `derived` premises *are*
		// checked, by TestDerivedFixturesStateResolvablePremises, which is where a file whose
		// provenance is entailment rather than transcription belongs.
		"../text/lexer_test.go":  true,
		"../text/parser_test.go": true,
		"../text/instr_test.go":  true,
		"../text/label_test.go":  true,
		"../text/annot_test.go":  true,
	}

	paths, err := filepath.Glob("../*/*_test.go")
	if err != nil {
		t.Fatal(err)
	}

	var scanned, withRows int
	for _, p := range paths {
		if strings.HasPrefix(p, "../spec/") {
			continue // this package's own tests hold no engine fixtures
		}
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		scanned++
		var rows []string
		for i, line := range strings.Split(string(src), "\n") {
			if _, _, ok := citedRow(line); !ok {
				continue
			}
			rows = append(rows, fmt.Sprintf("%s:%d: %s", p, i+1, strings.TrimSpace(line)))
		}
		if len(rows) == 0 {
			continue
		}
		withRows++
		if !checked[p] {
			t.Errorf("%s carries %d cited fixture row(s) but is in no provenance checker's "+
				"file list; its citations are unverified. First: %s",
				p, len(rows), rows[0])
		}
	}
	// Vacuity floors, because *a comparison against an empty set succeeds*: a glob that
	// stopped matching, or a row regexp that drifted from the fixtures, would leave this
	// loop finding nothing and reporting green while asserting nothing at all.
	if scanned < 10 {
		t.Fatalf("scanned only %d test files; the glob is not reaching the engine's packages",
			scanned)
	}
	if withRows < len(checked) {
		t.Errorf("found cited fixture rows in only %d files but %d are registered; the row "+
			"regexps have drifted from the fixtures, so this guard is asserting less than "+
			"its list claims", withRows, len(checked))
	}
	// And the reverse: a stale entry naming a file that no longer exists would make
	// the list look more thorough than it is.
	for p := range checked {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is in the file list but does not exist: %v", p, err)
		}
	}
}

// TestTextFixtureProvenance is the text-grammar half of the provenance rule.
//
// The wat fixtures cite `(module quote ...)` commands, whose vector is *source text plus an
// expected string* rather than a module image — so TestFixtureProvenance's byte-literal
// regexes cannot see them, and adding those files to its list would have satisfied
// TestEveryFixtureFileIsChecked while verifying nothing.
//
// What is verified, for every `<file>.wast:N` citation on a fixture row:
//
//  1. the cited line resolves to a quote-form command in that file;
//  2. the fixture's expected substring and the command's own `Expect` string agree — a
//     fixture claiming a different error than the suite asserts at that line is a drifted
//     citation even when fixture and suite are each internally consistent;
//  3. the fixture's `src` and the command's assembled source contain one another *in either
//     direction*. Containment rather than equality, because a lexer fixture is legitimately
//     a fragment (a row pinning one lexeme need not carry the whole module) and a parser
//     fixture is legitimately a *wrapper* (a quote module's body is bare `(func …)`, while a
//     vector handed to ReadModule needs `(module (func …))`). Text appearing in neither
//     relation to the cited command is a transcription error.
//
// It earned its keep on the first run: `(@a \x7f)` cited `annotations.wast:26`, which holds
// `(@a \03)`. Both the fixture and the suite were internally consistent — the fixture's
// expected string even matched, because both vectors want `illegal character` — so nothing
// but a machine comparing the *source text* could see it. That is the drifted-citation class
// exactly (PR #37), in the new grammar, found the day the grammar arrived.
//
// Rows marked `synthetic` or `derived` are exempt from (2) and (3) and counted separately,
// same as the binary side.
//
// # Two row layouts, because the fixtures have two
//
// The lexer fixtures put the citation in a *trailing comment* and the expectation last:
// `{"name", "<src>", "<expect>"}, // obsolete-keywords.wast:2`. The parser and instruction
// fixtures put it in a *field*, which shifts everything, and the two field-style widths are
// not even the same shape: `{"<src>", "block.wast:1475"}` carries no expectation at all (its
// table asserts one shared error in the loop body), while
// `{"name", <computed>, "<expect>", "simd_lane.wast:519"}` carries an expectation and no
// source literal, its immediate list spliced from Go constants. Reading any of the three with
// another's positional convention compares the wrong strings — and it does not fail loudly:
// reading `instr_test.go` by the comment-style offsets compared "unexpected token", as though
// it were a module, against an `i8x16.shuffle` vector.
//
// So the layout is *detected*, and the two checks apply **independently, each when its field
// is present**, rather than as a pair. A row with a source and no expectation is still checked
// on its source, which is the direction that catches transcription drift; requiring both would
// have silently exempted the nine `parser_test.go` rows, and an exemption granted by a layout
// mismatch is the worst kind, because nothing reports it. `sourcesChecked` and `expectsChecked`
// are floored separately for exactly that reason: one number cannot show that half the
// mechanism went dead. Rows with no source literal are counted in `computed` and **ceilinged**,
// so the weaker treatment cannot spread quietly.
//
// Falsified before trusted, each probe run rather than argued: perturbing a citation to the
// neighbouring command fails (3) — and note the discrimination, since `block.wast:1475` and
// `:1479` differ by one paren; corrupting only the source text fails (3) with the citation
// intact; swapping an expectation on a computed-vector row fails (2), which is the half with
// no source to fall back on; de-registering a file makes the guard report its 8 and 9 rows;
// dropping the citation anchor pulls in `utf8position_test.go`; and disabling the source scan
// turns all 17 field-style rows into `computed` and trips the ceiling rather than passing.
//
// Two mechanisms were **removed** because a `panic()` proved nothing reached them: an arm for
// a `{name, src, expect, cite}` width no fixture has, and whitespace-insensitive containment
// (the bidirectional test already covers every current row exactly). *A green that survives
// the bug it names is a control in name only* — and an arm no probe reaches has never been
// asked anything at all.
func TestTextFixtureProvenance(t *testing.T) {
	requireSuite(t)

	files := []string{
		"../text/lexer_test.go",
		"../text/match_test.go",
		"../text/parser_test.go",
		"../text/instr_test.go",
		"../text/label_test.go",
		"../text/annot_test.go",
	}

	type qcmd struct {
		src    string
		expect string
	}
	quotes := map[string]qcmd{}
	for _, p := range suitePaths(t) {
		s, err := ParseFile(p)
		if err != nil {
			continue
		}
		base := filepath.Base(p)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		nlines := strings.Count(string(raw), "\n") + 1
		for i, c := range s.Commands {
			if c.Source == nil {
				continue
			}
			// **A command occupies lines, and a citation may name any of them.** Keyed on
			// `c.Line` alone, seven of the newly-registered rows resolved to nothing — and not
			// because they had drifted: they cite the `(module quote …)` line *inside* an
			// `(assert_malformed` form, which is the more precise of the two citations,
			// naming the vector rather than the assertion wrapping it. A checker accepting
			// only the head line would have taught the fixtures to cite less precisely.
			//
			// The extent runs to the line before the next command, and only unclaimed lines
			// are filled, so a citation inside command N resolves to N and never to N-1's
			// tail. Bounding it by the command is what keeps it a check: an index over whole
			// files resolves nearly any integer — 169427 of the suite's 178222 lines fall
			// inside some command — and a lookup that always succeeds verifies nothing. The
			// verification is the source and expectation agreement below; the extent only
			// decides *which* command a row is claiming to be.
			end := nlines
			if i+1 < len(s.Commands) {
				end = s.Commands[i+1].Line - 1
			}
			q := qcmd{src: string(c.Source), expect: c.Expect}
			for n := c.Line; n <= end; n++ {
				k := fmt.Sprintf("%s:%d", base, n)
				if _, taken := quotes[k]; !taken {
					quotes[k] = q
				}
			}
		}
	}
	// A vacuity floor, not a non-nil check: an empty index makes every lookup below fail
	// loudly, but a *thin* one (say, one file parsed) would resolve a handful and silently
	// exempt the rest. annotations.wast and obsolete-keywords.wast alone hold hundreds.
	if len(quotes) < 100 {
		t.Fatalf("indexed only %d quote-form commands; the index is not reaching the suite",
			len(quotes))
	}

	var cited, exempt, sourcesChecked, expectsChecked, computed int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			lineNo := i + 1
			file, cl, ok := citedRow(line)
			if !ok {
				continue // prose, or a row with no citation
			}
			if strings.Contains(line, "synthetic") || strings.Contains(line, "derived") {
				exempt++
				continue
			}
			loc := file + ":" + cl
			cmd, ok := quotes[loc]
			if !ok {
				t.Errorf("%s:%d: citation %s resolves to no quote-form command; a citation "+
					"nobody can read is a claim, not a citation\n\t%s",
					f, lineNo, loc, strings.TrimSpace(line))
				continue
			}
			strs := goStringLiterals(line)
			// **The layout is detected, not assumed.** In the trailing-comment style the
			// citation is outside the literals, so the expectation is last and the source
			// second-from-last. In the field style the citation *is* the last literal, so
			// everything shifts by one — and a row may carry no expectation at all, its
			// table asserting one shared error in the loop body.
			//
			// Read positionally within each layout, never by search. The first draft of this
			// function searched for "whichever earlier literal the command contains", and a
			// control that picks its own input by whichever candidate passes will always find
			// one that passes: every row's leading name field is a substring of its own
			// source (`"current_memory"` in `(drop (current_memory))`), so the *name*
			// satisfied the containment check and the source was never compared at all. A
			// probe that corrupted only the source went green. Caught by perturbing a
			// different field than the probe that had already succeeded — *a control
			// falsified in one field is not falsified*.
			var want, vec string
			var haveWant, haveVec bool
			if citeComment.MatchString(line) {
				if len(strs) < 2 {
					t.Errorf("%s:%d: cited row holds %d string literals, want at least a "+
						"source and an expectation\n\t%s",
						f, lineNo, len(strs), strings.TrimSpace(line))
					continue
				}
				want, vec = strs[len(strs)-1], strs[len(strs)-2]
				haveWant, haveVec = true, true
			} else {
				// Field style: the citation is the last literal, so drop it and read the rest
				// by *shape* rather than by offset. These tables come in three widths —
				// `{src, cite}`, `{name, src, expect, cite}`, and `{name, <computed>, expect,
				// cite}`, where the immediate list is spliced from Go constants
				// (`sixteen + " 16"`) and no literal holds the vector at all. One fixed offset
				// cannot serve three widths: reading `instr_test.go` by offset put the
				// *expectation* in the source slot and compared "unexpected token" against an
				// `i8x16.shuffle` module, which is what the eight errors on the first run were.
				//
				// The rule is: **a wat source begins with `(`; the field after it, if any, is
				// the expectation.** That is a test on the field's shape, not on whether a
				// reading happens to pass — the distinction that matters, because the first
				// draft of the comment-style branch searched for "whichever earlier literal the
				// command contains" and a control choosing its own input by what passes will
				// always find something that passes. There, every row's name field was a
				// substring of its own source (`"current_memory"` in `(drop (current_memory))`),
				// so the name satisfied containment and the source was never compared; a probe
				// corrupting only the source went green. *A control falsified in one field is
				// not falsified.*
				//
				// **Only what a probe reaches.** Two earlier versions of this branch carried
				// machinery for widths no fixture has: first a `case` for
				// `{name, src, expect, cite}`, then a scan that also read the field *after* the
				// source as an expectation. A `panic()` in each proved both unreachable — every
				// field-style row today is either `{src, cite}` (source, table-shared
				// expectation) or `{name, <computed>, expect, cite}` (no source literal). An
				// unexercised arm inside a control is scaffolding indistinguishable, on the
				// board, from a check, so it is not kept against a future fixture that may never
				// arrive; the width is covered by the ceiling below noticing it, which is a
				// mechanism that *does* run. *Unreachability is a grave only when it's silent* —
				// this is the note.
				fields := strs[:len(strs)-1]
				if len(fields) == 0 {
					t.Errorf("%s:%d: cited row holds nothing but its citation\n\t%s",
						f, lineNo, strings.TrimSpace(line))
					continue
				}
				for _, s := range fields {
					if strings.HasPrefix(strings.TrimSpace(s), "(") {
						vec, haveVec = s, true
						break
					}
				}
				if !haveVec {
					// **The vector is computed, so only its expectation is checkable.** There
					// is no literal to compare, and inventing one means re-assembling the
					// fixture's own arithmetic here — a second implementation of the thing
					// under test. Counted and ceilinged, not silently skipped: see below.
					want, haveWant = fields[len(fields)-1], true
					computed++
				}
			}
			// The two checks are independent and each runs when its field is present. A row
			// with a source and no expectation is still checked on its source — requiring the
			// pair would have silently exempted every row whose table shares one error, and an
			// exemption granted by a layout mismatch is invisible.
			if haveWant {
				switch {
				case want == "":
					// An empty expectation is the row asserting the vector lexes clean, so the
					// cited command must not be asserting an error. Checked explicitly because
					// strings.Contains(anything, "") is true: leaving this to the substring
					// comparison below would make every clean-lexing row agree with every
					// assert_malformed in the suite.
					if cmd.expect != "" {
						t.Errorf("%s:%d: row expects the vector to lex clean but %s asserts %q",
							f, lineNo, loc, cmd.expect)
					}
				case cmd.expect == "":
					t.Errorf("%s:%d: row expects %q but %s asserts nothing",
						f, lineNo, want, loc)
				case !strings.Contains(cmd.expect, want) && !strings.Contains(want, cmd.expect):
					t.Errorf("%s:%d: row expects %q but %s asserts %q — the fixture and its "+
						"citation disagree about which error the suite wants",
						f, lineNo, want, loc, cmd.expect)
				}
				expectsChecked++
			}
			if haveVec {
				if vec == "" {
					t.Errorf("%s:%d: cited row's source field is empty; there is nothing to "+
						"compare against %s", f, lineNo, loc)
					continue
				}
				// **Containment in either direction.** A fixture is legitimately a fragment of
				// its cited command — a lexer row pinning one lexeme — and legitimately a
				// *wrapper* around it, because a quote module's body is bare (`(func …)`) while
				// a fixture handed to ReadModule needs `(module (func …))`. Nine of
				// parser_test.go's rows are the second shape, and requiring one direction
				// failed all nine on the first run. Neither direction is the weaker check: what
				// is asserted is that one text appears verbatim inside the other, so a changed
				// token, index or escape still fails.
				//
				// Verbatim, with no whitespace normalization. A draft stripped spaces on both
				// sides, reasoning that a quote module concatenated from string literals has no
				// space at the seams while a fixture on one line does — plausible, and a probe
				// showed **no row needs it**: the bidirectional test already covers every
				// current fixture exactly. A relaxation nothing reaches only weakens the next
				// comparison, so it is not carried on the strength of an argument. *Print it,
				// don't reason about it.*
				if !strings.Contains(cmd.src, vec) && !strings.Contains(vec, cmd.src) {
					t.Errorf("%s:%d: row source %q neither appears in nor contains %s; the "+
						"vector was transcribed from somewhere else, or the citation "+
						"drifted\n\tsuite source: %q", f, lineNo, vec, loc, cmd.src)
					continue
				}
				sourcesChecked++
			}
			cited++
		}
	}
	if cited == 0 {
		t.Fatal("no text citations checked — the row regex has drifted from the fixtures, " +
			"and a comparison against an empty set succeeds")
	}
	// Floored separately, because one number cannot show that half the mechanism went dead:
	// a layout-detection bug that read every row as source-only would leave `cited` healthy
	// while `expectsChecked` collapsed, and the reverse for a `vec` that stopped resolving.
	if sourcesChecked < 20 || expectsChecked < 20 {
		t.Errorf("checked %d sources and %d expectations across %d cited rows; both halves of "+
			"the comparison must stay live, and a collapse in one is invisible in the total",
			sourcesChecked, expectsChecked, cited)
	}
	// A row whose vector is computed is checked on its expectation only, which is a real
	// weakening and so is bounded rather than merely counted: if this grows, the fixture style
	// is drifting away from literal vectors and the source half of the rule is quietly
	// shrinking. *A precondition that excuses a gate is licensed at one place, or it is a hole.*
	//
	// **8 → 20 (#76), raised deliberately and itemized.** Both tables in the count are
	// `instr_test.go`'s, and both are one production read through a *shared prefix* rather than
	// per-row modules: 8 rows of `i8x16.shuffle` immediates (`sixteen + " 16"`) and 12 rows of
	// `lane_imms` arms (`"1 offset=0 align=1 1"`). The field is a literal in the second table —
	// what makes the vector uncomputable is that no single literal holds it, since the module is
	// `prefix + field + suffix`. Spelling those 12 as whole modules would mean 12 copies of a
	// 110-character wrapper differing in one immediate, which buys the source check by making the
	// table stop reading as "five arms of one production". The trade is stated rather than taken
	// silently: these 20 rows are checked on their expectation and on nothing else.
	if computed > 20 {
		t.Errorf("%d cited rows build their vector from Go expressions, so only their "+
			"expectation is checked; the ceiling is 20 (instr_test.go's two immediate tables: "+
			"8 shuffle rows and 12 lane_imms rows). Spell new vectors as literals, or raise "+
			"this deliberately", computed)
	}
	t.Logf("verified %d cited text fixtures (%d sources, %d expectations, %d expectation-only "+
		"because the vector is computed), %d exempt as synthetic or derived",
		cited, sourcesChecked, expectsChecked, computed, exempt)
}

// goStringLiterals extracts the interpreted string literals from one line of Go source,
// respecting backslash escapes — a wat fixture is full of them, and a naive split on `"`
// would cut `"(func $\"\\ef\")"` in half and then compare the fragments.
func goStringLiterals(line string) []string {
	var out []string
	for i := 0; i < len(line); i++ {
		if line[i] == '`' { // raw string: no escapes
			if j := strings.IndexByte(line[i+1:], '`'); j >= 0 {
				out = append(out, line[i+1:i+1+j])
				i += j + 1
			}
			continue
		}
		if line[i] != '"' {
			continue
		}
		var b strings.Builder
		i++
		for i < len(line) && line[i] != '"' {
			if line[i] == '\\' && i+1 < len(line) {
				switch line[i+1] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case '\\':
					b.WriteByte('\\')
				case '"':
					b.WriteByte('"')
				case 'x':
					// \xNN is decoded, and this is the difference between a checker and a
					// formality: every annotation fixture pins a *byte*, so a checker
					// comparing escape text against the suite's real byte would fail on all
					// of them and get exempted into uselessness — after which a genuinely
					// drifted citation is indistinguishable from the known limitation.
					// Decoding turned two blanket failures into one pass and one real finding.
					if i+3 < len(line) {
						if v, err := strconv.ParseUint(line[i+2:i+4], 16, 8); err == nil {
							b.WriteByte(byte(v))
							i += 4
							continue
						}
					}
					b.WriteByte('\\')
					b.WriteByte('x')
					i += 2
					continue
				default:
					// \uNNNN and friends: kept verbatim. No cited row uses them today, and a
					// wrong guess would be a silent mis-comparison rather than a visible one.
					b.WriteByte('\\')
					b.WriteByte(line[i+1])
				}
				i += 2
				continue
			}
			b.WriteByte(line[i])
			i++
		}
		out = append(out, b.String())
	}
	return out
}

// suiteImages indexes every module image in the suite by its bytes, and also by
// "file:line", so citations resolve in both directions.
func suiteImages(t *testing.T) map[string][]string {
	t.Helper()
	paths := suitePaths(t)
	idx := map[string][]string{}
	for _, p := range paths {
		s, err := ParseFile(p)
		if err != nil {
			continue // parse failures are TestParseEverySuiteFile's business
		}
		for _, c := range s.Commands {
			if c.Module == nil {
				continue
			}
			loc := fmt.Sprintf("%s:%d", filepath.Base(p), c.Line)
			idx[string(c.Module)] = append(idx[string(c.Module)], loc)
			idx["@"+loc] = []string{string(c.Module)}
		}
	}
	return idx
}

func suiteLine(idx map[string][]string, file, line string) ([]byte, bool) {
	v, ok := idx["@"+file+":"+line]
	if !ok || len(v) == 0 {
		return nil, false
	}
	return []byte(v[0]), true
}

func parseHexBytes(s string) []byte {
	var out []byte
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(f, "0x"), 16, 8)
		if err != nil {
			return nil
		}
		out = append(out, byte(v))
	}
	return out
}
