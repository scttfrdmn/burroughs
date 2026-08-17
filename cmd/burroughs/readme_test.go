// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestREADMETranscriptIsExecutable runs the README's own "Try it" transcript through the CLI and
// compares what it printed, and what it exited with, to what the file says.
//
// **A transcript is a claim about behaviour, so it gets the treatment every other claim here gets.**
// The alternative — a human pasting output once and the docs drifting from the engine one refactor
// later — is the drifted-fixture-citation defect in a place readers trust more than a test file,
// because a README is the first artifact anybody reads and the last one anybody re-runs.
//
// The commands are *not* run through a shell: argv is split on whitespace and handed to dispatch, so
// the transcript is checked against the engine rather than against a subprocess, and a line needing
// quoting or a pipe is rejected below rather than silently mis-parsed.
//
// # The domain, which the test prints
//
// Only lines invoking this CLI are executed. `go build`, `make spec-tests` and the conformance
// commands appear in the README too and are counted, never run — a test that shelled out to `make`
// would be a different instrument with a different cost. *Coverage is a claim*, so the count of each
// is logged rather than left to a reader's assumption about what "the transcript is checked" covers.
func TestREADMETranscriptIsExecutable(t *testing.T) {
	// The transcript's paths are written relative to the repo root, because that is where a reader
	// runs them. Running the commands anywhere else would make `inspect`'s own output — which echoes
	// the path it was given — disagree with the file for a reason that is about this test.
	t.Chdir("../..")

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}

	stanzas, other := parseTranscript(t, string(readme))
	t.Logf("transcript: %d burroughs stanzas executed, %d other command lines counted and not run",
		len(stanzas), other)

	// Vacuity, both halves. An empty stanza list would make every assertion below vacuous, and a
	// transcript with no failing command would document only the happy path while claiming to
	// document the exit taxonomy the section beneath it tabulates.
	if len(stanzas) < 4 {
		t.Fatalf("found %d executable stanzas in README.md; the \"Try it\" section is supposed to "+
			"be a transcript, and this test agrees with anything when there is nothing to run",
			len(stanzas))
	}
	if !slices.ContainsFunc(stanzas, func(s stanza) bool { return s.wantExit != exitOK }) {
		t.Error("every documented command exits 0, so the transcript demonstrates none of the exit " +
			"codes the README tabulates")
	}

	for _, s := range stanzas {
		var out, errOut bytes.Buffer
		code := dispatch(&out, &errOut, s.argv)

		// The streams are concatenated for the comparison, and that is a stated limit rather than an
		// oversight: a transcript cannot represent interleaving, so a command writing both would be
		// compared against a guess about ordering. TestRunSubcommandOutcomes is where the split is the
		// subject; here the requirement is that a documented command writes one stream, enforced.
		if out.Len() > 0 && errOut.Len() > 0 {
			t.Errorf("README.md:%d: `%s` wrote to both stdout and stderr; a transcript cannot say "+
				"which came first, so document commands that write one stream\nstdout: %q\nstderr: %q",
				s.line, strings.Join(s.argv, " "), out.String(), errOut.String())
			continue
		}
		if got := out.String() + errOut.String(); got != s.want {
			t.Errorf("README.md:%d: `%s` printed\n%s\nand the README says\n%s",
				s.line, strings.Join(s.argv, " "), quoteLines(got), quoteLines(s.want))
		}
		if code != s.wantExit {
			t.Errorf("README.md:%d: `%s` exited %d, and the README says %d",
				s.line, strings.Join(s.argv, " "), code, s.wantExit)
		}
	}
}

// stanza is one documented command: its argv, the output the README shows for it, and the exit code
// the README claims.
type stanza struct {
	line     int
	argv     []string
	want     string
	wantExit int
}

// exitsComment is the transcript's spelling for a non-zero exit: a trailing `# exits N` on the
// command line, which reads as a shell comment to a human and carries the claim to this test.
var exitsComment = regexp.MustCompile(`\s*#\s*exits\s+(\d+)\s*$`)

// parseTranscript pulls the executable stanzas out of doc, and counts the command lines it declined
// to execute.
//
// A stanza is a `$ ` line inside a fenced `console` block plus every following non-`$` line up to the
// next `$` line or the next blank line. The blank line is the terminator because that is how the
// transcript is written — one command and its output per paragraph — and a grammar that admitted more
// than the file uses would be a second grammar nobody keeps honest.
func parseTranscript(t *testing.T, doc string) (stanzas []stanza, other int) {
	t.Helper()

	var cur *stanza
	flush := func() {
		if cur != nil {
			cur.want = strings.TrimPrefix(cur.want, "\n")
			stanzas = append(stanzas, *cur)
			cur = nil
		}
	}

	inside := false
	for i, line := range strings.Split(doc, "\n") {
		lineNo := i + 1
		switch {
		case inside && strings.HasPrefix(line, "```"):
			flush()
			inside = false
		case line == "```console":
			inside = true
		case !inside:
		case strings.HasPrefix(line, "$ "):
			flush()
			cmd := strings.TrimPrefix(line, "$ ")
			wantExit := exitOK
			if m := exitsComment.FindStringSubmatch(cmd); m != nil {
				n, err := strconv.Atoi(m[1])
				if err != nil {
					t.Fatalf("README.md:%d: unreadable exit claim %q", lineNo, m[1])
				}
				wantExit = n
				cmd = exitsComment.ReplaceAllString(cmd, "")
			}
			// Everything after `#` is a comment for the reader, not argv.
			if idx := strings.Index(cmd, "#"); idx >= 0 {
				cmd = cmd[:idx]
			}
			fields := strings.Fields(cmd)
			if len(fields) == 0 {
				t.Fatalf("README.md:%d: a `$` line with no command", lineNo)
			}
			if fields[0] != "burroughs" && fields[0] != "./bin/burroughs" {
				other++
				continue
			}
			// No shell runs these, so a line that needs one is a line this test would mis-execute
			// while reporting a pass. Rejected loudly instead.
			if strings.ContainsAny(cmd, `"'|><&*$`) {
				t.Fatalf("README.md:%d: `%s` needs a shell, and this runner does not have one; "+
					"document a command whose argv splits on whitespace", lineNo, cmd)
			}
			cur = &stanza{line: lineNo, argv: fields[1:], wantExit: wantExit}
		case strings.TrimSpace(line) == "":
			flush()
		case cur != nil:
			cur.want += line + "\n"
		}
	}
	flush()
	return stanzas, other
}

// quoteLines renders a captured stream so a diff in trailing whitespace is visible in the failure.
func quoteLines(s string) string {
	if s == "" {
		return "\t(nothing)"
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		b.WriteString("\t" + strconv.Quote(line) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// TestREADMEDocumentsEveryExitCode checks the README's exit-code table against the constants.
//
// The taxonomy is only useful to somebody who can look it up, and a table that has quietly fallen one
// code behind the source is worse than no table: a script author reads it, believes the set is
// closed, and routes a new code to whatever their default branch does. Same shape as
// TestExitCodesCoverEveryPublicSentinel one channel over — that one asks whether the codes cover the
// library's classifications, this one asks whether the documentation covers the codes.
//
// The code set is **derived from run.go's own declarations**, not typed here, for the reason the
// sentinel test states: a domain typed beside the thing it checks cannot notice an addition. What
// this cannot check is whether each row's prose is *right* — that is prose against prose — so it
// checks the number and the count, in both directions.
func TestREADMEDocumentsEveryExitCode(t *testing.T) {
	codes := declaredExitCodes(t)

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}

	// A row in the exit-code table: `| `4` | … |`.
	row := regexp.MustCompile("(?m)^\\| `(\\d+)` \\|")
	documented := map[int]int{}
	for _, m := range row.FindAllStringSubmatch(string(readme), -1) {
		n, cerr := strconv.Atoi(m[1])
		if cerr != nil {
			t.Fatalf("unreadable code %q in the README's table", m[1])
		}
		documented[n]++
	}

	declared := map[int]bool{}
	for name, code := range codes {
		declared[code] = true
		switch documented[code] {
		case 1:
		case 0:
			t.Errorf("%s = %d is not in the README's exit-code table, so a caller reading that "+
				"table would treat the set as closed and route it to their default branch", name, code)
		default:
			t.Errorf("%s = %d has %d rows in the README's table; two rows for one code is two "+
				"meanings for one verdict", name, code, documented[code])
		}
	}
	// The other direction, which is the one a reader cannot check: a row for a code the CLI never
	// returns is documentation of behaviour that does not exist.
	for code := range documented {
		if !declared[code] {
			t.Errorf("the README documents exit %d, which no constant in run.go declares", code)
		}
	}
}

// declaredExitCodes reads the `exit*` constants out of run.go.
//
// The same AST walk declaredSentinels uses on the public package, for the same reason and with the
// same vacuity floor: the authority is the declaration, and a moved or renamed const block yields an
// empty set that would make every comparison above an agreement between nothings.
func declaredExitCodes(t *testing.T) map[string]int {
	t.Helper()

	const path = "run.go"
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	codes := map[string]int{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			if !strings.HasPrefix(vs.Names[0].Name, "exit") {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				continue
			}
			n, cerr := strconv.Atoi(lit.Value)
			if cerr != nil {
				t.Fatalf("%s: %s has an unreadable value %q", path, vs.Names[0].Name, lit.Value)
			}
			codes[vs.Names[0].Name] = n
		}
	}
	if len(codes) < 5 {
		t.Fatalf("found %d exit constants in %s (%v); the declarations moved, so this test is "+
			"measuring nothing", len(codes), path, codes)
	}
	return codes
}
