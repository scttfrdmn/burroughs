<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0078 — the size's second copy is deleted rather than locked, and the matchers compute the current type on demand like the reference's `type_of`

Date: 2026-09-06 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, it changes no gate's default, adds no exported symbol,
and reverses no stamped decision. Picked by the actor from the open backlog with no order behind it, so
every commit in this slice is `Ratio-Class: carried`.

Filed against **[#663](https://github.com/scttfrdmn/burroughs/issues/663)**.

## The population is two copies, and the issue names one

#663's body is about `memory.grow`'s write to `memory.limits.Min`. `internal/interp/table.go:table.grow`
ends with the identical statement against `table.limits.Min`, read by the identical matcher on another
goroutine's `link`, and the issue says nothing about it. That is *an issue's list read as an inventory*:
the body records where the defect was noticed, and the domain is derived from the field's writers. Both
are repaired here, and the table arm is the one with less coverage behind it — see the oracles below.

## Context

Both `grow`s end with a plain write to a field an unsynchronised reader reads:

```go
m.limits.Min = newSize   // internal/interp/memory.go:memory.grow
t.limits.Min = newSize   // internal/interp/table.go:table.grow
```

The reader is import matching. `internal/interp/link.go:importTypeMismatch` reaches
`internal/interp/link.go:matchMemoryType` and `internal/interp/link.go:matchTableType`, both of which
descend to `internal/interp/link.go:matchLimits` and compare `got.Min`, from another goroutine's
instantiation of a module that imports this memory or table. `grow` holds `growMu`; `link` holds nothing.
So this is a plain data race in the Go memory model's own terms, and unlike
[#586](https://github.com/scttfrdmn/burroughs/issues/586) it is one `-race` can see.

**Measured before deciding.** `go test -race -run TestImportMatchingDoesNotRaceAGrowing ./internal/interp/`
on `main` reports `WARNING: DATA RACE` on both arms — the memory write in
`internal/interp/memory.go:memory.grow` and the table write in `internal/interp/table.go:table.grow`, each
against a read inside `internal/interp/link.go:importTypeMismatch`. The controls are in this slice, so the
mutation needed no invention: `main`'s own code is the failing arm.

**Each arm has two read sites, which decides part of the mechanism.** `importTypeMismatch` reads the
limits once to *decide* the match and again to *render* the refusal through
`internal/interp/typestring.go`'s `externMemory` / `externTable`. A repair that fed only the verdict from
an authoritative source would leave the message racing, and worse, would let the message name a size the
verdict did not refuse on.

### Why the field is written at all

`memory.ml:64`'s `grow` sets `mem.ty <- MemoryT (at, lim')` with `lim'.min` the new size, and `type_of`
reads it back at import-match time (`instance.ml:76`), so a memory re-exported after growing must satisfy
an importer against its *current* size. `imports4.wast:19-37` pins exactly that, in its own words:
"imported memory limits should match, because external memory size is 2 now." The fact is required; only
its storage is in question.

## Options

1. **A mutex or `growMu` around the field.** Two writers agreeing about a duplicate is *a correctly
   synchronised wrong answer* — the reading it produces is still a second opinion about a quantity that
   already has an owner, and it prices every import match with the growth lock, which
   [ADR 0073](0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md)
   took for the boundary accessors only because they genuinely need the image not to move under them. A
   matcher does not.
2. **An atomic on `Min`.** Rejected on a mechanical fact rather than a preference: `Min` lives in
   `binary.Limits`, a **decoder** type, shared with `internal/binary` and `internal/validate` and copied
   by value at every use — `matchLimits` takes it by value, `newMemory` reads it, the validator reads it.
   An `atomic.Uint64` field would make the module's own representation non-copyable and push the
   interpreter's concurrency into the decoder. The type is the wrong home for the runtime's mutable size,
   which is the same finding stated as a type error.
3. **Snapshot the limits under `growMu` at the top of `link`.** Correct, and still synchronising the
   duplicate: it takes a lock to read a stale-tolerant quantity that a lock-free authority already
   publishes.
4. **Delete the second copy.** The published image's length is already the authority — `size()` reads it,
   `grow` computes `newSize` from it — so the matchers ask for the current type instead of a cached field.
   `limits.Min` becomes the *declared* minimum only, which is what its name says and what every other
   reader already wants.

## Choice

**Option 4.** In three parts:

- **`memory.typeOf()` and `table.typeOf()`** return a `binary.Limits` that is the declared type with `Min`
  replaced by the current size from the published image. They are named for the reference function whose
  job this is (`instance.ml:76`'s `type_of`), because that is what the deletion restores: the reference
  *computes* an instance's type on demand and this engine had *cached* it.
- **Both write statements go.** `limits` is the declared type, unmutated after construction.
- **`link` computes the current type once per arm** and feeds both the verdict and the message from that
  one value. `matchTableType`'s signature changes to take the limits and element type rather than the
  `*table`, so the two arms read alike and neither can grow a second read later. That is `grow`'s own
  argument for loading `img` once, one file over: *one is what makes it obviously correct, so the argument
  that the two agree no longer has to be made.*

## Consequences

- **[ADR 0061](0061-grow-serialises-on-its-own-mutex-rather-than-a-compare-and-swap-over-the-descriptor-because-the-length-lives-in-two-places-and-only-one-is-in-the-descriptor.md)'s
  title names a premise this deletion removes, and it gets a note rather than a rewrite.** Its decision
  stands — `growMu` is still what makes a grow indivisible, because the read-compute-publish sequence is
  three steps and `relocate` is more — but *"the length lives in two places"* stops being true here, so
  the sentence a reader arrives at first would otherwise be a foreclosing one: written before a change,
  left standing after it, telling the next reader the tree is in a state it is not. The note is dated and
  points here. Not a reversal, so not an escalation: no option of 0061's is unmade and its mutex is not
  touched. The same paragraph appears in `internal/interp/table.go`'s `growMu` comment via
  [ADR 0075](0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md)'s
  transfer, and it is repaired in the same slice.
- **One image load per import match**, replacing one field read: an `atomic.Pointer` load and a `len`.
  Per import, not per instruction, and no figure is claimed or measured — *cheap is a grammar claim*, and
  the grammar is a pointer load on a path that decodes and validates a module. If it needs pricing it
  needs a benchmark, and this ADR does not pretend to one.
- **The observation becomes well-defined instead of merely lucky.** A matcher racing a grow now reads
  either the pre-growth or the post-growth image, both of which are legal answers to *"what size is this
  memory now"*, and the answer arrives through an ordered load rather than through undefined behaviour.
  Import matching is inherently a question about a moving quantity; what it stops being is a question
  answered by a race.
- **The accept direction is what could break, and the table side had no oracle at all.**
  `imports4.wast:19-37` and `TestGrownMemoryReexportsItsCurrentSize` cover the memory: a grown memory must
  still match an importer declaring the grown size. **No `.wast` file in the suite grows a table and then
  re-imports it**, which is what `internal/interp/table.go:table.grow`'s comment meant by the memory
  case's sibling being *"not yet measured"* — so today's `t.limits.Min = newSize` is unwitnessed in both
  directions, and deleting it would be invisible. `TestGrownTableReexportsItsCurrentSize` is that witness,
  added here, and it retires the *"not yet measured"* clause by measuring it. This is §9 G-3's shape
  exactly: an accept-direction fact the corpus cannot ask about gets a unit witness rather than a note
  saying the corpus covers it.
- **`matchTableType`'s signature change gets an assertion of its own, and it covers less than it first
  claimed.** Passing the limits and element type separately is a wiring opportunity, so the new witness
  also asserts that an `externref` import against a grown `funcref` table still rejects. Measured:
  dropping both `MatchValType` terms fails that assertion; *duplicating the first direction in place of
  the second* does not, because `funcref` against `externref` mismatches either way round. What catches
  the one-directional slip is the corpus — the all-gates-on lane moves from 0 fail to 1 — so the unit
  assertion is scoped to the presence of element-type matching and its comment says so rather than
  claiming the bidirectionality it does not test. *An unmeasured complement is not an empty one*, and
  the narrowing is recorded before landing rather than after a reader finds it.
- **Two `-race` controls, one per copy**, named for the rule and not the field:
  `TestImportMatchingDoesNotRaceAGrowingMemory` and `TestImportMatchingDoesNotRaceAGrowingTable`. A future
  reader who re-introduces a cached size — under a lock, under an atomic, or as a third copy — trips them
  even though the lines they first fired on are gone. Their verdict lives in CI's `race` step inside the
  two-architecture `build` job; **`make check` does not pass `-race`**, so a green from it says nothing
  about their subject, and that is stated in both comments rather than assumed.
- **A vacuity arm copied from the sibling control would have been wrong, and the reason is recorded in
  the control.** `globaltear_test.go`'s controls require the two agents to have observably overlapped,
  because a tear is a real-time event. Written that way here — counting iterations that observed size 1
  against size 2 — it failed **0 accepted / 200 refused**, since a `link` does strictly more work before
  its read than a `grow` does before its write. The same run reported the race on both arms anyway,
  because the detector's question is the absence of a happens-before edge and not an order of arrival.
  So the imported arm asserted a stronger property than the oracle needs and would have made the control
  red for something that is not its subject: *copying a control inherits its visible property, not its
  load-bearing one.* What replaced it is the vacuity claim the oracle does need — that each side performed
  its access, with the link's reaching the matcher established by its verdict being one the matcher
  produces.

## Pre-registration

**The board does not move, and a moved bucket is a finding worth more than this repair.** `internal/spec`
keys failure buckets by error text, and the refusal message renders the limits — but it rendered a
`Min` that already tracked growth, so the value is the same before and after and the text cannot change.
Registered before the mechanism exists: **the counts are identical on the same corpus pin, with no failure
stratum non-zero.** If a bucket moves, the deletion lost the current-size fact somewhere the corpus can
see, and the right response is to read the vector rather than to adjust anything here.

**Held, and measured against a baseline rather than asserted.** The branch's board and `main`'s were both
taken from `go test ./internal/spec/ -run TestPhase1Files -v` in the same tree, the second with this
slice's tracked edits stashed so the corpus pin and the fetched files were identical, and both printed the
same line: **60957 pass, 0 fail, 0 unsupported, 4187 gated, 0 unimplemented over 256 files.** Two identical
boards are ordinarily *the corpus declining to choose*, and the reason that reading does not apply is that
the forecast said so first and named what does discriminate: `TestGrownTableReexportsItsCurrentSize` is the
hand-built discriminating case for the fact no `.wast` file asks about, and it was watched fail before it
was believed. An identical board is evidence here only because the pre-registration was written before the
mechanism and the unit witness was built to cover exactly the gap the identity exposes.

## What this deliberately does not do

- **It does not change the public boundary.** No exported symbol reads `limits.Min`; the field is
  `internal/interp`'s and the two matchers are unexported. Outside the escalation set.
- **It does not touch `memory.size` / `table.size` or the guest-visible size instructions**, which
  already read the image's length and were never party to the duplicate.
- **It does not revisit `growMu`.** The lock stays and is still needed; only one of the reasons given for
  choosing it over a compare-and-swap retires, and the note in 0061 says which.
- **It does not widen `matchLimits`.** The `HasMax`/`Max` terms are the declared type and stay declared;
  a maximum does not move when a memory grows, and pretending it might would be a second invented fact.
