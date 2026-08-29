package keywordgen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/gen/mllex"
)

// Source is one authority a table's arms were read from, in composition order.
//
// A slice rather than the single `SourceSHA` this type began with, because the wat grammar
// is the **union of the tracked set** (§9 G-2, and gates.md's ruling that gates partition
// acceptance rather than redraw the grammar) and the tracked set has more than one
// interpreter. A table read from the core pin alone omits `shared` and 67 atomic
// mnemonics, and this file's own header already predicted what that omission looks like
// downstream: `unknown operator` on a module the spec calls well-formed.
type Source struct {
	// Path is the repo-relative lexer.mll these arms were read from. Two pins both
	// license `interpreter/text/lexer.mll`, so the path — not the filename — is what
	// tells a reader which authority a row came from.
	Path string
	// SHA is the pinned revision, recorded verbatim from the pin's own fetch script.
	SHA string
	// Scope is why this authority is consulted, in one clause — carried from the pin's
	// `Why`. Present because *consultation is clause-scoped, never wholesale*: the
	// threads pin's baseline predates GC and memory64, so a wholesale read of it would
	// delete 102 core keywords rather than add 70.
	Scope string
	// Contributed is how many arms this source put into the composed table, which is not
	// the number it *extracted*: an overlay contributes only the keywords the base lacks.
	// Printed in the generated header so a reader can see the composition's shape without
	// re-running it, and so a source that silently contributed nothing is visible as a 0.
	Contributed int
}

// Table is one extraction's result: the keywords, plus the provenance needed to audit it.
type Table struct {
	// Sources is every authority this table was composed from, in application order:
	// the base first, then each overlay. Stamped, not deduced — a generated artifact
	// whose provenance needs git archaeology has hearsay for authority (0007, condition 3).
	Sources []Source
	// Arms, sorted by keyword so the generated output is stable and a diff means a real
	// change rather than a map iteration order.
	Arms []Arm
	// fallthroughLine is the 1-indexed line of `| _ -> unknown lexbuf`, the arm that
	// makes absence from this table mean "not a token".
	//
	// Recorded rather than written into the emitted prose as a literal, because that
	// prose cites it: a hand-typed line number in a generated file is a citation that
	// drifts from the authority the same file's header pins, which is the drifted-fixture
	// defect with a code generator's alibi. It is also this extractor's proof it found
	// the block's *end* — the number appearing at all means the fallthrough was matched.
	fallthroughLine int
}

// Floor is the minimum keyword count, and it is opcodegen's condition 1 in this grammar.
//
// This is *not* a sanity check on the parser's quality — it is the control on the failure
// errUnrecognized cannot see. An extractor that recognizes nothing (renamed rule, moved
// file, upstream refactor) produces zero arms and zero unrecognized lines, and a drift
// check comparing an empty table against an empty committed table agrees perfectly: a
// green with the mechanism intact and asserting nothing.
//
// 400 against 589 measured at bdd7164, which leaves upstream room to *remove* a third of
// the wat mnemonics without a false alarm while staying far above zero and far above "a
// handful" — a parser that finds 3 of 589 is as broken as one that finds none, and would
// pass any non-empty check. A floor, not a non-nil test, is the difference.
//
// One floor rather than opcodegen's per-region map because this grammar has one region:
// the keyword block is a single `match`, not four. The per-region shape there exists
// because a SIMD refactor could empty 0xfd while leaving the single-byte table intact,
// and there is no analogous partition here to under-count independently.
const Floor = 400

// Extract reads lexer.mll's source and returns the keyword table.
//
// sha is recorded verbatim into the result; the caller is responsible for it being the
// revision src was read from (scripts/fetch-spec-ref.sh pins and verifies it).
//
// **The block locating and the arm rejoining are `mllex`'s**, not this package's, and the
// consolidation is Scott's ruling on the third occurrence of the wrapped-arm shape (#78 here,
// #105 in opgen, avoided in memarggen by reading this file first). Avoidance is personal
// and does not survive the next author; one shared reader makes a fourth occurrence structural
// rather than lucky. What stays here is what this generator's grammar knows — reading a *token
// constructor* out of an arm's body — because that is genuinely different per consumer.
func Extract(src, sha string) (*Table, error) {
	block, err := mllex.FindBlock(strings.Split(src, "\n"))
	if err != nil {
		// Not an unrecognized *arm* — the block itself is gone. Distinguished because the
		// diagnosis differs: this is "upstream moved the lexer", not "upstream changed one
		// arm". Wrapped as ErrVacuous so this package's own error vocabulary is unchanged by
		// the refactor; mllex.ErrNoBlock stays readable underneath for a caller that wants it.
		return nil, fmt.Errorf("%w: %w", ErrVacuous, err)
	}
	raw, err := mllex.Arms(strings.Split(src, "\n"), block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUnrecognized, err)
	}

	arms := make([]Arm, 0, len(raw))
	for _, a := range raw {
		km := reKind.FindStringSubmatch(a.Body)
		if km == nil {
			// A keyword whose token kind could not be read. Erroring rather than emitting the
			// arm with an empty Kind is the same choice as errUnrecognized itself: a row saying
			// "this keyword lexes to nothing" would be a *narrower* claim than the authority's,
			// made silently, and a consumer would read it as a keyword that is not a token.
			return nil, fmt.Errorf("%w: lexer.mll:%d: no token constructor in %q (keyword %q)",
				errUnrecognized, a.Line, a.Body, a.Keyword)
		}
		arms = append(arms, Arm{Keyword: a.Keyword, Kind: Kind(km[1]), Line: a.Line})
	}

	t := &Table{
		Sources:         []Source{{Path: CoreAuthority, SHA: sha, Contributed: len(arms)}},
		Arms:            arms,
		fallthroughLine: block.Fallthrough,
	}
	slices.SortFunc(t.Arms, func(a, b Arm) int { return strings.Compare(a.Keyword, b.Keyword) })
	if err := t.checkFloor(); err != nil {
		return nil, err
	}
	if err := t.checkDuplicates(); err != nil {
		return nil, err
	}
	if err := t.checkShape(); err != nil {
		return nil, err
	}
	return t, nil
}

// checkDuplicates catches a keyword extracted twice, which would otherwise show up as a
// silently last-wins map entry.
//
// Cheap, and it is the control on the block-extent logic: if Extract's `hi` overshot the
// fallthrough into `and annot start = parse`, that rule's own `| "(@"...` arms would be
// read as keywords, and any collision with the keyword block would surface here rather
// than quietly overwriting. It is also how the extractor would notice the reference
// growing a genuinely duplicated arm, which ocamllex resolves by first-rule-wins and a Go
// map would resolve by insertion order — a divergence no vector could show.
func (t *Table) checkDuplicates() error {
	seen := map[string]int{}
	for _, a := range t.Arms {
		if prev, ok := seen[a.Keyword]; ok {
			return fmt.Errorf("%w: keyword %q appears at lexer.mll:%d and :%d",
				errUnrecognized, a.Keyword, prev, a.Line)
		}
		seen[a.Keyword] = a.Line
	}
	return nil
}

// CoreAuthority is the path Extract records when no pin metadata is supplied — the core
// interpreter's lexer, which is the base of every composition.
const CoreAuthority = "third_party/spec/interpreter/text/lexer.mll"

// WithSource restamps a freshly-extracted table's provenance with its pin's own, preserving
// the arm count Extract measured.
//
// Extract takes a bare SHA because its falsification tests build tables from string literals
// with no pin behind them; a caller that *does* have a pin knows the path and the clause scope,
// and those belong in the emitted header. The count is not taken from the caller: it is what
// was extracted, so a Source cannot claim a contribution it did not make.
func (t *Table) WithSource(s Source) *Table {
	s.Contributed = len(t.Arms)
	t.Sources = []Source{s}
	tag := SourceTag(s.Path)
	for i := range t.Arms {
		t.Arms[i].From = tag
	}
	return t
}

// SourceTag shortens an authority path to what a row's citation carries: `spec/lexer.mll`,
// `spec-threads/lexer.mll`. The distinguishing components only — both pins license a file
// named lexer.mll under an identical `interpreter/text/` subpath, so the filename alone
// cannot say which, and the full path repeated on 659 rows says it 659 times.
func SourceTag(path string) string {
	trimmed := strings.TrimPrefix(path, "third_party/")
	i := strings.LastIndex(trimmed, "/")
	if i < 0 {
		// A bare filename, which is what the falsification tests pass. Returned as it came:
		// a tag is a shortening, and there is nothing to shorten.
		return trimmed
	}
	dir, file := trimmed[:i], trimmed[i+1:]
	// The pin's own directory, not the file's — `interpreter/text` is common to both.
	if i := strings.Index(dir, "/"); i >= 0 {
		dir = dir[:i]
	}
	return dir + "/" + file
}

// Compose widens base with every keyword overlay has and base lacks, and returns the union.
//
// # Why a difference and not a union with a precedence rule
//
// They produce the same table here, and the difference is what happens when they stop
// agreeing. Measured across the two pins at bdd7164 and cc535ad:
//
//   - 70 keywords are in the threads pin and not in core — 67 atomic mnemonics plus
//     `shared`, `thread`, `wait`. These are the ones this function takes.
//   - 102 are in core and not in threads, because the threads baseline predates GC and
//     memory64. So the composition cannot be symmetric: reading the threads pin as an
//     equal would *delete* those 102, which is exactly the wholesale read the threads
//     pin's own `Why` warns against.
//   - 11 literals are in both with *different kinds* — `f32`/`f64`/`i32`/`i64` are
//     NUMTYPE in core and NUM_TYPE in threads, the six vector shapes are VECSHAPE versus
//     VEC_SHAPE, and `v128` is VECTYPE versus VEC_TYPE. Pure upstream renames, but a
//     union that let the overlay win on a collision would rewrite those 11 rows into a
//     vocabulary the rest of the table does not speak, and `plaininstrShapes` dispatches
//     on those names.
//
// A base-wins union and an overlay-difference are the same function; this one is spelled as
// the difference because the *skip* is the load-bearing step and a precedence flag would let
// a future caller invert it. The overlay's 10 kind names collide with none of core's 173,
// falsified by running the same comparison over the intersection instead, where 99 of 102
// collide — so the zero is a measurement and not an analytic one.
// Composition accumulates: the result carries every source the base carried plus the overlay,
// so a third pin appends rather than displacing the second. Written that way on the first
// draft's own bug — a two-source signature made the middle authority's provenance vanish the
// moment a third pin existed, and with exactly two pins nothing would have shown it.
func Compose(base, overlay *Table, overlayMeta Source) (*Table, error) {
	if base == nil || overlay == nil {
		return nil, fmt.Errorf("%w: compose needs two tables", ErrVacuous)
	}
	if len(base.Sources) == 0 {
		return nil, fmt.Errorf("%w: base table carries no provenance to compose onto", ErrVacuous)
	}
	have := make(map[string]bool, len(base.Arms))
	for _, a := range base.Arms {
		have[a.Keyword] = true
	}

	arms := slices.Clone(base.Arms)
	tag := SourceTag(overlayMeta.Path)
	added := 0
	for _, a := range overlay.Arms {
		if have[a.Keyword] {
			continue
		}
		a.From = tag
		arms = append(arms, a)
		added++
	}

	overlayMeta.Contributed = added
	t := &Table{
		Sources:         append(slices.Clone(base.Sources), overlayMeta),
		Arms:            arms,
		fallthroughLine: base.fallthroughLine,
	}
	slices.SortFunc(t.Arms, func(a, b Arm) int { return strings.Compare(a.Keyword, b.Keyword) })

	// An overlay that contributes nothing is the failure this cannot otherwise see: a
	// mis-pointed path, a pin fetched at the wrong revision, or an upstream merge that
	// made the overlay a subset all produce a composed table identical to the base, which
	// drift-checks clean against a committed file generated the same wrong way. The
	// specimen is this package's own Floor doc — an extractor recognizing nothing agrees
	// perfectly with an empty commit.
	if added == 0 {
		return nil, fmt.Errorf("%w: overlay %s at %s contributed 0 of its %d keywords; every one "+
			"is already in the base, so the composition is the base and the second authority is "+
			"asserting nothing", ErrVacuous, overlayMeta.Path, overlayMeta.SHA, len(overlay.Arms))
	}
	if err := t.checkFloor(); err != nil {
		return nil, err
	}
	if err := t.checkDuplicates(); err != nil {
		return nil, err
	}
	if err := t.checkShape(); err != nil {
		return nil, err
	}
	return t, nil
}

// checkFloor is the vacuity control. Read the doc on Floor for why it is not a sanity
// check but the control on a specific failure errUnrecognized is blind to.
func (t *Table) checkFloor() error {
	if len(t.Arms) < Floor {
		return fmt.Errorf("%w: extracted %d keywords, floor is %d — the extractor recognized "+
			"implausibly little, which is what a moved file or a renamed rule upstream looks "+
			"like; an empty table would otherwise drift-check clean against an empty commit",
			ErrVacuous, len(t.Arms), Floor)
	}
	return nil
}

// checkShape asserts every extracted keyword is lexable *as a keyword*.
//
// This is the check with no counterpart in opcodegen, and it exists because this grammar
// has something decode.ml's does not: a token shape the arm heads must satisfy for the
// arms to be reachable at all. `let keyword = ['a'-'z'] (letter | digit | '_' | '.' |
// ':')+` (lexer.mll:111) is what ocamllex matched *before* the `match s with` ran, so an
// arm head outside that charset is dead code in the reference and would be a keyword in
// our table that no input can produce. Notably `/` is absent from the charset, which is
// the character that puts `i32.wrap/i64` on the `reserved` path instead — the fact three
// of the eleven mnemonic vectors turn on.
//
// So this is not a formatting check. It is the assertion that the extracted set and the
// production that gates it agree, and it would fire if the block ever acquired an arm the
// keyword rule cannot deliver — which is the reference having a bug, and worth a build
// failure here rather than a silent unreachable row.
func (t *Table) checkShape() error {
	for _, a := range t.Arms {
		if !reKeywordShape.MatchString(a.Keyword) {
			return fmt.Errorf("%w: lexer.mll:%d: keyword %q does not match the `keyword` "+
				"production (lexer.mll:111) that gates this block, so no input can reach it",
				errUnrecognized, a.Line, a.Keyword)
		}
	}
	return nil
}
