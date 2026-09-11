// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/scttfrdmn/burroughs/internal/component"
)

// componentsGateEnv is the config gate for the component mechanism (`gate:components`, ADR 0084, off by
// default). The mechanism is present and the public entries below are additive surface over it, but a
// default build refuses to *run* a component — the refuse-by-name discipline the whole track uses — until
// the gate flip (its own stamp-tier event, behaviour 4, when the full value-type suite is green). Set to
// "1" to opt into the gated mechanism; a test enabling the exit sets it, a default build does not.
const componentsGateEnv = "BURROUGHS_COMPONENTS"

// componentsEnabled reports whether the `gate:components` config gate is on. Off is the default: a
// component is recognized (IsComponent) but not run.
func componentsEnabled() bool { return os.Getenv(componentsGateEnv) == "1" }

// ComponentConfig runs a WebAssembly **component** whose world is `wasi:cli/run` — the p3 track's
// public entry (ADR 0084 / 0085, #694), the component analogue of [WASIP1Config].
//
// **The version is in neither the type nor the method, unlike `WASIP1Config`.** A component names its
// own WASI version in its imports (`wasi:cli/run@0.2.3`, `wasi:io/streams@0.2.3`, …), so the entry does
// not: `Run` reaches whatever `wasi:cli/run` the component exports. Preview 1's version is in its type
// name because the *host* chooses it; a component's is the guest's own declaration.
//
// This slice runs a `wasi:cli/run` world with the standard streams. Value-carrying component exports an
// embedder calls with typed arguments are the `ComponentValue` surface (ADR 0085), which lands with its
// first embedder consumer — a component export called with values, which this stdio-run entry is not.
type ComponentConfig struct {
	Stdin  io.Reader // the component's stdin; nil means an empty stream
	Stdout io.Writer // the component's stdout; nil discards
	Stderr io.Writer // the component's stderr; nil discards
}

// Run instantiates the component against a preview-2 host over the configured streams, calls its
// `wasi:cli/run` export, and returns the guest's exit code. A guest `exit(err)` is the code with a nil
// error; a load/link failure or a trap is `(0, err)` — the one-channel discipline [WASIP1Config.Run]
// keeps, so an exit does not read as a silent success nor a trap as exit 0.
//
// **Stdout binds at the writer, not by a second stdio implementation** (ADR 0083's implement-once /
// bind-twice, the shared reading): the same `io.Writer` a `WASIP1Config` would receive is handed to the
// component host, which writes the guest's `list<u8>` to it — the preview-1 and preview-2 sides marshal
// different ABIs (iovecs vs a canon list) onto one sink, rather than reimplementing stdio twice.
func (c ComponentConfig) Run(wasm []byte) (exitCode int, err error) {
	// `gate:components` is off by default: the mechanism is present but a default build refuses to run a
	// component, by name (ADR 0084, behaviour 4). The flip to on is its own stamp-tier event, when the
	// full value-type suite is green — not this slice, whose one path is hello-world's.
	if !componentsEnabled() {
		return 0, fmt.Errorf("%w: gate:components is off in this build; set %s=1 to run a component "+
			"(the mechanism is present, the default-on flip is pending its full value-type suite)",
			ErrGated, componentsGateEnv)
	}
	stdout, stderr := c.Stdout, c.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	h := component.NewHost(stdout, stderr, c.Stdin)
	in, err := component.InstantiateWithHost(wasm, h)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err := in.CallRun(); err != nil {
		return 0, err
	}
	if h.Exited {
		return int(h.ExitCode), nil
	}
	return 0, nil
}

// IsComponent reports whether wasm is a WebAssembly component rather than a core module: it reads the
// 8-byte preamble's layer field (a component is layer 1, a core module layer 0), mirroring
// [IsWASIP1Command]'s fact-based detection without instantiating. Bad magic or a short header is
// `(false, err)`; the caller decides whether that is "not a component" or a failure to surface —
// `burroughs run` takes the former and lets a real load classify the bytes.
func IsComponent(wasm []byte) (bool, error) {
	if len(wasm) < 8 || string(wasm[0:4]) != "\x00asm" {
		return false, fmt.Errorf("burroughs: not a wasm binary (bad magic)")
	}
	layer := binary.LittleEndian.Uint16(wasm[6:8])
	return layer == 1, nil
}
