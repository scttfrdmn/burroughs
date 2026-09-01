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
	// `TestNoSyncPrimitiveIsUsedInEngineCode`, which walks the same population.
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
			"stale pointer paired with a fresh length (#556). Before this lands, one of: (1) reserve "+
			"for every memory an executing instance can reach, not just the shared ones; (2) track "+
			"reachability **per memory** rather than per `limits.Shared` flag, which is the shape #567 "+
			"names for its scoped option; or (3) show this goroutine can reach no linear memory at "+
			"all, and narrow this control's domain in the PR that adds it — say why, and do not add "+
			"the file to a list. What is **not** available is gating on `limits.Shared`: T-1's "+
			"`Spawn` runs the entry in the *same* instance, so that flag does not answer the "+
			"question. #554 is the spawn, #567 is the decision, and refusing to spawn on an instance "+
			"holding unshared memories is already rejected — a module may legitimately hold both",
			offenders)
	}
}
