// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build race

package component

// raceSlowdown scales SP-1's adapter-copy witness BOUND under `-race`, as `internal/wasi`'s constant of the
// same name scales the Phase 4 witnesses' timeouts. Under `-race` each chunk's copy also updates shadow
// memory for its whole range, so the arrival the bound must admit is slower. Measured locally: the
// slowest of 20 arrivals was about 0.4ms under `-race` against about 0.07ms without it. The witness's
// neuter is read without `-race`, where the bound is unscaled.
const raceSlowdown = 8
