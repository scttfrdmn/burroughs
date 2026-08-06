package text

import (
	"fmt"
	"maps"
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

// positionalLookups extracts, per `plaininstr` arm, the lookup expressions the arm's semantic action
// passes — **positionally**, `$N c <expr>`, where `<expr>` is a word or a parenthesised expression.
//
// # Why a second extractor rather than a parameter on the first one
//
// `TestIdxLookupKindsMatchTheReference`'s reader matches `c <word>` against a ten-way alternation of
// the module-level space names. That is the right instrument for its question and structurally
// incapable of answering this one: `struct.get`'s second lookup is `$3 c (field x.it)` (parser.mly:622),
// and `(field x.it)` is not a word in the alternation, so the alternation reader sees **one** lookup in
// an arm that passes two. Measured, both readers counting the same way: the alternation finds 8
// two-lookup arms and this finds **10**, and the two extra are exactly `STRUCT_GET` and `STRUCT_SET`.
//
// The disagreement is the finding, and it is why this extractor exists rather than a widened
// alternation. A pattern loose enough to catch `(field x.it)` is loose enough to catch any
// parenthesised expression in any action, which is how a category the enum has no name for gets
// invented — the exact hazard the alternation was written narrow for. Anchoring on `$N c` instead
// keeps the looseness on the *argument* while the `$N c` prefix stays the discriminator.
//
// **The prose in `idxPairLookupKinds` said this instrument finds 9 and it finds 10** — a number typed
// before the extractor existed, and the count is now printed by the extractor's own vacuity floor
// rather than asserted in a sentence. Same class as `plaininstrArms`' hand tally: a figure nobody ran.
func positionalLookups(t *testing.T) map[keywordKind][][]string {
	t.Helper()
	src := testenv.RequireSpecRef(t, testenv.RefParserMLY)
	body := productionBody(t, src, "plaininstr")

	// `$N c <expr>`. The argument alternative is ordered parenthesised-first, because Go's regexp is
	// leftmost-first among alternatives at a position and a bare-word branch tried first would match
	// nothing at `(` and skip the lookup entirely.
	re := regexp.MustCompile(`\$\d+\s+c\s+(\([^)]*\)|[a-z_][a-z_0-9]*)`)

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
		var cats []string
		for _, m := range re.FindAllStringSubmatch(chunk, -1) {
			cats = append(cats, m[1])
		}
		if len(cats) == 0 {
			continue
		}
		arms++
		perKind[kind] = append(perKind[kind], cats)
	}

	// Vacuity, per partition rather than one total, and the partitions here are *arms*, *kinds*, and
	// *two-lookup arms* — three, because the third is this reader's whole subject and the first two can
	// be healthy while it is zero. A reader that stopped seeing the second lookup of every arm leaves
	// arms and kinds at their full 49/47 and the pair set empty, and every comparison below would then
	// agree with an empty set. **49 arms over 47 kinds with 10 two-lookup arms at bdd7164**, printed by
	// the extractor. The floors sit under those so an arm arriving upstream does not fail the control.
	pairs := 0
	for _, armCats := range perKind {
		for _, cats := range armCats {
			if len(cats) >= 2 {
				pairs++
			}
		}
	}
	if arms < 40 || len(perKind) < 38 || pairs < 8 {
		t.Fatalf("the positional reader found %d lookup-passing arms over %d kinds with %d passing two, "+
			"want >=40, >=38 and >=8 (49/47/10 at bdd7164): it is not seeing the production's actions, "+
			"and the two-lookup floor is the one that matters here — the other two can be full while "+
			"this reader's actual subject is empty", arms, len(perKind), pairs)
	}

	// **The floor above cannot tell this reader from the alternation reader, and that is what the
	// falsification exercise found.** Stubbing the regexp to the alternation pattern yields *exactly*
	// 8 two-lookup arms — the floor's own value — so the degraded reader passed the floor and then
	// reported drift in `idxPairLookupKinds`, which is a control's blind spot presented as the
	// subject's defect. A floor bounds the catastrophic case (the regexp matching nothing) and cannot
	// see a 2-of-10 silent loss; the exact instrument has to sit beside it, and here the exact fact is
	// not a frozen count but the **discrimination** this extractor was written for: the field
	// expression must actually be among the lookups it returns. That is derived from the reference, so
	// it does not freeze at a revision the way `pairs == 10` would.
	if !slices.ContainsFunc(slices.Collect(maps.Values(perKind)), func(armCats [][]string) bool {
		return slices.ContainsFunc(armCats, func(cats []string) bool { return slices.Contains(cats, "(field x.it)") })
	}) {
		t.Fatalf("the positional reader returned %d two-lookup arms and none of them passes a "+
			"parenthesised lookup expression, so it is behaving as the alternation reader in "+
			"TestIdxLookupKindsMatchTheReference does — which is the one thing this extractor exists "+
			"not to do. The pair floor above cannot see this: the degraded reader finds exactly 8, "+
			"which is the floor", pairs)
	}
	return perKind
}

// TestIdxPairLookupKindsMatchTheReference is the drift control on `idxPairLookupKinds`: for every arm
// the reference gives *two* lookup categories, both of them, in written order.
//
// **The opposite selector from the single-index control, over the same production.** That one takes the
// arm with the *fewest* categories, because the category a written index takes is the sugar arm's;
// this takes the arm with the **most**, because the pair is a fact about the two-index spelling.
// `TABLE_INIT` is the row where they disagree — `catElem` there, `{catTable, catElem}` here — and a
// control that shared a selector would have to call one of the two tables drifted.
//
// The defect it exists to catch is `retainIdxPair`'s: a wrong *second* category resolves
// `table.init $t $e`'s element index in the table space, emitting a legal image that denotes a
// different instruction. §9 G-3 — every `unknown elem` vector in the corpus is an `assert_invalid`
// with a numeric index, which consults no space at all, so the suite scores this green by construction.
//
// **Watched die**, in both directions and per direction rather than once: transposing
// `TABLE_INIT`'s pair to `{catElem, catTable}` fails direction one naming both positions; deleting the
// `ARRAY_COPY` row fails direction two; and stubbing the extractor's regexp to the alternation pattern
// fails the pair floor in `positionalLookups` rather than reporting drift, which is the vacuity check
// doing its job instead of this test doing it wrongly.
func TestIdxPairLookupKindsMatchTheReference(t *testing.T) {
	perKind := positionalLookups(t)

	// The pair a two-index spelling takes: from the arm passing the *most* categories. Where a kind has
	// one arm, that arm; where it has two (the sugar kinds), the two-index one.
	pairs := map[keywordKind][]string{}
	for kind, armCats := range perKind {
		most := slices.MaxFunc(armCats, func(a, b []string) int { return len(a) - len(b) })
		if len(most) >= 2 {
			pairs[kind] = most
		}
	}

	// Direction one: every row names the two categories the reference's arm actually passes, in order.
	for kind, want := range idxPairLookupKinds {
		got, ok := pairs[kind]
		if !ok {
			t.Errorf("idxPairLookupKinds has a row for %s, which passes no *two* lookup categories in "+
				"any `plaininstr` arm (its arms pass %v): either the kind is misspelled — in which case "+
				"`pairCategories` silently falls back to the single-category table and both indices "+
				"resolve in one space — or upstream changed the arm", kind, perKind[kind])
			continue
		}
		for i, w := range []idxCategory{want.first, want.second} {
			refName := refPairCategoryNames[w]
			if refName == "" {
				t.Errorf("refPairCategoryNames has no spelling for %s's category %d, so this control "+
					"is comparing the reference against \"\": see "+
					"TestRefPairCategoryNamesCoversEveryCategory", kind, i)
				continue
			}
			if got[i] != refName {
				t.Errorf("idxPairLookupKinds resolves %s's index %d against %q, but its reference arm "+
					"passes %q (arm: %v): a wrong category here emits a *different, valid* module, "+
					"which no suite vector reports", kind, i, refName, got[i], got)
			}
		}
	}

	// Direction two, scoped to the space rather than to the tier this section encodes: an arm the
	// reference gives two categories and the table omits. There is no exemption map, because unlike the
	// single-index table there is nothing a missing row could honestly mean — `pairCategories` falls
	// back to *one* category for both indices, which for a genuinely two-space arm is the accept-
	// direction defect itself. The `table.copy`/`memory.copy` case is not an omission: their arm passes
	// one lookup to both indices and so never appears in `pairs` at all.
	for kind := range pairs {
		if _, ok := idxPairLookupKinds[kind]; !ok {
			t.Errorf("the reference's %s arm passes two lookup categories %v and idxPairLookupKinds has "+
				"no row for it, so pairCategories falls back to idxLookupKinds' single category for "+
				"*both* indices — resolving the second index in the first's space", kind, pairs[kind])
		}
	}
}

// refPairCategoryNames is `refCategoryNames` plus the one spelling only the pair control can meet.
//
// A second map rather than an entry added to the first, and the reason is that map's own comment:
// `catFieldOfType` is **never a first category**, so `refCategoryNames` deliberately has no row for it
// and `TestFieldOfTypeIsNeverAFirstCategory` asserts the asymmetry. Adding it there to save a map would
// delete the fact that test checks.
var refPairCategoryNames = func() map[idxCategory]string {
	m := map[idxCategory]string{
		// The reference does not name a *space* here: `$3 c (field x.it)` passes the field space of the
		// type `$2` just named (`field` at parser.mly:162 is `Lib.List32.nth c.types.fields x`). So the
		// spelling this control compares against is the expression, verbatim — there is no word to use,
		// which is the whole reason the positional extractor exists.
		catFieldOfType: "(field x.it)",
	}
	for cat, name := range refCategoryNames {
		m[cat] = name
	}
	return m
}()

// TestRefPairCategoryNamesCoversEveryCategory is TestRefCategoryNamesCoversEveryCategory for the pair
// table's vocabulary, and it exists for the identical reason: a missing spelling yields `""`, which
// compares unequal to every real one, so a gap in the *control's* vocabulary would be reported as
// drift in the table under test. A control's blind spot reported as the subject's defect is worse than
// no control.
func TestRefPairCategoryNamesCoversEveryCategory(t *testing.T) {
	used := map[idxCategory]bool{}
	for _, p := range idxPairLookupKinds {
		used[p.first] = true
		used[p.second] = true
	}
	if len(used) < 5 {
		t.Fatalf("only %d distinct categories appear in idxPairLookupKinds, too few to be the real set "+
			"(6 at bdd7164 — type, table, memory, data, elem, fieldOfType, and label from br_table makes "+
			"7): the table did not load and this check is comparing nothing", len(used))
	}
	var missing []string
	for cat := range used {
		if refPairCategoryNames[cat] == "" {
			missing = append(missing, fmt.Sprintf("idxCategory(%d)", cat))
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("refPairCategoryNames has no entry for %v, so TestIdxPairLookupKindsMatchTheReference "+
			"compares the reference's spelling against \"\" and reports drift that is really this "+
			"table's gap", missing)
	}
}

// TestFieldOfTypeIsNeverAFirstCategory asserts the asymmetry `catFieldOfType`'s constant claims: it is
// a *second* index's category and never a first one.
//
// **Cited by name in instr.go before it existed** — the dangling-identifier class (#114/#115/#116),
// and the reason the citation was worth keeping rather than deleting is that the sentence it appears in
// states a real invariant three other things depend on: `refCategoryNames` has no row for the category,
// `idxSpaceFor` returns nil for it, and `retainIdxIn` therefore never has to resolve it. If some future
// arm passed `(field x.it)` first, all three would be silently wrong at once — the single-index control
// would compare against `""` and report drift in `idxLookupKinds`, which is a defect reported against
// the wrong table.
//
// **Scoped to the space, not to today's two kinds.** The subject is the *reference*, not
// `idxPairLookupKinds`: the check reads every arm's lookups from the same extractor and asserts no arm
// passes the field expression in position 0. Asserting it over this package's tables instead would be
// asking the transcription whether the transcription is right.
//
// Watched die by inverting the position test (`i == 0` to `i != 0`), which reports both `STRUCT_GET`
// and `STRUCT_SET`; and by pointing `fieldExpr` at `type_`, a category that *is* passed first, which
// reports **15** arms. The mutation's expected count was written as 49 and measured at 15 — 49 is the
// number of lookup-passing arms, and only the fifteen `catType` ones pass `type_`. Recorded rather
// than quietly corrected, because a mutation whose expected value is a guess proves the control fired
// and not that it fired *on what the sentence claims*.
func TestFieldOfTypeIsNeverAFirstCategory(t *testing.T) {
	const fieldExpr = "(field x.it)"

	// The `refPairCategoryNames` round trip first, because the constant it comes from is what this test
	// is really about: if the spelling drifted, the loop below would search for a string the extractor
	// never yields and pass by finding nothing — a green from a typo, which is this control's own
	// vacuity case.
	if refPairCategoryNames[catFieldOfType] != fieldExpr {
		t.Fatalf("refPairCategoryNames spells catFieldOfType %q and this test searches for %q: they must "+
			"agree, or the search below matches nothing and passes vacuously",
			refPairCategoryNames[catFieldOfType], fieldExpr)
	}

	seen := 0
	for kind, armCats := range positionalLookups(t) {
		for _, cats := range armCats {
			for i, cat := range cats {
				if cat != fieldExpr {
					continue
				}
				seen++
				if i == 0 {
					t.Errorf("the reference's %s arm passes %s as its **first** lookup (arm: %v), which "+
						"catFieldOfType's constant says never happens. Three things assume it: "+
						"refCategoryNames has no row for the category, idxSpaceFor returns nil for it, "+
						"and retainIdxIn never resolves it — so this arm makes the single-index control "+
						"compare against \"\" and report drift in idxLookupKinds, which is not the "+
						"table at fault", kind, fieldExpr, cats)
				}
			}
		}
	}
	// Vacuity: the invariant is about a category that must actually occur. Zero occurrences means the
	// loop asserted nothing — an extractor change, or the reference dropping `struct.get`/`struct.set`.
	if seen < 2 {
		t.Errorf("found %s in %d arm positions, want at least 2 (STRUCT_GET and STRUCT_SET at bdd7164): "+
			"a category that never appears cannot be checked for the position it never takes, so this "+
			"control asserted nothing", fieldExpr, seen)
	}
}

// TestInitReversedKindsMatchTheReference is the drift control on `initReversedKinds`, and its authority
// is `encode.ml` rather than `parser.mly` — the fact under test is *emission order*, which the grammar
// does not state at all.
//
// # Scoped to the space by extraction, which is what the map's comment promises
//
// Not "check the two rows are right": the control reads **every** `idx <var>; idx <var>` arm in
// `encode.ml` — 14 at bdd7164 — pairs each with its constructor's argument order, and requires the
// reversing set to be exactly the four the map's comment names. So an upstream arm that starts or stops
// reversing fails the board whether or not this package encodes it yet.
//
// # Four reverse and two are in the map, which is not a discrepancy
//
//	CallIndirect       (x, y) -> op 0x11;        idx y; idx x   (:275)
//	ReturnCallIndirect (x, y) -> op 0x13;        idx y; idx x   (:278)
//	TableInit          (x, y) -> op 0xfc; 0x0cl; idx y; idx x   (:294)
//	MemoryInit         (x, y) -> op 0xfc; 0x08l; idx y; idx x   (:411)
//
// The `call_indirect` pair does not route through `retainIdxPair` — a typeuse must be interned in
// stage 2, so its immediates are built by `callIndirectImm`'s patch, which reverses on its own. Two
// places knowing one fact is the #78/#105/#106 shape, and the mitigation is that **this control names
// all four and states which mechanism carries each**: a reversal removed from `callIndirectImm` fails
// here, because the arm is still in the reference and the map is still not expected to hold it.
//
// # A wrong verdict here is invisible to the suite in one direction and loud in the other
//
// `table.init 1 0` with the pair transposed emits `fc 0c 00 01` instead of `fc 0c 01 00`: a legal image
// naming segment 1 into table 0 where the text said segment 0 into table 1. §9 G-3 — it decodes clean,
// and it only reddens a vector when the two indices happen to address things whose contents differ.
// That is why the authority is read rather than the board consulted.
//
// **Watched die** five ways: adding `TABLE_COPY` to the map (fails first on the missing `refCtorNames`
// row, and once that is supplied, on the arm emitting in order); deleting `TABLE_INIT` (fails "the
// reference reverses X and neither mechanism is credited"); crediting `MemoryInit` to
// `reversedByOtherMechanism` while it is still in `initReversedKinds` (fails on disjointness); stubbing
// the arm regexp to match nothing (fails the arm floor rather than reporting agreement); and stubbing
// the *head* regexp, which fails on the unparsed-arm branch naming all fourteen arms rather than
// quietly emptying both sets.
//
// The `TABLE_INIT` deletion is worth one more sentence, because the first attempt at it **passed and
// the control was right to pass**: the mutation script's pattern matched `initSugarKinds`, which holds
// a byte-identical `"TABLE_INIT":  true,` line one screen up, so it deleted a row in a different map
// and `initReversedKinds` was untouched. Printed the diff rather than trusting the edit, which is the
// field-attribution-is-not-first-match rule pointed at a mutation instead of at a generator. A
// falsification that passes is either a stillborn control or a mutation that did not apply, and the two
// are indistinguishable without looking.
func TestInitReversedKindsMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefEncodeML)

	// One arm's head — `Constructor (a, b) ->` — and its emission pair. Split on the instruction
	// match's arm separator rather than by line, because an arm can wrap: `BrOnCast`'s spans three
	// lines at bdd7164, and a line-oriented reader is the #78/#80/#105 shape this project has paid for
	// three times. Note the head pattern excludes `->` from the argument group for exactly that reason
	// — the wrapped-arm defect `keywordgen` solved and `opgen` re-earned.
	rePair := regexp.MustCompile(`idx\s+([a-z_][a-z_0-9]*)\s*;\s*idx\s+([a-z_][a-z_0-9]*)`)
	reHead := regexp.MustCompile(`^([A-Z][A-Za-z0-9_]*)\s*\(([^->]*)\)\s*->`)

	// reference constructor name → mnemonic kind, for the arms this package's tables can speak about.
	// Absent constructors are reported by name below rather than skipped: an arm that reverses and has
	// no row here is exactly the upstream change the control is scoped to catch.
	reversing, inOrder := map[string]bool{}, map[string]bool{}
	arms := 0
	for chunk := range strings.SplitSeq(src, "\n    | ") {
		m := rePair.FindStringSubmatch(chunk)
		if m == nil {
			continue
		}
		head := reHead.FindStringSubmatch(strings.TrimSpace(chunk))
		if head == nil {
			// An `idx x; idx y` in something that is not a constructor arm. Counted as unparsed rather
			// than ignored, because a head pattern that stopped matching would otherwise silently empty
			// both sets while the arm floor stayed green.
			t.Errorf("an arm emits an index pair and its head does not parse: %q — the head pattern has "+
				"drifted, and every arm past it is invisible to this control",
				strings.TrimSpace(strings.Split(chunk, "\n")[0]))
			continue
		}
		arms++
		ctor, args := head[1], strings.Split(strings.ReplaceAll(head[2], " ", ""), ",")
		if len(args) != 2 {
			continue // `BrTable (xs, x)` emits `vec idx xs; idx x`, which is not an index *pair*
		}
		switch {
		case m[1] == args[1] && m[2] == args[0]:
			reversing[ctor] = true
		case m[1] == args[0] && m[2] == args[1]:
			inOrder[ctor] = true
		default:
			t.Errorf("%s's constructor takes (%s, %s) and it emits (%s, %s), which is neither the "+
				"written order nor its reverse: the arm now does something this control cannot classify",
				ctor, args[0], args[1], m[1], m[2])
		}
	}

	// Vacuity, per partition: arms *and* each verdict, because a reader that matched every arm and
	// classified none — or classified all of them one way — leaves the comparisons below agreeing with
	// an empty set. **14 index-pair arms at bdd7164, 4 reversing and 8 in order** (the other two being
	// `BrTable`'s vec-and-default and nothing else). Floors under those.
	if arms < 10 || len(reversing) < 3 || len(inOrder) < 5 {
		t.Fatalf("read %d index-pair arms from encode.ml, %d reversing and %d in order; want >=10, >=3 "+
			"and >=5 (14/4/8 at bdd7164). Both verdict floors are needed: an arm reader that classified "+
			"everything one way would leave the other set empty and every check below would compare "+
			"against nothing", arms, len(reversing), len(inOrder))
	}

	// The reference's constructor name for each mnemonic this package reverses. Derived from the map
	// under test rather than listed, so a kind added there arrives here as a missing name rather than
	// as a silent pass.
	for kind := range initReversedKinds {
		ctor, ok := refCtorNames[kind]
		if !ok {
			t.Errorf("initReversedKinds claims %s reverses and refCtorNames has no reference "+
				"constructor for it, so this control cannot check the row at all", kind)
			continue
		}
		if inOrder[ctor] {
			t.Errorf("initReversedKinds says %s reverses its index pair; encode.ml's %s arm emits them "+
				"in the constructor's own order. retainIdxPair would transpose a correct emission — "+
				"`table.init 1 0` becoming segment 1 into table 0, a legal image denoting a different "+
				"instruction", kind, ctor)
		}
		if !reversing[ctor] {
			t.Errorf("initReversedKinds claims %s reverses and encode.ml's %s arm does not appear in "+
				"the reversing set at all (reversing: %v)", kind, ctor, slices.Sorted(maps.Keys(reversing)))
		}
	}

	// The direction scoped to the space: every arm the *reference* reverses is accounted for by one of
	// the two mechanisms. A reversal credited to neither is the case where this package emits the pair
	// in the wrong order and nothing says so.
	credited := map[string]bool{}
	for kind := range initReversedKinds {
		credited[refCtorNames[kind]] = true
	}
	for ctor := range reversedByOtherMechanism {
		if credited[ctor] {
			t.Errorf("%s is credited to both initReversedKinds and reversedByOtherMechanism. The two "+
				"sets must be disjoint: two places knowing one fact is the #78/#105/#106 shape, and a "+
				"kind in both means the reversal is applied twice or the note is stale", ctor)
			continue
		}
		credited[ctor] = true
	}
	for ctor := range reversing {
		if !credited[ctor] {
			t.Errorf("encode.ml's %s arm emits its index pair reversed and neither mechanism is "+
				"credited with it: add a row to initReversedKinds if retainIdxPair encodes the "+
				"mnemonic, or name it in reversedByOtherMechanism with the function that reverses it. "+
				"An uncredited reversal means the pair is emitted in written order — a legal image "+
				"denoting a different instruction (§9 G-3)", ctor)
		}
	}
	// And the mirror: a name in either set that the reference no longer reverses. Without this, an
	// upstream arm that *stopped* reversing would leave a stale row applying a transposition the
	// reference does not.
	for ctor := range reversedByOtherMechanism {
		if !reversing[ctor] {
			t.Errorf("reversedByOtherMechanism names %s and encode.ml's arm for it no longer reverses "+
				"(in order: %v): the mechanism it cites is now transposing a correct pair",
				ctor, slices.Sorted(maps.Keys(inOrder)))
		}
	}
}

// refCtorNames maps this package's mnemonic kind to `encode.ml`'s constructor name — the reference's
// own vocabulary for the arm, so the control above reads it rather than translating twice.
//
// Only the kinds `initReversedKinds` can hold: the map is a *vocabulary* for that set, and a row for a
// mnemonic nothing asks about would be an unused claim. The control reports a missing name rather than
// defaulting, per refCategoryNames' argument one table over.
var refCtorNames = map[keywordKind]string{
	"TABLE_INIT":  "TableInit",
	"MEMORY_INIT": "MemoryInit",
}

// reversedByOtherMechanism are the reference's reversing arms that `retainIdxPair` does not encode,
// each carrying the function that reverses them instead.
//
// This is the declared-and-tracked shape (#6) applied to a *set difference*: the reference reverses
// four arms and `initReversedKinds` holds two, and the honest way to state that is a named exemption
// rather than a control scoped to the two. `callIndirectImm` (parser.go) reverses both of these itself,
// because a typeuse must be interned in stage 2 and a patch cannot be half-built — see
// `initReversedKinds`' comment for why routing them through the cursor was declined.
var reversedByOtherMechanism = map[string]string{
	"CallIndirect":       "callIndirectImm, which writes the type index then the table index",
	"ReturnCallIndirect": "callIndirectImm, shared with call_indirect",
}
