// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"strings"
	"testing"
)

// TestLoadFlatIsLoadThenLowerFlat is LoadFlat's differential (ADR 0123): a value stored with StoreVia and read
// back with LoadFlat gives the words the model's flat lowering (`lowerFlat`) gives the value itself, in the
// same order and at the same core types. The cases are the ones a spilled async parameter list reaches, connect's
// `ip-socket-address` in both cases first, then the leaves a variant's join coerces.
//
// Neutered, run: the variant's coercion skipped (each payload word kept at its own type), and the u32 case of the
// i32-and-i64 join reads its payload word as i32 where the join is i64; the zero padding dropped, and the ipv4
// address reads 6 words where the variant flattens to 12.
func TestLoadFlatIsLoadThenLowerFlat(t *testing.T) {
	u8, u16, u32, u64 := Type{Kind: KindU8}, Type{Kind: KindU16}, Type{Kind: KindU32}, Type{Kind: KindU64}
	v4 := RecordType(Field{Name: "port", Type: u16}, Field{Name: "address", Type: TupleType(u8, u8, u8, u8)})
	v6 := RecordType(Field{Name: "port", Type: u16}, Field{Name: "flow-info", Type: u32},
		Field{Name: "address", Type: TupleType(u16, u16, u16, u16, u16, u16, u16, u16)},
		Field{Name: "scope-id", Type: u32})
	addrT := VariantType(Case{Name: "ipv4", Type: &v4}, Case{Name: "ipv6", Type: &v6})
	tuple := func(ty Type, vs ...Value) Value {
		m := map[string]Value{}
		for i, v := range vs {
			m[ty.Fields[i].Name] = v
		}
		r, err := Record(ty, m)
		if err != nil {
			t.Fatalf("building %s: %v", ty, err)
		}
		return r
	}
	variant := func(ty Type, c string, p *Value) Value {
		v, err := Variant(ty, c, p)
		if err != nil {
			t.Fatalf("building %s.%s: %v", ty, c, err)
		}
		return v
	}
	ipv4 := tuple(v4, U16(8080), tuple(TupleType(u8, u8, u8, u8), U8v(127), U8v(0), U8v(0), U8v(1)))
	ipv6 := tuple(v6, U16(443), U32(7), tuple(TupleType(u16, u16, u16, u16, u16, u16, u16, u16),
		U16(0xfe80), U16(0), U16(0), U16(0), U16(0), U16(0), U16(0), U16(1)), U32(3))
	mixed := VariantType(Case{Name: "small", Type: &u32}, Case{Name: "big", Type: &u64}, Case{Name: "none"})
	opt := OptionType(Type{Kind: KindS16})
	someNeg, err := Some(opt, S16(-2))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		v    Value
	}{
		{"ipv4-socket-address", variant(addrT, "ipv4", &ipv4)},
		{"ipv6-socket-address", variant(addrT, "ipv6", &ipv6)},
		{"tuple of the borrow's word and the address", tuple(TupleType(u32, addrT), U32(5), variant(addrT, "ipv4", &ipv4))},
		{"a u32 case of an i32-and-i64 join", variant(mixed, "small", ptrTo(U32(0xfffffffe)))},
		{"a u64 case of an i32-and-i64 join", variant(mixed, "big", ptrTo(U64(1<<40|3)))},
		{"a payload-less case pads", variant(mixed, "none", nil)},
		{"a negative s16 in an option", someNeg},
		{"bool", Bool(true)},
		{"s8", S8(-1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHeap(1 << 12)
			ptr, err := h.Realloc(0, 0, alignment(c.v.Type), size(c.v.Type))
			if err != nil {
				t.Fatal(err)
			}
			if serr := StoreVia(h, c.v, ptr); serr != nil {
				t.Fatalf("StoreVia: %v", serr)
			}
			want, err := h.lowerFlat(c.v)
			if err != nil {
				t.Fatalf("lowerFlat: %v", err)
			}
			bits, types, err := LoadFlat(h, c.v.Type, ptr)
			if err != nil {
				t.Fatalf("LoadFlat: %v", err)
			}
			if len(bits) != len(want) || len(types) != len(want) {
				t.Fatalf("LoadFlat gave %d words (%v), lowerFlat %d (%v)", len(bits), types, len(want), want)
			}
			for i := range want {
				if bits[i] != want[i].bits || types[i] != want[i].kind {
					t.Errorf("word %d = %s %#x, want %s %#x", i, types[i], bits[i], want[i].kind, want[i].bits)
				}
			}
		})
	}
	t.Run("a pointer not aligned to the tuple is refused", func(t *testing.T) {
		h := newHeap(1 << 12)
		if _, _, err := LoadFlat(h, addrT, 2); err == nil || !strings.Contains(err.Error(), "not aligned") {
			t.Errorf("err = %v, want the alignment refusal", err)
		}
	})
	t.Run("a tuple past the end of memory is refused", func(t *testing.T) {
		h := newHeap(16)
		if _, _, err := LoadFlat(h, addrT, 8); err == nil {
			t.Error("a 32-byte tuple at 8 in a 16-byte heap loaded")
		}
	})
	t.Run("a string leaf is its pair as it lies in memory", func(t *testing.T) {
		h := newHeap(1 << 12)
		ptr, err := h.Realloc(0, 0, 4, 8)
		if err != nil {
			t.Fatal(err)
		}
		if serr := StoreVia(h, Str("hi"), ptr); serr != nil {
			t.Fatal(serr)
		}
		bits, _, err := LoadFlat(h, Type{Kind: KindString}, ptr)
		if err != nil {
			t.Fatal(err)
		}
		if len(bits) != 2 || bits[1] != 2 {
			t.Errorf("string words = %v, want (ptr, 2)", bits)
		}
	})
}

func ptrTo(v Value) *Value { return &v }
