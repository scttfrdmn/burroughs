package text

import (
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// reProductionHead matches a menhir production's header line: a nonterminal at column 0
// followed by ` :`. Used to bound one production's arms without relying on how many blank
// lines happen to separate it from the next — `plaininstr` is followed by *two* at bdd7164
// and a single-blank-line bound would have read into `laneidx`, which is the unbounded-search
// defect utf8position_test.go's letBody was written to avoid.
var reProductionHead = regexp.MustCompile(`(?m)^[a-z_][a-z_0-9]* :$`)

// reMenhirComment strips `/* Sugar */` and its siblings from an arm's head.
var reMenhirComment = regexp.MustCompile(`/\*.*?\*/`)

// productionArms returns one menhir production's arms with semantic actions and comments
// stripped, bounded at the next production header.
//
// The bound is the point, per letBody's grave: a search for arms "after this header" finds
// the *next* production's arms too, and a table check that reads extra arms reports drift
// that is really the reader's.
func productionArms(t *testing.T, src, nonterminal string) []string {
	t.Helper()
	head := "\n" + nonterminal + " :\n"
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("parser.mly no longer defines the `%s` production; every comparison below "+
			"would be over an empty set, which agrees with anything", nonterminal)
	}
	rest := src[i+len(head):]
	if loc := reProductionHead.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}

	var arms []string
	for chunk := range strings.SplitSeq(rest, "\n  | ") {
		// Strip semantic actions, which nest.
		var depth int
		var head strings.Builder
		for _, ch := range chunk {
			switch {
			case ch == '{':
				depth++
			case ch == '}':
				depth--
			case depth == 0:
				head.WriteRune(ch)
			}
		}
		text := reMenhirComment.ReplaceAllString(head.String(), "")
		text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
		text = strings.TrimSpace(strings.TrimPrefix(text, "| "))
		if text == "" {
			continue
		}
		arms = append(arms, strings.Join(strings.Fields(text), " "))
	}
	return arms
}

// plaininstrArms re-extracts `plaininstr`'s arms from the reference: kind → the immediate
// sequences written after it.
//
// This is the *authority* half of the drift control, and it shares no code with
// `plaininstrShapes` — an extractor derived from the thing it checks agrees by construction,
// which is a control comparing a value to itself.
func plaininstrArms(t *testing.T) map[keywordKind][]string {
	t.Helper()
	src := testenv.RequireSpecRef(t, refPath(testenv.RefParserMLY))

	arms := map[keywordKind][]string{}
	total := 0
	for _, arm := range productionArms(t, src, "plaininstr") {
		fields := strings.Fields(arm)
		kind := keywordKind(fields[0])
		if kind != keywordKind(strings.ToUpper(string(kind))) {
			// A lowercase leader is a nonterminal, not a token: not a mnemonic arm. None exist
			// in `plaininstr` at bdd7164; the branch is here so one appearing upstream is
			// skipped rather than recorded as a bogus kind, and the arm floor below still
			// notices if many of them appear.
			continue
		}
		arms[kind] = append(arms[kind], strings.Join(fields[1:], " "))
		total++
	}

	// Vacuity, on both counts: 83 arms over 81 kinds at bdd7164, the two-arm kinds being why
	// those numbers differ. Floors rather than equalities, because upstream adding an
	// instruction must not fail this test — but *a comparison against an empty set succeeds*,
	// so without a plausible-size floor a reader that silently stopped working would make
	// every assertion below pass by asking nothing.
	//
	// Both figures are the extractor's own printed output, not a hand count. The first draft
	// of this comment said 82 kinds and the two `checked` floors below said 80 and 82; all
	// three were read off a hand tally and all three were wrong, in the direction that makes
	// a floor harder to satisfy rather than easier. Printed: 81, 79, 81.
	if total < 70 || len(arms) < 70 {
		t.Fatalf("extracted %d plaininstr arms over %d kinds, want >=70 of each (83/81 at "+
			"bdd7164); the extractor is not reading the production and the drift checks below "+
			"would agree with nothing", total, len(arms))
	}
	return arms
}

// wantShapes maps the reference's written immediate sequence to the shape this package names
// for it. The one fact stated by hand; every kind→shape row is checked against it.
var wantShapes = map[string]immShape{
	"":                             immNone,
	"idx":                          immIdx,
	"idx idx":                      immIdxIdx,
	"idx_opt":                      immIdxOpt,
	"idx_opt offset_opt align_opt": immMemarg,
	"idx_idx_opt":                  immIdxIdxOpt,
	"lane_imms":                    immLaneImms,
	"laneidx":                      immLaneIdx,
	"reftype":                      immReftype,
	"idx idx_list":                 immIdxIdxList,
	"idx reftype reftype":          immIdxReftype2,
	"heaptype":                     immHeaptype,
	"idx nat32":                    immIdxNat32,
	"num":                          immNum,
	"VECSHAPE list(num)":           immVecConst,
	"list(laneidx)":                immLaneIdxList,
}

// TestPlaininstrShapesMatchTheReference is the drift control on the hand-written kind→shape
// table: every kind the reference gives a `plaininstr` arm has a row, every row corresponds to
// a real arm, and the shapes agree.
//
// Scoped to the space rather than to today's sample: it iterates the *reference's* arms, so an
// instruction added upstream arrives as a missing row instead of staying invisible until
// something reaches it. The inverse direction is checked too — a row for a kind the reference
// gives no arm is a row matching nothing, the unreachable-branch shape wearing a map entry.
func TestPlaininstrShapesMatchTheReference(t *testing.T) {
	arms := plaininstrArms(t)

	checked := 0
	for kind, seqs := range arms {
		if initSugarKinds[kind] {
			continue // the two-arm kinds, asserted by name below rather than by lookup
		}
		if len(seqs) != 1 {
			t.Errorf("the reference gives %s %d plaininstr arms (%q) and initSugarKinds does not "+
				"name it; a kind with two immediate shapes cannot be one map row, and a second "+
				"arm nothing knows about is an accepted form the parser rejects — which no "+
				"assert_malformed vector can catch", kind, len(seqs), seqs)
			continue
		}
		got, ok := plaininstrShapes[kind]
		if !ok {
			t.Errorf("the reference gives %s a plaininstr arm (immediates %q) and "+
				"plaininstrShapes has no row for it; an instruction the table does not know is "+
				"one the parser cannot reach", kind, seqs[0])
			continue
		}
		want, ok := wantShapes[seqs[0]]
		if !ok {
			t.Errorf("%s's immediate sequence %q is not a shape this package names; a new "+
				"immediate form upstream needs an immShape and a reader, not a silent mismatch",
				kind, seqs[0])
			continue
		}
		if got != want {
			t.Errorf("%s: plaininstrShapes says shape %d, the reference writes %q (shape %d)",
				kind, got, seqs[0], want)
		}
		checked++
	}
	if checked < 70 {
		t.Errorf("only %d kinds compared against the reference, want >=70 (79 at bdd7164); the "+
			"loop above is agreeing about almost nothing", checked)
	}

	for kind := range plaininstrShapes {
		if _, ok := arms[kind]; !ok {
			t.Errorf("plaininstrShapes has a row for %s, which the reference gives no plaininstr "+
				"arm; a row matching nothing is the unreachable-branch shape wearing a map entry",
				kind)
		}
	}

	// initSugarKinds is a claim about the reference, so it is checked rather than trusted:
	// exactly these kinds get two arms, one `idx idx` and one `idx`.
	for kind := range initSugarKinds {
		seqs, ok := arms[kind]
		if !ok {
			t.Errorf("initSugarKinds names %s, which the reference gives no plaininstr arm", kind)
			continue
		}
		if len(seqs) != 2 {
			t.Errorf("initSugarKinds names %s as having an optional first index, but the "+
				"reference gives it %d arm(s) (%q); if the sugar arm went away upstream this "+
				"kind belongs in plaininstrShapes instead", kind, len(seqs), seqs)
			continue
		}
		set := map[string]bool{seqs[0]: true, seqs[1]: true}
		if !set["idx idx"] || !set["idx"] {
			t.Errorf("%s's two arms are %q, want one `idx idx` and one `idx`", kind, seqs)
		}
	}
}

// TestEveryPlaininstrKindIsInTheKeywordTable closes the loop between the two derived tables:
// every kind the shape table dispatches on must be a kind some mnemonic actually lexes to.
//
// Without it a row could name a kind no lexeme produces — a branch reachable by nothing, and
// invisible on a reject-only surface, where an unreachable arm still yields *an* error and
// only the wrong one.
func TestEveryPlaininstrKindIsInTheKeywordTable(t *testing.T) {
	if len(keywords) < 500 {
		t.Fatalf("keyword table holds %d entries, want >=500 (589 at bdd7164); the sweep below "+
			"would be over an empty set", len(keywords))
	}
	inTable := map[keywordKind]bool{}
	for _, kind := range keywords {
		inTable[kind] = true
	}

	checked := 0
	for kind := range plaininstrShapes {
		checked++
		if !inTable[kind] {
			t.Errorf("plaininstrShapes dispatches on %s, which no keyword in the generated table "+
				"lexes to; the row is unreachable", kind)
		}
	}
	for kind := range initSugarKinds {
		checked++
		if !inTable[kind] {
			t.Errorf("initSugarKinds names %s, which no keyword lexes to", kind)
		}
	}
	if checked < 70 {
		t.Errorf("only %d kinds checked against the keyword table, want >=70 (81 at bdd7164); "+
			"the tables shrank and this control is agreeing about almost nothing", checked)
	}
}

// TestLaneImmediateFaultOrdering pins the grave in #63's laneIdxList: for `i8x16.shuffle`, three
// different checks can fire on one index list, and which one wins is a property of *where the
// reference puts each check* rather than of which is cheapest to run.
//
// **The count is outside the other two, and the other two are not ordered against each other.**
// A per-index range error comes from `nat8` reducing inside the grammar and a syntax error comes
// from the automaton refusing to reduce on an illegal lookahead — both raised at a token position
// during a left-to-right scan, so between those two the *leftmost fault* wins. The count is the
// semantic action at parser.mly:653 and runs only once the list has reduced. The first draft of
// laneIdxList had the range check and the count but no follower arm, so every illegal follower was
// reported as a count error — right verdict, wrong message, on six vectors.
//
// This test's own first draft was named `…IsThreeDeep` and asserted a three-way precedence. That
// was wrong, and it is worth recording *how* it was wrong, because the error was invisible to the
// suite by construction: every cited vector below has exactly one fault, so nothing in the corpus
// can distinguish a precedence from a scan order. The two-fault rows are what separate them, and
// they had to be synthesised — see `range before syntax, by position` and its mirror.
//
// Written as a table over one shared prefix so the cases differ only in the faulting token and its
// position. Each row cites the vector that pins it, or says why it is synthetic.
//
// Falsified by running each defect, not by reasoning about it:
//   - Deleting the IntTok/FloatTok arm turns the four follower rows from `unexpected token` into
//     `wrong number of lane indices` — the grave, reproduced.
//   - Hoisting that arm above the loop changes *nothing*, which is how the three-deep claim died:
//     a real precedence would have been visible here. The two position rows are what fail if the
//     scan is replaced by a check that prefers one fault kind over the other.
//   - Moving the count check above the loop turns every single-fault row into a count error.
func TestLaneImmediateFaultOrdering(t *testing.T) {
	const (
		// Fifteen legal indices. Every case below appends its own sixteenth-position token, so
		// the count is one short until a row supplies a legal one.
		fifteen = "0 1 2 3 4 5 6 7 8 9 10 11 12 13 14"
		sixteen = fifteen + " 15"
	)
	cases := []struct {
		name string
		imms string
		want string
		why  string // the vector, or the reason it is synthetic
	}{
		// The count, when nothing else is wrong: seventeen legal indices reduce, the follower is
		// the closing paren, and only then does the action count them.
		{"seventeen legal", sixteen + " 16", "wrong number of lane indices", "simd_lane.wast:519"},
		{"fifteen legal", fifteen, "wrong number of lane indices", "simd_lane.wast:516"},

		// A range error, from inside the grammar. Sixteen tokens, the last out of nat8's range —
		// so the count is *right* and the earlier check still wins.
		{"sixteen with a bad index", fifteen + " 256", "i8 constant out of range", "simd_lane.wast:526"},

		// The follower, which is the layer the first draft was missing. Each of these leaves the
		// list at fifteen, so a count-first reader reports the count and is wrong.
		{"int follower", fifteen + " -1", "unexpected token", "simd_lane.wast:522"},
		{"float follower", fifteen + " 15.0", "unexpected token", "simd_lane.wast:604"},
		{"leading float", "0.5 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15", "unexpected token", "simd_lane.wast:608"},
		{"neg inf follower", "-inf " + fifteen, "unexpected token", "simd_lane.wast:612"},
		{"inf follower", fifteen + " inf", "unexpected token", "simd_lane.wast:616"},

		// Synthetic, and each names a reader the cited rows above cannot distinguish from the
		// real one. The suite never writes two faults into one index list, so every claim about
		// how two faults *interact* is unsampled — which is exactly where the three-deep error
		// lived.
		//
		// `short with a bad index`: fourteen legal, then an out-of-range one, then nothing. Both
		// the range check and the count are unsatisfied, and the range check wins because it is
		// reached during the scan. Fails if the count check is hoisted above the loop.
		{
			"short with a bad index", "0 1 2 3 4 5 6 7 8 9 10 11 12 13 256", "i8 constant out of range",
			"synthetic: pins range-before-count when both are violated, which no vector does",
		},
		// The pair that killed the precedence claim. Same two faults, opposite order in the
		// source, opposite verdicts — so range and syntax are ordered by *position*, and neither
		// kind outranks the other. A reader that preferred one kind would fail exactly one of
		// these two rows whichever kind it preferred, which is why they are written as a pair
		// rather than as one case.
		{
			"range before syntax, by position", "0 1 256 4 -1 6 7 8 9 10 11 12 13 14 15 16",
			"i8 constant out of range",
			"synthetic: bad nat at index 2, illegal follower at 4 — leftmost wins",
		},
		{
			"syntax before range, by position", "0 1 -1 4 256 6 7 8 9 10 11 12 13 14 15 16",
			"unexpected token",
			"synthetic: the mirror of the row above, and the two together are the only evidence " +
				"that this is a scan order and not a precedence",
		},
		// The all-legal case, which is the vacuity guard: if the sixteen-index form did not
		// parse, every row above would pass for the wrong reason and the table would be
		// asserting that shuffle is unreadable rather than that its errors are ordered.
		{
			"sixteen legal is accepted", sixteen, "",
			"synthetic: vacuity — an accept-direction row, which no assert_malformed can carry",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "(module (func (result v128) (i8x16.shuffle " + c.imms + ")))"
			err := ReadModule([]byte(src))
			switch {
			case c.want == "" && err != nil:
				t.Errorf("i8x16.shuffle with %s: %v; want accepted (%s)", c.imms, err, c.why)
			case c.want == "":
				return
			case err == nil:
				t.Errorf("i8x16.shuffle with %s: accepted; want %q (%s)", c.imms, c.want, c.why)
			case !strings.Contains(err.Error(), c.want):
				// The message is the finding, not the verdict: every row here is a module both
				// readers reject, so a wrong message is invisible on the board and this is the
				// only thing that looks at it.
				t.Errorf("i8x16.shuffle with %s: got %q, want it to contain %q (%s)",
					c.imms, err, c.want, c.why)
			}
		})
	}
}

// TestVecConstFollowerPrecedesTheCount is the sweep's dividend, and it is entirely synthetic.
//
// laneIdxList's grave was "the count preempted a syntax error". Sweeping for siblings of that
// shape found vecConst holding it too — `v128.const i8x16 0 … 14 $x` reported `wrong number of
// lane literals`, where the reference cannot reduce `VECSHAPE list(num)` with a VAR in the
// lookahead and so never reaches `vec`'s length test at all.
//
// **No vector in the suite covers this**, which is why the sweep found it and the board did not.
// `simd_const.wast` writes wrong-length lists and out-of-range literals but never an illegal
// follower after a short list, so the board reads green on both readings. Marked synthetic per the
// provenance rule; the premise is the `num` production (parser.mly:476-478, NAT | INT | FLOAT),
// which is machine-checkable and is what makes VAR an illegal follower rather than a guess.
//
// Falsified by deleting the VarTok arm from vecConst: `short list then a var` reverts to `wrong
// number of lane literals`. Run, not assumed.
func TestVecConstFollowerPrecedesTheCount(t *testing.T) {
	const fifteen = "0 1 2 3 4 5 6 7 8 9 10 11 12 13 14"
	cases := []struct {
		name, imms, want, why string
	}{
		// The sibling defect. Fifteen literals where sixteen are wanted *and* an illegal
		// follower: the follower wins, because the production cannot reduce.
		{
			"short list then a var", "i8x16 " + fifteen + " $x", "unexpected token",
			"synthetic: a VAR is not a `num` (parser.mly:476-478), so the automaton errors " +
				"before `vec` runs; the suite never writes an illegal follower after a short list",
		},
		// The count still wins when the list is merely the wrong length, which is the half the
		// suite does cover — kept here so the fix cannot be "always report the follower".
		{
			"short list, legal follower", "i8x16 " + fifteen, "wrong number of lane literals",
			"simd_const.wast:130",
		},
		// And the count still beats the range check, which is the ordering the header derives
		// from `of_strings` and the one an over-eager fix would invert.
		{
			"wrong length and out of range", "i32x4 0x10000000000000000 0x10000000000000000",
			"wrong number of lane literals", "simd_const.wast:480",
		},
		// Vacuity: if the legal form did not parse, every row above could pass while saying
		// nothing about ordering.
		{
			"sixteen legal is accepted", "i8x16 " + fifteen + " 15", "",
			"synthetic: vacuity — an accept-direction row, which no assert_malformed can carry",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "(module (func (result v128) (v128.const " + c.imms + ")))"
			err := ReadModule([]byte(src))
			switch {
			case c.want == "" && err != nil:
				t.Errorf("v128.const %s: %v; want accepted (%s)", c.imms, err, c.why)
			case c.want == "":
				return
			case err == nil:
				t.Errorf("v128.const %s: accepted; want %q (%s)", c.imms, c.want, c.why)
			case !strings.Contains(err.Error(), c.want):
				t.Errorf("v128.const %s: got %q, want it to contain %q (%s)",
					c.imms, err, c.want, c.why)
			}
		})
	}
}
