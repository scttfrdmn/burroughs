// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The authority-derived controls. Every check in this file compares something this package
// *asserts* against something outside it that can disagree — `valid.ml`, `binary.OpMnemonic`, or
// this package's own AST — and none of them can be satisfied by the corpus.
//
// That division is the point rather than a filing convenience. The corpus's `assert_invalid`
// vectors are satisfied by *any* refusal, so 84.3% of them cannot tell a right rule from a wrong
// one (see ErrTypeMismatch's comment); and no negative vector at all can catch an accept-direction
// defect. What is left is agreement with the authority, checked mechanically.
//
// # The third blind spot: message-match cannot see an under-rejection, and there are sixteen
//
// The two above are about the *corpus*. This one is about the instrument that was supposed to cover
// what the corpus cannot: 0003's message match, which compares the refusal's text against the
// vector's expectation and is the only thing that catches a right verdict delivered with wrong
// testimony. **It fires only on refusals.** A validator that *accepts* an invalid module emits no
// message, so there is nothing for the match to disagree with — the under-rejection has no message
// channel at all, not a channel that reports a passing grade. This is the accept-direction gap
// restated one layer in: not merely that no vector catches it, but that the instrument covering the
// vectors' weakness shares their direction.
//
// **Sixteen known instances lived in the tree when this paragraph was written**, named here rather
// than only where their count lived (ruling: Scott, PR #307): `decodeMemop` dropped the memarg
// alignment, so a vector whose sole defect was an over-aligned SIMD access was typed successfully
// and accepted. Four *more* of the same cause did reach the message match —
// `simd_store{8,16,32,64}_lane.wast`, which carry a second defect and so were refused for the wrong
// reason — and **that split is the exhibit, which is why it is kept now that #306 has closed both
// halves**: one cause, 20 vectors, and the message oracle saw exactly the quarter of it that
// happened to be refused at all. The 16 were invisible to it by construction, not by bad luck, and
// the fix arrived from reading the reference rather than from any signal this test could emit.
//
// A coverage claim is an assertion an instrument cannot check about itself, so its honest form
// carries its known counterexamples. This blind spot is unchanged by the specimen draining; what
// changed is that this campaign no longer knows of a live instance, which is a weaker statement than
// there being none.
//
// # And the weaker statement was the right one to make: two more instances arrived
//
// The paragraph above was written when the campaign knew of no live instance. Two turned up
// afterwards, neither found by anything in this file, and both are recorded here rather than only at
// their fix sites — because the count is the coverage claim's counterexample list, and a list that
// only grows where the fixes live is a list nobody reads as a list.
//
//   - **#311, `blockType`'s valtype arm.** `check_blocktype` calls `check_valtype` on the
//     single-valtype form (`valid.ml:420`) and this package did not, so `(block (result (ref 1)))`
//     typed successfully against a module declaring no type 1. Pure accept direction: the validator
//     said nothing, so there was no message for the match to disagree with, and no negative vector
//     could reach it either. Found by reading the reference, exactly as the alignment specimen was.
//   - **#310's divergence, and this one is a rule *no vector exercises at all*.** The offset bound
//     (`checkOffset`) is implemented, so it is not an under-rejection — but the decision inside it,
//     which memory's index type the bound reads, has no vector on either side. All four vectors
//     expecting `offset out of range` declare one memory, where the two readings agree. So the rule
//     is exercised only in the region where its open question does not apply, and the reject-direction
//     oracle is blind to it not because the verdict is an accept but because the *discriminating
//     input does not exist in the corpus*.
//
// That second shape is worth naming separately, because it is not what the three blind spots above
// describe and it is the more common thing to miss. A rule no vector exercises cannot be seen by any
// instrument that reads verdicts, whichever direction it reads them in — the corpus does not disagree
// with it, it declines to ask. `offset_test.go` builds the discriminating modules by hand for exactly
// that reason, and its tripwire watches the *reference's* text, since the suite has no channel
// through which this decision could ever report itself wrong. (Ruling: Scott, #310.)
//
// # The blind spot shrank measurably for the first time, and the measurement is what says so
//
// Everything above describes a population this file cannot see. #310 is the first entry that also
// *drained* it by a counted amount: `validateAdmitCeiling` **104 → 103**, one admission becoming a
// pass. The number matters less than the channel it came out of — a bare `+1` in the pass column is
// consistent with either an under-rejection repaired or a decline gaining vocabulary, and only the
// first is a shrink of this blind spot. So the ledger row asserts the *pair*: `accepted` down one and
// `pass` up one with `declined` untouched (`internal/spec/ledger_test.go`), which is a correctness
// claim where a lone increment would have been a movement.
//
// Worth having here rather than only in the ledger, because a coverage claim with no measured
// direction of travel is a claim that can only ever be restated. The population this file describes
// is now 103 admissions, and the next slice's reward is a subtraction from that figure. (Note:
// Scott, on the #317 relay.)
//
// # Correction: the three `module quote` vectors are not in the 103, and the sentence that said they
// were is the one that was here
//
// The first version of the paragraph above read "103 admissions, three of which are the `module
// quote` vectors no validator rule can reach". **Measured, they are in a different column.** All
// three — `address.wast:213` and `simd_address.wast:143,151` — are `assert_invalid` commands whose
// payload is a quoted module the wast reader does not build, so they score **unsupported**
// (`address.wast: 259/259 pass, 1 unsupported`; `simd_address.wast: 47/47 pass, 2 unsupported`) and
// are never handed to this package at all. `align.go` and `validate.go` both say `unsupported` about
// the same three vectors correctly; this file's sentence was written from memory rather than from the
// board, which is the drift a claim about *another instrument's column* invites.
//
// The consequence inverts what the wrong sentence implied, which is why it is worth more than a
// silent edit. Carried inside the admissions they would be permanently-unreachable residue, and an
// allowance holding residue implies a drain that can never complete — so the 103 would not be
// expected to reach zero. In the column they are actually in they are **drainable by harness work**
// (#8's text-format lane; #53 landed the lexer half, and a bare quoted module asserts *validity*,
// which is this package's word to give), and the 103 is expected to reach zero whole. Both figures
// keep their own instrument: the admissions are `validateAdmitCeiling`'s, the three are
// `unsupportedCeiling`'s.
//
// Recorded rather than repaired quietly because the residue treatment in #317 was asked for *on the
// strength of this sentence* — a wrong premise about a population is the one kind of error that
// arrives back as an instruction. (Correction: on the #317 relay.)

// TestUnknownCategoriesMatchTheReference is ErrUnknown*'s own promised control, in both
// directions.
//
// `valid.ml` has one `lookup`, not nine, and it composes the message as `"unknown " ^ category ^
// " " ^ index`. The ten categories are ten one-line bindings (`:44-53`), so the authority's set is
// *parsed* rather than transcribed: a renamed category fails the forward direction and a new one
// fails the reverse.
//
// The reverse direction is the half that matters. Slice 1 claims seven of the ten, and the three it
// does not claim are pinned as a literal set — so a later slice adding `tag` has to come here and
// say so, which is the difference between a scope declaration and a gap.
func TestUnknownCategoriesMatchTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefValidML)

	// `let <name> (c : context) x = lookup "<category>" ...` — keyed on the string literal
	// argument, which is the thing the message is built from, not on the binding's name.
	re := regexp.MustCompile(`lookup\s+"([^"]+)"`)
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		found[m[1]] = true
	}

	// Vacuity, and pinned exactly rather than floored. A regex that stops matching yields an
	// empty set, and an empty set agrees with every sentinel below by construction — the
	// comparison-against-nothing shape. Exact because the reference is fetched at a pin (#42's
	// discipline, applied to the interpreter rather than to the suite): upstream adding an
	// eleventh category is a fact for a reader to record, not churn to absorb.
	const wantCategories = 10
	if len(found) != wantCategories {
		t.Fatalf("parsed %d lookup categories from %s, want %d (%v); either the regex stopped "+
			"matching — in which case every check below is comparing against nothing — or "+
			"upstream changed the index spaces, which is a finding either way",
			len(found), testenv.RefValidML, wantCategories, sortedKeys(found))
	}

	// Forward: every sentinel this package declares must name one of the reference's categories,
	// spelled `unknown ` + the category and nothing else. The trailing-detail rule is a *format*
	// question and is checked separately, below.
	claimed := map[string]bool{}
	for _, err := range []error{
		ErrUnknownType, ErrUnknownGlobal, ErrUnknownMemory, ErrUnknownTable,
		ErrUnknownFunc, ErrUnknownLocal, ErrUnknownLabel,
		// Slice 5's two arrivals. They were `wantUnclaimed`'s second and third entries, and the
		// reverse direction below is what required this line to be added rather than allowing the
		// sentinels to appear with the list unchanged — which is the whole of what that list buys.
		ErrUnknownDataSegment, ErrUnknownElemSegment,
	} {
		msg := err.Error()
		cat, ok := strings.CutPrefix(msg, "unknown ")
		if !ok {
			t.Errorf("%q does not begin with %q; the reference composes this message as "+
				`"unknown " ^ category ^ " " ^ index, so the prefix is not stylistic`, msg, "unknown ")
			continue
		}
		if !found[cat] {
			t.Errorf("%q names category %q, which %s does not have (%v) — a sentinel spelling "+
				"a category the authority never uses reports a verdict no vector expects",
				msg, cat, testenv.RefValidML, sortedKeys(found))
			continue
		}
		claimed[cat] = true
	}

	// Reverse: exactly which of the ten slice 1 leaves alone, pinned so the next slice states
	// its arrival instead of inheriting it.
	//
	//   - `tag`: exception handling's index space (a later gate's).
	//
	// **`data segment` and `elem segment` were here until slice 5 and are not any more**, and the
	// entry they left is worth keeping as the record of why the list works. Their stated reason was
	// that the bulk memory/table ops are "declined at the prefixed-opcode arm before any index is
	// read" — a deferral whose subject was the *dispatch*, not the rules, so it expired the moment
	// 0xFC was typed and the two sentinels arrived in the same commit that removed it. That is the
	// list behaving as designed: it made a slice that claims a category come here and say so.
	wantUnclaimed := map[string]bool{"tag": true}
	for cat := range found {
		switch {
		case claimed[cat] && wantUnclaimed[cat]:
			t.Errorf("category %q is both claimed by a sentinel and listed as out of scope; the "+
				"list is a scope declaration and cannot also describe a rule that exists", cat)
		case !claimed[cat] && !wantUnclaimed[cat]:
			t.Errorf("category %q is one of the reference's index spaces, no sentinel here names "+
				"it, and it is not on the out-of-scope list — so it is a gap wearing no label. "+
				"Either declare it (add it to wantUnclaimed with the slice that owns it) or "+
				"implement it", cat)
		}
	}
	for cat := range wantUnclaimed {
		if !found[cat] {
			t.Errorf("%q is listed as an unclaimed reference category but %s has no such lookup; "+
				"a deferral that names something the authority does not have reads as tracked "+
				"while tracking nothing", cat, testenv.RefValidML)
		}
	}
}

// TestUnknownIndexMessagesAreCategorySpaceIndex is the *format* half, and it is an AST control
// because the hazard is a call site rather than a sentinel.
//
// The corpus expects both `unknown local` and `unknown local 2`, matched as substrings (decision
// 0003). So `fmt.Errorf("%w: local %d", ErrUnknownLocal, i)` satisfies the first vector and fails
// the second while being entirely right about the module — a wrong verdict on a right analysis,
// and invisible to any test that only checks the sentinel. The rule is that the index follows the
// category immediately, and any detail this validator wants to add goes *after* it.
//
// Derived, not enumerated: the sentinel set comes from the package's own `ErrUnknown*` declarations
// and the call sites from every `fmt.Errorf` mentioning one, so a ninth sentinel or a new site is
// covered without an edit here.
//
// # This is the coverage half of a two-part check, and it is checking a proxy
//
// What the rule is actually about is the *rendered* message, and an AST walk cannot render one. So
// this checks the format directive — a proxy — over **every** site, and the rendered strings are
// checked over a **sample** by the behavioural witnesses (`TestUnknownIndexMessagesRender`), which
// call the validator and read what comes out. Neither is sufficient: the AST walk cannot see what
// `%d` prints, and the witnesses cannot see a site no vector reaches.
//
// The proxy admits a **literal** index as well as `%d`, and that is not a loosening to make a
// failure go away — it is the first thing this control found. `addrType`'s no-memory verdict is
// `fmt.Errorf("%w 0 (module declares no memory)", ErrUnknownMemory)`: memory index 0 is the only
// index a slice-1 memory access can name, so the index is a constant and hardcoding it renders
// `unknown memory 0 (…)`, which is exactly the shape the rule demands. Requiring the *directive*
// `%d` there would have been a control insisting on its own proxy over the property.
func TestUnknownIndexMessagesAreCategorySpaceIndex(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing this package: %v", err)
	}
	fset := token.NewFileSet()
	sentinels := map[string]bool{}
	type site struct {
		format string
		pos    string
		name   string
	}
	var sites []site

	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue // the sentinels and their sites are engine code; a test's prose is not a message
		}
		f, err := goparser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				for _, id := range vs.Names {
					if strings.HasPrefix(id.Name, "ErrUnknown") {
						sentinels[id.Name] = true
					}
				}
			}
			return true
		})
	}
	// Vacuity on the sentinel set before the sites are read: zero sentinels means zero sites
	// match, and a walk over nothing agrees with any format string in the package.
	if len(sentinels) < 7 {
		t.Fatalf("found %d ErrUnknown* declarations in this package's AST, want at least the 7 "+
			"slice 1 declares; the trigger stopped matching, so the format check below has no "+
			"subject", len(sentinels))
	}

	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := goparser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Errorf" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, arg := range call.Args[1:] {
				id, ok := arg.(*ast.Ident)
				if !ok || !sentinels[id.Name] {
					continue
				}
				format, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Errorf("%s: cannot unquote format string: %v", fset.Position(lit.Pos()), err)
					continue
				}
				sites = append(sites, site{format, fset.Position(call.Pos()).String(), id.Name})
			}
			return true
		})
	}

	// A second vacuity floor, on the *sites* rather than the sentinels: seven sentinels with zero
	// call sites is the unreachable-constant shape, and it would pass a loop over `sites`
	// silently.
	if len(sites) < 7 {
		t.Fatalf("found %d fmt.Errorf sites wrapping an ErrUnknown* sentinel, want at least 7 "+
			"(one per sentinel); a sentinel with no site is a declared verdict nothing returns",
			len(sites))
	}

	// `%w ` then the index: either the directive or a decimal literal, and nothing else in
	// between. A prefix match rather than equality, because detail *after* the index is allowed.
	indexFirst := regexp.MustCompile(`^%w (%d|\d)`)
	literalIndex := 0
	for _, s := range sites {
		if !indexFirst.MatchString(s.format) {
			t.Errorf("%s: %s is formatted %q; the reference's message is `unknown <category> "+
				"<index>`, so the format must put the index immediately after the sentinel — `%%w "+
				"%%d`, or a literal where only one index is possible — and any detail after it. A "+
				"colon or a repeated category word here satisfies the corpus's `unknown local` "+
				"vectors and fails its `unknown local 2` vectors, on an analysis that was right",
				s.pos, s.name, s.format)
			continue
		}
		if !strings.HasPrefix(s.format, "%w %d") {
			literalIndex++
		}
	}
	t.Logf("%d ErrUnknown* sentinels, %d wrapping sites (%d with a literal index), all with the "+
		"index immediately after the category", len(sentinels), len(sites), literalIndex)
}

// TestStructuralOpcodesMatchTheTable checks the named constants in instr.go against
// `binary.OpMnemonic`, the authority's own transcribed table.
//
// # The domain this cannot check, stated because it is most of the value
//
// The table's `ok` means "there is a row", not "there is a name" (see `mnemonic`'s comment), and
// two of the constants here name opcodes whose rows carry an **empty** mnemonic: `else` (0x05) and
// `end` (0x0B) are sequence terminators in `decode.ml`, not named instructions. So for those two
// this control confirms only that the byte has a row — the *name* is this package's own, and no
// authority disagrees with it.
//
// One further blind spot: `select` (0x1B) and `select t` (0x1C) both render as `select` in the
// table, so a swap of those two constants would pass. What catches that swap is behavioural —
// `TestSelectAnnotatedTypesAgainstItsAnnotation` — and it is named here rather than left implied,
// because a control's domain is an assertion it cannot check about itself.
//
// That citation previously cited `TestSelectAnnotatedIsDeclinedAndBareIsNot`, and #294 rewrote the
// control the moment it typed the annotated form. The rename was caught by
// `TestEveryCitedTestNameResolves` rather than by anyone remembering this sentence — which is the
// blind spot the paragraph is about, one level up: a domain note is a citation, and a citation that
// no longer resolves documents a direction nothing checks.
func TestStructuralOpcodesMatchTheTable(t *testing.T) {
	// name → opcode, as instr.go's switch reads them. Written out rather than reflected because
	// they are untyped constants: there is nothing at run time to enumerate.
	cases := []struct {
		want string
		op   uint32
	}{
		{"unreachable", opUnreachable},
		{"nop", opNop},
		{"block", opBlock},
		{"loop", opLoop},
		// `if_`, with the underscore, is the authority's own spelling: `if` is an OCaml keyword,
		// so `decode.ml:371` escapes it and `optable.go:77` transcribes the escape. Found by this
		// control on its first run, expecting "if" — the table is right and the expectation was a
		// guess about the reference dressed as a fact about it.
		{"if_", opIf},
		{"br", opBr},
		{"br_if", opBrIf},
		{"br_table", opBrTable},
		{"return", opReturn},
		{"call", opCall},
		{"call_indirect", opCallIndirect},
		{"drop", opDrop},
		{"select", opSelect},
		{"select", opSelectT}, // both render `select`; see the doc comment's domain note
		{"local_get", opLocalGet},
		{"local_set", opLocalSet},
		{"local_tee", opLocalTee},
		{"global_get", opGlobalGet},
		{"global_set", opGlobalSet},
	}
	for _, c := range cases {
		name, ok := binary.OpMnemonic(c.op)
		if !ok {
			t.Errorf("opcode %#02x (this package calls it %q) has no row in binary.OpMnemonic — "+
				"a structural constant naming a byte the authority's table does not have",
				c.op, c.want)
			continue
		}
		if name != c.want {
			t.Errorf("opcode %#02x is %q in the authority's table, this package's constant is "+
				"named for %q", c.op, name, c.want)
		}
	}

	// The two whose rows are nameless, asserted as nameless. If upstream ever gives `end` a
	// mnemonic, `mnemonic()`'s hand-spelled fallback becomes a second authority for the same
	// fact and this is where that is noticed.
	for _, c := range []struct {
		op   uint32
		name string
	}{{opElse, "else"}, {opEnd, "end"}} {
		got, ok := binary.OpMnemonic(c.op)
		if !ok {
			t.Errorf("opcode %#02x (%s) has no row at all in binary.OpMnemonic; the empty-name "+
				"case this test pins is a row *with* an empty name", c.op, c.name)
			continue
		}
		if got != "" {
			t.Errorf("opcode %#02x now has the mnemonic %q in the authority's table, where it "+
				"was nameless; mnemonic()'s hand-spelled %q is now a second authority for one "+
				"fact and should be deleted in favour of the table", c.op, got, c.name)
		}
	}
}

// TestEveryNumericOpcodeHasASignature is sig.go's promised control, and it is scoped to the
// *space* rather than to the operators that happen to be listed.
//
// The domain is derived: every single-byte opcode with a row in `binary.OpMnemonic` whose mnemonic
// begins with a numeric type prefix is, by definition, a member of the family slice 1 claims to
// type. So `signature` must return a signature for each of them — not a decline. An operator
// missing from `unaryOps`/`binaryOps`/`compareOps` falls through to `ErrUnsupported`, which is a
// visible refusal in a named bucket and therefore *not* the accept-direction hazard; but it is
// still a rule slice 1 says it implements and does not, which is a lie in the scope declaration
// rather than in the verdict.
//
// A module is needed because memory accesses read their address type from it, so this walks with a
// one-memory module — the case that makes `load`/`store` answerable at all.
func TestEveryNumericOpcodeHasASignature(t *testing.T) {
	m := &binary.Module{Memories: []binary.Memory{{}}}

	var missing []string
	checked := 0
	for op := range uint32(0x100) {
		name, ok := binary.OpMnemonic(op)
		if !ok || name == "" {
			continue
		}
		prefix, _, found := strings.Cut(name, "_")
		if !found {
			continue
		}
		if _, isNum := numType(prefix); !isNum {
			continue
		}
		checked++
		if _, err := signature(m, binary.Instr{Op: op}); err != nil {
			missing = append(missing, name)
		}
	}

	// Vacuity, and **both bounds, because a floor alone cannot see a small loss.** The loose one
	// catches the catastrophic case (a `Cut` on the wrong separator, a renamed table: the walk
	// collapses and agrees with everything); the exact one catches the case a floor is blind to,
	// which is six opcodes quietly dropping out of the derivation while 149 still pass.
	//
	// Exact is affordable here and it would not be on a board count: this domain comes from
	// `optable.go`, which is **committed**, so the figure moves when someone edits the table and
	// never on upstream's schedule. That is decision 0012's situation (both inputs in the tree →
	// exact golden), not #42's — and *the strongest control the inputs admit, at each site*.
	const (
		numericWalkFloor = 120
		numericWalkExact = 155
	)
	if checked < numericWalkFloor {
		t.Fatalf("walked only %d numeric-prefixed opcodes, want ≥%d; the domain derivation "+
			"stopped matching, so an operator class could be empty and this test would agree",
			checked, numericWalkFloor)
	}
	if checked != numericWalkExact {
		t.Errorf("walked %d numeric-prefixed opcodes, want exactly %d. The single-byte numeric "+
			"space is fixed by a committed table, so a change here is either a table edit (re-base "+
			"this constant in that PR) or the derivation losing members — and a floor cannot tell "+
			"a loss of six from a healthy board", checked, numericWalkExact)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d numeric opcodes have no signature (%v); each falls through to "+
			"ErrUnsupported, so it is declined in a named bucket rather than wrongly accepted — "+
			"but slice 1's scope claims the numeric families, and an operator missing from "+
			"unaryOps/binaryOps/compareOps makes that claim false", len(missing), missing)
	}
	t.Logf("%d numeric-prefixed opcodes, all with a derived signature", checked)
}

// TestOperatorClassesAreDisjoint is the other half of sig.go's reason for using sets: `signature`
// tests the classes in order, so an operator in two of them silently takes the first arm and its
// second classification is unreachable — the shape grave 0003 is about, one layer down.
//
// `div` is the case that makes this real rather than hypothetical: it is a float binary operator
// and the integer forms are `div_s`/`div_u`, so a well-meaning addition of `"div.s"` to
// `unaryOps` would type `i32.div_s` as unary and be caught by nothing else here.
func TestOperatorClassesAreDisjoint(t *testing.T) {
	classes := map[string]map[string]bool{
		"unaryOps":   unaryOps,
		"binaryOps":  binaryOps,
		"compareOps": compareOps,
	}
	seen := map[string]string{}
	for _, name := range sortedKeys(map[string]bool{"unaryOps": true, "binaryOps": true, "compareOps": true}) {
		for _, op := range sortedKeys(classes[name]) {
			if prev, dup := seen[op]; dup {
				t.Errorf("operator %q is in both %s and %s; signature() tests the classes in "+
					"order, so the second classification is unreachable and the operator is "+
					"typed by whichever arm comes first", op, prev, name)
				continue
			}
			seen[op] = name
		}
	}
	// Exact, for the reason the numeric walk is exact: the three classes are in this package, so
	// the population moves only when someone edits them. A floor would let a class lose four
	// operators silently, and an operator dropping out of every class is a *decline* — visible in
	// a bucket, but visible as "out of scope" for a rule slice 1 claims.
	const classPopulation = 46
	if len(seen) != classPopulation {
		t.Errorf("the three operator classes hold %d operators between them, want exactly %d "+
			"(unaryOps 13, binaryOps 19, compareOps 14). An emptied class makes this disjointness "+
			"check trivially true; a shrunken one makes it partly so", len(seen), classPopulation)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
