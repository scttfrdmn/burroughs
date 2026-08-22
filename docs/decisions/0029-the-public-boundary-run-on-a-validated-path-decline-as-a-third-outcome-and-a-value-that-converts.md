# 0029 — The public boundary: `run` on a validated path, decline as a third outcome, and a `Value` that converts

Date: 2026-08-15 · Status: **accepted** (chat-Claude, PR #302, under the stability clause below).
Held `proposed` 2026-08-15 → 2026-08-15, one PR wide; the interval is recorded rather than erased.

Filed against **#299** (`type:decision`, `phase:v0`) and milestone **v0 interpreter**.

## The stability clause, which is what makes this ratification sufficient

**The surface this record defines carries no compatibility promise before `v1.0.0`.** Anything exported
outside `internal/` — `Instantiate`, `Config`, `Instance` (`Call`/`Exports`/`Decline`/`Deferred`), `Value`,
`Type`, `Kind`, `Trap`, the five sentinels, the seven exit codes — may be renamed, re-shaped, or removed in
any `v0.x`, and callers get no deprecation window. That
is not a hedge bolted on to cheapen the stamp; it is 0004's scheme applied where it lands hardest: `v0.x`
is *a privileged place to live*, freedom to break is the privilege, and a public API is the first thing
that privilege is actually worth anything for.

The clause is load-bearing for **who may stamp this**. A public API is normally Scott's call, because it
is the first artifact anyone outside the repo can depend on — and "can depend on" is exactly what the
clause removes until `v1.0.0`. With the promise withdrawn, the cost of a wrong shape is a rename in a
`v0.x`, so chat-Claude's ratification is sufficient here and Scott's veto stands as usual.
(Ruling: chat-Claude, PR #302: *"state in 0029 that the surface carries no compatibility promise before
v1.0. With that in it, my ratification is sufficient and Scott's veto stands as usual."*)

**Why it did not stay `proposed`:** the code this record describes is in `main`. A record held open while
its implementation ships is the drift shape in a new location — the gap between what the repo does and
what the repo says it decided — and it is the shape a `Status:` field exists to close, not to host. The
grounds were already ruled: Scott's words on the two *design questions* are quoted verbatim under each
below. What was unstamped was this record's wording, plus the one thing never put to him at all — the
premise correction in decision 2's guard, a measurement that contradicts a sentence in his own ruling.
A `Status:` is a citation to an approval (the ruling on #142), so both the stamp and the interval it
spent open are kept above.

**Revised once while still `proposed`, from the implementation.** Three things the writing of the code
established, which is why this record is worth revising rather than annotating: the guard shipped on a
different warrant than the one sketched below (§decision 2), the validator's decline message is worded
differently from what this file first claimed (§decision 1), and a **fifth** sentinel had to exist — a
gated proposal is not a malformed module, which was grave **#301**. Revising a `proposed` record is not
amending an accepted one; it means Scott rules on the taxonomy as five rather than being handed four and
a footnote.

## Question

Measured on `main` at `676aa29`: exactly one package sits outside `internal/` — `cmd/burroughs`,
`package main`, **zero exported identifiers** — and it imports **only `internal/binary`**, so it cannot
reach the interpreter at all. `inspect` is a section dump. All **59682** passing assertions were obtained
through one consumer, `internal/spec`.

So the boundary a user would cross has no vectors *and no path*. That is this repo's own coverage law at
project scale — the probe domain is short by one dimension, namely **how the thing is called** — and it
is the same defect as the executable-bit specimen, where four probes ran `sh scripts/citecheck.sh` and
both binding consumers ran `./scripts/citecheck.sh`.

Three things have to be decided before any of it can be written, because each one is a commitment that is
expensive to reverse once a user has imported it.

## Decision 1 — The public path validates, and **decline is a third outcome, not a failure**

Scott's ruling, verbatim:

> **Validator on the public path: yes, and decline is a third outcome, not a failure.** The two-way
> framing is the trap. Invalid means refuse, with the rule named. Out-of-vocabulary means run, with the
> construct named on stderr — not silently, and not by refusing. Collapsing decline into either bucket is
> the same mixture error as treating gated as failed on the board, and this project already refuses to
> make it there. Add `--strict` to make declines fatal for anyone who wants the carve-out closed today,
> with the default flipping when the vocabulary completes — same self-retiring shape as 0025.

So `internal/validate.Module` runs on the public path, and its result is read as **three** outcomes:

| outcome | condition | behaviour | `--strict` |
| --- | --- | --- | --- |
| **valid** | the validator typed the whole module | run | run |
| **invalid** | a rule was violated | **refuse**, naming the rule | refuse |
| **decline** | a construct is outside the validator's vocabulary (#9's deferred slices) | **run**, naming the construct on stderr | refuse |

The mixture argument is the load-bearing one and it is already settled elsewhere in this repo: the board
scores a vector for a gated proposal `gated`, never `failed`, precisely because a count that mixes "we
answered wrong" with "we did not ask" cannot be read in either direction. A validator that reports
`error` for both a type violation and an unvisited opcode has made that same mixture at the API, where the
consumer is a `switch` in someone else's program rather than a column in a table.

The default flips to refuse-on-decline when #9's vocabulary completes, which retires the flag's reason for
existing — the same self-retiring shape as ADR 0025's carve-out, and it is tracked the same way, by the
tripwire that fails when the last declining slice lands rather than by an intention.

### What validating on the public path does *not* buy: **142 admissions**

A decline is a *named* gap and `--strict` can close it. The stratum that neither names nor closes is the
accept direction: **142** `assert_invalid` vectors this validator **silently accepts** — modules the spec
says are invalid, which slice 1 types without complaint and reports as fully valid. The figure is not an
estimate; it is `validateAdmitCeiling` in `internal/spec/boardbound_test.go`, an exact re-based bound
beside its declined sibling (1059) inside the whole validator stratum (1201).

Stated here because it is the honest limit of "the public path validates", and because it is invisible to
the very mechanism the rest of this decision rests on. A decline arrives as a sentinel a caller can branch
on; an admission arrives as `nil`, indistinguishable at the boundary from a module that was genuinely
checked, and no `--strict` or third outcome can separate them — the validator has to *acquire the rule*.
So `Instance.Decline() == nil` means "no rule was missing that this validator knows it lacks", which is
weaker than "this module is valid", and the 142 are why the distinction is written down rather than left
to be re-derived from a wrong answer. They drain by #9's remaining slices and are tracked by that bound,
not by this paragraph.

### Two things stated rather than assumed, both at Scott's direction

**Why decline-and-run is tolerable.** His words: *"the failure mode for an invalid module in a Go host is a
panic or a wrong answer, not a sandbox escape, and that reasoning is what makes the default defensible — it
should be written down so it gets re-examined if the host ever stops being Go."* Written down, therefore, as
a **conditional** and not as a property of the design: the engine is pure Go (a discipline, `docs/laws/engine.md`),
memory is a `[]byte` with bounds-checked access, the operand stack is a Go slice, and an unvalidated module
that does something the validator would have rejected gets a Go panic or an incorrect result — recoverable,
attributable, and inside the process's own safety envelope. It is *not* an escape, because there is no
unchecked pointer arithmetic anywhere for it to escape through. **If that premise ever changes** — a JIT, an
`unsafe` fast path, a cgo boundary the no-cgo law would have to be amended to permit — then the default
flips to refuse and this paragraph is the thing that says so, rather than the decision having to be
rediscovered from the symptom.

**The decline message is the campaign's public work plan.** His words: *"A user who hits `ref.func: not yet
validated` is reading the slice queue. That's worth more than the CLI looking complete."* So the message is
written for that reader, and it is the validator's own string passed through unchanged rather than restated
here — a generic `module uses unsupported features` would be the CLI looking complete at the cost of the
only free documentation of the frontier this project has.

**What that string actually is, corrected from what this record first claimed.** Not "the construct in the
spelling the spec uses": `internal/validate` emits its **own mnemonic plus the opcode byte** —
`validator: instruction not in this slice: memory_size (0x3f)`, where the spec's own spelling is
`memory.size` — and for a prefixed opcode it emits **no mnemonic at all**, only the two bytes
(`prefixed opcode 0xfd 0x0c`). The corpus census through the public path says the second form dominates:
the top decline is `0xfd 0x0c` at 269 module forms, and seven of the top eight are byte pairs. So the work
plan is legible to *this project* and only half-legible to a user, which is a real cost of the choice and
is recorded as one rather than smoothed over. The boundary does not paper over it by re-spelling the
mnemonic, because two names for one construct is how the CLI's testimony and the harness's come apart;
the repair, when it is taken, belongs in the validator where the mnemonic table lives, and it is not this
ADR's to schedule.

### A fourth load-time verdict: **gated**, and the fifth sentinel (grave #301)

The three outcomes above are the *validator's*. The decoder has a fourth, and the first implementation of
this boundary lost it: a module using a proposal whose gate is off in this build was reported
`ErrMalformed`. **429 module forms across the corpus** were told they were broken when the truth was that
this build has GC and memory64 switched off.

That is the mixture argument of decision 1 one channel over, and it is worse in kind than the
decline/invalid collapse, because it is a false statement about the *module* rather than an
over-strict verdict on it. `internal/binary.ErrFeatureDisabled` exists precisely to avoid it — its doc
comment says it is deliberately **not** a malformed-string, since the module is well-formed and the spec
would accept it — and the law is written down (`docs/laws/gates.md`: *gates never manufacture
malformedness*). The property was established at the decoder and **not inherited** by a new consumer of
it, which is the shape, not the file: it cost 429 wrong verdicts to re-learn one layer up.

So the sentinel set is **five**, and the load-time verdicts are four:

| sentinel | question it answers | remedy it points at |
| --- | --- | --- |
| `ErrMalformed` | the decoder could not read this | fix the module's bytes |
| **`ErrGated`** | this build has that proposal's gate off | rebuild with the gate on, or wait for the flip |
| `ErrInvalid` | a typing rule was broken | fix the module |
| `ErrDeclined` | the validator has no rule for a construct yet | nothing; it ran (see above) |
| `ErrUnsupported` | the interpreter reached something it does not implement | engine work (#9 and the gate campaigns) |

`ErrGated` is ordered **ahead** of `ErrMalformed` at the boundary, narrow-before-general, exactly as
`ErrDeclined` precedes `ErrInvalid`; in both pairs the catch-all is the one that must not be able to claim
the narrow case. It differs from `ErrDeclined` in one respect worth stating: it has **no scheduled death**.
Declines drain when #9's vocabulary completes; gates drain one stamped flip at a time and contract §9 keeps
admitting new proposals, so this classification is permanent furniture.

At the CLI it takes **exit code 6** of its own, for the reason the code taxonomy exists at all: a script
that cannot tell "rebuild with the gate on" from "fix your module" is in the position a merged
`gated`/`failed` column puts a reader of the board.

## Decision 2 — A **public `Value` type plus conversion**, not a hoisted `interp.Value`

Scott's ruling, verbatim:

> **Value: public type plus conversion.** Hoisting `interp.Value` out of `internal/` freezes the internal
> representation as public API before the GC work is done, and `ValType` has already been widened once for
> GC. Making it public now means the next widening is a breaking change for users, which is precisely the
> commitment the contract's method says to defer. The conversion is crossed once per call, not per
> instruction, so the cost is real but bounded.

The premise checks out and is in fact stronger than stated. `binary.ValType` is a struct — `kind byte`,
`null bool`, `idx uint32` — whose second and third fields *are* the GC widening (nullability normalized at
decode time per grave #180, and the indexed form's type index). And `interp.Value` has been widened **twice**
more since: `Hi` for a v128's high 64 bits (decision 0024) and `IsHost` for decision 0027's externalized-host
discriminator, the latter added because `extern.convert_any` made `Type == ExternRef && !Null` stop implying
"identity in `RefID`". A type that took two widenings in the last three slices is not a type to publish.

`interp.Value`'s own doc comment says it is "exported now rather than kept internal" for §4's host contract.
That sentence is not wrong and it is not the same claim: it is exported **from an internal package**, which is
availability to this module and to nothing else. Publishing it is the commitment 0006 defers.

So: a public `Value` in the public package, converted at the boundary. The conversion is crossed once per
`Invoke` per argument and per result — not per instruction — which is what makes the cost bounded rather than
structural.

### The exhaustiveness guard, and a premise correction it needs

Scott ordered the guard, and it is the right instrument:

> The guard it needs, since Go won't give you exhaustiveness: a test that enumerates every `ValType` kind from
> `internal/gen`'s own table and asserts each one converts, failing on any unmapped kind. Not a `default` case
> that maps something silently — derive the domain from the space, and the space here is generated already.

**The last clause is false, and copying it would put a fabricated warrant in the record.** Measured:
`internal/gen/` holds `keywordgen`, `memarggen`, `mllex`, `opcodegen`, `opgen`, and `xcorpus` — and **no
generated `ValType` table**. `ValType` is hand-declared at `internal/binary/module.go:66`. There is no
`internal/binary/valtype.go`.

What *is* derivable, which is what the guard will actually stand on:

- **The twelve abstract heaptype `kind` bytes**, exported, each written as `byte(-0xNN & 0x7F)` — derived from
  its own sleb wire form by the arithmetic `decodeHeapType` uses rather than transcribed as a hex literal.
- **`kindIndexed = 0x80`**, the sentinel for `(ref $t)`, chosen one past the sleb range so it cannot collide.
- **The eight named `ValType` vars** — `NoValType`, `I32`, `I64`, `F32`, `F64`, `V128`, `FuncRef`, `ExternRef`
  — package-level `var` rather than `const` because a struct is not a Go constant.

And two space-derived instruments already read that space: `declaredValTypes` (`module_test.go`) enumerates the
named vars **by walking composite-literal `ValueSpec`s in the source**, and `TestHeapKindsAreWhatTheReaderProduces`
closes the loop from the decoder's end by decoding all twelve forms and comparing.

So the guard is buildable exactly as ordered and stays derived-from-the-space; only its **warrant** differs — an
AST-enumerated and decoder-confirmed domain rather than a generated table. The consequence is structural and worth
stating, because it is the reason this is not a one-line test: `kindIndexed` is **unexported**, and the five numeric
`kind` bytes are spelled only inside the named vars, so a test in the *public* package cannot enumerate the kind
space at all.

**What shipped, and why it is not the two-halves sketch this record first carried.** The sketch had
`internal/binary` export "the kind space as a derived enumeration" — a list of bytes — and the public test
range over it. Writing it showed that a byte list is the wrong export: a `Kind` byte with no constructor is
not a `ValType`, so the public test would have had to reassemble types from bytes, in the public package,
duplicating decoder logic it cannot see. The shipped shape exports the **constructor** instead:

- `binary.AbstractRefType(kind byte, null bool) (ValType, bool)` (`module.go:281`) — the one function that
  turns a heaptype byte into a type, declining every byte that is not one of the twelve. The public guard
  then derives its reference domain by **sweeping all 256 bytes in both nullabilities** and keeping what is
  accepted (`convert_test.go:138`), so a thirteenth heaptype enters the domain the day the decoder learns it,
  with nobody adding a line.
- The **named** half still cannot be swept — a `ValType` is a struct with unexported fields and no
  byte-to-numeric constructor is exported — so it is *bound* by name in `engineNamedValTypes` and the **name
  set** is derived by an AST walk of `internal/binary/module.go`
  (`declaredValTypeNames`), checked **bidirectionally** against the binding by
  `TestConversionDomainMatchesTheDeclaredValTypes`. Same shape as `declaredValTypes` upstream, for the same
  reason: a domain typed beside the table it checks is a list, and a list cannot notice an addition.
- The indexed form is appended explicitly (`RefType(0, false)`, `RefType(7, true)`), both nullabilities,
  because it has no byte to sweep for.

`TestEveryEngineTypeConvertsBothWays` then asserts a **round trip** over that domain, not merely that a
conversion succeeds: a mapping that dropped nullability or the type index would satisfy "no unmapped kind"
and still hand a host the wrong type, and `(ref null $3)` versus `(ref $3)` is one bit that decides whether
null is a legal value.

The constructor's own vacuity guard lives upstream, and it is the guard the public one cannot carry for
itself: `TestAbstractRefTypeDerivesTheTwelve` (`internal/binary/valtype_test.go:852`) asserts the sweep
accepts exactly twelve bytes and agrees with `HeapTypeName` in both directions. Without it, a constructor
that accepted nothing would make the boundary's exhaustiveness test pass over an **empty** reference space —
*a comparison against an empty set succeeds*, aimed at the newest instrument in the repo.

No `default` arm that maps something silently. An unmapped kind returns an error naming the kind byte, which is the
same discipline as `ErrUnsupportedOp` — a stated gap rather than a wrong value. The zero `ValType` is the one
documented refusal, and it is asserted to be the only one: grave **#300** was `Kind()` promising `ok == false`
for `NoValType` and returning `(0x00, true)`, found by writing this conversion, since the boundary is the
first consumer that has to tell "not a kind byte" from "a kind byte I have no mapping for".

## Decision 3 — The exported surface is instantiate, call, result, and nothing else yet

The minimal surface Scott named — "instantiate, call an export, return the result" — and no more, because 0006's
premature-generality rule applies hardest at a boundary that cannot be narrowed later. Imports, host functions,
memory access from the host, and the §4 host contract are all *not* in this decision; they arrive with the
consumers that need them, and §4's are v1's.

The one thing this decision does fix is that `run` and the embedding API are **the same path**, not two. The CLI is a
consumer of the exported package, so the coverage the vectors buy through one is coverage of the other. A `run` that
reached into `internal/` directly would recreate the defect this ADR exists because of: two paths, one of them
unprobed.

### How the vectors run through it: **a differential, not a second board**

Scott's direction was *"conformance-shaped coverage through that path rather than through the harness"*, and
the shape that took is worth recording because the obvious reading of it is unbuildable. The engine has
**1328** known fails on `main`. A conformance test at the public boundary asserting that every vector
returns the spec's expected result therefore cannot be written; and a test asserting a *ceiling* on
mismatches would be a **second fail ledger over a differently-filtered sample** — two numbers that must be
kept in agreement by hand, which is exactly what *one concept, one trigger* forbids.

So the assertable claim is **agreement between two paths**, not correctness on one. Every vector the driver
can spell is driven down both the public API and `internal/interp` directly, in lockstep, and the assertion
is that the two never disagree. That licenses reading the existing board as covering the public path too:
the board owns the fails, and this test owns the claim that the boundary does not add any.

Two properties make it an instrument rather than a ceremony:

- **The two converters are deliberately independent implementations.** `specToPublic`/`publicToSpec` and
  `specToRaw`/`rawToSpec` share no helper, because a shared one lets a single defect satisfy both arms and
  the differential would agree with itself. Verified by falsifying each direction separately: a perturbation
  at the boundary produced 16183 disagreements and 0 vector mismatches, and one inside the interpreter
  produced 0 disagreements and 16183 vector mismatches — the two failure modes are distinguishable, which is
  the whole reason for two arms.
- **A `trusted` bit**, because a driver that cannot model a command cannot keep pretending it knows the
  state. Any command the driver skips which *could* mutate an instance invalidates every subsequent
  assertion against it. This has **two** sites, not one — a standalone `invoke`, and an `assert_return`
  whose *arguments* are unspellable — and installing it at the first alone left 5 confident, tidy
  "engine defects" in `simd_const`/`simd_align` that were the driver's own bookkeeping. *Exactly small
  enough to believe* is what made them worth chasing.

### The vector census stays an assertion, and its domain is not the one the ruling assumed

This was flagged open in the implementing PR: the vector-level mismatch count is *asserted* at zero as
well as logged, which is stronger than the differential strictly needs and arguably the second ledger this
design was built to avoid.

**Ruled: it stays an assertion** (chat-Claude, PR #302). The grounds sharpen the design rather than merely
permitting it — *"a differential's value is that disagreement is a defect by construction. Same module,
same export, two paths; if they differ, one is wrong, and there is no legitimate population to census."* A
flake here would not be noise to absorb; it would be **nondeterminism between two paths in one engine**,
which is the most valuable thing this instrument can find, and a log is where that gets buried. With the
consequence stated: if exemptions ever become necessary they are **enumerated by name with a reason each,
on the nose — never a tolerated count**, which is the second-ledger shape itself.

The ruling also read the module census as saying the declines carry the legitimate exclusions, so that the
comparison's domain is the **1067** fully-checked modules. **Measured, that is false.** A decline is not a
refusal — it says this validator slice could not check every instruction, not that the module is rejected —
so a declining module instantiates, arms, and has its exports called like any other. **11166 of the 25666
comparisons, 43%, run on a declining instance**, and the domain is **1787 module forms (1067 ran + 720
declined)**. Recorded here for the reason the guard below is: *check a ruling's premises, not just its
conclusion*. The conclusion is unaffected and implemented as ruled; the number is corrected, the split is
now printed by the test, and the legitimate exclusions are the other named buckets — 429 gated, 22
encoder-frontier, 26146 no-instance, 78 unpassable, 599 identically-failing calls.

## The `unsupported` delta is **derived**, not forecast — a D row

The product law requires every PR to state its `unsupported`-column delta, and this PR's is **zero**. Two
readings of a zero already exist: a *confession* (overhead that moved nothing), and the gate-campaign
*structural* zero, where a gated vector cannot reach the column before its flip (Scott, PR #235). This PR
is neither, and the third case is ratified with a refinement about what kind of claim the zero is:

> *"Unsupported measures questions the harness cannot ask; a PR that adds a consumer changes who asks, not
> what can be asked. So the zero isn't a forecast that came in — it's derived, and measuring it against a
> worktree at main checks the derivation rather than confirming a prediction. Record it as a D row. Same
> distinction as the flip."* (Ruling: chat-Claude, PR #302.)

**Provenance: derived.** Two premises:

- `internal/spec/wast.go:4352` — `r.Unsupported++` sits in the **`default:` arm** of the harness's command
  dispatch, and the bucket key is the command's *head atom* rather than its kind, "because every
  unsupported command has `KindUnsupported`". The column therefore counts commands `internal/spec` has no
  case for: it is a measure of that package's command vocabulary.
- `git diff --stat main..HEAD -- internal/spec/` is **empty**. This PR adds no command kind to the harness,
  because it does not touch the harness.

Inference: a column whose population is the harness's missing command kinds cannot move by a diff that adds
no harness command kind. The delta is zero **by entailment**, before any measurement — which is what makes
it a D row rather than a pre-registered forecast. The two boards were nevertheless measured on both sides
(a throwaway worktree at `main` prints `59682 pass, 1328 fail, 83 unsupported, 4051 gated, 0 unimplemented`,
identical), and that measurement's job is to **check the derivation** — if the columns had differed, the
premise about what the column measures would be the thing that was wrong.

This is the same distinction the flip turns on. A flip's forecast is *pre-registered* precisely because its
numbers do not exist until the mechanism does, and a prediction stated afterwards is the actor choosing the
instrument that judges the actor. A derived zero has the opposite structure: it follows from a property of
the instrument that is true before the diff, so stating it afterwards costs nothing and measuring it is a
check rather than a confirmation. The reward figure this PR is judged on is elsewhere — the differential's
own census, 2238 module forms and 25666 compared assertions through the published API.

## Consequences

- The engine acquires its first public API, and therefore its first compatibility surface. `v0.x` is the privileged
  place for that (0004) and it is why this lands now rather than after v1.
- **Coverage moves from the harness to the boundary.** Conformance-shaped vectors run *through* the public path, so
  "the engine works" stops being a claim about `internal/spec`. Measured on the corpus: 2238 module forms and 25666
  compared assertions cross the public API, against a board that had crossed it zero times.
- A third outcome exists at the API and every consumer must read it. That is a cost, paid deliberately, for the same
  reason the board keeps `gated` out of `failed`. A **fifth sentinel** (`ErrGated`) and a **seventh exit code** are
  the same cost paid again, one channel over, and grave #301 is what it cost to not pay it the first time.
- **Two graves came out of writing the boundary, both in code the suite already covered** — #300 (`Kind()` reporting
  the zero `ValType` as a kind byte) and #301 (gated reported as malformed). Neither was reachable from
  `internal/spec`, because neither is a question a vector asks. That is the coverage law's dimension —
  *how the thing is called* — producing findings on its first use rather than in an argument about it.
- `--strict` is a flag with a scheduled death. It is a carve-out on #9's frontier and it retires when the frontier
  closes; a tripwire holds that, not this sentence.
- The two riders on the implementing PR delete rather than refresh two stale measured claims (`CLAUDE.md`'s
  `internal/interp` line counts and board figures, `exec.go`'s "what is deliberately absent"), on the rule that **any
  sentence asserting a measured quantity is generated or deleted**. A refreshed number rots on the same schedule as
  the one it replaced.

## Alternatives declined

- **Hoist `interp.Value`.** Declined by Scott's ruling above; the type has taken three widenings in recent slices and
  publishing it makes the fourth a breaking change.
- **Two-way validation (valid / invalid).** Declined as a mixture error: it either refuses every module touching #9's
  deferred vocabulary, which makes the CLI useless while the campaign runs, or it runs them silently, which hides the
  frontier from the only reader positioned to care.
- **Skip validation on the public path.** Declined. The validator is the thing that makes a refusal *nameable*, and an
  engine whose public entry point cannot say which rule a module broke has no better answer than a panic.
- **A `run` that calls `internal/interp` directly, with the embedding API added later.** Declined: two paths where one
  is unprobed is the defect being repaired, restated.
- **A mismatch ceiling at the public boundary.** Declined: a second fail ledger over a differently-filtered
  sample, which has to be kept in agreement with the board by hand and drifts silently when it is not. The
  differential asserts *agreement between paths* instead, and leaves the fails where they already have an
  owner. (*One concept, one trigger*, #82.)
- **One shared spec-value converter used by both arms of the differential.** Declined, and it is the
  tempting one: less code, obviously equivalent. It is also the change that makes the instrument agree with
  itself — a defect in the shared helper corrupts both arms identically and the test goes green on it.
- **Gate selection on `Config`.** Declined as a scope statement rather than an omission: a public consumer
  gets `binary.DefaultFeatures`, the gates §9 has flipped on their own stamped decisions. The all-gates-on
  lane is a *measurement instrument* whose defining property is that its suites are not green, and
  publishing it would ship that configuration as an option.

## Pointer amendment, appended 2026-08-22 — one re-pointed premise, and one word the drift showed was never true

Two edits to the D-row section above, both in the Provenance clause, and they are different kinds of
thing.

**The pointer.** The first premise cited `internal/spec/wast.go:2672` for `r.Unsupported++` sitting in
the command dispatch's `default:` arm. That line holds the `isGated` closure in `run`'s preamble; the
`default:` arm's increment is now `internal/spec/wast.go:4352`. Re-pointed by that statement's own
text. The premise itself is unchanged and still true — the arm is keyed on the command's head atom, so
the column measures `internal/spec`'s command vocabulary — which is exactly the half of a citation a
reader does not need and the half they do coming apart.

**The word.** The clause read *"Premises, both mechanically checkable"* and now reads *"Two
premises"*. This is not a consequence of the drift, it is a finding the drift exposed: **nothing in
this tree can mechanically check either premise**, and nothing ever could. The second is a `git diff
--stat` invocation, which is checkable by running it and is not a standing check; the first was a line
pointer, whose target `make cite` does not resolve — `citecheck` reads issue, grave and ADR tokens, and
a `<file>:<line>` is outside its domain
([#456](https://github.com/scttfrdmn/burroughs/issues/456)). So a premise offered as machine-verifiable
sat wrong for an unknown interval with every gate green over it, which is the strongest available
demonstration that the word was decorative when written.

The word is dropped rather than repaired, and no checker is built for it. Scott's ruling on the #486
review: *"the premise loses the word, because the drift didn't make it false — it revealed the word was
never true of that premise. Declining to build the checker is correct."* A claim of mechanical
checkability is itself a claim about an instrument, so it is subject to the same rule as any other: name
the instrument or do not make the claim. Filed as
[#485](https://github.com/scttfrdmn/burroughs/issues/485), which found this pointer alongside two others
in a sweep prompted by an unrelated insertion; the sibling ADR note is in 0028.
