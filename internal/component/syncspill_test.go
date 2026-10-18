// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestASyncCrossComponentSpillIsRefusedByName is #995's interim witness (the chair's ruling of 2026-10-18).
// A sync cross-component call whose result spills must be refused by name, with ErrUnsupportedForm naming
// the form, rather than failing on arity.
//
// `sync-spill-string.wasm` sync-lowers a sibling's sync `get: func() -> string`. wasmtime 49.0.3 copies the
// string across and reads 3, 10 of 10 (`sync-spill-string.reading`). So this is a stated limit, not
// conformance: the conformant fix is the memory-to-memory adapter, held in an ADR draft. Before this, Burroughs
// failed with *"interp: "get" takes 0 arguments, got 1"*.
//
// **Neuter, run:** skipping the `syncSpill` refusal in `bindCrossComponent` reads the arity error again.
func TestASyncCrossComponentSpillIsRefusedByName(t *testing.T) {
	b, err := os.ReadFile("testdata/sync-spill-string.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	_, err = in.CallValues("run")
	if !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "string result (2 flat values") {
		t.Fatalf("run: err = %v; want ErrUnsupportedForm naming the string result that spills. An arity "+
			"error is #995 before its interim refusal", err)
	}
}

// TestFlatWordsCountsTheCanonicalABIsFlattening holds `flatWords` to `flatten_type`'s lengths
// (CanonicalABI.md flatten_type, flatten_record, flatten_variant). A miscount either refuses a sync call that
// works or lets a spilled one reach the arity error. The `syncSpill` rows take the limits from both sides.
func TestFlatWordsCountsTheCanonicalABIsFlattening(t *testing.T) {
	u32 := ValType{Kind: VU32}
	str := ValType{Kind: VString}
	for _, tc := range []struct {
		name string
		vt   ValType
		want int
	}{
		{"u32", u32, 1},
		{"f64", ValType{Kind: VF64}, 1},
		{"string", str, 2},
		{"list", ValType{Kind: VList, Elem: &u32}, 2},
		{"record{u32}", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "a", Type: u32}}}, 1},
		{"record{u32,string}", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "a", Type: u32}, {Name: "b", Type: str}}}, 3},
		{"tuple<u32,u32>", ValType{Kind: VTuple, Elems: []ValType{u32, u32}}, 2},
		{"enum", ValType{Kind: VEnum, Labels: []string{"a", "b"}}, 1},
		{"flags 32", ValType{Kind: VFlags, Labels: make([]string, 32)}, 1},
		{"flags 33", ValType{Kind: VFlags, Labels: make([]string, 33)}, 2},
		{"option<u32>", ValType{Kind: VOption, Elem: &u32}, 2},
		{"result<_,_>", ValType{Kind: VResult}, 1},
		{"result<string,u32>", ValType{Kind: VResult, Ok: &str, Err: &u32}, 3},
		{"variant{a,b(string)}", ValType{Kind: VVariant, Cases: []VarCase{{Name: "a"}, {Name: "b", Type: &str}}}, 3},
	} {
		if got, ok := flatWords(tc.vt); !ok || got != tc.want {
			t.Errorf("flatWords(%s) = %d, %v; want %d", tc.name, got, ok, tc.want)
		}
	}

	if _, ok := flatWords(ValType{Kind: VRef}); ok {
		t.Error("flatWords counted an unresolved reference; it must report it uncountable so syncSpill leaves it alone")
	}

	sixteen := make([]NamedVal, 16)
	for i := range sixteen {
		sixteen[i] = NamedVal{Type: u32}
	}
	seventeen := append(append([]NamedVal{}, sixteen...), NamedVal{Type: u32})
	for _, tc := range []struct {
		name  string
		sig   *FuncType
		spill bool
	}{
		{"() -> u32", &FuncType{Result: &u32}, false},
		{"() -> string", &FuncType{Result: &str}, true},
		{"16 x u32 params", &FuncType{Params: sixteen}, false},
		{"17 x u32 params", &FuncType{Params: seventeen}, true},
		{"nil signature", nil, false},
	} {
		if _, got := syncSpill(tc.sig); got != tc.spill {
			t.Errorf("syncSpill(%s) = %v; want %v", tc.name, got, tc.spill)
		}
	}
}
