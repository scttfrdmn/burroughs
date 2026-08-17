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
	// mdLinkRE matches an inline markdown link. Only the target matters here.
	mdLinkRE = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

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

// link is one relative link on the page, with the line it was written on.
type link struct {
	target string // the whole target, `path` or `path#anchor`
	path   string // the path half, repo-relative
	anchor string // the anchor half, empty if the link has none
	line   int    // 1-indexed line in CLAUDE.md, for the failure message
}

// readLinks returns every relative link in `CLAUDE.md`.
//
// Absolute links (`https:`, `mailto:`) are skipped: their oracle is the network, and *split
// issues at the oracle seam* — this half's oracle is the filesystem, so this half is what runs in
// `go test`.
func readLinks(tb testing.TB) []link {
	tb.Helper()

	src, err := os.ReadFile(claudeMD)
	if err != nil {
		tb.Fatalf("reading %s: %v", claudeMD, err)
	}
	var links []link
	for i, l := range strings.Split(string(src), "\n") {
		for _, m := range mdLinkRE.FindAllStringSubmatch(l, -1) {
			t := m[1]
			if strings.Contains(t, ":") || strings.HasPrefix(t, "#") {
				continue
			}
			path, anchor, _ := strings.Cut(t, "#")
			links = append(links, link{target: t, path: path, anchor: anchor, line: i + 1})
		}
	}
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

// TestClaudeMDLinksResolve is the dangling-pointer half. Every relative link on the page names a
// file that exists, and every anchor names a heading that slugs to it.
//
// A separate test from the reachability check below, because the two fail for unrelated reasons
// and *one test asserting two properties that fail with one message is the partition defect*
// (grave #34): this one says a pointer leads nowhere, that one says a law cannot be reached.
func TestClaudeMDLinksResolve(t *testing.T) {
	links := readLinks(t)

	// Vacuity, derived: the page's whole job is pointing at the corpus, so at minimum it links
	// every family. A reader that silently stopped matching links reports zero here and one
	// failure per family below — it cannot produce a green.
	if len(links) == 0 {
		t.Fatalf("found no relative links in %s. Every assertion below is a loop over matches, so "+
			"an empty set satisfies all of them; either the page stopped linking the corpus, which "+
			"is the whole finding, or this reader stopped being able to see links", claudeMD)
	}

	for _, l := range links {
		info, err := os.Stat(filepath.Join(repoRoot, l.path))
		if err != nil {
			t.Errorf(`%s:%d: the link %q names a path that does not exist.

This page is made of pointers, so a pointer that resolves to nothing is its characteristic
failure — a reader following it finds a tombstone with no inscription, and nothing else on the
page is wrong. Either fix the path, or delete the claim it was supporting.`, claudeMD, l.line, l.target)
			continue
		}
		if info.IsDir() || l.anchor == "" {
			continue
		}
		headings := headingsIn(t, l.path)
		found := false
		for h := range headings {
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
				claudeMD, l.line, l.target, l.path)
		}
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
