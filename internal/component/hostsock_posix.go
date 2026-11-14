// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !windows

package component

import "syscall"

// platformErrnoLabels adds nothing off Windows: the POSIX errnos are the ones a socket error carries (ADR 0123).
var platformErrnoLabels = map[syscall.Errno]string{}
