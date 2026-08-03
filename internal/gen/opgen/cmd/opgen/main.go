// Command opgen regenerates the wat mnemonic→opcode table by joining the two tables already
// generated from the reference interpreter (decision 0014).
//
// Usage:
//
//	go run ./internal/gen/opgen/cmd/opgen -o internal/text/opcodes.go
//
// or, normally, 'make opcodes-text'. The output is committed, so a fresh clone builds with no
// fetch; 'make opcodes-text-drift' asserts the committed file still agrees with the reference.
//
// Three sources, one revision: decode.ml, parser.mly, lexer.mll are all read at the SHA the
// fetch script pins, and the join is only meaningful if they are the same revision. The SHA is
// read from that script rather than passed in, for the reason keywordgen's cmd states — a SHA
// typed at a second site is a citation that can drift from the pin it claims to describe.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/opgen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

func main() {
	out := flag.String("o", "", "output file (default stdout)")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "opgen: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	sha, err := gen.PinnedRefRev()
	if err != nil {
		return err
	}

	srcs := map[string]string{}
	for _, p := range []string{testenv.RefDecodeML, testenv.RefParserMLY, testenv.RefLexerMLL} {
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return fmt.Errorf("%w (run: make spec-ref)", rerr)
		}
		srcs[p] = string(b)
	}

	tab, err := opgen.Join(srcs[testenv.RefDecodeML], srcs[testenv.RefParserMLY], srcs[testenv.RefLexerMLL], sha)
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
