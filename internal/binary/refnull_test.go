// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package binary

import (
	"reflect"
	"testing"
)

// oneFuncImage assembles a one-function module whose body is the given instruction bytes followed by
// END, with no locals.
//
// Assembled rather than routed through the text front end for `brOnCastImage`'s reason: the subject
// is what *this* decoder retains for a heaptype byte, and the text path would ask a sibling whether
// it can spell the byte at all. The bodies are not type-correct — a bare `ref.null` leaves a value on
// the stack at END — and do not need to be: nothing in this file's extent runs the validator, which
// is the layering that keeps a decoder control a decoder control.
func oneFuncImage(instrs ...byte) []byte {
	body := append(append([]byte{0x00}, instrs...), 0x0B)
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
		0x03, 0x02, 0x01, 0x00, // function: one func, type 0
	}
	code := append([]byte{0x01, byte(len(body))}, body...)
	return append(img, append([]byte{0x0a, byte(len(code))}, code...)...)
}

// TestRefNullOpcodeMatchesTheTable is the tripwire `opRefNull`'s comment cites: a hand-written opcode
// constant beside a generated table is two places knowing one fact, and the table is the authority.
//
// **The neighbours are asserted too, and that is what makes this discriminate.** `OpMnemonic(0xD0)`
// alone passes for a constant that is simply correct; it also passes for a table that renumbered the
// family, since the assertion would then be checking the constant against whatever moved to sit under
// it. 0xD1 and 0xD2 are `ref.is_null` and `ref.func` — two rows a slipped digit lands on, and filing
// a heaptype under either would stage a *neighbour's* type at an index a consumer reads as its own.
// Naming all three pins the ordering rather than one row's contents.
//
// The immediate is checked as well, on `PrefixedOp`'s reasoning that a constant carries two facts: a
// row whose mnemonic still says `ref_null` while its immediates lost the heaptype would leave
// `castTypes` filing from an empty `heaps`, which its length guard turns into a silent no-file.
func TestRefNullOpcodeMatchesTheTable(t *testing.T) {
	for _, tc := range []struct {
		op   uint32
		want string
	}{
		{opRefNull, "ref_null"},
		{0xD1, "ref_is_null"},
		{0xD2, "ref_func"},
	} {
		got, ok := OpMnemonic(tc.op)
		if !ok {
			t.Errorf("the table has no row for %#02x, and %q is expected there", tc.op, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("%#02x is %q in the table, want %q — the constants and the table disagree "+
				"about which byte this family's rows sit on, and a heaptype filed under the wrong "+
				"one is a neighbour's type at an index read as its own", tc.op, got, tc.want)
		}
	}
	if !hasHeapTypeImm(opTable[opRefNull]) {
		t.Errorf("the %#02x row's immediates are %v, with no heaptype — `castTypes` files from "+
			"`heaps`, so a row that stopped reading one makes the filing site's length guard fire "+
			"and file nothing, silently", opRefNull, opTable[opRefNull].imms)
	}
}

// hasHeapTypeImm reports whether a row reads at least one heaptype.
func hasHeapTypeImm(info opInfo) bool {
	for _, im := range info.imms {
		if im == immHeapType {
			return true
		}
	}
	return false
}

// TestRefNullRetainsTheSpelledHeapType is the accept-direction control for `ref.null`'s retention,
// and it is the direction no corpus can score.
//
// Before #359 all thirteen heaptypes decoded to the same `Instr` — the gap 0027 declared — and a
// board could not see it, because no *rejection* vector distinguishes `ref.null func` from
// `ref.null extern`: both decode, and the difference only becomes a verdict once a validator types
// them (§9 G-3). So this asserts the retained type per heaptype rather than asserting that something
// was retained.
//
// **All twelve abstract forms plus the indexed one, and the failure it is against is a partial
// copy** — `refNull` preserving `null` while dropping `kind` or `idx`. `(ref null any)` and
// `(ref null $t)` differ in exactly the two fields such a copy loses, so a two-case control would
// agree with a decoder that returned one type for everything abstract.
//
// Measured, not asserted: filing `FuncRef` for every heaptype fails **12 of these 13 rows**, and the
// row it passes is `func` — whose expected value *is* `FuncRef`. So a control built from the obvious
// case would have been protected by coincidence on exactly the case it was built from, which is why
// the list is the whole vocabulary rather than a sample of it.
//
// The wire bytes come from the exported `Heap*` constants, which is not a coincidence being
// exploited: each is defined as its own `sleb(7)` form `& 0x7F`, and single-byte sleb128 of a value
// in -64..-1 *is* `v & 0x7F`, so the constant and the wire byte are one arithmetic fact rather than
// two transcriptions. `TestHeapKindsAreWhatTheReaderProduces` closes that loop already.
func TestRefNullRetainsTheSpelledHeapType(t *testing.T) {
	// Every gate on, not `Features{GC: true}`, since #395: two of the thirteen rows below spell
	// heap types the *exception* gate owns, and a GC-only decoder declined them — a retention
	// test failing on a gate is a test answering a question it did not ask. The subject here is
	// what the reader keeps, so the configuration is the one where nothing is declined at all.
	d := &Decoder{Features: featuresAllOn(t)}
	for _, tc := range []struct {
		name string
		ht   byte
		want string
	}{
		// The two Wasm 2.0 forms, whose nullable spelling *is* FuncRef/ExternRef by grave #180 — so
		// String prints the abbreviation rather than `(ref null func)`. Asserted in the spelling the
		// type actually has, since expecting the long form would test String's output against a spec
		// sentence instead of testing the retention against the wire.
		{"func", HeapFunc, "funcref"},
		{"extern", HeapExtern, "externref"},
		{"noexn", HeapNoExn, "(ref null noexn)"},
		{"nofunc", HeapNoFunc, "(ref null nofunc)"},
		{"noextern", HeapNoExtern, "(ref null noextern)"},
		{"none", HeapNone, "(ref null none)"},
		{"any", HeapAny, "(ref null any)"},
		{"eq", HeapEq, "(ref null eq)"},
		{"i31", HeapI31, "(ref null i31)"},
		{"struct", HeapStruct, "(ref null struct)"},
		{"array", HeapArray, "(ref null array)"},
		{"exn", HeapExn, "(ref null exn)"},
		// The indexed form: `0x00` is sleb 0, non-negative, so it is type index 0 and not an
		// abstract byte (sections.go:914-926). Type 0 exists in the image.
		{"typeidx 0", 0x00, "(ref null 0)"},
	} {
		m, err := d.DecodeModule(oneFuncImage(0xD0, tc.ht))
		if err != nil {
			t.Errorf("ref.null %s (%#02x): decode: %v", tc.name, tc.ht, err)
			continue
		}
		fn := &m.Funcs[0]
		if len(fn.Body) == 0 || fn.Body[0].Op != opRefNull || fn.Body[0].Prefix != 0x00 {
			t.Fatalf("ref.null %s: body[0] is not the ref.null, so the index this reads is not the "+
				"one emit filed under: %+v", tc.name, fn.Body)
		}
		v, ok := fn.CastTypes(0)
		if !ok {
			t.Errorf("ref.null %s: nothing retained — 0027 left this gap open on the condition that "+
				"a consumer arrive, and #9's validator is it", tc.name)
			continue
		}
		if len(v) != 1 {
			t.Errorf("ref.null %s: retained %d types, want exactly 1 — the count is the "+
				"discriminator and not a lower bound, since a two-type vector here would be the "+
				"cast pair's staging leaking into this row", tc.name, len(v))
			continue
		}
		if got := v[0].String(); got != tc.want {
			t.Errorf("ref.null %s: retained %s, want %s", tc.name, got, tc.want)
		}
		if !v[0].null {
			t.Errorf("ref.null %s: retained a non-nullable type — the null bit is the instruction's "+
				"*meaning* here (decode.ml:604, valid.ml:714-716), not an opcode bit as for "+
				"`fb 14`-`fb 17` and not an encoded one as for the br_on_cast pair, so it is "+
				"unconditional and no wire byte could turn it off", tc.name)
		}
	}
}

// TestEveryHeapTypeRowFilesACastType is the tripwire `emit`'s clearing comment cites, and the point
// of it is that the domain comes from the table.
//
// After #359 the rows that read a heaptype are exactly the rows that file a cast type, which is what
// left `emit`'s `heaps` clear with no member of its class. That coincidence is a property of the
// table's current contents and not a law: the next upstream row carrying an `immHeapType` reopens the
// class, and `castTypes`' `default: return` would stage the heaptype, file nothing, and be caught by
// nothing — a silent no-file, since a missing key and "not a cast instruction" are the same nil.
//
// **Derived, not enumerated.** The images are per-row data, but the *domain* is read out of
// `opTable`/`opTableFB`, so a new heaptype row with no image here fails with a demand rather than
// sitting quietly outside the control's extent. A control scoped to the seven rows that exist today
// would inherit today's blind spot.
func TestEveryHeapTypeRowFilesACastType(t *testing.T) {
	type row struct{ prefix, op byte }
	// One image per heaptype row, each decoding to a body whose *first* instruction is the row.
	// `fb 18`/`fb 19` carry a flags byte, a label and two heaptypes; the label need not resolve,
	// nothing here running the validator.
	images := map[row][]byte{
		{0x00, 0xD0}: oneFuncImage(0xD0, HeapAny),
		{0xFB, 0x14}: oneFuncImage(0xFB, 0x14, HeapAny),
		{0xFB, 0x15}: oneFuncImage(0xFB, 0x15, HeapAny),
		{0xFB, 0x16}: oneFuncImage(0xFB, 0x16, HeapAny),
		{0xFB, 0x17}: oneFuncImage(0xFB, 0x17, HeapAny),
		{0xFB, 0x18}: oneFuncImage(0xFB, 0x18, 0x00, 0x00, HeapAny, HeapI31),
		{0xFB, 0x19}: oneFuncImage(0xFB, 0x19, 0x00, 0x00, HeapAny, HeapI31),
	}

	var domain []row
	for op, info := range opTable {
		if hasHeapTypeImm(info) {
			domain = append(domain, row{0x00, byte(op)})
		}
	}
	for op, info := range opTableFB {
		if hasHeapTypeImm(info) {
			domain = append(domain, row{0xFB, byte(op)})
		}
	}
	// The vacuity check: a domain computed from the table would pass by asking nothing if it came
	// back empty, and `immHeapType` disappearing from every row under a regeneration is exactly
	// the accident that would empty it.
	if len(domain) != 7 {
		t.Errorf("the table has %d heaptype-reading rows and 7 are expected (ref.null plus the "+
			"cast family) — if that is a real upstream change, the images here need the new row "+
			"before this control means anything: %v", len(domain), domain)
	}

	d := &Decoder{Features: Features{GC: true}}
	for _, r := range domain {
		img, ok := images[r]
		if !ok {
			t.Errorf("prefix %#02x op %#02x reads a heaptype and has no image here, so whether it "+
				"files a cast type is untested — add one, or if it deliberately files nothing, say "+
				"so at `castTypes`' default arm and re-point `emit`'s clearing comment, whose class "+
				"this row reopens", r.prefix, r.op)
			continue
		}
		m, err := d.DecodeModule(img)
		if err != nil {
			t.Errorf("prefix %#02x op %#02x: image does not decode: %v", r.prefix, r.op, err)
			continue
		}
		fn := &m.Funcs[0]
		if len(fn.Body) == 0 || fn.Body[0].Prefix != r.prefix || byte(fn.Body[0].Op) != r.op {
			t.Errorf("prefix %#02x op %#02x: the image's first instruction is %+v, so index 0 is "+
				"not the row under test", r.prefix, r.op, fn.Body[0])
			continue
		}
		if _, ok := fn.CastTypes(0); !ok {
			t.Errorf("prefix %#02x op %#02x reads a heaptype and files no cast type — the heaptype "+
				"is staged in `heaps` and dropped by `emit`, which is the silent no-file this "+
				"exists to catch", r.prefix, r.op)
		}
	}
}

// TestConstExprRefNullFilesNoCastType pins the half of `ref.null`'s retention that #359 did *not*
// close, so the gap is declared rather than discovered.
//
// A const expression's instructions are retained (`decodeConstExprKeep`) and its `castsOut` is nil,
// because `Global.Init`, `ElemSegment.Offset`/`Exprs` and `DataSegment.Offset` are bare `[]Instr`
// with no side table beside them. `emit` therefore drops the staged reftype — correctly for a
// recognizing read, and indistinguishably from one here.
//
// **Not plumbed and not a bug, on the rule that closed the other half.** `internal/validate` walks
// const expressions for `checkConstGlobals`' ordering rule and does not type them, so a field would
// be retention ahead of its consumer (0016). What makes that a declaration rather than an intention
// is this test plus #361: when const-expression typing arrives it meets a known gap instead of a
// `CastTypes` that answers false for an instruction which has a static type.
//
// The assertion is of the *current* state on purpose, and it is a tripwire in both directions, which
// takes two different assertions because the two ways of closing the gap are observable in two
// different places. Plumbing it into a **word** shows up in the retained `Instr` — Imm0/Imm1 stop
// being zero — and plumbing it into a **side table** shows up in the struct that would hold it, since
// `Global.Init` is a bare slice with nowhere to key by instruction index. Asserting only the first
// would let the likelier of the two land silently, and the comment that used to stand here claimed
// both directions off the first assertion alone.
//
// A structural assertion is the only thing that can see the second direction: there is no value to
// read, only the absence of a place to read one from.
func TestConstExprRefNullFilesNoCastType(t *testing.T) {
	// A global whose initializer is `ref.null func`, END — the shortest const expression that
	// stages a reftype. Section 6, one global, type `funcref` immutable.
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> [] (so a typeidx heaptype would resolve)
		0x06, 0x06, 0x01, 0x70, 0x00, 0xD0, HeapFunc, 0x0B,
	}
	m, err := (&Decoder{Features: Features{GC: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m.Globals) != 1 {
		t.Fatalf("%d globals, want 1", len(m.Globals))
	}
	init := m.Globals[0].Init
	if len(init) == 0 || init[0].Op != opRefNull {
		t.Fatalf("the initializer's first instruction is not the ref.null, so this asserts nothing "+
			"about the staging path: %+v", init)
	}
	if init[0].Imm0 != 0 || init[0].Imm1 != 0 {
		t.Errorf("the retained ref.null carries Imm0=%d Imm1=%d, so a heaptype is reaching a word "+
			"— `immHeapType` stages none by 0027, and a word appearing here means the capacity "+
			"control `immStagedBits` bounds has a new per-row cost", init[0].Imm0, init[0].Imm1)
	}
	// The side-table direction. `Func` carries `Casts map[int][]ValType`; the three const-expression
	// holders carry no such field, and that absence *is* the gap. Checked structurally over all
	// three rather than on `Global` alone, because whichever one a future consumer needs first is
	// the one that would land while a control watching a different struct stayed green.
	for _, tc := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Global", reflect.TypeFor[Global]()},
		{"ElemSegment", reflect.TypeFor[ElemSegment]()},
		{"DataSegment", reflect.TypeFor[DataSegment]()},
	} {
		want := reflect.TypeFor[map[int][]ValType]()
		for i := range tc.typ.NumField() {
			if f := tc.typ.Field(i); f.Type == want {
				t.Errorf("%s.%s is a %s — a per-instruction cast side table beside a const "+
					"expression, which is #361 closing. Delete this pin and re-point the "+
					"`castsOut` comment, which still says the retention is declined",
					tc.name, f.Name, f.Type)
			}
		}
	}
	// Vacuity half: the same instruction in a *function body* does file, so the absences above are
	// about the call path and not about the opcode. Without this, the whole test would pass if
	// `ref.null` had never begun filing at all.
	fm, err := (&Decoder{Features: Features{GC: true}}).DecodeModule(oneFuncImage(0xD0, HeapFunc))
	if err != nil {
		t.Fatalf("the function-body image does not decode, so the comparison has one side: %v", err)
	}
	if _, ok := fm.Funcs[0].CastTypes(0); !ok {
		t.Fatal("a ref.null in a function body files no cast type either, so this test is pinning " +
			"a gap that is actually the whole feature missing — the absence above says nothing")
	}
}
