package text

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// TestEndOpcodeMatchesTheDecodersOpinion pins `opEnd` against the decoder rather than against a
// second copy of the number.
//
// `opEnd` is a literal in this package and the byte that ends a block is not this package's fact to
// state. The obvious cross-check — read `optable.go`'s `0x0b` row — is not available: that table is
// unexported in `internal/binary`, and exporting it to let a test make an assertion would widen a
// package's surface for the instrument's convenience. So the check is *behavioural*, and it is better
// for it: a body terminated with `opEnd` must decode, and a body terminated with any other byte must
// not. That is the decoder's own opinion, obtained by asking rather than by copying.
//
// **`opEnd`'s comment previously cited `TestEndOpcodeMatchesTheTable`, which never existed** — the
// dangling-citation class (#114/#115/#116), caught by `TestEveryCitedTestNameResolves` rather than by
// review. The name changed with the mechanism: there is no table comparison here, and a citation
// naming one would have been accurate about the intent and wrong about the code.
func TestEndOpcodeMatchesTheDecodersOpinion(t *testing.T) {
	// A minimal module whose one function body is a single byte: the terminator. Hand-assembled
	// rather than encoded from wat, because the whole point is to vary that byte — which no wat
	// source can spell, `end` being implicit in the text grammar.
	build := func(terminator byte) []byte {
		return []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // preamble
			0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
			0x03, 0x02, 0x01, 0x00, // function: one func, type 0
			0x0a, 0x04, 0x01, 0x02, 0x00, terminator, // code: one body, no locals, terminator
		}
	}

	// The positive half, and it is the vacuity check as much as an assertion: without it, "every
	// other byte fails" is satisfied by a module that is malformed for some unrelated reason, and the
	// loop below would be agreeing with a hand-assembly error rather than with the decoder.
	if _, err := binary.DecodeModule(build(opEnd)); err != nil {
		t.Fatalf("a body terminated with opEnd (%#x) does not decode: %v — either opEnd is the wrong "+
			"byte, or this hand-assembled module is wrong about something else, in which case the "+
			"negative half below proves nothing", opEnd, err)
	}

	// The negative half, scoped to the space: all 256 values rather than a sample. `end` must be the
	// *only* byte that terminates a one-instruction body, because a second one that also decoded
	// would mean the positive case does not identify `end` at all — `opEnd` could be either value and
	// this control would stay green.
	for b := range 256 {
		if byte(b) == opEnd {
			continue
		}
		if _, err := binary.DecodeModule(build(byte(b))); err == nil {
			t.Errorf("a body terminated with %#02x also decodes, so the positive case above does not "+
				"identify `end`", b)
		}
	}
}

// TestElseOpcodeMatchesTheDecodersOpinion pins `opElse` the same way, and its discriminator is
// sharper than the END test's because a lone byte cannot identify this one.
//
// `else` is legal *inside an `if`* and illegal inside a `block`, which is a two-module question. The
// looser form — "does a body containing this byte decode" — is passed by `nop`, `i32.const`'s opcode
// with its immediate, and every other zero-immediate instruction, so it would leave `opElse` free to
// be any of dozens of values and stay green. Asking both modules narrows the answer to one byte, and
// the loop below asserts that: **exactly one** value of 256 is accepted by the `if` and rejected by
// the `block`, measured rather than assumed.
//
// Hand-assembled for the END test's reason with one addition: no wat source can put an `else` in a
// `block`, so the illegal half of the question has no text spelling at all.
func TestElseOpcodeMatchesTheDecodersOpinion(t *testing.T) {
	// `opener blocktype-empty candidate end end` — the outer END is the function's, the inner one
	// closes the construct. `0x40` is the empty blocktype; see blockTypeEmptyByte.
	build := func(opener, candidate byte) []byte {
		return []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // preamble
			0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
			0x03, 0x02, 0x01, 0x00, // function: one func, type 0
			0x0a, 0x08, 0x01, 0x06, 0x00, // code: one body, no locals
			opener, blockTypeEmptyByte, candidate, opEnd, opEnd,
		}
	}
	// `if` needs its condition on the stack, so the opener is preceded by an `i32.const 0`. Written
	// as its own builder rather than a flag, because the two modules differ in length and the length
	// prefix is hand-written.
	buildIf := func(candidate byte) []byte {
		return []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
			0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
			0x03, 0x02, 0x01, 0x00,
			0x0a, 0x0a, 0x01, 0x08, 0x00,
			0x41, 0x00, // i32.const 0
			0x04, blockTypeEmptyByte, candidate, opEnd, opEnd,
		}
	}

	// The vacuity half, in both directions. Without the first, "the `if` accepts it" is satisfied by
	// a hand-assembly that is accepted for some other reason; without the second, the *block* module
	// might be malformed independently of the candidate byte and every candidate would look
	// discriminating.
	if _, err := binary.DecodeModule(buildIf(opElse)); err != nil {
		t.Fatalf("an `if` containing opElse (%#x) does not decode: %v — either opElse is the wrong "+
			"byte or this hand-assembly is wrong, and the count below proves nothing either way",
			opElse, err)
	}
	if _, err := binary.DecodeModule(build(0x02, 0x01)); err != nil { // block · nop · end
		t.Fatalf("a `block` containing nop does not decode: %v — the block builder is wrong, so the "+
			"negative half of the discriminator below is measuring the builder", err)
	}

	// Scoped to the space, and the assertion is on the *count*: a discriminator that admitted two
	// bytes would not identify `else` at all, and one that admitted none would mean the question is
	// the wrong one. Exactly one, and it is opElse.
	var discriminating []byte
	for b := range 256 {
		_, inIf := binary.DecodeModule(buildIf(byte(b)))
		_, inBlock := binary.DecodeModule(build(0x02, byte(b)))
		if inIf == nil && inBlock != nil {
			discriminating = append(discriminating, byte(b))
		}
	}
	if len(discriminating) != 1 || discriminating[0] != opElse {
		t.Errorf("bytes legal in an `if` and illegal in a `block`: % #x, want exactly [%#x] — the "+
			"decoder's opinion of which byte separates an if's arms", discriminating, opElse)
	}
}

// TestBlockTypeFormsMatchTheReference pins the three blocktype forms against the decoder's unpacking,
// which is where a width error hides.
//
// **The forms are an `s33`, and the tempting writer is a `u32`** — `blockTypeIdxBytes` says why at
// length. The two agree for every index below 64 and diverge at 64, where a u32 emits the single byte
// `0x40`: precisely the empty-blocktype marker. So an encoder using the wrong writer produces, for
// `(block (type 64) …)`, a block with *no signature*, which decodes clean and validates differently.
// That is the accept-direction class in its purest form — no `assert_malformed` vector can report a
// module that was accepted — and 64 is the one value where a control has to look.
//
// Asked through `binary.BlockType`, the decoder's own unpacker, rather than by comparing bytes to a
// second table of expected bytes: the question is what the *decoder* reads back, and a byte
// comparison would agree with a wrong constant as readily as with a right one.
func TestBlockTypeFormsMatchTheReference(t *testing.T) {
	// A module with `n+1` types so index `n` exists, then a body of `block <bt> end`. The type
	// section's entries are all `[] -> []`, which is legal but not what the blocktype *means* here —
	// nothing validates, and the question is only what byte sequence the decoder reads back.
	build := func(types int, bt []byte) []byte {
		var w writer
		w.bytes(binary.Magic[:])
		w.u32le(binary.Version)
		w.section(secType, func(b *writer) {
			b.vec(types, func(bw *writer, _ int) { bw.byte1(0x60); bw.u32(0); bw.u32(0) })
		})
		w.section(secFunction, func(b *writer) { b.vec(1, func(bw *writer, _ int) { bw.u32(0) }) })
		w.section(secCode, func(b *writer) {
			b.vec(1, func(bw *writer, _ int) {
				var fb writer
				fb.u32(0) // no locals
				fb.byte1(0x02)
				fb.bytes(bt)
				fb.byte1(opEnd)
				fb.byte1(opEnd)
				bw.u32(uint32(len(fb.b)))
				bw.bytes(fb.b)
			})
		})
		return w.b
	}
	// The first instruction of the one function's body, unpacked.
	blockTypeOf := func(t *testing.T, img []byte) (uint32, binary.ValType, bool) {
		t.Helper()
		m, err := binary.DecodeModule(img)
		if err != nil {
			t.Fatalf("hand-assembled module does not decode: %v", err)
		}
		if len(m.Funcs) != 1 || len(m.Funcs[0].Body) == 0 {
			t.Fatalf("expected one function with a body, got %d funcs", len(m.Funcs))
		}
		return binary.BlockType(m.Funcs[0].Body[0].Imm0)
	}

	t.Run("empty", func(t *testing.T) {
		idx, vt, empty := blockTypeOf(t, build(1, []byte{blockTypeEmptyByte}))
		if !empty {
			t.Errorf("blockTypeEmptyByte (%#x) decodes as idx=%d valtype=%v, want the empty form",
				blockTypeEmptyByte, idx, vt)
		}
	})

	// The **forward** direction of the same packing, which is what `encodableModules`' want columns
	// state and which no exported function supplies — see blockTypeImm's comment. Checked here
	// because this is the test that already holds the decoder's unpacker: a drifted literal would
	// otherwise make every block row in that table wrong in the same direction, and a want column
	// wrong in the encoder's own direction agrees with a wrong encoder.
	t.Run("packed Imm0 literals", func(t *testing.T) {
		if idx, vt, empty := binary.BlockType(blockTypeImm); !empty {
			t.Errorf("blockTypeImm (%#x) unpacks as idx=%d valtype=%v empty=false, want the empty form",
				blockTypeImm, idx, vt)
		}
		for _, want := range []binary.ValType{binary.I32, binary.I64, binary.F32, binary.F64, binary.FuncRef} {
			imm := blockTypeValTypeImm(want)
			idx, vt, empty := binary.BlockType(imm)
			if empty || vt != want {
				t.Errorf("blockTypeValTypeImm(%v) = %#x, which unpacks as idx=%d valtype=%v empty=%v",
					want, imm, idx, vt, empty)
			}
		}
		// And neither literal may collide with a type index, which is the disjointness the tags
		// living above 2^32 buys — asserted rather than assumed, since a want column of `Imm0: 0`
		// for `(block)` is exactly the mistake this rules out.
		if idx, _, empty := binary.BlockType(0); empty || idx != 0 {
			t.Errorf("Imm0 0 unpacks as idx=%d empty=%v, want type index 0 — so the empty blocktype "+
				"cannot be written as a zero want column", idx, empty)
		}
	})

	t.Run("single result", func(t *testing.T) {
		// The `([], [t])` arm, written as a bare valtype byte and not as an s33 — see
		// blockTypeBytes. `i32` is 0x7f, which as a signed LEB is -1, so a reader treating this
		// arm as an index would see a negative one.
		b, ok := valTypeByte(resolvedVal{num: "i32"})
		if !ok {
			t.Fatalf("i32 has no valtype byte, so this package cannot encode any block result")
		}
		idx, vt, empty := blockTypeOf(t, build(1, []byte{b}))
		if empty || vt != binary.I32 {
			t.Errorf("a bare %#x decodes as idx=%d valtype=%v empty=%v, want the i32 valtype form",
				b, idx, vt, empty)
		}
	})

	// The index form at both sides of the divergence. 63 is where s33 and u32 agree, so it proves
	// the *form* is right; 64 is where they differ, and it is the row a u32 writer fails.
	for _, idx := range []uint32{0, 1, 63, 64, 65, 127, 128} {
		t.Run(fmt.Sprintf("index %d", idx), func(t *testing.T) {
			got, vt, empty := blockTypeOf(t, build(int(idx)+1, blockTypeIdxBytes(idx)))
			if empty || got != idx {
				t.Errorf("blockTypeIdxBytes(%d) = % #x, which decodes as idx=%d valtype=%v empty=%v",
					idx, blockTypeIdxBytes(idx), got, vt, empty)
			}
		})
	}

	// The falsification, stated as a row rather than left to a reviewer: index 64 written as a u32
	// *is* the empty marker, so the wrong writer is not merely imprecise — it encodes a different
	// blocktype that decodes clean. Asserted here so the divergence is a checked fact and not a
	// claim in blockTypeIdxBytes' comment.
	if !slices.Equal(encodeLocalIdx(64), []byte{blockTypeEmptyByte}) {
		t.Errorf("encodeLocalIdx(64) = % #x, want % #x — the premise blockTypeIdxBytes exists for",
			encodeLocalIdx(64), []byte{blockTypeEmptyByte})
	}
	if slices.Equal(blockTypeIdxBytes(64), encodeLocalIdx(64)) {
		t.Errorf("blockTypeIdxBytes(64) = % #x agrees with the u32 writer, so the s33 form is not "+
			"being written", blockTypeIdxBytes(64))
	}
}

// TestSelectOpcodesMatchTheGeneratedTable is the corroboration `opSelect`/`opSelectT` are named
// against, and it asserts exactly what the table can say — no more.
//
// **The table holds both bytes and cannot say which is which.** `ambiguousOpcodes["select"]` is a
// slice whose order is `decode.ml`'s arm order as `OpsOf` happened to append it, and `Ambiguity` is
// sorted by *constructor*, so the pair's internal order is not a fact `opcodes.go` states. Reading
// `[0]` as the bare form would be depending on it anyway. So this checks the *set* — the two named
// constants are exactly the two encodings the reference gives the mnemonic — and
// TestSelectOpcodesMatchTheDecodersOpinion is what distinguishes them, from the decoder.
//
// The mnemonic's presence in `ambiguousOpcodes` is asserted too, and that is the half that catches a
// generator change: if the reference stopped giving `select` two encodings the map would lose the key
// and the set comparison below would silently compare against nothing — the empty-set agreement
// (decision 0007).
func TestSelectOpcodesMatchTheGeneratedTable(t *testing.T) {
	got, ok := ambiguousOpcodes["select"]
	if !ok {
		t.Fatalf("`select` is not in ambiguousOpcodes, so the two-encoding premise opSelect and "+
			"opSelectT are named on no longer holds: %d ambiguous mnemonics", len(ambiguousOpcodes))
	}
	codes := make([]byte, 0, len(got))
	for _, e := range got {
		if e.prefix != 0x00 {
			t.Errorf("select encoding % #x has a prefix, which neither named constant can carry", e)
			continue
		}
		codes = append(codes, byte(e.code))
	}
	slices.Sort(codes)
	want := []byte{opSelect, opSelectT}
	slices.Sort(want)
	if !slices.Equal(codes, want) {
		t.Errorf("ambiguousOpcodes[select] carries % #x, want the set % #x", codes, want)
	}
}

// TestSelectOpcodesMatchTheDecodersOpinion distinguishes the two select bytes, which the table cannot.
//
// The discriminator is the immediate: `0x1b` takes none and `0x1c` is followed by `vec valtype`
// (encode.ml:248-249). So a one-instruction body of the bare byte decodes for one of them and is
// truncated for the other, and that is a question about the *decoder* rather than about a table.
//
// **Both directions, because either alone is satisfied by the wrong assignment.** If the constants
// were swapped, `opSelect` alone would fail to decode and `opSelectT` followed by a vector would
// fail too — but a control checking only "opSelect decodes bare" would pass with any zero-immediate
// opcode substituted, and one checking only the vector form would pass for any opcode taking a
// vector. Pinning both to the *same pair of bytes* is what makes the assignment the only one that
// satisfies it.
func TestSelectOpcodesMatchTheDecodersOpinion(t *testing.T) {
	build := func(body ...byte) []byte {
		var w writer
		w.bytes(binary.Magic[:])
		w.u32le(binary.Version)
		w.section(secType, func(b *writer) {
			b.vec(1, func(bw *writer, _ int) { bw.byte1(0x60); bw.u32(0); bw.u32(0) })
		})
		w.section(secFunction, func(b *writer) { b.vec(1, func(bw *writer, _ int) { bw.u32(0) }) })
		w.section(secCode, func(b *writer) {
			b.vec(1, func(bw *writer, _ int) {
				var fb writer
				fb.u32(0)
				fb.bytes(body)
				fb.byte1(opEnd)
				bw.u32(uint32(len(fb.b)))
				bw.bytes(fb.b)
			})
		})
		return w.b
	}

	if _, err := binary.DecodeModule(build(opSelect)); err != nil {
		t.Errorf("opSelect (%#x) alone does not decode, so it is not the immediate-less form: %v",
			opSelect, err)
	}
	if _, err := binary.DecodeModule(build(opSelectT)); err == nil {
		t.Errorf("opSelectT (%#x) alone decodes, so it takes no result vector — the two constants "+
			"are assigned the wrong way round", opSelectT)
	}
	// `0x1c` with its vector, both lengths that matter: one type, and the **empty** vector, which is
	// the `select (result)` spelling selectOpByte exists for. If the empty case did not decode,
	// `len(results) > 0` would be a defensible predicate; it does, so it is not.
	for _, vec := range [][]byte{{0x01, 0x7f}, {0x00}} {
		img := build(append([]byte{opSelectT}, vec...)...)
		if _, err := binary.DecodeModule(img); err != nil {
			t.Errorf("opSelectT with vector % #x does not decode: %v", vec, err)
		}
	}
}

// TestIdxLookupKindsMatchTheReference is the drift control on `idxLookupKinds`, the hand-written
// mnemonic→index-category table.
//
// **The category is not in the grammar.** Every relevant `plaininstr` arm reads `idx`, and which
// space that index resolves against is the argument the *semantic action* passes — `$2 c label`
// against `$2 c func` against `$2 c local`. So this reads `productionBody`, which keeps actions,
// rather than `productionArms`, which strips them; a reader that strips actions cannot see the fact
// under test at all. That is why `productionBody` exists as a separate helper.
//
// Getting a category wrong is an accept-direction defect of the worst kind. `br $x` and `local.get $x`
// are both legal, both encode to a one-byte immediate, and a table resolving a `br` against the local
// space emits a *different, valid* module — decoding clean, scoring green, computing something else.
// No suite vector reports it (§9 G-3), because every `unknown <space>` vector in the corpus is an
// `assert_invalid` with a *numeric* index, which no lookup consults. Hence an authority rather than
// care.
//
// **`idxLookupKinds`' comment cited this test before it existed**, same class and same finder as
// `opEnd`'s above.
func TestIdxLookupKindsMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefParserMLY)
	body := productionBody(t, src, "plaininstr")

	// The category as the reference spells it, in an action. Note `type_`: OCaml reserves `type`, so
	// the reference's identifier carries the underscore, and the alternation is written out and
	// `\b`-anchored rather than being a general `c (\w+)` — a loose pattern would match `c x` in some
	// unrelated action and invent a category the enum has no name for.
	reCategory := regexp.MustCompile(`\bc\s+(label|func|local|global|table|memory|tag|type_|data|elem)\b`)

	// perKind[kind] is one entry per *arm*, each entry being that arm's categories in order.
	//
	// **Per arm, because two kinds have two arms that disagree, and the table states the shorter
	// one.** `MEMORY_INIT idx idx` passes `memory` then `data`; the sugar arm `MEMORY_INIT idx`
	// passes only `data`, the memory index defaulting to 0. So the category a *written* index takes
	// is the one from the arm with the fewest categories, which is the fact `idxLookupKinds` records
	// and calls out in its own comment. A first-arm-wins reader — which is what this test's first
	// draft did — reports `memory` and `table` for those two kinds and flags the table as drifted
	// when the table is right. Two rows out of 47, and both of them the ones the table's comment
	// exists to warn about: the arm order in the file is not the disambiguation order.
	perKind := map[keywordKind][][]string{}
	arms := 0
	for chunk := range strings.SplitSeq(body, "\n  | ") {
		fields := strings.Fields(chunk)
		if len(fields) == 0 {
			continue
		}
		kind := keywordKind(fields[0])
		if kind != keywordKind(strings.ToUpper(string(kind))) {
			continue // a lowercase leader is a nonterminal, not a mnemonic arm
		}
		cats := []string{}
		for _, m := range reCategory.FindAllStringSubmatch(chunk, -1) {
			cats = append(cats, m[1])
		}
		if len(cats) == 0 {
			continue // an arm passing no category: no immediate, or a non-idx one
		}
		arms++
		perKind[kind] = append(perKind[kind], cats)
	}

	// Vacuity, per partition rather than one total: arms *and* kinds, because a reader that stopped
	// working leaves both empty and every comparison below then agrees with nothing. **49 arms over
	// 47 kinds at bdd7164**, printed by the extractor rather than hand-counted (`plaininstrArms`'
	// comment records what a hand tally cost there). The floors sit below those, so an instruction
	// arriving upstream does not fail this control while a collapse does.
	if arms < 40 || len(perKind) < 38 {
		t.Fatalf("extracted %d category-passing arms over %d kinds, want >=40 and >=38 (49/47 at "+
			"bdd7164): the reader is not seeing the production's actions, and every row below would "+
			"be compared against nothing", arms, len(perKind))
	}

	// The category a written index takes: from the arm passing the fewest, per the note above.
	firstCat := map[keywordKind]string{}
	for kind, armCats := range perKind {
		fewest := slices.MinFunc(armCats, func(a, b []string) int { return len(a) - len(b) })
		firstCat[kind] = fewest[0]
	}

	// Direction one: every row in the table names the category the reference's arm actually passes.
	// This is what catches a transcription error — a `br` filed under `local`.
	for kind, want := range idxLookupKinds {
		got, ok := firstCat[kind]
		if !ok {
			t.Errorf("idxLookupKinds has a row for %s, which passes no lookup category in any "+
				"`plaininstr` arm: either the kind is misspelled — in which case the row is dead and "+
				"the mnemonic resolves against nothing — or upstream changed the arm", kind)
			continue
		}
		if refName := refCategoryNames[want]; got != refName {
			t.Errorf("idxLookupKinds resolves %s against %q, but its reference arm passes %q (arms: "+
				"%v): a wrong category emits a *different, valid* module, which no suite vector "+
				"reports", kind, refName, got, perKind[kind])
		}
	}

	// Direction two, and it is the one scoped to the space rather than to the slice: an arm the
	// reference gives a category and the table omits. A missing row is not automatically wrong — a
	// category with no index space yet is refused rather than resolved — but it must be *named*,
	// because an unexplained omission is the unreachable-error pattern wearing a table's clothes
	// (#6). The exemption map is empty today: all 47 have rows.
	for kind := range firstCat {
		if _, ok := idxLookupKinds[kind]; ok {
			continue
		}
		if !categoriesWithNoSpaceYet[kind] {
			t.Errorf("the reference's %s arm passes lookup categories %v and idxLookupKinds has no "+
				"row for it: either add the row, or name it in categoriesWithNoSpaceYet with the "+
				"reason (#6 — declared and tracked, never silent)", kind, perKind[kind])
		}
	}
}

// refCategoryNames maps this package's index category to the reference's own identifier for it: the
// word the semantic action passes. One direction of one fact, so the comparison above reads the
// reference's vocabulary instead of translating it twice.
//
// `catNone` is deliberately absent — it is the zero value meaning "this kind takes no index", and no
// arm passes it. Mapping it to some spelling would make a *missing* `idxLookupKinds` row compare
// equal to a real category, which is the map-returns-zero defect `TestEveryKindConstantIsInTheTable`
// covers one layer down.
var refCategoryNames = map[idxCategory]string{
	catLabel:  "label",
	catFunc:   "func",
	catLocal:  "local",
	catGlobal: "global",
	catTable:  "table",
	catMemory: "memory",
	catTag:    "tag",
	catType:   "type_", // OCaml reserves `type`; the reference's identifier carries the underscore
	catData:   "data",
	catElem:   "elem",
}

// categoriesWithNoSpaceYet are kinds the reference gives a lookup category and `idxLookupKinds`
// deliberately omits, each with the reason it is refused rather than resolved.
//
// **Empty, and that is a measurement**: all 47 category-passing arms at bdd7164 have rows, because
// `idxLookupKinds` is scoped to the space rather than to the tier this section encodes. The map
// exists so an arm arriving upstream lands as a failure demanding a classification — add the row, or
// state why the space does not exist — rather than as a silent omission.
var categoriesWithNoSpaceYet = map[keywordKind]bool{}

// TestRefCategoryNamesCoversEveryCategory keeps the helper table above honest, and it is not
// ceremony: `refCategoryNames` is a map, so a missing entry yields `""`, and `""` compares unequal to
// every real spelling — turning a *gap in the test's own vocabulary* into a stream of confident
// failures about the code under test. A control's blind spot reported as the subject's defect is
// worse than no control.
//
// Scoped to the space by walking `idxLookupKinds`' values rather than by listing categories: a
// category added to the enum and used in a row arrives here as a missing name.
func TestRefCategoryNamesCoversEveryCategory(t *testing.T) {
	used := map[idxCategory]bool{}
	for _, cat := range idxLookupKinds {
		used[cat] = true
	}
	if len(used) < 8 {
		t.Fatalf("only %d distinct categories are used by idxLookupKinds, which is too few to be the "+
			"real set (10 at bdd7164): the table did not load and this check is comparing nothing",
			len(used))
	}
	var missing []string
	for cat := range used {
		if refCategoryNames[cat] == "" {
			missing = append(missing, fmt.Sprintf("idxCategory(%d)", cat))
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("refCategoryNames has no entry for %v, so TestIdxLookupKindsMatchTheReference "+
			"compares the reference's spelling against \"\" and reports drift that is really this "+
			"table's gap", missing)
	}
}

// TestIntoSinkGatesOnTheModeNotTheSink is grave #144: `intoSink`, whose job is to *install* a sink,
// asked whether one was *already* installed.
//
// The two questions — `p.retain` (this parse is building a module) and `p.retaining()` (a sink is
// installed right now) — agree at every site inside a function body, because `funcField` installs the
// outer sink before any of them runs. That was every caller until section 9. At **module-field scope**
// they come apart: nothing installs a sink there, `retainedOffset` installs one for the offset alone
// and restores it before the element list is read, so `elemIdxSink` and `elemexprRetained` took the
// not-retaining short-circuit and returned empty sinks on a parse that was retaining.
//
// **What makes it worth a control rather than a one-line fix is the shape of the symptom.** Every
// segment's flag, offset, table index and reftype came out correct and only the element *contents*
// vanished, so `(module (func) (elem func 0))` wrote `01 01 00 00` — a legal section 9 declaring a
// segment with zero elements. It decodes clean and denotes a different module. That is the
// accept-direction class no `assert_malformed` can see (§9 G-3), and the only reason the board went
// red is that `wantElemSec` is a **hand-written** want column; a fixture generated from the encoder
// would have agreed with the defect.
//
// Watched die by reverting the condition in all three places (`intoSink`, `elemIdxList`, and the
// `p.retain` read that replaced them): every row below reports zero elements where it wants some.
//
// **Scoped to the space rather than to the row that failed.** The subject is not `(elem …)` — it is
// *any* retention at module-field scope, a category section 9 is merely the first member of. The
// table sugar is here because its `min = max = len(einit)` arithmetic converts the same bug into a
// wrong table *type*, which is a second observable for one defect; and the two-element rows are here
// because a length-one list cannot distinguish "one element retained" from "the count is written from
// the parse and the elements from the sink".
//
// **The element *count* is the wrong observable for half the grave, and the falsification is what said
// so.** The two gates fail differently: `elemIdxList`'s returns a nil slice, so the count goes to zero
// and a count check sees it — but `intoSink`'s returns an *empty sink per element*, so the count is
// right and every element is zero bytes long. Reverting `intoSink` alone left all ten rows **green** on
// a count-only assertion, which is a control passing while the defect it was written for is present.
// So each row states its expected *bytes*, and the count survives as the cheaper half of the same
// check. This is the difference between a floor and an exact count, in a place where the exact figure
// was available for the asking.
func TestIntoSinkGatesOnTheModeNotTheSink(t *testing.T) {
	// Each row states the bytes each element must encode to, which is the observable that sees both
	// gates; `elems` is the count, redundant with `len(wantExprs)` and stated anyway because a row
	// whose two columns disagree is a row that was edited carelessly.
	for _, tc := range []struct {
		src   string
		elems int
		// wantExprs is each element's constant expression as `defineElem` renders it, terminator
		// included. `d2 00 0b` is `ref.func 0` then `end`; a bare `0b` is an element whose
		// instructions went to an empty sink, which is `intoSink`'s half of the grave.
		wantExprs []string
		// tabMin is the table size the sugar derives from the element count, or 0 when the row has
		// no sugar. This is the second observable: the parser computes it from `len(elems)` at the
		// cursor, so an empty list is a `(table 0 0 funcref)` in the image.
		tabMin uint64
	}{
		// The index-list arms, which reach `elemIdxSink` — one nested sink per synthesized
		// `ref.func`, and the path `elemIdxList`'s own gate guarded.
		{src: `(module (func) (elem func 0))`, elems: 1, wantExprs: []string{"d2 00 0b"}},
		{src: `(module (func) (elem func 0 0))`, elems: 2, wantExprs: []string{"d2 00 0b", "d2 00 0b"}},
		{src: `(module (func) (elem declare func 0 0))`, elems: 2, wantExprs: []string{"d2 00 0b", "d2 00 0b"}},
		{
			src: `(module (func) (table 1 funcref) (elem (i32.const 0) 0 0))`, elems: 2,
			wantExprs: []string{"d2 00 0b", "d2 00 0b"},
		},
		// The expression arms, which reach `elemexprRetained` — `intoSink` directly. These are the rows
		// a count-only assertion could not see.
		{src: `(module (func) (elem funcref (ref.func 0)))`, elems: 1, wantExprs: []string{"d2 00 0b"}},
		{
			src: `(module (func) (elem funcref (ref.func 0) (ref.func 0)))`, elems: 2,
			wantExprs: []string{"d2 00 0b", "d2 00 0b"},
		},
		// Two instructions in *one* element, so the row also pins that the boundary between elements is
		// the sink's and not the instruction's: a shared sink would report one element of six bytes here
		// and the row above would report one element where it wants two.
		{
			src: `(module (func) (elem funcref (item (ref.func 0) (ref.func 0))))`, elems: 1,
			wantExprs: []string{"d2 00 d2 00 0b"},
		},
		{
			src: `(module (func) (elem declare funcref (ref.func 0) (ref.func 0)))`, elems: 2,
			wantExprs: []string{"d2 00 0b", "d2 00 0b"},
		},
		// The `(table … (elem …))` sugar, both arms: the element count is observable *twice*, once as
		// the segment's vector and once as the table's own min and max.
		{
			src: `(module (func) (table funcref (elem 0 0)))`, elems: 2,
			wantExprs: []string{"d2 00 0b", "d2 00 0b"}, tabMin: 2,
		},
		{
			src: `(module (func) (table funcref (elem (ref.func 0))))`, elems: 1,
			wantExprs: []string{"d2 00 0b"}, tabMin: 1,
		},
	} {
		t.Run(tc.src, func(t *testing.T) {
			p, err := parseModule([]byte(tc.src), build)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// **No `runDeferred` call here, and the first draft of this test had one.** `parseModule`
			// runs the deferred thunks itself, at the end of `moduleFields`, so calling it again runs
			// every thunk twice — and `defineElem` *appends* to `exprs` and `funcs`, so the second pass
			// doubles them. The symptom was every row reporting exactly 2N where it wanted N, which
			// reads like a duplicate-emission bug in the encoder and is an artefact of the instrument.
			// Worth the note because "exactly double" is the tell, and it points at the harness rather
			// than at the subject: a defect in the code under test would have no reason to land on a
			// clean factor.
			if len(p.ctx.elemDefs) != 1 {
				t.Fatalf("%d element segments retained, want 1", len(p.ctx.elemDefs))
			}
			if len(tc.wantExprs) != tc.elems {
				t.Fatalf("the row wants %d elements and lists %d expressions: a row disagreeing with "+
					"itself asserts whichever column is read first", tc.elems, len(tc.wantExprs))
			}
			e := p.ctx.elemDefs[0]
			// Both renderings are filled by `defineElem`, whichever the wire ends up taking, so both
			// are checked — a gate fixed for one and not the other is a live half of the same grave.
			if len(e.exprs) != tc.elems || len(e.funcs) != tc.elems {
				t.Fatalf("the segment holds %d expressions and %d indices, want %d of each.\n\t"+
					"Zero here is grave #144's elemIdxList half: a retention reader at module-field "+
					"scope asked p.retaining() — is a sink installed — where the question is p.retain, "+
					"the parse's mode. The flag and offset are still right, so the image decodes clean "+
					"and denotes a module with an empty segment.", len(e.exprs), len(e.funcs), tc.elems)
			}
			// The bytes, which is the half a count cannot see: `intoSink`'s gate yields an *empty sink
			// per element*, so the count is right and each expression is a bare terminator.
			for i, want := range tc.wantExprs {
				if got := fmt.Sprintf("% x", e.exprs[i]); got != want {
					t.Errorf("element %d encodes to %q, want %q.\n\tA bare \"0b\" is grave #144's "+
						"intoSink half — the element's instructions went into a sink that was never "+
						"installed, so the segment has the right number of elements and none of them "+
						"has any content. No count check can see this.", i, got, want)
				}
			}
			if tc.tabMin == 0 {
				return
			}
			if len(p.ctx.tabDefs) != 1 {
				t.Fatalf("%d tables retained, want 1", len(p.ctx.tabDefs))
			}
			// The sugar's second observable: `min = max = len(einit)` (parser.mly:1216-1222) is
			// computed at the cursor from the very slice the gate emptied.
			if got := p.ctx.tabDefs[0].lim; got.min != tc.tabMin || got.max != tc.tabMin || !got.hasMax {
				t.Errorf("the sugar's table is %+v, want min=max=%d with hasMax: the size is derived "+
					"from len(einit), so an empty element list writes a zero-sized table — the same "+
					"grave observed as a wrong table type rather than a short vector", got, tc.tabMin)
			}
		})
	}
}
