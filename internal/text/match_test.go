package text

import (
	"strings"
	"testing"
)

// The productions' own controls, scoped to the byte space rather than to vectors.
//
// # Why these exist, and why they are property-shaped
//
// Falsifying lexer_test.go one mutation at a time turned up five defects it could not see,
// and four of them share a cause worth naming: **both `unknown operator` producers emit the
// same string**, so a defect in the `keyword` production is invisible to any test that
// reads a message. Concretely, all of these survived the whole of lexer_test.go:
//
//   - the `keyword` arm deleted outright — `reserved` matches the same span and produces
//     the same text for every one of the eleven mnemonics;
//   - `matchKeyword` admitting `/` — then `i32.wrap/i64` matches *keyword* at 12, the
//     earliest arm wins instead of the longest, `emitKeyword` misses, and the message is
//     byte-identical to the right answer arrived at the right way;
//   - `matchKeyword` losing its `+` (min two chars) — a bare `a` becomes a keyword lexeme
//     whose table miss says `unknown operator a`, exactly what `reserved` would have said;
//   - `matchNewline` reversed to CR-LF — nothing in the token stream changes, only `Line`.
//
// That is the overfitting law pointed at a test file: every one of those is *right on the
// suite's vectors and wrong in general*, and a bigger corpus would not help, because the
// oracle's expected strings cannot distinguish the cases. So the control cannot be a
// vector. It has to assert the *language* each matcher accepts, over the whole byte space —
// **derive the domain, never enumerate it**, which is also what makes the coverage grow
// with the thing controlled instead of freezing at the moment of authorship.
//
// The one gap left open deliberately: agreement between these transcriptions and
// `lexer.mll` itself needs the vendored reference, so it cannot live in `make check`
// (decision 0007's consequence in this grammar). These judge the transcription's *stated*
// language; the drift lane judges whether that language is the reference's. A drift check
// and an integrity check are not the same question, and only one can be asked without a
// fetch — the same split keywords_test.go documents for the table.

// TestKeywordProductionIsExactlyTheReferences pins `matchKeyword`'s language against
// lexer.mll:112 — `['a'-'z'] (letter | digit | '_' | '.' | ':')+` — by exhausting the byte
// space in both positions rather than sampling it.
//
// The two properties that carry the disambiguation are asserted directly, because they are
// the ones the suite cannot see: `/` is **not** a continuation char (which is what makes
// `i32.wrap/i64` a 12-byte `reserved` and an 8-byte `keyword`), and the repetition is `+`
// so a keyword is at least two characters.
func TestKeywordProductionIsExactlyTheReferences(t *testing.T) {
	isKeywordStart := func(c byte) bool { return c >= 'a' && c <= 'z' }
	isKeywordCont := func(c byte) bool {
		return isLetter(c) || isDigit(c) || c == '_' || c == '.' || c == ':'
	}

	// First position: exactly ['a'-'z'], every one of the 256 bytes checked.
	for c := range 256 {
		b := byte(c)
		got := matchKeyword([]byte{b, 'x'})
		want := -1
		if isKeywordStart(b) {
			want = 2
		}
		if got != want {
			t.Errorf("matchKeyword(%q + \"x\") = %d, want %d; the first position is exactly "+
				"['a'-'z'] (lexer.mll:112)", b, got, want)
		}
	}

	// Continuation position: exactly letter | digit | '_' | '.' | ':'. This is where the
	// `/` case lives, and getting it wrong reverses the longest-match outcome on
	// obsolete-keywords.wast:63 while producing the identical message.
	for c := range 256 {
		b := byte(c)
		got := matchKeyword([]byte{'a', 'b', b})
		want := 2
		if isKeywordCont(b) {
			want = 3
		}
		if got != want {
			t.Errorf("matchKeyword(\"ab\" + %q) = %d, want %d; the continuation set is "+
				"letter|digit|'_'|'.'|':' and notably *excludes* every other symbol", b, got, want)
		}
	}

	// The `+`: two characters minimum. A single letter is not a keyword, it is `reserved`.
	if got := matchKeyword([]byte("a")); got != -1 {
		t.Errorf("matchKeyword(\"a\") = %d, want -1; the production is `['a'-'z'] (...)+` "+
			"and a bare letter falls through to reserved", got)
	}
	if got := matchKeyword([]byte("ab")); got != 2 {
		t.Errorf("matchKeyword(\"ab\") = %d, want 2", got)
	}

	// And the consequence, stated as the observation the disambiguation turns on: the two
	// lengths differ, which is the *only* reason the message names the full lexeme.
	kw, res := matchKeyword([]byte("i32.wrap/i64")), matchReserved([]byte("i32.wrap/i64"))
	if kw != 8 || res != 12 {
		t.Errorf("i32.wrap/i64: keyword=%d reserved=%d, want 8 and 12 — if these are equal "+
			"the earliest arm wins instead of the longest and obsolete-keywords.wast:63 "+
			"still passes, with the right text for the wrong reason", kw, res)
	}
}

// TestSymbolClassIsTheReferences pins `isSymbol` against lexer.mll:66–67 over all 256
// bytes. It cannot be guessed — it admits `/`, `:`, `.`, `'`, `$`, backtick and sixteen
// more — and it is what makes a reserved lexeme swallow `/`.
//
// Written as an independent restatement of the character set rather than a loop over
// `isSymbol` itself, because a control that reads the code it checks agrees with itself by
// construction.
func TestSymbolClassIsTheReferences(t *testing.T) {
	// lexer.mll:66-67, retyped from the reference:
	//   ['+''-''*''/''\\''^''~''=''<''>''!''?''@''#''$''%''&''|'':''`''.''\'']
	const symbols = "+-*/\\^~=<>!?@#$%&|:`.'"
	if len(symbols) != 22 {
		t.Fatalf("the retyped symbol set has %d members, the reference has 22 — a floor on "+
			"the control's own input, since a truncated string would make every disagreement "+
			"below invisible", len(symbols))
	}
	for c := range 256 {
		b := byte(c)
		want := strings.IndexByte(symbols, b) >= 0
		if got := isSymbol(b); got != want {
			t.Errorf("isSymbol(%q) = %v, want %v", b, got, want)
		}
	}
	// The class's consequence for `idchar`, which is what `reserved` is built from.
	for _, c := range []byte{'/', ':', '.', '$', '\'', '`'} {
		if !isIDChar(c) {
			t.Errorf("isIDChar(%q) = false; `idchar = letter | digit | '_' | symbol` "+
				"(lexer.mll:109) and this is a symbol", c)
		}
	}
}

// TestNewlineOrderIsLFCR pins matchNewline against `ascii_newline | "\x0a\x0d"`
// (lexer.mll:69–70). The reference's *own definition* is LF-CR, not the CR-LF a reader
// would assume from DOS line endings, and reversing it changes nothing in the token stream
// — only `Line`, which is why this needs its own assertion rather than riding on a lex
// verdict.
//
// synthetic on the ordering, cited on the consequence: comments.wast:83 is a quote module
// carrying `;; comment` terminated by LF, CR, and CRLF in three functions, which is the
// vector that cares that all three forms terminate a line comment at all.
func TestNewlineOrderIsLFCR(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"\n\r", 2}, // the reference's two-byte form
		{"\r\n", 1}, // *not* two: CR alone, then LF is a separate newline
		{"\n", 1},
		{"\r", 1},
		{"\n\n", 1},
		{"x", -1},
	}
	for _, c := range cases {
		if got := matchNewline([]byte(c.src)); got != c.want {
			t.Errorf("matchNewline(%q) = %d, want %d; the reference defines the two-byte "+
				"form as \\x0a\\x0d (LF-CR), and CR-LF is two newlines", c.src, got, c.want)
		}
	}

	// The observable consequence, so the ordering claim is anchored to something a caller
	// can see: `\r\n` is two newlines and `\n\r` is one, so the same two bytes in opposite
	// order put a following token on different lines.
	for _, c := range []struct {
		src  string
		line int
	}{
		// `nop` and not a bare letter, because a one-char lexeme is `reserved` rather than a
		// keyword — the min-2 rule, met while writing its own test's fixture.
		{"\n\r nop", 2}, // one newline
		{"\r\n nop", 3}, // two
	} {
		l := NewLexer([]byte(c.src))
		tok, err := l.Next()
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		if tok.Line != c.line {
			t.Errorf("%q put the token on line %d, want %d", c.src, tok.Line, c.line)
		}
	}
}

// TestUTF8EncRangesExcludeSurrogatesAndOverlongs pins matchUTF8Enc against lexer.mll:76–83.
//
// The authority's ranges already exclude the surrogates (`0xed` continues only to `0x9f`),
// the overlongs (`0xc2` is the first legal two-byte lead, `0xe0` continues from `0xa0`,
// `0xf0` from `0x90`), and everything past U+10FFFF (`0xf4` continues only to `0x8f`).
// Widening any of them is invisible in the token stream — it moves a byte between the
// `illegal character` and `malformed UTF-8 encoding` arms of the *annotation* rule, which
// is one vector's worth of difference and no test's worth unless asked directly.
//
// Transcribed rather than delegated to unicode/utf8 for the standing reason: the
// reference's ranges are the definition here, and a stdlib decoder agreeing today is a
// different claim from the ranges being right.
func TestUTF8EncRangesExcludeSurrogatesAndOverlongs(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want int
	}{
		{"two-byte minimum", []byte{0xc2, 0x80}, 2},
		{"overlong two-byte c0", []byte{0xc0, 0x80}, -1},
		{"overlong two-byte c1", []byte{0xc1, 0xbf}, -1},
		{"three-byte minimum", []byte{0xe0, 0xa0, 0x80}, 3},
		{"overlong three-byte", []byte{0xe0, 0x9f, 0xbf}, -1},
		{"surrogate d800", []byte{0xed, 0xa0, 0x80}, -1},
		{"surrogate dfff", []byte{0xed, 0xbf, 0xbf}, -1},
		{"just below surrogates", []byte{0xed, 0x9f, 0xbf}, 3},
		{"four-byte minimum", []byte{0xf0, 0x90, 0x80, 0x80}, 4},
		{"overlong four-byte", []byte{0xf0, 0x8f, 0xbf, 0xbf}, -1},
		{"max scalar", []byte{0xf4, 0x8f, 0xbf, 0xbf}, 4},
		{"past max scalar", []byte{0xf4, 0x90, 0x80, 0x80}, -1},
		{"f5 lead", []byte{0xf5, 0x80, 0x80, 0x80}, -1},
		{"bare continuation", []byte{0x80}, -1},
		{"truncated", []byte{0xe1, 0x80}, -1},
	}
	if len(cases) < 12 {
		t.Fatalf("only %d cases; this is a range partition and a short list would leave "+
			"whole ranges unasserted", len(cases))
	}
	for _, c := range cases {
		if got := matchUTF8Enc(c.b); got != c.want {
			t.Errorf("%s: matchUTF8Enc(% x) = %d, want %d", c.name, c.b, got, c.want)
		}
	}

	// The observable consequence in the annotation rule, which is where the two arms sit
	// next to each other: a well-formed char is `illegal character`, an ill-formed one is
	// `malformed UTF-8 encoding`. A widened surrogate range moves the first case's answer
	// to the second, and only this pairing can see it.
	//
	// derived from annotations.wast:23,57 — the suite brackets the two arms; the surrogate
	// specifically is on the ill-formed side by the authority's ranges and has no vector.
	if got := lexErr(t, "(@a \xed\xa0\x80)"); got != "malformed UTF-8 encoding" {
		t.Errorf("a surrogate in an annotation body gave %q, want `malformed UTF-8 "+
			"encoding` — if it says `illegal character` the 0xed range was widened", got)
	}
	if got := lexErr(t, "(@a \xe4\xb8\x80)"); got != "illegal character" {
		t.Errorf("a well-formed multi-byte char in an annotation body gave %q, want "+
			"`illegal character`", got)
	}
}

// TestValidKeywordsProduceKeywordTokens is the accept direction, and it is the control
// whose absence let the whole `keyword` arm be deleted with every test still green.
//
// Deleting the arm leaves `reserved` matching the same spans, so every reject-direction
// assertion still passes — the eleven mnemonics still error, with the same text. What
// changes is that no input produces a `KeywordTok` at all, i.e. the lexer stops lexing the
// 589 keywords the table exists to hold. *An accept-direction fact needs an
// accept-direction control*; the reject direction cannot imply it, and the committed
// table's own tests judge the table rather than the arm that reads it.
//
// The domain is derived from `keywords` rather than listed, so it grows with the table.
func TestValidKeywordsProduceKeywordTokens(t *testing.T) {
	if len(keywords) < 400 {
		t.Fatalf("keywords holds %d entries; the floor is 400 and a truncated table would "+
			"make the sweep below assert almost nothing", len(keywords))
	}
	var checked, skipped int
	for kw := range keywords {
		// A handful of table entries are not `keyword`-shaped — `offset=` and friends have
		// their own arms. Those are covered by TestDisambiguationIsLongestThenEarliest, and
		// counted here so the exclusion cannot grow silently.
		if matchKeyword([]byte(kw)) != len(kw) {
			skipped++
			continue
		}
		toks, err := LexAll([]byte(kw))
		if err != nil {
			t.Errorf("%q is in the committed table but does not lex: %v\n\t"+
				"the keyword arm is what reads the table; if it is shadowed or missing, "+
				"every reject-direction test still passes and no input lexes as a keyword",
				kw, err)
			continue
		}
		if len(toks) != 1 || toks[0].Kind != KeywordTok {
			t.Errorf("%q lexed as %v, want a single KeywordTok", kw, toks)
			continue
		}
		if toks[0].Text != kw {
			t.Errorf("%q lexed with Text %q", kw, toks[0].Text)
		}
		checked++
	}
	if checked < 400 {
		t.Fatalf("only %d of %d table entries were checked as keyword tokens (%d skipped as "+
			"not keyword-shaped); the sweep is not reaching the table", checked, len(keywords), skipped)
	}
	t.Logf("%d keywords lex as KeywordTok, %d skipped as not keyword-shaped", checked, skipped)
}

// TestUnicodeEscapeEncodesSurrogatesRaw pins `\u{...}` to `Utf8.encode`, which is the
// authority for this and has **no surrogate check** — only `Utf8.decode` rejects them, via
// `code` (binary/utf8.ml:26). So `\u{d800}` is three raw bytes `ed a0 80`, and delegating
// to Go's `string(rune(...))` or `utf8.AppendRune` silently substitutes U+FFFD (`ef bf bd`)
// — a *different string*, accepted either way, wrong content.
//
// synthetic, and the reason it has to be: no suite vector escapes a surrogate. Grepping
// `test/core/*.wast` for `\u{d8`, `\u{110000` and friends returns nothing, so the oracle
// cannot see this and never will — which is exactly the case §9 G-3 is about. The defect
// this guards against survived every other control in this package; it was found by
// mutation, not by a vector, and `appendUTF8` was already correct — the control confirms
// existing code rather than driving a fix, which is why its falsifiability was checked by
// swapping in the stdlib encoder and watching two rows fail.
//
// The out-of-range half is the other side of the same authority: `encode` *raises* above
// 0x10FFFF (:21), so `\u{110000}` must be rejected rather than clamped.
func TestUnicodeEscapeEncodesSurrogatesRaw(t *testing.T) {
	for _, c := range []struct {
		src  string
		want []byte
	}{
		{`"\u{d800}"`, []byte{0xed, 0xa0, 0x80}}, // low surrogate, raw — not U+FFFD
		{`"\u{dfff}"`, []byte{0xed, 0xbf, 0xbf}},
		{`"\u{0}"`, []byte{0x00}},
		{`"\u{7f}"`, []byte{0x7f}},
		{`"\u{80}"`, []byte{0xc2, 0x80}},
		{`"\u{7ff}"`, []byte{0xdf, 0xbf}},
		{`"\u{800}"`, []byte{0xe0, 0xa0, 0x80}},
		{`"\u{ffff}"`, []byte{0xef, 0xbf, 0xbf}},
		{`"\u{10000}"`, []byte{0xf0, 0x90, 0x80, 0x80}},
		{`"\u{10ffff}"`, []byte{0xf4, 0x8f, 0xbf, 0xbf}},
	} {
		t.Run(c.src, func(t *testing.T) {
			toks, err := LexAll([]byte(c.src))
			if err != nil {
				t.Fatalf("must lex: %v", err)
			}
			if len(toks) != 1 {
				t.Fatalf("got %d tokens", len(toks))
			}
			if got := toks[0].Value; string(got) != string(c.want) {
				t.Fatalf("Value = % x, want % x — `Utf8.encode` has no surrogate check and "+
					"no range clamp, so a stdlib encoder's U+FFFD substitution is a "+
					"different string accepted as if it were the right one", got, c.want)
			}
		})
	}
	// Above the maximum scalar `encode` raises (binary/utf8.ml:21), so this is a reject.
	if got := lexErr(t, `"\u{110000}"`); got == "" {
		t.Errorf("`\\u{110000}` must be rejected: Utf8.encode raises above 0x10FFFF rather " +
			"than clamping")
	}
}

// TestSignedNatIsAnEquivalentMutantNotAGap records a *negative* result, because a survivor
// that is not a gap is worth as much as one that is and is otherwise indistinguishable from
// a missing control.
//
// Making `matchInt`'s sign optional survived the whole battery, and it is not a hole: `nat`
// precedes `int` in `arms` and matches the identical span on unsigned input, so
// earliest-arm-wins picks `NatTok` no matter what `matchInt` says. The mutation is
// unobservable through the lexer's interface — an **equivalent mutant**, and writing a test
// to kill it would mean testing `matchInt` in isolation for a property no caller can see.
//
// Pinned here as the property that *makes* it equivalent, so that if the arm order ever
// changes, the equivalence claim fails rather than silently becoming false. (Swapping the
// two arms is caught by TestNoArmIsShadowed, which is the other half of the guard.)
func TestSignedNatIsAnEquivalentMutantNotAGap(t *testing.T) {
	natIdx, intIdx := -1, -1
	for i := range arms {
		switch arms[i].name {
		case "nat":
			natIdx = i
		case "int":
			intIdx = i
		}
	}
	if natIdx < 0 || intIdx < 0 || natIdx > intIdx {
		t.Fatalf("nat at %d, int at %d: the equivalence recorded in this test's comment "+
			"depends on nat preceding int, and it no longer does", natIdx, intIdx)
	}
	for _, src := range []string{"42", "0", "0x1f", "1_0"} {
		toks, err := LexAll([]byte(src))
		if err != nil || len(toks) != 1 || toks[0].Kind != NatTok {
			t.Errorf("%q must lex as a single NatTok, got %v %v", src, toks, err)
		}
	}
}

// TestTokenPositionsAreTheSpanStart pins Offset and Line to the *start* of the lexeme.
//
// Nothing in the reject direction reads these, so they are exactly the fields that can be
// wrong for a whole release: the messages carry their own offsets and the board reads
// neither. Asserted here because the parser (#53, PR B and after) will report positions
// out of them, and a position that is off by a token length is a diagnostic that sends a
// reader to the wrong place — the *error message is testimony* rule, applied one layer
// before there is a message.
func TestTokenPositionsAreTheSpanStart(t *testing.T) {
	const src = "(module\n  (func $f)\n)"
	toks, err := LexAll([]byte(src))
	if err != nil {
		t.Fatalf("%q must lex: %v", src, err)
	}
	want := []struct {
		kind   TokenKind
		text   string
		offset int
		line   int
	}{
		{LParen, "(", 0, 1},
		{KeywordTok, "module", 1, 1},
		{LParen, "(", 10, 2},
		{KeywordTok, "func", 11, 2},
		{VarTok, "$f", 16, 2},
		{RParen, ")", 18, 2},
		{RParen, ")", 20, 3},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want), toks)
	}
	for i, w := range want {
		got := toks[i]
		if got.Kind != w.kind || got.Text != w.text || got.Offset != w.offset || got.Line != w.line {
			t.Errorf("token %d = {%v %q off=%d line=%d}, want {%v %q off=%d line=%d}",
				i, got.Kind, got.Text, got.Offset, got.Line, w.kind, w.text, w.offset, w.line)
		}
	}
	// And the offsets are the span *starts*, which is the claim a length-off-by-one would
	// break while every Kind above still matched.
	for i, got := range toks {
		if src[got.Offset:got.Offset+len(got.Text)] != got.Text {
			t.Errorf("token %d claims offset %d but src there is %q, not %q",
				i, got.Offset, src[got.Offset:min(got.Offset+len(got.Text), len(src))], got.Text)
		}
	}
}

// TestNumericProductionsAreDisjointWhereTheReferenceSaysSo pins the nat/int/float split.
//
// `int` requires its sign (lexer.mll:105), which is the whole reason `nat` and `int` are
// separate arms rather than one with an optional sign — and a reader that made the sign
// optional would produce an `IntTok` where the grammar wants a `NatTok`, invisible until a
// parser distinguishes them. Same class as the keyword defects: a token-stream difference
// no message reads.
func TestNumericProductionsAreDisjointWhereTheReferenceSaysSo(t *testing.T) {
	for _, c := range []struct {
		src  string
		kind TokenKind
	}{
		{"0", NatTok},
		{"42", NatTok},
		{"1_000", NatTok},
		{"0x1f", NatTok},
		{"0xdead_beef", NatTok},
		{"+1", IntTok},
		{"-1", IntTok},
		{"-0x1f", IntTok},
		{"1.5", FloatTok},
		{"1.5e3", FloatTok},
		{"1e3", FloatTok},
		{"inf", FloatTok},
		{"-inf", FloatTok},
		{"nan", FloatTok},
		{"nan:0x1", FloatTok},
		{"0x1p3", FloatTok},
		{"0x1.8p3", FloatTok},
	} {
		t.Run(c.src, func(t *testing.T) {
			toks, err := LexAll([]byte(c.src))
			if err != nil {
				t.Fatalf("must lex: %v", err)
			}
			if len(toks) != 1 {
				t.Fatalf("got %d tokens, want 1: %v", len(toks), toks)
			}
			if toks[0].Kind != c.kind {
				t.Fatalf("lexed as %v, want %v", toks[0].Kind, c.kind)
			}
			if toks[0].Text != c.src {
				t.Fatalf("Text = %q, want the whole lexeme", toks[0].Text)
			}
		})
	}

	// `inf` and `nan` are floats, not keywords, and the table must not claim them — the
	// arms tie at three bytes and float comes first, so a table entry would be shadowed
	// and unreachable, which is a silent contradiction between two authorities.
	for _, kw := range []string{"inf", "nan"} {
		if _, ok := keywords[kw]; ok {
			t.Errorf("%q is in the committed keyword table, but the float arm precedes the "+
				"keyword arm and matches the same 3 bytes, so the entry is unreachable", kw)
		}
	}
}

// TestStringProductionRejectsWhatTheReferenceRejects covers the four string arms as a
// partition, by their *messages*, since these are the ones the suite reads.
//
// The four are separate arms in the reference (lexer.mll:137–140) with four distinct
// errors, and they overlap in shape — every one of them starts `'"' character*`. So a
// reader that ordered them differently, or collapsed two, still errors on every input any
// one of them matches, with the wrong text. Asserting them together is the only way to see
// that.
func TestStringProductionRejectsWhatTheReferenceRejects(t *testing.T) {
	for _, v := range []lexVector{
		// derived from annotations.wast:91,92 — the suite's `unclosed string` vectors are
		// inside annotation bodies, which reach the `annot` rule's copies of these arms;
		// the `token` rule's arms are the same four in the same order and the bare forms
		// are entailed rather than vectored.
		{"unclosed at eof", `"abc`, "unclosed string literal"},
		{"unclosed at newline", "\"abc\n\"", "unclosed string literal"},
		{"control character", "\"a\x01b\"", "illegal control character in string literal"},
		// `\x7f` is *not* this arm's, and the row asserting it was is retained inverted
		// because the reasoning that produced it is the interesting part. `control` is
		// `['\x00'-'\x1f'] # space` (lexer.mll:72) and stops at 0x1f; DEL is excluded by
		// `character` itself (`'\x7f'-'\xff'`, :89), so no string arm matches at all and the
		// catch-all answers. The `annot` rule's copy of the arm *does* list `'\x7f'`
		// explicitly (:887) — so the two rules genuinely differ here, and assuming they
		// agreed is what produced the wrong row. Two scanners, two answers, one byte.
		{"del is not this arm's, in token", "\"a\x7fb\"", "malformed UTF-8 encoding"},
		{"del is this arm's, in annot", "(@a \"x\x7f\")", "illegal control character in string literal"},
		{"illegal escape", `"\q"`, "illegal escape"},
		{"truncated hex escape", `"\f"`, "illegal escape"},
	} {
		t.Run(v.name, func(t *testing.T) {
			if got := lexErr(t, v.src); !strings.Contains(got, v.want) {
				t.Fatalf("got %q, want a substring match on %q", got, v.want)
			}
		})
	}

	// The accept side of the same partition, so none of the above is passing because the
	// string arm rejects everything. synthetic.
	for _, src := range []string{`"abc"`, `""`, `"\n\r\t\\\'\""`, `"\00\ff"`, `"\u{1f600}"`, `"日本"`} {
		if _, err := LexAll([]byte(src)); err != nil {
			t.Fatalf("%s must lex: %v", src, err)
		}
	}
}

// TestCommentsAreSkippedAndUnclosedOnesReject pins both comment forms.
//
// Block comments nest (lexer.mll:902–908), and the unclosed case must *reject* rather than
// run to eof silently — an unclosed comment that returns "no more tokens" makes the rest of
// a file vanish, which is a silent truncation rather than an error.
func TestCommentsAreSkippedAndUnclosedOnesReject(t *testing.T) {
	// comments.wast is the file these are cited to; the forms here are the shapes it
	// exercises, spelled minimally. derived from comments.wast:83 for the line-comment
	// terminators (LF, CR, CRLF all end a line comment).
	for _, src := range []string{
		";; comment\n(module)",
		";; comment\r(module)",
		";; comment to eof",
		"(; comment ;)(module)",
		"(; (; nested ;) ;)(module)",
		"(module) ;; trailing",
	} {
		if _, err := LexAll([]byte(src)); err != nil {
			t.Errorf("%q must lex: %v", src, err)
		}
	}
	// The partition: unclosed, nested-unclosed, and ill-formed. The middle row is the one
	// the grave was found by — closedness is the *depth*, and a check reading the trailing
	// two bytes calls this closed. The suite has no nested-unbalanced vector, so this row is
	// synthetic and says so.
	for _, c := range []struct{ src, want string }{
		{"(; unclosed", "unclosed comment"},
		{"(; (; half closed ;)", "unclosed comment"}, // synthetic: no suite vector nests unbalanced
		{"(@a (; unclosed", "unclosed comment"},
		{"(; \xff ;)", "malformed UTF-8 encoding"}, // `comment`'s own `| _` arm (lexer.mll:908)
		{";; \xff", "malformed UTF-8 encoding"},    // utf8_no_nl stops; the next position errors
	} {
		if got := lexErr(t, c.src); got != c.want {
			t.Errorf("%q gave %q, want %q — a comment scanner that skips what it cannot "+
				"classify makes the remainder of a file disappear", c.src, got, c.want)
		}
	}
	// A comment produces no token, which is the property that makes `skip` correct rather
	// than merely convenient.
	toks, err := LexAll([]byte("(; a ;) ;; b\n (module)"))
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 3 {
		t.Fatalf("got %d tokens %v, want 3 — comments must contribute none", len(toks), toks)
	}
}

// TestProductionsRejectTheEmptyInput is the boundary every matcher shares, swept rather
// than listed.
//
// A matcher that returns 0 for empty input instead of -1 makes `best` zero-valued in the
// arm loop, and `best <= 0` then panics on a legitimately empty tail rather than returning
// EOF. Sweeping is the point: this is a property of *all* of them, and any one written
// later is covered by the same loop only if the loop derives its subjects.
func TestProductionsRejectTheEmptyInput(t *testing.T) {
	matchers := map[string]func([]byte) int{
		"matchNum":               matchNum,
		"matchHexNum":            matchHexNum,
		"matchNat":               matchNat,
		"matchInt":               matchInt,
		"matchFloat":             matchFloat,
		"matchKeyword":           matchKeyword,
		"matchReserved":          matchReserved,
		"matchVarID":             matchVarID,
		"matchVarString":         matchVarString,
		"matchString":            matchString,
		"matchCharacter":         matchCharacter,
		"matchUnclosedString":    matchUnclosedString,
		"matchStringWithControl": matchStringWithControl,
		"matchStringBadEscape":   matchStringBadEscape,
		"matchUTF8Enc":           matchUTF8Enc,
		"matchLineComment":       matchLineComment,
		"matchBlockComment":      matchBlockComment,
		"matchNewline":           matchNewline,
	}
	if len(matchers) < 15 {
		t.Fatalf("only %d matchers listed; match.go has more and a short list makes this "+
			"sweep decoration", len(matchers))
	}
	for name, f := range matchers {
		if got := f(nil); got > 0 {
			t.Errorf("%s(nil) = %d, want <= 0", name, got)
		}
		if got := f([]byte{}); got > 0 {
			t.Errorf("%s([]) = %d, want <= 0", name, got)
		}
	}
	// And the arm loop's own boundary: an empty source is EOF, not a panic.
	l := NewLexer(nil)
	tok, err := l.Next()
	if err != nil || tok.Kind != EOF {
		t.Fatalf("empty source gave (%v, %v), want an EOF token and no error", tok.Kind, err)
	}
}

// TestNoArmIsShadowed is the structural half of the file, and it is the one that catches an
// arm being deleted or reordered into unreachability.
//
// TestEveryArmCanWin (lexer_test.go) asserts each arm present can win for some witness;
// this asserts the *table's shape* against the reference's — that the arms appear in the
// documented order and that the set is complete. Together they cover both directions:
// an arm that exists but never wins, and an arm that should exist and does not.
//
// The names are the transcription's own, and the order is lexer.mll's `rule token`. This is
// a hand-maintained list checked against a hand-maintained list, which would normally be
// two places to drift — the difference is that a drift here is a *failure*, and the drift
// that matters (against `lexer.mll` itself) is the vendored lane's question, not this one's.
func TestNoArmIsShadowed(t *testing.T) {
	want := []string{
		"lpar", "rpar",
		"nat", "int", "float",
		"string", "unclosed string", "control in string", "illegal escape",
		"keyword",
		"offset=", "align=",
		"$id", "$string", "$",
		"(@id", "(@string", "(@",
		"line comment", "block comment",
		"space", "newline",
		"reserved", "control", "utf8enc", "any",
	}
	if len(arms) != len(want) {
		t.Fatalf("arms has %d entries, the transcription is %d; an arm added without a "+
			"position claim, or one deleted, both land here — and deleting `keyword` is "+
			"invisible to every reject-direction test, because `reserved` matches the same "+
			"spans and produces the same message", len(arms), len(want))
	}
	for i, name := range want {
		if arms[i].name != name {
			t.Errorf("arms[%d] = %q, want %q; the order is `rule token`'s and it is "+
				"semantic — the earliest-arm tie-break reads it directly", i, arms[i].name, name)
		}
	}
	// The catch-all must be last, which is what makes Next's `best <= 0` panic unreachable
	// rather than merely unlikely.
	if arms[len(arms)-1].name != "any" {
		t.Fatalf("the last arm is %q, not the catch-all; Next's panic stops being "+
			"unreachable the moment something can match nothing", arms[len(arms)-1].name)
	}
}

// TestArmNamesAreUnique — a duplicated name would make TestEveryArmCanWin credit one arm's
// win to another, so the coverage claim silently weakens. The guard-the-guard move.
func TestArmNamesAreUnique(t *testing.T) {
	seen := map[string]int{}
	for i := range arms {
		if j, dup := seen[arms[i].name]; dup {
			t.Errorf("arms[%d] and arms[%d] are both named %q; TestEveryArmCanWin keys on "+
				"the name, so one would vouch for the other", j, i, arms[i].name)
		}
		seen[arms[i].name] = i
	}
	if len(seen) != len(arms) {
		t.Errorf("%d unique names for %d arms", len(seen), len(arms))
	}
}

// TestPanicOnNoArmMatchIsUnreachableByConstruction probes the one panic in this package.
//
// *A green that survives the bug it names is a control in name only* — so rather than
// asserting the panic never fires (which every input already does), this asserts the
// property that makes it unreachable: the final arm matches any single byte, for all 256.
// If that stops being true the panic becomes reachable and this fails, which is the
// difference between a documented invariant and a hoped-for one.
func TestPanicOnNoArmMatchIsUnreachableByConstruction(t *testing.T) {
	last := arms[len(arms)-1]
	for c := range 256 {
		if n := last.length(nil, []byte{byte(c)}); n != 1 {
			t.Errorf("the catch-all arm returned %d for byte %#02x; Next panics when no arm "+
				"matches, and that panic is only unreachable because this arm always does", n, c)
		}
	}
	// Every byte reaches *some* arm with a positive length, which is the same claim read
	// from the loop's side.
	for c := range 256 {
		best := -1
		for i := range arms {
			if n := arms[i].length(nil, []byte{byte(c)}); n > best {
				best = n
			}
		}
		if best <= 0 {
			t.Errorf("byte %#02x matches no arm with positive length; Next would panic", c)
		}
	}
}
