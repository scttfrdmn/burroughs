// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// #982's pair, as guest instructions on two agents over one shared memory. Agent A stores and loads an i32 at
// 0x931, which is unaligned; agent B loads and stores the byte at 0x930, which is aligned. The guest bytes are
// disjoint, so the guest model has no race between them.
const (
	unalignedPairASrc = `(module
  (memory (export "mem") 1 1 shared)
  (func (export "una") (param $n i32) (result i32) (local $i i32)
    (loop $l
      (i32.store (i32.const 0x931) (local.get $i))
      (drop (i32.load (i32.const 0x931)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $l (i32.lt_u (local.get $i) (local.get $n))))
    (local.get $i)))`
	unalignedPairBSrc = `(module
  (import "m" "mem" (memory 1 1 shared))
  (func (export "nb") (param $n i32) (result i32) (local $i i32)
    (loop $l
      (drop (i32.load8_u (i32.const 0x930)))
      (i32.store8 (i32.const 0x930) (local.get $i))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $l (i32.lt_u (local.get $i) (local.get $n))))
    (local.get $i)))`
)

// TestAnUnalignedAccessDoesNotRaceANeighbouringByte is #982's witness ([ADR 0111][0111]).
//
// **Its verdict is the race detector's**, so it asks its question under `-race` (`make race`, and CI's race
// job). There the old unaligned arms, a plain byte loop in `writeNum` and `loadValue` in `memAccess`, raced the
// atomic of the word containing B's byte. Without `-race` it still asserts that each agent's last value
// landed in its own bytes, which is the containing-word CAS's neighbour guarantee: an unaligned store that
// rewrote 0x930 would lose B's byte.
//
// **The memory is forced onto the Go heap**, because a reserved mapping is outside the race detector's view
// and the report could not happen there. Windows has no reservation at all, which is why #982 was seen only
// there. The premise is asserted, not assumed: one of the two heap-arm counters must move (a one-page memory
// is declined a reservation outright, and `withoutReservation` makes any larger one unavailable).
//
// **Neuters, run** (2026-10-28, darwin/arm64, under `-race`), each watched failing:
//   - `writeNum`'s unaligned arm restored to the byte loop: the detector reports at `writeNum`;
//   - `memAccess`'s unaligned arm restored to `loadValue`: the detector reports at `loadValue`.
//
// [0111]: ../../docs/decisions/0111-an-unaligned-typed-access-touches-its-containing-words-atomically-because-a-host-race-between-disjoint-guest-bytes-is-not-the-guests.md
func TestAnUnalignedAccessDoesNotRaceANeighbouringByte(t *testing.T) {
	withoutReservation(t)
	heapArms := func() int64 { return int64(reservationUnavailable.Load() + reservationDeclined.Load()) }
	before := heapArms()
	a, b, mems := litmusAgentsUnder(t, binary.Features{Threads: true}, unalignedPairASrc, unalignedPairBSrc)
	if heapArms() == before {
		t.Fatal("the shared memory was reserved, not allocated on the Go heap, so the race detector cannot see " +
			"it and this case would pass without asking its question")
	}
	const n = 10000
	done := make(chan outcome, 1)
	go func() { done <- callOffGoroutine(b, "nb", I32(n)) }()
	gotA := callOffGoroutine(a, "una", I32(n)).get(t)
	gotB := (<-done).get(t)
	if gotA != n || gotB != n {
		t.Fatalf("the agents ran %d and %d iterations, want %d each", gotA, gotB, n)
	}
	bs := mems[0].view()
	if got, want := loadValue(bs[0x931:0x935], memop{width: 4}), uint64(n-1); got != want {
		t.Errorf("agent A's last unaligned store reads back %#x at 0x931, want %#x", got, want)
	}
	if got, want := bs[0x930], byte((n-1)&0xff); got != want {
		t.Errorf("agent B's last byte at 0x930 reads back %#x, want %#x: an unaligned store to 0x931 overwrote "+
			"its neighbour", got, want)
	}
}

// TestAnUnalignedSpanEndingInTheLastWordFormsNoPointerPastTheImage is the witness for the defect #1166's first
// Windows run found in ADR 0111's helpers: they advanced a word pointer past the last word they touched, so a
// span ending in the image's last word formed a pointer one past the allocation.
//
// **Its verdict is `checkptr`'s**, which `-race` enables. `checkptr` rejects a pointer-arithmetic result only
// when it lands in a *different live* heap object, so a pointer one past an allocation is caught only when
// something is allocated right after it. Windows CI happened to have a neighbour after the image; a darwin run
// usually does not. So this case does not use an image: it supplies the neighbour itself. It allocates a run of
// 32-byte buffers, takes one whose successor is adjacent (asserted, as the premise), and runs every span shape
// against that buffer's end. A pointer formed past it is in the neighbour, and the runtime aborts the binary.
// Without `-race` the case still asserts the spans' byte arithmetic at the edge.
//
// **Neuters, run** (2026-10-28, darwin/arm64, `-race`): with either helper's trailing `unsafe.Add(w, 4)`
// restored, the binary aborts at the first row with *"pointer arithmetic result points to invalid
// allocation"*, in `atomicStoreSpan` or `atomicLoadSpan` respectively. Before the neighbour was supplied, the
// load neuter passed on darwin: no live object followed the image there.
func TestAnUnalignedSpanEndingInTheLastWordFormsNoPointerPastTheImage(t *testing.T) {
	const size = 32
	bufs := make([][]byte, 64)
	for i := range bufs {
		bufs[i] = make([]byte, size)
	}
	var buf []byte
	for i := 0; i+1 < len(bufs); i++ {
		if uintptr(unsafe.Pointer(&bufs[i][0]))+size == uintptr(unsafe.Pointer(&bufs[i+1][0])) {
			buf = bufs[i]
			break
		}
	}
	if buf == nil {
		t.Fatal("no two of 64 consecutive 32-byte allocations are adjacent, so no span here ends next to a live " +
			"object and checkptr has nothing to catch")
	}
	for _, c := range []struct{ ea, width uint64 }{
		{size - 3, 2},  // inside the last word, one word touched
		{size - 6, 4},  // straddles into the last word
		{size - 12, 8}, // 4-aligned but not 8: two whole words, the second the last
		{size - 9, 8},  // three words, the third the last
	} {
		v := uint64(0x0102030405060708) & (1<<(8*c.width) - 1)
		if c.width == 8 {
			v = 0x0102030405060708
		}
		bs := buf[c.ea : c.ea+c.width]
		if wordAligned(bs, c.width) {
			t.Fatalf("width %d at %d is aligned, so this row does not reach the span helpers", c.width, c.ea)
		}
		atomicStoreSpan(bs, v)
		if got := atomicLoadSpan(bs); got != v {
			t.Errorf("width %d at %d reads back %#x, want %#x", c.width, c.ea, got, v)
		}
	}
}
