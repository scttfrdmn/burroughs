// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunExecutesAWASICommand is decision 0081's external-invocation proof: `burroughs run <guest>`
// runs a Go wasip1 command to main and writes its stdout — the capability invoked the way a user
// invokes it, from outside the tree, through the same public package a host embedder uses.
func TestRunExecutesAWASICommand(t *testing.T) {
	guest := buildGuestFile(t, "hello")

	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", guest})
	if code != 0 {
		t.Errorf("`run <guest>` exited %d, want 0\nstderr: %q", code, errBuf.String())
	}
	if got := out.String(); !strings.Contains(got, "hello from a go guest on burroughs") {
		t.Errorf("stdout = %q, want the guest's greeting — a wasip1 command should run, not list", got)
	}
}

// TestExitCodeCarriesAWASIGuestsOwnCode pins that a guest's exit code is propagated verbatim, ahead of
// the CLI's own taxonomy. Code 3 is chosen deliberately: it is `exitRefused` in the taxonomy, so a
// wasiExit(3) that did not win ahead of the switch would still return 3 for the wrong reason — the
// test discriminates the guest's channel from the CLI's by using a value they share.
func TestExitCodeCarriesAWASIGuestsOwnCode(t *testing.T) {
	if got := exitCode(wasiExit(3)); got != 3 {
		t.Errorf("exitCode(wasiExit(3)) = %d, want 3: a guest's exit code is the process's, "+
			"caught ahead of the CLI taxonomy (decision 0081)", got)
	}
}

// TestRunGrantsAndDeniesFilesystemAccess is decision 0083's capability model at the command line:
// `--dir` grants a directory and `--` passes the guest its argv, so a granted read succeeds; with no
// `--dir` the same read is refused and its bytes never reach stdout. The grant is the only difference
// between the two halves, so a boundary that leaked would show up here as the ungranted read
// succeeding.
func TestRunGrantsAndDeniesFilesystemAccess(t *testing.T) {
	guest := buildGuestFile(t, "cat")
	dir := t.TempDir()
	const body = "cli-granted-body"
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// Granted: --dir maps the directory, -- passes the path as the guest's argv.
	var out, errBuf bytes.Buffer
	if code := dispatch(&out, &errBuf, []string{"run", "--dir", dir + ":/d", guest, "--", "/d/in.txt"}); code != 0 {
		t.Errorf("granted run exited %d, want 0\nstderr: %q", code, errBuf.String())
	}
	if !strings.Contains(out.String(), body) {
		t.Errorf("granted run stdout = %q, want the file's body", out.String())
	}

	// Denied: the same guest and path, no --dir — the read is unreachable and the guest exits non-zero.
	out.Reset()
	errBuf.Reset()
	if code := dispatch(&out, &errBuf, []string{"run", guest, "--", "/d/in.txt"}); code == 0 {
		t.Error("ungranted run exited 0; the CLI ran a read it never granted")
	}
	if strings.Contains(out.String(), body) {
		t.Errorf("ungranted run stdout = %q leaked the file with no --dir", out.String())
	}
}

// buildGuestFile compiles a shared guest (internal/wasi/testdata/<pkg>) to GOOS=wasip1 and returns the
// path to the .wasm, which `run` reads. The path is computed from this test file's own location, and
// the guests are the ones internal/wasi already carries rather than copies.
func buildGuestFile(t *testing.T, pkg string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH, so a wasip1 guest cannot be built: %v", err)
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	src := filepath.Join(repoRoot, "internal", "wasi", "testdata", pkg)
	out := filepath.Join(t.TempDir(), pkg+".wasm")
	cmd := exec.Command(goBin, "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if combined, berr := cmd.CombinedOutput(); berr != nil {
		t.Fatalf("go build -o %s %s (GOOS=wasip1 GOARCH=wasm) failed: %v\n%s", out, src, berr, combined)
	}
	if _, serr := os.Stat(out); serr != nil {
		t.Fatalf("the built guest is missing: %v", serr)
	}
	return out
}
