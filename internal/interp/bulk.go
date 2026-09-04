package interp

import (
	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The bulk memory and table operations: `fc 08` memory.init, `fc 09` data.drop, `fc 0a`
// memory.copy, `fc 0b` memory.fill, `fc 0c` table.init, `fc 0d` elem.drop, `fc 0e` table.copy.
//
// # One shape, five instances, and the shape is the reference's
//
// The five that move data all read `Num n :: Num s :: Num d` (or `n :: k :: i` for fill) off the
// stack, check bounds on every region they will touch, exit on a zero length, and then move bytes
// or refs. `eval.ml:549` (MemoryFill), `:567` (MemoryCopy), `:395` (TableCopy), `:427`
// (TableInit), `:603` (MemoryInit) are that sequence five times, and the *order* of the first two
// steps is load-bearing — see `outOfBounds`.
//
// The two drops are not that shape at all: `ElemDrop` and `DataDrop` pop nothing, check nothing,
// and empty a segment (`:446`, `:624`). They are here because they are the same *segment state*
// as the two `init`s and meaningless apart from them — see segment.go on why `table.init` cannot
// land without them.
//
// # The `init` pair differs from the `copy` pair in the one way that reverses an immediate
//
// `memory.init x y` and `table.init x y` name the destination *first* in the text and second in
// the encoding — `TableInit (x, y) -> op 0xfc; u32 0x0cl; idx y; idx x` (`encode.ml:294`),
// `MemoryInit` likewise at `:411`, against `TableCopy`'s in-order `idx x; idx y` at `:293`. So on
// the wire `Imm0` is the **segment** and `Imm1` is the table or memory, which is the opposite
// assignment from the copy arms three functions down, and the decoder's `0x0cl -> let y = at idx s
// in let x = at idx s in table_init x y` (`decode.ml:674`) is where that reversal is read back.
// The arms below therefore resolve `Imm1` as the destination, and it is not a typo.
//
// The reference expresses the move as a self-recursive rewrite: it emits a one-element
// load/store plus a re-entry with `n-1`, so a fill of 64 KiB is 65536 administrative steps. That
// is an interpreter written for clarity in a language with cheap tail calls, and reproducing it
// here would be transcription over translation — §1's workload is a Go guest running for hours,
// and a `memory.fill` that costs a stack frame per byte is the one place in this family where
// the difference is measurable. Go's `copy` and the `for`-range fill compute the same function,
// which is what makes the divergence a translation rather than a liberty.
//
// # Go's `copy` handles overlap, so the reference's two directions collapse to one call
//
// `MemoryCopy` branches on `I64.le_u d s`: copy forward from the low end when the destination is
// at or below the source, backward from the high end when it is above. That branch exists
// because a byte-at-a-time forward copy with `d > s` overwrites source bytes before reading
// them. Go's builtin says "the source and destination may overlap" — a `memmove`, not a
// `memcpy` — so a single `copy` is correct in both directions and the branch is *absent by
// construction* rather than forgotten.
//
// It is asserted anyway. `memory_copy.wast:263` copies 10←12 and `:305` copies 12←10, both with
// read-backs, so the suite covers both directions; the reason the arm below has no `d > s` case
// is a property of `copy`, and a property nothing exercises is a claim. See
// TestBulkCopyHandlesOverlapInBothDirections.
//
// # No zero-length fast path
//
// Deliberately, and stated here because it is the arm's least obvious line: see `outOfBounds`.

// execMemoryFill is `fc 0b` — `memory.fill x`, `eval.ml:549`.
//
// The value operand is an i32 whose low byte is stored (`Pack8` in the reference's Store), so
// `memory.fill` with 0x1234 writes 0x34. That truncation is the operand's semantics, not the
// engine's convenience, and the suite states it in a comment of its own: `bulk.wast:34` is
// ";; Fill value is stored as a byte", filling with `0xbbaa` and reading back `0xaa` twice
// (`:35`-`:37`). Without the narrowing a Go `byte()` conversion is the only thing standing
// between `0xbbaa` and a panic-free wrong answer, which is why the row exists.
//
// # The byte loop below is plain at every alignment, and ADR 0054 does not reach it (#627)
//
// 0054 made *typed word* accesses sequentially consistent — `atomicLoadWord`/`atomicStoreWord` at widths 4
// and 8, `atomicCell` at 1 and 2 — and its title's "every aligned guest access" was read as covering this
// file. It does not: the whole bulk family writes through plain loops and plain `copy`, so a
// `memory.fill` racing a concurrent guest load **is a Go data race**, measured under `-race` rather than
// argued. Two atomic readers were observed on the other side of this one write, so the finding is about
// the write. Ungated, too — `0xFC` needs no proposal — so this is not confined to a gate's blast radius.
//
// Whether these paths join the atomic regime is #627's decision and is Scott's, the cost being
// bulk-throughput rather than the per-access figure 0054 priced. **What must not happen quietly is the
// repair**: #10's `b-mm-2-sibling-field-after-wake` uses this write as the carrier for a `-race` verdict,
// so making it atomic leaves that case passing with nothing to detect. #627 carries the obligation; it is
// named here because a diff that routes this loop through `atomicCell` would show no sign of it.
func (in *Instance) execMemoryFill(ins binary.Instr, st *stack) error {
	mem, err := in.memoryFor("instruction", ins.Imm0)
	if err != nil {
		return err
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	// `Num n :: Num k :: Num i :: vs'` — the topmost operand is the length, so it pops first.
	n := mem.addr(st.popNum())
	k := byte(st.popNum())
	i := mem.addr(st.popNum())

	// One image load, then the bound and the access against that one slice (decision 0058): a second
	// load between the check and the fill could name a shorter array than the one checked.
	bs := mem.view()
	if outOfBounds(i, n, uint64(len(bs))) {
		return trapOOB
	}
	if n == 0 {
		return nil
	}
	// `clear`-then-set would be two passes; the range form is one and is what the compiler
	// recognizes as a memset.
	dst := bs[i : i+n]
	for j := range dst {
		dst[j] = k
	}
	return nil
}

// execMemoryCopy is `fc 0a` — `memory.copy x y`, `eval.ml:567`.
//
// **Two memories, two indices, and `x` is the destination.** `decode.ml:671` is
// `let x = at idx s in let y = at idx s in memory_copy x y`, so the first immediate on the wire
// is the destination — printed rather than inferred: `(table.copy 1 0)` encodes as `fc 0e 01 00`
// with `Imm0=1`, and the memory form stages identically. Reversing them is invisible in v0,
// where the multi-memory gate is off and both indices are 0 on every vector the suite has, which
// is exactly why the order is cited to the decoder rather than to a passing board.
func (in *Instance) execMemoryCopy(ins binary.Instr, st *stack) error {
	dstMem, err := in.memoryFor("instruction", ins.Imm0)
	if err != nil {
		return err
	}
	srcMem, err := in.memoryFor("instruction", ins.Imm1)
	if err != nil {
		return err
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	// `Num n :: Num s :: Num d :: vs'`. The length's width is `min at1 at2` per
	// `valid.ml:704`; narrowing it by the destination's is the same value whenever the two
	// agree, which is every module v0 admits, and the memory64 gate is where that stops being
	// true.
	n := dstMem.addr(st.popNum())
	s := srcMem.addr(st.popNum())
	d := dstMem.addr(st.popNum())

	// **Both regions are checked before either is touched**, and both traps are the same
	// string, so the only observable difference is that a failed copy leaves memory untouched —
	// which `memory_copy.wast:350` asserts by trapping a 40-byte copy at 65516 and then reading
	// the destination back across `:353`-onward. (A first draft cited `:428`, which is an
	// unrelated read-back inside the generated 8 KiB block; the line was checked and replaced.)
	//
	// **Two memories, so two image loads, and each is used for both of its own uses** (decision
	// 0058). `x` and `y` may be the same memory, in which case the two descriptors are the same
	// pointer and the copy is within one array exactly as before — the overlap argument above is
	// unaffected, because it is about `copy` and not about which array is named.
	dstBs := dstMem.view()
	srcBs := srcMem.view()
	if outOfBounds(d, n, uint64(len(dstBs))) ||
		outOfBounds(s, n, uint64(len(srcBs))) {
		return trapOOB
	}
	if n == 0 {
		return nil
	}
	copy(dstBs[d:d+n], srcBs[s:s+n])
	return nil
}

// execTableCopy is `fc 0e` — `table.copy x y`, `eval.ml:395`.
//
// The same arm over `[]ref` instead of `[]byte`, with two differences that are not cosmetic:
// the trap string is the table one (`out of bounds table access`, a distinct message the harness
// matches verbatim), and the bound is `Table.size` in *elements* where memory's is `bound` in
// bytes.
//
// The element-type compatibility check is `valid.ml:635`'s and belongs to validation (#9), not
// here: a `table.copy` between mismatched reftypes is an *invalid module*, so admitting it in v0
// is the standing layering debt rather than a semantic choice made in this arm.
func (in *Instance) execTableCopy(ins binary.Instr, st *stack) error {
	dstTab, err := in.tableFor("instruction", ins.Imm0)
	if err != nil {
		return err
	}
	srcTab, err := in.tableFor("instruction", ins.Imm1)
	if err != nil {
		return err
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	n := tableAddr(dstTab, st.popNum())
	s := tableAddr(srcTab, st.popNum())
	d := tableAddr(dstTab, st.popNum())

	if outOfBounds(d, n, dstTab.size()) || outOfBounds(s, n, srcTab.size()) {
		return trapOOBTable
	}
	if n == 0 {
		return nil
	}
	copy(dstTab.slots[d:d+n], srcTab.slots[s:s+n])
	return nil
}

// execTableInit is `fc 0c` — `table.init x y`, `eval.ml:427`.
//
//	| TableInit (x, y), Num n :: Num s :: Num d :: vs' ->
//	  if table_oob c.frame x d n || elem_oob c.frame y s n then Trap Table.Bounds
//	  else if n = 0 then vs'
//
// **`Imm1` is the table and `Imm0` is the element segment** — the reversal this file's header
// derives from `encode.ml:294` and `decode.ml:674`. Resolving them the other way round is the
// mutation that must move a board figure, not an argument (see bulk_test.go's ledger).
//
// Both regions are checked before either is touched and *before* the zero-length exit, which is
// `outOfBounds`'s standing note: a zero-length init at exactly the segment's end is legal and one
// element past it traps. `bulk.wast:265` and `:268` are that pair for the *dropped* segment case,
// where the segment's size is 0 so every non-zero length is out of bounds.
//
// The destination bound is the table's size in elements and the source bound is the segment's, so
// the two `outOfBounds` calls take different `j` — the shape `execTableCopy` has with two tables.
func (in *Instance) execTableInit(ins binary.Instr, st *stack) error {
	tab, err := in.tableFor("instruction", ins.Imm1)
	if err != nil {
		return err
	}
	seg, err := in.elemFor("instruction", ins.Imm0)
	if err != nil {
		return err
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	// `Num n :: Num s :: Num d :: vs'` — the length pops first.
	//
	// **Only the destination takes the table's width, and `table.init` differs from
	// `table.copy` in exactly that.** `valid.ml:641` types this
	// `[numtype_of_addrtype at; I32T; I32T]`: the destination is the table's address type, and
	// the source index and **the length** are i32 whatever the table is — where
	// `table.copy`'s length is `numtype_of_addrtype (min at1 at2)` (`:632`), a real address
	// type. A segment is indexed rather than addressed, so its own bound has no width, and the
	// length is bounded by the *segment* side of the pair.
	//
	// So `s` and `n` are popped as-is. They arrive already zero-extended — `pushI32` is
	// `uint64(uint32(v))`, which is what `addr_of_num`'s `extend_i32_u` does at `eval.ml:427`'s
	// `addr_of_num n` — and running them through `tableAddr` would be the identity on every
	// input while stating a width claim the type rule contradicts. That claim was written here
	// first and the validator was read second; `table_init64.wast` is 774 of this bucket's
	// vectors, so the file that would have made it visible is the one this arm exists for.
	n := st.popNum()
	s := st.popNum()
	d := tableAddr(tab, st.popNum())

	if outOfBounds(d, n, tab.size()) || outOfBounds(s, n, seg.size()) {
		return trapOOBTable
	}
	if n == 0 {
		return nil
	}
	copy(tab.slots[d:d+n], seg.refs[s:s+n])
	return nil
}

// execMemoryInit is `fc 08` — `memory.init x y`, `eval.ml:603`. execTableInit over bytes, with
// memory's trap string and the same immediate reversal (`encode.ml:411`, `decode.ml:669`).
//
// `bulk.wast:172` is the vector that makes the drop observable on this side: a one-byte
// `memory.init` out of a segment the module's own instantiation already dropped, expecting `out of
// bounds memory access`.
func (in *Instance) execMemoryInit(ins binary.Instr, st *stack) error {
	mem, err := in.memoryFor("instruction", ins.Imm1)
	if err != nil {
		return err
	}
	seg, err := in.dataFor("instruction", ins.Imm0)
	if err != nil {
		return err
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	// `valid.ml:706` is `[numtype_of_addrtype at; I32T; I32T]` — destination in the memory's
	// address type, source and length i32 — for execTableInit's stated reason.
	n := st.popNum()
	s := st.popNum()
	d := mem.addr(st.popNum())

	// One image load for both of the memory's uses (decision 0058). The segment needs none: a
	// `dataInstance`'s bytes are replaced only by `drop`, which is not concurrent with anything in
	// this slice, and #573 rather than 0058 is where that question lives.
	bs := mem.view()
	if outOfBounds(d, n, uint64(len(bs))) || outOfBounds(s, n, seg.size()) {
		return trapOOB
	}
	if n == 0 {
		return nil
	}
	copy(bs[d:d+n], seg.bytes[s:s+n])
	return nil
}

// execElemDrop is `fc 0d` — `elem.drop x`, `eval.ml:446`: `Elem.drop (elem c.frame.inst x)`.
//
// **Pops nothing, checks nothing, traps never.** Dropping an already-dropped segment is legal and
// does nothing (`bulk.wast:261` drops twice), which segment.go's `elemInstance` gets for free by
// making the dropped state a value rather than a flag. The only failure channel is the index
// resolution, which is the layering debt for a module #9 would have rejected.
func (in *Instance) execElemDrop(ins binary.Instr) error {
	seg, err := in.elemFor("instruction", ins.Imm0)
	if err != nil {
		return err
	}
	seg.drop()
	return nil
}

// execDataDrop is `fc 09` — `data.drop x`, `eval.ml:624`. execElemDrop's twin.
func (in *Instance) execDataDrop(ins binary.Instr) error {
	seg, err := in.dataFor("instruction", ins.Imm0)
	if err != nil {
		return err
	}
	seg.drop()
	return nil
}

// tableAddr narrows a popped slot to the table's index width — `memory.addr`'s twin, and
// `table.ml:45`'s `addr_of_num`: zero-extension for an i32-indexed table, the slot itself for an
// i64-indexed one.
//
// **The i32 arm is provably unobservable today, and this is the declared-and-tracked form of
// that** rather than a claim that it does work. Every producer of an i32 slot in this engine goes
// through `pushI32`, which is `uint64(uint32(v))` — the zero-extension is already done, and
// `value.go:167` says so as the function's whole purpose. So `uint64(uint32(slot))` here is the
// identity on every input the interpreter can present, and replacing it with `return slot`, or
// with a *sign*-extension, changes no answer.
//
// That was measured rather than assumed, and measuring it is what caught a stillborn control: a
// test previously named here — it asserted a trap on `I32(-1)` table indices — was written first
// and **passed with this narrowing deleted**, and passed again with `outOfBounds`'s wrap arm
// deleted at the same time. It is described rather than cited because it no longer exists, and a
// citation to a deleted test is the class TestEveryCitedTestNameResolves catches (#116). Calling the arm directly with a raw `0xffffffffffffffff` slot panics as expected;
// through the real front end the value never arrives, because `i32.const -1` stages `Imm0` as
// `0xffffffffffffffff` and `exec.go:1057` pushes it through `pushI32`, which narrows it (:457, wrong
// before #136 touched this file, named `if r.Null {`). The test is gone; this paragraph replaced it.
//
// It is kept, for two reasons that are not "defensiveness". First, `memory.addr` one file over is
// the same identity for the same reason and is equally load-bearing the moment memory64 lands:
// the arm exists so the *width* decision has a home, and deleting it would leave the i64 case
// with nowhere to be written. Second, the invariant it leans on belongs to `pushI32`, and an arm
// that silently depends on another function's zero-extension is the two-places-know-one-fact
// shape (#78/#105/#106) — stating the dependence here is the cheap half of not having it.
//
// A free function rather than a method because `call_indirect` needs the same narrowing, and it
// now calls this instead of open-coding it. The retrofit landed in the same PR rather than as the
// follow-up this comment first deferred: the whole reason the function is free is to have one
// place, and leaving the second copy in place for a later PR would be the two-places-know-one-fact
// shape kept deliberately for the length of a queue.
func tableAddr(t *table, slot uint64) uint64 {
	if t.limits.Addr64 {
		return slot
	}
	return uint64(uint32(slot))
}
