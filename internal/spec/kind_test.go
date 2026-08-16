package spec

import (
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
	// not move kindCount. It is visible in `String()` though: the default arm returns "unsupported"
	// for anything unnamed, so an ordinal above the end that renders as something else has a case
	// of its own and is therefore a real member sitting outside the domain.
	for k := KindUnsupported + 1; k <= KindUnsupported+kindsPastUnsupported; k++ {
		if got := k.String(); got != "unsupported" {
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
