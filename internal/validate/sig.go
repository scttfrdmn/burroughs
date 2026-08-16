// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// sig is one instruction's type: what it consumes, in stack order, and what it leaves.
type sig struct {
	params  []binary.ValType
	results []binary.ValType
}

// signature returns the type of a non-structural instruction, derived from its mnemonic.
//
// # Derived, not transcribed, and that choice is a control rather than a convenience
//
// The obvious implementation is a hand-written row per opcode: 0xB0 is `i32.wrap_i64`, so
// `(i64) -> i32`, and so on for about 180 rows. **Every error in such a table is
// accept-direction and invisible on the board** — a row that says `i32.add` takes `(i32, i64)`
// rejects valid modules (which the suite's pass column does see), but a row that says
// `i64.eqz` returns `i64` instead of `i32` makes the validator agree with modules the spec
// refuses, and no `assert_invalid` vector can catch it because the vector's expected string is
// satisfied by *any* refusal, including one for the wrong reason. Contract §9's G-3 names this
// class.
//
// So the signature comes out of the name. `OpMnemonic` is the authority's own table, and it is
// stronger than a transcription: `optable.go` is *generated* from `decode.ml` by decision 0007's
// mechanism B, carries a `refLine` per row, and `TestCommittedTableMatchesTheReference` re-runs
// the extraction and compares byte for byte, so drift is a build failure. `internal/interp`'s
// `memops` established the precedent (0014 promoted the mnemonic from "a label" to a fact,
// exactly so a consumer's hand-written table could be checked against it). Here the mnemonic is
// not a cross-check on a table; it *is* the table, which removes the class instead of policing
// it.
//
// **That sentence previously cited `TestOpTableAgreesWithReference`, which never existed, and
// called the table "transcribed" — two errors pointing the same way, both flattering.** The
// citation resolved to nothing and the mechanism claim understated a generator as hand work, so
// the paragraph arguing *derive, do not transcribe* rested on an invented control and a wrong
// description of the very artifact it derives from. Caught by
// `TestEveryCitedTestNameResolves` — and only on the cross-architecture run, because
// `make check` had already failed at fmt-check and never reached it: *a gate that stops early
// tells you less than the gate you skipped.*
//
// What remains hand-written is the *operator classification* — that `add` is binary and `clz`
// unary — because the name does not carry arity. That set is small, closed, and its errors are
// reject-direction: an operator missing from every class falls through to ErrUnsupported and
// lands in a named bucket, which is a visible refusal rather than a silent accept.
//
// The error return is three-valued and each value is a different claim: nil means the signature
// is known, ErrUnsupported means slice 1 has no rule for this opcode (a decline, into a named
// bucket), and ErrUnknownMemory means the rule *is* known and the module fails it. Collapsing
// the last two into one "cannot derive" bool would report `i32.load` in a module with no memory
// as out of scope, when the reference has a verdict for it and so does this package.
func signature(m *binary.Module, in binary.Instr) (sig, error) {
	name, ok := opMnemonic(in)
	if !ok {
		return sig{}, errNoSignature(in)
	}

	// The mnemonics are `i32_add` style in the table (the reference's own spelling); the suite
	// and the spec write `i32.add`. Normalized here so the parsing below reads like the spec.
	name = strings.ReplaceAll(name, "_", ".")

	prefix, rest, found := strings.Cut(name, ".")
	if !found {
		return sig{}, errNoSignature(in)
	}

	// `memory.size` and `memory.grow` are typed by the memory they name, not by a type prefix, so
	// they would fall through `numType` below into a decline. They are slice 5's, not because they
	// are encoded in its region — they are plain `0x3F`/`0x40` — but because they read the memory
	// index space, which is what that slice is; see bulk.go's header on the difference.
	if prefix == "memory" && (rest == "size" || rest == "grow") {
		return memoryIndexOp(m, in, rest == "grow")
	}

	t, ok := numType(prefix)
	if !ok {
		return sig{}, errNoSignature(in)
	}

	switch {
	case rest == "const":
		return sig{results: []binary.ValType{t}}, nil

	case rest == "eqz":
		// The one unary operator whose result is not its operand's type.
		return sig{params: []binary.ValType{t}, results: []binary.ValType{binary.I32}}, nil

	case unaryOps[rest]:
		return sig{params: []binary.ValType{t}, results: []binary.ValType{t}}, nil

	case binaryOps[rest]:
		return sig{params: []binary.ValType{t, t}, results: []binary.ValType{t}}, nil

	case compareOps[rest]:
		return sig{params: []binary.ValType{t, t}, results: []binary.ValType{binary.I32}}, nil

	// The two memarg arms, and they route through `checkMemop` rather than `addrType` directly:
	// resolving the address type is only half of the reference's shared preamble, the other half
	// being the alignment rule, and two arms remembering to call the second half separately is the
	// shape that had it called from nowhere.
	case strings.HasPrefix(rest, "load"):
		addr, err := checkMemop(m, in, name)
		if err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{addr}, results: []binary.ValType{t}}, nil

	case strings.HasPrefix(rest, "store"):
		addr, err := checkMemop(m, in, name)
		if err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{addr, t}}, nil
	}

	// The conversions: `<op>.<src>` optionally suffixed `_s`/`_u`, where src is a numeric type.
	// `i32.wrap.i64`, `f64.convert.i32.u`, `i32.reinterpret.f32`, `i32.trunc.sat.f32.s` all
	// resolve here, and their operand type is read off the name rather than enumerated.
	if src, ok := conversionSource(rest); ok {
		return sig{params: []binary.ValType{src}, results: []binary.ValType{t}}, nil
	}

	return sig{}, errNoSignature(in)
}

// opMnemonic is the authority's name for an instruction, from whichever of its tables holds the
// row — the plain one for a single-byte opcode, the prefixed one otherwise.
//
// **`signature` asked only `binary.OpMnemonic` until slice 5, which made its own doc comment
// false about eight opcodes.** That comment lists `i32.trunc.sat.f32.s` among the conversions
// whose operand type "is read off the name rather than enumerated", and the parsing below does
// handle it — but `trunc_sat` is `0xfc 0x00`-`0x07`, and the plain table has no row for a
// sub-opcode, so the name lookup failed before the parsing was reached and all eight declined.
// A claim about a code path nothing could execute: *the defect stated as the rule*, in the file
// whose header argues hardest for deriving over transcribing.
//
// It is separate from `mnemonic()`, which renders an instruction for a *human* and falls back to
// hex. This one must be able to fail, because a missing row means no signature can be derived
// and a hex string would parse as no type prefix and decline for the wrong reason. Two callers,
// two contracts, and the difference is that one of them is evidence and the other is a lookup.
func opMnemonic(in binary.Instr) (string, bool) {
	if in.Prefix != 0 {
		name, _, ok := binary.PrefixedOp(in.Prefix, in.Op)
		return name, ok && name != ""
	}
	return binary.OpMnemonic(in.Op)
}

// errNoSignature is the decline, and it names the opcode *and its mnemonic* rather than only the
// byte: the bucket this lands in is read by whoever picks up the next slice, and `opcode 0xd0` is
// a lookup task where `ref.null (0xd0)` is a work item.
func errNoSignature(in binary.Instr) error {
	return fmt.Errorf("%w: %s (%#02x)", ErrUnsupported, mnemonic(in), in.Op)
}

// numType maps a mnemonic's type prefix to its ValType.
func numType(s string) (binary.ValType, bool) {
	switch s {
	case "i32":
		return binary.I32, true
	case "i64":
		return binary.I64, true
	case "f32":
		return binary.F32, true
	case "f64":
		return binary.F64, true
	}
	return binary.ValType{}, false
}

// conversionSource extracts the source type from a conversion mnemonic's tail.
//
// Scans for the *last* type-shaped component rather than assuming a position, because the
// operator names have between one and three components before it (`wrap`, `trunc.sat`,
// `convert`) and a fixed index would be a claim about which family is being read.
func conversionSource(rest string) (binary.ValType, bool) {
	parts := strings.Split(rest, ".")
	if len(parts) < 2 {
		return binary.ValType{}, false
	}
	// A trailing signedness marker is not the type.
	if last := parts[len(parts)-1]; last == "s" || last == "u" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return binary.ValType{}, false
	}
	return numType(parts[len(parts)-1])
}

// addrTypeAt is a memory access's address type, and it is a *module* fact rather than a constant.
//
// i32 for a 32-bit memory, i64 for a 64-bit one (memory64). Read from the memory's own limits
// because that is where the decoder puts it (`Limits.Addr64`) — hardcoding i32 would reject
// every valid memory64 module while the gate is on, and hardcoding i64 the reverse.
//
// A module with no memory at all is `unknown memory 0`, returned as that verdict rather than as
// a decline. **This paragraph said the opposite until the probe ran** — that slice 1 should refuse
// with ErrUnsupported "instead of guessing which error to report", the index-space string being
// someone else's. There was nothing to guess: the reference resolves the memory through the same
// `lookup` as every other index space, so the error is already decided, and deferring it would
// have parked ten vectors in a decline bucket labelled as out of scope while the rule sat two
// lines away.
//
// # It takes the index, and it did not until #310
//
// This function read memory **0**, hardcoded, for its whole life before #310 — the imported memory
// if there was one, else `Memories[0]`. The reference's callers do not: `valid.ml:654` and its
// fifteen siblings resolve `memory c x` from the instruction's own index and use *that* for the
// operand type. So the old form diverged from the reference on any module with more than one memory,
// and it was correct only because multi-memory is gated and a validated memarg's index is therefore
// always 0 (`internal/binary/instr.go`'s bit-6 decline). Correct by coincidence of scope, which
// stops the day the gate flips rather than on a date anyone would notice.
//
// The index space is imports-then-definitions, `ImportedMems()` being the offset the defined
// memories start at — the same arrangement `internal/interp` resolves against, and the lesson that
// accessor's comment records is that reading `Memories[idx]` directly gets the wrong memory for
// every module that imports one.
func addrTypeAt(m *binary.Module, idx uint32) (binary.ValType, error) {
	imported := m.ImportedMems()
	if int(idx) < imported {
		n := 0
		for i := range m.Imports {
			if m.Imports[i].Kind != binary.ExternMemory {
				continue
			}
			if n == int(idx) {
				return addrTypeOf(m.Imports[i].Memory), nil
			}
			n++
		}
	}
	if defined := int(idx) - imported; defined >= 0 && defined < len(m.Memories) {
		return addrTypeOf(m.Memories[defined]), nil
	}
	// The index does not resolve. The reference reaches this through the same `lookup` the other
	// index spaces use (`memory c x`), so the verdict is `unknown memory N` and not a decline — an
	// access to a memory that is not there is a rule slice 1 can and does decide.
	//
	// The parenthetical keeps its `no memory` wording for the empty case rather than reading
	// `declares 0`, because that string is what 0003's message match has been comparing against and
	// the sentinel it wraps is unchanged.
	if total := imported + len(m.Memories); total > 0 {
		return binary.ValType{}, fmt.Errorf("%w %d (module declares %d)", ErrUnknownMemory, idx, total)
	}
	return binary.ValType{}, fmt.Errorf("%w %d (module declares no memory)", ErrUnknownMemory, idx)
}

func addrTypeOf(mem binary.Memory) binary.ValType {
	if mem.Limits.Addr64 {
		return binary.I64
	}
	return binary.I32
}

// The operator classes. Sets rather than a switch so `TestEveryNumericOpcodeHasASignature` can
// iterate them, and so an operator appearing in two classes is a compile-time-visible
// duplication rather than an unreachable `case`.
var (
	unaryOps = map[string]bool{
		// Integer
		"clz": true, "ctz": true, "popcnt": true,
		"extend8.s": true, "extend16.s": true, "extend32.s": true,
		// Float
		"abs": true, "neg": true, "sqrt": true, "ceil": true, "floor": true,
		"trunc": true, "nearest": true,
	}

	binaryOps = map[string]bool{
		"add": true, "sub": true, "mul": true,
		"div.s": true, "div.u": true, "rem.s": true, "rem.u": true,
		"and": true, "or": true, "xor": true,
		"shl": true, "shr.s": true, "shr.u": true, "rotl": true, "rotr": true,
		// Float
		"div": true, "min": true, "max": true, "copysign": true,
	}

	compareOps = map[string]bool{
		"eq": true, "ne": true,
		"lt.s": true, "lt.u": true, "gt.s": true, "gt.u": true,
		"le.s": true, "le.u": true, "ge.s": true, "ge.u": true,
		// Float
		"lt": true, "gt": true, "le": true, "ge": true,
	}
)
