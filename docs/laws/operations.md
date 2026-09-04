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
V=/tmp/ci-verdict-$SHA.txt
echo "SHA=$SHA RUN=$RUN" > "$V"                # stamp the identity FIRST — see below
gh run watch "$RUN" --compact --exit-status > /tmp/ci-frames.log 2>&1   # run_in_background
echo "WATCH_EXIT=$?" >> "$V"
gh run view "$RUN" --json jobs -q '.jobs[] | "\(.conclusion)\t\(.name)"' >> "$V"   # the verdict
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
   - **Third occurrence, and the hazard got worse: the newest run can now read `success` over a
     branch whose verdict is `failure`.** #422's and #424's empty greens sat beside a verdict run
     that was still `in_progress`, so reading the newest one reported *not yet known* as pass. On
     #554 the verdict run had already **completed `failure`** — two of seven jobs red — and the
     newest run on that same SHA concluded `success` over one real job and five `skipped`. So the
     wrong reading is no longer *premature*, it is **contradicted**: the emptiest run outranks a
     decided red, and nothing about the aggregate says so. The consequence for a report is a
     stronger rule than *read the job list*: **no run's conclusion but the push's may enter a
     report**, and it enters as the named jobs present-and-`success`, never as the run's own
     verdict. A parked branch is where this bites hardest — it is red for as long as it is parked,
     so every body edit adds another green above the red that explains why it is parked.
   - **The same instance is the first in which the two-halves disposition above was *applied* rather
     than derived, and it is worth naming because the halves came from different mechanisms.** #554's
     middle run failed on `citations`, down to its single assertion: three citations of the PR's own
     number in the body, a subject that is the body and not the tree. The newest run saw the
     corrected body, ran **that same job**, and passed. Both halves satisfied, and neither by the
     tool that made the mess — the local `citecheck --pr` and CI's `citations` job are two mechanisms
     agreeing in the right order, so the repair is confirmed by something other than the instrument
     that produced it. Worth naming because the second witness was **not arranged**: CI was not known
     to score a body edit, and it scored it both ways. That the intermediate red was real and public
     for about ninety seconds is
     the unavoidable shape of a sweep that runs after delivery: `closecheck` has a `--body <file>`
     form and can run before posting, `citecheck` has only `--pr`, which reads the *posted* body.

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

**And the verdict file states the SHA it watched, stamped before the watch starts.** Mistake 2 binds
the *watch* to a SHA; nothing so far binds the **artifact**, and a verdict file that does not name its
subject is unattributable the moment a second one exists. Two backgrounded watches on one branch
completed together and only one of the two output files opened with an identifying line — so a red
belonging to a commit **two behind** was one sentence away from being reported as `HEAD`'s. What
recovered it was `gh run list --json databaseId,headSha,conclusion`, which put the red on the older
commit and showed the commit after it fully green. `--exit-status` yields a number with no subject and a
job list yields a subject with no identity; neither is a verdict on its own. Stamp `SHA=` and `RUN=`
into the file **first**, so the identity survives a killed watch that never appends anything — the kill
mode above is exactly when an unstamped file is left behind to be misread later. And when a file's SHA
is missing, never infer it from recency: the newest run on a SHA is
[systematically the emptiest](#waiting-on-ci), and the newest *file* is not evidence about which commit
it describes. Note what a re-resolution does not buy: the following commit's green was over a
`CHANGELOG.md`-only diff, so it was byte-identical Go and [refuted
nothing](evidence-and-instruments.md#a-re-run-green-doesnt-refute-a-fail--explaining-the-fail-does).
Folded in here rather than filed, on the ruling that misattributing a verdict is the most load-bearing
failure available in this recipe. (Directive: Scott, on the #539/#540 report — *"every watch output file
records the SHA it watched … charged overhead, not a new PR."*)

**One run, one watcher, one verdict file — and the question that protects it is about the watcher, not
about the run.** Two watchers on one run both write the path above, and the *later* finisher wins, which
is not the more correct one by any mechanism: on #568 the surviving line came from the earlier watcher and
carried a real bug the replacement had already fixed (`echo "… conclusion=$(gh api …) watch-exit=$?"`
expands the substitution first, so `$?` is the api call's status). What starts it every time is reading an
**absent verdict file as a dead watcher**. It is not evidence of that: the file above is written by the
watch's own final commands, so it does not exist while the watch is working, and absence is the normal
mid-flight state.

The rule this file already carried aimed the reader at `gh run list` / `jobs?filter=all` to disambiguate —
and that is **a correct rule aimed at the wrong subject**. Those endpoints answer *is the run finished*;
the question is *is the watcher alive*, and the two are independent in both directions: a live run is
consistent with a dead watcher, and a finished run with a live watcher still appending. On #554 the run
was queried one command before the second watcher was started, came back `in_progress`, and that answer
licensed nothing — the recurrence happened with the rule in hand. Ask the process, not its subject: the
backgrounded task's own status is the answer, and it is not in the GitHub API at all. If a second watcher
is started anyway, give it a **watcher-distinct path** so neither can overwrite the other; agreement
between two files is luck, not a property, and on #554 they happened to agree. (Directive: Scott, on the
#554 report — *"the rule's disambiguator answers 'is the run finished' when the question was 'is the
watcher alive'. A correct rule aimed at the wrong subject."*)

**And a job's `run_attempt` names the attempt it belongs to, never the attempt it ran in.** This paragraph
used to say something else — that `gh run rerun --job <id>` *can re-run the whole run*, so a job's attempt
must be read per job rather than from the flag. The second half is right and insufficient; the first half is
a mechanic that does not exist, and it was minted by reading the field this section now warns about (grave
[#633](https://github.com/scttfrdmn/burroughs/issues/633)). A partial re-run bumps the **run's** attempt
number and re-labels *every* job with it, carrying the un-re-executed jobs' conclusions forward under the
new number. So seven attempt-2 rows do not mean seven jobs ran.

Measured on the specimen the false version was written from — run `33451294518` (PR #566), whose attempt 2
opened at `run_started_at` `23:50:10`. `jobs?filter=all` returns **14 rows for 7 jobs**, and of the seven at
attempt 2 exactly one started after that instant (`citations`, `23:50:14`); the other six repeat attempt 1's
windows *to the second* (`build (ubuntu-24.04)`: `23:35:08`→`23:49:33` under both numbers). Replicated on run
`33916148605` (PR #632): same shape, one executed, six carried. **`--job` re-ran the job it named**, and the
report that said otherwise was reading a grouping key as a record of execution.

Two things follow, and the second is the one to carry:

- **The discriminator is time.** A job executed in attempt *n* iff its `started_at` is at or after that
  attempt's `run_started_at`. Quote that, not the attempt number, when a report claims a job's verdict is
  fresh.
- **`filter=all` is what makes carrying visible at all.** The default `filter=latest` returns only the
  latest attempt's rows — seven green, nothing on any row saying six of them are copies of a previous
  attempt. So the population rule survives its own falsified premise: read `jobs?filter=all`, and read the
  timestamps in it.

The original error was favourable, which is why it stood for four days: it over-reported a green (seven
jobs fresh where one was) and it is the direction nobody re-checks — *an unmeasured complement is not an
empty one*, and a forecast beaten is a forecast falsified. (Scott ordered a line here on the re-run
mechanic; taking the measurement it needed is what produced the grave. Ordered in session and held by no
artifact, so the commit carrying it is `Ratio-Class: carried`.)

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

### Rewriting a branch's commit messages is licensed, and an empty tree diff is the licence

The standing posture is *corrections by posting, not editing* — a record is appended to, never altered,
so the wrong version stays readable beside the right one. **A commit message on a branch bound for a
squash merge is the one place that posture cannot reach**, and the reason is mechanical rather than a
preference: `gh pr merge --squash` builds the merge commit's body by concatenating the branch's messages,
so a later commit saying *"the message above carries a banned form"* does not retract it. It ships both.
The scanned population is `BASE..HEAD`, and every message in it is still in that population after the
one that apologises for it.

So a message that carries a closing keyword next to a reference, or any other construct the sweeps ban,
is repaired **in place, by rewriting the message**, and the correction is recorded in the rewritten
message itself rather than in a follow-up commit.

```bash
git branch backup-pre-msgfix                              # the pre-rewrite tip, kept until the check passes
git rebase -i --root      # or `commit --amend`, or `filter-branch --msg-filter` — messages only
git diff --stat backup-pre-msgfix HEAD                    # must be empty
```

**The empty diff is the whole safety argument**, and it is the same argument the section above makes for
`reset --hard`: an interactive rebase can silently drop or reorder a hunk, and a message-only rewrite is
distinguished from a content change by exactly one observation — the trees are identical. Keep the backup
ref until that diff comes back empty; the check is worthless taken afterwards.

**The specimen is #613's two message rewrites**, and the durable form of the check is the pair of commits
themselves: `d1b1698` → `74bc525` and `3ead596` → `a812479` have byte-identical trees
(`139f826…` and `10348973…` respectively, still resolvable with `git rev-parse <rev>^{tree}`), which is
the check above surviving the slice it was run on. Both were rewritten because a `CHANGELOG.md` entry
quoted a state-changing keyword in front of a reference; harmless in the changelog, where nothing parses
it, and live the moment the sentence was quoted into the PR body. The first rewrite of the first message
then FAILed the commit-message scan by **quoting** the offending form, which is the section below's rule
arriving from the other direction, and its second rewrite describes the form instead.

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

**And it recurred in a second instrument on #519, which is what makes it a shape rather than a
closecheck quirk.** `citecheck.sh --pr`'s discharge-claim check FAILed a body sentence that paired the
words *recorded in* with an ADR number — read as "this PR changed that ADR" when the claim was about the
tree's past. citecheck's own diff-mode note draws exactly that distinction out loud, and body mode cannot
draw it, because a body has no past tense a scanner can see. Then the bullet **reporting** that FAIL
failed identically, by quoting the sentence. So the generalization is not about closing keywords: **any
two-token co-occurrence check has this property, and the report about the check is inside its
population.** Remedy that worked: name the words and the number in separate sentences. The remedy that
does *not* work is an exemption — an exemption inherits none of the trigger's lessons, and here the
checker's reading of the words was fair each time.

Both halves' `--pr` arm fetches the body **live** rather than from the webhook payload, so a body
edited after the failure is scanned by `gh run rerun --failed` without a new push. That is a property
of the scripts, checked by `TestPRFetchFailureIsNeverAPass`, and not something to assume of a job in
general.

*A make target is a population, not a question* — this is the two-channel rule (`make check` is the
local mirror of CI, so a surprise in CI is a bug in the Makefile) meeting the one check whose subject
does not exist yet when the Makefile runs. The mirror is incomplete **by construction** here, which is
why the sequence above is written down instead of a Makefile target being fixed.

### A target's name is a claim about which population was scanned

Running both channels is half the discipline. The other half is on the **report**, and it is a citation
rule: the name you write beside a figure says which population produced it, so `make close` and
`closecheck.sh --pr N` are not two spellings of one verdict. They scan different populations — the table
above is the whole reason — and attributing one arm's green to the other arm's name is a false claim about
what was checked, in the sentence whose entire job is to say what was checked.

**The specimen is #613's own Board line**, which read *"`make close`: 0 banned constructs"*. The figure was
real and the run was real; it came from `closecheck.sh --pr 613`, the **body** arm. The commit-message arm
was red at that moment — that is what the two message rewrites one section up exist for — so the sentence
named the failing channel and reported the passing channel's number. Nothing in it was invented; the
attribution was.

**And the wrong name is the more likely error, because the target is the habitual name for the check.**
`make close` is what a person types, what the Makefile declares, and what `CLAUDE.md` names; `closecheck.sh
--pr N` is the arm that exists only in the opening sequence above. So the drift runs one way, toward the
familiar name, and the remedy is the same one the whole citation family runs on: **name the invocation, not
the check** — `closecheck.sh --pr 613`, with the population it scanned, and a second sentence for the
commit-message arm with its own number or its own red. A Board line reporting one figure under a target's
name is asserting the union of two populations from one of them.

## The maxim's precondition: the mirror holds where the Makefile observes a superset of CI

*A surprise in CI is a bug in the Makefile* is not a free-standing rule. It is an **inference from
containment**: if `make check` observes everything CI observes, then a difference between them can only
be the local gate's deficiency. Write the condition down and the maxim reads in full:

> `make check` is the local mirror of CI **where the Makefile observes a superset of what CI observes.**
> A surprise inside that region is a bug in the Makefile. A surprise outside it is the gap itself, and
> the gap is what to name.

**Nothing below is an exception, and nothing counts them.** They were written as exceptions — first as
*the* exception, then amended to *the second*, while `CLAUDE.md` still said *one* — and a count is a
foreclosing word, true when written and falsified by the next addition, which had already happened
twice before this section stopped counting. With the precondition stated, each is just a place where
containment fails, so they need no number and no closed list: a new one is a new instance, not an
amendment to an arithmetic. (Ruling: Scott, on the #541 report — *"Your own closing sentence states the
precondition … write the precondition into the maxim and let the instances sit under it unnumbered.
That retires the question permanently instead of answering it each time."* The earlier
naming-instead-of-counting step was the agent's, on the #539/#540 report, and it answered the question
once more instead of retiring it.)

Containment can fail in either direction, and the direction determines what to do:

- **CI observes more.** The subject is not in the Makefile's domain at all, so no `make` target could
  have caught it and the mirror is incomplete *by construction*. Look for the artifact the gap is a
  statement about, and put a control on that. Instances: a fetched artifact's presence, below; and [a
  PR body, which does not exist until the PR
  does](#opening-a-pr-the-body-is-a-scanned-population-and-make-check-cannot-see-it).
- **The Makefile observes more.** Then invoking the maxim sends the reader to repair the better of the
  two instruments. Ask which half is worse before assuming; the instance is
  [below](#an-instance-of-the-other-direction-the-makefile-can-be-the-better-half).

*Text mirrors are not failure-behaviour mirrors*; so are absence mirrors, and so is a mirror whose two
halves run in two shells.

### An instance: fetched-artifact presence is machine state, not repo state

There is a class the Makefile cannot observe: **whether a fetched artifact is present on the machine.**
A gitignored corpus —
the spec suite, either reference pin — is machine state, and a local gate running on a box that
already holds the artifact cannot test the case where it is missing. The absence is not in the
Makefile's domain, so no `make check` can be written that would have caught it. (Ruling: Scott, on the
#518 review — *"record it with the maxim, where someone invoking the maxim will see it."*)

The specimen. The threads reference pin (ADR 0007's 2026-08-28 amendment) landed with its fetch
script, its `make threads-ref` target, its per-file floors, its licensed paths and eight controls that
read it — and no CI step fetching it. `BURROUGHS_NO_SKIP: '1'` is workflow-wide, so an absent
authority is a **fail** and not a skip, and two jobs went red on an otherwise complete pin. `make
check` was green, truthfully: the local gate deliberately leaves `NO_SKIP` unset, and this box had run
`make threads-ref` an hour earlier, so the mirror was green *on a machine where the fetch had already
happened*. Nothing in the Makefile was wrong. What the invocation of the maxim would have produced is
a search for a Makefile bug that does not exist.

Two consequences, and the second is the one that generalizes:

- **A new corpus's CI step is part of the corpus, not a follow-up to it.** Script, target, floors,
  licensed paths, controls, *and* a fetch step in every job that runs its tests — one PR, because the
  local gate cannot report the missing member.
- **The join is checkable even though the absence is not.** `TestEveryPinnedCorpusIsFetchedByEveryUnitTestJob`
  (`internal/testenv`) asserts that every corpus declaring a revision pin under `scripts/` is fetched
  by every job that runs `go test` without `-fuzz` — a predicate over the Makefile and the workflows,
  both of which the tree *does* hold. The machine's state is unobservable; the *claim* the workflow
  makes about it is a text, and a text is in a control's domain. Wherever containment fails, look for
  the artifact the gap is a statement about.

(Ruling: Scott, on the #539/#540 report — *"record the second beside the first."*)

### An instance of the other direction: the Makefile can be the better half

Containment also fails the other way, **and then invoking the maxim sends the reader to repair the
better of the two instruments.**

The specimen is grave #539. `ci.yml`'s `no test declined to answer` step and `make strict` ran the same
command, `go test -v -shuffle=on ./...`, captured into `out="$(…)"`. The recipe captured `status=$?`
next and printed the FAIL and SKIP lines; the workflow step relied on `-e` and printed nothing. So a
red job's entire record was a group header, 82 seconds of silence, and `exit code 1`, with the step's
*name* — asserting a skip that was never detected — as the only cause on offer. `make strict` was
green and correct throughout, on the same tree. **Running the local mirror could not have exposed
this**, because the local mirror was the half that worked.

Three things this fixes in how the pair is read:

- **Ask which half is worse before assuming.** The maxim's operative content is *the two must agree*;
  *the Makefile is wrong* is the inference from containment, and containment is what this instance
  denies. `-e` is deliberately absent from `SHELL` here — the Makefile header argues for that at
  length, and names this workflow while doing it: *"ci.yml's two copies of it had to become loops:
  those run under `-e` as well, where the failing assignment kills the step before the floor can print
  its diagnosis."* The tree therefore held both halves of the diagnosis before #539 and still lost a
  verdict, because the sentence naming the hazard sat in the file that did not have it.
- **The better instrument is not the complete one.** `make strict` was better on three counts —
  captures the status, prints FAIL and SKIP unconditionally, separates *tests failed* from *a test
  skipped* — and shared a fourth defect with the step it beat: its filter is `(FAIL|SKIP)`, so
  `-test.shuffle <seed>`, the one line that makes a shuffled order reproducible, matched nothing and
  was discarded on exactly the runs that needed it. #540 is unworkable for that reason. A comparison
  that ranks two instruments answers *which* and never *whether*, so the winner still gets audited.
- **The remedy is one implementation, not a second correct copy.** The repair replaced the step's
  script with `run: make strict`. A third correct copy would have left three texts that can drift in
  two shells; calling the target removes the mirror's room to exist, the same move as [writing the
  sentinel block to a different file from the refresh
  stream](#waiting-on-ci) — remove the collision's room rather than out-argue it.

**The second specimen, grave #544, sharpens the third bullet from *unnecessary* to *impossible*.** The
`deadcode` gate existed in both halves and both halves suppressed the tool's exit status with `|| true`,
so an `out="$(…)"` that captured nothing because the tool never ran was indistinguishable from a clean
tree: an empty capture was the gate's spelling of *no dead code*. The correct repair is to stop
discarding the status. Measured, identical script, two shells:

```console
$ bash --noprofile --norc -e -o pipefail -c 'out="$(deadcode -tests ./... 2>err)"; status=$?; echo "REACHED $status"; cat err'
step exit=2                       # nothing printed at all

$ /bin/sh -c 'out="$(deadcode -tests ./... 2>err)"; status=$?; echo "REACHED $status"; head -1 err'
REACHED 2
flag provided but not defined: -tests
```

So the mirror was not merely redundant here, it was **hostile to its own repair**: under `-e` the
failing assignment kills the step on line one, and the fix cannot be written in the CI copy at all.
Which also means the `|| true` that destroyed the verdict channel was the same token protecting the
testimony channel — remove it in the wrong half and #539 comes back. One more reason the remedy is one
implementation: a mirror can make a defect unfixable in the half you happen to be editing.

And the sweep the previous grave asked for had not happened. #539's own lesson is *a repair to a defect
whose file records a prior instance of the same shape isn't done until it sweeps*, and #544 was found
because a principal pointed at it, not by that sweep. Running it afterwards over `ci.yml` and the
Makefile — every command-substitution assignment, every `|| true`, every `2>/dev/null` — found no third
silent-pass instance: the two `suite-count.sh` floors fail loudly under `-e`, and the byte-size reads
suppress to `0`, which is below every floor. A sweep that comes up empty is still the sweep; skipping it
is what let this one wait for a pointer.

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
