package spec

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// suiteDir is where make spec-tests vendors the upstream suite. Gitignored;
// tests skip rather than fail when it is absent, so a fresh clone is green
// before the fetch.
//
// That skip license is exactly one thing — local-dev convenience on an unvendored
// clone — and internal/testenv revokes it under BURROUGHS_NO_SKIP=1, which every
// CI job that runs tests sets. See the package doc there for the grave (#29).
const suiteDir = "../../testdata/spec"

// phase1Files is every suite file phase 1 can meaningfully score: all-byte-string
// corpora, no text-format modules.
//
// One definition, because four tests need it and four copies of a list is the drift
// vector TestEveryFixtureFileIsChecked exists to close. It had already started:
// adding the utf8 files to the board list alone would have left the gated
// allowlist, the verdict partition, and the priority-queue property checking a
// narrower corpus than the board reports — three controls quietly scoped to less
// than the number they sit beside.
//
// utf8-invalid-encoding.wast is deliberately absent. Its 176 forms are
// `(module quote ...)` text-format modules rather than byte strings, so phase 1
// cannot execute them; they belong to the text-format work (#8). Listing a file the
// harness cannot read would inflate the unsupported count and call it coverage.
var phase1Files = []string{
	"binary.wast",
	"binary-leb128.wast",
	"binary_leb128_64.wast",
	"binary0.wast",
	"custom.wast",

	// The name grammar (#26): 176 vectors each, all "malformed UTF-8 encoding".
	"utf8-import-module.wast",
	"utf8-import-field.wast",
	"utf8-custom-section-id.wast",
}

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

// requireSuite gates every board test on the corpus actually being there.
//
// License: local dev on a clone where `make spec-tests` has not been run.
// Revoked by BURROUGHS_NO_SKIP=1.
//
// It asserts a file *count*, not `os.Stat` on the directory as it once did: a
// partial or empty fetch passes an existence check and then produces a board over
// whatever happened to be present, which is a number with an unasserted input.
func requireSuite(t *testing.T) {
	t.Helper()
	testenv.RequireSuite(t, suiteDir)
}

// suitePaths returns every vendored .wast file, having already required the
// corpus. An empty result here is impossible rather than skippable — requireSuite
// asserted the count — so it is a Fatal: the two disagreeing would mean the glob
// and the assertion are looking at different things.
func suitePaths(t *testing.T) []string {
	t.Helper()
	requireSuite(t)
	paths, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob %s after requireSuite passed: %d paths, err=%v", suiteDir, len(paths), err)
	}
	return paths
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
	// after the section payload grammars (#5), 114 after the opcode table (#41), and
	// 127 — the whole file — after the instruction and function-body grammars
	// (#43/#39, with #22 closing inside).
	//
	// At the file's total the floor and the total coincide, so the inequality below can
	// no longer distinguish "held" from "improved". That is fine for a *floor*, and the
	// equality is asserted separately rather than left implied: a file that is fully
	// green has a stronger property than a floor, and stating the weaker one only would
	// let a vector go missing from the file unnoticed.
	const floor = 127
	if r.Pass < floor {
		t.Errorf("pass count %d fell below floor %d", r.Pass, floor)
	}
	if r.Fail != 0 {
		t.Errorf("binary.wast is fully green at %d/%d and must stay so; %d failing:\n%s",
			floor, r.Total(), r.Fail, r.Board())
	}
	if r.Total() != floor {
		t.Errorf("binary.wast scored %d vectors, want %d — the floor is the file's total, so a "+
			"changed count means the corpus moved and the floor is measuring a different file "+
			"than the one it was set against (#42 pins the suite by SHA)", r.Total(), floor)
	}
}

// TestClosedBuckets pins buckets that have reached zero. A bucket going to zero
// is a PR's measure of done (CLAUDE.md), and this is what stops it from quietly
// refilling: the floor above catches a net regression, but a bucket can refill
// while the total holds if another one drains at the same time.
//
// Entries are added only when the bucket is actually empty, and *because the grammar
// answers them* — a bucket emptied by declining to score its vectors is not closed, it
// is hidden. binary_leb128_64.wast is the live example and has no entry here: its one
// "integer too large" vector is `gated` under the default lane (memory64 off), so the
// bucket reads zero on a board that never asked the question. TestGatedVectors and the
// all-gates-on lane are what own that case; a closed-bucket entry would be the third
// verdict laundering itself into a green.
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

			// The instruction and function-body grammars (#43/#39), which took the file
			// to 127/127. Every one of these is a *mechanism* that landed, not a vector
			// that was argued away, and the counts are the base measured at 9569cb7:
			"illegal opcode ff",                     // #43, was 1 — the one oracle-covered rendering
			"illegal opcode",                        // #43, was 1 — binary.wast:345, the elem-segment byte
			"data count section required",           // #22, was 2 — closed inside #39, four opcodes from free.ml
			"too many locals",                       // #39, was 2 — the sum, checked at 64 bits
			"END opcode expected",                   // #39, was 1 — `end_ s` on a byte that is not END
			"unexpected end of section or function", // #39, was 3 — the deferred const verdict's half
			"section size mismatch",                 // #39, was 1 — `sized` wraps each *body*
			"integer too large",                     // #39, was 2 — a locals count's own LEB width
		},
		"custom.wast": {
			"function and code section have inconsistent lengths",
			"data count and data section have inconsistent lengths",
			"malformed section id",
			"unexpected end", // #5, was 2 — the custom section's name-inside-its-extent rule
		},

		// The utf8-*.wast files are single-bucket by construction: 176 vectors each,
		// every one expecting "malformed UTF-8 encoding". Closing all three at once
		// is what a general rule looks like on the board — one predicate, three
		// name positions (#26).
		"utf8-import-module.wast":     {"malformed UTF-8 encoding"},
		"utf8-import-field.wast":      {"malformed UTF-8 encoding"},
		"utf8-custom-section-id.wast": {"malformed UTF-8 encoding"},
	}
	// The keys are a fifth file list, so pin them against phase1Files. A closed
	// bucket in a file the board does not score is a regression control watching a
	// number nobody reports.
	inPhase1 := make(map[string]bool, len(phase1Files))
	for _, f := range phase1Files {
		inPhase1[f] = true
	}
	for file := range closed {
		if !inPhase1[file] {
			t.Errorf("%s has closed buckets but is not in phase1Files; the board does not score it", file)
		}
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

	files := phase1Files
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

// allFeaturesOn returns a Features with every gate the decoder knows about turned
// on, discovered by reflection rather than by an enumerated literal.
//
// The enumerated version is a drift vector of exactly the shape
// TestEveryFixtureFileIsChecked exists to close: adding a fifth gate and
// forgetting to add it here would leave the all-on lane quietly running with that
// gate off, so vectors could hide in `gated` in *both* lanes — the precise failure
// the lane is built to prevent. A non-bool field fails loudly rather than being
// skipped, because "I did not know how to turn this on" must not read as "it is on".
func allFeaturesOn(t *testing.T) binary.Features {
	t.Helper()
	var f binary.Features
	v := reflect.ValueOf(&f).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		fld := v.Field(i)
		if fld.Kind() != reflect.Bool {
			t.Fatalf("Features.%s is %s, not a bool: this test cannot turn it on, so the all-gates-on lane would silently run with it off",
				name, fld.Kind())
		}
		fld.SetBool(true)
	}
	return f
}

// TestAllGatesOnLeavesNothingGated is the structural control on the third verdict
// (Scott's condition on #27).
//
// TestGatedVectors bounds `gated` per vector, but per-vector allowlists are
// vigilance: they stop a vector from hiding *unnoticed*, not from hiding. This
// closes it structurally. Under every tracked gate on, no vector may be declined —
// every one answers on the merits, pass or fail, with nowhere to park.
//
// That makes `gated` a **deferral that cannot become a disappearance**: a vector
// sitting in `gated` on the default board is simultaneously being honestly *failed*
// here, and stays failed until its feature actually works. The default lane says
// "not asked"; this lane insists the question still exists.
//
// So this test is expected to be *red-ish* in aggregate — the fail counts below are
// higher than the default board's, and that is the point. What must be zero is
// Gated.
func TestAllGatesOnLeavesNothingGated(t *testing.T) {
	requireSuite(t)

	d := &binary.Decoder{Features: allFeaturesOn(t)}
	decodeAllOn := func(image []byte) error {
		_, err := d.DecodeModule(image)
		return err
	}

	files := phase1Files
	var totalPass, totalFail, totalGated int
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		// Still RunGated, deliberately: the point is to *measure* Gated and require
		// it to be zero. Using Run would fold declines into Fail and the requirement
		// would be unfalsifiable — the counter it asserts on could not be nonzero.
		r := s.RunGated(decodeAllOn, isGated)
		t.Log("\n" + r.Board())
		totalPass, totalFail, totalGated = totalPass+r.Pass, totalFail+r.Fail, totalGated+r.Gated

		if r.Gated != 0 {
			t.Errorf("%s: %d vectors declined with every gate on;\n"+
				"\ta gate that still declines under full features is not a gate — it is a rejection wearing a feature name, and the vector has nowhere left to be honestly scored",
				f, r.Gated)
			// Naming them, because "3 gated" is not an actionable board line.
			for _, c := range s.Commands {
				if isGated(decodeAllOn(c.Module)) {
					t.Errorf("  %s:%d still gated: %v", f, c.Line, decodeAllOn(c.Module))
				}
			}
		}
	}
	t.Logf("all gates on: %d pass, %d fail, %d gated (Gated must be 0; fail is expected to exceed the default lane's)",
		totalPass, totalFail, totalGated)
}

// TestVerdictsPartitionCommands checks the arithmetic the board depends on: every
// command lands in exactly one of pass, fail, unsupported, or gated.
//
// Without this, adding a verdict is a chance to lose vectors — a command that
// falls through every branch simply vanishes, and a board that does not sum is
// how a suite silently stops covering something.
func TestVerdictsPartitionCommands(t *testing.T) {
	requireSuite(t)
	files := phase1Files
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
	files := phase1Files
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
	paths := suitePaths(t)
	var broken int
	for _, p := range paths {
		if _, err := ParseFile(p); err != nil {
			broken++
			t.Errorf("%s: %v", filepath.Base(p), err)
		}
	}
	t.Logf("parsed %d/%d .wast files", len(paths)-broken, len(paths))
}
