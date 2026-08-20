# 0040 — `matches` forks on the pattern's family, and the type the harness cannot name gets a sentinel Kind

Date: 2026-08-20 · Status: **accepted** — ruled by Scott on
[#441](https://github.com/scttfrdmn/burroughs/issues/441#issuecomment-5361678300), three rulings, one
per decision below. The ruling was given in session and is transcribed onto the issue verbatim so
that this field cites an artifact rather than an order nobody else can read; the transcription is
labelled as one, because a forged provenance about the project's own governance is worse than a wrong
option.

## Context

[#441](https://github.com/scttfrdmn/burroughs/issues/441) is an **accept-direction** finding:
`matches` gated every comparison on `want.Kind != got.Kind`, a static-type equality the authority
does not have. `assert_ref_pat` (`third_party/spec/interpreter/script/runner.ml:464-476`) dispatches
on the runtime **constructor** and reads no static type at all, and `RefResult (RefPat r)` compares
two concrete references where neither side's static type is an operand.

No negative-direction vector can witness a wrong acceptance — a corpus of rejections cannot say what
a comparison wrongly admits — so the evidence had to be a census of the passing population, taken
before any diff. That census is `internal/spec/kindgate_test.go`, and it reported two things worth
carrying into this record:

- **Question 1's zero is analytic.** The issue asked which currently-passing vectors reach the gate
  with unequal kinds, and required the answer to be derived rather than argued. It is 0 in both lanes
  and *could not have been anything else*: the gate answering false fails its vector, so a refusal is
  a fail-column event by construction. A required zero that could not have come out otherwise answers
  nothing, so the census grew a second channel — the gate's reaches on the runs that *failed* — which
  is the only place this gate can be observed refusing anything.
- **Seven refusals, and in every one of them `got.Kind` was a documented placeholder.**
  `try_table.wast:464-466` (a `(ref.func)` pattern against a `func`-constructor reference typed
  `(ref null $t)`, which ValKind has no idx axis to name) and `local_init.wast:21,22,23,74` (a
  `(ref.extern N)` identity against a host reference carrying the same identity, typed `(ref extern)`,
  which `valKind` declines on the null bit alone). So the gate was not comparing two static types. It
  compared a real static type against a value whose type the harness had already said it could not
  name — and `fromInterpValue`'s own comment recorded that the placeholder was **chosen to make this
  gate agree**. That inverts the recorded dependency: the gate was the reason the placeholder had to
  be picked carefully, so removing the gate retires the constraint rather than breaking anything.

## Decision 1 — the reference and numeric families fork, and each states its own precondition

`matches` forks on `want.Kind.isRef()`. The reference family requires `got.Class != RefNone` and then
switches on `want.Class`, reading no static type on either side. The numeric family keeps
`want.Kind != got.Kind` unchanged.

The alternative considered was to keep one shared equality and widen what counts as equal, so that
the seven pairs agree. **Both arrangements produce identical boards in both lanes**, which is the
finding rather than a convenience: two candidate fixes agreeing on the board is the corpus declining
to choose, and a decision taken on an agreement it cannot see is a decision with no stated reason. So
the choice went up, and Scott's criterion was which arrangement *says why it should hold*:

> The split does — the reference path reads no static type because reference identity was never
> carried there; numeric keeps kind equality because `i32 1` and `i64 1` are genuinely different
> claims. The alternative preserves an agreement without saying why it should hold.

`rec.record(want, got)` stays **above** the fork, so the census's pins keep measuring the same event
before either family answers. The census's vocabulary moved with the code: "refused by the gate"
became "the two kinds differ", because what a recorder placed at the fork can see is the pair and
never the verdict. The old label got away with the verb only because a reference pair with unequal
kinds *was* refused — the defect stated as the rule, in an instrument's own output.

Because no vector discriminates the fork's edges, its behaviour is pinned by hand-built matrix rows
in `internal/spec/refboundary_test.go` against the reference's own arms, not by the board.

## Decision 2 — the four `local_init.wast` rows ride #441, and the measurement is what settles it

The seven rows split by **which authority adjudicates them** — `assert_ref_pat` for three,
`RefResult (RefPat r)` for four — and that was an argument for two issues. Scott's ruling:

> An issue's scope is set by the mechanism, not by the sentence that filed it — the two authorities
> are a fact about who reads the value, not about the defect, and all seven share the same one.
> Splitting on the authority would leave one issue closing with no diff. Measure it: any row the edit
> leaves standing gets its own issue with the residue as its subject.

**Measured: 7 of 7 cleared**, so there is no residue and no residue issue. `local_init.wast` still
carries 4 fails in the all-on lane and they are *not* the residue — they are that file's
`assert_invalid` vectors, a validator gap with no local-initialization rule behind it, filed as
[#452](https://github.com/scttfrdmn/burroughs/issues/452). Stated here because the file name is the
same and the adjacency invites the wrong reading.

## Decision 3 — the placeholder becomes a sentinel, `KindUnnameableRef`

> Its only recorded reason is that it makes the gate agree. Once nothing reads it, that's a
> plausible-looking constant with a dead reason and a doc comment asserting something false — exactly
> the thing the next reader infers a reason for. Pick a value that fails loudly if read, or make the
> field absent. Either way the gate sentence leaves the comment in the same diff.

`ValKind` gains a ninth member, appended: `KindUnnameableRef`, `isRef()`-true, printing
`unnameable-ref` — a spelling with a hyphen in it, because no Wasm type name has one, so it cannot be
mistaken for a type the harness claims to know. It is refused **by name** in `valType`, the argument
side's only route to a `binary.ValType`, so a sentinel cannot reach the engine as an argument.

Two placeholders, one reason each, and only one of them was this:

- The **null** placeholder stays `KindFuncRef` (grave #266). Its reason was never the gate — a null
  has no heaptype in the reference (`runtime/value.ml:20`) and `TestRefNullMatchesAcrossTwoHeaptypes`
  pins that nothing reads it.
- The **non-null** placeholder was arbitrary *and read*, which is what makes its replacement a
  sentinel rather than a rename.

`refPatterns`' Kind column changes under the same rule: `func`, `extern` and `any` keep the kinds
`valKind` can name, and the five whose heaptype ValKind has no member for — `eq`, `i31`, `struct`,
`array`, `exn` — carry the sentinel. The column used to be chosen by asking what the *got* side would
produce, so that the gate stayed inert; that reason is deleted, so the column is a statement now and
no longer a mechanism.

## Consequences

**The board.** Default lane unchanged at 60928 pass, 0 fail, 29 unsupported, 4187 gated, 0
unimplemented over 256 files — the seven rows are honestly `gated` there. All-on lane **65042 → 65049
pass** and **38 → 31 fail**, matching the pre-registration exactly. `allOnFailCeiling` re-bases to 31,
and the re-base is recorded as a **re-base and not a drain**: what left that column is harness error,
not engine error, and reading a fall of 7 as capability would credit the interpreter with work it did
not do. The `unsupported` delta is **0 and structural** — this changes what the harness *answers*, not
what it can *ask*.

**The census earned its keep on an edit that was not aimed at it.** `kindGateRefReaches` went 118 →
125 in the all-on lane, the +7 being the seven rows themselves (a row can only be counted on the pass
path, so their absence before was structural), and then **stayed at 125 while `kindGateUnequalPasses`
went 7 → 11**. One figure moved, one did not, with a reason each: reaches count *arrivals* and the
sentinel is `isRef()`-true, so no arrival changed; the map counts a *comparison between two Kinds*,
and splitting one member in two turned four previously-equal pairs (`extern.wast:53,54,55`,
`struct.wast:122`) into four admissions that had to be adjudicated. They had been passing the gate by
coincidence, under a shared spelling.

**A cancelled error pair lost one half, and the other half is now live.** `RefPat.admits` has
recorded since 0039 that it cannot tell an externalized aggregate from a bare one — externalization is
a *wrapping constructor* in the reference (`runtime/extern.ml:7`) and a sibling field here — but the
Kind gate refused those rows first, for an unrelated reason, so the harness agreed with the reference
by cancellation. Removing the gate uncovered it: `(ref.i31)`/`(ref.eq)`/`(ref.struct)`/`(ref.array)`
now admit an externalized aggregate that falls to `| _ -> false` at `runner.ml:477`. Filed as
[#451](https://github.com/scttfrdmn/burroughs/issues/451), pinned as a matrix row with 0 corpus
vectors on either side of it, which is the class of fact only the reference can falsify (contract §9
G-3, [0007](0007-opcode-table-authority.md)). The mirror case went the other way and now
**agrees**: `(ref.any)` against an externalized value is admitted at `runner.ml:468`, and the gate used
to refuse it. Removing an error can understate a divergence exactly as easily as it can repair one.

**#450's two halves separated.** Its message half is discharged, and not by fixing it: `Val.String()`'s
`RefExternIdentity` arm was a two-way `if`, and the else branch served both a genuine bare
`(ref.host N)` and a result whose type had been refused — so those rows printed `ref.host 3` for a
value the reference calls `ref.extern 3`. A two-way decision cannot report that it does not know. The
arm is now three-way plus a malformed-Val default, and the rows print `host identity 3,
externalization not carried`: a move from fabricating to declining. #450's naming half — `valKind`
declining `(ref extern)` on the null bit alone — is untouched and now unblocked.

**Ruling 3 swept past `internal/spec`.** Two independent transcriptions of the placeholder live in
`publicpath_test.go`, the public boundary's differential. `rawToSpec`'s carried the same defect as
#450's subject — a comparison against `binary.FuncRef`/`binary.ExternRef`, which are the *nullable*
spellings — so a non-null `(ref func)` result came back named `anyref`; it now reads the heaptype byte
and names all three nameable heaptypes, with the sentinel as the fallback. Measured: **0 corpus rows
reach that fallback**, confirmed by neutering the `anyref` arm and watching the differential stay
green, so the repair is a statement about what the arm would answer. `publicToSpec`'s refusal of the
remaining GC kinds stays a refusal, but its *reason* changed — the sentinel means a mapping is now
available — and the reason it is not taken is the measured zero rather than a principle. `specToPublic`
gains a sentinel arm, which `exhaustive` asked for on the nose.

**The fifth report emptied a table.** `TestGrave206KnownFailures`' allowance list drained to empty as
`try_table.wast`'s three rows went green, and an emptied table takes its own domain with it: that test
ranged over the allowance map's keys, so draining it would have silently killed the live "any fail
must be cited" arm. Domain (`governed`) is now separate from allowance (`known`, empty), and the
vacuity arm asserts a pass floor per governed file. The three stale lines in its header went stale
because this decision stopped the placeholder being refused, **not** because the harness limitation
they described was fixed.

**Not decided here.** #450's two options for `valKind`; #451's two directions for carrying
externalization; #452's local-initialization rule. Each is its own issue with its own oracle.
