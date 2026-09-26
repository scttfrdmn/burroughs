// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestClause1ForkComponentReturns42 is Phase 4 **clause 1**'s Engine-B arm, committed.
//
// # Why this file exists
//
// Clause 1's claim is *"a fork-built Go program runs as a p3 component and returns 42 on Wasmtime and 42 on
// Burroughs."* Its Burroughs half was an ad hoc test written into `internal/component`, run, and deleted —
// so when ADR 0090's merge gate asked for clauses 1–3 to be re-run, clause 1 was re-runnable only because
// its component happened to still exist in `/tmp`. That is the same standing clauses 2 and 3 were found in:
// **a witness that is not committed is not a witness** (fork standing property 18).
//
// # The two engines are committed differently, and the asymmetry is real
//
//   - **Engine B (this test)** runs the component on Burroughs, in CI, with the bytes committed beside it.
//   - **Engine A (Wasmtime)** cannot run in this repository's CI: it needs the `wasmtime` binary, which is
//     not a dependency here. What is committed instead is **its reading** — `testdata/clause1/hello.reading`,
//     copied from the fork, which records the invocation, the output `42`, the exit code, the engine version,
//     and the load-bearing `-W shared-memory=y` finding. That reading was committed in the fork *before the
//     fork could emit the module at all*, which is what makes it a prediction rather than a transcript.
//
// So this test proves the Burroughs half and **cites** the Wasmtime half. It does not claim to have run
// Wasmtime, and a reader comparing the two must compare this test's result against that committed reading.
//
// # What it does NOT test
//
// There is no public entry point for invoking a non-`run` component export, so this reaches through the
// package's own internals — the limitation recorded on #802's close. A public surface for it is a separate
// decision and is not implied here.
func TestClause1ForkComponentReturns42(t *testing.T) {
	img, err := os.ReadFile(filepath.Join("testdata", "clause1", "fork_hello_component.wasm"))
	if err != nil {
		t.Fatalf("read component: %v", err)
	}

	feats := bin.DefaultFeatures()
	// ADR 0088's occasion: the fork's guest carries a shared memory and atomics, so the loader's default
	// feature set refuses it. `gate:threads` IS load-bearing for this artifact, which is the narrower
	// reading ADR 0080's general-sounding comment gets here.
	feats.Threads = true

	in, err := InstantiateWithHostFeatures(img, NewHost(io.Discard, io.Discard, nil), feats)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	cd, ok := in.export.exports["hello"]
	if !ok {
		t.Fatal(`component has no "hello" export; the guest is not the one this arm is about`)
	}
	// A sync lift does not surface a result through `Call`, so the core instance is invoked directly — the
	// same route the fork's README records for this arm.
	vals, err := cd.fn.core.inst.Invoke(cd.fn.core.name)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(vals) != 1 {
		t.Fatalf("got %d results, want 1", len(vals))
	}
	// **42 is the committed reading's value**, not a number chosen here. `testdata/clause1/hello.reading`
	// records it for Wasmtime; this asserts Burroughs agrees.
	if got := vals[0].Int32(); got != 42 {
		t.Errorf("hello() = %d, want 42 — `testdata/clause1/hello.reading` records 42 on Wasmtime, so a "+
			"different value here is the two engines disagreeing, which is exactly clause 1's subject",
			got)
	}
}
