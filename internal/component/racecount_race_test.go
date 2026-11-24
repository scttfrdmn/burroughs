// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build race

package component

// raceCountScale divides parked-runner's count under `-race`, where one count of its runner costs about 21 times
// more: 632k counts per second against 13.6M, measured on darwin/arm64 on 2026-10-26. It scales a COUNT so that the
// runner counts for about the same wall-clock time in either build, the way `internal/wasi`'s `raceSlowdown` scales
// a bound.
//
// **Why a constant and not a calibration run.** The first draft calibrated K by timing a probe run of the same
// fixture. The h4 neuter (the park holding the run slot) then hung the probe, so the case died before its window
// opened, which any unrelated hang would also do. A count fixed per build leaves the case's own assertion to
// catch the neuter.
const raceCountScale = 20

// raceGuardScale stretches a hang guard under `-race`, as `cmd/burroughs`'s does (#1366). A guard turns a hang into a
// reported place and is not a measurement, so it may be as long as a slow build needs. TestC3MultiMRunsOnAtLeastFourAgents
// reads it. On #1390's macOS race run, its CPU phase outran the 2-minute READY guard: the rendezvous had closed 4 of 4
// and ExecutingAgents read 4, so the guest was computing, not parked, with the race job at 81% of its budget.
const raceGuardScale = 4
