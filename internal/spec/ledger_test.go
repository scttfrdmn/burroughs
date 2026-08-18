// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The destination ledger for the `assert_invalid` population, and it exists because a total was
// reported where a ledger was required.
//
// # A total is not a ledger
//
// PR #295's Board section quoted `unsupported 2657 → 83, −2574` and called the movement "engine
// capability, not a harness widening", then accounted for the 2574 as "829 conversions + 1201
// validate stratum". **That leaves 544 vectors unaccounted, and it conflates two facts the
// forecast had deliberately separated** (ruling: Scott, PR #295):
//
//   - The **column movement is the arm.** `classify`'s `assert_invalid` arm is what makes the
//     command askable, and it would have moved the column by very nearly the same 2574 with
//     nothing behind it — the vectors would simply have landed in the admission bucket instead.
//     That is a harness widening, and it is not the engine's reward.
//   - The **engine's contribution is the destination split**: how many of the 2574 arrived as
//     *passes* rather than in the validate stratum. That is a different number, and one figure
//     cannot carry both.
//
// The 829 was worse than incomplete: it is a figure over a *restricted subject* — board-visible
// `type mismatch` conversions only — dropped into an accounting identity over the whole
// population. 906 of the 2574 pass; 829 of those 906 expect `type mismatch` and 77 expect
// something else. Mixing a restricted figure into a total is how the 544 became invisible.
//
// So the ledger is executable rather than quoted, for the reason #282's fix is: a total of 2574
// would have agreed with the wrong sentence, and the *distribution* is the part that disagrees.
// Every destination is pinned exactly and the sum is asserted to close to the vector — the sum
// being a **checksum on the ledger, not a claim in its own right**.
//
// # Why every row is exact and none is a floor
//
// Both inputs are committed: the corpus is fetched at a pin (#42) and the arm is in this tree.
// A ceiling on the total would let a decline convert into an admission with no delta, and a
// floor on the pass column would let 900 passes and 6 regressions read as green — which is the
// compensation the ≤896 forecast bound actually suffered (four modules over their own upper
// bound, one 45 under, netting to a comfortable −39). *Errors of opposite sign cancel into a
// plausible total*, and that is the whole reason this is a per-destination assertion.
//
// # Slice 2 (#305) re-bases four of the five rows, and the fifth is the one worth reading
//
// The 0xFD region's typing moved 648 vectors from `declined` to `pass` and 20 the other way:
// **1554 pass (+648) / 388 declined (−668) / 158 admitted (+16) / 14 mismatch (+4) / 460 gated
// (unmoved)**. `gated` holding still is the row that says the delta is engine capability and not a
// harness widening — nothing here became *askable*, 648 things became answerable.
//
// **And this ledger is what caught the board's own bound mis-measuring.** The 4 that arrived in
// `mismatch` are validate-stratum failures, and `validateAdmitCeiling` was computed as
// `validateFail − validateDeclined`, so it had absorbed them into the accept-direction figure: 162
// against a true 158. Two mechanisms over one population disagreed by exactly the size of a
// population one of them could not see — the cross-check below is the instrument that reported it,
// and the fix is a third counter on the board rather than a wider tolerance here.
//
// # `declined` reads zero in both groups (#359), and the empty column is a milestone, not a fault
//
// **The vocabulary question is closed.** The validator has a rule for every instruction the
// `assert_invalid` corpus asks it about, so nothing left in this table is an untyped instruction:
// what remains is 31 admissions, 10 wrong-message rows and 465 gated, and every one of those is a
// question of the rules being *right* rather than of their existing. The campaign that this column
// was the work plan for is over; the campaign it hands off to is correctness.
//
// Stated in the header rather than only in the row below, because a reader who arrives later finds
// `declined: 0` pinned twice and the readiest explanation for an empty column is a broken counter.
// It is not broken, and two facts show it: the tally is still accumulated on every walk, and the
// board's *remaining* declines are pinned as this ledger's own complement (`stratumOther == 8`,
// the non-`assert_invalid` kinds — #341's eight relaxed-SIMD operators). A zero here is a fact
// about this population, not an absence of measurement over it.
//
// **The direction of read inverts, which is the practical consequence.** While the column was
// populated it estimated reward: a number to be driven down, and its size was the next slice's
// expected yield. At zero it becomes a *regression detector*, where nonzero no longer means "work
// remaining" but "an instruction lost its rule, or the corpus grew one this validator has never
// seen". Same counter, opposite inference, so the inference is written down.
func TestAssertInvalidDestinationLedgerCloses(t *testing.T) {
	requireSuite(t)

	// The two files this PR admitted to the board. They are separated from the rest because
	// their 123 commands **arrive** rather than convert: they were in no column at all before
	// the arm landed, so folding them into the 2574 would credit corpus admission as verdicts
	// earned — row 1 and row 2 of #291's forecast, kept apart for the reason they were
	// forecast apart.
	arrived := map[string]bool{"memory_size3.wast": true, "unreached-invalid.wast": true}

	type tally struct {
		total, pass, declined, accepted, mismatch, gated, precondition int
		// unsupHead is reported and deliberately **not** part of the identity; see below.
		unsupHead int
	}
	var already, fresh tally
	perArrived := map[string]tally{}

	// The *flags* path's tallies, accumulated in the same walk and split by whether the command is an
	// `assert_invalid` — the second half of the identity below, and the half that had rotted.
	//
	// `Failure.Declined`/`.Accepted` are form-blind and Kind-blind: they are what the board's stratum
	// figures are computed from, over every command kind. The rows above are computed from bucket-key
	// prefixes over `assert_invalid` heads only. Two paths, one population **if and only if** every
	// decline and admission on the board belongs to an `assert_invalid`, and that stopped being true
	// at #341. Splitting them here is what makes the difference a checked quantity.
	var stratumAI, stratumOther tally

	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		// **The domain is all three `assert_invalid` forms, via the Kind's own predicate.** It was
		// `c.Kind == KindAssertInvalid` and that was this ledger's subject exactly — until the
		// 17-head slice gave two more forms their own Kinds, at which point the equality test
		// became a *sample* of the subject and the rows below held still while the population grew
		// by 17. The predicate is the fix and the second mechanism that pins it is in
		// TestAssertInvalidKindsAreExactlyTheAssertInvalidForms; see `Kind.isAssertInvalid`.
		aiLines := map[int]bool{}
		for _, c := range s.Commands {
			if c.Kind.isAssertInvalid() {
				aiLines[c.Line] = true
			}
		}

		// `run`, which is `RunGated(engine())` — the board's own entry point. A hand-rolled
		// pipeline here would measure a lookalike, and the destinations are precisely the arm's
		// four outcomes, so they have to come from the arm.
		//
		// Every board file is run, including the ones with no classified `assert_invalid` at all.
		// **An earlier draft skipped those, and the skip cost it the residual**: it reported 14
		// unsupported heads where the board has 17, because three files contain *only* heads
		// `classify` declined and so were `continue`d past before the head census ran. A census
		// scoped to the population it is meant to be the complement of inherits that population's
		// blind spot.
		r := run(s)

		var tl tally
		tl.total = len(aiLines)
		for _, ln := range r.GatedAt {
			if aiLines[ln] {
				tl.gated++
			}
		}
		for key, fs := range r.Buckets {
			for _, fl := range fs {
				// The flags path, before the restriction below rather than in a second walk, so both
				// paths read the same `run` over the same files. A separate loop would be two
				// measurements of two things that happen to be spelled the same way.
				st := &stratumOther
				if fl.Kind.isAssertInvalid() {
					st = &stratumAI
				}
				if fl.Declined {
					st.declined++
				}
				if fl.Accepted {
					st.accepted++
				}
				if !fl.Kind.isAssertInvalid() {
					continue
				}
				// The key is `<Kind.String()> <marker><message>`, so the form is stripped by the
				// Kind that wrote it rather than by matching each form's literal. A literal list
				// here would be a second place knowing the key format, and its failure mode is
				// the worst available: an unstripped form falls into `default` and reports "the
				// arm grew an outcome" when the arm merely has a form this reader never learned.
				marker := strings.TrimPrefix(key, fl.Kind.String())
				switch {
				case strings.HasPrefix(marker, " declined:"):
					tl.declined++
				case strings.HasPrefix(marker, " accepted, expected:"):
					tl.accepted++
				case strings.HasPrefix(marker, " expected:"):
					tl.mismatch++
				case strings.HasPrefix(marker, " must decode"), strings.HasPrefix(marker, " must assemble"),
					strings.HasPrefix(marker, " two decode paths disagree"):
					// **The precondition destinations, named and pinned at zero rather than left
					// to `default`.** These are the 17-head slice's own reason for existing: a
					// `(module binary …)` whose image does not decode, or a `(module quote …)`
					// whose source does not assemble, is a defect in the *decoder* or the
					// *assembler* and never a validation verdict. Pinned at 0 in both rows below,
					// which is what makes them falsifiable — caught by `default` they would be
					// reported as an unknown outcome, which is true but says nothing about how
					// many, and a total is not a ledger.
					tl.precondition++
				default:
					// A fifth bucket shape means the arm grew an outcome this ledger does not
					// know about, which is exactly the drift a closed identity is for.
					t.Errorf("%s: assert_invalid failure in unrecognized bucket %q — the arm has "+
						"an outcome this ledger does not account for", f, key)
				}
			}
		}
		// **Head-keyed unsupported is reported beside the identity and never inside it, and the
		// first draft got that wrong.** An `assert_invalid` *head* that is still unsupported is a
		// command `classify` gave some other Kind — when this was written, the 17 `(module
		// binary …)`/`(module quote …)` forms, which now have Kinds of their own, leaving the
		// population empty but the arithmetic exactly as load-bearing — so it never entered
		// `total`, and subtracting it produced **negative residuals**
		// in three files. Two populations that share a name and are not the same set; the
		// negative pass count is what said so, which is why the residual is checked for sign
		// rather than assumed.
		for head, n := range r.UnsupportedByHead {
			if head == "assert_invalid" {
				tl.unsupHead += n
			}
		}
		tl.pass = tl.total - tl.gated - tl.declined - tl.mismatch - tl.accepted - tl.precondition
		if tl.pass < 0 {
			t.Errorf("%s: pass residual is %d, which is impossible: the four fail/gate "+
				"destinations claim more commands than the file has assert_invalid vectors "+
				"(%+v). Two populations are being conflated", f, tl.pass, tl)
		}

		dst := &already
		if arrived[f] {
			dst = &fresh
			perArrived[f] = tl
		}
		dst.total += tl.total
		dst.pass += tl.pass
		dst.declined += tl.declined
		dst.accepted += tl.accepted
		dst.mismatch += tl.mismatch
		dst.gated += tl.gated
		dst.precondition += tl.precondition
		dst.unsupHead += tl.unsupHead
	}

	// The ledger, pinned per destination. Figures measured at this commit; every one of them is
	// quoted in the PR's Board section and on #291, so a drift here is a drift in a published
	// account and not merely in a constant.
	for _, c := range []struct {
		name string
		got  tally
		want tally
		why  string
	}{
		{
			name: "already on the board (254 files) — the 2591 that left `unsupported`",
			got:  already,
			want: tally{total: 2591, pass: 2091, declined: 0, accepted: 28, mismatch: 10, gated: 462, precondition: 0},
			why: "the destination split IS the engine's contribution: 1615 passes is the reward, " +
				"386 named declines are the next slices' work plan, 103 admissions are the " +
				"accept-direction stratum, 10 are wrong-message on a right refusal, and 460 " +
				"never reached the validator at all. Slice 3 (#306) moved 58 of these rows in one " +
				"motion and the shape of the move is the interesting part: 54 out of `accepted` " +
				"and 4 out of `mismatch`, both into `pass`, with `declined` and `gated` untouched. " +
				"Two destinations draining into a third is a rule landing; had `declined` moved " +
				"too, some of the reward would have been vocabulary rather than correctness. " +
				"`select t` (#294) landed in the same PR and moved 2 the other way — out of " +
				"`declined` and into `pass`, with `accepted` and `mismatch` untouched — so this " +
				"row's two deltas are disjoint sub-populations of one cause, and the pair is the " +
				"evidence neither is a reclassification: a vocabulary gain and a correctness gain " +
				"cannot both be the same vectors moving twice. #310 moved one more row after those, " +
				"and it came out of `accepted` into `pass` with `declined`, `mismatch` and `gated` " +
				"all untouched — `align.wast:1004`, the one vector expecting `offset out of range` " +
				"that reaches this package. A single vector necessarily shows as −1/+1, which is the " +
				"shape this row warns can disguise a reclassification, so the discriminator is stated " +
				"rather than left to the arithmetic: the rule that moved it refuses a module the " +
				"validator used to accept, so the gain is correctness and not vocabulary, and a " +
				"vocabulary gain would have had to come out of `declined`. " +
				"**Slice 5 moved 356 of these rows and it is the first entry here whose delta " +
				"comes out of `declined` alone at scale** — `declined` 386 → 30, `pass` 1615 → " +
				"1971, with `accepted`, `mismatch` and `gated` all unmoved. That is a *vocabulary* " +
				"gain in this row's own vocabulary: 356 rules became known, none became right, " +
				"which is exactly what a slice is and exactly what a rule *fix* is not. The shape " +
				"is the discriminator the paragraph above asks for, and here it is load-bearing " +
				"in the other direction: had `accepted` moved down too, some of the reward would " +
				"have been the validator ceasing to admit modules it should always have refused, " +
				"and that would belong to a different account than a slice's. " +
				"**The 17-head slice is the first entry whose delta comes out of no destination at " +
				"all**, and the row's own instruction — say which destination the delta came *from* — " +
				"is what makes that worth stating rather than eliding: `total` rose 2574 → 2591, so " +
				"the 17 `(module binary …)` and `(module quote …)` heads are commands `classify` " +
				"previously gave `KindUnsupported` and now gives an `assert_invalid` Kind. They were " +
				"in the residual below, not in any row here. The distinction is the whole reason this " +
				"ledger keeps the residual as a checked complement rather than a log line: a " +
				"conversion trades between two rows and leaves `total` fixed, a *widening* raises " +
				"`total` and lowers the residual by the same 17, and the two shapes are only " +
				"distinguishable because both quantities are pinned. The 17 split 7 pass / 8 admitted " +
				"/ 2 gated, and the 8 are the honest half of the reward: an `assert_invalid` the " +
				"validator accepts is a rule this package does not have, so the slice's figure is 7 " +
				"verdicts earned and 8 rules named as owed. Recorded here rather than folded into the " +
				"pass count, because `accepted` rising is the accept-direction stratum growing, which " +
				"in every other row of this table would read as a regression. **The module-level slice is " +
				"the mirror of slice 5, and the pair is why this table records shapes and not only " +
				"deltas**: `accepted` 111 → 81, `pass` 1978 → 2008, with `declined`, `mismatch`, " +
				"`gated` and `total` all unmoved. A delta out of `accepted` alone is a *correctness* " +
				"gain in this row's own vocabulary — 30 rules became right and none became known — " +
				"where slice 5's equally sized move out of `declined` alone was 356 becoming known " +
				"and none becoming right. Two slices, two single-destination conversions, opposite " +
				"destinations, and this ledger tells them apart without being told which is which. It " +
				"also closes the 8 the sentence above named as owed: 8 of these 30 are those 8, so the " +
				"17-head slice's 7-earned-8-owed settled at 15 verdicts across two PRs. " +
				"**The export slice is the second single-destination correctness gain and the larger " +
				"one**: `accepted` 81 → 50, `pass` 2008 → 2039, again with `declined`, `mismatch`, " +
				"`gated` and `total` all unmoved. Same shape as the entry above and worth keeping as a " +
				"separate entry rather than merging the two, because the pair now says something " +
				"neither says alone — two consecutive slices in the *same* direction means the " +
				"remaining admissions are a work plan rather than a floor, and the census names what " +
				"is left. 19 of the 31 are one rule (`check_names`), which is the largest single-rule " +
				"reward this row has recorded. **The `check_elem` slice is the third in that same " +
				"direction**: `accepted` 50 → 43, `pass` 2039 → 2046, with `declined`, `mismatch`, " +
				"`gated` and `total` unmoved again. Three consecutive single-destination correctness " +
				"gains settles the reading the entry above offered tentatively — the admissions are a " +
				"work plan. This is also the first entry whose rows were **named individually before the " +
				"rule was written**, so the delta is attributed rather than merely sized, and that " +
				"distinction is this row's own blind spot: it can see that seven left `accepted` and " +
				"cannot see *which* seven, because the bucket key is a message and `unknown table` is a " +
				"message several reference rules produce. The row list is in " +
				"`internal/validate/elem_test.go`; without it, a rule firing on the wrong segments " +
				"delivers this exact arithmetic. " +
				"**The `check_global` slice is the fourth in that direction and the first whose rule is " +
				"one reference line**: `accepted` 43 → 31, `pass` 2046 → 2058, with `declined`, " +
				"`mismatch`, `gated` and `total` unmoved. Four consecutive conversions out of a single " +
				"destination is a pattern rather than a reading now, and this entry is where the pattern " +
				"stops being only about size: the twelve are `is_const`'s GlobalGet arm, and that one " +
				"arm emits **two** messages, so the pre-registered row list splits them 5 `constant " +
				"expression required` and 7 `unknown global` — disjoint populations of one line. That " +
				"split is what answers the blind spot the entry above names. This row still cannot see " +
				"*which* twelve, the bucket key being a message; but a rule firing on the wrong " +
				"expressions could not deliver both sub-counts, because a mutable-global check and an " +
				"index-resolution check have no vectors in common. Two figures pinned separately are " +
				"harder to hit by accident than one, and that is the only defence this row has against " +
				"arithmetic that looks right. " +
				"**The reference-type slice (#359) is the second single-destination *vocabulary* gain and " +
				"it drains the column to zero**: `declined` 30 \u2192 0, `pass` 2058 \u2192 2088, with " +
				"`accepted`, `mismatch`, `gated` and `total` unmoved. Slice 5's shape at a fiftieth of the " +
				"scale \u2014 30 rules became known, none became right \u2014 and the part worth reading is " +
				"not the 30 but the 0. This row's own header calls `declined` \"the next slices' work " +
				"plan\", and that sentence has lost its subject: with both groups' declined columns empty, " +
				"the remaining non-pass destinations are 31 admissions, 10 board-wide wrong-message rows and " +
				"465 gated, none of which names an untyped instruction. The work plan for the rest of #9 is " +
				"therefore no longer readable in this ledger \u2014 it is readable in `accepted` (the accept " +
				"direction) and in the board's *other* command kinds, where the 8 remaining declines all " +
				"live. **A column that reaches zero stops being an instrument**, and this entry says where " +
				"the subject went rather than only that the row moved. The zero is a campaign milestone and " +
				"not merely a navigation change — with no instruction in this population left untyped, " +
				"everything remaining here is correctness — and the header states what that means for " +
				"reading the column, since an empty one invites the theory that the counter broke. " +
				"**Slice 9 (ADR 0035) moves this row by one, out of `accepted` and into `pass`, and the " +
				"delta is a grave rather than a slice**: `accepted` 31 \u2192 30, `pass` 2088 \u2192 2089, " +
				"with `declined`, `mismatch`, `gated` and `total` unmoved. The slice's own two opcodes are " +
				"gated off on this lane and contribute nothing here; the row is `call_indirect.wast:994`, " +
				"which grave #390's repair converts — `valid.ml:563`'s element-type require, missing from " +
				"a `call_indirect` arm that had already been corrected once on its *address* type. So this " +
				"is the fifth single-destination correctness gain in this ledger and the smallest, and its " +
				"provenance is the part worth keeping: it was found by porting the sibling opcode, not by " +
				"auditing the arm. **A −1/+1 is the shape this row's own instruction warns can disguise a " +
				"reclassification**, so the discriminator is stated: the repair makes the validator *refuse* " +
				"a module it was accepting, which is correctness, and a vocabulary gain would have had to " +
				"come out of `declined` — which is empty and stayed empty. " +
				"**Slice 10 (ADR 0036) moves it by two in the same direction and the same shape**: " +
				"`accepted` 30 → 28, `pass` 2089 → 2091, with `declined`, `mismatch`, `gated` and " +
				"`total` unmoved. Sixth single-destination correctness gain, and for the second slice " +
				"running the subject is not the slice: exception handling is gated off on this lane, so " +
				"the rows are #391's — `check_elem` resolving the function indices a segment's " +
				"`ref.func` initialisers name, which `check_const` alone never did (`valid.ml:1097-1101`). " +
				"That is what makes this row's instruction earn its keep rather than merely being obeyed: " +
				"the slice is 29 rows in the all-gates-on lane and 0 here, so a reader given `+2` and no " +
				"destination would attribute it to exception handling and be wrong about every row. " +
				"**The difference from the entry above is that this +2 was forecast**, from the rider's " +
				"own call site rather than from the gate map — grave #390's lesson applied before the " +
				"number existed instead of after it",
		},
		{
			name: "arrived with the arm (2 files) — corpus admission, not verdicts earned",
			got:  fresh,
			want: tally{total: 123, pass: 120, declined: 0, accepted: 0, mismatch: 0, gated: 3, precondition: 0},
			why: "row 1 of #291's forecast, measured: these 123 were in no column before the arm, " +
				"so their 119 passes must never be quoted as conversion. Slice 5 moved 2 of them, " +
				"`declined` 3 → 1 into `pass`, and they are `memory_size3.wast`'s pair — the file " +
				"whose every vector is an `assert_invalid` about `memory.size`, and therefore the " +
				"one this slice's *widening* converts rather than its region. A row of 123 moving " +
				"by 2 is small enough to read as noise, which is why the file is named: this " +
				"population has no gated members left to drain and no admissions at all, so any " +
				"movement in it is a verdict changing. The 17-head slice moved this row by nothing, " +
				"and the zero is a fact about the corpus rather than about the slice: all 17 widened " +
				"heads live in files the arm already covered, so none of them landed here. Stated " +
				"because an unmoved row beside a moved one invites the reading that it was missed. " +
				"**The reference-type slice moved the last one**: `declined` 1 \u2192 0, `pass` 119 \u2192 " +
				"120, everything else unmoved, and the vector is named because a row of 123 moving by one is " +
				"otherwise unreadable \u2014 `unreached-invalid.wast:746`, the `$meet-bottom` module whose " +
				"`br_table` meets an `externref` label against an i32 one and whose operand is `(ref.null " +
				"extern)`. It declined on `ref_null` and now refuses with the `type mismatch` the corpus " +
				"expects. Worth being exact about what that does *not* demonstrate: the expectation is the " +
				"generic `type mismatch`, so this vector would also have passed had `ref.null` invented " +
				"`funcref` instead of reading its heaptype \u2014 `funcref` against an i32 label mismatches " +
				"too. The discriminator for that is in `internal/validate/ref_test.go`, not here, and " +
				"claiming it here would be a corpus row taking credit for a control it cannot run",
		},
	} {
		if c.got.total != c.want.total {
			t.Errorf("%s: total = %d, want %d — the population itself moved, so every row below "+
				"is measured against a different subject than the one pinned", c.name, c.got.total, c.want.total)
		}
		for _, row := range []struct {
			label    string
			got, exp int
		}{
			{"pass", c.got.pass, c.want.pass},
			{"declined", c.got.declined, c.want.declined},
			{"accepted (admission stratum)", c.got.accepted, c.want.accepted},
			{"mismatch (right refusal, wrong message)", c.got.mismatch, c.want.mismatch},
			{"gated", c.got.gated, c.want.gated},
			{"precondition (decode/assemble refused before validation)", c.got.precondition, c.want.precondition},
		} {
			if row.got != row.exp {
				t.Errorf("%s: %s = %d, want exactly %d.\n\t%s\n\tRe-base this row in the PR that "+
					"moves it, and say which destination the delta came *from* — a row that moves "+
					"alone is a conversion, and two rows moving in opposite directions by the same "+
					"amount is a reclassification wearing a conversion's clothes",
					c.name, row.label, row.got, row.exp, c.why)
			}
		}
		// The checksum. It cannot fail while every row above passes, and that is the point of
		// having it: it is an assertion about the *partition* — that the five destinations are
		// exhaustive and disjoint — rather than about any count. If the arm grows a sixth
		// outcome, the rows still pass and this closes short.
		sum := c.got.pass + c.got.declined + c.got.accepted + c.got.mismatch + c.got.gated + c.got.precondition
		if sum != c.got.total {
			t.Errorf("%s: the five destinations account for %d of %d commands — %d unaccounted. "+
				"Every vector arrives somewhere; a ledger that does not close names a destination "+
				"nobody is counting", c.name, sum, c.got.total, c.got.total-sum)
		}
	}

	// The cross-checks that tie this ledger to bounds asserted elsewhere, each from a different
	// path so neither vouches for itself.
	//
	// The board's `validateDeclineCeiling` + `validateAdmitCeiling` are computed from the stratum
	// field and the arm's own flags; declined + accepted here is computed from bucket-key prefixes
	// over both file groups. The two agreeing is the identity that made the slice 1 PR's original
	// "1201" wrong in a subtler way than the 544: the ceiling covers *both* groups, so quoting it
	// beside a conversion figure restricted to one of them mixed populations a second time.
	//
	// **This compared against `validateFailCeiling` — the whole stratum — and that stopped being the
	// same quantity in slice 2.** It worked while the stratum's third population was empty; slice 2
	// made it 4, and those 4 sat in `validateFailCeiling` and not in this sum, because the mismatch row
	// *here* is board-wide. Two populations sharing a bucket-key prefix and not a stratum — which is
	// exactly what the disagreement said when it fired, and the reason this identity names the two
	// sub-ceilings it can actually close against rather than the total it happened to equal.
	//
	// **Slice 3 drained the validator's 4 and the exclusion still stands**, which is the case worth
	// keeping the reasoning for: `validateMismatchCeiling` is 0 and the mismatch row below is 10, so
	// all ten now belong to other strata and the two quantities agree by *coincidence of one being
	// empty*. Restoring the total to this identity would therefore pass today and be wrong again on
	// the next stratum that refuses with the wrong message.
	//
	// **The 17-head slice put the two paths' independence to work rather than merely relying on
	// it.** The new forms write bucket keys carrying their own prefix — `assert_invalid (binary)
	// accepted, expected: …` — while the stratum figures come from `Failure.Declined`/`.Accepted`,
	// which are form-blind. So the left side of this identity had to learn a vocabulary the right
	// side cannot see, and it does that by stripping the prefix *the Kind that wrote the key*
	// supplies (`fl.Kind.String()`) rather than matching a literal. Had the classification been
	// keyed on the literal `assert_invalid`, the eight new admissions would have fallen through to
	// the unrecognized-bucket arm above — which is the failure this identity is *for*: two paths
	// disagreeing because one stopped recognizing a population, not because the engine moved.
	//
	// **And the identity itself had drifted, which is this slice's own finding about it.** It read
	// `want 62 to match validateDeclineCeiling (31) + validateAdmitCeiling (31)`, and that citation
	// was true when slice 5 wrote it. #341 then raised `validateDeclineCeiling` 31 → 55 by giving
	// `module text` commands a validator verdict — a population no `assert_invalid`-restricted
	// count can hold — so the stated derivation evaluated to 86 while the assertion compared
	// against the literal 62. It stayed green for eight merges because **the right-hand side was a
	// hand-maintained constant rather than a computed one**: the left side genuinely did not move at
	// #341, so nothing failed, and the prose explaining what the number meant was the only thing that
	// went wrong. An identity that closes against a literal is a pin wearing a cross-check's clothes;
	// the header two sections up credits this very identity with catching the board's bound
	// mis-measuring in slice 2, which is what makes the silence worth a grave (#362) rather than an
	// edit. *A self-citation must resolve like any other.*
	//
	// The repair is to compute both sides. `stratumAI` is the flags path restricted to
	// `assert_invalid`, which is the quantity the rows above are also measuring — so that
	// equality is the real two-mechanism check and the one that must hold exactly. `stratumOther` is
	// its complement, pinned rather than subtracted, and the board's own ceilings are then the sum of
	// the two. Three figures, each from a path that cannot see the others' blind spot.
	keyed := already.declined + fresh.declined + already.accepted + fresh.accepted
	if got := stratumAI.declined + stratumAI.accepted; got != keyed {
		t.Errorf("the two paths disagree over the same population: bucket keys say %d "+
			"assert_invalid declines + admissions, the arm's flags say %d. One of them has stopped "+
			"recognizing a bucket shape or a Kind; the engine moving cannot produce a disagreement "+
			"here, because both figures are counted from the same failures in the same walk", keyed, got)
	}
	if keyed != 28 {
		t.Errorf("assert_invalid declines + admissions = %d, want exactly 28 \u2014 0 declines and 28 "+
			"admissions at this commit. The declines reached zero with the reference-type slice, so "+
			"this figure is now the accept-direction stratum alone, and a nonzero declined component "+
			"returning means a slice regressed rather than that one is owed. Slice 9 (ADR 0035) took it "+
			"from 31 to 30, which is the first time a *grave* rather than a slice moved this figure: "+
			"the row is `call_indirect.wast:994` and the repair is #390's. Slice 10 (ADR 0036) took it "+
			"from 30 to 28 the same way \u2014 not its own opcodes, which are gated off on this lane, but "+
			"#391's two `(module (table funcref (elem 0 0)))` rows. Two consecutive movements sourced "+
			"from riders is why this figure is pinned rather than derived: the *slices* were 2 opcodes "+
			"and 3, and neither moved it at all", keyed)
	}
	if got := stratumOther.declined + stratumOther.accepted; got != 8 {
		t.Errorf("non-assert_invalid declines + admissions = %d, want exactly 8: the relaxed-SIMD "+
			"declines on `module text` commands, which are validate-stratum failures the ledger above "+
			"structurally cannot contain. This is the complement #341 created and the drift above "+
			"hid \u2014 pinned rather than derived by subtraction, so it is a second reading and not "+
			"the first one restated", got)
	}
	// The board's two ceilings are this sum, and stating it here is what re-ties the identity to the
	// constants it names. `validateMismatchCeiling` (0) stays deliberately outside: the mismatch row
	// below is board-wide and none of its 10 are the validator's.
	if got := keyed + stratumOther.declined + stratumOther.accepted; got != 36 {
		t.Errorf("validate-stratum declines + admissions = %d, want 36 to match "+
			"validateDeclineCeiling (8) + validateAdmitCeiling (28). Those are computed from the "+
			"stratum field; this is computed from the arm's flags over both sub-populations. A "+
			"disagreement means one path is describing a population the other is not \u2014 which is "+
			"exactly what went unreported between #341 and #359", got)
	}
	if got := already.gated + fresh.gated; got != 465 {
		t.Errorf("gated assert_invalid = %d, want 465 to match the unsupportedCeiling ledger's "+
			"own gated count, summed across both groups — note this figure's subject is "+
			"board-wide where the 2591 above is the converted group alone, so the two are cross-checks "+
			"of different populations and not two halves of one subtraction", got)
	}
	if got := already.pass + fresh.pass; got != 2211 {
		t.Errorf("assert_invalid passes = %d, want 2211 to match passFloor's account — 1023 at "+
			"slice 1, of which it names 18 as answered from above the validator, plus slice 2's 648, "+
			"slice 3's 58, #294's 2, slice 5's 358 (356 converted + 2 from the arrived group), "+
			"the 17-head slice's 7 — the only entry in this sum that raised the ledger's `total` "+
			"instead of converting inside it — the limits slice's 30, the export slice's 31, the "+
			"`check_elem` slice's 7 and the `check_global` slice's 12, and the reference-type slice's "+
			"31 (30 converted + 1 from the arrived group, the same two-group split slice 5's entry "+
			"has), and slice 9's 1 — grave #390's `call_indirect.wast:994`, the only entry in this "+
			"sum contributed by a repair to an arm the slice was not porting, and slice 10's 2 — "+
			"#391's elem rows, contributed by a rider to a *scope* slice whose own three opcodes are "+
			"gated off on this lane and add nothing here. "+
			"**That sums to 2210 against an actual 2211, and the one-vector residue is stated rather "+
			"than absorbed:** it predates the last seven slices (the account read 2096 against a "+
			"pinned 2097 before any of them), so it belongs to an entry above and not to them. Filed as "+
			"a loose end as #334; an unexplained +1 folded into a named entry would make one of these "+
			"figures a fudge and the whole account unreadable. The residue has now survived seven "+
			"re-basings unchanged, which is itself evidence for where it is not: an off-by-one in any "+
			"of the seven named deltas would have moved it", got)
	}
	// The residual, and the reason it is asserted rather than logged: it is the *complement* of
	// this ledger's subject, so it is where a command goes when `classify` stops recognizing one.
	// **It is now 0, and the zero is this slice's actual deliverable**: the 17 that stood here were
	// the `(module binary …)` and `(module quote …)` forms, and the sentence this comment used to
	// carry — that they "need a text-format assembler the arm does not have" — was falsified by
	// the arm acquiring one (`AssembleFunc`, wired to `text.EncodeModule`). Recorded rather than
	// deleted, because the prose was wrong in a specific and instructive way: `text.EncodeModule`
	// already existed and was already in use by the public-path fixtures, so the cost was never a
	// missing assembler but a missing *boundary* — a `ReadTextFunc` that could say "clean" without
	// handing back what it read.
	//
	// The zero costs this check half its informativeness, and that is worth naming rather than
	// enjoying. At 17 it moved in two directions and each meant something: a drop with a matching
	// rise in `total` was the arm widening, a rise was `classify` regressing. At 0 it can only
	// rise, so it has degraded from a two-sided identity to a one-sided tripwire — still live, and
	// still the only thing that catches `classify` handing an `assert_invalid` head back to
	// `KindUnsupported`, which the rows above would show as a *smaller population* rather than as
	// a loss. That asymmetry is the reason it stays asserted at zero instead of being dropped for
	// having nothing left to count: the direction that survives is the one that matters.
	if got := already.unsupHead + fresh.unsupHead; got != 0 {
		t.Errorf("unsupported assert_invalid heads = %d, want 0: every `assert_invalid` form the "+
			"corpus contains — bare, `(module binary …)` and `(module quote …)` — now has a Kind, "+
			"so this complement is empty and a nonzero value means `classify` stopped recognizing "+
			"one of them. These are commands classify gave a different Kind, so they sit in no row "+
			"of the ledger above; the two counts partition the corpus's assert_invalid heads "+
			"between them and must sum to it, which is why a loss here shows up as `total` "+
			"shrinking and not as a failure in any pinned row", got)
	}

	names := make([]string, 0, len(perArrived))
	for f := range perArrived {
		names = append(names, f)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, f := range names {
		tl := perArrived[f]
		fmt.Fprintf(&b, "\n\t%s: %d total → %d pass, %d declined, %d gated",
			f, tl.total, tl.pass, tl.declined, tl.gated)
	}
	// Printed from the tallies, never from the pinned constants. **The first draft spelled the
	// expected figures into this format string**, so the falsification run — which moved a
	// declined into mismatch — printed `1056 declined / 10 mismatch` above its own failure saying
	// 1055 and 11. A log that recites the constants it is meant to be reporting is the defect
	// stated as the rule: it makes the reader confirm the number by reading the assertion's other
	// copy of it.
	t.Logf("assert_invalid destination ledger: %d converted (%d pass / %d declined / %d admitted / "+
		"%d mismatch / %d gated) + %d arrived (%d pass / %d declined / %d admitted / %d mismatch / "+
		"%d gated). %d unsupported assert_invalid heads remain board-wide, in neither identity.%s",
		already.total, already.pass, already.declined, already.accepted, already.mismatch, already.gated,
		fresh.total, fresh.pass, fresh.declined, fresh.accepted, fresh.mismatch, fresh.gated,
		already.unsupHead+fresh.unsupHead, b.String())
}
