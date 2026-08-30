// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// # Citations to a location in this tree — #456, ADR 0047
//
// A citation naming a file and a line number in the **pinned reference** is a permanent address: the
// pin does not move, so the number is durable by construction and the form is exactly right. The
// habit was learned there, and then applied to files this repo edits, where the same syntax is a
// snapshot with nothing behind it. *A premise that holds for one mechanism is not a premise about the
// other.*
//
// **Nothing exempts this file, and its own footprint in its own sample is stated rather than left to
// be discovered.** The scan counts every citation-shaped token in the tree including the ones in this
// file, so an illustration written in the live positional form would land in the pinned population and
// appear in #497's work plan as a citation to convert. Illustrations here are therefore described in
// words rather than spelled — but not all of them, and the exceptions are the interesting part:
//
//   - a metasyntactic example whose file part is a bare basename and whose location is a stand-in
//     letter, in `TestSymbolCitationsResolveToADeclaration`'s doc comment;
//   - the mandated form itself, in the census's failure message, so the author who trips the pin reads
//     what to write instead of a rule number.
//
// **Neither is positional, so neither touches the pin, and the thing keeping them out of the binding
// assertion is the resolution axis rather than an exemption**: one names a file by basename, the other
// names no file at all, and the binding assertion's domain is path-qualified. That is also this file's
// own tripwire — write either of them at a real path and the assertion demands a declaration for a
// placeholder and fails. The repair then is to de-qualify the example, not to exempt the file. Ask the
// instrument for the current footprint; it is printed per citing file on every run.
//
// #440 is the proximate witness: 29 lines added to one doc block silently invalidated a citation that
// had been **exact**, and the only reason it did not stay broken is that the author happened to grep.
//
// # Two defects, and the second one does not need drift to be a defect
//
//   - **Drift.** The number was right and an insertion above it made it wrong. There is no
//     diagnostic: the citation still points *somewhere*, the somewhere is usually a comment, and the
//     reader lands on plausible English with no signal.
//   - **Ambiguity.** A citation whose file part is a bare basename names no file at all when two
//     tracked files share that basename. This one is wrong *today*, before any edit: `match.go` is
//     cited with a line number 22 times and there are two of them, in `internal/text` and
//     `internal/validate`, both of which do matching. `module.go` is cited 11 times against two
//     files, `instr.go` 10 against three. The census below is what found it; the issue had not named
//     it.
//
// # What this file asserts, and why the binding half is small on purpose
//
//   - **`TestSymbolCitationsResolveToADeclaration`** — binding. Every *path-qualified* citation
//     whose location is a symbol rather than a line names a symbol the cited file declares. This is
//     the form ADR 0047 mandates, and its domain is small today because the conversion is #497. It
//     is what makes the form self-resolving: a rename breaks the citation loudly, which a line
//     number can never do.
//   - **`TestPositionalCitationCensusIsPinned`** — binding, and the ratchet. Every positional
//     citation, bucketed twice, pinned to an exact count. The population grew 91 → 263 → 303 while
//     #456 sat open, at a rate the issue measured, and #502 moved it **in both directions**: −1 for
//     ADR 0035's one citation into `exec.go`, which had been *correct* until that PR's own six-line
//     insertion falsified it — drift the author caused is the author's to repair, whatever #497 decides
//     about the dated records nobody broke — and **+3** for ADR 0024's amendment, which quotes the
//     ADR's four drifted citations as its subject matter and so is positional by construction (below).
//     Net **305**. The decrease is reported as a decrease rather than netted away, because a population
//     that grows by specimens and shrinks by conversions is two rates and one number hides both.
//     **Four specimens, +3 here**, and the missing one is the pair of censuses working: the fourth has
//     no file part, so `posCiteRe` cannot see it and `TestBareContinuationCitationsAreBounded` counts
//     it instead (`go` 103 → 104). A specimen set splitting across the two populations is the reason
//     both are pinned — a reader who checks one number and infers the other gets three of four.
//     An exact pin is what stops the growth, because a new positional citation cannot land without
//     someone editing a number in this file and reading why. Exact and not a floor in both
//     directions: *a floor bounds the catastrophic case only*, and here the interesting motion is +1.
//
// # The rule that excludes the data keys, stated rather than applied by hand
//
// Scott's ruling on the #486 review: *"exclude the 39 map keys by a stated rule, not by hand."* The
// rule is the AST's own: **a positional citation in a composite-literal key is a coordinate in a
// control's data, not prose a reader follows.** `foreclose_test.go`'s allow map is keyed on a path, a
// line number, a bound-account name and a gate name, concatenated — and the number there identifies
// the *line* the scan reports, because the scan reports lines. No symbol lives there to name.
//
// **The ruling's number was an estimate and the rule's population is 23, not 39.** The file holds 43
// positional citations; 23 are composite-literal keys, 3 are prose inside a reason string, and 17
// are prose in comments. So a rule stated as "the map keys in that file" would have swept 20 pieces
// of prose in with the data — which is the point of deriving the population instead of naming it.
const citeFormDoc = "see the block comment above — ADR 0047, #456"

// posCiteRe matches a citation to a location in a Go file: a file part ending `.go`, a colon, and a
// location. Deliberately permissive on both sides, because the classification below is what decides
// what a token *is* — a regex that only matched the well-formed shape would report a clean tree by
// declining to look at the malformed one.
var posCiteRe = regexp.MustCompile(`[A-Za-z0-9_./-]+\.go:[A-Za-z0-9_.-]+`)

// A location is line-shaped, identifier-shaped, or neither, and `kindMetasyntax` is the **neither**
// bucket — a location like a bracketed placeholder that is not a legal identifier.
//
// **A metasyntactic stand-in that *is* identifier-shaped lands in the symbol bucket, and that is
// stated because the obvious reading of the three kinds is wrong.** A single `N` standing for "some
// line" matches the identifier pattern; nothing lexical distinguishes it from a real symbol name, and
// a hand list of placeholder spellings is the exemption surface Scott's ruling on the #486 review
// ruled out. What separates a placeholder from an address is therefore not the location at all but the
// **resolution**: metasyntax stands for an arbitrary file, so its file part is a placeholder too, and
// the binding assertion's path-qualified domain excludes it for that reason rather than by
// recognising it. There is no such thing as metasyntax at a real address — a citation naming a
// tracked file and an identifier is a claim about that file, and the assertion is right to check it.
var (
	lineLocRe   = regexp.MustCompile(`^[0-9]+([-,][0-9]+)*$`)
	symbolLocRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)
)

// locCite is one citation, carrying both classifications it is bucketed by. Two dimensions and not
// one composite key: the channel says who reads it, the resolution says whether it names a file, and
// a cross-tabulated pin would have to be rewritten cell by cell by every conversion PR while saying
// nothing the two margins do not.
type locCite struct {
	citer      string // repo-relative file the citation is written in
	line       int    // line in that file, for the failure message
	token      string // the matched text
	filePart   string // everything before the last `.go:`
	location   string // everything after it
	channel    string // who reads it: a comment, a data key, a string value, markdown
	resolution string // whether the file part names a file, and how
	target     string // the resolved repo-relative path, empty when unresolved
	kind       string // line | symbol | metasyntax
}

// Channels and resolutions, named once. A bucket's name appears in the pinned map, in the printed
// census and in a failure message, and three spellings of one bucket is how a census stops adding up.
const (
	chanComment  = "Go comment"
	chanDataKey  = "Go composite-literal key"
	chanLitValue = "Go string value"
	chanMarkdown = "markdown"

	resPathQualified = "path-qualified"
	resBasenameUniq  = "bare basename, unique"
	resBasenameAmbig = "bare basename, ambiguous"
	resNoSuchFile    = "names no file in the tree"

	kindLine       = "line"
	kindSymbol     = "symbol"
	kindMetasyntax = "metasyntax"
)

// treeFiles returns every file in the tree, repo-relative and sorted.
//
// The walk's own boundary addition is `third_party`, passed at the call site on `mdSources`'s
// precedent and for its reason: the fetched spec material is upstream's, cites itself in conventions
// this repo does not set, and a rule about our citations has no jurisdiction there.
//
// **Every file and not just the scanned ones**, because the two halves are different populations: a
// citation is *written* in `.go` and `.md`, and it *names* anything — a shell script, a Makefile, a
// `.wast` fixture. Deriving the vocabulary from the scanned set would report a correct citation to a
// generator's testdata as naming no file in the tree.
func treeFiles(tb testing.TB) []string {
	tb.Helper()

	var out []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipWalkDir(d, "third_party") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		tb.Fatalf("walking the tree for the citation vocabulary: %v", err)
	}
	slices.Sort(out)
	return out
}

// gatherLocationCitations scans every `.go` and `.md` file in the tree and classifies every location
// citation in it.
//
// **A `.go` file is read through its AST and a `.md` file as text**, and that asymmetry is the whole
// mechanism of the data-key rule: only the parse can tell a coordinate in a map key from a sentence.
// It is also where a partition can go wrong silently, so the caller asserts the identity — comments
// plus literals equals what a plain scan of the same bytes finds — rather than trusting it. Grave
// #416's shape: a breakdown that does not add up is a breakdown with a channel nobody is reading.
func gatherLocationCitations(tb testing.TB) []locCite {
	tb.Helper()

	files := treeFiles(tb)
	if len(files) < 200 {
		tb.Fatalf("the tree walk found %d files, which is too few to be this repo — the citation "+
			"vocabulary would be mostly empty and every citation would read as naming no file",
			len(files))
	}

	// The resolution vocabulary: full paths, and basenames with their candidate count. A basename
	// resolves only when exactly one tracked path ends in it — *a first-match pick declines to ask*,
	// and the count is the whole finding for `match.go`.
	paths := make(map[string]bool, len(files))
	byBase := map[string][]string{}
	for _, p := range files {
		paths[p] = true
		byBase[filepath.Base(p)] = append(byBase[filepath.Base(p)], p)
	}
	// A suffix match, not a basename match: `internal/validate/match.go` cited as
	// `validate/match.go` is path-*ish* and resolves to exactly one file, so it is not ambiguous —
	// it is simply not the form the ADR mandates, and the resolution axis is about whether a reader
	// can find the file, not about whether the author spelled it fully.
	resolveFilePart := func(fp string) (target, resolution string) {
		if paths[fp] {
			return fp, resPathQualified
		}
		var hits []string
		for _, p := range files {
			if strings.HasSuffix(p, "/"+fp) {
				hits = append(hits, p)
			}
		}
		switch len(hits) {
		case 0:
			return "", resNoSuchFile
		case 1:
			return hits[0], resBasenameUniq
		default:
			return "", resBasenameAmbig
		}
	}

	var out []locCite
	add := func(citer string, line int, channel, text string) {
		for _, m := range posCiteRe.FindAllString(text, -1) {
			i := strings.LastIndex(m, ".go:")
			fp, loc := m[:i+3], m[i+4:]
			target, resolution := resolveFilePart(fp)
			kind := kindMetasyntax
			switch {
			case lineLocRe.MatchString(loc):
				kind = kindLine
			case symbolLocRe.MatchString(loc):
				kind = kindSymbol
			}
			out = append(out, locCite{
				citer: citer, line: line, token: m, filePart: fp, location: loc,
				channel: channel, resolution: resolution, target: target, kind: kind,
			})
		}
	}

	for _, rel := range files {
		switch {
		case strings.HasSuffix(rel, ".md"):
			blob, err := os.ReadFile(filepath.Join(repoRoot, rel))
			if err != nil {
				tb.Fatalf("reading %s: %v", rel, err)
			}
			for n, text := range strings.Split(string(blob), "\n") {
				add(rel, n+1, chanMarkdown, text)
			}
		case strings.HasSuffix(rel, ".go"):
			blob, err := os.ReadFile(filepath.Join(repoRoot, rel))
			if err != nil {
				tb.Fatalf("reading %s: %v", rel, err)
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(repoRoot, rel), blob, parser.ParseComments)
			if err != nil {
				tb.Fatalf("parsing %s: %v", rel, err)
			}
			before := len(out)
			for _, cg := range f.Comments {
				for _, c := range cg.List {
					add(rel, fset.Position(c.Pos()).Line, chanComment, c.Text)
				}
			}
			keys := compositeKeyLits(f)
			ast.Inspect(f, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				channel := chanLitValue
				if keys[bl] {
					channel = chanDataKey
				}
				add(rel, fset.Position(bl.Pos()).Line, channel, bl.Value)
				return true
			})
			// The partition assertion, per file so a failure names the file rather than a delta.
			// A token in neither channel is in a channel nothing here reads — an identifier, a
			// struct tag, a raw import path — and the census would under-report it silently.
			if got, want := len(out)-before, len(posCiteRe.FindAllString(string(blob), -1)); got != want {
				tb.Errorf("%s: the AST channels found %d location citations and a plain scan of the "+
					"same bytes found %d. The difference is in a channel this control does not "+
					"read, so the census under-reports by %d and every pin below is wrong by that "+
					"much (%s)", rel, got, want, want-got, citeFormDoc)
			}
		}
	}
	return out
}

// compositeKeyLits returns every string literal that is part of a composite-literal key, following
// concatenation because the allow map's keys are written as `"a" + "b"` across a line break and a
// rule that only looked at a bare `*ast.BasicLit` key would classify half of one key as a value.
func compositeKeyLits(f *ast.File) map[*ast.BasicLit]bool {
	out := map[*ast.BasicLit]bool{}
	var mark func(ast.Expr)
	mark = func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				out[v] = true
			}
		case *ast.BinaryExpr:
			mark(v.X)
			mark(v.Y)
		case *ast.ParenExpr:
			mark(v.X)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range cl.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				mark(kv.Key)
			}
		}
		return true
	})
	return out
}

// declaredNames returns every name a Go file declares, in the spellings a citation may use.
//
// A method is registered under both `Method` and `Recv.Method`, and a struct field under
// `Type.Field`, because the prose in this tree writes all three — `Instance.link`, `decodeConstExpr`,
// `binary.Import`'s field names. A citation that names a *local* variable resolves against nothing
// here and is meant to: a name visible only inside one function body is not an address a reader can
// navigate to, which is the property the whole form is chosen for.
func declaredNames(tb testing.TB, rel string) map[string]bool {
	tb.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(repoRoot, rel), nil, 0)
	if err != nil {
		tb.Fatalf("parsing the citation target %s: %v", rel, err)
	}
	out := map[string]bool{}
	recvName := func(fl *ast.FieldList) string {
		if fl == nil || len(fl.List) == 0 {
			return ""
		}
		t := fl.List[0].Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if id, ok := t.(*ast.Ident); ok {
			return id.Name
		}
		return ""
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			out[d.Name.Name] = true
			if r := recvName(d.Recv); r != "" {
				out[r+"."+d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = true
					st, ok := s.Type.(*ast.StructType)
					if !ok || st.Fields == nil {
						continue
					}
					for _, fld := range st.Fields.List {
						for _, name := range fld.Names {
							out[name.Name] = true
							out[s.Name.Name+"."+name.Name] = true
						}
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						out[name.Name] = true
					}
				}
			}
		}
	}
	return out
}

// symbolCitationFloor is the number of path-qualified symbol citations this tree is known to hold,
// and it is a floor because the intended motion is upward: #497 converts the positional population
// into this form, and an equality would put this control in the way of the thing it exists to
// encourage.
//
// Four when it was written — `inventory_test.go`'s skip licences, whose map keys pair this package's
// path with `RequireSuite`, `RequireSuiteFile`, `RequireSpecRef` and `RequireProposalDoc`. They are
// the tree's own precedent for the form and the reason ADR 0047 does not have to invent a spelling,
// and they are also why the floor is exactly the real domain: this comment describes them rather than
// citing them, so the number counts the tree and not the instrument. The floor is a
// **vacuity** guard and not a target: the assertion below is universally quantified, so a domain
// drained to empty satisfies it while asserting nothing, and that is how this control would die
// quietly if a future edit narrowed the regex.
const symbolCitationFloor = 4

// TestSymbolCitationsResolveToADeclaration is the binding half of ADR 0047: the form is only better
// than a line number if something checks it, and this is the something.
//
// # What a failure means
//
// The cited file does not declare the named symbol. Either the symbol was renamed or deleted — in
// which case the sentence around the citation is describing code that is not there, which is the
// defect — or the citation names a local, which the form does not address by design.
//
// # Why the domain is path-qualified only, stated as an under-match rather than left implicit
//
// A bare basename cannot be resolved for 57 of this tree's citations, so a control that demanded a
// declaration from an unresolved file would fail on the ambiguity rather than on the symbol, and
// report the wrong defect. `call.go:N` in a `CHANGELOG.md` entry is the concrete case: it is
// metasyntax in a dated record, it is identifier-shaped, and it names nothing because it is not
// meant to. Those live in the census, counted and printed; only the path-qualified symbolic form is
// binding here.
func TestSymbolCitationsResolveToADeclaration(t *testing.T) {
	cites := gatherLocationCitations(t)

	decls := map[string]map[string]bool{}
	checked := 0
	for _, c := range cites {
		if c.kind != kindSymbol || c.resolution != resPathQualified {
			continue
		}
		if !strings.HasSuffix(c.target, ".go") {
			t.Errorf("%s:%d: `%s` names a symbol in %s, which is not a Go file, so there is no "+
				"declaration to resolve against (%s)", c.citer, c.line, c.token, c.target, citeFormDoc)
			continue
		}
		if decls[c.target] == nil {
			decls[c.target] = declaredNames(t, c.target)
		}
		checked++
		if !decls[c.target][c.location] {
			t.Errorf("%s:%d: `%s` names `%s`, and %s declares no such symbol. Either the symbol "+
				"moved and the citation is now describing code that is not there, or it names "+
				"something local, which this form cannot address (%s)",
				c.citer, c.line, c.token, c.location, c.target, citeFormDoc)
		}
	}

	if checked < symbolCitationFloor {
		t.Errorf("resolved %d path-qualified symbol citations, below the floor of %d. This "+
			"assertion is universally quantified, so a shrinking domain does not fail it — it "+
			"stops asserting anything. Either the form is being written some other way or the "+
			"scan no longer sees it (%s)", checked, symbolCitationFloor, citeFormDoc)
	}
	t.Logf("path-qualified symbol citations resolved against a declaration: %d (floor %d)",
		checked, symbolCitationFloor)
}

// positionalByChannel and positionalByResolution pin the positional population exactly, on two
// margins of the same set.
//
// # Why an exact pin
//
// The population is what #456 is about, and it **grew while the issue was open** — 91 in the title,
// 263 at `6afbd9c`, 303 at `496598e` — because *adding lines breaks every line citation below* and
// every slice that lands adds prose. Scott's order to do this next named that rate as the reason. A
// floor would have watched it grow; the exact pin is what makes +1 a red, and the red lands on the
// author of the +1 rather than on whoever eventually takes #497. It is **305 here**, and both
// components of that moved in the PR that earned them rather than in a later sweep: the one
// conversion down, the four quoted specimens up.
//
// # The pin has now landed on its own author three times, which is the evidence that it aims right
//
// Twice while #502 was being written, and once the turn before. A control that only ever fires on
// someone else's work is indistinguishable from a control tuned to the author's habits, so the
// sequence is worth recording where the next person to edit these numbers will read it:
//
//   - The citations law in `docs/laws/citations.md` falsified **its own draft** — the paragraph
//     asserting the form was written with a citation in the form it was banning.
//   - This pin fired on the `CHANGELOG.md` entry *about re-pointing markdown line citations*, which
//     had added three of them to describe the three it removed.
//   - This pin fired again on ADR 0024's amendment, whose whole subject is four drifted citations and
//     which necessarily writes all four down.
//
// The third is the one that changed the control's documentation rather than the diff, because it is
// the first firing that was **not** a mistake: the citations were supposed to be there. That is how a
// by-construction class gets found — not by predicting it, but by an exact pin refusing a change its
// author believed was exempt. (Scott, on the #502 review: *"two turns running, a control has caught
// its own author at the moment of authorship … that's the strongest evidence available that they're
// aimed correctly, and it's worth a sentence in their own documentation."*)
//
// The 303 is not the 301 the relay comment on #456 reported, and the difference is the instrument
// rather than the tree: that figure came from a shell one-liner whose file-part pattern rejected a
// digit, so `internal/spec/provenance_test.go` and one other citer were invisible to it. The number
// here is the one to carry forward — *measure with the instrument, not a regex*, and this control is
// now the instrument.
//
// # The two margins, and the identity between them
//
// A channel says who reads the citation. A resolution says whether it names a file. Every citation
// has exactly one of each, so the two maps sum to the same total, and the test asserts that — a
// margin that does not add up is a bucket somebody added on one axis and not the other.
//
// # What the numbers say, which is the residue measurement #492's probe was conditioned on
//
//   - **23 are composite-literal keys** — coordinates in `foreclose_test.go`'s allow map, positional
//     *by construction*. A conversion cannot remove these: the scan they belong to reports lines, so
//     a line is what its data is keyed on. This is why the residue is non-empty as a matter of form
//     rather than of unfinished work, and it is what discharges the probe.
//   - **183 are markdown**, of which the great majority are `CHANGELOG.md` and `docs/decisions/` —
//     dated records, where re-pointing a number rewrites the record rather than repairing it. That
//     question is #497's and Scott's, not this control's; they are counted here, not judged.
//     **One carve-out, and it is not a licence:** a citation that was correct until the *citing* PR's
//     own insertion falsified it is that PR's drift, not a dated record, and #502 converted the one
//     instance it created (181 → 180). Measured rather than assumed — **all four** of ADR 0024's
//     citations into `internal/interp` are wrong today, by roughly +24, +43, +86 and +335 lines, so
//     this population's drift is already unbounded and holding a file's line count constant rescues
//     none of it. A stale `+335` still names a line that exists, which is why nothing here fires on it.
//     (An earlier reading called the +24 one *arguable* on the grounds that its range contains call
//     sites; the ADR quotes the *definition* immediately below it, so the range is wrong too. The
//     correction ran against the reader's own interest, which is the only direction worth recording.)
//   - **Three of those 183 are positional by construction, and they are the first markdown rows that
//     are.** ADR 0024's amendment tabulates the four citations above in a *"cited as"* column, so the
//     drifted coordinate is the datum: converting one of those ranges to a symbol would destroy the
//     record of what the ADR said. (Named without quoting one, which would put another specimen in
//     this comment's own channel — the sentence does not need the coordinate, and a citation a
//     sentence does not need is the population.) Same class as the 23 data keys, from the other
//     direction — prose whose subject happens to be a coordinate. **Counted, not exempted**, because
//     an exemption is written by whoever tripped the instrument and *inherits none of the trigger's
//     lessons*; the pin rose by three and this paragraph is the receipt. The datum #497 should carry is
//     that its convertible population is smaller than its total by however many specimens the
//     conversion work itself writes down.
//   - **57 name no file**, because their basename is ambiguous. Those are broken now, and no drift
//     was needed to break them.
//   - **No bucket for a positional citation naming a file that does not exist**, because there are
//     none. Stated rather than left as an absent key: the map is compared for equality, so a bucket
//     appearing at all is a failure, and a reader should know that is the intended state and not an
//     omission.
var (
	positionalByChannel = map[string]int{
		chanComment:  92,
		chanDataKey:  23,
		chanLitValue: 7,
		chanMarkdown: 183,
	}
	positionalByResolution = map[string]int{
		resPathQualified: 90,
		resBasenameUniq:  158,
		resBasenameAmbig: 57,
	}
)

// TestPositionalCitationCensusIsPinned is the ratchet, and the residue measurement.
//
// It prints the whole census — both margins, the per-file breakdown, and every ambiguous basename
// with the files it could mean — because the print is what the next reader works from, and a control
// that only asserts a number tells them nothing about which of 305 citations to convert first.
func TestPositionalCitationCensusIsPinned(t *testing.T) {
	cites := gatherLocationCitations(t)

	byChannel, byResolution := map[string]int{}, map[string]int{}
	byCiter := map[string]int{}
	ambiguous := map[string]map[string]bool{}
	other := map[string]int{}
	joint := map[string]int{}
	for _, c := range cites {
		if c.kind != kindLine {
			other[c.kind]++
			continue
		}
		byChannel[c.channel]++
		byResolution[c.resolution]++
		byCiter[c.citer]++
		joint[c.channel+" / "+c.resolution]++
		if c.resolution == resBasenameAmbig {
			if ambiguous[c.filePart] == nil {
				ambiguous[c.filePart] = map[string]bool{}
			}
			ambiguous[c.filePart][c.citer] = true
		}
	}

	total := 0
	for _, n := range byChannel {
		total += n
	}
	resTotal := 0
	for _, n := range byResolution {
		resTotal += n
	}
	if total != resTotal {
		t.Errorf("the two margins disagree: %d positional citations by channel, %d by resolution. "+
			"Every citation has exactly one of each, so a difference is a bucket present on one "+
			"axis and absent on the other, and neither total can be trusted (%s)",
			total, resTotal, citeFormDoc)
	}

	if !maps.Equal(byChannel, positionalByChannel) {
		t.Errorf("the positional census by channel is %s, pinned at %s.\n"+
			"An increase is a new line-numbered citation into this tree: ADR 0047 says write "+
			"`path/to/file.go:SymbolName` instead, and `TestSymbolCitationsResolveToADeclaration` "+
			"will then check it. A decrease is #497 doing its work — lower the pin in the same PR, "+
			"which is the moment the list gets read (%s)",
			censusString(byChannel), censusString(positionalByChannel), citeFormDoc)
	}
	if !maps.Equal(byResolution, positionalByResolution) {
		t.Errorf("the positional census by resolution is %s, pinned at %s (%s)",
			censusString(byResolution), censusString(positionalByResolution), citeFormDoc)
	}

	t.Logf("positional location citations: %d\n  by channel:    %s\n  by resolution: %s\n"+
		"  other locations: %s", total, censusString(byChannel), censusString(byResolution),
		censusString(other))

	// The joint distribution, printed and **not** pinned. The two margins above are pinned exactly,
	// and a margin constrains a total without constraining the cell: "57 are ambiguous" and "92 are Go
	// comments" together say nothing about how many ambiguous ones are Go comments, which is precisely
	// the number #497's second option has to state its retirement condition over. So it is computed
	// here — one map increment over citations this control already gathered, no new scan — rather than
	// asserted, because *which* cell must reach zero is the thing Scott has not yet ruled: option (a)
	// drives the whole ambiguous column to zero, option (b) only its Go rows, and pinning the joint
	// distribution now would freeze a shape before the decision that gives it a target.
	jointKeys := make([]string, 0, len(joint))
	for k := range joint {
		jointKeys = append(jointKeys, k)
	}
	sort.Strings(jointKeys)
	var jb strings.Builder
	for _, k := range jointKeys {
		fmt.Fprintf(&jb, "\n  %4d  %s", joint[k], k)
	}
	t.Logf("channel × resolution, which is the cross-tab #497's options are priced in:%s", jb.String())

	var citers []string
	for f := range byCiter {
		citers = append(citers, f)
	}
	sort.Slice(citers, func(i, j int) bool {
		if byCiter[citers[i]] != byCiter[citers[j]] {
			return byCiter[citers[i]] > byCiter[citers[j]]
		}
		return citers[i] < citers[j]
	})
	var b strings.Builder
	for _, f := range citers {
		fmt.Fprintf(&b, "\n  %4d  %s", byCiter[f], f)
	}
	t.Logf("positional citations by citing file, which is #497's work plan:%s", b.String())

	files := treeFiles(t)
	var bases []string
	for fp := range ambiguous {
		bases = append(bases, fp)
	}
	sort.Strings(bases)
	b.Reset()
	for _, fp := range bases {
		var cands []string
		for _, p := range files {
			if strings.HasSuffix(p, "/"+fp) {
				cands = append(cands, p)
			}
		}
		fmt.Fprintf(&b, "\n  %-18s cited from %d file(s), could mean any of: %s",
			fp, len(ambiguous[fp]), strings.Join(cands, ", "))
	}
	t.Logf("ambiguous file parts — these name no file today, before any drift:%s", b.String())
}

// # The continuation form, which is the population Scott's ruling folded into this issue
//
// The ruling on the #486 review added a term this control has to answer: *"14 pre-existing bare-form
// `(:NNN)` reference citations in `internal/validate` carry no filename prefix, so `rangeRe`/`pointRe`
// never match them and no sweep in the tree can see them at all. That is the same defect one level
// worse than #456's population — a citation nothing resolves **and** nothing counts. Folded here
// rather than filed separately, because a symbol-based resolver has to decide what to do with them."*
//
// The shape is a bare colon-and-number in a code span, continuing a citation that named a file some
// lines earlier — a second address in the same paragraph, written the way English continues a
// reference rather than the way a machine resolves one. `posCiteRe` cannot see it: there is no file
// part, so there is nothing to anchor on.
//
// **Two of the ruling's three premises are false as stated, and the third is off by a factor.**
// Measured by the control below: `internal/validate` holds no occurrence of the parenthesised spelling
// the ruling names, the shape it does hold is code-span-delimited, and the tree-wide population is not
// 14. The ruling's location and spelling were an estimate from one reading session; its *proposition* —
// a citation nothing resolves and nothing counts — is exactly right, and is what this control acts on.
//
// # Why this is counted by antecedent and pinned only on one bucket
//
// A bare continuation is not uniformly a defect, and which kind it is depends on what it continues.
// Continuing a `.wast` or `.ml` antecedent means it addresses the **spec suite or the pinned
// reference** — durable by construction, the case #456 excludes, and the majority of the population.
// Continuing a Go antecedent means it addresses a file this repo edits, which is #456's defect with
// the file part deleted. So the Go bucket is pinned exactly, on the positional census's reasoning, and
// the reference buckets are printed with a floor: they are not a defect to ratchet, but a scan that
// stopped seeing them would report a clean tree by declining to look.
//
// # The attribution is a heuristic, and the honest reading of its number is a bound rather than a count
//
// The antecedent is the nearest filename-bearing citation within `bareContWindow` lines above. That is
// what a reader does, and it is not a parse: a citation whose file was named twenty lines up is
// attributed to nothing, and `(unattributed)` is a real bucket rather than a rounding error. It is
// pinned too, and for the reason that matters — an unattributed occurrence **may be** a Go one, so the
// Go bucket is a floor on the in-scope population and Go-plus-unattributed is its ceiling. Pinning
// both is what makes the interval closed; pinning only the Go bucket would let the in-scope population
// grow inside the bucket that admits it has not looked.
var bareContRe = regexp.MustCompile("`:[0-9]+(-[0-9]+)?(,[0-9]+)*`")

// anteFileRe matches any filename that could serve as a continuation's antecedent. Wider than
// `posCiteRe` by design — the antecedent may itself be a bare basename, as `internal/validate`'s does,
// and a citation continuing an already-ambiguous one is not less attributable for it, only worse.
var anteFileRe = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(go|ml|mly|wast|wat|md|py|sh|yml)`)

// bareContWindow is how many lines above an occurrence the antecedent search reads. Twelve, which is a
// paragraph: far enough to cross a wrapped sentence and a code block, short enough that a *different*
// citation two paragraphs up is not silently adopted as the antecedent. A number rather than a
// paragraph parse, and the `(unattributed)` bucket is the price stated out loud.
const bareContWindow = 12

// bareContReferenceFloor guards the reference-antecedent buckets against vacuity, the way
// symbolCitationFloor guards the symbolic domain. Those buckets are printed rather than pinned, so
// nothing about them fails when the scan narrows — this is what does.
//
// **Close to the population it bounds, deliberately.** The two buckets hold 490; a floor at half of
// that would run, agree, and say nothing, which is *an unasserted distance is the vacuum*. 450
// tolerates the ordinary motion — a reference citation converted, a paragraph rewritten — and fires on
// a scan that lost a tenth of what it used to see.
const bareContReferenceFloor = 450

// bareContByAntecedent pins the two buckets that bound the in-scope population.
//
// **The `go` bucket rose to 104 in #502, and the +1 is positional by construction.** ADR 0024's
// amendment tabulates the ADR's four drifted citations, one of which has no file part; the table's
// *"cited as"* column has to reproduce it verbatim, so the specimen of the bare form is itself a bare
// occurrence. Three sibling occurrences were kept out of `docs/laws/` in the same PR by describing the
// citations instead of re-quoting them — the law's sentence needed the offsets, not the coordinates —
// which is the ordinary rule rather than an exemption: a citation a sentence does not need is the
// population. What is left is the one the sentence cannot do without.
//
// **It rose to 106 in #513, and the +2 is the same rule applied to `foreclosingLicensed`'s
// thirteenth re-key.** That header records each generation's transition for the four in-reason
// pointers inside the allow map's reasons — the old value and the new one, in that file, not quoted
// again here — and the pair of numbers *is* the datum: the ninth generation's finding is that a number
// one role vacates can be occupied by
// another, which is only visible because every generation wrote both values down. The same PR's
// re-key note had five further coordinates in its first draft, in a list of six drifted pointers, and
// those were converted to their referents (*"the `Name resolution rather than grammar` residue row,
// by 3"*) — the sentence needed the drift and the subject, never the address. So the ratchet moved by
// the two the record cannot do without, and the receipt is that the other five went the other way in
// the same paragraph.
// **`(unattributed)` rose to 52 in #524, and the +3 are three specimens of the bare form itself** —
// the ADR 0024 case above, arrived at independently by three emitters. Grave #529 taught `opgen`'s and
// `memarggen`'s `Emit`, and `opgen`'s test for it, to refuse a row that carries a line number and no
// file; each of the three explains the refusal by quoting what such a row *renders* — a colon and the
// line number, nothing before it. The sentence cannot do without it: the whole hazard is that the
// output **looks like a citation** and resolves against whichever same-named file the reader opens, and
// paraphrasing it describes the input while losing the thing that makes it dangerous. Same test as the
// ADR 0024 rise — a citation the sentence does not need is the population; a specimen being exhibited
// is not.
//
// This paragraph describes the rendering rather than reproducing it, and the reason is that the first
// draft did reproduce it and **made the census 53**: a doc comment pinning the count of bare forms,
// pushing that count by one, in the file that does the counting. *A ban reported in the banned form is
// still the banned form* — the scanner reads tokens, not quotation marks, and the three sites it is
// reporting on are the exhibit. The same move the ADR 0024 rise records for `docs/laws/`, which
// described its four specimens rather than re-quoting them.
//
// Worth noting which way the ratchet moved in the same PR: that branch **removed 540** bare-basename
// citations from the two generated tables by teaching the generators to emit pin-qualified paths. Three
// added as specimens against 540 converted is the ratio the doctrine is aiming at, and it is also why
// refMatchedFloor exists — see its doc for the floor that fired on that conversion.
var bareContByAntecedent = map[string]int{
	"go":             106,
	"(unattributed)": 52,
}

// TestBareContinuationCitationsAreBounded answers the term Scott's #486 ruling folded into #456: what
// a symbol-based resolver does with a citation that has no file part is **count it**, because it
// cannot resolve one.
//
// Resolution is not available here even in principle. A bare `:NNN` names a line in a file the reader
// is expected to still have in mind, so converting it to the mandated form is not a re-spelling — it
// is recovering an antecedent, deciding whether the number still points where the author meant, and
// writing a symbol. That is #497's work on a sub-population #497 could not see before this control
// existed, and the count is what puts it in the plan.
func TestBareContinuationCitationsAreBounded(t *testing.T) {
	files := treeFiles(t)
	if len(files) < 200 {
		t.Fatalf("the tree walk found %d files, which is too few to be this repo", len(files))
	}

	byAnte := map[string]int{}
	byCiter := map[string]int{}
	total := 0
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, ".md") {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		// Read as text and not through the AST, unlike the positional census, and the asymmetry is
		// deliberate: a continuation's meaning comes from its *proximity* to an earlier citation, so
		// the unit is the file's lines as a reader sees them. The AST would hand back one comment
		// group at a time and lose the distance that does the attributing.
		lines := strings.Split(string(blob), "\n")
		for n, ln := range lines {
			for _, m := range bareContRe.FindAllStringIndex(ln, -1) {
				total++
				byCiter[rel]++
				window := strings.Join(lines[max(0, n-bareContWindow):n], "\n") + "\n" + ln[:m[0]]
				hits := anteFileRe.FindAllString(window, -1)
				if len(hits) == 0 {
					byAnte["(unattributed)"]++
					continue
				}
				last := hits[len(hits)-1]
				byAnte[last[strings.LastIndex(last, ".")+1:]]++
			}
		}
	}

	sum := 0
	for _, n := range byAnte {
		sum += n
	}
	if sum != total {
		t.Errorf("%d occurrences scanned and %d attributed — every occurrence takes exactly one "+
			"bucket, so a difference is a classification falling through (%s)", total, sum, citeFormDoc)
	}

	pinned := map[string]int{}
	for k := range bareContByAntecedent {
		pinned[k] = byAnte[k]
	}
	if !maps.Equal(pinned, bareContByAntecedent) {
		t.Errorf("the in-scope continuation census is %s, pinned at %s.\n"+
			"An increase in `go` is a citation into a file this repo edits, with no file part at all: "+
			"write the antecedent's path and a symbol. An increase in `(unattributed)` is the same "+
			"thing in the bucket that cannot tell — the ceiling on the in-scope population moved. A "+
			"decrease in either is #497 doing its work; lower the pin in the same PR (%s)",
			censusString(pinned), censusString(bareContByAntecedent), citeFormDoc)
	}

	reference := byAnte["ml"] + byAnte["mly"] + byAnte["wast"] + byAnte["wat"]
	if reference < bareContReferenceFloor {
		t.Errorf("the reference-antecedent buckets hold %d occurrences, below the floor of %d. Those "+
			"buckets are printed rather than pinned, so nothing else here fails when this scan stops "+
			"seeing the shape — a narrowed pattern would leave the pinned buckets satisfied and the "+
			"population invisible again, which is the state the ruling found it in (%s)",
			reference, bareContReferenceFloor, citeFormDoc)
	}

	t.Logf("bare continuation citations: %d\n  by antecedent: %s\n"+
		"  in scope for #497: %d..%d (go .. go+unattributed)",
		total, censusString(byAnte), byAnte["go"], byAnte["go"]+byAnte["(unattributed)"])

	var citers []string
	for f := range byCiter {
		citers = append(citers, f)
	}
	sort.Slice(citers, func(i, j int) bool {
		if byCiter[citers[i]] != byCiter[citers[j]] {
			return byCiter[citers[i]] > byCiter[citers[j]]
		}
		return citers[i] < citers[j]
	})
	var b strings.Builder
	for _, f := range citers[:min(15, len(citers))] {
		fmt.Fprintf(&b, "\n  %4d  %s", byCiter[f], f)
	}
	t.Logf("continuation citations by citing file, top %d of %d:%s",
		min(15, len(citers)), len(citers), b.String())
}

// # A citation into the reference names its pin in the path — grave #517
//
// The pin set went plural with ADR 0007's 2026-08-28 amendment, and a citation form that had been
// **exact** became ambiguous the same hour: both pins license `interpreter/binary/decode.ml` and both
// license `interpreter/valid/valid.ml`, so a citation carrying only a basename names two files. That is
// #456's ambiguity defect (see the head of this file) with a worse resolution profile, because the two
// authorities are the *same program at different revisions*: the line numbers are close, both files are
// long, and a number valid in one resolves to a real rule in the other.
//
// Grave #517 is that happening. A validator citation written as the pin's nickname beside a bare
// basename resolved against the **core** validator, where the cited range holds an unrelated rule about
// immutable globals, and the control checking that citations describe their subjects agreed with it —
// it read the wrong file and found nothing wrong. It failed only because the word the comment used
// happens not to appear in core's range: *a description that had named one would have been green on the
// wrong file.* The repair made four citation controls in `internal/validate` resolve per pin; this is
// the half those controls cannot do, because a citation in `internal/binary` — or in a law, an ADR, a
// changelog entry — is in no package's domain.
//
// # The banned form is a nickname claiming a non-default pin, and it is derived rather than listed
//
// A bare basename is *not* itself the defect here and is not counted: unqualified resolves to the core
// pin by every resolver in this tree, so a bare citation to a core clause says what it means and #497's
// conversion is a separate population (2591 occurrences at this writing, printed below). What this asserts is
// narrower and is the shape the grave had: prose that **names one pin in words** and then cites a
// basename the path does not qualify. The citation then resolves — successfully, silently — to the
// other pin's file.
//
// The nickname vocabulary is derived from `RefPins()` and not spelled: each pin's directory segment is
// split on hyphens, and a token claimed by exactly one pin is that pin's nickname. Today that yields
// the threads pin's two spellings and *nothing* for the core pin, whose shared token is claimed twice
// — which is correct rather than a gap: the core pin has no nickname that could mislead, because
// unqualified already means core. A third pin added to the list arrives with its own nickname on the
// same rule, and this is why the derivation is not a list: a list would have been written for the pin
// set that existed when the grave was dug.
//
// # Adjacency, and what the window deliberately does not count
//
// The nickname must sit within `refNickWindow` words of the citation, reading the previous line and the
// same line's prefix so a wrapped comment does not hide it. Tight on purpose: *aboutness is not
// proximity*, and a paragraph that discusses one pin at length may cite the other's clause legitimately
// a few sentences later. The window catches the case where the nickname grammatically governs the
// citation, which is the case the grave was. The residue is stated rather than hidden — a nickname
// further away is not counted, and no instrument in this tree reads it.
//
// # Why a zero is a measurement here
//
// The pinned expectation is zero, and *an analytic zero is not a measurement* — so the two ways this
// could report zero without looking are asserted separately. `refQualifiedFloor` says the qualified
// form is actually present in the tree, which is what dies if the pattern stops matching; the matched
// total is printed and floored (refMatchedFloor, both halves summed), which is what dies if the pin
// vocabulary comes back empty. And the zero
// is observable: this control was falsified by restoring one of the nickname citations it was written
// for, which is a two-word edit any author can make by writing the form that reads most naturally.
const refNickWindow = 8

// refQualifiedFloor guards the pinned zero against vacuity: it is the number of *correctly* qualified
// reference citations the scan must still find. **Thirteen at the pin's writing, and the number is the
// instrument's rather than a prototype's**: a hand-built pattern over the five basenames the grave
// touched said ten, and the derived vocabulary is every licensed basename across both pins, so three
// qualified citations lived outside the set the author had in mind. *Measure with the instrument, not a
// regex.* A floor of ten tolerates one being reworded away while firing on a pattern that has gone
// dead. Not a ratchet: this population is meant to grow with every conversion #497 makes.
const refQualifiedFloor = 10

// refMatchedFloor is the other vacuity guard, and it is on the **total** the scan matches — qualified
// plus unqualified — because that is the quantity that goes to zero if the derived basename vocabulary
// comes back empty. 2867 at this writing.
//
// # It used to floor the unqualified half, and that floor was pointed at a population under demolition
//
// `refBasenameFloor = 1500` guarded the unqualified count alone, set "well below" its then-2591 with the
// reason stated in its own doc: *"#497's conversions will draw it down deliberately."* They did. #524's
// opgen and memarggen halves taught the two generators to emit each row's citation with its pin path,
// which converted **540** bare basenames in `internal/text/opcodes.go` and `memarg.go` in one
// regeneration, and the count landed at 1465 — below the floor, on a change that is the entire point of
// #497.
//
// So the floor fired on success, which is a floor pointed the wrong way. Re-tuning it would have worked
// and would have to happen again on the next conversion, each time with the number chosen after seeing
// the result — *amending a threshold having seen the number*, indefinitely, on a schedule set by the
// project's own progress. The invariant was available instead: conversion moves a citation between the
// two halves and leaves the sum alone, while an emptied vocabulary takes the sum to zero. Same
// catastrophic case, no re-tuning, and it stops floor maintenance from being a tax on doing the work.
//
// *A tripwire whose subject dissolves is re-pointed, not retired* — and the subject here did not even
// dissolve, it just stopped being the thing the number could bound.
const refMatchedFloor = 1200

// TestReferenceCitationsNameTheirPinInThePath is grave #517's tripwire, tree-wide.
//
// The grave's residual half is what this covers: the repair taught four controls in `internal/validate`
// to route a citation to the pin its qualifier names, and left every other channel — the sibling
// engine packages, `docs/laws/`, the ADRs, the changelog — able to write the form that resolved to the
// wrong file. A control living in the package that owns the citation could never have seen it, which is
// why this one lives beside the other citation censuses and derives its domain from the tree walk.
func TestReferenceCitationsNameTheirPinInThePath(t *testing.T) {
	pins := testenv.RefPins()
	if len(pins) < 2 {
		t.Fatalf("RefPins returned %d pins, want >=2 — with one authority a bare basename is "+
			"unambiguous and this control has no subject, so it must fail rather than pass (%s)",
			len(pins), citeFormDoc)
	}

	segs := map[string]string{}            // directory segment naming a pin → its Dest
	claims := map[string]map[string]bool{} // candidate nickname → the pins claiming it
	bases := map[string]bool{}             // licensed basenames, across every pin
	// form is the remedy the failure message prints, keyed by pin and basename, and it is built from
	// the licensed path rather than by joining the pin segment to the basename. *An error message is
	// testimony*: the join produces a legal-looking qualifier for a path that does not exist, and the
	// first draft of this control printed exactly that — a pin directory with the middle of the
	// authority's path deleted, offered to the author as the form to write.
	form := map[string]string{}
	for _, p := range pins {
		seg := path.Base(strings.TrimSuffix(p.Dest, "/"))
		segs[seg] = p.Dest
		for _, tok := range append([]string{seg}, strings.Split(seg, "-")...) {
			if claims[tok] == nil {
				claims[tok] = map[string]bool{}
			}
			claims[tok][p.Dest] = true
		}
		for f := range p.Floors {
			base := path.Base(f)
			bases[base] = true
			form[p.Dest+base] = seg + "/" + path.Base(path.Dir(f)) + "/" + base
		}
	}
	nick := map[string]string{}
	for tok, owners := range claims {
		if len(owners) != 1 {
			continue
		}
		for d := range owners {
			nick[tok] = d
		}
	}
	if len(nick) == 0 {
		t.Fatalf("no pin has a distinguishing nickname among %v — every token is claimed by two "+
			"pins, so the scan below can recognise nothing and its zero means nothing (%s)",
			slices.Sorted(maps.Keys(claims)), citeFormDoc)
	}
	if len(bases) < 3 {
		t.Fatalf("the licensed basenames derived from the pins are %v, too few to be the reference "+
			"vocabulary — a citation to any authority outside this set is invisible here (%s)",
			slices.Sorted(maps.Keys(bases)), citeFormDoc)
	}

	quoted := make([]string, 0, len(bases))
	for _, b := range slices.Sorted(maps.Keys(bases)) {
		quoted = append(quoted, regexp.QuoteMeta(b))
	}
	// The prefix group is what decides qualification, so it is captured rather than skipped: a
	// citation is qualified when some pin's own directory segment appears in the path it carries.
	refCiteRe := regexp.MustCompile(`((?:[A-Za-z0-9_.-]+/)*)(` + strings.Join(quoted, "|") + `):([0-9][0-9,-]*)`)
	nickRe := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_-])(` +
		strings.Join(slices.Sorted(maps.Keys(nick)), "|") + `)([^A-Za-z0-9_-]|$)`)

	files := treeFiles(t)
	if len(files) < 200 {
		t.Fatalf("the tree walk found %d files, which is too few to be this repo", len(files))
	}

	qualified, unqualified := 0, 0
	byCiter := map[string]int{}
	var offenders []string
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, ".md") {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		// Read as text, on TestBareContinuationCitationsAreBounded's reasoning: what makes this a
		// defect is the *distance* between a word and a citation, and the AST hands back one comment
		// group at a time. A citation in a law file has no AST at all.
		lines := strings.Split(string(blob), "\n")
		for n, ln := range lines {
			for _, m := range refCiteRe.FindAllStringSubmatchIndex(ln, -1) {
				prefix, token := ln[m[2]:m[3]], ln[m[0]:m[1]]
				isQualified := false
				for seg := range segs {
					if strings.Contains(prefix, seg+"/") {
						isQualified = true
					}
				}
				if isQualified {
					qualified++
					continue
				}
				unqualified++
				byCiter[rel]++

				window := ln[:m[0]]
				if n > 0 {
					window = lines[n-1] + " " + window
				}
				if w := strings.Fields(window); len(w) > refNickWindow {
					window = strings.Join(w[len(w)-refNickWindow:], " ")
				}
				if hit := nickRe.FindStringSubmatch(window); hit != nil {
					want := form[nick[strings.ToLower(hit[2])]+ln[m[4]:m[5]]]
					offenders = append(offenders, fmt.Sprintf("%s:%d: %q names %q in words and "+
						"cites %q, which resolves to the other pin — write `%s:%s`",
						rel, n+1, strings.TrimSpace(window), hit[2], token, want, ln[m[6]:m[7]]))
				}
			}
		}
	}

	if len(offenders) > 0 {
		t.Errorf("%d citation(s) name a pin in prose and leave the path unqualified, which is the "+
			"form that resolved to the wrong authority in grave #517:\n  %s\n%s",
			len(offenders), strings.Join(offenders, "\n  "), citeFormDoc)
	}
	if qualified < refQualifiedFloor {
		t.Errorf("the scan found %d path-qualified reference citations, below the floor of %d — the "+
			"assertion above reports zero offenders when this pattern stops matching, so this is what "+
			"fails instead (%s)", qualified, refQualifiedFloor, citeFormDoc)
	}
	if matched := qualified + unqualified; matched < refMatchedFloor {
		t.Errorf("the scan matched %d reference citations in total (%d qualified, %d unqualified), "+
			"below the floor of %d — the derived basename vocabulary is %v and a vocabulary that stops "+
			"matching takes the pinned zero with it. The total is floored rather than the unqualified "+
			"half because a conversion moves a citation between the halves and leaves this sum alone "+
			"(%s)", matched, qualified, unqualified, refMatchedFloor,
			slices.Sorted(maps.Keys(bases)), citeFormDoc)
	}

	t.Logf("reference citations: %d qualified, %d unqualified (#497's population), nicknames %v",
		qualified, unqualified, slices.Sorted(maps.Keys(nick)))

	var citers []string
	for f := range byCiter {
		citers = append(citers, f)
	}
	sort.Slice(citers, func(i, j int) bool {
		if byCiter[citers[i]] != byCiter[citers[j]] {
			return byCiter[citers[i]] > byCiter[citers[j]]
		}
		return citers[i] < citers[j]
	})
	var b strings.Builder
	for _, f := range citers[:min(10, len(citers))] {
		fmt.Fprintf(&b, "\n  %4d  %s", byCiter[f], f)
	}
	t.Logf("unqualified reference citations by citing file, top %d of %d:%s",
		min(10, len(citers)), len(citers), b.String())
}

// censusString renders a bucket map in a stable order, because a failure message that reorders its
// own buckets between runs cannot be diffed against the pin it is complaining about.
func censusString(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	if len(parts) == 0 {
		return "{}"
	}
	return "{" + strings.Join(parts, " ") + "}"
}
