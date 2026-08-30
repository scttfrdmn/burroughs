// Command opcodegen regenerates the opcode immediate-shape table from the vendored
// reference interpreter (decision 0007).
//
// Usage:
//
//	go run ./internal/gen/opcodegen/cmd/opcodegen -o internal/binary/optable.go
//
// or, normally, 'make opcodes'. The output is committed, so a fresh clone builds with
// no fetch; 'make opcode-drift' asserts the committed file still agrees with the
// reference. Each revision is read from its own pin's fetch script rather than passed in,
// because a SHA typed at a second site is a citation that can drift from the pin it
// claims to describe — and there are two pins as of #524, the atomics region being
// composed from the threads proposal's decoder.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/opcodegen"
)

func main() {
	out := flag.String("o", "", "output file (default stdout)")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "opcodegen: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	// One call, because a generator and its drift check are one fact about how the artifact is
	// made: this used to read `testenv.RefDecodeML` and extract it here, which was correct for
	// as long as there was one authority. See opcodegen.BuildFromPins.
	tab, err := opcodegen.BuildFromPins()
	if err != nil {
		return err
	}
	code, err := tab.Emit()
	if err != nil {
		return err
	}
	formatted, err := gen.GofmtSource(code)
	if err != nil {
		return err
	}
	if out == "" {
		_, err := os.Stdout.WriteString(formatted)
		return err
	}
	return os.WriteFile(out, []byte(formatted), 0o644)
}
