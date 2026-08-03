# 0014 — mnemonic→opcode is a join of the two generated tables, not a third one

Date: 2026-08-02 · Status: **accepted** (Scott, 2026-08-03, PR #108) — *with the Correction
below*, which is the condition of acceptance rather than a footnote to it: the corrected premise
was re-verified by a detector **outside both readers**, so option C now stands on evidence that
could have disagreed with it.

## Decision

The wat encoder's mnemonic→opcode map is **generated, by joining `keywords.go` (0009) and
`optable.go` (0007) through the reference's own constructor names**, from the same pinned
revision both already cite. It is not hand-written, and it is not a third independent
extraction of the same fact.

The join key is the reference's constructor name — `i32_add`, `local_get` — which
`opcodegen.Arm.Mnemonic` already carries and which its own comment currently calls "a
label, not a fact". **This decision makes it a fact**, and that promotion is the whole
reason this is an ADR rather than a commit.

## Question

#8's accept grammar needs to answer "which opcode byte does `i32.add` encode to?" 487
times. Three ways to get there:

- **A — hand-write the table.** 487 rows. This repo's measured hand-transcription error
  rate is seven wrong citations in twelve items (#37), and the failure is
  accept-direction: a wrong row emits *a different instruction than the text denotes*,
  which decodes clean and scores green. Rejected on 0007's own argument.
- **B — a third extractor**, reading `lexer.mll` and `parser.mly` for opcodes directly.
  Two places would then know "`i32.add` is 0x6a" — `optable.go` and the new table — with
  no derivation between them. That is the enrolled-witness-or-derived-artifact rule
  violated at the table level, and 0006's drift risk with three tables instead of two.
- **C — join the two committed tables.** Chosen.

## What was measured before choosing

Not reasoned from the tables' shapes — counted, with the numbers printed:

- **`plaininstr` has 83 arms.** 51 name their constructor in the *grammar body*
  (`| NOP { fun c -> nop }`, `| LOCAL_GET idx { fun c -> local_get (...) }`); 30 do not,
  because the constructor arrives as the token's *payload* from the lexer
  (`| BINARY { fun c -> $1 }`, where `$1` is `i32_add` from `"i32.add" -> BINARY i32_add`).
  The remaining 2 are `MEMORY_INIT`/`TABLE_INIT`, already known to instr.go as the
  two-shape kinds.
- **The two authorities are exactly complementary.** Of the 30 kinds the grammar does not
  name, **30/30** have a mnemonic in their lexer payload. Of the 51 the grammar does name,
  **51/51** have bare lexer arms. **Overlap: 0. Gap: 0.** That partition is the finding —
  had it been "28 of 30 with 2 uncovered", this decision would be different, because the
  residue would need hand-writing and A's error rate would apply to it.
- **487 of 589 keywords join** (436 via lexer payload, 51 via grammar body). The 102 that
  do not are types, `$`-forms, and script keywords — things with no opcode to have.
- **Mnemonics are unique across all four regions except three**, and all three are
  operand-directed pairs the reference distinguishes by what follows the mnemonic, not by
  the mnemonic: `select` (0x1b bare / 0x1c typed), `ref_test` (0xfb 20/21, null and
  non-null), `ref_cast` (0xfb 22/23, likewise). 542 rows, 497 named, 3 ambiguous.

## Consequences

- **`Arm.Mnemonic` stops being decorative and its comment must say so.** "Kept for the
  generated table's readability … Not load-bearing" becomes false the moment a join keys on
  it: an upstream constructor rename would then move an opcode silently instead of changing
  a comment. Correcting that comment is part of this decision's implementation, not a
  follow-up — *a ruling retroactively falsifies prose written before it*.
- **The three ambiguous mnemonics are resolved by the operand, at the point of use, and
  named there.** They are not silently first-wins: a map keyed on mnemonic alone would
  return one of two opcodes for `select` and be wrong on `(select (result i32))` with no
  board consequence, since both spellings decode clean. The join emits *both* codes for an
  ambiguous mnemonic and the encoder must choose on the operand it read.
- **Vacuity floors are required, per 0007 condition 1 and the empty-set law.** A join whose
  inputs stop matching produces an empty map and agrees perfectly with an empty committed
  table. Floors: joined-keyword count, and the two partition counts (grammar-named,
  payload-named) separately — because one authority's extraction could break while the
  other still matches, and a single total floor would absorb it. **The partition itself is
  asserted: overlap 0, gap 0**, which is the property that made C available at all, so it
  is a control rather than a paragraph.
- **`make keywords`/`make opcodes` can now move this table**, the same coupling 0012's
  census has with `optable.go`, and it is drift-checked the same way.
- **Directional claim.** This serves contract §0's *correctness-neutral* half: it is the
  accept-direction representation for 487 facts the suite scores green by construction
  (§9 G-3). It buys the thesis nothing directly and is not meant to — it is the cheapest
  correct way to get #8's encoder its opcodes, and "cheapest correct" is the right
  standard for a table that must simply not be wrong.

## Correction — the premise was measured over the wrong space (2026-08-02, same session)

**The numbers above are wrong, and the way they were wrong is the finding.** Corrected here
rather than edited above, because the record of what was believed, and why it survived, is
the part worth keeping (the 0003 precedent).

`plaininstr` is not the only production that builds instructions. The reference has **five**,
because the forms taking a folded operand list cannot be plain arms:

| production | kinds named |
|---|---|
| `plaininstr` | 53 |
| `expr1` (the folded sugar spellings) | 9 |
| `blockinstr` | 5 |
| `callinstr_instr_list` | 4 |
| `selectinstr_instr_list` | 1 |

So the corrected figures are **58 grammar-named kinds** (not 51), **494 joined rows** (not
487), **95 unjoined** (not 102). The seven kinds the original measurement missed are
`SELECT`, `BLOCK`, `LOOP`, `IF`, `CALL_INDIRECT`, `RETURN_CALL_INDIRECT`, `TRY_TABLE` — every
control-flow construct in the language, each with an opcode and none joining to anything.

**Why it survived being measured.** The premise was measured with a probe scoped to
`plaininstr`, which is the same scope the reader had. The gap it was checking for was
therefore invisible to it by construction: a measurement that shares the code's blind spot
does not test the code, it echoes it. This is 0006/#33's rule (*scope controls to the space,
not the sample*) applied to a **premise** rather than to a control — the space is "the
grammar", and `plaininstr` was a sample of it. The tell available at the time and not taken:
the gap came out at exactly 0, and a partition that clean should have prompted the question
of what the counting instrument could not see.

Two consequences the corrected reading forces:

- **`grammarConstructors` reads every production**, so a sixth instruction-building
  production arriving upstream needs no edit. One kind may now be named in several
  productions (`select` at `selectinstr_instr_list:678` and `expr1:815`), and those must
  agree — asserted, because nothing upstream requires two productions to agree, and a
  disagreement would mean a mnemonic's plain and folded spellings encoding differently.
- **The gap control needs a detector the join does not supply.** Asking whether every kind
  the join resolved was resolved is a tautology; it is *the* tautology that hid these seven.
  The independent signal is the mnemonic's own spelling — rejected below as a join key, and
  unfit-to-key-on is exactly what makes it a sound second opinion, since it shares neither
  authority's arm shapes. It carries its own vacuity floor.

A third defect, found the same way and unrelated to scope: `constructorIn` was being fed the
whole arm including the **symbol list**, so `| LOOP labeling_opt block END labeling_end_opt`
resolved `LOOP` to the *nonterminal* `block` — which is also a constructor the opcode table
holds. `loop` would have encoded as **0x02**. A wrong opcode for a well-formed module decodes
clean, so no vector could ever have reported it. The reader now scans from the action's
opening brace.

All three were found by printing what the reader returned for named inputs, not by reading
it. None was findable from the board.

## Alternative considered and rejected inside C

**Keying the join on the wat mnemonic itself** (`i32.add` → `i32_add`, dots to
underscores). It resolves 52 of the 56 bare-payload arms and looked sufficient. It is a
*coincidence of naming*, not a derivation — nothing upstream promises the lexer literal
and the decoder constructor agree, and where they disagree the join would silently miss or,
worse, hit the wrong row. The grammar body is an authority; a string transformation is a
guess that happens to be right 52 times. Rejected on the same grounds as A.
