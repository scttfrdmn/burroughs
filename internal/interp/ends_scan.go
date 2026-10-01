// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

//go:build burroughs_scanlane

package interp

import "github.com/scttfrdmn/burroughs/internal/binary"

// This file **was** the default lane and is now behind `-tags burroughs_scanlane`. It is #136's lane A:
// target resolution by one matching-`end` scan per dynamic block entry.
//
// **It stays in the tree for two reasons, both of which outlive the flip.** It is the **rollback** — a build
// with this tag has exactly the behaviour the engine shipped before — and it is the **comparison lane** every
// future A/B of this mechanism needs. Removing a lane is a separate change with its own reasons, never a side
// effect of a flip.
//
// **Its tag is 18 characters, as every lane's is**, and that is not decoration. `runtime.modinfo.str` records
// `-tags`, so an untagged-versus-tagged A/B shifts 2474 of 5824 `nm -size` lines and the tag swap alone ran
// −3.05% to +0.65% at p<0.01. #136 minted the law — give both lanes a tag of equal length — so now that the
// *default* is untagged, a comparison against this lane needs an inert 18-character tag for the default side:
// `burroughs_lazylane`, which selects nothing and exists only to make the comparison tag-to-tag.
//
// The original note on this file's mechanism, which the flip does not change:
// one
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
//
// A method with an unused receiver, for the same one-`runFrame` reason: lane B reads the table off
// the instance's module (0048's arena), so the signature has to admit a receiver in both lanes.
// endTable is what a lane hands `endOf`. A per-lane alias, so `exec.go` is byte-identical in
// every lane and the A/B compares mechanisms rather than call sites.
type endTable = []int32

func (*Instance) frameEnds(*binary.Func) endTable { return nil }

// endOf pairs the structural header at `pc` with its END. In this lane it is `matchEnd` verbatim:
// the table argument is ignored because no table was built.
func endOf(body []binary.Instr, _ endTable, pc int) (int, error) { return matchEnd(body, pc) }
