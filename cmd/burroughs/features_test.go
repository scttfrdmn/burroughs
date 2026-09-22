// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

// atomicsGuest is the committed 113-byte wasip1 fixture whose only gated construct is one 0xFE atomic
// RMW; the package that owns it describes it (`internal/wasi/testdata/atomics-wasip1-synth.wat`).
const atomicsGuest = "../../internal/wasi/testdata/atomics-wasip1-synth.wasm"

// TestRunSuppliesCapabilitiesFromTheCommandLine is #813 at the CLI: `--features` reaches the engine, and
// all three outcomes are asserted rather than only the one that works.
//
// The middle case is the one that did not exist before: **the capability was unreachable from
// `burroughs run` on either path.** ADR 0088 added `ComponentConfig.Features` on 2026-09-18 and the CLI
// never passed it, which is why Phase 4's two-engine claim runs through an internal test.
func TestRunSuppliesCapabilitiesFromTheCommandLine(t *testing.T) {
	// WITHHELD → gated, and the exit code says *gated* rather than *broken*. That distinction is grave
	// #301's and it is the reason this asserts the code and not just non-zero: a script has to be able
	// to tell "rebuild with the capability" from "fix your module".
	var out, errBuf bytes.Buffer
	if code := dispatch(&out, &errBuf, []string{"run", atomicsGuest, "--", "x"}); code != exitGated {
		t.Errorf("without --features: exit %d, want exitGated (%d)\nstderr: %q",
			code, exitGated, errBuf.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "--features=threads") {
		t.Errorf("stderr = %q, want a hint naming the capability: a user told only "+
			"'gate is off' cannot act on it", got)
	}
	// And the error is reported ONCE. The first version printed it here and again from `diagnose`,
	// which reports one failure twice — found by running the binary, not by reading the code.
	if n := strings.Count(errBuf.String(), "feature gate disabled"); n != 1 {
		t.Errorf("the gate error appears %d times in stderr, want 1:\n%s", n, errBuf.String())
	}

	// SUPPLIED → runs, and the guest's own exit code (0, via proc_exit) comes back.
	out.Reset()
	errBuf.Reset()
	if code := dispatch(&out, &errBuf, []string{"run", "--features=threads", atomicsGuest}); code != 0 {
		t.Errorf("with --features=threads: exit %d, want 0\nstderr: %q", code, errBuf.String())
	}

	// UNRECOGNIZED → refused by name, and the hint must NOT appear: the user already passed
	// `--features`, so telling them to pass it is advice that cannot work.
	out.Reset()
	errBuf.Reset()
	code := dispatch(&out, &errBuf, []string{"run", "--features=threads,no-such-cap", atomicsGuest})
	if code != exitUnsupported {
		t.Errorf("with an unrecognized capability: exit %d, want exitUnsupported (%d)\nstderr: %q",
			code, exitUnsupported, errBuf.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "no-such-cap") {
		t.Errorf("stderr = %q, want the unrecognized name quoted back", got)
	}
	if got := errBuf.String(); strings.Contains(got, "--features=threads") {
		t.Errorf("stderr = %q: the capability hint fired for a bad NAME, where it is misleading", got)
	}
	// The message must not name one caller's path, because the resolver serves both. It said
	// "unknown component feature" on the wasip1 path until this was run.
	if got := errBuf.String(); strings.Contains(got, "component feature") {
		t.Errorf("stderr = %q: the shared resolver named the component path while serving wasip1", got)
	}
}

// TestFeatureFlagRejectsAnEmptyName covers the flag's own parsing, which is the CLI's and not the
// engine's: the recognized *set* is the engine's business (the flag deliberately does not know it), but
// "" is not a name at all and belongs here.
func TestFeatureFlagRejectsAnEmptyName(t *testing.T) {
	// A fresh flag per case, because the cases are independent claims and sharing one made this test
	// count the first case's residue as the second case's result — which is how the all-or-nothing
	// defect in Set was found.
	var bad featureFlag
	if err := bad.Set("threads,,other"); err == nil {
		t.Error("an empty capability name was accepted; --features=a,,b is a typo, not a request")
	}
	if got := len(bad.features()); got != 0 {
		t.Errorf("a rejected --features left %d capabilities behind, want 0: Set is all-or-nothing so a "+
			"refused value cannot half-apply", got)
	}

	var good featureFlag
	if err := good.Set("threads,other"); err != nil {
		t.Errorf("a comma-separated pair was rejected: %v", err)
	}
	if got := len(good.features()); got != 2 {
		t.Errorf("collected %d capabilities, want 2 — comma-separated values must both arrive", got)
	}
}
