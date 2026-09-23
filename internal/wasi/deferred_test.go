// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grantDir builds a directory the guest is given, with a payload whose bytes make an offset read
// distinguishable from a read from zero: "abcdef" then "GHIJK" at offset 6.
func grantDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.txt"), []byte("abcdefGHIJK"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDeferredReadsAreImplemented witnesses the two functions #816 obliges, through the same Go API the
// real consumer used (`ReadAt`, `os.ReadDir`) rather than a hand-rolled syscall.
//
// Both are READS, which is why ADR 0083's read-only decision survives this slice: the guest reads a
// descriptor the capability model already granted, and lists a directory it was already given.
func TestDeferredReadsAreImplemented(t *testing.T) {
	wasm := buildGuest(t, "dirread")
	dir := grantDir(t)

	var out, errBuf bytes.Buffer
	code, err := Run(Config{
		Wasm:     wasm,
		Args:     []string{"dirread"},
		Stdout:   &out,
		Stderr:   &errBuf,
		Preopens: []Preopen{{Host: dir, Guest: "/d"}},
	})
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, errBuf.String())
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errBuf.String())
	}
	got := out.String()

	// fd_readdir: both entries, with the directory distinguished from the file. The kinds matter — a
	// d_type of `unknown` for everything would still produce two names.
	if want := "READDIR 2 [d:subdir f:payload.txt]"; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want %q: fd_readdir must list the granted directory and type each entry",
			got, want)
	}

	// fd_pread: the bytes AT THE OFFSET, not from zero. "GHIJK" only appears if the offset was honoured.
	if want := `PREAD 5 "GHIJK"`; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want %q: fd_pread must read at the requested offset", got, want)
	}

	// **The assertion that distinguishes pread from seek+read**, and the reason the guest does a plain
	// Read afterwards: pread must NOT move the descriptor's own offset. Implemented as a seek-read-seek
	// this would print "GHIJK" here and pass the check above.
	if want := `AFTER-PREAD-READ 5 "abcde"`; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want %q: fd_pread moved the fd's offset, so it is seek+read wearing "+
			"pread's name", got, want)
	}

	// A pread past the end is a short read, not an engine error: preview 1 spells EOF as nread < asked
	// with ESUCCESS, and a guest that got an errno here would fail differently.
	if want := `PREAD-EOF 2 "JK"`; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want %q: a pread crossing the end must be a short read", got, want)
	}
}

// TestDeferredRefusalsFire is the other half, and it is the half a test of the permit path cannot give:
// every function #816 leaves deferred is REACHED by a guest here, and the refusal is witnessed on both
// channels — the host counted it, and the guest observed a failure.
//
// Asserting only the host's count would pass against a refusal the guest silently ignored; asserting only
// the guest's error would pass against a failure that never reached the preview-1 layer at all.
func TestDeferredRefusalsFire(t *testing.T) {
	wasm := buildGuest(t, "refused")
	dir := grantDir(t)

	// The host goes through `newHost`, NOT a composite literal, which is #815's whole point: a hand-built
	// host omits what the constructor sets, and the symptom surfaces in another subsystem. The test needs
	// its own host only so it can read `refusalsForTest` afterwards, which `Run` has no way to expose.
	var out, errBuf bytes.Buffer
	cfg := Config{
		Wasm:     wasm,
		Args:     []string{"refused"},
		Stdin:    strings.NewReader(""),
		Stdout:   &out,
		Stderr:   &errBuf,
		Preopens: []Preopen{{Host: dir, Guest: "/d"}},
	}
	m, derr := decodeGuest(cfg)
	if derr != nil {
		t.Fatal(derr)
	}
	h := newHost(cfg)
	if err := h.initFDs(cfg.Preopens); err != nil {
		t.Fatal(err)
	}

	code, err := runModule(m, h)
	if err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, out.String())
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0: the guest reports failures rather than exiting on them\nstdout: %s",
			code, out.String())
	}

	// Channel 1: the guest observed a failure for every call.
	guest := out.String()
	for _, want := range []string{
		"SEEK err=true", "MKDIR err=true", "REMOVE err=true", "SYMLINK err=true",
		"READLINK err=true", "TRUNCATE err=true", "RMDIR err=true",
	} {
		if !strings.Contains(guest, want) {
			t.Errorf("guest stdout = %q, want %q: a refused function must make the guest's own call fail",
				guest, want)
		}
	}

	// Channel 2: the host counted each refusal, so "never called" and "called and quietly refused" are
	// distinguishable — which is what makes "refused by name" a falsifiable claim rather than a label.
	got := h.refusalsForTest()
	for _, name := range []string{
		"fd_seek", "path_create_directory", "path_unlink_file", "path_symlink",
		"path_readlink", "fd_filestat_set_size", "path_remove_directory",
	} {
		if got[name] == 0 {
			t.Errorf("refusal %q never fired; the guest calls it, so either the call did not reach the "+
				"preview-1 layer or the counter is not wired", name)
		}
	}
	if len(got) == 0 {
		t.Fatal("no refusals were counted at all — the counter is dead, and every assertion above is " +
			"vacuous")
	}
}
