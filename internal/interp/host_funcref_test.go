// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestHostFunctionIsAFirstClassFuncref is ADR 0069 Option C's exit (the positive assertion condition 2
// registered): a host function put into a funcref table and reached with `call_indirect` runs, both
// when an `elem` segment places it and when the guest round-trips it through `table.set`/`table.get` —
// the same reference either way. A mismatched declared type traps with the reference's own message.
//
// This is the capability a guest built by the component toolchains demands: the fused adapter routes
// every wasi import through an `$imports` funcref table and calls it indirectly, so a host function
// that is only callable by name (0069's pre-amendment limit) leaves that guest unable to reach its
// imports. The suite itself never exercises this — its "imported functions" are other wasm modules,
// resolved through `ext.owner`, which always worked — so it is asserted here directly rather than as a
// board delta.
func TestHostFunctionIsAFirstClassFuncref(t *testing.T) {
	// f: (i32) -> i32, returning arg+1, so a wrong-slot or wrong-reference call gives a wrong number
	// rather than an error — the value discriminates the reading.
	f := HostExtern(ft([]binary.ValType{binary.I32}, []binary.ValType{binary.I32}),
		func(_ *Caller, args []Value) ([]Value, error) {
			return []Value{I32(args[0].Int32() + 1)}, nil
		})
	imports := hostImports(map[string]Extern{"f": f})

	src := `(module
		(import "host" "f" (func $f (param i32) (result i32)))
		(type $unary (func (param i32) (result i32)))
		(type $binary (func (param i32 i32) (result i32)))
		(table 4 funcref)
		(elem (i32.const 0) $f)
		(func (export "viaElem") (param i32) (result i32)
			(call_indirect (type $unary) (local.get 0) (i32.const 0)))
		(func (export "viaRoundTrip") (param i32) (result i32)
			(table.set (i32.const 1) (ref.func $f))
			(call_indirect (type $unary) (local.get 0) (i32.const 1)))
		(func (export "viaMismatch") (param i32) (result i32)
			(call_indirect (type $binary) (local.get 0) (local.get 0) (i32.const 0))))`
	in := hostLink(t, src, binary.Features{}, imports)

	// call_indirect through the elem-filled slot reaches the host function.
	if got := invokeI32(t, in, "viaElem", 41); got != 42 {
		t.Errorf("viaElem(41) = %d, want 42 (host func not reached through the table)", got)
	}

	// table.set the host func's ref.func into a fresh slot, table.get it back via call_indirect: the
	// reference round-trips as an ordinary funcref and names the same host function.
	if got := invokeI32(t, in, "viaRoundTrip", 41); got != 42 {
		t.Errorf("viaRoundTrip(41) = %d, want 42 (host funcref did not survive table.set/get)", got)
	}

	// A call_indirect whose declared type does not match the host function's traps — structural
	// equality is the check, and the reference's own message names it.
	_, err := in.Invoke("viaMismatch", I32(1))
	if err == nil || !strings.Contains(err.Error(), "indirect call type mismatch") {
		t.Errorf("viaMismatch: err = %v, want an indirect call type mismatch trap", err)
	}
}

// TestHostFunctionReachedByCallRef is the call_ref arm of Option C (gate:gc / function-references):
// `ref.func` of an imported host function is a callable funcref through `call_ref`.
func TestHostFunctionReachedByCallRef(t *testing.T) {
	f := HostExtern(ft([]binary.ValType{binary.I32}, []binary.ValType{binary.I32}),
		func(_ *Caller, args []Value) ([]Value, error) {
			return []Value{I32(args[0].Int32() + 1)}, nil
		})
	imports := hostImports(map[string]Extern{"f": f})

	src := `(module
		(import "host" "f" (func $f (param i32) (result i32)))
		(type $unary (func (param i32) (result i32)))
		(elem declare func $f)
		(func (export "viaRef") (param i32) (result i32)
			(call_ref $unary (local.get 0) (ref.func $f))))`
	in := hostLink(t, src, binary.Features{GC: true}, imports)

	if got := invokeI32(t, in, "viaRef", 41); got != 42 {
		t.Errorf("viaRef(41) = %d, want 42 (host func not reached through call_ref)", got)
	}
}

// invokeI32 invokes a single-i32-result export and returns the result, failing on any error or a
// non-i32 result.
func invokeI32(t *testing.T, in *Instance, name string, arg int32) int32 {
	t.Helper()
	res, err := in.Invoke(name, I32(arg))
	if err != nil {
		t.Fatalf("%s(%d): %v", name, arg, err)
	}
	if len(res) != 1 {
		t.Fatalf("%s(%d): got %d results, want 1", name, arg, len(res))
	}
	return res[0].Int32()
}
