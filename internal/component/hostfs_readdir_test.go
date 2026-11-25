// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// fsReaddirRow is one row of fs-readdir.wasm, in its `run(row)` order, which is fs-readdir.reading's line order.
type fsReaddirRow struct {
	name string
	// readOnly runs the row on a read-only grant, whose reading is the WIT's, since wasmtime has none. Every other row
	// runs on a writable grant, wasmtime's `--dir`.
	readOnly bool
	// links marks a row that reads the tree's symlink.
	links bool
	// files is how many file descriptors the row leaves open, which the instance's registry still holds when it
	// returns. Every listing it made has been finished or dropped by then, so none of their files is held.
	files int
}

var fsReaddirRows = []fsReaddirRow{
	{name: "empty"},
	{name: "one"},
	{name: "one-name"},
	{name: "one-drain"},
	{name: "many"},
	{name: "many-bytes"},
	{name: "kinds", links: true},
	{name: "names-realloc"},
	{name: "names-content"},
	{name: "one-at-a-time"},
	{name: "one-at-a-time-imm"},
	{name: "batch"},
	{name: "file-fd", files: 1},
	{name: "read-only-one", readOnly: true},
	{name: "no-read-flag"},
	{name: "read-zero"},
	{name: "early-drop"},
}

// fsReaddirTree makes fs-readdir's directory, as fs-readdir.reading's header names it, in a fresh temporary directory.
// The symlink is made only where the platform lets an unprivileged test make one.
func fsReaddirTree(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	files := map[string]string{"hello.txt": "hello world", "one/a.txt": "hello", "kinds/f": "x"}
	for _, n := range []string{"a", "bb", "ccc", "dddd", "eeeee"} {
		files["many/"+n] = "x"
	}
	for _, n := range []string{"p", "q", "r"} {
		files["three/"+n] = "x"
	}
	for _, n := range []string{"x", "é.txt", strings.Repeat("n", 40), strings.Repeat("L", 100)} {
		files["names/"+n] = "x"
	}
	for _, dir := range []string{"empty", "kinds/d"} {
		if err := os.MkdirAll(filepath.Join(d, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		p := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("f", filepath.Join(d, "kinds", "l")); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// lineAfter is the text after `<prefix>` on the first line of file that starts with it.
func lineAfter(t *testing.T, file, prefix string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the committed reading %s is missing, so this witness has no oracle: %v", file, err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, prefix); ok {
			return rest
		}
	}
	t.Fatalf("%s has no line starting %q", file, prefix)
	return ""
}

// fsReaddirPinReading is the pin's reading of a row (refread.reading's `fs-readdir` lines), or "" for a row the pin
// does not read: read-only-one, a grant the CM layer does not see.
func fsReaddirPinReading(t *testing.T, row string) string {
	t.Helper()
	if row == "read-only-one" {
		return ""
	}
	line := lineAfter(t, "testdata/cmthreads/reference/refread.reading", "fs-readdir "+row+" (")
	_, v, ok := strings.Cut(line, "): value ")
	if !ok {
		t.Fatalf("refread.reading's fs-readdir %s line reads %q, not one value: the pin's outcome was not a single value", row, line)
	}
	return v
}

// instantiateReaddir instantiates fs-readdir.wasm over dir, granted at "/".
func instantiateReaddir(t *testing.T, dir string, writable bool) *Instantiated {
	t.Helper()
	b, err := os.ReadFile("testdata/fs-readdir.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.Preopens = []Preopen{{Host: dir, Guest: "/", Writable: writable}}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	return in
}

// callReaddirRow calls run(row) on a goroutine and returns its u32, under a hang guard.
func callReaddirRow(t *testing.T, in *Instantiated, row int) uint32 {
	t.Helper()
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
		return v
	case e := <-errc:
		t.Fatalf("run(%d) = %v", row, e)
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatalf("run(%d) neither returned nor trapped within the 10s hang guard", row)
	}
	return 0
}

// registeredFiles is how many files the instance's registry holds.
func registeredFiles(in *Instantiated) int {
	r := in.w.hostRes.fs
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.files)
}

// TestFSReadDirectoryMatchesBothOracles is E1b at the ABI (ADR 0134): every row of fs-readdir.wasm against wasmtime
// 49.0.3's fs-readdir.reading (10 of 10 per row) and the pin's refread.reading (16 of 16, its host Burroughs' producer
// as ADR 0134 states it), each on a fresh tree, with gate:async on and gate:cm-threads off and on. The read-only row
// runs on a read-only grant and is asserted against the WIT's value the reading records, since wasmtime's CLI has no
// read-only grant. When a row returns, the registry holds only the file descriptors it left open: each listing's file
// was released by its stream's finish or its reader's drop.
//
// **The kinds row is not asked off darwin and linux**, by GOOS, and the test logs it: it reads a symlink, and creating
// one on Windows needs a privilege the runner may not hold.
//
// NEUTER, run: the listing's release (fsReadDirectory's onDone) dropped, and every row but file-fd, whose stream holds
// no file, fails its registry count in both gate states, one listing's file still held: early-drop's after its
// reader's drop, and every other row's after its stream finished.
func TestFSReadDirectoryMatchesBothOracles(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	posix := runtime.GOOS == "darwin" || runtime.GOOS == "linux"
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, gate)
			for i, row := range fsReaddirRows {
				if !posix && row.links {
					t.Logf("%s: not asked on %s (a symlink this GOOS does not let the test make)", row.name, runtime.GOOS)
					continue
				}
				t.Run(row.name, func(t *testing.T) {
					want := lineAfter(t, "testdata/fs-readdir.reading", row.name+": ")
					if pin := fsReaddirPinReading(t, row.name); pin != "" && pin != want {
						t.Fatalf("the oracles disagree on %s: wasmtime %s, the pin %s", row.name, want, pin)
					}
					in := instantiateReaddir(t, fsReaddirTree(t), !row.readOnly)
					v := callReaddirRow(t, in, i)
					held := registeredFiles(in)
					in.Close()
					if got := strconv.FormatUint(uint64(v), 10); got != want {
						t.Errorf("run(%d, %s) = %s (%#08x), want %s", i, row.name, got, v, want)
					}
					if held != row.files {
						t.Errorf("after run(%d, %s) the registry holds %d file(s), want %d: a listing's file outlived its "+
							"stream", i, row.name, held, row.files)
					}
					requireNoFileGoroutines(t)
				})
			}
		})
	}
}

// lockProbeHeap is a heap whose Realloc calls probe first, on the goroutine about to run the guest's realloc.
type lockProbeHeap struct {
	canon.Heap
	probe func()
}

func (p lockProbeHeap) Realloc(origPtr, origSize, align, newSize int) (int, error) {
	p.probe()
	return p.Heap.Realloc(origPtr, origSize, align, newSize)
}

// TestADirectoryReadIsLoweredWithNoEngineLockHeld is ADR 0134's lock discipline, asserted at its place: inside each
// call to the reader's realloc, which is where guest code runs, no engine lock is held. `dirLowerHeap` wraps the read's
// heap, and its Realloc asks asyncHandles.mu, the registry's lock and the resource table's lock with TryLock before the
// guest's realloc runs. fs-readdir's names-realloc row lowers four names, so the probe must run four times, and the
// row must still read wasmtime's value.
//
// **Inside the first realloc, it also tries the three built-ins another agent could call on the end being served**,
// the one place they can run while it is served without a second agent: a cancel traps by name (ADR 0134), a drop traps
// mid-copy, and a second read traps not IDLE. Each is called directly, below the may-leave guard a guest's call would
// meet first.
//
// NEUTER, run: serveDirRead holding p.h.mu across its reads and lowering, and all four probes find asyncHandles.mu
// held, in both gate states; the first finds no end it can look for, so it calls none of the three built-ins.
func TestADirectoryReadIsLoweredWithNoEngineLockHeld(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, gate)
			in := instantiateReaddir(t, fsReaddirTree(t), true)
			defer in.Close()
			locks := map[string]*sync.Mutex{
				"asyncHandles.mu": &in.w.async.mu, "hostFS.mu": &in.w.hostRes.fs.mu, "hostResources.mu": &in.w.hostRes.mu,
			}
			var calls int
			var held []string
			var served []error
			probe := func() {
				calls++
				for name, mu := range locks {
					if mu.TryLock() {
						mu.Unlock()
					} else {
						held = append(held, name)
					}
				}
				if calls != 1 {
					return
				}
				ri := -1
				if in.w.async.mu.TryLock() {
					for i, ent := range in.w.async.entries {
						if e, ok := ent.(*readableStreamEnd); ok && e.conn != nil && e.conn.producer != nil && e.conn.producer.serving {
							ri = i
						}
					}
					in.w.async.mu.Unlock()
				}
				if ri < 0 {
					served = append(served, errors.New("no end was being served"))
					return
				}
				idx := []interp.Value{interp.I32(int32(ri))}
				_, cerr := streamCancelRead(in.w.async, dirEntryElem)(nil, idx)
				_, derr := streamDropReadable(in.w.async, dirEntryElem)(nil, idx)
				_, rerr := streamRead(in.w.async, dirEntryElem)(nil, append(idx, interp.I32(0), interp.I32(0)))
				served = append(served, cerr, derr, rerr)
			}
			dirLowerHeap = func(h canon.Heap) canon.Heap { return lockProbeHeap{Heap: h, probe: probe} }
			t.Cleanup(func() { dirLowerHeap = nil })
			const row = 7 // names-realloc
			v := callReaddirRow(t, in, row)
			if want := lineAfter(t, "testdata/fs-readdir.reading", "names-realloc: "); strconv.FormatUint(uint64(v), 10) != want {
				t.Errorf("run(%d, names-realloc) = %d, want %s", row, v, want)
			}
			if calls != 4 {
				t.Fatalf("the probe ran %d times, want 4, one per name: the witness did not stand inside the realloc", calls)
			}
			if len(held) > 0 {
				t.Errorf("inside the reader's realloc these engine locks were held: %v", held)
			}
			want := []string{"being served by the host", "mid-copy", "not IDLE"}
			if len(served) != len(want) {
				t.Fatalf("the built-ins on the served end gave %v, want three traps", served)
			}
			for i, err := range served {
				var trap *interp.Trap
				if !errors.As(err, &trap) || !strings.Contains(err.Error(), want[i]) {
					t.Errorf("built-in %d on the served end = %v, want a trap naming %q", i, err, want[i])
				}
			}
		})
	}
}

// TestAStringElementIsNeverCopiedUnderTheLock is ADR 0134's structural guard: a copy under asyncHandles.mu, the one a
// delivery or a second arriver runs, refuses a host stream whose element is neither a byte nor an own, since its
// strings would be lowered through the reader's realloc under the lock and possibly off the reader's goroutine. A
// directory's reads never reach it; this asks it directly. The two ends a directory producer mints carry dirEntryElem.
func TestAStringElementIsNeverCopiedUnderTheLock(t *testing.T) {
	h := newAsyncHandles()
	p := &streamProducer{h: h, ok: fsOK(nil, nil), dir: &fsDirSource{}}
	h.mu.Lock()
	p.mintLocked()
	r := p.reader
	r.readN, r.hasRead, r.state = 8, true, copyStateCopying
	progress, err := driveStreamCopyLocked(r, p.writer)
	h.mu.Unlock()
	if p.writer.elem != dirEntryElem || r.elem != dirEntryElem {
		t.Errorf("a directory producer's ends carry %s and %s, want directory-entry", p.writer.elem, r.elem)
	}
	if !errors.Is(err, ErrUnsupportedForm) || !strings.Contains(err.Error(), "only inside its reader's own stream.read") {
		t.Fatalf("the copy = %d, %v; want ErrUnsupportedForm naming the reader's own stream.read", progress, err)
	}
}

// TestADirectoryEntryNameMustBeUTF8 is ADR 0134's name rule: a component string must be valid UTF-8, so a name that is
// not, which Linux admits, is `illegal-byte-sequence` rather than a string. A valid name is lowered with its link's own
// type. Asked of the function, since this machine's filesystem refuses such a name.
func TestADirectoryEntryNameMustBeUTF8(t *testing.T) {
	if _, label := fsDirEntryValue("caf\xe9", 0); label != "illegal-byte-sequence" {
		t.Errorf("an entry named \"caf\\xe9\" gives label %q, want illegal-byte-sequence", label)
	}
	v, label := fsDirEntryValue("é.txt", fs.ModeSymlink)
	if label != "" {
		t.Fatalf("an entry named \"é.txt\" gives label %q, want none", label)
	}
	name, _ := v.Field("name")
	if s, _ := name.Str(); s != "é.txt" {
		t.Errorf("the entry's name is %q, want \"é.txt\"", s)
	}
	typ, _ := v.Field("type")
	if want := fsLabelledValue(fsDescriptorTypeType(), "symbolic-link"); !reflect.DeepEqual(typ, want) {
		t.Errorf("a symlink's entry has type %v, want symbolic-link", typ)
	}
}

// TestReadDirectoryMustBeDeclaredAsTheHostDeclaresIt is the link's producer arm for an element of its own (ADR 0134): a
// guest whose directory-entry lists its fields in the other order is refused at instantiate, since the host lowers each
// entry at its own layout. wasmtime 49.0.3 refuses it too: "type mismatch for field type: expected `variant` found
// `string`". fs-readdir.wasm, which declares the host's, is the control.
func TestReadDirectoryMustBeDeclaredAsTheHostDeclaresIt(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	for _, c := range []struct {
		fixture string
		refused bool
	}{{"fs-readdir", false}, {"read-directory-mistyped", true}} {
		b, err := os.ReadFile("testdata/" + c.fixture + ".wasm")
		if err != nil {
			t.Fatalf("the committed fixture is missing: %v", err)
		}
		in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
		switch {
		case c.refused && err == nil:
			in.Close()
			t.Errorf("%s instantiated; want it refused for its directory-entry", c.fixture)
		case c.refused && (!errors.Is(err, ErrLinkRefused) || !strings.Contains(err.Error(), "tuple<stream<record")):
			t.Errorf("%s: err = %v, want ErrLinkRefused naming the host's tuple<stream<record …>, …>", c.fixture, err)
		case !c.refused && err != nil:
			t.Errorf("%s, the control, was refused: %v", c.fixture, err)
		case !c.refused:
			in.Close()
		}
	}
}

// TestCloseClosesADirectoryListingNobodyFinished is the listing's file in the registry (ADR 0134): a stream whose
// reader neither finished nor dropped it still holds the directory's file, and Close's stop closes it.
//
// NEUTER, run: fsReadDirectory building the listing's fsFile without adopting it, and the registry holds 0 files
// after read-directory, so stop could not reach it.
func TestCloseClosesADirectoryListingNobodyFinished(t *testing.T) {
	h := NewHost(io.Discard, io.Discard, nil)
	res := newHostResources()
	base := fsTestDir(t, res, fsReaddirTree(t), fsFlagRead)
	async := newAsyncHandles()
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatal(err)
	}
	self := res.mint(descriptorResource, base)
	if _, err := h.fsReadDirectory(async, res)(cc, []interp.Value{interp.I32(self), interp.I32(0)}); err != nil {
		t.Fatalf("read-directory: %v", err)
	}
	if n := len(res.fs.files); n != 1 {
		t.Fatalf("after read-directory the registry holds %d file(s), want the listing's 1", n)
	}
	var listing *fsFile
	for ff := range res.fs.files {
		listing = ff
	}
	res.fs.stop()
	if !fileClosed(listing.f) {
		t.Errorf("after stop the listing's file is still open")
	}
}
