// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The description-from-source tripwire, and it exists because inference was measured against the
// reference and lost.
//
// # The measurement
//
// The citation domain repair (#333) brought four more files under the range pins, and re-pinning
// required a description for each newly covered range. Six were written for files whose cited lines
// had not been read — from what the *Go* code around the citation appeared to be doing, which is the
// available reading and the wrong one. **Five of the six were wrong**, and the sixth was right
// because it was copied from a description someone had written from the reference:
//
//	written                                | valid.ml actually says
//	---------------------------------------+-----------------------
//	`check_elem`'s active arm              | the `TableInit` arm
//	the segment rules' region              | the table arms
//	`check_block`                          | `BrTable`
//	the blocktype lookup                   | `check_valtype`
//	the operand-stack type rule            | `check_block`
//
// All five *resolved*: every range was well-formed, inside the file, and (where keyable) contained
// its subject's message site. Nothing in this package could see them, because every existing citation
// check asks where a range points and none of them asks what the prose beside it *claims* the range
// contains. They were caught by reading `valid.ml` before the commit — a procedure, and one whose
// five-in-six error rate is the argument for not leaving it as one. (Ruling: Scott, PR #335 relay;
// the law is in `docs/laws/citations.md`.)
//
// # What is checked, and how the domain avoids being a list
//
// A citation line that names a reference-defined identifier in backticks is asserting a relationship
// between the two, and there are exactly two honest ones: the identifier is *in* the cited range, or
// the cited range is *inside* the identifier's own definition (a citation to three lines of
// `check_module`'s body names `check_module`). Either satisfies this check; neither holds for any of
// the five rows above.
//
// Both halves of the trigger are derived rather than enumerated, which is #333's lesson applied at
// the point of construction rather than after the grave:
//
//   - the **files** are globbed, and unlike `citationFiles` this glob keeps `_test.go` — a citation
//     is a citation wherever it is written, and the five specimens were written in a test file's pin
//     table;
//   - the **candidate identifiers** come from `valid.ml`'s own top-level bindings and constructor
//     arms, so `checkOffset` and `binary.Limits` are not candidates and a reference function this
//     package has never mentioned becomes one the moment a comment names it.
//
// # What it cannot see, stated because a coverage claim cannot check itself
//
// The window is **one line**. A description whose identifier wraps onto the line above is not keyed
// by this check — it lands in the residue count instead, which is pinned, so a wrap moves both pins
// and arrives loudly rather than silently. A two-line window was tried and is *worse*: on
// `ErrUnknownTag`'s comment it joined a clause naming the tag arm's own reference function to the
// next line's citation of the `lookup` family at `:40-49`, and reported that correct citation as
// wrong. A window wide enough to catch a wrap is wide enough to cross a clause boundary, and one of
// those two errors is silent.
//
// Residue is also where a genuinely region-shaped citation lands — `valid.ml:618-651` is "the table
// arms", a description with no single subject to name — and that is a fact about the reference rather
// than a gap. The count keeps it from being a silent exclusion.
//
// A line carrying several ranges is checked against *any* of them rather than each: two subjects and
// two ranges on one line are not claiming a pairing, and a per-clause parse is what a pairing check
// would need. Stated because it means a swapped pair inside one line survives this check.
//
// # It enforces agreement, not provenance — a lucky inference passes
//
// The rule this holds is *the description is written by reading the cited lines*, and that is a claim
// about **how the description was produced**, which no test can see. What this check asks is whether
// the description *agrees* with the lines — so it catches a description that disagrees, and a
// description inferred from the surrounding Go code that happened to land on the right subject passes
// it untouched. Agreement is the correct proxy and this is the right control; it is not the rule, and
// the gap between them is one-directional in the reassuring direction.
//
// Worth being exact about how the five specimens were actually found, since it is the same gap: they
// were caught by **reading `valid.ml`**, before any of this existed. Six descriptions, five wrong, and
// had the sixth been inferred rather than copied it would have been a sixth pass here with no more
// provenance than the five. So the measurement that minted this control is a measurement this control
// could not have taken — which is the honest statement of what it buys: it makes a *wrong* inference
// findable by machine, and leaves a *right* one indistinguishable from a reading. (Ruling: Scott, PR
// #337 relay.)
func TestRangeCitationSubjectsAreReadFromTheReference(t *testing.T) {
	ref := testenv.RequireSpecRef(t, testenv.RefValidML)
	lines := strings.Split(ref, "\n")
	defs, arms := refSubjects(lines)

	// Vacuity, and pinned exactly rather than floored — the same argument `wantCategories` makes a
	// file over: the reference is fetched at a pin, so upstream growing its vocabulary is a fact for
	// a reader to record rather than churn to absorb. A floor here would also be the wrong shape,
	// since the failure this guards is a regex that stopped matching and that failure is a *collapse*,
	// which any floor catches while hiding the interesting case of a few bindings going missing.
	const wantDefs, wantArms = 88, 170
	if len(defs) != wantDefs || len(arms) != wantArms {
		t.Fatalf("parsed %d top-level binding(s) and %d constructor arm(s) from %s, want %d and %d — "+
			"the candidate set is this trigger's domain, so a shrunken one makes citations unkeyable "+
			"and an empty one makes this test agree with everything",
			len(defs), len(arms), testenv.RefValidML, wantDefs, wantArms)
	}

	tickRe := regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_' ()]*)`")
	keyed, residue := 0, 0
	for _, file := range citingFilesWithTests(t) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			var ranges [][2]int
			for _, m := range rangeRe.FindAllStringSubmatch(line, -1) {
				lo, hi := atoiOrZero(m[1]), atoiOrZero(m[2])
				ranges = append(ranges, [2]int{lo, hi})
			}
			if len(ranges) == 0 {
				continue
			}
			var subjects []string
			for _, m := range tickRe.FindAllStringSubmatch(line, -1) {
				// `Select (Some ts)` names an arm and its payload; the arm is the subject.
				name := strings.Fields(m[1])
				if len(name) == 0 {
					continue
				}
				if _, ok := defs[name[0]]; ok || arms[name[0]] {
					subjects = append(subjects, name[0])
				}
			}
			if len(subjects) == 0 {
				residue++
				continue
			}
			for _, name := range subjects {
				keyed++
				if subjectAnswers(lines, defs, name, ranges) {
					continue
				}
				t.Errorf("%s:%d describes %s as %q and no reading of that citation holds: %q is "+
					"neither written inside %v nor is any of those ranges inside its own definition "+
					"at %v. The description was written from the code around the citation rather "+
					"than from the cited lines",
					file, i+1, testenv.RefValidML, name, name, ranges, defs[name])
			}
		}
	}

	// Both pinned, and separately, for the reason the sibling counts are: a description that stops
	// naming its subject moves a row from the checked column into the excused one, and that has to be
	// louder than a passing test with one fewer assertion in it.
	//
	// 27 keyed and 10 residue, and **six of the 27 are keyed because this PR named a subject that was
	// not there before** — `TableInit`, `BrTable`, `Select (Some ts)`, `check_block`, `check_limits`
	// and `check_memorytype`, which is the same list as the five wrong descriptions plus the one that
	// was right. A description that names its subject is a description someone had to read the
	// reference to write, so moving a row into the keyed column is the repair and not bookkeeping.
	//
	// The residue, in full, because an excused row that nobody can enumerate is an exclusion:
	// `vec.go`'s four section comments (:885-937, :906-908, :938-955, :663-686), `bulk.go`'s table-arms
	// region and this file's sentence about it (:618-651), `validate.go`'s slice-4 summary pointing at
	// `instr.go` (:442-446), and three rows in `vec_authority_test.go`'s own prose (:373-378, :390-393,
	// :41-42) that describe what another instrument keys rather than naming a reference subject.
	const wantKeyed, wantResidue = 27, 10
	if keyed != wantKeyed || residue != wantResidue {
		t.Errorf("keyed %d range citation(s) by named subject and left %d as residue, want %d and "+
			"%d — recount and re-pin. A row moves from residue to keyed when its description starts "+
			"naming the reference's own identifier, which is the direction this test wants, and the "+
			"pin is how the move gets read rather than absorbed", keyed, residue, wantKeyed, wantResidue)
	}
}

// subjectAnswers reports whether a citation's ranges are consistent with naming `name`.
//
// Two readings, both derived from the reference: the identifier appears within a cited range, or a
// cited range lies within the identifier's own definition. The second is not a weakening — a comment
// citing `valid.ml:1168-1169` and calling it `check_module`'s export phase is describing two lines of
// that function's body, and the function's name is nowhere in them.
func subjectAnswers(lines []string, defs map[string][2]int, name string, ranges [][2]int) bool {
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	span, defined := defs[name]
	for _, r := range ranges {
		if r[0] < 1 || r[1] > len(lines) || r[1] < r[0] {
			continue // malformed; TestReferenceRangeCitationsAreWellFormed owns that verdict
		}
		if word.MatchString(strings.Join(lines[r[0]-1:r[1]], "\n")) {
			return true
		}
		if defined && span[0] <= r[0] && r[1] <= span[1] {
			return true
		}
	}
	return false
}

// refSubjects parses `valid.ml`'s named subjects: top-level `let`/`and` bindings with the line span
// each occupies, and the constructor names that appear as match arms.
//
// Top-level means column zero. A local `let t1 = ...` inside a function body is not a subject a
// citation can name — the two the reference binds in `check_elem` are called `t1` and `t2`, and
// admitting them would key two rows on identifiers whose scope is four lines wide.
func refSubjects(lines []string) (map[string][2]int, map[string]bool) {
	bindRe := regexp.MustCompile(`^(?:let|and)\s+(?:rec\s+)?([a-z_][A-Za-z0-9_']*)`)
	armRe := regexp.MustCompile(`\|\s*([A-Z][A-Za-z0-9_']*)`)

	type binding struct {
		name string
		at   int
	}
	var order []binding
	arms := map[string]bool{}
	for i, l := range lines {
		if m := bindRe.FindStringSubmatch(l); m != nil {
			order = append(order, binding{m[1], i + 1})
		}
		for _, m := range armRe.FindAllStringSubmatch(l, -1) {
			arms[m[1]] = true
		}
	}
	defs := make(map[string][2]int, len(order))
	for k, b := range order {
		end := len(lines)
		if k+1 < len(order) {
			end = order[k+1].at - 1
		}
		// A name bound twice keeps its first start and its last end: `check_limits` is one binding,
		// but the reference does rebind a few helpers, and a span that ended at the first rebinding
		// would report a citation inside the second as unanswered.
		if span, ok := defs[b.name]; ok {
			defs[b.name] = [2]int{span[0], end}
			continue
		}
		defs[b.name] = [2]int{b.at, end}
	}
	return defs, arms
}

// citingFilesWithTests is citationFiles' domain plus this package's tests.
//
// Derived for #333's reason and *wider* than citationFiles for a reason of its own: the five wrong
// descriptions that minted this check were in `vec_authority_test.go`'s pin table, so a domain that
// excluded tests would have excluded every specimen. The two globs stay separate rather than one
// being widened — citationFiles feeds counts about the engine's citations, and folding test comments
// into those would move pins that mean something else.
func citingFilesWithTests(tb testing.TB) []string {
	tb.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		tb.Fatalf("globbing the package's sources: %v", err)
	}
	if len(files) < len(citationFiles) {
		tb.Fatalf("globbed %d .go file(s) and citationFiles holds %d — this domain is a superset of "+
			"that one by construction, so a smaller count means the glob ran somewhere else",
			len(files), len(citationFiles))
	}
	return files
}

// atoiOrZero is strconv.Atoi with the error dropped, safe here because rangeRe's groups are `\d+`
// and the caller bounds-checks the result against the reference's length anyway.
func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
