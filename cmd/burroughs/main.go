// Command burroughs is the engine CLI.
//
// v0 surface: inspect (section-level module dump) and version.
// The run subcommand arrives with the interpreter (decision 0002).
package main

import (
	"fmt"
	"os"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

const banner = "burroughs v0.0.1 — the machine Burroughs never built"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(banner)
	case "inspect":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		if err := inspect(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "burroughs:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func inspect(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, err := binary.DecodeModule(data)
	if err != nil {
		return err
	}
	fmt.Printf("%s: wasm v%d, %d section(s)\n", path, m.Version, len(m.Sections))
	for i, s := range m.Sections {
		fmt.Printf("  [%d] %-10s %6d bytes\n", i, s.ID, len(s.Payload))
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: burroughs <version|inspect file.wasm>")
}
