package interp

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestElemExprOpcodesAgreeWithTheDecoder is the check `opRefNull`/`opRefFunc` cite, and it is
// `TestSelectOpcodesAgreeWithTheDecoder`'s sibling for the same reason: this package holds a second
// copy of a fact `internal/binary`'s generated table already holds, sharing would put a table in the
// load-bearing spot for a two-line consumer, so the duplication stands and this is the tripwire.
//
// # Two authorities, not one, because the constants carry two facts
//
// A constant here asserts *which byte* and *which instruction*, and a control asking only one
// question passes on a defect in the other:
//
//   - **The name** — `binary.OpMnemonic` gives the reference's constructor for a byte
//     (`ref_null`, `ref_func`), so `opRefNull` naming `ref_func`'s byte fails here even though
//     both bytes are legal element-expression opcodes and both decode. This is the half that a
//     swapped pair fails, and swapping is the whole hazard: the two are adjacent-ish (0xd0, 0xd2)
//     and a transposition produces a module that decodes fine and evaluates `ref.func` as a null.
//   - **The immediate shape** — `ref.null` takes a heaptype, `ref.func` a func index, and both are
//     one byte in the encodings the suite uses, so the *names* alone would not catch a constant
//     pointing at `ref_is_null` (0xd1, no immediate). Asked of the decoder directly, since the
//     decoder is the authority both copies derive from and `constExprRef` reads its output.
//
// # The discriminator is what the byte *means* for the immediate, and it needed measuring
//
// The obvious discriminator — decode the bare byte and see which truncates — does not separate
// these two, because both take an immediate and both truncate. What separates them is that the
// immediate is read by *different productions*: `immHeapType` reads a reftype and rejects `0x00`
// with `gc: feature gate disabled` on the default board, while `immIdx` reads a u32 and accepts it
// as index 0. So `d0 00` is refused and `d2 00` decodes to `Imm0 = 0`, which is a difference no
// swap survives.
//
// Printed rather than reasoned, and the print corrected a draft: `d0 00` was expected to be
// `malformed reference type` (that is `d0 7f`), and the gate arm is what it actually is — 0x00 is
// `nofunc`, a Wasm 3.0 heaptype, so the gate is the honest refusal and *not* a malformedness claim
// (the gates-never-manufacture-malformedness rule, working). What matters to this control is only
// that the byte is refused where the other is accepted, but a comment asserting the wrong reason
// would be the defect-stated-as-the-rule shape.
//
// # The last block joins the two halves, and it does *not* pin the immediate's field
//
// It runs the decoder's own output through `constExpr`, so a byte the decoder accepts is shown to
// reach a reference. That is the seam between the two packages and it is the reason the block is
// here.
//
// A draft of this paragraph claimed the block pinned the index reaching `Imm0`, and the
// falsification pass said otherwise: rewriting the reader to take `Imm1` leaves this test **green**,
// because the index used here is 0 and both fields hold 0. The claim was about a mechanism rather
// than an observation of one — the same slip `TestI32ConstOccupiesItsSlotZeroExtended`'s comment
// records. (It was written with the name elided to an ellipsis, which
// `TestEveryCitedTestNameResolves` refused: an abbreviated identifier is a citation no resolver can
// follow, so the eliding *is* the drift.) What actually pins the field is
// TestElemExprIndexReachesTheRef below, with a
// nonzero index, and that is where the assertion belongs.
//
// **Re-pointed by #241, not rewritten.** The reader this asked about was `constExprRef`, a
// two-pattern matcher, and it is now `constExpr` running the expression through the interpreter — so
// the *field* is read by `opRefFunc`'s arm in exec.go rather than by a matcher, and the Imm1
// falsification above is a mutation of that arm. The risk is unchanged and so is this control: a
// tripwire whose subject dissolves is re-pointed, never closed. What did change is the negative
// direction, which moved to the test below along with its subject.
func TestElemExprOpcodesAgreeWithTheDecoder(t *testing.T) {
	// The mnemonic half. Read from the generated table, so a regeneration that renumbered either
	// opcode fails here rather than silently re-pointing this package's constants.
	for _, c := range []struct {
		op   uint32
		want string
	}{
		{opRefNull, "ref_null"},
		{opRefFunc, "ref_func"},
	} {
		got, ok := binary.OpMnemonic(c.op)
		if !ok {
			t.Errorf("%#02x has no row in the generated opcode table, so it is not an "+
				"instruction the decoder knows", c.op)
			continue
		}
		if got != c.want {
			t.Errorf("%#02x is %q in the reference, not %q — this package's constants name the "+
				"wrong instructions", c.op, got, c.want)
		}
	}

	// The immediate half, asked of the decoder. A passive element segment with expression-form
	// elements (flags 5) is the smallest wrapper that puts a const-expr on the wire, and it is
	// assembled here rather than encoded from wat for TestSelectOpcodesAgreeWithTheDecoder's
	// reason: the question is what the decoder says about a byte, and going through the text
	// encoder would ask this package's sibling instead.
	elemExpr := func(body ...byte) []byte {
		img := []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
			0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
			0x03, 0x02, 0x01, 0x00, // function: one func, type 0
			0x04, 0x04, 0x01, 0x70, 0x00, 0x01, // table: one funcref table, min 1
		}
		expr := append(append([]byte{}, body...), 0x0b)  // the const-expr's END
		seg := append([]byte{0x05, 0x70, 0x01}, expr...) // flags 5, funcref, one element
		img = append(img, append([]byte{0x09, byte(len(seg) + 1), 0x01}, seg...)...)
		return append(img, 0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b) // code: one empty body
	}

	// `ref.null func` — elem.wast:43's `(item (ref.null func))` — with 0x70 the funcref heaptype.
	if _, err := binary.DecodeModule(elemExpr(opRefNull, 0x70)); err != nil {
		t.Errorf("opRefNull (%#02x) with a funcref heaptype does not decode: %v", opRefNull, err)
	}
	// The discriminator: 0x00 is a heaptype the default board gates and a perfectly good func
	// index. Both directions, since either alone is satisfied by a swapped pair.
	if _, err := binary.DecodeModule(elemExpr(opRefNull, 0x00)); err == nil {
		t.Errorf("opRefNull (%#02x) followed by 0x00 decoded, so its immediate is being read as an "+
			"index rather than a heaptype — the two constants are assigned the wrong way round",
			opRefNull)
	} else if !strings.Contains(err.Error(), "gc") {
		// `0x00` is **type index 0**, not `nofunc` — this sentence said `nofunc` and was a false
		// premise under a true conclusion, the shape #360's second finding is about. `sleb(33)`
		// reads 0x00 as 0, non-negative, so it is the *indexed* heaptype form
		// (`sections.go:914-926`); `nofunc` is `-0x0D`, wire byte 0x73. The gate is the same
		// either way, which is exactly why the error nobody checked kept the wrong name alive.
		t.Errorf("opRefNull with heaptype 0x00 was refused as %v, and 0x00 is type index 0 — the "+
			"indexed heaptype form, gc-gated: the expected refusal is that gate, so this byte is "+
			"not reaching immHeapType", err)
	}
	m, err := binary.DecodeModule(elemExpr(opRefFunc, 0x00))
	if err != nil {
		t.Fatalf("opRefFunc (%#02x) with index 0 does not decode, so its immediate is not a func "+
			"index: %v", opRefFunc, err)
	}
	// And the decoder's own output evaluates — the seam, not the field. See the comment above for
	// why the field is asserted next door instead.
	if len(m.Elems) != 1 || len(m.Elems[0].Exprs) != 1 {
		t.Fatalf("the wrapper produced %d segment(s), want 1 with one expression", len(m.Elems))
	}
	es := m.Elems[0]
	v, err := (&Instance{}).constExpr(es.Exprs[0], es.ElemType, "an element expression")
	if err != nil {
		t.Fatalf("constExpr refused a `ref.func 0` element expression: %v", err)
	}
	got := v.ref
	if got.Null || got.Addr != 0 {
		t.Errorf("`ref.func 0` evaluated to %+v, want a non-null reference to function 0", got)
	}
}

// TestElemExprIndexReachesTheRef is the accept-direction half of the row above: the *index* the
// expression names has to survive into the reference, not just the opcode.
//
// Separate from the agreement test because it has a different subject and a different oracle.
// The agreement test asks the decoder about bytes; this asks `constExpr` about a value, and a
// nonzero index is what distinguishes "reads Imm0" from "reads the right field and then ignores
// it". `elem.wast:11` uses `(ref.func $f)` and `(ref.func $g)` in one segment for exactly this
// reason — two different functions — but the suite cannot fail an engine that resolves both to
// function 0 unless a vector then *calls* through the second slot, so the positive assertion is
// what covers it.
//
// The nonzero row is the whole point and it was earned by falsification: rewriting `opRefFunc`'s arm
// to read `Imm1` fails `ref.func 7` here and nothing else in the package. `ref.func 0` stays
// because a partition needs its protected side — it marks where the damage from such a change
// would *not* reach, which is every element segment naming function 0.
//
// # The negative direction inverted at #241, and inverting it is the point rather than a casualty
//
// This test used to end by asserting that a `global.get` element expression is **refused** — the
// contract `constExprRef` had, whose two patterns covered `ref.null` and `ref.func` and whose
// honesty was in naming everything else unsupported rather than defaulting it to null (a null where
// a function belonged is `uninitialized element` at the call: a wrong trap, not a missing feature).
// #241 replaced that matcher with an evaluator, so the form is now *evaluated*, and the old
// assertion would fail for the right reason.
//
// So the row is re-pointed to the capability that replaced it: the same expression, against an
// instance that actually has the global, yields that global's reference. That is what `elem.wast`'s
// import-initialized segments want and it is the accept-direction half of #241 — the half §9 G-3 says
// the suite cannot score, since a wrongly-*rejected* module is a fail the board attributes to a
// missing feature. The refusal direction did not disappear; it moved down a layer to `globalFor`,
// which is where an out-of-range index is #9's verdict, and the second row asserts it there.
func TestElemExprIndexReachesTheRef(t *testing.T) {
	// **Not `&Instance{}` per row, on purpose (0017 Q2, grave #163).** `ref.func`'s arm sets
	// `Inst` to the instance it evaluates against — that is the fix's whole shape — so a `ref`
	// pins its own struct only against a shared, named instance, and a fresh `&Instance{}` per
	// call would silently pass by comparing against whatever pointer that row happened to
	// allocate rather than asserting the field at all.
	in := &Instance{}
	for _, c := range []struct {
		what string
		expr []binary.Instr
		want ref
	}{
		{"ref.func 0", []binary.Instr{{Op: opRefFunc, Imm0: 0}, {Op: opEnd}}, ref{Addr: 0, Inst: in}},
		{"ref.func 7", []binary.Instr{{Op: opRefFunc, Imm0: 7}, {Op: opEnd}}, ref{Addr: 7, Inst: in}},
		{"ref.null", []binary.Instr{{Op: opRefNull}, {Op: opEnd}}, ref{Null: true}},
	} {
		got, err := in.constExpr(c.expr, binary.FuncRef, "an element expression")
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if got.ref != c.want {
			t.Errorf("%s evaluated to %+v, want %+v", c.what, got.ref, c.want)
		}
	}

	// The form the matcher could not run, now run. `global.get 0` against an instance holding one
	// funcref global must produce that global's value — a positive assertion, because the failure
	// mode #241 fixes is a *rejection*, and a rejection is invisible on a board that reads it as a
	// missing feature.
	held := ref{Addr: 4, Inst: in}
	withGlobal := &Instance{globals: []*global{{typ: binary.FuncRef, ref: held}}}
	got, err := withGlobal.constExpr(
		[]binary.Instr{{Op: 0x23, Imm0: 0}, {Op: opEnd}}, binary.FuncRef, "an element expression")
	if err != nil {
		t.Errorf("a `global.get 0` element expression was refused against an instance that has "+
			"global 0: %v — the whole of #241 is that this form evaluates", err)
	} else if got.ref != held {
		t.Errorf("`global.get 0` evaluated to %+v, want the global's own value %+v", got.ref, held)
	}

	// And the refusal direction, one layer down where it now lives: an index no global answers is
	// #9's verdict, never a silent null.
	if _, err := in.constExpr(
		[]binary.Instr{{Op: 0x23, Imm0: 0}, {Op: opEnd}}, binary.FuncRef, "an element expression",
	); err == nil {
		t.Error("`global.get 0` evaluated against an instance with no globals; an index nothing " +
			"answers must be reported, because defaulting to null makes it `uninitialized " +
			"element` at the call")
	}
}

// TestTableGetSetRoundTrip pins the six arms added for #7's opcode-arm stream: table.get,
// table.set, table.size, ref.null, ref.is_null, ref.func. Every value stays inside the wasm
// body rather than crossing `Instance.Invoke`'s Go boundary, which refuses a ref-typed
// parameter or result (interp.go's own check) — the same reason every corpus vector for these
// opcodes frames a ref value as a local born from `ref.null`/`ref.func`, never as an argument.
func TestTableGetSetRoundTrip(t *testing.T) {
	in := invoke1t(t, `(module
		(table $t 2 funcref)
		(func $eleven (result i32) (i32.const 11))
		(elem (table $t) (i32.const 0) func $eleven)

		(func (export "size") (result i32) (table.size $t))
		(func (export "is-null-0") (result i32) (ref.is_null (table.get $t (i32.const 0))))
		(func (export "is-null-1") (result i32) (ref.is_null (table.get $t (i32.const 1))))
		(func (export "call-0") (result i32) (call_indirect (type 0) (i32.const 0)))
		(func (export "set-and-call") (result i32)
			(table.set $t (i32.const 1) (ref.func $eleven))
			(call_indirect (type 0) (i32.const 1))
		))`)

	rows := []struct {
		fn   string
		want int32
	}{
		{"size", 2},
		{"is-null-0", 0}, // slot 0: the elem segment's ref.func, not null
		{"is-null-1", 1}, // slot 1: unfilled, null
		{"call-0", 11},
	}
	for _, r := range rows {
		got, err := in.Invoke(r.fn)
		if err != nil {
			t.Errorf("%s: %v", r.fn, err)
			continue
		}
		if len(got) != 1 || int32(got[0].Bits) != r.want {
			t.Errorf("%s = %v, want %d", r.fn, got, r.want)
		}
	}

	// table.set followed by table.get through call_indirect: a slot filled by ref.func at
	// runtime (not by an element segment) must be callable, which is the write half table.get
	// alone cannot exercise.
	got, err := in.Invoke("set-and-call")
	if err != nil {
		t.Fatalf("set-and-call: %v", err)
	}
	if len(got) != 1 || int32(got[0].Bits) != 11 {
		t.Errorf("set-and-call = %v, want 11", got)
	}
}

// TestTableGetSetOutOfBoundsReportsTheTableSentinel pins the reject direction and the sentinel
// each opcode's own text uses — table.get's is table.ml's plain `Bounds` ("out of bounds table
// access"), which grave-shape reasoning could plausibly confuse with call_indirect's
// `undefined element N` (both are the same underlying bounds check, wrapped with different
// text at different call sites in the reference). Falsified by routing table.get through
// `load` instead of `get` and confirming the wrong sentinel appears.
func TestTableGetSetOutOfBoundsReportsTheTableSentinel(t *testing.T) {
	in, trap := instantiate1(t, `(module
		(table $t 1 funcref)
		(func (export "get") (result funcref) (table.get $t (i32.const 5)))
		(func (export "set") (table.set $t (i32.const 5) (ref.null func))))`)
	if trap != nil {
		t.Fatalf("trap: %v", trap)
	}
	for _, fn := range []string{"get", "set"} {
		_, err := in.Invoke(fn)
		if err == nil {
			t.Fatalf("%s: want a trap for an out-of-bounds index", fn)
		}
		if !strings.Contains(err.Error(), "out of bounds table access") {
			t.Errorf("%s: %v, want \"out of bounds table access\" (not call_indirect's "+
				"\"undefined element\", the sibling sentinel for the same bounds check)", fn, err)
		}
	}
}

// TestTableGrowFillRoundTrip pins table.grow and table.fill: grow appends n slots filled with
// the given ref and returns the pre-growth size, fill overwrites an existing run. Both stay
// within Invoke's boundary via ref.null/ref.func locals, per the same rule the round-trip test
// above states.
func TestTableGrowFillRoundTrip(t *testing.T) {
	in := invoke1t(t, `(module
		(table $t 1 funcref)
		(func $eleven (result i32) (i32.const 11))

		(func (export "grow") (result i32) (table.grow $t (ref.null func) (i32.const 3)))
		(func (export "size") (result i32) (table.size $t))
		(func (export "fill-with-eleven") (table.fill $t (i32.const 1) (ref.func $eleven) (i32.const 3)))
		(func (export "call") (param $i i32) (result i32) (call_indirect (type 0) (local.get $i))))`)

	got, err := in.Invoke("grow")
	if err != nil {
		t.Fatalf("grow: %v", err)
	}
	if len(got) != 1 || int32(got[0].Bits) != 1 {
		t.Fatalf("grow = %v, want 1 (the pre-growth size)", got)
	}

	got, err = in.Invoke("size")
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if len(got) != 1 || int32(got[0].Bits) != 4 {
		t.Fatalf("size = %v, want 4 (1 + 3 grown)", got)
	}

	if _, err := in.Invoke("fill-with-eleven"); err != nil {
		t.Fatalf("fill-with-eleven: %v", err)
	}
	for _, i := range []int32{1, 2, 3} {
		got, err := in.Invoke("call", I32(i))
		if err != nil {
			t.Errorf("call(%d): %v", i, err)
			continue
		}
		if len(got) != 1 || int32(got[0].Bits) != 11 {
			t.Errorf("call(%d) = %v, want 11 — table.fill did not reach slot %d", i, got, i)
		}
	}
	// Slot 0 was never filled and must stay null — fill's own bound (i=1, n=3) must not have
	// been rounded or off-by-one into slot 0.
	if _, err := in.Invoke("call", I32(0)); err == nil {
		t.Error("call(0): want a trap, slot 0 was never filled and table.fill must not have reached it")
	}
}

// TestTableFillOutOfBoundsReportsTheTableSentinel pins fill's reject direction, which the
// round-trip test above never exercises (every row it fills is in bounds). `n` extends past the
// table's declared size, so the whole run must be refused before any slot is written — verified
// by checking a slot *inside* the requested range stayed untouched, which is the corpus's own
// pattern for table.fill's atomicity (`eval.ml`'s bound check runs before the store loop, never
// mid-loop).
func TestTableFillOutOfBoundsReportsTheTableSentinel(t *testing.T) {
	in := invoke1t(t, `(module
		(table $t 2 funcref)
		(func $eleven (result i32) (i32.const 11))
		(func (export "fill") (table.fill $t (i32.const 1) (ref.func $eleven) (i32.const 5)))
		(func (export "call") (param $i i32) (result i32) (call_indirect (type 0) (local.get $i))))`)

	if _, err := in.Invoke("fill"); err == nil {
		t.Fatal("fill: want a trap, n=5 from offset 1 exceeds the table's size of 2")
	} else if !strings.Contains(err.Error(), "out of bounds table access") {
		t.Errorf("fill: %v, want \"out of bounds table access\"", err)
	}

	// The whole run must be refused before any slot is written — slot 1 is inside the table's
	// bounds and inside the requested (out-of-bounds) run, and must still be untouched.
	if _, err := in.Invoke("call", I32(1)); err == nil {
		t.Error("call(1): want a trap, an out-of-bounds fill must not have written slot 1 " +
			"before discovering the overrun")
	}
}

// TestTableGrowFailureReturnsMinusOneNotATrap pins table.grow's total-function contract, the
// same shape memory.grow's own control names: growing past the declared maximum reports -1 in
// the result rather than trapping, so a table.grow that traps on overflow would convert
// assert_return vectors into assert_trap answers on the wrong channel.
func TestTableGrowFailureReturnsMinusOneNotATrap(t *testing.T) {
	in := invoke1t(t, `(module
		(table $t 1 2 funcref)
		(func (export "grow") (param $n i32) (result i32) (table.grow $t (ref.null func) (local.get $n))))`)

	got, err := in.Invoke("grow", I32(5))
	if err != nil {
		t.Fatalf("grow(5): got an error, want -1 in the result: %v", err)
	}
	if len(got) != 1 || int32(got[0].Bits) != -1 {
		t.Errorf("grow(5) = %v, want -1 (5 exceeds the declared max of 2)", got)
	}
}

// TestCallCheckesArityPerStack is the control for the grave this PR's own arms found: `invoke`'s
// post-call arity check counted only `st.num`'s delta, so *every* function returning a ref-typed
// value reported "declares 1 results and left 0 values on the stack" regardless of whether the
// ref side was correct — `table_get.wast`'s `is_null-funcref` (`ref.is_null (call $f3 …)`, `$f3`
// returning `funcref`) is the corpus's own specimen, unreachable before this PR because nothing
// produced a ref-typed result through plain `call`/`call_indirect` until `table.get`/`ref.func`
// had arms.
//
// Two rows, one per array, so a fix that repairs only one side is still caught: a function
// returning a numeric value round-trips through `call` (pins `st.num`'s count did not break),
// and a function returning a ref value does too (pins the fix, `st.refs`'s count).
func TestCallCheckesArityPerStack(t *testing.T) {
	in := invoke1t(t, `(module
		(table $t 1 funcref)
		(func $eleven (result i32) (i32.const 11))
		(elem (table $t) (i32.const 0) func $eleven)

		(func $getnum (result i32) (i32.const 5))
		(func $getref (result funcref) (table.get $t (i32.const 0)))

		(func (export "num") (result i32) (call $getnum))
		(func (export "ref-is-null") (result i32) (ref.is_null (call $getref))))`)

	got, err := in.Invoke("num")
	if err != nil {
		t.Fatalf("num: %v", err)
	}
	if len(got) != 1 || int32(got[0].Bits) != 5 {
		t.Errorf("num = %v, want 5", got)
	}

	got, err = in.Invoke("ref-is-null")
	if err != nil {
		t.Fatalf("ref-is-null: %v", err)
	}
	if len(got) != 1 || int32(got[0].Bits) != 0 {
		t.Errorf("ref-is-null = %v, want 0 (the elem segment filled slot 0, so it is not null)", got)
	}
}

// invoke1t is instantiate1 requiring success, the local shorthand this file's new rows share —
// distinct from memory_test.go's invoke1, which instantiates from source and invokes in one
// call; here every row invokes more than once against the same instance.
func invoke1t(t *testing.T, src string) *Instance {
	t.Helper()
	in, trap := instantiate1(t, src)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	return in
}
