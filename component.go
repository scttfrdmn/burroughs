// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/component"
)

// componentsGateEnv is the config gate for the component mechanism (`gate:components`, ADR 0084). It is
// **on by default** as of the 2026-09-11 flip (Scott's stamp on #720's pre-registered forecast, the
// stamp-tier event behaviour 4 requires): the value-type suite is verified against both oracles and every
// unmodeled kind is refused by name, so a default build runs a `wasi:cli/run` component. The gate remains
// present as a rollback: set to "0" to refuse — the same refuse-by-name path, only the default differs.
const componentsGateEnv = "BURROUGHS_COMPONENTS"

// componentsEnabled reports whether the `gate:components` config gate is on. **On is the default** (the
// 2026-09-11 flip); only an explicit "0" refuses. A component is recognized (IsComponent) and, unless
// explicitly gated off, run.
func componentsEnabled() bool { return os.Getenv(componentsGateEnv) != "0" }

// Feature names a WebAssembly proposal capability an embedder enables for a component's core
// modules ([ComponentConfig.Features]). It is the engine's first caller-supplied capability surface
// (ADR 0088, ruled on #798): a **named set** rather than one field per capability, because a field
// per capability would grow the public surface every time a feature question arrives — the drift
// that decision exists to prevent. The container is extensible; its contents are guest-driven, so
// exactly the capabilities a real consumer needs are defined and **an unrecognized value is refused
// by name** rather than ignored.
type Feature string

// FeatureThreads enables the threads proposal's surface — the 0xFE atomics region and shared
// memories — for a component's core modules.
//
// An embedder supplies it to say what their artifact requires, which is not the same as flipping a
// gate: `gate:threads`' default is unchanged, and a component that supplies nothing is refused
// exactly as before. The consumer that forced this capability is a Go component: Go's compiler emits
// atomics unconditionally — a hello-world that starts no goroutines carries over a thousand of them —
// so no Go component can decode under the default set.
//
// Whether `gate:threads` off *should* admit atomics when nothing can spawn is a separate, open
// question (#799), deliberately not answered by supplying this capability.
const FeatureThreads Feature = "threads"

// resolveFeatures maps the caller's named capabilities onto the decoder's feature set, starting from
// the default. An unrecognized name is **refused by name**: the set must not silently accept a
// capability this engine does not implement, which is the guard that lets the container be
// extensible without becoming a place where typos pass.
func resolveFeatures(fs []Feature) (bin.Features, error) {
	feats := bin.DefaultFeatures()
	for _, f := range fs {
		switch f {
		case FeatureThreads:
			feats.Threads = true
		default:
			// "feature", not "component feature": this resolver serves the wasip1 path too as of #813,
			// and a message naming one caller's path is wrong for the other. Found by running it there.
			return bin.Features{}, fmt.Errorf("%w: unknown feature %q; this build recognizes %q",
				ErrUnsupported, string(f), string(FeatureThreads))
		}
	}
	return feats, nil
}

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
	Args   []string  // the component's argv, lowered by wasi:cli/environment.get-arguments
	Stdin  io.Reader // the component's stdin; nil means an empty stream
	Stdout io.Writer // the component's stdout; nil discards
	Stderr io.Writer // the component's stderr; nil discards

	// Features are the proposal capabilities this component's core modules require (ADR 0088).
	// Empty — the zero value — is the engine's default set, so an existing embedder is unaffected
	// and a component needing a gated capability is still refused by name. An unrecognized value is
	// refused by name rather than ignored.
	Features []Feature
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
	// `gate:components` is on by default as of the 2026-09-11 flip (ADR 0084, #720; behaviour 4's
	// stamp-tier event). The gate remains present as a rollback: an explicit `BURROUGHS_COMPONENTS=0`
	// refuses to run a component, by name — the same refuse-by-name path, only the default differs.
	if !componentsEnabled() {
		return 0, fmt.Errorf("%w: gate:components is off in this build (%s=0); unset it to run a component "+
			"(the gate is on by default as of the 2026-09-11 flip)",
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
	h.Args = c.Args
	feats, ferr := resolveFeatures(c.Features)
	if ferr != nil {
		return 0, ferr
	}
	in, err := component.InstantiateWithHostFeatures(wasm, h, feats)
	if err != nil {
		// `gate:async` (ADR 0086) refuses an async component at bind, by name; it is a distinct gate from
		// `gate:components`, off by default. Wrap its sentinel as ErrGated so the boundary classifies it
		// exit 6 (well-formed, gate off — grave #301), the same outcome `gate:components` off produces.
		if errors.Is(err, component.ErrAsyncGated) {
			return 0, fmt.Errorf("%w: %w", ErrGated, err)
		}
		// `gate:async` on but the async tier's execution not yet built (#739 slice 1): the engine reached
		// something it does not implement in this phase — exit 5 (exitUnsupported), not a gate decline (the
		// gate is on) and not the invocation's own failure. It refuses by name, never a silent no-op.
		if errors.Is(err, component.ErrAsyncNotImplemented) {
			return 0, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
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
