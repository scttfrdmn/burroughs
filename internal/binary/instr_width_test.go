package binary

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// The two controls on Instr's fixed width, both scoped to the space rather than to the
// arms the retention happens to exercise today.
//
// # Why the width needs controls at all
//
// 0002 chose a fixed-width instruction with pre-decoded immediates, and that choice has a
// standing obligation: the table says what each arm reads, `Instr` says how much room
// there is, and nothing structural connects the two. Both directions of a mismatch are
// **accept-direction** defects — a value truncated into a smaller field and a value
// dropped for want of a slot are each a *different instruction than the module contains*,
// on input the suite expects to pass. Contract §9 G-3: the board scores those green by
// construction, so a control is the only instrument that can see them.
//
// Both controls have already earned their keep, and neither found what it was written to
// confirm:
//
//   - the `Op` width control's subject was a `byte` field, which the 0xfd region
//     overflows at 0x113;
//   - the immediate-width control's subject was the sentence "no arm stages more than
//     two", which eight rows falsify (`v128.load8_lane` and siblings: a memarg's two
//     words plus a lane index).
//
// In both cases the comment asserting safety was written before the measurement that
// would have refuted it. That is the pattern these two files exist against.

// TestPrefixedSubOpcodesFitOp asserts every sub-opcode in every region is representable in
// Instr.Op.
//
// Scoped to all four regions and every row in them, derived from prefixRegions rather than
// from a list of the prefixes SIMD happens to use — a control enumerating today's regions
// would freeze at authorship and say nothing about a fifth arriving upstream (*derive the
// domain, never enumerate it*).
//
// It is written against the *field's* width rather than against the constant 0x113, because
// the fact under test is "the field holds the table", not "the table's maximum is what it
// was on the day this was written". Narrowing Instr.Op back to a byte therefore fails here;
// so does a region growing past 2^32, which is the honest bound and not a plausible one.
func TestPrefixedSubOpcodesFitOp(t *testing.T) {
	var probe Instr
	// The field's capacity, read off the field. A literal here would be a second place
	// knowing Instr.Op's width, drifting silently the moment the field changes — the
	// one-concept-one-trigger rule (#82) applied to a width.
	probe.Op = ^probe.Op
	capacity := uint64(probe.Op)

	rows, maxima := 0, map[byte]uint32{}
	for prefix, region := range prefixRegions {
		if len(region) == 0 {
			t.Errorf("region %#02x is empty: a width claim over no rows is a claim about "+
				"nothing (the vacuity class, #29)", prefix)
		}
		for code, info := range region {
			rows++
			if uint64(code) > capacity {
				t.Errorf("%#02x %#x (%s, decode.ml:%d) does not fit Instr.Op, whose capacity "+
					"is %#x\n\ta sub-opcode truncated into the field is a *different "+
					"instruction* than the module contains, on valid input — the "+
					"accept-direction class no board can see (§9 G-3)",
					prefix, code, info.mnemonic, info.refLine, capacity)
			}
			maxima[prefix] = max(maxima[prefix], code)
		}
	}
	// A per-region floor, not just a non-empty check on the whole walk: an extractor that
	// found one region and lost three would pass a global count and is precisely the
	// moved-file failure the vacuity law names. 0xfd alone has hundreds of rows.
	if rows < 400 {
		t.Fatalf("walked %d rows across %d regions, want ≥400: the generated table declares "+
			"542 arms, so a walk this short means the domain was lost, not that the table "+
			"shrank", rows, len(prefixRegions))
	}
	// The measurement, printed rather than asserted. The maxima are *why* Op is a uint32,
	// and quoting them is what makes the next reader able to check the claim instead of
	// trusting it — a byte holds 0xff, and 0xfd's region does not.
	for prefix, m := range maxima {
		t.Logf("region %#02x: max sub-opcode %#x", prefix, m)
	}
	if maxima[0xfd] <= 0xff {
		t.Errorf("0xfd's maximum sub-opcode is %#x, which fits a byte — the whole reason "+
			"Instr.Op is a uint32 is that this region exceeds 0xff (measured at 0x113). "+
			"If the table really shrank, Op's width comment needs revising; if the region "+
			"was lost, this walk is vacuous", maxima[0xfd])
	}
}

// immStagedBits is how many bits of Instr each immediate commits, in the reader's own
// terms — instrCtx.imm is the authority for this table and the two are checked against
// each other below.
//
// Bits rather than slots, because the fix that made this control necessary packs three
// values into two words: a lane index shares Imm1's upper half with a memory index
// (stageLaneIdx). A slot count cannot express that and would either report the eight
// `loadN_lane` rows as overflowing when they do not, or license a genuine overflow by
// rounding down.
//
// Zero is a real entry, not a gap: `vec` immediates and heaptypes are read and *not*
// retained, each with a reason at its arm in instrCtx.imm. They commit no bits.
var immStagedBits = map[imm]int{
	immIdx:       64, // stage(uint64(u32))
	immU32:       64,
	immS32:       64, // sign-extended to the full word
	immS64:       64,
	immF32:       64, // the bit pattern, in a word of its own
	immF64:       64,
	immByte:      64,
	immLaneIdx:   8,   // packed above a memarg's memory index when Imm1 is taken
	immValType:   64,  // 0018: kind/null/idx packed into one word (see instrCtx.imm's arm)
	immBlockType: 128, // 0018: tag bits above 2^32 in the first word, the valtype's resolved
	// index (meaningful only for the indexed reference form) staged unconditionally in the
	// second — see module.go's BlockType comment
	immV128:   128, // both words, exactly
	immLane16: 128, // sixteen u8 lanes, packed
	immMemop:  96,  // a u64 offset plus a u32 memory index

	// Read and dropped, each with its reason at the arm. A vector cannot live in a
	// fixed-width instruction, so these are #7's side-array work rather than staging.
	immVecValType: 0,
	immVecIdx:     0,
	immCatchVec:   0,
	immHeapType:   0,

	// Structural: routed to instrCtx.structural, which emits its header before recursing.
	// The block itself stages nothing; the arms' *other* immediates are the ones above.
	immBlock: 0,
}

// TestInstrImmediateWidthCoversTheTable asserts no arm's immediates need more room than
// Instr provides.
//
// The obligation `stage` documents, discharged as a test rather than as a sentence. The
// sentence it replaces said "no arm stages more than two" and was false for eight rows,
// which is the case for measuring instead of asserting: the demand is summed **per row,
// over every region**, so an arm added upstream with a third word fails here rather than
// having a value silently dropped.
//
// Where the previous claim was about a *count*, this is about *bits*, and that is the
// substantive change. Three values legitimately fit two words when their widths allow it
// (memarg offset + memory index + lane index = 104 bits), so a control that counted
// staging calls would reject a correct reader. Counting bits distinguishes "packed" from
// "overflowed", which is the distinction the fix turns on.
func TestInstrImmediateWidthCoversTheTable(t *testing.T) {
	// Instr's capacity, read off the type rather than written as 128 — the same
	// one-definition rule the Op control follows.
	var probe Instr
	capacity := bitsOf(probe.Imm0) + bitsOf(probe.Imm1)

	// Every immediate the table names must appear in immStagedBits, and nothing else may:
	// an unmapped immediate would be silently summed as zero, which is a control agreeing
	// with a reader it never consulted.
	vocab := map[imm]bool{}
	for _, region := range prefixRegions {
		for _, info := range region {
			for _, im := range info.imms {
				vocab[im] = true
			}
		}
	}
	if len(vocab) == 0 {
		t.Fatal("no immediates found in the generated table: a width claim over an empty " +
			"vocabulary is vacuous (#29)")
	}
	for im := range vocab {
		if _, ok := immStagedBits[im]; !ok {
			t.Errorf("immediate %q appears in the table but not in immStagedBits: an "+
				"unmapped immediate sums as zero, so this control would license an "+
				"overflow rather than catch it", im)
		}
	}
	// The reverse direction, and it is checked against the **vocabulary the reader
	// dispatches** rather than against the rows that happen to use it. Those are not the
	// same set, which is a finding this control produced on its first run: `immValType` is
	// declared in optable.go and used by *no row* in the pinned revision, because
	// `select`'s type list is `vec_valtype` and the bare form the extractor recognizes
	// (`at valtype s`, extract.go:209) matches nothing at this revision. An entry for it is
	// therefore not stale — instrCtx.imm has a live arm for it, so the width claim has a
	// subject even with the row count at zero, and dropping the entry would make a future
	// row sum as zero bits.
	//
	// So the stale-entry check is against immVocabulary, which is derived from the
	// extractor's own declared constants: an entry naming an immediate the reader cannot
	// dispatch really is a claim about nobody.
	for im := range immStagedBits {
		if !immVocabulary[im] {
			t.Errorf("immStagedBits names %q, which is not in the immediate vocabulary: a "+
				"stale entry is a claim about a reader nobody calls", im)
		}
	}
	// And every dispatchable immediate needs an entry, whether or not a row uses it today
	// — a row arriving upstream for an unmapped immediate would sum as zero.
	for im := range immVocabulary {
		if _, ok := immStagedBits[im]; !ok {
			t.Errorf("immediate %q is in the vocabulary but not in immStagedBits: a row "+
				"arriving for it would sum as zero bits and license an overflow", im)
		}
	}

	rows, packed := 0, 0
	for prefix, region := range prefixRegions {
		for code, info := range region {
			rows++
			bits := 0
			for _, im := range info.imms {
				bits += immStagedBits[im]
			}
			if bits > capacity {
				t.Errorf("%#02x %#x (%s, decode.ml:%d) commits %d bits of immediate but Instr "+
					"holds %d\n\timmediates: %v\n\tthe overflow is dropped rather than "+
					"reported, so the retained instruction differs from the module's on "+
					"valid input — grow Instr or pack the narrow fields (see stageLaneIdx)",
					prefix, code, info.mnemonic, info.refLine, bits, capacity, info.imms)
			}
			// A row needing more staging *calls* than Instr has words is a row that only
			// fits because something packs. Counted so the next assertion is not vacuous.
			if len(info.imms) > 2 || bits > 64 && len(info.imms) > 1 {
				packed++
			}
		}
	}
	if rows < 400 {
		t.Fatalf("walked %d rows, want ≥400 across %d regions: the table declares 542 arms, "+
			"so a walk this short lost the domain", rows, len(prefixRegions))
	}
	// The eight `v128.loadN_lane` rows are the reason stageLaneIdx exists. If the packing
	// path has no population, the control above is proving a property of arms that never
	// exercise it — the sample-versus-space failure, inverted.
	if packed == 0 {
		t.Error("no row needs packing: stageLaneIdx exists for the eight v128.loadN_lane " +
			"arms (memarg + laneidx), so a zero here means those rows are gone and this " +
			"control no longer covers the case that motivated it")
	}
	t.Logf("%d rows walked, %d needing packed staging, Instr capacity %d bits", rows, packed, capacity)
}

// bitsOf returns a value's width in bits, without a literal anywhere.
//
// Graves #100 (the dropped lane index) and #101 (Op as a byte) are what this file's two
// controls were written against; both are accept-direction, so neither had any other witness.
func bitsOf(v uint64) int {
	n := 0
	for x := ^v & (v | ^v); x != 0; x >>= 1 { // all-ones, shifted down to zero
		n++
	}
	return n
}

// immVocabulary is every immediate the generated table *declares*, read out of optable.go's
// own const block rather than retyped here.
//
// **The rows are not the vocabulary, and finding that out is what this variable is for.**
// The first draft of the width control checked immStagedBits against the immediates rows
// actually use, and reported `immValType` as a stale entry — it is declared in optable.go,
// has a live arm in instrCtx.imm, and is used by no row at the pinned revision, because
// `select`'s type list is `vec_valtype` and the extractor's bare-valtype pattern
// (`at valtype s`, extract.go:209) matches nothing there. A row-derived domain would
// therefore have pushed the entry out of the table, and the immediate it describes would
// have summed as **zero bits** the day a row arrived for it — the control licensing the
// overflow it exists to catch.
//
// Read from source by AST rather than enumerated, for the reason every domain here is
// derived: a hand-written list is a sample of the vocabulary as of authorship, and the
// vocabulary is generated, so it moves without this file being touched. The extractor's own
// closed-set comment says adding one is "a deliberate act with a diff" — this is what makes
// the diff reach the reader.
var immVocabulary = func() map[imm]bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "optable.go", nil, 0)
	if err != nil {
		panic("immVocabulary: " + err.Error()) // a package that cannot parse its own table
	}
	v := map[imm]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// Typed `imm` constants only: the file's other const blocks are opcodes.
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "imm" {
				continue
			}
			for _, val := range vs.Values {
				lit, ok := val.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				v[imm(s)] = true
			}
		}
	}
	return v
}()

// TestImmVocabularyIsNonEmpty is the vacuity check on the derivation above.
//
// An AST walk that matches nothing returns an empty map, and an empty domain makes both
// directions of the width control's cross-check agree perfectly — the empty-set agreement
// (#29), which is exactly what a moved file or a renamed type produces. The floor is a
// count rather than a non-nil check, because "found some" is what an under-matching
// pattern reports too.
func TestImmVocabularyIsNonEmpty(t *testing.T) {
	// The vocabulary is closed and hand-declared upstream; 18 entries at the pinned
	// revision. A floor rather than an equality, so adding one is not a failure here —
	// the cross-check in the width control is what notices an addition.
	if len(immVocabulary) < 15 {
		t.Fatalf("derived %d immediates from optable.go's const block, want ≥15: the walk "+
			"found too few to be the vocabulary, so the width control's cross-check is "+
			"between two nearly-empty sets", len(immVocabulary))
	}
	t.Logf("%d immediates declared in optable.go", len(immVocabulary))
}

// TestStagedBitsAgreeWithTheReader checks immStagedBits against instrCtx.imm by running
// it, rather than by reading it.
//
// The width table above is hand-authored testimony about what the production reader does,
// and testimony nobody cross-examines is a claim (the drifted-citation class). So each
// immediate is *read for real* and the words it actually touched are compared to the
// entry's own claim: an entry saying 64 must move exactly one word, 128 must move two,
// and 0 must move none.
//
// Bits cannot be observed directly — a packed field shares a word — so the observable is
// the staging cursor, and the mapping between them is the one place this test is allowed
// to know both sides: ⌈bits/64⌉ words, except that a sub-word entry (laneidx's 8) claims
// one word when the cursor is free and none when it packs. Both cases are exercised.
func TestStagedBitsAgreeWithTheReader(t *testing.T) {
	// **Two inputs, for TestEveryImmediateHasAProductionReader's reason and not by copying
	// it.** No single filler byte is legal for the whole vocabulary, and the first draft
	// used all-zero alone and reported `valtype` as rejecting its input — 0x00 is not the
	// encoding of any value type, which is precisely the fact NoValType's comment turns
	// on. All-0x70 is a legal valtype/heaptype and a complete one-byte LEB; all-zero is
	// what the `vec` immediates need, since 0x70 as a vec *length* asks for 112 elements.
	//
	// Each immediate must read on at least one, and the word count is taken from the input
	// that read — a claim about the reader, not about the filler.
	inputs := [][]byte{bytes.Repeat([]byte{0x70}, 64), make([]byte, 64)} // 64 ≥ immLane16's 32

	checked := 0
	for im, bits := range immStagedBits {
		if im == immBlock {
			// Structural: needs a terminator no flat input supplies, and it stages nothing
			// by construction. Excluded by name rather than by the reader failing, which is
			// the distinction TestEveryImmediateHasAProductionReader draws.
			continue
		}
		read := false
		var errs []error
		for _, input := range inputs {
			c := &instrCtx{d: &Decoder{Features: featuresAllOn(t)}, nonConst: -1}
			r := &reader{b: input, eof: ErrPayloadEnd}
			if err := c.imm(r, im); err != nil {
				errs = append(errs, err)
				continue
			}
			read = true
			want := (bits + 63) / 64
			if c.immN != want {
				t.Errorf("immediate %q: immStagedBits says %d bits (%d words) but the reader "+
					"staged %d\n\tthe width table is testimony about this reader, so a "+
					"disagreement means one of them is wrong and the overflow control above is "+
					"summing fiction", im, bits, want, c.immN)
			}
			break
		}
		if !read {
			t.Errorf("immediate %q read neither all-0x70 nor all-zero: %v\n\tthe agreement "+
				"cannot be checked for an immediate that does not read at all", im, errs)
			continue
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no immediates checked: see the vacuity note in the width control")
	}

	// The packing case, which the loop above cannot reach because it reads one immediate at
	// a time and packing only happens when a *prior* immediate has taken both words.
	//
	// **Driven through the real rows, not through stageLaneIdx directly**, and that
	// distinction is a finding rather than a preference: the first draft of this section
	// called `c.stage`, `c.stage`, `c.stageLaneIdx` by hand, and reintroducing the original
	// defect — the `immLaneIdx` arm calling plain `stage`, which drops the lane — left it
	// **green**. It was asserting that the packing function packs, while the defect was
	// that nothing called it. *Registration is not verification* (#78): a control that
	// exercises a helper instead of the path is a control that cannot see the path being
	// bypassed.
	//
	// So the input is a real memarg followed by a real lane index, read by instrCtx.imm
	// through the immediates the eight `v128.loadN_lane` rows actually declare.
	for _, row := range laneMemopRows(t) {
		c := &instrCtx{d: &Decoder{Features: featuresAllOn(t)}, nonConst: -1}
		// flags 0x40 (explicit memory index), memidx 7, offset 0x5E, laneidx 0x0B. Every
		// field is a *complete* one-byte LEB: 0x5E rather than 0xDE, because 0xDE's high
		// bit is a continuation and the read would run off the end — which is how this
		// input was first written and what the reader said about it.
		r := &reader{b: []byte{0x40, 0x07, 0x5E, 0x0B}, eof: ErrPayloadEnd}
		for _, im := range row.imms {
			if err := c.imm(r, im); err != nil {
				t.Fatalf("%s: reading %q failed: %v", row.mnemonic, im, err)
			}
		}
		if c.imm0 != 0x5E {
			t.Errorf("%s: offset staged as %#x, want 0x5e", row.mnemonic, c.imm0)
		}
		if got := c.imm1 & 0xFFFF_FFFF; got != 7 {
			t.Errorf("%s: memory index staged as %#x, want 7 — packing the lane index above "+
				"it must not disturb it", row.mnemonic, got)
		}
		if got := c.imm1 >> 32 & 0xFF; got != 0x0B {
			t.Errorf("%s: lane index staged as %#x, want 0x0b\n\ta dropped lane index makes "+
				"this a different instruction than the module contains, on valid input — "+
				"which is why this is a control and not a comment", row.mnemonic, got)
		}
	}
}

// TestBlockTypeImm1IsFreeForStructuralOpcodes is the control 0018's implementation
// pre-registered rather than assumed: `BlockType`'s packing comment (module.go) claims Imm1
// is free for every opcode whose immediates include immBlockType, because decodeBlockTypeValue
// now stages *both* Instr words itself (Imm0's tag/kind/null, Imm1's resolved index for the
// indexed valtype form) and every other immediate those rows carry — immBlock, immCatchVec —
// stages zero bits per immStagedBits.
//
// **Derived from the generated table, not from the four opcodes (block/loop/if/try_table)
// named by number**, for the reason every domain here is derived: a hand-written list of
// "the four structural opcodes" freezes at authorship and says nothing about a fifth arriving
// upstream with a third immediate that also wants Imm1 — exactly the collision this control
// exists to catch before it silently drops a bit. TestPrefixedSubOpcodesFitOp and
// TestInstrImmediateWidthCoversTheTable are this file's siblings in shape; this one is scoped
// to the one row-shape 0018's packing depends on.
func TestBlockTypeImm1IsFreeForStructuralOpcodes(t *testing.T) {
	rows := 0
	for prefix, region := range prefixRegions {
		for code, info := range region {
			hasBlockType := false
			for _, im := range info.imms {
				if im == immBlockType {
					hasBlockType = true
					break
				}
			}
			if !hasBlockType {
				continue
			}
			rows++
			// Every immediate *other* than immBlockType itself must stage zero bits, or
			// this row already has something else contending for the second word
			// BlockType's packing claims for its own use.
			for _, im := range info.imms {
				if im == immBlockType {
					continue
				}
				if bits := immStagedBits[im]; bits != 0 {
					t.Errorf("%#02x %#x (%s, decode.ml:%d) carries immBlockType and %q, "+
						"which stages %d bits — BlockType's packing (module.go) assumes "+
						"Imm1 is free for the valtype form's resolved index, and this row "+
						"contends for it", prefix, code, info.mnemonic, info.refLine, im, bits)
				}
			}
		}
	}
	// The vacuity floor: an empty walk would make the loop above assert nothing and pass
	// for free, which is exactly the empty-set agreement (#29). Four rows at the pinned
	// revision — block, loop, if_, try_table — so two is a floor with margin rather than an
	// exact pin, since exactness here would fail the day a fifth structural opcode lands
	// with no code change to this test.
	if rows < 2 {
		t.Fatalf("found %d rows carrying immBlockType, want ≥2: the generated table declares "+
			"four (block, loop, if_, try_table), so a walk this short lost the domain", rows)
	}
	t.Logf("%d structural rows checked", rows)
}

// laneMemopRows returns every row whose immediates are a memarg followed by a lane index —
// the arms that need three values in two words.
//
// Derived from the table, so the eight `v128.loadN_lane` rows are found rather than named,
// and a ninth arriving upstream is covered without an edit here. The floor is the vacuity
// check: an empty result would make the packing assertions above run over nothing and agree
// perfectly, which is the empty-set class (#29).
func laneMemopRows(t *testing.T) []opInfo {
	t.Helper()

	var rows []opInfo
	for _, region := range prefixRegions {
		for _, info := range region {
			// The shape, not the mnemonic: a memarg (two words) with a laneidx after it.
			if len(info.imms) == 2 && info.imms[0] == immMemop && info.imms[1] == immLaneIdx {
				rows = append(rows, info)
			}
		}
	}
	if len(rows) == 0 {
		t.Fatal("no memarg+laneidx rows found: stageLaneIdx exists for exactly these arms, " +
			"so an empty walk means the packing assertions below assert nothing (#29)")
	}
	return rows
}
