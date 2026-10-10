// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"syscall"
	"testing"
)

// TestErrnoForAPathErrorNamesWindowsDirNotEmpty is the mapping's witness, with the OS injected so
// **the Linux gate exercises the Windows arm**.
//
// # The defect
//
// `path_remove_directory` on a non-empty directory must report `ENOTEMPTY`, and ADR 0091 says so:
// a guest that tries it gets a specific answer rather than `errIO`. The switch has had a
// `syscall.ENOTEMPTY` arm since that slice — and on Windows it never matched. Go defines the
// POSIX-ish `ENOTEMPTY` there (41), but `os.Remove` fails with the Win32 code `ERROR_DIR_NOT_EMPTY`
// (145), so the error fell through to `errIO`. Measured on the Windows job:
//
//	a non-empty rmdir reported "ERR remove /s/d: I/O error", want it to name ENOTEMPTY:
//	an unmapped host errno becomes errIO, which blames the host for a request the guest got wrong
//
// **Why that is worth fixing while Windows is unsupported**: it is a wrong answer *at the engine's
// public surface*, not a test's premise. The guest asked for something it got wrong and was told the
// host had an I/O problem. Every other parked Windows finding is a test asserting a POSIX property
// on a platform that lacks one.
//
// # Why the OS is a parameter
//
// A rule only its own platform can test is a rule nothing that gates a merge ever checks: the
// Windows job is a measurement run after the fact. Passing `goos` makes the Windows arm a row in a
// table that the Linux and macOS gates run. Same argument as `splitGrant`'s injected OS.
func TestErrnoForAPathErrorNamesWindowsDirNotEmpty(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		goos string
		want uint16
		why  string
	}{
		{
			name: "windows ERROR_DIR_NOT_EMPTY maps to ENOTEMPTY",
			err:  &fs.PathError{Op: "remove", Path: `C:\s\d`, Err: errnoWindowsDirNotEmpty},
			goos: "windows", want: errNotempty,
			why: "the arm this slice adds. Without it the value falls through every case below " +
				"and the guest is told the host had an I/O error",
		},
		{
			name: "the same code off Windows is NOT read as ENOTEMPTY",
			err:  &fs.PathError{Op: "remove", Path: "/s/d", Err: errnoWindowsDirNotEmpty},
			goos: "linux", want: errIO,
			why: "errno 145 means nothing in particular on Linux, so mapping it there would be " +
				"inventing a meaning. The new arm is consulted only when the OS is Windows, and " +
				"this row is what holds that containment",
		},
		{
			name: "POSIX ENOTEMPTY still maps on a POSIX host",
			err:  &fs.PathError{Op: "remove", Path: "/s/d", Err: syscall.ENOTEMPTY},
			goos: "linux", want: errNotempty,
			why: "the pre-existing arm, pinned so the Windows addition cannot be mistaken for " +
				"the whole of the behaviour",
		},
		{
			name: "POSIX ENOTEMPTY maps on Windows too",
			err:  &fs.PathError{Op: "remove", Path: `C:\s\d`, Err: syscall.ENOTEMPTY},
			goos: "windows", want: errNotempty,
			why: "Go defines ENOTEMPTY on Windows as 41. It is not what os.Remove returns there, " +
				"but a wrapped error could carry it, and the new check must not have displaced " +
				"the switch",
		},
		{
			name: "an unmapped errno is still errIO",
			err:  &fs.PathError{Op: "remove", Path: "/s/d", Err: syscall.Errno(0x7fff)},
			goos: "linux", want: errIO,
			why: "the fallback. If this ever stopped being errIO the table above would be " +
				"asserting against a default that had moved",
		},
		{
			name: "a not-exist error still maps through the os.Err fallback",
			err:  &fs.PathError{Op: "remove", Path: "/s/d", Err: os.ErrNotExist},
			goos: "windows", want: errNoent,
			why: "the wrapped-case path, checked on Windows because the new arm sits above it " +
				"and must not shadow it",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := errnoForPathErrorOn(c.err, c.goos); got != c.want {
				t.Errorf("errnoForPathErrorOn(%v, %q) = %d, want %d\nwhy this row exists: %s",
					c.err, c.goos, got, c.want, c.why)
			}
		})
	}

	// The wiring the table cannot reach: `errnoForPathError` must pass the platform actually
	// running, not a hard-coded one. Every row above would still pass if it did.
	t.Run("the live entry point uses runtime.GOOS", func(t *testing.T) {
		err := &fs.PathError{Op: "remove", Path: "/s/d", Err: errnoWindowsDirNotEmpty}
		got := errnoForPathError(err)
		want := errnoForPathErrorOn(err, runtime.GOOS)
		if got != want {
			t.Errorf("errnoForPathError = %d but errnoForPathErrorOn(..., %q) = %d — the entry "+
				"point is passing something other than the running platform",
				got, runtime.GOOS, want)
		}
	})

	// And the constant is the Win32 value, not the POSIX one. A row asserting a mapping is
	// worthless if the thing being mapped is the wrong number.
	t.Run("the constant is ERROR_DIR_NOT_EMPTY and not ENOTEMPTY", func(t *testing.T) {
		if errnoWindowsDirNotEmpty != 145 {
			t.Errorf("errnoWindowsDirNotEmpty = %d, want 145 (Win32 ERROR_DIR_NOT_EMPTY)",
				errnoWindowsDirNotEmpty)
		}
		if errnoWindowsDirNotEmpty == syscall.ENOTEMPTY {
			t.Error("errnoWindowsDirNotEmpty equals syscall.ENOTEMPTY, so this platform cannot " +
				"distinguish them and the table above proves nothing")
		}
		// Sanity: the error really does unwrap to the constant, so the rows are not passing
		// because `errors.As` found nothing and the fallbacks happened to agree.
		var se syscall.Errno
		if !errors.As(&fs.PathError{Err: errnoWindowsDirNotEmpty}, &se) || se != errnoWindowsDirNotEmpty {
			t.Error("a PathError wrapping the constant does not unwrap to it, so every row above " +
				"is exercising the os.Err fallbacks rather than the switch")
		}
	})
}
