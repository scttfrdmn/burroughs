package spec

import (
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
	r := s.Run(decode)
	t.Log("\n" + r.Board())

	if r.Unsupported != 0 {
		t.Errorf("binary.wast should be fully parseable by phase 1, got %d unsupported", r.Unsupported)
	}
	if r.Total() == 0 {
		t.Fatal("no assertions executed — harness is not wired")
	}
	// Regression floor, set to the count measured when the harness first ran.
	// Raise it as decoder work lands; never lower it.
	const floor = 49
	if r.Pass < floor {
		t.Errorf("pass count %d fell below floor %d", r.Pass, floor)
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
	totalPass, totalFail, totalUnsup := 0, 0, 0
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := s.Run(decode)
		t.Log("\n" + r.Board())
		totalPass += r.Pass
		totalFail += r.Fail
		totalUnsup += r.Unsupported
	}
	t.Logf("phase 1 total: %d pass, %d fail, %d unsupported", totalPass, totalFail, totalUnsup)
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
