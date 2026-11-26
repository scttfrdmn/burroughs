// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !darwin && !linux && !windows

package component

import (
	"errors"
	"os"
	"time"
)

// fsFileTimes is ADR 0138's fallback where this engine knows no way to set an open file's times at nanosecond
// precision through its fd: `unsupported`.
func fsFileTimes(*os.File, time.Time, time.Time) error { return errors.ErrUnsupported }
