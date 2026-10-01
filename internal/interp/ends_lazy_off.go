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

// retained is what the off-lane accessor reads, and it exists so the type is *used*.
//
// Without it `staticcheck` reports `lazyEnds` unused: an embedded zero-width struct that nothing reads is not
// a use as far as the checker is concerned. Routing the accessor through a method on the type is the fix that
// keeps `Instance`'s declaration identical across lanes — suppressing the check, or dropping the embed in this
// lane, would each trade the property the embed exists for.
func (lazyEnds) retained() (tables, slots int) { return 0, 0 }

// counts is the same, for the lookup and build counters.
func (lazyEnds) counts() (calls, builds int64) { return 0, 0 }
