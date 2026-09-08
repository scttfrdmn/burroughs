// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"io"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/wasi"
)

// wasiModule is the import module name every `wasip1` guest declares its host functions under.
const wasiModule = "wasi_snapshot_preview1"

// WASIConfig runs a WASI preview-1 guest — [decision 0081][0081]'s public entry, the commitment [ADR
// 0029](docs/decisions/0029-the-public-boundary-run-on-a-validated-path-decline-as-a-third-outcome-and-a-value-that-converts.md)
// forces because the CLI reaches the runner only through this package, never `internal/`. Its shape
// mirrors the internal runner's, committed against the two guests that shaped it (hello needed no
// stdin; the stdin-reading guest did).
//
// [0081]: docs/decisions/0081-burroughs-run-detects-a-wasip1-command-from-the-modules-sections-and-routes-to-a-public-wasi-entry-before-any-plain-instantiate.md
type WASIConfig struct {
	Args   []string  // argv; defaults to {"program"}
	Env    []string  // "KEY=VALUE" pairs; defaults to none
	Stdin  io.Reader // fd 0; defaults to os.Stdin
	Stdout io.Writer // fd 1; defaults to os.Stdout
	Stderr io.Writer // fd 2; defaults to os.Stderr
}

// Run decodes, validates, and runs the guest's `_start`, returning the guest's exit code. A
// `proc_exit` is the code with a nil error; a decode/validate/link failure or a trap is `(0, err)` —
// the one-channel discipline that keeps an exit from reading as a silent success and a trap from
// reading as exit 0.
func (c WASIConfig) Run(wasm []byte) (exitCode int, err error) {
	return wasi.Run(wasi.Config{
		Wasm:   wasm,
		Args:   c.Args,
		Env:    c.Env,
		Stdin:  c.Stdin,
		Stdout: c.Stdout,
		Stderr: c.Stderr,
	})
}

// IsCommand reports whether wasm is a `wasip1` command: a module that **imports
// `wasi_snapshot_preview1`** and **exports a `_start` function**. It reads the module's import and
// export sections — the ruled fact-based detection — and does **not** instantiate, so it is
// independent of [#686](https://github.com/scttfrdmn/burroughs/issues/686)'s nil-resolver behavior.
//
// A module that cannot be decoded is reported `(false, err)`: the caller decides whether an
// undecodable module is "not a command" (route elsewhere and let a real instantiate classify it) or a
// failure to surface. `burroughs run` takes the former — it ignores the error and falls through to the
// path that classifies a malformed module onto the public sentinels.
func IsCommand(wasm []byte) (bool, error) {
	m, err := (&binary.Decoder{Features: wasi.GuestFeatures()}).DecodeModule(wasm)
	if err != nil {
		return false, err
	}
	importsWASI := false
	for i := range m.Imports {
		if m.Imports[i].Module == wasiModule {
			importsWASI = true
			break
		}
	}
	if !importsWASI {
		return false, nil
	}
	for i := range m.Exports {
		if m.Exports[i].Name == "_start" && m.Exports[i].Kind == binary.ExternFunc {
			return true, nil
		}
	}
	return false, nil
}
