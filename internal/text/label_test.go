package text

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The label controls (#80).
//
// **Scoped to the mechanism, not to the two vectors.** The whole suite reward for symbolic label
// resolution is `token.wast:105` and `:121`, and both are the same shape: a `br_table` whose *first*
// index is an unbound name, inside a func holding exactly one named block. So a "fix" that resolved
// nothing at all and simply errored on any `$name` reaching `br_table` would score 2/2 and be wrong
// about every other spelling in the grammar. The board cannot tell those apart; this file is what
// can.
//
// The reference binds or reads a label at nine places and gets four facts right that no
// `assert_malformed` vector states (parser.mly:132-134, :514-519, :1020, :797-806):
//
//  1. `enter_func` **clears** the space — labels do not leak between funcs, or into a constexpr.
//  2. A block binds a level whether or not it is named (`anon_label`), and `func_body` binds one too.
//  3. `catch`'s label resolves in the **outer** context (`$4 c label`, `c` not `c'`).
//  4. `br_table` resolves **every** member of its tail, not just the first.
//
// Facts 3 and 4 are each a *rejection* the suite never asks for, and getting either wrong in the
// other direction over-*accepts* — invisible on the board by construction, which is what the reject
// rows below are for. Fact 2's visible half is the accept direction, and its index half is not
// observable at this stratum at all (decision 0011 computes no indices), so it is pinned as the depth
// invariant instead of as an answer: see TestLabelStackIsBalancedOnEveryExitPath.
//
// **Fact 1 is not observable in either direction, and each of the four was probed rather than
// argued.** Removing the `enter_func` reset leaves the board at 4161/2 and this package green,
// because every push site pops under `defer` and a func is a module field with nothing enclosing it —
// so no wat text distinguishes the reset from its absence, and no test here claims to. The reset
// stays as cited agreement with the reference; `funcField`'s header carries that argument and the
// probe's numbers. Fact 2's block push, by contrast, moves the board hard when dropped (4161 → 4077,
// 84 must-succeed modules into the fail bucket), and fact 3 shows up only in the six reject rows of
// TestCatchLabelResolvesInTheOuterScope. Naming which is which is the point: a file whose header
// claims four facts are controlled owes a reading of what each control can actually catch. See
// labelPushAnon's header for the earlier draft of the same overclaim, caught the same way.

// TestLabelTakingArmsMatchTheReference is the drift control on labelTakingKinds.
//
// The lookup category is not in the grammar — it is the *argument* a semantic action passes
// (`$2 c label` against `$2 c func`), so this reads the actions rather than the arms and is why
// productionBody exists beside productionArms.
//
// Scoped to the space: it iterates the reference's `plaininstr` arms, so an instruction that gains
// or loses a label index upstream arrives as a failure here rather than as a silently unresolved
// index. Enumerating today's five would freeze the control at the moment of authorship — the
// blind-spot shape decision 0006 names.
func TestLabelTakingArmsMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefParserMLY)
	body := productionBody(t, src, "plaininstr")

	// An arm's head is everything up to its action; the action is where the category lives. Split
	// on the same `\n  | ` boundary productionArms uses, so the two readers agree about what an arm
	// is.
	want := map[keywordKind]bool{}
	arms := 0
	for chunk := range strings.SplitSeq(body, "\n  | ") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		arms++
		head := chunk
		if i := strings.IndexByte(chunk, '{'); i >= 0 {
			head = chunk[:i]
		}
		fields := strings.Fields(reMenhirComment.ReplaceAllString(head, ""))
		if len(fields) == 0 {
			continue
		}
		kind := keywordKind(fields[0])
		if kind != keywordKind(strings.ToUpper(string(kind))) {
			continue // a nonterminal leader, not a mnemonic — see plaininstrArms
		}
		if reLabelLookup.MatchString(chunk) {
			want[kind] = true
		}
	}

	// Vacuity, on both counts and for two different failures. `arms` guards the reader: an
	// extractor that stopped splitting would find nothing and agree with everything. The
	// `len(want)` floor guards the *category* regexp specifically — it is the part that could
	// silently match zero while the arms still parse, and then this test would assert only that
	// labelTakingKinds is empty, which it is not, so it would fail loudly. Kept anyway, because the
	// loud failure would name the wrong thing: 83 arms and 0 categories is a broken regexp, and the
	// message should say so rather than reading as drift. Floors, not equalities: upstream adding a
	// branch instruction must fail the comparison below, not this guard.
	if arms < 70 {
		t.Fatalf("extracted %d plaininstr arms, want >=70 (83 at bdd7164); the reader is not "+
			"splitting the production and every comparison below is over an empty set", arms)
	}
	if len(want) < 3 {
		t.Fatalf("found %d arms passing `label` as their lookup category, want >=3 (5 at "+
			"bdd7164); reLabelLookup has drifted from the reference's actions, so this control "+
			"is comparing labelTakingKinds against almost nothing", len(want))
	}

	for kind := range want {
		if !labelTakingKinds[kind] {
			t.Errorf("the reference resolves %s's index against the label space (`$N c label`) "+
				"and labelTakingKinds does not list it; that index would be read and discarded, "+
				"so an unbound name in it is accepted — a rejection the parser owes and no "+
				"accept-direction vector can report", kind)
		}
	}
	for kind := range labelTakingKinds {
		if !want[kind] {
			t.Errorf("labelTakingKinds lists %s, which no reference arm resolves against the "+
				"label space; resolving it here rejects modules the reference accepts, which is "+
				"the class the suite is structurally blind to", kind)
		}
	}
}

// reLabelLookup matches a semantic action's label lookup: `$4 c label`, or `$3 c' label` if a
// future arm reads the inner context. The word boundary matters — `label` must not match
// `labeling_opt`, which appears in the block productions.
var reLabelLookup = regexp.MustCompile(`\$\d+ c'? label\b`)

// TestLabelLookupProductionsAreAllRead is the coverage half of the control above: it asks which
// *productions* the reference resolves a label in, not which instructions.
//
// The reason for a second control is that labelTakingKinds only covers `plaininstr`. The reference
// reads `label` in three productions — `plaininstr` and the two handler bodies — and a fourth
// appearing upstream would be a label position this parser reads as an unresolved index, with
// nothing failing. Derived rather than enumerated for the same reason as everything else here: the
// domain is "every production in the file", and the declared map is what gets compared to it.
//
// The counts are part of the claim, not decoration. `plaininstr`'s six lookups over five arms is
// `br_table`'s two (`$2 c label :: $3 c label`), which is precisely the fact labelIdxList exists
// for; a drop to five would mean the tail stopped being a label position upstream.
func TestLabelLookupProductionsAreAllRead(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefParserMLY)

	// The productions this package reads a label in, and where.
	want := map[string]struct {
		lookups int
		readBy  string
	}{
		"plaininstr":             {6, "immediates' labelTakingKinds branch (instr.go); six over five arms because br_table reads its tail too"},
		"handler_block_body":     {4, "handlerClauses, reached from handlerBlock — the flat try_table"},
		"try_block_handler_body": {4, "handlerClauses, reached from foldedBlock — the same four arms, folded"},
	}

	heads := reProductionHead.FindAllStringIndex(src, -1)
	if len(heads) < 100 {
		t.Fatalf("found %d productions in parser.mly, want >=100 (137 at bdd7164); the header "+
			"regexp is not reading the file and this control would find no lookups at all",
			len(heads))
	}

	got := map[string]int{}
	total := 0
	for i, h := range heads {
		name := strings.TrimSuffix(strings.Fields(src[h[0]:h[1]])[0], " :")
		end := len(src)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		n := len(reLabelLookup.FindAllString(src[h[0]:end], -1))
		if n == 0 {
			continue
		}
		got[name] = n
		total += n
	}
	if total < 8 {
		t.Fatalf("found %d label lookups across the whole file, want >=8 (14 at bdd7164); "+
			"reLabelLookup has drifted and the comparison below is vacuous", total)
	}

	for name, n := range got {
		w, ok := want[name]
		if !ok {
			t.Errorf("the reference resolves %d label(s) in the `%s` production, which this "+
				"package does not know is a label position; the index would be read and "+
				"discarded there, accepting an unbound name", n, name)
			continue
		}
		if n != w.lookups {
			t.Errorf("`%s` holds %d label lookups, want %d (read by %s); a changed count means "+
				"an arm gained or lost a label index", name, n, w.lookups, w.readBy)
		}
	}
	for name, w := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("this package reads `%s` as a label position (%s) and the reference "+
				"resolves no label there; resolving one rejects legal modules", name, w.readBy)
		}
	}
}

// TestCatchLabelIsTheLastIndexOfItsArm pins the fact handlerClauses hard-codes.
//
// The clause reader resolves the **last** index of every arm and leaves the others alone —
// `(catch $tag $label)` and `(catch_all $label)`. That is a claim about four arms in two
// productions, written once as "all but the last are unresolved", and it is exactly the kind of
// positional claim that is right until upstream reorders an arm. So the position is derived: for
// each arm, the `idx` occurrences in the head are numbered, and the action's `$N c label` must name
// the last of them.
//
// No vector can see this. `(catch $e $h)` with the operands swapped is a tag where a label belongs
// and a label where a tag belongs, and since tags are not resolved at this stratum at all, reading
// position 3 instead of 4 would report `unknown label $e` for a legal module — an over-rejection,
// which is the direction the suite cannot report.
func TestCatchLabelIsTheLastIndexOfItsArm(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefParserMLY)

	checked := 0
	for _, prod := range []string{"handler_block_body", "try_block_handler_body"} {
		body := productionBody(t, src, prod)
		for chunk := range strings.SplitSeq(body, "\n  | ") {
			chunk = strings.TrimSpace(chunk)
			if chunk == "" {
				continue
			}
			head, action := chunk, ""
			if i := strings.IndexByte(chunk, '{'); i >= 0 {
				head, action = chunk[:i], chunk[i:]
			}
			m := reLabelLookup.FindString(action)
			if m == "" {
				continue // the `instr_list` arm, which reads no label
			}
			// The reference numbers a semantic value by its position in the head, one-based
			// across *all* symbols, not just the indices.
			var lastIdx int
			for i, f := range strings.Fields(head) {
				if f == "idx" {
					lastIdx = i + 1
				}
			}
			if lastIdx == 0 {
				t.Errorf("`%s` arm %q resolves a label but its head holds no `idx`", prod, head)
				continue
			}
			pos := strings.TrimPrefix(strings.Fields(m)[0], "$")
			if pos != strconv.Itoa(lastIdx) {
				t.Errorf("`%s` arm %q resolves its label at $%s, but the last `idx` in the arm "+
					"is $%d; handlerClauses reads the last index as the label, so this arm's "+
					"tag and label have swapped and it now reports `unknown label` for a tag",
					prod, strings.TrimSpace(head), pos, lastIdx)
			}
			checked++
		}
	}
	// Eight arms — four in each production — and a floor rather than an equality so an upstream
	// fifth clause form fails the comparison above instead of this guard.
	if checked < 8 {
		t.Fatalf("checked %d clause arms, want >=8 (8 at bdd7164); the arm reader is not "+
			"reaching the handler bodies", checked)
	}
}

// TestUnknownLabelIsReportedForAnUnboundName is the two vectors, and only the two vectors.
//
// Everything else in this file is the mechanism; this is the reward, kept separate so the
// distinction stays visible. Both cite their line, so TestTextFixtureProvenance checks the
// transcription — and the `$l0` / `$l$l` lexing is asserted too, because a vector that failed for a
// *lexer* reason would pass this test for the wrong reason: `$l0` is one VAR token whose text is
// `$l0`, not `$l` followed by `0`.
func TestUnknownLabelIsReportedForAnUnboundName(t *testing.T) {
	// One row per line, and that is a mechanical requirement rather than a style choice:
	// `TestTextFixtureProvenance` reads a fixture row with a line-oriented regexp, so a row split
	// across lines carries a citation nothing checks. *A registration is not a check* — the file
	// would sit in the checker's list contributing zero verified rows, which is #78's shape.
	for _, tc := range []struct{ src, cite string }{
		{`(module (func (block $l (i32.const 0) (br_table $l0))))`, "token.wast:105 — `$l0` is one identifier, not `$l` then `0`"},
		{`(module (func (block $l (i32.const 0) (br_table $l$l))))`, "token.wast:121 — `$l$l` likewise; `$` is an idchar, so the name runs on"},
	} {
		// The lexing first, so a pass cannot be bought with a lex error.
		toks, err := lexToEOF([]byte(tc.src))
		if err != nil {
			t.Errorf("lexToEOF(%q) = %v; the vector must lex clean for this to be a label "+
				"question at all — %s", tc.src, err, tc.cite)
			continue
		}
		vars := 0
		for _, tk := range toks {
			if tk.Kind == VarTok {
				vars++
			}
		}
		if vars != 2 {
			t.Errorf("%q lexes to %d VAR tokens, want 2 (the binding `$l` and the use); the "+
				"vector is not asking a label question — %s", tc.src, vars, tc.cite)
			continue
		}
		err = ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; want `unknown label` — %s", tc.src, tc.cite)
			continue
		}
		if !strings.Contains(err.Error(), "unknown label") {
			t.Errorf("ReadModule(%q) = %q, want `unknown label` — %s", tc.src, err, tc.cite)
		}
	}
}

// TestUnknownLabelMessageMatchesTheReference pins the part of the message the oracle cannot see.
//
// The suite's expected string stops at `unknown label`, so everything after it is ours alone to keep
// honest (grave #36). Two facts, both checked because both are cheap to get wrong:
//
//   - **Two spaces.** `label (c : context) x = lookup "label " c.labels x` (parser.mly:161) — the
//     category carries a trailing space, and the renderer is `"unknown " ^ category ^ " " ^ print x`
//     (:150). Reproduced rather than tidied; "improving" it would make this the one message in the
//     package that disagrees with upstream.
//   - **The name is the token's own text.** `$"l"` is a spelling of the name `l`, and a message
//     rendered from the *decoded* name would print `$l` for an input that said `$"l"` — the
//     fabricated-evidence class, right verdict and a quoted byte the input never held.
func TestUnknownLabelMessageMatchesTheReference(t *testing.T) {
	for _, tc := range []struct{ src, want, why string }{
		{
			`(module (func (br $nope)))`,
			"unknown label  $nope",
			"the plain spelling, with the two spaces `lookup \"label \"` produces",
		},
		{
			`(module (func (br $"nope")))`,
			`unknown label  $"nope"`,
			"the string spelling: `print x` re-quotes what was written, so the message must " +
				"carry the quotes the source had rather than the decoded name",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; want %q — %s", tc.src, tc.want, tc.why)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("ReadModule(%q) = %q, want %q — %s", tc.src, got, tc.want, tc.why)
		}
	}
}

// TestLabelSpaceIsClearedPerFunc is fact 1: `enter_func` empties the label space (parser.mly:134).
//
// `let enter_func c loc = {(enter_let c loc) with labels = empty ()}`. A parser that kept one stack
// across module fields would resolve a label bound in one func from inside the next, which is
// over-acceptance and therefore reportable by nothing in the suite. The positive half is here too —
// the same names must still resolve *within* their own func — so the control cannot be satisfied by
// deleting the resolution.
func TestLabelSpaceIsClearedPerFunc(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func (block $l)) (func (br $l)))`,
			"the leak between funcs, both flat-scoped; `$l` is not in the second func's space",
		},
		{
			`(module (func (block $l (br $l))) (func (br $l)))`,
			"the same, with the first func actually *using* the label — so the space was " +
				"non-empty when the second func began",
		},
		{
			`(module (func (block $l)) (global i32 (br $l)))`,
			"a func's label leaking into a global's constexpr, which is the other side of " +
				"`enter_func` and reached through a different field",
		},
		{
			`(module (func (block $l)) (elem funcref (item (br $l))))`,
			"and into an elem segment's item expression",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err == nil {
			t.Errorf("ReadModule(%q) accepted; a label does not outlive its func "+
				"(enter_func, parser.mly:134) — %s", tc.src, tc.why)
		} else if !strings.Contains(err.Error(), "unknown label") {
			t.Errorf("ReadModule(%q) = %q, want `unknown label` — %s", tc.src, err, tc.why)
		}
	}
	// The positive half: a reset that works by never binding anything would pass every row above.
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func (block $l (br $l))) (func (block $l (br $l))))`,
			"derived from labels.wast:3,4 — the suite has a must-succeed module whose `br` " +
				"names its enclosing block; the inference is that two funcs may each bind the " +
				"same name, since the space is per-func rather than per-module",
		},
		{
			`(module (global i32 (block $l (br $l))) (func (block $l (br $l))))`,
			"derived from labels.wast:3,4 — the same in a constexpr, which is a label scope " +
				"of its own for the same reason",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — %s", tc.src, err, tc.why)
		}
	}
}

// TestFuncNameIsNotALabel is fact 1's sharper half: a func's own identifier is in the func space.
//
// `(func $f (br $f))` is `unknown label $f` — the parser binds `$f` with `bind_func` (:960) and the
// body's label space holds only `func_body`'s anonymous level (:1020). A single shared name map
// would accept it, and no vector says otherwise. Same question the binary side asks with
// TestIndexSpacesAreIndependent, one space further in.
func TestFuncNameIsNotALabel(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (func $f (br $f)))`, "the func's own name, in scope as a func index and not as a label"},
		{`(module (func (br $nope)))`, "a func body is a label scope with exactly one anonymous level, so any name in it is unbound"},
		{`(module (type $t (func)) (func (br $t)))`, "a type name, likewise a different space"},
		{`(module (global $g i32 (i32.const 0)) (func (br $g)))`, "a global's"},
		{`(module (func (param $x i32) (br $x)))`, "a local's — the space `enter_func` also resets, but a different one"},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; the label space is not the module's name "+
				"space — %s", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "unknown label") {
			t.Errorf("ReadModule(%q) = %q, want `unknown label` — %s", tc.src, err, tc.why)
		}
	}
}

// TestCatchLabelResolvesInTheOuterScope is fact 3, and it is the one fact in this file whose *only*
// oracle is this test.
//
// All eight clause arms read `($N c label)` — `c`, the enclosing context, where the body reads `c'`
// (parser.mly:797-806, :934-943). The suite's own use is `try_table.wast:30`,
// `(block $h (try_table (result i32) (catch $e0 $h) …))`, whose `$h` is the enclosing block's; a
// handler branching to the try_table itself would be a loop. Resolving in `c'` would still *find*
// `$h` — it is on the stack either way — and would merely compute an index one too large, invisible
// at a stratum that computes no indices. What it would also do is accept a clause naming the
// try_table's own label, which the reference rejects. That spelling is nowhere in the suite.
//
// All four clause keywords, both spellings of the block, because the two productions are separate
// arms and a fix applied to one is not applied to the other.
func TestCatchLabelResolvesInTheOuterScope(t *testing.T) {
	// Reject: the clause names the try_table's own label, which is not yet in scope for a clause.
	for _, tc := range []struct{ src, why string }{
		{`(module (tag $e) (func (try_table $t (catch $e $t))))`, "folded, `catch`"},
		{`(module (tag $e) (func (try_table $t (catch_ref $e $t))))`, "folded, `catch_ref`"},
		{`(module (tag $e) (func (try_table $t (catch_all $t))))`, "folded, `catch_all`"},
		{`(module (tag $e) (func (try_table $t (catch_all_ref $t))))`, "folded, `catch_all_ref`"},
		{`(module (tag $e) (func try_table $t (catch $e $t) end))`, "flat, `catch` — handler_block_body, the other production"},
		{`(module (tag $e) (func try_table $t (catch_all $t) end))`, "flat, `catch_all`"},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; a clause's label resolves in the enclosing "+
				"context (`$N c label`, parser.mly:797-806), where the try_table's own label is "+
				"not yet bound — %s", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "unknown label") {
			t.Errorf("ReadModule(%q) = %q, want `unknown label` — %s", tc.src, err, tc.why)
		}
	}
	// Accept: the clause names an *enclosing* block, which is what the suite writes. This half is
	// what a reader that simply refused every clause label would fail.
	for _, tc := range []struct{ src, why string }{
		{
			`(module (tag $e) (func (block $h (try_table (catch $e $h)))))`,
			"derived from try_table.wast:29,30 — the suite's `(block $h (try_table (result i32) " +
				"(catch $e0 $h) …))` with the signature and body dropped; the inference is that " +
				"the enclosing label is in scope for a clause, which is what those two lines " +
				"jointly say",
		},
		{
			`(module (tag $e) (func (block $h (try_table (catch_ref $e $h)))))`,
			"derived from try_table.wast:151,152 — `(block $h (result i32 exnref) (try_table " +
				"(result i32) (catch_ref $e-i32 $h) …))`, the `_ref` arm's own vector",
		},
		{
			`(module (tag $e) (func (block $h (try_table (catch_all $h)))))`,
			"derived from try_table.wast:40,41 — `(block $h (try_table (catch_all $h) …))`",
		},
		{
			`(module (tag $e) (func (block $h (try_table (catch_all_ref $h)))))`,
			"derived from try_table.wast:40,41 — the fourth arm, which no vector writes; the " +
				"inference is that it is the `catch_all` arm plus a ref",
		},
		{
			`(module (tag $e) (func block $h try_table (catch $e $h) end end))`,
			"derived from try_table.wast:29,30 — the flat spelling, handler_block_body",
		},
		{
			`(module (tag $e) (func block $h try_table (catch_all $h) end end))`,
			"derived from try_table.wast:40,41 — flat, `catch_all`",
		},
		{
			`(module (tag $e) (func (block $h (try_table $t (catch $e $h) (br $t)))))`,
			"derived from try_table.wast:29,30 — the discriminating accept: the try_table *is* " +
				"named and its label is in scope for the **body**, so a reader that restored the " +
				"outer scope and forgot to put its own back fails here and nowhere else",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — %s", tc.src, err, tc.why)
		}
	}
}

// TestBrTableResolvesEveryLabel is fact 4.
//
// `br_table idx idx_list` is `Lib.List.split_last ($2 c label :: $3 c label)` (parser.mly:563-565):
// the head and every member of the tail resolve against the label space, the last being the default
// target. **Both #80 vectors name their bad label first**, so a reader that resolved only the head
// scores 2/2 on the board and accepts `(block $l (br_table $l $nope))`.
func TestBrTableResolvesEveryLabel(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func (block $l (br_table $l $nope (i32.const 0)))))`,
			"the second member — the position the two vectors never exercise",
		},
		{
			`(module (func (block $l (br_table $l $l $nope (i32.const 0)))))`,
			"the third, so the loop is not an unrolled pair",
		},
		{
			`(module (func (block $l (br_table 0 $nope (i32.const 0)))))`,
			"a numeric head, so the tail is reached without the head resolving anything",
		},
		{
			`(module (func (block $l (br_table $nope $l (i32.const 0)))))`,
			"and the head itself, which is the vectors' own position",
		},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; every member of a br_table's list is a label "+
				"(parser.mly:563-565) — %s", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "unknown label") {
			t.Errorf("ReadModule(%q) = %q, want `unknown label` — %s", tc.src, err, tc.why)
		}
	}
	// The accept half, because rejecting every tail member would pass all four rows above.
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func (block $l1 (block $l2 (br_table $l1 $l2 $l1 (i32.const 0))))))`,
			"derived from br_table.wast:998,1000 — the suite's `meet-externref` with the " +
				"signature dropped; the inference is that a name may repeat in the list, since " +
				"each member resolves independently",
		},
		{
			`(module (func (block $l (br_table $l 0 $l (i32.const 0)))))`,
			"derived from br_table.wast:998,1000 — numeric and symbolic members mixed, which " +
				"follows from `idx` having two arms rather than the list having one kind",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — %s", tc.src, err, tc.why)
		}
	}
}

// TestNumericLabelIsNotTheParsersError is the negative space of the whole feature, and it is what
// keeps the pass count honest.
//
// `idx`'s NAT arm is `nat32 $1` (parser.mly:488) — a *width* check, with no lookup — so a numeric
// label is never `unknown label` from the parser. All 13 `assert_invalid "unknown label"` vectors
// are this shape (`br.wast:648`, `br_table.wast:1433`, …), and answering them here would buy 13
// pass counts by moving validation's verdict into the parser: it would also reject `(br 1)` inside
// code validation accepts, since depth is a validation-time property. *Overfitting to the oracle,
// invisible on the board by construction* (§9 G-3).
//
// The width check itself is asserted beside it, because "no lookup" must not become "no check".
func TestNumericLabelIsNotTheParsersError(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (func (br 0)))`, "no enclosing block at all — br.wast:648's shape, assert_invalid"},
		{`(module (func (br 1)))`, "past the func's own level; still validation's"},
		{`(module (func (block (block (br 5)))))`, "far past it, so this is not an off-by-one that happens to pass"},
		{`(module (func (br_table 0 1 2 (i32.const 0))))`, "the list too — br_table.wast:1433's shape"},
		{`(module (func (br 4294967295)))`, "the widest legal i32, which is a width question and not a depth one"},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; a numeric label is `nat32 $1` with no lookup, so an "+
				"out-of-range depth is the validator's — %s", tc.src, err, tc.why)
		}
	}
	// And the width check the NAT arm *does* make, so "no lookup" is not "no check".
	for _, tc := range []struct{ src, why string }{
		{`(module (func (br 0x100000000)))`, "2^32 in the head — binary-leb128.wast's question in text spelling"},
		{`(module (func (br_table 0 0x100000000 (i32.const 0))))`, "and in the tail, which reaches it through labelIdxList"},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; the NAT arm is a 32-bit width check — %s", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "constant out of range") {
			t.Errorf("ReadModule(%q) = %q, want `constant out of range` — %s", tc.src, err, tc.why)
		}
	}
}

// TestEveryBlockFormBindsItsLabel is the accept direction across the whole block family.
//
// Over-rejection is the class no `assert_malformed` can report, and it is what both of this
// milestone's graves were (#75, #76). A label binding is made at five sites — `blockinstr`'s four
// keywords, `foldedBlock`'s four, the `if`/`else` pair, `funcField`, and `handlerClauses`' restore —
// and missing any one of them rejects legal modules while still scoring 2/2 on the vectors that
// named the feature. `foldedBlock` is the sharpest: neither #80 vector writes a folded block, so
// dropping that push costs nothing on the board.
func TestEveryBlockFormBindsItsLabel(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{
			`(module (func (block $l (br $l))))`,
			"derived from labels.wast:3,4 — the suite's `(block $exit … (br $exit …))` with the " +
				"result type and operand dropped; the inference is that the minimal spelling of " +
				"a must-succeed construct is legal",
		},
		{
			`(module (func block $l br $l end))`,
			"derived from labels.wast:3,4 — the flat spelling of the same, which is blockinstr " +
				"rather than foldedBlock",
		},
		{
			`(module (func (loop $l (br $l))))`,
			"derived from labels.wast:13,18 — `(loop $cont … (br $cont))`, folded",
		},
		{
			`(module (func loop $l br $l end))`,
			"derived from labels.wast:13,18 — flat",
		},
		{
			`(module (func (block $l (if (i32.const 0) (then (br $l))))))`,
			"derived from labels.wast:15,16 — `(if … (then (br $exit …)))`, the folded if whose " +
				"branch names an outer label",
		},
		{
			`(module (func (if $l (i32.const 0) (then (br $l)) (else (br $l)))))`,
			"derived from labels.wast:15,16 — the `if`'s *own* label, in scope over both arms; " +
				"the else arm is a separate production (parser.mly:732-735) and so a separate " +
				"chance to lose the binding",
		},
		{
			`(module (func i32.const 0 if $l br $l else br $l end))`,
			"derived from labels.wast:15,16 — flat, both arms",
		},
		{
			`(module (tag $e) (func (try_table $l (catch $e 0) (br $l))))`,
			"derived from try_table.wast:29,30 — a named try_table whose body branches to it; " +
				"the clause takes a numeric tag and label so this row turns only on the body's " +
				"own scope",
		},
		{
			`(module (func (block $l (block (br $l)))))`,
			"derived from labels.wast:15,16 — the suite's `(loop $cont … (if … (then (br $exit " +
				"…))))` branches to an outer name across an intervening `if`, which binds an " +
				"*anonymous* level; the inference is that an anonymous block likewise does not " +
				"hide an outer name. **This row passes with or without the anonymous push** — " +
				"see labelPushAnon: at this stratum the answer is by name, so the level's " +
				"effect is an index nothing computes yet. Kept because the outer name must stay " +
				"reachable, not as evidence for the push",
		},
		{
			`(module (func (block $l (block $l (br $l)))))`,
			"derived from labels.wast:282,285 — the suite's `redefinition` func nests `$l1` " +
				"inside `$l1` and branches to it; with the result types dropped the inference is " +
				"that a duplicate is *not* an error in the label space the way it is in every " +
				"absolute space, because `bind_rel` is `VarMap.add`'s overwrite (parser.mly:179)",
		},
		{
			`(module (func (block $"l" (br $l))) (func (block $l (br $"l"))))`,
			"derived from id.wast:31 — `$\"l\"` is a spelling of the name `l`, so the two " +
				"resolve against each other; a lookup comparing raw lexemes rejects both halves",
		},
		{
			`(module (func (block $l (br_on_null $l (ref.null func)))) (func (block $l (br_on_cast $l funcref funcref (ref.null func)))))`,
			"derived from br_on_null.wast:11,12 and br_on_cast.wast:32,33 — the two label-taking " +
				"arms whose immediates continue *after* the label, so a reader that consumed the " +
				"wrong number of tokens fails here rather than in the message",
		},
	} {
		if err := ReadModule([]byte(tc.src)); err != nil {
			t.Errorf("ReadModule(%q) = %v; want accepted — %s", tc.src, err, tc.why)
		}
	}
}

// TestLabelScopeEndsWithItsBlock is the other half of the binding: a push that never pops accepts a
// name after its block has closed.
//
// The reference gets scope exit for free — `enter_block` returns a new context and the caller's own
// `c` is untouched — so there is no `pop` upstream to mirror and nothing upstream that can be wrong
// here. A mutable context has to undo the push, which makes this a defect class the reference does
// not have and the suite therefore never asks about.
func TestLabelScopeEndsWithItsBlock(t *testing.T) {
	for _, tc := range []struct{ src, why string }{
		{`(module (func (block $l) (br $l)))`, "a sibling block, folded"},
		{`(module (func block $l end br $l end))`, "flat, where the `end` is what closes the scope"},
		{`(module (func (block (block $l)) (br $l)))`, "one level in, so the pop is not merely the last one"},
		{`(module (func (loop $l) (br $l)))`, "the LOOP arm"},
		{`(module (func (if $l (i32.const 0) (then)) (br $l)))`, "the IF arm, whose else clause is a second exit path"},
		{`(module (tag $e) (func (try_table $l (catch $e 0)) (br $l)))`, "the TRY_TABLE arm, which pops through handlerClauses' restore"},
	} {
		err := ReadModule([]byte(tc.src))
		if err == nil {
			t.Errorf("ReadModule(%q) accepted; a label's scope is its block "+
				"(`{c with labels = …}`, parser.mly:132) — %s", tc.src, tc.why)
			continue
		}
		if !strings.Contains(err.Error(), "unknown label") {
			t.Errorf("ReadModule(%q) = %q, want `unknown label` — %s", tc.src, err, tc.why)
		}
	}
}

// TestLabelStackIsBalancedOnEveryExitPath is fact 2's checkable form, and the one place labelDepth
// is asserted.
//
// The depth invariant — stack depth equals block nesting depth — is what keeps `labelPop`
// unconditional and what the anonymous push exists for. It is *not* observable through
// `ReadModule`'s verdict at this stratum, because decision 0011 computes no indices and
// `lookupLabel` answers by name: a missing anonymous push changes no answer today, and a control
// claiming otherwise would pass with the bug in place. So the invariant is asserted directly, at the
// only two moments it can be: the space is empty when the parse ends, on **every** exit path,
// including the error paths where a `defer` is the only thing that pops.
//
// This is what makes 0011's second half and the #67 bridge safe to build on: when indices are
// needed, the depth is already right and the shift falls out of the position in the slice.
func TestLabelStackIsBalancedOnEveryExitPath(t *testing.T) {
	for _, src := range []string{
		// Accepting parses, one per push site.
		`(module (func (block $l (br $l))))`,
		`(module (func block $l br $l end))`,
		`(module (func (loop $a (block $b (block (br $a))))))`,
		`(module (func (if $l (i32.const 0) (then (br $l)) (else (br $l)))))`,
		`(module (tag $e) (func (block $h (try_table $t (catch $e $h) (br $t)))))`,
		`(module (tag $e) (func block $h try_table $t (catch $e $h) br $t end end))`,
		`(module (func (block $l)) (func (block $l)) (global i32 (block $l (br $l))))`,
		// Rejecting parses, where only a deferred pop unwinds the stack. Each aborts *inside* a
		// block body, which is the state a missing `defer` leaves behind.
		`(module (func (block $l (br $nope))))`,
		`(module (func (block $l (block $m (br $nope)))))`,
		`(module (func (loop $l (br $nope))))`,
		`(module (func (if $l (i32.const 0) (then (br $nope)))))`,
		`(module (tag $e) (func (block $h (try_table (catch $e $nope)))))`,
		`(module (tag $e) (func (block $h (try_table (catch $e $h) (br $nope)))))`,
		`(module (func block $l br $nope end))`,
		`(module (func (block $l (i32.const))))`, // a non-label failure, mid-block
		`(module (func (block $l`,                // truncated: the deepest error path there is
	} {
		c, err := newCursor([]byte(src))
		if err != nil {
			t.Errorf("newCursor(%q) = %v; the row must lex for the parser to be asked anything",
				src, err)
			continue
		}
		p := &parser{c: c}
		perr := p.module()
		if got := p.ctx.labels.labelDepth(); got != 0 {
			t.Errorf("after parsing %q (err=%v) the label stack holds %d level(s), want 0; a "+
				"push whose pop is skipped on this path leaves a label in scope for whatever "+
				"the parser reads next", src, perr, got)
		}
	}
}

// TestLabelIndexCountsAnonymousLevels is the depth half of fact 2, and it exists because
// `lookupLabel` started answering with a *number* when the code section needed one.
//
// `labelPushAnon`'s header spent a paragraph on the anonymous level changing no lookup's answer, and
// that was true while the answer was bound-or-not: `(block $l (block (br $l)))` resolves `$l` by name
// either way. The reference's `anon_label` calls `enter_block`, which shifts every enclosing binding
// up by one (parser.mly:132, :514), so the level is worth exactly one index — and the moment the
// depth is the answer, dropping the push turns a `br 1` into a `br 0`: a well-formed image branching
// to the wrong block. That is the accept-direction shape (§9 G-3), and the two rows below are what
// the earlier comment said it could not offer.
//
// **It is a control on the helper, not on the path, and that is declared rather than discovered.**
// No encodable module reaches the symbolic arm today: a symbolic label needs an enclosing block to
// bind it, and every block form refuses at `refuseUnencodable` before its body is parsed — probed
// over all seven spellings, all seven refusing at the block. So a module-level row would be asserting
// a property of code that does not run, which passes for the wrong reason and is indistinguishable on
// the board from one that passes for the right one. The `labelSpace` is exercised directly instead,
// and #63/#64's blocktype encoding is what makes the module-level row possible.
//
// The stack is built with `pushLabel`, the function the block sites actually call, rather than with
// `labelPush`/`labelPushAnon` directly — **and the reason first written here was wrong.** The draft
// said a control reaching past the dispatcher would keep passing if `pushLabel` stopped choosing the
// anonymous arm; collapsing `pushLabel` to an unconditional `labelPush(name)` was probed and this
// test stayed **green**, because `labelPush("")` and `labelPushAnon()` append the same empty string.
// The dispatcher is a readability split, not a behavioural one, and no test here can distinguish its
// arms. Kept as the entry point because it is the real one; the false claim is recorded rather than
// deleted, per the falsification-passing case being the exercise's most valuable outcome (#108).
func TestLabelIndexCountsAnonymousLevels(t *testing.T) {
	for _, tc := range []struct {
		levels []string // innermost last, "" for an anonymous block
		name   string
		want   uint32
		why    string
	}{
		{
			[]string{"l"},
			"l", 0,
			"the innermost binding is 0, which is `bind_label`'s own return value (:196)",
		},
		{
			[]string{"l", ""},
			"l", 1,
			"an anonymous block between the `br` and its target still occupies a level; a " +
				"reader that skipped it answers 0 and the branch leaves the wrong block",
		},
		{
			[]string{"l", "", ""},
			"l", 2,
			"two of them, so the shift is per level rather than a single flag",
		},
		{
			[]string{"", "l"},
			"l", 0,
			"an anonymous level *outside* the target contributes nothing — the depth counts " +
				"from the innermost, so this is the direction an off-by-one gets caught in",
		},
		{
			[]string{"outer", "", "inner"},
			"outer", 2,
			"a named level below and an anonymous one between: the shift counts every level, " +
				"named or not, which is what `scoped`'s `VarMap.map (shift …)` does to all of them",
		},
		{
			[]string{"l", "", "l"},
			"l", 0,
			"shadowing wins innermost, and the anonymous level between is not what makes it " +
				"win — `VarMap.add`'s overwrite is (:196)",
		},
	} {
		var l labelSpace
		for _, name := range tc.levels {
			// Through the dispatcher the block sites use, so the anonymous arm is the one under
			// test rather than one this test selected for itself.
			c := &context{labels: l}
			c.pushLabel(name)
			l = c.labels
		}
		got, err := l.lookupLabel(Token{Text: "$" + tc.name}, tc.name)
		if err != nil {
			t.Errorf("lookupLabel(%q) over %v = %v; the name is bound in this stack, so a "+
				"failure here means the scan is wrong rather than the arithmetic — %s",
				tc.name, tc.levels, err, tc.why)
			continue
		}
		if got != tc.want {
			t.Errorf("lookupLabel(%q) over %v = %d, want %d — %s",
				tc.name, tc.levels, got, tc.want, tc.why)
		}
	}
	// The reject direction, in the same shape: an anonymous level binds *no* name, so nothing
	// resolves against it. Without this, a `labelPushAnon` that pushed some placeholder name would
	// satisfy every row above by making the depths right for the wrong reason.
	for _, tc := range []struct {
		levels []string
		name   string
		why    string
	}{
		{[]string{""}, "", "the empty name, which is what the anonymous level holds internally"},
		{[]string{"", ""}, "", "two of them, so the scan's skip is not a single-level special case"},
		{[]string{"l", ""}, "", "and beside a named one, where a scan that matched empties would " +
			"answer 0 for a name no block bound"},
	} {
		var l labelSpace
		for _, name := range tc.levels {
			c := &context{labels: l}
			c.pushLabel(name)
			l = c.labels
		}
		if got, err := l.lookupLabel(Token{Text: "$"}, tc.name); err == nil {
			t.Errorf("lookupLabel(%q) over %v = %d; an anonymous level occupies a level and "+
				"binds no name (`(block $)` is `empty identifier` from the lexer, so no *named* "+
				"level can be \"\") — %s", tc.name, tc.levels, got, tc.why)
		}
	}
}
