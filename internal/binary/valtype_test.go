package binary

import (
	"errors"
	"strings"
	"testing"
)

// The four members of #88, which share one mechanism: *a production the reference reads with
// one function, read here by two, where the second is narrower.* `decodeValType` was a flat
// switch over seven bytes where `valtype` is `either [numtype; vectype; reftype]`
// (decode.ml:220-225) and `reftype` alone has fourteen forms; `immHeapType` read
// `decodeRefType` where the reference reads `heaptype`; and two segment-kind sentinels were
// named for fields the format does not have.
//
// Every one is **accept-direction or message-direction**, so the board cannot see any of
// them: the default lane's vectors are `assert_malformed` and its expected strings stop at
// the sentinel. The fixes moved 4162/0 to 4162/0. That is the reason this file exists —
// *a decoder that rejects valid modules is worse than one that misses an invalid one*
// (§9 G-3), and the oracle is silent on exactly that half.
//
// **The wrong reader was wrong in both directions, and only one was in the diagnosis.** #88
// measured the under-accept — `ref.null 0` rejected — and the over-accept turned up when the
// probe was pointed at the *fix*: `heaptype` has no `-0x1c`/`-0x1d` arm, so a decoder reading
// `reftype` there accepted `ref.null (ref null extern)`. One substitution was quietly right
// about a direction nobody had asked it about.
//
// # Falsification
//
// Each control below was verified by re-introducing the defect it names and watching it fail
// — *a green that survives the bug it names is a control in name only*. The interesting
// column is not that they fired but **what else did**, since a control's worth is the gap it
// closes. Six defects injected, one at a time, on this branch:
//
//	defect                                       sole witness?    spec board
//	valtype back to a flat switch                yes              green
//	valtype branches read a byte, not s7         no (blocktype)   green
//	immHeapType reads reftype                    yes              green
//	heaptype's gate checks removed               yes              green
//	heaptype's type-index gate ahead of the s33  no (five tests)  RED
//	the two element strings swapped              yes              green
//
// So **four of the six had no other witness in the package, and five of the six left the
// board entirely green** — which is the accept-direction blindness stated as a measurement
// rather than as an argument. The two exceptions are the informative ones. The byte-read is
// also caught by TestBlockTypeAlternationIsTheAuthority, because blocktype shares the
// alternation; the misplaced gate is caught by five tests *and* the board, because it
// declines `ref.null func` and the corpus is full of it. That is the shape to expect: a
// defect in the *reject* direction gets noticed, and one in the accept direction is on its
// own with whatever control was written for it.

// funcTypeParam wraps bytes as a functype's single parameter — the plainest `valtype`
// position in the format, and the one decodeCompType reaches with no gate of its own
// (`-0x20`, Wasm 1.0). A gated wrapper would make every row decline for the wrong reason.
func funcTypeParam(vt ...byte) []byte {
	payload := []byte{0x01, 0x60, 0x01} // 1 rectype; functype; 1 param
	payload = append(payload, vt...)
	return typeSection(append(payload, 0x00)) // 0 results
}

// refNullGlobal wraps bytes as `ref.null <heaptype>` in a global initialiser: the only
// `heaptype` position this decoder reaches, function bodies being #7's.
//
// The global's own type is `externref` (0x6F, Wasm 2.0) and immutable, so nothing in the
// wrapper is gated either. The section size is derived from the payload for typeSection's
// stated reason — a hand-written length tests the size mechanism instead of the grammar.
func refNullGlobal(ht ...byte) []byte {
	payload := []byte{0x01, 0x6F, 0x00, 0xD0} // 1 global; externref; const; ref.null
	payload = append(payload, ht...)
	payload = append(payload, 0x0B) // end
	return append([]byte{
		0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
		0x06, byte(len(payload)),
	}, payload...)
}

// TestValTypeAlternationIsTheReference pins `valtype` as the reference's three-way
// alternation, and it is cited from binary.go and sections.go because two of the facts below
// are counter-intuitive enough that a reader would otherwise re-derive them wrongly.
//
// The accept half is the finding: the old switch took seven bytes, `either
// [numtype; vectype; reftype]` takes seventeen, and the seven were a strict subset. Measured
// over all 256 first bytes at this position with every gate on, accept went **7 → 17**.
//
// The reject half is the message. There is **no `malformed value type` string anywhere in
// decode.ml** — every branch has its own text (:172, :177, :218) — and `either` returns the
// *last* branch's error (:126-131), so a byte that is no value type at all is reported as
// `malformed reference type`. Not "value type", which the reference never emits, and not
// "number type", which is where the alternation started.
func TestValTypeAlternationIsTheReference(t *testing.T) {
	on := featuresAllOn(t)

	for _, tc := range []struct {
		name string
		vt   []byte
		gate string // "": ungated. otherwise the feature whose decline must name it.
		bad  error  // non-nil: malformed in every configuration
	}{
		// numtype, the first branch — Wasm 1.0, and asserted ungated because a gate here
		// rejects every module in the corpus.
		{"i32", []byte{0x7F}, "", nil},
		{"i64", []byte{0x7E}, "", nil},
		{"f32", []byte{0x7D}, "", nil},
		{"f64", []byte{0x7C}, "", nil},

		// vectype, the second branch: one form, and SIMD's only value type.
		{"v128", []byte{0x7B}, "simd", nil},

		// reftype's Wasm 2.0 pair — the two the old switch had.
		{"funcref", []byte{0x70}, "", nil},
		{"externref", []byte{0x6F}, "", nil},

		// reftype's other twelve. Every one of these was reported malformed before #88,
		// and the spec defines all twelve, so every one was a wrongly-rejected module.
		//
		// **Two of them name a different gate, as of #395.** `exnref` and `nullexnref` are the
		// exception proposal's value type (Exceptions.md:337-349) and were asserted `"gc"` here
		// for three PRs — which passed, because the byte *was* gated on GC and this column only
		// ever asked whether the named gate declines it. Getting the wrong answer required the
		// implementation and the expectation to be wrong together, which is what a hand-written
		// expectation next to a hand-written arm buys.
		{"nullexnref", []byte{0x74}, "exception handling", nil},
		{"nullfuncref", []byte{0x73}, "gc", nil},
		{"nullexternref", []byte{0x72}, "gc", nil},
		{"nullref", []byte{0x71}, "gc", nil},
		{"anyref", []byte{0x6E}, "gc", nil},
		{"eqref", []byte{0x6D}, "gc", nil},
		{"i31ref", []byte{0x6C}, "gc", nil},
		{"structref", []byte{0x6B}, "gc", nil},
		{"arrayref", []byte{0x6A}, "gc", nil},
		{"exnref", []byte{0x69}, "exception handling", nil},

		// The parameterized prefixes, each followed by a nested heaptype — so a *type
		// index* is legal two productions down, which is the deepest thing the fix reaches.
		{"(ref null extern)", []byte{0x63, 0x6F}, "gc", nil},
		{"(ref extern)", []byte{0x64, 0x6F}, "gc", nil},
		{"(ref null 0)", []byte{0x63, 0x00}, "gc", nil},
		{"(ref 1)", []byte{0x64, 0x01}, "gc", nil},

		// No branch takes these, and the surviving message is the *last* branch's.
		{"0x66 is no valtype", []byte{0x66}, "", ErrMalformedRefType},
		{"0x00 is no valtype", []byte{0x00}, "", ErrMalformedRefType},
		{"0x40 is no valtype", []byte{0x40}, "", ErrMalformedRefType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := funcTypeParam(tc.vt...)

			if tc.bad != nil {
				// Both configurations, because *gates never manufacture malformedness* has a
				// second face: a gate must not relabel one either. A byte no branch accepts
				// is malformed whatever the features say, with the same string.
				for _, c := range []struct {
					lane string
					f    Features
				}{{"all gates on", on}, {"every gate off", Features{}}} {
					_, err := (&Decoder{Features: c.f}).DecodeModule(mod)
					t.Logf("%s: % x -> %v", c.lane, tc.vt, err)
					if !errors.Is(err, tc.bad) {
						t.Errorf("%s: got %v, want %v — `either` returns the *last* branch's "+
							"error (decode.ml:126-131) and reftype is last, so this is the "+
							"reference's answer even though the byte is no reference type; "+
							"there is no `malformed value type` string in decode.ml at all",
							c.lane, err, tc.bad)
					}
				}
				return
			}

			// The accept direction: the half no `assert_malformed` vector can cover.
			if _, err := (&Decoder{Features: on}).DecodeModule(mod); err != nil {
				t.Fatalf("all gates on: got %v, want accept — the reference's valtype has a "+
					"branch for this form, so rejecting it is the accept-direction defect the "+
					"board is blind to by construction (§9 G-3)", err)
			}

			off := on
			switch tc.gate {
			case "":
				if _, err := (&Decoder{}).DecodeModule(mod); err != nil {
					t.Errorf("every gate off: got %v, want accept — this form is not gated, and "+
						"gating it would reject modules the corpus contains", err)
				}
				return
			case "simd":
				off.SIMD = false
			case "gc":
				off.GC = false
			case "exception handling":
				off.ExceptionHandling = false
			default:
				t.Fatalf("unknown gate %q", tc.gate)
			}

			_, err := (&Decoder{Features: off}).DecodeModule(mod)
			t.Logf("%s off: % x -> %v", tc.gate, tc.vt, err)
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("%s off: got %v, want ErrFeatureDisabled — the form is well-formed per "+
					"Wasm 3.0, so what declines it is the engine's own configuration and it must "+
					"say so; a spec malformed-string here is a gate manufacturing malformedness "+
					"(#5)", tc.gate, err)
			}
			if !strings.Contains(err.Error(), tc.gate) {
				t.Errorf("%s off: %q does not name the feature", tc.gate, err)
			}
		})
	}
}

// TestValTypeBranchesReadASignedLEB pins the *width* of all three branches, which is grave
// #36's lesson applied one production over.
//
// `numtype`, `vectype`, and `reftype` each read `s7` (decode.ml:167, :175, :203), so an
// overlong encoding of a legal form is a **LEB fault** and not a bogus-form fault. A byte
// read would report `malformed number type: 0xff` for `\ff\7f` — the right verdict class
// carrying the wrong string, which is the half of an error no expected string reads.
//
// Derived, in PR #37's sense: `binary-leb128.wast` asserts malformedness for overlong
// encodings at *other* fields, and the inference is that a valtype position obeys the same
// rule because the reference reads it with the same `sN 7`. No vector states it, because no
// vector puts an overlong LEB at a valtype position — which is why it is stated here.
func TestValTypeBranchesReadASignedLEB(t *testing.T) {
	d := &Decoder{Features: featuresAllOn(t)}
	for _, tc := range []struct {
		name string
		in   []byte
		want error
	}{
		// A legal form in two bytes. s7's whole width budget is one byte, so the
		// continuation bit is the fault and no branch gets as far as the form.
		{"overlong i32 (-0x01)", []byte{0xFF, 0x7F}, ErrLEBTooLong},
		{"overlong v128 (-0x05)", []byte{0xFB, 0x7F}, ErrLEBTooLong},
		{"overlong funcref (-0x10)", []byte{0xF0, 0x7F}, ErrLEBTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &reader{b: tc.in, eof: ErrPayloadEnd}
			err := d.decodeValType(r)
			t.Logf("% x -> off=%d err=%v", tc.in, r.off, err)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v — every branch reads s7, so a redundant encoding of "+
					"a legal form is a LEB fault; a byte read reports a bogus form and scores "+
					"the wrong string (grave #36)", err, tc.want)
			}
			if r.off == 0 {
				t.Error("consumed nothing — a zero-progress read")
			}
		})
	}
}

// TestHeapTypeIsNotRefType is the both-directions control on #88's fourth member.
//
// `immHeapType` read `decodeRefType`. The two productions are **not** nested — each has an
// arm the other lacks — so one wrong call site under-accepted at one end and over-accepted
// at the other, and neither end is visible to a board of malformed vectors:
//
//	heaptype has, reftype lacks:  UseHT (typeuse s33) — a type index (decode.ml:182)
//	reftype has, heaptype lacks:  -0x1c / -0x1d, the parameterized prefixes (:216-217)
//
// So `ref.null 0` was rejected `malformed reference type: 0x00` and
// `ref.null (ref null extern)` was accepted, by one substitution, at once. Printed rather
// than reasoned about: the two readers sit forty lines apart and read almost alike, which is
// how the substitution looked correct in the first place.
func TestHeapTypeIsNotRefType(t *testing.T) {
	d := &Decoder{Features: featuresAllOn(t)}
	for _, tc := range []struct {
		name string
		ht   []byte
		ok   bool
		why  string
	}{
		// The under-accept direction: heaptype's first branch, which reftype has no arm for.
		{
			"type index 0",
			[]byte{0x00},
			true,
			"`UseHT (typeuse s33 s)` (decode.ml:182) — a non-negative s33 is a type index; " +
				"this is `ref.null 0` and it was rejected `malformed reference type: 0x00`",
		},
		{
			"type index 1",
			[]byte{0x01},
			true,
			"the discriminator is negativity at width 33, not the byte's value",
		},
		{
			"type index 129, two bytes of s33",
			[]byte{0x81, 0x01},
			true,
			"s33 is a LEB of up to five bytes, so an index above 127 spans two — on these " +
				"exact bytes reftype's sleb(7) reported `integer representation too long`, " +
				"an error naming a width the position does not have",
		},

		// The forms both productions share, so the fix is not a relabelling.
		{"func", []byte{0x70}, true, "-0x10, in both"},
		{"extern", []byte{0x6F}, true, "-0x11, in both"},
		{"any", []byte{0x6E}, true, "-0x12, in both"},
		{"exn", []byte{0x69}, true, "-0x17, the last abstract form"},

		// The over-accept direction: reftype's prefixes, which heaptype does not have. This
		// half was not in #88's diagnosis; it surfaced from probing the fix.
		{
			"(ref null extern) prefix is not a heaptype",
			[]byte{0x63, 0x6F},
			false,
			"-0x1d belongs to `reftype` (decode.ml:217) and heaptype's switch has no such " +
				"case, so `ref.null (ref null extern)` is malformed — it **decoded** before " +
				"#88, and an over-accept is as invisible on this board as an under-accept",
		},
		{
			"(ref extern) prefix is not a heaptype",
			[]byte{0x64, 0x6F},
			false,
			"-0x1c likewise (decode.ml:216)",
		},

		// Neither, so the message must name the production the position actually has.
		{"0x66 is neither", []byte{0x66}, false, "heaptype's own fallthrough (decode.ml:197)"},
		{"0x7f is neither", []byte{0x7F}, false, "i32 is a valtype; there is no i32 heaptype"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.DecodeModule(refNullGlobal(tc.ht...))
			t.Logf("% x -> %v", tc.ht, err)
			switch {
			case tc.ok && err != nil:
				t.Errorf("got %v, want accept\n\t%s", err, tc.why)
			case !tc.ok && err == nil:
				t.Errorf("accepted, want rejection\n\t%s", tc.why)
			case !tc.ok && !errors.Is(err, ErrMalformedHeapType):
				t.Errorf("got %v, want ErrMalformedHeapType — the position is a heaptype, so "+
					"its fallthrough owns the message; reporting reftype's names a production "+
					"the format does not have here\n\t%s", err, tc.why)
			}
		})
	}
}

// TestHeapTypeGatesFormsNotThePosition is the gate-placement control the substitution
// created the need for, and what it replaces is a *premise* rather than a check.
//
// `decodeHeapType` carried no gate checks, on the reasoning — sound when written — that it
// was reached only from `decodeRefType`, which gates before descending. That premise died
// the moment `immHeapType` began calling it directly, because `ref.null` is a Wasm 2.0
// opcode with no gate of its own, so the gate state at entry is no longer known. *A
// deferral outlives its reason silently.*
//
// Without the checks, gates off, `ref.null 0` and `ref.null any` decode clean — the
// accept-and-ignore half of the #5 ruling, and **neither lane sees it**: the default lane's
// vectors are `assert_malformed`, and the all-on lane's `Gated == 0` requirement is
// satisfied trivially by a gate that never fires. That is #48's hole exactly.
//
// The assertion is the *partition*: the gate is on the **forms**, not on the position, so
// `ref.null extern` must decode with every gate off and `ref.null 0` must not. A check at
// the position declines both. A check ahead of the s33 discriminator also declines both,
// and unrecoverably — `either` propagates `ErrFeatureDisabled` without backtracking (#86),
// so `ref.null extern`, whose byte is negative at s33 and belongs to the *next* branch,
// would never reach it.
func TestHeapTypeGatesFormsNotThePosition(t *testing.T) {
	on := featuresAllOn(t)

	// The gate is a **name**, not a bool, as of #395. It was `gate bool` meaning "GC off must
	// decline", and under that column `ref.null exn` asserted the true thing for the wrong
	// reason: the byte declined with GC off, and the column had no way to say it should not
	// have. A boolean can only ask whether some gate fires; the defect was *which*.
	off := func(gate string) Features {
		f := on
		switch gate {
		case "gc":
			f.GC = false
		case "exception handling":
			f.ExceptionHandling = false
		default:
			t.Fatalf("unknown gate %q — a row naming a gate this helper cannot turn off would "+
				"run against an all-on decoder and pass by asking nothing", gate)
		}
		return f
	}

	for _, tc := range []struct {
		name string
		ht   []byte
		gate string // "": ungated, every gate off must accept. otherwise the gate that owns it.
		why  string
	}{
		// Wasm 2.0's two. `ref.null func` is in the corpus — elem.wast encodes it inside
		// funcref element segments in three places — so gating it is not a subtle defect
		// but a board full of red, which is the direction that gets noticed. That is not
		// a transcription and deliberately so: the falsification table above *measured*
		// the consequence (a misplaced gate turns the spec board red), which is a stronger
		// statement than a copied byte string and has nothing in it that can drift.
		{"ref.null func", []byte{0x70}, "", "-0x10 is Wasm 2.0's"},
		{"ref.null extern", []byte{0x6F}, "", "-0x11 likewise"},

		// Function-references, folded into the GC gate by decision 0008 — and the branch
		// whose check has to sit after the discriminator.
		{
			"ref.null 0",
			[]byte{0x00},
			"gc",
			"a type index is not Wasm 2.0; the check follows the negativity test, because a " +
				"check ahead of it would decline ref.null extern as a GC construct",
		},
		{"ref.null 1", []byte{0x01}, "gc", "same branch, a different index"},

		// GC's abstract forms, one from each end of the switch's two surviving ranges.
		{"ref.null any", []byte{0x6E}, "gc", "-0x12"},
		{"ref.null none", []byte{0x71}, "gc", "-0x0f, the other range"},

		// The exception proposal's two — #395. Both are here rather than one, because the
		// bytes sit at opposite ends of what used to be GC's two ranges (`-0x17` at the far
		// end of one, `-0x0c` at the near end of the other), so a repair that moved only the
		// range boundary it noticed would leave the other byte behind and one row could not
		// tell. Each is now the first row in this file that fails if the arm goes back.
		{"ref.null exn", []byte{0x69}, "exception handling", "-0x17, `exnref`'s heap type"},
		{"ref.null noexn", []byte{0x74}, "exception handling", "-0x0c, `nullexnref`'s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := refNullGlobal(tc.ht...)

			// Every row decodes with every gate on. That is what makes a decline below a
			// *gate* rather than a permanent rejection wearing a feature name.
			if _, err := (&Decoder{Features: on}).DecodeModule(mod); err != nil {
				t.Fatalf("all gates on: got %v, want accept\n\t%s", err, tc.why)
			}

			if tc.gate == "" {
				if _, err := (&Decoder{}).DecodeModule(mod); err != nil {
					t.Errorf("every gate off: got %v, want accept — this form is Wasm 2.0's and "+
						"gating it rejects modules the corpus contains\n\t%s", err, tc.why)
				}
				return
			}

			_, err := (&Decoder{Features: off(tc.gate)}).DecodeModule(mod)
			t.Logf("%s off: % x -> %v", tc.gate, tc.ht, err)
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("%s off: got %v, want ErrFeatureDisabled — with no check here the form "+
					"decodes clean, which is accept-and-ignore, and neither lane can see it: "+
					"the default lane is assert_malformed-only and the all-on lane's Gated==0 "+
					"is trivially satisfied by a gate that never fires (#48)\n\t%s",
					tc.gate, err, tc.why)
			}
			if !strings.Contains(err.Error(), tc.gate) {
				t.Errorf("%s off: %q does not name the feature — and naming the *wrong* one is "+
					"#395, which this column exists to pin", tc.gate, err)
			}

			// The other direction, and the one a boolean column could not express: with only
			// the row's own gate on, the form decodes. Necessity above, sufficiency here —
			// together they say the byte belongs to this gate and to no other. `ref.null 0`
			// and `ref.null 1` are exempt because a type index needs a type *section*, which
			// `refNullGlobal` does not build.
			if tc.ht[0] == 0x00 || tc.ht[0] == 0x01 {
				return
			}
			var only Features
			switch tc.gate {
			case "gc":
				only.GC = true
			case "exception handling":
				only.ExceptionHandling = true
			}
			if _, err := (&Decoder{Features: only}).DecodeModule(mod); err != nil {
				t.Errorf("only %s on: got %v, want accept — a gate that cannot admit its own "+
					"proposal's grammar unaided is #395's defect exactly, and every witness "+
					"elsewhere in the tree then has to open a second gate for no reason of its "+
					"own\n\t%s", tc.gate, err, tc.why)
			}
		})
	}
}

// TestDecodeRefTypeRetainsGCShapedValues is 0018's implementation control: the whole point of
// widening `ValType` was to let `decodeRefType`/`decodeHeapType` stop writing `NoValType` for
// forms they already validate, and this asserts they actually do it — a table's element type,
// a global's type, and (through decodeBlockTypeValue) a blocktype result, each carrying a GC
// abstract heaptype, a Wasm 2.0 form, or the indexed form.
//
// Every row is reachable only in the all-gates-on lane, per decision 0008's gate boundary —
// none of this is new *acceptance*, only new *representation* of what the GC gate already let
// through as NoValType. So this is a representation control, not an acceptance one, matching
// 0018's own consequences section ("this is a representation decision, not a gate decision").
func TestDecodeRefTypeRetainsGCShapedValues(t *testing.T) {
	on := &Decoder{Features: Features{GC: true}}

	t.Run("table element type: GC abstract heaptype (anyref)", func(t *testing.T) {
		// id 4 (table), size 4, count 1, elemtype 0x6E (anyref), limits 0x00 0x01.
		img := []byte{
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x04, 0x04, 0x01, 0x6E, 0x00, 0x01,
		}
		m, err := on.DecodeModule(img)
		if err != nil {
			t.Fatalf("GC on: got %v, want accept", err)
		}
		if len(m.Tables) != 1 {
			t.Fatalf("got %d tables, want 1", len(m.Tables))
		}
		got := m.Tables[0].ElemType
		want := refKind(0x6E, true) // reftype's bare abstract forms are the (Null, ...) abbreviation
		if got != want {
			t.Errorf("table element type = %+v, want %+v (anyref, resolved rather than "+
				"NoValType — the whole point of 0018)", got, want)
		}
		if got == NoValType {
			t.Error("table element type is NoValType: decodeRefType's GC-abstract branch " +
				"still writes the pre-0018 sentinel instead of the resolved kind")
		}
		if !got.IsRef() {
			t.Error("anyref.IsRef() = false: a GC abstract heaptype must live in the " +
				"reference array, not the numeric one (0002)")
		}
	})

	t.Run("table element type: indexed reference form (ref null $t)", func(t *testing.T) {
		// A minimal type section (one func type, index 0) so the index is legal, then a
		// table whose element type is `(ref null 0)` — reftype's -0x1d prefix, heaptype 0.
		// id 1 (type): 1 rectype, functype tag 0x60, 0 params, 0 results.
		typeSec := []byte{0x01, 0x04, 0x01, 0x60, 0x00, 0x00}
		// id 4 (table): count 1, elemtype (0x63 0x00 = (ref null 0)), limits 0x00 0x01.
		tableSec := []byte{0x04, 0x05, 0x01, 0x63, 0x00, 0x00, 0x01}
		img := append([]byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}, typeSec...)
		img = append(img, tableSec...)

		m, err := on.DecodeModule(img)
		if err != nil {
			t.Fatalf("GC on: got %v, want accept", err)
		}
		if len(m.Tables) != 1 {
			t.Fatalf("got %d tables, want 1", len(m.Tables))
		}
		got := m.Tables[0].ElemType
		if !got.IsIndexed() {
			t.Errorf("table element type = %+v, want the indexed form (kind == kindIndexed)",
				got)
		}
		if got.Index() != 0 {
			t.Errorf("table element type names index %d, want 0", got.Index())
		}
		if !got.Null() {
			t.Error("table element type lost its nullability: -0x1d is (ref null $t), and " +
				"decodeRefType's parameterized branch is what supplies the null bit " +
				"decodeHeapType's own grammar cannot carry")
		}
		if !got.IsRef() {
			t.Error("the indexed form must be a reference (IsRef() == true)")
		}
	})

	t.Run("table element type: indexed reference form (ref $t), non-null", func(t *testing.T) {
		typeSec := []byte{0x01, 0x04, 0x01, 0x60, 0x00, 0x00}
		// -0x1c (0x64) is (ref $t), non-null.
		tableSec := []byte{0x04, 0x05, 0x01, 0x64, 0x00, 0x00, 0x01}
		img := append([]byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}, typeSec...)
		img = append(img, tableSec...)

		m, err := on.DecodeModule(img)
		if err != nil {
			t.Fatalf("GC on: got %v, want accept", err)
		}
		got := m.Tables[0].ElemType
		if got.Null() {
			t.Error("(ref 0) decoded as nullable; -0x1c is the non-null prefix")
		}
		if !got.IsIndexed() || got.Index() != 0 {
			t.Errorf("table element type = %+v, want the indexed form naming index 0", got)
		}
	})

	t.Run("global type: Wasm 2.0 form unchanged (funcref)", func(t *testing.T) {
		// id 6 (global): count 1, valtype 0x70 (funcref), mutable 0x00, init i32.const 0
		// end. Ungated, so this also confirms the Wasm 2.0 branch's backward-compatibility
		// requirement holds through a real decode rather than only through the unit-level
		// refKind call.
		img := []byte{
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x06, 0x06, 0x01, 0x70, 0x00, 0x41, 0x00, 0x0B,
		}
		m, err := DecodeModule(img) // every gate off — funcref is Wasm 2.0
		if err != nil {
			t.Fatalf("got %v, want accept", err)
		}
		if got := m.Globals[0].Type; got != FuncRef {
			t.Errorf("global type = %+v, want exactly FuncRef (%+v) — the two Wasm 2.0 "+
				"forms must keep their pre-0018 byte behavior unchanged", got, FuncRef)
		}
	})

	t.Run("global type: GC abstract heaptype (eqref), non-null via reftype", func(t *testing.T) {
		img := []byte{
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x06, 0x06, 0x01, 0x6D, 0x00, 0x41, 0x00, 0x0B,
		}
		m, err := on.DecodeModule(img)
		if err != nil {
			t.Fatalf("GC on: got %v, want accept", err)
		}
		got := m.Globals[0].Type
		want := refKind(0x6D, true)
		if got != want {
			t.Errorf("global type = %+v, want %+v (eqref)", got, want)
		}
	})
}

// TestBlockTypeRetainsAnIndexedResult is 0018's implementation control for the packing
// redesign in module.go: a bare-valtype blocktype whose single result is the GC-gated indexed
// reference form must round-trip through Imm0/Imm1 and back out through BlockType with its
// index intact — the case the pre-0018 packing had no room for at all.
func TestBlockTypeRetainsAnIndexedResult(t *testing.T) {
	// type 0: (func). type 1: (func) too, so a type-index result blocktype naming index 1
	// stays distinguishable from a bare "0". Function 0 has type 0 and a body of one
	// instruction: `block (result (ref null 1)) end end`.
	//
	// blocktype tail: -0x1d (0x63) then s33(1) — `(ref null 1)`, encoded as a single byte
	// since 1 < 64.
	body := []byte{
		0x00,             // no locals
		0x02, 0x63, 0x01, // block (result (ref null 1))
		0x0B, // end (block)
		0x0B, // end (func body)
	}
	img := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	img = append(img, 0x01, 0x07, 0x02, 0x60, 0x00, 0x00, 0x60, 0x00, 0x00) // type: two (func)
	img = append(img, 0x03, 0x02, 0x01, 0x00)                               // function: 1, type 0
	codeSec := append([]byte{0x01}, byte(len(body)))
	codeSec = append(codeSec, body...)
	img = append(img, 0x0A, byte(len(codeSec)))
	img = append(img, codeSec...)

	on := &Decoder{Features: Features{GC: true}}
	m, err := on.DecodeModule(img)
	if err != nil {
		t.Fatalf("GC on: got %v, want accept", err)
	}
	if len(m.Funcs) != 1 || len(m.Funcs[0].Body) == 0 {
		t.Fatalf("expected one function with a body, got %d funcs", len(m.Funcs))
	}
	blockInstr := m.Funcs[0].Body[0]
	if blockInstr.Op != 0x02 {
		t.Fatalf("first instruction is opcode %#02x, want 0x02 (block)", blockInstr.Op)
	}
	idx, vt, empty := BlockType(blockInstr.Imm0, blockInstr.Imm1)
	if empty {
		t.Fatal("BlockType reports empty, want the single-result valtype form")
	}
	if idx != 0 {
		t.Errorf("BlockType's typeIdx return is %d for the valtype form, want 0 (unused)", idx)
	}
	if !vt.IsIndexed() {
		t.Fatalf("blocktype result = %+v, want the indexed form", vt)
	}
	if vt.Index() != 1 {
		t.Errorf("blocktype result names index %d, want 1 — this is Imm1 carrying the "+
			"value BlockType's pre-0018 one-word packing had nowhere to put", vt.Index())
	}
	if !vt.Null() {
		t.Error("blocktype result lost its nullability: the source is (ref null 1)")
	}
}

// TestSegmentKindStringsAreAtTheirSites pins #88's two renamed sentinels where they are
// raised, which the sentinel inventory cannot do.
//
// TestEverySentinelIsTheReferencesOrIsDeclared asks whether a string exists in decode.ml.
// Necessary, not sufficient: an engine could adopt `malformed data segment kind` and raise
// it from the *element* segment and satisfy the inventory while lying about the layer. Two
// productions here have confusably similar text — `malformed elements segment kind`, the
// segment's leading u32 (decode.ml:1201), against `malformed element kind`, the one-byte
// `elem_kind` nested inside it (:1157) — and the old names (`ErrMalformedElemFlags`,
// `ErrMalformedDataFlags`) named a field the format does not have, so the rename is exactly
// the kind of change that can land at the wrong depth.
//
// The grammar was already right; only the strings were wrong. Recorded because it bounds the
// finding: this member of #88 is a message-direction defect and nothing else.
func TestSegmentKindStringsAreAtTheirSites(t *testing.T) {
	d := &Decoder{}
	for _, tc := range []struct {
		name string
		read func(*reader) error
		in   []byte
		want error
	}{
		{
			"the element segment's leading u32", d.decodeElemSegment,
			[]byte{0x08},
			ErrMalformedElemSegKind, // 0..7 are the legal flag combinations
		},
		{
			"the elem_kind byte inside a flags-1 segment", d.decodeElemSegment,
			[]byte{0x01, 0x01},
			ErrMalformedElemKind, // 0x00 is the only defined kind
		},
		{
			// Adapted, not weakened, when 0015 made decodeDataSegmentMode return the
			// segment it staged: the mode read is still the thing under test and its
			// error is still the assertion, so only the discarded value is new.
			"the data segment's leading u32",
			func(r *reader) error { _, err := d.decodeDataSegmentMode(r); return err },
			[]byte{0x03},
			ErrMalformedDataSegKind, // 0..2
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &reader{b: tc.in, eof: ErrPayloadEnd}
			err := tc.read(r)
			t.Logf("% x -> %v", tc.in, err)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v — the two element strings are separate productions "+
					"with near-identical text (decode.ml:1201 against :1157), so an inventory "+
					"check that only asks whether a string exists upstream stays green with "+
					"them swapped", err, tc.want)
			}
		})
	}
}

// TestHeapKindsAreWhatTheReaderProduces closes the loop the `Heap*` constants open: every
// abstract heaptype form, decoded through the real reader, resolves to the constant the
// interpreter's subtype lattice keys on — and nothing else does.
//
// # Why the constants needed a control at all
//
// Three places were about to enumerate the same twelve facts: `decodeHeapType`'s two `case`
// lists, `abstractHeapNames`' keys, and `castop.go`'s `matchHeapType`. The constants exist so
// the third reads the second reads the first; this test is what makes "reads" true rather
// than "was copied from".
//
// # What it can and cannot see, stated up front
//
// The constants are derived by `-0x0C & 0x7F` and the reader resolves by `byte(form & 0x7F)`,
// so **the two sides share that arithmetic by construction** and the byte column cannot catch
// a wrong *packing*. What it does catch is every way the wire form and the constant can come
// apart, which is the failure that actually threatens: the row is keyed by the **wire form**
// (decode.ml:179-200) and valued by the **constant symbol**, so a constant derived from the
// wrong form — `HeapAny = byte(-0x11 & 0x7F)`, extern's — makes the reader produce 0x6E where
// the row wants 0x6F and the row fires. The name column is independent of the arithmetic
// altogether: it is `string_of_heaptype`'s spelling (types.ml:336-350), which no amount of
// masking derives, so a mis-keyed `abstractHeapNames` entry has nowhere to hide either.
//
// The one fact outside this test's reach is whether the spec's form for `any` *is* -0x12. That
// is decode.ml's to say, cited per row, and no local control can substitute for it — said here
// rather than left implicit, because an instrument that does not name its blind spot invites
// the clean-result reading of its own green.
//
// # Scoped to the space, not to the twelve
//
// The loop runs **every negative `sleb(7)` value**, -64..-1 — the whole domain the abstract
// branch can see, since non-negative values are type indices taken by the earlier `s33` branch.
// So the twelve are pinned *and* the sixty-two non-forms are pinned as rejections: an arm
// accepting a byte the spec does not define is an over-accept, and this is the shape of defect
// #88's own fix turned out to have (`heaptype` has no `-0x1c`/`-0x1d` arm, and reading
// `reftype` there accepted `ref.null (ref null extern)`). A twelve-row table cannot see it.
//
// Both parameterized prefixes are run, `(ref null ht)` and `(ref ht)`, because the kind byte
// and the nullability bit come from different productions — the heaptype carries no
// nullability of its own (`decodeHeapType`'s doc) and the prefix supplies it. One direction
// alone would score a hardcoded `null` as correct on half the corpus.
func TestHeapKindsAreWhatTheReaderProduces(t *testing.T) {
	on := featuresAllOn(t)

	// Keyed by the wire form, valued by the constant and the reference's spelling. Twelve
	// rows, decode.ml:179-200 for the forms and types.ml:336-350 for the names.
	want := map[int]struct {
		kind byte
		name string
	}{
		-0x0C: {HeapNoExn, "noexn"},
		-0x0D: {HeapNoFunc, "nofunc"},
		-0x0E: {HeapNoExtern, "noextern"},
		-0x0F: {HeapNone, "none"},
		-0x10: {HeapFunc, "func"},
		-0x11: {HeapExtern, "extern"},
		-0x12: {HeapAny, "any"},
		-0x13: {HeapEq, "eq"},
		-0x14: {HeapI31, "i31"},
		-0x15: {HeapStruct, "struct"},
		-0x16: {HeapArray, "array"},
		-0x17: {HeapExn, "exn"},
	}
	if len(want) != 12 {
		t.Fatalf("table has %d rows, want 12 — the count is the spec's (decode.ml lists twelve "+
			"abstract forms, the miscount `decodeHeapType`'s prose carried for three PRs), and a "+
			"table that lost a row would still agree with a reader that lost the same arm", len(want))
	}

	for _, prefix := range []struct {
		name string
		b    byte
		null bool
	}{
		{"(ref null ht)", 0x63, true}, // -0x1d
		{"(ref ht)", 0x64, false},     // -0x1c
	} {
		accepted := 0
		for form := -64; form <= -1; form++ {
			b := byte(form & 0x7F) // single-byte sleb(7): -64..-1 all fit
			mod := funcTypeParam(prefix.b, b)
			m, err := (&Decoder{Features: on}).DecodeModule(mod)

			exp, isHeapType := want[form]
			if !isHeapType {
				if err == nil {
					t.Errorf("%s: form %#x (byte %#02x) was accepted; no heaptype branch defines "+
						"it, so accepting it is an over-accept of exactly #88's shape — a reader "+
						"reaching one production too wide", prefix.name, form, b)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s: form %#x (%s): %v — the spec defines this heaptype, so rejecting it "+
					"is the accept-direction defect no assert_malformed vector can see (§9 G-3)",
					prefix.name, form, exp.name, err)
				continue
			}
			accepted++

			vt := m.Types[0].Func.Params[0]
			if vt.kind != exp.kind {
				t.Errorf("%s: form %#x (%s) resolved to kind %#02x, want %#02x — the reader and "+
					"the Heap* constant disagree, which means the interpreter's lattice "+
					"(match.ml:76-105, castop.go) is keyed on a byte this form never produces",
					prefix.name, form, exp.name, vt.kind, exp.kind)
			}
			if vt.null != prefix.null {
				t.Errorf("%s: form %#x (%s) resolved with null=%v, want %v — nullability comes from "+
					"the prefix and not from the heaptype, so a hardcoded bit is right on one of "+
					"these two loops and wrong on the other",
					prefix.name, form, exp.name, vt.null, prefix.null)
			}
			name, ok := HeapTypeName(vt.kind)
			if !ok || name != exp.name {
				t.Errorf("%s: form %#x resolved to kind %#02x, which HeapTypeName spells %q/%v, "+
					"want %q — this column is independent of the `form & 0x7F` arithmetic the "+
					"reader and the constants share, so it is the half that can catch a mis-keyed "+
					"name map", prefix.name, form, vt.kind, name, ok, exp.name)
			}
		}
		if accepted != 12 {
			t.Errorf("%s: %d of 64 negative forms accepted, want 12 — a count of zero is the "+
				"vacuous case (a wrapper whose own bytes stopped decoding agrees with any table)",
				prefix.name, accepted)
		}
	}
}

// TestKindDeclinesTheZeroValType pins both arms of grave #300: `Kind()`'s doc comment promised
// `ok == false` for NoValType and its code returned `(0x00, true)`.
//
// Two assertions rather than one, because the repair could be wrong in either direction and the
// two failures mean different things. The zero value must decline — otherwise an external
// consumer cannot tell "not a kind byte" from "a kind byte I have no mapping for", which is the
// distinction the public boundary's conversion is built on (0029). And every type that *does*
// have a byte must still report it, because a fix that declined too much would break the seven
// encoder sites silently: they discard `ok`, so an over-eager decline reaches the wire as byte 0
// rather than as a test failure.
//
// The second half is scoped to the space rather than to the named vars: the five numeric forms
// come from the named `var`s (the only place their bytes are written), and the reference forms
// are enumerated by ranging over all 256 bytes and keeping the ones `AbstractRefType` accepts —
// the same derivation the boundary's own guard uses, so a thirteenth heaptype is covered here the
// moment it is added to `abstractHeapNames`.
func TestKindDeclinesTheZeroValType(t *testing.T) {
	if b, ok := NoValType.Kind(); ok {
		t.Errorf("NoValType.Kind() = (%#02x, true), want ok=false. 0x00 is not the encoding of any "+
			"value type — NoValType's own comment says so — and an accessor reporting it as a kind "+
			"byte tells a caller outside this package that a field nothing wrote holds a real type "+
			"(grave #300)", b)
	}
	if b, ok := RefType(0, true).Kind(); ok {
		t.Errorf("RefType(0, true).Kind() = (%#02x, true), want ok=false: the indexed form has no "+
			"single wire byte", b)
	}

	var withByte []ValType
	withByte = append(withByte, I32, I64, F32, F64, V128)
	for b := range 256 {
		for _, null := range []bool{false, true} {
			if vt, ok := AbstractRefType(byte(b), null); ok {
				withByte = append(withByte, vt)
			}
		}
	}
	// 5 numeric/vector forms plus the twelve heaptypes in both nullabilities. Floored on the
	// nose rather than as a minimum: a count that drifted would mean `AbstractRefType`'s
	// predicate moved, and this test's second half would then be comparing over a domain
	// nobody chose.
	if want := 5 + 12*2; len(withByte) != want {
		t.Fatalf("enumerated %d ValTypes with a kind byte, want %d — the derivation is the "+
			"assertion's domain, so a wrong count invalidates every row below", len(withByte), want)
	}
	for _, vt := range withByte {
		if _, ok := vt.Kind(); !ok {
			t.Errorf("%s.Kind() reports no kind byte, and it has one. Seven call sites in "+
				"internal/text discard the second result, so an over-eager decline here reaches "+
				"the wire as byte 0 rather than as a failure", vt)
		}
	}
}

// TestAbstractRefTypeDerivesTheTwelve is the domain check on the constructor the public boundary's
// guard ranges over. It is the vacuity guard that guard cannot carry itself: a constructor that
// accepted nothing would make the boundary's exhaustiveness test pass over an empty reference
// space, which is *a comparison against an empty set succeeds* aimed at the newest instrument.
//
// Checked against `HeapTypeName` in **both** directions. They read one table by construction, so
// this cannot catch a mis-keyed entry — what it catches is the two accessors coming apart, which is
// what a future arm that special-cases a form would do.
func TestAbstractRefTypeDerivesTheTwelve(t *testing.T) {
	accepted, named := map[byte]bool{}, map[byte]bool{}
	for b := range 256 {
		if _, ok := AbstractRefType(byte(b), false); ok {
			accepted[byte(b)] = true
		}
		if _, ok := HeapTypeName(byte(b)); ok {
			named[byte(b)] = true
		}
	}
	if len(accepted) != 12 {
		t.Errorf("AbstractRefType accepts %d of 256 bytes, want the twelve abstract heaptypes",
			len(accepted))
	}
	for b := range accepted {
		if !named[b] {
			t.Errorf("AbstractRefType accepts %#02x and HeapTypeName cannot spell it", b)
		}
	}
	for b := range named {
		if !accepted[b] {
			t.Errorf("HeapTypeName spells %#02x and AbstractRefType declines it — the boundary's "+
				"exhaustiveness guard ranges over what this accepts, so a form missing here is a "+
				"form nothing checks converts", b)
		}
	}
	// Nullability comes from the argument and not from the heaptype, which is the one property a
	// constructor keyed on a name map could get wrong for every form at once.
	for b := range accepted {
		for _, null := range []bool{false, true} {
			vt, _ := AbstractRefType(b, null)
			if vt.Null() != null {
				name, _ := HeapTypeName(b)
				t.Errorf("AbstractRefType(%s, %v).Null() = %v", name, null, vt.Null())
			}
		}
	}
}

// TestTheExceptionGateAdmitsItsOwnValueType is #395's witness, and it exists because **no board lane
// can state this property in either direction.** The default lane runs with both gates off and the
// all-on lane with both on, so no vector in the corpus is ever decoded in a configuration that holds
// `ExceptionHandling` and `GC` apart — the defect was invisible to 65,000 vectors by construction,
// and it will stay invisible now that it is fixed. A unit assertion is not a second-best witness
// here; it is the only kind available.
//
// The two halves are the two directions of one claim, and they fail for unrelated reasons:
//
//   - **Sufficiency.** `ExceptionHandling` alone admits `exnref` (0x69) and `nullexnref` (0x74).
//     This is the half that was broken: the module in #395's body — `(func (param exnref) ...)` —
//     was refused `gc: feature gate disabled`, so the gate did not admit the value type its own
//     proposal defines (`Exceptions.md:337-349`).
//   - **Non-contamination.** `GC` alone still admits its eight, and refuses these two *naming the
//     exception gate*. A repair that moved the bytes by widening GC's arm to accept them under
//     either gate would pass the sufficiency half and fail here.
//
// The eight are **derived** from `abstractHeapNames` minus the four whose gate is not GC, not
// enumerated: an enumeration written today inherits today's attribution, which is the mistake this
// test is about. A form added to the map with no gate decision lands in the derived set and fails
// here rather than being quietly absorbed — and the count is asserted so a *shrinking* map cannot
// make the sweep vacuous.
func TestTheExceptionGateAdmitsItsOwnValueType(t *testing.T) {
	ehOnly := Features{ExceptionHandling: true}
	gcOnly := Features{GC: true}

	// Both spellings of the exception proposal's value type, as one-byte reftypes at a param
	// position — #395's own repro, which is a param and not a table element type.
	for _, tc := range []struct {
		name string
		vt   byte
	}{
		{"exnref", HeapExn},       // -0x17
		{"nullexnref", HeapNoExn}, // -0x0c
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := funcTypeParam(tc.vt)
			if _, err := (&Decoder{Features: ehOnly}).DecodeModule(mod); err != nil {
				t.Errorf("only ExceptionHandling on: got %v, want accept — this is the type the "+
					"proposal defines, so a gate that declines it is not independently usable and "+
					"every witness elsewhere has to open GC for an unrelated reason (#395)", err)
			}
			_, err := (&Decoder{Features: gcOnly}).DecodeModule(mod)
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("only GC on: got %v, want ErrFeatureDisabled", err)
			}
			if !strings.Contains(err.Error(), "exception handling") {
				t.Errorf("only GC on: %q names the wrong gate — the whole content of #395 was a "+
					"decline that fired for a true reason under a false name", err)
			}
		})
	}

	// GC's eight, derived. func/extern are Wasm 2.0's and ungated; exn/noexn are the pair above.
	eight := 0
	for kind, name := range abstractHeapNames {
		switch kind {
		case HeapFunc, HeapExtern, HeapExn, HeapNoExn:
			continue
		}
		eight++
		t.Run("gc keeps "+name, func(t *testing.T) {
			mod := funcTypeParam(kind)
			if _, err := (&Decoder{Features: gcOnly}).DecodeModule(mod); err != nil {
				t.Errorf("only GC on: %s got %v, want accept — moving two bytes out of GC's arm "+
					"must not take any of the other eight with them", name, err)
			}
			_, err := (&Decoder{Features: ehOnly}).DecodeModule(mod)
			if !errors.Is(err, ErrFeatureDisabled) || !strings.Contains(err.Error(), "gc") {
				t.Errorf("only ExceptionHandling on: %s got %v, want a gc-named feature decline — "+
					"the exception gate must not have acquired GC's forms in the move", name, err)
			}
		})
	}
	if eight != 8 {
		t.Errorf("derived %d GC abstract forms, want 8 — twelve in `abstractHeapNames` minus "+
			"func/extern (Wasm 2.0) minus exn/noexn (#395); a map that lost a row would make "+
			"every sweep above agree with less of the space", eight)
	}
}
