// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package binary

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// limitsImage wraps a limits flags byte in the smallest module that reaches decodeLimits by the
// named route, so one table of flag expectations can be run against every production that reads
// the byte.
//
// Four routes and not one, because *the byte is read by four productions and each does something
// different with it*: the memory section keeps the shared bit, the table section refuses it, and
// the import section has one descriptor of each. #511's fix touched three of the four, and a
// fixture built for the memory section alone would have scored the memory rule and left the
// table's refusal — the half with no corpus witness — untested from either side.
func limitsImage(route string, flags byte) []byte {
	hdr := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	// min 1, and a max of 1 only when the flags say one follows: a max byte the flags did not
	// announce is trailing data, which would fail these modules for a reason that is not the
	// flags. Derived from bit 0 rather than from a per-row field for the same reason the decoder
	// now is — one place knows what bit 0 means.
	lim := []byte{flags, 0x01}
	if flags&flagsHasMax != 0 {
		lim = append(lim, 0x01)
	}
	sec := func(id byte, body ...byte) []byte {
		return append(append(hdr, id, byte(len(body))), body...)
	}
	switch route {
	case "memory section":
		return sec(0x05, append([]byte{0x01}, lim...)...)
	case "table section":
		return sec(0x04, append([]byte{0x01, 0x70}, lim...)...) // 0x70 = funcref
	case "imported memory":
		// one import: module name "", field name "", kind 0x02 (memory), then the limits.
		return sec(0x02, append([]byte{0x01, 0x00, 0x00, 0x02}, lim...)...)
	case "imported table":
		return sec(0x02, append([]byte{0x01, 0x00, 0x00, 0x01, 0x70}, lim...)...)
	}
	panic("unknown route " + route)
}

// flagsRow is one limits flags value and everything the two pins say about it.
//
// **The expectations are transcribed, never computed from the bits.** A row whose `wantShared`
// came from `flags&0x02 != 0` would be checking `decodeLimits` against its own expression, which
// agrees with itself however wrong it is — the shape *asserting a shape can still check a thing
// against itself* names. The literals below are read off the two `limits` productions: the core
// pin's `flags land 0xfa` mask with `at` from bit 2 (`decode.ml:279-286`) and the threads pin's
// `flags land 0xfc` with `shared` from bit 1 (`spec-threads/binary/decode.ml:181-188`).
type flagsRow struct {
	flags byte
	// needs is the gates the value requires, in the order the decoder must report them missing.
	// A row needing two gates is a value neither pin authorizes on its own, which is the whole
	// finding of the 0x06/0x07 pair — see TestLimitsFlagsAreReadBitByBit's comment.
	needs []string
	// The accepted Limits, for a module declaring min 1 (and max 1 where bit 0 is set).
	wantHasMax, wantShared, wantAddr64 bool
}

// flagsRows is every legal flags value. Values above the mask are covered separately, over the
// whole remaining byte space rather than by sampled rows.
var flagsRows = []flagsRow{
	{flags: 0x00},
	{flags: 0x01, wantHasMax: true},
	{flags: 0x02, needs: []string{"threads"}, wantShared: true},
	{flags: 0x03, needs: []string{"threads"}, wantHasMax: true, wantShared: true},
	{flags: 0x04, needs: []string{"memory64"}, wantAddr64: true},
	{flags: 0x05, needs: []string{"memory64"}, wantHasMax: true, wantAddr64: true},
	{flags: 0x06, needs: []string{"threads", "memory64"}, wantShared: true, wantAddr64: true},
	{flags: 0x07, needs: []string{"threads", "memory64"}, wantHasMax: true, wantShared: true, wantAddr64: true},
}

// TestLimitsFlagsAreReadBitByBit is grave #511's control, and it is a matrix rather than a case
// list because the defect was *a value the switch matched and a bit the arm forgot*.
//
// # The two halves of the grave, and why the second one needed a probe to find
//
// The reported half: with the threads gate on, `(memory 1 1 shared)` — flags `0x03` — decoded
// byte-identically to `(memory 1 1)`. `HasMax` came off bit 0 and bit 1 went nowhere, so the
// struct downstream could not tell a shared memory from an ordinary one, and every §§2-5
// consequence of sharing was a consequence of a bit the decoder discarded.
//
// The half nothing had reported, found by printing the whole matrix instead of the reported cell:
// **flags `0x06` and `0x07` were accepted with the threads gate off.** They set bit 1, the old
// `0x04..0x07` arm consulted `Memory64` alone, and the shared bit was admitted ungated and then
// dropped — so a module declaring a shared memory64 decoded as a plain memory64 with no gate
// having authorized anything. *A FAIL names a site, not the population*: the ordered fix was the
// `0x02, 0x03` arm, and the same discarded bit was live one arm down.
//
// Neither pin authorizes `0x06`: the core masks `land 0xfa` (bit 1 banned) and the threads pin
// masks `land 0xfc` (bit 2 banned), each because it predates the other's proposal. So the value
// needs *both* gates, which is what the `needs` column says and what nothing before this checked.
//
// # Every gate combination, because a gate is only checked when both its answers are
//
// Four combinations over two gates, so each row is asked both what it accepts and what it
// declines. The declining direction is where the leak lived, and a table listing only the
// all-on outcomes would have passed on the buggy decoder.
func TestLimitsFlagsAreReadBitByBit(t *testing.T) {
	combos := []struct {
		name string
		on   map[string]bool
		d    *Decoder
	}{
		{"all off", nil, &Decoder{}},
		{"threads", map[string]bool{"threads": true}, &Decoder{Features: Features{Threads: true}}},
		{"memory64", map[string]bool{"memory64": true}, &Decoder{Features: Features{Memory64: true}}},
		{
			"both",
			map[string]bool{"threads": true, "memory64": true},
			&Decoder{Features: Features{Threads: true, Memory64: true}},
		},
	}
	routes := []string{"memory section", "table section", "imported memory", "imported table"}

	for _, row := range flagsRows {
		for _, combo := range combos {
			// The first gate this value needs that this combination does not have. Order is the
			// row's, not a scan of the decoder's branches: two missing gates have two possible
			// testimonies and the row says which one is right.
			missing := ""
			for _, g := range row.needs {
				if !combo.on[g] {
					missing = g
					break
				}
			}
			for _, route := range routes {
				t.Run(fmt.Sprintf("%#02x/%s/%s", row.flags, combo.name, route), func(t *testing.T) {
					m, err := combo.d.DecodeModule(limitsImage(route, row.flags))

					if missing != "" {
						if !errors.Is(err, ErrFeatureDisabled) {
							t.Fatalf("flags %#02x with %s on: got %v, want the %s gate to decline.\n\t"+
								"A flags bit accepted without its proposal's gate is the grammar "+
								"widened by a gate that is off — #511's second half.",
								row.flags, combo.name, err, missing)
						}
						if !strings.Contains(err.Error(), missing) {
							t.Errorf("flags %#02x with %s on: %q does not name %q, so the reader "+
								"cannot tell which gate to flip", row.flags, combo.name, err, missing)
						}
						return
					}

					// A shared table is malformed whatever the gates say, so the two table routes
					// part company from the memory ones here rather than in a separate test — the
					// point being that one flags value has two verdicts by production.
					if row.wantShared && strings.Contains(route, "table") {
						if !errors.Is(err, ErrSharedTable) {
							t.Fatalf("flags %#02x on a %s: got %v, want ErrSharedTable.\n\t"+
								"`table_type` requires `not shared` (`spec-threads/binary/decode.ml:190-194`), "+
								"and **no vector in the corpus scores this in either direction** — "+
								"the reference is the only oracle it has.", row.flags, route, err)
						}
						return
					}

					if err != nil {
						t.Fatalf("flags %#02x on a %s with %s on: %v, want accept",
							row.flags, route, combo.name, err)
					}
					got := routeLimits(t, m, route)
					if got.HasMax != row.wantHasMax || got.Shared != row.wantShared ||
						got.Addr64 != row.wantAddr64 {
						t.Errorf("flags %#02x on a %s: HasMax=%v Shared=%v Addr64=%v, want %v/%v/%v.\n\t"+
							"Each bit is a separate consequence and the byte is gone afterwards, so a "+
							"bit read as false here is unrecoverable rather than merely late.",
							row.flags, route, got.HasMax, got.Shared, got.Addr64,
							row.wantHasMax, row.wantShared, row.wantAddr64)
					}
					if got.Min != 1 || (row.wantHasMax && got.Max != 1) {
						t.Errorf("flags %#02x on a %s: Min=%d Max=%d, want 1 and 1 — the flags byte "+
							"decides how many LEBs follow, so a misread bit 0 shifts the fields",
							row.flags, route, got.Min, got.Max)
					}
				})
			}
		}
	}

	// Every value the mask forbids, over the whole remaining byte space rather than a sample —
	// *derive the domain, never enumerate it*. All gates on, because a gate decline would also be
	// a rejection and would let a malformed value pass this check wearing the wrong verdict.
	on := &Decoder{Features: Features{Threads: true, Memory64: true}}
	for f := 0x08; f <= 0xff; f++ {
		_, err := on.DecodeModule(limitsImage("memory section", byte(f)))
		if !errors.Is(err, ErrMalformedLimits) {
			t.Errorf("flags %#02x with every gate on: got %v, want ErrMalformedLimits.\n\t"+
				"Both pins mask the high bits (`land 0xfa`, `land 0xfc`), so a value above the "+
				"union of the three known bits is malformed and not a feature nobody gated.", f, err)
		}
	}
}

// routeLimits pulls the Limits back out of whichever section the route wrote them to.
//
// It reads the *decoded module* rather than calling decodeLimits directly, and that is the point
// of the whole fixture: a control that called the reader would prove the reader works while the
// four callers each kept their own copy of the mistake — *a control can test the helper, not the
// path*.
func routeLimits(tb testing.TB, m *Module, route string) Limits {
	tb.Helper()
	switch route {
	case "memory section":
		if len(m.Memories) != 1 {
			tb.Fatalf("%s: %d memories, want 1", route, len(m.Memories))
		}
		return m.Memories[0].Limits
	case "table section":
		if len(m.Tables) != 1 {
			tb.Fatalf("%s: %d tables, want 1", route, len(m.Tables))
		}
		return m.Tables[0].Type().Limits
	case "imported memory":
		if len(m.Imports) != 1 {
			tb.Fatalf("%s: %d imports, want 1", route, len(m.Imports))
		}
		return m.Imports[0].Memory.Limits
	case "imported table":
		if len(m.Imports) != 1 {
			tb.Fatalf("%s: %d imports, want 1", route, len(m.Imports))
		}
		return m.Imports[0].Table.Limits
	}
	tb.Fatalf("unknown route %q", route)
	return Limits{}
}

// TestSharedFlagsClausesAreTheThreadsReferences machine-checks the three literals #511's fix
// transcribed, against the pin they came from.
//
// ADR 0007's requirement, in the amendment's own terms: the shared clauses are hand-written here
// because no generator covers `limits`, so what makes them *not* hand-trusted is a check against
// the reference. Contract §9 G-3 is why it cannot be the board — the table refusal has no vector
// in either direction, and the two masks are `assert_malformed` territory where a wrongly-accepted
// byte no vector uses is invisible.
//
// Three things, because they are three separate ways to be wrong: the mask (which bytes are legal
// at all), the bit each meaning lives in, and the message a refusal emits.
func TestSharedFlagsClausesAreTheThreadsReferences(t *testing.T) {
	dec := testenv.RequireSpecRef(t, filepath.Join("..", "..", testenv.ThreadsRefDecodeML))

	// The mask. The threads pin bans bit 2 and the core pin bans bit 1, so neither literal is
	// this engine's `flagsAll` — what is checkable is that every bit *this* engine accepts is a
	// bit one of the two pins names, which is asserted by the two clause checks below plus the
	// core pin's own mask, already pinned by the memory64 slice. What must not drift is the
	// threads mask's *width*: a pin whose `limits` stopped masking at all would make the
	// malformed sweep above an assertion about nothing.
	if !strings.Contains(dec, "flags land 0xfc = 0") {
		t.Errorf("%s no longer masks limits flags with `land 0xfc` — the clause the shared bit is "+
			"carved out of has moved, so every citation in decodeLimits names a line that is gone",
			testenv.ThreadsRefDecodeML)
	}

	// The bit. `flagsShared` is 0x02 because the reference says `flags land 2`, and the two other
	// bits are checked in the same shape so that a transposition — shared reading bit 2 — cannot
	// pass by agreeing with a copy of itself.
	for _, tc := range []struct {
		clause string
		bit    int
	}{
		{"has_max = (flags land 1 = 1)", flagsHasMax},
		{"shared = (flags land 2 = 2)", flagsShared},
	} {
		if !strings.Contains(dec, tc.clause) {
			t.Errorf("%s does not contain %q; decodeLimits reads that bit as %#02x on its authority",
				testenv.ThreadsRefDecodeML, tc.clause, tc.bit)
		}
	}

	// The message, verbatim including the parenthetical. `ErrSharedTable`'s text is the one thing
	// in this fix a future vector would match on, and it is the one thing no current vector does.
	if !strings.Contains(dec, `"`+ErrSharedTable.Error()+`"`) {
		t.Errorf("%s does not contain the literal %q that ErrSharedTable copies.\n\tThe refusal is "+
			"unwitnessed by the corpus, so this comparison is its only oracle — a reworded message "+
			"here would score a future vector wrong with nothing else to notice.",
			testenv.ThreadsRefDecodeML, ErrSharedTable.Error())
	}
	// And the requirement it guards, so the message is checked as *that rule's* message rather
	// than as a string that happens to be in the file.
	if !strings.Contains(dec, "require (not shared)") {
		t.Errorf("%s no longer requires `not shared` in `table_type`; the refusal decodeTableType "+
			"performs is a rule this pin does not have", testenv.ThreadsRefDecodeML)
	}
}
