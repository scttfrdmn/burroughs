package binary

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// This file is grave #264's tripwire, and only that. The grave is *A control that names the
// sentinel it expects cannot notice the sentinel is undeclared* — both of its instances were
// error sentinels missing from declaredErrors (fuzz_test.go), one found by the fuzzer in 41
// seconds and one found by a sweep and **not findable by the fuzzer at all**, its three call
// sites sitting behind gates the default decoder declines before reaching.
//
// The second instance is why this file exists rather than staying filed. Its own closing text
// says the omission "would have surfaced AT A GATE FLIP as an unexplained red" — a correct
// verdict on a malformed module, scored as a regression, in the PR least able to absorb one.
// The relaxed-SIMD flip is a gate flip, so the tripwire is discharged **before** it rather
// than after, in the direction #264 predicted.
//
// Two obligations the grave named, both structural here:
//
//   - **Scope to the space, not to today's sentinels.** The domain is read out of the
//     package's own source by AST, so it grows when a sentinel is added without this file
//     being touched. immVocabulary (instr_width_test.go) is the shape being copied, and its
//     comment carries the argument: a hand-written list is a sample of the vocabulary as of
//     authorship.
//   - **Vacuity check.** A walk that matches nothing produces two empty sets that agree
//     perfectly (#29). Floors per side, counts rather than non-nil checks, because "found
//     some" is what an under-matching pattern reports too.

// errSentinelPattern is the recognizer for a package-level error sentinel's *name*: Err*
// exported, err* unexported. Written down as its own name because it is a **claim about the
// space** — a sentinel whose name does not match it is invisible to everything below, so the
// pattern is the control's own blind spot and belongs where a reader will see it.
//
// The two compiled forms below differ only in what they anchor to, and both are built from
// this one string on purpose. The first draft wrote the textual form as a separate literal
// matching `Err[A-Z]…` only, which made the text search a **subset** of the AST domain for
// the two unexported sentinels while a comment two functions down asserted it was a superset
// "by construction" — and the floor built on that claim then passed 41 > 40 by coincidence.
// A shared source of truth is what stops the two spellings of one space from drifting.
const errSentinelPattern = `[Ee]rr[A-Z][A-Za-z0-9]*`

var (
	errSentinelName = regexp.MustCompile(`^` + errSentinelPattern + `$`)
	errSentinelText = regexp.MustCompile(`\b` + errSentinelPattern + `\b`)
)

// excludedFromDeclaredErrors holds the sentinels that are deliberately **not** in
// declaredErrors, each with the reason it is out.
//
// The discriminator is not reachability — ErrZeroByteExpected and ErrMisplacedOpcode are both
// declared while unreachable — it is #264's positive statement of it: whether surfacing the
// error would be *a verdict about the module* or *a bug in this package*. A spec verdict
// carrying the reference's own message text is declared whether it is reachable today or not;
// an engine failing to read a field it was told about is a fuzz find and must stay one.
//
// Each value points at the full reason rather than restating it, because both reasons are
// already written at the declarations and a second copy is a second authority that can drift.
// This is the deadcode-allowlist discipline (decision 0005) pointed at errors: an entry
// without a reason is a suppression wearing a disguise.
var excludedFromDeclaredErrors = map[string]string{
	"errNoImmReader":       "the immediate switch's default arm — a missing reader is a bug in instr.go, not a fact about the module; keeping it undeclared is what makes reaching it a fuzz find (reason at instr.go, on the declaration)",
	"errNotEmptyBlockType": "the blocktype alternation's middle branch declining, whose reference text is the empty string (decode.ml:337) because `either` overwrites it; undeclared and unreachable by construction (reason at instr.go, on the declaration, #88)",
}

// sentinel is one package-level error declaration, as read from source.
type sentinel struct {
	name string
	pos  string // file:line, so a failure names the declaration rather than the set
	// shape is the initializer's shape as text — "errors.New" for the package's uniform
	// idiom, anything else verbatim. Recorded rather than filtered on, so an initializer
	// this control does not understand is *reported* instead of silently dropped.
	shape string
}

// packageSentinels reads every package-level error sentinel out of the non-test sources.
//
// Derived, never enumerated, and derived by **AST rather than by grep**, which is not a
// stylistic preference: a text search over this package finds ErrMalformedFuncType,
// ErrMalformedValType and ErrNonConstantExpr — three sentinels that no longer exist and are
// named only in comments recording their retirement — and would report all three as
// unenrolled. The regex measures text; the AST measures declarations.
func packageSentinels(t *testing.T) map[string]sentinel {
	t.Helper()

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	found := map[string]sentinel{}
	files := 0
	fset := token.NewFileSet()
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err) // a package that cannot parse itself
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if !errSentinelName.MatchString(id.Name) {
						continue
					}
					shape := "<no initializer>"
					if i < len(vs.Values) {
						shape = initializerShape(vs.Values[i])
					}
					found[id.Name] = sentinel{
						name:  id.Name,
						pos:   fmt.Sprintf("%s:%d", name, fset.Position(id.Pos()).Line),
						shape: shape,
					}
				}
			}
		}
	}
	// Vacuity, on the mechanism rather than on its result: a wrong working directory, a
	// moved package, or a filter that excludes everything all produce an empty domain,
	// and an empty domain makes both directions below agree.
	if files < 5 {
		t.Fatalf("walked %d non-test source files, want ≥5: the package has more than that, "+
			"so a walk this short lost the domain rather than found it small", files)
	}
	return found
}

// initializerShape renders an initializer's *shape* — the callee for a call, the node type
// otherwise — without evaluating it. Only the shape matters: the control needs to know
// whether it is looking at the idiom it understands.
func initializerShape(v ast.Expr) string {
	call, ok := v.(*ast.CallExpr)
	if !ok {
		return fmt.Sprintf("%T", v)
	}
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	default:
		return fmt.Sprintf("call of %T", call.Fun)
	}
}

// enrolledSentinelNames reads the identifiers out of declaredErrors' composite literal.
//
// Read from source rather than from the slice itself because a []error at run time has
// values and no names: errors.Is can say *whether* a sentinel is in the set and never
// *which* name is missing, and a control that cannot name the missing entry is a control
// whose failure message asks the reader to go find it. The runtime length is then checked
// against this count below — the AST and the value must be talking about the same set, and
// that identity is the only thing keeping a blind walk from reading as a clean green.
func enrolledSentinelNames(t *testing.T) map[string]bool {
	t.Helper()
	return registryNames(t, "declaredErrors")
}

// registryNames reads the identifiers out of one named registry's composite literal.
//
// Parameterized on the variable name because there are **two** registries and the space is
// (sentinel × registry), which this file's header has said since #264 while its reader could
// only see one of them. `constExprErrors` was an anonymous literal inside the fuzz body until
// grave #531 — unaddressable by name, so the half of the space it holds was uncovered by
// construction, and the fourth instance of the class arrived there.
func registryNames(t *testing.T, varName string) map[string]bool {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fuzz_test.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing fuzz_test.go: %v", err)
	}
	names := map[string]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != varName {
				continue
			}
			for _, val := range vs.Values {
				lit, ok := val.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, elt := range lit.Elts {
					id, ok := elt.(*ast.Ident)
					if !ok {
						// An entry that is not a bare identifier — a call, a
						// selector from another package — is outside what this
						// reader understands, and the length identity below is
						// what turns that into a failure instead of a silent
						// undercount.
						continue
					}
					names[id.Name] = true
				}
			}
		}
	}
	return names
}

// TestEverySentinelIsDeclaredOrExcluded is the tripwire itself: the (sentinel × registry)
// relation, both directions, over a domain derived from the source.
//
// The two directions catch different things and neither substitutes for the other. Forwards:
// a sentinel exists and no registry mentions it — the #264 omission, whose two arrival
// stories are a 41-second fuzz reproducer or an unexplained red at a gate flip. Backwards: a
// registry names something the package no longer declares — a stale entry, which is a claim
// about nobody, and the failure mode immVocabulary's comment already describes.
//
// There is deliberately **no exact count pinned here.** An equality on the domain size would
// fire on exactly the event the set relation already fires on — a new sentinel — and that is
// one concept with two triggers (#82). The relation carries the exactness: a sentinel added
// and not enrolled fails the forward direction, and a sentinel that silently drops out of the
// walk while staying in declaredErrors fails the backward one. The floors below are for
// vacuity, which is a different failure.
func TestEverySentinelIsDeclaredOrExcluded(t *testing.T) {
	domain := packageSentinels(t)
	enrolled := enrolledSentinelNames(t)

	// Vacuity floors, per side. Set well under the counts at authorship (38 sentinels
	// declared, 38 enrolled, 2 excluded) so that adding or retiring one is not a failure
	// here — the relation is what notices those. What these catch is the domain or the
	// registry collapsing, which is what a moved file or a renamed variable produces.
	if len(domain) < 30 {
		t.Fatalf("found %d sentinels in the package source, want ≥30: this package declares "+
			"dozens, so a domain this small is the walk failing rather than the package shrinking", len(domain))
	}
	if len(enrolled) < 30 {
		t.Fatalf("read %d identifiers out of declaredErrors, want ≥30: an empty or tiny "+
			"registry agrees with an empty domain, which is the vacuity this floor exists for", len(enrolled))
	}
	if len(excludedFromDeclaredErrors) < 2 {
		t.Fatalf("the exclusion set holds %d entries, want ≥2: errNoImmReader and "+
			"errNotEmptyBlockType are both deliberately undeclared (#88), so a shorter set "+
			"means an exclusion was dropped and its sentinel now reads as unenrolled", len(excludedFromDeclaredErrors))
	}

	// The AST and the value must describe the same set. Without this, a reader that
	// silently skipped entries would shrink the registry and the forward direction below
	// would report *the walk's* omissions as the package's — a control blaming the code
	// for its own blindness.
	if len(enrolled) != len(declaredErrors) {
		t.Errorf("read %d identifiers out of declaredErrors' literal but the slice holds %d "+
			"values: the two must be the same set, so either an entry is not a bare "+
			"identifier (this reader skips those) or a name is listed twice", len(enrolled), len(declaredErrors))
	}

	// Forwards: every sentinel is in exactly one registry.
	for name, s := range domain {
		_, isEnrolled := enrolled[name]
		_, isExcluded := excludedFromDeclaredErrors[name]
		switch {
		case isEnrolled && isExcluded:
			t.Errorf("%s (%s) is both in declaredErrors and in the exclusion set: the two "+
				"registries answer the same question and must not disagree", name, s.pos)
		case !isEnrolled && !isExcluded:
			t.Errorf("%s (%s) is in neither declaredErrors nor the exclusion set — grave "+
				"#264. Add it to declaredErrors if surfacing it would be a verdict about "+
				"the module, or to excludedFromDeclaredErrors with its reason if reaching "+
				"it would be a bug in this package. Left in neither it arrives as a fuzz "+
				"find with no defect behind it, or as an unexplained red at a gate flip",
				name, s.pos)
		}
	}

	// Backwards, both registries. A name in a registry that the package does not declare
	// is a claim about nobody.
	for name := range enrolled {
		if _, ok := domain[name]; !ok {
			t.Errorf("declaredErrors names %s, which is not a package-level error sentinel "+
				"in the non-test sources: either it was retired (drop the entry) or this "+
				"control's recognizer no longer matches its declaration", name)
		}
	}
	for name, reason := range excludedFromDeclaredErrors {
		if _, ok := domain[name]; !ok {
			t.Errorf("the exclusion set names %s (%q), which the package does not declare: "+
				"a stale exclusion is a suppression with no subject", name, reason)
		}
	}

	// The recognizer's own blind spot, made loud. Every sentinel in this package is
	// `errors.New` at authorship — 38 of 38 exported and 2 of 2 unexported — so a
	// different initializer is either a new idiom worth a decision or a shape this walk
	// misreads. Reported rather than filtered, because a filter here would drop the
	// sentinel out of the domain entirely and the forward direction would go quiet.
	for name, s := range domain {
		if s.shape != "errors.New" {
			t.Errorf("%s (%s) is initialized by %s rather than errors.New: this control's "+
				"domain assumes the package's one sentinel idiom, so a second idiom needs "+
				"the recognizer widened deliberately rather than a sentinel drifting out "+
				"of scope", name, s.pos, s.shape)
		}
	}

	t.Logf("%d sentinels declared, %d enrolled in declaredErrors, %d excluded with reasons",
		len(domain), len(enrolled), len(excludedFromDeclaredErrors))
}

// TestTheASTGrepGapHasAWitness populates the argument packageSentinels makes in prose.
//
// That comment claims a text search would report retired sentinels — named only in comments
// that record their retirement — as unenrolled, and that the AST does not. A claim about the
// mechanism is worth no more than a claim about the engine, so it is measured here rather
// than asserted there: the names occurring in the source *text* minus the names the AST
// declares must be non-empty, and the difference is logged so a reader sees which. Both
// searches read the same name space (errSentinelPattern), which is what makes the text side a
// genuine superset rather than a nearly-equal count that happens to compare the right way.
//
// The check is the shape of instr_width_test.go's `packed == 0` — a population check on the
// case that motivates a mechanism. If the difference ever empties, this control's regex-vs-AST
// argument has no witness in this package and the failure says so, rather than the argument
// quietly becoming decorative.
func TestTheASTGrepGapHasAWitness(t *testing.T) {
	domain := packageSentinels(t)

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	inText := map[string]bool{}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, m := range errSentinelText.FindAllString(string(src), -1) {
			inText[m] = true
		}
	}
	if len(inText) < len(domain) {
		t.Fatalf("the text search found %d names and the AST found %d declarations: the text "+
			"search is a superset by construction (every declaration is also text), so a "+
			"smaller count means this check's own scan lost files", len(inText), len(domain))
	}
	var textOnly []string
	for name := range inText {
		if _, ok := domain[name]; !ok {
			textOnly = append(textOnly, name)
		}
	}
	if len(textOnly) == 0 {
		t.Error("every Err* name in the source text is also a declaration: the retired " +
			"sentinels whose names survive only in comments (ErrMalformedFuncType, " +
			"ErrMalformedValType, ErrNonConstantExpr at authorship) are gone, so the " +
			"regex-versus-AST argument in packageSentinels' comment is unpopulated — " +
			"either restore the historical prose or drop the claim")
	}
	t.Logf("%d Err* names in the source text, %d declared, %d text-only: %v",
		len(inText), len(domain), len(textOnly), textOnly)
}

// excludedFromConstExprErrors holds the sentinels instr.go raises that are deliberately **not**
// in `constExprErrors`, each with the reason it is out.
//
// All three are raised by the function-body layer that *wraps* the instruction grammar rather
// than by the grammar itself. They live in instr.go because that is where the code section's
// body reader lives, and `constExprErr` never enters it — a constant expression is read from a
// global, element or data segment, none of which has a body size or a locals vector. So the
// narrower registry rightly omits them, and all three are enrolled in `declaredErrors`, where
// the wider claim does cover them.
//
// The discriminator is *which reader raises it*, not reachability, for the reason
// `excludedFromDeclaredErrors` states one registry over. An entry without a reason is a
// suppression wearing a disguise.
var excludedFromConstExprErrors = map[string]string{
	"ErrSectionOverrun":      "decodeFuncBody's `sized` extent check — face 1 of the size mechanism at the body level, below a section and above the grammar",
	"ErrSectionSizeMismatch": "decodeFuncBody's reconciliation of declared body size against bytes the grammar consumed, which has no analogue inside a constant expression",
	"ErrTooManyLocals":       "decodeLocals' 2^32 aggregate — a locals vector is part of a function body's preamble and no constant expression has one",
}

// grammarSentinels reads every error sentinel mentioned inside a function body in instr.go,
// keyed by name and carrying its sites so a failure names the raise rather than the set.
//
// **Why instr.go and not a derived call graph.** The narrower registry's claim is "only these may
// come out of the instruction grammar", and instr.go is the file the instruction grammar is: the
// opcode dispatch, the immediate vocabulary, and the productions those two reach. A new immediate
// reader goes there, which is what makes the file a *structural* domain rather than a list of
// today's cases. Two other candidate domains were measured and rejected:
//
//   - **Receiver `*instrCtx`** — 6 sentinels, five of them already enrolled. It is the tightest
//     honest scope and it would have caught #531, but it drops `ErrIllegalOpcode`,
//     `ErrEndExpected`, `ErrMalformedTypeIndex` and `ErrMalformedCatch`, which the grammar raises
//     from free functions and plain methods. A domain that excludes four enrolled members is not
//     measuring the claim.
//   - **Adding constexpr.go** — three more sentinels (`ErrMalformedElemSegKind`,
//     `ErrMalformedElemKind`, `ErrMalformedDataSegKind`), all raised by the segment readers that
//     *call* the grammar, so it would cost three more exclusions and catch nothing.
//
// Mentions rather than raises, deliberately: a body-wide walk also picks up an `errors.Is`
// comparison (`either`'s `ErrFeatureDisabled`), which over-covers. Over-covering costs an
// exclusion entry with a reason; under-covering costs a grave.
func grammarSentinels(t *testing.T) map[string][]string {
	t.Helper()

	const grammarFile = "instr.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, grammarFile, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", grammarFile, err)
	}
	found := map[string][]string{}
	funcs := map[string]bool{}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		funcs[fd.Name.Name] = true
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || !errSentinelName.MatchString(id.Name) {
				return true
			}
			found[id.Name] = append(found[id.Name],
				fmt.Sprintf("%s:%d/%s", grammarFile, fset.Position(id.Pos()).Line, fd.Name.Name))
			return true
		})
	}
	// Liveness on the domain rather than on its size alone. A file rename, a package split, or
	// the grammar moving out of instr.go all produce a walk that reads a real file and finds
	// nothing this registry is about — and an empty domain agrees with any registry (#29). These
	// three names are the dispatch, the immediate vocabulary, and the deferred-decline recorder:
	// the grammar is not in this file if they are not.
	for _, want := range []string{"instr", "imm", "decline"} {
		if !funcs[want] {
			t.Fatalf("%s declares no %s: the instruction grammar has moved, so this control's "+
				"domain is a file that no longer holds its subject — re-point grammarFile rather "+
				"than reading its silence as a pass", grammarFile, want)
		}
	}
	if len(found) < 10 {
		t.Fatalf("found %d sentinels mentioned in %s, want ≥10: the grammar raises more than "+
			"that, so a domain this small is the walk failing rather than the file shrinking",
			len(found), grammarFile)
	}
	return found
}

// TestEveryInstrGrammarSentinelIsInTheConstExprRegistry is grave #531's tripwire: #264's
// relation, pointed at the *second* registry.
//
// #264's control asserts that every sentinel in the package is in `declaredErrors` or excluded
// with a reason. That is one registry, and the grave's own header says the space is (sentinel ×
// registry). `ErrZeroFlagExpected` was enrolled in `declaredErrors` — so that control was
// green — omitted from `constExprErrors`, and found by `FuzzConstExprProgress` on `fe 03 30`
// while `make check` passed. Fourth instance of the class, second one found by the fuzzer.
//
// Three relations, and they fail for unrelated reasons:
//
//   - **Forwards.** Every sentinel the grammar mentions is in `constExprErrors` or excluded with
//     a reason. This is the arm that fires on #531's omission.
//   - **Backwards.** Every name in `constExprErrors` is a sentinel the package declares — a
//     stale entry is a claim about nobody.
//   - **Subset.** `constExprErrors ⊆ declaredErrors`, because the narrower claim is a claim
//     about a subset of the module grammar's errors: an entry here and not there would mean the
//     instruction grammar may return something the decoder may not. Stated as a subset and not
//     an equality on purpose — an equality would make one list derivable from the other, which
//     is the tautology `constExprErrors`' own comment declines.
func TestEveryInstrGrammarSentinelIsInTheConstExprRegistry(t *testing.T) {
	domain := packageSentinels(t)
	grammar := grammarSentinels(t)
	narrow := registryNames(t, "constExprErrors")
	wide := registryNames(t, "declaredErrors")

	// Vacuity, per side. The relation notices an added or retired sentinel; these notice a
	// registry that collapsed to nothing, which reads as agreement.
	if len(narrow) < 10 {
		t.Fatalf("read %d identifiers out of constExprErrors, want ≥10: an empty registry "+
			"agrees with any domain, which is the vacuity this floor exists for", len(narrow))
	}
	if len(narrow) != len(constExprErrors) {
		t.Errorf("read %d identifiers out of constExprErrors' literal but the slice holds %d "+
			"values: the AST and the value must describe the same set, so either an entry is "+
			"not a bare identifier (this reader skips those) or a name is listed twice",
			len(narrow), len(constExprErrors))
	}
	if len(excludedFromConstExprErrors) < 3 {
		t.Fatalf("the exclusion set holds %d entries, want ≥3: the three function-body-layer "+
			"sentinels are all deliberately out, so a shorter set means one was dropped and its "+
			"sentinel now reads as an unenrolled grammar error", len(excludedFromConstExprErrors))
	}

	for name, sites := range grammar {
		switch {
		case narrow[name] && excludedFromConstExprErrors[name] != "":
			t.Errorf("%s (%s) is both in constExprErrors and in that registry's exclusion set: "+
				"the two answer one question and must not disagree", name, sites[0])
		case narrow[name], excludedFromConstExprErrors[name] != "":
			// Enrolled or excluded with a reason: either is an answer.
		case excludedFromDeclaredErrors[name] != "":
			// Undeclared for the whole module grammar, so a fortiori not an instruction-grammar
			// verdict. errNoImmReader and errNotEmptyBlockType reach here, and routing them
			// through the wider exclusion set rather than copying their reasons is deliberate:
			// two copies of one reason are two authorities that can drift.
		default:
			t.Errorf("%s (%s) is in neither constExprErrors nor either exclusion set — grave "+
				"#531. Add it to constExprErrors if the instruction grammar may return it, or "+
				"to excludedFromConstExprErrors with the reader that raises it if it cannot. "+
				"Left in neither, FuzzConstExprProgress reports it as an undeclared error with "+
				"no defect behind it — which is how #531 arrived", name, strings.Join(sites, " "))
		}
	}

	for name := range narrow {
		if _, ok := domain[name]; !ok {
			t.Errorf("constExprErrors names %s, which is not a package-level error sentinel in "+
				"the non-test sources: either it was retired (drop the entry) or this control's "+
				"recognizer no longer matches its declaration", name)
		}
		if !wide[name] {
			t.Errorf("constExprErrors names %s and declaredErrors does not: the instruction "+
				"grammar cannot be allowed to return an error the decoder is not allowed to "+
				"return, so the narrower registry must be a subset of the wider one", name)
		}
	}
	for name, reason := range excludedFromConstExprErrors {
		if _, ok := grammar[name]; !ok {
			t.Errorf("the exclusion set names %s (%q), which no function body in instr.go "+
				"mentions: a stale exclusion is a suppression with no subject, and this one "+
				"would hide the sentinel's return if the raise came back", name, reason)
		}
	}

	t.Logf("%d sentinels mentioned in the grammar, %d enrolled in constExprErrors, %d excluded "+
		"here, %d excluded from declaredErrors", len(grammar), len(narrow),
		len(excludedFromConstExprErrors), len(excludedFromDeclaredErrors))
}
