// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestErrNotValidatedHasNoProducer holds G-1's retirement (#1134, ADR 0043): ErrNotValidated has no call site in
// this package's engine code, so no module the validator would refuse is blamed through it. Each of its 78 code
// sites was refiled: classes A, B and D to ErrEngineInvariant, and class C's two to ErrEngineInvariant and
// ErrUnsupported.
//
// It reads the package with go/parser and counts identifier references, not text matches, so a comment naming
// the sentinel is not a producer. The one reference allowed is the declaration itself.
//
// **Neuter, run:** see the PR that landed this. With one refiled site put back on ErrNotValidated, this test
// names its file and line.
func TestErrNotValidatedHasNoProducer(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var uses []string
	files := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files++
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				// The declaration's own name is not a use. Its value is still walked.
				for _, v := range n.Values {
					ast.Inspect(v, func(m ast.Node) bool {
						if id, ok := m.(*ast.Ident); ok && id.Name == "ErrNotValidated" {
							uses = append(uses, fset.Position(id.Pos()).String())
						}
						return true
					})
				}
				return false
			case *ast.Ident:
				if n.Name == "ErrNotValidated" {
					uses = append(uses, fset.Position(n.Pos()).String())
				}
			}
			return true
		})
	}
	if files < 40 {
		t.Fatalf("parsed %d engine files, want at least 40 (46 when written): a walk over the wrong directory "+
			"finds no uses", files)
	}
	if len(uses) != 0 {
		t.Errorf("ErrNotValidated has %d producer(s) in engine code:\n  %s\nG-1's carve-out retired with its "+
			"last call site (#1134). A check the validator rules out reports ErrEngineInvariant", len(uses),
			strings.Join(uses, "\n  "))
	}
}
