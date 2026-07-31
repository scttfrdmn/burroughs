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

	// ErrTrailingData names the duplicate/misordered-section condition. Not yet
	// enforced — section order and uniqueness land with the type section.
	ErrTrailingData = errors.New("unexpected content after last section")
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
}

func (r *reader) remaining() int { return len(r.b) - r.off }

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, ErrTruncated
	}
	p := r.b[r.off : r.off+n]
	r.off += n
	return p, nil
}

func (r *reader) byte() (byte, error) {
	if r.remaining() < 1 {
		return 0, ErrTruncated
	}
	c := r.b[r.off]
	r.off++
	return c, nil
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

// DecodeModule performs a section-level decode of a complete module image.
func DecodeModule(b []byte) (*Module, error) {
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
	for r.remaining() > 0 {
		id, err := r.byte()
		if err != nil {
			return nil, err
		}
		size, err := r.u32()
		if err != nil {
			return nil, err
		}
		payload, err := r.bytes(int(size))
		if err != nil {
			return nil, ErrSectionOverrun
		}
		m.Sections = append(m.Sections, Section{ID: SectionID(id), Payload: payload})
	}
	return m, nil
}
