// Package testenv holds the test harness's strictness policy: which
// preconditions a test may excuse itself for, and when it may not.
//
// It exists because of a grave (#29). Every board test in internal/spec called a
// helper that did t.Skip when testdata/spec was absent, and the CI job running
// those tests never vendored the suite — so the pass floor, the closed-bucket
// regressions, the fixture-citation checks, and the gated allowlist skipped on
// every green CI run in the project's history. No CI green had ever asserted a
// suite count. The boards were never false; CI's countersignature was vacuous,
// and vacuous is indistinguishable from confirming at a glance.
//
// The lesson generalized past the instance: *a skip is not a verdict.* A skip is
// a report about the **question** — it could not be asked — so treating it as an
// answer means any gate with an unasserted precondition is a gate with an off
// switch nobody watches. The presence assertion in CI is the belt; this package
// is the suspenders, and it enforces from inside the harness what reading a log
// for "no SKIP lines" can only confirm from outside.
//
// A skip license is legitimate — running `go test ./...` on a fresh clone with no
// vendored corpus should work, and that convenience is why the skips exist at
// all. What is not legitimate is the same license applying in CI. So every skip
// site names its license here, and BURROUGHS_NO_SKIP=1 revokes them all.
//
// This is deliberately a non-test package rather than a _test.go helper: both
// internal/spec and internal/binary need it, internal/binary must not import
// internal/spec (that cycle is real — spec imports binary for its board), and a
// policy duplicated in two places is a policy that will diverge.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
)

// NoSkipEnv is the environment variable that revokes every skip license. CI sets
// it in every job that runs tests, not only the conformance lane: the grave was a
// precondition nobody asserted, and confining the fix to the one job that
// prompted it would leave the class open.
const NoSkipEnv = "BURROUGHS_NO_SKIP"

// SuitePaths is the **one definition of the suite population**: which files in a vendored
// testdata/spec are vectors. Every count, floor, seeder and board selector resolves it through
// here, so a disagreement about the population is a disagreement nothing can have.
//
// # It excludes AppleDouble sidecars, and that is the whole point (#340)
//
// `filepath.Glob("*.wast")` matches a *leading dot* — Go's `filepath.Match` has no special
// handling for it — while a POSIX shell's `*` does not. So `._address.wast`, the resource-fork
// sidecar macOS `tar` writes, is a vector to Go and invisible to every `ls *.wast` in the repo.
// #340's specimen is that asymmetry firing: a copy step left 257 sidecars beside 257 vectors,
// the shell checker reported 257 on the poisoned tree and 257 on the clean one, and Go saw 514.
// Twenty instruments reddened for the copy rather than for the architecture.
//
// **#340 prescribed making the shell dot-aware "so the shell counts what Go counts", and that
// prescription is wrong on its own goal** — measured rather than reasoned:
//
//	directory holding address.wast and ._address.wast
//	Go filepath.Glob("*.wast")               2   ← counts the sidecar
//	sh  ls *.wast | wc -l                    1
//	find -name '*.wast'                      2
//	find -name '*.wast' ! -name '._*'        1   ← the prescription: agrees with the *shell*
//
// The prescribed expression reproduces the shell's population, not Go's, so the two sides would
// still have disagreed — same direction, one layer better disguised. The fix is to pick the
// population that is *right* rather than the one that is easier to reach from either side, and a
// sidecar is not a vector: it is excluded on both sides, here and in the three shell sites, and
// then a poisoned tree yields 257 everywhere and a board computed over the real corpus.
//
// A prefix match rather than a `.`-prefix match: `._` is AppleDouble's own marker, and excluding
// every dotfile would silently widen this to a class nobody measured.
func SuitePaths(suiteDir string) ([]string, error) { return vectorsIn(suiteDir) }

// vectorsIn is "which files in this directory are vectors", in one place.
//
// Factored out when the threads lane needed the same question asked of
// `testdata/spec/proposals/threads` (#513). The exclusion above is the whole content of
// #340, and spelling it a second time in a second selector is how the two come to
// disagree about a sidecar — the *one definition* argument SuitePaths' own comment makes,
// applied to SuitePaths itself the moment it stopped being the only caller.
func vectorsIn(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.wast"))
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(paths, func(p string) bool {
		return strings.HasPrefix(filepath.Base(p), "._")
	}), nil
}

// ProposalDir is where one tracked proposal's vectors live under a vendored suite.
//
// The upstream testsuite ships `proposals/<name>/` beside the core vectors, and
// `SuitePaths`' glob is **one level**: those directories have never been in any board's
// population, which is the gap #513 exists to close for `threads`.
func ProposalDir(suiteDir, proposal string) string {
	return filepath.Join(suiteDir, "proposals", proposal)
}

// ProposalPaths is the population selector for one proposal's vectors.
//
// Through the same definition as the core suite's, so a sidecar is excluded here for the
// reason it is excluded there and not because someone remembered to.
//
// **A separate population rather than a widening of SuitePaths**, and the reason is that
// SuitePaths is load-bearing for facts that have nothing to do with any proposal: the fetch
// script reconciles it against `files="257"` exactly, `MinSuiteFiles` floors it,
// `unsupportedCeiling` is a monotonic bound whose subject is *that* corpus, and
// `TestPhase1Files`' `256 files` is published in `CHANGELOG.md` as a conformance claim.
// Folding four files in would move all of those at once and make a released board
// unreproducible — and it would put fails into the all-gates-on lane, whose entire guarantee
// is that nothing hides. (Endorsed by chat-Claude on the #519 review, and prescribed by #513
// itself for three named reasons.)
func ProposalPaths(suiteDir, proposal string) ([]string, error) {
	return vectorsIn(ProposalDir(suiteDir, proposal))
}

// MinSuiteFiles is the floor for a vendored suite to count as present.
//
// A count rather than a directory existence check, because the failure this
// guards is a *partial* fetch as much as a missing one: an empty or truncated
// testdata/spec would satisfy os.Stat and then skip, or worse, report a board
// computed over three files as though it were the corpus. The upstream suite has
// 257 .wast files; 250 leaves room for upstream churn without leaving room for a
// broken fetch.
const MinSuiteFiles = 250

// SuiteDir is where the vendored suite lives, relative to the repo root.
//
// Used by RequireSuiteFile, which resolves from the root rather than taking a path. Not
// used by RequireSuite or SuiteFiles, which take the directory as a parameter on purpose:
// their falsification tests point them at empty temp dirs, so a door that resolved its own
// location could not be shown to fail. Two doors, two shapes, and the difference is which
// question each was written to answer — the same argument that made RequireSuiteFile a
// fourth door instead of a call to RequireSuite.
const SuiteDir = "testdata/spec"

// NoSkip reports whether skip licenses are revoked in this run.
func NoSkip() bool { return os.Getenv(NoSkipEnv) == "1" }

// RequireSuite is the gate every suite-dependent test calls.
//
// Default (local dev): skips with an actionable message when the corpus is
// absent. Under BURROUGHS_NO_SKIP=1: fails, because in CI the absence of the
// oracle is not an excuse for not consulting it.
//
// Note it asserts a *count*, not existence, in both modes — so a partial fetch
// fails loudly rather than producing a board over whatever happened to be there.
// A number computed from an unasserted corpus is the hearsay problem pointed at
// the oracle's inputs.
func RequireSuite(tb testing.TB, suiteDir string) {
	tb.Helper()

	n, err := countSuiteFiles(suiteDir)
	if n >= MinSuiteFiles {
		return
	}

	reason := fmt.Sprintf("spec suite not vendored: found %d .wast files in %s, want >=%d (run: make spec-tests)",
		n, suiteDir, MinSuiteFiles)
	if err != nil {
		reason = fmt.Sprintf("%s: %v", reason, err)
	}

	// GRAVE (#32, sibling): if/else rather than Fatalf-then-Skip. Fatalf only stops
	// the test because it calls runtime.Goexit, and leaning on a callee not
	// returning is control flow one refactor away from reporting both verdicts —
	// which is what the first version did, caught by a testing.TB fake. A verdict
	// that is correct only by accident of another function's behaviour is the same
	// family as a test that passes for the wrong reason.
	if NoSkip() {
		tb.Fatalf("%s\n\t%s=1 revokes skip licenses: a skip is a report that the question could not be asked, and CI must not accept that as an answer",
			reason, NoSkipEnv)
	} else {
		tb.Skip(reason)
	}
}

// RequireProposal is the gate a proposal lane calls, and it reconciles an **exact** count.
//
// # Why exact, where the core suite gets a floor
//
// A floor is a class bound: 250 survives a pin bump and catches a fetch that lost most of
// its corpus. Over a four-file directory a floor is nearly vacuous — *reconcile an extent,
// never floor it* (#340) — because the interesting losses are all small ones, and no floor
// can see an *addition* at any size. So this asserts the population for this pin and says
// which pin it is asserting for at the call site.
//
// # Why a wrong count is never a skip
//
// It calls RequireSuite first, which is not belt-and-braces: `proposals/<name>/` comes out of
// the same checkout as the core vectors, so a suite present at its pinned count means this
// directory is present too. That makes the two failures separable — *the corpus is not
// vendored* is RequireSuite's answer and carries its skip license, and *the corpus is
// vendored and this directory disagrees* is a hard error in both modes. A single door with
// one skip branch would have let a partial checkout excuse itself from a lane whose whole
// point is that this population is no longer excused (#513, *a skip is not a verdict*).
func RequireProposal(tb testing.TB, suiteDir, proposal string, want int) []string {
	tb.Helper()

	// The corpus question is delegated, and then this **returns** rather than relying on
	// RequireSuite not returning. With a real *testing.T its Fatalf calls runtime.Goexit; with
	// a testing.TB stand-in — which is how this package's own doors are watched failing — it
	// records the call and execution continues, and the extent check below would then fire a
	// second Fatalf about a directory that is missing because nothing was ever fetched. Two
	// diagnoses for one cause, the later one overwriting the actionable one. That is grave #32,
	// whose lesson RequireSuite's own if/else records, one caller up.
	if n, err := countSuiteFiles(suiteDir); err != nil || n < MinSuiteFiles {
		RequireSuite(tb, suiteDir)
		return nil
	}

	dir := ProposalDir(suiteDir, proposal)
	paths, err := ProposalPaths(suiteDir, proposal)
	if err != nil {
		tb.Fatalf("selecting %s's vectors in %s: %v", proposal, dir, err)
		return nil
	}
	if len(paths) != want {
		tb.Fatalf("%s holds %d .wast files, but this lane is pinned to %d.\n\t"+
			"The suite is vendored at its pinned count, so this is not a missing fetch: either "+
			"the pin moved the proposal's population and the lane's per-file counts below are "+
			"now about a corpus nobody measured, or a file arrived from somewhere else. A board "+
			"over it would name a corpus it did not measure.",
			dir, len(paths), want)
		return nil
	}
	sort.Strings(paths)
	return paths
}

// SuiteFiles returns the vendored .wast paths, or nil when the corpus is absent.
//
// For seeders rather than tests: a fuzz target with no corpus is weaker, not
// broken, so f.Add-ing literal seeds and continuing is the right local behaviour.
// Under BURROUGHS_NO_SKIP=1 that silent degradation is itself a precondition
// excusing a gate — the same shape as the skip, one step quieter, because nothing
// in the output says the corpus was missing unless someone reads the log line. So
// it fails instead.
func SuiteFiles(tb testing.TB, suiteDir string) []string {
	tb.Helper()

	paths, err := SuitePaths(suiteDir)
	if err == nil && len(paths) >= MinSuiteFiles {
		return paths
	}

	if NoSkip() {
		tb.Fatalf("spec suite not vendored: found %d .wast files in %s, want >=%d (run: make spec-tests)\n\t"+
			"%s=1 forbids seeding from literals alone: a corpus-less fuzz target still passes, which makes a missing corpus a silent downgrade rather than a failure",
			len(paths), suiteDir, MinSuiteFiles, NoSkipEnv)
	}
	return nil
}

// RefDecodeML is the reference interpreter's decoder, relative to the repo root.
// The *authority* of decision 0007, as distinct from the suite, which samples it.
const RefDecodeML = "third_party/spec/interpreter/binary/decode.ml"

// RefLexerMLL is the reference interpreter's text lexer, relative to the repo root.
// The *authority* of decision 0009, and the same corpus as RefDecodeML: one fetch, one
// pin, two grammars.
const RefLexerMLL = "third_party/spec/interpreter/text/lexer.mll"

// MinRefDecodeBytes is the floor for the vendored decoder to count as present.
//
// A size, not an existence check, for RequireSuite's reason one layer over: a
// truncated decode.ml satisfies os.Stat and then yields an extraction with too few
// arms — and the interesting half of that failure is that the extractor's own vacuity
// floors would catch it, reporting "upstream refactored" for what is really "the fetch
// was cut short". Two controls, two diagnoses; this one owns the input.
// decode.ml is 38042 bytes at bdd7164.
const MinRefDecodeBytes = 20000

// RefParserMLY is the reference interpreter's text *parser*, relative to the repo root.
// The authority for #62's stratum: where the lexer's rules say what a token is,
// parser.mly's `name` (:46) and `var` (:49) say which token *positions* decode UTF-8 —
// and that position, not the byte pattern, is what the verdict turns on.
const RefParserMLY = "third_party/spec/interpreter/text/parser.mly"

// MinRefLexerBytes is the same floor for lexer.mll, which is 36686 bytes at bdd7164.
//
// Its own constant rather than reusing MinRefDecodeBytes, even though 20000 happens to
// hold for both files at this revision. Sharing it would make the two floors' agreement
// an accident that a future upstream edit silently ends — a floor is a claim about *one
// file's* plausible size, and a single number covering two files is right about neither
// on purpose.
const MinRefLexerBytes = 20000

// MinRefParserBytes is the floor for parser.mly, which is 54523 bytes at bdd7164.
//
// Its own constant, per the argument above: three files, three claims about three
// plausible sizes. That the number happens to match the other two at this revision is
// exactly the accident the separate constants exist to keep from becoming load-bearing.
const MinRefParserBytes = 20000

// RefEncodeML is the reference interpreter's binary *encoder*, relative to the repo root.
//
// The authority for the text→binary bridge (0011): decode.ml says what an image means,
// and encode.ml says which image a module produces — a different question, and the one
// every emitter in internal/text is written against. It was cited 28 times there before
// it was licensed here, which is the citation-without-a-resolver shape one level up from
// prose: the *file* was trusted with no floor and no presence check, so a truncated fetch
// would have been read as an authority. Registered when the first control actually
// compared against it (TestExternKindByteAgreesForBothSections).
const RefEncodeML = "third_party/spec/interpreter/binary/encode.ml"

// MinRefEncodeBytes is the floor for encode.ml, which is 45362 bytes at bdd7164.
//
// Its own constant, per the argument above — four files, four claims. The shared 20000 is
// deliberately not a shared symbol.
const MinRefEncodeBytes = 20000

// RefFreeML is the reference interpreter's *free-variable* pass, relative to the repo root.
//
// The authority for a question neither decode.ml nor encode.ml answers alone: **which
// index spaces an instruction references.** `encode.ml` guards the data count section on
// `Free.((module_ m).datas <> Set.empty)` (:1109) and stops there — the set's membership is
// computed here, and it is fed by four instruction arms (:165, 166, 175, 181) while a data
// *segment* contributes nothing (:217). So "does this module need a data count section" is
// answerable only by reading this file, and both sides of this project's round trip depend
// on the answer: internal/binary requires the section for those opcodes and internal/text
// emits it for them.
//
// Registered when the first control compared against it
// (TestSectionTwelveConditionIsTheReferences). `dataRefOps` in internal/binary has cited
// four of its lines since #22 with nothing resolving them, which is the same
// citation-without-a-resolver shape RefEncodeML's comment records one authority earlier.
const RefFreeML = "third_party/spec/interpreter/syntax/free.ml"

// MinRefFreeBytes is the floor for free.ml, which is 7508 bytes at bdd7164.
//
// **Nowhere near the other four's 20000, and that is the argument for per-file constants
// arriving as a measurement rather than as a principle.** free.ml is a sixth the size of
// decode.ml; a shared floor would have been either vacuous for this file or a false failure
// for it, and the "happens to match at this revision" coincidence the constants above are
// separate to guard against has now actually ended.
const MinRefFreeBytes = 5000

// RefValidML is the reference interpreter's *validator*, relative to the repo root.
//
// The authority for #9's whole campaign: which modules are well-typed. decode.ml says what an
// image means and encode.ml says which image a module produces; neither says whether the module
// is *valid*, and validity is a separate judgement with its own algorithm — the operand stack,
// the control-frame stack, the polymorphic-after-`unreachable` rule, and the instruction
// signature table all live here.
//
// **Licensed before the first citation rather than after the twenty-eighth, deliberately.** Both
// this file and match.ml were on disk and in neither this map nor the fetch script's presence
// loop, which is the shape RefEncodeML's comment records against itself — cited 28 times in
// internal/text with no floor and no presence check, so a truncated fetch would have read as an
// authority. The reason it is more urgent here than it was there: encode.ml was a cited
// artifact, where valid.ml is this campaign's **oracle**. Its absence from this map means a fetch
// change could drop the authority and no control would notice, which is *coverage is a claim*
// with the oracle as the subject. (Directive: chat-Claude, on the #290 relay; #291 commit one.)
const RefValidML = "third_party/spec/interpreter/valid/valid.ml"

// MinRefValidBytes is the floor for valid.ml, which is 38707 bytes at bdd7164.
//
// The shared 20000 again, and again as a coincidence rather than a principle — see
// MinRefFreeBytes, whose 5000 is where the coincidence first ended.
const MinRefValidBytes = 20000

// RefMatchML is the reference interpreter's *subtyping* relation, relative to the repo root.
//
// valid.ml's companion and not a duplicate of it: valid.ml asks whether each instruction's
// operands are present, and defers *whether this type is acceptable where that one is wanted* to
// `Match` — so the two together answer one question and neither answers it alone. That split is
// exactly why the type-mismatch family cannot be discriminated by expected string (2288 of 2714
// vectors want the identical `type mismatch`): the rule that refused is recoverable only from
// these two files.
//
// Licensed in the same motion as valid.ml. A validator campaign that licensed its type rules and
// not its subtyping rules would have narrowed its own authority by exactly the mechanism
// TestEveryPinsFetchScriptAssertsItsAuthorities was written to police — *not a wrong assertion, a
// shrinking one*.
const RefMatchML = "third_party/spec/interpreter/valid/match.ml"

// MinRefMatchBytes is the floor for match.ml, which is 5426 bytes at bdd7164.
//
// Below the 20000 four of the six share, like free.ml's, and for the same reason: it is a small
// file, and a floor is a measurement of the file it bounds rather than a house style.
const MinRefMatchBytes = 4000

// RefMnemonicsML is the reference interpreter's mnemonic-to-constructor table, relative to the
// repo root.
//
// **It is the missing link in valid.ml's chain, and without it valid.ml is an oracle nothing can
// reach.** valid.ml types instructions by their *constructor family* — `VecBinary`, `VecSplat`,
// `VecLoadLane` — and decode.ml maps an opcode to a *mnemonic*. Neither file says which family a
// mnemonic belongs to; this one does, in 256 one-line bindings for the vector region alone
// (`let i8x16_swizzle = VecBinary (V128 (I8x16 V128Op.Swizzle))`). So an opcode's type is
// recoverable only by joining three authorities, and this is the join key's home.
//
// The alternative was hand-classifying 236 SIMD opcodes into families by their names, whose
// errors are **accept-direction and invisible on the board** — contract §9's G-3 class, the one
// `signature`'s doc comment argues at length against. Licensed on arrival rather than after the
// first citation, which is RefValidML's own lesson carried one authority forward. (#305, slice 2.)
const RefMnemonicsML = "third_party/spec/interpreter/syntax/mnemonics.ml"

// MinRefMnemonicsBytes is the floor for mnemonics.ml, which is 28444 bytes at bdd7164.
const MinRefMnemonicsBytes = 20000

// RefV128ML is the reference interpreter's vector shape arithmetic, relative to the repo root.
//
// Licensed for two six-row functions, and the smallness is the point: `num_lanes` (`:22`) and
// `type_of_lane` (`:31`) are what turn a shape into a lane count and a lane scalar type, which is
// the entire content of `VecSplat`/`VecExtract`/`VecReplace`'s signatures and of every lane-index
// bound. Six rows is exactly the size at which transcribing feels too small to check — and
// `type_of_lane`'s first row folds three shapes onto `I32T`, so the plausible-looking wrong
// version (`i8x16` lanes are `i8`-ish, so surely not `i32`) is the one a reader would write from
// memory. An accept-direction error in it is invisible for G-3's reason.
//
// Not licensed for the *execution* semantics in the rest of the file: `internal/interp` runs SIMD
// already and does not cite this. The authority claimed here is the shape-to-lane mapping and
// nothing else, stated so a later reader does not read this constant as licensing the file's
// arithmetic.
const RefV128ML = "third_party/spec/interpreter/exec/v128.ml"

// MinRefV128Bytes is the floor for v128.ml, which is 16679 bytes at bdd7164.
const MinRefV128Bytes = 10000

// The **threads proposal's** reference interpreter, pinned separately at cc535ad — the
// second authority of ADR 0007's 2026-08-28 amendment.
//
// # Why these are separate constants rather than a `Dest`-swapped reuse
//
// Because they are different files with different contents at a different revision, and
// the one thing a reader must never do is cite "decode.ml" without saying which. The core
// pin's decode.ml knows 0xfb and memory64's limits flags; this one knows 0xfe and `shared`
// and neither of the first two. A citation naming only the basename resolves to a file that
// exists and answers a different question — the *ambiguous positional citation* class
// (#497), arriving here structurally rather than by drift.
//
// # What this pin is licensed for, and what it is not
//
// **Licensed:** the threads clauses. Concretely, at this revision: `limits`' shared bit
// (decode.ml:181-188), `table_type`'s refusal of a shared table (:190-194), `memory_type`'s
// shared memtype (:196-198), `check_memorytype`'s shared-needs-a-maximum rule
// (valid.ml:601-605), and — when the 0xfe region lands — the atomic opcode table and its
// text mnemonics.
//
// **Not licensed:** anything else in those files. Two measured reasons, either sufficient:
// its decode.ml has no 0xfb region at all, and its `limits` requires `flags land 0xfc = 0`,
// which makes memory64's 0x04-0x07 malformed. Both are shipped proposals here. A wholesale
// read of this authority is not a stricter engine, it is two features deleted — so every
// citation to this pin names the clause, and `RefPin.Why` says so at the pin.
const (
	// ThreadsRefDecodeML is the threads proposal's decoder: the limits flags' shared bit,
	// the shared-table refusal, and the 0xfe region.
	ThreadsRefDecodeML = "third_party/spec-threads/interpreter/binary/decode.ml"
	// ThreadsRefValidML is its validator: `check_memorytype`'s "shared memory must have
	// maximum". The rule the core pin's valid.ml cannot state, having no shared bit to
	// state it about.
	ThreadsRefValidML = "third_party/spec-threads/interpreter/valid/valid.ml"
	// ThreadsRefLexerMLL is its text lexer: the `shared` keyword and the `*.atomic.*`
	// mnemonic tokens. Licensed before its first citation rather than after it, which is
	// the lesson fetch-spec-ref.sh's own comment records paying for encode.ml twenty-eight
	// citations late.
	ThreadsRefLexerMLL = "third_party/spec-threads/interpreter/text/lexer.mll"
	// ThreadsRefParserMLY is its text parser: `memtype`'s shared arm, which is where
	// `internal/text/encode.go`'s standing note — *"the text grammar's `limits` has no
	// `shared` arm … so no wat source can denote a shared memory"* — stops being true.
	ThreadsRefParserMLY = "third_party/spec-threads/interpreter/text/parser.mly"
	// ThreadsRefEncodeML is its encoder: the wire form for a shared memtype and for the
	// atomic instructions, which is the bridge half 0011 established for the core pin.
	ThreadsRefEncodeML = "third_party/spec-threads/interpreter/binary/encode.ml"
)

// Floors for the threads pin, each stating the file's size at cc535ad beside it.
//
// Their own constants rather than reuse of the core pin's, even where a number would
// coincide, for the reason MinRefLexerBytes already gives one pin over: a floor is a fact
// about a file, and two files sharing a literal is a coincidence that reads as a
// relationship. These describe a *different revision of a different repository*, so sharing
// one would be the stronger version of the same error.
const (
	// MinThreadsRefDecodeBytes is the floor for the threads decode.ml, 34324 bytes at
	// cc535ad — and note it is *smaller* than the core pin's 38042, the baseline being
	// older. A floor copied from the core pin would therefore have rejected the correct
	// file.
	MinThreadsRefDecodeBytes = 20000
	// MinThreadsRefValidBytes is the floor for valid.ml, 23157 bytes at cc535ad.
	MinThreadsRefValidBytes = 14000
	// MinThreadsRefLexerBytes is the floor for lexer.mll, 37579 bytes at cc535ad.
	MinThreadsRefLexerBytes = 22000
	// MinThreadsRefParserBytes is the floor for parser.mly, 38622 bytes at cc535ad.
	MinThreadsRefParserBytes = 22000
	// MinThreadsRefEncodeBytes is the floor for encode.ml, 46030 bytes at cc535ad.
	MinThreadsRefEncodeBytes = 28000
)

// threadsRefFloors is the size floor per file of the threads pin.
//
// Five entries against the core pin's nine, and the gap is a fact about upstream rather
// than an omission here: `interpreter/valid/match.ml` and `interpreter/syntax/mnemonics.ml`
// **do not exist at cc535ad**, the proposal being forked from a core baseline older than
// either file. The remaining two the core pin licenses — free.ml and exec/v128.ml — exist
// but hold no threads clause (`grep -ic atomic` returns 0 in v128.ml at this revision), so
// licensing them would claim an authority this pin does not have.
var threadsRefFloors = map[string]int{
	ThreadsRefDecodeML:  MinThreadsRefDecodeBytes,
	ThreadsRefValidML:   MinThreadsRefValidBytes,
	ThreadsRefLexerMLL:  MinThreadsRefLexerBytes,
	ThreadsRefParserMLY: MinThreadsRefParserBytes,
	ThreadsRefEncodeML:  MinThreadsRefEncodeBytes,
}

// coreRefFloors is the size floor per file of the **core** pin, keyed by the path
// constants above.
//
// A map rather than a parameter on RequireSpecRef, deliberately: a floor passed at the
// call site is a fact about a file, typed somewhere other than where the file is named,
// and the failure mode is a caller passing 0 and defeating the control it is calling. Same
// argument as reading the SHA from the pin script instead of taking it as a flag — *a
// number typed at a second site is a claim that can drift from the thing it describes*.
// An unknown path is a hard failure below, never a default floor, because a default is
// how a third reference file would arrive with no floor at all.
//
// It was named `refFloors` while there was one pin. Renamed when the pin set went plural
// (ADR 0007's 2026-08-28 amendment) rather than left to mean "the floors" — a name that
// says *the* when there are two is how the second one comes to be forgotten, which is the
// narrowing TestEveryPinsFetchScriptAssertsItsAuthorities' own doc comment describes
// happening to that control.
var coreRefFloors = map[string]int{
	RefDecodeML:  MinRefDecodeBytes,
	RefLexerMLL:  MinRefLexerBytes,
	RefParserMLY: MinRefParserBytes,
	RefEncodeML:  MinRefEncodeBytes,
	RefFreeML:    MinRefFreeBytes,
	RefValidML:   MinRefValidBytes,
	RefMatchML:   MinRefMatchBytes,

	RefMnemonicsML: MinRefMnemonicsBytes,
	RefV128ML:      MinRefV128Bytes,
}

// RefPin is one vendored reference authority: the script that pins it, the directory it
// lands in, and the files this package licenses *from that pin*.
//
// The type exists because the pin set is plural as of ADR 0007's 2026-08-28 amendment, and
// because the three fields vary together. The alternative — a second flat map beside
// `coreRefFloors` plus a second script constant plus a second `Licensed…Paths` — is three
// duplications of one fact, and the control over them would have to enumerate the pins it
// knew about. Enumeration is what left the *first* fetch-script control checking a third of
// its subject for two authorities' worth of time.
type RefPin struct {
	// Script is the fetch script holding this pin's `rev=`, relative to the repo root.
	// One `rev=` per file, never two in one file: the pins are independently dated so
	// that drift in one cannot be silently absorbed by the other (Scott, on the v1
	// scoping report).
	Script string
	// Dest is where the pin's checkout lands, with a trailing slash, and it is the prefix
	// every one of Paths' entries begins with. Carried as a field rather than derived by
	// trimming, because the fetch scripts' presence loops are written *relative* to it and
	// the control has to reproduce that trim exactly.
	Dest string
	// Floors is the size floor per licensed file, keyed by full repo-root-relative path.
	Floors map[string]int
	// Why names what this pin is the authority *for*, in one clause. Present because the
	// threads pin is not a mirror of the core one and a reader finding two decode.ml
	// entries needs to know which clauses each answers — see ThreadsRefDecodeML.
	Why string
	// Target is the `make` target that runs Script, and it exists because RequireSpecRef
	// prints it to a human as an instruction. It was the literal `make spec-ref` in two
	// skip reasons, which is a *correct* remedy for nine files and a wrong one for five:
	// a threads authority missing would have told the reader to re-run the fetch that
	// cannot produce it. An error message is testimony, and this file's own truncation
	// comment records paying for the weaker version of the same error.
	//
	// Carried rather than derived from Script by stripping `scripts/fetch-` and `.sh`.
	// That derivation would work today and produce a plausible target name forever after
	// — including for a pin whose Makefile entry is named something else or absent, where
	// the instruction resolves to nothing. TestEveryPinsFetchScriptAssertsItsAuthorities
	// checks each one against the Makefile instead.
	Target string
}

// refPins is every reference authority, and the *derived* domain every control over them
// walks. A pin added here is covered on arrival; a pin added anywhere else is not covered
// at all, which is the shape this list exists to make impossible.
var refPins = []RefPin{{
	Script: "scripts/fetch-spec-ref.sh",
	Dest:   "third_party/spec/",
	Target: "spec-ref",
	Floors: coreRefFloors,
	Why:    "the core spec at bdd7164: every clause of the MVP and of the nine shipped proposals",
}, {
	Script: "scripts/fetch-threads-ref.sh",
	Dest:   "third_party/spec-threads/",
	Target: "threads-ref",
	Floors: threadsRefFloors,
	Why: "the threads proposal at cc535ad, and *only* its threads clauses — its baseline " +
		"predates GC and memory64, so a wholesale read of its decode.ml would delete both",
}}

// RefPins returns every licensed authority pin.
//
// Exported so a control can derive the pin set rather than restate it, which is the same
// argument LicensedRefPaths already made one level down: a test that listed the pins itself
// would be a third place knowing the fact, and the two-places problem is what is being
// solved. Returned as a fresh slice so a caller cannot reorder the package's own list.
func RefPins() []RefPin {
	out := make([]RefPin, len(refPins))
	copy(out, refPins)
	return out
}

// refFloors is every licensed file across every pin, which is what RequireSpecRef looks a
// caller's path up in.
//
// Derived from refPins rather than written out, and the duplicate check is not decoration:
// both pins license `interpreter/binary/decode.ml`, so the *only* thing keeping their
// entries apart is the `Dest` prefix. A pin declared with the wrong prefix — or with none
// — would silently overwrite the other's floor and point every citation at one file.
var refFloors = func() map[string]int {
	all := make(map[string]int)
	for _, pin := range refPins {
		for p, f := range pin.Floors {
			if !strings.HasPrefix(p, pin.Dest) {
				panic("testenv: licensed path " + p + " is not under pin dest " + pin.Dest +
					" — the fetch script's presence loop is written relative to Dest and " +
					"cannot check it")
			}
			if _, dup := all[p]; dup {
				panic("testenv: two pins license " + p + " — the paths carry their pin's " +
					"Dest precisely so the two decode.ml authorities cannot collide")
			}
			all[p] = f
		}
	}
	return all
}()

// LicensedRefPaths returns every reference file this package licenses as an authority,
// across every pin.
//
// Exported so the drift check between the floors and the fetch scripts can *derive* the
// set rather than restate it. A test that listed the paths itself would be a third place
// knowing the fact, and the two-places problem is the thing being solved.
//
// **Across every pin, which is a widening a caller can be wrong about.** It answered "the
// files under third_party/spec/" while there was one pin, and the two readings agreed for
// as long as that was true. A caller wanting one pin's files reads `RefPins()` and takes
// the `Floors` it wants; this returns the union, which is the right domain for a control
// asking "is every authority checked somewhere" and the wrong one for a control asking
// "does *this* script check its own".
func LicensedRefPaths() []string {
	paths := make([]string, 0, len(refFloors))
	for p := range refFloors {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// RequireSpecRef is the licensed door for tests that read the reference interpreter.
//
// Same policy as RequireSuite and the same reason it exists: a drift check's whole job is
// to compare a committed table against the authority, so a drift check that skips because
// the authority is absent reports agreement with a file it never read. That is worse than
// no check, being a green that has never once looked (#29).
//
// One door for every reference file rather than one per pin, because the size floor is what
// differs per file and it is looked up rather than passed. **The remedy is not shared,
// though, and that is the part the plural pin set changed**: this printed the literal `make
// spec-ref` in both skip reasons, which is the right instruction for the core pin's nine
// files and the wrong one for the threads pin's five — a reader whose threads authority was
// missing would have been sent to re-run the fetch that cannot produce it. So the pin owning
// the path is resolved first and its own `Target` is quoted. *An error message is testimony*,
// and the truncation comment below records paying for the weaker version of this same error.
//
// Returns the source so a caller cannot obtain the path without passing the gate.
func RequireSpecRef(tb testing.TB, path string) string {
	tb.Helper()

	// The path is resolved from the repo root by the constants, but callers reach it
	// through filepath.Join with `..` prefixes, so match on the suffix. An unrecognized
	// path fails rather than skipping: a door that licenses a path it does not know is a
	// door with no floor, which is this function's entire subject.
	//
	// **Suffix-matched against the full `Dest`-prefixed path, which is what keeps the two
	// decode.ml authorities apart.** `third_party/spec-threads/…/decode.ml` does not end
	// with `third_party/spec/…/decode.ml`, so the match is unambiguous — but only because
	// both constants carry their prefix. `refFloors`' construction panics on a licensed path
	// that does not, rather than leaving this loop to pick whichever entry it met first.
	floor, known, canon, target := 0, false, "", ""
	for _, pin := range refPins {
		for p, f := range pin.Floors {
			if strings.HasSuffix(filepath.ToSlash(path), p) {
				floor, known, canon, target = f, true, p, pin.Target
				break
			}
		}
		if known {
			break
		}
	}
	if !known {
		tb.Fatalf("RequireSpecRef: no size floor registered for %q — add it to the Floors map "+
			"of its pin in refPins, beside its path constant; a reference file with no floor "+
			"is a presence check that cannot tell a truncated fetch from a complete one", path)
		return ""
	}

	// **The caller's `..` prefix selects the floor; it does not locate the file.** The path
	// is re-resolved from the repo root, and the reason is a defect this door had rather
	// than a tidiness: every caller spelled its own distance to the root as a literal
	// (`"..", "..", "..", ".."`), which is a claim about where the package sits, and moving
	// a package falsifies it. Promoting the generators to `internal/gen/` for 0014's join
	// did exactly that — and the wrong path did not fail, it made a *vendored* reference
	// look absent, so this function licensed a skip and every generator drift check passed
	// by asking nothing. Only `BURROUGHS_NO_SKIP=1` turned it into the failure that found
	// it: *a skip is not a verdict*, catching, one level up from the corpus it was written
	// about, the case where the question could have been asked and the path said otherwise.
	//
	// Deriving the root removes the class instead of correcting five literals, which would
	// leave the trap armed for the next move (*derive the domain, never enumerate it*).
	resolved, err := gen.FromRoot(canon)
	if err != nil {
		tb.Fatalf("RequireSpecRef: %v", err)
		return ""
	}

	b, err := os.ReadFile(resolved)
	path = resolved
	if len(b) >= floor {
		return string(b)
	}

	// **Truncated and absent are different conditions, and this said "not vendored" for both.**
	// A file present at 900 bytes under a 20000 floor is vendored; the fetch is *incomplete*,
	// which has a different cause (an interrupted or partial checkout) and a different remedy
	// than a missing vendor. The old wording named a state the input did not have — right
	// verdict, wrong ground, and no board could contradict it because the verdict was correct
	// either way. That is `ErrMalformedFuncType`'s `0xde` in a skip reason (*an error message is
	// testimony*). Found by truncating valid.ml to falsify the floor sweep and reading the
	// message it produced, which is the same print-don't-trust move the law asks for.
	reason := fmt.Sprintf("reference interpreter is vendored but truncated: %s is %d bytes, want >=%d "+
		"(an incomplete fetch, not a missing one — re-run: make %s)", path, len(b), floor, target)
	if err != nil {
		reason = fmt.Sprintf("reference interpreter not vendored: %v (run: make %s)", err, target)
	}

	if NoSkip() {
		tb.Fatalf("%s\n\t%s=1 revokes skip licenses: a drift check that skips reports agreement with an authority it never read",
			reason, NoSkipEnv)
	} else {
		tb.Skip(reason)
	}
	return ""
}

// MinSuiteFileBytes is the floor for a single vendored .wast file to count as present.
//
// A size rather than existence, for the same reason every other floor here is one, and set
// low because the files this door serves are *individual vectors* rather than a corpus:
// obsolete-keywords.wast is 1147 bytes at the vendored revision, and the suite holds
// smaller files still. 200 bytes is below any real vector and above a truncated fetch or an
// empty placeholder.
const MinSuiteFileBytes = 200

// RequireSuiteFile is the licensed door for a test that reads *one* named suite file rather
// than the whole corpus.
//
// A fourth door rather than a call to RequireSuite, because the two ask different questions
// and a reader deserves the one that was actually asked. RequireSuite asserts a *count* —
// "is the corpus here" — which is right for a board over 257 files and wrong for a citation
// check against one vector: a full corpus with the cited file missing passes RequireSuite
// and then fails inside the test with a bare read error, and a corpus of 249 files fails
// RequireSuite for a reason that has nothing to do with the file the caller wanted.
//
// It exists because writing this test's skip inline failed TestEverySkipSiteIsLicensed —
// the second time that AST check has caught an unlicensed skip written by an author who
// knows the rule, which is the case for reading the AST instead of trusting the convention
// (the first was RequireProposalDoc's own arrival). *A precondition that excuses a gate is
// licensed at one place, or it is a hole.*
//
// The parameter is the file's name *within* the vendored suite ("annotations.wast"), not a
// path — deliberately, so a caller cannot express its distance to the repo root and
// therefore cannot get it wrong. See RequireSpecRef for the defect that motivates this: a
// caller-supplied `..` depth is a claim about where a package lives, it is falsified by
// moving the package, and it fails as a *skip* rather than as an error.
//
// Returns the contents so a caller cannot read the file without passing the gate.
func RequireSuiteFile(tb testing.TB, name string) []byte {
	tb.Helper()

	path, err := gen.FromRoot(SuiteDir, name)
	if err != nil {
		tb.Fatalf("RequireSuiteFile: %v", err)
		return nil
	}

	b, err := os.ReadFile(path)
	if len(b) >= MinSuiteFileBytes {
		return b
	}

	reason := fmt.Sprintf("spec suite not vendored: %s is %d bytes, want >=%d (run: make spec-tests)",
		path, len(b), MinSuiteFileBytes)
	if err != nil {
		reason = fmt.Sprintf("spec suite not vendored: %v (run: make spec-tests)", err)
	}

	if NoSkip() {
		tb.Fatalf("%s\n\t%s=1 revokes skip licenses: a citation check that skips is a citation nobody verified",
			reason, NoSkipEnv)
	} else {
		tb.Skip(reason)
	}
	return nil
}

// ProposalDoc is a proposal overview under the vendored reference tree, relative to the
// repo root. The *citation targets* of the gate mapping (decision 0008): each mapped
// construct names a line in one of these, and a machine checks that the line resolves.
func ProposalDoc(rel string) string {
	return filepath.Join("third_party", "spec", rel)
}

// MinProposalDocBytes is the floor for a proposal document to count as present.
//
// A size rather than existence, for RequireSpecRef's reason: a truncated document
// satisfies os.Stat and then makes every citation into it unresolvable, which the
// citation check would report as "the document moved under the citation" — the right
// alarm with the wrong diagnosis. The smallest document the mapping cites is
// tail-call/Overview.md at ~6KB; 1000 bytes leaves room for upstream editing without
// leaving room for a broken fetch.
const MinProposalDocBytes = 1000

// RequireProposalDoc is the licensed door for tests that read a vendored proposal
// document.
//
// Same policy and same reason as RequireSpecRef, one input over: a citation check that
// skips when the cited documents are absent reports agreement with files it never read,
// and *fixtures cite the suite, and the citations are checked* is worth nothing if the
// checking silently stops happening. Returns the contents so a caller cannot read the
// file without passing the gate.
func RequireProposalDoc(tb testing.TB, path string) string {
	tb.Helper()

	b, err := os.ReadFile(path)
	if len(b) >= MinProposalDocBytes {
		return string(b)
	}

	reason := fmt.Sprintf("proposal documents not vendored: %s is %d bytes, want >=%d (run: make spec-ref)",
		path, len(b), MinProposalDocBytes)
	if err != nil {
		reason = fmt.Sprintf("proposal documents not vendored: %v (run: make spec-ref)", err)
	}

	if NoSkip() {
		tb.Fatalf("%s\n\t%s=1 revokes skip licenses: a citation check that skips is a citation nobody verified",
			reason, NoSkipEnv)
	} else {
		tb.Skip(reason)
	}
	return ""
}

func countSuiteFiles(suiteDir string) (int, error) {
	paths, err := SuitePaths(suiteDir)
	if err != nil {
		return 0, err
	}
	return len(paths), nil
}
