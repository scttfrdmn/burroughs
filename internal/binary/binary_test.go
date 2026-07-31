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
	_, err := DecodeModule([]byte{0x00, 0x61, 0x73, 0x6E, 0x01, 0x00, 0x00, 0x00})
	if !errors.Is(err, ErrBadMagic) {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
}

func TestBadVersion(t *testing.T) {
	_, err := DecodeModule([]byte{0x00, 0x61, 0x73, 0x6D, 0x02, 0x00, 0x00, 0x00})
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

// TestShortPreamble pins the truncated-vs-wrong preamble split. Vectors are
// verbatim from binary.wast: a short image is "unexpected end", a full-width
// wrong one is a magic/version failure.
func TestShortPreamble(t *testing.T) {
	truncated := [][]byte{
		{},
		{0x01},
		{0x00, 0x61, 0x73},             // "\00as"
		{0x00, 0x61, 0x73, 0x6D},       // "\00asm"
		{0x00, 0x61, 0x73, 0x6D, 0x01}, // "\00asm\01"
		{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00}, // "\00asm\01\00\00"
	}
	for _, in := range truncated {
		if _, err := DecodeModule(in); !errors.Is(err, ErrTruncated) {
			t.Errorf("DecodeModule(%x): got %v, want ErrTruncated", in, err)
		}
	}
	// Full width, wrong magic → magic header not detected.
	for _, in := range [][]byte{
		{0x61, 0x73, 0x6D, 0x00, 0x01, 0x00, 0x00, 0x00}, // "asm\00..."
		{0x77, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}, // "wasm\01..."
		{0xEF, 0xBB, 0xBF, 0x00, 0x61, 0x73, 0x6D, 0x01}, // UTF-8 BOM prefix
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrBadMagic) {
			t.Errorf("DecodeModule(%x): got %v, want ErrBadMagic", in, err)
		}
	}
	// Full width, right magic, wrong version → unknown binary version.
	for _, in := range [][]byte{
		{0x00, 0x61, 0x73, 0x6D, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x61, 0x73, 0x6D, 0x0D, 0x00, 0x00, 0x00},
		{0x00, 0x61, 0x73, 0x6D, 0x00, 0x00, 0x00, 0x01},
	} {
		if _, err := DecodeModule(in); !errors.Is(err, ErrBadVersion) {
			t.Errorf("DecodeModule(%x): got %v, want ErrBadVersion", in, err)
		}
	}
}

// TestLEBTaxonomy is the regression test for decision 0003's two graves. Every
// vector is lifted from binary-leb128.wast, with the width it appears at there,
// because the verdict depends on the width: ff ff ff ff 0f is a valid u32 and a
// malformed i32 const.
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
		{"u64 eleven bytes", 64, []byte{0x82, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, ErrLEBTooLong, 0},

		// Continuation clear, unused bits set beyond the width.
		{"u32 unused bits 0x10", 32, []byte{0x80, 0x80, 0x80, 0x80, 0x10}, ErrLEBOverflow, 0},
		{"u32 unused bits 0x40", 32, []byte{0x83, 0x80, 0x80, 0x80, 0x40}, ErrLEBOverflow, 0},
		{"u32 unused bits 0x70", 32, []byte{0x80, 0x80, 0x80, 0x80, 0x70}, ErrLEBOverflow, 0},
		{"u32 unused bits 0x1f", 32, []byte{0x80, 0x80, 0x80, 0x80, 0x1F}, ErrLEBOverflow, 0},

		// Legal at the boundary — must NOT error.
		{"u32 max", 32, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}, nil, 0xFFFFFFFF},
		{"u32 zero", 32, []byte{0x00}, nil, 0},
		{"u32 624485", 32, []byte{0xE5, 0x8E, 0x26}, nil, 624485},
		{"u64 max", 64, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}, nil, 0xFFFFFFFFFFFFFFFF},
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
