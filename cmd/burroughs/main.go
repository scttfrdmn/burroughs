// Command burroughs is the engine CLI.
//
// v0 surface: run (instantiate a module and call an export), inspect (section-level module dump),
// and version. `run` is a consumer of the public `burroughs` package — decision 0029 — so the CLI
// and an embedding host cross the same path, and neither is covered without the other.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

const banner = "burroughs v0.0.1 — the machine Burroughs never built"

func main() { os.Exit(dispatch(os.Stdout, os.Stderr, os.Args[1:])) }

// dispatch is main's body: subcommand selection, and the exit code every path returns.
//
// **Writers and a returned code rather than `os.Stdout` and `os.Exit`**, which is the shape `run`
// already had and the reason it had it: everything a user sees has to be inside a function a test can
// call. `run` was written that way from the start and its two siblings were not, so `inspect` printed
// to a stream no test could read and was covered by nothing at all — the same lesson, one case to the
// left, not swept. TestREADMETranscriptIsExecutable now runs the README's own transcript through
// here, which is only possible because this returns rather than exits.
//
// `main` keeps exactly the `os.Exit` call, since that is the one thing a test cannot survive.
func dispatch(stdout, stderr io.Writer, argv []string) int {
	if len(argv) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch argv[0] {
	case "version":
		fmt.Fprintln(stdout, banner)
		return exitOK
	case "inspect":
		if len(argv) != 2 {
			usage(stderr)
			return exitUsage
		}
		if err := inspect(stdout, argv[1]); err != nil {
			fmt.Fprintln(stderr, "burroughs:", err)
			// Every inspect failure is reported as this invocation's own (exitError), including a
			// malformed module that `run` classifies as refused (exitRefused). Preserved exactly as it
			// was rather than harmonized here: which code `inspect` owes a malformed file is a question
			// about the CLI's contract, and a refactor that threads writers is not the artifact that
			// gets to answer it.
			return exitError
		}
		return exitOK
	case "run":
		return exitCode(runCmd(stdout, stderr, argv[1:]))
	default:
		usage(stderr)
		return exitUsage
	}
}

func inspect(stdout io.Writer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, err := binary.DecodeModule(data)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s: wasm v%d, %d section(s)\n", path, m.Version, len(m.Sections))
	for i, s := range m.Sections {
		fmt.Fprintf(stdout, "  [%d] %-10s %6d bytes\n", i, s.ID, len(s.Payload))
	}
	return nil
}

func usage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: burroughs <command> [arguments]")
	fmt.Fprintln(stderr, "  run [--strict] file.wasm [func [value...]]   instantiate and call an export")
	fmt.Fprintln(stderr, "  inspect file.wasm                            section-level module dump")
	fmt.Fprintln(stderr, "  version")
}
