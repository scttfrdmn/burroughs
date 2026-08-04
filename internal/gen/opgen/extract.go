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

	// Constructor is the reference's own constructor name, and it is the join key:
	// `i32_add`, `local_get`. Kept in the emitted table because it is the *evidence* for
	// the row — a reader auditing why `i32.add` is 0x6a follows this name into
	// optable.go, which cites its own decode.ml line.
	Constructor string

	// Prefix and Code are the encoding, copied from the opcode table's matching row.
	Prefix byte
	Code   uint32

	// Origin says which authority named Constructor.
	Origin Origin

	// Line is the 1-indexed line in that authority this row's constructor was read from.
	Line int
}

// Ambiguity is a constructor the opcode table holds under more than one encoding.
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
	Keyword     string
	Codes       []Code
}

// Code is one (prefix, opcode) pair.
type Code struct {
	Prefix byte
	Code   uint32
}

// Table is one extraction's result: the rows, the ambiguities, and the provenance needed to
// audit them.
type Table struct {
	// SourceSHA is the reference revision both authorities were read from. Stamped, not
	// deduced (0007, condition 3). One SHA rather than two, because the join is only
	// meaningful if both sides are the same revision — see the cmd, which reads it from
	// the fetch script's pin.
	SourceSHA string

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
var Floors = struct {
	Grammar int
	Lexer   int
	Total   int
}{
	Grammar: 40,  // measured 58
	Lexer:   350, // measured 436
	Total:   400, // measured 494
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
	// CodesFor returns every (prefix, opcode) the reference's named constructor encodes
	// to, or nil. More than one is an Ambiguity, never a silent first-wins.
	CodesFor(constructor string) []Code
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

// Extract joins the two authorities and returns the mnemonic→opcode table.
//
// mly and lex are the reference's parser and lexer sources; kws is the keyword table
// (keywordgen's arms); ops is the opcode table. sha is recorded verbatim — the caller is
// responsible for it being the revision all of these were read from.
func Extract(mly, lex string, kws []Keyword, ops OpTable, sha string) (*Table, error) {
	byGrammar, err := grammarConstructors(mly, ops)
	if err != nil {
		return nil, err
	}
	byLexer, err := lexerConstructors(lex, ops)
	if err != nil {
		return nil, err
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
	for _, kw := range kws {
		g, inGrammar := byGrammar[kw.Kind]
		l, inLexer := byLexer[kw.Keyword]
		if inGrammar && inLexer {
			return nil, fmt.Errorf("%w: keyword %q (kind %s) has a constructor from both authorities — "+
				"%q at parser.mly:%d and %q at lexer.mll:%d; decision 0014's premise was overlap 0, "+
				"and the join has no precedence rule to apply",
				ErrPartition, kw.Keyword, kw.Kind, g.ctor, g.line, l.ctor, l.line)
		}
	}

	tab := &Table{SourceSHA: sha}
	ambig := map[string]Ambiguity{}
	nGrammar, nLexer := 0, 0

	for _, kw := range kws {
		// Two lookups, in the order the partition permits: a kind whose grammar arm names
		// its constructor covers every keyword of that kind, while a payload constructor is
		// per-keyword. Overlap being 0 is what makes the order irrelevant to the result —
		// checked above, so this is not relying on it silently.
		ctor, origin, line := "", Origin(""), 0
		if g, ok := byGrammar[kw.Kind]; ok {
			ctor, origin, line = g.ctor, FromGrammar, g.line
		} else if l, ok := byLexer[kw.Keyword]; ok {
			ctor, origin, line = l.ctor, FromLexer, l.line
		}
		if ctor == "" {
			tab.Unjoined++
			continue
		}

		codes := ops.CodesFor(ctor)
		if len(codes) == 0 {
			// Unreachable through the constructors these two readers return, since both
			// filter on the opcode table. Asserted anyway: the filter is what makes a
			// lowercase identifier a *constructor* rather than a local variable name, and
			// if that filter ever moves, this is the difference between a wrong row and an
			// error.
			return nil, fmt.Errorf("%w: keyword %q resolved to constructor %q, which the opcode table does not hold",
				ErrUnrecognized, kw.Keyword, ctor)
		}
		if len(codes) > 1 {
			ambig[ctor] = Ambiguity{Constructor: ctor, Keyword: kw.Keyword, Codes: codes}
		}

		switch origin {
		case FromGrammar:
			nGrammar++
		case FromLexer:
			nLexer++
		}
		tab.Rows = append(tab.Rows, Row{
			Keyword: kw.Keyword, Kind: kw.Kind, Constructor: ctor,
			Prefix: codes[0].Prefix, Code: codes[0].Code,
			Origin: origin, Line: line,
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

	sort.Slice(tab.Rows, func(i, j int) bool { return tab.Rows[i].Keyword < tab.Rows[j].Keyword })
	for _, a := range ambig {
		tab.Ambiguous = append(tab.Ambiguous, a)
	}
	sort.Slice(tab.Ambiguous, func(i, j int) bool {
		return tab.Ambiguous[i].Constructor < tab.Ambiguous[j].Constructor
	})
	return tab, nil
}

// named is a constructor and where it was read from.
type named struct {
	ctor string
	line int
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
		// A kind named twice must be named the same way. The plain and folded spellings of
		// one instruction go through different productions, so this is the control on them
		// encoding identically — and it is a real risk rather than a defensive one, because
		// nothing upstream requires two arms of two productions to agree about anything.
		if prev, ok := out[kind]; ok && prev.ctor != c {
			return fmt.Errorf("%w: kind %s names constructor %q at parser.mly:%d and %q at :%d; "+
				"one mnemonic cannot encode two ways depending on which production parsed it",
				ErrPartition, kind, prev.ctor, prev.line, c, start)
		}
		if _, ok := out[kind]; !ok {
			out[kind] = named{ctor: c, line: start}
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
			out[a.Keyword] = named{ctor: c, line: a.Line}
		}
	}
	return out, nil
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
		if len(ops.CodesFor(id)) > 0 {
			return id
		}
	}
	return ""
}
