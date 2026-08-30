// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// #524's validation half: **the 0xFE region's types.**
//
// The reader half (#534) gave the 67 atomic mnemonics shapes, so `atomic.wast` parses; this file is
// the type checker's side of the same slice. Its population is the file's 48 `assert_invalid`
// vectors and 3 `module` definitions, all 51 of which were declining at `instr.go`'s region
// dispatch, and it is *their own oracle*: every one of the 48 expects `unknown memory`, which is a
// verdict this package already produces for every other memarg family.
//
// # The authority is the threads pin, and it is the only pin that has these arms
//
// `spec-threads/interpreter/valid/valid.ml:523-541`, `MemoryAtomicWait` … `AtomicRmwCmpXchg`:
// seven arms, quoted because the whole file is a transcription of them.
//
//	| MemoryAtomicWait atomicop -> check_memop Atomic …;
//	  [NumType I32Type; NumType atomicop.ty; NumType I64Type] --> [NumType I32Type]
//	| MemoryAtomicNotify atomicop -> check_memop Atomic …;
//	  [NumType I32Type; NumType I32Type] --> [NumType I32Type]
//	| AtomicFence -> [] --> []
//	| AtomicLoad atomicop -> check_memop Atomic …;
//	  [NumType I32Type] --> [NumType atomicop.ty]
//	| AtomicStore atomicop -> check_memop Atomic …;
//	  [NumType I32Type; NumType atomicop.ty] --> []
//	| AtomicRmw (rmwop, atomicop) -> check_memop Atomic …;
//	  [NumType I32Type; NumType atomicop.ty] --> [NumType atomicop.ty]
//	| AtomicRmwCmpXchg atomicop -> check_memop Atomic …;
//	  [NumType I32Type; NumType atomicop.ty; NumType atomicop.ty] --> [NumType atomicop.ty]
//
// The **core** pin has none of them — `grep -c Atomic` over `spec/interpreter/valid/valid.ml` at
// `bdd7164b` is 0 — so unlike every other region in this package there is no second authority to
// cross-check against, and `TestAtomicSignaturesMatchTheThreadsReference` derives the seven arms
// from the one there is rather than transcribing them into a table here.
//
// # Derived from the mnemonic, because a 67-row table would be 67 invisible errors
//
// `sig.go`'s header makes this argument for ~180 opcodes and it is stronger here, because the
// region is bigger relative to its witnesses: a hand-written row saying `i64.atomic.rmw32.add_u`
// returns `i32` makes this package **accept** modules the spec refuses, and no `assert_invalid`
// vector can see it (contract §9 G-3). So the type comes out of the name.
//
// The name carries everything the seven arms read. `atomicop.ty` is the mnemonic's type prefix;
// the family is its operator component; and the *rmw operator* — `add`, `sub`, `and`, `or`, `xor`,
// `xchg` — is not a term in any of the seven signatures at all, which the generated table says
// out loud: `0x1e` and `0x25` are **both** `mnemonic: "i32_atomic_rmw"`, differing only in an
// `operator` field that `binary.PrefixedOp` does not return. Forty-two of the sixty-seven rows
// collapse onto four signatures for that reason, and a table would have spelled all forty-two.
//
// # The address operand's type is composed from the two pins, and that is a considered divergence
//
// Every arm above says `NumType I32Type` for the address. This file passes the memory's own
// address type instead, from `checkMemop` — i64 for a memory64 — and the reason is that the
// threads pin's `I32Type` is **the baseline's age, not a statement about atomics**:
//
//   - the same pin's plain `Load` arm also says `[NumType I32Type] --> [NumType memop.ty]`
//     (`spec-threads/valid/valid.ml:363`), and this engine already declines to follow it there;
//   - the pin contains **no** `I64AT` and no `addrtype` — `grep -c` is 0 — so it predates memory64
//     and cannot express the question;
//   - the core pin, which does have address types, answers it for the identical question one family
//     over: `Load` at `valid.ml:653-656` reads `let MemoryT (at, _lim) = memory c x` and then
//     `[NumT (numtype_of_addrtype at)]`. Bare is the core pin by the citation rule, and it is bare
//     here deliberately — the bullet above it names the other pin in full, so the two spellings in
//     one list are the difference they encode rather than an inconsistency.
//
// So the two pins are composed rather than one of them followed off a cliff: the threads pin owns
// the *mode* and the *shape*, the core pin owns *how a memarg's address is typed*, and taking the
// atomics' address from the older pin would hardcode i32 into the one region whose authority is
// too old to have been asked.
//
// **Flagged to Scott rather than settled here**, on #310's precedent — that is the other place this
// package diverges from an authority inside a memarg check (`checkOffset`, where the reference reads
// memory 0 for the offset bound and this engine reads the instruction's own memory), and it was
// Scott's ruling and not a code decision. The observability condition, stated so the deferral has a
// trigger rather than a date:
//
//   - the **memory64** gate on, and
//   - an atomic access to a memory whose index type is i64.
//
// Both are needed and no corpus vector meets either: `atomic.wast`'s three modules all declare
// `(memory 1 1 shared)`. With memory64 off, `checkMemop` returns i32 for every memory and this
// file and the authority agree on every row — which is why
// `TestAtomicAddressTypeIsTheNamedMemorys` builds the discriminating module by hand, exactly as
// `checkOffset`'s pair does.
//
// # What the alignment rule cost to *reach*
//
// The rule `check_memop` applies in `Atomic` mode (`spec-threads/valid/valid.ml:203-209`) —
// `1 lsl align = size`, equality rather than a ceiling — was already written, already derived from
// the pin, and checked in both directions by
// `TestAtomicModeMatchesTheThreadsReference`. It was also **unreachable**: its
// only production caller is `checkAlignment`, reached from `checkMemop`, whose six call sites were
// slice 1's loads and stores and slice 2's four vector families. Nothing could hand it a 0xfe row,
// because `instr.go` declined the region first. *A control can test the helper, not the path.* This
// file's `checkMemop` call is the seventh site and the first that makes the rule run.
//
// It runs correctly, which was printed and not assumed, and the printing is why `atomicAccess`'
// separator handling was hardened next door without a grave: the predicate answered differently for
// `i32_atomic_load` and `i32.atomic.load`, and the write-up asserting that this path would therefore
// be checked in NonAtomic mode was **wrong** — the arm below passes the table's spelling, the one the
// old predicate read. That section carries the withdrawal and the two measurements.
//
// # Its citation was pointing at the wrong pin, and naming the subject is what found that
//
// The citation above carried no qualifier, and an unqualified one is the **core** pin. The core pin
// has no atomics at all, so those line numbers there land in `check_memorytype`'s body — a rule about
// page limits, cited under a sentence about alignment modes, in the one file whose header argues at
// length that the threads pin is the only authority these arms have. Both of this rule's other
// citations in the package had the qualifier — `internal/validate/align.go:atomicAccess`'s doc block
// and `align_atomic_test.go`'s header — which is what makes it drift rather than a misreading: the
// qualifier was dropped, and a dropped qualifier does not dangle, it **re-points**.
//
// The wrong form is described here and not spelled, and the right one is pointed at rather than
// quoted. A citation sweep reads tokens and not quotation marks, so a paragraph about a bad citation
// that transcribes it adds a real row to the population it is reporting on — and a paragraph
// transcribing the good form adds a second row whose only content is that it agrees. Every range
// citation in this file therefore names its subject and none is residue.
//
// What is worth recording is which instrument caught it. `TestReferenceRangeCitationsAreWellFormed`
// counted the citation and checked its bounds — 209 is inside the core pin's length, so it passed.
// `TestRangeCitationSubjectsAreReadFromTheReference` skipped it, because the first draft of the
// sentence put the rule's transcription on the citation's line and the rule's *name* on the line
// above, so the row landed in **residue**: counted, unkeyed, unchecked. It failed the moment the
// sentence was rewritten to name `check_memop` and `Atomic` on the citation's own line, and it failed
// with the exact reading — "neither written inside [[203 209]] nor is any of those ranges inside its
// own definition at [380 416]".
//
// So the residue column is not a neutral holding pen. The sibling pin's header records its rows as
// "recorded rather than repaired", weighing a faithful transcription against a keyable name; this is
// the first row in that ledger that was **wrong while excused**, and it says which way the trade
// actually runs. The two other range citations this file writes are keyed for that reason, and
// keying them cost one rewrite each.
//
// # The rule this slice implements has **zero** witnesses on the board
//
// `testdata/spec/proposals/threads/atomic.wast` — the file the harness runs — holds 48
// `assert_invalid` vectors and every one expects `unknown memory`. The threads pin's *own* copy of
// the same-named file holds **93**: those 48, and a further **45** expecting
// `atomic alignment must be natural`. The board's corpus and the atomics' authority are two
// different snapshots of the proposal:
//
//	testdata/spec           WebAssembly/testsuite @ de54fd27  (fetch-spec-tests.sh)   48 assert_invalid
//	third_party/spec-threads WebAssembly/threads  @ cc535ada  (fetch-threads-ref.sh)  93 assert_invalid
//
// So the equality rule ships with no corpus vector exercising it, in either direction, and the near
// miss above would have been invisible on the board rather than caught by it. That is why
// `TestAtomicAlignmentIsEqualityNotACeiling` builds its modules by hand, and it is filed as #537
// rather than fixed here: bumping a corpus pin is its own reviewable diff (#42).
//
// # What is not here
//
//   - **`shared` is not a validation rule.** The threads pin's `atomic.wast` has two
//     `expected shared memory` rows and both are `assert_trap`; the string is `exec/eval.ml:152`'s,
//     an execution-time refusal. The type checker does not ask. The board's copy of the file has
//     neither the string nor the rows — **stated with the path, because this is the second claim in
//     this slice that was measured on the wrong same-named file** (the first was the 45 alignment
//     vectors, see above). A bare `atomic.wast` names two files with different contents and one of
//     them is not the one the harness runs, so a count without a path is not a measurement here.
//   - **`check_pack`** at `spec-threads/valid/valid.ml:176-177`, whose body is the one line
//     `require (packed_size sz < t_sz) at "invalid sign extension"`, is in the reference's shared
//     preamble and in none of this package's — an omission that predates this
//     slice and applies identically to the core rows. Every one of the 67 rows in `opTableFE` packs
//     strictly narrower than its value type (`load8_u`/`load16_u` against i32, and those plus
//     `load32_u` against i64, and the same for the store and rmw families), so no decoded module
//     reaches it through this region.
//   - **The gate.** 0xFE is read by the decoder, which is the layer that owns a proposal's edge
//     (grave #427's lesson, one region over). Nothing here consults it.

// prefixAtomic is the threads region's prefix byte.
//
// Local for prefixSIMD's reason, which that constant states in full, and for prefixBulk's and
// prefixGC's after it: `internal/binary` spells the prefix as a literal at each of its own sites, so
// exporting a shared one is a convention change across two packages that no slice has been asked to
// make. `TestPrefixAtomicIsTheRegionBinaryDispatches` checks it against `binary`'s own dispatch
// rather than leaving the two agreeing by inspection.
const prefixAtomic = 0xfe

// atomicInstr types one 0xFE instruction, mirroring vecInstr and bulkInstr: resolve the signature,
// pop the operands, push the results.
func (v *validator) atomicInstr(in binary.Instr) error {
	s, err := atomicSignature(v.mod, in)
	if err != nil {
		return err
	}
	if err := v.popExpectAll(s.params); err != nil {
		return err
	}
	v.pushAll(s.results)
	return nil
}

// atomicFamily is one of the seven arms above, named as the reference's constructor names it.
type atomicFamily int

const (
	atomicNone atomicFamily = iota
	atomicFence
	atomicNotify
	atomicWait
	atomicLoad
	atomicStore
	atomicRmw
	atomicCmpXchg
)

// atomicSignature returns a 0xFE instruction's type, or a decline.
//
// Three-valued exactly as `signature`, `vecSignature` and `bulkSignature` are: nil means the type is
// known, ErrUnsupported means this slice has no rule for the opcode, and anything else is a rule
// that *is* known and that the module fails. The memory lookup is inside `checkMemop`, so a module
// with no memory reports `unknown memory 0` — which is what all 48 of the board's vectors expect,
// and it arrives here for free rather than as an arm of its own.
func atomicSignature(m *binary.Module, in binary.Instr) (sig, error) {
	name, ok := opMnemonic(in)
	if !ok {
		return sig{}, errNoAtomicSignature(in)
	}
	fam, ty, ok := atomicForm(name)
	if !ok {
		return sig{}, errNoAtomicSignature(in)
	}

	i32 := binary.I32

	// `AtomicFence -> [] --> []`, and it is answered before the memarg preamble because it has no
	// memarg and no `memory c x`: the reference's arm is the one line, with no `check_memop` above
	// it. Routing it through `checkMemop` would invent an `unknown memory` verdict for a module the
	// reference validates.
	if fam == atomicFence {
		return sig{}, nil
	}

	// The shared preamble, in the reference's order: resolve the memory (and with it the address
	// type), then alignment, then the offset bound. All six of the memarg-bearing families take it,
	// which is the same reason slice 1's two arms and slice 2's four route here rather than reading
	// the address type directly.
	addr, err := checkMemop(m, in, name)
	if err != nil {
		return sig{}, err
	}

	switch fam {
	case atomicLoad: // [addr] --> [ty]
		return sig{params: []binary.ValType{addr}, results: []binary.ValType{ty}}, nil

	case atomicStore: // [addr; ty] --> []
		return sig{params: []binary.ValType{addr, ty}}, nil

	case atomicRmw: // [addr; ty] --> [ty]
		return sig{params: []binary.ValType{addr, ty}, results: []binary.ValType{ty}}, nil

	case atomicCmpXchg: // [addr; ty; ty] --> [ty]
		return sig{params: []binary.ValType{addr, ty, ty}, results: []binary.ValType{ty}}, nil

	case atomicNotify: // [addr; i32] --> [i32]
		return sig{params: []binary.ValType{addr, i32}, results: []binary.ValType{i32}}, nil

	case atomicWait:
		// `[i32; atomicop.ty; I64Type] --> [i32]`. The **timeout is i64 for both** widths — it is a
		// nanosecond count and not a value read from memory — so `wait32`'s operands are
		// `(addr, i32, i64)` and only the *expected* operand follows the mnemonic's width. A
		// signature deriving all three from the name would type `wait32`'s timeout as i32 and accept
		// modules the reference refuses.
		return sig{params: []binary.ValType{addr, ty, binary.I64}, results: []binary.ValType{i32}}, nil

	default:
		// `atomicNone` and `atomicFence`, and the two arrive here for opposite reasons: `atomicNone`
		// is `atomicForm` saying *no* and is already refused above, while `atomicFence` is answered
		// before the memarg preamble and cannot reach a switch that runs after it. Spelled as a
		// `default` rather than as two named cases because a named case for either would be a line
		// claiming a family is typed here when it is not, and `exhaustive` reads a real fallback as
		// handling (the linter's own config says so).
		//
		// Unreachable through a decoded module either way — `atomicForm` answers for every row in
		// `opTableFE`, which `TestEveryAtomicRowHasASignature` walks — and returned rather than
		// panicked for `errNoSignature`'s reason: a row arriving here from a widened table should land
		// in a named bucket, not in a crash or an unearned accept.
		return sig{}, errNoAtomicSignature(in)
	}
}

// atomicForm reads a 0xFE mnemonic's family and value type off the name.
//
// The value type is `atomicop.ty`: the mnemonic's type prefix for the five `iNN_atomic_*` families,
// and — for `memory_atomic_waitNN` alone — the *suffix*, since those two spell their address space
// on the left and their value width on the right. `notify` has no width in its name because its
// `ty` is `I32Type` unconditionally, so it is the one family whose type is a constant rather than a
// reading.
//
// # The order of the tests is load-bearing
//
// `.cmpxchg` is asked **before** `rmw`, because `i32.atomic.rmw.cmpxchg` and
// `i32.atomic.rmw8.u.cmpxchg` both begin `rmw` and the two families differ by one operand. Asked
// the other way round, all nine cmpxchg rows would type as rmw — `[addr; ty] --> [ty]` instead of
// `[addr; ty; ty] --> [ty]` — which is a *reject*-direction error for a well-formed module and an
// accept-direction one for a module that pushes two values and should be refused. `naturalWidth`
// next door records the mirror image of this hazard for its own trim order.
func atomicForm(name string) (atomicFamily, binary.ValType, bool) {
	// The table spells mnemonics `i32_atomic_load`; the spec and this package's other readers use
	// `i32.atomic.load`. Normalized here so the parsing below reads like the spec, and so this
	// function answers the same for either spelling — the property `atomicAccess` lacked until this
	// slice, and whose absence there was one edit from mattering (see its doc section).
	name = strings.ReplaceAll(name, "_", ".")

	if name == "atomic.fence" {
		return atomicFence, binary.ValType{}, true
	}

	prefix, rest, found := strings.Cut(name, ".")
	if !found {
		return atomicNone, binary.ValType{}, false
	}
	// Every remaining row's second component is `atomic`, and requiring it is what keeps this
	// function from answering for a non-atomic name that happens to end in `load`.
	op, found := strings.CutPrefix(rest, "atomic.")
	if !found {
		return atomicNone, binary.ValType{}, false
	}

	if prefix == "memory" {
		switch {
		case op == "notify":
			return atomicNotify, binary.I32, true
		case strings.HasPrefix(op, "wait"):
			ty, ok := numType("i" + strings.TrimPrefix(op, "wait"))
			return atomicWait, ty, ok
		}
		return atomicNone, binary.ValType{}, false
	}

	ty, ok := numType(prefix)
	if !ok {
		return atomicNone, binary.ValType{}, false
	}
	switch {
	case strings.HasSuffix(op, ".cmpxchg"):
		return atomicCmpXchg, ty, true
	case strings.HasPrefix(op, "load"):
		return atomicLoad, ty, true
	case strings.HasPrefix(op, "store"):
		return atomicStore, ty, true
	case strings.HasPrefix(op, "rmw"):
		return atomicRmw, ty, true
	}
	return atomicNone, binary.ValType{}, false
}

// errNoAtomicSignature is the decline, mirroring errNoVecSignature: it names the mnemonic as well as
// the two bytes, because the bucket this lands in is the work plan for whoever picks the region up
// and `prefixed opcode 0xfe 0x4a` is a lookup task where `i32_atomic_rmw16_u_cmpxchg` is an item.
func errNoAtomicSignature(in binary.Instr) error {
	return fmt.Errorf("%w: %s (%#02x %#02x) is in no atomic family",
		ErrUnsupported, mnemonic(in), in.Prefix, in.Op)
}
