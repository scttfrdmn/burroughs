// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The array family — `fb 06`-`fb 13`, decision 0020's rung 3, fourteen arms over
// `eval.ml:711-926` and `aggr.ml`'s `alloc_array`/`array_length`.
//
// # An array is a gcObj, and that was 0020's plan rather than a convenience found later
//
// `gcObj.fields`'s own comment already said "for an array exactly the elements (rung 3's
// consumer)", so this rung adds no representation. The reference agrees at the same seam —
// `Aggr.Struct of deftype * field list` and `Aggr.Array of deftype * field list`, one constructor
// each over the *same* `field list` (aggr.ml:8-9), with `alloc_array dt vs` mapping one `ft` over
// every element where `alloc_struct` zips a list of them. So the difference between the two rungs
// is entirely in the *type* side: a struct's field type is looked up by index, an array's is the
// comptype's single content, which is why `arrayType` returns both and there is no `fieldStorage`
// call anywhere below.
//
// The consequence worth naming: **`popField`, `defaultField` and `pushField` are reused verbatim**,
// including the packed and v128 cases and both `read_field` `failwith`s. An `(array i8)` and a
// `(struct (field i8))` narrow, extend and refuse identically because in the reference they are the
// same three functions, and re-deriving them here would be the wrapped-arm grave's shape one
// package over (#105: *a same-shaped problem next door is a place to read, not a place to invent*).
//
// # Two trap strings, and the segment traps borrow someone else's
//
// `null array reference` and `out of bounds array access` are the family's own, both asserted
// verbatim by `array.wast` and therefore oracle-covered (#38's carve-out). The two segment arms are
// the interesting ones: `array.new_data` out of bounds reports **`out of bounds memory access`** and
// `array.new_elem` reports **`out of bounds table access`**, because the reference raises
// `Memory.Bounds`/`Table.Bounds` and renders them through `memory_error`/`table_error`
// (eval.ml:22-34) even though no memory or table is involved. `array.wast:209` and `:283` assert
// exactly that, so a "sensible" array-flavoured string here would fail four vectors while looking
// more correct than the specification.
//
// # The bounds check runs before the zero-length exit, five times
//
// `array_oob a i n` is `oob` over 64-bit zero-extensions of i, n and the length (eval.ml:174-176),
// which is `outOfBounds`, unchanged from the bulk family — and every arm that takes an `n` checks it
// **before** the `n = 0` exit. That ordering is the early-return grave (#41) and `bulk.go`'s own
// note explains why it cannot be reversed: a zero-length run at exactly the end is in bounds and one
// element past it is not, so an `if n == 0 { return nil }` opening skips the check it stands in
// front of. `array_fill.wast` and `array_copy.wast` both assert the zero-length-at-the-edge cases.
//
// # Allocation is unbounded, which is §1's non-goal and not an oversight
//
// `array.new $t (i32.const 0x8000_0000)` asks for two billion elements and this engine will ask Go
// for them. The reference does the same — `Lib.List32.make n arg` allocates a two-billion-element
// list — and the specification defines **no** trap for an allocation that cannot be served, so
// inventing one would be an accept-direction divergence (§9 G-3) with an arbitrary threshold. No
// corpus vector reaches it: `array.wast`'s four `new-overflow` rows use `array.new_data`/`new_elem`,
// whose *segment* bounds check refuses `0x8000_0000` before any allocation happens, and there is no
// interpreter fuzz target. Contract §1 names v0 hardening as a non-goal; this is one of the things
// that names.

// trapNullArray is `Trapping "null array reference"` (eval.ml:767, :776, :790, and five more) — the
// trap every array arm raises on a null operand.
//
// Its own constant beside `trapNullStruct` rather than a shared one, because the strings genuinely
// differ ("structure" spelled out, "array" not) and a shared constant would have to pick. Both are
// oracle-covered: `array.wast:191` and the nine `null array reference` assertions across the family
// say this text verbatim.
var trapNullArray = &Trap{Reason: "null array reference"}

// trapOOBArray is `Trapping "out of bounds array access"` — asserted 27 times across the family.
//
// Distinct from `trapOOB` (memory) and `trapOOBTable`, and the distinction is load-bearing in both
// directions: an array index out of range must say "array", while `array.new_data`'s segment
// overflow must **not** — see the file comment.
var trapOOBArray = &Trap{Reason: "out of bounds array access"}

// The 0xfb region's array sub-opcodes. Named for `opTableFB`'s reason: `fb 0c` and `fb 0d` differ by
// one bit and by whether the read sign-extends, which is the whole question a reader has.
const (
	opArrayNew        = 0x06
	opArrayNewDefault = 0x07
	opArrayNewFixed   = 0x08
	opArrayNewData    = 0x09
	opArrayNewElem    = 0x0a
	opArrayGet        = 0x0b
	opArrayGetS       = 0x0c
	opArrayGetU       = 0x0d
	opArraySet        = 0x0e
	opArrayLen        = 0x0f
	opArrayFill       = 0x10
	opArrayCopy       = 0x11
	opArrayInitData   = 0x12
	opArrayInitElem   = 0x13
)

// execArrayNew allocates an array of n copies of one initializer — `eval.ml:711-723`.
//
// **`n` is on top and the initializer underneath it** — `Num (I32 n) :: vs'`, with `List.hd vs'` the
// element — which is the opposite of the reading a text-form `(array.new $t (i32.const 7) (i32.const
// 3))` suggests, where the value is written first. A wrong order here is only visible when the two
// operands differ in *array*: for `(array i32)` both are numeric and swapping them produces a
// plausible answer, which is why `array.wast:59`'s `(array.new $vec (f64.const 7) (i32.const 3))` —
// an f64 element with an i32 count — is the row that cannot be satisfied by the wrong reading.
func (in *Instance) execArrayNew(ins binary.Instr, st *stack) error {
	ft, err := in.arrayType("array.new", ins.Imm0)
	if err != nil {
		return err
	}
	if short := st.needNum(1); short != nil {
		return short
	}
	n := uint64(uint32(st.popI32()))
	f, err := popField(ft, st)
	if err != nil {
		return err
	}
	fields := make([]gcField, n)
	for i := range fields {
		fields[i] = f
	}
	st.pushRef(ref{Obj: in.alloc(ins.Imm0, fields)})
	return nil
}

// execArrayNewDefault allocates an array of n defaults — `eval.ml:711-723`'s `Implicit` arm.
//
// A non-defaultable element — a non-nullable reference — is `defaultField`'s error and therefore
// #9's, per `Crash.error "non-defaultable type"`. Note the reference computes the default *before*
// it knows n is usable, and the order is unobservable: both failures discard the stack.
func (in *Instance) execArrayNewDefault(ins binary.Instr, st *stack) error {
	ft, err := in.arrayType("array.new_default", ins.Imm0)
	if err != nil {
		return err
	}
	f, err := defaultField(ft)
	if err != nil {
		return err
	}
	if short := st.needNum(1); short != nil {
		return short
	}
	n := uint64(uint32(st.popI32()))
	fields := make([]gcField, n)
	for i := range fields {
		fields[i] = f
	}
	st.pushRef(ref{Obj: in.alloc(ins.Imm0, fields)})
	return nil
}

// execArrayNewFixed allocates an array from n stack operands — `eval.ml:725-731`.
//
// **`split n` then `List.rev args`**, which is `struct.new`'s downward loop for the same reason: the
// *last* element's initializer is on top of the stack, so iterating from `n-1` down to 0 pops in
// exactly declaration order. `array.wast:26`'s `(array.new_fixed $vec 3 (i32.const 1) (i32.const 2)
// (i32.const 3))` is the vector that distinguishes the two orders, its three elements differing only
// in value.
//
// `n` is the **immediate** here and not an operand (`imms: {immIdx, immU32}`), which is the one place
// in the family where the count is known before the stack is touched.
func (in *Instance) execArrayNewFixed(ins binary.Instr, st *stack) error {
	ft, err := in.arrayType("array.new_fixed", ins.Imm0)
	if err != nil {
		return err
	}
	fields := make([]gcField, ins.Imm1)
	for i := len(fields) - 1; i >= 0; i-- {
		f, err := popField(ft, st)
		if err != nil {
			return err
		}
		fields[i] = f
	}
	st.pushRef(ref{Obj: in.alloc(ins.Imm0, fields)})
	return nil
}

// execArrayNewData allocates an array from a data segment's bytes — `eval.ml:748-765`.
//
// **The bound is in bytes, not elements**: `m_64 = n * storage_size st`, so `(array i16)` reading 3
// elements needs 6 bytes and a segment of 5 traps. Checking `n` against the segment's *length*
// instead would accept `array_new_data.wast:80`'s out-of-range read for every element type wider
// than a byte — the units slip `storageSize`'s comment names, here with a bucket behind it.
//
// The multiply is done in 64 bits from two 32-bit zero-extensions, so it cannot wrap: `n` is at most
// 2^32-1 and `storage_size` at most 16.
func (in *Instance) execArrayNewData(ins binary.Instr, st *stack) error {
	ft, err := in.arrayType("array.new_data", ins.Imm0)
	if err != nil {
		return err
	}
	seg, err := in.dataFor("array.new_data", ins.Imm1)
	if err != nil {
		return err
	}
	width, err := storageSize("array.new_data", ft.Storage)
	if err != nil {
		return err
	}
	if short := st.needNum(2); short != nil {
		return short
	}
	n := st.popNum()
	src := st.popNum()
	// One image load for the bound and the reads both (decision 0065): `size()` is itself a load, so
	// bounding against it and then reading the bytes separately could approve one image and read another.
	bs := seg.view()
	if outOfBounds(src, n*width, uint64(len(bs))) {
		return trapOOB
	}
	fields := make([]gcField, n)
	for i := range fields {
		a := src + uint64(i)*width
		fields[i] = loadStorage(bs[a:a+width], ft.Storage)
	}
	st.pushRef(ref{Obj: in.alloc(ins.Imm0, fields)})
	return nil
}

// execArrayNewElem allocates an array from an element segment's references — `eval.ml:733-746`.
//
// The bound is in **elements** against `Elem.size` (the segment's own length, not a table's), which
// is `execTableInit`'s live requirement stated one arm over. The trap is the *table* string; see the
// file comment.
func (in *Instance) execArrayNewElem(ins binary.Instr, st *stack) error {
	_, err := in.arrayType("array.new_elem", ins.Imm0)
	if err != nil {
		return err
	}
	seg, err := in.elemFor("array.new_elem", ins.Imm1)
	if err != nil {
		return err
	}
	if short := st.needNum(2); short != nil {
		return short
	}
	n := st.popNum()
	src := st.popNum()
	refs := seg.view() // one load, decision 0065
	if outOfBounds(src, n, uint64(len(refs))) {
		return trapOOBTable
	}
	fields := make([]gcField, n)
	for i := range fields {
		fields[i] = gcField{r: refs[src+uint64(i)]}
	}
	st.pushRef(ref{Obj: in.alloc(ins.Imm0, fields)})
	return nil
}

// popArrayIndexed pops an index and a reference, resolving the array — the four-line preamble
// `array.get`, `array.set` and the three-operand arms all share.
//
// **The null trap outranks the bounds check**, because OCaml's match puts `Ref NullRef` in an arm of
// its own *before* the `when array_oob` guard: `array.get` on a null reference traps null even when
// the index is absurd. `array.wast:210` (index 10 into a length-3 array) and `:191` (null) are
// separate vectors asserting separate strings, so the order is oracle-covered in both directions.
//
// Returns the object and the index. A nil object with `err == nil` cannot happen; the caller reads
// only the two values.
func popArrayIndexed(what string, st *stack) (*gcObj, uint64, error) {
	if short := st.needNum(1); short != nil {
		return nil, 0, short
	}
	i := uint64(uint32(st.popI32()))
	if short := st.needRef(1); short != nil {
		return nil, 0, short
	}
	r := st.popRef()
	if r.Null {
		return nil, 0, trapNullArray
	}
	if r.Obj == nil {
		return nil, 0, notAggregate(what, "an array instance", r)
	}
	if outOfBounds(i, 1, uint64(len(r.Obj.fields))) {
		return nil, 0, trapOOBArray
	}
	return r.Obj, i, nil
}

// execArrayGet reads one element — `eval.ml:767-778`.
//
// `ext` comes from the opcode and not from the element type — three opcodes, one arm — which is why
// `pushField` can report `read_field`'s two `failwith` cases (a signed read of an unpacked element, a
// plain read of a packed one) rather than silently picking something plausible. `array.wast:204`
// (`get_u` of `0xff` reading 255) and `:205` (`get_s` reading −1) are one segment byte answered two
// ways.
func (in *Instance) execArrayGet(ins binary.Instr, st *stack, ext fieldExt) error {
	ft, err := in.arrayType("array.get", ins.Imm0)
	if err != nil {
		return err
	}
	obj, i, err := popArrayIndexed("array.get", st)
	if err != nil {
		return err
	}
	return pushField(ft, obj.fields[i], ext, st)
}

// execArraySet writes one element — `eval.ml:780-791`.
//
// **The value is on top, the index under it, the reference under that** — `v :: Num (I32 i) :: Ref
// …`, read outside-in — so the value must be popped first, and popping it requires knowing which
// stack array it is in. That comes from the instruction's type immediate, exactly as `struct.set`'s
// does and for the identical reason: nothing can look at the object before the value is off the
// stack. `structop.go`'s note on the consequence applies unchanged — a type immediate naming a
// non-array is reported before the null trap, which is observable only in a module that is
// simultaneously invalid and trapping.
func (in *Instance) execArraySet(ins binary.Instr, st *stack) error {
	ft, err := in.arrayType("array.set", ins.Imm0)
	if err != nil {
		return err
	}
	f, err := popField(ft, st)
	if err != nil {
		return err
	}
	obj, i, err := popArrayIndexed("array.set", st)
	if err != nil {
		return err
	}
	obj.fields[i] = f
	return nil
}

// execArrayLen pushes the length — `eval.ml:793-796`, `array_length (Array (_, fs)) = length fs`.
//
// **No type immediate at all** (`opTableFB`'s `0x0f` has no `imms`), which makes this the one arm
// that cannot consult the declared element type — and therefore the one where a struct instance
// reaching an array instruction is least detectable. See `notAggregate` on why that check is filed
// rather than written.
func (in *Instance) execArrayLen(st *stack) error {
	if short := st.needRef(1); short != nil {
		return short
	}
	r := st.popRef()
	if r.Null {
		return trapNullArray
	}
	if r.Obj == nil {
		return notAggregate("array.len", "an array instance", r)
	}
	st.pushI32(int32(uint32(len(r.Obj.fields))))
	return nil
}

// popArrayTarget pops the destination pair every bulk-shaped array arm opens with — a count on top, a
// source-or-value in the middle, an index and the array reference underneath — down to but not
// including the middle operand, which differs per arm.
//
// It exists because the *order* is the part that is easy to get wrong and identical in all four:
// `Num (I32 n) :: … :: Num (I32 i) :: Ref a`. Splitting the middle out keeps one authority for the
// order while letting `array.fill` pop a value, `array.copy` pop a reference and an index, and the
// two `init`s pop a segment offset.
func popArrayTarget(what string, st *stack) (*gcObj, uint64, error) {
	if short := st.needNum(1); short != nil {
		return nil, 0, short
	}
	i := uint64(uint32(st.popI32()))
	if short := st.needRef(1); short != nil {
		return nil, 0, short
	}
	r := st.popRef()
	if r.Null {
		return nil, 0, trapNullArray
	}
	if r.Obj == nil {
		return nil, 0, notAggregate(what, "an array instance", r)
	}
	return r.Obj, i, nil
}

// execArrayFill writes one value into n consecutive elements — `eval.ml:849-868`.
//
// **The pop order is n, value, index, reference** — `Num (I32 n) :: v :: Num (I32 i) :: Ref a`, the
// count *outside* the value — and the bounds check precedes the zero-length exit (see the file
// comment). `array_fill.wast` asserts a zero-length fill at exactly the end succeeding and a
// one-element fill there trapping, which is the pair that pins the order.
func (in *Instance) execArrayFill(ins binary.Instr, st *stack) error {
	ft, err := in.arrayType("array.fill", ins.Imm0)
	if err != nil {
		return err
	}
	if short := st.needNum(1); short != nil {
		return short
	}
	n := uint64(uint32(st.popI32()))
	f, err := popField(ft, st)
	if err != nil {
		return err
	}
	obj, i, err := popArrayTarget("array.fill", st)
	if err != nil {
		return err
	}
	if outOfBounds(i, n, uint64(len(obj.fields))) {
		return trapOOBArray
	}
	for j := i; j < i+n; j++ {
		obj.fields[j] = f
	}
	return nil
}

// execArrayCopy copies n elements between two arrays — `eval.ml:798-847`.
//
// # The pop order interleaves, and both nulls are one trap
//
// `Num (I32 n) :: Num (I32 s) :: Ref sa :: Num (I32 d) :: Ref da` — count, then the *source* pair,
// then the *destination* pair, so the two references are not adjacent. The reference has two null
// arms, one per position, both `Trapping "null array reference"`; `array_copy.wast`'s
// `array_copy-null-left` and `-null-right` assert the same string for each, so the order in which
// they are tested is unobservable and only the string matters.
//
// # The element conversion is read-then-write, composed
//
// The reference rewrites the copy into `ArrayGet (y, exto)` followed by `ArraySet x`, with `exto =
// Some U` iff the **source** element type is packed. Composing those two functions on a stored value
// gives: a packed source reads back as its already-masked bits (`extend_u` is the identity on a
// `wrap`ped value), and a packed destination masks on write. Every other pair is the identity. So
// the whole conversion is *"mask iff the destination is packed"*, which is `copyElem` below —
// translation rather than transcription, exactly as `bulk.go`'s single `copy` call is for
// `memory.copy`.
//
// # Direction, and why it usually is not needed
//
// The reference branches on `I32.le_u d s` because its element-at-a-time rewrite would otherwise
// overwrite source elements before reading them — the same branch `memory.copy` has and Go's `copy`
// makes unnecessary, "the source and destination may overlap" being a `memmove`. When no conversion
// is required (destination unpacked) `copyElem` is the identity and one `copy` call is correct in
// both directions. When the destination *is* packed the per-element mask rules `copy` out, so the
// direction branch is written — and it is written from the reference rather than reasoned about,
// because a self-overlapping packed copy is the case no vector covers.
func (in *Instance) execArrayCopy(ins binary.Instr, st *stack) error {
	dstFT, err := in.arrayType("array.copy (destination)", ins.Imm0)
	if err != nil {
		return err
	}
	if _, err = in.arrayType("array.copy (source)", ins.Imm1); err != nil {
		return err
	}
	if short := st.needNum(1); short != nil {
		return short
	}
	n := uint64(uint32(st.popI32()))
	src, srcIdx, err := popArrayTarget("array.copy", st)
	if err != nil {
		return err
	}
	dst, dstIdx, err := popArrayTarget("array.copy", st)
	if err != nil {
		return err
	}
	if outOfBounds(srcIdx, n, uint64(len(src.fields))) ||
		outOfBounds(dstIdx, n, uint64(len(dst.fields))) {
		return trapOOBArray
	}
	if n == 0 {
		return nil
	}
	if !dstFT.Storage.Packed {
		// `copyElem` is the identity here, so this is one `memmove` and overlap is handled.
		// Asserted rather than assumed — see TestArrayCopyHandlesOverlapInBothDirections.
		copy(dst.fields[dstIdx:dstIdx+n], src.fields[srcIdx:srcIdx+n])
		return nil
	}
	if dstIdx <= srcIdx {
		for j := range n {
			dst.fields[dstIdx+j] = copyElem(dstFT, src.fields[srcIdx+j])
		}
		return nil
	}
	for j := n; j > 0; j-- {
		dst.fields[dstIdx+j-1] = copyElem(dstFT, src.fields[srcIdx+j-1])
	}
	return nil
}

// copyElem converts one stored element to a destination element type — `read_field src (Some U |
// None)` composed with `write_field dst`.
//
// **The source element type is not a parameter, because it has no reader.** That is the composition's
// own conclusion below, and the parameter is *absent* rather than accepted-and-ignored: an unread
// parameter is a claim that the answer depends on it, which `unparam` is right to call. The source
// still has to be *resolved* — `execArrayCopy` looks its immediate up for the error alone, exactly as
// `execArrayInitElem` does — so the reference's second type index is consulted and only its
// packedness is discarded.
//
// **Two cases, and the source's packedness does not appear.** A packed source stores an
// already-masked value, so `extend_u` reads back exactly those bits; a packed destination masks at
// write, which is `alloc_field`/`write_field`'s `wrap`. Every remaining pair — unpacked to unpacked,
// including references and v128 — moves the `gcField` whole, `hi` and `r` included, because the two
// element types agree in shape wherever validation permits the copy at all.
func copyElem(dst binary.FieldType, f gcField) gcField {
	if dst.Storage.Packed {
		return gcField{num: uint64(uint32(f.num) & packMask(dst.Storage.Width))}
	}
	return f
}

// execArrayInitData writes n elements from a data segment into an array — `eval.ml:870-899`.
//
// **The array bound is checked before the segment bound**, which is the reference's order
// (`array_oob a d n` then `data_oob`) and is observable: `array_init_data.wast` has rows where both
// are violated, and the two traps have different strings. Getting this backwards reports
// `out of bounds memory access` where the suite wants `out of bounds array access`.
//
// The source offset is popped with `popNum` rather than `popI32` because the reference reads it
// through `addr_of_num`, which admits an i64 under memory64; a pushed i32 occupies the slot
// zero-extended, so one pop serves both widths.
func (in *Instance) execArrayInitData(ins binary.Instr, st *stack) error {
	ft, err := in.arrayType("array.init_data", ins.Imm0)
	if err != nil {
		return err
	}
	seg, err := in.dataFor("array.init_data", ins.Imm1)
	if err != nil {
		return err
	}
	width, err := storageSize("array.init_data", ft.Storage)
	if err != nil {
		return err
	}
	if short := st.needNum(2); short != nil {
		return short
	}
	n := uint64(uint32(st.popI32()))
	src := st.popNum()
	obj, dstIdx, err := popArrayTarget("array.init_data", st)
	if err != nil {
		return err
	}
	if outOfBounds(dstIdx, n, uint64(len(obj.fields))) {
		return trapOOBArray
	}
	bs := seg.view() // one load, decision 0065
	if outOfBounds(src, n*width, uint64(len(bs))) {
		return trapOOB
	}
	for j := range n {
		a := src + j*width
		obj.fields[dstIdx+j] = loadStorage(bs[a:a+width], ft.Storage)
	}
	return nil
}

// execArrayInitElem writes n elements from an element segment into an array — `eval.ml:901-926`.
//
// The array bound precedes the segment bound, as in `array.init_data`, and the segment trap is the
// *table* string. No element conversion exists: an element segment holds references and a
// reference-typed array element is unpacked by construction, so there is no `exto` in the
// reference's rewrite either.
func (in *Instance) execArrayInitElem(ins binary.Instr, st *stack) error {
	if _, err := in.arrayType("array.init_elem", ins.Imm0); err != nil {
		return err
	}
	seg, err := in.elemFor("array.init_elem", ins.Imm1)
	if err != nil {
		return err
	}
	if short := st.needNum(2); short != nil {
		return short
	}
	n := uint64(uint32(st.popI32()))
	src := st.popNum()
	obj, dstIdx, err := popArrayTarget("array.init_elem", st)
	if err != nil {
		return err
	}
	if outOfBounds(dstIdx, n, uint64(len(obj.fields))) {
		return trapOOBArray
	}
	refs := seg.view() // one load, decision 0065
	if outOfBounds(src, n, uint64(len(refs))) {
		return trapOOBTable
	}
	for j := range n {
		obj.fields[dstIdx+j] = gcField{r: refs[src+j]}
	}
	return nil
}
