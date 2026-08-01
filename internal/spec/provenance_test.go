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

	var checked, checkedFragments, synthetic int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			lineNo := i + 1
			m := cite.FindStringSubmatch(line)
			if m == nil {
				// No citation. Flag lines that hold a preamble-shaped literal
				// anyway — an uncited vector is exactly what this test hunts.
				if lm := lit.FindStringSubmatch(line); lm != nil {
					b := parseHexBytes(lm[1])
					if len(b) >= 8 && !strings.Contains(line, "synthetic") {
						if _, ok := suite[string(b)]; !ok {
							t.Errorf("%s:%d: uncited %d-byte vector % x not found in the suite;\n"+
								"\tadd a `// <file>.wast:N` citation, or mark it `synthetic` with a reason",
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
	t.Logf("verified %d cited module images, %d cited fragments, %d declared synthetic",
		checked, checkedFragments, synthetic)
}

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

	files := []string{
		"../binary/binary_test.go",
		"../binary/sections_test.go",
		"../binary/constexpr_test.go",
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
				if _, ok := suiteSourceLine(t, m[1], n); !ok {
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

// TestEveryFixtureFileIsChecked guards the guard.
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
		"../text/lexer_test.go": true,
		"../text/match_test.go": true,
	}

	paths, err := filepath.Glob("../*/*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	cite := regexp.MustCompile(`//\s*[a-zA-Z0-9_.-]+\.wast:\d+`)
	for _, p := range paths {
		if strings.HasPrefix(p, "../spec/") {
			continue // this package's own tests hold no engine fixtures
		}
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if !cite.Match(src) {
			continue
		}
		if !checked[p] {
			t.Errorf("%s carries suite citations but is not in TestFixtureProvenance's file list; its citations are unverified", p)
		}
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
//  3. the fixture's `src` is *contained in* the command's assembled source. Containment
//     rather than equality, because a lexer fixture is legitimately a fragment: a row
//     pinning one lexeme need not carry the whole module. A fragment appearing nowhere in
//     the cited command is a transcription error.
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
// Falsified before trusted: perturbing a cited line number fails (1) and (3); swapping a
// row's expected string for a neighbour's fails (2); blanking the row regex or the quote
// index trips the vacuity floors rather than passing green.
func TestTextFixtureProvenance(t *testing.T) {
	requireSuite(t)

	files := []string{
		"../text/lexer_test.go",
		"../text/match_test.go",
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
		for _, c := range s.Commands {
			if c.Source == nil {
				continue
			}
			quotes[fmt.Sprintf("%s:%d", base, c.Line)] = qcmd{src: string(c.Source), expect: c.Expect}
		}
	}
	// A vacuity floor, not a non-nil check: an empty index makes every lookup below fail
	// loudly, but a *thin* one (say, one file parsed) would resolve a handful and silently
	// exempt the rest. annotations.wast and obsolete-keywords.wast alone hold hundreds.
	if len(quotes) < 100 {
		t.Fatalf("indexed only %d quote-form commands; the index is not reaching the suite",
			len(quotes))
	}

	cite := regexp.MustCompile(`//\s*([a-zA-Z0-9_.-]+\.wast):(\d+)`)
	// A fixture row is a composite literal holding at least one interpreted string.
	row := regexp.MustCompile(`\{.*".*".*\}`)

	var cited, exempt int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			lineNo := i + 1
			m := cite.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if !row.MatchString(line) {
				continue // a citation in prose, not on a fixture row
			}
			if strings.Contains(line, "synthetic") || strings.Contains(line, "derived") {
				exempt++
				continue
			}
			loc := m[1] + ":" + m[2]
			cmd, ok := quotes[loc]
			if !ok {
				t.Errorf("%s:%d: citation %s resolves to no quote-form command; a citation "+
					"nobody can read is a claim, not a citation\n\t%s",
					f, lineNo, loc, strings.TrimSpace(line))
				continue
			}
			strs := goStringLiterals(line)
			if len(strs) < 2 {
				t.Errorf("%s:%d: cited row holds %d string literals, want at least a source "+
					"and an expectation\n\t%s", f, lineNo, len(strs), strings.TrimSpace(line))
				continue
			}
			// Both fields are read *positionally* — the expectation is the row's last
			// literal, the source the one before it. The first draft searched for
			// "whichever earlier literal the command contains", and a control that picks
			// its own input by whichever candidate passes will always find one that
			// passes: every row's leading name field is a substring of its own source
			// (`"current_memory"` in `(drop (current_memory))`), so the *name* satisfied
			// the containment check and the source was never compared at all. A probe
			// that corrupted only the source went green. Caught by perturbing a different
			// field than the probe that had already succeeded — a control falsified in
			// one field is not falsified.
			want, src := strs[len(strs)-1], strs[len(strs)-2]
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
			if src == "" {
				t.Errorf("%s:%d: cited row's source field is empty; there is nothing to "+
					"compare against %s", f, lineNo, loc)
				continue
			}
			if !strings.Contains(cmd.src, src) {
				t.Errorf("%s:%d: row source %q does not appear in %s; the vector was "+
					"transcribed from somewhere else, or the citation drifted\n\tsuite source: %q",
					f, lineNo, src, loc, cmd.src)
				continue
			}
			cited++
		}
	}
	if cited == 0 {
		t.Fatal("no text citations checked — the row regex has drifted from the fixtures, " +
			"and a comparison against an empty set succeeds")
	}
	t.Logf("verified %d cited text fixtures, %d exempt as synthetic or derived", cited, exempt)
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
