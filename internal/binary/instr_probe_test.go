package binary

import (
	"errors"
	"runtime"
	"runtime/debug"
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

// TestDecodeCostIsProportionalToCompressedSize is grave #138's control, and it is a
// **resource** assertion because no verdict assertion can express the defect.
//
// # Why not a committed crasher
//
// The standing rule is that crashers are committed — `testdata/fuzz` is the graveyard's
// executable annex. This defect fails both of that rule's preconditions and the exception was
// ruled explicitly (Scott, PR #137): the input CI wrote does **not** reproduce as a
// single-input replay, because one worker holding 2.6 GiB completes fine and returns `ok`; only
// four concurrently exceeded the runner. Committed as a seed it would tax every `make check`
// 2.66 GiB of peak RSS (measured, `/usr/bin/time -l`) in exchange for asserting nothing. The
// refined rule: *a crasher is committed when its replay asserts the defect; when it cannot, the
// regression guard is a property assertion instead.*
//
// # The property
//
// A module declaring N locals is `O(image)` to decode, not `O(N)`. The old `decodeLocals`
// flattened into `make([]ValType, 0, total)`, so a five-byte LEB naming 0xFFFFFFFE bought 4.00
// GiB from a 30-byte image; the wire form's runs are now retained instead, and the sum check
// stayed exactly where it was because it was never the defect.
//
// Asserted as a **relation across rows** — allocation stays flat as N scales — never as a
// per-row byte budget. The paragraph that used to stand here recited that law correctly and the
// code beneath it did the opposite (`budget := len(img) * 64`, an absolute figure), which is
// grave #166: *the comment knew and the code didn't*, so review verified code against claims and
// the two concurred. Both of that budget's failure modes are the reason the shape changed:
//
//   - **It measured the process, not the decoder.** `TotalAlloc` is process-global, so a window
//     around one `DecodeModule` charges it for whatever else allocated meanwhile. Rows at 2^16
//     and 2^20 build the *same 28-byte image* and so shared an identical 1792-byte budget, with
//     ~890 bytes of real signal beneath it — about one stray 5 KB allocation from a false red,
//     and on #165's run the 2^16 row reported 6208 bytes while every larger row reported 888.
//   - **It never computed the comparison its own argument rested on.** Five rows each checked
//     against their own ceiling is five single-row measurements, and a single row cannot
//     distinguish "proportional to the image" from "proportional to N with a small constant" —
//     the reading that lets the defect back in. The *flatness* is the property.
//
// So the rows span 2^16 to 2^31 and the assertion is that the largest allocates no more than a
// small multiple of the smallest, across a 32767x sweep in N. Measured both ways rather than
// argued: flat, every row reads **888 bytes** — ratio 1.00, five rows, no spread at all. With the
// flattening `make([]ValType, 0, total)` reintroduced after the sum check the rows read 66424 /
// 1049464 / 16778104 / 268436344 / 2147484536, and the control fails at **32329.9x against a
// tolerance of 8** in 6.8s, naming both endpoints and both image lengths.
//
// That ratio corrects a figure this comment carried when it was written: the argued value was
// ~10^6, from ~2 GiB over ~900 bytes, and it is wrong because under the defect the *bottom* row
// inflates too — 65536 locals is already 66 KB, so the true separation is ~3x10^4. The
// conclusion is unchanged and the arithmetic was not: four orders of magnitude of margin over a
// tolerance of 8 is the same verdict as six. Recorded because the number was quoted before it was
// run, which is the habit this file exists to break, and because a corrected figure with its
// derivation is worth more than a deleted one.
//
// Budget by the quantity the purpose names (#28): the purpose is a relation, so is the budget.
func TestDecodeCostIsProportionalToCompressedSize(t *testing.T) {
	// A minimal module whose one function body declares `nlocals` locals of one type. Built
	// here rather than taken from the suite: no vector declares billions of locals *and* is
	// accepted, because the interesting count is one below a bound the suite only probes from
	// above (binary.wast:159 and :175 are the ≥2^32 rejections). Synthetic, and this is the
	// reason.
	build := func(nlocals uint32) []byte {
		body := append([]byte{0x01}, ulebBytes(nlocals)...) // one group, `nlocals` of them
		body = append(body, byte(I32), 0x0b)                // i32, then END

		code := append([]byte{0x01}, ulebBytes(uint32(len(body)))...)
		code = append(code, body...)

		img := []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00}
		for _, s := range []struct {
			id      byte
			payload []byte
		}{
			{1, []byte{0x01, 0x60, 0x00, 0x00}}, // type: one `(func)`
			{3, []byte{0x01, 0x00}},             // func: one function, type 0
			{10, code},
		} {
			img = append(img, s.id)
			img = append(img, ulebBytes(uint32(len(s.payload)))...)
			img = append(img, s.payload...)
		}
		return img
	}

	// One decode's allocation, in bytes, taken as the **minimum over repeats**. Three noise
	// defences, because the quantity is read off a process-global counter:
	//
	//   - the minimum, not the mean or a single reading: unrelated allocation inside the window
	//     can only ever push a sample *up*, so the floor is the closest estimate of the decode's
	//     own cost. This is what the old single reading got wrong — its one sample happened to be
	//     the contaminated one, and with nothing to compare against it had no way to know.
	//   - a discarded warm-up decode, so first-call lazy initialisation is not charged to row one.
	//     On #165's run the 2^16 row was the first to decode anything and read 7x every later row.
	//   - automatic GC off for the duration, so the collector cannot run background work inside a
	//     window; an *explicit* `runtime.GC()` still fires before each window, which is what keeps
	//     peak RSS to one decode's live set rather than the sum of nine. That second half is
	//     load-bearing under falsification rather than tidiness, and it was measured both ways
	//     (`/usr/bin/time -l`, defect reintroduced): **2.16 GiB peak with the explicit collect,
	//     8.68 GiB without** — one decode's live set against several, and the larger figure is
	//     past what a CI runner has. A control that gets OOM-killed names no row, which is the
	//     same "fail, never hang" failure the br_table loop row was rearranged for.
	//
	// `TotalAlloc` is cumulative and never decremented, so collecting between windows changes the
	// peak without touching the deltas. Repeats are cheap once the property holds: the retained
	// form makes every decode here ~900 bytes.
	allocFor := func(nlocals uint32) (alloc uint64, img []byte, m *Module, err error) {
		img = build(nlocals)

		if m, err = DecodeModule(img); err != nil { // warm-up, deliberately unmeasured
			return 0, img, m, err
		}

		defer debug.SetGCPercent(debug.SetGCPercent(-1))

		alloc = ^uint64(0)
		for range 8 {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			m, err = DecodeModule(img)
			runtime.ReadMemStats(&after)
			if err != nil {
				return 0, img, m, err
			}
			alloc = min(alloc, after.TotalAlloc-before.TotalAlloc)
		}
		return alloc, img, m, nil
	}

	// The tolerance: the widest row may allocate 8x the narrowest row's bytes. It is a *ratio
	// between rows*, so allocator noise and any future change to the decoder's constant factor
	// move both ends together and cancel; what cannot cancel is expansion, which separates them by
	// ~3x10^4 (measured, above). Eight is two orders of magnitude clear of the spread it must
	// tolerate — none, in fact: the five rows are identical at 888 bytes — and four orders clear of
	// the defect it must catch. The wide gap is the point, not slack.
	const flatnessTolerance = 8

	type row struct {
		nlocals uint32
		imgLen  int
		alloc   uint64
	}
	var rows []row

	for _, nlocals := range []uint32{1 << 16, 1 << 20, 1 << 24, 1 << 28, 1<<31 - 1} {
		alloc, img, m, err := allocFor(nlocals)
		// Accepting is half the assertion, and it is the half that keeps the fix honest.
		// Every one of these modules is **well-formed**: the sum is below 2^32, so the
		// reference decodes them all. Refusing one to save the memory would be the
		// accept-direction defect (§9 G-3) — strictly worse than the resource cost, and
		// invisible on a board whose vectors only probe the rejecting side of this bound.
		if err != nil {
			t.Errorf("%d locals: got %v, want accept — the sum is below 2^32, so this module "+
				"is well-formed and refusing it trades a resource bug for an accept-direction one",
				nlocals, err)
			continue
		}
		// And the groups are retained rather than expanded, which is the mechanism the
		// flatness above is a consequence of. Asserted separately so a failure says *which*
		// property broke: a ratio alone would report "too much memory" for what is
		// actually "the wire form stopped being retained".
		if got := len(m.Funcs[0].Locals); got != 1 {
			t.Errorf("%d locals: retained %d groups, want 1 — the image declares one run and "+
				"the decoder must not expand it", nlocals, got)
		}
		if got := m.Funcs[0].TotalLocals(); got != uint64(nlocals) {
			t.Errorf("%d locals: TotalLocals is %d — the flat count must survive the "+
				"compression, or consumers sizing frames from it are wrong by the ratio",
				nlocals, got)
		}

		rows = append(rows, row{nlocals, len(img), alloc})
		t.Logf("%10d locals: image %d bytes, allocated %d bytes", nlocals, len(img), alloc)
	}

	// A comparison against an empty set succeeds, so the relation gets a vacuity check: the
	// sweep is what makes the ratio mean anything, and a loop that `continue`d every row would
	// otherwise leave this test green having compared nothing.
	if len(rows) < 2 {
		t.Fatalf("measured %d rows, want all 5 — a flatness assertion needs at least two "+
			"points, and with fewer this test agrees with everything", len(rows))
	}

	// The extremes over *all* rows, not `rows[0]` and `rows[len(rows)-1]`. The doc comment above
	// promises "the largest allocates no more than a small multiple of the smallest", and a
	// first-to-last comparison would be a different, weaker claim wearing that sentence — which is
	// the defect this whole redesign exists to remove, so it does not get reintroduced one level
	// down. It also matters in fact: nothing guarantees allocation is monotone in N, and a middle
	// row ballooning while the last stayed flat is a perfectly good expansion bug that a
	// first-to-last check reads as flat.
	lo, hi := rows[0], rows[0]
	for _, r := range rows[1:] {
		if r.alloc < lo.alloc {
			lo = r
		}
		if r.alloc > hi.alloc {
			hi = r
		}
	}
	if lo.alloc == 0 {
		t.Fatalf("the %d-locals row allocated 0 bytes, which is not a decode — the measurement "+
			"is broken rather than the property satisfied", lo.nlocals)
	}
	// The relation, and the whole point of the redesign: the widest row is compared to the
	// *narrowest row*, not to a figure derived from its own image length.
	if hi.alloc > lo.alloc*flatnessTolerance {
		// The spread is between the extreme *allocations*, and the sweep is over the extreme
		// *declared counts* — two different pairs of rows, so they are reported as two separate
		// facts rather than divided into one number. Printing `hi.nlocals/lo.nlocals` as "the
		// sweep" would be arithmetic on whichever rows happened to be extreme in the other
		// quantity, which is a figure about nothing.
		t.Errorf("allocation is not flat as N scales: %d locals allocated %d bytes and %d locals "+
			"allocated %d bytes — a %.1fx spread, tolerance %dx, over a sweep from %d to %d "+
			"declared locals. That is proportional to the declared count rather than to the "+
			"image, which is grave #138: a 30-byte module bought 4.00 GiB. The two images are "+
			"%d and %d bytes, so the image cannot explain it.",
			lo.nlocals, lo.alloc, hi.nlocals, hi.alloc,
			float64(hi.alloc)/float64(lo.alloc), flatnessTolerance,
			rows[0].nlocals, rows[len(rows)-1].nlocals,
			lo.imgLen, hi.imgLen)
	}
}
