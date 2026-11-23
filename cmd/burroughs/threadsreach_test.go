// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestRunCannotStartASecondSharedMemoryThread was `burroughs run`'s arm of the root package's
// `TestNoPublicPathRunsASecondSharedMemoryThread` (the chair's fourth ruling of 2026-10-26, item 1). At v0.11.0 a
// guest importing the Phase 4 fork's spawn door, `"burroughs" "spawn"`, was refused at link on both of the CLI's
// module paths.
//
// **Since v0.12.0 a WASIP1 command can start a second agent** (#1122); `TestRunStartsAGuestsSecondThread` below is
// that answer. The name is kept because v0.11.0's release record cites it. What this still asserts:
//   - a **core module** importing the door is refused at link, naming the import. A core module runs through
//     `Config.Instantiate`, which supplies no imports, and host-started threads are not built;
//   - a module using the threads grammar alone runs, which is `gate:threads`' half.
func TestRunCannotStartASecondSharedMemoryThread(t *testing.T) {
	grammar := writeModule(t, "atomics.wasm", `(module (memory 1 1 shared)
		(func (export "f") (result i32) (i32.atomic.load (i32.const 0))))`)
	core := writeModule(t, "spawn-core.wasm", `(module (import "burroughs" "spawn" (func (param i32)))
		(memory 1 1 shared) (func (export "f")))`)

	var out, errOut bytes.Buffer
	if code := exitCode(runCmd(&out, &errOut, []string{grammar, "f"})); code != exitOK {
		t.Fatalf("a shared memory with atomics exited %d, want %d: gate:threads is not on (stderr: %s)",
			code, exitOK, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code := exitCode(runCmd(&out, &errOut, []string{core}))
	if code == exitOK {
		t.Errorf("a core module importing the spawn door ran, so the core path supplies one")
	}
	if !strings.Contains(errOut.String(), `"burroughs" "spawn"`) {
		t.Errorf("the core module exited %d, but its report does not name the spawn import: %s", code, errOut.String())
	}
}

// spawnHandshakeWAT is the root package's two-agent handshake (threadsreach_test.go there), repeated because the
// two test packages cannot share a constant. Exit 42 means `mstart` ran on a second agent, concurrently with
// `_start`. Run inline on the calling agent, it would time out and exit 1.
const spawnHandshakeWAT = `(module
  (import "burroughs" "spawn" (func $spawn (param i32)))
  (import "wasi_snapshot_preview1" "proc_exit" (func $exit (param i32)))
  (memory (export "memory") 1 1 shared)
  (func (export "mstart") (param i32)
    (drop (memory.atomic.wait32 (i32.const 0) (i32.const 0) (i64.const 5000000000)))
    (i32.atomic.store (i32.const 4) (i32.const 1))
    (drop (memory.atomic.notify (i32.const 4) (i32.const 1))))
  (func (export "_start")
    (call $spawn (i32.const 0))
    (i32.atomic.store (i32.const 0) (i32.const 1))
    (drop (memory.atomic.notify (i32.const 0) (i32.const 1)))
    (drop (memory.atomic.wait32 (i32.const 4) (i32.const 0) (i64.const 5000000000)))
    (call $exit (select (i32.const 42) (i32.const 1) (i32.atomic.load (i32.const 4))))))`

// TestRunStartsAGuestsSecondThread is v0.12.0's CLI answer (#1122): `burroughs run` on a WASIP1 command that uses
// the spawn door starts a second agent, with nothing supplied beyond the module. The guest's own code, 42, is the
// CLI's exit code (ADR 0081).
func TestRunStartsAGuestsSecondThread(t *testing.T) {
	cmd := writeModule(t, "handshake.wasm", spawnHandshakeWAT)
	var out, errOut bytes.Buffer
	if code := exitCode(runCmd(&out, &errOut, []string{cmd})); code != 42 {
		t.Fatalf("burroughs run exited %d, want 42: mstart did not run on a second agent (stderr: %s)",
			code, errOut.String())
	}
}

// spawnDoorChain is internal/component's spawn-door guest's `chain(k)`, repeated because the two test packages
// cannot share a function, and recomputed here so the expected output does not come from the guest.
func spawnDoorChain(k uint64) uint64 {
	x := k*0x9E3779B97F4A7C15 + 1
	for i := range uint64(100000) {
		x ^= x >> 33
		x *= 0xFF51AFD7ED558CCD
		x ^= x >> 29
		x += i
	}
	return x
}

// TestRunStartsAComponentsAgentsThroughItsSpawnDoor is `burroughs run`'s arm of E3 (#1337, ADR 0131): a component
// that imports `burroughs:threads/spawn@0.1.0` starts two agents of its own through `dispatch`, with
// BURROUGHS_CM_SHARED_MEMORY=1. Its stdout says each agent did its work with its own globals and saw run's
// release, which run issues only after meeting both agents while itself executing, so an agent run inline would
// print `released=00000000`. The executing-agent peak is read in internal/component's witness, which can reach the
// core instance. **With the gate off** the same component is refused by name, at exit 5.
func TestRunStartsAComponentsAgentsThroughItsSpawnDoor(t *testing.T) {
	const fixture = "../../internal/component/testdata/spawn-door.wasm"
	var want strings.Builder
	for k := uint64(1); k <= 2; k++ {
		fmt.Fprintf(&want, "agent %d chain=%016x entry-global=%08x global=%08x released=%08x\n", k, spawnDoorChain(k), 0, k, 1)
	}
	fmt.Fprintf(&want, "run global=%08x\n", 0x77)

	t.Setenv("BURROUGHS_CM_SHARED_MEMORY", "1")
	var out, errOut bytes.Buffer
	if code := dispatch(&out, &errOut, []string{"run", fixture}); code != exitOK {
		t.Fatalf("burroughs run exited %d, want %d (stderr: %s)", code, exitOK, errOut.String())
	}
	if out.String() != want.String() {
		t.Errorf("stdout\n%s\nwant\n%s", out.String(), want.String())
	}

	t.Setenv("BURROUGHS_CM_SHARED_MEMORY", "")
	out.Reset()
	errOut.Reset()
	if code := dispatch(&out, &errOut, []string{"run", fixture}); code != exitUnsupported {
		t.Errorf("with the gate off, burroughs run exited %d, want %d (stderr: %s)", code, exitUnsupported, errOut.String())
	}
	if !strings.Contains(errOut.String(), "burroughs:threads/spawn@0.1.0") ||
		!strings.Contains(errOut.String(), "gate:cm-shared-memory") {
		t.Errorf("with the gate off, the refusal does not name the door and the gate: %s", errOut.String())
	}
}
