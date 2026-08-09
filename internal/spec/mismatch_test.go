// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The `assert_return value mismatch` bucket: **asserted empty**, and partitioned by cause when it
// is not.
//
// It was named TestEveryValueMismatchIsDownstreamOfAFailedSetup while the bucket had 280 rows and
// the claim was about their authorship. The bucket is now at zero, so the assertion is the count and
// the partition is the diagnosis that runs only if a row appears — see the inversion note in the
// body for why the file was re-pointed rather than retired. The old name is recorded here because a
// test renamed silently makes every citation to it unresolvable, and the history is the point of
// keeping the rest of this comment intact below.
//
// # Why this bucket needs an instrument and not an argument
//
// The board keys this bucket by a fixed string, because a vector wanting a *value* supplies no
// expected error text to bucket by. So every mismatch on the board arrives under one name, and
// that name cannot say whether the engine computed a wrong answer or never ran the code that
// would have written the right one. Those are opposite work plans: the first is a defect to
// excavate, the second drains when a missing arm lands.
//
// The question was asked because the bucket grew 48 → 280, and growth-with-surface reads as the
// signature of a shared semantic root — a family of wrong answers. **That reading was wrong, and
// this file is the measurement that killed it.** The bucket scales with *modules admitted*, not
// with executed arithmetic surface: as the text encoder stopped refusing these modules, more of
// them reached their asserts and failed behind the same missing arms. `execFailCeiling`'s comment
// in spec_test.go had it right.
//
// Measured over the vendored suite at the commit that added this file: **280 mismatches in 16
// modules, and in every one of those modules its own setup `(invoke …)` failed.** What the setups
// failed with names the work and it is all mechanism, no semantics:
//
//	22  interp: no arm for opcode fc 0a   (memory.copy — #7)
//	13  interp: no arm for opcode fc 0b   (memory.fill — #7)
//	10  no instance: cannot yet encode memory.init  (#8)
//	10  no instance: cannot yet encode table.init   (#8)
//	 7  no instance: cannot yet encode the call_indirect instruction  (#8)
//
// By value type the bucket is 280 `i32 want / i32 got` — no representation axis, no float
// semantics, no lane reads. There is no root to dig for.
//
// # Why it asserts rather than prints
//
// A probe that only logs is a printer, and this bucket will grow again with every admission wave.
// So the finding is pinned as a claim: **every value mismatch stands behind a failed setup invoke
// in its own module.** While that holds, the bucket is incompleteness and needs no investigation.
// When it breaks, the engine is returning a wrong value on a module whose setup ran — which is
// this project's first such case since the graves closed, and it should arrive as a red board and
// a named line number, not as a number nobody re-partitioned.
//
// The finding and its history are #140.
//
// Its stated blind spot: a genuine arithmetic defect inside a module whose setup *also* fails is
// masked, because the mismatch is attributable either way. That is a limit of the discriminator,
// not a hole in it — the masked population shrinks to nothing as the missing arms land, which is
// the same work the assertion points at.
//
// # Two instruments corrected on the way to the answer
//
// Both are recorded because each produced a confident wrong reading first, and neither was caught
// by the board.
//
// **The first discriminator read the shape of `Got` and was backwards.** It classified non-zero
// results as "the engine computed something and it was wrong" — 247 of 280. But `checkRange`
// returns *an index*, so a non-zero result is precisely what an **un-copied** memory produces. A
// heuristic over the value is a regexp wearing a `_test.go` suffix; what answers the question is
// replaying the setup commands and reading the harness's own verdict on them.
//
// **The second discriminator was unanimous because it could not dissent.** Scoped to "any earlier
// failed invoke in the file", it was true at nearly every line of a 5,000-line file holding 14
// failures, so it returned `downstream` for everything — reproducing the expected answer while
// being structurally incapable of any other. Exactly zero disagreement on an agreement is the
// tell, and the repair is scope: per-module span, which can dissent and is proven to below.
func TestValueMismatchBucketIsEmptyAndSaysWhoWroteAnyRow(t *testing.T) {
	requireSuite(t)

	rows, byFile := mismatchRows(t)

	// **The bucket is at zero, and this is the re-pointing rather than the retirement.**
	//
	// The vacuity check below used to fire on an empty bucket and instruct its reader to retire this
	// file — an instruction written when a drained bucket could only mean the census had lost its
	// subject. It is preserved verbatim here rather than deleted, because an overridden instruction
	// that leaves no trace is the stale-claim defect run backwards: the next reader would find a file
	// asserting the opposite of what its history says and no record of the ruling in between.
	//
	//	no value mismatches on the board: this test's subject does not exist, which is a
	//	finding about the instrument and not about the engine — if the bucket really drained,
	//	retire this file and say so in the PR rather than leaving a green that asserts nothing
	//
	// The bucket drained (308 → 0 on the default lane, 609 → 0 with every gate on) and the
	// instruction is **not** followed, because *a tripwire whose subject dissolves is re-pointed,
	// never closed*: the risk this file names is "the engine returns a wrong value on a module that
	// ran", and that risk did not go anywhere. Only its current population did.
	//
	// **A file's local instruction does not outrank the project's law of record**, and that
	// precedence is the ruling rather than this author's reading (Scott, on the PR that drained the
	// bucket). The self-retirement order was authored before the tripwire law existed, or without
	// consulting it; a file obeys the project, not itself. What makes the override safe rather than
	// presumptuous is that the inverted assertion was **watched die** — see the paragraph below on
	// the `k+1` mutation — so what replaced the instruction is a live control and not a green with
	// better manners, which is precisely what the instruction existed to prevent.
	//
	// So the direction inverts. An empty bucket is now the **expected** state and a *non*-empty one
	// is the finding — which is strictly the stronger claim, since every row that appears from here
	// on has no missing arm to hide behind. The classifier below still runs when rows exist and
	// still partitions them; TestMismatchClassifierCanDissent is what keeps it alive across the
	// interval where it has nothing to classify, and that test was written for exactly this
	// eventuality (it asserts on synthetic states "independent of whatever the corpus happens to
	// contain this month").
	//
	// How it drained, measured on `(file, line)` keys: **284 of the 308 became passes** and 24
	// stayed red under the linking frontier — the bulk trio's arms landing (`fc 0a`, `fc 0b`,
	// `fc 0e`), which were three of the five causes this file's own table names as the work. That is
	// a bucket answered rather than reclassified, and it is the outcome the census was built to
	// point at.
	//
	// **The inverted direction was watched die, and the classifier dissented for the first time on
	// real data.** Mutating `execMemoryFill` to write `k+1`: 239 rows appear, and the partition reads
	// `0 downstream / 239 not downstream` — the arm below fires with "the engine ran a module's setup
	// successfully and then returned a wrong value", which is the verdict this file exists to
	// deliver and had never once delivered. Before the trio landed, that same mutation would have
	// been masked in every module whose setup also failed, which is the blind spot named further
	// down; it is no longer masked, because the setups now succeed. The census got sharper by the
	// bucket draining, which is the argument against retiring it stated as a measurement.
	// **The bucket is no longer empty, and the answer is a per-row registry rather than a raised
	// count.** 0017 Q1's registry admitted a population of cross-module vectors, and 22 of them
	// return a wrong value. A count would have been the cheaper move and is the wrong one: the
	// three causes below have three different work plans, and a single number cannot say which of
	// them a future change fixed or broke. So each row is named with the mechanism that authored
	// it, on TestGatedVectors' shape and for its reason — an unexplained entry is a suppression
	// wearing a disguise.
	//
	// The direction the comment above inverted stands: an *unlisted* row is the finding. What
	// changed is that "expected" is now a set rather than the empty set.
	//
	// **Three causes, and the second lane is what separated them.** With every gate on the bucket
	// holds **17**, so the 5 that vanish there are gate cascades and the 17 that survive are real.
	// Of those 17, two are the encoder and 15 are one engine defect. Both partitions sum, and they
	// cross-cut: `linking.wast` contributes to all three.
	//
	//	15  Q2's funcref identity (0017)  — a table slot's funcref carries a bare module-local
	//	                                    index, so a `call_indirect` through an imported table
	//	                                    resolves in the wrong instance
	//	 5  multi-memory gate cascade     — the module that *writes* the memory is declined, so the
	//	                                    reader's honest answer is a wrong one
	//	 2  encoder: no (start …) field   — the module that would write the memory cannot be emitted
	//
	// **The 15 are a filed accept-direction defect, not an excused row.** The identity confusion is
	// invisible whenever the two instances agree about an index, which is why no rejection vector
	// can score it (§9 G-3) and why it is quoted with its arithmetic here: `linking.wast:410`
	// expects `0` from `$f` and gets `4`, which is `$Mt`'s funcidx 0 (`$g`, `i32.const 4`) — the
	// same *number* the wrong instance holds at the same index. Q2 is where 0017 put the widening
	// and this is the population that will collect it.
	//
	// **Landed, grave #163.** The 15 are gone from `expectedMismatches` above and this paragraph
	// stays, era-stamped, because it is the record of what the bucket looked like with the defect
	// still live — the same "append rather than edit" rule the ADR's own census correction
	// follows. The bucket now holds the 7 that remain: 5 multi-memory, 2 encoder, both unrelated
	// to Q2 and unmoved by it.
	if len(rows) == 0 {
		t.Errorf("value mismatches: 0, where %d rows are registered below.\n"+
			"Every registered row names a live defect or frontier, so an empty bucket means either\n"+
			"one of the three causes was fixed without this list being updated, or the corpus moved.\n"+
			"Neither is a green: retire the entries that are genuinely answered and say so in the PR.",
			countMismatchRegistry())
		return
	}
	t.Logf("value mismatches: %d across %d files", len(rows), len(byFile))

	// The forward direction: a row nobody diagnosed.
	for _, r := range rows {
		if _, ok := expectedMismatches[r.file][r.line]; !ok {
			t.Errorf("%s:%d is a value mismatch with no registered cause (want %s, got %s).\n"+
				"\tA row here has no missing arm to hide behind: diagnose it against the all-gates-on\n"+
				"\tlane first — surviving there means the engine, vanishing there means a gate cascade.",
				r.file, r.line, r.expect, r.got)
		}
	}
	// The reverse, which is the direction that matters more: a registered row that stopped
	// mismatching is a defect *fixed* and a list gone stale, and a stale list overstates how much
	// of this bucket is understood.
	seen := map[string]bool{}
	for _, r := range rows {
		seen[fmt.Sprintf("%s:%d", r.file, r.line)] = true
	}
	for f, lines := range expectedMismatches {
		for line, why := range lines {
			if !seen[fmt.Sprintf("%s:%d", f, line)] {
				t.Errorf("%s:%d is registered as a value mismatch (%s) but no longer mismatches;\n"+
					"\tremove the entry — and if its cause is answered, the other rows sharing that\n"+
					"\tcause should have moved too", f, line, why)
			}
		}
	}
	for _, k := range sortedByCount(byFile) {
		t.Logf("  %5d  %s", byFile[k], k)
	}

	// By type pair, printed because a differing pair would be a *representation* defect and a
	// matching pair an arithmetic one — different investigations, and worth seeing before either.
	byTypes := map[string]int{}
	for _, r := range rows {
		byTypes[mismatchValType(r.expect)+" want / "+mismatchValType(r.got)+" got"]++
	}
	for _, k := range sortedByCount(byTypes) {
		t.Logf("  %5d  %s", byTypes[k], k)
	}

	setups := map[string]setupState{}
	for f := range byFile {
		setups[f] = setupOutcomes(t, f)
	}

	// What the failing setups failed with — the axis that names the work, printed every run so a
	// PR quotes a measurement rather than this comment's frozen numbers.
	t.Logf("--- what the failing setup invokes failed with ---")
	causes := map[string]int{}
	for f := range byFile {
		for k, n := range setupFailureCauses(t, f) {
			causes[k] += n
		}
	}
	for _, k := range sortedByCount(causes) {
		t.Logf("  %5d  %s", causes[k], k)
	}

	// The claim.
	var orphans []mismatchRow
	spans := map[string]int{}
	for _, r := range rows {
		st := setups[r.file]
		if st.failedInSpanOf(r.line) {
			spans[fmt.Sprintf("%s:%d", r.file, st.spanStart(r.line))]++
			continue
		}
		orphans = append(orphans, r)
	}
	t.Logf("--- author of the wrong value ---")
	t.Logf("  %5d  downstream of a failed setup in the same module, across %d modules",
		len(rows)-len(orphans), len(spans))
	t.Logf("  %5d  not downstream", len(orphans))

	// **The orphan count is printed, and the assertion it used to carry has moved above.** It
	// asserted that no mismatch stands on a module whose setup succeeded, and the registry's 22
	// are all exactly that: **0 downstream, 22 not**, which is the arm firing for the reason it
	// was written and delivering a verdict this file had never delivered before the trio landed.
	//
	// Erroring here *as well* would report the same 22 rows twice under two claims — once as
	// undiagnosed and once as not-downstream — so the diagnosis is the assertion and the
	// discriminator is the *evidence* for it. That is the ordering the header's own history asks
	// for: the discriminator's job was always to answer "which work plan", and it has, so what
	// survives is the answer keyed per row rather than a second count of the same population.
	//
	// The classifier keeps its teeth without asserting here — `TestMismatchClassifierCanDissent`
	// falsifies it on synthetic states, which is exactly what that test was written to do "across
	// the interval where it has nothing to classify". This is now the interval where it has
	// something to classify and nothing to *veto*.
	if len(orphans) > 0 {
		t.Logf("%d of these stand on a module whose setup succeeded — a wrong value rather than a\n"+
			"missing arm, which the registry above diagnoses per row:", len(orphans))
		for i, r := range orphans {
			if i >= 40 {
				t.Logf("  … and %d more", len(orphans)-40)
				break
			}
			t.Logf("  %s:%d  want %s  got %s", r.file, r.line, r.expect, r.got)
		}
	}
}

// TestMismatchClassifierCanDissent is the falsification, kept rather than performed once.
//
// The classifier's verdict was unanimous — 280 of 280 downstream — and a predicate that always
// answers the same way is indistinguishable, on any board, from one that answers correctly. The
// first version of it *was* that: scoped to the whole file, it could not return false. So the
// discriminating power is asserted directly, on synthetic states rather than on the corpus, which
// is what makes it independent of whatever the corpus happens to contain this month.
//
// This is the birth requirement: a control isn't born until it has been watched die. Both
// directions are pinned, because a classifier that can only say "downstream" and one that can
// only say "not downstream" are the same failure wearing opposite signs.
func TestMismatchClassifierCanDissent(t *testing.T) {
	st := setupState{
		moduleLines: []int{10, 100, 200},
		failedLines: []int{15, 210},
		okLines:     []int{110},
	}
	for _, tc := range []struct {
		name string
		line int
		want bool
	}{
		// Module at 10 has a failed setup at 15: a mismatch after it is downstream.
		{"after a failed setup in the same module", 20, true},

		// Module at 100 has a *successful* setup at 110. This is the row that proves dissent: the
		// whole-file predicate reported true here, because line 15 failed earlier in the file, and
		// the per-module predicate reports false because line 15 belongs to another module.
		{"after a successful setup in the same module", 120, false},

		// Same module, but *before* its setup ran. Not downstream of anything.
		{"before the setup in the same module", 105, false},

		// Module at 200 fails at 210; a mismatch at 205 precedes it.
		{"before a later failure in the same module", 205, false},
		{"after that failure", 220, true},

		// A line preceding every module: no span, nothing upstream.
		{"before every module", 5, false},
	} {
		if got := st.failedInSpanOf(tc.line); got != tc.want {
			t.Errorf("failedInSpanOf(%d) [%s] = %v, want %v", tc.line, tc.name, got, tc.want)
		}
	}

	// And the degenerate state: no recorded failures at all must flip every answer to false. This
	// is the whole-corpus falsification reduced to a unit — running the real classifier over the
	// suite with failures suppressed flipped all 280, and this asserts the same property without
	// a five-second replay.
	empty := setupState{moduleLines: []int{10}, okLines: []int{15}}
	for _, line := range []int{5, 12, 20, 1000} {
		if empty.failedInSpanOf(line) {
			t.Errorf("failedInSpanOf(%d) with no failures = true, want false: the classifier is\n"+
				"reporting downstream without evidence, which is how a unanimous verdict is\n"+
				"manufactured", line)
		}
	}
}

// expectedMismatches is every value mismatch on the default board, with the mechanism that
// authored it — file → line → cause.
//
// **Read from both lanes and printed, never inferred from the value.** The first version of this
// file's discriminator guessed from the shape of `Got` and was backwards (see the header), so each
// row's cause here is established by two measurements: whether it survives the all-gates-on lane,
// and what the module upstream of it actually reported. The arithmetic that identifies the funcref
// rows is quoted at the call site above.
var expectedMismatches = map[string]map[int]string{
	// `$M` at :1 owns one memory; the module at :10 imports it and declares a second, so its
	// memargs carry flags bit 6 and multi-memory declines it. Its data segment is what would
	// write 1..5 into `$M`'s memory, so `$M` honestly reads 0. Gone with every gate on, which is
	// how the cause was separated from the funcref rows.
	//
	// **`table_grow.wast`'s and `table_get.wast`'s entries retired, #196/#197.** Both used to
	// name the harness's own `readConst` gap (wast.go, pre-#196) as the cause: `grow`'s/`init`'s
	// ref-typed argument scored `unsupported` rather than running, so the table genuinely never
	// grew/was never written and the downstream `size`/`is_null-funcref` read was honestly
	// stale. #196/#197 close exactly that gap — `readConst` now reads `ref.null`/`ref.extern`,
	// and the boundary accepts the resulting argument — so both setup invokes run and both rows
	// stopped mismatching; `TestValueMismatchBucketIsEmptyAndSaysWhoWroteAnyRow`'s own
	// falsifiability check is what caught the entries going stale (a registered mismatch that no
	// longer mismatches), which is the control working exactly as designed rather than a defect
	// to route around.
	"load1.wast": {
		25: "multi-memory: the writer module at :10 is declined, so $M's memory was never written",
		26: "multi-memory: the writer module at :10 is declined, so $M's memory was never written",
		27: "multi-memory: the writer module at :10 is declined, so $M's memory was never written",
		28: "multi-memory: the writer module at :10 is declined, so $M's memory was never written",
		29: "multi-memory: the writer module at :10 is declined, so $M's memory was never written",
	},
	// The paradigm file. `:609` is the encoder gap; the nine Q2 rows this map held through
	// linking.wast:342-353/410/423, elem.wast:959/960/972/973/974 and linking0.wast:42 are gone
	// — grave #163 (0017 Q2): `ref` gained an `Inst *Instance` field naming the instance a
	// funcref's index belongs to, and `call_indirect` resolves through it instead of through
	// the caller. Confirmed by the full-board bucket join, not by re-reading this file: 1228 →
	// 1213, all 15 departures from `assert_return value mismatch`, zero arrivals elsewhere.
	"linking.wast": {
		609: "encoder: the module at :592 carries (start $main), which the emitter cannot write (#8)",
	},
	"linking3.wast": {
		82: "encoder: the module at :65 carries (start $main), which the emitter cannot write (#8)",
	},
}

// countMismatchRegistry is the registry's size, for the vacuity message. A literal would be a
// second place holding the same fact, which is the shape three graves in this repo share.
func countMismatchRegistry() int {
	n := 0
	for _, lines := range expectedMismatches {
		n += len(lines)
	}
	return n
}

type mismatchRow struct {
	file   string
	line   int
	expect string
	got    string
}

// mismatchRows replays every board file and collects the value-mismatch failures the board
// records, keyed as the board keys them. It reads the harness's `Failure` records rather than its
// log: a key names what a vector *wanted*, and the cause lives in `Got`, so a census over log text
// reports the wrong buckets. (That lesson was paid for by the `execFailCeiling` census — see PR
// #137's description for the history.)
func mismatchRows(t *testing.T) ([]mismatchRow, map[string]int) {
	t.Helper()
	var rows []mismatchRow
	byFile := map[string]int{}
	for _, f := range boardFiles(t) {
		s, err := ParseFile(filepath.Join(suiteDir, f))
		if err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		for _, fail := range run(s).Buckets["assert_return value mismatch"] {
			rows = append(rows, mismatchRow{file: f, line: fail.Line, expect: fail.Expect, got: fail.Got})
			byFile[f]++
		}
	}
	return rows, byFile
}

// setupState records a file's module boundaries and the outcome of each top-level `(invoke …)`.
//
// The span scoping is load-bearing rather than tidy: `memory_copy.wast` holds 14 failed setups
// across 5,000 lines and 33 modules, so a whole-file predicate is true almost everywhere and
// cannot dissent. `TestMismatchClassifierCanDissent` pins that this one can.
type setupState struct {
	failedLines []int
	okLines     []int
	moduleLines []int
}

// spanStart is the line of the module a given line belongs to: the last module command before it,
// or 0 when the line precedes every module.
func (s setupState) spanStart(line int) int {
	start := 0
	for _, m := range s.moduleLines {
		if m < line {
			start = m
		}
	}
	return start
}

// failedInSpanOf reports whether a setup invoke failed between the given line's module and the
// line itself — the one discriminator, with one implementation, so the two callers cannot drift.
func (s setupState) failedInSpanOf(line int) bool {
	start := s.spanStart(line)
	for _, l := range s.failedLines {
		if l > start && l < line {
			return true
		}
	}
	return false
}

// setupOutcomes replays a file and reports which of its bare `(invoke …)` commands failed. The
// outcome is read off the board's own buckets by line rather than re-executed here — one
// authority, not a second opinion that could disagree with the board it is explaining.
func setupOutcomes(t *testing.T, file string) setupState {
	t.Helper()
	s, err := ParseFile(filepath.Join(suiteDir, file))
	if err != nil {
		t.Fatalf("%s: parse: %v", file, err)
	}
	var st setupState
	invokeLines := map[int]bool{}
	for _, c := range s.Commands {
		if c.Kind == KindInvoke {
			invokeLines[c.Line] = true
		}
		if c.Head == "module" {
			st.moduleLines = append(st.moduleLines, c.Line)
		}
	}
	failed := map[int]bool{}
	for _, fs := range run(s).Buckets {
		for _, f := range fs {
			if invokeLines[f.Line] {
				failed[f.Line] = true
			}
		}
	}
	for l := range invokeLines {
		if failed[l] {
			st.failedLines = append(st.failedLines, l)
		} else {
			st.okLines = append(st.okLines, l)
		}
	}
	sort.Ints(st.failedLines)
	sort.Ints(st.okLines)
	sort.Ints(st.moduleLines)
	t.Logf("  setup invokes in %s: %d ok, %d failed, across %d modules",
		file, len(st.okLines), len(st.failedLines), len(st.moduleLines))
	return st
}

// setupFailureCauses buckets a file's failed setup invokes by the engine's error text, which is
// what names the work: `no arm for opcode fc 0a` is #7, `cannot yet encode …` is #8.
func setupFailureCauses(t *testing.T, file string) map[string]int {
	t.Helper()
	s, err := ParseFile(filepath.Join(suiteDir, file))
	if err != nil {
		t.Fatalf("%s: parse: %v", file, err)
	}
	invokeLines := map[int]bool{}
	for _, c := range s.Commands {
		if c.Kind == KindInvoke {
			invokeLines[c.Line] = true
		}
	}
	causes := map[string]int{}
	for k, fs := range run(s).Buckets {
		for _, f := range fs {
			if invokeLines[f.Line] {
				causes[k]++
			}
		}
	}
	return causes
}

func mismatchValType(s string) string {
	// `Expect` renders as "result N of fn: i32 1" and `Got` as "i32 1": take the atom after the
	// last colon-space, then its first word.
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

func sortedByCount(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		if m[ks[i]] != m[ks[j]] {
			return m[ks[i]] > m[ks[j]]
		}
		return ks[i] < ks[j]
	})
	return ks
}
