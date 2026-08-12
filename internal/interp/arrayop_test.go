// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"strings"
	"testing"
)

// The array family's controls. `structop_test.go`'s `gcGate`, `runGC` and `runGCErr` are reused
// unchanged — the reason given there for going through the encoder and decoder rather than
// hand-building a `binary.Module` is stronger here, not weaker: `array.copy` carries *two* type
// immediates and `array.new_fixed`'s count is an immediate rather than an operand, so a row that
// built `binary.Instr` directly would be asserting this file's opinion of the immediate shape
// instead of the decoder's, and the immediate shape is part of what these rows test.
//
// Every row below was watched fail, with the mutation's diff printed before the run — #159's rule,
// and its own reason: a falsification that passes is either a stillborn control or a mutation that
// did not apply, and nothing but the diff tells the two apart. The mutation each row catches is named
// in that row's comment so the next reader can re-run it rather than re-derive it.

// TestArrayCopyHandlesOverlapInBothDirections is the row `arrayop.go`'s own comment cites by name,
// and it exists because `execArrayCopy` takes **two different paths** through the same overlap
// question — one `copy` call when the destination is unpacked, an element-at-a-time loop with a
// direction branch when it is packed — and only one of them is covered by the suite.
//
// The four rows are a 2×2: destination packed or not, crossed with the two overlap directions. The
// packed pair is the bidirectional control (#36's shape): a single reversed `dstIdx <= srcIdx`
// condition fails the two halves in *opposite* directions, where either half alone would look like a
// plausible reading of the reference. Under a forward-only loop the `d > s` row reads 1 where it should
// read 3 (each write feeds the next read); under a backward-only loop the `d < s` row reads 4 where it
// should read 2. The unpacked pair pins the claim that Go's `copy` is a `memmove` and needs no branch
// at all — its mutation is replacing that call with a forward element loop, which reds the `d > s` row.
//
// **Synthetic, and deliberately so.** `array_copy.wast` copies between *distinct* arrays throughout;
// a self-overlapping copy into a **packed** array is the case no vector covers, which is exactly why
// the direction branch was translated from `eval.ml:798-847` rather than reasoned about, and exactly
// why it needs a control that is not the board.
func TestArrayCopyHandlesOverlapInBothDirections(t *testing.T) {
	// One module per row: an array of four elements, a self-copy, one element read back.
	src := func(elem, get string, dstIdx, srcIdx int) string {
		return `(module (type $a (array (mut ` + elem + `)))
		  (func (export "c") (result i32) (local $x (ref $a))
		    (local.set $x (array.new_fixed $a 4
		      (i32.const 1) (i32.const 2) (i32.const 3) (i32.const 4)))
		    (array.copy $a $a (local.get $x) (i32.const ` + itoa(uint64(dstIdx)) + `)
		                      (local.get $x) (i32.const ` + itoa(uint64(srcIdx)) + `) (i32.const 3))
		    (` + get + ` $a (local.get $x) (i32.const ` + itoa(uint64(readAt(dstIdx))) + `))))`
	}
	rows := []struct {
		name   string
		elem   string
		get    string
		dst    int
		src    int
		want   int32
		mutant int32 // what the wrong direction answers, and it must differ
	}{
		// d > s, overlapping upward. [1,2,3,4] copy 3 from 0 to 1 => [1,1,2,3]; read index 3.
		// A forward loop reads each element after overwriting it: [1,1,1,1], answering 1.
		{"packed, d > s", "i8", "array.get_u", 1, 0, 3, 1},
		// d < s, overlapping downward. [1,2,3,4] copy 3 from 1 to 0 => [2,3,4,4]; read index 0.
		// A backward loop answers 4 for the mirrored reason.
		{"packed, d < s", "i8", "array.get_u", 0, 1, 2, 4},
		// The same two shapes with an unpacked destination, where one `copy` call serves both.
		{"unpacked, d > s", "i32", "array.get", 1, 0, 3, 1},
		{"unpacked, d < s", "i32", "array.get", 0, 1, 2, 4},
	}
	// Vacuity: the pair carrying the bidirectional claim must actually disagree with its own
	// mutant, or the table has become four assertions that a correct engine and a
	// one-direction-only engine both satisfy. A comparison that agrees with itself is the
	// empty-set defect wearing a full table's clothes.
	for _, r := range rows {
		if r.want == r.mutant {
			t.Fatalf("row %q cannot distinguish the direction branch: want and mutant are both %d",
				r.name, r.want)
		}
	}
	if len(rows) != 4 {
		t.Fatalf("the 2x2 is the domain and it has %d rows, not 4 — a row was lost", len(rows))
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			out := runGC(t, src(r.elem, r.get, r.dst, r.src))
			if got := out[0].Int32(); got != r.want {
				t.Fatalf("self-copy d=%d s=%d n=3 over (array %s): read %d, want %d "+
					"(the one-direction-only mutant answers %d)",
					r.dst, r.src, r.elem, got, r.want, r.mutant)
			}
		})
	}
}

// readAt picks the element that distinguishes a correct overlapping copy from a one-direction-only
// one: the *last* element written when copying upward, the first when copying downward. Those are the
// two positions a wrong direction corrupts, and reading anywhere else scores both engines the same.
func readAt(dstIdx int) int {
	if dstIdx == 0 {
		return 0
	}
	return 3
}

// TestArrayPopOrderIsCountOutsideTheValue pins the operand order of the four arms where it is easy to
// get wrong and where a wrong reading still produces a plausible answer.
//
// The family's order is `Num (I32 n) :: … :: Num (I32 i) :: Ref a` read outside-in — **the count is
// always outermost**, which is the opposite of what the text form's argument order suggests, since
// `(array.fill $a (ref) (i)(v)(n))` writes the count last. Each row is built so that the *swapped*
// reading terminates and answers wrongly rather than trapping: a mutation that traps is a weaker
// finding than one that returns a number, and #142's br_table lesson is that a mutation which cannot
// answer at all names no row.
func TestArrayPopOrderIsCountOutsideTheValue(t *testing.T) {
	t.Run("array.new takes n from the top", func(t *testing.T) {
		// `array.wast:59`'s own shape: an f64 element with an i32 count, so the two operands are
		// not interchangeable. Swapping the pops reads 7.0's low 32 bits as the count — 0 — and
		// the length assertion catches it without an allocation.
		out := runGC(t, `(module (type $a (array f64))
		  (func (export "c") (result i32 f64) (local $x (ref $a))
		    (local.set $x (array.new $a (f64.const 7) (i32.const 3)))
		    (array.len (local.get $x))
		    (array.get $a (local.get $x) (i32.const 2))))`)
		if out[0].Int32() != 3 || out[1].Float64() != 7 {
			t.Fatalf("array.new (f64.const 7) (i32.const 3): len %d elem %v, want 3 and 7",
				out[0].Int32(), out[1].Float64())
		}
	})

	t.Run("array.new_fixed pops in reverse declaration order", func(t *testing.T) {
		// The last initializer is on top, so `execArrayNewFixed` counts *down*. Reading both ends
		// is what makes this bidirectional: a forward loop answers 3 and 1 rather than 1 and 3,
		// and reading only the middle element scores the two loops identically.
		out := runGC(t, `(module (type $a (array i32))
		  (func (export "c") (result i32 i32) (local $x (ref $a))
		    (local.set $x (array.new_fixed $a 3 (i32.const 1) (i32.const 2) (i32.const 3)))
		    (array.get $a (local.get $x) (i32.const 0))
		    (array.get $a (local.get $x) (i32.const 2))))`)
		if out[0].Int32() != 1 || out[1].Int32() != 3 {
			t.Fatalf("array.new_fixed 3 (1)(2)(3): first %d last %d, want 1 and 3 "+
				"(a forward loop answers 3 and 1)", out[0].Int32(), out[1].Int32())
		}
	})

	t.Run("array.fill takes n outside the value", func(t *testing.T) {
		// Deliberately sized so the swapped reading (n=3, v=2) stays *in bounds* of a length-4
		// array and writes [2,2,2,0] where the correct order writes [3,3,0,0]. Both read-backs
		// differ, and neither engine traps.
		out := runGC(t, `(module (type $a (array (mut i32)))
		  (func (export "c") (result i32 i32) (local $x (ref $a))
		    (local.set $x (array.new_default $a (i32.const 4)))
		    (array.fill $a (local.get $x) (i32.const 0) (i32.const 3) (i32.const 2))
		    (array.get $a (local.get $x) (i32.const 1))
		    (array.get $a (local.get $x) (i32.const 2))))`)
		if out[0].Int32() != 3 || out[1].Int32() != 0 {
			t.Fatalf("array.fill i=0 v=3 n=2 over a length-4 default array: [1]=%d [2]=%d, "+
				"want 3 and 0 (swapping v and n answers 2 and 2)", out[0].Int32(), out[1].Int32())
		}
	})

	t.Run("array.copy interleaves the two pairs", func(t *testing.T) {
		// The two references are *not* adjacent on the stack — count, then the source pair, then
		// the destination pair — so a reading that pops them adjacently writes the wrong array.
		// Asserting both arrays is what makes that visible: the destination must change and the
		// source must not.
		out := runGC(t, `(module (type $a (array (mut i32)))
		  (func (export "c") (result i32 i32) (local $d (ref $a)) (local $s (ref $a))
		    (local.set $d (array.new_fixed $a 4
		      (i32.const 1) (i32.const 2) (i32.const 3) (i32.const 4)))
		    (local.set $s (array.new_fixed $a 4
		      (i32.const 5) (i32.const 6) (i32.const 7) (i32.const 8)))
		    (array.copy $a $a (local.get $d) (i32.const 0)
		                      (local.get $s) (i32.const 2) (i32.const 2))
		    (array.get $a (local.get $d) (i32.const 0))
		    (array.get $a (local.get $s) (i32.const 2))))`)
		if out[0].Int32() != 7 || out[1].Int32() != 7 {
			t.Fatalf("array.copy d=0 s=2 n=2: destination[0]=%d source[2]=%d, want 7 and 7 "+
				"(swapping the pairs leaves the destination at 1)", out[0].Int32(), out[1].Int32())
		}
	})
}

// TestArraySegmentTrapsBorrowMemoryAndTableStrings pins the family's least guessable behaviour: an
// out-of-range **segment** read from `array.new_data` reports `out of bounds memory access` and from
// `array.new_elem` reports `out of bounds table access`, though no memory and no table is involved.
//
// The reference raises `Memory.Bounds`/`Table.Bounds` and renders them through
// `memory_error`/`table_error` (`eval.ml:22-34`); `array.wast:209` and `:283` assert exactly those
// strings. The third row is the contrast that makes this a partition rather than two facts: an
// out-of-range *array index* does say "array". So the mutation this catches is the sensible-looking
// one — replacing either segment trap with `trapOOBArray`, which reads as a correction and fails four
// board vectors.
func TestArraySegmentTrapsBorrowMemoryAndTableStrings(t *testing.T) {
	rows := []struct {
		name string
		want string
		src  string
	}{{
		// array.wast:209's shape — a data segment shorter than the requested read.
		"array.new_data borrows memory's string",
		"out of bounds memory access",
		`(module (type $a (array i8)) (data $d "ab")
		  (func (export "c") (result i32)
		    (array.len (array.new_data $a $d (i32.const 0) (i32.const 3)))))`,
	}, {
		// array.wast:283's shape — an element segment shorter than the requested read.
		"array.new_elem borrows table's string",
		"out of bounds table access",
		`(module (type $a (array funcref)) (elem $e func $f) (func $f)
		  (func (export "c") (result i32)
		    (array.len (array.new_elem $a $e (i32.const 0) (i32.const 2)))))`,
	}, {
		// The contrast, and array.wast:210's own shape: an index, not a segment.
		"an array index says array",
		"out of bounds array access",
		`(module (type $a (array i32))
		  (func (export "c") (result i32)
		    (array.get $a (array.new_default $a (i32.const 3)) (i32.const 10))))`,
	}}
	// Vacuity with teeth: the three expected strings must be three *distinct* strings, or this
	// test is asserting one fact three times and the partition it is named for does not exist.
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.want] = true
	}
	if len(seen) != 3 {
		t.Fatalf("the three traps must differ, got %d distinct strings — the partition is gone",
			len(seen))
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, err := runGCErr(r.src)
			if err == nil {
				t.Fatalf("want a trap %q, got no error at all", r.want)
			}
			if !strings.Contains(err.Error(), r.want) {
				t.Fatalf("trap text %q does not contain %q", err.Error(), r.want)
			}
		})
	}
}

// TestArrayBoundsCheckPrecedesTheZeroLengthExit is the early-return grave (#41) pointed at this
// family, where the same ordering occurs five times.
//
// A zero-length run at *exactly* the end of an array is in bounds and one element past it is not —
// `oob i n j = (i+n) < i || (i+n) > j` — so an `if n == 0 { return nil }` opening skips the check it
// stands in front of and turns the second row green. That mutation is the whole reason the rows come
// in pairs: the at-the-end row passes under both readings, and only the one-past-the-end row separates
// them. Both `array_fill.wast` and `array_copy.wast` assert this shape, so the arms are oracle-covered;
// the two `init` arms' zero-length edge is *not* covered by any vector, and they are here for that.
func TestArrayBoundsCheckPrecedesTheZeroLengthExit(t *testing.T) {
	// Each arm, with n=0 at the end (must succeed) and n=0 one past it (must trap). The array is
	// length 3 throughout, so index 3 is the edge and 4 is past it.
	arms := []struct {
		name string
		op   func(idx int) string // the instruction, at destination index idx
		pre  string               // module-level declarations the op needs
	}{{
		"array.fill",
		func(idx int) string {
			return `(array.fill $a (local.get $x) (i32.const ` + itoa(uint64(idx)) + `)
			         (i32.const 9) (i32.const 0))`
		},
		"",
	}, {
		"array.copy destination",
		func(idx int) string {
			return `(array.copy $a $a (local.get $x) (i32.const ` + itoa(uint64(idx)) + `)
			         (local.get $x) (i32.const 0) (i32.const 0))`
		},
		"",
	}, {
		"array.init_data",
		func(idx int) string {
			return `(array.init_data $a $d (local.get $x) (i32.const ` + itoa(uint64(idx)) + `)
			         (i32.const 0) (i32.const 0))`
		},
		`(data $d "abc")`,
	}, {
		"array.init_elem",
		func(idx int) string {
			return `(array.init_elem $a $e (local.get $x) (i32.const ` + itoa(uint64(idx)) + `)
			         (i32.const 0) (i32.const 0))`
		},
		`(elem $e func $f) (func $f)`,
	}}
	if len(arms) != 4 {
		t.Fatalf("four arms take an n and a destination index; the table has %d", len(arms))
	}
	for _, a := range arms {
		for _, edge := range []struct {
			idx      int
			wantTrap bool
		}{{3, false}, {4, true}} {
			name := a.name
			if edge.wantTrap {
				name += ", one past the end"
			} else {
				name += ", exactly at the end"
			}
			t.Run(name, func(t *testing.T) {
				// `(mut i32)` throughout: array.fill and the two init arms need a mutable
				// element, and array.copy's destination does too.
				elem := "(mut i32)"
				if strings.Contains(a.pre, "elem") {
					elem = "(mut funcref)"
				}
				_, err := runGCErr(`(module (type $a (array ` + elem + `)) ` + a.pre + `
				  (func (export "c") (local $x (ref $a))
				    (local.set $x (array.new_default $a (i32.const 3)))
				    ` + a.op(edge.idx) + `))`)
				switch {
				case edge.wantTrap && err == nil:
					t.Fatalf("a zero-length run at index 4 of a length-3 array must trap: "+
						"got no error, which is what an `if n == 0` opening before the "+
						"bounds check produces (%s)", a.name)
				case edge.wantTrap && !strings.Contains(err.Error(), "out of bounds array access"):
					t.Fatalf("want out of bounds array access, got %q", err)
				case !edge.wantTrap && err != nil:
					t.Fatalf("a zero-length run at index 3 of a length-3 array is in "+
						"bounds: got %v", err)
				}
			})
		}
	}
}

// TestArrayInitDataChecksTheArrayBoundBeforeTheSegmentBound pins the one ordering in this family that
// is observable *between two traps* rather than between a trap and a success.
//
// The reference checks `array_oob a d n` and only then `data_oob` (`eval.ml:870-899`), and the two
// raise different strings — so a module violating **both** bounds distinguishes the orderings, where a
// module violating one cannot. Swapping the two checks in `execArrayInitData` reports
// `out of bounds memory access` here. `array.init_elem`'s row is the same claim against the *table*
// string, and it is the half no vector covers.
func TestArrayInitDataChecksTheArrayBoundBeforeTheSegmentBound(t *testing.T) {
	rows := []struct {
		name string
		src  string
	}{{
		"array.init_data",
		`(module (type $a (array (mut i8))) (data $d "ab")
		  (func (export "c") (local $x (ref $a))
		    (local.set $x (array.new_default $a (i32.const 2)))
		    (array.init_data $a $d (local.get $x) (i32.const 1) (i32.const 1) (i32.const 9))))`,
	}, {
		"array.init_elem",
		`(module (type $a (array (mut funcref))) (elem $e func $f) (func $f)
		  (func (export "c") (local $x (ref $a))
		    (local.set $x (array.new_default $a (i32.const 2)))
		    (array.init_elem $a $e (local.get $x) (i32.const 1) (i32.const 1) (i32.const 9))))`,
	}}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			// d=1, n=9 into a length-2 array is out of bounds, and src=1, n=9 is out of the
			// two-entry segment as well. Both violated; the array's string must win.
			_, err := runGCErr(r.src)
			if err == nil {
				t.Fatal("both bounds are violated; want a trap")
			}
			if !strings.Contains(err.Error(), "out of bounds array access") {
				t.Fatalf("the array bound is checked first, so want out of bounds array "+
					"access; got %q — which is what swapping the two checks produces", err)
			}
		})
	}
}

// TestArrayNewDataBoundIsBytesNotElements pins `m_64 = n * storage_size st`: the segment bound is in
// **bytes**, so the same element count over the same segment succeeds or traps depending on the
// element's *width*.
//
// This is the bidirectional pair the units question hands us, and it is the reason `storageSize`
// exists separately from `packMask`: three `i16` elements need six bytes, so a five-byte segment traps
// and a six-byte one does not, while three `i8` elements over the same five bytes are fine. Dropping
// the `* width` turns the middle row green and leaves the other two unchanged — a mutation that only
// one of the three rows can see.
func TestArrayNewDataBoundIsBytesNotElements(t *testing.T) {
	rows := []struct {
		name     string
		elem     string
		seg      string
		n        int
		wantTrap bool
	}{
		// Three i8s from five bytes: three bytes needed, in bounds under either reading.
		{"i8 x3 over 5 bytes", "i8", `"abcde"`, 3, false},
		// Three i16s from five bytes: six bytes needed. **The row that sees the units slip** —
		// an element-count bound reads 3 <= 5 and accepts.
		{"i16 x3 over 5 bytes", "i16", `"abcde"`, 3, true},
		// Three i16s from six bytes: exactly enough, so the trap above is about the width and
		// not about i16 being rejected outright.
		{"i16 x3 over 6 bytes", "i16", `"abcdef"`, 3, false},
	}
	// The middle row is the whole control; the outer two exist to bracket it. If all three rows
	// agree on their verdict the bracket has collapsed and a fixed-width bound would pass.
	traps := 0
	for _, r := range rows {
		if r.wantTrap {
			traps++
		}
	}
	if traps != 1 {
		t.Fatalf("the bracket wants exactly one trapping row, got %d — a byte-count bound and "+
			"an element-count bound would then agree on every row", traps)
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, err := runGCErr(`(module (type $a (array ` + r.elem + `)) (data $d ` + r.seg + `)
			  (func (export "c") (result i32)
			    (array.len (array.new_data $a $d (i32.const 0) (i32.const ` + itoa(uint64(r.n)) + `)))))`)
			switch {
			case r.wantTrap && err == nil:
				t.Fatalf("%d %s elements need %d bytes and the segment is shorter: want a "+
					"trap, got none — which is what an element-count bound produces",
					r.n, r.elem, r.n*2)
			case r.wantTrap && !strings.Contains(err.Error(), "out of bounds memory access"):
				t.Fatalf("want out of bounds memory access, got %q", err)
			case !r.wantTrap && err != nil:
				t.Fatalf("the segment is long enough: got %v", err)
			}
		})
	}
}

// TestArrayNewDataReadsPackedElementsThenExtendsAtTheOpcode is the `(array i8)`-from-a-segment pair,
// and it is here to pin *where* the sign lives: the load is unsigned and the **opcode** decides.
//
// `Data.load_val_storage` goes through `val_of_storage_bits`, whose packed case is `Pack.U`
// unconditionally (`value.ml:206-210`), so byte `0xff` is stored as 255 and `array.get_s` re-derives
// −1 from those bits rather than from a sign-extended load. `array.wast:204-205` asserts both readings
// of one byte, so this is oracle-covered as well.
//
// **The unsigned load is load-bearing, and the reason is in `pushField`'s own comment.** `extU` is
// `st.pushI32(int32(uint32(f.num)))` with no mask, deliberately — "the identity on a value `wrap`
// already narrowed … if it needed one, the write side would be wrong" — so the two functions form a
// narrow-on-store, trust-on-read contract, and `loadStorage` is the *store* half for everything that
// arrives from a data segment. Break it (sign-extend the packed load) and `get_u` answers **−1**
// rather than 255 while `get_s` stays correct, which is a one-sided failure only this row's unsigned
// half can see. Rung 2's packed test pins the other store half, `popField`'s `wrap`; between them
// every path into a packed field is covered.
//
// Worth recording because this comment first claimed the opposite — that `pushField` masks and the
// row was therefore unpinnable — with a parenthesised measurement that had been reasoned about rather
// than run. The falsification round is what corrected it: the mutation was expected to pass and
// failed, which under #159's rule is the *third* reading of a surprising falsification outcome, after
// stillborn control and unapplied mutation. A wrong prediction about a control is the cheapest kind
// there is, and only running it produces one.
func TestArrayNewDataReadsPackedElementsThenExtendsAtTheOpcode(t *testing.T) {
	out := runGC(t, `(module (type $a (array i8)) (data $d "\ff")
	  (func (export "c") (result i32 i32) (local $x (ref $a))
	    (local.set $x (array.new_data $a $d (i32.const 0) (i32.const 1)))
	    (array.get_u $a (local.get $x) (i32.const 0))
	    (array.get_s $a (local.get $x) (i32.const 0))))`)
	if out[0].Int32() != 255 || out[1].Int32() != -1 {
		t.Fatalf("byte 0xff in an (array i8): get_u %d get_s %d, want 255 and -1",
			out[0].Int32(), out[1].Int32())
	}
}

// TestArrayNullTrapOutranksTheBoundsCheck pins the order inside `popArrayIndexed`: a null reference
// traps null **even when the index is absurd**, because the reference's match puts `Ref NullRef` in an
// arm of its own before the `when array_oob` guard.
//
// The row is the one shape that separates the two: an index of 999 into a null reference. Either
// ordering traps, so only the *string* distinguishes them, and `array.wast:191` and `:210` assert the
// two strings as separate vectors. The mutation is returning `trapOOBArray` from the null branch —
// which passes every vector that violates one condition at a time.
func TestArrayNullTrapOutranksTheBoundsCheck(t *testing.T) {
	for _, op := range []string{
		`(array.get $a (ref.null $a) (i32.const 999))`,
		`(array.len (ref.null $a))`,
	} {
		t.Run(op, func(t *testing.T) {
			_, err := runGCErr(`(module (type $a (array i32))
			  (func (export "c") (result i32) (drop ` + op + `) (i32.const 0)))`)
			if err == nil {
				t.Fatal("a null array reference must trap")
			}
			if !strings.Contains(err.Error(), "null array reference") {
				t.Fatalf("want null array reference, got %q — an index of 999 must not "+
					"promote the bounds check above the null check", err)
			}
		})
	}
}
