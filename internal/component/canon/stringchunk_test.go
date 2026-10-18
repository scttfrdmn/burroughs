// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// The string lift and lower stream in `StringChunk` pieces (contract §3 SP-1, ADR 0104), so validation is
// incremental and a rune can straddle a chunk boundary. These witnesses hold the streamed form to the
// whole-value one it replaced. That form's oracle is `utf8.Valid`, and the differential still covers the
// model's fixtures, all of which are a single chunk.

// TestUTF8JoinAgreesWithWholeValidation: for any bytes split at any points, `utf8Join` answers what
// `utf8.Valid` answers over the whole. The inputs mix runes of every width with stray continuation bytes,
// lone leads and bytes that are never legal, because a split is only interesting next to one of those.
//
// **It caught the joiner's first draft**, which judged a join as soon as a piece's leading continuations
// ran out, even when the piece was nothing but those and the rune was still unfinished. The input was a
// four-byte rune split one byte per piece, valid whole and refused in pieces. At 64 KiB pieces a production
// split never gives that shape, and this test's one-to-six-byte pieces exist to give it.
//
// NEUTER (run): drop the join's own validation of the held tail plus the next piece's leading continuation
// bytes, and the joined bytes are never judged. It failed at case 7, where an illegal 0xff byte, held back as
// a piece's tail, was accepted.
func TestUTF8JoinAgreesWithWholeValidation(t *testing.T) {
	alphabet := [][]byte{
		[]byte("a"), []byte("é"), []byte("€"), []byte("𝄞"),
		{0x80},
		{0xbf},
		{0xc3},
		{0xe2, 0x82},
		{0xf0, 0x9d},
		{0xff},
		{0xed, 0xa0, 0x80},
	}
	rng := rand.New(rand.NewPCG(7, 7))
	for c := range 20000 {
		var b []byte
		for range rng.IntN(24) {
			// Mostly valid runes, so that whole-valid inputs are common and not only invalid ones.
			if rng.IntN(4) == 0 {
				b = append(b, alphabet[4+rng.IntN(len(alphabet)-4)]...)
			} else {
				b = append(b, alphabet[rng.IntN(4)]...)
			}
		}
		var join utf8Join
		got := true
		for off := 0; off < len(b) || off == 0; {
			n := min(1+rng.IntN(6), len(b)-off)
			if !join.valid(b[off:off+n], off+n == len(b)) {
				got = false
				break
			}
			off += n
			if len(b) == 0 {
				break
			}
		}
		if want := utf8.Valid(b); got != want {
			t.Fatalf("case %d: %x split into pieces validated %v, whole validated %v", c, b, got, want)
		}
	}
}

// TestAMultiChunkStringRoundTrips: a string longer than one chunk, with a four-byte rune straddling each
// chunk boundary, stores and lifts back unchanged, and the same bytes with one stray continuation are refused.
func TestAMultiChunkStringRoundTrips(t *testing.T) {
	// "𝄞" is four bytes, so starting it two before each boundary splits it two and two.
	unit := strings.Repeat("a", StringChunk-2) + "𝄞" + strings.Repeat("b", StringChunk-2)
	s := unit + unit + "z"
	h := newHeap(4 * len(s))
	ptr, n, err := StoreStringIntoRange(h, s)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if n != len(s) {
		t.Fatalf("store returned length %d, want %d", n, len(s))
	}
	v, err := LoadStringFromRange(h, ptr, n)
	if err != nil {
		t.Fatalf("lift: %v", err)
	}
	if got, _ := v.Str(); got != s {
		t.Fatalf("the lifted string differs from the stored one (lengths %d and %d)", len(got), len(s))
	}
	// A stray continuation in place of the 'a' just after the third chunk boundary. The first draft put it
	// three chunks in, which is inside the second straddling rune: a continuation replacing a continuation,
	// which is still valid, and the test failed on its own input.
	h.mem[ptr+2*StringChunk+1] = 0x80
	if _, err := LoadStringFromRange(h, ptr, n); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("a stray continuation byte past the first chunk lifted with error %v; want a UTF-8 refusal", err)
	}
}
