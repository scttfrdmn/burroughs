// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// `CLAUDE.md` is a **brief and a pointer page**, and `docs/laws/` is the corpus. This file holds
// the one control that survives that inversion, and what survives is a **citation check**: a page
// whose content is almost entirely links fails by having a link that resolves to nothing.
//
// # What was retired here, and why the subject dissolved rather than the mechanism
//
// Scott's directive (the four-workstream brief, 2026-08-17): *"CLAUDE.md inversion — cut it to a
// page of links, and retire the ceiling apparatus with it. The 38400 budget, the index ledger
// test, the memory-index reserve, the extent reconciliation, the mint-and-demote economy all go.
// Once the file is a pointer page there's nothing to ration."*
//
// So three things are gone, and each is gone because **its subject no longer exists**, which is
// the only admissible reason under *a tripwire whose subject dissolves is re-pointed, never
// closed*:
//
//   - **The byte ceiling** (a budget on the index's size) and **the per-entry byte ledger** (which
//     entry spent the budget) were both instruments of an *economy*: index space was scarce, a
//     mint spent it, and a demotion bought it back. A pointer page rations nothing, so a budget
//     over it measures a quantity nobody is trading. Their golden file and its `make` target went
//     with them.
//   - **The per-law bijection** — one recall key in `CLAUDE.md` per `###` heading in `docs/laws/`,
//     checked equal as text — had its subject in the recall keys. There are no recall keys now;
//     the brief carries five behaviours and the page links *families*.
//
// # What was re-pointed, because its risk did not dissolve
//
// Both directions of the old bijection named a risk that outlives the mechanism, so both are
// re-pointed at the shape the file now has:
//
//  1. **A dangling pointer.** The old direction-1 check asked "does this recall key resolve to a
//     heading with the same text?" The page is now *made of* pointers, so the same risk is
//     larger and cruder: a link to a file that does not exist, or to an anchor no heading slugs
//     to. Checked for every relative link on the page, not only the `docs/laws/` ones — the
//     contract and the ADRs are cited the same way and rot the same way.
//  2. **A law nobody can reach.** The old direction-2 check was the silent half: *a body nobody
//     points at is a law that has fallen out of context*. That risk is unchanged, and only its
//     granularity moved. Per-law reachability is no longer assertable from `CLAUDE.md`, so what
//     is asserted is **per-family**: every `docs/laws/*.md` file is linked from the page, and
//     every family holds at least one heading. A new family file nobody linked is exactly the
//     silent loss the old direction caught, one level up.
//
// # Vacuity, without a rationed count
//
// `lawsFloor` is gone with the economy — a floor on the *number of laws* was part of the mint
// accounting. The vacuity guard that replaces it is **derived rather than enumerated**: the corpus
// is asserted non-empty, and every family in it must be linked, so a link reader that silently
// stopped matching cannot produce a green — it produces one failure per family. That is the
// vacuity check the comparison needs, taken from the space instead of from a number someone has
// to maintain.

const (
	claudeMD = "../../CLAUDE.md"
	lawsDir  = "../../docs/laws"
	repoRoot = "../.."
)

var (
	// mdLinkRE matches an inline markdown link, over the **whole file** rather than a line.
	//
	// Two deliberate differences from the line-oriented form this replaces, and the first one is
	// why the replacement exists. `(?s)` lets the link *text* span a line break, which prose in
	// this repo does constantly — and the old `\[[^\]]*\]\(([^)\s]+)\)` run per line could not see
	// those links at all. Measured on the tree at the widening: **6 of 70** relative links were
	// invisible to it, **3 of them on `CLAUDE.md` itself**, which is the page its own control
	// existed to check. A reader silently skipping 3 of 26 links on its only file is *a control is
	// a pattern plus the text it is handed* aimed at this file.
	//
	// The target is captured as `[^()]*`, which **admits whitespace on purpose**. A destination
	// containing unescaped whitespace is not a link at all under CommonMark — it renders as
	// literal text — so those are captured in order to be *reported*, by the wrapped-target
	// control below, rather than skipped by a pattern that declines to ask.
	mdLinkRE = regexp.MustCompile(`(?s)\[[^\]]*\]\(([^()]*)\)`)

	// fenceRE matches a fenced-code delimiter. A link inside a fence is a literal being shown to
	// the reader, not a citation being made, so it is excluded from the population — and counted,
	// because a silent exclusion is indistinguishable from having found nothing.
	fenceRE = regexp.MustCompile("^\\s*(```|~~~)")

	// headingRE matches any heading `##` and deeper. Deliberately not `### ` only: the law
	// families use `###` for a law and `operations.md` uses `##` for a recipe, and an anchor
	// resolves against a heading of any depth.
	headingRE = regexp.MustCompile(`^#{2,6} (.+)$`)
)

// anchorFor reproduces GitHub's heading-slug rules. It exists so the check derives the anchor it
// expects from the heading text rather than trusting a transcription on either side.
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

// link is one relative link, with the file and line it was written on.
type link struct {
	src      string // repo-relative markdown file the link was written in
	target   string // the whole target as written, `path` or `path#anchor`
	path     string // the path half, as written — relative to `src`'s directory
	resolved string // the path half made repo-relative, which is what gets opened
	anchor   string // the anchor half, empty if the link has none
	line     int    // 1-indexed line in `src`, for the failure message
}

// mdSources returns every markdown file in the tree, repo-relative and sorted.
//
// **Derived, not enumerated.** The population is "markdown in this repo", so a new law family, a
// new ADR, or a new top-level page is in the domain the moment it is written — *a control scoped to
// today's cases inherits today's blind spot*. The walk routes through `skipWalkDir` for grave
// #369's reason: `.claude/worktrees/` has held full copies of this repo, and a control that walks
// "the repo" is asserting it knows where the repo ends.
func mdSources(tb testing.TB) []string {
	tb.Helper()

	var out []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// `third_party` is this walk's own documented addition, passed at the call site
			// rather than added to the shared set, which is the idiom citation_test.go's cite
			// walk uses for the same directory and the same reason: the fetched spec material is
			// **upstream's markdown**, its 100-odd files cite each other in conventions this repo
			// does not set, and four of its links were reported as this tree's violations on the
			// first run of this control. A rule about our citations has no jurisdiction there.
			if skipWalkDir(d, "third_party") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
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
		tb.Fatalf("walking for markdown sources: %v", err)
	}
	slices.Sort(out)
	return out
}

// readLinksIn returns every relative link in one markdown file, plus the ones whose destination is
// broken across a line (which are not links at all) and the count excluded as fenced literals.
//
// Absolute links (`https:`, `mailto:`) are skipped: their oracle is the network, and *split issues
// at the oracle seam* — this half's oracle is the filesystem, so this half is what runs in
// `go test`.
//
// A link's path is relative to **its own file's directory**, not to the repo root. Stated in code
// because the corpus has already paid for the other reading: a repo-root-relative path written
// inside `docs/laws/` resolves to nothing.
func readLinksIn(tb testing.TB, src string) (links, wrapped []link, fenced int) {
	tb.Helper()

	blob, err := os.ReadFile(filepath.Join(repoRoot, src))
	if err != nil {
		tb.Fatalf("reading %s: %v", src, err)
	}
	content := string(blob)
	inFence := fencedOffsets(content)
	dir := filepath.Dir(src)

	for _, m := range mdLinkRE.FindAllStringSubmatchIndex(content, -1) {
		if inFence[m[0]] {
			fenced++
			continue
		}
		raw := content[m[2]:m[3]]
		l := link{src: src, line: 1 + strings.Count(content[:m[0]], "\n")}

		// A destination with whitespace in it renders as literal text rather than as a link, so
		// it is reported by its own control rather than repaired here by rejoining — rejoining
		// would certify a citation the reader can never follow.
		if strings.ContainsAny(raw, " \t\n\r") {
			l.target = strings.Join(strings.Fields(raw), " ")
			wrapped = append(wrapped, l)
			continue
		}
		if strings.Contains(raw, ":") || strings.HasPrefix(raw, "#") || raw == "" {
			continue
		}
		l.target = raw
		l.path, l.anchor, _ = strings.Cut(raw, "#")
		l.resolved = filepath.ToSlash(filepath.Join(dir, l.path))
		links = append(links, l)
	}
	return links, wrapped, fenced
}

// fencedOffsets marks every byte offset that sits inside a fenced code block.
func fencedOffsets(content string) map[int]bool {
	in := map[int]bool{}
	off, open := 0, false
	for _, line := range strings.Split(content, "\n") {
		if fenceRE.MatchString(line) {
			open = !open
			// The delimiter line itself is inside either way.
			for i := off; i <= off+len(line); i++ {
				in[i] = true
			}
		} else if open {
			for i := off; i <= off+len(line); i++ {
				in[i] = true
			}
		}
		off += len(line) + 1
	}
	return in
}

// readLinks returns every relative link in `CLAUDE.md`. Kept as a named reader because the
// reachability check below is a claim about *that page* specifically, not about markdown at large.
func readLinks(tb testing.TB) []link {
	tb.Helper()
	links, _, _ := readLinksIn(tb, "CLAUDE.md")
	return links
}

// readCorpus returns every heading in `docs/laws/`, keyed by family file.
func readCorpus(tb testing.TB) map[string]map[string]bool {
	tb.Helper()

	files, err := filepath.Glob(filepath.Join(lawsDir, "*.md"))
	if err != nil {
		tb.Fatalf("globbing the corpus: %v", err)
	}
	if len(files) == 0 {
		tb.Fatalf("%s holds no markdown files — the corpus %s points at is empty, and every "+
			"reachability assertion below would agree with it vacuously", lawsDir, claudeMD)
	}

	corpus := map[string]map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			tb.Fatalf("reading %s: %v", f, err)
		}
		family := strings.TrimSuffix(filepath.Base(f), ".md")
		corpus[family] = map[string]bool{}
		for _, l := range strings.Split(string(src), "\n") {
			if m := headingRE.FindStringSubmatch(l); m != nil {
				corpus[family][m[1]] = true
			}
		}
	}
	return corpus
}

// headingsIn returns the headings of an arbitrary repo-relative markdown file, for anchor
// checking outside `docs/laws/` — the contract and the ADRs are linked the same way.
func headingsIn(tb testing.TB, path string) map[string]bool {
	tb.Helper()

	src, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, l := range strings.Split(string(src), "\n") {
		if m := headingRE.FindStringSubmatch(l); m != nil {
			out[m[1]] = true
		}
	}
	return out
}

// TestMarkdownLinksResolve is the dangling-pointer half. Every relative link in every markdown file
// in the tree names a file that exists, and every anchor names a heading that slugs to it.
//
// A separate test from the reachability check below, because the two fail for unrelated reasons
// and *one test asserting two properties that fail with one message is the partition defect*
// (grave #34): this one says a pointer leads nowhere, that one says a law cannot be reached.
//
// # Why the domain is the tree and not `CLAUDE.md` (#466, Scott's ruling on PR #465)
//
// It was `CLAUDE.md`'s control for as long as `CLAUDE.md` was the only page made of pointers. It is
// not: the law corpus cross-references itself, `CHANGELOG.md` cites the law a change minted, and
// the ADRs cite both. **Measured at the widening: 23 anchor-bearing links, 9 on `CLAUDE.md` and 14
// in no control's domain** — and the two that were stale were stale for half an hour inside PR #465
// itself, created by renaming a heading whose incoming citations nothing checked.
//
// # The half this does not cover, stated because coverage is a claim
//
// **A citation carrying no anchor at all still passes.** PR #465's own near-published defect was a
// law cited under a title the corpus does not contain, written as `[…](boards-and-buckets.md)` with
// no fragment: the file exists, so this control is satisfied, and the sentence is still false. What
// is checked here is that a pointer leads *somewhere*; that it leads to the law the prose names is
// not checked by anything, and #466 says so rather than letting a widened control be read later as
// covering the class it belongs to.
func TestMarkdownLinksResolve(t *testing.T) {
	sources := mdSources(t)
	if len(sources) == 0 {
		t.Fatalf("found no markdown files under %s — every assertion below is a loop over matches, "+
			"so an empty domain satisfies all of them vacuously", repoRoot)
	}

	// Derived vacuity, taken from the space rather than from a maintained number: the corpus is
	// the one part of the domain that is known to exist by another reader in this file, so a walk
	// that lost it is caught without pinning a count that every new page would have to update.
	scanned := map[string]bool{}
	for _, s := range sources {
		scanned[s] = true
	}
	for family := range readCorpus(t) {
		if want := "docs/laws/" + family + ".md"; !scanned[want] {
			t.Errorf("%s is in the corpus and was not among the %d markdown files this control "+
				"walked. The domain is derived from a tree walk, so a family missing from it means "+
				"the walk's boundary moved, not that the family is gone", want, len(sources))
		}
	}

	var links, anchored, fenced int
	for _, s := range sources {
		found, _, fencedHere := readLinksIn(t, s)
		links += len(found)
		fenced += fencedHere

		for _, l := range found {
			info, err := os.Stat(filepath.Join(repoRoot, l.resolved))
			if err != nil {
				t.Errorf(`%s:%d: the link %q names a path that does not exist (resolved: %s).

A page made of pointers fails by having a pointer that resolves to nothing — a reader following it
finds a tombstone with no inscription, and nothing else on the page is wrong. Either fix the path,
or delete the claim it was supporting.`, l.src, l.line, l.target, l.resolved)
				continue
			}
			if info.IsDir() || l.anchor == "" {
				continue
			}
			anchored++
			found := false
			for h := range headingsIn(t, l.resolved) {
				if anchorFor(h) == l.anchor {
					found = true
					break
				}
			}
			if !found {
				t.Errorf(`%s:%d: the link %q names an anchor no heading in %s slugs to.

The anchor is derived from the heading text, so this fires when the heading was reworded and the
link was not — the citation still looks resolvable and lands the reader at the top of the file
instead of at the law. Re-derive it from the heading, or point at the file without an anchor.`,
					l.src, l.line, l.target, l.resolved)
			}
		}
	}

	if links == 0 {
		t.Fatalf("found no relative links across %d markdown files. Either the tree stopped citing "+
			"itself, which is the whole finding, or this reader stopped being able to see links",
			len(sources))
	}
	// The anchor half is the half that rots, so a reader that stopped seeing anchors would pass
	// every loop above by asking nothing. Asserted separately from the link count for that reason.
	if anchored == 0 {
		t.Fatalf("found %d relative links across %d markdown files and not one anchor among them. "+
			"The heading-slug comparison below is the point of this control, and it just ran over "+
			"an empty set", links, len(sources))
	}
	if !t.Failed() {
		t.Logf("%d markdown files, %d relative links, %d of them anchor-bearing, %d link(s) "+
			"excluded as fenced literals", len(sources), links, anchored, fenced)
	}
}

// TestMarkdownLinkTargetsAreNotWrapped is the third failure mode, and it is its own test because it
// fails for a different reason than the two above: not a citation that leads nowhere, but **a
// citation that is not a link at all**.
//
// A CommonMark destination may not contain unescaped whitespace, so a target broken across a line
// renders as literal square-bracketed text. The reader cannot follow it and no anchor check ever
// runs on it — it is a citation that fails *silently in the rendered page*, which is worse than a
// dangling one because the failure is invisible to `grep` for a `#anchor` too.
//
// **This starts green over an empty population, so it is a tripwire and was watched die before
// being believed** (`a control isn't born until it's watched die`): a hand-inserted wrapped target
// in a scratch copy is reported with its file and line. The population is zero at the widening
// because the tree has never had one, not because nothing is being asked.
func TestMarkdownLinkTargetsAreNotWrapped(t *testing.T) {
	sources := mdSources(t)
	if len(sources) == 0 {
		t.Fatalf("found no markdown files under %s, so this control asserted nothing", repoRoot)
	}

	total := 0
	for _, s := range sources {
		links, wrapped, _ := readLinksIn(t, s)
		total += len(links) + len(wrapped)
		for _, l := range wrapped {
			t.Errorf(`%s:%d: the link destination %q is broken across a line, so this is not a link.

CommonMark forbids unescaped whitespace in a destination: the rendered page shows the bracketed
text literally and the reader has nothing to follow. Put the whole destination on one line — the
link *text* may wrap freely, and wrapping it is how the line stays inside the margin.`,
				l.src, l.line, l.target)
		}
	}
	if total == 0 {
		t.Fatalf("found no markdown links at all across %d files, so the wrapped-destination check "+
			"ran over an empty set", len(sources))
	}
	if !t.Failed() {
		t.Logf("%d markdown link(s) across %d files, 0 with a wrapped destination", total, len(sources))
	}
}

// TestLawFamiliesAreReachable is the silent half, re-pointed: *a body nobody points at is a law
// that has fallen out of context*, at family granularity now that the page no longer indexes laws
// one by one.
func TestLawFamiliesAreReachable(t *testing.T) {
	corpus := readCorpus(t)
	linked := map[string]bool{}
	for _, l := range readLinks(t) {
		if !strings.HasPrefix(l.path, "docs/laws/") || !strings.HasSuffix(l.path, ".md") {
			continue
		}
		linked[strings.TrimSuffix(filepath.Base(l.path), ".md")] = true
	}

	for family, headings := range corpus {
		if !linked[family] {
			t.Errorf(`docs/laws/%s.md is in the corpus and is not linked from %s.

A family nobody points at is a set of laws that has fallen out of context: the agent they govern
never reads them, and nothing goes red. Add it to the corpus list on the page.`, family, claudeMD)
		}
		if len(headings) == 0 {
			t.Errorf(`docs/laws/%s.md holds no headings, so it is linked and empty.

Reachability is a claim about content, not about a filename — a family with nothing in it makes
every anchor check above pass by having nothing to disagree with.`, family)
		}
	}
	// Guarded, because an unconditional log here printed "11 law families, all linked" *beside*
	// the failure that said one was not — a witness contradicting the verdict in the same output,
	// which is *an error message is testimony* in the log channel. Found by falsifying this test.
	if !t.Failed() {
		t.Logf("%d law families, all linked from %s", len(corpus), claudeMD)
	}
}
