package spec

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// kindsPastUnsupported is how far this file probes above KindUnsupported to check that the enum
// really ends there. Eight rather than one because a Kind is rarely added alone.
const kindsPastUnsupported = 8

// kindCount pins the enum's size, and it is the half of the domain check that a name-derived scan
// cannot supply.
//
// Every loop below runs `KindModuleBinary` … `KindUnsupported`, which is a *claim* that the enum
// has no members outside that range — the thing an iota block gives no way to ask. Pinning the
// last member's ordinal makes an insertion anywhere in the block fail here rather than silently
// shrink every domain in this file, and the probe above KindUnsupported closes the other end.
// Together they are why the scans below can be described as covering the enum instead of covering
// the part of it somebody remembered.
const kindCount = 19 // KindModuleBinary(0) … KindUnsupported(18)

// TestAssertInvalidKindsAreExactlyTheAssertInvalidForms is the name-derived second mechanism for
// `Kind.isAssertInvalid`, whose body is a three-way equality test written by hand.
//
// **The two mechanisms must not share a source, and here they genuinely do not**: the predicate
// enumerates ordinals, this test derives the same set from `String()`'s rendering. A fourth
// `assert_invalid` form added to the enum gets a `String()` case in the same edit — that is what
// the enum is *for* — and then disagrees with a predicate nobody remembered to widen. Which is
// exactly the failure the 17-head slice inflicted on the destination ledger, whose domain was
// `c.Kind == KindAssertInvalid` and stayed correct-looking while its subject grew by 17.
//
// It is not a tautology check. `isAssertInvalid` could have been derived from the name — a
// `strings.HasPrefix` on `String()` — and then this test would compare a thing to itself. The
// predicate is enumerated *because* the run loop calls it per command and the ledger per failure,
// so the string work is not wanted there; paying for it once, here, in a test, is the trade. The
// comment on the predicate says so, which makes this test the thing keeping that comment honest.
//
// Watched dying on five mutations, each tripping a *different* assertion, which is the result
// worth having: dropping `KindAssertInvalidQuote` from the predicate, renaming a form's `String()`
// off the prefix, colliding two renderings, declaring a Kind above `KindUnsupported`, and
// inserting one inside the block. The last two are the pair that mattered — they are the two ends
// of the same range claim and neither guard sees the other's mutation, so a single one of them
// would have left half the domain unasserted while reading as though it covered the enum.
func TestAssertInvalidKindsAreExactlyTheAssertInvalidForms(t *testing.T) {
	if got := int(KindUnsupported) + 1; got != kindCount {
		t.Fatalf("the Kind enum has %d members, want %d — a Kind was added or removed, so every "+
			"domain in this file just changed size. Re-base kindCount, then check each scan below "+
			"still means what its name says: a new `assert_invalid` form belongs in "+
			"Kind.isAssertInvalid, and a new form of anything else must not accidentally match "+
			"the name test", got, kindCount)
	}
	// The other end of the range. KindUnsupported is the enum's last member and the loops rely on
	// it; a Kind declared *after* it would be outside every scan, and unlike an insertion it would
	// not move kindCount. It is visible in `String()` though: the default arm returns the
	// unsupported rendering
	// for anything unnamed, so an ordinal above the end that renders as something else has a case
	// of its own and is therefore a real member sitting outside the domain.
	for k := KindUnsupported + 1; k <= KindUnsupported+kindsPastUnsupported; k++ {
		if got := k.String(); got != unsupportedRendering {
			t.Errorf("Kind(%d).String() = %q, want %q — that ordinal has a String() case, so it is "+
				"a declared Kind above KindUnsupported and outside the range every loop in this "+
				"file scans. Move it into the block before KindUnsupported, which is where the "+
				"enum's end is assumed to be", int(k), got, "unsupported")
		}
	}

	// The domain check proper, both directions, over the whole enum.
	const prefix = "assert_invalid"
	var named []Kind
	for k := KindModuleBinary; k <= KindUnsupported; k++ {
		byName := strings.HasPrefix(k.String(), prefix)
		byPredicate := k.isAssertInvalid()
		if byName {
			named = append(named, k)
		}
		switch {
		case byName && !byPredicate:
			t.Errorf("Kind(%d) renders %q but isAssertInvalid() is false — a form whose own name "+
				"says it asserts validation refusal, that every reader of the predicate will skip. "+
				"This is the ledger's 17-head failure with the sign flipped: the population grew "+
				"and the domain did not", int(k), k)
		case byPredicate && !byName:
			t.Errorf("Kind(%d) renders %q but isAssertInvalid() is true — the predicate claims a "+
				"Kind the name test cannot see. Either String() lost the prefix (and every bucket "+
				"key derived from it just changed shape) or the predicate is over-matching",
				int(k), k)
		}
	}
	if len(named) != 3 {
		t.Errorf("%d Kinds render with the %q prefix, want 3 (bare, binary, quote). A fourth form "+
			"is a real thing the corpus could contain, so this is a re-base and not a bug — but it "+
			"is re-based *here*, beside the predicate and the ledger's domain, rather than only "+
			"where it was added", len(named), prefix)
	}

	// **String() is load-bearing beyond display, and this is where that gets asserted.** The
	// destination ledger builds its bucket keys by prefixing `Kind.String()` and reads them back by
	// stripping it, so two Kinds rendering the same text would merge two populations into one
	// bucket — and merge them *silently*, since the ledger's rows would still sum to its total.
	// Distinctness is checked across the whole enum rather than across the three forms, because the
	// collision that matters is between a form and anything else, not only between the forms.
	//
	// Watching it die established that the count check above cannot stand in for this one: making
	// `KindAssertInvalidQuote` render `"assert_invalid (binary)"` leaves three Kinds carrying the
	// prefix, so `len(named)` is still 3 and both direction checks still agree. Only distinctness
	// sees it. Two assertions that look like they overlap and do not.
	seen := make(map[string]Kind, kindCount)
	for k := KindModuleBinary; k <= KindUnsupported; k++ {
		if prev, dup := seen[k.String()]; dup {
			t.Errorf("Kind(%d) and Kind(%d) both render %q — the destination ledger keys its "+
				"buckets on this string, so the two populations would share a bucket and its rows "+
				"would still close. A silent merge, which is the failure mode the ledger's "+
				"unrecognized-bucket arm cannot catch", int(prev), int(k), k)
		}
		seen[k.String()] = k
	}
}

// unsupportedRendering is `KindUnsupported`'s string and the default arm's, named once because two
// checks depend on it being the *same* string: the range probe above reads an ordinal's rendering to
// decide whether it is a declared Kind, and the vocabulary test below asserts this one is not
// mistakable for a suite atom. A literal in both places is a literal that can be updated in one.
const unsupportedRendering = "<unsupported>"

// commandHeadRE matches a top-level command's head atom in a `.wast` file: an open paren in column
// zero and the atom after it. Column zero is the corpus's own structure for commands — nested forms
// are indented — so this is the head-atom set the suite actually uses, not every atom it contains.
var commandHeadRE = regexp.MustCompile(`(?m)^\(([a-z_][a-z_0-9]*)`)

// TestKindStringsSpeakTheSuitesVocabulary is Scott's ruling on `Kind.String()` (PR #364) made
// executable: **a Kind's string names the question the harness asked, in the suite's words, plus any
// distinction the Kind adds.**
//
// The reason the board's rows must be in the corpus's terms rather than the enum's, in his words:
// `assert_invalid` is checkable against the `.wast` files by anyone, where `KindAssertInvalid` is
// checkable only against source the reader does not have open. So this test derives the admissible
// vocabulary **from the corpus** and never from a list typed beside the switch — a list would make
// this a comparison of the enum against itself, which is the shape *a control scoped to the current
// sample* names.
//
// # What it checks, and the two halves are different claims
//
//   - **Every Kind's head token is a head atom the corpus writes.** That is what caught the two
//     strings #364 fixed, from the other direction: `module` and `assert_malformed` are both real
//     corpus atoms, so the head check passes on them — it is the *sibling* check below that fails
//     them.
//   - **No Kind's string is a bare head atom shared with a sibling that discriminates.** This is the
//     collapsing hazard Scott named, one step short of an actual collision: `module` for the binary
//     form sat beside `module binary`-shaped siblings and named the *text* form's spelling, and
//     `assert_malformed` named neither of its two wrapper forms. A bare atom is admissible only when
//     no sibling Kind adds a discriminator to it — which is `assert_invalid`'s case exactly, its own
//     corpus form being the unwrapped one.
//
// # What it deliberately does not check, because the answer would be a false green
//
// **`module text` is not a string the corpus contains**, and this test cannot ask it to be. The text
// form's faithful spelling is the bare `module`, since its distinction is the *absence* of a
// wrapper; printing that would leave a board row unable to distinguish one Kind from the head atom
// of three. The added word is legibility bought against fidelity, and it is declared here — in the
// control that would otherwise be assumed to have checked it — rather than left for a reader to
// discover by grepping the suite for `(module text` and finding nothing.
func TestKindStringsSpeakTheSuitesVocabulary(t *testing.T) {
	requireSuite(t)

	heads := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(suiteDir, "*.wast"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, m := range commandHeadRE.FindAllSubmatch(src, -1) {
			heads[string(m[1])] = true
		}
	}
	// The vacuity check, and it is not decoration: an empty or tiny set makes every membership
	// test below an agreement with nothing, which is the failure mode *a comparison against an
	// empty set succeeds* names. The suite's own command vocabulary is a dozen atoms and more.
	if len(heads) < 8 {
		t.Fatalf("found %d top-level command atoms across %d suite files (%v) — the scan is not "+
			"reading the corpus, so every check below is vacuous", len(heads), len(files),
			slices.Sorted(maps.Keys(heads)))
	}

	// The head token of every Kind, and the set of tokens that follow it per head — computed in one
	// pass so the sibling test below asks about the enum rather than about a remembered grouping.
	discriminated := map[string]bool{}
	head := func(k Kind) (string, string) {
		h, rest, _ := strings.Cut(k.String(), " ")
		return h, rest
	}
	for k := KindModuleBinary; k < KindUnsupported; k++ {
		if h, rest := head(k); rest != "" {
			discriminated[h] = true
		}
	}

	for k := KindModuleBinary; k < KindUnsupported; k++ {
		h, rest := head(k)
		if !heads[h] {
			t.Errorf("Kind(%d) renders %q, whose head atom %q is not a command head the corpus "+
				"writes — a board row naming a form no `.wast` file spells is checkable against "+
				"nothing", int(k), k, h)
		}
		if rest == "" && discriminated[h] {
			t.Errorf("Kind(%d) renders the bare atom %q while a sibling Kind discriminates on %q — "+
				"so this row names the head atom of a group rather than its own form, which is the "+
				"collapsing hazard one step short of a collision. Append the distinction this Kind "+
				"adds", int(k), k, h)
		}
	}

	// KindUnsupported is the one Kind with no head atom: it is the harness reporting that it
	// recognized nothing, not a form the suite has. Its rendering must therefore be unmistakable
	// for suite vocabulary, and the assertion is against the corpus rather than against a spelling
	// preference — no `.wast` file can produce a `(<…` head, so a bracketed string cannot be read
	// as one.
	if s := KindUnsupported.String(); s != unsupportedRendering {
		t.Errorf("KindUnsupported renders %q, want %q", s, unsupportedRendering)
	}
	for _, atom := range slices.Sorted(maps.Keys(heads)) {
		if KindUnsupported.String() == atom || KindUnsupported.String() == "("+atom {
			t.Errorf("KindUnsupported renders %q, which is a suite command atom — the one Kind that "+
				"names no suite form would be reading as though it named one", KindUnsupported)
		}
	}
	if !strings.ContainsAny(KindUnsupported.String(), "<>") {
		t.Errorf("KindUnsupported renders %q with no bracket — the property that keeps it out of the "+
			"corpus's vocabulary is that the suite cannot spell it", KindUnsupported)
	}
}
