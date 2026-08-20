// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// # The foreclosing-word sweep
//
// A wrong number invites re-measurement. A wrong *foreclosure* does not: "structural",
// "unreachable", "cannot" tell the next reader there is no work here, so the sentence is read as a
// reason to stop and nobody prices what it cost. This project has now been bitten four times.
//
//   - `internal/validate/vec.go` said relaxed SIMD's opcodes were "unreachable while the gate is
//     off". The gate had been on for three days (`7315b57`, #275/ADR 0028). Grave #427.
//   - `internal/spec/spec_test.go`'s `validateFailCeiling` account called its eight-row residue
//     **structural**, on the premise that the eight were "relaxed-SIMD operators whose gate is its
//     own event". Same stale gate, same PR to fix: eight rows of unworked engine, and the repair was
//     the deletion of a guard.
//   - the Docker box was called "unavailable" (`operations.md`'s cross-architecture recipe), which
//     was a claim about an environment and was also false.
//   - `internal/interp/bulk_test.go` ruled `0xfd` out as a tripwire's replacement subject because "a
//     v128 module never reaches an interpreter switch at all on the default board". Grave #428, and
//     the one that makes the case for an instrument: **it was true when written** (2026-08-07, #171)
//     and was falsified on 2026-08-11 by `0e41f9d`, a commit in another package that flipped SIMD
//     default-on. The first three a careful reader could in principle have caught. This one required
//     re-reading a tripwire's rationale while flipping a gate, which nobody does and nothing asked.
//
// # What this checks, and why it is not a citation sweep
//
// A marker-presence rule — "a foreclosing claim must cite something" — would have passed **all
// four**. `vec.go`'s paragraph cited `gateRelaxedSIMD` and `binary/gatemap.go`; the ceiling account
// cited ADR 0025; `bulk_test.go` cited `gatemap.go:180`, and cited it *correctly*. Every one of them
// named its mechanism. What made them wrong is that the mechanism was a **mutable fact** — a gate's
// default state — and nothing re-read the sentence when the fact moved.
//
// So the teeth are the gate table, not the citation: in the three positions where being wrong stops
// work, a paragraph that uses a foreclosing word *and* names a feature gate is checked against
// `DefaultFeatures()`. If the gate is on, the paragraph is flagged unless it is licensed here with a
// reason. Gates come from the `Features` struct by parsing it, and the default set from
// `DefaultFeatures`' own composite literal — one authority, so a gate added or flipped tomorrow is
// in this control's domain without anyone editing it. *Scope controls to the space.*
//
// # The three positions, and what the sweep deliberately does not cover
//
// Scott's scoping: foreclosing words matter "only in the places where being wrong stops work, which
// is bound accounts, ceiling comments, and out-of-scope registers". Those are the three, derived:
// the comment group above a `const …Ceiling`/`…Floor`, a comment group whose heading declares a
// non-goal, and a comment group standing over an `ErrUnsupported` decline. Unscoped prose is out —
// measured, and it is 276 occurrences over 85 positions, of which the overwhelming majority are this
// codebase using "cannot" and "never" *prescriptively* ("never compared to a threshold") and
// "structural" as its own term of art for a derived-domain control ("the structural bound", "the
// structural opcodes"). A control over that population would be a transcription exercise that rots
// on contact.
//
// Two gaps, stated because a named gap can be priced. The Docker "unavailable" rests on an
// environment fact and not on a gate, so **this sweep would not have caught the third of the four**;
// nothing here reaches a claim whose premise is the world outside the tree. And a paragraph can
// still be foreclosing about something that is not a gate at all — a corpus's contents, another
// package's arm — and pass. What this covers is the premise class that has cost the project three of
// its four instances.
//
// # Watched failing, five ways, and the first probe found it stillborn
//
// A control is not born until it is watched die (grave #34's family). Each mutation below was applied
// to a green tree, the result recorded, and the tree restored:
//
//  1. **`vec.go`'s pre-#427 paragraph restored verbatim → PASSED.** The founding specimen walked
//     straight through. The token list said `unreachable` and the sentence says "a rule with **no
//     reachable subject**" — written from a recollection of the defect instead of from the defect,
//     which is this file's own subject one level up. Repaired at `foreclosingWords`, re-probed, and it
//     now fires on both gates. Recorded rather than quietly fixed because the probe passing is the
//     load-bearing event: without it this control would have shipped green, cited #427 in its header,
//     and been blind to #427.
//  2. `RelaxedSIMD` flipped off in `DefaultFeatures` → **9 licences reported stale.** The gate table
//     is genuinely the authority; nothing here holds a private copy of which gates are on.
//  3. every gate flipped off → the `anyGateOn` guard fires. A control that cannot fire is not a
//     passing control.
//  4. `Features` renamed so the parse returns nothing → the `featureGateFloor` guard fires at 0
//     derived gates, which is the shape a sweep over an empty domain always has.
//  5. the walk pointed at a single package → the file and paragraph floors fire at 30 and 0. *A walk
//     that finds nothing agrees with every allow-map there is.*
func TestForeclosingClaimsAboutGatesMatchTheGateTable(t *testing.T) {
	gates := gateVariants(t)
	if len(gates) < featureGateFloor {
		t.Fatalf("derived %d feature gates, want at least %d: the `Features` struct moved or the "+
			"parse broke, and a sweep over zero gates passes by asking nothing", len(gates), featureGateFloor)
	}
	if !anyGateOn(gates) {
		t.Fatalf("no gate is on by default, so every claim resting on a gate is trivially safe and " +
			"this control cannot fire. That is either a real flip-everything-off change — in which " +
			"case say so here — or `DefaultFeatures` stopped being readable by the parse above")
	}

	sites, scanned, paragraphs := foreclosingSites(t, gates)
	if scanned < foreclosingFileFloor || paragraphs < foreclosingParagraphFloor {
		t.Fatalf("scanned %d files and %d in-position paragraphs, want at least %d and %d: a walk "+
			"that finds nothing agrees with every allow-map there is", scanned, paragraphs,
			foreclosingFileFloor, foreclosingParagraphFloor)
	}

	seen := map[string]bool{}
	for _, s := range sites {
		if !s.gateOn {
			continue // the premise holds: the gate really is off
		}
		key := s.key()
		seen[key] = true
		if _, ok := foreclosingLicensed[key]; ok {
			continue
		}
		t.Errorf("%s:%d is a %s that uses %q while naming the %s gate, which is ON in "+
			"DefaultFeatures().\n\nThis is grave #427's shape: a foreclosure resting on a gate's "+
			"state, in a position where being wrong tells the next reader there is no work here. "+
			"Either the paragraph is stale — repair it, and the eight rows #427 recovered are the "+
			"going rate — or the claim is true for a reason that does not depend on the gate being "+
			"off, in which case license it in `foreclosingLicensed` with that reason spelled out. A "+
			"citation is not a reason: all four instances of this defect carried one.\n\nParagraph:\n%s",
			s.file, s.line, s.position, s.word, s.gate, indent(s.para))
	}

	// A licence for a paragraph that has since been rewritten is a stale reason nobody re-read,
	// which is the defect one level up. `TestEverySkipSiteIsLicensed` learned this first.
	var stale []string
	for key := range foreclosingLicensed {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("`foreclosingLicensed` has an entry for %s and the sweep no longer finds it. The "+
			"paragraph moved, was rewritten, or lost its gate reference — drop the entry, since a "+
			"licence whose subject is gone is a reason held over prose that no longer says it", key)
	}
}

// TestForeclosingBoundAccountsCarryTheirReasonInline is the second half of Scott's ruling, and it is
// a separate test from the one above because the two fail for unrelated reasons.
//
// The gate-premise sweep asks whether a foreclosure's premise is *true*. This one asks whether the
// reason is *present*, which is a different defect with a different cost: a deferring paragraph is
// usually right, so nothing about it looks wrong, and what it does is charge the reader a lookup to
// find out whether the foreclosure still holds. The four instances that made the sweep necessary all
// survived a re-reading. A pointer to a paragraph ten entries up is how the next one will.
//
// Scope is the two positions where the deferral is load-bearing — bound accounts and non-goals
// registers — and deliberately not decline arms, where "same reason as the arm above" describes two
// adjacent arms of one switch and the reader is already looking at both. Gate-independent by
// construction: `foreclosingSites` emits a site whether or not the paragraph names a gate, and this
// test reads all of them.
// # Watched fail on its first run, and the one thing it found is also its precision limit
//
// It fired on `internal/spec/spec_test.go:8854`, a residue table whose `unknown label` row ended
// "Name resolution, same stratum as" — and pointed at the row above it. That row is now "Name
// resolution rather than grammar", which is shorter and says the thing.
//
// **But the foreclosing word in that paragraph was three rows away and unrelated**: a `never` about
// `assert_invalid` modules the text reader is not handed, carrying its own reason inline. So the
// pairing was a paragraph-scope coincidence, and the report was right about the prose for a reason
// that was not the one the message gave. Paragraph scope is exactly right for the gate check — the
// word and the gate it rests on are what sit lines apart — and it is *loose* here, where two
// unrelated rows of one table can be read as one claim. Recorded rather than tightened: the
// alternative is sentence scope, which would have missed the founding specimen of the sweep above,
// and a false positive that improves the prose it flags is the cheap direction to be wrong in.
//
// # Probes, all three watched to fail
//
//  1. **The repaired row restored verbatim → FAILED**, naming the file, line, position, word and the
//     matched phrase. The finding leg.
//  2. **The position filter short-circuited so nothing is examined → FAILED on the vacuity floor**,
//     `examined 0 … want at least 200`. Without this leg a green would be indistinguishable from a
//     filter that selects nothing, which is this test's own pass state.
//  3. **A licence added for a paragraph that does not defer → FAILED as stale.** The allow-map cannot
//     accumulate exemptions whose subjects are gone.
//
// The word-boundary anchoring in `deferredReason` was found the same way and before any of these: the
// unanchored draft matched "assertion above" inside this file's own vacuity-floor comment, which is a
// bound account, so the control's first false positive would have been about itself.
func TestForeclosingBoundAccountsCarryTheirReasonInline(t *testing.T) {
	sites, _, _ := foreclosingSites(t, gateVariants(t))

	inScope := 0
	seen := map[string]bool{}
	for _, s := range sites {
		if s.position == "decline arm" {
			continue
		}
		inScope++
		if !deferredReason.MatchString(s.para) {
			continue
		}
		key := fmt.Sprintf("%s:%d %s %s", s.file, s.line, s.position, s.word)
		if seen[key] {
			continue // one paragraph, one report, however many gates it happens to name
		}
		seen[key] = true
		if _, ok := deferredReasonLicensed[key]; ok {
			continue
		}
		t.Errorf("%s:%d is a %s that uses %q and points at another paragraph for its reason "+
			"(%q).\n\nScott's ruling on the PR that added this file: the word is fine when the reason "+
			"travels with it. A pointer is not the reason — it is a lookup charged to whoever reads "+
			"the bound next, and the paragraph it lands in was written about a different PR. State "+
			"the mechanism here, in one clause; if the earlier entry is genuinely load-bearing, keep "+
			"both and license the pair.\n\nParagraph:\n%s",
			s.file, s.line, s.position, s.word, deferredReason.FindString(s.para), indent(s.para))
	}

	// The vacuity leg, and it is the one this test needs most: its *pass* state is "nothing defers",
	// which is also what a filter that selects nothing reports. A floor over the paragraphs actually
	// examined separates the two. Deliberately not a floor over the *findings* — those should be
	// zero and stay zero — which is the direction the sibling above states as well: a control whose
	// green and whose blindness look identical needs a second number, and the second number is the
	// size of the population it looked at.
	if inScope < foreclosingInScopeFloor {
		t.Fatalf("examined %d bound-account and non-goals paragraphs holding a foreclosing word, "+
			"want at least %d: a green here means none of them defers its reason, and a filter that "+
			"selected nothing reports the identical green", inScope, foreclosingInScopeFloor)
	}
	t.Logf("%d foreclosing paragraphs in bound-account or non-goals position, %d deferring", inScope, len(seen))

	var stale []string
	for key := range deferredReasonLicensed {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		// Stated inline and not as "see the gate map's stale check", which is this test's own subject
		// applied to its own error message: a licence whose subject is gone is a reason held over
		// prose that no longer says it, and a reader of *this* failure should not have to go and
		// read a different one to find that out.
		t.Errorf("`deferredReasonLicensed` has an entry for %s and this sweep no longer finds it. "+
			"Drop it: a licence whose subject is gone is a reason held over prose that no longer "+
			"says it, so it reads as a checked exemption while checking nothing", key)
	}
}

// featureGateFloor and the two below are vacuity floors, not budgets: they catch the walk that
// finds nothing and the parse that returns nothing, both of which pass every assertion above by
// asking no question. Exact-count pins would be a maintenance tax with no defect to catch — the
// populations here move whenever anyone writes a comment — but a *floor* of zero is the one value
// that makes the control agree with itself.
const (
	featureGateFloor          = 9
	foreclosingFileFloor      = 200
	foreclosingParagraphFloor = 700
	// foreclosingInScopeFloor is the deferred-reason sweep's own vacuity floor: the paragraphs it
	// examines, not the ones it flags. **200 against a measured 227**, and the 27 of slack is the
	// honest bound rather than the tight one, because the population is every comment anyone writes
	// in a bound-account or non-goals position and it moves every PR — it moved by one in the very
	// next PR (#431's repair added a paragraph) and by one again inside that same PR (grave #434's
	// correction added another), which is the demonstration rather than the hypothetical: two
	// re-bases of this sentence in two commits. A floor bounds the catastrophic case only — a moved walk, a broken parse, a
	// filter inverted — so the residual is covered the other way, by `t.Logf`ing the count beside it
	// every run and re-basing this sentence when it moves. That is the pairing
	// `allOnPassFloor` did not have when it sat 3380 behind its measurement: the number was never
	// printed next to the constant, so nothing made the distance visible to a reader.
	foreclosingInScopeFloor = 200
)

// deferredReasonLicensed carries one entry per (file, position, word) paragraph that keeps its
// pointer to an earlier entry. Keyed without the gate, because the deferral is a property of the
// sentence and a paragraph naming two gates is still one sentence.
var deferredReasonLicensed = map[string]string{}

// foreclosingLicensed carries one entry per (file, position, word, gate) the sweep finds where the
// gate is on and the paragraph is right anyway. The value is the reason, and it must name why the
// claim does not depend on the gate — "cited ADR 0025" is what the defective versions all had.
//
// The idiom is `TestEverySkipSiteIsLicensed`'s and so is the reason for it: an allow-map with a
// per-entry justification makes the author write the sentence they would otherwise skip, and it has
// twice caught an author who knew the rule.
var foreclosingLicensed = map[string]string{
	// The historical-account class. `spec_test.go`'s bound comments are a changelog of past
	// measurements, so a foreclosing word inside a dated PR narrative is a claim about the board as
	// it stood then, not a statement that there is no work now. The tense is the mechanism.
	"internal/spec/spec_test.go:6955 bound account unreachable SIMD": "past-tense account of #328's " +
		"session; `unreachable` describes ErrNotValidated's call sites after #9 lands, a conditional " +
		"about a tracked issue rather than a claim resting on the SIMD gate",
	"internal/spec/spec_test.go:8705 bound account never SIMD": "past-tense account of #196's board; " +
		"`never asked` is what the default lane did at that measurement, and the paragraph exists to " +
		"record a delta rather than to forecast one",
	"internal/spec/spec_test.go:8705 bound account never RelaxedSIMD": "as above — the paragraph names " +
		"both gates while describing one past board",
	"internal/spec/spec_test.go:10430 bound account structural SIMD": "`structural` here is this " +
		"codebase's term of art for a derived-domain control as opposed to a per-vector allowlist " +
		"(\"the structural bound\", \"verified against the structural control\"), not a foreclosure " +
		"about a residue — the homonym, and the reason a token sweep over unscoped prose is noise",
	"internal/spec/spec_test.go:10430 bound account structural RelaxedSIMD": "as above, the control " +
		"sense of `structural`",
	"internal/spec/spec_test.go:11111 bound account structural SIMD": "the control sense again, in the " +
		"flip-reconciliation paragraphs: \"verified against the structural control, the all-on lane " +
		"reports 0\"",
	"internal/spec/spec_test.go:11111 bound account structural RelaxedSIMD": "as above",

	// The two paragraphs grave #427 rewrote. The word stays on the page as testimony — a corrected
	// transcript is a worse record than an annotated one — so the sweep finds it and must be told
	// that the instance it is looking at is the grave's own record of itself.
	"internal/spec/spec_test.go:10461 bound account structural SIMD": "grave #427's own record: the " +
		"falsified sentence is quoted here so the cost is legible, and the paragraph around it states " +
		"the residue is 0 and the gate is on",
	"internal/spec/spec_test.go:10461 bound account structural RelaxedSIMD": "as above — #427's " +
		"quoted testimony",
	"internal/spec/spec_test.go:10479 bound account structural SIMD": "as above; this is the " +
		"`validateFailCeiling` half of the same annotation",
	"internal/spec/spec_test.go:10479 bound account structural RelaxedSIMD": "as above",
	"internal/spec/spec_test.go:10492 bound account structural SIMD": "as above; the " +
		"`validateDeclineCeiling` half",
	"internal/spec/spec_test.go:10492 bound account structural RelaxedSIMD": "as above",

	// Grave #427's record at the fix site. This is the paragraph the falsification probe restored to
	// confirm the sweep can see the original defect, so it is licensed at the *repaired* version and
	// fires at the defective one — which is the only arrangement that proves both directions.
	"internal/validate/vec.go:96 non-goals register no reachable SIMD": "grave #427's record: the " +
		"falsified sentence is quoted, and the paragraph's own next clause is that both its halves " +
		"were false and `DefaultFeatures()` has had both gates on since `7315b57`",
	"internal/validate/vec.go:96 non-goals register no reachable RelaxedSIMD": "as above — the " +
		"quoted sentence names the gate it was wrong about",

	// Grave #428's record, the same annotate-rather-than-correct treatment as #427's two above.
	"internal/interp/bulk_test.go:541 decline arm never SIMD": "grave #428's own record: the falsified " +
		"sentence is quoted with the dates on both sides, and the paragraph replaces its conclusion " +
		"with two measured numbers (275 entries, 19 unhandled, 0 board rows) plus #429 for the " +
		"re-pointing it declines to make in passing",

	// **This file trips its own check, and licensing it is the right answer rather than exempting
	// the file.** The header quotes all four defective sentences, and a scanner reads tokens, not
	// quotation marks — the same law a `make close` report earned by reporting a banned form in the
	// banned form. Exempting `foreclose_test.go` wholesale would blind the sweep to a foreclosure
	// written into this file *later*, which is the one place a reader would least expect to check.
	"internal/testenv/foreclose_test.go:25 non-goals register unreachable SIMD": "this control's own " +
		"header, quoting the four instances it exists because of; the words are cited defects rather " +
		"than assertions, and the paragraph's whole claim is that they were false",
	"internal/testenv/foreclose_test.go:25 non-goals register unreachable RelaxedSIMD": "as above — " +
		"the specimen list names both gates because two of the four rested on them",
	"internal/testenv/foreclose_test.go:79 non-goals register unreachable RelaxedSIMD": "the " +
		"falsification-probe record: probe 1 quotes the token the list wrongly held and probe 2 names " +
		"the gate it flipped. Three self-licences in this file is the expected cost of a control that " +
		"documents its own specimens, and it is cheaper than the alternative — a file-level exemption " +
		"would blind the sweep in the one file whose author is least likely to be re-read",

	// Claims that are true for reasons no gate can move.
	//
	// **This class is the one that failed, and #432 is filed on it.** Its predecessor held a licence
	// for `vec.go`'s alignment paragraph reading "alignment `cannot` be checked here because
	// `decodeMemop` drops the memarg's alignment before this package sees it — a fact about what the
	// decoder retains, stated with its call site, and independent of every gate". Every word of that
	// about *gate-independence* was right, and the premise it restated had been false since #306
	// landed in #313 — so the sweep flagged grave #431's paragraph and this map cleared it. The class
	// heading says "true"; the only property anything examined was gate-independence. A licence
	// asserts a fact and nothing checks the licence's own fact.
	//
	// Until #432 rules, the standard applied to entries here is the narrowest one available: **the
	// ground must be checkable by reading the licensed paragraph itself**, not by trusting a claim it
	// makes about another file. "The word sits inside a verbatim quotation this paragraph refutes" is
	// such a ground. "The premise is a fact about the decoder" is not — that is a forward reference,
	// and a forward reference is what a citation-presence rule would have accepted from all four
	// founding specimens.
	"internal/validate/vec.go:27 non-goals register cannot SIMD": "the claim is that vector families " +
		"`cannot` be classified from their mnemonics by eye — a fact about the names " +
		"(`i32x4_bitmask` vs `i32x4_neg`), which is why the table is generated. Flipping any gate " +
		"leaves it exactly as true, and the ground is legible in the paragraph: it names the two " +
		"mnemonics it is about",
	"internal/validate/vec.go:58 non-goals register cannot SIMD": "the `cannot` is inside a verbatim " +
		"quotation of the falsified claim this paragraph exists to refute (grave #431), and the " +
		"refutation is in the same paragraph and in the same breath — clause-by-clause, with the " +
		"validator's own message printed. Checkable without leaving the paragraph, which is this " +
		"class's standard until #432 rules. The quote is inline rather than block-indented for a " +
		"mechanical reason recorded at the site: `foreclosingParagraphs` splits on blank comment " +
		"lines, so an indented quotation becomes its own paragraph and reads as a live assertion",
	"internal/validate/validate.go:175 non-goals register never SIMD": "the surviving `never` is " +
		"past-tense (\"the default lane never asked\") about slice 10's gated remainder; the " +
		"gate-dependent sentence that used to end this paragraph is quoted as falsified, which is " +
		"what the sweep found in #427's own PR",
	"internal/validate/validate.go:175 non-goals register never RelaxedSIMD": "as above — the " +
		"paragraph now records its own stale sentence rather than asserting it",
}

type foreclosingSite struct {
	file, position, word, gate, para string
	line                             int
	gateOn                           bool
}

func (s foreclosingSite) key() string {
	return fmt.Sprintf("%s:%d %s %s %s", s.file, s.line, s.position, s.word, s.gate)
}

type gate struct {
	field       string
	re          *regexp.Regexp
	onByDefault bool
}

func anyGateOn(gates []gate) bool {
	for _, g := range gates {
		if g.onByDefault {
			return true
		}
	}
	return false
}

// foreclosingWords is the token class, and two entries in it are here because the first
// falsification probe found the sweep blind to its own founding specimen.
//
// The probe restored `vec.go`'s pre-#427 paragraph verbatim and **the control passed.** The reason is
// that the sentence does not say "unreachable": it says "typing them here would be a rule with **no
// reachable subject**". The token list had been written from a recollection of the sentence rather
// than from the sentence, which is the defect this file is about, one level up — so the probe earned
// its place before the control did. `no reachable` / `no subject` are the repair, and they are not a
// one-off patch: the phrase is this project's house idiom for the move, appearing in #248's title
// ("retired for having no reachable subject"), `binary.go:694`, `boardbound_test.go:437` and
// `link_census_test.go:124` ("a constant with no reachable path", grave 0003).
//
// `unreachable` also stays, with a note: it is a Wasm *opcode*, so tree-wide it is mostly a mnemonic
// rather than a claim. Position scoping is what makes it usable — 305 mentions in the tree, 25 in
// the three positions, 1 of those naming a gate that is on.
var foreclosingWords = regexp.MustCompile(`(?i)(\b(structural|structurally|unreachable|impossible|unavailable|cannot|can't|never)\b|\bno(t| )\s*reachable\b|\bno subject\b)`)

var nonGoalHeading = regexp.MustCompile(`(?i)^\s*//\s*#+\s.*(does not|do not|not do|non-?goal|out of scope|leaves out|not typed)`)

var boundName = regexp.MustCompile(`(?i)(ceiling|floor)$`)

var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// deferredReason matches a paragraph that points at *another* paragraph for its reason instead of
// carrying one — "same reason as the tenth", "as above", "see the ninth entry", "for the reason the
// tenth gave".
//
// Scott's ruling on the PR that landed this file: *"'66 unsupported (unmoved, structural)' should
// carry its mechanism inline like every other survivor — classify is untouched, so the column can't
// move. The word is fine when the reason travels with it."* The gate-premise test above cannot see
// this failure at all, because a deferring paragraph is not wrong — the reason exists, ten entries
// up, and it is even correct. What it does is make the *reader* do a lookup to find out whether a
// foreclosure still holds, which is how a stale one survives a re-reading: nobody follows the
// pointer, so nobody notices that the paragraph it lands in was itself about a different PR.
//
// The rule is checkable in exactly one direction, so that is the direction it is written in: a
// deferral phrase is banned in these positions, licensed or not present. "Also carries the
// mechanism" is not mechanically decidable, and a predicate that tried to decide it would be the
// unfalsifiable kind — so a paragraph that states its reason *and* points at the earlier entry
// either drops the pointer, which costs nothing, or licenses it with the reason it is keeping both.
// Word-boundaried on every alternative, because the unanchored first draft of `\bas above\b` would
// have matched inside "assertion above" — the phrase standing over this file's own vacuity floors,
// which is a bound account and would have been the control's first false positive, in itself.
var deferredReason = regexp.MustCompile(`(?i)(\bsame reason as\b|\bfor the (same )?reason the \w+ (gave|did)\b|\bas above\b|\bsee the \w+ (entry|paragraph)\b|\bditto\b)`)

// gateVariants derives one gate per field of `binary.Features`, with its default state read from
// `DefaultFeatures`' own literal. Both by parsing `sections.go`, and neither by a copy kept here: a
// duplicated gate table is a second authority that drifts from the first, and this control exists
// because a sentence drifted from that table.
func gateVariants(t *testing.T) []gate {
	t.Helper()

	const src = "../binary/sections.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	var fields []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Features" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, fl := range st.Fields.List {
			for _, name := range fl.Names {
				fields = append(fields, name.Name)
			}
		}
		return false
	})

	on := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "DefaultFeatures" {
			return true
		}
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			lit, ok := m.(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if val, ok := kv.Value.(*ast.Ident); ok && val.Name == "true" {
					on[key.Name] = true
				}
			}
			return true
		})
		return false
	})

	var gates []gate
	for _, field := range fields {
		// `RelaxedSIMD` is written five ways in prose. Word-boundary alternation rather than
		// punctuation-stripping: stripping merges word boundaries, and `\bGC\b` against a stripped
		// string matches inside `ingcode`.
		spaced := camelBoundary.ReplaceAllString(field, "${1} ${2}")
		alts := []string{regexp.QuoteMeta(field), strings.Join(strings.Fields(spaced), `[-_ ]`)}
		gates = append(gates, gate{
			field:       field,
			re:          regexp.MustCompile(`(?i)\b(` + strings.Join(alts, "|") + `)\b`),
			onByDefault: on[field],
		})
	}
	return gates
}

func foreclosingSites(t *testing.T, gates []gate) (sites []foreclosingSite, scanned, paragraphs int) {
	t.Helper()

	root := "../.."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipWalkDir(d) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		scanned++

		blob, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(blob), "\n")

		// Position 1, bound accounts: any `const`-or-`var` spec whose name ends in Ceiling or
		// Floor. Derived from the naming convention rather than from a list of the bounds that
		// exist today, so a new bound is in the domain the moment it is named.
		boundLines := map[int]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for _, name := range spec.Names {
				if boundName.MatchString(name.Name) {
					boundLines[fset.Position(spec.Pos()).Line] = true
				}
			}
			return true
		})

		// Position 3, decline arms: a comment standing over an `ErrUnsupported`. This is where
		// `vec.go`'s guard comment lived.
		declineLines := map[int]bool{}
		for i, l := range lines {
			if strings.Contains(l, "ErrUnsupported") {
				declineLines[i+1] = true
			}
		}

		for _, cg := range f.Comments {
			end := fset.Position(cg.End()).Line
			position := ""
			switch {
			case boundLines[end+1] || boundLines[end+2]:
				position = "bound account"
			case commentHasNonGoalHeading(cg): // position 2, out-of-scope registers
				position = "non-goals register"
			case declineNear(declineLines, end):
				position = "decline arm"
			}
			if position == "" {
				continue
			}
			for _, para := range splitParagraphs(fset, cg) {
				paragraphs++
				word := foreclosingWords.FindString(para.text)
				if word == "" {
					continue
				}
				matched := false
				for _, g := range gates {
					if !g.re.MatchString(para.text) {
						continue
					}
					matched = true
					sites = append(sites, foreclosingSite{
						file: rel, line: para.line, position: position,
						word: strings.ToLower(word), gate: g.field, gateOn: g.onByDefault,
						para: para.text,
					})
				}
				// A foreclosing paragraph naming *no* gate is still a site, with an empty gate and
				// `gateOn` false. The gate-premise test above skips it by construction — its premise
				// does not rest on a gate, so there is nothing to check it against — and the
				// deferred-reason test below wants it, since "the reason travels with the word" is a
				// property of the sentence rather than of what the sentence rests on. Emitting one
				// site set for two tests keeps the walk count at one; the alternative was a fifth
				// tree walk over the same files for a different predicate.
				if !matched {
					sites = append(sites, foreclosingSite{
						file: rel, line: para.line, position: position,
						word: strings.ToLower(word), para: para.text,
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].file != sites[j].file {
			return sites[i].file < sites[j].file
		}
		if sites[i].line != sites[j].line {
			return sites[i].line < sites[j].line
		}
		return sites[i].gate < sites[j].gate
	})
	return sites, scanned, paragraphs
}

type paragraph struct {
	text string
	line int
}

// splitParagraphs breaks a comment group at blank comment lines. Paragraph scope rather than line
// scope because every one of the four defects put the foreclosing word and the gate it rested on
// several lines apart in one paragraph — `spec_test.go`'s were three lines apart — so a line-scoped
// version would have found none of them.
func splitParagraphs(fset *token.FileSet, cg *ast.CommentGroup) []paragraph {
	var out []paragraph
	cur := paragraph{line: -1}
	flush := func() {
		if strings.TrimSpace(cur.text) != "" {
			out = append(out, cur)
		}
		cur = paragraph{line: -1}
	}
	for _, c := range cg.List {
		body := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		if body == "" {
			flush()
			continue
		}
		if cur.line == -1 {
			cur.line = fset.Position(c.Pos()).Line
		}
		cur.text += body + "\n"
	}
	flush()
	return out
}

func commentHasNonGoalHeading(cg *ast.CommentGroup) bool {
	for _, c := range cg.List {
		if nonGoalHeading.MatchString(c.Text) {
			return true
		}
	}
	return false
}

func declineNear(declineLines map[int]bool, end int) bool {
	for i := end + 1; i <= end+6; i++ {
		if declineLines[i] {
			return true
		}
	}
	return false
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ")
}
