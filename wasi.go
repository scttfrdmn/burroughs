// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"fmt"
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

	// Features are the proposal capabilities this guest requires — ADR 0088's caller-supplied
	// capability set, on its **second** path (#813). The same [Feature] names the component path
	// takes, the same refusal of an unrecognized name, and the same non-effect on any gate's default:
	// supplying one is an embedder saying what their artifact needs, not a gate flipping.
	//
	// The consumer that forced it: Phase 4's fork emits Go `wasip1` guests carrying atomic
	// instructions — **1025 in a hello-world that starts no goroutines** — so none of them decodes
	// under the default set. ADR 0088 deferred this path by name and recorded the trigger; this is
	// that trigger firing.
	//
	// Supplying [FeatureThreads] here also admits a guest with real threads, which is why the
	// preview-1 host's fd table is locked as of the same change rather than later.
	Features []Feature
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
	feats, ferr := c.features()
	if ferr != nil {
		return 0, ferr
	}
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
		Features: feats,
	})
}

// features resolves the config's named capabilities, or reports nil for "the caller asked for nothing"
// so the runner keeps its own default. Nil rather than a resolved copy of the default, because the
// default belongs to `internal/wasi` and duplicating it here would be a second copy to keep in step.
func (c WASIP1Config) features() (*binary.Features, error) {
	if len(c.Features) == 0 {
		return nil, nil
	}
	f, err := resolveFeatures(c.Features)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// IsCommand reports whether wasm is a `wasip1` command, decoding under **this config's** capabilities.
//
// **It exists because [IsWASIP1Command] answers a capability question in an identity channel.** That
// function decodes with the default set, so a guest needing a capability comes back `(false, err)` —
// and a caller that tests only the bool reads "not a wasip1 command" about a module that imports
// `wasi_snapshot_preview1` and exports `_start`. It *is* one; this build was not permitted to decode
// it. `burroughs run` printed exactly that falsehood before #813.
//
// Detection answers *what is this*; capabilities answer *may I run it*. Deciding the first with the
// second lets a gate deny a module's identity, which is the face-4 family — *a call returning false
// reads as "nothing to do" when it means "cannot be reached"* — reaching this engine's own public
// surface for the first time.
//
// [IsWASIP1Command] keeps its behaviour: its documented contract already says an undecodable module is
// `(false, err)` and that the caller chooses what to do, so the repair is this sibling plus call sites
// that stop flattening the two channels — not a signature change to a published function.
func (c WASIP1Config) IsCommand(wasm []byte) (bool, error) {
	feats, ferr := c.features()
	if ferr != nil {
		return false, ferr
	}
	set := wasi.GuestFeatures()
	if feats != nil {
		set = *feats
	}
	return isWASIP1CommandWith(wasm, set)
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
// A guest that needs a capability this build gates off is `(false, err)` too, and the error says which
// gate — so the two meanings of `false` are distinguishable, but only by a caller that reads the error.
// [WASIP1Config.IsCommand] is the form that decodes under supplied capabilities (#813).
func IsWASIP1Command(wasm []byte) (bool, error) {
	return isWASIP1CommandWith(wasm, wasi.GuestFeatures())
}

// isWASIP1CommandWith is the detection body, parameterized on the feature set so the exported
// capability-free form and [WASIP1Config.IsCommand] cannot drift apart — one predicate, two entries.
func isWASIP1CommandWith(wasm []byte, feats binary.Features) (bool, error) {
	m, err := (&binary.Decoder{Features: feats}).DecodeModule(wasm)
	if err != nil {
		// **The error is now classifiable, and that is a deliberate, stated change to a published
		// function's error VALUE (#813).** It used to return the decoder's raw error, which matched no
		// public sentinel — measured — so a caller could not tell "this build gates a proposal your
		// module uses" from "your module is broken", and `burroughs run` mapped a gated guest onto its
		// generic failure code. [Config.Instantiate] has classified these two since grave #301; this is
		// the same two arms in the same order, narrow before general, so the general one cannot absorb
		// the gated case. The bool channel is untouched.
		if errors.Is(err, binary.ErrFeatureDisabled) {
			return false, fmt.Errorf("%w: %w", ErrGated, err)
		}
		return false, fmt.Errorf("%w: %w", ErrMalformed, err)
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
