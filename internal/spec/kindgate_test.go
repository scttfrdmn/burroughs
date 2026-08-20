// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
)

// # The `want.Kind != got.Kind` census (#441)
//
// `matches` gates the comparison on a static-type equality the authority does not have:
// `assert_ref_pat` (`script/runner.ml:464-476`) dispatches on the runtime **constructor** and reads
// no static type at all. Removing or narrowing that gate is an **accept-direction** change, and no
// negative-direction vector can witness a wrong acceptance — a corpus of rejections cannot say what
// a comparison wrongly admits. So the evidence has to be a census of the passing population, taken
// before any diff, which is what this file is.
//
// **This is a separate file from `spec_test.go` on purpose.** `foreclosingLicensed`
// (`internal/testenv/foreclose_test.go`) keys its entries on `<file>:<line>`, so an insertion into
// `spec_test.go` re-keys twelve of them, by different amounts depending on where the insertion
// sat — #447. A new file inserts nothing above anything.
//
// ## What the two channels are for
//
// `KindGateNumericEqual` is the bulk: every `i32 1` against `i32 1` reaches this gate. It is a
// count, and it is here for **vacuity** — a census reporting no numeric traffic has stopped walking
// rather than found nothing, and that is the failure mode a census is most likely to have.
// `KindGateReaches` enumerates everything else: any reach touching a reference kind, and any
// numeric pair whose two kinds differ.
//
// ## What changed when the gate did (ADR 0040)
//
// The gate is gone from the reference path — the families fork instead, `matches` states each one's
// own precondition, and the seven rows this file was written to enumerate now **pass**. The census's
// own vocabulary moved with it, and the renaming is not cosmetic:
//
//   - **"refused by the gate" became "the two kinds differ".** The census records a pair *at the
//     fork*, before either family answers, so what it can see is the pair and never the verdict.
//     That was true before the split too; the old label got away with it because a reference pair
//     with unequal kinds *was* refused, so the verb rode on a coincidence — the
//     defect-stated-as-the-rule shape, in an instrument's own output. What is pinned is what is
//     measured.
//   - **The pinned seven moved channel, from the fail path to the passing population.** They are
//     the reward and they keep witnessing it: a reach appears in the pass-path census only if its
//     vector passed (the run loop discards the census on the fail path), so a row's presence here
//     asserts both that the vector is green and that it got there with the two kinds unequal, which
//     is exactly the claim ADR 0040 makes — checked per row, not as a board total.

// kindGateUnequalPasses is the population ADR 0040 admits: every reach where the two kinds differ
// and the comparison succeeds anyway. Until 0040 this map was `kindGateRefusals` and the first seven
// rows were the gate's refusals, adjudicated one at a time against the reference; the keys and the
// readings are unchanged and only the channel and the verb moved.
//
// **This is an accept-direction control, which is the only kind this change can have.** No
// negative-direction vector can witness a wrong acceptance, so what stands in for one is the
// requirement that every admitted unequal-kind pair be named here with the authority that admits it.
// An unpinned row is an acceptance nobody adjudicated.
//
// **Eleven rows in three groups, and the third group is the sentinel's doing.** In the first seven
// it is the `got` side whose Kind is a stand-in: `valKind` refused the result's type and
// `fromInterpValue` substituted KindUnnameableRef, so the gate was comparing a real static type
// against a placeholder for a type the harness had already said it could not name — which is what
// made it wrong rather than merely unnecessary. The last four are the mirror image and they are here
// *because* the placeholder stopped being spelled `anyref`: their `want` side is a bare aggregate
// pattern, whose heaptype ValKind has no member for either, and their `got` side is a genuine
// `anyref`. Under one shared spelling those pairs compared **equal** and passed the gate by
// coincidence; giving the two roles different values turned four coincidental agreements into four
// admissions that had to be adjudicated. That is the census earning its keep on an edit that was not
// aimed at it.
//
//   - `try_table.wast:464-466` — `(ref.func)` type pattern against a concrete reference whose
//     constructor is `func`. `assert_ref_pat`'s `RefTypePat FuncHT` arm admits a `FuncRef`
//     constructor, so the reference says **true** where this harness said false. Exactly the defect
//     #441 describes. The results are typed `(ref null $t)` (`:428`), an indexed reference the
//     harness genuinely cannot name — ValKind has no idx axis by #270/0039's own design.
//   - `local_init.wast:21,22,23,74` — `(ref.extern N)` against a host reference carrying the same
//     identity. `assert_ref_pat` is **not** the authority here at all: `RefResult (RefPat r)`
//     compares two concrete references and neither side's static type is an operand. #441's text
//     does not mention this group; Scott's ruling on #441 is that it rides the same number anyway,
//     because *an issue's scope is set by the mechanism and not by the sentence that filed it* and
//     one edit clears all seven — measured, and the four are why the measurement was required.
//     These four are also the placeholder's *other* reason, and it is filed separately: their
//     results are typed `(ref extern)` (`:14`), which the harness declines on the **null bit
//     alone** (`binary.ExternRef` is `ref null extern`, grave #180) while `Val` carries nullity in
//     `Class` and has no nullability axis in `Kind` to lose. See #450.
//   - `extern.wast:53,54,55` and `struct.wast:122` — a bare `(ref.i31)`/`(ref.struct)`/`(ref.array)`
//     pattern against a result of static type `anyref` carrying the matching constructor.
//     `assert_ref_pat`'s heaptype arms (`runner.ml:469-472`) dispatch on that constructor and read
//     no static type, so these are admitted for the reason the whole fork exists. Note what the
//     `got` side is *not*: `externalize-ii` round-trips through `extern.convert_any` and
//     `any.convert_extern` and hands back the **unwrapped** value, so no row here is the
//     externalized-aggregate shape the reference refuses — that one has no vector and is #451.
//
// Keyed `<file>:<line> result <n>` so a row names a vector a reader can open. The names are bare
// because the vendored suite is flattened into `testdata/spec/` — `try_table.wast` is upstream's
// `test/core/exceptions/try_table.wast`, and a reader following the row upstream needs the
// proposal directory that the flattening drops.
var kindGateUnequalPasses = map[string]string{
	"try_table.wast:464 result 0": "want funcref/type-pattern/func, got unnameable-ref/concrete/func",
	"try_table.wast:465 result 0": "want funcref/type-pattern/func, got unnameable-ref/concrete/func",
	"try_table.wast:466 result 0": "want funcref/type-pattern/func, got unnameable-ref/concrete/func",
	"local_init.wast:21 result 0": "want externref/ref.extern-N/no-pattern, got unnameable-ref/ref.extern-N/host",
	"local_init.wast:22 result 0": "want externref/ref.extern-N/no-pattern, got unnameable-ref/ref.extern-N/host",
	"local_init.wast:23 result 0": "want externref/ref.extern-N/no-pattern, got unnameable-ref/ref.extern-N/host",
	"local_init.wast:74 result 0": "want externref/ref.extern-N/no-pattern, got unnameable-ref/ref.extern-N/host",
	"extern.wast:53 result 0":     "want unnameable-ref/type-pattern/i31, got anyref/concrete/i31",
	"extern.wast:54 result 0":     "want unnameable-ref/type-pattern/struct, got anyref/concrete/struct",
	"extern.wast:55 result 0":     "want unnameable-ref/type-pattern/array, got anyref/concrete/array",
	"struct.wast:122 result 0":    "want unnameable-ref/type-pattern/struct, got anyref/concrete/struct",
}

// kindGateRefReaches is the count of enumerated reaches per lane — every arrival touching a
// reference kind on either side, unequal-kind pairs included.
//
// **This is the population question 2 of #441 asks for**: the vectors whose green depends on this
// fork admitting them. The exact figure is pinned rather than floored, because a floor here would
// catch a lane that stopped running and nothing else, and the quantity that matters is a reach
// *appearing* or *changing shape* — a `(ref.any)` expectation replacing a `(ref.func)` one keeps the
// count and changes the reading.
//
// **The all-on figure went 118 → 125 with ADR 0040, and the +7 is the seven rows themselves.** They
// were absent before for a structural reason rather than a random one: their vectors failed, and the
// run loop discards the census on the fail path (`KindGateFailPrefix` is the prefix channel), so a
// row could be counted here only once it passed. The delta being exactly the pinned seven is what
// says no other row changed shape under the same edit.
//
// **It then stayed at 125 while `kindGateUnequalPasses` went 7 → 11**, which is the clearest available
// statement of what the two figures measure. This one counts *arrivals* and the sentinel changed no
// arrival — KindUnnameableRef is `isRef()`-true, so every row that touched a reference kind before
// still does. The map counts a *comparison* between two Kinds, and splitting one member into two made
// four previously-equal pairs unequal. A figure that moved and a figure that did not, under one edit,
// with a stated reason for each.
var kindGateRefReaches = map[string]int{"default": 30, "all-on": 125}

// TestKindGateCensusIsPinned prints the census and pins it.
//
// **Printed unconditionally, not only on failure.** #441 requires the census before the diff, and a
// number that exists only inside a failing assertion is a number nobody has read. `go test
// ./internal/spec/ -run TestKindGateCensusIsPinned -v` is the instrument.
func TestKindGateCensusIsPinned(t *testing.T) {
	requireSuite(t)

	_, _, allOnEngine := allOnLane(t)

	for _, lane := range []struct {
		name string
		eng  func() Engine
	}{
		{"default", engine},
		{"all-on", allOnEngine},
	} {
		numericEqual := 0
		byFile := map[string][]string{}
		refReaches, unequal, inAlt := 0, 0, 0
		var failUnequal []string
		gotUnequal := map[string]reachRow{}

		for _, f := range boardFiles(t) {
			s, err := ParseFile(filepath.Join(suiteDir, f))
			if err != nil {
				t.Errorf("%s: parse: %v", f, err)
				continue
			}
			r := s.RunGated(lane.eng())
			numericEqual += r.KindGateNumericEqual
			// **The passing population is where the unequal-kind rows live since ADR 0040**, and
			// that is a fact about the fork rather than a choice made here: an admitted pair goes on
			// to satisfy its expectation, so its vector passes and its reach survives in
			// `KindGateReaches`. Before 0040 the same seven rows could be seen only in the fail
			// prefix below, because the comparison they arrived at refused them.
			for _, k := range r.KindGateReaches {
				row := fmt.Sprintf("%d result %d: want %s, got %s", k.Line, k.Result, k.Want, k.Got)
				if k.InAlt {
					row += " [in either]"
					inAlt++
				}
				if k.Want != k.Got {
					row += " KINDS DIFFER"
					unequal++
					key := fmt.Sprintf("%s:%d result %d", f, k.Line, k.Result)
					gotUnequal[key] = reachRow{describeReach(k), reachAuthority(k)}
				}
				if k.Want.isRef() || k.Got.isRef() {
					refReaches++
				}
				byFile[f] = append(byFile[f], row)
			}
			// The fail-path prefix, which since 0040 is a **second** population rather than the
			// only observable one: a vector that fails for its own reasons may still have arrived
			// here with the two kinds unequal, and such a row is worth printing because the fork is
			// the thing that admitted it. Measured at 0 in both lanes, and the zero is now
			// contingent where it used to be analytic — before the split a refusal *caused* the
			// fail, so the passing population's zero could not have come out otherwise, which is
			// what made #441's first question unanswerable in the direction it was asked.
			for _, k := range r.KindGateFailPrefix {
				if k.Want == k.Got {
					continue // reached and let through, on the way to some other result's mismatch
				}
				key := fmt.Sprintf("%s:%d result %d", f, k.Line, k.Result)
				failUnequal = append(failUnequal,
					fmt.Sprintf("%s: %s — %s", key, describeReach(k), reachAuthority(k)))
			}
		}

		files := make([]string, 0, len(byFile))
		for f := range byFile {
			files = append(files, f)
		}
		sort.Strings(files)

		t.Logf("kind-gate census, %s lane: %d numeric-equal reaches (counted), %d enumerated "+
			"across %d file(s) — %d touching a reference kind, %d with the two kinds unequal "+
			"(admitted since ADR 0040), %d inside an `either`", lane.name, numericEqual,
			countRows(byFile), len(files), refReaches, unequal, inAlt)
		for _, f := range files {
			for _, row := range byFile[f] {
				t.Logf("  %s:%s", f, row)
			}
		}
		t.Logf("unequal-kind reaches on the FAIL path, %s lane: %d", lane.name, len(failUnequal))
		for _, row := range failUnequal {
			t.Logf("  %s", row)
		}

		// Vacuity: an empty census agrees with an empty table perfectly, and a lane that failed
		// to run is exactly that. The floor is on the numeric channel because it is the one that
		// cannot legitimately be zero — every `assert_return` of a numeric result reaches this
		// gate.
		//
		// **Routed through `boardBound` as a `vacuityBound`, not compared inline.** The first
		// draft was a `t.Fatalf` here, which `TestEveryBoardBoundIsChecked` correctly refused:
		// an inline comparison bypasses the registry, so the bound is invisible to the census
		// that keeps bounds documented and near what they bound (#87, grave #293). The cost of
		// routing is that `boardBound` reports with `Errorf` — so a vacuous lane no longer
		// truncates the run. That is the better trade here: the verdict is still FAIL, and both
		// lanes get measured instead of the first one stopping the second from being read.
		boardBound(t, "kindGateNumericFloor", numericEqual, kindGateNumericFloor, 0, vacuityBound,
			"every numeric assert_return in the corpus reaches this gate, so a figure this low "+
				"means the walk stopped rather than that the gate is quiet; the "+lane.name+
				" lane's reference-touching rows below are then agreeing about nothing")

		// The reference-touching population, pinned exactly. A floor here would tolerate the
		// reference vectors quietly draining away, which is the 6%-silent-loss case a floor
		// cannot see; the numeric channel above is floored because its exact value moves with
		// every unrelated board change and a tripwire that fires on things it has no opinion
		// about gets re-pinned rather than read.
		if want := kindGateRefReaches[lane.name]; refReaches != want {
			t.Errorf("%s lane: %d reaches touching a reference kind, pinned at %d.\n\tThe rows are "+
				"logged above. A reach appearing or disappearing is #441's own subject: it is the "+
				"population whose green depends on this gate letting it through, and the "+
				"replacement has to give every one of them the same answer the reference does.",
				lane.name, refReaches, want)
		}

		// **Both directions, and since ADR 0040 both are accept-direction claims.** An unpinned
		// row is a pair this fork started admitting on unequal kinds and nobody adjudicated —
		// which is the one failure mode no negative-direction vector can report, since a wrong
		// acceptance shows up as a *pass*. A pinned row that stops appearing is the reward
		// regressing or the vector no longer being asked; both must fail here, because a pin over a
		// vector nobody runs asserts nothing.
		//
		// The default lane pins nothing and must produce nothing: every reach there has equal
		// kinds, which is a fact about which files the gate list admits rather than a tautology —
		// the seven rows all sit in gated files.
		wantUnequal := kindGateUnequalPasses
		if lane.name == "default" {
			wantUnequal = map[string]string{}
		}
		for key, got := range gotUnequal {
			switch want, ok := wantUnequal[key]; {
			case !ok:
				t.Errorf("%s lane: unpinned unequal-kind pass at %s (%s).\n\tThe fork admitted a "+
					"pair whose two static types differ, and a wrong acceptance here scores as a "+
					"green rather than as a fail — so the row has to be adjudicated by hand and "+
					"added to kindGateUnequalPasses with its reading. The authority for this one: "+
					"%s.", lane.name, key, got.desc, got.authority)
			case want != got.desc:
				t.Errorf("%s lane: %s differs: pinned %q, got %q.\n\tThe pattern or the "+
					"constructor moved, so the adjudication recorded in kindGateUnequalPasses was "+
					"about a different comparison than the one running now.",
					lane.name, key, want, got.desc)
			}
		}
		for key := range wantUnequal {
			if _, ok := gotUnequal[key]; !ok {
				t.Errorf("%s lane: %s is pinned as an unequal-kind pass and no such reach "+
					"appeared.\n\tThese seven rows are ADR 0040's whole reward, and a reach only "+
					"survives into this channel if its vector passed — so either the fork stopped "+
					"admitting the pair (the reward regressing, and the row is back in the FAIL "+
					"prefix above) or the vector stopped being asked: gated, retired upstream, or "+
					"failing earlier.", lane.name, key)
			}
		}
	}
}

// reachRow is one unequal-kind reach as the census reports it: the pinned description and the
// derived authority clause, kept apart because only the first is pinned.
//
// **The authority is derived on every run rather than recorded in the pin**, which is the same
// separation `kindGateUnequalPasses`' own prose makes: the pin is the adjudication a human made, and
// the clause is a restatement of which arm of the reference decides the row. Pinning both would
// double-pin one fact and let the two drift, and the drift would be invisible because the clause is
// prose.
type reachRow struct {
	desc      string
	authority string
}

// describeReach spells a reach's two sides for a pin: kind, class, and the field the arm below the
// fork actually reads on each side (the pattern on the `want` side, the constructor on the `got`).
func describeReach(k kindGateReach) string {
	return fmt.Sprintf("want %s/%s/%s, got %s/%s/%s",
		k.Want, refClassName(k.WantClass), k.WantPat,
		k.Got, refClassName(k.GotClass), k.GotPayload)
}

// reachAuthority names the arm of the reference that decides a reach — **only where that authority
// applies**.
//
// `assert_ref_pat` dispatches a *pattern* against a constructor, so quoting it for a `(ref.extern N)`
// expectation would assert a dispatch that never happens. The first draft of the census quoted it at
// all seven rows and that is what this switch was extracted from: a row is testimony, and a row
// naming the wrong arm sends its reader to adjudicate against a function that has no say.
func reachAuthority(k kindGateReach) string {
	switch k.WantClass {
	case RefTypePattern:
		return fmt.Sprintf("assert_ref_pat dispatches %s against the %s constructor",
			k.WantPat, k.GotPayload)
	case RefExternIdentity:
		return "not assert_ref_pat's subject: `RefResult (RefPat r)` compares two concrete " +
			"references by identity, and neither side's static type is an operand"
	case RefLiteralNull, RefNone, RefConcrete:
		return "reached the fork as a " + refClassName(k.WantClass) +
			" expectation, whose arm reads no Kind at all"
	}
	return "no authority clause for " + refClassName(k.WantClass)
}

// refClassName spells a RefClass for a census row.
//
// **A switch and not a map, and in the test file and not beside the type.** A switch is what
// `exhaustive` can check, so a RefClass member added tomorrow fails the build here rather than
// printing a bare integer into a row someone is meant to adjudicate by hand — the same
// requirement #270's four coverage controls answer, bought from the linter instead of from a
// sentinel, because RefClass has no in-block sentinel to derive a domain from. It lives here
// because a `String()` on the type would have no production caller and the deadcode gate's
// allowlist is empty by policy (#270 paid for that lesson with `HostRef`).
func refClassName(c RefClass) string {
	switch c {
	case RefNone:
		return "not-a-ref"
	case RefLiteralNull:
		return "ref.null"
	case RefExternIdentity:
		return "ref.extern-N"
	case RefTypePattern:
		return "type-pattern"
	case RefConcrete:
		return "concrete"
	}
	return fmt.Sprintf("RefClass(%d)", byte(c))
}

// TestCrossKindNumericComparisonsAreRefused pins the half of #441's gate that must **survive** any
// replacement, and it exists because the corpus cannot say so.
//
// The census above answers #441's first two questions. What it cannot answer is which *part* of the
// gate is load-bearing, and the board says nothing about it: deleting `want.Kind != got.Kind`
// outright and replacing it with a family split that keeps it on the numeric path produce **the same
// board in both lanes** — 60928/0 default, 65049/31 all-on, identical to the row. So the corpus has
// no vector that distinguishes a correct replacement from a reckless one, which is #441's own thesis
// arriving on the question of what to do about #441.
//
// The distinguisher is these five pairs, hand-built because nothing in `testdata/spec` asserts an
// `i32` result against an `i64` expectation — vectors are written by people who know the types.
// Measured with the gate deleted entirely, the first four answer **true**: `matches` falls through
// to `want.Bits == got.Bits`, which compares 64 raw bits with no kind in the question, so `i32 1`
// satisfies `i64 1` and `i32 0` satisfies `f32 0`. The v128 row is the same defect one step worse —
// `want.Kind == KindV128` is false when *want* is the scalar, so a 128-bit result is compared
// against a 32-bit expectation by its low word.
//
// **Pre-registered before the change rather than added with it.** A control written in the PR that
// makes the change it guards has never been watched disagreeing with that change's alternative; this
// one has, and the figures above are what it was watched on. The fifth row is here for the opposite
// reason: it already passes under every option, because the three reference arms each refuse a
// numeric `got` for their own reason (`PayloadNone` is admitted by no pattern, `RefNone` is not
// `RefExternIdentity`). It is pinned so that a *new* `RefPat` or `RefPayload` member cannot quietly
// make a reference expectation satisfiable by an integer.
func TestCrossKindNumericComparisonsAreRefused(t *testing.T) {
	for _, c := range []struct {
		name      string
		want, got Val
	}{
		{"i32 1 against an i64 1", Val{Kind: KindI32, Bits: 1}, Val{Kind: KindI64, Bits: 1}},
		{"i32 0 against an f32 0", Val{Kind: KindI32, Bits: 0}, Val{Kind: KindF32, Bits: 0}},
		{"i64 1 against an f64 1", Val{Kind: KindI64, Bits: 1}, Val{Kind: KindF64, Bits: 1}},
		{"i32 1 against a v128 whose low word is 1", Val{Kind: KindI32, Bits: 1}, Val{Kind: KindV128, Bits: 1}},
		{"a (ref.func) pattern against an i32 0", Val{Kind: KindFuncRef, Class: RefTypePattern, Pat: PatFunc}, Val{Kind: KindI32, Bits: 0}},
	} {
		if c.want.Matches(c.got) {
			t.Errorf("%s matched: a %s expectation was satisfied by a %s result.\n\tNo vector in "+
				"the corpus asserts this pair, so the board cannot see it — that is why this test "+
				"is hand-written. Whatever replaced #441's Kind gate on the reference path, the "+
				"numeric path still needs a kind in its question: `want.Bits == got.Bits` compares "+
				"64 raw bits and a NaN class reads `got.Kind.width()`.",
				c.name, c.want.Kind, c.got.Kind)
		}
	}
}

func countRows(m map[string][]string) int {
	n := 0
	for _, rows := range m {
		n += len(rows)
	}
	return n
}

// kindGateNumericFloor bounds the numeric channel, whose job is vacuity: a census reporting no
// numeric traffic has stopped walking rather than found a quiet gate.
//
// A floor and not an exact pin, and the asymmetry is deliberate rather than a weakening: this
// figure moves with *every* unrelated change to what the board asks — a vector admitted, a gate
// flipped, a bucket drained — so an exact pin would be a tripwire firing on things it has no
// opinion about, and a tripwire that fires for unrelated reasons gets re-pinned rather than read.
// The two figures that *are* pinned exactly are the ones with a subject: kindGateRefReaches and
// kindGateRefusals. That makes it a `vacuityBound`, whose looseness is its function and which
// `boardBound` therefore never slack-checks.
//
// **But `vacuityBound` exempts the staleness check, not the requirement to sit near what it
// bounds**, and the first draft of this comment got that backwards: it read the exemption as
// licence and proposed reverting 140000 to an original 10000. 10000 against 143256 is the
// unasserted-distance defect exactly — a bound an order of magnitude away runs, agrees, and would
// watch 90% of the corpus's comparisons disappear without a word. Every sibling vacuity floor in
// this package sits within about 5% of its measured figure (`totalFloor` 2000/2143,
// `i32SpellingFloor` 2400/2531, `agreementFloor` 6200/6498), so tracking the measurement *is* the
// convention here and the exemption is only about slack.
//
// A floor this close to its actual is safe for a reason worth stating, because it is what makes
// the two rules compatible: **staleness on a floor accrues in the direction the corpus does not
// move it.** Upstream adding vectors — the churn the un-pinned fetch (#42) makes routine — pushes
// the actual *up*, away from the floor, where a floor tolerates it silently and forever. The only
// movement that can reach a floor from above is the corpus shrinking, which is the event this
// bound exists to report. So the headroom a loose floor buys is headroom against nothing.
const kindGateNumericFloor = 140000 // measured 143256 default, 145613 all-on
