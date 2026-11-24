// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// fsRow is one row of fs-descriptor.wasm, in its `run(row)` order, which is fs-descriptor.reading's line order.
type fsRow struct {
	name string
	// readOnly runs the row on a read-only grant, whose reading is the WIT's, since wasmtime has none. Every other
	// row runs on a writable grant, wasmtime's `--dir`.
	readOnly bool
	// links marks a row that reads a symlink the tree makes, and stats marks one that reads statExtra's fields.
	links, stats bool
}

var fsRows = []fsRow{
	{name: "preopens"},
	{name: "open-read"},
	{name: "content"},
	{name: "offset"},
	{name: "stat"},
	{name: "stat-at"},
	{name: "stat-at-kinds", links: true},
	{name: "stat-fields", stats: true},
	{name: "get-flags"},
	{name: "get-type"},
	{name: "write-at"},
	{name: "write-content"},
	{name: "write-read-only-fd"},
	{name: "escapes", links: true},
	{name: "symlinks", links: true},
	{name: "in-grant", links: true},
	{name: "errors"},
	{name: "nested"},
	{name: "sync-lower"},
	{name: "create"},
	{name: "read-only-opens", readOnly: true},
	{name: "read-only-truncate", readOnly: true},
}

// fsTree makes fs-descriptor's directory, as fs-descriptor.reading's header names it, in a fresh temporary directory.
func fsTree(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range map[string]string{"hello.txt": "hello world", "w.txt": "0123456789", "sub/inner.txt": "inner"} {
		p := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("hello.txt", filepath.Join(d, "in-link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc", filepath.Join(d, "out-link")); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func fsDescriptorReading(t *testing.T, row string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/fs-descriptor.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, row+": "); ok {
			return rest
		}
	}
	t.Fatalf("fs-descriptor.reading has no row %q", row)
	return ""
}

// startFSRow instantiates fs-descriptor.wasm over dir, granted at "/", and calls run(row) on a goroutine.
func startFSRow(t *testing.T, dir string, row int, writable bool) (*Instantiated, <-chan []canon.Value, <-chan error) {
	t.Helper()
	b, err := os.ReadFile("testdata/fs-descriptor.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.Preopens = []Preopen{{Host: dir, Guest: "/", Writable: writable}}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	res, errc := make(chan []canon.Value, 1), make(chan error, 1)
	go func() {
		r, e := in.CallValues("run", canon.U32(uint32(row)))
		if e != nil {
			errc <- e
			return
		}
		res <- r
	}()
	return in, res, errc
}

// runFSRow runs one row to its result, closes the instance, and returns the u32.
func runFSRow(t *testing.T, dir string, row int, writable bool) uint32 {
	t.Helper()
	in, res, errc := startFSRow(t, dir, row, writable)
	defer in.Close()
	select {
	case r := <-res:
		v, _ := r[0].U32()
		return v
	case e := <-errc:
		t.Fatalf("run(%d) = %v", row, e)
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatalf("run(%d) neither returned nor trapped within the 10s hang guard", row)
	}
	return 0
}

// TestFSDescriptorMatchesWasmtime is E1a at the ABI (ADR 0133): every row of fs-descriptor.wasm against wasmtime 49.0.3's
// fs-descriptor.reading (10 of 10 per row), each on a fresh tree, with gate:async on and gate:cm-threads off and on. The
// two read-only rows run on a read-only grant and are asserted against the WIT's values the reading records, since
// wasmtime's CLI has no read-only grant. The rows that write are also read on the host after the run, and every file
// goroutine is gone after Close.
//
// **Two kinds of row are not asked off darwin and linux**, by GOOS, and the test logs each: a row that reads a
// symlink, since creating one on Windows needs a privilege the runner may not hold, and stat-fields, since statExtra
// reads no stat record there (ADR 0133 names the fallback).
func TestFSDescriptorMatchesWasmtime(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	posix := runtime.GOOS == "darwin" || runtime.GOOS == "linux"
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, gate)
			for i, row := range fsRows {
				if !posix && (row.links || row.stats) {
					t.Logf("%s: not asked on %s (a symlink or a stat record this GOOS does not give)", row.name, runtime.GOOS)
					continue
				}
				t.Run(row.name, func(t *testing.T) {
					want := fsDescriptorReading(t, row.name)
					dir := fsTree(t)
					v := runFSRow(t, dir, i, !row.readOnly)
					if got := strconv.FormatUint(uint64(v), 10); got != want {
						t.Errorf("run(%d, %s) = %s (%#08x), want %s", i, row.name, got, v, want)
					}
					fsHostAfter(t, dir, row.name)
					requireNoFileGoroutines(t)
				})
			}
		})
	}
}

// fsHostAfter reads the host's tree after a row that writes, or must not have written.
func fsHostAfter(t *testing.T, dir, row string) {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "<" + err.Error() + ">"
		}
		return string(b)
	}
	want := map[string]map[string]string{
		"write-at":           {"w.txt": "012ABCDEFGHIJKLMNO"},
		"write-content":      {"w.txt": "012ABCDEFGHIJKLMNO"},
		"create":             {"new.txt": "AB"},
		"write-read-only-fd": {"hello.txt": "hello world"},
		"read-only-truncate": {"hello.txt": "hello world"},
	}[row]
	for name, body := range want {
		if got := read(name); got != body {
			t.Errorf("after %s the host's %s reads %q, want %q", row, name, got, body)
		}
	}
	if row == "read-only-opens" {
		if _, err := os.Lstat(filepath.Join(dir, "new.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("after read-only-opens the host's new.txt exists (Lstat: %v): a read-only grant created a file", err)
		}
	}
}

// fileGoroutines are E1a's goroutines still running: a write-via-stream writer, by the function that created it, and a
// read-via-stream producer's Read of a file, by its creator and the SectionReader it reads.
func fileGoroutines() []string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		created := ""
		if i := strings.LastIndex(g, "created by "); i >= 0 {
			created = g[i:]
		}
		if strings.Contains(created, "(*Host).fsWriteViaStream") ||
			(strings.Contains(created, "(*streamProducer).startReadLocked") && strings.Contains(g, "(*SectionReader).Read")) {
			out = append(out, g)
		}
	}
	return out
}

// requireNoFileGoroutines fails if a file goroutine is still running, after a hang guard for one that has called its
// ended and not yet returned.
func requireNoFileGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second) // a harness hang guard, not a measurement
	for {
		gs := fileGoroutines()
		if len(gs) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d file goroutine(s) outlived Close:\n%s", len(gs), strings.Join(gs, "\n\n"))
		}
		runtime.Gosched()
	}
}

// TestTheEscapeErrorIsTheToolchainsOwn pins fsEscapeMessage to the toolchain (ADR 0133): os.Root's refusal of a path
// that leaves its root must carry exactly that text, since fsErrorLabel matches it by string, Go's error being
// unexported. A toolchain that rewords it fails here rather than turning every escape into `other`.
func TestTheEscapeErrorIsTheToolchainsOwn(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = r.Open("../x")
	if err == nil {
		t.Fatal("os.Root opened ../x")
	}
	inner := err
	for u := errors.Unwrap(inner); u != nil; u = errors.Unwrap(inner) {
		inner = u
	}
	if inner.Error() != fsEscapeMessage {
		t.Fatalf("os.Root's escape error reads %q at its root, want %q: fsEscapeMessage no longer matches the toolchain",
			inner.Error(), fsEscapeMessage)
	}
	if got := fsErrorLabel(err); got != "not-permitted" {
		t.Errorf("fsErrorLabel(%v) = %s, want not-permitted", err, got)
	}
}

// TestAnAbsolutePathIsRefusedBeforeTheCall is ADR 0133's absolute-path rule, at the function the three path methods
// ask: a path that starts with a separator, or names a volume, is refused before os.Root is asked, so the refusal does
// not rest on the escape error's text. Through the fixture the rule is invisible, since os.Root refuses `/etc` too.
//
// NEUTER, run: fsAbsolute returning false, and the absolute rows here fail.
func TestAnAbsolutePathIsRefusedBeforeTheCall(t *testing.T) {
	rows := map[string]bool{"/etc": true, "/": true, "hello.txt": false, "sub/../x": false, "": false, "../x": false}
	if runtime.GOOS == "windows" {
		rows[`\x`] = true
		rows[`C:\x`] = true
		rows["C:x"] = true
	}
	for p, want := range rows {
		if got := fsAbsolute(p); got != want {
			t.Errorf("fsAbsolute(%q) = %v, want %v", p, got, want)
		}
	}
}

// fsTestDir is a grant's descriptor over dir, as get-directories mints it, in res's registry.
func fsTestDir(t *testing.T, res *hostResources, dir string, flags uint8) *fsDescriptor {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Closed before TempDir's RemoveAll, which runs later (cleanups run last-registered first): Windows refuses to
	// delete a directory a handle is open on, so the grant root this helper opens would fail the test's cleanup.
	t.Cleanup(func() { _ = r.Close() })
	return &fsDescriptor{fs: res.fs, path: ".", flags: flags, dir: r}
}

// fileClosed reports whether f has been closed, by closing it again: a second Close returns os.ErrClosed on every
// platform, while a first one returns nil. (*os.File).Stat cannot ask this portably. On Windows it does not check the
// file's closed state, and calls GetFileType on the stale handle, answering "The handle is invalid" for a file that
// was closed (#1388's first CI run). Closing an open file here is harmless, since the witness has already failed.
func fileClosed(f *os.File) bool {
	return errors.Is(f.Close(), os.ErrClosed)
}

// TestADroppedDescriptorClosesItsFile is the descriptor's destructor (ADR 0133), a leak witness: a guest's drop of a
// file descriptor closes the file, and of a directory descriptor its root, rather than leaving either to the collector.
func TestADroppedDescriptorClosesItsFile(t *testing.T) {
	res := newHostResources()
	base := fsTestDir(t, res, fsTree(t), fsFlagRead)
	file, label := base.openAt(pathFollow, "hello.txt", 0, fsFlagRead)
	if label != "" {
		t.Fatalf("open-at hello.txt: %s", label)
	}
	sub, label := base.openAt(pathFollow, "sub", openDirectory, fsFlagRead)
	if label != "" {
		t.Fatalf("open-at sub: %s", label)
	}
	fi, si := res.mint(descriptorResource, file), res.mint(descriptorResource, sub)
	if err := res.drop(descriptorResource, fi); err != nil {
		t.Fatalf("drop of the file descriptor: %v", err)
	}
	if !fileClosed(file.file.f) {
		t.Errorf("after the drop the file is open: the destructor left it open")
	}
	if err := res.drop(descriptorResource, si); err != nil {
		t.Fatalf("drop of the directory descriptor: %v", err)
	}
	if _, err := sub.dir.Stat("."); err == nil {
		t.Error("after the drop the directory's root still answers Stat: the destructor left it open")
	}
	if n := len(res.fs.files); n != 0 {
		t.Errorf("the registry still holds %d file(s) after the only descriptor's drop", n)
	}
}

// TestAStreamOutlivesItsDroppedDescriptor is the file's reference count (ADR 0133): a stream's reference keeps the
// file open after its descriptor is dropped, and the last release closes it.
func TestAStreamOutlivesItsDroppedDescriptor(t *testing.T) {
	res := newHostResources()
	base := fsTestDir(t, res, fsTree(t), fsFlagRead)
	d, label := base.openAt(pathFollow, "hello.txt", 0, fsFlagRead)
	if label != "" {
		t.Fatalf("open-at hello.txt: %s", label)
	}
	if !res.fs.retain(d.file) {
		t.Fatal("retain of a live file failed")
	}
	if err := destroyFSDescriptor(d); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if n, err := d.file.f.ReadAt(buf, 0); err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("after the descriptor's drop the stream's file reads %q, %v; want \"hello\", nil", buf[:n], err)
	}
	res.fs.release(d.file)
	if !fileClosed(d.file.f) {
		t.Errorf("after the last release the file is still open")
	}
}

// TestCloseClosesTheFilesTheGuestLeftOpen is the registry's stop (ADR 0133): a file only a stream still holds, which no
// destructor reaches, is closed by Close's stop, and one a writer holds is left to the writer, which closes it as it
// ends.
func TestCloseClosesTheFilesTheGuestLeftOpen(t *testing.T) {
	res := newHostResources()
	base := fsTestDir(t, res, fsTree(t), fsFlagRead|fsFlagMutateDir)
	held, label := base.openAt(pathFollow, "hello.txt", 0, fsFlagRead)
	if label != "" {
		t.Fatalf("open-at hello.txt: %s", label)
	}
	written, label := base.openAt(pathFollow, "w.txt", 0, fsFlagRead|fsFlagWrite)
	if label != "" {
		t.Fatalf("open-at w.txt: %s", label)
	}
	ended, ok := res.fs.trackWriter(written.file)
	if !ok {
		t.Fatal("trackWriter refused before stop")
	}
	res.fs.stop()
	if !fileClosed(held.file.f) {
		t.Errorf("after stop the held file is still open")
	}
	if _, err := written.file.f.Stat(); err != nil {
		t.Errorf("after stop the writer's file's Stat = %v: stop closed a file its writer still holds", err)
	}
	ended()
	if !fileClosed(written.file.f) {
		t.Errorf("after its writer ended the file is still open")
	}
	res.fs.wait()
	if _, ok := res.fs.track(); ok {
		t.Error("track counted a goroutine after stop")
	}
}

// TestAPreopenThatIsNotADirectoryIsRefusedAtInstantiate is ADR 0133's grant check: a preopen naming a file, or nothing,
// is refused at instantiate, naming it.
func TestAPreopenThatIsNotADirectoryIsRefusedAtInstantiate(t *testing.T) {
	b, err := os.ReadFile("testdata/fs-descriptor.wasm")
	if err != nil {
		t.Fatal(err)
	}
	dir := fsTree(t)
	for _, host := range []string{filepath.Join(dir, "hello.txt"), filepath.Join(dir, "absent")} {
		h := NewHost(io.Discard, io.Discard, nil)
		h.Preopens = []Preopen{{Host: host, Guest: "/"}}
		in, err := InstantiateWithHost(b, h)
		if err == nil {
			in.Close()
			t.Errorf("preopen %s: instantiate succeeded", host)
			continue
		}
		// The refusal quotes the path (%q), so it is matched quoted: on Windows %q doubles every backslash, and the
		// raw path is not a substring of it (#1388's first CI run).
		if !strings.Contains(err.Error(), strconv.Quote(host)) {
			t.Errorf("preopen %s: the refusal does not name it: %v", host, err)
		}
	}
}
