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

	// start and end are the node's byte extent in the source: start at its first byte,
	// end one past its last. For a list that is the `(` and the byte after the `)`.
	//
	// **This is what makes a bare `(module <wat body>)` askable at all** (#69). A quote
	// form hands its source over as a string literal; a bare body does not, and until
	// this field existed the s-expression reader had *consumed* the text into nodes with
	// no way back — so 1119 must-succeed modules across 57 files were recorded
	// `KindUnsupported`, against 7 reachable quote modules. The wat reader was being
	// scored for the accept direction against 7 vectors out of 1126.
	//
	// Retained on every node rather than on module forms only. Deriving the span at the
	// one call site that needs it would mean re-lexing to find the matching paren — a
	// second reader of the same grammar, which is the drift shape #33 was filed about.
	// The extent is known exactly once, while the node is being read, and it is recorded
	// there.
	start, end int
}

// span returns the node's source text, given the source it was parsed from.
//
// Callers pass the same src the parser was built on; the node does not retain it, because a
// node holding a slice of the whole file would make every node a reason to keep the file
// alive. Script.src is the one retention point (see Parse).
func (n node) span(src []byte) []byte { return src[n.start:n.end] }

func (n node) isList() bool { return n.list != nil }

// head returns the first atom of a list, or "" — the form's keyword.
func (n node) head() string {
	if len(n.list) > 0 && !n.list[0].isList() && !n.list[0].isS {
		return n.list[0].atom
	}
	return ""
}

// isAnnotation reports whether this node is a custom annotation, `(@id ...)`.
//
// The head atom is tested for a leading `@` rather than for a known id, because the id space
// is open: `annotations.wast:5` is `(@@) (@$) (@+) (@0) (@.) (@!$@#$23414@#$)` and every one of
// those is a well-formed annotation. `(@"a")` lexes as the atom `@` followed by a string,
// since isDelim breaks atoms at `"` — so the atom is `@` and the prefix test still holds.
func (n node) isAnnotation() bool { return strings.HasPrefix(n.head(), "@") }

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
		if n.isAnnotation() {
			continue
		}
		out = append(out, n)
	}
}

// Annotations are dropped from the tree, at both levels a node can be assembled — here and in
// parseNode's list loop — rather than stepped over by the readers that look for a head.
//
// **The two arms are not equally witnessed, and this arm is the unwitnessed one.** Disabling the
// skip *here* leaves TestAnnotatedModulesInstantiate green; disabling it in parseNode's list loop
// returns all three `annotations.wast` commands to `<unsupported>`. The reason is that a command
// like `((@a) module …)` is not itself an annotation — its head is `""` — so the node this arm
// drops is only a *bare* annotation standing between two top-level commands, and no file in the
// corpus has one (`grep -c '^(@' testdata/spec/*.wast` is zero everywhere). Retained anyway,
// because the reference's transparency is positional-blind by construction and dropping this arm
// would put back the one positional exception this reader is meant not to have — a pinned corpus
// that bumps is exactly where such an exception surfaces. Stated because it was found by the
// falsification *not* failing, which is the only way an unexercised arm announces itself, and
// because a branch whose corpus witness is zero should say so rather than be counted as covered.
//
// **That is where the reference puts them, and the mechanism is worth naming because it is not
// "skip the first element".** `lexer.mll:821-828` matches `"(@"(id)` and `"(@"(string)`, calls
// `Annot.record` to file the annotation in a side table, and then tail-calls `token lexbuf` —
// emitting *no token at all*. It sits three lines above the `;;` and `(;` rules (`:831-834`),
// which do the same thing, so an annotation is lexically transparent to the grammar in exactly
// the way a comment is. Nothing downstream of the lexer can see one; `parse_annots`
// (`parser.mly:252-269`) reads the side table afterwards to build custom sections.
//
// The consequence for this reader is the reason it is done here rather than in `head()`: an
// annotation is legal wherever a token is, so leaving the nodes in place would pollute every
// *positional* read as well as every head read. `wast.go` has six of those — `len(n.list) == 3`
// and `n.list[1]`/`n.list[2]` tests in the `assert_malformed`, `register` and action arms — and
// each would need its own skip, which is six chances to miss one against zero. The three
// `((@a) module …)` commands in `annotations.wast` only made the head case visible first.
//
// Dropping *after* the node is parsed, rather than scanning past the annotation's bytes in
// skipSpace, reuses the one grammar reader: annotations nest, and contain string literals with
// unbalanced parens (`:15` has `")" "(" x")"y`) and block comments (`:17` has `(;bla;)`). A
// second scanner for that is the drift shape the `start`/`end` comment above already names.
// Node extents are untouched, which is what keeps this invisible to the module arms — a module
// command's `Source` is its byte span, annotations included, and the engine's own text front end
// reads that shape already.
//
// **One stated leniency, unwitnessed by the corpus.** The reference's lexer *errors* on a
// malformed annotation id — `"(@" { error lexbuf "empty annotation id" }` (`:829`) — and this
// reader does not: `(@)` is dropped as an annotation instead. Every corpus vector for that
// direction is a `(module quote …)` form (`annotations.wast:72-83`, seven `empty annotation id`
// and three `unclosed annotation`), so the text is a string literal handed to the engine's front
// end, which does produce those messages; none of them reaches this reader. Recorded rather than
// guarded, because a guard no falsification can reach is the stillborn shape — and recorded
// rather than left silent, because the accept direction is where a harness's leniency hides.

func (p *parser) parseNode() (node, error) {
	p.skipSpace()
	if p.off >= len(p.src) {
		return node{}, p.errf("unexpected end of input")
	}
	line := p.line
	// The node's first byte, captured before any of the arms advance. Every return below
	// pairs it with p.off, which is one past the node's last byte at that point — so the
	// extent comes from the reader's own position rather than from a second scan.
	start := p.off
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
				return node{list: items, line: line, start: start, end: p.off}, nil
			}
			item, err := p.parseNode()
			if err != nil {
				return node{}, err
			}
			if item.isAnnotation() {
				continue
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
		return node{str: b, isS: true, line: line, start: start, end: p.off}, nil
	case ';':
		// A lone ';' that skipSpace did not consume — it is neither ';;' nor
		// part of '(;'. Illegal in wast proper, but the annotations proposal
		// permits arbitrary token soup inside (@id ...), and annotations.wast
		// contains lines like `(@a , ; ] [ }} }x{ ({) ,{{};}] ;)`. Lex it as a
		// one-byte atom so the reader can traverse files it does not interpret;
		// parsing and understanding are separate concerns here.
		p.off++
		return node{atom: ";", line: line, start: start, end: p.off}, nil
	default:
		for p.off < len(p.src) && !isDelim(p.src[p.off]) {
			p.off++
		}
		if p.off == start {
			return node{}, p.errf("unexpected byte %#x", c)
		}
		return node{atom: string(p.src[start:p.off]), line: line, start: start, end: p.off}, nil
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
