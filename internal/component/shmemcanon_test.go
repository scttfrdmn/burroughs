// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestASharedMemoryAsACanonMemoryOptionIsRefusedByName is #1141's witness (the chair's ruling of 2026-10-27, item
// 4). A component whose `canon lower` or `canon lift` names a SHARED memory as its `(memory …)` option is refused
// by name.
//
// It was admitted until this slice, while wasm-tools 1.258.0 refuses both fixtures (*"mismatch in the shared flag
// for memories"*). The pinned reference sets no rule for the option, and nothing witnessed what admitting it does.
// ADR 0102's posture is to follow the toolchain's stricter refusal unless Burroughs implements and witnesses what
// the pin admits.
//
// **The control:** the same shared memory, used by the component without being a canon option, still loads, so
// the refusal is the option and not the memory. All three fixtures are in testdata beside their `.wat`, built
// with wasm-tools 1.258.0. wasm-tools refuses the two canon fixtures and validates the control.
//
// **Neuter, run:** with `refuseSharedCanonMemory` returning nil, both canon fixtures instantiate again, and both
// arms fail.
func TestASharedMemoryAsACanonMemoryOptionIsRefusedByName(t *testing.T) {
	for _, f := range []string{"shmem-canon-lower-synth.wasm", "shmem-canon-lift-synth.wasm"} {
		t.Run(f, func(t *testing.T) {
			wasm, err := os.ReadFile("testdata/" + f)
			if err != nil {
				t.Fatalf("the committed fixture is missing, so this arm has no subject: %v", err)
			}
			in, err := Instantiate(wasm)
			if err == nil {
				in.Close()
				t.Fatal("a shared memory used as a canon (memory …) option was admitted")
			}
			if !errors.Is(err, ErrUnsupportedForm) {
				t.Errorf("refused, but not as ErrUnsupportedForm, so the public boundary misclassifies it: %v", err)
			}
			if !strings.Contains(err.Error(), "shared memory") || !strings.Contains(err.Error(), "#1141") {
				t.Errorf("refused, but not by name: %v", err)
			}
		})
	}
	t.Run("the_control_a_shared_memory_without_that_use_still_loads", func(t *testing.T) {
		wasm, err := os.ReadFile("testdata/shmem-no-canon-synth.wasm")
		if err != nil {
			t.Fatalf("the committed control is missing: %v", err)
		}
		in, err := Instantiate(wasm)
		if err != nil {
			t.Fatalf("a component with a shared memory used as no canon option was refused: %v", err)
		}
		in.Close()
	})
}
