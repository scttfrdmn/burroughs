package component

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// # A nested component is gated, and the class is derived rather than listed (#952)
//
// `gateAsync` used to be called at the two public entry points, ahead of their `walkComponent` call. That
// gated the outermost component and nothing else: the recursion that walks a nested component called
// `walkComponent` directly, so a built-in the gate exists to refuse **by name** instead reached core-module
// instantiation as an unsatisfied core import — `unknown import: "" "bpinc"` — and did so **identically with
// the gate on and off**, which is the tell that the gate had no say at all.
//
// The two fixtures below hold the *class* fixed and vary *nesting*, which is what makes the pair a
// discriminator rather than two examples:
//
//	starting-wat-parent/composed.wasm  nested     backpressure.inc (0x24)
//	p3async-cancel.wasm                top-level  future.cancel-read (0x18) + future.drop-writable (0x1b)
//
// Both carry a built-in from the no-lowering set; only the first was misreported. The second is the control
// for the *repair*: it refused correctly before this fix and must still refuse the same way after it, or the
// move changed the top-level path while claiming only to add the nested one.
//
// **The class is not a list in this comment.** `TestEveryUnloweredBuiltinIsRefusedByNameNotAsAnUnknownImport`
// below derives it from the decoder's own `case` list and from `isBuiltAsyncBuiltin`, because a hand-kept
// list of opcodes is the defect grave #850 already paid for one level down.

// nestedGateFixture is one committed artefact and what it carries.
type nestedGateFixture struct {
	path   string
	nested bool
	// op is the no-lowering built-in the gate-on refusal must name, as it appears in the message.
	op string
}

// TestTheRefusedFixtureIsTheOneWasmtimeMeasured gives `starting.reading` its Go consumer, which it did not
// have when #884 landed — at that point this engine could not run the component at all and the only
// available assertion would have been the defect itself (#953's report says so).
//
// **What it proves and what it does not.** It does NOT reproduce status 3: Burroughs refuses this component
// by name, correctly, because `backpressure.inc` has no lowering. What it holds is that the artefact this
// package refuses is the same artefact wasmtime measured — the reading's provenance names the child WAT the
// composed fixture is plugged from, and the reading's own status is the one #884 recorded. So a regenerated
// reading taken on some other fixture, or a fixture rebuilt from a different child, breaks the link instead
// of leaving two files that merely sit next to each other.
//
// The honest ceiling is stated rather than implied: the pairing is provenance, not parity. Parity arrives
// when the built-in has a lowering, which is a separate decision (#952 fixes the refusal, not backpressure).
func TestTheRefusedFixtureIsTheOneWasmtimeMeasured(t *testing.T) {
	const path = "testdata/asynclift/starting.reading"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading is missing, so the refusal has no measured subject: %v", err)
	}
	text := string(b)

	// The two facts that make this the status-3 reading. Read out of the file rather than retyped, so a
	// reading that moves upstream fails here instead of leaving a stale expectation asserted as current.
	for _, want := range []string{"LOWERSTATE  0 (STARTING)", "STATUS  3 (CANCELLED_BEFORE_STARTED)"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not record %q, so it is not the status-3 reading this test is about", path, want)
		}
	}
	// The provenance line that ties the reading to the artefact the gate test refuses. If the composed
	// fixture is ever rebuilt from a different child, this is what notices.
	if !strings.Contains(text, "starting-wat-child/child.wat") {
		t.Errorf("%s does not name starting-wat-child/child.wat in its provenance, so the reading and the "+
			"refused composed.wasm are no longer known to share a subject", path)
	}
	if _, err := os.Stat("testdata/asynclift/starting-wat-child/child.wat"); err != nil {
		t.Errorf("the reading names a child source that is not in the tree: %v", err)
	}
}

// TestANestedComponentIsGatedByName is the #952 witness: #884's fixture through both gate settings, with
// the top-level fixture from the same class as the control.
//
// # Two neuterings, because one of them does not localise and the first draft of this comment said it did
//
// Both were run by hand and are recorded here because no automated arm can hold either.
//
//  1. **The pre-fix arrangement** — `gateAsync` at the two entry points, absent from `walkComponent`.
//     The `composed.wasm` rows fail with `unknown import: "" "bpinc"` through both gate settings; the
//     `p3async-cancel` rows **pass**. That is the measurement that localises the defect to nesting, and it
//     is the behaviour of `main` before this slice.
//
//  2. **Deleting the one call in `walkComponent`** — which is the only call there now is. **All four rows
//     fail**, the control included: `p3async-cancel` surfaces its own unsatisfied import,
//     `unknown import: "[export]$root" "[future-cancel-read-0]mk"`. So this neutering does *not* isolate
//     the nested path, and the first draft of this comment claimed it did. That is the flip side of making
//     the gate structural: with exactly one call site, no single deletion can leave the top-level path
//     standing. The localisation comes from (1); what (2) shows is that the single site is load-bearing for
//     every depth, which is the property the move was for.
func TestANestedComponentIsGatedByName(t *testing.T) {
	fixtures := []nestedGateFixture{
		// #884's artefact: the child's `canon backpressure.inc` is reachable only through the nesting.
		// Its committed wasmtime reading is held by TestTheRefusedFixtureIsTheOneWasmtimeMeasured.
		{path: "testdata/asynclift/starting-wat-parent/composed.wasm", nested: true, op: "0x24"},
		// The control, top-level, same class of built-in.
		{path: "testdata/p3async-cancel.wasm", nested: false, op: "0x18"},
	}

	for _, fx := range fixtures {
		wasm, err := os.ReadFile(fx.path)
		if err != nil {
			t.Fatalf("%s: the committed fixture is missing, so this witness has no subject: %v", fx.path, err)
		}

		t.Run(fx.path+"/gate_on", func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			_, err := Instantiate(wasm)
			if !errors.Is(err, ErrAsyncNotImplemented) {
				t.Fatalf("gate on: err = %v; want ErrAsyncNotImplemented. A built-in with no lowering must "+
					"refuse by name, not arrive as an unsatisfied core import", err)
			}
			// The sentinel alone would pass on a refusal about some *other* async surface, so the arm
			// requires the opcode in the message: the refusal has to be about the built-in.
			if !strings.Contains(err.Error(), fx.op) {
				t.Errorf("gate on: refusal does not name %s, so it is not about the built-in: %v", fx.op, err)
			}
			if strings.Contains(err.Error(), "unknown import") {
				t.Errorf("gate on: refused as an unsatisfied core import, which is #952 returning: %v", err)
			}
		})

		t.Run(fx.path+"/gate_off", func(t *testing.T) {
			t.Setenv(asyncGateEnv, "0")
			_, err := Instantiate(wasm)
			if !errors.Is(err, ErrAsyncGated) {
				t.Fatalf("gate off: err = %v; want ErrAsyncGated. With the gate off a component using the "+
					"async ABI is a gate decline, whatever depth it sits at", err)
			}
			if strings.Contains(err.Error(), "unknown import") {
				t.Errorf("gate off: refused as an unsatisfied core import, which is #952 returning: %v", err)
			}
			// **The gate-off message names a surface, not necessarily the built-in**, and that is
			// asserted as it is rather than as one might wish. `asyncSurface`'s priority order reports
			// the `async` canonopt first, so this fixture's gate-off refusal says "an async canon lift";
			// the built-in is named by the gate-ON arm, where `unbuiltAsyncSurface` is what answers.
			if !strings.Contains(err.Error(), "async component-model ABI") {
				t.Errorf("gate off: refusal does not name the ABI it is declining: %v", err)
			}
		})
	}
}

// TestEveryUnloweredBuiltinIsRefusedByNameNotAsAnUnknownImport derives the no-lowering class from the
// engine's own two sources and asserts the gate reports every member of it.
//
// The decoder's `case` list (`instantiate.go`) says which opcodes become a `CanonAsyncBuiltin`;
// `isBuiltAsyncBuiltin` says which of those this engine executes. The difference is the class #952 is
// about, and it is computed here rather than written down, so landing a lowering shrinks the class without
// anyone editing this test — and so that a tenth member cannot appear unreported.
//
// What this asserts per member is that `unbuiltAsyncSurface` **names it**, which is the predicate the gate
// consults. That it then fires at every depth is the other test's job; splitting them keeps a failure
// legible: this one moves when the class moves, that one moves when the walk changes.
func TestEveryUnloweredBuiltinIsRefusedByNameNotAsAnUnknownImport(t *testing.T) {
	var unlowered []byte
	for op := range 0x100 {
		b := byte(op)
		if !decodesAsAsyncBuiltin(t, b) || isBuiltAsyncBuiltin(b) {
			continue
		}
		unlowered = append(unlowered, b)
	}
	// A derivation that found nothing would pass every assertion below and check nothing — the vacuity
	// this family keeps paying for. The count is not pinned (a landing lowering shrinks it); its being
	// non-empty is, because an empty class means the derivation broke, not that the engine is complete.
	if len(unlowered) == 0 {
		t.Fatal("derived an EMPTY no-lowering class, so this test asserts nothing: either every decoded " +
			"built-in now has a lowering (in which case #952's subject is gone and this test should go " +
			"with it) or decodesAsAsyncBuiltin stopped recognising the decoder's case list")
	}
	t.Logf("no-lowering class, derived: %d opcode(s) %#x", len(unlowered), unlowered)

	for _, op := range unlowered {
		c := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: op}}}
		what, ok := unbuiltAsyncSurface(c)
		if !ok {
			t.Errorf("opcode %#x decodes as a built-in and has no lowering, but the gate does not report "+
				"it — so a component using it reaches linking and fails as an unknown import", op)
			continue
		}
		if !strings.Contains(what, "built-in") {
			t.Errorf("opcode %#x is reported as %q, which does not name it as a built-in", op, what)
		}
	}
}

// decodesAsAsyncBuiltin reports whether the decoder turns this canon discriminant into a CanonAsyncBuiltin,
// asked of the decoder itself rather than of a copy of its case list. The operand bytes a given form needs
// vary, so a truncated body is expected: what distinguishes "this discriminant is an async built-in" from
// "this discriminant is not modeled" is **which** error comes back — the default arm's "is not modeled"
// message versus a read running off the end inside canonAsyncBuiltin.
func decodesAsAsyncBuiltin(t *testing.T, op byte) bool {
	t.Helper()
	r := &reader{b: []byte{op}}
	cn, err := r.canon()
	if err == nil {
		return cn.Kind == CanonAsyncBuiltin
	}
	if strings.Contains(err.Error(), "is not modeled") {
		return false
	}
	// Ran out of operand bytes: the discriminant was accepted and dispatched. Re-decode with a generous
	// run of zero operands to confirm it lands on CanonAsyncBuiltin rather than on lift/lower/resource,
	// whose discriminants are 0x00-0x04 and would also read past one byte.
	switch op {
	case 0x00, 0x01, 0x02, 0x03, 0x04:
		return false
	}
	body := make([]byte, 64)
	body[0] = op
	r2 := &reader{b: body}
	cn2, err2 := r2.canon()
	if err2 != nil {
		// Still short or otherwise refused. The discriminant is in the async case list (it did not hit
		// the default arm above), so report it as such rather than silently dropping a class member.
		return true
	}
	return cn2.Kind == CanonAsyncBuiltin
}
