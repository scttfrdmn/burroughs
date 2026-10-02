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
	stamp := filepath.Join(dir, "stamp")
	grandchildPID := filepath.Join(dir, "grandchild.pid")

	// A payload that spawns a child and waits. The child records its own pid so the arm can ask whether the
	// GROUP died rather than only the process detach.sh knows about.
	payload := "bash -c 'bash -c \"echo \\$\\$ > " + grandchildPID + "; while :; do sleep 1; done\" & wait'"

	launch := exec.Command("bash", "-c",
		detach+" "+stamp+" 120 -- "+payload+" >/dev/null 2>&1 &\n"+
			"while [ ! -s "+stamp+" ]; do sleep 0.2; done\n")
	launch.Dir = root
	if out, launchErr := launch.CombinedOutput(); launchErr != nil {
		t.Fatalf("launching the detached payload: %v\n%s", launchErr, out)
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
	for i := 0; i < 50 && alive(launcherPID); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(launcherPID) {
		t.Fatalf("the launcher %d survived SIGKILL; it would clean up the group and mask the subject",
			launcherPID)
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
