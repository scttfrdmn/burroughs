// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The canonical-ABI adapter (ADR 0084, §5 H-2 amended): a canon lower dispatched by `callAdapter`,
// whose impl receives a `*CanonCaller` that can invoke the lower's `cabi_realloc` as guest execution.
// These are the registered exit assertions on #694.

// providerModule instantiates a module exporting a memory and a `cabi_realloc(i32,i32,i32,i32)->i32`.
// `spinTrips` bakes a loop into realloc so a Stop/Close can catch the agent inside it; 0 makes realloc a
// constant that returns `retPtr` immediately.
func providerModule(t *testing.T, spinTrips int, retPtr int32) *Instance {
	t.Helper()
	body := ""
	if spinTrips > 0 {
		// A back-edged loop of spinTrips iterations, then the pointer — every iteration crosses a
		// safepoint poll, so a sticky Stop parks here and a Close terminates here.
		body = `(local.set 4 (i32.const ` + itoa(uint64(spinTrips)) + `))
			(loop
				(local.set 4 (i32.sub (local.get 4) (i32.const 1)))
				(br_if 0 (local.get 4)))`
	}
	src := `(module
		(memory (export "mem") 1)
		(func (export "cabi_realloc") (param i32 i32 i32 i32) (result i32) (local i32)
			` + body + `
			(i32.const ` + itoa(uint64(retPtr)) + `)))`
	return hostLink(t, src, binary.Features{}, nil)
}

// adapterCallerModule instantiates a module importing an adapter `h.lower : () -> i32` and calling it.
// The adapter is a canon lower bound to `provider`'s memory and realloc; its impl allocates a zero-byte
// buffer through the guest's realloc and returns the pointer — the model's `store_list_into_range` for
// an empty list (definitions.py: `cx.allocate` is called even at length 0).
func adapterCallerModule(t *testing.T, provider *Instance, onEnter func()) *Instance {
	t.Helper()
	mem, ok := provider.Export("mem")
	if !ok || mem.Kind != binary.ExternMemory {
		t.Fatal("provider exports no memory")
	}
	realloc, ok := provider.Export("cabi_realloc")
	if !ok || realloc.Kind != binary.ExternFunc {
		t.Fatal("provider exports no cabi_realloc")
	}
	lower := CanonLowerExtern(
		ft(nil, []binary.ValType{binary.I32}),
		func(c *CanonCaller, _ []Value) ([]Value, error) {
			if onEnter != nil {
				onEnter() // the impl is running: the Invoke is past its closed-instance guard
			}
			p, err := c.Realloc(0, 0, 4, 0) // allocate a zero-length list's backing
			if err != nil {
				return nil, err
			}
			return []Value{I32(int32(p))}, nil
		},
		CanonOptions{Memory: &mem, Realloc: &realloc},
	)
	return hostLink(t, `(module
		(import "h" "lower" (func $lower (result i32)))
		(func (export "call") (result i32) (call $lower)))`,
		binary.Features{}, hostImports(map[string]Extern{"lower": lower}))
}

// TestCanonAdapterReallocAtLengthZeroReturnsTheGuestPointer is the realloc exit: the adapter invokes the
// guest's `cabi_realloc` even for a zero-length allocation and gets the pointer the guest allocator
// returns — the model-faithful path condition 3 requires, replacing the `(0,0)` shortcut.
func TestCanonAdapterReallocAtLengthZeroReturnsTheGuestPointer(t *testing.T) {
	provider := providerModule(t, 0, 48)
	caller := adapterCallerModule(t, provider, nil)
	res, err := caller.Invoke("call")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(res) != 1 || res[0].Int32() != 48 {
		t.Errorf("call() = %v, want [48] — the pointer the guest's cabi_realloc returned for a "+
			"zero-length allocation", res)
	}
}

// TestCanonAdapterIsDistinctFromHostExtern is the H-2-intact assertion, structurally: a canon-lower
// adapter is dispatched by `callAdapter` (it carries `canon`, so its `CanonCaller` can re-enter the
// guest via realloc), while an embedder `HostExtern` is dispatched by `callHost` and its impl is handed
// a plain `*Caller` — which has no method that reaches guest code. The distinction is the enforcement:
// the amendment's realloc carve-out reaches only the adapter, never an embedder function.
func TestCanonAdapterIsDistinctFromHostExtern(t *testing.T) {
	embedder := HostExtern(ft(nil, nil), func(*Caller, []Value) ([]Value, error) { return nil, nil })
	if embedder.host == nil || embedder.host.canon != nil {
		t.Error("HostExtern produced an adapter — an embedder host function must dispatch through " +
			"callHost, with a plain *Caller that cannot re-enter the guest (H-2)")
	}
	adapter := CanonLowerExtern(ft(nil, nil), func(*CanonCaller, []Value) ([]Value, error) { return nil, nil }, CanonOptions{})
	if adapter.host == nil || adapter.host.canon == nil {
		t.Error("CanonLowerExtern did not produce an adapter — the canon lower must dispatch through " +
			"callAdapter so its CanonCaller can invoke realloc")
	}
	// The distinction is also a type fact the compiler enforces: a HostExtern impl is `func(*Caller, …)`
	// and `*Caller` has no Realloc, while a canon adapter is `func(*CanonCaller, …)` and only
	// `*CanonCaller` does — so an embedder function has no way to name the guest re-entry the adapter can.
}

// TestStopParksTheAgentInsideTheAdaptersRealloc is the safepoint assertion: a Stop while the adapter's
// realloc runs parks the agent at the realloc loop's poll (guest execution, honored), and Resume lets
// the call finish — the realloc is not host code the world cannot stop.
func TestStopParksTheAgentInsideTheAdaptersRealloc(t *testing.T) {
	const trips = 10_000_000 // ~half a second of guest work; a sticky Stop lands mid-loop by a wide margin
	provider := providerModule(t, trips, 64)
	caller := adapterCallerModule(t, provider, nil)

	done := make(chan []Value, 1)
	errs := make(chan error, 1)
	go func() {
		out, err := caller.Invoke("call")
		if err != nil {
			errs <- err
			return
		}
		done <- out
	}()

	if err := caller.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v — the agent is inside the adapter's realloc loop, whose every iteration "+
			"crosses a back-edge; a deadline here means the realloc is not running as guest work "+
			"(§5 H-2 amendment: it must be)", err)
	}
	select {
	case out := <-done:
		t.Fatalf("the call finished (%v) before the stop — the realloc loop is too short for the "+
			"test to be about safepoints", out)
	case err := <-errs:
		t.Fatalf("the call failed: %v", err)
	default:
	}

	caller.Resume()
	select {
	case out := <-done:
		if len(out) != 1 || out[0].Int32() != 64 {
			t.Errorf("after Resume the call returned %v, want [64] — parking and releasing inside "+
				"the realloc perturbed the stack or pc", out)
		}
	case err := <-errs:
		t.Fatalf("after Resume the call failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the call did not finish 30s after Resume — the release is not reaching the parked agent")
	}
}

// TestCloseTerminatesTheAgentInsideTheAdaptersRealloc is the fault-attribution assertion: a Close while
// the adapter's realloc runs terminates the agent at the realloc's poll and the call reports
// ErrTerminated (contract §2 T-5.4) — guest work ended at a safepoint, not a host call abandoned.
func TestCloseTerminatesTheAgentInsideTheAdaptersRealloc(t *testing.T) {
	const trips = 1_000_000_000 // long enough that Close reliably lands mid-loop before it can finish
	entered := make(chan struct{}, 1)
	provider := providerModule(t, trips, 64)
	caller := adapterCallerModule(t, provider, func() { entered <- struct{}{} })

	errs := make(chan error, 1)
	go func() { _, err := caller.Invoke("call"); errs <- err }()

	<-entered // the adapter impl is running (Invoke past its closed-instance guard) and about to realloc
	if err := caller.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-errs:
		if !errors.Is(err, ErrTerminated) {
			t.Fatalf("the call returned %v, want ErrTerminated — a Close mid-realloc must end the "+
				"agent at the realloc's safepoint as guest work (T-5.4)", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the call did not return 30s after Close — the terminal safepoint is not reaching " +
			"the agent inside the realloc")
	}
}
