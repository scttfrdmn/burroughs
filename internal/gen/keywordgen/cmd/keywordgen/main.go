// Command keywordgen regenerates the wat keyword table from the vendored reference
// interpreters' text lexers (decision 0009).
//
// Usage:
//
//	go run ./internal/gen/keywordgen/cmd/keywordgen -o internal/text/keywords.go
//
// or, normally, 'make keywords'. The output is committed, so a fresh clone builds with no
// fetch; 'make keyword-drift' asserts the committed file still agrees with every reference.
// Each revision is read from that pin's own fetch script rather than passed in, because a
// SHA typed at a second site is a citation that can drift from the pin it claims to
// describe.
//
// **The authorities are the pin set, not this file's own list.** The wat grammar is the union
// of the tracked set (contract §9 G-2), so every pin that licenses a text lexer contributes;
// the core pin is the base and each proposal pin adds only what the base lacks. Which pins
// those are is `testenv.RefPins`' answer, so a pin added there is read here on arrival.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/keywordgen"
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
	tab, err := keywordgen.BuildFromPins()
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
