package keywordgen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// refPath is the vendored authority, from this package's directory.
// refPath names the vendored authority; see opcodegen's copy for why it is a name and not
// a path.
var refPath = testenv.RefLexerMLL

func refSource(tb testing.TB) string {
	tb.Helper()
	return testenv.RequireSpecRef(tb, refPath)
}

// requireRef asserts every authority the composition reads is present, derived from the pin
// set rather than named.
//
// Requiring only the *core* lexer would let the drift check run against a composition that
// silently lost its overlay — and the comparison would then be a correctly-composed committed
// file against a 70-keyword-short rebuild, reported as drift in the committed file. The pin
// set is the domain here for the same reason it is BuildFromPins': a pin added there is
// covered on arrival.
func requireRef(tb testing.TB) {
	tb.Helper()
	for _, pin := range testenv.RefPins() {
		path, ok := LexerFor(pin)
		if !ok {
			continue
		}
		testenv.RequireSpecRef(tb, path)
	}
}

// TestExtractMatchesMeasuredShape pins the counts, and the numbers are the point.
//
// Not a floor — the floor is ErrVacuous's business, and a floor cannot tell 589 from 590.
// This asserts the exact shape measured at bdd7164, so an upstream revision that adds a
// mnemonic fails here with a diff rather than passing quietly with a bigger table. The pin
// is a *revision* pin, so an exact count is the honest assertion: nothing about lexer.mll
// can change without the SHA changing.
//
// 589 was counted against the file by independent enumeration before this package was
// written (`grep -c '^\s*| "'` over the block's line range, cross-checked by `sort -u` for
// uniqueness), not read off the extractor's own output — the failure that would make the
// two agree is exactly the one this test exists to catch.
func TestExtractMatchesMeasuredShape(t *testing.T) {
	tab, err := Extract(refSource(t), "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(tab.Arms) != 589 {
		t.Errorf("keywords: got %d, want 589", len(tab.Arms))
	}
	kinds := map[Kind]bool{}
	for _, a := range tab.Arms {
		kinds[a.Kind] = true
	}
	if len(kinds) != 173 {
		t.Errorf("distinct token kinds: got %d, want 173", len(kinds))
	}
	if tab.fallthroughLine != 809 {
		t.Errorf("fallthrough line: got %d, want 809 (`| _ -> unknown lexbuf`)", tab.fallthroughLine)
	}
}

// TestTheElevenObsoleteMnemonicsAreAbsent is the recon's central claim as a control, and
// it is a claim about *absence* — which is the strongest evidence available that the
// extractor read the grammar rather than dumping every string in the file.
//
// The eleven are what obsolete-keywords.wast asserts are no longer operators. Nine of them
// (`get_local`, `anyfunc`, …) are keyword-shaped and would be in this table if the
// extractor were scraping string literals anywhere outside the keyword block — the lexer's
// git history is not in the file, but a scraper that wandered into `and annot start` or
// into the parser's token declarations would pick up shapes the keyword block does not
// have. Absence here is therefore not a tautology; it is the block's extent being right.
//
// The remaining two shapes (`i32.wrap/i64` and friends) could never appear regardless,
// because `/` is outside the `keyword` production — which checkShape asserts separately.
// They are listed anyway, because the *vector* is what this test is named for, and a list
// that silently omitted two of eleven would be a test whose name overclaims its coverage
// (#34).
//
// Falsified by adding "get_local" to a synthetic table and watching the loop fail.
func TestTheElevenObsoleteMnemonicsAreAbsent(t *testing.T) {
	// Derived from testdata/spec/obsolete-keywords.wast, whose eleven assert_malformed
	// vectors each expect `unknown operator <mnemonic>`. Cited rather than synthetic:
	// the file is the vendored suite's, and TestObsoleteMnemonicsMatchTheVector below
	// checks the list against it rather than trusting this transcription.
	obsolete := []string{
		"current_memory", "grow_memory", "get_local", "set_local", "tee_local",
		"anyfunc", "get_global", "set_global",
		"i32.wrap/i64", "i32.trunc_s:sat/f32", "f32x4.convert_s/i32x4",
	}
	tab, err := Extract(refSource(t), "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	have := map[string]int{}
	for _, a := range tab.Arms {
		have[a.Keyword] = a.Line
	}
	for _, kw := range obsolete {
		if line, ok := have[kw]; ok {
			t.Errorf("obsolete mnemonic %q is in the extracted table (lexer.mll:%d) — the suite "+
				"asserts it is not an operator, so a table containing it would accept a module "+
				"the spec calls malformed", kw, line)
		}
	}
	// Vacuity floor on the control itself: an empty `obsolete` list would pass the loop
	// above while asserting nothing, which is the comparison-against-an-empty-set defect
	// pointed at a test's own inputs.
	if len(obsolete) != 11 {
		t.Fatalf("the obsolete list holds %d mnemonics, want 11 — this test's name is a claim "+
			"about eleven vectors", len(obsolete))
	}
}

// TestObsoleteMnemonicsMatchTheVector checks the list above against the suite rather than
// trusting it, because *fixtures cite the suite, and the citations are checked*.
//
// A hand-typed list of eleven strings is exactly the artifact this project has measured
// itself getting wrong: seven wrong citations in twelve hand-written items. So the vector
// file is parsed for its `unknown operator <mnemonic>` expectations and the two sets are
// compared — which also makes the list self-maintaining if upstream adds a twelfth.
func TestObsoleteMnemonicsMatchTheVector(t *testing.T) {
	const rel = "testdata/spec/obsolete-keywords.wast"
	b := testenv.RequireSuiteFile(t, filepath.Base(rel))
	re := regexp.MustCompile(`"unknown operator ([^"]+)"`)
	ms := re.FindAllStringSubmatch(string(b), -1)
	if len(ms) != 11 {
		t.Fatalf("%s holds %d `unknown operator <mnemonic>` expectations, want 11 — the vector "+
			"moved, so the list in TestTheElevenObsoleteMnemonicsAreAbsent is stale", rel, len(ms))
	}
	fromVector := map[string]bool{}
	for _, m := range ms {
		fromVector[m[1]] = true
	}
	for _, kw := range []string{
		"current_memory", "grow_memory", "get_local", "set_local", "tee_local",
		"anyfunc", "get_global", "set_global",
		"i32.wrap/i64", "i32.trunc_s:sat/f32", "f32x4.convert_s/i32x4",
	} {
		if !fromVector[kw] {
			t.Errorf("%q is in this package's obsolete list but not in %s", kw, rel)
		}
		delete(fromVector, kw)
	}
	for kw := range fromVector {
		t.Errorf("%s expects `unknown operator %s` and the list does not name it", rel, kw)
	}
}

// TestSlashSplitsTheTwoUnknownOperatorPaths pins the grammar fact three of the eleven
// vectors turn on, and it reads the fact out of the authority rather than restating it.
//
// The fact: `/` is in `symbol` (lexer.mll:66, hence in `idchar`, hence in `reserved`) and
// is *not* in the `keyword` charset (lexer.mll:111). So `i32.wrap/i64` cannot be a keyword
// arm — the keyword rule stops at `i32.wrap`, eight characters, while `reserved` matches all
// twelve — and ocamllex's longest-match rule sends the whole mnemonic to `unknown operator`
// through the second producer (lexer.mll:839) rather than the first (:809).
//
// That is why this is a control and not a comment: a port that lexed keyword-first-wins, or
// that stopped at the first non-keyword character, would report `unknown operator i32.wrap`.
// The expected string for those three vectors *contains the mnemonic*, so the suite would
// convict — but only once a lexer exists. Until then, this test is what holds the premise,
// and it fails if upstream ever moves `/` between the two charsets.
//
// **Written this way because the obvious version could not fail.** The first draft looped
// over the extracted table asserting no keyword holds a symbol — and `Extract` rejects such
// an arm through checkShape *before returning*, so the loop could never see one. A green
// that survives the bug it names. The falsifiable question is not "is the table clean" but
// "do the two charsets still disagree about `/`", which is answered from lexer.mll's text.
func TestSlashSplitsTheTwoUnknownOperatorPaths(t *testing.T) {
	src := refSource(t)

	def := func(name string) string {
		re := regexp.MustCompile(`(?m)^let ` + name + ` =?\s*(.*)$`)
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("lexer.mll no longer defines `let %s` — the production this test is about "+
				"was renamed, so the charset claim is unverified rather than false", name)
		}
		body := m[1]
		if strings.TrimSpace(body) == "" {
			// `let symbol =` puts its charset on the following line.
			rest := src[strings.Index(src, m[0])+len(m[0]):]
			body = strings.SplitN(strings.TrimLeft(rest, "\n"), "\n", 2)[0]
		}
		return body
	}

	keyword, symbol := def("keyword"), def("symbol")
	if strings.Contains(keyword, `'/'`) {
		t.Errorf("`/` has entered the `keyword` charset (%s) — `i32.wrap/i64` would now lex as a "+
			"keyword, so the three reserved-path vectors change meaning", keyword)
	}
	if !strings.Contains(symbol, `'/'`) {
		t.Errorf("`/` has left the `symbol` charset (%s) — it is no longer in `idchar`, so "+
			"`i32.wrap/i64` no longer lexes as `reserved` and the whole-mnemonic lexeme the "+
			"three vectors expect is not produced", symbol)
	}
	// The keyword charset must still hold the characters the *other* eight mnemonics
	// need, or those vectors take a different path than the recon measured. Asserted
	// rather than assumed: this is the half that decides 8 of the 11.
	for _, want := range []string{`'_'`, `'.'`, `':'`} {
		if !strings.Contains(keyword, want) {
			t.Errorf("`keyword` no longer holds %s (%s) — the eight fallthrough mnemonics "+
				"(get_local, i32.trunc_s:sat/..., …) may no longer reach lexer.mll:809", want, keyword)
		}
	}
	// Both producers of `unknown operator` must still exist, since the partition is
	// between them. A rename would leave the charset assertions above true and the
	// conclusion drawn from them false.
	for _, producer := range []string{"| _ -> unknown lexbuf", "| reserved { unknown lexbuf }"} {
		if !strings.Contains(src, producer) {
			t.Errorf("lexer.mll no longer holds %q — the two-producer partition this test "+
				"reasons about has changed", producer)
		}
	}
}

// TestVacuityIsCaughtByTheNamedMechanism is opcodegen's condition 1 in this grammar, and
// it is the control errUnrecognized cannot be.
//
// The failure mode: an upstream refactor the parser does not recognize yields zero arms and
// zero unrecognized lines, the generator writes an empty table, and the drift check
// compares empty against empty and agrees. A green with the mechanism fully intact,
// asserting nothing — grave #407 relocated into a code generator.
//
// Each case names *which* control must catch it, and the check is not errors.Is: both
// controls report ErrVacuous, so errors.Is cannot tell them apart, and a case caught by
// the wrong one would leave the mechanism it names untested while the pass count looked
// right (#34, and opcodegen's own experience of exactly this).
//
// Falsified by *inducing* each case against the real source, and cross-checked by
// disarming the floor (Floor = 0 locally) to confirm which cases survive.
func TestVacuityIsCaughtByTheNamedMechanism(t *testing.T) {
	src := refSource(t)

	const (
		byFloor  = "floor is" // checkFloor's message
		byLocate = "could not locate"
	)
	cases := []struct {
		name string
		by   string
		mung func(string) string
	}{
		{
			// The block is still there; every arm is unrecognizable. This is the case
			// that motivates the whole control: no arm *looks* like an arm, so
			// errUnrecognized cannot fire and a count is the only witness.
			name: "arms rewritten away, block intact",
			by:   byFloor,
			mung: func(s string) string {
				lo := strings.Index(s, "{ match s with")
				hi := strings.Index(s, "| _ -> unknown lexbuf")
				return s[:lo] + strings.ReplaceAll(s[lo:hi], `| "`, "(* x *) Q") + s[hi:]
			},
		},
		{
			// An upstream rename of the lexer's entry rule, or of the `keyword`
			// production it dispatches on. Caught by the locate check, *not* by the
			// floor — the block cannot be found at all, so there is nothing to count.
			name: "keyword dispatch renamed",
			by:   byLocate,
			mung: func(s string) string {
				return strings.Replace(s, "| keyword as s", "| mnemonic as s", 1)
			},
		},
		{
			// The two-line head is why this case exists: matching `| keyword as s`
			// alone would locate a block whose body is not a match, and the extractor
			// would then read the rest of the file as arms. Removing only the second
			// line must still fail to locate.
			name: "match line changed, head line intact",
			by:   byLocate,
			mung: func(s string) string {
				return strings.Replace(s, "{ match s with", "{ match String.lowercase_ascii s with", 1)
			},
		},
		{
			// A truncated fetch, which testenv's byte floor also guards — two controls,
			// two diagnoses, and this proves the extractor does not depend on the other
			// having run. Caught by locate: the truncation removes the fallthrough that
			// terminates the block.
			name: "source truncated mid-block",
			by:   byLocate,
			mung: func(s string) string {
				i := strings.Index(s, "{ match s with")
				return s[:i+400]
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			munged := c.mung(src)
			if munged == src {
				t.Fatal("mutation did not apply: the anchor changed upstream, so this case " +
					"is asserting nothing about a source it did not modify")
			}
			tab, err := Extract(munged, "test")
			n := -1
			if tab != nil {
				n = len(tab.Arms)
			}
			if !errors.Is(err, ErrVacuous) {
				t.Fatalf("got err=%v (keywords=%d), want ErrVacuous — a broken source that yields "+
					"a small or empty table passes the drift check against an empty commit", err, n)
			}
			if !strings.Contains(err.Error(), c.by) {
				t.Fatalf("caught by the wrong mechanism: want a message containing %q, got %v\n"+
					"\tboth controls report ErrVacuous, so this case is not exercising the one it names",
					c.by, err)
			}
		})
	}
}

// TestUnrecognizedArmIsAnErrorNotASkip pins the other half: a line that *is* an arm and
// cannot be parsed must break the build, never be omitted.
//
// This is the property that makes machine extraction trustworthy where a careful reading
// is not — the failure mode is inverted from silent undercoverage to a loud stop. Two
// shapes, because the arm has two halves that fail independently: an unreadable head and
// an unreadable right-hand side.
func TestUnrecognizedArmIsAnErrorNotASkip(t *testing.T) {
	src := refSource(t)
	cases := []struct {
		name     string
		from, to string
	}{
		{
			// A head shape the grammar does not cover: an or-pattern. Real OCaml an
			// upstream author could plausibly write, not a nonsense string — and note
			// that a lazier extractor would read this as the single keyword "nop" and
			// silently lose "no_op".
			name: "or-pattern head",
			from: `| "nop" -> NOP`,
			to:   `| "nop" | "no_op" -> NOP`,
		},
		{
			// A right-hand side with no token constructor. Emitting the arm with an
			// empty Kind would claim this keyword lexes to nothing.
			name: "no token constructor",
			from: `| "nop" -> NOP`,
			to:   `| "nop" -> ignore s`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			munged := strings.Replace(src, c.from, c.to, 1)
			if munged == src {
				t.Fatalf("mutation did not apply: anchor %q changed upstream", c.from)
			}
			if _, err := Extract(munged, "test"); !errors.Is(err, errUnrecognized) {
				t.Fatalf("got err=%v, want errUnrecognized: an arm the extractor cannot read must "+
					"stop the build, not shrink the table by one keyword", err)
			}
		})
	}
}

// TestDuplicateKeywordIsAnError pins the check that would otherwise be a silent last-wins
// map entry — and note the divergence it guards: ocamllex resolves a duplicated arm by
// first-rule-wins, a Go map by insertion order, and no vector could show the difference.
func TestDuplicateKeywordIsAnError(t *testing.T) {
	src := refSource(t)
	munged := strings.Replace(src, `| "nop" -> NOP`, "| \"nop\" -> NOP\n      | \"nop\" -> UNREACHABLE", 1)
	if munged == src {
		t.Fatal("mutation did not apply: the anchor line changed upstream")
	}
	if _, err := Extract(munged, "test"); !errors.Is(err, errUnrecognized) {
		t.Fatalf("got err=%v, want errUnrecognized for a duplicated keyword", err)
	}
}

// TestShapeCheckRejectsAnUnreachableArm falsifies checkShape by feeding it the thing it
// forbids: an arm head the `keyword` production cannot deliver.
//
// Induced against the real source, so the case proves the check runs in the real pipeline
// rather than only over a synthetic table.
func TestShapeCheckRejectsAnUnreachableArm(t *testing.T) {
	src := refSource(t)
	// `i32.wrap/i64` is the natural example: it is exactly what an author porting the
	// pre-720 mnemonics back would type, and `/` is outside the charset, so the arm
	// would be dead code in the reference and an unreachable row in our table.
	munged := strings.Replace(src, `| "nop" -> NOP`, "| \"nop\" -> NOP\n      | \"i32.wrap/i64\" -> CONVERT i32_wrap_i64", 1)
	if munged == src {
		t.Fatal("mutation did not apply: the anchor line changed upstream")
	}
	_, err := Extract(munged, "test")
	if !errors.Is(err, errUnrecognized) {
		t.Fatalf("got err=%v, want errUnrecognized for an arm outside the `keyword` production", err)
	}
	if !strings.Contains(err.Error(), "does not match the `keyword` production") {
		t.Errorf("caught by the wrong mechanism: %v", err)
	}
}

// TestEmitIsDeterministic guards the drift check's own premise.
//
// The check compares generated text against a committed file, so a generator whose output
// varies between runs turns that comparison into a coin flip — Go's map iteration order
// would do it, and this package builds a map of kinds for its header count. Three
// extractions, byte-identical, or the control below is measuring noise.
func TestEmitIsDeterministic(t *testing.T) {
	src := refSource(t)
	var prev string
	for i := range 3 {
		tab, err := Extract(src, "test")
		if err != nil {
			t.Fatalf("Extract: %v", err)
		}
		got, err := tab.Emit()
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		if i > 0 && got != prev {
			t.Fatal("Emit is not deterministic across runs; the drift check would be a coin flip")
		}
		prev = got
	}
}

// TestEmitRejectsAKeywordWithNoKind proves the coupling fails loudly rather than emitting
// a row that reads as "this keyword lexes to nothing".
func TestEmitRejectsAKeywordWithNoKind(t *testing.T) {
	tab := &Table{
		Sources: []Source{{Path: CoreAuthority, SHA: "test"}},
		Arms:    []Arm{{Keyword: "nop", Line: 1}},
	}
	if _, err := tab.Emit(); err == nil {
		t.Fatal("Emit accepted a keyword with no token kind; the emitted row would read as a " +
			"keyword that is not a token, which is the reject-direction contract inverted")
	}
}

// TestCommittedTableMatchesTheReference is the drift control: drift is a build failure, not
// a diff nobody ordered.
//
// It re-runs the extraction against the pinned reference and compares the result against
// the committed file byte for byte. It cannot live in `make check`, which must pass on a
// fresh clone with nothing vendored; it runs in `make keyword-drift` and in CI, and it
// refuses to run without the reference rather than skipping, because a drift check that
// skips reports agreement with an authority it never read.
// **It builds through BuildFromPins rather than extracting one authority**, and the reason is
// a failure this control produced on itself: composed against two pins by the generator and
// one by the check, it reported drift in a table that was correct. A drift check that composes
// differently from the generator is measuring the distance between two copies of the
// composition, and the committed artifact is the innocent party.
func TestCommittedTableMatchesTheReference(t *testing.T) {
	requireRef(t)
	tab, err := BuildFromPins()
	if err != nil {
		t.Fatalf("BuildFromPins: %v", err)
	}
	if len(tab.Sources) < 2 {
		t.Fatalf("composed from %d authorities, want >=2; the pin set is the derived domain and "+
			"a single-source table would agree with a committed file that is 70 keywords short",
			len(tab.Sources))
	}
	sha := tab.Sources[0].SHA
	want, err := tab.Emit()
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	path, err := gen.FromRoot(Output)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantFmt, err := gen.GofmtSource(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotB) != wantFmt {
		t.Errorf("internal/text/keywords.go disagrees with the reference at %s.\n"+
			"Regenerate with: make keywords\n"+
			"committed %d bytes, extracted %d bytes", sha, len(gotB), len(wantFmt))
		if d := firstDiff(string(gotB), wantFmt); d != "" {
			t.Errorf("first difference:\n%s", d)
		}
	}
}

func firstDiff(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(as), len(bs)) {
		x, y := "<eof>", "<eof>"
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return fmt.Sprintf("  line %d committed: %s\n  line %d extracted: %s", i+1, x, i+1, y)
		}
	}
	return ""
}
