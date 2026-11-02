// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build burroughs_plainspan

package interp

// plainSpan1089 is #1089's test-only fallback for one bisection arm (the chair's ruling of 2026-11-01, item 1): with
// burroughs_plainspan, ADR 0111's span helpers (atomicLoadSpan, atomicStoreSpan) read and write their bytes plainly, through packLE and unpackLE. Nothing ships with it.
const plainSpan1089 = true
