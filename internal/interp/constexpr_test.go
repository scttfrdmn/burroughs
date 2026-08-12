// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// twoI32 is a constant expression that leaves **two** numeric values where one is wanted, which is
// the shortest way to provoke the arity message from any of the four call sites.
//
// It works because the implicit label of a const-expr does not truncate on falling off the end — the
// same reason a `v128` initializer used to fail against a hard-coded `1` rather than silently keeping
// half of itself (grave #239). Measured, not assumed: with the arity check removed this expression
// leaves `len(st.num) == 2`.
func twoI32() []binary.Instr {
	return []binary.Instr{{Op: 0x41, Imm0: 1}, {Op: 0x41, Imm0: 2}, {Op: opEnd}}
}

// TestConstExprMessageNamesItsOwnCallerNotAnother is grave #240's control: each of the four
// constant-expression call sites reports **its own** construct.
//
// # Why this is a partition check and not an `errors.Is`
//
// All four messages wrap `ErrNotValidated`, so `errors.Is` scores every member as every other member
// — the exact defect grave #34 records. What distinguishes them is the *site string*, so that is what
// is asserted, and each row also asserts the **absence** of the other three sites' words. The
// absence half is the one that fails on the pre-#241 engine: `constExprValue` was shared by three
// callers and hard-coded "a data segment's offset", so an element segment's offset and a global
// initializer both reported themselves as data segments — present-and-correct for one row, wrong for
// two, and no `errors.Is` anywhere could see it.
//
// # Every row goes through instantiation, never through the helper
//
// A row that called `constAddr("an element segment's offset", …)` directly would assert that this
// test can pass a string to a function. The claim is about the *call sites* — which construct each
// one names — so each row is a module whose relevant segment is malformed in the arity direction, run
// through `Instantiate`, with the message read off `Deferred()`.
//
// The fourth site is why the count is four and not #241's three: an element segment's *offset* and an
// element *expression* are different lines of the user's module, so folding them into one string would
// send a reader of `(elem (offset …) (item …))` to the wrong half of their own segment.
func TestConstExprMessageNamesItsOwnCallerNotAnother(t *testing.T) {
	// funcref-typed table, so the element rows have somewhere to point.
	table := []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}}

	rows := []struct {
		site string
		mod  *binary.Module
	}{
		{
			"a data segment's offset",
			&binary.Module{
				Memories: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
				Datas:    []binary.DataSegment{{Offset: twoI32(), Init: []byte{}}},
			},
		},
		{
			"an element segment's offset",
			&binary.Module{
				Tables: table,
				Elems: []binary.ElemSegment{{
					Mode: binary.ElemActive, ElemType: binary.FuncRef, Offset: twoI32(),
				}},
			},
		},
		{
			// Two references where the element type wants one. Provoked with `ref.null` rather
			// than `ref.func` so the row does not also depend on a function existing.
			"an element expression",
			&binary.Module{
				Tables: table,
				Elems: []binary.ElemSegment{{
					Mode:     binary.ElemPassive,
					ElemType: binary.FuncRef,
					ByExpr:   true,
					Exprs: [][]binary.Instr{{
						{Op: opRefNull}, {Op: opRefNull}, {Op: opEnd},
					}},
				}},
			},
		},
		{
			"a global initializer",
			&binary.Module{
				Globals: []binary.Global{{Type: binary.I32, Init: twoI32()}},
			},
		},
	}

	// The vacuity floor. A partition check over an empty or shrunken row set agrees with itself
	// perfectly, and this one's rows are also the *count* being claimed above.
	if len(rows) != 4 {
		t.Fatalf("%d rows, want the four call sites named in this test's comment", len(rows))
	}

	for _, r := range rows {
		in, trap := Instantiate(r.mod)
		if trap != nil {
			t.Errorf("%s: trap %v, want the arity failure on the deferred channel", r.site, trap)
			continue
		}
		err := in.Deferred()
		if err == nil {
			t.Errorf("%s: an expression leaving two values where one is wanted was accepted", r.site)
			continue
		}
		if !errors.Is(err, ErrNotValidated) {
			t.Errorf("%s: got %v, want ErrNotValidated — a shape a validator would have caught is "+
				"#9's verdict, not a trap", r.site, err)
		}
		got := err.Error()
		if !strings.Contains(got, r.site) {
			t.Errorf("%s: message is %q and does not name this site", r.site, got)
		}
		// The discriminating half: no row may name another row's construct. This is what a shared
		// hard-coded string fails.
		for _, other := range rows {
			if other.site == r.site {
				continue
			}
			if strings.Contains(got, other.site) {
				t.Errorf("%s: message is %q, which names %q — the wrong construct, so a reader is "+
					"sent to the wrong line of their module (grave #240)", r.site, got, other.site)
			}
		}
	}
}

// TestConstExprChecksBothStackArrays is the row that separates a two-array shape check from a
// one-array one.
//
// `constExprValue` tested `len(st.num)` alone. Most mismatches fail that test by luck — an expression
// that pushes a reference where a number was wanted leaves zero numbers — so the discriminating case
// is one where the **numeric count is right and the reference count is wrong**: `(global i32
// (i32.const 1) (ref.null func))`. One number, as asked; one stray reference, unnoticed.
//
// What the old engine did with it is the reason this matters rather than being tidiness: the number
// was returned and the reference silently discarded, so a module a validator would have rejected
// instantiated with a plausible value. That is the accept direction, where §9 G-3 says the suite
// scores the defect green by construction.
func TestConstExprChecksBothStackArrays(t *testing.T) {
	in, trap := Instantiate(&binary.Module{
		Globals: []binary.Global{{Type: binary.I32, Init: []binary.Instr{
			{Op: 0x41, Imm0: 1}, {Op: opRefNull}, {Op: opEnd},
		}}},
	})
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	err := in.Deferred()
	if err == nil {
		t.Fatal("an i32 initializer that also left a reference was accepted; the numeric count " +
			"alone is right, which is exactly why one-array checking cannot see this")
	}
	// Both counts in the message, because a message reporting only the array it happens to check
	// describes the wrong half of the mismatch.
	if got := err.Error(); !strings.Contains(got, "1 numeric and 1 reference") {
		t.Errorf("message is %q, want it to report both stacks' counts", got)
	}
}

// TestElemSegmentWithANumericElementTypeIsRefused is the back-door assertion for `segmentRefs`'
// reftype guard, in `TestUnhandledFCSubOpcodeStaysOnTheWorkList`'s shape: no module the decoder
// accepts can carry a numeric `ElemType` (every wire form yields a reftype, which `ElemType`'s own
// doc comment records), so the front door cannot reach this and it is still worth asserting.
//
// The guard exists because `constExpr` dispatches on the type it is handed: a numeric element type
// would ask for a numeric slot, leave `constVal.ref` at its zero value, and fill the table with
// references that are neither null nor valid — which then reads as a *successful* instantiation
// holding wrong values, the accept-direction failure. Scoped to the space rather than to what the
// decoder produces today.
//
// # The expression is `i32.const 5`, and the choice is the whole row
//
// A `ref.null` expression here would make this test **red for the wrong reason**: without the guard,
// asking for one numeric slot and getting one reference fails the arity check, so the run goes red
// with an arity message and the guard's own subject is never reached. That is #159's attribution
// lesson — a control whose red names something other than the defect sends its reader to repair the
// wrong thing.
//
// `i32.const 5` is the discriminating expression because it satisfies the arity exactly. Remove the
// guard and this segment *succeeds*, returning one `ref{}` — not null, `Addr` 0, `Inst` nil — which
// `blit` writes into a funcref table and `call_indirect` then dereferences. So the assertion below is
// on the refusal **and** on the message naming the type, and the falsification confirmed both: with
// the guard deleted, `err` is nil.
func TestElemSegmentWithANumericElementTypeIsRefused(t *testing.T) {
	in := &Instance{}
	got, err := in.segmentRefs(&binary.ElemSegment{
		ElemType: binary.I32,
		ByExpr:   true,
		// Arity-satisfying on purpose: one numeric slot, which is what an i32 element type asks
		// for. See the comment above for why `ref.null` would test the arity check instead.
		Exprs: [][]binary.Instr{{{Op: 0x41, Imm0: 5}, {Op: opEnd}}},
	})
	if err == nil {
		t.Fatalf("an element segment typed i32 was accepted, yielding %+v — a reference that is "+
			"neither null nor valid, written into a table for call_indirect to dereference", got)
	}
	if !errors.Is(err, ErrNotValidated) {
		t.Fatalf("got %v, want ErrNotValidated for an element segment typed i32", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "i32") {
		t.Errorf("message is %q, want it to name the offending type", msg)
	}
}

// TestV128GlobalSetWritesBothHalves is `global.set`'s half of grave #239, and it is separate from
// `TestV128GlobalRoundTripsAllFourLanes` for the reason `TestGlobalSetOfARefWritesTheRefSlot` is
// separate from its own read: the two accessors are different dispatches, and one arm being right
// says nothing about the other.
//
// The body writes a vector whose four lanes all differ from the initializer's, then reads two lanes
// back — one from each 64-bit half. Two lanes rather than four because the question here is *which
// halves were written*, and one lane per half answers it; a `set` that wrote only the low half leaves
// the initializer's high lane, and one that transposed the halves answers both lanes with the other's
// value.
func TestV128GlobalSetWritesBothHalves(t *testing.T) {
	const initLo, initHi = 0x0000000200000001, 0x0000000400000003
	const setLo, setHi = 0x0000000600000005, 0x0000000800000007

	m := &binary.Module{
		Types: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Results: []binary.ValType{binary.I32, binary.I32},
		}}},
		Funcs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Prefix: 0xfd, Op: 0x0c, Imm0: setLo, Imm1: setHi}, // v128.const
			{Op: 0x24, Imm0: 0},               // global.set 0
			{Op: 0x23, Imm0: 0},               // global.get 0
			{Prefix: 0xfd, Op: 0x1b, Imm0: 0}, // i32x4.extract_lane 0 (low half)
			{Op: 0x23, Imm0: 0},               // global.get 0
			{Prefix: 0xfd, Op: 0x1b, Imm0: 3}, // i32x4.extract_lane 3 (high half)
			{Op: opEnd},
		}}},
		Globals: []binary.Global{{Type: binary.V128, Mutable: true, Init: []binary.Instr{
			{Prefix: 0xfd, Op: 0x0c, Imm0: initLo, Imm1: initHi},
			{Op: opEnd},
		}}},
		Exports: []binary.Export{{Name: "f", Kind: binary.ExternFunc, Index: 0}},
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("deferred: %v", err)
	}
	out, err := in.Invoke("f")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d results, want 2", len(out))
	}
	if out[0].Bits != 5 {
		t.Errorf("lane 0 = %d, want 5; 1 would mean global.set did not write the low half", out[0].Bits)
	}
	if out[1].Bits != 8 {
		t.Errorf("lane 3 = %d, want 8; 4 would mean global.set left the initializer's high half, "+
			"and 6 would mean the two halves were transposed", out[1].Bits)
	}
}
