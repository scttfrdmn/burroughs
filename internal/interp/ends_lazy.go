// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build burroughs_lazytabl

package interp

import (
	"sync"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// This file is **lane C** of #136's question, prototyped on the #839 ruling: a dense pairing table per body,
// built the **first time that body is entered** and never for a body that is not.
//
// # Why a third lane, when lane B exists and passes
//
// Lane B (`ends_table.go`, `-tags burroughs_endtable`) builds the arena for **every** defined body at decode
// time. Measured: **23 943 864 B** on the select-family guest, 120.8% of its wasm size. Measured on the same
// run: **507** of **1 720** bodies are ever entered. So roughly **70% of that arena is for code that never
// runs**, and the question this lane answers is what the same mechanism costs when it is only paid for.
//
// It keeps lane B's per-entry cost — an array index — which is the thing a map-keyed memo would trade away.
//
// # The publication problem, which is this lane's whole risk
//
// **This is the threaded tier and several agents run the same function at once** (chair's ruling: *"Lazy
// initialization is shared mutable state across agents"*). Two agents entering a cold body race to build its
// table, and a table written into shared memory while another agent reads it is a data race whichever agent
// wins.
//
// So the table is **built privately and then published**: `sync.Map.LoadOrStore` performs the install, and a
// loser discards its own copy and uses the winner's. Both copies are byte-identical — the pairing is a pure
// function of the body — so which one wins is immaterial, and that is *why* a `LoadOrStore` is sufficient and
// a lock is not needed. What is **not** immaterial is that no agent may observe a partially filled table,
// which is what `LoadOrStore`'s single atomic publication buys.
//
// The witness is `TestLazyTableIsPublishedSafely`, a multi-agent guest entering the same cold body from
// several agents at once under `-race`, and it is watched die against an install that writes the map directly.
//
// # Keyed by `*binary.Func`, and the key is the reason this is per-instance
//
// A `*binary.Func` is owned by a module, and two instances of the same module share it — so the table could in
// principle be module-wide. It is kept per-instance anyway, because lane B's own doc records that indexing
// another module's arena is *"silently"* wrong, and a per-instance map cannot make that mistake at all. The
// cost is one map's worth of duplication per instance of the same module, which is a cost this prototype
// reports rather than hides.
type lazyEnds struct {
	tables sync.Map // *binary.Func -> []int32
}

// frameEnds returns this body's pairing table, building it on first entry.
//
// Called **once per function call**, not per block entry, so the `Load` is amortized over the whole body's
// interpretation — which is what makes a map acceptable here and would not make it acceptable in `endOf`.
// endTable is what a lane hands `endOf`; see `ends_scan.go` for why it is an alias.
type endTable = []int32

func (in *Instance) frameEnds(fn *binary.Func) endTable {
	if fn == nil || len(fn.Body) == 0 {
		return nil
	}
	if v, ok := in.lazyEnds.tables.Load(fn); ok {
		return v.([]int32)
	}
	// **Built privately.** Nothing below is reachable by another agent until the LoadOrStore.
	built := buildEnds(fn.Body)
	// **Published atomically.** A loser takes the winner's table and drops its own; the two are identical
	// because the pairing is a pure function of the body, so the discard costs one allocation on a cold
	// body and never a wrong answer.
	actual, _ := in.lazyEnds.tables.LoadOrStore(fn, built)
	return actual.([]int32)
}

// buildEnds pairs every structural header in one body with its END, in a single pass.
//
// One pass rather than `len(body)` calls to `matchEnd`: a stack of open headers is what makes it linear, and
// building a table with a quadratic algorithm would hand back the cost this lane exists to remove.
//
// A header with no END leaves **-1**, which is the sentinel lane B's arena uses, so `endOf` is shared verbatim
// and the two lanes cannot diverge in what they report for a malformed body.
func buildEnds(body []binary.Instr) []int32 {
	ends := make([]int32, len(body))
	for i := range ends {
		ends[i] = -1
	}
	var open []int32
	for i := range body {
		if body[i].Prefix != 0x00 {
			continue
		}
		switch body[i].Op {
		case opBlock, opLoop, opIf, opTryTable:
			open = append(open, int32(i))
		case opEnd:
			if n := len(open); n > 0 {
				ends[open[n-1]] = int32(i)
				open = open[:n-1]
			}
		}
	}
	return ends
}

// endOf indexes the table, falling back to the scan when it has no answer — byte-identical to lane B's, and
// for the same two populations: a hand-built `Func` whose body was never entered through `frameEnds`, and a
// header with no END.
func endOf(body []binary.Instr, ends endTable, pc int) (int, error) {
	if pc < len(ends) {
		if e := ends[pc]; e >= 0 {
			return int(e), nil
		}
	}
	return matchEnd(body, pc)
}
