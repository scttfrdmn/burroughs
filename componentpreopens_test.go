// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// fsRunTree is fs-run's directory: hello.txt and w.txt, as fs-descriptor.reading's tree has them.
func fsRunTree(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range map[string]string{"hello.txt": "hello world", "w.txt": "0123456789"} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// TestPreopensReachBothConfiguredPaths is ComponentConfig.Preopens at the public boundary (ADR 0133, Scott's ruling 1 on
// #1374): Run and LoadComponent each thread it to the host, its zero value grants nothing, the bare LoadComponent
// grants nothing, and Writable is what separates a read-only grant from a writable one.
//
//   - LoadComponent: fs-descriptor.wasm's run(0) reads get-directories, 19857665 (one preopen named "/") on a grant,
//     wasmtime 49.0.3's fs-descriptor.reading, and 0 on none.
//   - Run: fs-run.wasm exits 111 on a read-only grant, 61 on a writable one, with the host's w.txt rewritten, and 3 on
//     none (fs-run.reading).
//
// Neutered, run: either path's `h.Preopens = c.hostPreopens()` dropped, and that path's granted rows read none.
func TestPreopensReachBothConfiguredPaths(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	t.Run("LoadComponent", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/fs-descriptor.wasm")
		if err != nil {
			t.Fatal(err)
		}
		dir := fsRunTree(t)
		for _, c := range []struct {
			name string
			load func() (*burroughs.Component, error)
			want uint32
		}{
			{"granted", func() (*burroughs.Component, error) {
				return burroughs.ComponentConfig{Preopens: []burroughs.Preopen{{Host: dir, Guest: "/"}}}.LoadComponent(wasm)
			}, 19857665},
			{"zero config", func() (*burroughs.Component, error) { return burroughs.ComponentConfig{}.LoadComponent(wasm) }, 0},
			{"bare", func() (*burroughs.Component, error) { return burroughs.LoadComponent(wasm) }, 0},
		} {
			comp, err := c.load()
			if err != nil {
				t.Fatalf("%s: LoadComponent: %v", c.name, err)
			}
			res, err := comp.Call(context.Background(), "run", burroughs.ComponentU32(0))
			if err != nil {
				comp.Close()
				t.Fatalf("%s: run(0): %v", c.name, err)
			}
			if v, _ := res[0].U32(); v != c.want {
				t.Errorf("%s: run(0) = %d, want %d", c.name, v, c.want)
			}
			comp.Close()
		}
	})
	t.Run("Run", func(t *testing.T) {
		wasm, err := os.ReadFile("internal/component/testdata/fs-run.wasm")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name    string
			grant   func(dir string) []burroughs.Preopen
			want    int
			wantW   string
			granted bool
		}{
			{"read-only", func(dir string) []burroughs.Preopen { return []burroughs.Preopen{{Host: dir, Guest: "/"}} }, 111, "0123456789", true},
			{"writable", func(dir string) []burroughs.Preopen {
				return []burroughs.Preopen{{Host: dir, Guest: "/", Writable: true}}
			}, 61, "XY23456789", true},
			{"none", func(string) []burroughs.Preopen { return nil }, 3, "0123456789", false},
		} {
			dir := fsRunTree(t)
			code, err := burroughs.ComponentConfig{Preopens: c.grant(dir)}.Run(wasm)
			if err != nil {
				t.Fatalf("%s: Run: %v", c.name, err)
			}
			if code != c.want {
				t.Errorf("%s: Run exited %d, want %d", c.name, code, c.want)
			}
			b, err := os.ReadFile(filepath.Join(dir, "w.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != c.wantW {
				t.Errorf("%s: after Run the host's w.txt reads %q, want %q", c.name, b, c.wantW)
			}
		}
	})
}

// TestAPreopenThatIsNotADirectoryFailsTheRun is ADR 0133's grant check at the public boundary: a Host that names a file
// fails Run and LoadComponent, naming it, before the component runs.
func TestAPreopenThatIsNotADirectoryFailsTheRun(t *testing.T) {
	wasm, err := os.ReadFile("internal/component/testdata/fs-run.wasm")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(fsRunTree(t), "hello.txt")
	cfg := burroughs.ComponentConfig{Preopens: []burroughs.Preopen{{Host: file, Guest: "/"}}}
	// The refusal quotes the path (%q), which on Windows doubles every backslash, so it is matched quoted.
	if _, err = cfg.Run(wasm); err == nil || !strings.Contains(err.Error(), strconv.Quote(file)) {
		t.Errorf("Run over a file preopen: err = %v, want one naming %s", err, file)
	}
	comp, err := cfg.LoadComponent(wasm)
	if err == nil {
		comp.Close()
	}
	if err == nil || !strings.Contains(err.Error(), strconv.Quote(file)) {
		t.Errorf("LoadComponent over a file preopen: err = %v, want one naming %s", err, file)
	}
}
