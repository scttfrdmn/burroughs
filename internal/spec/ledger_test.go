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
func TestAssertInvalidDestinationLedgerCloses(t *testing.T) {
	requireSuite(t)

	// The two files this PR admitted to the board. They are separated from the rest because
	// their 123 commands **arrive** rather than convert: they were in no column at all before
	// the arm landed, so folding them into the 2574 would credit corpus admission as verdicts
	// earned — row 1 and row 2 of #291's forecast, kept apart for the reason they were
	// forecast apart.
	arrived := map[string]bool{"memory_size3.wast": true, "unreached-invalid.wast": true}

	type tally struct {
		total, pass, declined, accepted, mismatch, gated int
		// unsupHead is reported and deliberately **not** part of the identity; see below.
		unsupHead int
	}
	var already, fresh tally
	perArrived := map[string]tally{}

	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		aiLines := map[int]bool{}
		for _, c := range s.Commands {
			if c.Kind == KindAssertInvalid {
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
				if fl.Kind != KindAssertInvalid {
					continue
				}
				switch {
				case strings.HasPrefix(key, "assert_invalid declined:"):
					tl.declined++
				case strings.HasPrefix(key, "assert_invalid accepted, expected:"):
					tl.accepted++
				case strings.HasPrefix(key, "assert_invalid expected:"):
					tl.mismatch++
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
		// command `classify` gave some other Kind — the 17 `(module binary …)`/`(module quote …)`
		// forms — so it never entered `total`, and subtracting it produced **negative residuals**
		// in three files. Two populations that share a name and are not the same set; the
		// negative pass count is what said so, which is why the residual is checked for sign
		// rather than assumed.
		for head, n := range r.UnsupportedByHead {
			if head == "assert_invalid" {
				tl.unsupHead += n
			}
		}
		tl.pass = tl.total - tl.gated - tl.declined - tl.mismatch - tl.accepted
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
			name: "already on the board (254 files) — the 2574 that left `unsupported`",
			got:  already,
			want: tally{total: 2574, pass: 1971, declined: 30, accepted: 103, mismatch: 10, gated: 460},
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
				"and that would belong to a different account than a slice's",
		},
		{
			name: "arrived with the arm (2 files) — corpus admission, not verdicts earned",
			got:  fresh,
			want: tally{total: 123, pass: 119, declined: 1, accepted: 0, mismatch: 0, gated: 3},
			why: "row 1 of #291's forecast, measured: these 123 were in no column before the arm, " +
				"so their 119 passes must never be quoted as conversion. Slice 5 moved 2 of them, " +
				"`declined` 3 → 1 into `pass`, and they are `memory_size3.wast`'s pair — the file " +
				"whose every vector is an `assert_invalid` about `memory.size`, and therefore the " +
				"one this slice's *widening* converts rather than its region. A row of 123 moving " +
				"by 2 is small enough to read as noise, which is why the file is named: this " +
				"population has no gated members left to drain and no admissions at all, so any " +
				"movement in it is a verdict changing",
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
		sum := c.got.pass + c.got.declined + c.got.accepted + c.got.mismatch + c.got.gated
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
	if got := already.declined + fresh.declined + already.accepted + fresh.accepted; got != 134 {
		t.Errorf("validate-stratum declines + admissions = %d, want 134 to match "+
			"validateDeclineCeiling (31) + validateAdmitCeiling (103). Those come from the stratum "+
			"field and the arm's flags and this from the bucket keys; they must agree or one of them "+
			"is describing a population the other is not. The stratum's third part "+
			"(validateMismatchCeiling, 0) is deliberately outside this identity: the mismatch row "+
			"below is board-wide and none of its 10 are the validator's", got)
	}
	if got := already.gated + fresh.gated; got != 463 {
		t.Errorf("gated assert_invalid = %d, want 463 to match the unsupportedCeiling ledger's "+
			"own split of the 2574 into 463 gated + 2111 verdicts", got)
	}
	if got := already.pass + fresh.pass; got != 2090 {
		t.Errorf("assert_invalid passes = %d, want 2090 to match passFloor's account — 1023 at "+
			"slice 1, of which it names 18 as answered from above the validator, plus slice 2's 648, "+
			"slice 3's 58, #294's 2, and slice 5's 358 (356 converted + 2 from the arrived group)", got)
	}
	// The residual, and the reason it is asserted rather than logged: it is the *complement* of
	// this ledger's subject, so it is where a command goes when `classify` stops recognizing one.
	// 17 is `unsupportedCeiling`'s own figure for what remains after the arm — the `(module
	// binary …)` and `(module quote …)` forms, which need a text-format assembler the arm does not
	// have. A drop here without a matching rise in `total` is an arm that grew coverage; a rise is
	// a regression in `classify` that the total alone would show as a smaller population rather
	// than as a loss.
	if got := already.unsupHead + fresh.unsupHead; got != 17 {
		t.Errorf("unsupported assert_invalid heads = %d, want 17 to match unsupportedCeiling's "+
			"residual. These are commands classify gave a different Kind, so they are in no row "+
			"of the ledger above — the two counts partition the corpus's assert_invalid heads "+
			"between them and must sum to it", got)
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
