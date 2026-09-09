// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// The third guest (decision 0083): it reads a filename from argv and reports the file's size. Compiled
// to GOOS=wasip1 by the test and run on Burroughs. It exercises the read-only filesystem — args_get,
// path_open, fd_read, fd_close, and the filestat calls — and its exit code distinguishes a granted
// read (0) from a refused one (1), which is what the capability controls assert.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cat <file>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "cat: error:", err)
		os.Exit(1)
	}
	// Report the contents, so a test can tell a real read from a refusal — and so an escape that
	// wrongly succeeded would be visible as the outside file's bytes on stdout.
	fmt.Printf("%s: %d bytes: %s\n", os.Args[1], len(b), string(b))
}
