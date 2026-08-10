package binary

import "testing"

// TestDecodeTryTableRetainsCatchClauses is #199's rung-1 implementation control, on
// TestDecodeRefTypeRetainsGCShapedValues' own style (hand-built wire bytes, DecodeModule,
// assert on the decoded Module): the whole point of building the `Catches` side table is that
// `try_table`'s handler-clause vector stops being read-and-discarded, and this asserts it is
// actually retained rather than merely that the module still decodes.
//
// The image: a type section (one `() -> ()` func type), an import section (three tag imports,
// so tag indices 0/1/2 are distinguishable from each other and from any label — exercising the
// accept path for import kind 4 without needing #95's still-open tag-section payload grammar), a
// function section (one function of that type), and a code section whose one body is two nested
// blocks around a `try_table` carrying all four catch-clause kinds.
//
// **Every tag-bearing clause uses a tag index that differs from its label index** — `catch`
// reads tag=2 label=0, `catch_ref` reads tag=1 label=0 — so a reader that swapped which u32 is
// the tag and which is the label (decodeCatch's two-index arm, the shape grave #100's lane-index
// finding warns is exactly where an extra or reordered value goes unnoticed) produces `{Tag:0,
// Label:2}`/`{Tag:0, Label:1}` instead, which the assertions below catch on the TagIndex field
// alone. Built by hand from decode.ml's grammar (:412-417, :975-981) rather than from this
// package's own encoder, so the test does not confirm the code against itself.
func TestDecodeTryTableRetainsCatchClauses(t *testing.T) {
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic, version
		// type section: id 1, size 4: vec(1) { func () -> () }
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		// import section: id 2, size 22: vec(3) tag imports "m"/"a", "m"/"b", "m"/"c"
		0x02, 0x16, 0x03,
		0x01, 0x6d, 0x01, 0x61, 0x04, 0x00, 0x00,
		0x01, 0x6d, 0x01, 0x62, 0x04, 0x00, 0x00,
		0x01, 0x6d, 0x01, 0x63, 0x04, 0x00, 0x00,
		// function section: id 3, size 2: vec(1) { typeidx 0 }
		0x03, 0x02, 0x01, 0x00,
		// code section: id 10, size 0x18: vec(1) { body }
		0x0a, 0x18, 0x01, 0x16, 0x00,
		// body: locals=0, then:
		//   block $outer (0x02 0x40)
		//     block $inner (0x02 0x40)
		//       try_table (0x1f 0x40), 4 clauses:
		0x02, 0x40,
		0x02, 0x40,
		0x1f, 0x40,
		0x04,             // vec count: 4 clauses
		0x00, 0x02, 0x00, // catch tag=2 label=0 (branches to $inner)
		0x01, 0x01, 0x00, // catch_ref tag=1 label=0 (branches to $inner)
		0x02, 0x01, // catch_all label=1 (branches to $outer)
		0x03, 0x00, // catch_all_ref label=0
		0x0b, // end of try_table
		0x0b, // end of inner block
		0x0b, // end of outer block
		0x0b, // end of function
	}

	m, err := (&Decoder{Features: Features{ExceptionHandling: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("DecodeModule: got %v, want accept", err)
	}
	if len(m.Funcs) != 1 {
		t.Fatalf("got %d funcs, want 1", len(m.Funcs))
	}
	f := m.Funcs[0]
	// Body is [outer-block, inner-block, try_table, end(try_table), end(inner), end(outer),
	// end(func)] — try_table is index 2.
	const tryTableIdx = 2
	got, ok := f.CatchVector(tryTableIdx)
	if !ok {
		t.Fatalf("CatchVector(%d) reports absent; want a retained vector for the try_table "+
			"(Body=%+v)", tryTableIdx, f.Body)
	}
	want := []Catch{
		{Kind: CatchTag, TagIndex: 2, LabelIndex: 0},
		{Kind: CatchTagRef, TagIndex: 1, LabelIndex: 0},
		{Kind: CatchAny, LabelIndex: 1},
		{Kind: CatchAnyRef, LabelIndex: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d clauses, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("clause %d = %+v, want %+v", i, got[i], w)
		}
	}

	// The two-result form is the whole point (LabelVector's own comment, mirrored here): an
	// instruction that is not a try_table must report "absent", not an empty slice — the block
	// opener at Body[0] is exactly that instruction.
	if v, ok := f.CatchVector(0); ok {
		t.Errorf("CatchVector(0) = %+v, ok=true; want ok=false for a non-try_table instruction", v)
	}
}

// TestDecodeTryTableRetainsEmptyCatchVector pins the other half of the two-result contract: a
// `try_table` with zero clauses is legal (decode.ml's `vec (at catch) s` accepts a zero-length
// vector) and means every exception falls through uncaught — so it must retain as an *empty,
// present* vector, not as absent.
func TestDecodeTryTableRetainsEmptyCatchVector(t *testing.T) {
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		// code: body is try_table with zero clauses, end, end
		0x0a, 0x08, 0x01, 0x06, 0x00,
		0x1f, 0x40, 0x00, // try_table, blocktype 0x40, 0 clauses
		0x0b, // end of try_table
		0x0b, // end of function
	}
	m, err := (&Decoder{Features: Features{ExceptionHandling: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("DecodeModule: got %v, want accept", err)
	}
	if len(m.Funcs) != 1 {
		t.Fatalf("got %d funcs, want 1", len(m.Funcs))
	}
	got, ok := m.Funcs[0].CatchVector(0)
	if !ok {
		t.Fatalf("CatchVector(0) reports absent; want present-and-empty for a zero-clause try_table")
	}
	if len(got) != 0 {
		t.Errorf("got %d clauses, want 0: %+v", len(got), got)
	}
}
