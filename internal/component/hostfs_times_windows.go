// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"syscall"
	"time"
)

// fsFileTimes sets an open file's access and modification times through its handle (ADR 0138): SetFileTime, at its
// 100-nanosecond precision, a zero time passed as nil and so left unchanged. SetFileTime needs write access to the
// handle, so a file opened without `write` answers `access`, ADR 0138's named limit. The handle is used inside
// SyscallConn's Control, so a Close meanwhile cannot free it.
func fsFileTimes(f *os.File, atime, mtime time.Time) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if cerr := rc.Control(func(h uintptr) {
		serr = syscall.SetFileTime(syscall.Handle(h), nil, fsFiletime(atime), fsFiletime(mtime))
	}); cerr != nil {
		return cerr
	}
	return serr
}

// fsFiletime is t as a FILETIME, or nil for the zero time.
func fsFiletime(t time.Time) *syscall.Filetime {
	if t.IsZero() {
		return nil
	}
	ft := syscall.NsecToFiletime(t.UnixNano())
	return &ft
}
