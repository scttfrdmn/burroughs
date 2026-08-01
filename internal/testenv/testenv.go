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
	"strings"
	"testing"
)

// NoSkipEnv is the environment variable that revokes every skip license. CI sets
// it in every job that runs tests, not only the conformance lane: the grave was a
// precondition nobody asserted, and confining the fix to the one job that
// prompted it would leave the class open.
const NoSkipEnv = "BURROUGHS_NO_SKIP"

// MinSuiteFiles is the floor for a vendored suite to count as present.
//
// A count rather than a directory existence check, because the failure this
// guards is a *partial* fetch as much as a missing one: an empty or truncated
// testdata/spec would satisfy os.Stat and then skip, or worse, report a board
// computed over three files as though it were the corpus. The upstream suite has
// 257 .wast files; 250 leaves room for upstream churn without leaving room for a
// broken fetch.
const MinSuiteFiles = 250

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

	paths, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
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

// MinRefLexerBytes is the same floor for lexer.mll, which is 36686 bytes at bdd7164.
//
// Its own constant rather than reusing MinRefDecodeBytes, even though 20000 happens to
// hold for both files at this revision. Sharing it would make the two floors' agreement
// an accident that a future upstream edit silently ends — a floor is a claim about *one
// file's* plausible size, and a single number covering two files is right about neither
// on purpose.
const MinRefLexerBytes = 20000

// refFloors is the size floor per reference file, keyed by the path constants above.
//
// A map rather than a parameter on RequireSpecRef, deliberately: a floor passed at the
// call site is a fact about a file, typed somewhere other than where the file is named,
// and the failure mode is a caller passing 0 and defeating the control it is calling. Same
// argument as reading the SHA from the pin script instead of taking it as a flag — *a
// number typed at a second site is a claim that can drift from the thing it describes*.
// An unknown path is a hard failure below, never a default floor, because a default is
// how a third reference file would arrive with no floor at all.
var refFloors = map[string]int{
	RefDecodeML: MinRefDecodeBytes,
	RefLexerMLL: MinRefLexerBytes,
}

// RequireSpecRef is the licensed door for tests that read the reference interpreter.
//
// Same policy as RequireSuite and the same reason it exists: a drift check's whole job is
// to compare a committed table against the authority, so a drift check that skips because
// the authority is absent reports agreement with a file it never read. That is worse than
// no check, being a green that has never once looked (#29).
//
// One door for both reference files rather than a fourth door beside it, because the
// inventory's unit is the *corpus*: decode.ml and lexer.mll arrive from one `make
// spec-ref`, at one pin, and a reader sent to that target is sent to the right place
// whichever file is missing. The size floor is what differs per file, and it is looked up
// rather than passed.
//
// Returns the source so a caller cannot obtain the path without passing the gate.
func RequireSpecRef(tb testing.TB, path string) string {
	tb.Helper()

	// The path is resolved from the repo root by the constants, but callers reach it
	// through filepath.Join with `..` prefixes, so match on the suffix. An unrecognized
	// path fails rather than skipping: a door that licenses a path it does not know is a
	// door with no floor, which is this function's entire subject.
	floor, known := 0, false
	for p, f := range refFloors {
		if strings.HasSuffix(filepath.ToSlash(path), p) {
			floor, known = f, true
			break
		}
	}
	if !known {
		tb.Fatalf("RequireSpecRef: no size floor registered for %q — add it to refFloors "+
			"beside its path constant; a reference file with no floor is a presence check "+
			"that cannot tell a truncated fetch from a complete one", path)
		return ""
	}

	b, err := os.ReadFile(path)
	if len(b) >= floor {
		return string(b)
	}

	reason := fmt.Sprintf("reference interpreter not vendored: %s is %d bytes, want >=%d (run: make spec-ref)",
		path, len(b), floor)
	if err != nil {
		reason = fmt.Sprintf("reference interpreter not vendored: %v (run: make spec-ref)", err)
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
// Returns the contents so a caller cannot read the file without passing the gate.
func RequireSuiteFile(tb testing.TB, path string) []byte {
	tb.Helper()

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
	paths, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
	if err != nil {
		return 0, err
	}
	return len(paths), nil
}
