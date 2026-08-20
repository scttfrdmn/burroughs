package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The bulk trio's controls, partitioned by the mistake each catches rather than by opcode: a
// single table over "bulk operations" would have thirty rows saying "the bytes moved" and none
// saying which property any one of them holds down.
//
// # The falsification pass, and the two controls it killed
//
// Thirteen mutations were introduced one at a time and run. **Eleven died with a named failing
// row; two passed, and both of those were controls rather than mutations** — the birth requirement
// working as written, since a falsification that passes is the most informative outcome the
// exercise has.
//
//	mutation                                       verdict
//	`if n == 0` above the check, fill               dies: 1 row (fill one-past-the-end)
//	`if n == 0` above the check, memory.copy        dies: 2 rows (dst and src one-past)
//	`if n == 0` above the check, table.copy         dies: 1 row — the *derived* row, only
//	outOfBounds `>` weakened to `>=`                dies: 5 rows, the at-the-limit successes
//	memory.copy's src bounds check dropped          dies: 1 row + a panic in the extent test
//	copy → forward byte loop, memory.copy           dies: 3 rows of the backward direction
//	copy → forward byte loop, table.copy            dies: 1 row (slot 4)
//	fill's operands popped i/k/n not n/k/i          dies: 4 rows
//	memory.copy's operands popped d/s/n not n/s/d   dies: 13 rows
//	table.copy returns trapOOB not trapOOBTable     dies: 2 rows, one on the message
//	table.copy's Imm0/Imm1 swapped                  dies: **the board**, 24 vectors
//	outOfBounds's wrap arm `end < i` dropped        **PASSES** — TestBulkTrapsOnAnExtentPastTheEnd
//	tableAddr's uint32 narrowing dropped            **PASSES** — see `tableAddr`'s comment
//
// Both survivors are the same finding in two places: an i32 operand cannot express the state the
// check guards against, because `pushI32` zero-extends every i32 slot. The wrap arm and the
// narrowing are each correct, each unreachable, and each documented at its definition instead of
// guarded by a test that would have to fabricate a stack the decoder cannot produce (#125).
//
// # The one mutation with no row here, because the suite already owns it
//
// `table.copy`'s destination/source order — `Imm0` is the destination (`decode.ml:676`) — survives
// every row in this file, because each of them names table 0 twice. It does **not** survive the
// board: `table_copy.wast:774` is `(table.copy $t1 $t0 (i32.const 10) (i32.const 0) (i32.const
// 20))` between two 30-slot tables with *different* element contents, followed by twenty
// `check_t0` and twenty `check_t1` read-backs, and swapping the immediates moves the exec stratum
// 608 → 632. Measured, on the board, not argued from the vector's shape.
//
// So no local row is added, and the reason is the rule rather than the convenience: the suite is
// the oracle, and a hand-written control duplicating a property forty spec assertions already pin
// is a second opinion competing with the first. The two survivors above are documented instead of
// tested for the mirror-image reason — there the suite *cannot* see the property, because no i32
// operand reaches the guarded state.
//
// The distinction is worth stating because it was nearly gotten wrong in the other direction. A
// two-distinct-size control was drafted here — tables of 10 and 4, `d=6 s=0 n=4`, legal one way
// and trapping the other — and it works; it was dropped only after the board was asked and
// answered.
//
// **And `table_copy_mixed.wast`, the file whose name says it is this property's home, cannot
// witness it at all** — for a reason that is not the one first written here. The draft said its
// distinct-table cases need the table64 gate and score `gated`; the board says `1/1 pass, 3
// unsupported`, so that was a guess dressed as a measurement. Reading the file: it *declares* four
// exported functions and **invokes none of them**, then asserts three `type mismatch` modules
// invalid. Nothing in it ever runs a `table.copy`, so no immediate order is observable there under
// any gate. The oracle is `table_copy.wast:774`, which invokes and reads back.
//
// None of the twelve wedged the harness — the loop-row hazard is real here, since these arms
// *are* loops and a mutation that lost an exit condition would time out and name no row. It does
// not fire because the loops are bounded by a slice length rather than by a decremented counter;
// confirmed by running them, not by reading them.
//
// `fill`'s `byte(...)` narrowing is absent from the ledger deliberately: `mem.bytes` is `[]byte`,
// so removing it is a compile error rather than a wrong answer. Noted so the count reads as
// twelve-of-twelve rather than as a row someone forgot.

// TestBulkZeroLengthChecksBoundsFirst is the ordering the reference mandates and the one a
// plausible implementation gets wrong.
//
// **A zero-length run at exactly the end succeeds; one byte past the end traps.** Both halves of
// that come from the reference testing `oob` *before* its `n = 0` exit (`eval.ml:549`, `:567`,
// `:395`), and the suite asserts it in its own words at `bulk.wast:48`-`:53` for fill and
// `:103`-`:111` for copy — including both the destination and the source direction, which is why
// there are two out-of-bounds copy rows rather than one.
//
// This is the early-return grave's shape (#41): an `if n == 0 { return nil }` at the top of each
// arm is the natural way to write these, it is faster, it passes every non-zero row in this file,
// and it fails exactly the four trap rows below. The rows that *succeed* are half the control —
// without them a bounds check off by one in the other direction would trap on a legal
// zero-length run at the limit and nothing here would notice.
func TestBulkZeroLengthChecksBoundsFirst(t *testing.T) {
	// One page, so the region is [0, 0x10000).
	const memMod = `(module (memory 1)
		(func (export "fill") (param i32 i32 i32)
			(memory.fill (local.get 0) (local.get 1) (local.get 2)))
		(func (export "copy") (param i32 i32 i32)
			(memory.copy (local.get 0) (local.get 1) (local.get 2))))`
	// Ten slots, so the region is [0, 10).
	const tabMod = `(module (table 10 funcref)
		(func (export "copy") (param i32 i32 i32)
			(table.copy (local.get 0) (local.get 1) (local.get 2))))`

	cases := []struct {
		what     string
		mod      string
		fn       string
		args     []Value
		wantTrap string // "" means it must succeed
	}{
		{
			"fill 0 bytes at exactly the end succeeds (bulk.wast:49)",
			memMod, "fill",
			[]Value{I32(0x10000), I32(0), I32(0)},
			"",
		},
		{
			"fill 0 bytes one past the end traps (bulk.wast:52)",
			memMod, "fill",
			[]Value{I32(0x10001), I32(0), I32(0)},
			"out of bounds memory access",
		},
		{
			"copy 0 bytes with dst at exactly the end succeeds (bulk.wast:104)",
			memMod, "copy",
			[]Value{I32(0x10000), I32(0), I32(0)},
			"",
		},
		{
			"copy 0 bytes with src at exactly the end succeeds (bulk.wast:105)",
			memMod, "copy",
			[]Value{I32(0), I32(0x10000), I32(0)},
			"",
		},
		{
			"copy 0 bytes with dst one past the end traps (bulk.wast:108)",
			memMod, "copy",
			[]Value{I32(0x10001), I32(0), I32(0)},
			"out of bounds memory access",
		},
		{
			// The row that dies to dropping *either* bounds check independently: the
			// destination is fine and only the source is out of range.
			"copy 0 bytes with src one past the end traps (bulk.wast:110)",
			memMod, "copy",
			[]Value{I32(0), I32(0x10001), I32(0)},
			"out of bounds memory access",
		},
		{
			"table.copy 0 elements at exactly the end succeeds (bulk.wast:344)",
			tabMod, "copy",
			[]Value{I32(10), I32(0), I32(0)},
			"",
		},
		{
			"table.copy 0 elements with src at exactly the end succeeds (bulk.wast:345)",
			tabMod, "copy",
			[]Value{I32(0), I32(10), I32(0)},
			"",
		},
		{
			"table.copy 0 elements one past the end traps (derived; see below)",
			tabMod, "copy",
			[]Value{I32(11), I32(0), I32(0)},
			"out of bounds table access",
		},
	}
	// The last row is **derived**, not cited: `bulk.wast` stops at the succeeding
	// zero-length table copies (`:344`, `:345`) and has no `:346` asserting the trap one
	// past the end, where the memory half has `:108` and `:110`. The premises are the two
	// cited succeeding rows above plus `eval.ml:395`, whose `table_oob` is textually
	// `mem_oob`'s twin over `Table.size` — so the memory half's asserted boundary transfers.
	// Stated because an unstated inference is synthetic with better manners.
	for _, c := range cases {
		in, trap := instantiate1(t, c.mod)
		if trap != nil {
			t.Fatalf("%s: instantiate: %v", c.what, trap)
		}
		if err := in.Deferred(); err != nil {
			t.Fatalf("%s: instantiate fell short: %v", c.what, err)
		}
		_, err := in.Invoke(c.fn, c.args...)
		if c.wantTrap == "" {
			if err != nil {
				t.Errorf("%s\n\tgot %v, want success: a zero-length run inside the "+
					"region is legal, and a bounds check that refuses it is off by one "+
					"in the direction the suite invokes rather than asserts", c.what, err)
			}
			continue
		}
		var tr *Trap
		if !errors.As(err, &tr) {
			t.Errorf("%s\n\tgot %v, want a trap %q: a zero-length fast path placed "+
				"*above* the bounds check answers success here", c.what, err, c.wantTrap)
			continue
		}
		if !strings.Contains(tr.Error(), c.wantTrap) {
			t.Errorf("%s\n\ttrapped %q, want it to contain %q", c.what, tr, c.wantTrap)
		}
	}
}

// TestBulkCopyHandlesOverlapInBothDirections asserts the property that lets the arms omit the
// reference's `I64.le_u d s` branch.
//
// `eval.ml:567` copies forward when `d <= s` and backward from the high end when `d > s`, because
// its per-byte rewrite would otherwise overwrite source bytes before reading them. `bulk.go`
// omits that branch on the ground that Go's `copy` is a memmove — "the source and destination may
// overlap", per the builtin's own documentation. **That is a property nothing in this engine
// exercises, and a property nothing exercises is a claim**, so both directions are pinned here.
//
// The byte patterns are the suite's and the expectations are copied from its read-backs rather
// than recomputed, since recomputing them is precisely the arithmetic under test:
//
//   - `bulk.wast:81` — copy 4 bytes from 10 to 8 (`d < s`, forward), read back at `:82`-`:87`;
//   - `bulk.wast:90` — copy 6 bytes from 7 to 10 (`d > s`, backward), read back at `:91`-`:97`.
//
// A forward byte-at-a-time loop passes the first and smears the second; a backward one passes
// the second and smears the first. Either alone is the fixed-point defect.
func TestBulkCopyHandlesOverlapInBothDirections(t *testing.T) {
	const mod = `(module (memory (data "\aa\bb\cc\dd"))
		(func (export "copy") (param i32 i32 i32)
			(memory.copy (local.get 0) (local.get 1) (local.get 2)))
		(func (export "load8_u") (param i32) (result i32)
			(i32.load8_u (local.get 0))))`

	in, trap := instantiate1(t, mod)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	load := func(addr int32) int64 {
		t.Helper()
		out, err := in.Invoke("load8_u", I32(addr))
		if err != nil {
			t.Fatalf("load8_u %d: %v", addr, err)
		}
		return int64(out[0].Bits)
	}
	do := func(what string, d, s, n int32) {
		t.Helper()
		if _, err := in.Invoke("copy", I32(d), I32(s), I32(n)); err != nil {
			t.Fatalf("%s: copy %d<-%d len %d: %v", what, d, s, n, err)
		}
	}
	check := func(what string, want map[int32]int64) {
		t.Helper()
		for addr, w := range want {
			if got := load(addr); got != w {
				t.Errorf("%s: [%d] = %#02x, want %#02x", what, addr, got, w)
			}
		}
	}

	// The suite's sequence, in order, because each step's expectations depend on the
	// previous step's writes — running these out of order silently changes what is asserted.
	do("non-overlapping (bulk.wast:69)", 10, 0, 4)
	check("non-overlapping (bulk.wast:71-76)", map[int32]int64{
		9: 0x00, 10: 0xaa, 11: 0xbb, 12: 0xcc, 13: 0xdd, 14: 0x00,
	})

	// d=8 < s=10: the forward direction.
	do("overlap, source > dest (bulk.wast:81)", 8, 10, 4)
	check("overlap, source > dest (bulk.wast:82-87)", map[int32]int64{
		8: 0xaa, 9: 0xbb, 10: 0xcc, 11: 0xdd, 12: 0xcc, 13: 0xdd,
	})

	// d=10 > s=7: the backward direction. A forward per-byte loop reads [10] after having
	// written it and produces 0x00,0xaa,0xbb,0xcc,0xdd,0x00 shifted — a plausible-looking
	// smear, which is why the whole read-back is here rather than one probe byte.
	do("overlap, source < dest (bulk.wast:90)", 10, 7, 6)
	check("overlap, source < dest (bulk.wast:91-97)", map[int32]int64{
		10: 0x00, 11: 0xaa, 12: 0xbb, 13: 0xcc, 14: 0xdd, 15: 0xcc, 16: 0x00,
	})
}

// TestBulkTableCopyHandlesOverlapInBothDirections is the same property over `[]ref`.
//
// Separate from the memory test rather than a row in it, because the two arms are separate
// functions over separate slice types: one `copy` call being right says nothing about the other,
// and `table.copy`'s slots carry `ref` values whose identity is observable only through
// `call_indirect`. The suite's own mechanism, so the same one is used here.
//
// `bulk.wast:326` copies 3 slots from 1 to 0 (`d < s`) and `:333` copies 3 from 0 to 2
// (`d > s`), with `call` read-backs at `:328`-`:330` and `:335`-`:337`.
func TestBulkTableCopyHandlesOverlapInBothDirections(t *testing.T) {
	const mod = `(module (table 10 funcref)
		(elem (i32.const 0) $zero $one $two)
		(func $zero (result i32) (i32.const 0))
		(func $one (result i32) (i32.const 1))
		(func $two (result i32) (i32.const 2))
		(func (export "copy") (param i32 i32 i32)
			(table.copy (local.get 0) (local.get 1) (local.get 2)))
		(func (export "call") (param i32) (result i32)
			(call_indirect (result i32) (local.get 0))))`

	in, trap := instantiate1(t, mod)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	do := func(what string, d, s, n int32) {
		t.Helper()
		if _, err := in.Invoke("copy", I32(d), I32(s), I32(n)); err != nil {
			t.Fatalf("%s: table.copy %d<-%d len %d: %v", what, d, s, n, err)
		}
	}
	check := func(what string, want map[int32]int64) {
		t.Helper()
		for slot, w := range want {
			out, err := in.Invoke("call", I32(slot))
			if err != nil {
				t.Errorf("%s: call slot %d: %v", what, slot, err)
				continue
			}
			if got := int64(out[0].Bits); got != w {
				t.Errorf("%s: slot %d called func returning %d, want %d", what, slot, got, w)
			}
		}
	}

	do("non-overlapping (bulk.wast:319)", 3, 0, 3)
	check("non-overlapping (bulk.wast:321-323)", map[int32]int64{3: 0, 4: 1, 5: 2})

	// d=0 < s=1.
	do("overlap, source > dest (bulk.wast:326)", 0, 1, 3)
	check("overlap, source > dest (bulk.wast:328-330)", map[int32]int64{0: 1, 1: 2, 2: 0})

	// d=2 > s=0.
	do("overlap, source < dest (bulk.wast:333)", 2, 0, 3)
	check("overlap, source < dest (bulk.wast:335-337)", map[int32]int64{2: 1, 3: 2, 4: 0})
}

// TestBulkFillTruncatesTheValueToAByte pins the operand semantics and the fill's extent.
//
// The value is an i32 and the reference stores it through `Pack8` (`eval.ml:552`), so the low
// byte is written and the rest discarded — ";; Fill value is stored as a byte", `bulk.wast:34`,
// which fills with `0xbbaa` and reads `0xaa`.
//
// **The `byte(...)` conversion in the arm cannot be removed to falsify this**, which is worth
// saying because the mutation ledger above would otherwise look short one row: `mem.bytes` is
// `[]byte`, so dropping the conversion is a compile error rather than a wrong answer. The row is
// kept because the *plausible* mutation is not deleting the narrowing but doing it at the wrong
// width — a `uint16` or a mask of `0xffff` compiles nowhere either, but a future arm that stages
// this value through a wider slot would, and this row is what would catch it.
//
// The extent rows are the second half: filling [1,4) must leave 0 and 4 untouched. An off-by-one
// in either direction is a plausible number in a plausible place, and `assert_return` cannot see
// it without the neighbours being read too — which is exactly what the suite does at `:28`-`:32`.
//
// # The loop-hazard note
//
// This arm's body is a loop, and a mutation that lost its exit condition would hang rather than
// fail — `panic: test timed out` names no row and takes the binary with it. The arm's loop is
// `for j := range dst`, whose bound is the slice's length rather than a counter the body
// decrements, so no single-token mutation of it can fail to terminate. That was checked by
// running the mutations, not deduced: which arrangement hangs is not readable off the source.
func TestBulkFillTruncatesTheValueToAByte(t *testing.T) {
	const mod = `(module (memory 1)
		(func (export "fill") (param i32 i32 i32)
			(memory.fill (local.get 0) (local.get 1) (local.get 2)))
		(func (export "load8_u") (param i32) (result i32)
			(i32.load8_u (local.get 0))))`

	in, trap := instantiate1(t, mod)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	load := func(addr int32) int64 {
		t.Helper()
		out, err := in.Invoke("load8_u", I32(addr))
		if err != nil {
			t.Fatalf("load8_u %d: %v", addr, err)
		}
		return int64(out[0].Bits)
	}

	// Basic fill, and the untouched neighbours (bulk.wast:27-32).
	if _, err := in.Invoke("fill", I32(1), I32(0xff), I32(3)); err != nil {
		t.Fatalf("fill: %v", err)
	}
	for addr, want := range map[int32]int64{0: 0x00, 1: 0xff, 2: 0xff, 3: 0xff, 4: 0x00} {
		if got := load(addr); got != want {
			t.Errorf("after fill(1, 0xff, 3): [%d] = %#02x, want %#02x", addr, got, want)
		}
	}

	// The value is stored as a byte (bulk.wast:34-37).
	if _, err := in.Invoke("fill", I32(0), I32(0xbbaa), I32(2)); err != nil {
		t.Fatalf("fill 0xbbaa: %v", err)
	}
	for _, addr := range []int32{0, 1} {
		if got := load(addr); got != 0xaa {
			t.Errorf("after fill(0, 0xbbaa, 2): [%d] = %#02x, want 0xaa — the fill value is "+
				"stored as a byte (bulk.wast:34)", addr, got)
		}
	}

	// Out-of-bounds fill writes nothing (bulk.wast:43-46). The trap is asserted elsewhere;
	// what this pins is the all-or-nothing property, which a per-byte fill without an
	// up-front check would break by writing the first 0x100 bytes before running off the end.
	if _, err := in.Invoke("fill", I32(0xff00), I32(1), I32(0x101)); err == nil {
		t.Fatal("fill(0xff00, 1, 0x101) succeeded, want a trap")
	}
	for _, addr := range []int32{0xff00, -1 /* 0xffff, below */} {
		if addr == -1 {
			addr = 0xffff
		}
		if got := load(addr); got != 0x00 {
			t.Errorf("after the trapping fill: [%#x] = %#02x, want 0 — a failed fill writes "+
				"nothing (bulk.wast:45-46)", addr, got)
		}
	}
}

// TestBulkTrapsOnAnExtentPastTheEnd is the extent half of `outOfBounds` at the largest addresses
// an i32 operand can name.
//
// **Named for what it asserts, after the name it was given first turned out to be false.** The
// original was `TestBulkTrapsOnAWrappedExtent`, on the reasoning that `oob i n j` is
// `lt_u (add i n) i || gt_u (add i n) j` and that these rows exercise the wrap arm. They do not:
// deleting `end < i` from `outOfBounds` leaves every row here green, measured. The operands are
// i32, zero-extended into 64-bit slots, so `i + n` maxes out near 2^33 and cannot wrap — the wrap
// arm is **unreachable through an i32-indexed memory or table** and becomes load-bearing only
// when the memory64 gate lands. A name promising the wrap arm while the rows exercise the extent
// arm is #34's defect exactly: a pass count that is right while the coverage is wrong.
//
// What the rows do cover is worth keeping. `0xffffffff` as an address with a nonzero length is
// the largest overrun i32 can express, and it is the shape where a 32-bit sum *anywhere* in the
// chain wraps to something small and admits the access — an accept-direction hole (§9 G-3), since
// every `memory_fill.wast` and `memory_copy.wast` vector uses addresses and lengths that stay far
// below the boundary. The wrap arm itself is left with no falsifying test and that is stated at
// `outOfBounds`, rather than papered over with a hand-built stack the decoder cannot produce.
func TestBulkTrapsOnAnExtentPastTheEnd(t *testing.T) {
	const mod = `(module (memory 1)
		(func (export "fill") (param i32 i32 i32)
			(memory.fill (local.get 0) (local.get 1) (local.get 2)))
		(func (export "copy") (param i32 i32 i32)
			(memory.copy (local.get 0) (local.get 1) (local.get 2))))`

	in, trap := instantiate1(t, mod)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	// -1 as an i32 is 0xffffffff, zero-extended to 4294967295. With a length of 2 the
	// 64-bit sum is 4294967297, far past one page and past 2^32 — the shape that wraps once
	// a 32-bit sum is used anywhere in the chain.
	cases := []struct {
		what string
		fn   string
		args []Value
	}{
		{"fill at 0xffffffff len 2", "fill", []Value{I32(-1), I32(0), I32(2)}},
		{"fill at 0xffffffff len 0xffffffff", "fill", []Value{I32(-1), I32(0), I32(-1)}},
		{"copy dst 0xffffffff len 2", "copy", []Value{I32(-1), I32(0), I32(2)}},
		{"copy src 0xffffffff len 2", "copy", []Value{I32(0), I32(-1), I32(2)}},
	}
	for _, c := range cases {
		_, err := in.Invoke(c.fn, c.args...)
		var tr *Trap
		if !errors.As(err, &tr) {
			t.Errorf("%s: got %v, want a trap — a 32-bit sum anywhere in the extent "+
				"computation wraps here and admits the access", c.what, err)
			continue
		}
		if !strings.Contains(tr.Error(), "out of bounds memory access") {
			t.Errorf("%s: trapped %q, want the memory out-of-bounds string", c.what, tr)
		}
	}
}

// TestBulkTableCopyTrapsWithTheTableString is the message, not the verdict.
//
// `table.copy`'s out-of-bounds trap is `out of bounds table access` (`eval.ml:24`'s
// `table_error`), a different string from memory's, and the harness matches `assert_trap`
// expectations by substring — so returning `trapOOB` here gives the right verdict with the wrong
// testimony and fails every `table_copy.wast` trap vector while passing every memory one. Cheap
// to get backwards, since the two traps are one identifier apart at the call site.
func TestBulkTableCopyTrapsWithTheTableString(t *testing.T) {
	const mod = `(module (table 10 funcref)
		(func (export "copy") (param i32 i32 i32)
			(table.copy (local.get 0) (local.get 1) (local.get 2))))`

	in, trap := instantiate1(t, mod)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	_, err := in.Invoke("copy", I32(0), I32(0), I32(11))
	var tr *Trap
	if !errors.As(err, &tr) {
		t.Fatalf("table.copy past the end: got %v, want a trap", err)
	}
	if !strings.Contains(tr.Error(), "out of bounds table access") {
		t.Errorf("trapped %q, want %q: a table overrun says *table*, and returning the memory "+
			"trap here is the right verdict with the wrong testimony",
			tr, "out of bounds table access")
	}
}

// A control for `tableAddr`'s zero-extension was written here and **deleted as stillborn**; the
// finding is recorded at `tableAddr`'s definition, which is where the next reader of that arm
// will be. In short: every i32 slot in this engine is already zero-extended by `pushI32`, so the
// narrowing is the identity on every input the front end can present, and the test passed with
// the narrowing removed — and passed with `outOfBounds`'s wrap arm removed at the same time,
// which is why it was not simply weak but empty. Calling the arm directly with a raw
// `0xffffffffffffffff` slot panics; nothing can put one there.
//
// Not replaced with a hand-built-`Instr` version that *would* fail: that test would assert the
// interpreter against a stack state the decoder cannot produce, which is grave #125's shape and
// buys a green for a defect that does not exist.

// TestUnhandledFCSubOpcodeStaysOnTheWorkList's subject dissolved rather than moved this time, so
// the re-pointing is structural rather than a row swap — a tripwire names a risk, not a code
// shape (the #33 ruling), and the risk survives even where every row that used to carry it is
// gone.
//
// The risk is that `execFC`'s `default` stops rendering an unhandled sub-opcode as `fc NN` and
// collapses the region into one bucket or, worse, into a bare `NN` that reads as a single-byte
// opcode. The board's fail buckets are keyed by that message and the work list is read off them,
// so a change that erased the partition would erase the schedule.
//
// **This PR is the one that drains the region, not just moves the row within it.** `fc 0b`
// (memory.fill) retired the first row this test held, and `fc 10` (table.size, filed alongside
// `fc 0f`/`fc 11`) retires the second — `opTableFC` has 18 entries and `execFC` now answers all
// 18, so there is no nineteenth unhandled sub-opcode to name.
//
// **`0xfd` was ruled out here on a premise that stopped being true four days later, and the
// conclusion does not survive it.** What stood here was: "`0xfd`'s region is not a replacement
// candidate: SIMD is declined at *decode* time when its gate is off (`gatemap.go:180`), so a v128
// module never reaches an interpreter switch at all on the default board, and the two regions are
// not the same risk." The conditional half is still exactly right. The clause about the default
// board was true when written (2026-08-07, #171) and false from `0e41f9d` (2026-08-11, #227/ADR
// 0025) onward, because SIMD flipped default-on and v128 modules have reached the interpreter ever
// since — so the region *is* a candidate, and it is one with the same shape rather than the same
// population. Measured, both halves: `binary.PrefixedOp` has **275** entries under `0xfd` and **19**
// of them still render `interp: no arm for opcode fd NN` (`fd 9a a2 a5 a6 af b0 b2 b3 b4 c2 c5 c6
// cf d0 d2 d3 d4 e2 ee`) — `execFD`'s header describing itself accurately, "unhandled sub-opcodes
// fall through to `unsupported`, rendering as `fd NN` — the board's existing bucket key" — and the
// board carries **0** rows in that bucket, because no corpus module reaches any of the 19. So the
// code population is live and the board population is empty, which is not a reason to look
// elsewhere: it is the position this test was *already* in when its own row dissolved, and the
// answer then was to move off the corpus and onto a direct call. `0xfd` supports the identical move
// with 19 subjects instead of one.
//
// **The re-pointing has since been made: `TestUnhandledFDSubOpcodesStayOnTheWorkList` in
// `simd_test.go`, #429.** It took the second of the two shapes that issue named — a sweep whose
// domain is derived from `binary.PrefixedOp` rather than 19 direct calls — so its population drains
// with the decoder's table instead of ageing beside it: a test naming `fd 9a` would have gone stale
// as SIMD families land, exactly the way the `fc 0b` row did, which is the staleness this very
// paragraph is a correction *of*. The count it reports today is #429's measured population
// unchanged, 275 rows with 19 unanswered. What follows is left in the past
// tense it was written in, because the shape is the lesson: nobody re-read a tripwire's
// rationale while flipping a gate, and nothing asked them to. The foreclosing-word sweep in
// `internal/testenv` is what asked, one PR after being written for a different instance of the same
// defect, and this is the instance that makes its case — the other three were wrong when written,
// and a careful reader could in principle have caught them. This one was *right* when written and
// was falsified by a commit in another package four days later. **Grave #428**, and the correction is
// recorded here rather than in the tracker alone because this is the paragraph a reader arrives at.
//
// **So the row moves off the corpus and onto a direct call**, which is the only way left to
// present `execFC` with a sub-opcode it does not have: the decoder itself rejects anything
// outside `opTableFC`'s 18 entries as malformed (`prefixRegion`, `instr.go:148`), so no module
// this engine accepts can carry one. `0x12` is one past the table's last entry and is
// unreachable from any accepted module — the same "cannot happen through the front door, still
// worth asserting at the back door" shape `TestElemExprIndexReachesTheRef` uses for the
// element-expression evaluator.
func TestUnhandledFCSubOpcodeStaysOnTheWorkList(t *testing.T) {
	err := (&Instance{}).execFC(binary.Instr{Prefix: 0xfc, Op: 0x12}, &stack{})
	if !errors.Is(err, ErrUnsupportedOp) {
		t.Fatalf("fc 12: got %v, want ErrUnsupportedOp", err)
	}
	if got := err.Error(); !strings.Contains(got, "fc 12") {
		t.Errorf("message is %q, want it to name `fc 12`: the board's buckets are keyed by this "+
			"string, and `12` alone would read as `return_call`", got)
	}
}
