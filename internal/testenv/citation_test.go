package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
// exemption") and the measurement said it was needed for exactly five names, all of them records
// of the #88 sweep's own repairs.
//
// **That figure was a snapshot and it has tripled: 17 names over 24 sites**, 3 of the sites
// naming the one test 0042 deleted. Re-measured with the instrument rather than re-read — a
// `t.Logf` in the exemption branch below, printing every name it excused — because the number is
// the only thing here that says how much prose this exemption is carrying, and a figure that
// grows silently is the one a reader trusts most. The exemption is a class, not a list: nothing
// enumerates the names and nothing should, so what this paragraph owes is the *size*, restated
// when the file is touched.
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

// wrapJoin rejoins a name split across a comment line break.
//
// **Both real instances are in `internal/spec/spec_test.go`** — one in the comment over the
// all-gates-on lane's pass floor, where a grave-pinning control's name wraps, and one in the
// comment over the `totalFloor` `boardBound` call, where a board-bound control's name does. They
// are cited by *what they stand over* rather than by line, because a `file:N` here has already
// drifted once (#456) and quoting the truncated identifier would manufacture exactly the finding
// this variable exists to suppress. Re-measure with the control's own preprocessing, never a raw
// grep: fed `group.Text()` it finds **2**, and run against raw bytes it finds **0**, because a
// continuation line begins with `//` and `\s*` cannot match a slash.
//
// It said `spec_test.go:2031` and `internal/spec/wast.go:911` before this repair, and the second
// half was the instructive one: `wast.go` holds no instance either way, so that citation named the
// wrong *file* rather than a drifted line — the half a line-drift sweep cannot see.
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
//
// # The marker is matched over collapsed whitespace, because a line wrap defeated it (grave #480)
//
// A sentence carries the newlines of the comment it came from, and every phrase in
// `pastReference` is two or three words — so a marker that straddles a wrap ("no\n// longer
// exists") does not match and a correctly-phrased historical citation is flagged. Found by
// writing one: `foreclose_test.go`'s ninth re-key entry records a deleted test's name, phrased
// exactly as the failure message prescribes, and was reported anyway until the wrap moved.
//
// **This is the trigger side's own wrap problem on the exemption side, where nothing looked for
// it.** `wrapJoin` above exists because a cited *identifier* can break across lines; the
// exemption was written as if its markers could not, and the two are the same fact about the
// medium. The cost of leaving it is a false positive that teaches the writer to phrase around
// the instrument, which is worse than the flag — *an instrument that shapes the prose it reads
// stops measuring it.*
func exemptedBy(sentence string) bool {
	return pastReference.MatchString(strings.Join(strings.Fields(sentence), " "))
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

// runFlagName matches a `-run <Name>` argument in a shell command — the gates' way of citing a
// test. Anchored on the flag rather than on the name's shape so that a `-run` whose argument is a
// regex alternation or a lowercase typo is captured and then fails to resolve, rather than being
// silently skipped for not looking like a test name.
var runFlagName = regexp.MustCompile(`-run ([A-Za-z_][A-Za-z0-9_]*)`)

// TestEveryGateCitedTestNameResolves is TestEveryCitedTestNameResolves' domain extended to the
// place where the same drift is silent instead of loud — and it is #322's shape on a second
// surface, found by sweeping for it.
//
// # The mechanism, measured rather than reasoned
//
// `go test -run ThisNameDoesNotExist ./internal/spec/` prints `testing: warning: no tests to
// run`, then `PASS`, then `ok … [no tests to run]`, and **exits 0**. So a gate that invokes a test
// by name reports a passing verdict about a question it never asked, the moment that name stops
// resolving. Two of those gates exist and they are the same control on both sides of the mirror:
//
//	Makefile:164        $(STRICT) $(GO) test -v -run TestAllGatesOnLeavesNothingGated ./internal/spec/
//	ci.yml (all-on job) go test -v -run TestAllGatesOnLeavesNothingGated ./internal/spec/
//
// A rename of that function — an ordinary, blameless refactor — turns both into no-ops that report
// success. Nothing in the tree noticed: the existing resolver above walks `.go` files and reads
// *comments*, so a Makefile recipe and a workflow step are outside the only instrument that checks
// this class of citation. **An instrument's domain is an assertion it cannot check about itself**,
// and this is that assertion coming due.
//
// # Why this is worse than the dangling comment the resolver above catches
//
// A stale comment misinforms a reader, who can then go and look. A stale `-run` misinforms *CI*,
// which cannot. The failure is in the direction that produces green — the whole reason #322 is
// filed about the formatting gate, and the reason `-run` deserves the same treatment: the two share
// the shape "the gate's success is indistinguishable from its non-execution", which is the
// verdict-channel/mechanism-channel confusion pointed at a gate instead of at a tool.
//
// No instance is live at the time of writing — `TestAllGatesOnLeavesNothingGated` resolves — so
// this lands as a tripwire on a prospective defect, which is the only time a tripwire can be
// written calmly.
//
// # The exemption, and why it is keyed on the mode flag rather than on `XXX`
//
// The fuzz and bench gates say `-run XXX -fuzz <target>` / `-run XXX -bench .`, where `XXX` is an
// idiom meaning *match no unit test, I only want the other mode's subject*. That is a deliberate
// non-citation and has to be excused. It is excused by the presence of a **mode flag on the same
// line**, not by the literal `XXX`: keying on the literal would also excuse a bare `-run XXX` with
// no mode flag after it, which is a gate that runs nothing at all and is precisely the defect this
// test exists for. A guard's trigger predicate is itself a claim about the space, and "the argument
// is XXX" is a claim about spelling where "this command selects a fuzz or bench subject" is a claim
// about meaning.
//
// **The first draft named only `-fuzz`, and the six fuzz gates it was written against all passed** —
// the bench gate at Makefile:280 is the same idiom with the other mode flag, and it was reported as
// a dangling citation of a test named `XXX`. An under-matching trigger predicate, caught in the
// direction that is merely noisy; the same omission on the *other* side of an exemption is the one
// that fails silently, which is why it is recorded here rather than quietly widened.
//
// # Comment lines are skipped, and that is a scope statement rather than a convenience
//
// `Makefile:367` contains the words "`a -run list of test names`" inside a comment explaining why a
// gate runs a whole package instead. That is prose, and it was the second false positive. What this
// test is about is the **silent-green mechanism**: a command that exits 0 while selecting nothing.
// A stale citation in a Makefile comment is the ordinary loud kind — it misinforms a reader who can
// then go and look — and it belongs to the resolver above, whose domain is prose. So lines whose
// first non-space character is `#` are not commands and are not read as such.
func TestEveryGateCitedTestNameResolves(t *testing.T) {
	defined, _ := citationInventory(t)

	// Reuses the resolver's own definition set, deliberately: two populations of "the tests that
	// exist", built two ways, would eventually disagree, and then a gate citation would be called
	// dangling by one and fine by the other. The floor below is what makes the reuse safe.
	if len(defined) < 200 {
		t.Fatalf("found %d defined test functions; a population this small means the walk stopped "+
			"reaching the tree, and every gate citation below would be reported dangling", len(defined))
	}

	gates := []string{"../../Makefile"}
	workflows, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}
	// Globbed rather than enumerated — *scope controls to the space, not to the current sample* —
	// so a workflow added tomorrow is covered without anyone remembering this file exists.
	gates = append(gates, workflows...)

	cited := 0
	for _, path := range gates {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // prose, not a command; see the scope note above
			}
			if strings.Contains(line, "-fuzz ") || strings.Contains(line, "-bench ") {
				continue // the `-run XXX -<mode>` idiom; see the exemption note above
			}
			for _, m := range runFlagName.FindAllStringSubmatch(line, -1) {
				name := m[1]
				cited++
				if defined[name] {
					continue
				}
				t.Errorf("%s:%d invokes `-run %s`, which no test function defines:\n\t%s\n\t"+
					"This gate exits 0 and prints PASS when the name matches nothing, so it is "+
					"currently reporting a verdict about a question it never asks (#322's shape). "+
					"Re-point it at the control's real name, or delete the gate — a gate whose "+
					"subject no longer exists is worse than no gate, because it reads as coverage",
					path, i+1, name, strings.TrimSpace(line))
			}
		}
	}

	// Vacuity, and it is the assertion that keeps the rest honest: every check above is a loop over
	// matches, so a regex that stopped matching, a moved Makefile, or a renamed workflow directory
	// all produce a clean green over an empty set. Two named gates cite a test today, so a floor of
	// 2 is the measurement and not a guess; it is a floor rather than an equality because gates
	// citing controls is a habit this project should be free to grow.
	if cited < 2 {
		t.Errorf("found %d `-run <Name>` citations across %d gate files, want at least 2 (the "+
			"all-gates-on control, invoked from both the Makefile and ci.yml). A comparison against "+
			"an empty set succeeds — either the gates stopped citing tests by name, which is worth "+
			"knowing, or this test stopped being able to see them", cited, len(gates))
	}
	if cited > 0 {
		t.Logf("%d `-run <Name>` gate citations across %d files, all resolving", cited, len(gates))
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
			// Same exclusions as TestEverySkipSiteIsLicensed, and now shared rather than
			// asserted: this comment used to claim the two lists were the same while the
			// literal beneath it carried a fifth entry the other did not — *the defect
			// stated as the rule*, which makes a reader confirm the drift as though it were
			// the design. `skipWalkDir` is the one list; `third_party` is passed as this
			// walk's own documented addition, vendored upstream material we do not author,
			// so the divergence is visible at the call site instead of hidden in a copy.
			if skipWalkDir(d, "third_party") {
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
