// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

const p3lookup = "../../internal/component/testdata/p3lookup/p3lookup.wasm"

// TestRunAComponentWithAllowIPNameLookupResolves is `--allow-ip-name-lookup` at the CLI (ADR 0126): `burroughs run
// --inherit-network --allow-ip-name-lookup p3lookup.wasm -- <port>` exits 0 with wasmtime 49.0.3's `-S
// inherit-network -S allow-ip-name-lookup` reading, and without the lookup flag with its `-S inherit-network`
// reading, every name EAI_FAIL and the connect to localhost failing at its lookup.
//
// Neutered, run: runComponent not passing the flag to ComponentConfig.AllowNameLookup, and the granted row prints
// the denied reading.
func TestRunAComponentWithAllowIPNameLookupResolves(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "")
	t.Setenv("BURROUGHS_ASYNC", "1")
	for _, c := range []struct {
		name    string
		flags   []string
		reading string
	}{
		{"granted", []string{"--inherit-network", "--allow-ip-name-lookup"}, "../../internal/component/testdata/p3lookup.reading"},
		{"lookup denied", []string{"--inherit-network"}, "../../internal/component/testdata/p3lookup-denied.reading"},
	} {
		t.Run(c.name, func(t *testing.T) {
			port, _ := echoPeer(t)
			var out, errBuf bytes.Buffer
			argv := append(append([]string{"run"}, c.flags...), p3lookup, "--", port)
			if code := dispatch(&out, &errBuf, argv); code != exitOK {
				t.Fatalf("exited %d, want 0\nstderr: %q", code, errBuf.String())
			}
			if want := readingStdout(t, c.reading); out.String() != want {
				t.Errorf("stdout = %q, want wasmtime's %q", out.String(), want)
			}
		})
	}
}

// TestAllowIPNameLookupIsAComponentsFlag is #1128's rule for `--allow-ip-name-lookup` (ADR 0126): a wasip1 command's
// preview-1 host serves no name lookup and a core module takes no capabilities, so the flag is refused by name as a
// usage error rather than dropped.
//
// Neutered, run: the refusal dropped, and the wasip1 command runs, exit 0, with the flag ignored.
func TestAllowIPNameLookupIsAComponentsFlag(t *testing.T) {
	cmd := writeModule(t, "cmd.wasm", `(module
		(import "wasi_snapshot_preview1" "proc_exit" (func (param i32)))
		(memory (export "memory") 1)
		(func (export "_start")))`)
	core := writeModule(t, "core.wasm", `(module (func (export "f")))`)
	for _, c := range []struct {
		name string
		argv []string
	}{
		{"a wasip1 command", []string{"run", "--allow-ip-name-lookup", cmd}},
		{"a core module", []string{"run", "--allow-ip-name-lookup", core, "f"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			code := dispatch(&out, &errBuf, c.argv)
			if code != exitUsage || !strings.Contains(errBuf.String(), "--allow-ip-name-lookup applies to a component") {
				t.Errorf("exit %d, stderr %q: want a usage refusal naming --allow-ip-name-lookup", code, errBuf.String())
			}
		})
	}
}
