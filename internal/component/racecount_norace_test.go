// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !race

package component

// raceCountScale is 1 without `-race`. The exclusive complement of `racecount_race_test.go`, so exactly one of the
// two is ever compiled and the constant can never be undefined.
const raceCountScale = 1
