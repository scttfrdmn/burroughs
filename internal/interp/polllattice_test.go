// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import "testing"

// The bulk witnesses' poll-interval check (the chair's ruling of 2026-10-19, item 1). It is the same rule
// `internal/component`'s `checkPollLattice` states for the adapter's copies. It is restated here rather than
// shared, because a test helper cannot cross packages, and the rule is two modulo checks.

// wordStopPoint is where a stopped forward write over the region [base, base+size) stopped: the byte offset,
// from base, of the first word still holding the old value. The region holds a prefix of the new value and a
// suffix of the old, so the offset is found by bisection. Read only while the agent is parked.
func wordStopPoint(t *testing.T, in *Instance, base, size int) uint64 {
	t.Helper()
	newWord := stopWord(t, in, base)
	lo, hi := 0, size/4-1 // word lo holds the new value; word hi the old
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if stopWord(t, in, base+4*mid) == newWord {
			lo = mid
		} else {
			hi = mid
		}
	}
	return uint64(4 * hi)
}

// checkBulkPollLattice asserts that every stop point is a multiple of `chunk` (no poll off the lattice), and
// that some point is not a multiple of 2·chunk (a poll at every chunk, not at every k-th). The unit is the
// instruction's own: bytes for the memory bulk ops, elements for the table and array ones. A copy polling every
// k·chunk, k ≥ 2, fails the second check, which is what the chunk-×64 neuter tests. It needs at least ten
// points, so that a correct copy's false failure, 2⁻ⁿ, is one in a thousand or less.
func checkBulkPollLattice(t *testing.T, points []uint64, chunk uint64, what string) {
	t.Helper()
	const minPoints = 10
	if len(points) < minPoints {
		t.Fatalf("only %d stop point(s) inside %s, fewer than the %d the interval check needs: instrument fault",
			len(points), what, minPoints)
	}
	offCoarse := 0
	for _, k := range points {
		if k%chunk != 0 {
			t.Fatalf("a stop parked inside %s at unit %d, not a multiple of the %d-unit chunk: a poll sits off "+
				"the chunk lattice", what, k, chunk)
		}
		if k%(2*chunk) != 0 {
			offCoarse++
		}
	}
	t.Logf("%d stop point(s) inside %s, all on the %d-unit lattice; %d off the 2×%d lattice",
		len(points), what, chunk, offCoarse, chunk)
	if offCoarse == 0 {
		t.Fatalf("all %d stop points inside %s are multiples of %d, twice the chunk: the instruction polls every "+
			"second chunk or less often, not every %d bytes", len(points), what, 2*chunk, chunk)
	}
}
