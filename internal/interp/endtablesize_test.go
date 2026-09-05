// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/spec"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// # The memory bill for #136's pairing table, priced before the representation is chosen
//
// `scandist_test.go` priced the scan in *time* and `scanbench` confirmed it is material at the median
// span. What neither measured is the other side of the trade: 0002's resolved-target form costs bytes
// retained beside every function body, for the life of the module, whether or not the function is ever
// called. That is the arithmetic decisions 0002 and 0016 both rest on, and it needs a number rather
// than either principal's judgement.
//
// **0016 is the decision this one answers to, and reading it corrected two things.** It chose a sparse
// `map[int][]uint32` for `br_table`'s label vectors, and the first draft of this file called a dense
// array "a departure from a shape this project has already reasoned about". That is not what 0016 says.
// Its rejected option A was growing `Instr` by a **24-byte** slice field to serve **one opcode in 256**
// — six times the per-slot cost of an `int32`, for an instruction an order of magnitude rarer than a
// structural opener — so the map won against a much more expensive alternative than this one, and the
// density question was **explicitly deferred to here**: *"#136's benchmark is where the arena question
// gets settled. This ADR does not pre-empt it"* (0016, Consequences). *A valid citation does not certify
// its sentence*: the pointer resolved and the clause between the pointers was invented.
//
// The second correction is the more useful one. 0016 records a fourth option it calls the one that
// *"reads as elegant"* — **a single arena on `Func` with an offset and length** — rejected for v0 only
// as premature, with the note that *"when it is run, this is the option to reach for."* This is that
// run. So the arena is not an extra variant somebody thought of; it is owed to this measurement by name,
// and a bill that omitted it would be answering a narrower question than 0016 asked.
//
// Ordered by Scott in session, relayed verbatim to a durable comment on #136 and cited from
// [0048](../../docs/decisions/0048-the-pairing-table-lives-in-a-per-module-arena-reached-by-one-int32-on-func-because-the-per-function-field-dominates-a-measured-bill.md):
// *"price the memory bill before landing it … From the same corpus with the same instrument, as
// evidence inside the ADR rather than a separate PR: total bytes for the full-body dense hybrid,
// against the sparse map, against a span-scoped dense variant indexed over `[first opener, last
// matching end]` rather than the whole body. If span-scoping saves substantially it's worth its one
// extra concept; if not, take the full dense array and say so with the number attached."*
//
// The order is cited through the comment rather than as "Scott said so in the session that produced
// this file", because *an in-session order has no citation* and this file's figures are what an ADR
// points at when it claims an approval exists.
//
// # The bill has two halves, and the first version of this file measured one of them
//
// A representation costs what its structures cost **plus what the field that reaches them costs**, and
// the second term is charged on **every function** rather than on the 13.5% that hold an opener. Over
// this corpus the field term is the larger of the two for every dense shape, so a bill that reported
// only the weighed structures was reporting the smaller half and calling it the bill.
//
// It was not merely incomplete, it was **unevenly incomplete, and in the same direction**. The first
// version held each body's structure in a `[]any`, and a `[]int32` is three words: converting one to an
// interface heap-allocates a 24-byte copy of the header. A `map`, a `*T` and an arena offset are all
// pointer-shaped and convert for free. So the harness charged a 24-byte-per-body header to exactly the
// variants whose real field is 24 bytes wide, and charged nothing to the variants whose real field is 8
// — an artifact that stood in for the omitted term, badly, for three rows out of six and not at all for
// the others. The two errors were not independent: *the pointer that reaches a structure is part of the
// structure*, and a harness that boxes some variants and not others has already made a choice about it.
//
// So this version does both halves explicitly:
//
//   - **structures are weighed** into a typed container allocated *before* the baseline, so nothing is
//     boxed and nothing is charged a header the port does not pay;
//   - **the field is charged analytically**, and the figure is derived from the real `binary.Func` and
//     `binary.Module` by `reflect.StructOf` rather than typed in by hand — so it prices padding
//     absorption correctly and cannot drift when either struct gains a field. See
//     `endSizeFieldGrowth`.
//
// # The question has two axes, and the omitted term is what made the second one visible
//
// 0048 asks *"where does the pairing live, and in what shape?"* — which is **placement** and **shape**,
// and the first version priced only shape, with placement varying accidentally between rows. Once the
// field is charged, placement turns out to be the axis that decides the corpus ranking, so the space is
// priced as a cross:
//
//   - **shapes**: dense over the whole body · dense span-scoped to `[first opener, last matching end]`
//     · sorted `(pc, end)` pairs with a binary search. All three are expressed as `[]int32` slots so
//     that one arena can hold any of them and so that the payload floor is one formula.
//   - **placements**: inline in `Func` · behind one pointer from `Func` · in a per-module arena with an
//     `int32` offset on `Func`. Their field terms differ by up to 24 bytes on every function, which is
//     a wider spread than any two shapes' payloads differ by.
//
// Nine cells, plus 0016's sparse map as a tenth row that belongs to neither axis. Every cell is built
// and weighed by **one code path**, which is what keeps a cell from being favoured by an accident in
// its own block — the failure mode where the comparison measures the harness. The arena was such a
// block in the first version: it could not fit the per-body builder signature, so it had its own
// weighing function and its own correctness loop.
//
// # The field's *position* is part of its cost, and that is what settled the ranking
//
// A field is charged at the cheapest position in the struct's field order, not appended — see
// `endSizeFieldGrowth` for why appending is the worst case by construction. `binary.Func` opens
// `TypeIndex uint32` followed by a slice header, so it carries **exactly one 4-byte interior hole**, and
// an `int32` placed in it costs **zero**. That is 75144 bytes over this corpus, it is the largest single
// term in the comparison, and no shape can use it twice: the hole holds one `int32` and the arena's
// offset is the one representation whose whole field fits. It is also why the span-scoped shape is
// refused — its origin is a *second* `int32`, and there is no second hole.
//
// The report measures the hole rather than describing it (`endSizeHole`), because "there are 4 bytes
// free after `TypeIndex`" is precisely the hand-computed layout claim this file exists to stop making.
//
// # Why every variant is weighed and none is computed from a formula
//
// A per-entry byte model for `map[int]int32` is a claim about Go's map implementation, not a
// measurement of it: Go 1.24 replaced the bucket layout with Swiss tables, so any figure carried over
// from the old shape is wrong in a direction nobody here has checked, and a hand-derived one is *a
// count that is really a model*. So every structure is **built over the whole corpus and weighed**,
// which prices allocator size-class rounding and map control words as the program pays them.
// `endSizeHeapLive` documents what the reported quantity is and the two wrong instruments it took to
// get there.
//
// Every variant with a definable payload also carries an **analytic lower bound** — 4 B per `int32`
// slot — reported and asserted beside the measurement. This is the second mechanism, and it is here
// because *a delta from one broken instrument can be sound while both absolute levels are wrong*. It
// earned its place immediately: the first instrument weighed the arena row at 55% of the bytes it
// provably contained, and nothing else in this file could have noticed. The map is the one shape with
// no honest analytic form, which is the whole reason the weighing exists.
//
// # The limit of this measurement, stated before its numbers
//
// **The corpus is the conformance suite, and the thesis workload is Go guests.** This is the same gap
// `scandist_test.go` names for the time half, and here it does more than bound the absolute figure: it
// bounds which *row wins*. Two properties of the corpus decide the ranking, both of them artifacts of
// hand-written conformance tests and both inverted by a Go binary:
//
//   - **the fraction of functions that hold any opener** — 13.5% here — which is what makes a pointer
//     placement's per-body cell cheaper than a wide inline field;
//   - **functions per module** — 2.2 here — which is what makes an arena's per-module field term large,
//     since a Go guest is one module with thousands of functions.
//
// So the report prints those two properties beside the bill, and the dominance relations between
// placements are stated as arithmetic over the measured per-function and per-module terms rather than
// as a ranking read off the totals. The **absolute bill for a Go guest** is not obtainable from this
// corpus and no claim in that direction may be sourced from this test; it is an open exposure recorded
// in the ADR with the condition that revisits it — the first Go guest the project builds.
//
// A second-order note, since this file is an instrument reporting on instruments: it shares
// `scandist_test.go`'s corpus and helpers deliberately (`scanDistImage`, `scanDistOpName`), so the two
// populations are the same population and their figures may be read against each other. Divergence
// between them would be a defect in one of the two, not a fact about the corpus.
const endSizeSuiteDir = scanDistSuiteDir

// endSizeBody is one decoded function body, retained so every variant is built over the same
// population rather than over whatever a fresh decode happens to produce.
type endSizeBody struct {
	body []binary.Instr
	// openers is every structural opener's index paired with its `END`, computed once by `matchEnd`
	// — the shipped oracle. Every variant is checked against this, so a representation that is cheap
	// because it is wrong cannot win the comparison.
	openers []endSizePair
	// mod is the ordinal of the module this body was decoded from. The arena placement needs it: an
	// arena's whole claim is that one allocation serves many bodies, and *which* bodies share it is
	// the module, since a module is what a `Store` retains and releases as a unit. Weighing a single
	// corpus-wide arena would price a structure the port cannot build.
	mod int
}

type endSizePair struct{ pc, end int32 }

// endSizeShape is what the pairing is stored *as*, independent of where it lives.
//
// Every shape is a run of `int32` slots, which is not a presentational choice: it is what lets one
// arena hold any of them, lets one payload floor cover all of them, and keeps the shape axis from
// smuggling in a placement difference. The pair shape spends two slots per opener and searches them;
// the dense shapes spend one slot per instruction and index them.
type endSizeShape struct {
	name string
	// slots is how many int32 slots body b needs.
	slots func(b *endSizeBody) int
	// origin is what a lookup subtracts from `pc` before indexing, and needsOrigin says whether the
	// owning struct has to retain it. Only the span shape does.
	origin      func(b *endSizeBody) int32
	needsOrigin bool
	// lenFromBody says the slot count is `len(Func.Body)` and so need not be retained. Only the
	// whole-body dense shape has that property, and it is worth an `int32` in the arena placement.
	lenFromBody bool
	// prefill says the slots must start at -1 ("no header here"). The pair shape writes every slot
	// it owns and so needs no sentinel.
	prefill bool
	// fill writes b's pairings into t, which is exactly slots(b) long.
	fill func(t []int32, b *endSizeBody, origin int32)
	// find answers pc from t, by the same contract `matchEnd` answers it.
	find func(t []int32, origin, pc int32) (int32, bool)
}

// endSizeVariant is one cell of the cross: the fields the owning structs carry, a store to build into,
// a builder, and a lookup.
type endSizeVariant struct {
	name string
	// shape and placement are the two axes, carried separately from `name` so the report can group
	// by one and compare along the other. Derived from the name in the first version, which made the
	// grouping a string-parsing accident rather than a property of the cell.
	shape, placement string
	// fields are appended to `binary.Func` to reach this representation, and charged to every
	// function in the corpus rather than to every opener. modFields are the same for
	// `binary.Module`; only the arena placement has any.
	fields    []reflect.Type
	modFields []reflect.Type
	// store allocates the container the built structures live in, *before* the baseline reading. It
	// stands for the fields of `Func` and `Module`, whose bytes are charged analytically instead —
	// charging them here would charge the variant for an allocation the port never makes.
	store func(bodies []endSizeBody, nmods int) any
	// build fills in body i's structure. It writes into the store rather than returning, which is
	// what keeps `[]int32` out of an interface and so keeps the boxing artifact out of the bill.
	build func(st any, i int, b *endSizeBody)
	// lookup answers "where is this opener's END" from the store, or reports no answer.
	lookup func(st any, i int, b *endSizeBody, pc int32) (int32, bool)
	// tally reports the structures built, the int32 slots they hold, and whether a payload floor is
	// derivable. False means "no honest formula for this shape", which is the map's case and is
	// reported as such rather than as a zero.
	tally func(st any) (structures, slots int, hasPay bool)
}

// TestEndTablePairingRepresentationsArePriced weighs the candidate representations over the suite and
// prints the bill. It asserts correctness and vacuity, and pins nothing about the sizes.
//
// **No pin on the bytes**, for `scandist_test.go`'s reason one step further on: the figures decide a
// representation in an ADR, and a pin would freeze them before the decision they exist to inform has
// been made. What is asserted is what makes them a measurement — that the corpus was read, that every
// variant answers every opener exactly as `matchEnd` does, and that the weighing is not reporting
// noise. *A comparison needs a vacuity check*, and the check that matters here is the agreement count:
// ten structures agreeing perfectly over an empty set of openers is the same clean zero that has been
// an instrument's own blindness every previous time it appeared.
func TestEndTablePairingRepresentationsArePriced(t *testing.T) {
	testenv.RequireSuite(t, endSizeSuiteDir)

	bodies, stats := endSizeCorpus(t)

	// Vacuity floors. Far below the live figures, as in `scandist_test.go`: they fire when this stops
	// seeing the corpus, which is the failure this test can actually have.
	if stats.modulesOK < 500 {
		t.Errorf("decoded %d modules, want >=500 — a bill computed over a corpus this test can no "+
			"longer read is a print with no subject", stats.modulesOK)
	}
	if len(bodies) < 2000 {
		t.Errorf("retained %d non-empty function bodies, want >=2000 — see above", len(bodies))
	}
	if stats.openers < 2000 {
		t.Errorf("paired %d openers, want >=2000 — the census (#503) measured 2020 over this same "+
			"corpus, so a materially smaller count means this walk and that one disagree about the "+
			"population", stats.openers)
	}

	variants := endSizeVariants()

	// The correctness gate, run before anything is weighed. A variant that cannot answer is not a
	// cheaper option, it is a different program. Every cell runs it on the same footing, which the
	// arena did not in the first version: it is the placement most likely to win, and a
	// representation whose index arithmetic is off by a body's offset would answer *some* openers
	// correctly — the shape where a partly-wrong structure looks cheap and passes a spot check.
	checks := 0
	for _, v := range variants {
		st := v.store(bodies, stats.modulesOK)
		for i := range bodies {
			v.build(st, i, &bodies[i])
		}
		for i := range bodies {
			b := &bodies[i]
			for _, p := range b.openers {
				got, ok := v.lookup(st, i, b, p.pc)
				if !ok {
					t.Fatalf("%s: no answer for opener at pc %d in a body of %d slots (module %d), "+
						"where matchEnd pairs it with %d", v.name, p.pc, len(b.body), b.mod, p.end)
				}
				if got != p.end {
					t.Fatalf("%s: opener at pc %d (module %d) resolved to %d, matchEnd says %d",
						v.name, p.pc, b.mod, got, p.end)
				}
				checks++
			}
		}
	}
	// The vacuity check on the agreement above, which is the one assertion in this file that has
	// caught something every time it has been written: perfect agreement over nothing is perfect.
	if want := stats.openers * len(variants); checks != want {
		t.Errorf("checked %d pairings, want %d (%d openers × %d representations) — an agreement claim "+
			"over a population smaller than the one measured is not the claim it reads as",
			checks, want, stats.openers, len(variants))
	}

	funcType, modType := endSizeUncommittedFunc(t), reflect.TypeOf(binary.Module{})
	var rows []endSizeRow
	spreads := make([]endSizeSpread, 0, len(variants))
	for _, v := range variants {
		row, sp := endSizeWeighBest(v, bodies, stats.modulesOK)
		spreads = append(spreads, sp)
		fc, err := endSizeFieldGrowth(funcType, v.fields)
		if err != nil {
			t.Fatalf("%s: pricing its fields on %s: %v", v.name, funcType, err)
		}
		row.fieldBytes, row.fieldWidth, row.fieldAt = stats.funcs*fc.growth, fc.growth, fc.at
		row.fieldAppended, row.fieldData, row.fieldExact = fc.appended, fc.data, fc.exact
		if len(v.modFields) > 0 {
			mc, err := endSizeFieldGrowth(modType, v.modFields)
			if err != nil {
				t.Fatalf("%s: pricing its fields on %s: %v", v.name, modType, err)
			}
			row.modBytes, row.modWidth, row.modAt = stats.modulesOK*mc.growth, mc.growth, mc.at
			row.fieldExact = row.fieldExact && mc.exact
		}
		row.total = row.alloc + int64(row.fieldBytes+row.modBytes)
		rows = append(rows, row)
	}

	// The analytic floor, asserted. A structure holding N `int32`s occupies at least 4N bytes, so a
	// weighing that comes in under its own payload is measuring the collector rather than the
	// structure — which is not hypothetical here: it is how the sweep-timing defect in
	// `endSizeHeapLive` was found, at 0.55× on the arena row.
	for _, r := range rows {
		if !r.hasPay {
			continue
		}
		// Asserted on the reported mechanism only. `heap` is a cross-check whose failure mode is
		// documented and was not fully explained, so it is printed rather than asserted — pinning it
		// would pin a defect this file has characterised.
		if r.alloc < int64(r.payload) {
			t.Errorf("%s: %d B cumulatively allocated to hold %d slots, whose payload floor is "+
				"%d B. A structure cannot occupy fewer bytes than it provably contains, and this "+
				"mechanism cannot under-read by reclamation, so it indicts the builder",
				r.name, r.alloc, r.slots, r.payload)
		}
	}

	// The field term's own check, and it took two goes to get right.
	//
	// It began as "a field that costs nothing is a field that is not there" — `fieldWidth > 0`. That
	// assertion is false, and it is false for the row that ends up chosen: once the charge is the
	// *cheapest position* rather than the appended one, a field landing in interior padding costs
	// **zero** legitimately, and asserting otherwise would have made the instrument reject its own
	// best result. *A bound in the wrong direction fires on the finding.*
	//
	// What it is replaced with is the check the original was reaching for — that the column is a
	// computed layout fact rather than a silent zero. Both halves are asserted: the fields carry real
	// data (so a row cannot reach a representation through nothing at all), and the swept position is
	// never *worse* than appending (so a sweep that found no valid layout cannot pass as a free field).
	for _, r := range rows {
		if r.fieldData <= 0 {
			t.Errorf("%s: its fields on %s carry %d B of data — a representation cannot be reached "+
				"through no field at all", r.name, funcType, r.fieldData)
		}
		if r.fieldExact && (r.fieldWidth < 0 || r.fieldWidth > r.fieldAppended) {
			t.Errorf("%s: fields at the cheapest position in %s grow it by %d B, appended by %d B — "+
				"the swept minimum cannot exceed the position every sweep contains",
				r.name, funcType, r.fieldWidth, r.fieldAppended)
		}
	}

	// The instrument's own resolution, asserted rather than asserted-about — and asserted as
	// *reproducibility of the minimum* rather than as a tolerance on two readings, which is grave #570
	// repaired at its second reading rather than its first.
	//
	// # Why a tolerance on a pair was the wrong shape, and why a minimum is the right one
	//
	// What stood here weighed the first variant twice and required the two readings within 2048 B. Its
	// stated ground was that the readings are *"bit-identical run to run, retained structures over a
	// revision-pinned corpus being deterministic"*, so the tolerance would only fire where that stopped
	// being true. The premise is right about the quantity and wrong about the window: `TotalAlloc` is
	// process-wide, so a reading is the structures' bytes **plus whatever else allocated between the two
	// `ReadMemStats` calls**, and a shuffled full-suite run has other goroutines in it. #570 caught that
	// on linux/arm64 CI, where the pair read 60008 and 54688 B — the second being the deterministic value
	// exactly, so one window was clean. This check then fired the same way on darwin/arm64, where the pair
	// read **54912 and 60232 B against a deterministic 54688**: both members contaminated, by +224 and
	// +5544. That is what rules out "cold window versus warm" as the mechanism and makes *"compare two
	// warm readings"* — the first item on #570's repair list — insufficient, because a pair can have no
	// clean member to be compared against. It also means the **bill** was quoting 54912 for that row, 224
	// B above the truth, which is the contamination #570 found in three of nine rows arriving in the
	// figure rather than in the guard. That the two pairs differ by 5320 B each is a coincidence with no
	// mechanism attached, and is left named rather than explained.
	//
	// The property that survives is one-sidedness. Foreign allocation inside a window can only **add**
	// bytes, never remove them, because `TotalAlloc` is cumulative and the window is a difference of two
	// of its values. So the clean windows all report the identical true value and the contaminated ones
	// report more, which makes the **minimum over K weighings the estimator** — every row above is one,
	// where before every row was a single window and three of nine of them were wrong.
	//
	// That the minimum recovers the truth is measured rather than argued, on two whole-suite runs: the
	// first had nine of ten rows carrying spread, the second six, and **in both runs every spread row's
	// minimum equalled the deterministic value #570 recorded** for that row on this platform — nine of
	// nine, then six of six. That the count of noisy rows moved between two runs of the same code over the
	// same corpus is the same fact from the other side: which windows get hit is a property of the run,
	// not of the row. The estimator is the repaired half.
	//
	// # The assertion is against the gap the decision turned on, because a clean window is not observable
	//
	// The first draft asserted the minimum was attained *twice* — "at least two clean windows agree
	// bit-identically", the original determinism premise in a form that survives one dirty window. The
	// whole-suite run falsified it immediately: contamination is not a rare event there but roughly half of
	// all windows (15 of 27 readings clean over nine spread rows), so "two clean of three" fails at a rate
	// no gate can carry, and raising K only buys an exponent against a probability that is not small. **A
	// clean window cannot be asserted in this process**; the honest options are to weigh in a process where
	// nothing else allocates, or to stop claiming a resolution the instrument does not have.
	//
	// So what is asserted is the claim the bill actually needs, which was always narrower than
	// bit-identity: the noise must be small against **the gap the ranking's conclusion turns on** — the
	// distance from the cheapest total to the next — because that is the comparison 0048 made and the only
	// one anything downstream rests on. Derived from the table rather than picked, which is what the
	// replaced `2048` could never be: an arbitrary tolerance is a threshold with no subject, and this one
	// has the subject printed beside it.
	//
	// **And the adjacent pairs the noise does swallow are named rather than left to a reader's arithmetic.**
	// Some middle rows sit closer together than the spread, so their *ordering* is not resolved by this
	// instrument, and a printed table always looks totally ordered. Listing them is the disclosure that
	// keeps the ranking's readable part separable from its unreadable part — *an unasserted distance is the
	// vacuum*, pointed at the gaps rather than at the bound.
	maxSpread, maxSpreadOf := int64(0), ""
	var noisy []string
	for i, sp := range spreads {
		if sp.max == sp.min {
			continue
		}
		noisy = append(noisy, fmt.Sprintf("%s %v", rows[i].name, sp.allocs))
		if d := sp.max - sp.min; d > maxSpread {
			maxSpread, maxSpreadOf = d, rows[i].name
		}
	}

	byTotal := make([]endSizeRow, len(rows))
	copy(byTotal, rows)
	sort.Slice(byTotal, func(i, j int) bool { return byTotal[i].total < byTotal[j].total })
	decisive := byTotal[1].total - byTotal[0].total
	if maxSpread >= decisive {
		t.Errorf("the noisiest row (%s) spread %d B over %d weighings, and the cheapest two totals are "+
			"%d B apart (%s at %d B, %s at %d B). The gap the conclusion turns on is no larger than the "+
			"instrument's own spread, so this board cannot say which representation is cheapest — which "+
			"is the one thing it is read for",
			maxSpreadOf, maxSpread, endSizeWeighings, decisive,
			byTotal[0].name, byTotal[0].total, byTotal[1].name, byTotal[1].total)
	}

	var unresolved []string
	for i := 1; i < len(byTotal); i++ {
		if g := byTotal[i].total - byTotal[i-1].total; g <= maxSpread {
			unresolved = append(unresolved, fmt.Sprintf("%s vs %s (%d B apart)",
				byTotal[i-1].name, byTotal[i].name, g))
		}
	}
	t.Logf("instrument resolution: %d variants × %d weighings, each row the minimum; %d row(s) had any "+
		"spread, the widest %d B (%s); the cheapest-to-second gap this board is read for is %d B\n"+
		"  rows with spread: %s\n"+
		"  adjacent pairs this instrument does not order (gap <= the widest spread): %s",
		len(variants), endSizeWeighings, len(noisy), maxSpread, endSizeOrNone(maxSpreadOf), decisive,
		endSizeOrNone(strings.Join(noisy, "; ")), endSizeOrNone(strings.Join(unresolved, "; ")))

	// The denominator that makes the bill mean something. An absolute byte count over a corpus is not
	// a materiality claim — 85 KB is either negligible or ruinous depending on what it is 85 KB *of* —
	// and the honest comparator is what the decoded bodies the table sits beside already cost. This is
	// the memory analogue of the time half's "the scan is 26% of a slot's cost": a share, measured
	// against the thing that must be retained anyway.
	instrSize := int(reflect.TypeOf(binary.Instr{}).Size())
	bodyBytes := instrSize * stats.slots

	t.Logf("corpus: %d modules · %d functions (%d with an empty body, excluded from the structures "+
		"and charged in the field term) · %d non-empty bodies retained · %d openers paired\n"+
		"  bodies containing at least one opener: %d (%.1f%% of all functions)\n"+
		"  instruction slots in all non-empty bodies: %d\n"+
		"  slots in bodies that contain an opener: %d (%.1f%% of all slots)\n"+
		"  slots inside [first opener, last matching end]: %d (%.1f%% of all slots)\n"+
		"  openers per body that has one: mean %.2f · max %d\n"+
		"  binary.Func is %d B and binary.Module is %d B before any field is added\n\n%s\n%s",
		stats.modulesOK, stats.funcs, stats.bodiesEmpty, len(bodies), stats.openers,
		stats.bodiesWithOpener, 100*float64(stats.bodiesWithOpener)/float64(stats.funcs),
		stats.slots, stats.slotsInOpenerBodies,
		100*float64(stats.slotsInOpenerBodies)/float64(stats.slots),
		stats.slotsInSpan, 100*float64(stats.slotsInSpan)/float64(stats.slots),
		float64(stats.openers)/float64(stats.bodiesWithOpener), stats.maxOpenersInBody,
		funcType.Size(), modType.Size(),
		endSizeReport(rows, bodyBytes, instrSize, stats.modulesOK, stats.funcs),
		endSizeRankingTurnsOn(rows, stats, funcType))
}

// endSizeFieldGrowth is what adding fields of the given types to struct `base` costs per instance, at
// **the cheapest position in the field order** — reported with the position, since the position is a
// choice the mechanism gets to make.
//
// **Derived from the real struct rather than typed in**, and that is the whole point: the field term
// is the larger half of the bill for every dense shape, and a hand-written "a slice is 24 bytes" would
// be *a count that is really a model* in the one column where padding decides the answer. A 4-byte
// `int32` appended to a struct whose size is already a multiple of 8 costs **8** bytes rather than 4,
// and two `int32`s cost the same 8 as one.
//
// # Why it sweeps positions instead of appending, which changed the answer
//
// The first version appended, and appending is the *worst* placement for a narrow field: it lands past
// the last field, where there is no padding to absorb it by construction. Real structs have interior
// padding, and `binary.Func` opens `TypeIndex uint32` followed by a slice header — **4 bytes of dead
// space at offset 4**, which is exactly an `int32`. So the charge for the chosen representation's field
// went from 8 B per function to **0**, which is 75144 B over this corpus and reorders the table.
//
// This is not a trick and it is not fragile: the mechanism declares the field, so it declares where in
// the order it goes, and holding the existing fields' order fixed keeps the claim narrow — nothing here
// proposes reordering `Func` to make room. It *is* dependent on the base struct's current layout, and
// that dependence is the reason this is computed rather than written down: gaining or losing a field in
// `Func` moves the figure, and the next reader gets the moved figure rather than this one.
//
// The fields are inserted as a group. Splitting them across several holes could do better for a
// multi-field representation, and it is not modelled — so for those rows this is the best *contiguous*
// placement rather than the best conceivable one, which is a bound in the honest direction.
//
// `reflect.StructOf` cannot mirror a struct with unexported fields, so that case returns the summed
// width of the fields and says it is not exact. Reported rather than silently substituted: a column
// that is sometimes a layout fact and sometimes a lower bound has to say which.
// endSizeUncommittedFunc is `binary.Func` with `EndsOff` removed: the struct as it stood before this
// measurement's own conclusion was implemented.
//
// # Without this the instrument stops reproducing its own result, and says so wrongly
//
// The comparison's largest single term is a 4-byte hole after `TypeIndex` that absorbs one `int32`
// for free (75144 B over this corpus against the appended position). 0048 chose the arena on that
// term and the mechanism then *spent* the hole — `Func.EndsOff` is the field that fills it. So from
// the moment the decision landed, charging a hypothetical `int32` against the live `Func` prices a
// **second** one at the full 8 B, the arena row moves from 154520 B to 229664 B, and the table
// re-orders. Both numbers are correct about different structs, which is the problem: the ADR cites
// the first and the test at HEAD printed the second, so the instrument refuted the record it is the
// evidence for. It also printed the prose "the only interior hole is 0 B wide, and the arena's offset
// is what fits in it", which is not a stale sentence but a false one — the same
// hand-derived-claim-over-a-machine-table defect this file already paid for once, recurring because
// the sentence's unstated premise was the tree it was written on.
//
// So the base is the counterfactual, and the counterfactual is the right question anyway: every row
// asks "what would this representation cost, added to a `Func` that does not already have it", which
// is the question the decision turned on and the only one under which all ten rows share a base. A
// provenance note in the ADR ("measured at the parent commit") would have been honest and strictly
// worse — a table nobody can re-derive is a table that drifts silently the next time `Func` moves.
//
// `TestEndsOffsetIsFreeInTheLayout` in `internal/binary` is this function's other half: it asserts
// that the field really is absorbed, so a `Func` that starts paying for it fails there rather than
// quietly changing the meaning of every figure here.
func endSizeUncommittedFunc(t *testing.T) reflect.Type {
	t.Helper()

	const committed = "EndsOff"
	ft := reflect.TypeOf(binary.Func{})
	var kept []reflect.StructField
	found := false
	for i := range ft.NumField() {
		f := ft.Field(i)
		if f.PkgPath != "" {
			t.Fatalf("binary.Func.%s is unexported, so this instrument can no longer mirror Func and "+
				"every field charge below would be a lower bound reported as exact", f.Name)
		}
		if f.Name == committed {
			found = true
			continue
		}
		kept = append(kept, f)
	}
	if !found {
		// Not a fatal: before the mechanism landed there was no such field, and the base was Func
		// itself. Logged rather than silent, because "the field is gone" and "the field was never
		// added" are the same observation here and the second is a change to what 0048 decided.
		t.Logf("binary.Func has no %s field; charging fields against Func as it stands", committed)
		return ft
	}
	return reflect.StructOf(kept)
}

func endSizeFieldGrowth(base reflect.Type, fs []reflect.Type) (endSizeFieldCost, error) {
	if base.Kind() != reflect.Struct {
		return endSizeFieldCost{}, fmt.Errorf("%s is not a struct", base)
	}
	if len(fs) == 0 {
		return endSizeFieldCost{}, fmt.Errorf("no fields given for %s", base)
	}
	cost := endSizeFieldCost{at: -1}
	for _, f := range fs {
		cost.data += int(f.Size())
	}

	existing := make([]reflect.StructField, 0, base.NumField())
	for i := range base.NumField() {
		sf := base.Field(i)
		if sf.PkgPath != "" {
			// Not an error: it is a fact about the base struct that costs this column its
			// exactness, and the bound it falls back to is still sound in the direction that
			// matters — a field cannot cost less than its own width.
			cost.growth, cost.appended = cost.data, cost.data
			return cost, nil
		}
		sf.Index, sf.Offset = nil, 0
		existing = append(existing, sf)
	}

	added := make([]reflect.StructField, len(fs))
	for i, f := range fs {
		added[i] = reflect.StructField{Name: fmt.Sprintf("EndSizeAddedField%d", i), Type: f}
	}

	cost.exact, cost.growth = true, -1
	for pos := 0; pos <= len(existing); pos++ {
		fields := make([]reflect.StructField, 0, len(existing)+len(added))
		fields = append(fields, existing[:pos]...)
		fields = append(fields, added...)
		fields = append(fields, existing[pos:]...)
		g := int(reflect.StructOf(fields).Size() - base.Size())
		if pos == len(existing) {
			cost.appended = g
		}
		if cost.growth < 0 || g < cost.growth {
			cost.growth, cost.at = g, pos
		}
	}
	return cost, nil
}

// endSizeFieldCost is what one representation's fields cost on one struct.
//
// `growth` is the per-instance charge at the cheapest position `at`; `appended` is what the same fields
// cost past the last field, kept because it is the figure a hand-model would produce and the gap between
// the two is the finding. `data` is the fields' own summed width, which is what makes "growth 0" a
// padding fact rather than a missing column. `exact` is false when the base struct could not be
// mirrored and `growth` is a lower bound.
type endSizeFieldCost struct {
	growth, at, appended, data int
	exact                      bool
}

// endSizeStats are the population figures the bill is denominated over.
type endSizeStats struct {
	modulesOK           int
	funcs               int
	bodiesEmpty         int
	openers             int
	bodiesWithOpener    int
	slots               int
	slotsInOpenerBodies int
	slotsInSpan         int
	maxOpenersInBody    int
}

// endSizeCorpus decodes the suite once and retains every non-empty body with its pairings.
func endSizeCorpus(t *testing.T) ([]endSizeBody, endSizeStats) {
	t.Helper()

	paths, err := testenv.SuitePaths(endSizeSuiteDir)
	if err != nil {
		t.Fatalf("SuitePaths %s after RequireSuite passed: %v", endSizeSuiteDir, err)
	}

	var (
		bodies []endSizeBody
		st     endSizeStats
	)
	for _, p := range paths {
		s, err := spec.ParseFile(filepath.Join(endSizeSuiteDir, p))
		if err != nil {
			continue
		}
		for _, c := range s.Commands {
			img, ok := scanDistImage(c)
			if !ok {
				continue
			}
			m, err := binary.DecodeModule(img)
			if err != nil {
				// The suite is full of modules that must fail to decode; that is
				// `assert_malformed`'s subject, not this file's.
				continue
			}
			mod := st.modulesOK
			st.modulesOK++
			for i := range m.Funcs {
				body := m.Funcs[i].Body
				st.funcs++
				if len(body) == 0 {
					st.bodiesEmpty++
					continue
				}
				st.slots += len(body)

				eb := endSizeBody{body: body, mod: mod}
				for pc := range body {
					ins := body[pc]
					if ins.Prefix != 0x00 {
						continue
					}
					if _, structural := scanDistOpName(ins.Op); !structural {
						continue
					}
					end, err := matchEnd(body, pc)
					if err != nil {
						// `matchEnd`'s not-found arm is the layering debt its own header names, and
						// `scandist_test.go` already asserts it is unreachable over this corpus. An
						// unpairable opener is skipped here rather than re-asserted, so that this
						// file has one subject.
						continue
					}
					eb.openers = append(eb.openers, endSizePair{pc: int32(pc), end: int32(end)})
				}
				if len(eb.openers) == 0 {
					// Retained anyway: a body with no opener is the population the nil arm exists
					// for, and dropping it here would price the table over a corpus selected to
					// make it look good.
					bodies = append(bodies, eb)
					continue
				}
				st.openers += len(eb.openers)
				st.bodiesWithOpener++
				st.slotsInOpenerBodies += len(body)
				st.slotsInSpan += endSizeSpanLen(&eb)
				if n := len(eb.openers); n > st.maxOpenersInBody {
					st.maxOpenersInBody = n
				}
				bodies = append(bodies, eb)
			}
		}
	}
	return bodies, st
}

// endSizeSpanLen is the length of the span shape's window — `[first opener, last matching end]` — and
// endSizeSpanOff its origin.
//
// Every opener lies inside that window, and this is the argument rather than an assumption the
// measurement inherits: the first opener is the minimum opener index by construction, and an opener's
// `END` is greater than the opener, so the maximum `END` is at or beyond every opener. The shape's own
// `find` asserts the consequence for every opener in the corpus.
func endSizeSpanLen(b *endSizeBody) int {
	lo, hi := endSizeSpanOff(b), int32(-1)
	for _, p := range b.openers {
		if p.end > hi {
			hi = p.end
		}
	}
	return int(hi - lo + 1)
}

func endSizeSpanOff(b *endSizeBody) int32 {
	lo := b.openers[0].pc
	for _, p := range b.openers {
		if p.pc < lo {
			lo = p.pc
		}
	}
	return lo
}

// endSizeShapes is the shape axis: what a body's pairings are stored as.
func endSizeShapes() []endSizeShape {
	return []endSizeShape{
		{
			// The shape `ends_table.go` measured, plus a nil arm for bodies with no opener. `-1` is
			// "no header here". The one shape whose slot count `Func` already knows, since it is
			// `len(Body)` — worth an `int32` in the arena placement and nothing anywhere else.
			name:        "dense, whole body",
			slots:       func(b *endSizeBody) int { return len(b.body) },
			origin:      func(*endSizeBody) int32 { return 0 },
			lenFromBody: true,
			prefill:     true,
			fill: func(t []int32, b *endSizeBody, _ int32) {
				for _, p := range b.openers {
					t[p.pc] = p.end
				}
			},
			find: func(t []int32, _, pc int32) (int32, bool) {
				if pc < 0 || int(pc) >= len(t) || t[pc] < 0 {
					return 0, false
				}
				return t[pc], true
			},
		},
		{
			// Scott's third arm: dense, but windowed to the region that can contain an opener. Its
			// origin has to be retained, which is the cost the payload saving is measured against.
			name:        "dense, span-scoped",
			slots:       endSizeSpanLen,
			origin:      endSizeSpanOff,
			needsOrigin: true,
			prefill:     true,
			fill: func(t []int32, b *endSizeBody, org int32) {
				for _, p := range b.openers {
					t[p.pc-org] = p.end
				}
			},
			find: func(t []int32, org, pc int32) (int32, bool) {
				j := pc - org
				if j < 0 || int(j) >= len(t) || t[j] < 0 {
					return 0, false
				}
				return t[j], true
			},
		},
		{
			// **Beyond the order, and reported as such.** The space Scott named has one dense axis
			// and one sparse axis, and the sparse point he named is a map — the shape 0016 chose. A
			// sorted pair run is the other sparse point: two slots per *opener* with no
			// per-instruction term at all and no map machinery, at the cost of a binary search
			// rather than an index.
			//
			// No sort call. `endSizeCorpus` walks `pc` upwards, so `openers` is already ascending,
			// and `TestEndTableOpenersAreAscending` asserts it rather than leaving it as a comment.
			// The first version sorted defensively and that cost 41512 B of transient `sort.Slice`
			// reflection garbage across the corpus, which showed up as that row's two mechanisms
			// disagreeing by a factor of two. *A defensive call is not free, and here it was
			// polluting the measurement it was defending.*
			name:   "sorted (pc,end) pairs, binary search",
			slots:  func(b *endSizeBody) int { return 2 * len(b.openers) },
			origin: func(*endSizeBody) int32 { return 0 },
			fill: func(t []int32, b *endSizeBody, _ int32) {
				for k, p := range b.openers {
					t[2*k], t[2*k+1] = p.pc, p.end
				}
			},
			find: func(t []int32, _, pc int32) (int32, bool) {
				n := len(t) / 2
				k := sort.Search(n, func(k int) bool { return t[2*k] >= pc })
				if k >= n || t[2*k] != pc {
					return 0, false
				}
				return t[2*k+1], true
			},
		},
	}
}

// The per-placement stores. Each one stands for the fields `binary.Func` and `binary.Module` would
// carry: one slot per body, allocated before the baseline reading, so the field's bytes are charged
// analytically by `endSizeFieldGrowth` instead of being weighed as an allocation the port never makes.
type (
	endSizeInlineStore struct {
		t      [][]int32
		origin []int32
	}
	endSizePtrStore struct {
		plain  []*endSizeCell
		origin []*endSizeOriginCell
	}
	endSizeArenaStore struct {
		// a is one arena per module; off is each body's origin within its module's arena, and n the
		// body's extent there.
		a            [][]int32
		off, n       []int32
		origin       []int32
		need, cursor []int
	}
	// endSizeMapStore is 0016's shape, which belongs to neither axis.
	endSizeMapStore struct{ m []map[int]int32 }
)

// endSizeCell and endSizeOriginCell are the pointer placement's per-body allocation: 24 bytes for a
// shape that needs no origin, 32 for one that does. Two types rather than one with an unused field,
// because charging the origin-free shapes for an origin would be *pricing a variant in a shape nobody
// proposed*.
type (
	endSizeCell       struct{ t []int32 }
	endSizeOriginCell struct {
		t      []int32
		origin int32
	}
)

var (
	endSizeInt32Slice = reflect.TypeOf([]int32(nil))
	endSizeInt32      = reflect.TypeOf(int32(0))
)

// endSizeVariants is the cross of shapes and placements, plus 0016's sparse map.
func endSizeVariants() []endSizeVariant {
	var vs []endSizeVariant
	for _, place := range []func(endSizeShape) endSizeVariant{
		endSizeInlineVariant, endSizePointerVariant, endSizeArenaVariant,
	} {
		for _, sh := range endSizeShapes() {
			vs = append(vs, place(sh))
		}
	}
	return append(vs, endSizeMapVariant())
}

// endSizeMakeSlots allocates one body's slot run. This is the allocation the bill is about.
func endSizeMakeSlots(sh endSizeShape, b *endSizeBody) []int32 {
	t := make([]int32, sh.slots(b))
	if sh.prefill {
		for j := range t {
			t[j] = -1
		}
	}
	return t
}

// endSizeInlineVariant places the slots in a slice field on `Func` — the widest field in the table,
// since a slice header is three words and a span-scoped shape needs a fourth for its origin.
func endSizeInlineVariant(sh endSizeShape) endSizeVariant {
	fields := []reflect.Type{endSizeInt32Slice}
	if sh.needsOrigin {
		fields = append(fields, endSizeInt32)
	}
	return endSizeVariant{
		name:      sh.name + " · inline in Func",
		shape:     sh.name,
		placement: "inline in Func",
		fields:    fields,
		store: func(bodies []endSizeBody, _ int) any {
			return &endSizeInlineStore{
				t: make([][]int32, len(bodies)), origin: make([]int32, len(bodies)),
			}
		},
		build: func(st any, i int, b *endSizeBody) {
			if len(b.openers) == 0 {
				return
			}
			s := st.(*endSizeInlineStore)
			org := sh.origin(b)
			t := endSizeMakeSlots(sh, b)
			sh.fill(t, b, org)
			s.t[i], s.origin[i] = t, org
		},
		lookup: func(st any, i int, _ *endSizeBody, pc int32) (int32, bool) {
			s := st.(*endSizeInlineStore)
			return sh.find(s.t[i], s.origin[i], pc)
		},
		tally: func(st any) (int, int, bool) {
			n, sl := 0, 0
			for _, t := range st.(*endSizeInlineStore).t {
				if t != nil {
					n++
					sl += len(t)
				}
			}
			return n, sl, true
		},
	}
}

// endSizePointerVariant places the slots behind one pointer from `Func`: an 8-byte field, and a
// 24-or-32-byte cell allocated for the bodies that need one.
//
// It trades 16 bytes of field on *every* function for a cell on every function that holds an opener,
// so it is cheaper exactly while the opener-bearing fraction is below `16 / sizeof(cell)`. That
// fraction is 13.5% on this corpus and is the single most corpus-dependent term in the bill.
func endSizePointerVariant(sh endSizeShape) endSizeVariant {
	field := reflect.TypeOf((*endSizeCell)(nil))
	if sh.needsOrigin {
		field = reflect.TypeOf((*endSizeOriginCell)(nil))
	}
	return endSizeVariant{
		name:      sh.name + " · behind one pointer from Func",
		shape:     sh.name,
		placement: "behind one pointer from Func",
		fields:    []reflect.Type{field},
		store: func(bodies []endSizeBody, _ int) any {
			s := &endSizePtrStore{}
			if sh.needsOrigin {
				s.origin = make([]*endSizeOriginCell, len(bodies))
			} else {
				s.plain = make([]*endSizeCell, len(bodies))
			}
			return s
		},
		build: func(st any, i int, b *endSizeBody) {
			if len(b.openers) == 0 {
				return
			}
			s := st.(*endSizePtrStore)
			org := sh.origin(b)
			t := endSizeMakeSlots(sh, b)
			sh.fill(t, b, org)
			if sh.needsOrigin {
				s.origin[i] = &endSizeOriginCell{t: t, origin: org}
				return
			}
			s.plain[i] = &endSizeCell{t: t}
		},
		lookup: func(st any, i int, _ *endSizeBody, pc int32) (int32, bool) {
			s := st.(*endSizePtrStore)
			if sh.needsOrigin {
				c := s.origin[i]
				if c == nil {
					return 0, false
				}
				return sh.find(c.t, c.origin, pc)
			}
			c := s.plain[i]
			if c == nil {
				return 0, false
			}
			return sh.find(c.t, 0, pc)
		},
		tally: func(st any) (int, int, bool) {
			s := st.(*endSizePtrStore)
			n, sl := 0, 0
			for _, c := range s.plain {
				if c != nil {
					n++
					sl += len(c.t)
				}
			}
			for _, c := range s.origin {
				if c != nil {
					n++
					sl += len(c.t)
				}
			}
			return n, sl, true
		},
	}
}

// endSizeArenaVariant is 0016's fourth option, the one it called elegant and deferred to this
// measurement: **one allocation per module** holding every body's slots end-to-end, with each body
// keeping an `int32` offset into it.
//
// Scoped **per module** rather than per corpus. A module is the unit a `Store` retains and releases,
// so a corpus-wide arena would price a structure the port cannot build, and it would flatter the row
// by thousands of allocations it would never actually avoid.
//
// It is the only placement with no per-body allocation at all, so it pays no size-class rounding and
// its field is one `int32`. What it pays instead is a slice header per *module* — which is 24 bytes
// against 4216 modules here and 24 bytes against one Go guest, the largest corpus dependence in the
// table after the opener-bearing fraction.
//
// **Priced with each module's extent already known, which the port must earn.** The arena is
// allocated once at its exact size, so `need` is computed in `store`, before the baseline. A decoder
// cannot know that while decoding the first body of a module; it can know it at end-of-module, from
// the bodies it has already decoded, and fill the arena there. That is a pass over the module's own
// function list, not a re-walk of any instruction sequence — the pairings are captured during decode
// either way. An `append`-grown arena would be a different row: cumulative allocation for a doubling
// append is about twice the final extent, so this row's margin is exactly what that end-of-module
// pass buys.
func endSizeArenaVariant(sh endSizeShape) endSizeVariant {
	// One `int32` for the offset; a second for the origin when the shape needs one; a third for the
	// extent unless `Func` already knows it as `len(Body)`. One and two cost the same 8 bytes on an
	// 88-byte struct, and three cost 16 — which is the whole reason `lenFromBody` is a field of
	// `endSizeShape` rather than a detail of one builder.
	fields := []reflect.Type{endSizeInt32}
	if sh.needsOrigin {
		fields = append(fields, endSizeInt32)
	}
	if !sh.lenFromBody {
		fields = append(fields, endSizeInt32)
	}
	return endSizeVariant{
		name:      sh.name + " · per-module arena, int32 offset on Func",
		shape:     sh.name,
		placement: "per-module arena, int32 offset on Func",
		fields:    fields,
		modFields: []reflect.Type{endSizeInt32Slice},
		store: func(bodies []endSizeBody, nmods int) any {
			s := &endSizeArenaStore{
				a:   make([][]int32, nmods),
				off: make([]int32, len(bodies)), n: make([]int32, len(bodies)),
				origin: make([]int32, len(bodies)),
				need:   make([]int, nmods), cursor: make([]int, nmods),
			}
			for i := range bodies {
				s.off[i] = -1
				if len(bodies[i].openers) == 0 {
					continue
				}
				s.need[bodies[i].mod] += sh.slots(&bodies[i])
			}
			return s
		},
		build: func(st any, i int, b *endSizeBody) {
			if len(b.openers) == 0 {
				return
			}
			s := st.(*endSizeArenaStore)
			if s.a[b.mod] == nil {
				// A module with no structural opener anywhere in it allocates nothing at all —
				// the nil arm, one level up from a nil per-body table. That is a real and large
				// population in the suite, and it is why this is allocated on first use rather
				// than for every module.
				t := make([]int32, s.need[b.mod])
				if sh.prefill {
					for j := range t {
						t[j] = -1
					}
				}
				s.a[b.mod] = t
			}
			n := sh.slots(b)
			off := s.cursor[b.mod]
			s.off[i], s.n[i], s.origin[i] = int32(off), int32(n), sh.origin(b)
			s.cursor[b.mod] = off + n
			sh.fill(s.a[b.mod][off:off+n], b, s.origin[i])
		},
		lookup: func(st any, i int, b *endSizeBody, pc int32) (int32, bool) {
			s := st.(*endSizeArenaStore)
			if s.off[i] < 0 {
				return 0, false
			}
			off, n := int(s.off[i]), int(s.n[i])
			return sh.find(s.a[b.mod][off:off+n], s.origin[i], pc)
		},
		tally: func(st any) (int, int, bool) {
			n, sl := 0, 0
			for _, t := range st.(*endSizeArenaStore).a {
				if t != nil {
					n++
					sl += len(t)
				}
			}
			return n, sl, true
		},
	}
}

// endSizeMapVariant is 0016's shape exactly: the same `map[int]...` keyed by instruction index that
// `Labels`, `Catches` and `Casts` use.
//
// Off both axes, because a map is neither a slot run nor a placement of one — which is itself part of
// the finding. Its field is one word, the narrowest in the table and a real advantage the first
// version of this test could not see; what it pays for that is a control structure per function that
// no formula here may state.
func endSizeMapVariant() endSizeVariant {
	return endSizeVariant{
		name:      "sparse map[int]int32 · inline in Func (0016's shape)",
		shape:     "sparse map[int]int32 (0016's shape)",
		placement: "inline in Func",
		fields:    []reflect.Type{reflect.TypeOf(map[int]int32(nil))},
		store: func(bodies []endSizeBody, _ int) any {
			return &endSizeMapStore{m: make([]map[int]int32, len(bodies))}
		},
		build: func(st any, i int, b *endSizeBody) {
			if len(b.openers) == 0 {
				return
			}
			m := make(map[int]int32, len(b.openers))
			for _, p := range b.openers {
				m[int(p.pc)] = p.end
			}
			st.(*endSizeMapStore).m[i] = m
		},
		lookup: func(st any, i int, _ *endSizeBody, pc int32) (int32, bool) {
			e, ok := st.(*endSizeMapStore).m[i][int(pc)]
			return e, ok
		},
		tally: func(st any) (int, int, bool) {
			n, el := 0, 0
			for _, m := range st.(*endSizeMapStore).m {
				if m != nil {
					n++
					el += len(m)
				}
			}
			// No honest formula: the point of weighing the map is that its overhead is the
			// implementation's, not a number derivable here.
			return n, el, false
		},
	}
}

// endSizeRow is one variant's bill: the weighed structures, the fields that reach them, and the sum.
type endSizeRow struct {
	name             string
	shape, placement string
	// heap is the live-bytes delta and alloc the cumulative-allocation delta, both for the
	// structures only. Two mechanisms for one quantity; see `endSizeHeapLive` for why one is not
	// enough.
	heap  int64
	alloc int64
	// fieldBytes is the analytic half: what the fields on `binary.Func` cost across every function
	// in the corpus, fieldWidth their per-function width at the cheapest position fieldAt, and
	// fieldExact whether that width came from the real struct's layout or from the fields' own sizes.
	// fieldAppended is the same charge past the last field — the figure a hand-model produces — and
	// fieldData the fields' own width, which is what distinguishes "free, in padding" from "absent".
	fieldBytes    int
	fieldWidth    int
	fieldAt       int
	fieldAppended int
	fieldData     int
	fieldExact    bool
	// modBytes and modWidth are the same for fields on `binary.Module`, at position modAt. Only the
	// arena has any.
	modBytes int
	modWidth int
	modAt    int
	total    int64
	payload  int
	hasPay   bool
	nonNil   int
	slots    int
}

// endSizeOrNone renders an empty report field as a word rather than as nothing.
//
// A blank where a name or a list belongs reads as a rendering bug, and on this instrument the empty case
// is the *good* one — no row had spread, no adjacent pair is unresolved — so it is the case most worth
// stating in words.
func endSizeOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// endSizeWeighings is how many times each variant is weighed, the bill taking the minimum.
//
// Three, and the number is a bet on P(all K windows contaminated) rather than a precision knob: under
// one-sided contamination the minimum of K is wrong only if *every* one of the K windows caught a
// foreign allocation, and each extra weighing multiplies that probability by the per-window rate.
// Measured at roughly 44% of windows under the shuffled whole-module run, so three puts the bet near a
// tenth. Not two, because at that rate a pair is entirely dirty about a fifth of the time — and two
// readings cannot say which of them is clean, which is the ambiguity #570 was read wrongly through,
// where the pair was assumed cold-then-warm and the inflation turned up on both sides. Not more,
// because each weighing is a full build of the representation over the corpus.
//
// **When the bet loses, it loses quietly**, and no assertion here catches it: three dirty windows that
// happen to be inflated by the same amount report zero spread and read exactly like three clean ones.
// A row with no spread is the absence of evidence of contamination, never evidence of a clean window;
// what the printed spread bounds is this instrument's *jitter*, not its *bias*. The direction that
// would actually retire the bias is weighing in a process where nothing else allocates.
const endSizeWeighings = 3

// endSizeSpread is one variant's K readings and their extremes — the minimum is the estimator, and the
// distance to the maximum is what the resolution check has to be small against.
type endSizeSpread struct {
	allocs   []int64
	min, max int64
}

// endSizeWeighBest weighs one variant endSizeWeighings times and returns the cheapest reading's row.
//
// The minimum rather than the mean or the first, because `TotalAlloc` contamination only adds — see the
// resolution check in the pricing test for the derivation. The returned row is a whole row from the
// cheapest weighing rather than a row with a patched `alloc` field, so its `heap` cross-check and its
// slot tallies come from the same weighing as the number the bill quotes; a row assembled from two
// different weighings would make the printed `live/structures` ratio a comparison of unrelated
// readings.
func endSizeWeighBest(v endSizeVariant, bodies []endSizeBody, nmods int) (endSizeRow, endSizeSpread) {
	best := endSizeWeigh(v, bodies, nmods)
	sp := endSizeSpread{allocs: []int64{best.alloc}, min: best.alloc, max: best.alloc}
	for range endSizeWeighings - 1 {
		r := endSizeWeigh(v, bodies, nmods)
		sp.allocs = append(sp.allocs, r.alloc)
		if r.alloc < sp.min {
			sp.min, best = r.alloc, r
		}
		if r.alloc > sp.max {
			sp.max = r.alloc
		}
	}
	return best, sp
}

// endSizeWeigh builds one variant over every body and weighs the structures.
//
// The store is allocated *before* the baseline reading and the structures are held live across the
// second reading, so each variant is weighed against the same quiet baseline rather than against the
// previous variant's residue. `runtime.KeepAlive` is what makes the retention a fact rather than an
// intention: without it the compiler is entitled to drop the store before the measurement it exists
// for.
func endSizeWeigh(v endSizeVariant, bodies []endSizeBody, nmods int) endSizeRow {
	row := endSizeRow{name: v.name, shape: v.shape, placement: v.placement}

	st := v.store(bodies, nmods)
	baseLive, baseTotal := endSizeHeapLive()

	for i := range bodies {
		v.build(st, i, &bodies[i])
	}
	afterLive, afterTotal := endSizeHeapLive()
	row.heap = int64(afterLive) - int64(baseLive)
	row.alloc = int64(afterTotal) - int64(baseTotal)

	row.nonNil, row.slots, row.hasPay = v.tally(st)
	row.payload = 4 * row.slots

	runtime.KeepAlive(st)
	return row
}

// endSizeHeapLive settles the heap and reports live bytes and cumulative bytes allocated.
//
// # Why the bill is cumulative allocation and not live heap, which took two wrong instruments to reach
//
// This began as `HeapAlloc` before and after, differenced. On that instrument the arena row weighed
// **27424 B while holding 12485 `int32`s**, whose payload cannot be under 49940 B — a structure
// measured at 55% of the bytes it provably contains. The floor assertion in the pricing test is what
// caught it; nothing about the number looked wrong, it simply could not be true.
//
// Two hypotheses were tested and both were wrong, which is why this comment is long:
//
//  1. **Lazy sweeping.** `runtime.GC` completes the mark phase and leaves sweeping to proceed
//     lazily, so a baseline could count marked-dead-but-unswept garbage that the later reading no
//     longer counts, subtracting reclamation from the structure. Plausible, and false here:
//     `debug.FreeOSMemory` (a collection *and* a complete sweep) did not move the figure.
//  2. **An unsettled baseline.** Also false: three consecutive readings at the baseline returned the
//     identical `2859176`, so the reading is stable and reproducible.
//
// What remained measured but unexplained was that **40960 B — exactly 40 KiB — was live at the
// baseline and dead at the second reading**, while the arena allocations themselves were all retained.
//
// **That divergence no longer reproduces**, and this comment says so rather than describing a defect
// the code no longer has — which is grave #505's shape, one file over. The arena's weighing was
// rewritten when it was folded into the placement cross: it no longer allocates a wrapper struct per
// module, and its store is allocated before the baseline like every other. Live heap and cumulative
// allocation now agree to 1.000 on every row. The residue is left named rather than explained, because
// *an unexplained residue reported is worth more than a mechanism invented to cover it* and a
// disappearance is not an explanation either.
//
// The **reported bill is `TotalAlloc`** and stays so: cumulative bytes handed out by the allocator,
// never decremented, and therefore immune to reclamation timing by construction. It is also the right
// quantity on its own terms — "bytes the allocator gave this representation over the corpus, all of
// which it retains" — and it prices size-class rounding and map control words as the program pays
// them. Its one requirement is that builders allocate nothing they drop, which is why the pair
// shape's defensive `sort.Slice` had to go: 41512 B of reflection garbage inside the window made that
// row's two mechanisms disagree by a factor of two.
//
// `HeapAlloc` is kept as the printed cross-check, and where the two diverge the divergence is reported
// rather than resolved by picking the convenient one. *A delta from one broken instrument can be sound
// while both absolute levels are wrong* — there the delta was the broken part, and the analytic floor
// was the only thing in the room that could say so.
func endSizeHeapLive() (live, total uint64) {
	runtime.GC()
	debug.FreeOSMemory()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc, ms.TotalAlloc
}

// endSizeReport renders the bill, cheapest total first: the weighed structures, the analytic field
// terms, their sum, the sum's share of what the bodies already cost, and each variant's ratio against
// the cheapest total.
func endSizeReport(rows []endSizeRow, bodyBytes, instrSize, modules, funcs int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  the denominator: binary.Instr is %d B, so the decoded bodies this table would "+
		"sit beside already retain %d B\n"+
		"  the field terms are charged to all %d functions and all %d modules; the structures are "+
		"built only for the bodies that hold an opener\n\n"+
		"  the pairing table's bill over the whole corpus, cheapest first:\n",
		instrSize, bodyBytes, funcs, modules)

	sorted := make([]endSizeRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].total < sorted[j].total })

	cheapest := int64(-1)
	for _, r := range sorted {
		if r.total > 0 && (cheapest < 0 || r.total < cheapest) {
			cheapest = r.total
		}
	}
	for _, r := range sorted {
		ratio, share, perMod := "n/a", "n/a", "n/a"
		if cheapest > 0 && r.total > 0 {
			ratio = fmt.Sprintf("%.2fx", float64(r.total)/float64(cheapest))
		}
		if bodyBytes > 0 {
			share = fmt.Sprintf("%+.1f%% on bodies", 100*float64(r.total)/float64(bodyBytes))
		}
		if modules > 0 {
			perMod = fmt.Sprintf("%.1f B/module", float64(r.total)/float64(modules))
		}
		fmt.Fprintf(&b, "    %-62s %9d B  %7s  %-16s %-15s\n",
			r.name, r.total, ratio, share, perMod)
		exact := fmt.Sprintf("%d B of data at field position %d, %d B if appended",
			r.fieldData, r.fieldAt, r.fieldAppended)
		if !r.fieldExact {
			exact = "lower bound: a base struct has unexported fields and cannot be mirrored"
		}
		fmt.Fprintf(&b, "      structures %d B weighed  +  Func field %d B (%d B/func × %d; %s)",
			r.alloc, r.fieldBytes, r.fieldWidth, funcs, exact)
		if r.modBytes != 0 {
			fmt.Fprintf(&b, "  +  Module field %d B (%d B × %d at position %d)",
				r.modBytes, r.modWidth, modules, r.modAt)
		}
		b.WriteString("\n")
		pay := "      no honest formula for this shape — weighed only, which is why it is weighed"
		if r.hasPay {
			over := "n/a"
			if r.payload > 0 && r.alloc > 0 {
				over = fmt.Sprintf("%.2fx", float64(r.alloc)/float64(r.payload))
			}
			pay = fmt.Sprintf("      payload floor %d B (4 B per int32 slot, no overhead); "+
				"structures/floor = %s", r.payload, over)
		}
		fmt.Fprintf(&b, "%s\n      live-heap cross-check %d B (live/structures = %s)  ·  "+
			"%d structures, %d slots\n",
			pay, r.heap, endSizeAgree(r.heap, r.alloc), r.nonNil, r.slots)
	}
	b.WriteString("    the structures are cumulative allocation, the field terms are arithmetic over " +
		"the real struct layouts; ratio is against the cheapest total, never against a threshold.\n")
	return b.String()
}

// endSizeRankingTurnsOn decomposes the bill along each axis, so the ranking's dependence on this
// corpus is legible in the output rather than only in an ADR's prose.
//
// # It is computed, and the first version was a formula that went stale within one commit
//
// This began as two hand-derived thresholds — "the pointer beats the inline placement while the
// opener-bearing fraction is below 16/24" and "the arena beats the pointer while modules <
// opener-bearing functions". Both read correctly and both were **wrong about the table printed above
// them**, because each omitted a term: the arena's field lands in `Func`'s interior padding and costs
// *nothing*, where the pointer's cannot and costs 8 B on every function. So the arena wins on this
// corpus, and the sentence beneath the table said the pointer did.
//
// That is *the defect stated as the rule*, in a report about a measurement: a hand-derived relation
// sitting under a machine-derived table, agreeing with nothing, and read by the next person as the
// table's explanation. So the relations are gone and what is printed is the **difference of the actual
// term totals**, per shape, against that shape's cheapest placement. A decomposition cannot disagree
// with the row it decomposes.
//
// The corpus properties are still printed, because they are what a reader needs in order to know which
// way the ranking would move — but as measured properties with no threshold attached to them.
// The `base` is the struct the field charges are taken against — `endSizeUncommittedFunc`, not
// `binary.Func`, once the mechanism has landed. Passed in rather than read here, because a report that
// reads its own subject off the live tree can describe a different struct than the table above it does:
// this function printed *"the only interior hole in binary.Func is 0 B wide, and the arena's offset is
// what fits in it … which is why the arena leads the dense rows"* on a board where the arena was fifth.
// Every clause of that was false and the numbers in it were real, which is the shape.
func endSizeRankingTurnsOn(rows []endSizeRow, st endSizeStats, base reflect.Type) string {
	var b strings.Builder

	absorbed := endSizeGrowthOrMinusOne(base, endSizeInt32, false)
	appended := endSizeGrowthOrMinusOne(base, endSizeInt32, true)

	// Named rather than printed with %s: a mirror built by reflect.StructOf has no name, so %s dumps
	// the whole field list and the reader cannot tell it from `binary.Func` at a glance — which is the
	// one distinction this line exists to make.
	name := "binary.Func"
	if base != reflect.TypeOf(binary.Func{}) {
		name = "binary.Func less EndsOff (the field this decision added — see endSizeUncommittedFunc)"
	}

	b.WriteString("  what the ranking turns on:\n")
	fmt.Fprintf(&b, "    %s\n      has a %d B interior hole; an int32 field costs %d B at its cheapest "+
		"position against %d B appended, so a\n      representation wanting one is charged %d B over this "+
		"corpus and one wanting two is charged %d B.\n      A cost of 0 means the hole is unspent; a cost "+
		"equal to the appended figure means something already holds it.\n",
		name, endSizeHole(base), absorbed, appended,
		st.funcs*absorbed, st.funcs*(absorbed+appended))
	fmt.Fprintf(&b, "    opener-bearing fraction of functions: %.1f%% (%d of %d) — the population the "+
		"structures are built for,\n      against the whole %d that the Func field is charged to.\n",
		100*float64(st.bodiesWithOpener)/float64(st.funcs), st.bodiesWithOpener, st.funcs, st.funcs)
	fmt.Fprintf(&b, "    functions per module: %.2f (%d functions in %d modules) — the denominator the "+
		"arena's per-module header is spread over.\n",
		float64(st.funcs)/float64(st.modulesOK), st.funcs, st.modulesOK)

	// Group by shape and decompose each placement against that shape's cheapest, term by term. The
	// terms sum to the total by construction, which is the property a hand-written relation lacked.
	b.WriteString("\n  each shape's placements, decomposed against that shape's cheapest:\n")
	seen := map[string]bool{}
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		if !seen[r.shape] {
			seen[r.shape] = true
			order = append(order, r.shape)
		}
	}
	for _, shape := range order {
		group := make([]endSizeRow, 0, 3)
		for _, r := range rows {
			if r.shape == shape {
				group = append(group, r)
			}
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].total < group[j].total })
		fmt.Fprintf(&b, "    %s\n", shape)
		base := group[0]
		for _, r := range group {
			delta := "the cheapest placement for this shape"
			if r.total != base.total {
				delta = fmt.Sprintf("%+d B = structures %+d, Func field %+d, Module field %+d",
					r.total-base.total, int(r.alloc-base.alloc), r.fieldBytes-base.fieldBytes,
					r.modBytes-base.modBytes)
			}
			fmt.Fprintf(&b, "      %-40s %9d B  %s\n", r.placement, r.total, delta)
		}
	}
	return b.String()
}

// endSizeHole is the widest run of interior padding in a struct, measured by finding the widest `int32`
// -aligned field that can be inserted somewhere without growing it.
//
// Measured rather than read off the field list, because "there are 4 bytes free after `TypeIndex`" is
// exactly the kind of hand-computed layout claim `endSizeFieldGrowth` exists to replace. Zero means the
// struct has no interior hole and every added field costs its own width.
func endSizeHole(base reflect.Type) int {
	widest := 0
	for _, t := range []reflect.Type{
		reflect.TypeOf(int8(0)), reflect.TypeOf(int16(0)), endSizeInt32, reflect.TypeOf(int64(0)),
	} {
		if endSizeGrowthOrMinusOne(base, t, false) == 0 && int(t.Size()) > widest {
			widest = int(t.Size())
		}
	}
	return widest
}

// endSizeGrowthOrMinusOne is `endSizeFieldGrowth` for a single field, either at its cheapest position
// or appended, reduced to one number for the report. -1 when the struct cannot be mirrored.
func endSizeGrowthOrMinusOne(base, f reflect.Type, appended bool) int {
	c, err := endSizeFieldGrowth(base, []reflect.Type{f})
	if err != nil || !c.exact {
		return -1
	}
	if appended {
		return c.appended
	}
	return c.growth
}

// TestEndTableOpenersAreAscending asserts the ordering the pair shape's binary search needs, which
// `endSizeCorpus` produces incidentally by walking `pc` upwards.
//
// Its own test rather than a line in the pricing test, because it is a precondition of one shape and
// the pricing test would report it as a lookup failure — *a verdict with no located failure*. If this
// fires, three rows are measuring an unsorted run searched as if sorted, and the correctness gate
// would catch it only for the openers that happened to land in order.
func TestEndTableOpenersAreAscending(t *testing.T) {
	testenv.RequireSuite(t, endSizeSuiteDir)

	bodies, stats := endSizeCorpus(t)
	if stats.openers < 2000 {
		t.Fatalf("paired %d openers, want >=2000: an ordering claim over a population this small is "+
			"not the claim it reads as", stats.openers)
	}
	checked := 0
	for i := range bodies {
		for j := 1; j < len(bodies[i].openers); j++ {
			if bodies[i].openers[j-1].pc >= bodies[i].openers[j].pc {
				t.Fatalf("body %d: openers[%d].pc=%d is not below openers[%d].pc=%d — the pair shape "+
					"binary-searches this run and would silently miss",
					i, j-1, bodies[i].openers[j-1].pc, j, bodies[i].openers[j].pc)
			}
			checked++
		}
	}
	// The population this ordering claim covers is *adjacent pairs*, which is fewer than the openers:
	// a body with one opener contributes none. Stated as a floor over the right denominator rather
	// than over `openers`, since the two differ by 1267 here and a floor over the wrong one would be
	// a bound that never binds.
	if want := stats.openers - stats.bodiesWithOpener; checked != want {
		t.Errorf("checked %d adjacent opener pairs, want %d (%d openers − %d bodies holding them)",
			checked, want, stats.openers, stats.bodiesWithOpener)
	}
	t.Logf("ordering holds over %d adjacent opener pairs in %d bodies", checked, stats.bodiesWithOpener)
}

// endSizeAgree renders how far the two mechanisms agree for one row.
//
// Printed rather than asserted-equal, because the two do not measure the same thing: live bytes is
// what is retained, cumulative allocation is what passed through the allocator. For a builder that
// keeps everything they converge; a ratio far from 1 means the builder allocated something it dropped,
// which is a fact about the builder and belongs in the report rather than in an error.
func endSizeAgree(live, alloc int64) string {
	if alloc == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.3f", float64(live)/float64(alloc))
}
