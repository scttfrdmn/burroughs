// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	burroughs "github.com/scttfrdmn/burroughs"
)

// atomicsWASIP1 is the committed fixture: 113 bytes, imports `wasi_snapshot_preview1.proc_exit`,
// exports `_start`, and its only gated construct is one 0xFE-region atomic RMW. The memory is
// deliberately unshared so the threads gate's *other* half cannot be what refuses it — the refusal
// must be attributable to the atomics region specifically. Source committed beside it as `.wat`.
//
// A synthesized fixture rather than the Go guest that forced this (ADR 0088's precedent, #802): the Go
// artifact is ~1.8MB and its own end-to-end result is recorded in Phase 4's registration.
const atomicsWASIP1 = "internal/wasi/testdata/atomics-wasip1-synth.wasm"

func readFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(atomicsWASIP1)
	if err != nil {
		t.Fatalf("fixture missing (it is committed): %v", err)
	}
	return b
}

// TestWASIP1FeaturesAreCallerSupplied is ADR 0088's append (#813) on the wasip1 path, and it asserts
// both halves for #714's reason: a gated entry's forecast registers the REFUSAL, not only the enabled
// exit. A test that only showed the capability working would pass just as well against a surface that
// had removed the gate.
func TestWASIP1FeaturesAreCallerSupplied(t *testing.T) {
	wasm := readFixture(t)

	// WITHHELD, under the rollback → refused, by name, and classifiable. This was the default until ADR 0106
	// flipped `gate:threads` on. It is now what `BURROUGHS_THREADS=0` must restore, so the off state keeps its
	// witness: supplying a capability makes the refusal conditional, and the rollback must make it reappear.
	t.Setenv("BURROUGHS_THREADS", "0")
	if _, err := (burroughs.WASIP1Config{Stdout: io.Discard, Stderr: io.Discard}).Run(wasm); err == nil {
		t.Fatal("a wasip1 guest using the 0xFE atomics region RAN with BURROUGHS_THREADS=0 — the rollback " +
			"must restore the refusal, or the flip removed a gate instead of changing its default")
	} else if !strings.Contains(err.Error(), "0xfe") {
		t.Errorf("refusal %q must name the 0xFE region (refuse by name)", err)
	}

	// SUPPLIED → the same bytes run, and the guest's own exit code comes back.
	code, err := (burroughs.WASIP1Config{
		Stdout:   io.Discard,
		Stderr:   io.Discard,
		Features: []burroughs.Feature{burroughs.FeatureThreads},
	}).Run(wasm)
	if err != nil {
		t.Fatalf("the same guest did not run with the threads capability supplied: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (the fixture calls proc_exit(0))", code)
	}

	// UNRECOGNIZED → refused by name. The container is extensible; that is only safe if a name this
	// build does not implement cannot pass silently.
	_, err = (burroughs.WASIP1Config{
		Stdout:   io.Discard,
		Stderr:   io.Discard,
		Features: []burroughs.Feature{"threads", "no-such-capability"},
	}).Run(wasm)
	if err == nil {
		t.Fatal("an unrecognized capability name was ACCEPTED; the set must refuse what this build does " +
			"not implement, or a typo silently becomes a missing capability")
	}
	if !errors.Is(err, burroughs.ErrUnsupported) {
		t.Errorf("unrecognized capability error = %v, want it to wrap ErrUnsupported so a caller can "+
			"classify it", err)
	}
	if !strings.Contains(err.Error(), "no-such-capability") {
		t.Errorf("refusal %q must name the unrecognized capability", err)
	}

	// THE DEFAULT, since ADR 0106 → the same bytes run with nothing supplied. This is the flip's promise on
	// this path, asserted beside the refusal the rollback restores, so neither half can pass alone.
	t.Setenv("BURROUGHS_THREADS", "")
	code, err = (burroughs.WASIP1Config{Stdout: io.Discard, Stderr: io.Discard}).Run(wasm)
	if err != nil {
		t.Fatalf("with gate:threads on by default, a wasip1 guest using the 0xFE region was refused with "+
			"nothing supplied: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d under the default, want 0", code)
	}
}

// TestDetectionIsNotAPermissionDecision is the substantive half of #813, and it is the failure ADR
// 0088's deferral did not anticipate: the deferral expected a *permission* problem ("a guest using
// atomics is equally unloadable") and on this path it is also an *identity* problem, because detection
// decodes.
//
// The discriminating pair is the same bytes through the two entries. `IsWASIP1Command` answers under
// the default set and reports `false` — which a caller testing only the bool reads as "not a wasip1
// command" about a module that imports `wasi_snapshot_preview1` and exports `_start`. It IS one. This
// is the face-4 family (*a call returning false reads as "nothing to do" when it means "cannot be
// reached"*) on the engine's own public surface.
func TestDetectionIsNotAPermissionDecision(t *testing.T) {
	wasm := readFixture(t)

	// Under the rollback, because the property is about a GATED module and, since ADR 0106, the default no
	// longer gates this one. `BURROUGHS_THREADS=0` restores the default this test was written against, and the
	// property must still hold there: an embedder who rolls back gets (false, ErrGated), not (false, nil).
	t.Setenv("BURROUGHS_THREADS", "0")

	// The capability-free entry: false, with an error that says why. Both channels are asserted,
	// because the bool alone is what misled `burroughs run` and the error alone would not show that.
	isCmd, err := burroughs.IsWASIP1Command(wasm)
	if isCmd {
		t.Fatal("the fixture decoded under the default set — then it is not a witness for this, and the " +
			"gate it was built to trip has moved")
	}
	if err == nil {
		t.Fatal("IsWASIP1Command reported (false, nil) for a gated module: that is the bug this test " +
			"exists for — 'not a command' asserted about a module that is one")
	}
	if !errors.Is(err, burroughs.ErrGated) {
		t.Errorf("error = %v, want ErrGated so a caller can tell 'gated' from 'malformed' (grave #301's "+
			"distinction, which this function did not make before #813)", err)
	}

	// The capability-aware entry, same bytes: true. This is the answer that was unavailable.
	isCmd, err = (burroughs.WASIP1Config{
		Features: []burroughs.Feature{burroughs.FeatureThreads},
	}).IsCommand(wasm)
	if err != nil {
		t.Fatalf("IsCommand with the capability supplied: %v", err)
	}
	if !isCmd {
		t.Fatal("IsCommand did not recognize a module that imports wasi_snapshot_preview1 and exports " +
			"_start, with the capability it needs supplied — the identity answer is still being decided " +
			"by a gate")
	}

	// And the pair is non-vacuous in the other direction: with nothing supplied, the capability-aware
	// entry agrees with the capability-free one. Without this, the test above would pass against an
	// IsCommand that ignored its features and always said true.
	isCmd, err = (burroughs.WASIP1Config{}).IsCommand(wasm)
	if isCmd || err == nil {
		t.Errorf("IsCommand with NO capability supplied = (%v, %v), want (false, non-nil): it must "+
			"decode under this config's set, not under an always-permissive one", isCmd, err)
	}

	// And under the default since ADR 0106, the capability-free entry answers true: the module IS a command,
	// and nothing now stands between it and the answer.
	t.Setenv("BURROUGHS_THREADS", "")
	if isCmd, err = burroughs.IsWASIP1Command(wasm); !isCmd || err != nil {
		t.Errorf("IsWASIP1Command under the default = (%v, %v), want (true, nil) now that gate:threads is on",
			isCmd, err)
	}
}

// TestStockGuestDetectionIsUnchanged is the regression half. A guest that needs no capability must be
// detected exactly as before, because #813 widened a surface and must not have moved the default.
// `cat` is one of the committed testdata guests, compiled by the test's own toolchain.
func TestStockGuestDetectionIsUnchanged(t *testing.T) {
	// A module that imports nothing and exports nothing is not a command, and the answer must be
	// (false, nil) — no error — so "not a command" stays distinguishable from "could not decode".
	empty := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	isCmd, err := burroughs.IsWASIP1Command(empty)
	if isCmd || err != nil {
		t.Errorf("empty module = (%v, %v), want (false, nil): a well-formed non-command must not be "+
			"reported through the error channel", isCmd, err)
	}
	isCmd, err = (burroughs.WASIP1Config{Features: []burroughs.Feature{burroughs.FeatureThreads}}).
		IsCommand(empty)
	if isCmd || err != nil {
		t.Errorf("empty module via IsCommand = (%v, %v), want (false, nil)", isCmd, err)
	}
}
