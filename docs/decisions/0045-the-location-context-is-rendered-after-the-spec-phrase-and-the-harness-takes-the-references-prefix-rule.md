# 0045 — The location context is rendered after the spec phrase, and the harness takes the reference's prefix rule

Date: 2026-08-22 · Status: **accepted** — option 4 approved by Scott on the #490 review, relayed to
[a durable comment](https://github.com/scttfrdmn/burroughs/issues/455#issuecomment-5381971036).

Filed against **#455**. Also discharges the fifth approval term of
[0044](0044-the-local-initialization-rule-is-a-per-frame-undo-list-and-the-block-wall-is-performed-rather-than-inherited.md)
— *the message is born spec-phrase-first* — which that ADR recorded as owed here.

## Context

The reference matches an expected error text by **prefix**. `assert_message` (`script/runner.ml:498-501`)
is `String.length msg < String.length re || String.sub msg 0 (String.length re) <> re`, negated —
`HasPrefix`, with a parameter name suggesting a regex the function does not contain — and all nine
text-matching call sites in the suite go through it. This harness matched by **substring**, at six
sites in the run loop plus one in a control.

`strings.Contains` is strictly looser than `strings.HasPrefix`: everything the reference accepts we
accept, plus every message carrying the expected text anywhere after position 0. That is an
**accept-direction** divergence, so no negative-direction vector can witness it — the rows that would
are rows the suite expects to pass and we do pass, for a reason the reference would not have accepted.
#455 therefore asked for a census of the *passing* population before any option could be priced, and
`TestSubstringOnlyMatchCensus` is that census.

It came back **large**: 6542 divergent rows in the default lane, 7831 all-on. #455's own
pre-registration read that as the case for option 3 — *keep substring, record the looseness* — because
option 1 had been priced as *"an engine-wide message rewrite"*.

**The count is over rows and the price is over mechanisms.** The census's `prefixFamily` grouping,
which collapses index numbers to `N` and parenthesized opcode names to `(op)`, resolves the whole
divergent population into two mechanisms with no remainder:

| lane | divergent | `trap: ` + `interp: link failed: ` | `internal/validate` location wrappers |
| --- | --- | --- | --- |
| default | 6542 | 4262 + 76 = **4338** | **2204** |
| all-on | 7831 | 4992 + 200 = **5192** | **2639** |

The columns sum to the totals exactly. The 15 and 19 distinct prefix families are 15 and 19 spellings
of those two mechanisms, and the corroborating datum sits in the same output: `assert_malformed
(quote)` and `assert_malformed (binary)` report 1229 and 709 matches with **zero** divergences, so
`internal/binary` and `internal/text` already put their sentinel at position 0. Two of the four layers
already satisfied the constraint option 1 was priced against.

The reason is visible in the code rather than inferred from the census. Of the ~180 `fmt.Errorf` calls
in `internal/validate` and `internal/interp` that wrap an error, all but 28 already place `%w` — the
sentinel carrying the spec's phrase — at position 0. The 28 are not messages; they are **location
context**: `func %d: `, `instr %d (%s): `, `element segment %d, element %d: `, `trap: `. A message
that says *what went wrong* conforms already; a wrapper that says *where* does not.

## Decision

**Option 4: keep the location context, and render it after the spec phrase.** Then take the
reference's prefix rule in the harness.

Two parts, and the second is conditional on the first having worked:

1. **The 28 wrapper sites move their context to a parenthesized suffix.** `fmt.Errorf("func %d: %w",
   i, err)` becomes `fmt.Errorf("%w (func %d)", err, i)`; `Trap.Error()` returns `t.Reason + "
   (trap)"`; `ErrLinkFailed` moves from the front of its three wraps to the back, where `%w` still
   satisfies `errors.Is`. **No message text changes** — the same words in the same order, with the
   locational clause moved from before the phrase to after it.

2. **The seven `Contains` sites become `HasPrefix`**, which makes the harness's rule the reference's
   rule at every text-matching site.

Nesting composes because the suffix accumulates outward-last. `instrs` wraps first and `checkFuncs`
second, so the reader gets the phrase, then the instruction, then the function:

```
uninitialized local: local 2 (instr 5: local.get) (func 3)
```

The form is not invented here: `internal/interp/call.go:449` already renders `fmt.Errorf("%w (%s)",
ierr, site)` for exactly this reason, and this decision makes that site's shape the rule rather than
the exception.

### The stated cost: Go's idiom and the spec's rule compete for position 0

Go's error convention is a context prefix — `interp: link failed: unknown import` — and the reason it
is a convention is that it reads correctly when a caller wraps again. The spec's matching rule wants
the phrase at position 0. **Both cannot have it**, and this is a real loss rather than a free
improvement: `unknown import: "m" "f" (interp: link failed)` is less idiomatic Go than what it
replaces, and a reader scanning a log for a package name now finds it at the end of the line.

The tie breaks toward the spec because **the suite is the oracle** and the alternative was to hold a
known accept-direction hole open in the instrument that scores the entire engine. That is a trade in
one direction only, and it does not generalize past the packages the harness reads.

### What is deliberately not in scope

The **public** `Trap.Error()` at `burroughs.go:80`, which renders `"trap: " + Reason`, and with it the
CLI's transcript in `README.md` and `TestREADMETranscriptIsExecutable`.

The reference's domain is the engine's internals: the harness constructs its `Engine` over
`internal/interp` and `internal/validate` and never sees the root package, so no public message is in
the divergent population. The public boundary's documented invariant is that the **Reason** is the
engine's (`burroughs.go:269-274` — *"the taxonomy is the engine's"*), not that its rendering is
byte-identical to the engine's rendering, and `Reason` is passed through unchanged. So the two
renderings of one field now differ on purpose, which is stated at both sites rather than left for a
reader to reconcile.

Leaving it is the narrower change and the honest one: repositioning a public error text is an
observable API change made for consistency rather than for a measurement, in the same PR as a change
made for a measurement, and the two would be indistinguishable afterwards. If uniformity is wanted it
is a one-line follow-up with its own line in the changelog.

## The two alternatives, and why they lose

**Option 1 as originally priced — a prefix rule against every message in the engine.** This is what
#455 rejected, and correctly, against the information it had: an engine-wide format constraint on
every trap, validation and decode string. The measurement is what changed, not the argument. Of the
messages the corpus actually reaches, the constraint is already satisfied everywhere except at
location wrappers, so the "engine-wide" scope was an artifact of pricing a mechanism repair in rows.
The general commitment survives — every message must now begin with its sentinel — and that is a real
ongoing constraint on new code, which is why it is written down here rather than inferred from the
diff.

**Option 3 — keep substring and record the looseness.** The honest fallback, and the one #455's own
decision rule pointed at. It loses because the looseness it records is not bounded by anything: a
wrong message containing the right phrase passes, and the population of such messages is every string
the engine can produce. Recording it at seven sites documents a hole; it does not close one. Since the
repair turned out to cost 28 line edits, paying for the record instead of the repair would be buying
the cheaper thing at the higher price.

**Option 2 — prefix against a normalized form** was already refused in #455's body as inventing a rule
the authority does not have, and the census gives no reason to revisit it: a normalization that
tolerates `func 3: ` at the front is a normalization written to excuse the 28 sites.

## The probe becomes an analytic zero, and is re-pointed rather than retired

`noteSubstringOnly` records `Index(got, expect) > 0` from **inside the award arm**. Once the arm's
guard is `HasPrefix`, a divergent row never reaches the recording, so the census reports 0 in a world
where it could not have reported anything else. *An analytic zero is not a measurement*, and it is the
worse kind: the number stays where a reader expects a measurement, reading as *"no divergences found"*
when it means *"divergences are unobservable here"*.

The re-point moves the census from the award to the **decision**. The rule and its census become one
closure the arms ask:

```go
expectMatches := func(c Command, st Stratum, got string) bool {
	if i := strings.Index(got, c.Expect); i > 0 {
		r.SubstringOnly = append(r.SubstringOnly, substringOnlyMatch{…, Offset: i, …})
	}
	if !strings.HasPrefix(got, c.Expect) {
		return false
	}
	r.ExpectMatched[c.Kind]++
	return true
}
```

Six call sites, one predicate, and the population is observable in both directions: a divergent row is
now recorded **and refused**, so the census's 0 is a fact about the corpus and a regression that
reintroduces a prefix wrapper appears as a board failure *with the census row that names it*. The
denominator keeps its meaning — `ExpectMatched` counts awards, and awards are still awards.

`TestSubstringOnlyProbeSeesEveryArm` inverts one of its two directions with this: the divergent
fixture asserted a pass and now asserts a fail plus a recorded row, while the prefix fixture still
asserts a pass, an award, and no record. Each arm therefore still proves it reaches the closure — the
prefix row proves reachability, the divergent row proves recording — which is the property that test
exists for.

## Pre-registration

Written before the first edit. The staged census is the point: a single "0 at the end" cannot
distinguish a complete repair from a census that stopped walking, so the tail is measured separately
from the head.

| # | term | forecast |
| --- | --- | --- |
| 1 | census after stage 1 — the five sites behind the three large families: `Trap.Error()`, `ErrLinkFailed` ×3, `instr %d (%s): `, `func %d: ` ×2 | default **92**, all-on **136** |
| 2 | census after repairing all 28 sites | **0** in both lanes; any residual names a site the enumeration missed |
| 3 | `matched` denominator, both lanes | unmoved: default **8519**, all-on **9844**. The repair moves no phrase, and the flip refuses only rows term 2 forecasts at zero |
| 4 | default-lane board, all five columns | unmoved. Every phrase stays contiguous, so `Contains` still holds through stage 1; the flip's fail delta *equals* the census by construction |
| 5 | all-on board | unmoved at **65107 pass / 2 fail** — the 2 are #471's |
| 6 | test expectations pinning a *rendered* message | exactly one: `internal/validate/exception_test.go:488`. A second is a message this enumeration did not predict |
| 7 | `TestREADMETranscriptIsExecutable` | green without editing `README.md`, which is the check on the scope boundary above |
| 8 | `unsupported` | **structurally 0** — the repair changes what the runtime *says*, never what the harness can ask |

Term 1 is **derived per family rather than by subtraction**, because the two readings disagree and the
subtraction is the wrong one. The three large family *strings* account for 6446 of 6542 default rows,
leaving 96 — but `func %d: ` is one of stage 1's sites, so the `func N: ` family (4 rows) clears with
them, and the composite families (`element segment N: instr N (op): ` and its three siblings, 26 rows)
stay divergent with a *shorter* prefix rather than clearing. 96 − 4 = **92**; all-on, 140 − 4 = **136**.
A stage-1 census reporting 96 would mean the `func` wrapper was not among the sites actually changed.

Term 4 is the one designed to fail loudly. A board that moves means the repair broke contiguity
somewhere — a wrapper whose `%w` sat mid-string, most likely `internal/validate/module.go:749`, the one
site of the 28 where the sentinel is neither first nor last.

## Outcome — seven met, one falsified

Measured 2026-08-22 on the landing tree. Terms 2–5 and 7–8 from the run recorded in the PR's Board;
term 1 from the staged census taken between stage 1 and the remaining 23 sites, which is a tree state
no commit holds and is therefore quoted as of that stage.

| # | forecast | measured | |
| --- | --- | --- | --- |
| 1 | default **92**, all-on **136** | default **92**, all-on **136** | met, exactly |
| 2 | **0** in both lanes | **0** in both lanes | met |
| 3 | default **8519**, all-on **9844** | default **8519**, all-on **9844** | met, unmoved |
| 4 | default board unmoved | **60957** pass / 0 fail / 0 unsupported / **4187** gated / 0 unimplemented | met, unmoved |
| 5 | all-on **65107 / 2** | **65107 pass / 2 fail / 0 gated** | met, unmoved |
| 6 | **exactly one** rendered-message expectation | **four** | **falsified** |
| 7 | `TestREADMETranscriptIsExecutable` green, `README.md` unedited | green; `README.md` not in the diff | met |
| 8 | `unsupported` structurally **0** | **0** | met, structural |

Term 1 landing on 92/136 rather than 96/140 is the derivation's own check: the subtraction reading
would have been wrong by exactly the `func N: ` family's 4 rows, and either number was available to a
reader who had not decided which. Term 4 was the term designed to fail loudly and did not: every
sentinel's phrase stayed contiguous through the repair, including `internal/validate/module.go:749`,
the one site where it is neither first nor last.

### Term 6's falsification: an enumeration by phrase cannot see an expectation that omits the phrase

Four expectations pinned a rendered message, not one. The forecast named
`internal/validate/exception_test.go:488`; the other three are outside the *pattern* the enumeration
used, in three different ways, and none is outside the repair:

1. **`exception_test.go:480`** — the sibling row of the one that was predicted, eight lines above it in
   the same table. Both pin the same sentinel through the same wrapper; the predicted row at `:488`
   carried an `import N: ` prefix and this one carried `tag N: `, and the enumeration's pattern set had
   the first spelling and not the second. So the miss is *one call site's spelling of one wrapper*.
   Two rows of one table is the cheapest possible miss, and it is the one that makes the point: the
   enumeration was over prefixes it had thought of, and a wrapper has as many prefixes as call sites.
2. **`validate_test.go:474`** — `strings.Contains(err.Error(), "(select)")`. It pins the rendering
   while quoting **no phrase and no sentinel**: a bare parenthesised mnemonic, which the repair turned
   into `(instr 3: select)`. No grep keyed on an error's text could have found it, because the text is
   not what it asserts.
3. **`sexpr_test.go:770`** — a fixture *inside the harness package* that builds an engine-shaped
   message by hand (`"trap: " + text + " at 4"`). The enumeration's domain was the four engine layers.
   A harness fixture asserting an engine rendering is a rendering site the engine does not own.

So the miss is in the enumeration, in both its pattern and its domain, and the lesson is the
already-paid one arriving from a new direction: *derive the domain, never enumerate it* — here, the
domain of "things that assert this rendering" is not the domain of "things that quote this phrase". The
instrument that would have caught all four is the one this ADR ships: the census, which reads what the
engine *renders* rather than what a test *quotes*. It reported 0 only after all four were repaired,
which is why term 2 and not term 6 is what #455 closes on.

## Consequences

**Every new error in `internal/validate` and `internal/interp` must begin with its sentinel.** That is
now a rule with an instrument behind it rather than a style preference: a wrapper that puts context in
front of the phrase turns the vectors it touches red, and the census names the offending prefix.
`internal/binary` and `internal/text` already obeyed it; this makes the constraint uniform across the
four layers the harness reads.

**`errors.Is` is unaffected and that is why the repair is cheap.** `%w` works from any position, so
moving `ErrLinkFailed` to the end of its three wraps and `ErrUninitializedLocal` to the front of
nothing keeps every sentinel test — `link_test.go:174`, `:449`, `link_census_test.go:151` — matching
the same errors. The messages are testimony; the sentinels are the verdict channel, and only the
testimony moved.

**#455 closes on term 2 and not before.** The flip is what closes it, the flip is conditional on the
census being 0, and a residual would mean the rendering repair is incomplete rather than that the rule
should stay loose.

**The public boundary now carries a deliberate divergence.** `burroughs.Trap.Error()` renders `"trap: "
+ Reason` and `interp.Trap.Error()` renders `Reason + " (trap)"`, both from the same `Reason`. Stated
at both sites. The risk is the ordinary one for a divergence held on purpose — a later reader
"repairing" it — and the mitigation is that the internal side has a census that turns red if it moves
and the public side has `TestREADMETranscriptIsExecutable` if it moves, so neither can drift silently.

**`docs/laws/errors-and-testimony.md` gains the law this slice pays for**: *a message is not its
rendering*. Scott's mint, on the observation that 0044's fifth term was satisfiable only at a
rendering site and so could not be discharged by the code that produced the message — `instrs` owns
the text the reader sees, and an approval given on a description of the message conflated the two.
