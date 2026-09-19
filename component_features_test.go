// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

const atomicsComponent = "internal/component/testdata/atomics-component-synth.wasm"

// TestComponentConfigFeaturesIsTheCallerSuppliedCapabilitySurface is ADR 0088's public surface — the
// engine's FIRST caller-supplied capability surface, stamped as a named set rather than a field per
// capability so the surface does not grow every time a feature question arrives.
//
// Three cases, and the third is a different kind of claim from the other two:
//
//   - **Withheld (the zero value) → refused by name.** An existing embedder is unaffected: no Features
//     means the engine's default set, and a component needing a gated capability is refused exactly as
//     before ADR 0088.
//   - **Supplied → the capability takes effect.** The atomics refusal is gone; the component decodes.
//     (This fixture exports `hello`, not `wasi:cli/run`, so `Run` then reports no run export — a
//     *world* mismatch, which is the proof that the *feature* refusal is no longer what stops it.)
//   - **Unrecognized → refused by name.** This one has **no consumer and cannot have one**: exactly one
//     capability is defined, so nothing but a constructed value can be unrecognized. It is a
//     **structural guard on the surface's precedent**, not a guest-driven witness, and it is what lets
//     the container be extensible without becoming a place where a typo is silently ignored.
func TestComponentConfigFeaturesIsTheCallerSuppliedCapabilitySurface(t *testing.T) {
	wasm, err := os.ReadFile(atomicsComponent)
	if err != nil {
		t.Fatalf("fixture missing (it is committed): %v", err)
	}

	// Withheld — the zero value is the old behaviour, refused by name.
	_, werr := (ComponentConfig{Stdout: io.Discard}).Run(wasm)
	if werr == nil {
		t.Error("a component using atomics ran with no Features supplied — the default must still refuse")
	} else if !strings.Contains(werr.Error(), "0xfe") {
		t.Errorf("refusal %q must name the 0xFE region", werr.Error())
	}

	// Supplied — the feature refusal is gone; what remains is the world mismatch, which is the point.
	_, err = (ComponentConfig{Stdout: io.Discard, Features: []Feature{FeatureThreads}}).Run(wasm)
	if err != nil && strings.Contains(err.Error(), "0xfe") {
		t.Errorf("the threads capability was supplied and the 0xFE refusal still fired: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "run export") {
		t.Errorf("want the world mismatch (no wasi:cli/run export) once the feature is supplied, got %v", err)
	}

	// Unrecognized — a STRUCTURAL guard: refused by name, listing what this build recognizes.
	_, err = (ComponentConfig{Features: []Feature{Feature("no-such-capability")}}).Run(wasm)
	if err == nil {
		t.Fatal("an unrecognized capability was accepted — the set must not silently admit a capability " +
			"this engine does not implement")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("an unrecognized capability's refusal is %v, want ErrUnsupported", err)
	}
	if got := err.Error(); !strings.Contains(got, "no-such-capability") || !strings.Contains(got, "threads") {
		t.Errorf("refusal %q must name the unrecognized value AND what is recognized", got)
	}
}
