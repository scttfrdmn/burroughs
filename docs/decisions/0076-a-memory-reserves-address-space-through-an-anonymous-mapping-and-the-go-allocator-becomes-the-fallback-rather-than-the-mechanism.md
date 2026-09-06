<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0076 — a memory reserves address space through an anonymous mapping, and the Go allocator becomes the fallback rather than the mechanism, because `make` commits everything it reserves and a kernel commits what is touched

Date: 2026-09-06 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Scott's words after
[#670](https://github.com/scttfrdmn/burroughs/pull/670) merged were *"You pick under
decide-and-proceed"*, with §8 M-1 named as one of the two real gaps and *"the dissolving move at platform
level"* as his reading of it. That is latitude and a premise, not approval of any option below, and it is
in-session with no artifact behind it: every commit in this slice is `Ratio-Class: carried` and this
document claims his approval for none of its choices. *Durability is not independence.*

Filed against **[#672](https://github.com/scttfrdmn/burroughs/issues/672)**. The one question it does not
decide is **[#671](https://github.com/scttfrdmn/burroughs/issues/671)**: M-1's MUST cannot be met on every
port Go supports, and whether §8 gains a port scope is contract § text — one of the three subjects a slice
may not decide for itself.

## Context

**§8 M-1, verbatim:** *"`memory.grow` MUST be amortized O(pages touched): address space reserved up front,
commit on grow, no full-copy growth path. Go will call it; it is not an exceptional event."*

The tree has the arms M-1 wants and does not have the reservation. `internal/interp/memory.go:allocate`
reserves capacity for a shared memory with a declared max, `internal/interp/memory.go:memory.grow`
reslices into that capacity when it fits, and
[ADR 0073](0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md)
refuses to relocate when a sibling agent could hold the abandoned image. What is missing is size: the
reservation is capped at `sharedReservePages = 128`, eight MiB, and every memory that is unshared or
declares no max gets no capacity at all and therefore reaches
`internal/interp/memory.go:memory.publish` — `make([]byte, n)` then `copy`, which *is* the full-copy
growth path M-1 forbids, in one function.

**The cap is not timidity; it is a fired rollback.** ADR 0051 pre-registered a bar of worst-of-five under
1 ms for reserving the largest declaration the address width allows, and the measurement came back
**4.288 ms best / 855.438 ms worst** for `(memory 1 65535 shared)`. Three orders over, so the rollback
fired and 128 is the largest rung whose *worst* case cleared the bar. The diagnosis is in that comment
too: the spread is the allocator's `needzero` — a fresh arena span is handed out already zero, and a span
the allocator has recycled is cleared first, so a 4 GiB reservation can buy a 4 GiB `memclr`.

**That diagnosis is the whole decision.** `make([]byte, n, reserve)` does not reserve; it *commits*, and
it may zero. There is no argument to be had with the Go allocator about this, because zeroing a recycled
span is the guarantee the language makes about `make`. M-1's sentence describes a different primitive
altogether — one where the reservation is an address-space bookkeeping entry, a page becomes real when it
is first stored to, and a fresh anonymous page is zero because the kernel hands out a zero page rather
than because anybody memset it. That primitive is `mmap` with `MAP_ANON|MAP_PRIVATE`.

**Direction before the numbers, and it is direction only.** An unqueued local poke on the dev box — a
poke, so its figures are not results and do not appear in this document — reserved 4 GiB in single-digit
microseconds with no change in resident set, against the same reservation through `make` costing
hundreds of milliseconds and 3 GiB of RSS, and made the cost of touching one fresh page flat in the
memory's current size. #672 carries the pre-registration; `### Measured` below carries the queued run.

## Options

**(A) Keep the Go allocator and raise `sharedReservePages`.** Rejected on the measurement that set it.
The cap is not a policy knob that was chosen conservatively — it is the largest value whose worst case
cleared a registered bar, and raising it re-buys the 855 ms. Nothing about this option is better than it
was in 0051.

**(B) Reserve address space with an anonymous mapping; keep the Go allocator as the fallback.** Chosen.
The reservation becomes what M-1 says it is, the growth arms that already exist (reslice inside the
reservation) become the ordinary path instead of the lucky one, and `publish`'s copy survives for the
ports and the run-time failures where a mapping is unavailable.

**(C) Mapping plus `Mprotect`, so the guest's current bound is enforced by hardware.** Deferred, not
rejected — and deliberately not bundled. The engine already checks the bound in software on every access
and the check is not what M-1 is about; `Mprotect` is absent from stdlib `syscall` on `freebsd`, so
bundling it would narrow (B)'s port set for a benefit (B) does not need. It is a separate decision with a
separate measurement, and its subject is access cost rather than growth cost.

**(D) Grow the mapping incrementally with `MAP_FIXED` instead of reserving up front.** Rejected on the
contract's own words — "address space reserved up front" — and on the mechanism: `MAP_FIXED` over the
region adjacent to an existing mapping will silently replace whatever is already there, which in a
process that shares its address space with the Go heap is a way to lose unrelated memory rather than a
way to grow.

**(E) `GOEXPERIMENT=arenas` / `runtime`'s arena API.** Rejected: it is experimental, off by default, and
not a stable stdlib surface. An engine whose growth path depends on a `GOEXPERIMENT` has a conformance
claim that evaporates when the experiment does.

## Choice

**(B), with the reservation bound by one rule that does not branch on the address type, and the fallback
kept as a real path rather than as an apology.**

### The primitive, and where it lives

Two files, split on the predeclared `unix` build tag:

- `internal/interp/reserve_unix.go` — `reserveMapping(n int) ([]byte, bool)` calling
  `syscall.Mmap(-1, 0, n, PROT_READ|PROT_WRITE, MAP_ANON|MAP_PRIVATE)`, and `releaseMapping([]byte) error`
  calling `syscall.Munmap`.
- `internal/interp/reserve_other.go` — the same two symbols, reporting unavailable.

**`unix` is the right constraint and it is measured rather than assumed.** A compile probe over stdlib
`syscall` — one library package, built per `GOOS/GOARCH`, absence read from the build error — compiles the
anonymous-mapping expression on **every** port for which that tag is true, `ios` and the 32-bit linux
ports included, and on none of `windows`, `plan9`, `js/wasm`, `wasip1/wasm`. The first version of that
probe built a `package main` and reported `ios` as unavailable on `requires external (cgo) linking, but
cgo is not enabled`, which is a link error saying nothing about the symbol; the correction is on #672,
because the lesson is the reusable part — a build probe has two channels and only one of them answers the
question being asked.

**This is the engine's first `syscall` import.** Stated because it is a real widening of what the engine
depends on: there was no such import anywhere in the module before this decision. It is still pure Go and
still cgo-free — `syscall` is stdlib, and the mapping is one call — and `golang.org/x/sys` remains out of
reach by the dependency-free `go.mod`, which is why `windows` is named as reachable-but-later rather than
implemented: `syscall.NewLazyDLL` gets to `VirtualAlloc` without a dependency, and that is its own slice.

### Reserve to what

One rule for both address types, because the cases that differ are exactly the cases where a rule that
branched would be guessing:

1. **A declared max** is the reservation. The module's own number, which is 0075's rule for tables read
   across.
2. **No declared max** reserves the address type's own ceiling for an i32 memory — `maxPages32`, which is
   `0xffff` and not `0x10000`, because a 65536-page memory would be exactly 2^32 bytes, one past the
   largest i32 address
   — and the *same* `maxPages32` for a memory64, whose ceiling is nothing at all
   (`internal/interp/memory.go:validSize` returns true unconditionally on that arm, following the
   reference). The ceiling is an existing constant rather than a new number on purpose: rule 2 is where
   the engine speaks in its own voice, and borrowing the address type's bound is the narrowest voice
   available. So a memory64 that declares no max carries a **named engine limit** just under 4 GiB rather
   than an unbounded reservation, counted by
   `internal/interp/memory.go:growthRefusedPastReservation`, which already exists for exactly this shape
   and already carries the discipline of stating which programs it excludes.
3. **A reservation that cannot be served** — the mapping is refused, or the size exceeds `math.MaxInt` —
   falls back to `make` and the memory keeps today's behaviour, including its ability to reach `publish`.

Rule 2 is where the engine names a limit rather than the module, so it is the one to watch: it is a
number in the engine's voice, and the thing that makes it honest is that it is counted and that the
counter's comment says which programs it excludes.

### Lifetime: a cleanup, not a `Close`

A mapping is not garbage. Something has to unmap it, and the obvious candidate is wrong:
`internal/interp/host.go:Instance.Close` cannot, because a memory may be imported into several instances
and the closing one has no way to know it is the last. Reference-counting the attached worlds would work
and is more machinery than the question needs.

**`runtime.AddCleanup` on the `*memory`, with the mapping's own slice as the argument.** The lifetime that
is already correct is reachability: when no instance can reach the memory, nothing can reach the mapping
either, and the cleanup runs then. The argument is the full-capacity slice, which is what `Munmap` needs
and which holds no pointer back to the `*memory` — the condition `AddCleanup` imposes, and the reason this
is a cleanup rather than a finalizer.

**The property this rests on, stated because it is what a future edit would break:** no exported method
hands an embedder a slice that aliases a memory's backing array. `internal/interp/host.go:Caller.Read`
copies into a fresh `[]byte`, which is what makes the cleanup safe — a caller holding an aliasing subslice
would keep the *bytes* reachable without keeping the `*memory` reachable, and that is a use-after-free
with the GC's help. It gets a control rather than a sentence.

### What does not change

- **The growth arms.** Reslice inside the reservation, or relocate under 0073's predicate. This decision
  changes where the reservation comes from and how big it is, not what `grow` does with it.
- **`checkBaseAlignment`.** A mapping's base is page-aligned, so ADR 0051's atomics premise and ADR 0053's
  `wordAligned` predicate are satisfied more strongly than by the allocator, not less. The assertion stays
  where it is, since the fallback path still needs it.
- **The zero-fill guarantee.** A fresh anonymous page is zero, which is the same promise `make` gives; it
  gets a witness rather than a claim, because a memory that read as garbage would be a conformance defect
  visible to any guest.
- **`sharedReservePages`.** It becomes the *fallback* path's cap, documented as such. Deleting it would
  delete 0051's measured result along with it.

## Consequences

- **M-1 becomes conformant on the `unix` ports and stays non-conformant on `windows`, `plan9` and the
  wasm ports.** That split is #671's question, and until it is answered no release note or §9 sentence
  may say M-1 is met, unqualified.
- **Both engine-limit counters lose most of their population.** A memory reserved to its declared max
  never reaches the relocating arm, so `growthRefusedPastReservation` and
  `growthRefusedWithASiblingAgent` — and with them ADR 0073's refusal *for memories* — become unreachable
  on the mapping path. **Unreachable, not repealed**: the fallback path reaches all of them, and a
  counter's population shrinking is not a reason to delete the counter.
- **The table twin does not come along, and the premise that scheduled this slice said it would.**
  Address-space reservation lifts the memory limitation and cannot lift the table one, because a table
  slot is a `ref` carrying an `*Instance` and a mapping is `[]byte`. ADR 0075's ladder arm B already
  measured what reserving `[]ref` costs — ten reserved tables per rung, `runtime.GC()` timed, rising 10×
  across the ladder — so a table reserved to its declared max trades a growth refusal for a mark-time
  regression. The 113 corpus tables that declare no max are lifted by a **pointer-free slot
  representation**, which is a second decision this one does not make.
- **Virtual size grows enormously while resident size does not.** An embedder with an address-space
  rlimit or a container `--memory-swap`-style bound will see the mapping refused and get the fallback.
  That is why rule 3 is a path rather than a panic, and why the refusal is counted.
- **The `!unix` arm is compiled by nothing in this tree.** `make check` runs on the dev box and CI runs
  on linux, so the fallback file would rot unobserved. The `build` target already carries the answer for
  the analogous case — it builds the `burroughs_endtable` arm precisely so a tagged arm the gate never
  builds cannot rot — so it gains a cross-compile of one non-`unix` port on the same argument. It is the
  same build over the same tree with one variable different, not a second oracle.
- **`-race` and 4 GiB mappings.** The race detector shadows memory it observes; the mapping is only
  touched up to the guest's current size, so the shadow follows the touched pages rather than the
  reservation. Stated as a thing that was checked rather than assumed, because a suite that could not run
  under `-race` would cost more than this decision buys.

## Rollback

Registered in #672 before any number existed, restated here so it can be checked against `### Measured`:

- **Arm A**, reservation cost, bar worst-of-five under 1 ms at every rung including 65535 pages. If 65535
  misses it, the mapping path keeps a cap and its value is the largest rung whose worst clears the bar —
  0051's own rule, not a new one.
- **Arm B**, one-page growth at increasing current size. If the mapping arm is not flat within 2× across
  the rungs, this document does not get to claim "amortized O(pages touched)": the win narrows to
  allocation time and the claim narrows with it.
- **Arm C**, RSS after reserving 65535 pages with only the minimum touched, bar under 1 MiB per 4 GiB
  reserved. If RSS tracks the reservation instead of the touches, rule 2 above cannot reserve an
  address-type ceiling and the no-declared-max cases keep a cap.
- **Arm D**, mark time with ten reservations live per rung, expected flat because the mapping is off-heap
  and a `[]byte` is pointer-free in the heap too. If it rises, something is scanning the mapping and the
  analysis in this document is wrong.
- **Arm E**, the control: `make([]byte, n, reserve)` at the same rungs, re-taking 0051's 855 ms column on
  the same host as the mapping arm rather than comparing against a remembered number.

Two guards on the instrument itself: **assert the arms differ**, since a run whose mapping path silently
fell back to `make` would print two agreeing columns and read as a null result, and **assert the
reservation happened** from the mapped capacity rather than from the absence of an error.
