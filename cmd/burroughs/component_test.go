// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"testing"
)

// TestRunExecutesAComponent is PR C's registered exit (ADR 0084, #694): `burroughs run p3hello.wasm`
// runs a cargo-component `wasi:cli/run` guest and writes its stdout — the capability invoked the way a
// user invokes it, through the public `burroughs` package (decision 0029), and its bytes checked
// against the committed wasmtime 48.0.1 reading (the same oracle `internal/component`'s own diff uses).
func TestRunExecutesAComponent(t *testing.T) {
	const guest = "../../internal/component/testdata/p3hello.wasm"
	want, err := os.ReadFile("../../internal/component/testdata/p3hello.stdout")
	if err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", guest})
	if code != 0 {
		t.Fatalf("`run p3hello.wasm` exited %d, want 0\nstderr: %q", code, errBuf.String())
	}
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("stdout = %q, want %q (wasmtime 48.0.1's reading)", got, want)
	}
}
