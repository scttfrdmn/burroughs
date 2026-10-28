// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestAComponentsInvalidCoreModuleIsRefusedAtLoad is #1162's witness: a component whose core module fails
// validation is refused at load, as `ErrInvalid`, through every public door, with the validator's own message.
//
// **Until #1162 no core module inside a component met the validator.** The loader decoded each one and handed
// it on, so the first fixture loaded and `Component.Call("f")` returned `u32(1)` from a function that declares
// an `i32` result and pushes an `i64`, with no error. The second reached the interpreter's `ErrNotValidated`
// check from a public path, which is #1134's census finding. `wasm-tools 1.258.0 validate --features all`
// refuses both.
//
// **All three doors**, because #1162 is a load-time verdict and the three classify independently:
// `LoadComponent`, `ComponentConfig.LoadComponent` and `ComponentConfig.Run`. A fix in one door that the
// others missed is the shape of the defect `LoadComponent`'s own comment records.
//
// **A declined core module is not witnessed here, and that is a fact about the corpus, not a gap in this
// test.** No construct the default decoder admits declines (#1134's census), so no fixture can reach
// `ErrCoreModuleDeclined` through a public path today. Its classification is the same `errors.Is` arm, one
// line below this one at each door.
//
// **Neuter, run:** with the `validate.Module` call removed from the loader, all six rows fail. Both fixtures load
// through both load doors, and `Run` refuses them only for having no `wasi:cli/run` export, which is not
// `ErrInvalid`.
func TestAComponentsInvalidCoreModuleIsRefusedAtLoad(t *testing.T) {
	for _, c := range []struct {
		fixture, want string
	}{
		{"internal/component/testdata/core-invalid-synth.wasm", "type mismatch"},
		{"internal/component/testdata/core-invalid-local-synth.wasm", "unknown local 5"},
	} {
		wasm, err := os.ReadFile(c.fixture)
		if err != nil {
			t.Fatal(err)
		}
		check := func(door string, err error) {
			t.Helper()
			switch {
			case err == nil:
				t.Errorf("%s: %s loaded; its core module fails validation, and the spec says reject it", door, c.fixture)
			case !errors.Is(err, ErrInvalid):
				t.Errorf("%s: %s refused, but not as ErrInvalid: %v", door, c.fixture, err)
			case !strings.Contains(err.Error(), c.want):
				t.Errorf("%s: %s refused as ErrInvalid without the validator's message %q: %v", door, c.fixture, c.want, err)
			case !strings.Contains(err.Error(), "core module 0"):
				t.Errorf("%s: %s refused without naming which core module: %v", door, c.fixture, err)
			}
		}

		comp, err := LoadComponent(wasm)
		if comp != nil {
			comp.Close()
		}
		check("LoadComponent", err)

		comp, err = ComponentConfig{}.LoadComponent(wasm)
		if comp != nil {
			comp.Close()
		}
		check("ComponentConfig.LoadComponent", err)

		_, err = ComponentConfig{}.Run(wasm)
		check("ComponentConfig.Run", err)
	}
}
