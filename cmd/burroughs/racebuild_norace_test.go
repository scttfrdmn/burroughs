// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !race

package main

// raceGuardScale is 1 without `-race`. It is the exclusive complement of `racebuild_race_test.go`, so exactly one of
// the two is ever compiled and the constant can never be undefined.
const raceGuardScale = 1
