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
func TestEveryFixtureFileIsChecked(t *testing.T) {
	checked := map[string]bool{
		"../binary/binary_test.go":    true,
		"../binary/sections_test.go":  true,
		"../binary/constexpr_test.go": true,
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
