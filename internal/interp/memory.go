package interp

import (
	"fmt"
	"math"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// pageSize is a wasm page: 64 KiB (`memory.ml:20`, `let page_size = 0x10000L`).
const pageSize = 0x10000

// maxPages32 is the page cap for an i32-addressed memory — `memory.ml:29`'s
// `valid_size`, which is `I64.le_u i 0xffffL` for I32AT and unconditionally true for I64AT.
//
// The bound is on *pages*, not bytes, and it is 0xffff rather than 0x10000: a 65536-page
// memory would be exactly 2^32 bytes, one past the largest i32 address. Reading it off the
// reference rather than deriving it from `math.MaxUint32/pageSize` because those differ by
// exactly this off-by-one and the derivation looks more principled than it is.
const maxPages32 = 0xffff

// trapOOB is the spec's out-of-bounds text (`eval.ml:30`, `Memory.Bounds`).
//
// One value rather than one per site, because the spec has one string: `assert_trap` matches
// it verbatim, and a second spelling would be the engine's testimony disagreeing with itself
// about the same event.
var trapOOB = &Trap{Reason: "out of bounds memory access"}

// memory is one linear memory: its bytes and the type that bounds them.
//
// **A flat `[]byte` grown by reallocation**, which is `memory.ml`'s own shape (`create` makes a
// zeroed Bigarray, `grow` allocates and blits) and not a decision this phase gets to make
// interestingly. What §1's workload wants — a Go guest that loads once and runs for hours — is
// a memory whose *steady state* is a single contiguous slice with no indirection per access,
// which this is. The interesting version of this question is v1's, where §4's boundary model
// and shared memories decide whether growth may move the backing array at all.
type memory struct {
	// bytes is the memory's contents. Its length is always a multiple of pageSize, and it
	// is the authority on the current size — the reference reads `size` back out of the
	// array's dimension (`memory.ml:47-50`) rather than keeping a counter, and a second
	// place holding the same fact is how the two drift.
	bytes []byte

	// limits is the declared type, kept because `grow` needs the max and the address width
	// to decide whether a delta is legal.
	limits binary.Limits
}

// newMemory allocates a memory at its declared minimum.
//
// It reports the reference's two *allocation* failures, and they are separate strings rather
// than one: `alloc` raises SizeOverflow when the minimum exceeds the address width's page cap
// and Type when the limits are inverted (`memory.ml:40-44`). Only the first is reachable here
// — inverted limits are #9's verdict, and this returns the layering debt for them rather than
// a trap, because "min > max" is a claim about the module.
func newMemory(m binary.Memory) (*memory, error) {
	lim := m.Limits
	if !validSize(lim, lim.Min) {
		// `memory size overflow` (eval.ml:31). A trap, not a verdict: the module said a
		// number the format allows and the address width does not.
		return nil, &Trap{Reason: "memory size overflow"}
	}
	if lim.HasMax && lim.Min > lim.Max {
		return nil, fmt.Errorf("%w: memory declares min %d above max %d",
			ErrNotValidated, lim.Min, lim.Max)
	}
	// The multiplication cannot overflow after validSize: an i32 memory is capped at 0xffff
	// pages and an i64 memory's min has already been bounded by what the host can allocate,
	// which is the OutOfMemory the reference also admits (`memory.ml:37`).
	n := lim.Min * pageSize
	if n > math.MaxInt {
		return nil, &Trap{Reason: "out of memory"}
	}
	return &memory{bytes: make([]byte, n), limits: lim}, nil
}

// validSize is `memory.ml:27`'s `valid_size`: an i32-addressed memory is capped at 0xffff
// pages, an i64-addressed one is not capped here at all.
//
// The i64 arm returning true unconditionally is the reference's, not an omission — a memory64's
// size is bounded by the host's allocator rather than by the address type, which is why
// newMemory checks MaxInt separately.
func validSize(lim binary.Limits, pages uint64) bool {
	if lim.Addr64 {
		return true
	}
	return pages <= maxPages32
}

// size is the memory's size in pages, read from the backing slice rather than from a counter
// (`memory.ml:47-50`).
func (m *memory) size() uint64 { return uint64(len(m.bytes)) / pageSize }

// effectiveAddress is `memory.ml:96`'s `effective_address`: the 64-bit sum of the dynamic index
// and the static offset, trapping when it wraps.
//
// **The index arrives already zero-extended, and that is the whole width story.**
// `value.ml:292` maps an i32 operand through `extend_i32_u` before this is reached, so both
// address widths compute here in 64 bits and the only overflow that matters is the 64-bit one.
// An earlier draft of this engine's design branched on the address type here; the reference
// makes no such distinction, and inventing one would be an accept-direction defect — a wrapped
// i32 address that the reference traps on would instead read some other byte.
func effectiveAddress(idx, offset uint64) (uint64, error) {
	ea := idx + offset
	if ea < idx { // unsigned wrap, `I64.lt_u ea a`
		return 0, trapOOB
	}
	return ea, nil
}

// outOfBounds is `eval.ml:159`'s `oob i n j` — `lt_u (add i n) i || gt_u (add i n) j` — the
// bounds predicate the bulk operations share with each other and with `table.blit`.
//
// Two tests, and the first is not decoration: `i + n` can wrap when both are near 2^64, and
// without the wrap check a wrapped end lands below `j` and admits exactly the access this
// refuses. `read`/`write` express the same bound in the subtraction form (`i > j - n`) because
// they have a `len` in hand and it cannot overflow there; here the reference's form is
// transcribed directly, since these arms' `n` is an *operand* rather than an instruction width.
//
// **A zero-length run at exactly `j` is in bounds and one byte past it is not**, which is the
// whole reason the bulk arms cannot open with an `if n == 0 { return nil }` fast path: the
// reference tests `oob` *before* its `n = 0` exit (`eval.ml:549`, `:567`, `:395`), and
// `bulk.wast` says so in its own words — `:49` "Succeed when writing 0 bytes at the end of the
// region" against `:52` "Writing 0 bytes outside the memory traps". That is the early-return
// grave's shape (#41): a fast path that skips the check it was placed in front of.
func outOfBounds(i, n, j uint64) bool {
	end := i + n
	return end < i || end > j
}

// read returns n bytes at the effective address, or traps.
//
// The bound check is against the *end* of the access and is written so it cannot itself
// overflow: `ea > len - n` rather than `ea + n > len`, since the latter wraps for an ea near
// 2^64 and would admit exactly the accesses this is here to refuse.
func (m *memory) read(idx, offset, n uint64) ([]byte, error) {
	ea, err := effectiveAddress(idx, offset)
	if err != nil {
		return nil, err
	}
	if n > uint64(len(m.bytes)) || ea > uint64(len(m.bytes))-n {
		return nil, trapOOB
	}
	return m.bytes[ea : ea+n], nil
}

// write copies bs to the effective address, or traps.
//
// **Nothing is written when the access is out of bounds**, and that is a real property rather
// than a consequence of the loop's shape: `memory.ml:87`'s `store_bytes` counts *downward*
// specifically so the highest address is touched first and a partial store cannot be observed.
// Checking the whole extent up front gets the same guarantee without depending on iteration
// order, which is the property `memory_trap.wast` asserts by reading the memory back after a
// failed store.
func (m *memory) write(idx, offset uint64, bs []byte) error {
	ea, err := effectiveAddress(idx, offset)
	if err != nil {
		return err
	}
	n := uint64(len(bs))
	if n > uint64(len(m.bytes)) || ea > uint64(len(m.bytes))-n {
		return trapOOB
	}
	copy(m.bytes[ea:], bs)
	return nil
}

// grow adds delta pages and returns the previous size, or -1 as the spec's failure value.
//
// **Three failure modes and one return convention.** `memory.grow` does not trap: it reports
// failure as `-1` in the result, so the reference's SizeOverflow/SizeLimit exceptions
// (`memory.ml:60-67`) become that value here rather than errors. Returning an error instead
// would make every failed grow a trap and turn ~53 assert_return vectors into assert_trap
// answers — the wrong verdict, arrived at by borrowing the wrong channel.
func (m *memory) grow(delta uint64) int64 {
	old := m.size()
	newSize := old + delta
	if newSize < old { // 64-bit wrap: `I64.gt_u old_size new_size`
		return -1
	}
	if !validSize(m.limits, newSize) {
		return -1
	}
	if m.limits.HasMax && newSize > m.limits.Max {
		return -1
	}
	n := newSize * pageSize
	if n > math.MaxInt {
		return -1
	}
	// Reallocate and copy, matching `grow`'s allocate-and-blit. `append` would also work
	// and would leave the growth factor to the runtime; an explicit make is what keeps the
	// length an exact multiple of pageSize, which `size` reads back as the authority.
	grown := make([]byte, n)
	copy(grown, m.bytes)
	m.bytes = grown
	// **The declared type grows with the memory, and it is mutable for exactly this
	// reason.** `memory.ml:64`'s `grow` sets `mem.ty <- MemoryT (at, lim')` with `lim'.min`
	// the new size — `type_of` (called at import-match time, `instance.ml:76`) reads that
	// field back, so a memory this instance re-exports after growing must satisfy an
	// importer against its *current* size, not the size it had when this instance was
	// built. `imports4.wast:22-37` pins exactly this, in its own comment: "imported memory
	// limits should match, because external memory size is 2 now."
	m.limits.Min = newSize
	return int64(old)
}

// runData performs one data segment's instantiation-time effect — `run_data`
// (`eval.ml:1278-1291`), the trapping half of 0015.
//
// **Two modes, not three, and one of them drops.** A data segment is Passive (no effect) or
// Active (copy, then `data.drop`); the reference's Declarative arm is `assert false` for data,
// which is why `DataSegment.Passive` is a bool where `ElemMode` is an enum. So this is
// `runElem`'s shape minus the declarative case, and the drop is load-bearing for the same
// reason — `bulk.wast:153-173` is the memory twin of the `init_passive`/`drop_passive` pattern,
// with `:172` asserting `out of bounds memory access` for a one-byte init out of a dropped
// segment.
//
// **The trap is the point.** `data1.wast` is 14 vectors of `assert_trap` wrapping a bare module,
// every one expecting `out of bounds memory access`, none of them invoking anything: a segment
// whose extent exceeds its memory is a module that *traps while coming to life*, not a module
// that is invalid. That distinction is the suite's, which is what made it the design's (0015).
//
// A segment naming a memory the module does not have is #9's verdict and is reported as the
// layering debt — the module is wrong, not the run. Note that `data1.wast:3` deliberately
// exercises the *other* case: `(data (memory 1) …)` against three declared memories is a
// legal index whose target is too small, which traps.
//
// **Resolution goes through memoryFor rather than indexing `in.mems` directly**, which is one
// concept getting one trigger. The first draft wrote its own bound check here — `MemIndex >=
// len(in.mems)` — and got half of memoryFor's job: it caught the out-of-range index and not the
// *reserved-but-empty* slot, so as soon as import slots became reachable a data segment aimed at
// an imported memory dereferenced nil and panicked. Two places knowing "how to turn a memory
// index into a memory" is the shape that produced graves #78, #105 and #106; there is now one.
func (in *Instance) runData(idx int, seg *binary.DataSegment) error {
	if seg.Passive {
		return nil
	}
	inst, err := in.dataFor("data segment", uint64(idx))
	if err != nil {
		return err
	}
	mem, err := in.memoryFor("data segment", uint64(seg.MemIndex))
	if err != nil {
		return err
	}
	off, err := in.constAddr(seg.Offset, "a data segment's offset")
	if err != nil {
		return err
	}
	// Offset 0 with an empty segment is in bounds for a zero-length memory, which `write`
	// gets right for free: `n == 0` makes the extent check `ea > len`, and ea is 0.
	//
	// A trapping copy does not drop, for `runElem`'s reason and with the same ordering.
	if err := mem.write(off, 0, inst.bytes); err != nil {
		return err
	}
	inst.drop()
	return nil
}

// The offset evaluator that used to live here is `constAddr` in constexpr.go, one of the four call
// sites of #241's single constant-expression evaluator. It moved rather than stayed because it was
// never a memory concern: it was the *general* const-expr path with a data segment's name on its
// error message, which is exactly the misattribution grave #240 filed.

// memoryFor resolves a memory index to a memory. It is the *only* place that does, which is what
// keeps its two failure modes from being half-remembered elsewhere (see initData).
//
// `what` names the thing holding the index — "instruction", "data segment" — because the error is
// read by someone looking for it in their module, and "instruction names memory 3 of 2" sends
// them to the wrong line when the index was in a `(data (memory 3) …)`.
//
// Reported as the layering debt rather than as a trap: an index past the end of the memory
// index space is a module the validator rejects, and this package's rule is that it never
// invents a verdict about a module.
func (in *Instance) memoryFor(what string, idx uint64) (*memory, error) {
	if idx >= uint64(len(in.mems)) {
		return nil, fmt.Errorf("%w: %s names memory %d of %d",
			ErrNotValidated, what, idx, len(in.mems))
	}
	if in.mems[idx] == nil {
		// A reserved slot with nothing in it, and the two reasons are reported apart because
		// they are different facts about the engine. An index below the import offset is an
		// **imported** memory *nothing supplied* — nothing went wrong with the module. Above
		// it, a declared memory whose allocation failed for a verdict-shaped reason, and the
		// reason is quoted rather than paraphrased: without it this reads "memory %d exists but
		// is nil", which tells the reader nothing about their module.
		//
		// **This arm is now the unlinked case rather than the only case, and no line of the
		// *logic* changed to become that.** `InstantiateLinked` fills the reserved slot, so a
		// supplied memory reaches the check below and is simply returned — supplying an import is
		// *filling*, never restructuring an index space, which is what the reserved-not-omitted
		// convention bought (22 vectors, see Instance.mems). The sentence that used to say v0
		// "cannot supply" this was true when written and is the prose a landing falsifies.
		//
		// **The message did change, and it had to**: it said `linking is not implemented`, which
		// this engine falsifies the moment it links, and an error naming a component's absence
		// while the component runs is the fabricated-evidence grave (#36) — the right verdict
		// quoting a fact the engine does not hold. The bucket-preservation argument that licensed
		// keeping it verbatim was discharged when the drain was measured: **624 → 13** under the
		// old string, so the key it protected has served its one purpose, and the 13 that remain
		// are unsupplied imports rather than an engine without a linker. Still ErrUnsupported —
		// "this phase does not run a module whose import went unsupplied" is true and is §3's.
		if idx < uint64(in.mod.ImportedMems()) {
			return nil, fmt.Errorf("%w: memory %d is an import nothing supplied (contract §3)",
				ErrUnsupported, idx)
		}
		return nil, fmt.Errorf("%w: memory %d was declared but not allocated: %w",
			ErrNotValidated, idx, in.deferred)
	}
	return in.mems[idx], nil
}

// memAccess executes one load or store from the 0x28-0x3e family.
//
// **Operand order is the stack's, and it is the half of this that a plausible-looking arm gets
// backwards.** A store pops the *value* first and the *address* second (`eval.ml:462`,
// `Store …, Num n :: Num i :: vs'`), because the address was pushed first. Reversing them
// produces a store to the value's address with the address's value — which for the many vectors
// whose address and value are both small integers writes plausible bytes to the wrong place.
func (in *Instance) memAccess(ins binary.Instr, st *stack) error {
	m, ok := memops[ins.Op]
	if !ok {
		// Not reachable from run's case list, which is derived from the same table's
		// domain; stated rather than assumed, since a divergence here would silently make
		// a load a no-op.
		return unsupported(ins)
	}
	// `resolveErr`, not `err`: the two `needNum` checks below bind their own `err` in inner
	// scopes, and reusing the name here would shadow this one — flagged by `govet`, and worth
	// renaming rather than suppressing because the two errors mean different things (no memory
	// versus no operands) and a single name invites returning whichever is in scope.
	// **Through binary.Memarg, not `ins.Imm1` bare.** This line read the whole second word
	// as a memory index, which was correct only for as long as nothing packed above bit 32
	// on these 23 rows — the SIMD lane paths next door already masked, because their lane
	// index had taught them to. #306 packs an alignment exponent at bits 40-45, so the bare
	// read would resolve `i32.load align=4` to memory index `2<<40`: not a subtle wrong
	// answer but every aligned load in every module failing to find its memory. The accessor
	// is the fix for the class rather than for this line (module.go's memarg comment).
	offset, memIdx, _ := binary.Memarg(ins.Imm0, ins.Imm1)
	mem, resolveErr := in.memoryFor("instruction", uint64(memIdx))
	if resolveErr != nil {
		return resolveErr
	}
	if isStore(ins.Op) {
		if err := st.needNum(2); err != nil {
			return err
		}
		v := st.popNum()   // the value, pushed second
		idx := st.popNum() // the address, pushed first
		return mem.write(mem.addr(idx), offset, storeBytes(v, m.width))
	}
	if err := st.needNum(1); err != nil {
		return err
	}
	idx := st.popNum()
	bs, err := mem.read(mem.addr(idx), offset, m.width)
	if err != nil {
		return err
	}
	st.pushNum(loadValue(bs, m))
	return nil
}

// addr narrows a popped slot to the memory's address width.
//
// **Zero-extension, not sign-extension**, and the reference is explicit: `value.ml:292` maps an
// i32 operand through `extend_i32_u`. So `i32.load` at address `-1` is address `0xFFFFFFFF` —
// far out of bounds and trapping — rather than address `0xFFFFFFFFFFFFFFFF` or a wrap to 0. An
// i64 memory's slot is already the address.
//
// The mask is applied here rather than at the pop site because the *memory* decides the width,
// not the instruction: the same `i32.load` opcode is 32-bit addressed against a memory32 and
// (with the memory64 gate on) 64-bit addressed against a memory64.
func (m *memory) addr(slot uint64) uint64 {
	if m.limits.Addr64 {
		return slot
	}
	return uint64(uint32(slot))
}

// An `errIsTrap` predicate stood here and was **never called**, `asTrap` (interp.go) having been
// written for the same job one file over and actually used. `deadcode` found it, which is the
// classification question working as decision 0005 intends: two helpers for one concept is the
// two-places-know-one-fact shape, and the one to keep is the one the instantiation boundary calls.
// Recorded rather than silently deleted because the *reason* it existed — 0015's channel split must
// be decided by type, not by convention — is a live requirement that `asTrap` now carries alone.
