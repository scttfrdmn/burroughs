// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// TestASharedMemoryAsACanonMemoryOptionIsRefusedByName is #1141's witness (the chair's ruling of 2026-10-27, item
// 4), and since #1335 it is `gate:cm-shared-memory`'s gate-off row. A component whose `canon lower` or `canon lift`
// names a SHARED memory as its `(memory …)` option is refused by name.
//
// **The refusal is the pin's own rule** (ADR 0129). CanonicalABI.md's "`canonopt` Validation" says *"`memory` -
// this is a subtype of `(memory 0)`"*, which is unshared, and a shared memory type matches only a shared one.
// wasm-tools 1.258.0 refuses both fixtures for that rule (*"mismatch in the shared flag for memories"*). This
// comment said until #1335 that *"the pinned reference sets no rule for the option"*, and that ADR 0102 made the
// refusal a toolchain's stricter one, followed until Burroughs witnessed what the pin admits. Both were wrong:
// `definitions.py` does not model the rule, and the validation prose states it.
//
// The gate is set off explicitly, to its default, so an operator's `BURROUGHS_CM_SHARED_MEMORY=1` in the
// environment cannot turn this row into the gate-on one.
//
// **The control:** the same shared memory, used by the component without being a canon option, still loads, so
// the refusal is the option and not the memory. All three fixtures are in testdata beside their `.wat`, built
// with wasm-tools 1.258.0. wasm-tools refuses the two canon fixtures and validates the control.
//
// **Neuter, run:** with `refuseSharedCanonMemory` returning nil, both canon fixtures instantiate again, and both
// arms fail.
//
// **NEUTER, run (#1335):** with the gate check in `refuseSharedCanonMemory` replaced by `true`, so a shared canonical
// memory is always admitted, both canon arms fail with *"a shared memory used as a canon (memory …) option was
// admitted"*, and the control still passes.
func TestASharedMemoryAsACanonMemoryOptionIsRefusedByName(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "") // the default, which is off
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

// TestTheSharedCanonMemoryGateIsAnOptIn reads `gate:cm-shared-memory`'s predicate (ADR 0129): it is on for
// `BURROUGHS_CM_SHARED_MEMORY=1` and off for everything else, unset included, because the gate is default-off and
// its extension departs from the pin.
func TestTheSharedCanonMemoryGateIsAnOptIn(t *testing.T) {
	for _, tc := range []struct {
		val string
		on  bool
	}{{"", false}, {"0", false}, {"true", false}, {"yes", false}, {"1", true}} {
		t.Setenv(cmSharedMemoryGateEnv, tc.val)
		if got := cmSharedMemoryEnabled(); got != tc.on {
			t.Errorf("%s=%q: the gate reads %v, want %v", cmSharedMemoryGateEnv, tc.val, got, tc.on)
		}
	}
}

// TestASharedCanonMemoryIsAdmittedAndRunsWithTheGateOn is `gate:cm-shared-memory`'s gate-on row over the gate-off
// row's own fixtures (#1335, ADR 0129): with the gate on, both load, and both run.
//
//   - **The lower fixture** runs `wasi:cli/run`, whose `get-arguments` is lowered into the shared memory through
//     the guest's `realloc`, so the host writes through it.
//   - **The lift fixture** calls `s`, whose `string` result is lifted out of the shared memory. Its guest returns
//     a pointer to eight zero bytes, so the string is empty.
//
// **NEUTER, run (#1335):** with the admission in `refuseSharedCanonMemory` removed, so a shared canonical memory is
// always refused, both arms fail at instantiate with the gate-off refusal (*"the Canonical ABI requires an
// unshared canonical memory"*).
func TestASharedCanonMemoryIsAdmittedAndRunsWithTheGateOn(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	t.Run("shmem-canon-lower-synth.wasm", func(t *testing.T) {
		h := NewHost(io.Discard, io.Discard, nil)
		h.Args = []string{"prog", "an argument"}
		in := instantiateFixtureWithHost(t, "shmem-canon-lower-synth.wasm", h)
		defer in.Close()
		if err := in.Call("wasi:cli/run@0.3.0"); err != nil {
			t.Fatalf("admitted, but its run did not run: %v", err)
		}
	})
	t.Run("shmem-canon-lift-synth.wasm", func(t *testing.T) {
		in := instantiateFixtureWithHost(t, "shmem-canon-lift-synth.wasm", NewHost(io.Discard, io.Discard, nil))
		defer in.Close()
		res, err := in.CallValues("s")
		if err != nil {
			t.Fatalf("admitted, but s did not run: %v", err)
		}
		if len(res) != 1 {
			t.Fatalf("s returned %d value(s), want 1", len(res))
		}
		if s, ok := res[0].Str(); !ok || s != "" {
			t.Errorf("s returned %v (a string: %v), want the empty string its eight zero bytes describe", res[0], ok)
		}
	})
}

// TestASharedCanonMemoryRoundTripIsByteIdenticalToItsUnsharedTwin is `gate:cm-shared-memory`'s witness with real
// use (#1335, ADR 0129). `shmem-canon-roundtrip.wasm` moves strings through SHARED canonical memories in every
// direction the Canonical ABI has: the host lowers `get-arguments`' list<string> into the parent through its
// realloc; each argument crosses a sync call into a nested child, lowered into the child's memory and lifted back;
// the results go out through `blocking-write-and-flush`'s list<u8>; and an embedder's call to the re-exported
// `shout` lowers a string into the child and lifts one out.
//
// **Its oracle is its unshared twin's wasmtime reading, byte for byte.** The shared fixture has no external
// reader: it is invalid at the pin, and wasm-tools 1.258.0 and wasmtime 49.0.3 both refuse it (*"mismatch in the
// shared flag for memories"*). `shmem-canon-roundtrip-unshared.wasm` is the same component with the word `shared`
// removed from its three memory declarations, which is valid, and wasmtime 49.0.3 read it 10 of 10
// (`testdata/shmem-canon-roundtrip.reading`). So the claim is exactly that sharing the canonical memory changes
// nothing a guest or an embedder can see, under one agent, which is all E1 claims.
//
// **The twin is read here too, in both gate states**, so a disagreement between the shared run and the reading
// cannot be a disagreement between Burroughs and wasmtime about the twin, and the gate is seen to touch nothing
// unshared.
//
// **NEUTER, run (#1335):** with the gate check in `refuseSharedCanonMemory` replaced by `true`, the
// gate-off shared row fails (*"a shared canonical memory was admitted with the gate off"*). With the admission
// removed, the gate-on shared row fails at instantiate with the gate-off refusal. The twin's rows pass under both.
func TestASharedCanonMemoryRoundTripIsByteIdenticalToItsUnsharedTwin(t *testing.T) {
	wantRun := readingString(t, "run, wasmtime 49.0.3, 10 of 10")
	wantShout := readingString(t, "shout, wasmtime 49.0.3, 10 of 10")
	// The invocation the reading records: `wasmtime run <twin> hello 'Wörld-42' '' abc`. The guest skips argv[0],
	// which wasmtime sets to the file's name.
	args := []string{"shmem-canon-roundtrip-unshared.wasm", "hello", "Wörld-42", "", "abc"}

	for _, tc := range []struct {
		fixture, gate string
	}{
		{"shmem-canon-roundtrip-unshared.wasm", ""},
		{"shmem-canon-roundtrip-unshared.wasm", "1"},
		{"shmem-canon-roundtrip.wasm", "1"},
	} {
		t.Run(tc.fixture+"/gate="+tc.gate, func(t *testing.T) {
			t.Setenv(cmSharedMemoryGateEnv, tc.gate)

			var out strings.Builder
			h := NewHost(&out, io.Discard, nil)
			h.Args = args
			in := instantiateFixtureWithHost(t, tc.fixture, h)
			defer in.Close()
			if err := in.Call("wasi:cli/run@0.2.3"); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := out.String(); got != wantRun {
				t.Errorf("run wrote %q, want wasmtime 49.0.3's reading of the unshared twin, %q", got, wantRun)
			}

			res, err := in.CallValues("shout", canon.Str("hello Wörld-42"))
			if err != nil {
				t.Fatalf("shout: %v", err)
			}
			if len(res) != 1 {
				t.Fatalf("shout returned %d value(s), want 1", len(res))
			}
			if got, ok := res[0].Str(); !ok || got != wantShout {
				t.Errorf("shout returned %v (a string: %v), want wasmtime 49.0.3's reading of the unshared twin, %q",
					res[0], ok, wantShout)
			}
		})
	}

	t.Run("shmem-canon-roundtrip.wasm/gate=off_is_refused_by_the_pins_rule", func(t *testing.T) {
		t.Setenv(cmSharedMemoryGateEnv, "")
		wasm, err := os.ReadFile("testdata/shmem-canon-roundtrip.wasm")
		if err != nil {
			t.Fatalf("the committed fixture is missing, so this row has no subject: %v", err)
		}
		in, err := InstantiateWithHost(wasm, NewHost(io.Discard, io.Discard, nil))
		if err == nil {
			in.Close()
			t.Fatal("a shared canonical memory was admitted with the gate off")
		}
		if !errors.Is(err, ErrUnsupportedForm) {
			t.Errorf("refused, but not as ErrUnsupportedForm: %v", err)
		}
		for _, name := range []string{
			"unshared canonical memory", "canonopt validation", cmSharedMemoryGateEnv + "=1",
			"Burroughs-only extension", "gate:cm-shared-memory",
		} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("refused, but the refusal does not name %q, so it does not say which rule refused it or "+
					"how the extension is admitted: %v", name, err)
			}
		}
	})
}

// instantiateFixtureWithHost loads a committed fixture under h, failing the test if either step fails.
func instantiateFixtureWithHost(t *testing.T, name string, h *Host) *Instantiated {
	t.Helper()
	wasm, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("the committed fixture is missing, so this row has no subject: %v", err)
	}
	in, err := InstantiateWithHost(wasm, h)
	if err != nil {
		t.Fatalf("instantiate %s: %v", name, err)
	}
	return in
}

// readingString is a reading from `testdata/shmem-canon-roundtrip.reading`, recorded there as a Go string literal
// because the oracle's output has newlines in it.
func readingString(t *testing.T, reader string) string {
	t.Helper()
	lit := readingLine(t, "testdata/shmem-canon-roundtrip.reading", reader)
	s, err := strconv.Unquote(lit)
	if err != nil {
		t.Fatalf("the reading for %q is not a Go string literal (%v): %s", reader, err, lit)
	}
	return s
}
