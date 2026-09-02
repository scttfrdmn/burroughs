package interp

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// #545, split from #524 at the seam that issue names: **the 0xFE region's semantics.**
//
// The reader half (#534) gave the 67 atomic mnemonics shapes and the validator half (#538) gave
// them types, which between them turned `atomic.wast`'s 48 `assert_invalid` vectors green. This
// file is the third and last part of the same work, and its population is the file's remaining
// 187 rows — 142 `assert_return` and 45 `assert_trap`, all of which were reaching
// `interp: no arm for opcode fe NN`. The arithmetic is worth stating because it is the whole work
// plan: 142 + 45 = 187, so the bucket and the file's own assertion census agree exactly, and there
// is no third stratum hiding in the count.
//
// # The authority, and the one place the proposal contradicts itself
//
// `spec-threads/interpreter/exec/eval.ml:377-471`, seven arms, and every semantic decision below is
// a transcription of one of them **except the alignment rule**, which is taken from the proposal's
// normative prose instead, because the two disagree.
//
// **Alignment is checked on the effective address**: `ea = i + memarg.offset`, trap when
// `ea mod N/8 != 0`. That is the rule at all six of the prose's atomic sites, each one defining
// `ea` two lines above the trap it guards:
//
//	spec-threads/document/core/exec/instructions.rst
//	  1737/1743  load with ord     "Let ea be the integer i + memarg.offset" / SEQCST trap
//	  2285/2291  store with ord    same pair
//	  3066/3068  atomic.load(n)    "Let ea be i + memarg.offset" / trap
//	  3205/3207  atomic.rmw(n)     same pair
//	  3364/3366  memory.atomic.notify
//	  3481/3483  memory.atomic.waitN
//
// **`eval.ml` computes something else, and this is an upstream inconsistency rather than a reading
// of ours.** All six `check_align` call sites pass `addr = I64_convert.extend_i32_u i` — the popped
// operand alone — while the static `offset` goes separately to `Memory.load_num`/`store_num` and is
// folded in by `effective_address` (`runtime/memory.ml:91-94`) only after the check has passed. The
// proposal's own specification text and its own reference interpreter cannot both be satisfied.
//
// The engine follows the prose. Prose is the specification and the interpreter is an
// implementation of it, so where they diverge the implementation is the thing with the bug —
// *the standard outranks the snapshot* ([ADR
// 0049](../../docs/decisions/0049-atomic-alignment-is-checked-on-the-effective-address-because-the-proposals-normative-prose-outranks-its-own-reference-interpreter.md),
// ruling on #546). Two things this replaced are worth naming, because both were wrong in a way a
// reader would not otherwise see:
//
//   - The question was first framed as `eval.ml` versus `proposals/threads/Overview.md:344-345`,
//     and **adjudicating between those two settles nothing** — they are artifacts of the same
//     proposal, and neither is the standard. That framing was ruled out rather than answered.
//   - Read through `Overview.md` alone, the disagreement looked like *two* of the seven arms
//     (its sentence is about wait and notify only). The prose puts it at **all six** sites that
//     check alignment at all. A design overview is a summary, and its silence is not agreement.
//
// The merged spec would outrank both, and **it does not exist at this pin set**: the core pin
// (`bdd7164`, 2026-07-28) has no atomics whatsoever — zero `Atomic` constructors in its `eval.ml`,
// no `check_align`, and `atomic` in one prose file where it means "atomically" in the linking
// sense. So there is no second interpreter to cross-check against, and the best available normative
// text is the proposal's own, which is spec-form prose in a fork of the spec repo rather than a
// design document.
//
// **No vector in either corpus can tell the two readings apart** — measured over both, because
// "the board has no witnesses" and "nothing has witnesses" are different claims, and #537 exists
// precisely because the two corpora differ by 45 alignment vectors:
//
//	$ grep -rhoE '(i32|i64|memory)\.atomic\.[a-z0-9_.]+[^)]*offset=[0-9]+' \
//	      testdata/spec third_party/spec-threads/test | wc -l
//	0
//	# and the same shape over plain loads, so the zero is not a broken search:
//	$ grep -rhoE '(i32|i64)\.load[a-z0-9_]*[^)]*offset=[0-9]+' \
//	      testdata/spec third_party/spec-threads/test | wc -l
//	2424
//
// Every atomic in both files carries a zero static offset, so `ea == i` on all 187 rows and the two
// readings score identically. Identical boards are the finding rather than the licence, so the rule
// is pinned by `TestAtomicAlignmentIsCheckedOnTheEffectiveAddress` — a hand-built discriminating
// pair neither corpus contains, run through the front end rather than against this file's helper, so
// that what is pinned is the behaviour and not the predicate.
//
// Not the equality rule #538 landed, which shares the word: that one checks the static `align=`
// immediate against the access size, in the validator. This one checks a runtime address.
//
// **It is deliberately *not* decided by #538's precedent, whose premise is absent here.** That
// ruling let this package take the address *type* from the core pin against the threads pin's
// `I32Type`, on the stated ground that the threads pin "predates memory64 and cannot express the
// question". This pin can express *this* question — and answers it twice, in two voices that
// disagree, which is a different situation and gets a different instrument.
//
// # Derived from the mnemonic and the operator, never transcribed
//
// The table below is built at init from the generated opcode table rather than written out, for
// `internal/validate/atomic.go`'s reason: 67 hand-written rows are 67 chances to write width 2
// where width 4 belongs. The corpus is a much better oracle here than it was for the validator —
// `atomic.wast` asserts an exact result for 66 of the 67 rows, so most width errors *would* fail a
// vector rather than pass silently — but "most" is doing work in that sentence, and a derivation
// costs less than establishing which rows are the exceptions.
//
// The mnemonic carries the access width, the slot width and the family; the operator carries which
// of the six read-modify-write functions applies, and it has to, because `fe 1e` and `fe 25` are
// both `mnemonic: "i32_atomic_rmw"`. Dispatching that pair to one arm computes a sum where the
// module asked for a difference.
//
// # Atomicity, and where it comes from
//
// Every operation below is sequentially consistent, reached through `atomicCell` and `sync/atomic`
// on the containing host word (#542, [ADR
// 0051](../../docs/decisions/0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md)).
// **SC is not this project's choice and there was no ordering decision to make**: the proposal fixes
// it, `relaxed.rst:35` giving `ordact(ARMW loc byte₁* byte₂*) = SEQCST` as a function with one case
// and `relaxed.rst:244` saying atomics *"are also required to be sequentially consistent"*. Go's `sync/atomic`
// is exactly SC, so the available strength matches the required one with nothing to spare.
//
// This paragraph used to read *"why this file contains none"*, and the sentence it opened with —
// plain read-then-write being *"observationally complete for the engine as it exists"* — was true
// when it was written and stopped being true the moment T-1 could spawn a second agent. The
// tripwire that watched for exactly that event fired, and it is still standing as
// `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling` — re-pointed twice and renamed twice, because
// the event it watches outlived two successive reasons for watching it (graves #561 and #576).
//
// **`atomic_fence`'s bare `return nil` is not the same statement in one line**, which is what this
// header used to claim, and #558 is the correction. The no-op is conformant, and for a reason that
// has nothing to do with the debt above: `AFENCE` appears in the action grammar
// (`runtime.rst:693`) and in the reduction rule (`instructions.rst:3631-3639`) and **in no
// consistency rule at all** — not `locact`, not `ordact`, not `readact` or `writeact`. An action no
// consistency rule constrains constrains no implementation, and once every atomic around it is SC a
// fence between them has nothing left to order. That is a *reading* rather than a measurement, and
// it has to be: no vector can witness a fence's absence, single- or multi-threaded, so there is no
// oracle here — only citations, which invert if upstream gives `AFENCE` a rule.
//
// What is still missing is named where it lives rather than here: #557 for the tearing requirement
// on *plain* aligned accesses, which is a different code path in memop.go, and #543 for a wait that
// can actually suspend.
type atomicKind uint8

const (
	// atomicLoad is `AtomicLoad`: zero-extend width bytes into the slot. The region has no
	// sign-extending load — every narrow row is spelled `_u`, and the reference passes `ZX`
	// unconditionally — so `signed` never appears in this file.
	atomicLoad atomicKind = iota
	atomicStore
	atomicRmw
	atomicCmpxchg
	atomicWait
	atomicNotify
	atomicFence
)

// rmwOp is which of the six read-modify-write functions an `AtomicRmw` row applies.
//
// `Eval_num.eval_rmwop` (`exec/eval_num.ml`) is the authority; the six are the complete set,
// and `RmwXchg` is included in it rather than treated as a store because it returns the old
// value like the other five.
type rmwOp uint8

const (
	rmwAdd rmwOp = iota
	rmwSub
	rmwAnd
	rmwOr
	rmwXor
	rmwXchg
)

// atomicop is one row of the 0xFE region: what it touches, what slot it lands in, and which arm
// of the reference's seven it belongs to.
type atomicop struct {
	kind atomicKind

	// width is the bytes touched in memory: 1, 2, 4 or 8. For the wait rows it is the width of
	// the *compared* value (4 for wait32, 8 for wait64), which is what the reference's
	// alignment check and load both use; the result they push is an i32 regardless.
	width uint64

	// is64 is whether the value slot is 64 bits. False for `notify` and both `wait` rows'
	// *results*, which are i32 — `wait64` sets it because its `expected` operand is an i64,
	// and the arm below reads that rather than this flag for the push.
	//
	// **No executor reads it, and that is a real observation rather than an oversight to fix.**
	// Its one consumer was `slotOf`, which passed it to `loadValue` — where the unsigned path
	// ignores it, since the atomics have no sign-extending row. ADR 0051 removed `slotOf`, so
	// what is left is a field the mnemonic derivation *writes* and that
	// `TestAtomicopParsesTheRegionsThreeHardPairs` reads back. That makes it a checked property
	// of the parse rather than an input to execution, and
	// deleting it would delete a row of that check: `i64.atomic.rmw32.add_u` and
	// `i32.atomic.rmw.add_u` differ in nothing else, so the flag is where the derivation's
	// family split is asserted. Stated because a write-only field is normally a defect, and this
	// one's reader is in a test file where `deadcode` cannot see the difference.
	is64 bool

	// rmw is meaningful only when kind is atomicRmw.
	rmw rmwOp
}

// trapUnalignedAtomic is the reference's own phrase (`eval.ml:148`).
//
// The corpus expects `"unaligned atomic"`, which is a **prefix** of this and therefore matches
// under ADR 0045's prefix rule — the same rule that makes Trap.Error render the reason first. The
// longer string is used rather than the corpus's shorter one because the reference is the authority
// for the text, and a message trimmed to exactly what the current assertion needs is a message that
// stops matching the moment upstream lengthens its expectation.
var trapUnalignedAtomic = &Trap{Reason: "unaligned atomic memory access"}

// trapExpectedShared is `check_shared`'s (`eval.ml:151-152`).
//
// **Reachable only from the two wait rows**, because `check_shared` is called from
// `MemoryAtomicWait` and from nothing else — notify, the loads, the stores, the rmws and cmpxchg
// all work on an unshared memory, which `atomic.wast:438` states in a comment (*"unshared memory is
// OK"*) and then demonstrates with a module that instantiates all of them. No vector invokes a wait
// on an unshared memory, so this trap is currently unwitnessed by the corpus; it is here because
// omitting it would make the engine answer 1 or 2 where the reference traps.
var trapExpectedShared = &Trap{Reason: "expected shared memory"}

// atomicops is the 0xFE region keyed by sub-opcode, derived at init.
//
// A map rather than per-instruction string parsing: the derivation is the point, but doing it on
// every executed instruction would put `strings.Cut` in the hot path of a memory operation.
var atomicops = buildAtomicops()

// buildAtomicops derives every row from the generated table's (mnemonic, operator) pair.
//
// Panics on a mnemonic it cannot parse, and that is deliberate: the alternative is a zero-valued
// `atomicop`, which is a well-formed `atomicLoad` of width 0 into an i32 slot — an instruction that
// silently reads nothing and pushes zero. A region the generator regenerates into a shape this
// parser does not know is a build-time fact, and it should read like one.
func buildAtomicops() map[uint32]atomicop {
	ops := binary.PrefixedRegionOpcodes(0xfe)
	out := make(map[uint32]atomicop, len(ops))
	for _, op := range ops {
		mnemonic, _, ok := binary.PrefixedOp(0xfe, op)
		if !ok {
			panic(fmt.Sprintf("interp: 0xfe %#x is in the region's opcode list but has no table row", op))
		}
		operator, _ := binary.PrefixedOperator(0xfe, op)
		a, err := parseAtomicMnemonic(mnemonic, operator)
		if err != nil {
			panic(fmt.Sprintf("interp: 0xfe %#x (%s): %v", op, mnemonic, err))
		}
		out[op] = a
	}
	return out
}

// rmwOperators maps the reference's operator constructor to this file's enum.
//
// Six entries, and the pairing is by *name* rather than by position, so a regenerated table that
// reorders the constructors cannot re-key it. (Re-keying an allow map by arithmetic is the defect
// this avoids; the subject's own text is the key.)
var rmwOperators = map[string]rmwOp{
	"RmwAdd":  rmwAdd,
	"RmwSub":  rmwSub,
	"RmwAnd":  rmwAnd,
	"RmwOr":   rmwOr,
	"RmwXor":  rmwXor,
	"RmwXchg": rmwXchg,
}

// parseAtomicMnemonic derives an atomicop from a generated mnemonic and operator.
//
// The grammar is the region's, and it is small: four fixed names, then
// `<ty>_atomic_<family>[<n>][_u][_cmpxchg]` where ty is i32 or i64, family is load, store or rmw,
// and n is 8, 16 or 32. Everything the arm below needs is in that shape — the type prefix gives the
// slot, the digits give the access width, and their absence gives the natural width.
func parseAtomicMnemonic(mnemonic, operator string) (atomicop, error) {
	switch mnemonic {
	case "atomic_fence":
		return atomicop{kind: atomicFence}, nil
	case "memory_atomic_notify":
		// "The notify operator requires an alignment of 32 bits" (Overview.md:348), which is
		// also what `check_align addr (I32 ...) None` computes.
		return atomicop{kind: atomicNotify, width: 4}, nil
	case "memory_atomic_wait32":
		return atomicop{kind: atomicWait, width: 4}, nil
	case "memory_atomic_wait64":
		return atomicop{kind: atomicWait, width: 8, is64: true}, nil
	}

	ty, rest, found := strings.Cut(mnemonic, "_atomic_")
	if !found {
		return atomicop{}, fmt.Errorf("mnemonic has no _atomic_ segment")
	}
	var a atomicop
	switch ty {
	case "i32":
		a.width = 4
	case "i64":
		a.width, a.is64 = 8, true
	default:
		return atomicop{}, fmt.Errorf("unknown type prefix %q", ty)
	}

	// `_cmpxchg` is a suffix on the rmw family rather than a family of its own in the naming,
	// so it is stripped before the family word is read. Order matters: `i64_atomic_rmw32_u`
	// and `i64_atomic_rmw32_u_cmpxchg` differ only here.
	if trimmed, isCmpxchg := strings.CutSuffix(rest, "_cmpxchg"); isCmpxchg {
		a.kind = atomicCmpxchg
		rest = trimmed
	}

	// The trailing `_u` carries no information this file uses — every narrow atomic access is
	// zero-extending — but it must come off before the digits can be read.
	rest = strings.TrimSuffix(rest, "_u")

	family := strings.TrimRight(rest, "0123456789")
	if a.kind != atomicCmpxchg {
		switch family {
		case "load":
			a.kind = atomicLoad
		case "store":
			a.kind = atomicStore
		case "rmw":
			a.kind = atomicRmw
		default:
			return atomicop{}, fmt.Errorf("unknown family %q", family)
		}
	} else if family != "rmw" {
		return atomicop{}, fmt.Errorf("cmpxchg on unexpected family %q", family)
	}

	// `widthTag` rather than `bits`: this slice imports `math/bits` for the endianness involution,
	// and the shorter name would shadow the package for the rest of the block.
	if widthTag := rest[len(family):]; widthTag != "" {
		n, err := strconv.Atoi(widthTag)
		if err != nil || n != 8 && n != 16 && n != 32 {
			return atomicop{}, fmt.Errorf("unreadable access width %q", widthTag)
		}
		a.width = uint64(n) / 8
	}

	if a.kind == atomicRmw {
		r, ok := rmwOperators[operator]
		if !ok {
			return atomicop{}, fmt.Errorf("rmw row carries unknown operator %q", operator)
		}
		a.rmw = r
	} else if operator != "" {
		return atomicop{}, fmt.Errorf("non-rmw row carries operator %q", operator)
	}
	return a, nil
}

// execFE runs one instruction from the 0xFE region.
//
// The seven families are dispatched in `eval.ml`'s own order of concerns rather than by opcode:
// resolve the memory, check alignment, then do the family's work. Alignment before the access is
// load-bearing — an unaligned address that is *also* out of bounds must report `unaligned atomic`,
// because that is the order `check_align addr; Memory.load_num` runs in.
func (in *Instance) execFE(ins binary.Instr, st *stack) error {
	a, ok := atomicops[ins.Op]
	if !ok {
		return unsupported(ins)
	}
	// **Before the memarg is read, because `atomic_fence` has no memarg.** Its immediate is
	// `immZeroByte`, so `Imm0` holds a reserved byte rather than an offset and `Imm1` holds
	// nothing at all; falling through would resolve memory index 0 and then find no operands.
	// `AtomicFence, vs -> vs, [], NoAction` (eval.ml:470-471) is the whole arm: it is a no-op
	// for this engine because there is nothing yet for it to order.
	if a.kind == atomicFence {
		return nil
	}

	offset, memIdx, _ := binary.Memarg(ins.Imm0, ins.Imm1)
	mem, err := in.memoryFor("instruction", uint64(memIdx))
	if err != nil {
		return err
	}

	switch a.kind {
	case atomicLoad:
		return in.atomicLoad(a, st, mem, offset)
	case atomicStore:
		return in.atomicStore(a, st, mem, offset)
	case atomicRmw:
		return in.atomicRmw(a, st, mem, offset)
	case atomicCmpxchg:
		return in.atomicCmpxchg(a, st, mem, offset)
	case atomicWait:
		return in.atomicWait(a, st, mem, offset)
	case atomicNotify:
		return in.atomicNotify(a, st, mem, offset)
	case atomicFence:
		// Named rather than left to the default, so the early return above is a *checked*
		// invariant instead of a comment asserting one. Deleting that return sends a fence
		// through `Memarg` — which would read its reserved byte as an offset — and memory
		// resolution, both of which succeed, and then lands here and fails loudly. A comment
		// claiming the property the code has to have is how review confirms a bug.
		return fmt.Errorf("%w: atomic.fence reached the memarg path, which has no memarg to read",
			ErrNotValidated)
	}
	// Not reachable: buildAtomicops assigns one of the seven kinds to every row or panics. Stated
	// rather than defaulted, since a silent `nil` here would make a future eighth family a no-op
	// that scores as a pass wherever the value coincided.
	return fmt.Errorf("%w: 0xfe atomic kind %d has no arm", ErrNotValidated, a.kind)
}

// checkAlign traps unless the **effective** address is naturally aligned for the access.
//
// The offset is a parameter because the rule is about `ea`, and this file's header records why that
// is the rule despite the reference interpreter computing something else: the normative prose says
// `ea = i + memarg.offset` and traps on `ea` at all six of its atomic sites, while `eval.ml` passes
// the popped operand alone. Prose outranks the implementation snapshot ([ADR
// 0049](../../docs/decisions/0049-atomic-alignment-is-checked-on-the-effective-address-because-the-proposals-normative-prose-outranks-its-own-reference-interpreter.md), #546).
//
// Written as its own function so the rule is a visible signature rather than a `+ offset` that has
// to be spotted at six call sites — and it keeps that property in the direction it now points:
// dropping the parameter is a compile error, not a silent revert to the reference's reading.
func checkAlign(addr, offset, width uint64) error {
	// Exact integer addition, matching the prose's `i + memarg.offset` rather than the wrapped
	// arithmetic a 32-bit address space might suggest: both operands are already widened to uint64
	// by `mem.addr`, so a u32 address plus a u32 offset cannot overflow here, and the bounds check
	// inside `mem.read`/`mem.write` is what rejects an `ea` past the end.
	if (addr+offset)&(width-1) != 0 {
		return trapUnalignedAtomic
	}
	return nil
}

// hostLittleEndian is whether this host stores a word's low byte first.
//
// Probed rather than taken from a build tag because the two forms cost the same and this one cannot
// go stale when a port is added: a `//go:build` list of little-endian architectures is a claim about
// every architecture Go will ever have, and the wrong answer there is silent byte-swapping on the
// new one.
var hostLittleEndian = func() bool {
	probe := uint16(1)
	return *(*byte)(unsafe.Pointer(&probe)) == 1
}()

// guestWord32 and guestWord64 convert between the host's in-memory word order and wasm's.
//
// **This is the function memop.go's endianness comment predicts the need for.** `loadValue` spells
// its little-endian assembly out byte by byte and says why: *"A big-endian host reading through
// unsafe would produce byte-swapped values for every vector, which dual-platform CI would catch only
// if one of its arches were big-endian — and neither is."* Reading a word through `unsafe` is exactly
// what the atomics now do, so that warning applies to them and the portability property has to be
// re-established rather than assumed. `guestWord32` is where it is re-established, and it is an
// involution, so one function serves both directions.
//
// Unexercised by CI, like everything else in the tree that depends on host byte order — which is why
// it is checked against `loadValue` in a test rather than argued: the byte loop is the authority the
// whole spec suite has already validated, and the two must agree on every host.
func guestWord32(v uint32) uint32 {
	if hostLittleEndian {
		return v
	}
	return bits.ReverseBytes32(v)
}

func guestWord64(v uint64) uint64 {
	if hostLittleEndian {
		return v
	}
	return bits.ReverseBytes64(v)
}

// **`guestWord16` stood here and is deleted by ADR 0054, and its own comment had named the condition
// under which it would die.** That comment said the two-byte involution *"exists for the `plain`
// accesses rather than for the atomics: `sync/atomic` has no 16-bit operations, which is why
// `atomicCell` reaches a narrow field through the containing 32-bit word and never needs this."* 0054
// makes every aligned access atomic, so the plain two-byte access it served no longer exists, and an
// aligned 2-byte guest access now reaches its bytes through `atomicCell`'s 32-bit word — where
// `guestWord32` does the normalization *before* the shift, so the byte order stays right on a
// big-endian host without a 16-bit conversion anywhere.
//
// Kept as a note rather than dropped silently because the deleted comment is the clearest statement in
// the tree of why the atomics and the plain path needed different byte-order helpers, and 0054's whole
// content is that there is no longer a plain path to need one.

// atomicCell is one atomic access resolved to the naturally-aligned host word that contains it,
// plus where the access's bytes sit inside that word.
//
// **The 1- and 2-byte accesses are why this indirection exists.** `sync/atomic` has no 8- or 16-bit
// operations — by design, its own doc saying operations on non-word-sized integers are *"inefficient
// or infeasible"* on many architectures — and 32 of the region's rows are that narrow. So a narrow
// access becomes a read-modify-write of the containing 32-bit word, and this type is where the shift
// and mask arithmetic lives once instead of at four call sites (ADR 0051).
//
// The containing word always fits inside the memory: a memory's length is a multiple of the 64 KiB
// page, so `(ea &^ 3) + 4 <= len(bytes)` wherever `ea < len(bytes)`. And a width-2 access is
// guaranteed 2-byte aligned by `checkAlign`, so a field never straddles the word boundary.
//
// **Nothing here is cached.** The pointer is computed per access from the memory's published image,
// which is what makes it impossible for it to be stale; a view captured at construction would survive
// an unshared memory's `grow` reallocating underneath it, and answering from a replaced array is the
// plausible-wrong-answer mode ADR 0051 chose this shape to avoid.
//
// **Per access is exactly one image load, and decision 0058 is why that sentence is now about a
// number.** The pointer is taken from a single `m.view()`, so the bounds check and the
// `unsafe.Pointer` conversion are against the same array — a second load between them would take the
// address of an offset the first array holds and the second may not. What the descriptor does *not*
// buy is coherence: an RMW on an array `grow` has abandoned is invisible to the agents on the
// replacement, which is a lost update rather than a memory-safety failure (**#586**), and `noMove` is
// what keeps it off the shared path. See `grow`'s reallocating arm.
type atomicCell struct {
	// Exactly one of these is non-nil; p64 means the containing word is the access itself.
	p32 *uint32
	p64 *uint64

	// shift is where the field's low bit sits in the *guest's* word, and mask is the field's
	// width at position zero. Both are in guest space, so `8 * (ea - base)` is right on every
	// host — the normalization happens before the shift, not after.
	shift uint
	mask  uint64
}

// cell resolves an atomic access, reporting the same out-of-bounds trap `read` and `write` do.
//
// Called *after* `checkAlign` at every site, which is the trap precedence the reference fixes: an
// unaligned access past the end of memory reports `unaligned atomic`, not the bounds trap.
func (m *memory) cell(addr, offset, width uint64) (atomicCell, error) {
	ea, err := effectiveAddress(addr, offset)
	if err != nil {
		return atomicCell{}, err
	}
	bs := m.view()
	if width > uint64(len(bs)) || ea > uint64(len(bs))-width {
		return atomicCell{}, trapOOB
	}
	if width == 8 {
		return atomicCell{p64: (*uint64)(unsafe.Pointer(&bs[ea])), mask: ^uint64(0)}, nil
	}
	base := ea &^ 3
	return atomicCell{
		p32:   (*uint32)(unsafe.Pointer(&bs[base])),
		shift: 8 * uint(ea-base),
		mask:  uint64(1)<<(8*width) - 1,
	}, nil
}

// word reads the containing word sequentially consistently, in guest byte order.
func (c atomicCell) word() uint64 {
	if c.p64 != nil {
		return guestWord64(atomic.LoadUint64(c.p64))
	}
	return uint64(guestWord32(atomic.LoadUint32(c.p32)))
}

// casWord replaces the whole containing word, comparing the whole containing word.
//
// **The full-word comparison is what makes a narrow access safe for its neighbours.** A concurrent
// write to another byte of the same word changes the word, so this fails and the caller re-reads;
// the write is never lost. Those are disjoint locations in the model — `loc` is a region and a byte
// range — so no rule would permit losing one, and this is why none is lost.
func (c atomicCell) casWord(old, replacement uint64) bool {
	if c.p64 != nil {
		return atomic.CompareAndSwapUint64(c.p64, guestWord64(old), guestWord64(replacement))
	}
	return atomic.CompareAndSwapUint32(c.p32, guestWord32(uint32(old)), guestWord32(uint32(replacement)))
}

// isWholeWord reports whether the field is the containing word, so no read-modify-write is needed.
func (c atomicCell) isWholeWord() bool { return c.p64 != nil || c.mask == 1<<32-1 }

// load is the field, zero-extended — the same value `loadValue` produces for these bytes.
func (c atomicCell) load() uint64 { return c.word() >> c.shift & c.mask }

// store writes the field, leaving the rest of the word alone.
func (c atomicCell) store(v uint64) {
	if c.isWholeWord() {
		if c.p64 != nil {
			atomic.StoreUint64(c.p64, guestWord64(v))
			return
		}
		atomic.StoreUint32(c.p32, guestWord32(uint32(v)))
		return
	}
	c.update(func(uint64) uint64 { return v })
}

// update applies f to the field until it lands, and returns the field's previous value.
//
// **The general form, and still the authority for every operator.** `nativeRmw` below replaces this
// loop for the shapes `sync/atomic` has a single primitive for; everything else — `xor` at every width,
// and `add`/`sub`/`xchg` on a field narrower than its containing word — arrives here, and so does every
// operator on a big-endian host that the native form cannot serve. `applyRmw` stays the single
// definition of the six, which is its own comment's reason: *"one copy of it is one place to be wrong."*
func (c atomicCell) update(f func(uint64) uint64) uint64 {
	for {
		w := c.word()
		old := w >> c.shift & c.mask
		if c.casWord(w, w&^(c.mask<<c.shift)|f(old)&c.mask<<c.shift) {
			return old
		}
	}
}

// nativeRmw applies op with one `sync/atomic` primitive where one exists for this cell's shape, and
// reports whether it did.
//
// **What it replaces is a round-trip, not the arithmetic.** `update` costs a `LoadUint32` and a
// `CompareAndSwapUint32` even uncontended, plus a `func(uint64) uint64` closure per instruction; each
// arm here is one atomic operation and no closure. It is [ADR
// 0057](../../docs/decisions/0057-the-native-atomic-rmw-dispatch-replaces-the-cas-loop-for-the-shapes-syncatomic-can-serve-in-one-operation.md)'s
// mechanism, landed on the measurement #559 pre-registered.
//
// # Which shapes, and why not the ones #559's body names
//
// That body puts the eligible set at *"four of the six at widths 4 and 8"*. Derived from this type's
// own shape instead, it is wider in one direction and narrower in another:
//
//   - **`and` and `or` are native at every width**, including a field inside a containing word, because
//     a field-scoped `and` is a full-word `AndUint32` carrying 1s outside the field and a field-scoped
//     `or` is an `OrUint32` carrying 0s there. Those are the identity elements, so the neighbours'
//     bytes are provably untouched — a *stronger* form of the property `casWord`'s comment argues for,
//     since nothing outside the field is written at all and there is no retry to lose a write in.
//   - **`xchg` is native only whole-word.** Replacing a field means preserving its neighbours, and no
//     single primitive both replaces and preserves.
//   - **`add` and `sub` are native only whole-word *and* only on a little-endian host.** Both
//     restrictions are real: a narrow add would carry out of the field into a neighbour's bytes, and
//     addition does not commute with the byte permutation `guestWord32` applies, because carries cross
//     byte boundaries. `and`, `or` and whole-word replacement do commute with it — π(g & m) = π(g) & π(m)
//     — which is why those three need no host-order condition and these two do.
//   - **`xor` is native at no width**, because `sync/atomic` has no `XorUint32`. Checked against `go doc
//     sync/atomic` rather than assumed.
//
// `sub` is the one arm that writes arithmetic rather than naming a primitive — `AddUint32` of the
// negation — and it is therefore the one place where a second copy of an operator's meaning exists in
// this file. `TestTheNativeRmwDispatchAgreesWithApplyRmw` is what makes that safe: it runs both paths
// over all six operators at all four widths at every in-word field position and requires the pushed
// value *and* the resulting memory image to be identical, so the fast path is checked against
// `applyRmw` rather than trusted beside it.
func (c atomicCell) nativeRmw(op rmwOp, operand uint64) (uint64, bool) {
	switch op {
	case rmwAnd:
		// 1s outside the field, so the neighbours are ANDed with the identity.
		return c.field(c.andWord(^(c.mask << c.shift) | (operand&c.mask)<<c.shift)), true
	case rmwOr:
		// 0s outside the field, likewise.
		return c.field(c.orWord((operand & c.mask) << c.shift)), true
	case rmwXchg:
		if !c.isWholeWord() {
			return 0, false
		}
		return c.field(c.swapWord(operand)), true
	case rmwAdd, rmwSub:
		if !c.isWholeWord() || !hostLittleEndian {
			return 0, false
		}
		if op == rmwSub {
			// Subtraction as addition of the negation, wrapping at the slot exactly as
			// `applyRmw`'s `old - operand` does. The truncation to the word's width happens
			// inside `addWord`.
			operand = -operand
		}
		return c.field(c.addWord(operand)), true
	case rmwXor:
		// No `XorUint32` exists to call. Spelled as its own arm rather than left to the fall-through
		// so the `exhaustive` linter reads it — and so the one operator with no primitive is stated
		// here, where a reader checking the table above lands, instead of inferred from an absence.
		return 0, false
	}
	return 0, false
}

// field extracts this cell's value from a whole word, which is what every primitive below returns.
func (c atomicCell) field(word uint64) uint64 { return word >> c.shift & c.mask }

// andWord, orWord, swapWord and addWord are the four primitives, each returning the *previous* whole
// word in guest byte order.
//
// Their arguments are in guest space, like `shift` and `mask`, and the normalization happens at the
// boundary — the same discipline `word` and `casWord` follow, and for the same reason: a value that is
// sometimes host-ordered and sometimes guest-ordered is a byte-swap waiting for a big-endian port.
func (c atomicCell) andWord(mask uint64) uint64 {
	if c.p64 != nil {
		return guestWord64(atomic.AndUint64(c.p64, guestWord64(mask)))
	}
	return uint64(guestWord32(atomic.AndUint32(c.p32, guestWord32(uint32(mask)))))
}

func (c atomicCell) orWord(mask uint64) uint64 {
	if c.p64 != nil {
		return guestWord64(atomic.OrUint64(c.p64, guestWord64(mask)))
	}
	return uint64(guestWord32(atomic.OrUint32(c.p32, guestWord32(uint32(mask)))))
}

func (c atomicCell) swapWord(v uint64) uint64 {
	if c.p64 != nil {
		return guestWord64(atomic.SwapUint64(c.p64, guestWord64(v)))
	}
	return uint64(guestWord32(atomic.SwapUint32(c.p32, guestWord32(uint32(v)))))
}

// addWord is reached only under hostLittleEndian, so no conversion appears in it — and its absence is
// the point rather than an omission. `guestWord32` is the identity there, and writing it anyway would
// suggest this arm were host-order-independent, which is the one thing it is not.
func (c atomicCell) addWord(delta uint64) uint64 {
	if c.p64 != nil {
		return atomic.AddUint64(c.p64, delta) - delta
	}
	// The old value is the new one minus the delta, wrapping at 32 bits, because `AddUint32`
	// returns the sum where the spec needs what was there.
	return uint64(atomic.AddUint32(c.p32, uint32(delta)) - uint32(delta))
}

// compareAndSwap stores replacement only if the field equals expected, and returns what was there.
//
// **A loop even for a whole-word access, because Go's primitive returns the wrong thing.**
// `CompareAndSwapUint32` reports a bool where the spec needs the value that was observed
// (`i32.atomic.rmw.cmpxchg` pushes the old value whether or not it swapped), and reading it back
// after a failed CAS would report a value the CAS never saw. So the observation comes from the load
// at the top of the loop, which is the only read whose result can honestly be pushed.
func (c atomicCell) compareAndSwap(expected, replacement uint64) uint64 {
	expected &= c.mask
	for {
		w := c.word()
		old := w >> c.shift & c.mask
		if old != expected {
			// No write, and the loop ends here: a mismatch is a complete answer.
			return old
		}
		if c.casWord(w, w&^(c.mask<<c.shift)|replacement&c.mask<<c.shift) {
			return old
		}
	}
}

func (in *Instance) atomicLoad(a atomicop, st *stack, mem *memory, offset uint64) error {
	if err := st.needNum(1); err != nil {
		return err
	}
	addr := mem.addr(st.popNum())
	if err := checkAlign(addr, offset, a.width); err != nil {
		return err
	}
	c, err := mem.cell(addr, offset, a.width)
	if err != nil {
		return err
	}
	st.pushNum(c.load())
	return nil
}

func (in *Instance) atomicStore(a atomicop, st *stack, mem *memory, offset uint64) error {
	if err := st.needNum(2); err != nil {
		return err
	}
	v := st.popNum() // the value, pushed second
	addr := mem.addr(st.popNum())
	if err := checkAlign(addr, offset, a.width); err != nil {
		return err
	}
	c, err := mem.cell(addr, offset, a.width)
	if err != nil {
		return err
	}
	// Truncation is the plain store's, and `memory.writeNum` is where that rule is written down
	// now — #557 deleted `storeBytes`, which this comment used to name. Same rule either way: a
	// store is not a conversion, so `i32.atomic.store8` writes the low byte and discards the rest.
	c.store(v & c.mask)
	return nil
}

// atomicRmw is the read-modify-write family: load, apply, store, push the value that was there.
//
// **The old value is the result, not the new one** (`Num n1 :: vs'`, eval.ml:415), and the
// distinction is invisible for `and`/`or`/`xor` against a zero cell — which is most of a fresh
// memory — so the corpus rows that discriminate it are the ones run after `init`.
//
// The three steps are now one atomic operation rather than three plain ones (#542, ADR 0051). What
// used to be `read`, `applyRmw`, `write` — with a window between each pair where another agent's
// update was lost — is a compare-and-swap loop that retries instead.
func (in *Instance) atomicRmw(a atomicop, st *stack, mem *memory, offset uint64) error {
	if err := st.needNum(2); err != nil {
		return err
	}
	operand := st.popNum()
	addr := mem.addr(st.popNum())
	if err := checkAlign(addr, offset, a.width); err != nil {
		return err
	}
	c, err := mem.cell(addr, offset, a.width)
	if err != nil {
		return err
	}
	// The native primitive where one serves this shape, and the general loop otherwise (ADR 0057).
	// The order matters only for reading: both paths compute the same answer, which is asserted over
	// the whole cross product rather than argued here.
	if old, ok := c.nativeRmw(a.rmw, operand); ok {
		st.pushNum(old)
		return nil
	}
	st.pushNum(c.update(func(old uint64) uint64 { return applyRmw(a.rmw, old, operand) }))
	return nil
}

// applyRmw is `Eval_num.eval_rmwop`.
//
// The arithmetic is done on the *slot* and truncated by `storeBytes`, which is what the reference's
// packed store does — `i32.atomic.rmw8.add_u` of 0xFF and 1 stores 0x00 and returns 0xFF, the carry
// going nowhere. No wrapping needs writing because Go's unsigned arithmetic already wraps at the
// slot, and the narrower widths are cut at the store.
func applyRmw(op rmwOp, old, operand uint64) uint64 {
	switch op {
	case rmwAdd:
		return old + operand
	case rmwSub:
		return old - operand
	case rmwAnd:
		return old & operand
	case rmwOr:
		return old | operand
	case rmwXor:
		return old ^ operand
	case rmwXchg:
		return operand
	}
	// Unreachable: parseAtomicMnemonic refuses a row whose operator is not one of the six.
	panic(fmt.Sprintf("interp: rmw operator %d has no arm", op))
}

// atomicCmpxchg compares, stores only on equality, and pushes the old value either way.
//
// **The comparison is at the access width, not the slot width**, which is the one arm where the
// reference does arithmetic the other six do not: `I32.trunc_to x ((packed_size sz) * 8)` narrows
// `expected` before comparing (eval.ml:424-430). Without it, `i32.atomic.rmw8.cmpxchg_u` with
// expected 0x100 would compare 0x100 against a zero-extended byte and never match, where the spec
// compares the low byte and matches at 0x00.
func (in *Instance) atomicCmpxchg(a atomicop, st *stack, mem *memory, offset uint64) error {
	if err := st.needNum(3); err != nil {
		return err
	}
	replacement := st.popNum()
	expected := st.popNum()
	addr := mem.addr(st.popNum())
	if err := checkAlign(addr, offset, a.width); err != nil {
		return err
	}
	c, err := mem.cell(addr, offset, a.width)
	if err != nil {
		return err
	}
	st.pushNum(c.compareAndSwap(truncTo(expected, a.width), replacement))
	return nil
}

// truncTo keeps the low width bytes, which is `trunc_to`.
//
// A full-width access is returned unchanged rather than shifted by 64, which in Go would be zero
// and would make every `i64.atomic.rmw.cmpxchg` compare against 0.
func truncTo(v, width uint64) uint64 {
	if width >= 8 {
		return v
	}
	return v & (1<<(width*8) - 1)
}

// atomicWait compares the cell against `expected` and reports why it is not waiting.
//
// Three results, and the reference's names for them: 0 "ok" (woken), 1 "not-equal", 2 "timed-out".
// This engine can produce 1 and 2 and **cannot yet produce 0**, because waking requires a notifier
// and v1's §§2-5 thread spawn does not exist.
//
//   - not equal → 1, immediately. Total, and the only path the corpus exercises: `atomic.wast:433`
//     seeds the cell with `0xffffffffffff` and then waits on 0, twice.
//   - equal, and the timeout is short → 2. The reference treats any timeout under
//     `timeout_epsilon` (1e6 ns, eval.ml:45) as having expired already, and with no other agent
//     that reading is exact rather than an approximation.
//   - equal, with a long or infinite timeout → the reference suspends. There is nothing here to
//     suspend, and the two available lies are both wrong in the accept direction: returning 2
//     claims a timeout that did not elapse, and returning 0 claims a wake that never happened.
//     So it reports an engine gap on the *mechanism* channel, which is a fail the board can see
//     rather than a value it would score. #543 tracks it, milestoned v1 with the futex work whose
//     absence is the actual cause.
func (in *Instance) atomicWait(a atomicop, st *stack, mem *memory, offset uint64) error {
	if err := st.needNum(3); err != nil {
		return err
	}
	timeout := int64(st.popNum())
	expected := st.popNum()
	addr := mem.addr(st.popNum())
	if err := checkAlign(addr, offset, a.width); err != nil {
		return err
	}
	// **After the alignment check and before the load**, which is `check_align; check_shared;
	// Memory.load_num` (eval.ml:445-447). An unaligned wait on an unshared memory reports
	// `unaligned atomic`, not `expected shared memory`.
	if !mem.limits.Shared {
		return trapExpectedShared
	}
	c, err := mem.cell(addr, offset, a.width)
	if err != nil {
		return err
	}
	// The compare reads the cell atomically, like every other access in this file: a wait that
	// decided on a torn read would report not-equal for a value no agent ever wrote.
	if c.load() != truncTo(expected, a.width) {
		st.pushI32(1)
		return nil
	}
	const timeoutEpsilon = 1_000_000
	if timeout >= 0 && timeout < timeoutEpsilon {
		st.pushI32(2)
		return nil
	}
	return fmt.Errorf("%w: memory.atomic.wait would suspend, and this engine has no scheduler to suspend (#543)", ErrUnsupportedOp)
}

// atomicNotify wakes nothing, because nothing can be waiting.
//
// **The load is not dead code**, and deleting it would be an accept-direction defect: the reference
// discards its result too (`let _ = Memory.load_num mem addr offset ty`, eval.ml:464) and keeps it
// because the load is what raises the out-of-bounds trap. A notify past the end of memory must trap,
// and this line is the only thing that makes it.
//
// `count = 0` returns 0 by the reference's own fast path. A non-zero count reaches `NotifyAction`
// there and would wake real waiters; here there are none, so 0 is the true answer rather than a
// placeholder — and it stays true until v1 gives the engine something that can wait.
func (in *Instance) atomicNotify(a atomicop, st *stack, mem *memory, offset uint64) error {
	if err := st.needNum(2); err != nil {
		return err
	}
	st.popNum() // count: read for the stack discipline, and 0 is the answer at every value
	addr := mem.addr(st.popNum())
	if err := checkAlign(addr, offset, a.width); err != nil {
		return err
	}
	if _, err := mem.read(addr, offset, a.width); err != nil {
		return err
	}
	st.pushI32(0)
	return nil
}
