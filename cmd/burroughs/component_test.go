// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

const (
	p3hello = "../../internal/component/testdata/p3hello.wasm"
	p3async = "../../internal/component/testdata/p3async-hello.wasm"
)

// TestRunRefusesAnAsyncComponentByName is the gate:async shell's end-to-end witness at the CLI (ADR 0086):
// with gate:components on (the default) but gate:async off (the default), `run p3async-hello.wasm` exits
// exitGated, stdout empty, stderr naming gate:async. The refusal fires because the guest's imports are
// async-lowered — a component the gate:components path alone would otherwise accept.
func TestRunRefusesAnAsyncComponentByName(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "") // gate:components on (default)
	t.Setenv("BURROUGHS_ASYNC", "")      // gate:async off (default)
	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", p3async})
	if code != exitGated {
		t.Fatalf("`run p3async-hello.wasm` exited %d, want %d (exitGated)\nstderr: %q", code, exitGated, errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty — a gated async component must not run", out.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "gate:async") {
		t.Errorf("stderr = %q, want it to name gate:async (refuse by name)", got)
	}
}

// TestRunRefusesAnAsyncComponentGateOnUnimplemented is the gate-on no-op kill at the CLI (#739 slice 1):
// with gate:components on and gate:async ON, `run p3async-hello.wasm` does not silently instantiate and
// then trap obscurely at run — it exits exitUnsupported (5, "the engine reached something it does not
// implement in this phase"), stdout empty, stderr naming gate:async. Distinct from the gate-off exitGated:
// the gate is open, the mechanism is being built incrementally.
func TestRunRefusesAnAsyncComponentGateOnUnimplemented(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "") // gate:components on (default)
	t.Setenv("BURROUGHS_ASYNC", "1")     // gate:async ON
	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", p3async})
	if code != exitUnsupported {
		t.Fatalf("`run p3async-hello.wasm` (gate on) exited %d, want %d (exitUnsupported)\nstderr: %q", code, exitUnsupported, errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty — the async tier's execution is not built", out.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "gate:async") || !strings.Contains(got, "not yet implemented") {
		t.Errorf("stderr = %q, want it to name gate:async and that execution is not yet implemented", got)
	}
}

// TestRunExecutesAComponent asserts the flipped-on default (2026-09-11, ADR 0084, #720): on a default
// build — the gate unset — `burroughs run p3hello.wasm` runs a cargo-component `wasi:cli/run` guest and
// writes its stdout, checked byte-for-byte against the committed wasmtime 48.0.1 reading. Before the flip
// this test set the gate on explicitly; it now rides the bare default, which is the flip's observable.
// The explicit-off rollback is TestRunRefusesAComponentWhenExplicitlyGatedOff.
func TestRunExecutesAComponent(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "") // the default build: the gate is on
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

// TestRunRefusesAComponentWhenExplicitlyGatedOff is the flip's rollback, witnessed pre-need (#720's
// forecast). Before the 2026-09-11 flip "off" was the default and this test set the gate to "" to reach
// it; now the default is on, so the rollback path is an explicit `BURROUGHS_COMPONENTS=0`. Same three
// assertions — refuse by exitGated, empty stdout, stderr names gate:components — so the revert is
// exercised before anyone reaches for it, not discovered when they do. The refuse-by-name path is the
// same code in both gate positions; only the default differs.
func TestRunRefusesAComponentWhenExplicitlyGatedOff(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "0") // the rollback: the gate is explicitly off
	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", p3hello})
	if code != exitGated {
		t.Errorf("`run p3hello.wasm` (gate explicitly off) exited %d, want %d (exitGated)\nstderr: %q",
			code, exitGated, errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty — a gated component must not run", out.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "gate:components") {
		t.Errorf("stderr = %q, want it to name gate:components (refuse by name)", got)
	}
}

// TestRunExecutesTheSecondGuest is #715's CLI exit: `burroughs run p3echo.wasm` (gate on), with stdin
// piped, echoes its argv and stdin byte-identical to the committed wasmtime reading — the args path
// (host-lowered list<string>) and the blocking-read path reached through the CLI, not just the API.
func TestRunExecutesTheSecondGuest(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "1")
	const guest = "../../internal/component/testdata/p3echo.wasm"
	want, err := os.ReadFile("../../internal/component/testdata/p3echo.stdout")
	if err != nil {
		t.Fatal(err)
	}
	// Pipe "hello\n" as the process stdin the CLI reads (run.go uses os.Stdin).
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = w.WriteString("hello\n"); _ = w.Close() }()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin; _ = r.Close() }()

	var out, errBuf bytes.Buffer
	code := dispatch(&out, &errBuf, []string{"run", guest})
	if code != 0 {
		t.Fatalf("`run p3echo.wasm` exited %d, want 0\nstderr: %q", code, errBuf.String())
	}
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("stdout = %q, want %q (wasmtime 48.0.1's reading)", got, want)
	}
}
