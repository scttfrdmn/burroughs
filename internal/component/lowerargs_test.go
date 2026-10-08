// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"errors"
	"io"
	"os"
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
	p := pendingLower{param: "s", bytes: []byte("abc"), align: 1}

	_, err := f.guestAlloc(&p)
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
	if _, err := noMem.guestAlloc(&p); err == nil {
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
	if err := checkLowerSize("s", canon.MaxStringByteLength); err != nil {
		t.Fatalf("a payload exactly at the cap was refused: %v", err)
	}
	for _, n := range []int{canon.MaxStringByteLength + 1, 1 << 30, -1} {
		err := checkLowerSize("s", n)
		if err == nil {
			t.Fatalf("a payload of %d bytes was accepted; the cap is %d", n, canon.MaxStringByteLength)
		}
		if !errors.Is(err, ErrUnsupportedForm) {
			t.Errorf("%d: the refusal is not ErrUnsupportedForm: %v", n, err)
		}
		if !strings.Contains(err.Error(), "\"s\"") {
			t.Errorf("%d: the refusal %q does not name the argument", n, err)
		}
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
	small := pendingLower{param: "s", bytes: []byte("abc"), align: 1}
	if _, err := f.guestAlloc(&small); err == nil {
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
		if _, err := ok.guestAlloc(&pendingLower{param: "s", bytes: []byte("abc"), align: align}); err != nil {
			t.Fatalf("the page-aligned realloc was refused at alignment %d: %v", align, err)
		}
	}

	bad := &compFunc{mem: misaligned.fn.mem, reallocCore: misaligned.fn.reallocCore}

	// **Alignment 1 and 2 are satisfied by page+2**, which is what makes this the realistic case rather
	// than an impossible address: a `string` crosses through this very realloc, and a `u16` would too.
	for _, align := range []int{1, 2} {
		if _, err := bad.guestAlloc(&pendingLower{param: "s", bytes: []byte("abc"), align: align}); err != nil {
			t.Errorf("page+2 was refused at alignment %d, which it satisfies: %v", align, err)
		}
	}

	// **Alignment 4 is not** — a `list<u32>`'s demand, and definitions.py:1599's trap.
	_, err := bad.guestAlloc(&pendingLower{param: "xs", bytes: []byte("abcd"), align: 4})
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
		[]pendingLower{{param: "s", bytes: []byte("x"), align: 1}})
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
