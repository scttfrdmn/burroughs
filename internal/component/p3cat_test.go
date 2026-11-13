// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// readP3CatReading parses one of p3cat's committed readings: stdout's lines, the exit code, and stderr's byte
// count, which is 0. A missing part fails rather than defaults (readingLine's rule).
func readP3CatReading(t *testing.T, path string) (stdout string, exit int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading %s is missing, so this witness has no oracle: %v", path, err)
	}
	var lines []string
	sawExit, sawStderr, endsNL := false, false, ""
	for _, ln := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "# exit:"):
			code, cerr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(ln, "# exit:")))
			if cerr != nil {
				t.Fatalf("%s's exit line %q: %v", path, ln, cerr)
			}
			exit, sawExit = code, true
		case strings.HasPrefix(ln, "# stderr bytes:"):
			if strings.TrimSpace(strings.TrimPrefix(ln, "# stderr bytes:")) != "0" {
				t.Fatalf("%s records stderr output (%q); this witness compares an empty stderr", path, ln)
			}
			sawStderr = true
		case strings.HasPrefix(ln, "# stdout ends with a newline:"):
			endsNL = strings.TrimSpace(strings.TrimPrefix(ln, "# stdout ends with a newline:"))
		case strings.HasPrefix(ln, "stdout: "):
			lines = append(lines, strings.TrimPrefix(ln, "stdout: "))
		}
	}
	if !sawExit || !sawStderr || endsNL != "yes" || len(lines) == 0 {
		t.Fatalf("%s is incomplete: exit %v, stderr %v, stdout ends with a newline %q, %d stdout lines",
			path, sawExit, sawStderr, endsNL, len(lines))
	}
	return strings.Join(lines, "\n") + "\n", exit
}

// TestP3CatMatchesWasmtime is E6's std witness (#1234, #1264, ADR 0122): an ordinary Rust wasm32-wasip3 program,
// with no bindings, reads stdin to its end with read_to_end and prints the byte count, a checksum, the line count
// and the first line. Its stdout, its empty stderr and its exit code must equal wasmtime 49.0.3's, byte for byte,
// read through CallRun as Run reads them, on two stdins:
//
//   - `p3cat.stdin`, 20770 bytes: two full 8 KiB reads and a short third, then the read that sees the writer's
//     drop (DROPPED) and the future read that tells the end of input from an error;
//   - none (a nil Host.Stdin, as `</dev/null` is to wasmtime): the first read is the DROPPED.
//
// wasi-libc calls read-via-stream once, sync-lowered with a return pointer, and issues one async stream.read per
// read(), which blocks on a waitable set inside `run` when it returns BLOCKED. A third stdin is the same bytes
// through an io.Pipe written in 1000-byte pieces, so every Read is short and some reads park until the next
// piece.
//
// Neutered, each run (ADR 0122):
//   - stream.read refused again (0x0f out of isBuiltAsyncBuiltin): refused at instantiate, naming 0xf;
//   - driveStreamCopyLocked's host-source arm dropped: every read completes with progress 0, which wasi-libc
//     takes as the end, and stdout reads "read 0 bytes";
//   - the end of input resolving the future `err(io)`: std prints "reading stdin failed: I/O error" and exits 1;
//   - the decoder dropping future.read's memory again: the future read at the end of input traps, "the instance
//     that declared this host function has no memory 0".
//
// #1264 unrepaired (dropWriterLocked leaving `conn` set and notifying only a pending read) does NOT fail this
// witness: p3cat's stream finishes with a read pending, which the old notify reached.
// TestAWritersDropUnlinksItsReader is #1264's witness.
func TestP3CatMatchesWasmtime(t *testing.T) {
	b, err := os.ReadFile("testdata/p3cat/p3cat.wasm")
	if err != nil {
		t.Fatalf("p3cat fixture missing (it is committed): %v", err)
	}
	stdin, err := os.ReadFile("testdata/p3cat.stdin")
	if err != nil {
		t.Fatalf("p3cat's stdin is missing (it is committed): %v", err)
	}
	// piped is the same bytes through an io.Pipe written in 1000-byte pieces, so each Read returns at most a
	// piece: the reads see partial progress at every step, and some park BLOCKED until the next piece.
	piped := func() io.Reader {
		pr, pw := io.Pipe()
		go func() {
			for rest := stdin; len(rest) > 0; {
				n := min(1000, len(rest))
				if _, werr := pw.Write(rest[:n]); werr != nil {
					return
				}
				rest = rest[n:]
			}
			_ = pw.Close()
		}()
		return pr
	}
	for _, c := range []struct {
		name    string
		stdin   func() io.Reader
		reading string
	}{
		{"p3cat.stdin", func() io.Reader { return bytes.NewReader(stdin) }, "testdata/p3cat.reading"},
		{"p3cat.stdin-piped", piped, "testdata/p3cat.reading"},
		{"no-stdin", func() io.Reader { return nil }, "testdata/p3cat-empty.reading"},
	} {
		want, wantExit := readP3CatReading(t, c.reading)
		for _, gate := range []string{"0", "1"} {
			t.Run(c.name+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				var out, errb bytes.Buffer
				in, err := InstantiateWithHost(b, NewHost(&out, &errb, c.stdin()))
				if err != nil {
					t.Fatalf("p3cat did not instantiate: %v", err)
				}
				defer in.Close()
				type result struct {
					code int
					err  error
				}
				done := make(chan result, 1)
				go func() { code, e := in.CallRun(); done <- result{code, e} }()
				var r result
				select {
				case r = <-done:
				case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
					t.Fatalf("p3cat's run did not return within 10s; stdout so far: %q", out.String())
				}
				if r.err != nil {
					t.Fatalf("p3cat did not run to its exit: %v\nstdout so far: %q\nstderr: %q", r.err, out.String(), errb.String())
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
}
