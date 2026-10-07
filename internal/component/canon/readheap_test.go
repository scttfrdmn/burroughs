// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"strings"
	"testing"
)

// Witnesses for the codec's lifting side (#903) — [ReadHeap], [LoadString], [LoadStringFromRange] and
// [LoadListU8].
//
// # Why these are hand-written and not left to the differential
//
// The differential against `definitions.py` covers the **happy path** and nothing else: its fixtures are
// emitted by the model, so every string in them is valid UTF-8 at an in-range pointer with a sane
// length. That is precisely why the string lift reached a live guest heap with **none of the model's
// four traps implemented** — it sliced the heap's backing array directly, and no fixture could tell.
//
// So the traps get adversarial inputs the model would never emit. Each one is a byte sequence or an
// offset a *guest* can produce with a single word, which is the threat model: the two words a string's
// flat form carries come from the guest.

func TestLoadStringImplementsTheModelsFourTraps(t *testing.T) {
	t.Run("invalid-utf8-is-refused-not-returned", func(t *testing.T) {
		h := newHeap(256)
		// 0xff is not a legal UTF-8 byte in any position. A lone 0x80 is a continuation with no lead.
		copy(h.mem[16:], []byte{0xff, 0xfe, 0x80})
		_, err := LoadStringFromRange(h, 16, 3)
		if err == nil {
			t.Fatal("three invalid UTF-8 bytes lifted successfully; definitions.py:1388 traps on " +
				"UnicodeError, and a Go string that silently is not UTF-8 is worse than an error")
		}
		if !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("the refusal %q does not say the bytes were not UTF-8", err)
		}
	})

	t.Run("a-truncated-multibyte-sequence-is-refused", func(t *testing.T) {
		h := newHeap(256)
		// "é" is 0xc3 0xa9. Lifting only its lead byte is the realistic guest bug: a length in bytes
		// mistaken for a length in characters.
		copy(h.mem[8:], []byte{0xc3, 0xa9})
		if _, err := LoadStringFromRange(h, 8, 1); err == nil {
			t.Fatal("a truncated two-byte sequence lifted successfully")
		}
		// And the whole thing is fine, so the test is about validity and not about the fixture.
		v, err := LoadStringFromRange(h, 8, 2)
		if err != nil {
			t.Fatalf("the complete sequence was refused: %v", err)
		}
		if got, ok := v.Str(); !ok || got != "é" {
			t.Fatalf("lifted %q (ok=%v), want é", got, ok)
		}
	})

	t.Run("a-range-past-the-heap-errors-rather-than-panicking", func(t *testing.T) {
		h := newHeap(64)
		// This is the arm that used to be a slice expression. Before #903 it would panic with
		// index-out-of-range, taking down whatever goroutine the guest's call was running on.
		if _, err := LoadStringFromRange(h, 60, 32); err == nil {
			t.Fatal("a range running past the heap lifted successfully; definitions.py:1385 traps")
		}
		if _, err := LoadStringFromRange(h, 1<<20, 1); err == nil {
			t.Fatal("a pointer far past the heap lifted successfully")
		}
	})

	t.Run("a-length-past-the-models-cap-is-refused-before-any-allocation", func(t *testing.T) {
		h := newHeap(64)
		// def:1383. The point of checking the cap *before* reading is that the length is a guest's word:
		// a 4GB length must not become a 4GB read attempt on the way to being refused.
		if _, err := LoadStringFromRange(h, 0, MaxStringByteLength+1); err == nil {
			t.Fatalf("a length of %d lifted successfully; the model caps it at %d",
				MaxStringByteLength+1, MaxStringByteLength)
		}
		if _, err := LoadStringFromRange(h, 0, -1); err == nil {
			t.Fatal("a negative length lifted successfully")
		}
	})

	// The alignment trap (def:1384) is vacuous at utf8's alignment of 1 — every pointer satisfies it.
	// Asserted as vacuous rather than omitted, so that whoever adds the utf16 arm finds a statement of
	// what was true before rather than an absence they have to interpret.
	t.Run("the-alignment-trap-is-vacuous-at-utf8", func(t *testing.T) {
		h := newHeap(64)
		copy(h.mem[3:], []byte("odd"))
		if _, err := LoadStringFromRange(h, 3, 3); err != nil {
			t.Fatalf("an odd pointer was refused at utf8's 1-byte alignment: %v", err)
		}
	})
}

// TestLoadStringReadsTheHeaderTheStoreWrote is the round trip, and it is the one that would catch a
// header read that got the two words backwards — which no trap test can, because both words are
// in-range and the bytes are valid either way.
func TestLoadStringReadsTheHeaderTheStoreWrote(t *testing.T) {
	for _, s := range []string{"", "hello", "é", "a longer string with spaces", "\x00embedded"} {
		h := newHeap(1 << 12)
		h.lastAlloc = 64 // keep the bump allocator away from the header
		if err := StoreString(h, s, 0); err != nil {
			t.Fatalf("StoreString(%q): %v", s, err)
		}
		v, err := LoadString(h, 0)
		if err != nil {
			t.Fatalf("LoadString after storing %q: %v", s, err)
		}
		got, ok := v.Str()
		if !ok {
			t.Fatalf("the lifted value of %q is not a string", s)
		}
		if got != s {
			t.Fatalf("round trip gave %q, want %q — a header whose two words were swapped would show "+
				"up here and nowhere in the trap tests", got, s)
		}
	}
}

// TestLiftFlatScalarRefusesANonScalar covers the arm the differential cannot reach: every case in
// `gen/cases.json` is a well-typed value, so no fixture asks this function for a `list` or a `record`.
//
// Its conversions are NOT tested here on purpose. `liftFlat` reaches this function for every scalar, so
// `TestCodecMatchesReferenceModel` verifies them against `definitions.py` over the emitted flat values —
// a table here would be a second, weaker opinion about the same arithmetic. What is tested is the
// boundary the model has no case for.
func TestLiftFlatScalarRefusesANonScalar(t *testing.T) {
	for _, c := range []struct {
		name string
		t    Type
	}{
		{"string", Type{Kind: KindString}},
		{"list", Type{Kind: KindList, Elem: &Type{Kind: KindU8}}},
		{"variant", VariantType(Case{Name: "a"})},
		{"tuple", TupleType(Type{Kind: KindU32})},
		{"own", OwnType(0)},
		{"future", Type{Kind: KindFuture}},
		{"stream", Type{Kind: KindStream}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LiftFlatScalar(c.t, 0)
			if err == nil {
				t.Fatalf("LiftFlatScalar carried a %s; it is not a one-word value", c.name)
			}
			if !strings.Contains(err.Error(), c.t.Kind.String()) {
				t.Fatalf("the refusal %q does not name %q", err, c.t.Kind)
			}
		})
	}

	// `char` is the one scalar whose lift can fail, and it fails for the model's reason rather than
	// because it is not a scalar. Checked here so the two refusal causes are not conflated.
	if _, err := LiftFlatScalar(Type{Kind: KindChar}, 0xD800); err == nil {
		t.Fatal("a surrogate lifted as a char; definitions.py:1346 traps")
	}
	if _, err := LiftFlatScalar(Type{Kind: KindChar}, 'x'); err != nil {
		t.Fatalf("an ordinary char was refused: %v", err)
	}
}

func TestLoadListU8(t *testing.T) {
	h := newHeap(256)
	want := []byte{0, 1, 2, 250, 255}
	copy(h.mem[32:], want)

	got, err := LoadListU8(h, 32, len(want))
	if err != nil {
		t.Fatalf("LoadListU8: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("LoadListU8 gave % x, want % x", got, want)
	}

	// An empty list reads nothing and is not an error — the case a guest writing zero bytes produces.
	if got, err := LoadListU8(h, 32, 0); err != nil || len(got) != 0 {
		t.Fatalf("an empty list<u8> gave (% x, %v), want (empty, nil)", got, err)
	}

	if _, err := LoadListU8(h, 250, 32); err == nil {
		t.Fatal("a list<u8> running past the heap lifted successfully")
	}
	if _, err := LoadListU8(h, -1, 4); err == nil {
		t.Fatal("a negative pointer lifted successfully")
	}
}

// TestHeapReadBytesIsBoundsChecked covers the model heap's own implementation, because it is what the
// string lift delegates its bounds trap to — a ReadHeap that panicked instead of erroring would make
// that trap untestable against this heap, and the trap tests above would be measuring nothing.
func TestHeapReadBytesIsBoundsChecked(t *testing.T) {
	h := newHeap(32)
	if _, err := h.ReadBytes(0, 32); err != nil {
		t.Fatalf("an exactly-full read was refused: %v", err)
	}
	for _, c := range []struct{ ptr, n int }{{0, 33}, {32, 1}, {-1, 1}, {0, -1}, {1 << 30, 1 << 30}} {
		if _, err := h.ReadBytes(c.ptr, c.n); err == nil {
			t.Fatalf("ReadBytes(%d, %d) succeeded on a 32-byte heap", c.ptr, c.n)
		}
	}
}
