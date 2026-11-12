// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build (darwin || linux) && !ios && !android && (amd64 || arm64 || ppc64 || ppc64le || riscv64 || s390x || mips64 || mips64le || loong64) && !burroughs_noreserve

package interp

import (
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// # [Decision 0113][0113]: where a reservation lands
//
// Under `-race` the Go runtime keeps its heap in [0x00c0_0000_0000, 0x00e0_0000_0000) and has no fallback when
// that band is taken (`runtime/malloc.go:mallocinit`, `(*mheap).sysAlloc`). darwin placed decision 0076's
// reservations upward from 0x70_0000_0000, into the band, and that was the darwin `-race` fatal (#1218). So a
// reservation is mapped at or above reservationBase instead, each at the end of the one before.
//
// **A hint, not `MAP_FIXED`.** `MAP_FIXED` would silently replace whatever is already mapped at the address.
// A kernel that ignores the hint returns a mapping somewhere else, and that mapping is kept, because it is no
// worse than decision 0076's placement. It is counted in reservationBelowBase, so a control can say so.
//
// **Why the raw syscall.** `syscall.Mmap` takes no address. And `syscall.Munmap` refuses a mapping it did not
// make, because it looks the slice up in its own table, so the release is raw too. Both are six-argument
// `mmap`/`munmap` on exactly the ports this file's constraint names, and on no others (decision 0113's scope).
//
// [0113]: ../../docs/decisions/0113-a-reservation-is-mapped-at-a-high-base-address-because-under-race-the-heap-must-live-in-a-fixed-band-that-a-kernel-placed-mapping-can-occupy.md
const hintedPlacement = true

// reservationBase is 16 TiB. It sits above the race heap's band, and above where a normal heap grows, which is
// from 256 GiB on arm64 and from 768 GiB on amd64, in 1 TiB strides.
const reservationBase uintptr = 1 << 44

var (
	placeMu   sync.Mutex
	placeNext = reservationBase

	// reservationOffHint counts mappings the kernel placed anywhere but the hint. It is a total, read by the
	// placement control: zero on a kernel that honours hints, which darwin and linux were measured to do (decision
	// 0113). It was `reservationBelowBase` and counted only placements below the cursor (grave #1254).
	reservationOffHint atomic.Uint64

	// mmapAt is the hinted anonymous mmap: the syscall, and a seam so a test can stand in for a kernel that
	// ignores a hint, which no kernel this was measured on does on demand.
	mmapAt = func(hint, n uintptr) (uintptr, syscall.Errno) {
		addr, _, errno := syscall.Syscall6(syscall.SYS_MMAP, hint, n,
			syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE, ^uintptr(0), 0)
		return addr, errno
	}
)

// mapPlaced maps n bytes at the next free address at or above reservationBase.
//
// **Released ranges are not reused.** placeNext only moves up. That is decision 0113's measured behaviour, and
// over a long-lived process it walks through address space nothing reclaims, which at 16 TiB of headroom per
// terabyte of reservations is the cost it states.
func mapPlaced(n int) ([]byte, bool) {
	placeMu.Lock()
	defer placeMu.Unlock()
	addr, errno := mmapAt(placeNext, uintptr(n))
	if errno != 0 {
		return nil, false
	}
	// **Only an honoured hint advances the cursor** (grave #1254). This was `addr < placeNext` counted as a
	// stray and anything else taken as the new cursor, so a mapping the kernel placed elsewhere but *above* the
	// cursor moved it there. Linux's own placement is top-down from about 0x7f…, so one ignored hint sent the
	// cursor to the top of the address space and every later reservation off-hint. A stray is counted and kept,
	// as decision 0113 says, and the cursor stays where the next hint should go.
	//
	// NEUTER: restore the `addr < placeNext` test, and TestOneIgnoredHintDoesNotMoveTheCursor fails.
	if addr == placeNext {
		placeNext = addr + uintptr(n)
	} else {
		reservationOffHint.Add(1)
	}
	// The address is read through a pointer to it rather than converted from a uintptr, which is the form
	// `go vet` accepts for memory the kernel returned and the runtime never moves (x/sys's mmap does the same).
	// It is not a Go object, so no GC invariant is in play.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	return unsafe.Slice((*byte)(p), n), true
}

// unmapPlaced releases a mapping mapPlaced made. bs must be at its full length, for the reason
// unmapReservation states.
func unmapPlaced(bs []byte) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_MUNMAP, uintptr(unsafe.Pointer(&bs[0])), uintptr(len(bs)), 0); errno != 0 {
		return errno
	}
	return nil
}
