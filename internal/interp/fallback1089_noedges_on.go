// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build burroughs_noedges

package interp

// noEdges1089 is #1089's test-only fallback for one bisection arm (the chair's ruling of 2026-11-01, item 1): with
// burroughs_noedges, ADR 0112's edgeSplit reports no edges, so every bulk and SIMD extent is all interior and plain, as before ADR 0112 (#1195's own neuter). Nothing ships with it.
const noEdges1089 = true
