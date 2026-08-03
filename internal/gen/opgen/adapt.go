package opgen

import (
	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/gen/opcodegen"
)

// This file is the *only* place that names the other two generators' types.
//
// Extract takes an OpTable interface and a []Keyword slice rather than importing them,
// because the falsification tests need to hand it a table with an injected defect and a
// keyword set with a hole — inputs no real extraction produces. That seam is worth having.
// What it is not worth is two spellings of the adaptation: the test had `opsOf`/`keywordsOf`
// and the cmd would have needed its own copies, which is 0006's drift risk over a mapping
// that decides *which opcode a mnemonic gets*. So the adapters are exported here and both
// callers use them.

// OpsOf adapts the generated opcode table (0007) to the join's OpTable.
//
// **This function is the whole of the coupling to opcodegen**, and it is three lines because
// the join reads exactly one fact from that table: which codes a constructor names. Arms with
// no mnemonic — the prefix escapes, the illegal arms, the misplaced-END reporters — carry no
// constructor and so cannot be joined to a keyword; skipping them is not a filter on the
// table's content but the absence of a join key.
func OpsOf(t *opcodegen.Table) OpTable {
	byCtor := map[string][]Code{}
	for _, a := range t.Arms {
		if a.Mnemonic == "" {
			continue
		}
		byCtor[a.Mnemonic] = append(byCtor[a.Mnemonic], Code{Prefix: a.Prefix, Code: a.Code})
	}
	return mapTable(byCtor)
}

// mapTable is OpTable over a plain map. Exported through OpsOf rather than directly, so a
// caller cannot construct one and skip the mnemonic-absence rule above.
type mapTable map[string][]Code

func (m mapTable) CodesFor(c string) []Code { return m[c] }

// KeywordsOf adapts the generated keyword table (0009) to the join's input.
func KeywordsOf(t *keywordgen.Table) []Keyword {
	out := make([]Keyword, 0, len(t.Arms))
	for _, a := range t.Arms {
		out = append(out, Keyword{Keyword: a.Keyword, Kind: string(a.Kind), Line: a.Line})
	}
	return out
}

// Join runs both predecessor extractions and this one, from the three reference sources.
//
// One SHA, not three, and it is the caller's claim that all three sources were read at that
// revision — the join is only meaningful if they were. `cmd/opgen` reads it from the fetch
// script's pin for the reason keywordgen's cmd does: a SHA typed at a second site is a
// citation that can drift from the pin it claims to describe.
func Join(dec, mly, lex, sha string) (*Table, error) {
	ot, err := opcodegen.Extract(dec, sha)
	if err != nil {
		return nil, err
	}
	kt, err := keywordgen.Extract(lex, sha)
	if err != nil {
		return nil, err
	}
	return Extract(mly, lex, KeywordsOf(kt), OpsOf(ot), sha)
}
