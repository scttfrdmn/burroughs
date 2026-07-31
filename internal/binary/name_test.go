package binary

import (
	"errors"
	"testing"
)

// Tests for the name grammar (#26): a name's bytes must be well-formed UTF-8.
//
// These vectors are `synthetic` on purpose and the reason matters. The suite's
// utf8-*.wast files enumerate 176 specific violations each, and the whole point of
// the implementation is that it does *not* consult that enumeration — it applies
// the encoding's rule (utf8.Valid) and the suite agrees on 528/528 as corroboration.
// So the unit tests here are organised by violation *class*, one representative per
// class, which is the shape of the claim being made. Copying suite bytes in would
// test the same thing the board already tests, and worse: it would suggest the rule
// came from the vectors.

// mod builds a minimal module carrying `nameBytes` in the given name position.
func modWithCustomName(nameBytes []byte) []byte {
	payload := append([]byte{byte(len(nameBytes))}, nameBytes...)
	m := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	m = append(m, 0x00, byte(len(payload)))
	return append(m, payload...)
}

// modWithExportName puts the bytes in an export name — a different name position
// reached by a different grammar, so it proves the predicate is on the name
// production and not bolted to one call site.
func modWithExportName(nameBytes []byte) []byte {
	m := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	// type section: one functype () -> ()
	m = append(m, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00)
	// function section: one function of type 0
	m = append(m, 0x03, 0x02, 0x01, 0x00)
	// export section: one export, the given name, func kind, index 0
	body := append([]byte{0x01, byte(len(nameBytes))}, nameBytes...)
	body = append(body, 0x00, 0x00)
	m = append(m, 0x07, byte(len(body)))
	m = append(m, body...)
	// code section: one empty body
	m = append(m, 0x0A, 0x04, 0x01, 0x02, 0x00, 0x0B)
	return m
}

// TestNameMustBeUTF8 covers the violation classes, not the suite's vector list.
func TestNameMustBeUTF8(t *testing.T) {
	// synthetic: organised by UTF-8 violation class rather than copied from
	// utf8-*.wast, because the claim under test is the general rule.
	cases := []struct {
		class string
		bytes []byte
	}{
		{"stray continuation byte", []byte{0x80}},
		{"truncated 2-byte sequence", []byte{0xC2}},
		{"truncated 3-byte sequence", []byte{0xE0, 0xA0}},
		{"truncated 4-byte sequence", []byte{0xF0, 0x90, 0x80}},
		{"overlong 2-byte encoding of U+0000", []byte{0xC0, 0x80}},
		{"overlong 3-byte encoding of U+007F", []byte{0xE0, 0x80, 0xBF}},
		{"overlong 4-byte encoding of U+FFFF", []byte{0xF0, 0x8F, 0xBF, 0xBF}},
		{"unpaired high surrogate U+D800", []byte{0xED, 0xA0, 0x80}},
		{"unpaired low surrogate U+DFFF", []byte{0xED, 0xBF, 0xBF}},
		{"code point above U+10FFFF", []byte{0xF4, 0x90, 0x80, 0x80}},
		{"5-byte sequence (never legal)", []byte{0xF8, 0x88, 0x80, 0x80, 0x80}},
		{"6-byte sequence (never legal)", []byte{0xFC, 0x84, 0x80, 0x80, 0x80, 0x80}},
		{"0xFE is not a lead byte", []byte{0xFE}},
		{"0xFF is not a lead byte", []byte{0xFF}},
		{"valid prefix then invalid tail", []byte{'o', 'k', 0xC2, 0x41}},
	}

	positions := []struct {
		where string
		build func([]byte) []byte
	}{
		{"custom section name", modWithCustomName},
		{"export name", modWithExportName},
	}

	for _, pos := range positions {
		for _, tc := range cases {
			_, err := DecodeModule(pos.build(tc.bytes))
			if !errors.Is(err, ErrMalformedUTF8) {
				t.Errorf("%s, %s (% x): got %v, want ErrMalformedUTF8",
					pos.where, tc.class, tc.bytes, err)
			}
		}
	}
}

// TestValidNamesAccepted is the half that stops the check from being "reject
// everything", which would pass every vector in all four utf8-*.wast files while
// making the decoder reject valid modules — the overfitting failure in its purest
// form, and the direction the suite cannot catch because it has no valid-name
// vectors of its own. A decoder that rejects valid modules is worse than one that
// misses an invalid one (CLAUDE.md).
func TestValidNamesAccepted(t *testing.T) {
	// synthetic: valid UTF-8 by construction, spanning all four sequence lengths
	// and the boundaries the invalid cases above sit just outside of.
	cases := []struct {
		class string
		bytes []byte
	}{
		{"empty name", []byte{}},
		{"ASCII", []byte("spectest")},
		{"NUL is a valid scalar value", []byte{0x00}},
		{"2-byte: U+0080, the boundary", []byte{0xC2, 0x80}},
		{"2-byte: U+07FF, the boundary", []byte{0xDF, 0xBF}},
		{"3-byte: U+0800, the boundary", []byte{0xE0, 0xA0, 0x80}},
		{"3-byte: U+D7FF, just below the surrogates", []byte{0xED, 0x9F, 0xBF}},
		{"3-byte: U+E000, just above the surrogates", []byte{0xEE, 0x80, 0x80}},
		{"3-byte: U+FFFF, the boundary", []byte{0xEF, 0xBF, 0xBF}},
		{"4-byte: U+10000, the boundary", []byte{0xF0, 0x90, 0x80, 0x80}},
		{"4-byte: U+10FFFF, the last scalar value", []byte{0xF4, 0x8F, 0xBF, 0xBF}},
		{"mixed widths", []byte("aé中\U0001F600")},
	}

	positions := []struct {
		where string
		build func([]byte) []byte
	}{
		{"custom section name", modWithCustomName},
		{"export name", modWithExportName},
	}

	for _, pos := range positions {
		for _, tc := range cases {
			if _, err := DecodeModule(pos.build(tc.bytes)); err != nil {
				t.Errorf("%s, %s (% x): got %v, want accept", pos.where, tc.class, tc.bytes, err)
			}
		}
	}
}

// TestByteVecIsNotAName is why the predicate lives on name() rather than on
// byteVec(). A data segment's contents are `vec(byte)` with no encoding
// constraint, so applying the UTF-8 rule to every byteVec would reject modules the
// spec accepts — and the suite would not notice, because none of its data segments
// happen to carry invalid UTF-8. That is the overfitting rule pointed at a
// *refactor*: the cheap generalisation passes every vector and is wrong about the
// grammar.
//
// GRAVE (#32): this tests the reader seam directly rather than a module carrying a
// data segment, and the difference is not stylistic. The first version *was* a
// module with a `\ff\fe\80` data segment; it passed, and a probe that pushed the
// UTF-8 check down into byteVec — the exact defect this test names — left it green.
// The data section payload is not descended into yet (#25), so no byteVec was ever
// reached and the assertion could not have failed.
//
// The lesson: a green that survives the bug it names is a control in name only, and
// on the board it is indistinguishable from one passing for the right reason. The
// fix is not a better fixture; it is testing at the level where the claim is
// checkable today. Verified by re-running the probe, which now fails.
func TestByteVecIsNotAName(t *testing.T) {
	// synthetic: bytes no UTF-8 decoder would accept as text, length-prefixed. The
	// spec places no encoding requirement on a data segment's contents.
	raw := []byte{0x03, 0xFF, 0xFE, 0x80}
	r := &reader{b: raw}

	got, err := r.byteVec()
	if err != nil {
		t.Fatalf("byteVec on % x: got %v, want the bytes back — contents are vec(byte), not name", raw, err)
	}
	if len(got) != 3 {
		t.Errorf("byteVec returned %d bytes, want 3", len(got))
	}

	// And the same bytes through name() must be rejected, which is what makes the
	// pair a distinction rather than two unrelated facts.
	r2 := &reader{b: raw}
	if err := r2.name(); !errors.Is(err, ErrMalformedUTF8) {
		t.Errorf("name on % x: got %v, want ErrMalformedUTF8", raw, err)
	}
}
