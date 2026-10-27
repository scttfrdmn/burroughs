// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build !burroughs_plainmem

package interp

import (
	"sync/atomic"
	"unsafe"
)

// atomicLoadWord and atomicStoreWord are documented in memop.go, where they stood until #1126 moved
// their bodies here so a neuter build could swap them (memop_plain.go). This is the shipped pair.

func atomicLoadWord(bs []byte) (uint64, bool) {
	switch len(bs) {
	case 4:
		return uint64(guestWord32(atomic.LoadUint32((*uint32)(unsafe.Pointer(&bs[0]))))), true
	case 8:
		return guestWord64(atomic.LoadUint64((*uint64)(unsafe.Pointer(&bs[0])))), true
	default:
		return 0, false
	}
}

func atomicStoreWord(bs []byte, v uint64) bool {
	switch len(bs) {
	case 4:
		atomic.StoreUint32((*uint32)(unsafe.Pointer(&bs[0])), guestWord32(uint32(v)))
		return true
	case 8:
		atomic.StoreUint64((*uint64)(unsafe.Pointer(&bs[0])), guestWord64(v))
		return true
	default:
		return false
	}
}
