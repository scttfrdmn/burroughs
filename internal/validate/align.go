// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The alignment constraint, and why it is a whole file.
//
// `valid.ml:380-394` is `check_memop`, and the rule this file exists for is its first `require`:
//
//	let size = match get_sz memop.pack with
//	  | None -> ty_size memop.ty
//	  | Some sz -> check_pack sz (ty_size memop.ty) at; Pack.packed_size sz
//	require (1 lsl memop.align >= 1 && 1 lsl memop.align <= size) at
//	  "alignment must not be larger than natural"
//
// The other two `require`s in the function are named at the end of this comment; the extent cited
// above is the whole function, not the quote.
//
// Every term in it is a fact about the *opcode* rather than about the module: which pack a row
// carries, which value type it names, and which of six `get_sz` functions its family passes. So the
// rule is a comparison against a per-opcode constant, and the whole difficulty is getting the
// constant from the reference rather than from recollection — 45 rows, five distinct widths, and an
// error in any row is accept-direction in exactly the way `sig.go`'s header describes.
//
// **This rule is why #306 existed at all.** The decoder read the alignment and dropped it, so the
// validator could not state a constraint it already knew, and 54 `assert_invalid` vectors were
// accepted — sixteen of them under-rejections that no message-match could see, since a validator
// that says nothing produces no message to disagree with (`internal/validate/authority_test.go`'s
// third blind spot). Retention landed in `decodeMemop`; this is the consumer that makes it worth
// retaining.
//
// # The exponent is compared, not the shift
//
// The reference computes `1 lsl align` and guards it with `>= 1`, which reads as a lower bound and
// is really an *overflow* guard: `align` comes from six bits, so `1 lsl 63` is `min_int` on OCaml's
// 63-bit ints and goes negative, and the `>= 1` clause is what catches that instead of admitting it.
// Comparing exponents (`align > log2(size)`) is equivalent over the entire six-bit domain and has no
// overflow to guard, so the guard has no translation here. Stated because a reader comparing the two
// forms should not have to wonder whether a clause was dropped: it was subsumed, and the arithmetic
// is why.
//
// # Where the widths come from
//
// The name, in `sig.go`'s posture — *derived, not transcribed* — and for its reason: a hand-written
// row per opcode has 45 chances to be wrong in the direction the board scores green. The reference's
// own constructors (`syntax/mnemonics.ml`) carry the `ty` and `pack` this parses back out, and
// `TestNaturalWidthsMatchTheReference` re-derives all 45 from that file and compares. So the parsing
// below is a *decoder for the reference's naming scheme*, not a table of facts about Wasm.
//
//   - `iNN.load` / `fNN.store` and friends with no pack suffix — the value type's own size, NN/8.
//   - `iNN.loadK_s` / `iNN.storeK` — the pack's size, K/8. (`i64.load32_u` is four bytes, not eight:
//     `pack = Some (Pack32, U)`.)
//   - `v128.load` / `v128.store` — 16, the vector's own size. `v128.store`'s family passes
//     `fun _ -> None`, so its `()` pack never reaches the pack branch and `vec_size` decides.
//   - `v128.loadNxM_s` — N*M/8. The pack is `Pack64` for all six of these (`8x8`, `16x4`, `32x2` all
//     read eight bytes and extend lanewise), which the product recovers without special-casing.
//   - `v128.loadN_splat` / `v128.loadN_zero` / `v128.loadN_lane` / `v128.storeN_lane` — N/8.
//
// The one thing the name does not carry is the *family*, and the family is what picks `get_sz`. It
// does not need to: the four `get_sz` forms that consult the pack all yield the pack's size, and the
// one that does not (`VecStore`'s `fun _ -> None`) belongs to the single row whose name carries no
// pack suffix. `TestNaturalWidthsMatchTheReference` reads the families out of `valid.ml` and asserts
// that coincidence rather than trusting it, because it is a coincidence and not a rule: a proposal
// adding `v128.store8x8` would break it, and the test is what says so.
//
// # check_memop's other two rules, and where each of them stands
//
// The reference function has three `require`s in it, and naming the ones this file does not implement
// is the point of saying so — an unimplemented rule that nobody wrote down reads exactly like a
// rule that does not exist. **Two of the three are now implemented**: the alignment rule above, and
// the offset bound in `checkOffset` below, which #310 landed. The section keeps its shape and its
// tense is corrected in place, because *which* rule was missing and for how long is the durable part.
//
//   - **`check_pack sz (ty_size memop.ty)` → `invalid sign extension`** (`:365`). Unreachable across
//     all 45 rows and not by luck: it requires `packed_size < ty_size`, and no constructor in
//     `mnemonics.ml` pairs a pack with a type it does not fit — the widest pack any row carries is
//     `Pack64` (8) against `vec_size` 16, and `i64.load32_u` is 4 against 8. There is no opcode for
//     the violating case, so this is a rule about the reference's *own* internal consistency rather
//     than about a module. `TestNaturalWidthsMatchTheReference` computes both sides, so a proposal
//     that introduces the case fails there rather than reaching an arm that does not exist.
//   - **`offset < 0x1_0000_0000` for an I32AT memory → `offset out of range`** (`:392`). Reachable,
//     and **unimplemented until #310** — an under-rejection this file named for two slices before
//     repairing: the offset is a u64 on the wire (`decodeMemop`'s own comment —
//     `binary-leb128.wast:730` needs the wide read), so a module could encode one past 2^32 against
//     a memory32 and this validator accepted it. **Four corpus vectors expect the string and exactly
//     one reaches this package**, measured rather than counted from grep: `align.wast:1004` is a
//     plain `(module …)` and landed in the admission census; `address.wast:213` and
//     `simd_address.wast:143,151` are `(module quote "…")` forms the wast reader does not build, so
//     they sit in the *unsupported* column and cannot express an opinion about the validator at all.
//     The reward was 1 today and 4 when `module quote` reads. It was a different *class* from #306's:
//     nothing was lost by the decoder, the offset having been retained in `Imm0` all along — a rule
//     never written, where alignment was a rule that could not be written. The decision inside it —
//     that the reference reads the address type from `memory c (0l @@ at)`, memory **0** literally,
//     not the instruction's own `x` — went to Scott rather than being settled in code, and
//     `checkOffset` carries the ruling and the divergence's observability condition.

// checkMemop is `check_memop` (`valid.ml:380`), and it is one function here because it is one
// function there: all six memarg arms call it, immediately after resolving the memory.
//
// It returns the *address* type rather than the reference's `memop.ty`, which is the difference
// between the two languages' arms and not a divergence. The reference gets the value type back so
// each arm can write `[NumT t] --> …`; here the arms already know their value type from the
// mnemonic (`signature`) or the family (`vecSignature`), and what they cannot get without a module
// lookup is whether the address is i32 or i64. So the shared preamble hands back the fact the arms
// share.
//
// **The order of the four steps is the reference's, and each step has a vector that reads it.**
// The memory lookup is first (`memory c x`, which #310 made a lookup *by the instruction's index*
// rather than of memory 0) so a module with no memory reports `unknown memory 0` and not an
// alignment complaint; alignment is next; the **offset bound** third, matching `check_memop`'s own
// order; the lane bound is *after* all of them, which is why the two `Vec…Lane` arms still call
// `checkPackedLaneIndex` themselves instead of it moving in here. `simd_store8_lane.wast:427` is the specimen for the last
// one: `v128.store8_lane align=2 0` has a legal lane and an illegal alignment, and its module also
// declares `(result v128)` on a function whose body stores — three candidate verdicts, of which the
// reference reports this one.
func checkMemop(m *binary.Module, in binary.Instr, name string) (binary.ValType, error) {
	// A row carrying no memarg encodes neither a memory index nor an offset, so it names memory 0
	// and has nothing for the bound to reject. That is the *absence* of the two operands rather than
	// an exemption from the two rules — all six call sites are load/store families, which the table
	// marks, and a row that reached here without a memarg would be answering both rules with the
	// values its encoding does not carry.
	var offset uint64
	var memIdx uint32
	if binary.HasMemarg(in.Prefix, in.Op) {
		offset, memIdx, _ = binary.Memarg(in.Imm0, in.Imm1)
	}
	addr, err := addrTypeAt(m, memIdx)
	if err != nil {
		return binary.ValType{}, err
	}
	if err := checkAlignment(in, name); err != nil {
		return binary.ValType{}, err
	}
	if err := checkOffset(in, addr, offset); err != nil {
		return binary.ValType{}, err
	}
	return addr, nil
}

// checkOffset is `check_memop`'s third and last `require` — the offset bound (`valid.ml:390-393`):
//
//	let MemoryT (at_, _lim) = memory c (0l @@ at) in
//	if at_ = I32AT then
//	  require (I64.lt_u memop.offset 0x1_0000_0000L) at
//	    "offset out of range";
//
// The offset is read as a full u64 on the wire — `decodeMemop` cites `binary-leb128.wast:730` for
// why the width is needed — and retained in `Imm0`, so a module can encode an offset past 2^32
// against a 32-bit memory and this is the rule that refuses it. The bound is unconditional for a
// 64-bit memory: a u64 offset cannot leave a 64-bit address space, so there is nothing to compare.
//
// # It reads the memory the instruction names, and the reference does not (#310)
//
// The quote above says `memory c (0l @@ at)` — memory **0**, hardcoded — while the *caller* two
// functions up reads `memory c x` for the operand type. The reference therefore consults two
// different memories inside one instruction's check, and Burroughs deliberately does not: the bound
// comes from the memory the instruction names, the same one the operand type comes from.
//
// **Ruled by Scott on #310**, and the reasoning is worth keeping next to the divergence rather than
// only in the issue. `valid.ml` is an *oracle* for conformance to the spec, not the norm itself;
// where an oracle and the norm disagree the oracle is the thing that is wrong, and a check that
// contradicts itself internally is far more likely an artifact than an intent. Bug-compatibility
// would have bought agreement with the oracle at the price of being wrong, at a price invisible
// today and unbounded later.
//
// # When the divergence becomes observable, stated as a condition
//
// A divergence at zero measured cost is deferred rather than costless, so the record says what
// defers it. Ours and the reference's verdicts differ on exactly one shape:
//
//   - a module declaring **two or more memories whose index types differ**, and
//   - an instruction naming a **non-zero** memory, and
//   - an offset **at or past 2^32**.
//
// All three are needed, and the first two require the **multi-memory** and **memory64** gates both
// on — multi-memory to encode a non-zero index at all (`internal/binary/instr.go`'s bit 6), and
// memory64 for two memories to disagree about their index type. No corpus vector meets the
// condition today: all four expecting `offset out of range` declare one memory, and exactly two
// `offset=` tokens in the whole suite reach 2^32, both in that group. So this rule is exercised in
// the direction where the two readings agree and in no other, which is why the pair of unit tests
// below construct the discriminating modules by hand — and why
// `TestReferenceStillReadsMemoryZeroForTheOffsetBound` watches the reference's own text, since the
// day upstream repairs its inconsistency is the day this divergence should be re-ruled rather than
// silently kept.
func checkOffset(in binary.Instr, addr binary.ValType, offset uint64) error {
	if addr != binary.I32 {
		return nil
	}
	if offset >= 1<<32 {
		// The reference's text verbatim per 0003, with the wrapped half naming the instruction and
		// the offset — the four vectors expecting this string cannot say which row produced it, so
		// the arrangement is ErrAlignmentTooLarge's directly above, for its reason.
		return fmt.Errorf("%w: %s offset %d does not fit a 32-bit address space",
			ErrOffsetOutOfRange, mnemonic(in), offset)
	}
	return nil
}

// checkAlignment is `check_memop`'s alignment `require`, split out so the rule and the preamble that
// orders it are separately readable.
//
// The domain is `binary.HasMemarg`'s rather than this package's guess about which mnemonics carry a
// memarg — one derived domain, from the generated table, instead of a second opinion that can
// under-match and exempt a row in silence.
func checkAlignment(in binary.Instr, name string) error {
	if !binary.HasMemarg(in.Prefix, in.Op) {
		return nil
	}
	natural, ok := naturalWidth(name)
	if !ok {
		return fmt.Errorf("%w: %s (%#02x %#02x)", errNoNaturalWidth, name, in.Prefix, in.Op)
	}
	_, _, alignExp := binary.Memarg(in.Imm0, in.Imm1)
	if atomicAccess(name) {
		// The threads pin's `Atomic` mode: equality, not a ceiling. Returned from here rather than
		// falling through to the comparison below, because the two rules are not nested — an
		// alignment *smaller* than natural passes the ceiling and fails this.
		if uint64(1)<<alignExp != natural {
			return fmt.Errorf("%w: %s aligns to %d bytes, natural is %d",
				ErrAtomicAlignment, mnemonic(in), uint64(1)<<alignExp, natural)
		}
		return nil
	}
	if uint64(1)<<alignExp > natural {
		// The reference's text verbatim, per 0003, and the detail after it: 62 corpus vectors
		// expect this string and not one of them can say which row produced it, so the sentinel
		// is shared and the wrapped half names the instruction and both widths — exactly
		// ErrInvalidLaneIndex's arrangement one file over, for the same reason.
		return fmt.Errorf("%w: %s aligns to %d bytes, natural is %d",
			ErrAlignmentTooLarge, mnemonic(in), uint64(1)<<alignExp, natural)
	}
	return nil
}

// atomicAccess reports whether a memarg row is checked in the threads pin's `Atomic` mode —
// `1 lsl align = size` rather than `<= size` (`spec-threads/valid/valid.ml:203-209`).
//
// # Keyed on the name's `atomic` component, not on the 0xfe prefix
//
// The reference keys the mode on the *constructor family*: six arms pass `Atomic` and six pass
// `NonAtomic`, and nothing about the opcode's encoding appears in the choice. The nearest thing this
// package has to a family is the mnemonic, which is the same join `naturalWidth` and `signature`
// already read, so this reads the mnemonic too. Keying on `in.Prefix == 0xfe` would be a claim about
// *where the proposal put its opcodes* standing in for a claim about which rule applies — true
// today, and true by a coincidence the authority does not state anywhere.
//
// A component rather than a substring, so a future mnemonic with `atomic` inside a longer word does
// not silently acquire the stricter rule. `TestAtomicModeMatchesTheThreadsReference` derives the
// mode per family from `valid.ml` and checks this predicate against it over both pins' constructor
// sets, in both directions — which is what makes the paragraph above a checked claim rather than an
// argument.
func atomicAccess(name string) bool {
	for _, part := range strings.Split(name, "_") {
		if part == "atomic" {
			return true
		}
	}
	return false
}

// errNoNaturalWidth is a row the table says carries a memarg and naturalWidth could not read.
//
// **Undeclared, and unreachable by construction** — `errNoImmReader`'s posture (`internal/binary`),
// for its reason. This is not a decline: ErrUnsupported means *"not yet in this slice's
// vocabulary"*, and a row whose signature arm just ran is plainly in the vocabulary, so reporting
// one here would put a naming bug in the census column that reads as scope. It is not a verdict
// either — nothing about the module is wrong. It is this file failing to parse a name it is
// responsible for, and the only honest channel for that is an internal error nobody expects to see.
//
// `TestEveryMemargRowHasANaturalWidth` walks `binary.HasMemarg`'s whole domain, so the unreachable
// claim is checked rather than asserted; reaching this at run time means that test was deleted.
var errNoNaturalWidth = errors.New("internal: no natural width for a memarg row")

// naturalWidth returns a memory access's natural width in bytes, read out of the reference's
// mnemonic, and whether the name is one this decoder recognizes.
//
// See the file header for the naming scheme and for why the reference's `get_sz` families do not
// have to appear here.
func naturalWidth(name string) (uint64, bool) {
	// The table spells mnemonics `i32_load8_u`; the spec and this parser read `i32.load8_u`.
	name = strings.ReplaceAll(name, "_", ".")
	prefix, rest, found := strings.Cut(name, ".")
	if !found {
		return 0, false
	}

	// The signedness/zero/splat suffixes carry no width. Stripped before the digits are read so
	// `load8x8.s` and `load8x8.u` are one case, and `load32.zero` does not read `32.zero`.
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, ".s"), ".u")
	for _, suffix := range []string{".zero", ".splat", ".lane"} {
		rest = strings.TrimSuffix(rest, suffix)
	}

	// The atomics naming scheme, threads-pin spelling. `atomic` is a component of the name and not
	// of the shape: `i32.atomic.load8_u`'s width comes from the same pack the non-atomic
	// `i32.load8_u`'s does, and the reference says so by giving both `pack = Some Pack8`. Stripped
	// rather than branched on, so the width arithmetic below has one form and the atomic rows cannot
	// drift away from the core rows they share a rule with. `memory.atomic.wait32` reaches here as
	// prefix `memory` and is the one row whose value type is in the *suffix* — handled below.
	rest = strings.TrimPrefix(rest, "atomic.")

	var op string
	switch {
	case strings.HasPrefix(rest, "load"):
		op = strings.TrimPrefix(rest, "load")
	case strings.HasPrefix(rest, "store"):
		op = strings.TrimPrefix(rest, "store")
	// `rmw` and `rmw.cmpxchg` read the same width as the load and store of the same pack, which is
	// again the reference's own statement: `AtomicRmw`, `AtomicRmwCmpXchg`, `AtomicLoad` and
	// `AtomicStore` all pass `num_size` and `(fun sz -> sz)` to `check_memop`, so the family is not
	// a term in the width at all. The `.cmpxchg` suffix carries no width, like `.s` and `.u` above,
	// and `i64.atomic.rmw32.u.cmpxchg` has already lost its `.u` to the trim above.
	//
	// `.cmpxchg` comes off before `.u`, and the order is the bug this would otherwise have: the
	// signedness strip above only runs on a *suffix*, and `i64.atomic.rmw32_u_cmpxchg` spells its
	// `u` in the middle, so `rmw32.u.cmpxchg` still carries it here. Trimmed in the other order the
	// pack would read `32.u`, `ParseUint` would decline, and the row would return false — a
	// decline, not a wrong width, so it fails loudly at the domain control rather than quietly at
	// the rule.
	case strings.HasPrefix(rest, "rmw"):
		op = strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(rest, "rmw"), ".cmpxchg"), ".u")
	// `memory.atomic.notify` and `memory.atomic.wait32`/`wait64`. The address prefix is `memory`, so
	// nothing about the value type is on the left of the name: `notify` is `ty = I32Type, pack =
	// None` (4 bytes) and `waitNN` is `ty = INNType, pack = None`, which the digits recover. Read as
	// a value-type width and not as a pack for that reason — the pack is None on all three, and a
	// row whose width came out of the pack branch would be agreeing with the reference by accident.
	case prefix == "memory" && rest == "notify":
		return 4, true
	case prefix == "memory" && strings.HasPrefix(rest, "wait"):
		bits, ok := numericTypeBits("i" + strings.TrimPrefix(rest, "wait"))
		return bits / 8, ok
	default:
		return 0, false
	}

	// No pack suffix: the value type's own size decides, which is `ty_size memop.ty` — 16 for
	// the vector families, NN/8 for a numeric type.
	if op == "" {
		if prefix == "v128" {
			return 16, true
		}
		bits, ok := numericTypeBits(prefix)
		return bits / 8, ok
	}

	// A pack suffix. `NxM` is a lane-extending load whose pack reads N*M bits; a bare `N` reads N.
	lo, hi, isProduct := strings.Cut(op, "x")
	n, err := strconv.ParseUint(lo, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	bits := n
	if isProduct {
		m, err := strconv.ParseUint(hi, 10, 32)
		if err != nil || m == 0 {
			return 0, false
		}
		bits = n * m
	}
	if bits%8 != 0 {
		return 0, false
	}
	return bits / 8, true
}

// numericTypeBits is the width of a numeric type's *name*, which is the only place the reference
// writes it down for this purpose (`num_size`).
//
// Its own function rather than a `strings.TrimLeft(prefix, "if")` on the digits, because that would
// accept `i128` and `f8` — names no type has — and return a confident answer for them. A closed set
// answers "not a numeric type" for everything outside it, which is what the caller's second return
// is for.
func numericTypeBits(prefix string) (uint64, bool) {
	switch prefix {
	case "i32", "f32":
		return 32, true
	case "i64", "f64":
		return 64, true
	}
	return 0, false
}
