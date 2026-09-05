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
// entries — measured rather than assumed (an active segment's Offset for `(i32.const 0)` decodes to
// `[{Op: 0x41}, {Op: opEnd}]`, END retained), and getting it wrong makes every initializer below
// report a stack shortfall rather than a value. The measurement used to be recorded at
// `constExprRef`'s definition one file over; #241 deleted that function, so the fact is restated
// here rather than left as a citation to nothing.
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
	if g.num.Load() != 22 {
		t.Errorf("global 2 = %d, want 22 (global 1's value); 11 would mean the initializer read "+
			"global 0 rather than the index it names", g.num.Load())
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
	if g.num.Load() != 99 {
		t.Errorf("global 1 = %d, want 99; the defined global is not at the import offset", g.num.Load())
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
	if err := in.run(fn, nil, st, 0, 1); err != nil {
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
	// Read back through `loadRef` because the slot is an `atomic.Pointer` (decision 0066) — the
	// assertion is on the published *value*, which is what a concurrent reader would see, and not on the
	// pointer that carries it.
	if got := g.loadRef(); got.Null || got.Addr != 5 {
		t.Errorf("got %+v, want {Null:false Addr:5}", got)
	}
	if len(st.refs) != 0 {
		t.Errorf("reference stack has %d slots after set, want 0: the value was not popped", len(st.refs))
	}
}

// TestV128GlobalRoundTripsAllFourLanes is grave #239's closing vector: a `(global v128 …)` module
// that **instantiates and reads its four lanes back**.
//
// # Why instantiation is not the assertion
//
// The grave was an arity check hard-coded to one numeric slot, so the visible symptom was a refused
// module — and a test that only asserted `Instantiate` returns no error would have been satisfied by a
// fix that widened the check and left the storage alone. `global` had **one** numeric field, so such
// a fix accepts the module, keeps the low half, silently drops the high half, and turns an honest
// refusal into a wrong answer. That is the trade the accept-direction rule (§9 G-3) says the board
// cannot see: the refusal was a *fail* attributed to a missing feature, and the wrong answer is a
// pass on every vector that only reads lane 0.
//
// So the four lanes are read individually and given **four distinct values, none of them zero**. Each
// choice is load-bearing:
//
//   - *Distinct* — a global whose halves were swapped, or whose high half came back as a copy of the
//     low, answers lanes 2 and 3 wrongly. Four equal lanes cannot see either defect.
//   - *Nonzero* — the failure mode is a zero-filled high half, so lanes 2 and 3 must differ from what
//     `numHi`'s zero value would produce. This is the fixed-point lesson: a row whose right answer and
//     wrong answer coincide is not a row.
//   - *Lanes 2 and 3 in the high half* — `v128.const`'s Imm0 is the low 64 bits and Imm1 the high
//     (`binary/instr.go:788-798`), so lanes 0/1 live in Imm0 and 2/3 in Imm1. A test reading only lanes
//     0 and 1 exercises exactly the half that was never broken.
//
// The read goes through `Invoke`, so `global.get`'s dispatch, `pushV128`'s slot pair, and
// `i32x4.extract_lane`'s own `popV128` are all on the path — the helper-versus-path distinction: calling
// `g.get` directly would prove the accessor works while nothing dispatched to it.
func TestV128GlobalRoundTripsAllFourLanes(t *testing.T) {
	// (global v128 (v128.const i32x4 0x11111111 0x22222222 0x33333333 0x44444444))
	const lo = 0x2222222211111111 // lanes 0, 1
	const hi = 0x4444444433333333 // lanes 2, 3 — zero in the broken engine
	want := []uint64{0x11111111, 0x22222222, 0x33333333, 0x44444444}

	// (func (export "lanes") (result i32 i32 i32 i32) — four `global.get`/`extract_lane` pairs,
	// because each extract consumes the vector it reads.
	body := []binary.Instr{}
	for lane := range 4 {
		body = append(body,
			binary.Instr{Op: 0x23, Imm0: 0},                          // global.get 0
			binary.Instr{Prefix: 0xfd, Op: 0x1b, Imm0: uint64(lane)}, // i32x4.extract_lane
		)
	}
	body = append(body, binary.Instr{Op: opEnd})

	i32x4 := []binary.ValType{binary.I32, binary.I32, binary.I32, binary.I32}
	m := &binary.Module{
		Types: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{Results: i32x4}}},
		Funcs: []binary.Func{{TypeIndex: 0, Body: body}},
		Globals: []binary.Global{{Type: binary.V128, Init: []binary.Instr{
			{Prefix: 0xfd, Op: 0x0c, Imm0: lo, Imm1: hi},
			{Op: opEnd},
		}}},
		Exports: []binary.Export{{Name: "lanes", Kind: binary.ExternFunc, Index: 0}},
	}

	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	// **`Deferred` is checked, not skipped.** A failed initializer is recorded rather than
	// returned — `globalFor` reports it later — so the pre-#241 engine reaches this line with
	// `trap == nil` and its refusal parked here. Omitting this check would make the whole test
	// depend on `Invoke` noticing, which is a longer chain than the grave needs.
	if err := in.Deferred(); err != nil {
		t.Fatalf("deferred: %v — a v128 global initializer was refused (grave #239)", err)
	}

	out, err := in.Invoke("lanes")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("got %d results, want 4", len(out))
	}
	for lane, w := range want {
		if got := out[lane].Bits; got != w {
			t.Errorf("lane %d = %#x, want %#x%s", lane, got, w,
				map[bool]string{true: " (zero here is the dropped high half — grave #239)"}[got == 0 && lane >= 2])
		}
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
// So this test *wants* the write to succeed, and it will start failing the day a validator refuses
// a write to an immutable global — which is the point: it is the tripwire that says the check moved
// rather than vanished. Its condition is that code state and not #9's closure, because the
// umbrella can close with this rule unimplemented and a tripwire silent on the day its subject
// changes is worth nothing (ADR 0043).
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
	if g.num.Load() != 42 {
		t.Errorf("global = %d, want 42", g.num.Load())
	}
}

// TestGlobalReadsWhatTheInterpreterHolds is `Instance.Global`'s own control (#323): every storage
// shape read **twice** — once by `global.get` inside a body, once through the new boundary — and
// required to agree, and required to equal a hand-written value besides.
//
// # Why agreement alone would be close to vacuous
//
// Both readings end at the same `*global`, so an engine with one storage bug answers both sides
// wrongly and the two agree perfectly. That is the empty-versus-empty shape, and it is why each row
// also carries a `want` written out by hand: the row asserts a *value*, and the agreement is the
// second claim on top of it.
//
// What agreement genuinely covers is the **layout dispatch**, which is the one thing the two paths
// do independently: `get` pushes onto whichever array `shape()` names, `value` builds a `Value` from
// the same switch, and a fourth storage shape added to one and not the other is exactly grave #239
// (a v128 global whose high half was dropped on the read-back half specifically). So `Hi` is
// compared, which means the comparison here is `!=` on the whole struct and **not** `Value.Equal` —
// `Equal` reads `Bits` alone for a numeric value and is blind to `Hi`, so using it would leave the
// grave's own field unasserted.
//
// # Why this test rather than the board
//
// The 11 corpus rows #323 drained are `i32`/`i64`/`f32` globals over two files; none is a `v128`, a
// reference, or an immutable-versus-mutable pair. So the board's +11 says the boundary answers the
// population that exists, and the rows below are the ones that do not exist yet — the same division
// of labour the `passFloor` ledger in `internal/spec` states from its side.
func TestGlobalReadsWhatTheInterpreterHolds(t *testing.T) {
	// funcTarget is the function index the funcref row's `ref.func` names — the last accessor, and
	// **nonzero on purpose**: index 0 is what a boundary that dropped the payload would also report,
	// so a zero target is a row whose right answer and wrong answer coincide.
	const funcTarget = 6

	rows := []struct {
		name string
		typ  binary.ValType
		mut  bool
		init []binary.Instr
		want Value
	}{{
		name: "i32",
		typ:  binary.I32,
		init: []binary.Instr{{Op: 0x41, Imm0: 0xdeadbeef}, {Op: opEnd}},
		want: Value{Type: binary.I32, Bits: 0xdeadbeef},
	}, {
		// The one mutable row. Mutability is orthogonal to reading — `value` consults `shape()` and
		// never `Mutable` — and a row of each is here so that a future guard mistakenly keyed on
		// mutability fails on one row rather than on none.
		name: "i64",
		typ:  binary.I64,
		mut:  true,
		init: []binary.Instr{{Op: 0x42, Imm0: 0x0123456789abcdef}, {Op: opEnd}},
		want: Value{Type: binary.I64, Bits: 0x0123456789abcdef},
	}, {
		// A **signaling NaN**, not a plain float: the payload is what a numeric conversion anywhere
		// on either path destroys while leaving every integral value intact (pushF32's own lesson).
		name: "f32",
		typ:  binary.F32,
		init: []binary.Instr{{Op: 0x43, Imm0: 0x7fa00001}, {Op: opEnd}},
		want: Value{Type: binary.F32, Bits: 0x7fa00001},
	}, {
		name: "f64",
		typ:  binary.F64,
		init: []binary.Instr{{Op: 0x44, Imm0: 0x7ff8000000000001}, {Op: opEnd}},
		want: Value{Type: binary.F64, Bits: 0x7ff8000000000001},
	}, {
		// Grave #239's shape asked of the new reader: four distinct nonzero lanes, so a dropped or
		// duplicated high half is wrong on the `Hi` field rather than agreeing by zero.
		name: "v128",
		typ:  binary.V128,
		init: []binary.Instr{
			{Prefix: 0xfd, Op: 0x0c, Imm0: 0x2222222211111111, Imm1: 0x4444444433333333},
			{Op: opEnd},
		},
		want: Value{Type: binary.V128, Bits: 0x2222222211111111, Hi: 0x4444444433333333},
	}, {
		// The reference array's half. A null reference's payload is the absence of one, so
		// `RefKind` stays `PayloadNone` — grave #266's fact, and the reason this row cannot be
		// confused with the one below it.
		name: "externref",
		typ:  binary.ExternRef,
		init: []binary.Instr{{Op: opRefNull}, {Op: opEnd}},
		want: Value{Type: binary.ExternRef, Null: true},
	}, {
		// A **non-null** reference, which is the row that makes the pair above discriminating: the
		// constructor crosses in `RefKind` and the function index in `Bits`, and an engine that
		// answered `Null: true` here or dropped the kind fails on a field the null row cannot see.
		name: "funcref",
		typ:  binary.FuncRef,
		init: []binary.Instr{{Op: opRefFunc, Imm0: funcTarget}, {Op: opEnd}},
		want: Value{Type: binary.FuncRef, RefKind: PayloadFunc, Bits: funcTarget},
	}}

	// The guard keeps `funcTarget` honest: it names the last accessor by number, so a row added
	// below moves the function it points at and the constant has to move with it.
	if funcTarget != len(rows)-1 {
		t.Fatalf("funcTarget is %d but the last accessor is %d: a row was added without moving the "+
			"constant, so the funcref row now names a different function", funcTarget, len(rows)-1)
	}

	// One accessor per global — `(func (export "<name>") (result <t>) (global.get i))` — and one
	// global export beside it, so both readings of row i are reachable by name.
	m := &binary.Module{}
	for i, r := range rows {
		m.Types = append(m.Types, binary.CompType{Kind: binary.CompFunc, Func: binary.FuncType{
			Results: []binary.ValType{r.typ},
		}})
		m.Funcs = append(m.Funcs, binary.Func{TypeIndex: uint32(i), Body: []binary.Instr{
			{Op: 0x23, Imm0: uint64(i)}, {Op: opEnd},
		}})
		m.Globals = append(m.Globals, binary.Global{Type: r.typ, Mutable: r.mut, Init: r.init})
		m.Exports = append(m.Exports,
			binary.Export{Name: r.name, Kind: binary.ExternFunc, Index: uint32(i)},
			binary.Export{Name: "g_" + r.name, Kind: binary.ExternGlobal, Index: uint32(i)})
	}

	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	// Checked rather than skipped, for TestV128GlobalRoundTripsAllFourLanes' reason: a refused
	// initializer is *parked* rather than returned, so omitting this makes every row below report a
	// missing global instead of the refusal that caused it.
	if err := in.Deferred(); err != nil {
		t.Fatalf("deferred: %v — a global initializer was refused", err)
	}

	for _, r := range rows {
		got, err := in.Global("g_" + r.name)
		if err != nil {
			t.Errorf("Global(%q): %v", "g_"+r.name, err)
			continue
		}
		if got != r.want {
			t.Errorf("Global(%q) = %+v, want %+v", "g_"+r.name, got, r.want)
		}
		out, err := in.Invoke(r.name)
		if err != nil {
			t.Errorf("Invoke(%q): %v", r.name, err)
			continue
		}
		if len(out) != 1 {
			t.Errorf("Invoke(%q) returned %d results, want 1", r.name, len(out))
			continue
		}
		// The differential. It fires where the two dispatches disagree — which is the failure a
		// hand-written `want` on one side alone cannot distinguish from a wrong expectation.
		if out[0] != got {
			t.Errorf("%s: global.get answered %+v and Global answered %+v; the two layout "+
				"dispatches disagree", r.name, out[0], got)
		}
	}
}

// TestGlobalExportKindIsNotDeclarationOrder is `exportedIndex`'s kind test, asserted rather than
// left to the comment that calls it load-bearing. A module may export a function and a global under
// **one name** — nothing in the spec forbids it, since the export space is not partitioned by kind —
// so a lookup that matched on the name alone would answer whichever came first in the section.
//
// Both orders are built, because a single order cannot tell a kind test from a declaration-order
// coincidence: with the function first, a name-only lookup hands `Global` a *function* index; with
// the global first, it hands `Invoke` a global's. Each order catches the defect the other hides.
//
// # The two index spaces are skewed, and the first draft of this test proves why that is required
//
// `"dual"` is **function 1 and global 0**, so a wrong-kind index names a different object in either
// direction. Written first with both at index 0 — the shape a reader would reach for — this test
// passed its own falsification: with the kind test deleted, a name-only lookup returned 0, and 0 was
// the right index for both, so only the `"callable"` row below fired. That is the fixed-point lesson
// arriving inside the control written to state it, and the reason the decoy function and global exist
// at all is to move the wrong answer off the right one.
func TestGlobalExportKindIsNotDeclarationOrder(t *testing.T) {
	// Four distinct values across two index spaces: a lookup that crossed kinds reports a number
	// that says which space it read and at what index.
	const wantGlobal, decoyGlobal = 7, 13
	const wantFunc, decoyFunc = 42, 99

	// (func (export "dual") (result i32) (i32.const 42)) at index **1**, beside
	// (global (export "dual") i32 7) at index **0**, each with a decoy at the other's index, plus a
	// function-only export to ask about a name no global carries.
	base := func(exports []binary.Export) *binary.Module {
		body := func(v uint64) []binary.Instr {
			return []binary.Instr{{Op: 0x41, Imm0: v}, {Op: opEnd}}
		}
		return &binary.Module{
			Types: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
				Results: []binary.ValType{binary.I32},
			}}},
			Funcs: []binary.Func{
				{TypeIndex: 0, Body: body(decoyFunc)},
				{TypeIndex: 0, Body: body(wantFunc)},
			},
			Globals: []binary.Global{
				{Type: binary.I32, Init: i32Const(wantGlobal)},
				{Type: binary.I32, Init: i32Const(decoyGlobal)},
			},
			Exports: exports,
		}
	}
	fn := binary.Export{Name: "dual", Kind: binary.ExternFunc, Index: 1}
	gl := binary.Export{Name: "dual", Kind: binary.ExternGlobal, Index: 0}
	onlyFunc := binary.Export{Name: "callable", Kind: binary.ExternFunc, Index: 1}

	for _, order := range []struct {
		what    string
		exports []binary.Export
	}{
		{"function first", []binary.Export{fn, gl, onlyFunc}},
		{"global first", []binary.Export{gl, fn, onlyFunc}},
	} {
		in, trap := Instantiate(base(order.exports))
		if trap != nil {
			t.Fatalf("%s: trap: %v", order.what, trap)
		}
		g, err := in.Global("dual")
		if err != nil {
			t.Errorf("%s: Global(\"dual\"): %v", order.what, err)
		} else if g.Bits != wantGlobal {
			t.Errorf("%s: Global(\"dual\") = %d, want %d", order.what, g.Bits, wantGlobal)
		}
		out, err := in.Invoke("dual")
		if err != nil {
			t.Errorf("%s: Invoke(\"dual\"): %v", order.what, err)
		} else if len(out) != 1 || out[0].Bits != wantFunc {
			t.Errorf("%s: Invoke(\"dual\") = %v, want a single i32 %d", order.what, out, wantFunc)
		}

		// A name only a *function* export carries is an error and not an empty value — the caller
		// asked the wrong question and is told so, which is the half of the kind test that has no
		// right answer to return.
		_, err = in.Global("callable")
		if err == nil {
			t.Errorf("%s: Global(\"callable\") succeeded; that name is a function export", order.what)
		} else if !strings.Contains(err.Error(), `no exported global "callable"`) {
			t.Errorf("%s: Global(\"callable\") said %q, want it to name the missing *global*",
				order.what, err)
		}
		if _, err := in.Global("absent"); err == nil {
			t.Errorf("%s: Global(\"absent\") succeeded on a name the module does not export at all",
				order.what)
		}
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
