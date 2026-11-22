// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The hang harness (#1303, ADR 0128) runs a guest whose wait nothing can wake in a child process, and reports
// whether the child was still alive a period after it started the guest.
//
// # Why a child
//
// The defect it witnesses is Go's runtime fatal, *"all goroutines are asleep - deadlock!"*, which ends the whole
// process. In the test binary it would end every test. It would not fire at all, either, because `go test`'s
// -test.timeout alarm is a timer, and Go's deadlock detector reports nothing while any timer is pending. So the
// child is this test binary re-executed with **-test.timeout=0**, which arms no alarm.
//
// # Why GOMAXPROCS=1
//
// Go's crypto/rand arms a one-minute warning timer on its first read in a process and stops it when the read
// returns (crypto/internal/sysrand.Read). A stopped timer stays in the heap of the P that armed it until that P
// next checks its timers, and the detector counts it. A component host reads crypto/rand when it is made
// (`newInsecureSeed`), so with more than one P the detector stays quiet for that minute: Burroughs at d8076a1
// died 60 s after reaching an unwakeable wait, at GOMAXPROCS=2 and at this machine's default alike. With one P,
// the P that goes idle is the one holding the stopped timer, and it removes the timer first, so the same build
// died as soon as it reached the wait. A core module reads no crypto/rand, and died at once either way.
//
// So a child at GOMAXPROCS=1 dies at the wait if it is going to die at all, and a period of a few seconds
// discriminates. At the default GOMAXPROCS a period under a minute would not.
//
// # How a hang is ended, and how its place is read
//
// A child still alive at the period is sent SIGQUIT. Go's default handling of SIGQUIT prints every goroutine's
// stack and exits, so the hang's stderr names the frame the guest was parked in. The tests assert that frame,
// because "still alive" alone is also what a child that never reached the wait would look like. Windows has no
// SIGQUIT, so there the child is killed and only its being alive is read.
//
// The timed actions are `time.AfterFunc` callbacks and the harness waits on `cmd.Wait` in its own goroutine, so
// it starts no goroutine of its own.

// HangChildEnv names the case a child was started to run. It is set only in a child.
const HangChildEnv = "BURROUGHS_HANG_CHILD"

// HangMark is the line a child prints on stdout immediately before it starts the guest. The period is measured
// from it, so the time the child takes to start is not counted.
const HangMark = "burroughs-hang-child: starting the guest"

// HangChildCase is the case this process was started to run as a child, or "" when it is not a child.
func HangChildCase() string { return os.Getenv(HangChildEnv) }

// HangResult is what a child did.
type HangResult struct {
	// Exited is true when the child ended by itself within the period. Code and After are then its exit
	// status and the time from HangMark to its exit.
	Exited bool
	Code   int
	After  time.Duration
	// Stdout and Stderr are everything the child wrote. When the harness ended the child, on unix, Stderr
	// holds the goroutine dump that SIGQUIT printed.
	Stdout, Stderr string
}

// Dumped reports whether Stderr holds a goroutine dump the harness's SIGQUIT printed.
func (r HangResult) Dumped() bool { return !r.Exited && runtime.GOOS != "windows" }

// FirstLine is the first line of the child's stderr, which for Go's runtime fatal is the fatal's message.
func (r HangResult) FirstLine() string {
	line, _, _ := strings.Cut(r.Stderr, "\n")
	return line
}

// hangStartBound bounds how long a child may take to print HangMark, and to end after its SIGQUIT. It is not a
// measurement: it is a backstop for a child that cannot start or will not end, far above the milliseconds
// either takes.
const hangStartBound = 60 * time.Second

// RunHangChild re-executes this test binary as a child that runs the test matching `run` with HangChildEnv set to
// `name`, at GOMAXPROCS=1, and reports what the child did within `period` of printing HangMark.
func RunHangChild(tb testing.TB, run, name string, period time.Duration) HangResult {
	tb.Helper()
	cmd := exec.Command(os.Args[0], "-test.run="+run, "-test.count=1", "-test.timeout=0")
	cmd.Env = append(os.Environ(), HangChildEnv+"="+name, "GOMAXPROCS=1", "GOTRACEBACK=all")
	w := &hangWatch{period: period}
	var stderr lockedBuffer
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Start(); err != nil {
		tb.Fatalf("%s: starting the child: %v", name, err)
	}
	w.mu.Lock()
	w.proc = cmd.Process
	w.mu.Unlock()
	backstop := time.AfterFunc(hangStartBound+period+hangStartBound, func() {
		_ = cmd.Process.Kill() //nolint:errcheck // a kill of an ended child changes nothing, and Wait reports the end
	})
	_ = cmd.Wait() //nolint:errcheck // the status is read from ProcessState, whatever Wait reports
	ended := time.Now()
	backstop.Stop()

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.quit != nil {
		w.quit.Stop()
	}
	switch {
	case w.marked.IsZero():
		tb.Fatalf("%s: the child ended before it started the guest, exit %d:\n%s",
			name, cmd.ProcessState.ExitCode(), stderr.String())
	case w.quitErr != nil:
		tb.Errorf("%s: ending the child at the period: %v", name, w.quitErr)
	}
	if !w.quitSent {
		return HangResult{
			Exited: true, Code: cmd.ProcessState.ExitCode(), After: ended.Sub(w.marked),
			Stdout: w.buf.String(), Stderr: stderr.String(),
		}
	}
	return HangResult{Stdout: w.buf.String(), Stderr: stderr.String()}
}

// hangWatch is a child's stdout. When HangMark arrives it arms the period, and at the period it ends the child.
type hangWatch struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	period   time.Duration
	proc     *os.Process
	marked   time.Time
	quit     *time.Timer
	quitSent bool
	quitErr  error
}

func (w *hangWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if w.marked.IsZero() && strings.Contains(w.buf.String(), HangMark+"\n") {
		w.marked = time.Now()
		w.quit = time.AfterFunc(w.period, w.end)
	}
	return n, err
}

// end runs at the period. A child that has already ended refuses the signal, and is then reported as having
// ended by itself.
func (w *hangWatch) end() {
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	if runtime.GOOS == "windows" {
		err = w.proc.Kill()
	} else {
		err = w.proc.Signal(syscall.SIGQUIT)
	}
	switch {
	case err == nil:
		w.quitSent = true
	case !errors.Is(err, os.ErrProcessDone):
		w.quitErr = err
	}
}

// JudgeHang compares what a child did with wasmtime's reading `want`, one row of a committed `.reading` file:
//   - `hangs` must be a child still alive at the period, with no deadlock fatal, and (where a goroutine dump was
//     taken) parked in `frame`;
//   - `exit N` must be a child that exited N by itself;
//   - anything else is a result wasmtime printed, and must be a child that exited 0 with `printed`, the same
//     result as this entry point spells it, on stdout.
func JudgeHang(tb testing.TB, name, want, frame string, got HangResult, printed string) {
	tb.Helper()
	switch {
	case want == "hangs":
		if got.Exited {
			tb.Fatalf("%s: wasmtime hangs, and the host ended %s after starting the guest, exit %d: %s",
				name, got.After.Round(time.Millisecond), got.Code, got.FirstLine())
		}
		if strings.Contains(got.Stderr, "all goroutines are asleep") {
			tb.Fatalf("%s: the child's stderr holds Go's deadlock fatal:\n%s", name, got.Stderr)
		}
		if got.Dumped() && !strings.Contains(got.Stderr, frame) {
			tb.Fatalf("%s: alive at the period, but its goroutine dump does not show it parked in %s, so the "+
				"guest may never have reached its wait:\n%s", name, frame, got.Stderr)
		}
	case strings.HasPrefix(want, "exit "):
		code, err := strconv.Atoi(strings.TrimPrefix(want, "exit "))
		if err != nil {
			tb.Fatalf("%s: reading row %q: %v", name, want, err)
		}
		if !got.Exited || got.Code != code {
			tb.Fatalf("%s: wasmtime exits %d; the child exited by itself=%v, code %d\nstderr:\n%s",
				name, code, got.Exited, got.Code, got.Stderr)
		}
	default:
		if !got.Exited || got.Code != 0 || !strings.Contains(got.Stdout, printed) {
			tb.Fatalf("%s: wasmtime prints %s; the child exited by itself=%v, code %d, and was to print %q\n"+
				"stdout:\n%s\nstderr:\n%s", name, want, got.Exited, got.Code, printed, got.Stdout, got.Stderr)
		}
	}
}

// ReadingRows reads a committed `.reading` file's `key: value` rows, skipping blank lines and `#` comments.
func ReadingRows(tb testing.TB, path string) map[string]string {
	tb.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading %s: %v", path, err)
	}
	rows := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			tb.Fatalf("%s: row %q is not `key: value`", path, line)
		}
		rows[k] = v
	}
	return rows
}

// lockedBuffer is a bytes.Buffer that exec's copying goroutine writes while the harness may read it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
