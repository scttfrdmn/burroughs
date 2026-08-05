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
// It runs the decoder's own output through `constExprRef`, so a byte the decoder accepts is shown
// to reach a reference rather than an "element expression this engine does not evaluate yet". That
// is the seam between the two packages and it is the reason the block is here.
//
// A draft of this paragraph claimed the block pinned the index reaching `Imm0`, and the
// falsification pass said otherwise: rewriting `constExprRef` to read `Imm1` leaves this test
// **green**, because the index used here is 0 and both fields hold 0. The claim was about a
// mechanism rather than an observation of one — the same slip
// `TestI32ConstOccupiesItsSlotZeroExtended`'s comment records. (It was written with the name elided
// to an ellipsis, which `TestEveryCitedTestNameResolves` refused: an abbreviated identifier is a
// citation no resolver can follow, so the eliding *is* the drift.) What actually pins the field is
// TestElemExprIndexReachesTheRef below, with a
// nonzero index, and that is where the assertion belongs.
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
		t.Errorf("opRefNull with heaptype 0x00 was refused as %v, and 0x00 is `nofunc`: the "+
			"expected refusal is the gc gate, so this byte is not reaching immHeapType", err)
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
	got, err := (&Instance{}).constExprRef(m.Elems[0].Exprs[0])
	if err != nil {
		t.Fatalf("constExprRef refused a `ref.func 0` element expression: %v", err)
	}
	if got.Null || got.Addr != 0 {
		t.Errorf("`ref.func 0` evaluated to %+v, want a non-null reference to function 0", got)
	}
}

// TestElemExprIndexReachesTheRef is the accept-direction half of the row above: the *index* the
// expression names has to survive into the reference, not just the opcode.
//
// Separate from the agreement test because it has a different subject and a different oracle.
// The agreement test asks the decoder about bytes; this asks `constExprRef` about a value, and a
// nonzero index is what distinguishes "reads Imm0" from "reads the right field and then ignores
// it". `elem.wast:11` uses `(ref.func $f)` and `(ref.func $g)` in one segment for exactly this
// reason — two different functions — but the suite cannot fail an engine that resolves both to
// function 0 unless a vector then *calls* through the second slot, so the positive assertion is
// what covers it.
//
// The nonzero row is the whole point and it was earned by falsification: rewriting `constExprRef`
// to read `Imm1` fails `ref.func 7` here and nothing else in the package. `ref.func 0` stays
// because a partition needs its protected side — it marks where the damage from such a change
// would *not* reach, which is every element segment naming function 0.
func TestElemExprIndexReachesTheRef(t *testing.T) {
	for _, c := range []struct {
		what string
		expr []binary.Instr
		want ref
	}{
		{"ref.func 0", []binary.Instr{{Op: opRefFunc, Imm0: 0}, {Op: opEnd}}, ref{Addr: 0}},
		{"ref.func 7", []binary.Instr{{Op: opRefFunc, Imm0: 7}, {Op: opEnd}}, ref{Addr: 7}},
		{"ref.null", []binary.Instr{{Op: opRefNull}, {Op: opEnd}}, ref{Null: true}},
	} {
		got, err := (&Instance{}).constExprRef(c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s evaluated to %+v, want %+v", c.what, got, c.want)
		}
	}
	// The negative side, because `constExprRef`'s whole contract is that anything it does not
	// evaluate is reported *by name* rather than treated as null: a null where a function belonged
	// is `uninitialized element` at the call, which is a wrong trap and not a missing feature.
	// `global.get` is the form the suite has that this does not run — elem.wast has segments
	// initialized from an imported global.
	if _, err := (&Instance{}).constExprRef([]binary.Instr{{Op: 0x23}, {Op: opEnd}}); err == nil {
		t.Error("a `global.get` element expression evaluated silently; an unevaluated form must be " +
			"reported, because defaulting to null makes it `uninitialized element` at the call")
	}
}
