// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The accept-direction controls for rung 5 slice 3's two arms, and the reason they exist is a
// measurement rather than a habit: **every claim below was mutated and the board did not move**.
//
// Three mutations, run with the diff printed and read for behaviour:
//
//   - `execAnyConvertExtern` reverted to the total-identity draft it was first written as (return the
//     operand unchanged instead of refusing) — 62173 pass / 127 fail all-on, identical.
//   - `execExternConvertAny` made to set the bit on a null — identical, and see that arm's own
//     comment for the mechanism (all three readers of `Externalized` guard on `Null` first, so the
//     distinction is unobservable however the corpus grows).
//   - `fromRef`'s `r.IsHost` guard dropped, so a non-null externref with no identity reports
//     `(ref.extern 0)` — identical.
//
// Each of those is a deviation the suite scores green by construction, which is §9 G-3's accept
// direction and the one case the disciplines call control work *product* work: nothing else will ever
// find them. The corpus that would score two of the three is `extern.wast:39`, `:42` and `:56`, all
// three `unsupported` pending **#270**, so these controls are what stands in for a board delta until
// that widening lands — and when it does, these tests are the record of what the arms were supposed
// to do while no vector was asking.

// TestAnyConvertExternRefusesANonExternref is the arm's **partiality**, and it is the whole of what
// the authority contributes: `eval.ml:929-933` has four `ExternConvert` arms, and a non-null operand
// that is not an `Extern.ExternRef` matches none of them, so the reference falls through to its own
// crash case rather than treating the value as an identity.
//
// The first draft of this arm returned the operand unchanged, which is total where the reference is
// partial. Nothing on the board separates the two readings — that is the mutation above — so this
// test is the separation.
func TestAnyConvertExternRefusesANonExternref(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    ref
	}{
		{"i31", ref{IsI31: true, I31: 5}},
		{"aggregate", ref{Obj: &gcObj{}}},
		{"host reference", ref{IsHost: true, Addr: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &stack{}
			st.pushRef(tc.r)
			err := execAnyConvertExtern(st)
			if !errors.Is(err, ErrNotValidated) {
				t.Errorf("execAnyConvertExtern(%+v) = %v, want ErrNotValidated: eval.ml:929-933 has "+
					"no arm for a non-null non-externref operand, so returning it unchanged invents "+
					"an identity conversion the reference does not have — and no vector can say so",
					tc.r, err)
			}
		})
	}
}

// TestExternConvertAnyRefusesNesting is the other partiality, in the other direction: the reference
// would build `ExternRef (ExternRef r)` and a one-bit wrapper cannot express depth two (0027
// decision 3), so flattening it silently would be the engine answering a question it cannot
// represent.
func TestExternConvertAnyRefusesNesting(t *testing.T) {
	st := &stack{}
	st.pushRef(ref{Externalized: true, IsI31: true, I31: 5})
	if err := execExternConvertAny(st); !errors.Is(err, ErrNotValidated) {
		t.Errorf("execExternConvertAny of an already-externalized reference = %v, want "+
			"ErrNotValidated: a bit cannot nest, so the alternative is returning the operand "+
			"unchanged and calling depth two depth one", err)
	}
}

// TestExternConvertAnyLeavesANullCanonical pins the one null reference value — `value.ml:20`'s
// nullary `NullRef`, and `eval.ml:935-936`'s dedicated arm matched *before* the general one.
//
// **This is the control for a claim no vector can hold**, and the distinction between this and a
// merely-unscored claim is worth stating: the mutation that sets the bit on a null is invisible to
// the *engine* too, because `typeOfRef`, `refEq` and `fromRef` all guard on `Null` first. So the test
// asserts the representation directly rather than any behaviour derived from it — the reason being
// grave #266, where a second null the reference does not have was asserted by the harness. Setting
// this bit is where the **engine** would acquire one, and a later reader of `Externalized` that
// forgot its null guard would inherit the deviation silently.
//
// **This control and the round trip below are not independent, and the prediction that they were was
// wrong.** The mutation setting the bit on a null was expected to kill only this test, on the
// reasoning that `execAnyConvertExtern` would see `Externalized` and clear it again; run, it killed
// the round trip's null row too, because that arm matches `case r.Null` *before* `case
// r.Externalized` and so never reaches the clearing. Which means the bit, once set on a null,
// **persists** rather than round-tripping away — still invisible to the three readers, hence the
// unmoved board, but visible to a comparison of the value itself. Recorded rather than tidied away:
// the overlap is the finding, and a comment claiming two independent witnesses where there is one
// fact seen twice would be the same defect these tests exist to catch.
func TestExternConvertAnyLeavesANullCanonical(t *testing.T) {
	st := &stack{}
	st.pushRef(ref{Null: true})
	if err := execExternConvertAny(st); err != nil {
		t.Fatalf("execExternConvertAny(null) errored: %v", err)
	}
	got := st.popRef()
	if want := (ref{Null: true}); got != want {
		t.Errorf("execExternConvertAny(null) = %+v, want %+v — externalizing a null must leave the "+
			"single null reference value, not manufacture a null carrying a wrapper bit", got, want)
	}
}

// TestExternRoundTripPreservesThePayload is the composition `extern.wast:52-57` exists to score, run
// at unit level because four of those six lines are `unsupported` (#270) and so score nothing today.
//
// The pair is `any.convert_extern (extern.convert_any x)`, and the option's dividend is that it is an
// identity *by construction* — one bit set, the same bit cleared, the payload never read — rather
// than by two arms agreeing about how to rebuild a value. The host row is the one that would need
// `:56`.
func TestExternRoundTripPreservesThePayload(t *testing.T) {
	obj := &gcObj{}
	for _, tc := range []struct {
		name string
		r    ref
	}{
		{"null", ref{Null: true}},
		{"i31", ref{IsI31: true, I31: 42}},
		{"aggregate", ref{Obj: obj}},
		{"host reference", ref{IsHost: true, Addr: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &stack{}
			st.pushRef(tc.r)
			if err := execExternConvertAny(st); err != nil {
				t.Fatalf("externalize: %v", err)
			}
			if err := execAnyConvertExtern(st); err != nil {
				t.Fatalf("internalize: %v", err)
			}
			if got := st.popRef(); got != tc.r {
				t.Errorf("round trip of %+v = %+v — the pair must be an identity: the wrapper is one "+
					"bit and the payload is never read, so any difference is a field one arm wrote",
					tc.r, got)
			}
		})
	}
}

// TestFromRefDoesNotFabricateAHostIdentity is the boundary half, and its subject is the difference
// between *no identity* and *identity zero*.
//
// `externalize-i` (`extern.wast:46-49`) returns a non-null externref with no host identity behind it;
// `(ref.extern 0)` is a host reference whose identity is the index 0. A `fromRef` that dropped the
// `IsHost` guard reports the first as the second, because `RefID`'s zero value is a legal index — and
// the whole file passes either way, which is the mutation above.
func TestFromRefDoesNotFabricateAHostIdentity(t *testing.T) {
	got := fromRef(ref{Externalized: true, IsI31: true, I31: 1}, binary.ExternRef)
	if got.IsHost || got.RefID != 0 {
		t.Fatalf("fromRef of a non-host externref = %+v, want no identity", got)
	}
	// The discriminating half: a control that only ever watched `fromRef` report "no identity"
	// would be satisfied by one that never reports an identity at all.
	host := fromRef(ref{IsHost: true, Addr: 0}, binary.ExternRef)
	if !host.IsHost || host.RefID != 0 {
		t.Errorf("fromRef of host reference 0 = %+v, want IsHost with RefID 0 — index zero is a "+
			"legal identity, and it is exactly the value a fabricating boundary would also produce, "+
			"so the pair above is the only thing that separates them", host)
	}
}
