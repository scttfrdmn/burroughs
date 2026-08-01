package text

// The character classes and productions of lexer.mll's preamble (lines 59–126),
// transcribed one-for-one.
//
// Each matcher reports the length of the longest match at the start of b, or -1 for no
// match. They are pure functions of the byte slice so that the ordering logic in
// lexer.go is the only place disambiguation happens — one rule, one site.
//
// The transcription is deliberate duplication of an authority, same as keywords.go: the
// alternative is inferring these classes from the vectors, and *an order-of-tests claim
// needs an authority, never a derivation from a sample that cannot falsify it* (the 0003
// LEB correction). `symbol` in particular cannot be guessed — it admits `/`, `:`, `.`,
// `'`, `$` and eleven more, and it is what makes `i32.wrap/i64` a single `reserved`
// lexeme rather than two tokens.

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isSymbol is lexer.mll:66–67, verbatim:
//
//	['+''-''*''/''\\''^''~''=''<''>''!''?''@''#''$''%''&''|'':''`''.''\'']
func isSymbol(c byte) bool {
	switch c {
	case '+', '-', '*', '/', '\\', '^', '~', '=', '<', '>', '!', '?', '@', '#',
		'$', '%', '&', '|', ':', '`', '.', '\'':
		return true
	}
	return false
}

// isIDChar is `letter | digit | '_' | symbol` (lexer.mll:109).
func isIDChar(c byte) bool {
	return isLetter(c) || isDigit(c) || c == '_' || isSymbol(c)
}

// isSpace is lexer.mll:71:
//
//	let space = [' ''\x09''\x0a''\x0d']
//
// Indented, and this is not a style choice. gofmt's doc-comment canonicaliser implements
// the old TeX quoting convention: a pair of backticks opens a quotation and a pair of
// single quotes closes it, so both get rewritten to typographic quotes in running prose.
// An OCaml character class is full of adjacent single quotes, so `make fmt` silently
// rewrote this line and annotStringError's — altering a *quotation of the authority*, which
// is the drifted-citation defect (PR #37) with a new cause, and one a formatter applies on
// your behalf while reporting nothing.
//
// Every quoted character class in this package therefore lives in an indented code block,
// which gofmt leaves verbatim; emitVarString's quotation survived on that basis before
// anyone knew the rule. And the prose above deliberately *names* the characters instead of
// showing them, because the first draft of this very paragraph was canonicalised by the
// mechanism it was describing — a comment cannot demonstrate the transformation it warns
// about, since it is subject to it.
//
// Sibling of *comments and ADRs are testimony too*: a citation nobody re-checks is a claim,
// and here the tool nobody re-checks after is the formatter.
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// isControl is `['\x00'-'\x1f'] # space` (lexer.mll:72): the control characters that are
// *not* whitespace. The subtraction matters — without it this arm would swallow the
// newline the space arm must handle.
func isControl(c byte) bool { return c <= 0x1f && !isSpace(c) }

// matchNewline is `ascii_newline | "\x0a\x0d"` (lexer.mll:69–70). Note the order in the
// reference's own definition: LF-CR, not CR-LF.
func matchNewline(b []byte) int {
	if len(b) == 0 {
		return -1
	}
	if len(b) >= 2 && b[0] == '\n' && b[1] == '\r' {
		return 2
	}
	if b[0] == '\n' || b[0] == '\r' {
		return 1
	}
	return -1
}

// matchNum is `digit ('_'? digit)*` (lexer.mll:62). The optional `_` is a separator, so
// a trailing underscore is not part of the match.
func matchNum(b []byte) int {
	if len(b) == 0 || !isDigit(b[0]) {
		return -1
	}
	i := 1
	for i < len(b) {
		if isDigit(b[i]) {
			i++
			continue
		}
		if b[i] == '_' && i+1 < len(b) && isDigit(b[i+1]) {
			i += 2
			continue
		}
		break
	}
	return i
}

func matchHexNum(b []byte) int {
	if len(b) == 0 || !isHexDigit(b[0]) {
		return -1
	}
	i := 1
	for i < len(b) {
		if isHexDigit(b[i]) {
			i++
			continue
		}
		if b[i] == '_' && i+1 < len(b) && isHexDigit(b[i+1]) {
			i += 2
			continue
		}
		break
	}
	return i
}

// matchNat is `num | "0x" hexnum` (lexer.mll:104).
func matchNat(b []byte) int {
	if len(b) >= 2 && b[0] == '0' && (b[1] == 'x' || b[1] == 'X') {
		// The reference writes "0x" lowercase only.
		if b[1] == 'x' {
			if n := matchHexNum(b[2:]); n > 0 {
				return 2 + n
			}
		}
	}
	return matchNum(b)
}

// matchInt is `sign nat` (lexer.mll:105) — the sign is *required*, which is why `nat`
// and `int` are separate arms.
func matchInt(b []byte) int {
	if len(b) == 0 || (b[0] != '+' && b[0] != '-') {
		return -1
	}
	if n := matchNat(b[1:]); n > 0 {
		return 1 + n
	}
	return -1
}

// matchFloat is lexer.mll:108–116, all six alternatives. Longest wins among them, which
// matters for `1.5e3`: the second alternative subsumes the first.
func matchFloat(b []byte) int {
	best := -1
	try := func(n int) {
		if n > best {
			best = n
		}
	}
	i := 0
	if i < len(b) && (b[i] == '+' || b[i] == '-') {
		i = 1
	}
	rest := b[i:]

	// sign? "inf" | sign? "nan" | sign? "nan:" "0x" hexnum
	if hasPrefix(rest, "inf") {
		try(i + 3)
	}
	if hasPrefix(rest, "nan") {
		try(i + 3)
		if hasPrefix(rest[3:], ":0x") {
			if n := matchHexNum(rest[6:]); n > 0 {
				try(i + 6 + n)
			}
		}
	}

	// The hex forms: sign? "0x" hexnum ('.' hexfrac?)? (('p'|'P') sign? num)?
	if hasPrefix(rest, "0x") {
		if h := matchHexNum(rest[2:]); h > 0 {
			j := 2 + h
			if j < len(rest) && rest[j] == '.' {
				j++
				if f := matchHexNum(rest[j:]); f > 0 {
					j += f
				}
				try(i + j) // sign? "0x" hexnum '.' hexfrac?
			}
			if k := matchExp(rest[j:], 'p', 'P'); k > 0 {
				try(i + j + k)
			}
		}
	}

	// The decimal forms: sign? num ('.' frac?)? (('e'|'E') sign? num)?
	if n := matchNum(rest); n > 0 {
		j := n
		if j < len(rest) && rest[j] == '.' {
			j++
			if f := matchNum(rest[j:]); f > 0 {
				j += f
			}
			try(i + j) // sign? num '.' frac?
		}
		if k := matchExp(rest[j:], 'e', 'E'); k > 0 {
			try(i + j + k)
		}
	}
	return best
}

// matchExp is the shared `('e'|'E') sign? num` tail of the float productions.
func matchExp(b []byte, lo, up byte) int {
	if len(b) == 0 || (b[0] != lo && b[0] != up) {
		return -1
	}
	i := 1
	if i < len(b) && (b[i] == '+' || b[i] == '-') {
		i++
	}
	if n := matchNum(b[i:]); n > 0 {
		return i + n
	}
	return -1
}

// matchKeyword is `['a'-'z'] (letter | digit | '_' | '.' | ':')+` (lexer.mll:112).
//
// Note what is *absent* from the continuation set: `/`, `-`, and every other symbol. That
// absence is what makes `i32.wrap/i64` match `reserved` at 12 bytes and `keyword` at only
// 8, so the longest-match rule sends the whole lexeme to the error and the message names
// it in full — which `obsolete-keywords.wast:67` reads back.
//
// Also note the `+`: a keyword is at least two characters. A bare `a` is not a keyword,
// it is `reserved`.
func matchKeyword(b []byte) int {
	if len(b) == 0 || b[0] < 'a' || b[0] > 'z' {
		return -1
	}
	i := 1
	for i < len(b) {
		c := b[i]
		if isLetter(c) || isDigit(c) || c == '_' || c == '.' || c == ':' {
			i++
			continue
		}
		break
	}
	if i < 2 {
		return -1
	}
	return i
}

// matchReserved is `(idchar | string)+ | ',' | ';' | '[' | ']' | '{' | '}'`
// (lexer.mll:113) — the second `unknown operator` producer's subject.
//
// The `string` alternative inside the repetition is not decoration: `0"a"` is one
// reserved lexeme, not a nat followed by a string, because the repetition can alternate.
func matchReserved(b []byte) int {
	if len(b) == 0 {
		return -1
	}
	switch b[0] {
	case ',', ';', '[', ']', '{', '}':
		return 1
	}
	i := 0
	for i < len(b) {
		if isIDChar(b[i]) {
			i++
			continue
		}
		if b[i] == '"' {
			if n := matchString(b[i:]); n > 0 {
				i += n
				continue
			}
		}
		break
	}
	if i == 0 {
		return -1
	}
	return i
}

// matchEqNat is the `"offset="(nat)` / `"align="(nat)` arms (lexer.mll:812–813).
func matchEqNat(b []byte, prefix string) int {
	if !hasPrefix(b, prefix) {
		return -1
	}
	if n := matchNat(b[len(prefix):]); n > 0 {
		return len(prefix) + n
	}
	return -1
}

// matchVarID is `'$'(id)` where `id = idchar+` (lexer.mll:110, 817).
func matchVarID(b []byte) int {
	if len(b) == 0 || b[0] != '$' {
		return -1
	}
	i := 1
	for i < len(b) && isIDChar(b[i]) {
		i++
	}
	if i == 1 {
		return -1
	}
	return i
}

// matchVarString is `'$'(string)` (lexer.mll:818).
func matchVarString(b []byte) int {
	if len(b) == 0 || b[0] != '$' {
		return -1
	}
	if n := matchString(b[1:]); n > 0 {
		return 1 + n
	}
	return -1
}

// matchString is `'"' character* '"'` (lexer.mll:107) with `character` at lexer.mll:88–94.
// Reports the whole span including quotes, or -1 if it does not close cleanly.
func matchString(b []byte) int {
	if len(b) == 0 || b[0] != '"' {
		return -1
	}
	i := 1
	for i < len(b) {
		if b[i] == '"' {
			return i + 1
		}
		n := matchCharacter(b[i:])
		if n < 0 {
			return -1
		}
		i += n
	}
	return -1
}

// matchCharacter is `character` (lexer.mll:88–94):
//
//	[^'"''\\''\x00'-'\x1f''\x7f'-'\xff'] | utf8enc | '\\'escape
//	  | '\\'hexdigit hexdigit | "\\u{" hexnum '}'
func matchCharacter(b []byte) int {
	if len(b) == 0 {
		return -1
	}
	c := b[0]
	if c == '\\' {
		if len(b) < 2 {
			return -1
		}
		switch b[1] {
		case 'n', 'r', 't', '\\', '\'', '"':
			return 2
		}
		if hasPrefix(b[1:], "u{") {
			if n := matchHexNum(b[3:]); n > 0 && 3+n < len(b) && b[3+n] == '}' {
				return 4 + n
			}
			return -1
		}
		if len(b) >= 3 && isHexDigit(b[1]) && isHexDigit(b[2]) {
			return 3
		}
		return -1
	}
	if c == '"' {
		return -1
	}
	if c <= 0x1f || c == 0x7f {
		return -1
	}
	if c >= 0x80 {
		return matchUTF8Enc(b)
	}
	return 1
}

// matchUnclosedString is `'"'character*(newline|eof)` (lexer.mll:137).
func matchUnclosedString(b []byte) int {
	if len(b) == 0 || b[0] != '"' {
		return -1
	}
	i := 1
	for i < len(b) {
		if n := matchNewline(b[i:]); n > 0 {
			return i + n
		}
		if b[i] == '"' {
			return -1 // it closed; not this arm
		}
		n := matchCharacter(b[i:])
		if n < 0 {
			return -1
		}
		i += n
	}
	return i // ran to eof
}

// matchStringWithControl is `'"'character*(control#ascii_newline)` (lexer.mll:138).
func matchStringWithControl(b []byte) int {
	if len(b) == 0 || b[0] != '"' {
		return -1
	}
	i := 1
	for i < len(b) {
		if b[i] == '"' {
			return -1
		}
		if isControl(b[i]) {
			return i + 1
		}
		n := matchCharacter(b[i:])
		if n < 0 {
			return -1
		}
		i += n
	}
	return -1
}

// matchStringBadEscape is `'"'character*'\\'_` (lexer.mll:140).
func matchStringBadEscape(b []byte) int {
	if len(b) == 0 || b[0] != '"' {
		return -1
	}
	i := 1
	for i < len(b) {
		if b[i] == '"' {
			return -1
		}
		if b[i] == '\\' {
			if n := matchCharacter(b[i:]); n > 0 {
				i += n
				continue
			}
			if i+1 < len(b) {
				return i + 2
			}
			return -1
		}
		n := matchCharacter(b[i:])
		if n < 0 {
			return -1
		}
		i += n
	}
	return -1
}

// matchUTF8Enc is `utf8enc` (lexer.mll:76–83), the *well-formed* multi-byte encodings
// with the surrogate and overlong ranges already excluded by the authority's own ranges.
// Transcribed rather than delegated to unicode/utf8, because the reference's ranges are
// the definition here and a stdlib decoder agreeing today is not the same claim.
func matchUTF8Enc(b []byte) int {
	if len(b) == 0 {
		return -1
	}
	cont := func(i int) bool { return i < len(b) && b[i] >= 0x80 && b[i] <= 0xbf }
	switch c := b[0]; {
	case c >= 0xc2 && c <= 0xdf:
		if cont(1) {
			return 2
		}
	case c == 0xe0:
		if len(b) > 1 && b[1] >= 0xa0 && b[1] <= 0xbf && cont(2) {
			return 3
		}
	case c == 0xed:
		if len(b) > 1 && b[1] >= 0x80 && b[1] <= 0x9f && cont(2) {
			return 3
		}
	case (c >= 0xe1 && c <= 0xec) || c == 0xee || c == 0xef:
		if cont(1) && cont(2) {
			return 3
		}
	case c == 0xf0:
		if len(b) > 1 && b[1] >= 0x90 && b[1] <= 0xbf && cont(2) && cont(3) {
			return 4
		}
	case c == 0xf4:
		if len(b) > 1 && b[1] >= 0x80 && b[1] <= 0x8f && cont(2) && cont(3) {
			return 4
		}
	case c >= 0xf1 && c <= 0xf3:
		if cont(1) && cont(2) && cont(3) {
			return 4
		}
	}
	return -1
}

// matchLineComment is `";;"utf8_no_nl*` with its eof and newline variants
// (lexer.mll:831–833). The newline is consumed as part of the comment.
//
// The star's *class* is load-bearing: `utf8_no_nl = ascii_no_nl | utf8enc` (lexer.mll:85),
// so the match stops at a byte that is neither ascii nor a well-formed encoding, and the
// reference's own comment says why — `token lexbuf (* causes error on following position *)`.
// A loop that skipped to eof instead would make `;; \ff` lex clean, which is the
// permissive-default defect that `scanAnnotBody` was already fixed for; *disguises come in
// families*, and this one was found by sweeping the comment scanners after the block-comment
// grave below.
func matchLineComment(b []byte) int {
	if !hasPrefix(b, ";;") {
		return -1
	}
	i := 2
	for i < len(b) {
		if n := matchNewline(b[i:]); n > 0 {
			return i + n
		}
		if b[i] < 0x80 {
			i++
			continue
		}
		if n := matchUTF8Enc(b[i:]); n > 0 {
			i += n
			continue
		}
		return i // the bad byte errors at the following position, as the reference notes
	}
	return i
}

// matchBlockComment is `"(;" ... ";)"`, nesting, per the reference's `comment` scanner
// (lexer.mll:902–908). It reports the span to consume; scanBlockComment carries the verdict.
func matchBlockComment(b []byte) int {
	n, _ := scanBlockComment(b)
	return n
}

// scanBlockComment is the `comment` scanner. It returns the length to consume and "", or
// the length to consume and an error message — the length is non-zero in *both* cases, so
// an erroring comment still makes progress rather than stalling (grave #18's shape).
//
// Two arms of the reference are easy to lose and both were:
//
//   - `| _ { error lexbuf "malformed UTF-8 encoding" }` — a block comment is not a region
//     where bytes are ignored. A `default: i++` lexes `(; \ff ;)` clean. Exactly the defect
//     removed from `scanAnnotBody`, in the sibling scanner, found by sweeping for it.
//   - closedness is the *depth*, not the last two bytes. The predecessor asked
//     `hasPrefix(s[len(s)-2:], ";)")`, which says `(; (; half closed ;)` closed — it ends in
//     `;)` at depth 1. The tell was in its own doc comment, which claimed a "negative length
//     convention" the code did not implement: **a comment describing a mechanism that isn't
//     there is the defect stated as the rule**, and it survived review for the same reason
//     0003's LEB ordering did. Caught by a control asserting the partition (closed / unclosed /
//     ill-formed) rather than by a vector; the suite has no nested-unbalanced case.
func scanBlockComment(b []byte) (length int, errMsg string) {
	if !hasPrefix(b, "(;") {
		return -1, ""
	}
	depth, i := 1, 2
	for i < len(b) {
		switch {
		case hasPrefix(b[i:], "(;"):
			depth++
			i += 2
		case hasPrefix(b[i:], ";)"):
			depth--
			i += 2
			if depth == 0 {
				return i, ""
			}
		default:
			if n := matchNewline(b[i:]); n > 0 {
				i += n
				continue
			}
			if b[i] < 0x80 { // ascii, including controls: `utf8_no_nl` admits them
				i++
				continue
			}
			if n := matchUTF8Enc(b[i:]); n > 0 {
				i += n
				continue
			}
			return i + 1, "malformed UTF-8 encoding"
		}
	}
	return i, "unclosed comment"
}

// matchAnnotStart matches `"(@"(id)` or `"(@"(string)` (lexer.mll:822, 825). The body is
// consumed separately by scanAnnotBody, because the reference uses a different *rule* for
// it and that rule's arms differ — most importantly its `reserved` arm produces an atom
// rather than `unknown operator`.
func matchAnnotStart(b []byte, str bool) int {
	if !hasPrefix(b, "(@") {
		return -1
	}
	if str {
		if n := matchString(b[2:]); n > 0 {
			return 2 + n
		}
		return -1
	}
	i := 2
	for i < len(b) && isIDChar(b[i]) {
		i++
	}
	if i == 2 {
		return -1
	}
	return i
}

// scanAnnotBody consumes an annotation body up to its balancing ')' , per the reference's
// `annot` rule (lexer.mll:857–900). Returns the length consumed and "" , or 0 and an
// error message.
//
// This is where the third `reserved` arm lives, and it is the reason this is a separate
// function rather than a recursive call into `token`: here a reserved lexeme is an
// *atom*, not an error. `annotations.wast:14` is the vector that cares — the wast reader
// already had a grave over exactly this file (a bare ';' inside an annotation), and this
// is the same token soup one layer down.
// The arms are transcribed in the reference's order, and the *last two* are the ones a
// permissive implementation loses: `| utf8 { error "illegal character" }` and
// `| _ { error "malformed UTF-8 encoding" }` (lexer.mll:899–900). A `default: i++` that
// skips an unrecognized byte lexes `(@a \00)` cleanly and silently converts 35 vectors
// the spec calls malformed into accepted input — measured, and it is what refuted this
// work's own forecast of 627. An annotation body is not a region where bytes are ignored.
func scanAnnotBody(b []byte) (length int, errMsg string) {
	depth, i := 1, 0
	for i < len(b) {
		switch {
		case b[i] == ')':
			depth--
			i++
			if depth == 0 {
				return i, ""
			}
		case hasPrefix(b[i:], "(@"):
			// A nested annotation: its id must still be well-formed.
			if n := matchAnnotStart(b[i:], false); n > 0 {
				i += n
				depth++
				continue
			}
			if n := matchAnnotStart(b[i:], true); n > 0 {
				i += n
				depth++
				continue
			}
			return 0, "empty annotation id"
		case hasPrefix(b[i:], "(;"):
			n, msg := scanBlockComment(b[i:])
			if msg != "" {
				return 0, msg
			}
			i += n
		case b[i] == '(':
			depth++
			i++
		case b[i] == '"':
			n := matchString(b[i:])
			if n < 0 {
				// The `annot` rule has the same three string-failure arms as `token`, in the
				// same order (lexer.mll:885–890) — and its control class is *wider*, listing
				// `'\x7f'` explicitly where `token`'s `control` stops at 0x1f. The
				// predecessor here had both branches of an if/else returning
				// `unclosed string literal`: a dead branch, which is the `ErrTrailingData`
				// shape (a defined-but-unreachable answer), and it made DEL-in-a-string
				// report `unclosed` in annotations while the reference reports a control
				// character. Two arms conflated is one message that is right by accident.
				return 0, annotStringError(b[i:])
			}
			i += n
		case b[i] == '$':
			if n := matchVarID(b[i:]); n > 0 {
				i += n
				continue
			}
			if n := matchVarString(b[i:]); n > 0 {
				v, ok := decodeString(b[i+1 : i+n])
				if !ok {
					return 0, "malformed string literal"
				}
				// Emptiness only, no UTF-8 — the `annot` rule's own `'$'(string)` arm
				// (lexer.mll:874–878) is `if s' = "" then error "empty identifier"` and
				// nothing more, exactly like `token`'s. Sibling of the same wrong-stratum
				// check removed from emitVarString, found by `grep validUTF8` after the
				// authority refuted the first one: *sweep after a grave*, and disguises
				// come in families.
				if len(v) == 0 {
					return 0, "empty identifier"
				}
				i += n
				continue
			}
			return 0, "empty identifier"
		case hasPrefix(b[i:], ";;"):
			i += matchLineComment(b[i:])
		case isSpace(b[i]):
			i++
		default:
			// The reference's ordered tail: reserved (an *atom* here, not an error — the
			// third `reserved` arm), then control, then utf8, then anything.
			if n := matchReserved(b[i:]); n > 0 {
				i += n
				continue
			}
			// `| utf8 { error "illegal character" }` then `| _ { error "malformed UTF-8
			// encoding" }` (lexer.mll:899–900), and `utf8 = ascii | utf8enc`. So a
			// *well-formed* multi-byte character here is still an error — `(@a Heiße
			// Würstchen)` is `illegal character`, not an atom — and only a byte that is
			// neither ascii nor a valid encoding is "malformed UTF-8". Getting this
			// backwards accepts three vectors the spec calls malformed, which is how it
			// was found: by printing what the code returns per vector rather than
			// reasoning about the arms.
			//
			// Note `annot` has no `control` arm of its own, unlike `token`: 0x00–0x1f are
			// plain ascii to `utf8`, so they take the same "illegal character" path as
			// `\7f`. Modelling that as a separate control case produced the right text for
			// the wrong reason and would have drifted the moment either message changed.
			if n := matchUTF8Enc(b[i:]); n > 0 {
				return 0, "illegal character"
			}
			if b[i] < 0x80 {
				return 0, "illegal character"
			}
			return 0, "malformed UTF-8 encoding"
		}
	}
	return 0, "unclosed annotation"
}

// annotStringError picks among the `annot` rule's three string-failure arms, in the
// reference's order (lexer.mll:885–890). b starts at the opening quote and matchString has
// already failed on it.
//
// The control class here is the arm's own (:887), in a code block for the gofmt reason
// documented at isSpace:
//
//	'"'character*['\x00'-'\x09''\x0b'-'\x1f''\x7f']
//
// — not `token`'s `control`, which stops at 0x1f. The difference is one byte and it is the
// whole reason this is a separate function rather than a call into the `token` arms.
func annotStringError(b []byte) string {
	if matchUnclosedString(b) > 0 {
		return "unclosed string literal"
	}
	// Walk the characters to see which arm the failure belongs to. `character*` is the
	// shared prefix of all three, so the first byte it cannot consume decides.
	for i := 1; i < len(b); {
		c := b[i]
		if c == '"' {
			break // matchString would have succeeded; unreachable, but not assumed
		}
		if (c <= 0x09) || (c >= 0x0b && c <= 0x1f) || c == 0x7f {
			return "illegal control character in string literal"
		}
		if c == '\\' {
			if n := matchCharacter(b[i:]); n > 0 {
				i += n
				continue
			}
			return "illegal escape"
		}
		n := matchCharacter(b[i:])
		if n <= 0 {
			return "malformed UTF-8 encoding"
		}
		i += n
	}
	return "unclosed string literal"
}

// validUTF8 reports whether every byte of b is part of a well-formed encoding by the
// authority's own ranges (matchUTF8Enc), not by the stdlib's. Used for the one name the
// lexer itself decodes — `$"..."` — and deliberately not for export names, which are
// parser-layer.
func validUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		if b[i] < 0x80 {
			i++
			continue
		}
		n := matchUTF8Enc(b[i:])
		if n < 0 {
			return false
		}
		i += n
	}
	return true
}

func hasPrefix(b []byte, s string) bool {
	return len(b) >= len(s) && string(b[:len(s)]) == s
}
