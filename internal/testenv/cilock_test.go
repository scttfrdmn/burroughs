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

// TestCILockAllowsOneGateAtATime witnesses `scripts/cilock.sh`.
//
// # Why a lock, when two mechanisms already make an overlap harmless
//
// Two `make ci` runs overlapped **three times** in one campaign. The start-SHA capture and `prmerge.sh`'s SHA
// check made the resulting stale green harmless — but neither prevents the overlap, and **removing the verdict
// at gate start cannot stop a run that finishes afterwards**: the write happens after the removal, so the file
// comes back. Once, a stale run's green landed on a newer run's reset.
//
// The lock is the thing that stops the second run from starting. The three arms below are the three behaviours
// that have to hold together — refuse a live holder, reclaim a dead one, and refuse to write a verdict once
// the lock has moved — because any two without the third leaves a hole: no reclaim wedges the tree until
// someone deletes the lock by hand, and no holds-check lets a reclaimed run write a verdict about a tree
// another gate owns.
func TestCILockAllowsOneGateAtATime(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	lockSh := filepath.Join(root, "scripts", "cilock.sh")
	verdictSh := filepath.Join(root, "scripts", "civerdict.sh")
	for _, p := range []string{lockSh, verdictSh} {
		if _, statErr := os.Stat(p); statErr != nil {
			t.Fatalf("%s is missing, so this witness has no subject: %v", p, statErr)
		}
	}

	// MAKEFLAGS is cleared on every invocation below. Without it, running these arms under `make ci` would
	// inherit the parent's flags and the dry-run guard could decline — the arm would pass by doing nothing.
	runLock := func(t *testing.T, args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{lockSh}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "MAKEFLAGS=")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		runErr := cmd.Run()
		code := 0
		if runErr != nil {
			var ee *exec.ExitError
			if !errors.As(runErr, &ee) {
				t.Fatalf("running cilock.sh %v: %v\n%s", args, runErr, out.String())
			}
			code = ee.ExitCode()
		}
		return code, out.String()
	}

	t.Run("a_second_gate_is_refused_while_the_first_is_live", func(t *testing.T) {
		lock := filepath.Join(t.TempDir(), "lock")

		// **A live holder created the way a REAL holder is created: a bash process recording its own
		// `$$`.** `make ci`'s recipe is `me=$$$$` under `SHELL := /bin/bash`, and it passes that to
		// `cilock.sh acquire`, which probes it with `kill -0` from the same `/bin/bash`. So in real use
		// the recorded pid and the probe are both the shell's.
		//
		// The first version started the holder with `exec.Command("sleep", "60")` — straight from Go —
		// and that is what made this arm fail on Windows. There, a Go-started process is a **native**
		// Windows process, `cilock.sh` runs under **MSYS** bash, and MSYS `kill -0` cannot resolve a pid
		// MSYS did not spawn: it reported a live holder gone, the lock was reclaimed as stale, and the
		// second gate was allowed.
		//
		// **The expectation is that this is a defect in this test's premise rather than in the lock** —
		// the holder it built could not occur in a real run — and that is read off the code above, not
		// yet measured. Two runs settle it and neither has happened at the time of writing:
		//
		//   - **this arm, on the Windows job**, which shows whether a bash-created holder is seen there;
		//   - **`make ci` twice over on a real Windows host**, which is the only thing that shows the
		//     *real* lock works, because the Windows job never invokes `make ci` at all.
		//
		// A green here alone would not license the claim: it shows a bash-started holder behaves, not
		// that the lock does. Until both are recorded this comment states an expectation, and the
		// wording is to be tightened to cite the runs once they exist (chair's ruling on the #940
		// review, which is also where the "don't get ahead of the evidence" instruction came from).
		holderPIDFile := filepath.Join(t.TempDir(), "holder.pid")
		holder := exec.Command("bash", "-c", `echo $$ > "$1"; while :; do sleep 1; done`, "_", holderPIDFile)
		if startErr := holder.Start(); startErr != nil {
			t.Fatal(startErr)
		}
		defer func() {
			_ = holder.Process.Kill()
			// Reaped, because `kill -0` cannot tell a zombie from a live process and a later arm in
			// this package may ask about a pid. Same reason as the detach witness.
			_, _ = holder.Process.Wait()
		}()

		// The holder's own pid, read from the file it wrote, bounded — not `holder.Process.Pid`, which
		// is what Go started and on Windows is the wrong namespace.
		hpid := ""
		pidDeadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(pidDeadline) {
			if b, readErr := os.ReadFile(holderPIDFile); readErr == nil {
				if s := strings.TrimSpace(string(b)); s != "" {
					hpid = s
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if hpid == "" {
			t.Fatalf("the holder shell wrote no pid within 20s, so this arm has no live holder to " +
				"witness and would otherwise measure nothing")
		}

		if code, out := runLock(t, "acquire", lock, hpid, "deadbeef"); code != 0 {
			t.Fatalf("the first acquire failed: exit %d\n%s", code, out)
		}

		// The second gate: this test's own pid stands in for a second `make ci`.
		code, out := runLock(t, "acquire", lock, strconv.Itoa(os.Getpid()))
		if code == 0 {
			t.Fatalf("a second gate was ALLOWED while the first holder was alive — which is the overlap "+
				"that collided two linters and let a stale verdict land on a newer run's reset:\n%s", out)
		}
		// The refusal must IDENTIFY the holder and say how to end it. A refusal that does neither teaches
		// the reader to delete the lock file, which is the mechanism's own defeat.
		for _, want := range []string{"REFUSED", "pid=" + hpid, "detach.sh --stop", "Do not delete"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not mention %q:\n%s", want, out)
			}
		}
		// And the lock must still name the original holder: a refused acquire that overwrote the file would
		// hand the tree to the loser.
		b, readErr := os.ReadFile(lock)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(b), "pid="+hpid) {
			t.Errorf("the refused acquire overwrote the lock:\n%s", b)
		}
	})

	t.Run("a_stale_lock_is_reclaimed_and_says_so", func(t *testing.T) {
		lock := filepath.Join(t.TempDir(), "lock")

		// A pid that is certainly dead, **and in the same namespace a real holder's pid lives in** — a
		// bash process that records `$$` and exits.
		//
		// The namespace matters here for a subtler reason than in the live arm. A Go-started process is
		// a native Windows pid, which MSYS `kill -0` cannot resolve *whether or not it is alive* — so
		// this arm would have passed on Windows by reading an unseeable pid as dead, which is the right
		// answer reached by a mechanism that cannot distinguish it from the wrong one. It would still
		// pass with the process very much alive. **A premise that holds for a reason other than the one
		// stated is a vacuous premise**, and the `Fatalf` below guards it only against the case the
		// stated reason covers.
		deadPIDFile := filepath.Join(t.TempDir(), "dead.pid")
		dead := exec.Command("bash", "-c", `echo $$ > "$1"`, "_", deadPIDFile)
		if startErr := dead.Start(); startErr != nil {
			t.Fatal(startErr)
		}
		_ = dead.Wait()
		pidBytes, readErr := os.ReadFile(deadPIDFile)
		if readErr != nil {
			t.Fatalf("the short-lived holder shell wrote no pid (%v), so 'a dead holder' cannot be "+
				"constructed here and this arm has no subject", readErr)
		}
		deadPID := strings.TrimSpace(string(pidBytes))
		if deadPID == "" {
			t.Fatal("the short-lived holder shell wrote an empty pid file, so this arm has no subject")
		}
		for range 50 {
			if exec.Command("kill", "-0", deadPID).Run() != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Fatal rather than Skip, deliberately. A waited-for child being still alive would mean the premise
		// this arm rests on is false, and a skip would make the arm VANISH at exactly the moment it stopped
		// being able to measure anything — a skip is not a verdict. If this ever fires it is a finding about
		// the platform, which is worth a red rather than a silence.
		if exec.Command("kill", "-0", deadPID).Run() == nil {
			t.Fatalf("pid %s is still reported alive after Wait() returned, so 'a dead holder' cannot be "+
				"constructed here and this arm has no subject", deadPID)
		}

		if err := os.WriteFile(lock, []byte("pid="+deadPID+"\npgid="+deadPID+"\nsha=cafebabe\nwhen=x\n"),
			0o600); err != nil {
			t.Fatal(err)
		}

		code, out := runLock(t, "acquire", lock, strconv.Itoa(os.Getpid()))
		if code != 0 {
			t.Fatalf("a lock held by a DEAD pid wedged the gate: exit %d\n%s\n\n"+
				"That is how a lock gets deleted by hand and never comes back.", code, out)
		}
		// Loudly: a gate that died without releasing is a fact worth one line, and a silent reclaim hides it.
		if !strings.Contains(out, "reclaiming a stale lock") {
			t.Errorf("the reclaim was silent, so a gate that died without releasing leaves no trace:\n%s", out)
		}
		b, _ := os.ReadFile(lock)
		if !strings.Contains(string(b), "pid="+strconv.Itoa(os.Getpid())) {
			t.Errorf("the reclaim did not take ownership:\n%s", b)
		}
	})

	t.Run("a_run_that_lost_the_lock_writes_no_verdict", func(t *testing.T) {
		// The third behaviour, and the one the SHA field cannot cover. A moved tip is caught by the SHA; two
		// gates on the SAME commit are not, and a reclaimed lock is exactly that case.
		dir := t.TempDir()
		lock := filepath.Join(dir, "lock")
		out := filepath.Join(dir, "verdict")

		// The lock is held by somebody else — a second gate that reclaimed it.
		other := exec.Command("sleep", "60")
		if startErr := other.Start(); startErr != nil {
			t.Fatal(startErr)
		}
		defer func() { _ = other.Process.Kill() }()
		if code, o := runLock(t, "acquire", lock, strconv.Itoa(other.Process.Pid), "deadbeef"); code != 0 {
			t.Fatalf("setting up the other holder failed: %d\n%s", code, o)
		}

		// This run believes it is the gate, and is not.
		cmd := exec.Command("bash", verdictSh, "0", out, "cafebabe", lock, "999999")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "MAKEFLAGS=")
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if runErr := cmd.Run(); runErr != nil {
			t.Fatalf("the writer should decline and exit 0, not fail: %v\n%s", runErr, buf.String())
		}
		if _, statErr := os.Stat(out); statErr == nil {
			body, _ := os.ReadFile(out)
			t.Errorf("a verdict was written by a run that no longer held the lock — this run's exit code "+
				"about a tree the OTHER run is gating:\n%s", body)
		}
		if !strings.Contains(buf.String(), "no longer holds the gate lock") {
			t.Errorf("the writer did not say why it declined; a missing verdict with no reason looks like "+
				"a broken writer:\n%s", buf.String())
		}
	})

	t.Run("the_holder_alone_may_release", func(t *testing.T) {
		// Without this, a run that was reclaimed deletes the lock of the run that reclaimed it, handing the
		// tree to a third gate while the second is still going.
		lock := filepath.Join(t.TempDir(), "lock")
		if code, o := runLock(t, "acquire", lock, strconv.Itoa(os.Getpid()), "deadbeef"); code != 0 {
			t.Fatalf("acquire failed: %d\n%s", code, o)
		}
		if _, o := runLock(t, "release", lock, "999999"); !strings.Contains(o, "not releasing") {
			t.Errorf("a non-holder's release was not refused:\n%s", o)
		}
		if _, statErr := os.Stat(lock); statErr != nil {
			t.Errorf("a non-holder deleted the lock: %v", statErr)
		}
		if code, o := runLock(t, "release", lock, strconv.Itoa(os.Getpid())); code != 0 {
			t.Fatalf("the holder's own release failed: %d\n%s", code, o)
		}
		if _, statErr := os.Stat(lock); statErr == nil {
			t.Errorf("the holder's release did not remove the lock")
		}
	})

	t.Run("a_dry_run_does_not_touch_the_lock", func(t *testing.T) {
		// `make -n ci` runs every command on the `$(MAKE)` line, so without this guard a dry run would both
		// write a real lock and — worse — be REFUSED while a real gate was running, reporting a conflict
		// about a run that is not happening.
		lock := filepath.Join(t.TempDir(), "lock")
		cmd := exec.Command("bash", lockSh, "acquire", lock, strconv.Itoa(os.Getpid()))
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "MAKEFLAGS=n")
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if runErr := cmd.Run(); runErr != nil {
			t.Fatalf("the dry-run path should exit 0: %v\n%s", runErr, buf.String())
		}
		if _, statErr := os.Stat(lock); statErr == nil {
			t.Errorf("a dry run created the lock file")
		}
		if !strings.Contains(buf.String(), "dry run") {
			t.Errorf("the dry-run decline is silent:\n%s", buf.String())
		}
	})
}
