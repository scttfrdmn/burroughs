// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build !burroughs_sitememo

package interp

// siteMemo is lane D's per-instance memo, and in every other lane it is **empty** — the same shape
// `lazyEnds` uses, and for the same reason: a field that existed only under a tag would make `Instance`'s
// declaration differ between lanes.
type siteMemo struct{}
