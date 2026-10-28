// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
)

const tagline = " — the machine Burroughs never built"

// TestTheVersionBannerResolvesBuildInfoFirstThenLdflags is the resolution's table: the module version the build
// recorded wins, `(devel)` and an empty version count as none, the ldflags value is the fallback, and with
// neither the banner says `(devel)` rather than inventing a number (#1130).
func TestTheVersionBannerResolvesBuildInfoFirstThenLdflags(t *testing.T) {
	stamped := &debug.BuildInfo{Main: debug.Module{Version: "v0.13.0"}}
	devel := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	empty := &debug.BuildInfo{}
	for _, c := range []struct {
		name    string
		bi      *debug.BuildInfo
		ok      bool
		ldflags string
		want    string
	}{
		{"a stamped build ignores ldflags", stamped, true, "v9.9.9", "v0.13.0"},
		{"a stamped build alone", stamped, true, "", "v0.13.0"},
		{"(devel) falls back to ldflags", devel, true, "v9.9.9", "v9.9.9"},
		{"an empty version falls back to ldflags", empty, true, "v9.9.9", "v9.9.9"},
		{"no build info falls back to ldflags", nil, false, "v9.9.9", "v9.9.9"},
		{"nothing at all", devel, true, "", "(devel)"},
	} {
		if got, want := bannerFor(c.bi, c.ok, c.ldflags), "burroughs "+c.want+tagline; got != want {
			t.Errorf("%s: %q, want %q", c.name, got, want)
		}
	}
}

// TestABuiltBinaryReportsTheVersionItWasBuiltWith is the ruling's witness (the chair's fifth ruling of
// 2026-10-28, item 3): the CLI built with a known ldflags value prints it. `-buildvcs=false` is what makes the
// fallback the arm under test, because a build in a checkout would otherwise record a VCS pseudo-version and
// the first arm would answer.
//
// **Neuter, run** (2026-10-28, darwin/arm64): with `banner` passing "" in place of `version`, the binary prints
// `(devel)` and this fails.
func TestABuiltBinaryReportsTheVersionItWasBuiltWith(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("no go toolchain on PATH, so the CLI cannot be built: %v", err)
	}
	const known = "v9.8.7-test.1130"
	bin := filepath.Join(t.TempDir(), "burroughs")
	if runtime.GOOS == "windows" {
		bin += ".exe" // exec finds no binary without it there (#1164's Windows run, the same harness shape)
	}
	build := exec.Command(goBin, "build", "-buildvcs=false", "-ldflags", "-X main.version="+known, "-o", bin, ".")
	if out, berr := build.CombinedOutput(); berr != nil {
		t.Fatalf("building the CLI: %v\n%s", berr, out)
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("running %s version: %v", bin, err)
	}
	if got, want := string(out), "burroughs "+known+tagline+"\n"; got != want {
		t.Errorf("the CLI built with -X main.version=%s printed %q, want %q", known, got, want)
	}
}
