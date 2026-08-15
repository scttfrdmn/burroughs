// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"math"
	"slices"
	"testing"
)

// spellableValues returns, for every Kind this package's vocabulary names, the values whose
// spelling the `i32:42` syntax is supposed to carry.
//
// **Derived from kindNames, not enumerated beside it.** A Kind added to the vocabulary without a
// case here fails the loop below rather than being quietly untested — which is the difference
// between a domain and a list, and the reason this helper exists instead of a table of literals.
// The samples per kind are chosen where the encoding is interesting: the sign of zero, the extremes
// of the range, and NaN with and without a payload.
func spellableValues(t *testing.T) map[Kind][]Value {
	t.Helper()

	f32 := func(bits uint32) Value { return Value{typ: Type{kind: KindF32}, bits: uint64(bits)} }
	f64bits := func(bits uint64) Value { return Value{typ: Type{kind: KindF64}, bits: bits} }

	out := map[Kind][]Value{
		KindI32: {I32(0), I32(1), I32(-1), I32(math.MinInt32), I32(math.MaxInt32)},
		KindI64: {I64(0), I64(1), I64(-1), I64(math.MinInt64), I64(math.MaxInt64)},
		KindF32: {
			F32(0), F32(float32(math.Copysign(0, -1))), F32(1.5), F32(-1e30),
			F32(float32(math.Inf(1))), F32(float32(math.Inf(-1))),
			f32(0x7fc00000), // canonical quiet NaN
			f32(0xffc00000), // negative quiet NaN
			f32(0x7f800001), // a signalling NaN: payload 1, and the one a float64 would quiet
			f32(0x7fa00000), // nan:0x200000, the spec corpus's own arithmetic-NaN payload
		},
		KindF64: {
			F64(0), F64(math.Copysign(0, -1)), F64(1.5), F64(-1e300),
			F64(math.Inf(1)), F64(math.Inf(-1)),
			f64bits(0x7ff8000000000000),
			f64bits(0xfff8000000000000),
			f64bits(0x7ff0000000000001),
			f64bits(0x7ff4000000000000),
		},
		KindV128: {V128(0, 0), V128(math.MaxUint64, math.MaxUint64), V128(0xdeadbeef, 1)},
	}

	for k := range kindNames {
		if !k.IsRef() || k == KindTypedRef {
			continue
		}
		typ, ok := AbstractRefType(k, true)
		if !ok {
			t.Fatalf("AbstractRefType(%v, true) refused an abstract reference kind", k)
		}
		v, ok := NullRef(typ)
		if !ok {
			t.Fatalf("NullRef(%v) refused a nullable reference type", typ)
		}
		out[k] = append(out[k], v)
	}
	out[KindExternRef] = append(out[KindExternRef], ExternRef(0), ExternRef(7), ExternRef(math.MaxUint32))

	// The coverage claim this helper makes about itself: every named Kind has samples, except the
	// one documented as unspellable. Without this the helper could silently stop covering a kind and
	// the round-trip test below would keep passing on the rest.
	for k := range kindNames {
		if k == KindTypedRef {
			continue
		}
		if len(out[k]) == 0 {
			t.Fatalf("Kind %v is in the vocabulary and has no sample value: the round trip is "+
				"untested for it", k)
		}
	}
	return out
}

// TestValueRoundTripsThroughItsOwnSpelling is the property Value.String and ParseValue jointly
// claim: what the CLI prints, the CLI reads.
//
// The comparison is `==` on the whole struct, deliberately — not on the accessor a kind happens to
// use — so a round trip that preserved the payload but lost the host discriminator, or the v128 high
// word, or nullability, fails here rather than in whichever consumer noticed first.
func TestValueRoundTripsThroughItsOwnSpelling(t *testing.T) {
	values := spellableValues(t)
	kinds := make([]Kind, 0, len(values))
	for k := range values {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)

	n := 0
	for _, k := range kinds {
		for _, want := range values[k] {
			s := want.String()
			got, err := ParseValue(s)
			if err != nil {
				t.Errorf("ParseValue(%q) (printed from %v): %v", s, k, err)
				continue
			}
			if got != want {
				t.Errorf("round trip of %v through %q: got %#v, want %#v", k, s, got, want)
				continue
			}
			if again := got.String(); again != s {
				t.Errorf("re-printing %q gave %q", s, again)
			}
			n++
		}
	}

	// A vacuity floor, because a helper that returned an empty map would make every assertion above
	// unexecuted and this test green. The number is the sample count, not a guess at it.
	// 5 i32 + 5 i64 + 10 f32 + 10 f64 + 3 v128 + 12 null references + 3 externrefs.
	if want := 5 + 5 + 10 + 10 + 3 + 12 + 3; n != want {
		t.Errorf("round-tripped %d values, expected %d — the sample set moved, so the floor is "+
			"measuring history rather than the samples", n, want)
	}
}

// TestTypedRefIsTheOneUnspellableKind pins the scope statement Value.String makes: the argument
// syntax names abstract reference types and not indexed ones.
//
// **Asserted, not assumed.** "This syntax cannot express X" is a claim about the parser, and the way
// it fails silently is by *starting* to express X — at which point the round-trip test above still
// passes (it never asks) while a user's `null:(ref null 3)` is accepted with a type nobody checked.
func TestTypedRefIsTheOneUnspellableKind(t *testing.T) {
	v, ok := NullRef(TypedRefType(3, true))
	if !ok {
		t.Fatal("NullRef refused a nullable typed reference")
	}
	s := v.String()
	if _, err := ParseValue(s); err == nil {
		t.Fatalf("ParseValue(%q) succeeded: a typed reference is now spellable, so it belongs in "+
			"the round-trip domain rather than in this exception", s)
	}

	// And the exception is exactly one Kind wide: everything else in the vocabulary parses.
	for k := range kindNames {
		if k == KindTypedRef {
			continue
		}
		var probe Value
		switch {
		case !k.IsRef():
			typ, _ := NumberType(k)
			probe = Value{typ: typ}
		default:
			typ, _ := AbstractRefType(k, true)
			probe, _ = NullRef(typ)
		}
		if _, err := ParseValue(probe.String()); err != nil {
			t.Errorf("%v prints %q, which ParseValue rejects: %v", k, probe.String(), err)
		}
	}
}

// TestFloatSpellingPreservesNaNPayloads is the control on the defect formatFloat's comment records:
// a float64 intermediate quiets an f32's signalling NaN, so `nan:0x200000` printed as
// `nan:0x600000` under a comment promising preservation.
//
// It sweeps every single-bit payload rather than the one value that caught it, because the specimen
// was one payload and the class is all of them — a fix verified only against its own reproducer is
// the shape this repo calls protection by coincidence.
func TestFloatSpellingPreservesNaNPayloads(t *testing.T) {
	for bit := range 23 {
		bits := uint32(0x7f800000) | uint32(1)<<bit
		if bits&0x007fffff == 0 {
			continue // the exponent's own bit: that value is an infinity, not a NaN
		}
		v := Value{typ: Type{kind: KindF32}, bits: uint64(bits)}
		got, err := ParseValue(v.String())
		if err != nil {
			t.Fatalf("ParseValue(%q): %v", v.String(), err)
		}
		if uint32(got.bits) != bits {
			t.Errorf("f32 NaN %#08x printed %q and read back %#08x", bits, v.String(), uint32(got.bits))
		}
	}
	for bit := range 52 {
		bits := uint64(0x7ff0000000000000) | uint64(1)<<bit
		v := Value{typ: Type{kind: KindF64}, bits: bits}
		got, err := ParseValue(v.String())
		if err != nil {
			t.Fatalf("ParseValue(%q): %v", v.String(), err)
		}
		if got.bits != bits {
			t.Errorf("f64 NaN %#016x printed %q and read back %#016x", bits, v.String(), got.bits)
		}
	}

	// The specimen itself, by name, so the grave's own reproducer stays in the file: an f32 holding
	// nan:0x200000 must not acquire the quiet bit by being printed.
	v := Value{typ: Type{kind: KindF32}, bits: 0x7fa00000}
	if s := v.String(); s != "f32:nan:0x200000" {
		t.Errorf("the specimen printed %q, want f32:nan:0x200000", s)
	}
}

// TestKindOrderingIsTheRefPartition pins the property Kind.IsRef derives instead of declaring: the
// reference kinds are a contiguous run ending at KindTypedRef.
//
// Checked against a **second mechanism** — the conversion table's own membership, which is built
// from `internal/binary`'s exported heaptype constants — rather than against a list written here. A
// partition test that enumerated its own answer would agree with itself.
func TestKindOrderingIsTheRefPartition(t *testing.T) {
	for k := range kindNames {
		_, isHeap := heapKindBytes[k]
		wantRef := isHeap || k == KindTypedRef
		if got := k.IsRef(); got != wantRef {
			t.Errorf("%v.IsRef() = %v; the conversion tables say ref = %v", k, got, wantRef)
		}
		if _, isNum := numericValTypes[k]; isNum == wantRef {
			t.Errorf("%v is in both conversion tables, or in neither", k)
		}
	}
	if KindNone.IsRef() {
		t.Error("the zero Kind reports itself a reference type")
	}

	// Contiguity, which is what makes the >= <= comparison legitimate: nothing between the first
	// reference kind and KindTypedRef is a non-reference.
	for k := KindFuncRef; k <= KindTypedRef; k++ {
		if _, named := kindNames[k]; !named {
			t.Errorf("Kind(%d) falls inside the reference range and has no name: the range now "+
				"covers a kind the vocabulary does not", uint8(k))
		}
	}
	if _, named := kindNames[KindTypedRef+1]; named {
		t.Error("a named Kind sits past KindTypedRef, so it is not the end of the reference run " +
			"and IsRef's upper bound is wrong")
	}
	if want := 12 + 1; int(KindTypedRef-KindFuncRef+1) != want {
		t.Errorf("the reference run is %d kinds wide, want %d (twelve abstract forms plus the "+
			"indexed one)", KindTypedRef-KindFuncRef+1, want)
	}
}

// TestPublicFloatConstructorsAreBitExact asks whether a NaN payload survives the *public* float
// path — `F32`/`F64` in, `Float32`/`Float64` out — which is a different question from whether
// Value.String spells one correctly, and the one a host actually depends on.
//
// **It is asked because the payload is part of the value here.** `nan` and `nan:0x200000` are
// different arguments to this corpus, and the public constructors take a `float32`/`float64` rather
// than bits, so the only way a host can pass a specific payload is `F32(math.Float32frombits(b))`.
// That route is bit-exact in Go on every architecture this project builds for, but it is exact by
// *nothing anyone wrote down* — no arithmetic touches the value, so nothing quiets it — and the
// previous grave on this axis (Value.String widening an f32 through float64) was the same
// assumption held one layer up and wrong. So the assumption is now a test rather than a habit.
//
// The domain is derived, not listed: every single-bit payload in both widths, both signs, plus the
// signalling specimen the last grave was found on.
func TestPublicFloatConstructorsAreBitExact(t *testing.T) {
	const (
		f32Quiet = uint32(0x00400000)
		f64Quiet = uint64(0x0008000000000000)
	)

	n32 := 0
	for bit := range 23 {
		for _, sign := range []uint32{0, 0x80000000} {
			// A NaN needs a non-zero mantissa, so each case is one payload bit *plus* the quiet bit
			// for the arms that would otherwise be an infinity — and the bare signalling payload on
			// its own, which is the case that quieting corrupts.
			for _, payload := range []uint32{1 << bit, 1<<bit | f32Quiet} {
				if payload == 0 {
					continue
				}
				want := sign | 0x7f800000 | payload
				got := math.Float32bits(F32(math.Float32frombits(want)).Float32())
				if got != want {
					t.Errorf("F32/Float32 turned %#08x into %#08x: the public float path is not "+
						"payload-exact, so a host cannot pass a signalling NaN and this API needs "+
						"bits-level constructors", want, got)
				}
				n32++
			}
		}
	}

	n64 := 0
	for bit := range 52 {
		for _, sign := range []uint64{0, 0x8000000000000000} {
			for _, payload := range []uint64{1 << bit, 1<<bit | f64Quiet} {
				want := sign | 0x7ff0000000000000 | payload
				got := math.Float64bits(F64(math.Float64frombits(want)).Float64())
				if got != want {
					t.Errorf("F64/Float64 turned %#016x into %#016x: the public float path is not "+
						"payload-exact", want, got)
				}
				n64++
			}
		}
	}

	// The specimen from the Value.String grave, asserted by name so the fix's own witness travels
	// with the property rather than only with the spelling.
	if got := math.Float32bits(F32(math.Float32frombits(0x7fa00000)).Float32()); got != 0x7fa00000 {
		t.Errorf("the signalling NaN 0x7fa00000 came back as %#08x through F32/Float32", got)
	}

	// The vacuity floor: a loop that ran zero times would agree with everything.
	if want := 23 * 2 * 2; n32 != want {
		t.Errorf("swept %d f32 payloads, want %d", n32, want)
	}
	if want := 52 * 2 * 2; n64 != want {
		t.Errorf("swept %d f64 payloads, want %d", n64, want)
	}
}
