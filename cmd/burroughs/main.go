// Command burroughs is the engine CLI.
//
// v0 surface: run (instantiate a module and call an export), inspect (section-level module dump),
// and version. `run` is a consumer of the public `burroughs` package — decision 0029 — so the CLI
// and an embedding host cross the same path, and neither is covered without the other.
//
// The **exit codes are the CLI's, not `run`'s** (decision 0033, #373): `inspect` classifies its
// refusals onto the same public sentinels and returns the same codes for them, so a script that
// inspects before running does not have to translate. It decodes without validating, which is why it
// reaches fewer of the codes and why it cannot route through `Config.Instantiate` to get them.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/scttfrdmn/burroughs"
	"github.com/scttfrdmn/burroughs/internal/binary"
)

const banner = "burroughs v0.0.1 — the machine Burroughs never built"

// prefix is what this CLI says before a diagnostic, and it is a constant because `diagnose` has to
// be able to ask whether the message it was handed already carries it.
const prefix = "burroughs: "

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
		// One taxonomy for the whole CLI — decision 0033, closing #373. This arm used to return
		// exitError for every inspect failure, including a malformed module that `run` classifies as
		// exitRefused and a gated one it classifies as exitGated; exitError means "this invocation's
		// own failure", so the tool blamed itself for the user's module and blamed the module for a
		// gate. The retired comment said the question was the CLI's contract and a writer-threading
		// refactor was not the artifact that answered it. This is that artifact.
		return exitCode(inspectCmd(stdout, stderr, argv[1]))
	case "run":
		return exitCode(runCmd(stdout, stderr, argv[1:]))
	default:
		usage(stderr)
		return exitUsage
	}
}

// inspectCmd is `burroughs inspect`: the dump, the diagnostic, and the error the taxonomy classifies.
//
// Shaped exactly like runCmd — do the work, print the diagnostic *here* rather than in `dispatch`,
// hand the error to `exitCode` — because the two subcommands' failures now travel one taxonomy, and
// two shapes for one channel is how they drift back apart.
func inspectCmd(stdout, stderr io.Writer, path string) error {
	err := inspect(stdout, path)
	if err != nil {
		diagnose(stderr, err)
	}
	return err
}

// diagnose prints err on stderr under this program's name, without saying the name twice.
//
// The library spells its sentinels "burroughs: malformed module" so a host that logs one bare still
// says who spoke; the CLI prefixes its *own* failures for the same reason ("open x.wasm: no such
// file"). Printed naively the two stack, and `run` did stack them — `burroughs: burroughs: malformed
// module: magic header not detected`, grave #383 — which reads as a defect in the tool at the one
// moment the tool is reporting a defect in the module.
//
// **A text test, and deliberately not a taxonomy test.** Classification is matched on sentinels here
// and everywhere else, for exitCode's stated reason. What this asks is whether a string this package
// owns is already present in a string about to be printed, which is a question about presentation
// with no verdict riding on it: guess wrong and the prefix is doubled or absent, not the code.
func diagnose(stderr io.Writer, err error) {
	if msg := err.Error(); strings.HasPrefix(msg, prefix) {
		fmt.Fprintln(stderr, msg)
		return
	}
	fmt.Fprintln(stderr, prefix+err.Error())
}

// inspect prints a section-level dump, classifying a refusal onto the public sentinels (#373).
//
// **It decodes and does not validate**, which is the reason it cannot borrow the library's
// classification instead of repeating it. A module that fails typing is still a module whose sections
// a reader wants dumped, so this path must survive what `Config.Instantiate` refuses — and
// `Instantiate` is where the library's gate-before-malformed ordering lives.
//
// So the ordering below is **a second copy of grave #301's**, declared as one. The alternatives were
// each worse: a shared classifier would have to be a decode-only entry point in the *public* API — a
// compatibility promise minted for a debugging convenience — and pushing the gate/malformed decision
// down into `exitCode` gets the number right by making the message say "malformed module" about a
// well-formed one, which is a correct verdict on fabricated testimony. The tripwire for the copy is
// TestBothSubcommandsClassifyOneModuleTheSameWay, which drives one gated and one malformed file
// through both subcommands and fails when either copy drifts.
func inspect(stdout io.Writer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		// The one failure here that is *not* about the module: exitError is the honest code for it,
		// and the error's own text already names the file.
		return err
	}
	m, err := binary.DecodeModule(data)
	if err != nil {
		// Gate first, general second — grave #301. A construct from a gated proposal is well-formed,
		// so a caller told "malformed" goes looking for a defect that is not in their module. The
		// features are DefaultFeatures on both paths (`binary.DecodeModule`'s own doc says so), which
		// is what makes the two subcommands' answers comparable at all.
		if errors.Is(err, binary.ErrFeatureDisabled) {
			return fmt.Errorf("%w: %w", burroughs.ErrGated, err)
		}
		return fmt.Errorf("%w: %w", burroughs.ErrMalformed, err)
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
