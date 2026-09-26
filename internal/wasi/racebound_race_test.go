// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build race

package wasi

// raceSlowdown scales the Phase 4 witnesses' TIMEOUTS under `-race`.
//
// **It exists because CI found what a local run could not.** Both clause witnesses passed on darwin/arm64
// and then timed out in CI's `race` job on Linux — clause 2 at its 60s bound, clause 3 at its 300s bound,
// three arms at once. The bounds had been calibrated on un-raced execution with no headroom, which is
// *compare the floor to the bar* on a timeout: a bound that sits just above the observed time on one build
// configuration says nothing about another.
//
// **It scales a BOUND, not a measurement.** Every figure these witnesses assert — sibling deltas, collection
// counts, verdicts, node counts — is unchanged by this constant. What changes is only how long the harness
// waits before calling a run hung, and under `-race` the interpreter is genuinely much slower, so the same
// wall-clock number means something different. A witness whose bound did not scale would report a slow build
// as a defect.
const raceSlowdown = 8
