// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The module-level error set. Same contract as validate.go's: the sentinel *is* the suite's
// expected string (0003), never a paraphrase.
var (
	// ErrMemorySize and ErrTableSize are the two `check_limits` range failures, and they are
	// two sentinels rather than one because the reference builds two different messages from
	// one function:
	//
	//	check_limits lim sz at ("memory size must be at most " ^ s)   (valid.ml:208)
	//	check_limits lim sz at ("table size must be at most " ^ s)    (valid.ml:218)
	//
	// The corpus matches `memory size` and `table size` as substrings, so a shared sentinel
	// reading "size must be at most …" would satisfy neither. The suffix `s` is the reference's
	// own per-address-type text and is reproduced at the call sites, where the range it names is
	// chosen — a message and the bound it describes are one fact, and splitting them is how they
	// drift.
	ErrMemorySize = errors.New("memory size must be at most")
	ErrTableSize  = errors.New("table size must be at most")

	// ErrLimitsMinMax is the third `check_limits` failure and the one the two callers *share*
	// verbatim, which is why it is one sentinel where the range messages are two (valid.ml:104-105).
	ErrLimitsMinMax = errors.New("size minimum must not be greater than maximum")
)

// The two address-type ranges per limits kind, from valid.ml:202-206 and 212-216 — the reference's
// numbers and its own descriptive text, kept adjacent because the text names the number.
//
// Note memories are counted in **pages** and tables in **elements**, so 0x1_0000 and 0xffff_ffff
// are not the same kind of quantity despite sitting in the same shape. The i64 rows are reachable
// only with memory64/table64 gated on; they are written now because the reference writes them here
// and omitting them would make the i32 row look like the rule rather than like one of two.
const (
	memRangeI32 = 0x1_0000              // 2^16 pages
	memRangeI64 = 0x1_0000_0000_0000    // 2^48 pages
	tabRangeI32 = 0xffff_ffff           // 2^32-1
	tabRangeI64 = 0xffff_ffff_ffff_ffff // 2^64-1
)

// checkLimits is `valid.ml:96-105`, transcribed including its order.
//
// **The order is observable and is therefore part of the rule.** `min <= range` is checked before
// `max <= range`, and both before `min <= max`, so `(table 0x1_0000_0000 0x1_0000_0000 funcref)`
// reports `table size` and not `size minimum must not be greater than maximum` — both predicates
// fail on that module and the suite asserts which one speaks (`table.wast:39-42`). A transcription
// that checked min-against-max first would pass every vector whose two defects agree and fail
// exactly the three where they disagree, which is the shape a reordering bug takes here.
//
// rangeErr is the caller's sentinel because the message names the quantity being bounded; see
// ErrMemorySize. The `%w %s` detail reproduces the reference's suffix, so the full string is
// `memory size must be at most 2^16 pages (4 GiB) for i32` — the vector matches the head of it.
func checkLimits(lim binary.Limits, limRange uint64, rangeErr error, rangeText string) error {
	if lim.Min > limRange {
		return fmt.Errorf("%w %s", rangeErr, rangeText)
	}
	if !lim.HasMax {
		return nil
	}
	if lim.Max > limRange {
		return fmt.Errorf("%w %s", rangeErr, rangeText)
	}
	if lim.Min > lim.Max {
		return fmt.Errorf("%w (minimum %d, maximum %d)", ErrLimitsMinMax, lim.Min, lim.Max)
	}
	return nil
}

// checkMemoryType is `check_memorytype` (valid.ml:200-208).
func checkMemoryType(mem binary.Memory) error {
	if mem.Limits.Addr64 {
		return checkLimits(mem.Limits, memRangeI64, ErrMemorySize, "2^48 pages (256 TiB) for i64")
	}
	return checkLimits(mem.Limits, memRangeI32, ErrMemorySize, "2^16 pages (4 GiB) for i32")
}

// checkTableType is `check_tabletype` (valid.ml:210-218), minus its `check_reftype` call.
//
// The element type is not checked here because a table's reftype is the *decoder's* to refuse in
// this engine: an unrecognized reftype byte never reaches a `binary.Table`. That is the same
// division of labour ErrUnknownDataSegment's comment records, and it is a division rather than a
// gap because the byte has no valid reading to defer.
func checkTableType(tab binary.Table) error {
	if tab.Limits.Addr64 {
		return checkLimits(tab.Limits, tabRangeI64, ErrTableSize, "2^64-1 for i64")
	}
	return checkLimits(tab.Limits, tabRangeI32, ErrTableSize, "2^32-1 for i32")
}

// module checks the module-level rules, in `check_module`'s order (valid.ml:1151-1165).
//
// # Why the order is transcribed rather than chosen
//
// `check_module` runs imports, then defined memories, then defined tables, then data segments, and
// only then every function body. A module with two defects reports the *first* rule in that
// sequence, so the order decides the message on every such module — and the suite contains them.
// This function therefore reads as a list of the reference's phases in the reference's sequence,
// with the phases this slice does not implement named in place rather than omitted, so the next
// slice inserts into a visible gap instead of re-deriving where its rule goes.
//
// Reference phases, and this slice's coverage:
//
//	check_type      types                    — not this slice
//	check_import    imports                  — LIMITS ONLY (memory/table descriptors)
//	check_tag       tags                     — gated proposal
//	check_func      function declarations     — not this slice
//	check_memory    defined memories          — this slice
//	check_table     defined tables            — this slice
//	check_global    globals                   — not this slice (`constant expression required`)
//	check_data      data segments             — MEMORY INDEX ONLY (not the offset's const check)
//	check_elem      element segments          — not this slice (`unknown table`)
//	check_func_body every body                — pre-existing, below
//	check_start     the start function        — not this slice
//	check_export    exports + check_names     — not this slice (`unknown memory`, `duplicate export name`)
//
// **Each "not this slice" line is a live admission bucket, not a hypothetical.** The board's
// accepted census is the work plan, and these are its remaining rows.
func module(m *binary.Module) error {
	// check_import → check_externtype → check_memorytype / check_tabletype. An imported memory's
	// limits are checked by the same rule as a defined one's, on the same descriptor fields, and
	// `memory.wast:90-100` is the suite asserting exactly that: three vectors whose only
	// difference from the three above them is the `(import "M" "m")`. Reaching those needs the
	// import arm, so the import arm is in this slice — an imports-later split would have left a
	// bucket that looks like a different rule.
	for i := range m.Imports {
		imp := &m.Imports[i]
		switch imp.Kind {
		case binary.ExternMemory:
			if err := checkMemoryType(imp.Memory); err != nil {
				return fmt.Errorf("import %d: %w", i, err)
			}
		case binary.ExternTable:
			if err := checkTableType(imp.Table); err != nil {
				return fmt.Errorf("import %d: %w", i, err)
			}
		case binary.ExternFunc, binary.ExternGlobal, binary.ExternTag:
			// `check_externtype`'s other three arms. Enumerated rather than defaulted because a
			// silent default here would absorb a *fourth* extern kind — a future proposal's — as
			// "checked", which is the under-matching predicate that fails by construction. A func
			// import's type index, a global's mutability, and a tag's attribute are real rules and
			// are not this slice's; each is named in the phase table above.
		}
	}
	for i := range m.Memories {
		if err := checkMemoryType(m.Memories[i]); err != nil {
			return fmt.Errorf("memory %d: %w", i, err)
		}
	}
	for i := range m.Tables {
		if err := checkTableType(m.Tables[i]); err != nil {
			return fmt.Errorf("table %d: %w", i, err)
		}
	}
	// check_data → check_datamode (valid.ml:1073-1084). The active arm resolves `memory c x`
	// *before* checking the offset expression, so a segment naming a memory that is not there
	// reports `unknown memory N` and never `constant expression required` — which is why the
	// offset's const check can be absent from this slice without changing any message this slice
	// claims. Passive segments name no memory and are skipped, matching the reference's arm.
	for i := range m.Datas {
		d := &m.Datas[i]
		if d.Passive {
			continue
		}
		if err := memoryExists(m, d.MemIndex); err != nil {
			return fmt.Errorf("data segment %d: %w", i, err)
		}
	}
	return nil
}

// memoryExists is the `memory c x` lookup with the descriptor thrown away.
//
// It shares its arithmetic with addrTypeAt and deliberately not its body: that function needs the
// memory's limits to answer an address type, this one needs only whether the index resolves, and
// the two messages must stay identical. They do because both are `%w %d (module declares …)` over
// ErrUnknownMemory — the format the sentinel's own comment pins, since the corpus matches both
// `unknown memory` and `unknown memory 1` and any text between the category and the index breaks
// the second.
func memoryExists(m *binary.Module, idx uint32) error {
	total := m.ImportedMems() + len(m.Memories)
	if int(idx) < total {
		return nil
	}
	if total > 0 {
		return fmt.Errorf("%w %d (module declares %d)", ErrUnknownMemory, idx, total)
	}
	return fmt.Errorf("%w %d (module declares no memory)", ErrUnknownMemory, idx)
}
