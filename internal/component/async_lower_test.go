// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The gate:async 2a-i-A wrapper witnesses run against the oracle (canon/testdata/fixtures.json's
// async_lowers) through a production-faithful CanonCaller (interp.NewCanonCallerForTest — the SAME
// construction callAdapter uses, over a real memory), not a convenience caller. Three witnesses:
// fixture-match (the sync-resolving arm returns [RETURNED] and lowers the result to the retptr, byte-for
// -byte with the model), refusal-firing (a blocking impl refuses by name), and retptr-unchanged (the
// refusal touches no guest memory — the ordering caution, witnessable only outside a trapping guest).

type asyncLowerFixture struct {
	Name       string `json:"name"`
	Args       []int  `json:"args"`
	ResultKind string `json:"result_kind"`
	RetValue   int64  `json:"ret_value"`
	Ret        []int  `json:"ret"`
	Retptr     *int   `json:"retptr"`
	MemoryHex  string `json:"memory_hex"`
}

func loadAsyncLowers(t *testing.T) []asyncLowerFixture {
	t.Helper()
	b, err := os.ReadFile("canon/testdata/fixtures.json")
	if err != nil {
		t.Fatalf("reading fixtures: %v", err)
	}
	var doc struct {
		AsyncLowers []asyncLowerFixture `json:"async_lowers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parsing fixtures: %v", err)
	}
	if len(doc.AsyncLowers) == 0 {
		t.Fatal("no async_lowers in fixtures.json")
	}
	return doc.AsyncLowers
}

// resultValue builds the canon.Value a case's callee resolves with, from the fixture's kind + value.
func resultValue(t *testing.T, kind string, v int64) (canon.Value, bool) {
	t.Helper()
	switch kind {
	case "":
		return canon.Value{}, false // empty result
	case "u32":
		return canon.U32(uint32(v)), true
	case "u64":
		return canon.U64(uint64(v)), true
	default:
		t.Fatalf("async-lower fixture result kind %q not handled by this test", kind)
		return canon.Value{}, false
	}
}

func resultSize(kind string) uint64 {
	switch kind {
	case "u32":
		return 4
	case "u64":
		return 8
	}
	return 0
}

// TestAsyncLowerMatchesTheOracle is the fixture-match: for each async_lowers case, the wrapper's
// sync-resolving arm returns [RETURNED] and lowers the resolved value to the retptr, byte-for-byte with
// the model's reading.
func TestAsyncLowerMatchesTheOracle(t *testing.T) {
	for _, fx := range loadAsyncLowers(t) {
		t.Run(fx.Name, func(t *testing.T) {
			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			v, hasResult := resultValue(t, fx.ResultKind, fx.RetValue)
			impl := func(_ *interp.CanonCaller, _ []interp.Value) (canon.Value, bool, error) {
				return v, true, nil // resolves inline
			}
			const retptr = 16 // a clear region past the params
			args := make([]interp.Value, 0, len(fx.Args)+1)
			for _, a := range fx.Args {
				args = append(args, interp.I32(int32(a)))
			}
			if hasResult {
				args = append(args, interp.I32(retptr))
			}
			ret, err := asyncLowerFunc(impl, hasResult)(cc, args)
			if err != nil {
				t.Fatalf("wrapper returned error on the sync-resolving arm: %v", err)
			}
			// The packed return is [RETURNED], matching the fixture's ret.
			if len(ret) != len(fx.Ret) || (len(ret) == 1 && ret[0].Int32() != int32(fx.Ret[0])) {
				t.Errorf("packed return = %v, want %v (fixture)", ret, fx.Ret)
			}
			if !hasResult {
				return // empty result: no retptr write to check
			}
			// The result lowered at the retptr is byte-for-byte the model's, whose memory_hex holds it at
			// the fixture's own retptr.
			size := resultSize(fx.ResultKind)
			got, err := cc.Read(retptr, size)
			if err != nil {
				t.Fatalf("reading retptr: %v", err)
			}
			mem, err := hex.DecodeString(fx.MemoryHex)
			if err != nil {
				t.Fatalf("decoding fixture memory: %v", err)
			}
			want := mem[*fx.Retptr : *fx.Retptr+int(size)]
			if !bytes.Equal(got, want) {
				t.Errorf("retptr bytes = %x, want %x (model's lowered result)", got, want)
			}
		})
	}
}

// TestAsyncLowerBlockingRefusesByNameLeavingRetptrUnchanged is the refusal-firing + retptr-unchanged
// witness (Scott's caution): a blocking impl (does not resolve inline) refuses by name with
// ErrAsyncNotImplemented, and the retptr is untouched — a refusal, not a half-lowered result. Witnessed
// outside a guest because a trapping refusal cannot be observed from inside one.
func TestAsyncLowerBlockingRefusesByNameLeavingRetptrUnchanged(t *testing.T) {
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	const retptr = 16
	sentinel := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if werr := cc.Write(retptr, sentinel); werr != nil {
		t.Fatalf("seeding retptr: %v", werr)
	}
	blocking := func(_ *interp.CanonCaller, _ []interp.Value) (canon.Value, bool, error) {
		return canon.Value{}, false, ErrAsyncWouldBlock // the blocking arm — 2b
	}
	_, err = asyncLowerFunc(blocking, true)(cc, []interp.Value{interp.I32(7), interp.I32(retptr)})
	if !errors.Is(err, ErrAsyncNotImplemented) {
		t.Fatalf("blocking async lower: err = %v, want ErrAsyncNotImplemented (refuse by name)", err)
	}
	got, err := cc.Read(retptr, 4)
	if err != nil {
		t.Fatalf("reading retptr: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Errorf("retptr = %x after a refused blocking lower, want %x unchanged — a refusal must not "+
			"half-lower a result", got, sentinel)
	}
}

// TestGateAsyncNarrowingPermitsLowerRefusesUnbuilt witnesses the narrowing per the #732-guard: gate on,
// an async-lower-only component is permitted through to the walk (2a-i-A executes it), while the surface
// the arm does not execute — an async built-in, an async lift — still refuses by name. Both halves are
// witnessed (the refusal firing, not just the permit passing).
func TestGateAsyncNarrowingPermitsLowerRefusesUnbuilt(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1") // gate on

	// Permit: an async-lower-only component reaches the walk (gateAsync returns nil).
	lowerOnly := &Component{Canons: []Canon{{Kind: CanonLower, Opts: CanonOpts{Async: true}}}}
	if err := gateAsync(lowerOnly); err != nil {
		t.Errorf("gate on, async-lower-only: gateAsync refused (%v), want permit — 2a-i-A executes the lower", err)
	}

	// Refuse-firing: an async canon built-in (the waitable-set loop is 2b) refuses by name.
	builtin := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x1f}}} // waitable-set.new
	if err := gateAsync(builtin); !errors.Is(err, ErrAsyncNotImplemented) {
		t.Errorf("gate on, async built-in: err = %v, want ErrAsyncNotImplemented (refuse by name)", err)
	}

	// Refuse-firing: an async lift (the export lift is deferred) refuses by name.
	lift := &Component{Canons: []Canon{{Kind: CanonLift, Opts: CanonOpts{Async: true}}}}
	if err := gateAsync(lift); !errors.Is(err, ErrAsyncNotImplemented) {
		t.Errorf("gate on, async lift: err = %v, want ErrAsyncNotImplemented (refuse by name)", err)
	}
}

// TestSynthAsyncLowerBindsAndReachesTheWrapper is the binding-branch end-to-end witness (Scott's
// synth live caller): a real-bytes async-lower-only component (testdata/async-lower-synth.wasm), gate on,
// instantiates (the narrowing permits it) and its async lower binds through the SEPARATE async-impl
// source to the wrapper — so Call("run") reaches the impl with the lifted param. The wrapper's byte-level
// behavior is the harness tests' job; this witnesses the permit + binding + reach that the harness cannot.
func TestSynthAsyncLowerBindsAndReachesTheWrapper(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1") // gate on
	b, err := os.ReadFile("testdata/async-lower-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	called := false
	var gotX int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(_ *interp.CanonCaller, params []interp.Value) (canon.Value, bool, error) {
			called = true
			if len(params) > 0 {
				gotX = params[0].Int32()
			}
			return canon.U32(107), true, nil // resolves inline
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("gate on, async-lower-only synth: instantiate refused (%v), want permit + bind", err)
	}
	defer in.Close()
	if err := in.Call("run"); err != nil {
		t.Fatalf("Call(run): %v", err)
	}
	if !called {
		t.Error("the async lower's impl was not called — the binding did not route to the async wrapper")
	}
	if gotX != 7 {
		t.Errorf("impl got x=%d, want 7 — the wrapper did not pass the lifted param", gotX)
	}
}
