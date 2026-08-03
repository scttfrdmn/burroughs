package testenv_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// recorder stands in for *testing.T so the strictness policy can be tested from
// both sides. A control whose failure mode has never been observed is a control
// nobody has reason to trust — and this one's whole job is to fail, so its failing
// path is the path that matters.
type recorder struct {
	testing.TB
	skipped bool
	failed  bool
	msg     string
}

func (r *recorder) Helper() {}

func (r *recorder) Skip(args ...any) {
	r.skipped = true
	r.msg = sprint(args)
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = format
	_ = args
}

func sprint(args []any) string {
	var b strings.Builder
	for _, a := range args {
		if s, ok := a.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

// present builds a directory holding MinSuiteFiles .wast files, so the happy path
// is exercised against a real count rather than a mocked one.
func present(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for i := range testenv.MinSuiteFiles {
		p := filepath.Join(dir, "f"+itoa(i)+".wast")
		if err := os.WriteFile(p, []byte("(module)"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dir
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}

func TestRequireSuiteAbsent(t *testing.T) {
	empty := t.TempDir()

	t.Run("default license skips", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "")
		r := &recorder{TB: t}
		testenv.RequireSuite(r, empty)
		if !r.skipped || r.failed {
			t.Fatalf("absent corpus with license intact: skipped=%v failed=%v, want skipped", r.skipped, r.failed)
		}
		if !strings.Contains(r.msg, "make spec-tests") {
			t.Errorf("skip message must be actionable, got %q", r.msg)
		}
	})

	t.Run("no-skip revokes it", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "1")
		r := &recorder{TB: t}
		testenv.RequireSuite(r, empty)
		if !r.failed || r.skipped {
			t.Fatalf("absent corpus with %s=1: skipped=%v failed=%v, want failed", testenv.NoSkipEnv, r.skipped, r.failed)
		}
	})
}

// TestRequireSuitePresent pins the other direction. Without it the policy could be
// "always fail under the flag", which would pass the test above and break CI —
// a predicate is only checked when both of its answers are.
func TestRequireSuitePresent(t *testing.T) {
	dir := present(t)
	for _, v := range []string{"", "1"} {
		t.Setenv(testenv.NoSkipEnv, v)
		r := &recorder{TB: t}
		testenv.RequireSuite(r, dir)
		if r.skipped || r.failed {
			t.Fatalf("%s=%q with %d files present: skipped=%v failed=%v, want neither",
				testenv.NoSkipEnv, v, testenv.MinSuiteFiles, r.skipped, r.failed)
		}
	}
}

// TestPartialFetchIsNotPresent is why the check counts instead of calling os.Stat.
// A directory that exists and holds three files satisfies existence and then yields
// a board computed over three files — a number whose input was never asserted.
func TestPartialFetchIsNotPresent(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(dir, "f"+itoa(i)+".wast"), []byte("(module)"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	t.Setenv(testenv.NoSkipEnv, "1")
	r := &recorder{TB: t}
	testenv.RequireSuite(r, dir)
	if !r.failed {
		t.Fatalf("partial fetch (3 files) accepted as a vendored suite; want failure")
	}
}

func TestSuiteFiles(t *testing.T) {
	empty := t.TempDir()

	t.Run("absent degrades under license", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "")
		r := &recorder{TB: t}
		if got := testenv.SuiteFiles(r, empty); got != nil {
			t.Fatalf("want nil paths, got %d", len(got))
		}
		if r.failed {
			t.Fatal("seeding without a corpus is legitimate locally; must not fail")
		}
	})

	t.Run("absent fails under no-skip", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "1")
		r := &recorder{TB: t}
		testenv.SuiteFiles(r, empty)
		if !r.failed {
			t.Fatalf("%s=1 must convert a silent seed downgrade into a failure", testenv.NoSkipEnv)
		}
	})

	t.Run("present returns paths", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "1")
		r := &recorder{TB: t}
		got := testenv.SuiteFiles(r, present(t))
		if r.failed || len(got) != testenv.MinSuiteFiles {
			t.Fatalf("got %d paths, failed=%v; want %d and no failure", len(got), r.failed, testenv.MinSuiteFiles)
		}
	})
}

// TestNoSkipIsExactlyOne guards against the sloppiest possible way for this to
// break: an env var that is "set to anything" would treat BURROUGHS_NO_SKIP=0 as
// strict mode, and a CI YAML typo like `false` as strict mode too. Harmless in that
// direction — but the same laxity read the other way is what makes `=1` the only
// contract worth documenting.
func TestNoSkipIsExactlyOne(t *testing.T) {
	for _, tc := range []struct{ val, want string }{
		{"1", "on"}, {"", "off"}, {"0", "off"}, {"true", "off"},
	} {
		t.Setenv(testenv.NoSkipEnv, tc.val)
		got := "off"
		if testenv.NoSkip() {
			got = "on"
		}
		if got != tc.want {
			t.Errorf("%s=%q: NoSkip is %s, want %s", testenv.NoSkipEnv, tc.val, got, tc.want)
		}
	}
}

// TestFetchScriptAssertsEveryAuthority pins the fetch script's presence loop to the set of
// files testenv licenses, because they are two places that know one fact.
//
// The fact is "these are the reference files the project treats as authorities". `refFloors`
// holds it in Go; `fetch-spec-ref.sh` holds it in a shell `for` list, and cannot read the
// Go one. When decode.ml was the only authority the two agreed trivially; lexer.mll's
// arrival (0009) made the script's check cover a third of its subject with nothing saying
// so, and it stayed that way until parser.mly was the third. That is the narrowing an
// unpoliced duplicate produces — not a wrong assertion, a shrinking one.
//
// So the control is the drift check 0006 calls for, and its direction matters: the *script*
// must cover everything Go licenses. A file the script checks and Go does not is harmless
// (an extra assertion); a file Go licenses and the script does not is a fetch that reports
// success with an authority missing, which is precisely the early-return defect the script's
// own comment records.
func TestFetchScriptAssertsEveryAuthority(t *testing.T) {
	const script = "../../scripts/fetch-spec-ref.sh"
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	src := string(b)

	licensed := testenv.LicensedRefPaths()

	// Vacuity floor. An empty licensed set makes every containment check below pass by
	// asking nothing, which is the comparison-against-an-empty-set defect: mechanism
	// intact, asserting nothing, green. Three is the count at this revision and a floor
	// rather than an equality, so adding a fourth authority does not fail here — it
	// fails in the loop, which is the assertion that should catch it.
	if len(licensed) < 3 {
		t.Fatalf("LicensedRefPaths returned %d paths, want >=3 (decode.ml, lexer.mll, "+
			"parser.mly) — a containment check over an empty set agrees with anything",
			len(licensed))
	}

	for _, p := range licensed {
		// The script's paths are relative to `$dest`, testenv's include it.
		rel := strings.TrimPrefix(p, "third_party/spec/")
		if rel == p {
			t.Errorf("licensed path %q does not start with third_party/spec/ — the script's "+
				"loop is written relative to $dest and cannot check it", p)
			continue
		}
		if !strings.Contains(src, rel) {
			t.Errorf("%s does not assert the presence of %q, which testenv licenses as an "+
				"authority.\n\tA fetch that reports success with an authority missing is the "+
				"precondition excusing the check that polices it — add it to the loop.", script, rel)
		}
	}
}

// TestSuitePinIsAssertedByTheFetchScript pins the suite fetch script's two duplicated facts
// to the Go constants that hold them, because they are two places knowing one thing each.
//
// #42 pinned the suite by SHA, and pinning created two duplications the reference pin did not
// have:
//
//   - the **file-count floor**. `testenv.MinSuiteFiles` holds it in Go; the script holds it as
//     `min=250` and cannot read a Go constant. Its purpose there is the vacuity law — a
//     checkout that yields one .wast file passes any `> 0` test while making every board count
//     meaningless — so a floor that drifted *below* the Go one would let a corpus through that
//     Go then treats as absent, which is the skip-is-not-a-verdict hole re-opened at the fetch
//     layer.
//   - the **pin's shape**. `gen.PinnedRev` reads `^rev="<40 hex>"`, and the suite script was
//     written to that shape deliberately so one reader serves both pins. If the script renamed
//     the field, `PinnedRev` would stop finding it — and per *one concept, one trigger* (#82)
//     the failure would be silent in exactly the way a duplicated regexp is.
//
// Direction, as in TestFetchScriptAssertsEveryAuthority above: the *script's* floor must be at
// least Go's. A script floor higher than Go's is harmless (a stricter fetch); lower is a
// corpus the fetch blesses and the tests disown.
func TestSuitePinIsAssertedByTheFetchScript(t *testing.T) {
	const script = "../../scripts/fetch-spec-tests.sh"
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	src := string(b)

	// The pin itself, read by the *same* reader the generators use rather than by a second
	// regexp written here — the duplication this control exists to forbid would otherwise be
	// committed by the control forbidding it.
	rev, err := gen.PinnedRev(script)
	if err != nil {
		t.Fatalf("gen.PinnedRev(%s): %v\n\tThe suite pin is written as `rev=\"<40 hex>\"` so "+
			"one reader serves both pins; if the field was renamed, PinnedRev stops finding "+
			"it and every provenance stamp silently loses its subject.", script, err)
	}
	if len(rev) != 40 {
		t.Errorf("suite pin %q is %d chars, want 40", rev, len(rev))
	}

	// The floor. Matched as an assignment so a `min=` inside prose cannot satisfy it.
	m := regexp.MustCompile(`(?m)^min=([0-9]+)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s has no `min=<n>` floor assignment.\n\tWithout it the file-count check is "+
			"a presence check, and a one-file checkout passes it (the vacuity law).", script)
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("floor %q is not a number: %v", m[1], err)
	}
	if got < testenv.MinSuiteFiles {
		t.Errorf("%s floors the corpus at %d but testenv.MinSuiteFiles is %d.\n\tA fetch that "+
			"blesses a corpus the tests treat as absent is the skip-is-not-a-verdict hole at "+
			"the fetch layer: the fetch says success, RequireSuite then skips, and the board "+
			"passes by asking nothing.", script, got, testenv.MinSuiteFiles)
	}

	// The post-conditions must not sit behind the already-at-the-right-rev branch. This is
	// fetch-spec-ref.sh's grave (*an early return can skip its own guard*), and a copied
	// script inherits the bug it already paid for unless something says otherwise. Checked
	// structurally: the pin assertion has to appear *after* the if/else closes.
	ifEnd := strings.Index(src, "\nfi\n")
	pinCheck := strings.Index(src, `if [ "$got" != "$rev" ]`)
	switch {
	case ifEnd < 0:
		t.Errorf("%s has no `fi` closing its fetch branch — the structure this checks is gone", script)
	case pinCheck < 0:
		t.Errorf("%s never compares $got to $rev; the pin is a comment, not an assertion", script)
	case pinCheck < ifEnd:
		t.Errorf("%s asserts the pin *inside* its fetch branch (offset %d < %d).\n\tThe "+
			"already-at-the-right-rev path then skips its own post-conditions, so a correct "+
			"SHA with a deleted corpus reports success — fetch-spec-ref.sh's grave, inherited.",
			script, pinCheck, ifEnd)
	}
}
