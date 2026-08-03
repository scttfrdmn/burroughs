// Command keywordgen regenerates the wat keyword table from the vendored reference
// interpreter's text lexer (decision 0009).
//
// Usage:
//
//	go run ./internal/gen/keywordgen/cmd/keywordgen -o internal/text/keywords.go
//
// or, normally, 'make keywords'. The output is committed, so a fresh clone builds with no
// fetch; 'make keyword-drift' asserts the committed file still agrees with the reference.
// The revision is read from scripts/fetch-spec-ref.sh rather than passed in, because a
// SHA typed at a second site is a citation that can drift from the pin it claims to
// describe.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

func main() {
	out := flag.String("o", "", "output file (default stdout)")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "keywordgen: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	sha, err := gen.PinnedRefRev()
	if err != nil {
		return err
	}
	src, err := os.ReadFile(testenv.RefLexerMLL)
	if err != nil {
		return fmt.Errorf("%w (run: make spec-ref)", err)
	}
	tab, err := keywordgen.Extract(string(src), sha)
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
