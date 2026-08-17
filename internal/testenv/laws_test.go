// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `CLAUDE.md` is an index and `docs/laws/` is the corpus, which is a **two-registry design over
// one space** — exactly the shape #264's third instance is filed against — so it does not get to
// rely on anyone remembering to update both. Two properties are checked, and they are checked
// separately because they fail for different reasons and want different messages:
//
//  1. **The index and the corpus are in bijection, with the recall key equal to the heading.** A
//     law whose body moved to `docs/laws/` keeps its one-line compressed form in `CLAUDE.md` as
//     the recall key; the key *is* the heading over there. Checking equality rather than mere
//     resolution is what kills the drift the duplication would otherwise invite: a key edited in
//     one place and not the other fails here, where a resolve-only check would pass. This is the
//     citation-resolving discipline (#114/#115/#116) pointed at the project's own governance
//     index instead of at its code.
//
//  2. **`CLAUDE.md` has a size ceiling.** The file that polices everything else is policed, and
//     mechanism beats memory. The failure mode the restructure exists to prevent is not a wrong
//     sentence, it is *the corpus growing back into the index* one defensible paragraph at a
//     time — which is the ratchet the stop condition names, applied to prose. A ceiling is the
//     right instrument for that and a threshold on law *count* is not, because the point is
//     context cost and new one-liners are cheap while pasted bodies are not.
//
// Both are floored, per the vacuity law: a reader that finds zero laws would satisfy every
// assertion below by asking nothing, and that is the exact shape of the defect this file's
// subject warns about at length.

const (
	claudeMD = "../../CLAUDE.md"
	lawsDir  = "../../docs/laws"

	// claudeMDCeiling is a **ceiling on the index's size in bytes**, and like every other
	// ceiling in this repo (`unsupportedCeiling`, `textFailCeiling`) it is meant to rot by the
	// system working: it is lowered when the index shrinks, never raised to accommodate a body
	// that should have gone to `docs/laws/`.
	//
	// Bytes rather than lines or laws, because the quantity the purpose names is **context
	// cost** — this file is re-read every session, so its size is the thing being conserved,
	// and *budget by the quantity the purpose names* applies to a prose gate as much as to
	// `fuzz-smoke`.
	//
	// Set at 38000 against a measured 36601 at the restructure: ~1400 bytes of headroom, which
	// is roughly seven more one-line recall keys with pointers. Deliberately tight, because *an
	// unasserted distance is the vacuum* — a ceiling far from what it bounds runs, agrees, and
	// says nothing. Tripping it is a question, not a verdict: is the new text governance (which
	// belongs here) or a law's body (which belongs in `docs/laws/`)? Where the room then comes from
	// is a separate question, ruled on below: not from this number.
	//
	// 38000 → 38400 (PR #295). It tripped at **38068** on minting *a total is not a ledger*, and
	// the question resolves the second way the doc comment below offers: the added text is a law's
	// **key**, one line and a pointer, with its entire body — two specimens, the minting history,
	// the attribution ruling — in `docs/laws/controls.md`. That is the restructure working as
	// designed rather than eroding, so the ceiling moved — under the pre-#298 reading, which the
	// ruling further down retires as a remedy. The headroom was already down to 262
	// bytes before this key, spent on governance additions (the gate-campaign carve-out, the flip's
	// stamp tier) that belong here by the same doc comment's first branch.
	//
	// # What the number protects, stated here because a bound whose rationale is not at the site
	// becomes decoration the first time someone needs room
	//
	// That is `passFloor`'s lesson (#285) applied to this file. So, explicitly: **38400 bytes is a
	// budget on the largest component of what every session must read before it can do anything.**
	// `CLAUDE.md` is loaded into context each turn, so its size is a standing tax on every task in
	// the repo — it is subtracted from the room available for the *code* under discussion. What the
	// ceiling protects is therefore not tidiness and not a line count: it is the agent's capacity to
	// hold an interpreter's decoder, its validator, and a spec vector in mind at once, which is the
	// work the file exists to govern. An index that has eaten that room governs a session that can no
	// longer do the thing being governed.
	//
	// # The domain is two files and this sentence said one — *coverage is a claim*, with the ceiling
	// as its own subject (#319)
	//
	// The sentence above read, until #319:
	//
	//	**38400 bytes is a budget on what every session must read before it can do anything.**
	//
	// That is a claim about the **consumer's** extent, and the consumer was never asked. Asked now —
	// by reading the assembled context block, which names its own sources, rather than by reasoning
	// about the loader — a session in this repo loads **two** files under that heading:
	//
	//   - `CLAUDE.md`, 37886 bytes, which is this ceiling's whole domain and the ledger's.
	//   - a per-project memory index under `$HOME/.claude/projects/…/memory/MEMORY.md`, 5793 bytes,
	//     outside the module and in no instrument's domain.
	//
	// The extent is **43679 bytes**, of which this bound covers 86.7%, and **the extent already
	// exceeds the budget** — by 5279 bytes — while every instrument here is green. Nothing in this
	// file is wrong about its own subject: the ledger reconciles `CLAUDE.md`'s rows against
	// `os.Stat` to the byte, exactly as it claims. That is the shape, not an exception to it. A green
	// control plus a domain nobody stated is indistinguishable from a green control over the real
	// thing, and the tell was not available from inside — it took a consumer-side figure that the two
	// green instruments could not account for (Scott, this session) before anyone asked what the
	// domain was.
	//
	// The second half is *reconcile an extent, never floor it* (#317) turned on the instrument that
	// was minted under it three PRs earlier. The extent has two ends; one is reconciled to the byte
	// and the other is not measured at all, and the unmeasured end is **monotone** — a memory index
	// grows one line per memory and memories are only ever added. It also cannot be measured from
	// here: the path embeds a specific `$HOME` and exists on neither CI runner, so a control that
	// read it would skip in CI, and *a skip is not a verdict*.
	//
	// **Deliberately not fixed by building something.** Partitioning the real extent means an
	// instrument reaching outside the module into a user-specific path, which is Scott's call and is
	// flagged as one on #319 with its options rather than taken here. The remedy applied is the one
	// he ordered for `pipefail-check`'s own boundary on PR #317 — *state the boundary, build
	// nothing* — and the boundary is stated in the sentence this section corrects rather than only in
	// the issue, because a domain claim belongs where the number is read.
	//
	// Two consequences, both operative:
	//
	//   - The trip is a **question, not a verdict** — is the new text governance (which belongs
	//     here) or a law's body (which belongs in `docs/laws/`)? The failure message spells both
	//     branches out. Neither branch's answer is "raise the number"; see the ruling below.
	//   - Raising it is never free, and "we needed room" is not a reason — it is the *statement of
	//     the trip*. The reason has to name which branch the text is and why.
	//
	// # It is a reconciliation guard over a per-entry ledger, not the primary control
	//
	// Ruled on PR #298, under *a total is not a ledger*'s own exception clause: a single byte
	// ceiling over entries that are individually enumerable is the aggregate that law demotes —
	// one recall key can bloat into a body while the total stays green, and trimming an unrelated
	// key buys the room for it, which is the same opposite-sign cancellation the ≤896 bound
	// suffered. So `TestClaudeMDIndexLedger` asserts **each index entry's bytes on the nose** and
	// is the primary control; this ceiling stays **fatal** because the exception applies squarely —
	// *the consumer consumes the total*, context cost being total bytes and not per-entry bytes, so
	// no sum of rows substitutes for the artifact's own size. The division of labour: the ledger
	// says *which entry* moved, the ceiling says *whether the file* still fits.
	//
	// # It is a budget and nothing else, and room is bought by demotion
	//
	// The paragraph this replaces flagged a structural tension and asked for a call: the index grows
	// by one key per law by construction, so a fixed byte ceiling on it is tripped by every future
	// mint, forever, and it cannot be the ratchet-stopper `unsupportedCeiling` is. Scott ruled (PR
	// #298): **with the ratchet caught per entry by the ledger, this number is a budget and nothing
	// else.** Read it that way. It is *not* a ratchet-stopper, and the reason that matters is not
	// taxonomy — a reader who takes it for one reaches for the wrong remedy when it trips.
	//
	// The remedy the ruling names is the operative half: **the next mint does not buy room by raising
	// 38400 — it buys room by demoting an already-enforced law to a pointer.** Scott's selection rule,
	// in his words: *a law with a live control that fails when it's violated does not need index prose
	// teaching it — the control teaches it, at the moment it matters. Index space should go to the laws
	// no control can enforce.* So a budget at its limit is a prompt to ask **which key is redundant
	// with a control**, not a prompt to move the limit. Named candidate, on its own terms: *a total is
	// not a ledger*, whose whole content is now enforced by `TestClaudeMDIndexLedger` sitting beside it.
	//
	// # The test is the control's failure *message*, not the control's existence (Scott, PR #339)
	//
	// The selection rule above says "a live control that fails when it's violated". Sharpened, in his
	// words: **a law is demotable when its enforcing control's failure message states the rule and the
	// remedy.** The reason is the index's own purpose — it is what a session reads *before* it acts, so
	// a demoted law has to be one a session does not need to have read, because the control will say
	// the whole thing at the moment it matters. A message naming the violation and not the remedy
	// leaves the reader knowing they are wrong and not what to write instead, which is a control that
	// still needs index prose behind it. Where that is the case, **fix the message first and the
	// demotion becomes free** — an ordering that turns the criterion into cheap work rather than a
	// blocked decision.
	//
	// Triage run on the two candidates he named, and the first one corrects the premise:
	//
	//   - **The closing-keyword rule has no index key to demote.** It was named the strongest
	//     candidate — `closecheck.sh` is fully mechanical and has caught the same author twice — and
	//     the check found no key for it in `CLAUDE.md` and no heading in `docs/laws/`: it exists as the
	//     script, the CI step, and grave #314, and never was promoted. That is evidence *for* the
	//     criterion rather than against it: a control whose message already states rule and remedy
	//     (it names the token-parsing cause and spells the three safe phrasings plus the by-hand close)
	//     never needed the prose, and never took the room.
	//   - **The citation-resolution keys pass only after a message repair, which is now made.** Three
	//     `citecheck.sh` failures named the violation and stopped there — an ADR number with no file,
	//     an issue number that does not resolve, and a `grave #N` without `type:grave`. Each now states
	//     its remedy (check the number, write the record, `gh issue create` and cite what comes back,
	//     `--add-label type:grave` with the lesson in the closing comment). So *fixtures cite the suite*
	//     and *a doc comment's identifier is a citation* are pre-cleared for demotion when a mint needs
	//     the room.
	//
	// No demotion is taken here: **the next law pays for itself** (Scott, PR #339 — *"the index at zero
	// headroom is the ratchet arriving, not a problem to solve"*). If no law is worth a key, the file
	// stops growing, which is what a ratchet is for.
	//
	// Two consequences, kept here because they retire a branch that every message in this file used to
	// offer:
	//
	//   - **"The text is governance, so the ceiling moves" is retired as a remedy.** Governance still
	//     stays in the index — that half is unchanged, and it is the whole reason the index carries
	//     operative text at all. What changed is where the room comes from: demotion, not the bound.
	//   - The rule is not a policy for later. Headroom was **224 bytes** when this was written, under
	//     a single one-line key; the live figure is printed by `TestClaudeMDIndexLedger` and that is
	//     where to read it rather than from this sentence, which rots.
	//
	// So the number stays at 38400, and the 38000 → 38400 record above is **the last raise made under
	// the old reading** — quoted with its era, as this repo's other superseded figures are.
	claudeMDCeiling = 38400

	// lawsFloor is the vacuity guard. 46 laws were relocated by the restructure; the floor sits
	// just under that so a reader that silently stops matching — the wrapped-lead defect this
	// project has **two** graves for (#78, #105) plus an un-graved recurrence on #80, which the
	// migration's own extractor committed and had to repair — fails loudly instead of certifying
	// an empty bijection. The count said "three graves for (#78/#80/#105)" until
	// `scripts/citecheck.sh`'s first repo-wide run resolved the three numbers and found #80
	// carries no `type:grave` label, because it is not a grave: it is the work issue the
	// recurrence fired during. A claim about the graveyard's own size, wrong in the file that
	// checks the law corpus.
	lawsFloor = 44
)

// A top-level law bullet in the index: a bold lead, then a pointer into `docs/laws/`. The lead is
// matched across line wraps, which is the whole of #78/#80/#105's lesson — a line-oriented
// trigger cannot see a lead that wraps, and 13 of the 46 leads here do wrap. So the section is
// split into bullets first and each bullet is joined before matching, rather than the regexp
// being asked to do both jobs on raw lines.
var (
	lawBulletRE  = regexp.MustCompile(`^- \*\*`)
	lawLeadRE    = regexp.MustCompile(`^- \*\*(.+?)\*\*`)
	lawPointerRE = regexp.MustCompile(`\(docs/laws/([a-z-]+)\.md#([a-z0-9-]+)\)`)
	lawHeadingRE = regexp.MustCompile(`^### (.+)$`)
)

// anchorFor reproduces GitHub's heading-slug rules. It exists so the check derives the anchor it
// expects from the heading text rather than trusting a transcription on either side — the same
// reason the migration computed both sides from one function.
func anchorFor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// indexedLaw is one recall key in `CLAUDE.md`'s Disciplines section.
type indexedLaw struct {
	lead   string // the bold lead sentence, which must equal the corpus heading verbatim
	family string // the `docs/laws/` file its body lives in
	anchor string // the anchor the pointer names
	line   int    // 1-indexed line in CLAUDE.md, for the failure message
}

// disciplinesRange returns the half-open line range of `CLAUDE.md`'s `## Disciplines` section.
//
// Factored out because two instruments read the same span and must not disagree about where it
// is: the bijection below, and the per-entry byte ledger in `claudemd_ledger_test.go`. Two
// copies of a boundary rule over one region is the two-registry shape this very file exists to
// police, so it gets one definition.
func disciplinesRange(tb testing.TB, lines []string) (start, end int) {
	tb.Helper()

	start, end = -1, len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, "## Disciplines") {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(l, "## ") {
			end = i
			break
		}
	}
	if start < 0 {
		tb.Fatalf("%s has no `## Disciplines` section — the index this check reads is gone, which "+
			"is a bigger finding than any drift it was written to catch", claudeMD)
	}
	return start, end
}

// readIndex returns the laws named in `CLAUDE.md`'s `## Disciplines` section.
func readIndex(t *testing.T) []indexedLaw {
	t.Helper()

	src, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("reading the index: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	start, end := disciplinesRange(t, lines)

	// Split into top-level bullets, then join each before matching. Split-then-join, never a
	// regexp over raw lines: see the comment on lawLeadRE.
	var laws []indexedLaw
	var cur []string
	curLine := 0
	flush := func() {
		if cur == nil {
			return
		}
		joined := strings.Join(cur, " ")
		lead := lawLeadRE.FindStringSubmatch(joined)
		ptr := lawPointerRE.FindStringSubmatch(joined)
		if lead == nil {
			t.Errorf("%s:%d: a top-level bullet in Disciplines with no bold lead, even joined "+
				"across wraps: %.90s", claudeMD, curLine, joined)
			cur = nil
			return
		}
		if ptr == nil {
			t.Errorf(`%s:%d: the law %q has no `+"`docs/laws/`"+` pointer.

Every law in the index points at its body, with no exemption — including the governance ones,
whose bodies are retained here and whose minting records are over there. A recall key with no
pointer is a law whose specimen exists nowhere, which is what the relocation was structured to
make impossible.`, claudeMD, curLine, lead[1])
			cur = nil
			return
		}
		laws = append(laws, indexedLaw{lead: lead[1], family: ptr[1], anchor: ptr[2], line: curLine})
		cur = nil
	}
	for i := start + 1; i < end; i++ {
		if lawBulletRE.MatchString(lines[i]) {
			flush()
			cur, curLine = []string{strings.TrimSpace(lines[i])}, i+1
			continue
		}
		if cur != nil {
			cur = append(cur, strings.TrimSpace(lines[i]))
		}
	}
	flush()
	return laws
}

// readCorpus returns every `### ` heading in `docs/laws/`, keyed by family file.
func readCorpus(t *testing.T) map[string]map[string]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(lawsDir, "*.md"))
	if err != nil {
		t.Fatalf("globbing the corpus: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no markdown files — the corpus the index points at is empty, and every "+
			"bijection below would agree with it vacuously", lawsDir)
	}

	corpus := map[string]map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		family := strings.TrimSuffix(filepath.Base(f), ".md")
		corpus[family] = map[string]bool{}
		for _, l := range strings.Split(string(src), "\n") {
			if m := lawHeadingRE.FindStringSubmatch(l); m != nil {
				corpus[family][m[1]] = true
			}
		}
	}
	return corpus
}

// TestEveryLawIsIndexed checks the bijection between `CLAUDE.md`'s recall keys and
// `docs/laws/`'s bodies, in **both** directions and on the heading *text* rather than on
// resolution alone.
//
// Both directions, because the two failures are different defects: a key with no body is a law
// whose specimen was lost in the move, and a body with no key is a law that has fallen out of
// context — invisible to the agent that is supposed to be governed by it, which is worse, being
// silent. One-directional coverage checks are how #78's 17 unregistered rows happened.
func TestEveryLawIsIndexed(t *testing.T) {
	laws := readIndex(t)
	corpus := readCorpus(t)

	if len(laws) < lawsFloor {
		t.Fatalf("read only %d laws from %s's Disciplines section, want at least %d.\n\n"+
			"Every check below is a comparison, and a comparison against a nearly-empty set "+
			"agrees perfectly. A reader that has silently stopped matching is the failure this "+
			"floor exists for — it is the shape the section's own #78/#80/#105 entries describe, "+
			"and the extractor that performed this migration committed it.",
			len(laws), claudeMD, lawsFloor)
	}

	// Direction 1: every recall key resolves, and resolves to a heading with the same text.
	pointed := map[string]map[string]bool{}
	for _, law := range laws {
		headings, ok := corpus[law.family]
		if !ok {
			t.Errorf("%s:%d: the law %q points at docs/laws/%s.md, which does not exist",
				claudeMD, law.line, law.lead, law.family)
			continue
		}
		if !headings[law.lead] {
			t.Errorf(`%s:%d: the recall key and its corpus heading are not the same text.

    key in %s: %q
    docs/laws/%s.md: no `+"`### `"+` heading with that text

The key here and the heading there are one sentence with two homes, so they are checked equal
rather than merely resolvable — a key edited in one place and not the other is precisely the
drift the two-registry design invites, and a resolve-only check would pass across it.`,
				claudeMD, law.line, claudeMD, law.lead, law.family)
			continue
		}
		if want := anchorFor(law.lead); law.anchor != want {
			t.Errorf("%s:%d: the law %q points at anchor #%s, but its heading slugs to #%s — the "+
				"link resolves to nothing", claudeMD, law.line, law.lead, law.anchor, want)
		}
		if pointed[law.family] == nil {
			pointed[law.family] = map[string]bool{}
		}
		if pointed[law.family][law.lead] {
			t.Errorf("%s:%d: the law %q is indexed twice — a duplicate key makes the count below "+
				"agree while covering one law less", claudeMD, law.line, law.lead)
		}
		pointed[law.family][law.lead] = true
	}

	// Direction 2: every body is pointed at. The silent half.
	for family, headings := range corpus {
		for heading := range headings {
			if !pointed[family][heading] {
				t.Errorf(`docs/laws/%s.md: the law %q is in the corpus and not in the index.

A body nobody points at is a law that has fallen out of context: the agent it governs never
reads it, and nothing goes red. Add its one-line compressed form to %s's Disciplines section —
the heading text verbatim — with a pointer back here.`, family, heading, claudeMD)
			}
		}
	}
}

// TestClaudeMDStaysAnIndex is the size tripwire. It is a separate test from the bijection above
// because the two fail for unrelated reasons and *one test asserting two properties that fail
// with one message is the partition defect* (grave #34).
func TestClaudeMDStaysAnIndex(t *testing.T) {
	info, err := os.Stat(claudeMD)
	if err != nil {
		t.Fatalf("stat %s: %v", claudeMD, err)
	}
	if got := info.Size(); got > claudeMDCeiling {
		t.Errorf(`%s is %d bytes, over its %d-byte ceiling.

This is a question, not a verdict, and it has two honest answers:

  - The new text is a **law's body** — a specimen, a minting record, a measurement table. It
    belongs in `+"`docs/laws/`"+`, with only its one-line compressed form left here. That is what
    the restructure was for, and the ratchet it exists to stop is exactly this: the corpus
    growing back into the index one defensible paragraph at a time.
  - The new text is **governance** — a stamp, a token, a stop condition, a merge rule, a report
    section. Those must be in context every turn, so they stay.

Note which remedy is *not* on that list. This number is a **budget and nothing else** (ruling:
Scott, PR #298): the ratchet is caught per entry by `+"`TestClaudeMDIndexLedger`"+`, so raising the
budget is not how governance text earns its room. Demotion is. A law with a live control that
fails when it is violated does not need index prose teaching it — the control teaches it, at the
moment it matters — so index space goes to the laws no control can enforce, and a trip is a prompt
to find the key that has become redundant with a control. See `+"`claudeMDCeiling`"+`'s comment for
the rule and its named candidate.

**The test for "redundant with a control" is the control's failure message, not the control's
existence** (ruling: Scott, PR #339): a law is demotable when the message **states the rule and
the remedy**. The index is what a session reads *before* it acts; a demoted law is one a session
does not need to have read, because the control will say the whole thing at the moment it matters.
A message that names the violation but not the remedy leaves the reader knowing they are wrong and
not what to write instead — so **fix the message first, and the demotion becomes free**.

What this must not become is a number nobody looks at, which is what raising it silently makes
it.`, claudeMD, got, claudeMDCeiling)
	}
}
