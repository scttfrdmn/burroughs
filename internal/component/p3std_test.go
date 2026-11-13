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

// readP3StdReading parses testdata/p3std.reading: each channel's bytes and the exit code. A channel's last
// line carries a newline only where the reading's header says the channel ended with one, because p3std's
// stdout does not, by design. A missing part fails rather than defaults (readingLine's rule).
func readP3StdReading(t *testing.T) p3sysReading {
	t.Helper()
	b, err := os.ReadFile("testdata/p3std.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	lines := map[string][]string{}
	ends := map[string]string{}
	exit, sawExit := 0, false
	for _, ln := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "# exit:"):
			code, cerr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(ln, "# exit:")))
			if cerr != nil {
				t.Fatalf("p3std.reading's exit line %q: %v", ln, cerr)
			}
			exit, sawExit = code, true
		case strings.HasPrefix(ln, "# stdout ends with a newline:"):
			ends["stdout"] = strings.TrimSpace(strings.TrimPrefix(ln, "# stdout ends with a newline:"))
		case strings.HasPrefix(ln, "# stderr ends with a newline:"):
			ends["stderr"] = strings.TrimSpace(strings.TrimPrefix(ln, "# stderr ends with a newline:"))
		case strings.HasPrefix(ln, "stdout: "):
			lines["stdout"] = append(lines["stdout"], strings.TrimPrefix(ln, "stdout: "))
		case strings.HasPrefix(ln, "stderr: "):
			lines["stderr"] = append(lines["stderr"], strings.TrimPrefix(ln, "stderr: "))
		}
	}
	channel := func(name string) string {
		if len(lines[name]) == 0 {
			t.Fatalf("p3std.reading records no %s", name)
		}
		s := strings.Join(lines[name], "\n")
		switch ends[name] {
		case "yes":
			return s + "\n"
		case "no":
			return s
		}
		t.Fatalf("p3std.reading does not say whether %s ends with a newline (got %q)", name, ends[name])
		return ""
	}
	if !sawExit {
		t.Fatal("p3std.reading records no exit")
	}
	return p3sysReading{stdout: channel("stdout"), stderr: channel("stderr"), exit: exit}
}

// TestP3StdMatchesWasmtime is the witness for #1261 and #1262 (ADR 0120): an ordinary Rust wasm32-wasip3 std
// program, with no bindings, prints three lines, one to stderr, a print of nothing and a flush, then a line
// with no newline, and calls std::process::exit(7). Its stdout, stderr and exit code under gate:async must
// equal wasmtime 49.0.3's, byte for byte, read through CallRun as Run reads them.
//
// Neutered, each run (ADR 0120):
//   - the host read not re-armed: the second line traps "cannot block a synchronous task before returning";
//   - futureRead's inline arm removed: the read of write-via-stream's future, after std drops its writer,
//     is refused as single-shot;
//   - exit-with-code returning nil: the guest runs on to std's `unreachable` and traps;
//   - CallRun not unwrapping the exit: the run's exit arrives as an error in place of code 7.
func TestP3StdMatchesWasmtime(t *testing.T) {
	want := readP3StdReading(t)
	b, err := os.ReadFile("testdata/p3std/p3std.wasm")
	if err != nil {
		t.Fatalf("p3std fixture missing (it is committed): %v", err)
	}
	t.Setenv("BURROUGHS_ASYNC", "1")
	var out, errb bytes.Buffer
	in, err := InstantiateWithHost(b, NewHost(&out, &errb, nil))
	if err != nil {
		t.Fatalf("p3std did not instantiate: %v", err)
	}
	defer in.Close()
	code, err := in.CallRun()
	if err != nil {
		t.Fatalf("p3std did not run to its exit: %v\nstdout so far: %q\nstderr so far: %q", err, out.String(), errb.String())
	}
	if got := out.String(); got != want.stdout {
		t.Errorf("stdout differs from wasmtime 49.0.3's reading.\n got: %q\nwant: %q", got, want.stdout)
	}
	if got := errb.String(); got != want.stderr {
		t.Errorf("stderr = %q, want wasmtime's %q", got, want.stderr)
	}
	if code != want.exit {
		t.Errorf("exit code = %d, want wasmtime's %d", code, want.exit)
	}
}

// TestARunReturningErrExitsOne is `run`'s result read (ADR 0120): a `wasi:cli/run` export returning err is
// exit 1 and its ok control exit 0, each as wasmtime 49.0.3 exits (run-result.reading). Burroughs discarded
// the result, so err exited 0.
//
// Neutered, run: runReturnedErr returning false, and the err fixture exits 0.
func TestARunReturningErrExitsOne(t *testing.T) {
	for _, c := range []struct{ name, reader string }{
		{"err", "wasmtime 49.0.3 on err, 10 of 10"},
		{"ok", "wasmtime 49.0.3 on ok, 10 of 10"},
	} {
		t.Run(c.name, func(t *testing.T) {
			want, err := strconv.Atoi(readingLine(t, "testdata/run-result.reading", c.reader))
			if err != nil {
				t.Fatalf("run-result.reading's %q line: %v", c.reader, err)
			}
			b, err := os.ReadFile("testdata/run-result-" + c.name + ".wasm")
			if err != nil {
				t.Fatal(err)
			}
			in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			code, err := in.CallRun()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if code != want {
				t.Errorf("run returning %s exited %d, want wasmtime's %d", c.name, code, want)
			}
		})
	}
}
