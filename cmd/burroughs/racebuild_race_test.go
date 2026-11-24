// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build race

package main

// raceGuardScale stretches a hang guard under `-race`, in the way `internal/wasi`'s `raceSlowdown` scales a bound: a
// guard converts a hang into a reported place, and it is not a measurement, so it may be as long as a slow build
// needs. C3's READY guard reads it (#1345). Under `-race`, C3's single-M CPU phase took about 97 s locally and
// outran the 120 s guard on CI's macOS and linux runners, so 4x leaves room for a runner twice as slow as the
// worst one observed.
//
// It replaced a skip of that row. CI sets BURROUGHS_NO_SKIP=1 workflow-wide, which revokes every licensed skip, so
// the skip never applied where it was needed, as the policy intends: a CI run asks every row.
const raceGuardScale = 4
