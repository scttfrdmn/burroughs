// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package main

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestADriveLetterGrantIsAcceptedOnWindows is the end-to-end half of the separator repair.
//
// # What this adds over the table test, which already covers these inputs
//
// `TestSplitGrantRecognisesADriveLetterOnWindowsOnly` asserts the *split*, with `goos` injected, so
// it runs everywhere and is where the rule is reviewed. What it cannot see is everything `Set` does
// after the split: the colon-leading check, the absolute-guest check, and the bare-form resolution
// through `filepath.Abs`, whose behaviour on a drive-letter path is a property of Windows rather
// than of this package. A split that is right and a `Set` that refuses the result anyway is exactly
// the failure this file exists to catch, and it was the shape of the original defect — the split
// was wrong and the *symptom* was an absolute-path refusal.
//
// So: the inputs overlap deliberately, and the assertions do not.
func TestADriveLetterGrantIsAcceptedOnWindows(t *testing.T) {
	for _, c := range []struct {
		name, in    string
		host, guest string
	}{
		{
			name: "backslash drive prefix", in: `C:\data:/d`,
			host: `C:\data`, guest: "/d",
		},
		{
			name: "forward-slash drive prefix", in: "C:/data:/d",
			host: "C:/data", guest: "/d",
		},
		{
			name: "lowercase drive letter", in: `c:\data:/d`,
			host: `c:\data`, guest: "/d",
		},
		{
			name: "a deeper host path", in: `C:\a\b\c:/mnt`,
			host: `C:\a\b\c`, guest: "/mnt",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var f preopenFlag
			if err := f.Set(c.in); err != nil {
				t.Fatalf("--dir %q was refused on Windows: %v\nThis is the defect the drive-letter "+
					"rule exists to fix: the split is only half of it, and a refusal here means "+
					"`Set`'s later checks reject a correctly split grant.", c.in, err)
			}
			if len(f) != 1 {
				t.Fatalf("--dir %q recorded %d grant(s), want 1", c.in, len(f))
			}
			if f[0].Host != c.host || f[0].Guest != c.guest {
				t.Errorf("--dir %q granted host=%q guest=%q, want host=%q guest=%q",
					c.in, f[0].Host, f[0].Guest, c.host, c.guest)
			}
		})
	}

	// `--scratch` must agree, and it does so by delegation rather than by a second parser. Asserted
	// because the delegation is the claim: a copied parser's *refusals* can diverge silently, which
	// is worse than a copied permit — the permit's divergence shows up as a broken run, the
	// refusal's as a grant nobody meant.
	t.Run("scratch takes the same form and is writable", func(t *testing.T) {
		var s scratchFlag
		if err := s.Set(`C:\data:/d`); err != nil {
			t.Fatalf("--scratch with a drive letter was refused: %v", err)
		}
		got := s.preopens()
		if len(got) != 1 {
			t.Fatalf("recorded %d grant(s), want 1", len(got))
		}
		want := burroughs.Preopen{Host: `C:\data`, Guest: "/d", Writable: true}
		if got[0] != want {
			t.Errorf("--scratch granted %+v, want %+v", got[0], want)
		}
	})
}

// TestTheWindowsOnlyRefusalsNameTheirCause covers the two shapes that have no correct outcome on
// Windows, and must say so rather than failing as a generic non-absolute guest path.
func TestTheWindowsOnlyRefusalsNameTheirCause(t *testing.T) {
	for _, c := range []struct {
		name, in string
		wants    []string
		why      string
	}{
		{
			name: "a drive-absolute host with no guest path", in: `C:\data`,
			wants: []string{"bare --dir HOST form cannot map", "--dir C:\\data:/guest"},
			why: "the bare form maps a directory under its own RESOLVED name, and a resolved " +
				"Windows path is never a POSIX guest path — so this shape has no correct " +
				"outcome for any input, and the message must name the missing half rather " +
				"than report a near miss",
		},
		{
			name: "a relative bare host", in: `data`,
			wants: []string{"bare --dir HOST form cannot map"},
			why: "the same, reached through filepath.Abs rather than directly: `data` resolves " +
				"to C:\\...\\data, which is equally unopenable by a guest",
		},
		{
			name: "the two-colon form after a drive prefix", in: `C:\data::/d`,
			wants: []string{"ONE colon", "wasmtime"},
			why: "the drive prefix is consumed and the two-colon mistake is still visible, which " +
				"is why the split takes the first colon AFTER the prefix and not the last colon " +
				"overall — a last-colon rule would accept this as host `C:\\data:`",
		},
		{
			name: "a bare drive letter", in: `C:`,
			wants: []string{"bare drive letter", "per-process"},
			why:   "`C:` is the current directory ON drive C, which is per-process state",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var f preopenFlag
			err := f.Set(c.in)
			if err == nil {
				t.Fatalf("--dir %q was accepted on Windows, granting %+v.\nWhy that is wrong: %s",
					c.in, []burroughs.Preopen(f), c.why)
			}
			for _, want := range c.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal of %q does not contain %q.\nGot: %v\nWhy this wording "+
						"matters: %s", c.in, want, err, c.why)
				}
			}
		})
	}
}
