// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// publicSentinels binds the public package's error classifications to their names, so the exit-code
// taxonomy can be checked against the set it claims to cover.
//
// A binding of a derived domain, not an enumeration of one: TestExitCodesCoverEveryPublicSentinel
// checks these keys against the declarations in the package's own source, so a fifth sentinel added
// upstream fails there rather than silently falling through this switch to exitError — which is the
// failure mode the codes exist to prevent, wearing the disguise of a working CLI.
//
// ErrGated is here because this guard put it here: the sentinel landed in the library first and
// this test failed with `the public package declares ErrGated and this taxonomy does not classify
// it: it would exit 1`. That is the whole claim the derived domain makes, made by the instrument
// rather than about it — the addition was caught by the walk, named, and priced, with no one
// remembering to come here.
var publicSentinels = map[string]error{
	"ErrMalformed":   burroughs.ErrMalformed,
	"ErrInvalid":     burroughs.ErrInvalid,
	"ErrDeclined":    burroughs.ErrDeclined,
	"ErrUnsupported": burroughs.ErrUnsupported,
	"ErrGated":       burroughs.ErrGated,
}

// declaredSentinels reads the exported `Err*` variables out of the public package's source.
//
// The same shape convert_test.go's AST walk uses on `internal/binary`'s named types, for the same
// reason: the authority is the declaration, and a domain typed out beside the table it checks cannot
// notice an addition. The floor below is the vacuity check — a moved declaration or a renamed file
// yields an empty set, which would make every comparison here an agreement between nothings.
func declaredSentinels(t *testing.T) []string {
	t.Helper()

	const path = "../../burroughs.go"
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var names []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.IsExported() && strings.HasPrefix(name.Name, "Err") {
					names = append(names, name.Name)
				}
			}
		}
	}
	slices.Sort(names)
	if len(names) < 4 {
		t.Fatalf("found %d exported sentinels in %s (%v); the declarations moved, so this test "+
			"is measuring nothing", len(names), path, names)
	}
	return names
}

// TestExitCodesCoverEveryPublicSentinel is the coverage claim the taxonomy makes about itself: every
// classification the library can return has a code of its own.
//
// **Coverage is a claim, and this is the one an exit-code switch cannot check about itself.** A
// sentinel with no arm returns exitError — indistinguishable from an unreadable file — so the failure
// is a script drawing the wrong conclusion, not a crash. Both directions are checked: a declared
// sentinel with no binding, and a binding for something no longer declared.
func TestExitCodesCoverEveryPublicSentinel(t *testing.T) {
	declared := declaredSentinels(t)
	for _, name := range declared {
		if _, ok := publicSentinels[name]; !ok {
			t.Errorf("the public package declares %s and this taxonomy does not classify it: it "+
				"would exit %d, which means \"this invocation's own failure\"", name, exitError)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(publicSentinels)) {
		if !slices.Contains(declared, name) {
			t.Errorf("this taxonomy binds %s, which the public package no longer declares", name)
		}
	}

	// And each one classifies as something other than the catch-all, wrapped the way the library
	// wraps it — a sentinel matched only when it is the error *itself* would pass a bare comparison
	// and fail on every real return.
	want := map[string]int{
		"ErrMalformed":   exitRefused,
		"ErrInvalid":     exitRefused,
		"ErrDeclined":    exitRefused,
		"ErrUnsupported": exitUnsupported,
		// Deliberately *not* exitRefused, which is grave #301 stated as a number: a gated module is
		// well-formed, so a caller told "refused" would go looking for the defect in their module.
		"ErrGated": exitGated,
	}
	for name, sentinel := range publicSentinels {
		got := exitCode(fmt.Errorf("wrapped: %w", sentinel))
		if got == exitError {
			t.Errorf("%s exits %d, the catch-all", name, got)
		}
		if w, ok := want[name]; ok && got != w {
			t.Errorf("%s exits %d, want %d", name, got, w)
		}
	}

	// The codes are distinct, which is the whole point of having more than one.
	codes := []int{exitOK, exitError, exitUsage, exitRefused, exitTrap, exitUnsupported, exitGated}
	if slices.Compact(slices.Sorted(slices.Values(codes)))[0] != exitOK ||
		len(slices.Compact(slices.Sorted(slices.Values(codes)))) != len(codes) {
		t.Errorf("the exit codes are not distinct: %v", codes)
	}
	if exitCode(nil) != exitOK {
		t.Errorf("success exits %d", exitCode(nil))
	}
	if exitCode(errUsage) != exitUsage {
		t.Errorf("a usage error exits %d", exitCode(errUsage))
	}
	if exitCode(&burroughs.Trap{Reason: "unreachable"}) != exitTrap {
		t.Errorf("a trap exits %d", exitCode(&burroughs.Trap{Reason: "unreachable"}))
	}
}

// writeModule assembles a module and writes it where the CLI can read it, since `run` takes a path.
func writeModule(t *testing.T, name, src string) string {
	t.Helper()
	wasm, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("assembling the fixture failed, so this test's subject was never reached: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunSubcommandOutcomes exercises the CLI the way a user does — through argv, reading stdout and
// stderr — over every outcome the library can produce.
//
// **The stream each message lands on is asserted, not just its presence.** A decline printed to stdout
// would corrupt the output of anyone piping a result, and a result printed to stderr would be invisible
// to them; both are "the text appeared somewhere" to a test that concatenates the two, which is why
// this one does not.
func TestRunSubcommandOutcomes(t *testing.T) {
	valid := writeModule(t, "valid.wasm", `(module
		(func (export "answer") (result i32) i32.const 42)
		(func (export "add") (param i32 i32) (result i32) local.get 0 local.get 1 i32.add)
		(func (export "divz") (param i32) (result i32) local.get 0 i32.const 0 i32.div_s)
		(func (export "nothing"))
		(memory (export "mem") 1))`)
	// `ref.null`/`ref.is_null`: implemented by the interpreter, not yet typed by the validator.
	// Re-pointed by slice 5, which types the previous specimen `memory.size` — see the note on
	// decliningWAT in the root package for why this specimen is a schedule rather than a risk.
	declining := writeModule(t, "declining.wasm",
		`(module (func (export "isnull") (result i32) (ref.is_null (ref.null func))))`)
	invalid := writeModule(t, "invalid.wasm", `(module (func (export "f") (result i32) i64.const 42))`)
	gated := writeModule(t, "gated.wasm", `(module (memory i64 1))`)
	malformed := filepath.Join(t.TempDir(), "malformed.wasm")
	if err := os.WriteFile(malformed, []byte("not a wasm module"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		argv       []string
		code       int
		stdout     string
		stderrHas  []string
		stdoutHas  []string
		emptyOut   bool
		stderrNone bool
	}{
		"call an export": {
			argv: []string{valid, "add", "i32:2", "i32:40"}, code: exitOK,
			stdout: "i32:42\n", stderrNone: true,
		},
		"unsigned spelling of a negative i32": {
			argv: []string{valid, "add", "i32:4294967295", "i32:1"}, code: exitOK,
			stdout: "i32:0\n",
		},
		"a function with no results prints nothing": {
			argv: []string{valid, "nothing"}, code: exitOK, emptyOut: true, stderrNone: true,
		},
		"list exports": {
			argv: []string{valid}, code: exitOK,
			stdout: "answer\nadd\ndivz\nnothing\n", stderrNone: true,
		},
		"a decline runs and reports on stderr": {
			argv: []string{declining, "isnull"}, code: exitOK,
			stdout:    "i32:1\n",
			stderrHas: []string{"ref_null", "--strict"},
		},
		"--strict refuses a decline": {
			argv: []string{"--strict", declining, "isnull"}, code: exitRefused,
			emptyOut: true, stderrHas: []string{"ref_null"},
		},
		"invalid is refused, naming the rule": {
			argv: []string{invalid, "f"}, code: exitRefused,
			emptyOut: true, stderrHas: []string{"type mismatch"},
		},
		"malformed is refused": {
			argv: []string{malformed}, code: exitRefused, emptyOut: true,
		},
		// Grave #301 at the shell: a code of its own, and the message names the proposal so the
		// remedy — a gate flip, not an edit to the module — is legible without reading this source.
		"a gated proposal has its own code": {
			argv: []string{gated}, code: exitGated, emptyOut: true,
			stderrHas: []string{"memory64", "gate"},
		},
		"a trap has its own code": {
			argv: []string{valid, "divz", "i32:1"}, code: exitTrap,
			emptyOut: true, stderrHas: []string{"integer divide by zero"},
		},
		"an unspellable argument is the caller's error": {
			argv: []string{valid, "add", "i32:1", "bogus:2"}, code: exitError,
			emptyOut: true, stderrHas: []string{"bogus"},
		},
		"an unknown export is the caller's error": {
			argv: []string{valid, "nosuch"}, code: exitError, emptyOut: true,
		},
		"a missing file is the caller's error": {
			argv: []string{filepath.Join(t.TempDir(), "absent.wasm")}, code: exitError, emptyOut: true,
		},
		"no arguments is a usage error": {
			argv: nil, code: exitUsage, emptyOut: true, stderrHas: []string{"usage: burroughs run"},
		},
	} {
		var out, errOut bytes.Buffer
		code := exitCode(runCmd(&out, &errOut, tc.argv))
		if code != tc.code {
			t.Errorf("%s: exit %d, want %d (stderr: %s)", name, code, tc.code, errOut.String())
		}
		if tc.stdout != "" && out.String() != tc.stdout {
			t.Errorf("%s: stdout %q, want %q", name, out.String(), tc.stdout)
		}
		if tc.emptyOut && out.Len() != 0 {
			t.Errorf("%s: stdout is %q, want nothing", name, out.String())
		}
		for _, want := range tc.stdoutHas {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s: stdout %q lacks %q", name, out.String(), want)
			}
		}
		for _, want := range tc.stderrHas {
			if !strings.Contains(errOut.String(), want) {
				t.Errorf("%s: stderr %q lacks %q", name, errOut.String(), want)
			}
		}
		if tc.stderrNone && errOut.Len() != 0 {
			t.Errorf("%s: stderr is %q, want nothing — a successful run has no diagnostics", name, errOut.String())
		}
	}
}

// TestRunPrintsResultsItCanReadBack closes the CLI's own round trip: the spelling `run` prints is the
// spelling `run` accepts.
//
// Asserted through the CLI rather than only through ParseValue, because that is where the property is
// *used* — the library round trip can hold while the CLI prints results through some other path. Same
// reason the coverage law names how a thing is called as a dimension of its own.
func TestRunPrintsResultsItCanReadBack(t *testing.T) {
	path := writeModule(t, "echo.wasm", `(module
		(func (export "i32") (param i32) (result i32) local.get 0)
		(func (export "i64") (param i64) (result i64) local.get 0)
		(func (export "f32") (param f32) (result f32) local.get 0)
		(func (export "f64") (param f64) (result f64) local.get 0))`)

	for _, arg := range []string{
		"i32:42", "i32:-1", "i64:-9223372036854775808", "i64:9223372036854775807",
		"f32:1.5", "f32:-inf", "f32:nan", "f32:nan:0x200000",
		"f64:1.5", "f64:inf", "f64:nan", "f64:nan:0x4000000000000",
	} {
		typ, _, _ := strings.Cut(arg, ":")
		var out, errOut bytes.Buffer
		if err := runCmd(&out, &errOut, []string{path, typ, arg}); err != nil {
			t.Errorf("run %s %s: %v", typ, arg, err)
			continue
		}
		if got := strings.TrimSpace(out.String()); got != arg {
			t.Errorf("run %s %s printed %q; the CLI cannot read back what it prints", typ, arg, got)
		}
	}
}
