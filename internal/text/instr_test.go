package text

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen/opgen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// reProductionHead matches a menhir production's header line: a nonterminal at column 0
// followed by ` :`, and optionally a trailing comment. Used to bound one production's arms
// without relying on how many blank lines happen to separate it from the next — `plaininstr` is
// followed by *two* at bdd7164 and a single-blank-line bound would have read into `laneidx`,
// which is the unbounded-search defect utf8position_test.go's letBody was written to avoid.
//
// **The trailing-comment alternative is a fix, not decoration.** This was `^[a-z_][a-z_0-9]* :$`,
// which misses all six headers the reference annotates — `expr`, `expr1`, `func_fields_import`,
// `func_fields_import_result`, `inline_module`, `inline_module1`, each written
// `name :  /* Sugar */`. Two consequences, and the second is the one worth naming: a *lookup* of a
// commented production found nothing, and a *bound* on the production before one ran straight
// through it. The existing caller was unaffected only by luck — `plaininstr` happens to be followed
// by `laneidx :`, uncommented — so the reader has been carrying a hole that no control could see
// until `expr1` became the first caller to land on it. Found by the vacuity floor in
// TestExpr1LeadersMatchTheReference firing on its first run, which is the floor doing precisely the
// job *a comparison against an empty set succeeds* describes: without it the new control would have
// extracted zero arms and agreed with everything.
var reProductionHead = regexp.MustCompile(`(?m)^[a-z_][a-z_0-9]* :(\s*/\*.*\*/)?$`)

// reMenhirComment strips `/* Sugar */` and its siblings from an arm's head.
var reMenhirComment = regexp.MustCompile(`/\*.*?\*/`)

// productionBody returns one menhir production's text, bounded at the next production header —
// arms *and* semantic actions, unstripped.
//
// Carved out of productionArms because a control on the reference's **lookup categories** needs the
// actions: the category is not in the grammar at all, it is the argument the action passes
// (`$2 c label` against `$2 c func`), so a reader that strips actions cannot see it. Two callers,
// one bound, and the bound is the part worth sharing — see productionArms for the grave.
func productionBody(t *testing.T, src, nonterminal string) string {
	t.Helper()
	// The header may carry a trailing comment, so it is matched rather than string-searched — see
	// reProductionHead for the hole this closed.
	reHead := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(nonterminal) + ` :(\s*/\*.*\*/)?$`)
	loc := reHead.FindStringIndex(src)
	if loc == nil {
		t.Fatalf("parser.mly no longer defines the `%s` production; every comparison below "+
			"would be over an empty set, which agrees with anything", nonterminal)
	}
	rest := src[loc[1]:]
	if next := reProductionHead.FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return rest
}

// productionArms returns one menhir production's arms with semantic actions and comments
// stripped, bounded at the next production header.
//
// The bound is the point, per letBody's grave: a search for arms "after this header" finds
// the *next* production's arms too, and a table check that reads extra arms reports drift
// that is really the reader's.
func productionArms(t *testing.T, src, nonterminal string) []string {
	t.Helper()
	rest := productionBody(t, src, nonterminal)

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

// plaininstrAuthority is one pin's text parser: the path it is cited by, its contents, and the name
// *that revision* gives the flat-instruction production.
type plaininstrAuthority struct {
	path       string
	src        string
	production string
}

// plaininstrSpellings are the names upstream has given the flat-instruction production, and the
// second one is a finding rather than defensive breadth: the core pin writes `plaininstr` (:557) and
// the threads pin writes `plain_instr` (:386). Upstream renamed it after cc535ad.
//
// **A nonterminal name is part of a citation, and this is grave #529 one grammar over.** That grave
// was a row citing a bare `lexer.mll:266`, which resolves against whichever of two same-named files
// the reader opens; this is the same ambiguity inside a file — a control asking for `plaininstr`
// against the threads parser asks for a production that does not exist there, and every arm-derived
// assertion is then made against an empty set. It was caught by `productionBody`'s own fatal, which
// is that fatal earning its place: the alternative to a hard failure on a missing production is
// exactly the *comparison against nothing* every floor in this file is written against.
var plaininstrSpellings = []string{"plaininstr", "plain_instr"}

// plaininstrProduction resolves which spelling one authority uses, and fails on zero or two.
//
// Exactly one, both directions. Zero means the reader has lost the production — the empty-set case
// above. **Two** means the revision defines both, at which point picking either is a first-match
// guess: *a first-match pick declines to ask*, and the two would be different grammars for one
// question. Neither branch has a corpus witness, so both are asserted.
func plaininstrProduction(t *testing.T, path, src string) string {
	t.Helper()
	var found []string
	for _, name := range plaininstrSpellings {
		if regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` :`).MatchString(src) {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s defines %d of the flat-instruction production's known spellings %v (found %v); "+
			"a nonterminal name is part of a citation, and reading arms from the wrong one — or from "+
			"none — makes every assertion below a comparison against an incomplete grammar",
			path, len(found), plaininstrSpellings, found)
	}
	return found[0]
}

// plaininstrAuthorities returns each pin's licensed text parser, base first.
//
// The path comes from `opgen.ParserFor` rather than from a suffix spelled here — one suffix, one
// owner, which is the rule `keywordgen.LexerFor` states and the generators already follow. A pin
// licensing no text parser contributes no grammar and is skipped, which is how a future pin whose
// authority is a decoder stays out of this composition without naming itself.
//
// The floor is on the *pin set*, and it is the one the per-kind assertions below cannot reach: a
// `refPins` whose threads entry lost its parser licence would compose one authority, find the seven
// atomic kinds missing from the reference, and report drift against a `plaininstrShapes` that is
// right. That failure names the wrong subject, so it is caught here where the subject is the fetch.
func plaininstrAuthorities(t *testing.T) []plaininstrAuthority {
	t.Helper()
	var out []plaininstrAuthority
	for _, pin := range testenv.RefPins() {
		path, ok := opgen.ParserFor(pin)
		if !ok {
			continue
		}
		src := testenv.RequireSpecRef(t, path)
		out = append(out, plaininstrAuthority{
			path: path, src: src, production: plaininstrProduction(t, path, src),
		})
	}
	if len(out) < 2 {
		t.Fatalf("only %d pin licenses a text parser, want >=2 (core and threads); a grammar read "+
			"from one authority omits a tracked proposal's arms and reads as complete", len(out))
	}
	return out
}

// plaininstrArms re-extracts `plaininstr`'s arms from the reference: kind → the immediate
// sequences written after it.
//
// This is the *authority* half of the drift control, and it shares no code with
// `plaininstrShapes` — an extractor derived from the thing it checks agrees by construction,
// which is a control comparing a value to itself.
//
// # More than one authority, composed base-wins
//
// The grammar is the union of the tracked set (§9 G-2), so the arms are composed over the pin set —
// and composed **base-wins**, for the reason keywordgen measured and this file can witness directly:
// the threads pin's baseline predates multi-memory, so its `LOAD` arm is `LOAD offset_opt align_opt`
// (spec-threads/parser.mly:419) against the core pin's `idx_opt offset_opt align_opt`
// (spec/parser.mly:596). A wholesale read of the overlay does not *add* the atomic arms, it rewrites
// four memarg arms to a three-proposals-stale form and deletes every GC and memory64 arm — so the
// base's arms for a kind win, and the overlay contributes only kinds the base has no arm for.
//
// TestAtomicMemargNarrownessIsTheRevisions pins that `LOAD` disagreement as the *reason* the
// atomic arms' missing `idx_opt` is not a claim about atomics.
func plaininstrArms(t *testing.T) map[keywordKind][]string {
	t.Helper()
	arms, _ := plaininstrArmsByPin(t)
	return arms
}

// plaininstrArmsOf extracts one authority's `plaininstr` arms, uncomposed.
//
// Split out so a control can hold the *two* authorities apart — the composed table cannot witness
// that the overlay's `LOAD` arm disagrees with the base's, because base-wins is precisely what
// discards it. TestAtomicMemargNarrownessIsTheRevisions is that control, and this is the reader it
// needs.
func plaininstrArmsOf(t *testing.T, auth plaininstrAuthority) map[keywordKind][]string {
	t.Helper()
	arms := map[keywordKind][]string{}
	for _, arm := range productionArms(t, auth.src, auth.production) {
		fields := strings.Fields(arm)
		kind := keywordKind(fields[0])
		if kind != keywordKind(strings.ToUpper(string(kind))) {
			// A lowercase leader is a nonterminal, not a token: not a mnemonic arm. None exist
			// in `plaininstr` at bdd7164; the branch is here so one appearing upstream is
			// skipped rather than recorded as a bogus kind, and the arm floors below still
			// notice if many of them appear.
			continue
		}
		arms[kind] = append(arms[kind], strings.Join(fields[1:], " "))
	}
	return arms
}

// plaininstrArmsByPin returns the composed arms and, beside them, the kinds each authority actually
// *contributed*.
//
// The second return value is not the same fact as "how many arms the authority holds", and the
// distinction is one memarggen paid for: the threads parser has 83 `plaininstr` arms and contributes
// 7, because base-wins keeps only the kinds the base has no arm for. A control asserting the overlay
// was *read* would pass over a composition that kept nothing from it.
func plaininstrArmsByPin(t *testing.T) (map[keywordKind][]string, map[string][]keywordKind) {
	t.Helper()

	arms := map[keywordKind][]string{}
	contributed := map[string][]keywordKind{}
	total := 0
	for _, auth := range plaininstrAuthorities(t) {
		for kind, seqs := range plaininstrArmsOf(t, auth) {
			if _, claimed := arms[kind]; claimed {
				// Base-wins: an overlay arm for a kind the base already gave one is *dropped*, not
				// appended. Appending would make every memarg kind a two-arm kind and send it into
				// the initSugarKinds branch below, which is a failure naming the wrong fact.
				continue
			}
			arms[kind] = seqs
			contributed[auth.path] = append(contributed[auth.path], kind)
			total += len(seqs)
		}
	}
	for path := range contributed {
		slices.Sort(contributed[path])
	}
	t.Logf("plaininstr arms composed: %d arms over %d kinds", total, len(arms))
	for _, auth := range plaininstrAuthorities(t) {
		t.Logf("  %s contributed %d kinds", auth.path, len(contributed[auth.path]))
	}

	// Every authority contributes, which is the check `total` cannot make: an overlay whose every
	// kind the base already holds reads its whole file and keeps nothing, and the composition is
	// then exactly the base with a second read charged to it.
	for _, auth := range plaininstrAuthorities(t) {
		if len(contributed[auth.path]) == 0 {
			t.Errorf("%s contributed no plaininstr kind to the composition; the pin is read and "+
				"discarded, so the table is the base pin's alone while reading as composed",
				auth.path)
		}
	}

	// Vacuity, on both counts: 90 arms over 88 kinds composed (83/81 core + 7/7 threads), the
	// two-arm kinds being why the first pair differs. Floors rather than equalities, because
	// upstream adding an instruction must not fail this test — but *a comparison against an empty
	// set succeeds*, so without a plausible-size floor a reader that silently stopped working would
	// make every assertion below pass by asking nothing.
	//
	// **Set above the base pin's own 83/81, which is what makes them bound the composition rather
	// than the read.** A floor of 70 was satisfied by the core pin alone, so a threads authority
	// that stopped being read would have passed it — *floors bound the catastrophic case only*, and
	// the catastrophe here is not an empty map but a table that is 8% short and looks whole. The
	// narrow population is pinned beside the aggregate by
	// TestThreadsPinContributesItsAtomicArms, which names the seven kinds; this pair is the
	// aggregate.
	//
	// Every figure is the extractor's own printed output, not a hand count. The first draft of this
	// comment said 82 kinds and the two `checked` floors below said 80 and 82; all three were read
	// off a hand tally and all three were wrong, in the direction that makes a floor harder to
	// satisfy rather than easier. Printed: 81, 79, 81 then; 90/88 composed now.
	if total < 86 || len(arms) < 84 {
		t.Fatalf("extracted %d plaininstr arms over %d kinds, want >=86/84 (90/88 composed: 83/81 "+
			"at bdd7164 plus 7 at cc535ad); the extractor is not reading every production and the "+
			"drift checks below would agree with an incomplete grammar", total, len(arms))
	}
	return arms, contributed
}

// TestThreadsPinContributesItsAtomicArms is the narrow population beside plaininstrArms' aggregate
// floor: the seven kinds the overlay is composed *for*, named.
//
// The floor says the composed table is big enough; this says it holds the right seven arms. They are
// different claims and they fail differently — an upstream core parser.mly that grew, say, four arms
// would satisfy the floor with the threads pin dropped entirely. *Pin the narrow population beside
// the aggregate even when they agree.*
//
// Named rather than derived, and legitimately: the reference enumerates them. These are seven arm
// heads written literally at spec-threads/parser.mly:453-459, and what makes the transcription
// trustworthy is that the composition below re-reads the same file — a renamed token or a deleted arm
// arrives here rather than in a green.
func TestThreadsPinContributesItsAtomicArms(t *testing.T) {
	want := []keywordKind{
		"ATOMIC_FENCE",         // :455
		"ATOMIC_LOAD",          // :456
		"ATOMIC_RMW",           // :458
		"ATOMIC_RMW_CMPXCHG",   // :459
		"ATOMIC_STORE",         // :457
		"MEMORY_ATOMIC_NOTIFY", // :454
		"MEMORY_ATOMIC_WAIT",   // :453
	}

	_, contributed := plaininstrArmsByPin(t)
	got, ok := contributed[testenv.ThreadsRefParserMLY]
	if !ok {
		t.Fatalf("the composition kept no arm from %s; every kind below would then have come from "+
			"the core pin, which has no 0xfe region at all", testenv.ThreadsRefParserMLY)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the threads pin contributed %v, want exactly %v.\n\tAn extra kind is the overlay "+
			"winning an arm the base owns — its parser predates multi-memory, so that is a memarg "+
			"losing its `idx_opt`. A missing one is an atomic instruction with no grammar, which "+
			"`plaininstr` then declines to start and the text layer reports as an unexpected token.",
			got, want)
	}
}

// TestAtomicMemargNarrownessIsTheRevisions measures the premise `plaininstrShapes` widens on: that
// the atomic arms' missing `idx_opt` is a fact about **cc535ad**, not about atomic instructions.
//
// The argument the widening rests on is not "the standard outranks the snapshot" alone — that is the
// ruling, and a ruling's premises are checked rather than copied. The premise is that the threads
// pin's *whole* memarg family lacks the memory index, its own `LOAD` arm included, because its
// baseline predates multi-memory. If that were false — if the overlay wrote `LOAD idx_opt offset_opt
// align_opt` beside `ATOMIC_LOAD offset_opt align_opt` — then the narrowness *would* be a statement
// about atomics, and giving them `immMemarg` would accept a form the reference rejects with no
// authority for it at all.
//
// So the three assertions are the premise, not a restatement of the conclusion: the overlay's two
// arms agree with each other, the base's `LOAD` arm disagrees with both, and the base gives
// `ATOMIC_LOAD` no arm (so the disagreement is between revisions and not between two live grammars).
//
// It reads the authorities *uncomposed*, which is the half plaininstrArms structurally cannot show:
// base-wins exists to discard the overlay's `LOAD` arm, so the composed table has no trace of the
// fact being measured here.
func TestAtomicMemargNarrownessIsTheRevisions(t *testing.T) {
	auths := plaininstrAuthorities(t)
	var base, overlay map[keywordKind][]string
	for _, auth := range auths {
		switch auth.path {
		case testenv.RefParserMLY:
			base = plaininstrArmsOf(t, auth)
		case testenv.ThreadsRefParserMLY:
			overlay = plaininstrArmsOf(t, auth)
		}
	}
	if base == nil || overlay == nil {
		t.Fatalf("did not find both text parsers among the %d licensed authorities; this control "+
			"compares two revisions and cannot run against one", len(auths))
	}

	one := func(arms map[keywordKind][]string, kind keywordKind) (string, bool) {
		seqs, ok := arms[kind]
		if !ok || len(seqs) != 1 {
			return "", false
		}
		return seqs[0], true
	}

	overlayLoad, ok := one(overlay, "LOAD")
	if !ok {
		t.Fatalf("the threads parser gives LOAD no single plaininstr arm (%q); the premise being "+
			"measured is about that arm", overlay["LOAD"])
	}
	overlayAtomicLoad, ok := one(overlay, "ATOMIC_LOAD")
	if !ok {
		t.Fatalf("the threads parser gives ATOMIC_LOAD no single plaininstr arm (%q)",
			overlay["ATOMIC_LOAD"])
	}
	baseLoad, ok := one(base, "LOAD")
	if !ok {
		t.Fatalf("the core parser gives LOAD no single plaininstr arm (%q)", base["LOAD"])
	}
	t.Logf("LOAD: core %q, threads %q; threads ATOMIC_LOAD %q", baseLoad, overlayLoad,
		overlayAtomicLoad)

	if overlayLoad != overlayAtomicLoad {
		t.Errorf("the threads parser writes LOAD as %q and ATOMIC_LOAD as %q. They agree at "+
			"cc535ad, which is what makes the missing `idx_opt` a property of the revision; a "+
			"disagreement makes it a statement about atomics, and plaininstrShapes' widening of "+
			"the six atomic kinds to immMemarg then has no authority behind it",
			overlayLoad, overlayAtomicLoad)
	}
	if baseLoad == overlayLoad {
		t.Errorf("both parsers write LOAD as %q, so this control has stopped measuring anything: "+
			"the widening's premise is that the two revisions disagree about the memarg, and two "+
			"agreeing revisions mean either the fetch landed the same file twice or multi-memory "+
			"left the core grammar", baseLoad)
	}
	if seqs, ok := base["ATOMIC_LOAD"]; ok {
		t.Errorf("the core parser now gives ATOMIC_LOAD an arm (%q); the widening is a deviation "+
			"from the overlay, and with the base holding the arm it is the base that should be "+
			"read — and base-wins already reads it, so plaininstrShapes' comment is stale rather "+
			"than the table wrong", seqs)
	}
}

// widenedMemargKinds are the kinds whose arm this package deliberately reads **wider** than the
// authority writes it, with the reason and the ruling.
//
// Their arms are `offset_opt align_opt` and `plaininstrShapes` gives them `immMemarg`, which is
// `idx_opt offset_opt align_opt`. Held as its own set rather than as a `"offset_opt align_opt":
// immMemarg` row in `wantShapes` below, and the difference is a control rather than a style: a
// `wantShapes` row would make the two written sequences *interchangeable*, so a core `LOAD` arm that
// lost its `idx_opt` upstream — multi-memory removed, or a bad fetch landing an older parser.mly —
// would map to `immMemarg` and pass. The narrowing this repo would then be blind to is the one it
// most needs to see, since `retainMemarg`'s 0x40 bit is written from the memory index.
//
// So the widening is admitted for exactly these kinds, and TestPlaininstrShapesMatchTheReference
// checks both halves of the admission: that each one's arm really is the narrow sequence, and that
// no *other* kind carries it.
//
// See plaininstrShapes for why the narrowness is a fact about cc535ad rather than about atomics, and
// TestAtomicMemargNarrownessIsTheRevisions for the measurement.
var widenedMemargKinds = map[keywordKind]string{
	"ATOMIC_LOAD":          "offset_opt align_opt", // spec-threads/parser.mly:456
	"ATOMIC_STORE":         "offset_opt align_opt", // :457
	"ATOMIC_RMW":           "offset_opt align_opt", // :458
	"ATOMIC_RMW_CMPXCHG":   "offset_opt align_opt", // :459
	"MEMORY_ATOMIC_WAIT":   "offset_opt align_opt", // :453
	"MEMORY_ATOMIC_NOTIFY": "offset_opt align_opt", // :454
}

// wantShapes maps the reference's written immediate sequence to the shape this package names
// for it. The one fact stated by hand; every kind→shape row is checked against it.
//
// `offset_opt align_opt` is deliberately **absent**: it is the six atomic kinds' written sequence and
// admitting it here would license it for `LOAD` too. See widenedMemargKinds.
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

	// The per-shape row counts, printed, because `immShape`'s own constant comments state a figure
	// per shape — and a count in a comment is a claim. *Measure with the instrument, not by eye.*
	//
	// **Rows, which is not the figure those comments give.** They count the reference's *arms*, and
	// the two differ by exactly the initSugarKinds pair: `MEMORY_INIT` and `TABLE_INIT` each
	// contribute an `idx idx` arm and an `idx` arm while owning no row here at all, so immIdx reads
	// 21 rows against 23 arms and immIdxIdx 7 against 9. Stated because the numbers look like a
	// disagreement and are not one.
	byShape := map[immShape]int{}
	for _, shape := range plaininstrShapes {
		byShape[shape]++
	}
	t.Logf("plaininstrShapes: %d rows over %d shapes %v", len(plaininstrShapes), len(byShape), byShape)

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
		if narrow, widened := widenedMemargKinds[kind]; widened {
			// The admitted widening, and both halves of the admission are checked here: the arm is
			// still the narrow sequence, and the shape is still the wide one. A kind whose arm grew
			// its `idx_opt` upstream leaves this set rather than staying in it — the widening is a
			// deliberate deviation from an authority, so an authority that stopped needing it must
			// stop being deviated from.
			if seqs[0] != narrow {
				t.Errorf("widenedMemargKinds says %s's arm is %q and the reference writes %q; the "+
					"widening is admitted against a sequence that no longer exists, so this row is "+
					"licensing a deviation from nothing", kind, narrow, seqs[0])
				continue
			}
			if got != immMemarg {
				t.Errorf("%s: plaininstrShapes says shape %d, and widenedMemargKinds admits it as "+
					"immMemarg (%d) against the reference's %q", kind, got, immMemarg, seqs[0])
			}
			checked++
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
	// Above the base pin's own 79, per plaininstrArms' floors: a threads authority that stopped
	// being read would satisfy 70 with nothing missing on the board.
	if checked < 82 {
		t.Errorf("only %d kinds compared against the reference, want >=82 (86 composed: 79 at "+
			"bdd7164 plus 7 at cc535ad); the loop above is agreeing about an incomplete grammar",
			checked)
	}

	// The widened set is a claim about the reference too, in the direction the loop above cannot
	// take: a kind whose arm is the narrow sequence and which is *not* admitted here would fall
	// through to `wantShapes`, find nothing, and report "not a shape this package names" — which is
	// the right failure. What has no failure is a row here for a kind the reference gives no arm at
	// all, so the widening would sit licensing a deviation from a grammar with nothing to deviate
	// from. Same both-directions argument TestExpr1LeadersMatchTheReference makes about its list.
	for kind := range widenedMemargKinds {
		if _, ok := arms[kind]; !ok {
			t.Errorf("widenedMemargKinds admits a widening for %s, which the reference gives no "+
				"plaininstr arm; the row licenses a deviation from nothing", kind)
		}
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

// TestExpr1LeadersMatchTheReference re-extracts `expr1`'s arms and checks expr1NonPlainLeaders
// against them, in both directions.
//
// The list is a transcription of seven arm heads (parser.mly:813-834), and a transcription is a
// claim. This repo's measured hand-transcription error rate is seven wrong citations in twelve
// items, so the list is machine-checked against the same authority it came from rather than
// reviewed. Both directions matter and they fail differently:
//
//   - a leader the reference has and the list lacks makes `startsInstruction` too *narrow*, so
//     `bodyBoundary` rejects a legal folded form as `unexpected token` — accept-direction, and no
//     assert_malformed can ever see it;
//   - a leader the list has and the reference lacks makes it too *wide*, so a malformed module gets
//     `unimplemented` and parks in #64's bucket unanswerable — which is the defect #70 fixed, and
//     it would be a regression wearing the shape of the fix.
//
// Vacuity: `expr1` has ten arms at bdd7164, and a comparison against an empty set agrees perfectly.
// A reader that stopped working — a renamed production, a changed indentation — yields no arms, and
// every loop below then passes by asking nothing. The floor is the control on the control.
func TestExpr1LeadersMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefParserMLY)
	arms := productionArms(t, src, "expr1")

	// 10 arms at bdd7164: `plaininstr expr_list`, SELECT, CALL_INDIRECT ×2, RETURN_CALL_INDIRECT ×2,
	// BLOCK, LOOP, IF, TRY_TABLE. A floor rather than an equality, so an arm added upstream fails
	// the direction checks below with a real message instead of failing here with a count.
	if len(arms) < 8 {
		t.Fatalf("extracted %d expr1 arms, want >=8 (10 at bdd7164); the extractor is not reading "+
			"the production and every check below would agree with an empty set", len(arms))
	}

	// The reference's leaders, minus the one lowercase (nonterminal) arm — `plaininstr expr_list`,
	// whose leaders are the mnemonics shapeOf already answers.
	refLeaders := map[keywordKind]bool{}
	sawPlaininstr := false
	for _, arm := range arms {
		leader := strings.Fields(arm)[0]
		if leader == "plaininstr" {
			sawPlaininstr = true
			continue
		}
		if leader != strings.ToUpper(leader) {
			t.Errorf("expr1 has an arm led by the nonterminal %q, which this control does not "+
				"model; startsInstruction cannot answer for a production it has not read", leader)
			continue
		}
		refLeaders[keywordKind(leader)] = true
	}
	if !sawPlaininstr {
		t.Error("expr1 no longer has a `plaininstr expr_list` arm; startsInstruction's whole " +
			"first branch — shapeOf's domain — rests on that arm existing")
	}
	if len(refLeaders) < 5 {
		t.Fatalf("only %d non-plaininstr leaders extracted, want >=5 (7 at bdd7164)", len(refLeaders))
	}

	for kind := range refLeaders {
		if !expr1NonPlainLeaders[kind] {
			t.Errorf("the reference gives expr1 an arm led by %s and expr1NonPlainLeaders omits "+
				"it; startsInstruction is too narrow, so bodyBoundary rejects a legal folded "+
				"`(%s …)` as `unexpected token` — an accept-direction defect no vector can catch",
				kind, strings.ToLower(string(kind)))
		}
	}
	for kind := range expr1NonPlainLeaders {
		if !refLeaders[kind] {
			t.Errorf("expr1NonPlainLeaders names %s, which leads no expr1 arm in the reference; "+
				"startsInstruction is too wide, so a malformed `(%s …)` gets `unimplemented` and "+
				"parks in #64's bucket unanswerable — the defect #70 fixed, regrown",
				kind, strings.ToLower(string(kind)))
		}
	}
}

// TestStartsInstructionIsTheUnionOfBothArms pins startsInstruction against its two sources
// directly, which is the half the drift control above cannot reach.
//
// The control above checks the *list*. This checks the *predicate*, and they are not the same
// assertion: a correct list wired into a predicate that ignored it would pass the first and fail
// here. Scoped to the space on the plaininstr side by reflecting over the generated keyword table's
// kinds rather than naming mnemonics — the domain is derived, per decision 0006's ruling.
func TestStartsInstructionIsTheUnionOfBothArms(t *testing.T) {
	kinds := map[keywordKind]bool{}
	for _, kind := range keywords {
		kinds[kind] = true
	}
	if len(kinds) < 100 {
		t.Fatalf("only %d kinds in the generated table, want >=100 (173 at bdd7164); the sweep "+
			"below would be over almost nothing", len(kinds))
	}

	plain, folded, neither := 0, 0, 0
	for kind := range kinds {
		_, isPlain := shapeOf(kind)
		want := isPlain || expr1NonPlainLeaders[kind]
		if got := startsInstruction(kind); got != want {
			t.Errorf("startsInstruction(%s) = %v, want %v (shapeOf %v, expr1 leader %v)",
				kind, got, want, isPlain, expr1NonPlainLeaders[kind])
		}
		switch {
		case isPlain:
			plain++
		case expr1NonPlainLeaders[kind]:
			folded++
		default:
			neither++
		}
	}

	// Every region non-empty and plausibly sized, per the vacuity rule: a predicate that answered
	// `true` for everything, or `false` for everything, would satisfy the loop above only if one of
	// these is zero — and a per-region floor is what a plain non-nil check misses. 81 plain / 7
	// folded / 85 neither at bdd7164, printed rather than hand-counted.
	if plain < 70 || folded < 5 || neither < 50 {
		t.Errorf("partition is %d plain / %d folded-only / %d neither, want >=70/>=5/>=50 "+
			"(81/7/85 at bdd7164); a region at zero means the predicate is constant over the "+
			"table and the comparison above is vacuous on that side", plain, folded, neither)
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
	// Above the pre-atomics 81, per plaininstrArms' argument: a floor the shrunken table still
	// satisfies bounds the catastrophic case only.
	if checked < 84 {
		t.Errorf("only %d kinds checked against the keyword table, want >=84 (88 with the threads "+
			"pin's seven; 81 at bdd7164 alone); the tables shrank and this control is agreeing "+
			"about almost nothing", checked)
	}
}

// reFeArm matches one arm of the reference encoder's 0xfe region: the two opcode bytes and whatever
// the arm writes after them.
//
// Anchored on `op 0xfe; op 0x…` rather than on the constructor names, because the constructors are
// the *AST*'s (`AtomicLoad {ty = I32Type; …}`) while this repo's join key is the operator name — so a
// per-constructor reader would need a second vocabulary, and the byte is what the question is about.
var reFeArm = regexp.MustCompile(`(?m)^\s*op 0xfe; op 0x([0-9a-f]+); (.*)$`)

// TestReservedByteWireFormsAreTheReferences derives `reservedByteWireForms`' domain from the
// reference encoder's 0xfe region instead of trusting the one mnemonic in it.
//
// The claim being controlled is *"`atomic.fence` is the one atomic instruction whose wire form
// carries a byte no immediate shape writes"*, and the dangerous half is the word **one**: a second
// such arm would encode truncated, silently, in the accept direction — and an enumeration cannot see
// its own omission. So the partition is computed over all sixty-seven arms:
//
//	op 0xfe; op 0xNN; memop mo   → a memarg, and the text side must give it immMemarg
//	op 0xfe; op 0xNN; op 0xMM    → a reserved byte, and the text side must refuse it
//	anything else                → a tail this control does not model, which is a failure
//
// The third branch is the one that makes this a derivation rather than a two-case check: an upstream
// arm writing something new (a lane byte, a second index) arrives as an unmodelled tail rather than
// being silently sorted into whichever bucket its regex happens to match.
//
// The join is on the *code*, since that is the key both sides have — encode.ml writes the byte and
// `mnemonicOpcodes` records it, machine-derived from the same revision. The memarg half is not
// decoration either: it is where the widening in `plaininstrShapes` is checked against the wire form
// rather than against the grammar, which is the direction §9 G-3 cares about.
func TestReservedByteWireFormsAreTheReferences(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.ThreadsRefEncodeML)

	// Every 0xfe mnemonic this package knows, by sub-opcode. A code with two mnemonics is a
	// generator defect rather than this control's business, so they are collected and reported.
	byCode := map[uint32][]string{}
	for mnemonic, enc := range mnemonicOpcodes {
		if enc.prefix == 0xfe {
			byCode[enc.code] = append(byCode[enc.code], mnemonic)
		}
	}
	for code := range byCode {
		slices.Sort(byCode[code])
	}

	arms := reFeArm.FindAllStringSubmatch(src, -1)
	// Vacuity: 67 arms at cc535ad. A regex that stopped matching — a reformatted encoder, a `vecop`
	// spelling — yields none, and every assertion below then agrees with an empty partition.
	if len(arms) < 60 {
		t.Fatalf("found %d `op 0xfe; op 0xNN; …` arms in %s, want >=60 (67 at cc535ad); the reader "+
			"is not seeing the region and the partition below would be over nothing",
			len(arms), testenv.ThreadsRefEncodeML)
	}

	var memargCodes, reservedCodes []uint32
	for _, m := range arms {
		code, err := strconv.ParseUint(m[1], 16, 32)
		if err != nil {
			t.Errorf("could not read the sub-opcode from arm %q: %v", m[0], err)
			continue
		}
		switch tail := strings.TrimSpace(m[2]); {
		case tail == "memop mo":
			memargCodes = append(memargCodes, uint32(code))
		case strings.HasPrefix(tail, "op 0x"):
			reservedCodes = append(reservedCodes, uint32(code))
		default:
			t.Errorf("0xfe %#02x's encode arm writes %q, which is neither a memop nor a reserved "+
				"byte. This control partitions the region into those two, so an unmodelled tail is "+
				"an immediate nothing on the text side has been asked to write — and the encoder "+
				"would emit the opcode and stop, which decodes as a different instruction",
				code, tail)
		}
	}
	t.Logf("%d 0xfe arms: %d memop, %d reserved-byte (codes %#x)",
		len(arms), len(memargCodes), len(reservedCodes), reservedCodes)

	// The reserved-byte half, both directions.
	refused := map[string]bool{}
	for _, code := range reservedCodes {
		mnemonics := byCode[code]
		if len(mnemonics) == 0 {
			t.Errorf("0xfe %#02x writes a reserved byte and no mnemonic in the generated table "+
				"encodes to it; the instruction is unreachable from the text, so nothing refuses it "+
				"and nothing needs to", code)
			continue
		}
		for _, mnemonic := range mnemonics {
			refused[mnemonic] = true
			if !reservedByteWireForms[mnemonic] {
				t.Errorf("%q encodes to 0xfe %#02x, whose reference arm writes a byte beyond the "+
					"opcode, and reservedByteWireForms does not name it: this encoder writes the "+
					"opcode and stops, producing a truncated instruction in a module the suite "+
					"expects to work", mnemonic, code)
			}
		}
	}
	for mnemonic := range reservedByteWireForms {
		if !refused[mnemonic] {
			t.Errorf("reservedByteWireForms names %q, which no arm of the reference's 0xfe region "+
				"gives a reserved byte; the refusal rejects a module the reference accepts, and it "+
				"cites #532 for a reason the authority no longer states", mnemonic)
		}
	}

	// The memarg half: the wire form takes a `memop`, so the text side must have a shape that writes
	// one. This is the widening checked against the *encoding* rather than against the grammar.
	checked := 0
	for _, code := range memargCodes {
		for _, mnemonic := range byCode[code] {
			kind, ok := keywords[mnemonic]
			if !ok {
				t.Errorf("%q encodes to 0xfe %#02x and no keyword table row lexes it; the mnemonic "+
					"cannot be reached from the text at all", mnemonic, code)
				continue
			}
			shape, ok := shapeOf(kind)
			if !ok {
				t.Errorf("%q (%s) encodes to 0xfe %#02x with a `memop` and shapeOf answers for no "+
					"such kind; `plaininstr` declines to start on it, so the mnemonic reads as an "+
					"unexpected token", mnemonic, kind, code)
				continue
			}
			if shape != immMemarg {
				t.Errorf("%q (%s) encodes to 0xfe %#02x with a `memop`, and its text shape is %d "+
					"rather than immMemarg (%d); the image wants flags/index/offset and the reader "+
					"writes something else", mnemonic, kind, code, shape, immMemarg)
			}
			checked++
		}
	}
	// 66 of the 67, the reserved-byte arm being the remainder. A floor because the mnemonic count per
	// code is upstream's business, not this control's.
	if checked < 60 {
		t.Errorf("only %d memarg mnemonics checked against the 0xfe region, want >=60 (66 at "+
			"cc535ad); the join through mnemonicOpcodes is resolving almost nothing", checked)
	}
}

// TestAtomicEncodeReachesTheFrontierAndStops is the behaviour half of the two facts above: the six
// memarg kinds encode, and `atomic.fence` is refused with the reason that names #532.
//
// Two directions in one control because they are one boundary, and each is the other's falsification:
// a refusal broad enough to catch `i32.atomic.load` would leave the memarg rows failing, and a
// mechanism that encoded `atomic.fence` would leave its row failing. The pair is what pins the
// boundary where it was put rather than merely somewhere.
//
// It goes through `EncodeModule` — the real entry point — rather than through `reservedByteWireForms`
// directly. *A control can test the helper, not the path*: asserting the map holds `atomic.fence`
// proves the map holds it while nothing consults it.
func TestAtomicEncodeReachesTheFrontierAndStops(t *testing.T) {
	// `(memory 1 1 shared)` because a shared memory needs a maximum (valid.ml:601-605 at cc535ad) —
	// though nothing here validates, the module is written the way the suite writes it so a later
	// reader can paste it into a vector.
	const frame = `(module (memory 1 1 shared) (func %s))`

	for _, tc := range []struct {
		mnemonic string
		body     string
		wantErr  string // "" means it must encode
	}{
		{mnemonic: "i32.atomic.load", body: "i32.const 0 i32.atomic.load drop"},
		{mnemonic: "i64.atomic.store", body: "i32.const 0 i64.const 0 i64.atomic.store"},
		{mnemonic: "i32.atomic.rmw.add", body: "i32.const 0 i32.const 1 i32.atomic.rmw.add drop"},
		{
			mnemonic: "i32.atomic.rmw.cmpxchg",
			body:     "i32.const 0 i32.const 1 i32.const 2 i32.atomic.rmw.cmpxchg drop",
		},
		{
			mnemonic: "memory.atomic.wait32",
			body:     "i32.const 0 i32.const 0 i64.const 0 memory.atomic.wait32 drop",
		},
		{
			mnemonic: "memory.atomic.notify",
			body:     "i32.const 0 i32.const 0 memory.atomic.notify drop",
		},
		{
			mnemonic: "atomic.fence",
			body:     "atomic.fence",
			wantErr:  "reserved byte",
		},
	} {
		t.Run(tc.mnemonic, func(t *testing.T) {
			src := fmt.Sprintf(frame, tc.body)
			_, err := EncodeModule([]byte(src))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("EncodeModule(%q) = %v, want no error.\n\tThe six memarg atomics encode "+
					"through retainMemarg over the generated naturalAlign table; a failure here is "+
					"either a missing alignment row or a shape the table does not give immMemarg.",
					src, err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("EncodeModule(%q) succeeded, want a refusal naming %q.\n\tIts wire form is "+
					"`op 0xfe; op 0x03; op 0x00` and no immediate shape writes that third byte, so "+
					"an image was just produced that decodes as something else (#532).",
					src, tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("EncodeModule(%q) = %v, want a refusal naming %q; the module is refused for "+
					"a reason other than the one #532 records, which is testimony about a frontier "+
					"that is not where the message says", src, err, tc.wantErr)
			}
		})
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

// TestLaneImmsCoversAllFiveArms is grave #76's control, and it is **scoped to the production, not
// to the nine vectors that fell**.
//
// The defect: `lane_imms` was implemented as `memarg laneidx`, so `idx_opt`'s greedy NAT ate the
// bare lane index of `v128.load8_lane 0 (…)` and the mandatory laneidx then found a paren. Nine
// must-succeed files — `simd_{load,store}{8,16,32,64}_lane.wast` and `simd_memory-multi.wast` —
// were at 0/1. Accept-direction, so *no `assert_malformed` could see it*; invisible until #69
// raised the accept oracle from 7 modules to 2130.
//
// **The nine vectors cannot certify the fix.** Eight of the nine files write only the bare arm, so
// a "fix" that simply stopped reading a memory index would take all nine to 1/1 and break arm 1 —
// scoring identically on the board while being wrong in general (§9 G-3, on a control instead of
// on the engine). Hence one row per arm, and the falsification below is the *two-NAT* case rather
// than the bare one.
//
// `simd_memory-multi.wast` is the one file that writes every arm, and it hands over a
// **bidirectional control**: `:12` is `v128.load8_lane 1 (i32.const 0)` where the lone `1` is a
// *lane* index, and `:22` is `v128.load8_lane 1 1` where the identical leading `1` is a *memory*
// index. Same token, opposite meanings, decided entirely by the follower — so a single wrong
// answer in `natContinuesMemarg` fails the two halves in opposite directions, where either half
// alone would look like a plausible reading. *When two fields disagree about a value, the suite has
// handed you a bidirectional control.*
//
// Falsified by running each defect, not by reasoning about it:
//   - Reverting to `memarg` then `laneidx`: the four bare-arm rows fail with `unexpected token`
//     and the board's nine files return to 0/1 — the grave, reproduced.
//   - Dropping `NatTok` from natContinuesMemarg: `two nats` and `nat lane, memory index` fail,
//     the bare rows stay green. This is the over-eager fix the nine vectors would have licensed.
//   - Dropping `OffsetEqNat`/`AlignEqNat`: `nat offset lane` and `nat align lane` fail, and the
//     board is unchanged — `simd_memory-multi.wast` writes those spellings but reports one verdict
//     for the whole module, so it cannot say *which* arm broke.
//   - Making natContinuesMemarg return true always: the bare rows fail, i.e. the original grave.
//   - Making it return false always: the memory-index rows fail. The pair is what makes this a
//     lookahead rather than a preference.
func TestLaneImmsCoversAllFiveArms(t *testing.T) {
	// One memory named and one anonymous, so both the NAT and the VAR memory-index arms have a
	// real target. Every row is spelled for `v128.load8_lane`, whose laneidx is `nat8` and whose
	// arms are shared with `v128.store8_lane` through one `immLaneImms` entry — the store rows
	// below are the check on that sharing rather than a repetition of it.
	const prefix = "(module (memory 1) (memory $m 1) (func (local $v v128) (drop (v128.load8_lane "
	cases := []struct {
		name, imms, want, why string
	}{
		// Arm 5 (parser.mly:673) — the one that was eaten. One NAT, and it is the lane index.
		{"bare laneidx", "0", "", "simd_load8_lane.wast:9"},
		{"bare laneidx, nonzero", "1", "", "simd_memory-multi.wast:12"},

		// Arm 1 (:663) — two NATs, the first a memory index. The other half of the pair.
		{"two nats", "1 1", "", "simd_memory-multi.wast:22"},
		{"nat offset lane", "1 offset=0 1", "", "simd_memory-multi.wast:13"},
		{"nat offset align lane", "1 offset=0 align=1 1", "", "simd_memory-multi.wast:14"},
		{"nat align lane", "1 align=1 1", "", "simd_memory-multi.wast:15"},

		// Arm 2 (:666) — a VAR is always a memory index, since laneidx is nat8 and has no
		// symbolic spelling.
		{"var lane", "$m 1", "", "simd_memory-multi.wast:17"},
		{"var offset lane", "$m offset=0 1", "", "simd_memory-multi.wast:18"},
		{"var offset align lane", "$m offset=0 align=1 1", "", "simd_memory-multi.wast:19"},
		{"var align lane", "$m align=1 1", "", "simd_memory-multi.wast:20"},

		// Arms 3 and 4 (:669, :671) — no leading idx at all, so nothing to disambiguate. Green
		// before the fix too, and here because the control is scoped to the production.
		{"offset lane", "offset=0 1", "", "simd_memory-multi.wast:28"},
		{"align lane", "align=1 1", "", "simd_memory-multi.wast:30"},

		// The laneidx is *mandatory* in all five arms, and this is the row that says the fix did
		// not make it optional — the natural way to over-correct. Synthetic: the suite writes no
		// malformed lane_imms at all, so every reject-direction row here is ours.
		{
			"memarg with no laneidx", "offset=0", "unexpected token",
			"synthetic: laneidx is mandatory in every arm (parser.mly:663-673); the suite has no " +
				"malformed lane_imms vector, so this direction is unsampled",
		},
		{
			"nothing at all", "", "unexpected token",
			"synthetic: arm 5 is `laneidx`, not `laneidx_opt` — the empty form matches no arm",
		},
		// And the width still belongs to laneidx rather than to idx: 256 is a legal *memory*
		// index and an illegal lane index, so this row would pass if the bare NAT were still
		// being read as an idx. The reject-direction face of the bidirectional control above.
		{
			"bare lane out of range", "256", "i8 constant out of range",
			"synthetic: laneidx is nat8 (:658) while idx is nat32 (:478) — 256 is legal as one " +
				"and not the other, so this pins which reader saw the token",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := prefix + c.imms + " (i32.const 0) (local.get $v)))))"
			err := ReadModule([]byte(src))
			switch {
			case c.want == "" && err != nil:
				t.Errorf("v128.load8_lane %s: %v; want accepted (%s)", c.imms, err, c.why)
			case c.want == "":
				return
			case err == nil:
				t.Errorf("v128.load8_lane %s: accepted; want %q (%s)", c.imms, c.want, c.why)
			case !strings.Contains(err.Error(), c.want):
				t.Errorf("v128.load8_lane %s: got %q, want it to contain %q (%s)",
					c.imms, err, c.want, c.why)
			}
		})
	}

	// `VEC_STORE_LANE` shares `immLaneImms` with `VEC_LOAD_LANE` (parser.mly:600-601), and the
	// shape table is the only thing that says so. If the store arm were wired to `immMemarg` — the
	// entry directly above it in the table, and a plausible slip — every row above would stay
	// green, since they are all spelled for the load family.
	//
	// **Three spellings, and the falsification is why it is not one.** The bare form was the
	// obvious choice and it is the one that does *not* catch this: under `immMemarg`,
	// `v128.store8_lane 1 (…)` is accepted, because `memarg` reads the lone NAT as a memory index
	// and then wants no laneidx at all. The two-NAT and full forms are what fail. Printed, not
	// predicted — the comment here first claimed the opposite. (`TestPlaininstrShapesMatchTheReference`
	// also fails on that mutation, from the other direction: it compares the table to the extracted
	// grammar. Two independent witnesses, which is the point of having both.)
	for _, imms := range []string{"1", "1 1", "$m offset=0 align=1 1"} {
		src := "(module (memory 1) (memory $m 1) (func (local $v v128) " +
			"(v128.store8_lane " + imms + " (i32.const 0) (local.get $v))))"
		if err := ReadModule([]byte(src)); err != nil {
			t.Errorf("v128.store8_lane %s: %v; want accepted "+
				"(simd_memory-multi.wast:27, :37, :34 — the store family shares lane_imms)", imms, err)
		}
	}
}

// TestEveryUnencodableShapeIsRefused is the *instruction* frontier's control, and it exists because
// the frontier had none.
//
// The field frontier has TestEncodeRefusesWhatItCannotWrite. The instruction frontier had nothing:
// deleting `blockinstr`'s `refuseUnencodable` call left the **entire package green** while
// `(module (func block i32.const 1 drop end i32.const 2 drop))` encoded to a body of `41 01 1a 41 02
// 1a 0b` — the block gone, its contents kept, the module decoding clean and computing something else.
// That is the accept-direction defect this whole file's discipline is about, and it was invisible.
//
// **The domain is derived, not enumerated** (0006/#33): every kind in `plaininstrShapes` whose shape
// is absent from `encodableShapes`, so a shape added to `immShape` without an entry in either map
// arrives here as an unrefused kind rather than as silence. A hand-listed set of "instructions we
// cannot encode yet" would freeze at the moment of authorship and go stale the first time the
// frontier moves outward — which is a thing that will happen deliberately and repeatedly.
//
// The witness per kind is a **mnemonic**, and that is where the derivation stops being free. A
// keywordKind is a *class* (`BINARY` covers `i32.add` through `f64.copysign`), so a wat module needs
// a spelling, and the spelling comes from the generated keyword table rather than from a second
// hand-written list — `keywordsByKind` is the same authority `TestStartsInstructionIsTheUnionOfBothArms`
// reads. What is hand-written is only the *frame* each mnemonic goes in, because a `load` needs an
// operand and a `memory.size` does not, and there is no table of that.
func TestEveryUnencodableShapeIsRefused(t *testing.T) {
	// A module frame per kind that needs operands to be well-formed. The frontier is about
	// well-formed input, so a row that does not parse is not a witness — the loop below fails such a
	// row rather than skipping it, on `a skip is not a verdict`.
	//
	// The default frame is a bare `(func <mnemonic> …)`, which suffices for every kind whose
	// immediates the parser reads from the text and whose operands it does not typecheck: the parser
	// is not a validator, so a stack-underflowing body is still well-formed here.
	frames := map[keywordKind]string{
		// The memarg family takes an address operand; the immediates are optional and omitted.
		"LOAD":           `(module (memory 1) (func (result i32) i32.const 0 %s))`,
		"STORE":          `(module (memory 1) (func i32.const 0 i32.const 0 %s))`,
		"VEC_LOAD":       `(module (memory 1) (func (result v128) i32.const 0 %s))`,
		"VEC_STORE":      `(module (memory 1) (func i32.const 0 v128.const i32x4 0 0 0 0 %s))`,
		"VEC_LOAD_LANE":  `(module (memory 1) (func (result v128) i32.const 0 v128.const i32x4 0 0 0 0 %s 0 0))`,
		"VEC_STORE_LANE": `(module (memory 1) (func i32.const 0 v128.const i32x4 0 0 0 0 %s 0 0))`,
		"VEC_EXTRACT":    `(module (func (result i32) v128.const i32x4 0 0 0 0 %s 0))`,
		"VEC_REPLACE":    `(module (func (result v128) v128.const i32x4 0 0 0 0 i32.const 0 %s 0))`,
		"VEC_SHUFFLE":    `(module (func (result v128) v128.const i32x4 0 0 0 0 v128.const i32x4 0 0 0 0 %s 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15))`,
		"REF_NULL":       `(module (func (result funcref) %s func))`,
		"BR_TABLE":       `(module (func i32.const 0 %s 0))`,
		"VEC_CONST":      `(module (func (result v128) %s i32x4 0 0 0 0))`,
	}

	// The kinds whose refusal cannot be attributed to the mnemonic, with the reason. Each needs a
	// construct that is *itself* unencodable in order to be well-formed at all, so the refusal
	// legitimately comes from elsewhere and this control can only witness that the module is refused —
	// not why.
	//
	// **They are still worth having as rows**, because "refused" is the property that matters for
	// accept-direction safety: what would be dangerous is emitting one. What they cannot do is
	// witness their own guard, and they are named here so that is on the record rather than hidden in
	// a green. Every entry leaves this table when GC's type encoding lands, at which point the frames
	// become attributable and the assertion above starts applying to them.
	witnessedByTheTypeLevel := map[keywordKind]bool{
		// The SIMD operand family: every one of these takes a `v128` operand, and the only way to
		// produce one is `v128.const` — itself `immVecConst`, itself unencodable. `v128.load` would
		// serve, and it is `immMemarg`, also unencodable. So there is no spelling of a v128 value in
		// this tier at all, which makes the whole family unattributable together.
		//
		// Note which SIMD kinds are *not* here: `VEC_LOAD` and `VEC_CONST` take no v128 operand, so
		// their frames are attributable and they are held to the assertion. The line falls exactly
		// where the operand type does, which is the tell that this list is a consequence rather than
		// a convenience.
		"VEC_STORE": true, "VEC_LOAD_LANE": true, "VEC_STORE_LANE": true,
		"VEC_EXTRACT": true, "VEC_REPLACE": true, "VEC_SHUFFLE": true,
	}

	// Every unencodable shape, and every kind carrying one — the derived domain.
	var kinds []keywordKind
	for k, shape := range plaininstrShapes {
		if !encodableShapes[shape] {
			kinds = append(kinds, k)
		}
	}
	slices.Sort(kinds)

	// The vacuity check is now on the *maps themselves*, not on the derived domain — #210 closed
	// the domain to zero on purpose, which is the milestone this rung was for, so "the domain is
	// nonempty" stopped being the right floor. A `plaininstrShapes` or `encodableShapes` that
	// failed to load would leave *both* maps empty (or `encodableShapes` short of all 16 known
	// shapes), which is what a floor on the total, rather than on the unencodable subset, still
	// catches — the same partition-not-total lesson `comparisons need a vacuity check` names,
	// pointed at a domain that is legitimately supposed to be empty today.
	// 86 rows and 16 shapes measured. The row floor sits above the 79 the table held before the
	// threads pin's seven kinds arrived, so a `plaininstrShapes` that lost them fails here rather
	// than passing a floor written for the smaller table — *floors bound the catastrophic case only*
	// unless they are re-measured when the population grows.
	if len(plaininstrShapes) < 82 || len(encodableShapes) < 16 {
		t.Fatalf("plaininstrShapes has %d rows and encodableShapes has %d shapes, want >=82/16 "+
			"(86/16 measured; 79 rows before the atomic kinds): an empty, truncated, or "+
			"atomics-less map here would make every row below pass by comparing nothing",
			len(plaininstrShapes), len(encodableShapes))
	}

	// **The frontier closed with #210, and that is the pass condition — not a fallback.** Every
	// one of the 16 immediate shapes `plaininstr` dispatches to is now in `encodableShapes`, so
	// the derived domain above is legitimately empty: there is no instruction immediate left in
	// the tracked grammar this encoder cannot write. A rise past zero — a 17th shape added to
	// `immShape` without a matching `encodableShapes` entry, or an existing entry flipped back to
	// `false` — re-arms the frame-based refusal check below automatically, on the same
	// derived-domain mechanism that has run every PR since #33; nothing about the mechanism
	// changes, only the fact it currently has nothing to report.
	if len(kinds) == 0 {
		return
	}

	// The spelling per kind, inverted out of the *generated* table rather than hand-listed. A kind is
	// a class — `BINARY` covers `i32.add` through `f64.copysign` — and `keywords` is spelling→kind, so
	// the inversion is where a witness comes from without a second vocabulary being written here.
	byKind := map[keywordKind][]string{}
	for spelling, k := range keywords {
		byKind[k] = append(byKind[k], spelling)
	}

	for _, kind := range kinds {
		mnemonics := byKind[kind]
		if len(mnemonics) == 0 {
			t.Errorf("kind %s has no mnemonic in the generated keyword table, so no wat module can "+
				"reach it and this control cannot witness its refusal", kind)
			continue
		}
		mnemonic := slices.Min(mnemonics) // deterministic, and any member exercises the shape
		frame, ok := frames[kind]
		if !ok {
			frame = `(module (func %s))`
		}
		src := fmt.Sprintf(frame, mnemonic)

		t.Run(string(kind), func(t *testing.T) {
			// A frontier is about well-formed input, so an unparseable frame is a defect in this
			// table and not a pass. Failed rather than skipped: a skip is not a verdict.
			if err := ReadModule([]byte(src)); err != nil {
				t.Fatalf("the frame for %s does not parse, so it witnesses nothing — fix the frame, "+
					"because a refusal on malformed input says only that the input was malformed: "+
					"%s\n%v", kind, src, err)
			}
			b, err := EncodeModule([]byte(src))
			if err == nil {
				t.Fatalf("EncodeModule wrote % x for %s (shape %d), which it has no encoding for: "+
					"emitting an instruction's opcode without its immediates, or dropping it and "+
					"keeping its operands, produces a module that decodes clean and computes "+
					"something else — the accept-direction defect no suite vector can see (§9 G-3)",
					b, mnemonic, plaininstrShapes[kind])
			}
			// **The refusal must name *this* mnemonic**, and that requirement is the whole reason
			// this assertion exists rather than a bare non-nil check.
			//
			// Measured at bdd7164, by neutralizing `plaininstr`'s shape gate: 13 of the 23 rows then
			// in this table failed as designed and **10 passed anyway** — `struct.get`, both
			// `ref.cast` and `ref.test`, and the six array forms, every one of them refused by a
			// *different instruction in its own frame*. `ref.cast`'s frame needs a `ref.null`
			// operand, whose `immHeaptype` is refused first; the array frames needed a `(type (array
			// …))`, which `encodableOrErr` refused at the type level before any body was reached.
			// Those rows were scoring green while saying nothing about the kind they were named for
			// — the witness-correlated-with-subject grave (#106) at row scale, and a bare non-nil
			// check cannot see it. `struct.get` and the six array forms have since left this table
			// (their `immIdxIdx`/`immIdxNat32` shapes are retained now), taking their
			// `witnessedByTheTypeLevel` rows with them — this paragraph stays as the measurement that
			// justified the mechanism, not as a claim about today's row count.
			//
			// So the frame's other refusals are not allowed to stand in for this one. Where a frame
			// genuinely cannot avoid them — a `struct.get` requires a struct type, and a struct type
			// is itself unencodable — the kind is named in `witnessedByTheTypeLevel` below with that
			// reason, which is a declared-and-tracked deferral rather than a silent pass (#6).
			if !witnessedByTheTypeLevel[kind] && !strings.Contains(err.Error(), mnemonic) {
				t.Errorf("refusing %s says %q, which does not name %s: the frame's *other* "+
					"instructions or types were refused first, so this row scores green while saying "+
					"nothing about the kind it is named for — either give it a frame whose only "+
					"unencodable thing is the mnemonic, or list it in witnessedByTheTypeLevel with "+
					"the reason it cannot have one", mnemonic, err, mnemonic)
			}
			if !strings.Contains(err.Error(), "#8") {
				t.Errorf("refusing %s says %q, want a tracking issue: an unexplained gap is the "+
					"declared-and-tracked ruling's silent half (#6)", mnemonic, err)
			}
			for _, spec := range []string{"malformed", "unexpected", "unknown", "invalid"} {
				if strings.Contains(err.Error(), spec) {
					t.Errorf("refusing %s says %q, which contains the spec word %q: reporting a "+
						"malformedness for a module the spec calls well-formed lies about the input "+
						"to conceal a gap in the engine (#5)", mnemonic, err, spec)
				}
			}
		})
	}
}

// The refusal test this file previously cited here named the last subject `try_table` had, and
// #199 gave it an encoding — that deleted test's own header called this outcome by name: "When
// it goes, this test goes with it." `try_table`'s dangerous-drop case (a body kept, its opener
// lost) is re-pointed to `encodableModules` in encode_test.go, on the same rule `block`/`loop`/
// `if`/`select` and `call_indirect`/`return_call_indirect` were re-pointed by before it — a round
// trip asserting the opener is *present* with its catch vector, rather than a refusal asserting
// it is *absent*.
