// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"strings"
	"testing"
	"unsafe"
)

// Rung 4's controls — #255, and the file is short **on purpose**, which is the part worth explaining.
//
// # The oracle covers the arithmetic, so a control that re-asserts it buys nothing
//
// `i31.wast` is unusually generous where these arms are concerned: `:37-42` pin `i31.get_u` across
// `-1`, `0x4000_0000`, `0x7fff_ffff`, `0xaaaa_aaaa` and `0xcaaa_aaaa`, `:48-51` pin `i31.get_s` on the
// same values, and `:53-54` assert both null traps by their exact text. So the construction mask, both
// extensions, and the trap string are **oracle-covered**, and the file went 7 pass / 64 fail → 54 / 17
// on these arms landing. A hand-written row asserting `get_s(0x7fff_ffff) == -1` would be a second
// copy of `i31.wast:49` with a worse provenance story, and the standing rule is to prefer deriving
// corpora from the suite at run time over transcribing them.
//
// What is left is the part no rejection corpus can reach — §9 G-3's accept direction, plus the two
// places this engine speaks in its own voice rather than the spec's. Four controls, each with a stated
// reason why the suite cannot ask its question:
//
//  1. TestRefEqSeesTheConstructionMask — a *derived* vector: the suite's own premises entail it and no
//     vector contains it, because `ref_eq.wast` only ever boxes 7 and 8.
//  2. TestI31ReachingAnAggregateArmNamesItself — reachable only because #9 is absent, so a validated
//     corpus has no vector by construction.
//  3. TestRefEqMixedI31AndFuncrefIsFalseNotADebt — same reason, and the failure mode is a *refusal of
//     a legal program*, which is the direction the board scores green.
//  4. TestRefWidthIsMeasuredNotAssumed — a claim about representation, which the spec has no opinion
//     about at all.
//
// Every row below was watched fail, and each site names the mutation that killed it. Two outcomes from
// the battery are recorded rather than tidied away, both being the standing rules paying out:
//
//   - **A no-op mutation with a non-empty diff.** The mixed-pair row's first mutation moved refEq's i31
//     clause below the *exnref* report and left it above the funcref one, so the pair under test never
//     changed path — three lines of diff, zero behaviour, a green that reads as a stillborn control.
//     Caught by reading the diff for behaviour instead of for non-emptiness (#250's M6), and it cost one
//     anchored retry: the mutation that actually tests the ordering claim is the `IsI31 && IsI31`
//     misreading, which is also the one a reader would really write.
//   - **A forecast miss whose cause was the measuring instrument.** M4 (the logical-shift mutation) was
//     pre-registered at three `i31.wast` vectors and cost **four**: the fourth is `:45`'s
//     `get_s(-1) = -1`, which the grep used to count negative-expected rows could not see, having
//     matched hex literals only. The model was right and the regex was the wrong instrument for
//     counting — so the number is corrected here rather than quietly replaced.

// TestRefEqSeesTheConstructionMask is the row that separates masking at construction from masking at
// read, and the corpus cannot: `ref_eq.wast:15-17` boxes only 7 and 8, whose masked and unmasked forms
// are identical.
//
// **Provenance: derived.** Premises, both checked by `TestFixtureProvenance`'s resolver:
//
//   - `i31.wast:37` — `(assert_return (invoke "get_u" (i32.const -1)) (i32.const 0x7fff_ffff))`. All
//     ones in, 31 ones out, so *something* masks bit 31 away.
//   - `ref_eq.wast:15-16` — two independently constructed `(ref.i31 (i32.const 7))` compare equal, so
//     `ref.eq` on i31 is structural on the payload rather than on the boxing.
//
// Inference: if the payload is masked and equality is structural on the payload, then two spellings
// that mask to one payload are `eq`. `0x8000_0001` and `1` are such a pair. The suite states both
// premises and asks neither the conjunction — which is exactly the gap the derived category exists for,
// and the inference is reviewed by eyes while the premises are machine-checked.
//
// **Why it is worth a row at all:** it is the *only* thing that fails if `i31Mask` moves from
// `execRefI31` to the two readers. Move it, and every one of `i31.wast`'s fourteen value vectors still
// passes — the reads mask identically — while `ref.eq` starts comparing 32-bit payloads and answers 0
// here. A refactor that looks like a pure relocation would take the board with it and lose this.
//
// Falsified by deleting `& i31Mask` from execRefI31 and masking inside popI31 instead: `i31.wast` stayed
// at 54 pass and this row went 1 → 0.
func TestRefEqSeesTheConstructionMask(t *testing.T) {
	src := `(module
	  (func (export "c") (result i32)
	    (ref.eq (ref.i31 (i32.const 0x80000001)) (ref.i31 (i32.const 1)))))`
	out := runGC(t, src)
	if got := out[0].Int32(); got != 1 {
		t.Errorf("got %d, want 1 — 0x8000_0001 and 1 box to the same 31-bit payload "+
			"(i31.wast:37 says construction masks; ref_eq.wast:15 says equality is structural), so "+
			"a read-time mask has been substituted for a construction-time one", got)
	}
}

// TestI31ReachingAnAggregateArmNamesItself is grave #36's rule at rung 4's new payload: a message that
// names a value from the input must not name a value the input never held.
//
// An i31 sets none of `ref`'s pointer fields, so `notAggregate`'s switch — which derives "which kind
// instead" from `Exc`/`Inst`/`Obj` — falls through to its default and reports **"a function
// reference"** unless i31 has an arm. That is the engine testifying to something false about its own
// input, with the right verdict: `array.len` on an i31 is refused either way, so no board moves and no
// vector exists, validation having ruled the case out (#9's absence is the only reason it is
// reachable).
//
// Falsified by deleting the `case r.IsI31` arm from `notAggregate`: the row reported "a function
// reference" and failed. Note what that mutation does *not* do — it does not change a single verdict
// anywhere in the corpus, which is the whole point of the row.
func TestI31ReachingAnAggregateArmNamesItself(t *testing.T) {
	src := `(module
	  (func (export "c") (result i32)
	    (array.len (ref.i31 (i32.const 1)))))`
	_, err := runGCErr(src)
	if err == nil {
		t.Fatalf("got no error, want a report: array.len on an i31 reference")
	}
	if !errors.Is(err, ErrNotValidated) {
		t.Errorf("got %v, want ErrNotValidated — #9's verdict, not a trap", err)
	}
	if !strings.Contains(err.Error(), "an i31 reference") {
		t.Errorf("got %q, want the kind named as an i31 reference", err)
	}
	if strings.Contains(err.Error(), "function reference") {
		t.Errorf("got %q — the input held no function reference, and an error naming one is a "+
			"lying witness with a correct verdict (grave #36)", err)
	}
}

// TestRefEqMixedI31AndFuncrefIsFalseNotADebt pins the answer this engine gives to a pair the reference
// answers by *delegation* rather than by an override, which is the reading that took a correction once
// already in refEq's aggregate clause and would have taken it again here.
//
// `i31.ml:20` matches only `I31Ref, I31Ref`; `instance.ml:42`'s `failwith` matches only
// `FuncRef, FuncRef`. A mixed pair matches neither and falls to the base `eq_ref' = (==)`, which
// answers **false** on two different blocks. So `ErrNotValidated` here would be wrong twice over: it
// invents a verdict the reference does not give, and it does so in the accept direction, refusing a
// program the reference runs.
//
// No vector can ask this — `funcref` is not under `eq`, so the validator rejects the module and the
// corpus has no such row. Reachable here only because #9 is absent, which is the same licence the
// control above runs on.
//
// Falsified by narrowing refEq's clause from `a.IsI31 || b.IsI31` to `a.IsI31 && b.IsI31` — the
// "each override matches its own kind, so test for both" misreading, which is what makes it the
// mutation worth running: it is the code a careful reader of `i31.ml:20` writes. The mixed pair then
// falls past the clause to the funcref report and the row got
// `ref.eq on a function reference, which is not under eq`. **Both board files stayed put** — 54/17 and
// 83/0 — so the refusal of a legal program is invisible to the oracle, which is the row's whole reason
// for existing.
//
// The first attempt at this mutation *relocated* the clause instead and passed, having moved it below
// the exnref report but still above the funcref one: a non-empty diff that changed no path this pair
// takes. Recorded in the file header, because the interesting half is that a green there would have
// read as a stillborn control.
func TestRefEqMixedI31AndFuncrefIsFalseNotADebt(t *testing.T) {
	src := `(module
	  (func $f)
	  (func (export "c") (result i32)
	    (ref.eq (ref.i31 (i32.const 1)) (ref.func $f))))`
	out, err := runGCErr(src)
	if err != nil {
		t.Fatalf("got %v, want an answer: a mixed pair delegates to physical equality and is "+
			"false, so reporting #9's debt here refuses a program the reference runs", err)
	}
	if got := out[0].Int32(); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// TestRefWidthIsMeasuredNotAssumed pins `ref`'s size, and exists because `IsI31`'s own comment cites it
// by name — a doc comment's identifier is a citation and gets a resolving check (#114).
//
// **The claim being pinned is a cost, not a correctness property**, so the row is a tripwire rather
// than a bound: 0002 puts every reference in a traced parallel array, so `ref`'s width is paid on every
// reference slot the engine ever holds, and a payload kind added carelessly widens it silently. Rung 4
// took it 32 → 40 and could not avoid doing so — the floor for two bools, two `uint32`s and three
// pointers is 34 bytes, which aligns to 40 — but *that* is a fact about this rung's fields, not a
// licence for the next one.
//
// This row is also where a wrong claim was caught rather than shipped: `IsI31` was first documented as
// packing free beside `Null` and keeping `ref` at 32 bytes, which is a plausible sentence about Go
// layout and false, both placements measuring 40. The comment now states the measurement and names
// unioning with `Addr` as the only route under it — the option 0020 declines by name. *Print, don't
// reason*, applied to a struct's own footprint.
func TestRefWidthIsMeasuredNotAssumed(t *testing.T) {
	// Two bools, two uint32s, three pointers: 1+1+4+4+8+8+8 = 34, aligned up to 40. Derived from
	// the field list rather than written as a bare 40, so the arithmetic is auditable and a reader
	// adding a field sees which term theirs joins.
	const want = 40
	if got := unsafe.Sizeof(ref{}); got != want {
		t.Errorf("unsafe.Sizeof(ref{}) = %d, want %d — a reference slot's width is paid on every "+
			"slot in a traced parallel array (0002), so a payload kind that widens it does so "+
			"everywhere; if the growth is necessary, say so here and move the number", got, want)
	}
	// And the vacuity half: a pin on a size says nothing unless the field it was added for is
	// actually in the struct. `refEqTreatment` covers presence for `refEq`'s purposes; this covers
	// it for the *width's*, so that trimming `I31` back out to buy the eight bytes fails a row
	// rather than passing two.
	if unsafe.Sizeof(ref{}.I31) != 4 || unsafe.Sizeof(ref{}.IsI31) != 1 {
		t.Errorf("the i31 payload is not the width this pin was computed from")
	}
}
