// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"syscall"
	"time"
)

// statExtra reads what `descriptor-stat` needs beyond os.FileInfo from darwin's stat record (ADR 0133): the access
// and status-change times and the link count. ok is false when the record is not darwin's.
func statExtra(fi os.FileInfo) (atime, ctime time.Time, nlink uint64, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return time.Time{}, time.Time{}, 0, false
	}
	return time.Unix(st.Atimespec.Unix()), time.Unix(st.Ctimespec.Unix()), uint64(st.Nlink), true
}
