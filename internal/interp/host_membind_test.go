// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestHostExternWithMemoryLiftsAgainstTheBoundMemory is the memory-binding increment's exit (ADR 0084):
// a host function reads the memory named in its binding, not the declaring instance's memory 0. It is
// the canonical-ABI adapter's need — a canon lower's `(memory $m)` names the memory the guest arguments
// index, but the `$imports` trampoline that dispatches the adapter has no memory of its own.
//
// The two instances are the whole point: `provider` owns the memory and writes "hello" into it; the
// host function is bound to *that* memory; `caller` has **no memory at all** and imports the host
// function. If the binding is honored, the host reads `provider`'s bytes from inside a call made by a
// memory-less instance — which without the binding is the "no memory 0" error the second half witnesses.
func TestHostExternWithMemoryLiftsAgainstTheBoundMemory(t *testing.T) {
	provider := hostLink(t, `(module
		(memory (export "mem") 1)
		(data (i32.const 0) "hello"))`, binary.Features{}, nil)
	mem, ok := provider.Export("mem")
	if !ok || mem.Kind != binary.ExternMemory {
		t.Fatal("provider does not export its memory")
	}

	// peek reads the first byte of its bound memory ('h' = 104) and returns it.
	peek := func(c *Caller, _ []Value) ([]Value, error) {
		b, err := c.Read(0, 1)
		if err != nil {
			return nil, err
		}
		return []Value{I32(int32(b[0]))}, nil
	}
	peekType := ft(nil, []binary.ValType{binary.I32})

	callerSrc := `(module
		(import "h" "peek" (func $peek (result i32)))
		(func (export "call") (result i32) (call $peek)))`

	t.Run("a bound memory is read from a memory-less caller", func(t *testing.T) {
		caller := hostLink(t, callerSrc, binary.Features{},
			hostImports(map[string]Extern{"peek": HostExternWithMemory(peekType, peek, mem)}))
		res, err := caller.Invoke("call")
		if err != nil {
			t.Fatalf("call: %v (the bound memory should be reachable though the caller has none)", err)
		}
		if len(res) != 1 || res[0].Int32() != 'h' {
			t.Errorf("call() = %v, want [104] ('h' from the provider's memory through the binding)", res)
		}
	})

	// The witness: without the binding, the same host function called from the same memory-less caller
	// reaches for the declaring instance's memory 0 and finds none — the error the binding removes.
	t.Run("without the binding the memory-less caller has no memory", func(t *testing.T) {
		caller := hostLink(t, callerSrc, binary.Features{},
			hostImports(map[string]Extern{"peek": HostExtern(peekType, peek)}))
		_, err := caller.Invoke("call")
		if err == nil || !errors.Is(err, ErrNoMemory) {
			t.Fatalf("call: err = %v, want ErrNoMemory (an unbound host func has no memory in a memory-less instance)", err)
		}
	})
}
