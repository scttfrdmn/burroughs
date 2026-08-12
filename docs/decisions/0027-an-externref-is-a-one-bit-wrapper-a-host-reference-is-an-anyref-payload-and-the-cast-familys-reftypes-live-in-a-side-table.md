# 0027 — An externref is a one-bit wrapper, a host reference is an anyref payload, and the cast family's reftypes live in a side table

Date: 2026-08-12 · Status: **accepted in part** — decisions 1, 2, 4, 5 and 6 **accepted**;
decision 3 remains **proposed**

> **Stamp: Scott, on the #259 relay (2026-08-12), with the carve stated in the stamp itself** —
> decisions 1, 2, 4, 5 and 6 accepted; **decision 3 held at `proposed` inside this document**, marked
> beside its own section, until slice 3's scoping firms it. The carve was granted on the ground that
> decision 3 is the only one of the six with prospective design content, and that the slice-2-first
> ruling (same relay, from #258's measured forecast) had already taken it off the critical path.
> Slice 2 needs decisions 1 and 2 and can proceed on this stamp alone.
>
> **The interval this document spent at `proposed` is kept, not tidied away**, because the rule it
> was following is that *a status field is a citation to an approval, and approvals are artifacts
> with provenance* (#142) — so the record has to hold both the stamp and the wait. What stood here
> before the stamp, verbatim:
>
> > Held at `proposed` deliberately, per the ruling on #142: *a status field is a citation to an
> > approval, and approvals are artifacts with provenance*. Marking this accepted before Scott has
> > ruled would be a fabricated citation about the project's own governance. 0016 sat `proposed`
> > through the PR that implemented it and that is the precedent being followed, not an exception
> > being taken.
>
> Amended on the head that merges, per the #252 precedent: the amendment rides the PR the stamp was
> given on and the green is **re-earned on the amended head**, so the verdict judges the document
> the stamp actually names rather than its predecessor.

Filed against **#258** (rung 5 of the GC ladder) and milestone **v0.2.0 GC gate**, downstream of
**0018** (the wide `ValType`), **0019** (runtime type tests are events), and **0020** (a struct or
array instance is a Go pointer). 0019 named the gap this ADR fills, in the words *"a fourth
decision must supply the **value**"* — 0020 supplied the aggregate half of that value; this ADR
supplies the two payload kinds 0020 did not meet, plus the immediate retention its arms need.

**Direction (contract §0/§1):** every option below is argued toward *language-directed for Go*.
The deciding weight throughout is that a Go guest allocates aggregates constantly and externrefs
almost never — so per-slot cost in the hot representation is priced against a Go workload, and
the extern boundary is allowed to be the narrow, cold thing it is. §1's disclaimed non-goal
(peak throughput parity, browser embedding) is what licenses the second half of that.

## Question

Rung 5 is `fb 14`–`fb 1b`: `ref.test`, `ref.cast`, `br_on_cast`, `br_on_cast_fail`,
`any.convert_extern`, `extern.convert_any`. 0019 decided *what the relation is and when it runs*.
Three things it does not decide have to be settled before any of the eight arms can be written,
and all three were found by reading the authority rather than by reasoning from the opcode list:

1. **Where does a cast's heaptype immediate live?** `immHeapType` currently **stages no word** —
   it calls `decodeHeapType` for the grammar and discards the result. `ref.test`/`ref.cast` cannot
   be implemented at all without their own immediate, so this is forced; what is *not* forced is
   `br_on_cast`/`br_on_cast_fail`, whose optable rows are
   `[immByte, immIdx, immHeapType, immHeapType]` — **four** immediates into `Instr`'s **two**
   words.
2. **How is an externref represented, given it is a wrapper?** `runtime/extern.ml:6` is
   `type ref_ += ExternRef of extern` where `extern = ref_` — an externref *contains* a
   reference — and `:19` gives `type_of_ref' (ExternRef _) = ExternHT` regardless of what it
   contains. This engine's `toRef` currently builds `ref{Addr: v.RefID}` for an externref, which
   conflates the wrapper with its contents and has no way to express
   `extern.convert_any (ref.i31 8)`.
3. **What is a host reference?** `script/script.ml:3` is `type Value.ref_ += HostRef of int32`
   with `:80` `type_of_ref' (HostRef _) = AnyHT`. A host reference is an **anyref**, and
   `ref.extern N` in a script is `ExternRef (HostRef N)` — the wrapper around one
   (`script/js.ml:401` pairs them explicitly). So the thing this engine calls an externref is two
   things, and only the outer one is an externref.

### A fourth question, measured and dissolved — recorded because a negative gets re-looked-for

**Does a null reference have to retain the heaptype it was spelled with?** Two sites declared the
gap **conditionally** and rung 5 looked exactly like the condition arriving, `ref.test` being the
first thing that must report a type: `opRefNull`'s arm calls it *"a real gap the moment something
must report which heaptype a null was spelled with"*, and `table.go`'s retention note records that
`ref.null func` and `ref.null extern` *"still decode to identical `Instr`s"*.

A third site had already answered the narrower version and answering it correctly is why this took
one read instead of a rung: `refEq`'s own comment (`refop.go:127-133`) states that *"the heaptype a
null was spelled with is not part of its identity"*, cited to `ref_eq.wast`'s `eq 0 1` expecting
**1** across a `ref.null eq`/`ref.null i31` pair. That is the *identity* half; what follows extends
it to the *type* half, which is the half the two conditional notes were about.

**The answer is no, and the authority is one line.** `runtime/value.ml:20` is
`type ref_ += NullRef` — **no argument**. `:112` is `type_of_ref NullRef = (Null, BotHT)`: a
single universal bottom, not a per-hierarchy one. Since `BotHT` is below every heaptype,
`ref.test rt` on any null is exactly *"is `rt` nullable"*, and `ref{Null: true}` — the shape this
engine has had since before it could execute anything — is already the reference's own model.

The corpus agrees on the nose, which is what makes this a measurement and not a reading.
`ref_test.wast` stores three *different* nulls in slots 0, 1 and 2 (`ref.null any`,
`ref.null struct`, `ref.null none`, lines 17-19) and then asserts `ref_test_i31` = **1** for all
three (`:130-132`) — a per-hierarchy bottom would answer 1, 1, 1 as well, but a *retained*
heaptype would answer 0 for slot 0, since `any ⊄ i31`. The vector set discriminates the three
models and picks `BotHT`.

So the conditional gap resolves the other way, and that is worth more written down than deleted:
the retention it names is real for the **text encoder** (which must re-spell the heaptype it read)
and imaginary for the **interpreter**, whose most demanding reader — `ref.test` — needs `BotHT` and
nothing else. *A deferral naming the wrong consumer reads as tracked.* Both comments get corrected
to say which half is live, and neither is deleted: a condition that was checked and did not fire is
a stronger record than its absence.

The reading order is also the lesson, and it is the standing one rather than a new one: the sibling
comment three functions away had the fact, and reading it first is what made this cheap. *Lessons
are indexed by shape, not by file* — "what does a null's spelled heaptype affect" is the shape, and
`refEq` had already paid for half of it.

## Options

### Q1 — where the cast family's type immediates live

- **A — pack into the existing two words.** Arithmetically comfortable: `br_on_cast`'s flags byte
  carries only the two heaptypes' nullability bits (`decode.ml:643-644`), so each heaptype plus its
  bit is a complete `ValType` — 0018's own three fields — and word 0 takes the label (32 bits) plus
  both kind bytes and both null bits (18) while word 1 takes the two resolved indices (32 + 32).
  114 bits of 128, nothing spanning a boundary.

  **Rejected, and by this project's own capacity control rather than by taste.** Two mechanisms
  refuse it together:
  1. `optable.go` is **generated** from `decode.ml` and marked `DO NOT EDIT`, with `make
     opcode-drift` asserting agreement. It faithfully reports `br_on_cast` as four immediates in
     reading order, because that is what the reference reads. Collapsing them into one synthetic
     immediate means teaching the extractor to editorialize its authority — the opposite of what
     decision 0007 built it for.
  2. `immStagedBits` (`instr_width_test.go:109`) costs immediates **per kind, globally**, and
     `TestInstrImmediateWidthCoversTheTable` sums per row. `br_on_cast` sits at exactly 128 bits
     today (`immByte` 64 + `immIdx` 64 + two heaptypes at 0); giving `immHeapType` any nonzero cost
     puts the row over capacity and the control fires — **correctly**, since `stage` keeps the first
     two words and silently drops the rest. Making the row fit needs `immIdx` and `immByte` costed
     at packed widths *for these rows only*, and a per-row exception in a capacity control is
     precisely the mechanism that let grave #100 drop fourteen lane indices. A control I would have
     to weaken to admit my design is a control disagreeing with the design.
- **B — 0016's side table, extended to the cast family, uniformly (chosen).** `Func` already
  carries two instruction-indexed side tables — `Labels` (0016, `br_table`'s vector) and `Catches`
  (`try_table`'s clauses) — each added when a consumer arrived, each with nil as the normal case and
  an accessor keeping "absent" distinct from "empty". A third holds the cast family's reftype
  immediates. Uniform across `fb 14`–`fb 19` rather than split by whether a given row's immediates
  happen to fit, because `immStagedBits` is per-kind: `immHeapType` cannot cost 40 bits for
  `ref.test` and 0 for `br_on_cast` at the same time, so one mechanism for the family is the only
  self-consistent choice. Consequence accepted: a map lookup per cast, and `immStagedBits` and the
  generated table both stay untouched.
- **C — grow `Instr` to three words.** Rejected for 0016 option A's own reason, restated: a
  third word is a tax on every instruction in every module to serve two opcodes in 256, and
  `Instr`'s compactness is what 0002's rewrite is *for*. §1's workload is megabyte-scale guests.

**Recorded because the revision is the useful part:** option A was this ADR's original decision,
with option B rejected in writing on the argument that 0016 is scoped to *unbounded* immediates and
these are bounded. That argument is still true and is no longer sufficient — the binding constraint
is not "unbounded" but **"more values than `stage` retains"**, which `br_on_cast` meets with four.
The reversal came from reading the width control while implementing, before a line was written; the
original reasoning is left visible above rather than tidied away, since an ADR that shows only the
surviving option teaches nothing about why it survived.

### Q2/Q3 — the externref wrapper and the host reference

- **A — one bit plus one payload kind (chosen).** `ref` grows `Externalized bool` (this
  reference is `ExternRef` *wrapping* the payload the other fields describe) and a host payload
  (`IsHost bool` + reuse of `Addr` for the identity), following 0020's stated one-field-per-
  payload-kind shape. `extern.convert_any` sets the bit; `any.convert_extern` clears it; neither
  touches the payload, which is what makes the round trip in `extern.wast:52-55`
  (`externalize-ii` returning `(ref.i31)`, `(ref.struct)`, `(ref.array)`) an identity by
  construction rather than by three arms agreeing.
- **B — a real wrapper: `Extern *ref`.** Faithful to `ExternRef of extern` transliterated, and
  rejected: it costs a pointer *and an allocation* per externalization, makes `refEq` recursive,
  and buys nothing, because **the type system permits exactly one level of wrapping** —
  `extern.convert_any` is `anyref → externref` and its result is therefore never its own input.
  A one-bit encoding of a one-level wrapper is not a shortcut, it is the wrapper's actual
  information content. (This is 0020's own argument against a second representation, arriving one
  payload kind later, exactly as `ref.Exc`'s comment predicted it would.)
- **C — keep conflating: an externref stays `ref{Addr: id}`.** Rejected, and it is the option
  the code is in today. It cannot express `extern.convert_any (ref.i31 8)` at all, and its
  failure mode is the **accept direction** (§9 G-3): `ref.test` on a conflated value answers a
  plausible number rather than refusing, so the suite would score it green wherever no vector
  discriminates. `extern.wast` does discriminate — **1 pass / 11 fail** in the all-on lane,
  measured — but a corpus that happens to ask is luck, not a control.

## Decision

1. **The cast family's reftype immediates live in a third side table on `Func`, keyed by
   instruction index** (Q1 option B) — `Labels`' and `Catches`' shape, and their conventions with
   them: nil is the normal case, a missing key is not an error, and consumers go through an accessor
   so *absent* stays distinguishable from *present and empty*. `immHeapType` continues to stage no
   word, so `immStagedBits` and the generated table are both unchanged.
   - **Retained as a full reftype pair, not as a bare heaptype**, because nullability is not in the
     encoding: `decode.ml:636-639` is four separate arms — `0x14 → ref_test (NoNull, ht)`,
     `0x15 → ref_test (Null, ht)` — so the wire carries a *heaptype* and the **opcode** carries the
     null bit. `decodeHeapType` builds its `ValType` with `null` hard-false
     (`sections.go:848-886`), correct for a heaptype and incomplete for this immediate. The
     resolution is to record the reftype the arm *means*, null bit included, at decode time rather
     than leaving the interpreter to re-derive it from its own case label: same fact, one home. Get
     this wrong and `ref.test (ref null $t)` and `ref.test (ref $t)` become indistinguishable — an
     accept-direction defect on precisely the pairs `ref_test.wast` is built out of.
   - **Which `Imm` slot a value lands in is printed, never reasoned about.** 0016 paid for this
     rule at exactly this kind of site: `br_table`'s default is in `Imm0`, not `Imm1`, because
     `immVecIdx` stages no word — and reasoning from the generated row's field order gives the
     wrong slot. `br_on_cast` has the same hazard with `immByte` and `immIdx`, so its slot
     assignment is established by decoding a real module and printing the fields.
2. **`br_on_cast`'s flags byte gets its malformedness check**, which the engine does not currently
   make. `decode.ml:642` is `require (flags land 0xfc = 0) s (pos + 2) "malformed br_on_cast
   flags"`, and the row reads the byte through `immByte` with nothing asserting its shape — so a
   module with `flags = 0x04` is accepted today where the spec calls it malformed. That is a
   *reject-direction* gap and therefore one the corpus can see, which makes it the cheap half. The
   spec string is quoted verbatim from the reference rather than invented, per the
   gates-never-manufacture-malformedness rule: the string belongs to the grammar, and the grammar
   here is the tracked union's (§9 G-2), which Wasm 3.0 is in.
3. **`ref` grows `Externalized` and a host payload** (Q2/Q3 option A). — **STATUS: `proposed`, not
   covered by this document's stamp.** The #259 stamp carved this decision out explicitly and it is
   the only one of the six still open; it is also the only one with prospective design content, since
   nothing implements it yet and slice 3 is where it lands. Do not cite it as accepted, and do not
   implement it on this document's authority: its scoping firms with slice 3 (a const-expression arm,
   harness `(ref.host N)` and three bare `RefTypePattern` heaptypes — see *what this does not
   decide*), and the stamp is expected then. The `Consequences` entries below that follow from it —
   `ref`'s width, `refEqTreatment["Addr"]`'s inverted reason, #260 — are therefore **forecasts**
   rather than accepted commitments, and are marked where they appear.

   An externref's dynamic
   heaptype is `extern` whatever it wraps (`extern.ml:19`); a host reference's is `any`
   (`script.ml:80`) — *not* `eq`, `i31` or `struct`, which is what `ref_test.wast:120-127`'s
   `ref_test_eq(6) = 0` against `ref_test_any(6) = 2` asserts.
4. **A null keeps no heaptype**, per the dissolved fourth question: `type_of_ref` maps
   `ref{Null: true}` to `BotHT` and the relation handles it. The two comments predicting otherwise
   are corrected to name the text encoder as the live consumer.
5. **`gcObj` gains its defining module, because a heaptype that cannot resolve its own supertypes
   is not a heaptype.** `gcObj.typ` is a `*binary.CompType` aliasing `mod.Types[idx]`, and
   `match_deftype`'s disjunct 3 walks `Supertypes`, which are **indices into the module that
   declared them** — so an object whose type came from another module cannot have its chain walked
   from the casting instance's type space. The reference has no such problem and the reason is
   instructive: `eval.ml:646` calls `subst_reftype (subst_of c.frame.inst)` first and then
   `Match.match_reftype []` with an **empty context**, every index having already been substituted
   into a self-contained `Def dt`. Go has no cheap equivalent of that substitution, so the module
   travels with the object instead.
   - **On the object, not on the reference**, though `ref.Inst` is a field that already exists and
     is unused for aggregates, so putting it there would cost zero bytes. Rejected anyway: a
     reference is *copied* — through `struct.get`, `array.get`, a table slot, a global, a local, a
     frame — and every one of those paths would have to remember to carry `Inst` alongside `Obj`,
     where forgetting is silent and yields a cast that cannot resolve a supertype. One fact with
     one home cannot drift; one fact that seven paths must propagate will. The object is allocated
     once, so the cost is one pointer per aggregate rather than per reference to it.
6. **`type_of_ref` is one function with one arm per payload kind, and it is the only place a
   dynamic heaptype is derived.** Every cast arm calls it; none re-derives a type from fields.
   This is `refEqTreatment`'s discipline extended to the second exhaustive-over-payload-kinds
   reader — and it gets the same tripwire, because a payload kind added without an arm here is an
   accept-direction defect by construction.

## What this does not decide, named rather than left implicit

- **The relation itself.** That is 0019, accepted: `matchDeftype` widens past `funcCompTypeAt` to
  struct/array comptypes and gains the abstract lattice, keeping one comparator and every caller.
  This ADR supplies the *value* side its `type_of_ref` reads; it does not restate the relation.
- **The harness's new result forms.** `extern.wast:38` expects `(ref.host 1)` and
  `ref_test.wast`/`extern.wast` expect `(ref.i31)`, `(ref.struct)`, `(ref.array)` — three
  `RefTypePattern` heaptypes and one literal `RefClass` the harness does not have
  (`internal/spec/value.go`'s five members). That is harness capability with no representation
  decision inside it, sized as PR work under #258 rather than as an ADR, exactly as 0019 sized
  the encoder-capacity gaps.
- **Whether `Externalized` and `IsHost` should later fold into one payload-kind enum.** `ref` now
  carries two bools and three pointers as discriminators, and at some count the
  one-field-per-kind shape 0020 chose stops paying. That count is not known and inventing a
  threshold here would be the disguised-minimum-size error the ratio ruling already names.
  `TestRefWidthIsMeasuredNotAssumed` pins the struct's width, so the next widening is a red board
  and not a silent 25% — the tripwire is the mechanism, and it already exists.

## Consequences

The **first four** entries below — `ref`'s width, `refEq`'s treatment map, that map's blind spot, and
the boundary constructor's new neighbour — follow from **decision 3**, which this document's stamp
carved out at `proposed`. They are therefore **forecasts of what slice 3 will owe**, not accepted
commitments, and none has fired yet because no field has been added. The remaining three (`ref.null`'s
retention gap, `immStagedBits`/`optable.go` untouched, the cast trap's tail) follow from accepted
decisions 4, 1 and 1/6 and stand as consequences. Counted against the list rather than estimated: an
"immediately below" that is off by one is a false claim inside an amendment about provenance.

- **`ref` grows by one word.** Measured, not assumed: 40 bytes today, and two more bools plus the
  host discriminator land in existing padding or open one word. `TestRefWidthIsMeasuredNotAssumed`
  fails on the change and is updated with the new figure stated, which is that test's whole
  purpose.
- **`refEq` must account for the new fields or the tripwire fires.** `refEqTreatment`
  (`refop.go:207`) names every field of `ref`, and `TestRefEqAccountsForEveryRefField`
  (`refop_test.go:159`) fails the moment the struct grows one the map does not mention. This ADR's
  implementation therefore cannot land without answering what `ref.eq` does with `Externalized` and
  the host payload — the pre-registered control working exactly as filed. The answers are the
  reference's: `extern.ml:13` unwraps and recurses, and `script.ml:93` is
  `HostRef n1, HostRef n2 -> n1 = n2`.
- **And the tripwire's blind spot, found by this ADR's own citation check rather than by the
  control: it pins field *names*, not *treatments*.** `refEqTreatment["Addr"]` currently reads
  *"not compared: reachable only on a funcref, which refEq reports as #9's"*, and the host payload
  makes the **reason** false while leaving the **verdict** true — `Addr` becomes reachable on a host
  reference, so the map's justification stops holding while the map still has an entry and the
  reflection still agrees. `TestRefEqAccountsForEveryRefField` cannot see that, by construction: it
  compares a key set to a field set.
  - The verdict survives on a *different* argument, which is the point of writing it down. A host
    reference's dynamic heaptype is `any` (`script.ml:80`), and `eq` is a **subtype** of `any`, not
    a supertype — so an `anyref` is not an `eqref`, validation rejects `ref.eq` on one, and
    "not compared" stays correct. `ref_test.wast`'s `ref_test_eq(6) = 0` against
    `ref_test_any(6) = 2` is the corpus asserting exactly that placement.
  - The live defect is in the **testimony**, not the verdict: `refEq`'s fall-through returns
    *"ref.eq on a function reference"* for anything that is neither null, i31, aggregate nor exnref
    — so a host reference reaching it would be reported as a funcref. That is grave #36's class (the
    engine wrong about its own input, inside a format string) and it is invisible to every vector,
    because the expected string stops at the sentinel. Rung 5 adds the arm and the reason, and
    `refEqTreatment["Addr"]`'s text is corrected in the same change.
  - Filed as the generalization rather than fixed silently: **a coverage control over a map of
    claims checks that a claim exists, never that it is true.** Whether that earns a tripwire of its
    own (a treatment-versus-behaviour cross-check) is a real question and not one to answer inside
    an ADR about representation — it goes to the tracker, where the co-blocking evidence for it can
    be measured.
- **The `ExternRef` boundary constructor and `Value.RefID` keep their meaning and gain a
  neighbour.** `interp.ExternRef(id)` builds the wrapper-around-host pair, which is what
  `ref.extern N` means; a *host* `Value` (for `ref.host N`) is a second constructor. Both are
  public API surface on a `v0.x` module, so neither needs a compatibility argument.
- **`ref.null`'s retention gap is *not* closed by this ADR, and saying so is the point.** The side
  table is keyed by instruction index and populated for the cast family, whose arms consume it;
  `0xd0 ref_null` still stages no word and still decodes identically for all thirteen heaptypes.
  Its consumer is the text encoder (#8), not the interpreter — established by the dissolved fourth
  question above — so closing it here would be retention ahead of its consumer, which is the thing
  0016 exists to refuse. `opRefNull`'s comment is corrected to name #8 rather than to imply the
  interpreter will need it.
- **`immStagedBits` and `optable.go` are untouched, which is the option's main dividend.** The
  capacity control keeps a single global cost per immediate kind and needs no per-row exception, so
  the mechanism that dropped fourteen lane indices (grave #100) gains no new place to hide.
- **A cast trap's tail is ours to keep honest.** `eval.ml:653-657` renders
  `"cast failure, expected " ^ string_of_reftype rt ^ " but got " ^ string_of_reftype
  (type_of_ref r)`, and all 30 `assert_trap` vectors stop at the sentinel `cast failure` — so the
  tail is oracle-invisible and must be rendered from the reftypes actually compared, never
  reconstructed (grave #36; #38's refinement for why the sentinel half *is* covered).
