// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// Witnesses for the list lifting side: [ListByteLength]'s overflow guard and [LoadList]'s framing.
//
// The framing's *correctness* against the model is the differential's job — `LoadList` reuses `size` and
// `alignment`, which every list case in `gen/cases.json` exercises through `store`/`load`. What these
// tests own is what no model-emitted fixture can carry: a count the model's own Python integers could
// never overflow, a misaligned pointer, and a span that runs off the end of memory.

// countingReadHeap records which [ReadHeap] method a caller reached, so a test can assert that a framing
// check did not copy. It delegates rather than reimplementing, so the bounds condition under test is the
// real one.
type countingReadHeap struct {
	inner  ReadHeap
	reads  int
	checks int
	bytes  int
}

func (c *countingReadHeap) ReadBytes(ptr, n int) ([]byte, error) {
	c.reads++
	b, err := c.inner.ReadBytes(ptr, n)
	c.bytes += len(b)
	return b, err
}

func (c *countingReadHeap) CheckRange(ptr, n int) error {
	c.checks++
	return c.inner.CheckRange(ptr, n)
}

func TestListByteLengthCannotOverflow(t *testing.T) {
	// The ordinary cases, so the guard is not the only thing tested.
	for _, c := range []struct{ count, elem, want int }{
		{0, 4, 0},
		{1, 1, 1},
		{3, 4, 12},
		{1000, 8, 8000},
		// A zero-size element is a legitimate shape — an empty tuple's — and must not divide by zero.
		{1 << 20, 0, 0},
	} {
		got, err := ListByteLength(c.count, c.elem)
		if err != nil {
			t.Fatalf("ListByteLength(%d, %d): %v", c.count, c.elem, err)
		}
		if got != c.want {
			t.Errorf("ListByteLength(%d, %d) = %d, want %d", c.count, c.elem, got, c.want)
		}
	}

	// Negatives are refused rather than converted: a count is a guest word on the lifting side, and
	// `int(negative)` reaching the multiplication would produce a negative length a bounds check reads
	// as "nothing to read". Asserted **before** the width guard below, because unlike the bound and the
	// overflow arms a negative is expressible at any int width.
	if _, err := ListByteLength(-1, 4); err == nil {
		t.Error("a negative count was accepted")
	}
	if _, err := ListByteLength(4, -1); err == nil {
		t.Error("a negative element size was accepted")
	}

	// **Everything past this point needs an `int` wider than 32 bits to express at all**, and until this
	// was noticed the whole function would not even compile on a 32-bit host: `int(ReallocI32Max)` and
	// `1 << 32` are **constant** conversions, which Go checks whether or not their branch runs. So the
	// witness for #922 — whose stated subject was typing `ReallocI32Max` so the engine builds on
	// `GOARCH=386` — was itself 64-bit-only. The engine was portable; the test that said so was not.
	//
	// Found by running `GOARCH=386 go vet ./internal/component/...` in the slice after. CI has no 32-bit
	// arm, so nothing else was ever going to say it.
	//
	// The repair is a width guard plus **runtime** conversions through `wide`, and the arms are skipped
	// rather than weakened on a narrow host, because there they are not weaker but meaningless: no `int`
	// can reach `ReallocI32Max`, so "one past the bound" truncates to a small number the guard rightly
	// accepts, and an assertion that it is refused would be a false failure.
	if strconv.IntSize < 64 {
		t.Logf("int is %d bits here, so no int can reach ReallocI32Max (%d): the bound and the overflow "+
			"arms are unreachable on this platform, not merely untested", strconv.IntSize, ReallocI32Max)
		return
	}
	// Every width-dependent value below goes through `wide`, which makes its conversion a **runtime**
	// one. That is the whole repair: `int(ReallocI32Max)` is a compile-time conversion of a typed
	// constant and fails to build on a narrow host; `wide(ReallocI32Max)` converts a function argument.
	wide := func(v uint64) int { return int(v) }

	// Exactly at the bound is allowed; one past is not. The pair is what says the comparison is not
	// off by one in either direction.
	if _, err := ListByteLength(wide(ReallocI32Max), 1); err != nil {
		t.Errorf("a byte length of exactly ReallocI32Max was refused: %v", err)
	}
	if _, err := ListByteLength(wide(ReallocI32Max+1), 1); err == nil {
		t.Error("a byte length one past ReallocI32Max was accepted")
	}

	// **The overflow cases.** A count that overflows 32 bits, and products that would wrap a 64-bit int
	// if they were computed before being checked. The division form refuses them without ever forming
	// the product — which is the difference between a guard and a post-hoc sanity check on a wrapped
	// value that may have landed on a plausible small positive.
	for _, c := range []struct {
		name        string
		count, elem uint64
	}{
		{"count past 32 bits, 4-byte elements", 1 << 32, 4},
		{"count past 32 bits, 1-byte elements", 1 << 33, 1},
		{"a product that would wrap a 64-bit int", 1 << 62, 8},
		{"the largest int times a wide element", uint64(math.MaxInt), 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			count, elem := wide(c.count), wide(c.elem)
			n, err := ListByteLength(count, elem)
			if err == nil {
				t.Fatalf("ListByteLength(%d, %d) = %d with no error; the product cannot be addressed by "+
					"the ABI's 32-bit pointer space, and if it wrapped it may look small and plausible",
					count, elem, n)
			}
			if !strings.Contains(err.Error(), "32-bit") {
				t.Errorf("the refusal %q does not say why the length is out of range", err)
			}
		})
	}
}

// TestLoadListFramesTheSpanBeforeReadingAnyElement covers the three refusals that belong to the framing
// rather than to an element.
func TestLoadListFramesTheSpanBeforeReadingAnyElement(t *testing.T) {
	h := newHeap(1 << 12)
	u32 := Type{Kind: KindU32}

	// A happy path first, so the refusals below are not the only thing exercised. Four u32s at an
	// aligned offset, each element read by the injected loader.
	for i, v := range []uint32{1, 2, 3, 4} {
		h.storeInt(uint64(v), 64+i*4, 4)
	}
	loads := 0
	got, err := LoadList(h, 64, 4, u32, func(at int) (Value, error) {
		loads++
		return Value{Type: u32, u: h.loadInt(at, 4)}, nil
	})
	if err != nil {
		t.Fatalf("LoadList: %v", err)
	}
	if loads != 4 {
		t.Errorf("the injected loader ran %d times, want 4", loads)
	}
	if n := len(got.list); n != 4 {
		t.Fatalf("lifted %d element(s), want 4", n)
	}
	for i, want := range []uint64{1, 2, 3, 4} {
		if u, ok := got.list[i].U32(); !ok || uint64(u) != want {
			t.Errorf("element %d = %d (ok=%v), want %d", i, u, ok, want)
		}
	}

	// **A misaligned pointer is refused, and for a u32 that check is not vacuous** — unlike a string's,
	// whose alignment is 1. definitions.py:1715 traps on it.
	if _, merr := LoadList(h, 66, 2, u32, func(int) (Value, error) { return Value{}, nil }); merr == nil {
		t.Error("a list<u32> at an odd-of-4 pointer was accepted; its elements require alignment 4")
	} else if !strings.Contains(merr.Error(), "aligned") {
		t.Errorf("the refusal %q does not name the alignment", merr)
	}

	// **The span is bounds-checked once, before any element is read.** The injected loader must not run
	// at all for a list that runs off the end — a loop that discovered this partway through would have
	// already handed the caller partial data to decide about.
	ran := false
	if _, berr := LoadList(h, 4000, 100, u32, func(int) (Value, error) {
		ran = true
		return Value{}, nil
	}); berr == nil {
		t.Error("a list running past the end of the heap was accepted")
	}
	if ran {
		t.Error("the injected loader ran for a list whose span is out of range; the bounds check must " +
			"frame the whole span before any element is read")
	}

	// **The framing check copies nothing**, which is the #921 repair and is asserted rather than assumed.
	//
	// A counting `ReadHeap` records which method the framing used. `LoadList` must reach `CheckRange` and
	// must **not** call `ReadBytes` for the span: on a guest heap `ReadBytes` allocates and copies, so a
	// guest-supplied count would make the check itself copy up to the whole addressable memory before a
	// single element was read. The element loader is injected, so any `ReadBytes` seen here is the
	// framing's.
	counting := &countingReadHeap{inner: h}
	if _, cerr := LoadList(counting, 64, 4, u32, func(at int) (Value, error) {
		return Value{Type: u32, u: h.loadInt(at, 4)}, nil
	}); cerr != nil {
		t.Fatalf("LoadList over the counting heap: %v", cerr)
	}
	if counting.checks == 0 {
		t.Error("LoadList never called CheckRange; the span was not framed")
	}
	if counting.reads != 0 {
		t.Errorf("LoadList called ReadBytes %d time(s) for the framing. On a guest heap that copies the "+
			"whole span and discards it — the check must be bounds-only", counting.reads)
	}
	if counting.bytes != 0 {
		t.Errorf("the framing check returned %d byte(s); a bounds-only check returns none", counting.bytes)
	}

	// An empty list reads nothing, runs the loader zero times, and is not an error.
	empty, err := LoadList(h, 64, 0, u32, func(int) (Value, error) {
		t.Error("the loader ran for an empty list")
		return Value{}, nil
	})
	if err != nil {
		t.Fatalf("an empty list<u32> was refused: %v", err)
	}
	if len(empty.list) != 0 {
		t.Errorf("an empty list lifted %d element(s)", len(empty.list))
	}
}
