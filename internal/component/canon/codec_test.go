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
	Pin   string        `json:"pin"`
	Cases []fixtureCase `json:"cases"`
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
	Kind string    `json:"kind"`
	Elem *typeSpec `json:"elem"`
}

type memFix struct {
	Ptr       int           `json:"ptr"`
	MemoryHex string        `json:"memory_hex"`
	Realloc   []reallocCall `json:"realloc"`
}

type flatFix struct {
	Types     []string      `json:"types"`
	Values    []json.Number `json:"values"`
	MemoryHex string        `json:"memory_hex"`
	Realloc   []reallocCall `json:"realloc"`
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
	k, ok := map[string]Kind{
		"bool": KindBool, "u8": KindU8, "u16": KindU16, "u32": KindU32, "u64": KindU64,
		"s8": KindS8, "s16": KindS16, "s32": KindS32, "s64": KindS64,
		"char": KindChar, "string": KindString, "list": KindList,
	}[s.Kind]
	if !ok {
		t.Fatalf("fixture type kind %q is outside the modeled scope", s.Kind)
	}
	if k == KindList {
		elem := typeFromSpec(t, *s.Elem)
		return Type{Kind: KindList, Elem: &elem}
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
		})
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
