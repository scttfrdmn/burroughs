// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !unix

package interp

import "errors"

// mapReservation reports that this port has no reservation primitive, which sends every memory down
// [decision 0076][0076]'s fallback — the Go allocator's path, with `sharedReservePages`' cap and a
// reachable `internal/interp/memory.go:memory.publish`.
//
// The arm is the refused-mapping one and not the decision's rule 3: every memory here *asks* and is told
// no, so `internal/interp/reserve.go:reservationUnavailable` is the counter that moves, which is what makes
// the port's non-conformance a figure rather than an inference.
//
// **The ports this file is compiled for are `windows`, `plan9`, `js/wasm` and `wasip1/wasm`**, measured by
// the compile probe recorded on `internal/interp/reserve_unix.go:mapReservation`. Two of them are
// 32-bit, so they could not host the reservation even with the syscall; `windows` has the primitive
// behind `syscall.NewLazyDLL` and is a later slice; `plan9` has nothing.
//
// **So contract §8 M-1 is not met on these ports, and that is a stated non-conformance rather than a
// silent one.** Which sentence §9 and a release note may then contain about M-1 is
// [#671](https://github.com/scttfrdmn/burroughs/issues/671) — contract § text, and not this file's to
// decide. What this file owes the question is the counter: every memory here increments
// `internal/interp/reserve.go:reservationUnavailable`, so the degradation is a figure an instrument can
// read rather than a property somebody has to infer.
//
// **Nothing in this tree compiles this file except a cross-compile that exists for it.** `make check` runs
// on the dev box and CI runs on linux, both `unix`, so the `build` target cross-compiles one non-`unix`
// port — the same argument it already makes for building the `burroughs_endtable` arm, which is that an
// arm the gate never builds rots without a signal.
//
// [0076]: ../../docs/decisions/0076-a-memory-reserves-address-space-through-an-anonymous-mapping-and-the-go-allocator-becomes-the-fallback-rather-than-the-mechanism.md
func mapReservation(int) ([]byte, bool) { return nil, false }

// unmapReservation cannot be reached on these ports, because nothing here maps anything — and it is a
// real error return rather than a `panic` for that reason. A panic would be a claim that the impossible
// case is worth crashing a host over; an error is a claim that the caller counts it, which
// `internal/interp/reserve.go:releaseMapping` does.
func unmapReservation([]byte) error {
	return errors.New("no address-space reservation on this port, so nothing was mapped to release")
}
