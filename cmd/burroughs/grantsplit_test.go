// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"runtime"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestSplitGrantRecognisesADriveLetterOnWindowsOnly is the parse rule's own witness, with the OS
// injected so **every platform's run checks every platform's rule**.
//
// # Why the OS is a parameter
//
// The defect being fixed is Windows-only: `strings.Cut(v, ":")` splits `--dir C:\data:/d` at the
// drive letter's colon. The repair must therefore behave differently on Windows — and a rule only
// its own platform can exercise is a rule reviewed by nobody, since the Windows job is a
// measurement run after the fact rather than a gate. Passing `goos` turns a platform-conditional
// behaviour into a table, so the Linux gate that must stay green is also the gate that checks the
// Windows branch.
//
// # The two cases that ruled out the obvious repair, and why they are here as pins
//
// A last-colon split is what "handle the drive letter" first suggests, and it breaks two things:
//
//   - `/h::/g` becomes host `/h:` + guest `/g` and is **accepted**, where today it is refused by
//     name as wasmtime's grammar. On Linux a directory named `h:` can exist, so that is a grant
//     against the wrong target rather than a late failure.
//   - `C:/x:/d` on Linux is a valid grant *today* — host `C`, guest `/x:/d` — and any off-Windows
//     drive-letter reading silently rewrites it.
//
// Both are in the table below with their expected splits, so a future "simplification" to
// last-colon fails here rather than in a user's grant.
func TestSplitGrantRecognisesADriveLetterOnWindowsOnly(t *testing.T) {
	const (
		win  = "windows"
		linx = "linux"
		mac  = "darwin"
	)
	for _, c := range []struct {
		name                string
		in, goos            string
		host, guest         string
		found               bool
		whyThisCaseIsHereIs string
	}{
		// --- The forms that work today, which must not move on any OS. ---
		{
			name: "absolute host and guest, linux", in: "/h:/g", goos: linx,
			host: "/h", guest: "/g", found: true,
			whyThisCaseIsHereIs: "the documented form",
		},
		{
			name: "absolute host and guest, windows", in: "/h:/g", goos: win,
			host: "/h", guest: "/g", found: true,
			whyThisCaseIsHereIs: "no drive prefix, so Windows takes the same path as everyone else",
		},
		{
			name: "bare host, linux", in: "/h", goos: linx,
			host: "/h", guest: "", found: false,
			whyThisCaseIsHereIs: "the bare form, unchanged",
		},
		{
			name: "relative bare host, linux", in: "sub/dir", goos: linx,
			host: "sub/dir", guest: "", found: false,
			whyThisCaseIsHereIs: "resolved by Set, not by the split",
		},

		// --- The case that forbids an off-Windows drive-letter rule. ---
		{
			name: "C:/x:/d on linux is host C and guest /x:/d", in: "C:/x:/d", goos: linx,
			host: "C", guest: "/x:/d", found: true,
			whyThisCaseIsHereIs: "A VALID GRANT TODAY. Reading `C:` as a drive letter here would " +
				"silently retarget it from host directory `C` to `C:/x`",
		},
		{
			name: "C:/x:/d on darwin is host C and guest /x:/d", in: "C:/x:/d", goos: mac,
			host: "C", guest: "/x:/d", found: true,
			whyThisCaseIsHereIs: "the same, on the other non-Windows platform the gate runs",
		},

		// --- The drive-letter forms, accepted on Windows. ---
		{
			name: "C:\\x:/d on windows", in: `C:\x:/d`, goos: win,
			host: `C:\x`, guest: "/d", found: true,
			whyThisCaseIsHereIs: "backslash drive prefix, the native shape",
		},
		{
			name: "C:/x:/d on windows", in: "C:/x:/d", goos: win,
			host: "C:/x", guest: "/d", found: true,
			whyThisCaseIsHereIs: "forward-slash drive prefix, which Windows accepts and people type",
		},
		{
			name: "lowercase drive letter on windows", in: `d:\x:/g`, goos: win,
			host: `d:\x`, guest: "/g", found: true,
			whyThisCaseIsHereIs: "drive letters are case-insensitive and both cases get typed",
		},
		{
			name: "a deeper host path on windows", in: `C:\a\b\c:/g`, goos: win,
			host: `C:\a\b\c`, guest: "/g", found: true,
			whyThisCaseIsHereIs: "the split is the first colon AFTER the prefix, not the last one",
		},

		// --- The case that forbids a last-colon rule, on every OS. ---
		{
			name: "the two-colon form keeps a colon-leading guest, linux", in: "/h::/g", goos: linx,
			host: "/h", guest: ":/g", found: true,
			whyThisCaseIsHereIs: "Set refuses a colon-leading guest BY NAME as wasmtime's grammar. " +
				"A last-colon split would make this host `/h:` + guest `/g` and ACCEPT it",
		},
		{
			name: "the two-colon form keeps a colon-leading guest, windows", in: "/h::/g", goos: win,
			host: "/h", guest: ":/g", found: true,
			whyThisCaseIsHereIs: "no drive prefix, so the same refusal is reached on Windows",
		},
		{
			name: "the two-colon form after a drive prefix, windows", in: `C:\x::/g`, goos: win,
			host: `C:\x`, guest: ":/g", found: true,
			whyThisCaseIsHereIs: "the drive prefix is consumed and the two-colon mistake is STILL " +
				"visible to Set's colon-leading check, which is the point of splitting at the " +
				"first colon after the prefix rather than the last colon overall",
		},

		// --- Bare drive letters: not a prefix, because they are not a fixed directory. ---
		{
			name: "a bare drive letter is not a drive prefix, windows", in: "C:", goos: win,
			host: "C", guest: "", found: true,
			whyThisCaseIsHereIs: "`C:` means the CURRENT directory on drive C — per-process state, " +
				"not a path — so it is not treated as a prefix here and Set refuses it by name",
		},
		{
			name: "a drive letter with a relative tail is not a prefix, windows", in: "C:x", goos: win,
			host: "C", guest: "x", found: true,
			whyThisCaseIsHereIs: "`C:x` is drive-relative, equally per-process; it falls through to " +
				"the non-absolute guest refusal rather than being read as a drive prefix",
		},
		{
			name: "a drive-absolute host with no guest half, windows", in: `C:\x`, goos: win,
			host: `C:\x`, guest: "", found: false,
			whyThisCaseIsHereIs: "a prefix with no later colon is the bare form; Set then refuses it " +
				"because a Windows path is never a POSIX guest path",
		},

		// --- Shapes that are not drive letters at all. ---
		{
			name: "a two-letter scheme is not a drive letter, windows", in: `ab:\x:/g`, goos: win,
			host: "ab", guest: `\x:/g`, found: true,
			whyThisCaseIsHereIs: "a drive letter is exactly one character; `ab:` is a host named `ab`",
		},
		{
			name: "a digit is not a drive letter, windows", in: `1:\x:/g`, goos: win,
			host: "1", guest: `\x:/g`, found: true,
			whyThisCaseIsHereIs: "the prefix test is a LETTER, a colon, a separator",
		},
		{
			name: "no colon at all, windows", in: `C\x`, goos: win,
			host: `C\x`, guest: "", found: false,
			whyThisCaseIsHereIs: "no separator to find; the bare form",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			host, guest, found := splitGrant(c.in, c.goos)
			if host != c.host || guest != c.guest || found != c.found {
				t.Errorf("splitGrant(%q, %q)\n got  host=%q guest=%q found=%v\n want host=%q guest=%q found=%v\n why this case exists: %s",
					c.in, c.goos, host, guest, found, c.host, c.guest, c.found, c.whyThisCaseIsHereIs)
			}
		})
	}
}

// TestSplitGrantIsTodaysParseWhereThereIsNoDrivePrefix is the containment claim, as a property.
//
// The repair's whole safety argument is "byte-for-byte today's parse except for a Windows drive
// prefix". That is a claim about *every* input rather than about the table above's twenty, and it
// is the claim a reviewer actually has to believe — so it is checked as one: for any input without
// a Windows drive prefix, on any OS, `splitGrant` must equal `strings.Cut(v, ":")`.
//
// A property rather than a fuzz target because the input space that matters here is tiny and
// enumerable, and because a fuzz target's corpus would be the thing under review instead of the
// rule.
func TestSplitGrantIsTodaysParseWhereThereIsNoDrivePrefix(t *testing.T) {
	inputs := []string{
		"", ":", "::", "/h", "/h:/g", "/h::/g", "h:g", "C", "C:", "C:x", "ab:\\x:/g",
		"1:\\x:/g", "sub/dir", "/a/b:/c/d", "/a:b:c", ".", "./x:/g", "-", "--dir",
		"C:/x:/d", "/h:", ":/g", "a:", "::::", "/weird:name:/g",
	}
	for _, goos := range []string{"linux", "darwin", "windows", "freebsd", "plan9"} {
		for _, in := range inputs {
			if goos == "windows" && drivePrefixLen(in) > 0 {
				continue // the one region where divergence is intended
			}
			wh, wg, wf := splitGrantReference(in)
			gh, gg, gf := splitGrant(in, goos)
			if gh != wh || gg != wg || gf != wf {
				t.Errorf("splitGrant(%q, %q) diverges from today's parse with no drive prefix in play\n"+
					" got  host=%q guest=%q found=%v\n want host=%q guest=%q found=%v",
					in, goos, gh, gg, gf, wh, wg, wf)
			}
		}
	}

	// And the divergence region is non-empty, or the loop above would be asserting equality over
	// everything and the "except" clause would be vacuous — a containment claim that excludes
	// nothing is not a containment claim.
	var diverged int
	for _, in := range inputs {
		if drivePrefixLen(in) > 0 {
			diverged++
		}
	}
	if diverged == 0 {
		t.Fatal("no input in the list has a Windows drive prefix, so the skip above never fires " +
			"and this test proves equality everywhere — which would mean the repair does nothing")
	}
	t.Logf("checked %d input(s) x 5 OS values; %d input(s) are in the intended divergence region",
		len(inputs), diverged)
}

// splitGrantReference is the parse as it stood before the drive-letter rule: `strings.Cut` at the
// first colon. Kept here, in the test, as the thing the containment property compares against —
// *not* in the production file, where a second parser would be a second answer.
func splitGrantReference(v string) (host, guest string, found bool) {
	for i := range len(v) {
		if v[i] == ':' {
			return v[:i], v[i+1:], true
		}
	}
	return v, "", false
}

// TestABareDriveLetterIsRefusedByNameOnEveryOS covers the one input whose two readings are both
// wrong, and which neither the split table nor the platform-specific file would catch.
//
// `--dir C:` is not a near miss. On Windows it means "the current directory on drive C" — a
// per-process notion, not a path — and off Windows it parses as host directory `C` with an empty
// guest, which the bare form would resolve against the working directory and grant under a name
// nobody wrote. Those are *different* wrong answers, which is what makes it ambiguous rather than
// merely unusual, and it is refused on every OS for that reason.
//
// Without this, the input reaches `filepath.Abs` and is granted silently.
func TestABareDriveLetterIsRefusedByNameOnEveryOS(t *testing.T) {
	for _, in := range []string{"C:", "c:", "Z:", "a:"} {
		t.Run(in, func(t *testing.T) {
			var f preopenFlag
			err := f.Set(in)
			if err == nil {
				t.Fatalf("--dir %q was accepted, granting %+v. On %s it resolves against the "+
					"process's working directory and grants a path the operator never named.",
					in, []burroughs.Preopen(f), runtime.GOOS)
			}
			// The wording is the whole effect of the guard, so the wording is asserted.
			for _, want := range []string{"bare drive letter", "per-process"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal of %q does not say %q, so a reader is not told which of "+
						"the two wrong readings they hit.\nGot: %v", in, want, err)
				}
			}
		})
	}

	// And a neighbouring form is NOT caught by the guard, or it would be refusing a class rather
	// than the input. `/h:` is a host with an empty guest, which is the bare form with a trailing
	// separator.
	//
	// **Two assertions, because either alone is weak in a different way.** The claim under test is
	// "the bare-drive-letter guard does not fire on this input", which holds on every OS and is
	// checked on every OS. But absence of that message, alone, would also pass if the input were
	// refused for some reason nobody intended — so where acceptance is the expected outcome, it is
	// still asserted.
	//
	// Acceptance is off-Windows only, and that asymmetry is not a weakening: `filepath.Abs("/h")`
	// yields `D:\h` on Windows, which is not a POSIX guest path, so the input is legitimately
	// refused there by the general absolute-guest check. Measured on the Windows job (#937's run),
	// where the first draft of this arm asserted acceptance unconditionally and failed — the Linux
	// gate could not have told me, which is the whole reason the injected-OS table exists next door.
	t.Run("the guard does not fire on an absolute host with an empty guest", func(t *testing.T) {
		var f preopenFlag
		err := f.Set("/h:")

		// Every OS: whatever happens, it is not the drive-letter guard's doing.
		if err != nil && strings.Contains(err.Error(), "bare drive letter") {
			t.Errorf("--dir \"/h:\" was refused BY THE BARE-DRIVE-LETTER GUARD (%v). That guard's "+
				"subject is a two-character drive reference; this input is a host path with a "+
				"trailing separator, so the guard has widened past what it was written for.", err)
		}

		if runtime.GOOS == "windows" {
			// Positive assertion for this platform: refused, and for the stated reason.
			if err == nil {
				t.Errorf("--dir \"/h:\" was accepted on Windows, where abs(\"/h\") is a " +
					"drive-rooted path a guest cannot open through; granting it would mean a " +
					"mapping was invented")
				return
			}
			if !strings.Contains(err.Error(), "is not absolute") {
				t.Errorf("--dir \"/h:\" was refused on Windows but not by the absolute-guest "+
					"check, so this arm cannot say which rule rejected it: %v", err)
			}
			return
		}
		if err != nil {
			t.Errorf("--dir \"/h:\" was refused (%v), but off Windows it is the bare form with a "+
				"trailing separator and resolves to a real guest path", err)
		}
	})
}

// TestARefusalNamesTheFlagTheOperatorTyped covers the delegation's one weak point.
//
// `scratchFlag` shares `preopenFlag`'s parser so their *refusals* cannot diverge — the right
// decision, and it had a cost nobody had paid: the messages were hard-coded to `--dir`, so
// `--scratch C:` was refused with text naming a flag that was not on the command line, and whose
// suggested remedy (`--dir HOST:/guest`) would have produced a **read-only** grant for an operator
// who asked for a writable one. A wrong remedy is worse than a terse one.
//
// Checked for both flags over every refusing shape, because the fix is a threaded parameter and a
// threaded parameter is exactly what gets dropped at one call site out of six.
func TestARefusalNamesTheFlagTheOperatorTyped(t *testing.T) {
	// Shapes that refuse on every OS. The Windows-only ones are in dirflag_windows_test.go; what
	// matters here is the flag name, which is platform-independent.
	refusing := []string{"C:", ":/g", "/h::/g", "/h:rel", "/h:."}

	for _, in := range refusing {
		t.Run("dir "+in, func(t *testing.T) {
			var f preopenFlag
			err := f.Set(in)
			// A Fatalf, not a skip. All five shapes refuse unconditionally on every OS -- `C:`
			// by the drive-letter guard, `:/g` by the empty host, the rest by the colon-leading
			// and absolute-guest checks -- so there is no platform on which one of them is
			// legitimately accepted. A defensive skip here would have no mechanism behind it and
			// would quietly shrink this table to nothing.
			if err == nil {
				t.Fatalf("%q was ACCEPTED on %s, granting %+v. Every shape in this list refuses "+
					"on every OS by construction, so this is a parser change rather than a "+
					"platform difference.", in, runtime.GOOS, []burroughs.Preopen(f))
			}
			if !strings.Contains(err.Error(), "--dir") {
				t.Errorf("--dir %q refused without naming --dir: %v", in, err)
			}
			if strings.Contains(err.Error(), "--scratch") {
				t.Errorf("--dir %q refused with text naming --scratch: %v", in, err)
			}
		})
		t.Run("scratch "+in, func(t *testing.T) {
			var s scratchFlag
			err := s.Set(in)
			if err == nil {
				t.Fatalf("%q was ACCEPTED by --scratch on %s, granting %+v — and a wrongly "+
					"accepted WRITABLE grant is the worse direction of the two flags.",
					in, runtime.GOOS, s.preopens())
			}
			if !strings.Contains(err.Error(), "--scratch") {
				t.Errorf("--scratch %q refused without naming --scratch, so the operator is told "+
					"about a flag they did not type: %v", in, err)
			}
			if strings.Contains(err.Error(), "--dir") {
				t.Errorf("--scratch %q refused with text naming --dir. If that text is the "+
					"REMEDY, following it produces a read-only grant for someone who asked for a "+
					"writable one: %v", in, err)
			}
		})
	}
}

// TestDrivePrefixIsNotRecognisedOffWindowsAtRuntime pins the wiring, which the table cannot reach.
//
// `splitGrant` takes `goos` as a parameter, so every case above would still pass if `Set` called it
// with a hard-coded `"windows"`. This asserts the live call site agrees with the platform actually
// running — the one claim the injected-OS design gives up, bought back explicitly.
func TestDrivePrefixIsNotRecognisedOffWindowsAtRuntime(t *testing.T) {
	const in = "C:/x:/d"
	host, guest, _ := splitGrant(in, runtime.GOOS)

	var f preopenFlag
	if err := f.Set(in); err != nil {
		t.Fatalf("Set(%q) on %s: %v", in, runtime.GOOS, err)
	}
	if len(f) != 1 {
		t.Fatalf("Set(%q) recorded %d grant(s), want 1", in, len(f))
	}
	if f[0].Host != host || f[0].Guest != guest {
		t.Errorf("Set used a different split from splitGrant(%q, runtime.GOOS)\n"+
			" Set:        host=%q guest=%q\n splitGrant: host=%q guest=%q\n"+
			"The call site is passing something other than the running platform.",
			in, f[0].Host, f[0].Guest, host, guest)
	}
	if runtime.GOOS != "windows" && f[0].Host != "C" {
		t.Errorf("on %s, --dir %q must still grant host %q (today's behaviour), got %q",
			runtime.GOOS, in, "C", f[0].Host)
	}
}
