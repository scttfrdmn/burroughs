package binary

import (
	"errors"
	"reflect"
	"testing"
)

// Tests for the const-expression grammars (#25).
//
// Vectors are cited to binary.wast and checked by TestFixtureProvenance
// (internal/spec), or marked synthetic with a reason.

// constExprErr is `decodeConstExpr` reporting only whether the expression read, with the
// expression itself discarded.
//
// A *test-side* helper, because the engine no longer has such a caller: the non-retaining twin
// and the `keep bool` it was spelled with went with #419's table initializer, discharging the
// condition `decodeConstExprKeep`'s own doc pre-registered ("collapse the two when nothing
// wants the recognize-only form"). The discard is these controls' actual question — every one
// below asks what verdict a byte sequence gets, none asks what it decodes to — so naming it
// once is what keeps ten call sites from each spelling a `_, _,` that reads like an oversight.
func constExprErr(d *Decoder, r *reader) error {
	_, _, err := d.decodeConstExpr(r)
	return err
}

// TestConstExprExtentIsDiscovered pins that the reader finds an expression's end by
// reading instructions, not by trusting a length — and that each opcode's immediate
// width is right.
//
// The immediate widths are the part that fails quietly. A wrong width does not
// produce a wrong answer at the const-expr; it shifts every byte after it, so the
// failure surfaces as a size mismatch or a bogus opcode somewhere downstream. That
// makes the correct-width case worth asserting directly rather than only through the
// suite, which is #33's property 2 for the same reason.
func TestConstExprExtentIsDiscovered(t *testing.T) {
	d := &Decoder{}
	// synthetic: exercises each const-legal opcode's immediate width at the reader
	// seam. The suite's vectors only ever use i32.const, global.get and ref.func, so
	// the other four widths have no vector — which is precisely why they are asserted
	// here rather than trusted.
	for _, tc := range []struct {
		name string
		in   []byte
		want int // bytes consumed, including the END
	}{
		{"i32.const 0", []byte{0x41, 0x00, 0x0B}, 3},
		{"i32.const -1 (sleb, one byte)", []byte{0x41, 0x7F, 0x0B}, 3},
		{"i32.const 0x100 (two-byte sleb)", []byte{0x41, 0x80, 0x02, 0x0B}, 4},
		{"i64.const 0", []byte{0x42, 0x00, 0x0B}, 3},
		{"f32.const (4 raw bytes)", []byte{0x43, 0x00, 0x00, 0x80, 0x3F, 0x0B}, 6},
		{"f64.const (8 raw bytes)", []byte{0x44, 0, 0, 0, 0, 0, 0, 0xF0, 0x3F, 0x0B}, 10},
		{"global.get 0", []byte{0x23, 0x00, 0x0B}, 3},
		{"ref.null funcref", []byte{0xD0, 0x70, 0x0B}, 3},
		{"ref.func 0", []byte{0xD2, 0x00, 0x0B}, 3},
		{"bare end", []byte{0x0B}, 1},
		// Multiple instructions: the extent is the whole sequence, and the reader
		// must not stop at the first one.
		{"two consts then end", []byte{0x41, 0x01, 0x41, 0x02, 0x0B}, 5},
	} {
		// A trailing sentinel byte no const-expr would consume, so "consumed exactly
		// want" is distinguishable from "consumed everything available".
		r := &reader{b: append(append([]byte{}, tc.in...), 0xFF), eof: ErrPayloadEnd}
		if err := constExprErr(d, r); err != nil {
			t.Errorf("%s: got %v, want accept", tc.name, err)
			continue
		}
		if r.off != tc.want {
			t.Errorf("%s: consumed %d bytes, want %d — a wrong immediate width shifts every byte after it", tc.name, r.off, tc.want)
		}
	}
}

// TestConstExprSeparatesMalformedFromInvalid is the same four bytes as before and the
// opposite assertion, which is what the dissolution changed (#43/#39).
//
// This test used to be named ...RejectsWithoutSpoofingASpecString and asserted the
// error was **neither** "illegal opcode" nor "constant expression required", on the
// stated grounds that the reader could not tell a nonexistent opcode from a
// real-but-non-constant one. That was an accurate statement about a reader holding an
// eight-entry accept set: claiming either string would have been a verdict it had not
// computed, and the cheap version — ErrIllegalOpcode for everything — buys
// binary.wast:345 and is wrong in general (§9 G-3).
//
// The authority-derived table computes the distinction, so the same four bytes now
// partition, and each half is asserted rather than jointly excluded:
//
//   - 0x0A (throw_ref), 0x92 (f32.add), 0x01 (nop) exist in the reference's table and
//     are simply not constant. *Invalid*, and calling them malformed would slander a
//     module the spec calls well-formed.
//   - 0xF3 has no arm at all: the catch-all's territory, so genuinely *malformed*.
//
// The inversion is the point. Keeping the old assertion would have kept a green that
// certifies the absence of a verdict the engine now has — a control outliving the
// uncertainty it was written for.
func TestConstExprSeparatesMalformedFromInvalid(t *testing.T) {
	// Every gate on: throw_ref is *also* exception handling's, and with the gates off the
	// feature decline outranks the const verdict (0008), which would answer a different
	// question than this test asks. See constVerdictDecoder.
	d := constVerdictDecoder(t)
	for _, tc := range []struct {
		name string
		b    byte
		want error
		// deny is the string the *other* half of the partition would produce. Asserted
		// per case rather than as a shared exclusion list, because the old test's
		// exclusion of both is exactly the vagueness this replaces.
		deny string
	}{
		{"throw_ref (real opcode, not const) — binary.wast:112's byte", 0x0A, ErrConstExprRequired, "illegal opcode"},
		{"f32.add (real opcode, not const)", 0x92, ErrConstExprRequired, "illegal opcode"},
		{"nop (real opcode, not const; global.wast:313 calls it invalid)", 0x01, ErrConstExprRequired, "illegal opcode"},
		{"0xf3 (no such opcode) — binary.wast:345's byte", 0xF3, ErrIllegalOpcode, "constant expression required"},
	} {
		r := &reader{b: []byte{tc.b, 0x0B}, eof: ErrPayloadEnd}
		err := constExprErr(d, r)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
			continue
		}
		// Substring, and deliberately stronger than the harness's own rule (prefix,
		// ADR 0045): the claim is that the other half's text appears *nowhere*, which
		// a prefix test cannot make.
		if contains(err.Error(), tc.deny) {
			t.Errorf("%s: error %q contains %q — the other half of the partition, so the "+
				"table's existence verdict was not consulted", tc.name, err, tc.deny)
		}
		// An invalid verdict must never wear a malformed string: that is the direction
		// no assert_malformed vector can catch, since it concerns modules the spec
		// accepts as well-formed.
		// errors.Is on the *expectation*, not ==: the table's want is a bare sentinel
		// today, and a `==` here would silently stop selecting this branch the moment one
		// of them is wrapped at its declaration. The check would then pass by not running
		// — a skip one step quieter, in a control whose whole subject is that an invalid
		// verdict must not wear a malformed string.
		if errors.Is(tc.want, ErrConstExprRequired) && contains(err.Error(), "malformed") {
			t.Errorf("%s: error %q says malformed for a well-formed module", tc.name, err)
		}
	}
}

// TestConstExprDefersTheConstVerdict is the control on the layering binary.wast:112
// forced, and it is the reason the const check records rather than returns.
//
// The vector: a global initialiser `\41\00` with no END, followed by the code section's
// id byte `\0a` — which *is* an opcode, throw_ref. An aborting reader stops at the
// non-const instruction and reports the const violation. The reference reads on, and
// the expression runs off the end of the image, so the verdict is the *malformed* one.
//
// derived from binary.wast:117,119 — the two byte lines of the vector whose
// assert_malformed opens at :112. :117 is the global section `\06\05\01\7f\00\41\00`,
// whose initialiser ends `\41\00` with no END, and :119 is the code section
// `\0a\04\01\02\00\0b` that follows it in the image. Jointly they say the byte after the
// unterminated expression is `\0a` — throw_ref — and the suite's expected string for the
// pair is `unexpected end of section or function`, not a const-expression verdict.
//
// The inference: since `const s = at instr_block s; end_ s` (decode.ml:983) contains no
// const check, a truncation encountered *after* a non-const instruction is still a
// truncation. The vectors below are the reader seam rather than the whole module, which
// is why this is derived and not cited — the suite implies the reader's behaviour without
// containing an expression-level vector for it.
func TestConstExprDefersTheConstVerdict(t *testing.T) {
	// Every gate on — throw_ref is exception handling's, and this test's subject is the
	// *const* deferral. The gate deferral's own ordering is TestGateDeclineYieldsToMalformed.
	d := constVerdictDecoder(t)

	// Non-const instruction, then the image ends before any END. Malformed wins.
	r := &reader{b: []byte{0x0A}, eof: ErrPayloadEnd}
	if err := constExprErr(d, r); !errors.Is(err, ErrPayloadEnd) {
		t.Errorf("non-const then truncation: got %v, want ErrPayloadEnd — an invalid verdict "+
			"that pre-empts a malformed one is reporting the wrong layer's answer", err)
	}

	// The same non-const instruction with a well-formed expression around it. Now the
	// grammar completes, so the deferred verdict is released.
	r = &reader{b: []byte{0x0A, 0x0B}, eof: ErrPayloadEnd}
	if err := constExprErr(d, r); !errors.Is(err, ErrConstExprRequired) {
		t.Errorf("non-const then END: got %v, want ErrConstExprRequired — the deferred verdict "+
			"must actually be released, or deferring it is just dropping it", err)
	}

	// And the *first* non-const instruction is the one reported, which is what a
	// validator reading left to right would say. 0x01 is nop, 0x92 is f32.add.
	r = &reader{b: []byte{0x01, 0x92, 0x0B}, eof: ErrPayloadEnd}
	err := constExprErr(d, r)
	if !errors.Is(err, ErrConstExprRequired) {
		t.Fatalf("two non-const instructions: got %v, want ErrConstExprRequired", err)
	}
	if !contains(err.Error(), "0x01") {
		t.Errorf("two non-const instructions: error %q does not name the first one (0x01 nop); "+
			"reporting the last is a different claim about the module", err)
	}
}

// TestDataSegmentContentsAreNotAName is the module-level form of
// TestByteVecIsNotAName, and it exists because grave #32 could not have it.
//
// That grave was exactly this test: a module carrying a `\ff\fe\80` data segment,
// asserting the decoder accepts it. It passed while the defect it named — the UTF-8
// predicate pushed down into byteVec — was present, because the data section was not
// descended into, so no byteVec was ever reached. The fix at the time was to test the
// reader seam instead.
//
// Now the data section *is* descended into, so the module-level claim is finally
// checkable, and it is the stronger one: it asserts the whole path from image to
// segment contents, which is where a future refactor would reintroduce the bug. Both
// forms are kept — the seam test states the distinction, this one states that the
// production path honours it.
//
// Falsified before trusted, per the discipline: pushing utf8.Valid into byteVecErr
// makes this fail with ErrMalformedUTF8, which is what the earlier version could not
// do.
func TestDataSegmentContentsAreNotAName(t *testing.T) {
	// synthetic: a minimal module whose data segment carries bytes no UTF-8 decoder
	// accepts. Derived from binary.wast:877's shape (memory section, then an active
	// data segment) with valid contents replaced by invalid UTF-8 — the suite has no
	// such vector, which is the reason to write one.
	img := []byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01, // memory section: 1 memory, no max, min 1
		0x0B, 0x09, 0x01, // data section, 9 bytes, 1 segment
		0x00, 0x41, 0x00, 0x0B, // active, offset i32.const 0, end
		0x03, 0xFF, 0xFE, 0x80, // 3 content bytes: invalid UTF-8
	}
	if _, err := DecodeModule(img); err != nil {
		t.Errorf("data segment with non-UTF-8 contents: got %v, want accept — contents are vec(byte), and the spec places no encoding constraint on them", err)
	}
}

// TestElemSegmentFlagFields pins that the flag *bits* are decoded rather than the
// seven legal values being memorised.
//
// binary.wast:345 and :373 are the same segment form (flags 5: passive, element
// expressions) failing in two different fields — one an opcode, one a reftype. A
// decoder that guessed the field layout could still get one of them right, so both
// are asserted, along with the forms that carry a table index and an elemkind byte.
//
// **Each accepting row now also states what was retained** (0016), because a verdict alone
// cannot see the retention: every one of these rows passed identically when all five fields
// were read and dropped, which is precisely why `discardIndex`'s replacement is invisible to
// the rejection corpus. The `want` column is where a mode misclassified, a table index read
// from the wrong offset, or an element vector filed under the wrong form becomes visible.
func TestElemSegmentFlagFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		ok   bool
		want *ElemSegment // asserted when ok; nil skips the retention check
	}{
		// Every flag form the suite encodes, transcribed from elem.wast rather than
		// reasoned about — this table *is* the derivation of the type-field presence
		// rule, and two plausible wrong rules each pass a subset of these rows.
		//
		// These citations name a *fragment*: one source line inside a `(module binary
		// ...)`, not a module image, because the unit under test is the segment
		// grammar. TestFixtureProvenance checks them against the `"\hh"` escapes on
		// that line — and earned its keep immediately, since two of the seven were
		// several lines off when first written. A transcription is the hazard the file
		// exists for; declaring these unverifiable would have been the wrong repair.
		// The `want` values are **printed, not reasoned**: each was read off the decoder's
		// actual output before being written here, which is how `Offset` came to include its
		// terminating END rather than stopping before it. A hand-reasoned expectation would
		// have omitted the `{Op: opEnd}` and the row would have been wrong about the engine
		// while looking right about the format.
		//
		// **`ElemType` is the one column printing could not certify, and grave #360 is the
		// price.** Every row here said `FuncRef` and five of them were wrong: the reference gives
		// the four index forms `(NoNull, FuncHT)` — flag 0's own literal (decode.ml:1163) and
		// `elem_kind`'s only value (decode.ml:1154-1157) — and reserves `(Null, FuncHT)` for flag
		// 4 (decode.ml:1183). Printing agreed with the decoder because the decoder is what
		// printed. Nothing in this file could have caught it and nothing in the rejection corpus
		// could either, because nullability is only ever read by a subtype check and there was no
		// subtype check on an element segment's type until #328's `check_elemmode` port. This is
		// the accept-direction hole §9 G-3 names, in a decoder rather than a validator: these
		// five columns are now read off decode.ml as a decision table, and the flag-4 row below
		// is the one that keeps the split from collapsing back into a constant.
		{
			"flags 0: active table 0, func indices", []byte{0x00, 0x41, 0x00, 0x0B, 0x01, 0x00}, true, // elem.wast:259
			&ElemSegment{Mode: ElemActive, ElemType: elemKindRefFunc, Offset: i32ConstZero, Funcs: []uint32{0}},
		},
		{
			"flags 1: passive, elemkind, func indices", []byte{0x01, 0x00, 0x01, 0x00}, true, // elem.wast:276
			&ElemSegment{Mode: ElemPassive, ElemType: elemKindRefFunc, Funcs: []uint32{0}},
		},
		{
			"flags 2: active explicit table, elemkind", []byte{0x02, 0x00, 0x41, 0x00, 0x0B, 0x00, 0x01, 0x00}, true, // elem.wast:293
			&ElemSegment{Mode: ElemActive, ElemType: elemKindRefFunc, Offset: i32ConstZero, Funcs: []uint32{0}},
		},
		// derived from elem.wast:293 — the suite's flags-2 vector names **table 0**, which is
		// also what every implicit-index form defaults to, so it cannot distinguish a decoder
		// that reads the index from one that skips the field and leaves the zero value. The
		// premise is that :293's shape is the flags-2 encoding; the inference is that changing
		// its index byte changes only TableIndex. That makes this the row where a table index
		// read from the wrong offset fails, and it is the pair `MemIndex` already has next door.
		{
			"flags 2 with a nonzero table index",
			[]byte{0x02, 0x03, 0x41, 0x00, 0x0B, 0x00, 0x01, 0x00},
			true,
			&ElemSegment{Mode: ElemActive, TableIndex: 3, ElemType: elemKindRefFunc, Offset: i32ConstZero, Funcs: []uint32{0}},
		},
		{
			"flags 3: declarative, elemkind", []byte{0x03, 0x00, 0x01, 0x00}, true, // elem.wast:310
			&ElemSegment{Mode: ElemDeclarative, ElemType: elemKindRefFunc, Funcs: []uint32{0}},
		},
		// flags 4 carries NO type field — the row that kills `flags != 0`. It is also the
		// retention half of ElemSegment's argument: the same segment as flags 0 above, same
		// table, same single function, and the two must not decode to the same value.
		//
		// **It is now the row that discriminates the type default too**, which is grave #360's
		// tripwire and costs nothing extra: flags 0 and 4 are the two forms with no type field on
		// the wire, and they default *differently* — `(ref func)` above and `funcref` here. A
		// decoder that went back to one constant for both would fail this row or the flags-0 one,
		// whichever way it picked, where before #360 either choice was green.
		{
			"flags 4: active table 0, element exprs, no type byte", []byte{0x04, 0x41, 0x00, 0x0B, 0x01, 0xD2, 0x00, 0x0B}, true, // elem.wast:327
			&ElemSegment{
				Mode: ElemActive, ElemType: FuncRef, Offset: i32ConstZero, ByExpr: true,
				Exprs: [][]Instr{{{Op: 0xD2}, {Op: opEnd}}}, // ref.func 0, end
				// **One nil entry, not an empty slice**, and the two rows carrying this are where
				// the index-parallelism invariant is asserted rather than described: an expression
				// holding no `ref.null` still occupies its position in `ExprCasts`, because the
				// only thing that makes `ExprCasts[j]` mean expression *j* is that every j has an
				// entry. A build that appended only where a cast was filed would pass a
				// single-element fixture and misattribute every segment with two.
				ExprCasts: []map[int][]ValType{nil},
			},
		},
		// flags 5 carries one — the row that kills `flags&explicit != 0`.
		{
			"flags 5: passive, reftype, element exprs", []byte{0x05, 0x70, 0x01, 0xD2, 0x00, 0x0B}, true, // elem.wast:360
			&ElemSegment{
				Mode: ElemPassive, ElemType: FuncRef, ByExpr: true,
				Exprs:     [][]Instr{{{Op: 0xD2}, {Op: opEnd}}},
				ExprCasts: []map[int][]ValType{nil},
			},
		},
		// `ref.null`'s heap type is **not** in the retained instruction, and this row is where
		// that is stated rather than discovered later: `Imm0` is 0, not `FuncRef`. The want was
		// first written as `Imm0: uint64(FuncRef)` by reasoning from the immediate's name, and
		// the print said otherwise — `immHeapType` reads the heaptype and stages no word
		// (instr.go's arm ends `return c.d.decodeHeapType(r)`), so `ref.null func` and
		// `ref.null extern` were the same two words. **That gap is #361 and it is closed** — the
		// heaptype is filed in `ExprCasts` now, which is what the third field below asserts, and
		// the row is the only one here whose `ExprCasts` is non-empty. The paragraph this
		// replaces said the retention was declined "because nothing in this PR's path consumes a
		// null's type"; #328's const-expression typing is that consumer, and a `funcref` table's
		// nulls being funcref nulls stopped being the whole story when
		// `(global externref (ref.null func))` became a module something had to refuse.
		{
			"flags 5 with ref.null funcref", []byte{0x05, 0x70, 0x01, 0xD0, 0x70, 0x0B}, true, // elem.wast:376
			&ElemSegment{
				Mode: ElemPassive, ElemType: FuncRef, ByExpr: true,
				Exprs:     [][]Instr{{{Op: 0xD0}, {Op: opEnd}}},
				ExprCasts: []map[int][]ValType{{0: {refNull(FuncRef, true)}}},
			},
		},
		// binary.wast:373 — flags 5 with a non-reftype where the reftype goes.
		{"flags 5 with valtype i32 as the reftype", []byte{0x05, 0x7F, 0x01, 0xD2, 0x00, 0x0B}, false, nil},
		{"flags 8 is not a legal flag value", []byte{0x08}, false, nil},
		// The row that rejects **inside** the element vector, and the only one that can:
		// every other rejecting row here fails at the flags byte or the reftype, before the
		// vector read exists to be interrupted. Without it the "retained 0 segments after
		// rejecting" assertion below is *stillborn* — an append moved above the vector read
		// leaves the check green, which is what happened when the falsification was run.
		//
		// derived from binary.wast:359,360,361 — the suite writes this segment across three
		// source lines (`"\05\70"`, `"\01"`, `"\f3\00\0b"`, the last commented "bad opcode,
		// index 0, end"), and a fragment citation resolves to *one* line by construction, so
		// no single citation can verify a six-byte fixture. The premises are those three
		// lines; the inference is that a `(module binary ...)` concatenates its string
		// literals, which is the same entailment the module-image path already relies on.
		// Marking it `synthetic` would have been the easier lie: these bytes are transcribed,
		// and transcription is the hazard the provenance file exists for.
		{"flags 5, one element, illegal opcode", []byte{0x05, 0x70, 0x01, 0xF3, 0x00, 0x0B}, false, nil},
	} {
		d := &Decoder{}
		r := &reader{b: tc.in, eof: ErrPayloadEnd}
		err := d.decodeElemSegment(r)
		if tc.ok && err != nil {
			t.Errorf("%s: got %v, want accept", tc.name, err)
			continue
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("%s: accepted, want rejection", tc.name)
			}
			// A rejected segment must not be *filed*. Appending before the vector read would
			// leave a half-decoded segment in the module's index order, which shifts every
			// later `table.init` index — an accept-direction consequence of a reject-direction
			// bug, and nothing else here would see it.
			if got := len(d.mod().Elems); got != 0 {
				t.Errorf("%s: retained %d segments after rejecting; want 0", tc.name, got)
			}
			continue
		}
		if tc.want == nil {
			t.Errorf("%s: accepting row with no want — every accepted segment states what it retained", tc.name)
			continue
		}
		if len(d.mod().Elems) != 1 {
			t.Errorf("%s: retained %d segments, want exactly 1", tc.name, len(d.mod().Elems))
			continue
		}
		if got := d.mod().Elems[0]; !reflect.DeepEqual(got, *tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, *tc.want)
		}
	}
}

// i32ConstZero is the offset expression every active row above carries: `i32.const 0` then
// END. Shared because it is the same six-byte prefix in the suite's vectors, and written as
// two instructions because that is what the decoder produced when printed — the END is part
// of the retained expression, exactly as it is for a global's initializer.
var i32ConstZero = []Instr{{Op: 0x41}, {Op: opEnd}}

// elemKindRefFunc is `(NoNull, FuncHT)` — `(ref func)`, the element type of every index-form
// element segment (decode.ml:1154-1157 for `elem_kind`, :1163 for flag 0's own literal).
//
// Spelled here rather than as `FuncRef` because the difference between the two is one field and the
// whole of grave #360, and named after the wire production rather than after the type, so a reader
// checking a row against decode.ml is comparing the same words the reference uses. It is
// deliberately *not* `binary.FuncRef.WithNull(false)` written inline at five call sites: five
// literals are five chances to fix four of them.
var elemKindRefFunc = refNull(FuncRef, false)

// TestSlebIsNotUlebWithACast pins the signed LEB reader's two-sided range check.
//
// This is grave 0003's lesson in the signed case: the malformed taxonomy is
// width-parameterized, and the signed check differs from the unsigned one in *both*
// directions. `\7f` is -1 at width 32, not 127; and the last byte's out-of-width bits
// must all match the sign rather than all be zero, so a reader that reused the
// unsigned rule would reject valid negatives while accepting binary.wast:125's
// over-wide value.
func TestSlebIsNotUlebWithACast(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		want int32
	}{
		{"0", []byte{0x00}, 0},
		{"1", []byte{0x01}, 1},
		{"-1 — the byte a cast from uleb would read as 127", []byte{0x7F}, -1},
		{"-2", []byte{0x7E}, -2},
		{"63", []byte{0x3F}, 63},
		{"-64", []byte{0x40}, -64},
		{"64 needs two bytes because 0x40 is the sign bit", []byte{0xC0, 0x00}, 64},
		{"-65", []byte{0xBF, 0x7F}, -65},
		{"min int32", []byte{0x80, 0x80, 0x80, 0x80, 0x78}, -2147483648},
		{"max int32", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x07}, 2147483647},
	} {
		r := &reader{b: tc.in}
		got, err := r.s32()
		if err != nil {
			t.Errorf("s32(% x) [%s]: got %v, want %d", tc.in, tc.name, err, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("s32(% x) [%s]: got %d, want %d", tc.in, tc.name, got, tc.want)
		}
		if r.off != len(tc.in) {
			t.Errorf("s32(% x) [%s]: consumed %d of %d bytes", tc.in, tc.name, r.off, len(tc.in))
		}
	}

	// The two malformed classes stay distinct, and the order is the reference's:
	// range *before* the continuation bit (grave #36; see uleb's comment for sN).
	for _, tc := range []struct {
		name string
		in   []byte
		want error
	}{
		// binary.wast:125's i32 immediate: 0x10 on the fifth byte sets a bit that is
		// neither a legal positive value nor a legal sign extension at width 32.
		{"0x100000000 as i32 — binary.wast:125", []byte{0x80, 0x80, 0x80, 0x80, 0x10}, ErrLEBOverflow},
		{"continuation set on the last permitted byte", []byte{0x80, 0x80, 0x80, 0x80, 0x80}, ErrLEBTooLong},

		// GRAVE (#36): this vector arrived here from TestLEBTaxonomy, where it was read
		// with the *unsigned* reader and expected "too long". It is an i32.const
		// immediate — binary-leb128.wast:497, "i32.const -1 with one byte too many" —
		// so the signed reader is the one that answers it, and the two readers give
		// different-but-correct verdicts on these exact bytes: sN(32) says too long (a
		// legal sign extension one byte past the budget), uN(32) says too large (the
		// fifth byte's payload exceeds the width). TestLEBTaxonomy pins the unsigned
		// half; this pins the signed half. A vector asserted against the wrong reader
		// is a citation to a question nobody asked.
		{"-1 with one byte too many — binary-leb128.wast:497", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, ErrLEBTooLong},
	} {
		r := &reader{b: tc.in}
		if _, err := r.s32(); !errors.Is(err, tc.want) {
			t.Errorf("s32(% x) [%s]: got %v, want %v", tc.in, tc.name, err, tc.want)
		}
	}
}
