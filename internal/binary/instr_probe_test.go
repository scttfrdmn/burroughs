package binary

import (
	"errors"
	"strings"
	"testing"
)

// The controls the falsifiability probe found missing (#43/#39).
//
// # How this file came to exist
//
// The instruction and function-body grammars took binary.wast from 114/127 to 127/127
// and drained all 26 failing vectors across the phase-1 corpus. A wholesale green is the
// shape that deserves suspicion rather than celebration, so each mechanism the PR claims
// was **broken on purpose** and the board re-read: *a green that survives the bug it
// names is a control in name only*, and the way to know is to break it.
//
// Nine defects were introduced. Six were caught, and the catches were specific — a
// 32-bit locals sum refilled exactly `too many locals` (2 vectors), a byte-read locals
// count refilled `integer too large` *and* `too many locals` (4), an aborting const
// verdict refilled `unexpected end of section or function` (1), the invalid string on an
// absent opcode refilled both `illegal opcode` buckets (2), an emptied `dataRefOps`
// refilled `data count section required` (2), a byte-read sub-opcode refilled two
// binary-leb128 vectors, a u32 memop offset refilled five, an early flags bound check
// refilled six, and deleting `either`'s cursor reset refilled one.
//
// **Three survived**, and this file is the three. None of them is a bug in the decoder —
// each is a fact the decoder gets right that *nothing was asking about*, which is the
// more dangerous position: a correct implementation with no witness is one refactor away
// from being an incorrect one with no witness.
//
//	per-body extent   disabling the check entirely left the whole corpus green
//	laneidx width     re-introducing grave #47's raw byte read left it green
//	blocktype order   reversing the alternation's branches left it green
//
// Each survivor gets a control below, and each control was itself falsified by
// re-introducing the defect it names before being committed. That step earned its keep on
// the third one: the control caught the defect, but *not by the assertion it was written
// around*, and chasing the discrepancy showed my account of why the branch order matters
// was wrong. See TestBlockTypeAlternationIsTheAuthority — falsifying a control tells you
// whether it fires; reading *which* assertion fired tells you whether you understood it.

// TestFuncBodyExtentIsPerBody is the first survivor, and the one that also corrects a
// claim this PR made in prose.
//
// sections.go and decodeFuncBody both say binary.wast:92 is a `section size mismatch`
// about a *function body* rather than about a section, on the strength of `code_section s
// = section Custom.Code (vec (at (sized code))) [] s` (decode.ml:1140) — `sized` wraps
// each body. That reading is correct, and printing the decoder's output for :92's module
// confirms it verbatim:
//
//	section size mismatch: function body declared 4 bytes, grammar consumed 5
//
// But **deleting the per-body check leaves all 766 vectors green**, because the section
// level then reports the same sentinel for the same module. So the suite cannot tell the
// two layers apart, and the PR's claim about which layer answers :92 was — before this
// test — an unwitnessed one. That is precisely the #34 shape: the pass count is right and
// the coverage is not.
//
// The discriminating field is the message, not the identity, exactly as
// TestSectionSizeBothSigns had to discover for its two signs: when a partition's members
// share an error value, `errors.Is` is not a partition check.
func TestFuncBodyExtentIsPerBody(t *testing.T) {
	// cited binary.wast:96,98,99 — the byte lines of the vector opening at :92. A code
	// section whose single body declares 4 bytes and omits its END, so the grammar
	// consumes the following data section's `\0b` as one and reads 5.
	img := []byte("\x00asm\x01\x00\x00\x00")
	img = append(img, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00) // type: 1 functype
	img = append(img, 0x03, 0x02, 0x01, 0x00)             // function: 1 function
	img = append(img, 0x0A, 0x06, 0x01,                   // code: 6 bytes, 1 body
		0x04,       // body declares 4 bytes
		0x00,       // no locals
		0x41, 0x01, // i32.const 1
		0x1A) // drop — and no END
	img = append(img, 0x0B, 0x03, 0x01, 0x01, 0x00) // data section, whose \0b is eaten

	_, err := DecodeModule(img)
	if !errors.Is(err, ErrSectionSizeMismatch) {
		t.Fatalf("binary.wast:92: got %v, want ErrSectionSizeMismatch", err)
	}
	// The claim under test: the mismatch is reported about a function body, with the
	// body's own declared and consumed counts — 4 and 5, not the section's 6.
	if !contains(err.Error(), "function body declared 4 bytes, grammar consumed 5") {
		t.Errorf("message %q does not report the *body's* extent (declared 4, consumed 5)\n\t"+
			"`sized` wraps each body rather than the section (decode.ml:1140), so a mismatch "+
			"caught only at the section level would name the section's 6 bytes — the same "+
			"sentinel from the wrong layer, which the suite scores as a pass either way", err)
	}
	// And the sign, since the two directions share the sentinel. Consumed exceeds
	// declared here, which is the long sign the suite could not previously supply.
	if contains(err.Error(), "consumed 4") {
		t.Errorf("message %q has the operands backwards: the grammar over-read, so consumed "+
			"must exceed declared", err)
	}
}

// TestLaneIdxIsALEBInTheProductionReader is the second survivor, and it is grave #47
// reaching a second site.
//
// #47 was `immBytes` reading a lane index with `r.byte()` instead of `r.uleb(8)`, and its
// stated root cause was that no lane instruction is const-legal, so the extent
// differential never reached the entry. `TestEveryReaderAgreesWithItsAuthorityDefinition`
// was written to close that — for `immBytes`.
//
// The dissolution created a *second* place the same fact lives: instr.go's `imm` switch,
// which is the production path. Re-introducing #47's exact defect there leaves the whole
// corpus green, for the identical reason (`\fd` appears nowhere in the phase-1 corpus)
// and with the identical camouflage ("a lane index is small, so it must be a byte").
// A grave whose lesson was applied to one copy of a fact and not the other is a grave
// half-buried.
//
// derived from binary-leb128.wast:984 — the vector establishing that a multi-byte LEB in
// an *opcode-adjacent* field is read as a LEB rather than truncated to a byte (it is the
// `\fc` sub-opcode there). `laneidx s = u8 s = uN 8` (decode.ml:152,103) is the same rule
// at a different field, so a two-byte encoding of a small value is legal and consumes
// two. The suite has no lane vector to cite, which is the accept-direction blind spot
// 0007 exists for.
func TestLaneIdxIsALEBInTheProductionReader(t *testing.T) {
	c := &instrCtx{d: &Decoder{}, nonConst: -1}

	// `81 00` is 1 encoded in two bytes: legal for uN 8, and the discriminator between
	// a LEB reader and a raw byte read.
	r := &reader{b: []byte{0x81, 0x00}, eof: ErrPayloadEnd}
	if err := c.imm(r, immLaneIdx); err != nil {
		t.Fatalf("laneidx on 81 00: %v", err)
	}
	if r.off != 2 {
		t.Errorf("laneidx consumed %d bytes of a two-byte LEB, want 2 — grave #47's exact "+
			"defect, at its second site (the production reader rather than immBytes)", r.off)
	}

	// And the sixteen-element form, whose extent is 16..32 rather than a flat 16.
	wide := make([]byte, 0, 32)
	for range 16 {
		wide = append(wide, 0x81, 0x00)
	}
	r = &reader{b: wide, eof: ErrPayloadEnd}
	if err := c.imm(r, immLane16); err != nil {
		t.Fatalf("lane16 on sixteen two-byte LEBs: %v", err)
	}
	if r.off != 32 {
		t.Errorf("lane16 consumed %d bytes, want 32: `repeat 16 laneidx` (decode.ml:699) is "+
			"sixteen laneidx reads, not a flat bytes(16)", r.off)
	}

	// The canonical width still works, so the assertion above is about LEB-ness rather
	// than about a fixed two.
	r = &reader{b: []byte{0x03}, eof: ErrPayloadEnd}
	if err := c.imm(r, immLaneIdx); err != nil || r.off != 1 {
		t.Errorf("laneidx on a canonical one-byte 03: off=%d err=%v, want off=1 nil", r.off, err)
	}
}

// TestBlockTypeAlternationIsTheAuthority is the third survivor, and writing it
// falsified my own account of why the branch order matters. The corrected account is the
// interesting part, so it is recorded here rather than quietly fixed.
//
// # What I claimed, and how it was wrong
//
// decodeBlockType's comment called the order "load-bearing" because `typeuse s33` runs
// first, so a non-negative s33 is a type index and the valtype shorthands reach the later
// branches only by being negative at width 33. All of that is true. I then inferred that
// putting valtype first would make `00` "silently become a valtype read, and its extent
// and its error both change" — and wrote a six-row table to catch exactly that.
//
// It does not. **`either` backtracks** (`r.off = start` before every branch), so a branch
// that fails costs nothing and the next one sees the original cursor. The accept set and
// every extent are therefore *order-invariant*, and the table's six accept rows are
// order-blind by construction. Measured rather than reasoned: the reader was run over all
// 256 first bytes × three tails, 768 rows, in both orders. **427 rows differ, and they
// differ in exactly one way** — `malformed value type: 0x41` becomes `malformed value
// type`. Not one accept flipped; not one extent moved.
//
// So the six rows are a control on the branches being *present*, which is worth having and
// is not what the name promised. What is actually order-dependent is **which branch's
// error survives**, since `either` returns the last one's — and that has two consequences
// the suite cannot reach, both asserted below. The second is the one that matters.
//
// This is the *test named for a partition* failure (#34) caught in the act: the case
// labels described an order-sensitivity the cases could not exhibit, and only printing
// what the code returns for each input showed it. The rows are kept, renamed for the
// property they actually hold.
func TestBlockTypeAlternationIsTheAuthority(t *testing.T) {
	d := &Decoder{}
	// synthetic: the suite has no blocktype vector at all — no phase-1 vector reaches a
	// structural instruction's immediate — so every row here is a print-check against
	// decode.ml:334-339 and 160-164 rather than a citation.
	//
	// These rows witness branch *presence and extent*, not order. Deleting the typeuse
	// branch fails the first three; deleting the 0x40 branch fails the fourth; deleting
	// the valtype branch fails the fifth. Reversing the order fails none of them, which
	// is the finding above.
	for _, tc := range []struct {
		name string
		in   []byte
		off  int
		err  error
		why  string
	}{
		{
			"a non-negative s33 is a type index",
			[]byte{0x00},
			1, nil,
			"typeuse s33 accepts index 0; no valtype shorthand is 0x00, so only this " +
				"branch can take it",
		},
		{
			"index 1, likewise",
			[]byte{0x01},
			1, nil,
			"the discriminator is negativity at width 33, not the byte's value",
		},
		{
			"a two-byte index encoding consumes both",
			[]byte{0x81, 0x00},
			2, nil,
			"s33 is a LEB, so the branch that accepts consumes the whole encoding — a " +
				"reader that stopped at one byte would leave 0x00 to be read as the next opcode",
		},
		{
			"0x40 is the empty result type",
			[]byte{0x40},
			1, nil,
			"expect 0x40 is its own branch; 0x40 is negative at s33 so typeuse declines it",
		},
		{
			"a valtype shorthand is accepted",
			[]byte{0x7F},
			1, nil,
			"0x7f is -1 at width 33, so both other branches decline and valtype takes it",
		},
	} {
		r := &reader{b: tc.in, eof: ErrPayloadEnd}
		err := d.decodeBlockType(r)
		t.Logf("%-42s % x -> off=%d err=%v", tc.name, tc.in, r.off, err)

		switch {
		case tc.err == nil && err != nil:
			t.Errorf("%s: got %v, want accept\n\t%s", tc.name, err, tc.why)
			continue
		case tc.err != nil && !errors.Is(err, tc.err):
			t.Errorf("%s: got %v, want %v\n\t%s", tc.name, err, tc.err, tc.why)
			continue
		}
		if r.off != tc.off {
			t.Errorf("%s: consumed %d bytes, want %d\n\t%s\n\t"+
				"a wrong extent here does not fail loudly — it shifts every byte after the "+
				"blocktype into the wrong instruction", tc.name, r.off, tc.off, tc.why)
		}
	}

	// Order-dependent consequence 1: the surviving error is the valtype branch's, so the
	// message carries valtype's *detail*. This is the whole content of the 427-row diff.
	//
	// It is grave #36's class — an error message is testimony — with the twist that the
	// wrong order does not fabricate a byte, it *drops* one, leaving a bare sentinel that
	// still matches the harness's expected string. Invisible on the board by construction,
	// which is why it is pinned by content and not by identity.
	//
	// The sentinel is **reftype's**, and that is a fact about `valtype` rather than about
	// this alternation: since #88, `valtype` is itself `either [numtype; vectype; reftype]`
	// (decode.ml:220-225), so the error that reaches here has already survived two nested
	// last-branch selections. `\xff` at width 7 is an overlong LEB in every branch, which is
	// why the byte-naming assertion below uses `\x66` — a byte that reaches a form
	// fallthrough rather than the LEB budget. The first draft of this edit kept `\xff` and
	// asserted the byte was named, which fails for a reason that has nothing to do with
	// branch order.
	r := &reader{b: []byte{0xFF}, eof: ErrPayloadEnd}
	err := d.decodeBlockType(r)
	if !errors.Is(err, ErrLEBTooLong) {
		t.Errorf("0xff blocktype: got %v, want ErrLEBTooLong — 0xff sets the continuation bit, "+
			"so every branch reads past the byte and exhausts its width budget", err)
	}
	r = &reader{b: []byte{0x66}, eof: ErrPayloadEnd}
	err = d.decodeBlockType(r)
	if !errors.Is(err, ErrMalformedRefType) {
		t.Errorf("unknown blocktype byte: got %v, want ErrMalformedRefType — either returns the "+
			"last branch's error (decode.ml:126-131), valtype is last here, and reftype is last "+
			"*inside* valtype, so the surviving message is two nested selections deep", err)
	}
	if err != nil && !contains(err.Error(), "0x66") {
		t.Errorf("unknown blocktype byte: error %q does not name the byte; the innermost branch's "+
			"detail is lost, which happens when it is not the last branch", err)
	}

	// Order-dependent consequence 2, and the reason the order is genuinely load-bearing:
	// **a feature decline only survives if the gated branch runs last.**
	//
	// 0x7b is v128. With SIMD off, decodeValType declines it with ErrFeatureDisabled — and
	// with the reference's order that is the final branch's error, so it stands. Move
	// valtype anywhere earlier and the alternation overwrites it with whatever the last
	// branch says: a spec malformed-string for a construct Wasm 3.0 defines. That is precisely the thing gates may not do, and it is
	// a *configuration* fact, so no assert_malformed vector can ever catch it.
	//
	// This is the assertion that actually failed when the order was reversed. The five
	// rows above did not, and neither did all 766 suite vectors.
	r = &reader{b: []byte{0x7B}, eof: ErrPayloadEnd}
	err = d.decodeBlockType(r)
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("v128 blocktype with SIMD off: got %v, want ErrFeatureDisabled — the gated "+
			"branch's decline must be the alternation's surviving error", err)
	}
	if err != nil && strings.Contains(err.Error(), "malformed") {
		t.Errorf("v128 blocktype with SIMD off: error %q says malformed for a construct Wasm "+
			"3.0 defines — a gate reporting a spec malformed-string it has no right to", err)
	}
	simd := &Decoder{Features: Features{SIMD: true}}
	r = &reader{b: []byte{0x7B}, eof: ErrPayloadEnd}
	if err := simd.decodeBlockType(r); err != nil {
		t.Errorf("v128 blocktype with SIMD on: got %v, want accept", err)
	}
}
