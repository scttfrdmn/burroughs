// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The Windows confinement witness: a scratch grant must not be escapable through a **junction**.
//
// # Why this test exists, and why it is Windows-only
//
// `--scratch` is the one flag that lets a guest change the host's filesystem, and decision 0083's
// capability model says nothing is visible unless named. `os.Root` is the mechanism that enforces it:
// every write the WASI host offers goes through one, which is what makes the grant a boundary rather
// than a path-prefix check.
//
// **GO-2026-6604 was a hole in that mechanism, on Windows only.** `os.Root.Mkdir(All)` could follow a
// *junction* out of the root — fixed in go1.26.9 and go1.27.2. The advisory's three live traces were
// this engine's own write paths:
//
//	internal/wasi/fs.go:pathCreateDirectory → os.Root.Mkdir
//	internal/wasi/fs.go:pathUnlinkFile      → os.Root.Remove
//	internal/wasi/fs.go:pathOpen            → os.Root.OpenFile
//
// Cited by **symbol** rather than by the line numbers `govulncheck` printed, which is ADR 0047's rule
// and the right one here for a specific reason: those lines will move the next time `fs.go` is touched,
// and a rotted citation in a security witness points a reader at whatever code has drifted into its
// place. `TestSymbolCitationsResolveToADeclaration` checks the symbol form; nothing can check a number.
// (The positional census caught the first draft of this comment, which had the numbers.)
//
// So the confinement guarantee was, for a window, false on Windows — and nothing in this tree could
// have said so, because nothing ran there. That is what this file answers.
//
// # A junction is not a symlink, and that is the whole point
//
// The existing `TestAScratchGrantConfinesEveryWriteToItsRoot` covers symlinks, including ones pointing
// at an absolute path outside the grant. A **junction** is a different NTFS object: a directory
// reparse point that the OS resolves more eagerly than a symlink, and it needs no Developer Mode to
// create. A symlink-only test would have passed throughout the advisory's window. Both are exercised
// here, so a future regression in either resolution path is caught, and the junction arm is the one
// with a known past failure.
//
// # Setup failure is a FATAL, not a skip
//
// If the runner cannot create a junction, this test cannot exercise the vector it exists for, and a
// skip would read as "confinement verified on Windows" in a board that counts passes. *A licensed skip
// forces the tool into CI* — here the honest move is the opposite: fail loudly and let the first run
// tell us what the runner can do, because the measurement is the deliverable.
func TestAScratchGrantIsNotEscapableThroughAJunction(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "grant")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{inside, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	// A file outside the grant, so a successful escape has something to find and this test can tell
	// "the call was refused" from "the call succeeded against nothing".
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("out of grant"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// **The junction.** `mklink /J` needs no elevation and no Developer Mode, unlike a symlink — which
	// is exactly why it is the interesting vector: a guest-influenced directory can hold one on a
	// machine where symlink creation is forbidden. `cmd /c` because `mklink` is a shell builtin, not an
	// executable.
	junction := filepath.Join(inside, "jct")
	out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput()
	if err != nil {
		t.Fatalf("could not create the junction this test exists to probe (%v): %s\n"+
			"Not skipped on purpose: a skip here would read as \"confinement verified on Windows\" in a "+
			"board that counts passes, while having exercised nothing. If the runner genuinely cannot "+
			"create a junction, that is the finding to report rather than to tolerate.", err, out)
	}

	// A symlink too, for the arm the existing Unix test covers — kept here so the two resolution paths
	// are compared on the same platform. Developer Mode or elevation is required, so a failure here is
	// reported and the test continues: the junction arm is the one with a known past failure, and losing
	// the symlink arm must not hide it.
	symlink := filepath.Join(inside, "lnk")
	symlinkOK := true
	if serr := os.Symlink(outside, symlink); serr != nil {
		symlinkOK = false
		t.Logf("symlink arm unavailable (%v) — Developer Mode or elevation is needed for os.Symlink on "+
			"Windows. The junction arm below is unaffected and is the one GO-2026-6604 concerned.", serr)
	}

	root, err := os.OpenRoot(inside)
	if err != nil {
		t.Fatalf("OpenRoot(%s): %v", inside, err)
	}
	defer func() {
		if cerr := root.Close(); cerr != nil {
			t.Errorf("closing the root: %v", cerr)
		}
	}()

	closeIf := func(f *os.File) {
		if f != nil {
			_ = f.Close()
		}
	}

	// Each arm names the WASI host function whose write path it stands for, so a failure points at the
	// guest-reachable operation rather than only at the Go call.
	escapes := []struct {
		name string
		op   func() error
	}{
		// path_create_directory → os.Root.Mkdir. THE advisory's arm: `Mkdir` through a junction.
		{"path_create_directory: mkdir jct/pwneddir through a junction", func() error {
			return root.Mkdir("jct/pwneddir", 0o700)
		}},
		{"path_create_directory: mkdirall jct/a/b through a junction", func() error {
			return root.MkdirAll("jct/a/b", 0o700)
		}},
		// path_open → os.Root.OpenFile, both reading and creating.
		{"path_open: create jct/pwned through a junction", func() error {
			f, e := root.Create("jct/pwned")
			closeIf(f)
			return e
		}},
		{"path_open: read jct/secret.txt through a junction", func() error {
			_, e := root.ReadFile("jct/secret.txt")
			return e
		}},
		// path_unlink_file → os.Root.Remove.
		{"path_unlink_file: remove jct/secret.txt through a junction", func() error {
			return root.Remove("jct/secret.txt")
		}},
		// And the textual form, which is the shape a prefix filter catches and a reparse point is not.
		{"path_create_directory: mkdir ../pwneddir", func() error { return root.Mkdir("../pwneddir", 0o700) }},
	}
	if symlinkOK {
		escapes = append(escapes,
			struct {
				name string
				op   func() error
			}{"path_open: create lnk/pwned through a symlink", func() error {
				f, e := root.Create("lnk/pwned")
				closeIf(f)
				return e
			}},
			struct {
				name string
				op   func() error
			}{"path_unlink_file: remove lnk/secret.txt through a symlink", func() error {
				return root.Remove("lnk/secret.txt")
			}},
		)
	}

	refused := 0
	for _, esc := range escapes {
		if err := esc.op(); err == nil {
			t.Errorf("ESCAPE PERMITTED: %s succeeded. A guest with a --scratch grant can reach outside "+
				"the directory the operator named, which is the confinement decision 0083 promises and "+
				"GO-2026-6604 broke on this platform.", esc.name)
			continue
		}
		refused++
	}

	// **Nothing outside the grant may have changed.** The per-arm assertions check that each call
	// *failed*; this checks the thing that actually matters, because a call can fail after doing damage.
	for _, p := range []string{"pwned", "pwneddir", "a"} {
		if _, serr := os.Stat(filepath.Join(outside, p)); serr == nil {
			t.Errorf("%s exists outside the grant: a refused call still wrote through the junction",
				filepath.Join(outside, p))
		}
	}
	if _, serr := os.Stat(filepath.Join(outside, "secret.txt")); serr != nil {
		t.Errorf("secret.txt outside the grant is gone (%v): a refused remove still deleted it", serr)
	}

	// The anti-vacuity line: a count, printed, so a run that probed two shapes instead of eight is
	// visible in the log rather than reading as a clean pass.
	t.Logf("WINDOWS-CONFINEMENT: %d escape shape(s) refused through a junction%s, nothing outside the "+
		"grant touched", refused, map[bool]string{true: " and a symlink", false: " (symlink arm unavailable)"}[symlinkOK])
}
