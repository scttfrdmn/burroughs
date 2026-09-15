// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"testing"
)

// TestOuterAliasedTypeRefusesByNameNotCrash witnesses the #753 fix: a component whose instance-import func
// carries an outer type alias in its signature (future-read-outer-alias.wasm) used to stack-overflow
// resolveVal (a self-referential VRef{0} placeholder). The fix — a distinct VUnresolvedAlias marker plus a
// visited set in resolveVal — makes it Load without crashing, and the lower of that unresolved type refuses
// BY NAME at instantiate (the correct refuse-by-name-at-the-earliest-point outcome) rather than crashing or
// binding over a type the engine did not resolve. The proper outer-alias resolution later relaxes this.
func TestOuterAliasedTypeRefusesByNameNotCrash(t *testing.T) {
	b, err := os.ReadFile("testdata/future-read-outer-alias.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	// Load must not crash (the whole point — resolveVal no longer overflows on the placeholder).
	if _, lerr := Load(b); lerr != nil {
		t.Fatalf("Load returned an error (should Load cleanly, refuse at instantiate): %v", lerr)
	}
	t.Setenv("BURROUGHS_ASYNC", "1")
	_, ierr := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if !errors.Is(ierr, ErrUnsupportedForm) {
		t.Fatalf("instantiate err = %v, want ErrUnsupportedForm (refuse by name on the unresolved outer alias)", ierr)
	}
}
