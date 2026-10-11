// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "fmt"

// The 🧵 shared-everything-threads canonical built-ins: refused by name, as unsupported, at decode
// (#957). Nothing here executes or models any of them.
//
// # Why the refusal is at decode
//
// Recognising these the way the async family is recognised — decoded into the graph so bind refuses
// them — requires reading each form's operands so the canon section advances exactly. From Binary.md at
// CANON_PIN that is six operand shapes over the eleven productions, plus one sub-production:
//
//	none                 0x26, 0x28
//	a vestigial 0x00     0x29, 0x2a, 0x2b, 0x2c, 0x2d
//	ft tbl               0x27
//	sh? ft               0x40
//	sh? ft tbl           0x41
//	sh?                  0x42
//	sh? ::= 0x00 => (absent) | 0x01 => shared
//
// No ADR authorises that work, and an operand reader that advances by the wrong amount corrupts the rest
// of the section. So the refusal stays where the discriminant is read.
//
// # Why this sentinel
//
// [ErrUnsupportedForm] already existed, and all three public entry points already map it to the public
// ErrUnsupported (exit 5). ErrAsyncNotImplemented does not apply: its text is "gate:async is on but the
// async tier's execution is not yet implemented", and this is neither that gate nor that tier.
//
// # Why the message says unsupported rather than gated
//
// No gate admits this family. gate:threads is the core-wasm threads proposal (contract §§2–5) and is a
// different proposal. If an ADR introduces a gate for 🧵, this message is where it would be named.
//
// # The name map and its control
//
// Naming thread.index rather than 0x26 needs a map, because the engine cannot read
// testdata/canon-builtins.tsv at run time. TestTheThreadBuiltinNamesAgreeWithThePinnedTable holds the
// map against the decoder's case arm in both directions and against the pinned table's production
// names, the arrangement ADR 0092 recorded for isBuiltAsyncBuiltin after grave #850. The set is the
// decoder's own case arm; this map only spells it.
//
// Two facts about the map's membership and spelling:
//
//   - thread.yield (0x0c) is not a member. The pinned table marks it 🔀 async, so it decodes as a
//     CanonAsyncBuiltin and is one of the nine unlowered built-ins gateAsync refuses (#952).
//   - thread.available-parallelism is spelled with a hyphen, following the pinned table. wasm-tools
//     1.258.0's text format spells it thread.available_parallelism, with an underscore. The map follows
//     the pinned table; which spelling Burroughs follows is the shared-everything-threads ADR's call.
var threadBuiltinNames = map[byte]string{
	// 🧵 phase 1 — Binary.md's `canon` production at CANON_PIN.
	0x26: "thread.index",
	0x27: "thread.new-indirect",
	0x28: "thread.resume-later",
	0x29: "thread.suspend",
	0x2a: "thread.suspend-then-resume",
	0x2b: "thread.yield-then-resume",
	0x2c: "thread.suspend-then-promote",
	0x2d: "thread.yield-then-promote",
	// 🧵② phase 2 — the three forms `definitions.py` does not define at this pin, which is why the
	// table is generated from the binary grammar and not from the reference implementation.
	0x40: "thread.spawn-ref",
	0x41: "thread.spawn-indirect",
	0x42: "thread.available-parallelism",
}

// threadBuiltinError carries ErrUnsupportedForm's classification without its sentence.
//
// A plain fmt.Errorf("%w: …", ErrUnsupportedForm, …) prints:
//
//	burroughs: feature not implemented in this phase: component: canon 0: component: unsupported
//	instantiation form: canon thread.index (0x26) is a shared-everything-threads built-in, …
//
// That is two component: prefixes, and it states that the component has an unsupported instantiation
// form. A canon built-in is not an instantiation form. The sentinel is still the one to classify by,
// since it is what all three public entry points map to the public ErrUnsupported (exit 5), so the
// classification is inherited through Unwrap and the sentence is this error's own.
type threadBuiltinError struct{ msg string }

func (e *threadBuiltinError) Error() string { return e.msg }

// Unwrap is what makes `errors.Is(err, ErrUnsupportedForm)` hold, including through the decoder's own
// `component: canon %d: %w` wrapper.
func (e *threadBuiltinError) Unwrap() error { return ErrUnsupportedForm }

// threadBuiltinRefusal is the refusal for one 🧵 discriminant: the production, the opcode, the proposal,
// and that nothing admits it.
//
// An unnamed discriminant cannot reach here — the decoder's case arm and `threadBuiltinNames` are held
// equal by a test in both directions — but the fallback spells the opcode rather than printing an empty
// name, because a refusal that names nothing is the thing this function exists to stop.
func threadBuiltinRefusal(op byte) error {
	name, ok := threadBuiltinNames[op]
	if !ok {
		return &threadBuiltinError{msg: fmt.Sprintf("canon built-in %#x is a shared-everything-threads "+
			"built-in with no name in this engine's table, which this engine does not support — the "+
			"component is well-formed and no gate admits this proposal", op)}
	}
	return &threadBuiltinError{msg: fmt.Sprintf("canon %s (%#x) is a shared-everything-threads built-in, "+
		"which this engine does not support — the component is well-formed and no gate admits this "+
		"proposal", name, op)}
}
