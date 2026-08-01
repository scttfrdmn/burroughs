package spec

import (
	"errors"
	"path/filepath"
	"reflect"
	"sort"
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

// boardFiles is the corpus the board scores: every vendored .wast file that holds at
// least one command the engine can answer.
//
// **Derived, not enumerated (#52).** It used to be a hand-written list of eight
// byte-string files, and that list was the enumerated-literal defect living in the
// corpus selector itself — *derive the domain, never enumerate it*, unapplied to the
// oracle's own inputs. Its blind spot was measured rather than argued — by the selector,
// not by a grep, since a regex over `(module\s+binary` counts *text* while binaryModule
// counts what the decoder will actually be handed: **six** files (align, binary-gc, elem,
// float_literals, global, simd_const) hold commands the engine already answers and were
// off the board because of their *neighbours* — an assert_return in the same file, not
// anything the decoder could not do. The result was 19 passes, 8 fails, and 6 gated
// vectors the board could not see, one of which (#51) is a live accept-direction defect:
// a valid module rejected with the spec's own word for malformed.
//
// The selection question changed with it. It was "which files did we list", and it is now
// "which files contain a command whose Kind the run loop scores" — a capability question,
// asked of the corpus rather than answered from memory. A file whose every command is
// unsupported is excluded because scoring it would add unsupported lines and no verdicts;
// a file with one answerable command is included, and its other commands are counted in
// the unsupported column where they are visible.
//
// The consequence is deliberate and is the doctrine this change carries: the board's
// unsupported count goes from zero to 1345. Zero-unsupported was a property of the
// byte-string corpus, never a law of the board. What is honest is that nothing hides —
// so the column is bucketed by command head, floored against growth, and expected to
// shrink monotonically as components land.
func boardFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, p := range suitePaths(t) {
		s, err := ParseFile(p)
		if err != nil {
			t.Errorf("%s: parse: %v", p, err)
			continue
		}
		if scorableCommands(s) > 0 {
			files = append(files, filepath.Base(p))
		}
	}
	// Vacuity floor, per the comparisons-need-a-vacuity-check rule: a selector that
	// finds nothing agrees with a board of nothing, and every assertion downstream
	// would compare two empty sets and pass.
	//
	// **14 files, printed rather than reasoned.** The first draft of this floor said 20
	// on the strength of "27 files have no interpreter-dependent command" — a different
	// set, and the floor caught the error immediately by failing. Those 27 include files
	// whose every command is a text-bodied module or an assert_invalid, all unsupported;
	// what this selector wants is files with at least one *scorable* command, which is
	// 14. (data.wast is the instructive miss: it has five (module binary ...) forms whose
	// elements are not all string literals, so binaryModule rejects them and the file has
	// nothing scorable.) 12 leaves room for upstream churn without leaving room for a
	// selector that stopped selecting.
	if len(files) < 12 {
		t.Fatalf("boardFiles selected only %d files, want >=12 — the selector is not "+
			"finding answerable commands, so every count below is over a corpus that "+
			"is not there (#42 pins the suite by SHA)", len(files))
	}
	sort.Strings(files)
	return files
}

// scorableCommands counts the commands in a script that the run loop scores — the
// capability predicate, in one place.
//
// Derived from Kind rather than from a list of heads, and deliberately *not* a
// hand-maintained set: when #53 teaches the harness `(module quote ...)`, a new Kind
// appears and this predicate widens on its own. A head list would have to be edited,
// and the edit is exactly what gets forgotten.
func scorableCommands(s *Script) int {
	n := 0
	for _, c := range s.Commands {
		if c.Kind != KindUnsupported {
			n++
		}
	}
	return n
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
	// The keys are a fifth file list, so pin them against the derived board corpus. A
	// closed bucket in a file the board does not score is a regression control
	// watching a number nobody reports.
	onBoard := make(map[string]bool)
	for _, f := range boardFiles(t) {
		onBoard[f] = true
	}
	for file := range closed {
		if !onBoard[file] {
			t.Errorf("%s has closed buckets but is not on the board; nothing scores it", file)
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

		// Six (module binary ...) forms whose type section declares a v128 result
		// (`\60\00\01\7b` — functype, no params, one result, valtype 0x7b). v128 is
		// SIMD's value type, so with the SIMD gate off the decoder must reject them
		// and none of the six is asking a question the engine can answer in that
		// state. Verified by reading the vectors rather than by trusting the count:
		// the gate is right, so these are allowlisted rather than treated as
		// over-gating.
		//
		// They arrived with the derived corpus (#52) — the file was off the board
		// before, so its 6 declines were invisible along with its 752 unsupported
		// commands. Note the shape: a wider corpus makes a *control* fire, which is
		// what a control that was scoped to a sample looks like when the sample grows.
		"simd_const.wast": {
			1570: "SIMD: v128 result in the type section",
			1587: "SIMD: v128 result in the type section",
			1604: "SIMD: v128 result in the type section",
			1621: "SIMD: v128 result in the type section",
			1638: "SIMD: v128 result in the type section",
			1655: "SIMD: v128 result in the type section",
		},

		// Seven (module binary ...) forms carrying the function-references table form:
		// `\40\00\64\70\00\01\d2\00\0b` — the 0x40 prefix, the reserved zero, tabletype
		// `(ref func)` with limits [1..], and a `(ref.func 0)` initializer. Decision
		// 0008 folds function references into the GC gate, so with GC off the decoder
		// must decline, and the decline must be feature-named rather than
		// `malformed reference type` (#51 was exactly that violation).
		//
		// Verified by reading all seven, not by trusting that seven declines in one
		// bucket share one cause: each carries the byte-identical table entry. The gate
		// is right, so these are allowlisted rather than treated as over-gating.
		//
		// **These lines were `fail` an hour ago**, which is the point of the entry. They
		// were the board's accept-direction bucket — a valid module rejected — and the
		// fix converts them to an honest decline. They are simultaneously *passing* in
		// the all-gates-on lane (798, up from 791), so the parked verdict is earned
		// there rather than deferred everywhere: a decline that cannot become a
		// disappearance.
		"elem.wast": {
			453: "gc/function-references: the 0x40 table form with an initializer",
			470: "gc/function-references: the 0x40 table form with an initializer",
			487: "gc/function-references: the 0x40 table form with an initializer",
			504: "gc/function-references: the 0x40 table form with an initializer",
			544: "gc/function-references: the 0x40 table form with an initializer",
			561: "gc/function-references: the 0x40 table form with an initializer",
			578: "gc/function-references: the 0x40 table form with an initializer",
		},
	}

	files := boardFiles(t)
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

	files := boardFiles(t)
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

	// A pass floor for *this* lane too, and the gap it closes was found by landing #51.
	//
	// Asserting only Gated == 0 makes this lane blind to the thing it is otherwise best
	// placed to see: a gated feature that *breaks*. Turn every gate on, decode a
	// construct wrong, and Gated stays zero while a pass silently becomes a fail — the
	// lane reports success because the one number it checks is unaffected. #51 moved this
	// count 791 → 798, which is only visible because it was printed; nothing would have
	// noticed it moving back.
	//
	// So: the same floor discipline as the default board, on the lane where gated
	// features are the only place they can be measured at all. The default lane cannot
	// substitute — there these seven vectors are honestly `gated`, and a floor over a
	// number that excludes them says nothing about whether GC still works.
	const allOnPassFloor = 798
	if totalPass < allOnPassFloor {
		t.Errorf("all-gates-on pass count %d fell below floor %d — a gated feature regressed, "+
			"which the Gated==0 assertion above cannot see: with every gate on, a broken "+
			"feature turns a pass into a fail and leaves Gated at zero",
			totalPass, allOnPassFloor)
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
	files := boardFiles(t)
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
	files := boardFiles(t)
	totalPass, totalFail, totalUnsup, totalGated := 0, 0, 0, 0
	byHead := map[string]int{}
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
		for h, n := range r.UnsupportedByHead {
			byHead[h] += n
		}
	}
	t.Logf("board total over %d files: %d pass, %d fail, %d unsupported, %d gated",
		len(files), totalPass, totalFail, totalUnsup, totalGated)

	// The unsupported column as a work plan, not a number. Printed every run so the
	// PR's Board line can quote which component each unrun vector waits on.
	agg := &Result{UnsupportedByHead: byHead}
	for _, h := range agg.UnsupportedByHeadBySize() {
		t.Logf("  unsupported %5d  %s", byHead[h], h)
	}

	// Monotonicity ceiling on the unsupported column (#52).
	//
	// The column is honest only if it is *watched*: an unsupported count that grows
	// means either the corpus moved or a capability regressed, and both want an
	// alarm rather than a larger number nobody re-reads. This is the pass-count
	// floor's mirror image — that one may only rise, this one may only fall — and
	// the pair is what makes "shrinking monotonically as components land" a
	// checkable claim instead of an intention.
	//
	// 1345 at the measured revision. Lowered as components land; never raised
	// without saying what moved. #42 (SHA-pin the suite) is what keeps this number
	// meaningful, since a corpus that drifts changes it for reasons that are not
	// findings.
	const unsupportedCeiling = 1345
	if totalUnsup > unsupportedCeiling {
		t.Errorf("unsupported rose to %d, ceiling %d — either a capability regressed or the "+
			"corpus moved; both need an explanation rather than a raised ceiling",
			totalUnsup, unsupportedCeiling)
	}

	// Pass floor over the whole board, the counterpart to TestBinaryWast's per-file
	// floor. 783 at the measured revision: 764 from the byte-string corpus plus the
	// 19 the derived selector newly reaches.
	const passFloor = 783
	if totalPass < passFloor {
		t.Errorf("board pass count %d fell below floor %d", totalPass, passFloor)
	}
}

// TestDenominatorExcludesUnaskedCommands pins Total()'s denominator: it counts what
// was asked, never what was skipped or declined.
//
// This control exists because the choice became load-bearing the moment the corpus
// was derived (#52). While the board ran eight byte-string files, Unsupported was
// zero and Gated was two, so folding either into the denominator would have been
// nearly invisible — the kind of decision a refactor makes silently because nothing
// fails. With ~1345 unsupported commands the same slip renders a green board as
// 783/2128 and reads as a collapse when nothing regressed, and worse, it makes the
// ratio improve when a *component* lands rather than when a *verdict* is earned.
//
// A comment cannot fail, so the invariant gets a test. Falsified while writing it:
// changing Total to Pass+Fail+Unsupported makes the second case below report 1/2.
func TestDenominatorExcludesUnaskedCommands(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want int
	}{
		{"pass and fail are the denominator", Result{Pass: 3, Fail: 2}, 5},
		{"unsupported is not asked, so not counted", Result{Pass: 1, Unsupported: 99}, 1},
		{"gated is declined, so not counted", Result{Pass: 1, Gated: 99}, 1},
		{"neither, together", Result{Pass: 2, Fail: 1, Unsupported: 40, Gated: 7}, 3},
		// The degenerate case stated rather than implied: a board that asked nothing
		// has a denominator of zero, which is why TestBinaryWast checks Total() != 0
		// before trusting a ratio. A pass rate over an empty denominator is the
		// vacuity failure wearing arithmetic.
		{"nothing asked at all", Result{Unsupported: 500}, 0},
	}
	for _, c := range cases {
		if got := c.r.Total(); got != c.want {
			t.Errorf("%s: Total() = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestUnsupportedIsBucketedByCommand checks that the unsupported column names what
// each unrun vector waits on, rather than reporting a bare total.
//
// The scalar and the map are two records of the same fact, so they can drift — and a
// map that silently stopped being populated would leave the board printing a large
// number with no work plan beside it, which is the column reverting to exactly the
// thing #52's doctrine forbids. Both directions: the sum must equal the scalar, and
// the map must be non-empty when the scalar is.
func TestUnsupportedIsBucketedByCommand(t *testing.T) {
	requireSuite(t)
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		r := run(s)
		sum := 0
		for _, n := range r.UnsupportedByHead {
			sum += n
		}
		if sum != r.Unsupported {
			t.Errorf("%s: UnsupportedByHead sums to %d but Unsupported is %d — the column and "+
				"its breakdown disagree, so one of them is not being maintained", f, sum, r.Unsupported)
		}
		if r.Unsupported > 0 && len(r.UnsupportedByHead) == 0 {
			t.Errorf("%s: %d unsupported commands and no breakdown; the board would print a "+
				"number with no work plan beside it", f, r.Unsupported)
		}
		// Every key names a real command head, or the breakdown is decoration. An
		// empty key would print as a blank row, which is the unlabelled-entry
		// failure the "(no head atom)" placeholder exists to prevent.
		for h := range r.UnsupportedByHead {
			if h == "" {
				t.Errorf("%s: UnsupportedByHead has an empty key; a blank row in a work-plan "+
					"column is the entry nobody investigates", f)
			}
		}
	}
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
