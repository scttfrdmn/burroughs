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

func serializeTable(tb *HandleTable) []tableEntry {
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
func seedTable(tb *HandleTable, entries []tableEntry) {
	for _, e := range entries {
		for len(tb.array) <= e.Index {
			tb.array = append(tb.array, nil)
		}
		tb.array[e.Index] = &ResourceHandle{rt: e.RT, rep: e.Rep, own: e.Own, numLends: e.NumLends}
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
	idx := h.table.Add(0, 7, false)
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

	idx := a.table.Add(0, 99, true) // minted in A
	ptr, err := b.realloc(0, 0, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	b.storeInt(uint64(idx), ptr, 4)
	if _, err := b.load(ptr, OwnType(0)); err == nil {
		t.Fatal("lift_own resolved instance A's handle index against instance B's table")
	}
}

// TestHandleTableLifecycleFollowsTheReference is the exported table's lifecycle (ADR 0117), scripted as
// rows over one table in the fixtures' serialization, each with the table the reference leaves behind.
//
// The slot order is the reference's own reading, not this file's: def:Table.add pops the free list from
// its end, and a probe of the pinned `Table` (add a, add b, remove a, add c; then remove b, remove c, add
// e, add f) returned indices 1, 2, 1, then 1, 2 — the last freed slot first. The lend rows follow
// def:lift_borrow (the handle stays, its lend count rises), def:Subtask.deliver_resolve (it falls at the
// call's return) and def:canon_resource_drop:"trap_if(h.num_lends != 0)".
func TestHandleTableLifecycleFollowsTheReference(t *testing.T) {
	const rt, other = 5, 6
	tb := NewHandleTable()
	own := func(i int, rep uint32, lends int) tableEntry {
		return tableEntry{Index: i, RT: rt, Rep: rep, Own: true, NumLends: lends}
	}
	steps := []struct {
		name    string
		do      func() (int, error)
		want    int // the index an add or a lift returns; ignored when refused
		refused bool
		table   []tableEntry
	}{
		{"add a", func() (int, error) { return tb.Add(rt, 10, true), nil }, 1, false, []tableEntry{own(1, 10, 0)}},
		{
			"add b", func() (int, error) { return tb.Add(rt, 11, true), nil }, 2, false,
			[]tableEntry{own(1, 10, 0), own(2, 11, 0)},
		},
		{
			"get a", func() (int, error) { h, err := tb.Get(1); return int(rep(h)), err }, 10, false,
			[]tableEntry{own(1, 10, 0), own(2, 11, 0)},
		},
		{
			"drop a", func() (int, error) { h, err := tb.Drop(1, rt); return int(rep(h)), err }, 10, false,
			[]tableEntry{own(2, 11, 0)},
		},
		{
			"get a after its drop", func() (int, error) { _, err := tb.Get(1); return 0, err }, 0, true,
			[]tableEntry{own(2, 11, 0)},
		},
		{
			"add c reuses a's slot", func() (int, error) { return tb.Add(rt, 12, true), nil }, 1, false,
			[]tableEntry{own(1, 12, 0), own(2, 11, 0)},
		},
		{
			"drop c as another type", func() (int, error) { _, err := tb.Drop(1, other); return 0, err }, 0, true,
			[]tableEntry{own(2, 11, 0)},
		},
		{
			"lend b to a call", func() (int, error) { _, err := tb.LiftBorrow(2, rt); return 0, err }, 0, false,
			[]tableEntry{own(2, 11, 1)},
		},
		{
			"lift b as an own while lent", func() (int, error) { r, err := tb.LiftOwn(2, rt); return int(r), err }, 0, true,
			nil,
		},
	}
	for _, s := range steps {
		got, err := s.do()
		switch {
		case s.refused && err == nil:
			t.Errorf("%s: accepted, want refused", s.name)
		case !s.refused && err != nil:
			t.Errorf("%s: refused: %v", s.name, err)
		case !s.refused && got != s.want:
			t.Errorf("%s: = %d, want %d", s.name, got, s.want)
		}
		assertTable(t, "after "+s.name, serializeTable(tb), s.table)
	}

	// The lent-own row, on a fresh table so its state is its own: an own lent to a call in progress cannot
	// be dropped, and the same handle drops once the call returns and the lend ends.
	tb2 := NewHandleTable()
	i := tb2.Add(rt, 20, true)
	h, err := tb2.LiftBorrow(i, rt)
	if err != nil {
		t.Fatalf("lend: %v", err)
	}
	if got := h.Lends(); got != 1 {
		t.Fatalf("lends during the call = %d, want 1", got)
	}
	if _, derr := tb2.Drop(i, rt); derr == nil {
		t.Error("dropping an own with a lend outstanding was accepted; the reference traps " +
			"(def:canon_resource_drop:\"trap_if(h.num_lends != 0)\")")
	}
	// The refused drop has freed the slot, as the reference's remove-then-trap order leaves it; the trap is
	// what ends the instance. Re-seeded to read the lend's end on a handle still in place.
	j := tb2.Add(rt, 21, true)
	h2, err := tb2.LiftBorrow(j, rt)
	if err != nil {
		t.Fatalf("lend: %v", err)
	}
	h2.EndLend()
	if got := h2.Lends(); got != 0 {
		t.Fatalf("lends after the call's return = %d, want 0", got)
	}
	if _, derr := tb2.Drop(j, rt); derr != nil {
		t.Errorf("dropping an own whose lend ended was refused: %v", derr)
	}
	assertTable(t, "after the lent-own rows", serializeTable(tb2), nil)
}

// rep is a handle's rep, or 0 for a refused lookup's nil.
func rep(h *ResourceHandle) uint32 {
	if h == nil {
		return 0
	}
	return h.Rep()
}
