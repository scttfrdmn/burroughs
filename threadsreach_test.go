// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestNoPublicPathRunsASecondSharedMemoryThread answered the chair's fourth ruling of 2026-10-26, item 1: for each
// public entry point, whether a guest can run a second thread of a shared memory through it. At v0.11.0 none
// could (#1121). `gate:threads` (ADR 0106) admits the threads GRAMMAR on every path, and that half is asserted here
// too, so that the answer is about starting a thread and not about decoding one.
//
// **Since v0.12.0 the WASIP1 path can** (#1122). It serves the Phase 4 fork's spawn door, `"burroughs" "spawn"`,
// to a module that imports it and exports `mstart`. `TestAGuestStartsASecondThreadThroughWASIP1ConfigRun` below is
// that answer. The name is kept because v0.11.0's release record cites it. Its arms now cover the paths that still
// start no second shared-memory thread, and the door's link-time refusal:
//   - **`Instantiate`:** it supplies no imports, so the door does not link. Host-started threads are not built
//     (Scott's decision).
//   - **WASIP1, without `mstart`:** the door is refused at link, by name, because a spawned agent would have no
//     entry.
//   - **Components:** `thread.spawn-ref` (0x40) and `thread.spawn-indirect` (0x41), the shared-everything-threads
//     built-ins. The first is refused by name. The second cannot be reached at all: the shared table it needs is
//     refused first, at decode (#1120).
//
// The CLI's arms are `cmd/burroughs`'s `TestRunCannotStartASecondSharedMemoryThread` (a core module, still
// refused) and `TestRunStartsAGuestsSecondThread`.
//
// **What this does not cover.** Component-model Threads (`gate:cm-threads`, default on since v0.8.0) DO run
// through the public component paths, and `TestAComponentModelThreadRunsThroughThePublicPath` below shows one. They
// are cooperative: one executes at a time per component instance, under the run slot (ADR 0101,
// `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce`). They are not agents over a shared memory, which is
// what the v0.10.0 threads claim is about.
func TestNoPublicPathRunsASecondSharedMemoryThread(t *testing.T) {
	encode := func(t *testing.T, src string) []byte {
		t.Helper()
		w, err := text.EncodeModule([]byte(src))
		if err != nil {
			t.Fatalf("assembling the fixture failed, so this arm's subject was never reached: %v", err)
		}
		return w
	}
	refusesTheSpawnImport := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal(`a guest importing "burroughs" "spawn" linked, so some public path supplies a spawn door`)
		}
		if !strings.Contains(err.Error(), `"burroughs" "spawn"`) {
			t.Errorf("the guest was refused, but not for the spawn import: %v", err)
		}
	}

	t.Run("Instantiate_admits_the_threads_grammar", func(t *testing.T) {
		in, err := burroughs.Instantiate(encode(t, `(module (memory 1 1 shared)
			(func (export "f") (result i32) (i32.atomic.load (i32.const 0))))`))
		if err != nil || in == nil {
			t.Fatalf("a shared memory with atomics was refused, so gate:threads is not on: %v", err)
		}
	})

	t.Run("Instantiate_cannot_link_the_spawn_import", func(t *testing.T) {
		_, err := burroughs.Instantiate(encode(t, `(module (import "burroughs" "spawn" (func (param i32)))
			(memory 1 1 shared) (func (export "f")))`))
		refusesTheSpawnImport(t, err)
	})

	t.Run("WASIP1Config_Run_refuses_the_door_without_mstart", func(t *testing.T) {
		var out, errOut bytes.Buffer
		_, err := burroughs.WASIP1Config{Stdout: &out, Stderr: &errOut}.Run(encode(t, `(module
			(import "burroughs" "spawn" (func (param i32)))
			(import "wasi_snapshot_preview1" "proc_exit" (func (param i32)))
			(memory (export "memory") 1 1 shared) (func (export "_start")))`))
		refusesTheSpawnImport(t, err)
		if err != nil && !strings.Contains(err.Error(), "mstart") {
			t.Errorf("refused, but not for the missing mstart entry: %v", err)
		}
	})

	for _, c := range []struct{ fixture, want string }{
		// Refused by name: the production and its opcode.
		{"thread-spawn-ref-synth.wasm", "thread.spawn-ref (0x40)"},
		// Refused before its canon is read: the shared table spawn-indirect requires is not decodable. That
		// refusal says "malformed", which is a misclassification of a well-formed component (wasm-tools 1.258.0
		// validates it with --features all), filed as #1120. It is still a refusal, so no path runs a thread.
		{"thread-spawn-indirect-synth.wasm", "reference type"},
	} {
		w, err := os.ReadFile("internal/component/testdata/" + c.fixture)
		if err != nil {
			t.Fatalf("the committed fixture is missing, so this arm has no subject: %v", err)
		}
		t.Run("LoadComponent_refuses_"+c.fixture, func(t *testing.T) {
			_, err := burroughs.LoadComponent(w)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("LoadComponent(%s) = %v, want a refusal containing %q", c.fixture, err, c.want)
			}
		})
		t.Run("ComponentConfig_Run_refuses_"+c.fixture, func(t *testing.T) {
			_, err := burroughs.ComponentConfig{}.Run(w)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ComponentConfig.Run(%s) = %v, want a refusal containing %q", c.fixture, err, c.want)
			}
		})
	}
	t.Run("the_named_refusal_is_ErrUnsupported", func(t *testing.T) {
		w, err := os.ReadFile("internal/component/testdata/thread-spawn-ref-synth.wasm")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := burroughs.LoadComponent(w); !errors.Is(err, burroughs.ErrUnsupported) {
			t.Errorf("thread.spawn-ref is not refused as ErrUnsupported: %v", err)
		}
	})
}

// TestAComponentModelThreadRunsThroughThePublicPath is the other half of the answer. A component-model Thread is a
// thread a guest CAN start through a public path, here `LoadComponent` and `Call`. `cmthreads/switch.wasm`
// creates five with `thread.new-indirect`, and every one of them sets its marker in the result (switch.reading:
// 143165567, mask 0b111111). They run one at a time, under the run slot (ADR 0101).
func TestAComponentModelThreadRunsThroughThePublicPath(t *testing.T) {
	w, err := os.ReadFile("internal/component/testdata/cmthreads/switch.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing, so this witness has no subject: %v", err)
	}
	c, err := burroughs.LoadComponent(w)
	if err != nil {
		t.Fatalf("LoadComponent(switch.wasm): %v", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.Call(context.Background(), "run", burroughs.ComponentU32(0))
	if err != nil {
		t.Fatalf("run(0): %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("run(0) returned %d values, want 1", len(res))
	}
	got, ok := res[0].U32()
	if !ok || got != 143165567 {
		t.Fatalf("run(0) = %v, want u32 143165567 (switch.reading)", res[0])
	}
	if got&0x3f != 0x3f {
		t.Errorf("run(0)'s marker mask is %#b, want 0b111111: not every created Thread ran", got&0x3f)
	}
}

// spawnHandshakeWAT is a two-agent handshake through the spawn door, and its exit code says whether a SECOND agent
// ran. `_start` spawns `mstart`, then sets A and waits for B. `mstart` waits for A, then sets B. If the door ran
// `mstart` inline on the calling agent, `mstart` would wait on an A not yet set, and time out; then B would never
// be set, and the exit code would be 1. **Exit 42 means `mstart` ran on another agent, concurrently.** The waits are
// bounded at 5s, so a missing second agent fails as a code, not a hang.
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

// TestAGuestStartsASecondThreadThroughWASIP1ConfigRun is v0.12.0's public-path answer (#1122): a guest starts a
// second shared-memory agent through `WASIP1Config.Run`, with nothing supplied beyond the module. The fork-built Go
// program that does the same at scale is `internal/wasi`'s `TestAForkBuiltGoProgramRunsWithRealThreadsThroughThePublicPath`.
//
// **Neuter, run:** with the door not served (`spawnDoorFor` replaced by a nil door in `runModule`), this fails to
// link with *"unknown import: "burroughs" "spawn""*.
func TestAGuestStartsASecondThreadThroughWASIP1ConfigRun(t *testing.T) {
	wasm, err := text.EncodeModule([]byte(spawnHandshakeWAT))
	if err != nil {
		t.Fatalf("assembling the handshake failed: %v", err)
	}
	var out, errOut bytes.Buffer
	code, err := burroughs.WASIP1Config{Stdout: &out, Stderr: &errOut}.Run(wasm)
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, errOut.String())
	}
	if code != 42 {
		t.Fatalf("exit %d, want 42: mstart did not run on a second agent concurrently with _start", code)
	}
}
