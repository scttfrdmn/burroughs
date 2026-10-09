// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDetachStopEndsTheWholeProcessGroup witnesses `detach.sh --stop`.
//
// # The specimen
//
// A `make ci` run was backgrounded and then stopped through the harness task. The harness ended the *task*;
// `golangci-lint` kept running, and the next gate died on `parallel golangci-lint is running` — a red that was
// not about the tree, and which cost a diagnosis before the cause was pinned.
//
// A **pid is not the handle.** A watcher spawns `gh`; a gate spawns `make`, `go`, and a linter. Killing the
// pid leaves the children reparented and running. The stamp file already recorded `child_pgid`, so the handle
// existed and nothing read it.
//
// # Why the subject is a GROUP and the arms are built that way
//
// The command below spawns a child and waits, so the process group has at least two members. A witness whose
// payload is a single process cannot tell "killed the pid" from "killed the group" — both pass — which is the
// same vacuity as a refusal-only check that cannot see a wrong aim. The grandchild's own liveness is what
// discriminates.
func TestDetachStopEndsTheWholeProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is required and absent: %v", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	detach := filepath.Join(root, "scripts", "detach.sh")
	if _, statErr := os.Stat(detach); statErr != nil {
		t.Fatalf("detach.sh is missing, so this witness has no subject: %v", statErr)
	}

	dir := t.TempDir()
	// **A directory name with a space in it, deliberately.** Every path below is handed to a
	// process as an argv element rather than pasted into a shell string, and this is what makes
	// that claim falsifiable on the platform the gate actually runs: a space is the cheapest input
	// that breaks the pasted form, and it breaks it on Linux and macOS too, so the gate fails
	// rather than the Windows measurement weeks later.
	dir = filepath.Join(dir, "has space")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("creating the spaced working directory: %v", mkErr)
	}
	stamp := filepath.Join(dir, "stamp")
	grandchildPID := filepath.Join(dir, "grandchild.pid")
	launchLog := filepath.Join(dir, "launch.log")

	// The payload is a FILE, not a quoted string.
	//
	// It used to be built by interpolating `grandchildPID` into nested `bash -c` quoting. That is
	// the defect this rewrite removes: on Windows those paths carry backslashes, bash consumes them
	// as escapes — measured, `C:\Users\x\scripts\detach.sh` becomes `C:Usersxscriptsdetach.sh` —
	// so the script was never found, its error went to the `>/dev/null 2>&1` below, the stamp was
	// never written, and the unbounded wait that followed hung for 9m49s until the package timed
	// out, taking every test scheduled after it. Two Windows runs, same hang, no diagnostic.
	//
	// As a file taking its output path as `$1`, nothing is parsed by a shell and the same code is
	// right on every platform.
	payload := filepath.Join(dir, "payload.sh")
	payloadBody := "#!/usr/bin/env bash\n" +
		"# $1 is where the grandchild records its own pid.\n" +
		"out=$1\n" +
		"bash -c 'echo $$ > \"$1\"; while :; do sleep 1; done' _ \"$out\" &\n" +
		"wait\n"
	if wrErr := os.WriteFile(payload, []byte(payloadBody), 0o700); wrErr != nil {
		t.Fatalf("writing the payload script: %v", wrErr)
	}

	// **No shell in the launch at all.** `detach.sh` runs its command as `"$@"`, so the command
	// arrives as argv elements and needs no quoting; and `bash <script> <args…>` needs no shell
	// either. This is the shape the `--stop` call below already used — the launch was the only
	// place in this file that built a shell string, and it was the only place that broke.
	//
	// `Start` rather than `Run`, because `detach.sh` blocks until its child ends: starting without
	// waiting is what the trailing `&` used to buy.
	logFile, err := os.Create(launchLog)
	if err != nil {
		t.Fatalf("creating the launch log: %v", err)
	}
	launch := exec.Command("bash", detach, stamp, "120", "--", "bash", payload, grandchildPID)
	launch.Dir = root
	launch.Stdout = logFile
	launch.Stderr = logFile
	if startErr := launch.Start(); startErr != nil {
		t.Fatalf("starting the detached payload: %v", startErr)
	}
	t.Cleanup(func() {
		_ = logFile.Close()
		// The reap for the EARLY-FAILURE paths only. The body reaps the launcher itself after
		// killing it, because the liveness assertion there depends on the reap having happened —
		// see the comment at that call. This one covers the `t.Fatalf`s before that point, where
		// nothing has reaped and the process would be left as a zombie child of the test binary.
		// A second `Wait` on an already-reaped process returns an error and changes nothing, which
		// is why it is discarded rather than branched on.
		_, _ = launch.Process.Wait()
	})

	// **The stamp wait is BOUNDED and names itself.** Previously this was a shell `while [ ! -s
	// "$stamp" ]; do sleep 0.2; done` with no deadline, inside the launch command — so a launcher
	// that never started produced a ten-minute hang and a package-wide timeout instead of a
	// failure. `detach.sh` writes the stamp *before* starting its child, so a few seconds is
	// generous; what matters is that exceeding it is reported here, under this test's name, with
	// the launcher's own output rather than a silent discard.
	stampDeadline := time.Now().Add(20 * time.Second)
	for {
		if fi, statErr := os.Stat(stamp); statErr == nil && fi.Size() > 0 {
			break
		}
		if time.Now().After(stampDeadline) {
			out, _ := os.ReadFile(launchLog)
			t.Fatalf("detach.sh wrote no stamp within 20s, so the launcher never started and there "+
				"is nothing to witness.\nIt records the stamp BEFORE starting its child, so an "+
				"empty stamp means the script itself did not run.\nlauncher output:\n%s\n"+
				"(This bound exists because its absence cost a 9m49s hang and a package timeout on "+
				"Windows, twice, with the launcher's error discarded to /dev/null.)", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for the grandchild to exist — on its own file, not on a duration. A fixed sleep here would make
	// the arm's strength a property of this machine's load.
	deadline := time.Now().Add(20 * time.Second)
	var gpid int
	for time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(grandchildPID); readErr == nil {
			if n, convErr := strconv.Atoi(strings.TrimSpace(string(b))); convErr == nil && n > 1 {
				gpid = n
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if gpid == 0 {
		b, _ := os.ReadFile(stamp)
		t.Fatalf("the payload's grandchild never recorded a pid, so there is no group to stop:\nstamp:\n%s", b)
	}
	alive := func(pid int) bool { return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil }
	if !alive(gpid) {
		t.Fatalf("the grandchild %d died on its own; the arm would pass for the wrong reason", gpid)
	}

	// **The launcher is killed first, and that is what makes this arm discriminate.**
	//
	// The first version of this test could not tell a pid-kill from a group-kill: an injection that TERMed
	// only the child pid still passed, because `detach.sh`'s own launcher loop sees `child-gone` and then
	// kills the GROUP itself. Two killers, so the witness was measuring the launcher, not `--stop`.
	//
	// Killing the launcher is also the production condition. The specimen was the harness ending a
	// backgrounded gate: the wrapper went, nothing was left to clean up, and `golangci-lint` ran on. With no
	// launcher, `--stop` is the only thing that can end the group — which is exactly when its correctness
	// matters and the only state in which it is observable.
	launcherPID := 0
	if b, readErr := os.ReadFile(stamp); readErr == nil {
		for _, f := range strings.Fields(string(b)) {
			if rest, ok := strings.CutPrefix(f, "launcher_pid="); ok {
				launcherPID, _ = strconv.Atoi(rest)
			}
		}
	}
	if launcherPID == 0 {
		t.Fatalf("the stamp records no launcher_pid, so this arm cannot remove the second killer")
	}
	_ = exec.Command("kill", "-9", strconv.Itoa(launcherPID)).Run()

	// **Reap it, then ask whether it is gone — in that order, because `kill -0` cannot tell a
	// zombie from a live process.**
	//
	// This is a consequence of launching through argv rather than through a shell, and it is worth
	// spelling out because it changed the process *topology*. Before, `detach.sh &` inside a
	// `bash -c` made the launcher a GRANDchild: the shell exited immediately, the launcher was
	// reparented to init, and init reaped it on death — so `kill -0` started failing promptly and
	// the poll loop below worked. Started with `cmd.Start()` it is a direct CHILD of this test
	// process, which reaps nothing until told to, and a killed-but-unreaped child keeps its pid
	// entry. `kill -0` succeeds on it. So the old loop polled for 5 seconds and then reported that
	// SIGKILL had been survived, which was false.
	//
	// `Wait` is the reap and also the synchronisation: it returns once the kernel has the exit
	// status, so no polling is needed and there is no interval to tune. The error is discarded on
	// purpose — a SIGKILLed process always reports failure, and that is the expected outcome here.
	_, _ = launch.Process.Wait()
	if alive(launcherPID) {
		t.Fatalf("the launcher %d survived SIGKILL even after being reaped; it would clean up the "+
			"group and mask the subject", launcherPID)
	}
	if !alive(gpid) {
		t.Fatalf("killing the launcher also took the grandchild %d, so there is nothing left for --stop "+
			"to do and the arm cannot discriminate", gpid)
	}

	stop := exec.Command("bash", detach, "--stop", stamp)
	stop.Dir = root
	var stopOut bytes.Buffer
	stop.Stdout = &stopOut
	stop.Stderr = &stopOut
	stopErr := stop.Run()
	code := 0
	if stopErr != nil {
		var ee *exec.ExitError
		if !errors.As(stopErr, &ee) {
			t.Fatalf("running --stop: %v\n%s", stopErr, stopOut.String())
		}
		code = ee.ExitCode()
	}
	if code != 0 {
		t.Errorf("--stop exited %d, want 0:\n%s", code, stopOut.String())
	}

	// The discriminating assertion: the GRANDCHILD is gone. This is what separates stopping the group from
	// stopping the one process the stamp names, and it is the exact failure that left a linter running.
	gone := false
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(gpid) {
			gone = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !gone {
		_ = exec.Command("kill", "-9", strconv.Itoa(gpid)).Run() // do not leak out of the test either
		t.Errorf("the grandchild %d survived --stop, so the pid was ended and the GROUP was not — which "+
			"is the defect this exists to close (a leftover linter reddening the next gate)", gpid)
	}

	// And the outcome is recorded, because a stop that left something running must be readable afterwards or
	// the next unexplained contention has no record to point at.
	b, readErr := os.ReadFile(stamp)
	if readErr != nil {
		t.Fatalf("reading the stamp: %v", readErr)
	}
	if !strings.Contains(string(b), "detach: stop pgid=") {
		t.Errorf("the stamp records no stop line:\n%s", b)
	}
	if strings.Contains(string(b), "SURVIVED") {
		t.Errorf("the stamp reports survivors:\n%s", b)
	}
	if !strings.Contains(stopOut.String(), "no survivors") {
		t.Errorf("--stop did not report that it verified absence; an unverified kill is a hope:\n%s",
			stopOut.String())
	}
}

// TestDetachStopRefusesAStampWithNoGroup covers the two inputs that must not read as success: a stamp that
// does not exist, and one that records no `child_pgid`. Both are plausible — a launch can die before writing
// the record — and "nothing to kill" must not be reported as "killed".
func TestDetachStopRefusesAStampWithNoGroup(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	detach := filepath.Join(root, "scripts", "detach.sh")
	dir := t.TempDir()

	bare := filepath.Join(dir, "bare")
	if writeErr := os.WriteFile(bare, []byte("detach: start=now stamp=x\n"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	for _, a := range []struct {
		name  string
		stamp string
		want  string
	}{
		{"a_missing_stamp_is_refused", filepath.Join(dir, "nope"), "no such stamp"},
		{"a_stamp_with_no_pgid_is_refused", bare, "records no child_pgid"},
	} {
		t.Run(a.name, func(t *testing.T) {
			cmd := exec.Command("bash", detach, "--stop", a.stamp)
			cmd.Dir = root
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if cmd.Run() == nil {
				t.Fatalf("--stop exited 0 on %s; nothing was stopped and it said so was fine:\n%s",
					a.name, out.String())
			}
			if !strings.Contains(out.String(), a.want) {
				t.Errorf("the refusal does not mention %q:\n%s", a.want, out.String())
			}
		})
	}
}
