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
	case KindU32, KindS32, KindF32, KindChar, KindString, KindList, KindOwn, KindBorrow:
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
	case KindU32, KindS32, KindF32, KindChar, KindOwn, KindBorrow:
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
	case KindOwn, KindBorrow:
		return []string{"i32"}
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
	table     *resourceTable // the instance's handle table (own/borrow), CanonicalABI.md's Table
}

func newHeap(size int) *heap { return &heap{mem: make([]byte, size), table: newResourceTable()} }

// resourceHandle is one entry in a component instance's handle table.
type resourceHandle struct {
	rt       int
	rep      uint32
	own      bool
	numLends int // bumped only under a borrow scope (PR B); always 0 in PR A's own-only paths
}

// resourceTable is a component instance's handle table (CanonicalABI.md Table): a slot array whose
// index 0 is a reserved sentinel, plus a free list. add/remove/get mirror the reference model, so a
// lowered handle's index and the table's shape match byte-for-byte.
type resourceTable struct {
	array []*resourceHandle // array[0] is the reserved nil sentinel
	free  []int
}

func newResourceTable() *resourceTable { return &resourceTable{array: []*resourceHandle{nil}} }

func (tb *resourceTable) add(h *resourceHandle) int {
	if n := len(tb.free); n > 0 {
		i := tb.free[n-1]
		tb.free = tb.free[:n-1]
		tb.array[i] = h
		return i
	}
	i := len(tb.array)
	tb.array = append(tb.array, h)
	return i
}

func (tb *resourceTable) get(i int) (*resourceHandle, error) {
	if i < 0 || i >= len(tb.array) || tb.array[i] == nil {
		return nil, fmt.Errorf("canon: handle index %d is not live", i)
	}
	return tb.array[i], nil
}

func (tb *resourceTable) remove(i int) (*resourceHandle, error) {
	h, err := tb.get(i)
	if err != nil {
		return nil, err
	}
	tb.array[i] = nil
	tb.free = append(tb.free, i)
	return h, nil
}

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
	case KindOwn:
		h.storeInt(uint64(h.lowerOwn(v)), ptr, 4)
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
	case KindOwn:
		return []flatVal{{"i32", uint64(h.lowerOwn(v))}}, nil
	default:
		return nil, fmt.Errorf("canon: lowerFlat: unmodeled kind %s", v.Type.Kind)
	}
}

// lowerOwn adds an owned handle to the instance table and returns its index (CanonicalABI.md
// lower_own). num_lends starts at 0; the lend accounting is PR B's, at the borrow scope.
func (h *heap) lowerOwn(v Value) int {
	return h.table.add(&resourceHandle{rt: v.Type.RT, rep: uint32(v.u), own: true})
}

// load lifts a value from linear memory (CanonicalABI.md `load`), the inverse of store. A float's NaN
// is canonicalized on lift as on lower; an own handle is consumed from the table (lift_own).
func (h *heap) load(ptr int, t Type) (Value, error) {
	switch t.Kind {
	case KindBool:
		return Bool(h.loadInt(ptr, 1) != 0), nil
	case KindU8:
		return Value{Type: t, u: h.loadInt(ptr, 1)}, nil
	case KindU16:
		return Value{Type: t, u: h.loadInt(ptr, 2)}, nil
	case KindU32:
		return Value{Type: t, u: h.loadInt(ptr, 4)}, nil
	case KindU64:
		return Value{Type: t, u: h.loadInt(ptr, 8)}, nil
	case KindS8:
		return Value{Type: t, u: uint64(int64(int8(h.loadInt(ptr, 1))))}, nil
	case KindS16:
		return Value{Type: t, u: uint64(int64(int16(h.loadInt(ptr, 2))))}, nil
	case KindS32:
		return Value{Type: t, u: uint64(int64(int32(h.loadInt(ptr, 4))))}, nil
	case KindS64:
		return Value{Type: t, u: h.loadInt(ptr, 8)}, nil
	case KindF32:
		return Value{Type: t, u: uint64(canonicalizeNaN32(uint32(h.loadInt(ptr, 4))))}, nil
	case KindF64:
		return Value{Type: t, u: canonicalizeNaN64(h.loadInt(ptr, 8))}, nil
	case KindChar:
		return Char(rune(h.loadInt(ptr, 4)))
	case KindString:
		begin := int(h.loadInt(ptr, ptrSize))
		n := int(h.loadInt(ptr+ptrSize, ptrSize))
		return Str(string(h.mem[begin : begin+n])), nil
	case KindList:
		begin := int(h.loadInt(ptr, ptrSize))
		n := int(h.loadInt(ptr+ptrSize, ptrSize))
		es := size(*t.Elem)
		vals := make([]Value, n)
		for i := range n {
			ev, err := h.load(begin+i*es, *t.Elem)
			if err != nil {
				return Value{}, err
			}
			vals[i] = ev
		}
		return List(*t.Elem, vals...)
	case KindVariant:
		return h.loadVariant(ptr, t)
	case KindOwn:
		return h.liftOwn(int(h.loadInt(ptr, 4)), t)
	default:
		return Value{}, fmt.Errorf("canon: load: unmodeled kind %s", t.Kind)
	}
}

// loadVariant reads a variant's discriminant, aligns to the cases' max alignment, and loads the
// selected case's payload (CanonicalABI.md load_variant).
func (h *heap) loadVariant(ptr int, t Type) (Value, error) {
	cases := t.Cases
	discSize := size(Type{Kind: discriminantType(len(cases))})
	ci := int(h.loadInt(ptr, discSize))
	if ci >= len(cases) {
		return Value{}, fmt.Errorf("canon: load_variant: case index %d out of range", ci)
	}
	c := cases[ci]
	var payload *Value
	if c.Type != nil {
		off := alignTo(ptr+discSize, maxCaseAlignment(cases))
		pv, err := h.load(off, *c.Type)
		if err != nil {
			return Value{}, err
		}
		payload = &pv
	}
	return Variant(t, c.Name, payload)
}

// liftOwn consumes an owned handle at table index i (CanonicalABI.md lift_own), trapping a missing
// slot, a wrong resource type, a lent handle, or a borrow in an own position.
func (h *heap) liftOwn(i int, t Type) (Value, error) {
	hnd, err := h.table.remove(i)
	if err != nil {
		return Value{}, err
	}
	if hnd.rt != t.RT {
		return Value{}, fmt.Errorf("canon: lift_own: handle rt %d != expected %d", hnd.rt, t.RT)
	}
	if hnd.numLends != 0 {
		return Value{}, fmt.Errorf("canon: lift_own: handle has %d active lends", hnd.numLends)
	}
	if !hnd.own {
		return Value{}, fmt.Errorf("canon: lift_own: handle is a borrow, not an own")
	}
	return Own(t.RT, hnd.rep), nil
}

// coreValueIter is a cursor over a flat core-value sequence (CanonicalABI.md CoreValueIter). next
// coerces the slot's declared type to the wanted type — the inverse of the variant flat widening.
type coreValueIter struct {
	types []string
	vals  []uint64
	i     int
}

func (it *coreValueIter) next(want string) uint64 {
	have := it.types[it.i]
	v := it.vals[it.i]
	it.i++
	return coerce(have, want, v)
}

// coerce narrows a joined variant slot back to a payload's own flat type. Bits are preserved (a float
// already lives as its bit pattern); a wider integer slot is truncated to its low word.
func coerce(have, want string, bits uint64) uint64 {
	switch {
	case have == want, have == "i32" && want == "f32":
		return bits
	case have == "i64" && (want == "i32" || want == "f32"):
		return bits & 0xffffffff
	case have == "i64" && want == "f64":
		return bits
	default:
		panic(fmt.Sprintf("canon: coerce %s->%s is not a variant widening inverse", have, want))
	}
}

// liftFlat reconstructs a value from a flat core-value sequence (CanonicalABI.md lift_flat), the inverse
// of lowerFlat. A string or list reads its (pointer, length) from the flat values and its elements from
// memory; a variant reads the discriminant, lifts the selected case's payload with slot coercion, and
// drains the unused joined slots.
func (h *heap) liftFlat(it *coreValueIter, t Type) (Value, error) {
	switch t.Kind {
	case KindBool:
		return Bool(it.next("i32") != 0), nil
	case KindU8:
		return Value{Type: t, u: it.next("i32") & 0xff}, nil
	case KindU16:
		return Value{Type: t, u: it.next("i32") & 0xffff}, nil
	case KindU32:
		return Value{Type: t, u: it.next("i32") & 0xffffffff}, nil
	case KindU64:
		return Value{Type: t, u: it.next("i64")}, nil
	case KindS8:
		return Value{Type: t, u: uint64(int64(int8(it.next("i32"))))}, nil
	case KindS16:
		return Value{Type: t, u: uint64(int64(int16(it.next("i32"))))}, nil
	case KindS32:
		return Value{Type: t, u: uint64(int64(int32(it.next("i32"))))}, nil
	case KindS64:
		return Value{Type: t, u: it.next("i64")}, nil
	case KindF32:
		return Value{Type: t, u: uint64(canonicalizeNaN32(uint32(it.next("f32"))))}, nil
	case KindF64:
		return Value{Type: t, u: canonicalizeNaN64(it.next("f64"))}, nil
	case KindChar:
		return Char(rune(it.next("i32")))
	case KindString:
		begin := int(it.next("i32"))
		n := int(it.next("i32"))
		return Str(string(h.mem[begin : begin+n])), nil
	case KindList:
		begin := int(it.next("i32"))
		n := int(it.next("i32"))
		es := size(*t.Elem)
		vals := make([]Value, n)
		for i := range n {
			ev, err := h.load(begin+i*es, *t.Elem)
			if err != nil {
				return Value{}, err
			}
			vals[i] = ev
		}
		return List(*t.Elem, vals...)
	case KindVariant:
		total := flattenVariant(t.Cases)
		start := it.i
		ci := int(it.next("i32"))
		if ci >= len(t.Cases) {
			return Value{}, fmt.Errorf("canon: lift_flat_variant: case index %d out of range", ci)
		}
		c := t.Cases[ci]
		var payload *Value
		if c.Type != nil {
			pv, err := h.liftFlat(it, *c.Type)
			if err != nil {
				return Value{}, err
			}
			payload = &pv
		}
		it.i = start + len(total) // drain the unused joined slots
		return Variant(t, c.Name, payload)
	case KindOwn:
		return h.liftOwn(int(it.next("i32")), t)
	default:
		return Value{}, fmt.Errorf("canon: liftFlat: unmodeled kind %s", t.Kind)
	}
}

// loadInt reads the low nbytes little-endian at ptr.
func (h *heap) loadInt(ptr, nbytes int) uint64 {
	var v uint64
	for i := range nbytes {
		v |= uint64(h.mem[ptr+i]) << (8 * i)
	}
	return v
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
