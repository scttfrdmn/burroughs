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
func TestSectionSizeBothSigns(t *testing.T) {
	// Grammar consumed MORE than declared: a type section declaring 7 bytes and
	// supplying two 3-byte functypes plus the count = 7 bytes of grammar... with
	// the count saying 1, so the grammar stops after 4 and 3 bytes are left over.
	grammarShort := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x07, 0x01, 0x60, 0x00, 0x00, 0x60, 0x00, 0x00,
	} // binary.wast:469  1 type declared, 2 given
	if _, err := DecodeModule(grammarShort); !errors.Is(err, ErrSectionSizeMismatch) {
		t.Errorf("grammar-short-of-declared: got %v, want ErrSectionSizeMismatch", err)
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
	if _, err := DecodeModule(declaredPastImage); !errors.Is(err, ErrSectionOverrun) {
		t.Errorf("declared-past-image: got %v, want ErrSectionOverrun", err)
	}

	// And face 3 in the grammar-long direction, where the image *is* long enough
	// because a later section supplies the bytes the grammar over-reads into. This
	// is the sign that a section-bounded reader would never produce, and the one
	// binary.wast:92 documents in its own comment.
	grammarLong := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x06, 0x0B, 0x01, 0x7F, 0x00, 0x41, 0x00, 0x0B, 0x7F, 0x00, 0x41, 0x00, 0x0B,
	} // binary.wast:714  1 global declared, 2 given
	// The global section has no grammar yet (#25), so this vector is still on the
	// board — asserting the error here would be asserting a check that does not
	// exist. What is pinned is that it is *not* silently accepted for the wrong
	// reason: it must fail, or be a declared gap.
	if _, err := DecodeModule(grammarLong); err == nil {
		t.Log("binary.wast:714 accepted — global section grammar is not implemented (#25); tracked, not silent")
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
