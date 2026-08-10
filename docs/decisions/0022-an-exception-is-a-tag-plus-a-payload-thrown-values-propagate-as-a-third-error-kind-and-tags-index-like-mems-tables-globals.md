# 0022 — An exception is a tag plus a payload, thrown values propagate as a third error kind, and tags index like mems/tables/globals

Date: 2026-08-10 · Status: **proposed** — awaiting Scott's stamp per the status-is-a-citation
ruling (PR #142): this section is held open until an approval exists to point at.

Filed against **#201** (the EH rung-2 recon) and #199's own two-rung structure, whose rung 1
(catch-clause and tag-field *retention*, #200) is landed and whose rung 2a (the harness gaining
`assert_exception`, #201's own prerequisite) is landed. This is rung 2b: the design the recon
found genuinely open, priced at "an ADR before implementation" rather than "mechanically forced".

## Question

`throw`, `throw_ref`, and `try_table` need three facts this engine has never had to represent:
what a thrown/caught exception *is* as a runtime value, how a throw's control transfer reaches an
enclosing handler through this engine's Go-native call recursion (no explicit frame stack to
unwind), and how a tag — the thing a `throw` names and a `catch` matches against — is identified
and indexed across instances. #201's recon read the reference's own model for all three and found
two mechanically settled by that reading plus 0020's precedent, and one — tag/`Instance` indexing
— settled by the reference's model directly. What remains open is *composition*: three settled
facts assembled into one coherent design, which is why this is one ADR and not three.

## What the reference's shape already settles

**An exception is `Exn of Tag.t * value list`** (`runtime/exn.ml:5`) — a tag plus its payload
values, no allocation wrapper beyond the variant. **`ref.eq` is undefined for it**:
`Value.eq_ref' = ref (==)` is the base case every other reference kind either uses as-is
(struct/array, 0020) or overrides (`i31`, `extern`, `instance`); `exn.ml` registers `failwith
"eq_ref"` (`:26`) instead — the reference *declining* to define identity comparison for exnrefs,
not merely omitting it. Confirmed against the corpus: zero `ref.eq`/`ref_eq` vectors anywhere
under `exceptions/` or `legacy/exceptions/` (9 files, all searched). A design question with no
consumer and no authority behind it does not exist — 0002's absence-of-a-consumer move.

**A tag is nothing but its declared type, and tag identity is allocation identity**:
`Tag.t = {ty : tagtype}`, `Tag.alloc ty = {ty}` (`runtime/tag.ml`) — a fresh record per
declaration, compared by `==` at the one place tags are ever compared, `eval.ml:1087`'s
`if a == tag c.frame.inst x1`. This is `ref.Inst`'s own funcref shape (0017 Q2, grave #163)
wearing a different type: an imported tag must resolve to the *same allocated tag object* the
exporting instance created, never a structural re-derivation from its type. The corpus has
#163-shaped fixtures ready to catch the wrong reading — `try_table.wast:8-19`'s two import
declarations of the same tag, `tag.wast:38-64`'s cross-module type mismatch — found and named
*before* any tag code exists, which is the graveyard's first fully preventive use in this
campaign.

**Instantiation order**: `eval.ml:1305-1319`'s fold is
`init_type → init_import → init_tag → init_func → init_global → init_table → init_memory →
init_data → init_elem → init_export`, and only *after* every index space is populated does
`init` evaluate `es_elem @ es_data @ es_start` (`:1322-1325`) — active-segment copies and the
start function run once, at the end, over the fully-built instance. **Tag allocation is not
interleaved with global/table/memory initializer evaluation** the way this engine's own
`build()` interleaves those three (`in.newGlobal`/`newTable`/`newMemory` are called per-index
inside one evaluation pass, because a later global's initializer may read an earlier one). A tag
has no initializer to evaluate — `init_tag`'s whole body is `Tag.alloc tt'`, a pure allocation
from the tag's *declared type*, which needs nothing but the type section already being populated.
So tag allocation is order-independent with respect to the value-initializer chain and only needs
to happen after imports are resolved (so an imported tag's slot exists) and before nothing in
particular — it can run wherever in `build()`'s existing sequence is convenient, and this ADR
places it first, immediately after `link()`, matching the reference's own position (tags
allocate before funcs, globals, tables, or memories touch anything).

## Decision

### 1. An exnref is a Go pointer to a package-local struct, held in `ref`'s existing shape

```go
// excObj is one thrown exception's payload: the tag that identifies it, and the values it
// carries — runtime/exn.ml's Exn(Tag.t, value list), one allocation per throw, matching this
// engine's own one-Go-value-per-runtime-object precedent (gcObj, 0020; newTable/newMemory/
// newGlobal before it).
type excObj struct {
	tag *tagInst // the tag this exception was thrown with — see tagInst below
	num []uint64 // the payload's numeric values, in declaration order
	refs []ref   // the payload's reference-typed values, in declaration order — 0002's
	             // two-array split at payload granularity, the same split gcField makes at
	             // field granularity (0020)
}
```

`ref` (value.go) gains a fifth field:

```go
type ref struct {
	Null bool
	Addr uint32
	Inst *Instance
	Obj  *gcObj  // 0020: a struct or array instance
	Exc  *excObj // an exception: excObj, non-nil exactly when Null is false and the reference's
	             // runtime type is exnref. Addr/Inst/Obj are meaningless for this case, the same
	             // way Obj's own comment states about Addr/Inst.
}
```

A fifth field rather than a union or an interface, for `Obj`'s own stated reason at 0020's
acceptance: `ref` grows one field per new payload kind, not a second representation. `ref.eq` on
two exnrefs needs no arm here — see Consequences.

### 2. A thrown exception is a third control-transfer value, propagated as a Go error type checked at every `runFrame`/`call`/`callIndirect` return, matched against `ctrl` at each level

Confirmed by reading, not assumed: this engine's wasm frames are Go frames (`call.go`'s own doc
comment — `run` calls `call` calls `run`), and every existing failure mode (`error`,
`*interp.Trap`) already propagates by ordinary Go `return err` up that same call chain. A thrown
exception joins that taxonomy as a third sibling:

```go
// thrown is an exception in flight, propagated as a Go error the way *Trap already is — the
// third control-transfer outcome (module-invalid layering debt, trap, exception), joining a
// taxonomy this package already has two members of rather than inventing a fourth channel.
type thrown struct {
	exc *excObj
}

func (t *thrown) Error() string { ... } // rendered for the case it escapes Invoke entirely —
                                          // see assert_exception's own harness half, #201
```

`throw`'s arm builds an `*excObj` from the tag and the popped payload arity (mirroring `call`'s
own `countByArray`-derived pop) and returns `&thrown{exc}` up through `runFrame`'s ordinary error
path — no new mechanism needed for the "leaves the current function" half, because that half is
just `return err` doing what it already does for a `Trap`.

**Catching is where the genuine new mechanism lives.** A `try_table`'s label (pushed onto `ctrl`
exactly as a block's is, `control.go`'s existing shape) carries its catch-clause vector. When
`runFrame`'s dispatch loop receives a `*thrown` from *any* instruction — a literal `throw`, or a
`call`/`call_indirect` whose callee's own `runFrame` returned one — it does not immediately
re-propagate: it scans `ctrl` from the top for the nearest label that is a `try_table` handler,
checks each of that handler's clauses against the thrown tag in order (`catch`/`catch_ref` match
by tag identity per §Decision 1's `tagInst` pointer comparison, `catch_all`/`catch_all_ref` match
unconditionally), and on a match performs the branch `eval.ml:1086-1102` specifies — pushing the
exnref first for the `_ref` clause kinds, then branching to the clause's label exactly as `branch`
already does for an ordinary `br`. On no match at any enclosing label, the `*thrown` propagates
past this `runFrame` invocation exactly like an uncaught `Trap` would — `call`'s `return err`,
unchanged.

This needs one new capability `control.go` does not have today: **inspecting `ctrl` on an error
path**, not only on a branch's normal-path label lookup. The scan is bounded by `ctrl`'s depth —
the same bound `matchEnd`/`elseOf`'s own per-entry scans already accept, for the same reason
(0002's build-cost-amortizes-on-workload argument, not a hot-loop cost) — and it runs once per
throw, not per instruction, so it does not touch the dispatch loop's steady-state cost.

### 3. `Instance.tags []*tagInst`, reserved-not-omitted, imports first then definitions — `mems`/`tables`/`globals`'s own shape, no new pattern

```go
// tagInst is one tag: an allocation, matching runtime/tag.ml's Tag.alloc exactly — identity is
// the allocation, never the declared type's structure, per #163's law applied here before any
// tag code shipped a wrong answer (#201's recon).
type tagInst struct {
	typ *binary.FuncType // the tag's param types (results are always empty — "non-empty tag
	                       // result type" is #9's own already-cited rejection)
}
```

`Instance` gains `tags []*tagInst`, sized `ImportedTags() + len(mod.Tags)` exactly like `mems`,
with the identical nil-slot convention (an unfilled tag import leaves its slot nil, exactly as an
unfilled memory import does) and the identical reason (`mems`'s own doc comment: reserved, not
omitted, or a later tag's index shifts). `link.go`'s `Extern` gains a `tag *tagInst` field beside
`mem`/`tab`/`glob`/`fn`; `case binary.ExternTag` in `link()`'s placement switch (currently a
deliberate no-op at `link.go:255-258`, already commented "nothing to fill... no supplier in this
engine can build [a tag Extern]") fills `in.tags[slot] = ext.tag` instead; `Export()`'s
`case binary.ExternTag: return Extern{}, false` (`link.go:80`, currently hardcoded absent)
resolves `Extern{Kind: e.Kind, tag: in.tags[e.Index]}` the way every sibling kind's export arm
already does.

**Tag allocation happens once per module-declared tag, immediately after `link()` returns and
before `build()`'s existing global/table/memory loop**, matching `init_tag`'s position ahead of
`init_func`/`init_global`/`init_table`/`init_memory` in the reference's own fold (`eval.ml:1310-
1319`) and needing nothing from that loop (§What the reference's shape already settles, above,
states why: a tag's allocation is pure, from its declared type alone, no initializer to
sequence against). This is a **new, small loop in `build()`**, not an insertion into the existing
global/table/memory loop — mirroring that loop's own reserved-slot-then-fill shape rather than
threading a fourth concern through code already carrying three interleaved ones.

## Options considered

**§1 (exnref representation):**
- **A — reuse `gcObj` directly, tagging it with a "this is an exception" bit.** Rejected: an
  exception carries no `CompType` (0020's `gcObj.typ` is load-bearing for field access, which
  exceptions have none of) and needs no identity comparison at all, so cramming it into `gcObj`'s
  shape would carry a field every exnref leaves unused and vice versa — the same "one field per
  payload kind, not a shared shape doing double duty" argument 0020 made for keeping `Obj` and
  `Inst` separate rather than unioning funcrefs and GC objects.
- **B — `excObj`, its own struct, `ref` gains one field (chosen).** See Decision.

**§2 (propagation mechanism):**
- **A — an explicit exception-handler stack threaded alongside `ctrl`, checked at throw time by
  scanning outward immediately, never leaving the throwing frame's own `runFrame` invocation.**
  Rejected: this only works if `throw` is always lexically inside the *same* `runFrame` as its
  handler, which is false the moment a `call` sits between them (`try_table.wast`'s
  `trap-in-callee` shape, generalized to throw) — a throw inside a callee must still reach a
  caller's handler, and nothing about "scan `ctrl` immediately" reaches across a Go call
  boundary that has already returned control to the caller.
- **B — a third Go error type, checked at every `runFrame` dispatch-loop return and at `call`/
  `callIndirect`'s own boundaries, matched against the *local* `ctrl` at each level on the way up
  (chosen).** This is what actually crosses the Go-call-is-a-wasm-frame boundary #201's recon
  measured: each `runFrame` invocation owns exactly the labels lexically inside its own function
  body, so a thrown value crossing into a caller's `runFrame` is correctly re-scanned against
  *that* caller's own `ctrl`, which is what "an enclosing `try_table` in the caller catches a
  throw from deep in a callee" requires.

**§3 (tag identity/indexing):**
- **A — index-relative comparison: two tags are "the same" if their declared types match
  structurally.** Rejected outright, not merely dispreferred: this is the exact shape of grave
  #163 reborn — `eval.ml:1087` compares by object identity, not by type, and `tag.wast:38-64`'s
  cross-module scenario is a corpus fixture that would score wrong under structural comparison
  (two same-shaped tags from different modules are different tags). Named and rejected before
  any code exists, which is the recon's own stated goal.
- **B — `Instance.tags []*tagInst`, reserved-not-omitted, allocation-identity comparison
  (chosen).** See Decision; it is `mems`/`tables`/`globals`'s own established pattern, not a new
  one.

## What this does not decide

- **The exact packing of `catch_ref`/`catch_all_ref`'s pushed exnref relative to the payload
  values on the branch target's stack** — an implementation detail of the branch mechanism in
  §2, decided at the PR that lands `throw`/`throw_ref`/`try_table`'s arms, against `eval.ml:1092-
  1102`'s exact ordering as authority (already read and cited in §Decision 2's prose; the ADR
  states the mechanism, the PR states the exact stack-surgery lines).
- **`delegate`'s semantics** (the legacy exceptions proposal's own construct) — out of scope per
  #201's own scoping note: the legacy proposal is lexed but never parsed, tracked by a different,
  currently-unfiled gap, not this gate.
- **Whether `ref.eq`'s dispatch on an exnref pair needs a runtime panic, a false, or is simply
  unreachable** — no corpus vector calls it (§What the reference's shape already settles), so
  this is genuinely undecided and deferred to whichever engine arm implements `ref.eq`'s existing
  switch, which already enumerates kinds exhaustively per `Export`'s own precedent.

## Consequences

- **`ref` grows to five fields plus `Null`**, the third widening of the same struct across two
  milestones (0018 named the direction, 0020 spent it once, this spends it again). Every existing
  construction site leaves `Exc` nil implicitly; only the new `throw`/`catch_ref`/`catch_all_ref`
  arms set it.
- **`runFrame`'s dispatch loop gains one new check shape**: every point that currently does
  `if err := ...; err != nil { return err }` for a sub-call's error needs a variant that first
  asks "is this a `*thrown`, and if so does any label in my own `ctrl` catch it" before falling
  through to the plain re-propagation `call`/`callIndirect` already do. This is additive to the
  existing error-checking shape, not a rewrite of it.
- **`Instance.tags` is a fourth reserved-slot index space**, following `mems`/`tables`/`globals`'s
  exact convention — `link.go`'s two currently-inert `ExternTag` arms (`link()`'s placement
  switch, `Export()`'s lookup) both become live, closing two already-flagged, already-commented
  gaps rather than opening new ones.
- **`ref.eq` remains unimplemented for every reference kind in this engine** (no `0xfb` GC
  dispatch exists yet, per 0020's own opening question) — this ADR adds nothing to that debt for
  exnrefs specifically, since the reference itself declines to define the comparison.
