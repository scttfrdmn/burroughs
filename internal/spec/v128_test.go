package spec

import (
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestReadV128Const is decision 0024's forced question 5, harness-side: every tracked shape
// reads into the right lane count, width, and Kind, and a lane's own NaN-class spelling is
// admitted exactly where a scalar float literal already admits it.
//
// The mixed-lanes case is grounded in a real corpus vector rather than invented: exact and
// NaN-class lanes never mix within one v128.const in the tracked shapes today (measured against
// testdata/spec/simd_*.wast), but simd_f32x4_arith.wast:732 shows all four lanes as
// nan:canonical, and this is the shape that would break first if the per-lane loop only checked
// lane 0.
func TestReadV128Const(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []Val // the decoded shape; Bits/NaN compared, Kind implied by the case
	}{
		{
			name: "i8x16, sixteen lanes widened to KindI32 slots",
			src:  "(v128.const i8x16 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16)",
			want: []Val{
				{Kind: KindI32, Bits: 1},
				{Kind: KindI32, Bits: 2},
				{Kind: KindI32, Bits: 3},
				{Kind: KindI32, Bits: 4},
				{Kind: KindI32, Bits: 5},
				{Kind: KindI32, Bits: 6},
				{Kind: KindI32, Bits: 7},
				{Kind: KindI32, Bits: 8},
				{Kind: KindI32, Bits: 9},
				{Kind: KindI32, Bits: 10},
				{Kind: KindI32, Bits: 11},
				{Kind: KindI32, Bits: 12},
				{Kind: KindI32, Bits: 13},
				{Kind: KindI32, Bits: 14},
				{Kind: KindI32, Bits: 15},
				{Kind: KindI32, Bits: 16},
			},
		},
		{
			name: "i16x8, eight lanes",
			src:  "(v128.const i16x8 1 2 3 4 5 6 7 8)",
			want: []Val{
				{Kind: KindI32, Bits: 1},
				{Kind: KindI32, Bits: 2},
				{Kind: KindI32, Bits: 3},
				{Kind: KindI32, Bits: 4},
				{Kind: KindI32, Bits: 5},
				{Kind: KindI32, Bits: 6},
				{Kind: KindI32, Bits: 7},
				{Kind: KindI32, Bits: 8},
			},
		},
		{
			name: "i32x4, four lanes, one negative (two's complement pattern)",
			src:  "(v128.const i32x4 1 -1 0 4294967295)",
			want: []Val{
				{Kind: KindI32, Bits: 1},
				{Kind: KindI32, Bits: 0xffffffff},
				{Kind: KindI32, Bits: 0},
				{Kind: KindI32, Bits: 0xffffffff},
			},
		},
		{
			name: "i64x2, two lanes",
			src:  "(v128.const i64x2 1 -1)",
			want: []Val{
				{Kind: KindI64, Bits: 1}, {Kind: KindI64, Bits: 0xffffffffffffffff},
			},
		},
		{
			name: "f32x4, exact lanes",
			src:  "(v128.const f32x4 1 2 3 4)",
			want: []Val{
				{Kind: KindF32, Bits: 0x3f800000},
				{Kind: KindF32, Bits: 0x40000000},
				{Kind: KindF32, Bits: 0x40400000},
				{Kind: KindF32, Bits: 0x40800000},
			},
		},
		{
			name: "f64x2, exact lanes",
			src:  "(v128.const f64x2 1 2)",
			want: []Val{
				{Kind: KindF64, Bits: 0x3ff0000000000000}, {Kind: KindF64, Bits: 0x4000000000000000},
			},
		},
		{
			// simd_f32x4_arith.wast:732 — all four lanes nan:canonical, the sum of two infinities
			// of opposite sign. Real vector, not invented: grounding per this package's own
			// fixture-provenance discipline.
			name: "f32x4, all four lanes nan:canonical (simd_f32x4_arith.wast:732)",
			src:  "(v128.const f32x4 nan:canonical nan:canonical nan:canonical nan:canonical)",
			want: []Val{
				{Kind: KindF32, NaN: NaNCanonical},
				{Kind: KindF32, NaN: NaNCanonical},
				{Kind: KindF32, NaN: NaNCanonical},
				{Kind: KindF32, NaN: NaNCanonical},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := newParser([]byte(c.src)).parseNode()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, ok := readV128Const(n)
			if !ok {
				t.Fatalf("readV128Const(%q) returned ok=false", c.src)
			}
			if got.Kind != KindV128 {
				t.Fatalf("Kind = %s, want v128", got.Kind)
			}
			if len(got.Lanes) != len(c.want) {
				t.Fatalf("%d lanes, want %d", len(got.Lanes), len(c.want))
			}
			for i, w := range c.want {
				g := got.Lanes[i]
				if g.Kind != w.Kind || g.Bits != w.Bits || g.NaN != w.NaN {
					t.Errorf("lane %d = %s, want %s", i, g, w)
				}
			}
		})
	}
}

// TestReadV128ConstRejectsWrongLaneCount is the falsifiable half of the lane-count check: a
// shape naming N lanes with a literal count other than N must be declined (ok=false), not
// silently truncated or padded — a malformed v128.const reaching this reader is a shape the
// script grammar never emits (readV128Const's own doc comment), so under- or over-supplying
// lanes is exactly the input this check exists to catch before it ever reaches a real vector.
func TestReadV128ConstRejectsWrongLaneCount(t *testing.T) {
	cases := []string{
		"(v128.const i32x4 1 2 3)",      // one short
		"(v128.const i32x4 1 2 3 4 5)",  // one over
		"(v128.const i8x16 1 2 3)",      // far short
		"(v128.const unknownshape 1 2)", // not a tracked shape at all
	}
	for _, src := range cases {
		n, err := newParser([]byte(src)).parseNode()
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		if _, ok := readV128Const(n); ok {
			t.Errorf("readV128Const(%q) = ok, want declined (wrong lane count for its shape)", src)
		}
	}
}

// TestPackAndSliceV128LanesRoundTrip is the falsifiable control on the pack/slice pair
// (spec_test.go's packV128Lanes, value.go's sliceV128Lanes): shaped lanes pack into raw (hi, lo)
// bits and slice back out to the identical values, for every tracked shape — not just i32x4,
// since the two functions' bit-offset arithmetic is what could plausibly be wrong per width, and
// a control exercised at only one width would inherit that width's blind spot for the others
// (the scope-controls-to-the-space discipline, pointed at this pair rather than at the corpus).
func TestPackAndSliceV128LanesRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		lanes []Val
	}{
		{"i32x4", []Val{
			{Kind: KindI32, Bits: 1, LaneBits: 32},
			{Kind: KindI32, Bits: 2, LaneBits: 32},
			{Kind: KindI32, Bits: 3, LaneBits: 32},
			{Kind: KindI32, Bits: 4, LaneBits: 32},
		}},
		{"i64x2", []Val{
			{Kind: KindI64, Bits: 0x1122334455667788, LaneBits: 64}, {Kind: KindI64, Bits: 0xffeeddccbbaa9988, LaneBits: 64},
		}},
		{"i16x8, narrow lanes exercise the sub-64-bit offsets", []Val{
			{Kind: KindI32, Bits: 0x0001, LaneBits: 16},
			{Kind: KindI32, Bits: 0x0002, LaneBits: 16},
			{Kind: KindI32, Bits: 0x0003, LaneBits: 16},
			{Kind: KindI32, Bits: 0x0004, LaneBits: 16},
			{Kind: KindI32, Bits: 0x0005, LaneBits: 16},
			{Kind: KindI32, Bits: 0x0006, LaneBits: 16},
			{Kind: KindI32, Bits: 0x0007, LaneBits: 16},
			{Kind: KindI32, Bits: 0x0008, LaneBits: 16},
		}},
		{"i8x16, narrowest lanes, sixteen offsets including the hi/lo boundary crossing at lane 8", []Val{
			{Kind: KindI32, Bits: 1, LaneBits: 8},
			{Kind: KindI32, Bits: 2, LaneBits: 8},
			{Kind: KindI32, Bits: 3, LaneBits: 8},
			{Kind: KindI32, Bits: 4, LaneBits: 8},
			{Kind: KindI32, Bits: 5, LaneBits: 8},
			{Kind: KindI32, Bits: 6, LaneBits: 8},
			{Kind: KindI32, Bits: 7, LaneBits: 8},
			{Kind: KindI32, Bits: 8, LaneBits: 8},
			{Kind: KindI32, Bits: 9, LaneBits: 8},
			{Kind: KindI32, Bits: 10, LaneBits: 8},
			{Kind: KindI32, Bits: 11, LaneBits: 8},
			{Kind: KindI32, Bits: 12, LaneBits: 8},
			{Kind: KindI32, Bits: 13, LaneBits: 8},
			{Kind: KindI32, Bits: 14, LaneBits: 8},
			{Kind: KindI32, Bits: 15, LaneBits: 8},
			{Kind: KindI32, Bits: 16, LaneBits: 8},
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hi, lo := packV128Lanes(c.lanes)
			// sliceV128Lanes's shape argument only ever reads .Kind and .LaneBits (never
			// .Bits — this function is producing the shape's own bits, so the shape
			// parameter's Bits values are checked here to be irrelevant), matching its own
			// doc comment.
			got := sliceV128Lanes(hi, lo, c.lanes)
			if len(got) != len(c.lanes) {
				t.Fatalf("sliceV128Lanes returned %d lanes, want %d", len(got), len(c.lanes))
			}
			for i, want := range c.lanes {
				if got[i].Kind != want.Kind || got[i].Bits != want.Bits {
					t.Errorf("lane %d: got %s, want %s (hi=%#x lo=%#x)", i, got[i], want, hi, lo)
				}
			}
		})
	}
}

// TestSliceV128LanesBitOffsetIsFalsifiable is the mutation half of the round-trip control above,
// isolated to sliceV128Lanes alone (mirroring TestRefExternIdentitySurvivesCorruption's own
// "test the unit, not just the pipeline" reasoning: a compensating error elsewhere could mask a
// defect in this one function on the corpus-level test, which is the correlated-errors risk).
//
// The mutation lives in this test only, never in production code, per this project's "watched
// die, then reverted" discipline: shift lane 1's expected position by one lane width and confirm
// the comparison that should catch a wrong offset actually does.
func TestSliceV128LanesBitOffsetIsFalsifiable(t *testing.T) {
	shape := []Val{
		{Kind: KindI32, LaneBits: 32},
		{Kind: KindI32, LaneBits: 32},
		{Kind: KindI32, LaneBits: 32},
		{Kind: KindI32, LaneBits: 32},
	}
	// hi=0, lo packs lane0=1, lane1=2, lane2=3, lane3=4 at 32-bit offsets 0/32/64/96.
	lo := uint64(2)<<32 | uint64(1)
	hi := uint64(4)<<32 | uint64(3)
	got := sliceV128Lanes(hi, lo, shape)
	want := []uint64{1, 2, 3, 4}
	for i, w := range want {
		if got[i].Bits != w {
			t.Fatalf("lane %d = %#x, want %#x — the positive case is broken before any mutation", i, got[i].Bits, w)
		}
	}
	// The mutation: compare lane 1 against lane 2's value. A control whose per-lane check
	// cannot distinguish adjacent lanes is not testing an offset at all.
	if got[1].Bits == want[2] {
		t.Fatalf("lane 1 (%#x) accidentally equals lane 2's expected value (%#x); this mutation's "+
			"premise (adjacent lanes carry different values) does not hold, so it cannot demonstrate anything",
			got[1].Bits, want[2])
	}
}

// TestV128MatchesDecomposesPerLane is Val.Matches's KindV128 branch: a whole-vector comparison
// reduces to N independent scalar comparisons, including a NaN-class lane matching a real NaN of
// that class and an exact lane requiring bit-for-bit equality — the same two rules the scalar
// float branch already enforces, reused rather than reimplemented (Lanes's own doc comment).
func TestV128MatchesDecomposesPerLane(t *testing.T) {
	// want: lane0 nan:canonical, lanes 1-3 exact 1.0 — mirrors a real corpus mixed-expectation
	// shape (simd_f32x4_arith.wast's own file mixes NaN-class and exact lanes across different
	// vectors; this pins the case within one v128.const, decision 0024's own stated reason
	// per-lane decomposition exists at all).
	want := Val{Kind: KindV128, Lanes: []Val{
		{Kind: KindF32, NaN: NaNCanonical, LaneBits: 32},
		{Kind: KindF32, Bits: 0x3f800000, LaneBits: 32}, // 1.0
		{Kind: KindF32, Bits: 0x3f800000, LaneBits: 32},
		{Kind: KindF32, Bits: 0x3f800000, LaneBits: 32},
	}}

	t.Run("a real canonical NaN in lane 0 and exact 1.0 elsewhere matches", func(t *testing.T) {
		// A canonical NaN pattern: exponent all ones, quiet bit set, payload zero, sign either.
		got := Val{Kind: KindV128, Hi: uint64(0x3f800000)<<32 | uint64(0x3f800000), Bits: uint64(0x3f800000)<<32 | uint64(0x7fc00000)}
		if !want.Matches(got) {
			t.Errorf("want.Matches(got) = false, want true")
		}
	})

	t.Run("a signalling NaN in lane 0 (quiet bit clear) does not satisfy nan:canonical", func(t *testing.T) {
		got := Val{Kind: KindV128, Hi: uint64(0x3f800000)<<32 | uint64(0x3f800000), Bits: uint64(0x3f800000)<<32 | uint64(0x7f800001)}
		if want.Matches(got) {
			t.Errorf("want.Matches(got) = true, want false (lane 0 is signalling, not quiet)")
		}
	})

	t.Run("lane 1 off by one bit fails the whole vector", func(t *testing.T) {
		got := Val{Kind: KindV128, Hi: uint64(0x3f800000)<<32 | uint64(0x3f800000), Bits: uint64(0x3f800001)<<32 | uint64(0x7fc00000)}
		if want.Matches(got) {
			t.Errorf("want.Matches(got) = true, want false (lane 1 is 0x3f800001, not 0x3f800000)")
		}
	})

	t.Run("a Kind mismatch on the whole Val is declined before any lane is read", func(t *testing.T) {
		got := Val{Kind: KindI32, Bits: 0}
		if want.Matches(got) {
			t.Errorf("want.Matches(got) = true, want false (got is not even a v128)")
		}
	})
}

// TestV128IsPassableRejectsNaNLane confirms isPassable's v128 arm — a NaN-class lane makes a
// whole v128 unpassable as a call argument, even when every other lane is concrete, matching the
// measured corpus fact the arm's own doc comment cites (0 argument-position occurrences of
// nan:canonical/nan:arithmetic inside any v128.const across testdata/spec/simd_*.wast).
func TestV128IsPassableRejectsNaNLane(t *testing.T) {
	allExact := Val{Kind: KindV128, Lanes: []Val{
		{Kind: KindI32, Bits: 1}, {Kind: KindI32, Bits: 2}, {Kind: KindI32, Bits: 3}, {Kind: KindI32, Bits: 4},
	}}
	if !allExact.isPassable() {
		t.Errorf("an all-exact v128 is not passable, want true")
	}

	oneNaN := Val{Kind: KindV128, Lanes: []Val{
		{Kind: KindF32, Bits: 0x3f800000},
		{Kind: KindF32, NaN: NaNCanonical},
		{Kind: KindF32, Bits: 0x3f800000},
		{Kind: KindF32, Bits: 0x3f800000},
	}}
	if oneNaN.isPassable() {
		t.Errorf("a v128 with one NaN-class lane is passable, want false")
	}
}

// TestV128StringPrintsEveryLaneOrTheRawHalves confirms Val.String's KindV128 branch, added
// because a mismatch message that cannot say what a v128 actually held is a diagnostic that
// hides the defect it exists to name — found in this PR's own investigation, where the generic
// `int64(v.Bits)` fallback printed a v128 result as a signed 64-bit reading of its *low* half
// alone, discarding the high half and every float/NaN reading entirely (see grave #223's own
// investigation, which needed the fix to read the mismatch at all).
func TestV128StringPrintsEveryLaneOrTheRawHalves(t *testing.T) {
	shaped := Val{Kind: KindV128, Lanes: []Val{
		{Kind: KindF32, Bits: 0x3f800000, LaneBits: 32},
		{Kind: KindF32, NaN: NaNCanonical, LaneBits: 32},
	}}
	got := shaped.String()
	want := "v128 [f32 0x3f800000 (1), f32 nan:canonical]"
	if got != want {
		t.Errorf("shaped v128 String() = %q, want %q", got, want)
	}

	raw := Val{Kind: KindV128, Hi: 0x1122334455667788, Bits: 0x8877665544332211}
	got = raw.String()
	want = "v128 hi=0x1122334455667788 lo=0x8877665544332211"
	if got != want {
		t.Errorf("raw v128 String() = %q, want %q", got, want)
	}
}

// TestV128BoundaryRoundTripsAgainstTheCorpus is this widening's own version of
// TestReferenceBoundaryRoundTrips (refboundary_test.go): a v128 argument and a v128 expectation,
// read from a real corpus file's own vectors, round through toInterpValue/fromInterpValue/Matches
// end to end. This is the harness widening's whole point stated as a test — decision 0024's
// forced question 5 existed because these vectors could not be asked at all before it; this
// confirms they now can.
//
// simd_f32x4_arith.wast is chosen because its own opening lines (f32x4.add/sub/mul/div/neg/sqrt)
// are already landed on the interpreter side (TestSIMDFloatArithmetic, internal/interp), so a
// fail here is unambiguously the harness's own crossing rather than a missing engine arm —
// exactly the discriminating property TestValueMismatchBucketIsEmptyAndSaysWhoWroteAnyRow's
// family exists to establish elsewhere.
func TestV128BoundaryRoundTripsAgainstTheCorpus(t *testing.T) {
	requireSuite(t)

	s, err := ParseFile(filepath.Join(suiteDir, "simd_f32x4_arith.wast"))
	if err != nil {
		t.Fatalf("parse simd_f32x4_arith.wast: %v", err)
	}
	allOn := allFeaturesOn(t)
	d := &binary.Decoder{Features: allOn}
	e := engine()
	e.Decode = func(image []byte) error {
		_, err := d.DecodeModule(image)
		return err
	}
	// Gates must reach instantiation too, per instantiateWith's own doc comment and the memory64
	// grave it records — decoding under all-on and instantiating under the default engine()'s
	// empty Features would decline the SIMD-gated vectors at the instantiation boundary instead
	// of running them.
	e.InstantiateLinked = func(c Command, reg Registry) (Instance, Stratum, error) {
		return instantiateWith(allOn, c, reg)
	}
	// The validator decodes too, so it takes the lane's gates — see validateWith.
	e.Validate = func(c Command) (Stratum, error) { return validateWith(allOn, c) }
	r := s.RunGated(e)

	for _, fs := range r.Buckets {
		for _, f := range fs {
			// A decline is not a verdict about the v128 value boundary: this file's 16
			// `assert_invalid` vectors name SIMD opcodes slice 1 of the validator (#9) does not
			// type, and they are fails in their own bucket on the board. Filtered on the flag
			// rather than on their lines, for refboundary_test.go's reason.
			if f.Declined {
				continue
			}
			t.Errorf("simd_f32x4_arith.wast:%d unexpected fail: want %s, got %s", f.Line, f.Expect, f.Got)
		}
	}
	if r.Pass == 0 {
		t.Fatalf("0 passes on simd_f32x4_arith.wast under all gates on — the vacuity check: " +
			"this file's own vectors, the ones the harness widening exists to make askable, did not run at all")
	}
	t.Logf("simd_f32x4_arith.wast under all gates on: %d pass, %d fail, %d unsupported, %d gated",
		r.Pass, r.Fail, r.Unsupported, r.Gated)
}
