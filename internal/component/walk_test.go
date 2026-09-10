// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

func mustModule(t *testing.T, wat string) *bin.Module {
	t.Helper()
	img, err := text.EncodeModule([]byte(wat))
	if err != nil {
		t.Fatalf("encode WAT: %v", err)
	}
	m, err := bin.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// TestP3HelloCoreModulesInstantiate is B.2b's exit for the core-instance walk: p3hello's four core
// modules all instantiate through the interpreter, their imports resolved from earlier instances'
// exports (by reference) and the canon-lowered wasi funcs filled by the refusing stub host.
func TestP3HelloCoreModulesInstantiate(t *testing.T) {
	b, err := os.ReadFile(fixtureWasm)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.instantiate()
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer w.close()

	realN := 0
	for _, ci := range w.coreInstances {
		if _, ok := ci.(realInst); ok {
			realN++
		}
	}
	if realN != len(c.CoreModules) {
		t.Errorf("instantiated core modules = %d, want %d (all up)", realN, len(c.CoreModules))
	}
	if len(c.CoreModules) != 4 {
		t.Errorf("core modules = %d, want 4", len(c.CoreModules))
	}
}

// TestStubHostRefusesByName is the "imports reach a stub host that refuses by name" boundary (PR B): a
// canon-lowered import the engine does not fill refuses, naming the import, when called.
func TestStubHostRefusesByName(t *testing.T) {
	_, err := refuse("wasi_snapshot_preview1", "fd_write")(nil, nil)
	if !errors.Is(err, ErrLinkRefused) {
		t.Fatalf("stub error = %v, want ErrLinkRefused", err)
	}
	if got := err.Error(); !contains(got, "fd_write") {
		t.Errorf("stub error %q does not name the import", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestExportedMemoryIsSharedByReference is a reference-semantics assertion (#694, folded into B.2b): a
// write through the importer's memory extern is visible through the exporter's — one shared memory, not
// a copy. If Instance.Export re-wrapped the memory, the exporter would read 0 after the importer wrote
// 42, a silent divergence the module-order oracle would not catch.
func TestExportedMemoryIsSharedByReference(t *testing.T) {
	exp := mustModule(t, `(module (memory (export "mem") 1) (func (export "r") (result i32) (i32.load (i32.const 0))))`)
	imp := mustModule(t, `(module (import "e" "mem" (memory 1)) (func (export "w") (i32.store (i32.const 0) (i32.const 42))))`)

	ein, trap, err := interp.Instantiate(exp)
	if err != nil || trap != nil {
		t.Fatalf("exporter: %v %v", err, trap)
	}
	defer ein.Close()
	iin, trap, err := interp.InstantiateLinked(imp, func(mod, name string) (interp.Extern, bool) {
		if mod == "e" && name == "mem" {
			return ein.Export("mem")
		}
		return interp.Extern{}, false
	})
	if err != nil || trap != nil {
		t.Fatalf("importer: %v %v", err, trap)
	}
	defer iin.Close()

	if _, werr := iin.Invoke("w"); werr != nil {
		t.Fatalf("write through importer: %v", werr)
	}
	got, err := ein.Invoke("r")
	if err != nil {
		t.Fatalf("read through exporter: %v", err)
	}
	if got[0].Int32() != 42 {
		t.Errorf("exporter reads %d after importer wrote 42 — memory not shared by reference", got[0].Int32())
	}
}

// TestImportedFuncRunsOnExporterState is the second reference-semantics assertion: a call through an
// imported function extern runs on the exporting instance's state — it reads the exporter's global, not
// the importer's (which has none).
func TestImportedFuncRunsOnExporterState(t *testing.T) {
	exp := mustModule(t, `(module (global i32 (i32.const 7)) (func (export "readg") (result i32) (global.get 0)))`)
	imp := mustModule(t, `(module (import "e" "readg" (func (result i32))) (func (export "call") (result i32) (call 0)))`)

	ein, trap, err := interp.Instantiate(exp)
	if err != nil || trap != nil {
		t.Fatalf("exporter: %v %v", err, trap)
	}
	defer ein.Close()
	iin, trap, err := interp.InstantiateLinked(imp, func(mod, name string) (interp.Extern, bool) {
		if mod == "e" && name == "readg" {
			return ein.Export("readg")
		}
		return interp.Extern{}, false
	})
	if err != nil || trap != nil {
		t.Fatalf("importer: %v %v", err, trap)
	}
	defer iin.Close()

	got, err := iin.Invoke("call")
	if err != nil {
		t.Fatalf("call imported func: %v", err)
	}
	if got[0].Int32() != 7 {
		t.Errorf("imported func returned %d, want 7 (the exporter's global) — not run on exporter state", got[0].Int32())
	}
}
