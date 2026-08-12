// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package binary

import (
	"errors"
	"strings"
	"testing"
)

// brOnCastImage assembles a one-function module whose body is a void block containing a single
// `br_on_cast`/`br_on_cast_fail` with the given flags byte, label, and two heaptypes.
//
// Assembled rather than encoded from wat for `decodesStandalone`'s reason one package over: the
// subject is what *this* decoder does with a flags byte, and routing through the text front end
// would ask a sibling whether it can produce the byte at all — which for the reserved values it
// cannot, since a malformed encoding has no source spelling.
func brOnCastImage(sub, flags, label, ht1, ht2 byte) []byte {
	body := []byte{
		0x00,       // no locals
		0x02, 0x40, // block, void blocktype
		0xFB, sub, flags, label, ht1, ht2,
		0x0B, // end (block)
		0x0B, // end (body)
	}
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
		0x03, 0x02, 0x01, 0x00, // function: one func, type 0
	}
	code := append([]byte{0x01, byte(len(body))}, body...)
	return append(img, append([]byte{0x0a, byte(len(code))}, code...)...)
}

// TestBrOnCastFlagsAreMaskedNotCompared is the bidirectional control the flags requirement needs,
// and the second direction is the one that matters.
//
// `decode.ml:642` is `require (flags land 0xfc = 0) s (pos + 2) "malformed br_on_cast flags"`. The
// reject direction — a reserved bit set is malformed — is what the corpus can see, and it is the
// cheap half. The **accept** direction is not in the corpus in a form that would catch the obvious
// wrong implementation: `flags != 0` rejects every reserved bit correctly *and* rejects
// `br_on_cast $l anyref (ref i31)`, which encodes `0x01` because rt1 is nullable and rt2 is not.
// Since bits 0 and 1 are the two null bits, a legal module can carry any of the four low values, and
// a check that compared against zero would reject three of them — the accept-direction failure §9
// G-3 calls worse than missing an invalid module, and one no rejection corpus can score.
//
// So all four legal values are asserted accepted and the six low reserved patterns asserted
// malformed, for both sub-opcodes. Both directions in one function, on the two-fields-disagree
// principle: a mask written with the wrong polarity fails the two halves in opposite directions,
// where either half alone reads as a plausible reading of the grammar.
func TestBrOnCastFlagsAreMaskedNotCompared(t *testing.T) {
	d := &Decoder{Features: Features{GC: true}}
	for _, sub := range []byte{0x18, 0x19} {
		// The accept direction: every value of the two null bits.
		for flags := byte(0x00); flags <= 0x03; flags++ {
			img := brOnCastImage(sub, flags, 0x00, HeapAny, HeapI31)
			if _, err := d.DecodeModule(img); err != nil {
				t.Errorf("fb %#02x flags %#02x rejected with %v — bits 0 and 1 are rt1's and "+
					"rt2's null bits (decode.ml:644-645), so all four values are legal and this "+
					"is a check comparing against zero instead of masking", sub, flags, err)
			}
		}
		// The reject direction: one reserved bit at a time, plus the two the suite's own
		// `assert_malformed` shapes would reach first.
		for _, flags := range []byte{0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x05, 0xff} {
			img := brOnCastImage(sub, flags, 0x00, HeapAny, HeapI31)
			_, err := d.DecodeModule(img)
			if err == nil {
				t.Errorf("fb %#02x flags %#02x accepted — `flags land 0xfc` is non-zero, which "+
					"the reference calls malformed", sub, flags)
				continue
			}
			if !errors.Is(err, ErrMalformedBrOnCastFlags) {
				t.Errorf("fb %#02x flags %#02x refused with %v, want ErrMalformedBrOnCastFlags",
					sub, flags, err)
			}
			// The spec string is the grammar's, quoted verbatim: gates never manufacture
			// malformedness and neither does this engine invent its wording.
			if !strings.Contains(err.Error(), "malformed br_on_cast flags") {
				t.Errorf("fb %#02x flags %#02x message is %q, want the reference's text",
					sub, flags, err)
			}
		}
	}
}

// TestBrOnCastFlagsPrecedeTheLabel pins the *ordering* the flat `imms` walk could not express, which
// is the whole reason `brOnCastImms` exists.
//
// A module whose flags byte is reserved **and** whose remaining immediates are truncated must report
// the flags, not the truncation: `require` fires at `pos + 2`, before `at var s` reads anything. A
// reader that validated the byte after the sequence would report `unexpected end` here — a true
// statement about a module the reference rejects for a different reason, which is the wrong-layer
// error one step earlier than usual (the layer is right, the *order* is wrong).
//
// The discriminator is that both errors are available: truncate after the flags byte so the two
// candidate implementations give measurably different answers rather than the same one.
func TestBrOnCastFlagsPrecedeTheLabel(t *testing.T) {
	trim := func(full []byte) []byte {
		// Drop the three bytes after the flags — label and both heaptypes — and shrink the two
		// length prefixes (code section payload, then body) to match, so the truncation is a
		// *well-formed section holding a short body* rather than a section-level size mismatch.
		out := make([]byte, 0, len(full))
		out = append(out, full[:len(full)-3-2]...) // up to the flags, minus the two ENDs
		out = append(out, 0x0B, 0x0B)              // the ENDs, now immediately after the flags
		for i := range out {
			if out[i] == 0x0a && i+3 < len(out) {
				out[i+1] = full[i+1] - 3
				out[i+3] = full[i+3] - 3
				break
			}
		}
		return out
	}
	d := &Decoder{Features: Features{GC: true}}

	// **The vacuity check first, and it is what makes the assertion below mean anything.** If the
	// truncation did not truncate — a mis-shrunk length, an off-by-one in the slice — then the
	// image is a perfectly good module, the only error available is the flags one, and the test
	// passes while asserting nothing about ordering. So: the same image with *legal* flags must
	// report the truncation. Measured, not assumed: it reports `unexpected end of section or
	// function`, which is precisely the error a reader that validated the byte too late would
	// give for the reserved-flags image.
	if _, err := d.DecodeModule(trim(brOnCastImage(0x18, 0x00, 0x00, HeapAny, HeapI31))); err == nil {
		t.Fatal("the truncated image decodes cleanly with legal flags — the truncation is not a " +
			"truncation, so the ordering assertion below has only one error available to it and " +
			"cannot distinguish the two implementations it exists to distinguish")
	}

	full := brOnCastImage(0x18, 0x04, 0x00, HeapAny, HeapI31)
	_, err := d.DecodeModule(trim(full))
	if err == nil {
		t.Fatal("accepted a module whose flags byte sets a reserved bit")
	}
	if !errors.Is(err, ErrMalformedBrOnCastFlags) {
		t.Errorf("got %v, want ErrMalformedBrOnCastFlags — the requirement is checked at "+
			"`pos + 2`, before the label is read (decode.ml:642), so a truncated tail must not "+
			"be what gets reported", err)
	}
}

// TestBrOnCastRetainsBothTypesWithFlagsNullability is the decoder half of the pair's retention: two
// types, in the reference's order, each with its null bit from its own flags bit.
//
// The four flag combinations are enumerated rather than sampled, because the defect this catches is
// a **swap** — taking bit 0 for rt2 and bit 1 for rt1 — which `0x00` and `0x03` cannot see: the two
// symmetric values agree under either reading, and only `0x01` and `0x02` discriminate. A control
// built from the two obvious cases would pass on a swapped implementation, which is the same shape
// as a mutation that only moves names.
func TestBrOnCastRetainsBothTypesWithFlagsNullability(t *testing.T) {
	d := &Decoder{Features: Features{GC: true}}
	for _, tc := range []struct {
		flags    byte
		rt1, rt2 string
	}{
		{0x00, "(ref any)", "(ref i31)"},
		{0x01, "(ref null any)", "(ref i31)"},
		{0x02, "(ref any)", "(ref null i31)"},
		{0x03, "(ref null any)", "(ref null i31)"},
	} {
		img := brOnCastImage(0x18, tc.flags, 0x00, HeapAny, HeapI31)
		m, err := d.DecodeModule(img)
		if err != nil {
			t.Fatalf("flags %#02x: decode: %v", tc.flags, err)
		}
		fn := &m.Funcs[0]
		var v []ValType
		for i := range fn.Body {
			if fn.Body[i].Prefix == 0xFB && fn.Body[i].Op == 0x18 {
				got, ok := fn.CastTypes(i)
				if !ok {
					t.Fatalf("flags %#02x: no cast types retained at the br_on_cast", tc.flags)
				}
				v = got
				break
			}
		}
		if len(v) != 2 {
			t.Fatalf("flags %#02x: retained %d types, want 2", tc.flags, len(v))
		}
		if got := v[0].String(); got != tc.rt1 {
			t.Errorf("flags %#02x: rt1 is %s, want %s — bit 0 is rt1's null bit, and a reading "+
				"that took bit 1 agrees on 0x00 and 0x03 and only differs here",
				tc.flags, got, tc.rt1)
		}
		if got := v[1].String(); got != tc.rt2 {
			t.Errorf("flags %#02x: rt2 is %s, want %s", tc.flags, got, tc.rt2)
		}
	}
}

// TestBrOnCastRowMatchesTheHandWrittenSequence is the tripwire for the second authority
// `brOnCastImms` introduces: a hand-written reader beside a generated table is two places knowing one
// grammar, and the generated one is the authority.
//
// The runtime guard in `brOnCastImms` already refuses a mismatched row — but a runtime guard fires
// only when a module is decoded, and reports a grammar change as an internal error on a *module*,
// which is the wrong subject. This asserts the agreement directly, and the floor is the point: both
// rows must exist, so a regenerated table that lost them is a failure here rather than a decoder that
// silently stops answering two opcodes.
func TestBrOnCastRowMatchesTheHandWrittenSequence(t *testing.T) {
	region, ok := prefixRegions[0xFB]
	if !ok {
		t.Fatal("no 0xfb region in the generated table — every assertion below would be vacuous")
	}
	for _, sub := range []uint32{brOnCast, brOnCastFail} {
		info, ok := region[sub]
		if !ok {
			t.Errorf("fb %#02x is absent from the generated table; brOnCastImms hand-codes a "+
				"sequence for it and would never be reached", sub)
			continue
		}
		if len(info.imms) != len(brOnCastImmSeq) {
			t.Errorf("fb %#02x row is %v, want %v", sub, info.imms, brOnCastImmSeq)
			continue
		}
		for i := range info.imms {
			if info.imms[i] != brOnCastImmSeq[i] {
				t.Errorf("fb %#02x row is %v, want %v (differs at %d)",
					sub, info.imms, brOnCastImmSeq, i)
				break
			}
		}
	}
}
