package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestDropPopsAReferenceWhenItIsTheLogicalTop is grave #206's own reproducer, pinned as a
// control: before decision 0023's fix, `drop` unconditionally popped `st.num`, so a bare
// reference left on top of the stack was silently discarded from the wrong array while the
// numeric value underneath it survived and got returned instead — corrupting the result of a
// module with no exception-handling machinery at all. `(ref.null func) (drop) (i32.const 7)`
// must return 7.
func TestDropPopsAReferenceWhenItIsTheLogicalTop(t *testing.T) {
	out := run1(t, `(module (func (export "c") (result i32)
		(ref.null func) (drop) (i32.const 7)))`)
	if len(out) != 1 || out[0].Bits != 7 {
		t.Errorf("got %+v, want i32 7 — drop popped the wrong array", out)
	}
}

// TestDropPopsNumericWhenNoReferenceEverPushed is the fix's own no-op case: a function that
// never touches a reference must behave exactly as before — `tracking` stays false, `drop`
// falls straight through to the numeric pop, at the cost of one length check.
func TestDropPopsNumericWhenNoReferenceEverPushed(t *testing.T) {
	out := run1(t, `(module (func (export "c") (result i32)
		(i32.const 1) (i32.const 2) (drop) (i32.const 3) (drop)))`)
	if len(out) != 1 || out[0].Bits != 1 {
		t.Errorf("got %+v, want i32 1 — two drops of pure-numeric scratch should leave the "+
			"first pushed value", out)
	}
}

// TestDropPopsNumericWhenReferenceIsBelow is the fix's other direction: once tracking has
// activated (a reference was pushed earlier in the same function), a *numeric* value pushed
// afterward and sitting on top must still be identified correctly — the sequence-number
// comparison, not merely "there is a reference somewhere," decides which array's top wins.
func TestDropPopsNumericWhenReferenceIsBelow(t *testing.T) {
	out := run1(t, `(module (func $f (result i32) (i32.const 1)) (func (export "c") (result i32)
		(ref.func $f) (drop)
		(i32.const 99) (drop)
		(i32.const 7)))`)
	if len(out) != 1 || out[0].Bits != 7 {
		t.Errorf("got %+v, want i32 7 — both drops must remove the value pushed immediately "+
			"before them, in the reverse of push order (ref then num), leaving 7", out)
	}
}

// TestDropAfterCatchRefRoundTrips is grave #206's own corpus-shaped reproducer — the exact
// pattern `try_table.wast`'s `throw-catch_ref-param-i32` and friends exercise: a `catch_ref`
// clause pushes a numeric payload value and then an exnref on top of it, and the immediately
// following `drop` (discarding the exnref so the block's own numeric result can return) must
// remove the reference, not the payload underneath it.
func TestDropAfterCatchRefRoundTrips(t *testing.T) {
	const src = `(module
		(tag $e0 (param i32))
		(func (export "c") (param i32) (result i32)
			(block $h (result i32 exnref)
				(try_table (result i32) (catch_ref $e0 $h) (throw $e0 (local.get 0)))
				(return)
			)
			(drop) (return)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d := &binary.Decoder{Features: ehGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	out, err := in.Invoke("c", Value{Type: binary.I32, Bits: 7})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 7 {
		t.Errorf("got %+v, want i32 7 — drop must remove the exnref catch_ref pushed, "+
			"leaving the numeric payload as the block's result", out)
	}
}

// TestDropAfterBranchCarriesSequenceNumbersThroughTruncation falsifies `branch`'s own extension
// (control.go): once tracking is active, a numeric value kept across a block's exit must carry
// its *own* sequence number through the truncation, not the stale number left behind at the
// destination index by the copy that `branch`'s own comment already performs for `st.num`.
//
// **Why this needs a specific seam, not just "any reference present"**: a reference pushed
// strictly before all the numeric activity always has the smallest sequence number in play, so
// *any* leftover numeric label — correct or stale — still reads as newer than it, and `drop`
// reaches the right answer by coincidence. The seam that actually distinguishes the two readings
// needs the reference's own sequence number sitting strictly *between* the stale label a broken
// truncation would leave behind and the real label the surviving slot should carry — which is
// exactly `(i32.const 111) (ref.func $f) (i32.const 222)`: 111 gets backfilled to the oldest
// sequence number, the ref lands next, and 222 (the value the branch actually keeps) is newer
// than both. A truncation that forgets to move 222's own sequence number down leaves 111's old,
// stale number in its place — smaller than the ref's — and `drop` would remove the reference
// instead of the numeric result, then leave two numeric values where a "funcref, i32" result
// needs one of each, an arity mismatch this test catches even without inspecting numbers by
// hand.
func TestDropAfterBranchCarriesSequenceNumbersThroughTruncation(t *testing.T) {
	out := run1(t, `(module (func $f (result i32) (i32.const 1)) (func (export "c") (result funcref i32)
		(block (result i32 funcref)
			(i32.const 111) (ref.func $f) (i32.const 222) (br 0)
		)
		(drop)
		(i32.const 9)))`)
	if len(out) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(out), out)
	}
	if out[0].Null || out[1].Bits != 9 {
		t.Errorf("got %+v, want (funcref non-null, i32 9) — drop must remove the block's own "+
			"kept numeric result (222, the newer value), not the reference it kept alongside it",
			out)
	}
}
