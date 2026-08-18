// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"errors"
	"fmt"
	"testing"
)

// errStubGate is the stub engine's gate sentinel, and it exists because `isGated` is *injected*:
// this file's Engine declares its own predicate, so nothing here depends on the real decoder's
// feature-gate error and the control keeps working when that error is reworded.
var errStubGate = errors.New("stub: feature gate disabled")

// TestRegistryCarriesItsGatedNames is decision 0037's control, and it asserts the half of the
// mechanism that lives in the harness: a `register` whose module was gate-declined **records the
// name**, a `register` that succeeds **clears it**, and the recorded set **reaches the caller** on
// every later instantiation.
//
// **The engine-side half — reading a decoded module's imports — is not asserted here and cannot be**,
// because it needs a decoder that `internal/spec` deliberately does not have. Its witness is the
// board's 71 gated lines and `gatedDeclinedRegistration`'s per-file bounds; this control is the
// other side of that seam.
//
// **The population it covers is not the corpus's.** Row 3 is a re-register whose new module is
// declined, which the suite does not witness at all: the reference is last-register-wins
// (`runner.ml:314`), so the stale instance must stop being resolvable, and an import satisfied from
// it would award a pass for a program the reference replaced. A control scoped to today's vectors
// would have inherited today's blind spot and left that shape asserted only in prose.
//
// **Falsified before being trusted, three ways, each killing a different row** — a green on the
// first run is a tell, not a result:
//
//	the register arm not calling `decline`   → rows 1, 3, 5 die
//	`decline` keeping the stale binding      → row 3 alone dies (which is why `wantBound` exists)
//	`bind` not clearing the gated mark       → row 4 alone dies
//
// Row 6 survives all three, correctly: it has no decline anywhere in it, and it is the row that
// stops the table passing on a mechanism that gates everything.
func TestRegistryCarriesItsGatedNames(t *testing.T) {
	// `$dead` is declined by the stub's gate; `$live` is not. The discriminator is the source text
	// rather than call order, for sexpr_test.go's reason: a positional stub inverts silently the
	// day the spectest bootstrap moves.
	const gatedMarker = "GATE-ME"

	for _, tc := range []struct {
		name string
		src  string
		// want is the gated-name set the last instantiation in the script must be handed.
		want []string
		// wantBound is the *bound* names it must be handed, and it is here because `want` alone is
		// vacuous on row 3: a `decline` that recorded the name and left the stale instance bound
		// would satisfy every gated-name expectation in this table while still being resolvable.
		// `spectest` is present in every row — decision 0017 part 3 pre-populates it, through this
		// same stub.
		wantBound []string
		// wantGated is whether the trailing importing command must score gated. It is the
		// falsification half: a control that only ever asserts "gated" passes on an engine that
		// gates everything.
		wantGated bool
	}{{
		name: "a declined register records its name",
		src: `(module $dead (memory 1) ;; GATE-ME
		       )
		       (register "M" $dead)
		       (module (import "M" "x" (func)))`,
		want:      []string{"M"},
		wantBound: []string{"spectest"},
		wantGated: false, // the module *definition* keeps its pass on an instantiation decline (#124)
	}, {
		name: "a successful register records nothing",
		src: `(module $live (memory 1))
		       (register "M" $live)
		       (module (import "M" "x" (func)))`,
		want:      nil,
		wantBound: []string{"M", "spectest"},
		wantGated: false,
	}, {
		name: "a declined re-register unbinds the stale instance",
		src: `(module $live (memory 1))
		       (register "M" $live)
		       (module $dead (memory 1) ;; GATE-ME
		       )
		       (register "M" $dead)
		       (module (import "M" "x" (func)))`,
		want:      []string{"M"},
		wantBound: []string{"spectest"},
		wantGated: false,
	}, {
		name: "a successful re-register clears an earlier decline",
		src: `(module $dead (memory 1) ;; GATE-ME
		       )
		       (register "M" $dead)
		       (module $live (memory 1))
		       (register "M" $live)
		       (module (import "M" "x" (func)))`,
		want:      nil,
		wantBound: []string{"M", "spectest"},
		wantGated: false,
	}, {
		name: "an assert_unlinkable against a declined name is gated, not failed",
		src: `(module $dead (memory 1) ;; GATE-ME
		       )
		       (register "M" $dead)
		       (assert_unlinkable (module (import "M" "x" (func))) "unknown import")`,
		want:      []string{"M"},
		wantBound: []string{"spectest"},
		wantGated: true,
	}, {
		name: "an assert_unlinkable against a live name still scores a verdict",
		src: `(module $live (memory 1))
		       (register "M" $live)
		       (assert_unlinkable (module (import "M" "x" (func))) "unknown import")`,
		want:      nil,
		wantBound: []string{"M", "spectest"},
		wantGated: false,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse("registry_gated_test.wast", []byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			// handed is what the *last* instantiation saw, which is the trailing importing
			// command in every row above. Recorded rather than inspected, because the run loop's
			// `registry` is a local and the only honest way to read it is through the seam that
			// crosses it.
			var handed, handedBound []string
			e := Engine{
				Validate: stubValidate, IsDeclined: stubDeclined,
				IsGated: func(err error) bool { return errors.Is(err, errStubGate) },
				IsTrap:  func(error) bool { return false },
				InstantiateLinked: func(c Command, reg Registry) (Instance, Stratum, error) {
					handed, handedBound = sortedKeys(reg.Gated), sortedInstanceKeys(reg.Instances)
					if isGateMarked(c, gatedMarker) {
						return nil, StratumBinary, fmt.Errorf("declining: %w", errStubGate)
					}
					// The stub stands in for the engine-side check: a module importing from a
					// recorded name fails *as gated*. It is a stub of the adapter, and what this
					// control asserts is the input it was handed, never this line's own logic.
					for _, name := range importedModules(c) {
						if reg.Gated[name] {
							return nil, StratumExec, fmt.Errorf(
								"import from declined %q: %w", name, errStubGate)
						}
					}
					return "instance", StratumUnset, nil
				},
				Has: []Capability{CapWatReader, CapInterpreter},
			}
			// ReadText and Assemble accept everything: this control is about registry state, and a
			// front end with an opinion would make the rows' verdicts depend on the assembler.
			e.ReadText = func([]byte) error { return nil }
			e.Assemble = func([]byte) ([]byte, error) { return []byte{0}, nil }
			e.Decode = func([]byte) error { return nil }

			r := s.RunGated(e)

			if got, want := handed, tc.want; !equalStrings(got, want) {
				t.Errorf("gated names handed to the last instantiation = %v, want %v", got, want)
			}
			if got, want := handedBound, tc.wantBound; !equalStrings(got, want) {
				t.Errorf("bound names handed to the last instantiation = %v, want %v", got, want)
			}
			// The verdict half. `Gated` counts commands, and the trailing command is the only one
			// in each row that can be gated *by a name* — every other gate is the stub declining a
			// module directly, which the row's own source controls.
			lastGated := r.Gated > declinedModules(tc.src)
			if lastGated != tc.wantGated {
				t.Errorf("trailing command gated = %v, want %v (Gated=%d, Fail=%d, Pass=%d)",
					lastGated, tc.wantGated, r.Gated, r.Fail, r.Pass)
			}
		})
	}
}

// isGateMarked reports whether a command's source carries the stub's decline marker.
func isGateMarked(c Command, marker string) bool {
	return containsBytes(c.Source, marker)
}

func containsBytes(b []byte, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == sub {
			return true
		}
	}
	return false
}

// importedModules reads the module names a text module imports from, by scanning its source for
// `(import "…"`. Crude on purpose: it is a stub's input, not the board's, and a real derivation
// belongs on the side of the seam that has a decoder — see declinedImport.
func importedModules(c Command) []string {
	var out []string
	src := string(c.Source)
	for i := range len(src) {
		const key = `(import "`
		if i+len(key) > len(src) || src[i:i+len(key)] != key {
			continue
		}
		j := i + len(key)
		for k := j; k < len(src); k++ {
			if src[k] == '"' {
				out = append(out, src[j:k])
				break
			}
		}
	}
	return out
}

// declinedModules counts the rows' own directly-declined module commands, which is the baseline the
// trailing command's gate is measured against.
func declinedModules(src string) int {
	n := 0
	for i := 0; i+7 <= len(src); i++ {
		if src[i:i+7] == "GATE-ME" {
			n++
		}
	}
	return n
}

func sortedInstanceKeys(m map[string]Instance) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
