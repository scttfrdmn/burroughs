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
	//
	// **`BURROUGHS_GATE_PGID` is cleared for the same class of reason, and it is the sharper case.**
	// `make ci` is normally launched through `detach.sh`, which exports that variable, and a child
	// started from inside that run stays in the same process group — so an arm meaning to test the
	// *not-owned* path would inherit a **matching** pgid and record `group_owned=yes`. It would then
	// pass or fail according to how the suite was launched rather than according to what it asserts.
	// Cleared here so every arm's environment is the one it describes; `runLockEnv` sets it explicitly
	// for the arms whose subject it is.
	runLockEnv := func(t *testing.T, gatePGID string, args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{lockSh}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "MAKEFLAGS=", "BURROUGHS_GATE_PGID="+gatePGID)
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
	runLock := func(t *testing.T, args ...string) (int, string) {
		t.Helper()
		return runLockEnv(t, "", args...)
	}

	// The arms below cover which advice the refusal gives — a subtest of this function rather than a
	// test of its own, because they need the same `runLockEnv` and lock fixtures as its siblings.
	//
	// (This comment first opened by naming a top-level test that does not exist, written as though
	// the arms had been split out. `TestEveryCitedTestNameResolves` refused it — the fourth
	// citation-shaped fabrication in this campaign, after two issue numbers and a PR number, and the
	// same error each time: naming what sounded right instead of what exists.)
	//
	// **The old message said "End it by its process GROUP, not its pid" unconditionally, and following
	// it could kill the reader.** A gate started with a plain `&` from a non-interactive shell stays in
	// the LAUNCHER's process group, so `kill -TERM -- -<pgid>` signals the launcher too — an agent's
	// session, or a person's terminal. Observed while measuring the lock's own TERM trap: the probe
	// killed its own driver and returned 144.
	//
	// Nothing inside a `make` recipe can decide whether its group is safe to kill; four candidate
	// signals were measured and all fail. `detach.sh` creates the group, so it exports the pgid it made
	// and `cilock.sh` compares against that — a positive fact at the point of creation rather than an
	// inference afterwards. These arms pin the comparison, including the two ways it must answer `no`.
	t.Run("the_stop_advice_matches_whether_the_group_is_the_gates_own", func(t *testing.T) {
		for _, c := range []struct {
			name string
			// gatePGID is what `BURROUGHS_GATE_PGID` holds when the lock is acquired. The sentinel
			// "MATCH" means "the holder's real pgid", resolved below — a literal cannot be written
			// here because the pgid is not known until the holder exists.
			gatePGID    string
			wantOwned   string
			wantSaid    []string
			wantNotSaid []string
			why         string
		}{
			{
				name: "detached_or_nested_so_the_group_is_ours", gatePGID: "MATCH",
				wantOwned: "group_owned=yes",
				wantSaid:  []string{"process GROUP", "detach.sh --stop"},
				why: "detach.sh made this group, or we are nested inside a run that did. Either way " +
					"it holds only gate processes, so killing it cannot reach anyone's session — " +
					"which is why the nested case is correctly `yes` rather than a leak.",
			},
			{
				name: "plain_ampersand_so_the_group_may_be_the_launchers", gatePGID: "",
				wantOwned:   "group_owned=no",
				wantSaid:    []string{"End it by its PID", "pgrep -f golangci-lint", "agent's session"},
				wantNotSaid: []string{"detach.sh --stop"},
				why: "no exported pgid, so ownership cannot be proved. The advice leads with the PID " +
					"and the group-kill line carries a warning naming the group leader. `--stop` is " +
					"NOT offered: there is no stamp for it to find, and a refusal naming the wrong " +
					"route is worse than one naming none.",
			},
			{
				name: "a_forged_or_stale_pgid_does_not_count_as_ownership", gatePGID: "999999",
				wantOwned:   "group_owned=no",
				wantSaid:    []string{"End it by its PID"},
				wantNotSaid: []string{"detach.sh --stop"},
				why: "the variable is set but does not match. Being unable to prove ownership is not " +
					"ownership, so this fails safe — a stale value from an earlier run must not " +
					"license a group kill.",
			},
		} {
			t.Run(c.name, func(t *testing.T) {
				dir := t.TempDir()
				lock := filepath.Join(dir, "lock")
				pidFile := filepath.Join(dir, "holder.pid")

				// A live holder in the shape a real one has: a bash process recording its own `$$`.
				holder := exec.Command("bash", "-c", `echo $$ > "$1"; while :; do sleep 1; done`,
					"_", pidFile)
				if startErr := holder.Start(); startErr != nil {
					t.Fatal(startErr)
				}
				defer func() {
					_ = holder.Process.Kill()
					_, _ = holder.Process.Wait()
				}()
				hpid := ""
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					if b, readErr := os.ReadFile(pidFile); readErr == nil {
						if s := strings.TrimSpace(string(b)); s != "" {
							hpid = s
							break
						}
					}
					time.Sleep(100 * time.Millisecond)
				}
				if hpid == "" {
					t.Fatal("the holder shell wrote no pid, so this arm has no live holder")
				}

				gate := c.gatePGID
				if gate == "MATCH" {
					// The holder's real process group, read the same way cilock.sh reads it.
					out, psErr := exec.Command("ps", "-o", "pgid=", "-p", hpid).Output()
					if psErr != nil {
						t.Fatalf("reading the holder's pgid: %v", psErr)
					}
					gate = strings.TrimSpace(string(out))
					if gate == "" {
						t.Fatal("ps reported no pgid for the holder, so the MATCH case cannot be built")
					}
				}

				if code, out := runLockEnv(t, gate, "acquire", lock, hpid, "deadbeef"); code != 0 {
					t.Fatalf("the first acquire failed: exit %d\n%s", code, out)
				}
				b, readErr := os.ReadFile(lock)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !strings.Contains(string(b), c.wantOwned) {
					t.Errorf("the lock does not record %q.\nwhy this case: %s\nlock:\n%s",
						c.wantOwned, c.why, b)
				}

				// The second gate's refusal is where the advice lives.
				_, out := runLockEnv(t, gate, "acquire", lock, strconv.Itoa(os.Getpid()))
				for _, want := range c.wantSaid {
					if !strings.Contains(out, want) {
						t.Errorf("the refusal does not say %q.\nwhy this case: %s\nrefusal:\n%s",
							want, c.why, out)
					}
				}
				for _, notWant := range c.wantNotSaid {
					if strings.Contains(out, notWant) {
						t.Errorf("the refusal says %q, which does not apply here.\nwhy this case: "+
							"%s\nrefusal:\n%s", notWant, c.why, out)
					}
				}
			})
		}
	})

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
		// **That was a defect in this test's premise, not in the lock, and both halves are measured.**
		// The holder it built could not occur in a real run. Two runs were needed because neither alone
		// licenses the claim — a green here shows a bash-started holder behaves, not that the lock does:
		//
		//   - **this arm on the Windows job**, green on #941's run, where it had failed on the four
		//     before it;
		//   - **`make ci` twice over on `black3.local`, 2026-10-09**: the recorded pid is MSYS's, MSYS
		//     `kill -0` resolves it, and the second gate is refused by name with the holder shown
		//     alive. That is the only exercise of the *real* lock anywhere, because the Windows job
		//     never invokes `make ci`. Commands, `.ci-lock` contents and refusal text are in
		//     `CHANGELOG.md` — cite that rather than a session.
		//
		// **So no limit is recorded and nothing is skipped**: a skip citing a limit the lock does not
		// have would be a false record. (Chair's rulings on the #940 and #941 reviews — investigate what
		// a limit affects before skipping anything, and do not let the comment outrun the evidence.)
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
		//
		// **`detach.sh --stop` is NOT asserted here**, and its absence is the point. This holder was
		// acquired with `BURROUGHS_GATE_PGID` cleared (see `runLock`), so the lock records
		// `group_owned=no` and the refusal takes the PID-advice branch. `--stop` only works for a run
		// detach.sh actually launched; naming it here would send the reader to a tool with no stamp to
		// find, and this file's own rule is that a refusal naming the wrong route is worse than one
		// naming none. The two advice shapes get their own arms below.
		for _, want := range []string{"REFUSED", "pid=" + hpid, "Do not delete"} {
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
