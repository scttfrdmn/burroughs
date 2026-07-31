package binary

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
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
// # Enrolled, not merely independent (ruling: Scott, PR #43)
//
// An independent hand-written copy of a duplicated fact is legitimate **only if it
// testifies**. Three copies with only some checked is a drift farm, so the rule that
// settles 0006's shape is: *every copy of a fact is either an enrolled witness or a
// derived artifact.* immBytes is enrolled. Two obligations follow, and both are
// discharged by tests below rather than by this comment:
//
//   - Every entry cites the authority's own definition (the `authority` field), and
//     TestImmBytesCitationsResolve machine-checks that the cited line says what the
//     citation claims — the fixture-provenance rule pointed at a reader table.
//   - Every non-nil entry is measured *on its own*, not only in composition over the
//     const set, by TestEveryReaderAgreesWithItsAuthorityDefinition. Composition-only
//     coverage is what let two wrong entries sit here green: `laneidx` and `laneidx16`
//     are unreachable from the const production, so the extent differential never once
//     compared them. Both were wrong (grave #47).
//
// On disagreement the **reference-derived table is the presumptive authority** — that is
// the whole content of 0007. A disagreement is a bug in this map until decode.ml is shown
// to say otherwise.
//
// A nil reader means "no flat reader exists", which is a real category rather than a
// gap: the four structural arms (block, loop, if, try_table) recurse through the
// instruction grammar and cannot be expressed as a byte count. The const production
// contains none of them, and TestConstSetUsesNoStructuralImmediate is what keeps that
// from being an assumption.
var immBytes = map[imm]reader3{
	// The LEB readers. Width matters and is the authority's, not a guess: idx is u32
	// (decode.ml:151), and laneidx is *u8* (decode.ml:152) — `uN 8`, a one-to-two-byte
	// LEB, not a raw byte. See ImmLaneIdx's entry for why that distinction cost a grave.
	immS32: {
		"decode.ml:107", "let s32 s = I32.of_int_s (I64.to_int_s (sN 32 s))",
		func(_ *Decoder, r *reader) error { _, err := r.s32(); return err },
	},
	immS64: {
		"decode.ml:109", "let s64 s = sN 64 s",
		func(_ *Decoder, r *reader) error { _, err := r.s64(); return err },
	},
	immU32: {
		"decode.ml:104", "let u32 s = I32.of_int_u (I64.to_int_u (uN 32 s))",
		func(_ *Decoder, r *reader) error { _, err := r.u32(); return err },
	},
	immIdx: {
		"decode.ml:151", "let idx s = u32 s",
		func(_ *Decoder, r *reader) error { return discardIndex(r) },
	},

	// The fixed-width readers. word32/word64 are little-endian raw reads, not LEBs, and
	// v128 is a flat 16-byte string — so a byte count is the honest reader here.
	immF32: {
		"decode.ml:110", "let f32 s = F32.of_bits (word32 s)",
		func(_ *Decoder, r *reader) error { _, err := r.bytes(4); return err },
	},
	immF64: {
		"decode.ml:111", "let f64 s = F64.of_bits (word64 s)",
		func(_ *Decoder, r *reader) error { _, err := r.bytes(8); return err },
	},
	immV128: {
		"decode.ml:112", "let v128 s = V128.of_bits (get_string 16 s)",
		func(_ *Decoder, r *reader) error { _, err := r.bytes(16); return err },
	},

	// `op s = byte s` — the raw one-byte read, and the only immediate that genuinely is
	// one. Everything that *looks* like a byte because its values are small is a LEB.
	immByte: {
		"decode.ml:67", "let byte s =",
		func(_ *Decoder, r *reader) error { _, err := r.byte(); return err },
	},

	// GRAVE (#47): this was r.byte(). `laneidx s = u8 s` and `u8 s = ... (uN 8 s)`, so a
	// lane index is a LEB whose canonical encoding is one byte and whose *legal* encoding
	// runs to two (`81 01` is 129 in two bytes: uN 8 accepts it, byte() stops after one).
	// Wrong by one byte on every non-canonical lane index, and invisible here for two
	// compounding reasons — no lane instruction is const-legal, so composition never
	// reached it, and "laneidx is small so it must be a byte" reads correctly.
	immLaneIdx: {
		"decode.ml:152", "let laneidx s = u8 s",
		func(_ *Decoder, r *reader) error { _, err := r.uleb(8); return err },
	},

	// GRAVE (#47): this was r.bytes(16). `repeat 16 laneidx s` is sixteen *laneidx*
	// reads, so its extent is 16..32 bytes, not 16. Same root as immLaneIdx and the same
	// blind spot: i8x16_shuffle is not const-legal either. The extractor got the
	// *count* right (that was #46) and this map got the *element* wrong.
	immLane16: {
		"decode.ml:699", "| 0x0dl -> let is = repeat 16 laneidx s in i8x16_shuffle is",
		func(_ *Decoder, r *reader) error {
			for range 16 {
				if _, err := r.uleb(8); err != nil {
					return err
				}
			}
			return nil
		},
	},

	// heaptype is `either [typeuse s33; s7 ...]` — a backtracking alternation, so its
	// extent is input-dependent in general. decodeRefType covers the one-byte shorthand
	// this decoder accepts today; the difference is bounded by
	// TestEveryReaderAgreesWithItsAuthorityDefinition's declared-partial entry rather
	// than left as a silent approximation.
	immHeapType: {
		"decode.ml:179", "let heaptype s =",
		func(d *Decoder, r *reader) error { return d.decodeRefType(r) },
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
	immMemop:      {"decode.ml:324", "let memop s =", nil},
	immValType:    {"decode.ml:220", "let valtype s =", nil},
	immVecValType: {"decode.ml:123", "let vec f s = let n = len32 s in list f n s", nil},
	immVecIdx:     {"decode.ml:123", "let vec f s = let n = len32 s in list f n s", nil},
	immCatchVec:   {"decode.ml:123", "let vec f s = let n = len32 s in list f n s", nil},
	immBlockType:  {"decode.ml:334", "let blocktype s =", nil},
	immBlock:      {"decode.ml:967", "and instr_block' s es =", nil},
}

// reader3 is one enrolled entry: this package's reader for an immediate, plus the
// citation that makes it a witness rather than a third opinion.
//
// The name is deliberate — it is the *third* copy of the fact (decoder readers, table,
// this map), and naming it that is a standing reminder of why the citation fields are
// not optional.
type reader3 struct {
	// authority is a `decode.ml:N` citation for the definition this reader mirrors.
	authority string
	// text is what that line says, checked verbatim against the vendored source by
	// TestImmBytesCitationsResolve. A citation nobody verifies is a claim, not a
	// citation — and a citation that has drifted to a *different* line is worse than
	// absent, because it looks checked.
	text string
	// read is nil when no flat reader exists; see immBytes' comment for each case.
	read func(*Decoder, *reader) error
}

// bytesFor is the accessor the extent comparisons use, so enrolling immBytes did not
// change their shape: a nil reader is still "no flat reader exists".
func bytesFor(im imm) func(*Decoder, *reader) error { return immBytes[im].read }

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

// refDecodeML is the vendored authority, reached through the licensed door.
var refDecodeML = filepath.Join("..", "..", testenv.RefDecodeML)

// TestImmBytesCitationsResolve is what makes immBytes an enrolled witness rather than a
// third hand-written opinion (ruling: Scott, PR #43).
//
// Every entry claims a `decode.ml:N` definition and quotes it; this reads the vendored
// source and checks the quote is what that line actually says. Exactly the
// fixture-provenance mechanism (`TestFixtureProvenance`) pointed at a reader table: a
// citation nobody verifies is a claim, and a citation that has drifted to a different
// line is *worse* than an absent one, because it looks checked.
//
// The reader itself is reviewed by eyes against the cited text — that judgement is
// allowed. What is machine-checked is that the premise resolves, which is the half a
// mechanism can own.
func TestImmBytesCitationsResolve(t *testing.T) {
	src := testenv.RequireSpecRef(t, refDecodeML)
	lines := strings.Split(src, "\n")

	if len(immBytes) == 0 {
		t.Fatal("immBytes is empty: a citation check over no entries resolves everything")
	}
	for im, e := range immBytes {
		if e.authority == "" || e.text == "" {
			t.Errorf("%q has no citation: an enrolled witness testifies or it is derived; "+
				"there is no third option (PR #43)", im)
			continue
		}
		prefix, num, ok := strings.Cut(e.authority, ":")
		if !ok || prefix != "decode.ml" {
			t.Errorf("%q: citation %q is not of the form decode.ml:N", im, e.authority)
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil || n < 1 || n > len(lines) {
			t.Errorf("%q: citation %q does not resolve to a line in a %d-line file",
				im, e.authority, len(lines))
			continue
		}
		if got := strings.TrimSpace(lines[n-1]); got != strings.TrimSpace(e.text) {
			t.Errorf("%q: %s says\n\t\tgot  %q\n\t\twant %q\n\t"+
				"the citation has drifted: either upstream moved and the reader needs "+
				"re-checking against the new definition, or the line was wrong when written",
				im, e.authority, got, e.text)
		}
	}
	t.Logf("%d immBytes citations resolve against %s", len(immBytes), refDecodeML)
}

// TestEveryReaderAgreesWithItsAuthorityDefinition measures each enrolled reader on its
// own, which composition over the const set cannot do.
//
// This is the control that would have caught grave #47 and did not exist to. The extent
// differential runs the *whole immediate sequence* of const-legal opcodes, so a reader
// no const-legal opcode uses is never exercised — `laneidx` and `laneidx16` sat in
// immBytes wrong, and green, for exactly that reason. Scoped to the space: every non-nil
// entry, not the ones today's const set reaches.
//
// The vectors are **derived**, in the provenance sense (PR #37): the suite contains no
// vector for a two-byte lane index, and `binary.wast` cannot supply one because a
// non-canonical-but-legal LEB in a lane field is *well-formed* — the accept direction,
// which is the blind spot 0007 exists for. Each case states the reference's rule and the
// extent it entails.
func TestEveryReaderAgreesWithItsAuthorityDefinition(t *testing.T) {
	// A LEB whose canonical form is one byte, encoded in two: continuation bit set on
	// the first, payload in the second. Legal for any width uN/sN accepts, and the
	// discriminator between a LEB reader and a raw byte read.
	const twoByteLEB = "\x81\x00"
	// Sixteen of them, for repeat 16.
	lane16Wide := strings.Repeat(twoByteLEB, 16)

	cases := map[imm]struct {
		in   string
		want int    // bytes the authority's definition consumes
		why  string // the rule that entails it
	}{
		immByte: {
			"\x81\x00", 1,
			"decode.ml:321 `op s = byte s` is a raw read: one byte, continuation bit or not",
		},
		immU32: {
			twoByteLEB, 2,
			"uN 32 consumes continuation bytes; a two-byte encoding of 1 is legal and consumed whole",
		},
		immIdx: {twoByteLEB, 2, "idx s = u32 s (decode.ml:151), so it is a LEB"},
		immS32: {twoByteLEB, 2, "sN 32, same continuation rule"},
		immS64: {twoByteLEB, 2, "sN 64, same continuation rule"},
		immF32: {
			"\x81\x00\x00\x00\x00", 4,
			"word32 is four raw little-endian bytes: no continuation logic at all",
		},
		immF64: {"\x81\x00\x00\x00\x00\x00\x00\x00\x00", 8, "word64, eight raw bytes"},
		immV128: {
			strings.Repeat("\x81", 17), 16,
			"v128 s = get_string 16 s: a flat sixteen-byte string",
		},
		immLaneIdx: {
			twoByteLEB, 2,
			"laneidx s = u8 s = uN 8 (decode.ml:152,103): a LEB, so a two-byte encoding " +
				"consumes two — this is grave #47's exact vector",
		},
		immLane16: {
			lane16Wide, 32,
			"repeat 16 laneidx (decode.ml:699): sixteen laneidx reads, so 16..32 bytes, " +
				"not a flat bytes(16)",
		},
		// heaptype is declared partial rather than omitted: decodeRefType covers the
		// one-byte shorthand and the alternation's other branch (typeuse s33) is #7's.
		// Stating the bound is what keeps a partial reader from being a silent one.
		immHeapType: {
			"\x70", 1,
			"decodeRefType covers heaptype's single-byte shorthand branch; the typeuse s33 " +
				"branch is not implemented and its coverage arrives with #7",
		},
	}

	d := &Decoder{}
	checked := 0
	for im, e := range immBytes {
		if e.read == nil {
			continue
		}
		c, ok := cases[im]
		if !ok {
			t.Errorf("%q has a reader but no per-reader vector: composition over the const "+
				"set does not reach every reader, so an unexercised entry is grave #47's "+
				"shape waiting to happen", im)
			continue
		}
		r := &reader{b: []byte(c.in)}
		if err := e.read(d, r); err != nil {
			t.Errorf("%q: reader failed on its own vector %x: %v", im, c.in, err)
			continue
		}
		if r.off != c.want {
			t.Errorf("%q (%s): consumed %d bytes on %x, the authority's definition entails %d\n\t%s",
				im, e.authority, r.off, c.in, c.want, c.why)
		}
		checked++
	}
	// Vacuity: an agreement over zero readers agrees perfectly. The floor is the count
	// of non-nil entries, derived rather than written down.
	flat := 0
	for _, e := range immBytes {
		if e.read != nil {
			flat++
		}
	}
	if checked != flat || flat == 0 {
		t.Errorf("measured %d of %d flat readers (flat must be >0): a per-reader claim must "+
			"walk every reader it claims", checked, flat)
	}
	t.Logf("%d flat readers agree with their authority definitions", checked)
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
			if bytesFor(im) == nil {
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
			read := bytesFor(im)
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
				if read := bytesFor(im); read != nil {
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
