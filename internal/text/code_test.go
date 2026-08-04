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
