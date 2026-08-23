package binary

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
// "unexpected end" is a *prefix* of "unexpected end of section or function",
// and the harness matches by prefix (ADR 0045). So the long form satisfies both families
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
	// upstream ever reworded either string so that one stopped *beginning* the
	// other, the harness would silently stop scoring one of the two families and
	// this is what would say so. The assertion below was written as a prefix test
	// while the harness matched by substring, so ADR 0045 changed the rule this
	// check states and not the check.
	short, long := ErrTruncated.Error(), ErrPayloadEnd.Error()
	if len(long) <= len(short) || long[:len(short)] != short {
		t.Errorf("ErrPayloadEnd (%q) must begin with ErrTruncated's text (%q): the harness matches by prefix (ADR 0045), and ten vectors depend on the longer form satisfying the shorter", long, short)
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
	// **Two rows were removed here when #86 landed, and that is the finding, not the
	// edit.** `0x5e` and `0x5f` sat in this malformedness partition as "array" and
	// "struct" — named for what they *are* in the spec while being asserted as forms with
	// no grammar. They are `comptype`'s second and third arms (decode.ml:250-259), so they
	// are now legal with the GC gate on and feature-declined with it off, and neither
	// verdict is a malformed-form error. A row whose label contradicts its assertion is
	// the partition defect (grave #34) with the label telling the truth: the case names
	// said array and struct, and only the reference said whether that mattered. They move
	// to TestCompTypeFormsAreDecoded, which asserts both directions.
	//
	// What remains is the set that really has no arm: non-negative bytes where an s7 form
	// goes, and the tags between the defined constructors.
	for _, tc := range []struct{ name, want string }{
		{"0x7f i32 where a form goes", "0x7f"},
		{"0x40 the s7 minimum", "0x40"},
		{"0x5d no such constructor", "0x5d"},
		{"0x00 zero — non-negative, so non-discriminating for the message byte", "0x00"},
	} {
		tag := map[string]byte{"0x7f": 0x7F, "0x40": 0x40, "0x5d": 0x5D, "0x00": 0x00}[tc.want]
		r := &reader{b: []byte{tag, 0x00, 0x00}, eof: ErrPayloadEnd}
		err := (&Decoder{Features: Features{GC: true}}).decodeCompType(r)
		if !errors.Is(err, ErrMalformedDefType) {
			t.Errorf("%s: got %v, want ErrMalformedDefType", tc.name, err)
			continue
		}
		if !contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name the byte the image held (%s) — the message must not invent bits the input never had", tc.name, err, tc.want)
		}
	}

	// The accept path, since every case above is a rejection: 0x60 is the one tag
	// that decodes to -0x20 and must still work as a plain byte in the stream. Ungated,
	// unlike its two siblings — functype is Wasm 1.0.
	r := &reader{b: []byte{0x60, 0x00, 0x00}, eof: ErrPayloadEnd}
	if err := (&Decoder{}).decodeCompType(r); err != nil {
		t.Errorf("0x60 functype: got %v, want accept", err)
	} else if r.off != 3 {
		t.Errorf("0x60 functype consumed %d bytes, want 3 — the tag is one byte on the wire even though it is read as an s7", r.off)
	}
}

// TestImportDescriptorsAreRetained pins #164's representation change directly at the decoder,
// independent of the linker that consumes it: a table, memory and global import each keep their
// full descriptor rather than only their kind byte, which is what decodeImport reads and used to
// discard.
//
// Hand-built rather than routed through the text encoder, because internal/text imports
// internal/binary and this file is `package binary` (white-box) — importing text here would be
// the cycle grave #125 already exists to avoid at the opposite seam.
func TestImportDescriptorsAreRetained(t *testing.T) {
	name := func(s string) []byte {
		return append(ulebBytes(uint32(len(s))), []byte(s)...)
	}
	var imports []byte
	imports = append(imports, name("m")...)
	imports = append(imports, name("t")...)
	imports = append(imports, 0x01)  // table
	imports = append(imports, 0x70)  // funcref
	imports = append(imports, 0x01)  // limits: has max
	imports = append(imports, 3, 10) // min 3, max 10
	imports = append(imports, name("m")...)
	imports = append(imports, name("mm")...)
	imports = append(imports, 0x02) // memory
	imports = append(imports, 0x01) // limits: has max
	imports = append(imports, 2, 4) // min 2, max 4
	imports = append(imports, name("m")...)
	imports = append(imports, name("g")...)
	imports = append(imports, 0x03) // global
	imports = append(imports, 0x7F) // i32
	imports = append(imports, 0x01) // mut

	img := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	payload := append([]byte{0x03}, imports...) // 3 imports
	img = append(img, 0x02)
	img = append(img, ulebBytes(uint32(len(payload)))...)
	img = append(img, payload...)

	m, err := DecodeModule(img)
	if err != nil {
		t.Fatalf("DecodeModule: %v", err)
	}
	if len(m.Imports) != 3 {
		t.Fatalf("got %d imports, want 3", len(m.Imports))
	}

	tab := m.Imports[0]
	if tab.Table.ElemType != FuncRef {
		t.Errorf("table import: ElemType = %v, want FuncRef", tab.Table.ElemType)
	}
	if tab.Table.Limits.Min != 3 || !tab.Table.Limits.HasMax || tab.Table.Limits.Max != 10 {
		t.Errorf("table import: Limits = %+v, want {Min:3 HasMax:true Max:10}", tab.Table.Limits)
	}

	mem := m.Imports[1]
	if mem.Memory.Limits.Min != 2 || !mem.Memory.Limits.HasMax || mem.Memory.Limits.Max != 4 {
		t.Errorf("memory import: Limits = %+v, want {Min:2 HasMax:true Max:4}", mem.Memory.Limits)
	}

	glob := m.Imports[2]
	if glob.GlobalType != I32 {
		t.Errorf("global import: GlobalType = %v, want I32", glob.GlobalType)
	}
	if !glob.GlobalMutable {
		t.Error("global import: GlobalMutable = false, want true")
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

	// **`&Decoder{}`, not `DecodeModule`** — the zero value, deliberately, per Features's own
	// doc comment: this test's subject is "the gate off", and `DecodeModule` no longer means
	// that for every gate since #227's SIMD flip (`DefaultFeatures` sets `SIMD: true`). The
	// zero value is still every gate off, unconditionally, which is exactly what "gate off"
	// needs to construct explicitly now that it and "default policy" are different facts.
	off := &Decoder{}
	for name, in := range map[string][]byte{"simd": simd, "memory64": mem64} {
		_, err := off.DecodeModule(in)
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
			ErrMalformedSectionID, ErrMalformedLimits,
			ErrMalformedNumType, ErrMalformedVecType, ErrMalformedRefType,
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

// TestDefaultFeaturesAndZeroValueAreDistinctFacts pins #227/ADR 0025's own mechanism: the
// zero value means every gate off, unconditionally, and DefaultFeatures is a separate,
// independently-settable fact — the two happened to be identical until this flip, and this
// test is what keeps a future flip from silently reintroducing the same collapse (a field
// whose zero value carries the default, which cannot flip default-on without either breaking
// every other caller's all-off assumption or inverting the field's own name into a lie).
//
// Its four scalar assertions moved to [TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp],
// which makes the same statement over **every** field by reflection rather than over the two
// that had flipped when this test was written. What stays here is the half that test cannot
// make: the policy actually reaching the package-level entry point. Two memberships asserted
// in two places would be two authorities for one fact, and the weaker one is the one a later
// reader trusts.
func TestDefaultFeaturesAndZeroValueAreDistinctFacts(t *testing.T) {
	// v128 in a type section (the identical synthetic image TestGatesRejectWithFeatureNames
	// uses for its own "simd" case): DecodeModule now accepts it directly, with no Decoder
	// constructed by the caller — this is DefaultFeatures actually reaching the package-level
	// entry point, not merely a fact asserted about the struct in isolation.
	simd := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x60, 0x01, 0x7B, 0x00,
	}
	if _, err := DecodeModule(simd); err != nil {
		t.Errorf("DecodeModule(v128 type), default policy: got %v, want accept", err)
	}
	// The explicit zero-value decoder still declines the identical image — proof this is a
	// policy fact layered on top of the zero value, not a change to the zero value itself.
	if _, err := (&Decoder{}).DecodeModule(simd); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("&Decoder{}.DecodeModule(v128 type): got %v, want ErrFeatureDisabled — the zero "+
			"value must still mean every gate off", err)
	}
}

// defaultGatePolicy is v0's whole default gate policy written down one gate at a time, and
// it is the line a flip has to edit before it can land.
//
// The two on-rows name the ADR that stamped them and the test resolves the file, because
// behaviour 4's rule is that a flip is a stamp-tier event: the mechanism self-merges, the
// *default* holds for a principal. So an on-row with no resolvable stamp is the one state
// this table exists to make impossible.
//
// The off-rows carry no citation on purpose. There is nothing to cite — the default is
// behaviour 4's, which is what a gate gets by *not* having a flip event, so a citation
// there would be a pointer to an absence. What they carry instead is why the gate is off
// today, since that is the sentence a future flip has to argue with.
var defaultGatePolicy = map[string]struct {
	on    bool
	stamp string // an on-row's flip ADR, resolved as a path by the test; empty for an off-row
	why   string
}{
	"ExceptionHandling": {why: "behaviour 4's default: no flip event. #95 owes the tag section's payload grammar first"},
	"SIMD":              {on: true, stamp: "0025-g-1-carves-out-vectors-whose-sole-blocker-is-9s-deferred-validator.md", why: "#227, the project's first default-behaviour change"},
	"Threads":           {why: "behaviour 4's default, and the feature is v1's phase (contract §§2–5) — its vectors are outside the board's file set, so no board could see this row move"},
	"Memory64":          {why: "behaviour 4's default: no flip event"},
	"GC":                {why: "behaviour 4's default. #172 is scheduled to `v0.2.0 GC gate`, a milestone after v0.1.0"},
	"TailCall":          {why: "behaviour 4's default: no flip event"},
	"RelaxedSIMD":       {on: true, stamp: "0028-relaxed-simd-lowerings-are-deterministic-and-architecture-uniform-the-references-choice-taken-once.md", why: "#275, and it satisfies G-1's literal reading without ADR 0025's carve-out"},
	"MultiMemory":       {why: "behaviour 4's default: no flip event"},
	"ExtendedConst":     {why: "behaviour 4's default. #109 is why this field exists at all — the gate the file forgot"},
}

// TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp is the whole of `DefaultFeatures`
// held by one assertion per gate, ordered by Scott on the #499 reconciliation.
//
// # The gap it was built for, which was measured rather than argued
//
// `CLAUDE.md`'s ladder used to close v0 on "every 3.0-feature gate present **and off**".
// #464/#466 found that clause falsified by two flips Scott had himself stamped, and the
// amendment reads "*and its default a recorded decision*" — which moved the condition from
// a state of the code into a claim about **provenance**, and provenance is the one kind of
// claim nothing in this tree reads.
//
// What held the amended condition before this test was
// [TestDefaultFeaturesAndZeroValueAreDistinctFacts] asserting **two memberships** — SIMD on,
// RelaxedSIMD on — and nothing asserting the *set*. The measurement, by substituting a flip
// into `DefaultFeatures` one field at a time and running the whole suite against each:
// six of the seven off-gates fail something, every one of them a test whose subject is
// elsewhere and which happens to name the gate in passing (chiefly the gate table in
// `internal/testenv`'s foreclosure sweep, whose subject is *prose*). **`Threads: true`
// passes the entire suite with the board byte-identical**, because the threads vectors live
// under `testdata/spec/proposals/threads`, outside the 256 files the board walks. A
// closure condition satisfiable by accident is not a condition, and one of the nine was
// not even that.
//
// # Watched failing, eleven ways
//
// A control is not born until it is watched die (grave #34's family). Nine mutations, one
// per field, each flipping that field's default to the opposite of its row and restoring
// after: **9 of 9 fired**, which is the pre-registered forecast on #499 met exactly, against
// 6-of-7-by-accident and 0-of-1 for `Threads` before it. Two more for the structure — a
// tenth field added to `Features` with no row here fires the coverage arm, and a row naming
// a field that does not exist fires the other direction. The forecast was registered before
// the mutations ran, because a number read off a control you already built is not a forecast.
//
// # Why the domain is reflected and not typed out
//
// A tenth gate is in this control's scope without anyone editing it, and the failure it
// gets is *"add a row"* rather than silence. Enumerating the field names here would inherit
// exactly today's blind spot — the shape that made `Threads` invisible in the first place,
// one level up.
func TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp(t *testing.T) {
	ty := reflect.TypeOf(Features{})
	def := reflect.ValueOf(DefaultFeatures())
	zero := reflect.ValueOf(Features{})

	if ty.NumField() < 4 {
		t.Fatalf("reflection derived %d fields from Features; the struct had 9 when this test was "+
			"written, and a domain this small means the parse found nothing to walk", ty.NumField())
	}

	// Both directions of coverage, and they are separate arms because they fail for
	// unrelated reasons: a gate added to the struct and forgotten here, versus a row that
	// outlived its field and now licenses nothing while reading as coverage.
	fields := map[string]bool{}
	for i := range ty.NumField() {
		fields[ty.Field(i).Name] = true
	}
	for name := range fields {
		if _, ok := defaultGatePolicy[name]; !ok {
			t.Errorf("Features.%s has no row in defaultGatePolicy: a gate whose default nothing "+
				"states can flip on without editing a line that names the stamp it needs, which "+
				"is the #464 gap this table was built to close", name)
		}
	}
	for name := range defaultGatePolicy {
		if !fields[name] {
			t.Errorf("defaultGatePolicy names %q, which is not a field of Features: the row is "+
				"stale and pins nothing while counting as coverage", name)
		}
	}

	var on, off int
	for i := range ty.NumField() {
		name := ty.Field(i).Name
		row, ok := defaultGatePolicy[name]
		if !ok {
			continue // already reported above
		}
		if ty.Field(i).Type.Kind() != reflect.Bool {
			t.Errorf("Features.%s is %s, not a bool: this table cannot state a default for it",
				name, ty.Field(i).Type.Kind())
			continue
		}

		// The default, gate by gate. This is the arm the nine mutations fire.
		if got := def.Field(i).Bool(); got != row.on {
			t.Errorf("DefaultFeatures().%s = %v, want %v.\n\nThe table says: %s\n\nA default gate "+
				"state is a recorded decision (CLAUDE.md's ladder, as amended by #466), so this is "+
				"either a flip that needs a principal's stamp and an ADR cited in the row above — "+
				"behaviour 4, and the flip is never in the mechanism's PR — or a regression in a "+
				"policy someone already stamped.", name, got, row.on, row.why)
		}

		// The zero value stays every gate off, for every field rather than for the two that
		// have flipped. `Features`' own doc comment makes this a load-bearing distinction:
		// a field whose zero value carried the default could not flip without breaking every
		// caller's all-off assumption.
		if zero.Field(i).Bool() {
			t.Errorf("Features{}.%s is true: the zero value must stay every gate off "+
				"unconditionally, per Features's own doc comment — a default belongs to "+
				"DefaultFeatures and nowhere else", name)
		}

		if row.why == "" {
			t.Errorf("defaultGatePolicy[%q] states no reason; the sentence a future flip has to "+
				"argue with is the point of the row", name)
		}
		if row.on {
			on++
			if row.stamp == "" {
				t.Errorf("Features.%s is on by default with no ADR cited: a flip is a stamp-tier "+
					"event (behaviour 4) and an uncited one is exactly the provenance claim nothing "+
					"in this tree reads", name)
				continue
			}
			// The stamp resolves, or it is a description of an approval rather than a
			// citation to one. `docs/decisions/` is committed, so there is no skip door
			// here: an absent file is a failure, never a licensed absence.
			path := filepath.Join("..", "..", "docs", "decisions", row.stamp)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("Features.%s cites %s, which does not resolve (%v): a Status field is a "+
					"citation to an approval, and so is this", name, row.stamp, err)
			}
		} else {
			off++
			if row.stamp != "" {
				t.Errorf("Features.%s is off by default but cites %q: an off default is behaviour "+
					"4's, so there is no flip event to point at and the citation names an absence",
					name, row.stamp)
			}
		}
		t.Logf("%-18s default=%-5v stamp=%s", name, def.Field(i).Bool(), row.stamp)
	}

	// Both halves need a subject. With no on-gate the stamp arm never runs and with no
	// off-gate the no-stamp arm never runs, and either way this test would pass by asking
	// half its questions.
	if on == 0 || off == 0 {
		t.Errorf("derived %d on-gates and %d off-gates from a %d-field struct: one side of this "+
			"control has no population, so it agrees by not being asked", on, off, ty.NumField())
	}
	t.Logf("pinned %d gates: %d on with a resolved stamp, %d off on behaviour 4's default",
		on+off, on, off)
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

// TestTableInitializerFormIsGatedNotMalformed is #51's accept-direction defect pinned
// from both sides, on the vector that exposed it.
//
// The bug: `(table 1 (ref func) (ref.func 0))` encodes as the `0x40` table form, the
// `0x40` reached decodeRefType, and the decoder answered `malformed reference type: 0x40`
// — a valid module rejected with the spec's own word, seven times in elem.wast. Two
// failures in one, which is why two assertions:
//
//  1. **Gate off**: rejected, but feature-named. The construct is defined by Wasm 3.0, so
//     the thing declining it is the engine's configuration, and it must say so. Asserted
//     negatively as well — `ErrMalformedRefType` specifically, since that is the string
//     the defect produced and a regression would produce it again.
//  2. **Gate on**: accepted, **and the initializer read back** rather than the accept taken as
//     proof of one. Until #419 the expression was decoded and dropped, so "accepted" was the
//     strongest thing available and it said only that the const-expr descent did not error.
//     A status flag cannot distinguish a retained `ref.func 0` from a retained nothing
//     (evidence-and-instruments.md: read the write's payload, not its status), and with the
//     field now populated the payload is the assertion.
//
// The vector is **cited**, not transcribed: the literal below is the assembled image of
// elem.wast:453, and TestFixtureProvenance compares it against what the parser builds
// from that line. All seven of the elem.wast declines carry the byte-identical table
// entry `\40\00\64\70\00\01\d2\00\0b`, so one citation covers the class; the other six
// are named in internal/spec's TestGatedVectors allowlist, where they are honestly
// `gated` on the default board and *passing* in the all-gates-on lane.
func TestTableInitializerFormIsGatedNotMalformed(t *testing.T) {
	img := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00, 0x03, 0x02, 0x01, 0x00, 0x04, 0x0A, 0x01, 0x40, 0x00, 0x64, 0x70, 0x00, 0x01, 0xD2, 0x00, 0x0B, 0x09, 0x07, 0x01, 0x00, 0x41, 0x00, 0x0B, 0x01, 0x00, 0x0A, 0x04, 0x01, 0x02, 0x00, 0x0B} // elem.wast:453

	_, err := DecodeModule(img)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("0x40 table form, GC gate off: got %v, want ErrFeatureDisabled", err)
	}
	if errors.Is(err, ErrMalformedRefType) {
		t.Error("0x40 table form reported as a malformed reference type: this is #51's defect — " +
			"the form is defined by Wasm 3.0, so a gate-off engine declines it by feature name")
	}
	if got := err.Error(); !contains(got, "gc") {
		t.Errorf("0x40 table form, GC gate off: error %q does not name the gate to flip", got)
	}

	on := &Decoder{Features: Features{GC: true}}
	m, err := on.DecodeModule(img)
	if err != nil {
		t.Errorf("0x40 table form, GC gate on: got %v, want accept", err)
		return
	}
	if len(m.Tables) != 1 {
		t.Fatalf("decoded %d tables, want 1", len(m.Tables))
	}
	// `\d2\00\0b` is `ref.func 0` then END, and both instructions are asserted: an
	// initializer truncated at the terminator still evaluates to the right value here, so a
	// check on the first instruction alone would pass on a retention that lost the extent
	// the whole grammar exists to discover.
	want := []Instr{{Op: 0xD2, Imm0: 0}, {Op: 0x0B}}
	if got := m.Tables[0].Init; !slices.Equal(got, want) {
		t.Errorf("table initializer: got %+v, want %+v — the const expr is decoded and retained "+
			"as of #419, so an accept no longer stands in for the value", got, want)
	}
}

// TestImportedTableDescriptorHasNoInitializerForm is grave #420, pinned in the direction
// that produced it.
//
// The reference's `externtype` reads an imported table's descriptor with `tabletype`
// (decode.ml:309), which is `reftype limits` and has **no `0x40` arm**. The `0x40 0x00
// tabletype const` form belongs to the table *section*'s `table` production
// (decode.ml:1050-1064). This decoder read both through one helper — factored on the
// difference that an import must not append to `m.Tables` — so an import descriptor
// admitted a form the reference calls `malformed reference type`, on the all-gates-on lane,
// where nothing in the corpus asks.
//
// Both lanes are asserted because the defect had a different shape on each. Gate off it was
// already a refusal, but by the *wrong* mechanism — `gc: feature gate disabled` names a gated
// construct, and here the byte is not a construct at all — so a fix that only moved the
// all-on lane would leave the gate-off error still testifying to a feature. Gate on it was an
// accept, which is the half with no vector: §9 G-3's blind spot.
//
// synthetic, and necessarily so: `binary.wast:397` is the suite's only `malformed reference
// type` vector and its subject is an element segment's reftype. No vector in either direction
// puts a `0x40` in an import descriptor, which is why this control is the only thing standing
// between the fix and its own regression.
func TestImportedTableDescriptorHasNoInitializerForm(t *testing.T) {
	img := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x02, 0x0E, 0x01, // import section, one entry
		0x01, 0x6D, 0x01, 0x74, 0x01, // "m" "t", kind 0x01 (table)
		0x40, 0x00, 0x70, 0x00, 0x01, // the table section's form, in a descriptor that has no such arm
		0xD0, 0x70, 0x0B, // ref.null func, END — the initializer it would have carried
	}
	for _, tc := range []struct {
		name string
		d    *Decoder
	}{
		{"GC gate off", &Decoder{}},
		{"every gate on", &Decoder{Features: featuresAllOn(t)}},
	} {
		_, err := tc.d.DecodeModule(img)
		if !errors.Is(err, ErrMalformedRefType) {
			t.Errorf("%s: got %v, want ErrMalformedRefType — 0x40 is not a reftype and an "+
				"import descriptor is `tabletype`, which has no arm that would make it one (grave #420)",
				tc.name, err)
			continue
		}
		// The gate-off half of the defect: a refusal that named a feature. `0x40` reaching
		// this production is malformed regardless of which proposals are on, so an error
		// mentioning a gate here is the old mechanism surviving behind the new verdict.
		if got := err.Error(); strings.Contains(got, "feature gate") {
			t.Errorf("%s: error %q declines by feature name for a byte no feature defines here", tc.name, got)
		}
	}
}

// TestRefTypeReadsTheReferencesFourteenForms scopes the control to the *space* rather
// than to the forms #51 needed (CLAUDE.md: a control scoped to the current sample
// inherits the current blind spot).
//
// decodeRefType previously compared two bytes; it now reads an s7 and ranks fourteen
// forms. Checking only the twelve that were wrong would leave the next added form
// unmeasured, so the domain here is **every s7 value a single byte can encode** —
// -64..63 — partitioned three ways: ungated accept, feature-named decline, malformed.
// The partition is asserted rather than the members enumerated, so a form moving between
// classes fails loudly and a form *added* to the reference shows up as a malformed byte
// the count no longer matches.
//
// synthetic: a one-byte reftype in a table section, constructed per form. The suite has
// vectors for a handful of these and none for most, which is the reason the sweep exists.
func TestRefTypeReadsTheReferencesFourteenForms(t *testing.T) {
	// The table section's element type is the byte under test; limits `\00\01` follow.
	image := func(form byte) []byte {
		return []byte{
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x04, 0x04, 0x01, form, 0x00, 0x01,
		}
	}
	// Every gate on, and the gated class is keyed by the **feature the decline names** rather
	// than tallied as one number. #395 is why: this sweep ran against a GC-only decoder and
	// counted ten forms as "gated off", which stayed true when two of the ten were gated by the
	// *wrong* proposal — `exn` and `noexn` are the exception proposal's (Exceptions.md:337-349)
	// and sat in GC's arm for three PRs. A partition that asks *whether* a form is gated cannot
	// see a form gated by the wrong gate; asking *which* gate is one more map key, and it turns
	// this into the control that would have caught it. Same shape as
	// TestHeapTypeGatesFormsNotThePosition's per-row gate, one production up.
	on := &Decoder{Features: featuresAllOn(t)}

	// Three bytes are not one-byte reftypes through this entry point, and each is
	// excluded for a *named* reason rather than by shrinking a count until it matched.
	// Measured, not predicted — the first draft of this test asserted 2/10/114 over all
	// 128 forms and found all three:
	//
	//	0x40  never reaches decodeRefType *through this image*. decodeTableForm peeks it and
	//	      takes the initializer form, so with GC on the `\00\01` limits are re-read as a
	//	      zero byte plus a reftype and the error names `0x01`. The enclosing grammar owns
	//	      the byte; TestTableInitializerFormIsGatedNotMalformed is its control.
	//
	//	      **GRAVE (#420): "the enclosing grammar" was two grammars.** This exclusion is
	//	      right about the table *section*'s `table` production and was written as though
	//	      that were the only one; an import descriptor is `tabletype` (decode.ml:309),
	//	      which has no `0x40` arm, so there the byte *is* decodeRefType's and is
	//	      malformed. The exclusion note is the sentence that hid it — a control's
	//	      carve-out inherits the domain its reason was written against, and this one's
	//	      reason quantified over one caller while its wording quantified over all of
	//	      them. TestImportedTableDescriptorHasNoInitializerForm is the other grammar's
	//	      control, and the reason the two exist separately is that they now disagree
	//	      about this byte on purpose.
	//	0x63  -0x1d, `(ref null ht)` — takes a following heaptype, so a one-byte image
	//	0x64  -0x1c, `(ref ht)`      — truncates instead of deciding. Both are covered by
	//	      TestHeapTypeFollowsTheParameterizedForms, which supplies the second byte.
	//
	// Asserted as a set, so a form leaving or joining this list is a failure rather than
	// a silently absorbed difference.
	excluded := map[byte]string{0x40: "table initializer prefix", 0x63: "(ref null ht)", 0x64: "(ref ht)"}

	var ungated, malformed int
	gatedBy := map[string]int{}
	seenExcluded := map[byte]bool{}
	for v := -64; v < 64; v++ {
		form := byte(v & 0x7F)
		img := image(form)

		_, off := DecodeModule(img)
		_, all := on.DecodeModule(img)

		switch {
		case off == nil && all == nil:
			ungated++
		case errors.Is(off, ErrFeatureDisabled) && all == nil:
			// The feature name is the text before `featureErr`'s separator, which is the
			// only place a decline says *which* gate declined — ErrFeatureDisabled cannot
			// carry it, and that is the verdict/mechanism split: the sentinel says whether,
			// the message says which.
			gatedBy[strings.TrimSuffix(off.Error(), ": "+ErrFeatureDisabled.Error())]++
		case errors.Is(off, ErrMalformedRefType) && errors.Is(all, ErrMalformedRefType):
			malformed++
		case excluded[form] != "":
			seenExcluded[form] = true
		default:
			t.Errorf("s7 form %d (byte %#02x) is in no class: gate off %v, all gates on %v",
				v, form, off, all)
		}
	}

	// The counts are the assertion: two ungated (funcref 0x70, externref 0x6F — the Wasm
	// 2.0 subset), eight abstract forms gated by GC, **two gated by exception handling**
	// (`exn` -0x17, `noexn` -0x0c), and the remaining 113 malformed. 2+8+2+113+3 = 128,
	// which is the whole s7 space. The per-gate split is #395's pin: before it, both
	// spellings of this line read `10` and agreed with a wrong attribution.
	wantGated := map[string]int{"gc": 8, "exception handling": 2}
	if ungated != 2 || malformed != 113 || !maps.Equal(gatedBy, wantGated) {
		t.Errorf("reftype partition over s7: %d ungated, %v gated, %d malformed; "+
			"want 2 / %v / 113 — funcref+externref ungated, eight GC abstract forms, "+
			"exn and noexn under the exception gate (#395), the rest malformed",
			ungated, gatedBy, malformed, wantGated)
	}
	// Vacuity floor: an all-zero tally satisfies a comparison against zeros, so the sweep
	// asserts it actually classified the space rather than merely finishing the loop.
	gated := 0
	for _, n := range gatedBy {
		gated += n
	}
	if n := ungated + gated + malformed + len(seenExcluded); n != 128 {
		t.Errorf("classified %d of 128 s7 forms; the sweep is not covering the space", n)
	}
	for form, why := range excluded {
		if !seenExcluded[form] {
			t.Errorf("byte %#02x (%s) no longer lands outside the one-byte partition; "+
				"either its grammar moved or this exclusion is stale", form, why)
		}
	}
}

// TestHeapTypeFollowsTheParameterizedForms covers the two reftype forms the sweep above
// deliberately cannot: -0x1c and -0x1d each take a heaptype, so they are two bytes.
//
// Both directions of the heaptype alternation, because it is an `either` and an `either`
// is where a specific error goes to die: a type *index* (s33, non-negative) and an
// abstract form. A one-byte image for these forms truncates rather than deciding, which
// is what keeps them out of the single-byte partition.
//
// synthetic: no phase-1 vector encodes a parameterized reftype outside elem.wast's table
// form, and there the heaptype is `\70` (func) only.
func TestHeapTypeFollowsTheParameterizedForms(t *testing.T) {
	// The declared section size is computed, not written by hand: one byte of vec count,
	// the reftype tail, and two bytes of limits. The first draft hard-coded the wrong sum
	// and every case failed with `declared 4, grammar consumed 5` — the size check doing
	// its job on the test rather than on the engine.
	image := func(tail ...byte) []byte {
		img := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x04, byte(3 + len(tail)), 0x01}
		return append(append(img, tail...), 0x00, 0x01)
	}
	on := &Decoder{Features: Features{GC: true}}

	cases := []struct {
		name string
		tail []byte
		ok   bool
	}{
		{"(ref func): abstract heaptype", []byte{0x64, 0x70}, true},
		{"(ref null func): abstract heaptype", []byte{0x63, 0x70}, true},
		{"(ref 0): a type index", []byte{0x64, 0x00}, true},
		{"(ref null 3): a type index", []byte{0x63, 0x03}, true},
		{"(ref 0x40): neither an index nor an abstract form", []byte{0x64, 0x40}, false},
	}
	for _, c := range cases {
		img := image(c.tail...)
		_, err := on.DecodeModule(img)
		if c.ok && err != nil {
			t.Errorf("%s: got %v, want accept with GC on", c.name, err)
		}
		if !c.ok && !errors.Is(err, ErrMalformedHeapType) {
			t.Errorf("%s: got %v, want ErrMalformedHeapType", c.name, err)
		}
		// The gate governs the prefix, so every one of these declines by feature name
		// with GC off — including the malformed heaptype, which never gets read. That
		// ordering is the point: decodeRefType checks the gate *before* descending, so
		// the error names the layer the user can act on.
		if _, off := DecodeModule(img); !errors.Is(off, ErrFeatureDisabled) {
			t.Errorf("%s, GC gate off: got %v, want ErrFeatureDisabled", c.name, off)
		}
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
