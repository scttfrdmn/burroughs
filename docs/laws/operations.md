<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Operations

The operational recipes: waiting on a CI verdict, confirming a cross-architecture claim before
pushing, recovering from a squash merge's rewritten history, and running the two sweeps whose second
population only exists once the PR does.

The first three were relocated from `CLAUDE.md`, **verbatim**, when that file became a brief and a
pointer page.
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
gh run view "$RUN" --json jobs -q '.jobs[] | "\(.conclusion)\t\(.name)"'   # the verdict is here
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
   when to look. Read the verdict from the run rather than from the shell — the
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
4. **`--json conclusion` cannot tell "everything passed" from "nothing ran".** A run's
   conclusion is an **aggregate**, and a run whose jobs were all skipped concludes
   `success` — truthfully. On #422, one SHA had **three** `pull_request` runs; the one
   that finished in under a minute had `citations` green and `build`, `lint`,
   `conformance`, `vuln` and `fuzz-smoke` all **`skipped`**, while the run carrying the
   verdict was still `in_progress` for another ten minutes. Reading the fast one's
   conclusion is a green over a population of one job. So read `.jobs[]` and assert the
   named jobs are **present and `success`, never `skipped`** — both `build` matrix legs
   included. Note that this is *not* mistake 2 with a different symptom: mistake 2 is the
   **wrong instance** (a stale SHA) and identity-checking fixes it, while this is the
   **empty instance** on the *right* SHA, which no amount of SHA-binding can catch. It is
   [a skip is not a verdict](boards-and-buckets.md#a-skip-is-not-a-verdict) one level up —
   a skipped job passes by asking nothing, and an aggregate over skips passes by asking
   nothing louder. A body-only `gh pr edit` creates a fresh `pull_request` run that is
   *supposed* to skip everything but the citations sweep, which is exactly why the skip has
   to be read rather than summed away. (Directive: Scott, PR #424.)
   - **Second occurrence, and the cause turns the hazard into a prediction: `gh pr edit`
     spawns a run that fires only the body-scoped jobs, so an empty instance is *expected
     after every body edit*.** PR #424's own SHA carried three runs for the same reason
     #422's did — one push plus two body edits — and the operative consequence is an
     ordering one: **the newest run on a SHA is systematically the emptiest, and the run
     carrying the verdict is the oldest**, because the push created it and the edits came
     after. Resolving "the latest run for this SHA" is therefore not a neutral tie-break
     but the reliably wrong choice on any PR whose body was edited, which is all of them
     here — the body *is* the report, so it is edited at least once after the push it
     describes. This is worth more than *watch out for empty runs*: a rule that says
     **when** to expect one is recognized rather than re-diagnosed, and re-diagnosis is
     what the second occurrence cost. (Directive: Scott, on the #424 merge — *"that makes
     it expected after every body edit rather than an anomaly, which is a better fact."*)
   - **And the same edit sequence leaves real reds on the SHA, so a red is disposed of on
     two halves or not at all.** #424's middle run *failed*: one assertion, a self-citation
     in the PR body, whose subject is the body and not the tree, and whose fix was an edit
     rather than a commit — so no later SHA exists to supersede it and the failing run sits
     on the merge candidate permanently. **A red whose subject is a superseded body is only
     superseded when a later run on the corrected body ran the same check and was green.**
     Both halves are load-bearing: the failing check identified down to its single
     assertion, *and* a later run in which **that same job** ran and passed. The first half
     alone is an excuse; the second alone is a green from a different question, since a run
     that skipped the job proves nothing about it — which is this mistake's own lesson
     turned back on the disposition. That is
     [a re-run green doesn't refute a fail](evidence-and-instruments.md#a-re-run-green-doesnt-refute-a-fail--explaining-the-fail-does)
     satisfied rather than waived: the cause is bounded, not called a flake.

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
time and to identity: ask the right channel, and ask it about the right run. The fourth is the
same pair applied to *extent* — the right channel, the right run, and a question narrow enough
that the answer cannot be true of an empty set.

**And write the sentinel block to a different file from the refresh stream.** `gh run watch`
redraws a live display of hundreds of lines; if the `WATCH_EXIT` / `FINAL` / `JOB` sentinel is
appended to that same capture, the grep that harvests the verdict is scanning a haystack the
watch wrote — and on PR #460 `grep -E '^JOB'` matched the watch's own **`JOBS`** section headers,
returning dozens of progress rows as if they were the job list. Anchoring the pattern (`^JOB `)
repairs that one instance; **redirecting the two streams to two files removes the collision's
room to exist**, which is the stronger move because the display's vocabulary is upstream's to
change. The general form, with its other specimen, is
[a control is a pattern plus the text it is
handed](evidence-and-instruments.md#a-control-is-a-pattern-plus-the-text-it-is-handed), whose narrow
form this is.
This compounds with mistake 4 rather than restating it: there, a sentinel over the wrong run
reports an empty green; here, a sentinel over the *right* run is answered by the watch's own
chrome, and neither SHA-binding nor reading `.jobs[]` can see it.

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

## Opening a PR: the body is a scanned population, and `make check` cannot see it

Two of the repo's sweeps scan **two populations each**, and the make targets reach only one of them:

| target | population it scans | the population it cannot |
|---|---|---|
| `make close` | commit messages in `BASE..HEAD` | the PR title and body |
| `make cite` | added lines of the diff | the PR title and body |

The body half needs a PR number, so it can only run **after** `gh pr create` — which means it is not
part of the local gate at all and has to be a step in the opening sequence:

```bash
gh pr create --title … --body-file /tmp/pr-N.md
sh scripts/closecheck.sh --pr <n>     # the body channel: closing keywords
sh scripts/citecheck.sh  --pr <n>     # the body channel: self-citation
```

**The specimen is #398**, whose body opened with the keyword `Closes` immediately followed by its
issue reference. The PR's own plan said "closecheck and citecheck, both channels"; `make close` ran,
reported `0 banned constructs` over 67 commit-message lines, and that green was read as the sweep's
verdict. It was one channel's verdict. CI failed the `citations` job on the exact construct the ban
exists for — a squash message is derived from the body, so the keyword would have taken the issue's
state with no lesson comment first, which is grave #314's whole subject.

**And the first draft of this entry tripped the other channel, twice, in the commit message that added
it** — once quoting the offending line and once describing its effect, both with the reference on the
same line as the keyword. That is *a ban reported in the banned form is still the banned form*: the
scanner reads tokens, not quotation marks. Hence the phrasing above, which keeps the keyword and the
reference apart, and hence the rule that the report about a sweep is inside that sweep's population —
re-run `closecheck.sh` **after** writing the commit that describes a closecheck failure.

Both halves' `--pr` arm fetches the body **live** rather than from the webhook payload, so a body
edited after the failure is scanned by `gh run rerun --failed` without a new push. That is a property
of the scripts, checked by `TestPRFetchFailureIsNeverAPass`, and not something to assume of a job in
general.

*A make target is a population, not a question* — this is the two-channel rule (`make check` is the
local mirror of CI, so a surprise in CI is a bug in the Makefile) meeting the one check whose subject
does not exist yet when the Makefile runs. The mirror is incomplete **by construction** here, which is
why the sequence above is written down instead of a Makefile target being fixed.

## Reading the tracker's state: the queue comes from the issues API, never from a cached listing

`decision-needed:scott` **is** the decisions-needed queue (`CLAUDE.md`, `## Where the work is
tracked`), so "the queue is empty" and "three items are waiting on you" are state claims about the
tracker made in a report to a principal. **No sweep in this repo can check them** — `citecheck.sh`
resolves pointers in the tree, and the tracker is outside every instrument's domain. The only defence
is the sourcing.

```bash
# The queue, by label. Excludes PRs: the issues endpoint returns both.
gh api --paginate 'repos/scttfrdmn/burroughs/issues?state=open&labels=decision-needed:scott&per_page=100' \
  --jq '.[] | select(.pull_request == null) | "\(.number) \(.title)"'

# One issue's labels, when the claim is about a specific issue rather than a population.
gh issue view <n> -R scttfrdmn/burroughs --json labels -q '.labels[].name'
```

**Not `gh issue list --label`.** It answers from a search index that lags the label mutation, so a
label removed a moment ago is still returned — the specimen is eight `decision-needed:scott` removals
still listed after every one had been applied, which would have gone into a report as *"eight items
await your ruling"* on the same turn they were discharged. The failure direction is the dangerous one:
the stale answer is the *pre-mutation* state, so a queue you have just drained reads as full, and a
queue you have just filled reads as empty.

The two channels agreed exactly when this entry was written — `type:decision` open: 14 by both,
all-open issues: 79 by both, `decision-needed:scott`: 0 by both. **That agreement is not the
measurement and does not license the listing**: agreement is what the index looks like when nothing
has changed recently, which is most of the time and never the moment a claim is being made. The
discriminating measurement is the one above, taken seconds after a mutation.

Two properties of the API arm worth stating, because both are ways to under-report a queue:

- **`per_page=100` is not "all"** — without `--paginate` the 101st item is silently absent, which is
  *a first-match pick declines to ask* in a paginated dress.
- **The issues endpoint returns pull requests**, which over-reports in the other direction; the
  `select(.pull_request == null)` filter is what makes the count a count of issues. Today the label
  populations happen to carry no PRs, so dropping the filter changes nothing — a
  passing-population coincidence, not a property.

(Ruling: Scott, PR #490 review — *"any state claim about the queue is read from the issues API, never
from a cached CLI listing. Put the procedure wherever the queue is documented."* Hence both this entry
and the pointer beside the label's definition in `CLAUDE.md`.)
