package text

import "fmt"

// The wat lexer, reject-direction first (#53).
//
// This is the consumer keywords.go was committed ahead of (decision 0009): the table
// is the authority for which mnemonics *are* tokens, and absence from it is the whole
// reject-direction contract — the reference states it in one line, the keyword block's
// `| _ -> unknown lexbuf` (lexer.mll:809).
//
// # Why the arm order and the longest match are modelled rather than approximated
//
// ocamllex resolves an ambiguous position by two rules, in this order: **longest match
// wins**, and among equal-length matches **the earliest arm wins**. Both are
// load-bearing here, and neither survives being approximated by a hand-ordered switch:
//
//   - `i32.wrap/i64` — `/` is *not* in the `keyword` production (lexer.mll:112 admits
//     only letter, digit, `_`, `.`, `:`), but it *is* an `idchar`, so `keyword` matches
//     `i32.wrap` (8) while `reserved` matches the whole `i32.wrap/i64` (12). Longest
//     wins, so the error names the whole lexeme. The suite reads that text back —
//     `obsolete-keywords.wast:67` expects exactly `unknown operator i32.wrap/i64` — so
//     a reader that stopped at `i32.wrap` would produce a plausible, wrong message and
//     fail a vector that can see it.
//   - `offset=0` — `keyword` matches `offset` (6), the `"offset="nat` arm matches 8, and
//     `reserved` also matches 8. Longest narrows it to two; *earliest arm* picks
//     `OFFSET_EQ_NAT` over `unknown operator`. Tie-breaking by position in the reference
//     is the only thing that gets this right.
//   - `get_local` — `keyword` and `reserved` both match all 9. Earliest arm sends it
//     through the keyword block, whose fallthrough produces the error. Same text either
//     way here, which is exactly why it cannot be used to *check* the rule.
//
// So `arms` below is a transcription of `rule token` in source order, and `next` picks
// by (length, index). The duplication of the reference's ordering is the point: this is
// a transcription of an authority, and reordering it silently is the defect the
// `i32.wrap/i64` vector exists to catch.
//
// # What this file deliberately does not do
//
// It lexes. It does not parse, resolve names, or decode a name's UTF-8 — those are
// `parser.mly`'s layer, and the 186 `malformed UTF-8 encoding` vectors live there
// (`parser.mly:47`/`:52`), not here: `(func (export "\80"))` is all-ASCII source whose
// string token lexes cleanly. Measured before writing this file rather than discovered
// after, and recorded on #53.

// TokenKind is the token's shape. Keyword tokens all share KeywordTok and carry their
// kind from the generated table, because the table is where that vocabulary is
// authoritative — re-declaring it as an enum here would be the second declaration
// keywords.go exists to avoid.
type TokenKind int

const (
	LParen TokenKind = iota
	RParen
	NatTok
	IntTok
	FloatTok
	StringTok
	VarTok
	KeywordTok
	OffsetEqNat
	AlignEqNat
	EOF
)

func (k TokenKind) String() string {
	switch k {
	case LParen:
		return "("
	case RParen:
		return ")"
	case NatTok:
		return "nat"
	case IntTok:
		return "int"
	case FloatTok:
		return "float"
	case StringTok:
		return "string"
	case VarTok:
		return "var"
	case KeywordTok:
		return "keyword"
	case OffsetEqNat:
		return "offset="
	case AlignEqNat:
		return "align="
	default:
		return "eof"
	}
}

// Token is one lexeme. Text is the source slice as written; Value is the decoded bytes
// for the forms that have a decoding (strings and `$"..."` identifiers), and nil
// otherwise.
//
// Value is non-nil-for-empty in the same way the wast reader's string node is: emptiness
// must be a length, never a nil, or a caller checking `Value != nil` misreads the empty
// string. That distinction was a fuzz finding in the sibling reader and is not going to
// be re-earned here.
type Token struct {
	Kind    TokenKind
	Keyword keywordKind
	Text    string
	Value   []byte
	Offset  int
	Line    int
}

// Error is a lex error. Msg is the reference's message text verbatim, because the suite
// matches by substring and eleven vectors read the *lexeme* back out of it — for those,
// message rendering is oracle-covered (#38's refinement), which is rare and worth
// naming at the site that produces it.
type Error struct {
	Msg    string
	Offset int
	Line   int
}

func (e *Error) Error() string { return e.Msg }

// Lexer scans wat source. Zero value is not usable; call NewLexer.
type Lexer struct {
	src  []byte
	pos  int
	line int
}

func NewLexer(src []byte) *Lexer { return &Lexer{src: src, line: 1} }

// Offset reports the scan position, which is what the progress-property fuzz target
// asserts moves. *Parsers prove progress, they don't assume it* (grave #18): a loop
// whose exit condition and error condition are the same predicate hangs on inputs where
// the offending byte is not a delimiter, and the only defence is an assertion that the
// offset advanced.
func (l *Lexer) Offset() int { return l.pos }

// arm is one production in `rule token`, in the reference's source order. length reports
// the match length at pos, or -1 for no match; emit turns a matched span into a token,
// an error, or a skip (nil token, nil error).
type arm struct {
	name   string
	length func(l *Lexer, s []byte) int
	emit   func(l *Lexer, s []byte) (*Token, error)
}

// Next returns the next token. At end of input it returns an EOF token repeatedly.
func (l *Lexer) Next() (Token, error) {
	for {
		if l.pos >= len(l.src) {
			return Token{Kind: EOF, Offset: l.pos, Line: l.line}, nil
		}
		rest := l.src[l.pos:]

		// Longest match, earliest arm. Both halves are the reference's disambiguation
		// and both are exercised by vectors; see this file's header.
		best, bestArm := -1, -1
		for i := range arms {
			if n := arms[i].length(l, rest); n > best {
				best, bestArm = n, i
			}
		}
		if best <= 0 {
			// No arm matched a non-empty span. Unreachable: the final arm matches any
			// single byte. Panicking rather than returning keeps it from becoming a
			// silent zero-progress loop, which is the shape grave #18 named.
			panic(fmt.Sprintf("text: no arm matched at offset %d (byte %#02x); the "+
				"catch-all arm is missing and this would be a zero-progress loop", l.pos, rest[0]))
		}
		if best > len(rest) {
			// An arm reporting more than it read. Checked here rather than left to the
			// slice below, because the runtime's `slice bounds out of range [:10] with
			// capacity 5` names the *symptom* at Next's line and says nothing about which
			// of two dozen arms lied — and a fuzzer's report is only as useful as the
			// message it captures. Every `match*` function is a separate opportunity for
			// this, and `scanBlockComment`/`scanAnnotBody` compute lengths over nested
			// structure, so the arm's name is the whole diagnosis.
			//
			// Found by falsifying FuzzLexerProgress: the mutant that reports `len(b)+5`
			// panicked *inside* this function, before the harness's own bounds assertion
			// could see it. So the harness's check was unreachable and its diagnosis was
			// the runtime's — which is the wrong layer holding the only witness.
			panic(fmt.Sprintf("text: arm %q reported length %d at offset %d with only %d "+
				"bytes remaining", arms[bestArm].name, best, l.pos, len(rest)))
		}

		span := rest[:best]
		start, startLine := l.pos, l.line
		l.pos += best
		l.line += countNewlines(span)

		tok, err := arms[bestArm].emit(l, span)
		if err != nil {
			return Token{}, err
		}
		if tok == nil {
			continue // whitespace, comment, annotation: no token, but progress was made
		}
		tok.Offset, tok.Line = start, startLine
		return *tok, nil
	}
}

// LexAll runs to EOF, returning the tokens or the first error.
func LexAll(src []byte) ([]Token, error) {
	l := NewLexer(src)
	var out []Token
	for {
		t, err := l.Next()
		if err != nil {
			return out, err
		}
		if t.Kind == EOF {
			return out, nil
		}
		out = append(out, t)
	}
}

func (l *Lexer) errAt(off int, msg string) error {
	return &Error{Msg: msg, Offset: off, Line: l.line}
}

func lit(s string) func(*Lexer, []byte) int {
	return func(_ *Lexer, b []byte) int {
		if len(b) >= len(s) && string(b[:len(s)]) == s {
			return len(s)
		}
		return -1
	}
}

func tokenOf(k TokenKind) func(*Lexer, []byte) (*Token, error) {
	return func(_ *Lexer, s []byte) (*Token, error) {
		return &Token{Kind: k, Text: string(s)}, nil
	}
}

func skip(_ *Lexer, _ []byte) (*Token, error) { return nil, nil }

// arms transcribes `rule token` (lexer.mll:128–842) in source order. Order is semantic:
// see the header. Adding an arm anywhere but its reference position is a defect even
// when the resulting token stream happens to agree.
var arms = []arm{
	{"lpar", lit("("), tokenOf(LParen)},
	{"rpar", lit(")"), tokenOf(RParen)},

	{"nat", func(_ *Lexer, b []byte) int { return matchNat(b) }, tokenOf(NatTok)},
	{"int", func(_ *Lexer, b []byte) int { return matchInt(b) }, tokenOf(IntTok)},
	{"float", func(_ *Lexer, b []byte) int { return matchFloat(b) }, tokenOf(FloatTok)},

	{"string", func(_ *Lexer, b []byte) int { return matchString(b) }, emitString},
	{
		"unclosed string", func(_ *Lexer, b []byte) int { return matchUnclosedString(b) },
		func(l *Lexer, s []byte) (*Token, error) {
			return nil, l.errAt(l.pos-len(s), "unclosed string literal")
		},
	},
	{
		"control in string", func(_ *Lexer, b []byte) int { return matchStringWithControl(b) },
		func(l *Lexer, s []byte) (*Token, error) {
			return nil, l.errAt(l.pos-len(s), "illegal control character in string literal")
		},
	},
	{
		"illegal escape", func(_ *Lexer, b []byte) int { return matchStringBadEscape(b) },
		func(l *Lexer, s []byte) (*Token, error) {
			return nil, l.errAt(l.pos-len(s), "illegal escape")
		},
	},

	{"keyword", func(_ *Lexer, b []byte) int { return matchKeyword(b) }, emitKeyword},

	{"offset=", func(_ *Lexer, b []byte) int { return matchEqNat(b, "offset=") }, tokenOf(OffsetEqNat)},
	{"align=", func(_ *Lexer, b []byte) int { return matchEqNat(b, "align=") }, tokenOf(AlignEqNat)},

	{
		"$id", func(_ *Lexer, b []byte) int { return matchVarID(b) },
		func(_ *Lexer, s []byte) (*Token, error) {
			return &Token{Kind: VarTok, Text: string(s), Value: append([]byte{}, s[1:]...)}, nil
		},
	},
	{"$string", func(_ *Lexer, b []byte) int { return matchVarString(b) }, emitVarString},
	{"$", lit("$"), func(l *Lexer, s []byte) (*Token, error) {
		return nil, l.errAt(l.pos-len(s), "empty identifier")
	}},

	{"(@id", func(_ *Lexer, b []byte) int { return matchAnnotStart(b, false) }, emitAnnot},
	{"(@string", func(_ *Lexer, b []byte) int { return matchAnnotStart(b, true) }, emitAnnot},
	{"(@", lit("(@"), func(l *Lexer, s []byte) (*Token, error) {
		return nil, l.errAt(l.pos-len(s), "empty annotation id")
	}},

	{"line comment", func(_ *Lexer, b []byte) int { return matchLineComment(b) }, skip},
	{"block comment", func(_ *Lexer, b []byte) int { return matchBlockComment(b) }, emitBlockComment},

	{"space", func(_ *Lexer, b []byte) int {
		if len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
			return 1
		}
		return -1
	}, skip},
	{"newline", func(_ *Lexer, b []byte) int { return matchNewline(b) }, skip},

	// `reserved` is the *second* of the two `unknown operator` producers (lexer.mll:839).
	// The third `reserved` arm in the reference (:882) is in the `annot` scanner and
	// produces an atom, not an error — treating all three alike would reject annotations
	// the spec accepts, which is an accept-direction defect no vector can see (§9 G-3).
	// Found by re-measuring the producer count rather than carrying "two" forward.
	{
		"reserved", func(_ *Lexer, b []byte) int { return matchReserved(b) },
		func(l *Lexer, s []byte) (*Token, error) {
			return nil, l.errAt(l.pos-len(s), "unknown operator "+string(s))
		},
	},
	{"control", func(_ *Lexer, b []byte) int {
		if len(b) > 0 && isControl(b[0]) {
			return 1
		}
		return -1
	}, func(l *Lexer, s []byte) (*Token, error) {
		return nil, l.errAt(l.pos-len(s), "misplaced control character")
	}},
	{
		"utf8enc", func(_ *Lexer, b []byte) int { return matchUTF8Enc(b) },
		func(l *Lexer, s []byte) (*Token, error) {
			return nil, l.errAt(l.pos-len(s), "misplaced unicode character")
		},
	},
	// The catch-all, which is why `best <= 0` above is unreachable.
	{"any", func(_ *Lexer, b []byte) int {
		if len(b) > 0 {
			return 1
		}
		return -1
	}, func(l *Lexer, s []byte) (*Token, error) {
		return nil, l.errAt(l.pos-len(s), "malformed UTF-8 encoding")
	}},
}

func emitString(_ *Lexer, s []byte) (*Token, error) {
	v, ok := decodeString(s)
	if !ok {
		// Unreachable: matchString only matches spans decodeString accepts. Kept as an
		// error rather than a silent empty value so the two cannot drift apart quietly.
		return nil, &Error{Msg: "malformed string literal"}
	}
	return &Token{Kind: StringTok, Text: string(s), Value: v}, nil
}

func emitVarString(l *Lexer, s []byte) (*Token, error) {
	v, ok := decodeString(s[1:])
	if !ok {
		return nil, &Error{Msg: "malformed string literal"}
	}
	// Emptiness is checked here and UTF-8 is *not*, and the asymmetry is the
	// authority's (lexer.mll:816–818):
	//
	//	| '$'(string as s)
	//	  { let s' = string s in
	//	    if s' = "" then error lexbuf "empty identifier"; VAR s' }
	//
	// A `VAR` token's UTF-8 is decoded by `var` at parser.mly:49–52, one layer up. So
	// `(func $"\ef")` — id.wast:31 — is a *parser*-layer rejection even though the lexer
	// is what decodes the escapes, and adding the check here was measured doing exactly
	// what the discipline forbids: the probe said "accepted but should reject", the check
	// made it reject, and the board would have read 630 with one vector answered from the
	// wrong stratum. Bought pass count, wrong in general, invisible by construction
	// (§9 G-3). Removed on that finding; see TestUTF8RejectionBelongsToExactlyOneForm.
	//
	// `(@"...")` is genuinely different and genuinely ours: `annot_id` (lexer.mll:51–54)
	// decodes *and* rejects, inside the lexer. Two forms that look identical on the board
	// living in different strata is the whole reason the partition had to be measured.
	if len(v) == 0 {
		return nil, l.errAt(l.pos-len(s), "empty identifier")
	}
	return &Token{Kind: VarTok, Text: string(s), Value: v}, nil
}

// emitKeyword is the keyword block's arm dispatch, and its fallthrough is the *first*
// `unknown operator` producer (lexer.mll:809). The table is consulted rather than a
// hand-written set: a keyword absent from it is not a token, which is the entire
// reject-direction contract and the reason the table was committed one increment early.
func emitKeyword(l *Lexer, s []byte) (*Token, error) {
	k, ok := keywords[string(s)]
	if !ok {
		return nil, l.errAt(l.pos-len(s), "unknown operator "+string(s))
	}
	return &Token{Kind: KeywordTok, Keyword: k, Text: string(s)}, nil
}

// emitBlockComment rejects an unterminated or ill-formed `(;`. The verdict comes from
// scanBlockComment, which is the reference's `comment` scanner — both of its error arms
// (`eof` → unclosed, `_` → malformed UTF-8) are live, and the span is re-scanned rather
// than re-derived from the bytes at the end of it, which is the grave documented there.
func emitBlockComment(l *Lexer, s []byte) (*Token, error) {
	if _, msg := scanBlockComment(s); msg != "" {
		return nil, l.errAt(l.pos-len(s), msg)
	}
	return nil, nil
}

// emitAnnot consumes the annotation body with the reference's `annot` scanner, which is
// a *different* rule from `token` — notably its `reserved` arm produces an atom rather
// than an error. Annotations record and produce no token.
func emitAnnot(l *Lexer, s []byte) (*Token, error) {
	// The string form's id goes through `annot_id` (lexer.mll:51–54), which decodes it
	// and rejects an *empty* result — `(@"")` is `empty annotation id`, not an
	// annotation named "". Matching the string shape is not the same as validating the
	// id, and conflating them accepted one vector the spec calls malformed.
	if len(s) > 2 && s[2] == '"' {
		v, ok := decodeString(s[2:])
		if !ok {
			return nil, l.errAt(l.pos-len(s), "malformed string literal")
		}
		if len(v) == 0 {
			return nil, l.errAt(l.pos-len(s), "empty annotation id")
		}
		if !validUTF8(v) {
			// `(@"\ef")` — annotations.wast:79, expecting "malformed UTF-8".
			return nil, l.errAt(l.pos-len(s), "malformed UTF-8 encoding")
		}
	}
	// Scan the body from the current position, which is just past "(@id".
	n, err := scanAnnotBody(l.src[l.pos:])
	if err != "" {
		return nil, l.errAt(l.pos, err)
	}
	body := l.src[l.pos : l.pos+n]
	l.pos += n
	l.line += countNewlines(body)
	return nil, nil
}

// countNewlines counts *newline matches* in a span, which is not the same as counting
// `'\n'` bytes — and the difference is a defect this function shipped with. The reference
// calls `Lexing.new_line` once per `newline` arm match (lexer.mll:836 and the same arm in
// `comment` and `annot`), and `newline = ascii_newline | "\x0a\x0d"` admits a bare CR. So a
// classic-Mac file, or a `\r`-terminated line comment, advanced no lines at all, and
// `\r\n` — two matches — counted as one.
//
// It passed every existing test because `Line` is read by nothing yet. That is *a test
// asserting a property of code that does not run yet*: the field was correct on the inputs
// anyone looked at and wrong for two of the three newline forms, with no control able to
// see it until TestNewlineOrderIsLFCR asked. The `matchNewline` reuse is the point — one
// definition of what a newline is, used by both the arm and the counter, so they cannot
// disagree about the LF-CR ordering.
func countNewlines(b []byte) int {
	n := 0
	for i := 0; i < len(b); {
		if m := matchNewline(b[i:]); m > 0 {
			n++
			i += m
			continue
		}
		i++
	}
	return n
}
