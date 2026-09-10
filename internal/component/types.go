// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "fmt"

// Component-type decoding (PR C / C.1, ADR 0084 §6). The loader framed the type section and B.1 read
// types only to canon's depth; the value-marshaling path (C.2) needs function types resolved to their
// parameter and result value types, so the type section now parses — to the guest-driven depth of the
// ten import signatures and the types they reach, per `Binary.md` @ 2bed77e. A type form beyond that
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
	VRef // a typeidx reference to a defined type
)

// ValType is a decoded component value type. Only the fields meaningful to its Kind are set; a
// compound holds its element/case/field types inline, own/borrow/ref hold a type index.
type ValType struct {
	Kind   ValKind
	Ref    uint32     // VRef / VOwn / VBorrow: the referenced type index
	Elem   *ValType   // VList / VOption
	Fields []NamedVal // VRecord
	Cases  []VarCase  // VVariant
	Elems  []ValType  // VTuple
	Labels []string   // VFlags / VEnum
	Ok     *ValType   // VResult (nil if absent)
	Err    *ValType   // VResult (nil if absent)
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

// FuncType is a component function type: named params and an optional single result.
type FuncType struct {
	Params []NamedVal
	Result *ValType // nil = no result
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
// retained — the marshaling reaches functions through an instance's export names).
type InstanceType struct {
	Exports []InstExport
}

// InstExport is one export declaration of an instance type: a name and the func type it names (nil for
// exports that are not functions — resources, types — which the marshaling does not reach).
type InstExport struct {
	Name string
	Func *FuncType
}

// TypeDef is one entry in the component type index space.
type TypeDef struct {
	Kind TypeDefKind
	Val  ValType
	Func *FuncType
	Inst *InstanceType
}

// parseTypes decodes the component type section into c.Types, appending in stream order. Each entry is
// dispatched on its opcode: a function type, an instance type, a resource type, or a value type.
func (c *Component) parseTypes(body []byte) error {
	r := &reader{b: body}
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
	case 0x43: // async functype
		return TypeDef{}, fmt.Errorf("component: async func type (0x43) not modeled this slice")
	default:
		// a defvaltype opcode (a negative SLEB single byte): the value type forms.
		vt, err := valTypeFromOpcode(r, op)
		return TypeDef{Kind: TDVal, Val: vt}, err
	}
}

// funcType reads a `functype` body (after the 0x40): a param vec of labelled value types, then a
// result — 0x00 valtype (one result) or 0x01 0x00 (none).
func (r *reader) funcType() (*FuncType, error) {
	params, err := r.namedValVec()
	if err != nil {
		return nil, err
	}
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
		return &FuncType{Params: params, Result: &vt}, nil
	case 0x01:
		if b, err := r.byte(); err != nil {
			return nil, err
		} else if b != 0x00 {
			return nil, fmt.Errorf("functype resultlist 0x01 not followed by 0x00 (got %#x)", b)
		}
		return &FuncType{Params: params}, nil
	default:
		return nil, fmt.Errorf("functype resultlist discriminant %#x is not 0x00/0x01", disc)
	}
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
			td, err := r.typeDef()
			if err != nil {
				return nil, err
			}
			local = append(local, td)
		case 0x02: // alias — an outer/export type alias within the instance type
			if err := r.skipInstanceAlias(&local); err != nil {
				return nil, err
			}
		case 0x04: // exportdecl: nameattributes + externtype
			name, err := r.name()
			if err != nil {
				return nil, err
			}
			ie, err := r.exportDecl(name, &local)
			if err != nil {
				return nil, err
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
			inst.Exports[i].Func = resolveFunc(inst.Exports[i].Func, local)
		}
	}
	return inst, nil
}

// resolveFunc inlines a function type's parameter and result value types against a local type space.
func resolveFunc(ft *FuncType, local []TypeDef) *FuncType {
	out := &FuncType{Params: make([]NamedVal, len(ft.Params))}
	for i, p := range ft.Params {
		out.Params[i] = NamedVal{Name: p.Name, Type: resolveVal(p.Type, local)}
	}
	if ft.Result != nil {
		r := resolveVal(*ft.Result, local)
		out.Result = &r
	}
	return out
}

// resolveVal follows a VRef into the local type space and inlines the referenced value type, recursing
// into compound elements. own/borrow keep their resource type index (a handle is an i32 regardless).
func resolveVal(vt ValType, local []TypeDef) ValType {
	switch vt.Kind {
	case VRef:
		if int(vt.Ref) < len(local) && local[vt.Ref].Kind == TDVal {
			return resolveVal(local[vt.Ref].Val, local)
		}
		return vt // a ref to a resource/func/instance type — left as a reference
	case VList, VOption:
		if vt.Elem != nil {
			e := resolveVal(*vt.Elem, local)
			vt.Elem = &e
		}
		return vt
	case VResult:
		if vt.Ok != nil {
			ok := resolveVal(*vt.Ok, local)
			vt.Ok = &ok
		}
		if vt.Err != nil {
			e := resolveVal(*vt.Err, local)
			vt.Err = &e
		}
		return vt
	case VVariant:
		cs := make([]VarCase, len(vt.Cases))
		for i, c := range vt.Cases {
			cs[i] = c
			if c.Type != nil {
				t := resolveVal(*c.Type, local)
				cs[i].Type = &t
			}
		}
		vt.Cases = cs
		return vt
	default:
		return vt
	}
}

// skipInstanceAlias handles an `alias` inside an instance type. The only form reached here is an
// outer type alias (kind type, an earlier type pulled into this space); it is recorded as a reference
// so the instance-local index space stays aligned.
func (r *reader) skipInstanceAlias(local *[]TypeDef) error {
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
	if _, err := r.u32(); err != nil { // count
		return err
	}
	if _, err := r.u32(); err != nil { // index
		return err
	}
	*local = append(*local, TypeDef{Kind: TDVal, Val: ValType{Kind: VRef}}) // placeholder, keeps indices aligned
	return nil
}

// exportDecl reads an exportdecl's externtype and, for a function export, resolves its type from the
// instance's local type space. A type-exporting decl (a resource subtype or a type eq) introduces a
// type into that space, so it appends to local — keeping later func references' indices aligned.
func (r *reader) exportDecl(name string, local *[]TypeDef) (InstExport, error) {
	disc, err := r.byte()
	if err != nil {
		return InstExport{}, err
	}
	switch disc {
	case 0x01: // func (type i)
		i, err := r.u32()
		if err != nil {
			return InstExport{}, err
		}
		if int(i) < len(*local) && (*local)[i].Kind == TDFunc {
			return InstExport{Name: name, Func: (*local)[i].Func}, nil
		}
		return InstExport{Name: name}, nil
	case 0x03: // type b — exports (and introduces) a type: a resource subtype or a type eq
		if err := r.typeBound(); err != nil {
			return InstExport{}, err
		}
		*local = append(*local, TypeDef{Kind: TDResource}) // the exported type joins the local type space
		return InstExport{Name: name}, nil
	case 0x05: // instance (type i)
		if _, err := r.u32(); err != nil {
			return InstExport{}, err
		}
		return InstExport{Name: name}, nil
	case 0x00: // core module
		if b, err := r.byte(); err != nil || b != 0x11 {
			return InstExport{}, fmt.Errorf("exportdecl core sort not module")
		}
		if _, err := r.u32(); err != nil {
			return InstExport{}, err
		}
		return InstExport{Name: name}, nil
	case 0x02: // value
		return InstExport{Name: name}, r.valueBound()
	case 0x04: // component
		if _, err := r.u32(); err != nil {
			return InstExport{}, err
		}
		return InstExport{Name: name}, nil
	default:
		return InstExport{}, fmt.Errorf("exportdecl externtype %#x undefined", disc)
	}
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
		return ValType{Kind: VRecord, Fields: fields}, err
	case 0x6f: // tuple
		n, err := r.u32()
		if err != nil {
			return ValType{}, err
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
