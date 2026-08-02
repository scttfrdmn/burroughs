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
		{"nullexnref", []byte{0x74}, "gc", nil},
		{"nullfuncref", []byte{0x73}, "gc", nil},
		{"nullexternref", []byte{0x72}, "gc", nil},
		{"nullref", []byte{0x71}, "gc", nil},
		{"anyref", []byte{0x6E}, "gc", nil},
		{"eqref", []byte{0x6D}, "gc", nil},
		{"i31ref", []byte{0x6C}, "gc", nil},
		{"structref", []byte{0x6B}, "gc", nil},
		{"arrayref", []byte{0x6A}, "gc", nil},
		{"exnref", []byte{0x69}, "gc", nil},

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
	withoutGC := on
	withoutGC.GC = false

	for _, tc := range []struct {
		name string
		ht   []byte
		gate bool // true: GC off must decline. false: GC off must accept.
		why  string
	}{
		// Wasm 2.0's two. `ref.null func` is in the corpus — elem.wast encodes it inside
		// funcref element segments in three places — so gating it is not a subtle defect
		// but a board full of red, which is the direction that gets noticed. That is not
		// a transcription and deliberately so: the falsification table above *measured*
		// the consequence (a misplaced gate turns the spec board red), which is a stronger
		// statement than a copied byte string and has nothing in it that can drift.
		{"ref.null func", []byte{0x70}, false, "-0x10 is Wasm 2.0's"},
		{"ref.null extern", []byte{0x6F}, false, "-0x11 likewise"},

		// Function-references, folded into the GC gate by decision 0008 — and the branch
		// whose check has to sit after the discriminator.
		{
			"ref.null 0",
			[]byte{0x00},
			true,
			"a type index is not Wasm 2.0; the check follows the negativity test, because a " +
				"check ahead of it would decline ref.null extern as a GC construct",
		},
		{"ref.null 1", []byte{0x01}, true, "same branch, a different index"},

		// GC's abstract forms, one from each end of the switch's two ranges.
		{"ref.null any", []byte{0x6E}, true, "-0x12"},
		{"ref.null none", []byte{0x71}, true, "-0x0f, the other range"},
		{"ref.null exn", []byte{0x69}, true, "-0x17"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := refNullGlobal(tc.ht...)

			// Every row decodes with GC on. That is what makes a decline below a *gate*
			// rather than a permanent rejection wearing a feature name.
			if _, err := (&Decoder{Features: on}).DecodeModule(mod); err != nil {
				t.Fatalf("GC on: got %v, want accept\n\t%s", err, tc.why)
			}

			_, err := (&Decoder{Features: withoutGC}).DecodeModule(mod)
			t.Logf("GC off: % x -> %v", tc.ht, err)
			if !tc.gate {
				if err != nil {
					t.Errorf("GC off: got %v, want accept — this form is Wasm 2.0's and gating "+
						"it rejects modules the corpus contains\n\t%s", err, tc.why)
				}
				return
			}
			if !errors.Is(err, ErrFeatureDisabled) {
				t.Fatalf("GC off: got %v, want ErrFeatureDisabled — with no check here the form "+
					"decodes clean, which is accept-and-ignore, and neither lane can see it: "+
					"the default lane is assert_malformed-only and the all-on lane's Gated==0 "+
					"is trivially satisfied by a gate that never fires (#48)\n\t%s", err, tc.why)
			}
			if !strings.Contains(err.Error(), "gc") {
				t.Errorf("GC off: %q does not name the feature", err)
			}
		})
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
			"the data segment's leading u32", d.decodeDataSegmentMode,
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
