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

// TestGateAsyncOnRefusesUnimplementedByName is the gate-on no-op kill (#739 slice 1): with gate:async ON,
// the async guest is NOT silently instantiated (which then trapped obscurely at run on a missing async
// runtime import) — it refuses at bind, by name, with ErrAsyncNotImplemented naming gate:async and that
// the tier's execution is not yet built. Distinct from the gate-off ErrAsyncGated: the gate is open, the
// mechanism is not there yet. As increments land this narrows; increment 4 lifts it and the guest runs.
func TestGateAsyncOnRefusesUnimplementedByName(t *testing.T) {
	b, err := os.ReadFile(asyncGuestWasm)
	if err != nil {
		t.Fatalf("async guest fixture missing (it is committed): %v", err)
	}
	t.Setenv("BURROUGHS_ASYNC", "1")
	h := NewHost(io.Discard, io.Discard, nil)
	_, err = InstantiateWithHost(b, h)
	if err == nil {
		t.Fatal("gate:async on: the async guest instantiated silently — a nil no-op; an unbuilt sub-path must refuse by name")
	}
	if !errors.Is(err, ErrAsyncNotImplemented) {
		t.Fatalf("gate:async on: refusal is not ErrAsyncNotImplemented: %v", err)
	}
	if errors.Is(err, ErrAsyncGated) {
		t.Fatalf("gate:async on: refused as ErrAsyncGated (gate off), but the gate is on: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "gate:async") || !strings.Contains(got, "not yet implemented") {
		t.Errorf("refusal %q must name gate:async and that execution is not yet implemented", got)
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

// TestSynthesizedAsyncLiftRefusedAtBindByName is the LIFT arm's refusal witness on real bytes, mirroring
// how the real guest witnesses the lower arm: a full, valid synthesized component (async-lift-synth.wasm —
// a core module exporting a trivial func, a core instance, a core-func alias, then a canon lift with the
// async canonopt over an async functype) Loads cleanly (the core func exists before the lift references it,
// so the forward-reference check passes), then is refused at bind, by name, naming gate:async and the lift
// arm — the refusal firing on synthesized bytes through Load→bind, not on a hand-built graph.
func TestSynthesizedAsyncLiftRefusedAtBindByName(t *testing.T) {
	b, err := os.ReadFile("testdata/async-lift-synth.wasm")
	if err != nil {
		t.Fatalf("synthesized async-lift fixture missing (it is committed): %v", err)
	}
	if _, lerr := Load(b); lerr != nil {
		t.Fatalf("Load refused the synthesized async-lift component — it must decode so the refusal fires at bind: %v", lerr)
	}
	t.Setenv("BURROUGHS_ASYNC", "") // gate:async off
	_, err = Instantiate(b)
	if err == nil {
		t.Fatal("gate:async off: the synthesized async lift instantiated — the lift arm was not refused")
	}
	if !errors.Is(err, ErrAsyncGated) {
		t.Fatalf("gate:async off: refusal is not ErrAsyncGated: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "gate:async") || !strings.Contains(got, "lift") {
		t.Errorf("refusal %q must name gate:async and the async lift (the firing arm)", got)
	}
	// With the gate on, the same bytes are refused by ErrAsyncNotImplemented (the gate opened; execution is
	// unbuilt), not by ErrAsyncGated (the gate off) — the no-op kill on the lift arm.
	t.Setenv("BURROUGHS_ASYNC", "1")
	_, onErr := Instantiate(b)
	if onErr == nil || !errors.Is(onErr, ErrAsyncNotImplemented) {
		t.Fatalf("gate:async on: synthesized async lift not refused by ErrAsyncNotImplemented: %v", onErr)
	}
	if errors.Is(onErr, ErrAsyncGated) {
		t.Fatalf("gate:async on: refused as ErrAsyncGated but the gate is on: %v", onErr)
	}
}

// TestAsyncLiftRefusedAtBindByName is a unit test of gateAsync on the lift arm at the graph level (the
// bytes path is TestSynthesizedAsyncLiftRefusedAtBindByName): a component whose canon section holds an
// async lift is refused, by name, naming gate:async and the lift arm.
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
	// And with the gate on, gateAsync refuses the same lift by ErrAsyncNotImplemented (gate open, execution
	// unbuilt), not by ErrAsyncGated — the no-op kill.
	t.Setenv("BURROUGHS_ASYNC", "1")
	onErr := gateAsync(c)
	if onErr == nil || !errors.Is(onErr, ErrAsyncNotImplemented) {
		t.Fatalf("gate:async on: async lift not refused by ErrAsyncNotImplemented: %v", onErr)
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
