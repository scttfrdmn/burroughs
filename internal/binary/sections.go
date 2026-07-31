package binary

import "fmt"

// Section payload decoding — "the decoder stops taking sections' word for it".
//
// # The size mechanism
//
// A section declares a byte length. Verifying it is one mechanism with three
// faces, and which face fires is decided by *where* the disagreement shows up.
// All three are the same question — does the grammar agree with the declared
// extent — asked at three different moments:
//
//  1. The declared length exceeds the bytes actually present in the image:
//     ErrSectionOverrun, "length out of bounds". Checked before any grammar runs.
//  2. The grammar wants a byte and the *image* is exhausted: ErrPayloadEnd,
//     "unexpected end of section or function".
//  3. The grammar finishes and the byte count it consumed differs from the
//     declared length, in either direction: ErrSectionSizeMismatch,
//     "section size mismatch".
//
// Face 3 is the two-signed one, and the tests pin both signs independently
// (TestSectionSizeBothSigns): a sign error there would swap nothing visible in
// the pass count, because both directions are malformed either way. Only the
// error *text* would be wrong, which is exactly the kind of defect a pass count
// cannot see.
//
// # Payload grammar is not bounded by the section
//
// Face 2 says "the image is exhausted", not "the section is exhausted", and that
// is deliberate — it is what the suite's vectors demand, and getting it wrong
// looks plausible in both directions:
//
//   - binary.wast:754 has an export section declaring 2 exports and supplying 1,
//     with a code section following. The grammar reads on past the section end
//     into the code section's id byte, takes it as a name length (0x0a = 10),
//     finds fewer than 10 bytes left in the *image*, and reports "length out of
//     bounds". A section-bounded reader would have stopped at the boundary and
//     said "unexpected end of section or function" instead.
//   - binary.wast:92 is the same story told by the suite itself, in a comment:
//     the reference interpreter "consumes the \0b (data section start) as an END
//     instruction and reports the code section as being larger than declared".
//     Over-reading past the boundary is how face 3 comes to fire at all.
//
// So the grammar runs against the whole remaining image and the extent is
// reconciled afterwards. The one place that needs the boundary back is the
// custom section, which is why it carries its own check — see decodeCustom.

// Features gates which proposals' constructs the decoder accepts.
//
// This is the acceptance layer, and it is a different layer from the grammar.
// *Malformed* belongs to the grammar of the tracked union and is gate-blind
// (CLAUDE.md, "gates never manufacture malformedness"): section id 13 is
// well-formed because Wasm 3.0 defines it, and id 14 is malformed because
// nothing tracked does — neither fact moves when a gate flips. What a gate
// decides is whether a well-formed construct is *accepted*, and a gate-off
// engine meeting a gated construct rejects it with a feature-named error, never
// with a spec malformed-string it has no right to.
//
// The zero value is v0's posture: every 3.0 gate present and off (contract §9).
type Features struct {
	ExceptionHandling bool // tag section (id 13), import/export kind 4
	SIMD              bool // v128 value type
	Threads           bool // shared limits flags (2, 3)
	Memory64          bool // 64-bit limits flags (4..7)
}

// Decoder holds the configuration one decode runs under. A config struct rather
// than a widening parameter list: per-section acceptance is gate-dependent for
// every section that will ever be added, so the gate set is a property of the
// decoder, not an argument to one function.
type Decoder struct {
	Features Features
}

// featureErr names the gate, never the grammar. The text deliberately does not
// resemble any assert_malformed string in the suite: a gate-off engine that
// spoofed a spec string would score itself green for rejecting a module the spec
// calls well-formed, which is the suite measuring the wrong thing.
func featureErr(feature string) error {
	return fmt.Errorf("%s: %w", feature, ErrFeatureDisabled)
}

// decodePayload runs the grammar for one section over r, which is positioned at
// the first payload byte and bounded only by the image.
//
// A section with no grammar here yet returns false: the caller skips its payload
// and no extent check runs, because an extent cannot be checked against a
// grammar that does not exist. That is the declared-and-tracked form of "not
// done" (CLAUDE.md) — the alternative, a grammar that consumes `size` bytes and
// declares victory, would report agreement it never verified.
func (d *Decoder) decodePayload(sid SectionID, size uint32, r *reader) (bool, error) {
	switch sid {
	case SectionCustom:
		return true, d.decodeCustom(size, r)
	case SectionType:
		return true, d.decodeVec(r, d.decodeFuncType)
	case SectionImport:
		return true, d.decodeVec(r, d.decodeImport)
	case SectionFunction:
		return true, d.decodeVec(r, discardIndex)
	case SectionTable:
		return true, d.decodeVec(r, d.decodeTable)
	case SectionMemory:
		return true, d.decodeVec(r, d.decodeMemory)
	case SectionExport:
		return true, d.decodeVec(r, d.decodeExport)
	case SectionStart:
		// A bare function index, not a vec.
		return true, discardIndex(r)
	case SectionDataCount:
		// A bare u32, not a vec. Its value is cross-checked in checkCounts; here
		// it only has to be present and well-formed.
		_, err := r.u32()
		return true, err
	case SectionTag:
		// Ranked by the structural layer (it is well-formed), accepted only by
		// the gate. Its payload grammar arrives with the EH gate (#8).
		if !d.Features.ExceptionHandling {
			return false, featureErr("exception handling")
		}
		return false, nil
	case SectionGlobal:
		return true, d.decodeVec(r, d.decodeGlobal)
	case SectionElement:
		return true, d.decodeVec(r, d.decodeElemSegment)
	case SectionData:
		return true, d.decodeVec(r, d.decodeDataSegment)
	default:
		// The code section, which needs full instruction decoding rather than the
		// constexpr subset (#7/#22). Declared-and-tracked, as above: its vectors stay
		// on the board.
		return false, nil
	}
}

// discardIndex reads an index and drops it. Index *validity* — does the function
// exist — is the validator's question, not the decoder's; here the only claim is
// that a well-formed u32 occupies those bytes.
func discardIndex(r *reader) error {
	_, err := r.u32()
	return err
}

// decodeVec applies an element grammar `count` times. The count is a u32 read
// from the head of the payload, and a truncated element is face 2 of the size
// mechanism, reported by the reader itself.
func (d *Decoder) decodeVec(r *reader, elem func(*reader) error) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	for range n {
		if err := elem(r); err != nil {
			return err
		}
	}
	return nil
}

// decodeCustom reads a custom section's name and skips the rest.
//
// This is the one grammar that needs the section boundary the others discard,
// and custom.wast:76 is why. That vector is an empty custom section (`\00\00`,
// declared length zero) followed by a well-formed one. Reading the name without
// the boundary would take the *next* section's id byte as a zero-length name,
// then decode the following section cleanly and accept the module — the suite
// says "unexpected end". The name lives inside the declared extent, so
// over-reading it is an error even though over-reading a type or export vec is
// not: the difference is that a custom section's tail is opaque bytes, so there
// is no later grammar step for face 3 to catch the over-read with.
func (d *Decoder) decodeCustom(size uint32, r *reader) error {
	start := r.off
	if err := r.name(); err != nil {
		return err
	}
	rest := int(size) - (r.off - start)
	if rest < 0 {
		return ErrPayloadEnd
	}
	_, err := r.bytes(rest)
	return err
}

// decodeFuncType reads a functype: the 0x60 form byte, then param and result
// vectors of value types.
func (d *Decoder) decodeFuncType(r *reader) error {
	// The form tag is a *signed* LEB of width 7, not a plain byte, and
	// binary-leb128.wast:1067 is the vector that says so: `\e0\7f` is -0x20 encoded
	// in two bytes, and the suite wants "integer representation too long" rather
	// than "malformed function type". The spec's type constructors live in negative
	// s7 space — 0x60 *is* -32 at width 7, as 0x5e (array) is -34 — so reading the
	// tag as a byte gets the right answer for well-formed input and the wrong error
	// for an overlong encoding of it.
	//
	// sleb(7) is exactly the right instrument: its width budget is one byte, so a
	// continuation bit on the first byte exhausts it, which is what "too long" means
	// here. Verified against the reference sN at width 7 (grave #36's port).
	form, err := r.sleb(7)
	if err != nil {
		return err
	}
	if form != -0x20 { // 0x60
		// GRAVE (#36): the message names the byte the image actually held, which at
		// width 7 is the low seven bits of the decoded value — the range is -64..63, so
		// form&0x7f is exactly the input byte, and a multi-byte encoding never reaches
		// here (sleb(7) has already returned "too long"). The first version of this
		// expression or'd a high bit in for every negative form and reported 0x5e
		// (array) as 0xde: an error about the module lying about the module, which no
		// suite can catch because the harness matches the sentinel and never reads past
		// the colon. Found by *printing* the output for nine tags rather than reading
		// the expression's shape. Pinned by TestFuncTypeFormIsASignedLEB.
		return fmt.Errorf("%w: %#02x", ErrMalformedFuncType, byte(form&0x7F))
	}
	for range 2 { // params, then results — same grammar, twice
		if err := d.decodeVec(r, d.decodeValType); err != nil {
			return err
		}
	}
	return nil
}

// decodeValType reads one value type byte.
func (d *Decoder) decodeValType(r *reader) error {
	b, err := r.byte()
	if err != nil {
		return err
	}
	switch b {
	case 0x7F, 0x7E, 0x7D, 0x7C: // i32 i64 f32 f64
		return nil
	case 0x7B: // v128
		if !d.Features.SIMD {
			return featureErr("simd")
		}
		return nil
	case 0x70, 0x6F: // funcref externref
		return nil
	}
	return fmt.Errorf("%w: %#02x", ErrMalformedValType, b)
}

// decodeRefType reads a reference type byte, as a table's element type.
func (d *Decoder) decodeRefType(r *reader) error {
	b, err := r.byte()
	if err != nil {
		return err
	}
	if b != 0x70 && b != 0x6F {
		return fmt.Errorf("%w: %#02x", ErrMalformedRefType, b)
	}
	return nil
}

// decodeLimits reads a limits flags byte and its min, plus max when present.
//
// The flags field is a single byte, not a LEB — which is the whole content of
// three suite vectors (binary.wast:632, :677, :686) that encode a *valid* flag
// value 1 as the two-byte LEB `\81\00` and expect "malformed limits flags". A
// decoder reading the flags with u32 would accept all three.
//
// min and max are read at **64 bits**, not 32, and the suite is unambiguous about
// it (grave #36). binary-leb128.wast:525 is a memory32 section whose min is a
// 10-byte LEB with unused bits set, and it wants "integer too large": ten bytes is
// legal *width* for a u64 and one byte too many for a u32, so a u32 read reports
// "integer representation too long" and scores the wrong string. The neighbouring
// :217 and :225 (11-byte fields) want "too long" and still get it, since 11 bytes
// overruns the u64 budget too — the two vectors bracket the width from both sides.
//
// The consequence is deliberate: a memory32 limit above 2^32 now decodes and is the
// *validator's* to reject ("memory size must be at most 65536 pages"), which is the
// correct layering. Reading the field narrowly to catch it here would be the decoder
// borrowing the validator's job and getting the malformed string wrong to do it.
//
// This is reader.u64's first production caller, closing #19's declared-and-tracked
// deferral by making it reachable rather than by allowlisting it.
func (d *Decoder) decodeLimits(r *reader) error {
	flags, err := r.byte()
	if err != nil {
		return err
	}
	var hasMax bool
	switch flags {
	case 0x00:
	case 0x01:
		hasMax = true
	case 0x02, 0x03:
		if !d.Features.Threads {
			return featureErr("threads")
		}
		hasMax = flags == 0x03
	case 0x04, 0x05, 0x06, 0x07:
		if !d.Features.Memory64 {
			return featureErr("memory64")
		}
		hasMax = flags&0x01 != 0
	default:
		return fmt.Errorf("%w: %#02x", ErrMalformedLimits, flags)
	}
	if _, err := r.u64(); err != nil {
		return err
	}
	if hasMax {
		if _, err := r.u64(); err != nil {
			return err
		}
	}
	return nil
}

func (d *Decoder) decodeTable(r *reader) error {
	if err := d.decodeRefType(r); err != nil {
		return err
	}
	return d.decodeLimits(r)
}

func (d *Decoder) decodeMemory(r *reader) error {
	return d.decodeLimits(r)
}

// decodeGlobalType reads a global's value type and mutability byte.
func (d *Decoder) decodeGlobalType(r *reader) error {
	if err := d.decodeValType(r); err != nil {
		return err
	}
	mut, err := r.byte()
	if err != nil {
		return err
	}
	if mut > 0x01 {
		return fmt.Errorf("%w: %#02x", ErrMalformedMutability, mut)
	}
	return nil
}

// decodeImport reads module name, field name, and the kind-specific descriptor.
func (d *Decoder) decodeImport(r *reader) error {
	for range 2 { // module name, then field name
		if err := r.name(); err != nil {
			return err
		}
	}
	kind, err := r.byte()
	if err != nil {
		return err
	}
	switch kind {
	case 0x00: // func: a type index
		_, err = r.u32()
		return err
	case 0x01:
		return d.decodeTable(r)
	case 0x02:
		return d.decodeMemory(r)
	case 0x03:
		return d.decodeGlobalType(r)
	case 0x04: // tag
		if !d.Features.ExceptionHandling {
			return featureErr("exception handling")
		}
		if _, err = r.byte(); err != nil { // attribute
			return err
		}
		_, err = r.u32() // type index
		return err
	}
	return fmt.Errorf("%w: %#02x", ErrMalformedImportKind, kind)
}

// decodeExport reads a name, a kind byte, and an index.
func (d *Decoder) decodeExport(r *reader) error {
	if err := r.name(); err != nil {
		return err
	}
	kind, err := r.byte()
	if err != nil {
		return err
	}
	switch kind {
	case 0x00, 0x01, 0x02, 0x03: // func table memory global
	case 0x04: // tag
		if !d.Features.ExceptionHandling {
			return featureErr("exception handling")
		}
	default:
		return fmt.Errorf("%w: %#02x", ErrMalformedExportKind, kind)
	}
	_, err = r.u32()
	return err
}
