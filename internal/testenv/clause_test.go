package testenv_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The contract-clause citation channel, ADR 0046's instrument (#442).
//
// # The defect that bought it
//
// §9 G-3 was cited 243 times in this tree and **not once for what it says**. Its text names where the
// neutrality guarantee lives; the 243 citations all meant a different proposition — that a corpus of
// rejections cannot witness an accept-direction defect — which §9 stated nowhere. The repair was a
// dated rider on G-3 ratifying the reading the tree had always used. This file is the channel's first
// instrument.
//
// # What it can and cannot do, stated up front
//
// **It would not have caught that defect.** G-3 exists; every clause token in the tree resolves, both
// before the amendment and after it. Whether a clause *supports* a proposition is not mechanizable from
// the clause's text, and a control that claimed otherwise would be worse than none.
//
// What it does catch is the mechanical half — a clause token whose number is outside the range its
// family defines, and a citation that writes the wrong section beside a clause that does exist. What it
// *shows* is the census, printed on every run whether or not anything fails: the citation traffic per
// clause, most-cited first, with the share each clause carries and the count of clauses carrying none.
// A misdirected citation is not detectable; a clause with three orders of magnitude more incoming
// citations than its neighbours is visible, and visible is what nobody had.
//
// **No figure from that census is repeated in this comment.** They move with every commit that mentions
// a clause — including commits to this file — so a number written here is a claim that goes stale
// silently while reading as current. CLAUDE.md carries the same rider about itself for the same reason:
// *ask the instrument*. The figures below that do appear are stamped to the revision or the run they
// were taken from, which is what makes them checkable rather than merely present.
//
// # Why the domain is the tree and not a diff
//
// `citecheck.sh --pr` was the home #442's body proposed. It sees a diff and a PR body, and the whole
// population here is standing text written before any check existed — at `30377aa`, 337 citations across
// 95 Go files, 16 markdown files, `scripts/`, and the CI workflow. A diff-scoped resolver would report
// the channel checked while every one of those stayed outside its domain. #466 performed exactly this
// widening on `TestMarkdownLinksResolve` for the same reason: *the corpus cites itself, and a heading
// rename breaks incoming citations no control could see.* A clause renumbering is that rename with a
// different token shape.
//
// # Why the domain is raw text and not Go comments
//
// `citation_test.go` reads its citations out of the parser's comment map, on the stated ground that *a
// name inside a string literal is not a citation*. That ground does not transfer here, and the
// difference is measured rather than assumed: **51 clause tokens in this tree live in Go string
// literals, and outside this file's own fixtures they are all genuine citations** — test failure
// messages of the form `denoting a different instruction (§9 G-3)`, in `internal/text` and
// `internal/validate`. A clause citation's natural home includes the message a developer reads when the
// test fires. Restricting to comments would have dropped ~37 real citations and gone blind to a
// renumbering that broke every one of them, so the domain is bytes and the cost is paid on the
// fixture side instead — see fixtureClauses.
//
// # Why the left word boundary is load-bearing
//
// The first census run reported a §5 clause the contract does not define. The token was `GH-7`, in a
// fixture inside `closebody_test.go`, whose last three characters are shaped exactly like a clause
// reference. That would have been this control's first FAIL and its first false one. A trigger without a
// left boundary fires on every `GH-N` in the tree and teaches its author to phrase around it, which is
// *an exemption inherits none of the trigger's lessons* arriving before the exemption is written. Both
// patterns below anchor on `\b`, and `TestClauseScanClassifiesItsFixtures` keeps that token as a
// permanent negative fixture.
//
// # This file was inside its own population, and the corpus had already ruled on that
//
// The first run reported **12 findings in 2 files: 6 here and 6 in ADR 0046** — prose explaining what a
// dangling clause reference looks like, illustrated by writing one. `citation_test.go` paid for this
// exact shape and ruled: *when a control fires on its own explanation, **fix the explanation***, because
// exempting "prose about the class" buys a green by blinding the control to the population most likely
// to hold a stale reference. So ten of the twelve were repaired in prose — the rule is now *described*
// where a draft illustrated it, and the fixtures below carry their specimens in a deliberately partial
// vocabulary rather than in invented tokens.
//
// **The other two are a genuinely different class and get the one exemption in this file.** ADR 0046's
// rejected-alternatives section names a clause that was considered and not created. That is not a typo
// dressed as prose: a decision record cannot state which option it declined without naming it, and the
// ADR corpus will keep producing them. See newClauseLead.
//
// # The apparatus is in its own sample, and the census says how much
//
// Landing this file changed the distribution it was built to reveal. Its prose and fixtures cite clauses
// too: on the run that landed it, **61 of 447 outside citations — 14% — were this file**, and three
// clauses went from cited-nowhere to cited, all three cited nowhere but here. That is *an instrument
// that shapes the prose it reads stops measuring it*, arriving as arithmetic rather than as a risk.
//
// The first draft of this paragraph said "a quarter", by subtracting the pre-slice total from the
// post-slice one and attributing the whole difference here. The difference is the *slice* — this file
// and ADR 0046 and the contract amendment — so the figure was an unmeasured complement read as an
// empty one, and the same mistake sat in the next clause, which credited this file with an 8-point fall
// in G-3's share that the slice as a whole produced. Both were corrected by reading the `self` column
// the paragraph is about, which is the argument for printing it.
//
// The handling is disclosure, not exclusion. Dropping this file from the census would be an exemption
// written by the author who benefits from it, and the verdict half must not have one at all — the rule
// applies to this file exactly as it applies to `internal/interp`. So the census prints a `self` column
// per clause, the footprint as a percentage, and how many clauses are cited **nowhere but this control**,
// which is the figure that says whether a row is real traffic or the apparatus talking to itself.

const (
	contractDoc = "docs/burroughs-contract-v0.1.md"

	// clauseControl is this file, and it is named because the census reports its own footprint. A
	// rename makes the self column read zero, which is a silent loss, so the tree-scale test asserts
	// the path is in the walked domain.
	clauseControl = "internal/testenv/clause_test.go"
)

var (
	// contractSectionRE matches a section heading — `## §9. Gates, conformance, and the edge`. The
	// section number is captured because a clause's section is a fact about *where it is written*,
	// not something to be transcribed into this file.
	contractSectionRE = regexp.MustCompile(`^## §(\d+)\.`)

	// contractClauseRE matches a clause definition — `- **B-MM-1.** Every host→guest transition`.
	//
	// The vocabulary is derived from the contract, so a new clause is in the domain the moment it is
	// written and a renumbered one takes its citations with it. *A control scoped to today's cases
	// inherits today's blind spot*, and the alternative here is a 33-entry literal that a contract
	// amendment silently invalidates — the same shape G-2's own #109 amendment was about.
	contractClauseRE = regexp.MustCompile(`^- \*\*([A-Z][A-Za-z-]*-\d+)\.\*\*`)

	// clausePairRE matches a citation that writes the section adjacent to the clause — `§9 G-3`, the
	// tree's dominant spelling at 242 of 378 tokens. Both coordinates are checkable in this form.
	//
	// **The under-match is stated rather than papered over.** `§9, G-1` (the CI workflow's header) and
	// `§9 (G-1, G-3, G-4)` (four ADRs' `Contract refs:` lines) associate a section with clauses through
	// punctuation this pattern does not read, so their clause halves are checked as bare tokens and
	// their section halves are checked by nothing. Adding those spellings means writing a small
	// grammar; counting them is what the census does instead, so the size of the unchecked remainder
	// is printed rather than assumed small.
	clausePairRE = regexp.MustCompile(`§(\d+) ([A-Z][A-Za-z-]*-\d+)`)

	// newClauseLead licenses a clause reference that its own grammar says does not exist yet — the
	// determiner-plus-`new` immediately before the token, as in ADR 0046's *"Not a new G-5"* and its
	// *"**A new G-5.**"* alternative heading.
	//
	// **Adjacent by construction, because *aboutness is not proximity*.** The exemption matches the
	// text ending where the token begins, so English grammar decides attachment rather than distance
	// does. A sentence-scoped marker would excuse every clause reference in a paragraph that mentioned
	// a new clause anywhere — which is the block-scoped leak `citation_test.go`'s exemptedBy measured
	// at *two of five real defects excused* when it was tried that way.
	//
	// **What licenses it: the phrase asserts the clause was not created.** That is the same rule
	// `pastReference` states for historical test names — every phrase in the exemption must itself say
	// the referent is not current — and it is what keeps the exemption from being a laundering channel.
	// To be excused by accident, a genuine typo would have to be written as a sentence introducing a
	// clause, which is a different mistake.
	//
	// It excuses the **dangling** verdict only. A reference that resolves is a reference to a clause
	// that exists, so `a new` was false and the section check still applies.
	newClauseLead = regexp.MustCompile(`(?i)\b(?:a|an|another)\s+new\s+$`)

	// commentLead strips the leader a wrapped line begins with, so the exemption can be matched across
	// a line break. Grave #480 is the reason this exists rather than being left to luck: every phrase
	// in an adjacency marker is two or three words, so a marker straddling a wrap does not match and a
	// correctly-phrased reference is flagged — *an instrument that shapes the prose it reads stops
	// measuring it*. No site in the tree wraps this phrase today; the handling is here because the
	// medium guarantees one eventually will.
	commentLead = regexp.MustCompile(`^[\s/*#>-]+`)
)

// clauseCite is one contract-clause citation, with enough of its site to name in a failure.
type clauseCite struct {
	clause       string // the clause as written, `G-3` or `B-MM-1`
	section      int    // the section the citation asserts, or 0 when it wrote none
	path         string // repo-relative file
	line         int    // 1-indexed
	text         string // the line, trimmed
	hypothetical bool   // introduced as a clause that does not exist — see newClauseLead
}

// contractClauses reads the contract and returns each defined clause mapped to its section number.
func contractClauses(tb testing.TB) map[string]int {
	tb.Helper()

	blob, err := os.ReadFile(filepath.Join(repoRoot, contractDoc))
	if err != nil {
		tb.Fatalf("reading the contract at %s: %v", contractDoc, err)
	}

	clauses := map[string]int{}
	sections := map[int]bool{}
	section := 0
	for _, line := range strings.Split(string(blob), "\n") {
		if m := contractSectionRE.FindStringSubmatch(line); m != nil {
			section, _ = strconv.Atoi(m[1])
			sections[section] = true
			continue
		}
		if m := contractClauseRE.FindStringSubmatch(line); m != nil {
			if section == 0 {
				tb.Errorf("%s: clause %s is defined before any `## §N.` heading, so it has no "+
					"section and every citation to it is unresolvable", contractDoc, m[1])
				continue
			}
			clauses[m[1]] = section
		}
	}

	// Vacuity, derived from the document rather than from a remembered count. The two readers above
	// are independent patterns over the same lines, and the failure that matters is one of them
	// silently stopping: no clauses at all makes the resolve check assert its property of nothing,
	// while a *section* with no clauses means the clause reader lost a whole block while the section
	// reader kept working. §§0, 1 and 10 are prose and define no clauses, so the domain is the
	// normative range — asserted per section rather than as a total, because a total of 33 is
	// satisfied by 33 clauses all read out of one section.
	if len(clauses) == 0 {
		tb.Fatalf("%s: no clause definitions matched %s. The vocabulary this control resolves "+
			"against is empty, so every citation in the tree would be reported as dangling — or, "+
			"with the bare-form scan derived from the same set, as nothing at all",
			contractDoc, contractClauseRE)
	}
	for s := range sections {
		if s < 2 || s > 9 {
			continue // §0, §1 (thesis, non-goals) and §10 (open questions) define no clauses.
		}
		found := false
		for _, cs := range clauses {
			if cs == s {
				found = true
				break
			}
		}
		if !found {
			tb.Errorf("%s: §%d defines no clause. §§2–9 are the normative clause sections, so a "+
				"section that reads as empty means the clause reader stopped matching that block's "+
				"form while the section reader kept going — the half-blind case a total count passes",
				contractDoc, s)
		}
	}
	return clauses
}

// clauseFamilyRE builds the bare-token pattern from the derived vocabulary — `\b(?:B-MM|SP|G|…)-\d+\b`.
//
// Families are sorted **longest first** because Go's regexp alternation is leftmost-first: with `M`
// ahead of `B-MM`, a `B-MM-1` would be read as a citation to §8's `M-1`, resolving successfully and
// landing in the wrong census cell. Sorted, not lucky, and held by a fixture below.
//
// The bound this form carries: a bare token can only fail on its *number*, and a bare reference to a
// family the contract has never had is invisible, because nothing distinguishes it from `UTF-8` or
// `x86-64` without a `§` or a known family to key on. That gap is why the pair form exists and why the
// census prints how much of the population each form covered.
func clauseFamilyRE(tb testing.TB, clauses map[string]int) *regexp.Regexp {
	tb.Helper()

	families := map[string]bool{}
	for c := range clauses {
		if i := strings.LastIndex(c, "-"); i > 0 {
			families[c[:i]] = true
		}
	}
	names := make([]string, 0, len(families))
	for f := range families {
		names = append(names, f)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	if len(names) == 0 {
		tb.Fatalf("derived no clause families from %d clauses — the bare-token scan would find "+
			"nothing and report a clean tree", len(clauses))
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(names, "|") + `)-\d+\b`)
}

// scanClauseCitations returns every clause token in one file's content, each counted exactly once.
//
// A token written as `§9 G-3` is a pair citation; the bare scan would find its clause half again, so
// pair spans are recorded and the bare scan skips anything inside one. Double-counting would not change
// either verdict — both copies resolve or neither does — but it would corrupt the census, which is the
// half a human reads.
func scanClauseCitations(path, content string, familyRE *regexp.Regexp) []clauseCite {
	var out []clauseCite
	var prev string
	for i, line := range strings.Split(content, "\n") {
		// lead answers newClauseLead over the text ending at a token, joined across the previous
		// line so a wrapped phrase still matches.
		lead := func(start int) bool {
			joined := prev + " " + commentLead.ReplaceAllString(line[:start], " ")
			return newClauseLead.MatchString(strings.Join(strings.Fields(joined), " ") + " ")
		}

		var covered [][2]int
		for _, m := range clausePairRE.FindAllStringSubmatchIndex(line, -1) {
			section, _ := strconv.Atoi(line[m[2]:m[3]])
			out = append(out, clauseCite{
				clause:       line[m[4]:m[5]],
				section:      section,
				path:         path,
				line:         i + 1,
				text:         strings.TrimSpace(line),
				hypothetical: lead(m[0]),
			})
			covered = append(covered, [2]int{m[4], m[5]})
		}
		for _, m := range familyRE.FindAllStringIndex(line, -1) {
			inPair := false
			for _, c := range covered {
				if m[0] >= c[0] && m[1] <= c[1] {
					inPair = true
					break
				}
			}
			if inPair {
				continue
			}
			out = append(out, clauseCite{
				clause:       line[m[0]:m[1]],
				path:         path,
				line:         i + 1,
				text:         strings.TrimSpace(line),
				hypothetical: lead(m[0]),
			})
		}
		prev = line
	}
	return out
}

// textSources returns every text file in the tree, repo-relative and sorted.
//
// **Text is decided by content, not by extension.** Clause citations live in `.go`, `.md`, `.sh`, the
// `Makefile`, and `.github/workflows/ci.yml`, and an extension list would have been written from
// exactly that sample — *derive the domain, never enumerate it*. Valid UTF-8 with no NUL byte is the
// derivation, so a new file type is in the domain the day it appears.
//
// The walk routes through `skipWalkDir` for grave #369's reason, with `third_party` as this walk's own
// documented addition: the fetched spec material is upstream's, it does not cite this contract, and a
// rule about our citations has no jurisdiction there.
func textSources(tb testing.TB) []string {
	tb.Helper()

	var out []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipWalkDir(d, "third_party") {
				return fs.SkipDir
			}
			return nil
		}
		blob, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(blob, 0) >= 0 || !utf8.Valid(blob) {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		tb.Fatalf("walking for text sources: %v", err)
	}
	slices.Sort(out)
	return out
}

// TestEveryContractClauseCitationResolves is ADR 0046's instrument: the two mechanical checks, plus the
// census that is the reason to have run them.
func TestEveryContractClauseCitationResolves(t *testing.T) {
	clauses := contractClauses(t)
	familyRE := clauseFamilyRE(t, clauses)
	sources := textSources(t)

	// The contract itself must be in the walked domain — it is the one file known to hold clause
	// tokens by a reader that never walked, so a walk that lost it is caught without pinning a count.
	if !slices.Contains(sources, contractDoc) {
		t.Fatalf("%s is not among the %d text files this control walked, and it is the file the "+
			"vocabulary was just read out of. The domain is derived from a tree walk, so this means "+
			"the walk's boundary moved", contractDoc, len(sources))
	}
	// The census reports this file's own contribution, so a rename that leaves the constant behind
	// turns that column into a silent zero — the disclosure would read as "the apparatus contributes
	// nothing", which is the one thing it demonstrably does not do.
	if !slices.Contains(sources, clauseControl) {
		t.Fatalf("%s is not among the %d text files this control walked. That is either a moved "+
			"walk boundary or this file's own path gone stale, and the second one makes the "+
			"census's self-footprint column report zero for the wrong reason",
			clauseControl, len(sources))
	}

	var cites []clauseCite
	for _, src := range sources {
		blob, err := os.ReadFile(filepath.Join(repoRoot, src))
		if err != nil {
			t.Fatalf("reading %s: %v", src, err)
		}
		cites = append(cites, scanClauseCitations(src, string(blob), familyRE)...)
	}

	pairs := 0
	for _, c := range cites {
		if c.section != 0 {
			pairs++
		}
	}

	// Two floors, on the two populations the two checks run over. Separate because they fail for
	// unrelated reasons: zero tokens means the whole scan died, while zero *pairs* means the section
	// check ran over an empty set while the resolve check was still working — *an unasserted distance
	// is the vacuum*, and the pair half is the half that depends on a punctuation-sensitive pattern.
	if len(cites) == 0 {
		t.Fatalf("found no clause tokens across %d text files. Either the tree stopped citing the "+
			"contract, which is a finding in itself, or this scan stopped being able to see citations",
			len(sources))
	}
	if pairs == 0 {
		t.Fatalf("found %d clause token(s) and not one written as `§N X-M`. The section-coordinate "+
			"check below is half this control's mechanical content and it just ran over an empty set",
			len(cites))
	}

	var excused []string
	for _, c := range cites {
		section, ok := clauses[c.clause]
		if !ok {
			if c.hypothetical {
				excused = append(excused, fmt.Sprintf("%s:%d %s", c.path, c.line, c.clause))
				continue
			}
			t.Errorf(`%s:%d: %s names no clause the contract defines.

	%s

The vocabulary is read from %s, so this fires on a typo and on a renumbering alike — and the second
one is the interesting case: the clause moved, its incoming citations did not, and every one of them
still looks resolvable to a reader who does not open the contract.

If the reference is to a clause that was proposed and not created, say so in the grammar — a
determiner and the word "new" immediately before it — which is what an ADR's rejected-alternatives
section already reads like.`,
				c.path, c.line, c.clause, c.text, contractDoc)
			continue
		}
		if c.section != 0 && c.section != section {
			t.Errorf(`%s:%d: cited as §%d %s, but %s is defined in §%d.

	%s

The clause resolves, so nothing else in this tree would have noticed. A reader following the section
number lands in a different part of the contract and reads a different rule — which is the shape #442
paid for at 243 sites, arriving through the other coordinate.`,
				c.path, c.line, c.section, c.clause, c.clause, section, c.text)
		}
	}

	// The exemption's own size, printed rather than trusted — *a silent exclusion is
	// indistinguishable from having found nothing*, and this is the number that says how much prose
	// the licence is carrying. Enumerated at the site because the population is small enough to read;
	// if it stops being, that is itself the finding.
	if len(excused) > 0 {
		t.Logf("%d reference(s) excused as clauses that do not exist, by the grammar that says so: %s",
			len(excused), strings.Join(excused, ", "))
	}
	if !t.Failed() {
		t.Log(clauseCensus(clauses, cites, len(sources), pairs))
	}
}

// clauseCensus renders the tell. It is printed on every green run, because the fact that decided
// #442's mechanism is a *distribution* and no verdict expresses it: G-3 carrying 72% of the tree's
// contract citations is not a failure, and neither is 27 clauses carrying none, but both are what a
// human needs in order to ask the question the resolver cannot.
//
// *Coverage is a claim*, so the population is printed beside the counts: how many files were walked,
// how many tokens found, and how many of them carried a section coordinate — the last one being the
// size of the set the pair check actually covered, rather than an assurance that the remainder is
// small.
func clauseCensus(clauses map[string]int, cites []clauseCite, files, pairs int) string {
	inside, outside, apparatus := map[string]int{}, map[string]int{}, map[string]int{}
	for _, c := range cites {
		switch c.path {
		case contractDoc:
			inside[c.clause]++
		case clauseControl:
			apparatus[c.clause]++
			outside[c.clause]++
		default:
			outside[c.clause]++
		}
	}

	ranked := make([]string, 0, len(clauses))
	for c := range clauses {
		ranked = append(ranked, c)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if outside[ranked[i]] != outside[ranked[j]] {
			return outside[ranked[i]] > outside[ranked[j]]
		}
		if clauses[ranked[i]] != clauses[ranked[j]] {
			return clauses[ranked[i]] < clauses[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})

	total, self := 0, 0
	for _, c := range ranked {
		total += outside[c]
		self += apparatus[c]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d clause token(s) across %d text file(s): %d citing the contract from outside "+
		"it, %d inside it, %d written as `§N X-M` (the only form whose section coordinate is "+
		"checked).\n", len(cites), files, total, len(cites)-total, pairs)
	fmt.Fprintf(&b, "%d clause(s) defined in %s. Citations from outside it, most-cited first — "+
		"`self` is this control's own share of each row:\n", len(clauses), contractDoc)

	uncited, uncitedElsewhere := 0, 0
	for _, c := range ranked {
		if outside[c] == 0 {
			uncited++
			uncitedElsewhere++
			continue
		}
		if outside[c] == apparatus[c] {
			uncitedElsewhere++
		}
		fmt.Fprintf(&b, "  §%d %-7s %4d  (%4.1f%%)  self %-3d  +%d in the contract\n",
			clauses[c], c, outside[c], 100*float64(outside[c])/float64(total), apparatus[c], inside[c])
	}
	fmt.Fprintf(&b, "  %d clause(s) cited nowhere outside the contract, %d nowhere but this "+
		"control.\n", uncited, uncitedElsewhere)
	fmt.Fprintf(&b, "  %d of the %d outside citations (%.0f%%) are this control's own prose and "+
		"fixtures — the apparatus's footprint in its own sample, disclosed rather than excluded.\n",
		self, total, 100*float64(self)/float64(total))
	return b.String()
}

// fixtureClauses is the vocabulary the classifier fixtures resolve against, and it is **deliberately
// not the contract's**.
//
// This is how a specimen for each verdict is held without this file writing a reference the real tree
// would reject. `G-4` is absent, so a fixture citing it is the dangling case here and resolves fine on
// the real run; `G-3` is recorded in §8, so a fixture writing the correct `§9 G-3` is the
// wrong-section case here and correct out there. The verdicts under test are *relative to the
// vocabulary handed in*, which is exactly what the checks compute.
//
// **The alternative was writing invented tokens, and the corpus refused it before this file existed.**
// `citation_test.go`'s ruling — *when a control fires on its own explanation, fix the explanation* —
// applies to fixtures as much as to prose, and the other way out, building the token from string
// fragments at run time, is *phrasing around the instrument* with extra steps.
var fixtureClauses = map[string]int{"G-1": 9, "G-3": 8, "H-3": 5, "M-1": 8, "B-MM-1": 4}

// TestClauseScanClassifiesItsFixtures is the judge's judge — G-4's rider, applied to this file.
//
// The tree passes both checks above, which means neither is watched die by the run that matters. A
// control certified only by passing is *a control that isn't born until it's watched die*, so the
// classifier is exercised here on hand-built lines carrying every outcome, including the two false
// positives a naive version produces.
//
// It does **not** discharge the real-path falsification: *a control can test the helper, not the path*,
// and these fixtures never touch textSources or the contract reader. The two tree-scale checks are
// mutation-tested separately when either changes — by renumbering a clause in the contract and
// confirming the FAIL names its citations, which is the run recorded in this slice's PR body.
func TestClauseScanClassifiesItsFixtures(t *testing.T) {
	familyRE := clauseFamilyRE(t, fixtureClauses)

	for _, tc := range []struct {
		name     string
		line     string
		want     []clauseCite
		dangles  bool // the reference names no clause in fixtureClauses
		wrongSec bool // it names one, in the wrong section
	}{
		{
			name: "pair form, correct section",
			line: "the neutrality guarantee (§9 G-1) bounds this",
			want: []clauseCite{{clause: "G-1", section: 9}},
		},
		{
			name:     "pair form, wrong section",
			line:     "see §9 G-3 for the ceiling",
			want:     []clauseCite{{clause: "G-3", section: 9}},
			wrongSec: true,
		},
		{
			name: "bare form resolves",
			line: "ADR 0025's G-1 carve-out",
			want: []clauseCite{{clause: "G-1"}},
		},
		{
			name:    "bare form, clause not in the vocabulary",
			line:    "as G-4 requires",
			want:    []clauseCite{{clause: "G-4"}},
			dangles: true,
		},
		{
			name:    "pair form, clause not in the vocabulary",
			line:    "the battery is §9 G-4",
			want:    []clauseCite{{clause: "G-4", section: 9}},
			dangles: true,
		},
		{
			// The exemption. Dangling and licensed, because the grammar in front of it says the
			// clause does not exist — ADR 0046's rejected alternative, verbatim in shape.
			name:    "a clause introduced as new is not dangling",
			line:    "Not a new G-4. The rider costs nothing.",
			want:    []clauseCite{{clause: "G-4"}},
			dangles: true,
		},
		{
			// The left boundary. This token's last three characters are shaped like a §5 clause
			// reference, and §5 defines three clauses. It is the real specimen from
			// `closebody_test.go`, kept so the boundary cannot be removed silently.
			name: "GH-N is not a clause citation",
			line: `write(t, "body.md", "Landed in "+ref+", see also GH-7.")`,
			want: nil,
		},
		{
			// The other shape a boundaryless scan invents: `B-MM-1` read as §8's `M-1`. Caught by
			// the longest-first family ordering, and a fixture rather than a comment because the
			// failure is a silently *successful* resolution into the wrong section's cell.
			name: "the longest family wins",
			line: "§4 B-MM-1 is the acquire edge",
			want: []clauseCite{{clause: "B-MM-1", section: 4}},
		},
		{
			name: "a pair is not also counted bare",
			line: "§9 G-1 and §9 G-1 and a loose G-1",
			want: []clauseCite{
				{clause: "G-1", section: 9},
				{clause: "G-1", section: 9},
				{clause: "G-1"},
			},
		},
		{
			// The stated under-match, asserted rather than described. The comma spelling loses its
			// section coordinate — the clause still resolves, the section is checked by nothing —
			// and a fixture keeps that a known bound instead of a surprise.
			name: "the comma spelling loses its section",
			line: "Two architectures on purpose (contract §9, G-1)",
			want: []clauseCite{{clause: "G-1"}},
		},
		{
			name: "non-clause hyphen-number shapes are not citations",
			line: "UTF-8, x86-64, SHA-256, RFC-2119, wasip1, B5000",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scanClauseCitations("fixture.md", tc.line, familyRE)
			if len(got) != len(tc.want) {
				t.Fatalf("scanned %d token(s), want %d\n  line: %s\n  got:  %+v",
					len(got), len(tc.want), tc.line, got)
			}
			for i, w := range tc.want {
				if got[i].clause != w.clause || got[i].section != w.section {
					t.Errorf("token %d: got %s/§%d, want %s/§%d",
						i, got[i].clause, got[i].section, w.clause, w.section)
				}
			}

			// The classification, run through the same predicates the tree-scale test uses, so a
			// change to either is caught here rather than only on a tree that happens to be clean.
			for _, c := range got {
				section, ok := fixtureClauses[c.clause]
				if ok == tc.dangles {
					t.Errorf("%s: resolved=%v, want dangling=%v", c.clause, ok, tc.dangles)
				}
				if ok && (c.section != 0 && c.section != section) != tc.wrongSec {
					t.Errorf("%s: cited §%d, defined §%d, want wrong-section=%v",
						c.clause, c.section, section, tc.wrongSec)
				}
			}
		})
	}
}

// TestTheNewClauseExemptionIsNarrow is the exemption's own control, and it exists because *an exemption
// inherits none of the trigger's lessons*: the licensing side is written after the trigger, by an author
// who wants a green, and a false positive there is invisible — it looks like a clean run.
//
// What it pins is the adjacency. The licence attaches by grammar, so a paragraph that mentions a new
// clause somewhere must not excuse a different clause reference elsewhere in the sentence.
func TestTheNewClauseExemptionIsNarrow(t *testing.T) {
	familyRE := clauseFamilyRE(t, fixtureClauses)

	for _, tc := range []struct {
		name    string
		line    string
		clause  string
		excused bool
	}{
		{
			name:   "licensed: the determiner and new sit immediately before it",
			line:   "The option was a new G-4, and it lost on site cost.",
			clause: "G-4", excused: true,
		},
		{
			name:   "licensed across a comment wrap",
			line:   "// new G-4 would have cost 243 edits",
			clause: "G-4", excused: true,
		},
		{
			name:   "not licensed: new is in the sentence but not before the token",
			line:   "A new clause would resolve them, so G-4 is what the sites should say.",
			clause: "G-4", excused: false,
		},
		{
			name:   "not licensed: bare new without a determiner",
			line:   "new G-4 rules the accept direction",
			clause: "G-4", excused: false,
		},
		{
			name:   "not licensed: the phrase follows the token",
			line:   "G-4 is a new clause",
			clause: "G-4", excused: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The wrap case needs its preceding line, which is the whole point of it.
			content := tc.line
			if strings.HasPrefix(tc.name, "licensed across") {
				content = "// The alternative was a\n" + tc.line
			}
			got := scanClauseCitations("fixture.md", content, familyRE)
			var found *clauseCite
			for i := range got {
				if got[i].clause == tc.clause {
					found = &got[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("the scan did not find %s at all, so this case asserts nothing about the "+
					"exemption\n  content: %q\n  got: %+v", tc.clause, content, got)
			}
			if found.hypothetical != tc.excused {
				t.Errorf("%s: excused=%v, want %v\n  content: %q",
					tc.clause, found.hypothetical, tc.excused, content)
			}
		})
	}
}
