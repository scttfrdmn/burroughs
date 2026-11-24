// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"syscall"
	"time"
)

// statExtra reads what `descriptor-stat` needs beyond os.FileInfo from linux's stat record (ADR 0133): the access
// and status-change times and the link count. ok is false when the record is not linux's.
func statExtra(fi os.FileInfo) (atime, ctime time.Time, nlink uint64, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return time.Time{}, time.Time{}, 0, false
	}
	return time.Unix(st.Atim.Unix()), time.Unix(st.Ctim.Unix()), widenCount(st.Nlink), true
}

// widenCount widens a link count of the architecture's width, which is 32 bits on some linux ports and 64 on others.
func widenCount[T ~uint32 | ~uint64](n T) uint64 { return uint64(n) }
