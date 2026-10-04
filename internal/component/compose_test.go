// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"testing"
)

// composedWorldImports returns the names of a composed component's OUTER-level imports, read with this
// engine's own decoder.
//
// # Why the engine's decoder and not a text scan
//
// A first measurement of this counted `(import` lines in `wasm-tools print` output at any indentation —
// and `wac` nests both components, so the **nested socket's own** import declaration is still in the text
// and is supposed to be. That count read as "the plugged import survived", which would have confirmed a
// wrong conclusion by a different route.
//
// `Load` parses the binary and `c.Imports` is the outer component's import section, so the question
// "which imports does this artefact still have" is answered by a parser over the structure rather than by
// a pattern over a rendering. *Measure with the instrument, not a regex.*
func composedWorldImports(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed composed artefact is missing (regenerate with compose.sh): %v", err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("%s: load: %v", path, err)
	}
	names := make([]string, 0, len(c.Imports))
	for i := range c.Imports {
		names = append(names, c.Imports[i].Name)
	}
	return names
}

func hasImport(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestWacSatisfiesThePluggedImportAndLeavesTheRest is the `wac` pin's witness.
//
// # What it is for
//
// A pinned tool with nothing exercising it is a pin nobody has tested. This asserts the property the
// pin exists to provide — that `wac plug` actually satisfies a dependency — **on the composed artefact's
// structure, because the exit status does not say.**
//
// # Two arms, and the negative one is why this is a test rather than a note
//
//	supplied: receipt/ plugged into a socket importing exactly its export
//	missing:  the same socket plus one import the child cannot supply
//
// **`wac plug` exits 0 for both.** So a check that read the status, or that merely ran the tool, would
// call the second one composed. The discriminator is the composed world: `missing` survives in one and
// not the other.
//
// # The positive assertion is NOT "zero imports", and that matters
//
// `receipt/` has its own host imports — `tick` and `note` — and composition correctly leaves them for the
// host to supply. A zero-import expectation would therefore **fail on a correct composition**. So the
// assertion is directional: the *plugged* import is gone, the *child's own* imports remain.
//
// # Why this does not invoke wac
//
// The committed `.wasm` files are the artefacts, as with `asynclift/`'s guests and readings: commit the
// tool's output, never depend on the tool at test time. `wac` is not in CI and does not need to be.
func TestWacSatisfiesThePluggedImportAndLeavesTheRest(t *testing.T) {
	t.Run("supplied_the_plugged_import_is_gone_and_the_childs_remain", func(t *testing.T) {
		names := composedWorldImports(t, "testdata/compose/composed-supplied.wasm")

		// The plug was satisfied: `run` is the socket's import and the child's export, so after
		// composition it must not be an import of the whole.
		if hasImport(names, "run") {
			t.Errorf("the composed world still imports `run`, which the plug was supposed to satisfy; "+
				"imports = %v", names)
		}
		// The child's own host imports survive — this is what makes the arm above a *directional* claim
		// rather than "composition removes imports". Without these, `run`'s absence would also be
		// consistent with an empty or truncated artefact.
		for _, want := range []string{"tick", "note"} {
			if !hasImport(names, want) {
				t.Errorf("the composed world does not import %q, but it is the CHILD's own host import "+
					"and composition must leave it for the host; imports = %v", want, names)
			}
		}
	})

	t.Run("missing_an_unsatisfied_import_survives_and_wac_said_nothing", func(t *testing.T) {
		names := composedWorldImports(t, "testdata/compose/composed-missing.wasm")

		// The arm that makes the check real: `wac plug` exited 0 producing this, with a dependency it
		// could not satisfy still outstanding.
		if !hasImport(names, "missing") {
			t.Errorf("the composed world does not import `missing`, but nothing supplies it — so this "+
				"fixture no longer exercises the unsatisfied case and the positive arm above is "+
				"unfalsified; imports = %v", names)
		}
		// And the socket's satisfiable import was still satisfied in the same composition, so the two
		// arms differ in exactly one import rather than in whether composition happened at all.
		if hasImport(names, "run") {
			t.Errorf("the composed world still imports `run` too, so this arm is not isolating the "+
				"unsatisfied import; imports = %v", names)
		}
	})
}
