// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"io"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/wasi"
)

// wasiP1Module is the import module name every `wasip1` guest declares its host functions under.
const wasiP1Module = "wasi_snapshot_preview1"

// WASIP1Config runs a WASI **preview 1** guest — [decision 0081][0081]'s public entry.
//
// **Preview 1 is the compatibility on-ramp, not the target.** The thesis is "host real Go, track the
// spec edge to wasip3" (contract §6), and preview 1 exists here for a measured reason: the stock Go
// toolchain emits `wasip1` only — `wasip2`/`wasip3` are `unsupported GOOS/GOARCH pair` — so a program
// people write is a `wasip1` guest today. The version is in the name so the eventual p3 entry is not a
// rename of a name that claimed to be all of WASI. Retirement condition: a p3-emitting toolchain Go
// programs can use ([#688](https://github.com/scttfrdmn/burroughs/issues/688) tracks the recon).
//
// The shape is committed against the two guests that shaped it (hello needed no stdin; the
// stdin-reading guest did), and [ADR 0029] is why it lives here rather than in `internal/`: the CLI
// reaches the runner only through this package.
//
// [0081]: docs/decisions/0081-burroughs-run-detects-a-wasip1-command-from-the-modules-sections-and-routes-to-a-public-wasi-entry-before-any-plain-instantiate.md
type WASIP1Config struct {
	Args     []string  // argv; defaults to {"program"}
	Env      []string  // "KEY=VALUE" pairs; defaults to none
	Stdin    io.Reader // fd 0; defaults to os.Stdin
	Stdout   io.Writer // fd 1; defaults to os.Stdout
	Stderr   io.Writer // fd 2; defaults to os.Stderr
	Preopens []Preopen // granted directories; empty (the default) means no filesystem access
}

// Preopen grants the guest one directory: Host on the host, Guest the name the guest sees. **There is
// no default grant** (decision 0083's capability model) — a guest with no Preopen reaches no
// filesystem, and a `..`, symlink, or absolute path cannot escape a granted directory's resolved root.
type Preopen struct {
	Host  string
	Guest string
}

// Run decodes, validates, and runs the guest's `_start`, returning the guest's exit code. A
// `proc_exit` is the code with a nil error; a decode/validate/link failure or a trap is `(0, err)` —
// the one-channel discipline that keeps an exit from reading as a silent success and a trap from
// reading as exit 0.
func (c WASIP1Config) Run(wasm []byte) (exitCode int, err error) {
	preopens := make([]wasi.Preopen, len(c.Preopens))
	for i, p := range c.Preopens {
		preopens[i] = wasi.Preopen{Host: p.Host, Guest: p.Guest}
	}
	return wasi.Run(wasi.Config{
		Wasm:     wasm,
		Args:     c.Args,
		Env:      c.Env,
		Stdin:    c.Stdin,
		Stdout:   c.Stdout,
		Stderr:   c.Stderr,
		Preopens: preopens,
	})
}

// IsWASIP1Command reports whether wasm is a `wasip1` command: a module that **imports
// `wasi_snapshot_preview1`** and **exports a `_start` function**. It reads the module's import and
// export sections — the ruled fact-based detection, and inherently preview-1-specific, which is why
// the version is in the name — and does **not** instantiate, so it is independent of
// [#686](https://github.com/scttfrdmn/burroughs/issues/686)'s nil-resolver behavior.
//
// A module that cannot be decoded is reported `(false, err)`: the caller decides whether an
// undecodable module is "not a command" (route elsewhere and let a real instantiate classify it) or a
// failure to surface. `burroughs run` takes the former — it ignores the error and falls through to the
// path that classifies a malformed module onto the public sentinels.
func IsWASIP1Command(wasm []byte) (bool, error) {
	m, err := (&binary.Decoder{Features: wasi.GuestFeatures()}).DecodeModule(wasm)
	if err != nil {
		return false, err
	}
	importsWASI := false
	for i := range m.Imports {
		if m.Imports[i].Module == wasiP1Module {
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
