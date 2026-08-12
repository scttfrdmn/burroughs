package interp

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestRung1OpcodesAgreeWithTheDecoder is TestElemExprOpcodesAgreeWithTheDecoder's sibling for the
// six constants #172's rung 1 adds, and it exists for that test's stated reason: this package holds
// a second copy of a fact `internal/binary`'s generated table already holds, and the duplication
// stands because sharing would put a table in the load-bearing spot for a handful of switch arms.
//
// # Scoped to the space, and the space here is "every constant this PR added"
//
// The domain is the table below, which is an enumeration — and that is the one thing this project's
// controls are told not to be. So the vacuity and completeness halves are asserted rather than
// assumed: the count is pinned at 6, and every entry's byte must be *distinct*, which is what a
// copy-paste error in the table itself would violate. An enumeration whose size and distinctness are
// checked is a sample that cannot silently shrink.
//
// The stronger scoping — reflecting over this package's constants — is not available in Go, there
// being no way to enumerate untyped consts. Stated rather than left as an apparent oversight, since
// *derive the domain, never enumerate it* is the standing rule and this is a declared exception with
// a reason rather than a lapse.
//
// # Both facts, because a constant carries two
//
// The mnemonic half catches a byte naming the wrong instruction; the immediate-shape half catches a
// byte naming an instruction whose immediates differ, which the mnemonics alone would pass if the
// generated table were regenerated with a rename. `br_on_null`/`br_on_non_null` (0xd5/0xd6) are the
// live hazard: adjacent bytes, identical immediate shape, and a transposition produces a module that
// decodes perfectly and branches on exactly the wrong polarity — every vector in both files fails,
// which is the good case, but nothing in *this* package would say why.
func TestRung1OpcodesAgreeWithTheDecoder(t *testing.T) {
	rows := []struct {
		op       uint32
		mnemonic string
		imms     int // how many immediates the generated table gives the byte
	}{
		{opRefEq, "ref_eq", 0},
		{opRefAsNonNull, "ref_as_non_null", 0},
		{opBrOnNull, "br_on_null", 1},
		{opBrOnNonNull, "br_on_non_null", 1},
		{opCallRef, "call_ref", 1},
		{opReturnCallRef, "return_call_ref", 1},
	}
	// Vacuity and distinctness, before anything is read: the table is the domain, so a table that
	// lost rows to a bad merge, or that names one byte twice, must fail here rather than assert less
	// than its name claims. *A comparison against an empty set succeeds.*
	if len(rows) != 6 {
		t.Fatalf("rung 1 is six opcodes (ref.eq, ref.as_non_null, br_on_null, br_on_non_null, "+
			"call_ref, return_call_ref) and this table has %d rows — a row was lost or added "+
			"without the count being reconsidered", len(rows))
	}
	seen := map[uint32]string{}
	for _, r := range rows {
		if prev, dup := seen[r.op]; dup {
			t.Errorf("%#02x appears twice in the table, as %s and as %s, so one of this "+
				"package's constants is not being checked at all", r.op, prev, r.mnemonic)
		}
		seen[r.op] = r.mnemonic
	}

	for _, r := range rows {
		got, ok := binary.OpMnemonic(r.op)
		if !ok {
			t.Errorf("%#02x has no row in the generated opcode table, so it is not an "+
				"instruction the decoder knows", r.op)
			continue
		}
		if got != r.mnemonic {
			t.Errorf("%#02x is %q in the generated table, and this package's constant calls it "+
				"%q — the constant names the wrong byte", r.op, got, r.mnemonic)
		}
	}

	// The immediate-shape half, asked of the decoder rather than of the table's `imms` field, so
	// that the authority is the code that actually reads the bytes. A no-immediate opcode decodes
	// standalone; a one-immediate opcode truncates without its operand. That separates
	// `ref.eq`/`ref.as_non_null` from the four label- and index-taking opcodes, which is the
	// partition the switch arms depend on: an arm reading `ins.Imm0` for an opcode that stages none
	// reads zero and branches to depth 0.
	for _, r := range rows {
		bare := decodesStandalone(t, r.op)
		if wantBare := r.imms == 0; bare != wantBare {
			t.Errorf("%#02x (%s): decoded standalone = %v, want %v — the constant names a byte "+
				"whose immediate shape differs from %s's, so a switch arm reading its immediates "+
				"is reading a different instruction's", r.op, r.mnemonic, bare, wantBare, r.mnemonic)
		}
	}
}

// decodesStandalone reports whether the opcode decodes as a complete instruction with no immediate
// bytes following it, by handing the decoder a minimal module whose only function body is that byte
// followed by END.
//
// The discriminator is that END is `0x0b`, which is **also a perfectly good LEB128 u32 equal to
// 11**. So a no-immediate opcode consumes nothing and meets its END; a label- or index-taking
// opcode swallows the END as its immediate and then runs off the end of the body. One byte
// separating the two shapes, and the same trick `TestElemExprOpcodesAgreeWithTheDecoder` uses with
// `0x00`-as-heaptype-versus-index.
//
// Assembled here rather than encoded from wat, for that test's stated reason: the question is what
// the *decoder* says about a byte, and going through the text encoder would ask this package's
// sibling instead.
//
// **GC on, deliberately.** All six are `gateGC` in `gatemap.go`, so asking the default-board decoder
// would get `gc: feature gate disabled` for every row, every call would return false, and the
// control would pass on `ref.eq`/`ref.as_non_null` for the wrong reason while failing the other four
// — a verdict about the gate wearing the shape of a verdict about immediates.
func decodesStandalone(t *testing.T, op uint32) bool {
	t.Helper()
	body := []byte{0x00, byte(op), byte(opEnd)} // no locals, the opcode, END
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
		0x03, 0x02, 0x01, 0x00, // function: one func, type 0
	}
	code := append([]byte{0x01, byte(len(body))}, body...) // one body, its size
	img = append(img, append([]byte{0x0a, byte(len(code))}, code...)...)

	d := &binary.Decoder{Features: binary.Features{GC: true}}
	_, err := d.DecodeModule(img)
	return err == nil
}

// TestRefEqAccountsForEveryRefField is the tripwire refEq's doc comment pre-registers: `ref` grows a
// field at every rung of #172's ladder (0020's `Obj *gcObj` at rung 2), and a comparison that
// silently ignores a new payload field is an **accept-direction** defect — two distinct structs
// reported `eq`, which §9 G-3 says a rejection corpus scores green by construction.
//
// # Why this is a reflection check and not a value-level probe
//
// The natural control — build two refs differing in one field, assert `refEq` says false — is
// *stillborn today and worse than useless tomorrow*. Today every non-null ref is a funcref or an
// exnref, so `refEq` returns `(false, ErrNotValidated)` for any such pair, and the probe passes
// while asserting nothing about comparison at all: the "not equal" comes from the layering-debt
// path, not from a field being read. Tomorrow, when `Obj` lands, two refs differing only in `Obj`
// still take that same path until `refEq` is extended — so the probe would keep passing through
// exactly the defect it was written for. That is the witness-correlated-with-subject shape: the
// control's verdict and the bug share a cause.
//
// Reflection breaks the correlation by asking a question `refEq`'s control flow cannot answer for
// itself — *does the struct have a field nobody has ruled on?* — which is a fact about the type, not
// about a code path that may not be reachable yet.
//
// # Both directions, plus a floor
//
// A field with no declaration is the defect this exists for. A declaration with no field is the
// *stale* direction and is checked too, because a rename that left `refEqTreatment` pointing at a
// gone field would otherwise leave this control silently asserting less than its name claims — the
// drifted-citation defect aimed at the package's own ledger. And the floor: `ref` having zero fields
// would make both directions vacuously agree, which is the empty-set agreement this project keeps
// tripping over, so the count is asserted non-trivial.
func TestRefEqAccountsForEveryRefField(t *testing.T) {
	declared, undeclared, stale := refFieldTreatments()

	// The floor first. Not `> 0` but a plausible size: `ref` has held at least Null plus a payload
	// since #196/#197, so a reading of one field means reflection found a different type than the
	// one this control names.
	if len(declared)+len(undeclared) < 2 {
		t.Fatalf("reflection found %d fields on `ref` (declared %v, undeclared %v) — `ref` has "+
			"carried a null flag plus at least one payload field since #196/#197, so this control "+
			"is reading something other than the struct it names and its agreement is vacuous",
			len(declared)+len(undeclared), declared, undeclared)
	}

	if len(undeclared) > 0 {
		t.Errorf(`refEq has no declared treatment for %d field(s) of ref: %v

This is the tripwire refEq's doc comment pre-registers, and it has fired because ref grew a
field. Decide what ref.eq does with it and record the decision in refEqTreatment:

  - A **payload** field (0020's Obj) must be COMPARED. Two structs are ref.eq iff they are the
    same allocation (aggr.ml registers no eq_ref' hook, so physical equality is the spec's own
    answer), and an i31 payload is compared STRUCTURALLY (i31.ml:20 is i1 = i2, not ==).
  - A field refEq deliberately ignores needs its reason in the map, not silence.

Leaving it undeclared is the accept-direction defect §9 G-3 names: refEq would report two
distinct references equal, every vector that asks would pass, and nothing on either board
would say so. Current claims:%s`, len(undeclared), undeclared, treatmentDump())
	}

	if len(stale) > 0 {
		t.Errorf(`refEqTreatment declares a treatment for %d field(s) ref does not have: %v

The stale direction, checked because a rename that left this map pointing at a gone field would
leave this control asserting less than its name claims while staying green — a citation to the
package's own ledger that no longer resolves. Drop the entry, or restore the field.`,
			len(stale), stale)
	}
}

// treatmentDump renders refEq's declared treatments for a failure message, so a fired control shows
// what the current claim *is* rather than only what is missing from it.
func treatmentDump() string {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(refEqTreatment)) {
		b.WriteString("\n    ")
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(refEqTreatment[name])
	}
	return b.String()
}
