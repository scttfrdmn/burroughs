// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build unix

package wasi

import (
	"os"
	"syscall"
)

// t406CanAskForStacks says that a hung torture run is asked for its goroutine dump here, so its record must carry one.
const t406CanAskForStacks = true

// t406AskForStacks sends SIGQUIT, which makes a Go process write every goroutine's stack to stderr and exit (#1181).
func t406AskForStacks(p *os.Process) bool {
	return p.Signal(syscall.SIGQUIT) == nil
}
