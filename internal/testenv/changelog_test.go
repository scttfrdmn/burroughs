package testenv_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The changelog's structure is a gate rather than a habit, for the reason decision 0005
// gives for every other gate here: **a convention that depends on remembering decays across
// session boundaries**, and this one had already decayed measurably before anything checked
// it. `[Unreleased]` reached **16 group headings** — 5 `### Added`, 6 `### Fixed`, 5
// `### Changed` — where Keep a Changelog 1.1.0 allows one of each per release (#55).
//
// It lives in `internal/testenv` rather than as a `make check` shell step because the two
// claims below want a vacuity floor and two distinguishable failure messages, and a shell
// pipeline that reports "no matches" is indistinguishable from one that agrees. That is the
// whole argument the issue makes for not accepting a two-line grep, and it applies to the
// *check's own* reading of the file as much as to the file's contents. The package's other
// inventory tests (TestEverySkipSiteIsLicensed, TestEveryFuzzTargetIsGated) are the same
// shape: read the repo, assert a structural property, floor the reading.
//
// **This check makes two claims, so it is falsified twice** — duplicate a heading and it
// must fail for repetition; invent a seventh group name and it must fail for vocabulary.
// One test asserting two properties that fail with one message is the partition defect
// (grave #34): the members share an error value, so every member scores as every other.

var (
	// A release section header: `## [Unreleased]` or `## [0.0.1] - 2026-07-30`.
	releaseHeader = regexp.MustCompile(`^## \[([^\]]+)\]`)
	// A group heading: `### Added`. Anchored and exact-depth, because `#### Added` inside
	// an entry's prose is not a group heading and matching it would report a repetition
	// that does not exist.
	groupHeading = regexp.MustCompile(`^### (.+?)\s*$`)
)

// keepAChangelogGroups is the vocabulary, in Keep a Changelog 1.1.0's canonical order.
//
// A slice rather than a set because the order is part of what is being pinned: the six are
// the standard's own sequence, and "one of each" without an order still permits a section
// that reads Fixed-then-Added and a reader who has to hunt.
var keepAChangelogGroups = []string{
	"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security",
}

// TestChangelogGroupsAreCanonical asserts three things about every release section in
// CHANGELOG.md: no group heading repeats, every heading is one of the six standard groups,
// and the headings appear in the standard's order.
//
// Scoped to *every* section rather than to `[Unreleased]`, per *scope controls to the
// space*: a check that only reads the section being edited today freezes at the moment of
// authorship, and the released sections are exactly where a structural defect becomes
// permanent. #55 puts released sections out of scope for *editing* — they are history — but
// not out of scope for *reading*, and the distinction matters: if this check ever fails on
// `[0.0.1]`, the finding is real and the remedy is a ruling, not a silent exemption.
func TestChangelogGroupsAreCanonical(t *testing.T) {
	sections := changelogSections(t)

	// Vacuity floor, per *a comparison against an empty set succeeds*. A changed heading
	// depth, a renamed file, or a regexp that stopped matching leaves this loop with
	// nothing to disagree with and reports green while asserting nothing — which is the
	// stated reason a grep is not sufficient, turned on this test itself. Floored on both
	// axes because they fail independently: the section reader can work while the heading
	// reader is dead, and then every section is vacuously canonical.
	if len(sections) < 2 {
		t.Fatalf("found %d release sections in CHANGELOG.md, want >=2 ([Unreleased] plus at "+
			"least one release); the section regexp has drifted from the file, so this "+
			"test is asserting nothing", len(sections))
	}
	total := 0
	for _, s := range sections {
		total += len(s.groups)
	}
	if total < 3 {
		t.Fatalf("found %d group headings across %d sections, want >=3; a section with no "+
			"groups is canonical by vacuity, and a file with none reports perfect agreement",
			total, len(sections))
	}

	rank := map[string]int{}
	for i, g := range keepAChangelogGroups {
		rank[g] = i
	}

	for _, s := range sections {
		// Claim 1: the vocabulary. Checked before repetition, so an invented name is
		// reported as an invented name rather than as an unexpected group — the two
		// failures have different remedies and must not share a message.
		for _, g := range s.groups {
			if _, ok := rank[g.name]; !ok {
				t.Errorf("CHANGELOG.md:%d: `### %s` in section [%s] is not one of Keep a "+
					"Changelog's six groups (%s); an invented group is invisible to any "+
					"reader looking for the standard ones",
					g.line, g.name, s.name, strings.Join(keepAChangelogGroups, " · "))
			}
		}

		// Claim 2: no repetition within a section. This is #55's defect: each PR appended
		// its own group rather than merging into the existing one, so the consolidation
		// became a second motion hiding inside "cutting a release is one motion".
		seen := map[string]int{}
		for _, g := range s.groups {
			if first, dup := seen[g.name]; dup {
				t.Errorf("CHANGELOG.md:%d: section [%s] repeats `### %s`, first seen at "+
					"line %d; Keep a Changelog 1.1.0 has one group per type per release, "+
					"and a duplicate means an entry was appended rather than merged (#55)",
					g.line, s.name, g.name, first)
				continue
			}
			seen[g.name] = g.line
		}

		// Claim 3: the standard's order. Reported separately from repetition because a
		// section can be free of duplicates and still read Fixed-then-Added.
		prev := -1
		prevName := ""
		for _, g := range s.groups {
			r, ok := rank[g.name]
			if !ok {
				continue // already reported by claim 1; an unknown name has no rank
			}
			if r < prev {
				t.Errorf("CHANGELOG.md:%d: section [%s] has `### %s` after `### %s`; Keep "+
					"a Changelog's order is %s", g.line, s.name, g.name, prevName,
					strings.Join(keepAChangelogGroups, " · "))
			}
			prev, prevName = r, g.name
		}
	}
}

type changelogGroup struct {
	name string
	line int
}

type changelogSection struct {
	name   string
	line   int
	groups []changelogGroup
}

// changelogSections parses CHANGELOG.md into release sections and their group headings.
//
// Line-oriented and anchored, deliberately: a fenced code block in an entry could contain a
// line beginning `### `, and the anchor is what keeps this from reading prose as structure.
// The known hazard in the other direction — a heading that is *not* at line start because it
// is wrapped — cannot occur for a markdown heading, since markdown requires the `#` at the
// start of the line for it to be a heading at all. So the trigger's population is exactly
// the file's headings, which is the property #78's guard did not have.
func changelogSections(tb testing.TB) []changelogSection {
	tb.Helper()

	const path = "../../CHANGELOG.md"
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s: %v — CHANGELOG.md is a standard repo file and its absence is "+
			"not a reason to decline the question", path, err)
		return nil
	}

	var sections []changelogSection
	inFence := false
	for i, line := range strings.Split(string(b), "\n") {
		lineNo := i + 1

		// Fenced blocks are skipped for both patterns. A ``` toggles rather than nests,
		// which is markdown's own rule.
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		if m := releaseHeader.FindStringSubmatch(line); m != nil {
			sections = append(sections, changelogSection{name: m[1], line: lineNo})
			continue
		}
		if m := groupHeading.FindStringSubmatch(line); m != nil {
			if len(sections) == 0 {
				tb.Errorf("CHANGELOG.md:%d: `### %s` appears before any `## [version]` "+
					"header, so it belongs to no release section and no per-section check "+
					"can see it", lineNo, m[1])
				continue
			}
			s := &sections[len(sections)-1]
			s.groups = append(s.groups, changelogGroup{name: m[1], line: lineNo})
		}
	}
	return sections
}

// TestChangelogHasAnUnreleasedSection pins the one structural fact the release motion
// depends on, separately from the group checks.
//
// *Cutting a release is one motion* (decision 0004): move `[Unreleased]` under a new version
// header, open a fresh `[Unreleased]`, tag. A file whose `[Unreleased]` was consumed and not
// reopened passes every check above — it has no repeated groups because it has no groups —
// and the next PR's entry then lands under a *released* header, editing history. That is the
// vacuity failure with a specific consequence, so it gets its own assertion rather than
// riding on the floor.
func TestChangelogHasAnUnreleasedSection(t *testing.T) {
	sections := changelogSections(t)
	if len(sections) == 0 {
		t.Fatal("no release sections found in CHANGELOG.md")
	}
	if sections[0].name != "Unreleased" {
		t.Errorf("CHANGELOG.md:%d: the first release section is [%s], want [Unreleased]; "+
			"entries are added newest-first at the top, so without it the next entry lands "+
			"under a released header and edits history",
			sections[0].line, sections[0].name)
	}
	// And it must be the only one, or "the top section" is ambiguous.
	for _, s := range sections[1:] {
		if s.name == "Unreleased" {
			t.Errorf("CHANGELOG.md:%d: a second [Unreleased] section; which one an entry "+
				"belongs in is then a guess", s.line)
		}
	}
}

// unreleasedGroupCeiling is #55's number, asserted as a ceiling rather than left to the
// repetition check alone.
//
// Redundant today — one group per type means at most 6 — and kept because the two assertions
// fail differently under a *changed* reader: if the group regexp ever stops matching some
// heading style, the repetition check goes quiet and this one goes quiet with it, but a
// reader that starts matching too much (an entry's `#### Added` prose caught by a loosened
// depth) trips this first and names the count. A ceiling on a quantity is a different
// instrument from a uniqueness check on its members.
func TestUnreleasedGroupCountIsBounded(t *testing.T) {
	sections := changelogSections(t)
	// Not a skip. A skip here would need a license in `licensed`, and it would not deserve
	// one: the condition is not an absent corpus, it is a malformed file in the repo, which
	// TestChangelogHasAnUnreleasedSection already fails on. Returning without asserting
	// would be *silent degradation, a skip one step quieter* — so it says so and fails.
	if len(sections) == 0 || sections[0].name != "Unreleased" {
		t.Fatalf("no leading [Unreleased] section, so this ceiling has nothing to bound; "+
			"see TestChangelogHasAnUnreleasedSection for the diagnosis (found %d sections)",
			len(sections))
	}
	got := len(sections[0].groups)
	if got > len(keepAChangelogGroups) {
		names := make([]string, 0, got)
		for _, g := range sections[0].groups {
			names = append(names, fmt.Sprintf("%s:%d", g.name, g.line))
		}
		t.Errorf("[Unreleased] holds %d group headings, ceiling %d (one per Keep a Changelog "+
			"group): %s", got, len(keepAChangelogGroups), strings.Join(names, ", "))
	}
}
