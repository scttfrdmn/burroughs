package text

// The encoder's byte layer: LEB128 and section framing, for #8's text→binary bridge.
//
// # Why the encoder exists at all, and what it is allowed to be
//
// 0011 part 2, veto lifted: the parser emits binary bytes into the proven decoder rather than
// growing a second module representation, and `binary.Module` stays the codebase's one module
// authority. So this file's product is *bytes*, and the acceptance question is whether
// `binary.DecodeModule` reads back the module the text denotes — #67's two halves.
//
// That makes the decoder the authority for every encoding decision here, and the direction of
// the dependency is the point: nothing in this file gets to have an opinion about what a LEB is.
// Where a reader exists on the other side, the writer is its inverse and is falsified by round
// trip, never by inspection. The one thing a round trip cannot catch is a *shared* misconception,
// which is exactly why #67 half 2's witness is the wabt corpus and not our own decoder.
//
// # Minimal-width LEB, and why that is a real choice rather than the obvious one
//
// `uleb` accepts any width up to the field's budget — `80 80 80 80 10` is a legal five-byte
// encoding of a small number, and `binary-leb128.wast` asserts as much. So an encoder is *free*
// here, and freedom is where a silent divergence lives: the cross-check corpus compares against
// wabt's bytes, and wabt writes minimal width. Two encoders that both round-trip through their
// own decoders can still disagree byte for byte.
//
// This writes minimal width, and the comparator therefore compares `[]Instr` rather than bytes
// (0011's appendix ruled exactly this). Byte equality with wabt is *not* the criterion and must
// not become one — it would make every legal width choice a false failure, and it would make the
// corpus an authority on encoding style, which it is not. What the corpus is an authority on is
// **which module the text denotes**.

import "math/bits"

// writer accumulates the encoded module.
//
// A plain byte slice rather than an io.Writer, because a section's length prefix is only known
// after its contents are written, so the encoder needs to seek backwards — or, as here, to
// encode a section into a nested buffer and splice it. An io.Writer would force a two-pass
// size calculation, which is a second place computing every length: the drift risk 0006 names,
// paid per section for no benefit.
type writer struct {
	b []byte
}

// byte1 appends one literal byte.
//
// Named `byte1` rather than `byte` because a method named for a builtin type shadows it inside
// the method set, and `[]byte(...)` conversions in this file would then fail to compile in a way
// that reads as a type error rather than as a naming collision.
func (w *writer) byte1(b byte) { w.b = append(w.b, b) }

// bytes appends a literal run.
func (w *writer) bytes(b []byte) { w.b = append(w.b, b...) }

// u32 writes an unsigned LEB128 integer, minimal width.
//
// The inverse of `(*reader).u32`, and minimal by construction rather than by a length check: the
// loop emits a continuation byte only while bits remain, so a zero is one byte and not five.
func (w *writer) u32(v uint32) { w.uleb(uint64(v)) }

// u64 writes an unsigned LEB128 integer of up to 64 bits, minimal width.
func (w *writer) u64(v uint64) { w.uleb(v) }

// uleb is the unsigned LEB128 kernel.
//
// Termination is on the *value*, not on a byte budget, which is what makes it minimal: the
// reader's width budget (5 bytes for u32, 10 for u64) is an upper bound it enforces on input,
// and an encoder that emitted its budget every time would be legal and would disagree with every
// other producer.
func (w *writer) uleb(v uint64) {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			w.b = append(w.b, c|0x80)
			continue
		}
		w.b = append(w.b, c)
		return
	}
}

// There is deliberately no `s32` yet. Its caller is `i32.const`'s immediate, which arrives with
// the instruction emitter, and a one-line wrapper with no caller is the placeholder shape the
// `ErrTrailingData` ruling (#6) says must be declared-and-tracked to be honest. Tracking it would
// cost more than writing it later does: `sleb` is the kernel and is already covered, so `s32` is
// `sleb` with a cast and nothing else. Added when something calls it.

// s64 writes a signed LEB128 integer, minimal width.
func (w *writer) s64(v int64) { w.sleb(v) }

// sleb is the signed LEB128 kernel, and it is deliberately not uleb with a cast.
//
// The two differ in both halves of the malformed taxonomy — grave 0003's lesson, which the
// decoder's `sleb` states for the reading direction. Writing has the mirror hazard: the sign bit
// of the final byte must be *correct*, so the loop cannot stop when the remaining magnitude is
// zero. It stops when the remaining value is the sign extension of what was already written,
// which is the condition `-1` and `0` both satisfy at their own final byte.
//
// The classic off-by-one here is `0x40`: as a payload it is the low six bits plus a set bit 6,
// which sign-extends to **-64**, so encoding 64 in one byte would produce -64. The loop's
// condition catches it because after `v >>= 7` the value is 0 while the emitted byte's sign bit
// is set, so 0 is not that byte's sign extension and a second byte is required.
func (w *writer) sleb(v int64) {
	for {
		c := byte(v & 0x7F)
		v >>= 7 // arithmetic shift: sign-extends, which is what the condition below reads
		signExtended := (v == 0 && c&0x40 == 0) || (v == -1 && c&0x40 != 0)
		if signExtended {
			w.b = append(w.b, c)
			return
		}
		w.b = append(w.b, c|0x80)
	}
}

// u32le writes four little-endian bytes: the *fixed-width* u32 the preamble's version field is.
//
// Distinct from `u32`, which is a LEB, and the distinction is the one place the module format has
// both — `binary.Version` is `01 00 00 00` and never `01`. A LEB there would produce a four-byte
// preamble the decoder reads as a short image ("unexpected end"), which is a loud failure and so
// the *lucky* case; the unlucky one is a version field that happens to LEB-encode to four bytes and
// means something else. Named for the difference rather than overloading u32.
func (w *writer) u32le(v uint32) { w.f32(v) }

// f32 writes four little-endian bytes.
func (w *writer) f32(v uint32) {
	w.b = append(w.b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// f64 writes eight little-endian bytes.
func (w *writer) f64(v uint64) {
	for i := range 8 {
		w.b = append(w.b, byte(v>>(8*i)))
	}
}

// byteVec writes a length-prefixed byte run: the `vec(byte)` shape a name or a data segment uses.
func (w *writer) byteVec(b []byte) {
	w.u32(uint32(len(b)))
	w.bytes(b)
}

// name writes a UTF-8 name as `vec(byte)`.
//
// The string is written verbatim. Go strings are not guaranteed valid UTF-8, and the *parser* is
// what establishes that a wat name is — `decodedName` in context.go is the one place that
// question is answered, and answering it a second time here would be two places knowing one
// fact. The decoder's `nameString` re-checks on the way back in, so a bad name cannot survive
// the round trip silently.
func (w *writer) name(s string) { w.byteVec([]byte(s)) }

// section writes a section with its id and length prefix, encoding the body via f.
//
// The body is encoded into a nested writer and spliced, so the length is measured rather than
// predicted. A section whose declared size disagrees with its content is the exact defect
// `checkSectionSize` reports from the other side (both signs of it — grave #34), and the way to
// make that unreachable from here is to never compute a length twice.
//
// An **empty body still writes the section**, which is a real decision and not an oversight: a
// module with a type section declaring zero types is well-formed, and whether to omit an empty
// section is the caller's question, because only the caller knows whether the text said anything.
// Deciding it here would silently normalize `(module (type))`-shaped input.
func (w *writer) section(id byte, f func(*writer)) {
	var body writer
	f(&body)
	w.byte1(id)
	w.u32(uint32(len(body.b)))
	w.bytes(body.b)
}

// vec writes a length-prefixed vector, calling f once per element.
//
// The count comes from n rather than from counting f's appends, so a caller whose n disagrees
// with its loop produces a decoder error rather than a silently short vector — the decoder reads
// exactly n elements and then finds the section's remaining bytes unaccounted for.
func (w *writer) vec(n int, f func(w *writer, i int)) {
	w.u32(uint32(n))
	for i := range n {
		f(w, i)
	}
}

// ulebWidth reports the number of bytes uleb will emit for v.
//
// Not used by the writer itself — the writer never needs to predict its own output, which is the
// whole reason `section` splices. It exists for the tests that assert minimality, because
// "minimal" is a claim about width and asserting it needs an independent statement of what the
// width should be. Derived from the value's bit length rather than by calling uleb and measuring,
// so it is a second opinion and not an echo (grave #106).
func ulebWidth(v uint64) int {
	if v == 0 {
		return 1
	}
	return (bits.Len64(v) + 6) / 7
}
