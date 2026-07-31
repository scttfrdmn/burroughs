// Package binary decodes the WebAssembly binary format.
//
// v0 scope: module preamble and section-level scan (contract phase v0).
// Section payloads are held opaque here; per-section decoding lands
// section-by-section with tests (see CLAUDE.md, Immediate queue).
package binary

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Magic is the module preamble "\0asm".
var Magic = [4]byte{0x00, 0x61, 0x73, 0x6D}

// Version is the binary format version Burroughs accepts.
const Version uint32 = 1

// Error text tracks the upstream suite's assert_malformed strings verbatim
// (decision 0003): the suite's strings are the decoder's error contract, and
// the harness matches on substring. Do not reword these without changing the
// tests that pin them to spec vectors.
var (
	ErrBadMagic   = errors.New("magic header not detected")
	ErrBadVersion = errors.New("unknown binary version")
	ErrTruncated  = errors.New("unexpected end")

	// ErrLEBTooLong is the continuation-bit case: more bytes follow than the
	// target width permits.
	ErrLEBTooLong = errors.New("integer representation too long")
	// ErrLEBOverflow is the unused-bits case: the final byte sets bits beyond
	// the target width.
	ErrLEBOverflow = errors.New("integer too large")

	// ErrSectionOverrun is a declared section size that runs past the end of the
	// module image. The suite calls this "length out of bounds" (binary.wast,
	// custom.wast); "unexpected content after last section" is a *different*
	// condition (duplicate/misordered sections) and is not what this reports.
	ErrSectionOverrun = errors.New("length out of bounds")

	// ErrTrailingData names the duplicate/misordered-section condition. The
	// suite's string reads oddly for a misordered section, but it is what the
	// spec's own grammar implies: sections are matched in a fixed order, so a
	// section out of place is unmatched *content* after the last section the
	// grammar could consume. 23 vectors in binary.wast assert it (#6).
	ErrTrailingData = errors.New("unexpected content after last section")

	// ErrMalformedSectionID is a section id with no place in the grammar. Not
	// separable from order enforcement: ranking sections requires knowing which
	// ids have a rank, so the lookup that orders them is the lookup that
	// validates them.
	ErrMalformedSectionID = errors.New("malformed section id")

	// ErrFuncCodeMismatch is a function section whose entry count disagrees with
	// the code section's body count, in either direction, including one present
	// and the other absent.
	ErrFuncCodeMismatch = errors.New("function and code section have inconsistent lengths")

	// ErrDataCountMismatch is a data count section disagreeing with the number of
	// segments the data section actually carries.
	ErrDataCountMismatch = errors.New("data count and data section have inconsistent lengths")

	// ErrPayloadEnd is the payload grammar wanting a byte the image does not
	// have. It is face 2 of the size mechanism (see sections.go).
	//
	// Note the relationship to ErrTruncated: "unexpected end" is a *substring* of
	// this text, and the harness matches by substring, so this error satisfies
	// both the vectors expecting the long form and the three custom.wast vectors
	// expecting the short one. That containment is the suite's, not a convenience
	// — it is why a payload-level truncation must never be reported as the bare
	// preamble-level ErrTruncated, which would fail the long-form vectors while
	// looking correct on the short ones.
	ErrPayloadEnd = errors.New("unexpected end of section or function")

	// ErrSectionSizeMismatch is a section whose grammar consumed a different
	// number of bytes than its header declared. Face 3, and the two-signed one:
	// grammar-short and grammar-long are both this error.
	ErrSectionSizeMismatch = errors.New("section size mismatch")

	// The malformed-form errors of the payload grammars. Each names the byte it
	// rejected, because "malformed limits flags" alone does not say which flags.
	ErrMalformedFuncType   = errors.New("malformed function type")
	ErrMalformedValType    = errors.New("malformed value type")
	ErrMalformedRefType    = errors.New("malformed reference type")
	ErrMalformedLimits     = errors.New("malformed limits flags")
	ErrMalformedMutability = errors.New("malformed mutability")
	ErrMalformedImportKind = errors.New("malformed import kind")
	ErrMalformedExportKind = errors.New("malformed export kind")

	// ErrMalformedUTF8 is a name whose bytes are not well-formed UTF-8.
	//
	// The spec's `name` production is `utf8(char*)` where char is a Unicode scalar
	// value, so the constraint is a property of the *encoding*, not a list of
	// rejected byte patterns: overlong forms, unpaired surrogates, code points
	// above U+10FFFF, truncated and stray continuation bytes are all the same
	// violation of the same rule. utf8.Valid is that rule.
	ErrMalformedUTF8 = errors.New("malformed UTF-8 encoding")

	// ErrFeatureDisabled is a well-formed construct from a gated proposal met
	// with its gate off. Deliberately *not* a suite malformed-string: the module
	// is well-formed and the spec would accept it, so claiming otherwise would be
	// the gate manufacturing malformedness (CLAUDE.md). Callers wrap it with the
	// feature's name via featureErr.
	ErrFeatureDisabled = errors.New("feature gate disabled")

	// ErrDataCountRequired is memory.init or data.drop appearing without a data
	// count section.
	//
	// Declared and tracked, not silent (the ruling in CLAUDE.md): deciding this
	// requires knowing whether those opcodes occur inside a function body, and a
	// byte-scan for `fc 08` would false-positive on any immediate that happens to
	// hold those bytes — a decoder rejecting valid modules is worse than one
	// missing an invalid one. Reachable when function bodies are decoded (#22).
	ErrDataCountRequired = errors.New("data count section required")

	// The malformed-form errors of the const-expression grammars (#25).
	ErrMalformedElemFlags = errors.New("malformed element segment flags")
	ErrMalformedElemKind  = errors.New("malformed element kind")
	ErrMalformedDataFlags = errors.New("malformed data segment flags")

	// ErrNonConstantExpr is a byte inside a constant expression that the constexpr
	// reader does not accept.
	//
	// Deliberately not a suite malformed-string, and the reason is the same shape as
	// ErrFeatureDisabled's. Two cases reach it — a byte that is no opcode at all
	// (malformed, "illegal opcode") and a real opcode that is not constant (invalid,
	// "constant expression required", which the suite asserts 22 times and always as
	// assert_invalid) — and distinguishing them needs the existence question answered
	// over the whole opcode space, which is #7's table.
	//
	// So the module is rejected (accept-and-ignore would break the extent, and every
	// size check downstream with it) while the error claims only what this layer
	// knows. The consequence is on the board and stays there: binary.wast:345 wants
	// "illegal opcode" and keeps failing until #7 lands. Spoofing the string would
	// buy that vector and report malformed for `(global f32 (f32.neg (f32.const 0)))`
	// — the accept direction the suite cannot catch. See #22's ruling and §9 G-3.
	ErrNonConstantExpr = errors.New("constexpr: opcode not in the constant subset")
)

// SectionID identifies a module section (Wasm 3.0 numbering).
type SectionID byte

const (
	SectionCustom    SectionID = 0
	SectionType      SectionID = 1
	SectionImport    SectionID = 2
	SectionFunction  SectionID = 3
	SectionTable     SectionID = 4
	SectionMemory    SectionID = 5
	SectionGlobal    SectionID = 6
	SectionExport    SectionID = 7
	SectionStart     SectionID = 8
	SectionElement   SectionID = 9
	SectionCode      SectionID = 10
	SectionData      SectionID = 11
	SectionDataCount SectionID = 12
	SectionTag       SectionID = 13 // exception handling (Wasm 3.0)
)

func (id SectionID) String() string {
	names := [...]string{
		"custom", "type", "import", "function", "table", "memory",
		"global", "export", "start", "element", "code", "data",
		"datacount", "tag",
	}
	if int(id) < len(names) {
		return names[id]
	}
	return fmt.Sprintf("unknown(%d)", byte(id))
}

// sectionRank orders the non-custom sections as the spec's module grammar
// matches them. Custom sections have no rank: they are permitted anywhere, any
// number of times, so they never participate in the ordering check.
//
// This is deliberately *not* SectionID order, and the difference is the whole
// reason it is a table. The data count section is id 12 but the grammar places
// it between element (9) and code (10) — `binary.wast:1194` asserts that a code
// section followed by a data count section is malformed, which a decoder ranking
// by id would happily accept. Reading the ids as a rank is a plausible shortcut
// that is wrong on exactly one pair, and the suite knows it.
//
// A section id absent from this table has no place in the grammar, which is why
// the same lookup answers both questions: rank, and whether the id is legal at
// all.
var sectionRank = map[SectionID]int{
	SectionType:     1,
	SectionImport:   2,
	SectionFunction: 3,
	SectionTable:    4,
	SectionMemory:   5,
	// The tag section is exception handling (Wasm 3.0). It is ranked, not
	// rejected: no suite vector asserts id 13 is a malformed id, and rejecting it
	// here would be the gate leaking into the decoder's structural layer. What the
	// EH gate governs is whether its *contents* are decoded (#8's family), not
	// whether the id has a place in the order.
	SectionTag:       6,
	SectionGlobal:    7,
	SectionExport:    8,
	SectionStart:     9,
	SectionElement:   10,
	SectionDataCount: 11, // id 12, but ordered before code — the reason for this table
	SectionCode:      12,
	SectionData:      13,
}

// Section is one raw module section: identity plus opaque payload.
type Section struct {
	ID      SectionID
	Payload []byte // aliases the input buffer; not copied
}

// Module is the section-level view of a decoded module.
type Module struct {
	Version  uint32
	Sections []Section
}

// reader is a cursor over the input. In-place posture: no copying,
// payloads alias the caller's buffer.
type reader struct {
	b   []byte
	off int

	// eof is the error to report when the cursor runs off the end. The preamble
	// reads with ErrTruncated ("unexpected end") and payload grammars read with
	// ErrPayloadEnd ("unexpected end of section or function"): the suite draws
	// that line — binary.wast:6 is the short form, binary.wast:88 the long one —
	// and it is a property of *where* the cursor is, not of which call runs out.
	// Threading it through the reader keeps every leaf read honest without each
	// one having to know its own depth.
	//
	// The zero value is ErrTruncated, via eofErr, so a reader constructed for a
	// preamble read needs no ceremony.
	eof error
}

func (r *reader) eofErr() error {
	if r.eof != nil {
		return r.eof
	}
	return ErrTruncated
}

func (r *reader) remaining() int { return len(r.b) - r.off }

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, r.eofErr()
	}
	p := r.b[r.off : r.off+n]
	r.off += n
	return p, nil
}

func (r *reader) byte() (byte, error) {
	if r.remaining() < 1 {
		return 0, r.eofErr()
	}
	c := r.b[r.off]
	r.off++
	return c, nil
}

// byteVec reads a length-prefixed byte sequence — the encoding of a name, and of
// a data segment's contents.
//
// The length is checked against the image before the slice is taken, and the
// overrun is ErrSectionOverrun ("length out of bounds") rather than an
// end-of-input error. binary.wast:754 is the vector that decides this: a name
// length of 10 with 6 bytes left in the image is what the suite calls "length out
// of bounds", not "unexpected end of section or function".
//
// byteVec is deliberately byte-neutral: a data segment's contents are arbitrary
// bytes, so the UTF-8 constraint belongs to name() rather than here. Reading a
// name is byteVec plus a predicate, and keeping them separate is what stops the
// predicate from being applied to bytes that were never text.
func (r *reader) byteVec() ([]byte, error) {
	return r.byteVecErr(ErrSectionOverrun)
}

// byteVecErr is byteVec with the overrun error chosen by the caller, because the
// suite gives the same shape two different strings and the field's role is what
// decides which.
//
// Both vectors are a declared length exceeding the bytes left in the image:
//
//   - binary.wast:754 — an export *name* of 10 bytes with 8 left: "length out of
//     bounds".
//   - binary.wast:877 — a data segment's *contents* of 7 bytes with 6 left:
//     "unexpected end of section or function".
//
// n=2, which is thin, so this is a parameter rather than a rule inferred from two
// points: the difference tracks name-vs-vec(byte), the same seam the UTF-8 predicate
// sits on (TestByteVecIsNotAName), and each call site states its own choice instead of
// one branch here guessing from context. If a third vector contradicts the split, the
// fix is at one call site rather than in a predicate that had over-generalised.
func (r *reader) byteVecErr(overrun error) ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if uint64(n) > uint64(r.remaining()) {
		return nil, fmt.Errorf("%w: %d bytes declared, %d left", overrun, n, r.remaining())
	}
	return r.bytes(int(n))
}

// name reads a `name`: a byte vector that must be well-formed UTF-8.
//
// The spec's production is `name ::= b*:vec(byte) => name` with the side condition
// that the bytes are `utf8(name)` — so the check is the encoding's own rule, and
// utf8.Valid *is* that rule. The suite's four utf8-*.wast files enumerate 176
// specific violations each (overlong forms, unpaired surrogates, code points past
// U+10FFFF, truncations, stray continuation bytes), and a check written from that
// enumeration would be the oracle mistaken for the objective function: it would
// pass the vectors while remaining wrong about the byte sequences the suite has no
// vector for. The stdlib predicate was measured against all 528 executable vectors
// as *evidence it is implemented correctly*, not as the source of the rule.
//
// Returns only an error, unlike byteVec. The bytes are consumed *here*, by the
// predicate — which is the whole difference between the two methods — so there is
// nothing speculative left to hand back. This is the same classification question
// the //nolint:unparam on byteVec answered the other way, and the answer differs
// because the facts do: byteVec's return had a named future consumer (this check),
// where name's would have none until the module structure retains names. Declared
// and tracked beats suppressed; nothing beats not needing either.
func (r *reader) name() error {
	b, err := r.byteVec()
	if err != nil {
		return err
	}
	if !utf8.Valid(b) {
		return fmt.Errorf("%w: % x", ErrMalformedUTF8, b)
	}
	return nil
}

// uleb reads an unsigned LEB128 integer of the given bit width.
//
// GRAVE (0003, grave 1): the malformed-integer taxonomy is width-parameterized,
// not a property of LEB128. `ff ff ff ff 0f` is a *valid* u32 (0xFFFFFFFF) and a
// *malformed* i32 constant — same bytes, different verdict. The predecessor
// folded both malformed classes into one check (`i == 4 && c&0xF0 != 0`) inside a
// u32-only method, so it could report neither correctly nor distinguish them:
// - continuation bit set on the last permitted byte → representation too long
// - continuation clear but bits beyond the width set → integer too large
// The order matters: test the continuation bit first.
//
// GRAVE (0003, grave 2): the predecessor's ErrLEBTooLong was unreachable — the
// i==4 branch returned before the loop could fall through, so no input of any
// length reached it (verified exhaustively over all 256 fifth-byte values). The
// lesson, for whoever adds the next error constant: an error with no reachable
// path is a missing check wearing a disguise. The two bugs propped each other
// up — the dead constant is why the conflation went unnoticed.
func (r *reader) uleb(bits uint) (uint64, error) {
	maxBytes := int((bits + 6) / 7)
	var v uint64
	var shift uint
	for i := range maxBytes {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		if i == maxBytes-1 {
			// Last permitted byte: continuation bit first, then unused bits.
			if c&0x80 != 0 {
				return 0, ErrLEBTooLong
			}
			if used := bits - shift; used < 7 && c>>used != 0 {
				return 0, ErrLEBOverflow
			}
		}
		v |= uint64(c&0x7F) << shift
		if c&0x80 == 0 {
			return v, nil
		}
		shift += 7
	}
	// Unreachable: the i == maxBytes-1 branch returns on every path. Kept as a
	// guard so a future edit to the loop bound cannot silently accept a value.
	return 0, ErrLEBTooLong
}

// u32 reads an unsigned LEB128-encoded 32-bit integer (≤ 5 bytes).
func (r *reader) u32() (uint32, error) {
	v, err := r.uleb(32)
	return uint32(v), err
}

// u64 reads an unsigned LEB128-encoded 64-bit integer (≤ 10 bytes).
//
// No caller yet: i64 immediates (#7) and memory64's 64-bit limits are what will
// use it. Declared-and-tracked rather than silent, per the ruling in CLAUDE.md —
// see #19 for why it is kept instead of deleted. FuzzULEB covers uleb(64)
// directly, so the width is exercised even without a production caller.
//
//nolint:unused // tracked in #19; awaits i64 immediates (#7) / memory64 gate
func (r *reader) u64() (uint64, error) { return r.uleb(64) }

// sleb reads a signed LEB128 integer of the given bit width.
//
// Not uleb with a cast: the two differ in *both* halves of the malformed taxonomy,
// which is the grave-0003 lesson (see uleb) restated for the signed case.
//
//   - Sign extension: the final byte's payload is extended from its high bit, so
//     `\7f` is -1 at width 32, not 127.
//   - The overflow check is two-sided. On the last permitted byte the unused high
//     bits must be *all zero or all one*, matching the sign — where the unsigned
//     check requires all zero. `\80\80\80\80\10` is the i32 vector at
//     binary.wast:125 ("integer too large"): 0x10 has bit 4 set, which is neither a
//     legal positive nor a legal negative extension at width 32. A reader that
//     reused the unsigned rule would reject some valid negatives and accept that.
//
// Same ordering rule as uleb: the continuation bit is "representation too long" and
// is tested before the range check, so the two malformed classes stay distinct.
func (r *reader) sleb(bits uint) (int64, error) {
	maxBytes := int((bits + 6) / 7)
	var v int64
	var shift uint
	for i := range maxBytes {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		if i == maxBytes-1 {
			if c&0x80 != 0 {
				return 0, ErrLEBTooLong
			}
			// The payload bits of this byte that fall outside the width must all equal
			// the sign bit that the width does reach — all zero for a positive value,
			// all one for a correct negative sign extension.
			//
			// Both sides are compared in the same frame — shifted down to bit 0 —
			// because the first version of this check masked the high bits in place
			// and compared them against a constant shifted differently, which
			// rejected min-int32 (`\80\80\80\80\78`, all three out-of-width bits set
			// as a correct sign extension). Caught by the min/max int32 rows in
			// TestSlebIsNotUlebWithACast, which is why they are there.
			if used := bits - shift; used < 7 {
				high := c & 0x7F >> used    // the out-of-width bits, at bit 0
				sign := c >> (used - 1) & 1 // the sign bit the width reaches
				all := byte(0x7F >> used)   // that many ones
				if (sign == 0 && high != 0) || (sign == 1 && high != all) {
					return 0, ErrLEBOverflow
				}
			}
		}
		v |= int64(c&0x7F) << shift
		shift += 7
		if c&0x80 == 0 {
			// Sign-extend from the last payload bit consumed.
			if shift < 64 && c&0x40 != 0 {
				v |= -1 << shift
			}
			return v, nil
		}
	}
	// Unreachable for the same reason as uleb's tail: the last-byte branch returns on
	// every path. Kept as the same kind of guard.
	return 0, ErrLEBTooLong
}

// s32 reads a signed LEB128-encoded 32-bit integer — an i32.const immediate.
func (r *reader) s32() (int32, error) {
	v, err := r.sleb(32)
	return int32(v), err
}

// s64 reads a signed LEB128-encoded 64-bit integer — an i64.const immediate.
func (r *reader) s64() (int64, error) { return r.sleb(64) }

// DecodeModule decodes a complete module image under v0's default gate posture:
// every 3.0 proposal gate present and off (contract §9).
func DecodeModule(b []byte) (*Module, error) {
	return (&Decoder{}).DecodeModule(b)
}

// DecodeModule decodes a complete module image under d's gate set.
func (d *Decoder) DecodeModule(b []byte) (*Module, error) {
	r := &reader{b: b}

	// A *short* preamble is "unexpected end"; a full-width but wrong one is
	// "magic header not detected" / "unknown binary version". binary.wast
	// distinguishes these: "" / "\01" / "\00as" are unexpected end, while
	// "asm\00" and "wasm\01\00\00\00" are magic-header failures.
	magic, err := r.bytes(4)
	if err != nil {
		return nil, ErrTruncated
	}
	if [4]byte(magic) != Magic {
		return nil, ErrBadMagic
	}
	verBytes, err := r.bytes(4)
	if err != nil {
		return nil, ErrTruncated
	}
	ver := binary.LittleEndian.Uint32(verBytes)
	if ver != Version {
		return nil, fmt.Errorf("%w: %d", ErrBadVersion, ver)
	}

	m := &Module{Version: ver}

	// lastRank enforces order and uniqueness with one predicate: ranks must
	// strictly increase. A duplicate section fails it for the same reason a
	// misordered one does — "not greater than" covers both — which is why the two
	// families in #6 are one check rather than a rank comparison plus a seen-set.
	lastRank := 0
	for r.remaining() > 0 {
		id, err := r.byte()
		if err != nil {
			return nil, err
		}
		sid := SectionID(id)

		if sid != SectionCustom {
			rank, ok := sectionRank[sid]
			if !ok {
				return nil, fmt.Errorf("%w: %d", ErrMalformedSectionID, id)
			}
			if rank <= lastRank {
				return nil, fmt.Errorf("%w: %s section", ErrTrailingData, sid)
			}
			lastRank = rank
		}

		size, err := r.u32()
		if err != nil {
			return nil, err
		}

		// Face 1 of the size mechanism: the declared extent must exist in the
		// image at all. Checked before the grammar runs, because a grammar let
		// loose on a bogus extent reports the wrong face.
		if uint64(size) > uint64(r.remaining()) {
			return nil, fmt.Errorf("%w: %d bytes declared, %d left", ErrSectionOverrun, size, r.remaining())
		}
		payload := r.b[r.off : r.off+int(size)]

		// The grammar reads from a payload-scoped cursor that is *not* bounded by
		// the section — see sections.go on why over-reading is required rather
		// than merely tolerated.
		pr := &reader{b: r.b, off: r.off, eof: ErrPayloadEnd}
		decoded, err := d.decodePayload(sid, size, pr)
		if err != nil {
			return nil, err
		}

		if decoded {
			// Faces 2 and 3. Face 2 already fired inside the grammar if the image
			// ran out; reaching here means the grammar completed, so what remains
			// is whether it agreed with the declared extent. Both signs are the
			// same error, and the message reports which sign so a swap is visible.
			if used := pr.off - r.off; used != int(size) {
				return nil, fmt.Errorf("%w: %s section declared %d bytes, grammar consumed %d",
					ErrSectionSizeMismatch, sid, size, used)
			}
		}

		r.off += int(size)
		m.Sections = append(m.Sections, Section{ID: sid, Payload: payload})
	}

	if err := m.checkCounts(); err != nil {
		return nil, err
	}
	return m, nil
}

// vecCount reads the element count from the head of a vec-shaped section
// payload. It runs after the payload grammars, so a payload short enough to
// truncate the count here is a section-level end, not a preamble one.
func vecCount(payload []byte) (uint32, error) {
	r := &reader{b: payload, eof: ErrPayloadEnd}
	return r.u32()
}

// checkCounts verifies the cross-section agreements the binary format requires:
// the function and code sections must describe the same number of functions, and
// a data count section must agree with the data section.
//
// These are structural, not semantic — they are decidable from section headers
// alone, which is why they belong here and not in the validator. The
// ErrDataCountRequired half of the data-count contract is *not* decidable here
// (it needs function bodies) and is tracked in #22 rather than guessed at.
func (m *Module) checkCounts() error {
	var (
		funcCount, codeCount uint32
		dataCount            uint32
		haveDataCount        bool
		dataSegs             uint32
	)
	for _, s := range m.Sections {
		var (
			dst *uint32
			err error
		)
		switch s.ID {
		case SectionFunction:
			dst = &funcCount
		case SectionCode:
			dst = &codeCount
		case SectionData:
			dst = &dataSegs
		case SectionDataCount:
			// The data count section is a bare u32, not a vec — but the encoding
			// of "one LEB at the head of the payload" is the same read.
			haveDataCount = true
			dst = &dataCount
		default:
			continue
		}
		if *dst, err = vecCount(s.Payload); err != nil {
			return err
		}
	}

	// An absent section means zero, so one rule covers all four vectors in the
	// bucket: both present and disagreeing, and either one present alone.
	if funcCount != codeCount {
		return fmt.Errorf("%w: %d and %d", ErrFuncCodeMismatch, funcCount, codeCount)
	}
	if haveDataCount && dataCount != dataSegs {
		return fmt.Errorf("%w: %d and %d", ErrDataCountMismatch, dataCount, dataSegs)
	}
	return nil
}
