// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"strconv"
	"syscall"
	"time"
)

// utimeOmit is darwin's UTIME_OMIT: a timespec's nanoseconds that leave its time unchanged.
const utimeOmit = -2

// fsFileTimes sets an open file's access and modification times through its fd (ADR 0138), a zero time leaving that
// time unchanged: utimensat on /dev/fd/N, which darwin resolves to the open file, at nanosecond precision.
// syscall.Futimes is microsecond-precise, and darwin's syscall package has no futimens. The fd is used inside
// SyscallConn's Control, so a Close meanwhile cannot free it and let another file take its number.
func fsFileTimes(f *os.File, atime, mtime time.Time) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	ts := []syscall.Timespec{fsTimespec(atime), fsTimespec(mtime)}
	// NEUTER, run: syscall.Futimes(fd) of the two as timevals instead, and TestFSRestMatchesWasmtime's set-times row
	// reads 0x01580500, both seconds set and neither nanosecond count (microseconds), and set-times-partial reads
	// 0x06000000, no-change's UTIME_OMIT taken as a time.
	if cerr := rc.Control(func(fd uintptr) {
		serr = syscall.UtimesNano("/dev/fd/"+strconv.FormatUint(uint64(fd), 10), ts)
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
