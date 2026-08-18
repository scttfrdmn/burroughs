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

// TestConstExprRefNullRetainsItsHeapType is #361 closed, and it is the successor to the tripwire that
// pinned the gap rather than a deletion of it.
//
// Formerly `TestConstExprRefNullFilesNoCastType`, which asserted that `Global`,
// `ElemSegment` and `DataSegment` carried **no** cast side table — the declared state, held on 0016's
// rule that retention waits for a consumer. #328's `checkConst` types all four constant-expression
// sites, which is the consumer #361 named, so the fields exist and that test failed exactly as
// designed. **Its message said "delete this pin", and taking that literally would have been the
// error**: a control whose subject closes is re-pointed at the closed state, because the silent-loss
// failure it was against did not go away — it moved from "the map is never filed" to "the map is
// filed for three of the four holders", which nothing would have noticed.
//
// So the domain here is the four holders, and the two halves are:
//
//   - **per site**, that a `ref.null` in that site's expression files its spelled heaptype, keyed by
//     the instruction's own index;
//   - **structurally**, that every constant expression a holder carries has a side table beside it,
//     with the field list *derived* from the struct rather than enumerated — so the fifth site
//     (`Table`'s initializer, which #7 introduces and which `modulePre`'s census names as absent)
//     cannot arrive without one.
//
// The heaptype is read back rather than the map's presence checked, for the reason the old pin's
// first assertion had: a filed-but-wrong vector and an unfiled one are different bugs, and only the
// value tells them apart.
func TestConstExprRefNullRetainsItsHeapType(t *testing.T) {
	// Every gate on, matching TestRefNullRetainsTheSpelledHeapType's configuration and for its
	// reason: the subject is what the reader keeps, so a declined feature would be a test answering
	// a question it did not ask.
	d := &Decoder{Features: featuresAllOn(t)}

	// The four sites, each as a whole module image, because three of them need a memory or a table
	// section for the segment to decode at all — a per-site fixture is the only form in which
	// "which holder was it filed on" is a real question.
	//
	// `HeapExtern` and not `HeapFunc` throughout: `ref.null extern` is the spelling that a
	// heaptype-blind retention cannot fake. A funcref null is what most vectors carry, so filing
	// `funcref` unconditionally would pass a `HeapFunc` fixture — the accept-direction defect
	// `refNull`'s own comment in `internal/validate` names, tested here from the retention side.
	preamble := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
	}
	for _, tc := range []struct {
		name string
		tail []byte
		// read pulls the site's side table out of the decoded module, and the instruction index
		// the `ref.null` sits at. The index is per-case because two of the four sites put the
		// `ref.null` at 0 and the elem offset puts it after nothing — see each row.
		read func(*Module) (map[int][]ValType, int, bool)
	}{
		{
			// A global `(global externref (ref.null extern))`. Section 6: one global, type
			// `externref` (0x6F), immutable, then the initializer.
			name: "global initializer",
			tail: []byte{0x06, 0x06, 0x01, 0x6F, 0x00, 0xD0, HeapExtern, 0x0B},
			read: func(m *Module) (map[int][]ValType, int, bool) {
				if len(m.Globals) != 1 {
					return nil, 0, false
				}
				return m.Globals[0].InitCasts, 0, m.Globals[0].Init[0].Op == opRefNull
			},
		},
		{
			// A data segment whose *offset* is `ref.null extern`. Invalid as a module — an offset
			// must type as an address — and that is the point: rejecting it is what needs the
			// heaptype, so the reject direction has the same retention requirement the accept one
			// does. Section 5 declares a memory so the segment decodes; the decoder does not type
			// offsets.
			name: "data segment offset",
			tail: []byte{
				0x05, 0x03, 0x01, 0x00, 0x01, // memory: one, min 1
				0x0b, 0x06, 0x01, 0x00, 0xD0, HeapExtern, 0x0B, 0x00, // data: active mem 0, offset, 0 bytes
			},
			read: func(m *Module) (map[int][]ValType, int, bool) {
				if len(m.Datas) != 1 {
					return nil, 0, false
				}
				return m.Datas[0].OffsetCasts, 0, m.Datas[0].Offset[0].Op == opRefNull
			},
		},
		{
			// An element segment whose offset is `ref.null extern`, same shape and same reason as
			// the data row. Flags 0x00 is active-at-table-0 with the elemkind form, so the element
			// vector is function indices and is empty.
			name: "element segment offset",
			tail: []byte{
				0x04, 0x04, 0x01, 0x70, 0x00, 0x01, // table: one funcref, min 1
				0x09, 0x06, 0x01, 0x00, 0xD0, HeapExtern, 0x0B, 0x00, // elem: flags 0, offset, 0 funcs
			},
			read: func(m *Module) (map[int][]ValType, int, bool) {
				if len(m.Elems) != 1 {
					return nil, 0, false
				}
				return m.Elems[0].OffsetCasts, 0, m.Elems[0].Offset[0].Op == opRefNull
			},
		},
		{
			// The expression form, and **the `ref.null` is the second element while the first
			// files nothing**. `ExprCasts` is the one site keyed twice — by expression and then by
			// instruction — and the two ways it can be wrong need different first elements to
			// witness. A build that returns expression 0's map for expression 1 is caught by the
			// `ref.null` not being at 0; a build that appends to `ExprCasts` only where a cast was
			// filed is caught by expression 0 filing *none*, which shortens the slice and moves
			// every later index down one. A fixture whose first element were a second `ref.null`
			// would see the first bug and be blind to the second — measured, not reasoned: that is
			// exactly what the first draft of this row was, and the falsification that appends
			// conditionally left it green.
			//
			// So expression 0 is `ref.func 0`, which needs the function and code sections below to
			// keep the fixture a well-formed module in every respect but the one under test.
			name: "element segment expression",
			tail: []byte{
				0x03, 0x02, 0x01, 0x00, // function: one func, type 0
				0x09, 0x0a, 0x01, 0x05, 0x6F, 0x02, // elem: flags 5, reftype externref, 2 exprs
				0xD2, 0x00, 0x0B, // expression 0: ref.func 0 — stages no cast type
				0xD0, HeapExtern, 0x0B, // expression 1: ref.null extern
				0x0a, 0x04, 0x01, 0x02, 0x00, 0x0B, // code: one empty body
			},
			read: func(m *Module) (map[int][]ValType, int, bool) {
				if len(m.Elems) != 1 || len(m.Elems[0].ExprCasts) != 2 {
					return nil, 0, false
				}
				return m.Elems[0].ExprCasts[1], 0, m.Elems[0].Exprs[1][0].Op == opRefNull
			},
		},
	} {
		m, err := d.DecodeModule(append(append([]byte{}, preamble...), tc.tail...))
		if err != nil {
			t.Errorf("%s: decode: %v", tc.name, err)
			continue
		}
		casts, at, ok := tc.read(m)
		if !ok {
			t.Errorf("%s: the fixture did not decode into the shape this row reads — the "+
				"instruction the assertion is about is not where it looks, so a green here would "+
				"say nothing", tc.name)
			continue
		}
		ts, filed := casts[at]
		if !filed {
			t.Errorf("%s: nothing retained for the ref.null at instruction %d — #361's gap, which "+
				"#328's checkConst is the consumer for; a nil map here is `emit` dropping the "+
				"staged reftype because this holder's castsOut never reached it", tc.name, at)
			continue
		}
		if len(ts) != 1 {
			t.Errorf("%s: retained %d types, want exactly 1 — the count is the discriminator and "+
				"not a lower bound, a two-type vector being the cast pair's staging leaking in",
				tc.name, len(ts))
			continue
		}
		if got := ts[0].String(); got != "externref" {
			t.Errorf("%s: retained %s, want externref — a retention that files `funcref` for every "+
				"`ref.null` passes a HeapFunc fixture and is exactly the invented type "+
				"`internal/validate`'s refNull refuses to fall back on", tc.name, got)
		}
		if !ts[0].null {
			t.Errorf("%s: retained a non-nullable type — the null bit is `ref.null`'s meaning "+
				"(decode.ml:604), unconditional and unencodable, the same in a constant "+
				"expression as in a body", tc.name)
		}
	}

	// The structural half, and its domain is **derived**: every field of a const-expression holder
	// whose type is `[]Instr` must have a `map[int][]ValType` sibling, and every `[][]Instr` field a
	// `[]map[int][]ValType` one. Enumerating the four sites instead would inherit today's list, which
	// is precisely the mistake the old pin was one revision away from — it watched three structs and
	// the fourth site (`Table`'s initializer) is on a fifth.
	//
	// `Table` is in the domain and contributes nothing today, which is the intent: its initializer is
	// decoded and discarded, `modulePre`'s census names the absence, and #7 is what adds the field.
	// When it does, this fires unless the side table comes with it.
	for _, tc := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Global", reflect.TypeFor[Global]()},
		{"ElemSegment", reflect.TypeFor[ElemSegment]()},
		{"DataSegment", reflect.TypeFor[DataSegment]()},
		{"Table", reflect.TypeFor[Table]()},
	} {
		var (
			one  = reflect.TypeFor[[]Instr]()
			many = reflect.TypeFor[[][]Instr]()
			side = map[reflect.Type]reflect.Type{
				one:  reflect.TypeFor[map[int][]ValType](),
				many: reflect.TypeFor[[]map[int][]ValType](),
			}
			have = map[reflect.Type]int{}
			want = map[reflect.Type]int{}
		)
		for i := range tc.typ.NumField() {
			switch ft := tc.typ.Field(i).Type; ft {
			case one, many:
				want[side[ft]]++
			default:
				have[ft]++
			}
		}
		for st, n := range want {
			if have[st] < n {
				t.Errorf("%s holds %d constant expression(s) wanting a %s beside them and carries "+
					"%d — a const expression with no side table is a `ref.null` whose heaptype is "+
					"dropped, which is #361 reopening at a site nobody was watching",
					tc.name, n, st, have[st])
			}
		}
	}

	// Vacuity half, kept from the pin this replaces and inverted with it: the same instruction in a
	// *function body* files too, so a green above is about both call paths rather than about one. If
	// `ref.null` stopped filing altogether, every assertion here would fail and this would say which.
	fm, err := d.DecodeModule(oneFuncImage(0xD0, HeapExtern))
	if err != nil {
		t.Fatalf("the function-body image does not decode, so the comparison has one side: %v", err)
	}
	if ts, ok := fm.Funcs[0].CastTypes(0); !ok || len(ts) != 1 || ts[0].String() != "externref" {
		t.Fatalf("a ref.null in a function body retains %v (filed=%v), so the const-expression "+
			"assertions above are measuring a feature that is broken on both paths", ts, ok)
	}
}
