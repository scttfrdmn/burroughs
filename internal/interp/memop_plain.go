// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build burroughs_plainmem

package interp

import "unsafe"

// The neuter of memop_sc.go, for #1126's hardware reading and for nothing else: an aligned typed guest
// access as a plain Go typed access, with ADR 0054's sequential consistency removed. Still one typed
// word access, so it cannot tear (ADR 0053), and that keeps the reading's subject to ordering alone.
//
// **A build with this tag is broken on purpose.** Under `-race` it reports every cross-agent guest access,
// and on weak hardware it lets the litmus shapes' SC-forbidden outcomes through. The second is the
// point: the hardware reading's engine arm is only evidence if this arm, on the same machine, dies.

func atomicLoadWord(bs []byte) (uint64, bool) {
	switch len(bs) {
	case 4:
		return uint64(guestWord32(*(*uint32)(unsafe.Pointer(&bs[0])))), true
	case 8:
		return guestWord64(*(*uint64)(unsafe.Pointer(&bs[0]))), true
	default:
		return 0, false
	}
}

func atomicStoreWord(bs []byte, v uint64) bool {
	switch len(bs) {
	case 4:
		*(*uint32)(unsafe.Pointer(&bs[0])) = guestWord32(uint32(v))
		return true
	case 8:
		*(*uint64)(unsafe.Pointer(&bs[0])) = guestWord64(v)
		return true
	default:
		return false
	}
}
