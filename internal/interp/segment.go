package interp

import (
	"fmt"
	"sync/atomic"

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

// The two published images — [decision 0065][0065]'s transfer of `memImage` to the two segment
// kinds, one struct each for the reason `tabImage`'s comment gives: the slice field lives inside the
// image and nowhere else, so `s.refs` is a compile error rather than a shape a control has to watch
// for. A drop then *publishes* the empty state instead of writing three words into an object another
// thread may be dereferencing, which is the whole of #622 on this side.
//
// [0065]: ../../docs/decisions/0065-the-table-and-segment-headers-move-inside-published-images-because-a-field-that-cannot-be-named-needs-no-enumeration-to-confine-it.md
type elemImage struct {
	refs []ref
}

type dataImage struct {
	bytes []byte
}

// The dropped state, shared by every segment of its kind, because a published image is never written
// after publication and therefore has nothing to distinguish one instance's from another's.
//
// **Shared rather than allocated per drop, and the reason is a guest loop.** `elem.drop` on an
// already-dropped segment is legal and does nothing (`bulk.wast:261` drops twice), so a guest can
// execute the opcode as often as it likes; a `&elemImage{}` per execution would be a
// guest-triggered allocation per instruction for a value that is always the same one. 0065's
// decision 5.
var (
	droppedElem = &elemImage{}
	droppedData = &dataImage{}
)

// elemInstance is one element segment's runtime contents — `elem.ml`'s `Value.ref_ list ref`.
//
// The image's `refs` nil and empty are the same state here, deliberately: the reference's `drop` is
// `seg := []`, and `Elem.size` of a dropped segment is 0. Nothing distinguishes "dropped" from
// "declared empty", because nothing in the semantics asks — `elem.drop` on an already-dropped
// segment is legal and does nothing (`bulk.wast:261` drops twice), which is only true if the
// dropped state is a *value* rather than a flag.
type elemInstance struct {
	// img is the published image. One load per operation; see `tabImage`'s field comment and
	// `TestEveryOperationLoadsAPublishedImageAtMostOnce`.
	img atomic.Pointer[elemImage]
}

// view is the currently published references — named `view` for `table.view`'s reason: the load-once
// control matches on the selector, so the name is what puts this subject inside it.
func (s *elemInstance) view() []ref { return s.img.Load().refs }

// size is the segment's length in elements — `Elem.size`, read off the slice rather than kept as
// a counter, for the reason `tabImage.slots` gives. It is itself an image load.
func (s *elemInstance) size() uint64 { return uint64(len(s.view())) }

// drop empties the segment — `Elem.drop`, `seg := []`.
//
// **A published empty image rather than an assignment**, which is #622: writing `s.refs = nil` into a
// reachable object publishes three words one at a time, and a reader pairing the new nil pointer with
// the old length indexes off address 0. Publishing a descriptor instead means a reader holds either
// the old image entire or the empty one entire.
//
// The dropped image names no array, rather than a truncated `refs[:0]`: the segment's backing array is
// the instance's only reference to those refs, and a dropped segment that keeps it alive is a leak
// whose size is the module's, not a subtlety. Semantically identical, since only `size` and the bulk
// arms read it.
func (s *elemInstance) drop() { s.img.Store(droppedElem) }

// newElemInstance publishes a segment's initial contents. It exists because the image field is
// unexported *and* unnamed at the call sites by construction, so every construction of a segment goes
// through one place — which is what makes "the image is stored before the instance is reachable" a
// property of one function rather than an agreement between several.
func newElemInstance(rs []ref) *elemInstance {
	s := &elemInstance{}
	s.img.Store(&elemImage{refs: rs})
	return s
}

// A `load` method transcribing `Elem.load` stood here and had **no caller**, which `golangci-lint`
// found and which is the classification question decision 0005 asks rather than an automatic bug.
// The classification: delete it. `eval.ml:427` checks `elem_oob` over the whole extent *before*
// reading, so the per-element bounds test is the redundant half of a belt-and-suspenders pair, and
// `execTableInit` does the copy in one `copy(slots[d:d+n], refs[s:s+n])` off two loaded images —
// there is no per-element read anywhere on this side.
//
// **The tell was the asymmetry, not the lint finding.** `dataInstance` has no `load` twin and never
// needed one, because `execMemoryInit` slices its bytes the same way; a method written for one of two
// parallel types is a method written from the *reference's* shape rather than from a consumer's
// requirement. That is design in the load-bearing spot with no witness (0006), and it is the same
// call `memory.go`'s closing note records for `errIsTrap`. Recorded rather than silently deleted
// because the live requirement — the element-index bound is on the *segment's* own length and not the
// table's — is a real fact that `execTableInit` now carries alone.

// dataInstance is one data segment's runtime contents — `data.ml`'s `string ref`.
//
// The bytes **alias the image** (the decoder's in-place posture) until a drop replaces the slice
// with nil, which is safe because nothing writes through them: `memory.init` reads out of a
// segment and into a memory, never the reverse. That is the same aliasing `DataSegment.Init`
// already documents, carried one layer inward rather than copied — a copy per segment would
// duplicate every module's data at instantiation for no semantic gain.
type dataInstance struct {
	// img is the published image; `elemInstance.img`'s twin, and the same one-load-per-operation rule.
	img atomic.Pointer[dataImage]
}

// view is the currently published bytes — `elemInstance.view`'s twin, same name for the same reason.
func (s *dataInstance) view() []byte { return s.img.Load().bytes }

// size is the segment's length in bytes — `Data.size`. Itself an image load.
func (s *dataInstance) size() uint64 { return uint64(len(s.view())) }

// drop empties the segment — `Data.drop`, `seg := ""`. `elemInstance.drop`'s twin; the argument for
// publishing rather than assigning is there.
func (s *dataInstance) drop() { s.img.Store(droppedData) }

// newDataInstance publishes a segment's initial bytes — `newElemInstance`'s twin, and the same
// single-construction-site reason.
func newDataInstance(bs []byte) *dataInstance {
	s := &dataInstance{}
	s.img.Store(&dataImage{bytes: bs})
	return s
}

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
	return newElemInstance(rs), nil
}
