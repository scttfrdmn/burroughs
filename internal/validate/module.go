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
	// verbatim — `check_limits` builds it once, at valid.ml:104-105 — which is why it is one
	// sentinel where the range messages are two.
	ErrLimitsMinMax = errors.New("size minimum must not be greater than maximum")

	// ErrDuplicateExport is `check_names`' only failure (valid.ml:1142-1149), and the reference
	// puts the offending name in quotes — `duplicate export name "a"`. The corpus matches the
	// head, so the quoting is reproduced because the reference does it and not because a vector
	// needs it; %q is Go's quoting rather than OCaml's `string_of_name`, which differs on
	// non-ASCII names and cannot differ on the matched head.
	ErrDuplicateExport = errors.New("duplicate export name")

	// ErrUnknownTag is the tag arm of `check_export`, and the tenth category of the reference's
	// one `lookup` function (valid.ml:40-49) — the whole family is `"unknown " ^ category ^ " "
	// ^ index`, which is why every sentinel here reads that way. Reachable only with the EH gate
	// on; written because the arm is one of five and omitting it would be a silent accept, not a
	// deferral.
	ErrUnknownTag = errors.New("unknown tag")
)

// The two address-type ranges per limits kind, from `check_memorytype`'s table at valid.ml:202-206
// and `check_tabletype`'s at 212-216 — the reference's numbers and its own descriptive text, kept
// adjacent because the text names the number.
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

// checkLimits is `check_limits` (`valid.ml:96-105`), transcribed including its order.
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

// modulePre checks the module-level rules that run *before* any function body, in
// `check_module`'s order (valid.ml:1151-1164). Its other half is moduleExports.
//
// **The split is the reference's own, not a convenience.** `check_module` builds its context from
// nine phases, then walks every body, then checks the start function and the exports (l.1165-1169).
// So the two halves cannot be one function without either moving the export phase ahead of the
// bodies or moving the bodies into this file, and the first of those is observable: a module with an
// ill-typed body and a bad export must report the body.
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
//	check_elem      element segments          — TABLE INDEX ONLY (not the reftype match, and not the
//	                                            offset's or the elements' const checks)
//	check_func_body every body                — pre-existing, in Module between the two halves
//	check_start     the start function        — not this slice, and not an admission either: its
//	                                            three vectors are refused above the validator with
//	                                            the wrong message, so they sit in the board-wide
//	                                            mismatch stratum and a rule here would have to take
//	                                            the refusal *over* rather than supply it
//	check_export    exports                   — moduleExports, below
//	check_names     export names              — moduleExports, below
//
// **Each "not this slice" line is a live admission bucket, not a hypothetical.** The board's
// accepted census is the work plan, and these are its remaining rows.
func modulePre(m *binary.Module) error {
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
	// `check_elem` → `check_elemmode` (valid.ml:1086-1102), and the same argument the data loop above
	// makes for its own two absences: the Active arm resolves `table c x` at `valid.ml:1090` *before*
	// the reftype match at :1091-1093 and before `check_const` on the offset at :1094, so a segment
	// naming a table the module does not have reports `unknown table N` and can report nothing else.
	// Both later halves are deferred, and the ordering is what makes deferring them message-preserving
	// rather than merely convenient.
	//
	// **`tableTypeAt` rather than an existence helper, and the discarded descriptor is the point.** The
	// reference destructures the table type here (`let TableT (at, _lim, rt) = table c x`) because both
	// deferred halves consume it — the reftype match wants `rt`, the offset's const check wants the
	// address type derived from `at`. Resolving through the function that already returns it means those
	// halves land at this call site without rewriting it, where `indexInScope` would have to be replaced
	// by their arrival. `data` next door genuinely has nothing to return (`valid.ml:52` answers unit),
	// which is why that loop's helper throws the descriptor away and this one does not.
	//
	// Passive and Declarative segments name no table and are skipped. That is the reference's arms and
	// not an optimization, and the two modes are skipped for *different* reasons the modes' own comment
	// records: `check_datamode`'s Declarative arm is `assert false` where `check_elemmode`'s is `()`, so
	// a declarative element segment is legal where a declarative data segment cannot be built at all.
	for i := range m.Elems {
		e := &m.Elems[i]
		if e.Mode != binary.ElemActive {
			continue
		}
		if _, err := tableTypeAt(m, e.TableIndex); err != nil {
			return fmt.Errorf("element segment %d: %w", i, err)
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

// moduleExports is `check_export` over every export followed by `check_names` over the names it
// produced — the reference's l.1168-1169, in that sequence.
//
// # Two orderings, both transcribed
//
// The first is why this is a separate function from modulePre: exports are checked **after every
// function body**, so a module with an ill-typed body and an unknown export index reports the body.
//
// The second is why this is two loops and not one: the reference maps `check_export` across *all*
// exports and only then hands the resulting names to `check_names`, so a module whose duplicate-named
// export also names a missing function reports `unknown function`, never `duplicate export name`.
// A single loop that compared each name as it went would invert that on exactly the modules where
// both defects appear. **Whether the corpus can see it is a separate question from whether it is the
// rule, and the answer is measured, not assumed:** fusing the loops moves neither lane — 60817/208
// default and 64708/338 all-on, either way — so this ordering is transcribed on the reference's
// authority alone, and export_test.go's M1 row is the only instrument holding it.
func moduleExports(m *binary.Module) error {
	for i := range m.Exports {
		ex := &m.Exports[i]
		if err := exportExists(m, ex.Kind, ex.Index); err != nil {
			return fmt.Errorf("export %d (%q): %w", i, ex.Name, err)
		}
	}
	seen := make(map[string]struct{}, len(m.Exports))
	for i := range m.Exports {
		name := m.Exports[i].Name
		if _, dup := seen[name]; dup {
			return fmt.Errorf("%w %q", ErrDuplicateExport, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// exportExists is `check_export`'s five arms (valid.ml:1128-1137) with the externtype thrown away —
// the same relationship memoryExists has to `memory c x`, and for the same reason: this phase needs
// only whether the index resolves.
//
// Each arm's message is the one that index space already emits elsewhere in this package, because an
// error's text is a property of the rule and not of the phase that happened to find the violation.
// That is why memory delegates rather than being spelled again here: `unknown memory` has a
// two-branch message of its own (`module declares no memory`), and a second copy of it in this switch
// is a copy that can drift.
func exportExists(m *binary.Module, kind binary.ExternKind, idx uint32) error {
	switch kind {
	case binary.ExternFunc:
		return indexInScope(idx, m.ImportedFuncs()+len(m.Funcs), ErrUnknownFunc)
	case binary.ExternTable:
		return indexInScope(idx, m.ImportedTables()+len(m.Tables), ErrUnknownTable)
	case binary.ExternMemory:
		return memoryExists(m, idx)
	case binary.ExternGlobal:
		return indexInScope(idx, m.ImportedGlobals()+len(m.Globals), ErrUnknownGlobal)
	case binary.ExternTag:
		return indexInScope(idx, m.ImportedTags()+len(m.Tags), ErrUnknownTag)
	}
	// Unreachable through the decoder, which refuses any sixth kind byte before a Module exists —
	// and therefore loud rather than a bare `return nil`. A silent accept here would make a future
	// proposal's extern kind read as *checked* the moment the decoder learned to admit it, which is
	// the under-matching predicate that fails by construction.
	return fmt.Errorf("export kind %#x is not one of check_externtype's five arms", byte(kind))
}

// indexInScope is the reference's `lookup` (valid.ml:40-42) for the index spaces whose engine message
// already reads `(%d in scope)` — funcTypeAt's and globalAt's tail, kept identical here so the same
// rule reads the same way whichever phase reports it.
//
// It takes the total rather than the two halves because the import-then-defined split matters to
// *resolution* and not to *existence*: only the boundary of the space decides this question, and the
// two accessors that compute it are the module's own.
func indexInScope(idx uint32, total int, sent error) error {
	if int(idx) < total {
		return nil
	}
	return fmt.Errorf("%w %d (%d in scope)", sent, idx, total)
}
