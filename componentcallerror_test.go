// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestAMistypedComponentCallArgumentIsTheCallersError is #1129's witness (v0.12.0; the chair's ruling of
// 2026-10-27, item 3), on the public path it was found on. `cmthreads/switch.wasm`'s `run(id: u32)`, called with a
// string, is refused, but **not** as `ErrUnsupported`. A wrong argument is the caller's mistake, and core
// `Instance.Call`'s arity and type checks classify it the same way: as a plain error, naming the parameter and both
// types. It used to read *"feature not implemented in this phase: … unsupported instantiation form"*.
//
// **Neuter, run:** with the `ErrUnsupportedForm` wrap restored at the type check (`internal/component/callvalues.go`),
// the type arm fails, classified `ErrUnsupported`.
func TestAMistypedComponentCallArgumentIsTheCallersError(t *testing.T) {
	w, err := os.ReadFile("internal/component/testdata/cmthreads/switch.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(w)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	defer func() { _ = c.Close() }()

	s, err := burroughs.ComponentString("not a u32")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		name string
		args []burroughs.ComponentValue
		want []string
	}{
		{"the_wrong_type", []burroughs.ComponentValue{s}, []string{`"id"`, "u32", "string"}},
		{"the_wrong_arity", nil, []string{"takes 1 parameter(s), got 0"}},
	} {
		t.Run(a.name, func(t *testing.T) {
			_, err := c.Call(context.Background(), "run", a.args...)
			if err == nil {
				t.Fatal("the mistaken call was accepted")
			}
			if errors.Is(err, burroughs.ErrUnsupported) {
				t.Errorf("classified ErrUnsupported, which tells the caller its own mistake is a missing feature: %v", err)
			}
			if strings.Contains(err.Error(), "unsupported instantiation form") {
				t.Errorf("the message still calls a caller's mistake an unsupported form: %v", err)
			}
			for _, w := range a.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal %q does not name %q", err, w)
				}
			}
		})
	}
}
