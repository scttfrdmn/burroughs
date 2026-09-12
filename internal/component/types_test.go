// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"testing"
)

func findExportFunc(c *Component, name string) *FuncType {
	for _, td := range c.Types {
		if td.Kind != TDInstance {
			continue
		}
		for _, e := range td.Inst.Exports {
			if e.Name == name {
				return e.Func
			}
		}
	}
	return nil
}

// TestComponentTypesDecodeToTheSignatures is C.1's exit: p3hello's type section decodes (consumed
// exactly), and the signatures the marshaling reaches resolve to concrete value types against each
// instance type's nested type space — the depth C.2 needs.
func TestComponentTypesDecodeToTheSignatures(t *testing.T) {
	b, err := os.ReadFile(fixtureWasm)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// output-stream.blocking-write-and-flush: (self: borrow<output-stream>, contents: list<u8>)
	// -> result<_, stream-error>.
	w := findExportFunc(c, "[method]output-stream.blocking-write-and-flush")
	if w == nil {
		t.Fatal("blocking-write-and-flush not decoded")
	}
	if len(w.Params) != 2 {
		t.Fatalf("write params = %d, want 2", len(w.Params))
	}
	if w.Params[0].Type.Kind != VBorrow {
		t.Errorf("write self = kind %d, want VBorrow", w.Params[0].Type.Kind)
	}
	if w.Params[1].Type.Kind != VList || w.Params[1].Type.Elem == nil || w.Params[1].Type.Elem.Kind != VU8 {
		t.Errorf("write contents = %+v, want list<u8>", w.Params[1].Type)
	}
	if w.Result == nil || w.Result.Kind != VResult {
		t.Errorf("write result = %+v, want result", w.Result)
	}

	// get-stdout: () -> own<output-stream>.
	gs := findExportFunc(c, "get-stdout")
	if gs == nil {
		t.Fatal("get-stdout not decoded")
	}
	if len(gs.Params) != 0 || gs.Result == nil || gs.Result.Kind != VOwn {
		t.Errorf("get-stdout = %+v -> %+v, want () -> own", gs.Params, gs.Result)
	}
}

// TestUnmodeledTypeFormRefusedByName witnesses the refuse-by-name discipline for the type section: a
// type form beyond the guest-driven depth (here a nested component type, 0x41) refuses at parse.
func TestUnmodeledTypeFormRefusedByName(t *testing.T) {
	comp := append(append([]byte(nil), componentPreamble...), section(byte(SectionType), 0x01, 0x41)...)
	if _, err := Load(comp); err == nil {
		t.Fatal("Load accepted a nested component type (0x41) — an unmodeled type form")
	}
}

// TestUnmodeledTypeFormsRefuseByName widens TestUnmodeledTypeFormRefusedByName across the refused-by-name
// set the loader's type decoder bounds (async func, the unmodeled value-type opcodes, an instance-type
// core:type): each refuses at parse, naming the form, so the flip's claim is bounded by a witnessed
// boundary rather than an assumed one (#720 forecast; the #714 lesson).
func TestUnmodeledTypeFormsRefuseByName(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte // a type section body: count=1 then the form
	}{
		{"async-func-0x43", []byte{0x01, 0x43}},
		{"nested-component-0x41", []byte{0x01, 0x41}},
		{"unmodeled-valtype-stream-0x66", []byte{0x01, 0x66}},
		{"unmodeled-valtype-future-0x65", []byte{0x01, 0x65}},
		{"instancetype-core-type-0x00", []byte{0x01, 0x42, 0x01, 0x00}}, // instancetype, 1 decl, core:type
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := append(append([]byte(nil), componentPreamble...), section(byte(SectionType), tc.body...)...)
			_, err := Load(comp)
			if err == nil {
				t.Fatalf("Load accepted %s — an unmodeled type form that must refuse by name", tc.name)
			}
		})
	}
}
