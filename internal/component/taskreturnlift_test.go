// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
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

// storedBytes lowers v through the codec and returns the first eight bytes it wrote.
//
// It exists because `canon.Value` has readers for `u32` and `string` only, so a test cannot read a `s8`
// or a `bool` back out to compare it. Two values of the same kind are equal exactly when their lowered
// bytes are equal, and the lowering is the differential-verified `StoreVia` — so this compares through
// machinery that is already an oracle rather than through an accessor written for the test.
//
// Eight bytes covers every scalar (the widest is `u64`/`f64`); the heap is zeroed, so a narrower kind's
// unwritten tail compares equal on both sides.
func storedBytes(t *testing.T, v canon.Value) [8]byte {
	t.Helper()
	h := &bridgeHeap{mem: make([]byte, 1<<12), next: 64}
	if err := canon.StoreVia(h, v, 0); err != nil {
		t.Fatalf("storing a %s: %v", v.Type.Kind, err)
	}
	var out [8]byte
	copy(out[:], h.mem[:8])
	return out
}

// TestTaskReturnLiftsEveryScalarKind is the regression's witness.
//
// `liftTaskReturnValue` handled `u32` alone when the eager lift landed. That was a regression on arrival:
// a non-`u32` scalar from an async cross-component child previously resolved through
// `crossComponentValue`, which has arms for `bool` and all eight integers — and an eagerly lifted value
// takes precedence over the flat words, so those arms became unreachable for the async path. A conforming
// child returning `bool` would have trapped where it used to work. No committed fixture returns one, so
// nothing caught it.
//
// The conversions themselves are **not** asserted here against a table: they are `canon.LiftFlatScalar`,
// which `liftFlat` reaches for every scalar, so `TestCodecMatchesReferenceModel` verifies them against
// `definitions.py` over `gen/cases.json`. What this test owns is the part the differential cannot see —
// that `task.return` **reaches** that function, for every kind, with the word unnarrowed.
func TestTaskReturnLiftsEveryScalarKind(t *testing.T) {
	cases := []struct {
		name string
		vt   ValType
		word uint64
		kind canon.Kind
		want canon.Value // the same value built directly, for a lowered-bytes comparison
	}{
		{"bool-false", ValType{Kind: VBool}, 0, canon.KindBool, canon.Bool(false)},
		{"bool-true-is-nonzero", ValType{Kind: VBool}, 2, canon.KindBool, canon.Bool(true)},
		{"u8-truncates", ValType{Kind: VU8}, 0x1ff, canon.KindU8, canon.U8(0xff)},
		{"u16", ValType{Kind: VU16}, 0xbeef, canon.KindU16, canon.U16(0xbeef)},
		{"u32", ValType{Kind: VU32}, 0xdeadbeef, canon.KindU32, canon.U32(0xdeadbeef)},
		{"u64-keeps-all-64-bits", ValType{Kind: VU64}, 0xdeadbeefcafef00d, canon.KindU64, canon.U64(0xdeadbeefcafef00d)},
		{"s8-sign-extends", ValType{Kind: VS8}, 0xff, canon.KindS8, canon.S8(-1)},
		{"s16-sign-extends", ValType{Kind: VS16}, 0xffff, canon.KindS16, canon.S16(-1)},
		{"s32-sign-extends", ValType{Kind: VS32}, 0xffffffff, canon.KindS32, canon.S32(-1)},
		{"s64-sign-extends", ValType{Kind: VS64}, 0xffffffffffffffff, canon.KindS64, canon.S64(-1)},
		{"char-ascii", ValType{Kind: VChar}, 'A', canon.KindChar, mustChar(t, 'A')},
		{"char-astral", ValType{Kind: VChar}, 0x10FFFF, canon.KindChar, mustChar(t, 0x10FFFF)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := liftTaskReturnValue(nil, c.vt, []interp.Value{{Bits: c.word}})
			if err != nil {
				t.Fatalf("lifting a %s from word %#x: %v", c.name, c.word, err)
			}
			if got.Type.Kind != c.kind {
				t.Fatalf("lifted kind %s, want %s", got.Type.Kind, c.kind)
			}
			if g, w := storedBytes(t, got), storedBytes(t, c.want); g != w {
				t.Fatalf("lifted value lowers to % x, want % x — the word was mis-converted on the way "+
					"through, or narrowed before it reached the codec", g, w)
			}
		})
	}

	// The two floats are carried too. Their `want` cannot be built with a constructor — `canon` has no
	// `F32`/`F64` — which is itself why they are checked by kind alone here and by the differential for
	// their value.
	for _, c := range []struct {
		name string
		vt   ValType
		kind canon.Kind
	}{
		{"f32", ValType{Kind: VF32}, canon.KindF32},
		{"f64", ValType{Kind: VF64}, canon.KindF64},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := liftTaskReturnValue(nil, c.vt, []interp.Value{{Bits: 0x3ff0000000000000}})
			if err != nil {
				t.Fatalf("lifting a %s: %v", c.name, err)
			}
			if got.Type.Kind != c.kind {
				t.Fatalf("lifted kind %s, want %s", got.Type.Kind, c.kind)
			}
		})
	}

	// A floor, because the point of this test is coverage of a closed set: twelve one-word kinds plus the
	// two floats handled above.
	if len(cases) != 12 {
		t.Fatalf("the table covers %d kind(s); the one-word set this test is about has 12 besides the "+
			"two floats, so a kind has been added or dropped without a decision", len(cases))
	}
}

func mustChar(t *testing.T, r rune) canon.Value {
	t.Helper()
	v, err := canon.Char(r)
	if err != nil {
		t.Fatalf("canon.Char(%#x): %v", r, err)
	}
	return v
}

// TestTaskReturnTrapsOnACharOutsideTheUnicodeScalarRange is the one scalar arm with a model TRAP rather
// than a conversion, so it gets its own test: `convert_i32_to_char` traps past the last code point and
// inside the surrogate range (definitions.py:1343-1347).
//
// A guest can produce either from one word, so both are guest faults rather than engine errors.
func TestTaskReturnTrapsOnACharOutsideTheUnicodeScalarRange(t *testing.T) {
	for _, c := range []struct {
		name string
		word uint64
	}{
		{"past-the-last-code-point", 0x110000},
		{"far-past-it", 0xFFFFFFFF},
		{"low-surrogate-start", 0xD800},
		{"high-surrogate-end", 0xDFFF},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := liftTaskReturnValue(nil, ValType{Kind: VChar}, []interp.Value{{Bits: c.word}}); err == nil {
				t.Fatalf("a char of %#x lifted successfully; the model traps there", c.word)
			}
		})
	}
	// And the values either side of the surrogate block are fine, so the refusal is a range and not a
	// blanket.
	for _, ok := range []uint64{0xD7FF, 0xE000, 0x10FFFF} {
		if _, err := liftTaskReturnValue(nil, ValType{Kind: VChar}, []interp.Value{{Bits: ok}}); err != nil {
			t.Fatalf("a char of %#x was refused: %v", ok, err)
		}
	}
}

// TestTaskReturnCoversEveryKindCrossComponentValueDoes pins the regression's **shape** rather than its
// instances: the eager lift must carry at least what the path it displaced carried.
//
// `crossComponentValue`'s scalar arms are now reached only by a sync cross-component call. For an async
// one, `task.return`'s lifted value wins — so any kind that switch handles and `liftTaskReturnValue` does
// not is a kind that silently stopped working.
//
// # The domain is derived, and the first draft only said so
//
// This test's comment claimed the domain was *"derived from the two implementations rather than listed,
// so a future arm added to one and not the other fails here"* while the code held a **hand-written list
// of nine kinds**. A tenth arm added to `crossComponentValue` would have passed unnoticed — exactly the
// drift the test advertised catching. *A comment names one constraint, the code embodies another*, and
// the comment is the one a reader trusts.
//
// So the domain now comes from the `ValKind` enum's own extent, read off `valKindName` the way
// `TestBridgeClassifiesEveryValKind` reads it, and membership is **asked of `crossComponentValue`
// itself** rather than asserted: every kind it accepts is a kind the eager lift owes.
func TestTaskReturnCoversEveryKindCrossComponentValueDoes(t *testing.T) {
	// The enum's extent: every kind `valKindName` names is declared, and the first number it renders as
	// `valkind(N)` is one past the end.
	var n int
	for k := range 256 {
		if strings.HasPrefix(valKindName(ValKind(k)), "valkind(") {
			n = k
			break
		}
	}
	// A floor on the derivation, not on the result: a domain of two or three means the extent walk
	// stopped early rather than that the enum shrank.
	if n < 20 {
		t.Fatalf("derived a ValKind extent of %d, below the floor; the enum has more kinds than that, so "+
			"the derivation is wrong rather than the enum small", n)
	}

	var carried, missing []string
	for k := range n {
		vt := ValType{Kind: ValKind(k)}
		// **Membership is asked, not listed.** A compound kind reaches this with its element/field slices
		// empty, which `crossComponentValue` refuses through its default arm along with every other
		// non-scalar — so a degenerate compound cannot smuggle itself into the domain.
		if _, err := crossComponentValue(&FuncType{Result: &vt}, liftResult{flat: []interp.Value{{Bits: 1}}}); err != nil {
			continue // not carried by the sync path, so the eager lift owes nothing for it
		}
		carried = append(carried, valKindName(vt.Kind))
		if _, err := liftTaskReturnValue(nil, vt, []interp.Value{{Bits: 1}}); err != nil {
			missing = append(missing, valKindName(vt.Kind))
		}
	}

	if len(missing) > 0 {
		t.Fatalf("task.return cannot lift %v, which crossComponentValue carries. An async child returning "+
			"one of those used to resolve through that switch and now resolves through the eager lift, so "+
			"each is a kind that silently stopped working.", missing)
	}
	// The floor the chair set: nine kinds are carried today (bool and the eight integers). Fewer means the
	// sync path's switch shrank, which makes this test's domain shrink with it — a green over a domain
	// that quietly emptied is the failure mode a derived domain is otherwise prone to.
	if len(carried) < 9 {
		t.Fatalf("crossComponentValue carries only %d kind(s) (%v); nine are expected, so either an arm "+
			"was removed or the probe stopped reaching them, and this control's domain has silently shrunk",
			len(carried), carried)
	}
	t.Logf("EAGER-LIFT-COVERS %d of %d ValKind(s): %v", len(carried), n, carried)
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

// TestASyncLiftRefusesANonScalarResultRatherThanReadingItsReturnPointer answers the chair's question on
// the #913 review: what does a **sync** lift declaring a `string` result do today?
//
// It refuses by name, and this pins that — because the alternative is not a wrong error but a **plausible
// wrong value**. A sync result flattening wider than one word returns through a return **pointer**
// (`MAX_FLAT_RESULTS` is 1, definitions.py:2109), so the single flat word is an address. The `u32` path
// would read that address as the number and hand it back as data.
//
// Asserted on the lift helper directly: no committed guest is a sync lift declaring a string result, and
// *a negative claim buys a branch an exemption only if something checks the exemption.*
func TestASyncLiftRefusesANonScalarResultRatherThanReadingItsReturnPointer(t *testing.T) {
	// One flat word, as a sync lift with a spilled result really does return — an address, here a
	// plausible-looking one.
	retPtr := []interp.Value{interp.I32(1024)}

	for _, kind := range []ValKind{VString, VList, VRecord} {
		vt := kind
		sig := &FuncType{Result: &ValType{Kind: vt}}
		_, err := liftFlatResult("echo", sig, liftResult{flat: retPtr})
		if err == nil {
			t.Fatalf("a sync lift returning %s was accepted; its flat word is a return pointer, so the "+
				"value handed back would be the address read as data", valKindName(vt))
		}
		if !errors.Is(err, ErrUnsupportedForm) {
			t.Errorf("%s: refusal is not ErrUnsupportedForm: %v", valKindName(vt), err)
		}
		// The message must name the kind and both obstacles, because "u32 only" alone was the stale
		// sentence this replaced — it described the engine as narrower than it is now that the async path
		// carries a string.
		msg := strings.ToLower(err.Error())
		for _, want := range []string{valKindName(vt), "pointer", "post-return", "task.return"} {
			if !strings.Contains(msg, strings.ToLower(want)) {
				t.Errorf("%s: the refusal %q does not mention %q", valKindName(vt), err, want)
			}
		}
		// And it must NOT claim this kind crosses from an async export — true of `string`, false of
		// `list` and `record`, which the eager lift refuses too. A refusal that misdirects is worse than
		// one that only declines.
		if vt != VString && strings.Contains(msg, "returning "+strings.ToLower(valKindName(vt))+" works") {
			t.Errorf("%s: the refusal claims an async export returning it works, which is false — the "+
				"eager lift refuses compound kinds as well", valKindName(vt))
		}
	}

	// And a sync u32 result still works, so the refusal is about width and not about sync lifts.
	out, err := liftFlatResult("compute", &FuncType{Result: &ValType{Kind: VU32}},
		liftResult{flat: []interp.Value{interp.I32(7)}})
	if err != nil {
		t.Fatalf("a sync u32 result was refused: %v", err)
	}
	if got, ok := out[0].U32(); !ok || got != 7 {
		t.Fatalf("a sync u32 result lifted to %v (ok=%v), want 7", got, ok)
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
