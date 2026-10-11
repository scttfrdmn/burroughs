// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "fmt"

// `core:type` sections, decoded exactly as far as `thread.new-indirect` needs and no further (#969).
//
// # Why this exists
//
// `thread.new-indirect`'s `ft` operand names a core type, and until #969 this loader decoded no
// `core:type` section at all — `SectionCoreType` appeared only in `SectionKind.String`. So `ft` was an
// index the engine had never read, and the only check was on the callee. Measured on one fixture: a canon
// declaring `(i64) -> ()` over a table holding an `(i32) -> ()` function was **rejected as invalid** by
// `wasm-tools validate` and by wasmtime 49.0.2 — *"start function must take a single `i32` argument
// (currently)"* — and **accepted and run** by Burroughs. Accepting invalid input is #952 and #957's class
// in the other direction.
//
// # How little is decoded, and why that is safe
//
// A `core:type` section is `vec(core:deftype)`, and the vec's **length comes first**. So every entry can be
// COUNTED into the core type index space even where it is not decoded, which keeps every later index
// right. What is decoded is the simple case — `0x60 vec(valtype) vec(valtype)` with single-byte value types
// — and the first entry that is anything else (a rec group, a subtype, a GC composite type, a module type,
// or a multi-byte reference type) stops decoding for the rest of that section. Those entries are recorded
// as opaque: `Func` stays nil.
//
// **Opaque is never treated as acceptable.** A `new-indirect` whose `ft` lands on one is refused by name,
// so the conservative direction is a refusal and never an acceptance — the opposite of the defect.
//
// Nothing else reads these definitions today, so a component with a `core:type` section this decoder
// cannot read loads exactly as it did before #969 unless it also uses `thread.new-indirect`.

// CoreTypeDef is one entry of a `core:type` section. `Func` is non-nil only for an entry decoded as a
// plain function type; everything else is opaque to this engine.
type CoreTypeDef struct {
	Func *CoreFuncSig
}

// CoreFuncSig is a core function type held as its value types' single-byte encodings (`0x7F` i32, `0x7E`
// i64, and so on). Bytes rather than `binary.ValType` because the only question asked of it is equality
// with `(i32) -> ()`, and a multi-byte type is opaque before it gets here.
type CoreFuncSig struct {
	Params  []byte
	Results []byte
}

// isI32Entry reports whether the signature is `(i32) -> ()`, the only `new-indirect` entry type the
// reference toolchain accepts today.
func (s *CoreFuncSig) isI32Entry() bool {
	return s != nil && len(s.Params) == 1 && s.Params[0] == 0x7F && len(s.Results) == 0
}

// singleByteValType reports whether b is a value type this decoder reads in one byte: the four numbers,
// v128, and the two abbreviated reference types `funcref` and `externref`.
func singleByteValType(b byte) bool {
	switch b {
	case 0x7F, 0x7E, 0x7D, 0x7C, 0x7B, 0x70, 0x6F:
		return true
	}
	return false
}

// parseCoreTypes reads a `core:type` section body, defining every entry in the core type space.
func (c *Component) parseCoreTypes(body []byte) error {
	r := &reader{b: body}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: core:type count: %w", err)
	}
	decoding := true
	for range n {
		var d CoreTypeDef
		if decoding {
			sig, ok, derr := r.coreFuncSig()
			if derr != nil {
				return fmt.Errorf("component: core:type %d: %w", len(c.CoreTypes), derr)
			}
			if ok {
				d.Func = sig
			} else {
				// Not a plain functype. Its length is not known without decoding it, so every later
				// entry in this section is opaque too — counted, never decoded.
				decoding = false
			}
		}
		c.CoreTypes = append(c.CoreTypes, d)
		c.def(SpaceCoreType, SectionCoreType, len(c.CoreTypes)-1)
	}
	if decoding {
		return endOf("core:type", r, len(body))
	}
	return nil
}

// coreFuncSig decodes one `0x60` functype of single-byte value types, or reports `ok == false` without
// consuming anything when the entry is any other form. An error is a malformed functype.
func (r *reader) coreFuncSig() (*CoreFuncSig, bool, error) {
	if r.pos >= len(r.b) || r.b[r.pos] != 0x60 {
		return nil, false, nil
	}
	save := r.pos
	r.pos++
	vec := func() ([]byte, bool, error) {
		k, err := r.u32()
		if err != nil {
			return nil, false, err
		}
		out := make([]byte, 0, k)
		for range k {
			b, err := r.byte()
			if err != nil {
				return nil, false, err
			}
			if !singleByteValType(b) {
				return nil, false, nil
			}
			out = append(out, b)
		}
		return out, true, nil
	}
	params, ok, err := vec()
	if err != nil || !ok {
		r.pos = save
		return nil, false, err
	}
	results, ok, err := vec()
	if err != nil || !ok {
		r.pos = save
		return nil, false, err
	}
	return &CoreFuncSig{Params: params, Results: results}, true, nil
}

// coreTypeAt resolves an index in the core type space to its definition, or reports `ok == false` when
// the index names something this engine does not decode — an opaque `core:type` entry, or an alias.
func (c *Component) coreTypeAt(idx uint32) (CoreTypeDef, bool) {
	var k uint32
	for _, d := range c.Defs {
		if d.Space != SpaceCoreType {
			continue
		}
		if k == idx {
			if d.Section != SectionCoreType {
				return CoreTypeDef{}, false
			}
			return c.CoreTypes[d.Item], true
		}
		k++
	}
	return CoreTypeDef{}, false
}

// checkNewIndirectFT refuses a `thread.new-indirect` whose declared `ft` is not `(i32) -> ()`, at LOAD,
// matching `wasm-tools validate` and wasmtime 49.0.2. Refused under ErrUnsupportedForm, as the 🧵 decode
// refusals are, because the reference toolchain treats this as invalid rather than as a trap.
func (c *Component) checkNewIndirectFT(ft uint32) error {
	d, ok := c.coreTypeAt(ft)
	if !ok || d.Func == nil {
		return fmt.Errorf("%w: thread.new-indirect's ft names core type %d, which this engine does not "+
			"decode, so it cannot be checked against the toolchain's constraint and is refused rather "+
			"than accepted unchecked", ErrUnsupportedForm, ft)
	}
	if !d.Func.isI32Entry() {
		return fmt.Errorf("%w: thread.new-indirect's ft (core type %d) is not (i32) -> (); wasm-tools "+
			"and wasmtime reject it the same way — \"start function must take a single `i32` argument "+
			"(currently)\"", ErrUnsupportedForm, ft)
	}
	return nil
}
