// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRefciteResolvesEachCitationAtItsPin witnesses `scripts/refcite.py`, the check that a citation into
// the Canonical ABI reference names a symbol that exists at its pin (the chair's ruling of 2026-10-17).
//
// # Why a synthetic reference
//
// The subject is the checker, not the tree's citations — `make refcite` asks the tree's question against the
// vendored pins. A synthetic `definitions.py` makes each arm's answer a property of its input: a symbol the
// fixture defines, or one it does not.
//
// # The neuter the ruling names, and the arms around it
//
// A citation to a symbol that does not exist must fail. Without the accepting arm that would be satisfied by
// a checker that refuses everything, and without the quote arm a quote copied from the wrong function would
// pass on the strength of its symbol.
func TestRefciteResolvesEachCitationAtItsPin(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "refcite.py")
	if _, statErr := os.Stat(script); statErr != nil {
		t.Fatalf("refcite.py is missing, so this witness has no subject: %v", statErr)
	}

	const (
		pin    = "1111111111111111111111111111111111111111"
		record = "2222222222222222222222222222222222222222"
	)
	current := "X = 1\n" +
		"assert(X > 0)\n" +
		"class Task:\n" +
		"  LIMIT = 3\n" +
		"  def cancel(self):\n" +
		"    trap_if(self.state ==   1)\n" +
		"    return None\n" +
		"def canon_lift(a):\n" +
		"  trap_if(not candidates)\n" +
		"  def thread_func():\n" +
		"    pass\n"
	// The record pin has a symbol the current pin has dropped, which is the case a qualified citation is for.
	old := current + "def stream_copy(a):\n  pass\n"

	// The fixtures spell the prefix `DEF` / `DEFINITIONS`, lowered here, because this file is in the tree
	// `make refcite` scans: written in the real spelling, its refusal arms would be refusals of the tree.
	lower := strings.NewReplacer("DEFINITIONS.py", "definitions.py", "DEF", "def")
	run := func(t *testing.T, cited string, vendorRecord bool) (int, string) {
		t.Helper()
		cited = lower.Replace(cited)
		repo := t.TempDir()
		if out, gitErr := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); gitErr != nil {
			t.Fatalf("git init: %v\n%s", gitErr, out)
		}
		if wErr := os.WriteFile(filepath.Join(repo, "cites.go"), []byte(cited), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		vendor := t.TempDir()
		if wErr := os.WriteFile(filepath.Join(vendor, "definitions-"+pin+".py"), []byte(current), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		if vendorRecord {
			if wErr := os.WriteFile(filepath.Join(vendor, "definitions-"+record+".py"), []byte(old), 0o600); wErr != nil {
				t.Fatal(wErr)
			}
		}
		cmd := exec.Command("python3", "-I", script, "--root", repo, "--vendor", vendor, "--pin", pin,
			"--record-pin", record)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		code := 0
		if runErr := cmd.Run(); runErr != nil {
			var ee *exec.ExitError
			if !errors.As(runErr, &ee) {
				t.Fatalf("running refcite.py: %v\n%s", runErr, out.String())
			}
			code = ee.ExitCode()
		}
		return code, out.String()
	}

	t.Run("every_form_that_resolves_is_accepted", func(t *testing.T) {
		cited := "// DEF:canon_lift and DEFINITIONS.py:canon_lift, a nested DEF:canon_lift.thread_func,\n" +
			"// a method DEF:Task.cancel:\"trap_if(self.state == 1)\", a class attribute DEF:Task.LIMIT,\n" +
			"// a module statement DEF:\"assert(X > 0)\", and a record DEF@2222222:stream_copy.\n"
		code, out := run(t, cited, true)
		if code != 0 {
			t.Fatalf("exit %d on citations that all resolve; a checker refusing these proves nothing by "+
				"refusing the others:\n%s", code, out)
		}
		// Counted, so an arm passing by matching nothing is visible: seven citations, two quoted, one at a
		// record pin.
		if !strings.Contains(out, "7 citation(s) in 1 file(s); 2 quoted, 1 at a record pin") {
			t.Errorf("the summary does not count what was cited:\n%s", out)
		}
	})

	for _, a := range []struct{ name, cited, want string }{
		{"a_symbol_that_does_not_exist_is_refused", "// DEF:no_such_symbol\n", "no symbol 'no_such_symbol'"},
		{
			"a_quote_from_another_symbol_is_refused", "// DEF:Task.cancel:\"trap_if(not candidates)\"\n",
			"the quote does not appear inside Task.cancel",
		},
		{
			"a_module_quote_that_is_not_there_is_refused", "// DEF:\"assert(X > 9)\"\n",
			"the quote does not appear in the module",
		},
		{"a_line_number_is_refused", "// see DEF:1234-1240\n", "1234-1240): cite a symbol"},
		{
			"a_line_number_glued_to_a_symbol_is_refused", "// DEF:canon_lift/2137 and DEF:Task.cancel and :1715\n",
			"a line number glued to a symbol citation",
		},
		{"a_dropped_symbol_cited_unqualified_is_refused", "// DEF:stream_copy\n", "no symbol 'stream_copy'"},
		{"an_unvendored_pin_is_refused", "// DEF@abcdef0:canon_lift\n", "is not a vendored record pin"},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.cited, true)
			if code == 0 {
				t.Fatalf("exit 0 — the citation was accepted:\n%s", out)
			}
			if !strings.Contains(out, a.want) {
				t.Errorf("refused, but not for the registered reason (want %q):\n%s", a.want, out)
			}
		})
	}

	t.Run("an_absent_reference_fails_rather_than_skips", func(t *testing.T) {
		code, out := run(t, "// DEF:canon_lift\n", false)
		if code == 0 || !strings.Contains(out, "is not vendored") {
			t.Errorf("exit %d with the record pin's reference absent; an absent reference must be a "+
				"failure, never a pass by not asking:\n%s", code, out)
		}
	})
}
