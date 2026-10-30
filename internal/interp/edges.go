// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

// The bulk and SIMD region's edge words ([ADR 0112][0112], #1167).
//
// **What this repairs is #982's host race, in ADR 0064's plain region.** A plain write of byte 0x931 from
// `memory.fill`, `memory.copy`, `memory.init` or a SIMD store sits beside an aligned narrow access to byte 0x930,
// and that narrow access is an atomic of the word containing both. The guest bytes are disjoint, so the guest
// has not raced, but the host has: a plain access and an atomic one to the same 32-bit word. ADR 0111 removed
// that pair from the typed path. ADR 0064's ruling 1 kept this region plain because *"the guest model permits
// the tear"*, which argues from a guest that races, and that does not reach a neighbour that doesn't.
//
// **Only the extent's partial edge words are touched atomically.** An interior word lies wholly inside the
// extent, so any other access to it overlaps the extent's own bytes, which is a guest race 0064 already permits.
// A neighbour that does not overlap can share only an edge word. So the interior keeps its plain `copy` and its
// memset, and only the first and last partial word go through ADR 0111's span helpers. That is at most two word
// operations per extent.
//
// [0112]: ../../docs/decisions/0112-a-bulk-or-simd-extent-touches-its-partial-edge-words-atomically-and-its-whole-words-plainly-because-only-an-edge-word-can-be-shared-with-a-disjoint-neighbour.md

// edgeSplit measures the extent [i, i+n) of an image that starts word-aligned: head is the number of bytes
// before its first word boundary, and tail the number after its last. An extent inside one word is all head.
// The bytes between are whole words of the extent, and an aligned whole-word extent has neither.
func edgeSplit(i, n uint64) (head, tail uint64) {
	head = (4 - i&3) & 3
	if head >= n {
		return n, 0
	}
	return head, (i + n) & 3
}

// splat is byte k in every byte of a word, for a fill's edge spans.
func splat(k byte) uint64 { return uint64(k) * 0x0101010101010101 }

// packLE and unpackLE carry up to 8 bytes as the span helpers do: byte j at bits 8j.
func packLE(b []byte) uint64 {
	var v uint64
	for j, x := range b {
		v |= uint64(x) << (8 * j)
	}
	return v
}

func unpackLE(b []byte, v uint64) {
	for j := range b {
		b[j] = byte(v >> (8 * j))
	}
}

// readEdges is `memory.read` for the SIMD loads: the n bytes at the effective address, with the access's partial
// edge words read atomically. A whole-word access returns the image's own bytes, as `read` does. A partial one is
// assembled in buf, which must hold n bytes. It is the caller's, so the unaligned path allocates nothing.
func (m *memory) readEdges(idx, offset, n uint64, buf []byte) ([]byte, error) {
	ea, err := effectiveAddress(idx, offset)
	if err != nil {
		return nil, err
	}
	// One load, then both the check and the access against it (decision 0058).
	bs := m.view()
	if n > uint64(len(bs)) || ea > uint64(len(bs))-n {
		return nil, trapOOB
	}
	head, tail := edgeSplit(ea, n)
	if head == 0 && tail == 0 {
		return bs[ea : ea+n], nil
	}
	dst := buf[:n]
	unpackLE(dst[:head], atomicLoadSpanOrZero(bs[ea:ea+head]))
	copy(dst[head:n-tail], bs[ea+head:ea+n-tail])
	unpackLE(dst[n-tail:], atomicLoadSpanOrZero(bs[ea+n-tail:ea+n]))
	return dst, nil
}

// writeEdges is `memory.write` for the SIMD stores: src at the effective address, with the access's partial edge
// words written through their containing words' atomics. Nothing is written when the access is out of bounds,
// for `write`'s reason.
func (m *memory) writeEdges(idx, offset uint64, src []byte) error {
	ea, err := effectiveAddress(idx, offset)
	if err != nil {
		return err
	}
	n := uint64(len(src))
	bs := m.view()
	if n > uint64(len(bs)) || ea > uint64(len(bs))-n {
		return trapOOB
	}
	head, tail := edgeSplit(ea, n)
	atomicStoreSpanOrNothing(bs[ea:ea+head], packLE(src[:head]))
	copy(bs[ea+head:ea+n-tail], src[head:n-tail])
	atomicStoreSpanOrNothing(bs[ea+n-tail:ea+n], packLE(src[n-tail:]))
	return nil
}

// atomicLoadSpanOrZero and atomicStoreSpanOrNothing are the span helpers for an edge that may be empty: an empty
// span has no first byte to take a pointer to.
func atomicLoadSpanOrZero(bs []byte) uint64 {
	if len(bs) == 0 {
		return 0
	}
	return atomicLoadSpan(bs)
}

func atomicStoreSpanOrNothing(bs []byte, v uint64) {
	if len(bs) != 0 {
		atomicStoreSpan(bs, v)
	}
}
