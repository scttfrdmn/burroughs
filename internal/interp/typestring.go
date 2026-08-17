// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// This file is `third_party/spec/interpreter/syntax/types.ml`'s `string_of_*` family, as far as
// `string_of_externtype` — the spelling of the two types in the linker's `incompatible import
// type` message.
//
// # Why it exists, which is a testimony argument and not a formatting one
//
// The message it replaces printed **type indices**:
//
//	expected func [(ref 4)] -> [], got func [(ref 3)] -> []
//	expected global const funcref, got global const (ref func)
//
// Both rows are #368. The first is a witness with no subject: `4` and `3` are indices into two
// *different* type sections, so a reader cannot tell whether they name the same type — and the
// engine had just answered that question, by comparing the numbers, wrongly. The second is worse,
// because it reads as a real difference and is not: `funcref` and `(ref func)` differ only in
// nullability, which is what the comparison should have turned on, and the reference spells both
// through `string_of_reftype` so the null bit is the visible fact rather than a spelling accident.
//
// *An error message is testimony*: once the matcher compares the types an index names, the message
// has to name them too, or the record of a refusal cites evidence the reader cannot resolve and
// the engine cannot be checked against the reference on the one channel a human reads.
//
// # The rolled form is the reason this is a renderer and not a Sprintf
//
// `string_of_deftype` is `DefT (RecT [st], 0l) -> string_of_subtype st` and otherwise
// `"(" ^ string_of_rectype rt ^ ")." ^ i` — so a member of a multi-member rec group prints its
// *whole group* and then its ordinal, and a reference back into that group prints `rec.N` rather
// than recursing. That is exactly the structure `sameDefType` compares (rec extent plus ordinal),
// which is what makes this rendering the message form of the fix rather than decoration: the two
// strings differ precisely when the comparison says the types do.

// speller renders types read in one module's type space, in the reference's own spellings.
//
// A type with the module on it rather than free functions taking `(mod, x)` pairs, because every
// one of the fifteen productions below needs the same module and the whole hazard this file exists
// to remove is a type printed against the wrong type section. One field, set once per side.
type speller struct {
	mod *binary.Module
}

// spellDepth bounds the recursion through cross-group type references.
//
// The reference needs no bound: its `deftype` values are finite rolled trees, cycles already
// having become `Rec` ordinals. This port resolves indices instead, and a *cross*-group reference
// resolves to another group — which in a well-scoped module is an earlier one, so the walk
// descends, by a rule that lives in the validator and is not this function's to assume (the same
// reasoning `matchDeclaredSupertypes`'s bound carries). A malformed or hostile module must not
// hang a message.
//
// At the bound the renderer degrades to the reference's *other* typeuse arm — `Idx x`, a bare
// index — rather than to an invented marker. An honest degradation names a real production: a
// reader sees an unresolved index and knows it is unresolved, which is what the old message
// printed for every type and this one prints only where the depth ran out.
const spellDepth = 8

// externFunc is `string_of_externtype (ExternFuncT ut)`: `"func " ^ string_of_typeuse ut`.
func (s speller) externFunc(x uint32) string { return "func " + s.typeuse(x, recSpan{}, 0) }

// externTag is `string_of_externtype (ExternTagT tt)` through `string_of_tagtype`, which is
// `string_of_typeuse` of the tag's deftype.
func (s speller) externTag(x uint32) string { return "tag " + s.typeuse(x, recSpan{}, 0) }

// externGlobal is `"global " ^ string_of_globaltype`, whose mutability is a *wrapper* and not a
// prefix word: `string_of_mut s Var = "(mut " ^ s ^ ")"` and `Cons = s`. The message this replaces
// wrote `const`/`mut` as words, which is a spelling the reference does not have.
func (s speller) externGlobal(mutable bool, t binary.ValType) string {
	return "global " + mut(s.valtype(t, recSpan{}, 0), mutable)
}

// externMemory is `"memory " ^ string_of_memorytype`, which is `string_of_addrtype at ^ " " ^
// string_of_limits lim`.
//
// **The address type is in the message because it is in the comparison** — `match_memorytype` is
// `at1 = at2 && match_limits c lim1 lim2` — and it was in neither. A message that omitted it
// could not have reported the eight `memory64-imports.wast` rows the check was missing even if the
// check had been there.
func (s speller) externMemory(lim binary.Limits) string {
	return "memory " + addrtype(lim) + " " + limitsString(lim)
}

// externTable is `"table " ^ string_of_tabletype`: address type, limits, element reftype.
func (s speller) externTable(lim binary.Limits, elem binary.ValType) string {
	return "table " + addrtype(lim) + " " + limitsString(lim) + " " +
		s.valtype(elem, recSpan{}, 0)
}

// recSpan is the rec group whose members' references render as `rec.N` — the rendering side of
// `validate`'s recScope, and the same fact: `roll_deftypes` rewrites references into the group
// being defined as ordinals.
//
// The zero value is the empty span, which is what a top-level call passes: nothing is in scope
// until `deftype` opens a group.
type recSpan struct {
	start, length uint32
}

func (r recSpan) holds(x uint32) bool { return x >= r.start && x < r.start+r.length }

// typeuse is `string_of_typeuse` (types.ml:331-334), whose three arms are the whole of this
// renderer's structure:
//
//	| Idx x -> string_of_idx x          the depth-bound degradation
//	| Rec x -> "rec." ^ x               a reference into the group being defined
//	| Def dt -> "(" ^ deftype ^ ")"     a reference out of it
func (s speller) typeuse(x uint32, scope recSpan, depth int) string {
	if scope.holds(x) {
		return "rec." + strconv.FormatUint(uint64(x-scope.start), 10)
	}
	if depth >= spellDepth {
		return strconv.FormatUint(uint64(x), 10)
	}
	return "(" + s.deftype(x, depth) + ")"
}

// deftype is `string_of_deftype` (types.ml:398-400) composed with `string_of_rectype`
// (types.ml:392-396). A singleton group prints as its one subtype; any other group prints
// `"(" ^ "rec " ^ ("(" subtype ")")… ^ ")." ^ ordinal`, so the `(rec …)` head and the
// per-member parentheses below are the two functions' spellings joined, not this port's own.
func (s speller) deftype(x uint32, depth int) string {
	ct, ok := s.compTypeAt(x)
	if !ok {
		// An index the module does not have. Rendered as the bare index rather than as an
		// error, for `compTypeAt`'s reason one package over: index validity is the validator's
		// question, and a message is not the place to raise it a second time.
		return strconv.FormatUint(uint64(x), 10)
	}
	scope := recSpan{ct.RecStart, ct.RecLen}
	if ct.RecLen == 1 {
		return s.subtype(ct, scope, depth)
	}
	var b strings.Builder
	b.WriteString("(rec")
	for i := range ct.RecLen {
		m, okM := s.compTypeAt(scope.start + i)
		if !okM {
			continue
		}
		b.WriteString(" (")
		b.WriteString(s.subtype(m, scope, depth))
		b.WriteString(")")
	}
	b.WriteString(").")
	b.WriteString(strconv.FormatUint(uint64(x-scope.start), 10))
	return b.String()
}

// subtype is `string_of_subtype` (types.ml:385-390): a final comptype with no declared supertypes
// prints as the comptype alone, and anything else prints its `sub` header first.
//
// `binary.CompType.Final` is true for a bare comptype as well as for an explicit `sub final`,
// which is the grammar's own default (`SubT (Final, [], ct)`) — so the first arm is reached by
// exactly the types the reference reaches it with.
func (s speller) subtype(ct binary.CompType, scope recSpan, depth int) string {
	if ct.Final && len(ct.Supertypes) == 0 {
		return s.comptype(ct, scope, depth)
	}
	parts := make([]string, 0, len(ct.Supertypes)+1)
	head := "sub"
	if ct.Final {
		head += " final"
	}
	parts = append(parts, head)
	for _, sup := range ct.Supertypes {
		parts = append(parts, s.typeuse(sup, scope, depth+1))
	}
	return strings.Join(parts, " ") + " (" + s.comptype(ct, scope, depth) + ")"
}

// comptype is `string_of_comptype` (types.ml:376-383).
func (s speller) comptype(ct binary.CompType, scope recSpan, depth int) string {
	switch ct.Kind {
	case binary.CompStruct:
		if len(ct.Fields) == 0 {
			return "struct"
		}
		parts := make([]string, 0, len(ct.Fields))
		for _, f := range ct.Fields {
			parts = append(parts, "(field "+s.fieldtype(f, scope, depth)+")")
		}
		return "struct " + strings.Join(parts, " ")
	case binary.CompArray:
		// One fieldtype by the grammar; a length check rather than an unguarded index, for
		// matchCompType's reason — a message is not allowed to panic either.
		if len(ct.Fields) != 1 {
			return "array"
		}
		return "array " + s.fieldtype(ct.Fields[0], scope, depth)
	default:
		return "func " + s.resulttype(ct.Func.Params, scope, depth) + " -> " +
			s.resulttype(ct.Func.Results, scope, depth)
	}
}

// fieldtype is `string_of_fieldtype`: the storage type, wrapped by mutability.
func (s speller) fieldtype(f binary.FieldType, scope recSpan, depth int) string {
	return mut(s.storagetype(f.Storage, scope, depth), f.Mutable)
}

// storagetype is `string_of_storagetype`, whose packed arm is `string_of_packtype` — `i8`/`i16`.
func (s speller) storagetype(st binary.StorageType, scope recSpan, depth int) string {
	if st.Packed {
		return "i" + strconv.FormatUint(uint64(st.Width), 10)
	}
	return s.valtype(st.Val, scope, depth)
}

// resulttype is `string_of_resulttype` (types.ml:361-362): bracketed, space separated, and the
// brackets are present for the empty sequence — `[]`, which is the whole reason it is not a bare
// join.
func (s speller) resulttype(ts []binary.ValType, scope recSpan, depth int) string {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, s.valtype(t, scope, depth))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// valtype is `string_of_valtype` (types.ml:355-359) over this engine's fused representation.
//
// **Every reference form prints as `string_of_reftype` (types.ml:352-353), which has one arm and is
// therefore always `(ref [null ]ht)` — there is no second spelling to pick.** The
// reference has no `funcref` or `externref` spelling at all — those are text-format abbreviations
// — so `binary.ValType.String()`, which prints them, is not usable here: it renders `(ref null
// func)` and `(ref func)` as `funcref` and `(ref func)`, two spellings whose difference reads as a
// change of *sort* where the real difference is one null bit. That was the fourth of #368's rows.
func (s speller) valtype(t binary.ValType, scope recSpan, depth int) string {
	if !t.IsRef() {
		return t.String()
	}
	null := ""
	if t.Null() {
		null = "null "
	}
	return "(ref " + null + s.heaptype(t, scope, depth) + ")"
}

// heaptype is `string_of_heaptype` (types.ml:336-350): a name for the twelve abstract forms, a
// typeuse for the indexed one.
func (s speller) heaptype(t binary.ValType, scope recSpan, depth int) string {
	if t.IsIndexed() {
		return s.typeuse(t.Index(), scope, depth+1)
	}
	kind, ok := t.Kind()
	if !ok {
		return t.String()
	}
	if name, okName := binary.HeapTypeName(kind); okName {
		return name
	}
	return t.String()
}

func (s speller) compTypeAt(x uint32) (binary.CompType, bool) {
	if s.mod == nil || x >= uint32(len(s.mod.Types)) {
		return binary.CompType{}, false
	}
	return s.mod.Types[x], true
}

// mut is `string_of_mut` (types.ml:314-316): a wrapper, not a prefix word.
func mut(s string, mutable bool) string {
	if mutable {
		return "(mut " + s + ")"
	}
	return s
}

// addrtype is `string_of_addrtype`, which is `string_of_numtype (numtype_of_addrtype at)` — so an
// address type prints as `i32` or `i64`, the numeric type it indexes with, and not as the word
// "memory64". `binary.Limits.Addr64` is where this engine keeps the bit (see its field comment for
// why it lives on the limits).
func addrtype(lim binary.Limits) string {
	if lim.Addr64 {
		return "i64"
	}
	return "i32"
}

// limitsString is `string_of_limits` (types.ml:402-405): the minimum, and the maximum only when
// there is one.
func limitsString(lim binary.Limits) string {
	if lim.HasMax {
		return fmt.Sprintf("%d %d", lim.Min, lim.Max)
	}
	return strconv.FormatUint(lim.Min, 10)
}
