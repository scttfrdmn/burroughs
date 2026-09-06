// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build unix

package interp

import "syscall"

// mapReservation reserves `n` bytes of address space and reports whether it got them.
//
// **This is the engine's only syscall, and it is the first one the module has ever had.** Stated because
// it is a real widening of what the engine depends on rather than a detail: before
// [decision 0076][0076] no file outside a test imported `syscall` at all. It is still pure Go and still
// cgo-free — `syscall` is stdlib and this is one call — and `golang.org/x/sys` stays out of reach, which
// is what makes `windows` a later slice rather than a line here: `syscall.NewLazyDLL` reaches
// `VirtualAlloc` without a dependency, and that is its own decision with its own measurement.
//
// **`MAP_ANON|MAP_PRIVATE` with `PROT_READ|PROT_WRITE`, and no `Mprotect`.** The mapping is readable and
// writable over its whole extent from the start, and the guest's *current* bound is enforced where it
// already was — in software, by the length of the slice `internal/interp/memory.go:memory.view` hands
// back. Reserving with `PROT_NONE` and committing with `Mprotect` would let the hardware enforce the bound
// too, which is 0076's option (C): deferred, because `syscall.Mprotect` is absent from stdlib on
// `freebsd` and bundling it would narrow this file's port set for a benefit M-1 does not ask for.
//
// **`//go:build unix`, and the constraint is measured rather than assumed.** A compile probe over stdlib
// `syscall` — one library package per symbol, built per `GOOS/GOARCH`, absence read from the build error —
// compiles this expression on every port for which `unix` is true, `ios` and the 32-bit linux ports
// included, and on none of `windows`, `plan9`, `js/wasm`, `wasip1/wasm`. The probe's first form built a
// `package main` and reported `ios` unavailable on *"requires external (cgo) linking, but cgo is not
// enabled"* — a link error that says nothing about the symbol, and the correction is on
// [#672](https://github.com/scttfrdmn/burroughs/issues/672).
//
// **A 32-bit port compiles this and cannot use it**, which is why the caller bounds the request against
// `math.MaxInt` rather than against `GOOS`: the exclusion there is arithmetic — a 4 GiB reservation does
// not fit in a 32-bit address space — and no build tag can express it.
//
// The zero-size guard is not defensiveness: `(memory 0)` is legal wasm (`align.wast:3`), `mmap` of zero
// bytes is `EINVAL`, and a memory with nothing to reserve should take the allocator's path rather than
// count as a refusal.
//
// [0076]: ../../docs/decisions/0076-a-memory-reserves-address-space-through-an-anonymous-mapping-and-the-go-allocator-becomes-the-fallback-rather-than-the-mechanism.md
func mapReservation(n int) ([]byte, bool) {
	if n <= 0 {
		return nil, false
	}
	bs, err := syscall.Mmap(-1, 0, n,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return nil, false
	}
	return bs, true
}

// unmapReservation releases a mapping. `bs` must be the slice `mapReservation` returned, at its full
// length — the kernel is given a base and a length, and a resliced view would unmap the wrong extent.
// `internal/interp/reserve.go:releaseMapping` is the only caller and it holds the full slice for exactly
// that reason.
func unmapReservation(bs []byte) error { return syscall.Munmap(bs) }
