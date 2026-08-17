// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/scttfrdmn/burroughs"
)

// The run subcommand: load a module, call an exported function, print the result.
//
// **A consumer of the public package, never of `internal/`** — which is decision 0029's decision 3
// and the point of the whole exercise rather than a style note. A `run` that reached into
// `internal/interp` directly would be a second path to the interpreter, unprobed by the vectors
// that cross the first one, which is the exact defect (an instrument's domain being an assertion it
// cannot check about itself) that this surface exists to repair. So everything below goes through
// the same calls a host embedding the engine would make, and the differential test in the root
// package covers both by covering one.

// runCmd implements `burroughs run`, including the reporting of its own failure.
//
// Writers are parameters rather than `os.Stdout`/`os.Stderr` reads so a test can read what a user
// would see without a subprocess — and the stdout/stderr split is load-bearing, not cosmetic:
// results go to stdout, declines and diagnostics to stderr, so a caller piping the result of a
// computation is not handed the validator's work plan in the middle of it.
//
// **The diagnostic is printed here rather than by main, so that it is a thing under test.** The first
// version returned the error and let `main` print it, which put every user-visible failure message
// outside the reach of the test that checks which stream each message lands on — the message was
// asserted against the error value while the stream it reached was asserted against nothing. `main`
// keeps only the exit.
func runCmd(stdout, stderr io.Writer, argv []string) error {
	err := run(stdout, stderr, argv)
	// A usage error has already printed the usage; adding "burroughs: usage" to it would be this
	// process reporting its own control flow as a diagnostic.
	if err != nil && !errors.Is(err, errUsage) {
		diagnose(stderr, err)
	}
	return err
}

// run is runCmd's body: everything but the reporting.
func run(stdout, stderr io.Writer, argv []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strict := fs.Bool("strict", false,
		"refuse a module the validator could not fully check, instead of running it")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: burroughs run [--strict] <file.wasm> [<func> [<value>...]]")
		fmt.Fprintln(stderr, "\nWith no function named, lists the module's exported functions.")
		fmt.Fprintln(stderr, "\nValues are typed: i32:42  i64:-1  f32:nan  f64:inf  v128:0x0:0x0  extern:3  null:func")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return errUsage
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return errUsage
	}

	wasm, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}

	in, err := burroughs.Config{Strict: *strict}.Instantiate(wasm)
	if err != nil {
		return err
	}

	// The decline is reported before anything is called, and on stderr, because it is a statement
	// about what was *not* checked and the user has to be able to read it alongside a result rather
	// than instead of one. Decision 0029: out-of-vocabulary means run, with the construct named —
	// not silently, and not by refusing.
	//
	// Both go through `diagnose` rather than through a `"burroughs: %v"` of their own, which is the
	// sweep half of grave #383: `Decline` returns an ErrDeclined and `Deferred` an ErrUnsupported, so
	// both already name the program and both stuttered. The sentence between them is prose and not an
	// error, so it keeps its literal prefix.
	if d := in.Decline(); d != nil {
		diagnose(stderr, d)
		fmt.Fprintln(stderr, prefix+"the module ran unvalidated in that respect; --strict refuses instead")
	}
	if d := in.Deferred(); d != nil {
		diagnose(stderr, d)
	}

	if fs.NArg() == 1 {
		return listExports(stdout, in)
	}

	args := make([]burroughs.Value, 0, fs.NArg()-2)
	for _, spelling := range fs.Args()[2:] {
		v, perr := burroughs.ParseValue(spelling)
		if perr != nil {
			return perr
		}
		args = append(args, v)
	}

	res, err := in.Call(fs.Arg(1), args...)
	if err != nil {
		return err
	}
	if len(res) == 0 {
		return nil
	}
	spellings := make([]string, len(res))
	for i, v := range res {
		spellings[i] = v.String()
	}
	fmt.Fprintln(stdout, strings.Join(spellings, " "))
	return nil
}

// listExports prints the exported function names, which is what `run` with no function does.
//
// A module with no exported functions is reported on stdout as such rather than as an empty
// success: an empty listing and a module that exports nothing look identical to a reader, and only
// one of them is worth knowing.
func listExports(stdout io.Writer, in *burroughs.Instance) error {
	names := in.Exports()
	if len(names) == 0 {
		fmt.Fprintln(stdout, "no exported functions")
		return nil
	}
	for _, n := range names {
		fmt.Fprintln(stdout, n)
	}
	return nil
}

// errUsage is a wrong invocation, distinct from every failure of the module or the engine. The flag
// set has already said what was wrong, so this carries no message of its own.
var errUsage = errors.New("usage")

// Exit codes, one per question a caller can ask about the run.
//
// **A single non-zero code would be the board's mixture error wearing a shell's clothes.** "Your
// module is wrong" and "this engine is incomplete" are different findings with different remedies,
// and a script that cannot tell them apart is in exactly the position a `failed`/`gated` merge puts
// a reader of the suite. An exit code is a verdict channel and cannot say *why* — that is what the
// stderr text is for — but it can say *which*, and these are the whichs:
const (
	exitOK          = 0 // the module ran
	exitError       = 1 // this invocation's own failure: unreadable file, unspellable value
	exitUsage       = 2 // wrong arguments; the conventional code for it
	exitRefused     = 3 // the module was refused: malformed, invalid, or declined under --strict
	exitTrap        = 4 // the module executed correctly and the program went wrong
	exitUnsupported = 5 // the engine reached something it does not implement in this phase
	exitGated       = 6 // the module is fine; this build has that proposal's gate off (#301)
)

// exitCode maps an error from runCmd **or inspectCmd** onto the taxonomy above.
//
// Both, as of decision 0033 (#373): the codes are the CLI's and not one subcommand's, and this switch
// is the single place the mapping happens. `inspect` reaches fewer of them — nothing it does can trap
// or reach an unimplemented instruction — which is a statement about the questions it asks, not about
// the taxonomy, and TestBothSubcommandsClassifyOneModuleTheSameWay is where that statement is
// executable.
//
// Matched with errors.Is/As against the public sentinels rather than by inspecting text, for the
// harness's own reason for doing so: the taxonomy belongs to the package that defines it, and a
// string match here would silently reclassify every message reworded upstream.
// TestExitCodesCoverEveryPublicSentinel is the guard, derived from the sentinel set rather than
// from this switch.
func exitCode(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, errUsage):
		return exitUsage
	// ErrGated ahead of ErrMalformed for the boundary's own reason (grave #301): the module is
	// well-formed, and a script that cannot tell "rebuild with the gate on" from "fix your module"
	// is in the position a merged gated/failed column puts a reader of the board.
	case errors.Is(err, burroughs.ErrGated):
		return exitGated
	case errors.Is(err, burroughs.ErrMalformed),
		errors.Is(err, burroughs.ErrInvalid),
		errors.Is(err, burroughs.ErrDeclined):
		return exitRefused
	case errors.Is(err, burroughs.ErrUnsupported):
		return exitUnsupported
	}
	var trap *burroughs.Trap
	if errors.As(err, &trap) {
		return exitTrap
	}
	return exitError
}
