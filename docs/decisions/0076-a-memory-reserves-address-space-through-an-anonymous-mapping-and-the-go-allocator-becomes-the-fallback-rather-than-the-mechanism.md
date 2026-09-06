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

### Measured

`internal/interp/memladder_test.go:BenchmarkMemoryReservationLadder`, on the queue as required.
**Provenance:** host `janus.local` (i9-9960X, linux/amd64), pueue group `measured`, **task 20**, label
`burroughs/fc19e76`, **0 concurrent tasks running on the box at submit time**, submitted through
`scripts/xcheck-amd64.sh` (which copies the working tree and reconciled the corpus at 257 vectors, 0
sidecars). Five arms, one invocation, exit 0.

**No rollback arm fired.** Every bar registered in #672 was cleared, and the arm that decides M-1's sentence
was cleared on both statistics rather than one.

**Arm A — `newMemory` at the rung** (bar: worst of five under 1 ms):

| rung (pages) | reserved bytes | best | worst | clears 1 ms |
| --- | --- | --- | --- | --- |
| 1 | 131072 | 1.663µs | 18.709µs | yes |
| 16 | 1048576 | 1.663µs | 2.771µs | yes |
| 256 | 16777216 | 1.654µs | 2.876µs | yes |
| 4096 | 268435456 | 1.719µs | 5.852µs | yes |
| 16384 | 1073741824 | 1.596µs | 1.895µs | yes |
| 32768 | 2147483648 | 1.626µs | 3.158µs | yes |
| 65535 | 4294901760 | 1.61µs | 1.774µs | yes |

The top rung's worst is **1.774 µs against a 1 ms bar**. What matters more than the margin is the *shape*:
the column does not rise with the rung at all — 1.61 µs of best at 4 GiB and 1.663 µs at 128 KiB — which is
what it looks like when the cost is one `mmap` and not a function of the size. ADR 0051's cap
(`sharedReservePages = 128`) existed because the same bar was missed at 4 GiB; on this host, with this
mechanism, it is cleared by about **560×**, and the largest rung is the *fastest* row in the worst column.

**Arm B — one-page `grow` at increasing current size** (bar: flat within 2×), 5 reps of 64 grows each:

| current pages | best per grow | worst per grow |
| --- | --- | --- |
| 2 | 50ns | 71ns |
| 17 | 50ns | 56ns |
| 257 | 50ns | 55ns |
| 4097 | 50ns | 56ns |
| 16385 | 50ns | 58ns |
| 32769 | 50ns | 56ns |
| 65001 | 50ns | 57ns |

Best **1.00×** across the ladder, worst **1.29×**, and the worst column's maximum sits at the **smallest**
rung — the opposite of the M-1-falsifying shape, which would put it at the largest. This is the arm the
sentence *"amortized O(pages touched)"* rests on, and it is the one this document was least entitled to
assume: a one-page grow at 65001 pages costs what a one-page grow at 2 pages costs.

**Arm C — RSS with only the minimum touched** (bar: under 1 MiB per 4 GiB reserved):

| reserved bytes | RSS delta | bar | clears |
| --- | --- | --- | --- |
| 4294901760 | 8192 | 1048576 | yes |

**8 KiB of resident memory for 4 GiB of reserved address space** — two OS pages, against a bar of one
mebibyte. This is rule 2's premise measured rather than argued: reserving an address-type ceiling for a
memory that declared no maximum is sound because the reservation is not a commitment. Without this figure
rule 2 would have had to keep a cap, which is the arm the rollback registered.

**Arm D — mark time with ten reservations live** (expected flat):

| rung (pages) | reserved bytes live | best | worst |
| --- | --- | --- | --- |
| 1 | 1310720 | 280.924µs | 358.109µs |
| 16 | 10485760 | 200.657µs | 265.241µs |
| 256 | 167772160 | 209.744µs | 277.713µs |
| 4096 | 2684354560 | 240.273µs | 284.321µs |
| 16384 | 10737418240 | 237.776µs | 282.511µs |
| 32768 | 21474836480 | 242.031µs | 300.185µs |
| 65535 | 42949017600 | 248.131µs | 296.209µs |

Flat, and the highest row is again the smallest rung. **42.9 GB of live reservations do not move mark
time**, which is the direct contrast with ADR 0075 arm B's **10×** rise across the reserved-`[]ref` ladder
and is why memories could go first while tables cannot: a `[]byte` is pointer-free and a mapping is
off-heap, so there is nothing in it for the collector to walk. Had this arm risen, the analysis in this
document would have been wrong and the rollback said so.

**Arm E — the control**, `make([]byte, pageSize, rung*pageSize)` at the same rungs, on the same host, in
the same invocation:

| rung (pages) | reserved bytes | best | worst | RSS delta |
| --- | --- | --- | --- | --- |
| 1 | 131072 | 558ns | 28.056µs | -110592 |
| 16 | 1048576 | 3.135µs | 564.587µs | 5316608 |
| 256 | 16777216 | 19.281µs | 1.721808ms | 17997824 |
| 4096 | 268435456 | 236.12µs | 24.38729ms | 253583360 |
| 16384 | 1073741824 | 612.901µs | 97.057255ms | 1880989696 |
| 32768 | 2147483648 | 1.41721ms | 193.324088ms | 4298805248 |
| 65535 | 4294901760 | 2.709965ms | 389.043337ms | 6454640640 |

Three readings, in descending order of how much they were needed:

1. **The control fails the bar at the top rung on both statistics** — 2.709965 ms best and 389.043337 ms
   worst against 1 ms — so ADR 0051's rollback was not a fluke of one host or one Go version. Against arm
   A's top rung this is about **1 680× on best and 219 000× on worst**. The remembered 855 ms figure is
   *corroborated* rather than assumed: 389 ms on different hardware is the same phenomenon, not a
   coincidence, and the point of re-taking the column here was that a remembered number cannot be compared
   to a fresh one.
2. **The control's cost is a function of the reservation and arm A's is not.** Read down the two `worst`
   columns: arm E rises by five orders of magnitude across the ladder while arm A does not rise at all.
   That is the mechanism claim — `make` commits and clears what it reserves, a kernel commits what is
   touched — visible as two shapes rather than as two numbers.
3. **The RSS column is guard 2, in the printed-not-asserted form this document registered.** 6.45 GB
   resident for a 4.29 GB reservation, against the mapping's 8 KiB: the control commits *more* than it
   reserved, because a growing `make` holds an old span while it fills a new one. The reason this is
   reported and not asserted is in the harness's own comment — a `make` landing on a fresh span skips its
   clear, which is 0051's own 4.288 ms best-case column, so an assertion here would fail for the reason
   0051 was right about. The rung-1 row's **negative** delta is the same instrument telling the truth about
   itself: at that size the figure is collector noise, not a measurement.

**Guard 1 held throughout** — every one of arm A's 35 reps asserted, from the mapped capacity *and* from
`reservationUnavailable` not moving, that it had actually taken the mapping. The suite-wide counter line
from the same run reads `unavailable=0 … declined=0, released=113, release failures=0`, so nothing on this
host quietly fell back and every mapping the run made was unmapped by its cleanup.

**What is still not measured.** Arm C is Linux-only by construction (`/proc/self/status`, no cgo), so the
`darwin` dev box reports it as *not taken* rather than skipping it. And the one rule-3 population that costs
something — a memory64 declaring a minimum above 4 GiB, which keeps relocate-and-copy — is not on any of
these ladders, because asserting it means committing 4 GiB;
`internal/interp/reserve.go:reservationDeclined` is what makes that population countable instead.

## The platform gap: ruled non-conformant, not port-scoped (#671)

**Scott ruled [#671](https://github.com/scttfrdmn/burroughs/issues/671) after this decision landed, and it
resolves the one question this document deliberately left open.** The mechanism above meets §8 M-1 on
`unix` and cannot meet it where there is no mapping primitive, so the question was whether the MUST is
scoped to the ports that can express it or whether those ports stand as failing it. His words:

> The full-copy fallback is correct; programs run and pay O(size) per grow. So this is a performance
> property, not a correctness one. But port-scoping makes the clause true by construction everywhere and
> erases the fact that three ports are worse. **Record windows, plan9 and the wasm ports as non-conformant
> with a measured figure.** That keeps the gap visible — reconcile an extent, never floor it.

So **§8 is not amended.** M-1's MUST stays unconditional, and the following ports do not meet it:

```text non-conformant-ports
js
plan9
wasip1
windows
```

**That list is derived, not maintained.** `internal/testenv:TestTheNonConformantPortsAreTheOnesWithoutAMapping`
reads the fence above, asks `go tool dist list` for every GOOS the toolchain knows, and asks `go list` under
each which of the two reserve files the build constraints select — then asserts set equality **in both
directions**. The reverse direction is the one worth having: when
[#674](https://github.com/scttfrdmn/burroughs/issues/674) gives windows a `VirtualAlloc` reservation, a hand-kept
list would go on naming it as non-conformant, and a document that over-reports its own gap is one the next
reader has to re-verify line by line. Cross-compilation proves file selection and nothing more, which is why
the figure below is a separate instrument: *the classification test is runtime-vs-harness*, and this half is
entirely harness.

**`windows` is on that list as a defect with a filed repair, not as a platform limit.** `VirtualAlloc` with
`MEM_RESERVE` does exactly what M-1 asks; what is missing is Go's *portable* syscall surface, not the
capability. `syscall.VirtualAlloc` does not exist on any GOOS — `GOOS=windows go doc syscall.VirtualAlloc`
reports no such symbol, which is why this is #674 and not a two-line port — but `syscall.NewLazyDLL`,
`LoadDLL`, `GetProcAddress` and `Syscall9` are all present, a 4 GiB `MEM_RESERVE` written that way
cross-compiles under `GOOS=windows GOARCH=amd64` with no cgo and nothing added to `go.mod`, and the `build`
gate now compiles that port on every PR. `plan9` and the wasm ports are not in #674's scope and are their own
question: `js` and `wasip1` have no address-space primitive to reach for at all, and plan9's `segattach` is a
different mechanism with a different decision behind it.

### The measured figure: arm F

**Scott's ruling asks for a number, not an adjective — *"reconcile an extent, never floor it"* — so the
non-conformance has an arm of its own.** `internal/interp/memladder_test.go:BenchmarkMemoryReservationLadder`
arm F times a **one-page** `memory.grow` at seven rungs of existing size, twice at each rung: once with
`reserveMapping` refused, which is what those four ports run unconditionally, and once on the mapping. Both
columns report time *per grow*.

`janus.local`, group `measured`, pueue task 23, 0 concurrent tasks at submit, native x86-64, `go test
-benchtime=1x`, 5 reps per cell:

| current pages | fallback best | fallback worst | mapped best | mapped worst | worst ratio |
| --- | --- | --- | --- | --- | --- |
| 2 | 24.76µs | 99.059µs | 53ns | 176ns | 562.8x |
| 17 | 233.109µs | 735.091µs | 84ns | 149ns | 4933.5x |
| 257 | 1.873329ms | 3.612156ms | 44ns | 45ns | 80270.1x |
| 4097 | 45.669866ms | 60.68681ms | 68ns | 116ns | 523162.2x |
| 16385 | 186.346939ms | 344.914566ms | 70ns | 380ns | 907669.9x |
| 32769 | 353.818634ms | 693.273177ms | 40ns | 44ns | 15756208.6x |
| 65001 | 695.004914ms | 1.378040963s | 40ns | 44ns | 31319112.8x |

**The extent, stated as the ratio the ruling asks for: growing an unshared memory by one page costs 28070x
more at 65001 pages than at 2 on the four non-conformant ports, and does not vary with size at all on the
mapping.** The fallback's own ladder is the load-bearing half — `695.004914ms` against `24.76µs`,
best-against-best — and the mapped column beside it spans 40 ns to 84 ns across a 32000x range of memory
size with no trend, which is arm B's *amortized O(pages touched)* claim reproduced inside this arm, on the
same invocation. That matters because it makes the comparison internal: the two columns of any one row were
measured microseconds apart on one box, so the ratio is not a cross-run difference wearing a result's
clothes.

**Which statistic is quoted was decided after seeing numbers, and that ordering is disclosed rather than
hidden.** The forecast registered on #671 was *above 100x*; every reading of the data clears it by three to
four orders of magnitude, so nothing turns on the choice. The ladder ran three times from an identical tree,
and the two candidate forms are not equally stable: best-against-best spans 1.42x across the three runs,
worst-against-worst 2.78x. Decomposed by term, the *numerator* is stable to 4% and the *denominator* moves
47% — a one-second `memcpy` at the top rung is a quantity nothing perturbs, while a 64 KiB copy at the bottom
is tens of microseconds, where the allocator and the scheduler are the same order as the signal. An extent
divided by its noisiest term is *a near-miss on an extreme statistic* waiting to happen, so both forms are
printed and the stable one is quoted. The three runs are tabulated on
[#671](https://github.com/scttfrdmn/burroughs/issues/671).

**What the figure does not say.** It is an amd64 Linux measurement of the *fallback path*, taken on a host
that has a mapping primitive and was told to refuse it. It is therefore the cost of the mechanism those four
ports use, not a measurement taken on any of them — no runner in this project's CI or lab is `windows`,
`plan9`, `js` or `wasip1`. Nothing here asserts they are otherwise identical; what is asserted is that they
take this path, which is the build-tag half held by
`internal/testenv:TestTheNonConformantPortsAreTheOnesWithoutAMapping`, and that this path costs this much.
*The classification test is runtime-vs-harness*, and the composite claim — *these ports are non-conformant,
by this much* — is deliberately two instruments rather than one that would have to pretend to be both.
