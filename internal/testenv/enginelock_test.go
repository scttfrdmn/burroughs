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
// The interval is computed from **positions, not control flow**, and there is one interval per
// `Lock`/`Unlock` pair *standing in the same statement list*: a lock opens a section, and only an unlock
// at the same block level closes it. An unlock one block deeper — the early release in an
// `if release == nil { w.mu.Unlock(); return }` — does **not** close the section, so a channel operation
// after that branch is still reported, which is the shape the conservative direction exists for. If any
// `Unlock` in the function is deferred, the section runs to the end of the function, as it always did.
//
// **This narrows a first-to-last rule, and the narrowing was compelled rather than chosen.** The
// original interval opened at a function's first `.Lock()` and closed at its *last* `.Unlock()`,
// over-reporting deliberately on the stated ground that *"a false positive is a comment away from being
// a narrowed rule"*. T-5's shutdown and join (#12, [ADR 0071]) are that comment arriving. Both are
// *lock → read the guarded state → unlock → block on a channel → lock → read what the wait produced*,
// and that is not a near-miss of B-MM-3 but the shape the clause **requires**: the second critical
// section is what lets the first one end before the blocking operation, and the channel operation
// between them is outside both. Under first-to-last the two sections merge and the compliant shape
// reports as four violations. A rule that cannot separate compliance from violation in the functions
// its clause is written for is not conservative but silent, because the only ways to satisfy it are to
// contort the engine or to exempt the file — and the message below forbids the exemption on a ground
// that applies to itself.
//
// What is **not** narrowed is the direction. Every shape the old interval caught, this one still
// catches, and one it caught by accident it now catches on purpose: `parkAtSafepoint` holds `w.mu`
// across neither of its two channel operations, but it passed the old rule only because its body-level
// unlock happened to be the textually last one in the function — put a third unlock after the receive
// and the old rule would have gone quiet on a real violation. Deleting that body-level unlock is one of
// the injections below, and the section then correctly runs past both channel operations.
//
// A false negative here is #10's litmus battery finding the hazard on a weakly-ordered platform, or not
// finding it, so each of the four injections below is run and read rather than argued.
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
// Watched die, against a committed baseline (grave #589's precondition), four injections — and the last
// two are the narrowed rule's own, because *a re-pointed control has not been watched die*: a message
// edit leaves the trigger where it was, and "it now permits the compliant shape" is a forecast to run,
// not a property to assert.
//
//  1. Restoring `Resume` to its first form — `defer w.mu.Unlock()` with `close(w.resume)` in the body —
//     fails naming `safepoint.go … Resume`. The deferred arm, untouched by the narrowing.
//  2. Blinding the `Lock` match fails the floor at `0 locked function(s)`, which is the failure mode
//     that would otherwise make the whole test vacuous now that the subject exists.
//  3. Moving `parkAtSafepoint`'s `<-release` above its body-level `w.mu.Unlock()` fails. This is the
//     violation the narrowing must still catch in a function whose unlocks are paired textually, and it
//     is the one the old rule would have caught for the wrong reason.
//  4. Deleting `parkAtSafepoint`'s body-level `w.mu.Unlock()`, leaving only the early release nested in
//     `if release == nil`, fails on **both** channel operations. This is the case a naive depth counter
//     would have got wrong — it would have paired the outer lock with the nested unlock, reached zero,
//     and gone quiet on a lock that is held for the rest of the function.
//
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
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
			sections := lockSections(fn)
			if len(sections) == 0 {
				continue
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
				if what == "" {
					return true
				}
				for _, s := range sections {
					if n.Pos() < s.lo || n.Pos() >= s.hi {
						continue
					}
					offenders = append(offenders, fmt.Sprintf("%s:%d in %s — %s inside the "+
						"critical section opened at line %d", rel,
						fset.Position(n.Pos()).Line, fn.Name.Name, what,
						fset.Position(s.lo).Line))
					break
				}
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
	// **Twenty-seven today; it said nine, and before that four.** Each figure was true when written
	// and false one slice later, which is the whole reason the number is restated at every touch
	// rather than left to be inferred from a floor that would have passed either way — and this is
	// the second restatement, so the pattern is the fact. The enumeration that used to stand here is
	// deleted rather than corrected: it named `leaveBlocked`, which #592 replaced with
	// `unmarkBlocked`, so a list of nine names had already rotted into eight names and a wrong one.
	// The floor's own message prints the population, which is the only copy that cannot go stale.
	//
	// **Raised from 9 to 20, on the file floor's argument one paragraph down.** A floor 3× below what
	// it bounds catches a `Lock` match that resolves *nothing* and cannot catch one that stops
	// resolving two thirds of the way — *an unasserted distance is the vacuum* — and the second
	// failure mode is the live one now that there are two mutexes and 27 sites. 20 keeps enough
	// headroom for a package's worth of locks to be refactored away without a false failure.
	// Measured with the instrument (999 forced into the floor, whose message prints the count), not
	// counted off the list by eye.
	//
	// A floor rather than an equality because a new lock is what this control should *judge* and not
	// refuse to look at.
	const lockedFuncsWhenWritten = 20
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

// lockSection is one critical section, as a half-open interval of source positions.
type lockSection struct{ lo, hi token.Pos }

// lockSections returns fn's critical sections under the pairing rule stated on
// TestNoEngineLockIsHeldAcrossAChannelOperation: a `Lock` pairs with an `Unlock` in the same statement
// list, and a lock left open at the end of a list runs to the end of the enclosing function body.
//
// **A deferred `Unlock` anywhere in the function keeps the whole-function interval, unnarrowed.** `defer`
// is precisely the case where the pairing is not textual — the unlock runs at a `}` the call is nowhere
// near — so pairing it by statement list would be pairing it by the wrong thing. That is also the arm the
// control's first violation was found on (`Resume`'s `defer w.mu.Unlock()` with `close(w.resume)` in the
// body), which is reason enough not to touch it while narrowing something else.
func lockSections(fn *ast.FuncDecl) []lockSection {
	if hasDeferredUnlock(fn.Body) {
		var lo token.Pos
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if name, ok := mutexCall(n); ok && !lo.IsValid() && (name == "Lock" || name == "RLock") {
				lo = n.Pos()
			}
			return true
		})
		if !lo.IsValid() {
			return nil
		}
		return []lockSection{{lo, fn.Body.End()}}
	}
	out := pairInList(fn.Body.List, fn.Body.End())
	// A function literal is its own scope: a lock left open inside one runs to the literal's `}` and not
	// to the enclosing function's. Its statements are reached only here, because `pairInList` descends
	// into control flow and never into an expression.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			out = append(out, pairInList(lit.Body.List, lit.Body.End())...)
		}
		return true
	})
	return out
}

// pairInList walks one statement list in order, opening a section at a `Lock` and closing it at an
// `Unlock` in that same list. A lock still open at the end runs to end, which is what makes the early
// release in a nested block fail to close the outer section — after a branch that locks and does not
// unlock, the lock is held, so that is the conservative answer and not a gap.
func pairInList(list []ast.Stmt, end token.Pos) []lockSection {
	var out []lockSection
	var open token.Pos
	for _, st := range list {
		if es, ok := st.(*ast.ExprStmt); ok {
			if name, ok := mutexCall(es.X); ok {
				switch name {
				case "Lock", "RLock":
					if !open.IsValid() {
						open = es.Pos()
					}
				case "Unlock", "RUnlock":
					if open.IsValid() {
						out = append(out, lockSection{open, es.End()})
						open = token.NoPos
					}
				}
				continue
			}
		}
		for _, nested := range nestedLists(st) {
			out = append(out, pairInList(nested, end)...)
		}
	}
	if open.IsValid() {
		out = append(out, lockSection{open, end})
	}
	return out
}

// nestedLists returns the statement lists st encloses, one level down. Expressions are not descended
// into: a `Lock` inside a function literal belongs to that literal's scope, and `lockSections` reaches it
// separately.
func nestedLists(st ast.Stmt) [][]ast.Stmt {
	switch s := st.(type) {
	case *ast.BlockStmt:
		return [][]ast.Stmt{s.List}
	case *ast.IfStmt:
		out := [][]ast.Stmt{s.Body.List}
		if s.Else != nil {
			out = append(out, []ast.Stmt{s.Else})
		}
		return out
	case *ast.ForStmt:
		return [][]ast.Stmt{s.Body.List}
	case *ast.RangeStmt:
		return [][]ast.Stmt{s.Body.List}
	case *ast.LabeledStmt:
		return [][]ast.Stmt{{s.Stmt}}
	case *ast.SwitchStmt:
		return clauseLists(s.Body)
	case *ast.TypeSwitchStmt:
		return clauseLists(s.Body)
	case *ast.SelectStmt:
		return clauseLists(s.Body)
	}
	return nil
}

// clauseLists returns the bodies of a switch's or select's clauses.
func clauseLists(body *ast.BlockStmt) [][]ast.Stmt {
	var out [][]ast.Stmt
	for _, cl := range body.List {
		switch c := cl.(type) {
		case *ast.CaseClause:
			out = append(out, c.Body)
		case *ast.CommClause:
			out = append(out, c.Body)
		}
	}
	return out
}

// mutexCall reports the lock-method name n calls, if n is a call of one.
//
// Matching the method name rather than the `sync` type is the predecessor's reasoning, kept: an aliased
// import evades a selector match on `sync.X`, and the author most likely to write one is an author
// working around this control.
func mutexCall(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Lock", "RLock", "Unlock", "RUnlock":
		return sel.Sel.Name, true
	}
	return "", false
}

// hasDeferredUnlock reports whether body defers a lock release anywhere, including inside a nested block.
// The predecessor looked only at the top-level statement list; widening it is the conservative direction,
// since a deferred unlock in a branch still runs at the function's `}`.
func hasDeferredUnlock(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		def, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		if name, ok := mutexCall(def.Call); ok && (name == "Unlock" || name == "RUnlock") {
			found = true
		}
		return true
	})
	return found
}
