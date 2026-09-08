// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// The second guest (ADR 0080's stub-growth): it reads stdin, transforms it, sleeps briefly, and
// writes the result — chosen because it forces WASI functions the hello guest did not. Reading stdin
// is fd_read (absent from hello's 15 imports); time.Sleep is poll_oneoff under Go's runtime timer.
// Compiled to GOOS=wasip1 by the test; lives under testdata so the toolchain does not build it with
// the package.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	var lines []string
	for sc.Scan() {
		lines = append(lines, strings.ToUpper(sc.Text()))
	}
	// A brief sleep, so the run exercises the runtime's timer path (poll_oneoff) deterministically.
	time.Sleep(time.Millisecond)
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Printf("lines: %d\n", len(lines))
}
