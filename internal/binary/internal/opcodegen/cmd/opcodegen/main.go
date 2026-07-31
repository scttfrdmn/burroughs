// Command opcodegen regenerates the opcode immediate-shape table from the vendored
// reference interpreter (decision 0007).
//
// Usage:
//
//	go run ./internal/binary/internal/opcodegen/cmd/opcodegen -o internal/binary/optable.go
//
// or, normally, 'make opcodes'. The output is committed, so a fresh clone builds with
// no fetch; 'make opcode-drift' asserts the committed file still agrees with the
// reference. The revision is read from scripts/fetch-spec-ref.sh rather than passed in,
// because a SHA typed at a second site is a citation that can drift from the pin it
// claims to describe.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/binary/internal/opcodegen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
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
	sha, err := opcodegen.PinnedRev("scripts/fetch-spec-ref.sh")
	if err != nil {
		return err
	}
	src, err := os.ReadFile(testenv.RefDecodeML)
	if err != nil {
		return fmt.Errorf("%w (run: make spec-ref)", err)
	}
	tab, err := opcodegen.Extract(string(src), sha)
	if err != nil {
		return err
	}
	code, err := tab.Emit()
	if err != nil {
		return err
	}
	formatted, err := opcodegen.GofmtSource(code)
	if err != nil {
		return err
	}
	if out == "" {
		_, err := os.Stdout.WriteString(formatted)
		return err
	}
	return os.WriteFile(out, []byte(formatted), 0o644)
}
