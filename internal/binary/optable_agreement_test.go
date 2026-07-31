package binary

import (
	"errors"
	"testing"
)

// The agreement test decision 0006 pre-registered (#33), landing in the same PR as the
// table it cross-checks, per that issue's last done-when box.
//
// # Why this file exists before the interpreter does
//
// 0006 declined to share an opcode table between the decoder's constexpr reader and
// #7's interpreter, on the grounds that shaping the interpreter's central structure
// from the decoder's requirements — before a second consumer exists — is speculative
// design in the load-bearing spot. That decline accepts a debt: **two places know that
// 0x41 takes a signed LEB.** What makes accepting it honest is that the risk was
// pre-registered as a failing test rather than as an intention (*a design debt is
// discharged by a tripwire, never by an intention*).
//
// The generated table (0007) is the second place. It arrived before the interpreter,
// so the debt came due early and this is the payment.
//
// # Scoped to the space, not to the sample
//
// #33 is explicit that a cross-check over the eight opcodes 0006 needed would be the
// overfitting failure applied to a control: green while saying nothing about the opcode
// either side adds next. So every walk here is over **all 256 single-byte opcodes** and
// **every tracked prefix region**, derived from the table rather than enumerated, and
// the immediate vocabulary is covered by a totality assertion rather than by a list of
// the ones today's const set happens to use.
//
// # What is assertable now, and what is not
//
// #33 names three properties. Two are fully assertable against the generated table;
// the third is half-assertable, and the half that is missing is named rather than
// quietly dropped, because a control that silently covers less than its name claims is
// the #34 defect (a test checked against its own case labels instead of against the
// partition).
//
//   - **Same membership** (property 1) — assertable in the direction that matters. The
//     table records *existence and immediate shape*; it does not record const-legality,
//     which is a validation-layer fact the reference does not encode either (its
//     `const s` is the full instruction grammar, decode.ml:983). So the assertable
//     claim is one-directional and it is the direction the suite cannot catch: every
//     opcode the decoder accepts in a const expr must **exist** in the authority's
//     table and must not be one the authority marks `illegal`. A const reader
//     accepting a byte the reference rejects outright is a bug no assert_malformed
//     vector can see.
//   - **Same immediate extent** (property 2) — fully assertable, and it is the whole
//     point. Both sides are run over identical input and their consumed byte counts
//     compared. This is the fact that goes wrong quietly when duplicated.
//   - **Same rejection** (property 3) — the decoder half is assertable exhaustively.
//     The interpreter half ("the interpreter's table agrees it is absent from the const
//     subset") needs a const subset the interpreter publishes, which is #7's. Until
//     then this file pins the *partition the table does supply* — absent / illegal /
//     present-but-not-const — which is the classification decodeConstExpr's error
//     comment says it cannot currently make.

// immBytes maps each immediate in the *authority's* vocabulary to the reader this
// package would use for it.
//
// This is the seam, stated once. The generated table speaks decode.ml's vocabulary on
// purpose (see opInfo's doc comment) precisely so that the translation to this
// package's readers is a single reviewable place rather than a decision smeared across
// call sites — and a single place is a thing a test can walk.
//
// It is deliberately *not* derived from the table. The risk this whole file addresses is
// two places knowing one fact, and immBytes is a third place — if it were generated from
// the table, the extent comparison would be comparing the table against itself. It maps
// the authority's vocabulary to this package's readers, so the differential is between
// *sequences the table chose* and *sequences constExprOps chose*, which are independent.
//
// A nil reader means "no flat reader exists", which is a real category rather than a
// gap: the four structural arms (block, loop, if, try_table) recurse through the
// instruction grammar and cannot be expressed as a byte count. The const production
// contains none of them, and TestConstSetUsesNoStructuralImmediate is what keeps that
// from being an assumption.
var immBytes = map[imm]func(*Decoder, *reader) error{
	immS32:     func(_ *Decoder, r *reader) error { _, err := r.s32(); return err },
	immS64:     func(_ *Decoder, r *reader) error { _, err := r.s64(); return err },
	immF32:     func(_ *Decoder, r *reader) error { _, err := r.bytes(4); return err },
	immF64:     func(_ *Decoder, r *reader) error { _, err := r.bytes(8); return err },
	immV128:    func(_ *Decoder, r *reader) error { _, err := r.bytes(16); return err },
	immIdx:     func(_ *Decoder, r *reader) error { return discardIndex(r) },
	immU32:     func(_ *Decoder, r *reader) error { _, err := r.u32(); return err },
	immByte:    func(_ *Decoder, r *reader) error { _, err := r.byte(); return err },
	immLaneIdx: func(_ *Decoder, r *reader) error { _, err := r.byte(); return err },
	immLane16:  func(_ *Decoder, r *reader) error { _, err := r.bytes(16); return err },
	immHeapType: func(d *Decoder, r *reader) error {
		return d.decodeRefType(r)
	},

	// Present in the vocabulary, no flat reader, and each absence is a claim:
	//
	//   immMemop       two u32 LEBs (align, offset) — and a memory index when the
	//                  align field's bit 6 is set (decode.ml:283), so its width is
	//                  input-dependent and #7's business, not a fixed count.
	//   immValType     needs the gate-dependent valtype grammar.
	//   immVecValType  a vec of the above.
	//   immVecIdx      a vec of indices (br_table).
	//   immCatchVec    a vec of catch clauses (try_table).
	//   immBlockType   a blocktype: an s33 that is either a valtype or a type index.
	//   immBlock       structural — instr_block + end_, i.e. recursion.
	immMemop:      nil,
	immValType:    nil,
	immVecValType: nil,
	immVecIdx:     nil,
	immCatchVec:   nil,
	immBlockType:  nil,
	immBlock:      nil,
}

// prefixRegions is every region of the generated table, derived from the generator's
// own output rather than listed here.
//
// Keyed by the prefix byte, 0x00 meaning "no prefix". Walked so a region added upstream
// (a new proposal's prefix) cannot be silently uncovered — the totality test below
// fails until this map names it, which is a build-time question rather than a hole.
var prefixRegions = map[byte]map[uint32]opInfo{
	0x00: opTable,
	0xfb: opTableFB,
	0xfc: opTableFC,
	0xfd: opTableFD,
}

// TestEveryImmediateInTheTableHasABytesVerdict asserts totality of the seam.
//
// Scoped to the space per #33: not "the immediates the const set uses" but *every
// immediate the authority's table actually mentions*, so an upstream arm introducing a
// new reader fails here rather than being silently absent from immBytes. A map lookup
// returning the zero value is indistinguishable from a deliberate nil, which is why
// this checks key presence rather than nil-ness.
func TestEveryImmediateInTheTableHasABytesVerdict(t *testing.T) {
	seen := map[imm]bool{}
	for _, region := range prefixRegions {
		for _, info := range region {
			for _, im := range info.imms {
				seen[im] = true
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no immediates found in the generated table: the table is empty or the " +
			"regions map is wired to nothing, and an agreement test over an empty domain " +
			"agrees with everything (the vacuity class, #29)")
	}
	for im := range seen {
		if _, ok := immBytes[im]; !ok {
			t.Errorf("immediate %q appears in the generated table with no entry in immBytes\n\t"+
				"add a reader, or an explicit nil with the reason it has none: an absent key is a\n\t"+
				"gap wearing the same face as a deliberate decline", im)
		}
	}
	t.Logf("%d distinct immediates in the table, all accounted for", len(seen))
}

// TestConstSetUsesNoStructuralImmediate keeps immBytes' nil entries from being an
// assumption.
//
// The extent comparison below can only run for opcodes whose immediates all have flat
// readers. If a const-legal opcode ever acquires a structural immediate, the extent
// test would have nothing to compare and — written the obvious way — would skip that
// opcode and stay green. That is the failure this test exists to convert into a red:
// the *reason* the extent check is total is recorded as its own assertion.
func TestConstSetUsesNoStructuralImmediate(t *testing.T) {
	for b, op := range constExprOps {
		info, ok := opTable[uint32(b)]
		if !ok {
			continue // membership's problem, asserted separately
		}
		for _, im := range info.imms {
			if immBytes[im] == nil {
				t.Errorf("const-legal %#02x (%s) has structural immediate %q: the extent "+
					"comparison cannot cover it, so it must not silently be excluded from one",
					b, op.name, im)
			}
		}
	}
}

// TestConstSetIsASubsetOfTheAuthority is #33's property 1, in the direction the
// generated table can answer.
//
// The reverse direction — every const-legal opcode in the authority appears here — is
// not assertable, because the authority does not record const-legality (decode.ml
// checks it nowhere; it is a validation fact). Saying so is the point: this control
// covers one direction and names the other rather than implying both.
func TestConstSetIsASubsetOfTheAuthority(t *testing.T) {
	if len(constExprOps) == 0 {
		t.Fatal("constExprOps is empty: a subset check over an empty set passes vacuously")
	}
	for b, op := range constExprOps {
		info, ok := opTable[uint32(b)]
		if !ok {
			t.Errorf("const-legal %#02x (%s) does not exist in the reference's table "+
				"(decode.ml has no arm for it): the decoder accepts a byte the authority does "+
				"not define, which no assert_malformed vector can catch", b, op.name)
			continue
		}
		// END is the documented exception, and it is a layering fact rather than a
		// disagreement. decode.ml's `instr` rejects a bare 0x0b as "misplaced END
		// opcode" because at that position it is one; `instr_block'` stops on it as a
		// terminator (decode.ml:612). The const production ends with END, so this
		// reader is the instr_block caller, not the instr caller.
		if b == opEnd {
			if info.reason == "" {
				t.Errorf("expected the authority to record a reason for %#02x; the layering "+
					"exception below is written against `misplaced END opcode` and this row no "+
					"longer carries it", b)
			}
			continue
		}
		if info.illegal {
			t.Errorf("const-legal %#02x (%s) is marked illegal by the authority "+
				"(decode.ml:%d): the decoder accepts what the reference rejects outright",
				b, op.name, info.refLine)
		}
	}
}

// TestConstExprExtentMatchesTheAuthority is #33's property 2 — the duplicated fact.
//
// Differential rather than declarative: both sides read the *same bytes* and their
// cursors are compared. A test asserting "0x41 consumes 1 byte" would be a third copy
// of the fact under test, and three copies of a fact agreeing proves only that someone
// typed it three times.
//
// The input is sixteen 0x70 bytes, which is deliberate rather than arbitrary. 0x70 has
// its continuation bit clear, so it is a complete one-byte LEB in every width; it is a
// valid heaptype (funcref); and it is enough bytes for the widest fixed-size read
// (f64's eight, v128's sixteen). One input that every const-legal reader accepts is
// what lets the comparison be over consumed extent rather than over error behaviour.
func TestConstExprExtentMatchesTheAuthority(t *testing.T) {
	const input = "\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70"

	if len(constExprOps) == 0 {
		t.Fatal("constExprOps is empty: an extent comparison over no opcodes compares nothing")
	}
	d := &Decoder{}
	checked := 0
	for b, op := range constExprOps {
		info, ok := opTable[uint32(b)]
		if !ok {
			continue // membership's problem
		}

		mine := &reader{b: []byte(input)}
		if err := op.imm(d, mine); err != nil {
			t.Errorf("%#02x (%s): this package's reader failed on the shared input: %v", b, op.name, err)
			continue
		}

		theirs := &reader{b: []byte(input)}
		for _, im := range info.imms {
			read := immBytes[im]
			if read == nil {
				t.Fatalf("%#02x (%s): structural immediate %q reached the extent comparison; "+
					"TestConstSetUsesNoStructuralImmediate should have caught this first", b, op.name, im)
			}
			if err := read(d, theirs); err != nil {
				t.Errorf("%#02x (%s): the authority's shape %v failed on the shared input at %q: %v",
					b, op.name, info.imms, im, err)
				break
			}
		}

		if mine.off != theirs.off {
			t.Errorf("%#02x (%s): extent disagreement — this package consumed %d bytes, "+
				"the authority's shape %v (decode.ml:%d) consumed %d\n\t"+
				"a wrong extent does not fail loudly: it shifts every subsequent byte, and the "+
				"error surfaces somewhere else entirely (#33, property 2)",
				b, op.name, mine.off, info.imms, info.refLine, theirs.off)
		}
		checked++
	}
	if checked != len(constExprOps) {
		t.Errorf("compared %d of %d const-legal opcodes: an extent check that quietly skips "+
			"is a coverage claim it cannot support", checked, len(constExprOps))
	}
	t.Logf("%d const-legal opcodes agree on immediate extent", checked)
}

// TestEveryNonConstByteIsRejected is #33's property 3, the decoder half, walked over
// the whole single-byte space.
//
// Exhaustive by construction rather than by a list: all 256 bytes, partitioned by what
// the *authority* says about each, so the classification decodeConstExpr's comment says
// it cannot currently make is at least pinned as a measured partition. When the const
// check moves to the validator, this partition is the work list — the `present` bucket
// is exactly the set that should report "constant expression required" from a layer
// above, and the `absent`/`illegal` buckets are the ones that are genuinely malformed.
func TestEveryNonConstByteIsRejected(t *testing.T) {
	d := &Decoder{}
	var absent, escape, illegal, present int
	for b := range 256 {
		if _, isConst := constExprOps[byte(b)]; isConst {
			continue
		}

		// A one-byte image holding just this opcode. Rejection must come from the
		// opcode itself, so the input carries nothing that could truncate first.
		r := &reader{b: []byte{byte(b)}, eof: ErrPayloadEnd}
		err := d.decodeConstExpr(r)
		if err == nil {
			t.Errorf("%#02x is not const-legal but decodeConstExpr accepted it", b)
			continue
		}
		if !errors.Is(err, ErrNonConstantExpr) {
			t.Errorf("%#02x: want %v, got %v", b, ErrNonConstantExpr, err)
			continue
		}

		info, ok := opTable[uint32(b)]
		switch {
		case !ok:
			absent++
		case info.escape:
			escape++
		case info.illegal:
			illegal++
		default:
			present++
		}
	}

	// Stamped, not deduced. The numbers are a claim about the authority at the pinned
	// revision, and they are what makes this a partition check rather than a loop that
	// asserts "some error happened" 248 times — the #34 lesson: when a partition's
	// members share an error value, errors.Is is not a partition check.
	// Measured against the table at the pinned revision, then written down — the
	// figures here were first *reasoned* (40 illegal, 170 present) and both were wrong,
	// 40 being the illegal count across all four regions rather than the single-byte
	// one. Printed, not deduced.
	const (
		wantAbsent  = 38  // no arm in decode.ml: the catch-all's territory
		wantEscape  = 3   // 0xfb, 0xfc, 0xfd: dispatch to a sub-table
		wantIllegal = 21  // an arm that explicitly rejects
		wantPresent = 186 // a real instruction that is simply not constant
	)
	if absent != wantAbsent || escape != wantEscape || illegal != wantIllegal || present != wantPresent {
		t.Errorf("rejection partition changed: absent=%d escape=%d illegal=%d present=%d, "+
			"want %d/%d/%d/%d (sum %d must be 256 minus %d const-legal)\n\t"+
			"these buckets are the work list for moving const-legality to the validator: "+
			"`present` is the set that should report `constant expression required` from the "+
			"layer above, `escape` needs the sub-table walked, and `absent`+`illegal` are the "+
			"genuinely malformed ones",
			absent, escape, illegal, present, wantAbsent, wantEscape, wantIllegal, wantPresent,
			absent+escape+illegal+present, len(constExprOps))
	}
	if got := absent + escape + illegal + present + len(constExprOps); got != 256 {
		t.Errorf("partition does not cover the space: %d of 256 bytes classified", got)
	}
}

// TestPrefixRegionsCoverTheTable keeps prefixRegions from silently missing a region.
//
// The single-byte table's three prefix escapes are facts *in* the table: 0xfb, 0xfc and
// 0xfd have arms with no mnemonic and no immediates, because decode.ml handles them
// with a nested match rather than a flat arm. So the set of regions is derivable, and
// deriving it is what makes a fourth prefix landing upstream a failure here instead of
// an uncovered region (*derive the domain, never enumerate it*).
func TestPrefixRegionsCoverTheTable(t *testing.T) {
	for p, region := range prefixRegions {
		if p == 0x00 {
			continue
		}
		if len(region) == 0 {
			t.Errorf("region %#02x is empty: a walk over it asserts nothing", p)
		}
		info, ok := opTable[uint32(p)]
		if !ok {
			t.Errorf("prefix %#02x has a region but no arm at all in the single-byte table", p)
			continue
		}
		if !info.escape {
			t.Errorf("prefix %#02x has a region but its single-byte arm is not marked as an "+
				"escape (decode.ml:%d)", p, info.refLine)
		}
	}
	// And the reverse: a prefix escape with no region would be a dispatch the decoder
	// cannot follow. Keyed on the escape field rather than on the *absence* of every
	// other field — inferring "this must be an escape because it has nothing else" is
	// how the escapes went missing in the first place, and a fact worth checking is a
	// fact worth recording (Arm.Escape).
	escapes := 0
	for code, info := range opTable {
		if !info.escape {
			continue
		}
		escapes++
		if _, ok := prefixRegions[byte(code)]; !ok {
			t.Errorf("%#02x is a prefix escape (decode.ml:%d) but prefixRegions has no "+
				"table for it: a dispatch the decoder cannot follow", code, info.refLine)
		}
	}
	if escapes != len(prefixRegions)-1 { // -1 for the 0x00 "no prefix" entry
		t.Errorf("%d escapes in the table, %d sub-tables in prefixRegions: the two halves "+
			"of the dispatch disagree", escapes, len(prefixRegions)-1)
	}
}

// TestAgreementHoldsUnderEveryFeatureConfiguration is #33's Gates section.
//
// Gated const-expr additions (WasmGC's ref.i31, extended-const's arithmetic) will route
// through Features on both sides, and the agreement must hold *per configuration* — a
// cross-check run only with gates off would say nothing about the configuration a user
// actually enables. Nothing in the const set is gate-dependent today except ref.null's
// heaptype, so this currently proves invariance rather than covering variation; it is
// here so that the coverage arrives with the feature instead of being remembered.
//
// The configurations are derived by reflection over Features in featureset_test.go's
// helper if one exists; here the four booleans are walked as a bitmask, which is the
// same derivation without a dependency.
func TestAgreementHoldsUnderEveryFeatureConfiguration(t *testing.T) {
	const input = "\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70"

	configs := 0
	for mask := range 16 {
		f := Features{
			ExceptionHandling: mask&1 != 0,
			SIMD:              mask&2 != 0,
			Threads:           mask&4 != 0,
			Memory64:          mask&8 != 0,
		}
		d := &Decoder{Features: f}
		for b, op := range constExprOps {
			info, ok := opTable[uint32(b)]
			if !ok {
				continue
			}
			mine := &reader{b: []byte(input)}
			if err := op.imm(d, mine); err != nil {
				t.Errorf("%+v: %#02x (%s) failed: %v", f, b, op.name, err)
				continue
			}
			theirs := &reader{b: []byte(input)}
			bad := false
			for _, im := range info.imms {
				if read := immBytes[im]; read != nil {
					if err := read(d, theirs); err != nil {
						bad = true
						break
					}
				}
			}
			if !bad && mine.off != theirs.off {
				t.Errorf("%+v: %#02x (%s) extent disagreement: %d vs %d",
					f, b, op.name, mine.off, theirs.off)
			}
		}
		configs++
	}
	if configs != 16 {
		t.Fatalf("walked %d configurations, want 16 (2^4 tracked gates): a per-configuration "+
			"claim must actually walk them", configs)
	}
}
