// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build !burroughs_lazytabl

package interp

// lazyEnds is the lazy lane's per-instance table store, and in every other lane it is **empty**.
//
// Embedded in [Instance] rather than carried as a tagged field, which is the shape `binary.Module` already
// uses for `moduleEnds`: a zero-width struct costs an untagged build nothing, and the alternative — a field
// that exists only under a tag — would make `Instance`'s own declaration differ between lanes and break the
// "two builds of one source share every other line" property the measurement rests on.
type lazyEnds struct{}
