// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build burroughs_lazytabl

package interp

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// This file is **lane C** of #136's question, prototyped on the chair's ruling on the #838 review: a dense pairing table per body,
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
	ends   sync.Map // body data pointer -> []int32
	calls  atomic.Int64
	builds atomic.Int64
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
	in.lazyEnds.calls.Add(1)
	// **A body with no structural opener gets no table, and that is the whole of the count fix.**
	//
	// The first version built one for every body `frameEnds` saw, and a run showed **101 044** tables for a
	// module with **10 402** functions. Two explanations were available and *both were wrong*: nothing
	// materializes a `Func` per indirect call (`DefinedFunc` returns `&m.Funcs[i]`, a stable pointer), and
	// nothing passes re-sliced body tails. Reading the source found a third cause —
	// [Instance.runConst] synthesizes `&binary.Func{Body: expr}` **once per const expression**, and this
	// guest has **100 000 active data segments** plus 8 globals and 1 element segment. 100 009 + ~1 034
	// entered function bodies is the 101 044.
	//
	// Those bodies are `i32.const N; end` — which is why the smallest table measured 2 slots. A const
	// expression cannot contain a block, so its table is all `-1` and can never answer anything. Skipping
	// blockless bodies removes every one of them, and removes blockless *functions* too, which is the
	// majority of them in a hand-written corpus.
	if !hasOpener(fn.Body) {
		return nil
	}
	k := bodyKey(fn.Body)
	if v, ok := in.lazyEnds.ends.Load(k); ok {
		return v.([]int32)
	}
	// **Built privately.** Nothing below is reachable by another agent until the LoadOrStore.
	in.lazyEnds.builds.Add(1)
	built := buildEnds(fn.Body)
	// **Published atomically.** A loser takes the winner's table and drops its own; the two are identical
	// because the pairing is a pure function of the body, so the discard costs one allocation on a cold
	// body and never a wrong answer.
	actual, _ := in.lazyEnds.ends.LoadOrStore(k, built)
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

// RetainedEndsBytes reports what this instance's lazy tables actually hold.
//
// **Measured rather than estimated, because the estimate would be the thing in question.** Criterion 3′ asks
// for retained bytes against peak RSS, and lane C's whole claim is that it retains less than lane B by only
// paying for bodies that run. A figure derived from "30% of bodies were entered on one guest" would be
// arithmetic over a different run; this counts the slices this instance is holding.
//
// Exported so a driver outside the package can read it after a run. It is not on a hot path and takes no lock:
// `sync.Map.Range` is safe against concurrent writers, and a table installed while this is counting is simply
// counted or not — a race the answer tolerates, since it is a size report rather than a verdict.
func (in *Instance) RetainedEndsBytes() (tables, slots int) {
	in.lazyEnds.ends.Range(func(_, v any) bool {
		tables++
		slots += len(v.([]int32))
		return true
	})
	// Summed with the other lane's store for the reason `ends_retained_off.go` records: one accessor,
	// lane-independent, so a flip package's figures come from one driver.
	mt, ms := in.memo.retained()
	return tables + mt, slots + ms
}

// RetainedEndsCalls reports frameEnds calls and builds, to tell "one key per function" from "one key per call".
func (in *Instance) RetainedEndsCalls() (calls, builds int64) {
	return in.lazyEnds.calls.Load(), in.lazyEnds.builds.Load()
}

// bodyKey is a retained body's stable identity: its backing array's data pointer.
func bodyKey(body []binary.Instr) uintptr {
	return uintptr(unsafe.Pointer(unsafe.SliceData(body)))
}

// hasOpener reports whether a body contains any structural header, i.e. whether a pairing table could ever
// answer a question about it.
//
// **One pass over a body that is about to be interpreted anyway**, and only on the cold path: a body with a
// table takes the `Load` and never reaches here again. A const expression — two instructions — costs two
// comparisons once.
func hasOpener(body []binary.Instr) bool {
	for i := range body {
		if body[i].Prefix != 0x00 {
			continue
		}
		switch body[i].Op {
		case opBlock, opLoop, opIf, opTryTable:
			return true
		}
	}
	return false
}
