// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slice 5's witnesses — the index-space-reading instructions driven through the real path.
//
// # The accept direction is first, for the reason it always is
//
// All 350 vectors this slice converts on the default board are `assert_invalid` rows, so every one
// of them is satisfied by *any* refusal (contract §9 G-3). A rule that reads the wrong index space,
// swaps two immediates, or demands an i64 where the reference asks for an i32 refuses **valid**
// modules, and that failure appears on no board: not as a fail, but as a module nobody wrote. The
// accept rows below are the only place in the project where those defects are observable.
//
// # What cannot be witnessed here yet, stated rather than left as a gap
//
// `table.grow` and `table.fill` take a *reference* operand, and the two instructions that produce
// one — `ref.null` and `ref.func` — are still declined (they are among the 39 declines slice 5
// leaves, and slice 6's subject). A valid module using them is therefore refused by this package
// for a reason that has nothing to do with slice 5, which would make such a row a witness to the
// wrong thing. The rows below reach the reference operand through a `funcref` **parameter** and
// `local.get` instead, which slice 1 types — so the accept direction is covered, by routing around
// a decline rather than by waiting for it.

// TestBulkAcceptsValidModules is the accept direction for the index-space family.
//
// One row per rule, each chosen because a plausible wrong signature rejects it.
func TestBulkAcceptsValidModules(t *testing.T) {
	for _, c := range []struct {
		name string
		why  string
		wat  string
		gate func(*binary.Features)
	}{
		{
			name: "memory.fill takes a byte value as an i32",
			why: "`[at; I32T; at]` (valid.ml:695): the middle operand is a byte widened into an " +
				"i32 and is *not* an address, so a signature written as three addresses still " +
				"accepts this on a 32-bit memory and rejects it on a 64-bit one — which is why the " +
				"memory64 row below exists beside it",
			wat: `(module (memory 1) (func (memory.fill (i32.const 0) (i32.const 0) (i32.const 1))))`,
		},
		{
			name: "memory.fill on a 64-bit memory keeps the i32 value",
			why: "the row that discriminates: with `at` = i64 the destination and length are i64 " +
				"and the value stays i32. A three-addresses signature rejects this, and a " +
				"three-i32s one rejects it too — only the reference's shape accepts",
			wat:  `(module (memory i64 1) (func (memory.fill (i64.const 0) (i32.const 0) (i64.const 1))))`,
			gate: func(f *binary.Features) { f.Memory64 = true },
		},
		{
			name: "memory.init addresses the memory and indexes the segment",
			why: "`[at; I32T; I32T]` (valid.ml:706): only the destination is an address. A segment " +
				"is indexed rather than addressed, so its offset and length have no width — a " +
				"signature that made all three `at` rejects this on a memory64 module",
			wat: `(module (memory 1) (data "x")
				(func (memory.init 0 (i32.const 0) (i32.const 0) (i32.const 1))))`,
		},
		{
			name: "memory.init on a 64-bit memory keeps i32 for the segment side",
			why: "the discriminating half of the row above, and the reason minAddrType is not " +
				"reached here: the asymmetry is between address and index, not between two widths",
			wat: `(module (memory i64 1) (data "x")
				(func (memory.init 0 (i64.const 0) (i32.const 0) (i32.const 1))))`,
			gate: func(f *binary.Features) { f.Memory64 = true },
		},
		{
			name: "data.drop names a declared segment",
			why: "`data c x` returns unit (valid.ml:711) — the rule is an existence check with no " +
				"operands at all, so a signature that popped anything rejects every use of it",
			wat: `(module (memory 1) (data "x") (func (data.drop 0)))`,
		},
		{
			name: "memory.copy takes two addresses and a length",
			why: "`min at1 at2` for the length (valid.ml:700), which on two 32-bit memories is i32 " +
				"and indistinguishable from every wrong rule — this row is the base case the mixed " +
				"one is read against",
			wat: `(module (memory 1) (func (memory.copy (i32.const 0) (i32.const 0) (i32.const 1))))`,
		},
		{
			name: "table.size yields the table's address type",
			why: "`[] --> [at]` (valid.ml:618): no operands and one result. A signature with the " +
				"result in the params list leaves the stack short and fails at the function's end",
			wat: `(module (table 1 funcref) (func (result i32) (table.size)))`,
		},
		{
			name: "table.grow takes the element type below the delta",
			why: "`[RefT rt; at] --> [at]` (valid.ml:622) — the operand *order* is the trap, the " +
				"reference value being pushed first and the delta second. The funcref arrives by " +
				"parameter because ref.null is slice 6's",
			wat: `(module (table 1 funcref) (func (param funcref) (result i32)
				(table.grow (local.get 0) (i32.const 1))))`,
		},
		{
			name: "table.fill takes an index, a value, and a count",
			why: "`[at; RefT rt; at]` (valid.ml:627): the reference sits *between* two addresses, " +
				"unlike memory.fill where the odd operand is an i32 — the two are easy to write " +
				"from each other and this row separates them",
			wat: `(module (table 1 externref) (func (param externref)
				(table.fill (i32.const 0) (local.get 0) (i32.const 1))))`,
		},
		{
			name: "table.fill on an externref table takes an externref",
			why: "the element type is read from the *table*, not assumed to be funcref — a rule " +
				"that hardcoded funcref rejects this and passes every funcref vector in the corpus",
			wat: `(module (table 1 externref) (func (param externref)
				(table.fill (i32.const 0) (local.get 0) (i32.const 1))))`,
		},
		{
			name: "table.copy between two tables of one type",
			why: "the element-type requirement is satisfied rather than absent, which the reject " +
				"row below cannot show: a rule that always reported a mismatch would pass every " +
				"assert_invalid vector this slice converts",
			wat: `(module (table 1 funcref) (table 1 funcref)
				(func (table.copy 0 1 (i32.const 0) (i32.const 0) (i32.const 1))))`,
		},
		{
			name: "table.init from a matching passive segment",
			why: "the accept half of the segment/table match, and the row whose immediates the " +
				"swap test below reads — here both indices are 0, which is the coincidence that " +
				"makes a swapped rule look right",
			wat: `(module (func $f) (table 1 funcref) (elem funcref (ref.func $f))
				(func (table.init 0 0 (i32.const 0) (i32.const 0) (i32.const 1))))`,
		},
		{
			name: "elem.drop names a declared segment",
			why: "`elem c x` (valid.ml:649), the element-segment twin of data.drop — it reads a " +
				"*type* where data.drop reads only an existence, so a rule sharing one lookup " +
				"between them would have to discard something",
			wat: `(module (func $f) (elem funcref (ref.func $f)) (func (elem.drop 0)))`,
		},
		{
			name: "memory.size yields the memory's address type",
			why: "the widening's own row: `[] --> [at]` (valid.ml:687). Plain 0x3F, in this slice " +
				"because it resolves `memory c x` — before the widening it declined, which is a " +
				"fail on the board and an untyped instruction in a module reported valid",
			wat: `(module (memory 1) (func (result i32) (memory.size)))`,
		},
		{
			name: "memory.grow takes and yields the address type",
			why: "`[at] --> [at]` (valid.ml:691), the arm two lines from MemorySize's and sharing " +
				"its preamble — which is the grouping argument bulk.go's header makes",
			wat: `(module (memory 1) (func (result i32) (memory.grow (i32.const 1))))`,
		},
		{
			name: "memory.size on a 64-bit memory yields an i64",
			why: "the discriminating row for the widening: a hardcoded i32 result is right for " +
				"every default-lane vector and wrong here, which is exactly the shape addrTypeAt's " +
				"own comment records being wrong about memory 0",
			wat:  `(module (memory i64 1) (func (result i64) (memory.size)))`,
			gate: func(f *binary.Features) { f.Memory64 = true },
		},
		{
			name: "memory.grow on a 64-bit memory takes and yields an i64",
			why:  "its twin, and the operand direction as well as the result",
			wat:  `(module (memory i64 1) (func (result i64) (memory.grow (i64.const 1))))`,
			gate: func(f *binary.Features) { f.Memory64 = true },
		},
		{
			name: "trunc_sat resolves through the mnemonic",
			why: "the eight opcodes `signature`'s doc comment claimed and could not reach: the " +
				"name lookup asked the plain table, which has no row for a prefixed opcode, so " +
				"every one of them declined. This row is the fix's witness in the direction that " +
				"cannot be seen on a board of refusals",
			wat: `(module (func (result i32) (i32.trunc_sat_f32_s (f32.const 1))))`,
		},
		{
			name: "trunc_sat at the other end of the range",
			why: "0xfc 0x07, the last conversion row — a fix that special-cased one sub-opcode " +
				"rather than routing the region would pass the row above and fail this one",
			wat: `(module (func (result i64) (i64.trunc_sat_f64_u (f64.const 1))))`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := validated(t, c.wat, c.gate); err != nil {
				t.Errorf("valid module refused: %v\nwhy this row exists: %s\n%s", err, c.why, c.wat)
			}
		})
	}
}

// TestBulkRejectsWithTheRuleThatRefused is the reject direction, keyed on the wrapped detail.
//
// The corpus already establishes that these modules are refused — 350 vectors' worth. What it
// cannot establish is **which rule** refused, because the expected string is `unknown table` or a
// bare `type mismatch` for nearly all of them. So each row names the rule it means to trip.
func TestBulkRejectsWithTheRuleThatRefused(t *testing.T) {
	for _, c := range []struct {
		name, why, wat, detail string
		is                     error
		gate                   func(*binary.Features)
	}{
		{
			name: "table.size on a table that does not exist",
			why: "the lookup, and the row that pins slice 5 onto slice 1's index space rather " +
				"than a second copy of it — 12 corpus vectors match this message as a substring",
			wat:    `(module (func (result i32) (table.size)))`,
			is:     ErrUnknownTable,
			detail: "unknown table 0 (0 in scope)",
		},
		{
			name: "the lookup happens before the operands are popped",
			why: "`valid.ml` resolves `table c x` in the `let` bindings above the `-->`, so an " +
				"out-of-range table index is `unknown table` and not a type mismatch about an " +
				"operand that was never going to be reached. Ten corpus vectors turn on this " +
				"ordering: reversing it agrees with the reference on every *verdict* and refuses " +
				"all ten with the wrong string",
			wat: `(module (table 1 funcref) (func
				(table.copy 0 4 (f32.const 0) (f32.const 0) (f32.const 0))))`,
			is:     ErrUnknownTable,
			detail: "unknown table 4",
		},
		{
			name: "data.drop on an undeclared segment",
			why: "the data-segment sentinel, whose *only* reachable path is an index overrunning " +
				"an honest count — a module whose data count section disagrees with its segments " +
				"is malformed and refused a layer below",
			wat:    `(module (memory 1) (data "x") (func (data.drop 1)))`,
			is:     ErrUnknownDataSegment,
			detail: "unknown data segment 1 (1 in scope)",
		},
		{
			name: "elem.drop on an undeclared segment",
			why:  "its element-segment twin, which is a separate sentinel because the reference's lookups are",
			wat:  `(module (func $f) (elem funcref (ref.func $f)) (func (elem.drop 1)))`,
			is:   ErrUnknownElemSegment,
			//nolint:misspell // "elem segment" is the reference's own spelling of the sentinel.
			detail: "unknown elem segment 1 (1 in scope)",
		},
		{
			name: "table.copy between mismatched element types",
			why: "`match_reftype c.types t2 t1` (valid.ml:637) — and the row whose message the " +
				"reference transposes, which is why the detail asserted here names source and " +
				"destination the way the `require` actually binds them",
			wat: `(module (table 1 funcref) (table 1 externref)
				(func (table.copy 0 1 (i32.const 0) (i32.const 0) (i32.const 1))))`,
			is:     ErrTypeMismatch,
			detail: "source element type externref does not match destination element type funcref",
		},
		{
			name: "table.init from a segment of the wrong type",
			why:  "the other transposed message (valid.ml:645), and the segment side of the same requirement",
			wat: `(module (func $f) (table 1 externref) (elem funcref (ref.func $f))
				(func (table.init 0 0 (i32.const 0) (i32.const 0) (i32.const 1))))`,
			is:     ErrTypeMismatch,
			detail: "element segment's type funcref does not match table's element type externref",
		},
		{
			name: "memory.fill's value operand is not an address",
			why: "the reject half of the memory64 accept row: under a three-addresses signature " +
				"this module *validates*, so the row is a witness in the direction a board can " +
				"see for a defect it cannot",
			wat:    `(module (memory i64 1) (func (memory.fill (i64.const 0) (i64.const 0) (i64.const 1))))`,
			is:     ErrTypeMismatch,
			detail: "instruction requires [i64 i32 i64] but stack has [i64 i64 i64]",
			gate:   func(f *binary.Features) { f.Memory64 = true },
		},
		{
			name: "table.grow's operands in the wrong order",
			why: "the delta below the reference value rather than above it — the arm's own trap, " +
				"and a swapped signature accepts this and rejects the valid form",
			wat: `(module (table 1 funcref) (func (param funcref) (result i32)
				(table.grow (i32.const 1) (local.get 0))))`,
			is: ErrTypeMismatch,
			// Both halves, because the operands are each other's wrong type and a message naming
			// only one of them would read the same under either ordering: the params are popped in
			// reverse, so the delta's i32 is expected against a funcref on top.
			detail: "instruction requires [funcref i32] but stack has [i32 funcref]",
		},
		{
			name: "memory.size on a module with no memory",
			why: "the widening's reject row: before it, this module declined — a *fail* on the " +
				"board, but with the wrong testimony, since the reference has a verdict here and " +
				"so does this package",
			wat:    `(module (func (result i32) (memory.size)))`,
			is:     ErrUnknownMemory,
			detail: "unknown memory 0",
		},
		{
			name: "memory.grow's result is the address type, not an i32",
			why: "the accept-direction defect made visible: with a hardcoded i32 result this " +
				"memory64 module validates, and no assert_invalid vector can report that",
			wat:    `(module (memory i64 1) (func (result i32) (memory.grow (i64.const 1))))`,
			is:     ErrTypeMismatch,
			detail: "i64",
			gate:   func(f *binary.Features) { f.Memory64 = true },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, c.gate)
			if err == nil {
				t.Fatalf("invalid module accepted — and an accepted-but-invalid module is the "+
					"failure no `assert_invalid` vector can report (§9 G-3)\nwhy this row "+
					"exists: %s\n%s", c.why, c.wat)
			}
			if !errors.Is(err, c.is) {
				t.Errorf("refused with %v, want the %v family\nwhy: %s", err, c.is, c.why)
			}
			if !strings.Contains(err.Error(), c.detail) {
				t.Errorf("refused with %q, which does not name the rule — want a message "+
					"containing %q\nwhy: %s", err, c.detail, c.why)
			}
		})
	}
}

// TestBulkInitImmediatesAreNotSwapped is the immediate-order witness for `table.init`.
//
// `memory.init` and `table.init` name the destination first in the *text* and second in the
// *encoding* (`decode.ml:669,674`), so `Imm0` is the segment and `Imm1` the table — the opposite
// assignment from `memory.copy`/`table.copy`. Both indices are 0 in nearly every corpus vector, so
// a swapped reading is right by coincidence there, which is the same way `addrTypeAt` was right
// about memory 0 for its whole life before #310.
//
// **The table side is observable in the default lane and the memory side is not**, multi-memory
// being gated: a module cannot carry two memories to tell `memory.init 0 1` from `memory.init 1 0`.
// So the memory half is covered with the gate on, below, rather than left unasserted — a rule
// checked on the one index space where the coincidence does not protect it is checked, and the
// other space's identical rule is not.
func TestBulkInitImmediatesAreNotSwapped(t *testing.T) {
	// Table 0 is externref, table 1 is funcref, and the single passive segment is funcref. So
	// `table.init 1 0` is valid, and under the swapped reading it resolves table 0 (externref)
	// against segment index 1 (which does not exist) — two different refusals, either of which
	// distinguishes the readings.
	const wat = `(module
		(func $f)
		(table 1 externref)
		(table 1 funcref)
		(elem funcref (ref.func $f))
		(func (table.init 1 0 (i32.const 0) (i32.const 0) (i32.const 1))))`

	if _, err := validated(t, wat, nil); err != nil {
		t.Errorf("valid module refused: %v\n"+
			"table 1 is funcref and segment 0 is funcref, so this validates; the swapped reading "+
			"resolves table 0 (externref) against a segment index that does not exist. Both "+
			"immediates are 0 in nearly every corpus vector, which is why this module exists "+
			"rather than a vector: %s", err, wat)
	}

	// The memory half, gated. Two memories of *different widths* make the swap observable through
	// the address type alone: `memory.init 1 0` types its destination as the i64 memory, and the
	// swapped reading types it as the i32 one.
	const memWat = `(module
		(memory 1)
		(memory i64 1)
		(data "x")
		(func (memory.init 1 0 (i64.const 0) (i32.const 0) (i32.const 1))))`

	_, err := validated(t, memWat, func(f *binary.Features) {
		f.MultiMemory = true
		f.Memory64 = true
	})
	if err != nil {
		t.Errorf("valid module refused: %v\n"+
			"memory 1 is 64-bit so the destination is an i64; the swapped reading reads memory 0 "+
			"and wants an i32. Unobservable in the default lane, multi-memory being gated: %s",
			err, memWat)
	}
}

// TestMixedWidthCopyTakesAnI32Length is minAddrType's witness — the reference's `min at1 at2`.
//
// OCaml's polymorphic `min` over `addrtype = I32AT | I64AT` (`types.ml:15`) compares by constructor
// order, so a copy between a 32-bit and a 64-bit side takes an **i32** length. The plausible wrong
// rules are "the destination's width" and "the wider of the two", and both accept an i64 length
// here — an accept-direction defect, invisible on any board of refusals (§9 G-3).
//
// `table_copy_mixed.wast` is the corpus file whose whole subject this is (1/4 → 4/4 in the
// all-gates-on lane, 0 in the default lane), and it is not a substitute: every one of its vectors
// is an `assert_invalid`, so it witnesses that a verdict was reached and never that the length
// operand's *type* was right.
func TestMixedWidthCopyTakesAnI32Length(t *testing.T) {
	gate := func(f *binary.Features) { f.Memory64 = true }

	// Table 0 is 64-bit, table 1 is 32-bit. `min i64 i32` is i32, so the length is an i32 while
	// both index operands keep their own tables' widths.
	const valid = `(module (table i64 1 funcref) (table 1 funcref)
		(func (table.copy 0 1 (i64.const 0) (i32.const 0) (i32.const 1))))`
	if _, err := validated(t, valid, gate); err != nil {
		t.Errorf("valid module refused: %v\n"+
			"the destination index is i64 (table 0), the source index i32 (table 1), and the "+
			"length i32 because `min` takes constructor order: %s", err, valid)
	}

	// The same copy with an i64 length, which is what a "destination's width" or "wider of the
	// two" rule accepts.
	const wrong = `(module (table i64 1 funcref) (table 1 funcref)
		(func (table.copy 0 1 (i64.const 0) (i32.const 0) (i64.const 1))))`
	_, err := validated(t, wrong, gate)
	if err == nil {
		t.Fatal("an i64 length was accepted for a mixed-width table.copy; `min at1 at2` gives i32 " +
			"unless both sides are 64-bit, and no assert_invalid vector can report this because " +
			"the failure is an acceptance")
	}
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("refused with %v, want ErrTypeMismatch — a refusal for another reason would "+
			"pass this row while leaving the length rule unasserted", err)
	}
}

// TestBulkOpcodesMatchTheTable checks bulk.go's named constants against the generated table.
//
// `TestStructuralOpcodesMatchTheTable` is the precedent and the argument is its argument: a named
// constant is a transcription of a generated row, and an unchecked transcription is the class
// `sig.go`'s header exists to remove. The hazard here is the adjacency — `table_grow` (0x0f),
// `table_size` (0x10) and `table_fill` (0x11) are three consecutive bytes whose signatures differ
// in exactly the way a one-off error produces, and a module using the wrong one of the pair
// `table_size`/`table_grow` decodes perfectly.
//
// **The immediate count is asserted beside the mnemonic**, PrefixedOp's own comment giving the
// reason: a name-only check passes a regenerated table that renumbered a region while keeping its
// names, and the two-index arms are exactly the ones whose immediates this slice reads.
func TestBulkOpcodesMatchTheTable(t *testing.T) {
	for _, c := range []struct {
		op       uint32
		mnemonic string
		imms     int
	}{
		{fcMemoryInit, "memory_init", 2},
		{fcDataDrop, "data_drop", 1},
		{fcMemoryCopy, "memory_copy", 2},
		{fcMemoryFill, "memory_fill", 1},
		{fcTableInit, "table_init", 2},
		{fcElemDrop, "elem_drop", 1},
		{fcTableCopy, "table_copy", 2},
		{fcTableGrow, "table_grow", 1},
		{fcTableSize, "table_size", 1},
		{fcTableFill, "table_fill", 1},
	} {
		t.Run(c.mnemonic, func(t *testing.T) {
			name, imms, ok := binary.PrefixedOp(prefixBulk, c.op)
			if !ok {
				t.Fatalf("the generated table has no row for %#02x %#02x, so this constant names "+
					"an instruction the decoder cannot produce", prefixBulk, c.op)
			}
			if name != c.mnemonic {
				t.Errorf("%#02x %#02x is %q in the table, not %q — the constant and the rule it "+
					"guards are describing different instructions", prefixBulk, c.op, name, c.mnemonic)
			}
			if imms != c.imms {
				t.Errorf("%s takes %d immediates in the table, not %d; this slice reads Imm0 and "+
					"Imm1 by that count", c.mnemonic, imms, c.imms)
			}
		})
	}

	// The trunc_sat range, which is a *boundary* rather than a member list — so both of its ends
	// are asserted. A floor alone would be satisfied by a range that ran past 0x07 and swallowed
	// `memory_init`, which is the arm that would then silently route into `signature` and decline.
	for op := uint32(truncSatFirst); op <= truncSatLast; op++ {
		name, _, ok := binary.PrefixedOp(prefixBulk, op)
		if !ok || !strings.Contains(name, "trunc_sat") {
			t.Errorf("%#02x %#02x is %q (row present: %t), which is inside "+
				"[truncSatFirst, truncSatLast] and must be a saturating conversion — the range "+
				"is what routes these into `signature`", prefixBulk, op, name, ok)
		}
	}
	if name, _, ok := binary.PrefixedOp(prefixBulk, truncSatLast+1); !ok || strings.Contains(name, "trunc_sat") {
		t.Errorf("%#02x %#02x is %q (row present: %t), want the first *non*-conversion row: the "+
			"range's upper end is exact, and a range one too wide routes a bulk operation into "+
			"`signature`, which declines it", prefixBulk, truncSatLast+1, name, ok)
	}
}

// TestPrefixBulkIsTheRegionBinaryDispatches checks the local constant against `binary`'s behaviour
// rather than against `binary`'s source.
//
// `prefixBulk` is local for prefixSIMD's reason, which means two packages hold the byte 0xFC
// independently. The way that agreement breaks is not by someone editing a literal — it is by the
// decoder's escape byte for this region changing, at which point this package's dispatch stops
// firing and every bulk instruction declines. So the check runs a real module through the decoder
// and reads the prefix off the instruction that comes back, which is the only form that fails when
// the *dispatch* diverges rather than when a constant does.
func TestPrefixBulkIsTheRegionBinaryDispatches(t *testing.T) {
	m := decodedModule(t, `(module (memory 1) (data "x")
		(func (memory.init 0 (i32.const 0) (i32.const 0) (i32.const 1))))`, nil)

	var found bool
	for _, in := range m.Funcs[0].Body {
		name, _, ok := binary.PrefixedOp(in.Prefix, in.Op)
		if !ok || name != "memory_init" {
			continue
		}
		found = true
		if in.Prefix != prefixBulk {
			t.Errorf("the decoder produced memory.init with prefix %#02x, but this package "+
				"dispatches on %#02x — the region would decline every instruction in it",
				in.Prefix, prefixBulk)
		}
	}
	if !found {
		t.Fatal("no memory.init in the decoded body: this test asserts nothing unless the " +
			"instruction it looks for is there, and an empty scan agrees with every prefix")
	}
}

// TestPrefixedRegionsPartitionIntoClaimedAndDeclined covers the dispatch over the whole prefix
// space rather than over the specimens available for it.
//
// Three regions are this package's and one is not, and the property worth asserting is not which —
// that changes every slice, and this sentence read "Two regions are this package's and two are not"
// until slice 7 typed 0xFB — but that the partition is **total**: every prefix either types or
// declines *naming itself*, and none falls through to an accept. A control listing today's regions
// inherits today's blind spot; this one derives the claimed set by asking the dispatch.
//
// The derivation is what made the slice-7 edit here small and honest: the loop needed no change at
// all, and what needed changing was the *landed* list below it, which is a claim about history rather
// than about the dispatch. Nothing in this file failed when 0xFB moved — a passing partition test
// whose prose calls a claimed region unclaimed is the drifted-testimony shape, and it is caught by
// reading rather than by running, which is the argument for keeping the two lists adjacent.
//
// It is also where 0xFE (threads) is covered at all. The text encoder has no operator for its
// instructions ("unknown operator memory.atomic.notify"), so no module can be built to carry one
// and `validated()` cannot reach it — which is why the instruction is constructed directly here.
// That is the same layering `TestVecDeclinesWhatThisSliceDoesNotType` names for relaxed SIMD:
// where no module can reach an arm, the arm is exercised directly and the reason is stated.
func TestPrefixedRegionsPartitionIntoClaimedAndDeclined(t *testing.T) {
	// The four regions of the instruction grammar. 0xFE has no table in `binary` at all, which is
	// itself the fact that keeps it out of the claimed set — and asserting over it anyway is the
	// point of a partition test.
	regions := []struct {
		prefix byte
		name   string
	}{
		{prefixGC, "GC"},
		{prefixBulk, "bulk memory/table"},
		{prefixSIMD, "SIMD"},
		{0xfe, "threads"},
	}

	claimed := map[byte]bool{}
	for _, r := range regions {
		// Sub-opcode 0 of each region, typed with no module. A region this package claims either
		// types it or refuses it with a *rule*; a region it does not claim declines with the
		// prefix in the message.
		v := &validator{mod: &binary.Module{}}
		err := func() (err error) {
			// A region whose arm indexes a module this bare would panic, and a panic here is a
			// finding about the dispatch rather than a broken test — recovered so the partition is
			// reported for all four regions instead of the run stopping at the first.
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panicked: %v", r)
				}
			}()
			v.frames = []frame{{}}
			return v.instr(0, binary.Instr{Prefix: r.prefix, Op: 0})
		}()

		declined := errors.Is(err, ErrUnsupported) &&
			strings.Contains(err.Error(), fmt.Sprintf("%#02x", r.prefix))
		switch {
		case declined:
			// An unclaimed region, saying so and naming itself. That is the whole contract.
		case errors.Is(err, ErrUnsupported):
			t.Errorf("the %s region (%#02x) declined without naming its prefix: %v\n"+
				"the decline census is the next slice's work plan, and a decline that does not "+
				"say what it declined is a visible refusal with unusable testimony",
				r.name, r.prefix, err)
		default:
			claimed[r.prefix] = true
		}
	}

	// The vacuity check, and it runs in both directions. With every region declining, the loop
	// above passes while asserting that this package types nothing — and with every region
	// claimed, the decline half has no subject.
	if len(claimed) == 0 {
		t.Error("no prefixed region is claimed, so the loop above only checked that four " +
			"declines name themselves — slice 2 types 0xFD and slice 5 types 0xFC, and a run " +
			"where neither does means the dispatch is not reached at all")
	}
	if len(claimed) == len(regions) {
		t.Error("every prefixed region is claimed, which leaves the decline half of this " +
			"partition with no subject: 0xFE is threads', a v1 milestone behind its own gate, and " +
			"a slice that typed it here would be landing a later phase's capability")
	}
	for _, p := range []byte{prefixBulk, prefixSIMD, prefixGC} {
		if !claimed[p] {
			t.Errorf("region %#02x declined, but its slice has landed — a region that types "+
				"nothing after its slice is a dispatch that stopped firing, which is invisible "+
				"on a board of refusals because a decline is a fail either way", p)
		}
	}
}
