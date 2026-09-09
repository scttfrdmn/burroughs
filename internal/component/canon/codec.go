// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import "fmt"

// ptrSize is the pointer width of an i32-memory component — the only memory kind this slice models.
const ptrSize = 4

// alignTo rounds ptr up to a multiple of a, matching the reference model's align_to.
func alignTo(ptr, a int) int { return (ptr + a - 1) / a * a }

// alignment is the byte alignment of a type in linear memory (CanonicalABI.md `alignment`).
func alignment(t Type) int {
	switch t.Kind {
	case KindBool, KindU8, KindS8:
		return 1
	case KindU16, KindS16:
		return 2
	case KindU32, KindS32, KindF32, KindChar, KindString, KindList:
		return 4
	case KindU64, KindS64, KindF64:
		return 8
	case KindVariant:
		return alignmentVariant(t.Cases)
	default:
		panic(fmt.Sprintf("canon: alignment: unmodeled kind %s", t.Kind))
	}
}

// size is the in-memory element size of a type (CanonicalABI.md `elem_size`). A string and a
// non-length-tagged list are each a (ptr, length) pair.
func size(t Type) int {
	switch t.Kind {
	case KindBool, KindU8, KindS8:
		return 1
	case KindU16, KindS16:
		return 2
	case KindU32, KindS32, KindF32, KindChar:
		return 4
	case KindU64, KindS64, KindF64:
		return 8
	case KindString, KindList:
		return 2 * ptrSize
	case KindVariant:
		return sizeVariant(t.Cases)
	default:
		panic(fmt.Sprintf("canon: size: unmodeled kind %s", t.Kind))
	}
}

// discriminantType is a variant's case-index integer type, sized to the case count (CanonicalABI.md
// discriminant_type): u8 for ≤256 cases, u16 for ≤65536, else u32.
func discriminantType(nCases int) Kind {
	switch {
	case nCases <= 1<<8:
		return KindU8
	case nCases <= 1<<16:
		return KindU16
	default:
		return KindU32
	}
}

func maxCaseAlignment(cases []Case) int {
	a := 1
	for _, c := range cases {
		if c.Type != nil {
			if ca := alignment(*c.Type); ca > a {
				a = ca
			}
		}
	}
	return a
}

func alignmentVariant(cases []Case) int {
	da := alignment(Type{Kind: discriminantType(len(cases))})
	if m := maxCaseAlignment(cases); m > da {
		return m
	}
	return da
}

func sizeVariant(cases []Case) int {
	s := size(Type{Kind: discriminantType(len(cases))})
	s = alignTo(s, maxCaseAlignment(cases))
	cs := 0
	for _, c := range cases {
		if c.Type != nil {
			if z := size(*c.Type); z > cs {
				cs = z
			}
		}
	}
	s += cs
	return alignTo(s, alignmentVariant(cases))
}

// flattenType is a type's core flat representation (CanonicalABI.md flatten_type).
func flattenType(t Type) []string {
	switch t.Kind {
	case KindBool, KindU8, KindU16, KindU32, KindS8, KindS16, KindS32, KindChar:
		return []string{"i32"}
	case KindU64, KindS64:
		return []string{"i64"}
	case KindF32:
		return []string{"f32"}
	case KindF64:
		return []string{"f64"}
	case KindString, KindList:
		return []string{"i32", "i32"}
	case KindVariant:
		return flattenVariant(t.Cases)
	default:
		panic(fmt.Sprintf("canon: flattenType: unmodeled kind %s", t.Kind))
	}
}

// flattenVariant is the discriminant's flat type followed by the join of the cases' flat types
// (CanonicalABI.md flatten_variant / join).
func flattenVariant(cases []Case) []string {
	var flat []string
	for _, c := range cases {
		if c.Type == nil {
			continue
		}
		for i, ft := range flattenType(*c.Type) {
			if i < len(flat) {
				flat[i] = join(flat[i], ft)
			} else {
				flat = append(flat, ft)
			}
		}
	}
	return append([]string{"i32"}, flat...)
}

func join(a, b string) string {
	switch {
	case a == b:
		return a
	case a == "i32" && b == "f32", a == "f32" && b == "i32":
		return "i32"
	default:
		return "i64"
	}
}

// intBytes is the fixed width an integer/bool/char stores as; 0 for a non-scalar.
func intBytes(k Kind) int {
	switch k {
	case KindBool, KindU8, KindS8:
		return 1
	case KindU16, KindS16:
		return 2
	case KindU32, KindS32, KindChar:
		return 4
	case KindU64, KindS64:
		return 8
	default:
		return 0
	}
}

// reallocCall records one realloc — its four arguments and the returned pointer — so the differential
// can compare Burroughs' allocation sequence to the reference model's, byte-for-byte.
type reallocCall struct {
	Args [4]int `json:"args"`
	Ret  int    `json:"ret"`
}

// heap is the bump allocator the differential runs against, identical to the reference model's Heap: a
// byte slice and a rising watermark, with realloc handing out aligned regions from it. It is the test
// harness's memory, not the engine's — the engine's realloc will be the guest's `cabi_realloc` (PR C).
type heap struct {
	mem       []byte
	lastAlloc int
	calls     []reallocCall
}

func newHeap(size int) *heap { return &heap{mem: make([]byte, size)} }

// realloc is the four-argument cabi_realloc contract (origPtr, origSize, align, newSize). Current
// callers all allocate fresh (origPtr 0); the grow path (utf16/latin1 string re-encode, list realloc)
// arrives with a non-zero origPtr in a later increment, so the parameters are the ABI's, not dead.
//
//nolint:unparam // origPtr/origSize are the cabi_realloc contract; non-zero callers land next increment
func (h *heap) realloc(origPtr, origSize, align, newSize int) (int, error) {
	if origPtr != 0 && newSize < origSize {
		ret := alignTo(origPtr, align)
		h.calls = append(h.calls, reallocCall{Args: [4]int{origPtr, origSize, align, newSize}, Ret: ret})
		return ret, nil
	}
	ret := alignTo(h.lastAlloc, align)
	h.lastAlloc = ret + newSize
	if h.lastAlloc > len(h.mem) {
		return 0, fmt.Errorf("canon: heap exhausted (need %d, have %d)", h.lastAlloc, len(h.mem))
	}
	copy(h.mem[ret:ret+origSize], h.mem[origPtr:origPtr+origSize])
	h.calls = append(h.calls, reallocCall{Args: [4]int{origPtr, origSize, align, newSize}, Ret: ret})
	return ret, nil
}

// Canonical NaN bit patterns (CanonicalABI.md; DETERMINISTIC_PROFILE in the reference model). A NaN is
// canonicalized on lower and lift; a non-NaN (including negative zero) is passed through unchanged.
const (
	canonicalNaN32 = 0x7fc00000
	canonicalNaN64 = 0x7ff8000000000000
)

// canonicalizeNaN32 returns bits unchanged unless they are a NaN, in which case the canonical NaN. A
// negative zero is not a NaN and is preserved.
func canonicalizeNaN32(bits uint32) uint32 {
	if bits&0x7f800000 == 0x7f800000 && bits&0x007fffff != 0 {
		return canonicalNaN32
	}
	return bits
}

func canonicalizeNaN64(bits uint64) uint64 {
	if bits&0x7ff0000000000000 == 0x7ff0000000000000 && bits&0x000fffffffffffff != 0 {
		return canonicalNaN64
	}
	return bits
}

// storeInt writes the low nbytes of v little-endian at ptr. A signed value is held in Value.u as its
// two's-complement bits, so the same low-byte copy serves signed and unsigned.
func (h *heap) storeInt(v uint64, ptr, nbytes int) {
	for i := range nbytes {
		h.mem[ptr+i] = byte(v >> (8 * i))
	}
}

// store writes v into h.mem at ptr (CanonicalABI.md `store`). ptr must be aligned and have room for
// size(v.Type); a string's or list's payload is allocated through realloc and its (ptr, length) pair is
// written at ptr.
func (h *heap) store(v Value, ptr int) error {
	if n := intBytes(v.Type.Kind); n > 0 {
		h.storeInt(v.u, ptr, n)
		return nil
	}
	switch v.Type.Kind {
	case KindF32:
		h.storeInt(uint64(canonicalizeNaN32(uint32(v.u))), ptr, 4)
		return nil
	case KindF64:
		h.storeInt(canonicalizeNaN64(v.u), ptr, 8)
		return nil
	case KindString:
		data := []byte(v.s)
		p, err := h.realloc(0, 0, 1, len(data))
		if err != nil {
			return err
		}
		copy(h.mem[p:p+len(data)], data)
		h.storeInt(uint64(p), ptr, ptrSize)
		h.storeInt(uint64(len(data)), ptr+ptrSize, ptrSize)
		return nil
	case KindList:
		p, err := h.storeListData(v)
		if err != nil {
			return err
		}
		h.storeInt(uint64(p), ptr, ptrSize)
		h.storeInt(uint64(len(v.list)), ptr+ptrSize, ptrSize)
		return nil
	case KindVariant:
		cases := v.Type.Cases
		discSize := size(Type{Kind: discriminantType(len(cases))})
		h.storeInt(v.u, ptr, discSize)
		if v.payload != nil {
			off := alignTo(ptr+discSize, maxCaseAlignment(cases))
			return h.store(*v.payload, off)
		}
		return nil
	default:
		return fmt.Errorf("canon: store: unmodeled kind %s", v.Type.Kind)
	}
}

// storeListData allocates the element region, stores each element into it, and returns its pointer.
func (h *heap) storeListData(v Value) (int, error) {
	es := size(*v.Type.Elem)
	p, err := h.realloc(0, 0, alignment(*v.Type.Elem), len(v.list)*es)
	if err != nil {
		return 0, err
	}
	for i, e := range v.list {
		if err := h.store(e, p+i*es); err != nil {
			return 0, err
		}
	}
	return p, nil
}

// flatVal is one lowered core value: its core type ("i32"/"i64"/"f32"/"f64") and its bits.
type flatVal struct {
	kind string
	bits uint64
}

// lowerFlat lowers v to the flat core-value sequence (CanonicalABI.md `lower_flat`). A string or list is
// stored to memory through realloc and lowered as its (pointer, length) pair.
func (h *heap) lowerFlat(v Value) ([]flatVal, error) {
	switch v.Type.Kind {
	case KindBool, KindU8, KindU16, KindU32, KindChar:
		return []flatVal{{"i32", v.u}}, nil
	case KindS8, KindS16, KindS32:
		return []flatVal{{"i32", uint64(uint32(v.u))}}, nil
	case KindU64, KindS64:
		return []flatVal{{"i64", v.u}}, nil
	case KindF32:
		return []flatVal{{"f32", uint64(canonicalizeNaN32(uint32(v.u)))}}, nil
	case KindF64:
		return []flatVal{{"f64", canonicalizeNaN64(v.u)}}, nil
	case KindString:
		data := []byte(v.s)
		p, err := h.realloc(0, 0, 1, len(data))
		if err != nil {
			return nil, err
		}
		copy(h.mem[p:p+len(data)], data)
		return []flatVal{{"i32", uint64(p)}, {"i32", uint64(len(data))}}, nil
	case KindList:
		p, err := h.storeListData(v)
		if err != nil {
			return nil, err
		}
		return []flatVal{{"i32", uint64(p)}, {"i32", uint64(len(v.list))}}, nil
	case KindVariant:
		return h.lowerFlatVariant(v)
	default:
		return nil, fmt.Errorf("canon: lowerFlat: unmodeled kind %s", v.Type.Kind)
	}
}

// lowerFlatVariant lowers a variant to [case_index] + the payload coerced to the joined flat types +
// zero padding for the slots a shorter case does not fill (CanonicalABI.md lower_flat_variant). The
// coercion is a widening only — a float bit pattern already lives in flatVal.bits, so every allowed
// (have, want) pair keeps the bits and takes the wider slot type.
func (h *heap) lowerFlatVariant(v Value) ([]flatVal, error) {
	cases := v.Type.Cases
	flatTypes := flattenVariant(cases)
	out := []flatVal{{"i32", v.u}} // flatTypes[0] is the "i32" discriminant
	rest := flatTypes[1:]
	if v.payload != nil {
		payload, err := h.lowerFlat(*v.payload)
		if err != nil {
			return nil, err
		}
		have := flattenType(v.payload.Type)
		for i, fv := range payload {
			c, err := coerceFlat(have[i], rest[i], fv)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		rest = rest[len(payload):]
	}
	for _, want := range rest {
		out = append(out, flatVal{want, 0})
	}
	return out, nil
}

// coerceFlat widens one payload flat value to the variant's joined slot type. Only the widenings the
// spec allows are legal (equal, or f32→i32, i32→i64, f32→i64, f64→i64); the bits are unchanged because
// flatVal already holds a float's bit pattern.
func coerceFlat(have, want string, fv flatVal) (flatVal, error) {
	if have == want ||
		(have == "f32" && want == "i32") ||
		(have == "i32" && want == "i64") ||
		(have == "f32" && want == "i64") ||
		(have == "f64" && want == "i64") {
		return flatVal{want, fv.bits}, nil
	}
	return flatVal{}, fmt.Errorf("canon: variant flat coercion %s->%s is not allowed", have, want)
}
