// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build (darwin || linux) && !ios && !android && (amd64 || arm64 || ppc64 || ppc64le || riscv64 || s390x || mips64 || mips64le || loong64) && !burroughs_noreserve

package interp

import (
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
	below := reservationBelowBase.Load()
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
	if got := reservationBelowBase.Load() - below; got != 0 {
		t.Errorf("%d reservation(s) counted below the base on a kernel measured to honour the hint", got)
	}
}
