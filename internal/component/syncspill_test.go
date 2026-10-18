// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"strings"
	"testing"
)

// TestASyncCrossComponentCallCrossesAsValues is ADR 0103's witness: a sync cross-component call whose values
// carry a pointer, or whose result spills, crosses as component values and reads the oracle's answer. It was
// #995's interim refusal witness, and is converted, as the chair's ruling ordered, to assert values.
//
//   - **`sync-spill-string.wasm`:** a `string` result spills. wasmtime 49.0.3 copies it and reads 3, 10 of 10.
//     Burroughs failed on arity, then refused by name as the interim.
//   - **`sync-string-param.wasm`:** a `string` parameter is two words and does not spill, but they point into
//     the caller's memory. wasmtime reads 97, 10 of 10. Burroughs read **120**, the child reading its own
//     memory at the caller's address, then refused by name.
//
// Both readings are in `sync-spill-string.reading`.
//
// **Neuters, run, one per half of `syncAdapter`, each failing its own fixture only:**
//   - **no lift**, the callee's flat words returned instead of its lifted result: `sync-spill-string` fails on
//     arity again (*"declares 0 result(s) and returned 1"*);
//   - **no lower**, the caller's words handed to the callee instead of the values lowered into its memory:
//     `sync-string-param` reads 120 again.
func TestASyncCrossComponentCallCrossesAsValues(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    uint32
	}{
		{"sync-spill-string.wasm", 3},
		{"sync-string-param.wasm", 97},
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
			if err != nil {
				t.Fatalf("run: %v; want %d, wasmtime's reading", err, tc.want)
			}
			if v, ok := res[0].U32(); len(res) != 1 || !ok || v != tc.want {
				t.Fatalf("run() = %v, want u32 %d (wasmtime's reading). 120 on the parameter fixture is a "+
					"string read from the wrong memory", res, tc.want)
			}
		})
	}
}

// TestASyncLiftsPostReturnRunsAfterItsResult is #1058's witness, built with ADR 0103. A sync lift's
// `post-return` must run once, after its result is lifted, as the reference's `def:canon_lift` runs it.
// `post-return-runs.wasm`'s `run()` reads 101 on wasmtime 49.0.3, 10 of 10 (`post-return-runs.reading`), and
// read 100 on Burroughs, which decoded the canonopt and never ran it.
//
// **Neuter, run:** skipping the `post-return` call in `invokeSync` reads 100.
func TestASyncLiftsPostReturnRunsAfterItsResult(t *testing.T) {
	b, err := os.ReadFile("testdata/post-return-runs.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	res, err := in.CallValues("run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v, ok := res[0].U32(); len(res) != 1 || !ok || v != 101 {
		t.Fatalf("run() = %v, want u32 101: get returned 42 and its post-return ran once. 100 is #1058, "+
			"post-return never run", res)
	}
}

// TestASpilledParameterListStaysRefusedByName holds ADR 0103's unmodelled list to its word for the one shape
// the adapter does not carry: parameters past MAX_FLAT_PARAMS. They are refused by name with ErrUnsupportedForm,
// not passed through.
func TestASpilledParameterListStaysRefusedByName(t *testing.T) {
	u32 := ValType{Kind: VU32}
	seventeen := make([]NamedVal, 17)
	for i := range seventeen {
		seventeen[i] = NamedVal{Type: u32}
	}
	form, spills := paramSpill(&FuncType{Params: seventeen})
	if !spills || !strings.Contains(form, "17 flat values") {
		t.Fatalf("paramSpill(17 x u32) = %q, %v; want a spill naming 17 flat values", form, spills)
	}
}

// TestFlatWordsCountsTheCanonicalABIsFlattening holds `flatWords` to `flatten_type`'s lengths
// (CanonicalABI.md flatten_type, flatten_record, flatten_variant). A miscount either sends a sync call to the
// adapter that the passthrough carries, or lets a spilled one through to the arity error. The predicate rows
// take the limits from both sides.
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
		t.Error("flatWords counted an unresolved reference; it must report it uncountable so the passthrough keeps it")
	}

	sixteen := make([]NamedVal, 16)
	for i := range sixteen {
		sixteen[i] = NamedVal{Type: u32}
	}
	seventeen := append(append([]NamedVal{}, sixteen...), NamedVal{Type: u32})
	for _, tc := range []struct {
		name        string
		sig         *FuncType
		adapter     bool
		paramsSpill bool
	}{
		{"() -> u32", &FuncType{Result: &u32}, false, false},
		{"() -> string", &FuncType{Result: &str}, true, false},
		{"(s: string) -> u32", &FuncType{Params: []NamedVal{{Name: "s", Type: str}}, Result: &u32}, true, false},
		{"(r: record{a: u32}) -> u32", &FuncType{Params: []NamedVal{{Name: "r", Type: ValType{
			Kind:   VRecord,
			Fields: []NamedVal{{Name: "a", Type: u32}},
		}}}, Result: &u32}, false, false},
		{"() -> option<string>", &FuncType{Result: &ValType{Kind: VOption, Elem: &str}}, true, false},
		{"() -> tuple<u32,u32>", &FuncType{Result: &ValType{Kind: VTuple, Elems: []ValType{u32, u32}}}, true, false},
		{"(h: own) -> u32", &FuncType{Params: []NamedVal{{Name: "h", Type: ValType{Kind: VOwn}}}, Result: &u32}, false, false},
		{"16 x u32 params", &FuncType{Params: sixteen}, false, false},
		{"17 x u32 params", &FuncType{Params: seventeen}, false, true},
		{"nil signature", nil, false, false},
	} {
		if got := syncNeedsAdapter(tc.sig); got != tc.adapter {
			t.Errorf("syncNeedsAdapter(%s) = %v; want %v", tc.name, got, tc.adapter)
		}
		if _, got := paramSpill(tc.sig); got != tc.paramsSpill {
			t.Errorf("paramSpill(%s) = %v; want %v", tc.name, got, tc.paramsSpill)
		}
	}
}
