// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "fmt"

// The 🧵 shared-everything-threads canonical built-ins: refused by NAME, as unsupported, at decode.
//
// # What this replaces, and why it was a defect (#957)
//
// These eleven discriminants used to fall through `canon`'s `default` arm, which produced
//
//	component: canon 0: canon built-in 0x26 is not modeled
//	           (this slice: lift, lower, resource.new/drop/rep, the async family)
//
// with no public sentinel, so the CLI classified it **exit 1** — the code a corrupt file gets. Four
// things were wrong with that and only the first is the exit code: the message's shape reads as a parse
// failure, it names an opcode rather than `thread.index`, it never names the proposal, and the
// parenthetical *"this slice"* is a note about the async tier's increments that has nothing to do with
// 🧵. A well-formed component that `wasm-tools` builds in 37 bytes was reported as malformed, which is
// the class #952 fixed one proposal over.
//
// # Three decisions, each narrower than it looks
//
// **The refusal stays at decode rather than the decoder learning to read these operands.** Recognising
// them the way the async family is recognised — decoded into the graph so bind refuses them — means
// reading each form's operands so the section advances exactly. Counted from Binary.md at CANON_PIN,
// that is **six distinct operand shapes over the eleven productions**, plus one sub-production:
//
//	none                 0x26, 0x28
//	a vestigial 0x00     0x29, 0x2a, 0x2b, 0x2c, 0x2d
//	ft tbl               0x27
//	sh? ft               0x40
//	sh? ft tbl           0x41
//	sh?                  0x42
//	sh? ::= 0x00 => (absent) | 0x01 => shared
//
// That is model work no ADR has authorised, and a wrong operand reader turns a clean refusal into a
// mis-advanced section: a worse failure than the one being repaired. Nothing here executes or models
// anything; the change is a sentinel and a string.
//
// **The sentinel is [ErrUnsupportedForm], which already existed.** No new sentinel, because all three
// public entry points already wrap that one as the public `ErrUnsupported` (exit 5), which is the
// classification `b8d371d` made uniform across the three doors. `ErrAsyncNotImplemented` was the other
// candidate and is wrong by its own text — *"gate:async is on but the async tier's execution is not yet
// implemented"* — since this is neither that gate nor that tier.
//
// **The message says unsupported, not gated, and that is a fact about today rather than a preference.**
// `gate:threads` is the *core-wasm* threads proposal (contract §§2–5) and does not cover this family; no
// gate admits these. A message naming a gate would send a reader looking for a switch that does not
// exist. If an ADR introduces one, this message is where it gets named.
//
// # The names are a table, so the table has a control
//
// Naming `thread.index` rather than `0x26` needs a map, because the engine cannot read
// `testdata/canon-builtins.tsv` at run time. A hand-kept list beside a case arm is correct exactly once
// — grave #850, whose repair ADR 0092 recorded — so `TestTheThreadBuiltinNamesAgreeWithThePinnedTable`
// checks this map in **both** directions: every discriminant the decoder refuses here has a name, and
// every name equals the pinned spec table's production for that opcode. The *set* is the decoder's own
// `case` arm; this map only spells it.
//
// Two notes a reader will otherwise re-derive:
//
//   - **`thread.yield` (0x0c) is NOT here.** The pinned table marks it 🔀 async, not 🧵, so it decodes
//     as a `CanonAsyncBuiltin` and is one of the nine unlowered built-ins `gateAsync` refuses (#952).
//     Its spelling makes it look like a member of this family and it is not.
//   - **`thread.available-parallelism` is spelled with a HYPHEN here**, following the pinned table.
//     `wasm-tools` 1.258.0's text format spells it `thread.available_parallelism`, with an underscore.
//     The discrepancy is recorded rather than resolved: which spelling Burroughs follows is the
//     shared-everything-threads ADR's call, and this map follows the spec-facts oracle in the meantime.
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

// threadBuiltinError carries ErrUnsupportedForm's *classification* without its sentence.
//
// **Measured reason for the type rather than a plain `fmt.Errorf("%w: …", ErrUnsupportedForm, …)`.**
// That spelling produced:
//
//	burroughs: feature not implemented in this phase: component: canon 0: component: unsupported
//	instantiation form: canon thread.index (0x26) is a shared-everything-threads built-in, …
//
// Two `component:` prefixes, and — worse than noise — the claim that the component has an unsupported
// *instantiation form*, which it does not: a canon built-in is not an instantiation form. The sentinel is
// still the right one to classify by, because it is what all three public entry points map to the public
// `ErrUnsupported` (exit 5). So the classification is inherited through Unwrap and the sentence is this
// error's own.
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
