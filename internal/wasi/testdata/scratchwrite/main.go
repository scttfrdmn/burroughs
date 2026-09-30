// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// The seventh guest (ADR 0091): it performs the write cycle a Go test's `t.TempDir()` performs, and prints
// each step's outcome so a test can read the same guest under two different grants.
//
// **It reports every step rather than exiting on the first failure**, because the interesting comparison is
// per step. Under a read-only grant the mkdir is refused and everything downstream is refused *for a
// different reason* — a distinction that a guest which exited at step one would hide.
//
// The `WriteAt` step exists specifically to reach `fd_pwrite`, whose capability is decided by the fd's
// origin rather than by a path, and which cannot be witnessed on its permitted path without a real guest.
//
// Compiled to GOOS=wasip1 by the test; lives under testdata so the toolchain does not build it with the
// package.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func step(name string, err error) {
	if err != nil {
		fmt.Printf("STEP %s ERR %v\n", name, err)
		return
	}
	fmt.Printf("STEP %s ok\n", name)
}

func main() {
	base := "/s"
	if len(os.Args) > 1 {
		base = os.Args[1]
	}
	dir := filepath.Join(base, "d")
	step("mkdir", os.Mkdir(dir, 0o700))

	f := filepath.Join(dir, "f.txt")
	step("writefile", os.WriteFile(f, []byte("scratch-body"), 0o600))

	// `WriteAt` is `fd_pwrite`. Opened O_RDWR so the fd carries write intent from `path_open`.
	h, oerr := os.OpenFile(filepath.Join(dir, "p.txt"), os.O_CREATE|os.O_RDWR, 0o600)
	step("open-rdwr", oerr)
	if oerr == nil {
		_, werr := h.WriteAt([]byte("pwrite"), 4)
		step("writeat", werr)
		step("close", h.Close())
	}

	b, rerr := os.ReadFile(f)
	step("readback", rerr)
	if rerr == nil && string(b) != "scratch-body" {
		step("readback-content", fmt.Errorf("read %q", b))
	}

	step("unlink", os.Remove(f))
	step("rmdir-nonempty-refused", os.Remove(dir)) // p.txt is still there, so this must fail
	step("unlink-p", os.Remove(filepath.Join(dir, "p.txt")))
	step("rmdir", os.Remove(dir))
	fmt.Println("SCRATCH-END")
}
