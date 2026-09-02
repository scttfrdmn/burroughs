// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestNoEngineLockIsHeldAcrossAChannelOperation is contract §4 **B-MM-3**, and it is the successor to a
// tripwire whose subject arrived.
//
// B-MM-3: the engine *"MUST NOT hold engine-internal locks across a guest resume, and MUST NOT resume a
// guest agent in a state where a previously acquired guest lock is held without the acquire edge of
// B-MM-1 having been established […] the contract closes it for every field, not per-field."*
//
// # What this replaces, and why the replacement is not a deletion
//
// The predecessor asserted that **no non-test file imports `sync`**, on the honest ground that B-MM-3
// had no subject: with no locks in the tree, a test asserting "no lock is held across a resume" is *an
// analytic zero*, so what was worth building was the thing that fires when the subject arrives, carrying
// its instruction to the author who would need it. It fired, on `internal/interp/safepoint.go`'s
// `world.mu` — #515's stop-the-world state — and it was right to: the first draft of `Resume` closed the
// release channel under `defer w.mu.Unlock()`, and **`close` is the guest resume**, so that was B-MM-3's
// prohibited shape exactly.
//
// Its own message named the two ways out — prove the lock is outside the hazard, or *"narrow this
// control's domain in the PR that adds it and say why"* — and neither is what happened, because both
// were written for a **harness** package needing `sync`. This is engine code, and B-MM-3 is about
// engine code, so narrowing the domain would have exempted the one file the clause is for. *A tripwire
// whose subject arrives is re-pointed, not retired*: the import scan is replaced by an assertion of the
// rule the import made checkable, and the domain is not narrowed at all.
//
// # The rule, as a syntactic interval
//
// A lock's critical section must contain **no channel operation** — no send, no receive, no `close`.
// That covers both halves of the hazard with one predicate, which is why it is the rule rather than
// "no `close` across a resume":
//
//   - `close` under a lock is a resume under a lock, B-MM-3's letter.
//   - a *receive* under a lock is worse than a violation, it is a deadlock: the goroutine that would
//     release it needs the same mutex.
//   - a *send* under a lock is the same hazard behind a buffer-size argument, and a buffer-size
//     argument is exactly the kind that a later change falsifies silently (SP-4 makes the thread
//     membership `Stop` sizes its channel from dynamic).
//
// The interval is computed from **positions, not control flow**: it opens at the first `.Lock()` and
// closes at the last `.Unlock()`, or at the end of the function if any `Unlock` is deferred. That is
// deliberately conservative — it over-reports rather than under-reports, treating everything between a
// function's first lock and its last unlock as locked even where a branch has released early — because
// the failure being priced is a hazard and the safe direction for a hazard control is to complain too
// much. A false positive is a comment away from being a narrowed rule; a false negative is #10's litmus
// battery finding it on a weakly-ordered platform, or not finding it.
//
// **Matching `.Lock()` by method name rather than `sync.Mutex` by type is what the predecessor's own
// reasoning bought.** An aliased import — `import mu "sync"` — evades a selector match on `sync.X`
// completely, and the author most likely to write one is an author working around a control. A method
// name cannot be aliased.
//
// # The domain has no exemptions, deliberately
//
// Every non-test `.go` file in the tree, because the resume is not confined to `internal/interp`: the
// public wrapper in `burroughs.go` calls `Invoke`, so a lock held there would be a lock held across a
// guest resume, and scoping this to the interpreter package would inherit exactly today's blind spot.
//
// Watched die, against a committed baseline (grave #589's precondition): restoring `Resume` to its first
// form — `defer w.mu.Unlock()` with `close(w.resume)` in the body — fails naming
// `safepoint.go … Resume`, and blinding the `Lock` match fails the floor at `0 locked function(s)`,
// which is the failure mode that would otherwise make the whole test vacuous now that the subject
// exists.
func TestNoEngineLockIsHeldAcrossAChannelOperation(t *testing.T) {
	var offenders, locked []string
	scanned := 0
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
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			return rerr
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		scanned++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			lo, hi, deferred := token.NoPos, token.NoPos, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Lock", "RLock":
					if !lo.IsValid() {
						lo = call.Pos()
					}
				case "Unlock", "RUnlock":
					hi = call.End()
				}
				return true
			})
			if !lo.IsValid() {
				continue
			}
			// A deferred unlock makes the rest of the body the critical section, so the interval
			// runs to the end of the function. Same for a lock with no unlock at all, which is
			// either a leak or a helper this control should be complaining about anyway.
			for _, st := range fn.Body.List {
				if def, ok := st.(*ast.DeferStmt); ok {
					if sel, ok := def.Call.Fun.(*ast.SelectorExpr); ok {
						if n := sel.Sel.Name; n == "Unlock" || n == "RUnlock" {
							deferred = true
						}
					}
				}
			}
			if deferred || !hi.IsValid() {
				hi = fn.Body.End()
			}
			site := fmt.Sprintf("%s:%d %s", rel, fset.Position(fn.Pos()).Line, fn.Name.Name)
			locked = append(locked, site)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					return false
				}
				var what string
				switch node := n.(type) {
				case *ast.SendStmt:
					what = "a channel send"
				case *ast.UnaryExpr:
					if node.Op == token.ARROW {
						what = "a channel receive"
					}
				case *ast.CallExpr:
					if id, ok := node.Fun.(*ast.Ident); ok && id.Name == "close" {
						what = "a close()"
					}
				}
				if what == "" || n.Pos() < lo || n.Pos() >= hi {
					return true
				}
				offenders = append(offenders, fmt.Sprintf("%s:%d in %s — %s inside the critical "+
					"section opened at line %d", rel, fset.Position(n.Pos()).Line, fn.Name.Name,
					what, fset.Position(lo).Line))
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", repoRoot, err)
	}
	sort.Strings(offenders)
	sort.Strings(locked)

	// Two vacuity floors, because the walk and the match fail for unrelated reasons. The file count
	// catches a walk that stopped reading the tree; the locked-function count catches a `Lock` match
	// that stopped resolving, which is now a live failure mode rather than a hypothetical — before
	// #515 there were no locks at all and this second floor could not have been written.
	//
	// **The file floor is raised from 40 to 100, and the measured population is 111.** A floor 2.8×
	// below what it bounds catches a walk that returns *nothing* and cannot catch one that stops
	// early — the failure mode a `SkipDir` mistake produces — so the distance was the vacuum. 100
	// keeps enough headroom for a package to be deleted without a false failure and still fires on a
	// walk that loses a tenth of the tree. Measured by the instrument (999 forced into the floor
	// below, whose message prints the population), not counted by eye.
	const engineFilesWhenWritten = 100
	if scanned < engineFilesWhenWritten {
		t.Fatalf("scanned %d non-test .go file(s) under %s, want at least %d — the walk is not "+
			"reading the tree, so the assertion below asserted its property of nothing and passed",
			scanned, repoRoot, engineFilesWhenWritten)
	}
	// **Nine today, and it said four.** `safepoint.go`'s `register`, `Stop`, `Resume`,
	// `parkAtSafepoint`, `enterBlocked` and `leaveBlocked`, plus `futex.go`'s `enqueueIfEqual`,
	// `resolveExpiry` and `detach` — #543's wait queue is the second mutex in engine code and it
	// arrived one slice after the first. The sentence naming four was true when written and false
	// after the next slice landed, which is why the count is re-measured here rather than left to be
	// inferred from a floor that would have passed either way.
	//
	// A floor rather than an equality because a new lock is what this control should *judge* and not
	// refuse to look at; the exact number is stated beside it because a floor alone catches a moved
	// file and never a silent partial loss.
	const lockedFuncsWhenWritten = 9
	if len(locked) < lockedFuncsWhenWritten {
		t.Fatalf("found %d locked function(s) across %d files (%v), and there were %d when this was "+
			"written. Below the floor means the `.Lock()` match has stopped resolving, so every "+
			"interval below is empty and the assertion passes by asking nothing",
			len(locked), scanned, locked, lockedFuncsWhenWritten)
	}

	if len(offenders) != 0 {
		t.Errorf("these critical sections contain a channel operation, and contract §4 B-MM-3 "+
			"forbids it:\n  %s\n"+
			"B-MM-3 closes the hazard for every field rather than per-field: an engine-internal lock "+
			"must not be held across a guest resume, and `close` on a release channel *is* the "+
			"resume. Move the operation after the unlock — clearing the guarded state under the lock "+
			"first, so a released thread cannot re-park for the round it was released from. A "+
			"receive here is a deadlock rather than a violation, since the goroutine that would "+
			"release the lock needs it. A send here is safe only by an argument about a buffer size, "+
			"which is the kind a later change falsifies silently.\n"+
			"If the lock is genuinely outside the hazard, narrow the *rule* in the PR that needs it "+
			"and say why — do not add a name to a list, because an exemption inherits none of this "+
			"control's lessons. Decision 0052, #516, ADR 0059, and #10 is the battery that would "+
			"catch getting this wrong on a weakly-ordered platform.\n"+
			"Locked functions considered: %v", strings.Join(offenders, "\n  "), locked)
	}
}
