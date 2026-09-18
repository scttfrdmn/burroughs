// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
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
	t.Setenv("BURROUGHS_ASYNC", "0") // gate:async OFF by explicit opt-out (on is the default since the flip)
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

// TestGateAsyncGuestRunsToTheCommittedReading is the increment-4 EXIT CONDITION, and the moment the earlier
// no-op-kill test (formerly TestGateAsyncOnRefusesUnimplementedByName) anticipated in its own words:
// "increment 4 lifts it and the guest runs." With gate:async ON, p3async-hello instantiates, runs, and
// writes "Hello, world!\n" — checked byte-for-byte against the committed wasmtime reading (#759, on main
// since before any op was built). This turns the tier's chain of individually-verified ops (stream.new,
// write, drops, cancels, the subtask ops, context, the waitable-set loop) into ONE end-to-end claim against
// an independent engine's reading. The real path is host-first: write-via-stream pends the read on the
// readable end the guest hands it, and the guest's stream.write drives the copy inline — the host-first
// inline copy built synthetically in #763, now run by a real guest, with its bytes sunk to stdout.
//
// The gate-on no-op-kill invariant — a component with a genuinely unbuilt async op refuses by name, not
// silently — is preserved on SYNTHETIC subjects that still have one:
// TestGateAsyncNarrowingPermitsLowerRefusesUnbuilt (future.write 0x17, an async lift) and
// TestSynthesizedAsyncLiftRefusedAtBindByName. p3async-hello is no longer such a subject.
func TestGateAsyncGuestRunsToTheCommittedReading(t *testing.T) {
	b, err := os.ReadFile(asyncGuestWasm)
	if err != nil {
		t.Fatalf("async guest fixture missing (it is committed): %v", err)
	}
	want, err := os.ReadFile("testdata/p3async-hello.stdout")
	if err != nil {
		t.Fatalf("committed reading missing (it is committed, #759): %v", err)
	}
	t.Setenv("BURROUGHS_ASYNC", "1")
	var out bytes.Buffer
	h := NewHost(&out, io.Discard, nil)
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("gate:async on: the async guest did not instantiate: %v", err)
	}
	if runErr := in.Call("wasi:cli/run@0.3.0"); runErr != nil {
		t.Fatalf("gate:async on: the async guest did not run to completion: %v", runErr)
	}
	if out.String() != string(want) {
		t.Errorf("stdout = %q, want %q (the committed wasmtime reading) — the end-to-end claim failed", out.String(), string(want))
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
	t.Setenv("BURROUGHS_ASYNC", "0") // gate:async off by explicit opt-out (on is the default since the flip)
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
	t.Setenv("BURROUGHS_ASYNC", "0") // gate:async off by explicit opt-out (on is the default since the flip)
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
	// With the gate on, this no-callback (STACKFUL) async lift is still refused by ErrAsyncNotImplemented:
	// the stackless (callback) arm is built (2nd async guest, #785), but the stackful arm stays deferred
	// (ADR 0086's guest-driven choice). The built path — a WITH-callback lift binding and running — is
	// covered by TestAsyncLiftExitOnlyResolvesViaTaskReturn.
	t.Setenv("BURROUGHS_ASYNC", "1")
	onErr := gateAsync(c)
	if onErr == nil || !errors.Is(onErr, ErrAsyncNotImplemented) {
		t.Fatalf("gate:async on: no-callback (stackful) async lift not refused by ErrAsyncNotImplemented: %v", onErr)
	}
}

// TestAsyncStreamValueTypeRefusedAtBind witnesses the defense-in-depth arm: a stream value type (async
// surface) in a component's types refuses at bind under gate:async even absent a canon marker.
func TestAsyncStreamValueTypeRefusedAtBind(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "0") // gate:async off by explicit opt-out (on is the default since the flip)
	c := &Component{Types: []TypeDef{{Kind: TDVal, Val: ValType{Kind: VStream}}}}
	err := gateAsync(c)
	if err == nil || !errors.Is(err, ErrAsyncGated) {
		t.Fatalf("a stream value type was not refused by gate:async: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "stream") {
		t.Errorf("refusal %q must name the stream value type", got)
	}
}

// TestGateAsyncOffByEnvStillRefusesByName is the FLIP's rollback witness, pre-registered on #792 before the
// flip landed (#714's lesson: a gated entry's forecast registers the refusal, not only the enabled exit).
// Both halves are asserted, because the flip is a claim about a default and a rollback is a claim about an
// opt-out:
//
//   - **The default runs.** With no explicit opt-out, the async guest instantiates — that IS the flip.
//   - **`BURROUGHS_ASYNC=0` still refuses, by name**, with ErrAsyncGated (wrapped to ErrGated at the public
//     boundary → CLI exit 6, grave #301's classification). The rollback is the revert of the flip commit;
//     this is what makes it *witnessed* rather than asserted — the same refuse-by-name path as before the
//     flip, only the default differs (the form gate:components set on 2026-09-11).
//
// A flip that made `=0` a silent no-op — the gate present in name but not in effect — fails the second half.
func TestGateAsyncOffByEnvStillRefusesByName(t *testing.T) {
	b, err := os.ReadFile(asyncGuestWasm)
	if err != nil {
		t.Fatalf("async guest fixture missing (it is committed): %v", err)
	}

	// Half 1 — the default is ON as of the flip: no opt-out, the guest instantiates.
	t.Setenv("BURROUGHS_ASYNC", "") // absent/empty is not "0", so the gate is on — the flipped default
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("the flipped default did not instantiate the async guest (the flip's own claim): %v", err)
	}
	in.Close()

	// Half 2 — the rollback: an explicit opt-out still refuses, by name.
	t.Setenv("BURROUGHS_ASYNC", "0")
	_, err = InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err == nil {
		t.Fatal("BURROUGHS_ASYNC=0 did not refuse — the gate is present in name but not in effect, and the " +
			"rollback would be unwitnessed")
	}
	if !errors.Is(err, ErrAsyncGated) {
		t.Fatalf("the opt-out's refusal is not ErrAsyncGated (the sentinel the public boundary wraps as "+
			"ErrGated for exit 6): %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "gate:async") {
		t.Errorf("refusal %q must name gate:async — refuse by name, never a silent decline", got)
	}
}
