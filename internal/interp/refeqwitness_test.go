// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"
)

// The witness half of #260, and the reason it exists is a defect the coverage control could not have
// seen.
//
// `TestRefEqAccountsForEveryRefField` checks that every field of `ref` has an *entry* in
// `refEqTreatment`. That is a real control — it fires when the struct grows — but it is a control
// over a map of **justifications**, and a justification's existence is not its truth. #260's
// specimen arrived on schedule and in the field the issue predicted: `Addr`'s entry read *"not
// compared: reachable only on a funcref, which refEq reports as #9's"*, both halves true when
// written, and rung 5 slice 3's host references made both halves false — a host reference reaches
// `refEq` with its identity in `Addr` and `refEq` compares it (`script.ml:93`). **The entry existed
// throughout, so the coverage control was green across the inversion and had nothing to say about
// it.** No amount of scoping that control to the space helps: the space it covers is *fields*, and
// the thing that rotted is a *sentence*.
//
// So each treatment gets a **discriminating witness pair** — two `ref` values differing in exactly
// the named field, whose outcomes under `refEq` are what the sentence claims they are. A pair is
// discriminating when the two members' answers *differ* for a "compared" claim: that is the property
// no map of prose can fake, because it is a fact about the running comparison. For a claim that a
// field is unreachable, the witness asserts the reported verdict instead, which is the claim's own
// content.
//
// **What this does not do, stated because the trichotomy's rider says liveness is not correctness.**
// A witness proves `refEq` *behaves as its entry says*. It cannot prove the entry agrees with the
// reference — that audit is reading `extern.ml`, `script.ml` and `i31.ml` against the sentence, and
// it is how the `Externalized` ordering hazard below was found rather than by any control. The
// citations in each entry are the record of that reading; the witnesses are the record that the code
// still does what the reading concluded.
type refEqWitness struct {
	// field is the `refEqTreatment` key this pair witnesses. Every key needs at least one.
	field string

	// why names what the pair demonstrates, printed on failure so a fired row explains the claim
	// it was defending rather than only the values that broke it.
	why string

	// a and b differ in exactly `field`. For a compared claim they must answer false; `same` is
	// the other half of the discrimination and must answer true.
	a, b ref

	// same is a pair that agrees in `field`, required for a compared claim and empty otherwise —
	// the half that makes the pair discriminating rather than merely negative. A control that only
	// ever watched `refEq` say false could be satisfied by a comparison that says false always.
	sameA, sameB ref

	// wantErr is set for a claim that the field is unreachable, where the entry's content is the
	// *verdict* (`refEq` reports #9's) rather than an equality answer.
	wantErr bool
}

// hostRef, i31Ref and the rest are the witness constructors, spelled out here rather than inline so
// each row below reads as "these two differ in one field".
func i31Ref(v uint32) ref  { return ref{IsI31: true, I31: v} }
func hostRef(n uint32) ref { return ref{IsHost: true, Addr: n} }

var (
	witInstA = &Instance{}
	witObjA  = &gcObj{}
	witObjB  = &gcObj{}
	witExcA  = &excObj{}
	witExcB  = &excObj{}
)

var refEqWitnesses = []refEqWitness{
	{
		field: "Null",
		why:   "two nulls are equal; a null and a non-null are not, whatever the non-null carries",
		a:     ref{Null: true}, b: i31Ref(5),
		sameA: ref{Null: true}, sameB: ref{Null: true},
	},
	{
		field: "IsI31",
		why:   "the discriminator selects the i31 clause, and a mixed pair answers false (i31.ml:20)",
		a:     i31Ref(5), b: ref{IsI31: false, I31: 5},
		sameA: i31Ref(5), sameB: i31Ref(5),
	},
	{
		field: "I31",
		why:   "the payload is the identity — same integer equal, different integer not",
		a:     i31Ref(5), b: i31Ref(6),
		sameA: i31Ref(5), sameB: i31Ref(5),
	},
	{
		field: "Obj",
		why:   "an aggregate's identity is its allocation, so two distinct objects are not eq (aggr.ml registers no hook)",
		a:     ref{Obj: witObjA}, b: ref{Obj: witObjB},
		sameA: ref{Obj: witObjA}, sameB: ref{Obj: witObjA},
	},
	{
		field: "Externalized",
		why: "both set recurses on the payload; a mixed pair answers false — and this row is the " +
			"**ordering** witness: with the clause below `IsI31` these two would answer true, " +
			"the bit being invisible to the i31 comparison",
		a: ref{Externalized: true, IsI31: true, I31: 5}, b: ref{Externalized: false, IsI31: true, I31: 5},
		sameA: ref{Externalized: true, IsI31: true, I31: 5}, sameB: ref{Externalized: true, IsI31: true, I31: 5},
	},
	{
		field: "IsHost",
		why:   "the discriminator selects the host clause, and a mixed pair answers false (script.ml:93)",
		a:     hostRef(1), b: ref{IsHost: false, Addr: 1, IsI31: true},
		sameA: hostRef(1), sameB: hostRef(1),
	},
	{
		field: "Addr",
		why: "compared **when IsHost** — the half of this entry that rung 5 slice 3 inverted, and " +
			"the row #260 exists for: before host references landed the entry said Addr is never " +
			"compared, and the coverage control was green across the change",
		a: hostRef(1), b: hostRef(2),
		sameA: hostRef(1), sameB: hostRef(1),
	},
	{
		field: "Addr",
		why: "and *not* compared on a funcref — same instance, different index, still #9's, so the " +
			"error is reached before Addr is ever read. The entry's two halves get two rows " +
			"because one row could satisfy either half alone",
		a:       ref{Inst: witInstA, Addr: 1},
		b:       ref{Inst: witInstA, Addr: 2},
		wantErr: true,
	},
	{
		field:   "Inst",
		why:     "unreachable: a funcref pair is reported as #9's (instance.ml:42 is failwith)",
		a:       ref{Inst: witInstA},
		b:       ref{Inst: witInstA},
		wantErr: true,
	},
	{
		field:   "Exc",
		why:     "unreachable: an exnref pair is reported as #9's (exn.ml:26 is failwith)",
		a:       ref{Exc: witExcA},
		b:       ref{Exc: witExcB},
		wantErr: true,
	},
}

// TestRefEqTreatmentsHaveWitnesses is #260 discharged: every claim in `refEqTreatment` is backed by
// a pair whose behaviour under `refEq` demonstrates it.
//
// Three obligations, and the first is the one that makes this a control over the *space* rather than
// over today's rows: every key in the map must have a witness. So a future payload kind cannot
// satisfy `TestRefEqAccountsForEveryRefField` with a sentence and stop there — the sentence needs a
// pair, and writing the pair is what forces its author to run the comparison they are describing.
func TestRefEqTreatmentsHaveWitnesses(t *testing.T) {
	// The floor, per the vacuity law: an empty witness list would satisfy every loop below.
	// Sized against the treatment map rather than a literal, so it tracks the space it covers.
	if len(refEqWitnesses) < len(refEqTreatment) {
		t.Fatalf("%d witnesses for %d treatments — a witness list shorter than the map it defends "+
			"cannot cover it, and every per-row check below passes vacuously on the rows that are "+
			"missing", len(refEqWitnesses), len(refEqTreatment))
	}

	// Obligation 1: coverage of the map. The direction that matters — a treatment with no witness
	// is exactly #260's defect, a claim nobody made `refEq` answer for.
	witnessed := map[string]bool{}
	for _, w := range refEqWitnesses {
		witnessed[w.field] = true
	}
	for field := range refEqTreatment {
		if !witnessed[field] {
			t.Errorf(`refEqTreatment declares a treatment for %q with no witness pair.

This is #260: the coverage control next door checks that an *entry* exists, which is a claim, not
its truth — Addr's entry sat unchanged and green while both halves of it became false. Add a row
to refEqWitnesses whose two refs differ in %q and whose answers under refEq are what the entry
says they are. Current claim:
    %s`, field, field, refEqTreatment[field])
		}
	}

	// Obligation 2: the stale direction, for the same reason the coverage control checks it — a
	// witness for a field the map no longer mentions is asserting something nobody claims.
	for _, w := range refEqWitnesses {
		if _, ok := refEqTreatment[w.field]; !ok {
			t.Errorf("witness for %q, which refEqTreatment does not mention: %s", w.field, w.why)
		}
	}

	// Obligation 3: each witness actually discriminates.
	for _, w := range refEqWitnesses {
		t.Run(w.field+"/"+w.why[:min(len(w.why), 40)], func(t *testing.T) {
			got, err := refEq(w.a, w.b)
			if w.wantErr {
				if !errors.Is(err, ErrNotValidated) {
					t.Errorf("refEq(%+v, %+v) = (%v, %v), want ErrNotValidated — the entry's claim "+
						"is about the *verdict*: %s", w.a, w.b, got, err, w.why)
				}
				return
			}
			if err != nil {
				t.Fatalf("refEq(%+v, %+v) errored: %v — a compared claim needs an answer, not a "+
					"refusal: %s", w.a, w.b, err, w.why)
			}
			if got {
				t.Errorf("refEq(%+v, %+v) = true, want false — the pair differs in %s: %s",
					w.a, w.b, w.field, w.why)
			}
			// The half that makes it a *discrimination* rather than a comparison that always
			// says false. Without this, a `refEq` returning false unconditionally would satisfy
			// every negative row above — which is the same defect as a bound far from what it
			// bounds: the assertion runs, agrees, and distinguishes nothing.
			same, err := refEq(w.sameA, w.sameB)
			if err != nil {
				t.Fatalf("refEq(%+v, %+v) errored: %v — the agreeing half of the pair must also "+
					"reach an answer: %s", w.sameA, w.sameB, err, w.why)
			}
			if !same {
				t.Errorf("refEq(%+v, %+v) = false, want true — a pair agreeing in %s must be eq, "+
					"or the negative row above is satisfied by a comparison that never says true: %s",
					w.sameA, w.sameB, w.field, w.why)
			}
		})
	}
}
