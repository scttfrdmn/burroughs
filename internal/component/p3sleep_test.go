// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// readP3SleepReading parses testdata/p3sleep.reading: stdout's lines, the exit code, and stderr's byte count,
// which is 0. A missing part fails rather than defaults (readingLine's rule).
func readP3SleepReading(t *testing.T) (stdout string, exit int) {
	t.Helper()
	b, err := os.ReadFile("testdata/p3sleep.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	var lines []string
	sawExit, sawStderr, endsNL := false, false, ""
	for _, ln := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "# exit:"):
			code, cerr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(ln, "# exit:")))
			if cerr != nil {
				t.Fatalf("p3sleep.reading's exit line %q: %v", ln, cerr)
			}
			exit, sawExit = code, true
		case strings.HasPrefix(ln, "# stderr bytes:"):
			if strings.TrimSpace(strings.TrimPrefix(ln, "# stderr bytes:")) != "0" {
				t.Fatalf("p3sleep.reading records stderr output (%q); this witness compares an empty stderr", ln)
			}
			sawStderr = true
		case strings.HasPrefix(ln, "# stdout ends with a newline:"):
			endsNL = strings.TrimSpace(strings.TrimPrefix(ln, "# stdout ends with a newline:"))
		case strings.HasPrefix(ln, "stdout: "):
			lines = append(lines, strings.TrimPrefix(ln, "stdout: "))
		}
	}
	if !sawExit || !sawStderr || endsNL != "yes" || len(lines) == 0 {
		t.Fatalf("p3sleep.reading is incomplete: exit %v, stderr %v, stdout ends with a newline %q, %d stdout lines",
			sawExit, sawStderr, endsNL, len(lines))
	}
	return strings.Join(lines, "\n") + "\n", exit
}

// TestP3SleepMatchesWasmtime is E5's std witness (#1233, grave #1267, ADR 0121): an ordinary Rust wasm32-wasip3
// program, with no bindings, sleeps 20ms with thread::sleep, sleeps until a deadline 20ms on with
// thread::sleep_until, and sleeps for zero. It prints properties of each, never durations, and returns from
// main. Its stdout, its empty stderr and its exit code must equal wasmtime 49.0.3's, byte for byte, read
// through CallRun as Run reads them.
//
// thread::sleep is wasi-libc's clock_nanosleep: it async-lowers `wait-for` and blocks in waitable-set.wait
// inside `run`, which is async-typed and lifted plainly. That is grave #1267's specimen.
//
// Neutered, each run (ADR 0121):
//   - the sync tail judged by lift form again (`!t.asyncTyped` dropped): the first sleep traps "cannot block a
//     synchronous task before returning";
//   - the timer resolving inline whatever the deadline: the sleep and sleep_until lines both read false;
//   - wait-until reading `when` against the system clock: the sleep_until line reads false.
func TestP3SleepMatchesWasmtime(t *testing.T) {
	want, wantExit := readP3SleepReading(t)
	b, err := os.ReadFile("testdata/p3sleep/p3sleep.wasm")
	if err != nil {
		t.Fatalf("p3sleep fixture missing (it is committed): %v", err)
	}
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			t.Setenv(cmThreadsGateEnv, gate)
			var out, errb bytes.Buffer
			in, err := InstantiateWithHost(b, NewHost(&out, &errb, nil))
			if err != nil {
				t.Fatalf("p3sleep did not instantiate: %v", err)
			}
			defer in.Close()
			type result struct {
				code int
				err  error
			}
			done := make(chan result, 1)
			go func() { c, e := in.CallRun(); done <- result{c, e} }()
			var r result
			select {
			case r = <-done:
			case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
				t.Fatalf("p3sleep's run did not return within 10s; stdout so far: %q", out.String())
			}
			if r.err != nil {
				t.Fatalf("p3sleep did not run to its exit: %v\nstdout so far: %q", r.err, out.String())
			}
			if got := out.String(); got != want {
				t.Errorf("stdout differs from wasmtime 49.0.3's reading.\n got: %q\nwant: %q", got, want)
			}
			if errb.Len() != 0 {
				t.Errorf("stderr = %q, want nothing, as wasmtime writes", errb.String())
			}
			if r.code != wantExit {
				t.Errorf("exit code = %d, want wasmtime's %d", r.code, wantExit)
			}
		})
	}
}
