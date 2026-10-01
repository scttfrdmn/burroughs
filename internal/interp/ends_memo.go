// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build burroughs_sitememo

package interp

import (
	"sync"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// This file is **lane D** of #136's question, prototyped on the chair's ruling on the #838 review: a pairing memo keyed by the
// structural site, filled the first time **that site** is entered.
//
// # What it is for, and the risk it is carrying
//
// Lane C (lazy per-body) spends `4 × len(Body)` on any body that is entered at all — measured at **507 of
// 1 720** bodies on the select-family run, so roughly 30% of lane B's arena. Lane D spends memory per
// **entered site**: measured at **15 070** sites on the same run, against 5 985 966 retained instructions.
//
// **Its risk is the per-entry cost, and that is the whole reason it is prototyped rather than assumed better.**
// `endOf` runs once per *dynamic block entry* — **4 603 468** times on the measured run — and this lane makes
// each one a map lookup where lanes B and C make it an array index. A sparse structure that wins on bytes and
// loses on time is not an improvement, and only a measurement says which.
//
// # Keyed by body identity and pc, not by `*binary.Func`
//
// `endOf` receives the body slice, not the `Func`, so the key is built from the slice's data pointer and the
// pc. That is sound because a retained body is never reallocated: `binary` decodes once and the interpreter
// only reads. It is also the reason this lane needs a composite key at all, which is itself a cost — lane C
// keys one map entry per body, this one keys 15 070.
//
// # Publication, the same requirement as lane C
//
// Several agents enter the same cold site at once, so a filled entry is **published** rather than written in
// place: `sync.Map.LoadOrStore`. Two agents computing the same site get the same answer — the pairing is a
// pure function of the body — so the loser discards and reads the winner's, and no agent can observe a
// half-written entry. `TestLazyTableIsPublishedSafely` covers both lanes for this reason.
type memoKey struct {
	body uintptr
	pc   int32
}

type siteMemo struct {
	sites sync.Map // memoKey -> int32
}

// endTable is what a lane hands `endOf`, and in this lane it is the memo rather than a slice — which is why
// `frameEnds`/`endOf` take an alias instead of `[]int32`, so `exec.go` is byte-identical in every lane.
type endTable = *siteMemo

// frameEnds hands back the per-instance memo. There is nothing per-body to build, which is the point: a body
// entered once pays for the sites it actually enters and nothing for the rest.
func (in *Instance) frameEnds(*binary.Func) endTable { return &in.memo }

// endOf answers from the memo, scanning once per site and publishing the result.
func endOf(body []binary.Instr, m endTable, pc int) (int, error) {
	if m == nil || len(body) == 0 {
		return matchEnd(body, pc)
	}
	k := memoKey{body: bodyID(body), pc: int32(pc)}
	if v, ok := m.sites.Load(k); ok {
		return int(v.(int32)), nil
	}
	end, err := matchEnd(body, pc)
	if err != nil {
		// A malformed header is not memoized: the error is what `matchEnd` writes, and caching it would make
		// the two lanes disagree about a body a fuzz mutation produced.
		return 0, err
	}
	m.sites.LoadOrStore(k, int32(end))
	return end, nil
}

// bodyID is the body slice's data pointer, used as half the memo key.
//
// `unsafe.SliceData` rather than `reflect.ValueOf(...).Pointer()`: the reflect route allocates and this runs
// on the lookup path, which is the one path this lane cannot afford to make slower than it already is.
func bodyID(body []binary.Instr) uintptr {
	return uintptr(unsafe.Pointer(unsafe.SliceData(body)))
}

// retained reports what the memo holds, for [Instance.RetainedEndsBytes]. One entry per *entered site*, which
// is the figure lane D's whole case rests on — and the one criterion 1 never let it reach.
func (m siteMemo) retained() (tables, slots int) {
	m.sites.Range(func(_, _ any) bool {
		slots++
		return true
	})
	return 1, slots
}
