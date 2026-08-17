// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The CLI's taxonomy across *both* subcommands — decision 0033, closing #373.
//
// # What this file is the tripwire for
//
// `inspect` decodes and must not validate, so it cannot borrow `Config.Instantiate`'s classification;
// it repeats the gate-before-malformed ordering instead, which is a second copy of grave #301's
// ordering and is declared as one in `main.go`. A duplicated ordering is the shape that loses an arm
// in one copy and passes every test written against the other, so the copies are held equal here
// rather than each held right in isolation.
//
// # Equality is not the assertion, and that is deliberate
//
// Two copies can be wrong together in exactly the way that makes an agreement worthless — the lesson
// is on file as a delta from one broken instrument being sound while both levels are wrong. So each
// row pins the **absolute** code for each subcommand as well as the relation between them.

// TestBothSubcommandsClassifyOneModuleTheSameWay drives one file through `run` and `inspect` and
// compares the codes.
//
// The rows where they *disagree* are as load-bearing as the rows where they agree: `inspect` answers a
// narrower question, and the scope statement in 0033's table ("`4` and `5` are unreachable; an invalid
// module is `0`") is prose until a row asserts it.
func TestBothSubcommandsClassifyOneModuleTheSameWay(t *testing.T) {
	malformed := filepath.Join(t.TempDir(), "malformed.wasm")
	if err := os.WriteFile(malformed, []byte("not a wasm module"), 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(t.TempDir(), "absent.wasm")

	cases := []struct {
		name        string
		path        string
		wantRun     int
		wantInspect int
		why         string
	}{
		{
			name: "a malformed image is the module's failure, not the invocation's",
			path: malformed,
			// The defect #373 names: exitError says "this invocation's own failure", so the old
			// `inspect` blamed itself for the user's module while its sibling said exitRefused.
			wantRun: exitRefused, wantInspect: exitRefused,
			why: "the decoder refused the image on both paths, so the code is the same on both",
		},
		{
			name: "a gated proposal keeps its own code on both paths",
			path: writeModule(t, "gated.wasm", `(module (memory i64 1))`),
			// Grave #301 at the shell, in the subcommand that did not have it: a gated module is
			// well-formed, and exitRefused would send a caller looking for a defect in their module.
			wantRun: exitGated, wantInspect: exitGated,
			why: "memory64's gate is off in DefaultFeatures, which is the feature set *both* paths " +
				"decode under — `binary.DecodeModule`'s doc and `Config.Instantiate`'s decoder agree, " +
				"and if they ever stopped agreeing this row is where it would show",
		},
		{
			name: "an unreadable file is the invocation's own failure on both paths",
			path: absent, wantRun: exitError, wantInspect: exitError,
			why: "the one refusal in `inspect` that is not about the module, and the one place " +
				"exitError is the honest answer rather than the catch-all it used to be",
		},
		{
			name: "a module both subcommands can read exits 0 on both",
			path: writeModule(t, "valid.wasm",
				`(module (func (export "answer") (result i32) i32.const 42))`),
			wantRun: exitOK, wantInspect: exitOK,
			why: "`run` with no function lists exports; the vacuity half of the table, since a row " +
				"set with no agreement at 0 could be satisfied by a CLI that refused everything",
		},
		{
			name: "an invalid module is refused by run and dumped by inspect",
			path: writeModule(t, "invalid.wasm",
				`(module (func (export "f") (result i32) i64.const 42))`),
			// **The deliberate disagreement**, and the executable form of 0033's scope statement.
			// `inspect` does not validate — a module that fails typing is still a module whose
			// sections a reader wants dumped, which is the tool's use during a slice campaign — so it
			// answered its question completely and 0 is correct. Scott's constraint on #373 is
			// "a refusal to answer never exits 0"; this is not a refusal to answer.
			wantRun: exitRefused, wantInspect: exitOK,
			why: "inspect decodes and does not validate, so the typing rule run refuses on is not a " +
				"question inspect asked",
		},
	}

	seen := map[int]bool{}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		if got := dispatch(&out, &errOut, []string{"run", c.path}); got != c.wantRun {
			t.Errorf("%s: `run` exited %d, want %d\nwhy: %s\nstderr: %s",
				c.name, got, c.wantRun, c.why, errOut.String())
		}
		out.Reset()
		errOut.Reset()
		got := dispatch(&out, &errOut, []string{"inspect", c.path})
		if got != c.wantInspect {
			t.Errorf("%s: `inspect` exited %d, want %d\nwhy: %s\nstderr: %s",
				c.name, got, c.wantInspect, c.why, errOut.String())
		}
		// Scott's constraint on #373, asserted rather than argued: a refusal to answer never exits 0.
		// Checked off the *output* and not off the expectation, because the failure it guards against
		// is a dump that printed some sections and then gave up.
		if got == exitOK && errOut.Len() > 0 && out.Len() == 0 {
			t.Errorf("%s: `inspect` exited 0 having written only a diagnostic (%q); a refusal to "+
				"answer must not exit 0", c.name, errOut.String())
		}
		seen[c.wantInspect] = true
	}

	// Coverage is a claim, so the domain is stated and checked rather than assumed from the row count.
	// The two classes that matter are the decoder's two refusals — a copy of the #301 ordering with
	// one arm missing passes a table that exercises only the other arm.
	for _, want := range []int{exitRefused, exitGated, exitError, exitOK} {
		if !seen[want] {
			t.Errorf("no row asks `inspect` for exit %d; the ordering this file is the tripwire for "+
				"has an arm per code, and an unexercised arm is where a copy drifts", want)
		}
	}
	// "asks for", not "exercised": `seen` is built from the *expectations*, so this line states the
	// table's domain and not the run's observations. Said that way on purpose — a log that reported
	// what the CLI returned would have printed a reassuring `[0 1 3 6]` during the falsification runs
	// that made this file fail, which is a coverage claim quietly measuring the wrong set.
	t.Logf("codes this table asks `inspect` for: %v; unreachable by construction: %d (trap) and "+
		"%d (unsupported), since inspect executes nothing",
		slices.Sorted(maps.Keys(seen)), exitTrap, exitUnsupported)
}

// TestDiagnosticNamesTheProgramExactlyOnce is grave #383's control.
//
// The defect: the public sentinels spell themselves "burroughs: malformed module" so a host logging
// one bare says who spoke, and the CLI prefixes its own failures for the same reason — composed, every
// classified error read `burroughs: burroughs: malformed module: …`. It survived because the stderr
// assertions in `TestRunSubcommandOutcomes` use `strings.Contains`, which is the right assertion for
// "the message names the proposal" and is blind to a duplicated prefix by construction.
//
// So this one counts occurrences at the **start of the line**, over every subcommand and every stream
// that carries a diagnostic — the sweep half, since `run`'s decline and deferred reports had their own
// copies of the literal prefix.
func TestDiagnosticNamesTheProgramExactlyOnce(t *testing.T) {
	malformed := filepath.Join(t.TempDir(), "malformed.wasm")
	if err := os.WriteFile(malformed, []byte("not a wasm module"), 0o600); err != nil {
		t.Fatal(err)
	}
	// `i8x16.relaxed_swizzle` is implemented by the interpreter and not typed by the validator, so
	// this module runs *and* reports a decline — the two-line stderr the sweep was about. Kept in step
	// with run_test.go's `declining` fixture, which carries the note on why this specimen is
	// structural and retires on a gate flip rather than on the next slice.
	declining := writeModule(t, "declining.wasm",
		`(module (func (export "swizzle") (result i32)
			(i8x16.extract_lane_s 0
				(i8x16.relaxed_swizzle
					(v128.const i8x16 7 6 5 4 3 2 1 0 0 0 0 0 0 0 0 0)
					(v128.const i8x16 3 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)))))`)

	for _, argv := range [][]string{
		{"run", malformed},
		{"inspect", malformed},
		{"run", writeModule(t, "gated.wasm", `(module (memory i64 1))`)},
		{"inspect", writeModule(t, "gated2.wasm", `(module (memory i64 1))`)},
		{"run", writeModule(t, "invalid.wasm",
			`(module (func (export "f") (result i32) i64.const 42))`)},
		{"run", declining, "swizzle"},
		{"run", "--strict", declining, "swizzle"},
		{"run", filepath.Join(t.TempDir(), "absent.wasm")},
	} {
		var out, errOut bytes.Buffer
		dispatch(&out, &errOut, argv)

		lines := strings.Split(strings.TrimSuffix(errOut.String(), "\n"), "\n")
		if errOut.Len() == 0 {
			t.Errorf("`%s` wrote no diagnostic; this row exists to read one, so it is asserting "+
				"nothing", strings.Join(argv, " "))
			continue
		}
		for _, line := range lines {
			if !strings.HasPrefix(line, prefix) {
				// Not a defect on its own — the flag package's usage text is not a diagnostic — but
				// worth reporting, since a diagnostic that does not name the program is the other
				// half of #383 and no test held that direction either.
				t.Errorf("`%s` wrote a stderr line that does not name the program: %q",
					strings.Join(argv, " "), line)
				continue
			}
			if rest := strings.TrimPrefix(line, prefix); strings.HasPrefix(rest, prefix) {
				t.Errorf("`%s` named the program twice: %q — grave #383, and the doubling reads as "+
					"a defect in the tool at the moment it is reporting a defect in the module",
					strings.Join(argv, " "), line)
			}
		}
	}
}
