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

	// ErrMalformedDefType is `comptype`'s fallthrough (decode.ml:259). It replaced
	// ErrMalformedFuncType at that site, which was a **sentinel the reference never
	// emits**: `grep -r 'malformed function type' third_party/spec/interpreter/` finds
	// nothing, and no suite vector asserts either string, so the board could not tell the
	// invented one from the real one. Grave #36's class one layer out — a fabricated
	// sentinel rather than a fabricated byte (#86).
	ErrMalformedDefType = errors.New("malformed definition type")

	// ErrMalformedStorageType is `packtype`'s fallthrough (decode.ml:234), reached as the
	// last branch of `storagetype`'s `either` — so it is the message a field type's first
	// byte gets when it is neither a valtype nor i8/i16. No suite vector asserts it: every
	// storage-type position is inside a GC construct, which a gate-off engine declines
	// first. Named at its definition site per the ErrTrailingData ruling (#6).
	ErrMalformedStorageType = errors.New("malformed storage type")

	// ErrMalformedNumType and ErrMalformedVecType are `numtype`'s and `vectype`'s
	// fallthroughs (decode.ml:172, :177), and they replaced a single
	// `malformed value type` — a **third invented sentinel**, found by the same widened
	// control that caught ErrMalformedFuncType (#88).
	//
	// The reference has no `valtype` message at all: `valtype` is
	// `either [numtype; vectype; reftype]` (:220-225), a *pure* alternation whose every
	// branch has its own text, so the string a bad value-type byte gets is always one of
	// the three below. This engine had a flat switch over seven bytes with a message of
	// its own invention — which was also the accept-direction half of #88, since the
	// reference's third branch accepts fourteen forms and the switch accepted two.
	//
	// `either` returns the *last* branch's error, so the message a byte that is no
	// valtype at all receives is `malformed reference type`. That is the reference's
	// answer and it is not intuitive; it is pinned by
	// TestValTypeAlternationIsTheReference rather than left to be re-derived.
	ErrMalformedNumType = errors.New("malformed number type")
	ErrMalformedVecType = errors.New("malformed vector type")

	ErrMalformedRefType    = errors.New("malformed reference type")
	ErrMalformedHeapType   = errors.New("malformed heap type")
	ErrMalformedLimits     = errors.New("malformed limits flags")
	ErrMalformedMutability = errors.New("malformed mutability")
	ErrMalformedImportKind = errors.New("malformed import kind")
	ErrMalformedExportKind = errors.New("malformed export kind")

	// ErrZeroByteExpected is a reserved byte that must be 0x00 and was not —
	// `zero s = expect 0x00 s "zero byte expected"` (decode.ml:150).
	//
	// The reference's message text, verbatim, because it is the reference's
	// production. No suite vector asserts this string today: the sites that call it
	// (the 0x40 table form, #51) are all gated constructs, so a gate-off engine
	// declines before reaching the zero byte. Named at its definition site rather
	// than being reported as a generic malformedness — the ErrTrailingData ruling,
	// applied to a string nothing yet reads.
	ErrZeroByteExpected = errors.New("zero byte expected")

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
	//
	// The elem and data ones say **kind**, not flags, and that is the reference's word:
	// `malformed elements segment kind` (decode.ml:1201) and `malformed data segment
	// kind` (:1223). This engine said "flags" at both, which was #88's second and third
	// invented sentinels — and unlike the valtype one they were pure renames, because
	// the *grammar* was already right: the reference dispatches on the same `u32` this
	// decoder reads, and its own accept sets (0..7 for elem, 0..2 for data) are what
	// constexpr.go enforces. Checked against :1159-1201 and :1208-1223 before the
	// strings moved, since a message from the wrong layer is evidence about a missing
	// descent rather than about a spelling (#36's tell).
	//
	// The engine's "flags" reading is not thereby wrong about the *field* — the elem
	// value is genuinely a bitfield and constexpr.go decodes it bit by bit, which the
	// reference's eight-arm switch only expresses positionally. But the spec's word for
	// the malformedness is `kind`, and the sentinel is testimony about the spec.
	//
	// Note the two elem entries are **different productions with confusably similar
	// text**: `elements segment kind` is the segment's leading u32 (:1201), while
	// `element kind` is the one-byte `elem_kind` inside it (:1157). The reference draws
	// that distinction and so must this, which is why the names are Seg/nothing rather
	// than a shared prefix a reader could conflate.
	ErrMalformedElemSegKind = errors.New("malformed elements segment kind")
	ErrMalformedElemKind    = errors.New("malformed element kind")
	ErrMalformedDataSegKind = errors.New("malformed data segment kind")

	// The malformed-form errors of the instruction grammar.
	//
	// ErrMalformedMemopFlags is `require (I32.lt_u flags 0x80l) ... "malformed memop
	// flags"` (decode.ml:327), and the *order* is the fact worth recording: the
	// require fires after the flags LEB has been read, so an overlong or oversized
	// flags encoding reports the LEB error and never reaches here. Four
	// binary-leb128.wast vectors turn on that ordering.
	ErrMalformedMemopFlags = errors.New("malformed memop flags")
	ErrMalformedCatch      = errors.New("malformed catch clause")
	ErrMalformedTypeIndex  = errors.New("malformed type index")

	// ErrTooManyLocals is a function body whose local declarations sum to 2^32 or
	// more (decode.ml:347-348).
	//
	// The check is on the *sum*, at 64 bits, which is the fact two vectors turn on:
	// binary.wast:159 declares 0xFFFFFFFF and 2, and :175 declares four groups of
	// 0x40000000, so a total accumulated at 32 bits would wrap and accept both while
	// each individual count stays legal.
	ErrTooManyLocals = errors.New("too many locals")

	// ErrIllegalOpcode is a byte (or prefix and sub-opcode) the reference's
	// instruction grammar does not define, or defines only to reject.
	//
	// The text is the reference's own and the *rendering* is load-bearing:
	// `illegal s pos b = error s pos ("illegal opcode " ^ string_of_byte b)` with
	// `string_of_byte = "%02x"` (decode.ml:35, 52). binary.wast:1218 expects
	// `illegal opcode ff` — the byte is *inside* the expected string — so for that
	// vector the rendering is oracle-covered, which is the one place the
	// invented-bits class (grave #36) has suite teeth (#38). Everywhere else the
	// harness stops at the sentinel and print-checks are the only cover.
	//
	// This sentinel replaces ErrNonConstantExpr, which existed because the constexpr
	// reader could not tell a nonexistent opcode from a real-but-non-constant one and
	// therefore claimed neither. The generated table (0007) answers the existence
	// question over the whole space, so the verdict is computed rather than declined.
	ErrIllegalOpcode = errors.New("illegal opcode")

	// ErrConstExprRequired is a real instruction, in a constant expression, that is
	// not one of the constant forms.
	//
	// The verdict belongs to *validation*, not to the grammar: the suite asserts
	// `constant expression required` 24 times and every one is assert_invalid, and the
	// reference's `const s` is `instr_block s; end_ s` with const-ness checked nowhere
	// in the decoder (decode.ml:983). Reporting it here is therefore a **layering
	// debt, declared not silent**: the decoder must reject (accept-and-ignore would
	// break the extent and every size check after it) and it now knows enough to name
	// the right verdict, but the layer that ought to own the string is #9's validator.
	// It moves there when the full instruction grammar makes reading-without-judging
	// possible (#39, then #9).
	//
	// What matters for §9 G-3 is the direction: this is an *invalid* string, never a
	// malformed one, so a well-formed module is no longer slandered as malformed —
	// which is precisely what the old blanket rejection risked buying.
	ErrConstExprRequired = errors.New("constant expression required")

	// ErrEndExpected is `end_ s = expect 0x0b s "END opcode expected"`
	// (decode.ml:322) — the terminator a block or expression demanded and did not
	// get.
	//
	// Reachable from a constant expression today by exactly one byte, and the trace
	// is worth stating because the obvious answer is wrong: `instr_block'` *peeks* and
	// stops on 0x05 as well as 0x0b (decode.ml:969), so a bare ELSE never reaches
	// `instr` and never produces the `misplaced ELSE opcode` its arm carries. It
	// reaches `end_`, which wants 0x0b and reports this.
	ErrEndExpected = errors.New("END opcode expected")

	// ErrMisplacedOpcode is a reference `error s pos "..."` arm — an opcode the
	// authority's `instr` defines only in order to reject with its own message.
	//
	// Declared and tracked, not silent (the ErrTrailingData ruling, #6): at bdd7164
	// both such arms are 0x05 and 0x0b, which are exactly the two bytes
	// `instr_block'` stops on, so neither reaches `instr` from a constant expression
	// and this error is unreachable. TestEveryReasonRowIsABlockDelimiter is the
	// tripwire: a third reason arm upstream turns the build red here rather than
	// producing a quietly wrong verdict. The reference's text is carried verbatim
	// after the colon, so the harness's substring match would find it.
	ErrMisplacedOpcode = errors.New("misplaced opcode")
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

// Module is a decoded module: the section-level view, plus the internal form the
// descent retained while recognizing it.
//
// The two coexist rather than one replacing the other, and that is not a transitional
// state. Sections carries the *raw* extents and is what checkCounts and the data-count
// cross-check read — questions about the image, answered on the image. The retained
// fields below are questions about the program. Collapsing them would make the
// count-agreement checks re-derive from a structure built by the very grammar they are
// meant to cross-check.
//
// Retention is partial and every gap is named. The sections whose grammars exist but
// whose contents are not yet kept — tags — say so at their decode sites and cite #7,
// which is the declared-and-tracked form of "not done" (#6 ruling) rather than a silent
// omission. Data segments were such a gap until 0015 and element segments until 0016;
// both are retained now, so this sentence names what is left rather than what it once did. Nothing reads a field that is never written,
// which is the property `deadcode` is checking.
type Module struct {
	Version  uint32
	Sections []Section

	// Types is the type index space, in index order — every accepted comptype, not just
	// the functypes. See CompType on why the struct and array forms take slots they do
	// not fill: skipping them would shift every later index in the all-gates-on lane
	// only, which is a defect the default board cannot see by construction.
	Types []CompType

	// Funcs is one entry per *defined* function — the function section's type indices
	// zipped with the code section's bodies. They are separate sections that
	// checkCounts already requires to agree in length, so zipping them here cannot
	// silently mismatch: a module reaching this point has passed that check.
	//
	// Imported functions are *not* here. They occupy the low function indices, and
	// FuncIndex is what maps an index to one or the other — see its comment for why
	// that is a method rather than a merged slice.
	Funcs []Func

	Imports  []Import
	Exports  []Export
	Tables   []Table
	Memories []Memory
	Globals  []Global

	// Tags is the tag section's payload — one type index per defined tag, in index order
	// (`decode.ml:1082-1087`'s `tag_section = section Custom.Tag (vec (at tag))`, each
	// `tag = TagT (typeuse idx s)`). Retained since #95; before it, section 13 was accepted
	// only by the gate with no payload grammar at all — well-formed by the tracked set,
	// nothing kept.
	Tags []Tag

	// Start is the start section's function index, valid only when HasStart.
	Start    uint32
	HasStart bool

	// Elems is the element section's segments, in index order — the index `table.init` and
	// `elem.drop` name. Retained under 0016's shape rule; see ElemSegment for why its two
	// element forms are kept apart where the reference normalizes them.
	Elems []ElemSegment

	// Datas is the data section's segments, in index order — the index `data.drop` and
	// `memory.init` name. Retained as of 0015; see DataSegment for why it was error-only
	// until #7 became its consumer.
	Datas []DataSegment
}

// ImportedFuncs counts the function imports, which is the offset defined functions
// start at in the function index space.
//
// Computed rather than stored, because a stored count is a second place knowing a fact
// Imports already holds — and the two would drift exactly when a new import kind
// arrives. The loop is over a slice that is empty for most modules.
func (m *Module) ImportedFuncs() int { return m.importedCount(ExternFunc) }

// ImportedMems counts the memory imports, which is the offset defined memories start at
// in the memory index space.
//
// **The same rule as ImportedFuncs, and it had to be paid for separately.** Every extern
// kind has one index space shared between its imports and its definitions, imports first —
// so an interpreter sizing its memory table by `len(Memories)` alone reads the wrong memory
// for every module that imports one, silently. Measured on `memory_grow.wast`, whose module
// imports two memories and then defines `$mem3 3`: `memory.size $mem1` returned 3, which is
// $mem3's page count, and 22 vectors across two files agreed on the wrong answer.
func (m *Module) ImportedMems() int { return m.importedCount(ExternMemory) }

// ImportedTables counts the table imports, which is the offset defined tables start at in the
// table index space.
//
// The third of these, and it needed no new reasoning — which is the point of importedCount being
// shared. The 22-vector lesson ImportedMems records is a fact about *every* extern kind's index
// space, so an interpreter sizing its table slice by `len(Tables)` alone would read the wrong
// table for every module that imports one, exactly as it read the wrong memory.
func (m *Module) ImportedTables() int { return m.importedCount(ExternTable) }

// ImportedGlobals counts the global imports, which is the offset defined globals start at in the
// global index space.
//
// The fourth, and like the third it needed no new reasoning — which is what a shared
// `importedCount` is for. The lesson ImportedMems records (22 vectors agreeing on the wrong
// memory's answer) applies here with a sharper edge, because a global's *initializer* can read an
// earlier global: an interpreter sizing its slice by `len(Globals)` alone would make `(global i32
// (global.get 0))` read a defined global where the module named an imported one, and
// `global.wast:344` is a module whose global 0 is exactly an import.
func (m *Module) ImportedGlobals() int { return m.importedCount(ExternGlobal) }

// ImportedTags counts the tag imports, which is the offset defined tags start at in the tag
// index space — the fifth of these, needing no new reasoning for `importedCount`'s own
// stated reason. #95's own consumer: `Instance.tags` (0022 §3) sizes itself
// `ImportedTags() + len(mod.Tags)`, exactly as `mems` sizes itself from ImportedMems.
func (m *Module) ImportedTags() int { return m.importedCount(ExternTag) }

// importedCount counts the imports of one extern kind.
//
// Shared rather than written once per kind: two loops differing only in a constant is the
// two-places-know-one-fact shape, and the fact here (imports occupy the low indices of their
// kind's space) is one fact about all five kinds.
func (m *Module) importedCount(k ExternKind) int {
	n := 0
	for _, im := range m.Imports {
		if im.Kind == k {
			n++
		}
	}
	return n
}

// DefinedFunc maps a function index to the defined function it names, reporting false
// when the index falls in the imported range or past the end.
//
// A method rather than a merged `[]Func` with placeholder entries for imports, and the
// reason is that an import has no body: a placeholder would be a Func whose Body is nil
// and whose nil is meaningful, which is the shape that makes a nil-deref look like a
// module property. Whether an index is *in range at all* is #9's question — this
// reports what it can find, and the caller decides whether not-found is a validation
// failure or a call into an unlinked import.
func (m *Module) DefinedFunc(idx uint32) (*Func, bool) {
	off := m.ImportedFuncs()
	if idx < uint32(off) {
		return nil, false
	}
	i := int(idx) - off
	if i >= len(m.Funcs) {
		return nil, false
	}
	return &m.Funcs[i], true
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

// peek returns the next byte without consuming it, and false at the end of the image
// — `peek s = if eos s then None else Some (read s)` (decode.ml:23).
//
// The distinction from byte() is the whole reason `instr_block'` can stop on a
// delimiter without eating it: END belongs to the *enclosing* production, so a
// consuming look would leave `end_` nothing to check. It returns a bool rather than an
// error because "no more bytes" is not a failure here — it is one of the two ways an
// instruction sequence legitimately ends.
func (r *reader) peek() (byte, bool) {
	if r.remaining() < 1 {
		return 0, false
	}
	return r.b[r.off], true
}

// skip advances past a byte a caller has already peeked — `skip n s` (decode.ml:20), used
// as `skip 1 s` after a successful `peek` at :264, :268, :275, and :1014.
//
// It exists because the reference has it, and the alternative was worse in a way a linter
// caught: transcribing `peek`-then-`skip 1` as `peek`-then-`_, _ = r.byte()` discards an
// error return that genuinely cannot fire (the peek proved the byte is there), and errcheck
// is right to object — *a discarded error is a claim about reachability that the code does
// not state*. Naming the operation states it: this consumes a byte whose presence is
// already established, so there is no error to handle rather than an error being ignored.
//
// Deliberately no bounds check and no return value. A skip past the end would be a caller
// that skipped without peeking, which is a bug in the caller rather than a malformed
// module — and clamping it would convert that bug into a silent misparse of the next field,
// the failure mode the `either` cursor-reset comment describes. The reference raises EOS;
// here the slice bound is the equivalent backstop, since every read after an over-skip
// finds `remaining() < 1`. (#86.)
func (r *reader) skip(n int) {
	r.off += n
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
// where name's would have none until the module structure retains names.
//
// **That condition has now been met** (#7), and it is discharged by nameString below
// rather than by widening this signature. The paragraph above stays because it is the
// record of what was believed and why — and because the split it describes is still
// live: `name` has callers that only check, and giving them a value to ignore would
// invert the classification it just settled.
func (r *reader) name() error {
	_, err := r.nameString()
	return err
}

// nameString reads a name and returns it as a Go string — the retaining form of `name`.
//
// A `string`, which is a **copy**, and deliberately against this reader's in-place
// posture. Everywhere else a payload aliases the caller's buffer, which is right for
// bytes the engine only compares; a name is a linker key that outlives the decode, so an
// aliased one would make a module's identity depend on the caller not reusing its image.
// The conversion is the copy and it is the cheapest correct thing available in pure Go.
//
// The two methods share one body, so the UTF-8 rule has one definition site. A second
// copy of `utf8.Valid` here is exactly the shape grave #83 keeps taking — one production
// in the reference, transcribed at two call sites, drifting at one.
func (r *reader) nameString() (string, error) {
	b, err := r.byteVec()
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("%w: % x", ErrMalformedUTF8, b)
	}
	return string(b), nil
}

// uleb reads an unsigned LEB128 integer of the given bit width.
//
// GRAVE (0003, grave 1): the malformed-integer taxonomy is width-parameterized,
// not a property of LEB128. `ff ff ff ff 0f` is a *valid* u32 (0xFFFFFFFF) and a
// *malformed* i32 constant — same bytes, different verdict. The predecessor
// folded both malformed classes into one check (`i == 4 && c&0xF0 != 0`) inside a
// u32-only method, so it could report neither correctly nor distinguish them:
// - continuation bit set on the last permitted byte → representation too long
// - payload bits beyond the width set → integer too large
//
// GRAVE (0003, grave 2): the predecessor's ErrLEBTooLong was unreachable — the
// i==4 branch returned before the loop could fall through, so no input of any
// length reached it (verified exhaustively over all 256 fifth-byte values). The
// lesson, for whoever adds the next error constant: an error with no reachable
// path is a missing check wearing a disguise. The two bugs propped each other
// up — the dead constant is why the conflation went unnoticed.
//
// GRAVE (#36): 0003's fix got the taxonomy right and *composed it in the wrong
// order*. Its comment said "test the continuation bit first", which is the
// defect stated as the rule — a byte that is both overlong-by-continuation and
// out-of-range then scores as "too long" where the spec says "too large". The
// authority is the reference interpreter's uN, which checks the range *before*
// consulting the continuation bit:
//
//	let rec uN n s =
//	  require (n > 0) s pos "integer representation too long";
//	  let b = byte s in
//	  require (n >= 7 || b land 0x7f < 1 lsl n) s pos "integer too large";
//	  ... if b land 0x80 = 0 then x else ... uN (n - 7) s
//
// So the width budget is exhausted *before* a byte is read (too long), and the
// range of a byte is judged *when* it is read (too large), independent of whether
// it continues. Two correct predicates in the wrong order is still wrong, and
// order of tests is itself a claim about the spec. Measured, not argued:
// TestLEBMatchesReferenceUN is a differential port of uN/sN over the derived
// disagreement space, and it found 112 disagreements at 32 bits and 126 at 64,
// identically in uleb and sleb.
//
// The loop is structured to mirror uN's recursion rather than to read the last
// byte specially: the "too long" case is now the loop *falling through* its width
// budget, which is what makes ErrLEBTooLong reachable by the honest path instead
// of by a special case.
func (r *reader) uleb(bits uint) (uint64, error) {
	maxBytes := int((bits + 6) / 7)
	var v uint64
	var shift uint
	for i := range maxBytes {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		// Range first, exactly as uN does, and regardless of the continuation bit:
		// on the last permitted byte the payload bits beyond the width must be zero.
		if used := bits - shift; used < 7 && c&0x7F>>used != 0 {
			return 0, ErrLEBOverflow
		}
		v |= uint64(c&0x7F) << shift
		if c&0x80 == 0 {
			return v, nil
		}
		shift += 7
		if i == maxBytes-1 {
			// Width budget exhausted with the continuation bit still set. uN's
			// `require (n > 0)` on the next recursion, reached rather than special-cased.
			return 0, ErrLEBTooLong
		}
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
// Called by decodeLimits: limits min/max are 64-bit fields, which the suite settles
// at binary-leb128.wast:525 (grave #36). This closes #19 — the reader was
// declared-and-tracked with a //nolint:unused for two milestones, and the
// placeholder discipline's intended ending is a production caller retiring the
// suppression, not an allowlist entry outliving it.
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
// Same ordering rule as uleb, and it is the reference interpreter's sN rather than
// the guess uleb's comment used to record (grave #36): the range check runs *before*
// the continuation bit is consulted, so a byte that is both out-of-range and
// overlong is "integer too large". sN's mask form:
//
//	let mask = (-1 lsl (n - 1)) land 0x7f in
//	require (n >= 7 || b land mask = 0 || b land mask = mask) ... "integer too large";
//
// which is this function's two-sided check written the other way round.
func (r *reader) sleb(bits uint) (int64, error) {
	maxBytes := int((bits + 6) / 7)
	var v int64
	var shift uint
	for i := range maxBytes {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		// The payload bits of this byte that fall outside the width must all equal
		// the sign bit that the width does reach — all zero for a positive value,
		// all one for a correct negative sign extension. Checked before the
		// continuation bit, per sN.
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
		v |= int64(c&0x7F) << shift
		shift += 7
		if c&0x80 == 0 {
			// Sign-extend from the last payload bit consumed.
			if shift < 64 && c&0x40 != 0 {
				v |= -1 << shift
			}
			return v, nil
		}
		if i == maxBytes-1 {
			// Width budget exhausted with the continuation bit still set — sN's
			// `require (n > 0)`, reached rather than special-cased.
			return 0, ErrLEBTooLong
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

// DecodeModule decodes a complete module image under v0's default gate posture
// (DefaultFeatures — contract §9 G-1), not the bare zero value: the two are different facts
// and have diverged since #227's SIMD flip. A caller wanting every gate off explicitly
// constructs `(&Decoder{}).DecodeModule(b)` instead.
func DecodeModule(b []byte) (*Module, error) {
	return (&Decoder{Features: DefaultFeatures()}).DecodeModule(b)
}

// DecodeModule decodes a complete module image under d's gate set.
func (d *Decoder) DecodeModule(b []byte) (*Module, error) {
	r := &reader{b: b}

	// Per-decode state, cleared at the start rather than at the end: a reused Decoder
	// that carried the previous module's answer would be an instrument measuring
	// history (#28), and clearing on exit leaves the zero-value path uncovered.
	d.sawDataRef = false
	d.funcTypeIdx = nil
	d.valType, d.blockType, d.blockTypeIdx = NoValType, 0, 0
	d.storageType, d.fieldType = StorageType{}, FieldType{}

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
	// The descent writes retained fields here as it recognizes — see Decoder.m for why
	// the producer is the descent rather than a second pass. Set after the preamble, so a
	// module rejected on its magic or version never gets one.
	d.m = m

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
	// #22, closed. The check needs both halves of a fact neither section knows alone:
	// whether any body referenced the data index space (decodeFuncBody's business) and
	// whether a data count section is present (the module's). Its order is the
	// reference's — after the two count agreements (decode.ml:1295-1301) — because a
	// module can fail more than one of the three and the suite expects the first.
	if d.sawDataRef && !m.hasSection(SectionDataCount) {
		return nil, ErrDataCountRequired
	}
	// The two halves of every Func meet here and nowhere earlier, because the function
	// and code sections are separate grammars and the *function* section comes first.
	// Placed after every verdict above for decodeFuncBody's reason: a module rejected by
	// checkCounts must not leave a zipped form behind.
	d.finishFuncs()
	return m, nil
}

// finishFuncs attaches the function section's type indices to the code section's bodies.
//
// checkCounts has already required the two counts to agree by the time this runs, so the
// pairing cannot silently mismatch — but it is written to survive disagreement anyway,
// pairing only as far as the shorter half. That is not defensive padding: `finishFuncs`
// is called from one place today and the cost of the alternative is a panic on a module
// the decoder has already decided about, which is the worst possible place to learn that
// an ordering assumption moved.
//
// A type index with no body, or a body with no index, is dropped rather than paired with
// a zero. A zero is a *legal* type index, so a fabricated one would be indistinguishable
// from a real one — the invented-evidence class (grave #36) in a field.
func (d *Decoder) finishFuncs() {
	m := d.mod()
	n := min(len(d.funcTypeIdx), len(m.Funcs))
	for i := range n {
		m.Funcs[i].TypeIndex = d.funcTypeIdx[i]
	}
	if len(m.Funcs) > n {
		m.Funcs = m.Funcs[:n]
	}
}

func (m *Module) hasSection(id SectionID) bool {
	for _, s := range m.Sections {
		if s.ID == id {
			return true
		}
	}
	return false
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
// (it needs function bodies), and it is decided by the caller once the bodies have
// been read — see the `sawDataRef` check in DecodeModule. This sentence used to say
// the half was "tracked in #22 rather than guessed at", which stopped being true when
// #39's code-section grammar made it reachable: *a ruling retroactively falsifies
// prose written before it*, and a deferral's citation is exactly the kind of sentence
// that outlives its subject.
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
