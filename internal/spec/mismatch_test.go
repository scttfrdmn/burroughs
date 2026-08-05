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

// The `assert_return value mismatch` bucket, partitioned by cause and pinned as a claim.
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
func TestEveryValueMismatchIsDownstreamOfAFailedSetup(t *testing.T) {
	requireSuite(t)

	rows, byFile := mismatchRows(t)

	// The vacuity check, because a partition of nothing prints a tidy empty table and agrees with
	// every hypothesis. An empty bucket would make the claim below vacuously true.
	if len(rows) == 0 {
		t.Fatalf("no value mismatches on the board: this test's subject does not exist, which is a\n" +
			"finding about the instrument and not about the engine — if the bucket really drained,\n" +
			"retire this file and say so in the PR rather than leaving a green that asserts nothing")
	}
	t.Logf("value mismatches: %d across %d files", len(rows), len(byFile))
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

	if len(orphans) > 0 {
		t.Errorf("%d value mismatches are NOT downstream of a failed setup invoke.\n"+
			"That means the engine ran a module's setup successfully and then returned a wrong\n"+
			"value — an arithmetic or semantic defect, not a missing arm, and the first of its kind\n"+
			"here since the graves closed. Partition these by opcode before anything else:",
			len(orphans))
		for i, r := range orphans {
			if i >= 40 {
				t.Logf("  … and %d more", len(orphans)-40)
				break
			}
			t.Errorf("  %s:%d  want %s  got %s", r.file, r.line, r.expect, r.got)
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
