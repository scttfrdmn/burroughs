// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"strconv"
	"syscall"
	"time"
)

// utimeOmit is linux's UTIME_OMIT: a timespec's nanoseconds that leave its time unchanged.
const utimeOmit = 1<<30 - 2

// fsFileTimes sets an open file's access and modification times through its fd (ADR 0138), a zero time leaving that
// time unchanged: utimensat on /proc/self/fd/N, whose magic link resolves to the open file, at nanosecond precision.
// It needs /proc mounted, ADR 0138's named limit. The fd is used inside SyscallConn's Control, so a Close meanwhile
// cannot free it and let another file take its number.
func fsFileTimes(f *os.File, atime, mtime time.Time) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	ts := []syscall.Timespec{fsTimespec(atime), fsTimespec(mtime)}
	if cerr := rc.Control(func(fd uintptr) {
		serr = syscall.UtimesNano("/proc/self/fd/"+strconv.FormatUint(uint64(fd), 10), ts)
	}); cerr != nil {
		return cerr
	}
	return serr
}

// fsTimespec is t as a timespec, or UTIME_OMIT for the zero time.
func fsTimespec(t time.Time) syscall.Timespec {
	if t.IsZero() {
		return syscall.Timespec{Nsec: utimeOmit}
	}
	return syscall.NsecToTimespec(t.UnixNano())
}
