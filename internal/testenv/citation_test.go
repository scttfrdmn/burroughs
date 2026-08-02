package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A cited test name is checkable, and this is what checks it (#93; the question was raised
// in #91 and ruled on by Scott — *yes, if there is strong evidence the control is effective*).
//
// # Why this exists, and the evidence that it works
//
// `TestFixtureProvenance` machine-checks `<file>.wast:N` citations because *a citation nobody
// verifies is a claim, not a citation*. A comment saying "pinned by <some test>" is the same
// kind of claim one class over, and nothing was checking it: #88's hand sweep found **five**
// stale ones, and the worst of them — `gatemap.go` citing a classification test that has
// never existed — documented as present the single direction nothing asserted. That is #91,
// and it existed *because* the missing control was cited as covering it.
//
// Effectiveness was measured before this was written, against `e4bfd62^` — the tree
// immediately before that hand sweep, where all five were live — rather than against the
// repaired tree:
//
//	tree          real defects   caught   missed   false positives
//	e4bfd62^      5              5        0        1 (a placeholder, excluded by shape)
//	65eb591       0              —        —        1
//
// **Recall is measured against history, not against the current tree**, and that is not a
// stylistic preference: the first draft of the exemption below passed perfectly on current
// `main` while *excusing two of the five real defects* on the pre-sweep tree. A control with
// nothing left to catch cannot distinguish a working exemption from a leaking one. Same shape
// as *a stateful instrument measures history until its state is controlled* — here the state
// is the repo's own repairs.
//
// # The three trigger defects measurement turned up, each an existing rule
//
//  1. **Hyphenated line-wraps fabricate findings.** A wrapped CamelCase name is one name that
//     ordinary comment wrapping split in two, and a line-oriented trigger reports two
//     nonexistent ones. This is #80's recurrence class seen from the other face: there a split
//     row went unread, here a split name is invented. Hence comment *blocks*, with the wrap
//     rejoined — see wrapJoin, which carries the concrete example.
//  2. **The exemption is per-sentence, because a block-scoped one leaked.** See exemptedBy.
//  3. **The one false positive is excluded by declaration shape, not by ignoring backticks.**
//     `internal/testenv`'s own fuzz-gating comment writes out a `func` signature with a
//     metasyntactic name, which is a shape sample rather than a reference. Stripping code
//     spans would have killed it — and blinded this control to **seven real citations that
//     live inside backticks** (`TestFixtureProvenance`,
//     `TestLabelStackIsBalancedOnEveryExitPath`, five more). Killing one false positive by
//     discarding a whole citation style is the overfitting failure (§9 G-3) pointed at a
//     control: *prefer the discriminator that names the actual difference*, which is that a
//     `func Name(` span is a code sample rather than a reference.
//
// # This file is inside its own population, and that is a finding, not an inconvenience
//
// The first run reported **eight** findings, every one in this file: prose *about* stale
// citations quotes the stale names, and a control cannot tell an illustration from a claim.
// Two ways out, and the choice matters. Widening the trigger — exempting "prose about the
// class", or this file — buys a green by making the control blind to a population it correctly
// reads, which is the overfitting failure one level up and would have exempted the one file
// most likely to discuss a name that has gone stale.
//
// So the *prose* changed instead: names that are no longer current are either described rather
// than named, or phrased in the vocabulary that already marks them historical. A control that
// forces its own documentation to be precise about which names exist is working. The rule
// generalises — **when a control fires on its own explanation, fix the explanation** — and it
// is the reason the paragraphs above now say "a classification test that has never existed"
// where a draft named it.
//
// # Scope
//
// The sibling class — `#NN` issue citations, from PR #84 — is deliberately **not** here. Its
// oracle is the GitHub API, so it cannot be a `go test`, and *split issues at the oracle
// seam*: this half's oracle is local, so this half ships now.

// citedName matches a test/fuzz/benchmark identifier as it appears in prose.
//
// Fuzz and Benchmark are in the same class as Test on purpose. The population is "things this
// repo cites as the control that pins a claim", and a benchmark or a fuzz target is cited that
// way as readily as a test — *derive the domain, never enumerate it*. Restricting to `Test`
// would inherit the current sample's shape, which is the blind spot #33 was widened for.
var citedName = regexp.MustCompile(`\b(?:Test|Fuzz|Benchmark)[A-Z][A-Za-z0-9_]*`)

// declSpan matches a Go declaration written out inside a code span — a `func Name(args)` sample.
//
// A declaration is a *sample of a shape*, not a reference to a function, so the name inside it
// need not resolve. This is the whole exclusion, and it is deliberately narrow: it requires
// `func`, the name, and an open paren, so a backticked bare citation stays visible.
var declSpan = regexp.MustCompile("`[^`]*\\bfunc\\s+(?:Test|Fuzz|Benchmark)[A-Z][A-Za-z0-9_]*\\s*\\(")

// pastReference is the vocabulary that marks a name as *historical* — a comment recording what
// a citation used to say, which is testimony worth keeping and not a claim about a test that
// exists now.
//
// #91 predicted this exemption would be needed ("the historical-reference case needs an
// exemption") and the measurement says it is needed for exactly five names today, all of them
// records of the #88 sweep's own repairs.
//
// **Every phrase here asserts the name is not current.** That is the licensing rule, and it is
// what keeps the exemption from becoming the laundering channel the derived-provenance
// category was careful not to become: "it was …", "previously cited …", "which never existed"
// all *say* the name is stale, so a reader is not misled and neither is this control. A vague
// marker — a bare "now", a bare "renamed" — would excuse live citations, which is precisely
// what happened (see exemptedBy).
var pastReference = regexp.MustCompile(`(?i)(it (said|was)\b|previously (cited|said)|began as|pre-rename|(has )?never existed|which never|used to (say|cite)|formerly|retired\s+(Test|Fuzz|Benchmark)|no longer (exists|named))`)

// sentenceSplit breaks a comment block into sentences: full stops, em-dash asides, semicolons.
var sentenceSplit = regexp.MustCompile(`(?:[.!?]\s+)|(?:\s+—\s+)|(?:;\s+)`)

// wrapJoin rejoins a name split across a comment line break. The real instances in this tree
// are in `internal/spec/spec_test.go:2031` and `internal/spec/wast.go:911`, where a board-bound
// control's name and an unsupported-bucketing control's name each wrap mid-identifier; they are
// cited by location rather than quoted here, because quoting a truncated identifier manufactures
// exactly the finding this variable exists to suppress. (It did, on the first run.)
//
// The capture is the continuation's first letter, restored by the `$1` in the replacement — Go's
// regexp is RE2 and has no lookahead, so the letter must be consumed and put back rather than
// merely peeked at.
//
// Narrow on purpose: it fires only on `-` immediately before a line break and an uppercase
// letter, which is what a wrapped CamelCase identifier looks like and what ordinary prose
// (`gate-off`, `at-terminal`, an em-dash aside) does not.
var wrapJoin = regexp.MustCompile(`-\n\s*([A-Z])`)

// exemptedBy reports whether the sentence containing a citation marks it as historical.
//
// **Per-sentence, and the block-scoped version is why.** The first draft matched the marker
// anywhere in the enclosing comment block, which passes flawlessly on current `main` and, run
// against `e4bfd62^`, **excused two of the five real defects** — `sexpr_test.go:145`'s "is
// asserted directly in …" and `spec_test.go:1040`'s "allowlisted in …", both live present-tense
// claims about tests that did not exist, both sitting in long comments that happened to contain
// a past-tense word somewhere else. The names are elided and the sites given instead, for
// wrapJoin's reason: naming them here would re-create the finding.
//
// The claim lives in the sentence, so the exemption must too. Generalises: **an exemption
// scoped more widely than the claim it excuses will excuse claims it never examined**, which
// is *a precondition that excuses a gate is licensed at one place* applied to the granularity
// of the license rather than to its location.
func exemptedBy(sentence string) bool {
	return pastReference.MatchString(sentence)
}

// citation is one occurrence of a cited name.
type citation struct {
	name, pos, sentence string
}

// TestEveryCitedTestNameResolves requires every test/fuzz/benchmark name cited in a comment to
// name a function that exists, unless the sentence marks it as historical.
func TestEveryCitedTestNameResolves(t *testing.T) {
	defined, cites := citationInventory(t)

	// Vacuity, and it is load-bearing twice over: a walk that finds no definitions calls every
	// citation dangling, and a walk that finds no citations agrees with any set of definitions.
	// Both are what a moved directory or a changed comment style produces — the empty-set
	// agreement (#82), and the exact shape by which #78's guard vouched for a file it read past.
	// Floors are stamped from the measurement (276 defined, 257 distinct cited) and asserted as
	// minima so growth is covered rather than ignored.
	const (
		definedFloor = 200
		citedFloor   = 180
	)
	if len(defined) < definedFloor {
		t.Fatalf("found %d defined test/fuzz/benchmark functions; 276 at 65eb591. A population "+
			"this small means the walk stopped reaching the tree, and every citation would be "+
			"reported dangling", len(defined))
	}
	distinct := map[string]bool{}
	for _, c := range cites {
		distinct[c.name] = true
	}
	if len(distinct) < citedFloor {
		t.Fatalf("found %d distinct cited names; 257 at 65eb591. A comparison against almost no "+
			"citations agrees with almost anything — *coverage is to a trigger what a vacuity "+
			"check is to a comparison* (#82)", len(distinct))
	}

	findings := 0
	for _, c := range cites {
		if defined[c.name] {
			continue
		}
		if exemptedBy(c.sentence) {
			continue // a record of what a citation used to say; see pastReference
		}
		findings++
		t.Errorf("%s cites %s, which no test/fuzz/benchmark function defines.\n\t"+
			"in: %q\n\t"+
			"Either re-point it at the control that does assert this, or — if the control does "+
			"not exist — the citation is documenting a direction nothing checks, which is how "+
			"#91 stayed open. If the sentence is recording what the citation *used to* say, "+
			"phrase it so (\"it was X\", \"previously cited X\").",
			c.pos, c.name, c.sentence)
	}
	if findings == 0 {
		t.Logf("%d citations of %d distinct names, all resolving against %d defined functions",
			len(cites), len(distinct), len(defined))
	}
}

// citationInventory walks the tree once, returning the defined names and every citation.
//
// Comments come from the parser's comment map rather than from raw text: a name inside a string
// literal is not a citation, and reading bytes would not know the difference.
func citationInventory(tb testing.TB) (map[string]bool, []citation) {
	tb.Helper()

	root := "../.."
	defined := map[string]bool{}
	var cites []citation

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Same exclusions as TestEverySkipSiteIsLicensed, deliberately: the two controls
			// walk one tree, and a divergence would mean they disagree about what "the tree"
			// is. third_party is vendored upstream material we do not author.
			switch d.Name() {
			case "testdata", "bin", ".git", "tools", "third_party":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			// A definition is a top-level func whose *whole* name matches the citation
			// shape — not merely contains it, or a helper whose name embeds a test's name
			// as a substring would silently satisfy a citation of that test.
			if citedName.FindString(fn.Name.Name) == fn.Name.Name {
				defined[fn.Name.Name] = true
			}
		}

		for _, group := range f.Comments {
			text := wrapJoin.ReplaceAllString(group.Text(), "$1")
			pos := fset.Position(group.Pos()).String()
			for _, sentence := range sentenceSplit.Split(text, -1) {
				// A declaration written out as a code sample is not a reference. Blanked
				// rather than skipped, so the rest of the sentence is still read: a sentence
				// can contain both a shape sample and a real citation.
				scan := declSpan.ReplaceAllString(sentence, "`sample(")
				for _, name := range citedName.FindAllString(scan, -1) {
					cites = append(cites, citation{name, pos, strings.TrimSpace(sentence)})
				}
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatalf("walking the tree: %v", err)
	}
	return defined, cites
}
