// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestP3HelloWorldMatchesWasmtime is PR C's exit (ADR 0084, #694): a real cargo-component `wasi:cli/run`
// guest, run end-to-end through the loader, the instantiation walk, ADR 0069's funcref dispatch, and the
// canonical-ABI adapter's memory-bound / realloc-bound marshaling, produces the bytes wasmtime produces.
//
// The oracle is committed (`testdata/p3hello.stdout`, wasmtime 48.0.1), not run here — CI installs no
// wasmtime, the same discipline as the `p3hello.wit` golden. So this is a differential against an
// external engine's reading, taken offline and pinned, exactly as PR A's value codec is differenced
// against `definitions.py`.
func TestP3HelloWorldMatchesWasmtime(t *testing.T) {
	wasm, err := os.ReadFile("testdata/p3hello.wasm")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/p3hello.stdout")
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	h := NewHost(&out, io.Discard, nil)
	in, err := InstantiateWithHost(wasm, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	if err := in.Call("wasi:cli/run@0.2.3"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("stdout = %q, want %q (wasmtime 48.0.1's reading)", got, want)
	}
	if h.Exited && h.ExitCode != 0 {
		t.Errorf("guest exited with code %d, want a clean run", h.ExitCode)
	}
}
