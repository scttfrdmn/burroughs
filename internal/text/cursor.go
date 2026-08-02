package text

import "fmt"

// cursor is the parser's token stream: a fully-lexed slice with a position, and the
// small set of questions the productions ask of it.
//
// **Fully lexed up front, not streamed.** The reference threads a `lexbuf` through menhir
// and consumes on demand, which is what an LR parser needs; this is a recursive-descent
// parser over a grammar with one-token lookahead, and a slice makes that lookahead free.
// It also puts every *lex* error before every *parse* error, which is the reference's
// order too — menhir pulls a token before it can reduce, so a malformed lexeme is
// reported wherever it sits regardless of the grammar's state. Matching that ordering is
// not an optimization; it is what keeps the error a vector expects the error it gets.
//
// The cost is holding the whole token slice. For the suite's largest file that is bounded
// and measured (`LexAll` already runs over all 257 at every board run), and if a
// pathological input ever makes it matter, the fix is a windowed cursor behind this same
// interface — the productions never see the representation.
type cursor struct {
	toks []Token
	pos  int
}

// newCursor lexes src and returns a cursor over its tokens, EOF included.
//
// It calls lexToEOF, not LexAll: the sentinel is what makes peek() safe. The first draft
// called LexAll and asserted in a comment that LexAll appends EOF. It does not — it stops at
// EOF and drops it — and TestCursorPeekAtEOFIsStable panicked on its first run. lexToEOF now
// exists so the token this file depends on comes from the function whose name promises it.
//
// A lex error is returned as-is, unwrapped: the suite's expected strings for malformed
// lexemes are the lexer's messages (`malformed UTF-8 encoding`, `unknown operator`), and
// wrapping would put parser prose in front of a string the oracle matches by substring —
// harmless for `Contains`, but it makes the error testify to a layer that did not produce
// it. *An error from the wrong layer is evidence about where structure was lost*, and
// there is no structure lost here: the lexer's verdict is final and correct.
func newCursor(src []byte) (*cursor, error) {
	toks, err := lexToEOF(src)
	if err != nil {
		return nil, err
	}
	return &cursor{toks: toks}, nil
}

// peek returns the current token without consuming it.
//
// lexToEOF's slice always ends with EOF and next() never advances past it, so this is always
// in range and needs no bounds branch — asserted by TestCursorPeekAtEOFIsStable rather than
// assumed, because it is a fact about another function that a refactor can quietly end. It
// already did once: see newCursor.
func (c *cursor) peek() Token { return c.toks[c.pos] }

// next consumes and returns the current token. At EOF it returns EOF without advancing,
// so a production that loops on a token it does not recognize cannot run off the end —
// it spins instead, which is the *visible* failure. Progress is asserted, not assumed:
// every production that loops proves the cursor moved (grave #18).
func (c *cursor) next() Token {
	t := c.toks[c.pos]
	if t.Kind != EOF {
		c.pos++
	}
	return t
}

// at reports whether the current token has the given kind.
func (c *cursor) at(k TokenKind) bool { return c.toks[c.pos].Kind == k }

// peek2 returns the token after the current one, or EOF at the end.
//
// The grammar needs exactly two tokens of lookahead and no more: every parenthesized form is
// distinguished by its keyword, so `(mut i32)` versus `(param i32)` versus `(ref null any)` is a
// decision about `c.toks[c.pos+1]`. This is the whole reason the cursor holds a slice rather than
// wrapping the lexer — and the bound is worth stating, because "just one more token" is how a
// recursive-descent parser turns into a backtracking one.
//
// Unlike peek, this needs the bounds check: EOF is the last element, so pos+1 can be past it.
func (c *cursor) peek2() Token {
	if c.pos+1 >= len(c.toks) {
		return c.toks[len(c.toks)-1] // the EOF token
	}
	return c.toks[c.pos+1]
}

// peek2Keyword reports whether the token after the current one is the given keyword.
func (c *cursor) peek2Keyword(k keywordKind) bool {
	t := c.peek2()
	return t.Kind == KeywordTok && t.Keyword == k
}

// atKeyword reports whether the current token is the given keyword.
//
// Compares against keywordKind, the *reference's* token vocabulary, because the generated
// table already speaks it (keywords.go) and a second vocabulary here would be a second
// declaration of the same set — the thing decision 0009 exists to avoid. So a production
// matching `(func …)` asks for "FUNC", the name lexer.mll gives it.
func (c *cursor) atKeyword(k keywordKind) bool {
	t := c.toks[c.pos]
	return t.Kind == KeywordTok && t.Keyword == k
}

// There are no offset() and line() accessors here, and their absence is a finding rather than an
// omission. The first draft had both, for error positions; `unused` reported them, because every
// error site takes a *token* and reads the position off that — errAt and errf below, and
// bodyBoundary. Keeping them would have been the unreachable-error shape (grave 0003) wearing an
// accessor: code that exists for a caller nobody wrote, in a package whose whole error convention
// says positions travel with the token that carries them.

// errAt builds a parse error at an explicit token.
//
// Reusing text.Error rather than a parser-specific type: the suite matches by substring
// and does not care which layer spoke, but a reader of the code does — and the layer is
// already recoverable from the message, which is the reference's verbatim text. A second
// error type would buy a type switch nobody needs and a second place for the Msg
// convention to drift.
func errAt(t Token, msg string) error {
	return &Error{Msg: msg, Offset: t.Offset, Line: t.Line}
}

// errf is errAt with formatting, for the messages that carry a value.
//
// Note what this does *not* do: reconstruct a byte from a decoded value. Grave #36 was an
// error message printing `0xde` for a `0x5e` the image never held — the right verdict
// quoting fabricated evidence, and green on every board by construction because the
// suite's expected string stopped at the sentinel. Every message here that names something
// from the input names the *token text as written*, which the lexer preserved for exactly
// this reason.
func errf(t Token, format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...), Offset: t.Offset, Line: t.Line}
}
