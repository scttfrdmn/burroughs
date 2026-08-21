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

// checkTableType is `check_tabletype` (valid.ml:210-218), including its `check_reftype` call.
//
// **This said the reftype was "the *decoder's* to refuse ... an unrecognized reftype byte never
// reaches a `binary.Table`", and the premise is true of the wrong half of the type.** An
// unrecognized byte is indeed refused a layer down; an *indexed* reftype is not, and cannot be —
// `(table 1 (ref null 7))` names a type the decoder is forbidden to have an opinion about, index
// validity being #9's question by construction. So the arm the old comment closed was the arm the
// rule is entirely about, which is the shape of grave #431: a division of labour asserted over a
// domain one of the two parties never saw. Grave #469, second of its two sites.
//
// **The `check_reftype` runs before `check_limits`, and the order is the observable part.** A table
// with an out-of-scope element type *and* an over-range minimum reports the element type. No vector
// pairs those two defects — `table.wast`'s reftypes all resolve — so the order is transcribed on the
// reference's authority, like `check_func`'s position below.
//
// The scope is the caller's because an imported table and a defined one reach this from different
// phases; both see the whole type space, `check_module` having folded every rec group in at :1156.
func checkTableType(scope int, tab binary.TableType) error {
	if err := checkValTypeScoped(scope, tab.ElemType); err != nil {
		return err
	}
	if tab.Limits.Addr64 {
		return checkLimits(tab.Limits, tabRangeI64, ErrTableSize, "2^64-1 for i64")
	}
	return checkLimits(tab.Limits, tabRangeI32, ErrTableSize, "2^32-1 for i32")
}

// checkTypes is `check_type` (valid.ml:1107), which is `check_rectype` per declared group: the
// context is extended one group at a time and each group's members are checked against it twice —
// `check_subtype` over all of them, then `check_subtype_sub` over all of them.
//
// `check_subtype_sub`'s half is the three `require`s a declared supertype list must satisfy, in the
// reference's order, because a subtype with two defects reports the first:
//
//	require (xi < x)                       forward use of type xi in sub type definition
//	require (fini = NoFinal)               sub type x has final super type xi
//	require (match_comptype c.types ct cti) sub type x does not match super type xi
//
// That half is the reject-direction half of decision 0031's slice, and it is a **different consumer
// of the same relation** than the operand comparisons are: all 21 of the vectors it converts are bare
// type sections with no functions at all, so `matches` could not have reached one of them. That
// is why the slice is one relation and two call sites rather than one of either.
//
// # The group scoping is here now, and the paragraph it replaces had been false for six PRs
//
// `check_rectype` (valid.ml:178-189) builds the context one rec group at a time — `c' = {c with
// types = c.types @ dts}` — and `check_subtype c'` then resolves every type reference against
// *that* context, so a reference to an index outside the groups declared so far is `unknown type`
// while a reference *within the current group* is fine even when it points forward:
// `(rec (type (func (param (ref 1)))))` followed by `(rec (type (func)))` is invalid where the
// byte-identical pair inside one `rec` is valid.
//
// This paragraph read *"and `binary.Module` retains none"* until now, with #357 tracking the wait.
// **The retention had already landed when the sentence was written**: #352 gave `binary.CompType`
// `RecStart`/`RecLen` for `matchDefType`'s rolled comparison — the two fields *are* a type's
// identity — and this file went on naming the same fact as absent. Same shape as grave #431 one
// package over: a foreclosing paragraph that a later PR falsified and nobody re-read, telling every
// reader after it to wait for a retention that was in the struct. What the rule actually needed was
// the **prefix-of-context discipline**, which is `recGroupExtent` below, and #357's own body said so
// in its second section while its title still said the representation was missing it. Grave #469,
// which carries both sites and the two reasons the #427 sweep reaches neither of them — a doc comment
// is not one of its three positions, and a sibling package's retention is not a gate.
//
// # The two passes are the reference's, and the order decides the message
//
// `check_rectype` runs `check_subtype` over **every** member of the group and only then
// `check_subtype_sub` over every member. So a group holding both an unresolvable type reference and
// a final supertype reports the reference, whichever member each defect is on — which is why this is
// two loops over one group rather than one loop doing both.
//
// It also supplies the message #358 recorded as a divergence: `check_subtype`'s
// `List.iter (fun ut -> check_typeuse c ut at) uts` resolves each declared supertype index against
// the group's own scope *before* `check_subtype_sub`'s `xi < x` runs, so a supertype index past the
// end is `unknown type` and not `forward use of type`. The divergence had a population of zero and
// closes as a side effect of transcribing the phase it lived in, which is the shape a never-fix takes
// when the rule around it lands.
func checkTypes(m *binary.Module) error {
	for x := 0; x < len(m.Types); {
		n, scope := recGroupExtent(m, x)
		// `List.iter (fun st -> check_subtype c' st at) sts` (valid.ml:186) — every member of this
		// group, against the context this group has just been appended to.
		for i := x; i < x+n; i++ {
			if err := checkSubtype(m, scope, i); err != nil {
				return err
			}
		}
		// `Lib.List32.iteri (fun i st -> check_subtype_sub c' st (Int32.add x i) at) sts`
		// (valid.ml:187-188) — a second pass over the same members.
		for i := x; i < x+n; i++ {
			if err := checkSubtypeSub(m, i); err != nil {
				return err
			}
		}
		x += n
	}
	return nil
}

// recGroupExtent answers, for the type at index x, how many members of x's rec group remain from x
// onward and how large `c.types` is once that group has been appended.
//
// **The scope is `RecStart+RecLen`, which is `Lib.List32.length c.types` after the append** — the
// group's own members are in scope for each other (that is what makes a forward reference *inside* a
// `rec` legal), and nothing declared later is.
//
// **The fallback is what a hand-built `binary.Module` gets, and it is stated rather than assumed
// away.** `RecStart`/`RecLen` carry the invariant `RecStart <= own index < RecStart+RecLen`, which
// the decoder establishes for every accepted type section (`labelRecGroup`) and which a zero value
// *violates* — 0 <= 0 < 0 is false — so an inconsistent extent is detectable rather than ambiguous.
// This package's own fixtures build modules by hand, and the ones that care set both fields
// explicitly (`match_two_module_test.go`); the ones that do not would otherwise be judged against a
// scope of zero and every type reference in them would become `unknown type`. So an inconsistent
// extent degrades to a singleton group over the flat type space, which is exactly the rule this
// function was added to tighten and no looser than what stood here before it. Two consequences worth
// naming. The loop above cannot spin, because `n >= 1` on every path. And **the fallback means a
// hand-built fixture cannot witness the prefix rule at all** — it would be judged against the flat
// space and pass either way — which is why `TestRecGroupPrefixIsTheScope` drives its pair through the
// encoder and the decoder and then asserts the extents it got back are self-consistent. That
// assertion is what distinguishes "the rule fired and agreed" from "the fallback ran", two outcomes a
// verdict alone cannot tell apart.
func recGroupExtent(m *binary.Module, x int) (n, scope int) {
	ct := m.Types[x]
	start, length := uint64(ct.RecStart), uint64(ct.RecLen)
	end := start + length
	if length > 0 && start <= uint64(x) && uint64(x) < end && end <= uint64(len(m.Types)) {
		return int(end - uint64(x)), int(end)
	}
	return 1, len(m.Types)
}

// checkSubtype is `check_subtype` (valid.ml:160-163): every declared supertype index resolves, and
// then the comptype's own type references do.
//
// Both halves are `check_typeuse`'s one rule reached through different productions — a supertype list
// holds typeuses directly, and a comptype reaches them through valtypes and storage types — so both
// go through `checkValTypeScoped`'s message rather than inventing a second `unknown type`.
func checkSubtype(m *binary.Module, scope, x int) error {
	ct := m.Types[x]
	// `List.iter (fun ut -> check_typeuse c ut at) uts`. A supertype index is a bare type index
	// rather than a valtype, so the bound is applied directly and the message is spelled the same
	// way `checkValTypeScoped` spells it.
	for _, xi := range ct.Supertypes {
		if uint64(xi) >= uint64(scope) {
			return fmt.Errorf("%w %d (%d in scope)", ErrUnknownType, xi, scope)
		}
	}
	// `check_comptype c ct at`.
	return checkCompType(scope, ct)
}

// checkCompType is `check_comptype` (valid.ml:150-159) — its three arms, each reducing to
// `check_valtype` over the type references the form carries.
//
// A struct's fields and an array's single field both go through `check_fieldtype` →
// `check_storagetype`, whose `PackStorageT` arm is `()`: a packed i8/i16 width names no type, so it is
// skipped here rather than being routed through a valtype it has no representation as (`StorageType`
// keeps the wire's own storage-versus-value boundary, per 0021).
func checkCompType(scope int, ct binary.CompType) error {
	switch ct.Kind {
	case binary.CompFunc:
		// `check_resulttype` over both halves, params first — the reference's order, and it is
		// observable on a functype whose params and results are both unresolvable.
		for _, t := range ct.Func.Params {
			if err := checkValTypeScoped(scope, t); err != nil {
				return err
			}
		}
		for _, t := range ct.Func.Results {
			if err := checkValTypeScoped(scope, t); err != nil {
				return err
			}
		}
	case binary.CompStruct, binary.CompArray:
		for _, f := range ct.Fields {
			if f.Storage.Packed {
				continue
			}
			if err := checkValTypeScoped(scope, f.Storage.Val); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSubtypeSub is `check_subtype_sub` (valid.ml:165-176) — the three `require`s a declared
// supertype list must satisfy, in the reference's order, because a subtype with two defects reports
// the first.
//
// Every index it reads has already resolved: `checkSubtype` ran over this whole group first, which is
// what licenses the bare `m.Types[xi]` below.
func checkSubtypeSub(m *binary.Module, x int) error {
	for _, xi := range m.Types[x].Supertypes {
		// `require (xi < x)`.
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
//	check_type      types                    — checkTypes, below: `check_rectype` whole, its context
//	                                            scoped per rec group, `check_subtype`'s typeuse and
//	                                            comptype walk, and `check_subtype_sub`'s three rules
//	check_import    imports                  — `check_externtype` whole: memory/table limits, a table's
//	                                            element type, `check_globaltype`, `check_tagtype` on a
//	                                            tag import (slice 10), and the `ExternFuncT` arm
//	                                            (#328's Rule B)
//	check_tag       tags                     — slice 10, `checkTagType` below
//	check_func      function declarations     — `funcType` over each declared index, below. This row
//	                                            read "not this slice" for four slices after the loop
//	                                            landed. `check_local`'s `check_valtype` belongs to
//	                                            `check_func_body` (valid.ml:1021) rather than to this
//	                                            phase and is in funcBody
//	check_memory    defined memories          — this slice
//	check_table     defined tables            — this slice, `check_tabletype` whole: the element type
//	                                            and then the limits, in that order
//	check_global    globals                   — this slice, `check_globaltype` and `check_const`
//	                                            whole: `is_const`'s GlobalGet arm (#342) and
//	                                            `check_block`'s type check on the initializer (#328's
//	                                            Rule A)
//	check_data      data segments             — memory index, and `check_const` whole on the offset,
//	                                            typed against the memory's address type
//	check_elem      element segments          — `check_reftype` on the declared element type, the
//	                                            table index, the reftype match (#328's Rule C), and
//	                                            `check_const` whole on the offset and on every
//	                                            expression-form element
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
// `check_const` is one rule reached from **five** call sites, and a rule implemented at four of five
// is not a rule — *coverage is a claim*, so the claim is enumerated. Both halves now run at all five,
// and the columns are the **expected type** each site passes — the parameter #328 added and which had
// never been read by anything — and the *scope*, which is how many defined globals the fold has put
// in the context by the time the site is reached:
//
//	valid.ml:1058   global initializer          the global's declared type      scope = i
//	valid.ml:1070   table initializer           the table's element type        scope = 0
//	valid.ml:1078   data segment offset         the memory's address type       scope = len(m.Globals)
//	valid.ml:1094   element segment offset      the table's address type        scope = len(m.Globals)
//	valid.ml:1100   element segment elements    the segment's declared reftype  scope = len(m.Globals)
//
// The offset rows take their type from the *descriptor* rather than a fixed `i32`, and that is not
// cosmetic: with both hardcoded to `i32`, the all-on lane loses 58 passes and
// `TestModuleDefinitionsAskTheValidator` names the over-rejections (`address64.wast:3`,
// `table_copy64.wast:1746`, …) while the default lane stays green, memory64 being gated off there.
//
// **The scope column is now three distinct values, and the middle one is why this census earns its
// second column.** Every site sits at a different point in `check_module`'s fold, so "which globals
// can this expression read" is answered per site and not once for the file: a global sees the ones
// declared before it, a table sees none, and the segments see all of them. Three values that a
// single-table, single-global module cannot distinguish — see the tables loop for the vector that
// does.
//
// This row read `NO SUBJECT — see below` until #419, on the argument that a table's initializer was
// discarded by the decoder and so there was "no field in `binary.Table` for a rule here to read",
// arriving with the GC gate (#7). It was right about the field and wrong about the gate: the bare
// tabletype form is MVP, the reference *synthesizes* an initializer for it (`decode.ml:1058-1063`),
// so the site had a subject in every module on the board and what was missing was retention rather
// than a proposal. The gap is recorded as having been mis-attributed rather than quietly re-pointed,
// because a census whose absent row names the wrong cause tells the next reader to wait for a gate
// instead of writing four lines.
//
// The gate was not irrelevant, which is the part worth keeping from the sentence it replaces: every
// initializer that *does anything* is behind it. A default-lane table's expression is the synthesized
// `ref.null`, which types against the element type and reads no index, so this row's whole measurable
// effect is in the all-on lane — 10 passes, per the M12 row in `global_test.go`. A site with a subject
// in both lanes and a verdict in one is the honest description, and neither half of it is what the
// old note said.
func modulePre(m *binary.Module, refs map[uint32]bool) error {
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
			if err := checkTableType(len(m.Types), imp.Table); err != nil {
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
		case binary.ExternFunc:
			// `check_externtype`'s `ExternFuncT` arm (valid.ml:230-231):
			//
			//	| ExternFuncT ut -> let _ft = func_type c (idx_of_typeuse ut @@ at) in ()
			//
			// The type it resolves is discarded by the reference too — this phase asks only whether
			// the index names a function type, and the *linker* is what consumes the type later. Two
			// admissions, `func_ptrs.wast:49` and `imports.wast:100`, and the second is the one that
			// makes the rule worth stating precisely: its module declares one type and imports
			// `(func (type 1))`, so the index is in range of nothing and off by exactly one. A
			// bounds check written against `len(m.Types)+1`, or against the import count, passes it.
			if _, err := funcType(m, imp.Index); err != nil {
				return fmt.Errorf("import %d: %w", i, err)
			}
		case binary.ExternGlobal:
			// The last arm of `check_externtype`, which reaches `check_globaltype` — the whole of it
			// being `check_valtype` over the declared type (valid.ml:197-199). Enumerated rather than folded
			// into a default because a silent default here would absorb a *sixth* extern kind — a
			// future proposal's — as "checked", which is the under-matching predicate that fails by
			// construction.
			//
			// This arm was a stated admission waiting on "the `unknown type` stratum", which is this
			// slice, so it lands with it. Its own witness is thin: an imported `(ref $t)` global needs
			// the GC gate to decode, and the all-on lane has no row expecting `unknown type` from an
			// import descriptor. It is written because the rule is one line of the same rule three
			// other positions in this function now run, and *a rule implemented at four of five sites
			// is not a rule*.
			if err := checkValTypeScoped(len(m.Types), imp.GlobalType); err != nil {
				return fmt.Errorf("import %d: %w", i, err)
			}
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
	// `check_func` (valid.ml:1013-1016), which is `func_type c x` over each *declaration* — the type
	// index in the function section, checked one phase before the memories and eight before the body.
	//
	// **This rule has no vector and lands anyway, and the reason is position rather than verdict.**
	// `funcBody` already resolves the same index through the same function, so every module this loop
	// refuses was refused before; what changes is *which* message a module with two defects gets.
	// `(module (func (type 42)) (memory 0x1_0000_0000))` is `unknown type 42` in the reference and was
	// `memory size must be at most …` here. No `assert_invalid` on the board pairs those two defects,
	// so this is transcribed on the reference's authority alone — the same standing `moduleExports`'
	// two-loop split has, and stated the same way, because a rule justified by an ordering no
	// instrument can score is a rule whose PR must say so rather than claim a fix.
	for i := range m.Funcs {
		if _, err := funcType(m, m.Funcs[i].TypeIndex); err != nil {
			return fmt.Errorf("func %d: %w", i, err)
		}
	}
	for i := range m.Memories {
		if err := checkMemoryType(m.Memories[i]); err != nil {
			return fmt.Errorf("memory %d: %w", i, err)
		}
	}
	// check_table (valid.ml:1066-1071), both halves, in the reference's position: after the defined
	// memories, before check_global.
	//
	//	let Table (tt, const) = tab.it in
	//	let TableT (_at, _lim, rt) = tt in
	//	check_tabletype c tt tab.at;
	//	check_const c const (RefT rt);
	//
	// **The scope argument is 0, and it is not a stand-in for `i`.** `check_module`'s fold puts
	// `check_list check_table` *before* `check_list check_global` (:1160-1161) and `check_table`
	// folds nothing into `c.globals`, so the context a table initializer is typed in holds no
	// defined global at all — only the imported ones, which `globalTypeAt` counts separately from
	// this argument. A table's `global.get` therefore behaves unlike a global's:
	// `(module (global i32 (i32.const 0)) (table 1 funcref (global.get 0)))` is `unknown global 0`
	// even though the module declares a global 0 and the table is written after it, because index 0
	// there is the *defined* global and no defined global is in scope yet.
	//
	// **The board holds both directions, in one lane, and `0` versus `i` is still transcribed.** The
	// accept direction is `table.wast:102-103`, two initializers reading the
	// `(global $g (import "M" "g") (ref $dummy))` declared at `:94`, and it passes under every
	// candidate argument — imports are counted outside this one entirely. The reject direction is
	// `global.wast:674-680`, `(global $g funcref (ref.null func))` then
	// `(table $t 10 funcref (global.get $g))` asserted `unknown global`, which `len(m.Globals)`
	// here costs exactly one all-on pass. Both are all-on rows: a table that spells an initializer
	// encodes to the `0x40` form, gated behind GC in `decodeTableForm`, so the default lane's tables
	// all carry the synthesized `ref.null` and read no global at all.
	//
	// What no vector separates is `0` from `i`, every corpus module with a table initializer having
	// one table — where the two are the same number. That one is the fold order on the reference's
	// authority alone, and TestTableInitializerSeesNoDefinedGlobal is its witness, with the M12-M14
	// measurements beside it.
	for i := range m.Tables {
		t := &m.Tables[i]
		if err := checkTableType(len(m.Types), t.Type()); err != nil {
			return fmt.Errorf("table %d: %w", i, err)
		}
		// `RefT rt` is the tabletype's own reftype, which `binary.Table` holds as `ElemType` — so
		// the expected type is read off the descriptor exactly as the two offset sites read theirs,
		// and a table whose text spelled no initializer is typed against the `ref.null ht` the
		// decoder synthesized for it (`Table.Init`'s comment on why that field is never absent).
		if err := checkConst(m, refs, t.Init, t.InitCasts, t.ElemType, 0); err != nil {
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
		g := &m.Globals[i]
		// `check_globaltype c gt glob.at` at :1057, ahead of the initializer's `check_const` — so
		// `(global (ref null 7) (ref.null 7))` in a module with three types reports `unknown type 7`
		// from the descriptor and not from the expression, which would otherwise reach the same
		// verdict through `checkConst`'s own walk and name the instruction.
		if err := checkValTypeScoped(len(m.Types), g.Type); err != nil {
			return fmt.Errorf("global %d: %w", i, err)
		}
		if err := checkConst(m, refs, g.Init, g.InitCasts, g.Type, i); err != nil {
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
		// **`addrTypeAt` rather than `memoryExists`, and the discarded descriptor is the point** — the
		// same trade `tableTypeAt` next door already makes, and the reason is now the same too. The
		// reference destructures the memory type here (`let MemoryT (at, _) = memory c x`) because the
		// offset's expected type is derived from it: `check_const c offset (NumT (numtype_of_addrtype
		// at))`. So a `(memory i64 1)` takes an `i64` offset and an i32 memory an `i32`, and writing
		// `binary.I32` here would pass every default-lane vector and over-reject in the all-on one —
		// #343 cause 2's shape, one phase up from the instruction that earned it.
		//
		// The two functions' messages are identical by construction (see memoryExists on the shared
		// `(module declares …)` parenthetical), so this substitution moves no verdict; `memoryExists`
		// keeps its other caller in `exportExists`.
		at, err := addrTypeAt(m, d.MemIndex)
		if err != nil {
			return fmt.Errorf("data segment %d: %w", i, err)
		}
		// `check_const c offset (NumT (numtype_of_addrtype at))` at :1076, after the memory resolves —
		// the sequence the loop's own comment above relies on. Every defined global is in scope here,
		// unlike a global's initializer: `check_module` has already folded all of them in by the
		// time it reaches the data segments (:1161 before :1163), which is why this passes the full
		// count where the loop above passes `i`.
		if err := checkConst(m, refs, d.Offset, d.OffsetCasts, at, len(m.Globals)); err != nil {
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
		// `check_reftype c rt elem.at` (valid.ml:1099) — the segment's **declared** element type
		// resolves before anything else in the segment is looked at, which is why this line is above
		// the expression loop and not folded into the match below: a segment whose declared type names
		// a missing type *and* whose expressions are ill-typed reports the type.
		//
		// `check_reftype` is `check_heaptype` is `check_typeuse` (valid.ml:113-129), so the rule is the
		// same one `checkValTypeScoped` spells and the message is the same; `ElemType` is a `ValType`
		// holding a reftype here, which is the representation this package has used for the reftype
		// positions since the elem match landed.
		if err := checkValTypeScoped(len(m.Types), e.ElemType); err != nil {
			return fmt.Errorf("element segment %d: %w", i, err)
		}
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
		// **The expression form's half of that lookup is now the walk's own**, which is #328 removing a
		// duplicate rather than adding a rule: `refFuncIndices` scanned the expression for `ref.func`
		// and resolved each index beside a const check that did not type anything, and `check_block`'s
		// `RefFunc` arm does exactly that resolution — through `funcTypeIndexAt`, over the same
		// imports-then-definitions total — because it needs the type to push. So the scan and its
		// helper are gone from this branch and `elemFuncsInScope` keeps the branch it was written for,
		// the index form, where both of #391's vectors are. Two functions resolving one index space
		// agree until one of them learns something (#241), and the walk is the one that will.
		if e.ByExpr {
			for j := range e.Exprs {
				// `check_const c const (RefT rt)` at :1100, per element, against the segment's *own*
				// declared reftype and not the table's — the table's is `check_elemmode`'s comparison
				// below, and conflating them would score `elem.wast:983` against the wrong descriptor.
				// `ExprCasts` is built index-parallel to `Exprs` by the decoder, and the bounds
				// test is not that parallelism being doubted — it is what a *hand-built* module
				// gets, which is every module in this package's own tests. Out of range yields a
				// nil map, so a `ref.null` in such an expression reports
				// `errNoRefNullHeapType` rather than reading a neighbour's heaptype: the loud
				// failure, which is the right one for an engine-internal absence.
				var ec map[int][]binary.ValType
				if j < len(e.ExprCasts) {
					ec = e.ExprCasts[j]
				}
				if err := checkConst(m, refs, e.Exprs[j], ec, e.ElemType, len(m.Globals)); err != nil {
					return fmt.Errorf("element segment %d, element %d: %w", i, j, err)
				}
			}
		} else if err := elemFuncsInScope(m, e.Funcs); err != nil {
			// The index form, which the wat front end desugars into `ref.func` const exprs
			// (`declaredFuncs`'s own account of `decode.ml`'s `elem_index`) and this decoder keeps as
			// a plain index vector. Both forms therefore have to resolve, and both admissions above
			// arrive in *this* branch — a fix written only for the expression form would have passed
			// every test anyone thought to write and moved no column.
			//
			// **`check_const`'s type half is vacuous on this branch and is therefore not written**,
			// which is a claim about the wire and not a shortcut: the reference normalizes an index to
			// `[ref_func x]` and types it against `RefT rt`, and the only `rt` this branch can carry is
			// `funcref` — the elemkind byte admits nothing else (`decodeElemSegment`), and `ref.func`
			// always pushes a function reference. A check that cannot fail is worse than an absent one
			// because it reads as coverage, so what stands here is the lookup and a note of why its
			// sibling would say nothing. The nullability the reference distinguishes at *encode* time
			// is unrepresentable in this ValType, which ElemSegment's own comment records.
			return fmt.Errorf("element segment %d: %w", i, err)
		}
		if e.Mode != binary.ElemActive {
			continue
		}
		tab, err := tableTypeAt(m, e.TableIndex)
		if err != nil {
			return fmt.Errorf("element segment %d: %w", i, err)
		}
		// `require (match_reftype c.types t rt)` at :1091-1093 — the segment's element type against the
		// table's, the deferred half `tableTypeAt` was resolving a whole descriptor for. **It sits
		// between the table lookup and the offset's const check, and that position is the rule**: the
		// comment this replaces recorded the inversion its absence caused, a module with both a reftype
		// mismatch and a bad offset reporting the offset where the reference reports the match.
		//
		// `match_reftype` and not identity, because the relation is subtyping in one direction: a
		// `(ref $t)` segment may fill a `funcref` table and not the reverse. Two admissions,
		// `elem.wast:978` (an index-form segment, implicitly `(ref func)` — **not** `funcref`, which is
		// grave #360 and is what this comment said before it — against an `externref` table) and
		// `elem.wast:983` (an `externref` segment against a `funcref` table): one in each direction,
		// which is what makes them a witness for the relation rather than for a mismatch. Both refuse
		// either way round, so *these two vectors* do not discriminate identity from subtyping. The
		// corpus does, though, and the accept-direction unit control this slice pre-registered was
		// therefore **not written** — see `TestModuleDefinitionsAskTheValidator`, which #341 built to
		// score module definitions on the validator's answer. Measured, with this line replaced by
		// `e.ElemType != tab.ElemType`: the default lane reports 194 over-rejections and 4
		// wrong-message rows, and `passFloor` falls 60868 → 60670. §9 G-3's "no `assert_invalid` vector
		// can witness this" is still true and is no longer the same thing as "the board cannot".
		//
		// This rule going in is also what first compared a segment's element type to anything, which is
		// how the two decode-direction graves surfaced at all.
		//
		// The message is the reference's own sentence (valid.ml:1092-1093), which names both types
		// because a mismatch between two descriptors is unreadable without them.
		if !matchRefType(tctx{gotMod: m, wantMod: m}, e.ElemType, tab.ElemType) {
			return fmt.Errorf("element segment %d: %w: element segment's type %s does not match "+
				"table's element type %s", i, ErrTypeMismatch, e.ElemType, tab.ElemType)
		}
		// `check_const c offset (NumT (numtype_of_addrtype at))` at :1094, after `table c x` and after
		// the reftype match above. The offset's type comes off the table's address type, so a table64
		// takes an i64 offset — `tableAddrType` is the accessor the bulk operands already read it
		// through, reused rather than re-derived for `tableOp`'s stated reason.
		if err := checkConst(m, refs, e.Offset, e.OffsetCasts, tableAddrType(tab), len(m.Globals)); err != nil {
			return fmt.Errorf("element segment %d: %w", i, err)
		}
	}
	return nil
}

// elemFuncsInScope is `func c x` over an element segment's function indices — the lookup half of
// `check_elem`'s `check_const` (valid.ml:1100), and #391.
//
// **Its expression-form twin is deleted rather than kept, and it is the deletion that is worth the
// note.** `refFuncIndices` scanned a const expression for top-level `ref.func` immediates so this
// function could resolve them; `check_block`'s own `RefFunc` arm resolves the same index through
// `funcTypeIndexAt` because it needs the type it pushes, so once #328 ran the walk over element
// expressions the scan was a second answer to a question already answered.
//
// The deletion stands on that redundancy alone, and **not** on the reason first written here — that
// "both of #391's vectors are in the index form, which is the branch that survives, so the surviving
// call is the one with corpus rows behind it." Both vectors are in the *expression* form
// (`parser.mly:1215` with `encode.ml:1044-1046`; grave #401 is why our own parser said otherwise), so
// the corpus rows are refused by `checkConst` instead. Valid vectors do *reach* the loop below —
// `elem.wast:978`'s bare-offset sugar is the index form — but no vector is *refused* by it: neuter it
// and both lanes of the board stay green, measured, not reasoned. Its entire readership is one row in
// `elem_test.go`, which is why that row was added when the account was corrected.
//
// `indexInScope` rather than `funcTypeAt`: the reference resolves the *type* here because `check_block`
// then matches it, and on this branch there is no const expression for `check_block` to run over, so
// nothing consumes the type. The segment's element type is matched against its table's separately, in
// `check_elemmode` — and it is `(ref func)`, not `funcref` (grave #360).
func elemFuncsInScope(m *binary.Module, idxs []uint32) error {
	total := m.ImportedFuncs() + len(m.Funcs)
	for _, x := range idxs {
		if err := indexInScope(x, total, ErrUnknownFunc); err != nil {
			return err
		}
	}
	return nil
}

// checkConst is the reference's `check_const` (valid.ml:1041-1044), whole:
//
//	require (List.for_all (is_const c) const.it) const.at "constant expression required";
//	check_block c const.it (InstrT ([], [t], [])) const.at
//
// Two requirements in the reference's order, and **the order is the rule** rather than a tidiness:
// `(global i32 (global.get $mut) (i32.const 0))` has both defects — a mutable global in a constant
// position and two values where one is wanted — and the reference reports `constant expression
// required`, because the `for_all` predicate runs over the whole sequence before anything is typed.
// A single fused walk that typed as it went would report `type mismatch` on exactly the modules
// where both defects appear.
//
// # The second requirement is #328's, and it is one rule at four call sites
//
// The first half is `checkConstGlobals` (#342). The second — the type check — is what this function
// adds, and until it existed the expected type of a constant expression was never read by anything:
// a global initializer's declared type, a data segment offset's address type, an element segment
// offset's, and an element expression's reftype were all retained and all unasked. 23 admissions
// across five files, and they are admissions rather than declines for the reason `validate.go`'s
// header gives: a rule attached to something the code-section walk never visits has no instruction to
// refuse, so the module is *accepted*, which no board scoring only refusals can see (§9 G-3).
//
// # `definedInScope` is threaded through to the walk, not just to the first half
//
// It was `checkConstGlobals`' parameter alone, and the type check needs the same number for the same
// reason: the walk's own `global.get` arm resolves an index, and resolving it at full scope makes a
// global's initializer able to name itself. That is what `validator.globalScope` is, and it is why
// this function takes the count rather than reading `len(m.Globals)` one layer down.
func checkConst(m *binary.Module, refs map[uint32]bool, expr []binary.Instr,
	casts map[int][]binary.ValType, want binary.ValType, definedInScope int,
) error {
	if err := checkConstGlobals(m, expr, definedInScope); err != nil {
		return err
	}
	return typeConstExpr(m, refs, expr, casts, want, definedInScope)
}

// typeConstExpr is `check_block c const.it (InstrT ([], [t], []))` — the same typing walk a function
// body gets, over an expression whose declared result is one value.
//
// **The walk is shared with function bodies rather than reimplemented, and that is the whole design
// choice here.** A dedicated const-expr typer would be a second implementation of the operand stack,
// the subtyping comparison and the block-end arity check, differing from the first one the day either
// learns something — which is the divergence #394 converged one rule down, and the reference's own
// answer: `check_const` calls `check_block`, the function every body goes through.
//
// What that buys, concretely, is that the const-expr sites inherit rules nobody wrote for them:
//
//   - `ref.func`'s index resolution, which is `ref_func.wast:68` (`unknown function 7`) — the arm
//     already resolves through `funcTypeIndexAt` and had no const expression to run on.
//   - the *empty* expression and the *two-value* expression, both of which are `endBlock`'s
//     `popExpectAll` and `expectEmptyFrame` and neither of which is a rule about constants.
//   - the subtyping comparison, so `(global funcref (ref.func 0))` is valid — the walk pushes the
//     function's own `(ref $t)` and `matches` accepts it under `funcref`. An identity comparison would
//     refuse a valid module, and **this slice pre-registered a unit control for that direction on the
//     premise that nothing else could see it, which measurement falsified.** With `matchRefType`
//     reduced to `got == want`, the default lane's over-rejection list names the sites: two globals
//     (`ref_func.wast:6`, `:80`) and one element expression (`global.wast:3`), each with
//     `requires [funcref] but stack has [(ref 0)]`, out of 399 rows. #341 is why — it scores a bare
//     `(module …)` on the validator's answer instead of the text reader's, so an over-rejection is a
//     board movement now. The controls were dropped rather than written: **a pre-registration is a
//     forecast about the instruments, and #341 changed the instruments under it.**
//
// The expression carries its own terminating `end` (`constExprBody` emits one, like a function
// body's), so the frame is closed by the walk and `endBlock` is what checks the result arity. The
// frames-empty check below is `funcBody`'s, for the reason recorded there: the arity check lives at
// `end` alone, and what has no other home is the structural question.
//
// # `curFunc` is nil, and the fourth side table had to be plumbed for that to be safe
//
// `locals` is empty and `curFunc` nil because a constant expression can hold neither a `local.get`
// nor a `select t` annotation, a `br_table` vector or a `try_table` clause list — the decoder's
// const-expr table refuses every opcode that would read one (`internal/binary/instr.go`). That is a
// precondition supplied by another package, and it is named rather than defended with a nil check,
// because no input this package can construct reaches those three arms.
//
// **`ref.null` is the exception, and the paragraph above stood here in a revision that claimed all
// four.** `ref.null` *is* const-legal, its arm reads `Casts`, and `Casts` hung off `Func` — so the
// first run of this walk over the corpus was a nil-pointer panic inside `refNull`, on the very first
// global initializer holding one. That is #361, whose declared discharge was this consumer arriving;
// the map is now a parameter, and `validator.castVector` is what reads it from either side. Recorded
// at this length because the falsified sentence was a comment asserting the property its own code
// lacked — the shape that makes review confirm the bug — and the thing that caught it was running
// the board rather than re-reading the claim.
func typeConstExpr(m *binary.Module, refs map[uint32]bool, expr []binary.Instr,
	casts map[int][]binary.ValType, want binary.ValType, definedInScope int,
) error {
	v := &validator{
		mod:         m,
		refs:        refs,
		casts:       casts,
		blocks:      map[int]Arity{},
		globalScope: definedInScope,
	}
	// The expression's own frame, typed as a block with no parameters and one result — the
	// `InstrT ([], [t], [])` the reference passes. Both label and end types are `[want]`, which is
	// `funcBody`'s shape and correct for the same reason: there is no `br` in a constant expression,
	// so the label list is unobservable and giving it the results keeps the one frame consistent
	// rather than inventing a second convention for it.
	v.pushFrame(opFuncBody, []binary.ValType{want}, []binary.ValType{want})
	if err := v.instrs(expr); err != nil {
		return err
	}
	if len(v.frames) != 0 {
		return fmt.Errorf("%w: %d block(s) still open at the end of a constant expression",
			ErrTypeMismatch, len(v.frames))
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
// expected result type. This is the first, and only the GlobalGet arm of the first; the second is
// `typeConstExpr` and the two are composed by `checkConst`, which is the function every call site
// now goes through. **The sentence above read "only the first is this slice's" and that clause is
// #328's**, retired rather than deleted because which half was owed and for how long is the durable
// part: the type check was owed from #342 until #328, and 23 vectors were accepted in the interval.
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

// moduleStart is `check_start`, whole (valid.ml:1110-1114):
//
//	let Start x = start.it in
//	let ft = functype_of_comptype (expand_deftype (func c x)) in
//	require (ft = ([], [])) start.at
//	  "start function must not have parameters or results"
//
// Three steps, and the *first* is the one with a corpus vector: `func c x` is the index-space lookup,
// so `(module (func) (start 1))` is `unknown function 1` and never reaches the `require`. See
// ErrStartFunction for why that makes this rule two vectors rather than three.
//
// **Its position is `Option.iter (check_start c) m.it.start` at valid.ml:1166 — after every function
// body, before the exports — and the position is observable.** A module with an ill-typed body and a
// start function taking a parameter reports the body; one with a bad start and an unknown export
// index reports the start. Whether the corpus can see either is a separate question from whether it
// is the rule, and it cannot: no vector carries two defects across this seam. So the ordering is
// transcribed on the reference's authority, which is `moduleExports`' arrangement exactly and is
// argued at length there.
//
// The `Option.iter` is `m.HasStart`, which is why an absent start section is a `nil` from this
// function rather than a zero index checked against something — see `binary.Module.HasStart`, and
// `context.startFunc` in `internal/text` for the same two-fields decision on the writing side.
func moduleStart(m *binary.Module) error {
	if !m.HasStart {
		return nil
	}
	ti, err := funcTypeIndexIn(m, m.Start)
	if err != nil {
		return err
	}
	ft, err := funcType(m, ti)
	if err != nil {
		return err
	}
	if len(ft.Params) != 0 || len(ft.Results) != 0 {
		return fmt.Errorf("%w: start function %d has type %v -> %v",
			ErrStartFunction, m.Start, ft.Params, ft.Results)
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
