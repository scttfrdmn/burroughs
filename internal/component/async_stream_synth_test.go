// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestSynthStreamWriteBindsAndReturnsBlocked is the binding-branch witness for the stream write-side slice:
// a real component whose async lower returns a stream<u8> (the wrapper mints a writable end and writes its
// handle), and whose guest then stream.write's that handle. Gate on, it instantiates (gateAsync permits
// stream.write 0x10 and stream value types), the lower binds, and stream.write reaches the impl and returns
// BLOCKED (the async write parks; the host consumer + delivery are the unit test's job). Reachable consumer
// of canon.Stream.
func TestSynthStreamWriteBindsAndReturnsBlocked(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/stream-write-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	called := false
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/sink::get-stream": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			called = true
			onResolve(canon.Stream()) // the host returns a writable stream<u8> end
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("gate on, stream-write synth: instantiate refused (%v), want permit + bind", err)
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
		t.Error("get-stream impl was not called — the async lower did not bind")
	}
	if len(res) != 1 || uint32(res[0].Int32()) != 0xffffffff {
		t.Errorf("run = %v, want [BLOCKED] (0xffffffff) — stream.write's async park signal", res)
	}
}
