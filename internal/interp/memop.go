package interp

import (
	"sync/atomic"
	"unsafe"
)

// memop describes one load or store: how many bytes it touches, whether a narrow load
// sign-extends, and which value type the slot holds.
//
// **A table rather than 23 switch arms, because the facts are exactly what a hand-written
// switch gets wrong silently.** `i64_load16_s` is two bytes, sign-extended, into an i64 slot;
// getting any one of those three wrong produces a plausible value for a legal module, which is
// an accept-direction defect the suite scores green by construction (§9 G-3). The rows are
// machine-checked against the generated opcode table's mnemonics by
// TestMemopTableAgreesWithMnemonics, which parses `i64_load16_s` into (8-byte type, 2-byte
// access, signed) and compares — so the table is *derived* from an authority that already has a
// conformance record rather than transcribed from the spec by hand.
type memop struct {
	// width is the number of bytes the access touches in memory: 1, 2, 4, or 8.
	width uint64

	// signed is whether a narrow *load* sign-extends. Meaningless for stores, which
	// truncate, and for full-width accesses.
	signed bool

	// is64 is whether the stack slot is 64 bits (i64/f64) rather than 32 (i32/f32).
	is64 bool

	// isFloat is whether the slot is a float. It changes nothing about the bytes — the
	// engine moves float bits verbatim, never through a Go float, so a signalling NaN's
	// payload survives — and is retained because the *slot* type is what pushI64 versus
	// pushF64 select, and because the table's cross-check needs to know.
	isFloat bool
}

// memops is the load/store family, 0x28-0x3e.
//
// Loads first (0x28-0x35), then stores (0x36-0x3e), in the reference's own order
// (`decode.ml:429-452`). Absent keys are not part of the family and fall to the switch's
// default, which reports the engine gap rather than a wrong answer.
var memops = map[uint32]memop{
	// loads
	0x28: {width: 4},                            // i32.load
	0x29: {width: 8, is64: true},                // i64.load
	0x2a: {width: 4, isFloat: true},             // f32.load
	0x2b: {width: 8, is64: true, isFloat: true}, // f64.load
	0x2c: {width: 1, signed: true},              // i32.load8_s
	0x2d: {width: 1},                            // i32.load8_u
	0x2e: {width: 2, signed: true},              // i32.load16_s
	0x2f: {width: 2},                            // i32.load16_u
	0x30: {width: 1, signed: true, is64: true},  // i64.load8_s
	0x31: {width: 1, is64: true},                // i64.load8_u
	0x32: {width: 2, signed: true, is64: true},  // i64.load16_s
	0x33: {width: 2, is64: true},                // i64.load16_u
	0x34: {width: 4, signed: true, is64: true},  // i64.load32_s
	0x35: {width: 4, is64: true},                // i64.load32_u

	// stores
	0x36: {width: 4},                            // i32.store
	0x37: {width: 8, is64: true},                // i64.store
	0x38: {width: 4, isFloat: true},             // f32.store
	0x39: {width: 8, is64: true, isFloat: true}, // f64.store
	0x3a: {width: 1},                            // i32.store8
	0x3b: {width: 2},                            // i32.store16
	0x3c: {width: 1, is64: true},                // i64.store8
	0x3d: {width: 2, is64: true},                // i64.store16
	0x3e: {width: 4, is64: true},                // i64.store32
}

// isStore reports whether an opcode in the family stores rather than loads.
//
// Derived from the range boundary the reference itself uses rather than listed, so an opcode
// added to `memops` cannot be classified by omission.
func isStore(op uint32) bool { return op >= 0x36 && op <= 0x3e }

// wordAligned reports whether bs starts at a host address that a single width-byte access can
// use, and is the whole of the tear-freedom predicate (ADR 0053).
//
// **The test is on the host address, not on the guest's effective address**, and the two are the
// same question here. `bs` is `m.bytes[ea : ea+width]`, so `&bs[0]` is the host address of `ea`;
// `checkBaseAlignment` refuses to construct a memory whose backing array is not 8-byte aligned, so
// for every width up to 8 the host address is aligned exactly when `ea mod width` is zero — which
// is the proposal's own condition, `u32 mod N/8 = 0`. The implication runs in the direction
// conformance needs: aligned in guest space implies aligned in host space implies the single-access
// path, so the accesses the proposal marks `NOTEARS` are the accesses that get it.
//
// Sound where that premise does *not* hold, which matters because `gcobj.go` passes Go-allocated
// field bytes rather than linear memory: a false answer only declines the fast path, and the byte
// loop is correct everywhere.
//
// Widths are always powers of two in this family (1, 2, 4, 8), so `&(width-1)` is the modulus.
func wordAligned(bs []byte, width uint64) bool {
	if uint64(len(bs)) != width {
		return false
	}
	return uintptr(unsafe.Pointer(&bs[0]))&uintptr(width-1) == 0
}

// **`loadWord` and `storeWord` stood here and are deleted by ADR 0054, along with `guestWord16`, whose
// only callers were their width-2 arms.** They were 0053's aligned access: one *typed* host-word
// operation, which cannot decompose. 0054 needs the same access to be *atomic*, which is a strictly
// stronger property at the same address, so their two call sites became `atomicLoadWord`/
// `atomicStoreWord` below and nothing was left to call them.
//
// **Recorded because the supersession is easy to under-describe, and this comment is the correction.**
// 0054's own text first said the rest of 0053 "stands and is the base this builds on"; what stands is
// `wordAligned`, the *predicate*, which 0054 leans on entirely. The access helpers it guarded do not —
// the `deadcode` gate said so, naming all three, which is how a supersession described as an amendment
// got caught being a deletion.
//
// Deleted rather than left for a later sweep, on the precedent `writeNum`'s comment records for
// `storeBytes`: the widths that still need a read-modify-write reach the containing word through
// `atomicCell` (ADR 0051), so there is no width left for which a plain typed access is the answer.

// atomicLoadWord and atomicStoreWord are `loadWord`/`storeWord` with the access made sequentially
// consistent, and they report `false` at the widths where a single atomic instruction does not exist
// (ADR 0054).
//
// # Why every access, and not only the ones that could tear
//
// 0053 made an aligned access one *typed* word access, which stopped it decomposing. That is
// tear-freedom and it is a guest-visible property. **0054 is a different requirement wearing the same
// shape**: a plain host read racing a host atomic write is a data race under Go's memory model and a
// report under `-race`, and that is true even at width 1, where the byte load is indivisible and could
// not tear if it wanted to. So the trigger here is not "could this tear" but "can two threads reach
// it" — and since a scoped gate is *unavailable* rather than unwritten (`Spawn` is ambient; see 0054),
// the answer is every aligned access.
//
// # Widths 1 and 2 report false, and the caller routes them through `atomicCell`
//
// `sync/atomic` has no narrow operations, so a 1- or 2-byte field has to be reached through a CAS loop
// on its containing 32-bit word — which is exactly what `atomicCell` (ADR 0051) already does, including
// the full-word comparison that keeps a neighbour's concurrent write from being lost. Duplicating that
// here would be a second implementation of the delicate half. Returning `(_, false)` and letting the
// caller fall through costs those two widths a re-resolution of the effective address; the measurement
// that chose this design says the re-resolution is worth ≈5–8pp, so it is paid only where it buys
// something rather than on all four widths.
//
// # The pointer is safe for exactly the reason the deleted `loadWord`'s was
//
// `wordAligned` has already checked the **host** address (`internal/interp/memop.go:wordAligned`), which
// is `sync/atomic`'s own
// requirement at both widths, and the caller has already bounds-checked the extent. Nothing is cached:
// the pointer is derived from the slice handed in per access, so an unshared memory's `grow` cannot
// leave a stale one behind — the property ADR 0051 states for `atomicCell` and the reason #568's
// tripwire is a separate guard rather than a duplicate of this one.
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

// extendSlot turns raw access bytes into a slot value: sign-extension where the mnemonic asks for it,
// verbatim otherwise.
//
// Its own function since #557 because **both** load paths need it and neither may host it — see
// `loadValue` for why the paths are two, and `memAccess` for where the choice between them is made.
//
// **No float branch, and its absence was verified rather than assumed.** `pushF32` stores
// `uint64(math.Float32bits(v))` and `pushF64` stores `math.Float64bits(v)`, so a float slot *is* the
// little-endian bits zero-extended — exactly what `raw` already holds. A branch reinterpreting through
// a Go float would round-trip a signalling NaN's payload through a quiet one, which the suite asserts
// exact and which no arithmetic vector would reveal. `isFloat` therefore exists for the mnemonic
// cross-check, not for this function.
func extendSlot(raw uint64, m memop) uint64 {
	if !m.signed {
		return raw
	}
	// Sign-extend from the access width, then narrow to the slot width. The two steps differ for
	// i32.load8_s: extending to 64 bits and truncating to 32 is what makes `i32.load8_s` of 0xFF read
	// 0xFFFFFFFF rather than 0xFFFFFFFFFFFFFFFF, and the i32 slot is defined as the low 32 bits with
	// the high bits *zero* (exec.go's i32.const grave).
	shift := 64 - m.width*8
	v := uint64(int64(raw<<shift) >> shift)
	if !m.is64 {
		return uint64(uint32(v))
	}
	return v
}

// loadValue reads width bytes little-endian and returns them as a slot, one byte at a time.
//
// **Little-endian is the format's, not the host's**, and the loop below spells it out byte by byte so
// that the engine's answer does not depend on the machine it runs on. It warned that *"a big-endian
// host reading through unsafe would produce byte-swapped values for every vector, which dual-platform
// CI would catch only if one of its arches were big-endian — and neither is"*, and since #557 that
// warning is load-bearing for the sibling path rather than hypothetical: an **aligned** linear-memory
// access is a word read through `unsafe`, where the property is re-established by `guestWord32`/`64`
// — and, at widths 1 and 2 since ADR 0054, by `atomicCell`'s normalization of the containing 32-bit
// word (`guestWord16` is deleted; see the note where it stood in atomic.go). The two are checked against each other with this loop as the
// authority — the same arrangement ADR 0051 made for the atomics, for the same reason: the loop is the
// code the whole spec suite has validated.
//
// # This is the *general* path, and the tear-free one is at the call site
//
// The threads proposal's `tearing` (`runtime.rst:742-746` at ADR 0049's pin) marks an aligned integer
// access no wider than 32 bits `NOTEARS`, and four separate byte reads are exactly the decomposition
// that word forbids. So an aligned linear-memory load must not come through here — and the branch that
// sends it elsewhere is in `memAccess` rather than at the top of this function, **because putting it
// here cost 6.24% on every unaligned load and bought nothing.** `wordAligned` and `loadWord` — the
// latter since deleted by ADR 0054, so the figure is historical — inlined to 89 against an 80-point
// budget, so a `loadValue` containing both stopped being inlinable at all
// (cost 65 → 165, measured with `-gcflags=-m=2`), and every load in every module paid a call. The
// figure, the mechanism and the pre-registration it falsified are in ADR 0053.
//
// **Its remaining callers are the ones with no tearing obligation**: `memAccess`'s unaligned branch,
// where tearing is permitted, and `gcobj.go`, whose bytes are Go-allocated struct fields rather than
// linear memory — `struct.get` is a different rule from `t.load`, so the proposal's condition does not
// reach them. That makes the narrower placement more faithful than the wider one, not a concession.
func loadValue(bs []byte, m memop) uint64 {
	var raw uint64
	for i := len(bs) - 1; i >= 0; i-- {
		raw = raw<<8 | uint64(bs[i])
	}
	return extendSlot(raw, m)
}
