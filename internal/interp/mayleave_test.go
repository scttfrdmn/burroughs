// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestMayLeaveIsFalseExactlyInsideARealloc is #990's mechanism witness at the interp layer: a canon
// adapter called from inside a `cabi_realloc` sees `MayLeave() == false`, and the same adapter called
// from ordinary guest code — before the realloc and after it returns — sees `true`.
//
// The component layer's witness (`TestAReallocThatCallsABuiltInTraps`) reaches the lift's realloc through
// `InvokeMayNotLeave`; this one reaches `CanonCaller.Realloc`, the lower's realloc, which lowers a
// host-produced result. The two sites clear the flag separately, so each has its own witness.
//
// **Neuter, run:** deleting `c.st.mayNotLeave++` from `CanonCaller.Realloc` fails the inside reading;
// leaving out the matching `--` fails the after reading.
func TestMayLeaveIsFalseExactlyInsideARealloc(t *testing.T) {
	var seen []bool
	probe := CanonLowerExtern(ft(nil, nil), func(c *CanonCaller, _ []Value) ([]Value, error) {
		seen = append(seen, c.MayLeave())
		return nil, nil
	}, CanonOptions{})
	provider := hostLink(t, `(module
		(import "h" "probe" (func $probe))
		(memory (export "mem") 1)
		(func (export "cabi_realloc") (param i32 i32 i32 i32) (result i32)
			(call $probe)
			(i32.const 64)))`, binary.Features{}, hostImports(map[string]Extern{"probe": probe}))
	mem, _ := provider.Export("mem")
	realloc, _ := provider.Export("cabi_realloc")
	lower := CanonLowerExtern(ft(nil, []binary.ValType{binary.I32}), func(c *CanonCaller, _ []Value) ([]Value, error) {
		p, err := c.Realloc(0, 0, 4, 8)
		return []Value{I32(int32(p))}, err
	}, CanonOptions{Memory: &mem, Realloc: &realloc})
	caller := hostLink(t, `(module
		(import "h" "probe" (func $probe))
		(import "h" "lower" (func $lower (result i32)))
		(func (export "call") (result i32) (local i32)
			(call $probe)
			(local.set 0 (call $lower))
			(call $probe)
			(local.get 0)))`, binary.Features{}, hostImports(map[string]Extern{"probe": probe, "lower": lower}))

	res, err := caller.Invoke("call")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res[0].Int32() != 64 {
		t.Fatalf("call() = %d; want 64, the realloc's pointer", res[0].Int32())
	}
	want := []bool{true, false, true} // before, inside the realloc, after
	if len(seen) != len(want) {
		t.Fatalf("probe ran %d times; want %d (before, inside, after)", len(seen), len(want))
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("probe %d saw MayLeave() = %v; want %v (readings: before, inside the realloc, after)",
				i, seen[i], w)
		}
	}
}
