# 0051 — The atomics become sequentially-consistent word operations over the backing array, because the proposal fixes the ordering and leaves only the mechanism

Date: 2026-08-31 · Status: **proposed** — no stamp exists to cite for the *mechanism*, and *a
`Status:` field is a citation to an approval*, so it stays open until one does. What is stamped is
the **scheduling**: Scott's option-1 ruling, recorded on [ADR
0050](0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md)
and in `internal/interp/thread.go`, ordered *"discharge #542 first — #542 → #516 → #10"* with one
constraint: *"scope it to what §4 requires for the atomics that exist. If it grows arms that aren't
discharging #542, it comes back to me."* That constraint is the reason this ADR's first section is
about scope rather than about atomics.

Filed against **#542**. One ADR, one implementation.

## Context

`internal/interp/atomic.go` implements all 67 opcodes of the 0xFE region as plain read-then-write on
the instance's byte slice — no lock, no intrinsic, no fence. `atomicRmw` is three separate steps:

```go
bs, err := mem.read(addr, offset, a.width)
old := slotOf(bs, a)
if err := mem.write(addr, offset, storeBytes(applyRmw(a.rmw, old, operand), a.width)); err != nil {
```

The subject is constructed rather than argued. Two threads doing 2000 `i32.atomic.rmw.add` each on
one cell land on **3392 of 4000**, with `-race` naming the read in `atomic.go` and the write in
`memory.go`. That measurement lives on **#554**, the parked T-1 PR whose `go` statement is what makes
it reachable.

### The scope question, and a premise of #542's own body that does not survive it

#542 prices its discharge as §4's work:

> the fix is not a change to this file in isolation: it is whatever memory model §4's litmus battery
> settles on, and choosing `sync/atomic` per width now would be picking that answer with none of
> that work done.

**Contract §4 has no clause about guest-to-guest atomicity.** Read end to end, §4 is *the boundary
memory model* and every clause is about a transition: B-MM-1 is host↔guest edges, B-MM-2 a wake's
scope, B-MM-3 engine locks across a resume, B-MM-4 host-call signatures, B-MM-5 the battery for
those. §2 is spawn, wait/notify, the per-thread slot and lifecycle; §3 is safepoints. Nothing in any
of them says an `i32.atomic.rmw.add` must be atomic.

So the authority is not this contract. It is **§0's correctness-neutrality** — *"any spec-conforming
guest runs correctly on Burroughs"* — made enforceable by **§9's proposal-suite gate**, which points
at the threads proposal's own normative text. That is the same authority ADR 0049 settled alignment
against, at the same pinned revision, and it is already fetched in this tree.

**The dependency runs the opposite way from #542's sentence.** B-MM-2 requires that a wake
synchronize *all* writes that happened-before it on the waking agent. That is unimplementable while
the atomics are plain: there is no happens-before to extend, because nothing establishes one. So §4
depends on #542, not the reverse.

Which means **Scott's ordering is right and the reason he was given for it was not**. The ruling
reversed an in-session sequence that had put spawn ahead of §4's model; the ground it now rests on is
stronger than the one in #542's body — #542 is not waiting on §4, it is what §4 waits on. Recorded
because *a ruling's premises are checkable separately from its conclusion*, and a reader who finds
only the conclusion cannot tell which of the two this was.

The consequence for Scott's constraint is that **"what §4 requires for the atomics that exist" is,
read literally, nothing** — and the constraint's evident intent is a bound on size, which the section
below honours by naming what was found and filed rather than absorbed.

### What the authority requires, which is less of a decision than expected

`third_party/spec-threads` at `cc535ada1aa21cfaa3cabf3ac73b89acef78a0a0` (2026-07-30), the revision
0049 pins and verified against the checkout's `HEAD`:

- **Every RMW is sequentially consistent, unconditionally.** `document/core/exec/relaxed.rst:35`:
  `ordact(ARMW loc byte₁* byte₂*) = SEQCST`. Not a parameter, not a default — the function has one
  case.
- **All atomics are sequentially consistent.** `relaxed.rst:244`: *"WebAssembly's atomic operations
  are also required to be sequentially consistent."*
- **Atomic accesses are always naturally aligned.** `relaxed.rst:242`. The engine already traps the
  rest, on the effective address, per 0049.

So there is **no ordering decision in this ADR.** The proposal fixes the strength at SC and this
project's job is to reach it. Go's `sync/atomic` is exactly SC — the Go memory model's own words are
that all atomic operations *"behave as though executed in some sequentially consistent order"* — so
the available strength matches the required one with nothing to spare and nothing wasted. What is
left is mechanism.

Worth stating because the ADR that was expected here was a comparison of memory orderings, and
`ordact`'s single case is what makes it a smaller document.

### Three findings from reading that authority, all filed rather than absorbed

Scott's constraint is about arms, so each of these got a number and, except where noted, no code in
this slice:

- **#556 — `memory.grow` reallocates the backing array, and the spec models a length change as an
  atomic RMW** (`relaxed.rst:246`). This one is **not** an arm: it is this decision's floor. An
  atomic operation on an array that can be replaced underneath it is not an atomic operation, so
  option A below is meaningless without it, and it is implemented here. The scope call is flagged in
  the report rather than made quietly.
- **#557 — aligned non-atomic integer accesses up to 32 bits must not tear**, and `loadValue`'s byte
  loop can (`runtime.rst:744`, called from the ordinary load and store at `instructions.rst:1763`
  and `instructions.rst:2315`). Different opcodes, different path, no code here.
- **#558 — `atomic.fence`'s no-op is conformant, and its comment gives the wrong reason.** `AFENCE`
  appears in the action grammar and in no consistency rule, so nothing constrains it; the header
  currently calls it debt of the same kind as the plain atomics. A comment correction rides this
  slice because it is in the same header the slice rewrites, and leaving a false confession beside a
  true repair is worse than either alone.

## Options

### A — `sync/atomic` on a word pointer cast per access from the backing array (chosen)

`atomic.AddUint32((*uint32)(unsafe.Pointer(&m.bytes[ea])), v)` and its siblings, after the existing
bounds and alignment checks.

The cast is **per access and nothing is cached**, which is the part worth arguing. A `[]uint32` view
captured at construction would go stale on any reslice, and staleness in a memory view is the
plausible-wrong-answer failure mode 0050 rejected option B for. A pointer computed from `m.bytes`
each time cannot be stale, and the cost is an address computation the bounds check already did.

Three costs, stated:

1. **This is the first `unsafe` in engine code.** The only current use in the tree is
   `internal/interp/i31op_test.go`, for `unsafe.Sizeof` in a struct-width pin. The contract forbids
   cgo and requires pure Go; `unsafe` is pure Go and neither `CLAUDE.md` nor the contract prohibits
   it. But a first-of-kind is not a mechanism detail, and it is **flagged for architecture review**
   rather than settled here.
2. **Absolute alignment is a premise Go does not document.** The proposal guarantees `ea mod
   width == 0`, which is alignment *relative to the memory's base*; `sync/atomic` on a 64-bit word
   needs 8-byte absolute alignment on 32-bit platforms. Measured, 800 allocations across 64 KiB to
   16 MiB: base `% 8` was **0 every time**, which is what the allocator's span alignment predicts
   for page-sized-and-larger objects. That is a mechanism, not a coincidence — and it is still
   undocumented, so it becomes an **invariant asserted at construction** rather than a premise
   trusted at every access. *A suspiciously clean result is a tell*; this one has a name, and the
   assertion is what makes the name unnecessary.
3. **It leaves the plain path alone**, so #557 stays open and `-race` will still report
   atomic-versus-plain pairs. That is a true report under Go's model and a *permitted* execution
   under wasm's, which is the consequence section's subject.

And one cost that turns out to be the opposite. **`-race` enables `checkptr`, which checks exactly
the premise cost 2 is about.** The compiler instruments every `unsafe.Pointer`-to-`*T` conversion
with `runtime.checkptrAlignment`, which panics on a misaligned result and on one that spans more
than one heap object. So the undocumented absolute-alignment premise is not merely asserted at
construction and hoped for: it is *machine-checked at every single access* on both CI architectures,
because `-race` is already a CI step. The construction-time assertion stays, because it names the
invariant where a reader can find it and it fires in non-race builds too — but the argument's weak
link now has an instrument on it that this slice does not have to build.

#### The three mechanism facts option A does not get to choose

Written into this ADR rather than discovered in the diff, because each one changes the code and the
first changes what "and its siblings" means:

- **Go has no 8-bit or 16-bit atomics**, and this is by design: `sync/atomic`'s own doc says
  operations on non-word-sized integers are *"inefficient or infeasible"* on many architectures. The
  narrowest is 32-bit. But the 0xFE region has widths 1 and 2 — `i32.atomic.rmw8.add_u`,
  `i64.atomic.store16`, and 30 more. So **widths 1 and 2 become a compare-and-swap loop on the
  containing naturally-aligned 32-bit word**, extracting and reinserting the field by shift and mask.
  The containing word always fits: a memory's length is a multiple of the 64 KiB page, so
  `(ea &^ 3) + 4 ≤ len` wherever `ea < len`.

  The property that makes this correct rather than merely conventional is that **the CAS compares the
  whole word**. A neighbouring byte's concurrent write — atomic or plain — changes the word, the CAS
  fails, and the loop re-reads. So the loop cannot lose a write to a byte it is not addressing, which
  is the failure this technique is usually suspected of. Those are *disjoint locations* in the model
  (`loc` is a region and a byte range), so no rule permits losing one; the full-word comparison is
  why none is lost.

- **The cast is host-endian, and option C was rejected for exactly that.** `*(*uint32)(p)` reads the
  four bytes in the host's order, where the guest's value is their little-endian reading — the
  property `memop.go` maintains explicitly and the second ground option C fell on. Option A does not
  get to keep that property for free just because it touches fewer lines. So all field arithmetic
  happens in **guest space**, with one normalizing involution between the host word and the guest
  word (identity on a little-endian host, `bits.ReverseBytes32/64` otherwise), and the shift for a
  sub-word field is then `8 × (ea − base)` on every host.

  This one gets an oracle rather than an argument: the normalized read of a word must equal
  `loadValue` of the same bytes, which is the byte loop the whole spec suite has already validated.
  *A repair is confirmed by the authority* — for a wire form, that is the little-endian assembly this
  engine is already known to get right, not the new path reading itself back.

- **One CAS loop serves all six RMW operators, reusing `applyRmw`.** Not `AddUint32` for add and
  `SwapUint32` for xchg and a CAS loop for xor: `applyRmw` is *"one copy of it is one place to be
  wrong"* by its own comment, widths 1 and 2 need the loop regardless, and six operators × two native
  widths is twelve dispatch arms to get right for a saving this ADR has not measured. Option A's
  headline `atomic.AddUint32` therefore appears only for what it is uniformly right for — the
  full-word load and store, where the field *is* the word and no loop is needed.

  **Compare-exchange is not on that short list either, and the reason is a signature mismatch rather
  than a width.** `atomic.CompareAndSwapUint32` returns a `bool`; `i32.atomic.rmw.cmpxchg` pushes the
  value it *observed*, which on failure is the value that was there. A bool cannot answer that, so
  even a full-word cmpxchg reads the word, compares, and only then swaps — the same loop, exited
  early when the comparison fails. Corrected here after the code was written, because this ADR's
  earlier draft listed compare-exchange among the native primitives and *the defect stated as the
  rule* cuts both ways: a reader trusting the list would have gone looking for a bug in the loop.

  The native-RMW fast path is filed as **#559** with a benchmark rather than dismissed with a
  sentence. The tempting sentence is that in an interpreter the difference between one `LOCK XADD`
  and a load-plus-CAS is lost in dispatch overhead. That is a *cheap-is-a-grammar-claim*: as
  falsifiable as any other, unmeasured here, and so it is a number to go and get, not a reason.

### B — A mutex per memory around each atomic operation

Correct, portable, no `unsafe`: Go's mutex gives SC between locked sections, so atomic-versus-atomic
is ordered. Rejected on two grounds, the second decisive.

The weaker ground is cost — an uncontended `Lock`/`Unlock` pair is several atomic operations where
option A is one instruction.

The decisive ground is **what the hot path is for this engine.** §0 is performance-partisan toward
Go, and a Go guest's atomics are not incidental: every `sync/atomic` call, every channel operation
and every `sync.Mutex` in guest code lowers to wasm atomics. So B makes the guest's own mutex cost
two mutexes, and the tax lands precisely on the workload §1 names. A lock is also the shape §4 spends
a clause forbidding near a boundary (B-MM-3), which makes it the wrong habit to establish here even
though this particular lock would never be held across a resume.

### C — Represent linear memory as a word array rather than `[]byte`

Atomics land naturally aligned with no `unsafe` at all: `atomic.AddUint32(&words[ea/4], v)`.
Genuinely the only option that avoids `unsafe`, so it gets a real hearing. Rejected on three counts:

- **Blast radius.** Every plain load and store, every bulk operation, data-segment instantiation,
  `memory.copy`/`fill`, `memory.grow`, and the SIMD loads read `[]byte` today. This is the arm Scott's
  constraint is about.
- **It makes the representation host-endian.** `memop.go` assembles values little-endian explicitly,
  which is why the engine is correct on a big-endian host; a `[]uint32` view of the same bytes
  reorders them there. Trading a documented portability property for the avoidance of one import is
  the wrong direction.
- **Two widths need two views.** i32 atomics want `[]uint32` and i64 atomics `[]uint64`, and one
  array cannot have both without the `unsafe` this option exists to avoid. Reaching i32 atomics
  through `CompareAndSwapUint64` loops on half a word is slower than option A and harder to read.

### D — A global lock for shared-memory instances

Rejected against the contract rather than on cost: §5's H-1 says a blocking host call blocks its
thread only, and T-2 deletes the no-agent-may-block class outright. A global lock reintroduces the
single-M assumption §2's provenance note exists to bury. Recorded because it is the shape a first
draft reaches for.

## The pre-registration, written before the numbers exist

**Two oracles, for two independent properties, and neither substitutes for the other.**

- **Mechanism correctness: `atomic.wast` stays at 297/297.** Those 297 vectors exercise all 67
  opcodes single-threaded, so they are a real regression check on the word-cast arithmetic — a
  botched width, offset or endianness breaks them. They are also *completely blind* to atomicity,
  which is #542's whole premise.
- **Atomicity: the two-thread witness.** 2 threads × 2000 `i32.atomic.rmw.add` on one cell must
  yield exactly **4000** where it yields 3392 today, and must report **zero data races** under
  `-race`. Not a forecast — the deliverable.

**Two forecasts that can fail.**

1. **The core board does not move by one vector: 60957 pass, 0 fail.** The plain path is untouched,
   so any movement means the word cast reached a non-atomic access. This is close to an analytic
   zero and is registered anyway because its *failure* would be informative even though its pass is
   not: it is the check that option A stayed inside the 0xFE region.
2. **A shared memory's capacity reservation costs under 1 ms at instantiation, for the largest
   declaration the address width allows.** #556's repair reserves `max` pages of capacity so the
   array never moves, and `(memory 1 65536 shared)` therefore reserves 4 GiB. The mechanism this
   rests on is that Go serves a large fresh allocation from newly mapped arena pages and skips the
   memset when the span is known-zero, so the pages fault in on touch. **Rollback if it exceeds
   that**, stated now rather than invented later: reserve lazily by rounding the reservation up to a
   configurable ceiling and falling back to allocate-and-blit above it, accepting that a shared
   memory grown past the ceiling needs the header protected some other way.

   This is the forecast with real risk in it, because a span the allocator has previously used *is*
   cleared, and a 4 GiB memclr is not a millisecond. It is also the one whose mechanism I can name
   but have not measured, which is the honest reason it is registered rather than asserted.

**No new instrument is built.** The witness goes in `internal/interp` beside T-1's tests, the board
figures come from `go test ./internal/spec/ -run TestPhase1Files -v`, and `-race` is already a CI
step on both `ubuntu-24.04` and `ubuntu-24.04-arm` — so the atomicity claim is checked on a
weakly-ordered platform without this slice adding an arm. That does **not** discharge #10: B-MM-5
asks for a litmus battery over boundary edges, and a race-free counter is neither a litmus test nor
about the boundary.

A forecast beaten is a forecast falsified. If forecast 2 comes out at a microsecond on every size I
will say so and ask why, rather than bank it.

## The numbers, read back against that registration

**Both oracles green.** `atomic.wast` holds at **297/297** (`TestThreadsProposalLane` ok). The
witness yields exactly **4000**, and `-race` × 10 reports zero races and no `checkptr` panic — so the
undocumented absolute-alignment premise of cost 2 was machine-checked on this host at every one of the
4000 accesses, which is the instrument the cost-that-inverted promised.

**The witness was falsified before it was trusted**, because it passed in 0.00s and *a suspiciously
clean result is a tell*. `atomicRmw` was patched back to the plain three-step read-modify-write:
**3493, 3517 and 3637 of 4000** across three runs. Three different losses is the proof the goroutines
genuinely overlap rather than serialising — a fixed number would have been the shape of a
coincidence. Twenty repaired runs then green. The zero-race half was vacuity-checked separately, since
*a comparison needs a vacuity check* and "no races reported" is exactly the claim a disabled detector
also makes: the plain mutation under `-race` prints `WARNING: DATA RACE / Write at 0x00c0002a0000 by
goroutine 9`, so the detector was demonstrably watching the same code path it later cleared.

**Forecast 1 exact.** The core board reads *"board total over 256 files: 60957 pass, 0 fail, 0
unsupported, 4187 gated, 0 unimplemented"* — the registered figure to the vector. Its pass says little,
as registered; what it rules out is the word cast having reached a plain access.

### Forecast 2 is falsified, by three orders of magnitude

Registered: *under 1 ms at instantiation, for the largest declaration the address width allows.*
Measured, best and worst of five `newMemory` calls each:

| declaration | best | worst |
| --- | --- | --- |
| `(memory 1 65535 shared)` | 4.288 ms | 855.438 ms |
| `(memory 65535 65535 shared)` | 546 ms | 1.256 s |

The named mechanism — a fresh arena span is known-zero, so the memset is skipped — is real and is
also *unreliable*, which the registration said in the sentence admitting it was the forecast with real
risk in it. A recycled span is cleared first, and the spread between 4 ms and 855 ms on identical
inputs is that clearing appearing and disappearing. This is why it was registered rather than asserted.

**The pre-registered rollback fired, and it narrowed the reservation to a measured ceiling.** *A
failed pre-registration narrows, it does not licence* — so the ceiling is not a number chosen to make
the bar, it is the largest one whose **worst** case still clears the bar that was registered before any
of this was known:

| reservation | worst of five |
| --- | --- |
| 64 pages | 474.7 µs |
| **128 pages (8 MiB)** | **618.6 µs** |
| 256 pages | 1.161 ms |
| 512 pages | 2.673 ms |
| 1024 pages | 26.05 ms |
| 2048 pages | 70.84 ms |

`sharedReservePages = 128`. The worst column is the one read, because the best case is what a fresh
process sees once and the worst is what a long-running host sees repeatedly, and an instantiation cost
is paid by whichever it lands on. **This value bounds which programs run** — a shared memory that grows
past 8 MiB fails its grow — so it is flagged for Scott rather than treated as a tuning constant.

**One deliberate deviation from the registered rollback, and the registration was wrong rather than
merely expensive.** It said *"falling back to allocate-and-blit above it."* That is unsafe in
combination with this ADR's own decision: the atomics hold a raw pointer into the array across an
access, so replacing the array under a concurrent thread is a **use-after-free**, not the header tear
the registration imagined. A shared memory above its reservation therefore returns **−1** instead, which
is conforming rather than a compromise: `memory.grow` reports failure in its result, and the reference
interpreter fails grows of its own for engine limits (`memory.ml:60-67`, `SizeOverflow`/`SizeLimit`).
Flagged for Scott, because a rollback that gets amended after the numbers arrive is the shape of the
thing the registration exists to prevent — the distinguishing fact is that this amendment is on the
*mechanism* and away from the engine's freedom, while the ceiling above is the registered rollback
executed as written.

## Consequences

- **The race detector cannot be the oracle for a conforming engine on shared memory, and this is
  where that gets recorded.** `relaxed.rst:248` is explicit: *"Unlike some other relaxed memory
  models, WebAssembly does not declare data races to be undefined behaviour."* A guest may race a
  plain store against an atomic RMW and the model gives that execution a defined, if
  non-deterministic, set of outcomes. Go's model calls the same pair undefined. So after this slice
  `go test -race ./...` will report true-under-Go, permitted-under-wasm races for any test that
  races plain accesses — and the resolution is **not** to make plain accesses atomic, which would
  pay SC cost on every load and store in the engine to satisfy an instrument. It is that #10's
  battery needs its `-race` disposition decided as part of #516, with the reason in the tree rather
  than in a suppression. Named here because the alternative is discovering it inside the PR that
  writes the litmus tests.
- **`slotOf` and the three-step read-modify-write leave the file entirely.** This bullet said they
  *survive for the non-atomic paths*, which the diff falsified: `atomic.go` had no non-atomic paths
  left once all four executors went through the cell, `slotOf`'s last caller went with them, and
  `deadcode` would have failed the gate had it stayed. `applyRmw` survives, called from inside the CAS
  loop, which is the reuse the third mechanism fact argues for. The header section is rewritten either
  way: it currently instructs a reader to discharge #542, and *a comment asserting the property the
  code lacks makes review confirm the bug* — the inverse holds too, and a stale confession is what
  #558 is about.
- **The single-thread tripwire is re-pointed, and it kept its mechanism.** It watched for the first
  `go` statement in this package's non-test files and told the reader to discharge #542, so this slice
  falsifies half of its premise and none of the other half: the atomics are synchronised, the plain
  accesses in `memop.go` are still a byte loop that tears (#557), and §4's boundary model is still
  #516. *A tripwire whose subject dissolves is re-pointed rather than retired* — so
  `TestAtomicsArePlainWhileTheInterpreterIsSingleThreaded` becomes
  `TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded`, same `go`-statement scan
  and same vacuity floor, with its failure message now naming #557 and #516 instead of #542. Nine
  citations moved with it, including this ADR's and — with a postscript rather than a rewrite, since it
  is accepted — ADR 0050's, whose chain says *"it is retired"*. **#554 merges after this**, which is
  the last link in the chain the ruling ordered.
- **The alignment invariant is asserted where the memory is built, not where it is used**, so a
  platform whose allocator returns an oddly-aligned span fails one loud construction rather than
  producing torn atomics on 32-bit hosts. A zero-page memory is legal — `(memory 0)` appears in
  `align.wast:3` — so the assertion is written against a slice that may have no first element.
- **This decision does not touch the `align=` immediate**, which is a third rule sharing the word:
  the validator's `1 lsl align = size` equality, landed in PR #538 with the corpus gap it exposed
  filed as #537. 0049 said this once already and it is repeated because the three rules keep being
  read as one — and the pair is spelled out here because 0049 writes it `(#538, #537)`, which reads
  as two issues where one is a merged PR. Both resolve, so no sweep could tell.
