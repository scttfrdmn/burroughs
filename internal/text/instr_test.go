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
