// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// Witnesses for lowering a `string` argument through the guest's own `cabi_realloc` (#902, ADR 0098).

func loadStringArgFixture(t *testing.T) *Instantiated {
	t.Helper()
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/string-arg-realloc-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(in.Close)
	return in
}

// TestAStringArgumentIsAllocatedByTheGuestAndWrittenIntoGrownMemory is the working path, and it is also
// the chair's growth witness.
//
// The fixture's `cabi_realloc` **grows the memory and returns a pointer into the page it just added**, so
// a write that had captured the memory's base or length before calling realloc would be describing an
// image that no longer exists. The guest sums the bytes it was handed, so the assertion is on the
// **content at the right address** rather than on the call merely completing.
func TestAStringArgumentIsAllocatedByTheGuestAndWrittenIntoGrownMemory(t *testing.T) {
	in := loadStringArgFixture(t)

	const arg = "abc" // 97 + 98 + 99
	want := uint32(0)
	for _, b := range []byte(arg) {
		want += uint32(b)
	}

	res, err := in.CallValues("echo", canon.Str(arg))
	if err != nil {
		t.Fatalf("CallValues(echo, %q): %v", arg, err)
	}
	if len(res) != 1 {
		t.Fatalf("echo returned %d value(s), want 1", len(res))
	}
	got, ok := res[0].U32()
	if !ok {
		t.Fatalf("echo returned a %s, want a u32", res[0].Type.Kind)
	}
	if got != want {
		t.Fatalf("echo summed the bytes to %d, want %d. The guest read %d byte(s) from the address its "+
			"own realloc returned — a mismatch means the bytes landed somewhere else, which is what a "+
			"write against a pre-growth image would do", got, want, len(arg))
	}

	// **What this test does NOT witness, stated rather than implied.** It shows the write lands at the
	// address the guest's realloc returned, in a page that existed only after the growth — so a base or
	// length captured before the realloc would fail here.
	//
	// It does **not** witness ADR 0073's locking half, and **no test in the tree does.** Searched on the
	// #915 review: of every `internal/interp` test that both spawns a goroutine and calls `.Write(`, one
	// exists and it never grows; and ADR 0073's own relocating-grow test represents the sibling agent as
	// a host call parked in the guest, ignoring its `Caller`, so it covers the world-count refusal rather
	// than the `RLock`. The `RLock` is there for the **retained-`Caller`** case, which no world count
	// sees — and that case is unwitnessed. Filed as #916; the gap predates this slice.

	// The empty string is the case most likely to be special-cased wrongly: it allocates zero bytes and
	// writes nothing, and must still arrive as an empty string rather than as a refusal.
	res, err = in.CallValues("echo", canon.Str(""))
	if err != nil {
		t.Fatalf("CallValues(echo, \"\"): %v", err)
	}
	if got, ok := res[0].U32(); !ok || got != 0 {
		t.Fatalf("echo(\"\") summed to %d (ok=%v), want 0", got, ok)
	}
}

// TestTheHostDoesNotTouchALoweredArgumentAfterTheCall is the ownership witness.
//
// The model's rule: a lowered argument belongs to the **callee** once the call is made. `lower_flat_values`
// has no matching free and there is no "unlower" anywhere in the model, so the host allocates through the
// guest's realloc and never releases, re-reads or zeroes it.
//
// The fixture's realloc returns a predictable address — the start of the page it grew into, 65536 for the
// first allocation — and `peek` reports the byte there. A host that freed or scrubbed the allocation would
// show up here and in no other test.
func TestTheHostDoesNotTouchALoweredArgumentAfterTheCall(t *testing.T) {
	in := loadStringArgFixture(t)

	if _, err := in.CallValues("echo", canon.Str("abc")); err != nil {
		t.Fatalf("CallValues(echo): %v", err)
	}
	res, err := in.CallValues("peek")
	if err != nil {
		t.Fatalf("CallValues(peek): %v", err)
	}
	got, ok := res[0].U32()
	if !ok {
		t.Fatalf("peek returned a %s, want a u32", res[0].Type.Kind)
	}
	if got != 'a' {
		t.Fatalf("the byte at the first allocation is %#x, want %#x ('a'). The host wrote the argument "+
			"there and must not have touched it since — the callee owns it once the call is made, and "+
			"freeing or scrubbing it would be the host reclaiming memory the guest still owns", got, 'a')
	}
}

// TestAReallocThatTrapsIsReportedAsTheGuestsRefusal covers a guest that declines the allocation.
//
// The `echo-trap` export has the same signature and a realloc whose body is `unreachable`. The host must
// surface that refusal, and in particular must **not** proceed to write at whatever the failed call left
// behind — there is no address to write to.
func TestAReallocThatTrapsIsReportedAsTheGuestsRefusal(t *testing.T) {
	in := loadStringArgFixture(t)

	_, err := in.CallValues("echo-trap", canon.Str("abc"))
	if err == nil {
		t.Fatal("a trapping realloc produced no error; the guest refused the allocation, so there is no " +
			"address the argument could have been written to")
	}
	// The message must say it was the realloc and which argument, because the alternative — a bare trap —
	// leaves an embedder unable to tell a refused allocation from a fault in the export's own body.
	for _, want := range []string{"realloc", "\"s\""} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}

// TestALiftWithNoReallocRefusesAStringArgumentByName covers the canonopt being absent rather than failing.
//
// Asserted on the lowering directly: a lift declaring a `string` parameter and no `(realloc)` is refused
// by `wasm-tools` at validation, so **no fixture can carry one** — the engine must still not dereference a
// zero `coreDef`, and this is where that is checked. *A negative claim buys a branch an exemption only if
// something checks the exemption.*
func TestALiftWithNoReallocRefusesAStringArgumentByName(t *testing.T) {
	// A compFunc with neither canonopt, which is what a lift that declared none leaves behind.
	f := &compFunc{}
	p := allocRequest{param: "s", kind: canon.KindString, byteLen: 3, align: 1}

	_, err := f.guestAlloc(p)
	if err == nil {
		t.Fatal("a lift with no realloc allocated something; there is nowhere to put the bytes")
	}
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Errorf("the refusal is not ErrUnsupportedForm: %v", err)
	}
	for _, want := range []string{"realloc", "\"s\""} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}

	// And with a realloc but no memory, which is the other half of the same absence and a different
	// message: there is somewhere to allocate and nowhere to write.
	in := loadStringArgFixture(t)
	cd := in.export.exports["echo"]
	if cd.fn == nil {
		t.Fatal("the fixture exports no echo")
	}
	noMem := &compFunc{reallocCore: cd.fn.reallocCore}
	if _, err := noMem.guestAlloc(p); err == nil {
		t.Fatal("a lift with no memory allocated something; there is nowhere to write the bytes")
	} else if !strings.Contains(err.Error(), "memory") {
		t.Errorf("the no-memory refusal %q does not mention memory", err)
	}
}

// TestALoweredPayloadPastTheStringCapIsRefusedBeforeTheRealloc is the size check, and the point is the
// **order**: the guest must never be asked for an allocation this engine would refuse to use.
//
// # It is stricter than the model, on purpose
//
// The model's **store** side only asserts `dst_byte_length <= REALLOC_I32_MAX` (2³²−1,
// definitions.py:1597). The 2²⁸−1 cap is the **load** side's trap (def:1383). So this refuses a string
// the model would store — deliberately, because such a string could never be read back as one: any
// `load_string` of it traps. It is unusable in both directions, and asking a guest to grow by a quarter
// of a gigabyte to satisfy a request about to be rejected is worse than declining first.
//
// **No 256 MiB string is allocated**, because the check is factored to take a length. That is why it is
// a separate function rather than three lines inside `guestAlloc`.
func TestALoweredPayloadPastTheStringCapIsRefusedBeforeTheRealloc(t *testing.T) {
	if err := checkLowerSize("s", canon.KindString, canon.MaxStringByteLength); err != nil {
		t.Fatalf("a string payload exactly at the cap was refused: %v", err)
	}
	for _, n := range []int{canon.MaxStringByteLength + 1, 1 << 30, -1} {
		err := checkLowerSize("s", canon.KindString, n)
		if err == nil {
			t.Fatalf("a string payload of %d bytes was accepted; the cap is %d", n, canon.MaxStringByteLength)
		}
		if !errors.Is(err, ErrUnsupportedForm) {
			t.Errorf("%d: the refusal is not ErrUnsupportedForm: %v", n, err)
		}
		if !strings.Contains(err.Error(), "\"s\"") {
			t.Errorf("%d: the refusal %q does not name the argument", n, err)
		}
	}

	// **The cap is the STRING's, and must not be applied to a list** (the #924 review). A `list<u32>` of
	// 100 million elements is 400 MB; the model allows it, nothing stops a guest reading it back, and a
	// single string-shaped cap refused it with a message about strings.
	//
	// No 400 MB is allocated here either — that is what taking a length buys.
	const fourHundredMB = 100_000_000 * 4
	if fourHundredMB <= canon.MaxStringByteLength {
		t.Fatalf("this test's premise is gone: %d is meant to exceed the string cap of %d, so the two "+
			"arms below no longer differ", fourHundredMB, canon.MaxStringByteLength)
	}
	if err := checkLowerSize("xs", canon.KindList, fourHundredMB); err != nil {
		t.Errorf("a %d-byte list was refused: %v — that is the string cap applied to a kind it does not "+
			"govern; a list is bounded by REALLOC_I32_MAX, which this is well inside", fourHundredMB, err)
	}
	// And a list IS still bounded — by the ABI's pointer space, not by the string rule.
	//
	// **`math.MaxInt` rather than `ReallocI32Max + 1`**, and the reason is the one `ReallocI32Max`'s own
	// doc comment records: `int(canon.ReallocI32Max)+1` is a *constant* conversion, so it fails to
	// compile on a 32-bit host whether or not the branch runs. `math.MaxInt` is the width-dependent
	// bound, which also makes the arm's unreachability on a 32-bit host explicit instead of accidental:
	// no `int` there can exceed REALLOC_I32_MAX, so there is nothing to refuse.
	if uint64(math.MaxInt) > canon.ReallocI32Max {
		if err := checkLowerSize("xs", canon.KindList, math.MaxInt); err == nil {
			t.Error("a list past REALLOC_I32_MAX was accepted; the ABI's 32-bit pointer space cannot " +
				"address it")
		}
	} else {
		t.Logf("int is %d bits here, so no length can pass REALLOC_I32_MAX (%d) and the upper bound is "+
			"unreachable on this platform", strconv.IntSize, canon.ReallocI32Max)
	}
	// The string cap still applies to a string of the same size, which is what says the two arms are
	// distinguished by kind rather than by the number.
	if err := checkLowerSize("s", canon.KindString, fourHundredMB); err == nil {
		t.Error("a 400 MB string was accepted; the load side traps past 2^28-1, so it could never be " +
			"read back")
	}

	// And the order: a `compFunc` with NO realloc still refuses on **size** for an over-cap payload,
	// which is only possible if the size check runs first. If the realloc were consulted first, this
	// would complain about the missing canonopt instead.
	// `checkLowerSize` above asserts the cap itself. What is left to check is that `guestAlloc` **calls**
	// it — and the honest way to do that without allocating 256 MiB is the complement: a payload small
	// enough to pass the size check, against a `compFunc` with no realloc, must then fail on the realloc.
	// If the two checks were in the other order this would still name the realloc, so this is a weaker
	// assertion than the ordering comment above and is labelled as such rather than oversold.
	f := &compFunc{}
	small := allocRequest{param: "s", kind: canon.KindString, byteLen: 3, align: 1}
	if _, err := f.guestAlloc(small); err == nil {
		t.Fatal("guestAlloc with no realloc accepted a payload")
	} else if !strings.Contains(err.Error(), "realloc") {
		t.Errorf("with a small payload and no realloc the refusal should name the realloc, got %q", err)
	}
}

// TestAMisalignedReallocResultIsRefused covers definitions.py:1599's trap with a pointer the ABI can
// actually produce.
//
// # Why the fixture grew a third realloc
//
// The first version of this test demanded alignment **65537** of a page-aligned pointer: that shows the
// engine's check *runs*, and nothing about whether it catches a case a guest could hand it. The chair
// named the realistic offence on the #915 review — **a multiple of 2 but not of 4**, which is what a
// guest allocator with a 2-byte bump produces and what a `list<u32>` at alignment 4 refuses.
//
// So `realloc-misaligned` returns page start **+ 2**, and `echo-misaligned` is a lift naming it. The
// alignment-4 demand stands in for a `list<u32>` argument until the list lowering lands; the pointer is
// the guest's own.
func TestAMisalignedReallocResultIsRefused(t *testing.T) {
	in := loadStringArgFixture(t)
	aligned := in.export.exports["echo"]
	misaligned := in.export.exports["echo-misaligned"]
	if aligned.fn == nil || misaligned.fn == nil {
		t.Fatal("the fixture must export both echo and echo-misaligned")
	}

	// The page-aligned realloc satisfies every alignment, including a u32's. That is the control arm:
	// without it, a refusal below could be about the demand rather than about the pointer.
	ok := &compFunc{mem: aligned.fn.mem, reallocCore: aligned.fn.reallocCore}
	for _, align := range []int{1, 2, 4, 8} {
		if _, err := ok.guestAlloc(allocRequest{param: "s", kind: canon.KindString, byteLen: 3, align: align}); err != nil {
			t.Fatalf("the page-aligned realloc was refused at alignment %d: %v", align, err)
		}
	}

	bad := &compFunc{mem: misaligned.fn.mem, reallocCore: misaligned.fn.reallocCore}

	// **Alignment 1 and 2 are satisfied by page+2**, which is what makes this the realistic case rather
	// than an impossible address: a `string` crosses through this very realloc, and a `u16` would too.
	for _, align := range []int{1, 2} {
		if _, err := bad.guestAlloc(allocRequest{param: "s", kind: canon.KindString, byteLen: 3, align: align}); err != nil {
			t.Errorf("page+2 was refused at alignment %d, which it satisfies: %v", align, err)
		}
	}

	// **Alignment 4 is not** — a `list<u32>`'s demand, and definitions.py:1599's trap.
	_, err := bad.guestAlloc(allocRequest{param: "xs", kind: canon.KindList, byteLen: 4, align: 4})
	if err == nil {
		t.Fatal("a pointer at page+2 was accepted for an alignment-4 payload; that is a list<u32>'s " +
			"alignment and the model traps on it (definitions.py:1599)")
	}
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Errorf("the refusal is not ErrUnsupportedForm: %v", err)
	}
	for _, want := range []string{"aligned", "\"xs\""} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}

// TestAListArgumentIsLoweredThroughTheCodecsFraming is the list argument path end to end.
//
// # What a list adds over a string
//
// Three things, and each is why the string arm could not have stood in:
//
//   - **Alignment 4 rather than 1.** A `list<u32>`'s elements are 4 bytes at a 4-byte stride, so the
//     realloc's pointer must be a multiple of 4 — the first argument for which the misalignment trap is
//     not vacuous.
//   - **A stride**, which the guest reads through. A lowering that packed the elements at the wrong pitch
//     yields a wrong **sum**, not merely a wrong length — so the assertion is on content, as with the
//     string's checksum.
//   - **The framing is the codec's.** The elements go in through `canon.StoreListIntoRange` with
//     `StoreVia` as the injected element store, which is the same framing the differential verifies and
//     the same one `list<string>` and later `list<record>` will reuse.
func TestAListArgumentIsLoweredThroughTheCodecsFraming(t *testing.T) {
	in := loadStringArgFixture(t)
	u32 := canon.Type{Kind: canon.KindU32}

	for _, c := range []struct {
		name  string
		elems []uint32
	}{
		{"three elements", []uint32{1, 2, 3}},
		{"one element", []uint32{42}},
		// An empty list still allocates (the model's `allocate` is unconditional) and writes nothing. It
		// is the case most likely to be special-cased wrongly in either direction.
		{"empty", nil},
		// Values with the high bit set, so a lowering that sign-extended or truncated shows up.
		{"high bits", []uint32{0xFFFFFFFF, 0x80000000}},
	} {
		t.Run(c.name, func(t *testing.T) {
			vals := make([]canon.Value, 0, len(c.elems))
			var want uint32
			for _, e := range c.elems {
				vals = append(vals, canon.U32(e))
				want += e
			}
			lst, err := canon.List(u32, vals...)
			if err != nil {
				t.Fatalf("building the list value: %v", err)
			}
			res, err := in.CallValues("sum-list", lst)
			if err != nil {
				t.Fatalf("CallValues(sum-list, %v): %v", c.elems, err)
			}
			got, ok := res[0].U32()
			if !ok {
				t.Fatalf("sum-list returned a %s, want a u32", res[0].Type.Kind)
			}
			if got != want {
				t.Fatalf("sum-list summed %v to %d, want %d — the guest read %d element(s) at a 4-byte "+
					"stride from the address its own realloc returned, so a mismatch means the elements "+
					"were written at the wrong pitch or the wrong place", c.elems, got, want, len(c.elems))
			}
		})
	}
}

// TestAParameterTypeIsComparedStructurallyNotByKind closes a correctness hole the chair found on the
// #924 review.
//
// # The hole
//
// The parameter check compared `args[i].Type.Kind` against the declared kind. `Kind` says `list` for
// both `list<u32>` and `list<string>`, so a `list<string>` passed to `sum-list` **got through** — and the
// lowering then wrote strings at the *value's* element stride into a buffer the guest reads as 4-byte
// integers. A plausible wrong value, not an error.
//
// It is ADR 0097's first change one layer down, and it had to be fixed **here** rather than only at the
// public constructor: `Instantiated.CallValues` is reachable without going through `ComponentList`.
func TestAParameterTypeIsComparedStructurallyNotByKind(t *testing.T) {
	in := loadStringArgFixture(t)

	strs, err := canon.List(canon.Type{Kind: canon.KindString}, canon.Str("a"), canon.Str("bb"))
	if err != nil {
		t.Fatalf("building the list<string> value: %v", err)
	}

	_, err = in.CallValues("sum-list", strs)
	if err == nil {
		t.Fatal("a list<string> was accepted for a list<u32> parameter. Both are lists, so a kind " +
			"comparison lets it through; the elements then go in at the string stride and the guest " +
			"reads them as u32s — a plausible wrong value, which is the outcome this check exists to stop")
	}
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Errorf("the refusal is not ErrUnsupportedForm: %v", err)
	}
	// **The message must distinguish the two types**, not print "list" twice: a refusal naming both sides
	// identically reads as though the engine had refused a type for matching.
	for _, want := range []string{"list<u32>", "list<string>", "\"xs\""} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}

	// The declared type still crosses, so the refusal is about the element type and not about lists.
	nums, err := canon.List(canon.Type{Kind: canon.KindU32}, canon.U32(1), canon.U32(2))
	if err != nil {
		t.Fatalf("building the list<u32> value: %v", err)
	}
	if _, werr := in.CallValues("sum-list", nums); werr != nil {
		t.Fatalf("a list<u32> was refused by the structural check: %v", werr)
	}

	// And the scalar arm is covered by the same comparison: a `u32` value for a `string` parameter is
	// refused by type rather than by an accessor's boolean further down.
	if _, serr := in.CallValues("echo", canon.U32(7)); serr == nil {
		t.Error("a u32 was accepted for a string parameter")
	}
}

// TestAListArgumentThroughAMisalignedReallocIsRefused is the misalignment trap firing through a **real
// parameter**, which is what the hand-built `pendingLower` in `TestAMisalignedReallocResultIsRefused`
// stood in for until the list argument path existed.
//
// `sum-list-misaligned` has the same signature as `sum-list` and names the realloc returning page+2. A
// `list<u32>` demands alignment 4; page+2 is a multiple of 2 and not of 4.
func TestAListArgumentThroughAMisalignedReallocIsRefused(t *testing.T) {
	in := loadStringArgFixture(t)
	lst, err := canon.List(canon.Type{Kind: canon.KindU32}, canon.U32(1), canon.U32(2))
	if err != nil {
		t.Fatalf("building the list value: %v", err)
	}

	_, err = in.CallValues("sum-list-misaligned", lst)
	if err == nil {
		t.Fatal("a list<u32> was lowered through a realloc returning page+2; its elements require " +
			"alignment 4 and the model traps on a misaligned pointer (definitions.py:1715)")
	}
	if !strings.Contains(err.Error(), "aligned") {
		t.Errorf("the refusal %q does not name the alignment", err)
	}
	// And the same list through the page-aligned realloc works, so the refusal is about the pointer and
	// not about lists.
	if _, werr := in.CallValues("sum-list", lst); werr != nil {
		t.Fatalf("the same list through the aligned realloc was refused: %v", werr)
	}
}

// TestAStringStillCrossesAMisalignedRealloc is the other half of the pair above, end to end: a `string`
// argument lowered through the realloc that returns page+2 **succeeds**, because a string's alignment is
// 1.
//
// It is what says the alignment check is **alignment-sensitive rather than address-sensitive**. A check
// that refused page+2 outright would pass the test above and break this one.
func TestAStringStillCrossesAMisalignedRealloc(t *testing.T) {
	in := loadStringArgFixture(t)

	const arg = "abc"
	var want uint32
	for _, b := range []byte(arg) {
		want += uint32(b)
	}
	res, err := in.CallValues("echo-misaligned", canon.Str(arg))
	if err != nil {
		t.Fatalf("CallValues(echo-misaligned, %q): %v", arg, err)
	}
	got, ok := res[0].U32()
	if !ok {
		t.Fatalf("echo-misaligned returned a %s, want a u32", res[0].Type.Kind)
	}
	if got != want {
		t.Fatalf("echo-misaligned summed to %d, want %d — the bytes were written at the misaligned "+
			"pointer the guest returned, and the guest read from the same place", got, want)
	}
}

// TestASyncLiftRefusesAStringArgument pins the one structural refusal the entry discipline forces.
//
// The lowering must happen inside the callee's task, on the entry the callee runs on — that is the
// model's ordering and the reason the allocation cannot be made in `CallValuesCtx`. A **sync** lift has
// no task and no entry slot, so there is nowhere to do it, and the refusal says so rather than lowering
// outside the entry and hoping no sibling calls in.
func TestASyncLiftRefusesAStringArgument(t *testing.T) {
	f := &compFunc{async: false}
	// `context.Background()` rather than nil: the refusal happens before the context is read, which is
	// the property being relied on, but a nil Context is a thing to pass nowhere on principle.
	_, err := f.invokeWithPending(context.Background(), nil,
		[]pendingLower{{param: "s", val: canon.Str("x")}})
	if err == nil {
		t.Fatal("a sync lift accepted a pending argument lowering")
	}
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Errorf("the refusal is not ErrUnsupportedForm: %v", err)
	}
	for _, want := range []string{"sync lift", "task"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}
