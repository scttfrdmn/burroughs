// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunCannotStartASecondSharedMemoryThread is `burroughs run`'s arm of the root package's
// `TestNoPublicPathRunsASecondSharedMemoryThread` (the chair's fourth ruling of 2026-10-26, item 1). A guest
// importing the Phase 4 fork's spawn door, `"burroughs" "spawn"`, is refused at link on both of the CLI's
// module paths, a core module and a WASIP1 command. It is never run, so it never starts a thread. A module
// using the threads grammar alone runs, which is `gate:threads`' half.
func TestRunCannotStartASecondSharedMemoryThread(t *testing.T) {
	grammar := writeModule(t, "atomics.wasm", `(module (memory 1 1 shared)
		(func (export "f") (result i32) (i32.atomic.load (i32.const 0))))`)
	core := writeModule(t, "spawn-core.wasm", `(module (import "burroughs" "spawn" (func (param i32)))
		(memory 1 1 shared) (func (export "f")))`)
	command := writeModule(t, "spawn-command.wasm", `(module
		(import "burroughs" "spawn" (func (param i32)))
		(import "wasi_snapshot_preview1" "proc_exit" (func (param i32)))
		(memory (export "memory") 1 1 shared) (func (export "_start")))`)

	var out, errOut bytes.Buffer
	if code := exitCode(runCmd(&out, &errOut, []string{grammar, "f"})); code != exitOK {
		t.Fatalf("a shared memory with atomics exited %d, want %d: gate:threads is not on (stderr: %s)",
			code, exitOK, errOut.String())
	}
	for _, path := range []string{core, command} {
		out.Reset()
		errOut.Reset()
		code := exitCode(runCmd(&out, &errOut, []string{path}))
		if code == exitOK {
			t.Errorf("%s exited 0: a guest importing the spawn door ran, so the CLI supplies one", path)
		}
		if !strings.Contains(errOut.String(), `"burroughs" "spawn"`) {
			t.Errorf("%s exited %d, but its report does not name the spawn import: %s", path, code, errOut.String())
		}
	}
}
