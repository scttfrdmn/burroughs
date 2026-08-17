// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestExampleWasmIsDerivedFromItsWat holds the committed example binary equal to the text it claims
// to come from.
//
// `examples/add/add.wasm` is in the tree so a fresh clone can run the README's transcript in one
// command, which makes it the same kind of artifact as the generated opcode table: committed output
// whose authority lives elsewhere. **Three provenance categories, and this one is derived** — the
// premise is `add.wat`, the inference is the engine's own assembler, and this test is what stops the
// inference from being asserted rather than checked. Regenerate with `go run ./examples/add/gen.go`.
//
// The failure is a real one to expect: the assembler's output is allowed to change (section order, a
// LEB width), and when it does this goes red and the artifact is re-derived rather than silently
// disagreeing with the source printed beside it in the docs.
func TestExampleWasmIsDerivedFromItsWat(t *testing.T) {
	src, err := os.ReadFile("examples/add/add.wat")
	if err != nil {
		t.Fatalf("the example's source is unreadable, so this test's subject was never reached: %v", err)
	}
	want, err := text.EncodeModule(src)
	if err != nil {
		t.Fatalf("assembling examples/add/add.wat: %v", err)
	}
	got, err := os.ReadFile("examples/add/add.wasm")
	if err != nil {
		t.Fatalf("the committed artifact is unreadable: %v", err)
	}
	if !bytes.Equal(got, want) {
		// Where, not just whether. The first version of this message reported the two lengths, which
		// on a one-byte mutation printed "97 bytes and 97" and left the reader with a contradiction
		// instead of a location — a diagnostic that establishes the verdict and withholds the
		// evidence. Found by falsifying it.
		at := len(got)
		for i := range min(len(got), len(want)) {
			if got[i] != want[i] {
				at = i
				break
			}
		}
		t.Errorf("examples/add/add.wasm (%d bytes) is not what add.wat assembles to (%d bytes): "+
			"first difference at offset %d, committed % #x, assembled % #x. The committed artifact "+
			"has drifted from its source — run: go run ./examples/add/gen.go",
			len(got), len(want), at, byteAt(got, at), byteAt(want, at))
	}
	// A vacuity floor, because two empty byte slices are equal and an assembler that returned nothing
	// would satisfy every line above. A module image cannot be shorter than its eight-byte preamble.
	if len(want) < 8 {
		t.Errorf("the assembler produced %d bytes, which is not a module image; this comparison "+
			"agreed about nothing", len(want))
	}
}

// byteAt reports the byte at i, or nothing when i is past the end — which is the case where one
// image is a prefix of the other, and where indexing would panic inside the diagnostic.
func byteAt(b []byte, i int) []byte {
	if i >= len(b) {
		return nil
	}
	return b[i : i+1]
}

// TestREADMEGoBlocksAreRealCode requires every fenced `go` block in README.md to appear, verbatim
// modulo indentation, inside a `.go` file this module builds.
//
// **A code sample nothing compiles is prose pretending to be tested.** The README's "Use from Go"
// section is the body of `Example` in example_test.go, whose printed output `go test` asserts; that
// is what makes the snippet a claim rather than an illustration, and this test is what keeps the two
// the same text. Editing either one alone goes red.
//
// Scoped to the *space* rather than to today's single block: any `go` block added to the README later
// is checked by the same walk, without anyone remembering to come here. Whitespace is normalized per
// line because a fenced block is dedented for reading while the function body it comes from is
// indented — the only difference the two are allowed to have.
func TestREADMEGoBlocksAreRealCode(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}

	blocks := fencedBlocks(string(readme), "go")
	if len(blocks) == 0 {
		t.Fatal("README.md holds no fenced `go` block, so this test checked nothing; either the " +
			"\"Use from Go\" section was removed or its fence's language tag was")
	}
	// The anchor: the README claims to document the Go surface with a snippet substantial enough to
	// copy, and a one-liner would satisfy the substring check below trivially. This is that claim,
	// asserted — it is the difference between "some go block exists" and "the section is there".
	longest := 0
	for _, b := range blocks {
		longest = max(longest, len(strings.Split(strings.TrimSpace(b), "\n")))
	}
	if longest < 15 {
		t.Errorf("the longest `go` block in README.md is %d lines; the \"Use from Go\" section is "+
			"supposed to carry a copyable example, and a short block passes the check below by "+
			"saying nothing", longest)
	}

	sources := goSources(t)
	for i, b := range blocks {
		norm := normalizeIndent(b)
		found := false
		for _, src := range sources {
			if strings.Contains(normalizeIndent(src.body), norm) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("README.md `go` block %d is in no .go file this module builds, so nothing "+
				"compiles it:\n%s", i+1, b)
		}
	}
}

type goSource struct {
	path string
	body string
}

// goSources reads every `.go` file in the module, which is the domain the check above claims.
//
// Derived by walking rather than listed, for the reason the coverage law names: a listed set is a
// claim about where code lives that stops being true the first time a package moves. The skipped
// directories are the ones holding material this module does not build — the vendored suite, the
// tool modfile's own world — and the floor below is the vacuity check on the walk itself.
func goSources(t *testing.T) []goSource {
	t.Helper()

	skip := map[string]bool{".git": true, "testdata": true, "third_party": true, "bin": true, ".claude": true}
	var out []goSource
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out = append(out, goSource{path: path, body: string(b)})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module for .go files: %v", err)
	}
	if len(out) < 20 {
		t.Fatalf("the walk found %d .go files, which is not this module; the comparison above "+
			"would be against nearly nothing", len(out))
	}
	return out
}

// fencedBlocks returns the bodies of the fenced blocks in doc whose info string is lang.
//
// Deliberately literal about the fence: three backticks at the start of a line, the language tag,
// and everything up to the next such line. Markdown admits more than that, and a parser that admits
// more than the file uses would be a second grammar to keep honest.
func fencedBlocks(doc, lang string) []string {
	var out []string
	var cur []string
	inside := false
	for line := range strings.SplitSeq(doc, "\n") {
		switch {
		case inside && strings.HasPrefix(line, "```"):
			out = append(out, strings.Join(cur, "\n"))
			cur, inside = nil, false
		case inside:
			cur = append(cur, line)
		case line == "```"+lang:
			inside = true
		}
	}
	return out
}

// normalizeIndent strips leading and trailing horizontal whitespace from every line.
//
// The one difference a fenced block and the function body it was taken from are allowed to have: the
// block is dedented so it reads as a program, the body carries the indentation of wherever it sits.
// Nothing else is normalized — a renamed identifier or a changed comment is a difference that should
// be reported, which is the whole point.
func normalizeIndent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}
