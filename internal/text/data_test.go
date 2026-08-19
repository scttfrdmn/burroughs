package text

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// TestRetainedOffsetRestoresTheOuterSink is `retainedOffset`'s swap-and-restore, falsified at birth.
//
// **The prose at that site was wrong about why the restore matters, and probing is what said so.** It
// claimed the saved sink "is genuinely non-nil in one case", which reads like the interesting half of
// the control. Measured — a counter on both branches, over `(data (i32.const 0) …)`,
// `(data (offset …) …)` and `(data (memory 0) …)`, one of them behind a `(func …)` field — it is
// `nil=1 nonNil=0` on every shape, because no spelling nests a `(data …)` field inside a func body. So
// the outer value is *always* nil today and the swap's whole job is the **clear**, not the restore.
//
// That correction is why this asserts what it asserts. A control written to the old prose would have
// looked for a non-nil outer sink surviving the call, found no input producing one, and been
// stillborn — a no-op guard wearing a predicate's clothes, which is the shape this project has now
// paid for twice. What is checkable is the direction that exists.
//
// Both halves were watched die by deleting the `outerSink`/`defer` pair:
//
//   - **The sink is nil after the parse.** Without the restore it holds the offset's instructions —
//     `&{[{[65] [0] <nil>}]}`, an `i32.const 0` still installed after the field closed.
//   - **A later field's refusal names the field, not an instruction.** The leaked sink made
//     `p.retaining()` true at module-field level, so `(table 1 funcref (ref.null func))` following a
//     data segment reported *"cannot yet encode the ref.null instruction's immediates"* where it had
//     to report *"this (table …) field"*. An error from the wrong layer, this project's standing tell
//     for lost structure — and it was the half worth having, because the leak is otherwise invisible:
//     four of the six probed modules encode byte-identically with and without the restore.
//
// **That second half is gone as of #419, and its loss is recorded here rather than in the deletion's
// diff.** The probe worked by putting a *field-level* refusal after the data segment, and #419 closed
// the last one: `tableField`'s `constexpr1` arm now retains its initializer and withdraws the
// frontier, so no well-formed module field is refused as a field any more and there is nothing whose
// layer the leak can change. Re-measured before deleting, by neutering the `defer` on this branch:
// half one fails on four of its five rows and **nothing else in the package fails at all** — not one
// image differs, not one verdict flips. So the deletion is what the measurement says, and this
// paragraph is what a reader gets instead of a row that would pass either way.
//
// What would make the leak visible again, stated because it is a live risk rather than a retired one
// (the re-pointing rule, #33): a construct parsed at **module-field scope that consults
// `retaining()` without establishing a sink**. There is none today because every instruction-parsing
// site outside a function body goes through `intoSink`, which gates on the *mode* (`p.retain`, grave
// #144) and therefore installs its own sink whether or not one leaked. `expr1`'s leader splice
// (parser.go's `retaining()`-and-not-`p.retain` site) is exactly such a consulting caller, and its
// own comment already forecasts the module-field-scope case — under a leak it would splice a leader
// into *the data segment's offset expression* rather than nil-dereference, which is a corrupted
// image and not a crash. Half one is what stands between the two.
func TestRetainedOffsetRestoresTheOuterSink(t *testing.T) {
	// The sink is clear when the field is done. Every arm that installs one, since the spellings
	// reach `retainedOffset` by three different paths in `dataField` — **plus one row that does not
	// reach it at all**, which the neutering above measured and this now says: `(module (memory (data
	// "a")))` synthesizes its offset through `sugarZeroOffset`, which builds a sink and never installs
	// it, so that row is the only one of the five the restore cannot fail. Kept, because it is the row
	// that says the sugar path leaves no sink behind *either* — a different claim from the other four,
	// and the one that would break if the sugar ever started parsing its offset instead of building it.
	for _, src := range []string{
		`(module (memory 1) (data (i32.const 0) "a"))`,
		`(module (memory 1) (data (offset (i32.const 0)) "a"))`,
		`(module (memory 1) (data (memory 0) (offset (i32.const 0)) "a"))`,
		`(module (memory 1) (data (offset) ""))`,
		`(module (memory (data "a")))`,
	} {
		t.Run(src, func(t *testing.T) {
			p, err := parseModule([]byte(src), build)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if p.sink != nil {
				t.Errorf("p.sink is %v after the parse, want nil: retainedOffset installed a sink for the "+
					"offset expression and did not restore it, so every module field after this one "+
					"parses as though it were inside a function body", p.sink)
			}
		})
	}
}

// TestSugarZeroOffsetEncodesTheAddressTypesConst pins the offset the data-bearing memory sugar
// synthesizes, by decoding an image rather than by comparing the sink to the table that filled it.
//
// `sugarZeroOffset` builds the one instruction in this package with **no source token**, so nothing
// in the parse can be checked against a spelling: the reference builds it from the address type
// (`at_const $1 (0L)`, parser.mly:1130; `I32AT -> i32_const`, `I64AT -> i64_const`,
// mnemonics.ml:18-20). A wrong mnemonic writes an i32 offset for an i64 memory — a validation error
// the *encoder* produced, invisible to every vector, because the suite's sugar modules are ones it
// expects to work.
//
// Read out of the encoded image through the decoder's own segmentation, not compared to `opBytes`'
// return value: comparing the sink to the table that built it is an echo (grave #106), agreeing
// perfectly when both are wrong the same way.
func TestSugarZeroOffsetEncodesTheAddressTypesConst(t *testing.T) {
	for _, tc := range []struct {
		src    string
		wantOp byte
		why    string
	}{
		{`(module (memory (data "x")))`, 0x41, "i32.const, the default address type"},
		{`(module (memory i64 (data "x")))`, 0x42, "i64.const, from the i64 addrtype"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			// The sink half, first, so a failure below can be attributed. One instruction: the
			// reference's offset is a one-element const expression and a sink of any other length is
			// not it.
			s := sugarZeroOffset(tc.wantOp == 0x42)
			if len(s.instrs) != 1 {
				t.Fatalf("sugarZeroOffset built %d instructions, want exactly 1", len(s.instrs))
			}

			img, err := EncodeModule([]byte(tc.src))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			payload, ok := sectionPayload(decodeForTest(t, img), binary.SectionData)
			if !ok {
				t.Fatalf("no data section in %#x: the sugar arm defines a segment, so its absence is "+
					"the segment being dropped rather than an offset being mis-encoded", img)
			}
			// `01` one segment, `00` the zero-index active arm (the sugar's memory is index 0 in
			// these modules), then the const expression.
			want := []byte{0x01, 0x00, tc.wantOp, 0x00, opEnd, 0x01, 'x'}
			if string(payload) != string(want) {
				t.Errorf("the sugar's data section payload is %#x, want %#x (%s).\n\tA "+
					"`(memory i64 (data …))` whose offset is an i32.const encodes an i32 offset for an "+
					"i64 memory — the address type is the only thing distinguishing the two arms, and "+
					"getting it wrong produces a module that decodes clean and fails validation.",
					payload, want, tc.why)
			}
		})
	}

	// The flags byte is *not* asserted here, and the omission is deliberate rather than a gap:
	// `binary.Memory` retains no address type, so the i64-ness of the memory itself is checked by
	// TestEncodeWritesTheAddressTypeFlagBit against section 5's bytes. This test owns the offset
	// *instruction*, which is a different fact in a different section — and the two together are what
	// say the sugar's two halves agree about the address type.
}

// TestConstExprBytesMatchesABodysEncoding holds the encoder's two writers of instruction bytes to one
// output.
//
// Two now exist. `constExprBytes` writes a data segment's offset; `resolveFuncs` writes a function's
// body. They share `instrSink` and the `patch` protocol and *nothing else* — the loop is written out
// twice — so two places know how a sink becomes bytes, which is the drift risk 0006 names. This is
// its tripwire, filed in the same PR that created the second writer rather than as an intention.
//
// The comparison is between the two paths on the same instruction, taken from two encoded images
// rather than from a literal expectation. A literal would be a third place knowing the encoding, and
// it would agree with both writers being wrong the same way — which is the failure mode that matters
// here, since the risk is *divergence*, not a wrong constant.
//
// **The offset carries its terminator and a body does not**, a real asymmetry rather than a defect:
// `const c = list instr c.it; end_ ()` (encode.ml:912-913) puts the `0x0b` inside the const
// expression, while `writeCodeSection` appends it after the locals and body. So the body's slice has
// its `drop` and terminator trimmed and one `0x0b` re-appended, and that normalization is *stated*,
// because a comparison whose two sides were normalized differently is not a comparison.
//
// `-1` is the row that earns the test. A signed immediate written `s32` by one writer and `u32` by
// the other agrees on every non-negative value — 0, 1, 127, 128, 1000000 all pass under that defect —
// and differs only here: `41 7f` versus `41 ff ff ff ff 0f`.
func TestConstExprBytesMatchesABodysEncoding(t *testing.T) {
	for _, imm := range []string{"0", "1", "127", "128", "-1", "1000000"} {
		t.Run(imm, func(t *testing.T) {
			offsetImg, err := EncodeModule([]byte(`(module (memory 1) (data (i32.const ` + imm + `) "x"))`))
			if err != nil {
				t.Fatalf("encode the data module: %v", err)
			}
			bodyImg, err := EncodeModule([]byte(`(module (func (i32.const ` + imm + `) (drop)))`))
			if err != nil {
				t.Fatalf("encode the func module: %v", err)
			}

			offsetExpr := offsetExprFromDataSection(t, offsetImg)
			bodyExpr := constExprFromBody(t, bodyImg)
			if string(offsetExpr) != string(bodyExpr) {
				t.Errorf("i32.const %s encodes to %#x as a data offset and %#x in a function body.\n\t"+
					"constExprBytes and resolveFuncs are two loops writing one encoding, sharing only "+
					"instrSink and the patch protocol; this is the drift they can drift into.",
					imm, offsetExpr, bodyExpr)
			}
		})
	}
}

// offsetExprFromDataSection returns the const expression bytes of a single-segment data section,
// terminator included.
//
// Reads the section the decoder segmented rather than scanning the image, so the extent is the
// decoder's opinion and not this test's arithmetic. Every shape assumption is a `Fatalf` naming
// itself as a reader defect, because a reader that silently returns a short slice makes the
// comparison it feeds agree with anything.
func offsetExprFromDataSection(t *testing.T, img []byte) []byte {
	t.Helper()
	payload, ok := sectionPayload(decodeForTest(t, img), binary.SectionData)
	if !ok {
		t.Fatalf("no data section in %#x", img)
	}
	if len(payload) < 3 || payload[0] != 0x01 || payload[1] != 0x00 {
		t.Fatalf("data payload %#x is not one segment in the 0x00 arm; this helper reads that shape "+
			"and cannot read any other, so a mismatch here is the helper's fault and not a finding",
			payload)
	}
	end := 2
	for end < len(payload) && payload[end] != opEnd {
		end++
	}
	if end == len(payload) {
		t.Fatalf("no %#02x terminator in data payload %#x: constExprBytes must write one, and without "+
			"it the segment's byte vector is read as more instructions", opEnd, payload)
	}
	return payload[2 : end+1]
}

// constExprFromBody returns a one-function code section's body as a const expression: the
// instructions with the trailing `drop` removed and a single terminator, matching what the offset
// side carries.
//
// The source is always `(func (i32.const N) (drop))`, so the tail is asserted before it is trimmed —
// a trim that silently matched nothing would shorten one side of the comparison and quietly make the
// two agree.
func constExprFromBody(t *testing.T, img []byte) []byte {
	t.Helper()
	payload, ok := sectionPayload(decodeForTest(t, img), binary.SectionCode)
	if !ok {
		t.Fatalf("no code section in %#x", img)
	}
	// count(1), the entry's size, then locals(0) and the instructions.
	if len(payload) < 4 || payload[0] != 0x01 || payload[2] != 0x00 {
		t.Fatalf("code payload %#x is not one function with no local groups; this helper reads that "+
			"shape only", payload)
	}
	body := payload[3:]
	const opDrop = 0x1a
	if len(body) < 2 || body[len(body)-1] != opEnd || body[len(body)-2] != opDrop {
		t.Fatalf("code body %#x does not end with drop then %#02x: the source is `(i32.const N) "+
			"(drop)`, so a different tail means this helper is wrong about the body rather than the "+
			"two writers disagreeing", body, opEnd)
	}
	out := append([]byte(nil), body[:len(body)-2]...)
	return append(out, opEnd) // the asymmetry, applied explicitly
}

// TestDataRefKindsMatchTheDecodersOpcodes pins `dataRefKinds` — the text side's section 12 trigger —
// against the authority both sides were read from, and against the decoder's mirror of it.
//
// Section 12's condition is a fact about **four instructions** (free.ml:165, 166, 175, 181), and this
// repo holds it in three places: `free.ml` upstream, `dataRefOps` in internal/binary keyed by opcode,
// and `dataRefKinds` here keyed by keyword kind. The decoder *requires* the section for those
// opcodes and this encoder *emits* it for them, so a disagreement makes the round trip fail on
// whichever opcode is the odd one out — or, in the accept direction, produces an image with no
// section 12 that another engine rejects.
//
// **The keys differ on purpose, so the join goes through a third party.** The text side cannot key
// on opcodes: it decides at a keyword token, before any immediate is read. So each kind is mapped to
// its mnemonic through the *keyword* table and then to an opcode through the generated opcode table,
// both machine-derived from the same revision. A hand-written correspondence would be a fourth place
// spelling out the fact being controlled.
//
// The reference's own arm count is asserted too, which is the direction scoped to the space rather
// than to the set: upstream adding a fifth `datas (idx …)` arm fails here instead of silently leaving
// both maps short, and a short map means a module needing section 12 does not get one.
func TestDataRefKindsMatchTheDecodersOpcodes(t *testing.T) {
	free := testenv.RequireSpecRef(t, testenv.RefFreeML)

	// The authority's count first, because everything below compares against a set whose size is the
	// claim. `datas (idx` is the reference's own way of contributing to the set — `datas s` builds a
	// singleton (:55) and `++` unions (:43) — so an arm mentioning it is an arm that references the
	// data index space.
	arms := strings.Count(free, "datas (idx ")
	if arms != len(dataRefKinds) {
		t.Errorf("free.ml has %d `datas (idx …)` arms and dataRefKinds has %d entries.\n\tSection 12 "+
			"is emitted exactly for modules whose instructions feed the reference's `datas` set "+
			"(encode.ml:1109), so an arm this table lacks is a module that needs the section and does "+
			"not get one — and one it invents is a section the reference would not write.",
			arms, len(dataRefKinds))
	}
	// Vacuity: `strings.Count` returning 0 would make the equality above a comparison between two
	// empty claims if the table were ever emptied too, which is the empty-vs-empty agreement.
	if arms == 0 {
		t.Fatalf("found no `datas (idx …)` arms in free.ml (%d bytes): the reader is not seeing the "+
			"production, and every assertion here is being made against nothing", len(free))
	}

	// Each text-side kind maps onto a real mnemonic and a real opcode. This is what catches a
	// transcription error — a kind misspelled here is a row that never fires, so a module using the
	// instruction silently loses its section 12.
	for kind := range dataRefKinds {
		mnemonic, ok := mnemonicForKind(kind)
		if !ok {
			t.Errorf("dataRefKinds has a row for %s, which no keyword in the generated table maps "+
				"to: the row is dead, so `sawDataRef` is never set for that instruction and every "+
				"module using it encodes without the data count section the decoder requires", kind)
			continue
		}
		op, ok := opBytes(mnemonic)
		if !ok {
			t.Errorf("dataRefKinds names %s (%q), which has no opcode in the generated table",
				kind, mnemonic)
			continue
		}
		// All four are prefixed (`fb`/`fc`), which is asserted rather than assumed: a single-byte
		// opcode here would mean the join found a different instruction than the one intended.
		if len(op) != 2 || (op[0] != 0xfb && op[0] != 0xfc) {
			t.Errorf("%s encodes to %#x, which is not an `fb`/`fc` prefixed opcode; all four "+
				"data-referencing instructions are (free.ml:165,166,175,181)", mnemonic, op)
			continue
		}
		// And the reference names this instruction in a `datas` arm. The constructor is the join key
		// the opcode table already uses (decision 0014), so this asks the authority directly rather
		// than trusting the mnemonic's shape.
		ctor := mnemonicOpcodes[mnemonic].constructor
		if !armMentionsDatas(free, ctor) {
			t.Errorf("dataRefKinds treats %s as referencing the data index space, but free.ml's arm "+
				"for %s does not contribute to `datas`.\n\tThis encoder would emit a data count "+
				"section the reference does not, which is the accept direction: the image decodes "+
				"clean here and is a different module than the text denotes elsewhere.",
				mnemonic, ctor)
		}
	}
}

// armMentionsDatas reports whether free.ml's arm for a constructor contributes to the `datas` set.
//
// The constructor name is the reference's CamelCase form of the mnemonic's snake_case — `data_drop`
// is `DataDrop` — and the arm is a single line in every one of the four cases, so a line-scoped match
// is enough and says so. A multi-line arm upstream would make this under-match, which is the trigger
// coverage defect (#78); it is bounded by the caller's arm count, which would then disagree.
func armMentionsDatas(free, constructor string) bool {
	want := camelFromSnake(constructor)
	for line := range strings.SplitSeq(free, "\n") {
		if !strings.Contains(line, "datas (idx ") {
			continue
		}
		// `| DataDrop x -> …` — the constructor is a whole word on the arm's left side.
		head, _, ok := strings.Cut(line, "->")
		if !ok {
			continue
		}
		for f := range strings.FieldsSeq(strings.NewReplacer("(", " ", ")", " ", ",", " ").Replace(head)) {
			if f == want {
				return true
			}
		}
	}
	return false
}

// camelFromSnake turns `array_new_data` into `ArrayNewData`, the reference's constructor spelling.
func camelFromSnake(s string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(s, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// mnemonicForKind is the reverse of the generated keyword table: the mnemonic a keyword kind is
// spelled by.
//
// Derived from `keywords` rather than written out, so it cannot name a spelling the lexer does not
// produce. All four data-referencing kinds have exactly one spelling; a kind with several would make
// the first match arbitrary, so that is reported rather than picked.
func mnemonicForKind(kind keywordKind) (string, bool) {
	found := ""
	for mnemonic, k := range keywords {
		if k != kind {
			continue
		}
		if found != "" {
			return "", false // several spellings: no single answer to give
		}
		found = mnemonic
	}
	return found, found != ""
}

// TestSectionTwelveConditionIsTheReferences pins the *shape* of section 12's condition, which the
// opcode set above cannot state: whether the section is emitted is a question about **instructions**,
// and the obvious `len(dataDefs) > 0` is wrong in both directions.
//
// `data_count_section` is guarded by `Free.((module_ m).datas <> Set.empty)` (encode.ml:1109), and
// `free.ml`'s `data` for a *segment* is `segmentmode memories mode` (:217) — a segment contributes
// nothing to the `datas` set. So:
//
//   - **segments, no reference** → no section 12, though the module has data segments.
//   - **a reference, no segments** → section 12 with a count of zero, though the module has none.
//
// Both directions are asserted, because either alone passes with `len(dataDefs) > 0` installed — and
// the falsification confirmed exactly that: swapping the condition failed 19 rows in the first
// direction and one in the second. The reject direction is real rather than hypothetical: before
// `sawDataRef` existed, `(module (func (data.drop 0)))` encoded with no section 12 and this project's
// own decoder rejected its own encoder's output with `data count section required`.
//
// **The condition's text is read off the authority too**, not only its consequences. A behavioural
// test alone agrees with any condition that happens to coincide on these rows; quoting the guard is
// what ties the behaviour to its reason, so an upstream change arrives as a failure here rather than
// as a slow divergence.
func TestSectionTwelveConditionIsTheReferences(t *testing.T) {
	enc := testenv.RequireSpecRef(t, testenv.RefEncodeML)
	if !strings.Contains(enc, "Free.((module_ m).datas <> Set.empty)") {
		t.Errorf("encode.ml no longer guards data_count_section on `Free.((module_ m).datas <> " +
			"Set.empty)`.\n\tThat guard is why sawDataRef is an instruction question rather than " +
			"`len(dataDefs) > 0`; if upstream changed it, this encoder's condition needs re-reading " +
			"rather than adjusting.")
	}
	free := testenv.RequireSpecRef(t, testenv.RefFreeML)
	if !strings.Contains(free, "let data d = let Data (_bs, mode) = d.it in segmentmode memories mode") {
		t.Errorf("free.ml's `data` is no longer `segmentmode memories mode`: a data *segment* " +
			"contributing to the `datas` set would make `len(dataDefs) > 0` right after all, and " +
			"this encoder's condition would then be wrong in the opposite direction.")
	}

	for _, tc := range []struct {
		src   string
		want  bool
		count byte
		why   string
	}{
		{
			src: `(module (memory 1) (data (i32.const 0) "a"))`, want: false,
			why: "a segment with nothing referencing it: free.ml's `data` never touches `datas`, so " +
				"`len(dataDefs) > 0` emits a section the reference does not",
		},
		{
			src: `(module (func (data.drop 0)))`, want: true, count: 0,
			why: "a reference with no segments: the count is 0 and the section is still required — " +
				"this exact image was rejected by this project's decoder with `data count section " +
				"required` before sawDataRef existed",
		},
		{
			src: `(module (memory 1) (data "x") (func (data.drop 0)))`, want: true, count: 1,
			why: "both, which is where the two quantities separate: the condition is the reference " +
				"and the count is the segments",
		},
	} {
		t.Run(tc.src, func(t *testing.T) {
			img, err := EncodeModule([]byte(tc.src))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			payload, got := sectionPayload(decodeForTest(t, img), binary.SectionDataCount)
			if got != tc.want {
				t.Fatalf("section 12 present=%v, want %v: %s", got, tc.want, tc.why)
			}
			if !tc.want {
				return
			}
			// A bare `u32` (`section 12 len …`, encode.ml:1109), not a vector: one LEB is the whole
			// section, so a trailing byte would be a second reading of the field.
			if len(payload) != 1 || payload[0] != tc.count {
				t.Errorf("section 12's payload is %#x, want the single byte %#02x: `len` is a bare u32 "+
					"and its value is the number of *segments*, a different quantity from the "+
					"condition that emitted the section", payload, tc.count)
			}
		})
	}

	// The other three members of the set have no row above, and the omission is *named* rather than
	// left as apparent coverage. `memory.init`, `array.new_data` and `array.init_data` are all
	// refused by the instruction frontier today — measured: `cannot yet encode memory.init (#8)` and
	// `cannot yet encode the array.new_data instruction's immediates (#8)` — so no module using them
	// reaches the emitter at all, and a behavioural row for them would assert the frontier rather
	// than the condition. What covers them meanwhile is
	// TestDataRefKindsMatchTheDecodersOpcodes, which is keyed on the authority and therefore
	// does not depend on a module being encodable. When #8's frontier reaches those three, this
	// table is where their rows go.
}
