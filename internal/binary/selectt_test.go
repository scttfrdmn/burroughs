// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package binary

import (
	"fmt"
	"testing"
)

// selectImage builds a one-function module whose body is a single `select` — `vec` is the
// annotation exactly as it appears on the wire, count byte included, or nil for the bare `0x1B`
// form.
//
// The body is not type-correct and deliberately so: this package has no validator, `select`'s
// operands are `assert_invalid`'s subject and not the decoder's, and pushing three constants first
// would make every case here depend on a second grammar for no gain. Modelled on
// `brOnCastImage` one file over.
func selectImage(vec []byte) []byte {
	body := []byte{0x00} // no locals
	if vec == nil {
		body = append(body, 0x1B)
	} else {
		body = append(body, 0x1C)
		body = append(body, vec...)
	}
	body = append(body, 0x0B) // end (body)

	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
		0x03, 0x02, 0x01, 0x00, // function: one func, type 0
	}
	code := append([]byte{0x01, byte(len(body))}, body...)
	return append(img, append([]byte{0x0a, byte(len(code))}, code...)...)
}

// selectAt finds the decoded `select` in a selectImage module and returns its instruction, its
// retained annotation, and whether one was filed.
func selectAt(t *testing.T, m *Module) (Instr, []ValType, bool) {
	t.Helper()
	fn := &m.Funcs[0]
	for i := range fn.Body {
		if fn.Body[i].Prefix != 0 {
			continue
		}
		if fn.Body[i].Op != 0x1B && fn.Body[i].Op != 0x1C {
			continue
		}
		v, ok := fn.SelectTypes(i)
		return fn.Body[i], v, ok
	}
	t.Fatal("no select in the decoded body — the image builder emitted something else")
	return Instr{}, nil, false
}

// TestSelectRetainsTheAnnotationIncludingItsIllegalArities is the decoder half of #294: the vector
// `immVecValType` used to read and drop is filed beside the body.
//
// **The arities the validator rejects are the cases this asserts hardest**, because they are the
// ones the retention exists for. `valid.ml:443` allows exactly one type; the two corpus vectors that
// convert are `select.wast:368`'s arity-0 `(result)` and `:373`'s arity-2 `(result i32 i32)`. Both
// are well-formed *encodings* — the arity rule is a typing rule, and a decoder enforcing it here
// would be manufacturing malformedness out of #9's work (the gates-never-manufacture-malformedness
// rule, applied to a validator rather than a gate). So the decoder must file a vector it knows to be
// unusable, and the validator must be the layer that says so.
func TestSelectRetainsTheAnnotationIncludingItsIllegalArities(t *testing.T) {
	d := &Decoder{Features: featuresAllOn(t)}
	for _, tc := range []struct {
		name string
		vec  []byte
		want []ValType
	}{
		// The bare opcode: no vector at all, which is the case `SelectTypes`' second result
		// exists to distinguish from the next one.
		{"0x1B, unannotated", nil, nil},

		// Arity 0 — `(select (result) …)`, select.wast:368. An empty annotation, *filed*.
		{"arity 0", []byte{0x00}, []ValType{}},

		{"arity 1, i32", []byte{0x01, 0x7F}, []ValType{I32}},
		{"arity 1, f64", []byte{0x01, 0x7C}, []ValType{F64}},
		{"arity 1, v128", []byte{0x01, 0x7B}, []ValType{V128}},
		{"arity 1, funcref", []byte{0x01, 0x70}, []ValType{FuncRef}},
		{"arity 1, externref", []byte{0x01, 0x6F}, []ValType{ExternRef}},

		// The indexed form, which is what `ref.wast:78` uses — `(select (result (ref 1)) …)`,
		// expecting `unknown type` from the validator's `check_resulttype`. Index 1 does not
		// exist in this module either, and the decoder files it regardless: resolving the index
		// is the validator's question, and a decoder that refused would answer it in the wrong
		// layer and with the wrong error.
		{"arity 1, (ref 1)", []byte{0x01, 0x64, 0x01}, []ValType{RefType(1, false)}},
		{"arity 1, (ref null 1)", []byte{0x01, 0x63, 0x01}, []ValType{RefType(1, true)}},

		// Arity 2 — select.wast:373. Order is asserted, not just length: a retention that
		// reversed the vector would agree on every symmetric case, which is every case in the
		// corpus.
		{"arity 2, i32 i64", []byte{0x02, 0x7F, 0x7E}, []ValType{I32, I64}},
		{"arity 3, i32 funcref f32", []byte{0x03, 0x7F, 0x70, 0x7D}, []ValType{I32, FuncRef, F32}},
	} {
		m, err := d.DecodeModule(selectImage(tc.vec))
		if err != nil {
			t.Errorf("%s: decode: %v — every case here is a well-formed encoding, and an "+
				"arity the validator rejects is still one the decoder must read", tc.name, err)
			continue
		}
		in, got, ok := selectAt(t, m)
		if tc.vec == nil {
			if ok {
				t.Errorf("%s: an annotation was filed (%v) for the opcode that carries none — "+
					"a consumer reading it would type the bare form against a vector the module "+
					"does not contain", tc.name, got)
			}
			if in.Op != 0x1B {
				t.Errorf("%s: decoded opcode %#02x, want 0x1B", tc.name, in.Op)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: no annotation filed — `SelectTypes`' second result says the decoder "+
				"dropped it, which is the state #294 exists to end", tc.name)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: filed %d types (%v), want %d (%v)", tc.name, len(got), got, len(tc.want), tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: type %d is %s, want %s", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// TestSelectImm0AgreesWithTheAnnotation is the bidirectional control two fields holding one fact are
// owed, and it is written in the diff that created the second field rather than after they drifted.
//
// `Imm0`'s low bit is the interpreter's dispatch input (#196/#197): reference operands live on
// `st.refs` and numeric ones on `st.num`, and a map lookup per `select` execution is the cost the bit
// exists to avoid. `Func.Selects` is the validator's input. **The same wire content, decoded twice
// into two places**, which is the pair that drifts — and a drift here is silent in the worst
// direction: the bit is what makes the interpreter's untagged slots sound, so a bit disagreeing with
// the annotation misdispatches a live reference as an integer with no verdict anywhere to say so.
//
// **The domain is derived from the decoder, not listed.** Every one of the 256 bytes is offered as a
// one-element annotation and the ones that decode *are* the single-byte valtype space — so a
// proposal adding a value type enters this control by existing, where a hand-written list would
// exempt it in silence (the scope-controls-to-the-space rule). The partition against
// `AbstractRefType`'s twelve is what keeps the derivation honest: exactly twelve of the accepted
// forms must be references and the rest not, so a byte silently changing class is a failure here and
// not a smaller number nobody reads.
func TestSelectImm0AgreesWithTheAnnotation(t *testing.T) {
	d := &Decoder{Features: featuresAllOn(t)}

	check := func(name string, vec []byte) {
		m, err := d.DecodeModule(selectImage(vec))
		if err != nil {
			t.Errorf("%s: decode: %v", name, err)
			return
		}
		in, got, ok := selectAt(t, m)
		if !ok {
			t.Errorf("%s: no annotation filed", name)
			return
		}
		// The cache's rule, stated as the code that computes it from the source of truth: the
		// *last* element's reference-ness, and false for an empty vector.
		want := len(got) > 0 && got[len(got)-1].IsRef()
		if bit := in.Imm0 != 0; bit != want {
			t.Errorf("%s: Imm0's bit is %v and the annotation %v says %v. The interpreter "+
				"dispatches on the bit and the validator on the vector, so this disagreement "+
				"is a reference executed as an integer on a module both layers accepted",
				name, bit, got, want)
		}
		// The bit is a bit. A staged word carrying anything else means the arm started staging
		// something wider here, which the next reader of `ins.Imm0 != 0` would read as "true".
		if in.Imm0 > 1 {
			t.Errorf("%s: Imm0 is %d, want 0 or 1 — the interpreter reads `!= 0` and would take "+
				"any other value as a reference annotation", name, in.Imm0)
		}
	}

	// Arity 0: no element, so no reference — and the case a "last element" reading crashes on if
	// it is written without the length guard.
	check("arity 0", []byte{0x00})

	// The derived single-byte domain.
	var singles, refs int
	for b := range 256 {
		vec := []byte{0x01, byte(b)}
		m, err := d.DecodeModule(selectImage(vec))
		if err != nil {
			continue
		}
		singles++
		_, got, _ := selectAt(t, m)
		if len(got) == 1 && got[0].IsRef() {
			refs++
		}
		check(fmt.Sprintf("arity 1, valtype byte %#02x", b), vec)
	}

	// The partition, against the constructor that owns the twelve. A count alone would pass on a
	// numeric byte quietly becoming a reference and on a reference quietly becoming numeric,
	// which are the two ways the bit's meaning can invert without the total moving.
	abstract := 0
	for b := range 256 {
		if _, ok := AbstractRefType(byte(b), false); ok {
			abstract++
		}
	}
	if refs != abstract {
		t.Errorf("%d of the %d single-byte valtypes are references and AbstractRefType derives %d "+
			"abstract heaptypes — the shorthand reftypes are exactly those twelve, so a "+
			"disagreement means a byte changed class and the dispatch bit changed meaning with it",
			refs, singles, abstract)
	}
	if want := abstract + 5; singles != want {
		t.Errorf("%d single-byte valtypes decode, want %d — the twelve shorthand reftypes plus "+
			"i32/i64/f32/f64/v128. A drop here is a value type this control silently stopped "+
			"covering; a rise is one it has started covering and the count is how anyone finds out",
			singles, want)
	}

	// The multi-byte forms, which the byte sweep above cannot reach: a parameterized reftype is a
	// prefix plus a nested heaptype, and the indexed one is what `ref.wast:78` annotates with.
	for _, tc := range []struct {
		name string
		vec  []byte
	}{
		{"(ref 1)", []byte{0x01, 0x64, 0x01}},
		{"(ref null 1)", []byte{0x01, 0x63, 0x01}},
		{"(ref extern)", []byte{0x01, 0x64, 0x6F}},
		{"(ref null any)", []byte{0x01, 0x63, 0x6E}},
	} {
		check(tc.name, tc.vec)
	}

	// Arity > 1, where the two readings of "is it a reference" come apart: the bit is the *last*
	// element's, so a vector ending in a reference sets it and one beginning with a reference does
	// not. No module reaches the interpreter this way — the validator's arity rule refuses both —
	// and the asymmetry is asserted anyway, because the day the reference lifts that rule
	// (`"not (yet) allowed"`) is the day this becomes an execution question.
	check("arity 2, i32 funcref", []byte{0x02, 0x7F, 0x70})
	check("arity 2, funcref i32", []byte{0x02, 0x70, 0x7F})
}
