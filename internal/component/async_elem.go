// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// elemType describes the element a stream or future type carries (ADR 0124): what a stream end's type is checked
// against (def:copy:"trap_if(end.t != stream_or_future_t.t)"), what a copy strides by (def:ReadableGuestBuffer.read,
// def:load_list_from_valid_range), and whether a copy between two ends of one instance traps (def:End.copy). It is
// `futureElem` (#1006) generalised to streams, and one descriptor serves both, so the element rule is one rule.
type elemType struct {
	// known reports that the type index named a stream (or, for futureElemOf, a future) at all.
	known bool
	// modeled reports that the codec lays the element out, so size and align are its layout and t its type. An
	// element it does not lay out (a stream or future of streams, futures, or error-contexts) is known and not
	// modeled: a stream of one is refused at its call, and a future of one traps at a copy within one instance,
	// which needs no layout.
	modeled bool
	// t is the element as the codec types it, through the host's resource RTs (canonTypeIn), or nil for a stream or
	// future with no element, or one not modeled.
	t *canon.Type
	// size and align are canon.ElemSize and canon.Alignment of t: 0 and 1 for no element.
	size, align int
	// float is 32 or 64 for a float element, whose NaN a copy canonicalises; 0 otherwise.
	float int
	// numeric is the pin's `none_or_number_type`: no element, an integer, or a float. Only these copy between two
	// ends of one instance (def:End.copy:"trap_if(self.owner and self.owner is self.other.owner and not
	// none_or_number_type(self.t))").
	numeric bool
	// own is the host resource an `own<R>` element names, resolved as resource.drop resolves its type
	// (hostResourceNamed), or nil. Nothing reads it yet: the accept stream E7b-2 builds (#1277) lowers each element
	// it produces through it, the pin's def:lower_own for an end with no owner.
	own *hostResource
}

// byteElem is `u8`'s descriptor, the element every host-minted stream end carries: a producer's (stdin, a socket's
// receive) and an async lower's stream result. A host consumer reads only a stream of it (driveStreamCopyLocked).
var byteElem = &elemType{known: true, modeled: true, t: &canon.Type{Kind: canon.KindU8}, size: 1, align: 1, numeric: true}

// same reports whether two descriptors are one element type, structurally, as the pin's `end.t !=
// stream_or_future_t.t` compares two ValTypes: two type indices naming `stream u32` are one type. An unmodeled
// descriptor is never the same as anything, since no end of one is ever made.
func (e *elemType) same(o *elemType) bool {
	if e == nil || o == nil || !e.modeled || !o.modeled {
		return false
	}
	if e.t == nil || o.t == nil {
		return e.t == nil && o.t == nil
	}
	return canon.TypeEqual(*e.t, *o.t)
}

// String renders the element as a WIT type, or "no element".
func (e *elemType) String() string {
	switch {
	case e == nil:
		return "an untyped end"
	case e.t != nil:
		return e.t.String()
	case e.modeled:
		return "no element"
	default:
		return "an element this engine does not lay out"
	}
}

// elemOf resolves type index ti to its element's descriptor, if ti names a value type of kind `kind` (VStream or
// VFuture); the zero descriptor, not known, otherwise.
func (w *walker) elemOf(ti uint32, kind ValKind) elemType {
	at := w.c.sectionTypeAt()
	td, ok := at(ti)
	if !ok || td.Kind != TDVal || td.Val.Kind != kind {
		return elemType{}
	}
	if td.Val.Elem == nil {
		return elemType{known: true, modeled: true, align: 1, numeric: true}
	}
	vt := resolveVal(*td.Val.Elem, at, nil)
	e := elemType{known: true}
	switch vt.Kind {
	case VS8, VU8, VS16, VU16, VS32, VU32, VS64, VU64:
		e.numeric = true
	case VF32:
		e.numeric, e.float = true, 32
	case VF64:
		e.numeric, e.float = true, 64
	case VOwn:
		e.own = w.hostResourceNamed(vt.Resource)
	default: // not a number: the pin traps a copy of it within one instance
	}
	ct, err := canonTypeIn(vt, w.hostRT)
	if err != nil {
		return e
	}
	e.t, e.modeled = &ct, true
	e.size, e.align = canon.ElemSize(ct), canon.Alignment(ct)
	return e
}

// streamElemOf is elemOf for a stream built-in's type index.
func (w *walker) streamElemOf(ti uint32) *elemType {
	e := w.elemOf(ti, VStream)
	return &e
}

// futureElemOf classifies future type `ti`'s element as the pin's `End.copy` does (2e7895c):
//
//		trap_if(self.owner and self.owner is self.other.owner and not none_or_number_type(self.t))
//
//	  - **No payload, or an integer:** the lift then lower is a byte copy.
//	  - **A float:** a byte copy with the NaN canonicalized, as the codec's lift does under the deterministic
//	    profile.
//	  - **Anything else** (`bool`, `char`, `string`, a list or record, and so on): the copy TRAPS within one
//	    instance. wasmtime 49.0.2 traps the same, *"cannot read from and write to intra-component future/stream
//	    with non-numeric payload"*, on `bool` and `char`.
//
// `ti` is the type future.write names, which the decoder keeps since #1271 (ADR 0124); it was always 0.
func (w *walker) futureElemOf(ti uint32) elemType { return w.elemOf(ti, VFuture) }

// canonicalizeFloats canonicalises each float element's NaN in buf, in place, as the codec's lift does under the
// deterministic profile (canon.CanonicalizeNaN32/64): a copy within one instance is a lift then a lower in the pin.
// float is the element's width, 32 or 64; any other value leaves buf as it is.
func canonicalizeFloats(buf []byte, float int) {
	switch float {
	case 32:
		for o := 0; o+4 <= len(buf); o += 4 {
			binary.LittleEndian.PutUint32(buf[o:], canon.CanonicalizeNaN32(binary.LittleEndian.Uint32(buf[o:])))
		}
	case 64:
		for o := 0; o+8 <= len(buf); o += 8 {
			binary.LittleEndian.PutUint64(buf[o:], canon.CanonicalizeNaN64(binary.LittleEndian.Uint64(buf[o:])))
		}
	}
}

// checkStreamEndType is def:copy's, def:cancel_copy's and def:drop's `trap_if(end.t != stream_or_future_t.t)` for a
// stream end: the built-in's element must be its end's (ADR 0124).
//
// NEUTER: return nil, and TestTypedStreamElementsMatchTheOracle's mismatch-write and mismatch-read park, BLOCKED,
// mismatch-cancel-write returns CANCELLED, and the two mismatched drops succeed.
func checkStreamEndType(op string, i uint32, end, named *elemType) error {
	if end.same(named) {
		return nil
	}
	return &interp.Trap{Reason: fmt.Sprintf("%s: end %d is a stream of a different type: its element is %s, and the "+
		"built-in names %s (def:copy: trap_if(end.t != stream_or_future_t.t))", op, i, end, named)}
}

// checkGuestBuffer is def:GuestBuffer's three traps for a stream.read's or stream.write's buffer of n elements at
// ptr, in the pin's order (ADR 0124):
//
//	trap_if(length > Buffer.MAX_LENGTH)
//	if t and length > 0:
//	  trap_if(ptr != align_to(ptr, alignment(t, cx.opts.memory.ptr_type())))
//	  trap_if(ptr + length * elem_size(t, cx.opts.memory.ptr_type()) > len(cx.opts.memory))
//
// c is the built-in's own caller, bound to the memory its canonopts name.
//
// NEUTER (the MAX_LENGTH line): drop it, and TestTypedStreamElementsMatchTheOracle's max-length-write still traps, for
// its range, since 2^28 u32 elements pass the end of its one page; it fails on its phrase only. A memory large enough
// to hold them (a GiB) is what a range-free witness would need.
func checkGuestBuffer(c *interp.CanonCaller, op string, el *elemType, ptr, n uint32) error {
	if n > bufferMaxLength {
		return &interp.Trap{Reason: fmt.Sprintf("%s: a buffer of %d elements is longer than %d (def:GuestBuffer)", op, n,
			bufferMaxLength)}
	}
	if el.t == nil || n == 0 {
		return nil
	}
	// NEUTER: drop this check, and TestTypedStreamElementsMatchTheOracle's misaligned-read and misaligned-write park,
	// BLOCKED, which is wasmtime 49.0.3's reading and not the pin's.
	if ptr%uint32(el.align) != 0 {
		return &interp.Trap{Reason: fmt.Sprintf("%s: a buffer at %#x is not aligned to its element %s, whose alignment "+
			"is %d (def:GuestBuffer)", op, ptr, el, el.align)}
	}
	// The range is n elements of el.size bytes each, not n bytes.
	//
	// NEUTER: check n bytes, and TestTypedStreamElementsMatchTheOracle's out-of-range-read and out-of-range-write park.
	nb := uint64(n) * uint64(el.size)
	if err := c.CheckRange(uint64(ptr), nb); err != nil {
		return &interp.Trap{Reason: fmt.Sprintf("%s: a buffer of %d elements (%d bytes) at %#x is outside memory: %v", op,
			n, nb, ptr, err)}
	}
	return nil
}

// refusedStreamElem is the binding of a stream built-in whose type index names no stream, or a stream of an element
// the codec does not lay out (ADR 0124): its call is refused by name, as an unserved import's is, and touches no
// handle. Bound rather than refused at instantiate, since bound is not run (#792).
func refusedStreamElem(name string, ti uint32, el *elemType) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		if !el.known {
			return nil, fmt.Errorf("%w: canon %s of type %d, which is not a stream type (ADR 0124)", ErrUnsupportedForm,
				name, ti)
		}
		return nil, fmt.Errorf("%w: canon %s of type %d, a stream of %s; a stream of a stream, a future or an "+
			"error-context is not built (ADR 0124)", ErrUnsupportedForm, name, ti, el)
	}
}
