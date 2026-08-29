// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build burroughs_endtable

package interp

import "github.com/scttfrdmn/burroughs/internal/binary"

// This file is **lane B** of #136: decision 0002's letter for branch resolution, so entering a block
// is an index rather than a scan. Its pair is `ends_scan.go`. See that file for why the two lanes are
// build tags rather than a runtime knob.
//
// **It was a probe and is now the port** (0048). The probe built the table on first entry to a body
// and cached it in a process-lifetime `sync.Map` keyed by `*binary.Func` — the same *amortization*
// as 0002's form, once per body rather than once per entry, reached without committing to a
// retention decision that had not been made. 0048 made it: the table is built at decode, in
// `internal/binary`, in a per-module arena located by one `int32` on `Func`. So the cache is gone
// along with its leak, and what is left here is a subslice of something the module already owns.
//
// That leaves this lane's whole cost at decode time, which is where the flip's forecast has to look:
// there is now no interpreter-side work at all beyond reading a field.

// frameEnds returns this body's pairing table, built by the decoder.
//
// **The receiver is the owner of `fn`, and that is a real precondition rather than a coincidence.**
// The arena lives on `in.mod`, so a frame running with the wrong instance as receiver would index
// another module's arena — silently, since the slice bounds would usually be valid. What makes it
// safe is the invariant `enterFrame` already documents for `memory`/`global.get`/`call` resolution:
// a frame runs with the *callee's* instance, imports and tail calls included. This lane rests on the
// same invariant those do, so it cannot drift from them separately.
//
// No build, no cache, no allocation: `binary.Module.FuncEnds` is bounds arithmetic over a slice the
// module retains.
func (in *Instance) frameEnds(fn *binary.Func) []int32 {
	return in.mod.FuncEnds(fn)
}

// endOf indexes the table, falling back to the scan when it has no answer.
//
// **The fallback is not defensive padding, and it has a live population.** Two of them. A `Func`
// built by hand — every interpreter test that assembles a body directly rather than decoding one —
// has `EndsOff == 0` and so no table at all, because only the decoder fills the arena; those bodies
// take `matchEnd` in both lanes, which is what keeps the tagged lane's tests meaningful instead of
// merely green. And a body carrying an unterminated header, which the decoder cannot produce but a
// fuzz mutation of an already-decoded form can, leaves `-1` in its slot; the error a caller must get
// for that is the one `matchEnd` already writes, so it is delegated rather than restated and the two
// lanes cannot diverge in what they report.
func endOf(body []binary.Instr, ends []int32, pc int) (int, error) {
	if pc < len(ends) {
		if e := ends[pc]; e >= 0 {
			return int(e), nil
		}
	}
	return matchEnd(body, pc)
}
