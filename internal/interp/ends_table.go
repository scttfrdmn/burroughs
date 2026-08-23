// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build burroughs_endtable

package interp

import (
	"sync"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// This file is **lane B** of #136's probe: decision 0002's letter for branch resolution — one pass
// per body recording every pairing, so entering a block is an index rather than a scan. Its pair is
// `ends_scan.go`. See that file for why the two lanes are build tags.
//
// **This is a probe, not the port.** 0002's form resolves at *decode* and files the pairing beside
// the body in `internal/binary` — the parallel array keyed by instruction index that #136's
// definition of done owes both consumers, `br_table`'s label vector included. What is here instead
// builds the table on first entry to a body and caches it for the process, which is the same
// *amortization* (once per body, not once per entry) reached without changing a retention decision
// that has not been made. If the measurement discharges #136, the table moves to `binary` and this
// file is deleted; if it falsifies 0002's premise, this file is deleted anyway. Either way the
// probe is not a design choice smuggled in as an experiment.

// endTables caches one table per `*binary.Func`. A `sync.Map` because the cost that matters is the
// *entry-time* one and this is paid once per **call**, not once per block entry; a benchmark whose
// hot loop iterates inside one call pays it once against a thousand iterations. It retains a table
// per function for the life of the process, which is a leak a probe may have and the port may not —
// the port has no cache at all, the table being decoded with the body.
var endTables sync.Map // *binary.Func -> []int32

// frameEnds returns the pairing table for this body, building it once.
func frameEnds(fn *binary.Func, body []binary.Instr) []int32 {
	if v, ok := endTables.Load(fn); ok {
		return v.([]int32)
	}
	t := buildEndTable(body)
	endTables.Store(fn, t)
	return t
}

// buildEndTable is the one pass: a stack of open headers, each closed by the `END` that pops it.
//
// **Dense `[]int32`, not a map, and that is part of what is under test.** A map lookup per block
// entry would be competing with a scan whose length is often two or three instructions, and a
// result reading "the table is slower on short bodies" would be a fact about `map` rather than
// about 0002's claim. 0002 says "resolved targets"; the honest reading is O(1), so the probe is
// O(1). `-1` is "no header here", which every non-structural index keeps.
func buildEndTable(body []binary.Instr) []int32 {
	t := make([]int32, len(body))
	for i := range t {
		t[i] = -1
	}
	var open []int32
	for i, ins := range body {
		if ins.Prefix != 0x00 {
			continue
		}
		switch ins.Op {
		case opBlock, opLoop, opIf, opTryTable:
			open = append(open, int32(i))
		case opEnd:
			if n := len(open); n > 0 {
				t[open[n-1]] = int32(i)
				open = open[:n-1]
			}
		}
	}
	return t
}

// endOf indexes the table, falling back to the scan when it has no answer.
//
// **The fallback is not defensive padding**: it is `matchEnd`'s own not-found case, which exists
// because a hand-built or fuzz-mutated body can carry an unterminated header the decoder could not
// have produced. `buildEndTable` leaves such a header at `-1`, and the error a caller must get for
// it is the one `matchEnd` already writes — so it is delegated rather than restated, and the two
// lanes cannot diverge in what they report for the layering debt.
func endOf(body []binary.Instr, ends []int32, pc int) (int, error) {
	if pc < len(ends) {
		if e := ends[pc]; e >= 0 {
			return int(e), nil
		}
	}
	return matchEnd(body, pc)
}
