// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"strings"
	"testing"
)

// The list lift's two bounded forms (contract §3 SP-1, ADR 0104; the chair's third ruling of 2026-10-18,
// item 2). A packed list is held as canonical bytes, and a long non-scalar list is held as segments. These
// witnesses hold each form to the plain lift it replaces: same elements, same traps, same stored bytes.

// TestAPackedListLiftsToThePlainElements: for every packable scalar kind, a packed lift materialises to exactly
// the elements `LoadVia` lifts one at a time. That includes bool's nonzero-is-true, a signed kind's sign, and
// a span of several `StringChunk`s, so a chunk boundary falls inside the list.
//
// **It cannot see bool's normalisation**, because materialising decodes through `LoadVia`, which normalises
// on its own. Dropping it in `loadPackedList` was run here and passed. `TestAPackedListStoresItsCanonicalBytes`
// is the test that catches it.
func TestAPackedListLiftsToThePlainElements(t *testing.T) {
	for _, k := range []Kind{KindBool, KindU8, KindS8, KindU16, KindS16, KindU32, KindS32, KindU64, KindS64, KindChar} {
		t.Run(k.String(), func(t *testing.T) {
			es := intBytes(k)
			count := 3*StringChunk/es + 7 // past three chunks, and not a whole number of them
			h := newHeap(count*es + 64)
			for i := range count * es {
				b := byte(i*37 + 11)
				if k == KindChar {
					// A char must be a valid scalar value: write small code points, little-endian.
					if i%4 == 0 {
						b = byte('a' + i/4%26)
					} else {
						b = 0
					}
				}
				h.mem[16+i] = b
			}
			elem := Type{Kind: k}
			got, err := LoadList(h, 16, count, elem, func(at int) (Value, error) { return LoadVia(h, elem, at) })
			if err != nil {
				t.Fatalf("LoadList: %v", err)
			}
			if got.u != listPacked {
				t.Fatalf("a list of %s lifted in form %d, want packed (%d)", k, got.u, listPacked)
			}
			elems, _ := got.List()
			if len(elems) != count {
				t.Fatalf("materialised %d elements, want %d", len(elems), count)
			}
			for i := range count {
				want, err := LoadVia(h, elem, 16+i*es)
				if err != nil {
					t.Fatalf("LoadVia element %d: %v", i, err)
				}
				if elems[i].u != want.u || elems[i].Type.Kind != want.Type.Kind {
					t.Fatalf("element %d: packed lift %v, plain lift %v", i, elems[i], want)
				}
			}
		})
	}
}

// TestAPackedListRefusesAnInvalidCharByElement: a surrogate in a `list<char>` past the first chunk is refused,
// naming its element, as the plain lift's `LiftFlatScalar` refuses it.
func TestAPackedListRefusesAnInvalidCharByElement(t *testing.T) {
	count := StringChunk/4 + 3
	h := newHeap(count*4 + 64)
	for i := range count {
		h.storeInt('x', 16+i*4, 4)
	}
	bad := count - 2
	h.storeInt(0xD800, 16+bad*4, 4)
	elem := Type{Kind: KindChar}
	_, err := LoadList(h, 16, count, elem, func(at int) (Value, error) { return LoadVia(h, elem, at) })
	if err == nil {
		t.Fatal("a list<char> holding the surrogate 0xD800 lifted; the reference traps on it")
	}
	if want := "element " + itoa(bad); !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal %q does not name %s", err, want)
	}
}

// TestAPackedListStoresItsCanonicalBytes: a packed `list<bool>` lowers to 0 and 1 bytes, whatever nonzero bytes
// it was lifted from, which is what lowering the plain lift's `true`s writes.
//
// NEUTER (run): drop bool's normalisation in `loadPackedList`, and the stored bytes are the source's
// `[0 1 2 255 0 7]`.
func TestAPackedListStoresItsCanonicalBytes(t *testing.T) {
	src := []byte{0, 1, 2, 255, 0, 7}
	h := newHeap(1 << 12)
	copy(h.mem[16:], src)
	elem := Type{Kind: KindBool}
	v, err := LoadList(h, 16, len(src), elem, func(at int) (Value, error) { return LoadVia(h, elem, at) })
	if err != nil {
		t.Fatalf("LoadList: %v", err)
	}
	p, n, err := StoreListIntoRange(h, v, func(e Value, at int) error { return StoreVia(h, e, at) })
	if err != nil {
		t.Fatalf("StoreListIntoRange: %v", err)
	}
	if n != len(src) {
		t.Fatalf("stored %d elements, want %d", n, len(src))
	}
	want := []byte{0, 1, 1, 1, 0, 1}
	if got := h.mem[p : p+n]; string(got) != string(want) {
		t.Fatalf("stored bytes %v, want %v", got, want)
	}
}

// TestALongListLiftsInBoundedPieces: a non-scalar list longer than one segment lifts as segments of at most
// `listSegment` elements, so no allocation is proportional to its count. It still materialises in order.
//
// This is the structural witness for the segmented build. **The arrival witness does not reliably catch its
// neuter**: under the one-allocation build, the `list<string>` arrival case failed 4 of 5 runs at 512 Ki
// elements and 0 of 10 at 1 Mi.
//
// NEUTER (run): `LoadList` building every list as one `make([]Value, count)`, and this fails on the form.
func TestALongListLiftsInBoundedPieces(t *testing.T) {
	own := OwnType(0)
	count := 3*listSegment + 5
	h := newHeap(count*4 + 64)
	for i := range count {
		h.storeInt(uint64(i), 16+i*4, 4)
	}
	v, err := LoadList(h, 16, count, own, func(at int) (Value, error) {
		return Value{Type: own, u: h.loadInt(at, 4)}, nil
	})
	if err != nil {
		t.Fatalf("LoadList: %v", err)
	}
	if v.u != listSegmented {
		t.Fatalf("a %d-element list<own> lifted in form %d, want segmented (%d)", count, v.u, listSegmented)
	}
	for i, seg := range v.list {
		if len(seg.list) > listSegment {
			t.Fatalf("segment %d holds %d elements, past listSegment %d", i, len(seg.list), listSegment)
		}
	}
	if n := v.listLen(); n != count {
		t.Fatalf("listLen = %d, want %d", n, count)
	}
	elems, _ := v.List()
	for i, e := range elems {
		if e.u != uint64(i) {
			t.Fatalf("element %d = %d, want %d", i, e.u, i)
		}
	}
}

// chunkHeap wraps the model heap and records the largest single read and write, so a witness can assert the
// codec never hands a heap more than one chunk at once. That bound is what keeps a guest heap's per-call
// poll one chunk away (contract §3 SP-1).
type chunkHeap struct {
	*heap
	maxRead, maxWrite, reads, writes int
}

func (c *chunkHeap) ReadBytes(ptr, n int) ([]byte, error) {
	c.reads++
	c.maxRead = max(c.maxRead, n)
	return c.heap.ReadBytes(ptr, n)
}

func (c *chunkHeap) WriteBytes(ptr int, data []byte) error {
	c.writes++
	c.maxWrite = max(c.maxWrite, len(data))
	return c.heap.WriteBytes(ptr, data)
}

// TestAPackedListCrossesTheHeapInChunks: a packed list and a string longer than several chunks are each
// lifted and lowered with no heap call larger than `StringChunk`. This is the deterministic witness for the
// lift side, which `TestAStopLandsInsideAnAdapterCopy` cannot see: a read leaves no memory state behind.
//
// NEUTERS (run): `loadPackedList` reading the whole span in one `ReadBytes` fails the list's read bound;
// `storeListData`'s packed arm writing it in one `WriteBytes` fails its write bound.
func TestAPackedListCrossesTheHeapInChunks(t *testing.T) {
	const n = 3*StringChunk + 17
	c := &chunkHeap{heap: newHeap(4*n + 64)}
	elem := Type{Kind: KindU8}
	v, err := LoadList(c, 16, n, elem, func(at int) (Value, error) { return LoadVia(c, elem, at) })
	if err != nil {
		t.Fatalf("LoadList: %v", err)
	}
	if c.reads < 4 || c.maxRead > StringChunk {
		t.Errorf("a %d-byte packed list was read in %d call(s), the largest %d bytes; want at least 4 calls of "+
			"at most %d", n, c.reads, c.maxRead, StringChunk)
	}
	if _, _, serr := StoreListIntoRange(c, v, func(e Value, at int) error { return StoreVia(c, e, at) }); serr != nil {
		t.Fatalf("StoreListIntoRange: %v", serr)
	}
	if c.writes < 4 || c.maxWrite > StringChunk {
		t.Errorf("a %d-byte packed list was written in %d call(s), the largest %d bytes; want at least 4 calls "+
			"of at most %d", n, c.writes, c.maxWrite, StringChunk)
	}

	s := &chunkHeap{heap: newHeap(4*n + 64)}
	p, ln, err := StoreStringIntoRange(s, strings.Repeat("x", n))
	if err != nil {
		t.Fatalf("StoreStringIntoRange: %v", err)
	}
	if _, err := LoadStringFromRange(s, p, ln); err != nil {
		t.Fatalf("LoadStringFromRange: %v", err)
	}
	if s.maxRead > StringChunk || s.maxWrite > StringChunk || s.reads < 4 || s.writes < 4 {
		t.Errorf("a %d-byte string crossed in %d read(s) of at most %d and %d write(s) of at most %d bytes; "+
			"want at least 4 of each, none past %d", n, s.reads, s.maxRead, s.writes, s.maxWrite, StringChunk)
	}
}

// itoa avoids importing strconv for one message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
