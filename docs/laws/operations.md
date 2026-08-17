<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Operations

The operational recipes: waiting on a CI verdict, confirming a cross-architecture claim before
pushing, and recovering from a squash merge's rewritten history.

Relocated from `CLAUDE.md`, **verbatim**, when that file became a brief and a pointer page.
Nothing was rewritten in the move — only the heading depth, which does not change the anchors
`CLAUDE.md` links to. These are recipes rather than laws, so they carry no `###` law heading and
no recall key: they are here because they exist nowhere else, and a page of pointers is the wrong
place for a shell snippet.

## Waiting on CI

**Wait on the verdict, never on a timer — and wait in the background.** After
pushing, resolve the run for `HEAD` and watch it detached:

```bash
SHA=$(git rev-parse HEAD)
for _ in $(seq 30); do   # the run takes a moment to appear; poll, don't guess
  RUN=$(gh run list --commit "$SHA" --limit 1 --json databaseId -q '.[0].databaseId')
  [ -n "$RUN" ] && break
  sleep 2
done
if [ -z "$RUN" ]; then   # no run — say WHICH no, don't just time out
  gh pr list --head "$(git branch --show-current)" --state open --json number \
    -q 'if length == 0 then "no open PR for this branch: ci.yml is `push: branches: [main]` plus `pull_request`, so a topic-branch push creates no run until its PR exists. Open the PR, then resolve the run." else "PR exists and no run appeared in 60s — that is a real anomaly, not a wait." end'
  exit 1
fi
gh run watch "$RUN" --compact --exit-status   # run this with run_in_background
```

**The loop's negative has two meanings and must say which.** `ci.yml` triggers on
`push` to `main` only, plus `pull_request` — so a push to a topic branch produces
**no run at all** until its PR is opened, and `gh run watch ""` then 404s. The
mechanism is behaving correctly, but the bare loop reports that identically to
"the run has not appeared yet" — a different condition with a different remedy
(open the PR versus wait longer). *A bounded wait that cannot distinguish its own
failure modes is a timer with better manners*; the branch above asks the question
that separates them. It fired for real on #80, and the first reading was "flake
in the poll". (Directive: Scott, PR #82.)

Three separate mistakes are being avoided, and they were made in that order:

1. **`sleep N && gh pr checks` — a duration is not a completion signal.** It
   guesses low and reports a pending run as though that were news, or guesses high
   and wastes the difference; either way the shell, not the CI system, decided
   when to look. Read the verdict from `gh run view "$RUN" --json conclusion` — the
   CI instance of *a command's exit status belongs to whatever ran last*.
   (Directive: Scott, PR #331.)
2. **`gh pr checks --watch` races the run's creation.** It watches whatever checks
   exist *now*, so seconds after a push it finds the previous commit's run,
   reports pass, and exits 0 — a stale green. Always resolve the run id from the
   pushed SHA. `--exit-status` then makes failure non-zero. This is *a verdict
   without an identity check is hearsay*: binding the verdict to the SHA it
   judges is the CI face of stamp-don't-deduce.
3. **Blocking the tool call wastes the wait.** Watch with `run_in_background` and
   keep working; the completion arrives as a notification. A five-minute CI run
   should cost five minutes of *CI*, not five minutes of doing nothing.

**And `sleep` is never how you wait for a signal that exists — background it and let the
wake-up arrive.** This is mistake 1 restated because restating it was necessary: it was
committed *again*, on an already-backgrounded watch: `sleep 200` polling a task's own output
file while its completion notification was in flight. Polling a background task with a timer is
strictly worse than a bare `sleep` — the signal exists and the timer replaces it with a guess.
The test is one question — **does a completion signal exist?** If yes, wait on the signal; if
no (GitHub has no "run created" event), poll for the *condition* in a bounded loop that gives up
loudly — the one honest `sleep` here, honest because it re-asks a real question where a bare
`sleep` asserts an answer. If nothing else is ready to do, say what is pending and stop: an idle
turn costs nothing, a blocked tool call costs the whole wait. (Directive: Scott, PR #103 —
*"stop using sleep"*; the rule was already written here, by the agent that broke it.)

The first two are *verdict channel and mechanism channel are different instruments* applied to
time and to identity: ask the right channel, and ask it about the right run.

## Local cross-architecture verification

The dev box is arm64 — the weakly-ordered side, contract §9's own reason both CI
runners exist (`ci.yml`'s own header: x86-64/TSO plus AArch64/weakly-ordered). CI
gives both on push; a claim needing confirmation *before* pushing — a G-1
demonstration, a redistribution forecast, a flip's own board delta — needs the other
architecture locally, and **`scripts/xcheck-amd64.sh` is how**:

```bash
./scripts/xcheck-amd64.sh                              # go test ./...
./scripts/xcheck-amd64.sh go test ./internal/spec/ -run TestAllGatesOnLeavesNothingGated -v
```

It prefers **native x86_64 on `janus.local`** — real TSO hardware, not an emulation
of it — over the amd64 container under QEMU, and its header carries the reasons it is
a script rather than a recipe here. The operative governance is only
this: **every exit path names its instrument**; unavailability is `NOT RUN` at exit 4,
*mechanism and not verdict*, with CI's x86-64 runner answering one push later; and **a
PR asserting a cross-architecture claim states which instrument confirmed it**.

## After a squash merge, local main diverges from origin/main — verify, don't force

`gh pr merge --squash` rewrites history on GitHub: the merge commit's tree is
identical to the branch just merged, but its hash is new, so a local checkout of
that same branch now points at a commit `origin/main` has never heard of and a
plain `git pull`/`git checkout main` reports "diverged." The fix is **verify then
reset, never reset first**:

```bash
git checkout main && git fetch origin
git diff origin/main <the-branch-or-commit-just-merged>   # must be empty
git reset --hard origin/main                              # only after the diff is empty
```

An empty diff is the check that makes `reset --hard` safe here — it confirms the
"divergence" is purely the squash rewriting the commit's identity, not a real
content difference. This surfaced three times in one session, each time re-derived
from scratch; the pattern is mechanical once named; don't re-derive it.
