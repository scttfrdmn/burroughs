// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

const p3hello = "../../internal/component/testdata/p3hello.wasm"

// TestRunExecutesAComponent is PR C's registered exit (ADR 0084, #694): with gate:components on,
// `burroughs run p3hello.wasm` runs a cargo-component `wasi:cli/run` guest and writes its stdout —
// checked byte-for-byte against the committed wasmtime 48.0.1 reading. The gate is on here because the
// exit is a gated capability (behaviour 4); the default-build refusal is the next test.
func TestRunExecutesAComponent(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "1")
	want, err := os.ReadFile("../../internal/component/testdata/p3hello.stdout")
	if err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", p3hello})
	if code != 0 {
		t.Fatalf("`run p3hello.wasm` exited %d, want 0\nstderr: %q", code, errBuf.String())
	}
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("stdout = %q, want %q (wasmtime 48.0.1's reading)", got, want)
	}
}

// TestRunRefusesAComponentWhenGatedOff is the observable that distinguishes additive plumbing from the
// gate flip (Scott's #694 ruling): on a default build (gate:components off), `burroughs run` recognizes
// the component but refuses to run it, by name — it does NOT print the string. That refusal, not a
// default-on run, is what keeps this an additive surface over a gated mechanism.
func TestRunRefusesAComponentWhenGatedOff(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "") // the default build: the gate is off
	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", p3hello})
	if code != exitGated {
		t.Errorf("`run p3hello.wasm` (gate off) exited %d, want %d (exitGated)\nstderr: %q",
			code, exitGated, errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty — a gated component must not run", out.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "gate:components") {
		t.Errorf("stderr = %q, want it to name gate:components (refuse by name)", got)
	}
}
