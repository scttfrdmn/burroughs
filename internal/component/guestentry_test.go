// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// guestEntryKind classifies a site where this package enters guest code.
type guestEntryKind string

const (
	// entryTheCall is the lifted function itself: the call an embedder, or another component, asked for.
	entryTheCall guestEntryKind = "the lifted call itself"
	// entryThread is a component-model thread's entry, started by `thread.new-indirect` (ADR 0101).
	entryThread guestEntryKind = "a component-model thread's entry"
	// entryRealloc and entryPostReturn are the canonical-ABI adapter's two carve-outs from contract §5 H-2, as
	// amended on #694: on the calling agent's stack the adapter may invoke "exactly the `realloc` and
	// `post-return` functions named in the canon lower's options, and no other guest function".
	entryRealloc    guestEntryKind = "the canon options' realloc (H-2 carve-out)"
	entryPostReturn guestEntryKind = "the canon options' post-return (H-2 carve-out)"
)

// guestEntrySites is every site in this package that enters guest code, keyed
// `file.go:Func:receiver`, the receiver being the expression the `Invoke*` method is called on.
//
// **This table is H-2's carve-out made checkable (#1136).** The amendment allows the adapter exactly
// realloc and post-return. A new `Invoke*` site fails TestTheAdapterEntersOnlyItsNamedGuestFunctions until it
// is classified here, and a classification other than the four kinds is a contract question, not a diff's.
var guestEntrySites = map[string]guestEntryKind{
	"link_component.go:invokeSync:f.core.inst":           entryTheCall,
	"link_component.go:enterInvokeLowering:target.inst":  entryTheCall,
	"link_component.go:enterAndInvoke:target.inst":       entryTheCall,
	"cmthreads.go:run:t.entry":                           entryThread,
	"lowerargs.go:guestAlloc:f.reallocCore.inst":         entryRealloc,
	"link_component.go:invokeSync:f.postReturnCore.inst": entryPostReturn,
}

// TestTheAdapterEntersOnlyItsNamedGuestFunctions derives every `Invoke*` call in this package's non-test
// sources from the AST and requires it to be exactly guestEntrySites: none missing, none extra.
//
// It pins **where** the package enters guest code, not which function a given call reaches at run time. That a
// realloc site calls the realloc named in the options is the site's own code (`f.reallocCore` is set from the
// canon options). This control's subject is that no other site appears without being said out loud.
//
// NEUTER, run (2026-10-29, darwin/arm64): with the post-return row deleted from the table, the site is reported
// as unclassified.
func TestTheAdapterEntersOnlyItsNamedGuestFunctions(t *testing.T) {
	found := map[string]bool{}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !strings.HasPrefix(sel.Sel.Name, "Invoke") {
					return true
				}
				var recv strings.Builder
				if err := printer.Fprint(&recv, fset, sel.X); err != nil {
					t.Fatal(err)
				}
				found[name+":"+fn.Name.Name+":"+recv.String()] = true
				return true
			})
		}
	}
	if len(found) == 0 {
		t.Fatal("no Invoke* call found in this package, so this control asks nothing; the derivation is broken")
	}
	var unclassified, stale []string
	for k := range found {
		if _, ok := guestEntrySites[k]; !ok {
			unclassified = append(unclassified, k)
		}
	}
	for k := range guestEntrySites {
		if !found[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	for _, k := range unclassified {
		t.Errorf("%s enters guest code and is not in guestEntrySites. Classify it there. If it is the adapter "+
			"invoking a guest function other than the canon options' realloc or post-return, that breaks contract §5 "+
			"H-2's carve-out (#694), and it needs a contract decision before a diff", k)
	}
	for _, k := range stale {
		t.Errorf("%s is in guestEntrySites but no longer in the source: remove the row, or it reads as covered", k)
	}
}
