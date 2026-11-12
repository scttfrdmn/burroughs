// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build (darwin || linux) && !ios && !android && (amd64 || arm64 || ppc64 || ppc64le || riscv64 || s390x || mips64 || mips64le || loong64) && !burroughs_noreserve

package interp

import (
	"syscall"
	"testing"
	"unsafe"
)

// TestAReservationIsPlacedAtOrAboveTheBase is decision 0113's placement control. Three 4 GiB reservations land at
// or above reservationBase, each at the end of the one before, and none is counted below the base.
//
// **What it guards:** a refactor back to `syscall.Mmap` would put reservations wherever the kernel chooses, which
// on darwin is upward from 0x70_0000_0000 and into the race heap's band. Nothing else would notice until a rare
// `-race` fatal came back. This fails at once instead.
func TestAReservationIsPlacedAtOrAboveTheBase(t *testing.T) {
	const n = 4 << 30
	below := reservationOffHint.Load()
	var held [][]byte
	t.Cleanup(func() {
		for _, bs := range held {
			if err := unmapReservation(bs); err != nil {
				t.Errorf("releasing a placed reservation: %v", err)
			}
		}
	})
	var prev uintptr
	for i := range 3 {
		bs, ok := mapReservation(n)
		if !ok {
			t.Fatalf("reservation %d of %d bytes was refused", i, n)
		}
		held = append(held, bs)
		base := uintptr(unsafe.Pointer(&bs[0]))
		if base < reservationBase {
			t.Fatalf("reservation %d landed at %#x, below the base %#x: decision 0113's placement is not in effect",
				i, base, reservationBase)
		}
		if i > 0 && base != prev+n {
			t.Errorf("reservation %d landed at %#x, not at the end of the one before (%#x)", i, base, prev+n)
		}
		prev = base
	}
	if got := reservationOffHint.Load() - below; got != 0 {
		t.Errorf("%d reservation(s) counted off-hint on a kernel measured to honour the hint", got)
	}
}

// TestOneIgnoredHintDoesNotMoveTheCursor is grave #1254's witness. A kernel that ignores one hint and places the
// mapping high, as Linux's top-down default does, must not move the cursor: the next reservation still asks for,
// and gets, the address the cursor named before the stray.
//
// The stray is stood in by the `mmapAt` seam, because no kernel this was measured on ignores a hint on demand.
// It must land **above** the cursor, as Linux's top-down placement did on CI's x86 runner: a stray below the
// cursor was already counted, and only one above it moved the cursor. So the stand-in maps for real 32 TiB above
// the hint it was given, which is a real mapping released like any other.
func TestOneIgnoredHintDoesNotMoveTheCursor(t *testing.T) {
	const n = 4 << 30
	kernel := mmapAt
	t.Cleanup(func() { mmapAt = kernel })

	var held [][]byte
	t.Cleanup(func() {
		for _, bs := range held {
			if err := unmapReservation(bs); err != nil {
				t.Errorf("releasing a reservation: %v", err)
			}
		}
	})

	placeMu.Lock()
	want := placeNext
	placeMu.Unlock()
	strays := reservationOffHint.Load()

	ignored := false
	mmapAt = func(hint, size uintptr) (uintptr, syscall.Errno) {
		if !ignored {
			ignored = true
			return kernel(hint+1<<45, size) // placed elsewhere, above the cursor, not at the hint
		}
		return kernel(hint, size)
	}

	stray, ok := mapReservation(n)
	if !ok {
		t.Fatal("the stray reservation was refused")
	}
	held = append(held, stray)
	if got := reservationOffHint.Load() - strays; got != 1 {
		t.Fatalf("the ignored hint counted %d stray(s), want 1", got)
	}

	next, ok := mapReservation(n)
	if !ok {
		t.Fatal("the reservation after the stray was refused")
	}
	held = append(held, next)
	if got := uintptr(unsafe.Pointer(&next[0])); got != want {
		t.Errorf("after one ignored hint the next reservation landed at %#x, not at the cursor's hint %#x: the "+
			"stray moved the cursor (grave #1254)", got, want)
	}
}
