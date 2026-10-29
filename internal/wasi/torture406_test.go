// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// #406's Go-runtime torture suite, black-box (the chair's fifth ruling of 2026-10-28, item 1).
//
// Each case is one Go program built twice from one source: with stock Go natively, which computes the expected
// result, and with the Phase 4 fork, run here through `Run`, the function behind the public `WASIP1Config.Run`
// (the same door v0.12.0's witness uses). A case passes when the fork guest's observable result equals stock Go's:
// its class, exit code, first stderr line and stdout. **Nothing is read in the fork's runtime.** The observables are
// what the guest prints and how it ends. The fixtures, their provenance and how each expectation was derived are in
// testdata/torture406/PROVENANCE.
//
// # Why each run is a child process
//
// A case can hang, and a hang has to be a verdict rather than a stuck test binary: `Run` has no cancellation, so a
// spinning guest would hold the test forever. Each run is this test binary re-executed in helper mode
// (`TestTorture406Helper`), and the parent kills it at the case's wall-clock bound and records **hung**.

// t406Class is how a run ended, which is what the classifiers decide.
type t406Class string

const (
	t406Completed t406Class = "completed" // exit 0
	t406Deadlock  t406Class = "deadlock"  // exit 2 with the runtime's "all goroutines are asleep" fatal
	t406Crashed   t406Class = "crashed"   // exit 2 with a panic
	t406Hung      t406Class = "hung"      // killed at the wall-clock bound
	t406Other     t406Class = "other"     // anything else, which no expectation names
)

const t406DeadlockFatal = "fatal error: all goroutines are asleep - deadlock!"

// t406Result is one run's observables.
type t406Result struct {
	class   t406Class
	exit    int
	stderr1 string // the first line of stderr; the rest is goroutine traces, which differ between native and wasm
	stdout  string
	stderr  string // all of it, for a failure's message
	gauge   int    // the most agents sampled executing guest code at once (fork runs only), or -1
}

func t406Classify(exit int, timedOut bool, stderr1 string) t406Class {
	switch {
	case timedOut:
		return t406Hung
	case exit == 0:
		return t406Completed
	case exit == 2 && stderr1 == t406DeadlockFatal:
		return t406Deadlock
	case exit == 2 && strings.HasPrefix(stderr1, "panic:"):
		return t406Crashed
	}
	return t406Other
}

// t406Case is a row of the suite.
type t406Case struct {
	name  string
	floor int // agents that must be sampled executing at once; 0 for no floor

	// pinned names an issue whose known limit makes the fork diverge from stock Go on this case, and the class
	// the fork currently produces. The case asserts the divergence, so a fix turns it red and says to retire the
	// pin rather than letting a silently-passing case go unnoticed. Empty for a case that must match stock Go.
	pinned      string
	pinnedClass t406Class

	// reportOnly names the issue under which a pinned case's departure is **logged rather than failed**: a named
	// suppression, cited here and at the arm that honours it. Empty for a pin that gates.
	//
	// c1stw's is #1172. It completed twice on CI's macos-15 runner while main's runs and every local run hang, and
	// no black-box observable separated "the limit lifted" from "the run never loaded the runtime": a start barrier
	// changed nothing, a delayed agent start still hangs, and the executing-agents gauge reads 4 even for workers
	// that return at once. Every other case still gates. #1172 says what ends the suppression.
	reportOnly string
}

var t406Cases = []t406Case{
	// Case 1: stop-the-world under load. c1alloc's workers allocate, which is where the fork's runtime can stop
	// them, and the case requires several agents executing at once, as v0.12.0's witness does (its floor's
	// derivation is at threads012ExecutingFloor).
	{name: "c1alloc", floor: threads012ExecutingFloor},
	// c1stw's workers spin without allocating. On the fork they are never preempted, so the collections never
	// complete: #1064's known limit, recorded on #406 with the variants that characterize it.
	{name: "c1stw", pinned: "#1064", pinnedClass: t406Hung, reportOnly: "#1172"},
	// Case 2: checkdead in both directions. 2a must be reported; 2b, with a sleeper that wakes the rest, must not.
	{name: "c2a"},
	{name: "c2b"},
	// Case 3: the inverses. 3a parks on a futex-backed sync.Cond a sleeper will signal; 3b is a deadlock that a
	// pending timer cannot resolve.
	{name: "c3a"},
	{name: "c3b"},
	// #1157's reproducer: four workers pinned with LockOSThread that sleep, so a locked M hands its P on. The fork at
	// 944cfd2 threw "on a locked thread with no template thread"; this guest is built from the fork commit that
	// repairs it (PROVENANCE), so it must match stock Go's "done".
	{name: "c4lock"},
	// Case 4's positive controls, one per classifier that no case above exercises: crashed and hung. Completed and
	// deadlock are exercised by c2b and c2a; wrong output has its own test below.
	{name: "ctlcrash"},
	{name: "ctlhung"},
}

const t406Dir = "testdata/torture406"

// t406Bound is how long a fork run may take before it is hung. A hung control or a pinned hang is decided at the
// short bound, so the suite does not wait out the long one on a case expected to hang.
func t406Bound(c t406Case) time.Duration {
	if c.name == "ctlhung" || c.pinnedClass == t406Hung {
		return 10 * time.Second * raceSlowdown
	}
	return 60 * time.Second * raceSlowdown
}

func t406ReadExpect(t *testing.T, name string) t406Result {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(t406Dir, name+".expect"))
	if err != nil {
		t.Fatalf("%s: no expectation, so this case has no subject: %v", name, err)
	}
	head, stdout, ok := strings.Cut(string(b), "stdout:\n")
	if !ok {
		t.Fatalf("%s.expect has no stdout: section", name)
	}
	var r t406Result
	for _, line := range strings.Split(strings.TrimRight(head, "\n"), "\n") {
		k, v, _ := strings.Cut(line, " ")
		switch k {
		case "class":
			r.class = t406Class(v)
		case "exit":
			r.exit, err = strconv.Atoi(v)
			if err != nil {
				t.Fatalf("%s.expect: exit %q: %v", name, v, err)
			}
		case "stderr1":
			r.stderr1 = v
		}
	}
	r.stdout = stdout
	return r
}

// t406Mismatch is the comparison every case and the wrong-output control share: a non-empty string names what
// differs.
func t406Mismatch(got, want t406Result) string {
	var d []string
	if got.class != want.class {
		d = append(d, fmt.Sprintf("class %s, want %s", got.class, want.class))
	}
	if want.class != t406Hung && got.exit != want.exit {
		d = append(d, fmt.Sprintf("exit %d, want %d", got.exit, want.exit))
	}
	if got.stderr1 != want.stderr1 {
		d = append(d, fmt.Sprintf("first stderr line %q, want %q", got.stderr1, want.stderr1))
	}
	if got.stdout != want.stdout {
		d = append(d, fmt.Sprintf("stdout %q, want %q", got.stdout, want.stdout))
	}
	return strings.Join(d, "; ")
}

func t406Wasm(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join(t406Dir, name+".wasm.gz"))
	if err != nil {
		t.Fatalf("%s: the committed fork-built guest is missing: %v", name, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b
}

// t406Run runs one command to completion or to its bound, and reads its observables.
func t406Run(cmd *exec.Cmd, bound time.Duration) (t406Result, error) {
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		return t406Result{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	select {
	case <-done:
	case <-time.After(bound):
		timedOut = true
		_ = cmd.Process.Kill()
		<-done
	}
	exit := cmd.ProcessState.ExitCode()
	stderr1, _, _ := strings.Cut(errb.String(), "\n")
	return t406Result{
		class: t406Classify(exit, timedOut, stderr1), exit: exit, stderr1: stderr1,
		stdout: out.String(), stderr: errb.String(), gauge: -1,
	}, nil
}

// t406RunFork runs a case's fork-built guest through Run, in a child process (see the file comment).
func t406RunFork(t *testing.T, c t406Case) t406Result {
	t.Helper()
	gauge := filepath.Join(t.TempDir(), "gauge")
	cmd := exec.Command(os.Args[0], "-test.run=^TestTorture406Helper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "BURROUGHS_T406_CASE="+c.name, "BURROUGHS_T406_GAUGE="+gauge)
	r, err := t406Run(cmd, t406Bound(c))
	if err != nil {
		t.Fatalf("%s: starting the helper: %v", c.name, err)
	}
	if strings.HasPrefix(r.stderr1, "T406-HELPER:") {
		t.Fatalf("%s: the helper failed before the guest ran: %s", c.name, r.stderr)
	}
	if b, err := os.ReadFile(gauge); err == nil {
		r.gauge, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	return r
}

// TestTorture406Helper is the child half of the fork runs. It does nothing unless BURROUGHS_T406_CASE is set, which
// only t406RunFork sets: it then runs that guest through Run with this process's streams, samples the executing-
// agents gauge, and exits with the guest's own code, so the parent sees exactly what the guest did.
//
// Not a skip: with the variable unset, there is no case to run, and the test passes having asserted nothing, which is
// its whole job outside a child. (A t.Skip here would be a skip site to license; this is not one.)
func TestTorture406Helper(t *testing.T) {
	name := os.Getenv("BURROUGHS_T406_CASE")
	if name == "" {
		return
	}
	wasm := t406Wasm(t, name)
	maxExec := 0
	stop := make(chan struct{})
	sampled := make(chan int, 1)
	beforeStartForTest = func(in *interp.Instance) {
		go func() {
			m := 0
			tick := time.NewTicker(200 * time.Microsecond)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					sampled <- m
					return
				case <-tick.C:
					m = max(m, in.ExecutingAgents())
				}
			}
		}()
	}
	afterStartForTest = func(*interp.Instance) {
		close(stop)
		maxExec = <-sampled
	}
	code, err := Run(Config{Wasm: wasm, Args: []string{name}, Stdout: os.Stdout, Stderr: os.Stderr})
	if g := os.Getenv("BURROUGHS_T406_GAUGE"); g != "" {
		_ = os.WriteFile(g, []byte(strconv.Itoa(maxExec)), 0o600)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "T406-HELPER: Run: %v\n", err)
		os.Exit(98)
	}
	os.Exit(code)
}

// TestGoRuntimeTortureSuite runs every case's fork-built guest and compares it with stock Go's result.
//
// **Neuters, run** (2026-10-28, darwin/arm64), each watched failing before the suite was trusted:
//   - c1alloc rebuilt with GOMAXPROCS = 1 **hangs** (predicted: below the floor; observed: worse). With one P, an
//     allocating worker holds it and main never runs to request the collections, because allocation only yields
//     once a stop has been asked for. It is #1064's non-preemption again, from the other side;
//   - c2a with a goroutine that eventually sends classifies completed, not deadlock;
//   - c2b with its sleeper removed classifies deadlock, not completed;
//   - c1stw's pin: with the pinned class set to completed, the case fails as hung. (Run before #1172 made c1stw
//     report-only. Since then a departure is logged, not failed, which was watched with a guest whose workers
//     return at once: it logs "REPORTED, NOT FAILED (#1172)" and passes.)
func TestGoRuntimeTortureSuite(t *testing.T) {
	for _, c := range t406Cases {
		t.Run(c.name, func(t *testing.T) {
			want := t406ReadExpect(t, c.name)
			got := t406RunFork(t, c)
			if c.pinned != "" {
				if got.class != c.pinnedClass && c.reportOnly != "" {
					t406Report(t, fmt.Sprintf("REPORTED, NOT FAILED (%s): %s departed from its pin, %s not %s "+
						"(exit %d, stdout %q, %d agents sampled executing). Record this run on %s.",
						c.reportOnly, c.name, got.class, c.pinnedClass, got.exit, got.stdout, got.gauge, c.reportOnly))
					return
				}
				if got.class != c.pinnedClass {
					t.Errorf("%s is pinned as %s by %s's known limit, but the fork now gives %s (exit %d, stdout %q), "+
						"with %d agents sampled executing at once. If it now matches stock Go (%s), the limit is lifted: "+
						"retire the pin and record it on %s",
						c.name, c.pinnedClass, c.pinned, got.class, got.exit, got.stdout, got.gauge, want.class, c.pinned)
				}
				return
			}
			if d := t406Mismatch(got, want); d != "" {
				t.Errorf("%s differs from stock Go: %s\nstderr:\n%s", c.name, d, got.stderr)
			}
			if c.floor > 0 && got.gauge < c.floor {
				t.Errorf("%s: at most %d agents executed guest code at once, below %d: the load did not run in "+
					"parallel while the collections happened", c.name, got.gauge, c.floor)
			}
		})
	}
}

// TestGoRuntimeTortureCatchesAWrongResult is the wrong-output classifier's positive control: the comparison the
// suite uses must report a run whose stdout differs from its expectation by one character.
func TestGoRuntimeTortureCatchesAWrongResult(t *testing.T) {
	c := t406Case{name: "c2b"}
	got := t406RunFork(t, c)
	want := t406ReadExpect(t, c.name)
	if d := t406Mismatch(got, want); d != "" {
		t.Fatalf("the control's own run does not match its expectation, so it cannot show the comparison: %s", d)
	}
	wrong := want
	wrong.stdout = strings.Replace(want.stdout, "42", "43", 1)
	if wrong.stdout == want.stdout {
		t.Fatal("the altered expectation equals the real one, so this control asks nothing")
	}
	if t406Mismatch(got, wrong) == "" {
		t.Error("the comparison accepted a run whose stdout differs from the expectation: it cannot catch a wrong result")
	}
}

// TestGoRuntimeTortureExpectationsAreStockGos re-derives every .expect from stock Go: each guest's source is built
// natively with the host toolchain and run under the case's bound, and its result must equal the committed
// expectation. So an expectation cannot drift from the toolchain it claims, and none of them is typed by hand except
// ctlhung's, whose "hung" this test confirms too.
func TestGoRuntimeTortureExpectationsAreStockGos(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("no go toolchain on PATH, so stock Go cannot compute the expectations: %v", err)
	}
	for _, c := range t406Cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src, err := os.ReadFile(filepath.Join(t406Dir, c.name+".go.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "main.go"), src, 0o600); err != nil {
				t.Fatal(err)
			}
			mod := fmt.Sprintf("module %s\n\ngo 1.26\n", c.name)
			if err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o600); err != nil {
				t.Fatal(err)
			}
			// `.exe` on Windows, where exec does not find a binary without it: the first Windows run reported
			// every case as "executable file not found" (#1164's Windows lane).
			bin := filepath.Join(dir, "native")
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			build := exec.Command(goBin, "build", "-o", bin, ".")
			build.Dir = dir
			build.Env = append(os.Environ(), "GOOS=", "GOARCH=", "GOFLAGS=")
			if out, berr := build.CombinedOutput(); berr != nil {
				t.Fatalf("building %s natively: %v\n%s", c.name, berr, out)
			}
			// Native runs are fast; the short bound is enough for the hung control to be decided.
			got, err := t406Run(exec.Command(bin), 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if d := t406Mismatch(got, t406ReadExpect(t, c.name)); d != "" {
				t.Errorf("stock Go's result for %s differs from %s.expect: %s", c.name, c.name, d)
			}
		})
	}
}

// TestGoRuntimeTortureStress is the 10,000-iteration stress, as #1111 ran its: every case that must match stock Go,
// N times, with the failing run's full stderr on any mismatch. It runs only when BURROUGHS_T406_ITERS is set,
// because N runs of a fork guest are hours, not a gate: it goes to janus through pueue `measured`, not `make ci`.
// Like the helper, it returns rather than skips when unset.
func TestGoRuntimeTortureStress(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("BURROUGHS_T406_ITERS"))
	if n <= 0 {
		return
	}
	for _, c := range t406Cases {
		if c.pinned != "" || strings.HasPrefix(c.name, "ctl") {
			continue
		}
		want := t406ReadExpect(t, c.name)
		start, fails := time.Now(), 0
		for i := range n {
			got := t406RunFork(t, c)
			d := t406Mismatch(got, want)
			if d == "" && c.floor > 0 && got.gauge < c.floor {
				d = fmt.Sprintf("gauge %d below floor %d", got.gauge, c.floor)
			}
			if d != "" {
				fails++
				t.Errorf("%s iteration %d of %d: %s\nstderr:\n%s", c.name, i+1, n, d, t406Head(got.stderr, 200))
			}
		}
		t.Logf("%s: %d of %d iterations failed, %v per iteration", c.name, fails, n,
			time.Since(start)/time.Duration(n))
	}
}

func t406Head(s string, lines int) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	var b strings.Builder
	for i := 0; i < lines && sc.Scan(); i++ {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	return b.String()
}

// t406Report is a report-only pin's channel: `t.Logf` for a local `-v` run, and a line appended to the GitHub
// Actions job summary when `GITHUB_STEP_SUMMARY` names one.
//
// **`t.Logf` alone reached nobody in CI.** The build job runs `go test -shuffle=on ./...` without `-v`, and a
// passing package's test output is not printed at all. So #1173's first version of this arm "logged" a departure
// into nothing, and #1172's promise to read departures at each cut could not be kept. The job summary is shown on
// the run's page whatever the step printed, so a departure is visible on every run that has one.
//
// A summary that cannot be written is the test's failure, not a silent drop, because the channel is the whole point.
func t406Report(t *testing.T, msg string) {
	t.Helper()
	t.Log(msg)
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("the job summary %s cannot be opened, so this report would reach nobody: %v", path, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "- **%s**\n", msg); err != nil {
		t.Fatalf("writing the job summary %s: %v", path, err)
	}
}

// TestAReportOnlyDepartureReachesTheJobSummary is t406Report's witness: with GITHUB_STEP_SUMMARY pointing at a file,
// the report lands in it.
//
// NEUTER, run (2026-10-29, darwin/arm64): with t406Report reduced to its `t.Log`, the file stays empty and this fails.
func TestAReportOnlyDepartureReachesTheJobSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", path)
	t406Report(t, "REPORTED, NOT FAILED (#1172): a witness line")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the summary was never written: %v", err)
	}
	if !strings.Contains(string(b), "REPORTED, NOT FAILED (#1172): a witness line") {
		t.Errorf("the summary holds %q, without the report", b)
	}
}
