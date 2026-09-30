// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// The sixth guest (#830): it reports its own environment, so a test can witness what the host passed
// in rather than inferring it from a side effect.
//
// It exists because #830 was found the hard way. `burroughs run` forwarded the host's whole
// environment, and nothing noticed until a leaked `PWD` moved where a relative path resolved — a
// filesystem symptom two removes from its cause. **No guest could say what its environment was**, so
// the only evidence available was indirect. This one says it directly: one `NAME=VALUE` per line,
// sorted, with a count, so a test can assert both what crossed and that nothing else did.
//
// Compiled to GOOS=wasip1 by the test; lives under testdata so the toolchain does not build it with
// the package.
package main

import (
	"fmt"
	"os"
	"sort"
)

func main() {
	env := os.Environ()
	sort.Strings(env)
	for _, kv := range env {
		fmt.Printf("ENV %s\n", kv)
	}
	// The count is printed separately so an empty environment is an assertable observation rather
	// than an absence of output, which is indistinguishable from a guest that failed to start.
	fmt.Printf("ENVCOUNT %d\n", len(env))
}
