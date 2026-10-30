// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// #1167's pair: #982's, with agent A's side moved from a typed access to the bulk and SIMD region. Every one of
// A's instructions touches byte 0x931 and never 0x930. Agent B loads and stores the aligned byte at 0x930, which
// is an atomic of the word [0x930, 0x934). The guest bytes are disjoint, so the guest model has no race between
// them. A's instructions:
//   - memory.fill, memory.init and v128.store with 0x931 as their first byte, so its word is a partial head edge;
//   - v128.store8_lane at 0x931, a one-byte extent, which is all edge;
//   - memory.copy into 0x931 (a destination edge) and out of 0x931 (a source edge);
//   - v128.load and v128.load8_lane at 0x931, the read side of the same edge.
const (
	edgePairASrc = `(module
  (memory (export "mem") 1 1 shared)
  (data $d "\01\02\03")
  (func (export "edges") (param $n i32) (result i32) (local $i i32)
    (loop $l
      (memory.fill (i32.const 0x931) (local.get $i) (i32.const 3))
      (memory.init $d (i32.const 0x931) (i32.const 0) (i32.const 3))
      (v128.store (i32.const 0x931) (v128.const i32x4 1 2 3 4))
      (v128.store8_lane 0 (i32.const 0x931) (v128.const i32x4 5 6 7 8))
      (memory.copy (i32.const 0x931) (i32.const 0x100) (i32.const 3))
      (memory.copy (i32.const 0x200) (i32.const 0x931) (i32.const 3))
      (drop (v128.load (i32.const 0x931)))
      (drop (v128.load8_lane 0 (i32.const 0x931) (v128.const i32x4 0 0 0 0)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $l (i32.lt_u (local.get $i) (local.get $n))))
    (local.get $i)))`
	edgePairBSrc = `(module
  (import "m" "mem" (memory 1 1 shared))
  (func (export "nb") (param $n i32) (result i32) (local $i i32)
    (loop $l
      (drop (i32.load8_u (i32.const 0x930)))
      (i32.store8 (i32.const 0x930) (local.get $i))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $l (i32.lt_u (local.get $i) (local.get $n))))
    (local.get $i)))`
)

// TestABulkOrSIMDEdgeDoesNotRaceANeighbouringByte is #1167's witness ([ADR 0112][0112]).
//
// **Its verdict is the race detector's**, as #982's witness's is, so it asks its question under `-race` (`make
// race`, and CI's race job). There the region's old plain paths raced the atomic of the word containing B's
// byte: the fill's memset, `memory.init`'s and `memory.copy`'s `copy`, and the SIMD accesses through
// `memory.read` and `memory.write`. Without `-race` it asserts that B's last byte survived A's edges, which is
// the containing-word compare-and-swap's neighbour guarantee.
//
// **The memory is forced onto the Go heap**, for #982's witness's reason: a reserved mapping is outside the race
// detector's view. The premise is asserted, not assumed.
//
// **NEUTER, run** (2026-10-30, darwin/arm64, under `-race`): with `edgeSplit` returning (0, 0), every extent is
// all interior and plain again, which is the region before ADR 0112. The detector then made 17 reports. The plain
// side was `execMemoryFill`, `execMemoryInit`, `execMemoryCopy`, `memory.writeEdges` (the SIMD stores), and
// `v128FromBytes` and `replaceLaneBytes` (the SIMD loads, reading the image plainly). The other side each time
// was B's `atomicCell.load` or `update`. So every path the repair covers was reported.
//
// [0112]: ../../docs/decisions/0112-a-bulk-or-simd-extent-touches-its-partial-edge-words-atomically-and-its-whole-words-plainly-because-only-an-edge-word-can-be-shared-with-a-disjoint-neighbour.md
func TestABulkOrSIMDEdgeDoesNotRaceANeighbouringByte(t *testing.T) {
	withoutReservation(t)
	heapArms := func() int64 { return int64(reservationUnavailable.Load() + reservationDeclined.Load()) }
	before := heapArms()
	a, b, mems := litmusAgentsUnder(t, binary.DefaultFeatures(), edgePairASrc, edgePairBSrc)
	if heapArms() == before {
		t.Fatal("the shared memory was reserved, not allocated on the Go heap, so the race detector cannot see " +
			"it and this case would pass without asking its question")
	}
	const n = 5000
	done := make(chan outcome, 1)
	go func() { done <- callOffGoroutine(b, "nb", I32(n)) }()
	gotA := callOffGoroutine(a, "edges", I32(n)).get(t)
	gotB := (<-done).get(t)
	if gotA != n || gotB != n {
		t.Fatalf("the agents ran %d and %d iterations, want %d each", gotA, gotB, n)
	}
	if got, want := mems[0].view()[0x930], byte((n-1)&0xff); got != want {
		t.Errorf("agent B's last byte at 0x930 reads back %#x, want %#x: an edge write at 0x931 overwrote its "+
			"neighbour", got, want)
	}
}

// TestEdgeSplitMeasuresAnExtentsPartialWords pins edgeSplit's arithmetic, which every edge path rests on. It
// includes the shapes a copy's clamp depends on: an extent inside one word, one ending on a boundary, and one
// of whole words.
func TestEdgeSplitMeasuresAnExtentsPartialWords(t *testing.T) {
	for _, c := range []struct{ i, n, head, tail uint64 }{
		{0, 4, 0, 0},   // one whole word
		{0, 12, 0, 0},  // three whole words
		{1, 12, 3, 1},  // 3-byte head, two whole words, 1-byte tail
		{1, 3, 3, 0},   // ends on a boundary: all head
		{1, 2, 2, 0},   // inside one word: all head
		{0, 3, 0, 3},   // starts on a boundary and stops inside the word: all tail
		{2, 4, 2, 2},   // straddles one boundary
		{3, 1, 1, 0},   // the last byte of a word
		{4, 1, 0, 1},   // the first byte of a word
		{1, 16, 3, 1},  // a v128 at +1
		{16, 16, 0, 0}, // an aligned v128
	} {
		if head, tail := edgeSplit(c.i, c.n); head != c.head || tail != c.tail {
			t.Errorf("edgeSplit(%d, %d) = (%d, %d), want (%d, %d)", c.i, c.n, head, tail, c.head, c.tail)
		}
	}
}
