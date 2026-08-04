// This file is `package mllex_test`, not `package mllex`, and the reason is mechanical rather
// than stylistic: `keywordgen` imports `mllex` now, so an in-package test importing `keywordgen`
// is an import cycle the compiler refuses. An external test package is the one place a
// consolidated helper can be checked against the consumer it was factored out of.
package mllex_test

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/gen/mllex"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// TestMllexAgreesWithKeywordgen is what makes this package a consolidation rather than a third
// opinion.
//
// A shared helper that finds a *different* arm set than the extractor it was factored out of has
// not removed a duplicated fact, it has added one — so the two must agree, and this is the same
// control `opgen`'s TestLexerBlockAgreesWithKeywordgen already applies for the same reason.
//
// **Set comparison including the line, not counts.** Two readers finding 589 arms each could be
// finding different 589 arms, which is exactly the failure a count cannot see; and the line is
// the part a consumer cites, so a table row pointing at a continuation instead of at its head
// would be a drifted citation with a generator's alibi.
//
// Both directions are asserted here, unlike opgen's one-way containment: keywordgen reads all 589
// and so does this, so a divergence in *either* direction is a real disagreement rather than a
// difference in what each reader is looking for.
//
// A note on what this control is worth *after* the consolidation, because it changed: keywordgen
// now calls mllex, so this compares a reader against its own caller and cannot catch the two
// drifting apart — nothing is left to drift. What it still catches is the case the refactor was
// most likely to produce, which is keywordgen's own body-reading loop silently *dropping* arms
// mllex handed it (the `reKind` error path turning into a skip). That is a real failure mode and
// this is a real assertion about it; it is no longer an independence claim, and saying so is
// cheaper than letting a future reader over-read it.
func TestMllexAgreesWithKeywordgen(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefLexerMLL)
	lines := strings.Split(src, "\n")

	b, err := mllex.FindBlock(lines)
	if err != nil {
		t.Fatalf("FindBlock: %v", err)
	}
	arms, err := mllex.Arms(lines, b)
	if err != nil {
		t.Fatalf("Arms: %v", err)
	}
	kt, err := keywordgen.Extract(src, "test")
	if err != nil {
		t.Fatalf("keywordgen.Extract: %v", err)
	}

	mine := map[string]int{}
	for _, a := range arms {
		mine[a.Keyword] = a.Line
	}
	theirs := map[string]int{}
	for _, a := range kt.Arms {
		theirs[a.Keyword] = a.Line
	}

	if len(mine) == 0 || len(theirs) == 0 {
		t.Fatalf("mllex read %d arms, keywordgen %d — an agreement between two empty sets is "+
			"perfect and asserts nothing", len(mine), len(theirs))
	}
	for kw, line := range mine {
		switch other, ok := theirs[kw]; {
		case !ok:
			t.Errorf("mllex found keyword %q (lexer.mll:%d) that keywordgen did not: the shared "+
				"helper handed over an arm the extractor dropped, which is a skip where an error "+
				"belongs", kw, line)
		case other != line:
			t.Errorf("keyword %q: mllex reads it at lexer.mll:%d, keywordgen at :%d — a row citing "+
				"the wrong line is a citation that does not resolve", kw, line, other)
		}
	}
	for kw, line := range theirs {
		if _, ok := mine[kw]; !ok {
			t.Errorf("keywordgen reported keyword %q (lexer.mll:%d) that mllex did not hand it: "+
				"the extractor is manufacturing rows its substrate never produced", kw, line)
		}
	}
	t.Logf("mllex %d arms, keywordgen %d, agreeing on keyword and line", len(mine), len(theirs))
}
