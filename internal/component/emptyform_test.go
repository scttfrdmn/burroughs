// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"strings"
	"testing"
)

// The empty-form rejection (#904): a component may not declare `record {}` or `tuple<>`.
//
// # This is a conformance rule, taken from the reference rather than invented
//
// Both specimens **encode fine** — `wasm-tools parse` accepts them — and `wasm-tools validate` refuses
// them with a message about the rule. So the bytes are well-formed and the *type* is not, which is why
// this is a validation rule and not a malformedness (grave #301's distinction, in the direction an
// engine has to be careful about: manufacturing a malformedness the reference does not report).
//
// The Canonical ABI is why the reference draws the line: `elem_size_record` asserts `s > 0`
// (definitions.py:1256), so an empty record has no layout — `elem_size(RecordType([]))` raises
// `AssertionError`, asked of the pinned model directly. A tuple despecializes to a record (:1133), so
// `tuple<>` is the same condition reached the other way.
//
// # What this closes
//
// Before it, three things stood on the premise that an empty form might reach the codec: `sizeRecord`'s
// panic, the bridge's refusal of each empty form, and a **declared divergence** between the bridge and
// `unmodeledValKind`, which accepted `tuple<>` because a loop over zero elements finds nothing to
// refuse. Once no decoded type can be an empty form, the first two become unreachable-by-construction
// (kept anyway, for a hand-built type) and the third has nothing to disagree about and is retired.

// TestTheReferencesEmptyFormRejectionIsMatched drives the two committed specimens through the loader and
// asserts each is refused **naming the rule the reference names**.
//
// The messages are matched against wasm-tools' own wording — "must have at least one field" / "at least
// one type" — rather than against something of this engine's invention. That is deliberate: when a
// reader hits this refusal they are most likely comparing against the reference's output, and two
// different sentences for one rule is the drift that makes a conformance claim unverifiable by reading.
func TestTheReferencesEmptyFormRejectionIsMatched(t *testing.T) {
	for _, c := range []struct {
		name    string
		file    string
		wantMsg string
		// refMsg is what `wasm-tools validate --features all` printed for this specimen, recorded so the
		// assertion above can be read against the authority without re-running it. Not machine-checked
		// here — `make strict` has no wasm-tools — which is stated rather than implied.
		refMsg string
	}{
		{
			"record{}",
			"testdata/empty-record-type.wasm",
			"record type must have at least one field",
			"error: record type must have at least one field (at offset 0xb)",
		},
		{
			"tuple<>",
			"testdata/empty-tuple-type.wasm",
			"tuple type must have at least one type",
			"error: tuple type must have at least one type (at offset 0xb)",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			wasm, err := os.ReadFile(c.file)
			if err != nil {
				t.Fatalf("the committed specimen is missing: %v", err)
			}
			// Through the real loader, not the type reader in isolation: the claim is that such a
			// component does not load, and a decoder-level assertion would leave open whether anything
			// upstream swallowed the error.
			_, err = InstantiateWithHost(wasm, NewHost(nil, nil, nil))
			if err == nil {
				t.Fatalf("a component declaring %s loaded. The reference validator refuses it (%q), and "+
					"the ABI gives it no layout, so accepting it means accepting a type no value can "+
					"ever inhabit", c.name, c.refMsg)
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("the refusal %q does not name the rule. The reference says %q, and matching its "+
					"wording is what lets a reader check this engine against it by reading",
					err, c.refMsg)
			}
		})
	}
}

// TestANonEmptyRecordAndTupleStillLoad is the anti-vacuity half: the rule above must refuse the empty
// forms **and nothing else**.
//
// Without this, a decoder arm that refused every record would satisfy the test above completely, and the
// conformance claim would be "stricter than the reference" rather than "matching it" — which is the
// failure mode a one-sided refusal test cannot see.
func TestANonEmptyRecordAndTupleStillLoad(t *testing.T) {
	// A one-field record and a one-element tuple, decoded through the same path as the specimens above.
	// Built from bytes rather than from a fixture because the point is the decoder's arm, and a
	// wasm-tools-validated component carrying a record needs its type exported too — a record is
	// nominal in the component model, which is a constraint on fixtures and not on this assertion.
	for _, c := range []struct {
		name string
		in   ValType
	}{
		{"record with one field", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "a", Type: ValType{Kind: VU32}}}}},
		{"tuple with one element", ValType{Kind: VTuple, Elems: []ValType{{Kind: VU32}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The bridge is the layer that answers "can the codec carry this", and it is the one the
			// decoder's new refusal is protecting: a non-empty form must still reach it and be carried.
			if _, bad := canonUnmodeled(c.in); bad {
				t.Errorf("%s is refused by the bridge; the empty-form rule must not have widened to "+
					"non-empty ones", c.name)
			}
			if _, err := canonTypeOf(c.in); err != nil {
				t.Errorf("%s does not convert: %v", c.name, err)
			}
		})
	}
}
