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

// TestUnfiredListsWhatWasNotCitedAndOmitsWhatWas is scripts/unfired.py's control (the chair's fifth ruling of
// 2026-10-28, item 6). It runs offline over fixed evidence and a fixed refusal log, so it asks the matching and
// not the network:
//   - a law cited by its anchor is not listed, and one cited by its quoted title is not listed either;
//   - a law cited only by a longer anchor that begins with its own is listed, because that is a different law;
//   - a law nobody cited is listed;
//   - a route with a refusal in the window is not listed, and one whose only refusal predates the window is.
//
// **Neuter, run** (2026-10-28, darwin/arm64): with `fired` returning False, the cited laws are listed and this
// fails on its first two rows.
func TestUnfiredListsWhatWasNotCitedAndOmitsWhatWas(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	evidence := filepath.Join(dir, "evidence")
	log := filepath.Join(dir, "log")
	ev := "see docs/laws/graves-and-sweeps.md#sweep-after-a-grave for the sweep\n" +
		"as *a third verdict needs a structural bound, not just a watched one* says\n" +
		"and boards-and-buckets.md#a-skip-is-not-a-verdict-x is a longer anchor\n"
	if err := os.WriteFile(evidence, []byte(ev), 0o600); err != nil {
		t.Fatal(err)
	}
	lg := "2026-10-28T10:00:00Z\tsleep-as-wait\tabc\n2025-01-01T00:00:00Z\ttee-write\tdef\n"
	if err := os.WriteFile(log, []byte(lg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", filepath.Join(root, "scripts", "unfired.py"),
		"--since", "2026-10-01T00:00:00Z", "--evidence", evidence, "--log", log)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("unfired.py: %v\n%s", err, stderr.String())
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(line); strings.HasPrefix(line, "  ") && len(f) > 0 {
			listed[f[0]] = true
		}
	}
	for _, c := range []struct {
		entry string
		want  bool
		why   string
	}{
		{"graves-and-sweeps.md#sweep-after-a-grave", false, "it is cited by its anchor"},
		{"boards-and-buckets.md#a-third-verdict-needs-a-structural-bound-not-just-a-watched-one", false, "its title is quoted"},
		{"boards-and-buckets.md#a-skip-is-not-a-verdict", true, "only a longer anchor that begins with its own is cited"},
		{"graves-and-sweeps.md#artifacts-become-oracles", true, "nothing cites it"},
		{"sleep-as-wait", false, "it refused inside the window"},
		{"tee-write", true, "its only refusal predates the window"},
	} {
		if listed[c.entry] != c.want {
			t.Errorf("%s: listed=%v, want %v, because %s\noutput:\n%s", c.entry, listed[c.entry], c.want, c.why, out.String())
		}
	}
}
