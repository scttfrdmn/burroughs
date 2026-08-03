package text

import (
	"errors"
	"strings"
	"testing"
)

// TestCursorPeekAtEOFIsStable pins the fact peek() relies on: the cursor's slice ends with
// EOF, and reading at EOF neither panics nor advances.
//
// Asserted rather than assumed because it is a fact about *another function*, and this test
// is why the other function now exists. peek() has no bounds branch, which is correct only
// while the lexing call appends EOF; the first draft called `LexAll` and *documented* that
// LexAll does so. It does not — it stops at EOF and drops it — and this test panicked with
// `index out of range [3] with length 3` on its first execution. `lexToEOF` was carved out in
// response, so the promise now lives in the name of the function keeping it.
//
// The general form is the falsifiability discipline pointed at premises rather than at
// assertions: *a premise about another function is checked by a test or it is a wish*, and
// the test that names the premise is where the cheque gets cashed.
func TestCursorPeekAtEOFIsStable(t *testing.T) {
	c, err := newCursor([]byte(`(func)`))
	if err != nil {
		t.Fatalf("newCursor: %v", err)
	}

	// Drain past the end. next() must not advance at EOF, so this terminates; if it ever
	// does advance, the slice index panics and the test fails loudly rather than hanging.
	for range 20 {
		c.next()
	}
	if got := c.peek(); got.Kind != EOF {
		t.Errorf("peek after draining = %v, want EOF", got.Kind)
	}
	if got := c.next(); got.Kind != EOF {
		t.Errorf("next at EOF = %v, want EOF repeatedly", got.Kind)
	}
}

// TestCursorNextAdvances is the progress property at the cursor layer.
//
// *Parsers prove progress, they don't assume it* (grave #18). The cursor is where every
// production's loop gets its advance, so a next() that failed to move would hang every one
// of them — and the failure would be a timeout, not an error.
func TestCursorNextAdvances(t *testing.T) {
	c, err := newCursor([]byte(`(func $f (export "e"))`))
	if err != nil {
		t.Fatalf("newCursor: %v", err)
	}
	for prev := -1; ; {
		if c.pos <= prev {
			t.Fatalf("cursor did not advance at pos %d (token %v)", c.pos, c.peek().Kind)
		}
		prev = c.pos
		if c.at(EOF) {
			break
		}
		c.next()
	}
}

// TestCursorReportsLexErrorsUnwrapped pins that a malformed lexeme surfaces as the lexer's
// own error, not wrapped in parser prose.
//
// The suite matches by substring so wrapping would not change a verdict — which is exactly
// why it needs a test rather than trust. *An error from the wrong layer is evidence about
// where structure was lost*, and an error wearing the wrong layer's prose is that evidence
// falsified.
func TestCursorReportsLexErrorsUnwrapped(t *testing.T) {
	_, err := newCursor([]byte(`(@"\ef")`))
	if err == nil {
		t.Fatal("newCursor accepted a malformed annotation; the lexer rejects it")
	}
	if got := err.Error(); got != "malformed UTF-8 encoding" {
		t.Errorf("lex error = %q, want exactly %q — a parser prefix would make the lexer's "+
			"verdict testify to the parser's layer", got, "malformed UTF-8 encoding")
	}
}

// TestImportAfterDefinitionNamesTheNearestDefinition is the ordering check's real contract,
// and **every case here is synthetic**.
//
// Reason it must be: all 16 suite vectors are `(<one definition>) (import …)` — one
// definition kind, one import — so they cannot distinguish which definition a
// multi-definition module names, which import position reports, or whether a definition
// *after* the last import counts. Three degrees of freedom, zero vectors. The suite scores
// 16/16 for at least three different implementations, one of which this file's first draft
// was.
//
// The authority is parser.mly:1349-1354. Three facts read off one arm, and **the position is
// checked as well as the message**, because two of the three readings differ only in the
// offset — a message-text-only test scores them equal. Each step is given a distinct offset so
// the reported one identifies which import spoke.
//
//	if funcs <> [] && m.imports <> [] then
//	  error (List.hd m.imports).at "import after function definition"
//
// Derived from the executable across three traces, two of which were wrong. Draft one folded
// the kind backwards; draft two got every kind right and reported at the *last* import instead
// of the first, which no message string can reveal.
func TestImportAfterDefinitionNamesTheNearestDefinition(t *testing.T) {
	for _, tc := range []struct {
		name string
		// steps is the sequence of module fields, as (kind, isImport) steps. Step i is given
		// offset i*10, so wantOffset names which step the error points at.
		steps      []step
		want       string
		wantOffset int
		why        string
	}{
		{
			name:       "single definition then import",
			steps:      []step{def(importFunc), imp()},
			want:       "import after function definition",
			wantOffset: 10,
			why:        "the suite's shape — imports.wast:677, the only one the 16 cover",
		},
		{
			name:       "two definitions, the latest qualifying one is named",
			steps:      []step{def(importFunc), def(importGlobal), imp()},
			want:       "import after global definition",
			wantOffset: 20,
			why: "synthetic: parser.mly:1349-1354 forces the inner arm first and error raises " +
				"immediately (:11), so `global` wins. A fixed kind-priority scan says " +
				"`function` and still passes all 16 vectors.",
		},
		{
			name:       "the FIRST import after that definition is the position",
			steps:      []step{def(importFunc), def(importGlobal), imp(), imp()},
			want:       "import after global definition",
			wantOffset: 20,
			why: "synthetic, and the case that separates the two surviving readings: `List.hd " +
				"m.imports` is the nearest import following the field, so offset 20 not 30. " +
				"Recording at every import and overwriting gets the KIND right and the " +
				"OFFSET wrong — invisible to any message-text check, which is why this " +
				"asserts the offset (grave #36's half of the error nobody reads).",
		},
		{
			name:       "a later definition takes over the report",
			steps:      []step{def(importFunc), imp(), def(importGlobal), imp()},
			want:       "import after global definition",
			wantOffset: 30,
			why: "synthetic: the global arm is deeper, so it raises first and points at its " +
				"own first following import. Returning eagerly at the first import says " +
				"`function` at offset 10.",
		},
		{
			name:       "a definition after the last import does not count",
			steps:      []step{def(importFunc), imp(), def(importGlobal)},
			want:       "import after function definition",
			wantOffset: 10,
			why: "synthetic: the global arm's suffix holds no imports, so it stays quiet. A " +
				"check that tested at the end against the last definition seen says `global`.",
		},
		{
			name:  "imports only",
			steps: []step{imp(), imp()},
			want:  "",
			why: "synthetic: legal. A count-based test reads the import's own index binding " +
				"as a definition and rejects this — which is why the check is not on space.count.",
		},
		{
			name:  "import before definition",
			steps: []step{imp(), def(importFunc)},
			want:  "",
			why:   "synthetic: legal, and the direction the whole check exists to permit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c context
			for i, s := range tc.steps {
				tok := Token{Kind: LParen, Offset: i * 10, Line: 1}
				if s.isImport {
					c.noteImport(tok)
				} else {
					c.markDefined(s.kind)
				}
			}

			err := c.importOrderErr()
			switch {
			case tc.want == "":
				if err != nil {
					t.Errorf("rejected with %v; it is LEGAL.\n\t%s", err, tc.why)
				}
				return
			case err == nil:
				t.Errorf("accepted; want %q.\n\t%s", tc.want, tc.why)
				return
			case err.Error() != tc.want:
				t.Errorf("error = %q, want %q.\n\t%s", err.Error(), tc.want, tc.why)
			}

			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error is %T, want *text.Error carrying a position", err)
			}
			if e.Offset != tc.wantOffset {
				t.Errorf("reported at offset %d, want %d — the message is right and the "+
					"position is wrong, which no suite vector can see.\n\t%s",
					e.Offset, tc.wantOffset, tc.why)
			}
		})
	}
}

type step struct {
	kind     importKind
	isImport bool
}

func def(k importKind) step { return step{kind: k} }
func imp() step             { return step{isImport: true} }

// TestImportKindWordsMatchTheReference pins the two-vocabularies fact.
//
// The same index space is called `function` by the import-ordering message
// (parser.mly:1354) and `func` by the duplicate message (`bind_func` → `bind_abs "function"`
// … and the suite wants "duplicate func"). Two words for one space, both suite-pinned, and a
// shared constant would be wrong for one of them.
func TestImportKindWordsMatchTheReference(t *testing.T) {
	if got := importFunc.String(); got != "function" {
		t.Errorf("importFunc = %q, want %q (parser.mly:1354, imports.wast:677 expects "+
			"\"import after function\")", got, "function")
	}
	// And the duplicate side, which is the other word for the same space.
	var s space
	tok := Token{Kind: VarTok, Text: "$f"}
	if err := s.bindAbs("func", tok, "$f"); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	err := s.bindAbs("func", tok, "$f")
	if err == nil {
		t.Fatal("second bind of $f accepted; want a duplicate error")
	}
	if !strings.HasPrefix(err.Error(), "duplicate func ") {
		t.Errorf("duplicate error = %q, want a %q prefix — the suite (func.wast) matches on "+
			"the short word while the ordering message uses the long one", err.Error(),
			"duplicate func ")
	}
}

// TestBindAbsCountsAnonymousDefinitions pins that **every** definition occupies an index,
// named or not.
//
// `(func) (func $f)` puts $f at index 1, not 0. Synthetic: no suite vector in this child's
// stratum reads an index back, because indices are the validator's business — but the
// binding is this layer's, and a count that skipped entries would be wrong in a way #63/#64
// would inherit silently.
//
// **Both arms of the partition, because the first draft had only one and a mutant survived
// it.** The draft bound anon-then-named and checked the named one landed at 1, which holds
// even if `bindAbs` never advances the count — bindAnon's increment carries it. Deleting
// `s.count++` from bindAbs left the whole suite green. That is the grave-#34 shape exactly: a
// test named for a property ("definitions occupy indices") checked against one of its cases
// rather than against the partition, and passing while covering half of what its name claims.
// The count is asserted after every step now, so a missing increment on either path fails.
func TestBindAbsCountsAnonymousDefinitions(t *testing.T) {
	var s space
	s.bindAnon()
	if s.count != 1 {
		t.Fatalf("after one anonymous definition count = %d, want 1", s.count)
	}
	if err := s.bindAbs("func", Token{Text: "$f"}, "$f"); err != nil {
		t.Fatalf("bind $f: %v", err)
	}
	if got := s.names["$f"]; got != 1 {
		t.Errorf("$f bound at index %d, want 1 — the anonymous (func) occupies 0 "+
			"(parser.mly:92, count is not len(names))", got)
	}
	if s.count != 2 {
		t.Errorf("after anon+named count = %d, want 2; a *named* binding that does not "+
			"advance the count is the arm the first draft of this test missed entirely",
			s.count)
	}

	// The mirror: named first, so the next two indices depend on bindAbs having advanced.
	var m space
	if err := m.bindAbs("func", Token{Text: "$a"}, "$a"); err != nil {
		t.Fatalf("bind $a: %v", err)
	}
	if err := m.bindAbs("func", Token{Text: "$b"}, "$b"); err != nil {
		t.Fatalf("bind $b: %v", err)
	}
	m.bindAnon()
	if err := m.bindAbs("func", Token{Text: "$c"}, "$c"); err != nil {
		t.Fatalf("bind $c: %v", err)
	}
	for name, want := range map[string]uint32{"$a": 0, "$b": 1, "$c": 3} {
		if got := m.names[name]; got != want {
			t.Errorf("%s bound at index %d, want %d — indices must advance across both "+
				"named and anonymous bindings, in either order", name, got, want)
		}
	}
	if m.count != 4 {
		t.Errorf("after three named and one anonymous count = %d, want 4", m.count)
	}
}

// TestMultipleStartSections pins the third grammar-level check.
func TestMultipleStartSections(t *testing.T) {
	var c context
	tok := Token{Kind: LParen}
	if err := c.checkStart(tok); err != nil {
		t.Fatalf("first (start …): %v", err)
	}
	err := c.checkStart(tok)
	if err == nil {
		t.Fatal("second (start …) accepted; want `multiple start sections`")
	}
	if err.Error() != "multiple start sections" {
		t.Errorf("error = %q, want %q (parser.mly:1372)", err.Error(), "multiple start sections")
	}
}

// TestDecodeSitesRejectInvalidUTF8 is the reject direction at the two positions.
//
// The accept direction lives in utf8position_test.go, at LexAll, where the wrong fix is
// reachable. This half is the narrow one: given a token in a `name` or `var` position with
// invalid bytes, the site rejects with the reference's message.
//
// **The tokens are lexed, not hand-built**, and that is a correction rather than a preference.
// The first draft passed `Token{Kind: VarTok, Text: string(bad)}` and decodedVar checked Text —
// test and code agreeing on a field the lexer does not put decoded bytes in, so the assertion
// passed over a production that could never fire on real input. A hand-built token makes the
// test's premise about the lexer unfalsifiable; lexing the source puts the field convention
// where it belongs, in the lexer, and the escape `\ef` is how invalid bytes get written in wat
// source at all. (Sibling of the LexAll-appends-EOF premise, same session.)
func TestDecodeSitesRejectInvalidUTF8(t *testing.T) {
	lexOne := func(src string) Token {
		t.Helper()
		toks, err := LexAll([]byte(src))
		if err != nil {
			t.Fatalf("LexAll(%q) = %v; the invalid bytes are the *parser's* to reject, not "+
				"the lexer's — see utf8position_test.go", src, err)
		}
		if len(toks) != 1 {
			t.Fatalf("LexAll(%q) gave %d tokens, want 1", src, len(toks))
		}
		return toks[0]
	}

	if _, err := decodedName(lexOne(`"\ef"`)); err == nil {
		t.Error("decodedName accepted invalid UTF-8; parser.mly:46-47 rejects")
	} else if err.Error() != "malformed UTF-8 encoding" {
		t.Errorf("decodedName error = %q, want %q", err.Error(), "malformed UTF-8 encoding")
	}

	if _, err := decodedVar(lexOne(`$"\ef"`)); err == nil {
		t.Error("decodedVar accepted invalid UTF-8; parser.mly:49-52 rejects")
	} else if err.Error() != "malformed UTF-8 encoding" {
		t.Errorf("decodedVar error = %q, want %q — the *vector* id.wast:31 expects the "+
			"shorter \"malformed UTF-8\", which this contains; the reference's message at "+
			"both sites is the long one", err.Error(), "malformed UTF-8 encoding")
	}

	// And the accept direction at these two functions specifically, so the pair cannot be
	// satisfied by a function that rejects everything. Both spellings of an identifier, because
	// `$f` and `$"f"` take different lexer arms (lexer.mll:815 vs :816) and only the second
	// unescapes — a decode reading the wrong field passes one and not the other.
	//
	// **The name's accept half asserts the decoded *value*, which it could not before #8**: the
	// function returned only an error, so the strongest available assertion was "did not reject" —
	// a check that a function returning nil unconditionally would pass. Now that the import section
	// spends the string, the escape's decoding is checkable, and `"\41"` is the case that
	// distinguishes a real decode from `t.Text`: the token's text is six characters of source and
	// the name is one byte, `A`. Without an escape in it, a function returning the raw spelling
	// would pass.
	//
	// The empty row was its own assertion before this table and keeps its citation: empty is valid
	// UTF-8 and a legal name — `(import "" "" (func))` is the suite's own shape at
	// imports.wast:677 — so a check written as `utf8.Valid(v) && len(v) > 0` would reject it, and
	// the vector that would catch that expects a *different* error.
	//
	// **That citation stays in this prose and must not be moved onto the row**, which is a ruling
	// this table earned by breaking: with the citation written as a trailing comment on the empty
	// row, it matched `citedRow` and `TestEveryFixtureFileIsChecked` failed, correctly, because
	// this file is in no provenance checker's list. Registering it would have been the worse
	// repair — these rows are
	// *(source, decoded value)*, so the text checker's layout would read `""` as an expected *error
	// string*, which is the positional-convention confusion its own comment warns about, and an
	// empty expect agrees with every command by containment. A registration that vouches for
	// nothing is worse than an absence. The citation is an argument about legality, not a
	// transcription of the vector's bytes, so it lives where prose citations live and is reviewed
	// by eyes.
	for _, tc := range []struct{ src, want string }{
		{`"ok"`, "ok"},
		{`"\41"`, "A"},
		{`""`, ""},
	} {
		got, err := decodedName(lexOne(tc.src))
		if err != nil {
			t.Errorf("decodedName(%s) rejected valid UTF-8: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("decodedName(%s) = %q, want %q", tc.src, got, tc.want)
		}
	}
	for _, src := range []string{`$f`, `$"f"`, `$"\41"`} {
		if _, err := decodedVar(lexOne(src)); err != nil {
			t.Errorf("decodedVar(%s) rejected valid UTF-8: %v", src, err)
		}
	}
}
