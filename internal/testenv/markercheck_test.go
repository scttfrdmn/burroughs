// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMarkerCheckRefusesAStatusWrittenAgainstAnOlderRelease is the control for `scripts/markercheck.py` (the
// chair's fifth ruling of 2026-10-28, item 4b). During a cut, the README's *Written against `vX.Y.Z`* marker must
// name the release being cut, so a front door describing the previous release fails the release PR.
//
// The witness is the stale row: a README written against 0.9.0, while 1.0.0 is cut, fails.
//
// NEUTER, run (2026-10-28, darwin/arm64): with the `markers[0] != cutting` comparison removed, the stale row
// passes and this fails on it.
func TestMarkerCheckRefusesAStatusWrittenAgainstAnOlderRelease(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "markercheck.py")
	cut := "# Changelog\n\n## [Unreleased]\n*Implements contract v0.1.*\n\n## [1.0.0] - 2026-10-28\n\n- a change\n"
	notCut := strings.Replace(cut, "*Implements contract v0.1.*\n", "*Implements contract v0.1.*\n\n### Changed\n- x\n", 1)
	readme := func(v string) string { return "# R\n\n## Status\n\n*Written against `v" + v + "`.*\n\n- a fact\n" }

	run := func(t *testing.T, changelog, rd string, args ...string) (int, string) {
		t.Helper()
		dir := t.TempDir()
		cl := filepath.Join(dir, "CHANGELOG.md")
		rm := filepath.Join(dir, "README.md")
		if werr := os.WriteFile(cl, []byte(changelog), 0o600); werr != nil {
			t.Fatal(werr)
		}
		if werr := os.WriteFile(rm, []byte(rd), 0o600); werr != nil {
			t.Fatal(werr)
		}
		cmd := exec.Command("python3", append(append([]string{script}, args...), cl, rm)...)
		out, rerr := cmd.CombinedOutput()
		code := 0
		var ee *exec.ExitError
		if errors.As(rerr, &ee) {
			code = ee.ExitCode()
		} else if rerr != nil {
			t.Fatalf("running markercheck: %v\n%s", rerr, out)
		}
		return code, string(out)
	}

	for _, a := range []struct {
		name, changelog, readme string
		args                    []string
		code                    int
		say                     string
	}{
		{"the marker names the release being cut", cut, readme("1.0.0"), nil, 0, "names v1.0.0"},
		{"a stale marker fails the cut", cut, readme("0.9.0"), nil, 1, "written against v0.9.0"},
		{"no marker fails", cut, "# R\n\n## Status\n\n- a fact\n", nil, 1, "has 0"},
		{"two markers fail", cut, readme("1.0.0") + readme("1.0.0"), nil, 1, "has 2"},
		{"between cuts nothing is checked", notCut, readme("0.9.0"), nil, 0, "no release cut is in progress"},
		{"--force checks between cuts", notCut, readme("0.9.0"), []string{"--force"}, 1, "written against v0.9.0"},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.changelog, a.readme, a.args...)
			if code != a.code || !strings.Contains(out, a.say) {
				t.Errorf("exit %d, want %d, with %q in the output:\n%s", code, a.code, a.say, out)
			}
		})
	}
}
