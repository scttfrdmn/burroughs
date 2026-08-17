// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build ignore

// Command gen assembles add.wat into add.wasm.
//
// The committed `.wasm` is a **derived** artifact, and this is the derivation — the same shape as
// `make opcodes` and the generated opcode table: the binary is in the tree so a fresh clone can run
// the README's example in one command, and the text beside it is the authority for what the binary
// says. TestExampleWasmIsDerivedFromItsWat asserts the two still agree, so the artifact cannot drift
// away from its source in silence; run this when the `.wat` changes or when the assembler's output
// does.
//
//	go run ./examples/add/gen.go
package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/scttfrdmn/burroughs/internal/text"
)

func main() {
	// Paths relative to this source file rather than to the working directory, because `go run
	// ./examples/add/gen.go` runs from the module root while a reader in the directory would run it
	// from there, and a generator whose output location depends on where it was invoked writes the
	// artifact somewhere nobody checks.
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("gen: cannot locate this source file, so the output path is unknown")
	}
	dir := filepath.Dir(self)

	src, err := os.ReadFile(filepath.Join(dir, "add.wat"))
	if err != nil {
		log.Fatalf("gen: %v", err)
	}
	wasm, err := text.EncodeModule(src)
	if err != nil {
		log.Fatalf("gen: assembling add.wat: %v", err)
	}
	out := filepath.Join(dir, "add.wasm")
	if err := os.WriteFile(out, wasm, 0o644); err != nil { //nolint:gosec // a committed example fixture, read by everyone who clones the repo
		log.Fatalf("gen: %v", err)
	}
	log.Printf("gen: wrote %s (%d bytes)", out, len(wasm))
}
