package spec

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/interp"
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
// scorable command".
//
// **And a fourth time, 253 → 254, when instantiation gained a trapping path (0015).**
// `data1.wast` was the one excluded file whose exclusion this list called "the
// interpreter's", and the label was a prediction that came due: every form in it is
// `assert_trap` wrapping a bare module, nothing else, so the file held no scorable command
// until that shape became a Kind. The list below is now three, and the shrink is the reason
// this paragraph exists — *an itemized exclusion outlives the exclusion unless someone
// re-prints it*, and this one had a file in it that the same PR admitted.
//
// 254 of 257 vendored files hold a scorable command, and the three that do not were
// *printed*, not predicted — two are the validator's and one is a real gap:
//
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
	// is decoration. 240 of 254, the 14-file margin being upstream churn room; the three
	// legitimately-excluded files are itemized at the top of this function, so a fourth
	// appearing is a fact somebody has to write down.
	//
	// Deliberately **not** raised to 241 by 0015's one extra file. The margin is churn room
	// measured against upstream, not slack to be reclaimed every time the number moves by
	// one, and re-pinning a floor to each new actual makes it a restatement of the actual
	// rather than a bound. It moves when the *distance* stops being meaningful — which is
	// the 60-against-253 case above — not when the actual moves inside it.
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

// instantiate is the interpreter's module entry point: it turns a scored module command into
// something invoke can be called on.
//
// **It decodes a second time, and that is not waste.** The module arm has already asked
// `decode` whether the image is well-formed — that is the *verdict*, and its answer is a
// board number. This call wants the decoded module itself, which DecodeFunc deliberately
// does not return: `func([]byte) error` is the shape it is because a harness holding a
// `*binary.Module` would be a harness that imports the engine's representation. So the
// duplication buys the neutrality, and it buys it at a cost the board can afford (one extra
// decode per module command, ~2100 of them).
//
// The text path re-encodes rather than re-reading: `text.ReadModule` is error-only by design
// (0011), so EncodeModule is the only path from wat source to an image. That is the same
// second call the board's readText already made, for the same reason.
func instantiate(c Command) (Instance, Stratum, error) {
	return instantiateWith(binary.Features{}, c)
}

// instantiateWith is instantiate under a stated gate set.
//
// **The gate set has to reach this path, and the all-gates-on lane is what proved it.** The
// lane replaces Engine.Decode and nothing else, so while this function called
// `binary.DecodeModule` — the default-features helper — a module reaching the interpreter
// through instantiation was decoded with every gate *off* no matter what the lane asked for.
// 17 memory64 vectors were declined in the lane whose defining property is that nothing is
// declined: `Gated must be 0` failed, naming the two files, which is the structural bound
// working exactly as decision 0010's ruling says it must. A per-vector allowlist would have
// absorbed them silently.
//
// Note which instrument found it. TestGatedVectors keys on `decode(c.Module)` and cannot see
// this path at all — a text module has no `c.Module` — so the per-vector control was blind by
// construction and the *structural* one was not. That is the argument for the all-on lane
// restated by measurement rather than by claim.
func instantiateWith(f binary.Features, c Command) (Instance, Stratum, error) {
	image := c.Module
	stratum := StratumBinary
	if c.Kind != KindModuleBinary {
		// **StratumEncode, not StratumText.** The module arm already asked ReadModule and
		// scored its answer; this is EncodeModule, a different entry point with a different
		// frontier, and charging its 13775 unemitted instruction bodies to the reader's
		// column would raise a ceiling that is 0 and destroy the only instrument watching
		// the reader for regressions.
		stratum = StratumEncode
		img, err := text.EncodeModule(c.Source)
		if err != nil {
			return nil, stratum, err
		}
		image = img
		// Past the encoder, a failure is the decoder's — reading its own output.
		stratum = StratumBinary
	}
	m, err := (&binary.Decoder{Features: f}).DecodeModule(image)
	if err != nil {
		return nil, stratum, err
	}
	// **A trap here is a verdict, not a failure to instantiate** (0015). Instantiation is
	// execution at time zero, so an active data segment copied out of bounds makes this a
	// module that came to life and died doing it — which is exactly what `data1.wast`'s 14
	// `assert_trap`-wrapping-a-module vectors assert. Charged to StratumExec because the
	// interpreter is the component that produced it.
	in, trap := interp.Instantiate(m)
	if trap != nil {
		return nil, StratumExec, trap
	}
	// **A nil trap is not "instantiation completed".** Instantiation can fall short without
	// trapping — an active data segment whose target memory is imported cannot be copied, and
	// "there is no linker" is neither a runtime event nor a verdict (0015), so it comes back on
	// this channel. Quoting it here is what makes `data1.wast`'s :80/:117/:136 read as *linking
	// is missing* instead of as "the module instantiated without trapping", which was true and
	// named no component. Charged to StratumExec, the layer that reported it.
	//
	// The instance is discarded rather than returned alongside the error, because a partially
	// initialized memory is exactly the thing a following assert_return must not read: it would
	// compute a confident answer over bytes that were never copied.
	if err := in.Deferred(); err != nil {
		return nil, StratumExec, err
	}
	return in, StratumUnset, nil
}

// invoke is the interpreter's call entry point, and it is where the two value models meet.
//
// This function is the *one* legitimate place that knows both `spec.Val` and
// `interp.Value` — the glue the ValKind doc comment names. The conversion is a bit-pattern
// copy plus a type-tag map in both directions and nothing else: no float arithmetic, no
// re-parsing, no NaN normalization. Anything cleverer here would be the harness recomputing
// a value it was handed, which is how a comparator starts agreeing with itself.
func invoke(in Instance, name string, args []Val) ([]Val, error) {
	inst, ok := in.(*interp.Instance)
	if !ok {
		return nil, fmt.Errorf("instance is %T, not *interp.Instance", in)
	}
	vs := make([]interp.Value, len(args))
	for i, a := range args {
		t, ok := valType(a.Kind)
		if !ok {
			return nil, fmt.Errorf("argument %d has kind %v, which has no binary.ValType", i, a.Kind)
		}
		vs[i] = interp.Value{Type: t, Bits: a.Bits}
	}
	out, err := inst.Invoke(name, vs...)
	if err != nil {
		return nil, err
	}
	res := make([]Val, len(out))
	for i, o := range out {
		k, ok := valKind(o.Type)
		if !ok {
			// A result type the harness cannot name. Reported rather than mapped to a
			// default, because a silent coercion here would make every v128 result
			// compare as an i32 and the mismatch bucket would name the wrong defect.
			return nil, fmt.Errorf("result %d has type %v, which the harness cannot represent", i, o.Type)
		}
		res[i] = Val{Kind: k, Bits: o.Bits}
	}
	return res, nil
}

// valType and valKind are the two directions of the value-type map, written as explicit
// switches over both enums rather than as an arithmetic offset.
//
// A `binary.ValType(0x7f - k)` trick would work today and silently break the day either enum
// gains a member — the enumerated-literal defect in its most tempting form, since the two
// orderings genuinely do correspond right now. Both functions report `ok` rather than
// defaulting, so an unmappable type is a named failure at the boundary.
func valType(k ValKind) (binary.ValType, bool) {
	switch k {
	case KindI32:
		return binary.I32, true
	case KindI64:
		return binary.I64, true
	case KindF32:
		return binary.F32, true
	case KindF64:
		return binary.F64, true
	}
	return binary.NoValType, false
}

func valKind(t binary.ValType) (ValKind, bool) {
	switch t {
	case binary.I32:
		return KindI32, true
	case binary.I64:
		return KindI64, true
	case binary.F32:
		return KindF32, true
	case binary.F64:
		return KindF64, true
	default:
		// V128, FuncRef, ExternRef, NoValType: types the *harness* cannot name (see
		// ValKind's four members). Reported as `false` so the caller says so rather than
		// coercing — a silent map to KindI32 would make every v128 result compare as an
		// i32 and bucket the wrong defect.
		return 0, false
	}
}

// engine is the board's engine description, in one place so that every board test scores
// against the same set of components. A test that wants a narrower engine builds its own
// Engine literal, which is visible at the call site rather than hidden in a positional
// argument.
func engine() Engine {
	return Engine{
		Decode: decode, ReadText: readText, IsGated: isGated,
		Instantiate: instantiate, Invoke: invoke,
	}
}

// run scores a script with gate declines separated from verdicts.
func run(s *Script) *Result { return s.RunGated(engine()) }

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

// suitePin reports the suite revision the board's counts were measured against.
//
// Read from the *fetch script's pin* rather than from `git -C testdata/spec rev-parse`, and
// the difference is the point: the script's `rev` is what the corpus is *supposed* to be, and
// the script asserts on every path that the checkout matches it (#42). So quoting the pin
// quotes an already-verified fact, where shelling out to git would make the board's provenance
// depend on a second, unasserted reading — and would make the test invoke git at all, which a
// board test has no business doing.
//
// Failure is fatal rather than a blank: *a provenance header that says nothing is worse than
// none, because it looks stamped* (gen.PinnedRev's own reason).
func suitePin(t *testing.T) string {
	t.Helper()
	rev, err := gen.PinnedSuiteRev()
	if err != nil {
		t.Fatalf("reading the suite pin: %v\n\tThe board cannot name the corpus it measured, "+
			"which makes every count below unattributable.", err)
	}
	// And the pin is checked against the checkout it claims to describe, because the two
	// can disagree: the script asserts them equal *when it runs*, and nothing stops a later
	// `git -C testdata/spec checkout` from moving the tree underneath it. Printing the pin
	// without this check would quote what the corpus is supposed to be while the counts came
	// from whatever is actually there — a stamped provenance that is wrong exactly when it
	// matters, which is the hearsay #42 is about.
	//
	// **Resolved with git rather than by reading .git/HEAD**, and the first draft did the
	// latter: on a branch checkout HEAD holds a *symbolic ref* (`ref: refs/heads/utf8-names`,
	// which is what the vendored corpus actually has), so the comparison put a ref name
	// against a SHA and failed for every developer whose corpus was not detached. A false
	// alarm on the board's own provenance line — and the fix is not more parsing, it is
	// *measure with the instrument*: `rev-parse` is the thing that knows how to resolve a
	// ref, and reimplementing it here would be a second opinion about git's storage format.
	//
	// The earlier version of this comment argued a board test "has no business" invoking
	// git. That was wrong, and it is quoted rather than deleted: verifying what a working
	// tree is at *is* a git question, and answering it with a file read was the reasoning
	// that produced the false alarm.
	//
	// A tree that is not a git checkout at all (a tarball) is unverifiable rather than
	// violated, so the board says which of the two it is instead of implying either.
	out, err := exec.Command("git", "-C", suiteDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Logf("note: cannot resolve %s to a revision (%v), so the pin %s is unverified "+
			"against the tree", suiteDir, err, rev)
		return rev
	}
	if got := strings.TrimSpace(string(out)); got != rev {
		t.Errorf("suite pin is %s but %s is checked out at %s.\n\tThe board would name a "+
			"corpus it did not measure. Run `make spec-tests` to return to the pin, or bump "+
			"the pin deliberately.", rev, suiteDir, got)
	}
	return rev
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

		// # The interpreter's arrival opened a second decline path, and these were its 17
		//
		// Every entry is an `assert_return` whose module carries an **i64 index type** —
		// `(memory i64 0)` at memory_grow64.wast:1, :36 and :48, `(table $t64 i64 0 externref)`
		// at table_grow64.wast:2 — which is memory64's defining feature. With the gate off the
		// decoder must reject the module, so the vector's question is never asked and `gated`
		// is the only honest verdict for it.
		//
		// **Two of those three line numbers were wrong until this edit**, and the correction is a
		// finding rather than a typo fix: the comment said ":1 and :34", which named one module
		// this file has and one it does not — line 34 is blank, the second `(memory i64 0)` is at
		// :36, and a *third* module at :48 was unmentioned because the memarg emitter had not yet
		// made its vectors reachable. A citation nobody re-resolves is a claim, and this one had
		// been read past in every review since #124.
		//
		// **They are here because the trigger could reach them, and it could not before.**
		// These declines happen at *instantiation*, on a wat module with no `c.Module` for the
		// old re-derived trigger to decode — so they were invisible to this allowlist while
		// being scored as **fails** one command downstream, in the interpreter's column, for a
		// feature decision that is not the interpreter's. Two defects with one cause; see the
		// GatedAt doc comment.
		//
		// Verified by reading the two module headers rather than by trusting that 17 declines
		// in two files share one cause. Both are passed in the all-gates-on lane, where the
		// memory64 gate is on and the vectors answer on the merits — so the parked verdict is
		// earned there rather than deferred everywhere.
		// # The memarg emitter's 114, which is the same mechanism widened by one instruction shape
		//
		// #8's memarg emitter taught the encoder to write load and store immediates, so 199 modules that used to
		// stop at `cannot yet encode` now reach the decoder — and 114 of their vectors reach a gate
		// there. **They are all `gated` for one of two features and neither is the encoder's**: a
		// module declaring `(memory i64 …)` is memory64, and a module with two or more memories
		// makes some memarg carry flags bit 6, which is multi-memory. The emitter is right in both
		// cases; the decoder is configured not to read what it correctly wrote.
		//
		// **The count is exactly the fail column's drop** — fail 14330 → 14216, gated 33 → 147 —
		// which is what makes this a verdict moving to its honest column rather than a board being
		// emptied. Every one is passed in the all-gates-on lane, where both gates are on and the
		// vectors answer on the merits.
		//
		// Verified by reading each module head rather than by trusting that 114 declines in eleven
		// files share two causes: the `module@` line is quoted per entry, and the two reasons were
		// separated by the error string the engine actually produced (`memory64: feature gate
		// disabled` versus `memarg flags bit 6 … decodeMemop`), not by the file's name — five of
		// these files are named `*64` and two of those carry *no* i64 memory.
		// # The data section's 810, which is the same mechanism widened by a whole module field
		//
		// Section 11 (#8) taught the emitter to write data segments and the data count section, so
		// **9082 modules that used to stop at `cannot yet encode` now reach the decoder** — and 810
		// of their vectors reach a gate there. As with the memarg emitter's 114 above, the emitter is
		// right and the decoder is configured not to read what it correctly wrote; three features,
		// none of them the encoder's, and each named from **the error string the engine produced**
		// rather than from the file's name.
		//
		// The column arithmetic is the honest part and it is not flattering: encode fail 13775 → 4693
		// is −9082, of which **8272 became exec fails and 810 became these declines. Zero became
		// passes.** That is not a drained board, it is a queue moving one layer down — the 8272 are
		// `interp: no arm for opcode 2d` and its 60 siblings, because a `(data …)` module that now
		// encodes still needs a load arm to answer an `assert_return`. Said plainly rather than
		// buried, because a section landing that produces no new pass is exactly the shape a Board
		// line is supposed to make visible.
		//
		// Verified by re-deriving each decline's cause from the run loop's own `GatedAt` — a
		// throwaway probe that walked the same commands and asserted **symmetric-difference zero**
		// against it per file, since a reason list built by a second traversal describes that
		// traversal until the two sets are proven identical.
		//
		// **Two of the counts written above were wrong, and that is a grave rather than a typo.**
		// `memory_trap0.wast:1` was documented as "five memories" and has three; `memory_fill0.wast:2`
		// as "four" and has three. Both numbers came from counting `(memory` in the source, where
		// `(memory.size $m)`, `(memory.grow …)` and `(data (memory 2) …)` spell those characters
		// without defining anything — so the reason stated a false fact about the module while
		// naming the right feature, and no reader re-derived it. The counts here are the *decoder's*
		// (`len(Memories)` plus the memory imports, all gates on), which is the index space the
		// flags bit is actually about. Measure with the instrument, not a regex (grave #129).
		// # The bare-invoke Kind's 74, which add no module and no feature
		//
		// #7's memory work taught the harness to run a top-level `(invoke …)` as a command
		// rather than to walk past it, so 74 commands that were never scored now reach the run
		// loop — and every one of them stands after a module the gates had **already** declined.
		// They add no file, no module and no feature: each of the 74 is spliced into a block
		// that existed, carrying the *same reason string as its own module's other vectors*,
		// which is what "no new feature" means concretely rather than as a claim.
		//
		// Four modules are the exception and they are the only entries here read from source
		// rather than inherited: `memory_copy64.wast:4863`, `:4875`, `:4887` and
		// `memory_init64.wast:203` are followed by an `(invoke "test")` and by nothing else, so
		// no sibling vector had ever put them in this list. All four declare `(memory i64 …)`.
		//
		// **The reason strings were inherited by module line, not retyped**, because the
		// alternative is 74 hand-copied claims about features and grave #129 is what retyping a
		// module fact costs. Verified in the direction that can fail silently: the pre-existing
		// 963 entries were extracted before and after and compared as sets — identical, +74 —
		// so this edit cannot have quietly changed a reason while adding lines. And the reverse
		// check below stayed silent throughout, which is the load-bearing negative: it says the
		// vectors these join are still declined, so the 74 are additions to a live list and not
		// a list going stale in place.
		"address0.wast": {
			105: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			106: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			107: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			108: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			109: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			111: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			112: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			113: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			114: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			115: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			117: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			118: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			119: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			120: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			121: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			123: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			124: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			125: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			126: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			127: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			129: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			130: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			131: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			132: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			133: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			135: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			136: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			137: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			138: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			139: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			141: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			142: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			143: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			144: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			145: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			147: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			148: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			149: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			150: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			151: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			153: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			154: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			155: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			156: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			157: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			159: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			160: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			161: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			162: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			163: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			165: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			166: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			167: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			168: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			169: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			171: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			172: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			173: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			174: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			175: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			177: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			178: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			179: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			180: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			181: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			183: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			184: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			185: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			186: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			187: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			189: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			190: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			191: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			192: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
		},
		"address1.wast": {
			146: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			147: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			148: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			149: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			150: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			152: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			153: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			154: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			155: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			156: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			158: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			159: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			160: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			161: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			162: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			164: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			165: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			166: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			167: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			168: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			170: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			171: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			172: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			173: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			174: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			176: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			177: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			178: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			179: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			180: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			182: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			183: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			184: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			185: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			186: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			188: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			189: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			190: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			191: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			192: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			194: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			195: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			196: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			197: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			198: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			200: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			201: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			202: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			203: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			204: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			206: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			207: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			208: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			209: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			210: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			212: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			213: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			214: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			215: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			216: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			218: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			219: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			220: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			221: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			222: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			224: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			225: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			226: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			227: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			228: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			230: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			231: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			232: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			233: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			234: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			236: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			237: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			238: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			239: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			240: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			242: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			243: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			244: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			245: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			246: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			248: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			249: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			250: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			251: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			252: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			254: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			255: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			256: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			257: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			258: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			260: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			261: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			262: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			263: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			264: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			266: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			267: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			268: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
			269: "multi-memory: 5 memories at :3, so a memarg carries flags bit 6",
		},
		"address64.wast": {
			104: "memory64: (memory i64 1) at :3 — an i64 index type",
			105: "memory64: (memory i64 1) at :3 — an i64 index type",
			106: "memory64: (memory i64 1) at :3 — an i64 index type",
			107: "memory64: (memory i64 1) at :3 — an i64 index type",
			108: "memory64: (memory i64 1) at :3 — an i64 index type",
			110: "memory64: (memory i64 1) at :3 — an i64 index type",
			111: "memory64: (memory i64 1) at :3 — an i64 index type",
			112: "memory64: (memory i64 1) at :3 — an i64 index type",
			113: "memory64: (memory i64 1) at :3 — an i64 index type",
			114: "memory64: (memory i64 1) at :3 — an i64 index type",
			116: "memory64: (memory i64 1) at :3 — an i64 index type",
			117: "memory64: (memory i64 1) at :3 — an i64 index type",
			118: "memory64: (memory i64 1) at :3 — an i64 index type",
			119: "memory64: (memory i64 1) at :3 — an i64 index type",
			120: "memory64: (memory i64 1) at :3 — an i64 index type",
			122: "memory64: (memory i64 1) at :3 — an i64 index type",
			123: "memory64: (memory i64 1) at :3 — an i64 index type",
			124: "memory64: (memory i64 1) at :3 — an i64 index type",
			125: "memory64: (memory i64 1) at :3 — an i64 index type",
			126: "memory64: (memory i64 1) at :3 — an i64 index type",
			128: "memory64: (memory i64 1) at :3 — an i64 index type",
			129: "memory64: (memory i64 1) at :3 — an i64 index type",
			130: "memory64: (memory i64 1) at :3 — an i64 index type",
			131: "memory64: (memory i64 1) at :3 — an i64 index type",
			132: "memory64: (memory i64 1) at :3 — an i64 index type",
			134: "memory64: (memory i64 1) at :3 — an i64 index type",
			135: "memory64: (memory i64 1) at :3 — an i64 index type",
			136: "memory64: (memory i64 1) at :3 — an i64 index type",
			137: "memory64: (memory i64 1) at :3 — an i64 index type",
			138: "memory64: (memory i64 1) at :3 — an i64 index type",
			140: "memory64: (memory i64 1) at :3 — an i64 index type",
			141: "memory64: (memory i64 1) at :3 — an i64 index type",
			142: "memory64: (memory i64 1) at :3 — an i64 index type",
			143: "memory64: (memory i64 1) at :3 — an i64 index type",
			144: "memory64: (memory i64 1) at :3 — an i64 index type",
			146: "memory64: (memory i64 1) at :3 — an i64 index type",
			147: "memory64: (memory i64 1) at :3 — an i64 index type",
			148: "memory64: (memory i64 1) at :3 — an i64 index type",
			149: "memory64: (memory i64 1) at :3 — an i64 index type",
			150: "memory64: (memory i64 1) at :3 — an i64 index type",
			152: "memory64: (memory i64 1) at :3 — an i64 index type",
			153: "memory64: (memory i64 1) at :3 — an i64 index type",
			154: "memory64: (memory i64 1) at :3 — an i64 index type",
			155: "memory64: (memory i64 1) at :3 — an i64 index type",
			156: "memory64: (memory i64 1) at :3 — an i64 index type",
			158: "memory64: (memory i64 1) at :3 — an i64 index type",
			159: "memory64: (memory i64 1) at :3 — an i64 index type",
			160: "memory64: (memory i64 1) at :3 — an i64 index type",
			161: "memory64: (memory i64 1) at :3 — an i64 index type",
			162: "memory64: (memory i64 1) at :3 — an i64 index type",
			164: "memory64: (memory i64 1) at :3 — an i64 index type",
			165: "memory64: (memory i64 1) at :3 — an i64 index type",
			166: "memory64: (memory i64 1) at :3 — an i64 index type",
			167: "memory64: (memory i64 1) at :3 — an i64 index type",
			168: "memory64: (memory i64 1) at :3 — an i64 index type",
			170: "memory64: (memory i64 1) at :3 — an i64 index type",
			171: "memory64: (memory i64 1) at :3 — an i64 index type",
			172: "memory64: (memory i64 1) at :3 — an i64 index type",
			173: "memory64: (memory i64 1) at :3 — an i64 index type",
			174: "memory64: (memory i64 1) at :3 — an i64 index type",
			176: "memory64: (memory i64 1) at :3 — an i64 index type",
			177: "memory64: (memory i64 1) at :3 — an i64 index type",
			178: "memory64: (memory i64 1) at :3 — an i64 index type",
			179: "memory64: (memory i64 1) at :3 — an i64 index type",
			180: "memory64: (memory i64 1) at :3 — an i64 index type",
			182: "memory64: (memory i64 1) at :3 — an i64 index type",
			183: "memory64: (memory i64 1) at :3 — an i64 index type",
			184: "memory64: (memory i64 1) at :3 — an i64 index type",
			185: "memory64: (memory i64 1) at :3 — an i64 index type",
			186: "memory64: (memory i64 1) at :3 — an i64 index type",
			188: "memory64: (memory i64 1) at :3 — an i64 index type",
			189: "memory64: (memory i64 1) at :3 — an i64 index type",
			190: "memory64: (memory i64 1) at :3 — an i64 index type",
			191: "memory64: (memory i64 1) at :3 — an i64 index type",
			348: "memory64: (memory i64 1) at :209 — an i64 index type",
			349: "memory64: (memory i64 1) at :209 — an i64 index type",
			350: "memory64: (memory i64 1) at :209 — an i64 index type",
			351: "memory64: (memory i64 1) at :209 — an i64 index type",
			352: "memory64: (memory i64 1) at :209 — an i64 index type",
			354: "memory64: (memory i64 1) at :209 — an i64 index type",
			355: "memory64: (memory i64 1) at :209 — an i64 index type",
			356: "memory64: (memory i64 1) at :209 — an i64 index type",
			357: "memory64: (memory i64 1) at :209 — an i64 index type",
			358: "memory64: (memory i64 1) at :209 — an i64 index type",
			360: "memory64: (memory i64 1) at :209 — an i64 index type",
			361: "memory64: (memory i64 1) at :209 — an i64 index type",
			362: "memory64: (memory i64 1) at :209 — an i64 index type",
			363: "memory64: (memory i64 1) at :209 — an i64 index type",
			364: "memory64: (memory i64 1) at :209 — an i64 index type",
			366: "memory64: (memory i64 1) at :209 — an i64 index type",
			367: "memory64: (memory i64 1) at :209 — an i64 index type",
			368: "memory64: (memory i64 1) at :209 — an i64 index type",
			369: "memory64: (memory i64 1) at :209 — an i64 index type",
			370: "memory64: (memory i64 1) at :209 — an i64 index type",
			372: "memory64: (memory i64 1) at :209 — an i64 index type",
			373: "memory64: (memory i64 1) at :209 — an i64 index type",
			374: "memory64: (memory i64 1) at :209 — an i64 index type",
			375: "memory64: (memory i64 1) at :209 — an i64 index type",
			376: "memory64: (memory i64 1) at :209 — an i64 index type",
			378: "memory64: (memory i64 1) at :209 — an i64 index type",
			379: "memory64: (memory i64 1) at :209 — an i64 index type",
			380: "memory64: (memory i64 1) at :209 — an i64 index type",
			381: "memory64: (memory i64 1) at :209 — an i64 index type",
			382: "memory64: (memory i64 1) at :209 — an i64 index type",
			384: "memory64: (memory i64 1) at :209 — an i64 index type",
			385: "memory64: (memory i64 1) at :209 — an i64 index type",
			386: "memory64: (memory i64 1) at :209 — an i64 index type",
			387: "memory64: (memory i64 1) at :209 — an i64 index type",
			388: "memory64: (memory i64 1) at :209 — an i64 index type",
			390: "memory64: (memory i64 1) at :209 — an i64 index type",
			391: "memory64: (memory i64 1) at :209 — an i64 index type",
			392: "memory64: (memory i64 1) at :209 — an i64 index type",
			393: "memory64: (memory i64 1) at :209 — an i64 index type",
			394: "memory64: (memory i64 1) at :209 — an i64 index type",
			396: "memory64: (memory i64 1) at :209 — an i64 index type",
			397: "memory64: (memory i64 1) at :209 — an i64 index type",
			398: "memory64: (memory i64 1) at :209 — an i64 index type",
			399: "memory64: (memory i64 1) at :209 — an i64 index type",
			400: "memory64: (memory i64 1) at :209 — an i64 index type",
			402: "memory64: (memory i64 1) at :209 — an i64 index type",
			403: "memory64: (memory i64 1) at :209 — an i64 index type",
			404: "memory64: (memory i64 1) at :209 — an i64 index type",
			405: "memory64: (memory i64 1) at :209 — an i64 index type",
			406: "memory64: (memory i64 1) at :209 — an i64 index type",
			408: "memory64: (memory i64 1) at :209 — an i64 index type",
			409: "memory64: (memory i64 1) at :209 — an i64 index type",
			410: "memory64: (memory i64 1) at :209 — an i64 index type",
			411: "memory64: (memory i64 1) at :209 — an i64 index type",
			412: "memory64: (memory i64 1) at :209 — an i64 index type",
			414: "memory64: (memory i64 1) at :209 — an i64 index type",
			415: "memory64: (memory i64 1) at :209 — an i64 index type",
			416: "memory64: (memory i64 1) at :209 — an i64 index type",
			417: "memory64: (memory i64 1) at :209 — an i64 index type",
			418: "memory64: (memory i64 1) at :209 — an i64 index type",
			420: "memory64: (memory i64 1) at :209 — an i64 index type",
			421: "memory64: (memory i64 1) at :209 — an i64 index type",
			422: "memory64: (memory i64 1) at :209 — an i64 index type",
			423: "memory64: (memory i64 1) at :209 — an i64 index type",
			424: "memory64: (memory i64 1) at :209 — an i64 index type",
			426: "memory64: (memory i64 1) at :209 — an i64 index type",
			427: "memory64: (memory i64 1) at :209 — an i64 index type",
			428: "memory64: (memory i64 1) at :209 — an i64 index type",
			429: "memory64: (memory i64 1) at :209 — an i64 index type",
			430: "memory64: (memory i64 1) at :209 — an i64 index type",
			432: "memory64: (memory i64 1) at :209 — an i64 index type",
			433: "memory64: (memory i64 1) at :209 — an i64 index type",
			434: "memory64: (memory i64 1) at :209 — an i64 index type",
			435: "memory64: (memory i64 1) at :209 — an i64 index type",
			436: "memory64: (memory i64 1) at :209 — an i64 index type",
			438: "memory64: (memory i64 1) at :209 — an i64 index type",
			439: "memory64: (memory i64 1) at :209 — an i64 index type",
			440: "memory64: (memory i64 1) at :209 — an i64 index type",
			441: "memory64: (memory i64 1) at :209 — an i64 index type",
			442: "memory64: (memory i64 1) at :209 — an i64 index type",
			444: "memory64: (memory i64 1) at :209 — an i64 index type",
			445: "memory64: (memory i64 1) at :209 — an i64 index type",
			446: "memory64: (memory i64 1) at :209 — an i64 index type",
			447: "memory64: (memory i64 1) at :209 — an i64 index type",
			448: "memory64: (memory i64 1) at :209 — an i64 index type",
			450: "memory64: (memory i64 1) at :209 — an i64 index type",
			451: "memory64: (memory i64 1) at :209 — an i64 index type",
			452: "memory64: (memory i64 1) at :209 — an i64 index type",
			453: "memory64: (memory i64 1) at :209 — an i64 index type",
			454: "memory64: (memory i64 1) at :209 — an i64 index type",
			456: "memory64: (memory i64 1) at :209 — an i64 index type",
			457: "memory64: (memory i64 1) at :209 — an i64 index type",
			458: "memory64: (memory i64 1) at :209 — an i64 index type",
			459: "memory64: (memory i64 1) at :209 — an i64 index type",
			460: "memory64: (memory i64 1) at :209 — an i64 index type",
			462: "memory64: (memory i64 1) at :209 — an i64 index type",
			463: "memory64: (memory i64 1) at :209 — an i64 index type",
			464: "memory64: (memory i64 1) at :209 — an i64 index type",
			465: "memory64: (memory i64 1) at :209 — an i64 index type",
			466: "memory64: (memory i64 1) at :209 — an i64 index type",
			468: "memory64: (memory i64 1) at :209 — an i64 index type",
			469: "memory64: (memory i64 1) at :209 — an i64 index type",
			470: "memory64: (memory i64 1) at :209 — an i64 index type",
			471: "memory64: (memory i64 1) at :209 — an i64 index type",
			516: "memory64: (memory i64 1) at :492 — an i64 index type",
			517: "memory64: (memory i64 1) at :492 — an i64 index type",
			518: "memory64: (memory i64 1) at :492 — an i64 index type",
			519: "memory64: (memory i64 1) at :492 — an i64 index type",
			520: "memory64: (memory i64 1) at :492 — an i64 index type",
			522: "memory64: (memory i64 1) at :492 — an i64 index type",
			523: "memory64: (memory i64 1) at :492 — an i64 index type",
			524: "memory64: (memory i64 1) at :492 — an i64 index type",
			525: "memory64: (memory i64 1) at :492 — an i64 index type",
			526: "memory64: (memory i64 1) at :492 — an i64 index type",
			528: "memory64: (memory i64 1) at :492 — an i64 index type",
			529: "memory64: (memory i64 1) at :492 — an i64 index type",
			530: "memory64: (memory i64 1) at :492 — an i64 index type",
			531: "memory64: (memory i64 1) at :492 — an i64 index type",
			563: "memory64: (memory i64 1) at :539 — an i64 index type",
			564: "memory64: (memory i64 1) at :539 — an i64 index type",
			565: "memory64: (memory i64 1) at :539 — an i64 index type",
			566: "memory64: (memory i64 1) at :539 — an i64 index type",
			567: "memory64: (memory i64 1) at :539 — an i64 index type",
			569: "memory64: (memory i64 1) at :539 — an i64 index type",
			570: "memory64: (memory i64 1) at :539 — an i64 index type",
			571: "memory64: (memory i64 1) at :539 — an i64 index type",
			572: "memory64: (memory i64 1) at :539 — an i64 index type",
			573: "memory64: (memory i64 1) at :539 — an i64 index type",
			575: "memory64: (memory i64 1) at :539 — an i64 index type",
			576: "memory64: (memory i64 1) at :539 — an i64 index type",
			577: "memory64: (memory i64 1) at :539 — an i64 index type",
			578: "memory64: (memory i64 1) at :539 — an i64 index type",
		},
		"align64.wast": {
			866: "memory64: (memory i64 1) at :854 — an i64 index type",
		},
		"bulk64.wast": {
			21:  "memory64: (memory i64 1) at :7 — an i64 index type",
			22:  "memory64: (memory i64 1) at :7 — an i64 index type",
			23:  "memory64: (memory i64 1) at :7 — an i64 index type",
			24:  "memory64: (memory i64 1) at :7 — an i64 index type",
			25:  "memory64: (memory i64 1) at :7 — an i64 index type",
			26:  "memory64: (memory i64 1) at :7 — an i64 index type",
			29:  "memory64: (memory i64 1) at :7 — an i64 index type",
			30:  "memory64: (memory i64 1) at :7 — an i64 index type",
			31:  "memory64: (memory i64 1) at :7 — an i64 index type",
			34:  "memory64: (memory i64 1) at :7 — an i64 index type",
			37:  "memory64: (memory i64 1) at :7 — an i64 index type",
			60:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			62:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			63:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			64:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			65:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			66:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			67:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			70:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			71:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			72:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			73:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			74:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			75:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			76:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			79:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			80:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			81:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			82:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			83:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			84:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			85:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			86:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			92:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			93:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			94:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			95:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			96:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			97:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			98:  "memory64: (memory i64 1 1) at :45 — an i64 index type",
			101: "memory64: (memory i64 1 1) at :45 — an i64 index type",
			102: "memory64: (memory i64 1 1) at :45 — an i64 index type",
			105: "memory64: (memory i64 1 1) at :45 — an i64 index type",
			106: "memory64: (memory i64 1 1) at :45 — an i64 index type",
		},
		"endianness64.wast": {
			133: "memory64: (memory i64 1) at :1 — an i64 index type",
			134: "memory64: (memory i64 1) at :1 — an i64 index type",
			135: "memory64: (memory i64 1) at :1 — an i64 index type",
			136: "memory64: (memory i64 1) at :1 — an i64 index type",
			138: "memory64: (memory i64 1) at :1 — an i64 index type",
			139: "memory64: (memory i64 1) at :1 — an i64 index type",
			140: "memory64: (memory i64 1) at :1 — an i64 index type",
			141: "memory64: (memory i64 1) at :1 — an i64 index type",
			143: "memory64: (memory i64 1) at :1 — an i64 index type",
			144: "memory64: (memory i64 1) at :1 — an i64 index type",
			145: "memory64: (memory i64 1) at :1 — an i64 index type",
			146: "memory64: (memory i64 1) at :1 — an i64 index type",
			148: "memory64: (memory i64 1) at :1 — an i64 index type",
			149: "memory64: (memory i64 1) at :1 — an i64 index type",
			150: "memory64: (memory i64 1) at :1 — an i64 index type",
			151: "memory64: (memory i64 1) at :1 — an i64 index type",
			153: "memory64: (memory i64 1) at :1 — an i64 index type",
			154: "memory64: (memory i64 1) at :1 — an i64 index type",
			155: "memory64: (memory i64 1) at :1 — an i64 index type",
			156: "memory64: (memory i64 1) at :1 — an i64 index type",
			158: "memory64: (memory i64 1) at :1 — an i64 index type",
			159: "memory64: (memory i64 1) at :1 — an i64 index type",
			160: "memory64: (memory i64 1) at :1 — an i64 index type",
			161: "memory64: (memory i64 1) at :1 — an i64 index type",
			163: "memory64: (memory i64 1) at :1 — an i64 index type",
			164: "memory64: (memory i64 1) at :1 — an i64 index type",
			165: "memory64: (memory i64 1) at :1 — an i64 index type",
			166: "memory64: (memory i64 1) at :1 — an i64 index type",
			168: "memory64: (memory i64 1) at :1 — an i64 index type",
			169: "memory64: (memory i64 1) at :1 — an i64 index type",
			170: "memory64: (memory i64 1) at :1 — an i64 index type",
			171: "memory64: (memory i64 1) at :1 — an i64 index type",
			173: "memory64: (memory i64 1) at :1 — an i64 index type",
			174: "memory64: (memory i64 1) at :1 — an i64 index type",
			175: "memory64: (memory i64 1) at :1 — an i64 index type",
			176: "memory64: (memory i64 1) at :1 — an i64 index type",
			178: "memory64: (memory i64 1) at :1 — an i64 index type",
			179: "memory64: (memory i64 1) at :1 — an i64 index type",
			180: "memory64: (memory i64 1) at :1 — an i64 index type",
			181: "memory64: (memory i64 1) at :1 — an i64 index type",
			184: "memory64: (memory i64 1) at :1 — an i64 index type",
			185: "memory64: (memory i64 1) at :1 — an i64 index type",
			186: "memory64: (memory i64 1) at :1 — an i64 index type",
			187: "memory64: (memory i64 1) at :1 — an i64 index type",
			189: "memory64: (memory i64 1) at :1 — an i64 index type",
			190: "memory64: (memory i64 1) at :1 — an i64 index type",
			191: "memory64: (memory i64 1) at :1 — an i64 index type",
			192: "memory64: (memory i64 1) at :1 — an i64 index type",
			194: "memory64: (memory i64 1) at :1 — an i64 index type",
			195: "memory64: (memory i64 1) at :1 — an i64 index type",
			196: "memory64: (memory i64 1) at :1 — an i64 index type",
			197: "memory64: (memory i64 1) at :1 — an i64 index type",
			199: "memory64: (memory i64 1) at :1 — an i64 index type",
			200: "memory64: (memory i64 1) at :1 — an i64 index type",
			201: "memory64: (memory i64 1) at :1 — an i64 index type",
			202: "memory64: (memory i64 1) at :1 — an i64 index type",
			204: "memory64: (memory i64 1) at :1 — an i64 index type",
			205: "memory64: (memory i64 1) at :1 — an i64 index type",
			206: "memory64: (memory i64 1) at :1 — an i64 index type",
			207: "memory64: (memory i64 1) at :1 — an i64 index type",
			209: "memory64: (memory i64 1) at :1 — an i64 index type",
			210: "memory64: (memory i64 1) at :1 — an i64 index type",
			211: "memory64: (memory i64 1) at :1 — an i64 index type",
			212: "memory64: (memory i64 1) at :1 — an i64 index type",
			214: "memory64: (memory i64 1) at :1 — an i64 index type",
			215: "memory64: (memory i64 1) at :1 — an i64 index type",
			216: "memory64: (memory i64 1) at :1 — an i64 index type",
			217: "memory64: (memory i64 1) at :1 — an i64 index type",
		},
		"float_memory0.wast": {
			20: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			21: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			22: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			23: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			24: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			25: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			26: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			27: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			28: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			29: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			30: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			31: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			32: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			33: "multi-memory: 6 memories at :5, so a memarg carries flags bit 6",
			46: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			47: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			48: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			49: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			50: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			51: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			52: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			53: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			54: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			55: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			56: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			57: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			58: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
			59: "multi-memory: 2 memories at :35, so a memarg carries flags bit 6",
		},
		"float_memory64.wast": {
			15:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			16:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			17:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			18:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			19:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			20:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			21:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			22:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			23:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			24:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			25:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			26:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			27:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			28:  "memory64: (memory i64 (data \"\\00\\00\\a0\\7f\")) at :5 — an i64 index type",
			40:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			41:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			42:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			43:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			44:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			45:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			46:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			47:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			48:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			49:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			50:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			51:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			52:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			53:  "memory64: (memory i64 (data …)) at :30 — an i64 index type",
			67:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			68:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			69:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			70:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			71:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			72:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			73:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			74:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			75:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			76:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			77:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			78:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			79:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			80:  "memory64: (memory i64 (data …)) at :57 — an i64 index type",
			92:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			93:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			94:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			95:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			96:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			97:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			98:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			99:  "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			100: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			101: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			102: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			103: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			104: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			105: "memory64: (memory i64 (data …)) at :82 — an i64 index type",
			119: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			120: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			121: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			122: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			123: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			124: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			125: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			126: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			127: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			128: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			129: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			130: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			131: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			132: "memory64: (memory i64 (data \"\\01\\00\\d0\\7f\")) at :109 — an i64 index type",
			144: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			145: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			146: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			147: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			148: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			149: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			150: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			151: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			152: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			153: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			154: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			155: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			156: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
			157: "memory64: (memory i64 (data …)) at :134 — an i64 index type",
		},
		"imports1.wast": {
			12: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			13: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
			14: "multi-memory: 4 memories at :1, so a memarg carries flags bit 6",
		},
		"imports2.wast": {
			17: "multi-memory: 2 memories at :9, so a memarg carries flags bit 6",
			18: "multi-memory: 2 memories at :9, so a memarg carries flags bit 6",
			19: "multi-memory: 2 memories at :9, so a memarg carries flags bit 6",
		},
		"load0.wast": {
			18: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			19: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
		},
		"load1.wast": {
			31: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			32: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			33: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			34: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			35: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			37: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			38: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			39: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			40: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
			41: "multi-memory: 2 memories at :10, so a memarg carries flags bit 6",
		},
		"memory-multi.wast": {
			41: "multi-memory: 2 memories at :26, so a memarg carries flags bit 6",
			42: "multi-memory: 2 memories at :26, so a memarg carries flags bit 6",
		},
		"memory64.wast": {
			12: "memory64: (memory i64 (data)) at :11 — an i64 index type",
			14: "memory64: (memory i64 (data \"\")) at :13 — an i64 index type",
			16: "memory64: (memory i64 (data \"x\")) at :15 — an i64 index type",
		},
		"memory_copy0.wast": {
			19: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			21: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			22: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			23: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			24: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			25: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			26: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			29: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			30: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			31: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			32: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			33: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			34: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			35: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			38: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			39: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			40: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			41: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			42: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			43: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			44: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			45: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			48: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			49: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			52: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
			53: "multi-memory: 4 memories at :2, so a memarg carries flags bit 6",
		},
		"memory_copy64.wast": {
			15:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			17:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			18:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			19:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			20:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			21:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			22:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			23:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			24:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			25:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			26:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			27:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			28:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			29:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			30:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			31:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			32:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			33:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			34:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			35:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			36:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			37:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			38:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			39:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			40:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			41:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			42:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			43:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			44:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			45:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			46:   "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			57:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			59:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			60:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			61:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			62:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			63:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			64:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			65:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			66:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			67:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			68:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			69:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			70:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			71:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			72:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			73:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			74:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			75:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			76:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			77:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			78:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			79:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			80:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			81:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			82:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			83:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			84:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			85:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			86:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			87:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			88:   "memory64: (memory (export \"memory0\") i64 1 1) at :48 — an i64 index type",
			99:   "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			101:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			102:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			103:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			104:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			105:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			106:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			107:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			108:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			109:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			110:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			111:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			112:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			113:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			114:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			115:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			116:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			117:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			118:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			119:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			120:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			121:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			122:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			123:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			124:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			125:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			126:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			127:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			128:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			129:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			130:  "memory64: (memory (export \"memory0\") i64 1 1) at :90 — an i64 index type",
			141:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			143:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			144:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			145:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			146:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			147:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			148:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			149:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			150:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			151:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			152:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			153:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			154:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			155:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			156:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			157:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			158:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			159:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			160:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			161:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			162:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			163:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			164:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			165:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			166:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			167:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			168:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			169:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			170:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			171:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			172:  "memory64: (memory (export \"memory0\") i64 1 1) at :132 — an i64 index type",
			183:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			185:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			186:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			187:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			188:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			189:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			190:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			191:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			192:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			193:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			194:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			195:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			196:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			197:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			198:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			199:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			200:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			201:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			202:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			203:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			204:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			205:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			206:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			207:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			208:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			209:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			210:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			211:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			212:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			213:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			214:  "memory64: (memory (export \"memory0\") i64 1 1) at :174 — an i64 index type",
			225:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			227:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			228:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			229:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			230:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			231:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			232:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			233:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			234:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			235:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			236:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			237:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			238:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			239:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			240:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			241:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			242:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			243:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			244:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			245:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			246:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			247:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			248:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			249:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			250:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			251:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			252:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			253:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			254:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			255:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			256:  "memory64: (memory (export \"memory0\") i64 1 1) at :216 — an i64 index type",
			267:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			269:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			270:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			271:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			272:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			273:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			274:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			275:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			276:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			277:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			278:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			279:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			280:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			281:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			282:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			283:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			284:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			285:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			286:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			287:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			288:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			289:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			290:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			291:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			292:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			293:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			294:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			295:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			296:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			297:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			298:  "memory64: (memory (export \"memory0\") i64 1 1) at :258 — an i64 index type",
			309:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			311:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			312:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			313:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			314:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			315:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			316:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			317:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			318:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			319:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			320:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			321:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			322:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			323:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			324:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			325:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			326:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			327:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			328:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			329:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			330:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			331:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			332:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			333:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			334:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			335:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			336:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			337:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			338:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			339:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			340:  "memory64: (memory (export \"memory0\") i64 1 1) at :300 — an i64 index type",
			4867: "memory64: (memory i64 1 1) at :4863 — an i64 index type",
			4879: "memory64: (memory i64 1 1) at :4875 — an i64 index type",
			4891: "memory64: (memory i64 1 1) at :4887 — an i64 index type",
		},
		"memory_fill0.wast": {
			18: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			19: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			20: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			21: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			22: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			23: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			26: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			27: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			28: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			31: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			36: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			37: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
			40: "multi-memory: 3 memories at :2, so a memarg carries flags bit 6",
		},
		"memory_grow64.wast": {
			14: "memory64: (memory i64 0) at :1 — an i64 index type",
			19: "memory64: (memory i64 0) at :1 — an i64 index type",
			20: "memory64: (memory i64 0) at :1 — an i64 index type",
			21: "memory64: (memory i64 0) at :1 — an i64 index type",
			22: "memory64: (memory i64 0) at :1 — an i64 index type",
			23: "memory64: (memory i64 0) at :1 — an i64 index type",
			26: "memory64: (memory i64 0) at :1 — an i64 index type",
			27: "memory64: (memory i64 0) at :1 — an i64 index type",
			28: "memory64: (memory i64 0) at :1 — an i64 index type",
			29: "memory64: (memory i64 0) at :1 — an i64 index type",
			30: "memory64: (memory i64 0) at :1 — an i64 index type",
			31: "memory64: (memory i64 0) at :1 — an i64 index type",
			32: "memory64: (memory i64 0) at :1 — an i64 index type",
			33: "memory64: (memory i64 0) at :1 — an i64 index type",
			41: "memory64: (memory i64 0) at :36 — an i64 index type",
			42: "memory64: (memory i64 0) at :36 — an i64 index type",
			43: "memory64: (memory i64 0) at :36 — an i64 index type",
			44: "memory64: (memory i64 0) at :36 — an i64 index type",
			45: "memory64: (memory i64 0) at :36 — an i64 index type",
			46: "memory64: (memory i64 0) at :36 — an i64 index type",
			53: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			54: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			55: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			56: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			57: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			58: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			59: "memory64: (memory i64 0 10) at :48 — an i64 index type",
			60: "memory64: (memory i64 0 10) at :48 — an i64 index type",
		},
		"memory_init64.wast": {
			17:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			19:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			20:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			21:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			22:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			23:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			24:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			25:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			26:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			27:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			28:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			29:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			30:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			31:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			32:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			33:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			34:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			35:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			36:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			37:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			38:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			39:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			40:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			41:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			42:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			43:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			44:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			45:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			46:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			47:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			48:  "memory64: (memory (export \"memory0\") i64 1 1) at :6 — an i64 index type",
			209: "memory64: (memory i64 1) at :203 — an i64 index type",
		},
		"memory_redundancy64.wast": {
			59: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			60: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			61: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			62: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			63: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			64: "memory64: (memory i64 1 1) at :5 — an i64 index type",
			65: "memory64: (memory i64 1 1) at :5 — an i64 index type",
		},
		"memory_trap0.wast": {
			23: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			24: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			35: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
		},
		"memory_trap1.wast": {
			237: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			238: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			242: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			244: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			246: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			248: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
			250: "multi-memory: 3 memories at :1, so a memarg carries flags bit 6",
		},
		"memory_trap64.wast": {
			21:  "memory64: (memory i64 1) at :1 — an i64 index type",
			22:  "memory64: (memory i64 1) at :1 — an i64 index type",
			268: "memory64: (memory i64 1) at :34 — an i64 index type",
			269: "memory64: (memory i64 1) at :34 — an i64 index type",
		},
		"simd_load.wast": {
			44: "SIMD: a v128 instruction in a function body at :34",
		},
		"store0.wast": {
			22: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			23: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			24: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
			25: "multi-memory: 2 memories at :3, so a memarg carries flags bit 6",
		},
		"store1.wast": {
			49: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
			50: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
			51: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
			52: "multi-memory: 2 memories at :30, so a memarg carries flags bit 6",
		},
		"table_grow64.wast": {
			12: "memory64: (table $t64 i64 0 externref) at :1 — an i64 index type",
			17: "memory64: (table $t64 i64 0 externref) at :1 — an i64 index type",
			25: "memory64: (table $t64 i64 0 externref) at :1 — an i64 index type",
		},
	}

	files := boardFiles(t)
	for _, f := range files {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		// **Read off the board's own count, not re-derived by asking the decoder.**
		//
		// This loop used to be `for _, c := range s.Commands { if isGated(decode(c.Module))
		// ... }`, which is a *second* trigger for the same concept — and it under-matched. It
		// can only see a decline that happens at `c.Module`, so when the interpreter added the
		// instantiation path (a **text** module declined for memory64, with no `c.Module` to
		// ask about) 17 of the board's 33 declines were outside its reach entirely. The
		// allowlist looked complete while covering half its population, and the 17 were
		// simultaneously *fails* one command downstream. One concept, one trigger (#82); the
		// run loop counts, and the control reads what it counted.
		r := run(s)
		if len(r.GatedAt) != r.Gated {
			t.Errorf("%s: Gated is %d but GatedAt has %d lines; the counter and the list "+
				"disagree, so this control is reading a subset it cannot name", f, r.Gated, len(r.GatedAt))
		}
		declined := make(map[int]bool, len(r.GatedAt))
		for _, line := range r.GatedAt {
			declined[line] = true
			if _, ok := allowed[f][line]; !ok {
				t.Errorf("%s:%d declined by a feature gate but is not in the allowed set;\n"+
					"\tif the gate is right, add it with the feature named; if not, the decoder is over-gating and hiding a failure",
					f, line)
			}
		}
		// The reverse: a stale entry would claim a decline that no longer happens,
		// overstating how much the gates are doing.
		for line := range allowed[f] {
			if !declined[line] {
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

	allOn := allFeaturesOn(t)
	d := &binary.Decoder{Features: allOn}
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
		e := engine()
		e.Decode = decodeAllOn
		// The instantiation path takes the lane's gates too — see instantiateWith for
		// why this line exists and what its absence cost.
		e.Instantiate = func(c Command) (Instance, Stratum, error) {
			return instantiateWith(allOn, c)
		}
		r := s.RunGated(e)
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
	// **Now slack-checked** (decision 0013): a floor that drifts 3380 behind its
	// measurement is a failure, not a habit lapse. boardBound asserts both directions, and
	// the error it raises on the upper side names the raise it wants.
	// **4178 → 17939, and the gap to the default lane's 17923 is the 16 this lane is for.**
	// The interpreter raises both floors by the same 13761; what makes this lane's number
	// larger is the same set of gated vectors it always was, now including the 17 memory64
	// modules that reach the interpreter through instantiation. Those 17 are `gated` on the
	// default board and *answered* here, which is the deferral-cannot-become-a-disappearance
	// property working on a path it could not previously see — see instantiateWith for the bug
	// this lane caught in the course of it.
	// **17939 → 27137, and the gap to the default lane's 26307 is 830.** #7's memory raises both
	// floors; this lane's rises further because the default board's 1031 `gated` vectors answer
	// on the merits here, and with load and store arms in place most of them now answer
	// *correctly*.
	//
	// **The gap is the gated population's pass share, not the population** — a distinction this
	// comment's first draft got wrong by calling 830 "the third-verdict population", and the
	// numbers refute it in one line: 1031 declines become **830 passes and 201 fails** here, and
	// fail moves 5349 → 5550 to match. 830 + 201 = 1031, which is the deferral-cannot-become-a-
	// disappearance property as arithmetic rather than as a claim: every parked vector is
	// accounted for in this lane, and the 201 stay honestly red until their feature works.
	// Quoting 830 as the population would have implied 201 vectors vanished.
	const allOnPassFloor = 27137
	boardBound(t, "allOnPassFloor", totalPass, allOnPassFloor, boardBoundSlack, floorBound,
		"a gated feature regressed, which the Gated==0 assertion above cannot see: with every "+
			"gate on, a broken feature turns a pass into a fail and leaves Gated at zero")
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
	// The corpus identity, printed *with* the counts and not merely available somewhere
	// (#42). A count is a claim about a corpus, so a board line that does not name its
	// corpus is a verdict without an identity check — two developers on different fetch
	// dates can quote incompatible numbers and both be honest. Pairing the suite pin with
	// the engine commit is what makes "never quote a suite count that wasn't run"
	// unambiguous about *which* run.
	t.Logf("corpus: suite pin %s (%s)", suitePin(t), suiteDir)
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
	// Slack-checked (0013), and this is the bound where staleness runs *downward*: every
	// capability that lands moves the actual further below the ceiling, so the gap grows on
	// exactly the schedule of ordinary progress. boardBound measures the distance in the
	// direction that applies to a ceiling.
	//
	// **60872 → 32764, and this is the first time this number has fallen for the reason it
	// was built to measure.** Both previous movements were the corpus growing; this one is
	// 28108 vectors *answered*, because `assert_return` stopped being a head the classifier
	// records and became a command the harness can ask. The whole fall is one head: 52638
	// assert_return commands were unsupported, 24530 still are — the shapes assertReturn
	// declines, which it declines structurally and says so at each arm — and 28108 now get a
	// verdict. Nothing else moved; the corpus was the same 253 files. (Past tense as of the
	// next movement below, which selects a 254th — the sentence was true when written and the
	// tense is what keeps it true.)
	//
	// The lowering is the record of the progress, per the standing rule, and it is lowered
	// **in the PR that drained it** rather than left as slack for a later reader to notice.
	//
	// # 32764 → 32377, and the small number is the honest one
	//
	// −387 in the PR that gave the engine a memory, and the modesty is the finding rather than an
	// embarrassment to be dressed up: memory work paid in the **fail** column (exec 8713 → 440),
	// not here, because those 8,000 vectors were already *asked* and answered wrongly. This
	// column moves only when a command shape the classifier used to decline becomes askable, and
	// #7's memory work made two such shapes askable:
	//
	//	347  `invoke` — a bare top-level `(invoke …)`, 357 → 10. It is a state mutation, so
	//	     walking past it did not merely lose the command: it made the *following*
	//	     `assert_return` read a memory nobody had stored to, and 73 of those were being
	//	     scored as the interpreter computing a wrong value. A skip is not a verdict, with the
	//	     roles swapped — the skip passed by asking nothing and made a neighbour fail.
	//	 40  `assert_trap` wrapping a bare `(module …)` — instantiation itself trapping, which
	//	     0015 is the record of. 54 such forms exist and all 54 are on the board; **40 of them
	//	     moved off this column and 14 did not, because those 14 were not *on the board* at
	//	     all.** They are `data1.wast`, every form in which is this shape — so under
	//	     per-command selection the file held no scorable command, boardFiles did not select
	//	     it, and it contributed 0 to this column and 0 to the denominator. Its 14 therefore
	//	     arrive as **new** commands in a **new file**, which is the one place in this PR the
	//	     board's file count moves: **253 → 254**. Measured by set-differencing the
	//	     `assert_trap` command keys at b11a664 against this tree — 4963 → 4977, the entire
	//	     difference being `data1.wast:14` — rather than inferred from 54 − 40, which is the
	//	     arithmetic that hid the distinction in this comment's first draft. A conversion
	//	     lowers this column; an admission raises the denominator instead, and a bare
	//	     `54 − 40 = 14` calls them the same thing.
	//
	// 347 + 40 = 387, which is why the two lines are quoted rather than the total alone. The 10
	// `invoke` commands still unsupported are the shapes classify declines structurally.
	const unsupportedCeiling = 32377
	boardBound(t, "unsupportedCeiling", totalUnsup, unsupportedCeiling, boardBoundSlack, ceilingBound,
		"either a capability regressed or the corpus moved; both need an explanation rather "+
			"than a raised ceiling")

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
	// Slack 0: at its terminal value, where the distance to the actual is not a quantity
	// that can grow. 0004 fixes the terminal at zero, so this one cannot go stale even in
	// principle — unlike unsupportedCeiling above, which drains without a floor under it.
	const unimplementedCeiling = 0
	boardBound(t, "unimplementedCeiling", totalUnimpl, unimplementedCeiling, 0, ceilingBound,
		"a new capability gap appeared or one widened; the column exists to drain, so growth "+
			"needs an explanation")

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
	// ceiling is now **four ceilings over a structural partition**: a new decoder defect
	// arrives as `binaryFail 1 > 0` regardless of what the other three columns are doing.
	// The partition is not on the bucket string because the layers share strings —
	// `malformed UTF-8 encoding` is a bucket on both front ends — and *when a partition's
	// members share a value, an equality on that value is not a partition check*.
	//
	// **The key is Failure.Stratum, not Failure.Kind, and the change of key is #7's first
	// finding rather than a refactor.** Kind worked as the partition key for exactly as
	// long as every failure was caused by the command it was reported against. An
	// `assert_return` breaks that identity: the *command* is an assert_return, and the
	// *defect* belongs to whichever component failed to produce the instance. Measured at
	// this revision, **13775 of the 14216 fails are `assert_return`s whose module never
	// instantiated, and every one of them is text.EncodeModule's instruction frontier
	// (#8)** — so a Kind-keyed switch would have charged 13775 encoder gaps to the
	// interpreter's brand-new ceiling and reported the wrong stratum as broken on the day
	// the interpreter landed. That is the same defect as #69's `default` arm, one layer
	// deeper: not a Kind assigned to the wrong column, but a *column that cannot be
	// derived from Kind at all*.
	//
	// So the stratum is **stated at the failure site** by the code that knows which entry
	// point returned the error, never derived here. Deriving it is precisely what put 13
	// text reds in the decoder's column in #69.
	//
	// Falsified in both directions before being trusted, per the print-don't-trust rule:
	// with StratumEncode's arm folded into StratumExec, execFail reads 14347 and encodeFail
	// 0, and both arms fail. That is the check TestSectionSizeBothSigns's grave (#34) asks
	// for — a partition test verified against the partition, not against its labels.
	//
	// **Every arm is named and none is a `default`.** A `default` absorbs every stratum
	// added later and assigns it a layer by omission; StratumUnset is a loud failure rather
	// than a quietly larger number, which is the same move as the unregistered-capability
	// panic.
	binaryFail, textFail, encodeFail, execFail := 0, 0, 0, 0
	for _, fs := range aggBuckets {
		for _, f := range fs {
			switch f.Stratum {
			case StratumBinary:
				binaryFail++
			case StratumText:
				textFail++
			case StratumEncode:
				// **A column of its own, not folded into StratumText** (#8). ReadModule
				// answers 254 files' module forms with 0 reds; EncodeModule cannot yet emit
				// most instruction bodies. Folding them would raise the reader's ceiling
				// from 0 to 13775 and destroy the only instrument watching the reader for
				// regressions — one instrument per component, or neither is an instrument.
				encodeFail++
			case StratumExec:
				// **A fourth partition, not a third arm on an existing one** (#7). The
				// others are the front ends; this is the execution layer, and merging it
				// into any of them would charge one layer's reds to another layer's ceiling.
				execFail++
			case StratumUnset:
				t.Errorf("failure at line %d (%q, kind %v) carries no stratum — the site "+
					"that reported it did not say which component failed, so its red would "+
					"be charged to whichever ceiling this switch defaulted to",
					f.Line, f.Expect, f.Kind)
			default:
				t.Errorf("failure of unhandled stratum %v at line %d (%q) — a new Stratum "+
					"was added without a ceiling under it", f.Stratum, f.Line, f.Expect)
			}
			if f.Kind == KindUnsupported {
				t.Errorf("a KindUnsupported command produced a failure bucket entry at "+
					"line %d (%q); unsupported commands are not scored, so this is the "+
					"run loop losing track of a verdict", f.Line, f.Expect)
			}
		}
	}
	if binaryFail+textFail+encodeFail+execFail != totalFail {
		t.Errorf("fail partition sums to %d but the column is %d; a failure escaped every "+
			"arm, so one of the four ceilings below is watching a subset it cannot name",
			binaryFail+textFail+encodeFail+execFail, totalFail)
	}
	t.Logf("  fail by stratum: binary %d, text %d, encode %d, exec %d",
		binaryFail, textFail, encodeFail, execFail)

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
	// TestGatedVectors with the feature named, and **passed** in the
	// all-gates-on lane, where 4178 pass / 0 fail / 0 gated.
	// Slack 0, and deliberately: a ceiling *at* zero cannot go stale, because the distance
	// between "at most 0" and "0" is not a quantity that can silently grow. That is not an
	// omission to be repaired by a future reader — see 0013 — and it is stated because
	// silence about a member of the space is how #48 happened.
	const binaryFailCeiling = 0
	boardBound(t, "binaryFailCeiling", binaryFail, binaryFailCeiling, 0, ceilingBound,
		"a defect landed in the binary decoder; this ceiling is deliberately not shared with "+
			"the text column so that a decoder regression cannot hide inside the text column's "+
			"unwritten grammars")

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
	// Slack 0 for the same structural reason as binaryFailCeiling above.
	const textFailCeiling = 0
	boardBound(t, "textFailCeiling", textFail, textFailCeiling, 0, ceilingBound,
		"either the reader regressed on vectors it used to answer, or the corpus moved")

	// # The encoder's ceiling — 4693, after section 11 took 9082 out of it
	//
	// **This column exists because the interpreter's arrival created it, and it is the reason
	// Stratum replaced Kind as the partition key.** Every member is an `assert_return` whose
	// module `text.EncodeModule` could not emit, so the vector reaches the interpreter with no
	// instance to run against. The command is an assert_return; the defect is the encoder's.
	// Charged by Kind, all 13775 it held on the day it was born would have landed on `execFail`
	// and reported the interpreter as 40× more broken than it is.
	//
	// A **work plan with a ceiling**, not a defect count — the same shape textFailCeiling had
	// at 391. The buckets printed above are the order to take them in, and they are keyed by
	// the encoder's own message, so the column reads as #8's instruction work list:
	//
	//	1162  call_indirect               1087  block                 620  loop
	//	 420  if                           405  ref.null              318  select
	//	 221  table.init                   201  memory.init            42  return_call_indirect
	//	  41  br_table                      23  array.copy             23  array.init_data
	//	  17  ref.cast                      17  i8x16.extract_lane_s   14  (global …) field
	//	  12  (start …) field           …and 20 more, all #8, plus 3 for #77
	//
	// **The `(data …)` bucket is gone, and section 11 is what removed it** (13775 → 4693). It led
	// this list at 8882 with `(memory …)` behind it at 212, and both are absent: the emitter now
	// writes the data section, the data count section, and the memory field's inline-data sugar.
	// The 9082 is 8882 + 212 − 12, the twelve being modules whose *second* frontier is also in a
	// module field — which `code.go:195` pre-registered as the reason a per-shape census is
	// upper-bound-shaped, and which the memarg emitter's 199 demonstrated in the other direction.
	//
	// **Where the 9082 went matters more than that it left, and it did not become passes.** 8272
	// became `execFail` and 810 became `gated`; **zero became `pass`.** A `(data …)` module that
	// now encodes still needs a *load arm* to answer its `assert_return`, so the queue moved one
	// stratum down rather than draining — `interp: no arm for opcode 2d` alone holds 8001 of it.
	// Stated at the site because a ceiling falling by 9082 while the pass count does not move is
	// exactly the shape a reader would otherwise mis-read as progress.
	//
	// **Separate from textFailCeiling, and the separation is load-bearing.** `text.ReadModule`
	// answers all 254 files' module forms with **0** reds; `text.EncodeModule` cannot emit most
	// instruction bodies. Folding them would take the reader's ceiling from 0 to 4693 and
	// destroy the only instrument watching the reader for regressions — one instrument per
	// component, or neither is an instrument. They are two entry points in one package, which
	// is exactly the case a Kind-keyed partition cannot express and a stated stratum can.
	//
	// Slack 0 like the two above: it may only fall, and it falls as #8 lands.
	//
	// # Re-based 4693 → 4909, and a slack-0 ceiling rising is a claim that needs a diagnosis
	//
	// This bound says the encoder may only lose ground, so its own message read the rise as an
	// encoder regression. It is not one, and the way to tell is to **partition the column by the
	// Kind that produced it** rather than to trust either the message or this comment:
	//
	//	assert_return  4693   ← unchanged, to the vector
	//	invoke          216   ← a population that did not exist before this PR
	//
	// The `assert_return` half is *identical*. What grew is the denominator: #7's memory work
	// taught the harness to run a bare top-level `(invoke …)` as a command instead of walking
	// past it (KindInvoke), so 216 more commands now meet the encoder's **unmoved** frontier and
	// are honestly charged to it. Nothing the encoder used to emit stopped being emitted.
	//
	// Rebased rather than exempted, and the ceiling keeps slack 0 — but note what the instrument
	// could and could not say. It fired correctly (the number rose), and its *stated reason* was
	// wrong (nothing regressed), because a ceiling can express "may only fall" and cannot express
	// "may only fall per vector asked". A bound over a growing population is measuring two things
	// at once; the Kind split above is the one that answers the question this bound was for, and
	// it is written here as the check to run the next time this rises rather than as a fact about
	// this rise. Same shape as the exec ceiling's own note that it would rise as #8 landed.
	const encodeFailCeiling = 4909
	boardBound(t, "encodeFailCeiling", encodeFail, encodeFailCeiling, 0, ceilingBound,
		"the wat encoder lost ground: either it stopped emitting an instruction it used to "+
			"emit, or the corpus moved. This ceiling is deliberately not shared with the text "+
			"column so an encoder regression cannot hide behind ReadModule's zero")

	// # The interpreter's ceiling — 8713, the whole of it #7's opcode work list
	//
	// **The first ceiling this project has had over executed code.** Every member is a vector
	// that reached `interp.Invoke` and got an error, bucketed by that error's own text, which
	// `ErrUnsupportedOp` renders with its bytes — so the column is per-opcode and reads as the
	// arms exec.go's switch still owes:
	//
	//	8001  2d i32.load8_u    89  3f memory.size   53  40 memory.grow  45  10 call
	//	  41  28 i32.load       35  2b f64.load      35  2a f32.load     32  29 i64.load
	//	  26  0f return         25  fc 03            24  fc 04          24  fc 06
	//	  23  fc 07             22  fc 00            22  fc 02          21  fc 01
	//	  19  fc 05             15  2c i32.load8_s    15  2e i32.load16_s  … 30 more, each ≤15
	//
	// **Re-based 441 → 8713 by section 11, and the +8272 is one bucket rather than a spread.**
	// `2d i32.load8_u` went 9 → 8001 by itself, because `address*.wast` and the two
	// `memory_copy*.wast` files are per-offset sweeps over a data segment — every vector in them
	// needed the data section to instantiate and needs a load arm to answer. So the largest number
	// on the board is now a single missing arm, which is the most actionable this column has ever
	// been: the next PR's product work is named by it rather than inferred.
	//
	// **Re-based 356 → 441 before that by #8's memarg emitter**, and that +85 was accounted per
	// opcode: `10 call` +43, the load/store region `28`/`2d`/`36`–`3e` +41, `40 memory.grow` +1.
	// Both re-basings were measured by diffing this column against the same corpus with the work
	// stashed — the rise this comment's next paragraph predicted, arriving twice for the reason it
	// predicted, and every member of it is `no arm for opcode`, which is #7's frontier and not a
	// wrong answer.
	//
	// Two things this ceiling is *not*. It is not the interpreter's total exposure: 4693
	// vectors never reach it at all, held upstream by encodeFailCeiling, so this number will
	// **rise** as #8 lands and more modules instantiate. That is the honest direction and it is
	// stated here so the rise is not read as a regression — but a ceiling cannot express "may
	// rise for a good reason", so when #8 moves it this constant is re-based *with the
	// instruction that unblocked it named*, exactly as textFailCeiling was re-based stepwise.
	//
	// And it is not a defect count either. `interp: no arm for opcode 3f` is a stratum boundary
	// honestly declared, the same category as the text reader's named boundaries — a vector the
	// engine reached and could not answer, reported as a fail with a bucket rather than hidden
	// behind a fourth verdict. *Bucketed failures are the work plan.*
	//
	// Slack 0.
	//
	// # Re-based 8713 → 440 by #7's memory work, which is what this column was for
	//
	// The `2d i32.load8_u` bucket above went **8001 → 0**, and with it the whole load/store
	// region, `3f memory.size` and `40 memory.grow`. What is left is 440, and it partitions to
	// exactly 440 — which is the point of a ceiling that names its members:
	//
	//	291  opcode arms this phase does not have, `no arm for opcode` and nothing else: `10
	//	     call` 74, `0f return` 26, the `fc 00`–`fc 07` saturating conversions 180, `23
	//	     global.get` 7, `fc 09`/`fc 10` 4. #7's remainder and #8's, unrelated to memory.
	//	 48  `assert_return value mismatch`, every one downstream of a missing `fc 0a`/`fc 0b`
	//	     — a vector that copies or fills before it loads reads bytes nobody wrote.
	//	 41  `memory %d is imported, and linking is not implemented (contract §3)` — the third
	//	     error category (ErrUnsupported), naming linking rather than blaming a table. 32
	//	     reached at the instruction, 9 as `no instance` from the module command.
	//	 34  `assert_trap (module)` where the trap did not arrive: 16 want *out of bounds table
	//	     access* (element segments, unbuilt), 15 *out of bounds memory access* against a
	//	     memory this phase cannot link, 3 *unreachable* from a start function.
	//	 26  `fc 0a`/`fc 0b` themselves, reached directly rather than through a later load.
	//
	// **The 95 value mismatches this work introduced are the number that had to be explained
	// rather than quoted**, because that bucket was 0 before this PR and a value mismatch reads
	// as the interpreter computing wrong answers. 22 of them were: `memory.size $mem1` returning
	// $mem3's page count, because the memory index space is shared between imports and
	// definitions and this table was sized `len(m.Memories)`. Not an unimplemented import
	// reported honestly — a *wrong answer about a different memory*, green on 22 vectors by
	// construction. 73 were the harness's, not the engine's: a bare top-level `(invoke …)` was
	// never run, so a following `assert_return` read a memory nobody had stored to. Both fixed;
	// the 48 that remain are the `fc 0a`/`fc 0b` line above.
	//
	// **The partition above was drafted from the census and then corrected by running it**, which
	// is worth the sentence because the draft was plausible: it said `10 call` 45 (it is 74), put
	// the residue at 289 (291), and omitted the 34 `assert_trap (module)` fails entirely — a
	// whole category, in a list that claimed to sum. The five lines now sum to 440 because they
	// were read off the buckets rather than reasoned toward them.
	//
	// The prediction in the paragraph above — that this number would **rise** as #8 landed and
	// more modules instantiated — is left standing rather than trimmed, because it was right and
	// then overtaken: it rose 356 → 441 → 8713 exactly as written, and then the arms arrived and
	// it fell by 8273. A forecast that came true twice and was then made obsolete by the work it
	// was forecasting is the record worth keeping.
	const execFailCeiling = 440
	boardBound(t, "execFailCeiling", execFail, execFailCeiling, 0, ceilingBound,
		"the interpreter answered fewer vectors than it did: either an opcode arm regressed or "+
			"a value comparison started disagreeing. A *rise* caused by #8 unblocking more "+
			"modules is legitimate and gets this constant re-based with the instruction named")

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
	// # 4162 → 17923, and 13761 of those are values the engine computed
	//
	// **The first movement on this floor that is not about a front end.** Every raise above was
	// a rejector installed or an over-rejection repaired; this one is 13761 `assert_return`
	// vectors where a function ran and returned the bits the suite asked for. The floor's
	// character changes with it: up to here it watched for over-rejection, and it now watches
	// for that *plus* an interpreter that starts computing a different answer.
	//
	// The number is not decomposed further because the decomposition that matters is the
	// **fail** side's — an interpreter that returns a wrong value produces a member of
	// `assert_return value mismatch`, and there are **0** of those. That zero is the sharper
	// claim: 13761 vectors compared bit-for-bit against expectations read by an independent
	// literal reader (see value.go for why the reader is independent, and grave #106 for the
	// echo it exists to avoid), and not one disagreed.
	//
	// Falsified by perturbing `binI32`'s 0x6a arm to `a + b + 1`, and the measurement corrected
	// this paragraph's first draft. It predicted the bucket would fill "while this floor barely
	// moves" — reasoning from the 13761-vector slack rather than from the mechanism, and wrong,
	// because a floor fails on *any* drop regardless of slack. What actually ran: 10 vectors
	// into `assert_return value mismatch`, `execFail` 356 → 366 tripping execFailCeiling, and
	// this floor 17923 → 17913, failing. Three instruments, three fails, and the sharpest is
	// the ceiling — 10 against a bound of 356 versus 10 against a floor of 17923. Stated
	// because a floor that claims to be the evidence for a property it is the weakest witness
	// to is overclaiming.
	// # Re-based 17923 → 26307 by #7's memory, and the +8384 is one family
	//
	// Load and store arms, `memory.size`, `memory.grow`, memarg addressing, OOB traps and
	// instantiation-time data-segment copying: one family, and the pass column's largest single
	// jump. The account is on the exec ceiling below, which fell 8713 → 440 in the same motion —
	// the two are the same 8,000 vectors seen from opposite sides, which is why a rise here and a
	// fall there is one fact and not two.
	//
	// **The gain was measured rather than forecast, on Scott's rider, and the census was generous
	// by 289 in one direction and blind by 136 in the other.** Pre-registered: ~8,424 payable,
	// 289 residue. Delivered: exec 8713 → 440, the 289 residue exact — and two buckets the census
	// had not predicted at all, 95 value mismatches and 41 memory-index errors, where 95 had been
	// **zero** before this PR. Quoting the drop without them would have been the flattering half
	// of a measurement. Partitioned: 22 were imported memories shifting the memory index space
	// (fixed, class now 0), 73 were the harness never running a bare top-level `(invoke …)` — the
	// engine right, the jig not having set the state up — and the 48 that remain are all
	// downstream of the missing `fc 0a`/`fc 0b` arms and say so.
	const passFloor = 26307
	boardBound(t, "passFloor", totalPass, passFloor, boardBoundSlack, floorBound,
		"a regression in a grammar that used to answer, or the corpus moved")
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
		_ = s.RunWith(Engine{Decode: decode, IsGated: isGated, Has: []Capability{CapWatReader}})
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

	// vacuityBound, not floorBound: these two are *plausibility* bounds and their looseness
	// is the design (2000 against 2143, 230 against 242). Slack-checking them would fire on
	// a control working exactly as intended, and a gate that fires for reasons which are not
	// findings trains the reflex of scrolling past it. Routed through boardBound anyway, so
	// the exemption is named at one place rather than being an absence — TestEveryBoardBound-
	// IsChecked reads this call, and *a precondition that excuses a gate is licensed at one
	// place, or it is a hole*. (0013.)
	boardBound(t, "totalFloor", total, totalFloor, 0, vacuityBound,
		"the span mechanism is not retaining source, so commands the pass floor counts on are "+
			"back in the unsupported column")
	boardBound(t, "filesFloor", withAny, filesFloor, 0, vacuityBound,
		"a total floor cannot catch this: const.wast alone holds 402, so the distribution needs "+
			"its own bound")
}
