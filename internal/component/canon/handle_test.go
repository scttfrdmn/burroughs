// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"encoding/hex"
	"testing"
)

// handleFix is an own-handle round-trip the reference model emitted: lower_own's stored index, table
// state, and realloc, then lift_own's returned rep and (emptied) table. Borrow has no value round-trip
// here — its lend accounting is PR B's, at the call scope (disposition on #694).
type handleFix struct {
	Name  string `json:"name"`
	RT    int    `json:"rt"`
	Rep   uint32 `json:"rep"`
	Lower struct {
		MemoryHex string        `json:"memory_hex"`
		Index     int           `json:"index"`
		Table     []tableEntry  `json:"table"`
		Realloc   []reallocCall `json:"realloc"`
	} `json:"lower"`
	Lift struct {
		Rep   uint32       `json:"rep"`
		Table []tableEntry `json:"table"`
	} `json:"lift"`
}

type tableEntry struct {
	Index    int    `json:"index"`
	RT       int    `json:"rt"`
	Rep      uint32 `json:"rep"`
	Own      bool   `json:"own"`
	NumLends int    `json:"num_lends"`
}

type shapeFix struct {
	Kind  string   `json:"kind"`
	Size  int      `json:"size"`
	Align int      `json:"align"`
	Flat  []string `json:"flat"`
}

func serializeTable(tb *resourceTable) []tableEntry {
	var out []tableEntry
	for i, h := range tb.array {
		if h != nil {
			out = append(out, tableEntry{Index: i, RT: h.rt, Rep: h.rep, Own: h.own, NumLends: h.numLends})
		}
	}
	return out
}

// seedTable places handles into a table at their fixture indices, so a lift round-trip can consume a
// handle a store (on a different heap) put in its own table — the handle table is model state the
// fixture's memory bytes do not carry (#728). Index 0 stays the reserved nil sentinel.
func seedTable(tb *resourceTable, entries []tableEntry) {
	for _, e := range entries {
		for len(tb.array) <= e.Index {
			tb.array = append(tb.array, nil)
		}
		tb.array[e.Index] = &resourceHandle{rt: e.RT, rep: e.Rep, own: e.Own, numLends: e.NumLends}
	}
}

func assertTable(t *testing.T, what string, got, want []tableEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s table = %+v, want %+v", what, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s table[%d] = %+v, want %+v", what, i, got[i], want[i])
		}
	}
}

// TestHandleCodecMatchesReferenceModel is PR A's own-handle exit: lower_own's stored index, table
// entry, and lift_own's returned rep and emptied table match the reference model, and own/borrow's
// static shape (size/align/flat) matches. The borrow lend/lifetime discipline is PR B's.
func TestHandleCodecMatchesReferenceModel(t *testing.T) {
	f := loadFixtures(t)

	typeByKind := func(k string) Type {
		if k == "borrow" {
			return BorrowType(0)
		}
		return OwnType(0)
	}
	for _, s := range f.Shapes {
		typ := typeByKind(s.Kind)
		if got := size(typ); got != s.Size {
			t.Errorf("%s size = %d, want %d", s.Kind, got, s.Size)
		}
		if got := alignment(typ); got != s.Align {
			t.Errorf("%s align = %d, want %d", s.Kind, got, s.Align)
		}
		flat := flattenType(typ)
		if len(flat) != len(s.Flat) || (len(flat) == 1 && flat[0] != s.Flat[0]) {
			t.Errorf("%s flat = %v, want %v", s.Kind, flat, s.Flat)
		}
	}

	for _, hc := range f.Handles {
		t.Run(hc.Name, func(t *testing.T) {
			typ := OwnType(hc.RT)
			h := newHeap(64)
			ptr, err := h.realloc(0, 0, alignment(typ), size(typ))
			if err != nil {
				t.Fatal(err)
			}
			if serr := h.store(Own(hc.RT, hc.Rep), ptr); serr != nil {
				t.Fatalf("lower_own: %v", serr)
			}
			if got := hex.EncodeToString(h.mem); got != hc.Lower.MemoryHex {
				t.Errorf("lower_own memory:\n got %s\nwant %s", got, hc.Lower.MemoryHex)
			}
			if got := int(h.loadInt(ptr, 4)); got != hc.Lower.Index {
				t.Errorf("lower_own index = %d, want %d", got, hc.Lower.Index)
			}
			assertRealloc(t, "lower_own", h.calls, hc.Lower.Realloc)
			assertTable(t, "after lower_own", serializeTable(h.table), hc.Lower.Table)

			v, err := h.load(ptr, typ)
			if err != nil {
				t.Fatalf("lift_own: %v", err)
			}
			if uint32(v.u) != hc.Lift.Rep {
				t.Errorf("lift_own rep = %d, want %d", uint32(v.u), hc.Lift.Rep)
			}
			assertTable(t, "after lift_own", serializeTable(h.table), hc.Lift.Table)
		})
	}
}

// TestOwnDroppedAsBorrowIsRefused is the own-dropped-as-borrow positive assertion: a handle that should
// transfer ownership but is marked as a borrow (own=false) must be refused by lift_own, so a mislaid
// ownership bit is caught rather than silently double-freeing or leaking. Witnessed by lift_own's own
// check; own=false is a legitimate field value (a borrow handle has it), constructed here as input.
func TestOwnDroppedAsBorrowIsRefused(t *testing.T) {
	h := newHeap(64)
	// A peer receives, in an own position, a handle whose ownership bit was dropped to borrow.
	idx := h.table.add(&resourceHandle{rt: 0, rep: 7, own: false})
	ptr, err := h.realloc(0, 0, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	h.storeInt(uint64(idx), ptr, 4)
	if _, err := h.load(ptr, OwnType(0)); err == nil {
		t.Fatal("lift_own accepted a borrow handle in an own position — the mislaid ownership this asserts against")
	}
}

// TestWrongTableResolutionIsRefused is the wrong-table positive assertion: a handle index is meaningful
// only in the instance table that minted it, so resolving instance A's index against instance B's table
// must be refused, not silently resolved to whatever B holds at that slot. Witnessed by the tables
// being per-instance: B has no handle at A's index.
func TestWrongTableResolutionIsRefused(t *testing.T) {
	a := newHeap(64)
	b := newHeap(64)

	idx := a.table.add(&resourceHandle{rt: 0, rep: 99, own: true}) // minted in A
	ptr, err := b.realloc(0, 0, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	b.storeInt(uint64(idx), ptr, 4)
	if _, err := b.load(ptr, OwnType(0)); err == nil {
		t.Fatal("lift_own resolved instance A's handle index against instance B's table")
	}
}
