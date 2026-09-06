// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// withoutReservation makes `reserveMapping` refuse for one test and restores it.
//
// **Every test whose subject is the reallocating grow needs this, and that is decision 0076's shape rather
// than a testing inconvenience.** A memory reserved to its ceiling never relocates, so the arms ADR 0058's
// publication and ADR 0073's refusal are about have no population on the mapping path — and the tests that
// name those arms already guard against exactly this, by asserting `cap == len` before they grow. Those
// guards *fired* when the mapping landed, which is the strongest evidence the mechanism is live that this
// slice has: the fixtures said, unprompted, that the array they expected to move no longer moves.
//
// So the repair is to point them at the path the arm still lives on rather than to weaken them. The
// fallback is not a hypothetical: it is the whole of `windows`, `plan9` and the wasm ports, and any host
// whose overcommit policy or address-space rlimit refuses a 4 GiB mapping.
//
// The restore is a `t.Cleanup` rather than a `defer` in each caller, because a `t.Fatalf` between a manual
// set and a `defer` would leave the rest of the package running against the fallback — which fails nothing
// and quietly deletes the mapping path's coverage. Safe because no test in this package calls
// `t.Parallel()`, a premise `internal/interp/boundary.go` and `internal/interp/host.go` already rely on.
func withoutReservation(t *testing.T) {
	t.Helper()

	was := reserveMapping
	reserveMapping = func(int) ([]byte, bool) { return nil, false }
	t.Cleanup(func() { reserveMapping = was })
}

// TestTheFallbackPathIsTheOneWithoutAMapping is the other half of *assert the arms differ*: it drives the
// same constructor with the primitive refusing and asserts the memory came out on the allocator's path,
// counted.
//
// **Without this the seam is unverified in both directions.** A `reserveMapping` that returned success
// while doing nothing useful, or a `withoutReservation` that failed to take effect, would leave every test
// in `internal/interp/reserve_unix_test.go` passing and every test that relies on the helper asserting the
// wrong arm. This one is untagged and never skips, so it is also the whole of what the `!unix` ports assert
// about decision 0076 — there the fallback is not an arm, it is the mechanism.
func TestTheFallbackPathIsTheOneWithoutAMapping(t *testing.T) {
	withoutReservation(t)

	before := reservationUnavailable.Load()
	mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 1, Max: 4096, HasMax: true}})
	if err != nil {
		t.Fatalf("newMemory: %v", err)
	}
	if got := reservationUnavailable.Load() - before; got != 1 {
		t.Fatalf("reservationUnavailable moved by %d, want 1: a memory that got no mapping is "+
			"exactly what this counter is for, and an uncounted degradation to O(size) growth "+
			"is indistinguishable from conformance from outside", got)
	}
	if img := mem.img.Load().bytes; cap(img) != len(img) {
		t.Errorf("the fallback gave a %d-byte memory %d bytes of capacity, want no reservation: "+
			"an unshared memory with no declaration to reserve against takes `make([]byte, n)`",
			len(img), cap(img))
	}
	if mem.noMove {
		t.Errorf("noMove is set on a memory that reserved nothing, which would put every " +
			"growth on the engine-limit refusal arm rather than on the relocating one")
	}
}

// TestNoExportedMethodReturnsASliceAliasingAMemory is the property the cleanup's safety rests on, as a
// control rather than as a sentence in a comment.
//
// **The failure it exists to prevent is a use-after-free with the GC's help.** `runtime.AddCleanup` fires
// when the `*memory` is unreachable. An embedder holding a subslice of the backing array keeps the *bytes*
// reachable and the `*memory` not, so the cleanup unmaps memory the embedder is still reading — and reading
// unmapped address space is a `SIGSEGV` the Go runtime does not turn into a panic. Today
// `internal/interp/host.go:Caller.Read` copies, which is what makes the whole scheme sound; this fires if
// a future edit makes it return a window instead, whatever the motivation.
//
// **The domain is derived, not listed.** The methods that could hand bytes out come from reflecting over
// the boundary types rather than from a list in this test, because *a comment's caller list is not the call
// graph*: a new exported `[]byte`-returning method would otherwise be born uncovered, and the one that
// mattered would be the one somebody added for performance. Every such method must have a probe here, and
// the absence of a probe is a failure rather than a gap.
func TestNoExportedMethodReturnsASliceAliasingAMemory(t *testing.T) {
	var got []byte
	in := hostLink(t, `(module
	  (import "h" "peek" (func $peek))
	  (memory 1 64)
	  (func (export "go") (call $peek)))`,
		binary.Features{}, hostImports(map[string]Extern{
			"peek": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				bs, err := c.Read(0, pageSize)
				got = bs
				return nil, err
			}),
		}))

	img := in.mems[0].img.Load().bytes
	base := uintptr(unsafe.Pointer(&img[0]))
	end := base + uintptr(cap(img))

	// probes covers, by method name, every exported way out. `Caller.Read` is the only one today.
	probes := map[string]func() []byte{
		"Read": func() []byte {
			if _, err := in.Invoke("go"); err != nil {
				t.Fatalf("invoke: %v", err)
			}
			return got
		},
	}

	// The derived domain: exported methods on the boundary types whose results include a []byte.
	byteSlice := reflect.TypeOf([]byte(nil))
	for _, rt := range []reflect.Type{reflect.TypeOf(&Caller{}), reflect.TypeOf(&Instance{})} {
		for i := range rt.NumMethod() {
			m := rt.Method(i)
			hands := false
			for j := range m.Type.NumOut() {
				if m.Type.Out(j) == byteSlice {
					hands = true
				}
			}
			if !hands {
				continue
			}
			if _, ok := probes[m.Name]; !ok {
				t.Errorf("%s.%s returns a []byte and no probe here reads it.\n"+
					"Every exported method that can hand an embedder bytes out of a "+
					"memory is in this control's domain, because decision 0076's "+
					"cleanup is safe only while none of them aliases the backing "+
					"array. Add a probe — do not narrow the domain",
					rt.Elem().Name(), m.Name)
			}
		}
	}
	if len(probes) == 0 {
		t.Fatal("no probe reached a memory's bytes, so this control asserts nothing")
	}

	for name, probe := range probes {
		bs := probe()
		if len(bs) != pageSize {
			t.Errorf("%s returned %d bytes, want %d: the probe is not reading the whole page "+
				"and an aliasing window might sit outside what it looked at", name, len(bs), pageSize)
		}
		if len(bs) == 0 {
			continue
		}
		if p := uintptr(unsafe.Pointer(&bs[0])); p >= base && p < end {
			t.Errorf("%s returned a slice aliasing the memory's backing array.\n"+
				"That is a use-after-free once `runtime.AddCleanup` unmaps the reservation: "+
				"the bytes stay reachable, the `*memory` does not, and the embedder reads "+
				"unmapped address space — a SIGSEGV the Go runtime does not turn into a "+
				"panic. The boundary must copy: see decision 0076's lifetime section", name)
		}
	}
}
