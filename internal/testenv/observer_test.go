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

// TestEveryGoStatementInEngineCodeIsPrecededByTheWalk was formerly
// `TestNothingInEngineCodeCreatesASecondObserver`, and the rename is the honest form of a trigger
// change rather than a cosmetic one.
//
// # Why the trigger had to move, and why the control is not retired
//
// The predecessor failed on *any* `go` statement in engine code, because `internal/interp`'s
// `allocate` argued in prose that *"an unshared memory has no second observer by construction"* and
// nothing checked it. That argument is why an unshared memory did not reserve its maximum, which is
// why its backing array could move under `grow` — a memory-safety question, since a concurrent reader
// can observe a fresh length paired with a stale pointer (#556).
//
// T-1's `Spawn` (#554) is the second observer, and it has now landed together with its remedy.
// Decision [0056] chose the mark: `grow` refuses on a per-memory `noMove` flag rather than on
// `limits.Shared` (#572), and `Spawn` relocates and marks every memory the new thread can reach
// *before* starting it (`reserveForASecondThread`, called from `spawn`). So "there are no goroutines"
// has stopped being the property to protect, and a control that kept asserting it would fail forever
// on a tree that had done exactly what the control asked for.
//
// **The remedy is incomplete, which changes what a green here means and not what this should check**
// (#575). `Spawn`'s walk covers the memories the new thread reaches through *import slots*, and a
// table slot holding another instance's funcref takes it outside that closure — so pairing a `go`
// statement with the walk is **necessary and not sufficient**, and this control's green is a
// statement about the ordering rather than about the hole. The dynamic row in `internal/interp` that
// witnesses the gap fails today and says so; nothing about the pairing changes under #575's remedy,
// since a rule about when relocation is permitted still needs the relocation to happen first.
//
// **Retiring it instead would be the mistake this project has already paid for.** A tripwire names a
// *risk*, not a code shape, and the risk here has not gone anywhere: it is now "a goroutine in engine
// code that starts without the walk in front of it". So the subject is re-pointed at the ordering
// decision 0056 turns on — relocate and mark while one thread exists, never after — and the trigger
// becomes the pairing rather than the presence.
//
// **This is a trigger change, so it has been watched die on its own terms**, which the predecessor's
// message edit explicitly had not been and said so. Three falsifications, each run and read back:
// deleting the `reserveForASecondThread` call from `spawn` (FAIL, the `go` reported as unpaired),
// moving it below the `go` statement (FAIL, on the ordering rather than the presence), and adding a
// scratch non-test file with a bare `go` statement in a function that marks nothing (FAIL, naming the
// scratch site). The scratch file was removed in the same command — grave #561's method.
//
// # What it asserts, exactly
//
// For every `go` statement in a non-test file in the module: the enclosing function body contains a
// call to `reserveForASecondThread` at a source position *before* the `go`. The pairing is
// intraprocedural and positional, which is the strongest thing a syntactic check can say, and the two
// ways past it are worth naming rather than leaving to be found:
//
//   - A marking call inside a branch that does not execute, or a loop over an empty sequence, satisfies
//     this and marks nothing. `TestSpawnMarksEveryMemoryTheNewThreadCanReach` in `internal/interp` is
//     what asserts the property dynamically; this control's job is to catch the *next* `go` statement,
//     written by someone who never read decision 0056, before it reaches review.
//   - Hoisting the walk into a helper the enclosing function calls would read as unpaired here and
//     fail. That is a deliberate false positive: the ordering is the soundness argument, and a
//     reviewer being made to look at a refactor of it is the outcome this wants.
//
// It still does not try to prove the goroutine reaches linear memory. That needs a call graph, it is
// the thing an author could argue their way past, and it is not the question — the walk is cheap and
// unconditional, so pairing costs a spawner nothing even where the reachability argument would have
// gone its way.
//
// # What it cannot see, stated rather than left to be discovered
//
// **An embedder calling `Invoke` on one instance from two goroutines.** Nothing in this tree documents
// whether that is permitted — `Instance` carries no concurrency contract either way — so the walk only
// knows about threads the *engine* starts, and no control here can assert anything about a caller
// outside the module. Writing that contract down is public-API-surface design, which §0 makes partisan
// and therefore Scott's and chat-Claude's rather than a test's to settle.
//
// [0056]: ../../docs/decisions/0056-the-no-move-mark-is-set-where-the-reservation-happens-and-grow-refuses-on-the-mark-because-spawn-can-establish-it-while-one-thread-exists.md
func TestEveryGoStatementInEngineCodeIsPrecededByTheWalk(t *testing.T) {
	// The marking step's name. One identifier rather than a pattern, because decision 0056 puts the
	// reservation and the mark in one place on purpose: if this name stops existing, the citation
	// sweep and this control both fail, which is the coupling that is wanted.
	const marker = "reserveForASecondThread"

	var unpaired, paired, scanned []string
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
		// Full parse, not ImportsOnly: the subject is a statement, so the bodies are the domain.
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		scanned = append(scanned, rel)

		// Per *function body*, so a marking call in one function cannot pair a `go` in another. A
		// `go` outside any function body is impossible in Go, but a function literal assigned at
		// package level has no FuncDecl — `ast.Inspect` over the file with the innermost enclosing
		// body tracked would be the general form; walking declarations covers every construct this
		// module has, and a `go` this loop never reaches is reported by the census check below.
		var seenGo int
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}
			var marks, gos []token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.GoStmt:
					gos = append(gos, node.Go)
				case *ast.SelectorExpr:
					if node.Sel != nil && node.Sel.Name == marker {
						marks = append(marks, node.Sel.NamePos)
					}
				case *ast.Ident:
					// An unqualified call, for a future caller inside the memory's own package.
					if node.Name == marker {
						marks = append(marks, node.NamePos)
					}
				}
				return true
			})
			for _, g := range gos {
				seenGo++
				site := rel + ":" + fset.Position(g).String()[len(fset.Position(g).Filename)+1:] +
					" in " + fn.Name.Name
				ok := false
				for _, m := range marks {
					if m < g {
						ok = true
						break
					}
				}
				if ok {
					paired = append(paired, site)
				} else {
					unpaired = append(unpaired, site)
				}
			}
		}
		// A `go` statement the declaration loop could not attribute to a function body. Counted
		// separately so that a construct this control does not model reads as a failure rather than
		// as a pass — the alternative is a site that is neither paired nor reported.
		var total int
		ast.Inspect(file, func(n ast.Node) bool {
			if _, isGo := n.(*ast.GoStmt); isGo {
				total++
			}
			return true
		})
		if total != seenGo {
			unpaired = append(unpaired, rel+": "+
				"a go statement outside any function declaration this check walks")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", repoRoot, err)
	}
	sort.Strings(unpaired)
	sort.Strings(paired)

	// The vacuity check the predecessor had, for the reason it stated: a walk that read no Go files
	// would report no offenders and pass, which is indistinguishable from a clean tree. Same floor as
	// `TestNoSyncPrimitiveIsUsedInEngineCode`, which walks the same population.
	const engineFilesWhenWritten = 40
	if len(scanned) < engineFilesWhenWritten {
		t.Fatalf("scanned %d non-test .go file(s) under %s, want at least %d — the walk is not "+
			"reading the tree, so the assertion below asserted its property of nothing and passed",
			len(scanned), repoRoot, engineFilesWhenWritten)
	}

	// **The second vacuity check, and it is new with this trigger.** The predecessor was satisfied by
	// an empty census — zero `go` statements was its passing state. This one is *falsified* by an
	// empty census: with no `go` statement in the tree there is no pairing to check, and a green here
	// would be a required zero that could not have come out otherwise. So the census is asserted to
	// be non-empty, which also makes the control notice if `Spawn`'s goroutine is ever deleted or
	// moved behind a build tag this walk does not read.
	if len(paired)+len(unpaired) == 0 {
		t.Fatalf("found no `go` statement in %d non-test .go file(s) — this control pairs goroutines "+
			"with decision 0056's walk, so an empty census means it asserted nothing and passed. "+
			"T-1's `Spawn` (#554) is the site that should be here; if it has been removed or gated "+
			"behind a tag this walk cannot see, this control needs its subject back before it can "+
			"protect anything", len(scanned))
	}

	if len(unpaired) != 0 {
		t.Errorf("these non-test sites start a goroutine with no call to %q before it in the same "+
			"function: %v\n"+
			"A second thread makes `internal/interp`'s unshared memories two-thread-reachable, and "+
			"the consequence is not a slow path: `grow` may replace such a memory's backing array "+
			"while the other thread holds the old slice header or an ADR 0051 pointer into it — a "+
			"stale pointer paired with a fresh length, and a use-after-free under the atomics "+
			"(#556).\n"+
			"**Decision 0056 fixed the remedy and its ordering, so this is not a design question.** "+
			"Relocate every memory the new thread can reach onto a reserved array and mark it "+
			"`noMove` *before* starting the thread — `Spawn` does this with "+
			"`target.reachableMemories()` and %q, and `grow`'s refusal arm is what reads the mark. "+
			"Doing it after the goroutine starts is option (C), which 0056 rejects: a mark written "+
			"while two threads run is not a fact about the past, and nothing makes it one.\n"+
			"Two ways out this message does not offer, because the ruling closed them. Reserving for "+
			"every memory without extending the refusal is not sufficient — the reservation is "+
			"capped at `sharedReservePages`, so it closes the hole below the cap and reopens it "+
			"above. And gating on `limits.Shared` was never available: `Spawn` runs the entry in the "+
			"*same* instance, so that flag does not answer the question, and refusing to spawn on an "+
			"instance holding unshared memories is separately rejected, since a module may "+
			"legitimately hold both.\n"+
			"If your goroutine genuinely reaches no linear memory, that is a real case this control "+
			"deliberately does not model — say so in the PR and narrow the domain there, with the "+
			"reachability argument written down. Do not add the file to a list.\n"+
			"Paired sites, for contrast: %v",
			marker, unpaired, marker, paired)
	}
}
