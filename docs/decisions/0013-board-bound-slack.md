# 0013 — A board bound asserts its own slack, and the slack is per-bound

Date: 2026-08-02 · Status: **accepted** (Scott delegated the call, 2026-08-02 — #87)

## Decision

**Option 1 of #87 — assert the slack — with two corrections to the recommendation as
filed, both forced by measurement.**

Each board bound gains a companion upper bound, so a bound left far behind its measurement
is a **failure** rather than a silent degradation:

```
pass floors:    floor ≤ actual ≤ floor + slack
fail ceilings:  actual ≤ ceiling            (no slack term — see below)
```

**Correction 1: not by reflection.** #87 recommended "scoped by reflection over the
constants rather than a list". That is not buildable: all four bounds are **function-local
`const` declarations** in four different test functions, and reflection cannot see a
function-local const. The recommendation named the right instinct — *derive the domain,
never enumerate it* — and reached for a mechanism the code shape does not admit. What
replaces it is a single helper, `boardBound`, that every bound routes through, plus an
AST-reading control (`TestEveryBoardBoundIsChecked`) that walks `spec_test.go` for
comparisons against a `*Floor`/`*Ceiling` const and requires each to go through the helper.
That is the `TestEverySkipSiteIsLicensed` pattern: when a rule says "all of these go through
one door", something has to assert that they do, or the mechanism has the shape it exists
to forbid.

**Correction 2: per-bound slack, and ceilings get none.** #87 asked whether the slack is one
number or per-bound. Per-bound, because the bounds are not the same kind of claim:

- **`passFloor`** (default lane) moves in *strata* — measured across 15 commits: 783 → 1419
  → 1628 → 1941 → 1953 → 1992 → 4122 → 4159 → 4161 → 4162. Two jumps exceed 600 in a single
  PR (`+636`, `+2130`), so a tight slack would fire on ordinary progress and be trained
  away, which is worse than no control. **Slack 250**: smaller than every real stratum, so
  a stratum-sized jump forces the constant forward in the PR that caused it; larger than the
  routine `+1`/`+3` PRs, so it does not fire on them.
- **`allOnPassFloor`** tracks the same board plus the gated vectors, so it moves with the
  same strata. **Slack 250**, same reasoning, same shape.
- **`binaryFailCeiling` and `textFailCeiling` get no slack term at all.** They are at
  **0**, and a ceiling at zero cannot go stale — the distance between "at most 0 failures"
  and "0 failures" is not a quantity that can silently grow. Adding a slack term here would
  be a mechanism with no risk to catch, which is the sort of decoration this very issue is
  about. Stated rather than omitted, because silence about a member of the space is how
  #48 happened: the space is four bounds, two need slack, two are structurally immune, and
  a reader deserves to know which they are looking at.

## Question

`allOnPassFloor` was **798 against an actual 4178** and had been since #56, 15 commits back.
It could not have caught a regression erasing four fifths of the all-gates-on lane. Found by
reading the printed total next to the constant while raising it for #86 — by eye, not by any
control.

The floor is falsifiable in the ordinary sense: drop the count below 798 and it fires. So
*break the thing it names and watch it fail* is satisfied and the floor is **still**
decoration, because the defect is not in the assertion, it is in the **distance between the
assertion and the measurement**. That distance was unasserted, so it grew every time a PR
moved the count and left the constant alone.

This is the vacuity class with the vacuum in a new place. A comparison against an empty set
agrees perfectly; so does a comparison against a set that is merely far away. Same
signature: the mechanism runs, agrees, and says nothing.

## Why 250, and why the number is weather

The slack is not derived from a principle — there isn't one — so it is derived from the
history it must survive, and the honest statement of that is a *range*, not a figure. The
sequence above has a modal step of 1–3, a 90th-percentile step near 40, and two strata over
600. Any number in roughly 100–600 separates "stratum" from "routine". 250 sits in the
middle of that window rather than at an edge, so it is not tuned to the largest routine PR
observed so far — tuning to the sample is how a bound inherits the sample's blind spot.

The second-order honesty point, since this ADR is about exactly this failure: **250 is a
choice, not a measurement**, and if it fires on a PR that is genuinely routine, the remedy is
to widen it *in that PR with the new observation recorded here*, not to raise the floor and
move on. A slack bound that gets quietly widened is the staleness defect one level up.

### Correction to the paragraph above, and it is the same defect this ADR is about

The section as written quotes "two strata over 600" from the shape of the sequence rather than
from the subtraction. The steps are **+636, +209, +313, +12, +39, +2130, +37, +2, +1** —
*three* over 250, one of them (+313) a middling stratum with no landmark attached. A figure
quoted from memory of a distribution, inside the decision record about numbers going stale,
which is the second-order failure exactly: *apply the discipline to its own output.*

Worse, the reasoning was aimed at the wrong quantity. **A PR that moves the board and raises
the bound in the same PR leaves a distance of zero**, so no step size, however large, trips
this check — that is the rule the decision establishes. The step distribution is therefore
almost irrelevant to the slack's size, and the paragraph above is answering a question the
mechanism does not ask.

What the slack must actually absorb is **corpus drift between fetches**: the suite is not
SHA-pinned (#42), so upstream adding vectors moves the actual with no local change and nobody
to raise the bound. 250 stands as the number — roughly 6% of a 4162 board, well above
upstream's observed churn and far below any real regression — but the *justification* is
corpus drift, not stratum size. Which sharpens the earlier consequence: when #42 lands, this
constant does not merely become unnecessary, it becomes **the last remaining reason** the
counts are not exact.

## The other three bounds were current, which is the argument

`passFloor` 4162/4162, `binaryFailCeiling` 0/0, `textFailCeiling` 0/0 — all current at the
time of filing. They are current **by attention**, and decision 0005 says attention decays
across session boundaries. Three of four being right is the same evidence as one being
wrong: nothing distinguished the three from the one except that someone happened to be
looking. The control is what converts that from luck into a gate.

## Why not the other two options

**Option 2, derive the floors from a committed board snapshot.** Rejected, and the reason
is a fact #87 did not have: **the spec suite is not SHA-pinned** (#42 — `scripts/fetch-spec-tests.sh`
does `git clone --depth 1` of the default branch). So the board count is a function of
whenever the corpus was fetched, and an exact committed snapshot would fail on upstream's
schedule rather than on ours — a control that fires for reasons that are not findings, which
is the budget-by-the-quantity-the-purpose-names failure. A floor-plus-slack is the right
instrument *precisely because* its input is unpinned; an exact snapshot becomes available if
and when #42 lands, and this ADR should be revisited then. (Contrast 0012, whose inputs are
both committed, and which therefore gets the exact golden file this option wanted.)

**Option 3, print the slack and leave it to review.** Rejected: it is what already happened.
The slack *was* printed — `t.Logf` on the line above the constant — and the staleness
survived 15 commits anyway. An option whose failure is already in the record is not a
candidate. Its one merit is retained: the slack is printed *as well as* asserted, because a
bound that fires should say by how much.

## Consequences

- Every board bound now states two numbers, and the second is the one that keeps the first
  honest. A PR moving the board past a slack window must raise the floor **in that PR** —
  which is the same rule as *update `[Unreleased]` in the same PR as the change*, applied to
  a number instead of a changelog.
- `TestEveryBoardBoundIsChecked` reads the AST of `spec_test.go`. It needs a vacuity floor
  of its own: a bound-finding walk that finds zero bounds agrees with any helper usage. The
  floor is 4 (the current population), asserted as a minimum so a fifth bound arriving is
  covered rather than ignored.
- The two zero ceilings are documented as *structurally immune* rather than left silent, so
  a future reader does not "fix" the omission by adding a slack term that can never fire.
- **#42 is now load-bearing for a control, not just for reproducibility.** Noted on that
  issue: pinning the corpus would let the pass floors become exact, retiring the slack
  numbers entirely. That is the better end state, and this decision is the honest instrument
  for the world as it currently is.

## Correction, appended at implementation: the space is eight bounds, not four

Everything above was written believing the population was four. `TestEveryBoardBoundIsChecked`
found **eight** on its first run, and the body above is preserved rather than edited because
the record of what was believed — and of a control falsifying its own ADR within minutes of
existing — is the part worth keeping.

The four unnamed ones were `unsupportedCeiling`, `unimplementedCeiling`, `totalFloor`, and
`filesFloor`. Two consequences, both of which changed the design:

**A ceiling goes stale in the opposite direction, and one of ours is doing it now.**
`unsupportedCeiling` is 60872 against an actual 60872 — current today, but it *drains*: every
capability that lands moves the actual further below the ceiling, so its gap grows on exactly
the schedule of ordinary progress. `allOnPassFloor` needed fifteen commits of neglect to rot;
this one rots by working. So `boardBound` measures the distance in the direction that applies
to the bound's kind, and getting that backwards would yield a check that never fires and looks
identical to one that does — the #34 partition lesson, which is why the falsification pass
below drives each direction separately.

**A third kind exists: the vacuity floor, exempt by design.** `totalFloor` (2000 against 2143)
and `filesFloor` (230 against 242) are plausibility bounds in
`TestBareModuleSpansAreNonEmptyAndPlausible`, and looseness is their *function* — they exist to
catch a walk that found nothing, so they must sit far below the real figure to survive ordinary
corpus movement. Slack-checking them would fire on a control that is working exactly as
designed, and a gate that fires for reasons which are not findings trains the reflex of
scrolling past it (0005's lint-noise argument). They therefore route through `boardBound` with
a `vacuityBound` kind that names the exemption, rather than staying outside the door: *a
precondition that excuses a gate is licensed at one place, or it is a hole.* The exemption is a
property of the kind, not of a caller remembering to pass 0.

So the final partition is three kinds — three slack-checked board counts, three at-terminal
zeros, two vacuity floors — and the reason the trigger caught what the ADR missed is that it
keys on the **naming convention** (`*Floor`/`*Ceiling`) rather than on a list. An enumeration
would have agreed with the wrong count, silently. *A control scoped to the current sample
inherits the current blind spot* — here the sample was the author's memory.
