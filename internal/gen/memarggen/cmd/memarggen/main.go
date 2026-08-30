// Command memarggen regenerates the natural-alignment table from the vendored reference
// interpreter's text lexer.
//
// Usage:
//
//	go run ./internal/gen/memarggen/cmd/memarggen -o internal/text/memarg.go
//
// or, normally, 'make memarg'. The output is committed, so a fresh clone builds with no fetch;
// 'make memarg-drift' asserts the committed file still agrees with every pinned reference. One
// lexer per pin, one revision *per pin*, each read from that pin's own fetch script rather than
// passed in — a SHA typed at a second site is a citation that can drift from the pin it claims to
// describe, and a single SHA over a composed table would have to name one pin's revision for rows
// read at another's.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/gen/memarggen"
)

func main() {
	out := flag.String("o", "", "output file (default stdout)")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "memarggen: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	tab, err := memarggen.BuildFromPins()
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
