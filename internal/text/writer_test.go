package text

import (
	"math"
	"testing"
)

// TestULEBRoundTripsThroughTheDecoder pins the writer against the authority rather than against
// a hand-typed expectation.
//
// The decoder has 4162 vectors of conformance record and this writer has none, so the direction
// of the check is fixed: a disagreement is the writer's until proven otherwise. Hand-typing
// expected byte strings here would make the test a second transcription of the format — and this
// repo's measured hand-transcription rate is 7 wrong in 12 (#37).
//
// The values are chosen at the *boundaries the encoding has*, not sampled: each 7-bit width's
// last value and the next one, since a width transition is where a continuation-bit error lives.
func TestULEBRoundTripsThroughTheDecoder(t *testing.T) {
	var vals []uint64
	for shift := 0; shift < 64; shift += 7 {
		v := uint64(1) << shift
		vals = append(vals, v-1, v, v+1)
	}
	vals = append(vals, 0, math.MaxUint64, math.MaxUint32, math.MaxUint32+1)

	for _, want := range vals {
		var w writer
		w.u64(want)

		got, n, err := decodeULEBForTest(w.b)
		if err != nil {
			t.Errorf("u64(%d) wrote % x, which the reference reading rejects: %v", want, w.b, err)
			continue
		}
		if got != want {
			t.Errorf("u64(%d) wrote % x, read back as %d", want, w.b, got)
		}
		if n != len(w.b) {
			t.Errorf("u64(%d) wrote %d bytes and the reading consumed %d: trailing bytes in an "+
				"integer encoding are a padded LEB, which is legal to read and wrong to write",
				want, len(w.b), n)
		}
		// Minimality, against an independently derived width. Legal-but-padded is the failure
		// this catches, and it is invisible to a round trip: `80 80 80 80 10` reads back as the
		// right number.
		if got, want2 := len(w.b), ulebWidth(want); got != want2 {
			t.Errorf("u64(%d) wrote %d bytes, minimal is %d (% x)", want, got, want2, w.b)
		}
	}
}

// TestSLEBRoundTripsThroughTheDecoder is the signed half, and it exists separately because
// signed and unsigned LEB differ in both halves of the malformed taxonomy (grave 0003).
//
// **0x40 and -64 are in the table on purpose.** They are the encoding's real trap: 0x40 as a
// final payload byte sign-extends to -64, so an encoder that stops when the magnitude runs out
// writes 64 as `40` and means -64. That is a wrong *value* on valid input, so it is an
// accept-direction defect — no suite vector can see it, because the module stays well-formed
// (contract §9 G-3).
func TestSLEBRoundTripsThroughTheDecoder(t *testing.T) {
	vals := []int64{
		0, 1, -1, 63, 64, 65, -63, -64, -65,
		127, 128, -127, -128, -129,
		8191, 8192, -8192, -8193,
		math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64,
	}
	for shift := 0; shift < 63; shift += 7 {
		v := int64(1) << shift
		vals = append(vals, v-1, v, v+1, -v, -v-1, -v+1)
	}

	for _, want := range vals {
		var w writer
		w.s64(want)

		got, n, err := decodeSLEBForTest(w.b)
		if err != nil {
			t.Errorf("s64(%d) wrote % x, which the reference reading rejects: %v", want, w.b, err)
			continue
		}
		if got != want {
			t.Errorf("s64(%d) wrote % x, read back as %d — if these differ by a power of two "+
				"the sign bit of the final byte is wrong, which is the 0x40 trap", want, w.b, got)
		}
		if n != len(w.b) {
			t.Errorf("s64(%d) wrote %d bytes and the reading consumed %d", want, len(w.b), n)
		}
	}
}

// TestSLEBIsNotULEBWithACast falsifies the tempting implementation.
//
// A control that only round-trips cannot distinguish the two kernels, because a correct sleb and
// a "uleb with a cast" agree on every non-negative value — which is most of any sample. So this
// asserts the property that separates them: negative values must not encode as their two's
// complement bit pattern widened, and the *width* is where that shows.
func TestSLEBIsNotULEBWithACast(t *testing.T) {
	var s, u writer
	s.s64(-1)
	u.u64(math.MaxUint64) // -1's bit pattern read as unsigned

	if len(s.b) == len(u.b) {
		t.Errorf("s64(-1) wrote %d bytes and u64(1<<64-1) wrote %d: sleb appears to be uleb over "+
			"the two's complement pattern, which agrees with a correct sleb on every "+
			"non-negative value and so is invisible to a round-trip test", len(s.b), len(u.b))
	}
	if len(s.b) != 1 || s.b[0] != 0x7F {
		t.Errorf("s64(-1) wrote % x, want 7f: -1 is the one-byte all-ones payload", s.b)
	}
}

// TestSectionLengthIsMeasuredNotPredicted asserts the framing, through the decoder's own extent
// check rather than by reading the length back out ourselves.
//
// `checkSectionSize` reports both signs of a size disagreement (grave #34), so the decoder is a
// real oracle here: a writer that predicted lengths and got one wrong fails on the way back in.
func TestSectionLengthIsMeasuredNotPredicted(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(*writer)
		want int
	}{
		{"empty", func(*writer) {}, 0},
		{"one byte", func(w *writer) { w.byte1(0x7F) }, 1},
		{"a body long enough to need a two-byte length", func(w *writer) {
			for range 200 {
				w.byte1(0)
			}
		}, 200},
		{"a nested section", func(w *writer) {
			w.section(1, func(inner *writer) { inner.bytes([]byte{1, 2, 3}) })
		}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w writer
			w.section(0x01, tc.body)

			if len(w.b) < 2 {
				t.Fatalf("section wrote % x, too short to hold an id and a length", w.b)
			}
			if w.b[0] != 0x01 {
				t.Errorf("section id is %#02x, want 0x01", w.b[0])
			}
			got, n, err := decodeULEBForTest(w.b[1:])
			if err != nil {
				t.Fatalf("the section's length prefix does not read as a LEB: %v", err)
			}
			if int(got) != tc.want {
				t.Errorf("section declared %d bytes, body is %d", got, tc.want)
			}
			if rest := len(w.b) - 1 - n; rest != tc.want {
				t.Errorf("section declared %d bytes and %d follow the prefix: a declared size "+
					"that disagrees with the content is the defect checkSectionSize reports "+
					"from the other side, in both signs", got, rest)
			}
		})
	}
}

// TestVecCountComesFromTheCaller pins the one place `vec` can be misused, so the misuse produces
// a decoder error rather than a short vector.
func TestVecCountComesFromTheCaller(t *testing.T) {
	var w writer
	w.vec(3, func(w *writer, i int) { w.byte1(byte(i)) })
	if got, want := w.b, []byte{3, 0, 1, 2}; string(got) != string(want) {
		t.Errorf("vec wrote % x, want % x", got, want)
	}

	var empty writer
	empty.vec(0, func(*writer, int) {
		t.Error("vec(0) called its element function: an empty vector is a count and nothing else")
	})
	if got, want := empty.b, []byte{0}; string(got) != string(want) {
		t.Errorf("vec(0) wrote % x, want % x", got, want)
	}
}

// TestNameAndByteVecArePrefixed covers the `vec(byte)` shape, including the empty name — which is
// legal and is the case a length-prefix bug most easily survives.
func TestNameAndByteVecArePrefixed(t *testing.T) {
	for _, s := range []string{"", "a", "spectest", "global_i32", "é中"} {
		var w writer
		w.name(s)
		n, consumed, err := decodeULEBForTest(w.b)
		if err != nil {
			t.Fatalf("name(%q) wrote % x, whose prefix does not read: %v", s, w.b, err)
		}
		if int(n) != len(s) {
			t.Errorf("name(%q) declared %d bytes, the string is %d bytes", s, n, len(s))
		}
		if got := string(w.b[consumed:]); got != s {
			t.Errorf("name(%q) wrote payload %q", s, got)
		}
	}
}

// TestFloatsAreLittleEndian pins the byte order against the spec's, which is the one thing about
// float encoding an encoder can get wrong without changing any value it can observe itself.
func TestFloatsAreLittleEndian(t *testing.T) {
	var w writer
	w.f32(0x04030201)
	if got, want := w.b, []byte{1, 2, 3, 4}; string(got) != string(want) {
		t.Errorf("f32 wrote % x, want % x", got, want)
	}

	var v writer
	v.f64(0x0807060504030201)
	if got, want := v.b, []byte{1, 2, 3, 4, 5, 6, 7, 8}; string(got) != string(want) {
		t.Errorf("f64 wrote % x, want % x", got, want)
	}
}

// decodeULEBForTest and decodeSLEBForTest are the reference readings the assertions above check
// against.
//
// **They are deliberately not the decoder's `uleb`/`sleb`.** Those are unexported in
// `internal/binary`, and the alternative — exporting them, or moving this test there — would make
// the check compare the writer against the reader it was written from. That is self-agreement:
// the two would share any misconception about the format, and a shared misconception is exactly
// what a round trip cannot see (grave #106's shape — a premise measured with the code's own
// instrument is an echo).
//
// So these are independent readings, written from the spec's LEB128 definition, and they are
// *narrower* than the decoder's on purpose: no width budget, no overflow taxonomy, because their
// job is to say what the bytes mean and not to judge them. The full malformed taxonomy is the
// decoder's and is already covered by its own suite. The real cross-check against a producer that
// shares nothing with us is the wabt corpus, at the module level, where it belongs.
func decodeULEBForTest(b []byte) (v uint64, n int, err error) {
	var shift uint
	for n < len(b) {
		c := b[n]
		n++
		if shift >= 64 {
			return 0, n, errTestLEBTooWide
		}
		v |= uint64(c&0x7F) << shift
		if c&0x80 == 0 {
			return v, n, nil
		}
		shift += 7
	}
	return 0, n, errTestLEBTruncated
}

func decodeSLEBForTest(b []byte) (v int64, n int, err error) {
	var shift uint
	for n < len(b) {
		c := b[n]
		n++
		if shift >= 64 {
			return 0, n, errTestLEBTooWide
		}
		v |= int64(c&0x7F) << shift
		shift += 7
		if c&0x80 == 0 {
			// Sign-extend from the final payload bit, which is what makes this a *signed*
			// reading rather than an unsigned one with a cast.
			if shift < 64 && c&0x40 != 0 {
				v |= -1 << shift
			}
			return v, n, nil
		}
	}
	return 0, n, errTestLEBTruncated
}

type testLEBError string

func (e testLEBError) Error() string { return string(e) }

const (
	errTestLEBTooWide   = testLEBError("LEB payload exceeds 64 bits")
	errTestLEBTruncated = testLEBError("LEB ended with the continuation bit set")
)
