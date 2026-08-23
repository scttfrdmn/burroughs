// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build !burroughs_endtable

package interp

import "github.com/scttfrdmn/burroughs/internal/binary"

// This file is **lane A** of #136's probe: target resolution as this engine ships it, one
// matching-`end` scan per dynamic block entry. Its pair is `ends_table.go`, built with
// `-tags burroughs_endtable`, which resolves once per body and indexes.
//
// The two lanes are build tags rather than a runtime knob for a measurement reason: a package-level
// flag read at every block entry puts a branch in the lane that is supposed to be *unmodified*, and
// the attribution control (`scanbench`'s pad-once shape) would then be pricing the branch. Two
// builds of one source share every other line, which is what makes them comparable at all.

// frameEnds is the per-frame target table, and in this lane there is none — the scan is the
// mechanism, so there is nothing to hoist. Returning nil rather than not existing is what lets one
// `runFrame` serve both lanes; the call is one nil return per *function call*, against a body that
// then interprets the whole function, and it is named here rather than left for the reader to
// discover because it is a real if tiny cost this lane pays and today's `main` does not.
func frameEnds(*binary.Func, []binary.Instr) []int32 { return nil }

// endOf pairs the structural header at `pc` with its END. In this lane it is `matchEnd` verbatim:
// the table argument is ignored because no table was built.
func endOf(body []binary.Instr, _ []int32, pc int) (int, error) { return matchEnd(body, pc) }
