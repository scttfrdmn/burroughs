// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// gcGate is the Features every source in this file decodes with. `v128` fields need SIMD too, and it
// is named here rather than left to the zero value even though #233 flipped SIMD default-on: a gate
// this file's rows *depend* on is stated, so that a future default change moves the default and not
// these rows' meaning.
var gcGate = binary.Features{GC: true, SIMD: true}

// runGC is run1 with the GC gate on — run1's own reasoning for going through the encoder and decoder
// rather than hand-building a `binary.Module` applies unchanged, and here it earns something extra:
// the struct arms' immediates are a *pair* (type index, field index), so a row that built `binary.Instr`
// directly would be asserting this test's opinion of which immediate is which rather than the
// decoder's. `opTableFB`'s `{immIdx, immIdx}` shape is part of what is under test.
func runGC(t *testing.T, src string) []Value {
	t.Helper()
	out, err := runGCErr(src)
	if err != nil {
		t.Fatalf("invoke %s: %v", src, err)
	}
	return out
}

// runGCErr is runGC's error-returning twin, for the rows whose subject *is* the refusal —
// `encodeDecodeInvoke`'s reason: a `Fatalf` helper cannot serve a test asserting something is
// rejected, and it returns the first error from any stage because "the encoder refused it" and "the
// interpreter refused it" are different findings.
func runGCErr(src string) ([]Value, error) {
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		return nil, err
	}
	d := &binary.Decoder{Features: gcGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		return nil, err
	}
	in, trap := Instantiate(m)
	if trap != nil {
		return nil, trap
	}
	return in.Invoke("c")
}

// TestStructPackedFieldWrapsAtWriteAndExtendsAtRead is the bidirectional control the packed-field
// design hands us: **the same value's verdict depends on the field's width, not on the bytes**, so a
// single wrong width fails the two halves in opposite directions where either half alone would look
// like a plausible reading.
//
// The pattern is the one #36's limits-versus-memory-index pair established: prefer a pair whose
// members disagree over two independent assertions. Here 257 stored into an `i8` reads back **1**
// and into an `i16` reads back **257** — from identical source bytes and an identical instruction —
// so an engine that masks with the wrong width, or that stores unmasked and masks at read, cannot
// satisfy both. Concretely, the three mutations this row exists to catch:
//
//   - drop the `wrap` at write and extend at read instead → the i8 row answers 257, not 1;
//   - mask with a fixed 8 bits → the i16 row answers 1, not 257;
//   - mask with a fixed 16 bits → the i8 row answers 257, not 1.
//
// The signed half is a second axis crossed with the first: 254 in an `i8` is **-2** signed and
// **254** unsigned, and 65535 in an `i16` is **-1** and **65535**. Since `extend_u` is the identity
// on an already-wrapped value (`aggr.ml:17`), the unsigned rows are what catch a write side that
// forgot to wrap, and the signed rows are what catch a `gap` computed in bytes where the field's
// `Width` is in bits — a units slip that is right for neither width but *looks* right, and which
// would otherwise be caught by only one of the two widths.
//
// Cited rather than synthetic: `struct.wast:196-229` asserts exactly these six numbers through
// `get_packed_g1_*` and `set_get_packed_g0_*`. The rows are here as well because the board reports a
// file, and a wrong width inside one of six read-backs is a number no bucket names.
func TestStructPackedFieldWrapsAtWriteAndExtendsAtRead(t *testing.T) {
	rows := []struct {
		name  string
		field string // the struct field's declared storage
		store uint64 // what the module writes
		signd int32  // struct.get_s
		unsgn int32  // struct.get_u
	}{
		// struct.wast:200 — `(assert_return (invoke "get_packed_g1_0") (i32.const -2) (i32.const 254))`
		{"i8 holds 254", "i8", 254, -2, 254},
		// struct.wast:203 — `get_packed_g1_1` over 255
		{"i8 holds 255", "i8", 255, -1, 255},
		// struct.wast:206 — `get_packed_g1_2` over 65534
		{"i16 holds 65534", "i16", 65534, -2, 65534},
		// struct.wast:209 — `get_packed_g1_3` over 65535
		{"i16 holds 65535", "i16", 65535, -1, 65535},
		// The disagreeing pair, and the reason this table is one table: struct.wast:222 asserts
		// `set_get_packed_g0_1 (i32.const 257)` reads back 1, and :226 asserts
		// `set_get_packed_g0_3 (i32.const 257)` reads back 257. Same bytes, different verdicts,
		// decided by the field.
		{"i8 truncates 257 to 1", "i8", 257, 1, 1},
		{"i16 keeps 257", "i16", 257, 257, 257},
	}
	if len(rows) != 6 {
		t.Fatalf("the table is the domain and it has %d rows, not 6 — a row was lost", len(rows))
	}
	// Vacuity beyond the count: the pair that carries the *bidirectional* claim must actually
	// disagree, or the table has been edited into six independent assertions and the property this
	// test is named for is no longer being made. A comparison that agrees with itself is the
	// empty-set defect wearing a full table's clothes.
	var wide, narrow int32
	for _, r := range rows {
		if r.store == 257 && r.field == "i8" {
			narrow = r.unsgn
		}
		if r.store == 257 && r.field == "i16" {
			wide = r.unsgn
		}
	}
	if wide == narrow {
		t.Fatalf("the i8 and i16 rows for 257 expect the same answer (%d), so this table no "+
			"longer makes a claim about the field deciding the verdict", wide)
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			src := `(module
  (type $p (struct (field (mut ` + r.field + `))))
  (func (export "c") (result i32 i32)
    (local $s (ref null $p))
    (local.set $s (struct.new_default $p))
    (struct.set $p 0 (local.get $s) (i32.const ` + itoa(r.store) + `))
    (struct.get_s $p 0 (local.get $s))
    (struct.get_u $p 0 (local.get $s))))`
			out := runGC(t, src)
			if len(out) != 2 {
				t.Fatalf("want two results, got %d: %v", len(out), out)
			}
			if got := out[0].Int32(); got != r.signd {
				t.Errorf("struct.get_s on %s holding %d = %d, want %d",
					r.field, r.store, got, r.signd)
			}
			if got := out[1].Int32(); got != r.unsgn {
				t.Errorf("struct.get_u on %s holding %d = %d, want %d",
					r.field, r.store, got, r.unsgn)
			}
		})
	}
}

// TestStructNewFillsFieldsInDeclarationOrder pins `split` plus `List.rev args` (eval.ml:676-679):
// `struct.new` takes its initializers off the stack with the **last** field's value on top.
//
// Three fields with three distinguishable values, which is the only way to see it — a struct whose
// fields all hold the same value is satisfied by any permutation, and `struct.wast:73`'s
// `(struct.new $vec (f32.const 1) (f32.const 2) (f32.const 3))` is distinguishable *only* by value
// for exactly this reason. Reversing the loop's direction answers 3, 2, 1 here.
//
// The types are deliberately mixed — i32, i64, f64 — so the row also catches a version that popped
// every field from one array: an i64 field read as an i32's slot is a different number, not merely a
// misordered one.
func TestStructNewFillsFieldsInDeclarationOrder(t *testing.T) {
	src := `(module
  (type $t (struct (field i32) (field i64) (field f64)))
  (func (export "c") (result i32 i64 f64)
    (local $s (ref null $t))
    (local.set $s (struct.new $t (i32.const 11) (i64.const 22) (f64.const 33)))
    (struct.get $t 0 (local.get $s))
    (struct.get $t 1 (local.get $s))
    (struct.get $t 2 (local.get $s))))`
	out := runGC(t, src)
	if len(out) != 3 {
		t.Fatalf("want three results, got %d: %v", len(out), out)
	}
	if got := out[0].Int32(); got != 11 {
		t.Errorf("field 0 = %d, want 11 — the initializers were consumed in the wrong order", got)
	}
	if got := out[1].Int64(); got != 22 {
		t.Errorf("field 1 = %d, want 22", got)
	}
	if got := out[2].Float64(); got != 33 {
		t.Errorf("field 2 = %v, want 33", got)
	}
}

// TestStructNewDrawsEachFieldFromItsOwnStack is the mixed-array half stated as its own row, because
// the ordering test above cannot see it: a struct whose fields span the numeric *and* reference
// stacks needs `popField` to be per-field, and a version that asked for `len(fields)` numeric slots
// would leave the reference stack untouched and read a number where the reference belongs.
//
// The reference field sits **between** two numeric ones deliberately. With it first or last, a
// version that popped all numerics and then all references would still answer correctly for one
// ordering; interleaved, the two disciplines disagree.
func TestStructNewDrawsEachFieldFromItsOwnStack(t *testing.T) {
	src := `(module
  (type $t (struct (field i32) (field anyref) (field i32)))
  (func (export "c") (result i32 i32 i32)
    (local $s (ref null $t))
    (local.set $s (struct.new $t (i32.const 7) (ref.null any) (i32.const 9)))
    (struct.get $t 0 (local.get $s))
    (ref.is_null (struct.get $t 1 (local.get $s)))
    (struct.get $t 2 (local.get $s))))`
	out := runGC(t, src)
	if len(out) != 3 {
		t.Fatalf("want three results, got %d: %v", len(out), out)
	}
	if got := out[0].Int32(); got != 7 {
		t.Errorf("field 0 = %d, want 7", got)
	}
	if got := out[1].Int32(); got != 1 {
		t.Errorf("ref.is_null of field 1 = %d, want 1 — the ref.null stored there was\n"+
			"read from the numeric stack instead", got)
	}
	if got := out[2].Int32(); got != 9 {
		t.Errorf("field 2 = %d, want 9", got)
	}
}

// TestStructSetPopsValueAboveReference pins `v :: Ref (StructRef s) :: vs'` (eval.ml:701) — the value
// is on top, the target reference underneath.
//
// **The field must be reference-typed for this to be a claim at all.** With two stack arrays, a
// numeric field's value and the target reference live in different arrays and either pop order
// works, so the whole corpus of numeric `struct.set` vectors is blind to a transposition. A
// reference-typed field puts both operands in `refs`, and popping them backwards writes the target
// object *into itself*.
//
// The assertion distinguishes the two outcomes rather than merely checking for null: field 0 is set
// to a reference to a *second* struct, and the row reads field 0 of that second struct back through
// the first. Transposed, the outer `struct.set` writes into the inner object and the outer one's
// field 0 keeps its default null, so the read traps instead of answering 42 — a different result,
// not a coincidentally equal one.
func TestStructSetPopsValueAboveReference(t *testing.T) {
	src := `(module
  (type $inner (struct (field i32)))
  (type $outer (struct (field (mut (ref null $inner)))))
  (func (export "c") (result i32)
    (local $o (ref null $outer))
    (local.set $o (struct.new_default $outer))
    (struct.set $outer 0 (local.get $o) (struct.new $inner (i32.const 42)))
    (struct.get $inner 0 (struct.get $outer 0 (local.get $o)))))`
	out := runGC(t, src)
	if len(out) != 1 {
		t.Fatalf("want one result, got %d: %v", len(out), out)
	}
	if got := out[0].Int32(); got != 42 {
		t.Errorf("got %d, want 42 — struct.set consumed its two references in the wrong order", got)
	}
}

// TestStructMutationIsSharedThroughEveryCopyOfTheReference is decision 0020's whole point as a
// control: a `ref` copy shares the `*gcObj`, so a write through one name is visible through another.
//
// A handle-and-side-table design would satisfy this too; what it actually falsifies is the opposite
// mistake — a `gcObj` copied *by value* into a local or a global, which Go makes easy and which
// would answer 0 here. The two names are a local and a global, chosen because a global is where
// `struct.wast:216-229` keeps its own shared object (`set_get_packed_g0_*` writes through `$g0` and
// reads back through a second `global.get`).
func TestStructMutationIsSharedThroughEveryCopyOfTheReference(t *testing.T) {
	src := `(module
  (type $t (struct (field (mut i32))))
  (global $g (mut (ref null $t)) (ref.null $t))
  (func (export "c") (result i32)
    (local $s (ref null $t))
    (local.set $s (struct.new $t (i32.const 0)))
    (global.set $g (local.get $s))
    (struct.set $t 0 (local.get $s) (i32.const 99))
    (struct.get $t 0 (global.get $g))))`
	out := runGC(t, src)
	if len(out) != 1 {
		t.Fatalf("want one result, got %d: %v", len(out), out)
	}
	if got := out[0].Int32(); got != 99 {
		t.Errorf("got %d, want 99 — the global holds a copy of the object rather than a "+
			"reference to it, so 0020's shared-mutation semantics is not implemented", got)
	}
}

// TestStructNullTraps pins `Trapping "null structure reference"` (eval.ml:697, :711) for both the
// read and the write.
//
// **The trap string is asserted verbatim because the oracle asserts it verbatim** —
// `struct.wast:147` and `:152` are `assert_trap … "null structure reference"`, so this is one of the
// cases #38 carves out where message rendering is oracle-covered. It is checked here anyway because
// the board reports a file: a trap raised with `refop.go`'s neighbouring "null reference" string
// would fail two vectors in a 25-vector file and name no mechanism.
//
// The operand is an **uninitialized reference local**, not a `ref.null`, and that is deliberate:
// `struct.wast` writes it that way, and it is the only place in the corpus where grave #246's
// default is observable. A `(local $s (ref null $t))` that is never set must read as null — with the
// pre-#246 `newFrame`, it read as a funcref to function 0, `r.Null` was false, and this row reached
// `notAStruct` and reported #9's debt instead of trapping.
func TestStructNullTraps(t *testing.T) {
	rows := []struct{ name, body string }{
		// struct.wast:147 — `(struct.get $t 0 (local.get $r))` on an unset local
		{"struct.get", `(drop (struct.get $t 0 (local.get $s)))`},
		// struct.wast:152 — the write direction
		{"struct.set", `(struct.set $t 0 (local.get $s) (i32.const 1))`},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			src := `(module
  (type $t (struct (field (mut i32))))
  (func (export "c")
    (local $s (ref null $t))
    ` + r.body + `))`
			_, err := runGCErr(src)
			if err == nil {
				t.Fatalf("%s on a null reference was accepted; want a trap", r.name)
			}
			var trap *Trap
			if !errors.As(err, &trap) {
				t.Fatalf("%s on null gave %T (%v), want a *Trap — an uninitialized reference "+
					"local that is not null is grave #246", r.name, err, err)
			}
			if trap.Reason != "null structure reference" {
				t.Errorf("trap reason %q, want %q — struct.wast asserts this string verbatim",
					trap.Reason, "null structure reference")
			}
		})
	}
}

// TestStructGetSignednessMustMatchFieldPacking pins `read_field`'s two `failwith` cases
// (aggr.ml:33-38), both of which `eval.ml:699` renders as `Crash.error "type mismatch reading
// field"` — validation errors, therefore #9's debt here and not traps.
//
// The two halves are opposite mistakes and neither is a trap:
//
//   - `struct.get_s`/`get_u` on an **unpacked** field: there is no width to extend from, and the
//     tempting fallback (ignore the signedness) would answer plausibly forever;
//   - plain `struct.get` on a **packed** field: the tempting fallback is to hand back the stored
//     bits, which for an i8 field holding 254 answers 254 where no correct answer exists.
//
// Both are asserted as `ErrNotValidated` *and* as not-a-trap, because reporting a trap would be
// green on any board that only checks a module was refused.
func TestStructGetSignednessMustMatchFieldPacking(t *testing.T) {
	rows := []struct{ name, field, get string }{
		{"get_s on i32", "i32", "struct.get_s"},
		{"get_u on i32", "i32", "struct.get_u"},
		{"get_s on f64", "f64", "struct.get_s"},
		{"plain get on i8", "i8", "struct.get"},
		{"plain get on i16", "i16", "struct.get"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			src := `(module
  (type $t (struct (field ` + r.field + `)))
  (func (export "c")
    (local $s (ref null $t))
    (local.set $s (struct.new_default $t))
    (drop (` + r.get + ` $t 0 (local.get $s)))))`
			_, err := runGCErr(src)
			if err == nil {
				t.Fatalf("%s on a %s field was accepted, want the reference's `failwith`",
					r.get, r.field)
			}
			if !errors.Is(err, ErrNotValidated) {
				t.Errorf("%s on a %s field gave %v, want ErrNotValidated — aggr.ml:38 is a "+
					"`failwith`, which is validation's verdict and not a trap", r.get, r.field, err)
			}
			var trap *Trap
			if errors.As(err, &trap) {
				t.Errorf("%s on a %s field trapped (%q); a type error is not a runtime event",
					r.get, r.field, trap.Reason)
			}
		})
	}
}

// TestStructNewDefaultRefusesNonDefaultableField pins `Crash.error "non-defaultable type"`
// (eval.ml:683): `default_ref (NoNull, _)` is `None` (value.ml:150-152), so a **non-nullable**
// reference field has no default and `struct.new_default` on such a struct is a module validation
// rejects.
//
// The nullable row is the half that makes this a partition rather than a single assertion: the same
// field type with `null` added must *succeed* and read back as null. Without it, an implementation
// that refused every reference-typed field would pass — the vacuity failure one level up, where the
// rejecting direction is asserted and the accepting direction is nobody's row. Accept-direction
// coverage is exactly what §9 G-3 says a rejection corpus cannot supply.
func TestStructNewDefaultRefusesNonDefaultableField(t *testing.T) {
	t.Run("non-nullable is refused", func(t *testing.T) {
		src := `(module
  (type $inner (struct (field i32)))
  (type $t (struct (field (ref $inner))))
  (func (export "c")
    (local $s (ref null $t))
    (local.set $s (struct.new_default $t))))`
		_, err := runGCErr(src)
		if err == nil {
			t.Fatalf("struct.new_default on a non-nullable reference field was accepted")
		}
		if !errors.Is(err, ErrNotValidated) {
			t.Errorf("got %v, want ErrNotValidated — eval.ml:683 is a Crash, not a trap", err)
		}
		if !strings.Contains(err.Error(), "non-defaultable") {
			t.Errorf("message %q does not name the reason; the reference calls it "+
				"`non-defaultable type` and a reader needs the word", err)
		}
	})
	t.Run("nullable defaults to null", func(t *testing.T) {
		src := `(module
  (type $inner (struct (field i32)))
  (type $t (struct (field (ref null $inner))))
  (func (export "c") (result i32)
    (local $s (ref null $t))
    (local.set $s (struct.new_default $t))
    (ref.is_null (struct.get $t 0 (local.get $s)))))`
		out := runGC(t, src)
		if len(out) != 1 || out[0].Int32() != 1 {
			t.Errorf("a nullable reference field defaulted to %v, want null", out)
		}
	})
}

// TestStructV128FieldKeepsBothHalves is the synthetic control for the case with **no corpus vector**
// — decision 0024's two-slot value as a struct field, which is `gcField.hi`'s only reason to exist.
//
// Stated as synthetic because it is: `struct.wast` and the array files declare no `v128` field, so
// the oracle is silent here by construction and this row is written from the grammar
// (`storagetype ::= valtype | packedtype`, and `valtype` includes `v128`). That silence is precisely
// why the row exists — grave #243 was filed because a two-slot value met a one-slot store and the
// arm was written as a *default*, and its consequence was not a truncated lane but the next call
// reading its arguments out of a stack left one slot deep.
//
// All four lanes are read back, for grave #239's reason: an arm that keeps only the low half passes
// any assertion that checks lanes 0 and 1, and the high half is where a missing `hi` shows up.
func TestStructV128FieldKeepsBothHalves(t *testing.T) {
	src := `(module
  (type $t (struct (field (mut v128))))
  (func (export "c") (result i32 i32 i32 i32)
    (local $s (ref null $t))
    (local.set $s (struct.new $t (v128.const i32x4 1 2 3 4)))
    (struct.set $t 0 (local.get $s) (v128.const i32x4 5 6 7 8))
    (i32x4.extract_lane 0 (struct.get $t 0 (local.get $s)))
    (i32x4.extract_lane 1 (struct.get $t 0 (local.get $s)))
    (i32x4.extract_lane 2 (struct.get $t 0 (local.get $s)))
    (i32x4.extract_lane 3 (struct.get $t 0 (local.get $s)))))`
	out := runGC(t, src)
	if len(out) != 4 {
		t.Fatalf("want four lanes, got %d: %v", len(out), out)
	}
	for i, want := range []int32{5, 6, 7, 8} {
		if got := out[i].Int32(); got != want {
			t.Errorf("lane %d = %d, want %d — a v128 field is two slots (0024), and lanes 2 "+
				"and 3 are the half a one-slot store loses", i, got, want)
		}
	}
}

// TestRefEqOnAggregatesIsPointerIdentity pins the arm `aggr.ml`'s *silence* specifies: no
// `eq_ref'` override for aggregates, so `StructRef, StructRef` falls to the base
// `let eq_ref' = ref (==)` (value.ml:127) and identity is the allocation.
//
// Four rows, and the partition is what matters rather than any one of them:
//
//   - two names for one allocation → 1. This is the row a structural/field-wise comparison fails
//     nothing on, so it is not sufficient alone;
//   - two `struct.new`s with **identical field values** → 0. This is the row that kills a field-wise
//     comparison, and it is why the two structs are built with the same contents;
//   - an aggregate against a null → 0, from the null clause;
//   - two nulls → 1, which is the clause #172's falsification M11 showed is worth **+55** vectors to
//     get *wrong*. Kept here so the board's reward for the wrong reading has a local objector.
func TestRefEqOnAggregatesIsPointerIdentity(t *testing.T) {
	src := `(module
  (type $t (struct (field i32)))
  (func (export "c") (result i32 i32 i32 i32)
    (local $a (ref null $t)) (local $b (ref null $t)) (local $c (ref null $t))
    (local.set $a (struct.new $t (i32.const 1)))
    (local.set $b (local.get $a))
    (local.set $c (struct.new $t (i32.const 1)))
    (ref.eq (local.get $a) (local.get $b))
    (ref.eq (local.get $a) (local.get $c))
    (ref.eq (local.get $a) (ref.null $t))
    (ref.eq (ref.null $t) (ref.null none))))`
	out := runGC(t, src)
	if len(out) != 4 {
		t.Fatalf("want four results, got %d: %v", len(out), out)
	}
	rows := []struct {
		name string
		want int32
	}{
		{"two names for one allocation", 1},
		{"two allocations with equal fields", 0},
		{"an allocation and a null", 0},
		{"two nulls spelled with different heaptypes", 1},
	}
	for i, r := range rows {
		if got := out[i].Int32(); got != r.want {
			t.Errorf("ref.eq %s = %d, want %d", r.name, got, r.want)
		}
	}
}

// TestRefLocalDefaultsToNull is grave #246's control: a reference local that has never been written
// reads as `ref.null`, not as a funcref to function 0.
//
// **Driven through the interpreter, never through `newFrame` directly**, which is the whole point of
// the shape — a row calling `newFrame` and inspecting `f.refs` would assert that the fix's loop
// works while nothing in the engine called it (*a control can test the helper, not the path*). Each
// row is a module that reads an unset local and answers `ref.is_null`.
//
// **Scoped to the space rather than to the case that found it.** The defect was found through a
// `funcref` local and is observable in the corpus only through `(ref null $t)`, so a row for each
// would be the sample. The domain is "reference-typed local", and these five span its shapes:
// the two MVP reftypes, an abstract GC heaptype, an indexed struct type, and an indexed function
// type — because `isRef()` is what the fix is keyed on, and a shape that answers `IsRef()` without
// being covered here is the next silent slot.
func TestRefLocalDefaultsToNull(t *testing.T) {
	rows := []struct{ name, typ string }{
		{"funcref", "funcref"},
		{"externref", "externref"},
		{"anyref", "anyref"},
		{"indexed struct type", "(ref null $s)"},
		{"indexed func type", "(ref null $f)"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			src := `(module
  (type $s (struct (field i32)))
  (type $f (func))
  (func (export "c") (result i32)
    (local $r ` + r.typ + `)
    (ref.is_null (local.get $r))))`
			out := runGC(t, src)
			if len(out) != 1 {
				t.Fatalf("want one result, got %d: %v", len(out), out)
			}
			if got := out[0].Int32(); got != 1 {
				t.Errorf("an uninitialized %s local is not null (grave #246): a reference "+
					"local defaults to ref.null, and Go's zero `ref` has Null=false, so "+
					"`make([]ref, n)` claims a funcref to function 0", r.typ)
			}
		})
	}
}

// TestRefLocalDefaultSurvivesAParameter is the row the loop above cannot supply: a frame with **both**
// a reference parameter and a reference local, checking the parameter still receives the caller's
// argument after the null-fill runs.
//
// The fill covers the whole `refs` array rather than only the declared locals, which is redundant for
// parameters — the caller overwrites them a moment later — and this row is what makes "redundant"
// rather than "wrong" a checked claim instead of a comment.
//
// **Falsified by M15, and the mutation is at the caller rather than in `newFrame`**, which is the
// point worth recording: the ordering this row guards spans two functions, so the first reading was
// that no local edit could break it and the birth requirement would have to be waived here. That
// reading was wrong. `newFrame` has no "after" — the arguments are written by `call`'s own loop
// (`call.go`) — so the mutation is a *second* fill inserted there, after the argument write, which is
// exactly the ordering mistake this row exists for. It nulls every reference argument and no other
// control in this file notices. A guard whose subject spans two functions is falsified at the seam,
// not at either end; "unfalsifiable" was a statement about where I looked.
func TestRefLocalDefaultSurvivesAParameter(t *testing.T) {
	src := `(module
  (type $s (struct (field i32)))
  (func $inner (param $p (ref null $s)) (result i32 i32)
    (local $r (ref null $s))
    (ref.is_null (local.get $p))
    (ref.is_null (local.get $r)))
  (func (export "c") (result i32 i32)
    (call $inner (struct.new $s (i32.const 1)))))`
	out := runGC(t, src)
	if len(out) != 2 {
		t.Fatalf("want two results, got %d: %v", len(out), out)
	}
	if got := out[0].Int32(); got != 0 {
		t.Errorf("a reference parameter read as null — the null-fill ran after the caller's " +
			"arguments were written, which nulls every reference argument")
	}
	if got := out[1].Int32(); got != 1 {
		t.Errorf("the uninitialized reference local is not null (grave #246)")
	}
}

// TestRung2OpcodesAgreeWithTheDecoder is TestRung1OpcodesAgreeWithTheDecoder's sibling for the six
// struct sub-opcodes, and it exists for that test's stated reason: this package holds a second copy
// of a fact `internal/binary`'s generated table already holds.
//
// The live hazard here is sharper than rung 1's. `fb 02`/`fb 03`/`fb 04` are `struct.get`,
// `struct.get_s`, `struct.get_u` — three adjacent bytes differing only in a signedness this engine
// dispatches to three separate `extNone`/`extS`/`extU` arms. Transposing `get_s` and `get_u`
// produces a module that decodes perfectly and sign-extends exactly backwards: `struct.wast`'s six
// packed read-backs all fail, which is the good case, but the bucket names a *value mismatch* and no
// opcode. And transposing `fb 00`/`fb 01` swaps `struct.new` with `struct.new_default`, which for a
// struct whose fields happen to be zero is **indistinguishable**.
//
// The immediate-count half catches the other family of table drift: `fb 00`/`fb 01` take one
// immediate and `fb 02`-`fb 05` take two, so a regenerated table that renumbered the region would
// otherwise pass the mnemonic check.
func TestRung2OpcodesAgreeWithTheDecoder(t *testing.T) {
	rows := []struct {
		op       uint32
		mnemonic string
		imms     int
	}{
		{opStructNew, "struct_new", 1},
		{opStructNewDefault, "struct_new_default", 1},
		{opStructGet, "struct_get", 2},
		{opStructGetS, "struct_get_s", 2},
		{opStructGetU, "struct_get_u", 2},
		{opStructSet, "struct_set", 2},
	}
	if len(rows) != 6 {
		t.Fatalf("rung 2 is six sub-opcodes and this table has %d rows — a row was lost or "+
			"added without the count being reconsidered", len(rows))
	}
	seen := map[uint32]string{}
	for _, r := range rows {
		if prev, dup := seen[r.op]; dup {
			t.Errorf("%#02x appears twice, as %s and as %s, so one of this package's "+
				"constants is not being checked at all", r.op, prev, r.mnemonic)
		}
		seen[r.op] = r.mnemonic
	}
	for _, r := range rows {
		got, imms, ok := binary.PrefixedOp(0xfb, r.op)
		if !ok {
			t.Errorf("the decoder's table has no fb %#02x, which this package dispatches as %s",
				r.op, r.mnemonic)
			continue
		}
		if got != r.mnemonic {
			t.Errorf("fb %#02x is %s in the decoder's table and %s here", r.op, got, r.mnemonic)
		}
		if imms != r.imms {
			t.Errorf("fb %#02x (%s) takes %d immediates in the decoder's table and %d here",
				r.op, r.mnemonic, imms, r.imms)
		}
	}
}
