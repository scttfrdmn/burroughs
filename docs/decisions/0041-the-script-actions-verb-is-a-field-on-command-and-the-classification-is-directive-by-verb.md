# 0041 — the script action's verb is a field on `Command`, and the classification is directive × verb

Date: 2026-08-20 · Status: **accepted** — ruled by Scott on
[#323](https://github.com/scttfrdmn/burroughs/issues/323#issuecomment-5364724819), one ruling with
one condition attached. The ruling was given in session on the #460 merge relay and is transcribed
onto the issue verbatim so that this field cites an artifact rather than an order nobody else can
read; the transcription is labelled as one, because a forged provenance about the project's own
governance is worse than a wrong option.

## Context

[#323](https://github.com/scttfrdmn/burroughs/issues/323) is the last 11 rows of the board's
`unsupported` column: `(assert_return (get $M "x") …)` vectors in `exports.wast` and
`linking.wast`, which the harness classified as unsupported because it read exactly one script
action — `(invoke "name" arg*)`. Nothing about the *engine* was missing: those globals were
allocated, initialized and exported already, and `global.get` reads them from inside a function
body every time the corpus asks. What was missing was a **read path across the boundary**, plus a
place in `Command` to say which action a command carries.

The shape of the fix was a real choice, and the tree had already forecast the losing option.
`Command.Invoke`'s doc comment said, in Scott's own words from PR #364
([0017](0017-the-script-registry-is-a-name-to-instance-map-and-a-funcref-names-an-instance.md)
part 2), that when
another script action became askable "it arrives as its own Kind, which is where the
classification decision is visible" — so the default answer was `KindAssertGet` and
`KindNamedAssertGet`.

## Decision 1 — the verb is a field on `Command`, not two more Kinds

`Command` gains `Verb ActionVerb` (`VerbNone`/`VerbInvoke`/`VerbGet`), and `Kind` gains nothing.
Scott's reason is a citation to this project's own law rather than a preference:

> The reason is this project's own law from grave #445 — a spelling's authority is the grammar, not
> the enum in scope. In wast an *action* is `invoke | get`, and actions appear both standalone and
> inside `assert_return` and `assert_trap`. Command kind and action verb are two axes. Two new
> Kinds encodes their product into one enum and duplicates every action-bearing command; the verb
> field factors the way the grammar already does.

The authority is `interpreter/script/script.ml`:

```ocaml
and action' =
  | Invoke of var option * Ast.name * literal list
  | Get of var option * Ast.name
```

The verb is the variant; the module selector is a `var option` **field on both variants**; the
export name is the same `Ast.name` in both, resolved through `lookup_export` either way. So
directive and verb are independent axes, and `Target` was already the selector-as-field.

**The condition was checked before the ruling was applied, and it did not fire.** The ruling was
conditional: *"If the current Kind enum already conflates action-bearing commands such that a verb
field would be a third axis rather than a factoring of two, that's a different question — come back
rather than forcing my answer onto a shape it doesn't fit."* The enum does carry a second axis for
action-bearing commands — `KindNamedAssertReturn`/`KindNamedAssertTrap`/`KindNamedInvoke` against
their unnamed counterparts — but that axis is **redundant with a field rather than load-bearing**:
`Command.Target` holds the selector, so within the action-bearing set `Kind ∈ Named*` and
`Target != ""` are the same predicate. Measured over **57940** action-bearing commands: **0**
disagreements, at `namedKind = 132` and `targetSet = 132`. A clean zero on an agreement is this
project's own tell for an instrument reporting its blindness, so the probe was **watched die** —
admitting `KindRegister`, which sets `Target` without being a `Named*` Kind, produced **24**
disagreements and named them (`elem.wast:944`, `imports4.wast:25`, …). The zero is a reading, not a
silence.

**The 11 rows therefore cost zero new Kinds against a forecast of two.** They split 10 named / 1
unnamed, and each arrives on the `assert_return` Kind its selector already chose. That the *named*
form is the majority is the mirror image of [#440](https://github.com/scttfrdmn/burroughs/issues/440),
where all 15 `assert_exhaustion` vectors were unnamed and `KindNamedAssertExhaustion` was
deliberately not created for want of a witness.

**Four readers, one dispatcher, and the product lands in the readers because that is where the
grammar puts it.** `getAction`/`namedGetAction` join `invokeAction`/`namedInvokeAction` — a reader
is a grammar production and the grammar has four — and the new `action` function is the single
place the two axes are enumerated together, dispatching on the head atom so that a third verb is a
case rather than a fifth `if !ok`. `assertReturn` now asks `action` and reads the Kind off
`Target`; the arms that admit only `invoke` stay open-coded, because **neither verb is admitted
where the corpus has no witness** — there are 0 top-level `(get …)` commands and 0
`(assert_trap (get …))` vectors, and an admitted shape with no vector behind it is an unexercised
admission.

**Two rulings stand as what they were, dated, rather than one edited to look like it always said
this.** `Command.Invoke`'s comment forecast the opposite shape and cited Scott for it; leaving it
in the present tense would make review confirm the old shape (*the defect stated as the rule*), and
deleting it would make the reversal invisible. It is quoted as PR #364-era and answered by
#460-era. The field is also renamed `Invoke` → `Export`: it held the export name for the one verb
that existed, so a `get` reaching a field named for the other verb is the same defect one layer
down, and both grammar variants carry an export name.

## Decision 2 — `VerbNone` is the zero value, and it is an error rather than a default

Making `VerbInvoke` zero would have cost nothing at the call sites — every command that existed
before this field carries `invoke` — and would have made *forgetting to set the verb*
indistinguishable from setting it correctly, on the axis whose entire purpose is to say which
action a command is. So the zero value is `VerbNone`, `runOpts.action` panics on it naming the line
and the export it would otherwise have called, and a reader that admits an action states its verb.
That is *an analytic zero is not a measurement* pointed at a struct field: a default that could not
have come out otherwise answers nothing.

The engine component follows the same rule one level out. `Engine.ReadGlobal` is **checked at the
verb, not beside `Invoke`** — a caller that can call a function and cannot read a global is a real
intermediate state, and every caller in this repo's tests was that state until now, so requiring it
at declaration would panic on scripts containing no `get`. The nil check sits beside the
`wantsTrap`/`wantsException` tripwires, keyed on `c.Verb` where those are keyed on `c.Kind`, so it
can name the vector and the missing component instead of taking the board down with a nil deref in
`wast.go`.

## Decision 3 — the two verb-conditioned bucket keys are literals, not derivations

Two `assert_return` failure keys grow verb-aware twins (`assert_return (get) result arity`,
`assert_return (get) value mismatch`). They are written as literals rather than derived from
`Kind.String()`, because `KindAssertReturn.String()` is `assert_return (invoke)` and deriving both
would silently re-key every existing row from `assert_return result arity` to
`assert_return (invoke) result arity` — a board change with no finding behind it. A bucket key is a
work-plan line, so a `get` failing under the invoke's key would name a call the vector does not
make; `assert_exhaustion`'s own argument for its own prefix, one axis over.

`Kind.String()` itself is left alone and is now **documented as not a complete label for an
action-bearing command**: no display path renders it for an action row (that arm's keys are the
engine's own error text, these two literals, and `no instance:`), and a third consumer wanting a
label must build it from (Kind, Verb).

## Decision 4 — the read path is `global.value`, sharing `get`'s dispatch through an enum

The engine end is `interp.Instance.Global` and `burroughs.Instance.Global`, reaching a new
`global.value()` — `global.get`'s read-only twin, which hands back a boundary `Value` instead of
pushing onto the interpreter's two-array stack. A `globalShape` enum (`shapeNum`/`shapeV128`/
`shapeRef`) is introduced the moment the second consumer arrives, rather than as a second `switch`:
two places that turn a declared type into a storage layout is graves #78/#105/#106's shape, and the
`v128` arm specifically is grave #239, whose lesson was that the read-back half can be missing
while the write half is right. `exportedIndex` is parameterized on the extern kind for the same
reason, and its `e.Kind` test is load-bearing — a module may export a function and a global under
the same name, so dropping it would make the caller's kind depend on declaration order.

**No re-export delegation, where `invokeIndex` needs one, and the asymmetry is a property of what
the two things are.** A function export resolves to an index whose body lives in the supplying
instance, so the call must travel there; a global export resolves to storage, and `link`'s
`ExternGlobal` arm assigns the supplier's own `*global` into `in.globals[slot]`, so the pointer
already *is* the supplier's object. Reading through a re-export chain and reading at the definition
are the same read of the same slot.

## Consequences

**The board.** Default lane **60946 → 60957 pass (+11)**, **11 → 0 unsupported**, 0 fail, 4187
gated, 0 unimplemented over 256 files. All-on lane **65067 → 65078 pass (+11)**, and
**`allOnFailCeiling` does not move, which is the load-bearing half**: this is the first of the four
`unsupported` drains that adds engine surface, so a wrong answer would have landed in that lane's
`fail` column. `passFloor`, `allOnPassFloor` and `unsupportedCeiling` re-base with the lane; the
last re-bases to **0**, and **v0's `unsupported` column is drained**.

**The +11 looks like capability and is not.** Every one of those globals was already allocated,
initialized and exported; what the engine gained is a *read path*. The claim is made where it is
checkable rather than in prose: `TestGlobalReadsWhatTheInterpreterHolds` reads a 7-row table
(i32/i64/f32/f64/v128/externref/funcref, including two NaN payloads and a non-null `ref.func`)
through both layout dispatches and compares whole structs with `!=` — not `Value.Equal`, which
compares `Bits` only for numerics and is **blind to `Hi`**, so a v128 losing its high lane would
pass a comparison built on it.

**A control isn't born until it's watched die, and one of these two was stillborn on its first
draft.** `TestGlobalExportKindIsNotDeclarationOrder` exports the name `"dual"` as both a function
and a global to pin `exportedIndex`'s kind test; in the first draft both sat at index 0, so a
wrong-kind lookup returned the right answer by coincidence and deleting `e.Kind == kind` left the
test green on that leg. The index spaces are skewed (function 1, global 0, with decoys) and both
halves now fire.

**A first pass on a new public method is the weakest evidence about it.** The 11 corpus rows span 2
files, carry no v128, exercise no reference, and draw no mutability distinction, which is why the
control above exists rather than the board being cited for the method's correctness.

**The `unsupported` delta is −11**, and it has a subject: what the harness can *ask* changed.

**Not decided here.** `KindNamedAssertReturn`/`KindNamedAssertTrap`/`KindNamedInvoke` carry no
information beyond `Target != ""`, and the reference agrees the selector is not a command axis — so
the grammar-faithful shape collapses those three Kinds into their unnamed counterparts and reads
the selector from `Target`, across **26** sites in 4 files. That reverses
[0017](0017-the-script-registry-is-a-name-to-instance-map-and-a-funcref-names-an-instance.md) part
2's encoding and wants its own ADR and a stamp rather than riding a self-merge, so it is
[#462](https://github.com/scttfrdmn/burroughs/issues/462) and explicitly **not** folded in.

The site count is **26 as this lands and was 22 when the ruling was given** — the four added are
this PR's own comments naming those Kinds, which is the direction that figure moves in when a
decision is deferred with its reasons written down. Quoted as of this revision rather than carried
from the relay, because a number measured before a diff describes the tree before the diff.
