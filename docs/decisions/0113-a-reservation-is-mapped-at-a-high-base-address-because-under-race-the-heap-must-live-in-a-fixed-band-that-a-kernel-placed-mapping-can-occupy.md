# 0113 — A reservation is mapped at a high base address, because under `-race` the heap must live in a fixed band that a kernel-placed mapping can occupy

Date: 2026-11-09 · Status: **proposed**. It changes the mechanism of [ADR 0076](0076-a-memory-reserves-address-space-through-an-anonymous-mapping-and-the-go-allocator-becomes-the-fallback-rather-than-the-mechanism.md), which is Scott's stamp, so it is held open for his. Drafted at Scott's request on 2026-11-09 (*"draft the ADR for that fix"*), after [#1218](https://github.com/scttfrdmn/burroughs/issues/1218)'s option A (narrow the cause within ADR 0076) found the cause. Filed against [#1218](https://github.com/scttfrdmn/burroughs/issues/1218).

## Context

**The defect.** Under `-race` on darwin/arm64, `internal/spec`'s `TestThreadsGateLeavesTheCoreBoardUnchanged` dies with `runtime: split stack overflow`. It happens in about 1 in 1,550 runs, and more often on a loaded host (#1089, #1218). With `burroughs_noreserve`, where every memory is a Go allocation, it read 0 in 10,000. So ADR 0076's mappings are its cause. Every shipped build is unaffected: the fatal needs `-race`.

**The mechanism, from the Go runtime's own source** (Go 1.26.9):
- In race mode, `runtime/malloc.go:mallocinit` gives the heap arena hints only in **[0x00c0_0000_0000, 0x00e0_0000_0000)**, a 128 GiB band at 768 GiB. Its comment reads *"The TSAN runtime requires the heap to be in the range [0x00c000000000, 0x00e000000000)."*
- When those hints are used up, `(*mheap).sysAlloc` throws *"too many address space collisions for -race mode"*. Outside race mode the same function instead *"take[s] any (sufficiently aligned) address the kernel will give us"*.
- ADR 0076 reserves up to 4 GiB per memory, placed by the kernel. **Measured on 2026-11-09** with a probe making four unhinted 4 GiB anonymous mappings:
  - **darwin/arm64** placed them at 0x70_0000_0000, 0x71…, 0x72…, 0x73…: from 448 GiB upward, 4 GiB at a time. So about 80 of them live at once reach the band at 768 GiB, and a heap or stack growth in that window has nowhere to go.
  - **linux/arm64** (castor) placed them top-down from 0xf2ef_bd40_0000, about 243 TiB, far above the band. That is consistent with the fatal never being seen on linux under `-race`.
- Non-race builds take the fallback, which is why no shipped build fails.

**The evidence that this is the cause** (#1218, darwin/arm64, `-race`, Go 1.26.9):

| Arm | Result |
|---|---|
| never unmap (`burroughs_nounmap`), 4 GiB reservations | *"too many address space collisions"*, every run, ~3 s |
| never unmap, reservations capped at 2 GiB | **`runtime: split stack overflow`**, the rare fatal's own signature, on every run, ~2 s |
| never unmap, capped at 256 MiB or 64 MiB | pass |
| never unmap, 4 GiB reservations **mapped at a hint of 2 TiB or 16 TiB** | **pass**, every run |
| ordinary runs, reservations capped at 64 MiB (`burroughs_reservecap`) | 0 / 6,195, against `default`'s 4 / 6,191 |

Holding the mappings reproduces the rare fatal deterministically. Placing the same 4 GiB reservations, never released, above the band removes it. **The trigger is where the reservations land, not how big they are.**

## Options

1. **Map every reservation at a high base address, on every 64-bit unix build.** A hint at 16 TiB, with each mapping placed after the previous one.
   - *Keeps:* contract §8 M-1 in full: reserved up front, no cap, no copying growth. One mechanism in every build, so `-race` lanes keep testing what ships.
   - *Costs:* `syscall.Mmap` takes no address, so the call becomes a raw `syscall.Syscall6(SYS_MMAP, …)`, with a matching raw `munmap`, since `syscall.Munmap` refuses a mapping it did not make. That is the engine's second and third syscalls.
2. **The same, under `-race` only.**
   - *Keeps:* M-1 in full.
   - *Costs:* `-race` builds and shipped builds place memory differently. The `-race` lanes then test a mechanism that does not ship, and a placement defect in either could hide from the other. This is half of what was wrong with #1218's option B.
3. **Cap the reservation under `-race`.**
   - *Costs:* growth past the cap relocates, which is a copying growth path, so `-race` builds become non-conformant to M-1 (#671's ruling). It also only lowers the odds rather than removing the cause.
4. **The Go allocator under `-race`** (#1218's original option B). Withdrawn for M-1 non-conformance.

## Choice

**Option 1.** It removes the cause, not the odds. It keeps M-1 in every build, and it keeps the `-race` lanes on the shipped mechanism.

- **Where:** `internal/interp/reserve_unix.go:mapReservation` maps at the next free address at or above a base of `0x1000_0000_0000` (16 TiB), and `unmapReservation` releases with a raw `munmap`.
  - 16 TiB sits above the race band and above where a normal arm64 heap grows. Normal-mode hints start at 256 GiB, rising in 1 TiB strides as the heap needs them. On amd64 they start at 768 GiB.
  - A process would need terabytes of Go heap to reach 16 TiB.
- **A hint, not `MAP_FIXED`.** The same probe found the 16 TiB hint honoured exactly on both kernels, four mappings in a row. Still, the kernel may place a mapping elsewhere, and an ignored hint must not fail the memory. A mapping that lands below the base is counted (`reservationBelowBase`) and kept: it is no worse than today's placement. `MAP_FIXED` would silently replace whatever was already mapped at the address.
- **Contiguous above the base.** Each reservation's hint is the end of the previous one, under a mutex. Released ranges are not reused, which is the arm's measured behaviour. Over a long-lived process that walks upward through address space that is never reclaimed. Reuse of the lowest released range is a refinement for when that is measured to matter.
- **32-bit ports:** unchanged. The caller's `math.MaxInt` bound already keeps them from mapping.

## Consequences

- **Witness, deterministic and fast:** a `-race`-only test in `internal/interp`. In a child process, it makes and holds enough reservations to cover the race band, then grows the heap, and it asserts the child exits 0.
  - **Its neuter, run:** with the base removed (kernel placement restored), the child must die with *"too many address space collisions for -race mode"*. On darwin that was every run, in seconds.
  - On linux, kernel placement does not reach the band, so the neuter is not expected to fire there. The witness says so instead of skipping.
- **Acceptance, pre-registered:** the rare form in ordinary runs, on the host that reproduces it most.
  - Run `default` against the fix, side by side, on indigo, about 3,300 iterations per arm, so `default` is expected to fail about 5 times.
  - **The forecast:** the fix reads 0, and `default` reads at least 3. If `default` reads fewer than 3, the run is inconclusive and is extended, not read as a pass.
- **A control** asserts that `mapReservation` returns addresses at or above the base when the kernel honours hints (darwin and linux), so a refactor back to `syscall.Mmap` fails loudly rather than reopening the window.
- **ADR 0076 is amended, not reversed.** Its mechanism, its size rule, its lifetime (`runtime.AddCleanup`) and its fallback all stand. Only where the mapping lands changes, and 0076 gets a postscript pointing here.
- **The test-only arms** (`burroughs_nounmap`, `burroughs_reservecap`, `burroughs_hintmap`) stay off main. They were #1218's instruments, and this decision replaces them. `noreserve`, `plainspan` and `noedges` stay, for #1089.
- **No contract text changes.** M-1 is met as before, and the non-conformant port list is unchanged.

## Rollback

Reverting the change to `reserve_unix.go` restores kernel placement exactly. Nothing else depends on the base address. The witness's neuter is that revert, run.
