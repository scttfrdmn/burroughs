// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build !burroughs_lazytabl

package interp

// RetainedEndsBytes reports what this instance retains for pairing tables, summed over **both** lazy stores.
//
// # Why it sums rather than switching
//
// Lanes C and D each have a store (`lazyEnds`, `siteMemo`) and every other lane has neither — but all four
// lanes compile this one accessor, so a single driver can measure any of them. Summing is what lets the
// signature be lane-independent: the lane that has nothing contributes zero, and a flip package's four
// figures come from one program rather than four.
//
// It also keeps both zero-width types *read* in the lanes where they are empty. `staticcheck` reports an
// embedded struct that nothing consults as unused, and the alternatives were worse: dropping the field in
// some lanes would make `Instance`'s declaration differ between them, and suppressing the check would hide
// the same question for a future field that really is dead.
func (in *Instance) RetainedEndsBytes() (tables, slots int) {
	lt, ls := in.retained()
	mt, ms := in.memo.retained()
	return lt + mt, ls + ms
}

// RetainedEndsCalls reports lookups and builds. It is the counter that caught lane C's 101 044-table anomaly,
// so it stays beside the bytes rather than being deleted once that was understood.
func (in *Instance) RetainedEndsCalls() (calls, builds int64) { return in.counts() }
