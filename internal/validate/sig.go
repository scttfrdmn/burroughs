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
// So the signature comes out of the name. `OpMnemonic` is the authority's own table —
// transcribed from `decode.ml` with a `refLine` per row and cross-checked by
// `TestOpTableAgreesWithReference` — and `internal/interp`'s `memops` already established the
// precedent (0014 promoted the mnemonic from "a label" to a fact, exactly so a consumer's
// hand-written table could be checked against it). Here the mnemonic is not a cross-check on a
// table; it *is* the table, which removes the class instead of policing it.
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
	name, ok := binary.OpMnemonic(in.Op)
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

	case strings.HasPrefix(rest, "load"):
		addr, err := addrType(m)
		if err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{addr}, results: []binary.ValType{t}}, nil

	case strings.HasPrefix(rest, "store"):
		addr, err := addrType(m)
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

// addrType is a memory access's address type, and it is a *module* fact rather than a constant.
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
func addrType(m *binary.Module) (binary.ValType, error) {
	if m.ImportedMems() > 0 {
		for i := range m.Imports {
			if m.Imports[i].Kind == binary.ExternMemory {
				return addrTypeOf(m.Imports[i].Memory), nil
			}
		}
	}
	if len(m.Memories) > 0 {
		return addrTypeOf(m.Memories[0]), nil
	}
	// Memory index 0 does not resolve. The reference reaches this through the same `lookup` the
	// other index spaces use (`memory c x`), so the verdict is `unknown memory 0` and not a
	// decline — an access to a memory that is not there is a rule slice 1 can and does decide.
	return binary.ValType{}, fmt.Errorf("%w 0 (module declares no memory)", ErrUnknownMemory)
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
