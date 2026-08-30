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
//
// **And the memory is unshared, which is load-bearing — the sentence above used to say the
// memory was not asserted about, and a shared one was asserted about by every gate-off test in
// this file (grave #531).** Flags `0x03` sets the shared bit, which is itself gated on threads
// (`limits_flags_test.go`), so a gate-off decode of an image carrying one fails in the *memory
// section* and never reaches the code section at all. Both gate-off tests below were reading
// that decline and scoring it as the 0xfe region's: TestAtomicRegionIsGatedOnThreads passed with
// the region's gate-map range shrunk to exclude both opcodes it tests, and passed again with
// `prefixed`'s entire `gateCheck` call commented out. Unshared here means the decline they read
// is the one they name.
func atomicImage(sub []byte, rest ...byte) []byte {
	body := append([]byte{0x00, 0xFE}, sub...) // no locals, then the prefix and sub-opcode
	body = append(body, rest...)
	body = append(body, 0x0B) // end (body)

	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
		0x03, 0x02, 0x01, 0x00, // function: one func, type 0
		0x05, 0x04, 0x01, 0x01, 0x01, 0x01, // memory: one, unshared, min 1 max 1
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
//
// # Grave: this control was stillborn, and its own repair note was the tell (#531)
//
// It passed with the 0xfe entry's gate-map range shrunk to `0x01..0x02` — excluding both opcodes
// it decodes — and passed again with `prefixed`'s whole `c.gateCheck(prefix, sub)` call commented
// out. It was reading `atomicImage`'s **shared** memory declining one section earlier, so every
// assertion below was about `decodeLimits` and none about the region.
//
// The message assertion is what made that survivable, and its history is the specimen. It first
// demanded the string "atomics", passed, and was then "corrected" to demand "threads" — with a
// comment saying *"the test was wrong, not the message"*. Both halves of that were false. The
// region decline renders `the 0xfe region (atomics): feature gate disabled` and never contained
// "threads" at all; the original assertion had been right, the correction was confirmed by the
// memory section's `threads: feature gate disabled`, and the note recording it is a repair
// verified by the mechanism that made the mess.
//
// So the string is no longer typed here. It is read out of the gate map, which is the authority
// the message is built from — an identity check on the *dispatch* (does prefix 0xfe sub N key the
// row this test means?) and deliberately not a check on the row's content, which
// `gatecensus_test.go` pins and the citation sweep resolves. The negative arm is what a
// self-agreeing check cannot supply: the message must not be some other region's `what`.
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
		// The construct's name, read from the row this opcode keys. `featureErr` renders
		// `<what>: feature gate disabled`, and for an opcode-mapped gate `what` is the
		// construct — "throw", "the 0xfd region (v128)", "the 0xfe region (atomics)". The gate's
		// own name appears only in the non-opcode declines (`gatedNonOpcodes`), which is the
		// convention this file follows rather than an omission to be fixed here.
		g, ok := gateFor(0xFE, uint32(tc.sub))
		if !ok {
			t.Errorf("fe %#02x keys no gate-map row at all: the region entry covers "+
				"0x00..0xffffffff, so a miss here is the dispatch this test is about being "+
				"absent rather than its message being wrong", tc.sub)
			continue
		}
		if g.gate != gateThreads {
			t.Errorf("fe %#02x keys the %v gate, not gateThreads: this test's subject is the "+
				"threads region, so a different gate means the row moved under it", tc.sub, g.gate)
			continue
		}
		if !strings.Contains(err.Error(), g.what) {
			t.Errorf("fe %#02x declined with %v, which does not name %q — the construct is what "+
				"tells a user which feature they met", tc.sub, err, g.what)
		}
		// And not a *different* region's construct, which is the arm an assertion read off the
		// same table cannot supply: a dispatch keying 0xfd's row for an 0xfe opcode would satisfy
		// every check above if `what` were compared to whatever the dispatch happened to find.
		for _, other := range gatedOpcodes {
			if other.what == g.what || !strings.Contains(err.Error(), other.what) {
				continue
			}
			t.Errorf("fe %#02x declined with %v, which names %q — a second region's construct in "+
				"an 0xfe decline is the dispatch reading the wrong row", tc.sub, err, other.what)
		}
	}
}

// TestAtomicFenceRejectsANonZeroFlagWithTheGateOff is the sentence grave #531 falsified, moved
// out of a comment and into a place where it can fail.
//
// A comment on `ErrZeroFlagExpected`'s enrollment in `declaredErrors` said the verdict "is
// reachable only with the Threads gate on" and that "the fuzzer's default-gate corpus cannot
// produce it at all". Both halves are false, and `FuzzConstExprProgress` produced `fe 03 30` at
// a zero-value Decoder inside one CI run to prove it. **A gate decline is recorded, not
// returned** — `gateCheck` calls `c.decline`, and malformed outranks a deferred decline in
// 0008's order (instr.go, on `declined`) — so a gated arm reads its immediates so the grammar
// can finish, and a malformed immediate is a malformed verdict at any gate setting.
//
// The pair is the point. One byte apart:
//
//	fe 03 00   gate off → ErrFeatureDisabled     (TestAtomicRegionIsGatedOnThreads)
//	fe 03 30   gate off → ErrZeroFlagExpected    (here)
//
// Neither verdict is the other's bug: the first module is well-formed and merely gated, the
// second is malformed whether or not anyone asked for atomics. The generalization is what makes
// this worth a test rather than a corrected comment — **every gated arm's malformed-immediate
// sentinel is reachable with its gate off**, so "gated, therefore unobservable" is not a reason
// to leave a sentinel out of a fuzz-facing registry, which is exactly the reason #531 was
// written down as one.
func TestAtomicFenceRejectsANonZeroFlagWithTheGateOff(t *testing.T) {
	off := &Decoder{}
	if off.Features.Threads {
		t.Fatal("the zero-value Decoder has Threads on: this test's whole subject is the " +
			"gate-off path, so a flipped default makes it a duplicate of its sibling rather " +
			"than a failure — re-point it at an explicitly-off Features")
	}

	const fence = 0x03
	// The fuzz find's own bytes, not a re-derivation of them: 0x30 is what the mutator
	// produced, and a control on a crasher that uses a different input is a control on a
	// different input.
	_, err := off.DecodeModule(atomicImage([]byte{fence}, 0x30))
	if err == nil {
		t.Fatal("fe 03 30 decoded with Threads off — it is malformed at every gate setting")
	}
	if !errors.Is(err, ErrZeroFlagExpected) {
		t.Errorf("fe 03 30 with Threads off refused with %v, want ErrZeroFlagExpected: a "+
			"feature decline here would mean the gate check now returns rather than defers, "+
			"which reorders malformed against declined for every gated arm in the table", err)
	}
	if errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("fe 03 30 reported %v, which claims both verdicts at once: the decline is "+
			"deferred and the malformed verdict supersedes it, so the gate's name must not "+
			"reach the caller", err)
	}
}
