<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0072 — A detached run is bounded by a wall-clock timeout and the *session's* liveness, because the launching shell exits before the work does

Date: 2026-09-05 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is harness
mechanism, it changes no gate's default, no contract §, and no public signature, and it reverses no
stamped ADR. Scott's four-item order is in-session and carries no artifact to cite, which is why every
commit in this slice is `Ratio-Class: carried` and why this document does not claim his approval for its
option choices — *durability is not independence*.

Filed against **[#659](https://github.com/scttfrdmn/burroughs/issues/659)**. It settles how a detached run
states what will end it. It does **not** settle what the CI-watch recipe *reads* once a run is over — the
per-job, `started_at`-against-`run_started_at` reading is unchanged and already in
[operations.md](../laws/operations.md#waiting-on-ci).

## Context

Scott's order on the #658 review, ahead of #586: *"the orphaned processes go first … That's a defect
class, not housekeeping, and Scott has been cleaning up after it by hand."* The governing rule he stated:
**a launched process is a claim that something will end it.** No process starts without naming what ends
it. Four items — enumerate and kill; every detached process carries a timeout *and* an exit when its
parent dies; every launcher records its pid in the SHA-stamped file it already writes; the spinner rigs
get the same bound.

The enumeration is in #659 and it moved the subject. Measured on both boxes this repo has standing to
touch, **no orphan is this project's**: no `gh`, `go` or `*.test` process runs locally or on
`janus.local`, every pueue task is `Done`, and the three genuine orphans on the dev box belong to
`surface-loop-demo` and `keel`. The tree's spinner rigs are bounded too —
`TestThreeConcurrentCallersAndAStopDoNotHang/spinningCallers` releases `gatedSpinModule`'s gate in a
`t.Cleanup` that runs `Resume` unconditionally and then waits per caller, which is this ADR's rule already
implemented once with grave **#599** as its reason.

**What is unbounded is the launcher, because there is no launcher.** Every leak in this class came from a
background command typed fresh each time: the watcher the harness killed before it wrote its verdict, the
relaunch after it, the two watchers on one run, and the 8,000-attempt / fourteen-spinner / two-million-round
probe rigs whose results are quoted in `internal/interp/futex_test.go`, ADR 0060 and
[controls.md](../laws/controls.md) with no artifact that ran them. A habit cannot hold a timeout, cannot
record a pid before it starts, and cannot be read by a later session.

This is not policing typed commands. Scott's 2026-08-31 ruling on that stands — hold the habit, file
nothing, build no checker. The direction here is the opposite: stop typing *this* one, so the property is
carried by a thing instead of by memory.

### The measurement that decides option A, before any argument about it

A backgrounded child was launched from a tool-call shell and asked to record its own view once a second:

```
t=1 self=34095 ppid=413 pgid=34095
t=2 self=34095 ppid=413 pgid=34095
t=3 self=34095 ppid=413 pgid=          <- the launching shell is GONE here
...
t=6 self=34095 ppid=413 pgid=
```

- **The launching shell exits almost immediately, by design.** 34095 was gone by `t=3` while its child ran
  to `t=6`; `ps -p 34095` afterwards confirms it. So *"exit when your parent dies"*, read against the
  immediate parent, would end a CI watcher seconds after launch, having watched nothing.
- **`$PPID` is not a liveness handle.** In `zsh` a subshell inherits the variable rather than recomputing
  it, so the child reads `413` — its *grandparent* — and never updates on reparenting. A watcher polling
  `$PPID` polls the wrong process and reports health forever.
- **413 is the session:** `claude --resume`, and it is the `$PPID` of every tool-call shell (34095 then,
  34991 in a later call). The thing whose death should end a watcher is the session that wanted the answer.

Two failures of my own instrument are worth recording, because both are shapes this corpus already owns. A
first probe read `$$` inside a subshell, which is the *parent's* pid in both shells — the measurement was
of the wrong process and the numbers looked fine. A second probe used `$BASHPID` under `set -u` in `zsh`,
where it is unset, so the subshell died before its first `echo` and the output file had **zero lines** —
and I had pre-labelled zero lines as *"the harness reaped it"*. An unrun probe reads exactly like a
measurement of the thing you wanted; the pre-registered interpretation is what stopped it becoming one.

## Options

**A. Poll the immediate parent (`$PPID`).** The literal form of the order. **Falsified by the measurement
above** — it ends the watcher in ~2s. Kept in this list rather than dropped because it is what the order
says, and the reason it cannot be done is a fact about the harness rather than a preference.

**B. Poll the session pid, verified by `comm`. Chosen.** The launcher derives its grandparent, checks that
its executable name is `claude`, records the pid it settled on, and polls it with `kill -0`. Where it
cannot establish such a pid it says so in the file and the timeout stands alone — a liveness handle that
might name a **recycled** pid is worse than no handle, because it converts a leak into a wrongly-killed
run at an unrelated moment.

**C. Timeout only.** Rejected. A 15-minute bound is still a 15-minute orphan across a session that ended
after 30 seconds, and Scott asked for both conditions, not the cheaper one.

**D. A supervisor process that reaps.** Rejected on regress: a supervisor is itself a launched process and
needs the same claim, so it moves the problem up one level and adds a bigger artifact than the defect.

**E. Where the pid is written — at exit (status quo) versus before the work starts.** Chosen: **before**.
A pid written at exit is recorded exactly in the runs that did not leak, which is the one population that
never needs it. The record is written by the process that might become the orphan, before it can.

**F. Shape — a CI-specific watcher versus a generic wrapper.** Chosen: **generic**. Item 4 names probe
rigs, not CI, so a CI-only script would leave the rigs to the habit it is replacing. The wrapper takes the
stamp file, a timeout and a command; the CI-watch body stays the recipe it already is and is passed *to*
it, so one file holds both the pid record and the verdict — which is what *"in the SHA-stamped file it
already writes"* asks for.

## Choice

`scripts/detach.sh <stampfile> <timeout-seconds> -- <command…>`:

1. Writes `detach:` header lines into `<stampfile>` **first** — its own pid, the child's process group, the
   session pid it settled on (or the reason it could not), the deadline as an absolute time, and the
   command. A later session reads that file to find and end an orphan without guessing.
2. Runs the command in **its own process group**, so the killable handle is the group. A watcher spawns
   `gh`; killing the pid alone leaves the grandchild.
3. One poll loop, **four** exit conditions: the child recorded a status, the child is gone having recorded
   none, the deadline passed, or the session pid is gone. This list said *three* until the battery ran; the
   fourth is below.
4. **Always appends a terminal line** — `detach: end reason=… child_exit=…`. On expiry it kills the group
   (TERM, grace, KILL) and *still writes*, because writing nothing on the abnormal path is precisely the
   hole the killed watcher left.

Exit status is the command's own where the command ended it, and otherwise a code per reason: **124**
deadline, **125** session gone, **120** child ended from outside leaving no status. 120 rather than 126 or
127 because those already mean something about *invoking* a command, and a code that means two things is
what the `reason=` word exists to avoid.

## Consequences

- **The bound is watched die**, five rows and 19 assertions, since a launcher whose bound never fires has
  not been shown to have one. A: the happy path, status propagated, `killed=none`, and the session handle
  **resolved by the walk** rather than falling back — without that row the whole liveness arm could be
  vacuous here. B: a command that never exits, against a 3s deadline — exit 124, group gone, terminal line
  written. C: the session handle pointed at a verified-absent pid against a **600s** deadline — exit 125 in
  1s, so the exit is attributable to the handle and not to the clock. D: the same runaway with a 600s
  deadline and a live session must **survive**, which is what makes B's kill attributable to the deadline
  rather than to the command ending on its own. E: below.
- **Row E is the defect this design had, found by exercising the feature it advertises.** The battery ended
  the child using the `kill_handle` the stamp file records — and that handle kills the group, which includes
  the wrapper subshell whose job is to record the child's status. So the status file never appeared, and
  with a distant deadline and a live session the loop polled out the **whole remaining 592 seconds over a
  child that no longer existed**. A launcher holding a ten-minute claim on nothing is precisely the leak
  class this script exists to close, reproduced inside the closure — and the thing that surfaced it was
  using the handle rather than reasoning about it. Hence the fourth exit condition. Not a grave: it never
  reached main.
- **`sleep` is still never how you wait for a signal.** The poll loop is inside the *bound*, not inside
  the verdict: the verdict is still read from the run, per behaviour 5. A timeout is a duration because a
  ceiling is a duration; a completion is not.
- **A second watcher on one run stays possible.** This ADR does not make the stamp file exclusive, and
  *one run, one watcher, one verdict file* remains a discipline. What changes is that the first watcher's
  pid is now in the file, so the second launcher's operator can see it — the collision becomes visible
  rather than silent. Making it *refused* is a lock, and a lock whose stale-holder policy is unstated is
  its own defect class; not taken here.
- **The three foreign orphans are reported, not killed.** Killing another project's live work is not
  reversible and is not what the order implies; #659 names them with pid, age and cwd so the owner can.
- **The law minted:** *a launched process is a claim that something will end it* — into
  [operations.md](../laws/operations.md), beside the waiting-on-CI recipe that prescribed the typed form.
