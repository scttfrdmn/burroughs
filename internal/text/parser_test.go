package text

import (
	"errors"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestEveryKindConstantIsInTheTable asserts every keyword kind the parser matches on is a kind
// the generated table actually produces.
//
// The failure it exists to catch is silent by construction: `atKeyword("FNUC")` compiles, is
// always false, and turns a production into one that never fires — and because #62's surface is
// error-only, a production that never fires still returns an error, just the wrong one. No
// expected-string check sees the difference between "rejected for the right reason" and
// "rejected because a branch was unreachable".
//
// Derived from the table rather than from a list of what the table should contain, per *scope
// controls to the space*: the domain grows when keywords.go is regenerated.
func TestEveryKindConstantIsInTheTable(t *testing.T) {
	// Vacuity check first. An empty table agrees with everything, and a comparison against an
	// empty set succeeds — the degenerate case that no amount of breaking the assertion finds.
	if len(keywords) < 500 {
		t.Fatalf("the keyword table holds %d entries, want at least 500; the generated table "+
			"is empty or truncated and every assertion below would pass vacuously", len(keywords))
	}
	inTable := map[keywordKind]string{}
	for kw, kind := range keywords {
		inTable[kind] = kw
	}
	if len(inTable) < 150 {
		t.Fatalf("the table yields %d distinct kinds, want at least 150 (header says 173)",
			len(inTable))
	}
	if len(parserKinds) < 45 {
		t.Fatalf("parserKinds holds %d entries, want at least 45; a truncated list would make "+
			"this test assert nothing about the constants it skipped", len(parserKinds))
	}

	for _, k := range parserKinds {
		if _, ok := inTable[k]; !ok {
			t.Errorf("kind %q is matched by the parser but no keyword in the generated table "+
				"produces it — the production matching it can never fire", k)
		}
	}
}

// TestParserKindsListsEveryConstant guards the one escape hatch in the control above: a constant
// declared in kinds.go and left out of parserKinds is unchecked.
//
// Reads the source, because Go offers no reflection over package-level constants. Same manoeuvre
// as TestEverySkipSiteIsLicensed — a rule requiring everything go through one door needs
// something asserting that it does, or the mechanism has the shape it exists to forbid.
func TestParserKindsListsEveryConstant(t *testing.T) {
	b, err := os.ReadFile("kinds.go")
	if err != nil {
		t.Fatalf("read kinds.go: %v", err)
	}
	src := string(b)

	// Every `kwFoo keywordKind = "BAR"` declaration.
	decls := reKindDecl.FindAllStringSubmatch(src, -1)
	if len(decls) < 45 {
		t.Fatalf("found %d kind declarations in kinds.go, want at least 45 — the regexp has "+
			"drifted from the source and this test would pass while checking nothing",
			len(decls))
	}

	// And the parserKinds literal's body, so membership is a text question about the same file.
	body := reParserKindsBody.FindStringSubmatch(src)
	if body == nil {
		t.Fatal("could not find the parserKinds literal in kinds.go")
	}
	for _, d := range decls {
		name := d[1]
		if !strings.Contains(body[1], name) {
			t.Errorf("constant %s is declared but absent from parserKinds, so "+
				"TestEveryKindConstantIsInTheTable never checks it", name)
		}
	}
}

// nonKeywordSources is one lexable source per non-keyword TokenKind, so the predicate sweeps
// below cover the whole token vocabulary and not just the keyword half of it.
//
// **This list exists because leaving it out cost a survivor.** The first draft of the two
// agreement controls swept `keywords`, which is the space `atHeaptypeStart`'s twelve keyword arms
// live in — and heaptype's *thirteenth* arm is `idx`, whose tokens are NatTok and VarTok and are
// not keywords at all. Deleting `p.c.at(NatTok) || p.c.at(VarTok)` from the predicate left both
// controls green: `(ref $t)` would be rejected as invalid, and the sweep was scoped to a space
// that could not contain the disagreement. The scope-controls-to-the-space law with a blind spot
// of exactly one token class, which is how that law fails when it fails — not by enumerating the
// wrong members, but by picking a space one dimension too small.
//
// Kept honest by TestNonKeywordSourcesCoverEveryTokenKind below, which derives the domain from
// TokenKind's own extent rather than trusting this list to be complete.
//
// **Guarded rather than derived, and that is a limit rather than a preference.** The obvious
// derivation would read the lexer's `arms` table, which already pairs every token shape with its
// kind — but an arm holds a *matcher*, and a matcher recognizes rather than generates: there is no
// way to ask `matchFloat` for a float. Examples cannot be derived from acceptors. So the list is
// authored and its *completeness* is what gets derived, which is the strongest available form of
// the rule here: the enumeration is allowed, and the thing an enumeration gets wrong — going stale
// against the space — is machine-checked.
var nonKeywordSources = []string{
	"(", ")", "0", "-1", "1.5", `"s"`, "$v", "offset=0", "align=1", "",
}

// agreementSources yields every single-token source the predicate sweeps run over: the whole
// generated keyword table plus one source per non-keyword kind.
func agreementSources(t *testing.T) []string {
	t.Helper()
	if len(keywords) < 500 {
		t.Fatalf("keyword table holds %d entries; the sweep below would be vacuous", len(keywords))
	}
	srcs := make([]string, 0, len(keywords)+len(nonKeywordSources))
	for kw := range keywords {
		srcs = append(srcs, kw)
	}
	return append(srcs, nonKeywordSources...)
}

// TestNonKeywordSourcesCoverEveryTokenKind is the vacuity check on the list above.
//
// A hand-written list of sources is exactly the enumeration the discipline distrusts, so it is
// checked against the *type's* extent: every TokenKind value the String method distinguishes must
// be produced by something the sweeps feed in. A kind added upstream fails here rather than
// silently narrowing two controls that look like they cover everything.
func TestNonKeywordSourcesCoverEveryTokenKind(t *testing.T) {
	// KeywordTok is covered by the keyword table half of agreementSources; every other kind
	// must come from nonKeywordSources.
	seen := map[TokenKind]bool{KeywordTok: true}
	for _, src := range nonKeywordSources {
		toks, err := lexToEOF([]byte(src))
		if err != nil {
			t.Fatalf("lex %q: %v", src, err)
		}
		seen[toks[0].Kind] = true
	}
	for k := LParen; ; k++ {
		if !seen[k] {
			t.Errorf("TokenKind %v (%d) is produced by no source in nonKeywordSources, so "+
				"the predicate sweeps say nothing about it", k, int(k))
		}
		if k == EOF {
			break
		}
	}
	if len(seen) < 11 {
		t.Errorf("only %d token kinds covered; TokenKind has 11 values", len(seen))
	}
}

// TestHeaptypeStartAgreesWithHeaptype pins the predicate against the consumer.
//
// atHeaptypeStart and heaptype list the same twelve keywords in two places, which is the drift
// shape #33 was filed about. The control is not "the two lists match textually" — it is that for
// **every single-token source**, the predicate's answer and the production's success agree.
// Scoped to the space: a thirteenth heap type added upstream is covered without editing this
// test, and so is the `idx` arm, whose tokens are not keywords — see nonKeywordSources for what
// leaving them out cost.
func TestHeaptypeStartAgreesWithHeaptype(t *testing.T) {
	checked := 0
	for _, src := range agreementSources(t) {
		toks, err := lexToEOF([]byte(src))
		if err != nil || len(toks) != 2 {
			continue // not a single token; irrelevant to this predicate
		}
		checked++

		pred := &parser{c: &cursor{toks: toks}}
		want := pred.atHeaptypeStart()

		cons := &parser{c: &cursor{toks: toks}}
		_, consErr := cons.heaptype()
		got := consErr == nil

		if want != got {
			t.Errorf("source %q: atHeaptypeStart=%v but heaptype succeeded=%v — the "+
				"predicate and the production disagree, so a form is either rejected as "+
				"valid or admitted as invalid", src, want, got)
		}
	}
	if checked < 400 {
		t.Fatalf("only %d sources were single-token and checkable, want at least 400", checked)
	}
	if !t.Failed() {
		t.Logf("predicate and production agree over %d single-token sources", checked)
	}
}

// TestValtypeStartAgreesWithValtype is the same control at the valtype layer, where the predicate
// carries more weight: valtypeList's loop condition *is* atValtypeStart, so a disagreement is
// either an infinite loop or a silently truncated list.
func TestValtypeStartAgreesWithValtype(t *testing.T) {
	checked := 0
	for _, src := range agreementSources(t) {
		toks, err := lexToEOF([]byte(src))
		if err != nil || len(toks) != 2 {
			continue
		}
		checked++
		pred := &parser{c: &cursor{toks: toks}}
		cons := &parser{c: &cursor{toks: toks}}
		_, consErr := cons.valtype()
		if want, got := pred.atValtypeStart(), consErr == nil; want != got {
			t.Errorf("source %q: atValtypeStart=%v but valtype succeeded=%v", src, want, got)
		}
	}
	if checked < 400 {
		t.Fatalf("only %d sources checkable, want at least 400", checked)
	}
}

// TestValtypeListTerminates is the progress property one layer up from the cursor.
//
// valtypeList loops while atValtypeStart holds and relies on valtype consuming at least one
// token. If a form ever satisfies the predicate without consuming, this hangs — the failure mode
// grave #18 named, where the exit condition and the error condition are the same predicate. The
// assertion is on the cursor position, not on a timeout.
func TestValtypeListTerminates(t *testing.T) {
	for _, src := range []string{
		"i32 i64 f32 f64",
		"v128 i32",
		"funcref externref anyref",
		"(ref null any) (ref $t) i32",
		"", // the empty list, which must consume nothing and return 0
	} {
		toks, err := lexToEOF([]byte(src))
		if err != nil {
			t.Fatalf("lex %q: %v", src, err)
		}
		p := &parser{c: &cursor{toks: toks}}
		before := p.c.pos
		vs, err := p.valtypeList()
		if err != nil {
			t.Errorf("valtypeList(%q) = %v", src, err)
			continue
		}
		n := len(vs)
		if n > 0 && p.c.pos == before {
			t.Errorf("valtypeList(%q) reported %d types and consumed nothing", src, n)
		}
		if n == 0 && p.c.pos != before {
			t.Errorf("valtypeList(%q) reported 0 types and consumed %d tokens",
				src, p.c.pos-before)
		}
	}
}

// TestValtypeListCount pins the count, which is the one number this stratum's error-only surface
// depends on and the one thing no error message mentions.
//
// `anon_locals c (fst $3)` (parser.mly:1006) advances the local index space by the param count,
// so a count off by one shifts every subsequent local binding — and under #62 the only visible
// consequence is a `duplicate local` that does or does not fire. Silent by construction, hence a
// direct test.
func TestValtypeListCount(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want int
	}{
		{"", 0},
		{"i32", 1},
		{"i32 i64", 2},
		{"i32 v128 funcref", 3},
		{"(ref null any) i32", 2},
		{"(ref $t) (ref null $t) i32 externref", 4},
		// i8 is PACKTYPE, which is a *storagetype* and not a valtype (parser.mly:406), so the
		// list stops immediately and consumes nothing — `i32` after it is never reached. Written
		// as `want: 1` in the first draft, with a case label that said the right thing and a
		// number that contradicted it; the test printed 0 and the label was correct. Fourth
		// hand-count miss on this grammar, and the standing practice earned its keep again.
		{"i8 i32", 0},
		{"i32 i8", 1}, // the mirror: the valtype is taken, the packed type ends the list
	} {
		toks, err := lexToEOF([]byte(tc.src))
		if err != nil {
			t.Fatalf("lex %q: %v", tc.src, err)
		}
		p := &parser{c: &cursor{toks: toks}}
		vs, err := p.valtypeList()
		if err != nil {
			t.Errorf("valtypeList(%q) = %v", tc.src, err)
			continue
		}
		if n := len(vs); n != tc.want {
			t.Errorf("valtypeList(%q) = %d, want %d", tc.src, n, tc.want)
		}
	}
}

// TestModuleAcceptDirection is the direction the suite is weakest in and the direction this
// stratum can most easily get wrong.
//
// Every case is a module the reference **accepts** and this stratum reaches the end of. A sugar
// arm omitted from a production shows up here and nowhere else: no vector asserts that a valid
// module parses, so a missing arm is a valid module rejected and invisible on the board (contract
// §9 G-3). These are *derived* from parser.mly's arms — each cites the arm it exercises, and the
// premise is that the arm exists at that line.
func TestModuleAcceptDirection(t *testing.T) {
	for _, tc := range []struct{ src, arm string }{
		{`(module)`, "module_:1389 with empty module_fields:1309"},
		{`(module $m)`, "module_var:1386, sugar"},
		{``, "inline_module:1394 — a bare field list, here empty"},

		// Types.
		{`(module (type (func)))`, "type_def:1276 + comptype:1449"},
		{`(module (type $t (func)))`, "type_def:1279, the bindidx sugar"},
		{`(module (type (func (param i32) (result i64))))`, "functype:1433/1441"},
		{`(module (type (func (param $x i32))))`, "functype:1436, named param sugar"},
		{`(module (type (func (param i32 i64) (result f32 f64))))`, "valtype_list:1396"},
		{`(module (type (struct)))`, "comptype:1447 + empty struct_field_list:1417"},
		{`(module (type (struct (field i32))))`, "struct_field_list:1419"},
		{`(module (type (struct (field $f i32))))`, "struct_field_list:1422, named"},
		{`(module (type (struct (field i32 i64) (field f32))))`, "two field groups"},
		{`(module (type (struct (field (mut i32)))))`, "fieldtype:1410"},
		{`(module (type (struct (field i8) (field i16))))`, "storagetype:1406, PACKTYPE"},
		{`(module (type (array i32)))`, "comptype:1448 + arraytype:1428"},
		{`(module (type (array (mut i8))))`, "arraytype with a packed mutable field"},
		{`(module (type (sub (func))))`, "subtype:1453"},
		{`(module (type (sub final (func))))`, "subtype:1456"},
		// **Corrected from `(module (type (sub $a $b (func))))`, an unbound-name case that read as
		// accepted only while `subtype`'s supertype list was discarded rather than resolved.**
		// `subtype`'s own grammar action calls `type_` (`lookup "type" c.types.space x`,
		// parser.mly:453-455) on each declared supertype eagerly, at parse time, and `$a`/`$b`
		// bound to nothing is `unknown type $a` on the reference's own terms — the sibling of the
		// drifted-fixture defect: an accept-direction claim citing a real grammar arm, never
		// checked against what that arm's semantic action actually requires. `check_subtype_sub`'s
		// forward-reference rule (valid.ml:169-171) is validation's, not this stratum's, but the
		// *name resolving to something at all* is `subtype`'s own action, so the two named types
		// below are declared first for the lookup to find.
		{`(module (type $a (func)) (type $b (func)) (type (sub $a $b (func))))`, "subtype:1453 with idx_list"},
		{`(module (rec (type (func))))`, "rectype:1294"},
		{`(module (rec (type (struct)) (type (array i32))))`, "type_def_list:1284"},

		// Reference types, all twelve abbreviations plus the explicit form.
		{`(module (type (func (param anyref nullref eqref i31ref))))`, "reftype:1378-1381"},
		{`(module (type (func (param structref arrayref funcref nullfuncref))))`, "reftype:1383-1385"},
		{`(module (type (func (param exnref nullexnref externref nullexternref))))`, "reftype:1386-1389"},
		{`(module (type (func (param (ref any) (ref null any)))))`, "reftype:1377 + null_opt:357"},
		{`(module (type (func (param (ref none) (ref eq) (ref i31)))))`, "heaptype:1362-1365"},
		{`(module (type (func (param (ref struct) (ref array) (ref func)))))`, "heaptype:1366-1368"},
		{`(module (type (func (param (ref nofunc) (ref exn) (ref noexn)))))`, "heaptype:1369-1371"},
		{`(module (type (func (param (ref extern) (ref noextern)))))`, "heaptype:1372-1373"},
		// **The two `idx` arms differ and this row was wrong until #64 made it checkable.** It
		// read `(ref 0) (ref $t)` in a module defining neither, on the reasoning that a heaptype
		// index is read and discarded — true of the reader, false of the reference. `heaptype`'s
		// idx arm is `UseHT (Idx ($1 c type_).it)` (:374), and `idx`'s VAR arm is `lookup c (var
		// $1)` (:489): so a *symbolic* heaptype resolves at parse time and `$t` unbound is
		// `unknown type $t`, while a *numeric* one is `nat32 $1` with no lookup and `(ref 0)` in
		// an empty module is the validator's `unknown type 0` — which is what ref.wast:27-33
		// asserts, as `assert_invalid`. The row passed for as long as neither half was
		// implemented, then failed the moment the resolution phase existed. A green that survives
		// the bug it names, and the bug's arrival is what named it.
		{`(module (type (func (param (ref 0)))))`, "heaptype:1374's NAT arm — no lookup, so an " +
			"out-of-range index is validation's (ref.wast:27, assert_invalid)"},
		{`(module (type $t (func (param (ref $t)))))`, "heaptype:1374's VAR arm, which resolves " +
			"at parse time — so the name must be bound, and self-reference is legal"},

		// Imports.
		{`(module (import "" "" (func)))`, "import:1250 + externtype:1228, and the empty name"},
		{`(module (import "m" "f" (func $f)))`, "externtype:1228 with bindidx_opt"},
		{`(module (import "m" "f" (func (type 0))))`, "externtype:1228 + typeuse:1470"},
		{`(module (import "m" "f" (func (param i32) (result i32))))`, "externtype:1246, sugar"},
		{`(module (import "m" "t" (tag (type 0))))`, "externtype:1231"},
		{`(module (import "m" "t" (tag (param i32))))`, "externtype:1234, sugar"},
		{`(module (import "m" "g" (global i32)))`, "externtype:1237"},
		{`(module (import "m" "g" (global (mut i64))))`, "globaltype:1402"},
		{`(module (import "m" "m" (memory 1)))`, "externtype:1240 + limits:1466"},
		{`(module (import "m" "m" (memory 1 2)))`, "limits:1467, both bounds"},
		{`(module (import "m" "m" (memory i64 1)))`, "addrtype:347, explicit i64"},
		{`(module (import "m" "t" (table 1 funcref)))`, "externtype:1243 + tabletype:1460"},
		{`(module (import "m" "t" (table i64 1 2 externref)))`, "tabletype with addrtype and max"},

		// Exports.
		{`(module (export "e" (func 0)))`, "export:1265 + externidx:1262"},
		// This row used to be `(module (export "e" (global $g)))` with `$g` never defined, on the
		// claim that externidx:1259 accepts it. It does not: the arm is `fun c -> GlobalX ($3 c
		// global)` and `global` is `lookup "global" c.globals`, which raises `unknown global $g`.
		// The row was green only because the index was discarded unresolved, so the accept table
		// was asserting the *absence* of a check as though it were a grammar fact — and it was
		// caught by resolveSpaceIdx's arrival, not by review. Defining `$g` keeps the arm covered
		// and makes the claim true; the reject direction is TestExportResolvesInEverySpace's.
		{`(module (global $g i32) (export "e" (global $g)))`, "externidx:1259, a *defined* $g"},
		{
			`(module (export "a" (func $a)) (func $a))`,
			"exports.wast:14 — the forward reference, which is why resolution is stage 2's",
		},
		{`(module (export "" (memory 0)))`, "externidx:1260, empty name"},
		{`(module (export "e" (table 0)) (export "f" (tag 0)))`, "externidx:1261/1258"},

		// Definitions that complete without an instruction body.
		{`(module (func))`, "func:959 with an empty body"},
		{`(module (func $f))`, "func with bindidx"},
		{`(module (func (param i32) (result i32)))`, "func_fields_body:1005/1013"},
		{`(module (func (param $x i32) (local $y i64)))`, "bind_local at :1009 and :1026"},
		{`(module (func (local i32 i64)))`, "func_body:1023, anon_locals"},
		{`(module (func (export "e")))`, "inline_export:1269"},
		{`(module (func (export "a") (export "b")))`, "inline_export recursion"},
		{`(module (func (import "m" "f")))`, "func_fields_import:991"},
		{`(module (func (import "m" "f") (param i32) (result i32)))`, "func_fields_import:994"},
		{`(module (func (export "e") (import "m" "f")))`, "inline_export then inline_import"},
		{`(module (func (type 0)))`, "func_fields:965 with typeuse"},

		{`(module (tag))`, "tag:1042 with an empty functype"},
		{`(module (tag $t (param i32)))`, "tag_fields:1051"},
		{`(module (tag (import "m" "t") (param i32)))`, "tag_fields:1066"},
		{`(module (tag (export "e")))`, "tag_fields:1071"},

		{`(module (global (import "m" "g") i32))`, "global_fields:1084"},
		{`(module (global (import "m" "g") (mut i32)))`, "global_fields:1084 with mut"},
		{`(module (global (export "e") (import "m" "g") i32))`, "global_fields:1087"},

		{`(module (memory 1))`, "memory_fields:1118"},
		{`(module (memory $m 1 2))`, "memory with bindidx and max"},
		{`(module (memory i64 1))`, "addrtype in memory_fields"},
		{`(module (memory (import "m" "m") 1))`, "memory_fields:1120"},
		{`(module (memory (export "e") 1))`, "memory_fields:1125"},
		{`(module (memory (data)))`, "memory_fields:1129, the data sugar with no strings"},
		{`(module (memory (data "abc" "def")))`, "memory_fields:1129 + string_list:1343"},
		{`(module (memory i64 (data "abc")))`, "the data sugar with an explicit addrtype"},

		{`(module (table 1 funcref))`, "table_fields:1192, the bare-tabletype arm"},
		{`(module (table $t 1 2 externref))`, "tabletype with bindidx and max"},
		{`(module (table (import "m" "t") 1 funcref))`, "table_fields:1197"},
		{`(module (table (export "e") 1 funcref))`, "table_fields:1201"},
		{`(module (table funcref (elem)))`, "table_fields:1216 with an empty elemidx_list"},
		{`(module (table funcref (elem 0 1 $f)))`, "table_fields:1216 + elemidx_list:1147"},

		{`(module (data))`, "data:1096, passive with no strings"},
		{`(module (data $d))`, "data with bindidx"},
		{`(module (data "abc"))`, "data:1096 + string_list"},
		{`(module (data "\ef\ff\fe"))`, "string_list:1343 does NOT decode — the whole reason " +
			"the UTF-8 check is at name/var and not in the lexer"},
		{`(module (data "" "" ""))`, "string_list concatenating empties"},

		// `(elem)` and `(elem $e)` sat here claiming "elem:1158 with an empty elem_list", and the
		// reference has no such derivation: `elem_list`'s two arms are `elemkind elemidx_list` and
		// `reftype elemexpr_list` (:1152-1156), whose heads — `FUNC` and reftype's thirteen arms — each
		// consume a token, so `elem_list` is not nullable and neither of these is a sentence. The rows
		// that *do* reach an empty element list keep an explicit head, below. What settled it was a
		// nullability fixpoint over the whole `.mly` rather than a third reading of the two productions:
		// 50 of 137 nonterminals are nullable and `elem_list`, `offset`, `expr` and `reftype` are not.
		// (#143. The first two attempts at that instrument reported *every* nonterminal nullable and
		// then all 137 again — a spurious ε arm from layout — which is the vacuity law: `nullable=false`
		// out of an extractor that found zero arms is not an answer.)
		{`(module (table 1 funcref) (elem (i32.const 0)))`, "elem:1175, the offset sugar over an " +
			"empty elemidx_list — idx_list:500 IS nullable, and this is the one arm of five that " +
			"reaches an empty element list (29 corpus rows, elem.wast:35/:39)"},
		{`(module (table 1 funcref) (elem $a (offset (i32.const 0))))`, "the same arm with bindidx " +
			"and the explicit `(offset …)` spelling"},
		{`(module (elem func))`, "elem_list:1153 + elemkind:1136, empty idx list"},
		{`(module (elem func 0 1 $f))`, "elemidx_list:1147"},
		{`(module (elem declare func $f))`, "elem:1168"},
		{`(module (elem funcref))`, "elem_list:1155 with an empty elemexpr_list"},

		{`(module (start 0))`, "start:1304"},
		{`(module (start $f))`, "start with a symbolic index"},

		// Ordering and multiplicity that must be permitted.
		{`(module (import "m" "a" (func)) (import "m" "b" (func)))`, "two imports, no definition"},
		{`(module (import "m" "f" (func)) (func))`, "import BEFORE definition is legal"},
		{`(module (func) (func))`, "two anonymous funcs — bindAnon, not a duplicate"},
		// Two spaces may hold the same name. Written without an inline import on purpose:
		// the first draft was `(global $a (import "m" "g") i32)`, which the reference
		// *rejects* — an inline-imported field contributes nothing to `globs` but its import
		// does land in `m.imports`, so the preceding `(func $a)` trips :1348. The case meant
		// to exercise index-space independence had quietly become an ordering case, and the
		// accept sweep is what said so.
		{`(module (func $a) (global $a i32))`, "same name in two spaces"},
		{`(module (import "m" "g" (global $a i32)) (func $a))`, "same name in two spaces, " +
			"with the import first so no ordering check fires"},
		{`(module (start 0) (func))`, "one start section"},
		{`(module (type (func)) (import "m" "f" (func (type 0))))`, "a type is not a definition " +
			"for ordering purposes — type_:1314 has no import check"},

		// A bare field list, the inline_module sugar.
		{`(func) (memory 1)`, "inline_module1:1401"},

		// `instr_list`'s third and fourth arms (:549/:550), the flat `select` and `call_indirect`
		// family. **These are the grave.** Nothing read them, so every module containing a flat
		// `select` was rejected — accept-direction, therefore invisible to every
		// `assert_malformed` vector in the corpus, which is exactly what this table exists for.
		{`(module (func select))`, "selectinstr_instr_list:549 → :677, no result annotation"},
		{`(module (func select (result i32)))`, "selectexpr_results:677's annotated arm"},
		{`(module (func nop select nop))`, "the chain absorbs the list's tail, so a select may " +
			"sit mid-sequence"},
		{`(module (table 0 funcref) (func call_indirect))`, "callinstr_instr_list:550 → :689"},
		{
			`(module (type (func)) (table 0 funcref) (func call_indirect (type 0)))`,
			"callinstr's typeuse arm",
		},
		{
			`(module (table 0 funcref) (func call_indirect (param i32) (result i32)))`,
			"callinstr_params_instr_list:712, the ordered chain",
		},
		{
			`(module (type (func)) (table 0 funcref) (func return_call_indirect (type 0)))`,
			"the return_ variant, :699",
		},

		// `expr1`'s ten arms (:813-834), the folded forms. Carried over from the retired
		// TestBodyBoundaryIsNamed, whose ten cases were the only assertion that these *parse*
		// — the re-pointed tripwire asserts nothing reports `unimplemented`, which is a
		// different claim. A control's replacement inherits its obligations, not just its name.
		{`(module (func (block)))`, "expr1:826, the folded block — no END, extent from the paren"},
		{`(module (func (loop)))`, "expr1:828"},
		{`(module (func (if (then))))`, "expr1:830 + if_:896, the one-armed sugar"},
		{`(module (func (try_table)))`, "expr1:833"},
		{`(module (func (select)))`, "expr1:815 → selectexpr_results"},
		{`(module (type (func)) (table 0 funcref) (func (call_indirect (type 0))))`, "expr1:817"},
		{`(module (func (i32.eqz (block))))`, "a folded block as a folded operand — expr_list:946 " +
			"nested one deep, which is where a boundary was most easily leaked"},
		{`(module (func (drop (select))))`, "the same, through select"},
		{`(module (memory 1) (data (offset (block)) "abc"))`, "offset:1091 descending into expr1"},
		{`(module (table 1 funcref) (elem (table 0) (offset (block)) func))`, "elem's offset"},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v\n\tthis module is ACCEPTED by the reference "+
				"(%s); no suite vector asserts it, so this is the only place the omission "+
				"is visible", tc.src, err, tc.arm)
		}
	}
}

// TestModuleRejectDirection pins the four grammar-level errors this stratum owns, at module
// level rather than at the context's method level.
//
// Each cites its suite vector where one exists. `duplicate export name` is deliberately absent:
// it is valid.ml:1146, the validator's, 26 vectors, and measured to be outside this stratum
// before the forecast was written.
func TestModuleRejectDirection(t *testing.T) {
	for _, tc := range []struct{ src, want, cite string }{
		// The three category words grave #120 corrected. Each row used to want the short word,
		// citing the very line that refutes it: `bind_abs:174` renders `"duplicate " ^ category`,
		// and the category `bind_func` passes is `"function"` (parser.mly:192), not `func`.
		// `func.wast:966` wants the string `"duplicate func"` and gets it — as a *prefix*, under
		// the harness's substring match — which is why three suite vectors could not tell the two
		// spellings apart. `data`/`elem` had no vector at all.
		{`(module (func $foo)(func $foo))`, "duplicate function $foo", "func.wast:966, bind_func:192"},
		{
			`(module (global $g i32) (global $g (import "m" "g") i32))`, "duplicate global $g",
			"bind_abs:174 via bind_global",
		},
		{`(module (type $t (func)) (type $t (func)))`, "duplicate type $t", "bind_abs:174"},
		{`(module (memory $m 1) (memory $m 1))`, "duplicate memory $m", "bind_abs:174"},
		{`(module (table $t 1 funcref) (table $t 1 funcref))`, "duplicate table $t", "bind_abs:174"},
		{`(module (data $d) (data $d))`, "duplicate data segment $d", "bind_data:193 — two words"},
		// `func` after each bindidx, not a bare `(elem $e)`: this row is about `bind_elem`, and a
		// carrier that is itself a syntax error tests the wrong rejection (#143 — it *did* pass, on
		// "unexpected token", which is the shape a partition check catches and an `errors.Is` does not).
		{
			`(module (elem $e func) (elem $e func))`, "duplicate elem segment $e",
			"bind_elem:194 — two words",
		},
		{`(module (tag $t) (tag $t))`, "duplicate tag $t", "bind_abs:174"},
		{
			`(module (func (param $x i32) (local $x i32)))`, "duplicate local $x",
			"params and locals share one space; :1009 and :1026",
		},
		{`(module (func (param $x i32) (param $x i64)))`, "duplicate local $x", "same space"},
		{
			`(module (type (struct (field $f i32) (field $f i32))))`, "duplicate field $f",
			"bind_field:420-423, a per-type space",
		},

		{
			`(module (func) (import "m" "f" (func)))`, "import after function definition",
			"imports.wast:677",
		},
		{
			`(module (global $g i32 ) (import "m" "f" (func)))`, "import after global definition",
			"imports.wast — and note the import is a FUNC while the message says GLOBAL",
		},
		{`(module (memory 1) (import "m" "f" (func)))`, "import after memory definition", "imports.wast"},
		{
			`(module (table 1 funcref) (import "m" "f" (func)))`, "import after table definition",
			"imports.wast",
		},
		{`(module (tag) (import "m" "f" (func)))`, "import after tag definition", "imports.wast"},

		{`(module (start 0) (start 0))`, "multiple start sections", "parser.mly:1372"},

		{
			`(module (import "m" "\ef" (func)))`, "malformed UTF-8 encoding",
			"utf8-invalid-encoding.wast — the name site, where 176 vectors land",
		},
		{`(module (export "\80" (func 0)))`, "malformed UTF-8 encoding", "the name site"},
		{
			`(module (func $"\ef"))`, "malformed UTF-8 encoding",
			"id.wast:31 — the var site, the single vector for it",
		},

		{`(module (memory f32 1))`, "malformed address type", "addrtype:352"},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; want %q (%s)", tc.src, tc.want, tc.cite)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ReadModule(%q) = %q, want it to contain %q (%s)",
				tc.src, err.Error(), tc.want, tc.cite)
		}
	}
}

// TestGlobalIsNotADefinitionWhenImported is the inline-import subtlety, stated as a test because
// the plausible wrong reading is one line away.
//
// `(global (import …) i32)` is a *field* of kind global that produces no definition, so a later
// import is legal. The reference's arm has `globs = []` on the import path, which is what makes
// the `globs <> []` guard false. A parser that called markDefined for every global field would
// reject this module — and no vector says so, because every ordering vector uses a real
// definition.
func TestGlobalIsNotADefinitionWhenImported(t *testing.T) {
	for _, src := range []string{
		`(module (global (import "m" "g") i32) (import "m" "f" (func)))`,
		`(module (func (import "m" "f")) (import "m" "g" (global i32)))`,
		`(module (memory (import "m" "m") 1) (import "m" "f" (func)))`,
		`(module (table (import "m" "t") 1 funcref) (import "m" "f" (func)))`,
		`(module (tag (import "m" "t")) (import "m" "f" (func)))`,
	} {
		if err := ReadModule([]byte(src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; an inline-imported field is NOT a definition, so "+
				"the following import is legal (parser.mly:1353's `funcs <> []` is false on "+
				"the import arms)", src, err)
		}
	}
}

// TestNoInstructionLeaderIsUnread began as TestBodyBoundaryIsNamed, which pinned that stopping
// short is reported as stopping short.
//
// A module whose only unmet requirement is an instruction body must say `unimplemented`, not
// `unexpected token`: the board buckets by expected string, and a boundary masquerading as a
// syntax error is a work item filed under the wrong heading. *An error from the wrong layer is
// evidence about where structure was lost* — so the layer that is missing names itself.
//
// **Re-pointed by #63, not closed.** Every case this test was written with — `(func nop)`,
// `(global i32 (i32.const 0))`, all six data/elem/table offset forms — now parses, so the test
// failed with eleven "accepted; this stratum does not parse instruction bodies" errors. That is
// the tripwire-whose-subject-dissolves shape: the control names the *risk* that a boundary gets
// reported as a syntax error, and the risk did not go away when the flat grammar landed, it moved
// to #64's arms. Deleting the test as "no longer applicable" would retire a live risk on a
// technicality; relaxing it to accept the eleven would leave nothing asserting the property.
//
// So the cases are now #64's productions, one per family, each reached *through* #63's readers —
// which is also the stronger test: the boundary is now hit mid-parse, after a mnemonic has been
// consumed, where before it was hit on the first token of the body. A `block` inside a folded
// expr operand is the case that would most plausibly leak an `unexpected token`.
//
// **Re-pointed a second time, inside the same PR, and the second move is the instructive one.**
// The four flat block forms above were filed here as "#64's" on the strength of their *surface*
// — a `block` is a block — and the seam ruling had already said the opposite: seams follow defect
// ownership, not surface form. #63's Scope list names `blockinstr` (:726) and the block family
// (:740–:792), so `(func block end)` was always this issue's, and the measurement that settled it
// (flat=17, folded=75) is in the changelog. They now parse on the merits, and what replaces them
// is the *folded* spelling of each — `expr1`'s BLOCK/LOOP/IF/TRY_TABLE arms (:826–:834), which
// really are #64's, and which reach the boundary one paren deeper than the flat forms ever did.
//
// The lesson is that a tripwire's case list can be mis-assigned even while the tripwire itself is
// correct: this test never stopped asserting the right property, it just asserted it about four
// vectors that belonged to the PR it was riding in. A control's *scope* is as falsifiable as its
// predicate, and the way to check it is the same — measure which reader answers the case, don't
// read the mnemonic.
//
// # Re-pointed a third time by #64, and this time the *risk* inverts rather than moving
//
// All ten cases above now parse or are correctly rejected, because the folded arms landed and there
// is no later stratum left: all four `instr_list` arms (parser.mly:546-550) and all three `instr1`
// arms (:552-554) have readers. So the risk this tripwire was filed against — a deferral reported
// as a syntax error — has no subject, and the *opposite* risk is now the live one: a **syntax error
// reported as a deferral**, `unimplemented` promising a reader nobody will ever write. That is the
// same wrong-layer defect with its sign flipped, and it is the flattering direction, because it
// parks a module the reference rejects in a bucket the board reads as remaining work.
//
// Which is exactly the defect #70 fixed for parenthesized forms and did not fix for bare ones: with
// #70's boundary in place, `(module (func param))` still answered `unimplemented: instruction body
// at "param"`, because the derived check only looked past a `(`. The rule says a dissolved subject
// is re-pointed, never closed, and the re-pointing here is a *sign change* rather than a new case
// list — which is why it also comes with the case list being thrown away.
//
// **Scoped to the space, not to cases.** The two halves below sweep the generated keyword table
// rather than enumerating spellings, per *derive the domain, never enumerate it*: an enumeration
// would freeze at the moment of authorship and is precisely what let three re-pointings all be
// about which examples belonged in a list. The premise the sweep needs — that no reader is owed —
// is checkable, and half one is the check.
func TestNoInstructionLeaderIsUnread(t *testing.T) {
	// Half one, the accept direction: every keyword startsInstruction admits must be *consumed*
	// by some reader. This is the premise half two rests on — the `unimplemented` arm is only safe
	// to delete if nothing admitted is still owed a reader — and it is the direction no
	// `assert_malformed` can see, since a leader nobody reads makes a legal module fail.
	//
	// The discriminator is the error's own offset. A reader that claims a mnemonic and then dislikes
	// its immediates reports at the *immediate*; only a cursor nobody advanced is still sitting on
	// the leader. So `unexpected token` blamed at the leader's own offset means unread — and the one
	// keyword that errors at its own offset for a reader-internal reason, `i8x16.shuffle` with
	// `wrong number of lane indices`, is distinguished by its message rather than excused by name.
	const prefix = "(module (func "
	admitted := 0
	for text, kind := range keywords {
		if !startsInstruction(kind) {
			continue
		}
		admitted++
		src := prefix + text + "))"
		var e *Error
		if err := ReadModule([]byte(src)); err == nil || !errors.As(err, &e) {
			continue
		}
		if e.Offset == len(prefix) && e.Msg == "unexpected token" {
			t.Errorf("ReadModule(%q) = %q blamed at the leader's own offset %d: no reader "+
				"consumed %q, so a keyword startsInstruction admits is unread — a legal "+
				"module rejected, which no assert_malformed vector can catch",
				src, e.Msg, e.Offset, text)
		}
	}
	// The vacuity check, because half one's verdict is "no counterexample found" and an empty
	// domain finds none: a `startsInstruction` that returned false for everything, or a `keywords`
	// table the generator emptied, would pass silently. 494 today; the floor is deliberately loose
	// because the count grows with the proposal gates.
	if admitted < 400 {
		t.Fatalf("only %d keywords start an instruction; the sweep above asserted almost "+
			"nothing (expected ~494 — plaininstr's mnemonics plus expr1's seven leaders)", admitted)
	}

	// Half two, the re-pointed tripwire: nothing may report `unimplemented` any more. Swept over
	// the same table plus the token classes that are not keywords at all — a bare NAT, string, VAR
	// or float in instruction position, which are what the old arm actually still reached.
	nonKeyword := []string{`5`, `1.5`, `"abc"`, `$x`, `0x10`, `-3`}
	probes := make([]string, 0, len(keywords)+len(nonKeyword))
	for text := range keywords {
		probes = append(probes, text)
	}
	probes = append(probes, nonKeyword...)
	for _, tok := range probes {
		// Both positions, since #70's fix covered the parenthesized one and left the bare one:
		// `(func (param i32))` was answered and `(func param)` was not.
		for _, src := range []string{prefix + tok + "))", prefix + "(" + tok + ")))"} {
			err := ReadModule([]byte(src))
			if err != nil && strings.Contains(err.Error(), "unimplemented") {
				t.Errorf("ReadModule(%q) = %q; every instruction production has a reader, so "+
					"an `unimplemented` here promises work that does not exist and parks a "+
					"module the reference rejects in the board's remaining-work bucket",
					src, err.Error())
			}
		}
	}
}

// TestMissingMandatoryBodyIsNotABoundary is the complement, and it is the direction that flatters.
//
// Every caller reaching bodyBoundary has a grammar demanding at least one instruction —
// `constexpr1` (parser.mly:951), `offset` (:1091), the table sugar's leading `elemexpr` (:1205).
// So these modules are malformed on the merits and this stratum can judge them: an `unimplemented`
// here would park a module the *reference rejects* in the work-plan bucket, as though finishing
// the instruction grammar would one day make it legal. That is the wrong-layer error pointed in
// the self-serving direction, and it inflates a bucket the board reads as remaining work.
//
// Distinguishing this from the empty-arm cases is the same question instrList answers, asked from
// the other side: `(func)` is legal because instr_list has an empty arm, `(data (memory 0))` is
// not because offset does not.
func TestMissingMandatoryBodyIsNotABoundary(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (data (memory 0)))`, "data:1099 requires an offset after memoryuse"},
		{`(module (elem (table 0)))`, "elem:1164 requires an offset after tableuse"},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; %s", tc.src, tc.why)
			continue
		}
		if strings.Contains(err.Error(), "unimplemented") {
			t.Errorf("ReadModule(%q) = %q; the reference has no arm for this, so it is "+
				"malformed rather than unread (%s)", tc.src, err.Error(), tc.why)
		}
	}
}

// TestReadModuleProgress is the parser's half of the progress property.
//
// The cursor's next() spins at EOF rather than running off the end, which converts "ran off the
// end" into "loops forever" — visible, but only if something looks. moduleFields loops while the
// cursor is at LParen, and every field production must consume the LParen it dispatched on. A
// production that returned nil without consuming would hang here, so this asserts termination
// over shapes designed to find that.
func TestReadModuleProgress(t *testing.T) {
	for _, src := range []string{
		`(module (`,
		`(module ()`,
		`(module ())`,
		`(module (func`,
		`(module (type`,
		`(module (type (`,
		`(module (import`,
		`(module (import "m"`,
		`(module (memory`,
		`(module (table`,
		`(module (elem (`,
		`(module (data (`,
		`(`,
		`()`,
		`((((((((((`,
		`(module (module))`,
	} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = ReadModule([]byte(src))
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			// A timer, and the honest kind: the property is *termination*, whose falsification
			// has no completion signal to wait on — nothing can be polled for "this loop will
			// never finish". That is the one case where a duration is the instrument rather
			// than a stand-in for one. Not a verdict about speed: 5s for a parse of at most
			// sixteen tokens is four orders of magnitude of headroom, so a trip means a hang.
			t.Fatalf("ReadModule(%q) did not terminate — a field production returned "+
				"without consuming its LPAR and moduleFields is spinning (grave #18)", src)
		}
	}
}

// TestLparConsumesBothOrNeither pins the half-consumed-cursor invariant.
//
// lpar checks the paren and the keyword together and consumes both or neither. Nothing backtracks
// today, so a half-consumed cursor is not yet a bug — which is exactly when it is cheap to pin,
// and *a design debt is discharged by a tripwire, never by an intention*. If a production ever
// speculates, this is the invariant it will rely on.
func TestLparConsumesBothOrNeither(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want keywordKind
		ok   bool
	}{
		{`(func`, kwFunc, true},
		{`(func`, kwGlobal, false},
		{`(`, kwFunc, false},
		{`func`, kwFunc, false},
		{`($x`, kwFunc, false},
		{`(0`, kwFunc, false},
	} {
		toks, err := lexToEOF([]byte(tc.src))
		if err != nil {
			t.Fatalf("lex %q: %v", tc.src, err)
		}
		p := &parser{c: &cursor{toks: toks}}
		gotErr := p.lpar(tc.want)
		if (gotErr == nil) != tc.ok {
			t.Errorf("lpar(%q, %q) error = %v, want ok=%v", tc.src, tc.want, gotErr, tc.ok)
			continue
		}
		want := 0
		if tc.ok {
			want = 2
		}
		if p.c.pos != want {
			t.Errorf("lpar(%q, %q) consumed %d tokens, want %d — a failed lpar must leave "+
				"the cursor where it found it", tc.src, tc.want, p.c.pos, want)
		}
	}
}

// TestRparRejectsAndDoesNotConsume is lpar's counterpart, and it exists because a mutation
// survived the whole board without it.
//
// Making rpar accept any token and consume it left every test green — including the 152-vector
// `unexpected token` bucket — and `(module (type (func (param i32` was **accepted**. The mechanism
// is worth stating, because it is the cursor's EOF invariant turning into a hole: next() does not
// advance at EOF, so a permissive rpar at end-of-input consumes nothing and returns nil, every
// production unwinds successfully, and ReadModule's closing `at(EOF)` check is satisfied by the
// same EOF token rpar declined to move past. A truncated module parses clean.
//
// **Synthetic, necessarily.** The suite's `unexpected token` vectors are all *surplus* — a token
// where the grammar wanted `)` — and none is *truncated*, because a .wast file whose parens do not
// balance cannot be read as a script at all: the harness would never extract a module from it. The
// oracle cannot ask this question by construction, which is the same structural blindness the
// accept direction has (contract §9 G-3), pointed at the other end of the input.
func TestRparRejectsAndDoesNotConsume(t *testing.T) {
	for _, tc := range []struct {
		src string
		ok  bool
		why string
	}{
		{`)`, true, "the token it exists to accept"},
		{`(`, false, "surplus open paren — the suite's shape"},
		{`func`, false, "a keyword where `)` was wanted"},
		{`0`, false, "a nat where `)` was wanted"},
		{``, false, "**end of input**, which is the case the board could not see: at EOF the " +
			"cursor does not advance, so an rpar that consumed-and-succeeded here would " +
			"accept a module whose parens never closed"},
	} {
		toks, err := lexToEOF([]byte(tc.src))
		if err != nil {
			t.Fatalf("lex %q: %v", tc.src, err)
		}
		p := &parser{c: &cursor{toks: toks}}
		gotErr := p.rpar()
		if (gotErr == nil) != tc.ok {
			t.Errorf("rpar(%q) error = %v, want ok=%v\n\t%s", tc.src, gotErr, tc.ok, tc.why)
			continue
		}
		want := 0
		if tc.ok {
			want = 1
		}
		if p.c.pos != want {
			t.Errorf("rpar(%q) consumed %d tokens, want %d — a failed rpar must leave the "+
				"cursor where it found it\n\t%s", tc.src, p.c.pos, want, tc.why)
		}
	}
}

// TestTruncatedModuleIsRejected is the same finding at the entry point, where it is a claim about
// ReadModule rather than about one production.
//
// The unit test above pins rpar; this pins that the invariant reaches the surface. Both are here
// because the mutation that motivated them was invisible to *every* existing test, and a unit test
// on rpar alone would not have caught, say, a caller that ignored its error. Every case is a prefix
// of a module the parser otherwise accepts, so a rejection cannot come from the content.
func TestTruncatedModuleIsRejected(t *testing.T) {
	for _, src := range []string{
		`(module`,
		`(module (type (func`,
		`(module (type (func (param i32`,
		`(module (memory 1`,
		`(module (table 1 funcref`,
		`(module (import "m" "f" (func`,
		`(module (export "e" (func 0`,
	} {
		if err := ReadModule([]byte(src)); err == nil {
			t.Errorf("ReadModule(%q) accepted a module whose parens never close; at EOF the "+
				"cursor cannot advance, so every unwinding rpar must still *reject*", src)
		}
	}
	// The vacuity half: each case above must be a prefix of something accepted, or the test
	// proves nothing about truncation.
	for _, src := range []string{
		`(module)`,
		`(module (type (func)))`,
		`(module (type (func (param i32))))`,
		`(module (memory 1))`,
		`(module (table 1 funcref))`,
		`(module (import "m" "f" (func)))`,
		`(module (func) (export "e" (func 0)))`,
	} {
		if err := ReadModule([]byte(src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; the closed form must be accepted or the truncated "+
				"case above is rejected for its content, not for its truncation", src, err)
		}
	}
}

// TestUnexpectedTokenCarriesNoInventedText pins the half of the error the oracle cannot read.
//
// The suite's 152-vector bucket matches `unexpected token` and stops. Everything past the
// sentinel is ours alone to keep honest, and grave #36 was an error quoting a byte the input never
// held. The reference's own message is menhir's, generated from a parse state we do not have — so
// reconstructing a token name would be inventing evidence about the parser's internals. This
// asserts the message is the sentinel and nothing more.
func TestUnexpectedTokenCarriesNoInventedText(t *testing.T) {
	for _, src := range []string{
		`(module (func) x)`,
		`(module (type (func (param))) 0)`,
		`(module (import "m" "f" (nop)))`,
		`(module (export "e" (nop 0)))`,
	} {
		err := ReadModule([]byte(src))
		if err == nil {
			continue // some of these may reach the body boundary instead; that is fine
		}
		msg := err.Error()
		if !strings.Contains(msg, "unexpected token") && !strings.Contains(msg, "unimplemented") {
			continue
		}
		if strings.Contains(msg, "unexpected token") && msg != "unexpected token" {
			t.Errorf("ReadModule(%q) = %q; the message must be the bare sentinel — anything "+
				"past it is text no vector checks and we would have to defend (grave #36)",
				src, msg)
		}
	}
}

// TestErrorsCarryAPosition asserts every error this package returns is a *text.Error with a real
// offset and line.
//
// Positions are not in any expected string, so nothing on the board defends them — and an error
// at offset 0 for a defect on line 40 is a lying witness in the same family as a fabricated byte.
//
// **All three error constructors, because covering one let two mutations survive.** The first
// draft checked the duplicate message only, which comes from errf; blanking the position in
// `unexpectedAt` — the constructor behind the 152-vector `unexpected token` bucket, the most
// common error this package emits — changed nothing on the board and nothing in this test, and
// blanking it in `errAt` was invisible too. The parametrization is the fix, and the partition is
// *the constructors*, not the messages: a test named for "every error this package returns" is
// checked against the set of things that build one. There are exactly three (errAt, errf,
// unexpectedAt, all in cursor.go and parser.go), and each has a case below. A fourth added later
// needs a case, which the count assertion at the end is there to force.
func TestErrorsCarryAPosition(t *testing.T) {
	// Which constructor each case exercises, so the coverage claim is checkable rather than
	// implied by the case names.
	const (
		viaErrf        = "errf"
		viaErrAt       = "errAt"
		viaUnexpectedA = "unexpectedAt"
	)
	covered := map[string]bool{}

	for _, tc := range []struct {
		name        string
		constructor string
		src         string
		wantLine    int
		wantAt      string // the source text the offset must point at
	}{
		{
			// errf's path: `duplicate <category> <name>`.
			name:        "duplicate",
			constructor: viaErrf,
			src:         "(module\n  (func $f)\n  (func $f)\n)",
			wantLine:    3,
			wantAt:      "$f",
		},
		{
			// errAt's path, via checkStart. Also missed by the first draft.
			name:        "multiple start sections",
			constructor: viaErrAt,
			src:         "(module\n  (func)\n  (start 0)\n  (start 0)\n)",
			wantLine:    4,
			wantAt:      "(start",
		},
		{
			// errAt again, via addrtype — the one grammar error that reads a token's text.
			name:        "malformed address type",
			constructor: viaErrAt,
			src:         "(module\n  (memory\n    f32 1)\n)",
			wantLine:    3,
			wantAt:      "f32",
		},
		{
			// unexpectedAt's path, and the one the first draft missed. `unexpected token` is
			// the suite's largest bucket here and its position was entirely unasserted.
			name:        "unexpected token",
			constructor: viaUnexpectedA,
			src:         "(module\n  (func)\n  (memory 1)\n  0\n)",
			wantLine:    4,
			wantAt:      "0",
		},
		{
			// unexpectedAt reached through *lookahead*, where the offending token is not the
			// one the cursor sits on — so a position read off the cursor instead of off the
			// token would point at the `(` on the line before.
			name:        "unexpected token via peek2",
			constructor: viaUnexpectedA,
			src:         "(module\n  (func)\n  (0)\n)",
			wantLine:    3,
			wantAt:      "0",
		},
	} {
		covered[tc.constructor] = true
		t.Run(tc.name, func(t *testing.T) {
			err := ReadModule([]byte(tc.src))
			if err == nil {
				t.Fatal("expected an error")
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error is %T, want *text.Error", err)
			}
			if e.Line != tc.wantLine {
				t.Errorf("%v reported on line %d, want %d", err, e.Line, tc.wantLine)
			}
			if e.Offset <= 0 || e.Offset >= len(tc.src) {
				t.Errorf("offset %d is outside the source (len %d)", e.Offset, len(tc.src))
				return
			}
			if !strings.HasPrefix(tc.src[e.Offset:], tc.wantAt) {
				t.Errorf("offset %d points at %q, want %q", e.Offset,
					tc.src[e.Offset:min(e.Offset+4, len(tc.src))], tc.wantAt)
			}
		})
	}

	for _, ctor := range []string{viaErrf, viaErrAt, viaUnexpectedA} {
		if !covered[ctor] {
			t.Errorf("no case exercises %s, so a position dropped there is invisible — which "+
				"is exactly what happened to errAt and unexpectedAt", ctor)
		}
	}
}

// TestErrorConstructorsAreAccountedFor is the vacuity check on the partition above.
//
// TestErrorsCarryAPosition claims to cover every error constructor in the package, and that claim
// is only worth its name while the list of constructors is the real one. A fourth added later would
// leave the coverage assertion passing over a stale set — the enumeration going out of date without
// anything saying so, which is the shape the discipline distrusts most. So the set is derived from
// the source: every function in the non-test files whose body returns a `&Error{…}` literal.
//
// The lexer's own errAt is deliberately excluded: it takes an offset rather than a token and its
// positions are the lexer's, covered by the lexer's tests.
//
// Reads the files with ParseFile over a glob rather than ParseDir, which staticcheck deprecates for
// ignoring build tags. The engine's go.mod is dependency-free by policy, so `go/packages` is not
// available — and it is not wanted either: this package has no build-tagged files, so the deprecation's
// concern does not apply, and a glob makes the *input set* visible to the vacuity check below.
func TestErrorConstructorsAreAccountedFor(t *testing.T) {
	known := map[string]bool{"errAt": true, "errf": true, "unexpectedAt": true}
	found := map[string]bool{}

	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	swept := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") || name == "lexer.go" {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		swept++
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				lit, ok := m.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "Error" {
					found[fn.Name.Name] = true
				}
				return true
			})
			return false // don't descend into nested funcs twice
		})
	}

	// Two vacuity checks, not one. The file count catches a rename or a moved package that leaves
	// the glob matching nothing; the constructor count catches a sweep that reads the files and
	// recognizes nothing in them (a renamed Error type). Either alone is a comparison against an
	// empty set wearing a different disguise.
	if swept < 4 {
		t.Fatalf("swept %d non-test files, want at least 4; the glob found nothing and every "+
			"assertion below would pass vacuously", swept)
	}
	if len(found) == 0 {
		t.Fatal("no error constructors found; the AST sweep is vacuous (renamed Error type?)")
	}
	for name := range found {
		if !known[name] {
			t.Errorf("%s builds a *text.Error but TestErrorsCarryAPosition has no case for it; "+
				"add one and list it here", name)
		}
	}
	for name := range known {
		if !found[name] {
			t.Errorf("%s is listed as an error constructor but builds no *text.Error; the "+
				"coverage claim in TestErrorsCarryAPosition names something that no longer "+
				"exists", name)
		}
	}
}

// TestIndexSpacesAreIndependent pins that the nine spaces do not share a map.
//
// One name in two spaces is legal — `(func $x) (global $x …)` — and a single shared map would
// reject it while passing every duplicate vector, because each vector duplicates within one
// space. The accept direction is where the shared-map bug lives, so it is checked over every pair.
func TestIndexSpacesAreIndependent(t *testing.T) {
	fields := []string{
		`(type $x (func))`,
		`(func $x)`,
		`(global $x (import "m" "g") i32)`,
		`(memory $x 1)`,
		`(table $x 1 funcref)`,
		`(data $x)`,
		// `func` after the bindidx: `elem_list` is not nullable, so a bare `(elem $x)` is a syntax
		// error and every pair containing it failed for that reason rather than for a shared map —
		// seven rows of this cross-product asserting nothing about index spaces (#143).
		`(elem $x func)`,
		`(tag $x)`,
	}
	// Imports first would trip the ordering check, so definitions only, and in an order the
	// ordering check permits: every field here is a definition, and there are no imports except
	// the inline one on the global, which must come before any definition.
	for i, a := range fields {
		for j, b := range fields {
			if i >= j {
				continue
			}
			src := "(module " + a + " " + b + ")"
			if strings.Contains(a, "import") || strings.Contains(b, "import") {
				continue // the inline import would need to precede the definition
			}
			if err := ReadModule([]byte(src)); err != nil {
				t.Errorf("ReadModule(%q) = %v; $x in two different index spaces is legal, "+
					"and a shared map would still pass every duplicate vector", src, err)
			}
		}
	}
	if got := len(fields); got < 8 {
		t.Fatalf("only %d field kinds under test; the sweep is thinner than the nine spaces "+
			"context declares", got)
	}
}

// TestFuncLocalSpaceResetsPerFunction pins that locals do not leak between functions.
//
// `(func (local $x i32)) (func (local $x i32))` is legal: `enter_func` (parser.mly:965) gives each
// function a fresh local space. A parser that kept one space would report a duplicate on the
// second function — and no vector covers it, because the duplicate vectors are all within one
// function.
func TestFuncLocalSpaceResetsPerFunction(t *testing.T) {
	src := `(module (func (local $x i32)) (func (local $x i32)))`
	if err := ReadModule([]byte(src)); err != nil {
		t.Errorf("ReadModule(%q) = %v; each function gets a fresh local space "+
			"(enter_func, parser.mly:965)", src, err)
	}
	// And the negative half, so the reset is not just "the check is gone".
	dup := `(module (func (local $x i32) (local $x i64)))`
	if err := ReadModule([]byte(dup)); err == nil {
		t.Errorf("ReadModule(%q) accepted; two locals named $x in ONE function is a "+
			"duplicate", dup)
	}
}

// TestStructFieldSpaceIsPerType is the same reset property at the field layer.
func TestStructFieldSpaceIsPerType(t *testing.T) {
	ok := `(module (type (struct (field $f i32))) (type (struct (field $f i32))))`
	if err := ReadModule([]byte(ok)); err != nil {
		t.Errorf("ReadModule(%q) = %v; the field space is per struct type (parser.mly:420)",
			ok, err)
	}
	dup := `(module (type (struct (field $f i32) (field $f i64))))`
	if err := ReadModule([]byte(dup)); err == nil {
		t.Errorf("ReadModule(%q) accepted; two fields named $f in one struct is a duplicate", dup)
	}
}

// TestLimitsNatsAreCheckedAtSixtyFourBits is grave #112's control, and it is scoped to the *space*
// of limits positions rather than to the one field that found the defect.
//
// `limits` is `NAT | NAT NAT`, both arms `nat64` (parser.mly:467-468), so the reference reports
// `i64 constant out of range` **from the parser** for a bound that does not fit 64 bits. Before the
// encoder needed the value, `limits` advanced the cursor and read nothing — every over-wide bound was
// accepted. Invisible to the suite twice over: no vector writes a 2^64 limit at all, and an
// accept-direction defect has no `assert_malformed` that can complain (contract §9 G-3).
//
// The space is four positions × two arms: memory and table, each defining or imported, with the
// minimum and the maximum. Each is a *separate call* into `limits` in this reader — memoryField and
// tableField call it inline, the inline-import arms reach it through `memorytype`/`tabletype` — so a
// fix applied at one call site and not the others is exactly what a control scoped to `(memory N)`
// would have missed. The `i64`/`i32` addrtype rows are here because the address type does not change
// the *field's* declared width: `nat64` is what the production says regardless, which is the
// bidirectional-control shape a width parameter threaded from the addrtype would break.
func TestLimitsNatsAreCheckedAtSixtyFourBits(t *testing.T) {
	// 2^64 — the first value `nat64` rejects. Written as the literal rather than computed, because
	// the reference's check is on the decimal text.
	const wide = "18446744073709551616"
	// 2^64-1 — the widest value it accepts, and the accept half of every row below. Without it a
	// reader that rejected *every* two-or-more-digit bound would pass the reject half wholesale.
	const widest = "18446744073709551615"

	for _, tc := range []struct{ field, why string }{
		{"(memory " + wide + ")", "memoryField's own limits call, the minimum — the position the grave was found at"},
		{"(memory 0 " + wide + ")", "the same call, the maximum: limits' second arm, a second nat64"},
		{"(memory i64 " + wide + ")", "addrtype i64 does not widen the field: the production is nat64 either way"},
		{"(memory i32 " + wide + ")", "and addrtype i32 does not narrow it to nat32, which is the reading a width parameter would invite"},
		{"(table " + wide + " funcref)", "tableField's limits call — a separate call site from memoryField's"},
		{"(table 0 " + wide + " funcref)", "and its maximum"},
		{`(memory (import "m" "a") ` + wide + ")", "the inline-import arm, which reaches limits through memorytype rather than inline"},
		{`(memory (import "m" "b") 0 ` + wide + ")", "same, the maximum"},
		{`(table (import "m" "c") ` + wide + " funcref)", "the fourth call site: tabletype, via tableField's import arm"},
		{`(table (import "m" "d") 0 ` + wide + " funcref)", "same, the maximum"},
	} {
		src := "(module " + tc.field + ")"
		err := ReadModule([]byte(src))
		if err == nil {
			t.Errorf("ReadModule(%s) accepted; both limits arms are nat64 (parser.mly:467-468) — %s",
				tc.field, tc.why)
			continue
		}
		// The message is asserted, not just the verdict: `i64` versus `i32` is which production ran,
		// and a limits position checked at 32 bits would reject these too — for the wrong reason,
		// while wrongly rejecting the legal 4294967296 that the accept half below admits.
		if !strings.Contains(err.Error(), "i64 constant out of range") {
			t.Errorf("ReadModule(%s) = %q, want `i64 constant out of range` — %s", tc.field, err, tc.why)
		}
	}

	// The accept half, at the same four call sites. This is what separates "checks at 64 bits" from
	// "rejects large numbers": 2^64-1 is legal in every limits position, and 2^32 is legal too —
	// which is the row that fails if anyone reuses nat32 here.
	for _, tc := range []struct{ field, why string }{
		{"(memory " + widest + ")", "2^64-1 is the widest legal minimum"},
		{"(memory 0 " + widest + ")", "and the widest legal maximum"},
		{"(memory 4294967296)", "2^32 in a limits position is legal — nat64, not nat32"},
		{"(table " + widest + " funcref)", "the table's minimum, same width"},
		{"(table 0 " + widest + " funcref)", "and its maximum"},
		{`(memory (import "m" "a") ` + widest + ")", "the imported memory's, through memorytype"},
		{`(table (import "m" "b") 0 ` + widest + " funcref)", "the imported table's, through tabletype"},
	} {
		src := "(module " + tc.field + ")"
		if err := ReadModule([]byte(src)); err != nil {
			t.Errorf("ReadModule(%s) = %v; want accepted — %s. Whether the *engine* can honour a "+
				"limit this large is validation's question (memory.wast's `memory size must be at "+
				"most 65536 pages`), not this stratum's", tc.field, err, tc.why)
		}
	}
}

// TestModuleFieldIdxIsCheckedAtThirtyTwoBits is the sibling grave #112's sweep turned up: the same
// class — a width the code gets right and nothing asserts — one production over.
//
// `idx`'s NAT arm is `nat32 $1 $sloc` (parser.mly:488), so a 33-bit index is `i32 constant out of
// range` from the parser. types.go's `idx` comment says as much and adds that "no vector puts an
// over-wide index [in a module field] without an instruction body in the same module" — a true
// statement about the oracle that left the property untested at every module-field position. The
// instruction and label positions *are* covered (instr_test.go's lane vectors, label_test.go's
// `br 0x100000000`); the module fields were not covered anywhere.
//
// Scoped to the *space* of module-field idx positions rather than to the one that prompted it: all
// eight export/start/elem/data/subtype/typeuse sites reached without an instruction body, each with
// the reject at 2^32 and the accept at 2^32-1. The accept half is not decoration — it is what
// separates "checks at 32 bits" from "rejects long digit strings", and it is the half that would
// fail if anyone reached for nat64 here on the reasoning that limits uses it (#112's fix is the
// nearby precedent that makes that mistake plausible).
func TestModuleFieldIdxIsCheckedAtThirtyTwoBits(t *testing.T) {
	// Each row is a module field with an idx in it, written twice: `%s` takes the index. No
	// instruction bodies, which is exactly the region types.go names as unsampled.
	for _, tc := range []struct{ form, why string }{
		{`(module (export "e" (func %s)))`, "export_desc:1235 — the func index space"},
		{`(module (export "e" (table %s)))`, "export_desc:1236"},
		{`(module (export "e" (memory %s)))`, "export_desc:1237"},
		{`(module (export "e" (global %s)))`, "export_desc:1238"},
		{`(module (export "e" (tag %s)))`, "export_desc:1239"},
		{`(module (start %s))`, "start:1265, the single-index field"},
		{`(module (elem declare func %s))`, "elemidx_list, reached past the declare keyword"},
		{`(module (data (memory %s) (i32.const 0)))`, "data's memory index — a nested field, so the idx is not the field's first token"},
		{`(module (elem (table %s) (i32.const 0) func))`, "elem's table index, the same nesting one field over"},
		{`(module (type (sub %s (func))))`, "subtype's idx_list (parser.mly:453) — a type index in a type definition"},
		{`(module (func (type %s)))`, "typeuse:1218, which resolves through the type table rather than deferring"},
	} {
		// 2^32 — the first value nat32 rejects.
		over := strings.Replace(tc.form, "%s", "4294967296", 1)
		err := ReadModule([]byte(over))
		if err == nil {
			t.Errorf("ReadModule(%s) accepted; idx's NAT arm is nat32 (parser.mly:488) — %s", over, tc.why)
		} else if !strings.Contains(err.Error(), "i32 constant out of range") {
			t.Errorf("ReadModule(%s) = %q, want `i32 constant out of range` — %s", over, err, tc.why)
		}
		// 2^32-1 — the widest legal index, and legal *here* even where it names nothing: an index
		// out of range for the module is the validator's `unknown func`, not this stratum's width
		// error. That distinction is the reason the accept half is written per position.
		widest := strings.Replace(tc.form, "%s", "4294967295", 1)
		if err := ReadModule([]byte(widest)); err != nil {
			t.Errorf("ReadModule(%s) = %v; 4294967295 fits nat32, and an index naming nothing is "+
				"validation's `unknown …` rather than a parse error — %s", widest, err, tc.why)
		}
	}
}

// TestNamedParamTakesExactlyOneType pins a rejection the grammar makes and no message announces.
//
// `(param $x i32 i64)` is not legal — the bindidx arm (parser.mly:1436) takes a single valtype,
// unlike the list arm. Same for `(field $f i32 i64)` at :1422. Both fall out of following the arms
// and would be lost by a paraphrase that treated the named form as "a list with a name".
func TestNamedParamTakesExactlyOneType(t *testing.T) {
	for _, src := range []string{
		`(module (type (func (param $x i32 i64))))`,
		`(module (func (param $x i32 i64)))`,
		`(module (func (local $x i32 i64)))`,
		`(module (type (struct (field $f i32 i64))))`,
	} {
		if err := ReadModule([]byte(src)); err == nil {
			t.Errorf("ReadModule(%q) accepted; the named arm takes exactly one type "+
				"(parser.mly:1436/1422), and no error message says so — which is why this "+
				"is a test", src)
		}
	}
}

// TestResultHasNoNamedForm pins the param/result asymmetry.
//
// `(param $x i32)` is legal sugar; `(result $x i32)` is not. Compare parser.mly:1436 with :1443 —
// functype_result has no bindidx arm. An easy thing to "fix" into a bug on the grounds of
// symmetry.
func TestResultHasNoNamedForm(t *testing.T) {
	if err := ReadModule([]byte(`(module (type (func (param $x i32))))`)); err != nil {
		t.Errorf("the named param form is legal (parser.mly:1436): %v", err)
	}
	if err := ReadModule([]byte(`(module (type (func (result $x i32))))`)); err == nil {
		t.Error("`(result $x i32)` accepted; functype_result (parser.mly:1441-1444) has no " +
			"bindidx arm, unlike functype's param arms")
	}
}

// TestParamsMayNotFollowResults pins the ordering functype encodes structurally.
//
// The reference splits functype from functype_result precisely so that params cannot appear after
// results. A single loop accepting either in any order passes every vector and admits
// `(func (result i32) (param i32))`.
func TestParamsMayNotFollowResults(t *testing.T) {
	if err := ReadModule([]byte(`(module (type (func (param i32) (result i32))))`)); err != nil {
		t.Errorf("params then results is legal: %v", err)
	}
	if err := ReadModule([]byte(`(module (type (func (result i32) (param i32))))`)); err == nil {
		t.Error("`(result i32) (param i32)` accepted; functype:1433 must be exhausted before " +
			"functype_result:1441 begins, so a result cannot precede a param")
	}
}

// TestBlockParamHasNoNamedForm is the grave's control, and it is the *sibling* of
// TestNamedParamTakesExactlyOneType rather than a copy of it.
//
// A block's parameter list is `block_param_body` (parser.mly:754-758), whose param arm is
// `LPAR PARAM valtype_list RPAR` — **one arm, no bindidx**. A function's is `functype`
// (:430-:438), which has *two* param arms, the second being the named sugar `LPAR PARAM bindidx
// valtype RPAR` (:436). So `(param $x i32)` is legal in a `(func …)` signature and malformed in a
// `block`, and the same two lines of grammar decide both.
//
// `blockSignature` delegated to `functype` and so accepted the block form — and the comment on it
// asserted the two prefixes were the same production shape, which is the defect stated as the rule.
// Six vectors cover it and three of them are this stratum's; the other three are the folded
// spelling, which is #64's and still red.
//
// Falsified by restoring `return p.functype()`: the three flat rows below go from `unexpected
// token` to accepted, and block.wast:1475 / loop.wast:783 / if.wast:1513 come back red.
func TestBlockParamHasNoNamedForm(t *testing.T) {
	// The reject direction, one row per blockinstr arm — scoped to the family rather than to the
	// three keywords the vectors happen to use, since all four share blockSignature.
	for _, tc := range []struct{ src, why string }{
		{`(module (func (param i32) (result i32) block (param $x i32) end))`, "block.wast:1475"},
		{`(module (func (param i32) (result i32) loop (param $x i32) end))`, "loop.wast:783"},
		{`(module (func (param i32) (result i32) if (param $x i32) end))`, "if.wast:1513"},
		{
			`(module (func try_table (param $x i32) end))`,
			"synthetic: try_table reaches the same blockSignature via handler_block_param_body " +
				"(parser.mly:780-784), which likewise has no bindidx arm; no vector writes it",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; a block's param list has no named form "+
				"(block_param_body, parser.mly:756) — [%s]", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "unexpected token") {
			t.Errorf("ReadModule(%q) = %q, want `unexpected token` — [%s]", tc.src, err, tc.why)
		}
	}

	// The accept direction, which is the half that catches over-correction: narrowing the param
	// arm too far, or copying the narrowing into the *result* chain, where it does not belong.
	// `block_result_body` (:762) and `functype_result` (:443) really are the same shape.
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func block (param i32 i64) (param f32) (result i32) i32.const 0 end drop))`,
			"synthetic: the unnamed arm repeats and precedes the results, per :756/:762",
		},
		{
			`(module (func (param $x i32) block end))`,
			"synthetic: the *function's* named param is still legal — the narrowing is the " +
				"block's alone (:436 versus :756)",
		},
		{
			`(module (type (func (result i32))) (func block (type 0) i32.const 0 end drop))`,
			"synthetic: block's first arm is `typeuse block_param_body` (:741)",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — [%s]", tc.src, err, tc.why)
		}
	}

	// And the asymmetry's other half, so the two chains are pinned against each other rather than
	// each against a recollection: `(result $x …)` is illegal in *both*.
	for _, src := range []string{
		`(module (func block (result $x i32) end))`,
		`(module (type (func (result $x i32))))`,
	} {
		if err := ReadModule([]byte(src)); err == nil {
			t.Errorf("ReadModule(%q) accepted; neither result chain has a bindidx arm "+
				"(parser.mly:762, :443)", src)
		}
	}
}

// TestFoldedAndFlatSignaturesAgree pins that the folded family *reaches* orderedTypeUse rather than
// reimplementing it.
//
// **Five production families share one ordered chain** — optional `typeuse`, then `(param …)*`, then
// `(result …)*`, then a tail that differs per family:
//
//	block_param_body / block_result_body                :754 / :760   tail instr_list
//	handler_block_param_body / handler_block_result_body :780 / :786   tail handler clauses
//	if_block_param_body / if_block_result_body           :879 / :885   tail if_
//	callexpr_params / callexpr_results                   :851 / :858   tail expr_list
//	callinstr_params_instr_list / …_results_instr_list   :712 / :720   tail instr_list
//
// Ten productions, one shape. orderedTypeUse is the one reader, parameterized by tail — and the
// risk that creates is the one 0006 names: **two places knowing the same fact**. A folded reader
// that read its own params-then-results chain would agree with the flat one on every case anyone
// thought to write, and diverge on the case nobody did. Board counts cannot see it: both spellings
// would still be accepted or rejected, just for parallel reasons.
//
// So the assertion is *agreement between the two spellings*, not a verdict on either. Each row is
// one signature written twice — flat with `end`, folded in parens — and the control fails when the
// two disagree, whichever way. That makes it blind to which verdict is right and sensitive to
// exactly the thing being claimed, which is the point: TestBlockParamHasNoNamedForm already pins
// the verdicts.
//
// Scoped to the shape rather than to a family, since all four block-ish leaders (`block`, `loop`,
// `if`, `try_table`) route through the same reader — a fifth added upstream would need a row here,
// and TestExpr1LeadersMatchTheReference is what catches its arrival.
//
// Falsified twice, and **the first attempt at the second falsification is the instructive part**.
// Pointing foldedBlock at `functype` — the delegation that was grave #63 — fails eleven rows in
// *both* directions: `(param $x i32)` flat-rejects and folded-accepts, `(type 0)` does the reverse,
// because functype has the named arm and no typeuse prefix. Good.
//
// The second was meant to be "drop the `(param …)*` loop", and I first dropped it from
// orderedTypeUse itself. **That passed**, and it had to: mutating the *shared* reader moves both
// spellings together, which is precisely the property this control asserts. It also broke four
// other tests and cost the board 4 vectors, so it was not a silent pass — but as a falsification of
// *this* control it proves nothing, and reading a green there as "the control is weak" would have
// been the wrong lesson. An agreement control is falsified by making the two paths **diverge**, not
// by breaking the thing they share; a mutation that keeps them equal is outside its charter and a
// different control's business. Redone as a divergent copy — `foldedSigCopy` with the typeuse arm
// and the results chain but no param loop — it fails the six param rows, folded rejecting where
// flat accepts.
//
// Which is the shape of an agreement control generally: it is blind by design to anything that
// moves both operands, so it must be *paired* with a control that pins one of them. That pairing is
// TestBlockParamHasNoNamedForm, and the four tests the bad mutation broke are what caught it.
func TestFoldedAndFlatSignaturesAgree(t *testing.T) {
	// Signature bodies, each legal-or-not on its own merits — the control does not care which,
	// only that both spellings say the same thing. The mix is deliberate: the named-param and
	// result-before-param rows are the *reject* side, the rest the accept side, so a copy that
	// diverged in either direction is caught.
	sigs := []struct{ sig, why string }{
		{``, "the empty signature, both bodies' base case"},
		{`(param i32)`, "one unnamed param"},
		{`(param i32 i64)`, "valtype_list, more than one type in one clause"},
		{`(param i32) (param i64)`, "the clause repeats — :756 is a list, not one arm"},
		{`(result i32)`, "one result"},
		{`(result i32) (result i64)`, "the result clause repeats too"},
		{`(param i32) (result i64)`, "the ordered case, params before results"},
		{`(result i64) (param i32)`, "the *disordered* case — reject, :760 follows :754"},
		{`(param $x i32)`, "the named form — reject, block_param_body has no bindidx arm (#63)"},
		{`(result $x i32)`, "no result chain has a bindidx arm"},
		{`(type 0)`, "the typeuse arm, which precedes both clauses"},
		{`(type 0) (param i32) (result i64)`, "typeuse and both clauses, the full chain"},
		{`(type 0) (result i64) (param i32)`, "typeuse present and the clauses disordered"},
	}
	// One row per leader, flat and folded. `try_table` reaches the same reader through
	// handler_block_*, `if` through if_block_* — different tails, same prefix, which is the whole
	// claim.
	forms := []struct {
		leader     string
		flat, fold string
	}{
		{"block", `(module (type (func)) (func block %s end))`, `(module (type (func)) (func (block %s)))`},
		{"loop", `(module (type (func)) (func loop %s end))`, `(module (type (func)) (func (loop %s)))`},
		{
			"if",
			`(module (type (func)) (func if %s end))`,
			`(module (type (func)) (func (if %s (then))))`,
		},
		{
			"try_table",
			`(module (type (func)) (func try_table %s end))`,
			`(module (type (func)) (func (try_table %s)))`,
		},
	}
	checked := 0
	for _, f := range forms {
		for _, s := range sigs {
			flatSrc := strings.Replace(f.flat, "%s", s.sig, 1)
			foldSrc := strings.Replace(f.fold, "%s", s.sig, 1)
			flatErr := ReadModule([]byte(flatSrc)) != nil
			foldErr := ReadModule([]byte(foldSrc)) != nil
			checked++
			if flatErr != foldErr {
				t.Errorf("%s signature %q: flat rejected=%v, folded rejected=%v — the two "+
					"spellings share one signature reader (orderedTypeUse), so a disagreement "+
					"means the folded path reimplemented the chain [%s]\n  flat:   %s\n  folded: %s",
					f.leader, s.sig, flatErr, foldErr, s.why, flatSrc, foldSrc)
			}
		}
	}
	// Vacuity check: an agreement over zero comparisons agrees perfectly.
	if want := len(forms) * len(sigs); checked != want {
		t.Fatalf("compared %d signature pairs, want %d — the loop above skipped rows and an "+
			"agreement over the ones it kept says nothing about the ones it dropped", checked, want)
	}
}

// TestFoldedIfRequiresThen pins `if_`'s mandatory arm (parser.mly:891-898).
//
// Both sugar arms of `if_` require `(then …)`: `LPAR THEN instr_list RPAR` alone (:896) or followed
// by `LPAR ELSE instr_list RPAR` (:893). There is no arm without it, and the first arm `expr if_`
// (:891) is right-recursive over *operands*, so a folded `if`'s condition must itself be folded.
//
// `if.wast:1561` is the vector for the operand half — `(if (i32.const 0) (then))` is legal and a
// bare `i32.const 0` before the `(then)` is not, because `expr` requires the paren. The rest is
// synthetic: the suite has no vector for a `(if)` with no arms at all, which is exactly the kind of
// hole a reader written from the vectors would leave open.
func TestFoldedIfRequiresThen(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (func (if (then))))`, "the one-armed sugar arm, :896"},
		{`(module (func (if (then) (else))))`, "the two-armed arm, :893"},
		{`(module (func (if (then (nop)) (else (nop)))))`, "both arms with bodies"},
		{
			`(module (func (if (i32.const 0) (then))))`,
			"if.wast:1561 — the `expr if_` arm (:891), condition folded",
		},
		{
			`(module (func (if (result i32) (then (i32.const 0)) (else (i32.const 1)))))`,
			"synthetic: the signature precedes the arms, if_block_result_body :885",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — [%s]", tc.src, err, tc.why)
		}
	}
	for _, tc := range []struct{ src, why string }{
		{`(module (func (if)))`, "synthetic: no arm of if_ is empty — `(then …)` is mandatory"},
		{`(module (func (if (else))))`, "synthetic: `(else …)` cannot appear without `(then …)`"},
		{
			`(module (func (if i32.const 0 (then))))`,
			"synthetic: `expr if_` takes an *expr*, and a bare mnemonic is not one (:891)",
		},
		{
			`(module (func (if (then) (else) (then))))`,
			"synthetic: if_ has no third arm, so the list ends after the else",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err == nil {
			t.Errorf("ReadModule(%q) accepted — [%s]", tc.src, tc.why)
		}
	}
}

// TestBlockTerminatorsEndTheList is atBlockTerminator's control, named in that function's comment.
//
// `instr_list`'s follow set inside a block is `END` and `ELSE` (parser.mly:727-738), which menhir
// derives and a recursive-descent reader must state. Getting it wrong does not accept anything
// wrong — it reports the *boundary* at a token this stratum reads perfectly well, which is the
// unimplemented bucket claiming finished work. That is why the reject rows below assert the
// message and not merely the verdict.
//
// The `(catch …)` clauses are deliberately **not** terminators: they precede the body
// (`handler_block_body`, :792-:806), so `handlerBlock` has already consumed them by the time
// instrList runs, and a `(catch …)` appearing after an instruction is a syntax error the grammar
// wants reported. A reader that stopped on them would accept `try_table nop (catch 0 0) end`.
//
// Falsified three ways: dropping `kwEnd` makes every accept row below report the boundary at
// `"end"`; dropping `kwElse` does the same for the else row only; *adding* kwCatch makes the last
// reject row accept.
func TestBlockTerminatorsEndTheList(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (func block nop end))`, "END ends the nested list"},
		{`(module (func i32.const 0 if nop else nop end))`, "ELSE ends the then-list"},
		{`(module (func block block nop end nop end))`, "two levels, the inner END is the inner list's"},
		{`(module (func try_table (catch 0 0) nop end))`, "the clauses precede the body"},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — %s", tc.src, err, tc.why)
		}
	}
	// A handler clause out of position, which must be `unexpected token` and not the boundary:
	// the clauses are in no `expr1` arm, so #64 will never grow a reader for one, and filing it as
	// unimplemented promises work that cannot arrive.
	//
	// Scoped to all four clause keywords, not the two the vectors write — the fault is that a
	// clause appears where no production admits one, and that is true of the whole set.
	//
	// **The last row is the discriminating one and it was found by a falsification run that did not
	// fail.** The rejected alternative fix — put the clauses in atBlockTerminator instead of in
	// bodyBoundary — passes every other row here, so this control as first written could not tell
	// the two apart while its comment claimed it could. A folded *operand* reaches bodyBoundary
	// through `expr`'s operand loop (parser.go, expr:1254) rather than through instrList, which a
	// terminator set cannot see: under that variant `(func (drop (catch_all)))` reports
	// `unimplemented`. Same class as the three-deep precedence claim this PR also retracted — a
	// structural argument no assertion held. Print the verdicts for both candidates before writing
	// down which layer owns a fault.
	for _, tc := range []struct{ src, why string }{
		{`(module (func try_table nop (catch 0 0) end))`, "synthetic: after the body has begun"},
		{`(module (func (catch_all)))`, "try_table.wast:366"},
		{`(module (tag $e) (func (catch $e)))`, "try_table.wast:371"},
		{
			`(module (func (catch_ref 0 0)))`,
			"synthetic: catch_ref is in no expr1 arm either; no vector writes it",
		},
		{
			`(module (func (catch_all_ref 0)))`,
			"synthetic: the fourth clause, likewise unreachable in instruction position",
		},
		{
			`(module (func (drop (catch_all))))`,
			"synthetic, and the only row that separates the two candidate fixes: a folded " +
				"operand reaches bodyBoundary through expr's loop, not through instrList",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; handler clauses appear only in "+
				"handler_block_body (parser.mly:792-806) and try_block_handler_body (:929), "+
				"both of which consume them before the instr_list — [%s]", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "unexpected token") {
			t.Errorf("ReadModule(%q) = %q, want `unexpected token`; a clause out of position is "+
				"malformed on the merits and is in no expr1 arm, so filing it as unimplemented "+
				"would park it in #64's bucket where finishing #64 cannot answer it — [%s]",
				tc.src, err, tc.why)
		}
	}
}

// TestBlockEndIsRequired pins the one place a missing token is *not* the boundary.
//
// `END` is a required terminal of all five blockinstr arms (parser.mly:727-738), so `(func block)`
// is malformed on the merits and this stratum can say so. Reporting `unimplemented` there would
// be the wrong-layer error in the flattering direction — a module the reference rejects, parked in
// the work plan as though finishing #64 would one day make it legal. Sibling of
// TestMissingMandatoryBodyIsNotABoundary above, and the same argument.
// **The first five rows below cannot falsify the claim, and saying so is the point.** They all put
// a `)` where the END should be, and bodyBoundary *already* answers a closing paren with
// `unexpected token` (see its header) — so `return p.unexpected()` and `return p.bodyBoundary()`
// return the identical error on every one of them. Swapping the two passed the whole table, which
// is a green surviving the bug it names for the second time in this PR, and the cause is the same
// each time: a partition asserted from case labels rather than checked against what the code
// returns.
//
// The rows that *do* discriminate were found by printing both variants: a token that is neither
// `)` nor `end`. `else` after a non-`if` block and a truncated source are the two shapes — under
// the variant they read `unimplemented: instruction body at "else"` and `… at ""`, the second being
// the engine claiming that finishing #64 would make an unterminated file legal. The five paren rows
// are kept, because they are the spellings the reference's own vectors would use and a future
// change to bodyBoundary's paren handling would make them live.
func TestBlockEndIsRequired(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		// The paren rows: correct, and *not* discriminating — see the header.
		{`(module (func block))`, "the plainest form"},
		{`(module (func loop nop))`, "with a body"},
		{`(module (func i32.const 0 if nop else nop))`, "the else arm's END is missing too"},
		{`(module (func try_table (catch 0 0)))`, "try_table's arm, :736"},
		{`(module (func block block end))`, "the *outer* END is missing"},
		// The discriminating rows: the token in END's place is neither `)` nor `end`.
		{
			`(module (func block nop else nop end))`,
			"discriminating: `else` belongs only to the IF arms (:733), so on a block it is a " +
				"token in END's position that bodyBoundary would call unimplemented",
		},
		{`(module (func loop else end))`, "discriminating: the same on loop"},
		{
			`(module (func block nop`,
			"discriminating: EOF in END's place — the variant reports `unimplemented … at \"\"`, " +
				"which claims a truncated file becomes legal once #64 lands",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; END is a required terminal of every "+
				"blockinstr arm (parser.mly:727-738) — %s", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "unexpected token") {
			t.Errorf("ReadModule(%q) = %q, want `unexpected token` — a missing required "+
				"terminal is this stratum's own syntax error, not a boundary — %s",
				tc.src, err, tc.why)
		}
	}
}

// TestEmptyOpenerRejectsAnyEndLabel pins labeling_end_opt's harder arm.
//
// The anonymous `labeling_opt` arm is `List.iter (fun x -> error x.at "mismatching label") xs`
// (parser.mly:512-513) — an *unconditional* error over the end-labels. So `block end $l` is a
// mismatch against nothing rather than an unknown label, and a reader that only compared when the
// opener had a name would accept it. The named arm compares textually (`x.it <> $1.it`, :518).
//
// `if … else … end` is the case with two end-labels, and the reference checks both against the one
// opener by concatenating them (`$5 @ $8`, :734) — so `if $a else $b end $a` is a mismatch at
// `$b` even though the final label agrees.
func TestEmptyOpenerRejectsAnyEndLabel(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (func block end $l))`, "block.wast:1484, the empty-opener arm"},
		{`(module (func block $a end $l))`, "block.wast:1488, the named arm disagreeing"},
		{`(module (func loop end $l))`, "loop.wast:791"},
		{`(module (func i32.const 0 if end $l))`, "if.wast:1521"},
		{
			`(module (func i32.const 0 if $a else $b end $a))`,
			"synthetic: the else-clause's own labeling_end_opt is checked against the same " +
				"opener (`$5 @ $8`, parser.mly:734), so $b fails though $a agrees",
		},
		{
			`(module (func try_table end $l))`,
			"synthetic: try_table's arm (:736) carries labeling_end_opt like the rest; no " +
				"vector writes a mismatched try_table label",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; want `mismatching label` — [%s]", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "mismatching label") {
			t.Errorf("ReadModule(%q) = %q, want `mismatching label` — [%s]", tc.src, err, tc.why)
		}
	}

	// The accept rows, which is where an over-eager check shows up: an *absent* end-label is
	// always legal (`labeling_end_opt`'s empty arm, :522), whatever the opener.
	for _, tc := range []struct{ src, why string }{
		{`(module (func block end))`, "empty opener, empty end — the common case"},
		{`(module (func block $a end))`, "named opener, empty end: :522's empty arm"},
		{`(module (func block $a end $a))`, "named opener agreeing textually, :518"},
		{
			`(module (func i32.const 0 if $a else $a end $a))`,
			"both end-labels agree with the opener",
		},
		{`(module (func i32.const 0 if $a else end))`, "the else's end-label is optional too"},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — %s", tc.src, err, tc.why)
		}
	}
}

// TestEndLabelFaultsAtTheEndLabel pins *where* the mismatch is reported.
//
// `error x.at` (parser.mly:513/:518) points at the end-label — the offending token — not at the
// opener that named something else. Position is not covered by any expected string, so this is the
// half of the message the oracle cannot see, and print-don't-trust applies: the column is checked
// against the source rather than reasoned about.
// The position is read off the *Error struct*, not the message: `Error()` returns Msg alone
// (lexer.go:120), so a control matching the rendered text would be asserting nothing about the
// offset. Same mechanism TestErrorsCarryAPosition uses, and for the same reason — it is the only
// channel the position is actually on.
func TestEndLabelFaultsAtTheEndLabel(t *testing.T) {
	for _, tc := range []struct{ src, wantAt, why string }{
		{`(module (func block $a end $b))`, "$b", "the named arm, `error x.at` at :518"},
		{`(module (func block end $b))`, "$b", "the empty arm, `error x.at` at :513"},
		{
			`(module (func i32.const 0 if $a else $b end $a))`,
			"$b",
			"the else-clause's label is the offender; the final $a agrees",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted", tc.src)
			continue
		}
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("ReadModule(%q) = %v, not a *text.Error, so it carries no position",
				tc.src, err)
			continue
		}
		want := strings.Index(tc.src, tc.wantAt)
		if want < 0 {
			t.Fatalf("test bug: %q not in %q", tc.wantAt, tc.src)
		}
		if e.Offset != want {
			t.Errorf("ReadModule(%q) faulted at offset %d (%q), want %d (%q) — %s; reporting "+
				"the opener's position instead would be testimony about the wrong token",
				tc.src, e.Offset, at(tc.src, e.Offset), want, tc.wantAt, tc.why)
		}
	}
}

// TestLabelsCompareDecodedNames pins that both label readers go through `bindidx`, which decodes.
//
// The grave this was written against compared `Token.Text` — the raw lexeme — where the reference
// compares `x.it <> $1.it` on values that `var` (parser.mly:48-51) has already `Utf8.decode`d.
// A spelling comparison is wrong in *both* directions at once, which is why the two halves below
// are one control rather than two: it accepts a label whose bytes are not UTF-8, and it rejects a
// pair of labels that are the same name spelled two ways.
//
// The second half is the direction with no vector and the one a spelling comparison passes by
// accident on every vector that exists: `$a` (lexer.mll:815, the `id` arm) and `$"a"` (:816, the
// `string` arm) are two spellings of the *same* name, so `Text` differs while the decoded value
// agrees. `id.wast` proves the two spellings are interchangeable as *bindings* (:8-:11 bind
// `$"^"`-style names and use them); that they are interchangeable across an opener/end-label pair
// is the entailment, and it is derived rather than cited for that reason.
func TestLabelsCompareDecodedNames(t *testing.T) {
	// Reject: a label whose decoded bytes are not UTF-8. One row per blockinstr arm, because the
	// grave was a *missing call* and a missing call is per-arm — four arms, and `labelingOpt` is
	// reached from all of them. The end-label position too, which is labelingEndOpt's own read.
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func block $"\ff" end))`,
			"the opener's label. id.wast:31 is the same escape in a func's binding position; " +
				"the entailment is that `labeling_opt`'s named arm is the same `bindidx` " +
				"production (:515), so the same bytes are malformed one production away",
		},
		{`(module (func loop $"\ff" end))`, "derived, the LOOP arm (:727)"},
		{`(module (func i32.const 0 if $"\ff" else end))`, "derived, the IF arm (:729)"},
		{`(module (func try_table $"\ff" end))`, "derived, the TRY_TABLE arm (:735)"},
		{
			`(module (func block end $"\ff"))`,
			"the *end*-label, `labeling_end_opt` = `| bindidx` (:523) — labelingEndOpt's own " +
				"read, which the opener's fix does not cover",
		},
		{
			`(module (func block $"\ff" end $"\ff"))`,
			"both labels bad and identical. A spelling comparison finds them equal and " +
				"accepts, which is why this row exists separately from the two above",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; want `malformed UTF-8 encoding` — [%s]", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "malformed UTF-8 encoding") {
			t.Errorf("ReadModule(%q) = %q, want `malformed UTF-8 encoding` — [%s]", tc.src, err, tc.why)
		}
	}

	// Accept: the same name in two spellings. This is the half that a raw-lexeme comparison
	// *rejects*, and no assert_malformed can ever notice a wrongly-rejected module.
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func block $a end $"a"))`,
			`derived: id.wast establishes $"..." as a spelling of a name; $a and $"a" decode ` +
				"to the same bytes, so :518's `x.it <> $1.it` is false and this matches",
		},
		{`(module (func block $"a" end $a))`, "the mirror, so a one-sided decode fails one row"},
		{`(module (func block $"a" end $"a"))`, "both in the string spelling"},
		{
			`(module (func i32.const 0 if $a else $"a" end $a))`,
			"the else-clause's end-label goes through the same reader (`$5 @ $8`, :734)",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — %s", tc.src, err, tc.why)
		}
	}
}

// TestLabelDecodePrecedesComparison pins the *order* of the two checks, which is the reference's
// rather than a preference.
//
// `bindidx` is reduced when its VAR is read, so `var`'s decode has already errored by the time
// blockinstr's action applies the `labeling_opt` closure that iterates `xs` and reports the
// mismatch. So an end-label that is *both* malformed and mismatched is reported as malformed: it
// is not well-formed enough to disagree. The reverse order is a plausible reading — check the
// cheap textual comparison first — and it is wrong, so it gets an assertion rather than a comment.
//
// Synthetic: no vector writes a label that is simultaneously bad UTF-8 and a mismatch, because
// each of the two facts alone is enough for the malformedness the vector is asserting. Authority
// is the grammar's reduction order, not a wast line.
func TestLabelDecodePrecedesComparison(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func block $a end $"\ff"))`,
			"named opener, malformed end-label: the names also differ, so a comparison-first " +
				"reader says `mismatching label`",
		},
		{
			`(module (func block end $"\ff"))`,
			"empty opener, malformed end-label: :512-513's arm errors on *any* end-label " +
				"unconditionally, so a comparison-first reader says `mismatching label` here too",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted — [%s]", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "malformed UTF-8 encoding") {
			t.Errorf("ReadModule(%q) = %q, want `malformed UTF-8 encoding` — [%s]", tc.src, err, tc.why)
		}
	}
}

// TestEmptyIdentifierHasNoSpelling pins the premise labelingOpt's "" sentinel rests on.
//
// labelingOpt returns "" for the anonymous arm and a decoded name otherwise, which is only
// unambiguous because no VarTok decodes to the empty string. That is true, and it is true in
// *another file*: both `$`-forms reject it at the lexer (`empty identifier`, lexer.mll:817 for
// `$""` and :819 for a bare `$`). An invariant a function depends on and does not enforce is a
// claim about code it does not contain, so it is asserted here rather than trusted — if the lexer
// ever admitted an empty identifier, `block $"" end` would silently read as an anonymous opener
// and `block $"" end $""` would be a mismatch against nothing.
func TestEmptyIdentifierHasNoSpelling(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (func block $"" end))`, `lexer.mll:817, the string spelling decoding to ""`},
		{`(module (func block $ end))`, "lexer.mll:819, a bare `$`"},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; want `empty identifier` — [%s]. labelingOpt's \"\" "+
				"sentinel is ambiguous if this ever parses", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "empty identifier") {
			t.Errorf("ReadModule(%q) = %q, want `empty identifier` — [%s]", tc.src, err, tc.why)
		}
	}
}

// TestElemListArmsAreNotShadowedByTheOffsetSugar is grave #75's control, and it is a **product** —
// both arms of `elem_list` crossed with both spellings of an offset — rather than the three
// vectors that fell.
//
// The defect: `elemField`'s offset branch tested `at(LParen) && !peek2Keyword(kwItem)` and
// concluded "an offset". But `elem_list`'s second arm is `reftype elemexpr_list` (parser.mly:1155),
// and a reftype has a parenthesized spelling — `(ref func)`, `(ref null func)`, `(ref $t)` — led by
// neither `item` nor an instruction. So the reftype was read as an offset and the arm was shadowed
// entirely, rejecting `elem.wast:539`, `:573` and `array.wast:219`. All three must-succeed, so
// **no `assert_malformed` could see it**; it surfaced only when #69 raised the accept oracle from
// 7 modules to 2130.
//
// **Three vectors cannot certify the fix, and the product is why.** All three write the reftype
// arm with *no* offset, so a "fix" that stopped treating any paren as an offset would pass all
// three and lose the offset-sugar arm — which the corpus does exercise, and which is the arm the
// original lookahead existed to reach. The rows below therefore cross:
//
//	elem_list arm            offset spelling        row
//	elemkind elemidx_list    none (passive)         `(elem func $f)`
//	elemkind elemidx_list    (offset …)             `(elem (offset (i32.const 0)) func $f)`
//	elemkind elemidx_list    bare expr              `(elem (i32.const 0) func $f)`
//	reftype elemexpr_list    none (passive)         `(elem (ref func) (ref.func 0))`
//	reftype elemexpr_list    (offset …)             `(elem (offset (i32.const 0)) (ref func) …)`
//	reftype elemexpr_list    bare expr              `(elem (i32.const 0) (ref func) …)`
//
// The bare-expr column is the one that makes the lookahead a *partition* rather than a priority
// ordering: `offset` is `LPAR OFFSET constexpr RPAR | expr` (:1091-1093), so an offset may be any
// folded instruction, and `(i32.const 0) (ref func)` has a paren in both positions. What separates
// them is that `ref` is not an instruction mnemonic — `REF` is its own token (lexer.mll:180) while
// `ref.func`/`ref.null` are others (:326-327) — so `(ref …)` cannot begin an expr at all. That
// premise is a fact about the *keyword table*, and `TestEveryPlaininstrKindIsInTheKeywordTable`
// already machine-checks the table against lexer.mll, so it is cited rather than re-asserted here.
//
// Falsified by running each defect:
//   - Restoring the two-condition test (dropping `!p.atReftypeStart()`): the three reftype rows
//     fail with `unexpected token` — the grave, reproduced, and the board's three vectors return.
//   - Dropping the `at(LParen)` test so the offset branch never runs: the four offset rows fail.
//     This is the over-correction the three vectors would have licensed.
//   - Dropping `!peek2Keyword(kwItem)`: **nothing fails, and that is a finding rather than a gap
//     in this table.** The condition predates #75 and is in the product because a control scoped to
//     one lookahead in a three-way decision inherits the blind spot of the other two — so running
//     the defect is what discovered that this third lookahead discriminates nothing. A `panic()` in
//     its complementary branch never fired across the suite: `elemexpr_list` follows a *mandatory*
//     reftype (parser.mly:1155), so `(item …)` cannot be the first thing after `elem`. Both
//     readings reject `(elem (item …))` with the same message, printed. The `item` rows below are
//     therefore honest about what they pin — the elemexpr arm in its legal position — and no row
//     here claims to falsify a condition that cannot be falsified. See the note at the call site.
//
// *A control falsified in one field is not falsified* — and running the third defect is the only
// reason this is known rather than assumed.
func TestElemListArmsAreNotShadowedByTheOffsetSugar(t *testing.T) {
	// One table so the active arms have a target, one func so `ref.func 0` resolves, and the table
	// typed `(ref func)` because that is how the cited vectors write it.
	const prefix = "(module (func) (table 1 (ref func) (ref.func 0)) "
	cases := []struct {
		name, field, why string
	}{
		// Arm 1 — `elemkind elemidx_list`. The arm that worked, kept because a control scoped to
		// the broken arm cannot see a fix that breaks the working one.
		{
			"elemkind, passive", "(elem func 0)",
			"elem.wast:12 — `(elem func)`, the passive elemkind arm, unaffected by #75",
		},
		{
			"elemkind, (offset ...)", "(elem (offset (i32.const 0)) func 0)",
			"elem.wast:37 — the explicit offset spelling with the elemkind arm",
		},
		{
			"elemkind, bare expr offset", "(elem (i32.const 0) func 0)",
			"elem.wast:41 — offset as a bare expr (parser.mly:1093), the spelling that makes the " +
				"paren uninformative",
		},

		// Arm 2 — `reftype elemexpr_list`, the shadowed one. Three spellings of the reftype,
		// because `atReftypeStart` admits an abbreviated keyword as well as `(ref …)` and only the
		// parenthesized spellings were ever at risk.
		{
			"reftype (ref func), passive", "(elem (ref func) (ref.func 0))",
			"elem.wast:539 — the vector the grave was filed on",
		},
		{
			"reftype (ref null func), passive", "(elem (ref null func) (ref.func 0))",
			"synthetic: `(ref null func)` is the three-token spelling of the same arm; the suite " +
				"writes it in table types but not in an elem, so this direction is unsampled",
		},
		{
			"reftype funcref, passive", "(elem funcref (ref.func 0))",
			"synthetic: the abbreviated reftype needs no paren, so it never reached the offset " +
				"branch — here to show the fix did not narrow the arm to its parenthesized forms",
		},
		{
			"reftype, declare", "(elem declare (ref func) (ref.func 0))",
			"elem.wast:573 — the declarative arm (parser.mly:1167), which reaches elem_list past " +
				"a keyword rather than past the offset lookahead",
		},
		{
			"reftype, (offset ...)", "(elem (offset (i32.const 0)) (ref func) (ref.func 0))",
			"synthetic: the crossing the suite does not write — offset sugar *and* the reftype " +
				"arm, which is the row a fix that dropped the offset branch would fail",
		},
		{
			"reftype, bare expr offset", "(elem (i32.const 0) (ref func) (ref.func 0))",
			"synthetic: two parens in a row, the first an offset and the second a reftype — the " +
				"case that shows this is a partition and not a priority",
		},

		// The `(item …)` elemexpr, whose lookahead predates #75 and is now one of three.
		{
			"item elemexpr", "(elem (ref func) (item (ref.func 0)))",
			"elem.wast:43 — `(item …)` is an elemexpr (parser.mly:1140), not an offset",
		},
		{
			"item elemexpr after an offset", "(elem (offset (i32.const 0)) func 0)",
			"elem.wast:37 — kept adjacent to the row above so the two `(`-led forms after `elem` " +
				"are both present in the table",
		},

		// The empty element list, on the one arm that reaches it. This row was `(elem)` and was the
		// table's *vacuity* row, on the reasoning that several rows above would assert something
		// narrower than they claim if the empty shape did not parse at all. The shape was right and the
		// spelling was wrong, which is worse than either mistake alone: `(elem)` parsed, so the row was
		// green, and it was green because this parser had an arm the reference does not have.
		//
		// **The `why` field said the wrong thing for as long as the row existed, and it said it the way
		// the graveyard's worst cases do: by citing a real line for a conclusion the line does not
		// support.** Verbatim, because the wrong belief is the part worth keeping — *"`(elem)` is
		// well-formed — `elemkind` has no empty arm but `reftype elemexpr_list` reaches an empty list
		// (parser.mly:1144)"*. Every clause of that is true except the inference: `elemexpr_list` does
		// derive empty at :1144, and the **`reftype` in front of it does not** (:376-389, thirteen arms,
		// every one consuming a token), so `elem_list` derives nothing empty. The spec agrees
		// independently — `elemlist ::= rt:reftype e*:list(elemexpr)`, §6.6.9 — and a resolving citation
		// check passes on the sentence, because the line exists and says what it is quoted as saying.
		// Only reading the *other* symbol in the production falsifies it.
		//
		// The empty list is reachable, one level up and in exactly one of the five `elem` arms: :1175 is
		// `offset elemidx_list`, and `elemidx_list` is `idx_list`, which **is** nullable (:500). So the
		// row keeps its job with a spelling the reference derives, and it now also pins that the
		// offset-sugar branch's `RParen` case exists — 29 corpus rows take it (#143).
		{
			"empty elemidx_list after an offset", "(elem (i32.const 0))",
			"elem.wast:39 — elem:1175's `offset elemidx_list` over an empty idx_list (parser.mly:500), " +
				"the only one of the five arms that reaches an empty element list; the sibling " +
				"`tableElemSugar` accepts the same emptiness at table_fields:1216",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := prefix + c.field + ")"
			if err := ReadModule([]byte(src)); err != nil {
				t.Errorf("ReadModule(%s) = %v; want accepted — [%s]", c.field, err, c.why)
			}
		})
	}

	// The `array.wast:219` shape: a reftype naming a *defined* type rather than `func`. Separate
	// from the table because it needs its own type section, and worth its own case — `(ref $t)`
	// exercises `atReftypeStart`'s peek2 on a VAR-led reftype, where the two above are keyword-led.
	const named = `(module
		(type $bvec (array i8))
		(elem $e (ref $bvec) (array.new_fixed $bvec 2 (i32.const 1) (i32.const 2))))`
	if err := ReadModule([]byte(named)); err != nil {
		t.Errorf("ReadModule(named reftype elem) = %v; want accepted "+
			"(array.wast:219 — `(elem $e (ref $bvec) …)`, the third vector #75 rejected and the "+
			"one its issue did not list)", err)
	}
}

// at renders the token-ish text at an offset, for the message above.
func at(src string, off int) string {
	if off < 0 || off > len(src) {
		return "<out of range>"
	}
	end := off
	for end < len(src) && src[end] != ' ' && src[end] != ')' {
		end++
	}
	if end == off && end < len(src) {
		end++
	}
	return src[off:end]
}

// reKindDecl matches a `kwFoo keywordKind = "BAR"` declaration in kinds.go.
var reKindDecl = regexp.MustCompile(`(?m)^\s*(kw[A-Za-z0-9]+)\s+keywordKind\s*=\s*"([A-Z_0-9]+)"`)

// reParserKindsBody matches the parserKinds literal's contents.
var reParserKindsBody = regexp.MustCompile(`(?s)var parserKinds = \[\]keywordKind\{(.*?)\n\}`)
