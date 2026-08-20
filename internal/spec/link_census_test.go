// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package spec

import (
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestModuleDefinitionLinkCensus counts the link failures raised while instantiating a *module
// definition* command, in the all-gates-on lane, and pins the count exactly and in both directions.
//
// # Why a census exists at all: the board cannot see this population
//
// A `(module …)` command's third fact — that it instantiates — is **unscored** (#367, blocked on
// #366). A module definition whose instantiation fails at `StratumExec` therefore scores a *pass*,
// so the board's counters are structurally blind to every wrongly-refused link in the corpus. That
// is contract §9's G-3 accept-direction blind spot with a concrete population attached, and it is
// how grave #368 stayed invisible: four modules the reference links were being refused, on a board
// that could not have a column for them.
//
// This is deliberately **not** a fix to the scoring path — it is a census beside it. Scoring fact 3
// changes what the harness *asks* of 2494 commands at once and is #367's own work; counting what
// the linker already answers costs one hook and asks nothing new.
//
// # Why the count is exact, and why both directions
//
// *Floors bound the catastrophic case; only an exact count sees a small silent loss.* A floor of
// "no more than 3" would pass a regression that refused one more module, and a bare "at least 3"
// would pass one that stopped refusing the three that are right. So the table below is per-row and
// the check runs both ways: an unlisted refusal is a *new over-rejection*, and a listed refusal
// that stopped happening is a *stale pre-registration* — `TestGrave206KnownFailures`' rule, which
// caught two stale rows in this very change.
//
// **The residue was three rows and is now zero, so one of the two directions has lost its subject.**
// The paragraph above used to end "the residue is not empty, which is what makes the check
// falsifiable rather than a comparison against nothing (*a comparison against an empty set
// succeeds*): three `instance.wast` rows are genuine `unknown import` failures the harness's own
// registry semantics produce, not matcher verdicts, and they stay listed precisely so this test has a
// subject when the matcher is right." #426 removed the cause those rows named — an instance form the
// harness could not classify — so the sentence is collected rather than deleted: the rows were right
// about why they were red, and the fix was one layer below the matcher exactly as they said.
//
// The consequence is unpleasant enough to state where the claim is. The stale-pre-registration
// direction is now empty against empty and says nothing, which leaves `attemptedFloor` as the only
// thing standing between `0 refused` and `0 asked`. See `known`'s own comment for what the forward
// direction gained in exchange.
//
// # The vacuity floor
//
// `attemptedFloor` is the second half of the same worry: every assertion here is quantified over
// module-definition instantiations, so a hook that stopped firing — a lane wired to the wrong
// entry point, a `RunGated` that short-circuits, an `InstantiateLinked` override lost the way
// `allOnLane`'s own comment records it being lost three times — would make every row vacuously
// absent and the census would report a perfect zero. *A suspiciously clean result is a tell, and
// exactly zero is the cleanest one.* The floor is well below the measured 2494 so that corpus
// growth does not fail it, and far above zero so that a silenced hook does.
func TestModuleDefinitionLinkCensus(t *testing.T) {
	requireSuite(t)

	// file:line → why this module definition is refused. Every entry needs a citation, on
	// TestGatedVectors' own principle: an unexplained pre-registration is a suppression wearing a
	// disguise.
	//
	// **Grave #368 emptied the `incompatible import type` half of this table**, which is the whole
	// reward figure the board could not show. The four rows as this census printed them *before* the
	// fix — the messages themselves are also #368, printing indices at a reader who cannot resolve
	// them, and the current spelling of the first row is `expected global (ref null func), got global
	// (ref func)`:
	//
	//	linking.wast:112          expected global const funcref, got global const (ref func)
	//	type-equivalence.wast:218 expected func [(ref 4)] -> [], got func [(ref 3)] -> []
	//	type-subtyping.wast:713   expected func [] -> [(ref 2)], got func [] -> [(ref 0)]
	//	type-subtyping.wast:731   expected func [] -> [(ref 6)], got func [] -> [(ref 4)]
	//
	// Those four rows are gone and must not come back: the first was the global arm's `==`
	// refusing `match_globaltype`'s covariance, the other three the func arm comparing type
	// indices drawn from two different type sections.
	// **#426 emptied the other half, and the reason is the one the rows themselves gave.** All three
	// said "registry semantics, not the matcher" — `(register "I1" $I1)` named a `(module instance …)`
	// the harness had no case for, so the name was never bound and the import genuinely was not there.
	// The rows as this census printed them before the widening, with the count they carried:
	//
	//	instance.wast:15   unknown import
	//	instance.wast:62   unknown import
	//	instance.wast:128  unknown import
	//
	// The three are gone because the cause is: an instance form now instantiates, its register binds,
	// and all three importing modules link. Recorded rather than only performed — a table that
	// silently shrinks cannot be told from one trimmed to make a test pass, and that argument applies
	// hardest to the deletion that empties it (`moduleOverRejections`' own words, one file over).
	//
	// **What is vacuous now, and what still binds.** The reverse direction — a listed refusal that
	// stopped happening — has no subject at 0 rows and asserts nothing; that is a real loss of an
	// instrument and it is stated rather than left for a reader to infer from an empty literal. The
	// forward direction is not only live but **strictly stronger**: with the table empty, *any* link
	// refusal of a module definition in the all-on lane is now a failure, which is the assertion this
	// census wanted from the start and could not make while three rows were legitimately red. It is
	// held up by `attemptedFloor`, and only by that — see the vacuity paragraph above, which is what
	// keeps `0 refused` distinguishable from `0 asked`.
	known := map[string]string{}

	// A floor, not the measured figure: this is the vacuity check, and pinning it exactly would
	// make every added corpus file a failure of a test about linking.
	const attemptedFloor = 1500

	_, _, allOnEngine := allOnLane(t)

	attempted := 0
	got := map[string]string{}
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		e := allOnEngine()
		inner := e.InstantiateLinked
		e.InstantiateLinked = func(c Command, reg Registry) (Instance, Stratum, error) {
			in, st, ierr := inner(c, reg)
			switch c.Kind {
			case KindModuleText, KindModuleBinary, KindModuleQuote:
			default:
				// Not a module *definition*: an `assert_unlinkable` or `assert_trap` instantiation
				// is a scored command and the board already reports its verdict. Counting those
				// here would mix a population the board sees with one it does not, and the whole
				// point of this census is the second one.
				//
				// **#426's two Kinds belong here rather than in the list above, and for two
				// different reasons** — worth naming because "a new module form" reads like an
				// omission. `KindModuleDefinition` never reaches this hook at all: a definition
				// validates and deliberately does not instantiate, which is the form's entire
				// content. `KindModuleInstance` does reach it and is **scored** — an instance form
				// asserts that its definition instantiates, so a link refusal there is a board
				// `fail` with a bucket, not the invisible pass that #367 leaves on a plain
				// `(module …)`. Admitting it would put a seen population in the census whose
				// premise is the unseen one.
				return in, st, ierr
			}
			attempted++
			// **`errors.Is` against the sentinel, not a substring of the message.** The message is
			// testimony and is expected to change wording; the sentinel is the verdict channel and
			// is not. A census keyed on prose would silently empty itself the next time a detail
			// string is reworded — which is exactly what this change did to all four of the rows
			// the table above records.
			if errors.Is(ierr, interp.ErrLinkFailed) {
				got[rowKey(f, c.Line)] = ierr.Error()
			}
			return in, st, ierr
		}
		s.RunGated(e)
	}

	// Routed through `boardBound` as a `vacuityBound` rather than compared inline, because
	// `TestEveryBoardBoundIsChecked` is right that an inline comparison bypasses the staleness
	// machinery and is indistinguishable from a constant with no reachable path (grave 0003). The
	// kind is the plausibility one: this floor exists to catch a hook that found *nothing*, so its
	// looseness is its function and slack-checking it would fire on ordinary corpus growth.
	boardBound(t, "attemptedFloor", attempted, attemptedFloor, 0, vacuityBound,
		"every assertion in this census is quantified over module-definition instantiations, so a "+
			"hook that stopped firing would report a clean zero rather than a failure — check that "+
			"allOnLane still overrides the InstantiateLinked the engine actually calls")
	if attempted < attemptedFloor {
		return // the rows below would be a census of nothing
	}

	rows := make([]string, 0, len(got))
	for row := range got {
		rows = append(rows, row)
	}
	sort.Strings(rows)
	for _, row := range rows {
		if _, ok := known[row]; !ok {
			t.Errorf("%s: a module definition the corpus expects to link was refused: %s\n\t"+
				"this is an accept-direction over-rejection, which no negative vector can "+
				"falsify (contract §9 G-3) and which the board does not score (#367) — diagnose "+
				"it against valid/match.ml and fix it, or file it and add the row with a citation",
				row, got[row])
		}
	}
	for row, why := range known {
		if _, ok := got[row]; !ok {
			t.Errorf("%s is pre-registered as refused (%s) but links now; remove the entry — "+
				"a stale pre-registration overstates what is broken", row, why)
		}
	}
	t.Logf("all-on lane: %d module-definition instantiations, %d refused, %d pinned",
		attempted, len(got), len(known))
}

// rowKey is the census's `file:line` spelling, in one place so the table's keys and the observed
// rows cannot drift in formatting — a mismatch there would read as a clean census.
func rowKey(file string, line int) string {
	return file + ":" + strconv.Itoa(line)
}
