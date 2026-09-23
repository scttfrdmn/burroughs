// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// The guest for #816's two implemented functions. It is compiled to GOOS=wasip1 by the test and run on
// Burroughs; it lives under testdata so the toolchain does not build it with the package (ADR 0080,
// decision 5).
//
// It drives `fd_pread` and `fd_readdir` through the SAME Go API calls the real consumer used — `ReadAt`
// and `os.ReadDir`, as `archive/zip` does — rather than through a hand-rolled syscall, so a pass here is
// evidence about the path a guest actually takes.
package main

import (
	"fmt"
	"os"
	"sort"
)

func main() {
	// fd_readdir: list the granted directory. Sorted, because the host returns the host's order and a
	// test that depended on it would be asserting a property of the filesystem.
	ents, err := os.ReadDir("/d")
	if err != nil {
		fmt.Printf("READDIR-ERR %v\n", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		kind := "f"
		if e.IsDir() {
			kind = "d"
		}
		names = append(names, kind+":"+e.Name())
	}
	sort.Strings(names)
	fmt.Printf("READDIR %d %v\n", len(names), names)

	// fd_pread: read at an offset WITHOUT moving the fd's own offset. The second assertion is the one
	// that distinguishes pread from seek+read: a plain Read afterwards must still start at 0.
	f, err := os.Open("/d/payload.txt")
	if err != nil {
		fmt.Printf("OPEN-ERR %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	at := make([]byte, 5)
	n, err := f.ReadAt(at, 6)
	if err != nil {
		fmt.Printf("PREAD-ERR %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("PREAD %d %q\n", n, string(at[:n]))

	head := make([]byte, 5)
	m, err := f.Read(head)
	if err != nil {
		fmt.Printf("READ-ERR %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("AFTER-PREAD-READ %d %q\n", m, string(head[:m]))

	// A pread past the end is a short read with io.EOF, not an engine error.
	tail := make([]byte, 8)
	k, eerr := f.ReadAt(tail, 9)
	fmt.Printf("PREAD-EOF %d %q err=%v\n", k, string(tail[:k]), eerr != nil)
}
