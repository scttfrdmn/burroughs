package spec

import (
	"go/ast"
	goparser "go/parser" // aliased: this package already has a `parser` of its own (sexpr.go)
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The board's bounds, and the control that keeps a bound near the thing it bounds
// (decision 0013, #87).
//
// # The defect this exists for
//
// `allOnPassFloor` was **798 against an actual 4178** and had been since #56, fifteen
// commits back. It could not have caught a regression erasing four fifths of the
// all-gates-on lane. Found by reading the printed total next to the constant while raising
// it for #86 — by eye, not by any control.
//
// The floor was falsifiable in the ordinary sense: drop the count below 798 and it fires.
// So *break the thing it names and watch it fail* was satisfied and the floor was still
// decoration, because the defect is not in the assertion — it is in the **distance between
// the assertion and the measurement**. That distance was unasserted, so it grew every time
// a PR moved the count and left the constant alone.
//
// This is the vacuity class with the vacuum somewhere new. A comparison against an empty
// set agrees perfectly; so does a comparison against a set that is merely far away. Same
// signature: the mechanism runs, agrees, and says nothing.
//
// # The shape, and why ceilings differ from floors
//
// A tracked bound asserts two things now — `bound ≤ actual ≤ bound + slack` for a floor,
// mirrored for a ceiling — so a board that jumps past its window forces the constant forward
// *in the PR that moved it*, the same rule as updating `[Unreleased]` in the PR that earns
// the entry.
//
// # The space is nineteen bounds, not four, and the control is what said so — three times
//
// Decision 0013 was written claiming four (`passFloor`, `allOnPassFloor`,
// `binaryFailCeiling`, `textFailCeiling`) and this test's first run named four more. The ADR
// was corrected rather than the trigger narrowed, which is the point of scoping a trigger to
// the *convention* instead of to the names you had in mind: an enumeration would have agreed
// with the wrong count, silently, and *a control scoped to the current sample inherits the
// current blind spot*.
//
// **Then this table went stale by ten rows, which is the defect it exists to name arriving in its
// own prose.** It said "eight" through the literal cross-check's five bounds, the two fail
// ceilings, and #9's three validator bounds — every one of them routed correctly through
// `boardBound`, so the executable control stayed green while its documentation described a
// population that had more than doubled. The rows below are now the full set, and
// `TestEveryBoardBoundIsChecked` asserts that every bound it finds in the AST is *named here*, so
// a twentieth cannot land undocumented. What that check cannot verify is whether a row's kind and
// reason are *right* — prose is not machine-checkable, and where this table and the call sites
// disagree, the call sites outrank.
//
// **The third time is `validateMismatchCeiling` (#305), and it is the one this table's mechanism
// could not have caught**: the population existed, was documented in prose as "the 0 that is not
// here", and had *no constant at all* — so there was nothing for the AST walk to find or for this
// table to be missing. A bound that does not exist is not an undocumented bound; the row that
// caught it was an independent count disagreeing by 4 (see `Failure.Accepted`).
//
// **A fourth event, and it is not a fourth bound — it is this walk's own trigger narrowing** (#307).
// `validateAdmitCeiling` became derived (`142 + <live members of a named set>`) and therefore a
// `:=`, which `*ast.ValueSpec` does not match, so the census reported **18** with all nineteen bounds
// present, correctly declared, and correctly checked. Nothing was undocumented and nothing was
// unbounded; the *instrument's* claim about where bounds live had gone false. The heading above still
// says nineteen for that reason — the population did not move — and the loss was visible only to the
// exact count, `minBoundPopulation` being 8 and perfectly content with 18. See the `*ast.AssignStmt`
// arm for the fix and for why a floor could not have caught it. That row is a `const` again as of
// #306, so the arm it forced now covers a shape with no live specimen; the *Derived* kind below says
// what follows from that.
//
// **The `actual` column is the figure at the row's last re-base and several are older than that** —
// `passFloor` reads 4162 against a live 60390. The columns doing work here are `kind` and `slack`,
// which is the part a reader cannot infer; the live figure is at the call site, which outranks this
// table by the rule above.
//
// They partition into four kinds, and the kind decides whether slack applies:
//
//	bound                    actual   kind          slack   why
//	passFloor                4162     board count   250     moves in strata; can go stale
//	allOnPassFloor           64075    board count   250     same board plus gated vectors
//	unsupportedCeiling       60872    board count   250     shrinks as capabilities land
//	binaryFailCeiling        0        at terminal   —       0 cannot drift from 0
//	textFailCeiling          0        at terminal   —       0 cannot drift from 0
//	unimplementedCeiling     0        at terminal   —       0, and 0004 fixes it there
//	encodeFailCeiling        46       exact re-base  0      drains as the encoder learns forms
//	execFailCeiling          81       exact re-base  0      drains as the interpreter lands rules
//	validateFailCeiling      493      exact re-base  0      the whole validator stratum
//	validateDeclineCeiling   389      exact re-base  0      its declines, named per opcode
//	validateAdmitCeiling     104      exact re-base  0      its admissions — the accept direction
//	validateMismatchCeiling  0        at terminal   —       right refusal, wrong message (0003)
//	totalFloor               2143     vacuity       —       deliberately loose by design
//	filesFloor               242      vacuity       —       deliberately loose by design
//	i32SpellingFloor         2531     vacuity       —       the extractor found this kind at all
//	i64SpellingFloor         1081     vacuity       —       same, per kind
//	f32SpellingFloor         1335     vacuity       —       same, per kind
//	f64SpellingFloor         1551     vacuity       —       same, per kind
//	agreementFloor           6498     vacuity       —       the cross-check compared something
//
// **Exact re-base** is the third kind and it is not a kind in the code, deliberately: it is
// `ceilingBound` with slack 0, meaning "move me in the PR that moves the column". That the helper
// used to *exempt* those is grave #293, immediately below.
//
// **Derived** was a fifth kind and **no row is of that kind today**, which is worth a paragraph
// rather than a deletion. It was exact re-base with the constant replaced by an expression over a
// named set: `validateAdmitCeiling` read `142 + <live members of alignmentAdmissions>` under #307's
// condition on taking the admission, so a member draining moved the bound with no edit and what
// needed re-basing was the total it decomposed. #306 drained the whole named set, the ledger retired,
// and the row is a plain `const` again at 104.
//
// The kind stays described because **the walk's `*ast.AssignStmt` arm exists for it and now has no
// subject in the tree.** That is a trigger predicate whose only specimen has left: keep the arm (a
// tripwire is re-pointed, never closed — the *risk* is "a bound is not a `const`", which no rule
// prevents recurring), and know that nothing currently exercises it, so its own correctness rests on
// having been watched fire in #307 rather than on today's green.
//
// **At terminal**: a bound already at the value it is draining toward cannot go stale,
// because the distance between "at most 0" and "0" is not a quantity that can grow. A slack
// term there is a mechanism with no risk to catch, which is the very thing #87 is about.
//
// **Vacuity floors are exempt on purpose, and this is the distinction that matters most.**
// `totalFloor`/`filesFloor` in TestBareModuleSpansAreNonEmptyAndPlausible are *plausibility*
// bounds — 2000 against 2143, 230 against 242 — and looseness is their function: they exist
// to catch a walk that found nothing, so they must sit far enough below the real figure to
// survive ordinary corpus movement. Slack-checking them would fire on a control that is
// working exactly as designed, and "fires for reasons that are not findings" is how a gate
// trains the reflex of scrolling past it. So they route through boardBound with slack 0 and
// `vacuityBound`, which *names* the exemption instead of leaving them outside the door — the
// licensed-skip pattern: an exemption granted at one place, in the open.
//
// # slack 0 meant two opposite things, and the helper honoured the wrong one (grave #293)
//
// The table above listed only #87's eight until the row above this one was written, and every bound
// added between — `encodeFailCeiling`, `execFailCeiling`, and #9's three validator bounds — was
// written with **slack 0 meaning "this column must be re-based exactly, no room"**; their comments
// say "Slack stays 0" in exactly that sense. `boardBound` read the same 0 as the table's *other* meaning, "at terminal, cannot drift",
// and returned before the staleness check. So the tightest intention a caller could express
// produced the loosest behaviour available, and it did so silently:
//
//	encodeFailCeiling   517 against an actual  46 — stale by 471
//	execFailCeiling     243 against an actual  81 — stale by 162
//
// Both are #87's own defect, in the mechanism written to end it, reached through the one argument
// the mechanism could not tell apart from its own exemption. The fix is that the exemption was
// **never needed for the case it was written for**: a ceiling at terminal 0 with an actual of 0 has
// distance 0, so `distance > slack` is false and it passes on the arithmetic. Removing the early
// return costs the terminal bounds nothing and gives every later slack-0 bound the exactness its
// author was asking for.
//
// The shape, since it is the reusable part: **a sentinel value that encodes an author's intent must
// not collide with a value the mechanism reads as permission.** "0 slack" and "no slack applies"
// are different claims, and a single int cannot carry both — which is why the vacuity exemption is
// a *kind* (see boundKind's own comment, which says exactly this about `vacuityBound` and was
// therefore already the answer, one argument over).

// boardBound checks one bound in the direction it constrains **and** the distance between
// the bound and the measurement.
//
// `why` is the site's own diagnosis of what a violation means — kept at the call site
// because each of these bounds fails for a different reason, and a generic message would
// throw away the part a reader needs. It is a parameter rather than a second inline
// comparison so that **one concept has one trigger** (#78): the first draft left the
// original `if` in place next to the helper call, which double-reports on a real regression
// and leaves two comparisons to keep in agreement.
//
// slack 0 means "re-base this bound exactly, no room" and **is checked**, not exempted: a bound
// that genuinely cannot drift is at terminal, so its distance is 0 and it satisfies the check
// arithmetically. The exemption belongs to `vacuityBound`, a kind rather than a magic slack — see
// the package comment's grave on the two things slack 0 used to mean.
func boardBound(tb testing.TB, name string, actual, bound, slack int, kind boundKind, why string) {
	tb.Helper()

	distance := 0
	switch kind {
	case floorBound, vacuityBound:
		if actual < bound {
			tb.Errorf("%s: board count %d fell below floor %d — %s", name, actual, bound, why)
			return
		}
		distance = actual - bound
	case ceilingBound:
		if actual > bound {
			tb.Errorf("%s: count %d rose above ceiling %d — %s", name, actual, bound, why)
			return
		}
		// Mirrored: a ceiling goes stale by the actual falling *away below* it, which is
		// what unsupportedCeiling does every time a capability lands. The direction of
		// staleness is opposite to the direction of the constraint — worth stating, because
		// getting it backwards yields a check that never fires and looks identical to one
		// that does (the #34 partition lesson: assert the discriminating direction).
		distance = bound - actual
	}

	if kind == vacuityBound {
		return // exempt by kind; see the package comment's table
	}
	// slack 0 is checked, not exempted — see the note on "slack 0 meant two opposite things".
	// A bound at terminal has distance 0 and satisfies `distance > slack` trivially, so the
	// exemption was never needed for the case it was written for.
	if distance > slack {
		tb.Errorf("%s is stale: %d against an actual %d, a distance of %d with a slack of %d.\n\t"+
			"Move it to %d in this PR. A bound left behind by a large jump degrades into "+
			"decoration — it stops being able to catch anything smaller than the gap, which is "+
			"how allOnPassFloor came to sit at 798 against 4178 for fifteen commits (#87).",
			name, bound, actual, distance, slack, actual)
	}
}

type boundKind int

const (
	floorBound boundKind = iota
	ceilingBound
	// vacuityBound is a floor whose looseness is its function: a plausibility bound
	// asserting a walk found something, not a regression bound tracking a number. It
	// constrains the low side like floorBound and is never slack-checked, no matter what
	// slack is passed — so the exemption is a property of the *kind*, not of a caller
	// remembering to pass 0.
	vacuityBound
)

// boardBoundSlack is the slack for the three tracked board counts — `passFloor`,
// `allOnPassFloor`, `unsupportedCeiling`.
//
// **250, and what it is actually absorbing is not what the first draft claimed.** That draft
// reasoned "large enough not to fire on ordinary progress", measured `passFloor` across
// fifteen commits (783 → 1419 → 1628 → 1941 → 1953 → 1992 → 4122 → 4159 → 4161 → 4162), and
// quoted two steps over 250. Recomputing the steps says **+636, +209, +313, +12, +39, +2130,
// +37, +2, +1** — *three* over 250, not two, and one of them (+313) is a middling stratum
// rather than a landmark. The figure was quoted from memory of the shape instead of from the
// subtraction, which is the sin this ADR is about, committed in its own justification.
//
// The recomputation also shows the reasoning was aimed at the wrong quantity. **A PR that
// moves the board and raises the bound in the same PR leaves a distance of zero**, so no
// step size, however large, trips this — that is the rule (0013), the same rule as updating
// `[Unreleased]` in the PR that earns the entry. The step distribution is therefore almost
// irrelevant to the size.
//
// What the slack must genuinely absorb is **corpus drift between fetches**: the suite is not
// SHA-pinned (#42 — `git clone --depth 1` of the default branch), so upstream adding vectors
// moves the actual with no local change and nobody to raise the bound. 250 is roughly 6% of
// a 4162 board, comfortably more than upstream's observed churn and far less than any real
// regression. When #42 lands this constant should shrink hard or disappear in favour of an
// exact count.
//
// So: still weather rather than a principle, but now weather about the right thing. The
// second-order honesty point stands — if this fires on a PR that is genuinely routine, widen
// it here **with the new observation recorded**, rather than raising the bound and moving on.
// A slack quietly widened is the staleness defect one level up.
//
// It would not be needed at all if the corpus were pinned: an exact expected count is
// strictly stronger, and #42 (the suite fetch is `git clone --depth 1` of the default
// branch, not a SHA) is what makes an exact count fire on upstream's schedule rather than
// ours. Contrast decision 0012's census, whose inputs are both committed and which
// therefore gets the exact golden file this could not have. *The strongest control the
// inputs admit, at each site.*
const boardBoundSlack = 250

// TestEveryBoardBoundIsChecked reads this package's AST and requires every board bound to
// route through boardBound.
//
// #87 recommended scoping this "by reflection over the constants rather than a list", and
// that is not buildable: all four bounds are **function-local** `const` declarations, which
// reflection cannot see. The instinct was right — *derive the domain, never enumerate it* —
// and the mechanism had to change to the AST, which is the same move
// TestEverySkipSiteIsLicensed makes. A rule saying "all of these go through one door" needs
// something asserting that they do, or the mechanism has the shape it exists to forbid.
func TestEveryBoardBoundIsChecked(t *testing.T) {
	// ParseFile per file rather than ParseDir: the latter is deprecated as of Go 1.25 in
	// favour of golang.org/x/tools/go/packages, and the engine's go.mod is dependency-free
	// (0005) — so a lint suppression would be the wrong answer to a real deprecation when a
	// dependency-free alternative exists. Same shape as internal/testenv's two AST controls,
	// which is the established pattern here.
	paths, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("globbing this package: %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("found %d _test.go files in this package; a walk over almost no files agrees "+
			"with any set of boardBound calls", len(paths))
	}

	fset := token.NewFileSet()
	type site struct{ name, pos string }
	var bounds, checked []site
	// This file's own comments — the table the bounds are documented in. A Builder rather than
	// `+=` because the linter is right about the quadratic copy, and the read below wants one
	// string: `registry.String()` is taken once, after every file has been walked.
	var registry strings.Builder

	for _, path := range paths {
		file, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		if path == "boardbound_test.go" {
			for _, g := range file.Comments {
				registry.WriteString(g.Text())
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.ValueSpec:
				// A const named *Floor or *Ceiling is a board bound by construction of
				// the naming convention, and the convention is the trigger. Keyed on the
				// name rather than on the comparison, because a bound that is declared
				// and *never compared at all* must also be a finding — that is the
				// unreachable-constant shape (grave 0003).
				for _, id := range v.Names {
					if isBoundName(id.Name) {
						bounds = append(bounds, site{id.Name, fset.Position(id.Pos()).String()})
					}
				}
			case *ast.AssignStmt:
				// **A bound does not have to be a constant, and this arm exists because one
				// stopped being one.** `validateAdmitCeiling` was `142 + <live members of a
				// named set>` (#307's condition), so it was a `:=` — an `*ast.AssignStmt`, which
				// `*ast.ValueSpec` does not cover — and the census's population fell 19 → 18 with
				// every bound still present and still checked. *A guard's trigger predicate is
				// itself a claim about the space*, and the claim here was "a bound is declared in
				// a ValueSpec", true for eighteen bounds and false for the nineteenth.
				//
				// **#306 drained that ledger and the row is a `const` again, so this arm currently
				// matches nothing** — kept anyway, because what it guards is the *risk* that a bound
				// is not a `const`, and nothing in the conventions prevents that recurring. A
				// tripwire whose subject dissolves is re-pointed and never closed. The consequence
				// to hold onto is that its green today is vacuous: it was watched fire once, on
				// #307, and that observation is the whole evidence that it works.
				//
				// Worth reading for *which* instrument caught it: `minBoundPopulation` is 8, so the
				// floor was satisfied by 18 and said nothing. Only the exact count fired. That is
				// the floors-bound-the-catastrophic-case rule landing on the file that minted it —
				// a small silent loss is exactly what a floor cannot see, and a trigger narrowing
				// by one shape is the smallest such loss there is.
				if v.Tok != token.DEFINE {
					return true
				}
				for _, lhs := range v.Lhs {
					id, ok := lhs.(*ast.Ident)
					if ok && isBoundName(id.Name) {
						bounds = append(bounds, site{id.Name, fset.Position(id.Pos()).String()})
					}
				}
			case *ast.CallExpr:
				fn, ok := v.Fun.(*ast.Ident)
				if !ok || fn.Name != "boardBound" {
					return true
				}
				// arg 1 is the name string; record what it claims to bound.
				if len(v.Args) > 1 {
					if lit, ok := v.Args[1].(*ast.BasicLit); ok {
						checked = append(checked, site{
							strings.Trim(lit.Value, `"`), fset.Position(v.Pos()).String(),
						})
					}
				}
			}
			return true
		})
	}

	// Vacuity, and it is the whole reason this test can be trusted: a walk that finds zero
	// bounds agrees with any set of boardBound calls, and a moved file or a renamed
	// convention produces exactly that. Asserted as a minimum so a ninth bound is covered
	// rather than ignored.
	//
	// Eight, not the four 0013 was drafted claiming: this walk is what corrected the ADR,
	// and the floor quotes the measured population rather than the remembered one.
	const minBoundPopulation = 8
	if len(bounds) < minBoundPopulation {
		t.Fatalf("found %d board bounds in this package's AST; there were %d when 0013 was "+
			"written (passFloor, allOnPassFloor, unsupportedCeiling, unimplementedCeiling, "+
			"binaryFailCeiling, textFailCeiling, totalFloor, filesFloor), and a population "+
			"this small means the trigger stopped matching — *coverage is to a trigger what a "+
			"vacuity check is to a comparison* (#82)", len(bounds), minBoundPopulation)
	}
	if len(checked) < minBoundPopulation {
		t.Fatalf("found %d boardBound calls; want one per bound (%d found). A bound compared "+
			"inline bypasses the staleness check entirely, which is the #87 defect surviving "+
			"the control written for it", len(checked), len(bounds))
	}

	// **The exact count beside the floor, because the floor is what let the table rot.** A
	// minimum covers a nineteenth bound automatically — which was the intent, and which is
	// precisely how ten bounds arrived without anyone updating the package comment. *Floors bound
	// the catastrophic case; only an exact count sees a small silent gain.* Both inputs are in the
	// tree, so this is 0012's exact-golden situation rather than #42's drifting corpus.
	sortedBoundNames := make([]string, 0, len(bounds))
	for _, b := range bounds {
		sortedBoundNames = append(sortedBoundNames, b.name)
	}
	sort.Strings(sortedBoundNames)

	// 18 → 19 with `validateMismatchCeiling` (#305 slice 2), which is the first bound this exact
	// count has ever seen arrive — and it arrived for the reason the count exists: a population that
	// had been 0 since the stratum was created became 4, and it was being *silently absorbed* by the
	// bound next door rather than going unbounded in a visible way.
	const boundPopulation = 19
	if len(bounds) != boundPopulation {
		t.Errorf("found %d board bounds, want exactly %d. A new bound is welcome — add its row to "+
			"this file's table with its kind and its reason, and re-base this constant in the same "+
			"PR. Bounds found: %v", len(bounds), boundPopulation, sortedBoundNames)
	}

	byName := map[string]bool{}
	for _, c := range checked {
		byName[c.name] = true
	}
	for _, b := range bounds {
		if !byName[b.name] {
			t.Errorf("%s (%s) is a board bound with no boardBound call naming it: it is either "+
				"compared inline — bypassing the staleness check — or never compared at all, "+
				"which is a constant with no reachable path (grave 0003)", b.name, b.pos)
		}
	}
	// The documentation half. Routing through `boardBound` is what makes a bound *checked*; being
	// named in this file's table is what makes it *findable*, and the ten undocumented bounds
	// proved those are different properties. Checked against the comments rather than the whole
	// file, since every bound's name appears in its own declaration by construction — a citation
	// check that reads the code it is citing agrees with itself.
	table := registry.String()
	if len(table) < 1000 {
		t.Fatalf("read %d bytes of comments from boardbound_test.go; the table lives there, so a "+
			"read this short means the citation check below is searching an empty string and "+
			"agreeing with every bound", len(table))
	}
	for _, b := range bounds {
		if !strings.Contains(table, b.name) {
			t.Errorf("%s (%s) is checked but undocumented: add a row to boardbound_test.go's "+
				"table with its kind, its measured actual, and what a violation means. The kind is "+
				"the part a reader cannot infer — slack 0 means `re-base me exactly` for a fail "+
				"ceiling and `I am at terminal` for a drained one, and grave #293 is what happens "+
				"when that distinction is left to the reader", b.name, b.pos)
		}
	}

	if !t.Failed() {
		t.Logf("%d board bounds, all routed through boardBound and all named in the table", len(bounds))
	}
}

// isBoundName is the trigger predicate, and it is deliberately broader than the four names.
//
// *A guard's trigger predicate is itself a claim about the space, and an under-matching one
// fails silently by construction* (#82, grave #78): a trigger listing today's four names
// would go green on a fifth bound called `simdPassFloor`, producing no finding rather than
// a wrong one. The convention is the domain.
//
// It therefore **over**-matches, which is the safe direction and not a hypothetical one: this test's
// own population minimum was briefly called `boundFloor`, and the walk dutifully reported it as a
// nineteenth board bound that was neither checked nor documented. The finding was correct and the
// name was wrong — a control's own constants must not wear the convention it hunts. Widening the
// predicate to exclude them would have traded a loud false positive for the silent false negative
// this comment exists to forbid.
func isBoundName(name string) bool {
	return strings.HasSuffix(name, "Floor") || strings.HasSuffix(name, "Ceiling")
}
