// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"fmt"
	"unicode/utf8"
)

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
	case KindTuple:
		return alignmentTuple(t.Fields)
	default:
		panic(fmt.Sprintf("canon: alignment: unmodeled kind %s", t.Kind))
	}
}

// alignmentTuple is a tuple's alignment: the max of its fields' (CanonicalABI.md, an empty tuple is 1).
func alignmentTuple(fields []Type) int {
	a := 1
	for i := range fields {
		if fa := alignment(fields[i]); fa > a {
			a = fa
		}
	}
	return a
}

// sizeTuple is a tuple's size: each field placed at its aligned offset, the whole aligned to the tuple's
// alignment (CanonicalABI.md `record`/`tuple` layout).
func sizeTuple(fields []Type) int {
	s := 0
	for i := range fields {
		s = alignTo(s, alignment(fields[i]))
		s += size(fields[i])
	}
	return alignTo(s, alignmentTuple(fields))
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
	case KindTuple:
		return sizeTuple(t.Fields)
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

// Heap is the memory a value lowering allocates in and writes through: the model's `[]byte` bump heap in
// the differential, and guest linear memory in the host (ADR 0084 / #719). Extracting it lets one
// lowering — [StoreString] here, the list/record lowerings as #718 extends it — run against the
// definitions.py fixtures and against a live guest without a second implementation to drift from (ADR
// 0083's implement-once). All offsets are `int` byte positions into the heap.
type Heap interface {
	Realloc(origPtr, origSize, align, newSize int) (int, error)
	WriteBytes(ptr int, data []byte) error
	StoreInt(v uint64, ptr, nbytes int) error
}

// The model `*heap` is a [Heap]: its writes never fail (in-bounds by construction), so the error arms
// are nil — a guest heap's are where an out-of-bounds guest pointer surfaces.
func (h *heap) Realloc(origPtr, origSize, align, newSize int) (int, error) {
	return h.realloc(origPtr, origSize, align, newSize)
}

func (h *heap) WriteBytes(ptr int, data []byte) error {
	copy(h.mem[ptr:ptr+len(data)], data)
	return nil
}

func (h *heap) StoreInt(v uint64, ptr, nbytes int) error {
	h.storeInt(v, ptr, nbytes)
	return nil
}

// The model `*heap` is also a [ReadHeap]. **Its bounds check is not decoration** even though the
// differential's fixtures are in range: the check is what the string lift delegates its def:1385 trap to,
// so a heap that panicked instead would make the trap untestable against this heap — and the traps are
// tested here, where a byte slice can be set up out of range, rather than only against a live guest.
func (h *heap) ReadBytes(ptr, n int) ([]byte, error) {
	if ptr < 0 || n < 0 || ptr+n > len(h.mem) || ptr+n < 0 {
		return nil, fmt.Errorf("canon: read of %d byte(s) at %d is outside a %d-byte heap", n, ptr, len(h.mem))
	}
	return h.mem[ptr : ptr+n], nil
}

// ReadHeap is the memory a value **lifting** reads through — the mirror of [Heap], which only writes.
//
// # Why this did not exist until #903
//
// Every use of this codec so far lowered: the host answers a guest through `StoreString`/`StoreVia`
// against guest memory, and the differential lowers into the model heap. **Lifting had no abstraction at
// all** — `load` and `liftFlat` are methods on the unexported `*heap` and slice `h.mem` directly, so
// guest-to-host lifting of a compound value was not implementable outside this package. `streamWrite`
// reads a guest's `list<u8>` with a raw `CanonCaller.Read` for exactly that reason, while its own comment
// claimed it lifted "through canon load over u8".
//
// It is a separate interface from [Heap] rather than three more methods on it, because the two
// capabilities have different holders: a lowering needs a realloc and is performed by whoever owns the
// allocation, while a lifting needs only to read and is performed by whoever owns the pointer. A host
// lifting a `task.return` value has no business reallocating in the guest.
type ReadHeap interface {
	// ReadBytes returns n bytes at ptr, or an error if that range is not readable. **The error is the
	// bounds check**: the model traps when `ptr + byte_length > len(memory)` (definitions.py:1385), and an
	// implementation over a Go slice must not be allowed to panic instead.
	ReadBytes(ptr, n int) ([]byte, error)
}

// MaxStringByteLength is the model's cap on a lifted string's byte length (definitions.py:1360), which it
// traps past (def:1383). It bounds an allocation this process would otherwise make on a guest's word.
const MaxStringByteLength = (1 << 28) - 1

// LoadStringFromRange lifts a `string` from a (pointer, byte-length) range — the model's
// `load_string_from_range` for the `utf8` encoding (definitions.py:1363-1389).
//
// **It is the one string lift, and it implements all four of the model's traps**, none of which the
// codec performed before #903:
//
//  1. byte length past [MaxStringByteLength] (def:1383);
//  2. a misaligned pointer (def:1384) — vacuous at utf8's alignment of 1, and present because the arm
//     that makes it non-vacuous is utf16's, which this engine does not yet carry;
//  3. the range not within memory (def:1385) — delegated to [ReadHeap.ReadBytes], which is why that
//     method returns an error;
//  4. bytes that are not valid UTF-8 (def:1386-1389, `except UnicodeError: trap()`).
//
// All four were unreachable while the only heap was the differential's, whose fixtures are well-formed
// by construction. Against guest memory every one is reachable from a guest's own word, and three are
// worse than a wrong answer: an out-of-range slice panics, and invalid bytes produce a Go string that
// silently is not UTF-8.
//
// The `utf8` encoding is the only one this engine carries; a component declaring `utf16` or
// `latin1+utf16` is refused by name at the canonopt, not here.
func LoadStringFromRange(h ReadHeap, ptr, byteLength int) (Value, error) {
	if byteLength < 0 || byteLength > MaxStringByteLength {
		return Value{}, fmt.Errorf("canon: string byte length %d is out of range (max %d)",
			byteLength, MaxStringByteLength)
	}
	if ptr < 0 {
		return Value{}, fmt.Errorf("canon: string pointer %d is negative", ptr)
	}
	// utf8's alignment is 1, so every pointer satisfies it. Written as the model writes it so the
	// utf16 arm has somewhere to land rather than being remembered.
	const alignment = 1
	if ptr != alignTo(ptr, alignment) {
		return Value{}, fmt.Errorf("canon: string pointer %d is not aligned to %d", ptr, alignment)
	}
	data, err := h.ReadBytes(ptr, byteLength)
	if err != nil {
		return Value{}, fmt.Errorf("canon: reading a %d-byte string at %d: %w", byteLength, ptr, err)
	}
	if !utf8.Valid(data) {
		return Value{}, fmt.Errorf("canon: the %d bytes at %d are not valid UTF-8", byteLength, ptr)
	}
	return Str(string(data)), nil
}

// LiftFlatScalar lifts a scalar, `bool` or `char` value of type t from the single core word it occupies.
//
// # Why this is exported and why there is exactly one of it
//
// It is the codec's own flat-lift arms, extracted (#903) so that the component layer's `task.return` can
// lift its result **eagerly** without reimplementing them. It had reimplemented one of them: `u32` alone,
// which was a regression the moment it shipped, because a non-`u32` scalar from an async cross-component
// child used to resolve through a path with arms for `bool` and all eight integers. A partial switch over
// a closed enum is a defect waiting for its first caller.
//
// **So the differential is this function's oracle.** `TestCodecMatchesReferenceModel` drives `liftFlat`
// over every case in `gen/cases.json` against the flat types and values `definitions.py` emits, and
// `liftFlat` now reaches every scalar through here. The conversions are therefore verified against the
// model rather than against a table someone wrote out, which is the whole reason for extracting rather
// than copying.
//
// The conversions, with the model's rules:
//
//   - the unsigned integers are `lift_flat_unsigned` (definitions.py:1914-1917): the word modulo the
//     target width, which a Go truncating conversion is;
//   - the signed integers are `lift_flat_signed` (def:1919-1925): the same reduction, then the top bit
//     read as the sign, which a Go conversion through the sized signed type is;
//   - `bool` is `convert_int_to_bool` (def:1311-1313) — **nonzero is true**, `bool(i)` and not `i == 1`,
//     so a guest returning 2 for true is returning true;
//   - `char` is `convert_i32_to_char` (def:1343-1347), which **traps** past the last code point or inside
//     the surrogate range — delegated to [Char], which applies exactly those two conditions, so the
//     refusal is the constructor's and cannot drift from it;
//   - `f32`/`f64` canonicalize a NaN (def:1319-1341), because a guest may hand over any of the many NaN
//     bit patterns and the ABI admits one.
//
// A non-scalar kind is refused by name: it is not a one-word value and has no business here.
func LiftFlatScalar(t Type, word uint64) (Value, error) {
	switch t.Kind {
	case KindBool:
		return Bool(word != 0), nil
	case KindU8:
		return Value{Type: t, u: word & 0xff}, nil
	case KindU16:
		return Value{Type: t, u: word & 0xffff}, nil
	case KindU32:
		return Value{Type: t, u: word & 0xffffffff}, nil
	case KindU64:
		return Value{Type: t, u: word}, nil
	case KindS8:
		return Value{Type: t, u: uint64(int64(int8(word)))}, nil
	case KindS16:
		return Value{Type: t, u: uint64(int64(int16(word)))}, nil
	case KindS32:
		return Value{Type: t, u: uint64(int64(int32(word)))}, nil
	case KindS64:
		return Value{Type: t, u: word}, nil
	case KindF32:
		return Value{Type: t, u: uint64(canonicalizeNaN32(uint32(word)))}, nil
	case KindF64:
		return Value{Type: t, u: canonicalizeNaN64(word)}, nil
	case KindChar:
		// The word is read UNSIGNED. The model asserts `i >= 0` because its iterator yields the unsigned
		// word, so a word with the top bit set is a large code point to be trapped — not a negative rune
		// that would slip past an `i >= 0x110000` test.
		return Char(rune(uint32(word)))
	default:
		return Value{}, fmt.Errorf("canon: %s is not a scalar and does not lift from a single core word", t.Kind)
	}
}

// ListByteLength is `count × elem_size` for a list, computed so it cannot overflow and bounded the way
// the model bounds it.
//
// # Why an overflow check where the model needs none
//
// `store_list_into_range` computes `len(v) * elem_size(...)` and asserts the product is at most
// `REALLOC_I32_MAX` (definitions.py:1711-1713). Python integers do not overflow, so the model's `assert`
// is the whole of its guard. **Go's do**, and the count on the lifting side is a word the **guest**
// supplies — so the multiplication is attacker-controlled arithmetic and the product must be checked
// before it is used as a length.
//
// The check is the division form rather than "multiply and see if it got smaller": the latter is
// undefined-adjacent reasoning about wraparound, and on a 64-bit `int` a product can wrap past zero and
// land on a *plausible small positive*, which is the value a bounds check would then wave through.
//
// `ReallocI32Max` is the model's own bound (def:1359), so a list whose bytes cannot be addressed by the
// ABI's 32-bit pointer space is refused here rather than at a realloc that would be asked for a quantity
// it cannot express.
func ListByteLength(count, elemSize int) (int, error) {
	if count < 0 {
		return 0, fmt.Errorf("canon: list count %d is negative", count)
	}
	if elemSize < 0 {
		return 0, fmt.Errorf("canon: list element size %d is negative", elemSize)
	}
	if elemSize == 0 {
		// A zero-size element makes the byte length zero for any count, which is a legitimate shape (an
		// empty tuple's) and must not divide by zero below.
		return 0, nil
	}
	if count > ReallocI32Max/elemSize {
		return 0, fmt.Errorf("canon: a list of %d elements of %d bytes needs %d×%d bytes, past the %d-byte "+
			"maximum the Canonical ABI's 32-bit pointer space can address",
			count, elemSize, count, elemSize, ReallocI32Max)
	}
	return count * elemSize, nil
}

// ReallocI32Max is the model's `REALLOC_I32_MAX` (definitions.py:1359): the largest byte count the ABI's
// 32-bit pointer space can address.
const ReallocI32Max = 1<<32 - 1

// LoadList lifts a `list<T>` given its (pointer, count), with the per-element load **injected** — the
// mirror of [StoreList]'s injected element store.
//
// # Why injected rather than switched on the element kind
//
// `StoreList` takes its element store for a stated reason: the model heap can lower any element kind
// through its own `store`, while a guest heap lowers only the composable ones, and the **framing** —
// stride, the backing allocation, the header offsets — is shared regardless. The lifting side has the
// same split and the same framing, so it takes the same shape. That is what lets `list<string>` and
// later `list<record>` reuse one framing instead of each growing a loop of its own.
//
// What is shared and verified here: the byte length and its overflow guard, the stride, and the refusal
// of a count the pointer space cannot address. What the caller supplies is how to read one element at an
// offset.
//
// **The element's stride is `size(elem)`, derived rather than passed**, so a caller cannot disagree with
// the codec about layout — which is the mistake [LoadListU8]'s own guard exists to catch for its one
// reduced case.
func LoadList(h ReadHeap, ptr, count int, elem Type, loadElem func(int) (Value, error)) (Value, error) {
	es := size(elem)
	byteLen, err := ListByteLength(count, es)
	if err != nil {
		return Value{}, err
	}
	if ptr < 0 {
		return Value{}, fmt.Errorf("canon: list pointer %d is negative", ptr)
	}
	if a := alignment(elem); a > 0 && ptr%a != 0 {
		// definitions.py:1715's trap, on the lifting side: a list's data must sit at its element's
		// alignment. Non-vacuous as soon as the element is wider than a byte, unlike a string's.
		return Value{}, fmt.Errorf("canon: list pointer %d is not aligned to %d", ptr, a)
	}
	// **Bounds-checked once, over the whole span**, before any element is read — so a count that runs off
	// the end is refused rather than discovered partway through a loop that has already allocated.
	if _, rerr := h.ReadBytes(ptr, byteLen); rerr != nil {
		return Value{}, fmt.Errorf("canon: reading a %d-element list at %d (%d bytes): %w",
			count, ptr, byteLen, rerr)
	}
	vals := make([]Value, count)
	for i := range count {
		v, lerr := loadElem(ptr + i*es)
		if lerr != nil {
			return Value{}, fmt.Errorf("canon: list element %d at %d: %w", i, ptr+i*es, lerr)
		}
		vals[i] = v
	}
	return List(elem, vals...)
}

// LoadListU8 lifts the elements of a `list<u8>` given its (pointer, count) — the one case where the
// model's per-element load loop reduces to a contiguous read, because a `u8` is one byte at a one-byte
// stride. It is the lifting counterpart of the framing [StoreList] owns.
//
// **Separate from a general list lift, deliberately.** A general one needs a per-element load the way
// `StoreList` takes a per-element store, and no caller needs that yet; `list<u8>` has a caller today
// (a guest's stream write) and reduces exactly. A wider list lift arrives with the consumer that needs
// it, and when it does, this stays as the fast path or goes — it is not a shape to generalise on spec.
func LoadListU8(h ReadHeap, ptr, count int) ([]byte, error) {
	if ptr < 0 || count < 0 {
		return nil, fmt.Errorf("canon: list<u8> at %d with count %d is out of range", ptr, count)
	}
	// A u8's size and alignment are both 1, derived rather than written as literals so a change to the
	// codec's own tables reaches here. `alignTo` is then the identity, which is the honest reason there is
	// no alignment trap on this path.
	elem := Type{Kind: KindU8}
	if s, a := size(elem), alignment(elem); s != 1 || a != 1 {
		return nil, fmt.Errorf("canon: list<u8> fast path assumes a 1-byte element at 1-byte alignment, "+
			"but u8 now sizes %d and aligns %d — the contiguous read is no longer the element loop", s, a)
	}
	return h.ReadBytes(ptr, count)
}

// LoadString lifts a `string` from the (pointer, length) PAIR stored at ptr — the model's `load_string`
// (definitions.py:1351-1354), which reads the two words and defers to [LoadStringFromRange]. Separate
// from that function because the flat lifting gets its two words from the core value iterator rather
// than from memory, and only the range is common to both.
func LoadString(h ReadHeap, ptr int) (Value, error) {
	hdr, err := h.ReadBytes(ptr, 2*ptrSize)
	if err != nil {
		return Value{}, fmt.Errorf("canon: reading a string header at %d: %w", ptr, err)
	}
	var begin, n uint64
	for i := range ptrSize {
		begin |= uint64(hdr[i]) << (8 * i)
		n |= uint64(hdr[ptrSize+i]) << (8 * i)
	}
	return LoadStringFromRange(h, int(begin), int(n))
}

// StoreString lowers s as a `string` (CanonicalABI.md `store_string`, utf-8): its bytes are allocated
// through the heap's realloc (align 1) and its (ptr, length) pair is written at ptr. This is the one
// string lowering — the codec's `store` calls it for `KindString`, so the definitions.py string fixtures
// (`string-hello`/`-empty`/`-utf8`) verify it, and the host's canon adapter calls it against guest memory
// (#719), so the bytes the guest reads are lowered by the same verified code, not a hand path.
func StoreString(h Heap, s string, ptr int) error {
	data := []byte(s)
	p, err := h.Realloc(0, 0, 1, len(data))
	if err != nil {
		return err
	}
	if len(data) > 0 {
		if err := h.WriteBytes(p, data); err != nil {
			return err
		}
	}
	if err := h.StoreInt(uint64(p), ptr, ptrSize); err != nil {
		return err
	}
	return h.StoreInt(uint64(len(data)), ptr+ptrSize, ptrSize)
}

// StoreList lowers a `list<T>` (CanonicalABI.md `store_list`): the element backing is allocated through
// the heap's realloc (element size × count, element alignment), each element is lowered into it by
// `storeElem`, and the (ptr, count) pair is written at ptr. This is the one list **framing** — the
// codec's `store` calls it for `KindList` with the full element store, and the host's canon adapter
// calls it against guest memory (#718) with a heap-composable one; the framing (stride, per-element
// realloc, header offsets) — the part a hand lowering gets subtly wrong — is thus verified by the
// definitions.py list fixtures rather than reimplemented. `storeElem` is injected because the model heap
// can lower any element kind (through `*heap.store`) while a guest heap lowers only the composable ones
// ([StoreVia]); the framing is shared regardless.
func StoreList(h Heap, v Value, ptr int, storeElem func(Value, int) error) error {
	p, err := storeListData(h, v, storeElem)
	if err != nil {
		return err
	}
	if err := h.StoreInt(uint64(p), ptr, ptrSize); err != nil {
		return err
	}
	return h.StoreInt(uint64(len(v.list)), ptr+ptrSize, ptrSize)
}

// StoreVia lowers a value through a [Heap] for the kinds a guest lowering composes — integers, `string`,
// and `list` of those (recursively). It is the host's element store: `get-arguments`'s `list<string>`
// lowers through it against guest memory, the same [StoreList]/[StoreString] the model heap runs. A kind
// that needs the concrete heap (own/variant/float NaN-canonicalization) is not composable here and
// refuses by name — guest-driven, a later guest that lowers one extends this rather than replaces it.
func StoreVia(h Heap, v Value, ptr int) error {
	if n := intBytes(v.Type.Kind); n > 0 {
		return h.StoreInt(v.u, ptr, n)
	}
	switch v.Type.Kind {
	case KindString:
		return StoreString(h, v.s, ptr)
	case KindList:
		return StoreList(h, v, ptr, func(e Value, p int) error { return StoreVia(h, e, p) })
	case KindVariant:
		return StoreVariant(h, v, ptr, func(e Value, p int) error { return StoreVia(h, e, p) })
	case KindOwn:
		// The guest heap writes an already-minted handle (the host mints it before building the value,
		// so `v.u` is the handle index, not a rep). The model heap's KindOwn assigns the index via its
		// table instead; both write the i32 at `ptr`.
		return h.StoreInt(v.u, ptr, 4)
	default:
		return fmt.Errorf("canon: StoreVia: kind %s is not heap-composable (guest-driven; the concrete heap lowers it)", v.Type.Kind)
	}
}

// StoreVariant lowers a `variant`/`result` (CanonicalABI.md `store_variant`): the discriminant, then the
// selected case's payload — via `storeElem` — at the case offset (aligned to the cases' max alignment).
// `store`'s `KindVariant` calls it with the full element store (so the definitions.py variant/result
// fixtures verify the framing), and `StoreVia` calls it against guest memory with the guest element
// store; the framing is shared regardless (ADR 0083, #724).
func StoreVariant(h Heap, v Value, ptr int, storeElem func(Value, int) error) error {
	cases := v.Type.Cases
	discSize := size(Type{Kind: discriminantType(len(cases))})
	if err := h.StoreInt(v.u, ptr, discSize); err != nil {
		return err
	}
	if v.payload != nil {
		off := alignTo(ptr+discSize, maxCaseAlignment(cases))
		return storeElem(*v.payload, off)
	}
	return nil
}

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
// (Reached now through the `Heap.Realloc` wrapper too, whose interface signature keeps every parameter.)
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
		// The one string lowering, shared with the host's canon adapter (#719): `*heap` is a Heap, so
		// this is the same code the guest-memory heap runs, verified by the string fixtures.
		return StoreString(h, v.s, ptr)
	case KindList:
		// The one list framing, shared with the host's canon adapter (#718): `*heap` is a Heap and
		// `h.store` is the full element store, so this is the same StoreList the guest-memory heap runs,
		// verified by the definitions.py list fixtures.
		return StoreList(h, v, ptr, h.store)
	case KindVariant:
		// The one variant framing, shared with the host's canon adapter (#724): `h.store` is the full
		// payload store, so this is the same StoreVariant the guest-memory heap runs, verified by the
		// definitions.py variant/result fixtures.
		return StoreVariant(h, v, ptr, h.store)
	case KindOwn:
		// The model heap assigns the handle index through its own table (lower_own); the guest heap
		// receives an already-minted handle (StoreVia's KindOwn). Both write an i32 — the same encoding.
		h.storeInt(uint64(h.lowerOwn(v)), ptr, 4)
		return nil
	default:
		return fmt.Errorf("canon: store: unmodeled kind %s", v.Type.Kind)
	}
}

// storeListData allocates the element region through the heap's realloc, stores each element into it via
// storeElem, and returns its pointer — the shared core of a list store ([StoreList], which adds the
// (ptr, count) header) and a list flat-lower (which returns the pointer and count as flat values). One
// element loop over a [Heap], not one per operation.
func storeListData(h Heap, v Value, storeElem func(Value, int) error) (int, error) {
	elem := *v.Type.Elem
	es := size(elem)
	p, err := h.Realloc(0, 0, alignment(elem), len(v.list)*es)
	if err != nil {
		return 0, err
	}
	for i, e := range v.list {
		if err := storeElem(e, p+i*es); err != nil {
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
		p, err := storeListData(h, v, h.store)
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
		// Through the one string lift (#903), where this sliced `h.mem` directly. The differential's
		// fixtures are well-formed so the traps never fired here, which is exactly why they were missing
		// when a guest heap arrived.
		return LoadString(h, ptr)
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
	case KindBool, KindU8, KindU16, KindU32, KindU64, KindS8, KindS16, KindS32, KindS64,
		KindF32, KindF64, KindChar:
		// **One scalar flat lift** (#903), where these were thirteen arms here and a second set of the
		// same conversions in the component layer's `task.return`. The core type to pull is the kind's own
		// flat type rather than a literal per arm — `flattenType` is the authority on which word a scalar
		// occupies, so an arm cannot disagree with it.
		return LiftFlatScalar(t, it.next(flattenType(t)[0]))
	case KindString:
		// Through the one string lift (#903). The flat form supplies the two words from the core value
		// iterator rather than from memory, which is why it calls the range function and `load` calls the
		// header one.
		begin := int(it.next("i32"))
		n := int(it.next("i32"))
		return LoadStringFromRange(h, begin, n)
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
