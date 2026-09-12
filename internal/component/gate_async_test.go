// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

const asyncGuestWasm = "testdata/p3async-hello.wasm"

// The gate:async shell (ADR 0086): a component that uses the async component-model ABI decodes but is
// refused at bind, by name, while gate:async is off. These tests witness the refusal FIRING on each arm
// (the #732 standard — a permit-only test is a permissiveness test), keyed on the async canonopt read from
// the canon section, not on a world name. The lower arm is the real guest's (its run export is
// sync-lifted, its imports async-lowered — #734); the lift arm is witnessed from decoded bytes and a
// constructed async lift, since no guest lifts async yet.

// TestAsyncSurfaceDecodesNotRefusedAtLoad witnesses that the async surface is recognized, not fatal, at
// decode — the guest Loads (every canon def wasm-tools sees is advanced past) so the refusal can fire at
// bind. If Load refused here, the gate:async refusal would be a decode "not modeled", not a named gate.
func TestAsyncSurfaceDecodesNotRefusedAtLoad(t *testing.T) {
	b, err := os.ReadFile(asyncGuestWasm)
	if err != nil {
		t.Fatalf("async guest fixture missing (it is committed): %v", err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("Load refused the async guest — the surface must decode so the refusal fires at bind: %v", err)
	}
	// 35 canon defs (matches `wasm-tools print | grep -c '(canon '`), of which the async lowers and the
	// async built-ins are async surface.
	if len(c.Canons) != 35 {
		t.Errorf("decoded %d canons, want 35 (the wasm-tools count — decode must advance past every async built-in)", len(c.Canons))
	}
	nAsync := 0
	for _, cn := range c.Canons {
		if cn.isAsync() {
			nAsync++
		}
	}
	if nAsync == 0 {
		t.Fatal("no async canon recognized — the lower/built-in surface was not decoded")
	}
}

// TestAsyncGuestRefusedAtBindByName is the LOWER arm, on the real guest's bytes: with gate:async off, the
// guest is refused at bind, by name, naming gate:async — and the refusal fires because the guest's imports
// are async-lowered (the async canonopt on a lower), not because it lifts async (its lift is sync). This is
// the #732-shape hole a lift-only check would leave open.
func TestAsyncGuestRefusedAtBindByName(t *testing.T) {
	b, err := os.ReadFile(asyncGuestWasm)
	if err != nil {
		t.Fatalf("async guest fixture missing (it is committed): %v", err)
	}
	t.Setenv("BURROUGHS_ASYNC", "") // the default build: gate:async is off
	h := NewHost(io.Discard, io.Discard, nil)
	_, err = InstantiateWithHost(b, h)
	if err == nil {
		t.Fatal("gate:async off: the async guest instantiated — a lift-only check would let its async lowers through")
	}
	if !errors.Is(err, ErrAsyncGated) {
		t.Fatalf("gate:async off: refusal is not ErrAsyncGated: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "gate:async") || !strings.Contains(got, "lower") {
		t.Errorf("refusal %q must name gate:async and the async lower (the firing arm)", got)
	}
}

// TestGateAsyncOnDoesNotRefuse witnesses that the gate is a gate: with gate:async on, the async surface is
// NOT refused by gate:async. Nothing runs behind it yet (slice 1 is the enabled exit), so this asserts only
// that the gate:async refusal does not fire — not that the guest runs.
func TestGateAsyncOnDoesNotRefuse(t *testing.T) {
	b, err := os.ReadFile(asyncGuestWasm)
	if err != nil {
		t.Fatalf("async guest fixture missing (it is committed): %v", err)
	}
	t.Setenv("BURROUGHS_ASYNC", "1")
	h := NewHost(io.Discard, io.Discard, nil)
	if _, err := InstantiateWithHost(b, h); err != nil && errors.Is(err, ErrAsyncGated) {
		t.Fatalf("gate:async on: still refused by gate:async — the gate did not open: %v", err)
	}
}

// TestAsyncCanonoptDecodesOnLift is the LIFT arm's decode witness, from real bytes: a canon lift carrying
// the async canonopt (0x06) decodes to a lift with Opts.Async set. This is the marker the gate keys on;
// witnessing it on a lift proves the lift arm is not untested just because no guest lifts async.
func TestAsyncCanonoptDecodesOnLift(t *testing.T) {
	// canon lift: 0x00 (lift) 0x00 (func sort) 0x00 (core funcidx) 0x01 (opts count) 0x06 (async) 0x00 (typeidx)
	r := &reader{b: []byte{0x00, 0x00, 0x00, 0x01, 0x06, 0x00}}
	cn, err := r.canon()
	if err != nil {
		t.Fatalf("decoding an async canon lift: %v", err)
	}
	if cn.Kind != CanonLift {
		t.Fatalf("kind = %v, want CanonLift", cn.Kind)
	}
	if !cn.Opts.Async {
		t.Fatal("the async canonopt (0x06) on a lift did not set Opts.Async")
	}
}

// TestAsyncLiftRefusedAtBindByName is the LIFT arm's refusal witness: a component whose canon section holds
// an async lift is refused at bind, by name, naming gate:async and the lift arm. Built at the graph level
// because a real-bytes async lift referencing a core func is refused at Load first (the forward-reference
// check, TestForwardReferenceRefusedOnTheStream), so a full valid synthetic component would be needed to
// reach bind; the marker's decode-from-bytes is witnessed by TestAsyncCanonoptDecodesOnLift.
func TestAsyncLiftRefusedAtBindByName(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "") // gate:async off
	c := &Component{Canons: []Canon{{Kind: CanonLift, Opts: CanonOpts{Async: true}}}}
	err := gateAsync(c)
	if err == nil {
		t.Fatal("an async canon lift was not refused with gate:async off")
	}
	if !errors.Is(err, ErrAsyncGated) {
		t.Fatalf("refusal is not ErrAsyncGated: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "gate:async") || !strings.Contains(got, "lift") {
		t.Errorf("refusal %q must name gate:async and the async lift", got)
	}
	// And with the gate on, the same lift is not refused by gate:async.
	t.Setenv("BURROUGHS_ASYNC", "1")
	if err := gateAsync(c); err != nil {
		t.Fatalf("gate:async on: async lift still refused: %v", err)
	}
}

// TestAsyncStreamValueTypeRefusedAtBind witnesses the defense-in-depth arm: a stream value type (async
// surface) in a component's types refuses at bind under gate:async even absent a canon marker.
func TestAsyncStreamValueTypeRefusedAtBind(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "") // gate:async off
	c := &Component{Types: []TypeDef{{Kind: TDVal, Val: ValType{Kind: VStream}}}}
	err := gateAsync(c)
	if err == nil || !errors.Is(err, ErrAsyncGated) {
		t.Fatalf("a stream value type was not refused by gate:async: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "stream") {
		t.Errorf("refusal %q must name the stream value type", got)
	}
}
