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
	files := []string{
		"../binary/binary_test.go",
	}

	// A citation is a comment of the form `// <file>.wast:<line>` anywhere on the
	// line that also holds the byte literal.
	cite := regexp.MustCompile(`//\s*([a-zA-Z0-9_.-]+\.wast):(\d+)`)
	// A byte-slice literal: {0x00, 0x61, ...}. Braces, hex bytes, commas only.
	lit := regexp.MustCompile(`\{((?:\s*0x[0-9a-fA-F]{2}\s*,?)*)\}`)

	var checked, synthetic int
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
			got, ok := suiteLine(suite, m[1], m[2])
			if !ok {
				t.Errorf("%s:%d: citation %s:%s names no module image in the suite", f, lineNo, m[1], m[2])
				continue
			}
			if !bytes.Equal(want, got) {
				t.Errorf("%s:%d: fixture disagrees with its citation %s:%s\n\tfixture: % x\n\tsuite:   % x",
					f, lineNo, m[1], m[2], want, got)
				continue
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no citations checked — the regexes have drifted from the fixtures")
	}
	t.Logf("verified %d cited vectors, %d declared synthetic", checked, synthetic)
}

// suiteImages indexes every module image in the suite by its bytes, and also by
// "file:line", so citations resolve in both directions.
func suiteImages(t *testing.T) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
	if err != nil || len(paths) == 0 {
		t.Skip("no .wast files found")
	}
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
