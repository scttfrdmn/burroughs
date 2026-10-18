// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestASyncCrossComponentSpillIsRefusedByName is #995's interim witness (the chair's ruling of 2026-10-18). A
// sync cross-component call whose values cannot cross as flat words must be refused by name, with
// ErrUnsupportedForm naming the form. Both of the arm's faces are covered:
//
//   - **`sync-spill-string.wasm`, loud:** a `string` result spills. wasmtime 49.0.3 copies it and reads 3, 10
//     of 10. Burroughs failed on arity, *"interp: "get" takes 0 arguments, got 1"*.
//   - **`sync-string-param.wasm`, silent:** a `string` parameter is two words and does not spill, but they
//     point into the caller's memory. wasmtime reads 97, 10 of 10. Burroughs read **120**: the child read its
//     own memory at the caller's address and returned a plausible wrong byte.
//
// Both readings are in `sync-spill-string.reading`. This is a stated limit, not conformance: the conformant
// fix is the memory-to-memory adapter, held in an ADR draft.
//
// **Neuter, run:** skipping the `syncSpill` refusal in `bindCrossComponent`. The spill arm reads the arity
// error again, and the parameter arm reads 120.
func TestASyncCrossComponentSpillIsRefusedByName(t *testing.T) {
	for _, tc := range []struct{ fixture, phrase string }{
		{"sync-spill-string.wasm", "string result"},
		{"sync-string-param.wasm", `parameter "s" (a string`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + tc.fixture)
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			in, err := Instantiate(b)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			res, err := in.CallValues("run")
			if !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), tc.phrase) {
				t.Fatalf("run: res = %v, err = %v; want ErrUnsupportedForm naming the %s. An arity error, "+
					"or a value read from the wrong memory, is #995 before its interim refusal", res, err, tc.phrase)
			}
		})
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
		{"(s: string) -> u32", &FuncType{Params: []NamedVal{{Name: "s", Type: str}}, Result: &u32}, true},
		{"(r: record{a: u32}) -> u32", &FuncType{Params: []NamedVal{{Name: "r", Type: ValType{
			Kind:   VRecord,
			Fields: []NamedVal{{Name: "a", Type: u32}},
		}}}, Result: &u32}, false},
		{"() -> option<string>", &FuncType{Result: &ValType{Kind: VOption, Elem: &str}}, true},
		{"(h: own) -> u32", &FuncType{Params: []NamedVal{{Name: "h", Type: ValType{Kind: VOwn}}}, Result: &u32}, false},
		{"16 x u32 params", &FuncType{Params: sixteen}, false},
		{"17 x u32 params", &FuncType{Params: seventeen}, true},
		{"nil signature", nil, false},
	} {
		if _, got := syncSpill(tc.sig); got != tc.spill {
			t.Errorf("syncSpill(%s) = %v; want %v", tc.name, got, tc.spill)
		}
	}
}
