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

// MinRefDecodeBytes is the floor for the vendored reference to count as present.
//
// A size, not an existence check, for RequireSuite's reason one layer over: a
// truncated decode.ml satisfies os.Stat and then yields an extraction with too few
// arms — and the interesting half of that failure is that the extractor's own vacuity
// floors would catch it, reporting "upstream refactored" for what is really "the fetch
// was cut short". Two controls, two diagnoses; this one owns the input.
// decode.ml is 38042 bytes at bdd7164.
const MinRefDecodeBytes = 20000

// RequireSpecRef is the licensed door for tests that read the reference interpreter.
//
// Same policy as RequireSuite and the same reason it exists: the drift check's whole
// job is to compare the committed table against the authority, so a drift check that
// skips because the authority is absent reports agreement with a file it never read.
// That is worse than no check, being a green that has never once looked (#29).
//
// Returns the source so a caller cannot obtain the path without passing the gate.
func RequireSpecRef(tb testing.TB, path string) string {
	tb.Helper()

	b, err := os.ReadFile(path)
	if len(b) >= MinRefDecodeBytes {
		return string(b)
	}

	reason := fmt.Sprintf("reference interpreter not vendored: %s is %d bytes, want >=%d (run: make spec-ref)",
		path, len(b), MinRefDecodeBytes)
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

func countSuiteFiles(suiteDir string) (int, error) {
	paths, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
	if err != nil {
		return 0, err
	}
	return len(paths), nil
}
