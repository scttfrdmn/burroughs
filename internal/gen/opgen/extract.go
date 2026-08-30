// Package opgen derives the wat mnemonic→opcode map by joining the two tables already
// generated from the reference interpreter (decision 0014).
//
// # Why a join and not a third extraction
//
// #8's encoder must answer "which opcode does `i32.add` encode to?" 494 times. Writing
// those rows by hand is out on 0007's argument — the failure is accept-direction, a wrong
// row emits *a different instruction than the text denotes*, and that decodes clean and
// scores green. Extracting them freshly is out on 0006's: `optable.go` already knows
// `i32_add` is 0x6a, and a second table knowing the same fact with no derivation between
// them is the drift risk that rule says to prefer away from.
//
// So this package reads no opcodes. It reads the *link* between two committed artifacts —
// the reference's own constructor name — and emits their join.
//
// # The two authorities, and why there must be two
//
// The reference's instruction-building arms name their constructor in two different places:
//
//	| NOP { fun c -> nop }                          the grammar body names it
//	| LOCAL_GET idx { fun c -> local_get ($2 c …) }  likewise
//	| BINARY { fun c -> $1 }                        the body names nothing; $1 is the
//	                                                lexer's payload, from
//	                                                "i32.add" -> BINARY i32_add
//
// The second shape exists because one token kind covers many mnemonics: `BINARY` is 44
// keywords, `LOAD` is 19. The kind cannot carry the opcode, so the *keyword* must, and it
// does — in the lexer arm's payload expression, which keywordgen deliberately does not
// extract (see its header: "the instruction each kind builds is the parser's business").
// This package is where that business gets done.
//
// **The two authorities are exactly complementary**: 58 kinds are named by the grammar's
// semantic actions, 436 keywords by their lexer payloads, and no keyword is named by both or
// by neither. Had the residue been non-empty it would have needed hand-writing, and 0014
// would have been decided differently.
//
// That partition is asserted by TestAuthoritiesPartitionTheKinds rather than trusted — and
// the reason it is asserted with a detector *outside* this package's readers is that trusting
// it once already failed. 0014's premise was originally measured at 51 kinds by a probe
// scoped to `plaininstr`, the same scope the reader had, so the gap came out at 0 because
// neither could see it: `select`, `block`, `loop`, `if`, `call_indirect`,
// `return_call_indirect` and `try_table` joined to nothing. See 0014's Correction section,
// and grammarConstructors for what changed.
//
// # What this package inherits from its two predecessors
//
// The property that makes an extraction trustworthy: **it never skips a line it does not
// understand.** An arm-shaped line whose shape is unrecognized is a hard error.
// And because that error cannot fire when *nothing* looks like an arm, floors cover the
// moved-file case — per-partition floors, not one total, because either authority's
// extraction could break while the other still matches and a single total would absorb it
// (the vacuity law: a comparison against an empty set succeeds).
package opgen

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/mllex"
)

// Errors this package reports. Three, and the split is diagnostic: an unreadable arm means
// upstream changed one line, a vacuous extraction means upstream moved the file, and a
// broken partition means the *premise of decision 0014* no longer holds. Those are three
// different remedies, so they are three different errors.
var (
	// ErrUnrecognized is an arm inside a located block whose shape this extractor cannot
	// read. Never an omission — see the package header.
	ErrUnrecognized = errors.New("unrecognized arm")

	// ErrVacuous is a located-nothing extraction: a region head that did not match, or a
	// count below its floor. The control on the failure ErrUnrecognized cannot see.
	ErrVacuous = errors.New("vacuous extraction")

	// ErrPartition is the premise of 0014 failing: a kind named by both authorities, or by
	// neither. Its own error because the fix is a decision, not a regexp — if upstream
	// starts naming constructors in both places, the join needs a precedence rule that
	// nobody has ruled on.
	ErrPartition = errors.New("authority partition broken")
)

// Origin says which authority named a row's constructor. Recorded rather than inferred,
// because the two partitions are floored separately and a row that cannot say where it came
// from cannot be counted.
type Origin string

const (
	// FromGrammar is a constructor read from a grammar arm's semantic action.
	FromGrammar Origin = "grammar"

	// FromLexer is a constructor read from a lexer keyword arm's payload expression.
	FromLexer Origin = "lexer"
)

// Row is one wat mnemonic and the opcode it encodes to.
type Row struct {
	// Keyword is the wat mnemonic as it appears in source text: `i32.add`, `local.get`.
	Keyword string

	// Kind is the reference token kind the keyword lexes to — keywordgen's vocabulary,
	// carried so a consumer can cross-check against keywords.go without a second lookup.
	Kind string

	// Constructor is the reference's own constructor name — `i32_add`, `local_get` — and half
	// the join key. Kept in the emitted table because it is the *evidence* for the row: a
	// reader auditing why `i32.add` is 0x6a follows this name into optable.go, which cites its
	// own decode.ml line.
	Constructor string

	// Operator is the operator constructor the instruction is applied to where it takes one —
	// `RmwXor` from `i32_atomic_rmw (Values.I32 I32Op.RmwXor) (opt a 2)` — and empty otherwise,
	// which is every core row (zero of them have one, counted by opcodegen at bdd7164).
	//
	// **It is the other half of the join key, and the join was wrong without it.** The threads
	// pin builds the read-modify-write family by applying seven constructors to six operators
	// each, so `Constructor` alone maps 42 opcodes onto 7 names: five of every six mnemonics
	// took `codes[0]` — `i32.atomic.rmw.xor` encoding as `i32.atomic.rmw.add` — and the sixth
	// landed in the ambiguity map by map-iteration order. That is the accept-direction defect
	// §9 G-3 names, in the shape this whole table exists to prevent: a wrong row emits a
	// different instruction than the text denotes, and it decodes clean. Found by reading the
	// generated ambiguity set after the pin-set composition, where 3 entries became 10 —
	// `opcodegen` had already paid for this one region over and named the field `Operator`
	// there, which is what made the diagnosis a lookup rather than an investigation.
	Operator string

	// Prefix and Code are the encoding, copied from the opcode table's matching row.
	Prefix byte
	Code   uint32

	// Origin says which authority named Constructor.
	Origin Origin

	// Line is the 1-indexed line in that authority this row's constructor was read from.
	Line int

	// From is the short tag of the file Line indexes — `spec/parser.mly`,
	// `spec-threads/lexer.mll` — and it is the other half of the citation.
	//
	// **Origin is not enough to name the file, and that is grave #529's shape one generator
	// over.** Origin says *which kind* of authority named the constructor, so a row used to
	// render `lexer.mll:266`; with two pins licensing a file of that name, line 266 of the
	// core lexer is an unrelated arm and the citation resolves — to the wrong place. Two pins
	// times two authorities is four files a row's Line can index, and Origin distinguishes two
	// of them.
	From string
}

// Ambiguity is a join key the opcode table holds under more than one encoding.
//
// **Emitted rather than resolved, and that is 0014's ruling.** Three exist at bdd7164 —
// `select` (0x1b bare, 0x1c typed), `ref_test` and `ref_cast` (0xfb 20/21 and 22/23, null
// and non-null) — and in every case the reference distinguishes them by *what follows the
// mnemonic*, not by the mnemonic. A map keyed on the constructor alone would return one of
// the two and be wrong on the other spelling with no board consequence, since both decode
// clean: the accept-direction defect §9 G-3 names. So the join reports both codes and the
// encoder chooses on the operand it read.
type Ambiguity struct {
	Constructor string
	Operator    string
	Keyword     string
	Codes       []Code
}

// Code is one (prefix, opcode) pair.
type Code struct {
	Prefix byte
	Code   uint32
}

// Authority is one pin's pair of text sources, at that pin's own revision.
//
// A pair rather than a file, because this generator is the only one of the four whose rows
// cite **two** authorities: a constructor comes from a grammar arm's semantic action or from
// a lexer arm's payload, and the two files are read together or the partition below cannot be
// checked. So a pin contributes both or neither.
//
// The paths are repo-relative and are what a row's citation names; the contents are passed in
// rather than read here so that the falsification tests can hand this package a grammar with
// an injected defect — the seam Extract's signature has always had, kept.
type Authority struct {
	// ParserPath and LexerPath are the repo-relative sources, in the spelling a row's
	// citation carries (see Row.From) and the generated header prints.
	ParserPath string
	LexerPath  string
	// Parser and Lexer are their contents.
	Parser string
	Lexer  string
	// SHA is this pin's revision, read from the pin's own fetch script.
	//
	// **Per authority, not per table.** The join used to stamp one SHA and say so — "the join
	// is only meaningful if both sides are the same revision" — which is true of one pin's two
	// files and false of the pin set: the core pin is at bdd7164 and the threads pin at
	// cc535ad, and a single stamp would have to name one of them for rows read from the other.
	SHA string
	// Scope is why this authority is consulted, in one clause, carried from the pin's `Why`.
	// Present because *consultation is clause-scoped, never wholesale* — see keywordgen.Source.
	Scope string
}

// Source is one authority's contribution to a composed table: its provenance, plus how many
// rows it actually put in.
//
// The counts are per *partition* (grammar, lexer) and per authority, which is what makes the
// widening auditable. A total is not enough: the threads pin contributes 66 lexer rows and
// exactly **one** grammar row (`ATOMIC_FENCE`), so a grammar read that silently broke would
// lose 1 row of 561 and every aggregate floor in this file would absorb it.
type Source struct {
	// ParserPath, LexerPath, SHA and Scope are the Authority's, carried through.
	ParserPath string
	LexerPath  string
	SHA        string
	Scope      string
	// Grammar and Lexer are the rows this authority's two files contributed to the composed
	// table — not the constructors it named. An authority can name a constructor that joins to
	// no keyword, and a row is the thing the emitted table has.
	Grammar int
	Lexer   int
}

// Table is one extraction's result: the rows, the ambiguities, and the provenance needed to
// audit them.
type Table struct {
	// Sources is every authority this table was composed from, in application order: the
	// base first, then each overlay. Stamped, not deduced — a generated artifact whose
	// provenance needs git archaeology has hearsay for authority (0007, condition 3).
	Sources []Source

	// Rows, sorted by keyword so the output is stable and a diff means a real change.
	Rows []Row

	// Ambiguous constructors, sorted. See Ambiguity.
	Ambiguous []Ambiguity

	// Unjoined is the keyword count that named no opcode — types, script keywords,
	// `$`-forms. Recorded because it is the arithmetic's other side: joined + unjoined
	// must equal the keyword table's size, which is what proves no keyword was dropped
	// rather than declined.
	Unjoined int
}

// Floors are the minimum row counts, per partition and in total, and they are 0007's
// condition 1 in this grammar.
//
// **Per-partition, not one total, and that is the whole point.** The join has two
// independent extractions feeding it: if `parser.mly`'s arm layout changes and the grammar
// side finds zero, a total floor of 400 still passes on the lexer side's 436 alone — an
// empty half absorbed by a full one, which is the vacuity law with a partner to hide
// behind. So each side floors separately.
//
// Set below the counts measured at bdd7164 with room for upstream to *remove* instructions
// without a false alarm, and far above "a handful": an extractor finding 3 of 436 is as
// broken as one finding none.
// **They are also not enough on their own once the table is composed**, and the reason is
// arithmetic rather than principle: the threads pin contributes 1 grammar row and 66 lexer
// rows to a table of 561, so an authority whose read broke entirely stays inside every floor
// here. What covers that is the per-authority contribution check in ExtractFrom — a floor
// bounds the catastrophic case and cannot see a 1-in-561 silent loss.
var Floors = struct {
	Grammar int
	Lexer   int
	Total   int
}{
	Grammar: 40,  // measured 59 composed (58 core + ATOMIC_FENCE)
	Lexer:   350, // measured 502 composed (436 core + 66 atomic mnemonics)
	Total:   400, // measured 561 composed (494 core + 67)
}

var (
	// A production head, which by the grammar's own layout is at column zero. Every
	// production is read, and the *name* is captured only for error messages — see
	// grammarConstructors for why the reader is not scoped to a list of productions.
	reProdHead = regexp.MustCompile(`^([a-z_][a-zA-Z_0-9]*)\s*:`)

	// An arm's head: `| TOKEN …`. The token kind is uppercase by ocamlyacc convention and
	// keywordgen already relies on that same fact.
	reArmHead = regexp.MustCompile(`^\s*\|\s*([A-Z][A-Z_0-9]*)`)

	// A lowercase OCaml identifier, which is what a constructor name looks like. Applied
	// to an arm's *action* only, and filtered against the opcode table — this deliberately
	// does not try to parse OCaml. See constructorIn.
	reIdent = regexp.MustCompile(`\b[a-z][a-z0-9_]*\b`)

	// The five regexps that read `lexer.mll` — the arm head, the arm-shaped discriminator,
	// and the block's three delimiters — used to be declared here. They are `mllex`'s now;
	// see lexerConstructors for the grave that moved them.
)

// OpTable is the opcode side of the join: the caller supplies it, because this package must
// not read `optable.go`'s *source*.
//
// **Supplied rather than parsed, and the reason is the second-declaration rule.** Reading
// the generated Go file with a regexp would make this package a parser of a file whose
// format is `opcodegen`'s business, so a change to `Emit` would break the join for reasons
// that have nothing to do with the reference. The caller passes what `opcodegen.Extract`
// returned — the same authority, at the same revision, through the code that owns it.
type OpTable interface {
	// CodesFor returns every (prefix, opcode) the reference's named constructor, applied to
	// the named operator, encodes to — or nil. More than one is an Ambiguity, never a silent
	// first-wins.
	//
	// The pair, not the constructor: see Row.Operator for the 42 rows that make it a pair and
	// the wrong answers `Constructor` alone gave.
	CodesFor(constructor, operator string) []Code

	// Holds reports whether the table names this constructor under *any* operator, and it is
	// the question constructorIn asks — "is this lowercase OCaml identifier an instruction
	// constructor at all", which is prior to knowing which operator the arm applies. Keeping
	// the two questions separate is what stops the filter from having to guess an operator in
	// order to decide whether it is looking at a constructor.
	Holds(constructor string) bool
}

// Keyword is one keyword-table entry the join reads: what keywordgen already extracted.
//
// Taken as input for the same reason OpTable is: keywords.go is generated, and re-parsing
// generated Go would be a second declaration of keywordgen's output format.
type Keyword struct {
	Keyword string
	Kind    string
	Line    int
}

// CoreParserAuthority and CoreLexerAuthority are the paths Extract records for its single
// authority: the core interpreter's text sources, which are the base of every composition.
const (
	CoreParserAuthority = "third_party/spec/interpreter/text/parser.mly"
	CoreLexerAuthority  = "third_party/spec/interpreter/text/lexer.mll"
)

// Extract joins one authority's two files against the keyword and opcode tables.
//
// mly and lex are that authority's parser and lexer sources; kws is the keyword table
// (keywordgen's arms); ops is the opcode table. sha is recorded verbatim — the caller is
// responsible for it being the revision both files were read at.
//
// **This is the one-pin entry point, and it is no longer how the committed table is built** —
// see BuildFromPins. It stays exported because the falsification tests need to hand the join a
// grammar with an injected defect and a keyword set with a hole, inputs no pin produces; the
// paths it records are the core pin's because that is the only pin a single-authority read can
// honestly claim to be.
func Extract(mly, lex string, kws []Keyword, ops OpTable, sha string) (*Table, error) {
	return ExtractFrom([]Authority{{
		ParserPath: CoreParserAuthority, LexerPath: CoreLexerAuthority,
		Parser: mly, Lexer: lex, SHA: sha,
	}}, kws, ops)
}

// ExtractFrom joins the authorities in order, base first, and returns the composed table.
//
// # The composition is base-wins, and the direction is load-bearing
//
// Each authority contributes only the constructors the ones before it did not name. That is
// keywordgen.Compose's rule and it is here for the same measured reason: the threads pin's
// baseline predates GC and memory64, so letting a later authority win would *delete* core
// clauses rather than add proposal ones. Composing constructor maps rather than whole tables
// is what this generator's shape allows — the keyword set and the opcode table arrive already
// composed by their own generators, so the only thing left to compose is the link between
// them, which is exactly the fact this package owns.
//
// # Why the grammar side is filtered by the keyword table's kinds
//
// A grammar arm's *token kind* is the join key, so a kind no keyword lexes to can never be
// looked up — and reading one into the map is not merely useless, it is wrong. The threads
// pin's `memory_fields` has `| LPAR DATA string_list RPAR /* Sugar */` whose action builds an
// `i32_const` offset, so the reader resolves kind `LPAR` to constructor `i32_const`: grave
// #107's shape (a nonterminal mistaken for a constructor) in its other direction, an arm whose
// *head* is not a mnemonic at all. Filtering against the keyword table's kinds is a derivation
// rather than an exclusion list — the domain is "kinds some keyword lexes to", which is the
// only domain the join has — and it drops exactly one entry, LPAR, over the two pins.
func ExtractFrom(auths []Authority, kws []Keyword, ops OpTable) (*Table, error) {
	if len(auths) == 0 {
		return nil, fmt.Errorf("%w: no authority to join", ErrVacuous)
	}

	// The kinds the join can key on, derived from the keyword table rather than listed. See
	// the header above for the one entry this drops and why it is wrong rather than idle.
	joinable := make(map[string]bool, len(kws))
	for _, kw := range kws {
		joinable[kw.Kind] = true
	}

	byGrammar := map[string]named{}
	byLexer := map[string]named{}
	// Where a row's `From` tag sends its contribution count back. Keyed by tag rather than by
	// index because the join loop below sees a row's *citation*, not the authority it walked —
	// and deriving the count from the citation is what makes the two agree by construction
	// instead of by a second bookkeeping pass that could disagree with the printed provenance.
	sourceOf := map[string]int{}
	tab := &Table{}
	for _, a := range auths {
		if a.ParserPath == "" || a.LexerPath == "" {
			return nil, fmt.Errorf("%w: an authority with no path: a row read from it would carry "+
				"a line number and no file, which is grave #529's half-citation", ErrVacuous)
		}
		g, err := grammarConstructors(a.Parser, ops)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.ParserPath, err)
		}
		l, err := lexerConstructors(a.Lexer, ops)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.LexerPath, err)
		}
		pTag, lTag := gen.SourceTag(a.ParserPath), gen.SourceTag(a.LexerPath)
		if pTag == lTag {
			return nil, fmt.Errorf("%w: %s and %s share the tag %q, so a row's citation cannot say "+
				"which of the two it was read from", ErrVacuous, a.ParserPath, a.LexerPath, pTag)
		}
		tab.Sources = append(tab.Sources, Source{
			ParserPath: a.ParserPath, LexerPath: a.LexerPath, SHA: a.SHA, Scope: a.Scope,
		})
		sourceOf[pTag] = len(tab.Sources) - 1
		sourceOf[lTag] = len(tab.Sources) - 1
		for kind, n := range g {
			if !joinable[kind] {
				continue
			}
			if _, ok := byGrammar[kind]; ok {
				continue
			}
			n.from = pTag
			byGrammar[kind] = n
		}
		for kw, n := range l {
			if _, ok := byLexer[kw]; ok {
				continue
			}
			n.from = lTag
			byLexer[kw] = n
		}
	}

	// The partition check, and it runs *before* the join rather than after: if a kind is
	// named by both authorities the join needs a precedence rule, and there is no ruling
	// establishing one. Erroring here refuses to invent it (0014's premise, asserted).
	//
	// **The two maps are keyed differently, and the first draft of this loop ignored that.**
	// byGrammar is per *kind* (a grammar arm covers every keyword of its kind); byLexer is
	// per *keyword* (a payload is one mnemonic's). `byLexer[kind]` therefore asked whether
	// some keyword is spelled `BINARY` — never true, uppercase — so the check could not fire
	// on any input and was decoration. It read as a partition check and was a no-op, which is
	// *a green that survives the bug it names*: found by writing the falsification test
	// (injecting a constructor into `| BINARY { fun c -> $1 }`) and watching it not fail.
	//
	// **Composition does not weaken it.** The premise is over the composed maps, so a kind the
	// core grammar names and the threads *lexer* also names would fire here even though
	// neither pin alone has an overlap — which is the new way this could break and the reason
	// the check is not scoped per authority.
	for _, kw := range kws {
		g, inGrammar := byGrammar[kw.Kind]
		l, inLexer := byLexer[kw.Keyword]
		if inGrammar && inLexer {
			return nil, fmt.Errorf("%w: keyword %q (kind %s) has a constructor from both authorities — "+
				"%q at %s:%d and %q at %s:%d; decision 0014's premise was overlap 0, "+
				"and the join has no precedence rule to apply",
				ErrPartition, kw.Keyword, kw.Kind, g.ctor, g.from, g.line, l.ctor, l.from, l.line)
		}
	}

	ambig := map[string]Ambiguity{}
	nGrammar, nLexer := 0, 0

	for _, kw := range kws {
		// Two lookups, in the order the partition permits: a kind whose grammar arm names
		// its constructor covers every keyword of that kind, while a payload constructor is
		// per-keyword. Overlap being 0 is what makes the order irrelevant to the result —
		// checked above, so this is not relying on it silently.
		ctor, op, origin, line, from := "", "", Origin(""), 0, ""
		if g, ok := byGrammar[kw.Kind]; ok {
			ctor, op, origin, line, from = g.ctor, g.op, FromGrammar, g.line, g.from
		} else if l, ok := byLexer[kw.Keyword]; ok {
			ctor, op, origin, line, from = l.ctor, l.op, FromLexer, l.line, l.from
		}
		if ctor == "" {
			tab.Unjoined++
			continue
		}

		codes := ops.CodesFor(ctor, op)
		if len(codes) == 0 {
			// **Reachable now, where it used to be unreachable, and the widening is the reason.**
			// Both readers filter their identifiers through OpTable.Holds, so the *constructor*
			// half cannot miss; the operator half can, because the two authorities spell the
			// tagged operator differently and this package reads the text one. A text arm whose
			// operator this reader failed to capture resolves to the pair (ctor, "") — which the
			// opcode table does not hold for an rmw constructor — so the miss is this error
			// rather than a row silently taking the family's first code.
			return nil, fmt.Errorf("%w: keyword %q resolved to constructor %q with operator %q "+
				"(%s:%d), which the opcode table does not hold; the join key is the pair, so an "+
				"operator this reader missed is a pair that names no encoding",
				ErrUnrecognized, kw.Keyword, ctor, op, from, line)
		}
		if len(codes) > 1 {
			ambig[ctor+"/"+op] = Ambiguity{Constructor: ctor, Operator: op, Keyword: kw.Keyword, Codes: codes}
		}

		// The row's own citation decides which Source it is charged to, so the printed
		// provenance and the counts cannot disagree.
		si, ok := sourceOf[from]
		if !ok {
			return nil, fmt.Errorf("%w: keyword %q cites %q, which is no authority in this join",
				ErrVacuous, kw.Keyword, from)
		}
		switch origin {
		case FromGrammar:
			nGrammar++
			tab.Sources[si].Grammar++
		case FromLexer:
			nLexer++
			tab.Sources[si].Lexer++
		}
		tab.Rows = append(tab.Rows, Row{
			Keyword: kw.Keyword, Kind: kw.Kind, Constructor: ctor, Operator: op,
			Prefix: codes[0].Prefix, Code: codes[0].Code,
			Origin: origin, Line: line, From: from,
		})
	}

	if nGrammar < Floors.Grammar {
		return nil, fmt.Errorf("%w: %d rows from parser.mly's grammar bodies, want >= %d",
			ErrVacuous, nGrammar, Floors.Grammar)
	}
	if nLexer < Floors.Lexer {
		return nil, fmt.Errorf("%w: %d rows from lexer.mll's token payloads, want >= %d",
			ErrVacuous, nLexer, Floors.Lexer)
	}
	if len(tab.Rows) < Floors.Total {
		return nil, fmt.Errorf("%w: %d joined rows, want >= %d", ErrVacuous, len(tab.Rows), Floors.Total)
	}

	// Every authority contributes, and this is the check the floors above cannot be: the threads
	// pin puts 67 rows into a table of 561, so a pin whose *both* files stopped being read would
	// leave all three floors satisfied and the composed table silently back to the core's content.
	//
	// **A total per authority, not a per-side floor**, and the line is drawn where the honest
	// claim is. "This pin contributes at least one row" is true of any pin worth composing — a
	// pin naming nothing is a pin the caller should not have passed. "This pin contributes at
	// least one *grammar* row" is not: a proposal adding only mnemonics of existing kinds would
	// contribute 0 grammar rows legitimately, and a check that fired on it would be teaching the
	// next author to route around the instrument. The per-side numbers are pinned instead where a
	// change is visible without being forbidden — Emit prints them per authority, so the drift
	// check sees `grammar 1` become `grammar 0` as a diff.
	for i, s := range tab.Sources {
		if s.Grammar+s.Lexer > 0 {
			continue
		}
		return nil, fmt.Errorf("%w: authority %d (%s + %s at %s) contributed no rows at all; "+
			"the aggregate floors cannot see this, since a pin can be a small fraction of the table",
			ErrVacuous, i, s.ParserPath, s.LexerPath, s.SHA)
	}

	sort.Slice(tab.Rows, func(i, j int) bool { return tab.Rows[i].Keyword < tab.Rows[j].Keyword })
	for _, a := range ambig {
		tab.Ambiguous = append(tab.Ambiguous, a)
	}
	sort.Slice(tab.Ambiguous, func(i, j int) bool {
		if tab.Ambiguous[i].Constructor != tab.Ambiguous[j].Constructor {
			return tab.Ambiguous[i].Constructor < tab.Ambiguous[j].Constructor
		}
		return tab.Ambiguous[i].Operator < tab.Ambiguous[j].Operator
	})
	return tab, nil
}

// named is a constructor and where it was read from.
//
// `from` is stamped by ExtractFrom rather than by the two readers, because a reader is handed
// one file's *contents* and has no idea which pin they came from — the file it is reading is the
// caller's fact, and a reader that guessed would be inventing the citation half of grave #529.
type named struct {
	ctor string
	op   string
	line int
	from string
}

// grammarConstructors reads *every* production in parser.mly and returns, per token kind,
// the constructor that kind's semantic actions name.
//
// Kinds whose actions name none are simply absent — that is the lexer's half, and the
// partition check is what makes "absent here" mean "present there" rather than "lost".
//
// # Why the whole grammar and not `plaininstr` (GRAVE #106)
//
// The first draft read `plaininstr` alone, on the reasoning that it is *the* instruction
// production, and 0014's premise ("overlap 0, gap 0") was measured against that scope and
// came out clean. It was clean about the wrong space. The reference builds instructions in
// **five** productions, because the ones taking a folded operand list cannot be plain arms:
//
//	plaininstr             53 kinds   the bulk
//	blockinstr              5         block / loop / if / try_table
//	callinstr_instr_list    4         call_indirect / return_call_indirect
//	selectinstr_instr_list  1         select
//	expr1                   9         the folded (sugar) spellings of all of the above
//
// So `select`, `block`, `loop`, `if`, `call_indirect`, `return_call_indirect` and
// `try_table` were named by neither authority and joined to nothing — seven instructions
// including every control-flow construct, silently absent from the encoder's table. **The
// premise held over the sample and was false over the space** (0006/#33), and the tell was
// that the gap looked empty when the gap test was scoped to the same production the reader
// was.
//
// Reading every production makes the scope the grammar rather than a list, so a sixth
// instruction-building production arriving upstream is covered without an edit here. The
// cost is that one kind can now be named in several productions — `select` at both
// `selectinstr_instr_list:678` and `expr1:815`, the folded form — and those must *agree*,
// which is asserted rather than assumed: a disagreement means the plain and folded
// spellings of one mnemonic would encode differently.
func grammarConstructors(mly string, ops OpTable) (map[string]named, error) {
	lines := strings.Split(mly, "\n")

	out := map[string]named{}
	kind, start := "", 0
	var body strings.Builder
	flush := func() error {
		if kind == "" {
			return nil
		}
		c := constructorIn(body.String(), ops)
		if c == "" {
			return nil
		}
		// The operator is read here too, though no grammar arm at either pin applies one — the
		// read-modify-write family names its operator in the *lexer's* payload. Derived rather
		// than assumed absent, because "the grammar never applies an operator" is a claim about
		// today's two authorities and reading it costs one regexp; assuming it would put a
		// silently-dropped discriminator in the one direction that decodes clean.
		op := operatorIn(body.String())
		// A kind named twice must be named the same way. The plain and folded spellings of
		// one instruction go through different productions, so this is the control on them
		// encoding identically — and it is a real risk rather than a defensive one, because
		// nothing upstream requires two arms of two productions to agree about anything.
		//
		// The comparison is on the *pair*, since the join key is the pair: two arms naming one
		// constructor and two operators encode two different instructions, which is exactly the
		// disagreement this check exists to refuse.
		if prev, ok := out[kind]; ok && (prev.ctor != c || prev.op != op) {
			return fmt.Errorf("%w: kind %s names constructor %q (operator %q) at parser.mly:%d and "+
				"%q (operator %q) at :%d; one mnemonic cannot encode two ways depending on which "+
				"production parsed it",
				ErrPartition, kind, prev.ctor, prev.op, prev.line, c, op, start)
		}
		if _, ok := out[kind]; !ok {
			out[kind] = named{ctor: c, op: op, line: start}
		}
		return nil
	}
	for i, l := range lines {
		if reProdHead.MatchString(l) {
			if err := flush(); err != nil {
				return nil, err
			}
			// A production head ends the previous production's last arm. The name is not
			// retained: every production is read, so there is nothing to compare it against.
			kind = ""
			continue
		}
		if m := reArmHead.FindStringSubmatch(l); m != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			kind, start = m[1], i+1
			body.Reset()
			// **The action only, never the symbol list** (GRAVE #107), and this is the
			// second defect the whole-grammar scope exposed, though it would have been wrong
			// at any scope. `| LOOP labeling_opt block END labeling_end_opt`
			// names the *nonterminal* `block`, which is also an instruction constructor the
			// opcode table holds — so scanning the whole arm resolved LOOP to `block` and
			// would have encoded `loop` as **0x02**. A wrong opcode for a well-formed
			// module: it decodes clean, so no vector can see it (§9 G-3), and it was found
			// by printing the reader's output for LOOP rather than by reading the code.
			// Scoped to the braces, a nonterminal in the symbol list cannot be mistaken for
			// a constructor in the action.
			if j := strings.Index(l, "{"); j >= 0 {
				body.WriteString(l[j:])
				body.WriteString("\n")
			}
			continue
		}
		if kind != "" {
			body.WriteString(l)
			body.WriteString("\n")
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// lexerConstructors reads the lexer's keyword block and returns, per keyword, the
// constructor its payload expression names.
//
// Keywords whose payload names none are absent, which is the grammar's half or no
// instruction at all (a type keyword, a script keyword).
//
// # GRAVE #105 lives here, and its regexps do not
//
// **The `->` ends an arm's head, and the token kind is deliberately not part of the match.**
// The first draft of this reader required `-> TOKEN` on one line, which is true of 564 arms
// and false of 25 — the `const` family and the `v128.*_lane`/`_splat` group wrap:
//
//	| "i32.const" ->
//	  CONST (fun s -> … i32_const (n @@ s.at), Value.I32 n)
//
// Those 25 did not match the head, so they were absorbed as *continuation lines of the
// preceding keyword* and vanished: 411 rows where 436 were measured, no error, no
// unrecognized arm. That is #78's under-matching trigger exactly — a trigger that
// under-matches produces **no finding rather than a wrong one** — and it was caught by
// printing what the reader returned for `i32.const` rather than by reading the regexp, which
// is the only way this class is ever caught. `keywordgen` had already met and solved it; the
// sibling defect was reintroduced here because the shape was re-derived instead of copied.
// Hence TestExtractMatchesMeasuredShape's exact counts: the floors did *not* catch this,
// because 411 clears a floor of 350.
//
// The block locating and the arm rejoining are **`mllex`'s** now, not this package's, on
// Scott's consolidation clause: three occurrences of one shape un-freezes tooling, so a
// fourth is structurally impossible rather than personally avoided. The prose that used to
// stand at the regexps said "keywordgen's are unexported, so the honest options are
// duplication or exporting a locator" — the ruling falsified it, and the third option was a
// package neither generator owns.
//
// What is still this package's is the part that is genuinely per-consumer: mining an arm's
// *body* for a lowercase identifier the opcode table holds. The token kind is included in
// that body, and that is safe because reIdent only matches lowercase-initial identifiers
// while every kind is SCREAMING_CASE — so the kind cannot become a constructor, and not
// having to *exclude* it is what lets the shared head regexp stop at `->` at all.
func lexerConstructors(lex string, ops OpTable) (map[string]named, error) {
	lines := strings.Split(lex, "\n")
	block, err := mllex.FindBlock(lines)
	if err != nil {
		// Wrapped as ErrVacuous so this package's error vocabulary is unchanged by the
		// consolidation; mllex.ErrNoBlock stays readable underneath.
		return nil, fmt.Errorf("%w: %w", ErrVacuous, err)
	}
	arms, err := mllex.Arms(lines, block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnrecognized, err)
	}

	out := map[string]named{}
	for _, a := range arms {
		if c := constructorIn(a.Body, ops); c != "" {
			out[a.Keyword] = named{ctor: c, op: operatorIn(a.Body), line: a.Line}
		}
	}
	return out, nil
}

// reOperatorArg matches the operator constructor an arm's action applies:
// `(i32_atomic_rmw (Values.I32 I32Op.RmwXor) (opt a 2)) o`
// (spec-threads/interpreter/text/lexer.mll:321).
//
// **Not opcodegen's `reOperatorArg`, and the difference is the qualifier.** That one requires
// `(I32 I32Op.RmwXor)` because the *decoder's* atomics region opens with `let open Values in`;
// the text lexer does not, so it spells the tag `Values.I32`. One pattern accepting both would
// have to make the qualifier optional on both sides, which loosens each reader to admit a
// spelling its own authority does not use — two files spelling one thing two ways is two facts,
// not one fact duplicated, so the *shape* is not shared even though the captured name is.
//
// The numeric-type names are spelled out rather than `\w+` for opcodegen's reason: what is being
// recognized is `Values`' tagged operator, and a looser pattern would claim any parenthesised
// qualified name, of which the reference has several that are not operators.
var reOperatorArg = regexp.MustCompile(`\(\s*Values\.(?:I32|I64|F32|F64)\s+(?:I32|I64|F32|F64)Op\.(\w+)\s*\)`)

// operatorIn returns the operator constructor an action applies, or "" where it applies none —
// which is every core arm, so this reads "" for 494 of the 561 composed rows.
func operatorIn(expr string) string {
	if m := reOperatorArg.FindStringSubmatch(expr); m != nil {
		return m[1]
	}
	return ""
}

// constructorIn returns the first lowercase identifier in an OCaml expression that the
// opcode table holds as a constructor, or "".
//
// **The opcode table is the filter, and that is what makes this sound without parsing
// OCaml.** An action like `fun c -> local_get ($2 c local)` contains `fun`, `c`,
// `local_get`, `local` — four identifiers, one of which is an instruction constructor, and
// the thing that knows which is the table generated from the reference's *decoder*. So the
// question this asks is not "which token is a constructor" (a parsing problem) but "which
// of these names does the authority hold" (a lookup), and a name the authority does not
// hold cannot become a row.
//
// The residual risk is a *collision*: an OCaml local variable sharing a name with an
// instruction constructor, earlier in the expression than the real one. Measured at
// bdd7164 — the join's 494 rows were cross-checked against the wat mnemonic spelling for
// every row where the two are comparable, and TestConstructorsAgreeWithMnemonicSpelling is
// that check standing. It is not a proof; it is the same posture as every extractor here,
// where the unrecognized-arm error and the floors bound what the reader can get wrong
// silently.
func constructorIn(expr string, ops OpTable) string {
	for _, id := range reIdent.FindAllString(expr, -1) {
		if ops.Holds(id) {
			return id
		}
	}
	return ""
}
