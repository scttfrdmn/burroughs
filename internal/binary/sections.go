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
	// The comments name every construct the gate governs, because a gate whose scope is
	// only known at its check sites is a gate nobody can audit — and writing them out is
	// how #48 was found. See that issue: the instruction grammar dispatches from the
	// generated table with no feature check anywhere, so with these gates off the decoder
	// *accepts* gated opcodes in function bodies, which is the accept-and-ignore the
	// gates-never-manufacture-malformedness ruling forbids in its other direction.
	//
	// So these comments describe the gates as they *are*, and the missing opcode scope is
	// tracked rather than described as present.
	// The opcode half of every scope below is now real, and it lives in gatemap.go
	// rather than here: the mapping is hand-authored testimony with a citation per
	// entry, and this struct is where the gates are *declared*, not where their scope is
	// enumerated. Decision 0008.
	ExceptionHandling bool // tag section (id 13), import/export kind 4; throw, throw_ref, try_table
	SIMD              bool // v128 value type, including as a blocktype; the 0xfd region
	Threads           bool // shared limits flags (2, 3)
	Memory64          bool // 64-bit limits flags (4..7)

	// The four gates #48 found missing. A *tracked* proposal (contract §9 G-2) with no
	// bool here is worse than a gate that never fires, because the reflection-derived
	// lanes cannot exercise a gate that is not there to reflect over — the
	// forgotten-fifth-gate scenario existing in the wild, four times.
	GC          bool // the 0xfb region; ref.eq, and the function-references five (0008)
	TailCall    bool // return_call, return_call_indirect
	RelaxedSIMD bool // the fd 0x100..0x12f window, inside SIMD's region
	MultiMemory bool // memarg flags bit 6: an explicit memory index on loads and stores
}

// Decoder holds the configuration one decode runs under. A config struct rather
// than a widening parameter list: per-section acceptance is gate-dependent for
// every section that will ever be added, so the gate set is a property of the
// decoder, not an argument to one function.
type Decoder struct {
	Features Features

	// sawDataRef records that a decoded function body used an opcode whose free
	// variables include the data index space (see dataRefOps). It is per-decode state
	// on the Decoder rather than a return value because the question it answers is
	// asked at the *module* level — `require (data_count <> None || ... datas =
	// Set.empty)` (decode.ml:1299) — and reset at the top of DecodeModule so a reused
	// Decoder cannot carry one module's answer into the next. That reset is the
	// stateful-instrument law (#28) applied to the decoder itself: state that survives
	// a measurement reports history.
	sawDataRef bool
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
	case SectionCode:
		// The last section to get a grammar (#39), and the one #22 was waiting on.
		//
		// Each body carries its own `sized` extent (decode.ml:1140), so the per-body
		// mismatch is reported inside decodeFuncBody and the section-level extent check
		// still runs on top — two levels of the same mechanism, which is why
		// binary.wast:92 is a `section size mismatch` about a *function*.
		return true, d.decodeVec(r, d.decodeFuncBody)
	default:
		// Unreachable: every id in sectionRank has a case above, and an id absent from
		// sectionRank was rejected as malformed before the payload was reached.
		// Declared rather than silent — a section added to the rank table without a
		// grammar arrives here, and returning false would let it decode as an
		// unchecked skip.
		return false, fmt.Errorf("%w: %d has no payload grammar", ErrMalformedSectionID, byte(sid))
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
		// suite vector here can catch, this one's expected string being the bare
		// sentinel — the harness reads exactly as far as the expected string does, and
		// where a spec string embeds a value (`illegal opcode ff`) the rendering *is*
		// oracle-covered (#38). Found by *printing* the output for nine tags rather than
		// reading the expression's shape. Pinned by TestFuncTypeFormIsASignedLEB.
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

// decodeRefType reads a reference type, as a table's element type — `reftype`
// (decode.ml:201-218).
//
// The reference reads a signed LEB at width 7 and accepts **fourteen** forms, of which
// this engine's original two (0x70 funcref, 0x6F externref) are the Wasm 2.0 subset. The
// other twelve are GC's — the abstract heap types (-0x0c..-0x0f, -0x12..-0x17) and the
// two *parameterized* prefixes -0x1c (ref, non-null) and -0x1d (ref null), each of which
// is followed by a `heaptype`. Reading them as `malformed reference type` was #51's
// accept-direction defect: the spec defines them, so the engine's own configuration is
// what declines them, and it must say so (*gates never manufacture malformedness*).
//
// A signed LEB, not a byte, and that is the reference's choice rather than a flourish:
// grave #36's lesson is that the width decides the *error string* for an overlong
// encoding, and `sleb(7)` reports "too long" where a byte read would report a bogus
// form. The message names the input byte via form&0x7F, which at width 7 is exact for
// the same reason decodeFuncType's does (range -64..63) — the reconstruction that grave
// found lying is not repeated here.
func (d *Decoder) decodeRefType(r *reader) error {
	form, err := r.sleb(7)
	if err != nil {
		return err
	}
	switch form {
	case -0x10, -0x11: // funcref (0x70), externref (0x6F) — Wasm 2.0, ungated
		return nil

	case -0x0C, -0x0D, -0x0E, -0x0F, // noexn, nofunc, noextern, none
		-0x12, -0x13, -0x14, -0x15, -0x16, -0x17: // any, eq, i31, struct, array, exn
		// GC's abstract heap types. Well-formed per Wasm 3.0, so the decline is
		// feature-named (decision 0008 folds function references into the GC gate).
		if !d.Features.GC {
			return featureErr("gc")
		}
		return nil

	case -0x1C, -0x1D: // (ref ht), (ref null ht)
		// The parameterized forms, each followed by a heaptype. The gate is checked
		// *before* descending, because the heaptype read would otherwise be the thing
		// that reports the error and it would report the wrong layer.
		if !d.Features.GC {
			return featureErr("gc")
		}
		return d.decodeHeapType(r)
	}
	return fmt.Errorf("%w: %#02x", ErrMalformedRefType, byte(form&0x7F))
}

// decodeHeapType reads a heap type — `heaptype` (decode.ml:178-198).
//
// An `either` alternation in the reference: a type *index* (s33, so a plain funcidx-style
// number) or one of the eleven abstract forms. Written as one here for the reason
// decodeBlockType is: on an overlong LEB the first branch's error must not stand, since
// the cursor rewinds and the bytes get judged again.
//
// Reached only with the GC gate on — decodeRefType checks the gate before descending — so
// the abstract forms need no second gate check. That is deliberate rather than an
// omission: two gate checks for one construct is the accept-and-ignore hazard's mirror,
// where a reader disagrees with its caller about whether a feature is on.
func (d *Decoder) decodeHeapType(r *reader) error {
	return either(r,
		func(r *reader) error {
			// `UseHT (typeuse s33 s)` — a type index. Negative values are not indices,
			// which is what sends the abstract forms to the next branch.
			v, err := r.sleb(33)
			if err != nil {
				return err
			}
			if v < 0 {
				return ErrMalformedTypeIndex
			}
			return nil
		},
		func(r *reader) error {
			form, err := r.sleb(7)
			if err != nil {
				return err
			}
			switch form {
			case -0x0C, -0x0D, -0x0E, -0x0F,
				-0x10, -0x11, -0x12, -0x13, -0x14, -0x15, -0x16, -0x17:
				return nil
			}
			return fmt.Errorf("%w: %#02x", ErrMalformedHeapType, byte(form&0x7F))
		},
	)
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

// decodeTable reads one table — `table` (decode.ml:1049-1063).
//
// Two forms, and the second one is #51: function references adds a `0x40` prefix, a
// reserved zero byte, a tabletype, and a const-expr **initializer** —
// `(table 1 (ref func) (ref.func 0))` in text. Without this branch the 0x40 reached
// decodeRefType and came back `malformed reference type: 0x40`, the decoder rejecting a
// module the suite calls *valid*, seven times in elem.wast. A decoder that rejects valid
// modules is worse than one that misses an invalid one.
//
// **This is deliberately not an `either`, and the reason is a measurement.** The
// reference uses one, and copying that shape here would break the gate decline: `either`
// lets the *last* branch's error stand, so a `\40` with GC off would be judged by the
// plain branch and reported as `malformed reference type: 0x40` — the gate manufacturing
// malformedness for a module Wasm 3.0 defines, which is the exact defect #51 filed. The
// blocktype alternation solves the same problem by *ordering* (valtype last), and the
// first draft of this function tried to, with the gated branch first; that ordering is
// unreachable here, because the branch that must speak is the one the alternation is
// structured to discard.
//
// A switch is sound where blocktype's could not be, and that difference is measured, not
// assumed: over all 256 first bytes the two forms are **disjoint** — the plain branch
// accepts 12, the 0x40 form accepts 1, and *zero* bytes are accepted by both. `0x40` is
// not a legal reftype in any gate configuration. With no ambiguity there is nothing to
// backtrack for, so the alternation's only remaining effect would be to lose the error
// that matters. (The first version of this comment claimed the order also decides
// *extent*, since the forms consume different byte counts — that was invented reasoning,
// and the disjointness probe is what killed it. Extent cannot differ between branches
// that never both apply.)
func (d *Decoder) decodeTable(r *reader) error {
	if b, ok := r.peek(); ok && b == 0x40 {
		// `expect 0x40 s ""; zero s; tabletype s; const s`.
		if _, err := r.byte(); err != nil {
			return err
		}
		if !d.Features.GC {
			// Function references, folded into the GC gate by decision 0008. Named
			// before the zero byte is read, so the decline describes the construct
			// rather than whatever byte happens to follow it.
			return featureErr("gc")
		}
		z, err := r.byte()
		if err != nil {
			return err
		}
		if z != 0x00 {
			return fmt.Errorf("%w: %#02x", ErrZeroByteExpected, z)
		}
		if err := d.decodeTableType(r); err != nil {
			return err
		}
		// The initializer, through the existing const-expr grammar — which is why this
		// is a small change rather than a new reader: #25's authority-derived table
		// already knows every const instruction's immediate widths.
		return d.decodeConstExpr(r)
	}
	return d.decodeTableType(r)
}

// decodeTableType reads a tabletype: element type then limits (decode.ml:301-304).
//
// Split out of decodeTable because the 0x40 form needs it too, and because the reference
// has it as its own production. Both callers are in this file, so this is not the
// premature-sharing case decision 0006 warns about — the second consumer exists now.
func (d *Decoder) decodeTableType(r *reader) error {
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
