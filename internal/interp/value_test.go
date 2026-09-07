// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestAnUnexpressibleReferenceArgumentIsRefusedAtTheBoundary is [decision 0079][0079]'s domain control,
// and the reason it walks `RefPayload` rather than listing cases is that the property under test is a
// *complement*.
//
// # The rule
//
// A non-null reference crosses inward **iff its payload rides in the `Value` itself** — `PayloadHost`,
// whose payload is `RefID`, and `PayloadI31`, whose payload is `I31`. That set is exactly what this
// package's own public constructors build (`NullRef`, `ExternRef`, `HostRef`, and `ParseValue`), and the
// expectation below is *computed from the kind* rather than tabulated, so a kind added to `RefPayload`
// without an arm in `Value.toRef` is a failure here as well as an `exhaustive` build break. A table of
// today's kinds would have gone on passing while saying nothing about tomorrow's.
//
// # Two parameter spellings, because one of them hides everything
//
// `externref` **and** `anyref`, and the externref column is the one that matters: `toRef` reads
// externalization off the *static* type and `typeOfRef` dispatches on `Externalized` first, so before
// 0079 every refused kind here was answered `extern`, matched, and **silently admitted** — no error at
// any point, the value reaching the guest or the embedder as a `(ref.extern 0)` it is not. A control run
// only at `anyref` would have seen the milder half (an `ErrEngineInvariant` several frames later) and
// reported the boundary fixed while the corpus's most-used reference type still admitted the fabrication.
//
// The injection battery measured that asymmetry rather than assuming it, and it is sharper than "milder":
// with the no-kind arm's refusal removed, the `externref` and host-result columns both failed and the
// **`anyref` column did not**. At that spelling a second refusal stands behind this one — `typeOfRef`'s
// default arm, which errors plainly and carries no sentinel, which is what this walk asserts for a no-kind
// row — so that column's verdict for `PayloadNone` is *stolen* by a check that is not the subject. It is
// recorded here because it is the same shape 0079 repairs in
// `TestAHostFunctionThatDoesNotHonourItsDeclaredTypeIsRefused`, arriving inside the control written to
// repair it: the row goes green either way, so only the column where nothing stands behind the refusal is
// evidence about the refusal. Every other kind failed in all three columns.
//
// # And a host result, because there are two call sites
//
// `pushHostResults` is a deliberate copy of `invokeIndex`'s loop — its own comment says so — and #677's
// scope named one of them. The third column runs the same walk *inward from a host function*, so the two
// copies cannot drift apart without a verdict. It is one module rather than nine because the host closure
// reads a captured variable: the row under test is the `Value`, not the module.
//
// # The register is asserted, and its absence is asserted too
//
// 0079 splits on whether a widening could lift the refusal ([#680][680] is that widening). A real
// reference constructor the engine declines to carry gets `ErrUnsupportedOp`, *this engine cannot*; a
// `Value` naming no constructor at all, or naming the domain's bound, is malformed and gets **no
// sentinel**, because `publicError` would otherwise tell an embedder a feature is missing when their
// argument is wrong. Both halves are checked, since a single `err != nil` row would pass on either.
//
// [0079]: ../../docs/decisions/0079-the-boundary-refuses-a-reference-argument-by-its-own-payload-kind-rather-than-by-the-parameters-spelling-and-the-register-splits-on-whether-a-widening-could-lift-it.md
// [680]: https://github.com/scttfrdmn/burroughs/issues/680
func TestAnUnexpressibleReferenceArgumentIsRefusedAtTheBoundary(t *testing.T) {
	anyRef, ok := binary.AbstractRefType(binary.HeapAny, true)
	if !ok {
		t.Fatal("no anyref valtype, so the second column below cannot be built and the walk would " +
			"run entirely against the externalizing arm — the one that hides every refusal")
	}
	in := instantiateGC(t, `(module
		(func (export "er") (param externref))
		(func (export "ar") (param anyref)))`)

	// One module, one captured Value: the host function hands back whatever the row put there, so
	// the inward-from-a-host-call column costs one instantiation rather than one per kind.
	var give Value
	hin := hostLink(t, `(module
		(import "h" "f" (func $f (result externref)))
		(func (export "call") (result i32) (drop (call $f)) (i32.const 99)))`,
		binary.Features{GC: true},
		hostImports(map[string]Extern{
			"f": HostExtern(ft(nil, []binary.ValType{binary.ExternRef}),
				func(_ *Caller, _ []Value) ([]Value, error) { return []Value{give}, nil }),
		}))

	cols := []struct {
		name string
		typ  binary.ValType
		call func(v Value) error
	}{
		{"externref parameter", binary.ExternRef, func(v Value) error {
			_, err := in.Invoke("er", v)
			return err
		}},
		{"anyref parameter", anyRef, func(v Value) error {
			_, err := in.Invoke("ar", v)
			return err
		}},
		{"host function result", binary.ExternRef, func(v Value) error {
			give = v
			_, err := hin.Invoke("call")
			return err
		}},
	}

	// The walk runs one past PayloadPastEnd on purpose: the bound itself and anything above it reach
	// two *different* arms of `toRef` — the named-bound arm and the not-a-member fallthrough that
	// `exhaustive` cannot see, because a uint8 above the last constant is not a case it knows about.
	var crossed, refusedWithSentinel, refusedPlain int
	for k := PayloadNone; k <= PayloadPastEnd+1; k++ {
		// Derived, not tabulated. `crosses` is the payload-rides-in-the-Value set; `sentinel` is
		// "a real constructor that does not cross", which is every member strictly between the
		// zero value and the domain's bound, minus the two that cross.
		crosses := k == PayloadHost || k == PayloadI31
		sentinel := !crosses && k > PayloadNone && k < PayloadPastEnd

		for _, col := range cols {
			t.Run(k.String()+" at "+col.name, func(t *testing.T) {
				v := Value{Type: col.typ, RefKind: k}
				if k == PayloadHost {
					v.RefID = 7
				}
				if k == PayloadI31 {
					v.I31 = 3
				}
				err := col.call(v)

				if crosses {
					if err != nil {
						t.Fatalf("a %s reference was refused: %v\n"+
							"Its payload rides in the Value itself, so the engine can "+
							"rebuild it exactly — refusing it would make the boundary "+
							"unable to accept a value its own constructors produce", k, err)
					}
					crossed++
					return
				}
				if err == nil {
					t.Fatalf("a non-null %s reference was accepted at %s.\n"+
						"`toRef` cannot rebuild its payload, so what crossed is a reference "+
						"the engine will classify as something it is not — and at an "+
						"externref spelling nothing downstream ever notices (decision 0079)",
						k, col.typ)
				}
				switch {
				case sentinel:
					if !errors.Is(err, ErrUnsupportedOp) {
						t.Errorf("got %v, want ErrUnsupportedOp.\n"+
							"%s is a real reference constructor whose payload this engine "+
							"declines to carry inward, which is the `this engine cannot` "+
							"register — the one #680's widening could lift, and the one "+
							"the sibling funcref refusal already carries", err, k)
					}
					refusedWithSentinel++
				default:
					if errors.Is(err, ErrUnsupportedOp) {
						t.Errorf("got %v, want no sentinel.\n"+
							"%s does not name a reference constructor, so the Value is "+
							"malformed rather than unsupported: ErrUnsupportedOp reaches "+
							"an embedder as ErrUnsupported through publicError and sends "+
							"them looking for a missing feature (decision 0079's split)",
							err, k)
					}
					refusedPlain++
				}
			})
		}
	}

	// Vacuity, three ways, because a fixture that refused *everything* would satisfy every
	// refusal row above and a fixture that accepted everything would satisfy none of them — and
	// the split's whole content is that the two refusal registers are both populated.
	if crossed == 0 || refusedWithSentinel == 0 || refusedPlain == 0 {
		t.Errorf("crossed=%d refused-with-sentinel=%d refused-plain=%d, want all three non-zero.\n"+
			"A zero in any of them means this walk is not discriminating anything: the "+
			"expectations are computed from the kind, so they agree with a boundary that "+
			"uniformly accepts or uniformly refuses", crossed, refusedWithSentinel, refusedPlain)
	}
}

// TestTheFuncrefRefusalKeysOnTheArgumentAndNotOnTheParameterSpelling is the pair of failures the
// pre-[0079][0079] guard had, and it is the only control that can see that guard come back.
//
// What stood at both call sites was `if p == binary.FuncRef && !args[i].Null`. `binary.FuncRef` is one
// value — `ValType{kind: 0x70, null: true}`, the **nullable abstract** spelling — so the predicate was a
// claim about how the callee spelled its parameter, while the thing being refused is a property of the
// caller's argument. That reads wrong in both directions at once:
//
//   - **under-refusal.** A `(ref func)` parameter is a different `ValType`, so a `PayloadFunc` argument
//     went to `toRef`, which resolved the bare index against the *callee's* instance. With index 0 that
//     was admitted: a caller who cannot name a function fabricated a reference to one.
//   - **over-refusal, with a false message.** An `externref` argument at a `funcref` parameter was
//     caught by the guard and told it *"is a non-null funcref"*. It is not one, and `matchRefType` two
//     lines further on had the correct answer — the guard preempted the check that could give it.
//
// Both rows therefore assert the *message*, not just the outcome, because both outcomes were already
// errors of some kind before 0079 and only the text distinguishes the guard from the repair. The
// non-nullable premise is asserted first: if `(ref func)` ever became equal to `binary.FuncRef`, the
// under-refusal row would pass for a reason that has nothing to do with the argument.
//
// [0079]: ../../docs/decisions/0079-the-boundary-refuses-a-reference-argument-by-its-own-payload-kind-rather-than-by-the-parameters-spelling-and-the-register-splits-on-whether-a-widening-could-lift-it.md
func TestTheFuncrefRefusalKeysOnTheArgumentAndNotOnTheParameterSpelling(t *testing.T) {
	in := instantiateGC(t, `(module
		(func (export "concrete") (param (ref func)))
		(func (export "abstract") (param funcref) (result i32) (ref.is_null (local.get 0))))`)

	refFunc := in.mod.Types[in.mod.Funcs[0].TypeIndex].Func.Params[0]
	if refFunc == binary.FuncRef {
		t.Fatalf("(ref func) decoded as %s, which equals binary.FuncRef.\n"+
			"The deleted guard's under-refusal was exactly that these are different ValTypes, so "+
			"with them equal the row below cannot be about the argument", refFunc)
	}

	// Under-refusal: the parameter is not the abstract spelling, and the argument is still refused.
	// Index 0 exists — it is this very export — so before 0079 this was silently admitted rather
	// than caught downstream by a bounds check.
	if _, err := in.Invoke("concrete", Value{Type: refFunc, RefKind: PayloadFunc, Bits: 0}); err == nil {
		t.Errorf("a fabricated funcref was accepted at a (ref func) parameter.\n" +
			"A bare index names no instance (interp.Value.RefID's scope statement), so what " +
			"crossed is a reference to whichever function the *callee* has at that index")
	} else if !errors.Is(err, ErrUnsupportedOp) {
		t.Errorf("got %v, want ErrUnsupportedOp — the same register the funcref refusal has "+
			"always had, which is what keeps its public class through publicError unchanged", err)
	} else if !strings.Contains(err.Error(), "is a non-null funcref") {
		t.Errorf("error %q does not say what was wrong with the argument.\n"+
			"The refusal is preserved byte for byte from the deleted guard precisely so an "+
			"embedder reads the same sentence at both parameter spellings", err)
	}

	// Over-refusal: an externref at a funcref parameter is a *type* mismatch, and saying anything
	// about funcrefs being non-null is a statement about a value that is not one.
	_, err := in.Invoke("abstract", ExternRef(7))
	if err == nil {
		t.Fatal("an externref was accepted at a funcref parameter, so this row is not about the " +
			"message any more — extern and func are disjoint hierarchies")
	}
	if strings.Contains(err.Error(), "is a non-null funcref") {
		t.Errorf("error %q calls an externref a non-null funcref.\n"+
			"That is the deleted guard: it fired on the *parameter's* spelling, so it caught an "+
			"argument of the wrong hierarchy and described it as the shape it was screening for", err)
	}
	if !strings.Contains(err.Error(), "funcref") || !strings.Contains(err.Error(), "extern") {
		t.Errorf("error %q names neither the declared type nor the one supplied.\n"+
			"`matchRefType` is what answers this row, and its whole advantage over the guard is "+
			"that it can say which two types failed to match", err)
	}
	if errors.Is(err, ErrUnsupportedOp) {
		t.Errorf("got %v, which carries ErrUnsupportedOp; want a plain type mismatch.\n"+
			"Nothing here is unsupported: the engine carries externrefs and funcrefs both, and "+
			"the caller supplied one where the other was declared", err)
	}

	// The floor, and it is an accept-direction row so the guest *uses* the reference: a null funcref
	// still crosses. Without it, a boundary that refused every funcref-typed argument would satisfy
	// both rows above.
	out, ferr := in.Invoke("abstract", NullRef(binary.FuncRef))
	if ferr != nil {
		t.Fatalf("a null funcref argument was refused: %v\n"+
			"0079 refuses payload kinds, and a null names none — RefKind is not read on a null "+
			"at all (grave #266's single heaptype-free null)", ferr)
	}
	if len(out) != 1 || out[0].Bits != 1 {
		t.Errorf("ref.is_null answered %v, want a single i32 1 — the null did not arrive as a null",
			out)
	}
}
