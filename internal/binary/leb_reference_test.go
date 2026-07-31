package binary

import (
	"errors"
	"testing"
)

// TestLEBMatchesReferenceUN is a differential test against the spec's own
// executable: ports of the reference interpreter's uN and sN, run over the whole
// space where an ordering mistake can hide.
//
// It exists because grave #36 was a defect in *composition* rather than in either
// predicate. Both of uleb's checks were correct and its comment even named the two
// malformed classes correctly — it simply tested them in the wrong order, so a byte
// that is both overlong-by-continuation and out-of-range reported "representation
// too long" where the spec says "integer too large". No unit test caught it, and
// TestLEBTaxonomy stayed green throughout, because every vector it asks about is
// one where the two conditions do not overlap. The overlap region *is* the bug.
//
// Two properties make this a control rather than a second implementation:
//
//  1. The oracle is the spec's, not mine. uN/sN are transcribed from
//     interpreter/binary/decode.ml with their `require` order preserved, which is
//     the thing under test. Order of tests is itself a claim about the spec, so the
//     claim has to be read off the spec's own code.
//  2. The domain is *derived*, not enumerated. The disagreement space is
//     structural: k all-continuation bytes followed by one arbitrary byte, for
//     every k from 0 to past the width budget, over all 256 final bytes. That
//     covers every (budget state × final byte) pair a reader can be in, so it grows
//     with the widths rather than freezing at the cases I thought of.
//
// A sampled version of this test — the four suite vectors, say — would have passed
// before the fix. That is the whole point (CLAUDE.md: scope controls to the space).
func TestLEBMatchesReferenceUN(t *testing.T) {
	for _, bits := range []uint{32, 64} {
		var checked, diffs int
		// Past the budget on purpose: k == maxBytes exercises the too-long path, and
		// k beyond it must not change the verdict.
		maxK := int(bits/7) + 3
		for k := 0; k <= maxK; k++ {
			for last := range 256 {
				in := make([]byte, 0, k+1)
				for range k {
					in = append(in, 0x80)
				}
				in = append(in, byte(last))

				checked++
				if got, want := ulebVerdict(in, bits), refUN(in, int(bits)); got != want {
					diffs++
					if diffs <= 8 {
						t.Errorf("uleb(%d) on %d×0x80 + %#02x: got %q, reference uN says %q", bits, k, last, got, want)
					}
				}

				checked++
				if got, want := slebVerdict(in, bits), refSN(in, int(bits)); got != want {
					diffs++
					if diffs <= 8 {
						t.Errorf("sleb(%d) on %d×0x80 + %#02x: got %q, reference sN says %q", bits, k, last, got, want)
					}
				}
			}
		}
		if diffs > 8 {
			t.Errorf("uleb/sleb(%d): %d disagreements with the reference in total (first 8 shown)", bits, diffs)
		}
		t.Logf("bits=%d: %d verdicts compared against the reference, %d disagreements", bits, checked, diffs)
	}
}

// TestReferencePortIsNotVacuous guards the oracle. A differential test whose
// reference agrees with everything, or which never reaches the interesting
// verdicts, is a control that cannot fail — the shape #25's data-segment test wore
// (grave #32). So assert the port actually produces all four verdicts over the
// derived space, including the two the grave was about.
//
// Without this, deleting the range check from refUN would leave
// TestLEBMatchesReferenceUN green only if the engine had the same hole — which is
// exactly the coupled-failure mode a differential test is supposed to rule out.
func TestReferencePortIsNotVacuous(t *testing.T) {
	for _, bits := range []int{32, 64} {
		seenU := map[string]int{}
		seenS := map[string]int{}
		for k := 0; k <= bits/7+3; k++ {
			for last := range 256 {
				in := make([]byte, 0, k+1)
				for range k {
					in = append(in, 0x80)
				}
				in = append(in, byte(last))
				seenU[refUN(in, bits)]++
				seenS[refSN(in, bits)]++
			}
		}
		for _, want := range []string{"ok", "too long", "too large"} {
			if seenU[want] == 0 {
				t.Errorf("refUN(%d) never returns %q over the derived space; the oracle is not exercising that verdict", bits, want)
			}
			if seenS[want] == 0 {
				t.Errorf("refSN(%d) never returns %q over the derived space; the oracle is not exercising that verdict", bits, want)
			}
		}
		t.Logf("bits=%d: refUN %v, refSN %v", bits, seenU, seenS)
	}
}

// refUN is the reference interpreter's uN, from interpreter/binary/decode.ml. The
// `require` order is load-bearing and is preserved verbatim:
//
//	let rec uN n s =
//	  require (n > 0) s (pos s) "integer representation too long";
//	  let b = byte s in
//	  require (n >= 7 || Int32.lt_u (Int32.of_int (b land 0x7f)) (Int32.shift_left 1l n))
//	    s (pos s - 1) "integer too large";
//	  let x = Int64.of_int (b land 0x7f) in
//	  if b land 0x80 = 0 then x else Int64.logor x (Int64.shift_left (uN (n - 7) s) 7)
//
// Note what the structure says: the budget is checked *before* a byte is read, and
// the range *when* it is read, independent of whether that byte continues.
func refUN(b []byte, bits int) string {
	for i, n := 0, bits; ; n -= 7 {
		if n <= 0 {
			return "too long"
		}
		if i >= len(b) {
			return "eof"
		}
		c := b[i]
		i++
		if n < 7 && int(c&0x7F) >= 1<<uint(n) {
			return "too large"
		}
		if c&0x80 == 0 {
			return "ok"
		}
	}
}

// refSN is the reference interpreter's sN, same file, same ordering property. Its
// range check is two-sided via a mask rather than a comparison, which is the signed
// case's whole difference:
//
//	let rec sN n s =
//	  require (n > 0) s (pos s) "integer representation too long";
//	  let b = byte s in
//	  let mask = (-1 lsl (n - 1)) land 0x7f in
//	  require (n >= 7 || b land mask = 0 || b land mask = mask) s (pos s - 1)
//	    "integer too large";
//	  ...
func refSN(b []byte, bits int) string {
	for i, n := 0, bits; ; n -= 7 {
		if n <= 0 {
			return "too long"
		}
		if i >= len(b) {
			return "eof"
		}
		c := int(b[i])
		i++
		mask := (-1 << uint(n-1)) & 0x7F
		if n < 7 && c&mask != 0 && c&mask != mask {
			return "too large"
		}
		if c&0x80 == 0 {
			return "ok"
		}
	}
}

// ulebVerdict and slebVerdict collapse the engine's errors onto the reference's
// vocabulary. Deliberately total: an unrecognized error becomes a distinct string
// rather than being folded into a known one, so a new error condition shows up as a
// disagreement instead of hiding inside a bucket.
func ulebVerdict(b []byte, bits uint) string {
	r := &reader{b: b}
	_, err := r.uleb(bits)
	return verdictOf(err)
}

func slebVerdict(b []byte, bits uint) string {
	r := &reader{b: b}
	_, err := r.sleb(bits)
	return verdictOf(err)
}

func verdictOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrLEBTooLong):
		return "too long"
	case errors.Is(err, ErrLEBOverflow):
		return "too large"
	case errors.Is(err, ErrTruncated):
		return "eof"
	}
	return "undeclared: " + err.Error()
}
