// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The authority-derived controls. Every check in this file compares something this package
// *asserts* against something outside it that can disagree — `valid.ml`, `binary.OpMnemonic`, or
// this package's own AST — and none of them can be satisfied by the corpus.
//
// That division is the point rather than a filing convenience. The corpus's `assert_invalid`
// vectors are satisfied by *any* refusal, so 84.3% of them cannot tell a right rule from a wrong
// one (see ErrTypeMismatch's comment); and no negative vector at all can catch an accept-direction
// defect. What is left is agreement with the authority, checked mechanically.
//
// # The third blind spot: message-match cannot see an under-rejection, and there are sixteen
//
// The two above are about the *corpus*. This one is about the instrument that was supposed to cover
// what the corpus cannot: 0003's message match, which compares the refusal's text against the
// vector's expectation and is the only thing that catches a right verdict delivered with wrong
// testimony. **It fires only on refusals.** A validator that *accepts* an invalid module emits no
// message, so there is nothing for the match to disagree with — the under-rejection has no message
// channel at all, not a channel that reports a passing grade. This is the accept-direction gap
// restated one layer in: not merely that no vector catches it, but that the instrument covering the
// vectors' weakness shares their direction.
//
// **Sixteen known instances lived in the tree when this paragraph was written**, named here rather
// than only where their count lived (ruling: Scott, PR #307): `decodeMemop` dropped the memarg
// alignment, so a vector whose sole defect was an over-aligned SIMD access was typed successfully
// and accepted. Four *more* of the same cause did reach the message match —
// `simd_store{8,16,32,64}_lane.wast`, which carry a second defect and so were refused for the wrong
// reason — and **that split is the exhibit, which is why it is kept now that #306 has closed both
// halves**: one cause, 20 vectors, and the message oracle saw exactly the quarter of it that
// happened to be refused at all. The 16 were invisible to it by construction, not by bad luck, and
// the fix arrived from reading the reference rather than from any signal this test could emit.
//
// A coverage claim is an assertion an instrument cannot check about itself, so its honest form
// carries its known counterexamples. This blind spot is unchanged by the specimen draining; what
// changed is that this campaign no longer knows of a live instance, which is a weaker statement than
// there being none.
//
// # And the weaker statement was the right one to make: two more instances arrived
//
// The paragraph above was written when the campaign knew of no live instance. Two turned up
// afterwards, neither found by anything in this file, and both are recorded here rather than only at
// their fix sites — because the count is the coverage claim's counterexample list, and a list that
// only grows where the fixes live is a list nobody reads as a list.
//
//   - **#311, `blockType`'s valtype arm.** `check_blocktype` calls `check_valtype` on the
//     single-valtype form (`valid.ml:420`) and this package did not, so `(block (result (ref 1)))`
//     typed successfully against a module declaring no type 1. Pure accept direction: the validator
//     said nothing, so there was no message for the match to disagree with, and no negative vector
//     could reach it either. Found by reading the reference, exactly as the alignment specimen was.
//   - **#310's divergence, and this one is a rule *no vector exercises at all*.** The offset bound
//     (`checkOffset`) is implemented, so it is not an under-rejection — but the decision inside it,
//     which memory's index type the bound reads, has no vector on either side. All four vectors
//     expecting `offset out of range` declare one memory, where the two readings agree. So the rule
//     is exercised only in the region where its open question does not apply, and the reject-direction
//     oracle is blind to it not because the verdict is an accept but because the *discriminating
//     input does not exist in the corpus*.
//
// That second shape is worth naming separately, because it is not what the three blind spots above
// describe and it is the more common thing to miss. A rule no vector exercises cannot be seen by any
// instrument that reads verdicts, whichever direction it reads them in — the corpus does not disagree
// with it, it declines to ask. `offset_test.go` builds the discriminating modules by hand for exactly
// that reason, and its tripwire watches the *reference's* text, since the suite has no channel
// through which this decision could ever report itself wrong. (Ruling: Scott, #310.) **Naming it
// separately turned out to be the start of a register rather than the whole treatment** — three more
// slices found members of the same shape, and they are collected below as the fourth blind spot.
//
// # The blind spot shrank measurably for the first time, and the measurement is what says so
//
// Everything above describes a population this file cannot see. #310 is the first entry that also
// *drained* it by a counted amount: `validateAdmitCeiling` **104 → 103**, one admission becoming a
// pass. The number matters less than the channel it came out of — a bare `+1` in the pass column is
// consistent with either an under-rejection repaired or a decline gaining vocabulary, and only the
// first is a shrink of this blind spot. So the ledger row asserts the *pair*: `accepted` down one and
// `pass` up one with `declined` untouched (`internal/spec/ledger_test.go`), which is a correctness
// claim where a lone increment would have been a movement.
//
// Worth having here rather than only in the ledger, because a coverage claim with no measured
// direction of travel is a claim that can only ever be restated. The population this file describes
// is now 103 admissions, and the next slice's reward is a subtraction from that figure. (Note:
// Scott, on the #317 relay.)
//
// # Correction: the three `module quote` vectors are not in the 103, and the sentence that said they
// were is the one that was here
//
// The first version of the paragraph above read "103 admissions, three of which are the `module
// quote` vectors no validator rule can reach". **Measured, they are in a different column.** All
// three — `address.wast:213` and `simd_address.wast:143,151` — are `assert_invalid` commands whose
// payload is a quoted module the wast reader does not build, so they score **unsupported**
// (`address.wast: 259/259 pass, 1 unsupported`; `simd_address.wast: 47/47 pass, 2 unsupported`) and
// are never handed to this package at all. `align.go` and `validate.go` both say `unsupported` about
// the same three vectors correctly; this file's sentence was written from memory rather than from the
// board, which is the drift a claim about *another instrument's column* invites.
//
// The consequence inverts what the wrong sentence implied, which is why it is worth more than a
// silent edit. Carried inside the admissions they would be permanently-unreachable residue, and an
// allowance holding residue implies a drain that can never complete — so the 103 would not be
// expected to reach zero. In the column they are actually in they are **drainable by harness work**
// (#8's text-format lane; #53 landed the lexer half, and a bare quoted module asserts *validity*,
// which is this package's word to give), and the 103 is expected to reach zero whole. Both figures
// keep their own instrument: the admissions are `validateAdmitCeiling`'s, the three are
// `unsupportedCeiling`'s.
//
// Recorded rather than repaired quietly because the residue treatment in #317 was asked for *on the
// strength of this sentence* — a wrong premise about a population is the one kind of error that
// arrives back as an instruction. (Correction: on the #317 relay.)
//
// # The fourth blind spot: the rules with no corpus witness at any gate setting, and there are seven
//
// #310's entry above named this shape — a rule the corpus does not disagree with but *declines to
// ask about* — and named it as a single divergence. It is not one. Three consecutive slices each
// found instances of it independently, by the same method (mutate the rule, run both lanes, read the
// delta), and each recorded its own in its own PR's prose. **A shape named once with one instance
// reads as an incident, so the instances are collected here**: the point of a register is that a
// reader can ask how many of this validator's rules the board is silent about, and get a number.
//
// Seven, as of the export slice, counting #310's own divergence as the first — it is a member and
// not the preamble to the list. Grouped by *what* has no witness rather than by slice, because the
// grouping is the finding: an arm is the obvious case and it is the minority.
//
//   - **A reading**, and the entry that named the shape: which memory's index type `checkOffset`'s
//     bound consults (#310). All four vectors expecting `offset out of range` declare one memory,
//     where the two readings agree. Held by `TestReferenceStillReadsMemoryZeroForTheOffsetBound`
//     (`offset_test.go`), which watches the reference's text because the suite has no channel.
//   - **A value.** `memRangeI64` and `tabRangeI64` (`module.go`). Both arms are reached, and mutating
//     the *constant* to anything between the largest valid i64 limit in the corpus and the smallest
//     invalid one passes the entire suite in both lanes. `tabRangeI64` is stronger still: it is the
//     full u64 domain, so no `binary.Limits` can violate it and the arm's message can never be
//     emitted by any module at all. Held by `TestLimitsRangesMatchTheReference`, which reads the
//     numbers out of `valid.ml`.
//   - **An ordering, twice.** `checkLimits` tests `min <= range` before `min <= max`
//     (`limits_authority_test.go`), and `moduleExports` resolves every index before comparing any
//     name (`export_test.go`'s M1). Neither is observable: every min-over-max vector in the corpus
//     has its minimum inside the range, and fusing the export loops moves neither lane. Both are
//     transcribed from the reference on its authority alone and held by unit rows.
//   - **An arm.** `exportExists`' tag arm (`export_test.go`'s M7). Making it accept unconditionally
//     moves neither lane *with every gate on*, so no vector anywhere in the corpus exports a tag
//     with an out-of-scope index. The EH gate makes the arm reachable; it does not make it sampled.
//   - **A delegation.** `exportExists`' memory arm calls `memoryExists` instead of spelling the
//     lookup again (`export_test.go`'s M6). A hand-rolled copy moves neither lane, which is exactly
//     the condition under which two copies agree — the copy that drifts is not the copy written
//     today.
//
// Two things this register is careful about. It is a count of the **known** members, and the
// population is unknown by the same argument every coverage claim here carries: what produced each
// entry was a mutation somebody chose to run, so the honest reading is "seven found by seven
// bills", not "seven that exist". And nothing counts them mechanically, because a mutation's null
// result is not a property of the tree that a test could read — it is a measurement. What *is* mechanical is that
// each member has a unit row naming the mutation it stands in for, so the bills in
// `limits_authority_test.go` and `export_test.go` are where a reader goes from this list to the
// assertion holding each one, `offset_test.go` for the first. (Ruling: Scott, PR #335 relay —
// accumulate the set in the one place that already exists for it, or the next slice rediscovers the
// shape.)
//
// # This register is a floor, not an extent, and that is #333's shape again
//
// The paragraph above says "seven found by seven bills" and stops one step short of naming why that
// is a defect rather than a caveat. **The seven were collected from the mutations somebody happened
// to run while working on some other slice**, so the register's domain is "the rules that got
// mutated" — and a list assembled that way can only ever agree with itself. A fourth unwitnessed arm
// in a file nobody mutated moves no entry here, exactly as #333's total summed over the registry
// could only ever agree with the registry. Sited beside the list rather than left implicit because
// the failure mode is that a *growing* register reads as a census: five slices, five entries, and no
// reader can tell collection from coverage.
//
// The derivation exists and has been used informally at every discovery: **an arm is unwitnessed
// exactly when mutating it moves neither lane.** That makes the extent computable rather than
// collectable, so what stands between this floor and an extent is a full sweep across the package's
// arms under that predicate — filed as **#338**, deliberately deferred, because it is instrument work
// and the counter says the next PRs are product. Cited as a tracked sweep rather than described as an
// intention, which is *a design debt is discharged by a tripwire, never by an intention* honoured at
// the only strength a deferral can honour it. (Ruling: Scott, PR #337 relay.)
//
// **Still seven after the `check_elem` slice, and the reason is a measurement rather than an
// omission.** That slice carried an obvious candidate — the implicit-table form, where wire flags 0
// means active-at-table-0 and the index the rule resolves is one nothing in the module wrote — so the
// derivation above was run on it deliberately instead of waiting for the next slice to trip over it.
// Making the rule treat an implicit index as absent moves **both** lanes by seven, so the arm is
// witnessed six times over, and by economy rather than by intent: `(elem (i32.const 0))` is the
// shortest way to write an active segment, so the suite reaches the implicit form whenever it wants a
// tableless-module vector at all. The register does not grow, and this paragraph is why a reader can
// tell that from the register never having been asked — which is the same distinction between
// collection and coverage the section above is about, one level down.
//
// **The board is that mutation's only reader, and no unit row anywhere covers it.** Every row in
// `elem_test.go` passes with the implicit index skipped, because a row that constructs a segment with
// `TableIndex: 0` cannot distinguish "implicit" from "explicit zero" any more than the validator can —
// the wire bit is not in `binary.ElemSegment`, by that field's own design (*absent and empty are
// different facts* was applied to `ByExpr` and not to this). So the seven is quoted here rather than
// asserted as a row, and the two mutations in that slice's battery are covered by disjoint instruments:
// this one board-only, the parenthetical unit-only. Deleting the corpus would uncover this one silently,
// which is what the suite-as-oracle already implies and is worth stating where the figure lives.
// (Directive: Scott, PR #337 relay, and the answer to the question he asked; the single-reader note is
// his PR #339 review.)
//
// # The message oracle discriminates layers, never rules within a layer
//
// A limit of 0003's message match distinct from the third blind spot above, which is about the
// *direction* the instrument reads. This one is about its *resolution*, and it holds even on refusals
// it can see: the oracle compares the refusal's text against the vector's expectation, so two rules
// that produce the same string are one row to it. It can tell a validator refusal from a decoder
// refusal — that is the layer question, and it answers it well. It cannot tell which of a layer's
// rules refused.
//
// **Two structural instances, which is what makes this a property of the oracle rather than a quirk
// of one family.** `type mismatch` is the long-standing one: **2288 vectors across 124 files** name
// it, spanning most of the instruction validator, and no census keyed on that string can attribute a
// single one of them to a rule. The second arrived with the `check_elem` slice and is small enough to
// read whole: `unknown table` is named by **16 vectors in 8 files under two keys** (12 want the bare
// string, 4 want `unknown table 0`), and `table c x` — the one reference lookup that produces it — has
// **twelve call sites in `valid.ml`**: ten inside the instruction arms, of which `table.copy` alone
// holds two (`:633-634`), plus `check_elemmode` (`:1090`) and `check_export` (`:1135`). The corpus
// reaches five of those twelve, so a bucket labelled `unknown table` holds seven rows belonging to
// `check_elemmode`, four to `table.init`, three to `check_export`, one to `call_indirect` and one to
// `return_call_indirect` behind the tail-call gate — and reports that as one number.
//
// The consequence is specific and it is not "the census is untrustworthy": the census delta is exactly
// right about *how many* rows moved and says nothing about *which*. So a rule that fires too broadly
// can deliver the forecast total out of the wrong rows — over-converting rows belonging to a
// neighbouring rule while under-converting its own — and the arithmetic reads identically to a correct
// slice. **The remedy is a habit rather than an instrument, because the row list costs nothing:** name
// the vectors the rule may move *before* writing it, and predict that only those move. `check_elem`'s
// list is in `elem_test.go`'s header, where it also caught two facts a grep had wrong — four of the
// sixteen are multi-line assertions no single-line search finds, and six of the seven movers live
// outside `elem.wast`. That the repo had already paid for this distinction once (`spec_test.go`'s "7
// by key and 9 by cause") is the argument for making the list standard equipment for every remaining
// slice rather than a one-off for this one. (Ruling: Scott, PR #337 relay.)

// TestUnknownCategoriesMatchTheReference is ErrUnknown*'s own promised control, in both
// directions.
//
// `valid.ml` has one `lookup`, not nine, and it composes the message as `"unknown " ^ category ^
// " " ^ index`. The ten categories are ten one-line bindings (`:44-53`), so the authority's set is
// *parsed* rather than transcribed: a renamed category fails the forward direction and a new one
// fails the reverse.
//
// The reverse direction is the half that matters, and it spent five slices *saying* so on a
// mechanism it did not have. This paragraph read: "Slice 1 claims seven of the ten, and the three it
// does not claim are pinned as a literal set — so a later slice adding `tag` has to come here and say
// so." Phrased historically now, because #336 measured it and it was false: a slice adding `tag`
// came, and nothing here made it say anything. What holds the claim today is the forward loop's
// derived domain, described at its own site below.
func TestUnknownCategoriesMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefValidML)

	// `let <name> (c : context) x = lookup "<category>" ...` — keyed on the string literal
	// argument, which is the thing the message is built from, not on the binding's name.
	re := regexp.MustCompile(`lookup\s+"([^"]+)"`)
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		found[m[1]] = true
	}

	// Vacuity, and pinned exactly rather than floored. A regex that stops matching yields an
	// empty set, and an empty set agrees with every sentinel below by construction — the
	// comparison-against-nothing shape. Exact because the reference is fetched at a pin (#42's
	// discipline, applied to the interpreter rather than to the suite): upstream adding an
	// eleventh category is a fact for a reader to record, not churn to absorb.
	const wantCategories = 10
	if len(found) != wantCategories {
		t.Fatalf("parsed %d lookup categories from %s, want %d (%v); either the regex stopped "+
			"matching — in which case every check below is comparing against nothing — or "+
			"upstream changed the index spaces, which is a finding either way",
			len(found), testenv.RefValidML, wantCategories, sortedKeys(found))
	}

	// Forward: every sentinel this package declares must name one of the reference's categories,
	// spelled `unknown ` + the category and nothing else. The trailing-detail rule is a *format*
	// question and is checked separately, below.
	//
	// # This loop was nine literals until #336, and the reverse direction below was decoration
	//
	// The list read `ErrUnknownType, …, ErrUnknownElemSegment`, and the comment on the reverse check
	// said a later slice adding `tag` "has to come here and say so". **It did not.** The export slice
	// declared `ErrUnknownTag`, left both this list and `wantUnclaimed` alone, and the whole file
	// stayed green — because a sentinel absent from the literal is never *claimed*, so `tag` matched
	// the "declared out of scope" arm exactly as before. The list's stated mechanism required the
	// enumeration to be extended, which is the one thing an enumeration cannot make anyone do.
	//
	// That is `#333`'s shape one file over, and the derived sibling was already directly below: the
	// format check reads the package's `ErrUnknown*` declarations out of the AST. So the domain here
	// comes from source too — `packageSentinels` over the globbed non-test files — and the reverse
	// direction now has something to be a check *of*. The candidate predicate is the union of the two
	// halves rather than either alone: an `ErrUnknown*` name with a message that does not begin
	// `unknown ` is a misspelling that must fail loudly, and a sentinel named something else with a
	// message that does is still a lookup message.
	lookupSentinels := map[string]string{}
	for id, msg := range packageSentinels(nil) {
		if strings.HasPrefix(id, "ErrUnknown") || strings.HasPrefix(msg, "unknown ") {
			lookupSentinels[id] = msg
		}
	}
	// Pinned exactly, on the vacuity argument the categories carry above: a derived set that shrinks
	// makes the reverse check quietly report unclaimed categories as gaps, and one that empties makes
	// this loop assert nothing at all.
	const wantSentinels = 10
	if len(lookupSentinels) != wantSentinels {
		t.Fatalf("derived %d lookup sentinel(s) from this package's source, want %d (%v) — either a "+
			"sentinel was removed or `packageSentinels` stopped reading a file, and both make the "+
			"reverse direction below agree with a shorter list than the package has",
			len(lookupSentinels), wantSentinels, sortedKeys(lookupSentinels))
	}

	claimed := map[string]bool{}
	for id, msg := range lookupSentinels {
		cat, ok := strings.CutPrefix(msg, "unknown ")
		if !ok {
			t.Errorf("%s = %q, which does not begin with %q; the reference composes this message as "+
				`"unknown " ^ category ^ " " ^ index, so the prefix is not stylistic`, id, msg, "unknown ")
			continue
		}
		if !found[cat] {
			t.Errorf("%s = %q names category %q, which %s does not have (%v) — a sentinel spelling "+
				"a category the authority never uses reports a verdict no vector expects",
				id, msg, cat, testenv.RefValidML, sortedKeys(found))
			continue
		}
		claimed[cat] = true
	}

	// Reverse: which of the ten no sentinel claims. **The list is now empty, and that is a board
	// figure rather than a formality** — the export slice's `ErrUnknownTag` was the tenth, so this
	// package names every index space the reference has a `lookup` for.
	//
	// The two entries it used to hold are worth keeping as the record of what the list was *for*.
	// `data segment` and `elem segment` sat here until slice 5 on the stated reason that the bulk
	// memory/table ops are "declined at the prefixed-opcode arm before any index is read" — a deferral
	// whose subject was the *dispatch*, not the rules, so it expired the moment 0xFC was typed. `tag`
	// sat here until the export slice, and **left without anyone editing this line**, which is the
	// grave the forward loop above now records: the list only ever made a slice come here and say so
	// when a human happened to extend an enumeration two directions away.
	//
	// Empty is not vacuous here. Every category runs through the loop below, and an eleventh arriving
	// upstream lands in the "gap wearing no label" arm — so what the emptiness asserts is *full
	// coverage*, checked, rather than nothing left to check.
	wantUnclaimed := map[string]bool{}
	for cat := range found {
		switch {
		case claimed[cat] && wantUnclaimed[cat]:
			t.Errorf("category %q is both claimed by a sentinel and listed as out of scope; the "+
				"list is a scope declaration and cannot also describe a rule that exists", cat)
		case !claimed[cat] && !wantUnclaimed[cat]:
			t.Errorf("category %q is one of the reference's index spaces, no sentinel here names "+
				"it, and it is not on the out-of-scope list — so it is a gap wearing no label. "+
				"Either declare it (add it to wantUnclaimed with the slice that owns it) or "+
				"implement it", cat)
		}
	}
	for cat := range wantUnclaimed {
		if !found[cat] {
			t.Errorf("%q is listed as an unclaimed reference category but %s has no such lookup; "+
				"a deferral that names something the authority does not have reads as tracked "+
				"while tracking nothing", cat, testenv.RefValidML)
		}
	}
}

// TestUnknownIndexMessagesAreCategorySpaceIndex is the *format* half, and it is an AST control
// because the hazard is a call site rather than a sentinel.
//
// The corpus expects both `unknown local` and `unknown local 2`, matched as substrings (decision
// 0003). So `fmt.Errorf("%w: local %d", ErrUnknownLocal, i)` satisfies the first vector and fails
// the second while being entirely right about the module — a wrong verdict on a right analysis,
// and invisible to any test that only checks the sentinel. The rule is that the index follows the
// category immediately, and any detail this validator wants to add goes *after* it.
//
// Derived, not enumerated: the sentinel set comes from the package's own `ErrUnknown*` declarations
// and the call sites from every `fmt.Errorf` mentioning one, so a ninth sentinel or a new site is
// covered without an edit here.
//
// # This is the coverage half of a two-part check, and it is checking a proxy
//
// What the rule is actually about is the *rendered* message, and an AST walk cannot render one. So
// this checks the format directive — a proxy — over **every** site, and the rendered strings are
// checked over a **sample** by the behavioural witnesses (`TestUnknownIndexMessagesRender`), which
// call the validator and read what comes out. Neither is sufficient: the AST walk cannot see what
// `%d` prints, and the witnesses cannot see a site no vector reaches.
//
// The proxy admits a **literal** index as well as `%d`, and that is not a loosening to make a
// failure go away — it is the first thing this control found. `addrType`'s no-memory verdict is
// `fmt.Errorf("%w 0 (module declares no memory)", ErrUnknownMemory)`: memory index 0 is the only
// index a slice-1 memory access can name, so the index is a constant and hardcoding it renders
// `unknown memory 0 (…)`, which is exactly the shape the rule demands. Requiring the *directive*
// `%d` there would have been a control insisting on its own proxy over the property.
func TestUnknownIndexMessagesAreCategorySpaceIndex(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing this package: %v", err)
	}
	fset := token.NewFileSet()
	sentinels := map[string]bool{}
	type site struct {
		format string
		pos    string
		name   string
	}
	var sites []site

	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue // the sentinels and their sites are engine code; a test's prose is not a message
		}
		f, err := goparser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				for _, id := range vs.Names {
					if strings.HasPrefix(id.Name, "ErrUnknown") {
						sentinels[id.Name] = true
					}
				}
			}
			return true
		})
	}
	// Vacuity on the sentinel set before the sites are read: zero sentinels means zero sites
	// match, and a walk over nothing agrees with any format string in the package.
	if len(sentinels) < 7 {
		t.Fatalf("found %d ErrUnknown* declarations in this package's AST, want at least the 7 "+
			"slice 1 declares; the trigger stopped matching, so the format check below has no "+
			"subject", len(sentinels))
	}

	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := goparser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Errorf" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, arg := range call.Args[1:] {
				id, ok := arg.(*ast.Ident)
				if !ok || !sentinels[id.Name] {
					continue
				}
				format, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Errorf("%s: cannot unquote format string: %v", fset.Position(lit.Pos()), err)
					continue
				}
				sites = append(sites, site{format, fset.Position(call.Pos()).String(), id.Name})
			}
			return true
		})
	}

	// A second vacuity floor, on the *sites* rather than the sentinels: seven sentinels with zero
	// call sites is the unreachable-constant shape, and it would pass a loop over `sites`
	// silently.
	if len(sites) < 7 {
		t.Fatalf("found %d fmt.Errorf sites wrapping an ErrUnknown* sentinel, want at least 7 "+
			"(one per sentinel); a sentinel with no site is a declared verdict nothing returns",
			len(sites))
	}

	// `%w ` then the index: either the directive or a decimal literal, and nothing else in
	// between. A prefix match rather than equality, because detail *after* the index is allowed.
	indexFirst := regexp.MustCompile(`^%w (%d|\d)`)
	literalIndex := 0
	for _, s := range sites {
		if !indexFirst.MatchString(s.format) {
			t.Errorf("%s: %s is formatted %q; the reference's message is `unknown <category> "+
				"<index>`, so the format must put the index immediately after the sentinel — `%%w "+
				"%%d`, or a literal where only one index is possible — and any detail after it. A "+
				"colon or a repeated category word here satisfies the corpus's `unknown local` "+
				"vectors and fails its `unknown local 2` vectors, on an analysis that was right",
				s.pos, s.name, s.format)
			continue
		}
		if !strings.HasPrefix(s.format, "%w %d") {
			literalIndex++
		}
	}
	t.Logf("%d ErrUnknown* sentinels, %d wrapping sites (%d with a literal index), all with the "+
		"index immediately after the category", len(sentinels), len(sites), literalIndex)
}

// TestStructuralOpcodesMatchTheTable checks the named constants in instr.go against
// `binary.OpMnemonic`, the authority's own transcribed table.
//
// # The domain this cannot check, stated because it is most of the value
//
// The table's `ok` means "there is a row", not "there is a name" (see `mnemonic`'s comment), and
// two of the constants here name opcodes whose rows carry an **empty** mnemonic: `else` (0x05) and
// `end` (0x0B) are sequence terminators in `decode.ml`, not named instructions. So for those two
// this control confirms only that the byte has a row — the *name* is this package's own, and no
// authority disagrees with it.
//
// One further blind spot: `select` (0x1B) and `select t` (0x1C) both render as `select` in the
// table, so a swap of those two constants would pass. What catches that swap is behavioural —
// `TestSelectAnnotatedTypesAgainstItsAnnotation` — and it is named here rather than left implied,
// because a control's domain is an assertion it cannot check about itself.
//
// That citation previously cited `TestSelectAnnotatedIsDeclinedAndBareIsNot`, and #294 rewrote the
// control the moment it typed the annotated form. The rename was caught by
// `TestEveryCitedTestNameResolves` rather than by anyone remembering this sentence — which is the
// blind spot the paragraph is about, one level up: a domain note is a citation, and a citation that
// no longer resolves documents a direction nothing checks.
func TestStructuralOpcodesMatchTheTable(t *testing.T) {
	// name → opcode, as instr.go's switch reads them. Written out rather than reflected because
	// they are untyped constants: there is nothing at run time to enumerate.
	cases := []struct {
		want string
		op   uint32
	}{
		{"unreachable", opUnreachable},
		{"nop", opNop},
		{"block", opBlock},
		{"loop", opLoop},
		// `if_`, with the underscore, is the authority's own spelling: `if` is an OCaml keyword,
		// so `decode.ml:371` escapes it and `optable.go:77` transcribes the escape. Found by this
		// control on its first run, expecting "if" — the table is right and the expectation was a
		// guess about the reference dressed as a fact about it.
		{"if_", opIf},
		{"br", opBr},
		{"br_if", opBrIf},
		{"br_table", opBrTable},
		{"return", opReturn},
		{"call", opCall},
		{"call_indirect", opCallIndirect},
		{"drop", opDrop},
		{"select", opSelect},
		{"select", opSelectT}, // both render `select`; see the doc comment's domain note
		{"local_get", opLocalGet},
		{"local_set", opLocalSet},
		{"local_tee", opLocalTee},
		{"global_get", opGlobalGet},
		{"global_set", opGlobalSet},
	}
	for _, c := range cases {
		name, ok := binary.OpMnemonic(c.op)
		if !ok {
			t.Errorf("opcode %#02x (this package calls it %q) has no row in binary.OpMnemonic — "+
				"a structural constant naming a byte the authority's table does not have",
				c.op, c.want)
			continue
		}
		if name != c.want {
			t.Errorf("opcode %#02x is %q in the authority's table, this package's constant is "+
				"named for %q", c.op, name, c.want)
		}
	}

	// The two whose rows are nameless, asserted as nameless. If upstream ever gives `end` a
	// mnemonic, `mnemonic()`'s hand-spelled fallback becomes a second authority for the same
	// fact and this is where that is noticed.
	for _, c := range []struct {
		op   uint32
		name string
	}{{opElse, "else"}, {opEnd, "end"}} {
		got, ok := binary.OpMnemonic(c.op)
		if !ok {
			t.Errorf("opcode %#02x (%s) has no row at all in binary.OpMnemonic; the empty-name "+
				"case this test pins is a row *with* an empty name", c.op, c.name)
			continue
		}
		if got != "" {
			t.Errorf("opcode %#02x now has the mnemonic %q in the authority's table, where it "+
				"was nameless; mnemonic()'s hand-spelled %q is now a second authority for one "+
				"fact and should be deleted in favour of the table", c.op, got, c.name)
		}
	}
}

// TestEveryNumericOpcodeHasASignature is sig.go's promised control, and it is scoped to the
// *space* rather than to the operators that happen to be listed.
//
// The domain is derived: every single-byte opcode with a row in `binary.OpMnemonic` whose mnemonic
// begins with a numeric type prefix is, by definition, a member of the family slice 1 claims to
// type. So `signature` must return a signature for each of them — not a decline. An operator
// missing from `unaryOps`/`binaryOps`/`compareOps` falls through to `ErrUnsupported`, which is a
// visible refusal in a named bucket and therefore *not* the accept-direction hazard; but it is
// still a rule slice 1 says it implements and does not, which is a lie in the scope declaration
// rather than in the verdict.
//
// A module is needed because memory accesses read their address type from it, so this walks with a
// one-memory module — the case that makes `load`/`store` answerable at all.
func TestEveryNumericOpcodeHasASignature(t *testing.T) {
	m := &binary.Module{Memories: []binary.Memory{{}}}

	var missing []string
	checked := 0
	for op := range uint32(0x100) {
		name, ok := binary.OpMnemonic(op)
		if !ok || name == "" {
			continue
		}
		prefix, _, found := strings.Cut(name, "_")
		if !found {
			continue
		}
		if _, isNum := numType(prefix); !isNum {
			continue
		}
		checked++
		if _, err := signature(m, binary.Instr{Op: op}); err != nil {
			missing = append(missing, name)
		}
	}

	// Vacuity, and **both bounds, because a floor alone cannot see a small loss.** The loose one
	// catches the catastrophic case (a `Cut` on the wrong separator, a renamed table: the walk
	// collapses and agrees with everything); the exact one catches the case a floor is blind to,
	// which is six opcodes quietly dropping out of the derivation while 149 still pass.
	//
	// Exact is affordable here and it would not be on a board count: this domain comes from
	// `optable.go`, which is **committed**, so the figure moves when someone edits the table and
	// never on upstream's schedule. That is decision 0012's situation (both inputs in the tree →
	// exact golden), not #42's — and *the strongest control the inputs admit, at each site*.
	const (
		numericWalkFloor = 120
		numericWalkExact = 155
	)
	if checked < numericWalkFloor {
		t.Fatalf("walked only %d numeric-prefixed opcodes, want ≥%d; the domain derivation "+
			"stopped matching, so an operator class could be empty and this test would agree",
			checked, numericWalkFloor)
	}
	if checked != numericWalkExact {
		t.Errorf("walked %d numeric-prefixed opcodes, want exactly %d. The single-byte numeric "+
			"space is fixed by a committed table, so a change here is either a table edit (re-base "+
			"this constant in that PR) or the derivation losing members — and a floor cannot tell "+
			"a loss of six from a healthy board", checked, numericWalkExact)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d numeric opcodes have no signature (%v); each falls through to "+
			"ErrUnsupported, so it is declined in a named bucket rather than wrongly accepted — "+
			"but slice 1's scope claims the numeric families, and an operator missing from "+
			"unaryOps/binaryOps/compareOps makes that claim false", len(missing), missing)
	}
	t.Logf("%d numeric-prefixed opcodes, all with a derived signature", checked)
}

// TestOperatorClassesAreDisjoint is the other half of sig.go's reason for using sets: `signature`
// tests the classes in order, so an operator in two of them silently takes the first arm and its
// second classification is unreachable — the shape grave 0003 is about, one layer down.
//
// `div` is the case that makes this real rather than hypothetical: it is a float binary operator
// and the integer forms are `div_s`/`div_u`, so a well-meaning addition of `"div.s"` to
// `unaryOps` would type `i32.div_s` as unary and be caught by nothing else here.
func TestOperatorClassesAreDisjoint(t *testing.T) {
	classes := map[string]map[string]bool{
		"unaryOps":   unaryOps,
		"binaryOps":  binaryOps,
		"compareOps": compareOps,
	}
	seen := map[string]string{}
	for _, name := range sortedKeys(map[string]bool{"unaryOps": true, "binaryOps": true, "compareOps": true}) {
		for _, op := range sortedKeys(classes[name]) {
			if prev, dup := seen[op]; dup {
				t.Errorf("operator %q is in both %s and %s; signature() tests the classes in "+
					"order, so the second classification is unreachable and the operator is "+
					"typed by whichever arm comes first", op, prev, name)
				continue
			}
			seen[op] = name
		}
	}
	// Exact, for the reason the numeric walk is exact: the three classes are in this package, so
	// the population moves only when someone edits them. A floor would let a class lose four
	// operators silently, and an operator dropping out of every class is a *decline* — visible in
	// a bucket, but visible as "out of scope" for a rule slice 1 claims.
	const classPopulation = 46
	if len(seen) != classPopulation {
		t.Errorf("the three operator classes hold %d operators between them, want exactly %d "+
			"(unaryOps 13, binaryOps 19, compareOps 14). An emptied class makes this disjointness "+
			"check trivially true; a shrunken one makes it partly so", len(seen), classPopulation)
	}
}

// Generic in the value because the callers hold two different maps of the same keys — a category set
// and a sentinel-to-message map — and a second copy of this for the second value type is the kind of
// duplication that lets one copy drift.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
