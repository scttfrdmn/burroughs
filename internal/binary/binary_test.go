package binary

import (
	"bytes"
	"errors"
	"testing"
)

var preamble = []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}

func TestDecodeEmptyModule(t *testing.T) {
	m, err := DecodeModule(preamble)
	if err != nil {
		t.Fatalf("empty module: %v", err)
	}
	if m.Version != 1 || len(m.Sections) != 0 {
		t.Fatalf("got version=%d sections=%d", m.Version, len(m.Sections))
	}
}

func TestDecodeCustomSection(t *testing.T) {
	// custom section: id=0, size=6, payload = name vec "name" + 1 data byte
	payload := append([]byte{0x04}, []byte("name")...)
	payload = append(payload, 0xAB)
	mod := append(append([]byte{}, preamble...), 0x00, byte(len(payload)))
	mod = append(mod, payload...)

	m, err := DecodeModule(mod)
	if err != nil {
		t.Fatalf("custom section: %v", err)
	}
	if len(m.Sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(m.Sections))
	}
	s := m.Sections[0]
	if s.ID != SectionCustom || !bytes.Equal(s.Payload, payload) {
		t.Fatalf("got section %v payload %x", s.ID, s.Payload)
	}
}

func TestBadMagic(t *testing.T) {
	// binary.wast:11  "msa\00\00\00\00\01"
	_, err := DecodeModule([]byte{0x6D, 0x73, 0x61, 0x00, 0x00, 0x00, 0x00, 0x01})
	if !errors.Is(err, ErrBadMagic) {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
}

func TestBadVersion(t *testing.T) {
	// binary.wast:41  "\00asm\0d\00\00\00"
	_, err := DecodeModule([]byte{0x00, 0x61, 0x73, 0x6D, 0x0D, 0x00, 0x00, 0x00})
	if !errors.Is(err, ErrBadVersion) {
		t.Fatalf("got %v, want ErrBadVersion", err)
	}
}

func TestSectionOverrun(t *testing.T) {
	// section claims 5 payload bytes, only 1 present
	mod := append(append([]byte{}, preamble...), 0x01, 0x05, 0xFF)
	_, err := DecodeModule(mod)
	if !errors.Is(err, ErrSectionOverrun) {
		t.Fatalf("got %v, want ErrSectionOverrun", err)
	}
}

// TestShortPreamble pins the truncated-vs-wrong preamble split: a short image
// is "unexpected end", a full-width wrong one is a magic/version failure.
//
// Every vector carries a `binary.wast:N` citation, and those citations are
// machine-checked — see TestFixtureProvenance in internal/spec, which reads
// this file, extracts each citation, and asserts the bytes match the suite at
// that line. The check exists because an earlier version of this comment
// claimed the vectors were verbatim while two of them were not: the BOM case
// had been hand-truncated from 11 bytes to 8, and the "asm\00" case was a
// hand-mutation of a vector the suite does not contain. That is grave #1's own
// distinction — short preamble versus wrong magic — reintroduced in the fixture
// that tests it. Cite, and let a test check the citation.
func TestShortPreamble(t *testing.T) {
	truncated := [][]byte{
		{},                             // binary.wast:6   ""
		{0x01},                         // binary.wast:7   "\01"
		{0x00, 0x61, 0x73},             // binary.wast:8   "\00as"
		{0x00, 0x61, 0x73, 0x6D},       // binary.wast:37  "\00asm"
		{0x00, 0x61, 0x73, 0x6D, 0x01}, // binary.wast:38  "\00asm\01"
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00}, // binary.wast:39  "\00asm\01\00\00"
	}
	for _, in := range truncated {
		if _, err := DecodeModule(in); !errors.Is(err, ErrTruncated) {
			t.Errorf("DecodeModule(%x): got %v, want ErrTruncated", in, err)
		}
	}
	// Full width, wrong magic → magic header not detected.
	for _, in := range [][]byte{
		{0x6D, 0x73, 0x61, 0x00, 0x01, 0x00, 0x00, 0x00},                   // binary.wast:11  "msa\00\01\00\00\00"
		{0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x00},                   // binary.wast:13  "asm\01\00\00\00\00"
		{0x77, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00},                   // binary.wast:14  "wasm\01\00\00\00"
		{0x00, 0x00, 0x00, 0x01, 0x6D, 0x73, 0x61, 0x00},                   // binary.wast:21  8-byte endian-reversed
		{0x00, 0x41, 0x53, 0x4D, 0x01, 0x00, 0x00, 0x00},                   // binary.wast:28  upper-cased
		{0x00, 0x81, 0xA2, 0x94, 0x01, 0x00, 0x00, 0x00},                   // binary.wast:31  EBCDIC-encoded magic
		{0xEF, 0xBB, 0xBF, 0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}, // binary.wast:34  UTF-8 BOM prefix
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrBadMagic) {
			t.Errorf("DecodeModule(%x): got %v, want ErrBadMagic", in, err)
		}
	}
	// Full width, right magic, wrong version → unknown binary version.
	for _, in := range [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x00, 0x00, 0x00, 0x00}, // binary.wast:40
		{0x00, 0x61, 0x73, 0x6D, 0x0D, 0x00, 0x00, 0x00}, // binary.wast:41
		{0x00, 0x61, 0x73, 0x6D, 0x0E, 0x00, 0x00, 0x00}, // binary.wast:42
		{0x00, 0x61, 0x73, 0x6D, 0x00, 0x01, 0x00, 0x00}, // binary.wast:43
		{0x00, 0x61, 0x73, 0x6D, 0x00, 0x00, 0x01, 0x00}, // binary.wast:44
		{0x00, 0x61, 0x73, 0x6D, 0x00, 0x00, 0x00, 0x01}, // binary.wast:45
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrBadVersion) {
			t.Errorf("DecodeModule(%x): got %v, want ErrBadVersion", in, err)
		}
	}
}

// TestLEBTaxonomy is the regression test for decision 0003's two graves. Every
// vector is the integer field extracted from a binary-leb128.wast module, at the
// width it appears at there, because the verdict depends on the width:
// ff ff ff ff 0f is a valid u32 and a malformed i32 const.
//
// These are `synthetic` in TestFixtureProvenance's sense — deliberately so, and
// this is the distinction that test exists to force. A vector here is a *field*
// lifted out of a module image, not the image; it cannot equal a suite module
// image because it is a fragment of one. Marked rather than left uncited: the
// classification question is the point, and "extracted fragment" is a real
// answer where "close enough to verbatim" was not.
func TestLEBTaxonomy(t *testing.T) {
	cases := []struct {
		name string
		bits uint
		in   []byte
		want error
		val  uint64
	}{
		// Continuation bit set on the last permitted byte.
		{"u32 five 0x80 then 0", 32, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, ErrLEBTooLong, 0},
		{"u32 min-2 one byte too many", 32, []byte{0x82, 0x80, 0x80, 0x80, 0x80, 0x00}, ErrLEBTooLong, 0},
		{"u32 all-ff then 0x7f", 32, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, ErrLEBTooLong, 0},
		{"u32 section size 3 too long", 32, []byte{0x83, 0x80, 0x80, 0x80, 0x80, 0x00}, ErrLEBTooLong, 0},
		{"u64 eleven bytes", 64, []byte{0x82, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, ErrLEBTooLong, 0}, // synthetic: extracted field, one byte past the u64 limit

		// Continuation clear, unused bits set beyond the width.
		{"u32 unused bits 0x10", 32, []byte{0x80, 0x80, 0x80, 0x80, 0x10}, ErrLEBOverflow, 0},
		{"u32 unused bits 0x40", 32, []byte{0x83, 0x80, 0x80, 0x80, 0x40}, ErrLEBOverflow, 0},
		{"u32 unused bits 0x70", 32, []byte{0x80, 0x80, 0x80, 0x80, 0x70}, ErrLEBOverflow, 0},
		{"u32 unused bits 0x1f", 32, []byte{0x80, 0x80, 0x80, 0x80, 0x1F}, ErrLEBOverflow, 0},

		// Legal at the boundary — must NOT error.
		{"u32 max", 32, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}, nil, 0xFFFFFFFF},
		{"u32 zero", 32, []byte{0x00}, nil, 0},
		{"u32 624485", 32, []byte{0xE5, 0x8E, 0x26}, nil, 624485},
		{"u64 max", 64, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}, nil, 0xFFFFFFFFFFFFFFFF}, // synthetic: extracted field, the u64 boundary value
	}
	for _, c := range cases {
		r := &reader{b: c.in}
		got, err := r.uleb(c.bits)
		if c.want != nil {
			if !errors.Is(err, c.want) {
				t.Errorf("%s: uleb(%d) over %x = %v, want %v", c.name, c.bits, c.in, err, c.want)
			}
			continue
		}
		if err != nil || got != c.val {
			t.Errorf("%s: uleb(%d) over %x = %d, %v; want %d", c.name, c.bits, c.in, got, err, c.val)
		}
	}
}

// TestLEBTooLongReachable is grave 2's marker as an executable claim: the
// predecessor's ErrLEBTooLong was unreachable for every input. If a future edit
// reintroduces a single early-return that swallows the continuation-bit case,
// this fails.
func TestLEBTooLongReachable(t *testing.T) {
	r := &reader{b: []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}}
	if _, err := r.uleb(32); !errors.Is(err, ErrLEBTooLong) {
		t.Fatalf("ErrLEBTooLong unreachable again: got %v", err)
	}
}

// TestSectionOrder pins #6's order and uniqueness enforcement. Both families are
// one predicate — ranks must strictly increase — so a duplicate fails for the
// same reason a misordered section does, and these vectors exercise both faces of
// the single check.
func TestSectionOrder(t *testing.T) {
	for _, in := range [][]byte{
		// Duplicates: the same id twice, "not greater than" its own rank.
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x01, 0x00, 0x01, 0x01, 0x00}, // binary.wast:1080  type
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x02, 0x01, 0x00, 0x02, 0x01, 0x00}, // binary.wast:1070  import
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x04, 0x01, 0x00, 0x04, 0x01, 0x00}, // binary.wast:1050  table
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x05, 0x01, 0x00, 0x05, 0x01, 0x00}, // binary.wast:1090  memory
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x06, 0x01, 0x00, 0x06, 0x01, 0x00}, // binary.wast:1030  global
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x07, 0x01, 0x00, 0x07, 0x01, 0x00}, // binary.wast:1040  export
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x09, 0x01, 0x00, 0x09, 0x01, 0x00}, // binary.wast:1060  element
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0B, 0x01, 0x00, 0x0B, 0x01, 0x00}, // binary.wast:1020  data
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0C, 0x01, 0x01, 0x0C, 0x01, 0x01}, // binary.wast:1010  data count

		// Misordered: a lower rank following a higher one.
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x02, 0x01, 0x00, 0x01, 0x01, 0x00}, // binary.wast:1100  type after import
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x03, 0x01, 0x00, 0x02, 0x01, 0x00}, // binary.wast:1110  import after function
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x04, 0x01, 0x00, 0x03, 0x01, 0x00}, // binary.wast:1120  function after table
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x05, 0x01, 0x00, 0x04, 0x01, 0x00}, // binary.wast:1130  table after memory
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x06, 0x01, 0x00, 0x05, 0x01, 0x00}, // binary.wast:1140  memory after global
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x07, 0x01, 0x00, 0x06, 0x01, 0x00}, // binary.wast:1150  global after export

		// The pair that a rank-by-id decoder would wrongly accept: data count
		// is id 12 but ranks *before* code (id 10).
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0A, 0x01, 0x00, 0x0C, 0x01, 0x01}, // binary.wast:1194  data count after code
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0B, 0x01, 0x00, 0x0A, 0x01, 0x00}, // binary.wast:1204  code after data
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0C, 0x01, 0x01, 0x09, 0x01, 0x00}, // binary.wast:1184  element after data count
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrTrailingData) {
			t.Errorf("DecodeModule(%x): got %v, want ErrTrailingData", in, err)
		}
	}
}

// TestSectionOrderAcceptsValid is the other half of the predicate, and the half
// that a strictness bug would break silently: increasing ranks must be accepted,
// custom sections must be legal anywhere and repeatable, and the data-count
// section must be accepted in its grammar position (before code) even though its
// id is numerically the highest of the three.
func TestSectionOrderAcceptsValid(t *testing.T) {
	for name, in := range map[string][]byte{
		// synthetic: constructed to exercise the accept path, which the suite's
		// assert_malformed vectors cannot cover by construction.
		"type then memory": {
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x01, 0x01, 0x00, 0x05, 0x01, 0x00,
		},
		"custom between, and repeated": {
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x00, 0x01, 0x00, // custom
			0x01, 0x01, 0x00, // type
			0x00, 0x01, 0x00, // custom again
			0x00, 0x01, 0x00, // and again
			0x05, 0x01, 0x00, // memory
			0x00, 0x01, 0x00, // trailing custom
		},
		"custom sections only": {
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x00, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x01, 0x00,
		},
		"data count before code, ids out of numeric order": {
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x0C, 0x01, 0x00, // data count: 0 segments
			0x0A, 0x01, 0x00, // code: 0 bodies
		},
	} {
		if _, err := DecodeModule(in); err != nil {
			t.Errorf("%s: DecodeModule(%x) = %v, want accept", name, in, err)
		}
	}
}

// TestMalformedSectionID pins the ids with no place in the grammar. The lookup
// that ranks a section is the lookup that validates it, so this and
// TestSectionOrder are two questions answered by one table.
func TestMalformedSectionID(t *testing.T) {
	for _, in := range [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0E, 0x01, 0x00},                   // binary.wast:48  id 14
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x7F, 0x01, 0x00},                   // binary.wast:49  id 127
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x80, 0x01, 0x00, 0x01, 0x01, 0x00}, // binary.wast:50  id 128 (multi-byte)
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrMalformedSectionID) {
			t.Errorf("DecodeModule(%x): got %v, want ErrMalformedSectionID", in, err)
		}
	}
	// The tag section (id 13) is ranked, not rejected as a malformed id — but with
	// the EH gate off it is still rejected, by the gate. This assertion used to
	// require *acceptance*, which was wrong in the accept-and-ignore direction:
	// a gate-off engine that decoded a tag section's neighbours and shrugged at the
	// tag would silently change the module's semantics. See
	// TestTagSectionIsWellFormedButGated (sections_test.go) for both gate states.
	tagged := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0D, 0x01, 0x00} // synthetic: no suite vector asserts a verdict for id 13 alone
	if _, err := DecodeModule(tagged); errors.Is(err, ErrMalformedSectionID) {
		t.Error("tag section reported as a malformed id: Wasm 3.0 defines id 13, so it is well-formed in the tracked union and the gate must not redraw the grammar")
	}
}

// TestCrossSectionCounts pins the function/code and data-count agreements. The
// "one present, one absent" vectors are the reason the check treats a missing
// section as zero rather than skipping the comparison: binary.wast:209 has a
// function section and no code section, and 219 the reverse.
func TestCrossSectionCounts(t *testing.T) {
	funcCode := [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00, 0x03, 0x03, 0x02, 0x00, 0x00},                                                 // binary.wast:209  2 funcs, no code
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0A, 0x04, 0x01, 0x02, 0x00, 0x0B},                                                                               // binary.wast:219  1 body, no function
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00, 0x03, 0x03, 0x02, 0x00, 0x00, 0x0A, 0x04, 0x01, 0x02, 0x00, 0x0B},             // binary.wast:228  2 vs 1
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00, 0x03, 0x02, 0x01, 0x00, 0x0A, 0x07, 0x02, 0x02, 0x00, 0x0B, 0x02, 0x00, 0x0B}, // binary.wast:239  1 vs 2
	}
	for _, in := range funcCode {
		if _, err := DecodeModule(in); !errors.Is(err, ErrFuncCodeMismatch) {
			t.Errorf("DecodeModule(%x): got %v, want ErrFuncCodeMismatch", in, err)
		}
	}

	dataCount := [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0C, 0x01, 0x03, 0x0B, 0x05, 0x02, 0x01, 0x00, 0x01, 0x00}, // binary.wast:262  declares 3, has 2
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x0C, 0x01, 0x01, 0x0B, 0x05, 0x02, 0x01, 0x00, 0x01, 0x00}, // binary.wast:274  declares 1, has 2
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01, 0x0C, 0x01, 0x01},             // binary.wast:286  declares 1, no data section
	}
	for _, in := range dataCount {
		if _, err := DecodeModule(in); !errors.Is(err, ErrDataCountMismatch) {
			t.Errorf("DecodeModule(%x): got %v, want ErrDataCountMismatch", in, err)
		}
	}
}

// TestSectionRankTableIsAPermutation is a property test on the table itself,
// rather than on any vector it decides. It is here because the table is
// hand-written data that looks like it could be derived from SectionID order —
// and is not. A future editor "tidying" it into id order breaks exactly one pair
// (data count / code), which is one failing suite vector: easy to miss, easy to
// dismiss. This states the invariants directly.
func TestSectionRankTableIsAPermutation(t *testing.T) {
	if _, ok := sectionRank[SectionCustom]; ok {
		t.Error("custom section must not have a rank: it is legal anywhere, any number of times")
	}
	seen := map[int]SectionID{}
	for id, rank := range sectionRank {
		if prev, dup := seen[rank]; dup {
			t.Errorf("rank %d assigned to both %s and %s; ranks must be unique or the order is ambiguous", rank, prev, id)
		}
		seen[rank] = id
	}
	if len(seen) != len(sectionRank) {
		t.Fatalf("ranks are not distinct: %d ranks for %d sections", len(seen), len(sectionRank))
	}
	// The divergence from id order, asserted rather than assumed.
	if sectionRank[SectionDataCount] >= sectionRank[SectionCode] {
		t.Error("data count must rank before code (binary.wast:1194), even though its id is higher")
	}
	if SectionDataCount <= SectionCode {
		t.Error("this test's premise is gone: data count's id is no longer above code's, so the table no longer diverges from id order and its comment needs revisiting")
	}
}

func TestLEB128(t *testing.T) {
	cases := []struct {
		in   []byte
		want uint32
		err  error
	}{
		{[]byte{0x00}, 0, nil},
		{[]byte{0x7F}, 127, nil},
		{[]byte{0x80, 0x01}, 128, nil},
		{[]byte{0xE5, 0x8E, 0x26}, 624485, nil},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}, 0xFFFFFFFF, nil},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x1F}, 0, ErrLEBOverflow},
		{[]byte{0x80}, 0, ErrTruncated},
		// See TestLEBTaxonomy for the full width-parameterized battery (0003).
		{[]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, 0, ErrLEBTooLong},
	}
	for _, c := range cases {
		r := &reader{b: c.in}
		got, err := r.u32()
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("u32(%x): got err %v, want %v", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("u32(%x) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}
