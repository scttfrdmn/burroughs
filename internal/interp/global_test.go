package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// i32Const and globalGet build the two initializer forms these tests need.
//
// A const-expr is a sequence terminated by END, so a single-instruction expression is **two**
// entries — measured in `constExprRef`'s comment one file over, not assumed here, and getting it
// wrong makes every initializer below report a stack shortfall rather than a value.
func i32Const(v uint64) []binary.Instr {
	return []binary.Instr{{Op: 0x41, Imm0: v}, {Op: opEnd}}
}

func globalGet(idx uint64) []binary.Instr {
	return []binary.Instr{{Op: 0x23, Imm0: idx}, {Op: opEnd}}
}

// TestGlobalInitializerSeesEarlierGlobals pins the ordering `Instantiate` takes from
// `eval.ml:1206`'s fold: each global's initializer is evaluated against the instance as it stands,
// so global N reads globals 0..N-1.
//
// **The values are chosen so that a wrong engine answers wrongly rather than coincidentally
// right.** `global.wast:17` is `(global $z1 i32 (global.get 0))` reading global 0, and a test built
// on that shape alone cannot fail an engine that always reads index 0 — which is why the chain here
// is three deep with *distinct* values and the read is of the **middle** global, not the first.
// This is the fixed-point lesson: a row whose right answer and wrong answer coincide is not a row.
func TestGlobalInitializerSeesEarlierGlobals(t *testing.T) {
	m := &binary.Module{
		Globals: []binary.Global{
			{Type: binary.I32, Init: i32Const(11)},
			{Type: binary.I32, Init: i32Const(22)},
			// Reads global 1, so an engine that reads global 0 answers 11 and an engine
			// that evaluates before filling slots answers neither.
			{Type: binary.I32, Init: globalGet(1)},
		},
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("deferred: %v", err)
	}
	g, err := in.globalFor("test", 2)
	if err != nil {
		t.Fatalf("globalFor(2): %v", err)
	}
	if g.num != 22 {
		t.Errorf("global 2 = %d, want 22 (global 1's value); 11 would mean the initializer read "+
			"global 0 rather than the index it names", g.num)
	}
}

// TestGlobalIndexSpacePutsImportsFirst is the 22-vector lesson `ImportedMems` records, asked of the
// global index space — and it is asked here because a global's *initializer* can read a global,
// which no other extern kind can do. An engine sizing `globals` by `len(m.Globals)` alone would
// resolve every defined global one slot low.
//
// The import slot must be **reserved and empty**: reserved so the defined global lands at index 1,
// empty because this row goes through `Instantiate`, which supplies nothing. Both halves are
// asserted, since a slice of the right length whose slot 0 held the *defined* global would satisfy
// a length check and answer the wrong global. The reason for the emptiness changed when the linker
// landed — it was "v0 has no linker" — and the *reservation* is what the row is about either way:
// `InstantiateLinked` fills this slot precisely because it was reserved.
func TestGlobalIndexSpacePutsImportsFirst(t *testing.T) {
	m := &binary.Module{
		Imports: []binary.Import{{Kind: binary.ExternGlobal, Module: "m", Name: "g"}},
		Globals: []binary.Global{{Type: binary.I32, Init: i32Const(99)}},
	}
	in, _ := Instantiate(m)
	if len(in.globals) != 2 {
		t.Fatalf("global index space is %d wide, want 2 (one import, one definition)", len(in.globals))
	}
	// Slot 0 is the import: reserved, nil, and reported as an *unsupplied import* rather than as a
	// bad module — the two facts globalFor keeps apart.
	if _, err := in.globalFor("test", 0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("global 0 (imported): got %v, want ErrUnsupported naming the unsupplied import", err)
	}
	// Slot 1 is the definition, at the offset the import consumed.
	g, err := in.globalFor("test", 1)
	if err != nil {
		t.Fatalf("globalFor(1): %v", err)
	}
	if g.num != 99 {
		t.Errorf("global 1 = %d, want 99; the defined global is not at the import offset", g.num)
	}
}

// TestGlobalGetSetRoundTrip runs both arms through the interpreter rather than calling the helpers,
// because a test that calls `global.get`'s helper proves the helper works while nothing dispatches
// to it. The body is `global.set 0` then `global.get 0`, so the value observed is the one written.
//
// **The written value differs from the initializer**, which is what makes this a round trip rather
// than a read: an engine whose `global.set` is a no-op returns the initializer and passes a test
// that wrote the same number.
func TestGlobalGetSetRoundTrip(t *testing.T) {
	// (func (export "f") (result i32) (global.set 0 (i32.const 7)) (global.get 0))
	m := &binary.Module{
		Types: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Results: []binary.ValType{binary.I32},
		}}},
		Funcs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 7}, // i32.const 7
			{Op: 0x24, Imm0: 0}, // global.set 0
			{Op: 0x23, Imm0: 0}, // global.get 0
			{Op: opEnd},
		}}},
		Globals: []binary.Global{{Type: binary.I32, Mutable: true, Init: i32Const(3)}},
		Exports: []binary.Export{{Name: "f", Kind: binary.ExternFunc, Index: 0}},
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	out, err := in.Invoke("f")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 7 {
		t.Errorf("got %v, want a single i32 7; 3 would mean global.set did not write", out)
	}
}

// TestGlobalGetOfARefUsesTheRefStack is the reference half, and it exists because the numeric half
// cannot see it: a global's slot is chosen by its *declared type*, and an engine that pushed an
// externref onto the numeric stack would satisfy every row above.
//
// `global.wast:24` is `(global $r externref (ref.null extern))` and `:30` reads it, so this is the
// suite's own shape. The assertion is on the **stack array**, not on a returned value, because
// `Invoke` refuses a ref-typed result (that is a separate frontier) — so the arm is exercised
// through `run` directly, which is where the two-array invariant is observable.
func TestGlobalGetOfARefUsesTheRefStack(t *testing.T) {
	m := &binary.Module{
		Globals: []binary.Global{{
			Type: binary.ExternRef,
			Init: []binary.Instr{{Op: opRefNull}, {Op: opEnd}},
		}},
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("deferred: %v", err)
	}
	fn := &binary.Func{Body: []binary.Instr{{Op: 0x23, Imm0: 0}, {Op: opEnd}}}
	st := &stack{}
	// Arity 0: this body's value is read off the stack below rather than returned, so telling
	// `run` to expect a result would make the arity check the thing under test.
	if err := in.run(fn, nil, st, 0); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(st.num) != 0 {
		t.Errorf("numeric stack has %d slots, want 0: an externref was pushed as a number", len(st.num))
	}
	if len(st.refs) != 1 {
		t.Fatalf("reference stack has %d slots, want 1", len(st.refs))
	}
	if !st.refs[0].Null {
		t.Errorf("got %+v, want a null reference", st.refs[0])
	}
}

// TestGlobalSetOfARefWritesTheRefSlot is the write direction of the test above. Separate rather
// than folded in, because `global.set`'s two halves are a different dispatch from `global.get`'s and
// a shared arm getting one right says nothing about the other.
//
// The value written is a **non-null** ref, so an engine whose ref `set` is a no-op leaves the null
// the initializer put there and fails on the discriminating field rather than on a length.
func TestGlobalSetOfARefWritesTheRefSlot(t *testing.T) {
	m := &binary.Module{
		Globals: []binary.Global{{
			Type:    binary.ExternRef,
			Mutable: true,
			Init:    []binary.Instr{{Op: opRefNull}, {Op: opEnd}},
		}},
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	g, err := in.globalFor("test", 0)
	if err != nil {
		t.Fatalf("globalFor: %v", err)
	}
	st := &stack{}
	st.pushRef(ref{Addr: 5})
	if err := g.set(st); err != nil {
		t.Fatalf("set: %v", err)
	}
	if g.ref.Null || g.ref.Addr != 5 {
		t.Errorf("got %+v, want {Null:false Addr:5}", g.ref)
	}
	if len(st.refs) != 0 {
		t.Errorf("reference stack has %d slots after set, want 0: the value was not popped", len(st.refs))
	}
}

// TestGlobalOutOfRangeIsTheLayeringDebt pins that an index past the end of the global index space is
// reported as ErrNotValidated and *never* as a trap or a spec verdict — this package's standing rule
// (0015's channel split), asked of the arm that most invites a bounds trap.
//
// Both directions of globalFor's index check are covered: past the end, and the empty-slot case
// above. A single row would leave the other arm asserting nothing.
func TestGlobalOutOfRangeIsTheLayeringDebt(t *testing.T) {
	in, trap := Instantiate(&binary.Module{
		Globals: []binary.Global{{Type: binary.I32, Init: i32Const(1)}},
	})
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	_, err := in.globalFor("instruction", 4)
	if !errors.Is(err, ErrNotValidated) {
		t.Errorf("got %v, want ErrNotValidated", err)
	}
	// The message names the holder and both numbers, because "global 4" alone sends a reader
	// looking for a global they do not have without saying how many they do.
	if got := err.Error(); !strings.Contains(got, "instruction names global 4 of 1") {
		t.Errorf("message is %q, want it to name the holder and both counts", got)
	}
	var tr *Trap
	if errors.As(err, &tr) {
		t.Errorf("an out-of-range global index produced a trap (%v); that verdict is #9's", tr)
	}
}

// TestImmutableGlobalIsNotRefusedHere is the *absence* of a check, asserted rather than left to be
// inferred — the declared-and-tracked shape. Writing an immutable global is `assert_invalid` with
// `global is immutable` (`global.wast:249` onward), which makes it #9's verdict; an engine
// enforcing it here would put the validator's answer somewhere the validator cannot be tested from.
//
// So this test *wants* the write to succeed, and it will start failing the day #9 lands — which is
// the point: it is the tripwire that says the check moved rather than vanished.
func TestImmutableGlobalIsNotRefusedHere(t *testing.T) {
	in, trap := Instantiate(&binary.Module{
		Globals: []binary.Global{{Type: binary.I32, Mutable: false, Init: i32Const(1)}},
	})
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	g, err := in.globalFor("test", 0)
	if err != nil {
		t.Fatalf("globalFor: %v", err)
	}
	st := &stack{}
	st.pushNum(42)
	if err := g.set(st); err != nil {
		t.Fatalf("set on an immutable global: %v — immutability is #9's verdict, not this "+
			"package's, so this arm must not refuse", err)
	}
	if g.num != 42 {
		t.Errorf("global = %d, want 42", g.num)
	}
}

// TestNeedRefReadsTheRefArray is the vacuity check on the pair of depth helpers: `needNum` and
// `needRef` read *different* arrays, and a single helper wired to the wrong one would answer every
// question about references by measuring numbers.
//
// A pushed number must not satisfy a reference's requirement, which is the assertion no
// same-array implementation can pass.
func TestNeedRefReadsTheRefArray(t *testing.T) {
	st := &stack{}
	st.pushNum(1)
	if err := st.needRef(1); !errors.Is(err, ErrNotValidated) {
		t.Errorf("needRef(1) with one *numeric* slot: got %v, want ErrNotValidated — "+
			"the two arrays have independent depths", err)
	}
	st.pushRef(ref{Null: true})
	if err := st.needRef(1); err != nil {
		t.Errorf("needRef(1) with one reference: %v", err)
	}
	if err := st.needRef(2); !errors.Is(err, ErrNotValidated) {
		t.Errorf("needRef(2) with one reference: got %v, want ErrNotValidated", err)
	}
}
