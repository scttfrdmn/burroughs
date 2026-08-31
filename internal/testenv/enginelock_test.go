// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestNoSyncPrimitiveIsUsedInEngineCode is contract §4 **B-MM-3**'s tripwire, and it is a tripwire
// rather than a check because the clause currently has no subject.
//
// B-MM-3: the engine *"MUST NOT hold engine-internal locks across a guest resume, and MUST NOT resume
// a guest agent in a state where a previously acquired guest lock is held without the acquire edge of
// B-MM-1 having been established […] the contract closes it for every field, not per-field."*
//
// # Its green today says nothing, and that is stated rather than hidden
//
// There are no locks. A test asserting "no lock is held across a resume" over a tree with no locks is
// *an analytic zero* — it could not have come out otherwise — and decision 0052 declines to dress one
// up as coverage. What is worth building is the thing that fires when the subject arrives, carrying
// the instruction to the author who needs it. **The value of this control is entirely in its failure
// message**, and the domain is non-empty (asserted below) so that the failure can actually happen.
//
// # Why the import path and not a `sync.Mutex` selector
//
// An aliased import — `import mu "sync"` — evades a selector match on `sync.X` completely, and the
// author most likely to write one is an author working around a control. The import path cannot be
// spelled two ways. `sync/atomic` is a different path, so decision 0052's own mechanism (an
// `atomic.Uint64` in `internal/interp/boundary.go`) is not in this domain: atomics are the *answer* to
// B-MM-1, and B-MM-3 is about locks.
//
// # The domain has no exemptions, deliberately
//
// Every non-test `.go` file in the tree, because the resume is not confined to `internal/interp`: the
// public wrapper in `burroughs.go` calls `Invoke`, so a lock held there would be a lock held across a
// guest resume, and scoping this control to the interpreter package would inherit exactly today's
// blind spot. A harness package that legitimately needs `sync` is a reason to **narrow this domain in
// a PR that says so**, on the record, and not a reason to add a name to a list — *an exemption
// inherits none of the trigger's lessons*, and the exemption side is written later, by someone who is
// arguing with the instrument.
//
// Watched die by injection, the method grave **#561** paid for: a scratch non-test file importing
// `"sync"`, the FAIL read back, and the file removed in the same command.
func TestNoSyncPrimitiveIsUsedInEngineCode(t *testing.T) {
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
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			return rerr
		}
		fset := token.NewFileSet()
		// ImportsOnly: the question is answered by the import block, and parsing bodies would make
		// this the slowest control in the package for nothing.
		file, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		scanned = append(scanned, rel)
		for _, imp := range file.Imports {
			if imp.Path != nil && imp.Path.Value == `"sync"` {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", repoRoot, err)
	}
	sort.Strings(offenders)

	// The vacuity check, and it is not decoration: a walk that found no Go files would report no
	// offenders and pass, which is indistinguishable from a clean tree. The floor is well below the
	// real count so that adding or moving packages never touches it, and well above zero.
	const engineFilesWhenWritten = 40
	if len(scanned) < engineFilesWhenWritten {
		t.Fatalf("scanned %d non-test .go file(s) under %s, want at least %d — the walk is not "+
			"reading the tree, so the assertion below asserted its property of nothing and passed",
			len(scanned), repoRoot, engineFilesWhenWritten)
	}

	if len(offenders) != 0 {
		t.Errorf("these non-test files import `sync`, and contract §4 B-MM-3 has an opinion about "+
			"the first one: %v\n"+
			"B-MM-3 forbids holding an engine-internal lock across a guest resume, and closes the "+
			"hazard for every field rather than per-field. So the lock this import introduces needs, "+
			"before it lands: (1) proof it is never held across a call that enters the interpreter — "+
			"`Invoke`, `Instantiate`, or anything reaching `internal/interp`'s `enterGuest` sites; "+
			"and (2) B-MM-1's acquire edge established on any resume that follows releasing it, which "+
			"`internal/interp/boundary.go` provides. If the lock is genuinely outside that hazard, "+
			"narrow this control's domain in the PR that adds it and say why — do not add the file to "+
			"a list. Decision 0052, #516, and #10 is the battery that would catch getting this wrong "+
			"on a weakly-ordered platform", offenders)
	}
}
