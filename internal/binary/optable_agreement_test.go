package binary

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
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
// **That sentence was false from #33 until #471 — grave #495.** The walks were keyed on
// `constLegalBytes(tb) map[byte]bool`, so the second half of the claim was not merely
// unchecked but *unrepresentable*: a `byte` cannot name `0xFB 0x1C`, and no entry could
// have been missing from a list because there was no list. It reads as a scoping decision
// and is a type. The domain was 305 prefixed arms short of what the sentence says, and the
// cost was 296 opcodes const-legal by omission in the engine (#471) against two board rows.
// Re-keyed to `constLegalOps` here; the space itself is now pinned, in the direction the
// type used to bound, by `TestConstLegalSpaceIsTheWholeDispatchableSpace`.
//
// Kept rather than rewritten into the present tense, because the sentence is what the
// scoping *argument* was and the argument was right — a claim about coverage that a type
// silently narrows is worth more as a specimen than as a corrected line.
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
//
//     **"The reference does not encode it either" is false, and it is the second half of
//     grave #495.** It is true of `decode.ml`, which is the parser — const-legality is a
//     *validation* fact, so its absence there is the file minding its own business, not the
//     reference declining to record it. `valid.ml:1029-1038` records it, as a ten-arm match
//     called `is_const`, and it has been there the whole time. The citation resolves and says
//     what it is quoted as saying; the sentence generalizes from one file to the reference,
//     which no citation sweep can check (*a valid citation does not certify its sentence*).
//     What the wrong authority bought was a *reason not to look*: property 1's missing half
//     was recorded as unassertable-in-principle for as long as it took a board row to
//     contradict it. Both directions are now walked against `is_const` in
//     `constlegal_test.go`, so the one-directional claim below is this file's scope and no
//     longer the project's.
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
// *the sequence the table chose* and *the reader immBytes chose*, which are independent.
//
// The dissolution (#43/#39) made that independence load-bearing rather than incidental.
// The differential used to have `constExprOps`' hand-written readers on one side; those
// are gone, and the production path now reads immediates *through* the table — so
// immBytes is the only remaining independent witness to what each immediate's extent is.
// Deriving it from the table would leave the extent fact with no witness at all.
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
		"spec/binary/decode.ml:107", "let s32 s = I32.of_int_s (I64.to_int_s (sN 32 s))",
		func(_ *Decoder, r *reader) error { _, err := r.s32(); return err },
	},
	immS64: {
		"spec/binary/decode.ml:109", "let s64 s = sN 64 s",
		func(_ *Decoder, r *reader) error { _, err := r.s64(); return err },
	},
	immU32: {
		"spec/binary/decode.ml:104", "let u32 s = I32.of_int_u (I64.to_int_u (uN 32 s))",
		func(_ *Decoder, r *reader) error { _, err := r.u32(); return err },
	},
	immIdx: {
		"spec/binary/decode.ml:151", "let idx s = u32 s",
		func(_ *Decoder, r *reader) error { return discardIndex(r) },
	},

	// The fixed-width readers. word32/word64 are little-endian raw reads, not LEBs, and
	// v128 is a flat 16-byte string — so a byte count is the honest reader here.
	immF32: {
		"spec/binary/decode.ml:110", "let f32 s = F32.of_bits (word32 s)",
		func(_ *Decoder, r *reader) error { _, err := r.bytes(4); return err },
	},
	immF64: {
		"spec/binary/decode.ml:111", "let f64 s = F64.of_bits (word64 s)",
		func(_ *Decoder, r *reader) error { _, err := r.bytes(8); return err },
	},
	immV128: {
		"spec/binary/decode.ml:112", "let v128 s = V128.of_bits (get_string 16 s)",
		func(_ *Decoder, r *reader) error { _, err := r.bytes(16); return err },
	},

	// `op s = byte s` — the raw one-byte read, and the only immediate that genuinely is
	// one. Everything that *looks* like a byte because its values are small is a LEB.
	immByte: {
		"spec/binary/decode.ml:67", "let byte s =",
		func(_ *Decoder, r *reader) error { _, err := r.byte(); return err },
	},

	// `atomic.fence`'s flag: one byte whose only legal value is zero. The *extent* is what
	// this table witnesses, and it is one byte either way — so the reader here is the plain
	// byte read, and the constraint is deliberately absent from it. A reader that also
	// refused a non-zero value would make this entry a second copy of the check in
	// instrCtx.imm, and the differential would then compare that check against itself. The
	// constraint's own witness is TestAtomicFenceRejectsANonZeroFlag.
	//
	// The first entry here whose authority is not the core pin, which is why every citation
	// above now carries a pin name.
	immZeroByte: {
		"spec-threads/binary/decode.ml:786", "| 0x03 -> expect 0x00 s \"zero flag expected\"; atomic_fence",
		func(_ *Decoder, r *reader) error { _, err := r.byte(); return err },
	},

	// GRAVE (#47): this was r.byte(). `laneidx s = u8 s` and `u8 s = ... (uN 8 s)`, so a
	// lane index is a LEB whose canonical encoding is one byte and whose *legal* encoding
	// runs to two (`81 01` is 129 in two bytes: uN 8 accepts it, byte() stops after one).
	// Wrong by one byte on every non-canonical lane index, and invisible here for two
	// compounding reasons — no lane instruction is const-legal, so composition never
	// reached it, and "laneidx is small so it must be a byte" reads correctly.
	immLaneIdx: {
		"spec/binary/decode.ml:152", "let laneidx s = u8 s",
		func(_ *Decoder, r *reader) error { _, err := r.uleb(8); return err },
	},

	// GRAVE (#47): this was r.bytes(16). `repeat 16 laneidx s` is sixteen *laneidx*
	// reads, so its extent is 16..32 bytes, not 16. Same root as immLaneIdx and the same
	// blind spot: i8x16_shuffle is not const-legal either. The extractor got the
	// *count* right (that was #46) and this map got the *element* wrong.
	immLane16: {
		"spec/binary/decode.ml:699", "| 0x0dl -> let is = repeat 16 laneidx s in i8x16_shuffle is",
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
	// extent is input-dependent in general, and both branches now exist: the reader is
	// `decodeHeapType`, complete, where it was `decodeRefType` and **partial in both
	// directions** (#88). The declared-partial entry in
	// TestEveryReaderAgreesWithItsAuthorityDefinition retired with the partiality — a
	// bound stated for a difference that no longer exists is a claim about code that
	// is not there.
	//
	// The extent claim below is the one this map is for, and it is the half the wrong
	// reader got wrong quietly: `typeuse s33` is a LEB, so a two-byte index encoding
	// consumes two, where decodeRefType's `sleb(7)` would stop after one and leave the
	// second byte to be read as the next opcode. That is the grave #47 shape at a third
	// site, and it never fired because the type-index branch was unreachable.
	immHeapType: {
		"spec/binary/decode.ml:179", "let heaptype s =",
		func(d *Decoder, r *reader) error { return d.decodeHeapType(r) },
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
	immMemop:      {"spec/binary/decode.ml:324", "let memop s =", nil},
	immValType:    {"spec/binary/decode.ml:220", "let valtype s =", nil},
	immVecValType: {"spec/binary/decode.ml:123", "let vec f s = let n = len32 s in list f n s", nil},
	immVecIdx:     {"spec/binary/decode.ml:123", "let vec f s = let n = len32 s in list f n s", nil},
	immCatchVec:   {"spec/binary/decode.ml:123", "let vec f s = let n = len32 s in list f n s", nil},
	immBlockType:  {"spec/binary/decode.ml:334", "let blocktype s =", nil},
	immBlock:      {"spec/binary/decode.ml:967", "and instr_block' s es =", nil},
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

// refDecodeML is the vendored authority, reached through the licensed door.
//
// **The core pin's, specifically, and every caller here wants exactly that**: the opcode
// tables are generated from this file and the threads pin's decoder does not know prefix
// 0xfb. Where a sentinel's *authority* is the question rather than the table's, the domain is
// every pin's decoder — see refDecodersML.
var refDecodeML = filepath.Join("..", "..", testenv.RefDecodeML)

// refDecodersML is every pin's `binary/decode.ml`, derived from the pin set so that a third
// pin is in scope on arrival (ADR 0007's 2026-08-28 amendment).
//
// Selected by path suffix rather than listed, because listing is what left the fetch-script
// control checking a third of its subject for two authorities' worth of time. The paths come
// from each pin's own `Floors`, so a pin that does not license a decoder contributes nothing
// and no citation is manufactured for it.
func refDecodersML() []string {
	var out []string
	for _, pin := range testenv.RefPins() {
		for p := range pin.Floors {
			if strings.HasSuffix(p, "interpreter/binary/decode.ml") {
				out = append(out, filepath.Join("..", "..", p))
			}
		}
	}
	sort.Strings(out)
	return out
}

// armAuthority is the decoder a row's `refLine` is a line *in*: the row's own `refPath` where it
// carries one, and its region's otherwise.
//
// # The two fields are one citation
//
// A `refLine` alone names no file, and this package has two decode.ml files.
// `TestStructuralArmsAreExactlyTheBlockRows` walks every region and resolved all of them against
// `refDecodeML`, so the 67 atomics rows and the 0xfe escape were checked against a file with no
// atomics region — 68 of 610 rows, agreeing because the wrong lines happen to contain neither
// `instr_block s` nor `end_ s`. The escape row is the sharper half: it sits in the *single-byte*
// region, whose authority is the core pin, carrying line 780 from the threads pin, where the core
// pin has `v128_store16_lane`. Grave
// [#529](https://github.com/scttfrdmn/burroughs/issues/529).
//
// This is the same defect `regionAuthority` was generated to close, still standing in the control
// beside the one that got fixed — *a FAIL names a site, not the population*. The population is
// every row-to-source resolution in this file, and it now goes through here.
func armAuthority(tb testing.TB, prefix byte, info opInfo) string {
	tb.Helper()
	if info.refPath != "" {
		return filepath.Join("..", "..", info.refPath)
	}
	path, declared := regionAuthority[prefix]
	if !declared {
		tb.Fatalf("region %#02x has no entry in regionAuthority, so a row in it cites a line in no "+
			"named file — the generated provenance map covers every region the table has, so this "+
			"means the table and the map came from different runs; regenerate with: make opcodes",
			prefix)
	}
	return filepath.Join("..", "..", path)
}

// citeArm renders a row's citation whole, path and line, for a failure message.
//
// Because `decode.ml:780` names no pin and this tree has paid for that twice: grave #517 in
// prose, grave #529 in the generated table. A message is testimony
// ([errors-and-testimony.md](../../docs/laws/errors-and-testimony.md)), and one that hands the
// reader an ambiguous citation sends them to the wrong file to check a real failure.
func citeArm(prefix byte, info opInfo) string {
	path := info.refPath
	if path == "" {
		var declared bool
		if path, declared = regionAuthority[prefix]; !declared {
			path = fmt.Sprintf("<no authority recorded for region %#02x>", prefix)
		}
	}
	return fmt.Sprintf("%s:%d", path, info.refLine)
}

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
//
// # The pin name is part of the citation
//
// The form was `decode.ml:N` and it resolved against one file, which was right while one pin
// supplied every immediate. `immZeroByte` comes from the threads pin, at a line number the
// core pin also has and where it says something else entirely — so an unprefixed citation
// would have resolved, quoted the wrong file, and reported a drift that is not one. Every
// entry now names its pin, and the name→path mapping is derived from the pin set: *a label is
// part of the citation*, and a default of "unprefixed means core" is the discovery rule this
// tree keeps paying for.
func TestImmBytesCitationsResolve(t *testing.T) {
	pins := decodersByPinName()
	if len(pins) < 2 {
		t.Fatalf("%d pins license a decoder: this check resolves citations by pin name, so a "+
			"one-pin mapping cannot tell a wrong-pin citation from a right one", len(pins))
	}
	lines := map[string][]string{}
	for name, path := range pins {
		lines[name] = strings.Split(testenv.RequireSpecRef(t, path), "\n")
	}

	if len(immBytes) == 0 {
		t.Fatal("immBytes is empty: a citation check over no entries resolves everything")
	}
	cited := map[string]int{}
	for im, e := range immBytes {
		if e.authority == "" || e.text == "" {
			t.Errorf("%q has no citation: an enrolled witness testifies or it is derived; "+
				"there is no third option (PR #43)", im)
			continue
		}
		pin, rest, ok := strings.Cut(e.authority, "/")
		if !ok {
			t.Errorf("%q: citation %q names no pin; the form is <pin>/binary/decode.ml:N, and the pin "+
				"is load-bearing because both pins have a decode.ml with that line number",
				im, e.authority)
			continue
		}
		src, known := lines[pin]
		if !known {
			t.Errorf("%q: citation %q names pin %q, which licenses no decoder in refPins",
				im, e.authority, pin)
			continue
		}
		file, num, ok := strings.Cut(rest, ":")
		if !ok || file != "binary/decode.ml" {
			t.Errorf("%q: citation %q is not of the form <pin>/binary/decode.ml:N", im, e.authority)
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil || n < 1 || n > len(src) {
			t.Errorf("%q: citation %q does not resolve to a line in %s's %d-line decoder",
				im, e.authority, pin, len(src))
			continue
		}
		if got := strings.TrimSpace(src[n-1]); got != strings.TrimSpace(e.text) {
			t.Errorf("%q: %s says\n\t\tgot  %q\n\t\twant %q\n\t"+
				"the citation has drifted: either upstream moved and the reader needs "+
				"re-checking against the new definition, or the line was wrong when written",
				im, e.authority, got, e.text)
		}
		cited[pin]++
	}
	// Per pin, not a total: a mapping with two pins in it and every citation aimed at one of
	// them resolves perfectly and leaves the second file unread. That is the shape a total
	// cannot express — the same argument censusRegionFloors makes one directory over.
	for name := range pins {
		if cited[name] == 0 {
			t.Errorf("no immBytes citation resolves against pin %q: its decoder is loaded and "+
				"never read, so a wrong path for it would not fail this test", name)
		}
	}
	if !t.Failed() {
		t.Logf("%d immBytes citations resolve, across %d pins: %v", len(immBytes), len(pins), cited)
	}
}

// decodersByPinName is each pin's decoder keyed by the pin's short name — "spec",
// "spec-threads" — resolved for reading from this package's directory.
//
// The name comes from the pin's own `Dest`, which is the field that already carries the job of
// telling the two decode.ml authorities apart (see consultedClauses on why not the index or a
// literal). Derived from refPins, so a third pin is in scope on arrival, and unlike
// refDecodersML this keeps the key: a citation has to say *which*.
func decodersByPinName() map[string]string {
	out := map[string]string{}
	for _, pin := range testenv.RefPins() {
		for p := range pin.Floors {
			if strings.HasSuffix(p, "interpreter/binary/decode.ml") {
				name := strings.TrimSuffix(strings.TrimPrefix(pin.Dest, "third_party/"), "/")
				out[name] = filepath.Join("..", "..", p)
			}
		}
	}
	return out
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
		// The same vector as immByte's, and deliberately the *illegal* value: extent is this
		// test's question, and `expect 0x00 s` consumes its byte before deciding anything about
		// it. A vector of \x00 would agree with a reader that peeked without consuming.
		immZeroByte: {
			"\x81\x00", 1,
			"expect 0x00 s (spec-threads/binary/decode.ml:786) reads one byte through `byte s` and " +
				"then tests it: the constraint is on the value, never on the extent",
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
		// heaptype's **type-index** branch, which is the one no vector reaches and the one
		// the wrong reader could not read at all (#88). A two-byte encoding of index 1 is
		// legal `s33`, so the authority consumes both; decodeRefType's sleb(7) reported
		// `integer representation too long` on these same bytes — measured, and the reason
		// the vector here is the wide encoding rather than the one-byte shorthand that
		// would have agreed with either reader.
		immHeapType: {
			twoByteLEB, 2,
			"heaptype = either [typeuse s33; s7 ...] (decode.ml:178-198); typeuse s33 is a " +
				"LEB, so a two-byte index encoding consumes two — a one-byte reader leaves " +
				"0x00 to be read as the next opcode",
		},
	}

	// **Every gate on**, and that is not a convenience: this test measures *extent*, which
	// is a property of the grammar and not of the configuration, so a reader whose vector
	// sits behind a gate would otherwise fail here for a reason that has nothing to do with
	// how many bytes the authority consumes. It came up the moment an enrolled reader
	// acquired a gate — `heaptype`'s type-index branch is function-references, so with the
	// zero-value Decoder this test read `gc: feature gate disabled` and reported a
	// disagreement about bytes. The gate-off behaviour is TestHeapTypeGatesFormsNotThePosition's
	// subject; the extent is this test's, and one instrument per question.
	d := &Decoder{Features: Features{
		ExceptionHandling: true, SIMD: true, Threads: true, Memory64: true,
		GC: true, TailCall: true, RelaxedSIMD: true, MultiMemory: true,
	}}
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
	if !t.Failed() { // guarded: see TestPrefixIllegalRenderingMatchesTheAuthority's closing note
		t.Logf("%d flat readers agree with their authority definitions", checked)
	}
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
	// Guarded: on failure the loop above has just named an immediate that is *not*
	// accounted for, and an unconditional "all accounted for" is a summary contradicting
	// the testimony above it — the log line being the thing a reviewer skims. Found by
	// sweeping for siblings after the same defect appeared in gatecensus_test.go;
	// inventory_test.go had already established the guard and the reasoning.
	if !t.Failed() {
		t.Logf("%d distinct immediates in the table, all accounted for", len(seen))
	}
}

// opKey is the {prefix, sub} pair the engine keys prefixed facts on (`dataRefOps`,
// `constPrefixedOps`), with prefix 0x00 meaning the single-byte space — `instrCtx.opID`'s
// convention, reused here so a test key and a production key are the same shape.
func opKey(prefix byte, sub uint32) [2]uint32 { return [2]uint32{uint32(prefix), sub} }

// infoFor resolves one key against the generated table, through whichever of the two dispatch
// structures owns it.
func infoFor(k [2]uint32) (opInfo, bool) {
	if k[0] == 0x00 {
		info, ok := opTable[k[1]]
		return info, ok
	}
	region, ok := prefixRegion(byte(k[0]))
	if !ok {
		return opInfo{}, false
	}
	info, ok := region[k[1]]
	return info, ok
}

// renderKey names a key the way the engine's errors do, for a failure message.
func renderKey(k [2]uint32) string {
	name := "unknown to the table"
	if info, ok := infoFor(k); ok {
		name = info.mnemonic
	}
	return opID{prefix: byte(k[0]), sub: k[1]}.String() + " (" + name + ")"
}

// constLegalOps is the whole const-legal space: the unconditional set, the set extended-const
// admits under its gate, and the prefixed arms — keyed by `opKey` across both spaces.
//
// It exists because three controls in this file were keyed on `constOps` alone, and once
// extended-const's six became const-legal-under-a-gate that key stopped naming the space and
// started naming a *sample of it* — the overfitting-applied-to-a-control failure #33 was widened
// to avoid, arriving by the back door as a new set rather than as a new opcode. The union is
// derived here once so a tenth const-legal opcode, whatever gate admits it, is covered by every
// walk below on arrival instead of by whichever ones remembered to mention it.
//
// # Grave #495: the widening's own type was the blind spot
//
// This returned `map[byte]bool` until #471, and the paragraph above is the reason that is worth a
// grave rather than a rename. 0006's **property 1** pre-registered coverage of *"every one of the
// 256 single-byte opcodes and every tracked multi-byte prefix"*, and this file's header claimed
// exactly that of every walk in it. The three walks keyed here covered 256 of 561 dispatchable
// arms, because a prefixed opcode was **unrepresentable in the return type** — so the missing 305
// were not omitted from a list a reader could audit, they were outside what the helper could say.
//
// A byte-keyed map cannot fail to mention a prefixed opcode in the way an enumeration can: there
// is nothing to leave out. *Scope controls to the space* is usually a rule about a domain that was
// enumerated too narrowly; here the domain was fixed by a type, which is the same defect with no
// site to point at and no diff that would have shown it. What is checkable, and what
// `TestConstLegalSpaceIsTheWholeDispatchableSpace` now checks, is the *count of arms the walks
// reach* against the count the table dispatches.
//
// The floor is not decoration. If any of the maps were emptied — or if a future refactor moved
// membership somewhere this function does not read — the walks it feeds would each pass by
// comparing nothing, which is the vacuity class and is invisible on a board by construction.
func constLegalOps(tb testing.TB) map[[2]uint32]bool {
	tb.Helper()

	want := len(constOps) + len(extendedConstOps) + len(constPrefixedOps)
	all := make(map[[2]uint32]bool, want)
	add := func(k [2]uint32, from string) {
		if all[k] {
			tb.Errorf("%s is in two const sets (%s is the second): an opcode that is both "+
				"unconditionally const-legal and gated makes the gate unobservable, because the "+
				"unconditional arm answers first (constLegal checks constOps before the gate)",
				renderKey(k), from)
		}
		all[k] = true
	}
	for b := range constOps {
		add(opKey(0x00, uint32(b)), "constOps")
	}
	for b := range extendedConstOps {
		add(opKey(0x00, uint32(b)), "extendedConstOps")
	}
	for k := range constPrefixedOps {
		add(k, "constPrefixedOps")
	}
	if len(all) != want {
		tb.Fatalf("union of the const sets has %d members, want %d: an overlap above is the "+
			"only way this happens and it must not be reached silently", len(all), want)
	}
	if len(all) < 22 {
		tb.Fatalf("the const-legal space has %d members; it had 13 when #109 landed (seven "+
			"unconditional, six extended-const) and 22 when #471 added the nine prefixed arms, "+
			"and a domain smaller than that means a set this helper reads has been emptied — "+
			"every walk keyed on it would then agree vacuously", len(all))
	}
	return all
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
	for k := range constLegalOps(t) {
		info, ok := infoFor(k)
		if !ok {
			continue // membership's problem, asserted separately
		}
		for _, im := range info.imms {
			if bytesFor(im) == nil {
				t.Errorf("const-legal %s has structural immediate %q: the extent "+
					"comparison cannot cover it, so it must not silently be excluded from one",
					renderKey(k), im)
			}
		}
	}
}

// TestConstSetIsASubsetOfTheAuthority is #33's property 1, in the direction the
// generated table can answer.
//
// The reverse direction — every const-legal opcode in the authority appears here — is
// not assertable *from this table*, because a decode table records existence and immediate
// shape and const-legality is a validation fact. Naming the direction was the point, and it
// still is; what was wrong for as long as this comment stood alone was the word choice one
// step out, "the authority": `decode.ml` is not the authority for a validation fact, and
// `valid.ml`'s `is_const` is (grave #495, and the header). The reverse direction is
// asserted, against that file, by `TestConstLegalSpaceMatchesIsConst`.
//
// END dropped out of the set the dissolution shrank: it was in `constExprOps` because
// that map carried immediate shapes and the terminator needed one, and it needed a
// documented layering exception here because the authority's `instr` calls a bare 0x0b
// `misplaced END opcode`. Now that the const set is a *predicate* rather than a
// dispatch table, END is not in it at all — it is a delimiter, handled by `block` and
// `expectEnd` — so the exception is gone rather than suppressed. The fact it stood on
// is still checked, by TestEveryReasonRowIsABlockDelimiter.
func TestConstSetIsASubsetOfTheAuthority(t *testing.T) {
	for k := range constLegalOps(t) {
		info, ok := infoFor(k)
		if !ok {
			t.Errorf("const-legal %s does not exist in the reference's table "+
				"(decode.ml has no arm for it): the decoder accepts an opcode the authority does "+
				"not define, which no assert_malformed vector can catch", renderKey(k))
			continue
		}
		if info.illegal {
			t.Errorf("const-legal %s is marked illegal by the authority "+
				"(decode.ml:%d): the decoder accepts what the reference rejects outright",
				renderKey(k), info.refLine)
		}
		if info.reason != "" {
			t.Errorf("const-legal %s is a reason arm in the authority (decode.ml:%d): "+
				"the reference defines it only to reject", renderKey(k), info.refLine)
		}
		if info.escape {
			t.Errorf("const-legal %s is a prefix escape (decode.ml:%d), not an instruction",
				renderKey(k), info.refLine)
		}
	}
}

// TestConstExprExtentMatchesTheAuthority is #33's property 2 — and the dissolution
// changed what it proves, so it is worth being exact about what it now covers.
//
// It *was* a differential between two independent copies of the immediate shapes:
// `constExprOps`' hand-written readers against the generated table's `imms`. The
// dissolution deleted one side. The decoder now reads immediates *through* the table,
// so a differential between the two would compare the table with itself — which is the
// vacuity failure wearing the previous version's clothes.
//
// What is left is genuinely independent and is the half that matters: `immBytes` is a
// third, hand-written, individually-cited set of readers (see its comment on being an
// enrolled witness), so this compares **the production path** against **the enrolled
// witness** over the same bytes. A wrong reader in instr.go's `imm` switch — the new
// place the fact lives — fails here.
//
// The input is sixteen 0x70 bytes, which is deliberate rather than arbitrary. 0x70 has
// its continuation bit clear, so it is a complete one-byte LEB in every width; it is a
// valid heaptype (funcref); and it is enough bytes for the widest fixed-size read
// (f64's eight, v128's sixteen). One input every const-legal reader accepts is what
// lets the comparison be over consumed extent rather than over error behaviour.
func TestConstExprExtentMatchesTheAuthority(t *testing.T) {
	const input = "\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70"

	space := constLegalOps(t)
	d := &Decoder{}
	c := &instrCtx{d: d}
	checked := 0
	for k := range space {
		info, ok := infoFor(k)
		if !ok {
			continue // membership's problem
		}

		mine := &reader{b: []byte(input)}
		if err := c.imms(mine, immsOpArg(k), info.imms); err != nil {
			t.Errorf("%s: the production reader failed on the shared input: %v", renderKey(k), err)
			continue
		}

		theirs := &reader{b: []byte(input)}
		for _, im := range info.imms {
			read := bytesFor(im)
			if read == nil {
				t.Fatalf("%s: structural immediate %q reached the extent comparison; "+
					"TestConstSetUsesNoStructuralImmediate should have caught this first", renderKey(k), im)
			}
			if err := read(d, theirs); err != nil {
				t.Errorf("%s: the enrolled witness's shape %v failed on the shared input at %q: %v",
					renderKey(k), info.imms, im, err)
				break
			}
		}

		if mine.off != theirs.off {
			t.Errorf("%s: extent disagreement — the production reader consumed %d bytes, "+
				"the enrolled witness for %v (decode.ml:%d) consumed %d\n\t"+
				"a wrong extent does not fail loudly: it shifts every subsequent byte, and the "+
				"error surfaces somewhere else entirely (#33, property 2)",
				renderKey(k), mine.off, info.imms, info.refLine, theirs.off)
		}
		checked++
	}
	if checked != len(space) {
		t.Errorf("compared %d of %d const-legal opcodes: an extent check that quietly skips "+
			"is a coverage claim it cannot support", checked, len(space))
	}
	t.Logf("%d const-legal opcodes agree on immediate extent", checked)
}

// immsOpArg is the opcode argument `imms` takes, mirroring the two production call sites: the byte
// itself on the single-byte path, and 0x00 from `prefixed`.
//
// The argument only reaches `structural`, and no const-legal opcode is structural — which is
// TestConstSetUsesNoStructuralImmediate's assertion, not this helper's assumption. Mirrored anyway,
// because a walk that computes its own argument for a production function is comparing the table
// against a call the engine does not make.
func immsOpArg(k [2]uint32) byte {
	if k[0] == 0x00 {
		return byte(k[1])
	}
	return 0x00
}

// TestEveryImmediateHasAProductionReader is the totality control on the new place the
// immediate fact lives.
//
// The dissolution moved immediate *reading* into instr.go's `imm` switch, whose default
// arm is an error rather than a skip — but a default nobody reaches proves nothing, and
// a switch missing an arm is only loud if something asks. This asks, over every
// immediate in the vocabulary rather than every immediate today's const set reaches:
// that is the same widening #33 forced on the rest of this file (*scope controls to the
// space*), and it is the control the old `constExprOps` era could not have, because
// eight opcodes never mentioned sixteen immediates.
//
// It asserts *dispatch and progress*, not extent — extent is the differential above.
//
// Two inputs, because no single one is benign for the whole vocabulary and pretending
// otherwise is how a totality check becomes a coverage claim it cannot support. All-0x70
// is a valid valtype/heaptype and a complete one-byte LEB; all-0x00 is what the four
// `vec` immediates need, since 0x70 as a vec *length* asks for 112 elements. Each
// immediate must succeed on at least one — which is a real claim (every immediate has
// *some* legal encoding) rather than a hedge, and it fails loudly if a reader rejects
// both.
//
// The first draft used one all-0x70 input and reported the three vec immediates as
// failures. The finding was the test's, not the decoder's — but it is the reason this
// comment names its inputs' properties instead of calling them "benign bytes".
func TestEveryImmediateHasAProductionReader(t *testing.T) {
	// Long enough for immLane16's worst case (32 bytes) with room to spare.
	inputs := []string{strings.Repeat("\x70", 64), strings.Repeat("\x00", 64)}

	vocab := map[imm]bool{}
	for _, region := range prefixRegions {
		for _, info := range region {
			for _, im := range info.imms {
				vocab[im] = true
			}
		}
	}
	if len(vocab) == 0 {
		t.Fatal("no immediates found in the generated table: a totality check over an empty " +
			"domain is total over nothing (the vacuity class, #29)")
	}

	c := &instrCtx{d: &Decoder{}}
	checked := 0
	for im := range vocab {
		if im == immBlock {
			// Structural: routed through instrCtx.structural, which needs a terminator no
			// flat input supplies. Covered by TestStructuralArmsAreExactlyTheBlockRows and
			// by the body-grammar tests, and excluded here *by name* rather than by the
			// reader happening to fail.
			continue
		}
		ok := false
		var errs []error
		for _, in := range inputs {
			r := &reader{b: []byte(in), eof: ErrPayloadEnd}
			err := c.imm(r, im)
			if errors.Is(err, errNoImmReader) {
				// The failure this test exists for, and it is distinguishable from a
				// reader that ran and rejected precisely because errNoImmReader is its own
				// sentinel rather than a shared one.
				t.Errorf("immediate %q has no arm in instrCtx.imm: %v\n\t"+
					"the generated table names a reader the production path cannot dispatch, so "+
					"an opcode carrying it is read with the wrong extent", im, err)
				ok = true // reported; do not double-report below
				break
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if r.off == 0 {
				t.Errorf("immediate %q consumed zero bytes: every immediate in the vocabulary "+
					"has a non-empty encoding, so a zero-width read is an arm that does nothing", im)
			}
			ok = true
			break
		}
		if !ok {
			t.Errorf("immediate %q rejected every input: %v\n\tone of all-0x70 or all-0x00 must "+
				"be a legal encoding, or this reader accepts nothing at all", im, errs)
		}
		checked++
	}
	if checked == 0 {
		t.Error("no immediates checked: see the vacuity note above")
	}
	t.Logf("%d immediates in the table, %d dispatched by the production reader", len(vocab), checked)
}

// TestStructuralArmsAreExactlyTheBlockRows is the tripwire on instr.go's one
// hand-written departure from the table.
//
// Four arms cannot be table-driven — block, loop, if, try_table recurse and then
// consume an END — and instr.go detects them by the presence of `immBlock` in a row's
// immediates rather than by an opcode list. This asserts that marker is exactly right:
// the set of rows carrying immBlock is the set of arms whose reference source contains
// both `instr_block s` and `end_ s`, derived from decode.ml rather than enumerated.
//
// A fifth structural arm upstream then fails *here*, loudly, instead of being read flat
// — which is the difference between a marker and a guess. Scoped to the space: every
// region, not just the single-byte table.
//
// **Every region, and every region's own authority.** A single hoisted `src` read the core pin
// for all 610 rows, which is armAuthority's grave: the atomics rows were compared against
// whatever the core decoder has at those line numbers.
func TestStructuralArmsAreExactlyTheBlockRows(t *testing.T) {
	// Read each authority once, keyed by the path the rows name — so a file no row cites is
	// never opened, and the count below says how many were.
	srcs := map[string][]string{}
	linesFor := func(prefix byte, info opInfo) []string {
		path := armAuthority(t, prefix, info)
		if l, read := srcs[path]; read {
			return l
		}
		l := strings.Split(testenv.RequireSpecRef(t, path), "\n")
		srcs[path] = l
		return l
	}

	marked, matched := 0, 0
	for prefix, region := range prefixRegions {
		for code, info := range region {
			lines := linesFor(prefix, info)
			hasBlock := false
			for _, im := range info.imms {
				if im == immBlock {
					hasBlock = true
				}
			}
			if hasBlock {
				marked++
			}
			// The arm's source text, from its cited line to the next arm head. The
			// citation is the table's own refLine, which TestImmBytesCitationsResolve's
			// sibling mechanism keeps honest for the immediates.
			if info.refLine < 1 || info.refLine > len(lines) {
				t.Errorf("%#02x %#x: %s does not resolve in a %d-line file",
					prefix, code, citeArm(prefix, info), len(lines))
				continue
			}
			arm := armText(lines, info.refLine-1)
			// An arm that recurses *and* terminates is structural. `instr_block` alone
			// is not enough and neither is `end_` alone — it is the pair that makes the
			// shape unreadable from a flat immediate list.
			isStructural := strings.Contains(arm, "instr_block s") && strings.Contains(arm, "end_ s")
			if isStructural {
				matched++
			}
			if isStructural != hasBlock {
				t.Errorf("%#02x %#x (%s, %s): the authority's arm is structural=%v "+
					"but the table's immBlock marker says %v\n\tarm: %s\n\t"+
					"instr.go keys its hand-written recursion off immBlock, so a disagreement "+
					"here means an arm is read with the wrong shape",
					prefix, code, info.mnemonic, citeArm(prefix, info), isStructural, hasBlock, arm)
			}
		}
	}
	// Vacuity, both halves: a comparison that found no structural arms at all would
	// agree perfectly with a table that marked none (#29, and the empty-set law).
	if marked == 0 || matched == 0 {
		t.Errorf("found %d immBlock rows and %d structural arms in the authority; both must be "+
			">0 or this agreement is between two empty sets", marked, matched)
	}
	// The domain's own size, printed: at one file this control is back to comparing the atomics
	// rows against the core decoder, which is the state it passed in.
	if len(srcs) < 2 {
		t.Errorf("resolved rows against %d authority file(s), want >=2 — every region's rows are "+
			"in scope, and they do not all come from one decoder", len(srcs))
	}
	if !t.Failed() {
		t.Logf("%d structural arms agreed between the table's immBlock marker and %d authorities",
			marked, len(srcs))
	}
}

// armText returns the reference arm beginning at lines[i], joined to the next arm head.
//
// A multi-line RHS is the norm for exactly the arms this file cares about, so reading
// one line would find `instr_block` in none of them.
func armText(lines []string, i int) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(lines[i]))
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "| ") || strings.HasPrefix(t, ")") {
			break
		}
		b.WriteString(" ")
		b.WriteString(t)
	}
	return b.String()
}

// TestEveryReasonRowIsABlockDelimiter is the tripwire ErrMisplacedOpcode's doc names.
//
// The two `error s pos "..."` arms at bdd7164 are 0x05 and 0x0b — precisely the two
// bytes `instr_block'` stops on without consuming (decode.ml:969) — so neither ever
// reaches the dispatch, and instr.go's reason branch is unreachable. That is a
// declared-and-tracked deferral rather than a silent one (the ErrTrailingData ruling),
// and this is what keeps the declaration true: a third reason arm upstream, on a byte
// that is *not* a delimiter, would be genuinely reachable and would need a real
// verdict rather than the placeholder.
//
// Derived from the table rather than asserted about 0x05 and 0x0b, so the control grows
// with the thing controlled.
func TestEveryReasonRowIsABlockDelimiter(t *testing.T) {
	delimiters := map[uint32]bool{opElse: true, opEnd: true}
	found := 0
	for prefix, region := range prefixRegions {
		for code, info := range region {
			if info.reason == "" {
				continue
			}
			found++
			if prefix != 0x00 || !delimiters[code] {
				t.Errorf("%#02x %#x (decode.ml:%d) reports %q and is not a block delimiter\n\t"+
					"instr.go's reason branch is declared unreachable on the grounds that every "+
					"such arm is a byte `block` stops on; this one is reachable and needs a real "+
					"verdict, not ErrMisplacedOpcode",
					prefix, code, info.refLine, info.reason)
			}
		}
	}
	// Vacuity: zero reason rows would make the claim above true by asking nothing, and
	// would itself be a sign the extractor stopped recognising the arms.
	if found != 2 {
		t.Errorf("found %d reason rows, want 2 (0x05 misplaced ELSE, 0x0b misplaced END at "+
			"bdd7164): a change here is a real change in the authority, not a nit", found)
	}
}

// TestEveryNonConstByteGetsTheRightVerdict is #33's property 3, and it is the
// dissolution's measure of done.
//
// The previous version of this test asserted every one of the 248 non-const bytes
// produced `ErrNonConstantExpr` — one error for four different situations — and pinned
// the partition as a *comment on future work*: `present` was the bucket that should
// report `constant expression required` from a layer above, `absent`+`illegal` the
// genuinely malformed ones, `escape` the ones needing a sub-table walk.
//
// That work is this change, so the partition stops being a promise and becomes the
// assertion. The counts are unchanged and still stamped — they are a claim about the
// authority at the pinned revision — but each bucket now asserts its own *verdict*,
// which is what makes this a partition check rather than `errors.Is` 248 times (#34:
// when a partition's members share an error value, errors.Is is not a partition check).
//
// The two reason rows are not in any bucket, and that is the fifth verdict the previous
// count folded into `present`: 0x05 and 0x0b are delimiters, so `decodeConstExpr`
// *accepts* a bare one — as an empty expression — rather than rejecting it. See
// TestEveryReasonRowIsABlockDelimiter.
//
// **`present` fell 185 → 179 when extended-const's gate landed** (#109), and the direction is
// the point: this walk runs with every gate *on*, so the six opcodes extended-const admits are
// const-legal here and the `released` half rightly stopped reporting `constant expression
// required` for them. They leave the `present` bucket for the same reason `constOps`' seven were
// never in it — under this decoder they are legal — and the excluded set is therefore
// `constOps ∪ extendedConstOps` rather than `constOps`. This test failed exactly here when the
// gate was added, which is the partition control doing its job: a bucket whose membership rule
// changed said so instead of absorbing six members silently.
func TestEveryNonConstByteGetsTheRightVerdict(t *testing.T) {
	// Every gate on. Ten of the non-const bytes are *also* gated — throw, throw_ref,
	// the tail calls, the function-references five, ref.eq — so on v0's gates-off posture
	// the feature decline outranks the const verdict (0008) and this walk would score ten
	// members against the wrong question. The gated verdict has its own controls in
	// gatemap_test.go; this one is about const-ness over the whole space.
	//
	// It is also why extended-const's six are skipped below rather than bucketed: with its gate
	// on they are legal, and *this* decoder has it on. The gate-off direction — where the same
	// six must be declined by name and must not report `constant expression required` — is
	// TestExtendedConstGateIsPositional's, because a walk with all gates on cannot ask it.
	d := constVerdictDecoder(t)
	c := &instrCtx{d: d}
	var absent, escape, illegal, present, delimiter, released int
	for b := range 256 {
		if constOps[byte(b)] || extendedConstOps[byte(b)] {
			continue
		}

		// A one-byte image holding just this opcode, then nothing. The verdict must come
		// from the opcode itself — but note that a real instruction *with* immediates
		// runs out of bytes, so the expected error depends on the bucket and the test
		// says which rather than accepting any error.
		r := &reader{b: []byte{byte(b)}, eof: ErrPayloadEnd}
		err := constExprErr(d, r)

		info, ok := opTable[uint32(b)]
		switch {
		case !ok, ok && info.illegal:
			// Genuinely malformed: no arm at all, or an arm that exists to reject.
			// Rendered as the reference renders it, lowercase two-digit hex.
			if !errors.Is(err, ErrIllegalOpcode) {
				t.Errorf("%#02x: want ErrIllegalOpcode, got %v", b, err)
			}
			if want := "illegal opcode " + hex2(byte(b)); err != nil && !strings.Contains(err.Error(), want) {
				t.Errorf("%#02x: error %q does not contain %q — `string_of_byte` is %%02x "+
					"(decode.ml:35) and binary.wast:1218 puts the byte inside the expected "+
					"string, so the rendering is oracle-covered (#38)", b, err, want)
			}
			if ok {
				illegal++
			} else {
				absent++
			}

		case info.reason != "":
			// A delimiter: `block` stops without consuming and `expectEnd` judges it.
			// 0x0b is a well-formed empty expression; 0x05 is not END, so it is
			// `END opcode expected`.
			switch b {
			case opEnd:
				if err != nil {
					t.Errorf("%#02x: a bare END is an empty constant expression, got %v", b, err)
				}
			default:
				if !errors.Is(err, ErrEndExpected) {
					t.Errorf("%#02x: `block` stops on it without consuming, so `end_` judges it; "+
						"want ErrEndExpected, got %v", b, err)
				}
			}
			delimiter++

		case info.escape:
			// The prefix reads a u32 sub-opcode and the one-byte image has none, so the
			// honest verdict is the truncation, not a guess at the sub-opcode. The
			// sub-table walk itself is TestPrefixedOpcodeVerdicts'.
			if !errors.Is(err, ErrPayloadEnd) {
				t.Errorf("%#02x is a prefix escape with no sub-opcode in the image; want "+
					"ErrPayloadEnd, got %v", b, err)
			}
			escape++

		default:
			// A real instruction that is simply not constant — and the *interesting*
			// bucket, because the const verdict is deferred. On this one-byte image the
			// grammar never completes: `block` peeks past the opcode, finds nothing, and
			// `end_` reports the truncation. So the answer here is ErrPayloadEnd for every
			// member, and that is not an evasion — it is binary.wast:112's rule, asserted
			// on 185 bytes instead of one. A reader that aborted at the non-const
			// instruction would report ErrConstExprRequired here and fail this half.
			if !errors.Is(err, ErrPayloadEnd) {
				t.Errorf("%#02x (%s): a bare non-const opcode leaves the expression unterminated, "+
					"so the malformed verdict wins; want ErrPayloadEnd, got %v\n\t"+
					"an invalid verdict that pre-empts a malformed one is reporting the wrong "+
					"layer's answer (binary.wast:112)", b, info.mnemonic, err)
			}

			// The other half: terminate the expression properly and the deferred verdict
			// is released. Without this, the assertion above is satisfied by a reader that
			// *never* reports non-constness at all.
			if img, ok := wellFormedExpr(c, byte(b), info); ok {
				err := constExprErr(d, &reader{b: img, eof: ErrPayloadEnd})
				if !errors.Is(err, ErrConstExprRequired) {
					t.Errorf("%#02x (%s): % x is a well-formed expression containing a non-const "+
						"instruction; want ErrConstExprRequired, got %v", b, info.mnemonic, img, err)
				}
				if err != nil && strings.Contains(err.Error(), "malformed") {
					t.Errorf("%#02x (%s): error %q says malformed for a module the spec calls "+
						"well-formed — the accept-direction failure §9 G-3 names", b, info.mnemonic, err)
				}
				released++
			}
			present++
		}
	}

	// Vacuity on the released half specifically. `wellFormedExpr` declines when neither
	// fill byte makes a legal immediate, and a decline is invisible: if it declined for
	// every opcode, the loop above would assert only the truncation half and this test
	// would certify a reader that never releases the deferred verdict at all. The floor
	// is a fraction of the bucket rather than a count, so it survives the table growing.
	if released*2 < present {
		t.Errorf("released the deferred verdict for only %d of %d non-const instructions: "+
			"wellFormedExpr declined too often for this to be a claim about the bucket", released, present)
	}
	t.Logf("%d non-const instructions, %d with a well-formed expression built for them", present, released)

	// Stamped, not deduced. The numbers are a claim about the authority at the pinned
	// revision — first *reasoned* as 40 illegal and 170 present, and both were wrong,
	// 40 being the illegal count across all four regions rather than the single-byte
	// one. Printed, not deduced.
	//
	// `present` moved from 186 to 185 and `delimiter` is new: the previous partition
	// counted 0x05 in `present` (the loop skipped only the eight members of the old
	// const set, which included 0x0b but not 0x05) and had no bucket for a byte that
	// is neither instruction nor rejection. Same 256 bytes, one more honest column.
	//
	// `absent` 38 → 37 and `escape` 3 → 4 with the atomics region: 0xfe moved from "no arm in
	// decode.ml" to "dispatch to a sub-table", one byte crossing between two buckets. The sum is
	// unchanged, which is the property that makes this partition worth pinning as five figures
	// rather than as a total — a total cannot see a byte change verdict, and this delta is
	// nothing but a byte changing verdict.
	const (
		wantAbsent    = 37  // no arm in decode.ml: the catch-all's territory
		wantEscape    = 4   // 0xfb, 0xfc, 0xfd, 0xfe: dispatch to a sub-table
		wantIllegal   = 21  // an arm that explicitly rejects
		wantPresent   = 179 // a real instruction that is simply not constant
		wantDelimiter = 2   // 0x05, 0x0b: `block` stops on them
	)
	if absent != wantAbsent || escape != wantEscape || illegal != wantIllegal ||
		present != wantPresent || delimiter != wantDelimiter {
		t.Errorf("verdict partition changed: absent=%d escape=%d illegal=%d present=%d "+
			"delimiter=%d, want %d/%d/%d/%d/%d",
			absent, escape, illegal, present, delimiter,
			wantAbsent, wantEscape, wantIllegal, wantPresent, wantDelimiter)
	}
	// The coverage sum takes both const sets, matching the loop's skip. Two independent
	// oversights are caught by keeping this in step: a member of one set going missing (the
	// buckets would over-count) and the skip and the sum disagreeing (256 would not be hit).
	if got := absent + escape + illegal + present + delimiter + len(constOps) + len(extendedConstOps); got != 256 {
		t.Errorf("partition does not cover the space: %d of 256 bytes classified", got)
	}
}

// wellFormedExpr builds `<opcode> <immediates> 0x0B` for one opcode, or declines.
//
// The immediates are produced by *measuring* the production reader against a fill of
// identical bytes and keeping exactly what it consumed — not by knowing each
// immediate's width. That direction matters: a helper that encoded widths itself would
// be a fourth copy of the fact this whole file exists to keep from being duplicated, and
// it would agree with a wrong reader by construction. Measuring instead means this
// helper is correct whenever the reader is self-consistent, and the reader's correctness
// is the differential's job, not this one's.
//
// It declines — rather than guessing — when neither fill byte yields a complete read
// (structural arms, and any immediate for which 0x70 and 0x00 are both illegal). The
// decline is counted by the caller, because a helper that silently declines everywhere
// would empty the assertion it feeds.
func wellFormedExpr(c *instrCtx, op byte, info opInfo) ([]byte, bool) {
	for _, fill := range []byte{0x00, 0x70} {
		buf := make([]byte, 64)
		for i := range buf {
			buf[i] = fill
		}
		r := &reader{b: buf, eof: ErrPayloadEnd}
		if err := c.imms(r, op, info.imms); err != nil {
			continue
		}
		img := append([]byte{op}, buf[:r.off]...)
		return append(img, opEnd), true
	}
	return nil, false
}

// hex2 renders a byte as the reference's `string_of_byte` does: `Printf.sprintf "%02x"`
// (decode.ml:35). Written out rather than reusing the production formatter, because a
// test that formats with the code under test cannot catch the code under test.
func hex2(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0x0F]})
}

// TestPrefixedOpcodeVerdicts walks the sub-tables, which the single-byte sweep above
// reaches only as far as the escape.
//
// Three regions, three renderings, and they do **not** agree: 0xfb and 0xfc fall
// through to `illegal2` (prefix *and* sub-opcode), 0xfd to plain `illegal` (sub-opcode
// alone). No vector in the phase-1 corpus reaches any of it — the corpus contains no
// `\fb` or `\fd` byte at all — so this is print-check territory, which is precisely
// where the invented-bits class (grave #36) hides.
//
// Scoped to the space: every region in prefixRegions, and for each one both a known
// arm and a sub-opcode above its maximum.
func TestPrefixedOpcodeVerdicts(t *testing.T) {
	d := &Decoder{}
	for prefix, region := range prefixRegions {
		if prefix == 0x00 {
			continue
		}
		// A sub-opcode past every arm in this region: unknown, so the fallthrough.
		var highest uint32
		for code := range region {
			highest = maxU32(highest, code)
		}
		unknown := highest + 1

		var img []byte
		img = append(img, prefix)
		img = append(img, ulebBytes(unknown)...)
		r := &reader{b: img, eof: ErrPayloadEnd}
		err := constExprErr(d, r)
		if !errors.Is(err, ErrIllegalOpcode) {
			t.Errorf("%#02x %#x (unknown sub-opcode): want ErrIllegalOpcode, got %v", prefix, unknown, err)
			continue
		}
		want := "illegal opcode " + hex2(prefix) + " " + hex2x(unknown)
		if !twoFieldIllegal[prefix] {
			want = "illegal opcode " + hex2x(unknown)
		}
		if got := err.Error(); !strings.Contains(got, want) {
			t.Errorf("%#02x %#x: error %q does not contain %q\n\t"+
				"0xfb/0xfc fall through to illegal2 (prefix and sub-opcode, decode.ml:655,681) "+
				"and 0xfd to plain illegal (sub-opcode alone, :961) — the three regions disagree "+
				"and no suite vector covers any of them", prefix, unknown, got, want)
		}
	}
}

// TestIllegalOpcodeRenderings is the print-check, and it is written against **literal
// strings** on purpose.
//
// Every other rendering assertion in this file builds its expectation with hex2/hex2x,
// which are this package's reimplementations of the reference's `string_of_byte` and
// `string_of_multi`. Those helpers are independent of the production formatter, which is
// what makes those assertions worth making — but they are not independent of *each
// other's idea of hex*, and a shared misunderstanding (uppercase, a `0x` prefix, a wrong
// minimum width) would be invisible to all of them at once.
//
// So these five expectations are typed out. One of them, `illegal opcode ff`, is
// verbatim from binary.wast:1218 and therefore oracle-covered; the other four are the
// shapes the oracle never sees, which per #38 is where a print-check earns the most. The
// t.Log is not decoration: grave #36 was found by printing what the formatter returned,
// not by reading the expression, and the renderings that no vector reaches deserve to be
// visible in the log for the same reason.
func TestIllegalOpcodeRenderings(t *testing.T) {
	for _, tc := range []struct {
		what string
		got  error
		want string
	}{
		// cited binary.wast:1218 — `illegal opcode ff`, the byte inside the sentinel.
		{"the one oracle-covered rendering", illegalOpcode(0xFF), "illegal opcode ff"},
		// synthetic: the low boundary, where a %02x that lost its zero-padding would
		// print `illegal opcode 0` and no vector would notice.
		{"a byte needing zero-padding", illegalOpcode(0x00), "illegal opcode 00"},
		{"a byte whose hex has a letter", illegalOpcode(0x0F), "illegal opcode 0f"},
		// synthetic: illegal2's two fields, which no phase-1 vector reaches — the corpus
		// contains no \fb byte at all.
		{"illegal2, two fields (0xfb region)", illegalPrefixed(0xFB, 0x20), "illegal opcode fb 20"},
		// synthetic: 0xfd falls through to plain illegal, printing the sub-opcode alone.
		{"plain illegal from a prefix region (0xfd)", illegalPrefixed(0xFD, 0x9A), "illegal opcode 9a"},
	} {
		got := tc.got.Error()
		t.Logf("%-42s %q", tc.what, got)
		if got != tc.want {
			t.Errorf("%s: rendered %q, want %q\n\t"+
				"the reference's formatters are `%%02x` for a byte (decode.ml:35) and `%%02lx` "+
				"for a multi-byte opcode (:36) — lowercase, no prefix, minimum two digits",
				tc.what, got, tc.want)
		}
	}
}

// TestPrefixIllegalRenderingMatchesTheAuthority derives twoFieldIllegal from decode.ml
// instead of trusting the map.
//
// twoFieldIllegal is hand-written — it has to be, because the extractor deliberately
// skips fallthrough arms (they bind a variable, not an opcode), so the fact is not in
// the generated table. That makes it an enrolled witness, and this is the enrolment: the
// authority's own text decides, and the map is checked against it over every region in
// prefixRegions rather than over the entries the map happens to have.
//
// # Which authority, which is the half this got wrong
//
// It read one file — the core pin's decode.ml — for every region, and that was right while
// every region came from one pin. The atomics region arrived from the threads pin and the
// control reported `0xfe: no fallthrough arm found in decode.ml after line 857`: a derivation
// failing, correctly, over a file that does not define its subject. The fact it needed was
// already in optable.go's *header comment* and nowhere a control could read it, which is why
// `regionAuthority` is now generated data. Per region, no default.
//
// The answer for 0xfe, once it read the right file, is that the region does *not* use
// `illegal2`: its fallthrough is `| b -> illegal s (pos + 1) b`, because the threads pin's
// baseline predates the two-field rendering. That is a difference in refusal text between
// this region and 0xfb, it has no vector behind it — `atomic.wast` has 0 assert_malformed
// rows — and it is the reject direction, so following the authority per region is the
// conservative reading. It is *not* the sub-opcode-width question wearing a different hat:
// that one changed which instruction a legal module decodes to, and this one changes only
// what a rejected module is told. Flagged in the report either way, because "follow the
// snapshot here, the standard there" is a distinction a principal should get to see.
func TestPrefixIllegalRenderingMatchesTheAuthority(t *testing.T) {
	checked := 0
	for prefix, region := range prefixRegions {
		if prefix == 0x00 {
			continue
		}
		// The region's own decoder, generated beside the region's arms. A single `src` hoisted
		// out of this loop is exactly the bug this control had.
		path, declared := regionAuthority[prefix]
		if !declared {
			t.Errorf("%#02x has a region but no entry in regionAuthority: the generated "+
				"provenance map covers every region the table has, so this means the table and "+
				"the map came from different runs — regenerate with: make opcodes", prefix)
			continue
		}
		src := testenv.RequireSpecRef(t, filepath.Join("..", "..", path))
		lines := strings.Split(src, "\n")
		// The region's extent: from its escape row's line to the last arm's, then on to
		// the fallthrough that closes the nested match.
		info, ok := opTable[uint32(prefix)]
		if !ok || !info.escape {
			t.Errorf("%#02x has a region but no escape row in the single-byte table", prefix)
			continue
		}
		var last int
		for _, sub := range region {
			last = max(last, sub.refLine)
		}
		want, found := false, false
		for i := last; i < len(lines) && i < last+40; i++ {
			l := strings.TrimSpace(lines[i])
			if !strings.HasPrefix(l, "| n ->") && !strings.HasPrefix(l, "| b ->") {
				continue
			}
			found = true
			want = strings.Contains(l, "illegal2")
			break
		}
		if !found {
			t.Errorf("%#02x: no fallthrough arm found in decode.ml after line %d; the region's "+
				"rejection shape cannot be derived, so twoFieldIllegal[%#02x] is unchecked",
				prefix, last, prefix)
			continue
		}
		if got := twoFieldIllegal[prefix]; got != want {
			t.Errorf("%#02x: twoFieldIllegal says %v, decode.ml's fallthrough after line %d says "+
				"%v (illegal2 prints prefix and sub-opcode; illegal prints the sub-opcode alone)",
				prefix, got, last, want)
		}
		checked++
	}
	if checked == 0 {
		t.Error("no prefix regions checked: a derivation over no regions derives nothing")
	}
	// Guarded, and it was not: falsifying the 0xfe entry printed the disagreement and then
	// "4 prefix regions' rejection renderings agree" underneath it. `checked` counts regions
	// *derived*, which is the vacuity guard and is right to include a region that disagreed —
	// but the sentence claimed agreement. Same shape as TestCensusRowsAreWellFormed's guarded
	// summary, found the same way: by reading a falsification's output rather than its exit
	// code.
	if !t.Failed() {
		t.Logf("%d prefix regions' rejection renderings agree with their own pin's decode.ml", checked)
	}
}

func maxU32(a, b uint32) uint32 { return max(a, b) }

// hex2x renders a u32 as the reference's `string_of_multi` does: `"%02lx"`
// (decode.ml:36) — at least two digits, more when the value needs them.
func hex2x(v uint32) string {
	const digits = "0123456789abcdef"
	var out []byte
	for v > 0 {
		out = append([]byte{digits[v&0x0F]}, out...)
		v >>= 4
	}
	for len(out) < 2 {
		out = append([]byte{'0'}, out...)
	}
	return string(out)
}

// ulebBytes encodes v as an unsigned LEB128. A test helper, kept minimal: the readers
// are what is under test, so the writer must not share code with them.
func ulebBytes(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

// TestPrefixRegionsCoverTheTable keeps prefixRegions from silently missing a region.
//
// The single-byte table's prefix escapes are facts *in* the table: each has an arm with no
// mnemonic and no immediates, because the reference handles them with a nested match rather than
// a flat arm. So the set of regions is derivable, and deriving it is what makes a new prefix
// landing upstream a failure here instead of an uncovered region (*derive the domain, never
// enumerate it*).
//
// This doc said "three prefix escapes … a fourth prefix landing upstream" and both numbers were
// spent when 0xfe landed. The *check* was fine — it derives, and it is what demanded the 0xfe
// entry in prefixRegions — so the counts were decoration that went stale silently. De-enumerated
// rather than bumped to four: a comment that counts a derived set has to be re-edited every time
// the set grows, and the one thing it can do in the meantime is mislead.
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
			// Region 0x00, not `p`: an escape arm *lives in* the single-byte table and merely
			// names the region it dispatches to, so its citation resolves against the
			// single-byte region's authority unless the row carries its own refPath — which
			// is exactly what the 0xfe row does, and exactly what grave #529 got wrong.
			t.Errorf("prefix %#02x has a region but its single-byte arm is not marked as an "+
				"escape (%s)", p, citeArm(0x00, info))
		}
	}
	// And the reverse: a prefix escape with no region would be a dispatch the decoder
	// cannot follow. Keyed on the escape field rather than on the *absence* of every
	// other field — inferring "this must be an escape because it has nothing else" is
	// how the escapes went missing in the first place, and a fact worth checking is a
	// fact worth recording (Arm.Escape).
	escapes, crossRegion := 0, 0
	for code, info := range opTable {
		if !info.escape {
			continue
		}
		escapes++
		if _, ok := prefixRegions[byte(code)]; !ok {
			t.Errorf("%#02x is a prefix escape (%s) but prefixRegions has no "+
				"table for it: a dispatch the decoder cannot follow", code, citeArm(0x00, info))
		}
		// And the citation itself, resolved: the arm at the cited line must be the dispatch on
		// this very byte. Here rather than anywhere else because the escape rows are *the*
		// cross-region rows — an escape lives in the single-byte table and is read from the
		// decoder that defines the region it jumps to, so 0xfe's row is the only one in the
		// table carrying its own refPath, and this is the only assertion whose verdict changes
		// when armAuthority stops reading that field. Stripping the refPath branch left every
		// test in this package green (grave #529's repair, unwatched dying) until this walk.
		if info.refPath != "" {
			crossRegion++
		}
		lines := strings.Split(testenv.RequireSpecRef(t, armAuthority(t, 0x00, info)), "\n")
		if info.refLine < 1 || info.refLine > len(lines) {
			t.Errorf("%#02x is a prefix escape whose citation %s does not resolve in a %d-line file",
				code, citeArm(0x00, info), len(lines))
			continue
		}
		if arm := armText(lines, info.refLine-1); !strings.Contains(arm, fmt.Sprintf("%#02x", code)) {
			t.Errorf("%#02x is a prefix escape citing %s, and that arm does not mention %#02x\n\t"+
				"arm: %s\n\tthe row names the wrong file or the wrong line, so an auditor reading "+
				"the citation is handed some other proposal's opcode", code, citeArm(0x00, info), code, arm)
		}
	}
	// A cross-region row is what refPath exists for; at zero, the field is emitted by nobody and
	// read by nobody, and armAuthority is back to one authority per region (grave #529's state).
	if crossRegion == 0 {
		t.Error("no escape row carries a refPath: the composed table has a region read from an " +
			"overlay pin, so its escape arm's citation is a line in that pin's decoder — regenerate " +
			"with: make opcodes")
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
// actually enables. That sentence was written as a prophecy and #109 is its subject
// arriving: extended-const's six are const-legal only under a gate, so this test has
// stopped proving invariance and started covering variation, which is what it was filed
// for. *A pre-registered control's value is collected when its subject shows up*, and the
// collection is two edits — the walk's domain and the mask's width.
//
// **The mask is now reflected over `Features`, and the previous version's claim that four
// booleans were "the same derivation without a dependency" was false.** It walked
// ExceptionHandling, SIMD, Threads, Memory64 — four of what were then eight gates — so
// GC, TailCall, RelaxedSIMD and MultiMemory were pinned *off* in all sixteen
// configurations and the count `16` read as complete. An enumeration is a sample and a
// sample has a blind spot by construction; here the blind spot was half the struct, and
// the comment asserting otherwise is why nobody looked. Reflection makes a ninth gate widen
// the walk to 512 rather than leaving it at 16 with one more field silently held low.
//
// Grave #114. The lesson is that an enumeration wearing a *derivation's* description is
// worse than a bare enumeration, because the description defeats the review that would have
// caught it: print the domain's size, never read the sentence claiming it was derived.
func TestAgreementHoldsUnderEveryFeatureConfiguration(t *testing.T) {
	const input = "\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70\x70"

	gates := featureGateIDs(t)
	space := constLegalOps(t)
	want := 1 << len(gates)

	configs := 0
	for mask := range want {
		var f Features
		v := reflect.ValueOf(&f).Elem()
		for i, g := range gates {
			fld := v.FieldByName(string(g))
			if !fld.IsValid() || fld.Kind() != reflect.Bool {
				t.Fatalf("Features.%s is not a settable bool: a gate this walk cannot vary "+
					"would run as off in every configuration while the count claims coverage", g)
			}
			fld.SetBool(mask&(1<<i) != 0)
		}
		d := &Decoder{Features: f}
		c := &instrCtx{d: d}
		for k := range space {
			info, ok := infoFor(k)
			if !ok {
				continue
			}
			mine := &reader{b: []byte(input)}
			if err := c.imms(mine, immsOpArg(k), info.imms); err != nil {
				t.Errorf("%+v: %s failed: %v", f, renderKey(k), err)
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
				t.Errorf("%+v: %s extent disagreement: %d vs %d",
					f, renderKey(k), mine.off, theirs.off)
			}
		}
		configs++
	}
	if configs != want {
		t.Fatalf("walked %d configurations, want %d (2^%d tracked gates): a per-configuration "+
			"claim must actually walk them", configs, want, len(gates))
	}
	t.Logf("%d configurations × %d const-legal opcodes agree on immediate extent",
		configs, len(space))
}
