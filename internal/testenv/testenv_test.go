package testenv_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
