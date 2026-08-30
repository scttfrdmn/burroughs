// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package binary

import (
	"errors"
	"strings"
	"testing"
)

// atomicImage assembles a one-function module whose body is a single 0xfe instruction, with
// `sub` supplying the sub-opcode bytes verbatim and `rest` its immediates.
//
// The sub-opcode is `[]byte` rather than a `uint32` deliberately: the subject of the test below
// is *how many bytes this decoder reads for a sub-opcode and how it interprets them*, so a
// helper that encoded the number would decide the question under test. Assembled rather than
// encoded from wat for brOnCastImage's reason — a non-minimal LEB has no source spelling, so
// routing through the text front end would ask a sibling to produce a byte string it cannot.
//
// A memory section is included because every atomic access but the fence declares one, and its
// absence is a *validation* fact (`atomic.fence` "may occur in modules which declare no memory
// without causing a validation error" — spec-threads/proposals/threads/Overview.md:438). This
// file's subject is the decoder, so the memory is here to keep the image plausible rather than
// to be asserted about.
func atomicImage(sub []byte, rest ...byte) []byte {
	body := append([]byte{0x00, 0xFE}, sub...) // no locals, then the prefix and sub-opcode
	body = append(body, rest...)
	body = append(body, 0x0B) // end (body)

	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
		0x03, 0x02, 0x01, 0x00, // function: one func, type 0
		0x05, 0x04, 0x01, 0x03, 0x01, 0x01, // memory: one, shared, min 1 max 1
	}
	code := append([]byte{0x01, byte(len(body))}, body...)
	return append(img, append([]byte{0x0a, byte(len(code))}, code...)...)
}

// TestAtomicSubOpcodeIsReadAsALEB is the control on decision #524's one real choice, and it is
// aimed at a mistake this tree has already made rather than at a shape.
//
// # The choice
//
// The four core regions read their sub-opcode with `u32` — a LEB128. The threads pin's 0xfe
// region reads it with `op`, which is `let op s = byte s` (spec-threads/binary/decode.ml:219,
// :782): one raw byte. **Burroughs takes u32**, on Scott's ruling — *"the standard outranks the
// snapshot"*, the merged proposal reading a u32 like every other region, and taking `byte` for
// 0xfe alone making our own decoder inconsistent across five regions for no gain.
//
// # Why the choice is observable, which it was nearly recorded as not being
//
// It was tempting to call the ruling free, on the theory that Wasm requires canonical LEB
// encodings so no legal input could tell the two readings apart. **That premise is false**, and
// this tree's own corpus says so: `binary-leb128.wast`'s first line is *"Unsigned LEB128 can
// have non-minimal length"*, and this package's `uleb` mirrors the reference by checking the
// width budget and the value range but never minimality.
//
// `0x4e` is one byte in canonical form — that is a statement about the canonical width and
// nothing more, not a claim that `0x4e` is the only encoding of 78. `0xce 0x00` is the
// non-minimal two-byte encoding of the same value, and it is legal. So the two readings give
// measurably different answers on it: u32 decodes `i64.atomic.rmw32.cmpxchg_u`, byte reads
// `0xce` and reports no such opcode. A discriminating input, not a shape assertion.
//
// The mistake it is aimed at: 0007's first draft assumed the SIMD sub-opcode was a byte and
// undercounted that region by 19 arms. Same assumption, one region over.
func TestAtomicSubOpcodeIsReadAsALEB(t *testing.T) {
	d := &Decoder{Features: Features{Threads: true}}

	// The row the discriminating input must land on, read from the table rather than named
	// here: a hand-written mnemonic would be a second copy of the fact under test.
	const code = 0x4e
	want, ok := opTableFE[code]
	if !ok {
		t.Fatalf("opTableFE has no row at %#02x: the discriminating input was chosen for a "+
			"row that no longer exists, so this test asserts nothing", code)
	}
	if want.mnemonic != "i64_atomic_rmw32_u_cmpxchg" {
		t.Errorf("fe %#02x is %q, and this test was written when it was "+
			"i64_atomic_rmw32_u_cmpxchg — the region was renumbered upstream, so re-derive the "+
			"discriminating input before trusting the result below", code, want.mnemonic)
	}

	// memarg: align 2, offset 0. Present because the row declares immMemop, and read from the
	// row rather than assumed so a renumbering cannot leave this reading the wrong shape.
	if len(want.imms) != 1 || want.imms[0] != immMemop {
		t.Fatalf("fe %#02x declares imms %v, want exactly one immMemop", code, want.imms)
	}
	memarg := []byte{0x02, 0x00}

	// The canonical one-byte encoding, which both readings accept. Establishes that the row is
	// reachable at all, so a failure on the two-byte form below is about the *width* and not
	// about the region being unwired — the vacuity guard on the real assertion.
	if _, err := d.DecodeModule(atomicImage([]byte{code}, memarg...)); err != nil {
		t.Fatalf("fe %#02x with a canonical sub-opcode was rejected with %v — the region is not "+
			"reachable, so the non-minimal case below could not distinguish the two readings", code, err)
	}

	// The assertion: the non-minimal two-byte encoding of the same value.
	nonMinimal := []byte{0xce, 0x00}
	if _, err := d.DecodeModule(atomicImage(nonMinimal, memarg...)); err != nil {
		t.Errorf("fe ce 00 (a non-minimal LEB128 encoding of %#02x) was rejected with %v — "+
			"a byte reading sees sub-opcode 0xce and reports no such opcode, which is the "+
			"reading Scott's ruling on #524 declined. Wasm permits non-minimal LEB128 "+
			"(binary-leb128.wast:1), so this input is legal and the two readings disagree on it",
			code, err)
	}
}

// TestAtomicFenceRejectsANonZeroFlag is the control on the immediate whose whole content is a
// constraint, and it exists because no vector in the corpus does.
//
// `atomic.fence`'s only immediate is `expect 0x00 s "zero flag expected"`
// (spec-threads/binary/decode.ml:786), normatively `| 0xFE 0x03 0x00 => atomic.fence`
// (spec-threads/proposals/threads/Overview.md:598). A decoder reading it as a raw byte accepts
// `fe 03 01`, and **nothing on the board would say so**: `atomic.wast` has 0 assert_malformed
// rows at cc535ad — counted, not assumed — and its one `atomic.fence` mention is a positive row
// (:965). That is contract §9 G-3 with nothing left over, and the reason the constraint is a
// table fact (`immZeroByte`, distinct from `immByte`) rather than a decoder detail.
//
// Both directions, because the two candidate implementations differ in opposite ways: a raw-byte
// reader accepts what it should refuse, and a reader that consumed no byte at all would misread
// the following `end` as the flag and then run off the body.
func TestAtomicFenceRejectsANonZeroFlag(t *testing.T) {
	d := &Decoder{Features: Features{Threads: true}}

	const fence = 0x03
	if info, ok := opTableFE[fence]; !ok || info.mnemonic != "atomic_fence" {
		t.Fatalf("fe %#02x is %q (present=%v), want atomic_fence: the region was renumbered, so "+
			"the flag byte below belongs to a different instruction", fence, info.mnemonic, ok)
	}

	// The accept direction: the one legal value.
	if _, err := d.DecodeModule(atomicImage([]byte{fence}, 0x00)); err != nil {
		t.Fatalf("fe 03 00 was rejected with %v — 0x00 is the flag value the grammar spells out, "+
			"so this is the accept direction failing and the reject direction below cannot be "+
			"trusted to be about the constraint", err)
	}

	// The reject direction: every other value is malformed, not merely unusual.
	for _, flag := range []byte{0x01, 0x02, 0x7f, 0x80, 0xff} {
		_, err := d.DecodeModule(atomicImage([]byte{fence}, flag))
		if err == nil {
			t.Errorf("fe 03 %#02x accepted — `expect 0x00 s` refuses every value but zero, and "+
				"a reader that staged the byte without checking it would accept exactly this", flag)
			continue
		}
		if !errors.Is(err, ErrZeroFlagExpected) {
			t.Errorf("fe 03 %#02x refused with %v, want ErrZeroFlagExpected", flag, err)
			continue
		}
		// The reference's message text verbatim: this engine does not invent a wording for a
		// production the authority already names.
		if !strings.Contains(err.Error(), "zero flag expected") {
			t.Errorf("fe 03 %#02x message is %q, want the reference's text", flag, err)
		}
	}
}

// TestAtomicRegionIsGatedOnThreads asserts the region declines as a whole when the gate is off.
//
// Behaviour 4 of the brief: nothing defaults on without its own suite green, and `Threads` is
// off by default. The check is that the *decline* is a feature refusal rather than a malformed
// verdict — a gate that manufactured malformedness would score the wrong string on every vector
// in `atomic.wast`, and gates never manufacture malformedness (docs/laws/gates.md).
func TestAtomicRegionIsGatedOnThreads(t *testing.T) {
	off := &Decoder{}
	// A row with a memarg and the fence, so the gate is exercised on both immediate shapes.
	for _, tc := range []struct {
		sub  byte
		rest []byte
	}{
		{0x10, []byte{0x02, 0x00}}, // i32.atomic.load
		{0x03, []byte{0x00}},       // atomic.fence
	} {
		_, err := off.DecodeModule(atomicImage([]byte{tc.sub}, tc.rest...))
		if err == nil {
			t.Errorf("fe %#02x decoded with Threads off — the whole 0xfe region is gated "+
				"(gatemap.go, cite spec-threads/proposals/threads/Overview.md:594)", tc.sub)
			continue
		}
		if errors.Is(err, ErrZeroFlagExpected) {
			t.Errorf("fe %#02x with the gate off reported %v — a gated opcode's immediates are "+
				"read so the grammar can finish, but the verdict must be the feature refusal",
				tc.sub, err)
			continue
		}
		if !errors.Is(err, ErrFeatureDisabled) {
			t.Errorf("fe %#02x declined with %v, want ErrFeatureDisabled", tc.sub, err)
			continue
		}
		// The gate's name, not the construct's — `featureErr` names the gate and deliberately
		// does not resemble any assert_malformed string in the suite, because a gate-off engine
		// spoofing a spec string would score itself green for rejecting a well-formed module.
		// This assertion first demanded the *construct* ("atomics"), which is a property the
		// design explicitly refuses; the test was wrong, not the message.
		if !strings.Contains(err.Error(), "threads") {
			t.Errorf("fe %#02x declined with %v, which does not name the gate — the gate name is "+
				"what tells a user which flag to set", tc.sub, err)
		}
	}
}
