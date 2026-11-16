// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// forkP3Dir holds the fork's components (F1, F2, F3a, F3b, F5, F4), their sources, their wasmtime readings and their PROVENANCE. The bytes
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

// forkP3HangGuard bounds one run of an F5 witness. A goroutine the fork's netpoller never readies leaves the run
// parked or spinning for good, and the guard turns that into a failure. It is a harness bound, not a measurement.
const forkP3HangGuard = 20 * time.Second

// runForkP3 runs name's `run` export with stdin under forkP3HangGuard, and returns its exit code, stdout and stderr.
func runForkP3(t *testing.T, name string, stdin io.Reader) (code int, stdout, stderr string) {
	t.Helper()
	return runForkP3Host(t, name, forkP3HangGuard, func(out, errb io.Writer) *Host { return NewHost(out, errb, stdin) })
}

// runForkP3Host is runForkP3 with the Host newHost makes over the run's stdout and stderr, under guard: F4's witnesses
// set argv and the network grant on it. The instance is closed before it returns.
func runForkP3Host(t *testing.T, name string, guard time.Duration, newHost func(out, errb io.Writer) *Host) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	in, err := InstantiateWithHost(readForkP3(t, name), newHost(&out, &errb))
	if err != nil {
		t.Fatalf("%s did not instantiate: %v", name, err)
	}
	defer in.Close()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() { c, e := in.CallRun(); done <- result{c, e} }()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("%s's run failed where wasmtime's exits: %v (stdout %q, stderr %q)", name, r.err, out.String(), errb.String())
		}
		return r.code, out.String(), errb.String()
	case <-time.After(guard):
		t.Fatalf("%s's run did not return within %v: a goroutine parked in the fork's netpoller was never readied",
			name, guard)
	}
	return 0, "", ""
}

// matchForkP3Reading compares one run with the reading recorded under key: "<key>, 10 of 10" is the exit code, and
// "<key> stdout" and "<key> stderr" the two streams' digests.
func matchForkP3Reading(t *testing.T, key string, code int, stdout, stderr string) {
	t.Helper()
	reading := filepath.Join(forkP3Dir, "forkp3.reading")
	want := readingLine(t, reading, key+", 10 of 10")
	wantCode, err := strconv.Atoi(strings.TrimPrefix(want, "exit "))
	if !strings.HasPrefix(want, "exit ") || err != nil {
		t.Fatalf("forkp3.reading's %q line %q is not an exit code", key, want)
	}
	if code != wantCode {
		t.Errorf("%s: exited %d, want wasmtime's %d (stderr %q)", key, code, wantCode, stderr)
	}
	if got, want := streamDigest(stdout), readingStream(t, reading, key+" stdout"); got != want {
		t.Errorf("%s: stdout is %s (%q), want wasmtime's %s", key, got, stdout, want)
	}
	if got, want := streamDigest(stderr), readingStream(t, reading, key+" stderr"); got != want {
		t.Errorf("%s: stderr is %s (%q), want wasmtime's %s", key, got, stderr, want)
	}
}

// p3pollProperties is the number of p3poll's stdout lines that print ": true" when their property holds; its one
// other line is the netpoller's order.
const p3pollProperties = 10

// TestForkWasip3PollMatchesWasmtime is F5's witness (#1240, ADR 0114). The fork's runtime has a single-M netpoller over
// one waitable set: a stream operation the host has not finished is armed by its handle, its goroutine parks alone, and
// netpoll(0) drains the set with waitable-set.poll, netpoll(-1) waits on it, and netpoll(delay) joins an async-lowered
// wait-for timer beside the armed operations. p3poll is an ordinary Go program on it: os.Pipe is a guest-to-guest
// stream<u8>, so a reader parks while a sleeper runs, a writer parks until its reader reads, two readers become ready
// together, a read abandoned at its deadline is collected by the next one, and a read end closed mid-read cancels the
// read before the drop.
//
// Its exit code, stdout and stderr must equal wasmtime 49.0.3's byte for byte (forkp3.reading). Its stdout is also
// checked to be every property holding, so a reading in which wasmtime itself saw one fail cannot pass as an agreement.
// The order line, "readers ran: 2:two 1:one", is the drain's: one poll takes both readers' events, and the runtime runs
// the last one readied first.
//
// NEUTER: netpoll returns at once with nothing (fork runtime; p3poll rebuilt): the run never returns, and this test
// fails at forkP3HangGuard; wasmtime hangs too.
// NEUTER: the waitable-set.poll drain removed (fork runtime; p3poll rebuilt): the stdout digest differs, the order line
// reading "readers ran: 1:one 2:two", since each wait readies one reader; wasmtime prints the same line.
// NEUTER: netpoll(-1) restored to F3b's trap (fork runtime; p3poll rebuilt): SURVIVES. Every wait p3poll makes has a
// timer pending or finds its event already ready; TestForkWasip3StdinMatchesWasmtime witnesses the unbounded wait.
// NEUTER: Close drops a read end without cancelling its read (fork syscall; p3poll rebuilt): the run traps at the drop,
// "stream.drop-readable: end 17 is mid-copy"; wasmtime refuses it too, "cannot remove busy stream".
func TestForkWasip3PollMatchesWasmtime(t *testing.T) {
	code, stdout, stderr := runForkP3(t, "p3poll", strings.NewReader(""))
	matchForkP3Reading(t, "wasmtime 49.0.3 on p3poll", code, stdout, stderr)

	var props int
	for _, ln := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		switch {
		case strings.HasPrefix(ln, "readers ran: "):
		case strings.HasSuffix(ln, ": true"):
			props++
		default:
			t.Errorf("p3poll printed %q: that property does not hold", ln)
		}
	}
	if props != p3pollProperties {
		t.Errorf("p3poll printed %d holding properties, want %d", props, p3pollProperties)
	}
}

// TestForkWasip3StdinMatchesWasmtime is F5's stdin witness (#1240). os.Stdin on the fork's GOOS=wasip3 is
// read-via-stream, registered with the netpoller because fd 0 reports FDFLAG_NONBLOCK, and read in the completion
// model: each Read is a real async stream.read into a buffer the fd owns, armed when it returns BLOCKED, and the
// retry collects it; at DROPPED the future is read, and ok is the end of input. p3stdin reads all of it and prints
// p3cat's four facts. Its first read waits with no timer pending (netpoll(-1)); then a goroutine sleeps an hour, so
// every later read waits with a timer in the set, which is cancelled when stdin's event comes first.
//
// It is fed what wasmtime was fed: p3cat's committed stdin, the same bytes through an io.Pipe in 1000-byte pieces (so
// every host Read is short, read against the same reading), and an empty reader for /dev/null. Each run's exit code,
// stdout and stderr must equal wasmtime 49.0.3's (forkp3.reading), and its stdout must also be p3cat's on the same
// stdin, a Rust std program's on wasmtime (p3cat.reading, p3cat-empty.reading), with p3stdin's name.
//
// NEUTER: netpoll returns at once with nothing (fork runtime; p3stdin rebuilt): every case fails at forkP3HangGuard;
// wasmtime hangs too.
// NEUTER: netpoll(-1) restored to F3b's trap (fork runtime; p3stdin rebuilt): every case's run fails, trap
// `unreachable` at the first read; wasmtime traps in runtime.netpoll.
// NEUTER: stdin's Read answers EBADF, F3b's (fork syscall; p3stdin rebuilt): every case exits 1 with "read /dev/stdin:
// Bad file number" on stderr; wasmtime the same.
// NEUTER: the waitable-set.poll drain removed: SURVIVES. stdin's events come one at a time, so a drain never finds a
// second; TestForkWasip3PollMatchesWasmtime witnesses the drain.
func TestForkWasip3StdinMatchesWasmtime(t *testing.T) {
	stdin, err := os.ReadFile("testdata/p3cat.stdin")
	if err != nil {
		t.Fatalf("p3cat's stdin is missing (it is committed): %v", err)
	}
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
		name, key, p3cat string
		stdin            func() io.Reader
	}{
		{
			"p3cat.stdin", "wasmtime 49.0.3 on p3stdin < p3cat.stdin", "testdata/p3cat.reading",
			func() io.Reader { return bytes.NewReader(stdin) },
		},
		{"p3cat.stdin-piped", "wasmtime 49.0.3 on p3stdin < p3cat.stdin", "testdata/p3cat.reading", piped},
		{
			"empty", "wasmtime 49.0.3 on p3stdin < /dev/null", "testdata/p3cat-empty.reading",
			func() io.Reader { return strings.NewReader("") },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runForkP3(t, "p3stdin", c.stdin())
			matchForkP3Reading(t, c.key, code, stdout, stderr)
			p3cat, _ := readP3CatReading(t, c.p3cat)
			if want := strings.ReplaceAll(p3cat, "p3cat: ", "p3stdin: "); stdout != want {
				t.Errorf("p3stdin's stdout is %q, want p3cat's on the same bytes (%s), %q", stdout, c.p3cat, want)
			}
		})
	}
}

// p3netProperties is the number of p3net's granted stdout lines that print ": true" when their property holds; its one
// other line is the HTTP response, which must be p3netHTTPLine.
const p3netProperties = 7

// p3netHTTPLine is the line p3net prints for its HTTP request: the status, the body, the content length and the read.
const p3netHTTPLine = `http: 200 OK, body "hello, wasip3\n", length 14, read ok: true`

// p3netHangGuard bounds one p3net run, which interprets 1 MiB through an echo, its sha256 and an HTTP exchange in an
// 11 MB module, where forkP3HangGuard bounds F5's small ones. A harness bound, not a measurement.
const p3netHangGuard = 300 * time.Second

// TestForkWasip3NetMatchesWasmtime is F4's witness (#1239, ADR 0114). Go's net on the fork's GOOS=wasip3 takes its stock
// POSIX path over wasi:sockets' tcp-socket: Socket makes no host socket until Bind or Connect creates one, Connect is
// the async lower whose subtask is armed for the fd's pollDesc so net's own connect loop waits on it, Accept reads one
// element of listen's stream<tcp-socket>, and a connection's Read and Write are F5's completion-model stream halves
// over receive's stream and the one handed to send. p3net is an ordinary Go program on it, with no peer outside the
// instance: it listens, echoes 1 MiB through io.Copy and reads it back after CloseWrite, times a read out at 5 ms and
// then reads the late bytes, dials a closed port, closes a listener under a parked Accept, and serves and gets one
// HTTP request. It prints properties, never a port.
//
// Both gate states, read as p3tcp's are (TestP3TcpMatchesWasmtime):
//   - **granted** (InheritNetwork, wasmtime's -S inherit-network): every property holds and the HTTP line is the one
//     the handler wrote;
//   - **denied** (the zero Host, wasmtime with no network flag): the listen and a dial are each refused EACCES, which
//     is create's access-denied.
//
// Each run's exit code, stdout and stderr must equal wasmtime 49.0.3's byte for byte (forkp3.reading), and after Close
// no socket goroutine is left running.
//
// NEUTER: Connect returns success without waiting (fork syscall; p3net rebuilt): the echo's sha256 line is false on
// both engines, and the run then fails differently on each (wasmtime panics on a nil LocalAddr, exit 2; Burroughs
// exits 1 at the HTTP request, "Socket is not connected"), where wasmtime's reading is exit 0 with every property.
// NEUTER: netpollopen's re-bind of an operation armed before its pollDesc removed (fork runtime; p3net rebuilt): the
// first dial never returns, on both engines, after the listen line; this test fails at p3netHangGuard.
// NEUTER: Close drops a listen stream with its accept read in flight, without the cancel (fork syscall; p3net
// rebuilt): the run traps at the parked Accept's Close, "stream.drop-readable: end 32 is mid-copy"; wasmtime refuses
// it too, "cannot remove busy stream".
func TestForkWasip3NetMatchesWasmtime(t *testing.T) {
	for _, c := range []struct {
		name, key string
		grant     bool
	}{
		{"granted", "wasmtime 49.0.3 on p3net", true},
		{"denied", "wasmtime 49.0.3 on p3net, denied", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runForkP3Host(t, "p3net", p3netHangGuard, func(out, errb io.Writer) *Host {
				h := NewHost(out, errb, nil)
				h.InheritNetwork = c.grant
				return h
			})
			matchForkP3Reading(t, c.key, code, stdout, stderr)
			requireNoSocketGoroutines(t)

			lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
			if !c.grant {
				if want := []string{"listen refused, EACCES: true", "dial refused, EACCES: true"}; !slices.Equal(lines, want) {
					t.Errorf("p3net's denied stdout is %q, want %q", lines, want)
				}
				return
			}
			var props int
			for _, ln := range lines {
				switch {
				case ln == p3netHTTPLine:
				case strings.HasSuffix(ln, ": true"):
					props++
				default:
					t.Errorf("p3net printed %q: that property does not hold", ln)
				}
			}
			if props != p3netProperties || lines[len(lines)-1] != p3netHTTPLine {
				t.Errorf("p3net printed %d holding properties and last line %q, want %d and %q",
					props, lines[len(lines)-1], p3netProperties, p3netHTTPLine)
			}
		})
	}
}

// TestForkWasip3NetTailMatchesWasmtime is F4's measurement of a server's tail (#1239), the question C2's (#1242) /quit
// asks. p3nettail listens on the port its argv names, serves one connection from a peer outside the instance, writes
// 256 KiB, closes the connection and exits. Every Write returns once the host has taken the bytes, so what the peer
// counts is what the host did with them after the guest closed and the instance was closed: here the write-behind is
// Burroughs' 64 KiB send sink, which ADR 0123 (choices 8 and 12) drains at the writer's drop and discards at
// walker.close. The peer's count must equal wasmtime's (forkp3.reading), as must the exit code and both streams.
func TestForkWasip3NetTailMatchesWasmtime(t *testing.T) {
	reading := filepath.Join(forkP3Dir, "forkp3.reading")
	want := readingLine(t, reading, "wasmtime 49.0.3 on p3nettail, the peer received")

	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	_ = free.Close()

	type count struct {
		n   int64
		err error
	}
	got := make(chan count, 1)
	go func() {
		deadline := time.Now().Add(forkP3HangGuard) // a harness hang guard, not a measurement
		for {
			c, err := net.Dial("tcp4", "127.0.0.1:"+strconv.Itoa(port))
			if err == nil {
				n, rerr := io.Copy(io.Discard, c)
				_ = c.Close()
				got <- count{n, rerr}
				return
			}
			if time.Now().After(deadline) {
				got <- count{0, err}
				return
			}
			time.Sleep(10 * time.Millisecond) // the guest has not listened yet: poll, as a peer outside it must
		}
	}()
	code, stdout, stderr := runForkP3Host(t, "p3nettail", forkP3HangGuard, func(out, errb io.Writer) *Host {
		h := NewHost(out, errb, nil)
		h.Args = []string{"p3nettail.wasm", strconv.Itoa(port)}
		h.InheritNetwork = true
		return h
	})
	matchForkP3Reading(t, "wasmtime 49.0.3 on p3nettail", code, stdout, stderr)
	select {
	case c := <-got:
		if c.err != nil {
			t.Fatalf("the peer's read ended in %v after %d bytes", c.err, c.n)
		}
		if g := fmt.Sprintf("%d bytes", c.n); g != want {
			t.Errorf("the peer received %s from Burroughs, want wasmtime's %s", g, want)
		}
	case <-time.After(forkP3HangGuard):
		t.Fatal("the peer's read did not end after the instance was closed")
	}
	requireNoSocketGoroutines(t)
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
