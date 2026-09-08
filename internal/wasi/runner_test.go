// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestAGoGuestRunsToMainAndWritesStdout is [ADR 0080][0080]'s whole claim, and its result is binary:
// a program compiled by a third-party toolchain reached `main`, wrote to stdout through the preview-1
// host module, and exited 0.
//
// **The guest is compiled here, not committed as a `.wasm`.** Building it every run proves "third-party
// toolchain" freshly and avoids a 2.4 MB binary in the tree whose staleness would make it an oracle
// nobody appointed. The toolchain's absence is a *loud* skip that says why; a toolchain that is present
// but cannot build the target is a failure, not a skip — a skip is not a verdict, so the inputs are
// asserted to exist (grave: [[a-skip-is-not-a-verdict]]).
//
// [0080]: ../../docs/decisions/0080-a-go-wasip1-guest-runs-to-main-on-the-host-surface-and-the-preview1-import-set-is-supplied-whole-because-link-refuses-a-gap.md
func TestAGoGuestRunsToMainAndWritesStdout(t *testing.T) {
	guest := buildGuest(t)

	var out, errBuf bytes.Buffer
	code, err := Run(Config{
		Wasm:   guest,
		Args:   []string{"hello.wasm"},
		Stdout: &out,
		Stderr: &errBuf,
	})
	if err != nil {
		t.Fatalf("running the guest failed: %v\nstderr: %q", err, errBuf.String())
	}
	if code != 0 {
		t.Errorf("guest exited %d, want 0\nstdout: %q\nstderr: %q", code, out.String(), errBuf.String())
	}
	if got := out.String(); !strings.Contains(got, "hello from a go guest on burroughs") {
		t.Errorf("stdout = %q, want it to contain the guest's line — the program ran but its output "+
			"did not reach the host through fd_write", got)
	}
}

// TestTheGoGuestNeedsNoThreadsFeature turns ADR 0080's finding 2 into a checked assertion: the guest
// decodes under a feature set with `Threads` off, so it uses no atomics and no shared memory, so
// `gate:threads` is not load-bearing for this workload. A future Go runtime that started emitting
// atomics would fail here rather than silently — the premise is asserted, not assumed.
func TestTheGoGuestNeedsNoThreadsFeature(t *testing.T) {
	if GuestFeatures().Threads {
		t.Fatal("GuestFeatures has Threads on, so decoding it proves nothing about the guest's needs")
	}
	guest := buildGuest(t)
	if _, err := (&bin.Decoder{Features: GuestFeatures()}).DecodeModule(guest); err != nil {
		t.Errorf("the guest failed to decode with Threads off: %v\n"+
			"That would mean the Go wasip1 runtime now emits a threads-gated opcode, and gate:threads "+
			"would have become load-bearing for a plain guest — the opposite of 0080's finding", err)
	}
}

// buildGuest compiles testdata/hello to GOOS=wasip1 and returns the module bytes.
func buildGuest(t *testing.T) []byte {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH, so a wasip1 guest cannot be built: %v", err)
	}
	out := filepath.Join(t.TempDir(), "hello.wasm")
	cmd := exec.Command(goBin, "build", "-o", out, "./testdata/hello")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if combined, berr := cmd.CombinedOutput(); berr != nil {
		// Present-but-cannot-build is a failure, not a skip: the wasip1 target ships with Go >= 1.21,
		// so a build failure here is a real regression, not an environment gap.
		t.Fatalf("go build -o %s ./testdata/hello (GOOS=wasip1 GOARCH=wasm) failed: %v\n%s", out, berr, combined)
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
