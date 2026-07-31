// Package spec is the .wast harness — the oracle (contract §9).
//
// Phase 1 scope (decision 0003): an s-expression reader, wast string-literal
// decoding, the `(module binary ...)` form, and `assert_malformed`. That is
// enough to run every test in binary.wast, which is 107 assert_malformed forms
// and nothing else. The wat text format is phase 2 (issue #8), timed to when
// the interpreter can answer assert_return.
//
// Deliberately no dependency on wabt or any non-Go tool: a non-Go binary in the
// conformance loop is reproducibility debt in the one place the project can
// least afford it.
package spec

import (
	"fmt"
	"strings"
)

// node is one s-expression datum: either a list, or an atom, or a string
// literal. String literals stay distinct from atoms because `"\00asm"` is a
// byte payload, not a symbol, and the difference is load-bearing for
// (module binary ...).
type node struct {
	list []node // non-nil for lists
	atom string // set for bare atoms (keywords, $names, numbers)
	str  []byte // set for string literals
	isS  bool   // true if this node is a string literal (str may be empty)
	line int    // 1-based source line, for diagnostics that name a test
}

func (n node) isList() bool { return n.list != nil }

// head returns the first atom of a list, or "" — the form's keyword.
func (n node) head() string {
	if len(n.list) > 0 && !n.list[0].isList() && !n.list[0].isS {
		return n.list[0].atom
	}
	return ""
}

type parser struct {
	src  []byte
	off  int
	line int
}

func newParser(src []byte) *parser { return &parser{src: src, line: 1} }

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("wast:%d: %s", p.line, fmt.Sprintf(format, args...))
}

// skipSpace consumes whitespace, ;; line comments, and (; block ;) comments.
// Block comments nest, per the wast lexical grammar.
func (p *parser) skipSpace() {
	for p.off < len(p.src) {
		c := p.src[p.off]
		switch {
		case c == '\n':
			p.line++
			p.off++
		case c == ' ' || c == '\t' || c == '\r':
			p.off++
		case c == ';' && p.off+1 < len(p.src) && p.src[p.off+1] == ';':
			for p.off < len(p.src) && p.src[p.off] != '\n' {
				p.off++
			}
		case c == '(' && p.off+1 < len(p.src) && p.src[p.off+1] == ';':
			depth := 0
			for p.off < len(p.src) {
				if p.src[p.off] == '(' && p.off+1 < len(p.src) && p.src[p.off+1] == ';' {
					depth++
					p.off += 2
					continue
				}
				if p.src[p.off] == ';' && p.off+1 < len(p.src) && p.src[p.off+1] == ')' {
					depth--
					p.off += 2
					if depth == 0 {
						break
					}
					continue
				}
				if p.src[p.off] == '\n' {
					p.line++
				}
				p.off++
			}
		default:
			return
		}
	}
}

// parseAll reads every top-level form in the source.
func (p *parser) parseAll() ([]node, error) {
	var out []node
	for {
		p.skipSpace()
		if p.off >= len(p.src) {
			return out, nil
		}
		n, err := p.parseNode()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
}

func (p *parser) parseNode() (node, error) {
	p.skipSpace()
	if p.off >= len(p.src) {
		return node{}, p.errf("unexpected end of input")
	}
	line := p.line
	switch c := p.src[p.off]; c {
	case '(':
		p.off++
		// A list is non-nil even when empty, which is how isList stays honest.
		items := []node{}
		for {
			p.skipSpace()
			if p.off >= len(p.src) {
				return node{}, p.errf("unclosed list")
			}
			if p.src[p.off] == ')' {
				p.off++
				return node{list: items, line: line}, nil
			}
			item, err := p.parseNode()
			if err != nil {
				return node{}, err
			}
			items = append(items, item)
		}
	case ')':
		return node{}, p.errf("unexpected )")
	case '"':
		b, err := p.parseString()
		if err != nil {
			return node{}, err
		}
		return node{str: b, isS: true, line: line}, nil
	case ';':
		// A lone ';' that skipSpace did not consume — it is neither ';;' nor
		// part of '(;'. Illegal in wast proper, but the annotations proposal
		// permits arbitrary token soup inside (@id ...), and annotations.wast
		// contains lines like `(@a , ; ] [ }} }x{ ({) ,{{};}] ;)`. Lex it as a
		// one-byte atom so the reader can traverse files it does not interpret;
		// parsing and understanding are separate concerns here.
		p.off++
		return node{atom: ";", line: line}, nil
	default:
		start := p.off
		for p.off < len(p.src) && !isDelim(p.src[p.off]) {
			p.off++
		}
		if p.off == start {
			return node{}, p.errf("unexpected byte %#x", c)
		}
		return node{atom: string(p.src[start:p.off]), line: line}, nil
	}
}

func isDelim(c byte) bool {
	return c == '(' || c == ')' || c == '"' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ';'
}

// parseString decodes a wast string literal into raw bytes.
//
// The escapes that matter for phase 1 are \hh hex pairs — the byte-string
// files use those and nothing else (verified: 15627 hex escapes, zero others).
// The rest of the wast escape set is implemented anyway because phase 2 needs
// it and getting it wrong later would be a silent corruption rather than a
// parse error.
func (p *parser) parseString() ([]byte, error) {
	if p.src[p.off] != '"' {
		return nil, p.errf("expected string")
	}
	p.off++
	// Non-nil, deliberately: `(module binary "")` is a real suite vector and the
	// empty image is the "unexpected end" boundary case. isS already carries
	// "this is a string", so str must not double as the presence flag — a reader
	// reaching for `str != nil` would get the wrong answer for exactly the one
	// input that matters most. Found by FuzzWastLexer on its first run.
	out := []byte{}
	for {
		if p.off >= len(p.src) {
			return nil, p.errf("unterminated string")
		}
		c := p.src[p.off]
		switch c {
		case '"':
			p.off++
			return out, nil
		case '\n':
			return nil, p.errf("newline in string literal")
		case '\\':
			p.off++
			if p.off >= len(p.src) {
				return nil, p.errf("unterminated escape")
			}
			e := p.src[p.off]
			switch e {
			case 't':
				out = append(out, '\t')
				p.off++
			case 'n':
				out = append(out, '\n')
				p.off++
			case 'r':
				out = append(out, '\r')
				p.off++
			case '"':
				out = append(out, '"')
				p.off++
			case '\'':
				out = append(out, '\'')
				p.off++
			case '\\':
				out = append(out, '\\')
				p.off++
			case 'u':
				// \u{XXXX} — a Unicode scalar, encoded UTF-8.
				p.off++
				if p.off >= len(p.src) || p.src[p.off] != '{' {
					return nil, p.errf(`expected { after \u`)
				}
				p.off++
				var v rune
				digits := 0
				for p.off < len(p.src) && p.src[p.off] != '}' {
					d, ok := hexVal(p.src[p.off])
					if !ok {
						return nil, p.errf("bad hex digit in \\u{}")
					}
					v = v<<4 | rune(d)
					digits++
					p.off++
				}
				if digits == 0 || p.off >= len(p.src) {
					return nil, p.errf("malformed \\u{} escape")
				}
				p.off++ // consume }
				out = append(out, []byte(string(v))...)
			default:
				// \hh — the workhorse. Two hex digits, one raw byte. Note this
				// is a *byte*, not a rune: "\ff" is one invalid-UTF-8 byte, which
				// is exactly what the utf8-*.wast tests need it to be.
				hi, ok1 := hexVal(e)
				if !ok1 || p.off+1 >= len(p.src) {
					return nil, p.errf("bad escape \\%c", e)
				}
				lo, ok2 := hexVal(p.src[p.off+1])
				if !ok2 {
					return nil, p.errf("bad hex escape \\%c%c", e, p.src[p.off+1])
				}
				out = append(out, hi<<4|lo)
				p.off += 2
			}
		default:
			out = append(out, c)
			p.off++
		}
	}
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// String renders a node back to something readable, for failure messages.
func (n node) String() string {
	switch {
	case n.isS:
		return fmt.Sprintf("%q", n.str)
	case n.isList():
		parts := make([]string, len(n.list))
		for i, c := range n.list {
			parts[i] = c.String()
		}
		return "(" + strings.Join(parts, " ") + ")"
	default:
		return n.atom
	}
}
