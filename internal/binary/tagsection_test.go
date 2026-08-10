package binary

import (
	"errors"
	"testing"
)

// TestDecodeTagSectionRetainsTypeIndices is #95's implementation control, on
// TestDecodeTryTableRetainsCatchClauses' own style: the whole point of `decodeTag` is that
// section 13's payload stops being accepted-and-discarded (the pre-#95 shape, `decodePayload`
// returning `false` with no grammar at all) and becomes a retained `[]Tag`.
//
// The image: a type section with **two distinct** func types — `() -> ()` and `(i32) -> ()` —
// and a tag section declaring three tags: type 1, type 0, type 1 again. Two different types
// and a repeated one, so a reader that dropped the index (retaining only a count) or that
// always retained the *last* tag's index for every entry would both be caught: the assertions
// below check each tag's TypeIndex individually rather than only the tag count.
func TestDecodeTagSectionRetainsTypeIndices(t *testing.T) {
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic, version
		// type section: id 1, size 8: vec(2) { func () -> (), func (i32) -> () }
		0x01, 0x08, 0x02, 0x60, 0x00, 0x00, 0x60, 0x01, 0x7f, 0x00,
		// tag section: id 13, size 7: vec(3) { (attr=0, type=1), (attr=0, type=0), (attr=0, type=1) }
		0x0d, 0x07, 0x03,
		0x00, 0x01,
		0x00, 0x00,
		0x00, 0x01,
	}
	m, err := (&Decoder{Features: Features{ExceptionHandling: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("DecodeModule: got %v, want accept", err)
	}
	want := []Tag{{TypeIndex: 1}, {TypeIndex: 0}, {TypeIndex: 1}}
	if len(m.Tags) != len(want) {
		t.Fatalf("got %d tags, want %d: %+v", len(m.Tags), len(want), m.Tags)
	}
	for i, w := range want {
		if m.Tags[i] != w {
			t.Errorf("tag %d = %+v, want %+v", i, m.Tags[i], w)
		}
	}
}

// TestDecodeTagSectionRejectsNonzeroAttribute pins the reject direction: `tagtype`'s leading
// byte is `zero s` (decode.ml:288), a hard requirement rather than a value this decoder happens
// not to use — the reference's own reserved-byte discipline every other attribute byte in this
// package already follows (`ErrZeroByteExpected`). No corpus vector exercises this today
// (`wat2wasm` never emits anything but zero), so this is the accept-direction's own mirror: a
// decoder that skipped the byte instead of checking it would be invisible on the whole suite.
func TestDecodeTagSectionRejectsNonzeroAttribute(t *testing.T) {
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		// tag section: id 13, size 3: vec(1) { (attr=1, type=0) } -- attr must be zero
		0x0d, 0x03, 0x01, 0x01, 0x00,
	}
	_, err := (&Decoder{Features: Features{ExceptionHandling: true}}).DecodeModule(img)
	if err == nil {
		t.Fatal("DecodeModule: got accept, want a rejection for the nonzero tag attribute byte")
	}
	if !errors.Is(err, ErrZeroByteExpected) {
		t.Errorf("got %v, want an error wrapping ErrZeroByteExpected", err)
	}
}

// TestDecodeTagImportRetainsTypeIndex is #204's implementation control: a tag *import*'s
// declared type index used to be decoded and discarded (the u32 read into `_`), leaving
// `Instance.link`'s tag-import placement (0022 §3) nothing to compare a supplier's actual tag
// type against. Retained in `Import.Index`, the same field a func import's type index already
// uses — `Import`'s own doc comment states the reason: the descriptor *is* a type index, not a
// separate structured value.
//
// Two imports of different types, so a reader retaining only the *last* import's index (the
// same failure mode TestDecodeTagSectionRetainsTypeIndices' repeated-type row guards against)
// is caught on the first.
func TestDecodeTagImportRetainsTypeIndex(t *testing.T) {
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section: id 1, size 8: vec(2) { func () -> (), func (i32) -> () }
		0x01, 0x08, 0x02, 0x60, 0x00, 0x00, 0x60, 0x01, 0x7f, 0x00,
		// import section: id 2, size 15: vec(2) tag imports "m"/"a" -> type 1, "m"/"b" -> type 0
		0x02, 0x0f, 0x02,
		0x01, 0x6d, 0x01, 0x61, 0x04, 0x00, 0x01,
		0x01, 0x6d, 0x01, 0x62, 0x04, 0x00, 0x00,
	}
	m, err := (&Decoder{Features: Features{ExceptionHandling: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("DecodeModule: got %v, want accept", err)
	}
	if len(m.Imports) != 2 {
		t.Fatalf("got %d imports, want 2: %+v", len(m.Imports), m.Imports)
	}
	want := []uint32{1, 0}
	for i, w := range want {
		if m.Imports[i].Kind != ExternTag {
			t.Errorf("import %d Kind = %v, want ExternTag", i, m.Imports[i].Kind)
		}
		if m.Imports[i].Index != w {
			t.Errorf("import %d Index = %d, want %d", i, m.Imports[i].Index, w)
		}
	}
}

// TestDecodeTagImportRejectsNonzeroAttribute is TestDecodeTagSectionRejectsNonzeroAttribute's
// own reject-direction mirror for the import form, which reads the identical attribute byte at
// a different call site and used to accept any value there silently.
func TestDecodeTagImportRejectsNonzeroAttribute(t *testing.T) {
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		// import section: id 2, size 8: vec(1) { "m"/"a" tag, attr=1, type=0 }
		0x02, 0x08, 0x01,
		0x01, 0x6d, 0x01, 0x61, 0x04, 0x01, 0x00,
	}
	_, err := (&Decoder{Features: Features{ExceptionHandling: true}}).DecodeModule(img)
	if err == nil {
		t.Fatal("DecodeModule: got accept, want a rejection for the nonzero tag-import attribute byte")
	}
	if !errors.Is(err, ErrZeroByteExpected) {
		t.Errorf("got %v, want an error wrapping ErrZeroByteExpected", err)
	}
}
