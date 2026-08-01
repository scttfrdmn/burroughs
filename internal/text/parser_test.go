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
		got := cons.heaptype() == nil

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
		if want, got := pred.atValtypeStart(), cons.valtype() == nil; want != got {
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
		n, err := p.valtypeList()
		if err != nil {
			t.Errorf("valtypeList(%q) = %v", src, err)
			continue
		}
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
		n, err := p.valtypeList()
		if err != nil {
			t.Errorf("valtypeList(%q) = %v", tc.src, err)
			continue
		}
		if n != tc.want {
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
		{`(module (type (sub $a $b (func))))`, "subtype:1453 with idx_list"},
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
		{`(module (type (func (param (ref 0) (ref $t)))))`, "heaptype:1374, the idx arm"},

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
		{`(module (export "e" (global $g)))`, "externidx:1259"},
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

		{`(module (elem))`, "elem:1158 with an empty elem_list"},
		{`(module (elem $e))`, "elem with bindidx"},
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
		{`(module (func $f) (func $f))`, "duplicate func $f", "func.wast, bind_abs:174"},
		{
			`(module (global $g i32) (global $g (import "m" "g") i32))`, "duplicate global $g",
			"bind_abs:174 via bind_global",
		},
		{`(module (type $t (func)) (type $t (func)))`, "duplicate type $t", "bind_abs:174"},
		{`(module (memory $m 1) (memory $m 1))`, "duplicate memory $m", "bind_abs:174"},
		{`(module (table $t 1 funcref) (table $t 1 funcref))`, "duplicate table $t", "bind_abs:174"},
		{`(module (data $d) (data $d))`, "duplicate data $d", "bind_abs:174"},
		{`(module (elem $e) (elem $e))`, "duplicate elem $e", "bind_abs:174"},
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

// TestBodyBoundaryIsNamed pins that stopping short is reported as stopping short.
//
// A module whose only unmet requirement is an instruction body must say `unimplemented`, not
// `unexpected token`: the board buckets by expected string, and a boundary masquerading as a
// syntax error is a work item filed under the wrong heading. *An error from the wrong layer is
// evidence about where structure was lost* — so the layer that is missing names itself.
func TestBodyBoundaryIsNamed(t *testing.T) {
	for _, src := range []string{
		`(module (func nop))`,
		`(module (func (i32.const 0)))`,
		`(module (global i32 (i32.const 0)))`,
		`(module (data (i32.const 0) "abc"))`,
		`(module (data (offset (i32.const 0)) "abc"))`,
		`(module (data (memory 0) (i32.const 0) "abc"))`,
		`(module (elem (i32.const 0) func))`,
		`(module (elem (table 0) (i32.const 0) func))`,
		`(module (elem funcref (item (ref.null func))))`,
		`(module (table 1 funcref (ref.null func)))`,
		`(module (table funcref (elem (ref.null func))))`,
	} {
		err := ReadModule([]byte(src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; this stratum does not parse instruction "+
				"bodies, so accepting is a false green", src)
			continue
		}
		if !strings.Contains(err.Error(), "unimplemented") {
			t.Errorf("ReadModule(%q) = %q, want an `unimplemented` boundary error — a "+
				"body this stratum cannot read must not be filed as a syntax error",
				src, err.Error())
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
		`(elem $x)`,
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

// reKindDecl matches a `kwFoo keywordKind = "BAR"` declaration in kinds.go.
var reKindDecl = regexp.MustCompile(`(?m)^\s*(kw[A-Za-z0-9]+)\s+keywordKind\s*=\s*"([A-Z_0-9]+)"`)

// reParserKindsBody matches the parserKinds literal's contents.
var reParserKindsBody = regexp.MustCompile(`(?s)var parserKinds = \[\]keywordKind\{(.*?)\n\}`)
