// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"
)

// TestEngineWiringChecksNameTheEngineNotTheModule is #1134's class B refile witness. Eleven checks that
// assert a fact about the engine's own wiring were filed under ErrNotValidated, which tells an embedder a
// module reached the interpreter unvalidated. That sends them to audit a module with nothing wrong in it.
// ADR 0077 made ErrEngineInvariant for exactly this category, and the census on #1134 found them misfiled.
//
// The rows are the class B sites a test can reach without building an engine in a broken state: the three
// boundary accessors given no memory. Each must be ErrEngineInvariant and must **not** be ErrNotValidated. A
// fourth, the element segment of non-reference type, is held by TestElemSegmentWithANumericElementTypeIsRefused,
// whose expression is chosen to reach the guard and nothing else. The other seven class B sites need a thread no
// world admitted, a nil bound memory, an atomic kind outside the table, or a re-export resolving to a
// non-definer. Those states are unreachable by construction, which is why they are invariants, and they are
// refiled by the same edit without a row here.
//
// NEUTER, run (2026-10-28, darwin/arm64): with `CheckBoundaryRange`'s sentinel put back to ErrNotValidated, its
// row fails on both assertions.
func TestEngineWiringChecksNameTheEngineNotTheModule(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"CheckBoundaryRange with no memory", CheckBoundaryRange(nil, 0, 0)},
		{"WriteBoundaryMemory with no memory", WriteBoundaryMemory(nil, 0, nil)},
		{"ReadBoundaryMemory with no memory", func() error { _, err := ReadBoundaryMemory(nil, 0, 0); return err }()},
	} {
		if !errors.Is(c.err, ErrEngineInvariant) {
			t.Errorf("%s: %v, want ErrEngineInvariant", c.name, c.err)
		}
		if errors.Is(c.err, ErrNotValidated) {
			t.Errorf("%s: %v also wraps ErrNotValidated, which blames the module for the engine's wiring", c.name, c.err)
		}
	}
}
