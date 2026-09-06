// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"sync/atomic"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// # [Decision 0076][0076]'s reservation, and why the primitive is a seam
//
// Contract §8 M-1 wants `memory.grow` to be *"amortized O(pages touched): address space reserved up
// front, commit on grow, no full-copy growth path."* The Go allocator cannot serve that sentence, and the
// reason is measured rather than aesthetic: `make([]byte, n, reserve)` **commits** what it reserves and
// clears a span the allocator has recycled, which is what turned ADR 0051's 1 ms forecast into a
// **855 ms** worst case at 4 GiB and left `sharedReservePages` capped at 128. An anonymous mapping is the
// primitive M-1 describes — the reservation is address space, a page becomes real when it is first stored
// to, and a fresh anonymous page is zero because the kernel hands out a zero page rather than because
// anything memset it.
//
// **`reserveMapping` is a var rather than a direct call, and the reason is the fallback's oracle.** Rule
// 3 of 0076 keeps `make`-and-copy as a real path for the ports with no primitive and for a mapping the
// kernel refuses at run time, and a path nothing can reach is a path nothing tests. On `unix` the
// mapping always succeeds for a size the address space can hold, so *the only way to exercise the
// fallback on the hosts this project runs on is to make the primitive say no* — which is what this seam
// is for. It is not a configuration knob: nothing outside this package can reach it.
//
// [0076]: ../../docs/decisions/0076-a-memory-reserves-address-space-through-an-anonymous-mapping-and-the-go-allocator-becomes-the-fallback-rather-than-the-mechanism.md
var reserveMapping = mapReservation

// reservationFor is decision 0076's *reserve to what* rule, in bytes, for a memory whose minimum is
// already `n` bytes. Zero means "do not map this one" and sends the caller to the fallback.
//
// **One rule, and it does not branch on the address type**, because the cases where i32 and i64 differ
// are exactly the cases where a branch would be guessing:
//
//  1. A **declared max** is the reservation — the module's own number, which is
//     [ADR 0075][0075]'s rule for tables read across.
//  2. **No declared max** reserves `maxPages32`, for both address types. For an i32 memory that is the
//     address type's own ceiling and no engine limit at all. For a memory64 — whose ceiling is nothing,
//     since `internal/interp/memory.go:validSize` returns true unconditionally on that arm, following
//     the reference — it is a **named engine limit** just under 4 GiB, counted by
//     `internal/interp/memory.go:growthRefusedPastReservation`. Borrowing the i32 ceiling rather than
//     inventing a number is the narrowest voice available where the engine has to speak in its own.
//  3. **No room to grow into is not a reservation.** When the ceiling above is at or below the memory's
//     own minimum this returns zero, and the memory keeps the allocator's path — which for a memory64
//     declaring a minimum over 4 GiB means it keeps today's relocate-and-copy `grow` rather than gaining
//     a refusal. That arm exists so this decision cannot *narrow* which programs run: mapping a
//     reservation with no headroom would set `noMove` on a memory whose every growth would then be
//     refused instead of copied.
//
// A max at or below the minimum lands in rule 3 too, and correctly: `grow` already refuses past
// `limits.Max` one check earlier, so there is nothing for a reservation to buy.
//
// [0075]: ../../docs/decisions/0075-a-table-reserves-to-its-declared-max-under-a-measured-ceiling-and-refuses-to-relocate-with-a-sibling-agent.md
func reservationFor(lim binary.Limits, n uint64) uint64 {
	pages := uint64(maxPages32)
	if lim.HasMax && lim.Max < pages {
		pages = lim.Max
	}
	reserve := pages * pageSize
	if reserve <= n {
		return 0
	}
	return reserve
}

// releaseMapping unmaps a reservation and counts what happened, which is the whole reason it wraps
// `unmapReservation` instead of being called directly.
//
// **It runs from a `runtime.AddCleanup` callback, on a goroutine with nobody to report to.** There is no
// caller to return an error to and no guest to trap: the `*memory` is already unreachable by the time
// this runs. So the two outcomes go to counters, which is the only channel a cleanup has — and having
// *two* of them is the point, because "released 400 mappings" and "failed to release 400 mappings" are
// the same number in a channel that only counts successes.
func releaseMapping(bs []byte) {
	if err := unmapReservation(bs); err != nil {
		reservationReleaseFailed.Add(1)
		return
	}
	reservationReleased.Add(1)
}

// reservationUnavailable counts a memory that **asked** for a mapping and did not get one, and therefore
// took the Go allocator's path with everything that implies — a capped reservation, a reachable `publish`,
// and the two engine limits that exist because of it.
//
// **It is not rule 3, and an earlier draft of this comment said it was.** Rule 3 returns zero *before*
// anything is asked, so a no-headroom memory never touches this counter; `reservationDeclined` is that
// population. The distinction is the file's own two-questions-two-counters rule read once more: "the host
// refused" and "we never asked" have different repairs, and one number over both can name neither.
//
// **The counter is what makes the fallback observable, and M-1 is unfalsifiable without it.** An engine
// that silently degrades to O(size) growth looks exactly like one that reserved successfully, from
// outside. This is incremented on the `!unix` ports, where it will be every memory, and on a `unix` port
// whenever the kernel refuses — an address-space rlimit, strict overcommit, or a process that has already
// reserved more than the address space holds. That last case is why this is read off the **suite** as a
// board figure rather than trusted to be zero: several hundred live memories each reserving just under
// 4 GiB is a claim about the host's address space, not about this code.
var reservationUnavailable atomic.Uint64

// reservationReleased counts mappings unmapped by the cleanup. Its only job is to be an oracle for the
// lifetime: a mapping is not garbage, so nothing else in this process can say whether reachability and
// unmapping actually coincide.
var reservationReleased atomic.Uint64

// reservationDeclined counts `reservationFor`'s rule 3: a memory the engine did not ask for a mapping for,
// because the ceiling the rule would reserve to is at or below the memory's own minimum.
//
// **Most of this population is uninteresting and one part of it is not.** A max at or below the minimum
// cannot grow at all, so a reservation would buy nothing and its absence costs nothing. A memory64
// declaring a minimum over 4 GiB is the other part: it keeps today's relocate-and-copy `grow`, which is
// contract §8 M-1 unmet, and rule 3 is deliberately the arm that keeps such a module *running* rather than
// refusing it. That is a trade this decision made knowingly, and this counter is what keeps it from being
// made silently — without it the only observable difference between "reserved" and "declined to reserve"
// is the growth cost, which is exactly what M-1 is about.
var reservationDeclined atomic.Uint64

// reservationReleaseFailed counts an `Munmap` that returned an error, which would mean the engine is
// leaking address space and — worse — that the slice it handed `Munmap` was not the one it mapped.
// Separate from `reservationReleased` for `growthRefusedPastReservation`'s reason: two questions, two
// counters, because one counter over both cannot say which happened.
var reservationReleaseFailed atomic.Uint64
