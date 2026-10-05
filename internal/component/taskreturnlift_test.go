// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestTaskReturnLiftsItsStringResultBeforeTheGuestReusesTheBuffer is the witness for the eager lift
// (#903).
//
// # The claim, and why only a non-scalar can carry it
//
// The model lifts inside `canon_task_return` (definitions.py:2336) and hands `Task.return_` a value that
// is already lifted (def:487-492). Burroughs stored the flat words and lifted them after the callback
// loop exited. For a `u32` the two are the same thing — the value IS the word — so **every committed
// guest in this tree passed either way**, and that is precisely why the defect survived: the only result
// type any fixture used could not distinguish the orders.
//
// For a `string` the words are a `(ptr, len)` into guest memory, and between `task.return` and the
// loop's exit the guest **runs again**. `task-return-string-clobber-synth.wasm` uses that window: it
// resolves with the nine bytes at 1024, then overwrites them with 'X', then exits.
//
// Both readings are nine bytes of valid UTF-8, so a late lift fails with a **wrong value** rather than a
// decode error — the stronger shape, because an engine could pass every UTF-8 check and still be
// returning the guest's scratch.
func TestTaskReturnLiftsItsStringResultBeforeTheGuestReusesTheBuffer(t *testing.T) {
	const want = "burroughs"
	const clobbered = "XXXXXXXXX"

	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/task-return-string-clobber-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	res, err := in.CallValues("run")
	if err != nil {
		t.Fatalf("CallValues(run): %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("run returned %d value(s), want 1", len(res))
	}
	got, ok := res[0].Str()
	if !ok {
		t.Fatalf("run returned a %s, want a string", res[0].Type.Kind)
	}
	if got == clobbered {
		t.Fatalf("run returned %q — the guest's scratch, not its result. This is the late lift: the "+
			"(ptr, len) was kept and read after the callback loop exited, by which time the guest had "+
			"reused the buffer. definitions.py:2336 lifts inside canon_task_return for this reason.", got)
	}
	if got != want {
		t.Fatalf("run returned %q, want %q", got, want)
	}

	// **The anti-vacuity half, and it is not optional.** If the clobber loop never ran, `run` would
	// return "burroughs" for the trivial reason that nothing overwrote it, and everything above would be
	// green while exercising none of the window this fixture exists for. `peek` reports the byte now at
	// 1024, so "the result survived" and "the buffer was overwritten" become two independent facts —
	// and only their conjunction says the result was read *before* the overwrite.
	pk, err := in.CallValues("peek")
	if err != nil {
		t.Fatalf("CallValues(peek): %v", err)
	}
	if len(pk) != 1 {
		t.Fatalf("peek returned %d value(s), want 1", len(pk))
	}
	b0, ok := pk[0].U32()
	if !ok {
		t.Fatalf("peek returned a %s, want a u32", pk[0].Type.Kind)
	}
	if b0 != 'X' {
		t.Fatalf("peek says the byte at 1024 is %#x, want %#x ('X'). The guest did NOT reuse its result "+
			"buffer, so this fixture did not exercise the eager-lift window at all and the assertion "+
			"above passed vacuously.", b0, byte('X'))
	}
}

// TestTwoExportedLiftsResolveToTheirOwnFunctions is a separate defect's witness that happens to share
// this fixture.
//
// `exportRef` returned the **last** component func of the matching sort for every export name, because
// `parseExports` discarded the sortidx's index. Its comment said that was *"sufficient for the single
// func/instance export shapes this slice reaches"* — accurate about every fixture in the tree, which is
// why nothing caught it. This fixture is the first to export two lifts, and under the old resolution
// both names got `peek`: a `string`-returning export reported a `u32` result and lifted accordingly.
//
// It lives here, beside the eager-lift tests, because the two-export fixture is what exposed it; it is
// its own test because it fails for an unrelated reason and a reader should not have to infer it from a
// string assertion.
func TestTwoExportedLiftsResolveToTheirOwnFunctions(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/task-return-string-clobber-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	runFn := in.export.exports["run"]
	peekFn := in.export.exports["peek"]
	if runFn.fn == nil || peekFn.fn == nil {
		t.Fatal("the fixture exports two functions; one did not resolve")
	}
	// Distinct functions. Identity first, because two names sharing one function is the defect's
	// signature and every other assertion below is downstream of it.
	if runFn.fn == peekFn.fn {
		t.Fatal("both exports resolved to the same compFunc — every export name is being answered by " +
			"one function, which is what discarding the sortidx's index did")
	}
	if runFn.fn.core.name != "callee" {
		t.Errorf("run resolved to core func %q, want callee", runFn.fn.core.name)
	}
	if peekFn.fn.core.name != "peek" {
		t.Errorf("peek resolved to core func %q, want peek", peekFn.fn.core.name)
	}
	// And each carries its OWN signature. A resolution that got the function right and the signature
	// from elsewhere would still mis-lift.
	if runFn.fn.sig == nil || runFn.fn.sig.Result == nil || runFn.fn.sig.Result.Kind != VString {
		t.Errorf("run's signature does not declare a string result: %+v", runFn.fn.sig)
	}
	if peekFn.fn.sig == nil || peekFn.fn.sig.Result == nil || peekFn.fn.sig.Result.Kind != VU32 {
		t.Errorf("peek's signature does not declare a u32 result: %+v", peekFn.fn.sig)
	}
}

// TestTaskReturnRefusesAResultKindItCannotLift pins the refusal side, because the eager lift's scope is
// narrower than the bridge's: a flat lift carries the scalars and `string`, and anything compound needs a
// per-element load the codec does not expose.
//
// Asserted on the lift helper directly rather than through a guest, for the reason
// `TestACrossComponentResultShapeRefusesByName` gives one level over: no committed artefact returns an
// aggregate from a `task.return`, and *a negative claim buys a branch an exemption only if something
// checks the exemption*.
func TestTaskReturnRefusesAResultKindItCannotLift(t *testing.T) {
	for _, c := range []struct {
		name string
		vt   ValType
		want string
	}{
		// Carryable by the bridge, not by a FLAT lift — the distinction this refusal draws.
		{"list", ValType{Kind: VList, Elem: &ValType{Kind: VU8}}, "list"},
		{"variant", ValType{Kind: VVariant, Cases: []VarCase{{Name: "a"}}}, "variant"},
		// Not carryable by the bridge either, so it refuses one step earlier. Included so the two
		// refusal sites are both exercised and a change that collapsed them would show up.
		{"record", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "x", Type: ValType{Kind: VU32}}}}, "record"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := liftTaskReturnValue(nil, c.vt, nil)
			if err == nil {
				t.Fatalf("a %s result lifted successfully from flat values", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("the refusal %q does not name %q", err, c.want)
			}
		})
	}
}

// TestTaskReturnChecksItsFlatArity covers the arity guard, which is the arm that would otherwise index
// past the end of a short argument slice.
//
// `nil` is a legitimate `*interp.CanonCaller` here: the arity check runs before anything reads memory,
// which is itself the property being relied on — a guard that dereferenced the caller first would turn a
// wrong arity into a nil dereference.
func TestTaskReturnChecksItsFlatArity(t *testing.T) {
	if _, err := liftTaskReturnValue(nil, ValType{Kind: VU32}, nil); err == nil {
		t.Fatal("a u32 result with zero flat values lifted successfully")
	}
	if _, err := liftTaskReturnValue(nil, ValType{Kind: VString}, nil); err == nil {
		t.Fatal("a string result with zero flat values lifted successfully")
	}
	// One word is right for a u32 and wrong for a string: the two must not share a check.
	if _, err := liftTaskReturnValue(nil, ValType{Kind: VString}, []interp.Value{interp.I32(0)}); err == nil {
		t.Fatal("a string result with one flat value lifted successfully; a string is two words")
	}
}

// TestLiftedResultWinsOverTheFlatWords pins the precedence rule, which is the half of the repair that
// lives outside `task.return`. An eager lift that nothing preferred would be dead work.
func TestLiftedResultWinsOverTheFlatWords(t *testing.T) {
	sig := &FuncType{Result: &ValType{Kind: VString}}
	lifted := canon.Str("burroughs")

	// The flat words are deliberately nonsense: if they were consulted, the result would not be the
	// string, and on this path they cannot even be lifted (no memory, no caller).
	out, err := liftFlatResult("run", sig, liftResult{
		flat:   []interp.Value{interp.I32(9999), interp.I32(9999)},
		lifted: &lifted,
	})
	if err != nil {
		t.Fatalf("a resolution carrying an eagerly-lifted string: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d value(s), want 1", len(out))
	}
	if got, ok := out[0].Str(); !ok || got != "burroughs" {
		t.Fatalf("got %q (ok=%v), want burroughs — the flat words were preferred over the lifted value",
			got, ok)
	}

	// And a lifted value whose kind disagrees with the declaration is refused rather than passed on: a
	// mis-typing is a mis-typing whichever side produced it.
	u := canon.U32(7)
	if _, err := liftFlatResult("run", sig, liftResult{lifted: &u}); err == nil {
		t.Fatal("a u32 lifted value satisfied a string-declaring export")
	}
	if _, err := liftFlatResult("run", &FuncType{}, liftResult{lifted: &lifted}); err == nil {
		t.Fatal("an export declaring no result accepted a lifted string")
	}
}
