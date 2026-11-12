// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// Component-type decoding (PR C / C.1, ADR 0084 §6). The loader framed the type section and B.1 read
// types only to canon's depth; the value-marshaling path (C.2) needs function types resolved to their
// parameter and result value types, so the type section now parses — to the guest-driven depth of the
// ten import signatures and the types they reach, per `Binary.md` @ 2e7895c. A type form beyond that
// (async func, nested component type, map/stream/future, fixed-length list) refuses at parse by name,
// and the section is consumed exactly, as elsewhere.

// ValKind is a component value type's discriminant (the defvaltype grammar).
type ValKind uint8

const (
	VBool ValKind = iota
	VS8
	VU8
	VS16
	VU16
	VS32
	VU32
	VS64
	VU64
	VF32
	VF64
	VChar
	VString
	VErrorContext
	VList
	VRecord
	VVariant
	VTuple
	VFlags
	VEnum
	VOption
	VResult
	VOwn
	VBorrow
	VRef    // a typeidx reference to a defined type
	VStream // (stream t?) — async surface, recognized not modeled (gate:async, ADR 0086)
	VFuture // (future t?) — async surface, recognized not modeled
	// VUnresolvedAlias marks an outer type alias inside an instance type that names no type this reader can
	// resolve (instanceAlias): a scope it was not given, or an ordinal naming an import or alias rather than
	// a parsed type. Since #756 a resolvable alias is inlined instead, so this marks only what is left. It
	// is DISTINCT from VRef so it cannot be mistaken for a legitimate type reference, and so a func signature
	// carrying it refuses by name rather than crashing (the old placeholder was a self-referential VRef{0}
	// that overflowed resolveVal, #753) or silently mis-lowering. resolveVal also returns it when it detects
	// a VRef cycle. It is refused where a value of it would be marshaled.
	VUnresolvedAlias
)

// ValType is a decoded component value type. Only the fields meaningful to its Kind are set; a
// compound holds its element/case/field types inline, own/borrow/ref hold a type index.
type ValType struct {
	Kind   ValKind
	Ref    uint32     // VRef / VOwn / VBorrow: the referenced type index
	Elem   *ValType   // VList / VOption / VFuture / VStream
	Fields []NamedVal // VRecord
	Cases  []VarCase  // VVariant
	Elems  []ValType  // VTuple
	Labels []string   // VFlags / VEnum
	Ok     *ValType   // VResult (nil if absent)
	Err    *ValType   // VResult (nil if absent)
	// Resource is a VOwn / VBorrow's resource identity, filled in by resolveVal when Ref reaches a named
	// resource type (ADR 0118): `interface@version::resource` once bound to an import, the form `typeStep`
	// builds and `lookupHost` reads. "" for a guest-defined resource, which keeps only its local index
	// until #1251. See selfResourceID for the unbound form.
	Resource string
}

// NamedVal is a labelled value type (a record field / a function parameter).
type NamedVal struct {
	Name string
	Type ValType
}

// VarCase is one variant case: a label and an optional payload.
type VarCase struct {
	Name string
	Type *ValType // nil for a payload-less case
}

// FuncType is a component function type: named params and an optional single result. Async marks a
// `0x43` async functype — recognized (decoded like a sync functype) so the type section survives, and
// treated as async surface the gate:async refusal names (ADR 0086).
type FuncType struct {
	Params []NamedVal
	Result *ValType // nil = no result
	Async  bool     // a 0x43 async functype
}

// TypeDefKind is which of the four top-level type-definition forms a TypeDef is.
type TypeDefKind uint8

const (
	TDVal TypeDefKind = iota
	TDFunc
	TDInstance
	TDResource
)

// InstanceType is a component instance type: the export names it declares with the type each names, in
// its own nested type space (the declarations that build that space are decoded but only exports are
// retained — the marshaling reaches functions through an instance's export names, and a `use` reaches a
// type through its type export).
type InstanceType struct {
	Exports []InstExport
	// Types are the instance type's type exports, each resolved against its local space so it can be
	// copied out of it (ADR 0118). A top-level `alias export $inst "x" (type)` of an imported instance
	// resolves to the entry named x.
	Types []InstTypeExport
}

// InstExport is one export declaration of an instance type: a name and the func type it names (nil for
// exports that are not functions — resources, types — which the marshaling does not reach).
type InstExport struct {
	Name string
	Func *FuncType
}

// InstTypeExport is one type export of an instance type: `(export "x" (type (eq N)))`, whose Def is the
// definition N names, or `(export "x" (type (sub resource)))`, a TDResource whose identity is
// selfResourceID(x) until the instance type is bound to an import.
type InstTypeExport struct {
	Name string
	Def  TypeDef
}

// TypeDef is one entry in the component type index space.
type TypeDef struct {
	Kind TypeDefKind
	Val  ValType
	Func *FuncType
	Inst *InstanceType
	// Resource is a TDResource's identity, the form ValType.Resource carries (ADR 0118); "" for a resource
	// the component defines itself.
	Resource string
}

// selfResourceID is the identity of a resource an instance type declares with `(export "x" (type (sub
// resource)))`, before the instance type is bound to an import: "::x". Which interface the resource belongs
// to is the import's name, which the instance type does not know, since one instance type can type several
// imports. qualifyResourceID completes it when the import is known.
//
// The empty interface cannot be confused with a bound identity: a component import's name is never empty.
func selfResourceID(name string) string { return "::" + name }

// qualifyResourceID completes an unbound identity with the interface it was imported as, and leaves a bound
// one (`wasi:io/error@0.2.3::error`, reached through an outer alias) as it is.
func qualifyResourceID(id, iface string) string {
	if strings.HasPrefix(id, "::") {
		return iface + id
	}
	return id
}

// parseTypes decodes the component type section into c.Types, appending in stream order. Each entry is
// dispatched on its opcode: a function type, an instance type, a resource type, or a value type.
func (c *Component) parseTypes(body []byte) error {
	// The component's own type space is the scope an instance type's `alias outer 1` names (#756). It is
	// read live, so an alias resolves against every type defined before it, which is all an outer alias
	// may name.
	r := &reader{b: body, outer: []typeAt{c.sectionTypeAt()}}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: type count: %w", err)
	}
	for range n {
		td, err := r.typeDef()
		if err != nil {
			return fmt.Errorf("component: type %d: %w", len(c.Types), err)
		}
		c.Types = append(c.Types, td)
		c.def(SpaceType, SectionType, len(c.Types)-1)
	}
	return endOf("type", r, len(body))
}

// typeDef reads one type definition, dispatching on the leading opcode byte.
func (r *reader) typeDef() (TypeDef, error) {
	op, err := r.byte()
	if err != nil {
		return TypeDef{}, err
	}
	switch op {
	case 0x40: // functype
		ft, err := r.funcType()
		return TypeDef{Kind: TDFunc, Func: ft}, err
	case 0x42: // instancetype
		it, err := r.instanceType()
		return TypeDef{Kind: TDInstance, Inst: it}, err
	case 0x3f: // resourcetype: a core:valtype rep + optional core:funcidx dtor
		if _, err := r.byte(); err != nil { // rep (a core valtype, one byte for i32/i64/...)
			return TypeDef{}, err
		}
		present, err := r.byte()
		if err != nil {
			return TypeDef{}, err
		}
		switch present {
		case 0x00:
		case 0x01:
			if _, err := r.u32(); err != nil {
				return TypeDef{}, err
			}
		default:
			return TypeDef{}, fmt.Errorf("resourcetype dtor flag %#x is not 0x00/0x01", present)
		}
		return TypeDef{Kind: TDResource}, nil
	case 0x41: // componenttype (nested) — not reached by the ten import signatures
		return TypeDef{}, fmt.Errorf("component: nested component type (0x41) not modeled this slice")
	case 0x43: // async functype — recognized (gate:async, ADR 0086): decoded like a functype, marked async
		ft, err := r.funcType()
		if err != nil {
			return TypeDef{}, err
		}
		ft.Async = true
		return TypeDef{Kind: TDFunc, Func: ft}, nil
	default:
		// a defvaltype opcode (a negative SLEB single byte): the value type forms.
		vt, err := valTypeFromOpcode(r, op)
		return TypeDef{Kind: TDVal, Val: vt}, err
	}
}

// resultList reads a `resultlist`: `0x00 valtype` for one result, or `0x01 0x00` for none. It returns nil
// for the empty form.
//
// # One decoder, because two was the defect (grave #885)
//
// This encoding appears in **two** places — a `functype`'s result and `canon task.return`'s operand — and
// until this slice each read it separately. `funcType` handled both arms; `canon task.return` handled only
// `0x00`, so **an async-lifted export returning nothing could not be loaded at all**: a guest declaring
// `export go: async func()` was refused at instantiate with *"resultlist discriminant 0x1 is not 0x00"*.
//
// The duplication is the defect and the missing case was its symptom, so the repair is this function and
// not a second copy of the switch. The search is stated on the grave: exactly two sites decode a result
// list, and there is no third — `grep -rn "resultlist\|result list"` over the package plus a pass over
// every `valType()` call site, to catch a site that decodes the shape without using the word.
//
// **The error messages name `resultlist` and not the caller**, deliberately: one decoder cannot honestly
// say which construct it is inside, and a message that guessed would be worse than one that does not. The
// caller's own context is what wraps it — `canon 0:` and `nested component 1:` were already in the
// refusal a reader saw.
func (r *reader) resultList() (*ValType, error) {
	disc, err := r.byte()
	if err != nil {
		return nil, err
	}
	switch disc {
	case 0x00:
		vt, err := r.valType()
		if err != nil {
			return nil, err
		}
		return &vt, nil
	case 0x01:
		if b, err := r.byte(); err != nil {
			return nil, err
		} else if b != 0x00 {
			return nil, fmt.Errorf("resultlist 0x01 not followed by 0x00 (got %#x)", b)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("resultlist discriminant %#x is not 0x00/0x01", disc)
	}
}

// funcType reads a `functype` body (after the 0x40): a param vec of labelled value types, then a
// result list — see resultList, which `canon task.return` reads through too.
func (r *reader) funcType() (*FuncType, error) {
	params, err := r.namedValVec()
	if err != nil {
		return nil, err
	}
	res, err := r.resultList()
	if err != nil {
		return nil, err
	}
	return &FuncType{Params: params, Result: res}, nil
}

// instanceType reads an `instancetype` body (after the 0x42): a vec of instancedecls. The nested type
// space is decoded so exportdecls resolve; only the export declarations (name → func type) are
// retained, since the marshaling reaches functions through an instance's exports.
func (r *reader) instanceType() (*InstanceType, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	inst := &InstanceType{}
	var local []TypeDef // the instance's nested type space
	for range n {
		disc, err := r.byte()
		if err != nil {
			return nil, err
		}
		switch disc {
		case 0x00: // core:type — one core type opcode; skip its body to canon's depth is not needed here
			return nil, fmt.Errorf("instancedecl core:type (0x00) not modeled this slice")
		case 0x01: // type
			// A type declared inside this instance type sees this instance's local space as its `outer 1`.
			r.outer = append(r.outer, sliceTypeAt(local))
			td, err := r.typeDef()
			r.outer = r.outer[:len(r.outer)-1]
			if err != nil {
				return nil, err
			}
			local = append(local, td)
		case 0x02: // alias — an outer/export type alias within the instance type
			if err := r.instanceAlias(&local); err != nil {
				return nil, err
			}
		case 0x04: // exportdecl: nameattributes + externtype
			name, err := r.name()
			if err != nil {
				return nil, err
			}
			ie, isType, err := r.exportDecl(name, &local)
			if err != nil {
				return nil, err
			}
			if isType {
				// The definition the export introduced is the one exportDecl just appended to local.
				inst.Types = append(inst.Types, InstTypeExport{Name: name, Def: local[len(local)-1]})
			}
			inst.Exports = append(inst.Exports, ie)
		default:
			return nil, fmt.Errorf("instancedecl discriminant %#x not modeled this slice", disc)
		}
	}
	// Resolve each exported function's parameter and result types against the instance's local type
	// space, so the retained signature is concrete (the shape the marshaling needs) rather than a set of
	// local type-index references.
	for i := range inst.Exports {
		if inst.Exports[i].Func != nil {
			inst.Exports[i].Func = resolveFunc(inst.Exports[i].Func, sliceTypeAt(local))
		}
	}
	// The type exports likewise, since a top-level alias copies one out of this space, where its local
	// ordinals would name whatever sits at the same position (typeAt's wrong-type-that-exists).
	//
	// NEUTER THIS loop, and TestAUsedCompoundTypeIsResolvedInItsOwnScope reads the record's field against the
	// wrong scope.
	for i := range inst.Types {
		inst.Types[i].Def = inlineTypeDef(inst.Types[i].Def, sliceTypeAt(local))
	}
	return inst, nil
}

// resolveFunc inlines a function type's parameter and result value types against a type space.
//
// **`Async` is carried through.** It was dropped while the only caller was the instance-export path,
// where no caller read it back off a resolved signature; the lift path added in #903 does, and a
// signature that forgot it was an async functype would be a different type than the one decoded.
//
// One resolve, so one size budget, covers the parameters and the result together (resolveState).
func resolveFunc(ft *FuncType, at typeAt) *FuncType {
	st := newResolveState()
	out := &FuncType{Params: make([]NamedVal, len(ft.Params)), Async: ft.Async}
	for i, p := range ft.Params {
		out.Params[i] = NamedVal{Name: p.Name, Type: resolveVal(p.Type, at, st)}
	}
	if ft.Result != nil {
		r := resolveVal(*ft.Result, at, st)
		out.Result = &r
	}
	return out
}

// maxResolvedTypeSize is the most nodes one resolve may build, wasmparser's `limits::MAX_WASM_TYPE_SIZE`
// (1_000_000 in wasmparser 0.258.3, which wasm-tools 1.258.0 uses, and in 0.262.0). wasmparser bounds a
// type's size by it; this engine inlines references, so the same bound is applied to the inlined form.
const maxResolvedTypeSize = 1_000_000

// resolveState is one resolve's state (grave #1256): the VRef indices on the current chain, for the cycle
// check, and the nodes the resolve may still build.
type resolveState struct {
	seen map[uint32]bool
	left int
}

func newResolveState() *resolveState {
	return &resolveState{seen: map[uint32]bool{}, left: maxResolvedTypeSize}
}

// typeAt resolves a type index to its definition, reporting whether the index names a parsed one.
//
// **It is a function and not a slice because the two index spaces this engine resolves against are not
// interchangeable, and indexing the wrong one is silently wrong rather than out of range.** An instance
// type's VRef is an ordinal into that instance's own local type space, which is a plain slice built in
// declaration order. A *top-level* VRef is an ordinal into the component's type-index **space**, of which
// `c.Types` is a compacted subset — aliases and instance-local sub-types also grow the space without
// appending to the section, so `c.Types[ref]` for any ordinal past the first interleaved alias names a
// *different type that exists*. That is the failure mode a slice parameter cannot prevent and a lookup
// can: each caller supplies the lookup for the space its indices belong to.
type typeAt func(uint32) (TypeDef, bool)

// sliceTypeAt is the lookup for a local type space indexed directly — an instance type's own.
func sliceTypeAt(local []TypeDef) typeAt {
	return func(i uint32) (TypeDef, bool) {
		if int(i) >= len(local) {
			return TypeDef{}, false
		}
		return local[i], true
	}
}

// sectionTypeAt is the lookup for a top-level VRef: a type-index-space ordinal, mapped through the
// definition that contributed it. A section type is mapped through [Component.typeSpaceToTypes] to the
// compacted section; the alias and import forms ADR 0118 resolves are in [Component.typeOrdinal]. An
// ordinal naming anything else does not resolve, which is the honest answer — the alternative is the
// wrong-type-that-exists above.
func (c *Component) sectionTypeAt() typeAt {
	return func(i uint32) (TypeDef, bool) { return c.typeOrdinal(i, 0) }
}

// maxTypeChase bounds typeOrdinal's recursion. Each step follows a reference to an earlier definition, so a
// well-formed component never nears it; it is reached only by a crafted chain that the forward-reference
// check (run once the whole stream is in) has not yet refused, since types resolve live during the decode.
const maxTypeChase = 64

// typeOrdinal resolves one type-index-space ordinal (#1248, ADR 0118):
//
//   - a type section entry is itself;
//   - `alias export $inst "x" (type)` of an **imported** instance is that instance type's type export x,
//     with its unbound resource identities completed by the import's name. This is how a `use`d type reaches
//     a guest: `use types.{error-code}` is an alias of `wasi:cli/types`' type export, which the next instance
//     type pulls in by `alias outer`. An alias out of an instance the component instantiates itself is not
//     resolved, since its exports' types are the nested component's (out of scope, ADR 0118);
//   - an imported type is its `eq` target, or for `sub resource` a fresh resource named by the import.
//
// **Until ADR 0118 only the first resolved**, so every `alias outer` that named a `use`d type became
// VUnresolvedAlias, and a signature carrying one is refused at instantiate. NEUTER THIS alias arm (leave it
// unresolved), and TestUsedTypesResolveInP3HelloAndEveryImportStillBinds finds `datetime` unresolved and
// p3hello refused.
func (c *Component) typeOrdinal(ord uint32, depth int) (TypeDef, bool) {
	if depth > maxTypeChase {
		return TypeDef{}, false
	}
	d, ok := c.spaceDef(SpaceType, ord)
	if !ok {
		return TypeDef{}, false
	}
	switch d.Section {
	case SectionType:
		if d.Item >= len(c.Types) {
			return TypeDef{}, false
		}
		return c.Types[d.Item], true
	case SectionAlias:
		if d.Item >= len(c.Aliases) {
			return TypeDef{}, false
		}
		a := c.Aliases[d.Item]
		if a.Kind != AliasExport || a.Sort != SortType {
			return TypeDef{}, false
		}
		it, iface, ok := c.importedInstanceType(a.InstanceIdx, depth+1)
		if !ok {
			return TypeDef{}, false
		}
		for _, te := range it.Types {
			if te.Name == a.Name {
				return qualifyTypeDef(te.Def, iface), true
			}
		}
		return TypeDef{}, false
	case SectionImport:
		if d.Item >= len(c.Imports) {
			return TypeDef{}, false
		}
		imp := c.Imports[d.Item]
		// NEUTER THIS arm (answer nothing), and TestAWorldLevelTypeImportResolvesThroughItsBound reads a
		// self with no identity and a result left as a reference.
		if imp.Kind != ExternType {
			return TypeDef{}, false
		}
		if imp.SubResource {
			return TypeDef{Kind: TDResource, Resource: imp.Name}, true
		}
		if imp.TypeIndex >= ord { // an eq names an earlier type; anything else is malformed
			return TypeDef{}, false
		}
		return c.typeOrdinal(imp.TypeIndex, depth+1)
	default:
		return TypeDef{}, false
	}
}

// importedInstanceType resolves an instance-index-space ordinal that names an instance import to that
// import's instance type and its name, the interface its unbound resource identities belong to.
func (c *Component) importedInstanceType(ord uint32, depth int) (*InstanceType, string, bool) {
	d, ok := c.spaceDef(SpaceInstance, ord)
	if !ok || d.Section != SectionImport || d.Item >= len(c.Imports) {
		return nil, "", false
	}
	imp := c.Imports[d.Item]
	if imp.Kind != ExternInstance {
		return nil, "", false
	}
	td, ok := c.typeOrdinal(imp.TypeIndex, depth)
	if !ok || td.Kind != TDInstance || td.Inst == nil {
		return nil, "", false
	}
	return td.Inst, imp.Name, true
}

// spaceDef is the definition that contributed ordinal ord of one index space.
func (c *Component) spaceDef(s Space, ord uint32) (Def, bool) {
	var seen uint32
	for _, d := range c.Defs {
		if d.Space != s {
			continue
		}
		if seen == ord {
			return d, true
		}
		seen++
	}
	return Def{}, false
}

// resolveVal follows a VRef through `at` and inlines the referenced value type, recursing into compound
// elements. own/borrow keep their resource type index (a handle is an i32 regardless) and gain the
// resource's identity when the index names one (ADR 0118).
// `st` carries one resolve's state (resolveState): the VRef indices on the current chain, so a cyclic
// reference (a self-referential placeholder, or any fuzzer-crafted VRef cycle) is detected and returned as
// VUnresolvedAlias — a named, refusable outcome — rather than overflowing the stack (#753); and the size
// budget left, so a legal acyclic type cannot expand without bound. `st` is nil at the top level, which
// starts a fresh resolve.
//
// **The chain set does not bound the work, and this comment said it did.** It read *"It bounds nothing
// legal: a finite, acyclic type visits each index at most once."* True only while the set never shrank,
// which was grave #1256: a second sibling reference to the same type read as a cycle. Once an index leaves
// the set when its chain returns, a type shared by others is expanded at every use, and a chain of
// `tuple<t, t>` types doubles with each level. The budget is what bounds it now.
//
// **The record and tuple arms were absent until #903**, so a record field or tuple element that was
// itself a VRef stayed a reference while every other compound inlined. It was unreachable in the one
// direction that mattered — a record is unmodeled, so a signature carrying one refuses before anything
// reads its fields — but `tuple` is modeled for size and alignment, and the bridge to the codec's types
// reads both. A resolver that inlines four of six compound kinds is one whose output cannot be told from
// a fully resolved type by its own shape.
func resolveVal(vt ValType, at typeAt, st *resolveState) ValType {
	if st == nil {
		st = newResolveState()
	}
	// **The size budget** (grave #1256): every node this resolve builds is counted, and past the budget the
	// node resolves to VUnresolvedAlias, which the instantiate-time refusal names. NEUTER THIS check, and
	// TestASharedTypeChainIsCutAtTheSizeBudget's 24-level chain resolves in full, with no alias in it.
	st.left--
	if st.left < 0 {
		return ValType{Kind: VUnresolvedAlias}
	}
	switch vt.Kind {
	case VRef:
		if td, ok := at(vt.Ref); ok && td.Kind == TDVal {
			if st.seen[vt.Ref] {
				return ValType{Kind: VUnresolvedAlias} // a cycle — unresolvable, refuse by name downstream
			}
			// **The set is the current chain, so an index leaves it on the way back out** (grave #1256, ADR
			// 0118). It was never removed, so the map recorded every index entered anywhere in the type, and a
			// second sibling reference to the same type read as a cycle: p3hello's `descriptor-stat` names
			// `option<datetime>` three times, and the second and third became VUnresolvedAlias. Nothing
			// reached it while a `use`d type decoded as a resource placeholder, which is not followed. NEUTER
			// THIS delete, and TestUsedTypesResolveInP3HelloAndEveryImportStillBinds finds the second
			// timestamp unresolved.
			st.seen[vt.Ref] = true
			out := resolveVal(td.Val, at, st)
			delete(st.seen, vt.Ref)
			return out
		}
		return vt // a ref to a resource/func/instance type — left as a reference
	case VOwn, VBorrow:
		// The handle keeps its index and gains the resource's identity when the index names one (ADR
		// 0118), which is what lets a guest's signature be checked against the host's resource types.
		//
		// **An identity once found is kept.** A resolved type copied into another scope (an outer alias of a
		// `use`d type) is resolved again there, and its Ref is still an ordinal of the scope it came from,
		// so asking the new scope would name whatever resource sits at that position.
		//
		// NEUTER THIS keep, and TestAResolvedHandleKeepsItsIdentityInAnotherScope names the second scope's
		// resource. NEUTER the fill below it, and TestAGuestHandleCarriesItsHostResourcesIdentity reads an
		// empty identity on output-stream.write's self.
		if vt.Resource != "" {
			return vt
		}
		if td, ok := at(vt.Ref); ok && td.Kind == TDResource {
			vt.Resource = td.Resource
		}
		return vt
	case VFuture, VStream:
		// **Absent until ADR 0118**, so a future's payload stayed a reference while every other container's
		// element inlined. Nothing read the payload until #1248 gave `write-via-stream`'s one a definition.
		// NEUTER THIS, and TestAUsedTypeDecodesAsItsDefinition reads the payload as a reference.
		if vt.Elem != nil {
			e := resolveVal(*vt.Elem, at, st)
			vt.Elem = &e
		}
		return vt
	case VList, VOption:
		if vt.Elem != nil {
			e := resolveVal(*vt.Elem, at, st)
			vt.Elem = &e
		}
		return vt
	case VResult:
		if vt.Ok != nil {
			ok := resolveVal(*vt.Ok, at, st)
			vt.Ok = &ok
		}
		if vt.Err != nil {
			e := resolveVal(*vt.Err, at, st)
			vt.Err = &e
		}
		return vt
	case VVariant:
		cs := make([]VarCase, len(vt.Cases))
		for i, c := range vt.Cases {
			cs[i] = c
			if c.Type != nil {
				t := resolveVal(*c.Type, at, st)
				cs[i].Type = &t
			}
		}
		vt.Cases = cs
		return vt
	case VRecord:
		fs := make([]NamedVal, len(vt.Fields))
		for i, f := range vt.Fields {
			fs[i] = NamedVal{Name: f.Name, Type: resolveVal(f.Type, at, st)}
		}
		vt.Fields = fs
		return vt
	case VTuple:
		es := make([]ValType, len(vt.Elems))
		for i, e := range vt.Elems {
			es[i] = resolveVal(e, at, st)
		}
		vt.Elems = es
		return vt
	default:
		return vt
	}
}

// instanceAlias handles an `alias` inside an instance type. The only form reached here is an outer type
// alias: `alias outer ct idx (type)`, which pulls type `idx` of the scope `ct` levels out into this
// instance's local space.
//
// # It resolves the alias, where it used to record a placeholder (#756)
//
// Since ADR 0118 the component scope it reads answers for a `use`d type too (Component.typeOrdinal), so an
// alias of one inlines the type's definition rather than becoming the placeholder.
//
// It read the count and index and appended a placeholder, so a func whose TYPE was the alias got a nil
// signature in `exportDecl`, and an async lower of it never lowered its result. Measured before the fix:
// with `async-lower-synth.wasm`'s shape, an impl's result was not stored at the retptr. wasmtime 49.0.3
// reads the aliased and the inline-typed forms alike (`outer-alias-oracle.reading`).
//
// **The aliased type is inlined against the scope it came from before it enters this one.** A type's
// `VRef`s are ordinals into its own scope, so copying one across unresolved would make it name whatever
// sits at the same ordinal here. That is the wrong-type-that-exists failure `typeAt`'s comment describes.
//
// An alias that names no resolvable type keeps the placeholder: a scope this reader was not given, or an
// ordinal naming an import or alias rather than a parsed type. It is still refused by name where a value
// of it would be marshaled. **A placeholder rather than a decode error**, because a component that never
// marshals the type is still well formed.
func (r *reader) instanceAlias(local *[]TypeDef) error {
	sort, _, err := r.sort()
	if err != nil {
		return err
	}
	disc, err := r.byte()
	if err != nil {
		return err
	}
	if sort != SortType || disc != 0x02 { // outer type alias
		return fmt.Errorf("instance-type alias (sort %s, form %#x) not modeled this slice", sort, disc)
	}
	ct, err := r.u32()
	if err != nil {
		return err
	}
	idx, err := r.u32()
	if err != nil {
		return err
	}
	// ct 0 is this instance's own space; ct n is n scopes out. r.outer holds the enclosing scopes, innermost
	// last.
	var at typeAt
	switch {
	case ct == 0:
		at = sliceTypeAt(*local)
	case int(ct) <= len(r.outer):
		at = r.outer[len(r.outer)-int(ct)]
	}
	if at != nil {
		if td, ok := at(idx); ok {
			*local = append(*local, inlineTypeDef(td, at))
			return nil
		}
	}
	// A DISTINCT unresolved-alias marker, not a VRef{0}: the old VRef{0} placeholder pointed at local[0]
	// (often itself), and resolveVal recursed on it forever (#753). VUnresolvedAlias resolves to itself and
	// is refused by name where a value of it would be marshaled.
	*local = append(*local, TypeDef{Kind: TDVal, Val: ValType{Kind: VUnresolvedAlias}}) // keeps indices aligned
	return nil
}

// inlineTypeDef resolves a type definition's references against the scope it was defined in, so it can be
// copied into another scope without its ordinals changing meaning. An instance type is already resolved
// when it is read, and a resource carries no references.
func inlineTypeDef(td TypeDef, at typeAt) TypeDef {
	switch td.Kind {
	case TDFunc:
		if td.Func != nil {
			td.Func = resolveFunc(td.Func, at)
		}
	case TDVal:
		td.Val = resolveVal(td.Val, at, nil)
	case TDInstance, TDResource:
		// Already resolved when read, and carrying no references, respectively.
	}
	return td
}

// qualifyTypeDef completes a definition's unbound resource identities with the interface its instance type
// was imported as (qualifyResourceID), so a type copied out of an imported instance names that instance's
// resources. A nested instance type is left alone: its resources are its own import's, not this one's.
func qualifyTypeDef(td TypeDef, iface string) TypeDef {
	switch td.Kind {
	case TDResource:
		td.Resource = qualifyResourceID(td.Resource, iface)
	case TDVal:
		td.Val = qualifyVal(td.Val, iface)
	case TDFunc:
		if td.Func != nil {
			td.Func = qualifyFunc(td.Func, iface)
		}
	case TDInstance:
	}
	return td
}

// qualifyFunc is qualifyTypeDef over a function type's parameters and result. The input is not mutated.
func qualifyFunc(ft *FuncType, iface string) *FuncType {
	out := &FuncType{Params: make([]NamedVal, len(ft.Params)), Async: ft.Async}
	for i, p := range ft.Params {
		out.Params[i] = NamedVal{Name: p.Name, Type: qualifyVal(p.Type, iface)}
	}
	if ft.Result != nil {
		r := qualifyVal(*ft.Result, iface)
		out.Result = &r
	}
	return out
}

// qualifyVal is qualifyTypeDef over a resolved value type, rebuilding every compound it descends, since the
// decoded slices are shared by every reader of the type.
func qualifyVal(vt ValType, iface string) ValType {
	elem := func(p *ValType) *ValType {
		if p == nil {
			return nil
		}
		v := qualifyVal(*p, iface)
		return &v
	}
	switch vt.Kind {
	case VOwn, VBorrow:
		vt.Resource = qualifyResourceID(vt.Resource, iface)
	case VList, VOption, VFuture, VStream:
		vt.Elem = elem(vt.Elem)
	case VResult:
		vt.Ok, vt.Err = elem(vt.Ok), elem(vt.Err)
	case VVariant:
		cs := make([]VarCase, len(vt.Cases))
		for i, c := range vt.Cases {
			cs[i] = VarCase{Name: c.Name, Type: elem(c.Type)}
		}
		vt.Cases = cs
	case VRecord:
		fs := make([]NamedVal, len(vt.Fields))
		for i, f := range vt.Fields {
			fs[i] = NamedVal{Name: f.Name, Type: qualifyVal(f.Type, iface)}
		}
		vt.Fields = fs
	case VTuple:
		es := make([]ValType, len(vt.Elems))
		for i, e := range vt.Elems {
			es[i] = qualifyVal(e, iface)
		}
		vt.Elems = es
	default:
	}
	return vt
}

// exportDecl reads an exportdecl's externtype and, for a function export, resolves its type from the
// instance's local type space. A type-exporting decl (a resource subtype or a type eq) introduces a
// type into that space, so it appends to local — keeping later func references' indices aligned — and
// reports isType, so the caller records the export.
func (r *reader) exportDecl(name string, local *[]TypeDef) (ie InstExport, isType bool, err error) {
	disc, err := r.byte()
	if err != nil {
		return InstExport{}, false, err
	}
	switch disc {
	case 0x01: // func (type i)
		i, err := r.u32()
		if err != nil {
			return InstExport{}, false, err
		}
		if int(i) < len(*local) && (*local)[i].Kind == TDFunc {
			return InstExport{Name: name, Func: (*local)[i].Func}, false, nil
		}
		return InstExport{Name: name}, false, nil
	case 0x03: // type b — exports (and introduces) a type: a resource subtype or a type eq
		eq, idx, err := r.typeBound()
		if err != nil {
			return InstExport{}, false, err
		}
		*local = append(*local, exportedTypeDef(name, eq, idx, *local)) // the exported type joins the local space
		return InstExport{Name: name}, true, nil
	case 0x05: // instance (type i)
		if _, err := r.u32(); err != nil {
			return InstExport{}, false, err
		}
		return InstExport{Name: name}, false, nil
	case 0x00: // core module
		if b, err := r.byte(); err != nil || b != 0x11 {
			return InstExport{}, false, fmt.Errorf("exportdecl core sort not module")
		}
		if _, err := r.u32(); err != nil {
			return InstExport{}, false, err
		}
		return InstExport{Name: name}, false, nil
	case 0x02: // value
		return InstExport{Name: name}, false, r.valueBound()
	case 0x04: // component
		if _, err := r.u32(); err != nil {
			return InstExport{}, false, err
		}
		return InstExport{Name: name}, false, nil
	default:
		return InstExport{}, false, fmt.Errorf("exportdecl externtype %#x undefined", disc)
	}
}

// exportedTypeDef is the definition a type export introduces into its instance type's local space (#1248,
// ADR 0118).
//
//   - `(eq N)` introduces the type N already names, so it is a copy of local[N]. `wasi:cli/types`' `error-code`
//     is `(export "error-code" (type (eq 0)))` over an enum, and a signature reaching it now reaches the enum.
//   - `(sub resource)` introduces a fresh resource type, whose identity is the export's name in an instance
//     not yet known (selfResourceID).
//
// **This was a resource placeholder for both**, whatever the bound was, so a `use`d record, enum or variant
// decoded as a reference to a resource, and `write-via-stream`'s `future<result<_, error-code>>` as
// `future<ref>`. NEUTER THIS eq arm (return the sub-resource placeholder for both), and
// TestAUsedTypeDecodesAsItsDefinition reads a VRef where the enum should be.
//
// An eq naming no earlier type is malformed; it keeps the unresolved-alias marker rather than failing the
// decode, for instanceAlias's reason: a component that never marshals the type is still well formed here.
func exportedTypeDef(name string, eq bool, idx uint32, local []TypeDef) TypeDef {
	if !eq {
		return TypeDef{Kind: TDResource, Resource: selfResourceID(name)}
	}
	if int(idx) < len(local) {
		return local[idx]
	}
	return TypeDef{Kind: TDVal, Val: ValType{Kind: VUnresolvedAlias}}
}

// labelName reads a label (a plain length-prefixed UTF-8 name, as in a record field or a param).
func (r *reader) labelName() (string, error) { return r.coreName() }

// valType reads a `valtype`: a signed LEB — non-negative is a type index (VRef), negative is a
// primitive/compound opcode.
func (r *reader) valType() (ValType, error) {
	v, err := r.sleb()
	if err != nil {
		return ValType{}, err
	}
	if v >= 0 {
		return ValType{Kind: VRef, Ref: uint32(v)}, nil
	}
	return valTypeFromOpcode(r, byte(0x80+v))
}

// valTypeFromOpcode decodes a value type given its already-read opcode byte (a defvaltype).
func valTypeFromOpcode(r *reader, op byte) (ValType, error) {
	switch op {
	case 0x7f:
		return ValType{Kind: VBool}, nil
	case 0x7e:
		return ValType{Kind: VS8}, nil
	case 0x7d:
		return ValType{Kind: VU8}, nil
	case 0x7c:
		return ValType{Kind: VS16}, nil
	case 0x7b:
		return ValType{Kind: VU16}, nil
	case 0x7a:
		return ValType{Kind: VS32}, nil
	case 0x79:
		return ValType{Kind: VU32}, nil
	case 0x78:
		return ValType{Kind: VS64}, nil
	case 0x77:
		return ValType{Kind: VU64}, nil
	case 0x76:
		return ValType{Kind: VF32}, nil
	case 0x75:
		return ValType{Kind: VF64}, nil
	case 0x74:
		return ValType{Kind: VChar}, nil
	case 0x73:
		return ValType{Kind: VString}, nil
	case 0x64:
		return ValType{Kind: VErrorContext}, nil
	case 0x66: // stream t? — async surface, recognized not modeled (gate:async, ADR 0086)
		elem, err := r.optValType()
		return ValType{Kind: VStream, Elem: elem}, err
	case 0x65: // future t? — async surface, recognized not modeled
		elem, err := r.optValType()
		return ValType{Kind: VFuture, Elem: elem}, err
	case 0x70: // list
		elem, err := r.valType()
		return ValType{Kind: VList, Elem: &elem}, err
	case 0x6b: // option
		elem, err := r.valType()
		return ValType{Kind: VOption, Elem: &elem}, err
	case 0x69: // own
		i, err := r.u32()
		return ValType{Kind: VOwn, Ref: i}, err
	case 0x68: // borrow
		i, err := r.u32()
		return ValType{Kind: VBorrow, Ref: i}, err
	case 0x6a: // result: optional ok, optional err
		ok, err := r.optValType()
		if err != nil {
			return ValType{}, err
		}
		e, err := r.optValType()
		if err != nil {
			return ValType{}, err
		}
		return ValType{Kind: VResult, Ok: ok, Err: e}, nil
	case 0x72: // record
		fields, err := r.namedValVec()
		if err != nil {
			return ValType{}, err
		}
		// **An empty record is refused, matching the reference validator.** Measured rather than
		// assumed: a component declaring `(record)` encodes fine — `wasm-tools parse` accepts it — and
		// `wasm-tools validate` refuses with *"record type must have at least one field (at offset
		// 0xb)"*. So this is a conformance fix and not this engine inventing a restriction.
		//
		// The Canonical ABI is the reason the reference draws the line there: `elem_size_record` ends
		// with `assert(s > 0)` (definitions.py:elem_size_record:"assert(s > 0)"), so an empty record has no layout at all — asked of
		// the pinned model directly, `elem_size(RecordType([]))` raises `AssertionError`. A type with no
		// layout cannot carry a value, so refusing it at load is strictly better than accepting a type
		// nothing can do anything with.
		//
		// **Refused at decode, which is where this engine can refuse it.** The component path has no
		// separate validation pass and its decode errors surface without the malformed/invalid split
		// the core-module boundary draws — so the honest thing is to name the rule in the message, which
		// is what a reader needs whichever sentinel it eventually wears. Reported as the *rule* being
		// broken rather than as a short read, because the bytes are well-formed; this is grave #301's
		// distinction, applied in the direction that engine has to be careful about.
		if len(fields) == 0 {
			return ValType{}, fmt.Errorf("record type must have at least one field")
		}
		return ValType{Kind: VRecord, Fields: fields}, nil
	case 0x6f: // tuple
		n, err := r.u32()
		if err != nil {
			return ValType{}, err
		}
		// The same rule, the same source: `wasm-tools validate` refuses `(tuple)` with *"tuple type must
		// have at least one type (at offset 0xb)"*. A tuple despecializes to a record
		// (definitions.py:despecialize:"case TupleType(ts) : return RecordType([ FieldType(str(i),"), so an empty one is an empty record and has no layout either — which is
		// why these two arms refuse together rather than one of them being the general case.
		if n == 0 {
			return ValType{}, fmt.Errorf("tuple type must have at least one type")
		}
		elems := make([]ValType, n)
		for i := range n {
			if elems[i], err = r.valType(); err != nil {
				return ValType{}, err
			}
		}
		return ValType{Kind: VTuple, Elems: elems}, nil
	case 0x71: // variant
		cases, err := r.variantCases()
		return ValType{Kind: VVariant, Cases: cases}, err
	case 0x6e: // flags
		labels, err := r.labelVec()
		return ValType{Kind: VFlags, Labels: labels}, err
	case 0x6d: // enum
		labels, err := r.labelVec()
		return ValType{Kind: VEnum, Labels: labels}, err
	default:
		return ValType{}, fmt.Errorf("valtype opcode %#x not modeled this slice", op)
	}
}

// optValType reads a `valtype?`: 0x00 absent, 0x01 valtype present.
func (r *reader) optValType() (*ValType, error) {
	present, err := r.byte()
	if err != nil {
		return nil, err
	}
	switch present {
	case 0x00:
		return nil, nil
	case 0x01:
		vt, err := r.valType()
		return &vt, err
	default:
		return nil, fmt.Errorf("optional valtype flag %#x is not 0x00/0x01", present)
	}
}

func (r *reader) namedValVec() ([]NamedVal, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	out := make([]NamedVal, n)
	for i := range n {
		name, err := r.labelName()
		if err != nil {
			return nil, err
		}
		vt, err := r.valType()
		if err != nil {
			return nil, err
		}
		out[i] = NamedVal{Name: name, Type: vt}
	}
	return out, nil
}

func (r *reader) variantCases() ([]VarCase, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	out := make([]VarCase, n)
	for i := range n {
		name, err := r.labelName()
		if err != nil {
			return nil, err
		}
		payload, err := r.optValType()
		if err != nil {
			return nil, err
		}
		if _, err := r.byte(); err != nil { // trailing 0x00 (refinement slot)
			return nil, err
		}
		out[i] = VarCase{Name: name, Type: payload}
	}
	return out, nil
}

func (r *reader) labelVec() ([]string, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	out := make([]string, n)
	for i := range n {
		if out[i], err = r.labelName(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// unmodeledValKind reports the first component value kind in vt that the canon codec has no value
// representation for — `error-context` (no `canon.Kind` and no `store`/`StoreVia` arm lowers it; a value
// can never be constructed), and the wrapper-minted `stream`/`future`. It recurses through the containers
// the codec *does* model (`list`, `option`, `result`, `variant`, `tuple`, `record`) so a modeled container
// of an unmodeled element is caught — `list<tuple<string,string>>` is clean, `option<error-context>` is
// not — while the containers themselves, `flags`, `enum`, and every scalar/string/handle, are modeled and
// pass. Derived from the codec's kinds (ADR 0084 / #720's binding refusal), not a hardcoded list.
//
// `record`, `flags`, `enum` and `option` left the refused list with ADR 0116, which gave the codec their
// arms. Widening this predicate changes which components instantiate — an implemented lower carrying one
// now binds — which is why it moved in the slice that gave the codec the kinds, and not before.
func unmodeledValKind(vt ValType, depth int) (ValKind, bool) {
	if depth > 32 {
		return 0, false // a cyclic/pathological type; the decoder's own depth guards apply first
	}
	switch vt.Kind {
	case VErrorContext, VStream, VFuture, VUnresolvedAlias:
		// A future or stream is refused as itself, so its payload, resolvable since ADR 0118, is never
		// reached here: no element could make the verdict cleaner, and the kind already names the obstacle.
		return vt.Kind, true
	case VFlags:
		// Modeled, but only for a label set the codec can lay out: the bridge refuses the rest through
		// `canon.FlagsType`, and asking the same constructor here is what keeps the two walks agreeing.
		if _, err := canon.FlagsType(vt.Labels...); err != nil {
			return VFlags, true
		}
	case VEnum:
		if _, err := canon.EnumType(vt.Labels...); err != nil {
			return VEnum, true
		}
	case VRecord:
		// An empty record is unmodeled for `VTuple`'s reason below: the model gives it no size. The
		// decoder refuses one, so this answers for a hand-built `ValType`.
		if len(vt.Fields) == 0 {
			return VRecord, true
		}
		for _, f := range vt.Fields {
			if k, ok := unmodeledValKind(f.Type, depth+1); ok {
				return k, true
			}
		}
	case VList, VOption: // each recurses into its element
		if vt.Elem != nil {
			return unmodeledValKind(*vt.Elem, depth+1)
		}
	case VResult:
		if vt.Ok != nil {
			if k, ok := unmodeledValKind(*vt.Ok, depth+1); ok {
				return k, true
			}
		}
		if vt.Err != nil {
			return unmodeledValKind(*vt.Err, depth+1)
		}
	case VVariant:
		for _, c := range vt.Cases {
			if c.Type != nil {
				if k, ok := unmodeledValKind(*c.Type, depth+1); ok {
					return k, true
				}
			}
		}
	case VTuple:
		// **An empty tuple is unmodeled, and saying so is what retires a declared divergence.**
		//
		// The loop below finds nothing to refuse in zero elements, so `tuple<>` used to pass here while
		// the bridge refused it — the ABI gives an empty tuple no size, since it despecializes to an
		// empty record and `elem_size_record` asserts `s > 0` (definitions.py:elem_size_record:"assert(s > 0)"). That disagreement
		// was declared in `TestTheBridgeAndTheUnmodeledPredicateAgree` rather than hidden, and it is
		// closed here rather than left declared, because a divergence nobody needs is a divergence that
		// will be read as intentional.
		//
		// Unreachable from a decoded component as of the same slice — `valType` refuses the type
		// outright, matching `wasm-tools validate` — so this arm answers for a hand-built `ValType`
		// only. Added anyway: the predicate's contract is "which kinds can the codec not marshal", and
		// an answer that depends on who built the type is not an answer to that question.
		if len(vt.Elems) == 0 {
			return VTuple, true
		}
		for _, e := range vt.Elems {
			if k, ok := unmodeledValKind(e, depth+1); ok {
				return k, true
			}
		}
	default:
		// every scalar, string, own/borrow handle, and VRef — modeled, so clean.
	}
	return 0, false
}

// unmodeledInSig reports the first unmodeled value kind anywhere in a function type's parameters or
// result — the check a binding (implemented import) and an export `Call` make before a value of the kind
// would be marshaled.
func unmodeledInSig(ft *FuncType) (ValKind, bool) {
	if ft == nil {
		return 0, false
	}
	for _, p := range ft.Params {
		if k, ok := unmodeledValKind(p.Type, 0); ok {
			return k, true
		}
	}
	if ft.Result != nil {
		return unmodeledValKind(*ft.Result, 0)
	}
	return 0, false
}

// sigHasUnresolvedAlias reports whether a function signature carries a VUnresolvedAlias anywhere — an outer
// type alias this engine does not resolve (#753). Distinct from unmodeledInSig because it must fire even in
// a signature that also carries a modeled-elsewhere async kind (a future is handled by the async wrapper,
// but an unresolved alias beside it is still unmarshalable), so it cannot rely on unmodeledInSig's
// first-kind-wins result.
func sigHasUnresolvedAlias(ft *FuncType) bool {
	if ft == nil {
		return false
	}
	for _, p := range ft.Params {
		if valHasUnresolvedAlias(p.Type, 0) {
			return true
		}
	}
	return ft.Result != nil && valHasUnresolvedAlias(*ft.Result, 0)
}

func valHasUnresolvedAlias(vt ValType, depth int) bool {
	if depth > 32 {
		return false
	}
	switch vt.Kind {
	case VUnresolvedAlias:
		return true
	case VList, VOption, VFuture, VStream:
		// The future and stream arms are ADR 0118's: a future's payload is a type like any other, and since
		// #1248 it can carry a `use`d type, which is where an unresolved alias would sit. NEUTER THESE and the
		// record arm, and TestAnUnresolvedAliasIsSeenInsideAFutureAndARecord reads all three specimens clean.
		return vt.Elem != nil && valHasUnresolvedAlias(*vt.Elem, depth+1)
	case VRecord:
		// Absent until ADR 0118, so an unresolved field of a record was not seen. p3hello's
		// `descriptor-stat` is a record whose timestamps are `option<datetime>`, a `use`d type.
		for _, f := range vt.Fields {
			if valHasUnresolvedAlias(f.Type, depth+1) {
				return true
			}
		}
	case VResult:
		return (vt.Ok != nil && valHasUnresolvedAlias(*vt.Ok, depth+1)) || (vt.Err != nil && valHasUnresolvedAlias(*vt.Err, depth+1))
	case VVariant:
		for _, c := range vt.Cases {
			if c.Type != nil && valHasUnresolvedAlias(*c.Type, depth+1) {
				return true
			}
		}
	case VTuple:
		for _, e := range vt.Elems {
			if valHasUnresolvedAlias(e, depth+1) {
				return true
			}
		}
	default:
	}
	return false
}

// valKindName is the WIT name of a value kind, for a refuse-by-name message.
func valKindName(k ValKind) string {
	switch k {
	// The scalars and the other shapes were missing until #888, and the gap is the shape grave #885
	// records one level up: **a function that is total for its first consumer and partial for the next.**
	// This existed for the unmodeled-kind binding refusal, which only ever names the kinds it refuses, so
	// `string` and the numerics fell to the default. #888's cross-component result refusal names whatever
	// kind it could not carry — and its own witness caught the message reading `valkind(12)`, which is
	// exactly the "a bare unsupported leaves the next author guessing" failure that assertion exists for.
	//
	// Completed here rather than given a second namer beside it, on #885's lesson: the duplication is the
	// defect and the missing case is its symptom.
	case VBool:
		return "bool"
	case VS8:
		return "s8"
	case VU8:
		return "u8"
	case VS16:
		return "s16"
	case VU16:
		return "u16"
	case VS32:
		return "s32"
	case VU32:
		return "u32"
	case VS64:
		return "s64"
	case VU64:
		return "u64"
	case VF32:
		return "f32"
	case VF64:
		return "f64"
	case VChar:
		return "char"
	case VString:
		return "string"
	case VList:
		return "list"
	case VVariant:
		return "variant"
	case VTuple:
		return "tuple"
	case VResult:
		return "result"
	case VOwn:
		return "own"
	case VBorrow:
		return "borrow"
	case VRef:
		return "a typeidx reference"
	case VRecord:
		return "record"
	case VFlags:
		return "flags"
	case VEnum:
		return "enum"
	case VOption:
		return "option"
	case VErrorContext:
		return "error-context"
	case VStream:
		return "stream"
	case VFuture:
		return "future"
	case VUnresolvedAlias:
		return "an unresolved outer type alias"
	default:
		return fmt.Sprintf("valkind(%d)", int(k))
	}
}
