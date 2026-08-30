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
// Three sources per pin, one revision *per pin*: decode.ml, parser.mly and lexer.mll are read at
// the SHA that pin's own fetch script names, and the join of one pin's three files is only
// meaningful if they are the same revision. Across pins they are not, and must not be — the core
// pin is at bdd7164 and the threads pin at cc535ad, so provenance is per authority and the
// composition is base-wins. No SHA is spelled here, for the reason keywordgen's cmd states: a SHA
// typed at a second site is a citation that can drift from the pin it claims to describe.
//
// The whole build is opgen.BuildFromPins, not a loop here, because the drift control has to build
// the table the same way this command does or it compares the committed file against a
// differently-composed one.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/opgen"
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
	tab, err := opgen.BuildFromPins()
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
