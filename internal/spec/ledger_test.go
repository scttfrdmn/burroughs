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
			want: tally{total: 2574, pass: 906, declined: 1056, accepted: 142, mismatch: 10, gated: 460},
			why: "the destination split IS the engine's contribution: 906 passes is the reward, " +
				"1056 named declines are the next slices' work plan, 142 admissions are the " +
				"accept-direction stratum, 10 are wrong-message on a right refusal, and 460 " +
				"never reached the validator at all",
		},
		{
			name: "arrived with the arm (2 files) — corpus admission, not verdicts earned",
			got:  fresh,
			want: tally{total: 123, pass: 117, declined: 3, accepted: 0, mismatch: 0, gated: 3},
			why: "row 1 of #291's forecast, measured: these 123 were in no column before the arm, " +
				"so their 117 passes must never be quoted as conversion",
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
	// `validateFailCeiling` is 1201 and is computed by the board from the stratum field;
	// declined + accepted here is computed from bucket-key prefixes over both file groups. The
	// two agreeing is the identity that made the PR's original "1201" wrong in a subtler way
	// than the 544: the ceiling covers *both* groups, so quoting it beside a conversion figure
	// restricted to one of them mixed populations a second time.
	if got := already.declined + fresh.declined + already.accepted + fresh.accepted; got != 1201 {
		t.Errorf("validate-stratum assert_invalid fails = %d, want 1201 to match "+
			"validateFailCeiling (1059 declined + 142 admitted). The ceiling is derived from the "+
			"stratum field and this from the bucket keys; they must agree or one of them is "+
			"describing a population the other is not", got)
	}
	if got := already.gated + fresh.gated; got != 463 {
		t.Errorf("gated assert_invalid = %d, want 463 to match the unsupportedCeiling ledger's "+
			"own split of the 2574 into 463 gated + 2111 verdicts", got)
	}
	if got := already.pass + fresh.pass; got != 1023 {
		t.Errorf("assert_invalid passes = %d, want 1023 to match passFloor's account, which "+
			"names 18 of the 1023 as answered from above the validator", got)
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
