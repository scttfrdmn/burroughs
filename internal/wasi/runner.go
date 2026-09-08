// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/validate"
)

// Config is one guest run: the module bytes and its standard-stream and startup environment.
//
// Internal-only for this slice — the public embedder API is deferred to its own decision because it is
// public API surface (ADR 0080, decision 7). A zero Config runs with `program` as argv[0], no
// environment, and the process's own stdout/stderr.
type Config struct {
	Wasm   []byte    // the guest module
	Args   []string  // argv; defaults to {"program"}
	Env    []string  // "KEY=VALUE" pairs; defaults to none
	Stdin  io.Reader // fd 0; defaults to os.Stdin
	Stdout io.Writer // defaults to os.Stdout
	Stderr io.Writer // defaults to os.Stderr
}

// GuestFeatures is the decoder feature set for a Go `wasip1` guest: the default proposals (which have
// `Threads` off), because a measured Go guest uses bulk memory and reference types — always-on
// baseline — and **no wasm atomics or shared memory** (ADR 0080's finding 2). A guest that decodes
// under this set needs no threads feature, which is the checkable form of "`gate:threads` is not
// load-bearing for this workload."
func GuestFeatures() bin.Features { return bin.DefaultFeatures() }

// Run decodes, validates, and instantiates the guest with the preview-1 host module, then invokes
// `_start`.
//
// **The return contract has one channel for two outcomes that must not be confused.** A `proc_exit`
// is unwrapped to `(code, nil)`; a decode/validate/link failure or a genuine trap is `(0, err)`. So an
// exit is never a silent success and a trap is never exit 0 — the same one-channel discipline
// [interp.HostFunc] uses, applied at the top.
func Run(cfg Config) (int, error) {
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Args == nil {
		cfg.Args = []string{"program"}
	}

	m, err := (&bin.Decoder{Features: GuestFeatures()}).DecodeModule(cfg.Wasm)
	if err != nil {
		return 0, fmt.Errorf("wasi: decode: %w", err)
	}
	if _, verr := validate.Module(m); verr != nil {
		return 0, fmt.Errorf("wasi: validate: %w", verr)
	}

	h := &host{
		args:   cfg.Args,
		env:    cfg.Env,
		stdin:  cfg.Stdin,
		stdout: cfg.Stdout,
		stderr: cfg.Stderr,
		start:  time.Now(),
	}
	in, trap, err := interp.InstantiateLinked(m, h.imports())
	if err != nil {
		return 0, fmt.Errorf("wasi: link: %w", err)
	}
	if trap != nil {
		return 0, fmt.Errorf("wasi: instantiate: %w", trap)
	}
	defer in.Close()

	if _, err := in.Invoke("_start"); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			return ee.code, nil
		}
		return 0, fmt.Errorf("wasi: _start: %w", err)
	}
	// A `_start` that returns without calling `proc_exit` is a normal exit 0 — WASI's convention.
	return 0, nil
}
