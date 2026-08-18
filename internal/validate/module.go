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
	// on; **written one slice before its instruction consumers**, which is what let slice 10's
	// `tagTypeAt` land without a sentinel of its own.
	ErrUnknownTag = errors.New("unknown tag")

	// ErrNonEmptyTagResult is `check_tagtype`'s one `require` (valid.ml:194) and slice 10's only
	// new sentinel — a tag is a thrown signature, so it has parameters and no results.
	//
	// Not part of the `unknown <category>` family, so it does not enter `authority_test.go`'s
	// lookup-sentinel census: neither its identifier nor its message matches either half of that
	// control's derived predicate, which is the correct answer rather than an escape — the census
	// is about index-space verdicts and this is a well-formedness one.
	ErrNonEmptyTagResult = errors.New("non-empty tag result type")
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

// checkTypes is `check_type` (valid.ml:1107) reduced to what it delegates to `check_subtype_sub`
// (valid.ml:165-176) — the three `require`s a declared supertype list must satisfy, in the
// reference's order, because a subtype with two defects reports the first:
//
//	require (xi < x)                       forward use of type xi in sub type definition
//	require (fini = NoFinal)               sub type x has final super type xi
//	require (match_comptype c.types ct cti) sub type x does not match super type xi
//
// This is the reject-direction half of decision 0031's slice, and it is a **different consumer of
// the same relation** than the operand comparisons are: all 21 of the vectors it converts are bare
// type sections with no functions at all, so `matches` could not have reached one of them. That
// is why the slice is one relation and two call sites rather than one of either.
//
// # What is not here, and it is the same retention `matchDefType` names
//
// `check_rectype` (valid.ml:178-189) builds the context one rec group at a time — `c' = {c with
// types = c.types @ dts}` — and `check_subtype c'` then resolves every type reference against
// *that* context, so a reference to an index outside the groups declared so far is `unknown type`
// while a reference *within the current group* is fine even when it points forward. Both halves
// are the same fact about rec-group boundaries, and `binary.Module` retains none:
// `(rec (type (func (param (ref 1)))))` followed by `(rec (type (func)))` is invalid where the
// byte-identical pair inside one `rec` is valid, and nothing in this representation can tell them
// apart. Three admissions wait on it — `type-rec.wast:21,28` and `type-equivalence.wast:76`, all
// expecting `unknown type`. **Tracked as #357**, added in #353 rather than in the PR that wrote this
// paragraph: it declared the debt and gave it no number, and *declared-and-tracked passes while
// silent fails* — a "waits on it" with nothing to point at reads as tracked from every direction.
//
// The consequence for the rules that *are* here is one divergence, named because it is a real
// difference and not a simplification: a supertype index pointing past the end of the type space
// is refused by the forward-use rule (`xi < x` fails for any `xi >= len(Types)`) where the
// reference refuses it as `unknown type`. Same verdict, different message. It has no subject in
// the corpus — no `assert_invalid` expects `unknown type` from a supertype position — so it is
// recorded rather than worked around, since a workaround would be a second unresolvable-index rule
// competing with `check_typeuse`'s. **Tracked as #358**, for the reason #357 is: a zero-population
// divergence is a legitimate never-fix, and what it is not is a debt with no number.
func checkTypes(m *binary.Module) error {
	for x := range m.Types {
		for _, xi := range m.Types[x].Supertypes {
			// `require (xi < x)`. This subsumes the bounds check for every index at or above the
			// type space's length, which is the divergence the header names — and it must run
			// before the two rules below, both of which would otherwise index with xi.
			if uint64(xi) >= uint64(x) {
				return fmt.Errorf("%w %d in sub type definition", ErrForwardTypeUse, xi)
			}
			super := m.Types[xi]
			// `require (fini = NoFinal)` — a `sub final` type may not be named as a supertype.
			// `binary.CompType.Final` is `true` for a bare comptype as well as for an explicit
			// `sub final`, which is the grammar's own default (`SubT (Final, [], ct)`), so a plain
			// `(type $t (func))` is a final supertype and four of the 21 vectors are exactly that.
			if super.Final {
				return fmt.Errorf("%w %d has final super type %d", ErrSubType, x, xi)
			}
			// `require (match_comptype c.types ct cti)` — the relation, in match.go.
			if !matchCompType(tctx{gotMod: m, wantMod: m}, m.Types[x], super) {
				return fmt.Errorf("%w %d does not match super type %d", ErrSubType, x, xi)
			}
		}
	}
	return nil
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
//	check_type      types                    — checkTypes, below: check_subtype_sub's three rules.
//	                                            `check_rectype`'s context *scoping* is not here —
//	                                            see checkTypes on the retention it needs
//	check_import    imports                  — memory/table limits, and `check_tagtype` on a tag
//	                                            import (slice 10; the func and global arms are not
//	                                            this slice)
//	check_tag       tags                     — slice 10, `checkTagType` below
//	check_func      function declarations     — not this slice
//	check_memory    defined memories          — this slice
//	check_table     defined tables            — this slice
//	check_global    globals                   — this slice, `is_const`'s GlobalGet arm only (the
//	                                            declared type is checked; `check_block`'s type
//	                                            check on the initializer is not)
//	check_data      data segments             — memory index, and the offset's GlobalGet arm
//	check_elem      element segments          — table index, and the GlobalGet arm on the offset and
//	                                            on every expression-form element (not the reftype
//	                                            match, and not `check_block`'s type check)
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
//
// # The const-expression sites, counted against the reference rather than against this file
//
// `is_const`'s GlobalGet arm is one rule reached from **five** `check_const` call sites, and a
// rule implemented at four of five is not a rule — *coverage is a claim*, so the claim is
// enumerated and the absence is named:
//
//	valid.ml:1058   global initializer          checkConstGlobals(…, i)               scope = i
//	valid.ml:1070   table initializer           NO SUBJECT — see below
//	valid.ml:1078   data segment offset         checkConstGlobals(…, len(m.Globals))
//	valid.ml:1094   element segment offset      checkConstGlobals(…, len(m.Globals))
//	valid.ml:1100   element segment elements    checkConstGlobals(…, len(m.Globals)), every mode
//
// The table-initializer site is absent **by representation and not by omission**: a table's
// initializer expression is decoded and then discarded, so there is no field in
// `binary.Table` for a rule here to read. It arrives with the GC gate (#7), which is what
// introduces the form; until then a call site would have nothing to be passed. Recorded here
// because a four-of-five census that says "five" is the coverage claim an instrument cannot
// make about itself, and the next slice to touch tables needs the gap where it can see it.
func modulePre(m *binary.Module) error {
	// check_type → check_rectype → check_subtype_sub (valid.ml:178-189, :1107), the phase this
	// table listed as "not this slice" until decision 0031 opened it. First in `check_module`'s
	// order and therefore first here: a module with an ill-formed subtype declaration *and* a bad
	// memory limit reports the subtype.
	if err := checkTypes(m); err != nil {
		return err
	}
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
		case binary.ExternTag:
			// `check_externtype`'s `ExternTagT` arm (valid.ml:222-223), which is `check_tagtype` over
			// an *imported* tag — the same rule the defined-tag loop below runs, reached one phase
			// earlier. `tag.wast:22` is the vector, and it is the reason slice 10 could not write the
			// rule at the `check_tag` site alone: an import arm named as "not this slice" in the table
			// above was a live admission, exactly as that table's own note says.
			if err := checkTagType(m, imp.Index); err != nil {
				return fmt.Errorf("import %d: %w", i, err)
			}
		case binary.ExternFunc, binary.ExternGlobal:
			// `check_externtype`'s other two arms. Enumerated rather than defaulted because a
			// silent default here would absorb a *fourth* extern kind — a future proposal's — as
			// "checked", which is the under-matching predicate that fails by construction. A func
			// import's type index and a global's mutability are real rules and are not this slice's;
			// each is named in the phase table above.
		}
	}
	// check_tag (valid.ml:1049-1052, folded at :1157) → check_tagtype, in the reference's position:
	// after every import, before the function declarations and the defined memories. Slice 10's
	// module-level half, and the only rule in that family that is not an instruction rule.
	//
	// `m.Tags` is empty unless the exception-handling gate is on, since the decoder gates the tag
	// section — which is why this loop's two vectors (`tag.wast:18` for here, `:22` for the import arm
	// above) are all-on-lane rows and ADR 0036 forecasts no default-lane movement from them.
	for i := range m.Tags {
		if err := checkTagType(m, m.Tags[i].TypeIndex); err != nil {
			return fmt.Errorf("tag %d: %w", i, err)
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
	// check_global (valid.ml:1054-1059), in the reference's position: after check_table, before
	// check_data. `check_globaltype` then `check_const const t`, and this slice supplies the first
	// half of `check_const` — the `require (List.for_all (is_const c) const.it)` at :1042 — while
	// `check_block`'s type check on the initializer stays deferred.
	//
	// **The scope argument is `i`, and it is the whole rule.** `check_global` folds
	// (`{c with globals = c.globals @ [gt]}` at :1059) so a global's initializer sees the imported
	// globals plus the globals *declared before it* and not itself: `(module (global i32 (global.get
	// 0)))` is `unknown global 0` even though the module has a global 0, while the module one line
	// below it in the suite — `(global i32 (i32.const 0)) (global i32 (global.get 0))` — is valid.
	// A whole-module view answers both wrong, in opposite directions, and only one of those two
	// vectors is an `assert_invalid` the board would notice.
	for i := range m.Globals {
		if err := checkConstGlobals(m, m.Globals[i].Init, i); err != nil {
			return fmt.Errorf("global %d: %w", i, err)
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
		// `check_const c offset (addr_type_of_memory)` at :1076, after the memory resolves — the
		// sequence the loop's own comment above relies on. Every defined global is in scope here,
		// unlike a global's initializer: `check_module` has already folded all of them in by the
		// time it reaches the data segments (:1161 before :1163), which is why this passes the full
		// count where the loop above passes `i`.
		if err := checkConstGlobals(m, d.Offset, len(m.Globals)); err != nil {
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
		// The element expressions are checked in **every** mode (`check_elem` at :1100 runs
		// `check_const` over the segment's elements at :1100, before `check_elemmode` at :1101 decides
		// anything about a table), so this half is outside the Active guard below. A passive segment's
		// `(item (global.get 0))` is as much a const expression as an active one's — and an *active*
		// segment naming a missing table with a bad element expression reports the element, not the
		// table, which is why this loop precedes the tableTypeAt call rather than following it.
		//
		// **The function indices those elements name are resolved here too (#391)**, which is the
		// half of `check_const` that is a lookup rather than a type check: `check_block` walks the
		// expression and its `RefFunc x` arm runs `func c x`, so `(module (table funcref (elem 0 0)))`
		// is `unknown function 0` in the reference and was *accepted* here. Two admissions
		// (`call_indirect.wast:1037`, `return_call_indirect.wast:600`), and they are **not** instruction
		// rules — the code-section walk never visits an elem segment, which is why they were filed apart
		// from the slice whose files they sit in. *A vector's file is not its stratum.*
		//
		// The reftype match against the element type stays deferred; what lands is the index
		// resolution, in the position `check_const` reaches it, per element and after that element's
		// const check.
		if e.ByExpr {
			for j := range e.Exprs {
				if err := checkConstGlobals(m, e.Exprs[j], len(m.Globals)); err != nil {
					return fmt.Errorf("element segment %d, element %d: %w", i, j, err)
				}
				if err := elemFuncsInScope(m, refFuncIndices(e.Exprs[j])); err != nil {
					return fmt.Errorf("element segment %d, element %d: %w", i, j, err)
				}
			}
		} else if err := elemFuncsInScope(m, e.Funcs); err != nil {
			// The index form, which the wat front end desugars into `ref.func` const exprs
			// (`declaredFuncs`'s own account of `decode.ml`'s `elem_index`) and this decoder keeps as
			// a plain index vector. Both forms therefore have to resolve, and both admissions above
			// arrive in *this* branch — a fix written only for the expression form would have passed
			// every test anyone thought to write and moved no column.
			return fmt.Errorf("element segment %d: %w", i, err)
		}
		if e.Mode != binary.ElemActive {
			continue
		}
		if _, err := tableTypeAt(m, e.TableIndex); err != nil {
			return fmt.Errorf("element segment %d: %w", i, err)
		}
		// `check_const c offset ...` at :1094, after `table c x` and after the reftype match that is
		// still deferred. **The deferred check sits between these two, so a module with both a
		// reftype mismatch and a non-constant offset reports this one where the reference reports
		// the match** — a message inversion the board is measured for rather than assumed clear of,
		// and the reason the elem loop's absences were worth transcribing in order.
		if err := checkConstGlobals(m, e.Offset, len(m.Globals)); err != nil {
			return fmt.Errorf("element segment %d: %w", i, err)
		}
	}
	return nil
}

// refFuncIndices is the function indices a constant expression names through `ref.func`.
//
// The same walk `declaredFuncs` does, and deliberately not shared with it: that one unions indices
// from the whole module to build `context.refs` and cannot report *where* it found one, which is
// exactly what an `unknown function N` verdict needs. Two walks over the same instruction shape
// answering two different questions, rather than one walk whose caller has to reconstruct the
// position.
func refFuncIndices(body []binary.Instr) []uint32 {
	var idxs []uint32
	for k := range body {
		if body[k].Prefix == 0 && body[k].Op == opRefFunc {
			idxs = append(idxs, uint32(body[k].Imm0))
		}
	}
	return idxs
}

// elemFuncsInScope is `func c x` over an element segment's function indices — the lookup half of
// `check_elem`'s `check_const` (valid.ml:1100), and #391.
//
// `indexInScope` rather than `funcTypeAt`: the reference resolves the *type* here because
// `check_block` then matches it, and that match is deferred — so this is the lookup-that-discards-
// its-result case `tableTypeAt`'s comment describes from the other side, and taking the type would
// mean resolving something no rule in this slice reads.
func elemFuncsInScope(m *binary.Module, idxs []uint32) error {
	total := m.ImportedFuncs() + len(m.Funcs)
	for _, x := range idxs {
		if err := indexInScope(x, total, ErrUnknownFunc); err != nil {
			return err
		}
	}
	return nil
}

// checkConstGlobals is `is_const`'s GlobalGet arm (valid.ml:1037) over one constant expression:
//
//	| GlobalGet x -> let GlobalT (mut, _t) = global c x in mut = Cons
//
// One line of the reference, and it carries two distinct messages because `global c x` *raises*
// before the mutability test can be reached: an index that does not resolve is `unknown global N`,
// and one that resolves to a `Var` global is `constant expression required`. Ordering them the
// other way around would report the const-expr refusal for a module whose real defect is a
// dangling index — the same message-inversion the loops above are written to avoid, one rule down.
//
// # What this function is not
//
// It is **not** `check_const`. That rule is two requirements (`valid.ml:1041-1044`): the
// `List.for_all (is_const c)` predicate, and then `check_block` typing the expression against the
// expected result type. Only the first is this slice's, and only the GlobalGet arm of the first —
// `is_const`'s other arms are answered one layer down, by the decoder's own const-expr table
// (`internal/binary/instr.go:588`), which refuses a non-const *opcode* with this same
// `ErrConstExprRequired` identity before a Module exists. That split is the declared layering debt
// the sentinel's comment records, and reusing the identity rather than minting a second one is the
// `ErrUnknownTable` precedent: one rule, one error, whichever layer happens to be the one that can
// see the violation.
//
// # definedInScope, and why the caller supplies it
//
// The count of *defined* globals visible to this expression — not `len(m.Globals)` unless the
// caller means all of them. `check_global` folds one global into the context at a time
// (`{c with globals = c.globals @ [gt]}`, :1059), so a global's own initializer sees the globals
// declared before it and not itself or any after; a data or element expression sees every global,
// `check_module` having folded them all in by :1162. Imports are always in scope and are counted
// by the module, so only the defined half is a parameter.
func checkConstGlobals(m *binary.Module, expr []binary.Instr, definedInScope int) error {
	for i := range expr {
		// `Prefix == 0` is load-bearing, and it is a precondition this function's *caller* does not
		// supply where `globalOp`'s did. The validator's instruction loop reaches globalOp through a
		// switch that has already separated the prefixed opcode spaces, so a bare `Op == opGlobalGet`
		// is unambiguous there; scanning a raw expression, it is not — `Op` is the sub-opcode for a
		// prefixed instruction, so `0xfd 0x23` would read as `global.get` and resolve a SIMD
		// instruction's first immediate as a global index. That the decoder's const-expr table would
		// have refused that byte pair first is true and is exactly the reason not to lean on it: the
		// refusal is a layering debt, and a debt is not an invariant.
		if expr[i].Prefix != 0 || expr[i].Op != opGlobalGet {
			continue
		}
		idx := uint32(expr[i].Imm0)
		_, mutable, err := globalTypeAt(m, idx, definedInScope)
		if err != nil {
			return err
		}
		if mutable {
			return fmt.Errorf("%w: global.get %d names a mutable global", binary.ErrConstExprRequired, idx)
		}
	}
	return nil
}

// globalTypeAt resolves a global index to its type and mutability across the imports-then-defined
// index space, with the defined half bounded by an explicit scope.
//
// The scope parameter is the only thing this has that `tableTypeAt` next door does not, and it is
// there because global scope is the one index space the reference *grows during* module checking —
// see checkConstGlobals on the fold. `validator.globalAt` delegates here at full scope, which is
// correct for its callers: a function body runs after :1161, by which time every global is in.
func globalTypeAt(m *binary.Module, idx uint32, definedInScope int) (binary.ValType, bool, error) {
	imported := m.ImportedGlobals()
	if int(idx) < imported {
		n := 0
		for i := range m.Imports {
			if m.Imports[i].Kind != binary.ExternGlobal {
				continue
			}
			if n == int(idx) {
				return m.Imports[i].GlobalType, m.Imports[i].GlobalMutable, nil
			}
			n++
		}
		// Unreachable while ImportedGlobals counts the same predicate this loop filters on, and loud
		// rather than silent for that reason: the two agreeing is what makes the branch above sound,
		// so the way to hear about them disagreeing is to say so here.
		return binary.ValType{}, false, fmt.Errorf("%w %d (import scan found no match)", ErrUnknownGlobal, idx)
	}
	if defined := int(idx) - imported; defined < definedInScope {
		g := m.Globals[defined]
		return g.Type, g.Mutable, nil
	}
	// `(%d in scope)` verbatim from indexInScope, because the corpus matches by substring (0003) and
	// three bucket keys ride on this one format: the bare `unknown global`, `unknown global 0`, and
	// `unknown global 1`. Any text between the category and the index breaks the latter two.
	return binary.ValType{}, false, fmt.Errorf("%w %d (%d in scope)",
		ErrUnknownGlobal, idx, imported+definedInScope)
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
