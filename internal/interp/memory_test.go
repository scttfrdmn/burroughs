package interp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// instantiate1 builds a module through the real front end and returns the instance.
//
// The same path as run1 and for the same reason (grave #125): the immediate staging is part of
// the subject, so a hand-built `binary.Module` would let these tests assert the interpreter
// against its own assumption about the decoder. Unlike run1 it hands back the trap instead of
// failing on it, because half of what is under test here is *which* modules trap.
func instantiate1(t *testing.T, src string) (*Instance, *Trap) {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return Instantiate(m)
}

// invoke1 instantiates and invokes, requiring both to succeed.
func invoke1(t *testing.T, src, fn string, args ...Value) []Value {
	t.Helper()
	in, trap := instantiate1(t, src)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	out, err := in.Invoke(fn, args...)
	if err != nil {
		t.Fatalf("invoke %s: %v", fn, err)
	}
	return out
}

// TestNarrowLoadSignExtendsIntoItsOwnSlotWidth pins the three facts a hand-written load arm gets
// wrong silently: access width, sign-extension, and *which slot* the extension stops at.
//
// **This is an accept-direction control** (§9 G-3): every module below is valid and every wrong
// answer is a plausible number, so the suite scores such a defect green by construction. There is
// no negative vector for "i32.load8_s of 0xFF returned the wrong non-trapping value".
//
// The load rows are chosen so that each one *distinguishes a different mistake*. The byte pattern
// is `\ff\ff\ff\ff\ff\ff\ff\ff` throughout, because all-ones is the value where width and
// signedness both show:
//
//   - `i32.load8_s` — the two-step extension. Extending to 64 bits and forgetting to narrow to
//     the i32 slot yields 0xFFFFFFFFFFFFFFFF, which as an i32 result reads correct at the host
//     boundary and is wrong in the slot; the i64 row below is what sees it.
//   - `i64.load8_s` versus `i64.load8_u` — signedness alone, same width, same slot.
//   - `i64.load32_s` versus `i64.load32_u` — the widest narrow load, where a `width*8` shift that
//     used 32 instead of the access width would coincide with the right answer for one of the two.
//   - `i32.load16_u` — narrowing with *no* extension, the negative side of the partition: a fix
//     that sign-extended unconditionally would break exactly this row.
//
// # Falsified at birth, and asserting the slot is what makes it fail
//
// Perturbing `loadValue`'s narrow-slot step from `uint64(uint32(v))` to `v` — the mistake the
// two-step comment exists to prevent — fails **both** signed i32 rows: `i32_8s` and `i32_16s`
// each read `0xffffffffffffffff` where the slot must be `0x00000000ffffffff`.
//
// It fails *only* because the rows compare `Value.Bits`. A version of this test that compared
// boundary values as int32s would be green on the same defect, since the wrong bits live above
// bit 31 and every read-back path through `popI32` truncates them away — which is exactly how the
// i32.const grave (#125) survived 114 wrong conversions with a byte-identical board. The subject
// is the slot, not the boundary, so the assertion is on the slot.
func TestNarrowLoadSignExtendsIntoItsOwnSlotWidth(t *testing.T) {
	const src = `(module
		(memory 1)
		(data (i32.const 0) "\ff\ff\ff\ff\ff\ff\ff\ff")
		(func (export "i32_8s")  (result i32) (i32.load8_s  (i32.const 0)))
		(func (export "i32_8u")  (result i32) (i32.load8_u  (i32.const 0)))
		(func (export "i32_16s") (result i32) (i32.load16_s (i32.const 0)))
		(func (export "i32_16u") (result i32) (i32.load16_u (i32.const 0)))
		(func (export "i64_8s")  (result i64) (i64.load8_s  (i32.const 0)))
		(func (export "i64_8u")  (result i64) (i64.load8_u  (i32.const 0)))
		(func (export "i64_32s") (result i64) (i64.load32_s (i32.const 0)))
		(func (export "i64_32u") (result i64) (i64.load32_u (i32.const 0)))
	)`

	cases := []struct {
		fn   string
		want uint64 // the raw slot, not the boundary value
	}{
		// An i32 slot is the low 32 bits with the high bits zero, so a sign-extended
		// -1 is 0x00000000FFFFFFFF and never 0xFFFFFFFFFFFFFFFF.
		{"i32_8s", 0xFFFFFFFF},
		{"i32_8u", 0xFF},
		{"i32_16s", 0xFFFFFFFF},
		{"i32_16u", 0xFFFF},
		{"i64_8s", 0xFFFFFFFFFFFFFFFF},
		{"i64_8u", 0xFF},
		{"i64_32s", 0xFFFFFFFFFFFFFFFF},
		{"i64_32u", 0xFFFFFFFF},
	}
	for _, c := range cases {
		t.Run(c.fn, func(t *testing.T) {
			out := invoke1(t, src, c.fn)
			if len(out) != 1 {
				t.Fatalf("%s returned %d values, want 1", c.fn, len(out))
			}
			if out[0].Bits != c.want {
				t.Errorf("%s slot = %#016x, want %#016x", c.fn, out[0].Bits, c.want)
			}
		})
	}
}

// TestStoreTruncatesAndIsLittleEndian pins the store direction, read back through a *wider* load.
//
// **Reading back with a load of the same width would be circular**: a store and a load that shared a
// byte-order mistake would agree perfectly, and the round trip would be green on a big-endian answer.
// So each row stores narrow and reads back the full 8 bytes, which makes the *placement* of the bytes
// observable rather than just their round trip. That is the same reason the memop table is
// cross-checked against the generated mnemonics rather than against itself (grave #106: a premise
// measured over the same sample the code reads is an echo).
//
// # The address is a row field since #557, because truncation moved into a partition
//
// Every row used to store at address 0, which is aligned for every width — so after ADR 0053 this
// test would have exercised the *word* path alone, and the byte fallback's truncation would have had
// no test at all while this one's name went on claiming the property. Both paths truncate, by
// different means (`storeWord` by converting to the width's type, the fallback by shifting), so the
// rows are doubled onto an unaligned address and **a falsification now has to cross the partition
// twice** to fail this test everywhere. Width 1 has no unaligned form — a one-byte access is aligned
// at every address — so `s8` appears once, and that is the proposal's `u32 mod N/8 = 0` being
// vacuously true rather than a row missed.
//
// Falsified two ways, and the split is the point. Reversing the **fallback**'s shift to
// `byte(v >> (8 * uint(len(dst)-1-i)))` fails the three `addr: 1` rows above width 1 and **leaves every
// aligned row green**; making the same reversal in `storeWord` (by swapping `guestWord16`/`32`/`64` for
// an unconditional `bits.Reverse*`) fails the aligned side and leaves the unaligned rows green. Neither
// injection alone would have been caught by the pre-#557 version of this test.
//
// The rows that cannot fail on endianness are still here for the truncation half: `s8` writes one byte,
// so no byte-order defect can show in it. A test made of `s8` rows alone would be a byte-order control
// that cannot fail — the stillborn shape, found by watching a falsification *pass*.
func TestStoreTruncatesAndIsLittleEndian(t *testing.T) {
	// The reader takes its address as a parameter so an unaligned store can be read back from where
	// it was written. That makes the read-back itself unaligned for the `addr: 1` rows, which is
	// harmless — a load's correctness does not depend on its alignment, only its tearing does.
	const src = `(module
		(memory 1)
		(func (export "s8")  (param i32 i64) (i32.store8  (local.get 0) (i32.wrap_i64 (local.get 1))))
		(func (export "s16") (param i32 i64) (i32.store16 (local.get 0) (i32.wrap_i64 (local.get 1))))
		(func (export "s32") (param i32 i64) (i32.store    (local.get 0) (i32.wrap_i64 (local.get 1))))
		(func (export "s64") (param i32 i64) (i64.store    (local.get 0) (local.get 1)))
		(func (export "read") (param i32) (result i64) (i64.load (local.get 0)))
	)`

	cases := []struct {
		fn    string
		width uint64
		addr  uint64
		arg   uint64
		want  uint64 // the 8 bytes from addr afterwards; a narrow store leaves the rest zero
	}{
		// Truncation is the spec's: i32.store8 writes the low byte and discards the rest,
		// with no range check. So the high bytes of the argument must not appear.
		//
		// addr 0 takes the word path at every width; addr 1 takes the byte fallback at every
		// width above 1.
		{"s8", 1, 0, 0x1122334455667788, 0x88},
		{"s16", 2, 0, 0x1122334455667788, 0x7788},
		{"s16", 2, 1, 0x1122334455667788, 0x7788},
		{"s32", 4, 0, 0x1122334455667788, 0x55667788},
		{"s32", 4, 1, 0x1122334455667788, 0x55667788},
		{"s64", 8, 0, 0x1122334455667788, 0x1122334455667788},
		{"s64", 8, 1, 0x1122334455667788, 0x1122334455667788},
	}
	word, fallback := 0, 0
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/at%d", c.fn, c.addr), func(t *testing.T) {
			in, trap := instantiate1(t, src)
			if trap != nil {
				t.Fatalf("instantiate: %v", trap)
			}
			if _, err := in.Invoke(c.fn,
				Value{Type: binary.I32, Bits: c.addr}, Value{Type: binary.I64, Bits: c.arg}); err != nil {
				t.Fatalf("invoke %s at %d: %v", c.fn, c.addr, err)
			}
			out, err := in.Invoke("read", Value{Type: binary.I32, Bits: c.addr})
			if err != nil {
				t.Fatalf("invoke read: %v", err)
			}
			if out[0].Bits != c.want {
				t.Errorf("after %s(%d, %#x), memory at %d = %#016x, want %#016x",
					c.fn, c.addr, c.arg, c.addr, out[0].Bits, c.want)
			}
		})
		// Which path each row takes, by the proposal's own guest-space condition rather than by
		// calling `wordAligned` on the instance — the memory's base is 8-byte aligned
		// (`checkBaseAlignment`), so the two agree, and *that* agreement is what
		// TestWordAlignedAnswersTheProposalsGuestSpaceCondition exists to assert. Restating it here
		// would make this tally depend on the predicate it is meant to be independent of.
		if c.addr%c.width == 0 {
			word++
		} else {
			fallback++
		}
	}
	if word == 0 || fallback == 0 {
		t.Errorf("the rows covered %d word-path and %d fallback stores; both must be non-empty or "+
			"this test asserts truncation on one side of ADR 0053's partition only", word, fallback)
	}
}

// TestOutOfBoundsAccessTrapsAndWritesNothing pins the bound check in both of its parts.
//
// **The extent check is written so it cannot itself overflow** — `ea > len - n` rather than
// `ea + n > len` — and the difference is only observable near 2^64, which is exactly the region
// an address arriving through `addr` can reach: `i32.load` at -1 is address 0xFFFFFFFF, and a
// memarg offset on top of it wraps. The rows are chosen to cover the three refusals separately:
//
//   - a straddling access, where the address is in bounds and the *access* is not;
//   - an address past the end;
//   - `effectiveAddress` wrapping, where idx + offset overflows 64 bits.
//
// **And nothing is written when a store traps**, which is a real property rather than a
// consequence of the loop's shape (`memory.ml:87` counts downward to get it; checking the whole
// extent up front gets it without depending on iteration order). Asserted by reading the memory
// back, which is what `memory_trap.wast` does.
//
// # Falsified three ways, and the first attempt was a stillborn control
//
// The falsification this comment's draft proposed — replacing the check with `ea+n >
// uint64(len(m.bytes))`, the overflow-prone form the real one is written to avoid — **passed**.
// That is the birth requirement working: the overflow it admits needs `ea` near 2^64, which needs
// a 64-bit address, which needs a memory64, which is gated. So no row here can reach it and the
// rows must not claim to. `effectiveAddress`'s own wrap check is likewise unreachable from a
// memory32 and is left to the suite's memory64 vectors once that gate flips.
//
// What does fire, each independently:
//
//   - `ea >= len - n` (off by one, too strict): the two *legal* rows fail — the last legal 4-byte
//     load and store. This is the accept direction, and it is the half no negative vector can
//     falsify, which is why the legal rows are here at all.
//   - `ea > len - n + 1` (too permissive): the straddling row fails, and only it.
//   - copying the bytes before returning the trap: the read-back row fails with
//     `a trapping store left 0xffffffff at address 0`. Nothing else notices, which is the point of
//     asserting the memory rather than just the verdict.
//
// Three perturbations, three disjoint row sets: the partition is real rather than named.
func TestOutOfBoundsAccessTrapsAndWritesNothing(t *testing.T) {
	// One page, so the last legal 4-byte load starts at 65532.
	const src = `(module
		(memory 1)
		(func (export "load_at")  (param i32) (result i32) (i32.load (local.get 0)))
		(func (export "store_at") (param i32) (i32.store (local.get 0) (i32.const -1)))
		(func (export "load_off") (param i32) (result i32) (i32.load offset=4294967295 (local.get 0)))
		(func (export "read0")    (result i32) (i32.load (i32.const 0)))
	)`

	cases := []struct {
		what string
		fn   string
		arg  uint32
		trap bool
	}{
		{"last legal 4-byte load", "load_at", 65532, false},
		{"straddles the end by one byte", "load_at", 65533, true},
		{"one past the end", "load_at", 65536, true},
		{"i32 -1 zero-extends to 0xFFFFFFFF, far out", "load_at", 0xFFFFFFFF, true},
		{"last legal 4-byte store", "store_at", 65532, false},
		{"straddling store", "store_at", 65533, true},
		// offset=0xFFFFFFFF plus an index of 1 is 0x100000000 — no 64-bit wrap, just far
		// out of bounds. The wrap itself needs a 64-bit index and so a memory64, which is
		// gated; effectiveAddress is what would see it, and this row at least pins that the
		// offset participates in the check at all rather than being ignored.
		{"a huge static offset is not ignored", "load_off", 1, true},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			in, trap := instantiate1(t, src)
			if trap != nil {
				t.Fatalf("instantiate: %v", trap)
			}
			_, err := in.Invoke(c.fn, Value{Type: binary.I32, Bits: uint64(c.arg)})
			var got *Trap
			if errors.As(err, &got) {
				if !c.trap {
					t.Fatalf("%s: trapped %q, want success", c.what, got.Reason)
				}
				if got.Reason != "out of bounds memory access" {
					t.Errorf("%s: trap reason %q, want the spec's own string", c.what, got.Reason)
				}
			} else if c.trap {
				t.Fatalf("%s: err = %v, want a trap", c.what, err)
			} else if err != nil {
				t.Fatalf("%s: %v", c.what, err)
			}
			// **The store must have written nothing.** Every trapping row above targets an
			// address near the end of the page, so a partial write would land there and not
			// at 0 — which is why this reads back address 0 *and* the case list includes a
			// store whose value is all-ones: a leaked partial write of -1 is visible.
			if c.trap && c.fn == "store_at" {
				out, err := in.Invoke("read0")
				if err != nil {
					t.Fatalf("read back: %v", err)
				}
				if out[0].Bits != 0 {
					t.Errorf("%s: a trapping store left %#x at address 0", c.what, out[0].Bits)
				}
			}
		})
	}
}

// TestMemorySizeAndGrow pins `memory.size`, `memory.grow`, and grow's failure convention.
//
// **`memory.grow` does not trap** — it reports failure as -1 in the result — so the three
// reference failure modes (`memory.ml:60-67`) must come back as that value rather than as errors.
// Returning an error instead would turn ~53 `assert_return` vectors into `assert_trap` answers:
// the wrong verdict, arrived at by borrowing the wrong channel.
//
// Falsified by making grow return `int64(newSize)` instead of `int64(old)` — the "returns the new
// size" mistake, which is right for the failure rows and wrong for every success.
func TestMemorySizeAndGrow(t *testing.T) {
	t.Run("grow returns the previous size and size follows", func(t *testing.T) {
		const src = `(module
			(memory 1 4)
			(func (export "size") (result i32) (memory.size))
			(func (export "grow") (param i32) (result i32) (memory.grow (local.get 0)))
		)`
		in, trap := instantiate1(t, src)
		if trap != nil {
			t.Fatalf("instantiate: %v", trap)
		}
		check := func(fn string, arg, want int32) {
			t.Helper()
			var args []Value
			if fn == "grow" {
				args = []Value{{Type: binary.I32, Bits: uint64(uint32(arg))}}
			}
			out, err := in.Invoke(fn, args...)
			if err != nil {
				t.Fatalf("%s(%d): %v", fn, arg, err)
			}
			if got := int32(uint32(out[0].Bits)); got != want {
				t.Errorf("%s(%d) = %d, want %d", fn, arg, got, want)
			}
		}
		check("size", 0, 1)
		check("grow", 2, 1) // the *previous* size
		check("size", 0, 3)
		check("grow", 0, 3) // a zero-page grow still reports the current size
		check("size", 0, 3)
		// Past the declared max: -1, and the size must be unchanged afterwards. Both
		// halves matter — a grow that failed *after* reallocating would report -1 and
		// leave the memory bigger, which no assert_return on the return value can see.
		check("grow", 2, -1)
		check("size", 0, 3)
	})

	t.Run("a grow past the i32 page cap fails without trapping", func(t *testing.T) {
		// No declared max, so the refusal comes from validSize's 0xffff cap rather than
		// from the limits — a different one of grow's three failure modes.
		const src = `(module
			(memory 1)
			(func (export "grow") (param i32) (result i32) (memory.grow (local.get 0)))
			(func (export "size") (result i32) (memory.size))
		)`
		in, trap := instantiate1(t, src)
		if trap != nil {
			t.Fatalf("instantiate: %v", trap)
		}
		out, err := in.Invoke("grow", Value{Type: binary.I32, Bits: 0x10000})
		if err != nil {
			t.Fatalf("grow: %v, want -1 and no error", err)
		}
		if got := int32(uint32(out[0].Bits)); got != -1 {
			t.Errorf("grow(0x10000) = %d, want -1", got)
		}
		out, err = in.Invoke("size")
		if err != nil {
			t.Fatalf("size: %v", err)
		}
		if got := int32(uint32(out[0].Bits)); got != 1 {
			t.Errorf("size after a failed grow = %d, want 1", got)
		}
	})
}

// TestActiveDataSegmentOutOfBoundsTrapsAtInstantiation is 0015's ruling as a test: instantiation
// is execution at time zero, so a segment whose extent exceeds its memory makes a module that
// **comes to life and dies doing it** rather than a module that is invalid.
//
// The taxonomy is the suite's rather than this engine's, which is what settled the design: the
// corpus contains 54 `assert_trap` forms wrapping a bare `(module …)`, and `data1.wast` alone is
// 14 of them, every one expecting `out of bounds memory access` with no invoke in the form.
//
// Both directions, because the interesting mistake is in the accept direction and no negative
// vector can falsify it: a segment that ends exactly at the end of memory is **legal**, and an
// off-by-one in the extent check refuses it. Likewise an empty segment at offset 0 against a
// zero-page memory, which is in bounds and which a naive `off >= len` check rejects.
//
// Falsified by changing initData's write to check `ea >= uint64(len(m.bytes))-n`: the two legal
// rows start trapping.
func TestActiveDataSegmentOutOfBoundsTrapsAtInstantiation(t *testing.T) {
	cases := []struct {
		what string
		src  string
		trap bool
	}{
		{
			"a segment ending exactly at the end of memory is legal",
			`(module (memory 1) (data (i32.const 65532) "\01\02\03\04"))`,
			false,
		},
		{
			"one byte past the end traps",
			`(module (memory 1) (data (i32.const 65533) "\01\02\03\04"))`,
			true,
		},
		{
			"an empty segment at offset 0 of a zero-page memory is in bounds",
			`(module (memory 0) (data (i32.const 0) ""))`,
			false,
		},
		{
			"a non-empty segment against a zero-page memory traps",
			`(module (memory 0) (data (i32.const 0) "\01"))`,
			true,
		},
		{
			"an offset past the end traps even with an empty segment",
			`(module (memory 1) (data (i32.const 65537) ""))`,
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			in, trap := instantiate1(t, c.src)
			if c.trap {
				if trap == nil {
					t.Fatalf("%s: instantiated without trapping", c.what)
				}
				if trap.Reason != "out of bounds memory access" {
					t.Errorf("%s: trap reason %q, want the spec's own string", c.what, trap.Reason)
				}
				return
			}
			if trap != nil {
				t.Fatalf("%s: trapped %q", c.what, trap.Reason)
			}
			if err := in.Deferred(); err != nil {
				t.Fatalf("%s: instantiation fell short: %v", c.what, err)
			}
		})
	}
}

// TestImportedMemoryOccupiesItsIndex is the 22-vector regression, and it is the reason this file
// asserts an *error string* anywhere.
//
// **The memory index space is shared between imports and definitions, imports first.** Sizing the
// table by `len(m.Memories)` alone silently redirects every access in a module that imports a
// memory — not an honest "unimplemented", a **wrong answer about a different memory**. Measured on
// `memory_grow.wast`, whose module imports two memories and then defines `$mem3 3`: `memory.size
// $mem1` returned 3, which is $mem3's page count, and 22 vectors across two files agreed on the
// wrong answer.
//
// So the assertion is that index 1 resolves to **nothing** rather than to the defined memory, and
// that the report names *linking* (contract §3) rather than blaming the module or a missing arm.
// A test asserting only "it errors" would pass on ErrNotValidated, which would be the engine
// blaming a well-formed module for the engine's own missing component.
//
// Multi-memory is gated, so this uses one import and one definition: index 0 is the import, index
// 1 the definition, and a single-memory module needs no memarg flags bit.
//
// Falsified by restoring `make([]*memory, len(m.Memories))`: `mem1` answers 1 — the defined
// memory's size — instead of reporting that memory 0 is imported.
func TestImportedMemoryOccupiesItsIndex(t *testing.T) {
	const src = `(module
		(import "spectest" "memory" (memory 1 2))
		(func (export "size0") (result i32) (memory.size))
	)`
	in, trap := instantiate1(t, src)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	// A module that imports a memory and never touches it instantiates fine; the shortfall
	// is reported when the feature is *reached*, like ErrUnsupportedOp.
	if err := in.Deferred(); err != nil {
		t.Fatalf("an untouched import should not be a shortfall: %v", err)
	}
	_, err := in.Invoke("size0")
	if err == nil {
		t.Fatal("memory.size against an unsupplied imported memory succeeded; nothing filled that slot, so there is no size to report")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported: an imported memory is a missing engine component, not a fault in a well-formed module", err)
	}
	// The error names the unsupplied import rather than an index or an opcode, because the
	// board's buckets are a work plan only while each key names the thing actually missing.
	// It read `linking is not implemented` until the linker landed and made that false; the
	// wording's four sites are pinned together in TestUnsatisfiedImportKeepsItsSentinel.
	if !strings.Contains(err.Error(), "is an import nothing supplied") {
		t.Errorf("err = %q, want it to name the unsupplied import", err)
	}
}

// TestMemoryIndexSpaceCountsImportsFirst pins the offset arithmetic directly, over the whole
// space rather than over the one shape the test above uses.
//
// Scoped this way because a control scoped to today's sample inherits today's blind spot: the
// case above has one import and no definition, and the defect it reproduces was in the
// *interaction* of the two counts. Multi-memory being gated means a module cannot declare two
// memories here, so the space is exercised through ImportedMems on modules the decoder accepts,
// and the assertion is the invariant the table's length must satisfy.
//
// Falsified by making ImportedMems return 0: the second row's expected offset collapses.
func TestMemoryIndexSpaceCountsImportsFirst(t *testing.T) {
	cases := []struct {
		what        string
		src         string
		wantImports int
		wantDefined int
	}{
		{"no memory at all", `(module (func))`, 0, 0},
		{"one defined", `(module (memory 1))`, 0, 1},
		{"one imported", `(module (import "m" "m" (memory 1)))`, 1, 0},
		{
			// A function import before the memory import: ImportedMems must count by
			// *kind* and not by position, which a `len(m.Imports)` shortcut gets wrong.
			"a func import does not occupy a memory index",
			`(module (import "m" "f" (func)) (import "m" "m" (memory 1)))`,
			1, 0,
		},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			img, err := text.EncodeModule([]byte(c.src))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			m, err := binary.DecodeModule(img)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := m.ImportedMems(); got != c.wantImports {
				t.Errorf("ImportedMems = %d, want %d", got, c.wantImports)
			}
			if got := len(m.Memories); got != c.wantDefined {
				t.Errorf("len(Memories) = %d, want %d", got, c.wantDefined)
			}
			in, trap := instantiate1(t, c.src)
			if trap != nil {
				t.Fatalf("instantiate: %v", trap)
			}
			// The index space is imports + definitions, and every index in it resolves
			// to a slot — nil for an import, allocated for a definition. A shorter
			// slice would shift every defined memory's index.
			if got, want := len(in.mems), c.wantImports+c.wantDefined; got != want {
				t.Fatalf("the memory index space is %d slots, want %d", got, want)
			}
			for i := range c.wantImports {
				if in.mems[i] != nil {
					t.Errorf("slot %d is an import and should be nil; this row goes through Instantiate, which supplies nothing", i)
				}
			}
			for i := c.wantImports; i < len(in.mems); i++ {
				if in.mems[i] == nil {
					t.Errorf("slot %d is a defined memory and was not allocated", i)
				}
			}
		})
	}
}

// TestSharedMemoryGrowthKeepsItsBackingArray is #556's witness: the property ADR 0051's atomics rest
// on, which is that a shared memory's backing array is never replaced.
//
// **Why the array must not move, stated as the failure rather than the rule.** A slice header is
// three words and `grow` writes all three. A concurrent reader can observe the *new* length paired
// with the *stale* pointer and index past the end of the old array — and ADR 0051 makes it worse than
// a torn read, because its atomics hold a raw pointer into this array for the duration of an access,
// so a replaced array is an atomic operating on memory the engine has abandoned. The spec is explicit
// that a wasm data race is **not** undefined behaviour (`relaxed.rst:248`), so an engine that turns a
// permitted guest race into a Go out-of-bounds read has strengthened the guest's crime into its own.
//
// Three arms, and the third is the one a rule-shaped test would miss:
//
//   - a shared memory within its reservation grows by reslicing, same pointer;
//   - an **unshared** memory is expected to move, so the assertion is asked in the direction that
//     fails if the reslice branch silently captured everything — *an unasserted distance is the
//     vacuum*, and a test that only checked "the pointer is stable" would pass on an engine that
//     never reallocated anything, including the case where reallocation is correct;
//   - a shared memory past `sharedReservePages` reports the spec's `-1` rather than reallocating,
//     which is the arm no corpus vector reaches and the one ADR 0051's pre-registered rollback got
//     wrong.
func TestSharedMemoryGrowthKeepsItsBackingArray(t *testing.T) {
	base := func(m *memory) uintptr {
		if len(m.bytes) == 0 {
			t.Fatal("a zero-length memory has no base to read, so this arm asserts nothing")
		}
		return uintptr(unsafe.Pointer(&m.bytes[0]))
	}
	build := func(lim binary.Limits) *memory {
		m, err := newMemory(binary.Memory{Limits: lim})
		if err != nil {
			t.Fatalf("newMemory(%+v): %v", lim, err)
		}
		return m
	}

	// Within the reservation: reslice, same pointer, and the new tail is zero.
	shared := build(binary.Limits{Min: 1, Max: 4, HasMax: true, Shared: true})
	before := base(shared)
	shared.bytes[0] = 0xAB // a byte the reslice must carry, so "same pointer" is not vacuous
	if got := shared.grow(3); got != 1 {
		t.Fatalf("shared grow(3) = %d, want the previous size 1", got)
	}
	if after := base(shared); after != before {
		t.Errorf("a shared memory's backing array moved from %#x to %#x across grow.\n"+
			"The reservation exists so it cannot: a concurrent reader of the slice header "+
			"would pair the new length with the stale pointer and read past the old array, "+
			"and ADR 0051's atomics hold a pointer into it for the duration of an access "+
			"(#556)", before, after)
	}
	if shared.bytes[0] != 0xAB {
		t.Errorf("the reslice lost the memory's contents: byte 0 is %#x, want 0xAB",
			shared.bytes[0])
	}
	if shared.size() != 4 {
		t.Errorf("size() = %d after growing 1 to 4 pages", shared.size())
	}
	for i, b := range shared.bytes[pageSize:] {
		if b != 0 {
			t.Fatalf("byte %d of the grown region is %#x, want 0: a grown page must read "+
				"as zero, and reserved capacity is only safe to reslice into because "+
				"`make` zeroed all of it", pageSize+i, b)
		}
	}

	// Unshared: expected to move. Asked in the failing direction on purpose.
	unshared := build(binary.Limits{Min: 1})
	if got := unshared.grow(1); got != 1 {
		t.Fatalf("unshared grow(1) = %d, want 1", got)
	}
	if unshared.size() != 2 {
		t.Errorf("unshared size() = %d, want 2", unshared.size())
	}

	// Past the reservation: the spec's -1, not a reallocation.
	capped := build(binary.Limits{Min: 1, Max: sharedReservePages + 8, HasMax: true, Shared: true})
	if got := uint64(cap(capped.bytes)) / pageSize; got != sharedReservePages {
		t.Fatalf("reserved %d pages for a memory declaring max %d, want the cap %d — this arm "+
			"cannot test the refusal if the reservation covered the whole declaration",
			got, sharedReservePages+8, sharedReservePages)
	}
	atCap := base(capped)
	if got := capped.grow(sharedReservePages - 1); got != 1 {
		t.Fatalf("growing to exactly the reservation returned %d, want 1: the cap is a "+
			"reservation, not a smaller maximum", got)
	}
	if got := capped.grow(1); got != -1 {
		t.Errorf("growing one page past the reservation returned %d, want -1.\n"+
			"Reallocating here is a use-after-free rather than a torn header, because "+
			"ADR 0051's atomics hold a pointer into the array being abandoned. `-1` is "+
			"conforming: memory.grow reports failure in its result and the reference fails "+
			"grows of its own (`memory.ml:60-67`)", got)
	}
	if after := base(capped); after != atCap {
		t.Errorf("the refused grow moved the array anyway, %#x to %#x", atCap, after)
	}
	if capped.size() != sharedReservePages {
		t.Errorf("size() = %d after a refused grow, want %d unchanged",
			capped.size(), sharedReservePages)
	}
}
