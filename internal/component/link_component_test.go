// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"testing"
)

// TestP3HelloRunIsCallableAndImportsReachTheStub is PR B's exit (bar the borrow-lifetime discipline):
// the Step-0 component loads, its core modules instantiate (including the recursive nested-component
// shim carrying the lifted run func), its wasi:cli/run export resolves to a callable, and invoking it
// drives the guest until — with no host supplied — it reaches a wasi import, which the stub host refuses
// by name. That the failure is ErrLinkRefused at a wasi import (not a load/instantiate/link error) is
// what proves the whole chain ran.
func TestP3HelloRunIsCallableAndImportsReachTheStub(t *testing.T) {
	b, err := os.ReadFile(fixtureWasm)
	if err != nil {
		t.Fatal(err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	err = in.Call("wasi:cli/run@0.2.3")
	if err == nil {
		t.Fatal("Call(run) succeeded under the stub host; expected a refusal when the guest reaches a wasi import")
	}
	if !errors.Is(err, ErrLinkRefused) {
		t.Errorf("Call(run) = %v, want a stub-host refusal (ErrLinkRefused) at a wasi import", err)
	}
}
