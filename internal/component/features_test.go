// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"strings"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestComponentCoreModuleFeaturesAreCallerSupplied is ADR 0088's mechanism at the loader: the feature
// set used to decode a component's core modules comes from the caller, not from a hardcoded
// `DefaultFeatures()`. Both halves are asserted, because the decision's shape is that the refusal
// becomes *conditional on what the caller supplied* rather than disappearing (#800's forecast, #714's
// lesson — a gated entry's forecast registers the refusal, not only the enabled exit):
//
//   - **Withheld → refused by name.** The default set is unchanged, so a component whose core module
//     uses the 0xFE atomics region is refused naming the region and the gate. This is the behaviour on
//     main before ADR 0088 and it must survive as the default.
//   - **Supplied → loads, and the export answers.** With the threads capability supplied, the same
//     bytes decode, instantiate, and the lifted export returns its value.
//
// The fixture is a 180-byte synthesized component (`atomics-component-synth.wasm`) rather than the Go
// component that forced this decision: that artifact is 1.8MB, four times this tree's entire component
// testdata, so committing it would pay a large repository cost for a claim this fixture makes exactly.
// The Go component's own end-to-end result is recorded on #800 (it returns 42 on both engines).
func TestComponentCoreModuleFeaturesAreCallerSupplied(t *testing.T) {
	b, err := os.ReadFile("testdata/atomics-component-synth.wasm")
	if err != nil {
		t.Fatalf("fixture missing (it is committed): %v", err)
	}

	// Withheld: the default set refuses the 0xFE region, by name.
	_, err = InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err == nil {
		t.Fatal("a component using the 0xFE atomics region loaded under the DEFAULT feature set — the " +
			"refusal must survive ADR 0088 as the default, or the surface removed a gate instead of " +
			"making it conditional")
	}
	if got := err.Error(); !strings.Contains(got, "0xfe") {
		t.Errorf("refusal %q must name the 0xFE region (refuse by name)", got)
	}

	// Supplied: the same bytes load, and the export answers.
	feats := bin.DefaultFeatures()
	feats.Threads = true
	in, err := InstantiateWithHostFeatures(b, NewHost(io.Discard, io.Discard, nil), feats)
	if err != nil {
		t.Fatalf("the same component did not load with the threads capability supplied: %v", err)
	}
	defer in.Close()
	cd, ok := in.export.exports["hello"]
	if !ok || cd.fn == nil {
		t.Fatal("no hello export")
	}
	res, cerr := cd.fn.core.inst.Invoke(cd.fn.core.name)
	if cerr != nil {
		t.Fatalf("calling the lifted export: %v", cerr)
	}
	// A sync lift of a scalar u32 moves no values through the codec, so the core func's return IS the
	// component's return — stated because reading the core return is a weaker claim for any other type.
	if len(res) != 1 || res[0].Bits != 42 {
		t.Errorf("hello() = %v, want 42", res)
	}
}
