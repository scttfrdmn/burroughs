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
//
// **And again, 68 files to 253, when the bare `(module <wat body>)` form became scorable
// (#69).** Same sentence as the paragraph above, and that repetition is the finding: this
// is the third time a capability landing has moved the *file set* rather than the command
// mix inside a fixed set, because the selector's question is "does this file hold one
// scorable command". 253 of 257 vendored files now do, and the four that do not were
// *printed*, not predicted — three are the interpreter's and one is a real gap:
//
//	data1.wast              14 assert_trap        the interpreter's
//	memory_size3.wast        2 assert_invalid     the validator's
//	unreached-invalid.wast 121 assert_invalid     the validator's
//	inline-module.wast       3 bare fields        **not** a later stratum's — see below
//
// `inline-module.wast` is the honest miss. Its commands are `(func)`, `(func)`, `(memory
// 1)` at top level: the reference's `inline_module` production (parser.mly:1447), where a
// script's module wrapper is elided and bare fields *are* the module. The classifier keys
// on the `module` head, so it sees three unrelated forms rather than one module and calls
// each unsupported. That is a harness gap of the same species #69 just closed, and it is
// left open deliberately — recognizing it means the classifier must fold a run of adjacent
// field forms into one synthetic module, which is a classification decision and not a span
// one. Named here rather than folded in, because a file excluded for a reason nobody wrote
// down is indistinguishable from one excluded correctly.
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
	// silently, which is the vacuity hole one step short of empty. 60 left room for
	// upstream churn without leaving room for a selector that mostly stopped selecting.
	//
	// **Raised 60 → 240 by #69**, and the raise is the rule applied to itself. A floor of
	// 60 against 253 selected files is the same defect it was written to prevent, one
	// magnitude up: the selector could lose 193 files — every file the bare-module
	// admission just brought in, and therefore every vector the 4122 pass floor rests on
	// — and this guard would still report success. *A floor left at its historical value
	// is a floor that stopped bounding anything*, so it moves with the measurement or it
	// is decoration. 240 of 253, the 13-file margin being upstream churn room; the four
	// legitimately-excluded files are itemized at the top of this function, so a fifth
	// appearing is a fact somebody has to write down.
	if len(files) < 240 {
		t.Fatalf("boardFiles selected only %d files, want >=240 — the selector is not "+
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

// readText is the wat entry point the board scores: the lexer, and now the module-field
// grammar above it (#62).
//
// **What this does not do is still the reason the reject-direction column reads the way it
// does**, only one stratum further up. `text.ReadModule` lexes, then parses module fields
// and the type algebra, and stops at the first instruction — so a vector whose
// malformedness lives in an instruction body, in a validator (`alignment`, `type
// mismatch`), or in name resolution (`unknown func`) is still a *fail*, in a named bucket,
// and not a skip. That is the bucketed-failures discipline: what remains is the work plan
// for #63/#64 and the validator, not a debt hidden behind a fourth verdict.
//
// It is called on the raw source and not on a pre-lexed token slice on purpose: ReadModule
// owns the lex-then-parse ordering (see cursor's header for why the ordering is load-bearing),
// and a harness that lexed first would be reimplementing that ordering in a second place.
func readText(src []byte) error { return text.ReadModule(src) }

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

		// The board's last `fail` became a `gated`, which is the gates doctrine working
		// rather than a deferral bought cheaply: the vector is an `assert_malformed` on an
		// **array type's** field mutability byte, and an array type is GC's. With the gate
		// off the module is declined for the feature before the mutability byte is read,
		// so the engine never gets to the question the vector asks (#86).
		//
		// This is exactly the entry the all-gates-on lane exists to keep honest: with
		// `GC: true` the decline does not happen, the mutability check runs, and
		// `malformed mutability` is reported — so the vector is *passed* there, not parked.
		// TestAllGatesOnLeavesNothingGated is what proves that, and it is why a gated
		// verdict here cannot become a disappearance.
		"binary-gc.wast": {
			1: "gc: an array type's fieldtype, whose mutability byte is what the vector asserts",
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
// a listed vector that has started *failing* means the reader regressed on something it
// used to accept — an accept-direction defect, the class no negative vector can falsify
// (decision 0007) — and it would otherwise read as this list going stale.
//
// # Retirement is a third state, and it is not the same as either
//
// #62's parser turned one of the seven from unearned into a *named boundary*:
// `comments.wast:83` is a quote form whose payload has instruction bodies, so ReadModule now
// reaches it and stops with `unimplemented`. The reverse-direction arm caught that, which is
// the arm working — but its diagnosis was wrong, and taking it at face value would have been
// the worse outcome. It reads any error as an accept-direction defect, and there are now two
// ways a listed vector can stop passing:
//
//   - the reader **regressed**: it rejects a module the reference accepts, for a reason it
//     believes. That is the defect, and it stays a hard error.
//   - the reader **advanced past the lexer and stopped short honestly**, declaring the
//     stratum boundary. The pass was never earned; it is now not claimed at all, which is
//     strictly better than an unopposed green and is exactly the shrink-to-zero this test
//     was written to expect.
//
// Collapsing the two would be dishonest in whichever direction it resolved: treat retirement
// as a defect and the board goes red for progress; treat any error as retirement and a real
// accept-direction defect hides behind the same arm. So the two are separated by the one
// thing that distinguishes them — whether the error *names itself as unread* — and the
// boundary is not an excuse a vector can grow into, because a retired entry must be removed
// from the list rather than annotated in it, and `retiredThisStratum` is floored and counted
// like everything else here.
//
// # Retirement is reversible, and #63 reversed it
//
// `comments.wast:83` went back to `unearned` when #63's instruction grammar read the payload
// #62 had stopped at. The switch's retired arm offers two dispositions for an entry that
// starts passing again — the boundary moved, or the pass is now earned — and the choice
// between them is not "did the reader get further", it is **what does the vector assert**. A
// bare `(module quote ...)` asserts *validity*, so the only stratum that can earn its pass is
// the one that can disagree about types; a parser that reads the payload and says nothing is a
// better-informed silence, not a verdict. Retiring it a second time on the strength of the
// parser alone would have been the omission-pass laundered through a list built to expose it.
//
// The general shape, since this is the second control in the project to hit it: **a state a
// mechanism was built to drain can refill, so its account is kept on the sum and its list is
// not deleted when it empties.** Sibling of the re-pointed tripwire (#33) — there an
// obligation outlived its subject, here a state outlived its emptying.
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
		// Returned to this list by #63, and the round trip is the finding. #62 retired it by
		// reaching the payload's instruction bodies and stopping short; #63's flat instruction
		// grammar reads them, so the vector is accepted again — which is the retired arm's
		// *first* named outcome, "the boundary moved and the entry belongs back on the
		// unearned list", not its second.
		//
		// It is the first and not the second because a bare `(module quote ...)` asserts its
		// source is **valid**, and validity is the validator's word. #63 raised the answer from
		// "it lexes" to "it parses", which is a better reason to be silent and still not the
		// question the vector asks: `(func (export "f1") (result i32) (i32.const 1) (return
		// (i32.const 2)))` has a result type to agree with and a return to typecheck, and
		// nothing in the engine yet does either. The stratum that owns it is the validator.
		//
		// So the pass is unearned again rather than earned, and the reason it was ever retired
		// was the shape of #62's boundary, not the vector's difficulty. That is worth keeping:
		// an entry can leave `retired` in either direction, and only naming which one keeps the
		// list from reading as monotone progress.
		"comments.wast": {
			83: "instruction bodies now parse, but the vector asserts *validity* — the result " +
				"type and the return want a typechecker (the validator's stratum)",
		},
	}

	// retired lists the entries a parser took off the unearned list by reaching them and
	// declaring the boundary — the shrink this test was written to expect, recorded rather
	// than deleted so the arithmetic against the original seven stays checkable.
	//
	// **Empty as of #63**, and empty is not the same as finished. Its one entry,
	// `comments.wast:83`, went back to `unearned` rather than away: see the note there. The
	// map stays, and stays in the arithmetic, because retirement is a state vectors will keep
	// entering as strata land — #64's folded forms and the validator both have candidates —
	// and a list deleted the first time it empties is a mechanism that has to be re-derived
	// by whoever needs it next.
	retired := map[string]map[int]string{}

	// Vacuity floor. A list of unearned passes that finds nothing is indistinguishable
	// from a board with none, and the second is the state this test exists to detect the
	// arrival of. Floored at the list's own length rather than at 1, per *a floor below
	// the list's own length is decoration*.
	want, wantRetired := 0, 0
	for _, m := range unearned {
		want += len(m)
	}
	for _, m := range retired {
		wantRetired += len(m)
	}
	// The sum is what must hold at seven, not either part: an entry moving from unearned to
	// retired is progress, an entry *vanishing* from both is the list going stale, and only
	// the sum can tell those apart. Seven-plus-zero today — and it was six-plus-one under
	// #62, which is the clearest demonstration of why the invariant is on the sum: the
	// entry has now crossed in both directions and the guard never moved.
	if want+wantRetired != 7 {
		t.Fatalf("the unearned and retired lists hold %d+%d entries, want 7 between them; "+
			"the count is quoted in the pass floor's account and in PR #61, so the two must "+
			"not drift — and an entry that left both lists left without an account",
			want, wantRetired)
	}

	seen, seenRetired := 0, 0
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
			_, wasRetired := retired[f][c.Line]
			switch {
			case err == nil && !listed && !wasRetired:
				t.Errorf("%s:%d is a bare (module quote ...) that the reader accepts and is "+
					"not in either list; it passes because nothing above the parser's "+
					"current stratum can disagree with it, and a pass arrived at by "+
					"omission has to be named", f, c.Line)
			case err == nil && wasRetired:
				t.Errorf("%s:%d is listed as retired but the reader accepts it again; a "+
					"retired entry is one whose pass was *withdrawn*, so an acceptance "+
					"means either the boundary moved and the entry belongs back on the "+
					"unearned list, or the pass is now earned and the entry goes away",
					f, c.Line)
			case err != nil && listed:
				// Two ways this happens and they are not the same finding. A named boundary
				// is the reader declining to claim a pass it cannot earn; anything else is
				// the reader rejecting a module the reference accepts.
				if strings.Contains(err.Error(), "unimplemented") {
					t.Errorf("%s:%d is on the unearned list but the reader now stops short "+
						"of it with a named boundary (%v); that is progress, not a defect — "+
						"move the entry to `retired` with the stratum that owns the rest",
						f, c.Line, err)
				} else {
					t.Errorf("%s:%d is listed as an unearned pass but the reader now "+
						"rejects it (%v); a valid module rejected is an accept-direction "+
						"defect, which no negative vector in the suite can catch",
						f, c.Line, err)
				}
			case err != nil && wasRetired:
				// A retired entry must keep stopping short *honestly*. If it starts failing
				// for a reason the reader believes, it is the accept-direction defect the
				// unearned arm watches for, arriving one list over.
				if !strings.Contains(err.Error(), "unimplemented") {
					t.Errorf("%s:%d is retired behind a stratum boundary but now fails with "+
						"%v; a retired entry that stops naming itself unread is a valid "+
						"module being rejected on the merits — the accept-direction defect, "+
						"hiding in the list that was supposed to account for its silence",
						f, c.Line, err)
					continue
				}
				seenRetired++
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
	if seenRetired != wantRetired {
		t.Errorf("found %d of %d retired entries; a retired vector the loop never reached "+
			"means the file left the board, and the account of the original seven no longer "+
			"has anything behind it", seenRetired, wantRetired)
	}
	t.Logf("%d bare (module quote ...) forms pass unearned, %d retired behind a named "+
		"stratum boundary (#63/#64); 7 originally", seen, seenRetired)
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
	// **This floor was 798 against an actual 4178** — stale by 3380, which means it could
	// not have caught a regression that erased four fifths of the lane. It was set in #56
	// (`git log -S`), 15 commits back, and the text strata that landed since moved the
	// count past it without anything noticing. Found by *reading the printed total next to the constant* while raising it
	// for #86, not by any control: nothing asserts that a floor is close to what it
	// floors, so a floor left behind by a large jump degrades silently into decoration.
	// The same defect class as a vacuity floor that passes on an empty set — the
	// comparison runs, agrees, and says nothing — and the reason the discipline says a
	// control's green must be falsifiable: this one was green at 798 whether the engine
	// worked or not.
	//
	// The general form, worth stating because it is not the same as "keep numbers
	// current": **a floor's distance from its measurement is itself a claim, and an
	// unasserted distance is where the assertion leaks out.** Raised to the measured
	// value here, with #86's +1; whether it should be *checked* for staleness is **#87**,
	// filed rather than decided in a PR about the type section.
	const allOnPassFloor = 4178
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
	//
	// # 60872 after the bare-module admission (#69), was 26742
	//
	// Raised a second time, same licensed reason — the corpus grew — but the *shape* of the
	// growth is different from the quote admission's and the difference is the whole
	// account. Retaining a source span made `(module <wat body>)` askable, and that changed
	// two things at once:
	//
	//   - **Within the 68 files already on the board**, the column *fell* by exactly the
	//     bare-module count: 26742 → 25623, −1119, with pass +1114 and fail +5. Net zero on
	//     the total, which is the identity #69's definition of done asked for.
	//   - **185 further files entered the board**, because boardFiles selects on "has at
	//     least one scorable command" and these files had none until now. They bring 1016
	//     pass, 17 fail — and 35240 unsupported, since a file admitted for its module forms
	//     also brings its assert_return/assert_invalid population with it.
	//
	// So 60872 = 25623 + 35240 + 9, and the +9 is the `(module definition …)` /
	// `(module instance …)` forms newly *classified* as unsupported rather than handed to
	// the wat reader (see classify — asking the wrong reader manufactured 9 of the first 22
	// reds). Both movements are stated because reporting only the first would be the
	// invisibility decision 0010 exists to prevent: the honest sentence is "the column grew
	// by 34130 while the population it was measuring shrank by 1119", and a single number
	// cannot say that.
	//
	// The ceiling is deliberately *not* split per-population, though the temptation is
	// real. A per-file or per-cohort ceiling would bind tighter, and it is the right next
	// move if this number is raised again — noted rather than done, because #69 is
	// board-shape work and a ceiling redesign is its own decision.
	const unsupportedCeiling = 60872
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
	// **Both arms are named, and neither is a `default`.** This switch used to send the
	// text kinds one way and everything else to `default`, and #69 broke it in the one run
	// before this rewrite: KindModuleText landed in `default`, so 13 *text* reds were
	// reported as **decoder** failures and tripped binaryFailCeiling at 14. The instrument
	// whose entire purpose is keeping the two layers apart mixed them — *an error from the
	// wrong layer is evidence about where structure was lost*, pointed at a test rather
	// than at the engine.
	//
	// A `default` arm is what made that silent: it absorbs every Kind added later and
	// assigns it a layer by omission. So both arms are explicit and an unrecognized kind is
	// a **loud failure** rather than a decoder failure. That is the same move as the
	// unregistered-capability panic — a classification the harness did not decide is a stop,
	// not a quietly larger number.
	binaryFail, textFail := 0, 0
	for _, fs := range aggBuckets {
		for _, f := range fs {
			switch f.Kind {
			case KindModuleQuote, KindModuleText, KindAssertMalformedText:
				textFail++
			case KindModuleBinary, KindAssertMalformed:
				binaryFail++
			case KindUnsupported:
				t.Errorf("a KindUnsupported command produced a failure bucket entry at "+
					"line %d (%q); unsupported commands are not scored, so this is the "+
					"run loop losing track of a verdict", f.Line, f.Expect)
			default:
				t.Errorf("failure of unhandled kind %v at line %d (%q) — a new Kind was "+
					"added without assigning it to the binary or text arm, so its "+
					"failures would have been charged to whichever ceiling this switch "+
					"defaulted to", f.Kind, f.Line, f.Expect)
			}
		}
	}
	if binaryFail+textFail != totalFail {
		t.Errorf("fail partition sums to %d but the column is %d; a failure escaped both "+
			"arms, so one of the two ceilings below is watching a subset it cannot name",
			binaryFail+textFail, totalFail)
	}

	// **0 at the measured revision, and it was 1 for the whole life of this ceiling.**
	// The one member was binary-gc.wast:1, reported as "malformed function type: 0x5e"
	// under an expected "malformed mutability" — an *array* type read as a malformed
	// functype, because the type section decoded `functype` where the reference decodes
	// `rectype` (#86). Unmoved by the wat reader and unmoved again by #62's parser, which
	// is what the split column was for and what made this one durable enough to diagnose.
	//
	// It is 0 rather than gone because the ceiling's job is now the other direction:
	// **both columns are zero, so any new fail in either is a regression by definition**,
	// and a ceiling at 0 is the strongest form of that claim. Printed, not deduced:
	// `binaryFail=0 textFail=0` with the fix, `1` and `0` without it — the falsification
	// pass ran both ways, because a ceiling lowered to a number the code already meets
	// asserts nothing (which is how this ceiling was briefly wrong at 1 in #83).
	//
	// The vector itself is now a *gated* verdict on the default board, allowlisted in
	// TestNoGatedVectorIsUnlisted with the feature named, and **passed** in the
	// all-gates-on lane, where 4178 pass / 0 fail / 0 gated.
	const binaryFailCeiling = 0
	if binaryFail > binaryFailCeiling {
		t.Errorf("decoder failures rose to %d, ceiling %d — a defect landed in the binary "+
			"decoder; this ceiling is deliberately not shared with the text column so that "+
			"a decoder regression cannot hide inside the text column's unwritten grammars",
			binaryFail, binaryFailCeiling)
	}

	// **391 at the measured revision, down from 600 when the module-field parser landed
	// (#62).** This is a **work plan with a ceiling** rather than a defect count: every
	// member is a vector the reader reached and could not answer, bucketed by the spec
	// string that names what is missing. It may only fall, and it falls as the remaining
	// strata land. The buckets printed above are the order to take them in.
	//
	// The fall is 209 vectors, itemized rather than absorbed: 176 `malformed UTF-8
	// encoding` (the whole bucket, at the `name` production), 12 `import after
	// {function,memory,table}`, 10 `duplicate {memory,local,table,func,global,field}`, 6
	// `unexpected token`, 1 `malformed UTF-8` (id.wast:31, the `var` site) — 210 answered
	// against 1 newly withdrawn, `comments.wast:83`, whose unearned pass the parser
	// replaced with a named boundary (see TestBareQuoteFormsPassUnearned). Net 209.
	//
	// Against #62's pre-registered 221–225 that is an **under-delivery of 12–16**, and the
	// cause is one shape rather than a spread: seven vectors whose check this stratum
	// implements sit behind an instruction body in the *same module*, so the check is never
	// reached — `imports.wast:692,696,700,704` (`import after global`, all four with a
	// `(global i32 (i32.const 0))` initializer ahead of the import), `global.wast:685`
	// (`duplicate global`), `start.wast:102` (`multiple start sections`), `func.wast:447`
	// (`unknown type`). They arrive with #63/#64's instruction grammar at no extra cost to
	// it, and they are the concrete form of the forecast's own caution — the classifier
	// asked whether a *vector* was instruction-bodied, and the question that decides
	// reachability is whether the *module* is.
	//
	// # 97 after #63's flat instruction grammar, and the 294 itemized
	//
	// **391 → 97 text, the fall being 294 vectors.** Six buckets emptied and one shrank, and the
	// account is per-bucket rather than net, measured by re-running the board at 1f86fa1 and
	// diffing the bucket tables:
	//
	//	92  alignment                          → 0   memarg's `align=`, the largest single win
	//	84  unexpected token                   146 → 62
	//	55  constant out of range              → 0   constImm at all four widths
	//	22  alignment must be a power of two   → 0
	//	15  i8 constant out of range           → 0   laneidx, nat8-reduced
	//	 8  wrong number of lane literals      → 0   vecConst
	//	 5  wrong number of lane indices       → 0   laneIdxList
	//	 4  import after global                → 0   ┐ four of the seven #62 predicted would
	//	 1  multiple start sections            → 0   │ arrive here free; `func.wast:447`
	//	 1  duplicate global                   → 0   ┘ (`unknown type`) is the one that did not
	//	 1  (module quote ...) must read        → 0   comments.wast:83, the retired pass returning
	//
	// **Against #63's pre-registered 353 that is an under-delivery of 59**, and unlike #62's it
	// is *not* one shape. Partitioned by mechanism from the engine's own error text — the failure
	// bucket keys name what the suite wanted, which is the wrong key for asking why we did not
	// deliver:
	//
	//   - **92 are `unimplemented: instruction body`, i.e. #64's**, and they are the forecast's
	//     real error. The 353 was a count of vectors whose *fault* lives in one of #63's readers;
	//     92 of them reach that fault only through a `block`/`loop`/`if`/`try_table` this stratum
	//     does not read. Same defect ownership, unreachable extent — the seam ruling put the
	//     `expr1` minimal arm here for exactly this reason, and it was not enough because folded
	//     *plain* instructions are #63's while folded *block* instructions are #64's.
	//   - **5 need a type context**, which is neither stratum's: `func.wast:601,608,615` and
	//     `:447` want `(type $sig)` compared against inline params/results
	//     (`inline_functype_explicit`, parser.mly:246) and a type index space to resolve `(type 2)`
	//     against. These are the five accept-direction members of the column — we accept modules
	//     the reference rejects — and they are the reason the fall is 294 and not 299.
	//   - **1 is the decoder's**, `binary-gc.wast:1`, held by binaryFailCeiling above.
	//
	// So the honest reading is that #63's forecast measured its own extent correctly and its
	// *reachability* wrongly, in the same direction #62's did and for a differently-shaped
	// reason. Recorded rather than smoothed: the 92 are #64's inventory, and #64's own forecast
	// starts from them rather than from a fresh classification.
	//
	// # 79 after the flat block family, and the "92 are #64's" above is corrected
	//
	// **97 → 79, the fall being 18 more vectors and the account correcting the one above.** The
	// paragraph naming 92 as #64's inventory was wrong, and it was wrong in the way a reconciliation
	// most easily goes wrong: it read the *surface* of the unanswered vectors instead of checking
	// them against the owning issue's Scope list. #63's Scope names `blockinstr` (parser.mly:726),
	// the block family (:740-:792), `labeling_opt` (:510) and `labeling_end_opt` (:521), and the
	// seam ruling moved the `expr1` minimal arm *in* — it moved nothing out. So the **flat** block
	// forms were always #63's, and the forecast's own table said so: it has a "17 flat" row.
	// Measured rather than re-argued, by classifying each unanswered vector on whether its boundary
	// token is a block keyword or a `(`:
	//
	//	flat boundary    17     #63's, and they were still red
	//	folded boundary  75     #64's, genuinely
	//
	// Landing `blockinstr` answered the 17 exactly, in two buckets:
	//
	//	14  mismatching label   → 0   labeling_end_opt, both arms (block.wast:1484/:1488 et al)
	//	 3  unexpected token    56 → 53  block/loop/if `(param $x …)`, the named-form grave
	//
	// Plus **1 more the controls found rather than the board**: `try_table.wast:366` and `:371`
	// (`(func (catch_all))`, `(func (catch $e))`) were reporting the *boundary* for a clause no
	// production admits in instruction position — 51 rather than 53 in the `unexpected token`
	// bucket, and the general form of that defect is #70.
	//
	// **The corrected partition of what remains, itemized from the engine's own error text:**
	//
	//	75  unimplemented: instruction body   #64's folded arms — the real inventory
	//	 5  accepted (no error at all)         the type context, neither stratum's
	//	 1  malformed function type: 0x5e      binary-gc.wast:1, the decoder's
	//	--
	//	81  … then 79 after the two try_table vectors
	//
	// **Against the 353 the shortfall is now 42, not 59**, and the whole difference is the 17 this
	// paragraph reassigns. The lesson is the one the seam ruling already stated and the
	// reconciliation then ignored: *seams follow defect ownership, not surface form*. Reading a
	// bucket's members off their spelling is the same manoeuvre as reading a test's coverage off its
	// case labels, and it produced a number that was wrong by exactly the vectors the issue was
	// chartered to fix. Check the Scope list, not the mnemonics.
	// # 67 after the derived instruction boundary (#70)
	//
	// **79 → 67, and the 12 are the correction of a "zero vectors" claim I made from five
	// hand-picked probes.** #70's issue said "board effect: none" on the strength of trying five
	// spellings at a Go prompt and generalizing; the figure was wrong by 12, and it was wrong in
	// the way the *cheap-is-a-grammar-claim* rule names: a board figure asserted without the
	// board. What the five probes could not reach is the class itself — **reachable keywords in
	// an unreachable position**. Every one of the 12 leads with a keyword the parser knows
	// (`type`, `param`, `result`, `local`), which is exactly why hand-picking failed: the
	// interesting inputs are not exotic tokens, they are ordinary ones where no instruction may
	// start, and you do not stumble onto those by inventing spellings.
	//
	// The 12, all `unexpected token`, all in func.wast, itemized from the engine's own text:
	//
	//	 6  :559 :566 :573 :580 :587 :594   type-use ordering — `(func (type $sig) (result i32)
	//	                                    (param i32) …)` and its five permutations
	//	 6  :937 :941 :945 :949 :953 :957   field-after-body and misordered fields — `(func (nop)
	//	                                    (local i32))`, `(func (local i32) (param i32))`, …
	//
	// They answer because `func_body` is `instr_list` (parser.mly:1017), which cannot begin with
	// `(param`/`(local`/`(result`/`(type`. The old boundary asked "is this `(` followed by a
	// handler clause?" and said `unimplemented` to everything else; the derived boundary asks
	// "can an instruction start here?" — `startsInstruction`, the union of `plaininstr`'s
	// mnemonics and `expr1`'s nine non-plain arms — and a `(param` in instruction position is
	// now `unexpected token` on the merits. Position-dependence comes free: the boundary only
	// runs where an instruction was *required*, so `(func (param i32))` still parses.
	//
	// **Nothing was withdrawn and no bucket grew**, checked per file rather than on the total:
	// the only line that moved was `func.wast: 6/23 → 18/23`. `unexpected token` 51 → 39;
	// `inline function type` 24, `unknown label` 2, `malformed mutability` 1, `unknown type` 1
	// all unmoved. What remains in func.wast is 4 `inline function type` and 1 `unknown type`,
	// both #64's.
	// # 28 after the folded/sugar stratum (#64, first half)
	//
	// **67 → 28, and the 39 that fell are the folded spellings of a grammar that was already
	// right.** Every `unexpected token` in block/loop/if/call_indirect/return_call_indirect went to
	// zero; the five files moved 3/15→11/15, 3/15→11/15, 11/24→20/24, 0/11→7/11, 0/11→7/11.
	// Nothing withdrawn, checked per file rather than on the total — the only lines that moved are
	// those five, all upward.
	//
	// The sequencing forecast I posted on #64 said **41** and the measurement says 39. The two
	// missing are `token.wast:101` and `:117` (`$l0`, `$l$l` in a `br_table`), which the folded
	// reader now *reaches* and which turn on name **resolution** rather than grammar — they are
	// `unknown label`, and they belong with the 24 below rather than here. A forecast wrong by two
	// in the direction of "I mistook a resolution question for a syntax one" is the same error the
	// #64 partition made twice at larger scale; recorded rather than rounded.
	//
	// **The residue is 28 and every one of them is a semantic question, not a grammatical one:**
	//
	//	24  inline function type   exactly 4 each in block, loop, if, call_indirect,
	//	                           return_call_indirect and func — a six-file × four grid, which is
	//	                           the tell that one production is responsible:
	//	                           `inline_functype_explicit` (parser.mly:238) compares an inline
	//	                           signature against the explicit `(type n)` and needs the type
	//	                           section resolved plus functype equality. #64's second half.
	//	 2  unknown label          token.wast:101/:117 — `$l0` and `$l$l` lex as one VAR, so the
	//	                           label does not resolve. Name resolution, same stratum as above.
	//	 1  unknown type           func.wast:456 — `(func (type 2) (param i32))` where the module
	//	                           defines fewer types. The file holds four `unknown type` vectors
	//	                           and this is the only one in a `(module quote …)`; the other three
	//	                           (:444, :632, :640) are `assert_invalid` on real modules, which
	//	                           the text reader is never handed. Read from the file rather than
	//	                           from the bucket, because a bucket count of 1 against a grep count
	//	                           of 4 is exactly where a citation goes wrong.
	//	 1  malformed mutability   binary-gc.wast:1 — the *decoder's*, not the parser's.
	//
	// The `unimplemented: instruction body` bucket is at **zero**, and that is what retired the
	// boundary itself: with all four `instr_list` arms and all three `instr1` arms read, an
	// `unimplemented` promises a reader nobody will write. See internal/text's expectedInstr for
	// the sweep (493 of 494 admitted leaders consumed) and TestNoInstructionLeaderIsUnread for the
	// re-pointed tripwire.
	//
	// **Pre-registered: the next PR takes this to 3.** The 24 plus the 2 plus the 1 are one job —
	// `typeuse` resolution — leaving only binary-gc.wast:1, which is the decoder's. Stated here so
	// the claim is falsifiable before the work rather than after it.
	//
	// (That pre-registration is **unmet and deferred, not missed**: #69 came first, for the
	// reason in the next block — resolution is a *rejector*, and installing one under a
	// 7-vector accept oracle is the overfitting risk in its purest form. The 27 named above
	// are all still here, unmoved, and the forecast stands for #64's second half.)
	//
	// # 41 after the bare-module admission (#69), was 28
	//
	// **+13, and the forecast was wrong by an order of magnitude in the pessimistic
	// direction.** Pre-registered at 150–400 fails of the then-known 1119, centred near 250,
	// on the reasoning that a parser built against reject vectors plus *seven* accept vectors
	// would over-reject badly — grave #63 (flat `select`, every module containing one
	// rejected, invisible to the board) being exactly what that produces. The measurement is
	// **13 of 2152**. The parser accepts 2130 valid modules it had never been scored against.
	//
	// Recording the miss rather than quietly enjoying it, because the *reasoning* was sound
	// and the conclusion was still wrong: reject-direction construction predicts
	// over-rejection, and the honest lesson is that #62/#63/#64 tracked the reference's arm
	// lists closely enough that following the grammar bought the accept direction too. Also
	// note 1119 → 2152: #69's figure counted bare modules in *board* files, and the corpus
	// holds 2152 once the newly-admitted files are included.
	//
	// **The 13, partitioned by mechanism rather than quoted as one number** — two v0
	// grammar defects and one phase-v3 form, all accept-direction, none of them visible
	// before this admission:
	//
	//	 9  `lane_imms`, parser.mly:661   simd_{load,store}{8,16,32,64}_lane.wast:4 and
	//	   (#76)                          simd_memory-multi.wast:5. `v128.load8_lane 0 (…)` —
	//	                                  our memarg reads the leading `0` as a memory index,
	//	                                  but it is the bare `| laneidx` arm (:673). The
	//	                                  reference multiplies the production out into five
	//	                                  arms "to avoid spurious conflicts"; telling them
	//	                                  apart needs lookahead for a *second* nat.
	//	 3  `elem_list`, parser.mly:1155  elem.wast:539/:573 and array.wast:219. `(elem (ref
	//	   (#75)                          $b) …)` — the offset-sugar branch tests
	//	                                  `at(LParen) && !peek2Keyword(kwItem)`, which claims
	//	                                  a `(ref …)` is an offset and shadows the `reftype
	//	                                  elemexpr_list` arm entirely.
	//	 1  annotations.wast:1            `empty annotation id` — the lexer's, on a module
	//	   (#83)                          whose first field is `(@a …)`.
	//
	// The two grammar defects are engine fixes and are **not** in this PR: #69 is
	// board-shape work and travels alone, per *board-shape changes travel as their own
	// decisions*. They are the work plan this admission exists to produce, and each is
	// filed with the arm it misreads.
	//
	// **The partition above is the corrected one, and the correction is the lesson.** It
	// first read 10 / 2 / 1, with the tenth lane vector hedged as "one further `simd_*_lane`
	// file". There is no tenth: the vector is `array.wast:219`, a `(elem $e (ref $bvec) …)`
	// in a GC module, and it belongs to `elem_list`. Confirmed by running the reader —
	// `(module (type $b (array i8)) (elem $e (ref $b) (ref.null $b)))` errors while `(elem $e
	// func)` returns nil — after *printing the bucket's members*, which is what should have
	// produced the partition in the first place.
	//
	// *Bucket size estimates the reward, not the job* says partition by mechanism before
	// estimating. The failure here was one level in: the partition was made by mechanism, but
	// the file set for each mechanism came from memory of where the defect lived rather than
	// from the board. That is *derive the domain, never enumerate it* applied to the work plan
	// instead of to the engine — and an enumerated file set has a blind spot exactly the shape
	// of the defect one did not know was shared. Print the bucket; do not recall it.
	//
	// # 16 after typeuse resolution and functype equality (#64, second half), was 41
	//
	// **−25, and the forecast was met exactly** — pre-registered on #64 as 41 → 16 before the
	// work, itemized as 24 `inline function type does not match explicit type` plus 1 `unknown
	// type`, with the 2 `unknown label` vectors excluded as a separate mechanism. Both numbers
	// landed on the nose, which is worth stating plainly *and* discounting: the forecast was
	// made after the bucket had been printed and partitioned, so it predicted the size of a set
	// whose members were already known. An exact hit there is bookkeeping, not foresight. The
	// forecasts worth crediting are the ones over unlisted spaces, and this PR's two of those
	// were both wrong (see below).
	//
	// The 25, by file, from a per-file diff against the pre-change board — six files moved, all
	// upward, none losing a pass:
	//
	//	block.wast                12/16 → 16/16   +4
	//	call_indirect.wast        10/14 → 14/14   +4
	//	func.wast                 22/27 → 27/27   +5
	//	if.wast                   21/25 → 25/25   +4
	//	loop.wast                 12/16 → 16/16   +4
	//	return_call_indirect.wast 10/14 → 14/14   +4
	//
	// **The residue is 13 + 2 + 1 and none of it is this mechanism's**: 13 in `(module <wat
	// body>) must read` (#75's 3 `elem_list` and #76's 9 `lane_imms`, plus annotations.wast:1
	// which is #83's, the annotation lexer's), 2 `unknown label` (token.wast:101/:117 — `enter_block` and scoped
	// labels, parser.mly:132-134, deliberately its own PR), 1 `malformed mutability`
	// (binary-gc.wast, the decoder's). So the pre-registration two blocks up — "the next PR takes
	// this to 3" — is met on its own terms: the 24 + 2 + 1 it named as one job turned out to be
	// 25 + 2, the two labels being a different mechanism, and the 13 arrived in between from #69's
	// admission. The 3 it forecast is now 3 = 2 labels + 1 decoder, with #75/#76's 13 alongside.
	//
	// **`non-function type <n>` is implemented and corpus-unreachable.** Zero vectors: measured
	// across the whole corpus, no `assert_malformed` names it, because reaching it needs a struct
	// or array type used as a typeuse with an inline signature and the GC files' subtyping
	// vectors are all `assert_invalid`. It is implemented anyway — it is one of `func_type`'s
	// three outcomes (parser.mly:164-168) and omitting it would report `unknown type` for an
	// index that resolves perfectly well, which is the engine lying about its input. Pinned by a
	// print check (TestNonFunctionTypeMessage) rather than by the board, per *the oracle reads
	// exactly as far as its expected string does*.
	//
	// **Two forecasts over unlisted spaces, both wrong, both in the same direction.** (1) The
	// nesting order of implicitly-interned block types was written into a comment on
	// `orderedTypeUse` as inner-before-outer, correctly, and the code did outer-before-inner —
	// `blockSignature` passed a nil tail so its three callers read the body *after* the signature
	// op was recorded. Caught by a synthetic control, and the board is identical either way. (2)
	// `externtype` was assumed to compare an inline signature against its typeuse; its arms are
	// `typeuse` XOR `functype` (parser.mly:1226-1248), so two test rows asserting a mismatch
	// error there failed against a parser that was right. Both were found by *printing what the
	// code returns* rather than by reasoning, which is the same instrument that found #70's 12.
	//
	// Eleven controls in internal/text/typetable_test.go, each falsified by introducing the
	// defect it names; **nine of the eleven defects leave this board unchanged**, the two
	// exceptions being an over-rejecting resolver (4147 → 4145, imports.wast) and the block arms
	// wired to the create-helper (4147 → 4135). The table is in that file's header. That ratio is
	// the honest measure of what this bucket's fall is evidence for.
	//
	// # 7 after lane_imms' bare laneidx arm (#76), was 16
	//
	// **−9, and the forecast said 10.** #76 named eight `simd_{load,store}{8,16,32,64}_lane.wast`
	// files plus `simd_memory-multi.wast` — nine — and then hedged with "(one further `simd_*_lane`
	// file; the exact set is printed by the bucket)". There is no tenth: the per-file diff shows
	// exactly those nine going 0/1 → 1/1 and nothing else moving. The hedge was the error, and it
	// is the honest kind — the issue said the set was to be *printed*, and printing it is what
	// settled the count. A forecast that names its own oracle is falsifiable by consulting it.
	//
	// Residue 7 = 4 `(module <wat body>) must read` + 2 `unknown label` + 1 `malformed
	// mutability`. The 4 are `annotations.wast:1` (#83's), `array.wast` 1 and `elem.wast` 2
	// (#75's `elem_list` reftype arm) — so #75 is the whole remainder of that bucket, and the
	// bucket falls to 1 when it lands, exactly as #76's definition of done predicted.
	//
	// # 4 after elem_list's shadowed reftype arm (#75), was 7
	//
	// **−3, and the forecast said 2.** #75 named `elem.wast:539` and `:573`. The third,
	// `array.wast:219` — `(elem $e (ref $bvec) …)`, a reftype naming a *defined* type rather than
	// `func` — was in the same bucket the whole time and was not listed, because the bucket key is
	// the expected spec string and the string says nothing about which arm broke. Found by printing
	// every failing module's error rather than by trusting the issue's list: *bucket size estimates
	// the reward, not the job*, and this is the same lesson from the other side — a partition can
	// be finer than the issue that named it, not just coarser.
	//
	// The bucket is now **1** (`annotations.wast:1`, #83's), which is what #75's and #76's
	// definitions of done both predicted, from opposite ends of the same 13.
	//
	// Residue 4 = 1 `(module <wat body>) must read` + 2 `unknown label` + 1 `malformed mutability`.
	// **Nothing left in the text bucket is a typeuse, lane or elem question**: the labels are
	// `enter_block` and scoped labels (parser.mly:132-134), the mutability one is the decoder's, and
	// annotations is the lexer's.
	//
	// # 2 after symbolic label resolution (#80), was 4
	//
	// **−2, forecast 2, and this one was made against a printed set rather than a guessed one.**
	// `token.wast` 59/61 → 61/61 and the `unknown label` bucket closes. What made the forecast safe
	// is the measurement in #80: matching every `unknown *` vector's module body for a symbolic name
	// in a use position the same module never binds returns **exactly two rows across all 253
	// files**, both of them these. So the fix's reach was known before it was written, and the two
	// vectors are the whole population rather than a sample of it.
	//
	// That measurement is also what kept the change small. Read literally the reference resolves
	// *every* symbolic index in the parser — the lookup category is a parameter of `idx`
	// (parser.mly:487-489) and all 83 `plaininstr` arms supply one — which would have made this a
	// job spanning nine index spaces. The suite says only labels are reachable: numeric indices are
	// `nat32 $1` with no lookup, so all 13 `assert_invalid "unknown label"` vectors are `(br 1)`-
	// shaped and validation's, and the remaining names (`global.wast:668`'s forward `$g2`) are bound
	// later in the module and need #64's deferred phase. Labels are the one space whose scope is
	// *lexical*, so they resolve where they are read. Splitting at that seam is what made the
	// reachable half reachable now.
	//
	// **The residue is zero, and this ceiling is now the assertion that it stays there.** The
	// board's remaining fail is `malformed mutability` in `binary-gc.wast`, which is a
	// `(module binary ...)` vector and therefore charged to `binaryFail` — so *no* failing
	// vector on the board is a text-kind command any more.
	//
	// The ceiling was first written as 1 here, reasoning from the board's total of one fail.
	// That is the wrong quantity: this ceiling counts the *text* partition, and the one
	// survivor is in the other one. Caught by the falsification pass rather than by reading —
	// reverting #83's fix left `textFail` at exactly 1, so a ceiling of 1 sat green over the
	// defect it was being lowered to catch. **A ceiling is a claim about a partition, so it is
	// read off the partition, not off the total** — the same error as scoping a control to the
	// sample instead of the space, one aggregation level up. Printed, not deduced: `textFail=0
	// binaryFail=1` with the fix, `1` and `1` without it.
	//
	// **The residue was 2, and the second one was ours after all.** `annotations.wast:1` was
	// attributed to the lexer and dismissed as somebody else's for three PRs — the sentence above
	// said "neither is the text parser's" and it was half wrong, because the vector *is* this
	// package's and the attribution was to an issue number about the CHANGELOG. Grave #83. The
	// number was never checkable: this file's provenance guards resolve a `.wast:N` citation
	// against the suite, and nothing resolves an issue number to its subject, so a bare `#NN` in
	// prose is exactly the drifted-citation defect with the machine-checked half removed. It got
	// quoted forward five times here and three times in the changelog because quoting is cheaper
	// than checking. **A ceiling that names the residue is asserting a diagnosis, and a diagnosis
	// is falsifiable** — this one was falsified by running the vector, which took one probe.
	const textFailCeiling = 0
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
	//
	// **1628 = 1419 + 210 − 1 after #62's module-field parser**, and the arithmetic is the
	// point: 210 vectors answered on the merits, and **one pass given back** —
	// `comments.wast:83`, an unearned pass the parser retired by reaching it and declaring
	// the boundary. A floor is normally raised by only counting what was won; this one
	// records the withdrawal in the same expression, because netting it out would let a
	// green claim credit for a vector the board no longer answers. The unearned six are
	// still six, still named, and still not netted out. See textFailCeiling above for the
	// itemized reconciliation against #62's 221–225 forecast.
	//
	// **1922 = 1628 + 294 after #63's flat instruction grammar**, and this time the two columns
	// move by the same number in opposite directions: 294 answered, nothing withdrawn. The
	// per-bucket account is at textFailCeiling above.
	//
	// The seven unearned quote forms are **seven again, not six**, and that is a real change
	// rather than a rounding of the sentence above. `comments.wast:83` went back onto the
	// unearned list: #62 retired it by stopping at the instruction body, #63 reads that body, so
	// the vector is accepted again — and a bare `(module quote ...)` asserts *validity*, which
	// wants a typechecker. So the pass is unearned once more rather than earned, the withdrawal
	// recorded in #62's `− 1` is handed back, and the 294 is a gross figure that needs no
	// netting. TestBareQuoteFormsPassUnearned holds the sum at seven and has the argument.
	//
	// **1941 = 1922 + 17 + 2 after the flat block family**, and the split matters more than the sum:
	//
	//	17  blockinstr and the block family — #63's own Scope, mis-assigned to #64 in the
	//	    reconciliation above until the flat/folded classification was measured
	//	 2  try_table.wast:366/:371, found by a control rather than by the board — the boundary
	//	    was claiming a handler clause in instruction position, which #70 generalizes
	//
	// Nothing withdrawn either time, so this is gross like the 294. The two-from-a-control rows are
	// worth naming separately: they are vectors the *suite* had all along and the *board* could not
	// point at, because a bucket keyed on the expected string cannot distinguish "we have not
	// written that reader" from "we are wrong about which reader would answer it".
	// **1953 = 1941 + 12 after the derived instruction boundary (#70)**, gross again — nothing
	// withdrawn, checked per file. All 12 are func.wast field-ordering vectors, itemized by line
	// at textFailCeiling above along with why the "zero vectors" forecast was wrong.
	//
	// Worth recording as a *measurement* rather than as a sum: the 12 were found by patching the
	// boundary and reading the board, after a hand-probe of five spellings said none existed.
	// Two claims of mine were falsified by running the check instead of reasoning about it, and
	// this is the second — the first was in #63's label readers. The board is the instrument.
	// **1992 = 1953 + 39 after the folded/sugar stratum (#64, first half)**, gross again — nothing
	// withdrawn, and this time the per-file check is the *whole* evidence rather than a footnote,
	// because a stratum that touches five files at once is exactly where a quiet withdrawal in a
	// sixth would hide. Diffed file by file against the pre-change board: five lines moved, all
	// upward, summing to 39. The itemization and the forecast's two-vector error are at
	// textFailCeiling above.
	//
	// The 39 are the folded spellings of a grammar #63 had already made correct — `blockSignature`
	// and its bindidx rejection were landed then, so the folded arms mostly needed *routing* to the
	// existing reader rather than new rules. That is why the fall is one PR rather than three, and
	// why the controls that matter here are agreement controls (TestFoldedAndFlatSignaturesAgree)
	// rather than verdict controls: the risk was a second implementation, not a wrong one.
	// **4122 = 1992 + 2130 after the bare-module admission (#69).** The largest single move
	// this floor has made, and it is *earned coverage rather than earned correctness*: not
	// one line of the reader changed: 2130 modules the parser already accepted became
	// *scored* instead of invisible. The board did not get better, it got honest.
	//
	// Decomposed, because a floor is only an assertion if it knows what it is bounding:
	// within the 68 files already on the board, pass rose 1992 → 3106 (+1114 of the 1119
	// bare modules there, the other 5 failing); the 185 newly-admitted files bring 1016 more.
	// 1114 + 1016 = 2130.
	//
	// This is the number the #64-second-half work will be measured against, and that is the
	// point of doing #69 first. Resolution is a **rejector**: it can only turn passes into
	// fails. Installing one while the accept oracle was 7 vectors would have made
	// over-rejection invisible by construction — the overfitting law (§9 G-3) at its purest,
	// since the cheap wrong check and the correct one score identically on a corpus that
	// asks nothing. Against 2130 must-succeed modules, an over-eager resolver fails loudly.
	//
	// **4147 = 4122 + 25 after typeuse resolution and functype equality (#64, second half)**,
	// gross — nothing withdrawn, checked file by file: six files moved, all upward, summing to
	// 25, and the per-file decomposition is at textFailCeiling above.
	//
	// **The floor is where #69's argument gets paid off, and it did its job.** Resolution is a
	// rejector, so the interesting column here was never the 25 — it was the 2130 bare modules
	// that had to keep passing. They did, and the falsification pass proves the check has teeth:
	// making `resolveTypeIdx` run at the typeuse instead of in `runDeferred` — the single most
	// natural way to write this wrong, and the way that reads more simply — drops this count to
	// 4145 on `imports.wast:62`'s forward reference. Under the 7-vector accept oracle that
	// preceded #69 the same defect would have been a silent green. One vector of 2130 is a thin
	// margin, and it is a real one.
	//
	// **4156 = 4147 + 9 after lane_imms (#76)**, and this floor is where that fix is *certified*
	// rather than merely observed: all nine vectors are must-succeed, so the entire finding lives
	// in this column and none of it in the fail bucket's key. The board cannot distinguish a
	// correct `lane_imms` from one that stopped reading a memory index altogether — eight of the
	// nine files write only the bare arm — which is why the arm-by-arm control in
	// `internal/text/instr_test.go` is the actual evidence and this number is the receipt that it
	// did not cost anything elsewhere.
	//
	// **4159 = 4156 + 3 after elem_list (#75)**, and all three are must-succeed modules, so — as
	// with #76 — the whole finding lives in this column. The pattern across both graves is worth
	// naming: **every defect #69's admission surfaced is an over-rejection**, which is the class a
	// reject-direction corpus is structurally blind to. Two graves, twelve vectors, and not one of
	// them would have been visible on the 7-module accept oracle that preceded it.
	//
	// **4161 = 4159 + 2 after symbolic label resolution (#80)**, and this is the *third* rejector
	// installed against this floor, so #69's argument earns its keep once more: the two vectors that
	// move are must-fail, and everything the change could break is must-succeed. It broke none.
	//
	// The accept direction is where the label work is actually at risk, and the floor is most of the
	// evidence: the reference binds a label at four sites this parser has to mirror (an anonymous one
	// at each unnamed block, one at every `func`, a *cleared* space at `enter_func`, and `catch`'s
	// target resolved in the **outer** context), and getting any of them wrong rejects legal modules
	// while still scoring 2/2 on the vectors that named the feature. Which is why the mechanism
	// controls in `internal/text/label_test.go` are scoped to those four facts and not to the two
	// vectors: the two vectors are the reward, the 2130 bare modules are the job.
	//
	// **How sharp each of the four is was measured, and the first draft of this paragraph got it
	// wrong.** It said `foldedBlock`'s push was the sharpest case because dropping it "costs nothing
	// in the fail bucket and shows up here" — an inference from the two vectors' spelling, not a
	// reading. Dropping it moves this count 4161 → 4077 and the *fail* bucket 2 → 86, all 84 landing
	// in `(module <wat body>) must read`: a rejector that over-rejects turns must-succeed modules into
	// failures, so it is loud in **both** columns, and the "invisible in the fail bucket" half was
	// simply false. The board is the instrument, again — the third time on this floor that reasoning
	// about a check was corrected by running it.
	//
	// The probe's other three arms are the finding worth keeping. `catch`-in-the-outer-context is
	// package-visible only (6 reject rows in label_test.go, nothing on the board). And **two of the
	// four are not falsifiable at all**: `funcField`'s label reset and its anonymous push can each be
	// dropped with the board at 4161/2 and `./internal/text/` green, because every push site pops
	// under `defer` and a func has no enclosing scope to leak into. Recorded here rather than only at
	// the definition site, because a floor that claims to be the evidence for four facts is
	// overclaiming when it is the evidence for one; the argument for keeping the unfalsifiable two is
	// in `funcField`'s header, where it can be read next to the lines it defends.
	//
	// **4161 → 4162 with grave #83**, and the one vector it adds is the whole `annotations.wast:1`
	// module: `scanAnnotBody` carried `token`'s bare-`(@` error arm (lexer.mll:829), which the
	// `annot` rule does not have (:850 and :855 are its only two), so `(@)` nested in a body was
	// rejected and the file's leading must-succeed module went with it. Same over-rejection shape
	// as the `foldedBlock` measurement two paragraphs up, and the same reason it is visible here:
	// one rejected legal module is one pass, and rejecting legal input is what this floor watches.
	const passFloor = 4162
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

// TestBareModuleSpansAreNonEmptyAndPlausible is #69's vacuity floor over the real corpus, and
// it is deliberately three assertions rather than one, because "the span mechanism works" can
// fail in three independent ways that a single count cannot separate.
//
// The rule being applied is *comparisons need a vacuity check*, at the scale that matters: the
// pass floor of 4122 rests on 2130 newly-scored bare modules, and a span mechanism that
// silently found **zero** of them would leave the harness classifying nothing as
// KindModuleText, every one of those commands back in `unsupported`, and this file's floors
// failing with a number that says "the reader regressed" — which would be a lie about which
// stratum broke. A global `> 0` is not enough either: `const.wast` alone holds 402, so a bug
// that lost every file but one would still be comfortably non-zero.
//
// So the three assertions are:
//
//  1. **A corpus total floor** — 2000 against 2143 measured. Bounds a wholesale loss.
//  2. **A file-count floor** — 230 against 242 files holding at least one. This is the one a
//     total cannot give: it bounds the *distribution*. Not asserted but measured — dropping
//     the 13 smallest files trips this floor at 229 while the total sits at **2130**, still
//     1.06× above (1). So (2) is not a weaker restatement of (1); there is a real regression
//     shape that only it sees, and the gap is 130 vectors wide rather than the "200 small
//     files" this comment first guessed at.
//  3. **A per-span emptiness check** — every retained span is non-empty and starts with `(`.
//     A `start == end` span is what an off-by-one in the wrong direction produces, and it
//     reaches the reader as an empty module rather than as a missing one.
//
// The 11 board files with zero bare modules are the byte-string corpus (binary*.wast,
// utf8-*.wast, custom.wast, obsolete-keywords.wast) — files whose every module is a
// `(module binary ...)` form, so zero is correct there and a floor demanding one per file
// would be wrong. That is why (2) floors the *count of files* rather than asserting a
// per-file minimum: the honest invariant is "most files have some", not "all do".
//
// Falsified three ways while writing it, each by introducing the defect it names and running
// the suite — and the third falsification corrected this comment rather than confirming it:
//
//   - `end: start` in parseNode's list arm → (3) fires, 2149 lines of it, first at
//     address.wast:3: "KindModuleText with an empty Source".
//   - `end: p.off - 1` (drop the closing paren) → TestNodeSpanIsExactSource fails on all four
//     spans and TestBareModuleSourceRoundTrips reports `unclosed list` on the re-parse. Not
//     caught here, and that is the right division of labour: this test bounds *how many*
//     spans exist, the sexpr tests bound *what they contain*.
//   - Classification loss — replacing the KindModuleText arm with KindUnsupported, which is
//     what a mis-scoped moduleFormKeyword would do — trips **boardFiles' own 240-file floor
//     first**, at 68. I had written that (1) and (2) catch this; they do, at 0 and 0, but only
//     once the outer floor is lowered out of the way. Recorded as measured rather than as
//     predicted, because "two floors catch it" and "one floor catches it and the other would
//     have" are different facts about the mechanism.
func TestBareModuleSpansAreNonEmptyAndPlausible(t *testing.T) {
	requireSuite(t)

	const (
		totalFloor = 2000 // measured 2143
		filesFloor = 230  // measured 242 of 253 board files
	)

	total, withAny := 0, 0
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		n := 0
		for _, c := range s.Commands {
			if c.Kind != KindModuleText {
				continue
			}
			n++
			// (3): the span is a real extent, not a degenerate one. Checked per command
			// rather than sampled — an empty span reaches text.ReadModule as a syntax
			// error attributed to the reader, so this is the assertion that keeps a
			// harness bug from being read as an engine bug.
			if len(c.Source) == 0 {
				t.Errorf("%s:%d: KindModuleText with an empty Source — a degenerate span",
					f, c.Line)
				continue
			}
			if c.Source[0] != '(' {
				t.Errorf("%s:%d: span starts with %q, want '(' — the extent is off its "+
					"opening paren", f, c.Line, c.Source[0])
			}
		}
		total += n
		if n > 0 {
			withAny++
		}
	}

	if total < totalFloor {
		t.Errorf("found %d bare module spans across the board, floor %d — the span "+
			"mechanism is not retaining source, so %d commands the pass floor counts on "+
			"are back in the unsupported column", total, totalFloor, totalFloor-total)
	}
	if withAny < filesFloor {
		t.Errorf("only %d files hold a bare module span, floor %d — a total floor cannot "+
			"catch this: const.wast alone holds 402, so the distribution needs its own "+
			"bound", withAny, filesFloor)
	}
}
