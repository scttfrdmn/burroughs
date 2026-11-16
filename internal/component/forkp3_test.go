// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// forkP3Dir holds the fork's components (F1, F2, F3a, F3b), their sources, their wasmtime readings and their PROVENANCE. The bytes
// are committed, so this test needs no fork and runs wherever the package's other tests do.
const forkP3Dir = "testdata/forkp3"

// TestForkWasip3MatchesWasmtime is F1's witness (#1236, ADR 0114): Go programs built for the fork's
// GOOS=wasip3, formed into WASI 0.3 components by `wasm-tools component new` with no adapter, run on
// Burroughs as they run on wasmtime 49.0.3.
//
//   - p3exit0, `func main() {}`, exits 0: main returns, the runtime calls wasi:cli/exit@0.3.0's exit with ok.
//
// F1's second case, p3panic, trapped `unreachable` in the runtime's usleep before printing anything. F3a
// (#1238) rebuilt it on a runtime that sleeps and writes stderr, so it now prints and exits 2, and
// TestForkWasip3RuntimeMatchesWasmtime asks about it, stdout and stderr included. F1's reading of it is kept
// in forkp3.reading, relabelled.
//
// The expected outcomes are wasmtime's, read from the committed forkp3.reading, not chosen here. The runs go
// through CallRun, the call ComponentConfig.Run makes. The core-module subtest asserts F1's other claim:
// the Go module imports only Canonical ABI `wasi:` names, so no adapter stands between it and the host.
//
// Neutered, each run:
//   - p3exit0.wasm replaced by F3a's p3panic.wasm: p3exit0 exits 2 where wasmtime's reading is exit 0 (with
//     F1's p3panic.wasm it trapped `unreachable`). The core-module subtest fails too, on 3 core modules: an
//     F3a build carries wit-component's shim and fixup modules beside the Go module, for the imports lowered
//     with the memory option, as F2's p3bind already does. Neither is an adapter, so "one core module" is
//     the no-adapter shape only for F1's single-import builds, which is all this subtest is asked about.
//   - p3exit0.wasm replaced by a component built from the fork's wasip1 `_start` (GOEXPERIMENT=componentabi,
//     the same source) under an empty world: its run is refused, "no wasi:cli/run export", and its core
//     module imports nothing, which the core-module subtest refuses as vacuous. `wasm-tools component new`
//     refuses that module against wasi:cli/command@0.3.0 outright: "failed to find export of interface
//     `wasi:cli/run@0.3.0` function `run`".
//   - the prefix asked of each import narrowed to "wasi:http": the subtest names "wasi:cli/exit@0.3.0"
//     "exit", so the loop reads the import it is about.
func TestForkWasip3MatchesWasmtime(t *testing.T) {
	for _, c := range []string{"p3exit0"} {
		t.Run(c, func(t *testing.T) {
			want := readingLine(t, filepath.Join(forkP3Dir, "forkp3.reading"), "wasmtime 49.0.3 on "+c+", 10 of 10")
			in, err := InstantiateWithHost(readForkP3(t, c), NewHost(io.Discard, io.Discard, nil))
			if err != nil {
				t.Fatalf("%s did not instantiate: %v", c, err)
			}
			defer in.Close()
			code, err := in.CallRun()
			switch {
			case strings.HasPrefix(want, "exit "):
				wantCode, cerr := strconv.Atoi(strings.TrimPrefix(want, "exit "))
				if cerr != nil {
					t.Fatalf("forkp3.reading's %s line %q: %v", c, want, cerr)
				}
				if err != nil {
					t.Fatalf("%s's run failed where wasmtime exits %d: %v", c, wantCode, err)
				}
				if code != wantCode {
					t.Errorf("%s exited %d, want wasmtime's %d", c, code, wantCode)
				}
			case strings.HasPrefix(want, "trap "):
				var tr *interp.Trap
				if !errors.As(err, &tr) {
					t.Fatalf("%s's run = (%d, %v), want a trap, as wasmtime traps", c, code, err)
				}
				if reason := strings.TrimPrefix(want, "trap "); tr.Reason != reason {
					t.Errorf("%s trapped %q, want wasmtime's %q", c, tr.Reason, reason)
				}
			default:
				t.Fatalf("forkp3.reading's %s line %q is neither an exit nor a trap", c, want)
			}
		})
	}

	t.Run("core module imports only wasi names", func(t *testing.T) {
		comp, err := Load(readForkP3(t, "p3exit0"))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		// One core module is the no-adapter shape: an adapted component carries the adapter's module too.
		if len(comp.CoreModules) != 1 {
			t.Fatalf("p3exit0 carries %d core modules, want 1, the Go module and no adapter", len(comp.CoreModules))
		}
		imports := comp.CoreModules[0].Imports
		// An empty list would pass the loop below for no reason, so the vacuous case is refused by name.
		if len(imports) == 0 {
			t.Fatal("p3exit0's core module imports nothing, so the Canonical ABI names it should import were not asked about")
		}
		for _, im := range imports {
			if !strings.HasPrefix(im.Module, "wasi:") {
				t.Errorf("p3exit0's core module imports %q %q, which is not a wasi: interface name", im.Module, im.Name)
			}
		}
	})
}

// p3bindAllProperties is every bit p3bind.go.txt can set: its seven properties, each held.
const p3bindAllProperties = 0x7f

// TestForkWasip3BindingsMatchWasmtime is F2's witness (#1237, ADR 0114). p3bind is a GOOS=wasip3 guest that calls
// the fork's generated WASI 0.3 bindings directly: get-arguments and get-random-bytes, whose lists the host places
// through the bindings' own cabi_realloc; monotonic-clock now, a flat i64; system-clock now, a 16-byte record through
// a return area; get-random-u64; and get-insecure-seed, a tuple through a return area. It reports each property it
// observed as one bit of the exit code, through exit-with-code, because F2 lowers no stdout.
//
// Run with the argv the committed reading records, it exits here with the code wasmtime 49.0.3 exits with, read
// from forkp3.reading, and neither engine writes a byte. That code is also checked to be every property, so a
// reading in which wasmtime itself observed a failure cannot pass as an agreement.
//
// Neutered, each run:
//   - the argv's token replaced by "f2-bindings-wrong": p3bind exits 126 (0x01 clear) where wasmtime's reading is 127.
//   - p3bind.wasm replaced by a build whose generated system_clock.go reads instant.nanoseconds at offset 0 rather
//     than 8: it exits 123 (0x04 clear), as it does on wasmtime.
//   - p3bind.wasm replaced by a build with the linker's trap stand-in forced and internal/wasi's cabi_realloc left
//     unexported: its run fails, trap `unreachable`, at the host's first allocation, where wasmtime's reading is
//     exit 127. wasmtime's backtrace for that build names the stand-in, cabi_realloc (forkp3.reading).
//   - wasmtime's reading edited to 126, with the token wrong as above: the run agrees with it, and the
//     every-property check refuses the reading.
func TestForkWasip3BindingsMatchWasmtime(t *testing.T) {
	reading := filepath.Join(forkP3Dir, "forkp3.reading")
	argv := strings.Fields(readingLine(t, reading, "p3bind argv"))
	if len(argv) != 2 {
		t.Fatalf("forkp3.reading's p3bind argv is %q, want the program name and the token", argv)
	}
	want := readingLine(t, reading, "wasmtime 49.0.3 on p3bind, 10 of 10")
	wantCode, err := strconv.Atoi(strings.TrimPrefix(want, "exit "))
	if !strings.HasPrefix(want, "exit ") || err != nil {
		t.Fatalf("forkp3.reading's p3bind line %q is not an exit code", want)
	}
	if wantCode != p3bindAllProperties {
		t.Fatalf("wasmtime's reading is exit %d (%07b), not every property (%07b): the oracle itself saw a property fail",
			wantCode, wantCode, p3bindAllProperties)
	}

	var stdout, stderr strings.Builder
	h := NewHost(&stdout, &stderr, nil)
	h.Args = argv
	in, err := InstantiateWithHost(readForkP3(t, "p3bind"), h)
	if err != nil {
		t.Fatalf("p3bind did not instantiate: %v", err)
	}
	defer in.Close()
	code, err := in.CallRun()
	if err != nil {
		t.Fatalf("p3bind's run failed where wasmtime exits %d: %v", wantCode, err)
	}
	if code != wantCode {
		t.Errorf("p3bind exited %d (%07b), want wasmtime's %d (%07b): bits %07b were observed there and not here",
			code, code, wantCode, wantCode, wantCode&^code)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("p3bind wrote %q to stdout and %q to stderr; wasmtime's runs wrote nothing", stdout.String(), stderr.String())
	}
}

// p3timeAllProperties is every bit p3time.go.txt can set: its three clock properties, each held.
const p3timeAllProperties = 0x07

// TestForkWasip3RuntimeMatchesWasmtime is F3a's witness (#1238, ADR 0114). The fork's runtime now makes its own host
// calls through WASI 0.3.0 imports: exit-with-code, monotonic and system-clock now, a sync-lowered wait-for for
// usleep, get-random-u64, and write1 for fds 1 and 2, which writes one long-lived stream per fd through
// write-via-stream and flushes it at exit (writer dropped, future read, future dropped, waitable set dropped).
//
//   - p3panic, F1's `panic("x")` rebuilt: it prints the panic and its goroutine trace on stderr and exits 2.
//   - p3print: println to stderr, one 70 KiB runtime.write to stdout, exit 3 through runtime.exit.
//   - p3time: exits with a bitmask of three clock properties, which must be every one of them.
//
// Each run's exit code, stdout and stderr must equal wasmtime 49.0.3's byte for byte. The readings are the
// committed forkp3.reading's, each stream recorded as its length and sha256 (p3print's stdout is 70 KiB).
//
// Grave #1296 is the defect this found: before cf7fdf6, p3panic and p3print printed the same bytes and then
// trapped at the flush's waitable-set.drop, because a dropped stream or future end stayed in its set.
//
// NOT witnessed here: write1's wait on a BLOCKED stream.write and its loop over a partial copy. Neither engine
// returns BLOCKED or a partial copy for a stdio write (a 16 MiB write is one copy on both, measured), so a
// neuter of either cannot fail through stdout. The fork's phase4/wasip3-f3a.md records the gap.
//
// NEUTER: usleep restored to F1's trap (fork runtime, p3panic rebuilt): p3panic's run traps `unreachable`
// before printing anything, where wasmtime's reading is exit 2 with a 69-byte stderr; wasmtime traps too.
// NEUTER: F1's fictional counter clock restored (fork runtime, p3time rebuilt): p3time exits 3 (0x04 clear,
// the wall clock reads 1970) where wasmtime's reading is 7; it exits 3 on wasmtime too.
func TestForkWasip3RuntimeMatchesWasmtime(t *testing.T) {
	reading := filepath.Join(forkP3Dir, "forkp3.reading")
	for _, c := range []string{"p3panic", "p3print", "p3time"} {
		t.Run(c, func(t *testing.T) {
			want := readingLine(t, reading, "wasmtime 49.0.3 on "+c+", 10 of 10")
			wantCode, err := strconv.Atoi(strings.TrimPrefix(want, "exit "))
			if !strings.HasPrefix(want, "exit ") || err != nil {
				t.Fatalf("forkp3.reading's %s line %q is not an exit code", c, want)
			}
			if c == "p3time" && wantCode != p3timeAllProperties {
				t.Fatalf("wasmtime's reading is exit %d (%03b), not every property (%03b): the oracle itself saw a clock fail",
					wantCode, wantCode, p3timeAllProperties)
			}
			wantOut := readingStream(t, reading, "wasmtime 49.0.3 on "+c+" stdout")
			wantErr := readingStream(t, reading, "wasmtime 49.0.3 on "+c+" stderr")

			var stdout, stderr strings.Builder
			in, err := InstantiateWithHost(readForkP3(t, c), NewHost(&stdout, &stderr, nil))
			if err != nil {
				t.Fatalf("%s did not instantiate: %v", c, err)
			}
			defer in.Close()
			code, err := in.CallRun()
			if err != nil {
				t.Fatalf("%s's run failed where wasmtime exits %d: %v (stderr so far %q)", c, wantCode, err, stderr.String())
			}
			if code != wantCode {
				t.Errorf("%s exited %d, want wasmtime's %d", c, code, wantCode)
			}
			if got := streamDigest(stdout.String()); got != wantOut {
				t.Errorf("%s's stdout is %s, want wasmtime's %s", c, got, wantOut)
			}
			if got := streamDigest(stderr.String()); got != wantErr {
				t.Errorf("%s's stderr is %s (%q), want wasmtime's %s", c, got, stderr.String(), wantErr)
			}
		})
	}
}

// p3c1Properties is the number of stdout lines after p3c1's args line, each a property that prints ": true" when it
// holds: the monotonic delta, the year, the sleep, crypto/rand and the unset X.
const p3c1Properties = 5

// TestForkWasip3C1MatchesWasmtime is checkpoint C1 (#1241), delivered by F3b (#1295, ADR 0114). p3c1 is an ordinary
// Go program, fmt, os, time and crypto/rand with no linkname, built by the fork for GOOS=wasip3. Its argv and
// environment come from get-arguments and get-environment, which the fork's syscall.init calls and pushes into the
// runtime; its clocks are monotonic and system-clock now; time.Sleep waits in the runtime's netpoll on wait-for;
// crypto/rand reads get-random-bytes through syscall.RandomGet; and os.Stdout and os.Stderr write through the
// runtime's one write-via-stream stream per fd. It prints properties, never raw values, interleaves the runtime's
// println with os.Stderr, and exits 7.
//
// Run with the argv the committed reading records, its exit code, stdout and stderr must equal wasmtime 49.0.3's
// byte for byte. Its stdout is also checked to be every property holding, with the args line naming the reading's
// argv, so a reading in which wasmtime itself saw a property fail cannot pass as an agreement.
//
// NEUTER: argv not pushed (the fork's syscall.init passes nil args to the runtime; p3c1 rebuilt): os.Args is empty,
// so os.Args[1:] panics and the run exits 2 with no stdout, where wasmtime's reading is exit 7; it exits 2 on wasmtime
// too.
// NEUTER: RandomGet fills zeros (fork syscall; p3c1 rebuilt): the stdout digest differs and the property check names
// the rand line, false; wasmtime prints the same false line.
// NEUTER: netpoll(delay) returns at once (fork runtime; p3c1 rebuilt): SURVIVES. findRunnable spins to the timer's
// deadline, so the sleep still lasts 10 ms and every byte matches; only host CPU differs. The netpoll path is
// witnessed by a second neuter, any blocking netpoll restored to F3a's trap: the run traps `unreachable` after three
// stdout lines, where wasmtime's reading is exit 7.
// NEUTER: fds 1 and 2 written through a second stream each, opened in the fork's syscall.Write and never flushed
// (p3c1 rebuilt): SURVIVES. Both engines write every completed stream.write through at once and lose nothing left
// unflushed at exit, so one stream per fd is built and not witnessed here, as F3a's wait and partial-copy loop are
// not (the fork's phase4/wasip3-f3b.md).
func TestForkWasip3C1MatchesWasmtime(t *testing.T) {
	reading := filepath.Join(forkP3Dir, "forkp3.reading")
	argv := strings.Fields(readingLine(t, reading, "p3c1 argv"))
	if len(argv) < 2 {
		t.Fatalf("forkp3.reading's p3c1 argv is %q, want the program name and at least one argument", argv)
	}
	want := readingLine(t, reading, "wasmtime 49.0.3 on p3c1, 10 of 10")
	wantCode, err := strconv.Atoi(strings.TrimPrefix(want, "exit "))
	if !strings.HasPrefix(want, "exit ") || err != nil {
		t.Fatalf("forkp3.reading's p3c1 line %q is not an exit code", want)
	}
	wantOut := readingStream(t, reading, "wasmtime 49.0.3 on p3c1 stdout")
	wantErr := readingStream(t, reading, "wasmtime 49.0.3 on p3c1 stderr")

	var stdout, stderr strings.Builder
	h := NewHost(&stdout, &stderr, nil)
	h.Args = argv
	in, err := InstantiateWithHost(readForkP3(t, "p3c1"), h)
	if err != nil {
		t.Fatalf("p3c1 did not instantiate: %v", err)
	}
	defer in.Close()
	code, err := in.CallRun()
	if err != nil {
		t.Fatalf("p3c1's run failed where wasmtime exits %d: %v (stdout so far %q, stderr %q)",
			wantCode, err, stdout.String(), stderr.String())
	}
	if code != wantCode {
		t.Errorf("p3c1 exited %d, want wasmtime's %d (stderr %q)", code, wantCode, stderr.String())
	}
	if got := streamDigest(stdout.String()); got != wantOut {
		t.Errorf("p3c1's stdout is %s (%q), want wasmtime's %s", got, stdout.String(), wantOut)
	}
	if got := streamDigest(stderr.String()); got != wantErr {
		t.Errorf("p3c1's stderr is %s (%q), want wasmtime's %s", got, stderr.String(), wantErr)
	}

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 1+p3c1Properties {
		t.Fatalf("p3c1 printed %d stdout lines, want the args line and %d properties: %q", len(lines), p3c1Properties, lines)
	}
	if wantArgs := "args: [" + strings.Join(argv[1:], " ") + "]"; lines[0] != wantArgs {
		t.Errorf("p3c1's args line is %q, want %q, the reading's argv", lines[0], wantArgs)
	}
	for _, ln := range lines[1:] {
		if !strings.HasSuffix(ln, ": true") {
			t.Errorf("p3c1 printed %q: that property does not hold", ln)
		}
	}
}

// readingStream returns a stream's recorded "N bytes, sha256 H", refusing a line of any other shape so that a
// malformed reading cannot compare equal to a malformed digest.
func readingStream(t *testing.T, path, reader string) string {
	t.Helper()
	s := readingLine(t, path, reader)
	n, h, ok := strings.Cut(s, " bytes, sha256 ")
	if _, err := strconv.Atoi(n); !ok || err != nil || len(h) != 64 {
		t.Fatalf("%s's %q reading %q is not \"N bytes, sha256 H\"", path, reader, s)
	}
	return s
}

// streamDigest renders b in readingStream's shape.
func streamDigest(b string) string {
	sum := sha256.Sum256([]byte(b))
	return fmt.Sprintf("%d bytes, sha256 %s", len(b), hex.EncodeToString(sum[:]))
}

func readForkP3(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(forkP3Dir, name+".wasm"))
	if err != nil {
		t.Fatalf("%s.wasm is missing (it is committed; PROVENANCE records how it was built): %v", name, err)
	}
	return b
}
