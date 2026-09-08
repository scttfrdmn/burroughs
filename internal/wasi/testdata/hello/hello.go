// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// This is the guest, not part of the wasi package: it is compiled to GOOS=wasip1 by the test and run
// on Burroughs. It lives under testdata so the toolchain does not build it with the package (ADR
// 0080, decision 5). It exercises fmt (the runtime, formatting, and fd_write to stdout) and returns
// a non-trivial exit path is deliberately avoided — the slice's result is binary: it ran or it did
// not.
package main

import "fmt"

func main() {
	fmt.Println("hello from a go guest on burroughs")
}
