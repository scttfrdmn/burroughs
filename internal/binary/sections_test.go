package binary

import (
	"errors"
	"testing"
)

// Tests for the section payload grammars (#5).
//
// The vectors here are cited to binary.wast and checked by TestFixtureProvenance
// (internal/spec), same rule as binary_test.go: a citation nobody verifies is a
// claim, not a citation.

// TestSectionSizeBothSigns is the test Scott's shaping note asked for, and the
// reason it is its own test rather than a row in a table.
//
// The two spec strings for a size disagreement are the two *signs* of one
// comparison: the grammar consuming more than declared, and less. A sign error in
// that comparison swaps the two messages while keeping the pass count
// superficially plausible — both directions are malformed either way, so the
// suite's totals barely move and the *texts* go wrong silently. Pinning each
// direction independently, with an explicit note of which sign it is, is what
// makes that failure loud.
//
// GRAVE (#34): this test was named for both signs and pinned one of them
// twice. Its first case was labelled "grammar consumed MORE than declared" while its
// own prose said "3 bytes are left over" — and the decoder reported `declared 7,
// grammar consumed 4`, the *short* sign. Its second case is face 1, a different
// mechanism entirely. So the grammar-long direction, the one the test exists for,
// had no assertion at all; the third case that was meant to carry it could not,
// because the global grammar did not exist yet, and its `t.Log` deferral hid that
// the *sign* was missing rather than just that one vector was.
//
// The lesson is the coverage cousin of "a green that survives the bug it names":
// **a test named for a partition must be checked against the partition, not against
// its own case labels.** Two cases whose labels say "more" and "less" can both
// produce "less", and nothing on the board says so — the count is right and the
// coverage is not. Verified by printing the decoder's actual message for each
// vector rather than trusting the label, which is what turned this up.
func TestSectionSizeBothSigns(t *testing.T) {
	// Face 3, grammar consumed LESS than declared: a type section declaring 7 bytes
	// with a count of 1, so the grammar stops after 4 and 3 bytes are left over.
	//
	// The assertion checks the *message*, not just the identity. Both signs are the
	// same error value, so `errors.Is` alone cannot tell them apart — which is how
	// this test came to pin one sign twice while reading as though it covered both.
	grammarShort := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x07, 0x01, 0x60, 0x00, 0x00, 0x60, 0x00, 0x00,
	} // binary.wast:469  1 type declared, 2 given
	_, err := DecodeModule(grammarShort)
	if !errors.Is(err, ErrSectionSizeMismatch) {
		t.Errorf("grammar-short-of-declared: got %v, want ErrSectionSizeMismatch", err)
	} else if !contains(err.Error(), "declared 7 bytes, grammar consumed 4") {
		t.Errorf("grammar-short-of-declared: message %q does not report declared 7 / consumed 4 — the sign is not being distinguished", err)
	}

	// The opposite sign: a type section declaring 7 bytes with a count of 2, so
	// the grammar wants 7 bytes of functypes but only 3 are inside the declared
	// extent. Here the grammar runs *past* the declared end.
	//
	// This one is ErrSectionOverrun rather than ErrSectionSizeMismatch, and that is
	// the suite's call, not a shortcut: the declared 7 bytes do not exist in the
	// image at all, so face 1 fires before any grammar runs. Face 1 and face 3 are
	// distinguishable only by whether the image is long enough, which is exactly
	// what these two vectors differ in.
	declaredPastImage := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x07, 0x02, 0x60, 0x00, 0x00,
	} // binary.wast:458  2 types declared, 1 given
	if _, err = DecodeModule(declaredPastImage); !errors.Is(err, ErrSectionOverrun) {
		t.Errorf("declared-past-image: got %v, want ErrSectionOverrun", err)
	}

	// binary.wast:714, which #25 named as this test's third case and which turns out
	// to be the *same* sign as the first: 1 global declared with 2 given, so the
	// grammar stops after one global (6 bytes) inside a declared 11. It asserts now
	// that the global grammar exists — but it does not add a sign, which is the
	// grave above.
	oneGlobalDeclaredTwoGiven := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x06, 0x0B, 0x01, 0x7F, 0x00, 0x41, 0x00, 0x0B, 0x7F, 0x00, 0x41, 0x00, 0x0B,
	} // binary.wast:714  1 global declared, 2 given
	_, err = DecodeModule(oneGlobalDeclaredTwoGiven)
	if !errors.Is(err, ErrSectionSizeMismatch) {
		t.Errorf("binary.wast:714: got %v, want ErrSectionSizeMismatch", err)
	} else if !contains(err.Error(), "declared 11 bytes, grammar consumed 6") {
		t.Errorf("binary.wast:714: message %q does not report declared 11 / consumed 6", err)
	}

	// Face 3 in the grammar-LONG direction — the sign this test was named for and
	// never had. A global section declaring 3 bytes when one global needs 5, so the
	// grammar reads past the declared end into bytes the image does supply (which is
	// what distinguishes this from face 1: the declared extent is *inside* the image,
	// the grammar simply disagrees with it).
	//
	// Synthetic, and it has to be: the suite's size-mismatch vectors are all the
	// short sign, so this direction has no upstream vector. That is precisely the
	// argument for asserting it here — a sign the suite never exercises is a sign a
	// pass count cannot defend, and swapping the comparison would still leave the
	// board green.
	grammarLong := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x06, 0x03, 0x01, 0x7F, 0x00, // global section: declares 3, count + 2 bytes
		0x41, 0x00, 0x0B, // the rest of global 0, past the declared extent
	} // synthetic: the suite has no grammar-long size-mismatch vector
	_, err = DecodeModule(grammarLong)
	if !errors.Is(err, ErrSectionSizeMismatch) {
		t.Errorf("grammar-long-of-declared: got %v, want ErrSectionSizeMismatch", err)
	} else if !contains(err.Error(), "declared 3 bytes, grammar consumed 6") {
		t.Errorf("grammar-long-of-declared: message %q does not report declared 3 / consumed 6 — a swapped comparison would read the same as the short sign", err)
	}
}

// TestPayloadEndVsTruncated pins the boundary between the two end-of-input
// strings, which is where a plausible-looking simplification goes wrong.
//
// "unexpected end" is a *substring* of "unexpected end of section or function",
// and the harness matches by substring. So the long form satisfies both families
// of vector and the short form satisfies only one. That asymmetry means the cheap
// mistake — reporting the preamble's ErrTruncated everywhere — passes the three
// custom.wast vectors while failing ten others, and the expensive mistake —
// reporting ErrPayloadEnd in the preamble — passes everything in this suite while
// being wrong about the preamble. The suite cannot catch the second one, so this
// test does.
func TestPayloadEndVsTruncated(t *testing.T) {
	// Preamble-level: the short form, and it must not be the long one.
	for _, in := range [][]byte{
		{},                             // binary.wast:6
		{0x01},                         // binary.wast:7
		{0x00, 0x61, 0x73},             // binary.wast:8  ("\00as")
		{0x00, 0x61, 0x73, 0x6D},       // binary.wast:37
		{0x00, 0x61, 0x73, 0x6D, 0x01}, // binary.wast:38
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00}, // binary.wast:39
	} {
		_, err := DecodeModule(in)
		if !errors.Is(err, ErrTruncated) {
			t.Errorf("DecodeModule(% x): got %v, want ErrTruncated", in, err)
		}
		if errors.Is(err, ErrPayloadEnd) {
			t.Errorf("DecodeModule(% x): reported a section-level end for a preamble truncation", in)
		}
	}

	// Payload-level: the long form. A table section declaring one entry and
	// supplying none.
	payload := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x04, 0x01, 0x01,
	} // binary.wast:603  1 table declared, 0 given
	if _, err := DecodeModule(payload); !errors.Is(err, ErrPayloadEnd) {
		t.Errorf("DecodeModule(% x): got %v, want ErrPayloadEnd", payload, err)
	}

	// The containment that makes the above work, asserted rather than assumed. If
	// upstream ever reworded either string so that one stopped containing the
	// other, the substring harness would silently stop scoring one of the two
	// families and this is what would say so.
	short, long := ErrTruncated.Error(), ErrPayloadEnd.Error()
	if len(long) <= len(short) || long[:len(short)] != short {
		t.Errorf("ErrPayloadEnd (%q) must begin with ErrTruncated's text (%q): the harness matches by substring, and ten vectors depend on the longer form satisfying the shorter", long, short)
	}
}

// TestCustomSectionNeedsItsBoundary is the one grammar that must respect the
// declared extent, and custom.wast:76 is why.
//
// A zero-length custom section is `\00\00`: id, then size 0. The name lives
// inside that extent and there is none, so it is malformed. A reader that ignored
// the boundary would take the *next* byte as the name length — and in the
// two-section vector, that byte is a well-formed section id, so the module would
// decode cleanly and be accepted. The suite says "unexpected end".
func TestCustomSectionNeedsItsBoundary(t *testing.T) {
	for _, in := range [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x00},                                                 // custom.wast:60  id with no size
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00},                                           // custom.wast:68  size 0, no name
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x05, 0x01, 0x00, 0x07, 0x00, 0x00}, // custom.wast:76  empty custom followed by real sections
	} {
		if _, err := DecodeModule(in); err == nil {
			t.Errorf("DecodeModule(% x): accepted; a custom section's name must fit inside its declared size", in)
		}
	}

	// The accept path, which a too-strict boundary check would break: a custom
	// section whose name exactly fills the extent, and one with a payload after it.
	for name, in := range map[string][]byte{
		// synthetic: name fills the payload exactly, no trailing bytes.
		"name fills extent": {
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x00, 0x02, 0x01, 0x61, // custom, size 2: name len 1, "a"
		},
		// synthetic: opaque bytes after the name are the normal case.
		"name then payload": {
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x00, 0x05, 0x01, 0x61, 0xDE, 0xAD, 0xBE, // name "a", 3 payload bytes
		},
		// synthetic: a zero-length *name* is legal; a zero-length section is not.
		"empty name, empty payload": {
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x00, 0x01, 0x00,
		},
	} {
		if _, err := DecodeModule(in); err != nil {
			t.Errorf("%s: DecodeModule(% x) = %v, want accept", name, in, err)
		}
	}
}

// TestLimitsFlagsIsAByteNotALEB pins the three vectors that encode a *valid*
// flags value as a multi-byte LEB and expect rejection.
//
// This is the same shape as the section-id rule (an id is one byte, so `\80\01`
// is malformed rather than 128) and it is the kind of thing a decoder gets wrong
// by being helpful: reading the field with u32 accepts all three, because
// `\81\00` really does encode 1. The field is a byte. The redundant encoding is
// the malformedness.
func TestLimitsFlagsIsAByteNotALEB(t *testing.T) {
	for _, in := range [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x04, 0x06, 0x01, 0x70, 0x81, 0x00, 0x00, 0x00}, // binary.wast:632  table flags as LEB
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x05, 0x05, 0x01, 0x81, 0x00, 0x00, 0x00},       // binary.wast:677  memory flags as LEB
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x05, 0x05, 0x01, 0x81, 0x01, 0x00, 0x00},       // binary.wast:686  memory flags as LEB, high bits
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrMalformedLimits) {
			t.Errorf("DecodeModule(% x): got %v, want ErrMalformedLimits", in, err)
		}
	}

	// The plain single-byte forms still work, both with and without a maximum.
	for name, in := range map[string][]byte{
		// The accept path for each flags encoding. All synthetic: the suite's
		// limits vectors are assert_malformed forms, so they cannot cover accept.
		"memory, no max":   {0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01},       // synthetic
		"memory, with max": {0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x05, 0x04, 0x01, 0x01, 0x01, 0x02}, // synthetic
		"table, no max":    {0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x04, 0x04, 0x01, 0x70, 0x00, 0x01}, // synthetic
	} {
		if _, err := DecodeModule(in); err != nil {
			t.Errorf("%s: DecodeModule(% x) = %v, want accept", name, in, err)
		}
	}
}

// TestLEBWidthIsPerField is the bidirectional control the suite hands over for free:
// the *same five bytes* are malformed in one field and legal in another, because the
// fields have different widths. Grave #36 — a decoder with one width for every
// integer cannot pass both halves, and it fails them in opposite directions, so
// neither half alone would have caught the wrong choice.
//
//	80 80 80 80 10  =  2^32
//
//	as a data-segment memory index (u32)  → "integer too large"   binary-leb128.wast:565
//	as a limits minimum         (u64)     → accepted, value 2^32   (derived, see below)
//
// The accept half is not a suite vector and cannot be: binary-leb128.wast only asserts
// malformedness, so nothing there says a wide-but-legal limits field is *fine*. It is
// derived instead from the reject half's neighbours — :529's ten-byte min is "integer
// too large" (ten bytes is legal width for a u64, so the fault is the unused payload
// bits) while :217's eleven-byte min is "integer representation too long" (eleven
// overruns even a u64). Those two bracket the width at exactly 64, and this test's
// accept row is what that bracket implies, asserted directly.
//
// It also pins the layering consequence on purpose: a memory32 whose minimum is 2^32
// pages *decodes*. It is the validator's to reject ("memory size must be at most 65536
// pages"), and catching it here by reading the field narrowly would be the decoder
// borrowing the validator's job and getting the malformed string wrong to do it.
func TestLEBWidthIsPerField(t *testing.T) {
	// The one hand-typed vector, cited as a fragment so TestFixtureProvenance checks
	// the transcription. Both modules below are assembled *around* it, which is the
	// point: the bytes under test are literally identical, and only the field they
	// land in differs.
	twoTo32 := []byte{0x80, 0x80, 0x80, 0x80, 0x10} // binary-leb128.wast:565

	// synthetic scaffolding around the cited fragment: a memory section so the data
	// segment is well-formed, then the fragment in the memory-index position.
	rejected := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x00, // memory: no max, min 0
		0x0B, 0x0A, 0x01, // data section, 10 bytes, 1 segment
	}
	rejected = append(append(rejected, twoTo32...), 0x41, 0x00, 0x0B, 0x00) // (i32.const 0), contents ""
	if _, err := DecodeModule(rejected); !errors.Is(err, ErrLEBOverflow) {
		t.Errorf("memory index as %x: got %v, want ErrLEBOverflow — an index is a u32, so 2^32 does not fit", twoTo32, err)
	}

	// The same fragment as a limits minimum, where the field is 64 bits wide and the
	// value is representable.
	//
	// Provenance: derived from binary-leb128.wast:529,221 — not cited, because the
	// suite cannot state this. Those two lines are limits minima of ten and eleven
	// bytes, wanting "integer too large" and "integer representation too long"
	// respectively; the *only* width for which those two verdicts both hold is 64, so
	// a five-byte 2^32 in that field is legal. That is the inference, and it is what
	// `derived` means: entailment from checked facts. The premises are machine-checked
	// by TestDerivedFixturesStateResolvablePremises; the inference is reviewed by eyes.
	accepted := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x07, 0x01, 0x00, // memory section, 7 bytes, 1 memory, no max
	}
	accepted = append(accepted, twoTo32...)
	if _, err := DecodeModule(accepted); err != nil {
		t.Errorf("limits min as %x: got %v, want accept — limits are u64 fields, and whether 2^32 pages is *too many* is the validator's question", twoTo32, err)
	}
}

// TestFuncTypeFormIsASignedLEB pins the functype tag as an s7, which is the
// inverse of TestLimitsFlagsIsAByteNotALEB: there the field really is a byte, so a
// multi-byte encoding of a legal value is malformed *limits*; here the field is a
// signed LEB of width 7, so a multi-byte encoding of the legal value is "integer
// representation too long" and not "malformed function type" at all.
//
// binary-leb128.wast:1067 is the vector that settles it (its tag fragment is on
// :1073) — `\e0\7f`, which the suite itself annotates as "-0x20 in signed LEB128
// encoding" and expects to fail as too long. 0x60 *is* -32 read as an s7 (the spec's type constructors live in negative
// s7 space: 0x5e array is -34, 0x5f struct is -33), so a decoder reading the tag as
// a plain byte gets the right answer for well-formed input and the wrong error
// string for an overlong encoding of it. Grave #36.
//
// The error-message assertion is not decoration. The message must name the byte the
// image actually held, and the first version of that reconstruction did not: it or'd
// a high bit in for every negative form and reported 0x5e as 0xde — an error about
// the module lying about the module, which is the wrong-layer tell in miniature.
// Nothing in the suite can catch that *for this vector*, since its expected string
// is the bare sentinel and the harness reads no further than the expected string
// does. Which is a property of the vector, not of the harness (#38): where a spec
// string embeds data — `illegal opcode ff` — the rendered value is oracle-covered.
// This assertion is what covers the case where it is not.
func TestFuncTypeFormIsASignedLEB(t *testing.T) {
	tag := []byte{0xE0, 0x7F} // binary-leb128.wast:1073 — -0x20 in two bytes

	// synthetic scaffolding around the cited fragment: a type section holding one
	// entry, whose form tag is the overlong encoding.
	overlong := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, // type section, 5 bytes, 1 type
	}
	overlong = append(append(overlong, tag...), 0x00, 0x00) // empty params, empty results
	if _, err := DecodeModule(overlong); !errors.Is(err, ErrLEBTooLong) {
		t.Errorf("overlong functype tag: got %v, want ErrLEBTooLong — a redundant encoding of a legal tag is a LEB fault, not a functype fault", err)
	}

	// synthetic: the suite has no vector for a *wrong* single-byte tag, so the
	// accept/reject boundary at width 7 and the message's byte are pinned here. The
	// first four tags are negative as an s7, which is what makes the reconstruction
	// easy to get wrong — the buggy version differed from the input only in the sign
	// region. 0x00 is marked as the row that *cannot* discriminate that: it is
	// non-negative, so both the right and the wrong reconstruction print 0x00, and it
	// stays green under falsification. It is kept as the zero case for the sentinel
	// claim and labelled so it is not mistaken for coverage of the message claim
	// (grave #34: a partition's rows get checked against the partition).
	for _, tc := range []struct{ name, want string }{
		{"0x5e array", "0x5e"},
		{"0x5f struct", "0x5f"},
		{"0x7f i32 where a form goes", "0x7f"},
		{"0x40 the s7 minimum", "0x40"},
		{"0x00 zero — non-negative, so non-discriminating for the message byte", "0x00"},
	} {
		tag := map[string]byte{"0x5e": 0x5E, "0x5f": 0x5F, "0x7f": 0x7F, "0x40": 0x40, "0x00": 0x00}[tc.want]
		r := &reader{b: []byte{tag, 0x00, 0x00}, eof: ErrPayloadEnd}
		err := (&Decoder{}).decodeFuncType(r)
		if !errors.Is(err, ErrMalformedFuncType) {
			t.Errorf("%s: got %v, want ErrMalformedFuncType", tc.name, err)
			continue
		}
		if !contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name the byte the image held (%s) — the message must not invent bits the input never had", tc.name, err, tc.want)
		}
	}

	// The accept path, since every case above is a rejection: 0x60 is the one tag
	// that decodes to -0x20 and must still work as a plain byte in the stream.
	r := &reader{b: []byte{0x60, 0x00, 0x00}, eof: ErrPayloadEnd}
	if err := (&Decoder{}).decodeFuncType(r); err != nil {
		t.Errorf("0x60 functype: got %v, want accept", err)
	} else if r.off != 3 {
		t.Errorf("0x60 functype consumed %d bytes, want 3 — the tag is one byte on the wire even though it is read as an s7", r.off)
	}
}

// TestMalformedImportKind pins the import descriptor's kind byte.
func TestMalformedImportKind(t *testing.T) {
	for _, in := range [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x02, 0x04, 0x01, 0x00, 0x00, 0x05},       // binary.wast:488  kind 5
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x02, 0x05, 0x01, 0x00, 0x00, 0x05, 0x00}, // binary.wast:498  kind 5 + dummy
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x02, 0x04, 0x01, 0x00, 0x00, 0x80},       // binary.wast:530  kind 0x80
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrMalformedImportKind) {
			t.Errorf("DecodeModule(% x): got %v, want ErrMalformedImportKind", in, err)
		}
	}
}

// TestGatesRejectWithFeatureNames is the executable form of "gates never
// manufacture malformedness" (CLAUDE.md, Scott's ruling on #5).
//
// Three claims, and the third is the one that needs a test rather than a comment:
//  1. a gated construct is rejected — accept-and-ignore silently breaks semantics;
//  2. the error names the feature, so it is answerable;
//  3. the error is NOT any suite malformed-string. A gate that spoofed a spec
//     string would score itself green for rejecting a module the spec calls
//     well-formed, which is the suite measuring the wrong thing. That is a
//     property of the error text, invisible to any pass count, so it is asserted
//     directly.
func TestGatesRejectWithFeatureNames(t *testing.T) {
	// v128 in a type section, with the SIMD gate off.
	simd := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x60, 0x01, 0x7B, 0x00, // functype: (param v128)
	} // synthetic: the suite's v128 vectors live in simd_*.wast, which phase 1 does not run
	// memory64 limits flags, gate off. This one is not synthetic — it is the
	// vector that exposed the harness's missing third verdict.
	mem64 := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x03, 0x01, 0x04, 0x00,
	} // synthetic: the flags byte lifted out of binary_leb128_64.wast:1, which also needs a code section

	for name, in := range map[string][]byte{"simd": simd, "memory64": mem64} {
		_, err := DecodeModule(in)
		if !errors.Is(err, ErrFeatureDisabled) {
			t.Errorf("%s gate off: got %v, want ErrFeatureDisabled", name, err)
			continue
		}
		// Claim 2: the feature is named, so the error tells you which gate to flip.
		if got := err.Error(); !contains(got, name) {
			t.Errorf("%s gate off: error %q does not name the feature", name, got)
		}
		// Claim 3: no spec malformed-string is impersonated.
		for _, spec := range []error{
			ErrTruncated, ErrPayloadEnd, ErrSectionSizeMismatch, ErrSectionOverrun,
			ErrMalformedSectionID, ErrMalformedLimits, ErrMalformedValType,
			ErrTrailingData,
		} {
			if errors.Is(err, spec) {
				t.Errorf("%s gate off: error impersonates the spec string %q; a gate partitions acceptance, it does not redraw the grammar", name, spec)
			}
		}
	}

	// With the gate on, the same images decode. This is the half that proves the
	// gate is a gate and not a permanent rejection wearing a feature name.
	on := &Decoder{Features: Features{SIMD: true, Memory64: true}}
	for name, in := range map[string][]byte{"simd": simd, "memory64": mem64} {
		if _, err := on.DecodeModule(in); err != nil {
			t.Errorf("%s gate on: got %v, want accept", name, err)
		}
	}
}

// TestTagSectionIsWellFormedButGated is the ruling on id 13 made executable.
//
// Scott's correction of his own earlier direction: the suite asserts nothing
// about id 13, 14 is the first malformed id, and *malformed* belongs to the
// grammar of the tracked union — which includes the tag section, because Wasm 3.0
// defines it. So with the EH gate off the module is rejected, but never as
// "malformed section id"; and the structural layer ranks the id in both gate
// states.
func TestTagSectionIsWellFormedButGated(t *testing.T) {
	// No suite vector asserts a verdict for a tag section in either gate state,
	// which is precisely the finding that corrected the issue text.
	tag := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0D, 0x01, 0x00} // synthetic

	_, err := DecodeModule(tag)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("tag section, EH gate off: got %v, want ErrFeatureDisabled", err)
	}
	if errors.Is(err, ErrMalformedSectionID) {
		t.Error("tag section reported as a malformed id: id 13 is defined by Wasm 3.0, so it is well-formed in the tracked union regardless of the gate")
	}

	// Gate on: the id is accepted and its contents become the EH gate's business
	// (#8). Ranked in both states — the structural layer never consults a gate.
	on := &Decoder{Features: Features{ExceptionHandling: true}}
	if _, err := on.DecodeModule(tag); err != nil {
		t.Errorf("tag section, EH gate on: got %v, want accept", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
