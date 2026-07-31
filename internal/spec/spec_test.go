package spec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// suiteDir is where make spec-tests vendors the upstream suite. Gitignored;
// tests skip rather than fail when it is absent, so a fresh clone is green
// before the fetch.
const suiteDir = "../../testdata/spec"

func decode(image []byte) error {
	_, err := binary.DecodeModule(image)
	return err
}

// isGated asks the engine, rather than reading its error text. The taxonomy is
// the engine's to define; a substring test here would be the harness guessing at
// the thing it exists to check.
func isGated(err error) bool { return errors.Is(err, binary.ErrFeatureDisabled) }

// run scores a script with gate declines separated from verdicts.
func run(s *Script) *Result { return s.RunGated(decode, isGated) }

func requireSuite(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(suiteDir); err != nil {
		t.Skip("spec suite not vendored; run: make spec-tests")
	}
}

// TestBinaryWast is the first real suite number: binary.wast is 107
// assert_malformed forms and nothing else, so phase 1 can execute all of it.
//
// This test reports; it does not gate. Failures are expected while the decoder
// is incomplete, and their buckets are the work plan (issues #5, #6). A hard
// pass-count floor guards against regression without pretending the suite is
// green.
func TestBinaryWast(t *testing.T) {
	requireSuite(t)
	s, err := ParseFile(filepath.Join(suiteDir, "binary.wast"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := run(s)
	t.Log("\n" + r.Board())

	if r.Unsupported != 0 {
		t.Errorf("binary.wast should be fully parseable by phase 1, got %d unsupported", r.Unsupported)
	}
	if r.Total() == 0 {
		t.Fatal("no assertions executed — harness is not wired")
	}
	// Regression floor, raised as decoder work lands; never lowered. 49 when the
	// harness first ran, 84 after section order and cross-section counts (#6), 104
	// after the section payload grammars (#5).
	const floor = 104
	if r.Pass < floor {
		t.Errorf("pass count %d fell below floor %d", r.Pass, floor)
	}
}

// TestClosedBuckets pins buckets that have reached zero. A bucket going to zero
// is a PR's measure of done (CLAUDE.md), and this is what stops it from quietly
// refilling: the floor above catches a net regression, but a bucket can refill
// while the total holds if another one drains at the same time.
//
// Entries are added only when the bucket is actually empty. "data count section
// required" is deliberately absent — it needs function-body decoding (#22) and
// is still 2, which is the honest state of #6.
//
// Note what is *not* here from #5: "unexpected end of section or function" and
// "section size mismatch" both drained substantially (9 → 6, 8 → 5) without
// reaching zero, because their remainder needs the code, global, and element
// grammars. A partially-drained bucket earns no entry; that is the difference
// between this test and the pass-count floor.
func TestClosedBuckets(t *testing.T) {
	requireSuite(t)
	closed := map[string][]string{
		"binary.wast": {
			"unexpected content after last section",                 // #6, was 23
			"function and code section have inconsistent lengths",   // #6, was 4
			"data count and data section have inconsistent lengths", // #6, was 3
			"malformed section id",                                  // #6, was 5
			"malformed limits flags",                                // #5, was 7
			"malformed import kind",                                 // #5, was 6
			"length out of bounds",                                  // #5, was 1
		},
		"custom.wast": {
			"function and code section have inconsistent lengths",
			"data count and data section have inconsistent lengths",
			"malformed section id",
			"unexpected end", // #5, was 2 — the custom section's name-inside-its-extent rule
		},
	}
	for file, keys := range closed {
		s, err := ParseFile(filepath.Join(suiteDir, file))
		if err != nil {
			t.Errorf("%s: parse: %v", file, err)
			continue
		}
		r := run(s)
		for _, k := range keys {
			if got := len(r.Buckets[k]); got != 0 {
				t.Errorf("%s: bucket %q refilled to %d; it was closed and must stay closed", file, k, got)
				for _, f := range r.Buckets[k] {
					t.Logf("  line %d: got %q", f.Line, f.Got)
				}
			}
		}
	}
}

// TestGatedVectors pins exactly which vectors the engine is allowed to decline.
//
// Result.Gated is a third verdict, and a third verdict is a way to make a board
// look better by moving failures into it. This is the control on that: the gated
// set is enumerated here, so a decline that appears anywhere else is a test
// failure rather than a quietly emptier board.
//
// Every entry needs a reason naming the gated feature, on the same principle as
// the deadcode allowlist (decision 0005): an unexplained allowlist entry is a
// suppression wearing a disguise.
func TestGatedVectors(t *testing.T) {
	requireSuite(t)

	// file → line → why it is gated.
	allowed := map[string]map[int]string{
		// Both vectors carry i64 memory limits flags (0x04), which is memory64.
		// With that gate off the decoder must reject them, and neither vector is
		// asking a question the engine can answer in that state.
		"binary_leb128_64.wast": {
			1:  "memory64: i64 limits flags on the memory section",
			16: "memory64: i64 limits flags on the memory section",
		},
	}

	files := []string{
		"binary.wast", "binary-leb128.wast", "binary_leb128_64.wast",
		"binary0.wast", "custom.wast",
	}
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		// Re-run per command so a decline can be attributed to a line.
		for _, c := range s.Commands {
			if c.Kind == KindUnsupported {
				continue
			}
			if !isGated(decode(c.Module)) {
				continue
			}
			if _, ok := allowed[f][c.Line]; !ok {
				t.Errorf("%s:%d declined by a feature gate but is not in the allowed set;\n"+
					"\tif the gate is right, add it with the feature named; if not, the decoder is over-gating and hiding a failure",
					f, c.Line)
			}
		}
		// The reverse: a stale entry would claim a decline that no longer happens,
		// overstating how much the gates are doing.
		for line := range allowed[f] {
			var found bool
			for _, c := range s.Commands {
				if c.Line == line && isGated(decode(c.Module)) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s:%d is in the allowed-gated set but is no longer declined; remove the entry", f, line)
			}
		}
	}
}

// TestVerdictsPartitionCommands checks the arithmetic the board depends on: every
// command lands in exactly one of pass, fail, unsupported, or gated.
//
// Without this, adding a verdict is a chance to lose vectors — a command that
// falls through every branch simply vanishes, and a board that does not sum is
// how a suite silently stops covering something.
func TestVerdictsPartitionCommands(t *testing.T) {
	requireSuite(t)
	files := []string{
		"binary.wast", "binary-leb128.wast", "binary_leb128_64.wast",
		"binary0.wast", "custom.wast",
	}
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := run(s)
		if got, want := r.Pass+r.Fail+r.Unsupported+r.Gated, len(s.Commands); got != want {
			t.Errorf("%s: verdicts sum to %d but the script has %d commands; %d vectors are unaccounted for",
				f, got, want, want-got)
		}
	}
}

// TestPhase1Files runs every suite file that phase 1 can meaningfully score,
// so the board covers the byte-string corpus rather than one file.
func TestPhase1Files(t *testing.T) {
	requireSuite(t)
	files := []string{
		"binary.wast",
		"binary-leb128.wast",
		"binary_leb128_64.wast",
		"binary0.wast",
		"custom.wast",
	}
	totalPass, totalFail, totalUnsup, totalGated := 0, 0, 0, 0
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := run(s)
		t.Log("\n" + r.Board())
		totalPass += r.Pass
		totalFail += r.Fail
		totalUnsup += r.Unsupported
		totalGated += r.Gated
	}
	t.Logf("phase 1 total: %d pass, %d fail, %d unsupported, %d gated",
		totalPass, totalFail, totalUnsup, totalGated)
}

// TestParseEverySuiteFile is a parser-robustness sweep: the s-expression reader
// must survive all 257 upstream .wast files without a parse error, even the ones
// full of wat it cannot interpret. Parsing and understanding are separate
// concerns, and conflating them would hide the real unsupported count.
func TestParseEverySuiteFile(t *testing.T) {
	requireSuite(t)
	paths, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
	if err != nil || len(paths) == 0 {
		t.Skip("no .wast files found")
	}
	var broken int
	for _, p := range paths {
		if _, err := ParseFile(p); err != nil {
			broken++
			t.Errorf("%s: %v", filepath.Base(p), err)
		}
	}
	t.Logf("parsed %d/%d .wast files", len(paths)-broken, len(paths))
}
