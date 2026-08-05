package binary

// The internal form: what the decoder *retains* rather than merely recognizes.
//
// # Why this file exists at all, and why here
//
// Before it, nothing in the codebase could represent a module. 28 of the 29 `decode*`
// functions returned a bare `error` and the 29th returned `(bool, error)` where the bool
// reported whether a section *has* a grammar — so `Module` was `{Version, Sections}` of
// payloads aliasing the input image, which is a *verdict* about bytes and not a program.
// That one missing artifact was behind 93.6% of the board: #7 execution, #9 validation,
// #67's half-2 comparator, and the text encoder's target all wait on it.
//
// It grows out of the **decoder** rather than out of the text parser, and that is 0006's
// load-bearing-spot rule plus 0011's own option-B refusal: the decoder has a conformance
// record (4162 vectors) and the text path has never accepted a module, so shaping the
// representation from the text side would fit it to a producer that cannot yet exercise
// it. 0011's appended ruling states the ordering — internal form first, encoder targets
// it afterward.
//
// # The producer seam, which 0002 left open
//
// 0002 decides the *form* (`[]ins` with pre-decoded immediates and resolved branch
// targets, giant-switch dispatch, `[]uint64` plus a parallel `[]ref`). It does not decide
// who builds it. Two candidates: the descent grows retention as it goes, or a second pass
// re-reads the payloads the first pass aliased.
//
// **The descent, and the precedent is already in the tree.** `sawDataRef` is per-decode
// state on `Decoder`, set from inside `prefixed` at the bottom of the instruction
// grammar, because the question it answers is asked at the module level. Retention is the
// same shape and gets the same answer: one grammar, one traversal, state on the decoder.
// A second pass would be a second grammar over the same bytes — two places knowing the
// binary format, drifting silently, which is precisely the risk 0006 says to prefer away
// from.
//
// The cost of that choice is stated rather than hidden: `decode*` functions that used to
// return only an error now have somewhere to put what they read, so their signatures stay
// error-only and the *fields* land on the decoder's in-progress module. That keeps the
// existing error contract — every one of those 4162 vectors is a rejection, and a
// rejection's path must not change shape — while giving the accept direction a
// destination.

// ValType is a value type, in the spec's own encoding.
//
// A byte-width enum rather than a struct, because the encoding *is* the identity for
// every form this configuration accepts. The values are the spec's negative s7 forms
// folded to their byte encoding, which is what the decoder already reads — so this type
// needs no conversion table and cannot disagree with `decodeValType` about what a form is.
//
// The twelve GC reference forms do not fit a byte, because `(ref ht)` and `(ref null ht)`
// are *parameterized* by a heap type. They are not represented here and they do not need
// to be: `decodeRefType` declines all twelve with a feature-named error while the GC gate
// is off, so no accepted module can hold one. When the gate flips, this type grows a
// parameterized case and the growth is forced by the gate, not by a guess made now — the
// premature-generality trap 0006 names.
type ValType byte

const (
	// NoValType is the sentinel for a form this type cannot represent: the twelve GC
	// reference types, which the GC gate accepts in the all-gates-on lane and which do
	// not fit a byte.
	//
	// It exists because the alternative is worse. `decodeValType`'s out-parameter is
	// written on every accepting path, so an arm that accepted without writing would
	// leave the *previous* read's type standing as this one's answer — an engine
	// reporting a value its input never held, which is grave #36's class relocated from
	// a message into a field. A named sentinel makes the gap loud at the point a
	// consumer reads it, where silence would make it a plausible wrong answer.
	//
	// 0x00 is safe as the value: it is not the encoding of any type, and it is the zero
	// value, so a field nothing wrote reads as "unrepresentable" rather than as i32.
	NoValType ValType = 0x00

	I32  ValType = 0x7F
	I64  ValType = 0x7E
	F32  ValType = 0x7D
	F64  ValType = 0x7C
	V128 ValType = 0x7B

	// FuncRef and ExternRef are Wasm 2.0's two ungated reference types. The other
	// twelve reference forms are GC's and arrive with that gate; a module using one is
	// declined by `decodeRefType` before it reaches here.
	FuncRef   ValType = 0x70
	ExternRef ValType = 0x6F
)

func (t ValType) String() string {
	switch t {
	case NoValType:
		// Its own case, and not folded into the "unknown" default: the two are different
		// facts and the lint that required this arm was right to. *Unknown* means a byte
		// this type has no name for; *unrepresentable* means a form the spec defines and
		// this type deliberately cannot hold — the twelve GC reference types. A consumer
		// printing "unknown" for one of those would report a defect in the module where the
		// truth is a limitation of the engine's representation, which is grave #36's class
		// (an engine lying about its input) in a String method.
		return "unrepresentable"
	case I32:
		return "i32"
	case I64:
		return "i64"
	case F32:
		return "f32"
	case F64:
		return "f64"
	case V128:
		return "v128"
	case FuncRef:
		return "funcref"
	case ExternRef:
		return "externref"
	}
	return "unknown"
}

// IsRef reports whether values of this type live in the reference array rather than the
// numeric one.
//
// **0002 pins this as a consequence, not a detail.** A Go pointer stored in a `uint64` is
// invisible to the garbage collector and pure Go (no cgo) offers no escape hatch, so the
// value stack is two parallel arrays from the first line of interpreter code — and the
// predicate that decides which array a slot uses has to exist before any opcode touches
// the stack. Adding it later means auditing every stack-touching opcode.
func (t ValType) IsRef() bool {
	return t == FuncRef || t == ExternRef
}

// FuncType is a function's signature: the params and results of `functype`.
//
// Two slices rather than a count plus a flat array, because the validator asks about them
// separately and an arity is derivable from a slice while a slice is not derivable from
// an arity.
type FuncType struct {
	Params  []ValType
	Results []ValType
}

// CompKind distinguishes the three composite type forms — `comptype` (decode.ml:250-259).
type CompKind byte

const (
	CompFunc CompKind = iota
	CompStruct
	CompArray
)

func (k CompKind) String() string {
	switch k {
	case CompFunc:
		return "func"
	case CompStruct:
		return "struct"
	case CompArray:
		return "array"
	}
	return "unknown"
}

// CompType is one entry in the type index space.
//
// **This is a slice of comptypes rather than of functypes, and index alignment is why.**
// GC's `struct` and `array` forms occupy type indices exactly as `func` does, so a
// `[]FuncType` that skipped them would silently shift every later index — and it would do
// so *only* in the all-gates-on lane, where GC accepts them. That is the worst shape a
// defect can have here: correct on the default board, wrong in the lane whose whole job is
// to catch what the default board cannot see. So every accepted comptype takes a slot.
//
// The struct and array *contents* are not retained — fieldtypes have no representation yet,
// and nothing consumes them. Declared here and at decodeCompType rather than left silent
// (#6's declared-and-tracked ruling); #7 is the tracking issue, and the growth is forced
// when a GC consumer arrives rather than guessed at now.
type CompType struct {
	Kind CompKind

	// Func is the signature, valid only when Kind is CompFunc.
	Func FuncType
}

// Func is one function: its type index, its declared locals, and its body.
//
// The type index rather than a resolved *FuncType, and the reason is layering: index
// *validity* is #9's question, so the decoder records what the module said and the
// validator decides whether it names anything. Resolving here would make the decoder
// reject a module for a reason the spec calls invalid rather than malformed — the
// wrong-layer error the `constant expression required` debt already documents.
type Func struct {
	TypeIndex uint32

	// Locals is the declared local **groups**, one entry per `(count, valtype)` run in the
	// image — *not* one entry per local.
	//
	// # It was the flattened vector, and 30 bytes bought a 4 GiB lunch
	//
	// The wire form is runs, and this field used to hold them expanded: one `ValType` per
	// local, flattened by `decodeLocals` once the `too many locals` sum check passed. That
	// check is the reference's (`total >= 1<<32`) and it is right about the *verdict* — but
	// `ValType` is a byte, so a body declaring `0xFFFFFFFE` locals is a **legal** module
	// that the old field expanded into 4.00 GiB from a 30-byte image, measured. Grave #138.
	//
	// **No vector could see it, in either direction**: the module is spec-legal, this engine
	// agreed with the reference on accepting it, and the only witness was a resource
	// measurement. The suite is the oracle for verdicts and is silent on cost by
	// construction, which is why the fuzzer found it and three years of boards would not
	// have. The hazard was even *stated* one comment away — the old prose explained that
	// flattening waits for the sum check so four billion entries are not allocated for a
	// module the next line refuses, which is true of `0xFFFFFFFF` and silent about
	// `0xFFFFFFFE`, the neighbour that next line **accepts**. The rule was right; only its
	// extent was wrong, and a boundary comment that does not state its extent reads as a
	// proof. (Ruling: Scott, PR #137.)
	//
	// # So retention is the wire form, which is the truer reading anyway
	//
	// Keeping runs is not merely the cheap fix. The image says "n of this type" and this now
	// says the same thing, so decode cost is proportional to the *compressed* size — the
	// decoder stops paying execution's rent. A consumer that genuinely needs 4Gi slots bills
	// itself, where the cost can be bounded or refused on execution's terms — `interp` does
	// exactly that, and it refuses as an **engine limit** rather than as a decode error or a
	// trap. Not a trap specifically: a trap is a spec-defined outcome, so reporting one would
	// have this engine claim the module traps when the spec says it runs. The module is
	// well-formed and this phase cannot run it, which is precisely the third category.
	//
	// `TotalLocals` is the flat count, and `EachLocal` iterates the flat reading for the
	// consumers that want one — both without materializing anything.
	Locals []LocalGroup

	// Body is the internal form: `[]Instr` with immediates pre-decoded and branch
	// targets resolved to indices in this slice (0002, Q1 option B).
	Body []Instr
}

// LocalGroup is one `(count, valtype)` run of a body's local declarations, exactly as the
// image spells it.
//
// **Count is a uint32 and deliberately not narrowed**, because narrowing it here would move
// the `too many locals` verdict out of the decoder and into this type's constructor — and
// the verdict belongs where the reference puts it, on the 64-bit sum across all groups. A
// single group's count is legal up to 0xFFFFFFFF; it is the *sum* that is bounded.
type LocalGroup struct {
	Count uint32
	Type  ValType
}

// TotalLocals is the flat local count: the sum of the groups' counts.
//
// Returns a uint64 because that is the width the sum is *computed* at — `decodeLocals`
// rejects a sum at or above 2^32, so every retained Func's total fits in 32 bits, and
// returning the wider type keeps this function honest for a hand-built Func that never went
// through the decoder. A consumer that needs an int says so at its own call site, where the
// bound it is checking against lives.
func (f *Func) TotalLocals() uint64 {
	var total uint64
	for _, g := range f.Locals {
		total += uint64(g.Count)
	}
	return total
}

// EachLocal yields every local's type in index order — the flat reading, without the flat
// slice.
//
// An iterator rather than a `[]ValType` accessor, and that is the whole point of #138: an
// accessor returning the flattened vector would put the 4 GiB allocation back, one call
// deeper and harder to see. Anything wanting the flat reading gets it a value at a time and
// pays only for what it consumes; a consumer that must materialize slots checks its own
// bound against TotalLocals *first* and then fills them.
func (f *Func) EachLocal(yield func(idx uint32, vt ValType) bool) {
	var idx uint32
	for _, g := range f.Locals {
		for range g.Count {
			if !yield(idx, g.Type) {
				return
			}
			idx++
		}
	}
}

// Instr is one instruction in the internal form.
//
// Fixed-width and immediate-carrying, which is the whole content of 0002's Q1: the
// measurement that decided it (`internal/interp/dispatchbench`, n=10 with benchstat)
// found rewrite **immune to immediate width** (13.30µs vs 13.32µs, inside the bands)
// where in-place pays 14% for multi-byte LEBs — and found the side-table compromise
// *slowest of the three*, because splitting the program across two arrays costs two cache
// lines per instruction.
//
// The `Imm` pair is deliberately not a variant type or an `any`. Every immediate shape the
// decoder reads fits two 64-bit words — an index, a signed constant, a memarg's
// offset-and-flags, a block's arity-and-target — and the opcode says which reading
// applies, exactly as it says which stack effect applies. An interface here would
// allocate per instruction and defeat the measurement that chose this form.
type Instr struct {
	// Op is the opcode, or the sub-opcode for a prefixed instruction. Prefix carries
	// the prefix byte; a single-byte instruction leaves it zero.
	//
	// **A uint32, not a byte, and that was a measurement rather than a preference**
	// (grave #101). The
	// first version of this field was a byte, on the reasonable-sounding ground that an
	// opcode is one — and the 0xfd sub-table reaches **0x113 (275)**, because SIMD has
	// more than 256 instructions. A byte would have truncated `v128.load32_zero` and
	// friends into *different instructions than the module contains*, silently, on valid
	// input: an accept-direction defect no board can see, since every affected vector is
	// one the suite expects to pass. Found by printing the sub-tables' maxima instead of
	// trusting the word "opcode" (`opTableFB` max 0x1e, `opTableFC` 0x11, `opTableFD`
	// **0x113**). TestPrefixedSubOpcodesFitOp is the control, scoped to every row of
	// every region rather than to the one that overflowed.
	Op     uint32
	Prefix byte

	// Imm0 and Imm1 are the pre-decoded immediates, read according to Op. Two words
	// rather than one because no immediate shape the table defines needs three and
	// several need two (memarg, br_table's default plus count, if's two arms).
	Imm0 uint64
	Imm1 uint64
}

// The blocktype encoding used by Instr.Imm0 for the four structural arms.
//
// A blocktype is three disjoint things — a type index, the empty result type, or a single
// valtype — and it has to fit one immediate word. The tags sit above 2^32 because a type
// index is read as `s33` and is non-negative when it *is* an index, so no legal index can
// collide with either tag. That is disjointness by construction rather than by an
// assumption about how many types a module declares.
const (
	// blockTypeEmpty is the `0x40` form: no parameters, no results.
	blockTypeEmpty uint64 = 1 << 33

	// blockTypeValType tags a single-result blocktype; the low bits hold the ValType.
	blockTypeValType uint64 = 1 << 34
)

// BlockType reads a structural instruction's `Imm0` back into the three cases the
// encoding above packs into it.
//
// **An accessor rather than exported constants, because the packing is this package's fact
// and not its consumers'.** The interpreter needs a block's arity — how many values the
// block yields, so `br` can keep exactly that many and discard the rest — and it needs to
// ask without knowing that the tags live at bits 33 and 34. Exporting the two constants
// would put the decoding rule in every consumer that reads a blocktype, which is the
// two-places-know-one-fact shape; a function keeps it here, where `decodeBlockType` writes
// it, so the writer and the reader cannot drift.
//
// The three returns are disjoint by construction and the caller must branch on them in this
// order — `empty` first, then `valType`, then `typeIdx` — because only the tags distinguish
// a tagged word from an index, and an index of 0 is legal.
func BlockType(imm0 uint64) (typeIdx uint32, valType ValType, empty bool) {
	switch {
	case imm0 == blockTypeEmpty:
		return 0, 0, true
	case imm0&blockTypeValType != 0:
		return 0, ValType(imm0 & 0xFF), false
	default:
		return uint32(imm0), 0, false
	}
}

// Global is one global: its type, mutability, and initializer.
type Global struct {
	Type    ValType
	Mutable bool

	// Init is the constant expression, in the same internal form as a function body.
	// One form for both, because the reference's `const` production *is* the full
	// instruction grammar (decode.ml:983) — const-ness is a validation fact, which is
	// why `ErrConstExprRequired` is a declared layering debt rather than a grammar rule.
	Init []Instr
}

// Import is one import: its two names and what kind of thing it brings in.
type Import struct {
	Module string
	Name   string
	Kind   ExternKind

	// Index is the type index for a function import. The other kinds carry their
	// descriptor in the module's own index spaces, which the decoder appends to as it
	// reads them — an imported function occupies function index 0 before any defined
	// function does, and that ordering is the validator's and the interpreter's to rely
	// on.
	Index uint32
}

// Export is one export: a name and what it names.
type Export struct {
	Name  string
	Kind  ExternKind
	Index uint32
}

// ExternKind is the kind byte shared by imports and exports.
type ExternKind byte

const (
	ExternFunc   ExternKind = 0x00
	ExternTable  ExternKind = 0x01
	ExternMemory ExternKind = 0x02
	ExternGlobal ExternKind = 0x03
	ExternTag    ExternKind = 0x04
)

func (k ExternKind) String() string {
	switch k {
	case ExternFunc:
		return "func"
	case ExternTable:
		return "table"
	case ExternMemory:
		return "memory"
	case ExternGlobal:
		return "global"
	case ExternTag:
		return "tag"
	}
	return "unknown"
}

// Limits is a table's or memory's size bounds.
//
// Min and Max are 64-bit, and that is the suite's ruling rather than future-proofing:
// `binary-leb128.wast:525` is a memory32 whose min is a ten-byte LEB with unused bits set
// and wants `integer too large`, which only a 64-bit read reports (grave #36). A memory32
// limit above 2^32 therefore *decodes*, and rejecting it is #9's job.
type Limits struct {
	Min    uint64
	Max    uint64
	HasMax bool

	// Addr64 is set when the flags byte selected the memory64 address type (the 0x04-0x07
	// range), making this memory's or table's addresses i64 rather than i32.
	//
	// **On Limits rather than on Memory, because it is read from the limits flags** — the
	// same byte HasMax comes from — and because table64 will want the identical field from
	// the identical position.
	//
	// Retained as of 0015 because it governs the **size** limit, not the effective-address
	// computation. `memory.ml:27`'s `valid_size` caps an i32 memory at `0xffff` pages and an
	// i64 memory at nothing, and both `alloc` and `grow` consult it — so the width decides
	// which allocations and which `memory.grow` deltas are legal.
	//
	// It is deliberately *not* consulted when computing an address, and the first draft of
	// this comment said the opposite: `value.ml:292` **zero-extends** an i32 index to 64
	// bits (`extend_i32_u`) and `effective_address` then adds the static offset in 64 bits
	// with an unsigned-overflow check. There is no 32-bit wrapping at any width, so an
	// address path that branched on this field would be inventing a distinction the
	// reference does not make. Read from the executable rather than from the word
	// "memory64" — comments and ADRs are testimony, and the executable outranks.
	//
	// It cannot be recovered later: the flags are gone, and `Min > 1<<32` is the wrong
	// question because a memory64 of one page is still 64-bit addressed.
	Addr64 bool
}

// Table is one table: its element type and limits.
type Table struct {
	ElemType ValType
	Limits   Limits
}

// Memory is one memory: its limits, which carry its address type (see Limits.Addr64).
type Memory struct {
	Limits Limits
}

// DataSegment is one data segment: where it goes, and what goes there.
//
// **Retained as of 0015**, which is the consumer-forced retention pre-registered when the wat
// encoder's round-trip witness was found blind: `decodeDataSegment` was error-only, so nothing
// in the codebase could represent a module's data, so the only available witness was
// byte-level over `Section.Payload`. #7 executing memory tests is the consumer that knocked.
type DataSegment struct {
	// Passive is set for the 0x01 mode: the segment is not copied at instantiation and is
	// only reachable through `memory.init`. Active segments (modes 0x00 and 0x02) are
	// copied at time zero and may trap doing it.
	Passive bool

	// MemIndex is the memory the segment initializes, 0 for the implicit-index mode.
	// Meaningless when Passive.
	MemIndex uint32

	// Offset is the offset constant expression, in the same internal form as a function
	// body — one form for both, for Global.Init's reason. Nil when Passive.
	Offset []Instr

	// Init is the segment's bytes, aliasing the decoder's image (the in-place posture).
	// Never nil for a segment that decoded, and empty is legal: `(data "")` is a real
	// module in memory64.wast.
	Init []byte
}

// OpMnemonic returns the reference's constructor name for a single-byte opcode, and whether the
// table has a row for it.
//
// **Exported so that a consumer's hand-written table can be cross-checked against this one**,
// which decision 0014 already made legitimate by promoting the mnemonic from "a label" to a
// fact. The consumer it exists for is `internal/interp`'s `memops`: the load/store family's
// width, signedness, and slot type are all recoverable from `i64_load16_s`, so a control can
// parse the name and compare, rather than trusting 23 hand-written rows whose errors would be
// accept-direction and invisible on the board (§9 G-3).
//
// Single-byte only, because that is the region the consumer needs; a prefixed accessor is worth
// adding when something asks, not before.
func OpMnemonic(op uint32) (string, bool) {
	info, ok := opTable[op]
	if !ok {
		return "", false
	}
	return info.mnemonic, true
}
