// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// p3sysReading is wasmtime 49.0.3's reading of p3sys.wasm, parsed: each channel's bytes and the exit code.
type p3sysReading struct {
	stdout, stderr string
	exit           int
}

// readP3SysReading parses testdata/p3sys.reading, failing rather than defaulting when a part is missing: a
// witness that falls back to a built-in expectation is checking itself (readingLine's rule).
func readP3SysReading(t *testing.T) p3sysReading {
	t.Helper()
	b, err := os.ReadFile("testdata/p3sys.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	var r p3sysReading
	var out, errb strings.Builder
	sawExit := false
	for _, ln := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "# exit:"):
			code, cerr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(ln, "# exit:")))
			if cerr != nil {
				t.Fatalf("p3sys.reading's exit line %q: %v", ln, cerr)
			}
			r.exit, sawExit = code, true
		case strings.HasPrefix(ln, "stdout: "):
			out.WriteString(strings.TrimPrefix(ln, "stdout: ") + "\n")
		case strings.HasPrefix(ln, "stderr: "):
			errb.WriteString(strings.TrimPrefix(ln, "stderr: ") + "\n")
		}
	}
	if !sawExit || out.Len() == 0 || errb.Len() == 0 {
		t.Fatalf("p3sys.reading records no exit, stdout or stderr (exit seen %v, %d and %d bytes)", sawExit, out.Len(), errb.Len())
	}
	r.stdout, r.stderr = out.String(), errb.String()
	return r
}

// TestP3SysMatchesWasmtime is E4's witness (#1232, ADR 0119): a Rust wasm32-wasip3 std guest calls every
// synchronous WASI 0.3 function E4 serves — the five random functions, both clocks' now and get-resolution,
// get-environment, get-initial-cwd, the three get-terminal-*, stderr's write-via-stream and exit-with-code —
// and prints properties of the answers. Its stdout, stderr and exit code under gate:async must equal wasmtime
// 49.0.3's, byte for byte, as Run would return them.
//
// Neutered, each run (ADR 0119): the random cap at 0 reads false on the two 16-byte lines and 0 on the three
// capped ones; NewHost without start reads "advances: false"; the terminal resource types undeclared refuse
// the guest at instantiate; stderr written to stdout moves its line; realloc over the caller's context slots
// traps out of bounds at the first write after get-random-bytes.
func TestP3SysMatchesWasmtime(t *testing.T) {
	want := readP3SysReading(t)
	b, err := os.ReadFile("testdata/p3sys/p3sys.wasm")
	if err != nil {
		t.Fatalf("p3sys fixture missing (it is committed): %v", err)
	}
	t.Setenv("BURROUGHS_ASYNC", "1")
	var out, errb bytes.Buffer
	h := NewHost(&out, &errb, nil)
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("p3sys did not instantiate: %v", err)
	}
	defer in.Close()
	if err := in.CallRun(); err != nil {
		t.Fatalf("p3sys did not run to completion: %v\nstdout so far:\n%s", err, out.String())
	}
	// Run's own reading of the exit: the recorded code if the guest exited, else 0.
	code := 0
	if h.Exited {
		code = int(h.ExitCode)
	}
	if got := out.String(); got != want.stdout {
		t.Errorf("stdout differs from wasmtime 49.0.3's reading.\n got:\n%s\nwant:\n%s", got, want.stdout)
	}
	if got := errb.String(); got != want.stderr {
		t.Errorf("stderr = %q, want wasmtime's %q", got, want.stderr)
	}
	if code != want.exit {
		t.Errorf("exit code = %d, want wasmtime's %d", code, want.exit)
	}
}

// TestReallocRunsWithItsOwnContext is the repair p3sys compelled (ADR 0119), on a fixture that isolates it:
// a lower's realloc runs with context storage of its own, starting at zero, and the caller's slots are as it
// left them when the realloc returns. In the pin, `reallocate` invokes the realloc as a call of its own, on a
// new Thread. realloc-context.wasm's `run` reports both halves in one u32, and wasmtime 49.0.3 reads
// 12345 << 16 | 0.
//
// Neutered, each run: realloc over the caller's slots, as before ADR 0119, reads 0xdead << 16 | 12345. The
// zeroing alone removed reads 12345 << 16 | 12345, and the restore alone removed reads 0xdead << 16 | 0, so
// each half is witnessed here, where p3sys witnesses only the restore.
func TestReallocRunsWithItsOwnContext(t *testing.T) {
	want := readingLine(t, "testdata/realloc-context.reading", "wasmtime 49.0.3, 10 of 10")
	b, err := os.ReadFile("testdata/realloc-context.wasm")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BURROUGHS_ASYNC", "1")
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	res, err := in.CallValues("run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	v, ok := res[0].U32()
	if !ok {
		t.Fatalf("run returned %v, not a u32", res[0])
	}
	if got := strconv.FormatUint(uint64(v), 10); got != want {
		t.Errorf("run = %s (run's slot %d, the realloc saw %d), want wasmtime's %s", got, v>>16, v&0xffff, want)
	}
}
