// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build !burroughs_scanlane && !burroughs_endtable && !burroughs_sitememo

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
	// slots is one atomic pointer per DEFINED FUNCTION, indexed by that function's position in
	// `mod.Funcs`. Publishing a table is a single compare-and-swap on one slot: no map, no copy, no `any`.
	//
	// **It was a copy-on-write map, and that was quadratic in a hidden variable** — the very shape #835
	// itself was. Every cold miss copied the whole map, so total work grew with the square of the number of
	// distinct functions entered. Measured on lever 2's filter: **1 814 misses, 1 644 391 entries copied**,
	// which is exactly m(m−1)/2 to the entry. This guest has **10 402** functions, so a long-running program
	// that eventually touched most of its code would copy **54 103 401** entries — a startup cost growing
	// quadratically that no short-test benchmark would ever show. Having just removed one cost that was
	// quadratic in a hidden variable, shipping another was not an option (chair's ruling, #840 review).
	//
	// Allocated once under `once`, which also supplies the happens-before every later reader needs: a
	// reader that has returned from `once.Do` is guaranteed to see the fully allocated slice.
	once  sync.Once
	slots []atomic.Pointer[[]int32]

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
	in.calls.Add(1)
	// A body with no structural opener gets no table: its table would be all `-1` and could never answer
	// anything. That removes every const-expression body — 100 009 of them on this guest — and every
	// blockless function, which is the majority of them in a hand-written corpus.
	if !hasOpener(fn.Body) {
		return nil
	}
	slot := in.funcSlot(fn)
	if slot == nil {
		// Not one of this module's defined functions, so it has no slot. The only producer of such a `Func`
		// is `runConst`, whose bodies are const expressions — and those were already excluded above, so this
		// is a belt-and-braces return rather than a live path. Returning nil means `endOf` scans, which is
		// slower and correct.
		return nil
	}
	if tbl := slot.Load(); tbl != nil {
		return *tbl
	}
	// **Built privately, then published with ONE compare-and-swap on this function's own slot.** A loser
	// takes the winner's table: the two are identical, because the pairing is a pure function of the body,
	// which is why a single CAS suffices and no lock or retry loop is needed.
	in.builds.Add(1)
	built := buildEnds(fn.Body)
	if slot.CompareAndSwap(nil, &built) {
		return built
	}
	if tbl := slot.Load(); tbl != nil {
		return *tbl
	}
	return built
}

// funcSlot answers this function's table slot, or nil if it is not one of the module's defined functions.
//
// # The index comes from the pointer, and it is VERIFIED rather than assumed
//
// `DefinedFunc` returns `&mod.Funcs[i]`, a stable pointer into a slice the decoder has finished with, so a
// function's index is recoverable from its address in O(1). The alternative was a field on `binary.Func`
// holding the slot — rejected because it puts *runtime* state in the *decoder's* type, and keeping the decoder
// free of runtime state is the reason this lives on the instance.
//
// Every step is checked: the offset must be within the slice, must be a whole multiple of the element size,
// and `&Funcs[i]` must be the pointer handed in. **A failed check is not an invariant violation** — it means
// the `Func` was synthesized elsewhere, which `runConst` legitimately does — so it answers nil rather than
// panicking, and the caller scans.
//
// # The slots are per-instance, and sharing them would also be correct
//
// A table depends only on a function's *body*, so two instances of one module could share one set of slots and
// each would compute the same answers. They are not shared, because lane B's own doc records that indexing
// another module's arena is wrong *silently*, and a per-instance slice cannot make that mistake at all. The
// cost is one slice of pointers per instance, which is `8 × len(Funcs)` bytes and does not grow with use.
func (in *Instance) funcSlot(fn *binary.Func) *atomic.Pointer[[]int32] {
	// **The nil-module guard is not defensive padding; a witness found it.** An `Instance` with no module is
	// constructible — `&Instance{}` is what the publication witness built before this mechanism needed a
	// module — and the previous body-pointer key never touched `mod`. Dereferencing it here segfaulted. An
	// instance with no functions has no slot to hand back, which is the same answer as a `Func` from
	// elsewhere.
	if in.mod == nil {
		return nil
	}
	fns := in.mod.Funcs
	if len(fns) == 0 {
		return nil
	}
	base := uintptr(unsafe.Pointer(&fns[0]))
	p := uintptr(unsafe.Pointer(fn))
	if p < base {
		return nil
	}
	size := unsafe.Sizeof(fns[0])
	off := p - base
	if off%size != 0 {
		return nil
	}
	i := off / size
	if i >= uintptr(len(fns)) || &fns[i] != fn {
		return nil
	}
	in.once.Do(func() { in.slots = make([]atomic.Pointer[[]int32], len(fns)) })
	return &in.slots[i]
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
	for i := range in.slots {
		if tbl := in.slots[i].Load(); tbl != nil {
			tables++
			slots += len(*tbl)
		}
	}
	// Summed with the other lane's store for the reason `ends_retained_off.go` records: one accessor,
	// lane-independent, so a flip package's figures come from one driver.
	mt, ms := in.memo.retained()
	return tables + mt, slots + ms
}

// RetainedEndsCalls reports frameEnds calls and builds, to tell "one key per function" from "one key per call".
func (in *Instance) RetainedEndsCalls() (calls, builds int64) {
	return in.calls.Load(), in.builds.Load()
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
