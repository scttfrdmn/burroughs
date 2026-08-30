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
// **This function is the whole of the coupling to opcodegen**, and it reads exactly two facts
// from that table: which codes a (constructor, operator) pair names, and which constructors it
// holds at all. Arms with no mnemonic — the prefix escapes, the illegal arms, the misplaced-END
// reporters — carry no constructor and so cannot be joined to a keyword; skipping them is not a
// filter on the table's content but the absence of a join key.
//
// **Keyed on the pair, and `opcodegen` had already named the field.** Keying on `Mnemonic` alone
// put the atomics region's 42 rmw opcodes under 7 keys, which is not an untidy table but seven
// wrong answers to "which opcode does this mnemonic encode to" — see Row.Operator. The
// constructor set is kept separately rather than derived by scanning the keys, because the
// question it answers is asked of every lowercase identifier in every arm body and is prior to
// knowing any operator.
func OpsOf(t *opcodegen.Table) OpTable {
	m := mapTable{byPair: map[string][]Code{}, ctors: map[string]bool{}}
	for _, a := range t.Arms {
		if a.Mnemonic == "" {
			continue
		}
		k := a.Mnemonic + "/" + a.Operator
		m.byPair[k] = append(m.byPair[k], Code{Prefix: a.Prefix, Code: a.Code})
		m.ctors[a.Mnemonic] = true
	}
	return m
}

// mapTable is OpTable over two plain maps. Exported through OpsOf rather than directly, so a
// caller cannot construct one and skip the mnemonic-absence rule above.
type mapTable struct {
	byPair map[string][]Code
	ctors  map[string]bool
}

func (m mapTable) CodesFor(ctor, op string) []Code { return m.byPair[ctor+"/"+op] }

func (m mapTable) Holds(ctor string) bool { return m.ctors[ctor] }

// KeywordsOf adapts the generated keyword table (0009) to the join's input.
func KeywordsOf(t *keywordgen.Table) []Keyword {
	out := make([]Keyword, 0, len(t.Arms))
	for _, a := range t.Arms {
		out = append(out, Keyword{Keyword: a.Keyword, Kind: string(a.Kind), Line: a.Line})
	}
	return out
}

// `Join(dec, mly, lex, sha)` used to stand here: three sources at one SHA, run through the two
// predecessors' single-pin `Extract`s and this one's. It is gone rather than extended, and the
// deletion is the point — its signature *was* the one-revision claim ("the join is only
// meaningful if all three were read at that revision"), which is true of one pin's three files
// and false of the pin set. A composed table's revisions are per authority, so the entry point
// is BuildFromPins, which reads each pin's SHA from that pin's own script and stamps it on the
// rows that came from it. A function whose parameter list cannot express the fact it certifies
// is a function that certifies the wrong fact.
