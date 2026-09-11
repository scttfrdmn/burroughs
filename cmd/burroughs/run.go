// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/scttfrdmn/burroughs"
)

// preopenFlag collects repeated `--dir HOST[:GUEST]` grants into preopens for a wasip1 command. HOST
// alone maps the host directory under its own name; HOST:GUEST maps it under GUEST. Capability-based:
// only what is named here is visible to the guest (decision 0083).
type preopenFlag []burroughs.Preopen

func (f *preopenFlag) String() string {
	parts := make([]string, len(*f))
	for i, p := range *f {
		parts[i] = p.Host + ":" + p.Guest
	}
	return strings.Join(parts, ",")
}

func (f *preopenFlag) Set(v string) error {
	host, guest, found := strings.Cut(v, ":")
	if host == "" {
		return fmt.Errorf("empty host directory in --dir %q", v)
	}
	if !found || guest == "" {
		guest = host
	}
	*f = append(*f, burroughs.Preopen{Host: host, Guest: guest})
	return nil
}

func (f preopenFlag) preopens() []burroughs.Preopen { return []burroughs.Preopen(f) }

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
	// process reporting its own control flow as a diagnostic. A wasiExit is not a diagnostic either —
	// it is the guest's own exit code, and the guest has already written whatever it meant to stderr.
	var we wasiExit
	if err != nil && !errors.Is(err, errUsage) && !errors.As(err, &we) {
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
	var dirs preopenFlag
	fs.Var(&dirs, "dir", "grant a wasip1 command a directory as HOST[:GUEST] (repeatable); "+
		"no directory is visible unless named (decision 0083)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: burroughs run [--strict] [--dir HOST[:GUEST]]... <file.wasm> "+
			"[-- <arg>...] | [<func> [<value>...]]")
		fmt.Fprintln(stderr, "\nA wasip1 command (imports wasi_snapshot_preview1, exports _start) runs; "+
			"its argv is what follows --, and its exit code becomes this process's. --dir grants it a "+
			"directory, capability-based: nothing is visible unless named.")
		fmt.Fprintln(stderr, "Any other module: with a function named it is invoked; with none its exports are listed.")
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

	file := fs.Arg(0)
	// The tokens after the file split at "--": what follows it is a command's argv, so a guest
	// argument is never read as a function name (decision 0083); what precedes it is the func-invoke
	// form. `--` itself is a positional here because it comes after the file, so flag parsing has
	// already stopped.
	rest := fs.Args()[1:]
	var invokeArgs, guestArgs []string
	if i := slices.Index(rest, "--"); i >= 0 {
		invokeArgs, guestArgs = rest[:i], rest[i+1:]
	} else {
		invokeArgs = rest
	}

	wasm, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	// A wasip1 command runs (decision 0081); detection reads the module's sections before any plain
	// instantiate, so it never depends on #686's nil-resolver behavior. A decode error is not handled
	// here — it falls through to Instantiate below, which classifies a malformed module onto the
	// public sentinels — so detection routes to WASI only on a clean `(true, nil)`.
	if isCmd, derr := burroughs.IsWASIP1Command(wasm); derr == nil && isCmd {
		if len(invokeArgs) > 0 {
			// A command's arguments go after `--`; a bare token before it is not a function to invoke.
			fmt.Fprintf(stderr, "%sa wasip1 command takes its arguments after --, e.g. run %s -- ARG\n", prefix, file)
			return errUsage
		}
		code, rerr := burroughs.WASIP1Config{
			Args:     append([]string{filepath.Base(file)}, guestArgs...),
			Env:      os.Environ(),
			Stdin:    os.Stdin,
			Stdout:   stdout,
			Stderr:   stderr,
			Preopens: dirs.preopens(),
		}.Run(wasm)
		if rerr != nil {
			return rerr
		}
		if code != 0 {
			return wasiExit(code)
		}
		return nil
	}

	// A component (layer 1) runs its `wasi:cli/run` world through the preview-2 host, dispatched on the
	// preamble like the wasip1 command above (ADR 0084/0085, #694). Detection reads the header before any
	// instantiate; a bad header falls through to the core-module path, which classifies it. The stdio is
	// bound at the writer, the same os.Stdout/os.Stderr a wasip1 command receives (ADR 0083, shared).
	if isComp, cerr := burroughs.IsComponent(wasm); cerr == nil && isComp {
		if len(invokeArgs) > 0 || len(dirs) > 0 {
			fmt.Fprintf(stderr, "%sa component runs its wasi:cli/run world; it takes no function name or --dir\n", prefix)
			return errUsage
		}
		code, rerr := burroughs.ComponentConfig{
			Stdin:  os.Stdin,
			Stdout: stdout,
			Stderr: stderr,
		}.Run(wasm)
		if rerr != nil {
			return rerr
		}
		if code != 0 {
			return wasiExit(code)
		}
		return nil
	}

	// Not a command: `--` and `--dir` are the command grammar and do not apply.
	if len(guestArgs) > 0 || len(dirs) > 0 {
		fmt.Fprintf(stderr, "%s-- and --dir apply to a wasip1 command, and this module is not one\n", prefix)
		return errUsage
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

	if len(invokeArgs) == 0 {
		return listExports(stdout, in)
	}

	args := make([]burroughs.Value, 0, len(invokeArgs)-1)
	for _, spelling := range invokeArgs[1:] {
		v, perr := burroughs.ParseValue(spelling)
		if perr != nil {
			return perr
		}
		args = append(args, v)
	}

	res, err := in.Call(invokeArgs[0], args...)
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

// wasiExit carries a wasip1 guest's own `proc_exit` code so it becomes this process's exit code
// (decision 0081). It is not a diagnostic — a guest exiting non-zero is the guest's verdict, not the
// CLI's — so `runCmd` does not print it, and `exitCode` returns it verbatim ahead of the CLI's own
// taxonomy. A guest that exits 0 returns a nil error instead, so 0 is never spelled as this type.
type wasiExit int

func (e wasiExit) Error() string { return fmt.Sprintf("wasi guest exited %d", int(e)) }

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
	// A wasip1 guest's own exit code is propagated verbatim, ahead of the CLI's taxonomy: it is the
	// guest's verdict, not the CLI's, and a process runner exits with its child's status (decision
	// 0081). The overlap with the CLI's own codes (a guest exiting 4, exitTrap = 4) is resolved in the
	// guest's favor — it chose the code.
	var we wasiExit
	if errors.As(err, &we) {
		return int(we)
	}
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
		errors.Is(err, burroughs.ErrDeclined),
		errors.Is(err, burroughs.ErrUnlinkable):
		// ErrUnlinkable joins the refused class (decision 0082): a module with unsupplied imports is
		// refused at load, the same outcome family as malformed/invalid/declined — the module cannot
		// be run as given, which is `assert_unlinkable`, not a trap or an engine gap.
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
