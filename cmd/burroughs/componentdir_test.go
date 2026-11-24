// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const fsRun = "../../internal/component/testdata/fs-run.wasm"

// TestRunAComponentWithDirAndScratch is Scott's ruling 2 on #1374 (ADR 0133): `burroughs run` accepts `--dir` and
// `--scratch` for a component, in wasip1's one-colon HOST[:/GUEST] grammar, and maps them to ComponentConfig.Preopens.
// fs-run.wasm's exit code is its reading (fs-run.reading): 111 on a read-only grant, whose open of w.txt for write is
// refused `read-only`; 61 on a writable one, wasmtime 49.0.3's `--dir` reading, with the host's w.txt rewritten; and 3
// with neither flag. Each grant is an explicit HOST:/ mapping: a bare `C:\…` is refused at flag parsing on Windows.
//
// Neutered, run: runComponent not passing Preopens, and the --dir and --scratch rows exit 3, granted nothing.
func TestRunAComponentWithDirAndScratch(t *testing.T) {
	t.Setenv("BURROUGHS_COMPONENTS", "")
	t.Setenv("BURROUGHS_ASYNC", "1")
	for _, c := range []struct {
		name  string
		flags func(dir string) []string
		code  int
		wantW string
	}{
		{"--dir", func(dir string) []string { return []string{"--dir", dir + ":/"} }, 111, "0123456789"},
		{"--scratch", func(dir string) []string { return []string{"--scratch", dir + ":/"} }, 61, "XY23456789"},
		{"neither", func(string) []string { return nil }, 3, "0123456789"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range map[string]string{"hello.txt": "hello world", "w.txt": "0123456789"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out, errBuf bytes.Buffer
			argv := append(append([]string{"run"}, c.flags(dir)...), fsRun)
			if code := dispatch(&out, &errBuf, argv); code != c.code {
				t.Fatalf("exited %d, want %d\nstderr: %q", code, c.code, errBuf.String())
			}
			b, err := os.ReadFile(filepath.Join(dir, "w.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != c.wantW {
				t.Errorf("after the run the host's w.txt reads %q, want %q", b, c.wantW)
			}
		})
	}
}
