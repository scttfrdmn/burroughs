// Command burroughs is the engine CLI.
//
// v0 surface: run (instantiate a module and call an export), inspect (section-level module dump),
// and version. `run` is a consumer of the public `burroughs` package — decision 0029 — so the CLI
// and an embedding host cross the same path, and neither is covered without the other.
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
	case "run":
		// Nothing is printed here: runCmd reports its own outcome on the writers it is handed, so
		// the whole of what a user sees is inside the function the tests call.
		os.Exit(exitCode(runCmd(os.Stdout, os.Stderr, os.Args[2:])))
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
	fmt.Fprintln(os.Stderr, "usage: burroughs <command> [arguments]")
	fmt.Fprintln(os.Stderr, "  run [--strict] file.wasm [func [value...]]   instantiate and call an export")
	fmt.Fprintln(os.Stderr, "  inspect file.wasm                            section-level module dump")
	fmt.Fprintln(os.Stderr, "  version")
}
