package keywordgen

import (
	"fmt"
	"slices"
	"strings"
)

// Table is one extraction's result: the keywords, plus the provenance needed to audit it.
type Table struct {
	// SourceSHA is the reference revision the arms were read from. Stamped, not
	// deduced: a generated artifact whose provenance needs git archaeology has hearsay
	// for authority (0007, condition 3).
	SourceSHA string
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
func Extract(src, sha string) (*Table, error) {
	lines := strings.Split(src, "\n")

	lo, hi := -1, -1
	for i, l := range lines {
		// The head is two lines: `| keyword as s` then `{ match s with`. Both are
		// required, because `| keyword as s` alone also introduces nothing this
		// extractor could read, and matching one line would locate a block that is not
		// the block.
		if lo < 0 && reBlockStart.MatchString(l) && i+1 < len(lines) && reBlockMatch.MatchString(lines[i+1]) {
			lo = i + 2
			continue
		}
		if lo >= 0 && reFallthrough.MatchString(l) {
			hi = i
			break
		}
	}
	if lo < 0 || hi < 0 {
		// Not an unrecognized *arm* — the block itself is gone. Distinguished from
		// errUnrecognized because the diagnosis differs: this is "upstream moved the
		// lexer", not "upstream changed one arm". Same split as opcodegen's locate
		// failure.
		return nil, fmt.Errorf("%w: could not locate `| keyword as s` / `{ match s with` .. `| _ -> unknown lexbuf` (found %d..%d)",
			ErrVacuous, lo, hi)
	}

	var arms []Arm
	for i := lo; i < hi; i++ {
		got, err := parseArm(lines, i)
		if err != nil {
			return nil, err
		}
		if got != nil {
			arms = append(arms, *got)
		}
	}

	t := &Table{SourceSHA: sha, Arms: arms, fallthroughLine: hi + 1}
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

// parseArm reads the arm at lines[i], or reports that the line is not an arm.
//
// Lines that are not arms yield nothing. Lines that *look* like arms and cannot be
// understood are errUnrecognized — never skipped, which is the property that makes this
// extraction trustworthy in a way a careful reading is not.
func parseArm(lines []string, i int) (*Arm, error) {
	line := lines[i]
	m := reArm.FindStringSubmatch(line)
	if m == nil {
		// Only complain about lines that plausibly *are* arms. A blank line, or a
		// constructor continuing the previous arm, is not an unrecognized arm — it is
		// not an arm. The discriminator is a leading `| "`, which is the head shape:
		// anything else inside this block is a continuation, and a continuation whose
		// own arm was read is already accounted for.
		if strings.HasPrefix(strings.TrimSpace(line), `| "`) {
			return nil, fmt.Errorf("%w: lexer.mll:%d: %s", errUnrecognized, i+1, strings.TrimSpace(line))
		}
		return nil, nil
	}

	kw, rhs := m[1], strings.TrimSpace(m[2])
	// Fourteen arms at bdd7164 put their constructor on the following line (the
	// `v128.load*_splat`/`_lane` family and the five `const` forms). Read forward for
	// the kind rather than treating the arm as unreadable — but bounded by the next
	// arm's arrival, so a genuinely empty arm is an error rather than inheriting its
	// successor's constructor.
	for j := i + 1; rhs == "" && j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, `| `) {
			break
		}
		rhs = t
	}

	km := reKind.FindStringSubmatch(rhs)
	if km == nil {
		// A keyword whose token kind could not be read. Erroring rather than emitting
		// the arm with an empty Kind is the same choice as errUnrecognized itself: a
		// row that says "this keyword lexes to nothing" would be a *narrower* claim
		// than the authority's, made silently, and a consumer would read it as a
		// keyword that is not a token.
		return nil, fmt.Errorf("%w: lexer.mll:%d: no token constructor in %q (keyword %q)",
			errUnrecognized, i+1, rhs, kw)
	}
	return &Arm{Keyword: kw, Kind: Kind(km[1]), Line: i + 1}, nil
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
