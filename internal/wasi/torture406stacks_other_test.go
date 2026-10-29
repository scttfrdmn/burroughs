// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !unix

package wasi

import "os"

// t406CanAskForStacks is false off unix. Windows has no signal that makes a Go process dump its stacks, so a hung run
// is killed bare there, as it was before #1181.
const t406CanAskForStacks = false

func t406AskForStacks(*os.Process) bool { return false }
