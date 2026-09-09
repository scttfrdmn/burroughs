// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCatGuestReadsAGrantedFile is [decision 0083][0083]'s capability line: a Go wasip1 guest reads a
// file the embedder granted it, through `path_open`/`fd_read`/`fd_close` and the filestat calls.
//
// [0083]: ../../docs/decisions/0083-a-read-only-wasi-preview1-filesystem-a-per-run-fd-table-capability-preopens-and-path-open-scoped-to-reading.md
func TestCatGuestReadsAGrantedFile(t *testing.T) {
	guest := buildGuest(t, "cat")
	dir := t.TempDir()
	const body = "granted file body"
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	code, err := Run(Config{
		Wasm:     guest,
		Args:     []string{"cat", "/sandbox/in.txt"},
		Preopens: []Preopen{{Host: dir, Guest: "/sandbox"}},
		Stdout:   &out,
		Stderr:   &errBuf,
	})
	if err != nil {
		t.Fatalf("run: %v\nstderr: %q", err, errBuf.String())
	}
	if code != 0 {
		t.Fatalf("guest exited %d, want 0 (it should read a granted file)\nstderr: %q", code, errBuf.String())
	}
	if got := out.String(); !strings.Contains(got, body) || !strings.Contains(got, "17 bytes") {
		t.Errorf("stdout = %q, want the granted file's size and body", got)
	}
}

// TestCatGuestCannotReadWithoutAGrant is the capability model's first control: with no preopen, the
// guest can open nothing, so the read fails and its bytes never reach stdout. The must-fail direction
// is witnessed by the granted test above — same guest, same file, a grant is the only difference — so
// this row failing to refuse would show up there as a read that needs no grant.
func TestCatGuestCannotReadWithoutAGrant(t *testing.T) {
	guest := buildGuest(t, "cat")
	dir := t.TempDir()
	const body = "ungranted file body"
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	code, err := Run(Config{
		Wasm:   guest,
		Args:   []string{"cat", "/sandbox/in.txt"}, // the same path — but no Preopen grants it
		Stdout: &out,
		Stderr: &errBuf,
	})
	if err != nil {
		t.Fatalf("run: %v\nstderr: %q", err, errBuf.String())
	}
	if code == 0 {
		t.Errorf("guest exited 0 with no directory granted; it read a file it was never given")
	}
	if strings.Contains(out.String(), body) {
		t.Errorf("stdout = %q leaked the file's contents with no grant", out.String())
	}
}

// TestPathEscapeIsRefusedOnTheResolvedPath witnesses the capability boundary the way decision 0083
// requires (requirement 6): with the escape *actually succeeding* when the check is not applied, not
// merely erroring differently.
//
// The check is a named step — `contained` — separate from `resolvePath`, so this test shows the escape
// working through the check-free `resolvePath` (it resolves to the outside secret, whose bytes are
// readable) and then shows `resolveUnder` refusing it. Removing `contained` from `resolveUnder` would
// hand back the resolved outside path, which `path_open` would open and `fd_read` would read — the
// escape this test proves is otherwise reachable. Requirement 5 is exercised too: the escape is via a
// symlink, which a textual `..` filter would miss and the resolved-path check catches.
func TestPathEscapeIsRefusedOnTheResolvedPath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const secret = "SECRET-OUTSIDE-THE-PREOPEN"
	if err = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := &preopenDir{guestName: "/sandbox", hostRoot: root}

	// A textual `..` escape, and a symlink escape (a `..` filter would pass this one).
	rel, err := filepath.Rel(root, filepath.Join(outside, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	for _, esc := range []struct {
		what string
		path string
	}{
		{"dotdot", rel},
		{"symlink", "link/secret.txt"},
	} {
		t.Run(esc.what, func(t *testing.T) {
			// (1) The escape succeeds when the check is not applied: resolvePath (the check-free step)
			//     reaches the outside secret, and its bytes are readable. Without this, the refusal
			//     below would be witnessed against a phantom (grave: a control isn't born until it is
			//     watched die, with the thing it prevents shown to be real).
			resolved, rerr := resolvePath(root, esc.path)
			if rerr != nil {
				t.Fatalf("resolvePath(%q): %v", esc.path, rerr)
			}
			if got, _ := os.ReadFile(resolved); string(got) != secret {
				t.Fatalf("the escape target is not the readable secret (%q); the witness would be vacuous", resolved)
			}
			if contained(root, resolved) {
				t.Fatalf("contained() accepted a path outside the root — the boundary is absent or textual")
			}
			// (2) resolveUnder refuses the escape as a missing capability, not a missing file.
			if _, e := (&host{}).resolveUnder(dir, esc.path); e != errNotcapable {
				t.Errorf("resolveUnder(%q) = errno %d, want errNotcapable (%d)", esc.path, e, errNotcapable)
			}
		})
	}

	// And the floor: a path *inside* the preopen resolves and is accepted, so the boundary is not a
	// lookup that refuses everything.
	if err := os.WriteFile(filepath.Join(root, "in.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, e := (&host{}).resolveUnder(dir, "in.txt"); e != errSuccess {
		t.Errorf("resolveUnder(in.txt) = errno %d, want success", e)
	} else if !contained(root, p) {
		t.Errorf("an inside file resolved outside the root: %q", p)
	}
}
