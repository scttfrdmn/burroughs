package text

import (
	"errors"
	"reflect"
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
// The suite matched by substring when this was written, so wrapping would not have changed a
// verdict — which is exactly why it needed a test rather than trust. Under prefix matching (ADR
// 0045) wrapping *is* a verdict change, so the row this pins is now oracle-backed as well. *An error from the wrong layer is evidence about
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

// TestImportKindWordsMatchTheReference pins the import-ordering message's five words.
//
// **This test used to assert a two-vocabularies fact that does not exist**, and the retired
// paragraph is kept because it is the defect stated as the rule (grave #120):
//
//	The same index space is called `function` by the import-ordering message
//	(parser.mly:1354) and `func` by the duplicate message (`bind_func` → `bind_abs
//	"function"` … and the suite wants "duplicate func"). Two words for one space, both
//	suite-pinned, and a shared constant would be wrong for one of them.
//
// The parenthetical contains its own refutation: `bind_func` *is* `bind_abs "function"`, so the
// reference says `duplicate function $foo` and there is one vocabulary. What the suite wants is
// the string `"duplicate func"`, which the harness matches by **prefix** (ADR 0045) — so
// it is satisfied by `duplicate function $foo` as a prefix, and both words scored green. The
// duplicate half of the assertion moved to TestSpaceKindWordsMatchTheReference, which checks all
// eleven spaces against the authority instead of one space against a suite string that cannot
// distinguish them.
func TestImportKindWordsMatchTheReference(t *testing.T) {
	if got := importFunc.String(); got != "function" {
		t.Errorf("importFunc = %q, want %q (parser.mly:1354, imports.wast:677 expects "+
			"\"import after function\")", got, "function")
	}
}

// TestSpaceKindWordsMatchTheReference pins every category word against parser.mly.
//
// **The whole domain, not the spaces this PR touches** — the reference defines eleven
// `bind_*`/`lookup` pairs and all eleven are listed, because a control scoped to today's callers
// inherits today's blind spot, and this defect *was* today's blind spot three times over. Three
// of these eleven were wrong when the test was written: `func`, `data`, `elem` (grave #120).
//
// Why a hand table is the right instrument here and a machine extraction is not: these are
// eleven string literals in two adjacent helper blocks, and the extraction would have to parse
// OCaml `let` bindings to recover them — more transcription risk in the extractor than in the
// table. Contrast keywordgen's 589 arms, where the ratio runs the other way. The mitigation is
// that the table is *complete over the reference's helpers*, so a reviewer diffs eleven lines
// against parser.mly:152-163 and :187-198 rather than trusting a sample.
//
// The two trailing-space quirks are deliberately **not** in this table: `lookup "label "` (:161)
// and `lookup "field "` (:163) differ from their bind words, and that asymmetry belongs to the
// lookup site. lookupLabel writes `unknown label  %s` with the doubled space itself, and
// TestUnknownLabelMessageMatchesTheReference is what holds it.
func TestSpaceKindWordsMatchTheReference(t *testing.T) {
	// Every category literal the reference passes to bind_abs/bind_rel/bind and to lookup, read
	// from parser.mly at the pinned revision. The cite is the bind side; the lookup side agrees
	// for all nine absolute spaces (:152-160).
	for _, tc := range []struct {
		kind spaceKind
		want string
		cite string
	}{
		{spaceType, "type", "bind_type :187 / lookup :152"},
		{spaceTag, "tag", "bind_tag :188 / lookup :153"},
		{spaceGlobal, "global", "bind_global :189 / lookup :154"},
		{spaceMemory, "memory", "bind_memory :190 / lookup :155"},
		{spaceTable, "table", "bind_table :191 / lookup :156"},
		{spaceFunc, "function", "bind_func :192 / lookup :157 — NOT `func`, grave #120"},
		{spaceData, "data segment", "bind_data :193 / lookup :158 — two words, grave #120"},
		{spaceElem, "elem segment", "bind_elem :194 / lookup :159 — two words, grave #120"},
		{spaceLocal, "local", "bind_local :195 / lookup :160"},
		{spaceField, "field", "bind_field :198"},
		{spaceLabel, "label", "bind_label :196 (lookup adds a trailing space, :161)"},
	} {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("spaceKind(%d) = %q, want %q (%s)\n\tthe suite cannot see this: "+
				"expected strings are matched by prefix, so a short word is a passing "+
				"prefix of the reference's real one", int(tc.kind), got, tc.want, tc.cite)
		}
	}
	// Vacuity's cousin: the table above is a list, so it cannot notice a kind it omits. Bound the
	// domain by its extent instead of by this table's length.
	if got := int(spaceLabel) + 1; got != 12 {
		t.Errorf("spaceKind has %d values including spaceUnset, table covers 11 plus unset — "+
			"a new space needs a row above, and this is what says so", got)
	}
}

// TestUnsetSpaceKindIsNotASpace pins that the zero value names no space.
//
// `context` is built by newContext, but a `space` written as a literal elsewhere (the per-struct
// field space, or any test) gets the zero value — and if `spaceType` sat at ordinal 0 that space
// would print `duplicate type $f` for a func. The marker is unmistakable in a message, which is
// the point: an omission should look like an omission rather than like a plausible word.
func TestUnsetSpaceKindIsNotASpace(t *testing.T) {
	var s space
	if err := s.bindAbs(Token{Kind: VarTok, Text: "$f"}, "$f"); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	err := s.bindAbs(Token{Kind: VarTok, Text: "$f"}, "$f")
	if err == nil {
		t.Fatal("second bind of $f accepted; want a duplicate error")
	}
	if !strings.Contains(err.Error(), "<space kind unset>") {
		t.Errorf("zero-value space reported %q; want the unset marker, so a space that was "+
			"never given its kind cannot impersonate one that was", err.Error())
	}
}

// TestEverySpaceHasItsKind walks context's fields, so a space added later cannot skip newContext.
//
// Derived from the struct rather than from a list of the nine spaces, for the reason every
// control in this repo is: an enumeration freezes at the moment of authorship, and the failure
// mode here is silent — a tenth space left at spaceUnset binds and looks up perfectly well, and
// only its error messages are wrong, in the direction no vector covers.
func TestEverySpaceHasItsKind(t *testing.T) {
	c := newContext()
	v := reflect.ValueOf(c)
	spaceStruct := reflect.TypeOf(space{})
	var checked int
	for i := range v.NumField() {
		if v.Type().Field(i).Type != spaceStruct {
			continue
		}
		checked++
		// Int() rather than Interface(): reflect refuses to hand out an unexported field as an
		// any, and spaceKind is an int, so its numeric value is readable without it.
		if spaceKind(v.Field(i).FieldByName("kind").Int()) == spaceUnset {
			t.Errorf("context.%s has no spaceKind — add it to newContext",
				v.Type().Field(i).Name)
		}
	}
	if checked != 9 {
		t.Errorf("walked %d space fields, want 9 — if a space was added, this count moves "+
			"with it deliberately; if it dropped, the loop stopped finding them and every "+
			"assertion above went vacuous", checked)
	}
}

// TestSpaceForCoversEveryImportKind walks the `importKind` enum so `spaceFor`'s default arm cannot
// silently absorb a kind.
//
// `spaceFor` ends in `default: return &c.funcs` rather than in a fifth `case`, which is the arm that
// makes a *sixth* kind route to the func space with nothing saying so — the enum's extent is the only
// thing that can notice. Derived from `importTag..importFunc` rather than listed, and the identity is
// checked by pointer against the field it must name: comparing `String()` or a kind byte would let
// two arms return the same space and still agree.
//
// The distinctness assertion is the load-bearing half. Five arms returning five pointers is what the
// default arm's safety rests on, and a `case importGlobal: return &c.globals` mistyped as
// `&c.memories` produces a perfectly working parser that resolves every `(export "e" (global $g))`
// against the memory space — an accept-direction defect (§9 G-3), so no vector can see it.
func TestSpaceForCoversEveryImportKind(t *testing.T) {
	c := newContext()
	want := map[importKind]*space{
		importTag:    &c.tags,
		importGlobal: &c.globals,
		importMemory: &c.memories,
		importTable:  &c.tables,
		importFunc:   &c.funcs,
	}
	seen := map[*space]importKind{}
	var covered int
	for k := importTag; k <= importFunc; k++ {
		covered++
		got := c.spaceFor(k)
		exp, ok := want[k]
		if !ok {
			t.Errorf("importKind %d (%s) has no expected space here; a new kind needs a row, and "+
				"until it has one spaceFor's default arm routes it to funcs", int(k), k)
			continue
		}
		if got != exp {
			t.Errorf("spaceFor(%s) returned the wrong space — an export or import of a %s would "+
				"resolve against another space's names, which is green on every board because "+
				"the suite has no accept-direction vector for it", k, k)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("spaceFor(%s) and spaceFor(%s) return the same space; the default arm's "+
				"safety rests on the five arms being five distinct spaces", k, prev)
		}
		seen[got] = k
	}
	// The extent, not the map's length: a shrunken enum would make the loop cover less while every
	// assertion inside it still passed. Five is the count at parser.mly:1258-1263.
	if covered != 5 {
		t.Errorf("walked %d importKinds, want 5 (externidx's arms, parser.mly:1258-1263) — the "+
			"loop bounds are the enum's extent, so this moving means importFunc is no longer last "+
			"and defCount's array bound is wrong too", covered)
	}
}

// TestExportResolvesInEverySpace is the reject direction of the export field's five arms.
//
// `TestModuleAcceptDirection` covers the accept side one arm at a time, and it cannot see the defect
// that matters: **which space each arm looks in**. An arm wired to the wrong space accepts every
// module whose name happens to exist in *some* space, so the accept table stays green — which is how
// its `(export "e" (global $g))` row came to assert that an undefined `$g` is fine (it is not;
// parser.mly:1259 is `$3 c global`, a `lookup`). The row was green because the index was discarded
// unresolved, so the table was asserting the absence of a check as a grammar fact.
//
// Two halves per space, because either alone passes a wrong wiring:
//
//   - an *unbound* name in that space must be rejected with the reference's own category word, which
//     is what proves a lookup happened at all;
//   - a name bound in a *different* space must still be rejected, which is what proves the lookup
//     went to the right space. `(global $x i32) (export "e" (func $x))` is the shape: `$x` exists,
//     and a func-arm reading the global space would resolve it and emit an export of func 0.
//
// The second half is the one no vector covers and the one a hand review reads straight past.
func TestExportResolvesInEverySpace(t *testing.T) {
	// One definition per space, and the *other* four spaces' arms must all reject its name. Keyed
	// by the keyword the externidx arm takes so the table reads as the grammar does.
	spaces := []struct {
		kw   string // the externidx keyword (parser.mly:1258-1263)
		def  string // a module field defining `$x` in that space
		word string // the reference's lookup category for it (parser.mly:153-157)
	}{
		{"tag", `(tag $x)`, "tag"},
		{"global", `(global $x i32)`, "global"},
		{"memory", `(memory $x 1)`, "memory"},
		{"table", `(table $x 1 funcref)`, "table"},
		{"func", `(func $x)`, "function"},
	}

	for _, s := range spaces {
		// Half one: nothing defines $x anywhere.
		src := `(module (export "e" (` + s.kw + ` $x)))`
		err := ReadModule([]byte(src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted an unbound $x; parser.mly:1258-1263 makes every "+
				"externidx arm a lookup, so this arm is resolving nothing", src)
		} else if want := "unknown " + s.word + " $x"; !strings.Contains(err.Error(), want) {
			t.Errorf("ReadModule(%q) = %q, want it to contain %q — the category word is the "+
				"space's own (grave #120), and the suite cannot tell a short word from the "+
				"reference's because it matches by prefix", src, err.Error(), want)
		}

		// Half two: $x is defined, but in each of the other four spaces.
		for _, other := range spaces {
			if other.kw == s.kw {
				continue
			}
			src := `(module ` + other.def + ` (export "e" (` + s.kw + ` $x)))`
			err := ReadModule([]byte(src))
			if err == nil {
				t.Errorf("ReadModule(%q) accepted: `$x` is defined in the %s space and this is a "+
					"%s export, so the arm looked in the wrong space.\n\tThe module encodes to a "+
					"valid image naming the wrong thing — an accept-direction defect no vector "+
					"can see (§9 G-3), which is this test's whole subject",
					src, other.word, s.kw)
				continue
			}
			if want := "unknown " + s.word + " $x"; !strings.Contains(err.Error(), want) {
				t.Errorf("ReadModule(%q) = %q, want it to contain %q — it was rejected, but for "+
					"the wrong reason, and a right verdict from a wrong layer is evidence about "+
					"where structure was lost", src, err.Error(), want)
			}
		}
	}

	// Vacuity floor: every assertion above is inside the loop, so an emptied table passes by
	// iterating nothing. Five is externidx's arm count, and the inner loop makes 5*4 cross-space
	// cases — the count is asserted rather than the table's length so a duplicated row cannot
	// stand in for a missing one.
	if len(spaces) != 5 {
		t.Fatalf("table covers %d spaces, want externidx's 5 (parser.mly:1258-1263)", len(spaces))
	}
	seen := map[string]bool{}
	for _, s := range spaces {
		if seen[s.kw] {
			t.Errorf("the table has two %s rows; a duplicate fills the count while leaving a "+
				"space uncovered", s.kw)
		}
		seen[s.kw] = true
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
	s := space{kind: spaceFunc}
	s.bindAnon()
	if s.count != 1 {
		t.Fatalf("after one anonymous definition count = %d, want 1", s.count)
	}
	if err := s.bindAbs(Token{Text: "$f"}, "$f"); err != nil {
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
	m := space{kind: spaceFunc}
	if err := m.bindAbs(Token{Text: "$a"}, "$a"); err != nil {
		t.Fatalf("bind $a: %v", err)
	}
	if err := m.bindAbs(Token{Text: "$b"}, "$b"); err != nil {
		t.Fatalf("bind $b: %v", err)
	}
	m.bindAnon()
	if err := m.bindAbs(Token{Text: "$c"}, "$c"); err != nil {
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
