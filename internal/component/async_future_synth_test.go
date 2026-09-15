// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestSynthFutureReadBindsAndReturnsBlocked is the binding-branch end-to-end witness for increment 3: a
// real component whose async lower returns a future<u32> (the wrapper mints a readable end and writes its
// handle), and whose guest then future.read's that handle. Gate on, it instantiates (gateAsync permits
// future.read 0x16 / future.drop-readable 0x1a), the lower binds through the async-impl source, and
// future.read reaches the impl and returns BLOCKED (the async read parks; this witness does not complete
// it — the completion + delivery is TestFutureReadDeliversTheOracleOutcomes' job). Also the reachable
// consumer of canon.Future.
func TestSynthFutureReadBindsAndReturnsBlocked(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/future-read-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	called := false
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/src::get-future": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			called = true
			onResolve(canon.Future(107)) // the host returns a future<u32> delivering 107
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("gate on, future-read synth: instantiate refused (%v), want permit + bind", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "run")
	if ci == nil {
		t.Fatal("no core instance exports run")
	}
	res, err := ci.Invoke("run")
	if err != nil {
		t.Fatalf("Invoke(run): %v", err)
	}
	if !called {
		t.Error("get-future impl was not called — the async lower did not bind")
	}
	if len(res) != 1 || uint32(res[0].Int32()) != 0xffffffff {
		t.Errorf("run = %v, want [BLOCKED] (0xffffffff) — future.read's async park signal", res)
	}
}
