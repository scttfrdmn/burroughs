// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build hwlitmus && !burroughs_plainmem

package interp

// The shipped build: ADR 0054's promise is in force, and TestHWLitmus asserts it on the SC kinds.
const (
	hwArm          = "engine"
	hwPromiseHolds = true
)
