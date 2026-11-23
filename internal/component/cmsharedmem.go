// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "os"

// `gate:cm-shared-memory`: a shared memory admitted as a canon's `(memory …)` option, **a Burroughs-only
// extension that departs from the pin** (ADR 0129, #1335; Scott's revised ruling 4 on #1332).
//
// # What the pin says, and why this is a departure rather than a following
//
// The pin's CanonicalABI.md, under "`canonopt` Validation", says *"`memory` - this is a subtype of
// `(memory 0)`"*. `(memory 0)` is unshared, and a shared and an unshared memory type never match: the suite
// asks for equality of the flag in both directions, and `internal/interp/link.go:matchMemoryType` compares it.
// So a component naming a shared memory as its canonical memory is invalid at the pin, and wasm-tools 1.258.0
// and wasmtime 49.0.3 refuse it for exactly that rule (*"mismatch in the shared flag for memories"*). The pin
// lists a `shared` canonical option as future work under shared-everything-threads. ADR 0102 does not cover
// this: its rule is about a toolchain stricter than the pin, and here the toolchains are the pin.
//
// # What the gate admits, and what it does not yet claim
//
// With the gate on, the canonical memory may be shared, and the Canonical ABI reads and writes it as it does
// an unshared one. An instance whose canonical memory is shared is also **free** (E2, #1336, ADR 0130): its
// spawned agents run guest code outside ADR 0101's run slot, as threads of their spawner's task (`agents.go`).
// This paragraph said until #1336 that guest code still ran on one agent at a time, which was true while
// nothing could start a second one. A guest starts one through the component spawn door,
// `burroughs:threads/spawn@0.1.0` (spawndoor.go, #1337, ADR 0131), which is offered only with this gate on. This
// said until #1337 that nothing a guest could call started one.
//
// # Off by default, and an opt-in
//
// It reads `== "1"`, as `gate:cm-threads` did before its flip. With it off, the pin's rule is enforced as it
// was before this gate existed: `walker.refuseSharedCanonMemory` refuses the component by name.
const cmSharedMemoryGateEnv = "BURROUGHS_CM_SHARED_MEMORY"

// cmSharedMemoryEnabled reports whether `gate:cm-shared-memory` is on. It is off unless
// `BURROUGHS_CM_SHARED_MEMORY=1`.
func cmSharedMemoryEnabled() bool { return os.Getenv(cmSharedMemoryGateEnv) == "1" }
