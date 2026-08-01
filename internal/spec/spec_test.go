package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
	"github.com/scttfrdmn/burroughs/internal/text"
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
//
// **Admitting (module quote ...) widened it again, 14 files to 68 (decision 0010).** The
// selector did not change: it still asks which files hold a command whose Kind the run
// loop scores, and two new Kinds made 54 more files answer yes. That is the derived
// selector working as designed — a capability question re-asked when capability moves,
// rather than a list re-edited — and it is why the admission could not be scoped to the
// eleven vectors that prompted it. The unsupported ceiling rose 1345 → 26742 in the same
// motion, which is *corpus admitted*, not regression.
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
	// **68 files, printed rather than reasoned.** The first draft of this floor said 20
	// on the strength of "27 files have no interpreter-dependent command" — a different
	// set, and the floor caught the error immediately by failing. Those 27 include files
	// whose every command is a text-bodied module or an assert_invalid, all unsupported;
	// what this selector wants is files with at least one *scorable* command, which was
	// 14 before the quote admission and is 68 after it. (data.wast is the instructive
	// miss: it has five (module binary ...) forms whose elements are not all string
	// literals, so binaryModule rejects them and the file has nothing scorable.)
	//
	// The floor tracks the measurement rather than staying at its historical value: a
	// floor of 12 against 68 selected files would tolerate the selector losing 56 files
	// silently, which is the vacuity hole one step short of empty. 60 leaves room for
	// upstream churn without leaving room for a selector that mostly stopped selecting.
	if len(files) < 60 {
		t.Fatalf("boardFiles selected only %d files, want >=60 — the selector is not "+
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

// readText is the wat entry point the board scores — the lexer, and only the lexer.
//
// **What this does not do is the whole reason the reject-direction column reads the way
// it does.** `text.LexAll` runs to EOF and returns the first lex error; nothing above it
// exists yet, so every vector whose malformedness lives in the grammar (`unexpected
// token`), in a validator (`alignment`, `duplicate func`), or in the parser's UTF-8
// decode of a name (176 of the 186 `malformed UTF-8 encoding` vectors) is a *fail*, in a
// named bucket, and not a skip. That is the bucketed-failures discipline: the 600 are the
// work plan for #8 and the parser, not a debt hidden behind a fourth verdict — the fourth
// verdict was for a component that did not exist, and the lexer exists.
func readText(src []byte) error {
	_, err := text.LexAll(src)
	return err
}

// isGated asks the engine, rather than reading its error text. The taxonomy is
// the engine's to define; a substring test here would be the harness guessing at
// the thing it exists to check.
func isGated(err error) bool { return errors.Is(err, binary.ErrFeatureDisabled) }

// run scores a script with gate declines separated from verdicts.
func run(s *Script) *Result { return s.RunGated(decode, readText, isGated) }

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

// TestBareQuoteFormsPassUnearned names the seven passes the reader did not earn.
//
// A bare `(module quote "...")` asserts its source is *valid* wat. The engine's answer is
// `text.LexAll`, which asserts only that the source lexes — so a vector passes here by the
// absence of everything above the lexer, not by anything the lexer decided. That is
// **overfitting arrived at by omission** (§9 G-3): pass count bought by a check that is
// right on the vectors and wrong in general, and invisible on the board by construction
// because the board cannot tell an earned pass from an unopposed one.
//
// So it is reported rather than netted out. The seven are enumerated with what each one
// actually turns on, in the shape TestGatedVectors uses for the third verdict and for the
// same reason — an unexplained entry is a suppression wearing a disguise, and a category
// that can grow silently is a lever. When #8's parser lands, these seven become genuine
// verdicts and this test's job is to *shrink to zero*; a new bare form appearing here
// before then is a vector claiming a pass nobody looked at.
//
// The reverse direction matters more than the forward one, which is why both are checked:
// a listed vector that has started *failing* means the lexer regressed on something it
// used to accept — an accept-direction defect, the class no negative vector can falsify
// (decision 0007) — and it would otherwise read as this list going stale.
func TestBareQuoteFormsPassUnearned(t *testing.T) {
	requireSuite(t)

	// file → line → what the vector actually tests, i.e. what the lexer is silent about.
	unearned := map[string]map[int]string{
		// Annotation *placement* and shape: the spec says an unrecognized annotation is
		// ignored wherever it may appear, and these assert it in six positions. The lexer
		// skips annotation bodies (they produce no token), so it agrees for the wrong
		// reason — it cannot tell a well-placed annotation from a misplaced one, because
		// it has no notion of position at all.
		"annotations.wast": {
			32:  "annotation before a module field — placement is the parser's judgement",
			33:  "annotation inside a func body — same",
			36:  "annotation with a nested s-expression body",
			55:  "annotation containing string and id atoms",
			206: "annotation in a type position",
			207: "annotation in an import position",
		},
		// Comment nesting and termination inside a module. scanBlockComment does decide
		// closedness, so this one is the closest to earned of the seven — but what the
		// vector asserts is that the *module* is valid with comments interleaved, and the
		// module's validity is not a question the lexer is asked.
		"comments.wast": {
			83: "nested block comments interleaved with module fields",
		},
	}

	// Vacuity floor. A list of unearned passes that finds nothing is indistinguishable
	// from a board with none, and the second is the state this test exists to detect the
	// arrival of. Floored at the list's own length rather than at 1, per *a floor below
	// the list's own length is decoration*.
	want := 0
	for _, m := range unearned {
		want += len(m)
	}
	if want != 7 {
		t.Fatalf("the unearned list holds %d entries, want 7; the count is quoted in the "+
			"pass floor's account and in PR #61, so the two must not drift", want)
	}

	seen := 0
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		for _, c := range s.Commands {
			if c.Kind != KindModuleQuote {
				continue
			}
			err := readText(c.Source)
			_, listed := unearned[f][c.Line]
			switch {
			case err == nil && !listed:
				t.Errorf("%s:%d is a bare (module quote ...) that lexes clean and is not in "+
					"the unearned list; it passes because nothing above the lexer can "+
					"disagree with it, and a pass arrived at by omission has to be named",
					f, c.Line)
			case err != nil && listed:
				// The direction that is a defect rather than staleness.
				t.Errorf("%s:%d is listed as an unearned pass but the reader now rejects it "+
					"(%v); a valid module rejected is an accept-direction defect, which no "+
					"negative vector in the suite can catch", f, c.Line, err)
			case err == nil && listed:
				seen++
			}
		}
	}
	if seen != want {
		t.Errorf("found %d of %d listed unearned passes; a listed vector the loop never "+
			"reached means the file left the board and this list is watching nothing",
			seen, want)
	}
	t.Logf("%d bare (module quote ...) forms pass unearned, pending #8's parser", seen)
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
		r := s.RunGated(decodeAllOn, readText, isGated)
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
// command lands in exactly one of pass, fail, unsupported, gated, or unimplemented.
//
// Without this, adding a verdict is a chance to lose vectors — a command that
// falls through every branch simply vanishes, and a board that does not sum is
// how a suite silently stops covering something. Decision 0010 added the fifth
// term, which is precisely the event this test was written in advance of: the
// arithmetic was asserted before there was a fourth verdict to lose vectors to.
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
		if got, want := r.Pass+r.Fail+r.Unsupported+r.Gated+r.Unimplemented, len(s.Commands); got != want {
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
	totalPass, totalFail, totalUnsup, totalGated, totalUnimpl := 0, 0, 0, 0, 0
	byHead := map[string]int{}
	byCap := map[Capability]int{}
	aggBuckets := map[string][]Failure{}
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
		totalUnimpl += r.Unimplemented
		for h, n := range r.UnsupportedByHead {
			byHead[h] += n
		}
		for c, n := range r.UnimplementedByCapability {
			byCap[c] += n
		}
		for k, fs := range r.Buckets {
			aggBuckets[k] = append(aggBuckets[k], fs...)
		}
	}
	t.Logf("board total over %d files: %d pass, %d fail, %d unsupported, %d gated, %d unimplemented",
		len(files), totalPass, totalFail, totalUnsup, totalGated, totalUnimpl)

	// The fail column as a work plan across the whole board, not per file. Printed every
	// run so the PR's Board line quotes measured buckets rather than a recollection.
	for _, k := range (&Result{Buckets: aggBuckets}).BucketsBySize() {
		t.Logf("  fail %5d  %s", len(aggBuckets[k]), k)
	}

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
	// **26742 after the quote admission (decision 0010), was 1345.** Raised exactly
	// once, with the reason stated: admitting (module quote ...) put 54 more files on
	// the derived board, and their commands are overwhelmingly forms the harness still
	// cannot ask about. That is the one licensed reason to raise this number — the
	// corpus grew — and it is why the rule is "never raised *without saying what
	// moved*" rather than "never raised".
	//
	// Lowered as components land. #42 (SHA-pin the suite) is what keeps this number
	// meaningful, since a corpus that drifts changes it for reasons that are not
	// findings.
	const unsupportedCeiling = 26742
	if totalUnsup > unsupportedCeiling {
		t.Errorf("unsupported rose to %d, ceiling %d — either a capability regressed or the "+
			"corpus moved; both need an explanation rather than a raised ceiling",
			totalUnsup, unsupportedCeiling)
	}

	// The fourth verdict's ceiling, and its purpose is the *drain* (decision 0010).
	//
	// **At its terminal value, which is the only value that makes it an assertion.** It
	// was 1236 from the column's creation until the wat reader landed, all of them
	// waiting on it (#53); the retirement converted every one, so the ceiling is 0.
	//
	// Lowering it is not bookkeeping. A ceiling of 1236 against an actual 0 permits 1236
	// vectors to reappear in the fourth column without a word — the *whole* population,
	// and precisely the disappearance guard 6 exists to prevent, wearing a ceiling's
	// clothes. A bound that no longer binds anything is a control asserting nothing while
	// looking like one, and this drain had a terminal value fixed by decision 0004 rather
	// than by taste: no minor version is cut while its milestone's unimplemented is
	// nonzero, and v0.1.0 requires zero.
	//
	// Still a ceiling rather than an equality, because at zero the two coincide and the
	// ceiling generalizes: the next capability admitted raises it with an account, and
	// drains it back down.
	const unimplementedCeiling = 0
	if totalUnimpl > unimplementedCeiling {
		t.Errorf("unimplemented rose to %d, ceiling %d — a new capability gap appeared or "+
			"one widened; the column exists to drain, so growth needs an explanation",
			totalUnimpl, unimplementedCeiling)
	}

	// Every unimplemented vector is attributed, or the column is a bare number again.
	attributed := 0
	for _, n := range byCap {
		attributed += n
	}
	if attributed != totalUnimpl {
		t.Errorf("unimplemented totals %d but attribution sums to %d; the column and its "+
			"work plan disagree", totalUnimpl, attributed)
	}
	for _, c := range (&Result{UnimplementedByCapability: byCap}).UnimplementedByCapabilityBySize() {
		issue, _ := CapabilityIssue(c)
		t.Logf("  unimplemented %5d  %s (%s)", byCap[c], c, issue)
	}

	// **The fail column is the board's instrument, and this is what keeps it one.**
	//
	// This assertion is the reason decision 0010 exists. Reading A of the admission
	// would have scored the 1236 quote vectors as failures, taking fail from 1 to 1237
	// — and a genuine regression landing tomorrow would arrive as 1238, invisible. A
	// ceiling on fail is only meaningful while fail means *defect*, so the two changes
	// are one change: admit the corpus, and keep the column that says what is broken.
	//
	// **The wat reader raised this 1 → 601, and the account is why that is not the
	// invisibility the paragraph above forbids.** 600 of the 601 are text-layer vectors
	// whose grammar is not written: the lexer answers 636 of the 1236 quote vectors and
	// the other 600 need the parser (#8), the validator, or the name decoder. Every one
	// of them is a *fail*, in a named bucket, and not a fourth-verdict entry — the
	// fourth verdict was for a component that did not exist, and the lexer exists, so a
	// vector it can be asked about has no excuse left. Reporting them as unimplemented
	// would be the disappearance guard 6 exists to prevent, one layer up.
	//
	// What would have been invisible is a single ceiling of 601, which is why the
	// ceiling is now **two ceilings over a structural partition**: a new decoder defect
	// arrives as `binaryFail 2 > 1` regardless of what the text column is doing. The
	// partition is on Failure.Kind rather than on the bucket string because the two
	// layers share strings — `malformed UTF-8 encoding` is a bucket on both sides — and
	// *when a partition's members share a value, an equality on that value is not a
	// partition check*.
	//
	// Falsified in both directions before being trusted, per the print-don't-trust rule:
	// with the operands of the Kind test swapped, binaryFail reads 600 and textFail 1,
	// and both arms fail. That is the check TestSectionSizeBothSigns's grave (#34) asks
	// for — a partition test verified against the partition, not against its labels.
	binaryFail, textFail := 0, 0
	for _, fs := range aggBuckets {
		for _, f := range fs {
			switch f.Kind {
			case KindModuleQuote, KindAssertMalformedText:
				textFail++
			default:
				binaryFail++
			}
		}
	}
	if binaryFail+textFail != totalFail {
		t.Errorf("fail partition sums to %d but the column is %d; a failure escaped both "+
			"arms, so one of the two ceilings below is watching a subset it cannot name",
			binaryFail+textFail, totalFail)
	}

	// 1 at the measured revision: binary-gc.wast:1, "malformed function type: 0x5e"
	// under an expected "malformed mutability". Unmoved by the wat reader — which is the
	// point of splitting the column, and the claim the split makes checkable.
	const binaryFailCeiling = 1
	if binaryFail > binaryFailCeiling {
		t.Errorf("decoder failures rose to %d, ceiling %d — a defect landed in the binary "+
			"decoder; this ceiling is deliberately not shared with the text column so that "+
			"a decoder regression cannot hide inside 600 unwritten grammars",
			binaryFail, binaryFailCeiling)
	}

	// 600 at the measured revision, and this one is a **work plan with a ceiling**
	// rather than a defect count: every member is a vector the lexer reached and could
	// not answer, bucketed by the spec string that names what is missing. It may only
	// fall, and it falls as #8's parser and the validator land. The buckets printed
	// above are the order to take them in.
	const textFailCeiling = 600
	if textFail > textFailCeiling {
		t.Errorf("text failures rose to %d, ceiling %d — either the reader regressed on "+
			"vectors it used to answer, or the corpus moved", textFail, textFailCeiling)
	}

	// Pass floor over the whole board, the counterpart to TestBinaryWast's per-file
	// floor.
	//
	// **1419 = 783 + 636, and the 636 is the forecast reconciled rather than absorbed.**
	// 783 is the pre-reader board: 764 from the byte-string corpus plus the 19 the
	// derived selector newly reaches, unmoved by the quote admission because admitting a
	// corpus earns no verdicts. The 636 the reader earns is 629 vectors answered through
	// an error whose text matches, plus **7 bare `(module quote ...)` forms that lex
	// clean and are unearned** — six in annotations.wast, one in comments.wast. None of
	// the seven turns on lexing; they are valid modules whose validity the lexer cannot
	// assess, and they pass because the parser's absence means nothing contradicts them.
	// Named here rather than netted out of the total: they are overfitting arrived at by
	// omission, and they will stop being free the moment #8 can disagree with them. They
	// are pinned individually by TestBareQuoteFormsPassUnearned so the seven cannot grow
	// quietly into a habit.
	const passFloor = 1419
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
		// The fourth verdict is excluded for the same reason as the other two, and at
		// 1236 vectors this is the largest of the three exclusions by far: folding it in
		// would render a 783/784 board as 783/2020 and read as a collapse on the day the
		// corpus was admitted.
		{"unimplemented is unanswered, so not counted", Result{Pass: 1, Unimplemented: 1236}, 1},
		{"all three exclusions at once", Result{Pass: 5, Fail: 2, Unsupported: 30, Gated: 4, Unimplemented: 99}, 7},
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

// TestEveryNeededCapabilityIsRegistered is guard 2 of decision 0010: a vector may
// reach the fourth verdict only via a registered capability.
//
// The abuse the fourth verdict invites is the one the third verdict already had to be
// defended against (#27): a category that is neither pass nor fail is a lever for
// emptying a board by fiat. `gated` was fenced with a per-vector allowlist plus an
// all-on lane, and neither transfers here — 1236 vectors cannot be allowlisted
// individually, and "turn the capability on" is not a configuration change when the
// component does not exist.
//
// So the fence is the registry: classify may only ask for a capability that has an
// entry, and the entry carries the issue that closes it — plus, per guard 6, the
// condition under which it must be deleted. This test reads the whole corpus rather
// than the board, because an unregistered capability on an unadmitted file is still a
// classification defect. TestNoCapabilityOutlivesItsComponent is the other half: this
// one guards the entry's birth, that one its death.
//
// **What "registered" means was refined by the first retirement, not weakened.** The
// original invariant read *every needed capability has a registry entry*, which was
// exactly right while no capability had ever been retired and became false the moment
// one was: `wat-reader` is needed by 1236 commands, has no entry, and that is the
// *success* condition, not a hole. The real invariant is the one the entry existed to
// serve — **every needed capability is accounted for, as a tracked debt or as a
// declared component** — and it is stronger than the old reading rather than looser,
// because the two arms are exclusive: a capability both registered and declared is
// guard 6's other-half failure, and one that is neither is guard 2's. The retirement
// is what made the distinction observable; before it, the two readings agreed on every
// input.
func TestEveryNeededCapabilityIsRegistered(t *testing.T) {
	requireSuite(t)
	seen := map[Capability]int{}
	for _, p := range suitePaths(t) {
		s, err := ParseFile(p)
		if err != nil {
			t.Errorf("%s: parse: %v", p, err)
			continue
		}
		for _, c := range s.Commands {
			if c.Needs == CapNone {
				continue
			}
			seen[c.Needs]++
			issue, registered := CapabilityIssue(c.Needs)
			declared := EngineHas(c.Needs)
			switch {
			case registered && declared:
				// Guard 6's first arm, and it is asserted here as well as in the death
				// test so that the birth guard cannot report "accounted for" on a
				// capability that is accounted for twice.
				t.Errorf("%s:%d needs capability %q, which is both registered as missing and "+
					"declared by the engine; retirement is one motion and this is the half "+
					"that was skipped", filepath.Base(p), c.Line, c.Needs)
			case declared:
				// Retired: the engine has it, so there is no debt to track. Nothing to
				// check here — TestNoCapabilityOutlivesItsComponent is what proves the
				// population drained, which is the claim this arm is standing on.
			case !registered:
				t.Errorf("%s:%d needs capability %q, which is neither registered nor declared "+
					"by the engine; an unaccounted capability is a fourth-verdict column with "+
					"no owner", filepath.Base(p), c.Line, c.Needs)
			default:
				if issue == "" {
					t.Errorf("capability %q is registered with an empty issue; the tracking "+
						"number is what makes it a debt rather than an intention", c.Needs)
				}
				if ret, _ := CapabilityRetirement(c.Needs); ret == "" {
					t.Errorf("capability %q is registered with no retirement condition; an entry "+
						"born without a death certificate is a squatter, and its column becomes "+
						"permanent by omission rather than by decision", c.Needs)
				}
			}
		}
	}

	// Vacuity floor. A classifier that stopped setting Needs would leave this test
	// comparing an empty set against a registry and agreeing perfectly — the
	// comparisons-need-a-vacuity-check rule, and the exact shape that let an empty
	// keyword extraction drift-check clean (0009).
	//
	// The floor is now the *only* thing keeping this test non-vacuous, which the
	// retirement is what changed: while the registry was non-empty, an emptied `seen`
	// would also have tripped the used-members loop below. With the registry empty that
	// loop iterates zero times and asserts nothing, so a count floor is load-bearing
	// where it used to be belt-and-braces — and *a floor below the list's own length is
	// decoration*, so it is the measured 1236 rather than a token 1.
	if len(seen) == 0 {
		t.Fatal("no command in the corpus needs any capability, so this test asserted " +
			"nothing; classify has stopped setting Needs and the fourth verdict is dead code")
	}
	if n := seen[CapWatReader]; n < 1200 {
		t.Errorf("only %d commands need %s, want >=1200; the classifier has stopped "+
			"recognizing quote forms, and every arm above would then be agreeing about "+
			"almost nothing", n, CapWatReader)
	}
	// And the registry's own members must be *used*, or an entry is a debt nobody owes:
	// a stale capability overstates what the engine is waiting on.
	for _, c := range RegisteredCapabilities() {
		if seen[c] == 0 {
			t.Errorf("capability %q is registered but no command needs it; remove the "+
				"entry or the registry overstates the engine's outstanding work", c)
		}
	}
	for c, n := range seen {
		t.Logf("capability %s: %d commands", c, n)
	}
}

// TestQuoteFormsHaveTheirReader is the drain tripwire (decision 0010), re-pointed at the
// case that is still wrong now that the reader has landed.
//
// It was TestQuoteFormsAwaitTheirReader, and it asserted two things: that an undeclared
// capability scored `unimplemented`, and that declaring CapWatReader with no reader wired
// panicked. The first half's subject **dissolved** — the capability is declared, so the
// gap it measured cannot exist for wat-reader any more — and the second half's *risk* did
// not: a caller can still declare the capability and hand the run loop a nil ReadTextFunc,
// which is the registry running ahead of the engine in the only form still available. *A
// tripwire whose subject dissolves is re-pointed, never closed* — closing this as "no
// longer applicable" would retire a live risk on a technicality.
//
// So the three properties below are the same obligation aimed at what exists now: the
// eleven vectors score, they score as *verdicts* rather than as the fourth column, and
// the declared-without-supplied combination still panics.
func TestQuoteFormsHaveTheirReader(t *testing.T) {
	requireSuite(t)
	s, err := ParseFile(filepath.Join(suiteDir, "obsolete-keywords.wast"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := run(s)
	// The drain, at the file that prompted the capability. Eleven quote vectors, each
	// naming a mnemonic the keyword table omits, and each one now answered.
	if r.Unimplemented != 0 {
		t.Errorf("%d vectors still unimplemented in obsolete-keywords.wast; the reader is "+
			"declared, so nothing in this file has an excuse left", r.Unimplemented)
	}
	if r.Pass != 11 {
		t.Errorf("obsolete-keywords.wast scored %d pass, want 11 — this is the file whose "+
			"eleven `unknown operator` vectors are the reject-direction contract (0009), and "+
			"a count below 11 means the keyword table is admitting a mnemonic the spec does "+
			"not\n%s", r.Pass, r.Board())
	}
	if r.Fail != 0 {
		t.Errorf("%d fail in obsolete-keywords.wast:\n%s", r.Fail, r.Board())
	}

	// The re-pointed half. Declaring a capability whose component was not supplied must
	// fail loudly rather than score against a nil entry point. Recovered deliberately:
	// the panic *is* the assertion.
	//
	// Falsified while re-pointing it, per *break the control to know its green is
	// falsifiable*: with the nil check removed the run loop dereferences nil and panics
	// anyway, which would have made this pass for the wrong reason — a nil-map read, not
	// a diagnosis. So the message is asserted, not merely the panic.
	func() {
		defer func() {
			switch v := recover(); {
			case v == nil:
				t.Error("declaring CapWatReader with no ReadTextFunc did not panic; the " +
					"registry is allowed to run ahead of the engine, so a vector could be " +
					"scored against a component that was never handed over")
			case !strings.Contains(fmt.Sprint(v), "no ReadTextFunc was supplied"):
				t.Errorf("panic does not name the missing component: %v", v)
			}
		}()
		_ = s.RunWith(decode, nil, isGated, CapWatReader)
	}()
}

// TestNoCapabilityOutlivesItsComponent is the second structural control on the fourth
// verdict (ruling: chat-Claude, PR #58), and it is temporal where `gated`'s is spatial.
//
// `gated` gets its anti-dumping-ground guarantee from a lane: turn every gate on, and
// the gated count must be zero, so a vector parked in the third verdict is
// simultaneously being failed somewhere. That does not transfer, and the reason is
// exactly why this category exists — absence-by-construction has nothing to switch on.
// You cannot enable a component that has not been written.
//
// So the guarantee is delivered as a tripwire on the registry's *lifecycle* instead.
// Two directions, and both are failures:
//
//   - A capability the engine declares must not still be registered as missing. The
//     registry would be claiming the engine is waiting on something it has.
//   - A capability the engine declares must have drained its population to exactly
//     zero. Landing a component that leaves some of its vectors in the fourth column
//     is the disappearance the whole ruling exists to prevent: the component exists,
//     so those vectors have no excuse left, and they must be pass or fail.
//
// The two together are why retirement is a single motion — declare the capability,
// delete the entry, and the population is zero because nothing can produce it. Doing
// half of it fails here rather than being noticed at review time, which is what makes
// the entry's stated Retires condition an assertion rather than a promise.
func TestNoCapabilityOutlivesItsComponent(t *testing.T) {
	requireSuite(t)

	engine := EngineCapabilities()

	// Vacuity, and **the retirement moved which set has to be non-empty** — which is the
	// finding this floor recorded by failing. It read "the registry must be non-empty,
	// because the engine's set is empty by design", and after the first retirement both
	// halves of that sentence are false: the registry is empty by design and the engine's
	// set is what carries the content. Left as written it would have Fataled on the state
	// it exists to certify, which is a control asserting the absence of its own success.
	//
	// The invariant that survives the swap is the one to floor: **the control's two loops
	// iterate over `engine`, so `engine` is what must be non-empty.** A capability
	// registry emptied without any component landing would leave both sets empty, and
	// this catches it from the side that will keep being true as capabilities are added.
	if len(engine) == 0 {
		t.Fatal("the engine declares no capabilities, so both loops below iterate zero " +
			"times and this test asserted nothing; either the registry was emptied without a " +
			"component landing, or engineCapabilities lost a declaration")
	}

	for _, c := range engine {
		if _, stillRegistered := CapabilityIssue(c); stillRegistered {
			ret, _ := CapabilityRetirement(c)
			t.Errorf("the engine declares %q but it is still in capabilityIssues; retirement "+
				"is one motion, and this is the half that was skipped.\n  its stated condition: %s",
				c, ret)
		}
	}

	// The population check, over the whole corpus rather than the board: a declared
	// capability leaving vectors behind on an unadmitted file is the same defect.
	pop := map[Capability]int{}
	for _, p := range suitePaths(t) {
		s, err := ParseFile(p)
		if err != nil {
			t.Errorf("%s: parse: %v", p, err)
			continue
		}
		r := run(s)
		for c, n := range r.UnimplementedByCapability {
			pop[c] += n
		}
	}
	for _, c := range engine {
		if n := pop[c]; n != 0 {
			t.Errorf("the engine declares %q, yet %d vectors are still scored unimplemented "+
				"against it; a component that lands without draining its column to zero has "+
				"converted a deferral into a disappearance", c, n)
		}
	}

	// And the corpus's own report, over the union rather than over the registry: after a
	// retirement the registry is the *shorter* list, and logging only its members would
	// stop reporting exactly the capability whose drain this test just certified.
	for _, c := range RegisteredCapabilities() {
		issue, _ := CapabilityIssue(c)
		t.Logf("registered %s (%s): %d vectors outstanding, engine has it: %v",
			c, issue, pop[c], EngineHas(c))
	}
	for _, c := range engine {
		t.Logf("declared %s: %d vectors outstanding (must be 0), still registered: %v",
			c, pop[c], func() bool { _, ok := CapabilityIssue(c); return ok }())
	}
}
