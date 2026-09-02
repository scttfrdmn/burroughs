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

// TestNothingInEngineCodeCreatesASecondObserver is the tripwire for a claim `internal/interp`'s
// `allocate` makes in prose and nothing checks: *"an unshared memory has no second observer by
// construction, so §0's performance partisanship says leave its allocate-and-blit alone rather than
// reserve address space no guest can race for."*
//
// # The claim is load-bearing, and it is about to become false
//
// That sentence is why an unshared memory does not reserve its maximum, which is why its backing array
// can move under `grow` — and a moving array is a memory-safety question, not a performance one, since
// a concurrent reader can observe the new length paired with the stale pointer (#556, and `allocate`'s
// own comment). The whole argument rests on there being exactly one observer.
//
// **T-1's spawn falsifies it.** On `v1/t1-spawn` (#554), `Instance.Spawn` refuses an instance with no
// shared memory (`ErrNotShared`) and then runs the entry function *in the same instance*, so a
// spawn-capable instance's **unshared** memories are reachable from two threads. `limits.Shared` is
// therefore not a sound gate for anything, and this comment becomes false the day that merges. Scott
// has allowed #554 to proceed **with this tripwire in place**, which is what this file is; he has also
// rejected closing the gap by refusing to spawn on an instance holding unshared memories, since a
// module may legitimately hold both and that would reject valid programs.
//
// **The remedy is decided, and half of it has landed — so this control's *message* changed while its
// trigger did not.** Decision [0056] takes the mark: `grow` refuses on a per-memory `noMove` flag
// rather than on `limits.Shared`, `allocate` sets it wherever it reserves, and `Spawn` walks the
// instance's memories before starting the first goroutine (#572 for the first two, #554 for the walk).
// The trigger is untouched, which means **this control has not been watched die under the new
// message**: the message is instructions for an author, not an assertion, and a changed message with
// an unchanged trigger permits and refuses exactly what it did before. Stated because *a re-pointed
// control has not been watched die* — the thing that would need re-watching is a trigger change, and
// there isn't one.
//
// # Why a `go` statement is the trigger
//
// A second observer needs a second stack. Inside this module the only way to get one is a `go`
// statement, so the trigger is syntactic and total rather than a guess about which function looks
// concurrent — *a comment's caller list is not the call graph*, and a predicate over names would miss
// the spawn helper nobody thought of. **The census is exactly zero today, module-wide**, which is what
// makes an allow-list unnecessary: there is no legitimate `go` in engine code to carve out, so the
// exemption side that a later author would argue with does not exist yet.
//
// It deliberately does **not** try to prove the spawned goroutine reaches linear memory. That would be
// a reachability question needing a call graph, it would be the thing an author could argue their way
// past, and it is not the question: any concurrent executor in this engine invalidates the *reason*
// `allocate` gives, whether or not the first one written happens to touch memory.
//
// # What it cannot see, stated rather than left to be discovered
//
// **An embedder calling `Invoke` on one instance from two goroutines.** Nothing in this tree documents
// whether that is permitted — `Instance` carries no concurrency contract either way — so "by
// construction" is today resting on an undocumented API promise, and no control here can assert
// anything about a caller outside the module. Writing that contract down is public-API-surface design,
// which §0 makes partisan and therefore Scott's and chat-Claude's rather than a test's to settle; it is
// flagged in the report that lands this file rather than decided here.
//
// Watched die by injection: a scratch non-test file containing a `go` statement, the FAIL read back,
// and the file removed in the same command — the method grave #561 paid for, since *a re-pointed
// control has not been watched die*.
//
// [0056]: ../../docs/decisions/0056-the-no-move-mark-is-set-where-the-reservation-happens-and-grow-refuses-on-the-mark-because-spawn-can-establish-it-while-one-thread-exists.md
func TestNothingInEngineCodeCreatesASecondObserver(t *testing.T) {
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
		// Full parse, not ImportsOnly: the subject is a statement, so the bodies are the domain.
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		scanned = append(scanned, rel)
		ast.Inspect(file, func(n ast.Node) bool {
			if g, isGo := n.(*ast.GoStmt); isGo {
				offenders = append(offenders, rel+":"+
					fset.Position(g.Go).String()[len(fset.Position(g.Go).Filename)+1:])
			}
			return true
		})
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
		t.Errorf("these non-test sites start a goroutine, and `internal/interp`'s `allocate` has "+
			"been assuming none exists: %v\n"+
			"`allocate` reserves the declared maximum only for a **shared** memory, on the stated "+
			"reason that \"an unshared memory has no second observer by construction\". A second "+
			"goroutine in engine code falsifies that, and the consequence is not a slow path but an "+
			"unshared memory whose backing array `grow` may move while another thread reads it — a "+
			"stale pointer paired with a fresh length (#556).\n"+
			"**The way out is no longer a choice: decision 0056 made it, and half of it has landed.** "+
			"`grow`'s refusal arm now tests a per-memory `noMove` mark instead of `limits.Shared`, and "+
			"`allocate` sets that mark wherever it reserves (#572). What is left is the half whose "+
			"oracle needs this goroutine to exist: before starting it, walk the instance's memories, "+
			"relocate any unreserved one onto a reserved backing array, and mark it — while exactly "+
			"one thread still exists, which is what makes the mark unreadable racily and the "+
			"relocation safe. Marking after the goroutine starts is option (C) that 0056 rejects as "+
			"unsound.\n"+
			"Two things this message no longer offers, because the ruling closed them. Reserving for "+
			"every memory *without* extending the refusal is not sufficient: the reservation is capped "+
			"at `sharedReservePages`, so it closes the hole below the cap and reopens it above. And "+
			"gating on `limits.Shared` was never available — T-1's `Spawn` runs the entry in the "+
			"*same* instance, so that flag does not answer the question, and refusing to spawn on an "+
			"instance holding unshared memories is separately rejected, since a module may "+
			"legitimately hold both. If you believe instead that this goroutine can reach no linear "+
			"memory at all, narrow this control's domain in the PR that adds it — say why, and do not "+
			"add the file to a list. #554 is the spawn; 0056 is the decision",
			offenders)
	}
}
