package testenv_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The §§2–5 litmus battery's pre-registration, checked against the contract it claims to quote
// (#10, ADR 0055).
//
// # What this control asserts, and the one thing it must never be read as asserting
//
// `docs/litmus-battery-preregistration.md` is **data**. Nothing in it runs; every case in it is
// blocked on a mechanism that is not on main. This control's green is a claim about *that document's
// agreement with the contract* — every clause in §§2–5 has an entry, every entry names a clause that
// exists, every quotation is really the contract's own words, every case carries the keys a case must
// carry — and it is **never** a conformance verdict about the engine. There is no boundary-memory-model
// green in this package and there will not be one until #554 and #543 are discharged.
//
// That distinction is Scott's structural constraint on this slice, in his words on the #569 review:
// *"The tables must not be readable as a passing suite. If they're data, there's no green to misread.
// If they're tests, they fail or skip loudly naming what's missing — never pass vacuously."* The tables
// are data, so there is one control beside them rather than eleven skipping tests, and its name says
// what it checks — the clause coverage — rather than naming the memory model. A name asserting the
// boundary model holds would be the whole misreading in one identifier, and it is not written out even
// as a counter-example: an invented `Test`-shaped token in a comment is a dangling citation, which is a
// finding `TestEveryCitedTestNameResolves` produced against this file's first draft.
//
// # Why a pre-registration is not a stillborn control
//
// `controls.md`'s stillborn family is an instrument that can never fire — a green meaning nothing. The
// worry was raised against landing these tables before any mechanism exists, and Scott ruled it a
// category error: *"A pre-registration asserts nothing about today's engine; its entire value is being
// un-editable by the PR that satisfies it. Landing after #554 destroys precisely that, since the
// tables would then be written by someone who has seen the mechanism."* This control is not stillborn
// on its own terms either: its subject is the document, the document exists, and it was watched die by
// injection — see the checks below, each of which names what breaks it.
//
// # The inverse tripwire, which is the half that fires later
//
// Checks 1–6 fire today. Check 7 is aimed at the failure this document is most likely to suffer *after*
// the mechanism lands: a case whose blocker is discharged while its status still reads `blocked — #554`,
// so the tables quietly describe a battery nobody wrote. A status of the form `implemented — ` followed
// by a test's name must name a test that resolves in `citationInventory`, which makes the transition
// checkable in one
// direction — and the other direction is the point: writing the test without updating the status leaves
// a `blocked` row citing a closed issue, which `citecheck`'s state-claim channel already sees.
//
// # Watched die, and the two injections that only looked like passes
//
// Every check below was falsified by injection before this file was trusted, and what each one produced
// is recorded beside the check rather than asserted in aggregate. Two of the thirteen injections first
// reported `ok`, and neither was a hole in the control: the `perl` substitution had not matched, so the
// document was unmodified and the run was measuring the unmutated file. That is *an unrun command looks
// like a pass* arriving twice inside a battery written to avoid it, and the fix is the one the lesson
// names — the mutation is diffed before its result is read, which is how both were caught. Re-run by
// line number, the missing-`Shape` injection produced the two FAILs it was aimed at; the drained-contract
// injection produced the first vacuity `Fatalf`.
//
// One injection is worth its own line because it fired *three* checks at once: renaming `### H-1` to
// `### G-3` reported H-1 as uncovered, G-3 as not a §§2–5 clause, and the case beneath it as discharging
// a clause it no longer sits under. Three unrelated readers converging on one edit is what makes the
// two-way coverage check more than a spelling test.
const litmusDoc = "docs/litmus-battery-preregistration.md"

// The document's grammar, as five patterns. `litmusEntryRE` shares `clause_test.go`'s clause shape
// deliberately — the whole point is that the two readers agree about what a clause name looks like —
// but it is written out rather than reused, because `contractClauseRE` anchors on the contract's `-
// **T-1.**` bullet form and this document's entries are `###` headings.
var (
	litmusEntryRE  = regexp.MustCompile(`^### ([A-Z][A-Za-z-]*-\d+) — \S`)
	litmusCaseRE   = regexp.MustCompile("^#### Case `([a-z0-9-]+)`$")
	litmusKeyRE    = regexp.MustCompile(`^- \*\*([A-Z][A-Za-z ]*):\*\* +(\S.*)$`)
	litmusImplRE   = regexp.MustCompile(`^implemented — (Test[A-Za-z0-9_]+)$`)
	litmusBlockRE  = regexp.MustCompile(`^blocked — #\d+`)
	litmusSectRE   = regexp.MustCompile(`^## §(\d+)\.`)
	litmusClauseRE = regexp.MustCompile(`^- \*\*([A-Z][A-Za-z-]*-\d+)\.\*\* *(.*)$`)
)

// The four shapes an entry may declare. `outcome` and `timing` clauses must carry at least one case;
// `structural` and `contract-deferred` clauses must carry none and must say why instead. That
// asymmetry is the reason the field exists: without it, a clause no litmus case can reach would be
// indistinguishable from a clause somebody forgot.
var litmusShapes = map[string]bool{
	"outcome":           true,
	"timing":            true,
	"structural":        true,
	"contract-deferred": true,
}

// The keys every case must carry. `Floor` is on this list for the reason ADR 0055 gives: a case that
// cannot say how often it reached its interleaving cannot distinguish *conforming* from *never raced*,
// which is the whole vacuity family in one line. `Arbiter` is on it because §4's cases pass on amd64 by
// construction, so a case with no stated arbiter is a case whose green may mean nothing on the platform
// it ran on.
var litmusCaseKeys = []string{"Discharges", "Allowed", "Forbidden", "Witness", "Floor", "Arbiter", "Status"}

// The keys a caseless entry must carry in place of cases — one of these two, since a clause may be
// unreachable because no interleaving can witness it (`structural`) or because the contract has not
// yet said what it means (`contract-deferred`).
var litmusWhyKeys = []string{"Why no litmus case", "Why no outcome set"}

type litmusCase struct {
	name string
	line int
	keys map[string]string
}

type litmusEntry struct {
	clause string
	line   int
	quote  string
	keys   map[string]string
	cases  []litmusCase
}

func TestEveryClauseInSectionsTwoThroughFiveIsPreregistered(t *testing.T) {
	entries := litmusEntries(t)
	clauses, text := contractSectionsTwoThroughFive(t)

	// Vacuity, both readers. Either one silently matching nothing would make every check below
	// assert its property of an empty set — and the coverage check in particular would pass in the
	// most misleading possible way, reporting full agreement between two empty maps. *Empty vs.
	// empty agrees perfectly*, so both sides are floored, and floored against the document rather
	// than against a remembered number: §§2–5 is where the clauses are, and if it ever has none the
	// contract has been gutted.
	if len(clauses) == 0 {
		t.Fatalf("%s: no clause definitions matched %s within §§2–5. Every coverage claim below "+
			"would then be a claim about nothing", contractDoc, litmusClauseRE)
	}
	if len(entries) == 0 {
		t.Fatalf("%s: no entries matched %s. This control's whole subject is missing, and a green "+
			"here would report the battery pre-registered when the document is unreadable",
			litmusDoc, litmusEntryRE)
	}

	// Check 1 — every clause in §§2–5 has an entry. Injection: renaming `### H-2` to `### H-2x`, so
	// the entry pattern no longer matches it, reported `clause H-2 (§5) has no entry`. Note what the
	// same edit did *not* do: it produced no check-2 FAIL, because a malformed heading is not an
	// entry at all — which is why check 2 is watched die by its own injection below.
	//
	// A duplicate entry is caught in the parser rather than here: renaming `### T-2` to a second
	// `### T-1` reported `clause T-1 has a second entry (the first is at line 94)` beside T-2's
	// absence. Two entries for one clause is two outcome sets with nothing saying which one the
	// battery registered.
	for clause, section := range clauses {
		if _, ok := entries[clause]; !ok {
			t.Errorf("%s: clause %s (§%d) has no entry in %s. A clause with no entry reads as "+
				"covered — the caseless clauses are in the document *saying* they are caseless, "+
				"which is the only way a reader can tell a deliberate gap from a forgotten one",
				contractDoc, clause, section, litmusDoc)
		}
	}

	// Check 2 — every entry names a clause that exists in §§2–5. Injection: renaming `### H-1` to
	// `### G-3` reported `entry G-3 is not a clause defined in §§2–5`, and G-3 is the interesting
	// choice rather than a made-up number — it is a real clause the tree cites hundreds of times, in
	// §9, whose oracle is the upstream spec suite rather than this contract. An entry pointing there
	// would be an outcome set checked against a clause about neutrality.
	for clause, e := range entries {
		if _, ok := clauses[clause]; !ok {
			t.Errorf("%s:%d: entry %s is not a clause defined in §§2–5 of %s. Either the clause "+
				"was renumbered — in which case this entry's outcome set now describes nothing — "+
				"or the entry belongs to a different battery with a different oracle (#406 is the "+
				"Go-runtime one)", litmusDoc, e.line, clause, contractDoc)
		}
	}

	shapes := map[string]int{}
	cases := 0

	for _, clause := range sortedKeys(entries) {
		e := entries[clause]

		// Check 3 — the quotation is the contract's own words. Whitespace is collapsed on both
		// sides because the contract hard-wraps at 88 columns and the document rewraps, so a
		// line-for-line comparison would fail on formatting and teach its author to reflow the
		// contract. Injection: changing B-MM-2's quote from "not only the futex word" to "not just
		// the futex word" reported the mismatch with both strings printed.
		if e.quote == "" {
			t.Errorf("%s:%d: entry %s carries no blockquote. The quotation is what lets a reader "+
				"check the clause reading rather than trust it, and an outcome set derived from "+
				"an unquoted clause is unreviewable", litmusDoc, e.line, clause)
			// The `ok` guard below is not a silent skip: it is false only for an entry naming a
			// clause §§2–5 does not define, and check 2 reports exactly that entry. Stated because
			// a lookup guard that swallows the interesting case is how a quotation check goes
			// vacuous, and the reason it does not here is a property of another check rather than
			// of this line.
		} else if want, ok := text[clause]; ok && !strings.Contains(normalizeSpace(want), normalizeSpace(e.quote)) {
			t.Errorf("%s:%d: entry %s quotes text that is not a contiguous part of the clause in "+
				"%s.\n  quoted:   %s\n  contract: %s\nEither the contract was amended — in which "+
				"case this entry's outcome set was derived from words that no longer bind — or the "+
				"quotation was paraphrased, which is the same defect written by hand",
				litmusDoc, e.line, clause, contractDoc, normalizeSpace(e.quote), normalizeSpace(want))
		}

		// Check 4 — the shape is one of the four, and it decides whether cases are required or
		// forbidden. Four injections, because the branches fail for four reasons: setting T-3's
		// shape to `latency` reported the unknown value with the permitted set; renaming SP-3's
		// `Why no litmus case` key reported a structural clause carrying neither of the two
		// permitted explanations; splicing a case under B-MM-4 reported a structural clause
		// carrying one; and renaming SP-1's `Shape` and `Blocked by` keys together reported both
		// as absent. That last one is the injection that first reported `ok` on a substitution
		// that never matched — see the note at the top of this file.
		shape := e.keys["Shape"]
		switch {
		case shape == "":
			t.Errorf("%s:%d: entry %s declares no `- **Shape:**`. Without it there is no way to "+
				"tell a clause that needs cases from one no interleaving can reach",
				litmusDoc, e.line, clause)
		case !litmusShapes[shape]:
			t.Errorf("%s:%d: entry %s declares shape %q, which is not one of %s",
				litmusDoc, e.line, clause, shape, sortedKeys(litmusShapes))
		default:
			shapes[shape]++
		}
		if e.keys["Blocked by"] == "" {
			t.Errorf("%s:%d: entry %s declares no `- **Blocked by:**`. Every case in this document "+
				"is blocked on something, and *a deferral's citation outlives its subject* — the "+
				"blocker is named so a reader can check whether it is still open",
				litmusDoc, e.line, clause)
		}

		switch shape {
		case "outcome", "timing":
			if len(e.cases) == 0 {
				t.Errorf("%s:%d: entry %s declares shape %q and carries no `#### Case` block. A "+
					"clause whose shape says an outcome tuple can reach it, with no tuple written "+
					"down, is the gap this whole document exists to make visible",
					litmusDoc, e.line, clause, shape)
			}
		case "structural", "contract-deferred":
			if len(e.cases) > 0 {
				t.Errorf("%s:%d: entry %s declares shape %q yet carries %d `#### Case` block(s). "+
					"If a case can reach the clause, the shape is wrong; if it cannot, the case "+
					"will pass without testing the clause",
					litmusDoc, e.line, clause, shape, len(e.cases))
			}
			if !hasAnyKey(e.keys, litmusWhyKeys) {
				t.Errorf("%s:%d: entry %s declares shape %q and carries none of %s. A caseless "+
					"clause has to say why no interleaving can reach it and what discharges it "+
					"instead, or it is indistinguishable from an omission",
					litmusDoc, e.line, clause, shape, litmusWhyKeys)
			}
		}

		for _, c := range e.cases {
			cases++

			// Check 5 — the required keys. Injection: deleting B-MM-1's `Forbidden` line
			// reported the missing key by case name and line.
			for _, k := range litmusCaseKeys {
				if strings.TrimSpace(c.keys[k]) == "" {
					t.Errorf("%s:%d: case %s carries no `- **%s:**`. The required keys are %s, "+
						"and the two most easily dropped — Forbidden and Floor — are the two "+
						"that decide whether the case can fail and whether it raced at all",
						litmusDoc, c.line, c.name, k, litmusCaseKeys)
				}
			}

			// Check 6 — the case discharges the clause it sits under. Injection: pointing
			// `sp4-...`'s Discharges at SP-1 reported the mismatch.
			if d := c.keys["Discharges"]; d != "" && !strings.HasPrefix(d, clause) {
				t.Errorf("%s:%d: case %s sits under entry %s but discharges %q. A case filed "+
					"under the wrong clause is an outcome set checked against the wrong words",
					litmusDoc, c.line, c.name, clause, d)
			}

			// Check 7 — the inverse tripwire. `blocked — #N` must cite an issue; an
			// `implemented — ` status must name a test that exists. Injection: setting
			// `t1-n-agents-block-simultaneously`'s status to name
			// `TestTheFirstAgentWaitsAndAChildWakesIt`, which has never existed in this tree,
			// reported the dangling name — exactly the FAIL whoever discharges #543 gets for
			// free. The census line then read `1 implemented, 10 still blocked`, so the count
			// moves with the claim rather than only the prose.
			status := c.keys["Status"]
			switch {
			case litmusBlockRE.MatchString(status):
			case litmusImplRE.MatchString(status):
			default:
				t.Errorf("%s:%d: case %s has status %q, which is neither `blocked — #N` nor "+
					"`implemented — TestName`. Those are the two states a pre-registered case "+
					"can be in, and a third one is how a case stops being either checkable or "+
					"honest", litmusDoc, c.line, c.name, status)
			}
		}
	}

	// The implemented half needs the tree's test names, fetched once rather than per case.
	defined, _ := citationInventory(t)
	implemented := 0
	for _, clause := range sortedKeys(entries) {
		for _, c := range entries[clause].cases {
			m := litmusImplRE.FindStringSubmatch(c.keys["Status"])
			if m == nil {
				continue
			}
			implemented++
			if !defined[m[1]] {
				t.Errorf("%s:%d: case %s reports status `implemented — %s`, and no test of that "+
					"name is declared anywhere in the tree. *A test name is a checkable "+
					"citation*: either the case was marked implemented before its test was "+
					"written, or the test was renamed and this row now claims coverage nothing "+
					"provides", litmusDoc, c.line, c.name, m[1])
			}
		}
	}

	// The census, printed on every run whether or not anything failed. It is here because the
	// number that matters about this document changes with the mechanism, not with the document:
	// how many cases are still blocked. A run that prints `11 blocked, 0 implemented` after #554
	// and #543 close is the rot this control's check 7 cannot see from the inside — nothing is
	// wrong with the file, and nothing has been written either.
	t.Logf("%s: %d entries over %d clauses in §§2–5; shapes %v; %d case(s), %d implemented, "+
		"%d still blocked", litmusDoc, len(entries), len(clauses), shapes, cases, implemented,
		cases-implemented)
}

// litmusEntries reads the pre-registration into entries keyed by clause. Continuation lines matter:
// the document wraps at 100 columns, so a key's value routinely spans three lines, and a reader that
// took only the first line would compare truncated quotations and see missing keys where the text is
// merely indented.
func litmusEntries(tb testing.TB) map[string]*litmusEntry {
	tb.Helper()

	path := filepath.Join(repoRoot, litmusDoc)
	blob, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading the pre-registration at %s: %v", litmusDoc, err)
	}

	entries := map[string]*litmusEntry{}
	var cur *litmusEntry
	var curCase *litmusCase
	var lastKey string
	inFence := false

	for i, line := range strings.Split(string(blob), "\n") {
		lineNo := i + 1

		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		if m := litmusEntryRE.FindStringSubmatch(line); m != nil {
			if prev, dup := entries[m[1]]; dup {
				tb.Errorf("%s:%d: clause %s has a second entry (the first is at line %d). Two "+
					"entries for one clause means two outcome sets, and nothing says which one "+
					"the battery is registered against", litmusDoc, lineNo, m[1], prev.line)
			}
			cur = &litmusEntry{clause: m[1], line: lineNo, keys: map[string]string{}}
			entries[m[1]] = cur
			curCase, lastKey = nil, ""
			continue
		}
		if cur == nil {
			continue // Preamble: the caveat, the provenance, how to read an entry, the sequencing.
		}

		if m := litmusCaseRE.FindStringSubmatch(line); m != nil {
			cur.cases = append(cur.cases, litmusCase{name: m[1], line: lineNo, keys: map[string]string{}})
			curCase = &cur.cases[len(cur.cases)-1]
			lastKey = ""
			continue
		}

		keys := cur.keys
		if curCase != nil {
			keys = curCase.keys
		}

		if m := litmusKeyRE.FindStringSubmatch(line); m != nil {
			lastKey = m[1]
			keys[lastKey] = m[2]
			continue
		}
		if lastKey != "" && strings.HasPrefix(line, "  ") {
			keys[lastKey] += " " + strings.TrimSpace(line)
			continue
		}
		lastKey = ""

		// The quotation belongs to the entry, never to a case: a case restating the clause would
		// be quoting a quotation, and check 3 would then verify the document against itself.
		if strings.HasPrefix(line, "> ") && curCase == nil {
			if cur.quote != "" {
				cur.quote += " "
			}
			cur.quote += strings.TrimSpace(strings.TrimPrefix(line, ">"))
		}
	}

	return entries
}

// contractSectionsTwoThroughFive returns the clauses of §§2–5 and each clause's own text. It parses
// the contract a second time rather than calling `contractClauses`, and the reason is the payload:
// that reader answers *which section is this clause in*, which is all its citation channel needs,
// while check 3 needs the clause's words. Sharing the section/clause patterns would couple two
// controls that fail for unrelated reasons — grave #34's shape — so they are stated independently and
// the coverage check is what holds them to the same answer.
func contractSectionsTwoThroughFive(tb testing.TB) (map[string]int, map[string]string) {
	tb.Helper()

	blob, err := os.ReadFile(filepath.Join(repoRoot, contractDoc))
	if err != nil {
		tb.Fatalf("reading the contract at %s: %v", contractDoc, err)
	}

	clauses := map[string]int{}
	text := map[string]string{}
	section, last := 0, ""

	for _, line := range strings.Split(string(blob), "\n") {
		if m := litmusSectRE.FindStringSubmatch(line); m != nil {
			section, _ = strconv.Atoi(m[1])
			last = ""
			continue
		}
		if section < 2 || section > 5 {
			continue
		}
		if m := litmusClauseRE.FindStringSubmatch(line); m != nil {
			clauses[m[1]] = section
			text[m[1]] = m[2]
			last = m[1]
			continue
		}
		// A clause's text continues on indented lines until the next bullet or a blank line. The
		// italic provenance paragraphs are part of it, deliberately: an entry may quote its
		// clause's provenance — B-MM-2's D20 note is the reason that clause exists — and a reader
		// that stopped at the normative sentence would call such a quotation a paraphrase.
		if last != "" && strings.HasPrefix(line, "  ") {
			text[last] += " " + strings.TrimSpace(line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			last = ""
		}
	}

	return clauses, text
}

func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func hasAnyKey(keys map[string]string, want []string) bool {
	for _, k := range want {
		if strings.TrimSpace(keys[k]) != "" {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
