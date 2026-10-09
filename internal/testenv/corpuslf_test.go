// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir and fails the test with the command and its output on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		// A synthetic repo needs an identity to commit, and the ambient one must not be
		// relied on: a CI runner may have none, and borrowing the developer's would make the
		// test's behaviour depend on their config.
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		// And no global config may reach it, or the very setting under test could be
		// inherited from the machine running the test.
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// TestCorpusLFRepairsACRLFWorktreeThatGitCallsClean witnesses `scripts/corpus-lf.sh`.
//
// # The scenario, and why it is the only one worth testing
//
// The corpora are separate git repositories that inherit the *machine's* `core.autocrlf`, which Git
// for Windows sets to `true`. The fetch scripts' update path is a **no-op when the revision already
// matches** — nothing is re-materialised — so a corpus checked out before the LF config existed
// keeps its CRLF bytes indefinitely. Setting the config fixes the *next* checkout and does nothing
// for that tree, which is why the chair's ruling required a repair rather than an assertion: an
// assertion that only failed would leave every existing Windows checkout broken until a human
// deleted the directory.
//
// The hard part is that such a tree is **clean by git's own reckoning**. The file was written as
// CRLF by a checkout under `autocrlf=true`, so the index's cached stat information agrees with it
// and `git status` reports nothing. A repair that relies on git noticing a modification has nothing
// to notice. So this test builds exactly that state — commits LF content, switches the repo to
// `autocrlf=true`, re-materialises the file as CRLF, and asserts `git status` is empty *before*
// running the repair. Without that precondition check the test would pass against a far easier
// scenario and claim the harder one.
//
// # Why a synthetic repository rather than the real corpus
//
// Seeding CRLF into `third_party/spec` would be visible to every other package: `go test ./...`
// runs packages in parallel, and `internal/text`, `internal/validate` and `internal/gen` all read
// those files byte-exactly. A test that corrupts shared state to prove it can repair it will fail
// its neighbours while it works. A synthetic repo also needs no vendored corpus, so this witness
// carries **no skip license** — it runs on a bare clone, which is where `make strict` revokes the
// licenses the corpus-dependent tests hold.
//
// The cost is that this covers the mechanism and not the wiring; the wiring is
// `TestEveryFetchScriptSetsTheLFConfigBeforeAnyCheckout` below.
func TestCorpusLFRepairsACRLFWorktreeThatGitCallsClean(t *testing.T) {
	script, err := filepath.Abs("../../scripts/corpus-lf.sh")
	if err != nil {
		t.Fatalf("resolving the script: %v", err)
	}
	if _, serr := os.Stat(script); serr != nil {
		t.Fatalf("the script under test is missing: %v", serr)
	}

	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "--local", "core.autocrlf", "false")
	gitIn(t, dir, "config", "--local", "core.eol", "lf")

	// Two files, because the repair resets the whole tree and a single-file test could not tell a
	// working repair from one that happens to rewrite the file it was told about.
	const body = "line one\nline two\nline three\n"
	for _, name := range []string{"lexer.mll", "parser.mly"} {
		if werr := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); werr != nil {
			t.Fatalf("writing %s: %v", name, werr)
		}
	}
	gitIn(t, dir, "add", "lexer.mll", "parser.mly")
	gitIn(t, dir, "commit", "-q", "-m", "pinned content")
	rev := strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))

	// Now the Windows shape: the repo's own config says CRLF, and the files are re-materialised
	// under it. This is what a corpus fetched on a stock Git-for-Windows install looks like.
	gitIn(t, dir, "config", "--local", "core.autocrlf", "true")
	gitIn(t, dir, "config", "--local", "core.eol", "crlf")
	for _, name := range []string{"lexer.mll", "parser.mly"} {
		if rerr := os.Remove(filepath.Join(dir, name)); rerr != nil {
			t.Fatalf("removing %s: %v", name, rerr)
		}
	}
	gitIn(t, dir, "checkout", "-f", "--", "lexer.mll", "parser.mly")

	// Precondition 1: the bytes really are CRLF. Asserted rather than assumed, because every
	// claim below is about a state this setup has to have actually produced.
	got, err := os.ReadFile(filepath.Join(dir, "lexer.mll"))
	if err != nil {
		t.Fatalf("reading the seeded file: %v", err)
	}
	if !bytes.Contains(got, []byte("\r\n")) {
		t.Skipf("this git (%s) did not produce a CRLF worktree under core.autocrlf=true, so the "+
			"scenario under test does not exist here and a pass would mean nothing",
			strings.TrimSpace(gitIn(t, dir, "--version")))
	}

	// Precondition 2 — the one that makes this the HARD case. If git reported these as modified,
	// any reset would repair them and the test would be witnessing a much weaker claim.
	if st := gitIn(t, dir, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Fatalf("the seeded worktree is not stat-clean, so this is not the scenario under test:\n%s"+
			"\nA CRLF tree git already calls modified is repaired by any reset; the defect is the "+
			"tree git calls CLEAN.", st)
	}

	// The repair, through the real script, with the config mode first exactly as the fetch
	// scripts call it.
	run := func(args ...string) (string, error) {
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		// The script takes paths relative to <dest>, and <dest> relative to the caller's cwd.
		cmd.Dir = dir
		out, rerr := cmd.CombinedOutput()
		return string(out), rerr
	}
	if out, rerr := run("config", "."); rerr != nil {
		t.Fatalf("corpus-lf config: %v\n%s", rerr, out)
	}

	cmd := exec.Command("sh", script, "verify", ".", rev)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("lexer.mll\nparser.mly\n")
	out, verr := cmd.CombinedOutput()
	if verr != nil {
		t.Fatalf("corpus-lf verify refused a tree it should have repaired: %v\n%s", verr, out)
	}
	if !strings.Contains(string(out), "repaired") {
		t.Errorf("the script reported success without saying it repaired anything, so it may have "+
			"found the tree already clean — which would mean the seeding above silently failed and "+
			"this test is vacuous.\nOutput:\n%s", out)
	}

	// The readback: both files back to the committed bytes, byte for byte.
	for _, name := range []string{"lexer.mll", "parser.mly"} {
		after, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			t.Fatalf("reading %s after the repair: %v", name, rerr)
		}
		if string(after) != body {
			t.Errorf("%s was not restored to the committed bytes.\n got %q\nwant %q", name, after, body)
		}
	}

	// And the config the repair ran under is left in place, so the *next* checkout is LF too.
	// A repair that fixed the bytes and left `autocrlf=true` would be undone by the next fetch.
	for _, kv := range [][2]string{{"core.autocrlf", "false"}, {"core.eol", "lf"}} {
		if v := strings.TrimSpace(gitIn(t, dir, "config", "--local", "--get", kv[0])); v != kv[1] {
			t.Errorf("after the repair %s is %q, want %q — the bytes would be fixed and the next "+
				"checkout would re-break them", kv[0], v, kv[1])
		}
	}
}

// TestCorpusLFRefusesAnEmptyOrAbsentPopulation is the vacuity arm.
//
// `verify`'s whole output is a verdict about a set of files, so the two ways it can agree with
// anything are an empty list and a list naming nothing that exists. Both are what a broken caller
// produces — `suite-count.sh --list` through a pipe whose left side failed yields the first, and a
// stale path list yields the second — and the script is the only place either can be seen, because
// a pipeline reports its last command's status.
func TestCorpusLFRefusesAnEmptyOrAbsentPopulation(t *testing.T) {
	script, err := filepath.Abs("../../scripts/corpus-lf.sh")
	if err != nil {
		t.Fatalf("resolving the script: %v", err)
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")

	for _, c := range []struct {
		name  string
		stdin string
		want  string
	}{
		{
			name:  "an empty path list is refused by name",
			stdin: "",
			want:  "EMPTY path list",
		},
		{
			name:  "a list naming no existing file is refused by name",
			stdin: "no/such/file.ml\nalso/missing.mll\n",
			want:  "none of the",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command("sh", script, "verify", ".", "deadbeef")
			cmd.Dir = dir
			cmd.Stdin = strings.NewReader(c.stdin)
			out, rerr := cmd.CombinedOutput()
			if rerr == nil {
				t.Fatalf("the script reported LF-clean over a population it cannot have checked.\n%s", out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Errorf("the refusal does not name %q, so it may be refusing for an unrelated "+
					"reason.\nOutput:\n%s", c.want, out)
			}
		})
	}
}

// TestEveryFetchScriptSetsTheLFConfigBeforeAnyCheckout covers the wiring the witness above cannot.
//
// The mechanism being correct is worth nothing if a fetch script checks out first and configures
// afterwards, and the ordering is the whole point: `core.autocrlf` is consulted **at checkout
// time**. It is also the half that a future script copied from these — which is how all three came
// to exist — would be most likely to get wrong, because the config call looks like setup that can
// go anywhere.
//
// Derived over `RefPins()` plus the suite script, rather than a written list, for the reason the
// neighbouring control records: an enumerated population is how a control comes to check a third of
// its subject and say nothing about the rest.
func TestEveryFetchScriptSetsTheLFConfigBeforeAnyCheckout(t *testing.T) {
	scripts := map[string]bool{"scripts/fetch-spec-tests.sh": true}
	for _, pin := range RefPins() {
		scripts[pin.Script] = true
	}
	// Vacuity floor: a drained `refPins` would leave only the suite script and the loop would
	// report green over one of three.
	if len(scripts) < 3 {
		t.Fatalf("found %d fetch script(s), want >= 3 (the suite, the core ref, the threads ref) "+
			"— a sweep this small is not checking the population it names", len(scripts))
	}

	for name := range scripts {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("../..", name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			src := string(b)

			// Two calls, one per branch of the create/update split. One call cannot cover both:
			// the create path must configure after `git init`, and the update path has no init.
			if n := strings.Count(src, "corpus-lf.sh config"); n < 2 {
				t.Errorf("%s calls `corpus-lf.sh config` %d time(s), want >= 2 — once after "+
					"`git init` on the create path and once on the update path, which is a no-op "+
					"when the rev matches and so is exactly where a CRLF tree survives", name, n)
			}

			// Ordering: the first config call precedes the first checkout. `core.autocrlf` is read
			// at checkout time, so a config set afterwards governs nothing that already happened.
			cfg := strings.Index(src, "corpus-lf.sh config")
			ck := strings.Index(src, "checkout -q --detach")
			if ck < 0 {
				ck = strings.Index(src, "checkout --detach")
			}
			switch {
			case cfg < 0:
				t.Errorf("%s never calls `corpus-lf.sh config`, so its corpus inherits the "+
					"machine's core.autocrlf", name)
			case ck < 0:
				t.Errorf("%s has no `checkout --detach`, so this control cannot locate the "+
					"ordering it exists to check — the script's shape changed and the check "+
					"silently stopped applying", name)
			case cfg > ck:
				t.Errorf("%s calls `corpus-lf.sh config` at offset %d, AFTER its first checkout "+
					"at offset %d. core.autocrlf is consulted at checkout time, so the config "+
					"governs nothing that has already been written", name, cfg, ck)
			}

			// And the post-condition exists at all.
			if !strings.Contains(src, "corpus-lf.sh verify") {
				t.Errorf("%s never calls `corpus-lf.sh verify`, so nothing checks that the "+
					"checkout it just made is actually LF", name)
			}
		})
	}
}
