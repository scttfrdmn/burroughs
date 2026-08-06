package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The *runtime* halves of the element and data sections: one mutable cell per segment, allocated
// at instantiation and emptied by a drop.
//
// # Why the module's segments are not enough
//
// `binary.ElemSegment` and `binary.DataSegment` are the *image*, and an image is immutable — the
// decoder's payloads alias the caller's bytes. What `table.init` reads is not the image, it is a
// segment **instance** whose contents a previous `elem.drop` may have emptied, and whose active
// segments the reference empties at instantiation before any exported function runs. The
// reference keeps them apart in the same way and for the same reason: `elem.ml` is
// `Value.ref_ list ref` and `data.ml` is `string ref`, one cell each, `drop seg = seg := []`.
//
// **That drop is not an optimization and it is observable.** `run_elem` (`eval.ml:1264-1276`)
// emits, for an Active segment, the offset expression followed by `TableInit` and then
// **`ElemDrop`** — so a module's own active segments are already empty by the time an exported
// function can reach them, and a Declarative segment emits `ElemDrop` alone. `bulk.wast:250-270`
// asserts exactly that shape: `init_active` with length 0 succeeds and with length 1 traps `out
// of bounds table access`, because the segment behind it holds nothing. An engine that skipped
// the drop would answer that vector by copying a function reference the reference cannot see —
// accept-direction, and a *passing* vector turned wrong rather than a missing feature.
//
// So a `table.init` arm without drop state is not a partial implementation of `table.init`, it is
// a different instruction. The two land together.
//
// # One type over two element kinds, and the seam is where the reference's is
//
// `elemInstance` holds `[]ref` and `dataInstance` holds `[]byte`, which is `Elem.size` in
// *elements* against `Data.size` in *bytes* — the same split `table.size` and `memory.bound`
// already have one layer up, so the two are separate types rather than a generic over a slice.
// What they share is the drop, and a shared `drop()` over two element types would be a type
// parameter earning one line; the cost of not sharing is that `drop` is written twice, and the
// cost of sharing would be a generic in the load-bearing spot for it (0006).

// elemInstance is one element segment's runtime contents — `elem.ml`'s `Value.ref_ list ref`.
//
// `refs` nil and `refs` empty are the same state here, deliberately: the reference's `drop` is
// `seg := []`, and `Elem.size` of a dropped segment is 0. Nothing distinguishes "dropped" from
// "declared empty", because nothing in the semantics asks — `elem.drop` on an already-dropped
// segment is legal and does nothing (`bulk.wast:261` drops twice), which is only true if the
// dropped state is a *value* rather than a flag.
type elemInstance struct {
	refs []ref
}

// size is the segment's length in elements — `Elem.size`, read off the slice rather than kept as
// a counter, for the reason `table.slots` gives.
func (s *elemInstance) size() uint64 { return uint64(len(s.refs)) }

// drop empties the segment — `Elem.drop`, `seg := []`.
//
// Set to nil rather than truncated to `refs[:0]`: the segment's backing array is the instance's
// only reference to those refs, and a dropped segment that keeps it alive is a leak whose size is
// the module's, not a subtlety. Semantically identical, since only `size` and `load` read it.
func (s *elemInstance) drop() { s.refs = nil }

// A `load` method transcribing `Elem.load` stood here and had **no caller**, which `golangci-lint`
// found and which is the classification question decision 0005 asks rather than an automatic bug.
// The classification: delete it. `eval.ml:427` checks `elem_oob` over the whole extent *before*
// reading, so the per-element bounds test is the redundant half of a belt-and-suspenders pair, and
// `execTableInit` does the copy in one `copy(tab.slots[d:d+n], seg.refs[s:s+n])` — there is no
// per-element read anywhere on this side.
//
// **The tell was the asymmetry, not the lint finding.** `dataInstance` has no `load` twin and never
// needed one, because `execMemoryInit` slices its bytes the same way; a method written for one of two
// parallel types is a method written from the *reference's* shape rather than from a consumer's
// requirement. That is design in the load-bearing spot with no witness (0006), and it is the same
// call `memory.go`'s closing note records for `errIsTrap`. Recorded rather than silently deleted
// because the live requirement — the element-index bound is `outOfBounds(s, n, seg.size())`, on the
// segment's own size and not the table's — is a real fact that `execTableInit` now carries alone.

// dataInstance is one data segment's runtime contents — `data.ml`'s `string ref`.
//
// The bytes **alias the image** (the decoder's in-place posture) until a drop replaces the slice
// with nil, which is safe because nothing writes through them: `memory.init` reads out of a
// segment and into a memory, never the reverse. That is the same aliasing `DataSegment.Init`
// already documents, carried one layer inward rather than copied — a copy per segment would
// duplicate every module's data at instantiation for no semantic gain.
type dataInstance struct {
	bytes []byte
}

// size is the segment's length in bytes — `Data.size`.
func (s *dataInstance) size() uint64 { return uint64(len(s.bytes)) }

// drop empties the segment — `Data.drop`, `seg := ""`.
func (s *dataInstance) drop() { s.bytes = nil }

// elemFor resolves an element segment index to its instance.
//
// The only place that does, which is `tableFor`'s rule and the grave it was paid for (#78/#105/
// #106: two places knowing how to turn an index into a thing). Unlike tables and memories there
// is no import offset to reserve for — the element and data index spaces are **module-local**,
// having no import kind at all (`ast.ml`'s `externtype` is func/table/memory/global/tag) — so an
// out-of-range index is a validation verdict with no linking arm beside it.
func (in *Instance) elemFor(what string, idx uint64) (*elemInstance, error) {
	if idx >= uint64(len(in.elems)) {
		return nil, fmt.Errorf("%w: %s names element segment %d of %d",
			ErrNotValidated, what, idx, len(in.elems))
	}
	return in.elems[idx], nil
}

// dataFor resolves a data segment index to its instance — elemFor's twin, and the same
// single-authority rule.
func (in *Instance) dataFor(what string, idx uint64) (*dataInstance, error) {
	if idx >= uint64(len(in.datas)) {
		return nil, fmt.Errorf("%w: %s names data segment %d of %d",
			ErrNotValidated, what, idx, len(in.datas))
	}
	return in.datas[idx], nil
}

// allocElem builds one element segment's instance — `init_elem`, `Elem.alloc (List.map …)`.
//
// **Every mode is allocated, including Declarative**, and the reference's fold says so: `init` runs
// `init_list init_elem m.it.elems` over *all* of them before `run_elem` drops the ones that are
// not passive. A declarative segment's elements are still evaluated, which is what makes
// `(elem declare func $f)` a forward declaration of `$f` rather than a no-op.
func (in *Instance) allocElem(seg *binary.ElemSegment) (*elemInstance, error) {
	rs, err := in.segmentRefs(seg)
	if err != nil {
		return nil, err
	}
	return &elemInstance{refs: rs}, nil
}
