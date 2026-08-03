package opgen

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/gen/opcodegen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The three authorities this join reads, and it reads all three of them at the *same*
// revision — which is not a precaution but the join's precondition. A constructor name is
// only a valid key if the file that produces it and the file that consumes it are the same
// upstream tree; joining decode.ml at one revision against parser.mly at another would
// produce a table whose every row is individually defensible and whose provenance header is
// a lie. `scripts/fetch-spec-ref.sh` vendors one tree at one pin, so this is guaranteed by
// the fetch rather than asserted here — but stated, because a precondition that lives in a
// shell script and nowhere else is a precondition nobody reading this package can see.

func refs(tb testing.TB) (mly, lex string, kws []Keyword, ops OpTable) {
	tb.Helper()

	dec := testenv.RequireSpecRef(tb, testenv.RefDecodeML)
	mly = testenv.RequireSpecRef(tb, testenv.RefParserMLY)
	lex = testenv.RequireSpecRef(tb, testenv.RefLexerMLL)

	ot, err := opcodegen.Extract(dec, "test")
	if err != nil {
		tb.Fatalf("opcodegen.Extract: %v", err)
	}
	kt, err := keywordgen.Extract(lex, "test")
	if err != nil {
		tb.Fatalf("keywordgen.Extract: %v", err)
	}
	return mly, lex, KeywordsOf(kt), OpsOf(ot)
}

// TestAuthoritiesPartitionTheKinds is decision 0014's premise standing as a control, and it
// is the reason C was available rather than a paragraph asserting C was available.
//
// The claim: **every token kind that carries an instruction has its constructor named by
// exactly one of the two authorities.** The grammar names 58 in its semantic actions
// (`| NOP { fun c -> nop }`); the lexer names the rest in its token payloads
// (`"i32.add" -> BINARY i32_add`, consumed by `| BINARY { fun c -> $1 }`). Overlap 0, gap 0
// over instruction-bearing kinds.
//
// **The 58 is the corrected figure and the correction is the reason this test exists as it
// does.** 0014 was written against 51, measured over `plaininstr` alone — which is where the
// reader looked, so the measurement and the reader shared a blind spot and agreed. The seven
// missing kinds were `select`, `block`, `loop`, `if`, `call_indirect`,
// `return_call_indirect`, `try_table`: every control-flow construct, joined to nothing. A
// premise measured over the same sample the code reads is not a premise, it is an echo.
//
// Both halves are asserted, because they fail differently and each alone is survivable by a
// broken join. **Overlap** would mean the join needs a precedence rule nobody ruled on —
// Extract errors on it rather than picking, so this pins the premise that makes the error
// unreachable. **A gap** would mean a kind with an opcode that neither authority names, so
// its keywords silently do not join and the encoder is missing instructions: the
// accept-direction failure this whole ADR exists to avoid, invisible on the board because a
// module the encoder cannot emit is a module the suite never sees emitted wrong.
func TestAuthoritiesPartitionTheKinds(t *testing.T) {
	mly, lex, kws, ops := refs(t)

	byGrammar, err := grammarConstructors(mly, ops)
	if err != nil {
		t.Fatalf("grammarConstructors: %v", err)
	}
	byLexer, err := lexerConstructors(lex, ops)
	if err != nil {
		t.Fatalf("lexerConstructors: %v", err)
	}

	// Overlap: no kind is named by both.
	kindOfKeyword := map[string]string{}
	for _, kw := range kws {
		kindOfKeyword[kw.Keyword] = kw.Kind
	}
	for keyword := range byLexer {
		kind := kindOfKeyword[keyword]
		if _, both := byGrammar[kind]; both {
			t.Errorf("kind %s (keyword %q) is named by both parser.mly and lexer.mll; "+
				"0014's premise was overlap 0 and the join has no precedence rule", kind, keyword)
		}
	}

	// Gap: every kind appearing on a keyword whose *sibling* keywords join must itself
	// join. Scoped to the space of kinds the keyword table actually produces, not to the
	// kinds this join happens to resolve — the latter would be the control-scoped-to-the-
	// sample failure, agreeing with itself by construction.
	unnamed := map[string][]string{}
	for _, kw := range kws {
		if _, ok := byGrammar[kw.Kind]; ok {
			continue
		}
		if _, ok := byLexer[kw.Keyword]; ok {
			continue
		}
		unnamed[kw.Kind] = append(unnamed[kw.Kind], kw.Keyword)
	}

	// **The gap half needs a detector of "this kind has an opcode" that does not come from
	// the join**, or it agrees with itself: asking "did every kind the join resolved get
	// resolved?" is a tautology, and it is exactly the tautology that let the seven
	// control-flow kinds sit in the gap while the premise read as clean.
	//
	// The independent signal is the mnemonic's own spelling with dots turned to underscores.
	// 0014 rejected that transformation as the join *key* — it is a naming coincidence, not a
	// derivation — and being unfit to key on is precisely what makes it a good second opinion:
	// it knows nothing about either authority's arm shapes, so it cannot share their blind
	// spot. If `i32.add` spells a name the opcode table holds, then `i32.add` has an opcode,
	// whatever any grammar says.
	for kind, keywords := range unnamed {
		for _, kw := range keywords {
			spelled := strings.ReplaceAll(kw, ".", "_")
			if len(ops.CodesFor(spelled)) == 0 {
				continue
			}
			t.Errorf("keyword %q (kind %s) spells constructor %q, which the opcode table holds — "+
				"so it has an encoding — but neither authority named it, so it joins to nothing "+
				"and #8's encoder cannot emit it. No vector can see this: a module the encoder "+
				"never emits is never emitted wrong (§9 G-3).", kw, kind, spelled)
		}
	}

	// Vacuity: the detector must have found *something* to be capable of finding a gap.
	// Without this, a broken `ops` makes every CodesFor return nil, the loop above never
	// fires, and the gap half passes by asking nothing — the empty-set agreement inside a
	// control written against that very defect.
	var detectable int
	for _, kw := range kws {
		if len(ops.CodesFor(strings.ReplaceAll(kw.Keyword, ".", "_"))) > 0 {
			detectable++
		}
	}
	if detectable < 400 {
		t.Errorf("the mnemonic-spelling detector recognizes only %d of %d keywords as having an "+
			"encoding; it has stopped detecting and the gap check above is vacuous",
			detectable, len(kws))
	}

	if len(byGrammar) != 58 {
		t.Errorf("grammar-named kinds: got %d, want 58 (measured at the pinned revision)", len(byGrammar))
	}
	if len(byLexer) != 436 {
		t.Errorf("lexer-named keywords: got %d, want 436 (measured at the pinned revision)", len(byLexer))
	}
}

// TestExtractMatchesMeasuredShape pins the join's exact shape at the pinned revision.
//
// **Exact counts, not floors, and the floors do not make this redundant** — that is the
// finding this test is written around. The lexer reader's first draft required the token
// kind on the arm's head line, which is true of 564 arms and false of 25 (the `const`
// family, the `v128.*_lane`/`_splat` group): those 25 were absorbed as continuation lines of
// the preceding keyword and vanished silently, producing **411** rows where 436 were
// measured. `Floors.Lexer` is 350. The floor was green. Only a count that knows the right
// answer could say otherwise.
//
// So this is the vacuity law's own limit stated as a test: a floor bounds the *catastrophic*
// case (a moved file, zero rows) and cannot see a 6% silent loss, which is exactly the size
// of loss an under-matching trigger produces. The revision is pinned, so an exact count is
// the honest assertion — nothing upstream can change without the SHA changing.
func TestExtractMatchesMeasuredShape(t *testing.T) {
	mly, lex, kws, ops := refs(t)
	tab, err := Extract(mly, lex, kws, ops, "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var nGrammar, nLexer int
	for _, r := range tab.Rows {
		switch r.Origin {
		case FromGrammar:
			nGrammar++
		case FromLexer:
			nLexer++
		default:
			t.Errorf("row %q has origin %q, which is neither authority", r.Keyword, r.Origin)
		}
	}

	for _, c := range []struct {
		what      string
		got, want int
	}{
		{"joined rows", len(tab.Rows), 494},
		{"rows from parser.mly's grammar bodies", nGrammar, 58},
		{"rows from lexer.mll's token payloads", nLexer, 436},
		{"keywords with no constructor", tab.Unjoined, 95},
		{"ambiguous constructors", len(tab.Ambiguous), 3},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %d, want %d", c.what, c.got, c.want)
		}
	}

	if len(kws) != tab.Unjoined+len(tab.Rows) {
		t.Errorf("%d keywords in, %d joined + %d unjoined = %d out: the join lost rows without "+
			"accounting for them", len(kws), len(tab.Rows), tab.Unjoined, tab.Unjoined+len(tab.Rows))
	}
}

// TestAmbiguousMnemonicsAreTheOperandDirectedThree pins *which* mnemonics are ambiguous, not
// how many.
//
// All three are pairs the reference distinguishes by what follows the mnemonic rather than by
// the mnemonic: `select` bare versus with a result type, `ref.test`/`ref.cast` on nullable
// versus non-nullable references. A map keyed on mnemonic alone would silently return one of
// the two and be wrong on `(select (result i32))` **with no board consequence**, because both
// encodings decode clean — so the join emits both codes and the encoder must choose on the
// operand it read.
//
// A count would pass if a fourth mnemonic became ambiguous while one of these three stopped
// being — the same reason the unnamed kinds above are pinned as a set.
func TestAmbiguousMnemonicsAreTheOperandDirectedThree(t *testing.T) {
	mly, lex, kws, ops := refs(t)
	tab, err := Extract(mly, lex, kws, ops, "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	got := map[string][]Code{}
	for _, a := range tab.Ambiguous {
		got[a.Constructor] = a.Codes
	}
	want := map[string][2]Code{
		"select":   {{Prefix: 0, Code: 0x1b}, {Prefix: 0, Code: 0x1c}},
		"ref_test": {{Prefix: 0xfb, Code: 0x14}, {Prefix: 0xfb, Code: 0x15}},
		"ref_cast": {{Prefix: 0xfb, Code: 0x16}, {Prefix: 0xfb, Code: 0x17}},
	}
	for ctor, codes := range want {
		g, ok := got[ctor]
		if !ok {
			t.Errorf("%s is not reported ambiguous, but the reference gives it two encodings "+
				"(%v and %v) chosen on the operand — a join that picks one is silently wrong on "+
				"the other, and both decode clean", ctor, codes[0], codes[1])
			continue
		}
		if len(g) != 2 || g[0] != codes[0] || g[1] != codes[1] {
			t.Errorf("%s: got codes %v, want %v", ctor, g, codes)
		}
		delete(got, ctor)
	}
	for ctor, codes := range got {
		t.Errorf("%s is ambiguous (%v) and unaccounted for: a mnemonic with two encodings needs "+
			"an operand rule at the point of use, so a new one is a decision, not a count", ctor, codes)
	}
}

// TestConstructorsAgreeWithMnemonicSpelling is the control on constructorIn's residual risk:
// an OCaml local variable that shares a name with an instruction constructor and appears
// earlier in the expression than the real one, which would make the join return the wrong
// opcode for a keyword that nonetheless joins.
//
// The check is independent of the mechanism it checks. constructorIn works by *filtering
// identifiers through the opcode table*; this compares the constructor it chose against the
// wat mnemonic's own spelling with dots turned to underscores. Where the two are comparable
// they must agree, and 0014 explicitly rejected using this transformation as the join key —
// it is a naming coincidence, not a derivation, so it makes a fine cross-check and a bad
// authority. That is the whole point: a second opinion is only worth having if it comes from
// somewhere else.
//
// The exemptions are the arms where the reference's constructor genuinely is not the
// mnemonic. They are listed rather than pattern-matched, and the *count* is asserted, so an
// exemption silently growing to cover a real disagreement fails here.
func TestConstructorsAgreeWithMnemonicSpelling(t *testing.T) {
	mly, lex, kws, ops := refs(t)
	tab, err := Extract(mly, lex, kws, ops, "test")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var compared, exempted int
	for _, r := range tab.Rows {
		spelled := strings.ReplaceAll(r.Keyword, ".", "_")
		if r.Constructor == spelled {
			compared++
			continue
		}
		exempted++
	}

	// A vacuity floor on the comparison itself: if the spelling rule stopped matching
	// anything, every row would be "exempt" and this test would agree with nothing. *A
	// comparison against an empty set succeeds.*
	if compared < 400 {
		t.Errorf("only %d of %d rows had a constructor matching their mnemonic's spelling; "+
			"this cross-check has stopped comparing anything and is agreeing vacuously",
			compared, len(tab.Rows))
	}
	if exempted > 90 {
		t.Errorf("%d rows disagree with their mnemonic's spelling, want <=90: either the "+
			"reference renamed constructors, or constructorIn is picking up a local variable "+
			"that shadows a constructor name", exempted)
	}
}

// TestLexerBlockAgreesWithKeywordgen justifies this package's duplicated block-locator
// regexps, which are *one concept, two triggers* (#82) — the defect class that let a fixture
// file register with a checker unable to read it.
//
// The duplication is not avoidable: keywordgen's locators are unexported and this is a
// different package, so the honest options were duplication or exporting a locator, and
// exporting one would make keywordgen's internal grammar part of its API. What makes
// duplication safe is this test — the two locators must find *the same arm set*, so a
// divergence is a failure rather than a silent difference in which lines each reader
// considers a keyword arm.
//
// Set comparison, not counts: two readers finding 436 arms each could be finding different
// 436 arms, which is precisely the failure a count cannot see.
func TestLexerBlockAgreesWithKeywordgen(t *testing.T) {
	_, lex, kws, ops := refs(t)

	mine, err := lexerConstructors(lex, ops)
	if err != nil {
		t.Fatalf("lexerConstructors: %v", err)
	}

	theirs := map[string]int{}
	for _, kw := range kws {
		theirs[kw.Keyword] = kw.Line
	}

	// Every keyword this reader found must be one keywordgen found, at the same line. The
	// converse does not hold — keywordgen finds all 589 arms, this reader returns only those
	// whose payload names a constructor — so the containment is asserted in the direction
	// that is actually a claim.
	for keyword, n := range mine {
		line, ok := theirs[keyword]
		if !ok {
			t.Errorf("this package's locator found keyword %q (lexer.mll:%d) that keywordgen's "+
				"did not: the two readers disagree about which lines are keyword arms, so one of "+
				"them is reading a block that is not the block", keyword, n.line)
			continue
		}
		if line != n.line {
			t.Errorf("keyword %q: keywordgen reads it at lexer.mll:%d, this package at :%d",
				keyword, line, n.line)
		}
	}

	if len(mine) < Floors.Lexer {
		t.Errorf("agreement checked over only %d keywords: an empty reader agrees with "+
			"everything", len(mine))
	}
}

// TestWrappedArmsAreRead is the grave's regression test, and it names its three populations
// rather than asserting a count — because the count is what did not catch this.
//
// Each of these arms puts its constructor on a line *after* the `->`. The head regexp's
// first draft required `-> TOKEN` together, so all 25 such arms were swallowed as
// continuation lines of the preceding keyword: no error, no unrecognized arm, 411 rows
// instead of 436, and `Floors.Lexer` (350) green throughout. An under-matching trigger
// produces no finding rather than a wrong one (#78/#82).
func TestWrappedArmsAreRead(t *testing.T) {
	_, lex, _, ops := refs(t)
	mine, err := lexerConstructors(lex, ops)
	if err != nil {
		t.Fatalf("lexerConstructors: %v", err)
	}

	for _, c := range []struct{ keyword, ctor string }{
		// The five `const` forms — payload is a closure spanning two lines.
		{"i32.const", "i32_const"},
		{"i64.const", "i64_const"},
		{"f32.const", "f32_const"},
		{"f64.const", "f64_const"},
		{"v128.const", "v128_const"},
		// The lane family — payload is a closure with four parameters.
		{"v128.store64_lane", "v128_store64_lane"},
		{"v128.load8_lane", "v128_load8_lane"},
		// A splat, for the third population.
		{"v128.load32_splat", "v128_load32_splat"},
		// And one unwrapped arm, so a reader that *only* handled wrapped arms would fail
		// too: the partition has two sides and a regression test for one side is half a
		// control.
		{"i32.add", "i32_add"},
	} {
		if got := mine[c.keyword].ctor; got != c.ctor {
			t.Errorf("%q joined to constructor %q, want %q — a wrapped arm's payload was not "+
				"read, which loses the row silently", c.keyword, got, c.ctor)
		}
	}
}

// TestUnreadableArmIsAnErrorNotASkip falsifies the reader's loud half.
//
// The wrapped-arm defect was silent *because* the head regexp did not match, so the line
// never reached the error path — it looked like a continuation. `reLexArmish` is the
// discriminator that closes that: a line opening with `| "` that does not parse as an arm is
// an error. This introduces exactly that line and asserts the error, per *a control's green
// must be falsifiable, and the way to know is to break it*.
func TestUnreadableArmIsAnErrorNotASkip(t *testing.T) {
	_, lex, _, ops := refs(t)

	// Inject a line inside the keyword block that looks like an arm and has no `->`.
	const anchor = `  | "i32.add" -> BINARY i32_add`
	// A missing anchor is a **failure, not a skip**, and the two sibling generators already
	// settled this: a mutation that did not apply leaves the test asserting the *unmodified*
	// reader's behaviour, which passes. So a skip here would not decline to answer, it would
	// answer the wrong question and score it green — the worse half of *a skip is not a
	// verdict*. Caught by TestEverySkipSiteIsLicensed, which is the mechanism working.
	if !strings.Contains(lex, anchor) {
		t.Fatalf("mutation did not apply: anchor %q changed upstream, so this control is "+
			"asserting the unmodified reader; re-point the injection", anchor)
	}
	broken := strings.Replace(lex, anchor, "  | \"i32.wrongly.written\"\n"+anchor, 1)

	if _, err := lexerConstructors(broken, ops); !errors.Is(err, ErrUnrecognized) {
		t.Errorf("an arm-shaped line with no `->` gave error %v, want ErrUnrecognized: a line "+
			"the reader cannot parse must be a failure, because the alternative is the silent "+
			"absorption that lost 25 arms", err)
	}
}

// TestVacuityIsCaughtPerPartition falsifies the floors, and it does so *per authority* —
// which is the whole reason there are three floors rather than one total.
//
// A single total floor of 400 would be satisfied by the lexer's 436 alone. So an extraction
// where `grammarConstructors` broke completely — a renamed production, a moved file, 0 of 51
// rows — would clear it, and 51 instructions including every `br`, `call`, `local.get` and
// `select` would silently have no encoding. **An empty half hidden behind a full one is the
// vacuity defect with a partner to hide behind**, and the only thing that sees it is a floor
// per partition.
//
// Both halves are broken independently, because a shared floor would pass one and fail the
// other for the same reason and neither failure would name its cause.
func TestVacuityIsCaughtPerPartition(t *testing.T) {
	mly, lex, kws, ops := refs(t)

	t.Run("grammar half emptied", func(t *testing.T) {
		// Strip the semantic actions' opening brace, which is where grammarConstructors
		// starts reading each arm. This is the upstream-changed-its-action-layout case: the
		// file is present, the arms are present, and the reader finds nothing in them.
		//
		// **Renaming `plaininstr :` used to be this injection and no longer works** — the
		// reader reads every production now, so four others still contribute and the floor
		// stays clear. That the old injection stopped failing is the widening's own
		// evidence: a control whose defect became unreachable had its subject move, so the
		// injection is re-pointed rather than the test deleted (*a tripwire whose subject
		// dissolves is re-pointed*).
		broken := strings.ReplaceAll(mly, "{ fun c ->", "( fun c ->")
		if broken == mly {
			t.Fatal("could not find `{ fun c ->` to break; re-point this injection")
		}
		_, err := Extract(broken, lex, kws, ops, "test")
		if !errors.Is(err, ErrVacuous) {
			t.Errorf("emptying the grammar half gave %v, want ErrVacuous — 436 lexer rows would "+
				"otherwise clear a total floor while 58 instruction kinds, every control-flow "+
				"construct among them, have no encoding", err)
		}
	})

	t.Run("lexer half emptied", func(t *testing.T) {
		broken := strings.Replace(lex, "| keyword as s", "| keyword_renamed as s", 1)
		if broken == lex {
			t.Fatal("could not find the keyword block head to break; re-point this injection")
		}
		_, err := Extract(mly, broken, kws, ops, "test")
		if !errors.Is(err, ErrVacuous) {
			t.Errorf("emptying the lexer half gave %v, want ErrVacuous", err)
		}
	})
}

// TestOverlapIsAnErrorNotAPrecedenceRule falsifies the partition check.
//
// If a kind is ever named by both authorities the join needs a precedence rule, and there is
// no ruling establishing one — so Extract refuses rather than picking. Asserting the refusal
// matters more than it looks: picking silently would be *correct on today's reference* (the
// two authorities agree where they could overlap), so the wrong behaviour would pass every
// count in this file and only be wrong on a future revision nobody is watching for.
func TestOverlapIsAnErrorNotAPrecedenceRule(t *testing.T) {
	mly, lex, kws, ops := refs(t)

	// `BINARY` is a lexer-payload kind: its grammar arm is `| BINARY { fun c -> $1 }`, which
	// names no constructor. Give it one, and the kind is now named by both.
	const before = "| BINARY { fun c -> $1 }"
	// Fatal, not Skip: see TestUnreadableArmIsAnErrorNotASkip's note. An unapplied mutation
	// makes this control assert that the *unmodified* grammar has no overlap, which is true
	// and is not what the test is for.
	if !strings.Contains(mly, before) {
		t.Fatalf("mutation did not apply: anchor %q changed upstream, so this control is "+
			"asserting the unmodified grammar; re-point the injection", before)
	}
	broken := strings.Replace(mly, before, "| BINARY { fun c -> i32_add }", 1)

	_, err := Extract(broken, lex, kws, ops, "test")
	if !errors.Is(err, ErrPartition) {
		t.Errorf("a kind named by both authorities gave %v, want ErrPartition: the join has no "+
			"precedence rule, and inventing one silently would be right on this revision and "+
			"unwatched on the next", err)
	}
}

// TestCommittedTableMatchesTheReference is condition 4 of 0007 for the third generated table,
// and it is the reason internal/text/opcodes.go is allowed to be committed at all.
//
// Same shape as opcodegen's and keywordgen's: re-run the extraction against the pinned
// reference, compare against the committed file byte for byte, and *refuse to run* without
// the reference rather than skipping — a drift check that skips reports agreement with an
// authority it never read (grave #29).
//
// One thing it inherits that the other two do not need: the join reads three sources, and it
// stamps *one* SHA. That is a claim about the vendored tree, not about this test — see the
// note at the top of this file.
func TestCommittedTableMatchesTheReference(t *testing.T) {
	mly, lex, kws, ops := refs(t)
	sha, err := gen.PinnedRefRev()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := Extract(mly, lex, kws, ops, sha)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	code, err := tab.Emit()
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	want, err := gen.GofmtSource(code)
	if err != nil {
		t.Fatal(err)
	}
	path, err := gen.FromRoot(Output)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s disagrees with the reference at %s.\n"+
			"Regenerate with: make opcodes-text\n"+
			"committed %d bytes, extracted %d bytes", Output, sha, len(got), len(want))
		if d := firstDiff(string(got), want); d != "" {
			t.Errorf("first difference:\n%s", d)
		}
	}
}

// firstDiff reports the first line at which two texts diverge.
//
// Duplicated from opcodegen's test rather than shared, and stated because the duplication is
// a judgement rather than an oversight: it is six lines of test scaffolding with no fact in
// it, so *one concept, one trigger* does not apply — that rule is about a mechanism whose two
// copies can disagree about something, and this one carries nothing to disagree about. If a
// third copy appears it goes in internal/gen.
func firstDiff(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(as), len(bs)) {
		x, y := "<eof>", "<eof>"
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return fmt.Sprintf("  line %d committed: %s\n  line %d extracted: %s", i+1, x, i+1, y)
		}
	}
	return ""
}

// TestEmitRefusesAnEmptyTable is the vacuity law at the *emitter*, which is a different place
// from the extractor's floors and fails differently.
//
// Extract's floors catch an extraction that found too little. They cannot catch a caller that
// hands Emit a table it constructed some other way — and what such a call renders is not an
// error but an *empty map*, which compiles, formats, and reads as "no mnemonic encodes to
// anything". A drift check comparing that to a committed empty table agrees perfectly. So the
// emitter asserts its own input, and this test is the assertion's falsification.
func TestEmitRefusesAnEmptyTable(t *testing.T) {
	if _, err := (&Table{SourceSHA: "test"}).Emit(); err == nil {
		t.Fatal("Emit accepted a table with no rows; an empty map renders as a valid file that " +
			"says every mnemonic is unencodable, and a drift check against an empty committed " +
			"table would agree with it")
	}
}

// TestEmitRejectsAnOriginlessRow pins the other half of Emit's input check.
//
// A row whose Origin is neither authority cannot be counted into either partition, so it
// would silently be *absent from the header's two figures while present in the map* — a
// provenance header that undercounts its own table. Emit errors rather than defaulting the
// row into one side, because defaulting picks an authority for a row that has none, which is
// the invented-evidence failure (#36) in a generated comment.
func TestEmitRejectsAnOriginlessRow(t *testing.T) {
	tab := &Table{SourceSHA: "test", Rows: []Row{{
		Keyword: "i32.add", Kind: "BINARY", Constructor: "i32_add", Code: 0x6a,
		Origin: Origin("neither"), Line: 1,
	}}}
	if _, err := tab.Emit(); err == nil {
		t.Fatal("Emit accepted a row with an unknown origin; it would be missing from the " +
			"header's per-authority counts while present in the map")
	}
}
