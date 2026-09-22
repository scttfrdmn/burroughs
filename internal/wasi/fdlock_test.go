// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestFDTableIsSafeUnderConcurrentHostCalls is the lock's witness (#813). Until #813 the fd table's
// safety rested on `GuestFeatures()` having `Threads` off — the front door made host calls sequential —
// and [Config.Features] removes that guarantee, so the table needs the lock the source comment demanded
// "in the same change".
//
// **What this does NOT witness, stated because the gap is the interesting part.** The real subject is
// two guest agents doing I/O at once, and no committable guest can produce that: it needs
// `GOEXPERIMENT=burroughsspawn`, a `burroughs spawn` import this package cannot supply, and a ~6MB test
// binary. So this drives the table through the accessors and one real handler from several goroutines
// instead — the same memory operations in the same order, without the guest. It is the honest unit for
// a data race whose guest-level witness lives in Phase 4's fork, and the trigger for replacing it is a
// public import-registration surface (#804), which would let a threaded guest reach this host at all.
//
// It is only a witness under `-race`; without it a data race on a Go map is silently sometimes-fine.
// `make check` runs the suite with the detector on.
func TestFDTableIsSafeUnderConcurrentHostCalls(t *testing.T) {
	h := &host{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	if err := h.initFDs(nil); err != nil {
		t.Fatalf("initFDs: %v", err)
	}

	const workers, each = 8, 200
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				// A writer: allocate an entry, then remove it. `addFD` and `dropFD` both mutate the map
				// and the counter, which is where an unlocked table corrupts rather than merely races.
				fd := h.addFD(&fdEntry{writer: io.Discard})
				// A reader on a *stable* fd, concurrent with other workers' mutations. This is the read
				// that an unlocked map turns into a concurrent-map-read-and-write fatal error.
				if e := h.fd(1); e == nil {
					t.Error("fd 1 (stdout) vanished from the table")
				}
				// A real handler, so at least one of these calls is on the path a guest reaches rather
				// than only on the accessors. fdClose ignores its Caller, which is what makes it
				// callable here without a guest instance.
				if _, err := h.fdClose(nil, []interp.Value{interp.I32(int32(fd))}); err != nil {
					t.Errorf("fdClose: %v", err)
				}
				if !h.dropFD(fd) {
					// fdClose only drops an entry holding a *file; this one holds a writer, so the drop
					// here is the one that removes it. If that ever changes, this line is the tell.
					t.Errorf("fd %d was already gone", fd)
				}
			}
		}()
	}
	wg.Wait()

	// The table is back to its three stdio entries: every allocation was removed. A count, not a
	// spot-check, because the failure mode of a racing map is a *lost* write, which a spot-check on a
	// surviving key cannot see.
	if got := len(h.fds); got != 3 {
		t.Errorf("table holds %d entries after all workers finished, want 3 (stdio only) — a lost "+
			"delete or a lost insert", got)
	}
}

// TestFDTableIsReachedThroughAccessors is the rule's control: the map is reached through `fd`,
// `addFD` and `dropFD`, so the lock cannot be bypassed by a new call site that simply indexes it.
//
// **Named after the rule rather than the current state**, because a state-assertion ("there are three
// direct accesses") gets falsified by correct work, while the rule survives it.
//
// **The domain is derived, not listed:** every `.go` file in this package that is not a test, and every
// function in it. A hand list of today's files would silently stop covering a file added tomorrow.
// The containing function is resolved with the **parser**, because a line scanner answers the wrong
// scope — it cannot tell a use inside `fd` from one in the function printed above it.
func TestFDTableIsReachedThroughAccessors(t *testing.T) {
	// The accessors themselves, plus initFDs, which builds the table before `_start` runs and so has no
	// second party to lock against (its own comment states that, and this is the list that comment
	// promises exists).
	allowed := map[string]bool{"fd": true, "addFD": true, "dropFD": true, "initFDs": true}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned, violations := 0, 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		scanned++
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, perr := parser.ParseFile(fset, f, src, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", f, perr)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "fds", "nextFD":
				default:
					return true
				}
				if allowed[name] {
					return true
				}
				violations++
				t.Errorf("%s: %s touches h.%s directly; the fd table is reached through fd/addFD/dropFD "+
					"so the lock cannot be bypassed (add an accessor, or justify an exemption in the "+
					"struct's comment and here)", fset.Position(sel.Pos()), name, sel.Sel.Name)
				return true
			})
		}
	}

	// A floor on the scan itself: a glob that matched nothing, or a parser change that silently stopped
	// finding function bodies, would make this test pass by seeing nothing at all.
	if scanned < 2 {
		t.Fatalf("scanned %d non-test files in this package; the domain derivation is broken, so a pass "+
			"here means nothing", scanned)
	}
	if violations == 0 {
		t.Logf("scanned %d files, no direct access outside %v", scanned, keys(allowed))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
