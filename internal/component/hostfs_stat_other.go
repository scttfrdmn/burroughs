// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !darwin && !linux

package component

import (
	"os"
	"time"
)

// statExtra is ADR 0133's fallback where Go exposes no stat record this engine reads: no access or status-change
// time, so both are none, and a link count of 1.
func statExtra(os.FileInfo) (atime, ctime time.Time, nlink uint64, ok bool) {
	return time.Time{}, time.Time{}, 0, false
}

// fileIdentity is ADR 0138's fallback where Go exposes no device or inode number: none, so metadata-hash hashes the
// type, size and modification time instead.
func fileIdentity(os.FileInfo) (dev, ino uint64, ok bool) { return 0, 0, false }
