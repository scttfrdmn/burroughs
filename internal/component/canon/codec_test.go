// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// The differential fixtures are the reference model's own reading. `gen/gen.py` drives the pinned
// `definitions.py` (component-model @ 2bed77e) over `gen/cases.json` and emits `testdata/fixtures.json`;
// this test compares Burroughs' codec to it with no Python in the loop — the wabt precedent, so
// BURROUGHS_NO_SKIP=1 passes with no interpreter present. Regenerate with `make canon-fixtures`.
const fixturesPath = "testdata/fixtures.json"

type fixtureFile struct {
	Pin     string        `json:"pin"`
	Cases   []fixtureCase `json:"cases"`
	Handles []handleFix   `json:"handles"`
	Shapes  []shapeFix    `json:"shapes"`
}

type fixtureCase struct {
	Name     string   `json:"name"`
	Type     typeSpec `json:"type"`
	Value    any      `json:"value"`
	HeapSize int      `json:"heap_size"`
	Store    memFix   `json:"store"`
	Flat     flatFix  `json:"flat"`
}

type typeSpec struct {
	Kind   string     `json:"kind"`
	Elem   *typeSpec  `json:"elem"`
	Cases  []caseSpec `json:"cases"`
	Fields []typeSpec `json:"fields"`
	Ok     *typeSpec  `json:"ok"`
	Err    *typeSpec  `json:"err"`
	Rt     int        `json:"rt"`
}

type caseSpec struct {
	Name string    `json:"name"`
	Type *typeSpec `json:"type"`
}

type memFix struct {
	Ptr       int           `json:"ptr"`
	MemoryHex string        `json:"memory_hex"`
	Realloc   []reallocCall `json:"realloc"`
	Table     []tableEntry  `json:"table"` // the post-store handle table (empty for non-own types); seeds the lift (#728)
}

type flatFix struct {
	Types     []string      `json:"types"`
	Values    []json.Number `json:"values"`
	MemoryHex string        `json:"memory_hex"`
	Realloc   []reallocCall `json:"realloc"`
	Table     []tableEntry  `json:"table"` // the flat lowering's handle table; seeds lift-flat (#728)
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	data, err := os.ReadFile(fixturesPath)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var f fixtureFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode %s: %v", fixturesPath, err)
	}
	return f
}

func typeFromSpec(t *testing.T, s typeSpec) Type {
	t.Helper()
	optType := func(ts *typeSpec) *Type {
		if ts == nil {
			return nil
		}
		x := typeFromSpec(t, *ts)
		return &x
	}
	switch s.Kind {
	case "list":
		return Type{Kind: KindList, Elem: optType(s.Elem)}
	case "variant":
		cases := make([]Case, len(s.Cases))
		for i, cs := range s.Cases {
			cases[i] = Case{Name: cs.Name, Type: optType(cs.Type)}
		}
		return VariantType(cases...)
	case "result":
		return ResultType(optType(s.Ok), optType(s.Err))
	case "own":
		return OwnType(s.Rt)
	case "borrow":
		return BorrowType(s.Rt)
	case "tuple":
		fields := make([]Type, len(s.Fields))
		for i := range s.Fields {
			fields[i] = typeFromSpec(t, s.Fields[i])
		}
		return TupleType(fields...)
	}
	k, ok := map[string]Kind{
		"bool": KindBool, "u8": KindU8, "u16": KindU16, "u32": KindU32, "u64": KindU64,
		"s8": KindS8, "s16": KindS16, "s32": KindS32, "s64": KindS64,
		"f32": KindF32, "f64": KindF64,
		"char": KindChar, "string": KindString,
	}[s.Kind]
	if !ok {
		t.Fatalf("fixture type kind %q is outside the modeled scope", s.Kind)
	}
	return Type{Kind: k}
}

// valueFromJSON builds a value through the public constructors — so the differential exercises the
// constructor surface (ADR 0085), including Char's surrogate refusal and List's element-type check, not
// just the codec.
func valueFromJSON(t *testing.T, typ Type, raw any) Value {
	t.Helper()
	switch typ.Kind {
	case KindBool:
		return Bool(raw.(bool))
	case KindU8:
		return U8(uint8(mustParseUint(t, raw.(json.Number))))
	case KindU16:
		return U16(uint16(mustParseUint(t, raw.(json.Number))))
	case KindU32:
		return U32(uint32(mustParseUint(t, raw.(json.Number))))
	case KindU64:
		return U64(mustParseUint(t, raw.(json.Number)))
	case KindS8:
		return S8(int8(mustParseInt(t, raw.(json.Number))))
	case KindS16:
		return S16(int16(mustParseInt(t, raw.(json.Number))))
	case KindS32:
		return S32(int32(mustParseInt(t, raw.(json.Number))))
	case KindS64:
		return S64(mustParseInt(t, raw.(json.Number)))
	case KindF32, KindF64:
		// A float case gives its IEEE bits as a hex string, so a non-canonical NaN payload survives to
		// the codec (Go's float32() would not preserve it). The codec canonicalizes NaN on lower.
		bits, err := strconv.ParseUint(raw.(string)[2:], 16, 64)
		if err != nil {
			t.Fatalf("float bits %v: %v", raw, err)
		}
		return Value{Type: typ, u: bits}
	case KindChar:
		v, err := Char([]rune(raw.(string))[0])
		if err != nil {
			t.Fatal(err)
		}
		return v
	case KindString:
		return Str(raw.(string))
	case KindList:
		arr := raw.([]any)
		vals := make([]Value, len(arr))
		for i, e := range arr {
			vals[i] = valueFromJSON(t, *typ.Elem, e)
		}
		v, err := List(*typ.Elem, vals...)
		if err != nil {
			t.Fatal(err)
		}
		return v
	case KindVariant:
		m := raw.(map[string]any)
		var name string
		var rawPayload any
		for k, val := range m { // a variant value is a single {case: payload} pair
			name, rawPayload = k, val
		}
		var payload *Value
		for _, c := range typ.Cases {
			if c.Name == name && c.Type != nil {
				p := valueFromJSON(t, *c.Type, rawPayload)
				payload = &p
			}
		}
		v, err := Variant(typ, name, payload)
		if err != nil {
			t.Fatal(err)
		}
		return v
	case KindOwn:
		// An own handle's fixture value is its resource representation (an i32 rep); the codec's
		// lower_own assigns the table index, as the reference model does.
		return Own(typ.RT, uint32(mustParseUint(t, raw.(json.Number))))
	default:
		t.Fatalf("value kind %s outside modeled scope", typ.Kind)
		return Value{}
	}
}

func mustParseUint(t *testing.T, n json.Number) uint64 {
	t.Helper()
	u, err := strconv.ParseUint(n.String(), 10, 64)
	if err != nil {
		t.Fatalf("parse uint %s: %v", n, err)
	}
	return u
}

func mustParseInt(t *testing.T, n json.Number) int64 {
	t.Helper()
	i, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		t.Fatalf("parse int %s: %v", n, err)
	}
	return i
}

// TestCodecMatchesReferenceModel is PR A's exit for the modeled types: for every fixture case, the
// codec's store image, its realloc sequence, and its flat lowering match the reference model's reading
// byte-for-byte.
func TestCodecMatchesReferenceModel(t *testing.T) {
	f := loadFixtures(t)
	if len(f.Cases) == 0 {
		t.Fatal("no fixtures: run make canon-fixtures")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			typ := typeFromSpec(t, c.Type)
			v := valueFromJSON(t, typ, c.Value)

			// store: reserve the value's region as the generator does, then store into it.
			sh := newHeap(c.HeapSize)
			ptr, err := sh.realloc(0, 0, alignment(typ), size(typ))
			if err != nil {
				t.Fatal(err)
			}
			if ptr != c.Store.Ptr {
				t.Fatalf("reserved ptr = %d, want %d", ptr, c.Store.Ptr)
			}
			if serr := sh.store(v, ptr); serr != nil {
				t.Fatalf("store: %v", serr)
			}
			if got := hex.EncodeToString(sh.mem); got != c.Store.MemoryHex {
				t.Errorf("store memory:\n got %s\nwant %s", got, c.Store.MemoryHex)
			}
			assertRealloc(t, "store", sh.calls, c.Store.Realloc)
			// The store's handle table matches the model's (empty for non-own types; for an own-bearing
			// type, lower_own's entry — the index the memory holds, its rep/own bit) — #728.
			assertTable(t, "after store", serializeTable(sh.table), c.Store.Table)

			// flat: lower to core values, compare types, values, and the memory the lowering touched.
			fh := newHeap(c.HeapSize)
			flats, err := fh.lowerFlat(v)
			if err != nil {
				t.Fatalf("lowerFlat: %v", err)
			}
			if len(flats) != len(c.Flat.Types) {
				t.Fatalf("flat arity = %d, want %d", len(flats), len(c.Flat.Types))
			}
			for i, fv := range flats {
				if fv.kind != c.Flat.Types[i] {
					t.Errorf("flat[%d] type = %s, want %s", i, fv.kind, c.Flat.Types[i])
				}
				if want := mustParseUint(t, c.Flat.Values[i]); fv.bits != want {
					t.Errorf("flat[%d] bits = %d, want %d", i, fv.bits, want)
				}
			}
			if got := hex.EncodeToString(fh.mem); got != c.Flat.MemoryHex {
				t.Errorf("flat memory:\n got %s\nwant %s", got, c.Flat.MemoryHex)
			}
			assertRealloc(t, "flat", fh.calls, c.Flat.Realloc)

			// lift: load the reference model's stored bytes back and check the value round-trips. This
			// tests load against definitions.py's store output, not against the codec's own store.
			//
			// **Own-bearing types seed the handle table first (#728).** Lifting an `own` (`lift_own`)
			// consumes the handle from the instance table, which is model state the fixture's memory
			// bytes do not carry. The fixture's `store.table` is the model's post-store table, so seeding
			// the load heap from it lets the composed lift run — and matches definitions.py's `lift`: the
			// lifted value (the own's rep) and, checked below, the emptied table. For non-own types the
			// table is empty and the seed is a no-op, so every type round-trips here with no conditional.
			lh := newHeap(c.HeapSize)
			raw, derr := hex.DecodeString(c.Store.MemoryHex)
			if derr != nil {
				t.Fatal(derr)
			}
			copy(lh.mem, raw)
			seedTable(lh.table, c.Store.Table)
			got, lerr := lh.load(c.Store.Ptr, typ)
			if lerr != nil {
				t.Fatalf("load: %v", lerr)
			}
			if !valueEqual(got, v) {
				t.Errorf("lift: load returned a value unequal to the lowered one (%s)", typ.Kind)
			}
			// lift_own consumes the handle, so an own-bearing type's table is emptied afterward.
			if got := serializeTable(lh.table); len(got) != 0 {
				t.Errorf("lift: table not emptied after load, still %+v (lift_own must consume the handle)", got)
			}

			// lift-flat: reconstruct from the flat sequence, reading string/list data from the flat
			// memory. This is the inverse of lowerFlat, completing "every lift and lower" for the type.
			flatVals := make([]uint64, len(c.Flat.Values))
			for i, n := range c.Flat.Values {
				flatVals[i] = mustParseUint(t, n)
			}
			flh := newHeap(c.HeapSize)
			fraw, derr2 := hex.DecodeString(c.Flat.MemoryHex)
			if derr2 != nil {
				t.Fatal(derr2)
			}
			copy(flh.mem, fraw)
			seedTable(flh.table, c.Flat.Table)
			gotFlat, ferr := flh.liftFlat(&coreValueIter{types: c.Flat.Types, vals: flatVals}, typ)
			if ferr != nil {
				t.Fatalf("liftFlat: %v", ferr)
			}
			if !valueEqual(gotFlat, v) {
				t.Errorf("lift-flat: liftFlat returned a value unequal to the lowered one (%s)", typ.Kind)
			}
		})
	}
}

// valueEqual compares two values for the differential's lift check. Floats compare as canonicalized
// bits (a non-canonical NaN lowered then lifted comes back canonical), matching how the codec and the
// reference model both canonicalize.
func valueEqual(a, b Value) bool {
	if a.Type.Kind != b.Type.Kind {
		return false
	}
	switch a.Type.Kind {
	case KindF32:
		return canonicalizeNaN32(uint32(a.u)) == canonicalizeNaN32(uint32(b.u))
	case KindF64:
		return canonicalizeNaN64(a.u) == canonicalizeNaN64(b.u)
	case KindString:
		return a.s == b.s
	case KindList:
		if len(a.list) != len(b.list) || !typeEqual(a.Type, b.Type) {
			return false
		}
		for i := range a.list {
			if !valueEqual(a.list[i], b.list[i]) {
				return false
			}
		}
		return true
	case KindVariant:
		if a.u != b.u || (a.payload == nil) != (b.payload == nil) {
			return false
		}
		return a.payload == nil || valueEqual(*a.payload, *b.payload)
	case KindOwn:
		return a.u == b.u && a.Type.RT == b.Type.RT
	default:
		return a.u == b.u
	}
}

func assertRealloc(t *testing.T, what string, got, want []reallocCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s realloc count = %d, want %d\n got %+v\nwant %+v", what, len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s realloc[%d] = %+v, want %+v", what, i, got[i], want[i])
		}
	}
}

// TestMisLoweredVariantIsRefused is #694 refinement 2's positive assertion: a variant sent down the
// wrong branch reads as a plausible value, so the two silent mis-layouts are witnessed refused. A wrong
// payload offset (not aligned to the cases' max alignment) and a wrong discriminant width (u16 where
// the case count needs only u8) each produce a plausible-but-wrong image the model does not.
func TestMisLoweredVariantIsRefused(t *testing.T) {
	u8t := Type{Kind: KindU8}
	u64t := Type{Kind: KindU64}

	// (1) Payload offset: the largest case is u64, so the payload aligns to offset 8 after the 1-byte
	// discriminant. A codec that packed it right after the discriminant would put it at offset 1.
	vt := VariantType(Case{Name: "a", Type: &u8t}, Case{Name: "b", Type: &u64t})
	const pval = uint64(0x1122334455667788)
	pv := U64(pval)
	v, err := Variant(vt, "b", &pv)
	if err != nil {
		t.Fatal(err)
	}
	h := newHeap(64)
	p, err := h.realloc(0, 0, alignment(vt), size(vt))
	if err != nil {
		t.Fatal(err)
	}
	if serr := h.store(v, p); serr != nil {
		t.Fatal(serr)
	}
	if h.mem[0] != 1 {
		t.Fatalf("discriminant = %d, want case index 1", h.mem[0])
	}
	var pbuf [8]byte
	for i := range 8 {
		pbuf[i] = byte(pval >> (8 * i))
	}
	off := alignTo(1, 8) // = 8
	if !bytes.Equal(h.mem[off:off+8], pbuf[:]) {
		t.Errorf("payload at offset %d = % x, want % x", off, h.mem[off:off+8], pbuf)
	}
	if bytes.Equal(h.mem[1:9], pbuf[:]) {
		t.Error("payload packed at offset 1 — the mis-alignment this asserts against")
	}

	// (2) Discriminant width: all-u8 cases have max alignment 1, so a 1-byte discriminant puts the
	// payload at offset 1. A 2-byte discriminant (wrong for ≤256 cases) would push it to offset 2.
	vt2 := VariantType(Case{Name: "a", Type: &u8t}, Case{Name: "b", Type: &u8t})
	pv2 := U8(0x42)
	v2, err := Variant(vt2, "b", &pv2)
	if err != nil {
		t.Fatal(err)
	}
	h2 := newHeap(64)
	p2, err := h2.realloc(0, 0, alignment(vt2), size(vt2))
	if err != nil {
		t.Fatal(err)
	}
	if serr := h2.store(v2, p2); serr != nil {
		t.Fatal(serr)
	}
	if h2.mem[0] != 1 {
		t.Fatalf("discriminant = %d, want case index 1", h2.mem[0])
	}
	if h2.mem[1] != 0x42 {
		t.Fatalf("payload at offset 1 = %#x, want 0x42 — a wider discriminant would push it to offset 2", h2.mem[1])
	}
}

// TestMisLoweredStringIsRefused is ADR 0084's positive assertion for strings: a string lowered by rune
// count rather than UTF-8 byte length reads as a plausible value (the pointer is right, the length is
// close), so the hazard is witnessed by constructing the discriminating case and showing the codec does
// not produce it. "café" is 4 runes but 5 UTF-8 bytes; a codec that lowered the rune count would pass a
// length no reader could tell was wrong until the bytes ran short.
func TestMisLoweredStringIsRefused(t *testing.T) {
	const s = "café" // 4 runes, 5 UTF-8 bytes
	runeCount := len([]rune(s))
	byteLen := len([]byte(s))
	if runeCount == byteLen {
		t.Fatal("test string must be multibyte for the assertion to discriminate")
	}

	h := newHeap(64)
	flats, err := h.lowerFlat(Str(s))
	if err != nil {
		t.Fatal(err)
	}
	gotLen := int(flats[1].bits)
	if gotLen == runeCount {
		t.Fatalf("string lowered to rune count %d — the mis-lowering this asserts against", runeCount)
	}
	if gotLen != byteLen {
		t.Fatalf("string length = %d, want UTF-8 byte length %d", gotLen, byteLen)
	}
	// And the bytes actually written are the UTF-8 encoding, not a truncation.
	ptr := int(flats[0].bits)
	if !bytes.Equal(h.mem[ptr:ptr+byteLen], []byte(s)) {
		t.Errorf("lowered bytes = % x, want % x", h.mem[ptr:ptr+byteLen], []byte(s))
	}
}
