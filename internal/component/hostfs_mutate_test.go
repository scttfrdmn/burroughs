// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// fsFixtureRow is one row of fs-mutate.wasm or fs-rest.wasm, in its `run(row)` order, which is its reading's
// row-line order.
type fsFixtureRow struct {
	name string
	// readOnly runs the row on a read-only first grant, whose reading is the WIT's, since wasmtime has none.
	readOnly bool
	// links marks a row that reads a symlink the tree makes, and posix one whose reading rests on POSIX's errnos or
	// rename semantics, which Windows reports through codes of its own. Neither is asked off darwin and linux.
	links, posix bool
	// trap marks a row whose run traps.
	trap bool
}

var fsMutateRows = []fsFixtureRow{
	{name: "mkdir"},
	{name: "mkdir-errors", posix: true},
	{name: "mkdir-escapes", links: true},
	{name: "mkdir-nested"},
	{name: "unlink", links: true},
	{name: "unlink-kinds", links: true},
	{name: "unlink-escapes", links: true},
	{name: "rmdir", posix: true},
	{name: "rmdir-kinds", links: true},
	{name: "rmdir-escapes", links: true},
	{name: "rename"},
	{name: "rename-over", posix: true},
	{name: "rename-errors", posix: true},
	{name: "rename-escapes", links: true},
	{name: "rename-two"},
	{name: "rename-nested-escape"},
	{name: "rename-two-calls"},
	{name: "rename-cross-grant"},
	// Windows refuses to rename a directory a handle is open on, and the row holds one on sub.
	{name: "rename-stale", posix: true},
	{name: "base-file"},
	{name: "child-read"},
	{name: "child-read-more"},
	{name: "sync-lower"},
	{name: "read-only", readOnly: true},
	{name: "rename-dir-over-empty", posix: true},
}

// fsTrees makes the three siblings fs-mutate.reading's (mutate) or fs-rest.reading's header names in a fresh temporary
// directory, and returns it: tree/, the first grant; tree2/, the second; and outside/, which an escape that was not
// refused would reach. The symlinks are made only where an unprivileged test can make one.
func fsTrees(t *testing.T, mutate bool) string {
	t.Helper()
	base := t.TempDir()
	files := map[string]string{
		"tree/hello.txt": "hello world", "tree/w.txt": "0123456789", "tree/sub/inner.txt": "inner",
		"tree2/other.txt": "other", "outside/victim.txt": "v",
	}
	var dirs []string
	if mutate {
		files["tree/full/f"] = "x"
		dirs = []string{"tree/empty", "outside/victim"}
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		p := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if fsPosix() {
		links := map[string]string{
			"in-link": "hello.txt", "dir-link": "sub", "up-link": "..", "out-link": filepath.Join(base, "outside"),
		}
		if !mutate {
			links["rel-out"] = "../outside"
		}
		for name, target := range links {
			if err := os.Symlink(target, filepath.Join(base, "tree", name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return base
}

func fsPosix() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "linux" }

// runFSFixture instantiates wasm over base's two trees, tree/ at "/" (read-only when readOnly) and tree2/ at "/b",
// calls run(row) under a hang guard, and closes the instance.
func runFSFixture(t *testing.T, wasm, base string, row int, readOnly bool) (uint32, error) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", wasm))
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.Preopens = []Preopen{
		{Host: filepath.Join(base, "tree"), Guest: "/", Writable: !readOnly},
		{Host: filepath.Join(base, "tree2"), Guest: "/b", Writable: true},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	res, errc := make(chan []canon.Value, 1), make(chan error, 1)
	go func() {
		r, e := in.CallValues("run", canon.U32(uint32(row)))
		if e != nil {
			errc <- e
			return
		}
		res <- r
	}()
	select {
	case r := <-res:
		v, _ := r[0].U32()
		return v, nil
	case e := <-errc:
		return 0, e
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatalf("run(%d) neither returned nor trapped within the 10s hang guard", row)
	}
	return 0, nil
}

// fsHostState is one path beneath base as an after-check names it: a file's content, `<dir>`, `<link>` or `<absent>`.
func fsHostState(base, rel string) string {
	p := filepath.Join(base, filepath.FromSlash(rel))
	fi, err := os.Lstat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "<absent>"
	case err != nil:
		return "<" + err.Error() + ">"
	case fi.Mode()&os.ModeSymlink != 0:
		return "<link>"
	case fi.IsDir():
		return "<dir>"
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// fsListing is dir's entries beneath base, sorted, each with its state.
func fsListing(t *testing.T, base, dir string) string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(base, filepath.FromSlash(dir)))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name()+"="+fsHostState(base, dir+"/"+e.Name()))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// fsNothingEscaped is every row's after-check: the temporary directory still holds only the three trees, and
// outside/ is as it was made, so no row reached past its grant.
func fsNothingEscaped(t *testing.T, base string, mutate bool) {
	t.Helper()
	if got, want := fsListing(t, base, "."), "outside=<dir> tree2=<dir> tree=<dir>"; got != want {
		t.Errorf("the trees' parent holds %q, want %q: a row reached past its grant", got, want)
	}
	want := "victim.txt=v"
	if mutate {
		want = "victim.txt=v victim=<dir>"
	}
	if got := fsListing(t, base, "outside"); got != want {
		t.Errorf("outside/ holds %q, want %q: a row reached past its grant", got, want)
	}
	// Every instant a row sets is 2023-11-14 (1.7e9) or earlier, and outside/ was made at the row's start.
	if fi, err := os.Lstat(filepath.Join(base, "outside", "victim.txt")); err == nil && fi.ModTime().Unix() <= 1700000000 {
		t.Errorf("outside/victim.txt's modification time is %v: a row set the times of a file past its grant", fi.ModTime())
	}
}

// fsMutateAfter is each fs-mutate row's own after-check: what the row must have written, or must not have.
var fsMutateAfter = map[string]map[string]string{
	"mkdir":                 {"tree/newdir": "<dir>"},
	"mkdir-nested":          {"tree/sub/deeper": "<dir>"},
	"unlink":                {"tree/w.txt": "<absent>", "tree/in-link": "<absent>", "tree/hello.txt": "hello world"},
	"unlink-kinds":          {"tree/sub/inner.txt": "inner", "tree/empty": "<dir>", "tree/dir-link": "<absent>"},
	"rmdir":                 {"tree/empty": "<absent>", "tree/full/f": "x"},
	"rmdir-kinds":           {"tree/dir-link": "<link>", "tree/sub/inner.txt": "inner", "tree/hello.txt": "hello world"},
	"rename":                {"tree/moved.txt": "hello world", "tree/hello.txt": "<absent>"},
	"rename-over":           {"tree/hello.txt": "0123456789", "tree/w.txt": "<absent>", "tree/full/f": "x", "tree/empty": "<dir>", "tree/sub/inner.txt": "inner"},
	"rename-escapes":        {"tree/hello.txt": "hello world", "tree/stolen": "<absent>"},
	"rename-two":            {"tree/sub/moved.txt": "hello world", "tree/inner-up.txt": "inner", "tree/hello.txt": "<absent>", "tree/sub/inner.txt": "<absent>"},
	"rename-nested-escape":  {"tree/hello.txt": "hello world", "tree/sub/inner.txt": "inner", "tree/x": "<absent>", "tree/sub/x": "<absent>"},
	"rename-two-calls":      {"tree/moved.txt": "hello world", "tree/hello.txt": "<absent>"},
	"rename-cross-grant":    {"tree/hello.txt": "hello world", "tree2/moved.txt": "<absent>", "tree2/other.txt": "other", "tree/x": "<absent>"},
	"rename-stale":          {"tree/sub2": "<dir>", "tree/sub": "<absent>", "tree/sub2/moved.txt": "<absent>", "tree/hello.txt": "hello world"},
	"child-read":            {"tree/sub/inner.txt": "inner", "tree/sub/deeper": "<absent>", "tree/x": "<absent>"},
	"child-read-more":       {"tree/hello.txt": "hello world", "tree/sub/x": "<absent>"},
	"sync-lower":            {"tree/moved.txt": "hello world"},
	"read-only":             {"tree/newdir": "<absent>", "tree/hello.txt": "hello world", "tree/empty": "<dir>", "tree/moved.txt": "<absent>"},
	"rename-dir-over-empty": {"tree/sub/inner.txt": "inner", "tree/empty": "<dir>", "tree/empty/inner.txt": "<absent>", "tree/hello.txt": "hello world"},
}

// checkFSAfter checks a row's own after-check map against the host's tree.
func checkFSAfter(t *testing.T, base, row string, after map[string]map[string]string) {
	t.Helper()
	for rel, want := range after[row] {
		if got := fsHostState(base, rel); got != want {
			t.Errorf("after %s the host's %s is %q, want %q", row, rel, got, want)
		}
	}
}

// TestFSMutationsMatchWasmtime is E1c at the ABI (ADR 0138): every row of fs-mutate.wasm against fs-mutate.reading, each
// on fresh trees, with gate:async on and gate:cm-threads off and on. The reading is wasmtime 49.0.3's (10 of 10 per
// row) but for the read-only-grant row, the WIT's, and the rows where Burroughs departs by ADR 0138, whose wasmtime
// readings the file records beside them. After each row the host's trees are read: the row's own writes, and that
// nothing escaped its grant; and every file goroutine is gone after Close.
//
// **Two kinds of row are not asked off darwin and linux**, by GOOS, and the test logs each: a row that reads a symlink,
// since creating one on Windows needs a privilege the runner may not hold, and a row whose reading rests on POSIX's
// errnos or rename semantics, which Windows reports through codes of its own.
func TestFSMutationsMatchWasmtime(t *testing.T) {
	testFSFixture(t, "fs-mutate", fsMutateRows, true, fsMutateAfter)
}

// testFSFixture runs every row of name.wasm against name.reading, as TestFSMutationsMatchWasmtime states.
func testFSFixture(t *testing.T, name string, rows []fsFixtureRow, mutate bool, after map[string]map[string]string) {
	t.Helper()
	t.Setenv(asyncGateEnv, "1")
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, gate)
			for i, row := range rows {
				if !fsPosix() && (row.links || row.posix) {
					t.Logf("%s: not asked on %s (a symlink, or a POSIX errno, this GOOS does not give)", row.name, runtime.GOOS)
					continue
				}
				t.Run(row.name, func(t *testing.T) {
					want := lineAfter(t, "testdata/"+name+".reading", row.name+": ")
					base := fsTrees(t, mutate)
					v, err := runFSFixture(t, name+".wasm", base, i, row.readOnly)
					switch {
					case row.trap:
						if err == nil || !strings.HasPrefix(want, "trap") {
							t.Errorf("run(%d, %s) = %d, %v; want a trap (%s)", i, row.name, v, err, want)
						}
					case err != nil:
						t.Fatalf("run(%d, %s) = %v", i, row.name, err)
					default:
						if got := strconv.FormatUint(uint64(v), 10); got != want {
							t.Errorf("run(%d, %s) = %s (%#08x), want %s", i, row.name, got, v, want)
						}
					}
					checkFSAfter(t, base, row.name, after)
					fsNothingEscaped(t, base, mutate)
					requireNoFileGoroutines(t)
				})
			}
		})
	}
}

// TestATwoDescriptorCallResolvesEachPathOnItsOwnRoot is ADR 0138's confinement for two descriptors of one grant: each
// path is resolved on its own descriptor's root before the grant's root is asked, so a `..` that leaves its descriptor
// is `not-permitted` although the grant's root would reach where it points. The fixture's rename-nested-escape asks the
// new path's half; its old path's half is asked only here, since its one rename from sub's ../hello.txt names sub twice
// and so is the same-descriptor arm.
func TestATwoDescriptorCallResolvesEachPathOnItsOwnRoot(t *testing.T) {
	for _, c := range []struct {
		name             string
		subFirst         bool // the old path is sub's and the new the grant's, else the other way round
		oldPath, newPath string
	}{
		{"the old path", true, "../hello.txt", "x"},
		{"the new path", false, "hello.txt", "../x"},
	} {
		dir := fsTree(t)
		h := NewHost(io.Discard, io.Discard, nil)
		h.Preopens = []Preopen{{Host: dir, Guest: "/", Writable: true}}
		res := newHostResources()
		grant := fsTestDir(t, res, dir, fsFlagRead|fsFlagMutateDir)
		sub, label := grant.openAt(pathFollow, "sub", openDirectory, fsFlagRead|fsFlagMutateDir)
		if label != "" {
			t.Fatalf("open-at sub: %s", label)
		}
		t.Cleanup(func() { _ = sub.dir.Close() })
		d, n := grant, sub
		if c.subFirst {
			d, n = sub, grant
		}
		if got := h.fsTwoDescriptors(d, c.oldPath, n, c.newPath, renameIn); got != "not-permitted" {
			t.Errorf("%s leaving its descriptor: fsTwoDescriptors = %q, want not-permitted", c.name, got)
		}
		if got := fsHostState(dir, "hello.txt"); got != "hello world" {
			t.Errorf("%s leaving its descriptor: the host's hello.txt is %q afterwards, want \"hello world\"", c.name, got)
		}
		if got := fsHostState(dir, "x"); got != "<absent>" {
			t.Errorf("%s leaving its descriptor: the host's x is %q afterwards, want <absent>", c.name, got)
		}
	}
}

// TestATwoDescriptorCallAcrossGrantsIsCrossDevice is ADR 0138's grant rule at the function rename-at and link-at ask:
// two descriptors of two grants are `cross-device` even when both grants are one host directory, where the grant's
// root would reach both and the recorded-path check would pass. Through the fixture the rule is masked by that check,
// since its two grants are two directories. Two descriptors of one grant are renamed, the control.
func TestATwoDescriptorCallAcrossGrantsIsCrossDevice(t *testing.T) {
	dir := fsTree(t)
	h := NewHost(io.Discard, io.Discard, nil)
	h.Preopens = []Preopen{{Host: dir, Guest: "/", Writable: true}, {Host: dir, Guest: "/again", Writable: true}}
	res := newHostResources()
	first := fsTestDir(t, res, dir, fsFlagRead|fsFlagMutateDir)
	second := fsTestDir(t, res, dir, fsFlagRead|fsFlagMutateDir)
	second.grant = 1
	if got := h.fsTwoDescriptors(first, "hello.txt", second, "moved.txt", renameIn); got != "cross-device" {
		t.Errorf("a rename across two grants of one directory = %q, want cross-device", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "moved.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after the refused rename the host's moved.txt exists (Lstat: %v)", err)
	}
	again := fsTestDir(t, res, dir, fsFlagRead|fsFlagMutateDir)
	if got := h.fsTwoDescriptors(first, "hello.txt", again, "moved.txt", renameIn); got != "" {
		t.Errorf("a rename across two descriptors of one grant = %q, want ok", got)
	}
	if got := fsHostState(dir, "moved.txt"); got != "hello world" {
		t.Errorf("after the rename the host's moved.txt is %q, want \"hello world\"", got)
	}
}
