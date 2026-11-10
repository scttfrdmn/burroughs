// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build unix && !((darwin || linux) && !ios && !android && (amd64 || arm64 || ppc64 || ppc64le || riscv64 || s390x || mips64 || mips64le || loong64)) && !burroughs_noreserve

package interp

// hintedPlacement is false on every unix port outside decision 0113's scope:
//   - 32-bit ports, where the raw `SYS_MMAP` is a different call or absent;
//   - openbsd, solaris/illumos, aix, freebsd and netbsd, where a raw `syscall.Syscall6` is restricted, differs,
//     or is unmeasured;
//   - ios and android, which satisfy the `darwin` and `linux` tags but whose address spaces were not measured
//     (iOS's is far smaller than the 16 TiB base).
//
// They keep decision 0076's `syscall.Mmap` placement unchanged. They keep decision 0076's `syscall.Mmap`
// placement unchanged. See internal/interp/reserve_place.go.
const hintedPlacement = false

func mapPlaced(int) ([]byte, bool) { return nil, false }

func unmapPlaced([]byte) error { return nil }
