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
// numeric pair the gate refuses.

// kindGateRefusals is the population #441 is about: every reach where this gate answered `false`,
// which — since a false here fails its vector — is a subset of the **fail** column and is empty in
// the passing one by construction.
//
// **Seven rows, and they are two different questions.** Both share one property, and it is the
// property that makes the gate wrong rather than merely unnecessary: in every one of the seven,
// `got.Kind` is `anyref` while the value's own constructor is narrower, and **the arm below the gate
// reads no static type in either group**. The gate is refusing on an operand its own authority does
// not have.
//
//   - `try_table.wast:464-466` — `(ref.func)` type pattern against a concrete reference whose
//     constructor is `func`. `assert_ref_pat`'s `RefTypePat FuncHT` arm admits a `FuncRef`
//     constructor, so the reference says **true** and this harness says false. This is exactly the
//     defect #441 describes.
//   - `local_init.wast:21,22,23,74` — `(ref.extern N)` against a host reference carrying the same
//     identity. `assert_ref_pat` is **not** the authority here at all: `RefResult (RefPat r)`
//     compares two concrete references and neither side's static type is an operand. #441's text
//     does not mention this group, and it is a second question wearing the same number — the
//     got-side Kind attribution (`fromInterpValue` reporting `anyref` for a host reference whose
//     static type is extern) is a defect in its own right, and removing the gate would make these
//     four pass by cancelling one wrong against another.
//
// Keyed `<file>:<line> result <n>` so a row names a vector a reader can open. The names are bare
// because the vendored suite is flattened into `testdata/spec/` — `try_table.wast` is upstream's
// `test/core/exceptions/try_table.wast`, and a reader following the row upstream needs the
// proposal directory that the flattening drops.
var kindGateRefusals = map[string]string{
	"try_table.wast:464 result 0": "want funcref/type-pattern/func, got anyref/concrete/func",
	"try_table.wast:465 result 0": "want funcref/type-pattern/func, got anyref/concrete/func",
	"try_table.wast:466 result 0": "want funcref/type-pattern/func, got anyref/concrete/func",
	"local_init.wast:21 result 0": "want externref/ref.extern-N/no-pattern, got anyref/ref.extern-N/host",
	"local_init.wast:22 result 0": "want externref/ref.extern-N/no-pattern, got anyref/ref.extern-N/host",
	"local_init.wast:23 result 0": "want externref/ref.extern-N/no-pattern, got anyref/ref.extern-N/host",
	"local_init.wast:74 result 0": "want externref/ref.extern-N/no-pattern, got anyref/ref.extern-N/host",
}

// kindGateRefReaches is the count of enumerated reaches per lane — every arrival touching a
// reference kind on either side, refusals included.
//
// **This is the population question 2 of #441 asks for**: the vectors whose green currently depends
// on the gate letting them through. The exact figure is pinned rather than floored, because a floor
// here would catch a lane that stopped running and nothing else, and the quantity that matters is a
// reach *appearing* or *changing shape* — a `(ref.any)` expectation replacing a `(ref.func)` one
// keeps the count and changes the reading.
var kindGateRefReaches = map[string]int{"default": 30, "all-on": 118}

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
		refReaches, refused, inAlt := 0, 0, 0
		var failRefusals []string
		gotRefusals := map[string]string{}

		for _, f := range boardFiles(t) {
			s, err := ParseFile(filepath.Join(suiteDir, f))
			if err != nil {
				t.Errorf("%s: parse: %v", f, err)
				continue
			}
			r := s.RunGated(lane.eng())
			numericEqual += r.KindGateNumericEqual
			// The fail-path prefix first, because it is the only channel in which this gate
			// can be seen refusing: a refusal fails its vector, so the passing population's
			// zero is a fact about control flow and not a measurement. See KindGateFailPrefix.
			for _, k := range r.KindGateFailPrefix {
				if k.Want == k.Got {
					continue // reached and let through, on the way to some other result's mismatch
				}
				// **The authority clause is printed only where that authority applies.**
				// `assert_ref_pat` dispatches a *pattern* against a constructor, so quoting it
				// for a `(ref.extern N)` expectation would assert a dispatch that never
				// happens — a row is testimony, and a row naming the wrong arm sends its
				// reader to adjudicate against a function that has no say. The other classes
				// get their own arm named instead.
				var authority string
				switch k.WantClass {
				case RefTypePattern:
					authority = fmt.Sprintf("assert_ref_pat dispatches %s against the %s "+
						"constructor", k.WantPat, k.GotPayload)
				case RefExternIdentity:
					authority = "not assert_ref_pat's subject: `RefResult (RefPat r)` compares " +
						"two concrete references by identity, and neither side's static type " +
						"is an operand"
				case RefLiteralNull, RefNone, RefConcrete:
					authority = "reached this gate as a " + refClassName(k.WantClass) +
						" expectation, which the arms below the gate do not read a Kind for"
				}
				key := fmt.Sprintf("%s:%d result %d", f, k.Line, k.Result)
				gotRefusals[key] = fmt.Sprintf("want %s/%s/%s, got %s/%s/%s",
					k.Want, refClassName(k.WantClass), k.WantPat,
					k.Got, refClassName(k.GotClass), k.GotPayload)
				failRefusals = append(failRefusals,
					fmt.Sprintf("%s: %s — %s", key, gotRefusals[key], authority))
			}
			for _, k := range r.KindGateReaches {
				row := fmt.Sprintf("%d result %d: want %s, got %s", k.Line, k.Result, k.Want, k.Got)
				if k.InAlt {
					row += " [in either]"
					inAlt++
				}
				if k.Want != k.Got {
					row += " REFUSED"
					refused++
				}
				if k.Want.isRef() || k.Got.isRef() {
					refReaches++
				}
				byFile[f] = append(byFile[f], row)
			}
		}

		files := make([]string, 0, len(byFile))
		for f := range byFile {
			files = append(files, f)
		}
		sort.Strings(files)

		t.Logf("kind-gate census, %s lane: %d numeric-equal reaches (counted), %d enumerated "+
			"across %d file(s) — %d touching a reference kind, %d refused by the gate, %d inside "+
			"an `either`", lane.name, numericEqual, countRows(byFile),
			len(files), refReaches, refused, inAlt)
		for _, f := range files {
			for _, row := range byFile[f] {
				t.Logf("  %s:%s", f, row)
			}
		}
		t.Logf("kind-gate refusals on the FAIL path, %s lane: %d", lane.name, len(failRefusals))
		for _, row := range failRefusals {
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

		// **Both directions.** A refusal with no pinned row is a vector this gate started
		// refusing and nobody adjudicated; a pinned row with no refusal means the gate stopped
		// refusing it — which is the *intended* end state of #441 and must still fail here, so
		// that the reward is recorded deliberately rather than absorbed silently. The default
		// lane pins nothing and must produce nothing: its fail column is 0, so it has no fail
		// path to walk.
		wantRefusals := kindGateRefusals
		if lane.name == "default" {
			wantRefusals = map[string]string{}
		}
		for key, got := range gotRefusals {
			switch want, ok := wantRefusals[key]; {
			case !ok:
				t.Errorf("%s lane: unpinned kind-gate refusal at %s (%s).\n\tThis gate answering "+
					"false fails its vector, so a new row here is a new failure caused by a "+
					"comparison the authority does not make. Adjudicate it against "+
					"`script/runner.ml:464-476` and add it to kindGateRefusals with its reading.",
					lane.name, key, got)
			case want != got:
				t.Errorf("%s lane: %s refuses differently: pinned %q, got %q.\n\tThe pattern or "+
					"the constructor moved, so the adjudication recorded in kindGateRefusals was "+
					"about a different comparison than the one running now.", lane.name, key, want, got)
			}
		}
		for key := range wantRefusals {
			if _, ok := gotRefusals[key]; !ok {
				t.Errorf("%s lane: %s is pinned as a kind-gate refusal and the gate no longer "+
					"refuses it.\n\tIf #441's replacement landed, this is the reward and the pin "+
					"comes out in the same commit. If nothing changed here, the vector stopped "+
					"being asked — gated, retired upstream, or failing earlier — and a pin over a "+
					"vector nobody runs asserts nothing.", lane.name, key)
			}
		}
	}
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
