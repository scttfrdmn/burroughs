// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build unix

package interp

import (
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// # Why this file carries a build tag instead of three skips
//
// The three tests here assert decision 0076's mapping path, which does not exist on `windows`, `plan9` or
// the wasm ports — so on those ports there is nothing to assert and **nothing to skip either**. A
// `t.Skip` would have been the wrong shape twice over: it needs a license in
// `internal/testenv/inventory_test.go`, and *a skip is not a verdict* — a port where the mechanism is
// absent should have no test, not a green earned by declining to ask.
//
// **On a `unix` port a refused mapping is a `t.Fatalf` and not a skip**, which is the same rule read the
// other way. A host that cannot reserve address space is exactly the host where M-1 silently degrades to
// O(size) growth, so it is the last case that should be allowed to report a pass. If this fires in CI the
// finding is real — an address-space rlimit, a strict overcommit policy, or a suite that has reserved more
// than the address space holds — and it is 0076's arm C rollback that says what to do about it.

// requireReservation fails the test if this host cannot serve a mapping — see the file comment for why
// that is a failure rather than a skip. It releases the probe's own mapping rather than leaking one per
// call.
func requireReservation(t *testing.T) {
	t.Helper()

	bs, ok := mapReservation(pageSize)
	if !ok {
		t.Fatalf("this host refused a one-page anonymous mapping, so decision 0076's mechanism "+
			"cannot be asserted here. That is a finding rather than a reason to skip: every "+
			"memory on this host takes the allocator's fallback and this engine does not meet "+
			"contract \u00a78 M-1 (reservations refused so far: %d)",
			reservationUnavailable.Load())
	}
	_ = unmapReservation(bs)
}

// TestAMemoryReservesAddressSpaceRatherThanCommittingIt is M-1's own assertion at the seam it is decided
// at: the memory's capacity is the reservation `reservationFor` computed, and the reservation came from a
// mapping rather than from the allocator.
//
// **Read from the capacity and from the counter, not from the absence of an error.** 0076 registered that
// guard in advance, because a run whose mapping silently fell back to `make` would still produce a working
// memory of the right length — the fallback *is* a correct path — and would assert nothing at all about M-1.
func TestAMemoryReservesAddressSpaceRatherThanCommittingIt(t *testing.T) {
	requireReservation(t)

	cases := []struct {
		name string
		lim  binary.Limits
		want uint64 // reserved pages
	}{
		{"a declared max is the reservation", binary.Limits{Min: 1, Max: 4096, HasMax: true}, 4096},
		{"shared is not a condition of it", binary.Limits{Min: 1, Max: 4096, HasMax: true, Shared: true}, 4096},
		{"no declared max reserves the i32 ceiling", binary.Limits{Min: 1}, maxPages32},
		{"a max above the ceiling is clamped to it", binary.Limits{Min: 1, Max: 1 << 20, HasMax: true}, maxPages32},
		{"a zero-page memory still reserves", binary.Limits{}, maxPages32},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := reservationUnavailable.Load()
			mem, err := newMemory(binary.Memory{Limits: c.lim})
			if err != nil {
				t.Fatalf("newMemory: %v", err)
			}
			if got := reservationUnavailable.Load() - before; got != 0 {
				t.Fatalf("reservationUnavailable moved by %d, so this memory took the "+
					"allocator's path and nothing below is about M-1", got)
			}
			img := mem.img.Load().bytes
			if got := uint64(cap(img)); got != c.want*pageSize {
				t.Errorf("capacity %d bytes (%d pages), want %d bytes (%d pages)",
					cap(img), got/pageSize, c.want*pageSize, c.want)
			}
			if got := uint64(len(img)); got != c.lim.Min*pageSize {
				t.Errorf("length %d bytes, want the declared minimum's %d: the reservation "+
					"is capacity, and a guest that can address it has been given memory "+
					"the module did not ask for", got, c.lim.Min*pageSize)
			}
			// **Aligned to the *OS* page, which is a stronger premise than the one ADR
			// 0051's atomics rest on and a weaker one than this first asserted.**
			// `sync/atomic`'s bug note promises an allocated slice's first word is 64-bit
			// aligned; a mapping's base is OS-page aligned by the kernel's own contract.
			// The first draft compared against `pageSize` — wasm's 64 KiB — and failed on
			// darwin/arm64 at an offset of 16384, because the OS page there is 16 KiB and
			// `mmap` aligns to the OS page and to nothing larger. *A literal duplicating a
			// property is correct once*: the number has to come from the platform.
			//
			// Asserted rather than assumed because `checkBaseAlignment` only checks the
			// 8-byte property, so nothing else here would notice a primitive that handed
			// back an offset window into a larger mapping.
			if len(img) > 0 {
				osPage := uintptr(os.Getpagesize())
				if p := uintptr(unsafe.Pointer(&img[0])); p%osPage != 0 {
					t.Errorf("the mapped base %#x is not aligned to this platform's "+
						"%d-byte page (off by %d)", p, osPage, p%osPage)
				}
			}
			// **Marked, and charged nothing — which is the half of decision 0056's option (A)
			// that 0076 answers.** 0056 rejected marking every memory because a marked memory
			// cannot grow past its reservation and the reservation was a 128-page cap. Here the
			// reservation is the declared max or the address type's ceiling, so the mark excludes
			// no program the declaration did not already exclude:
			// `TestTheEngineLimitRefusalIsDistinguishableFromEveryOtherRefusal` holds the
			// complement, on the path where the cap is still real.
			if !mem.noMove {
				t.Errorf("a reservation-backed memory is not marked no-move. The mapping is " +
					"unmapped by `releaseMapping` at this base, so a `grow` that " +
					"replaced the array would abandon it — and ADR 0073's refusal, which " +
					"is what keeps that from happening, is gated on the mark")
			}
		})
	}
}

// TestAMappedMemoryGrowsWithoutMovingItsArray is contract §8 M-1's sentence read as a property rather than
// as a benchmark: *"no full-copy growth path"*. The queued ladder measures the cost; this asserts the thing
// the cost follows from, which is that the array the guest was using is the array it keeps.
//
// The two properties are separate assertions on purpose. A `grow` could keep the base and still hand back a
// window over uninitialised bytes, and a guest reading a nonzero byte out of a freshly grown page is a
// conformance defect any module can see.
func TestAMappedMemoryGrowsWithoutMovingItsArray(t *testing.T) {
	requireReservation(t)

	mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 1, Max: 64, HasMax: true}})
	if err != nil {
		t.Fatalf("newMemory: %v", err)
	}
	img := mem.img.Load().bytes
	if cap(img) != 64*pageSize {
		t.Fatalf("capacity %d, want the declared max's %d: this test asserts nothing about "+
			"reservation-backed growth if the memory was not reservation-backed", cap(img), 64*pageSize)
	}
	was := &img[0]
	mem.view()[0] = 0x5a

	for want := uint64(2); want <= 64; want++ {
		if got := mem.grow(1, nil); got != int64(want)-1 {
			t.Fatalf("grow to %d pages returned %d, want the previous size %d",
				want, got, want-1)
		}
		now := mem.img.Load().bytes
		if &now[0] != was {
			t.Fatalf("growing to %d pages moved the array from %p to %p.\nThat is the "+
				"full-copy growth path §8 M-1 forbids: the reservation was %d bytes and "+
				"the new length is %d, so nothing about this growth needed a new array",
				want, was, &now[0], cap(img), len(now))
		}
		if now[0] != 0x5a {
			t.Fatalf("the byte written before the grow reads %#x at %d pages, want 0x5a",
				now[0], want)
		}
		for i, b := range now[(want-1)*pageSize:] {
			if b != 0 {
				t.Fatalf("byte %d of the page grown into reads %#x, want 0.\nA fresh "+
					"anonymous page is zero because the kernel hands out a zero page; "+
					"if this fires, the mapping is being reused rather than reserved",
					int((want-1)*pageSize)+i, b)
			}
		}
	}
}

// TestAReservationIsReleasedWhenTheMemoryBecomesUnreachable is the lifetime, and it is the assertion 0076's
// `runtime.AddCleanup` rests on: a mapping is not garbage, so nothing in this process reclaims it unless
// the cleanup runs.
//
// **A bounded wait and a FAIL, never a skip.** `AddCleanup` gives no promise about *when*, so the test
// drives the collector and waits for the counter; a leaked mapping is address space this engine never gets
// back, and *a skip is not a verdict* — a version of this test that gave up quietly would report a green
// for a runtime that had stopped running cleanups at all.
func TestAReservationIsReleasedWhenTheMemoryBecomesUnreachable(t *testing.T) {
	requireReservation(t)

	before := reservationReleased.Load()
	failed := reservationReleaseFailed.Load()

	func() {
		mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 1, Max: 64, HasMax: true}})
		if err != nil {
			t.Fatalf("newMemory: %v", err)
		}
		mem.view()[0] = 1 // touch it, so the mapping is committed and not merely reserved
	}()

	deadline := time.Now().Add(10 * time.Second)
	for reservationReleased.Load() == before {
		if time.Now().After(deadline) {
			t.Fatalf("no mapping was released in 10s after the only reference to the memory "+
				"went out of scope (released %d, release failures %d).\n"+
				"A mapping is not garbage: if the cleanup does not run, this engine leaks "+
				"address space for the life of the host process",
				reservationReleased.Load()-before, reservationReleaseFailed.Load()-failed)
		}
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if got := reservationReleaseFailed.Load() - failed; got != 0 {
		t.Errorf("%d releases failed, want 0: an `Munmap` error means the slice handed to the "+
			"kernel was not the one that was mapped", got)
	}
}
