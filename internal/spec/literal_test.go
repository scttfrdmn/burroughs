package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestHarnessAndEngineLiteralReadersAgree is the second opinion value.go's header promises.
//
// # Why two readers exist at all
//
// 13670 of the 13671 answerable `assert_return` vectors get their module from wat source, so
// `internal/text`'s literal reader produced the module's own constants — and 1111 of them invoke
// a function whose body contains a `const`. For those, reading the *expected* value with the
// same code would supply both sides of the comparison: a conversion bug shifts the two together
// and the vector passes by construction. That is grave #106's shape — a premise measured with
// the subject's own instrument is an echo — and it lands hardest on `const.wast` and
// `float_literals.wast`, whose entire purpose is literal conversion.
//
// So `readConst` is derived independently from fxx.ml/ixx.ml. This test is what keeps the
// duplication a second opinion rather than a silent drift.
//
// # It compares on the suite's own spellings, and that is the whole design
//
// The tempting version of this test renders a `Val` back to a lexeme and checks the round trip.
// That version is nearly worthless: it exercises whatever spellings the renderer emits, which is
// one canonical form per value, and the corpus's interesting spellings — `0xa0ff.f141a59a` with
// no binary exponent, `nan:0x200000`, `1_000_000`, `+0x1p-149` — are precisely the ones a
// renderer never produces. So the lexemes are read out of the `.wast` files verbatim, and each
// one is compiled into a one-instruction module whose return value *is* the engine's conversion
// of that exact text.
//
// The comparison also goes through the engine's real path rather than a helper.
// `text.intConstBits` and `text.floatConstBits` are unexported, and exporting them for a test
// would make this control read the helper while nothing checks the path — the two are the same
// only until a caller changes.
//
// # Vacuity
//
// A comparison over an empty set agrees perfectly, and the degenerate case is easy to reach: an
// extractor that finds nothing produces a green board asserting nothing. So the corpus is floored
// **per kind** — an empty f32 half absorbed by a full i32 half is the vacuity defect with a
// partner to hide behind (grave #105) — and the exact counts are logged beside the floors.
func TestHarnessAndEngineLiteralReadersAgree(t *testing.T) {
	requireSuite(t)

	// Every distinct `(TYPE.const LEXEME)` spelling in the corpus, keyed by kind *and* text:
	// `f32.const 1` and `f64.const 1` are different conversions of one spelling, and a
	// text-keyed set would test only whichever was seen first.
	type lit struct {
		kind ValKind
		text string
	}
	seen := map[lit]bool{}
	for _, f := range boardFiles(t) {
		nodes := parseFileNodes(t, f)
		var walk func(n node)
		walk = func(n node) {
			if k, txt, ok := constSpelling(n); ok {
				seen[lit{k, txt}] = true
			}
			for _, c := range n.list {
				walk(c)
			}
		}
		for _, n := range nodes {
			walk(n)
		}
	}

	// Per-kind floors, **calibrated against the printed counts rather than guessed**.
	//
	// The first draft wrote 50 for every kind. Measured, the populations are i32 2531, i64
	// 1081, f32 1335, f64 1551 — so 50 sat 95% below the smallest of them, which is the
	// unasserted-distance defect (#87) in a bound written after the rule against it: a floor
	// that far away cannot catch anything smaller than the gap, and the extractor could lose
	// nine tenths of a kind and stay green. The calibrated siblings are the pattern to copy —
	// `totalFloor` 2000 against 2143, `filesFloor` 230 against 242, both ~5-7% under.
	//
	// Floors rather than equalities because the corpus is not SHA-pinned (#42), so upstream
	// adding vectors moves these with no local change; the margin is corpus drift, not slack
	// for our own regressions. A *fall* is the extractor having stopped reaching a population,
	// which is the silent case this exists for. Per kind and never one total, because an empty
	// f32 half absorbed by a full i32 half is the vacuity defect with a partner to hide behind
	// (grave #105).
	// The four calls are **unrolled rather than looped, and the control is why**.
	// TestEveryBoardBoundIsChecked matches a `*Floor` constant against the *string literal* in
	// its boardBound call's name argument, so the looped first draft — passing
	// `fmt.Sprintf("literalSpellings[%s]", k)` — declared four bounds that the door could not
	// see, and it said so on the first run. A trigger keyed on a literal is the right trade
	// (an interpolated name could be anything at run time), so the call sites conform to the
	// mechanism instead of the mechanism widening to admit them.
	const (
		i32SpellingFloor = 2400 // measured 2531
		i64SpellingFloor = 1000 // measured 1081
		f32SpellingFloor = 1250 // measured 1335
		f64SpellingFloor = 1450 // measured 1551
	)
	byKind := map[ValKind]int{}
	for l := range seen {
		byKind[l.kind]++
	}
	const whyKind = "the spelling extractor stopped reaching this kind, so its half of the " +
		"cross-check is comparing an empty set and agreeing perfectly"
	boardBound(t, "i32SpellingFloor", byKind[KindI32], i32SpellingFloor, 0, vacuityBound, whyKind)
	boardBound(t, "i64SpellingFloor", byKind[KindI64], i64SpellingFloor, 0, vacuityBound, whyKind)
	boardBound(t, "f32SpellingFloor", byKind[KindF32], f32SpellingFloor, 0, vacuityBound, whyKind)
	boardBound(t, "f64SpellingFloor", byKind[KindF64], f64SpellingFloor, 0, vacuityBound, whyKind)
	for _, k := range []ValKind{KindI32, KindI64, KindF32, KindF64} {
		t.Logf("  %s: %d distinct spellings", k, byKind[k])
	}

	// The engine's side, one module per spelling: `(func (export "c") (result T) (T.const L))`
	// — the shortest program whose return value is the conversion and nothing else.
	var agreed, disagreed, uncovered, rejectedByBoth int
	for l := range seen {
		want, harnessOK := readConst(constNode(t, l.kind, l.text))
		src := fmt.Sprintf(`(module (func (export "c") (result %s) (%s.const %s)))`,
			l.kind, l.kind, l.text)
		img, encErr := text.EncodeModule([]byte(src))
		if encErr != nil {
			// **The two readers disagreeing about *legality* is a disagreement.** A spelling
			// the harness converts and the engine rejects is an over-rejection, the class no
			// reject-direction corpus can falsify (decision 0007) — so it is an error, not a
			// skip. The encoder's instruction frontier (#8) cannot reach here: the module is
			// one const, which the encoder emits.
			if harnessOK {
				disagreed++
				t.Errorf("%s.const %s: the harness reads it as %s, the engine rejects it: %v\n"+
					"\tone of the two readers is wrong about the reference's grammar",
					l.kind, l.text, want, encErr)
				continue
			}
			rejectedByBoth++
			continue
		}
		if !harnessOK {
			disagreed++
			t.Errorf("%s.const %s: the engine accepts it, the harness cannot read it\n"+
				"\treadConst is under-reading the constant grammar, which on a real vector "+
				"becomes an unsupported command rather than a verdict", l.kind, l.text)
			continue
		}
		m, err := binary.DecodeModule(img)
		if err != nil {
			t.Errorf("%s.const %s: the engine's own output did not decode: %v", l.kind, l.text, err)
			continue
		}
		in, trap := interp.Instantiate(m)
		if trap != nil {
			// These modules are a single const and an END with no memory, so a trap is a
			// finding about instantiation rather than about the literal. Counted as
			// uncovered with the reason quoted, never silently skipped.
			uncovered++
			t.Logf("  not covered (instantiation trapped): %s.const %s: %v", l.kind, l.text, trap)
			continue
		}
		got, err := invoke(in, "c", nil)
		if err != nil {
			// The interpreter's frontier. Logged rather than skipped silently: an uncovered
			// spelling that looks covered is this control's own failure mode.
			uncovered++
			t.Logf("  not covered (interpreter): %s.const %s: %v", l.kind, l.text, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("%s.const %s: engine returned %d values, want 1", l.kind, l.text, len(got))
			continue
		}
		// **Bits, not Matches.** Matches admits the NaN classes, and admitting them here would
		// let a genuine payload disagreement pass as a class agreement — the matcher's
		// tolerance is right for a vector's expectation and wrong for a reader cross-check.
		if got[0].Kind != want.Kind || got[0].Bits != want.Bits {
			disagreed++
			t.Errorf("%s.const %s: harness reads %s, engine computes %s\n"+
				"\tthe two literal readers disagree, so one is wrong about the reference's "+
				"derivation — and on a real vector this bug would be invisible, because the "+
				"engine's reader also produced the module under test", l.kind, l.text, want, got[0])
			continue
		}
		agreed++
	}
	t.Logf("%d distinct spellings: %d agreed, %d disagreed, %d rejected by both, "+
		"%d not covered (interpreter)",
		len(seen), agreed, disagreed, rejectedByBoth, uncovered)

	// The vacuity check on the *comparison* rather than on the corpus. Every count above can
	// be nonzero while `agreed` is zero — if every spelling landed in `uncovered`, this test
	// would report a clean board having compared nothing. That is the empty-set agreement one
	// level in from the per-kind floors, so it gets its own bound.
	//
	// **6200 against a measured 6498, and the measurement is what makes it worth having.** The
	// first draft said 500, which every one of the four per-kind floors already implies — a
	// bound entailed by its neighbours asserts nothing new. The interpreter answers *every*
	// spelling in the corpus today (0 uncovered), so this is effectively an equality with
	// corpus-drift margin, and a fall means the engine stopped executing constants it used to.
	const agreementFloor = 6200 // measured 6498
	boardBound(t, "agreementFloor", agreed, agreementFloor, 0, vacuityBound,
		"the cross-check compared almost nothing: the spellings were found but the engine "+
			"could not answer for them, so the agreement is over an empty set")
}

// constSpelling reports whether a node is a `(TYPE.const LEXEME)` form, and its parts.
//
// Scoped to the **whole file** rather than to the assert_return commands the classifier admits,
// which is deliberate and is the scope-controls-to-the-space rule: the vectors readConst is asked
// about today are a sample, and a cross-check over that sample would freeze at the moment of
// authorship. Module bodies contribute their constants too, which is where the interesting
// float spellings live.
func constSpelling(n node) (ValKind, string, bool) {
	if !n.isList() || len(n.list) != 2 || n.list[1].isList() || n.list[1].isS {
		return 0, "", false
	}
	var k ValKind
	switch n.head() {
	case "i32.const":
		k = KindI32
	case "i64.const":
		k = KindI64
	case "f32.const":
		k = KindF32
	case "f64.const":
		k = KindF64
	default:
		return 0, "", false
	}
	// The NaN classes are `assert_return`-only predicates with no bit pattern for the engine to
	// agree with, so they are out of this control's scope. That asymmetry is readFloatLit's and
	// is asserted where it lives.
	if txt := n.list[1].atom; txt != "nan:canonical" && txt != "nan:arithmetic" {
		return k, txt, true
	}
	return 0, "", false
}

// constNode builds the node readConst expects, so the harness side is exercised through its real
// entry point rather than through readIntLit/readFloatLit directly.
func constNode(t *testing.T, k ValKind, text string) node {
	t.Helper()
	n, err := newParser([]byte(fmt.Sprintf("(%s.const %s)", k, text))).parseNode()
	if err != nil {
		t.Fatalf("(%s.const %s) does not re-parse: %v", k, text, err)
	}
	return n
}

// parseFileNodes reads a .wast file as s-expressions, bypassing classify.
func parseFileNodes(t *testing.T, name string) []node {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(suiteDir, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	nodes, err := newParser(src).parseAll()
	if err != nil {
		t.Fatalf("%s: parse: %v", name, err)
	}
	return nodes
}
