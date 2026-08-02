package text

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The `token` and `annot` rules share most of their arms, and the difference between them
// is the whole subject of this file. Grave #83 is what it is for: `scanAnnotBody` carried
// `token`'s bare-`(@` error arm (lexer.mll:829), which `annot` does not have, and rejected
// `annotations.wast:1` — a **must-succeed** module — for the whole file.
//
// The controls below are the two halves that failure needs, and they are different
// mechanisms rather than two vectors:
//
//   - **The arm sets are compared** (TestAnnotRuleArmsAgreeWithTheReference), because a
//     *missing* arm is invisible to any check on the arms you copied. Reading the arms we
//     transcribed and finding them right is exactly what happened for three PRs. The
//     comparison is over the `"(@"` arms of both rules, derived from the reference.
//   - **The behavioural difference is asserted at the boundary**
//     (TestBareAnnotStartIsAnErrorOnlyAtTopLevel), because the arm-set check needs the
//     vendored reference and cannot run in `make check`. Same split match_test.go
//     documents: a drift check and a language check are not the same question.
//
// Both are needed. Either alone passed the broken code — the second because we never wrote
// the nested case, the first because it did not exist.

// annotArmRE matches an `"(@"` arm's head in lexer.mll, capturing what follows the literal:
// `(id as n)`, `(string as s)`, or nothing at all. The last is the arm at issue.
var annotArmRE = regexp.MustCompile(`(?m)^\s*\|\s*"\(@"(.*)$`)

// TestAnnotRuleArmsAgreeWithTheReference pins the `"(@"` arm *set* of each rule against
// lexer.mll, and it is the control grave #83 did not have.
//
// Scoped to the rules rather than to the three arms today's code implements, because a
// control scoped to the current sample inherits the current blind spot: the defect was an
// arm we had and the reference did not, so a check enumerating our arms agrees with itself.
// The assertion is on the count and the shapes *per rule*, so either rule gaining or losing
// an arm upstream fails here rather than silently.
func TestAnnotRuleArmsAgreeWithTheReference(t *testing.T) {
	src := testenv.RequireSpecRef(t, refPath(testenv.RefLexerMLL))

	tokenRule, annotRule := splitAtAnnotRule(t, src)

	// Vacuity floor, per *a comparison against an empty set succeeds*. A moved rule
	// boundary or a changed indentation yields two empty arm lists that agree perfectly,
	// which is the degenerate pass this whole file exists to make impossible. The floors
	// are the counts at the pinned revision, asserted as a minimum rather than an
	// equality so an upstream addition is a fail with a readable diff, not a floor bust.
	tokenArms := annotArms(tokenRule)
	annotArmsList := annotArms(annotRule)
	if len(tokenArms) < 3 || len(annotArmsList) < 2 {
		t.Fatalf("found %d `(@` arms in `token` and %d in `annot`, want >=3 and >=2; "+
			"an empty or near-empty extraction agrees with anything, so this is a broken "+
			"reader rather than a finding (splitAtAnnotRule's boundary, or the arm regexp)",
			len(tokenArms), len(annotArmsList))
	}

	// `token` has the id form, the string form, and the bare error form (lexer.mll:821,
	// :825, :829). `annot` has the first two only (:850, :855) — no bare form.
	wantToken := []string{"id", "string", "bare"}
	wantAnnot := []string{"id", "string"}

	if got := classifyArms(tokenArms); !equalStrings(got, wantToken) {
		t.Errorf("`token` rule's `(@` arms are %v, want %v (lexer.mll:821,825,829)", got, wantToken)
	}
	got := classifyArms(annotArmsList)
	if !equalStrings(got, wantAnnot) {
		t.Errorf("`annot` rule's `(@` arms are %v, want %v (lexer.mll:850,855)\n\t"+
			"if a `bare` arm appeared here, `scanAnnotBody` may now reject `(@)` inside a "+
			"body — which is what grave #83 was; if one vanished from `token`, the "+
			"top-level check is now over-accepting", got, wantAnnot)
	}

	// The asymmetry stated as itself, because it is the fact the engine depends on and
	// the two assertions above could both hold under a rewrite that moved the arm.
	if len(tokenArms) == len(annotArmsList) {
		t.Errorf("both rules have %d `(@` arms; the engine's `annot` scanner is written "+
			"around `token` having a bare-`(@` error arm that `annot` lacks, and if that "+
			"stopped being true, scanAnnotBody's fallthrough to `| \"(\"` is wrong",
			len(tokenArms))
	}
}

// splitAtAnnotRule returns the text of the `token` rule and of the `annot` rule.
//
// Bounded at the next rule head in both directions, for letBody's reason: an unbounded
// search finds the fact in the *following* rule, and the arms being compared here are
// near-identical between the two rules, so an unbounded reader finds `annot`'s arms while
// claiming to read `token`'s and the comparison becomes a tautology.
func splitAtAnnotRule(tb testing.TB, src string) (tokenRule, annotRule string) {
	tb.Helper()

	// The rule heads, in the reference's order: `rule token = parse`, then the `and <name>
	// = parse` continuations. `annot` is one of those and `token` is the first.
	const tokenHead, annotHead = "rule token = parse", "and annot start = parse"

	ti, ai := strings.Index(src, tokenHead), strings.Index(src, annotHead)
	if ti < 0 || ai < 0 {
		tb.Fatalf("could not locate the rule heads in lexer.mll (token at %d, annot at %d); "+
			"both are cited by scanAnnotBody and emitAnnot, and a citation that no longer "+
			"resolves is the drift this test exists to catch", ti, ai)
		return "", ""
	}
	if ai < ti {
		tb.Fatalf("`annot` precedes `token` in lexer.mll; the bounds below assume otherwise")
		return "", ""
	}

	// `token` ends at the next `and <x> = parse`, whichever it is — not at `annot`, which
	// is several rules later and would swallow the intervening ones.
	rest := src[ti+len(tokenHead):]
	if e := regexp.MustCompile(`(?m)^and \w+ .*= parse`).FindStringIndex(rest); e != nil {
		tokenRule = rest[:e[0]]
	} else {
		tb.Fatalf("no rule follows `token` in lexer.mll; the bound is unbounded, so " +
			"`token`'s arm list would include every later rule's")
		return "", ""
	}

	rest = src[ai+len(annotHead):]
	if e := regexp.MustCompile(`(?m)^and \w+ .*= parse`).FindStringIndex(rest); e != nil {
		annotRule = rest[:e[0]]
	} else {
		annotRule = rest // `annot` may be the last rule in the file
	}
	return tokenRule, annotRule
}

// annotArms returns the text following `| "(@"` for each such arm in one rule's body.
func annotArms(rule string) []string {
	m := annotArmRE.FindAllStringSubmatch(rule, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, strings.TrimSpace(g[1]))
	}
	return out
}

// classifyArms names each arm by the form it matches: the identifier form, the string
// form, or the bare form that matches `"(@"` and nothing else.
//
// A classifier rather than a string comparison because the reference writes the binder
// names (`as n`, `as s`) and a whitespace or rename change upstream is not drift in the
// arm *set*. What is being compared is which forms exist, which is the fact #83 turned on.
func classifyArms(arms []string) []string {
	out := make([]string, 0, len(arms))
	for _, a := range arms {
		switch {
		case strings.HasPrefix(a, "(id"):
			out = append(out, "id")
		case strings.HasPrefix(a, "(string"):
			out = append(out, "string")
		case a == "" || strings.HasPrefix(a, "{"):
			out = append(out, "bare")
		default:
			out = append(out, "unknown:"+a)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBareAnnotStartIsAnErrorOnlyAtTopLevel is the behavioural half, and it is the vector
// grave #83 failed.
//
// `(@)` at the top level is `empty annotation id` (lexer.mll:829, annotations.wast:72).
// The *same three bytes* nested in an annotation body lex clean: `annot` has no such arm,
// so the `(` takes `| "("` (:846) and the `@` becomes a `reserved` atom (:882).
//
// **When two positions disagree about the same bytes, the suite has handed you a
// bidirectional control** — one wrong arm then fails the two halves in opposite directions,
// where either half alone reads as a plausible arm set. That is what makes this a pair
// rather than two tests: the broken code passed the reject half.
func TestBareAnnotStartIsAnErrorOnlyAtTopLevel(t *testing.T) {
	// The reject half is top-level and cited per row; the accept half is nested and its
	// rows are `derived from annotations.wast:1,16` — :16 holds every one of these forms
	// and :1 is the `(module` the suite expects to read, so the forms' legality *inside a
	// body* is entailed jointly by the two lines rather than stated by either.
	for _, v := range []lexVector{
		{"top level (@)", "(@)", "empty annotation id"},     // annotations.wast:72
		{"top level (@ )", "(@ )", "empty annotation id"},   // annotations.wast:73
		{"top level (@ x)", "(@ x)", "empty annotation id"}, // annotations.wast:74

		{"nested (@)", "(@a (@))", ""},                                         // derived from annotations.wast:1,16
		{"nested (@ x)", "(@a (@ x))", ""},                                     // derived from annotations.wast:1,16
		{"nested deep", "(@a (@(@(@(@)))))", ""},                               // derived from annotations.wast:1,16
		{"the whole line", "(@a @ @x (@x) (@x y) (@) (@ x) (@(@(@(@)))))", ""}, // annotations.wast:16
	} {
		t.Run(v.name, func(t *testing.T) {
			_, err := LexAll([]byte(v.src))
			if v.want == "" {
				if err != nil {
					t.Fatalf("must lex clean, got %v\n\t"+
						"`annot` has no bare-`(@` arm (lexer.mll:850,855 are its only two); "+
						"inside a body these bytes are `| \"(\"` plus a `reserved` atom, and "+
						"rejecting them fails annotations.wast:1 for the whole file — grave #83", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("lexed clean, want %q — lexer.mll:829 is `token`'s third `(@` arm "+
					"and losing it over-accepts at top level", v.want)
			}
			if !strings.Contains(err.Error(), v.want) {
				t.Fatalf("got %q, want %q", err, v.want)
			}
		})
	}
}

// TestNestedAnnotStringIDRunsAnnotID pins the second half of grave #83: the nested
// `"(@"(string)` arm calls `annot_id` (lexer.mll:858), exactly as the token-level arm does
// (:828), and ours matched the shape while validating nothing.
//
// **No vector can score this.** Every string-id vector in `annotations.wast` (:76–:79) is
// top-level, so `(@a (@""))` is a position the oracle never asks about — which is why the
// defect sat behind a green board and why the warrant here is agreement with the reference
// rather than a citation. The one direction the suite *does* fix is the top-level spelling,
// asserted alongside so the pair cannot drift into being two different rules.
func TestNestedAnnotStringIDRunsAnnotID(t *testing.T) {
	// The nested rows are `derived from annotations.wast:76,79`: the premise is that
	// `annot`'s string arm calls the *same* `annot_id` the token arm calls (lexer.mll:858
	// vs :828), so one nesting level down the two messages are entailed rather than
	// sampled. The top-level rows are the premises themselves, asserted alongside so the
	// pair cannot drift into being two different rules.
	for _, v := range []lexVector{
		{"top level empty", `(@"")`, "empty annotation id"},      // annotations.wast:76
		{"top level bad utf8", "(@\"\\ef\")", "malformed UTF-8"}, // annotations.wast:79

		{"nested empty", `(@a (@""))`, "empty annotation id"},      // derived from annotations.wast:76,79
		{"nested bad utf8", "(@a (@\"\\ef\"))", "malformed UTF-8"}, // derived from annotations.wast:76,79
		{"nested legal", `(@a (@"ok" x y))`, ""},                   // derived from annotations.wast:1,6
	} {
		t.Run(v.name, func(t *testing.T) {
			_, err := LexAll([]byte(v.src))
			if v.want == "" {
				if err != nil {
					t.Fatalf("must lex clean, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("lexed clean, want %q — matching `(@\"...\"` is not validating the "+
					"id, and the nested arm calls annot_id too (lexer.mll:858)", v.want)
			}
			if !strings.Contains(err.Error(), v.want) {
				t.Fatalf("got %q, want %q", err, v.want)
			}
		})
	}
}

// TestAnnotationsWastFirstModuleReads reads the actual vector, end to end, through the
// actual entry point.
//
// The board scores this file and would have caught the regression — it is *why* the count
// is 4162 — but the board's message is `(module <wat body>) must read` with no indication
// of which construct, and diagnosing it took reading `annot` arm by arm. This test names
// the file and the mechanism so the next failure arrives pre-diagnosed. It reads the module
// from the vendored suite rather than transcribing it: *prefer deriving corpora from the
// suite at run time — no transcription step, no drift*.
func TestAnnotationsWastFirstModuleReads(t *testing.T) {
	src := testenv.RequireSuiteFile(t, filepath.Join("..", "..", "testdata", "spec", "annotations.wast"))

	// The first module is the file's leading `(module ... )` form, ending at the first
	// line that is exactly `)`. Bounded rather than searched, for splitAtAnnotRule's
	// reason.
	const head = "(module\n"
	i := strings.Index(string(src), head)
	if i != 0 {
		t.Fatalf("annotations.wast does not begin with a `(module` form (found at %d); "+
			"this test's premise is that the file's first vector is the annotation soup "+
			"module at :1, and upstream has moved it", i)
	}
	end := strings.Index(string(src), "\n)\n")
	if end < 0 {
		t.Fatal("could not find the first module's closing paren")
	}
	mod := string(src[:end+3])

	// Vacuity floor: the module is 20 lines holding 29 `(@` forms at the pinned revision.
	// A bound that collapsed to `(module\n)` reads clean and asserts nothing. The floor is
	// a plausible size rather than the exact 29, so an upstream addition to the vector is
	// not a false failure — but it is high enough that any truncation of the bound is one.
	if n := strings.Count(mod, "(@"); n < 25 {
		t.Fatalf("extracted module holds only %d `(@` forms, want >=25 — the bound "+
			"collapsed, and a two-line module passes this test without testing anything:\n%s",
			n, mod)
	}

	if err := ReadModule([]byte(mod)); err != nil {
		t.Fatalf("annotations.wast:1 must read, got %v\n\t"+
			"this is the board's `(module <wat body>) must read` bucket for this file; the "+
			"construct is the annotation grammar, and grave #83 was an arm `scanAnnotBody` "+
			"had that the reference's `annot` rule does not", err)
	}
}
