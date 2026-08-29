package spec

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// threadsProposal is the vendored suite's directory name for the threads vectors.
const threadsProposal = "threads"

// threadsLaneFiles is the proposal's population at suite pin de54fd2, reconciled exactly
// rather than floored — see testenv.RequireProposal for why a four-file directory gets an
// extent and the 257-file corpus gets both.
const threadsLaneFiles = 4

// threadsLaneCommands is the lane's total command count at that pin: 619 top-level directives
// across the four files, of which 444 are assertions.
//
// Pinned as well as the per-file rows, and not as arithmetic over them: the per-file tables
// could both be edited to agree with a shrunken corpus, and then every check in this file
// would pass over a population half its size. One number over the whole lane is the arm that
// makes that edit visible — the same reason the fetch script reconciles `files=` beside a
// floor rather than instead of one.
const threadsLaneCommands = 619

// threadsLaneRow is one file's pinned figures: what the parser found, and what the lane
// scored.
//
// Two tables in one row on purpose. `Heads` is a claim about the **corpus** — how many
// directives of each kind the file holds — and `pass/fail/…` is a claim about the **engine**
// against them. They move for unrelated reasons (a pin bump moves the first, a slice of work
// moves the second), and a single blob of numbers would report either as the other.
type threadsLaneRow struct {
	// Heads counts commands by their head atom as written, keyed the way the issue's table
	// is keyed so the two are directly comparable.
	Heads map[string]int

	// The verdict columns, pinned exactly. Not a floor and not a ceiling: four files is
	// small enough that every movement should be a reviewed diff, and *floors bound the
	// catastrophic case only* — a ceiling at a distance says nothing about a 6% loss.
	Pass, Fail, Unsupported, Gated, Unimplemented, Bound int

	// Declined is the validate stratum's decline count for this file, and it is a column here
	// rather than a figure in a log line because **this lane is the widening that reissues #9's
	// closure claim.**
	//
	// That criterion — *"no `assert_invalid` vector is declined against the corpus as the harness
	// may currently ask it"* (Scott's stamp on the #476 review) — is dated 2026-08-19, `f33a5c9`,
	// precisely because it is jointly a claim about the answerer and the asker: it can be
	// un-achieved by the question set widening with no regression whatsoever in the validator, and
	// a widening is not a population-size change, so no count sees it. Adding four files that ask
	// 444 new assertions is that event, on its first real occasion since the rule was made.
	//
	// A decline here would already be inside `Fail`, so the pinned `Fail` cannot see one arrive:
	// declines and mismatches are the same column at that resolution. This is the column that can.
	// It is not [#477](https://github.com/scttfrdmn/burroughs/issues/477)'s instrument, which is a
	// per-command-kind predicate table pinned by digest and stays open and unbuilt — it is the one
	// figure the reissued claim needs in order to be checkable over the population it now covers.
	Declined int
	// ValidateReached is how many of the file's failures the validator was the deciding layer for,
	// and it is pinned beside Declined because it is what tells the two readings of `Declined: 0`
	// apart — a validator that declined nothing, or a validator nothing arrived at.
	//
	// **It reports arrivals only in the direction it can see, and the asymmetry is the load-bearing
	// caveat: a bucket is a failure.** A vector the validator answered *correctly* leaves no row, so
	// this column counts the arrivals the validator got wrong and is a **lower bound** on arrivals
	// overall. That is enough for the vacuity question and not enough for a coverage claim, so it is
	// only ever used for the first. The measured values are 6 (imports.wast) and 8 (memory.wast),
	// which is what makes the lane's 0 declines a real 0 rather than an analytic one: at least 14
	// vectors got as far as the type checker and none was refused.
	//
	// Pinned rather than logged so the transition is a reviewed diff in either direction. It will
	// move when the wat `shared` keyword lands — in a PR whose author would otherwise have no reason
	// to know that landing it puts a stamped closure claim back in play.
	ValidateReached int
	// InvalidReached narrows ValidateReached to the criterion's own population: validate-stratum
	// failures on `assert_invalid` commands. Separate because the aggregate is not evidence about
	// the criterion — a validate-stratum failure on an `assert_return`'s module is the accept
	// direction, and #9's criterion quantifies over the reject direction only.
	//
	// **Measured 14 against ValidateReached's 14, so the narrowing is a no-op today**, and the
	// column stays anyway. Not on the grounds that it might diverge later: reading the criterion off
	// the aggregate would be reading it off a figure that *happens* to coincide, and protection by
	// coincidence stops protecting without notice. The first `assert_return` module this lane
	// rejects wrongly separates them.
	InvalidReached int
	// InvalidFailed is every failure on an `assert_invalid` command, at any stratum, and it is here
	// so the criterion's population is accounted for rather than assumed: 96 asked, **62 failed
	// somewhere and 34 pass**. *An unmeasured complement is not an empty one* — without this column
	// the 82 vectors outside `InvalidReached` would be describable as "presumably passing", and two
	// thirds of them are not.
	//
	// It is also what locates the 34: `exports.wast` holds 22 `assert_invalid` and fails none of
	// them, so a fifth of the criterion's population in this lane is already answered correctly by
	// *some* layer — which layer, a failure-shaped instrument cannot say.
	InvalidFailed int

	// GateSensitive is whether this file's ten verdict columns move when `Threads` is turned off,
	// and it is **the column that replaced an assertion of board-wide identity**.
	//
	// The original arm asserted the whole lane scored the same gate-on and gate-off, because it
	// did: the four files are wat text and the reader refused `shared` upstream of any feature
	// check, so the lane's stated premise — "what the suite says with the threads gate on" — was a
	// premise about a code path no vector reached. That arm was written to fire when the premise
	// became real, and the wat `shared` keyword is the slice that made it fire, for `imports.wast`
	// and `memory.wast`.
	//
	// **A pinned predicate rather than a second ten-column table.** What the off-board is for is
	// answering *is the gate load-bearing here*, and that is one bit per file; pinning the off
	// figures exactly would pin a board that is nobody's subject and would have to be re-read on
	// every slice that moves the on-board. What this cannot see is an off-board that changes while
	// staying different, and that is the deliberate bound: the off run exists to qualify the gate,
	// not to be a second lane.
	//
	// Pinned in both directions, which is the half that matters now. `true` going false is a gate
	// gone inert again — the state this lane spent its first two revisions in, undetectably. `false`
	// going true is a file whose vectors have newly reached the gate, which is progress and still a
	// reviewed diff. `atomic.wast` and `exports.wast` are the two `false`s: `atomic.wast`'s modules
	// are refused for their atomic mnemonics before any memory type is read, and `exports.wast`
	// declares no shared memory at all.
	GateSensitive bool
}

// threadsLane is the pinned board for testdata/spec/proposals/threads at suite pin de54fd2.
//
// **Pasted from the literal the test prints, not typed.** Every figure below was read off the
// harness and re-pinning is a paste — see threadsLaneLiteral.
//
// # The `Heads` half is where the issue's table was wrong, and the mechanism is the lesson
//
// #513 tabulated 98 `assert_invalid` and 446 assertions. The parser finds **96 and 444**, and
// the whole difference is `exports.wast`, where lines 134 and 190 are `;; (assert_invalid` —
// vectors upstream **commented out**. A text count sees them; a parser does not, because they
// are not commands. *Measure with the instrument, not a regex*: a grep counts tokens, and the
// question is what the harness can ask.
//
// The issue's other figure reconciles rather than disagrees. Its 323 `invoke` is the count of
// invoke *actions* — `(invoke` anywhere, including the one nested inside each `assert_return`
// — and 59 is the count of top-level invoke *commands*, which is what this table keys. Both
// are correct about different populations, so the number is not corrected here; it is being
// said which population each names. A figure quoted without its population is the shape that
// makes two honest readers disagree.
var threadsLane = map[string]threadsLaneRow{
	"atomic.wast": {
		Heads: map[string]int{"assert_invalid": 48, "assert_return": 142, "assert_trap": 45, "invoke": 59, "module": 3},
		Pass:  0, Fail: 297, Unsupported: 0, Gated: 0, Unimplemented: 0, Bound: 0,
		Declined: 0, ValidateReached: 0, InvalidReached: 0, InvalidFailed: 48,
		GateSensitive: false,
	},
	"exports.wast": {
		Heads: map[string]int{"assert_invalid": 22, "assert_return": 6, "module": 60},
		Pass:  88, Fail: 0, Unsupported: 0, Gated: 0, Unimplemented: 0, Bound: 0,
		Declined: 0, ValidateReached: 0, InvalidReached: 0, InvalidFailed: 0,
		GateSensitive: false,
	},
	"imports.wast": {
		Heads: map[string]int{"assert_invalid": 7, "assert_malformed": 16, "assert_return": 21, "assert_trap": 8, "assert_unlinkable": 59, "module": 39, "register": 2},
		Pass:  120, Fail: 30, Unsupported: 0, Gated: 0, Unimplemented: 0, Bound: 2,
		Declined: 0, ValidateReached: 6, InvalidReached: 6, InvalidFailed: 6,
		GateSensitive: true,
	},
	"memory.wast": {
		Heads: map[string]int{"assert_invalid": 19, "assert_malformed": 6, "assert_return": 45, "module": 12},
		Pass:  71, Fail: 11, Unsupported: 0, Gated: 0, Unimplemented: 0, Bound: 0,
		Declined: 0, ValidateReached: 8, InvalidReached: 8, InvalidFailed: 8,
		GateSensitive: true,
	},
}

// TestThreadsProposalLane is the threads suite lane: the 446 assertions under
// `testdata/spec/proposals/threads/` that `suitePaths`' one-level glob has never asked (#513).
//
// # Why the lane exists at all
//
// `suitePaths` globs `testdata/spec/*.wast` — one level — so four files have been in the
// vendored corpus and in no board's population since the corpus was pinned. Without them
// there is no oracle for any of contract §§2–5, which is the standing exception this
// instrument lands under: *no new instrument unless one is blocking code* (Scott, on the v1
// scoping report: *"it's blocking code, and without it nothing here has an oracle"*).
//
// The measurement that names the gap most sharply is not this test's: `Threads: true`
// substituted into `DefaultFeatures` **passes the entire suite with the board byte-identical**,
// because the vectors live here (see TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp in
// internal/binary, where that was measured). One of the nine gates could not be broken by any
// control in the tree.
//
// # Why it is a separate population
//
// See testenv.ProposalPaths. Folding these four files into `suitePaths` would move the fetch
// script's exact reconciliation, `MinSuiteFiles`, the monotonic `unsupported` ceiling, and the
// `256 files` board total published in `CHANGELOG.md` as a v0.4.0 conformance claim — and it
// would put fails into TestAllGatesOnLeavesNothingGated, whose entire guarantee is that
// nothing hides.
//
// # Why it cannot pass by asking nothing
//
// This is precisely the population whose invisibility hid #511's discarded shared bit while
// the all-gates-on lane read `0 fail, 0 gated`. So: the inputs are asserted present and
// reconciled to an extent (testenv.RequireProposal), the per-file directive counts are pinned
// from the parser rather than from a grep — *measure with the instrument* — and the verdict
// columns are pinned exactly in both directions. A clean board over an empty file set fails
// here at the door.
//
// # What the gates are, and what the board therefore says
//
// `DefaultFeatures` plus `Threads`, not every gate: the question the lane exists to ask is
// what the threads proposal's own suite says when the threads gate is on, and an all-on lane
// would answer a different question with eight other proposals' failures mixed in. The
// arrangement is `gateLane`'s — three decoding entry points, all carrying the gates — because
// assembling a second lane by hand is how a lane comes to score gates it does not have.
//
// **And the gate is load-bearing for half the lane, which is measured per file rather than
// assumed.** It was inert for the whole lane on the first two revisions — the board was identical
// with `Threads` off, a stated gate that changed no column, which is the *analytic zero* shape:
// configuration that configures nothing. The wat `shared` keyword ended that for `imports.wast` and
// `memory.wast`. See `GateSensitive` for the pin and for what it deliberately does not cover.
//
// # What the boards said, and the forecast each one falsified
//
// The first board was `269 pass, 348 fail, 0 unsupported, 0 gated, 2 bound` over 619 commands. The
// forecast going in — stated on the #519 review — was that **`unsupported` would move up first**,
// since all four files need the wat `shared` keyword and a form the harness cannot read is a
// question it cannot ask. The measurement said otherwise for a reason that generalises:
// `unsupported` is a property of the **Kind**, and these are ordinary `assert_return`/`module`
// directives. The harness asks every one of them; the *engine* is what refuses. So the honest
// direction was `fail` up, with the buckets carrying the diagnosis instead of the column:
//
//	246  no instance: unknown operator shared     (atomic.wast — the downstream of 3 modules)
//	 48  assert_invalid (module) expected: unknown memory
//	 16  (module <wat body>) must read
//
// **The second board falsified the reading of that first bucket, and this is the more expensive
// lesson of the two.** 246 of `atomic.wast`'s 297 were one *bucket* seen 246 times, and the slice
// that removed its stated cause moved none of them: the keyword landed, `(memory 1 1 shared)` now
// reads, and all three of `atomic.wast`'s modules still fail — on `i32.atomic.load` and its 66
// siblings, which lex from the same union'd table and have no `plaininstrShapes` entry. The bucket
// text changed from `unknown operator shared` to `unexpected token` and the count did not move.
// *An unmeasured complement is not an empty one*: attributing a fail set to the one cause its
// message names leaves every cause behind it unmeasured, and a necessary condition reads exactly
// like a sufficient one until it is removed. The board now reads `279 pass, 338 fail` — the +10 is
// `exports.wast` (+6), `memory.wast` (+3) and `imports.wast` (+1), and none of it is `atomic.wast`.
//
// # The lane reissues #9's closure criterion, and the reissue is measured rather than asserted
//
// #9 closed on a stamped criterion whose wording is about the *asker* as much as the answerer: *"no
// `assert_invalid` vector is declined against the corpus as the harness may currently ask it"*
// (2026-08-19, `f33a5c9`). This lane widens what the harness may ask by 96 `assert_invalid` vectors,
// which is the tracked event #477 exists for — its first real occasion — so the claim gets a fresh
// date and, being a claim about a population, a fresh measurement over that population.
//
// Measured: **96 asked, 62 failing at some layer, 14 decided by the validator, 0 declined.** The
// criterion holds over the widened set. It is not vacuous — 14 vectors reach the type checker — and
// the 14 is a lower bound on arrivals, because the validator's correct answers leave no bucket to
// count. See threadsLaneRow's ValidateReached for the asymmetry and for the two wrong readings that
// preceded the number.
//
// The columns are pinned so the next widening cannot happen quietly: this is the only place in the
// tree where the criterion's population and its decline count sit beside each other as data.
//
// Landing red is the arrangement, not a concession: the board reports and the buckets are the
// work plan (TestAllGatesOnLeavesNothingGated is deliberately red-ish for the same reason).
// The pins are what make it a control — every column exact in both directions, so the 338
// cannot quietly become 339 and cannot quietly become 330 either.
func TestThreadsProposalLane(t *testing.T) {
	paths := testenv.RequireProposal(t, suiteDir, threadsProposal, threadsLaneFiles)

	f := binary.DefaultFeatures()
	f.Threads = true

	rows, tot := scoreThreadsLane(t, paths, f, true)

	// **The gate is load-bearing for two of the four files, and which two is pinned.**
	//
	// This was an assertion of board-wide *identity* — the whole lane scored the same with `Threads`
	// off, because nothing reached the decoder's shared-limits path — written to fire on the day a
	// vector got there. It fired on the wat `shared` keyword: `imports.wast` and `memory.wast` now
	// differ, `atomic.wast` and `exports.wast` still do not. The claim was inverted rather than
	// deleted, which is what a tripwire whose subject moves gets: the *risk* was never "the boards
	// are equal", it was "the lane's stated gate configures nothing", and that risk is live in both
	// directions now that part of the lane depends on the gate.
	//
	// It is worth recording how the original was arrived at, since the successor inherits the
	// method: by neutering the gate line and reading the board. A gate set that changes nothing is
	// indistinguishable from one that works — every column agreeing is exactly what a working one
	// looks like — so *identical boards are the finding*, and the only way to have the finding is to
	// break the line on purpose.
	//
	// The comparison feeds `GateSensitive`, and the pin is checked by the same per-column loop as
	// every other figure, so the two directions are one assertion rather than a bespoke arm.
	offRows, offTot := scoreThreadsLane(t, paths, binary.DefaultFeatures(), false)
	sensitive := 0
	for name, on := range rows {
		off, ok := offRows[name]
		if !ok {
			// A file the off pass did not score at all, which `scoreThreadsLane` only produces by
			// failing to parse — already an error there. Left unmarked rather than defaulted to
			// either bit: a missing measurement is not evidence of insensitivity.
			continue
		}
		on.GateSensitive = !sameVerdicts(on, off)
		rows[name] = on
		if on.GateSensitive {
			sensitive++
			t.Logf("%s is gate-sensitive: Threads on (%d/%d/%d/%d/%d/%d/%d/%d/%d/%d), off "+
				"(%d/%d/%d/%d/%d/%d/%d/%d/%d/%d), as pass/fail/unsupported/gated/unimplemented/"+
				"bound/declined/validate-reached/invalid-reached/invalid-failed",
				name,
				on.Pass, on.Fail, on.Unsupported, on.Gated, on.Unimplemented, on.Bound,
				on.Declined, on.ValidateReached, on.InvalidReached, on.InvalidFailed,
				off.Pass, off.Fail, off.Unsupported, off.Gated, off.Unimplemented, off.Bound,
				off.Declined, off.ValidateReached, off.InvalidReached, off.InvalidFailed)
		}
	}
	t.Logf("gate check: Threads off scores %d pass, %d fail, %d bound, %d declined over %d "+
		"validate-stratum failures; %d of %d files score differently with it on, so the gate is "+
		"load-bearing over part of this population and inert over the rest",
		offTot.Pass, offTot.Fail, offTot.Bound, offTot.Declined, offTot.ValidateReached,
		sensitive, len(rows))

	// The pins are checked *after* the gate pass, because `GateSensitive` is one of the pinned
	// columns and is only known once both boards exist. Checking before would compare a row with
	// that field still at its zero value against a pin that says `true`, which is a false failure
	// whose message would name the right column for the wrong reason.
	for name, got := range rows {
		want, ok := threadsLane[name]
		if !ok {
			t.Errorf("%s has no row in threadsLane: a vector file in the lane's population "+
				"and in no pinned table is scored by nothing", name)
			continue
		}
		checkThreadsRow(t, name, want, got)
	}

	for name := range threadsLane {
		if !containsBase(paths, name) {
			t.Errorf("threadsLane pins %q, which is not in %s: the row outlived its file and "+
				"pins nothing while counting as coverage",
				name, testenv.ProposalDir(suiteDir, threadsProposal))
		}
	}

	if tot := totalHeads(tot.Heads); tot != threadsLaneCommands {
		t.Errorf("the lane walked %d commands, pinned %d: the per-file rows above can be edited "+
			"into agreement with a corpus that lost vectors, and this is the arm that cannot",
			tot, threadsLaneCommands)
	}

	t.Logf("corpus: suite pin %s (%s)", suitePin(t), testenv.ProposalDir(suiteDir, threadsProposal))
	t.Logf("threads lane over %d files: %d pass, %d fail, %d unsupported, %d gated, %d unimplemented, %d bound",
		len(paths), tot.Pass, tot.Fail, tot.Unsupported, tot.Gated, tot.Unimplemented, tot.Bound)
	// #9's criterion, over the population this lane adds, said in the figures it takes to say it
	// honestly. The vacuity terms come first because they are what qualify the declines figure, and
	// the narrow one governs: the criterion quantifies over `assert_invalid` vectors, so
	// validate-stratum failures on other kinds are a different population's evidence.
	t.Logf("#9's closure criterion over the lane: %d assert_invalid vectors asked, %d failed at "+
		"some layer (%d passing), %d decided by the validator (of %d validate-stratum failures in "+
		"all), %d declined — so the figure is %s",
		tot.Heads["assert_invalid"], tot.InvalidFailed, tot.Heads["assert_invalid"]-tot.InvalidFailed,
		tot.InvalidReached, tot.ValidateReached, tot.Declined,
		map[bool]string{
			true: "a measurement over a population the validator actually decided",
			false: "**unasked over this population, not satisfied by it**: every assert_invalid " +
				"vector here is answered upstream of the type checker, so the lane widens the " +
				"question set without widening the criterion's evidence",
		}[tot.InvalidReached > 0])
	t.Logf("directives by head: %s", headsLine(tot.Heads))

	// The sibling proposal directories, named by a glob rather than by this comment, so the
	// gap this lane does *not* close is priced rather than implied. Threads is the one with a
	// milestone; the others have no lane and no figures anywhere.
	if others, err := filepath.Glob(filepath.Join(suiteDir, "proposals", "*")); err == nil {
		var unlaned []string
		for _, d := range others {
			if filepath.Base(d) != threadsProposal {
				unlaned = append(unlaned, filepath.Base(d))
			}
		}
		t.Logf("proposal directories with no lane: %s", strings.Join(unlaned, " "))
	}

	t.Logf("re-pin (paste over threadsLane when a figure moves, and say which way in the PR):\n%s",
		threadsLaneLiteral(rows))
}

// scoreThreadsLane walks the lane's population under a stated gate set and returns the per-file
// figures plus their total.
//
// Extracted so the same walk answers the pinned board and the gate-inertness arm. Two hand-written
// walks would be two chances to score the second one differently from the first, which is the
// defect the gate arm exists to detect — and a detector that runs a different measurement from the
// thing it compares against detects its own difference.
//
// `log` is false for the second pass: its boards are not a report, and printing two sets of
// figures that differ only by a gate nobody has reached yet is how a reader comes to quote the
// wrong one.
func scoreThreadsLane(t *testing.T, paths []string, f binary.Features, log bool) (map[string]threadsLaneRow, threadsLaneRow) {
	t.Helper()

	_, engineFor := gateLane(f)
	rows := map[string]threadsLaneRow{}
	tot := threadsLaneRow{Heads: map[string]int{}}

	for _, p := range paths {
		name := filepath.Base(p)
		s, err := ParseFile(p)
		if err != nil {
			t.Errorf("%s: parse: %v", name, err)
			continue
		}

		heads := map[string]int{}
		for _, c := range s.Commands {
			heads[c.Head]++
			tot.Heads[c.Head]++
		}

		r := s.RunGated(engineFor())
		if log {
			t.Log("\n" + r.Board())
		}

		declined, declinedInvalid, reached, invalidReached, invalidFailed := declinedInLane(r.Buckets)
		rows[name] = threadsLaneRow{
			Heads: heads, Pass: r.Pass, Fail: r.Fail, Unsupported: r.Unsupported,
			Gated: r.Gated, Unimplemented: r.Unimplemented, Bound: r.Bound,
			Declined: declined, ValidateReached: reached, InvalidReached: invalidReached,
			InvalidFailed: invalidFailed,
		}
		if log && reached > 0 {
			// Logged only when the validator was reached, because a line reporting "0 declines"
			// over a population that asked the validator nothing is the analytic zero written out
			// as if it were a result. The `assert_invalid` subsets are separate because that is
			// the population #9's criterion quantifies over: a decline on an `assert_return`'s
			// module is a different claim from a decline on a vector the corpus says is invalid.
			t.Logf("%s: the validator decided %d failures (%d on assert_invalid), %d declined "+
				"(%d of those on assert_invalid)",
				name, reached, invalidReached, declined, declinedInvalid)
		}

		// The columns must sum to the command count, per file. TestVerdictsPartitionCommands
		// asserts this over `boardFiles`, which is the core suite: a new population is a new
		// place for a verdict to go missing, and the sum is the cheapest thing that notices.
		//
		// **Six terms, and `Bound` is the one this would have dropped.** `Result.Total()` is
		// `Pass + Fail` — a *ratio denominator*, not a partition — so the first draft here
		// summed with it and reported imports.wast's two `register` commands as unaccounted.
		// The control was right that a column was missing; the missing column was in the
		// assertion. That sixth term is the one TestVerdictsPartitionCommands' own comment
		// records paying for, and inheriting it costs one line here instead of a second grave.
		if got := r.Pass + r.Fail + r.Unsupported + r.Gated + r.Unimplemented + r.Bound; got != len(s.Commands) {
			t.Errorf("%s: verdicts sum to %d but the script has %d commands; %d vectors are "+
				"unaccounted for, so every figure below is over a population that does not add up",
				name, got, len(s.Commands), len(s.Commands)-got)
		}

		tot.Pass += r.Pass
		tot.Fail += r.Fail
		tot.Unsupported += r.Unsupported
		tot.Gated += r.Gated
		tot.Unimplemented += r.Unimplemented
		tot.Bound += r.Bound
		tot.Declined += declined
		tot.ValidateReached += reached
		tot.InvalidReached += invalidReached
		tot.InvalidFailed += invalidFailed
	}
	return rows, tot
}

// declinedInLane counts a result's validate-stratum declines, the `assert_invalid` subset of them,
// and — the reason there is a third return — how many rows reach the validate stratum at all.
//
// It reads `Failure.Declined` over `StratumValidate` rows — the flags, not a message match and not
// a subtraction. That is the same four-way read the core board does (TestPhase1Files, the arm that
// feeds `validateDeclineCeiling`), reused rather than re-derived: *lessons are indexed by shape*, and
// the shape already paid for there is that `validateFail − validateDeclined` named the admission
// stratum only while the wrong-message population was 0, and reported 162 for a population of 158
// the moment it wasn't.
//
// # `reached` is the vacuity check, and running it changed the reissue's sentence twice
//
// The lane's declines came out **0**, and a 0 that could not have come out otherwise answers
// nothing: *an analytic zero is not a measurement*. A decline is a thing `internal/validate` does,
// so a population none of whose vectors reach the validator reports 0 declines with the validator
// removed from the binary. `reached` distinguishes *"the validator was asked and declined none"*
// from *"the validator was never asked"* — only the first is evidence about #9's criterion, and the
// second would make the reissue say **unasked** rather than **satisfied**.
//
// Both readings I wrote before measuring were wrong, in opposite directions, which is the reason
// this comment is longer than the function:
//
//  1. Expected vacuum, found arrivals. The prediction was 0 — all four files need the wat `shared`
//     keyword, so the reader should refuse every module upstream of the type checker. It refuses
//     `atomic.wast`'s three and `exports.wast`'s sixty; `imports.wast` and `memory.wast` reach the
//     validator 14 times. So the 0 declines is a real 0.
//  2. Then the aggregate looked like the answer and wasn't. 14 validate-stratum failures is not 14
//     *criterion-population* failures; those are `invalidReached`, and it took a second measurement
//     to find they are the same 14 here. A figure that coincides with the one you want is not the
//     one you want.
//
// **And what the count cannot see, stated because it bounds the claim: a bucket is a failure.** The
// validator's *correct* answers leave no row, so `reached` is a lower bound on arrivals — sufficient
// to refute vacuity, insufficient for any statement of the form "the criterion was checked over N
// vectors". `exports.wast`'s 22 clean `assert_invalid` rows are the visible edge of that blind spot.
//
// Not a new instrument by the stated exemption — a predicate over data the lane's own run already
// produced, in columns of a table that is already pinned.
func declinedInLane(buckets map[string][]Failure) (total, onAssertInvalid, reached, invalidReached, invalidFailed int) {
	for _, fs := range buckets {
		for _, f := range fs {
			if f.Kind == KindAssertInvalid {
				// Counted across every stratum, because this is the term that says where the
				// criterion's population *went*. 96 vectors are asked and 14 reach the validator;
				// without this figure the remaining 82 are unaccounted, and "the rest presumably
				// passed" is an unmeasured complement.
				invalidFailed++
			}
			if f.Stratum != StratumValidate {
				continue
			}
			reached++
			if f.Kind == KindAssertInvalid {
				invalidReached++
			}
			if !f.Declined {
				continue
			}
			total++
			if f.Kind == KindAssertInvalid {
				onAssertInvalid++
			}
		}
	}
	return total, onAssertInvalid, reached, invalidReached, invalidFailed
}

// sameVerdicts reports whether two boards for the same file agree on every verdict column.
//
// `Heads` is excluded because it is a claim about the corpus and cannot move with a gate; so is
// `GateSensitive`, which is this function's own output and would make the comparison circular. The
// ten columns are listed rather than compared by struct equality for that reason — adding a
// verdict column must be a decision about whether the gate can move it, and `==` would answer that
// silently in whichever direction the field's zero value happened to give.
func sameVerdicts(a, b threadsLaneRow) bool {
	return a.Pass == b.Pass && a.Fail == b.Fail && a.Unsupported == b.Unsupported &&
		a.Gated == b.Gated && a.Unimplemented == b.Unimplemented && a.Bound == b.Bound &&
		a.Declined == b.Declined && a.ValidateReached == b.ValidateReached &&
		a.InvalidReached == b.InvalidReached && a.InvalidFailed == b.InvalidFailed
}

// checkThreadsRow compares one file's measurement against its pin, one column at a time.
//
// Column by column rather than by struct equality, because "the row differs" is not an
// actionable board line: which column moved is the whole content of the report, and a
// reward figure needs a subject.
func checkThreadsRow(t *testing.T, name string, want, got threadsLaneRow) {
	t.Helper()

	for _, col := range []struct {
		what      string
		want, got int
	}{
		{"pass", want.Pass, got.Pass},
		{"fail", want.Fail, got.Fail},
		{"unsupported", want.Unsupported, got.Unsupported},
		{"gated", want.Gated, got.Gated},
		{"unimplemented", want.Unimplemented, got.Unimplemented},
		{"bound", want.Bound, got.Bound},
		{"declined", want.Declined, got.Declined},
		{"validate-reached", want.ValidateReached, got.ValidateReached},
		{"invalid-reached", want.InvalidReached, got.InvalidReached},
		{"invalid-failed", want.InvalidFailed, got.InvalidFailed},
	} {
		if col.want != col.got {
			t.Errorf("%s: %s = %d, pinned %d.\n\tThe lane's figures are pinned exactly in both "+
				"directions: a movement is either the work this PR is for — re-pin from the "+
				"printed literal and state the direction — or a regression in a population "+
				"nothing else walks.", name, col.what, col.got, col.want)
		}
	}

	// The gate bit, out of the integer loop because its two failure directions are two different
	// findings and a "= false, pinned true" line would report them as one. See GateSensitive.
	if want.GateSensitive != got.GateSensitive {
		if want.GateSensitive {
			t.Errorf("%s: the Threads gate no longer moves this file's board, and it did when the "+
				"row was pinned.\n\tThe lane's premise is that its figures are what the suite says "+
				"*with the gate on*; a gate that has gone inert again makes that premise describe "+
				"a code path no vector reaches, which is the state this file spent two revisions "+
				"in without anything noticing.", name)
		} else {
			t.Errorf("%s: the Threads gate now moves this file's board, and it did not when the "+
				"row was pinned.\n\tThis is progress and still a reviewed diff: re-pin from the "+
				"printed literal and say in the PR which slice made the gate reachable here.", name)
		}
	}

	// The corpus half, both directions. A head the file holds with no row is a directive the
	// pin does not know about; a row with no head is a pin that outlived its subject.
	for h, n := range got.Heads {
		if want.Heads[h] != n {
			t.Errorf("%s: %d %s directives, pinned %d — the *corpus* moved, which a pin bump "+
				"does and a slice of work does not", name, n, h, want.Heads[h])
		}
	}
	for h, n := range want.Heads {
		if _, ok := got.Heads[h]; !ok {
			t.Errorf("%s: pinned %d %s directives and the file holds none: the row is stale",
				name, n, h)
		}
	}
}

// totalHeads sums a head tally.
func totalHeads(heads map[string]int) int {
	n := 0
	for _, v := range heads {
		n += v
	}
	return n
}

// containsBase reports whether any path has this base name.
func containsBase(paths []string, base string) bool {
	for _, p := range paths {
		if filepath.Base(p) == base {
			return true
		}
	}
	return false
}

// headsLine renders a head tally largest-first, for a log line a human reads against the
// issue's table.
func headsLine(heads map[string]int) string {
	keys := make([]string, 0, len(heads))
	for k := range heads {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if heads[keys[i]] != heads[keys[j]] {
			return heads[keys[i]] > heads[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d %s", heads[k], k)
	}
	return b.String()
}

// threadsLaneLiteral renders a measured table as the Go literal that pins it.
//
// Printed rather than described: a re-pin done by reading a board and retyping numbers is a
// hand-copied measurement, and this package's whole convention is that a figure a human typed
// is a figure nobody measured.
func threadsLaneLiteral(m map[string]threadsLaneRow) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("var threadsLane = map[string]threadsLaneRow{\n")
	for _, n := range names {
		r := m[n]
		fmt.Fprintf(&b, "\t%q: {\n\t\tHeads: map[string]int{", n)
		heads := make([]string, 0, len(r.Heads))
		for h := range r.Heads {
			heads = append(heads, h)
		}
		sort.Strings(heads)
		for i, h := range heads {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %d", h, r.Heads[h])
		}
		fmt.Fprintf(&b, "},\n\t\tPass: %d, Fail: %d, Unsupported: %d, Gated: %d, Unimplemented: %d, Bound: %d,\n\t\tDeclined: %d, ValidateReached: %d, InvalidReached: %d, InvalidFailed: %d,\n\t\tGateSensitive: %t,\n\t},\n",
			r.Pass, r.Fail, r.Unsupported, r.Gated, r.Unimplemented, r.Bound,
			r.Declined, r.ValidateReached, r.InvalidReached, r.InvalidFailed, r.GateSensitive)
	}
	b.WriteString("}\n")
	return b.String()
}
