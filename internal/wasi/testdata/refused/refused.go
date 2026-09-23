// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// The guest that makes #816's refusals FIRE. Every call below reaches a preview-1 function this engine
// supplies-but-refuses, so the test can witness the refusal happening rather than infer it from the
// permit path working (#714's lesson: a gated entry's forecast registers the refusal).
//
// It reports what the guest OBSERVED for each, because a refusal has two halves — the host counted it,
// and the guest saw a failure — and a test asserting only the first would pass against a refusal the
// guest silently ignored.
package main

import (
	"fmt"
	"os"
)

func main() {
	f, err := os.Open("/d/payload.txt")
	if err != nil {
		fmt.Printf("OPEN-ERR %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	// fd_seek
	_, serr := f.Seek(3, 0)
	fmt.Printf("SEEK err=%v\n", serr != nil)

	// path_create_directory — the one a measured guest actually calls, whose refusal archive/zip absorbed
	fmt.Printf("MKDIR err=%v\n", os.Mkdir("/d/newdir", 0o755) != nil)

	// path_unlink_file
	fmt.Printf("REMOVE err=%v\n", os.Remove("/d/payload.txt") != nil)

	// path_symlink
	fmt.Printf("SYMLINK err=%v\n", os.Symlink("/d/payload.txt", "/d/link") != nil)

	// path_readlink
	_, lerr := os.Readlink("/d/payload.txt")
	fmt.Printf("READLINK err=%v\n", lerr != nil)

	// fd_filestat_set_size
	fmt.Printf("TRUNCATE err=%v\n", f.Truncate(2) != nil)

	// path_remove_directory
	fmt.Printf("RMDIR err=%v\n", os.Remove("/d/subdir") != nil)
}
