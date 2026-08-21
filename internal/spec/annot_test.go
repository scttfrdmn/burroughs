package spec

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

// The three `((@a) module …)` commands in annotations.wast, by line. Named by line because that
// is the handle the board prints and the corpus pins; the classification assertion below checks
// the name and kind at each one, so a corpus revision that moves them fails loudly rather than
// silently probing three other modules.
var annotatedModules = []struct {
	line int
	name string
	// probe is a command appended after the module's own source, asking a question the corpus
	// never asks of it. Empty for none.
	probe string
}{
	{line: 98, name: "$m", probe: `(assert_return (invoke $m "f" (i32.const 1) (f32.const 1)))`},
	{line: 129, name: "$m1", probe: `(assert_return (invoke $m1 "f" (i32.const 1) (f32.const 1)))`},
	{line: 154, name: "$m2", probe: `(assert_return (invoke $m2 "f" (i32.const 1) (i64.const 2) (i32.const 3)) (i32.const 4))`},
}

// TestAnnotatedModulesInstantiate is the instantiation probe Scott made a condition of the #459
// stamp, and it exists because **the board cannot ask this question about these three vectors.**
//
// A `KindModuleText` row passes on decode plus validate. Instantiation runs, but an instantiation
// decline does *not* withhold the pass — it is remembered and carried forward to the vectors that
// depend on the module (#124's ruling, at the `remember(c, in, st, ierr, …)` call in the
// KindModuleText arm of Run). That is right for a module with dependents: one missing rule would
// otherwise go red across every vector downstream. But these three modules have **no dependent
// commands anywhere in annotations.wast** — nothing invokes them, nothing registers them, nothing
// reads an export — so there is no vector for a decline to travel to. All three would score
// `pass` with instantiation completely broken, and the board would read 74/74.
//
// So the witness the #459 measurement carried forward — `imports.wast: 166/166 pass`, therefore
// spectest globals/tables/memories/funcs instantiate — was not merely indirect. It was pointed at
// a column that cannot see the event. *A row that passes without asking is the same shape as a
// skip.*
//
// **The probe reuses the corpus's own bytes.** Each module's source is taken from the parsed
// command's own span rather than retyped here: a hand-copied 30-line annotation torture module
// would test this file's transcription of the vector, and would go stale against a suite bump
// without any signal. What is hand-written is only the appended question.
//
// $m2 is the load-bearing one of the three. It carries an active `elem` segment, an active `data`
// segment and a `start` function (`annotations.wast:193-203`), all of which execute during
// instantiation, and its exported `$f` computes `$x + p0` through a `block (result i32)` — so the
// probe reads a value back rather than checking that a call returned. $m and $m1 are the linking
// half: both import four `spectest` externs, one via inline import syntax and one via the folded
// form.
func TestAnnotatedModulesInstantiate(t *testing.T) {
	requireSuite(t)

	s, err := ParseFile(filepath.Join(suiteDir, "annotations.wast"))
	if err != nil {
		t.Fatalf("parse annotations.wast: %v", err)
	}

	// Fact 1: the three commands classify as named text modules at all. Before the annotation
	// change in this PR they were KindUnsupported with no head atom, so this half is the
	// classification assertion and the source extraction's precondition in one.
	byLine := map[int]Command{}
	for _, c := range s.Commands {
		byLine[c.Line] = c
	}
	var script bytes.Buffer
	for _, want := range annotatedModules {
		c, ok := byLine[want.line]
		if !ok {
			t.Fatalf("annotations.wast:%d: no command at this line; the corpus moved and this "+
				"probe is aimed at nothing", want.line)
		}
		if c.Kind != KindModuleText {
			t.Errorf("annotations.wast:%d: Kind = %v, want %v", want.line, c.Kind, KindModuleText)
			continue
		}
		if c.Name != want.name {
			t.Errorf("annotations.wast:%d: Name = %q, want %q", want.line, c.Name, want.name)
			continue
		}
		if len(c.Source) == 0 {
			t.Fatalf("annotations.wast:%d: empty Source span", want.line)
		}
		// A vacuity guard on the extraction: the span must actually be the annotated form, or
		// the probe below is instantiating some other module that happens to parse.
		if !bytes.HasPrefix(c.Source, []byte("((@")) {
			t.Errorf("annotations.wast:%d: Source does not begin with an annotation: %.20q",
				want.line, c.Source)
		}
		fmt.Fprintf(&script, "%s\n%s\n", c.Source, want.probe)
	}
	if t.Failed() {
		return
	}

	// Fact 2: instantiation, linking, the start function and the segments all succeed — asked
	// through the harness's own verbs on the board's own engine, so the path is the real one.
	probe, err := Parse("annotations-instantiation-probe.wast", script.Bytes())
	if err != nil {
		t.Fatalf("parse probe script: %v", err)
	}
	wantCommands := 2 * len(annotatedModules)
	if len(probe.Commands) != wantCommands {
		t.Fatalf("probe script has %d commands, want %d — the appended questions did not all "+
			"parse, and a short script would pass this test by asking less",
			len(probe.Commands), wantCommands)
	}
	r := run(probe)
	if r.Pass != wantCommands || r.Fail != 0 || r.Unsupported != 0 || r.Gated != 0 {
		t.Errorf("probe board: %d pass, %d fail, %d unsupported, %d gated; want %d/0/0/0",
			r.Pass, r.Fail, r.Unsupported, r.Gated, wantCommands)
		for key, fs := range r.Buckets {
			for _, f := range fs {
				t.Errorf("  %s:%d: %s", key, f.Line, f.Got)
			}
		}
	}
}
