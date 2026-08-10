package interp

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// ehGate is the Features every test in this file decodes with — try_table/throw/throw_ref/tag
// all live behind ExceptionHandling, so a bare binary.DecodeModule (run1's/link1's own zero-value
// gate) would refuse every source here before this rung's own arms ever run.
// **GC joins ExceptionHandling** because decision 0008 folds `exnref`/`(ref null exn)`'s
// abstract heaptype bytes into the GC gate (sections.go's `-0x17` case) — exnref locals and
// blocktypes need both gates on, not just the one that governs throw/throw_ref/try_table's own
// opcodes.
var ehGate = binary.Features{ExceptionHandling: true, GC: true}

// run1EH is run1 with the EH gate on — its own doc comment's reasoning (the encoder/decoder path
// is the thing under test, not a hand-built binary.Module) applies unchanged.
func run1EH(t *testing.T, src string) ([]Value, error) {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode %s: %v", src, err)
	}
	d := &binary.Decoder{Features: ehGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode %s: %v", src, err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate %s: %v", src, trap)
	}
	return in.Invoke("c")
}

// linkEH is link1 with the EH gate on, for the cross-instance tag-identity tests — a tag import
// needs the gate to even decode.
func linkEH(t *testing.T, src string, imp Imports) (*Instance, *Trap, error) {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d := &binary.Decoder{Features: ehGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return InstantiateLinked(m, imp)
}

// supplierEH is supplier with the EH gate on.
func supplierEH(t *testing.T, src string) *Instance {
	t.Helper()
	in, trap, err := linkEH(t, src, nil)
	if err != nil {
		t.Fatalf("supplier: %v", err)
	}
	if trap != nil {
		t.Fatalf("supplier trapped: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("supplier fell short: %v", derr)
	}
	return in
}

// TestThrowCaughtBySameFrameHandler is the mechanism's simplest case: a throw inside its own
// try_table's dynamic extent, caught in the same runFrame invocation, no call boundary crossed.
// Falsifies the mechanism at its narrowest — if this fails, nothing built on top of it can work.
func TestThrowCaughtBySameFrameHandler(t *testing.T) {
	out, err := run1EH(t, `(module
		(tag $e0)
		(func (export "c") (result i32)
			(block $h
				(try_table (result i32) (catch $e0 $h)
					(throw $e0)
					(i32.const 99)
				)
				(return)
			)
			(i32.const 42)))`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 42 {
		t.Errorf("got %+v, want i32 42 — the throw should have branched to $h", out)
	}
}

// TestThrowUncaughtByNonMatchingClauseEscapes falsifies the tag-comparison itself: a catch
// clause for a *different* tag must not match, and the exception must propagate rather than
// being incorrectly swallowed at the first handler it merely passes through.
func TestThrowUncaughtByNonMatchingClauseEscapes(t *testing.T) {
	_, err := run1EH(t, `(module
		(tag $e0)
		(tag $e1)
		(func (export "c") (result i32)
			(block $h
				(try_table (result i32) (catch $e1 $h)
					(throw $e0)
					(i32.const 99)
				)
				(return)
			)
			(i32.const 42)))`)
	var u *Uncaught
	if !errors.As(err, &u) {
		t.Fatalf("got %v, want an *Uncaught — $e0 does not match $e1's clause, so the throw "+
			"must escape rather than being caught by a handler for the wrong tag", err)
	}
}

// TestCatchClauseOrderIsFirstMatchNotMostSpecific is Scott's own named concentration point: the
// reference's reduction (eval.ml:1086-1104) peels clauses head-first and stops at the first one
// whose kind and tag agree — never "most specific" (a catch_all before a matching catch $e0 for
// the thrown tag must still win if it comes first in the vector), and never "best tag match
// among several catch_all-adjacent clauses". `duplicated-catches`/`catch-all-before-catch` are
// this rung's own corpus vectors for exactly this shape (try_table.wast); this pins the same
// property directly against the mechanism rather than only through the board.
func TestCatchClauseOrderIsFirstMatchNotMostSpecific(t *testing.T) {
	// try_table.wast's own `catch-all-before-catch`, verbatim (:269-280): catch_all comes
	// *before* a matching catch $e0 — the reference's own reduction must take the catch_all
	// (label 0, the innermost block) even though a later clause names the thrown tag exactly.
	out, err := run1EH(t, `(module
		(tag $e0)
		(func (export "c") (result i32)
			(block
				(block
					(try_table (catch_all 0) (catch $e0 1)
						(throw $e0)
					)
				)
				(return (i32.const 2))
			)
			(return (i32.const 3))))`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	// catch_all branches to label 0, the inner block — exiting it lands at `(return (i32.const
	// 2))`, immediately after. NOT label 1 (the outer block, `(return (i32.const 3))`), which is
	// what a most-specific-tag-match reading would incorrectly take instead (the corpus itself
	// asserts this exact value, try_table.wast:340).
	if len(out) != 1 || out[0].Bits != 2 {
		t.Errorf("got %+v, want i32 2 — clause order is first-match: catch_all (label 0) "+
			"precedes catch $e0 (label 1) in the wire vector and must win despite matching "+
			"less specifically", out)
	}
}

// TestCatchClauseOrderSecondClauseWinsWhenFirstDoesNotMatch is the order test's own mirror: with
// the first clause's tag changed so it no longer matches, the same module's outcome must flip to
// the second clause's label — proving the previous test's green was reading real clause-order
// behaviour and not a coincidence of which label happens to be reachable.
func TestCatchClauseOrderSecondClauseWinsWhenFirstDoesNotMatch(t *testing.T) {
	out, err := run1EH(t, `(module
		(tag $e0)
		(tag $e1)
		(func (export "c") (result i32)
			(block
				(block
					(try_table (catch $e1 0) (catch $e0 1)
						(throw $e0)
					)
				)
				(return (i32.const 2))
			)
			(return (i32.const 3))))`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	// $e1's clause (label 0, the inner block) does not match a thrown $e0, so the scan falls
	// through to the second clause, catch $e0 (label 1, the outer block) — exiting it lands at
	// `(return (i32.const 3))`.
	if len(out) != 1 || out[0].Bits != 3 {
		t.Errorf("got %+v, want i32 3 — the first clause's tag does not match, so the second "+
			"clause (label 1) must win", out)
	}
}

// TestCrossInstanceTagIdentityMatchesByAllocationNotShape is grave #163's own shape, applied to
// tags before any tag code shipped a wrong answer (0022 §3's stated preventive purpose,
// confirmed here): a tag import must resolve to the *same allocated tagInst* the exporting
// instance created, and a thrown tag from one instance must be compared against a catching
// clause's tag by pointer identity — never by re-deriving "does this look like the same tag" from
// its type. Two importers of the *same* exported tag (try_table.wast:8-19's own shape) must both
// catch a throw of that tag; a structural-comparison bug would make this pass by coincidence (the
// types trivially agree, since it's the same declaration) — the sharper falsification is the
// sibling test below, with a second, differently-typed tag the importer must NOT catch with.
func TestCrossInstanceTagIdentityMatchesByAllocationNotShape(t *testing.T) {
	exporter := supplierEH(t, `(module (tag $e0 (export "e0")))`)
	importer, trap, err := linkEH(t, `(module
		(tag $imported (import "m" "e0"))
		(func (export "c") (result i32)
			(block $h
				(try_table (result i32) (catch $imported $h)
					(throw $imported)
					(i32.const 99)
				)
				(return)
			)
			(i32.const 42)))`, exportsOf(exporter))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	out, err := importer.Invoke("c")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 42 {
		t.Errorf("got %+v, want i32 42 — a tag thrown and caught within the importer, both "+
			"referring to the same imported tagInst, must match", out)
	}
}

// TestCrossInstanceTagIdentityRejectsAStructurallyIdenticalDifferentTag is the sharper half:
// two *separately declared* tags of the identical type ((tag) with no params, in two different
// modules) must NOT match each other — proving the comparison is genuinely by allocation and
// not secretly by structural type, which would pass the previous test for the wrong reason.
func TestCrossInstanceTagIdentityRejectsAStructurallyIdenticalDifferentTag(t *testing.T) {
	// $other is exported by a second, unrelated module with a structurally identical tag type.
	other := supplierEH(t, `(module (tag $e1 (export "e1")))`)
	exporter := supplierEH(t, `(module (tag $e0 (export "e0")))`)
	registry := func(mod, name string) (Extern, bool) {
		if mod == "other" {
			return other.Export(name)
		}
		return exporter.Export(name)
	}
	importer, trap, err := linkEH(t, `(module
		(tag $mine (import "m" "e0"))
		(tag $theirs (import "other" "e1"))
		(func (export "c") (result i32)
			(block $h
				(try_table (result i32) (catch $theirs $h)
					(throw $mine)
					(i32.const 99)
				)
				(return)
			)
			(i32.const 42)))`, registry)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	_, err = importer.Invoke("c")
	var u *Uncaught
	if !errors.As(err, &u) {
		t.Fatalf("got %v, want an *Uncaught — $mine ($e0) and $theirs ($e1) are structurally "+
			"identical but separately allocated tags, and a catch clause for one must not "+
			"match a throw of the other", err)
	}
}

// TestThrowCaughtAcrossACallBoundary is the mechanism's genuinely new half: a throw inside a
// *callee*'s own function body, with the enclosing try_table in the *caller* — crossing the
// Go-call-is-a-wasm-frame boundary 0022 names as the design's whole point. Falsifies that
// raiseOrCatch actually runs at the call sites (opCall), not only at the throw site itself.
func TestThrowCaughtAcrossACallBoundary(t *testing.T) {
	out, err := run1EH(t, `(module
		(tag $e0)
		(func $callee (throw $e0))
		(func (export "c") (result i32)
			(block $h
				(try_table (result i32) (catch $e0 $h)
					(call $callee)
					(i32.const 99)
				)
				(return)
			)
			(i32.const 42)))`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 42 {
		t.Errorf("got %+v, want i32 42 — the callee's throw must be caught by the caller's "+
			"try_table, proving the catch scan runs at the call site and not only at throw", out)
	}
}

// TestThrowRefRoundTripsTheExactExnref falsifies catch_ref's own payload-and-exnref push: the
// pushed exnref, saved to a local and re-thrown via throw_ref from an entirely separate
// try_table, must still carry the original tag and be caught by a matching outer handler —
// proving ref.Exc genuinely round-trips the same excObj rather than being reconstructed or
// losing its tag identity somewhere across the two hops. The numeric payload (7) surviving the
// round trip is the same falsification pushPayload's own doc comment names for a mixed-kind
// throw, checked concretely here rather than only asserted in prose.
func TestThrowRefRoundTripsTheExactExnref(t *testing.T) {
	out, err := run1EH(t, `(module
		(tag $e0 (param i32))
		(func (export "c") (result i32)
			(local $e exnref)
			(block $h1 (result i32 exnref)
				(try_table (result i32) (catch_ref $e0 $h1) (throw $e0 (i32.const 7)))
				(return)
			)
			(local.set $e)
			(drop)
			(block $h2 (result i32)
				(try_table (result i32) (catch $e0 $h2) (throw_ref (local.get $e)))
				(return)
			)))`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 7 {
		t.Errorf("got %+v, want i32 7 — the payload must survive catch_ref's push, "+
			"local.set/get, and throw_ref's re-throw, then be caught by the second try_table's "+
			"plain catch", out)
	}
}
