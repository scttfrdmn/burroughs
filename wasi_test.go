// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAWASICommandRunsThroughThePublicPath is [decision 0081][0081]'s rider: the public WASI path is
// covered from its first commit, which is [ADR 0029]'s reason for funnelling the CLI through this
// package rather than letting it reach `internal/`. A Go `wasip1` guest is detected as a command and
// run through `WASIP1Config.Run`, reaching `main`, writing stdout, and exiting 0 — the same evidence the
// internal test carries, now proven on the public surface an embedder and the CLI both use.
//
// [0081]: docs/decisions/0081-burroughs-run-detects-a-wasip1-command-from-the-modules-sections-and-routes-to-a-public-wasi-entry-before-any-plain-instantiate.md
func TestAWASICommandRunsThroughThePublicPath(t *testing.T) {
	wasm := buildWASIGuest(t)

	if ok, err := IsWASIP1Command(wasm); err != nil || !ok {
		t.Fatalf("IsWASIP1Command = (%v, %v), want (true, nil): a Go wasip1 guest imports "+
			"wasi_snapshot_preview1 and exports _start", ok, err)
	}

	var out, errBuf bytes.Buffer
	code, err := WASIP1Config{Args: []string{"hello.wasm"}, Stdout: &out, Stderr: &errBuf}.Run(wasm)
	if err != nil {
		t.Fatalf("WASIP1Config.Run failed: %v\nstderr: %q", err, errBuf.String())
	}
	if code != 0 {
		t.Errorf("guest exited %d, want 0\nstdout: %q\nstderr: %q", code, out.String(), errBuf.String())
	}
	if got := out.String(); !strings.Contains(got, "hello from a go guest on burroughs") {
		t.Errorf("stdout = %q, want the guest's greeting through the public WASI path", got)
	}
}

// TestANonCommandIsNotDetectedAsAWASICommand is IsWASIP1Command's false direction, and it needs no guest: an
// empty module imports nothing and exports no _start, so it is not a command. Without this, a detector
// that answered true for everything would satisfy the row above.
func TestANonCommandIsNotDetectedAsAWASICommand(t *testing.T) {
	empty := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // "\0asm" + version 1
	ok, err := IsWASIP1Command(empty)
	if err != nil {
		t.Fatalf("IsWASIP1Command(empty module) errored: %v", err)
	}
	if ok {
		t.Error("IsWASIP1Command(empty module) = true; a module that imports no wasi and exports no _start " +
			"is not a command, and routing it to the WASI runner would run a module that is not one")
	}
}

// buildWASIGuest compiles the shared hello guest (internal/wasi/testdata/hello) to GOOS=wasip1 and
// returns the bytes. It reuses that guest rather than committing a third copy; the path is computed
// from this test file's own location so it holds regardless of the working directory.
func buildWASIGuest(t *testing.T) []byte {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH, so a wasip1 guest cannot be built: %v", err)
	}
	_, thisFile, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(thisFile), "internal", "wasi", "testdata", "hello")
	out := filepath.Join(t.TempDir(), "hello.wasm")
	cmd := exec.Command(goBin, "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if combined, berr := cmd.CombinedOutput(); berr != nil {
		t.Fatalf("go build -o %s %s (GOOS=wasip1 GOARCH=wasm) failed: %v\n%s", out, src, berr, combined)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the built guest: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("the built guest is empty")
	}
	return b
}
