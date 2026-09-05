// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryEngineGoroutineIsAtASiteADecisionAuthorises is the module-wide census of concurrency the
// *engine* constructs. Every `go` statement in a non-test file must be at a site a decision doc names,
// and the entry names it.
//
// # It was renamed, and the rename is the lesson rather than housekeeping
//
// It was `TestNothingInEngineCodeCreatesASecondObserver`, placed to protect a claim
// `internal/interp`'s `allocate` makes in prose: *"an unshared memory has no second observer by
// construction, so §0's performance partisanship says leave its allocate-and-blit alone rather than
// reserve address space no guest can race for."* That name asserted a **property** — that nothing
// creates a second observer — and T-1's spawn ([ADR 0068][0068]) makes it flatly false. A property
// name is falsified by whichever proposal discharges it, so *name a control after the rule, not the
// property*: the rule is that a goroutine in engine code is at an authorised site, and no proposal can
// discharge that, because a proposal is the thing that has to obtain the authorisation.
//
// **This tree has now paid for that twice, one proposal apart.** `internal/interp`'s package-scoped
// sibling `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling` was renamed twice for the same defect
// before it was given a rule name — and *it* survived this slice under its own name, which is the
// comparison worth keeping: the two controls watched the same event, and only the one named after a
// rule needed no rename when the event's authorisation arrived.
//
// # What the original claim was, and what became of it
//
// The `allocate` sentence is why an unshared memory does not reserve its maximum, which is why its
// backing array can move under `grow` — and a moving array was a memory-safety question, not a
// performance one, since a concurrent reader could observe the new length paired with the stale
// pointer (#556). **That half is closed by mechanism rather than by census.** [ADR 0058][0058] made
// `memory.img` an `atomic.Pointer[memImage]`, so moving the array is memory-safe for every memory,
// marked or not; [ADR 0056][0056] moved `grow`'s refusal onto a per-memory `noMove` mark that
// `allocate` sets wherever it reserves (#572). What is left of the original worry is a **coherence**
// residual with a stated population — an unshared memory in an instance that has spawned, grown while
// another thread holds an older image — filed as **#586**, needing §4 (**#10**) to say what is
// permitted. It is not memory unsafety and it is not what this control watches.
//
// So this control is no longer the tripwire for `allocate`'s prose. It is the census that keeps the
// *authorisation* honest: engine concurrency arrives one decided site at a time, and an undecided
// `go` anywhere in the module is what it fails on.
//
// # Why a `go` statement is the trigger, and why the allow list is keyed by function
//
// A second executor needs a second stack, and inside this module the only way to get one is a `go`
// statement, so the trigger stays syntactic and total rather than a guess about which function looks
// concurrent — *a comment's caller list is not the call graph*, and a predicate over names would miss
// the spawn helper nobody thought of.
//
// The census is no longer zero, so the allow list this control did not need now exists — and its key
// is `(path, enclosing function)`. Not a path alone, which would authorise every future `go` in
// `internal/interp/thread.go`; not a line, which re-points itself wrongly on the next insertion
// (*re-key an allow map by content, not by arithmetic*). Each entry carries the decision that
// authorised it and the exact count it may have, and **an entry that matches nothing is a FAIL** — an
// exemption that has rotted would otherwise leave the control green while permitting a site that no
// longer exists.
//
// It deliberately does **not** try to prove a spawned goroutine reaches linear memory. That would be a
// reachability question needing a call graph, it would be the thing an author could argue their way
// past, and it is not the question: any concurrent executor in this engine is a fact a decision doc
// should have named, whether or not it touches memory.
//
// # What it cannot see, stated rather than left to be discovered
//
// **An embedder calling `Invoke` on one instance from two goroutines.** Nothing in this tree documents
// whether that is permitted — `Instance` carries no concurrency contract either way — and no control
// here can assert anything about a caller outside the module. Writing that contract down is
// public-API-surface design, which §0 makes partisan and therefore Scott's and chat-Claude's rather
// than a test's to settle. Spawn landing does not change this: it adds threads the engine creates and
// says nothing about threads an embedder brings.
//
// Watched die by injection in both directions, because *a re-pointed control has not been watched die*
// and *"it now permits X"* is a forecast to run: a scratch non-test file containing a `go` statement
// FAILs, a second `go` inside the authorised function FAILs on the pinned count, and the authorised
// site's own `go` removed FAILs on the unmatched-entry arm. The permit direction is the ordinary green
// with spawn on main.
//
// [0056]: ../../docs/decisions/0056-the-no-move-mark-is-set-where-the-reservation-happens-and-grow-refuses-on-the-mark-because-spawn-can-establish-it-while-one-thread-exists.md
// [0058]: ../../docs/decisions/0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md
// [0068]: ../../docs/decisions/0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md
func TestEveryEngineGoroutineIsAtASiteADecisionAuthorises(t *testing.T) {
	// Keyed by repo-relative slash path and enclosing function, with the count each site may have and
	// the decision that says so. Adding an entry here is not a way to make this control pass: the
	// decision named in it has to exist and to say this.
	type site struct {
		path, fn string
	}
	authorised := map[site]struct {
		count int
		by    string
	}{
		{"internal/interp/thread.go", "(*Instance).spawn"}: {
			count: 1,
			by: "ADR 0068 — T-1's spawn, one OS-thread-locked goroutine per `Spawn`, " +
				"registered in the world before the statement runs",
		},
	}

	seen := map[site]int{}
	var offenders, scanned []string
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
		name := d.Name()
		// Non-test files only. A test that spawns goroutines is doing its job — the litmus battery
		// #10 will be nothing but — and the claim is about what the *engine* constructs.
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		// Full parse, not ImportsOnly: the subject is a statement, so the bodies are the domain.
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		scanned = append(scanned, rel)
		// Walked per declaration so each `go` carries its enclosing function. A `go` outside every
		// `FuncDecl` — in a package-level `var x = func() { go f() }` — gets the empty key, matches no
		// entry, and is an offender, which is the safe direction.
		for _, decl := range file.Decls {
			key := site{path: rel}
			if fn, isFunc := decl.(*ast.FuncDecl); isFunc {
				key.fn = declKey(fn)
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				g, isGo := n.(*ast.GoStmt)
				if !isGo {
					return true
				}
				pos := fset.Position(g.Go)
				if _, ok := authorised[key]; ok {
					seen[key]++
					return true
				}
				offenders = append(offenders, rel+":"+
					pos.String()[len(pos.Filename)+1:]+" in "+key.fn)
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", repoRoot, err)
	}
	sort.Strings(offenders)

	// The vacuity check, for the reason its sibling states: a walk that read no Go files would report
	// no offenders and pass, which is indistinguishable from a clean tree. Same floor as
	// `TestNoEngineLockIsHeldAcrossAChannelOperation`, which walks the same population.
	const engineFilesWhenWritten = 40
	if len(scanned) < engineFilesWhenWritten {
		t.Fatalf("scanned %d non-test .go file(s) under %s, want at least %d — the walk is not "+
			"reading the tree, so the assertion below asserted its property of nothing and passed",
			len(scanned), repoRoot, engineFilesWhenWritten)
	}

	if len(offenders) != 0 {
		t.Errorf("these non-test sites start a goroutine at no site a decision authorises: %v\n"+
			"Engine concurrency arrives one decided site at a time, and this control is the census "+
			"that keeps that true module-wide. What a new goroutine inherits, stated so it is not "+
			"discovered later: §4's boundary memory model has its mechanism (ADR 0052) and its "+
			"litmus battery is parked past what spawn needed (#10), so there is no vector that will "+
			"report a violation of the model this engine has not finished stating — the threads "+
			"corpus is not that instrument.\n"+
			"There is also a coherence residual with a named population (#586): an unshared memory "+
			"in an instance that has spawned, grown by one thread while another holds an older "+
			"image, loses the writes made through that image. Not memory unsafety — ADR 0058 "+
			"publishes the image through an atomic pointer, so a moving array is safe for every "+
			"memory — but undescribed for atomics until §4 speaks.\n"+
			"The way through is a decision doc that names this site and says what it does about "+
			"both, then an entry above citing it. Adding the entry without the decision forges the "+
			"authorisation, and moving the statement to a sibling package does not help: this "+
			"control's domain is the module",
			offenders)
	}

	// **Every entry has to match something, and match exactly as much as it claims.** An entry that
	// resolves to nothing is an exemption that has rotted — the control stays green while authorising
	// a site that no longer exists, and the next `go` written into that function is permitted by a key
	// nothing checked. Pinned to the exact count rather than a floor, because *a floor is not a
	// census*.
	for s, want := range authorised {
		got := seen[s]
		if got == want.count {
			continue
		}
		t.Errorf("%s's %s has %d `go` statement(s), and this control authorises %d (%s).\n"+
			"Too few: the site has moved or gone and the entry now resolves to nothing — re-point it "+
			"or delete it.\n"+
			"Too many: a goroutine has been added at an authorised site, which is not the same as "+
			"being an authorised goroutine. The decision named one",
			s.path, s.fn, got, want.count, want.by)
	}
}

// declKey names a declaration as the allow list above does: `Func`, `(Recv).Func` or `(*Recv).Func`.
// Not `fn.Name.Name` alone — two methods on different types can share a name, and a colliding key
// would authorise a `go` in whichever of them a later author wrote.
func declKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := ""
	switch e := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			recv = "*" + id.Name
		}
	case *ast.Ident:
		recv = e.Name
	}
	if recv == "" {
		return fn.Name.Name
	}
	return "(" + recv + ")." + fn.Name.Name
}
