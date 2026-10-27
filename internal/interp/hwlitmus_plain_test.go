// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build hwlitmus && burroughs_plainmem

package interp

// The neuter build (memop_plain.go): the promise is removed, so TestHWLitmus asserts only the allowed set,
// and the forbidden count on the aligned kind is the reading.
const (
	hwArm          = "engine neuter"
	hwPromiseHolds = false
)
