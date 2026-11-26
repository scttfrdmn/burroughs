// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build race

package interp

// raceGuardScale stretches a hang guard under `-race`, as `internal/component`'s and `cmd/burroughs`'s do (#1366). A
// guard turns a hang into a reported place and is not a measurement, so it may be as long as a slow build needs.
// TestH1ABlockingHostCallDoesNotStarveSiblings reads it (#1402).
const raceGuardScale = 4
