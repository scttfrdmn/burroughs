// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWitnessRunRefusesARunThatDidNotDoTheWork witnesses `scripts/witnessrun.sh`.
//
// # Why this exists now, and why it did not before
//
// `witnessrun.sh` is the authority on whether the Phase 4 clause witnesses actually executed, and it had no
// witness of its own. That was tolerable while its verdict was mostly a wall-clock floor — a floor either
// fires or it does not, and its arithmetic is one comparison. It stopped being tolerable when the floor was
// demoted to a sanity bound (chair's ruling, 2026-10-09) and the **real** verdict became four assertions
// over `go test -json` events: a cached package, a witness that never ran, a witness that was skipped, a
// subtest that was skipped. Four greps over a format this script does not control is exactly the shape that
// passes by matching nothing.
//
// **The failure mode this guards is the script agreeing with everything.** Each check below is a `grep` for
// a pattern in a `-json` stream. If the stream is empty, or `go test`'s JSON schema changes, every
// `! grep -q` succeeds and every `grep -q` fails — which means *no objection*, which reads as a pass. A run
// that executed nothing and a run that went perfectly produce the same verdict. So the falsification here is
// not "does it catch a bad run" but "does it catch each bad run *by name*".
//
// # Why it drives the script rather than reimplementing its logic
//
// The alternative — a Go port of the greps, tested against the same fixtures — would be a second reading
// that agrees with the first by construction and drifts silently. This feeds crafted `-json` files to the
// real file on disk, through a stubbed `go`, so what is asserted is the behaviour that runs in CI.
func TestWitnessRunRefusesARunThatDidNotDoTheWork(t *testing.T) {
	script, err := filepath.Abs("../../scripts/witnessrun.sh")
	if err != nil {
		t.Fatalf("resolving the script: %v", err)
	}
	if _, serr := os.Stat(script); serr != nil {
		t.Fatalf("the script under test is missing: %v", serr)
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the repo root: %v", err)
	}

	// The three witnesses the script names. Taken from the script itself rather than written here, so a
	// rename in one place cannot leave this test asserting about a set that no longer exists.
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading the script: %v", err)
	}
	witnesses := scriptWitnessNames(string(src))
	if len(witnesses) < 3 {
		t.Fatalf("parsed %d witness name(s) from the script (%v); the `witnesses=` block is not being "+
			"found, so every fixture below would be built from an empty set and would agree with anything",
			len(witnesses), witnesses)
	}

	// A `-json` line per (action, test). The real stream carries far more, and that asymmetry is the point:
	// the script's greps must find what they look for in a realistic-shaped stream, not in a minimal one
	// built to satisfy them.
	ev := func(action, test string) string {
		return `{"Time":"2026-10-09T00:00:00Z","Action":"` + action +
			`","Package":"github.com/scttfrdmn/burroughs/internal/wasi","Test":"` + test + `"}`
	}
	// A full, passing stream: every witness runs and passes, plus the subtests and output lines a real run
	// emits. This is the control — if it does not pass, every refusal below could be refusing for an
	// unrelated reason.
	good := func() []string {
		var out []string
		for _, w := range witnesses {
			out = append(out, ev("run", w), ev("pass", w))
			out = append(out, ev("run", w+"/arm"), ev("pass", w+"/arm"))
		}
		out = append(out, `{"Action":"output","Package":"github.com/scttfrdmn/burroughs/internal/wasi",`+
			`"Output":"ok  \tgithub.com/scttfrdmn/burroughs/internal/wasi\t26.0s\n"}`)
		return out
	}

	for _, c := range []struct {
		name string
		// stream returns the fake `go test -json` output.
		stream func() []string
		// bound is the sanity-bound argument. **0 for every case whose subject is the event stream**,
		// which is all but one: a bound of 0 is no bound, so the stub answers instantly and no content
		// assertion depends on the clock. The one case that IS about time passes a positive bound against
		// the same instant stub, which is the whole of what it tests.
		//
		// This separation is not just speed. With a sleep per case, every content assertion was also
		// implicitly asserting that the stub outran the bound -- so a timing change could have turned a
		// content failure into a bound failure, or masked one. Six cases at 3s also put 16s on every
		// `make check` for no extra assertion.
		bound string
		// wantRefused is false only for the control.
		wantRefused bool
		// wantNamed is a substring the refusal must contain, so a test cannot pass on a refusal for
		// the wrong reason — the defect this whole file is about.
		wantNamed string
	}{
		{
			name:        "a full passing run is accepted",
			stream:      good,
			bound:       "0",
			wantRefused: false,
		},
		{
			name: "a CACHED package result is refused by name",
			stream: func() []string {
				return append(good(), `{"Action":"output","Package":"github.com/scttfrdmn/burroughs/`+
					`internal/wasi","Output":"ok  \tgithub.com/scttfrdmn/burroughs/internal/wasi\t(cached)\n"}`)
			},
			bound:       "0",
			wantRefused: true,
			wantNamed:   "CACHED",
		},
		{
			name: "a RENAMED witness — one that never emits a run event — is refused by name",
			stream: func() []string {
				var out []string
				// Everything except the first witness, which stands for one renamed, deleted, or no
				// longer matched by the script's own -run pattern.
				for _, w := range witnesses[1:] {
					out = append(out, ev("run", w), ev("pass", w))
				}
				return out
			},
			bound:       "0",
			wantRefused: true,
			wantNamed:   "never emitted a `run` event",
		},
		{
			name: "a SKIPPED witness is refused by name",
			stream: func() []string {
				var out []string
				out = append(out, ev("run", witnesses[0]), ev("skip", witnesses[0]))
				for _, w := range witnesses[1:] {
					out = append(out, ev("run", w), ev("pass", w))
				}
				return out
			},
			bound:       "0",
			wantRefused: true,
			wantNamed:   "SKIPPED",
		},
		{
			name: "a skipped ARM of a witness is refused by name",
			stream: func() []string {
				return append(good(), ev("skip", witnesses[1]+"/mechanism_absent"))
			},
			bound:       "0",
			wantRefused: true,
			wantNamed:   "subtest of a witness was skipped",
		},
		{
			// **The only case about time**, and it needs no sleep either: an instant stub against a
			// positive bound is exactly "finished faster than the bound", which is the whole claim.
			name:        "a run under the SANITY BOUND is refused by name",
			stream:      good,
			bound:       "2",
			wantRefused: true,
			wantNamed:   "SANITY BOUND",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A stub `go` on PATH, so the script's own `$GO test -json` is what produces the fixture.
			// `GO=` is honoured by the script, so the stub is named plainly.
			dir := t.TempDir()
			stub := filepath.Join(dir, "fakego")
			// No sleep in any case: the stub answers instantly, and the bound does the discriminating.
			body := "#!/bin/sh\ncat <<'JSONEOF'\n" +
				strings.Join(c.stream(), "\n") + "\nJSONEOF\nexit 0\n"
			if err := os.WriteFile(stub, []byte(body), 0o700); err != nil {
				t.Fatalf("writing the stub: %v", err)
			}

			// Bound of 2s with a 3s stub, not the Makefile's 5s with a 6s one: the mechanism is identical
			// and the number is an argument, so fidelity to the Makefile value is not available either
			// way -- and six cases at 6s each put 31s on every `make check` for no extra assertion.
			cmd := exec.Command("sh", script, "witnesses-test", "600", c.bound)
			cmd.Dir = repoRoot
			// CI= set, because the sanity bound only BINDS there — locally it is advisory, and a test
			// that ran without this would assert the advisory path and call it the binding one.
			cmd.Env = append(os.Environ(), "GO="+stub, "CI=1")
			out, runErr := cmd.CombinedOutput()
			refused := runErr != nil

			if refused != c.wantRefused {
				t.Fatalf("refused=%v, want %v. Output:\n%s", refused, c.wantRefused, out)
			}
			if !c.wantRefused {
				return
			}
			if !strings.Contains(string(out), c.wantNamed) {
				t.Errorf("the refusal does not name %q, so this case may be refusing for an unrelated "+
					"reason — which is the defect this file exists for, since four greps over a format "+
					"the script does not control all read as \"no objection\" when they match nothing.\n"+
					"Output:\n%s", c.wantNamed, out)
			}
		})
	}
}

// scriptWitnessNames parses the `witnesses='…'` block out of the script, so the fixtures above are built
// from the set the script actually names.
//
// Derived rather than duplicated for the reason the chair refused a source-level constant check next door: a
// second written-in copy of a value is the pattern this project keeps paying for. Here the parse is cheap and
// its failure is loud — the caller refuses a set smaller than three rather than proceeding over an empty one.
func scriptWitnessNames(src string) []string {
	_, rest, ok := strings.Cut(src, "witnesses='")
	if !ok {
		return nil
	}
	block, _, ok := strings.Cut(rest, "'")
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if n := strings.TrimSpace(line); n != "" {
			out = append(out, n)
		}
	}
	return out
}
