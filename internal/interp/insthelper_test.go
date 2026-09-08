// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// mustInst instantiates an import-free test module, failing if it does not link.
//
// Refuse-at-link (decision 0082) gave Instantiate a third, link-error return. Every module a unit test
// here builds is import-free, so that error is provably nil; a non-nil one is a finding — a test
// module unexpectedly carrying an import — not an expected outcome, so it is a `Fatalf` rather than a
// discarded value. This one helper is why the 33 call sites did not each grow an error check.
func mustInst(tb testing.TB, m *binary.Module) (*Instance, *Trap) {
	tb.Helper()
	in, trap, err := Instantiate(m)
	if err != nil {
		tb.Fatalf("instantiate: unexpected link failure on an import-free test module: %v", err)
	}
	return in, trap
}
