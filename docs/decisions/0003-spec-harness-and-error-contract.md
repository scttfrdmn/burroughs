# 0003 — Spec harness strategy and the decoder error contract

Date: 2026-07-30 · Status: **accepted** (Scott, 2026-07-30)
Contract refs: §9 (G-1, G-3, G-4)

## Decision

**Option C — staged pure-Go harness — accepted, substring matching
included.** Phase 1 (s-expr reader, wast string literals,
`(module binary ...)`, `assert_malformed`) now; full wat text format with
the interpreter, timed to `assert_return`. wabt stays out of the oracle
path permanently.

Substring matching is what every conforming implementation does with
`assert_malformed` text, and the 107-forms / zero-wat / zero-quote
verification below means phase one's scope is a **measured fact**, not a
claim inherited from review.

The suite's strings are hereby the decoder's error contract.

---

*Deliberation as written before the decision follows, unedited.*

## Context

The suite is the oracle (CLAUDE.md, Disciplines). Nothing in Burroughs is
"done" until its spec tests pass, so how we consume the upstream suite is a
founding decision, not plumbing. It is also decision-independent of 0002:
the harness can exist before a single interpreter opcode does, and if it
does, 0002's implementation gets an oracle on day one.

`make spec-tests` now vendors the suite: **257 `.wast` files** at
`testdata/spec` (gitignored).

Two claims from chat-Claude's review shaped this doc. Both were checked
against the vendored suite rather than taken on faith; both hold, one with
a correction that changes the work.

### Verified: the malformed tests need no wat compiler

`binary.wast` — the first file the decoder faces — contains **107
`assert_malformed` forms and 0 other assert forms**. Every module in it is
defined as `(module binary "\00asm" "\01\00\00\00" ...)`: raw quoted byte
strings. There are **zero `(module quote ...)` forms and zero wat function
bodies** in the file. 15 files suite-wide use the `(module binary ...)`
form.

So phase one of a pure-Go harness needs exactly four things: an s-expression
reader, wast string-literal decoding (`\hh` escapes plus raw chars), the
`(module binary ...)` form, and `assert_malformed`. That is days of work,
not the months a full wat compiler implies. Confirmed as described.

### Verified with a correction: the error strings are a real contract

The suite's `assert_malformed` failure strings are load-bearing and
specific. Suite-wide tallies of the decoder-relevant ones:

| string | count |
|---|---|
| `malformed UTF-8 encoding` | 714 |
| `integer too large` | 34 |
| `integer representation too long` | 26 |
| `unexpected content after last section` | 22 |
| `magic header not detected` | 16 |
| `unexpected end` | 11 |
| `unexpected end of section or function` | 10 |
| `section size mismatch` | 8 |
| `malformed limits flags` | 7 |
| `unknown binary version` | 6 |
| `malformed section id` | 6 |
| `malformed import kind` | 6 |
| `malformed mutability` | 5 |
| `function and code section have inconsistent lengths` | 5 |

`binary-leb128.wast` uses **only** the two integer strings (33 `integer too
large`, 25 `integer representation too long`). The correction to
chat-Claude's framing: the discriminator is **not** continuation-bit vs
high-bits as two independent checks — it is width-parameterized, and the
order matters. Derived from the actual vectors:

- Last permitted byte has its **continuation bit set** (more bytes follow
  than the width allows) → `integer representation too long`.
  Witness: `\80\80\80\80\80\00` (u32), `\ff\ff\ff\ff\ff\7f` (i32 const).
- Last permitted byte has continuation clear but **bits beyond the width
  set** → `integer too large`.
  Witness: `\80\80\80\80\10` (u32), `\ff\ff\ff\ff\0f` for an *i32* const is
  `integer too large` while the identical bytes are a *valid* u32
  `0xFFFFFFFF`.

That last pair is the important one: the same five bytes are legal as a u32
and malformed as an i32. **The check is a function of the target width and
signedness, not a property of LEB128.** A single `u32()` method cannot
carry it. See Graves.

## Options

### A. Preconvert with `wast2json` (wabt), consume JSON
- **+** Fast to stand up; wabt is the reference tooling.
- **−** Puts a non-Go binary **in the oracle path**. CI reproducibility
  debt in the one place we can least afford it: a suite result would depend
  on a wabt version nobody pinned. Also not installed on the dev box
  (`wat2wasm`/`wasm-tools` absent; only `wasmtime` present), so it is a new
  dependency, not a free one.
- **−** Tensions with "no cgo, pure Go" in spirit if not in letter.

### B. Pure-Go harness, all at once (full wat parser first)
- **+** No external tooling, ever. Permanent asset.
- **−** Front-loads a wat compiler before the decoder can face a single
  test. Months before first green. Violates "suite subset green before the
  next" pacing.

### C. Pure-Go harness, staged ladder — **recommended**
- **Phase 1 (now):** s-expr reader + string literals + `(module binary ...)`
  + `assert_malformed`. Unlocks all 107 `binary.wast` tests and the other
  14 byte-string files. No wat compiler.
- **Phase 2 (with the interpreter):** `assert_return`/`assert_trap` and the
  wat text format, timed to when 0002's interpreter can execute. By then
  it is ~80% of the wat tooling Burroughs wants anyway.
- **+** First green in days; keeps wabt out of the oracle path permanently;
  each phase paid for by tests it unlocks.
- **−** Two harness increments instead of one. Accepted: the second is
  wanted regardless.

## Recommendation

**Option C.** The reproducibility argument is decisive: the oracle is the
project's definition of truth, and a truth that depends on an unpinned
third-party binary is weaker than one that depends only on `go test`. The
staging is what makes it affordable, and the verification above is what
makes the staging credible rather than hopeful.

Consequences if accepted:

1. `internal/spec` gets a pure-Go wast reader, phase 1 scope only.
2. **The suite's strings become the decoder's error contract.** Decoder
   errors are declared to match spec text exactly, and the harness compares
   against them. This is what converts gap 3 from cosmetics into a fix with
   a test (see Graves).
3. Error matching follows the upstream convention: the assertion passes if
   the engine's message *contains* the expected string (upstream runners
   match on prefix/substring, and some strings like `alignment` are
   deliberate prefixes of `alignment must be a power of two`). We record
   substring matching so the two `alignment` variants don't need special
   casing.

## Corollary: gates reach into the decoder

Per Scott's §9 reading (v0 = MVP core green, 3.0-feature gates present and
**off**), section-id validity is **gate-dependent, not constant**. Section
id 13 (tag) is valid with the exception-handling gate on and `malformed
section id` with it off. The suite agrees: `\0e` (14) and `\7f` are
`malformed section id`, as is any id whose LEB is multi-byte
(`\80\01`, `\ff\01` — an id must be a single byte).

This retires the "add a range check" framing of gap 1. The decoder needs a
**gate-aware highest-valid-id**, which means the gate set must be a decoder
input from the start. Recorded here; implemented with the type section.

## Graves (pre-dug, found by this investigation)

Two bugs in `internal/binary`, both in `reader.u32`, both now with spec
vectors to test against.

**Grave 1 — the taxonomy conflation.** `if i == 4 && c&0xF0 != 0` fires for
*both* malformed classes, so every over-long u32 reports `integer too
large` when half of them are `integer representation too long`. Probed
against the real vectors:

| input | spec wants | current code |
|---|---|---|
| `80 80 80 80 80 00` | representation too long | integer too large |
| `ff ff ff ff ff 7f` | representation too long | integer too large |
| `80 80 80 80 10` | integer too large | integer too large ✓ |
| `ff ff ff ff 0f` | valid `0xFFFFFFFF` | valid ✓ |

**Grave 2 — `ErrLEBTooLong` is dead code.** The `i == 4` branch returns
before the loop can exit, so the `return 0, ErrLEBTooLong` after the loop
is unreachable. Exhaustive probe over all 256 fifth-byte values: **0 reach
it.** The error that *should* name the continuation-bit case is the one
that can never fire — which is exactly why grave 1 went unnoticed. The two
bugs held each other up.

Lesson for the fix site: **an error constant with no reachable path is a
missing check wearing a disguise.** Both graves get comment markers naming
this, per the discipline.

## Decision asked of Scott (resolved — see Decision, above)

Accept Option C (staged pure-Go harness) and the error-string contract,
including substring matching? The trade is schedule: Option A would show
green sooner, and I am arguing that green would be worth less.
→ **Accepted as staged, substring matching included.**

## Sweep corollary to the grave lesson

chat-Claude attached a corollary to grave 2's lesson: it is the inverse face
of the predicate-property rule, so **grep for other defined-but-never-returned
errors in the same session.** Swept:

| error | returned? |
|---|---|
| `ErrBadMagic`, `ErrBadVersion`, `ErrTruncated`, `ErrLEBTooLong`, `ErrLEBOverflow`, `ErrSectionOverrun` | yes |
| `ErrTrailingData` | **no** |

`ErrTrailingData` is defined and never returned — the same shape as grave 2.
It is *not* a third grave: it is the documented not-yet-enforced
duplicate/misordered-section condition, named as such in the source at the
time of definition. The distinction that keeps it honest is whether the
disguise was noticed and labelled. It ships with a tracking issue rather
than a silent definition.

## Correction — 2026-07-31, the LEB taxonomy's *order* was wrong (#36)

Appended, not edited. The body above stands as the record of what was believed
and — the part worth preserving — of **why it survived review**. (Directive:
Scott, PR #37; same law as the #5 amendment.)

The taxonomy section is right about *what* the two classes are and wrong about
**when each is tested.** It says "the order matters" and then prescribes the
wrong one: the two bullets are listed continuation-bit first, and the
implementation followed the document faithfully. The reference interpreter's
`uN` checks the unused-bits **range** before consulting the continuation bit —

```ocaml
let rec uN n s =
  require (n > 0) s pos "integer representation too long";
  let b = byte s in
  require (n >= 7 || b land 0x7f < 1 lsl n) s pos "integer too large";
  ... if b land 0x80 = 0 then x else ... uN (n - 7) s
```

— so the width budget is exhausted *before* a byte is read (too long) and a
byte's range is judged *when* it is read (too large), independent of whether it
continues. For an input that is **both** over-wide and continued, the spec says
*integer too large*; this document's ordering says *too long*.

**Why this is the correction worth appending rather than a footnote.** The
prescription above is *the defect stated as the rule*, and that is the strongest
camouflage a bug can wear: review verifies code against its claims, so every
subsequent reader who checked `uleb` against this ADR and against its own
comment found agreement. Both were wrong together. **Comments and ADRs are
testimony, and where prose and the reference's executable disagree, the
executable outranks.** Two correct predicates composed in the wrong order is
still wrong, and *order of tests is itself a claim about the spec* — which means
it is a claim that needs an authority, not a derivation from vectors that happen
not to distinguish the orderings.

That last clause is the mechanical failure. The section says "Derived from the
actual vectors", and it was — but the vectors it was derived from are exactly
those where the two conditions **do not overlap**, so they cannot distinguish
the two orderings. `TestLEBTaxonomy`, built from those same vectors, was green
through the entire life of the defect. The overlap region *is* the bug, and no
row asked about it.

Also corrected: the witness `\ff\ff\ff\ff\ff\7f` is listed under the
continuation-bit bullet, sourced from an **i32 const** — a *signed* field. The
verdict for those bytes depends on the reader: `sN(32)` says *too long* (a legal
sign extension one byte past the budget), `uN(32)` says *too large* (the fifth
byte's payload exceeds the width). Both are right. The taxonomy's own lesson
extends one step further than it states — the verdict is a function of the
field's width **and signedness**, so a fixture must be read by the reader the
field it came from actually uses. In the code that row had been asserted against
`uleb`, and it only surfaced *because* the ordering fix broke it: it had been
green for the wrong reason.

**Measured, not argued.** `TestLEBMatchesReferenceUN` is a differential port of
`uN`/`sN` over the derived disagreement space — k all-continuation prefix bytes
× all 256 final bytes, k from 0 past the width budget:

| | before | after |
|---|---|---|
| `bits=32` | 112 disagreements | 4096 verdicts, 0 |
| `bits=64` | 126 disagreements | 6656 verdicts, 0 |

Identical counts for `uleb` and `sleb`, which is what established one structural
defect with two tenants; each half was falsified independently by reverting it
alone and recovering exactly its own count. The port carries a vacuity control,
because "0 disagreements" is also what an oracle that answers nothing produces.

**What the design decision above got right, and keeps:** "the check is a function
of the target width and signedness, not a property of LEB128" is the load-bearing
claim, and it is correct. It is also what made the fix cheap — #36's width
corrections (limits min/max are u64, the functype form tag is an s7) were
parameter changes, not rewrites, and the same design handed over a free
bidirectional control: identical bytes `80 80 80 80 10` are *integer too large*
as a data-segment memory index and legal as a limits minimum. The ADR's
architecture survived; its prescribed test order did not.

## Status

**Accepted** 2026-07-30. Phase 1 implementation may proceed. The two graves
were fixed in the same session as bugs against the decoder's existing scope,
with regression tests drawn from the vectors above.

**Amended** 2026-07-31 — see the Correction above (#36). The decision stands;
the taxonomy's prescribed test order is superseded by the reference
interpreter's, and the authority for order-of-tests questions is `decode.ml`,
not a derivation from vectors that do not distinguish the orderings.
