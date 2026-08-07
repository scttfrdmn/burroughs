package text

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// decodeForTest reads an encoder's output with the gates the *encoder* can produce turned on.
//
// SIMD specifically, and this is not a convenience. `v128` is a number type the parser accepts and
// this encoder writes (0x7B), while `decodeVecType` declines it with the SIMD gate off — so a
// default-gated round trip of a v128 signature fails for a reason that is about the decoder's
// configuration and says nothing about the encoder. Naming the gate here rather than dropping v128
// from the tables keeps the encodable set and the checked set the same set.
//
// **Memory64 is on for the same reason, and it was added because a defect probe passed.** Swapping
// `flags |= 0x04` for `0x02` — writing the *shared* bit where the 64-bit bit belongs — left every
// row green, which said the tables had no row exercising an i64 addrtype at all. They could not
// have: `(memory i64 1)` encodes to flags 0x04 and `decodeLimits` declines that with Memory64 off,
// so the row would have failed for the decoder's configuration rather than for the encoder. The
// retained `addr64` field was written by the emitter and checked by nothing — one bit of every
// limits flags byte, unasserted. Found by budgeting for the falsification to *pass*, which is the
// outcome the exercise exists for.
//
// GC stays **off** on purpose: it is the frontier `encodableOrErr` refuses at, so turning it on
// would test bytes this encoder does not emit. Threads stays off for a different reason — the text
// grammar has no `shared` arm (parser.mly:466-468), so no wat source can denote a shared memory and
// there is nothing for the gate to admit. The distinction is worth keeping: SIMD and Memory64 are on
// because the encoder *can* emit them, GC is off because it cannot yet, and Threads is off because
// no input can ask for it.
//
// **ExceptionHandling is on by that rule rather than by a new decision**, and the derivation is worth
// showing because it is the rule doing work rather than being restated. The text grammar has a `tag`
// arm in `externtype` (parser.mly:1230-1236) with no gate on it, so wat source *can* ask for a tag
// import; the emitter writes its kind byte 0x04 and attribute (encode.ml:191); and `decodeImport`
// declines 0x04 with the gate off. That is the SIMD case exactly — a construct the encoder emits
// meeting a decoder configured not to read it — so the row would fail for the decoder's configuration
// and say nothing about the encoder. It is *not* the GC case, which is a frontier the encoder refuses
// at, and not the Threads case, which no input can reach.
//
// **MultiMemory joined the list the same way — by a row failing, not by reasoning ahead.** The memarg
// row carrying an explicit memory index (#8) was written with a comment claiming bit 6 needed no
// gate to *decode*, on the ground that `memopIndex` records its decline rather than returning it. The
// row then failed at this function: `release()` returns the recorded decline once the body's grammar
// completes, so a deferred decline is still a decline. Third derivation of one rule and third time the
// prose was wrong until an input said so — the text grammar's `idx_opt` in a memarg (parser.mly:596)
// has no gate, so wat source can write `(i32.load 1 …)`, the emitter sets bit 6, and `memopIndex`
// declines it with the gate off. SIMD's case exactly.
// sectionPayload returns a decoded module's section payload by id.
//
// **Through the decoder's own segmentation, never by scanning the image.** A test that walked the bytes
// looking for an id would be a second section reader — one whose bugs would be indistinguishable from
// the encoder's, and which would find an `11` inside a payload. `Module.Sections` is what the descent
// recorded, so the extent is the decoder's and only the content is under assertion.
//
// Not `hasSection`, which is unexported and answers a different question; this needs the bytes. The
// found bool rather than a nil payload, because an *empty* section is a legal thing to have written and
// a distinguishable defect from having written none — `writer.section`'s comment makes the same point
// from the other side.
func sectionPayload(m *binary.Module, id binary.SectionID) ([]byte, bool) {
	for _, s := range m.Sections {
		if s.ID == id {
			return s.Payload, true
		}
	}
	return nil, false
}

// blockTypeImm and blockTypeValTypeImm are the packed `Imm0` words a want column states for a
// structural instruction — the empty form, and the single-valtype form.
//
// **Written as literals here, not read out of `binary`, because the packing constants are
// deliberately unexported** (`BlockType`'s comment: the rule is that package's fact, and exporting
// the constants would put the decoding in every consumer). A `want` column needs the *forward*
// direction, which no exported function supplies, so these two are a second reading of
// `module.go:247-253` — `1 << 33` for empty, `1 << 34 | uint64(t)` for a valtype — and they are
// checked against `binary.BlockType` by TestBlockTypeFormsMatchTheReference rather than trusted.
// A literal that has drifted from the packing would otherwise make every block row below wrong in
// the same direction, which is the failure mode a want column exists to not have.
//
// Note what neither can be: **0**. An `Imm0` of 0 is type index 0, a legal and different
// blocktype, so `(block)` cannot be written as `{Op: 0x02}` and a row that did would be asserting
// a signature the text does not name.
const blockTypeImm uint64 = 1 << 33

func blockTypeValTypeImm(t binary.ValType) uint64 { return 1<<34 | uint64(t) }

func decodeForTest(t *testing.T, b []byte) *binary.Module {
	t.Helper()
	d := &binary.Decoder{Features: binary.Features{
		SIMD: true, Memory64: true, ExceptionHandling: true, MultiMemory: true,
	}}
	m, err := d.DecodeModule(b)
	if err != nil {
		t.Fatalf("the encoder produced % x, which the decoder rejects: %v", b, err)
	}
	return m
}

// encodableModules are wat modules this encoder can write in full, with the type index space each
// denotes stated independently of the encoder.
//
// **The want column is written from the text, not from the encoder's output.** That is the whole
// value: a round trip alone asserts the encoder and decoder agree, which they would even if both
// were wrong about what `(param i32 f32)` means. The want column is a second reading of the wat, by
// hand, and it is what makes this a check rather than a tautology.
//
// The implicit-type rows are the ones that would be lost to a re-`runDeferred`: `(type (func))`
// plus a distinct inline signature must yield **two** slots, and interning must reuse rather than
// append for an inline signature equal to an existing type. Neither is visible in a module with one
// type, which is why both shapes are here.
// The memory and table columns are `nil` on the type-only rows and vice versa, which is deliberate:
// one table over all encodable modules, so a section this emitter writes for a module that should
// have none is a failure rather than an unchecked case. A per-section table would have no row saying
// `(module (type (func)))` has *no* memory section.
//
// **`wantFuncs` states the body, and the body's last instruction is always `end`.** The text does
// not spell it: `encode.ml`'s code writer calls `end_ ()` after the body (:1029-1039), so every
// function's bytes finish with an explicit `0x0b` and the decoder retains it in `Body`. A want column
// that omitted it would be asserting the encoder's output against a reading of the *text* that is
// wrong about the format — so it is written out on every row, and a `(func)` with an empty body is
// one instruction rather than none.
// **Sections 11 and 12 are asserted as payload bytes, and that is forced rather than chosen.**
// `binary.Module` has no `Datas` field: `decodeDataSegment` reads the section's grammar and retains
// nothing, citing #7. So the round trip *cannot see section 11* — an emitter that dropped it, or wrote
// the wrong mode flag, or put the payload before the offset, would decode clean and every column above
// would agree. That is the discard-blindness the memarg rows already hit twice (alignment, the
// address-type bit) and it is temporary: #7's execution of the memory tests forces `Module` to retain
// segments, and this column becomes a structured one then.
//
// Until it does, the witness is the bytes, read out of `Section.Payload` — the decoder's own
// segmentation of the image, so the extent is not this test's arithmetic and only the *content* is
// under assertion. Written by hand from the format, which makes it the second reading the rest of the
// table is: `encode.ml`'s `data` is `mode-flag, [index], [const expr], vec(byte)` (:1092-1101) and
// nothing here calls the encoder to find out what it should have said. Reconstructing the flag with a
// helper that branched on the mode would be an echo of the code under test (grave #106).
//
// `nil` asserts the section is **absent**, which is a real assertion and not an unchecked case: a
// segment-less module that emits an empty section 11, or a segment-only module that emits a section 12,
// is wrong in the way that costs a decode — `data count section required` and its mismatch sibling.
var encodableModules = []struct {
	src         string
	want        []binary.CompType
	wantTabs    []binary.Table
	wantMems    []binary.Memory
	wantImports []binary.Import
	wantExports []binary.Export
	wantFuncs   []binary.Func
	// wantGlobals is section 6's *defined* globals, in source order. An imported global is not here —
	// it is a `wantImports` row — which is the population split `context.globalDefs` records.
	//
	// **A structured column rather than payload bytes, and the difference from sections 9 and 11 is that
	// `binary.Global` loses nothing.** Type, mutability and the whole initializer are retained
	// (`constexpr.go:30`), and section 6 has no mode flag, no index and no family choice for a normalizing
	// pair of encodings to hide inside — so there is no fact here that decoding discards, which is the
	// exact condition the byte columns exist for. Asserting bytes anyway would be pinning this encoder's
	// LEB widths, which writer.go's header says is not the criterion.
	wantGlobals []binary.Global
	// wantElemSec is section 9's payload: the segment count, then each segment. nil means no section 9.
	//
	// Bytes for `wantDataSec`'s reason, and section 9's case is the stronger one. **This comment used
	// to say `decodeElemSection` "retains nothing into" `ElemSegment`, which was already false when it
	// was written** (`module.go:560`, retained under 0016) — see the `withElem` floor below for the
	// measurement that replaced it. What survives the correction is the operative half: the retained
	// struct normalizes flags 0/2 and 4/6 to identical values, so an encoder that wrote the
	// explicit-table form where the implicit one belongs, or an elemkind byte where a reftype belongs,
	// decodes clean and every other column here agrees. These rows are the only instrument over the
	// flag byte, which is why they get their own floor below.
	wantElemSec []byte
	// wantDataSec is section 11's payload: the segment count, then each segment. nil means no
	// section 11.
	wantDataSec []byte
	// wantDataCountSec is section 12's payload, which is a bare `u32` count and not a vector
	// (`section 12 len (List.length datas)`, encode.ml:1109). nil means no section 12 — and whether
	// there is one is a question about *instructions*, never about this list's length.
	wantDataCountSec []byte
}{
	{src: `(module)`},
	{src: `(module (type (func)))`, want: []binary.CompType{
		{Kind: binary.CompFunc},
	}},
	{src: `(module (type (func (param i32))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
	}},
	{src: `(module (type (func (result i64))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I64}}},
	}},
	// Param order and result order, both, in one signature — a reversal in either would still
	// round-trip if the writer and reader reversed together, and the want column is what catches it.
	{src: `(module (type (func (param i32 f32) (result f64 i64))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{
			Params:  []binary.ValType{binary.I32, binary.F32},
			Results: []binary.ValType{binary.F64, binary.I64},
		}},
	}},
	// A named type. The name is a parse-time binding and must leave no trace in the image.
	{src: `(module (type $t (func (param f32))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.F32}}},
	}},
	// All five number types, in one place, so a transposed byte in the table is a single failure
	// rather than a missing row.
	{src: `(module (type (func (param i32 i64 f32 f64 v128))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{
			binary.I32, binary.I64, binary.F32, binary.F64, binary.V128,
		}}},
	}},
	// The two ungated reference types, and the two abbreviations that denote them. `funcref` and
	// `(ref null func)` are the *same type* — the parser normalizes the abbreviation — so all four
	// params here are exactly two distinct types, and an encoder that treated the spellings as
	// distinct would produce four different bytes.
	{
		src: `(module (type (func (param funcref externref (ref null func) (ref null extern)))))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{
			binary.FuncRef, binary.ExternRef, binary.FuncRef, binary.ExternRef,
		}}}},
	},
	// Multiple results, which is a Wasm 2.0 feature the encoder gets for free from `vec` and which
	// a one-result assumption would silently truncate.
	{src: `(module (type (func (result i32 i32 i32))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{
			binary.I32, binary.I32, binary.I32,
		}}},
	}},
	// Two explicit types, so index order is asserted rather than assumed.
	{src: `(module (type (func (param i32))) (type (func (param i64))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I64}}},
	}},
	// A `(rec …)` group. Its members occupy ordinary type indices, and with GC off a rec group of
	// functypes is still a legal spelling — so this asserts the group does not become one slot.
	{src: `(module (rec (type (func (param i32))) (type (func (param i64)))))`, want: []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I64}}},
	}},
	// `(sub …)`'s supertype list is read and discarded (subtype's comment), so the encoded type is
	// the inner comptype and the parents leave no bytes.
	{src: `(module (type (func)) (type (sub 0 (func (param i32)))))`, want: []binary.CompType{
		{Kind: binary.CompFunc},
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
	}},

	// # Memories (#8)
	//
	// The want column is read from the wat: `(memory 1)` is min 1, no max, 32-bit — so flags 0x00.
	{src: `(module (memory 1))`, wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}}},
	// A maximum, which is flag bit 0 — and the *presence* is asserted separately from the value,
	// because `HasMax` false with `Max` 0 and `HasMax` true with `Max` 0 are different modules.
	{src: `(module (memory 1 2))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 1, Max: 2, HasMax: true}},
	}},
	// Max equal to min, which is what the `(data …)` sugar would produce and what an encoder writing
	// `hasMax` from `max != 0` would get wrong in the other direction.
	{src: `(module (memory 0 0))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 0, Max: 0, HasMax: true}},
	}},
	// The explicit i32 addrtype, which is the *same type* as the empty arm — so flags stay 0x00 and
	// an encoder that set bit 2 on any explicit addrtype would fail here rather than on i64.
	{src: `(module (memory i32 1))`, wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}}},
	// Two memories, so index order is asserted. Multi-memory is a tracked gate and the *text* grammar
	// admits two `(memory …)` fields regardless — the decoder's own multi-memory gate governs the
	// binary side, and this row is what would fail if it did not.
	{src: `(module (memory 1) (memory 2 3))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 1}},
		{Limits: binary.Limits{Min: 2, Max: 3, HasMax: true}},
	}},
	// A named memory: the name is a parse-time binding and must leave no trace in the image.
	{src: `(module (memory $m 1))`, wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}}},
	// A minimum needing a multi-byte LEB, so the writer's u64 is exercised past one byte in a real
	// section rather than only in writer_test's unit rows.
	{src: `(module (memory 65536))`, wantMems: []binary.Memory{{Limits: binary.Limits{Min: 65536}}}},
	// The i64 addrtype, which is flags bit **2**. Added because its absence let a defect probe pass:
	// see decodeForTest for the measurement.
	//
	// **These rows now assert the bit directly, and the change is a retention landing, not a
	// strengthening of the test.** The paragraph here used to say `Memory` carries no address-type
	// field of its own, so the want column could only state the limits and the bit was asserted
	// *indirectly* — by two defect probes failing on this row. `Limits.Addr64` exists as of #7's
	// memory work, because `memory.ml:27`'s `valid_size` caps an i32 memory at 0xffff pages and an
	// i64 memory at nothing, so the interpreter has to know which it has. The moment it was
	// retained these five rows failed, which is the round-trip witness doing precisely its job: a
	// newly-kept field is a newly-checkable fact, and an expectation that predates the field is
	// silently weaker than it reads.
	{src: `(module (memory i64 1))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 1, Addr64: true}},
	}},
	{src: `(module (memory i64 1 4))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 1, Max: 4, HasMax: true, Addr64: true}},
	}},
	// A minimum above 2^32, which *only* an i64 memory can have — and which is the reason `limits`
	// reads `nat64`. A 32-bit read would have truncated this silently.
	{src: `(module (memory i64 4294967296))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 4294967296, Addr64: true}},
	}},

	// # Tables (#8)
	//
	// `(table 1 funcref)` is min 1, no max, element type funcref — and note the emitted field order
	// is reftype-then-limits, the reverse of the text's.
	{src: `(module (table 1 funcref))`, wantTabs: []binary.Table{
		{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
	}},
	{src: `(module (table 1 2 externref))`, wantTabs: []binary.Table{
		{ElemType: binary.ExternRef, Limits: binary.Limits{Min: 1, Max: 2, HasMax: true}},
	}},
	// `funcref` and `(ref null func)` are the same type, so these two rows must produce the same
	// element byte — the abbreviation-normalization assertion, at the table's element position.
	{src: `(module (table 0 (ref null func)))`, wantTabs: []binary.Table{
		{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 0}},
	}},
	{src: `(module (table $t 3 funcref))`, wantTabs: []binary.Table{
		{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 3}},
	}},
	// A table's own i64 addrtype — the same flag bit, at the other construct, because `addrtype` is
	// shared between `tabletype` and `memorytype` and an emitter could get one right and the other
	// wrong.
	{src: `(module (table i64 1 funcref))`, wantTabs: []binary.Table{
		{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1, Addr64: true}},
	}},
	// Two tables, index order, with different element types — a transposed element would swap them.
	{src: `(module (table 1 funcref) (table 2 externref))`, wantTabs: []binary.Table{
		{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		{ElemType: binary.ExternRef, Limits: binary.Limits{Min: 2}},
	}},

	// # Globals (#8)
	//
	// The want column is read from the wat, and for a global that means three independent facts: the
	// valtype, the mutability byte, and the initializer's instructions. Every row states all three,
	// because the two bytes are written in the *reverse* of the text's order and an encoder that
	// transposed them writes `00 7f` — which the decoder reads as an `i32`-typed… no, as a *valtype*
	// `0x00`, and rejects. The lucky failure; `mutability` inverted is the unlucky one, and only this
	// column sees it.
	{
		src:         `(module (global i32 (i32.const 7)))`,
		wantGlobals: []binary.Global{{Type: binary.I32, Init: []binary.Instr{{Op: 0x41, Imm0: 7}, {Op: 0x0b}}}},
	},
	// `(mut …)`, the other value of the one byte this section has an arm for. Written beside the
	// immutable row rather than alone, because `mutability` writing a constant would pass either row on
	// its own — the pair is the assertion.
	{
		src: `(module (global (mut i64) (i64.const 0)))`,
		wantGlobals: []binary.Global{
			{Type: binary.I64, Mutable: true, Init: []binary.Instr{{Op: 0x42, Imm0: 0}, {Op: 0x0b}}},
		},
	},
	// A named global: the name is a parse-time binding and must leave no trace in the image.
	{
		src:         `(module (global $g f32 (f32.const 0)))`,
		wantGlobals: []binary.Global{{Type: binary.F32, Init: []binary.Instr{{Op: 0x43, Imm0: 0}, {Op: 0x0b}}}},
	},
	// **Two globals, which is the row a shared initializer sink fails.** Written with the prediction that
	// a shared sink would concatenate both expressions into the *first* global's initializer and leave
	// the second a bare terminator; the mutation was run and the print said the opposite — the
	// accumulation lands in global **1**, which gets `i32.const 1, i32.const 2, end` while global 0 keeps
	// its own single expression. Obvious in hindsight (each global's slot is filled with the sink as it
	// stood when *that* field closed, so the later one carries the arrears) and not obvious enough to
	// have been written correctly from reading the code, which is the whole content of print-don't-trust.
	// The row catches it either way; the comment was the thing that was wrong, and a comment asserting
	// the wrong failure mode sends the next reader to the wrong mechanism.
	//
	// The count stays right in both directions, which is what makes this invisible to every other
	// column: a vector of two whose contents have moved. Same shape `elemexprRetained`'s per-element
	// sink exists to prevent one layer down.
	{
		src: `(module (global i32 (i32.const 1)) (global i32 (i32.const 2)))`,
		wantGlobals: []binary.Global{
			{Type: binary.I32, Init: []binary.Instr{{Op: 0x41, Imm0: 1}, {Op: 0x0b}}},
			{Type: binary.I32, Init: []binary.Instr{{Op: 0x41, Imm0: 2}, {Op: 0x0b}}},
		},
	},
	// A `global.get` initializer naming an **imported** global, which is the only index a defined
	// global's initializer may legally name (`valid.ml`'s const context) — and the row that makes
	// `defineGlobal`'s deferral do work: the immediate is resolved in stage 2 through `instr.patch`.
	{
		src:  `(module (import "m" "g" (global i32)) (global i32 (global.get 0)))`,
		want: nil,
		wantImports: []binary.Import{
			{Module: "m", Name: "g", Kind: binary.ExternGlobal},
		},
		wantGlobals: []binary.Global{
			{Type: binary.I32, Init: []binary.Instr{{Op: 0x23, Imm0: 0}, {Op: 0x0b}}},
		},
	},
	// The **symbolic** form of the row above, which is what the deferral is actually for: `$i` is bound
	// by the import that precedes it here, but the resolution runs in stage 2 either way, and this is the
	// spelling under which a cursor-time resolution would be visible as `unknown global`.
	{
		src: `(module (import "m" "g" (global $i i32)) (global $g i32 (global.get $i)))`,
		wantImports: []binary.Import{
			{Module: "m", Name: "g", Kind: binary.ExternGlobal},
		},
		wantGlobals: []binary.Global{
			{Type: binary.I32, Init: []binary.Instr{{Op: 0x23, Imm0: 0}, {Op: 0x0b}}},
		},
	},
	// An **empty** initializer, which `constexpr` being `instr_list` makes well-formed grammatically —
	// the arity is validation's complaint. It encodes to a bare `0x0b`, which the decoder reads back as a
	// one-instruction body, and that is what the reference writes for the same text. Here because "a
	// global with no initializer" is the case an encoder that skipped the empty sink would produce for
	// *every* global.
	{
		src:         `(module (global i32))`,
		wantGlobals: []binary.Global{{Type: binary.I32, Init: []binary.Instr{{Op: 0x0b}}}},
	},
	// A `funcref` global holding `ref.null func` — **the row where this PR's two halves meet.** The
	// heaptype immediate is `heaptypeRetained`'s and the section is `encodeGlobals`', and neither alone
	// encodes this module: `(global funcref (ref.null func))` is the spelling `global.wast` opens with,
	// and it was refused twice over before this PR.
	//
	// **`Imm0` is 0 and not the heap type, which is a retention gap and not a typo in this row.**
	// `immHeapType`'s arm validates the heaptype and stages no word (`instr.go:831` ends
	// `return c.d.decodeHeapType(r)`), so `ref.null func` and `ref.null extern` decode to the *same two
	// instructions* and the pair below differs only in its `Type`. That is filed under 0016 and was
	// already stated at `constexpr_test.go:296`, where the want was first written as `uint64(FuncRef)` by
	// reasoning from the immediate's name and the print said otherwise — so this row is the second site
	// to make the same wrong guess, and copying the sibling's *assertion* rather than re-deriving it is
	// what the shape-indexed-lessons rule asks for. Consequence worth naming: these two rows cannot tell
	// a `d0 70` from a `d0 6f` in the initializer, and only the byte-level probe in
	// `TestRefNullEncodesItsHeapType` can. That is why that test exists as well as these rows.
	{
		src: `(module (global funcref (ref.null func)))`,
		wantGlobals: []binary.Global{
			{Type: binary.FuncRef, Init: []binary.Instr{{Op: 0xd0}, {Op: 0x0b}}},
		},
	},
	{
		src: `(module (global externref (ref.null extern)))`,
		wantGlobals: []binary.Global{
			{Type: binary.ExternRef, Init: []binary.Instr{{Op: 0xd0}, {Op: 0x0b}}},
		},
	},
	// A defined global **and** an imported one, so the population split is asserted rather than assumed:
	// section 6 carries one entry and section 2 the other, and an emitter that put the import in
	// `globalDefs` would write a global with no initializer and an index space one too long.
	//
	// **Written first with two *defined* globals under this comment**, which asserted nothing the
	// two-globals row above does not — a case labelled for a partition it is not a member of, which is
	// grave #34 exactly, and caught here the way that grave says to catch it: by reading the src against
	// the label rather than the label against itself.
	{
		src: `(module (import "m" "g" (global (mut i32))) (global i32 (i32.const 4)))`,
		wantImports: []binary.Import{
			{Module: "m", Name: "g", Kind: binary.ExternGlobal},
		},
		wantGlobals: []binary.Global{
			{Type: binary.I32, Init: []binary.Instr{{Op: 0x41, Imm0: 4}, {Op: 0x0b}}},
		},
	},
	// The inline-import spelling, paired with the `(import … (global …))` rows below for the reason every
	// other sugar pair here is: `(global (import "m" "g") i32)` and the field form are the *same import*,
	// and a mutability or kind byte written correctly in one spelling and wrongly in the other would
	// round-trip for every vector that used the working one. No section 6 at all — the arm produces an
	// `Import` and no `Global` (parser.mly:1082-1085).
	{
		src: `(module (global (import "m" "g") (mut i64)))`,
		wantImports: []binary.Import{
			{Module: "m", Name: "g", Kind: binary.ExternGlobal},
		},
	},
	// An inline **export** on a defined global, which is the third spelling that touches section 6 and
	// the one whose frontier withdrawal is newest: `inlineExports` runs before either arm, so the export
	// names this global's own index whether or not the field turns out to be an import.
	{
		src:         `(module (global (export "g") i32 (i32.const 5)))`,
		wantExports: []binary.Export{{Name: "g", Kind: binary.ExternGlobal, Index: 0}},
		wantGlobals: []binary.Global{{Type: binary.I32, Init: []binary.Instr{{Op: 0x41, Imm0: 5}, {Op: 0x0b}}}},
	},

	// # Imports (#8)
	//
	// **What the want column can and cannot state here is set by `binary.Import`, and the gap is
	// where the kind-byte assertion lives.** The decoder retains `{Module, Name, Kind, Index}` and
	// reads the non-func descriptors for well-formedness only (`decodeImport`'s closing comment), so a
	// row cannot state an imported memory's limits — but it *can* state the `Kind`, and that is the
	// field `externKindByte` would get wrong under a cast, since `importKind`'s order and the binary
	// kind bytes agree on nothing. A transposed kind byte decodes clean and lands in this column.
	//
	// Every row is the `(import …)` field spelling; the sugar spellings are below, paired with these.
	//
	// `(func)` with an empty signature interns type 0 — so the type section is present and the import
	// names index 0. Both halves are stated, because an emitter that wrote the import without the
	// implicit type would produce a module whose import names a type that does not exist.
	{
		src:  `(module (import "m" "f" (func)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
	},
	// A func import with a signature, so the interned type is not the empty one and `Index` still
	// points at it.
	{
		src: `(module (import "m" "f" (func (param i32) (result f64))))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Params:  []binary.ValType{binary.I32},
			Results: []binary.ValType{binary.F64},
		}}},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
	},
	// The **typeuse** spelling, forward-referencing a type defined after it: `imports.wast:62`'s own
	// shape, and the vector the whole deferred phase exists for. A resolver that looked `$forward` up
	// where it is used would reject this module.
	{
		src: `(module (import "m" "f" (func (type $forward))) (type $forward (func (param i32))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
	},
	// A numeric typeuse naming the *second* type, so `Index` is 1 and a hardcoded zero fails. The two
	// types are distinct signatures, so interning cannot collapse them.
	{
		src: `(module (type (func)) (type (func (param i64))) (import "m" "f" (func (type 1))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc},
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I64}}},
		},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 1},
		},
	},
	// The typeuse-with-matching-inline-signature spelling, which reaches `checkExplicit`'s comparing
	// branch rather than its deferred one — and must still yield the *named* index rather than a fresh
	// intern. One type in the image is the assertion: an emitter that interned instead of comparing
	// would produce two.
	//
	// **It is written in the inline-import spelling because the `(import …)` field cannot express it**,
	// and that is the grammar rather than a workaround: `externtype`'s func arms are `typeuse` alone or
	// `functype` alone (:1227/:1246), never both, while `func_fields`'s inline-import arm is
	// `inline_import typeuse func_fields_import` (:975) — a typeuse *and* a signature. The first draft
	// of this row put it in the field spelling and the parser rejected it with `unexpected token`,
	// correctly; `externtype`'s own comment in parser.go says as much and the row was written without
	// reading it.
	{
		src: `(module (type $t (func (param i32))) (func (import "m" "f") (type $t) (param i32)))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
	},
	// The four non-func kinds, each in the `(import …)` field spelling. The `Kind` column is the
	// assertion; the descriptors are read and dropped by the decoder.
	{
		src:         `(module (import "m" "x" (memory 1)))`,
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternMemory}},
	},
	{
		src:         `(module (import "m" "x" (table 1 funcref)))`,
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternTable}},
	},
	// Both mutabilities, because the byte is one bit of difference and `(global i32)` versus
	// `(global (mut i32))` are different modules. The decoder drops the mutability, so these two rows
	// assert only that both *decode* — the byte itself is pinned by the byte-level probe below, which
	// is the same division of labour as the address-type flag bit.
	{
		src:         `(module (import "m" "g" (global i32)))`,
		wantImports: []binary.Import{{Module: "m", Name: "g", Kind: binary.ExternGlobal}},
	},
	{
		src:         `(module (import "m" "g" (global (mut i64))))`,
		wantImports: []binary.Import{{Module: "m", Name: "g", Kind: binary.ExternGlobal}},
	},
	// A tag import: kind 0x04, an attribute byte, and a type index. Its signature interns a type, so
	// the type section is asserted too — and `ExceptionHandling` is on in decodeForTest for exactly
	// this row, per the derivation in its comment.
	{
		src: `(module (import "m" "t" (tag (param i32))))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Params: []binary.ValType{binary.I32},
		}}},
		wantImports: []binary.Import{{Module: "m", Name: "t", Kind: binary.ExternTag, Index: 0}},
	},
	// Import *order*, with two different kinds, so a transposed pair fails on the Kind column rather
	// than passing as a reordering.
	{
		src: `(module (import "a" "1" (memory 1)) (import "b" "2" (table 1 funcref)))`,
		wantImports: []binary.Import{
			{Module: "a", Name: "1", Kind: binary.ExternMemory},
			{Module: "b", Name: "2", Kind: binary.ExternTable},
		},
	},
	// Empty names, which are legal and are the suite's own shape at imports.wast:677. A `name` writer
	// that omitted a zero-length vector's length byte would produce a shorter image that decodes as
	// something else entirely.
	{
		src:         `(module (import "" "" (memory 1)))`,
		wantImports: []binary.Import{{Module: "", Name: "", Kind: binary.ExternMemory}},
	},
	// An escaped name, so the *decoded* bytes reach the image rather than the source spelling — the
	// same assertion decodedName's accept half makes at the unit level, here end to end.
	{
		src:         `(module (import "\41" "\42" (memory 1)))`,
		wantImports: []binary.Import{{Module: "A", Name: "B", Kind: binary.ExternMemory}},
	},
	// A UTF-8 name past ASCII: `name` writes a byte count, not a character count, so a rune-counted
	// length would produce a truncated vector here and decode as garbage.
	//
	// The escapes are wat's, which is `\` followed by **exactly two hex digits** (lexer.mll's `hexdigit
	// hexdigit` arm) — no `\x` prefix. The first draft wrote `\xa9` in C/Python spelling and the lexer
	// rejected it with `illegal escape`, which is the lexer being right about a syntax I invented.
	{
		src:         `(module (import "m\c3\a9" "\e2\82\ac" (memory 1)))`,
		wantImports: []binary.Import{{Module: "mé", Name: "€", Kind: binary.ExternMemory}},
	},

	// # The inline-import sugar spellings, paired with the field spellings above
	//
	// **Each of the five denotes the same import as its `(import …)` twin, and that is the assertion
	// the pairing makes.** The reference produces an `Import` from both (`inline_import` appears in the
	// arm that would otherwise produce a definition), so a row here disagreeing with its twin means
	// one spelling is retained wrongly — the failure `importedGlobal`/`importedMemory`/`importedTable`
	// exist as shared functions to prevent. They are the only reason the withdrawal check has a
	// population to run on, too.
	{
		src:  `(module (func (import "m" "f")))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
	},
	// The inline spelling with a typeuse, which is `func_fields`'s :975 arm — `checkExplicit` with an
	// empty inline signature, so the deferred branch, so the *named* index.
	{
		src: `(module (type $t (func (param f32))) (func (import "m" "f") (type $t)))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.F32}}},
		},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
	},
	{
		src: `(module (tag (import "m" "t") (param i32)))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Params: []binary.ValType{binary.I32},
		}}},
		wantImports: []binary.Import{{Module: "m", Name: "t", Kind: binary.ExternTag, Index: 0}},
	},
	{
		src:         `(module (global (import "m" "g") i32))`,
		wantImports: []binary.Import{{Module: "m", Name: "g", Kind: binary.ExternGlobal}},
	},
	{
		src:         `(module (memory (import "m" "x") 1))`,
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternMemory}},
	},
	{
		src:         `(module (table (import "m" "x") 1 funcref))`,
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternTable}},
	},
	// A named inline import: the identifier binds a parse-time name into the index space and must
	// leave no trace in the image, exactly as on a definition.
	{
		src:         `(module (memory $m (import "m" "x") 1))`,
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternMemory}},
	},

	// # An import and a definition of the same kind, which is where index spaces are load-bearing
	//
	// The import occupies memory index 0 and the definition memory index 1 — but the *memory section*
	// holds only the definition, so this row asserts the split `defineMemory`'s comment names: one
	// import, one memory, and an emitter that put the imported memory in section 5 would produce two
	// memories here.
	{
		src:         `(module (import "m" "x" (memory 1)) (memory 2 3))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 2, Max: 3, HasMax: true}}},
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternMemory}},
	},
	// The same for tables, in both spellings at once, so the two sugar arms and the definition arm are
	// distinguished in one module.
	{
		src:         `(module (table (import "m" "x") 1 funcref) (table 2 externref))`,
		wantTabs:    []binary.Table{{ElemType: binary.ExternRef, Limits: binary.Limits{Min: 2}}},
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternTable}},
	},

	// # The four sections together, so section *order* is asserted
	//
	// `checkSectionOrder` rejects an image whose ids do not ascend, and the text field order here is
	// deliberately the reverse of the binary section order (memory before table, table id 4 before
	// memory id 5) — so an emitter that wrote sections in field order rather than id order fails.
	//
	// The import is **first** in the text and its section is **second** in the image (id 2, between
	// type and table) — the position a `w.section` call appended after the existing ones would get
	// wrong. It has to be first in the text: the reference requires every import to precede every
	// definition (parser.mly:1349-1354, and `TestImportAfterDefinitionNamesTheNearestDefinition` is
	// this project's reading of that arm), so the first draft of this row put it last and was rejected
	// with `import after table definition`. The ordering assertion survives the move intact, because
	// what it needs is a text order that is not the section order: the `(type …)` field is last in the
	// text and its section is first in the image, and memory-before-table in the text is
	// table-before-memory (id 4 before id 5) in the image.
	{
		src: `(module (import "m" "x" (memory 2)) (memory 1) (table 1 funcref) (type (func (param i32))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		},
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternMemory}},
	},

	// # The export section (id 7)
	//
	// **One row per space, because the kind byte is a five-way mapping and a suite vector cannot see
	// it.** `externKindByte` is shared with the import section, whose orders are exact reversals
	// (memory being the fixed point — grave #119's pass-set shape), so a row for each space is what
	// distinguishes a working mapping from one that happens to be right for memory.
	{
		src:         `(module (memory 1) (export "m" (memory 0)))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantExports: []binary.Export{{Name: "m", Kind: binary.ExternMemory, Index: 0}},
	},
	{
		src:         `(module (table 1 funcref) (export "t" (table 0)))`,
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantExports: []binary.Export{{Name: "t", Kind: binary.ExternTable, Index: 0}},
	},
	{
		src:         `(module (import "m" "f" (func)) (export "f" (func 0)))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantImports: []binary.Import{{Module: "m", Name: "f", Kind: binary.ExternFunc}},
		wantExports: []binary.Export{{Name: "f", Kind: binary.ExternFunc, Index: 0}},
	},
	{
		src:         `(module (import "m" "g" (global i32)) (export "g" (global 0)))`,
		wantImports: []binary.Import{{Module: "m", Name: "g", Kind: binary.ExternGlobal}},
		wantExports: []binary.Export{{Name: "g", Kind: binary.ExternGlobal, Index: 0}},
	},
	{
		src:         `(module (import "m" "t" (tag)) (export "t" (tag 0)))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantImports: []binary.Import{{Module: "m", Name: "t", Kind: binary.ExternTag}},
		wantExports: []binary.Export{{Name: "t", Kind: binary.ExternTag, Index: 0}},
	},

	// **A symbolic index, resolved forward.** `exports.wast:14`'s shape: `$m` is not bound when the
	// export is read, so an emitter resolving at the cursor rejects a module the spec accepts. The
	// Index column is what makes the resolution visible — a parse verdict cannot see it.
	{
		src:         `(module (export "m" (memory $m)) (memory $m 1))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantExports: []binary.Export{{Name: "m", Kind: binary.ExternMemory, Index: 0}},
	},
	// **A symbolic index that is not zero, and an imported entry ahead of it.** Two ways to get the
	// index wrong that a zero cannot distinguish: resolving to the wrong binding, and forgetting that
	// an *imported* memory occupies index 0 (`space.count` advances for imports, which is why
	// `bindidxOpt` runs on the import arms too). Index 2 fails on either mistake.
	{
		src: `(module (import "m" "x" (memory 3)) (memory $a 1) (memory $b 2) ` +
			`(export "b" (memory $b)))`,
		wantMems: []binary.Memory{
			{Limits: binary.Limits{Min: 1}}, {Limits: binary.Limits{Min: 2}},
		},
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternMemory}},
		wantExports: []binary.Export{{Name: "b", Kind: binary.ExternMemory, Index: 2}},
	},
	// Source order is section order, and an empty name is legal (`exports.wast` exports `""`). Three
	// entries so a reversal or a stable-sort-by-name is visible; the names are deliberately not in
	// alphabetical order.
	{
		src: `(module (memory 1) (export "z" (memory 0)) (export "" (memory 0)) ` +
			`(export "a" (memory 0)))`,
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantExports: []binary.Export{
			{Name: "z", Kind: binary.ExternMemory, Index: 0},
			{Name: "", Kind: binary.ExternMemory, Index: 0},
			{Name: "a", Kind: binary.ExternMemory, Index: 0},
		},
	},

	// # The inline-export sugar (parser.mly:1269-1274)
	//
	// **Paired with the module-field spelling above, because the two must produce the same image and
	// nothing else can say so.** `(memory (export "m") 1)` and `(memory 1) (export "m" (memory 0))`
	// denote one module; a suite vector cannot tell them apart, since both are `assert_malformed`-free
	// text whose only observable is the image. These rows moved out of
	// `TestEncodeRefusesWhatItCannotWrite`, where they sat as "+ an export section" refusals.
	//
	// The sugar takes its index from the enclosing field rather than from a lookup, so the defect it
	// can have is *off-by-one against the field's own binding* — which is why the pairs below put the
	// exported field at a non-zero index wherever the space allows one.
	{
		src:         `(module (memory (export "m") 1))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantExports: []binary.Export{{Name: "m", Kind: binary.ExternMemory, Index: 0}},
	},
	{
		src:         `(module (table (export "t") 1 funcref))`,
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantExports: []binary.Export{{Name: "t", Kind: binary.ExternTable, Index: 0}},
	},
	// **The index is the field's own, not zero.** Two memories ahead of the exported one, so an
	// emitter that wrote 0 — or that read `space.count` *after* binding and wrote 3 — fails here. This
	// is the row the sugar's whole mechanism rests on, and no vector can see it.
	{
		src: `(module (memory 1) (memory 2) (memory (export "third") 3))`,
		wantMems: []binary.Memory{
			{Limits: binary.Limits{Min: 1}},
			{Limits: binary.Limits{Min: 2}},
			{Limits: binary.Limits{Min: 3}},
		},
		wantExports: []binary.Export{{Name: "third", Kind: binary.ExternMemory, Index: 2}},
	},
	// **An inline export on an inline *import*.** The one arm where the two sugars interact: the
	// export comes first in the recursion (`inline_export func_fields`, :985), so this parses, and the
	// index it exports is the import's — an imported memory occupies index 0 like any other. An
	// emitter that skipped imports when numbering would write 0 here and be right by accident, so the
	// pair below adds a defined memory ahead of nothing and an import ahead of the export.
	{
		src:         `(module (memory (export "m") (import "m" "x") 1))`,
		wantImports: []binary.Import{{Module: "m", Name: "x", Kind: binary.ExternMemory}},
		wantExports: []binary.Export{{Name: "m", Kind: binary.ExternMemory, Index: 0}},
	},
	{
		src: `(module (import "m" "a" (memory 1)) (memory (export "second") (import "m" "b") 2))`,
		wantImports: []binary.Import{
			{Module: "m", Name: "a", Kind: binary.ExternMemory},
			{Module: "m", Name: "b", Kind: binary.ExternMemory},
		},
		wantExports: []binary.Export{{Name: "second", Kind: binary.ExternMemory, Index: 1}},
	},
	{
		src:         `(module (global (export "g") (import "m" "g") i32))`,
		wantImports: []binary.Import{{Module: "m", Name: "g", Kind: binary.ExternGlobal}},
		wantExports: []binary.Export{{Name: "g", Kind: binary.ExternGlobal, Index: 0}},
	},
	{
		src:         `(module (func (export "f") (import "m" "f")))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantImports: []binary.Import{{Module: "m", Name: "f", Kind: binary.ExternFunc}},
		wantExports: []binary.Export{{Name: "f", Kind: binary.ExternFunc, Index: 0}},
	},
	{
		src:         `(module (tag (export "t") (import "m" "t")))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantImports: []binary.Import{{Module: "m", Name: "t", Kind: binary.ExternTag}},
		wantExports: []binary.Export{{Name: "t", Kind: binary.ExternTag, Index: 0}},
	},
	// **The recursion, which is why `inlineExports` is a loop.** `inline_export func_fields` is
	// right-recursive over the whole field, so two are legal and their order is source order. A
	// non-looping reader accepts the first and leaves the second for `inlineImport` to reject.
	{
		src:      `(module (table (export "a") (export "b") 1 funcref))`,
		wantTabs: []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantExports: []binary.Export{
			{Name: "a", Kind: binary.ExternTable, Index: 0},
			{Name: "b", Kind: binary.ExternTable, Index: 0},
		},
	},
	// **Both spellings in one module, and the sugar's entry comes first.** The reference builds the
	// export list per field (`$1 (FuncX x) c :: exs`, :987) inside `module_fields1`'s fold, so a
	// field's inline exports land where the *field* is, not after every module-field export. Written
	// with the `(export …)` field first in the text to make the two orders differ: source order for
	// the section is memory-sugar then field, because the memory field precedes it.
	{
		src: `(module (memory (export "inline") 1) (export "field" (memory 0)))`,
		wantMems: []binary.Memory{
			{Limits: binary.Limits{Min: 1}},
		},
		wantExports: []binary.Export{
			{Name: "inline", Kind: binary.ExternMemory, Index: 0},
			{Name: "field", Kind: binary.ExternMemory, Index: 0},
		},
	},

	// # The function and code sections (#8)
	//
	// **One list, two sections.** `encode.ml` derives both from `m.it.funcs` under one condition
	// (:1141 and :1159), and a module whose two sections disagree about N is the malformed `function
	// and code section have inconsistent lengths`. So every row here asserts the *pair*: the decoder
	// zips them, and `binary.Func` existing at all is that zip.
	{
		src:  `(module (func))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}},
		},
	},
	// The inline signature interns, so the type section the func *implies* is asserted here as well
	// as the body — a func whose signature vanished would still round-trip its body.
	{
		src: `(module (func (param i32) (result i64)))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Params:  []binary.ValType{binary.I32},
			Results: []binary.ValType{binary.I64},
		}}},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}},
		},
	},
	// **Three funcs and two distinct signatures**, which is what pins section 3 to being a vector of
	// *type indices* rather than of positions: func 1 must carry index 1 and func 2 index 0 again. An
	// emitter writing `i` instead of the interned index passes every single-signature row above.
	{
		src: `(module (func) (func (param i32)) (func))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc},
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}},
			{TypeIndex: 1, Body: []binary.Instr{{Op: 0x0b}}},
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}},
		},
	},
	// An explicit `(type $t)` typeuse with the type defined *after* the func, which is what
	// `textFunc.typeIdx` being a thunk is for. Eager resolution rejects this legal module.
	{
		src:  `(module (func (type $t)) (type $t (func)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}},
		},
	},
	// **Locals, declared in three groups totalling five, and the groups are now the assertion.**
	// The wire form is run-length (`count, valtype`, encode.ml:238-242) and the decoder retains
	// it as such since #138, so the want column states the *runs* — which is strictly more than
	// the flat reading it replaced. This case used to want
	// `{I32, I32, I64, F32, F32}` and could not distinguish "emitted 2×i32" from "emitted i32
	// twice"; both flatten identically, and only one is the RLE the format specifies. An
	// emitter that wrote five singleton groups now fails here and used to pass.
	{
		src:  `(module (func (local i32 i32) (local i64) (local f32 f32)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{
			TypeIndex: 0,
			Locals: []binary.LocalGroup{
				{Count: 2, Type: binary.I32},
				{Count: 1, Type: binary.I64},
				{Count: 2, Type: binary.F32},
			},
			Body: []binary.Instr{{Op: 0x0b}},
		}},
	},
	// **Params are not locals in the wire form.** They occupy the same index space at *validation*,
	// but the code section declares only the declared locals — so a func with two params and one local
	// declares one group of one, and `local.get 2` reaches that local. An emitter that wrote the
	// params into the locals vector would double them and still round-trip.
	{
		src: `(module (func (param i32 i64) (local f64) (local.get 2) drop))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Params: []binary.ValType{binary.I32, binary.I64},
		}}},
		wantFuncs: []binary.Func{{
			TypeIndex: 0,
			Locals:    []binary.LocalGroup{{Count: 1, Type: binary.F64}},
			Body: []binary.Instr{
				{Op: 0x20, Imm0: 2}, // local.get 2 — the local, one past the two params
				{Op: 0x1a},          // drop
				{Op: 0x0b},
			},
		}},
	},
	// A **named** local and a named param, resolved at the cursor. `funcSignature` resets
	// `p.ctx.locals` per function and binds the params first, so `$x` is 0 and `$y` is 1 — an
	// off-by-one here is a wrong-but-well-formed module, which is why the want column reads the
	// indices rather than the names.
	{
		src: `(module (func (param $x i32) (local $y i32) (local.get $y) (local.get $x) drop drop))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{
			Params: []binary.ValType{binary.I32},
		}}},
		wantFuncs: []binary.Func{{
			TypeIndex: 0,
			Locals:    []binary.LocalGroup{{Count: 1, Type: binary.I32}},
			Body: []binary.Instr{
				{Op: 0x20, Imm0: 1}, // $y, the local
				{Op: 0x20, Imm0: 0}, // $x, the param
				{Op: 0x1a},
				{Op: 0x1a},
				{Op: 0x0b},
			},
		}},
	},
	// **A folded expression: operands precede their leader.** `expr1`'s `plaininstr expr_list`
	// (parser.mly:814) parses the leader *first* and must emit it *last*, which is what the nested
	// sink and `splice` are for. Reversed, this encodes `i32.add` before its operands — a body that
	// decodes clean and computes something else, and the reason the frontier refuses rather than
	// drops. Nested two deep so a one-level-only splice fails too.
	{
		src:  `(module (func (result i32) (i32.add (i32.const 1) (i32.mul (i32.const 2) (i32.const 3)))))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1}, // i32.const 1
			{Op: 0x41, Imm0: 2}, // i32.const 2
			{Op: 0x41, Imm0: 3}, // i32.const 3
			{Op: 0x6c},          // i32.mul — after its two operands
			{Op: 0x6a},          // i32.add — after all of its
			{Op: 0x0b},
		}}},
	},
	// The same instructions written **flat**, which must produce the identical body. Two spellings of
	// one module, the pattern the inline-import and inline-export rows above use: an emitter that got
	// the fold's order wrong would disagree with itself between these two rows.
	{
		src:  `(module (func (result i32) i32.const 1 i32.const 2 i32.mul i32.add))`,
		want: []binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x41, Imm0: 2},
			{Op: 0x6c},
			{Op: 0x6a},
			{Op: 0x0b},
		}}},
	},
	// **A forward `call`, which is the one index category that defers** (`p.immPatch`). Locals resolve
	// at the cursor because `p.ctx.locals` is per-function and a deferred local resolution would run
	// against the *last* function's locals; func indices cannot resolve at the cursor because this
	// module is legal. Both timings in one place, one row above and one here.
	{
		src:  `(module (func (call $late)) (func $late))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x10, Imm0: 1}, {Op: 0x0b}}},
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}},
		},
	},
	// # The block family and select (#7)
	//
	// **`Imm0` on a structural instruction is the *packed* blocktype, not a type index**, which is
	// what `blockTypeImm` below spells out: `binary.BlockType` unpacks three disjoint cases from one
	// word, and the empty form is `1 << 33` rather than 0. A want column writing `Imm0: 0` for
	// `(block)` would be asserting a block whose signature is type 0 — a legal, different module.
	//
	// Every row below is one where a plausible encoder differs. A dropped opener, a `u32` blocktype,
	// an ELSE emitted for an empty else-arm, a missing END, an opener spliced *after* its body: each
	// produces a module that decodes clean and computes something else, and the suite scores all of
	// them green by construction, every vector containing a block being one it expects to work
	// (§9 G-3). The interpreter's semantic rows live in `internal/interp`'s
	// TestControlFlowSemantics — this table's question is the bytes.
	{
		src:  `(module (func block end))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeImm}, // block, empty blocktype
			{Op: 0x0b},                     // the block's END, which the text does spell
			{Op: 0x0b},                     // the function's, which it does not
		}}},
	},
	// The **folded** spelling of the identical module. Two productions, one image — and the row that
	// catches a folded arm emitting its opener in the wrong place, since `expr1` reaches
	// `foldedBlock` and the row above reaches `blockinstr`. Neither form's END is spelled here: the
	// flat one writes `end` and the folded one writes `)`, and both encode `0x0b`.
	{
		src:  `(module (func (block)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeImm}, {Op: 0x0b}, {Op: 0x0b},
		}}},
	},
	// `loop`, whose only difference from `block` on the wire is the opcode — and whose difference in
	// *meaning* (a branch re-enters) is the interpreter's. Here to pin the opcode: an encoder reading
	// one keyword's row for both would pass every block row and fail this one.
	{
		src:  `(module (func loop end) (func (loop)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x03, Imm0: blockTypeImm}, {Op: 0x0b}, {Op: 0x0b}}},
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x03, Imm0: blockTypeImm}, {Op: 0x0b}, {Op: 0x0b}}},
		},
	},
	// **A block with a body, which is the shape a dropped opener makes dangerous rather than merely
	// incomplete.** Without the opener this is `41 01 1a 41 02 1a 0b` — the block gone, its contents
	// kept, decoding clean. That was `TestEveryStructuralInstructionIsRefused`'s row while the
	// construct was refused; it is here now, asserting the opener is *present* and in front of the
	// body rather than behind it.
	{
		src:  `(module (func block i32.const 1 drop end i32.const 2 drop))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeImm},
			{Op: 0x41, Imm0: 1},
			{Op: 0x1a},
			{Op: 0x0b}, // the block's END, before the trailing instructions
			{Op: 0x41, Imm0: 2},
			{Op: 0x1a},
			{Op: 0x0b},
		}}},
	},
	{
		src:  `(module (func (block (i32.const 1) drop) (i32.const 2) drop))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeImm},
			{Op: 0x41, Imm0: 1},
			{Op: 0x1a},
			{Op: 0x0b},
			{Op: 0x41, Imm0: 2},
			{Op: 0x1a},
			{Op: 0x0b},
		}}},
	},
	// **The single-result blocktype: a bare valtype byte, and `([], [t])` interns nothing.** The type
	// section stays at one entry — the function's own `[] -> []`-shaped signature — which is the
	// assertion that `inlineBlockType` is being consulted: an encoder interning unconditionally would
	// produce two types here and write an index where a valtype byte belongs.
	{
		src:  `(module (func (block (result i32) (i32.const 1) drop)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeValTypeImm(binary.I32)},
			{Op: 0x41, Imm0: 1},
			{Op: 0x1a},
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// **A block with params, which is the arm that *does* intern** — `([], [])` and `([], [t])` are
	// the only two inline forms, so `(param i32)` falls through to `inline_functype` and the
	// blocktype is a type index. Two types in the section: the function's, then the block's, in that
	// order because the function's signature is recorded by `funcField` before the body is read.
	//
	// `Imm0: 1` is a *bare* index rather than a tagged word, which is the packing's own doing:
	// `blockTypeEmpty`/`blockTypeValType` sit above 2^32 so an index needs no tag. So this row also
	// pins that the empty form is not index 0.
	{
		src: `(module (func (result i32) (i32.const 1) (block (param i32) (result i32))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
			{Kind: binary.CompFunc, Func: binary.FuncType{
				Params:  []binary.ValType{binary.I32},
				Results: []binary.ValType{binary.I32},
			}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x02, Imm0: 1}, // the interned type index, not a tagged blocktype
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// **An explicit `(type $t)` blocktype encodes the index even when the signature is inline-able.**
	// `block`'s first arm is `VarBlockType x` with no `([], [t])` case in front of it
	// (parser.mly:741-744), so this is `0x02 00` and not `0x02 7f` — two well-formed modules that
	// validate the same and differ in bytes, which is precisely where an encoder collapsing the
	// spelling would be wrong and invisible.
	{
		src: `(module (type $t (func (result i32))) (func (result i32) (block (type $t) (i32.const 1))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: 0}, // type index 0, written as an s33 — not the 0x40 empty marker
			{Op: 0x41, Imm0: 1},
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// **`if` with no else-arm emits no ELSE byte.** `encode.ml`'s condition is `if es2 <> [] then op
	// 0x05` (:254), so the absence is the reference's and not an omission — an encoder emitting it
	// unconditionally produces a *different* instruction sequence that still decodes.
	{
		src:  `(module (func i32.const 0 if end))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x04, Imm0: blockTypeImm},
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// **`if … else … end` with an *empty* else-arm also emits no ELSE**, which is the row that
	// separates the reference's condition from the tempting one. The keyword is written and `es2` is
	// still `[]`, so this module is byte-identical to the row above — an encoder keying on "was
	// `else` spelled" emits `0x05` here and disagrees with the reference.
	{
		src:  `(module (func i32.const 0 if else end))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x04, Imm0: blockTypeImm},
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// A **non-empty** else-arm, which is the row that makes the two above more than a tautology: an
	// encoder that never emitted `0x05` passes both of them and fails here. Both arms carry an
	// instruction so a swap between them is visible.
	{
		src: `(module (func (result i32) i32.const 0 if (result i32) i32.const 1 else i32.const 2 end))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x04, Imm0: blockTypeValTypeImm(binary.I32)},
			{Op: 0x41, Imm0: 1}, // the then-arm
			{Op: 0x05},          // ELSE
			{Op: 0x41, Imm0: 2}, // the else-arm
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// **The folded `if`: its condition operands precede the opener, its arms follow it.** `if_`'s
	// three components go to three places (`[], $3 c', $7 c'`, parser.mly:893) and this is the row
	// that says so — a reader diverting the whole tail into the then-arm would emit `04 7f 41 00 …`,
	// the condition *inside* the block, which decodes clean and evaluates it in the wrong frame.
	// Byte-identical to the flat row above, which is what makes the pair a control on both readers.
	{
		src: `(module (func (result i32) (if (result i32) (i32.const 0) (then (i32.const 1)) (else (i32.const 2)))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0}, // the condition, ahead of the opener
			{Op: 0x04, Imm0: blockTypeValTypeImm(binary.I32)},
			{Op: 0x41, Imm0: 1},
			{Op: 0x05},
			{Op: 0x41, Imm0: 2},
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// The folded one-armed sugar (:896), whose else-arm is absent rather than empty — the third
	// spelling that must produce no `0x05`.
	{
		src:  `(module (func (if (i32.const 0) (then (nop)))))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x04, Imm0: blockTypeImm},
			{Op: 0x01}, // nop
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// **A nested block's implicit type interns before its enclosing one's**, which is the ordering
	// `orderedTypeUse` calls its tail for — and the row that makes the indices in the blocktypes
	// witness it. The inner `(param i32)` is type 1 and the outer `(param i64)` type 2; reversed,
	// both blocks would name a signature they do not have, and every module here would still decode.
	// TestNestedBlockTypeInternsBeforeItsEnclosingOne asserts the same order from the type table;
	// this asserts it from the *instruction stream*, which is the half a table check cannot see.
	{
		src: `(module (func (i64.const 0) (block (param i64) (i32.const 0) (block (param i32) drop) drop)))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc},
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I64}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x42, Imm0: 0},
			{Op: 0x02, Imm0: 2}, // the outer block: interned second
			{Op: 0x41, Imm0: 0},
			{Op: 0x02, Imm0: 1}, // the inner: interned first
			{Op: 0x1a},
			{Op: 0x0b}, // the inner block's END
			{Op: 0x1a},
			{Op: 0x0b},
			{Op: 0x0b},
		}}},
	},
	// **A symbolic label, both spellings**, which `labelIdx`'s comment pre-registered as owed to this
	// PR: in build mode only the NAT arm was reachable before the block family encoded, so `(block $l
	// (br $l))` is the first module-level row where a *named* label resolves. `br 0` targets the
	// block; a reader that skipped the push would report `unknown label` on a legal module, and one
	// that resolved against the wrong depth would encode `br 1` — a return.
	{
		src:  `(module (func block $l br $l end) (func (block $l (br $l))))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{
				{Op: 0x02, Imm0: blockTypeImm}, {Op: 0x0c, Imm0: 0}, {Op: 0x0b}, {Op: 0x0b},
			}},
			{TypeIndex: 0, Body: []binary.Instr{
				{Op: 0x02, Imm0: blockTypeImm}, {Op: 0x0c, Imm0: 0}, {Op: 0x0b}, {Op: 0x0b},
			}},
		},
	},
	// **`select`'s two opcodes, chosen on whether `(result …)` was written and not on what it
	// contained.** The bare form is `0x1b`; the annotated form is `0x1c` plus `vec valtype`.
	{
		src: `(module (func (result i32) i32.const 1 i32.const 2 i32.const 0 select))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x41, Imm0: 2},
			{Op: 0x41, Imm0: 0},
			{Op: 0x1b},
			{Op: 0x0b},
		}}},
	},
	{
		src: `(module (func (result i32) i32.const 1 i32.const 2 i32.const 0 select (result i32)))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x41, Imm0: 2},
			{Op: 0x41, Imm0: 0},
			{Op: 0x1c},
			{Op: 0x0b},
		}}},
	},
	// **`select (result)` — a written but *empty* result list, which is `Some []` and therefore
	// `0x1c` with a zero-length vector.** This is the row `selectOpByte`'s "not on whether it
	// contained anything" exists for: a `len(results) > 0` predicate encodes this as a bare `0x1b`,
	// a *different instruction* that decodes clean and validates differently. `valtype_list` has an
	// empty arm (:396-398), so the spelling is legal and no vector uses it.
	//
	// Nothing in the decoded body distinguishes the vector's length — `immVecValType` reads the
	// types and drops them — so the opcode is the whole observable, which is exactly why the
	// wrong predicate would be invisible without this row.
	{
		src: `(module (func (result i32) i32.const 1 i32.const 2 i32.const 0 select (result)))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x41, Imm0: 2},
			{Op: 0x41, Imm0: 0},
			{Op: 0x1c},
			{Op: 0x0b},
		}}},
	},
	// The folded spelling, whose operands are `expr_list` rather than `instr_list` (:837-840 against
	// :682-686) and which must produce the identical body.
	{
		src: `(module (func (result i32) (select (i32.const 1) (i32.const 2) (i32.const 0))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x41, Imm0: 2},
			{Op: 0x41, Imm0: 0},
			{Op: 0x1b},
			{Op: 0x0b},
		}}},
	},
	// **The pair that separates the two placements, which every row above is blind to** (grave #145).
	// The four `select` rows above all write it as the sequence's last instruction, where "emit before
	// the tail" and "emit after the tail" produce identical bytes because the tail is empty — so a
	// reader emitting a *flat* select after its `instr_list` was green on all four while denoting a
	// different program for every flat select with anything after it.
	//
	// A `drop` and an `i32.const` follow, and the two spellings put them in opposite places: flat is
	// `select :: [drop; i32.const 9]` (:678-680), folded is `[operands] @ [select]` then the drop and
	// the const as the enclosing sequence's own members. Confirmed against `wat2wasm --enable-all`,
	// which writes `1b 1a 41 09` for the flat source where this encoder wrote `1a 41 09 1b`.
	//
	// **The suite *does* have the spelling — select.wast:582, `unreachable select nop` — and that is
	// the more interesting fact, because it means the vector was being run and passing.** The first
	// draft of this comment called the row derived on the strength of a grep that found every
	// occurrence final or folded; the grep was measuring text with a pattern that excluded `(select`
	// and therefore also excluded the flat rows written beside folded ones. *Measure with the
	// instrument, not a regex.*
	//
	// What blinds the board is the vector's *shape*, not its absence. select.wast:577's "Flat syntax"
	// block is a bare `(module …)` with no assertion attached, so the harness asks only whether it
	// decodes and validates — and every instruction in it follows `unreachable`, which is dead code
	// the validator accepts in either order. So the suite exercises this production and cannot see the
	// answer, which is the accept-direction case §9 G-3 names and a sharper version of it: not a
	// missing vector but a *present* one whose oracle stops short.
	//
	// The order below is wabt's, on this row's own source: `1b 1a 41 09` where this encoder wrote
	// `1a 41 09 1b`.
	{
		src: `(module (func (result i32) i32.const 1 i32.const 2 i32.const 0 select drop i32.const 9))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x41, Imm0: 2},
			{Op: 0x41, Imm0: 0},
			{Op: 0x1b}, // before the drop, because the drop is the select's `instr_list` tail
			{Op: 0x1a},
			{Op: 0x41, Imm0: 9},
			{Op: 0x0b},
		}}},
	},
	// The folded twin, and it is what stops the fix from being "reverse both" — the same argument
	// `expr1`'s plain arm makes at its own splice. Here the operands are the tail and the select
	// follows them, so the drop and the const are the *function's* instructions rather than the
	// select's, and they still come after.
	{
		src: `(module (func (result i32) (select (i32.const 1) (i32.const 2) (i32.const 0)) drop i32.const 9))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 1},
			{Op: 0x41, Imm0: 2},
			{Op: 0x41, Imm0: 0},
			{Op: 0x1b},
			{Op: 0x1a},
			{Op: 0x41, Imm0: 9},
			{Op: 0x0b},
		}}},
	},

	// # `call_indirect`, whose two immediates are written in one order and encoded in the other (#8)
	//
	// `CallIndirect (x, y) → op 0x11; idx y; idx x` (encode.ml:275): `x` is the table, `y` the type, so
	// the **type index precedes the table index on the wire** while the text writes the table first and
	// usually omits it. Every row's want column is read from the *encoding* rather than from this
	// encoder, and corroborated byte-for-byte against `wat2wasm --enable-all`.
	//
	// **A swapped pair is invisible on the majority spelling, which is why one row uses index 1 for
	// each side separately.** Both immediates are u32 LEBs, so a transposition decodes clean and calls
	// through the wrong table with the wrong type; and for the common `(table 1 funcref)` plus one type
	// it is `11 00 00` either way. So there is a row whose *table* is 1 and type 0, and a row whose
	// *type* is 1 and table 0 — a transposition fails exactly one of them in each direction, where
	// either alone would look like a plausible reading.
	//
	// The bare spelling: an omitted table index is the sugar arm's `0l` (parser.mly:693), and the
	// omitted signature interns the empty functype, which is type 0 here because the func's own
	// signature interned it first.
	{
		src:  `(module (table 1 funcref) (func i32.const 0 call_indirect))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x11, Imm0: 0, Imm1: 0}, // type 0, table 0
			{Op: 0x0b},
		}}},
	},
	// **Type 1, table 0** — the inline `(result i32)` interns a second type, and `callchain` interns
	// unconditionally where a block's `([], [t])` would not (:709 against :746). So `Imm0` is 1 and
	// `Imm1` is 0, and a reader that swapped them would encode `11 00 01`: a call through table 1,
	// which this module does not have, on a module the decoder still accepts because the table index
	// is not validated here.
	{
		src: `(module (table 1 funcref) (func i32.const 0 call_indirect (result i32) drop))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc},
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
		},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x11, Imm0: 1, Imm1: 0}, // type 1, table 0
			{Op: 0x1a},
			{Op: 0x0b},
		}}},
	},
	// **Type 0, table 1** — the other direction, and the row that makes the pair a bidirectional
	// control rather than two assertions. A symbolic table index too, so the resolution runs: `$b` is
	// the second table, and its space is resolved in stage 2 by `callIndirectImm` rather than at the
	// cursor, because a patch replaces the immediates and cannot be half-built.
	{
		src: `(module (table 0 funcref) (table $b 1 funcref) (type $t (func)) ` +
			`(func i32.const 0 call_indirect $b (type $t)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 0}},
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x11, Imm0: 0, Imm1: 1}, // type 0, table 1
			{Op: 0x0b},
		}}},
	},
	// The written signature's params, which the `(result i32)` row alone cannot distinguish from a
	// results-only chain: `(param i32) (result i32)` interns `[i32] → [i32]`, so a chain that dropped
	// its params would intern `[] → [i32]` and encode a *legal* type index naming the wrong functype.
	{
		src: `(module (table 1 funcref) (func i32.const 7 i32.const 0 ` +
			`call_indirect (param i32) (result i32) drop))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc},
			{Kind: binary.CompFunc, Func: binary.FuncType{
				Params:  []binary.ValType{binary.I32},
				Results: []binary.ValType{binary.I32},
			}},
		},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 7},
			{Op: 0x41, Imm0: 0},
			{Op: 0x11, Imm0: 1, Imm1: 0},
			{Op: 0x1a},
			{Op: 0x0b},
		}}},
	},
	// **The placement pair, the same control the flat/folded `select` rows carry** (grave #145): all
	// four arms of `callinstr_instr_list` end in `… :: es` (:689-701) where the folded arms are
	// `es, call_indirect (…)` (:817-823), so a flat `call_indirect` precedes what follows it and a
	// folded one follows its operands. Both spellings below encode identically, and wabt agrees on
	// both — which is what makes the fix "one placement per production" rather than "reverse both".
	//
	// This is the row that would have inherited #145 verbatim had the select defect not been fixed
	// first: the shared emitter was the reason it was found.
	{
		src: `(module (table 1 funcref) (type $t (func (result i32))) ` +
			`(func i32.const 0 call_indirect (type $t) drop i32.const 9 drop))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
			{Kind: binary.CompFunc},
		},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 1, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0},
			{Op: 0x11, Imm0: 0, Imm1: 0}, // before the drop, its `instr_list` tail
			{Op: 0x1a},
			{Op: 0x41, Imm0: 9},
			{Op: 0x1a},
			{Op: 0x0b},
		}}},
	},
	{
		src: `(module (table 1 funcref) (type $t (func (result i32))) ` +
			`(func (call_indirect (type $t) (i32.const 0)) drop i32.const 9 drop))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
			{Kind: binary.CompFunc},
		},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantFuncs: []binary.Func{{TypeIndex: 1, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0}, // the operand, which is the folded arm's tail
			{Op: 0x11, Imm0: 0, Imm1: 0},
			{Op: 0x1a},
			{Op: 0x41, Imm0: 9},
			{Op: 0x1a},
			{Op: 0x0b},
		}}},
	},

	// # `br_table`, whose written form and wire form differ in three ways (0016)
	//
	// The text is `idx idx_list` — no count, no separator, and never empty — while the encoding is
	// `vec(labelidx) labelidx`. So a count the parser does not yet have precedes the members, and the
	// **last written** label is the default rather than a member (`Lib.List.split_last`,
	// parser.mly:563-565). Three transformations, and the want columns below are read from the
	// *encoding* rather than from this encoder: `Imm0` is the default, `Labels` is the table.
	//
	// **`Imm0` and not `Imm1`, measured rather than reasoned.** `immVecIdx` stages no word, so the
	// default is this opcode's first staged immediate; reading the table row's field order gives
	// `Imm1` and is wrong — see `binary.Func.Labels`.
	//
	// One written label, which is an **empty** table with that label as the default: three
	// immediate bytes (`0e 00 00`), not two. An encoder that wrote the label as a table entry with no
	// default produces `0e 01 00` and the decoder reads the following `0x0b` as the default —
	// accepting, and a different module.
	{
		src:  `(module (func (block (br_table 0 (i32.const 0)))))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeImm},
			{Op: 0x41, Imm0: 0},
			{Op: 0x0e, Imm0: 0}, // default 0, and no table
			{Op: 0x0b},
			{Op: 0x0b},
		}, Labels: map[int][]uint32{2: {}}}},
	},
	// **Three labels whose written order is not symmetric, which is what makes the split visible.**
	// `br_table $a $b $a` from inside two blocks writes depths `1 0 1`, so the table is `[1, 0]` and
	// the default is `1`. Every plausible misreading gives a different answer here and the same
	// answer on a uniform table: taking the *first* label as the default gives table `[0, 1]`, an
	// encoder that kept all three as members gives a three-entry table, and one that reversed the
	// vector gives `[0, 1]` with the right default. The `Labels` column is the only witness — `Op`
	// and both `Imm` words are identical in the first and third cases.
	{
		src:  `(module (func (block $a (block $b (br_table $a $b $a (i32.const 0))))))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeImm},
			{Op: 0x02, Imm0: blockTypeImm},
			{Op: 0x41, Imm0: 0},
			{Op: 0x0e, Imm0: 1}, // the last written label, $a at depth 1
			{Op: 0x0b},
			{Op: 0x0b},
			{Op: 0x0b},
		}, Labels: map[int][]uint32{3: {1, 0}}}},
	},
	// **Three identical labels, and this row's own catch is a *deduplicating* encoder** — the one
	// defect the two rows above are blind to, because a table of distinct depths has nothing to
	// collapse. `[0, 0]` with default `0` is legal and means two indices and the default all target
	// the same block; an encoder that treated the vector as a set writes a one-entry table, which
	// decodes clean and sends index 1 to the default. Measured against a `compact`-style probe: this
	// row failed and the other two passed.
	//
	// The reason it is *not* the count witness — which is what the first draft of this comment
	// claimed — is that writing `len(depths)` for `len(depths)-1` consumes the terminating END as the
	// default and fails all three rows on `unexpected end of section or function`. A count error is
	// caught by every row here, so no row needs to be selected for it, and a comment claiming
	// otherwise would send the next author to the wrong row when this one goes red.
	{
		src:  `(module (func (block (br_table 0 0 0 (i32.const 0)))))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x02, Imm0: blockTypeImm},
			{Op: 0x41, Imm0: 0},
			{Op: 0x0e, Imm0: 0},
			{Op: 0x0b},
			{Op: 0x0b},
		}, Labels: map[int][]uint32{2: {0, 0}}}},
	},

	// An **imported** func occupies index 0, so the defined func's `call 0` names the import and not
	// itself. Section 3 lists only the defined one — the import's type index is the descriptor's —
	// which is the split `Funcs` being "one entry per *defined* function" states.
	{
		src:  `(module (import "m" "f" (func)) (func (call 0)))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantImports: []binary.Import{
			{Module: "m", Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x10, Imm0: 0}, {Op: 0x0b}}},
		},
	},
	// **The four constant widths, with the sign and bit-pattern readings that distinguish them.**
	// `i32.const -1` and `i32.const 4294967295` are the same instruction: `constImmBytes` interprets
	// the sign at the *mnemonic's* width, so both are `41 7f` and both decode to the same Imm0. The
	// float rows carry a NaN payload, which is the one place a naive strconv round trip loses data —
	// `floatConstBits` and TestFloatConstBitsMatchTheSuitesBitPatterns exist for it, and this is the row that
	// takes those bits all the way through a decoder.
	//
	// **The i32 rows read `0xffffffffffffffff`, not `0xffffffff`, and that is the decoder's
	// deliberate choice rather than a surprise.** `immS32` is *sign-extended* into the 64-bit slot
	// (instr.go:686-690: "the same value at 32 and a different one the moment anything widens it"),
	// so both spellings land on the full-width -1. Written as `0xffffffff` first, which is what a
	// reading of the *text* alone suggests, and the failure sent this to the decoder's comment — the
	// want column is a second reading of the wat and the format, and the format had the answer.
	{
		src: `(module (func (result i32) i32.const 4294967295) (func (result i32) i32.const -1)
		              (func (result i64) i64.const -1) (func (result f32) f32.const nan:0x200000)
		              (func (result f64) f64.const -0x1p-1))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I32}}},
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I64}}},
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.F32}}},
			{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.F64}}},
		},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x41, Imm0: 0xffffffffffffffff}, {Op: 0x0b}}},
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x41, Imm0: 0xffffffffffffffff}, {Op: 0x0b}}},
			{TypeIndex: 1, Body: []binary.Instr{{Op: 0x42, Imm0: 0xffffffffffffffff}, {Op: 0x0b}}},
			{TypeIndex: 2, Body: []binary.Instr{{Op: 0x43, Imm0: 0x7fa00000}, {Op: 0x0b}}},
			{TypeIndex: 3, Body: []binary.Instr{{Op: 0x44, Imm0: 0xbfe0000000000000}, {Op: 0x0b}}},
		},
	},
	// **`memory.size` writes an index the text does not spell** — a bare `idx`, encode.ml:601, which
	// has no empty arm. An emitter that wrote no immediate for the omitted operand produces a body one
	// byte short and the *next* instruction is read out of it.
	{
		src:      `(module (memory 1) (func (memory.size) drop))`,
		want:     []binary.CompType{{Kind: binary.CompFunc}},
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x3f, Imm0: 0}, {Op: 0x1a}, {Op: 0x0b},
		}}},
	},
	// **A label immediate, which was emitted as nothing at all.** `br 0` wrote `0x0c` with no operand,
	// so the body's terminating `0x0b` became the operand and `(func br 0)` decoded as a branch to
	// label 11 followed by nothing: a well-formed image denoting a different function, green on every
	// board. The wabt corpus is what said so — `token#5`, 26 bytes against wabt's 27 — and the cause
	// is that every label-taking arm returns before the main switch's `idxRetained` (`immediates`), so
	// the retention had to be added at `labelIdx` or nowhere.
	//
	// The trailing `(nop)` is the vector's own and it is the part with teeth: without an instruction
	// after the `br`, a missing immediate eats the `end` and *still* decodes, which is exactly how
	// this survived. With it, the wrong reading is a body one byte short of its declared size.
	{
		src:  `(module (func br 0(nop)))`, // token.wast:30
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x0c, Imm0: 0}, // br 0 — the immediate the emitter dropped
			{Op: 0x01},          // nop
			{Op: 0x0b},
		}}},
	},
	// A non-zero depth and a second label-taking mnemonic, so the row above is not a fixed point: `br
	// 0`'s correct bytes and its buggy bytes differ only in *length*, and an emitter writing a
	// constant `0x00` operand would pass it. `br_if` also proves the retention is not `br`-specific —
	// both reach `labelIdx` through `labelTakingKinds`, which is the mechanism under test.
	{
		src:  `(module (func (br 3) (nop)) (func (br_if 2 (i32.const 0))))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0c, Imm0: 3}, {Op: 0x01}, {Op: 0x0b}}},
			{TypeIndex: 0, Body: []binary.Instr{
				{Op: 0x41, Imm0: 0}, {Op: 0x0d, Imm0: 2}, {Op: 0x0b},
			}},
		},
	},
	// **A func and an inline export**, so the export's index resolves against a func space whose sole
	// member is this one — and so the withdrawal's `func` row is not the only place `funcField`'s tail
	// runs beside another section's retention.
	{
		src:  `(module (func (export "f")))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantExports: []binary.Export{
			{Name: "f", Kind: binary.ExternFunc, Index: 0},
		},
		wantFuncs: []binary.Func{
			{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}},
		},
	},

	// # The memarg rows (#8), and **only two of its three fields are here**
	//
	// `decodeMemop` stages the offset in `Imm0` and the memory index in `Imm1` and
	// **discards the alignment** — its own comment says so, on the ground that alignment is a
	// validation constraint with no execution semantics. So a round trip is structurally blind to
	// the whole generated `naturalAlign` table, exactly as it is blind to the limits address-type
	// bit, and for the same reason: the decoded value carries no field to disagree in.
	// `TestEncodeWritesTheNaturalAlignmentDefault` is the other witness, at byte level, and it
	// exists because these rows cannot be it. Two fields checkable here, one not, stated rather
	// than left for a reader to infer from which assertions happen to appear.
	{
		src:      `(module (memory 1) (func (drop (i32.load (i32.const 0)))))`,
		want:     []binary.CompType{{Kind: binary.CompFunc}},
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0}, // i32.const 0 — the address
			{Op: 0x28},          // i32.load: offset 0, memory 0, both implicit
			{Op: 0x1a},          // drop
			{Op: 0x0b},
		}}},
	},
	// **`offset=` is the one memarg field a round trip sees on its own**, so it is asserted at a
	// value past one LEB byte: 128 encodes as `80 01`, and a writer that emitted a single byte
	// would put `01` where the next instruction belongs. `u64` is the width (`memopOffset`), and
	// `offsetEqValue` is the lexeme reader between the two.
	{
		src:      `(module (memory 1) (func (drop (i32.load offset=128 (i32.const 0)))))`,
		want:     []binary.CompType{{Kind: binary.CompFunc}},
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0}, {Op: 0x28, Imm0: 128}, {Op: 0x1a}, {Op: 0x0b},
		}}},
	},
	// An explicit memory index `0`, whose correct bytes are the *implicit* case's — and **this row
	// cannot see that, which was measured rather than assumed.** The presence-vs-value defect
	// (`hasIdx := m.haveIdx`) was installed here and **passed**: it writes flags `0x42` plus an
	// index field plus the offset, which is one byte longer and decodes to the identical `Imm0` and
	// `Imm1`, because the emitter's own body-size field keeps the image self-consistent. So the
	// wrong bytes are not a wrong *module* to any consumer that reads through the decoder — they
	// differ from the reference's `memop` output and nothing in this direction can say so.
	//
	// The property therefore lives in `TestEncodeWritesTheNaturalAlignmentDefault`, which reads the
	// flags byte and does see `0x42` against `0x02`. The row stays here because it belongs in the
	// encodable set, and this paragraph is the record of a control that was written for a defect it
	// could not catch — found by budgeting for the falsification to pass (#108).
	{
		src:      `(module (memory 1) (func (drop (i32.load 0 (i32.const 0)))))`,
		want:     []binary.CompType{{Kind: binary.CompFunc}},
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0}, {Op: 0x28}, {Op: 0x1a}, {Op: 0x0b},
		}}},
	},
	// A **non-zero** memory index, which is the row that makes the two above falsifiable: it is the
	// only one where `has_idx` is true, so an emitter that never set 0x40 passes both of them and
	// fails here. `Imm1: 1` is the staged index; the flags byte is `0x42` (align 2 | 0x40).
	//
	// **This row is why `decodeForTest` turns MultiMemory on, and it got there by failing.** The
	// sentence here first claimed bit 6 needed no gate to decode, reasoning from `memopIndex`
	// recording its decline rather than returning it — but `release()` hands the recorded decline
	// back once the body's grammar completes, so the row failed at `decodeForTest` with `memarg
	// flags bit 6 … feature gate disabled`. That is the same mechanism the 114 new board declines
	// come from, met here one layer closer: the emitter is right, the *decoder configuration* is
	// what a gated construct needs, and on the board the gate stays off so those vectors are
	// honestly `gated` rather than passed.
	{
		src:      `(module (memory 1) (memory 1) (func (drop (i32.load 1 (i32.const 0)))))`,
		want:     []binary.CompType{{Kind: binary.CompFunc}},
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}, {Limits: binary.Limits{Min: 1}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0}, {Op: 0x28, Imm1: 1}, {Op: 0x1a}, {Op: 0x0b},
		}}},
	},
	// A store, so the memarg reader is exercised on an instruction whose operands it follows rather
	// than precedes, and with both optional fields at once. `0x36` is `i32.store`; natural alignment
	// 2, offset 4.
	{
		src:      `(module (memory 1) (func (i32.store offset=4 (i32.const 0) (i32.const 1))))`,
		want:     []binary.CompType{{Kind: binary.CompFunc}},
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{
			{Op: 0x41, Imm0: 0}, {Op: 0x41, Imm0: 1}, {Op: 0x36, Imm0: 4}, {Op: 0x0b},
		}}},
	},

	// # The data and data count sections (#8)
	//
	// Asserted as payload bytes, per the table header — the mode byte does not survive into
	// `binary.Module.Datas` (modes 0 and 2 decode identically; measured at the `withData` floor), so
	// these rows are the only instrument over it, and every column above is blind to that distinction.
	//
	// **The mode flag is the resolved index, not the spelling**, which is the first three rows: an
	// explicit `(memory 0)` and the sugar collapse to the identical two-byte `0x00` form, because
	// `encode.ml` matches `Active ({it = 0l; _}, c)` before `Active (x, c)` (:1096-1099). Three
	// spellings, one image, and a suite vector cannot see it — all three are well-formed text whose
	// only observable is the bytes.
	{
		src:         `(module (memory 1) (data (i32.const 0) "abc"))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x03, 'a', 'b', 'c'},
	},
	{
		src:         `(module (memory 1) (data (offset (i32.const 0)) "abc"))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x03, 'a', 'b', 'c'},
	},
	{
		src:         `(module (memory 1) (data (memory 0) (offset (i32.const 0)) "abc"))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x03, 'a', 'b', 'c'},
	},
	// A **non-zero** memory index, which is the `0x02` arm and the row that makes the three above
	// more than a tautology: an emitter that always wrote `0x00` and dropped the index passes all
	// three of them. MultiMemory is on in `decodeForTest`, which is what lets a second memory exist.
	{
		src:      `(module (memory 1) (memory 1) (data (memory 1) (i32.const 0) "z"))`,
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}, {Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{
			0x01, 0x02, 0x01, 0x41, 0x00, 0x0b, 0x01, 'z',
		},
	},
	// The **passive** arm: `0x01` and a payload, no offset at all — and no memory, which is what
	// makes a passive segment legal in a module with no memory section. This row is
	// `(module (data "abc"))` from `TestEncodeRefusesWhatItCannotWrite`, arriving.
	{src: `(module (data "hello"))`, wantDataSec: []byte{
		0x01, 0x01, 0x05, 'h', 'e', 'l', 'l', 'o',
	}},
	// **An active segment with an *empty* offset expression**, which is why `textData.passive` is a
	// field rather than a nil test. `(offset)` is legal — `constexpr` is `instr_list` (parser.mly:1091,
	// :950) — so this is `0x00` with a bare terminator, and an emitter reading nil-ness as passive
	// writes `0x01` here: a different segment mode in a well-formed image. Paired with the passive row
	// above, whose bytes it must *not* equal.
	{
		src:         `(module (memory 1) (data (offset) ""))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{0x01, 0x00, 0x0b, 0x00},
	},
	// An empty payload on the passive arm, for the same reason in the other direction: `0x01 0x00` is
	// a segment with no bytes, not the absence of a segment.
	{src: `(module (data))`, wantDataSec: []byte{0x01, 0x01, 0x00}},
	// **`string_list` concatenates**, and the count is the sum. Three tokens, four bytes, one segment
	// — an emitter retaining only the last token writes `0x02 'c' 'd'` and every other row passes.
	{
		src:         `(module (memory 1) (data (i32.const 0) "a" "b" "cd"))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x04, 'a', 'b', 'c', 'd'},
	},
	// **A payload that is not valid UTF-8**, which is the row `stringList`'s "no decode" paragraph
	// exists for: `(data "\ef\ff\fe")` is legal wat, and a blanket UTF-8 check on string tokens would
	// reject it while passing all 176 `utf8-invalid-encoding.wast` vectors. The escapes are the
	// lexer's, so this also pins that `Token.Value` is the *unescaped* bytes — six source characters
	// per escape, three bytes in the image.
	{
		src:         `(module (memory 1) (data (i32.const 0) "\ef\ff\fe"))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x03, 0xef, 0xff, 0xfe},
	},
	// Two segments, so the vector count is exercised as a count and the order as source order.
	{
		src:      `(module (memory 1) (data (i32.const 0) "a") (data (i32.const 8) "b"))`,
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{
			0x02,
			0x00, 0x41, 0x00, 0x0b, 0x01, 'a',
			0x00, 0x41, 0x08, 0x0b, 0x01, 'b',
		},
	},
	// **A symbolic memory index defined *after* the segment**, which is what `defineData`'s stage-2
	// deferral is for: `module_fields1` evaluates data fields in its second closure, so this is a
	// legal module and resolving at the cursor rejects it. The image is identical to the numeric
	// spelling's, so the *verdict* is the assertion — the accept direction, where the suite has
	// nothing (its `unknown memory` vectors are numeric and hence the validator's).
	{
		src:         `(module (data (memory $m) (i32.const 0) "q") (memory $m 1))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 'q'},
	},
	// A symbolic index naming memory **1**, so the resolution is asserted as a value rather than as
	// "it did not error": an implementation resolving every symbolic memory to 0 passes the row above.
	{
		src:      `(module (memory 1) (memory $m 1) (data (memory $m) (i32.const 0) "q"))`,
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}, {Limits: binary.Limits{Min: 1}}},
		wantDataSec: []byte{
			0x01, 0x02, 0x01, 0x41, 0x00, 0x0b, 0x01, 'q',
		},
	},

	// ## `(memory <addrtype> (data …))` — the sugar that defines two things
	//
	// The memory's limits are `(len + 65535) / 65536` **as both min and max** (parser.mly:1128), and
	// the offset is a synthesized `at_const` zero (:1130) with no source token — so the `wantMems`
	// column and the `wantDataSec` column are checking two halves of one arm, and neither alone would
	// notice the other's absence. Ceiling division, not floor: three bytes is *one* page.
	{
		src:         `(module (memory (data "abc")))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1, Max: 1, HasMax: true}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x03, 'a', 'b', 'c'},
	},
	// The **empty** payload, which is the row that pins ceiling division as ceiling *of zero*: no
	// bytes is zero pages, not one. A `+1` implementation passes the row above and fails here.
	{
		src:         `(module (memory (data)))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Max: 0, HasMax: true}}},
		wantDataSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x00},
	},
	// **Exactly one page**, the boundary the ceiling formula is most likely to be wrong at: 65536
	// bytes is one page and 65537 is two. Written with a repeat rather than a literal, so the row
	// states the length arithmetic rather than transcribing it.
	{
		src:         `(module (memory (data "` + strings.Repeat(`a`, 0x10000) + `")))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1, Max: 1, HasMax: true}}},
		wantDataSec: append([]byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x80, 0x80, 0x04}, dataFill(0x10000)...),
	},
	{
		src:      `(module (memory (data "` + strings.Repeat(`a`, 0x10001) + `")))`,
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 2, Max: 2, HasMax: true}}},
		wantDataSec: append([]byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x81, 0x80, 0x04},
			dataFill(0x10001)...),
	},
	// **The `i64` addrtype**, which is the other half of `at_const`: the synthesized offset must be
	// `i64.const` (0x42), not `i32.const` (0x41). An `i32.const` offset in an i64 memory is a
	// validation error this encoder has no business producing.
	//
	// `wantMems` cannot say the memory is 64-bit — `binary.Memory` is `{Limits}` and retains no address
	// type, which is the same discard that made `TestEncodeWritesTheAddressTypeFlagBit` a separate
	// instrument. So the addrtype's *two* consequences are checked in two places: the limits flag bit
	// there, and the offset opcode here. Neither column sees the other's, and the row is paired with
	// the i32 sugar row above precisely so the 0x41/0x42 difference is the only thing between them.
	{
		src:         `(module (memory i64 (data "abc")))`,
		wantMems:    []binary.Memory{{Limits: binary.Limits{Min: 1, Max: 1, HasMax: true, Addr64: true}}},
		wantDataSec: []byte{0x01, 0x00, 0x42, 0x00, 0x0b, 0x03, 'a', 'b', 'c'},
	},
	// **The sugar's segment belongs to *its own* memory**, not to memory 0 — `Active (x, offset)`
	// where `x` is the field's index (parser.mly:1129-1131). With a memory ahead of it the segment
	// takes the `0x02` arm with index 1, and an emitter defaulting to 0 would write the bytes into
	// the wrong memory: a well-formed image denoting a different module.
	{
		src: `(module (memory 1) (memory (data "xy")))`,
		wantMems: []binary.Memory{
			{Limits: binary.Limits{Min: 1}},
			{Limits: binary.Limits{Min: 1, Max: 1, HasMax: true}},
		},
		wantDataSec: []byte{0x01, 0x02, 0x01, 0x41, 0x00, 0x0b, 0x02, 'x', 'y'},
	},
	// Both spellings in one module, which is what `datasSeen` counts and what puts the two in source
	// order: the sugar's segment is first because its field is.
	{
		src: `(module (memory (data "s")) (data (memory 0) (i32.const 4) "f"))`,
		wantMems: []binary.Memory{
			{Limits: binary.Limits{Min: 1, Max: 1, HasMax: true}},
		},
		wantDataSec: []byte{
			0x02,
			0x00, 0x41, 0x00, 0x0b, 0x01, 's',
			0x00, 0x41, 0x04, 0x0b, 0x01, 'f',
		},
	},

	// ## Section 12, whose condition is about instructions rather than segments
	//
	// `data_count_section` is guarded by `Free.((module_ m).datas <> Set.empty)` (encode.ml:1109), and
	// `free.ml`'s `data` for a *segment* is `segmentmode memories mode` (:217) — a segment contributes
	// nothing. So the two rows that matter are the two the obvious `len(datas) > 0` test gets
	// backwards, and they are both here:
	//
	//   - segments and **no** data-referencing instruction: no section 12 (every row above)
	//   - a `data.drop` and **no** segments: section 12 with a count of **zero**
	//
	// The second was measured before it was written: `(module (func (data.drop 0)))` emitted no
	// section 12 and `binary.DecodeModule` rejected it with `data count section required`, the
	// decoder's mirror of the same four `free.ml` lines.
	{
		src:              `(module (func (data.drop 0)))`,
		want:             []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:        []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x09, Prefix: 0xfc, Imm0: 0}, {Op: 0x0b}}}},
		wantDataCountSec: []byte{0x00},
	},
	// A `data.drop` **and** a segment, which is the shape a real module has: section 12 counts the
	// segments (1), and the image carries both sections in `module_`'s order — 12 before 10, 11 last.
	{
		src:              `(module (memory 1) (func (data.drop 0)) (data "x"))`,
		want:             []binary.CompType{{Kind: binary.CompFunc}},
		wantMems:         []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantFuncs:        []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x09, Prefix: 0xfc, Imm0: 0}, {Op: 0x0b}}}},
		wantDataSec:      []byte{0x01, 0x01, 0x01, 'x'},
		wantDataCountSec: []byte{0x01},
	},
	// A **symbolic** data index in the body, resolved against the space the segment's `bindidx_opt`
	// bound — so this row asserts `retainIdx`'s `catData` path and section 12's condition at once.
	{
		src:              `(module (memory 1) (data $seg (i32.const 0) "a") (func (data.drop $seg)))`,
		want:             []binary.CompType{{Kind: binary.CompFunc}},
		wantMems:         []binary.Memory{{Limits: binary.Limits{Min: 1}}},
		wantFuncs:        []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x09, Prefix: 0xfc, Imm0: 0}, {Op: 0x0b}}}},
		wantDataSec:      []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 'a'},
		wantDataCountSec: []byte{0x01},
	},

	// # Section 9, all eight arms (#8, 0016)
	//
	// Every payload here is written by hand from `encode.ml`'s `elem` (:1062-1084), which is the second
	// reading this table exists to be. Nothing calls `encodeElems` to find out what it should say —
	// reconstructing a flag with a helper that branched on the mode would be an echo of the code under
	// test (grave #106), and the flag *is* the thing most easily got wrong.
	//
	// The eight arms split into two families of four, and which family a segment takes is a question
	// about its **content**: `is_elem_kind rt && for_all is_elem_index cs`. So the pairs below that look
	// like spelling variants are not — `(elem func $f)` and `(elem funcref (ref.func $f))` denote the
	// same segment and encode differently, one 0x01 and one 0x05, because `funcref` is nullable and
	// `(ref func)` is not. Those two rows are the table's central assertion and the reason
	// `encodeElems`' derivation is not decorative.
	//
	// Read alongside `TestEncodeMatchesTheReferenceOnElemFlags`, which is the same fact from the other
	// direction: this table asserts the bytes for eleven modules, that test asserts the *flag* over the
	// cross product of mode, reftype and element shape, so an arm that is right here and wrong for a
	// mode this table does not spell fails there.

	// Arm 1, flag 0x01 — passive, elemkind, index form. The commonest shape in the corpus after the
	// abbreviated reftype, 482 of 1383 `(elem` forms.
	{src: `(module (elem func))`, wantElemSec: []byte{0x01, 0x01, 0x00, 0x00}},
	{
		src:         `(module (func) (elem func 0))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x01, 0x00, 0x01, 0x00},
	},
	// A **symbolic** func index in the element list, which is `retainIdx`'s `catFunc` path through
	// `immPatch` — the segment is parsed before the func it names, so the index is not knowable at the
	// cursor and a row with a literal `0` could not tell a working deferral from an eager one.
	{
		src:         `(module (elem func $f $f) (func $f))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x01, 0x00, 0x02, 0x00, 0x00},
	},
	// Arm 2, flag 0x00 — active at table 0, index form, and **no elemkind byte in the payload**: the
	// short active form omits it (`u32 0x00l; const c; vec elem_index cs`, :1069-1070). A row that
	// expected 0x00 twice would pass an encoder that wrote the elemkind unconditionally, and the two
	// zeroes would be indistinguishable by eye — which is why the count is spelled out here.
	{
		src:         `(module (table 1 funcref) (func) (elem (i32.const 0) 0))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 0x00},
	},
	// The same arm with an **empty** element list, which is the one shape of five that reaches an empty
	// list (`elemidx_list` is `idx_list`, nullable at parser.mly:500) and 29 corpus rows write. It also
	// pins `allElemIndex`'s vacuous truth: an empty list takes the *index* family, so this is flag 0x00
	// and not 0x04.
	{
		src:         `(module (table 1 funcref) (elem (i32.const 0)))`,
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantElemSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x00},
	},
	// Arm 3, flag 0x02 — active at a **non-zero** table, index form: `idx x; const c; elem_kind rt; vec`
	// (:1071-1073), so this is the one arm whose payload carries the table index *and* the elemkind, in
	// that order with the offset between them. Two tables so index 1 exists.
	{
		src:  `(module (table 1 funcref) (table $t 1 funcref) (func) (elem (table $t) (i32.const 0) func 0))`,
		want: []binary.CompType{{Kind: binary.CompFunc}},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x02, 0x01, 0x41, 0x00, 0x0b, 0x00, 0x01, 0x00},
	},
	// A `(table 0)` spelled explicitly, which must produce the **same bytes** as arm 2's row above: the
	// discriminator is the resolved index, never whether the text wrote a tableuse. `encodeDatas` makes
	// the identical claim for `(data (memory 0) …)` and this is section 9's half of it.
	{
		src:         `(module (table 1 funcref) (func) (elem (table 0) (i32.const 0) func 0))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 0x00},
	},
	// Arm 4, flag 0x03 — declarative, index form.
	{
		src:         `(module (elem declare func $f) (func $f))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x03, 0x00, 0x01, 0x00},
	},
	// Arm 5, flag 0x05 — passive, **expression** form, and this row is the pair to the flag-0x01 row at
	// the top. `(elem funcref (ref.func 0))` and `(elem func 0)` denote one segment; `funcref` is
	// `(Null, FuncHT)`, so `is_elem_kind` is false and the whole family changes. The payload carries a
	// **reftype** byte (0x70) where the index form carried an elemkind (0x00), and each element is a full
	// const expression — `d2 00 0b`, `ref.func 0` then `end` — where the index form wrote a bare LEB.
	//
	// **An element expression is not length-prefixed, and this row's want column said it was.** The bytes
	// here were `05 70 01 03 d2 00 0b` — a `0x03` size in front of the expression — copied from the data
	// segment's shape one section over, where the payload genuinely is `vec byte`. `const c` is
	// `list instr c.it; end_ ()` (encode.ml:912-913): the instructions, then `0x0b`, and nothing sizing
	// them. Only the *code* section's bodies are sized, through `gap32`. wabt 1.0.41 `--enable-all`
	// writes `09 07 01 05 70 01 d2 00 0b` for this module, agreeing to the byte.
	//
	// Worth the paragraph because it is the failure mode a hand-written want column exists to catch and
	// also the one it can cause: the encoder was right and the fixture was wrong, so the red board was
	// the fixture's error, and had the fixture been generated from the encoder both would have agreed on
	// the invented prefix. What settled it was the reference's executable, neither side of the test.
	{
		src:         `(module (func) (elem funcref (ref.func 0)))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x05, 0x70, 0x01, 0xd2, 0x00, 0x0b},
	},
	// A segment **mixing** a one-instruction element with a two-instruction one, which is the row that
	// forces `for_all` to be a fold rather than a decision made on the first element: element 0
	// satisfies `is_elem_index` and element 1 does not, so the whole segment takes the expression family
	// even though its type is `funcref` either way. A `funcs`-vs-`exprs` choice keyed on element 0
	// passes every other row here.
	//
	// **`(item (ref.func 0) (ref.func 0))` rather than `(ref.null func)`, and the swap is the frontier's
	// doing rather than a preference.** `ref.null` takes a heaptype immediate, a shape deliberately
	// outside `encodableShapes`, so the first draft of this row asked `EncodeModule` for a module the
	// encoder refuses — the fixture reaching past a boundary this PR does not move, not a defect in it.
	// The `(item …)` spelling keeps every instruction inside the frontier while failing
	// `is_elem_index` for the reason that predicate actually names: `[{it = RefFunc _}]` matches a list
	// of length *one*, so two `ref.func`s fail it where one passes. Ill-typed, and irrelevant that it is
	// — the encoder writes what the grammar admits and validation is #9's. wabt 1.0.41 `--enable-all
	// --no-check` writes `01 05 70 01 d2 00 d2 00 0b` for it, which is this row's want column.
	{
		src:         `(module (func) (elem funcref (ref.func 0) (item (ref.func 0) (ref.func 0))))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x05, 0x70, 0x02, 0xd2, 0x00, 0x0b, 0xd2, 0x00, 0xd2, 0x00, 0x0b},
	},
	// Arm 6, flag 0x04 — active at table 0, expression form, **and the guard**: available only when the
	// reftype is exactly `funcref` (`when rt = (Null, FuncHT)`, :1079), because 0x04's payload has no
	// reftype field. The element is the two-instruction `(item …)` above, which fails `is_elem_index`,
	// so the segment cannot take flag 0x00 and this is the arm that remains.
	{
		src:         `(module (func) (table 1 funcref) (elem (i32.const 0) funcref (item (ref.func 0) (ref.func 0))))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantElemSec: []byte{0x01, 0x04, 0x41, 0x00, 0x0b, 0x01, 0xd2, 0x00, 0xd2, 0x00, 0x0b},
	},
	// Arm 7, flag 0x07 — declarative, expression form.
	{
		src:         `(module (func) (elem declare funcref (ref.func 0)))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x07, 0x70, 0x01, 0xd2, 0x00, 0x0b},
	},
	// Arm 8, flag 0x06 — active at a non-zero table, expression form: `idx x; const c; reftype rt; vec`
	// (:1081), the mirror of arm 3 with a reftype where the elemkind was, and the same field order.
	{
		src: `(module (func) (table 1 funcref) (table $t 1 funcref) ` +
			`(elem (table $t) (i32.const 0) funcref (item (ref.func 0) (ref.func 0))))`,
		want:      []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs: []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantTabs: []binary.Table{
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
			{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		},
		wantElemSec: []byte{0x01, 0x06, 0x01, 0x41, 0x00, 0x0b, 0x70, 0x01, 0xd2, 0x00, 0xd2, 0x00, 0x0b},
	},
	// Two segments, so the section's own vector count is asserted by something other than 1 — and in
	// source order, which is the order the *table* is initialized in and therefore semantic.
	{
		src:         `(module (func) (elem func 0) (elem declare func 0))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x02, 0x01, 0x00, 0x01, 0x00, 0x03, 0x00, 0x01, 0x00},
	},
	// The `(table … (elem …))` sugar, which defines a table *and* a segment from one field: the table is
	// sized `min = max = len(einit)` (parser.mly:1216-1222) and the segment is active at that table with
	// a synthesized zero offset. Two rows because the sugar's two arms differ in the element list's form,
	// and the encoded flag differs with it — the idx-list arm is `(NoNull, FuncHT)` by the action's own
	// `let rt`, the elemexpr arm takes whatever reftype was written.
	//
	// This is the row the section-9 withdrawal check is a live control for: the arm must `defineElem` as
	// well as `defineTable`, and before it did, the table's size came out of an element count whose
	// elements went nowhere.
	{
		src:         `(module (func) (table funcref (elem 0 0)))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 2, Max: 2, HasMax: true}}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x02, 0x00, 0x00},
	},
	{
		src:         `(module (func) (table funcref (elem (ref.func 0))))`,
		want:        []binary.CompType{{Kind: binary.CompFunc}},
		wantTabs:    []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1, Max: 1, HasMax: true}}},
		wantFuncs:   []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: 0x0b}}}},
		wantElemSec: []byte{0x01, 0x04, 0x41, 0x00, 0x0b, 0x01, 0xd2, 0x00, 0x0b},
	},
}

// dataFill is the payload the two page-boundary rows above expect, stated as an argument rather than
// transcribed.
//
// Its whole purpose is that the row's `src` and its `wantDataSec` are built from the *same* length by
// two different expressions — `strings.Repeat` in the text and this in the bytes — so a row whose two
// halves disagree is a compile-time-visible mismatch of one number rather than a silent one buried in
// 65536 hex digits. A literal is not available at this size and calling the encoder would be an echo.
func dataFill(n int) []byte { return []byte(strings.Repeat("a", n)) }

// TestEncodeRoundTripsThroughTheDecoder is the encoder's acceptance check, and the direction of the
// authority is fixed: the decoder has 4162 vectors of conformance record and this encoder has none,
// so a disagreement is the encoder's.
//
// It asserts two things that are genuinely different, and the second is the one with teeth:
//
//   - the bytes decode at all (the round trip)
//   - the module they decode to is the one the *text* denotes (the want column, read from the wat
//     by hand rather than from the encoder's output)
//
// A round trip alone cannot see a shared misconception — an encoder and decoder that both had
// params and results backwards would agree perfectly. The independent statement is what makes this
// a check. The fully independent witness is the wabt corpus, at module level, which is #67's other
// half; this is the part that can be asserted inside the package.
func TestEncodeRoundTripsThroughTheDecoder(t *testing.T) {
	// Vacuity, and **per partition rather than one total**. The table is 132 rows, of which 43 assert a
	// function body; a single floor lets the func half go to zero and be absorbed by the other 89,
	// which is the empty-half-hiding-behind-a-full-one defect (grave #105) with a table for a
	// partner. So the code section's rows are counted separately from the table's size.
	//
	// The floor was 12 against 84 rows for five sections' worth of growth — a bound so far from what
	// it bounds that it ran, agreed, and said nothing. Set close enough to the real counts to notice a
	// deletion, and loose enough that adding a row is not a failure. **Every floor moves with the
	// table in the same PR that grows it**, or the distance re-opens: the memarg rows (#8) added seven (84→91,
	// 14→21) and a floor left at 70/12 would have gone straight back into the vacuum it was raised
	// out of, silently, because a floor never complains about slack.
	//
	// **A partition gets its own floor as soon as it exists**, which section 11's rows are the third
	// application of. The data rows are the *only* instrument over section 11's mode byte — modes 0
	// and 2 decode to identical `DataSegment` values, so `wantDataSec` is the whole assertion over the
	// one fact the round trip drops — and a total-only floor would let all 21 of them go while the
	// other 92 held the number up. That is the
	// empty-half-behind-a-full-one shape (grave #105) exactly, and the reason it gets a counter rather
	// than trust is that this partition is the one whose loss is invisible everywhere else.
	//
	// **Raised with the block family (#7): 113→132 rows, 24→43 with a body.** The func floor moves
	// nineteen because that is what the block family added, and leaving it at 22 would have put a
	// half-empty partition back inside the slack it was raised out of — a floor never complains about
	// slack, which is why moving it is part of the same edit and not a follow-up.
	//
	// **Raised with `br_table` (#8): 132→135 rows, 43→46 with a body, and a new partition at 3.**
	// Three rows and three floors moved in the same edit, for the reason above.
	//
	// **Raised again at 153 rows / 62 bodies, and the gap is the confession**: the table grew from 135
	// to 153 across the element-segment and instruction work while these numbers sat still, so the
	// distance the paragraph above says to close had quietly re-opened to 26 rows and 19 bodies. A
	// floor never complains about slack — that is the whole of its blind spot — so the only thing that
	// keeps it honest is moving it in the PR that grows the table, and the rule was written here and
	// then not followed. Recorded rather than silently corrected, because the *pattern* of drift is
	// what a reader needs: the instruction is "in the same edit", and the failure mode is that nothing
	// fails when it isn't.
	// **Raised with section 6 (#8): 153→171 rows, 62→68 bodies, and a new partition at 11.** Moved in
	// the same edit that grew the table, which is what the paragraph above asks for and what the
	// paragraph above records not having been done last time.
	if len(encodableModules) < 164 {
		t.Fatalf("encodableModules has %d rows, want >=164 (171 at this commit): a table this check "+
			"reads is a table whose size is part of the assertion, since a comparison over an empty "+
			"set succeeds", len(encodableModules))
	}
	withFuncs, withData, withDataCount, withLabels, withElem, withGlobals := 0, 0, 0, 0, 0, 0
	for _, tc := range encodableModules {
		if len(tc.wantFuncs) > 0 {
			withFuncs++
		}
		if len(tc.wantGlobals) > 0 {
			withGlobals++
		}
		if tc.wantDataSec != nil {
			withData++
		}
		if tc.wantElemSec != nil {
			withElem++
		}
		if tc.wantDataCountSec != nil {
			withDataCount++
		}
		if slices.ContainsFunc(tc.wantFuncs, func(f binary.Func) bool { return f.Labels != nil }) {
			withLabels++
		}
	}
	if withFuncs < 64 {
		t.Fatalf("only %d of %d encodableModules rows assert a function body, want >=64 (68 at this "+
			"commit): the code section is the newest and least-covered half of this table, and a "+
			"total-only floor would let its rows go to zero behind the other sections'",
			withFuncs, len(encodableModules))
	}
	// Section 6's own floor, and **its reason is the opposite of section 9's and 11's**, which is worth
	// stating because the three sit together and a reader will assume one argument covers all of them.
	// The byte-column floors exist because decoding *discards* a fact (the flag byte, the mode byte);
	// this one exists because `binary.Global` discards nothing, so `wantGlobals` is a structural column
	// and its risk is not invisibility but **absorption** — eleven rows behind 160 others is the
	// empty-half-behind-a-full-one shape (grave #105) regardless of what the column asserts. What these
	// rows are the only instrument over is the mutability byte and the *reversed* field order
	// (`valtype` then `mutability`, encode.ml:193-194), neither of which any other partition touches.
	//
	// A floor and not an exact count, on the criterion the floors rule states: the number of round-trip
	// rows a section deserves is a judgement about coverage, not a derivable quantity, so there is no
	// sharper instrument being passed over here. Where an exact count *is* knowable it is pinned instead
	// — `TestRefNullEncodesItsHeapType` is exhaustive over the two encodable heaptypes.
	if withGlobals < 9 {
		t.Fatalf("only %d of %d encodableModules rows assert a global section, want >=9 (11 at this "+
			"commit): these rows are the only instrument over the mutability byte and over section 6's "+
			"reversed field order, and a total-only floor would let them go behind the other sections'",
			withGlobals, len(encodableModules))
	}
	// **Its own floor, and it is the one this function *computed and did not assert*** — `withElem`
	// was counted in the loop above and then read by nothing, which is a counter with no consumer: the
	// shape of a floor that was going to be written and wasn't. An unasserted count is strictly worse
	// than an absent one, because the loop reads as though the partition is covered.
	//
	// Section 9 needs it because **the retained struct does not determine the flag byte**, which is a
	// narrower claim than the one this comment used to make and is the true one. It said "nothing
	// retains element segments either"; `binary.Module.Elems` has existed since 165e77e
	// (`module.go:560`), so that sentence was false at the moment it was written and the floor's
	// stated reason was fiction while the floor itself was sound. What is actually unrecoverable was
	// measured by decoding one segment under each of the eight legal flags and printing the struct:
	//
	//	flags 0 and 2 → Mode:active Tbl:0 ByExpr:false ElemType:funcref nFuncs:1  — identical
	//	flags 4 and 6 → Mode:active Tbl:0 ByExpr:true  ElemType:funcref nExprs:1  — identical
	//
	// The implicit-table and explicit-table encodings of an active segment naming table 0 normalize
	// to the same value, exactly as `encode.ml` chooses between them, so a comparison against `Elems`
	// scores flag 0 and flag 2 the same and *cannot* see an encoder that emits the longer form.
	// These rows' byte-level `wantElemSec` is the only instrument that can. They are also the only
	// place the two elemkind/reftype encodings are pinned. Sixteen rows behind 137 others is exactly
	// the empty-half-behind-a-full-one shape (grave #105).
	if withElem < 14 {
		t.Fatalf("only %d of %d encodableModules rows assert an element section payload, want >=14 "+
			"(16 at this commit): flags 0/2 and 4/6 decode to identical `ElemSegment` values, so "+
			"these rows are the only instrument over section 9's flag byte, elemkinds and reftypes "+
			"— and this count was computed and unasserted before now", withElem, len(encodableModules))
	}
	// Its own floor, because the label side table is a partition of the code rows that the body
	// comparison **structurally cannot see**: `Func.Labels` is a map keyed by instruction index, and
	// an encoder that dropped every label vector produces byte-identical `Instr` values. Losing these
	// three rows leaves `br_table`'s three transformations — the count, the split_last default, and
	// the never-empty sequence — asserted by nothing, behind 43 body rows holding the number up.
	// Three is the minimum, and each row's unique catch was measured rather than asserted: the empty
	// table catches an encoder that writes no vector at all, the asymmetric one catches the
	// split_last default and the vector's order, and the uniform one catches a deduplicating
	// encoder. Every row catches a wrong count.
	if withLabels < 3 {
		t.Fatalf("only %d of %d encodableModules rows assert a label vector, want >=3 (3 at this "+
			"commit): `Func.Labels` is invisible to the body comparison, so these rows are the only "+
			"instrument over `br_table`'s three text-to-wire transformations (0016)",
			withLabels, len(encodableModules))
	}
	// The data floor's reason is the element floor's, measured the same way and with the same
	// correction: it said "nothing retains data segments (#7)", and `binary.Module.Datas` has
	// existed since 0015 — the ADR that retained it is cited three lines from the claim. What the
	// struct cannot recover is the *mode byte*: decoding one segment under each legal mode prints
	// `Passive:false Mem:0 nOffset:2` for **both** mode 0 (implicit memory) and mode 2 (explicit
	// memory 0), so a comparison against `Datas` cannot see an encoder that writes the longer form.
	// Only these rows' byte-level `wantDataSec` can. Same shape as section 9's flags 0/2, because it
	// is the same normalization in `encode.ml`.
	if withData < 19 {
		t.Fatalf("only %d of %d encodableModules rows assert a data section payload, want >=19 (21 at "+
			"this commit): modes 0 and 2 decode to identical `DataSegment` values, so these rows are "+
			"the only instrument over section 11's mode byte — losing them leaves the section emitted "+
			"and unchecked", withData, len(encodableModules))
	}
	// Its own floor at 2, small but not foldable into the one above: section 12's *condition* is a
	// question about instructions rather than segments, so a row asserting a data section says nothing
	// about it. Two is the minimum that covers both directions — a `data.drop` with no segments and
	// one with a segment — which is the pair the obvious `len(datas) > 0` test gets backwards.
	if withDataCount < 2 {
		t.Fatalf("only %d of %d encodableModules rows assert a data count section, want >=2 (3 at this "+
			"commit): section 12's condition is `free.ml`'s instruction set, not the segment list, and "+
			"a single row cannot cover both directions of that", withDataCount, len(encodableModules))
	}
	for _, tc := range encodableModules {
		t.Run(tc.src, func(t *testing.T) {
			b, err := EncodeModule([]byte(tc.src))
			if err != nil {
				t.Fatalf("EncodeModule refused a module the encoder is meant to write: %v", err)
			}
			m := decodeForTest(t, b)

			if len(m.Types) != len(tc.want) {
				t.Fatalf("encoded % x, which decodes to %d types, want %d: %v",
					b, len(m.Types), len(tc.want), m.Types)
			}
			if len(m.Memories) != len(tc.wantMems) {
				t.Fatalf("encoded % x, which decodes to %d memories, want %d: %v",
					b, len(m.Memories), len(tc.wantMems), m.Memories)
			}
			for i, want := range tc.wantMems {
				if got := m.Memories[i]; got != want {
					t.Errorf("memory %d is %+v, want %+v", i, got, want)
				}
			}
			if len(m.Tables) != len(tc.wantTabs) {
				t.Fatalf("encoded % x, which decodes to %d tables, want %d: %v",
					b, len(m.Tables), len(tc.wantTabs), m.Tables)
			}
			for i, want := range tc.wantTabs {
				if got := m.Tables[i]; got != want {
					t.Errorf("table %d is %+v, want %+v", i, got, want)
				}
			}
			if len(m.Imports) != len(tc.wantImports) {
				t.Fatalf("encoded % x, which decodes to %d imports, want %d: %v",
					b, len(m.Imports), len(tc.wantImports), m.Imports)
			}
			for i, want := range tc.wantImports {
				if got := m.Imports[i]; got != want {
					t.Errorf("import %d is %+v, want %+v", i, got, want)
				}
			}
			if len(m.Globals) != len(tc.wantGlobals) {
				t.Fatalf("encoded % x, which decodes to %d defined globals, want %d: %v",
					b, len(m.Globals), len(tc.wantGlobals), m.Globals)
			}
			for i, want := range tc.wantGlobals {
				got := m.Globals[i]
				if got.Type != want.Type || got.Mutable != want.Mutable {
					t.Errorf("global %d is type %v mutable=%v, want type %v mutable=%v: the two bytes "+
						"are written in the *binary* order (valtype then mutability, encode.ml:193) "+
						"which is the reverse of the text's `(mut i32)`", i, got.Type, got.Mutable,
						want.Type, want.Mutable)
				}
				if !slices.Equal(got.Init, want.Init) {
					t.Errorf("global %d has initializer %+v, want %+v: an initializer written into the "+
						"wrong global's expression, or run together with its neighbour's, decodes clean "+
						"and denotes a different module — the accept-direction defect no suite vector "+
						"can see (§9 G-3)", i, got.Init, want.Init)
				}
			}
			if len(m.Exports) != len(tc.wantExports) {
				t.Fatalf("encoded % x, which decodes to %d exports, want %d: %v",
					b, len(m.Exports), len(tc.wantExports), m.Exports)
			}
			for i, want := range tc.wantExports {
				if got := m.Exports[i]; got != want {
					t.Errorf("export %d is %+v, want %+v — the Index is the resolved one, so a "+
						"wrong resolution shows up here and not in any parse verdict", i, got, want)
				}
			}

			// Sections 11 and 12, by payload bytes — see the table's header for why this column is
			// bytes and why that is temporary. Read through `Section.Payload`, so a section written
			// with the wrong id, or written twice, or out of order, fails in `decodeForTest` above
			// rather than being papered over here.
			for _, s := range []struct {
				id   binary.SectionID
				what string
				want []byte
			}{
				{binary.SectionElement, "element", tc.wantElemSec},
				{binary.SectionData, "data", tc.wantDataSec},
				{binary.SectionDataCount, "data count", tc.wantDataCountSec},
			} {
				got, found := sectionPayload(m, s.id)
				switch {
				case s.want == nil && found:
					t.Errorf("encoded % x, which carries a %s section (% x) for a module that "+
						"should have none: an unexpected section 11 or 12 is a decode failure on "+
						"any real consumer, not a harmless extra", b, s.what, got)
				case s.want != nil && !found:
					t.Errorf("encoded % x, which carries no %s section: want payload % x. No other "+
						"column in this table asserts sections 9, 11 or 12, so a dropped one is "+
						"invisible to the round trip above", b, s.what, s.want)
				case s.want != nil && !slices.Equal(got, s.want):
					t.Errorf("the %s section's payload is % x, want % x: the mode byte does not "+
						"survive decoding — modes 0 and 2, and element flags 0 and 2, produce "+
						"identical segment values — so the longer form decodes clean and denotes a "+
						"module this comparison would score correct", s.what, got, s.want)
				}
			}

			// The function and code sections, asserted as one thing because the decoder zips them
			// and a length disagreement between the two is malformed (`function and code section
			// have inconsistent lengths`) — so a wrong N here fails at `decodeForTest` above, and
			// what this reads is the content.
			if len(m.Funcs) != len(tc.wantFuncs) {
				t.Fatalf("encoded % x, which decodes to %d defined functions, want %d: %v",
					b, len(m.Funcs), len(tc.wantFuncs), m.Funcs)
			}
			for i, want := range tc.wantFuncs {
				got := m.Funcs[i]
				if got.TypeIndex != want.TypeIndex {
					t.Errorf("func %d has type index %d, want %d: section 3 is a vector of interned "+
						"type indices, not of positions", i, got.TypeIndex, want.TypeIndex)
				}
				if !slices.Equal(got.Locals, want.Locals) {
					t.Errorf("func %d has locals %v, want %v: both sides are the wire form's "+
						"run-length groups since #138, so a mismatch in the *counts* means the RLE "+
						"fold disagrees with the reference's `combine` (encode.ml:238) — which the "+
						"old flat comparison could not see, five singleton runs and one run of five "+
						"flattening alike", i, got.Locals, want.Locals)
				}
				if !slices.Equal(got.Body, want.Body) {
					t.Errorf("func %d has body %+v, want %+v: a body whose instructions are in the "+
						"wrong order decodes clean and computes something else, which is the "+
						"accept-direction defect no suite vector can see (§9 G-3)",
						i, got.Body, want.Body)
				}
				// **The label side table, which `Body` structurally cannot see.** `br_table`'s
				// vector lives in `Func.Labels` keyed by instruction index (0016), so the body
				// comparison above agrees for an encoder that wrote the labels in the wrong order,
				// wrote the wrong count, or wrote the default as a table entry — the instruction's
				// `Op` and two words are identical in every one of those cases. Compared per
				// instruction index rather than as two maps so a failure names the instruction, and
				// **through `LabelVector`** so "no vector" and "an empty vector" stay distinct: an
				// empty table is legal and means every index takes the default.
				for j := range max(len(got.Body), len(want.Body)) {
					gotV, gotOK := got.LabelVector(j)
					wantV, wantOK := want.LabelVector(j)
					switch {
					case gotOK != wantOK:
						t.Errorf("func %d instruction %d: retained a label vector = %v, want %v: "+
							"the presence of a vector is a fact about which opcode was written, "+
							"and the body comparison above cannot see it", i, j, gotOK, wantOK)
					case gotOK && !slices.Equal(gotV, wantV):
						t.Errorf("func %d instruction %d has label vector %v, want %v: the "+
							"written order and the written length are both the wire form's, and "+
							"the default is *not* a member of this vector (0016) — an encoder that "+
							"took the first written label as the default encodes the table "+
							"shifted by one and every other column here agrees", i, j, gotV, wantV)
					}
				}
			}

			for i, want := range tc.want {
				got := m.Types[i]
				if got.Kind != want.Kind {
					t.Errorf("type %d is a %v, want %v", i, got.Kind, want.Kind)
				}
				for _, g := range []struct {
					what      string
					got, want []binary.ValType
				}{
					{"params", got.Func.Params, want.Func.Params},
					{"results", got.Func.Results, want.Func.Results},
				} {
					if len(g.got) != len(g.want) {
						t.Errorf("type %d %s: got %v, want %v", i, g.what, g.got, g.want)
						continue
					}
					for j := range g.want {
						if g.got[j] != g.want[j] {
							t.Errorf("type %d %s[%d]: got %v, want %v", i, g.what, j, g.got[j], g.want[j])
						}
					}
				}
			}
		})
	}
}

// TestEncodeWritesTheAddressTypeFlagBit asserts the one bit the round trip structurally cannot see.
//
// **This is a byte-level assertion, and writer.go's header says byte equality is not the criterion —
// so the exception needs its reason.** The reason is that the round trip has no opinion here:
// `binary.Limits` is `{Min, Max, HasMax}` and carries **no address type**, so `decodeLimits` reads
// flags bit 2, uses it to pick a gate, and then discards which one it saw. A memory64 with min 1 and
// a memory32 with min 1 decode to *identical* `binary.Memory` values. Every other encoding fact in
// this file is falsified by round trip; this one has nothing to round-trip against.
//
// Found by the falsification budget rather than by design. Two probes were run against the flags
// byte: writing bit 1 (shared) instead of bit 2 *passed* the whole suite, which said no row
// exercised an i64 addrtype — fixed by adding rows and turning Memory64 on. Then dropping bit 2
// **entirely** passed again, with the rows present, and that second pass is what identified the
// blindness as structural rather than as missing coverage. A control that cannot fail is worth more
// as a discovery than as a control (#108's birth rule), and the discovery here is that the
// instrument was wrong, not the table.
//
// The expected flags are read from `encode.ml:187` — `flag (max <> None) 0 + flag (at = I64AT) 2` —
// not from this encoder's output. The image is scanned for the memory section rather than indexed at
// a fixed offset, so a row's type section does not shift the assertion.
//
// The independent witness for the same fact is the wabt corpus (#67 half 2), and **its reach here was
// measured rather than assumed, because the first version of this sentence was wrong.** It claimed the
// corpus "compares whole images and therefore does see the bit". Joined on `(File, Ordinal)` over the
// suite, the emitter produces 51 encodable modules, **15** of which have a wabt witness at all, and of
// those 15 only **5** have a non-empty table or memory section — the other ten agree empty-to-empty,
// which is no agreement. Two of the five do carry this bit: `memory64-imports#12` and `#13`, where wabt
// and this encoder both write `01 70 04 0a`, an i64 table. So the corroboration is real and it is two
// modules deep.
//
// The reason it is not deeper is worth keeping, because it is the shape of the whole witness: wabt
// **cannot parse 31 of the suite's files**, and among them are `memory`, `memory64`, `table`, and
// `table64` — the four that exercise this section most — all four rejected on `(module definition …)`,
// a spec form wabt does not implement, which fails the entire file. Every one of the 36 modules this
// PR made encodable is in a skipped file. *The second opinion is absent from exactly the region the
// work is in*, which is the same lesson as scoping a control to the current sample: an independent
// witness has a blind spot too, and it is not the same one. Hence this test — it covers the region the
// corpus cannot reach, and the corpus covers the two rows it can.
func TestEncodeWritesTheAddressTypeFlagBit(t *testing.T) {
	for _, tc := range []struct {
		src       string
		wantFlags byte
	}{
		{`(module (memory 1))`, 0x00},
		{`(module (memory i32 1))`, 0x00},
		{`(module (memory 1 2))`, 0x01},
		{`(module (memory i64 1))`, 0x04},
		{`(module (memory i64 1 2))`, 0x05},
		// This row exists to falsify `bytesIndex`, not the flags byte. The table section is id 4 and
		// is emitted before the memory section, so a `bytesIndex` that *searched* for the byte `05`
		// instead of walking the framing would stop on this table's limits minimum — the earlier
		// occurrence — and read the memory section's id and size as a count and a flags byte. Without
		// this row that shortcut passes every case above, which is what it did when it was installed
		// as a probe: the helper was correct in general and asserted by nothing.
		{`(module (table 5 funcref) (memory i64 1))`, 0x04},
	} {
		t.Run(tc.src, func(t *testing.T) {
			b, err := EncodeModule([]byte(tc.src))
			if err != nil {
				t.Fatalf("EncodeModule refused a module the encoder is meant to write: %v", err)
			}
			// The memory section is id 5, and every module here has exactly one memory — so the
			// section body is `01` (the vector count) followed by the flags byte. Located by scanning
			// for the id rather than by a fixed offset.
			i := bytesIndex(b, secMemory)
			if i < 0 {
				t.Fatalf("the encoder produced % x with no memory section (id %d)", b, secMemory)
			}
			// id, length, count, flags
			if len(b) < i+4 {
				t.Fatalf("the memory section in % x is truncated", b)
			}
			if got, want := b[i+2], byte(0x01); got != want {
				t.Fatalf("the memory section in % x declares %d entries, want %d", b, got, want)
			}
			if got := b[i+3]; got != tc.wantFlags {
				t.Errorf("the limits flags byte is %#02x, want %#02x: bit 0 is a maximum and bit 2 is "+
					"a 64-bit address type (encode.ml:187), and neither is visible to a round trip "+
					"because binary.Limits carries no address type", got, tc.wantFlags)
			}
		})
	}
}

// TestEncodeWritesTheNaturalAlignmentDefault asserts the memarg field the round trip is
// structurally blind to, and the blindness is the decoder's own stated design.
//
// `decodeMemop` stages the offset and the memory index and **discards the alignment** — "alignment
// is a validation constraint and carries no execution semantics, so keeping it would be storing a
// fact only #9 reads". That is the right call for the decoder and it means `binary.Instr` has no
// field for the flags byte's low bits to disagree in: `i32.load align=1` and `i32.load align=4`
// decode to *identical* instructions. So the `encodableModules` rows cannot see this table, in
// precisely the way they cannot see the limits address-type bit, and this test is the second
// witness for the same reason `TestEncodeWritesTheAddressTypeFlagBit` is.
//
// **The table is what makes it worth a test rather than a comment.** 45 generated numbers reach the
// image through this one byte, and contract §9 G-3 is the whole argument for machine-reading them:
// `align=` is optional in the text, every value the field can hold is a *legal* alignment, and the
// validator rejects only over-alignment — so a mistyped default yields an image that decodes clean,
// runs, and denotes an access the source did not write. No `assert_malformed` inspects the byte.
// `make memarg-drift` asserts the table still agrees with lexer.mll; this asserts the emitter
// actually writes what the table says, which is a different claim.
//
// The rows are three partitions and each is load-bearing:
//
//   - **defaults across the exponent range 0..4**, taken from `naturalAlign` by hand rather than by
//     reading the map — a want column read out of the table under test is the tautology this file's
//     header forbids. 0, 1, 2, 3 and 4 all appear, so a writer that emitted a constant, or the
//     access width in bytes instead of its log2, fails somewhere.
//   - **an explicit `align=` differing from the default in both directions**, which is what
//     falsifies "the emitter ignores the text and always uses the table" — a defect every default
//     row above would pass.
//   - **one row with bit 6 set**, so the flags byte is asserted as a whole rather than as its low
//     three bits: `0x43` is align 3 for `i64.load` *or* to a reader that only masks 0x07 it is
//     indistinguishable from `0x03`.
func TestEncodeWritesTheNaturalAlignmentDefault(t *testing.T) {
	for _, tc := range []struct {
		src       string
		wantFlags byte
	}{
		// The defaults, one per exponent the table uses. Read from lexer.mll's `opt a N` arms as
		// memarg.go records them, then written here by hand: 8-bit accesses default to 0, 16-bit to
		// 1, i32/f32 to 2, i64/f64 to 3, v128 to 4.
		{`(module (memory 1) (func (drop (i32.load8_u (i32.const 0)))))`, 0x00},
		{`(module (memory 1) (func (drop (i32.load16_u (i32.const 0)))))`, 0x01},
		{`(module (memory 1) (func (drop (i32.load (i32.const 0)))))`, 0x02},
		{`(module (memory 1) (func (drop (i64.load (i32.const 0)))))`, 0x03},
		{`(module (memory 1) (func (drop (v128.load (i32.const 0)))))`, 0x04},
		// Stores, because the five above are all loads and the table's `STORE`-tagged arms are a
		// separate region of lexer.mll — five of its rows carry the upstream `LOAD` tag on a store
		// mnemonic (see memarg.go), so "loads work" is not evidence about stores.
		{`(module (memory 1) (func (i32.store8 (i32.const 0) (i32.const 1))))`, 0x00},
		{`(module (memory 1) (func (i32.store16 (i32.const 0) (i32.const 1))))`, 0x01},
		{`(module (memory 1) (func (i64.store (i32.const 0) (i64.const 1))))`, 0x03},
		// Explicit `align=`, below and above the mnemonic's default. Both directions, because an
		// emitter that took `min(written, natural)` or `max` would pass one of them.
		{`(module (memory 1) (func (drop (i32.load align=1 (i32.const 0)))))`, 0x00},
		{`(module (memory 1) (func (drop (i32.load align=8 (i32.const 0)))))`, 0x03},
		{`(module (memory 1) (func (drop (i64.load align=1 (i32.const 0)))))`, 0x00},
		// Bit 6 beside a non-zero alignment, so the assertion is on the whole byte. `i64.load`'s
		// default 3 or'd with 0x40.
		{`(module (memory 1) (memory 1) (func (drop (i64.load 1 (i32.const 0)))))`, 0x43},
		// **An explicit memory index `0`, and this row is the only witness the project has for
		// `has_idx`'s value-not-presence test.** `idx_opt` (parser.mly:492) returns `0l` for the
		// empty production, so a written literal `0` and an omitted index are the same AST and must
		// be the same bytes: no 0x40, no index field. The defect — reading `has_idx` as "the text
		// wrote one" — emits `28 42 00 00`, which is *self-consistently* one byte longer and
		// **round-trips identically**, `Imm0` and `Imm1` both 0. It was installed against the
		// `encodableModules` row for this source and passed; only the flags byte distinguishes it.
		// So the value test has exactly one instrument, this is it, and no `assert_malformed` can
		// express the question at all.
		{`(module (memory 1) (func (drop (i32.load 0 (i32.const 0)))))`, 0x02},
	} {
		t.Run(tc.src, func(t *testing.T) {
			b, err := EncodeModule([]byte(tc.src))
			if err != nil {
				t.Fatalf("EncodeModule refused a module the encoder is meant to write: %v", err)
			}
			// Located by walking the framing to the code section and then to the memarg, rather
			// than by searching for the opcode: an opcode byte occurs inside a LEB and inside a
			// valtype, which is the shortcut `bytesIndex`'s doc records as installed-and-passing.
			got, err := memargFlags(b)
			if err != nil {
				t.Fatalf("locating the memarg in % x: %v", b, err)
			}
			if got != tc.wantFlags {
				t.Errorf("the memarg flags byte is %#02x, want %#02x: bits 0-5 are the log2 "+
					"alignment (encode.ml:221) and bit 6 is an explicit memory index. Neither is "+
					"visible to a round trip, because decodeMemop discards the alignment by design "+
					"and no assert_malformed inspects it — a wrong value here is a legal image "+
					"denoting a different access width (§9 G-3)", got, tc.wantFlags)
			}
		})
	}
}

// memargFlags returns the flags byte of the memarg in a single-function module's body.
//
// It walks: code section, vector count, body size, local declarations, then the instructions,
// skipping each `i32.const`/`i64.const` operand by its LEB and stopping at the first
// memory-accessing opcode. Written as a walk rather than a search for the same reason
// `bytesIndex` is — `0x28` is a legal LEB byte and a legal valtype — and it refuses rather than
// guessing, so a module this helper cannot read is a loud failure instead of a byte from the
// wrong place.
//
// It handles exactly the shapes the rows above use. That is a deliberate narrowness with a
// notice attached: widening the table past one function, one non-const operand, or a
// multi-byte-LEB body size needs this helper widened first, and it will say so by erroring.
func memargFlags(b []byte) (byte, error) {
	i := bytesIndex(b, secCode)
	if i < 0 {
		return 0, errors.New("no code section")
	}
	_, n := uvarint(b[i+1:]) // section size
	if n <= 0 {
		return 0, errors.New("malformed code section size")
	}
	p := i + 1 + n
	count, n := uvarint(b[p:])
	if n <= 0 || count != 1 {
		return 0, errors.New("this helper reads a single-function module")
	}
	p += n
	if _, n = uvarint(b[p:]); n <= 0 { // body size
		return 0, errors.New("malformed body size")
	}
	p += n
	locals, n := uvarint(b[p:])
	if n <= 0 || locals != 0 {
		return 0, errors.New("this helper reads a body with no local declarations")
	}
	p += n
	for p < len(b) {
		switch op := b[p]; {
		case op == 0x41 || op == 0x42: // i32.const, i64.const — skip the operand
			if _, n = uvarint(b[p+1:]); n <= 0 {
				return 0, errors.New("malformed const operand")
			}
			p += 1 + n
		case op == 0x1a: // drop
			p++
		case op >= 0x28 && op <= 0x3e: // the MVP load/store region — memarg follows
			return b[p+1], nil
		case op == 0xfd: // the SIMD prefix: one LEB opcode, then the memarg
			if _, n = uvarint(b[p+1:]); n <= 0 {
				return 0, errors.New("malformed vector opcode")
			}
			return b[p+1+n], nil
		default:
			return 0, errors.New("unexpected opcode in a body this helper is meant to read")
		}
	}
	return 0, errors.New("no memory-accessing instruction in the body")
}

// bytesIndex finds a section's id byte in an encoded module by walking the framing.
//
// It walks rather than searching for the byte, because `05` occurs inside a LEB, a valtype, or a
// limits minimum, and a search would find one of those first. The shortcut was installed as a probe
// and **passed** — it is right on every row that has one section before the memory — so the
// `(table 5 funcref)` row above exists to kill it. That row is the whole reason this comment is a
// claim rather than an assertion: a helper whose general-case correctness nothing exercises is the
// stillborn-control shape one level down, in the instrument instead of in the engine (#108).
//
// `uvarint`'s multi-byte path is **not** exercised here: no section in any row reaches 128 bytes, so
// every size LEB is one byte and the continuation branch never runs. Stated rather than hidden, and
// the exactness is the point — this is a knowable zero, not an estimate. It stays unexercised because
// a 128-byte section would need ~40 memories to say nothing this file does not already say; the fact
// worth having is that the shortcut `size := b[i+1]` would pass identically today, so anyone widening
// these rows past that boundary is the one who needs the coverage, and this sentence is the notice.
func bytesIndex(b []byte, id byte) int {
	i := 8 // past magic and version
	for i < len(b) {
		secID := b[i]
		size, n := uvarint(b[i+1:])
		if n <= 0 {
			return -1
		}
		if secID == id {
			return i
		}
		i += 1 + n + int(size)
	}
	return -1
}

// uvarint reads a LEB128 the way the decoder's reader does, for the section walk above.
//
// Not `binary.Uvarint` from the standard library's `encoding/binary`: that package is not imported
// here and this file's `binary` is the engine's own. Ten bytes maximum, matching the u64 budget.
func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i := range min(len(b), 10) {
		v |= uint64(b[i]&0x7F) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

// TestEncodeDoesNotRerunTheDeferredPhase pins the defect the first draft of `encode` had, and it
// checks the state a re-run actually corrupts rather than the state the draft's comment claimed.
//
// **The first version of this control was stillborn, and installing the defect is what said so.**
// It asserted `len(typeCtx)` across `encode`, on the stated reasoning that a second `runDeferred`
// rebuilds the table from `typeDefs` alone and so drops the implicit types `inlineFuncType`
// interned. With `p.ctx.runDeferred()` put back at the top of `encode`, every row still passed —
// because the thunks re-intern the same signatures in the same order, so `typeCtx` comes back
// *identical*. The claim was plausible, the control agreed with it, and both were wrong.
//
// What a second run does corrupt is `types.count`, which `inlineFuncType` increments per interned
// type: 1→2 on `(module (func))`, 2→4 on two distinct inline signatures. So the count is asserted
// here, and it is the half with teeth. The table is asserted too — not because a re-run changes it
// today, but because its surviving a re-run is contingent on what the thunks happen to do, and a
// future thunk that appends unconditionally is exactly what this should catch.
//
// The per-row expectations come from the reference's `inline_functype` (parser.mly:222-235) rather
// than from this encoder: an inline signature distinct from every existing type appends a slot, and
// one structurally equal to an existing type reuses it. Both readings are needed, since a check
// that only ever appends and a correct one agree on every module with a single signature.
//
// The modules all carry a `(func …)` field, so `encode` refuses them — which is why this reads the
// *state* rather than the bytes. That is the honest thing to check today: the retention is what a
// re-run corrupts, and it is checkable now where the bytes are not.
func TestEncodeDoesNotRerunTheDeferredPhase(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want int
	}{
		{`(module (func))`, 1},                                       // one implicit: [] -> []
		{`(module (func (param i32)))`, 1},                           // one implicit: [i32] -> []
		{`(module (type (func)) (func))`, 1},                         // the inline signature reuses type 0
		{`(module (type (func)) (func (param i32)))`, 2},             // distinct: interned as type 1
		{`(module (type (func (param i32))) (func (param i32)))`, 1}, // equal: reused
		{`(module (func (param i32)) (func (param i64)))`, 2},        // two distinct implicits
		{`(module (func (param i32)) (func (param i32)))`, 1},        // the second reuses the first
	} {
		t.Run(tc.src, func(t *testing.T) {
			p, err := parseModule([]byte(tc.src), build)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := len(p.ctx.typeCtx); got != tc.want {
				t.Errorf("the resolved type space has %d entries, want %d: a second runDeferred "+
					"rebuilds typeCtx from typeDefs alone, dropping the implicit types "+
					"inlineFuncType appended", got, tc.want)
			}

			// And `encode` must not change the type space — table *or* count. The refusal is
			// expected here (every row has a func field), and it is irrelevant: the state must be
			// untouched on either path, because encode reads the phase's result and does not own it.
			//
			// `types.count` is the assertion that falsifies. It is what a second runDeferred
			// double-counts, and checking only the table is how the first version of this control
			// passed with the defect installed.
			beforeLen, beforeCount := len(p.ctx.typeCtx), p.ctx.types.count
			snapshot := append([]resolvedComp(nil), p.ctx.typeCtx...)

			_, _ = p.encode()

			if after := p.ctx.types.count; after != beforeCount {
				t.Errorf("encode() changed types.count from %d to %d: inlineFuncType increments it "+
					"per interned type, so a second runDeferred double-counts the implicit types — "+
					"the corruption a table-only check cannot see", beforeCount, after)
			}
			if after := len(p.ctx.typeCtx); after != beforeLen {
				t.Errorf("encode() changed the resolved type space from %d entries to %d: it must "+
					"read the table, never rebuild it", beforeLen, after)
			}
			for i := range snapshot {
				if i >= len(p.ctx.typeCtx) {
					break
				}
				got := p.ctx.typeCtx[i]
				if got.isFunc != snapshot[i].isFunc || !got.ft.equal(snapshot[i].ft) {
					t.Errorf("encode() changed type %d's content: the thunks' idempotence is "+
						"contingent on what they currently do, and this is what says when that "+
						"stops holding", i)
				}
			}
		})
	}
}

// TestEncodeRefusesWhatItCannotWrite is the frontier's control, and it checks the *direction* of
// the refusal as much as the fact of it.
//
// A frontier is not a malformedness: these modules are well-formed, `ReadModule` accepts every one
// of them, and the encoder's error must name the gap in the engine rather than a defect in the
// input. So each row asserts three things — ReadModule accepts, EncodeModule refuses, and the
// message says what is missing without borrowing a spec string.
func TestEncodeRefusesWhatItCannotWrite(t *testing.T) {
	for _, tc := range []struct{ src, contains string }{
		// `(module (func))` was here and is now in `encodableModules`, which is what the code section
		// *is*. The `(start 0)` row below still needs a func to be well-formed and is still refused —
		// by the start field, now, rather than by the func that precedes it.
		//
		// `(module (global i32 (i32.const 0)))` was here and is now in `encodableModules`, which is what
		// section 6 *is* — the fourth field to make that move, after `func`, `data` and `elem`.
		{`(module (start 0) (func))`, "(start …) field"},
		// `(module (data "abc"))` was here and is now in `encodableModules`, which is what section 11
		// *is* — the same move `(module (func))` made when the code section landed.
		{`(module (tag))`, "(tag …) field"},
		{`(module (type (struct)))`, "struct or array"},
		{`(module (type (array (mut i32))))`, "struct or array"},
		{`(module (type (func (param (ref func)))))`, "parameterized reference"},
		{`(module (type (func (param (ref extern)))))`, "parameterized reference"},
		{`(module (type $t (func)) (type (func (param (ref null $t)))))`, "parameterized reference"},
		{`(module (type (func (param anyref))))`, "parameterized reference"},

		// # The unencodable *arms* of memory and table (#8)
		//
		// **This is the half of the frontier the per-field version could not express.** `(memory 1)`
		// and `(table 1 funcref)` moved out of this table and into `encodableModules`, and what
		// remains is per-arm: each row below dispatches on a keyword the emitter *can* write and is
		// still refused, because its own arm retains something no section exists for. A frontier
		// keyed on the field kind would have accepted every one of these and emitted a module with
		// the sugar's data or elem segment silently dropped.
		//
		// **The inline-import rows moved to `encodableModules` in this PR, and the move is the point of
		// it.** They were here because an imported memory belongs to the import section and there was no
		// import section — the population split `defineMemory` names — so the only honest thing to do
		// with one was refuse it. Now both spellings retain, and the pairs in `encodableModules` assert
		// the two denote the same import. What stays here is every arm that retains something *no*
		// section exists for.
		// **The two memory-data-sugar rows moved to `encodableModules` in this PR**, and their departure
		// is section 11's. They were here because the arm defines a memory *sized from* a data segment
		// there was no section for, so emitting the memory alone would have written a page count with
		// nothing in it. Both spellings now retain both halves, and the pairs in `encodableModules`
		// assert the sugar and its longhand denote one module — which is the only instrument that can,
		// the sugar's size arithmetic and synthesized offset having no source token to be wrong about.
		//
		// **The seven inline-export rows moved to `encodableModules` in this PR**, and the move is what
		// the export section *is*. They were here because an inline export needed a section that did
		// not exist, so every field carrying one had to keep its refusal — which is what the
		// `exported` lookahead in each field bought, and what `inlineImportTail`'s deleted parameter
		// was for. Now the sugar retains, so nothing suppresses the withdrawal and the pairs in
		// `encodableModules` assert that the two spellings denote one module.
		//
		// The `(import …)` field has no inline-export arm at all (:1250), so there was never a row for
		// it — the asymmetry is the grammar's, and `importField`'s unconditional withdrawal is why it
		// never needed the lookahead the other five have now lost.
		// An initializer expr. This row read `"(func …) field"` until the code section landed, because
		// the func was refused before the table was reached — so what it *checked* was the func
		// frontier, and the table's own arm was never the thing under test. Now the func encodes and
		// the row says what its comment always claimed.
		{`(module (func $f) (table 1 funcref (ref.func $f)))`, "(table …) field"},
		// A table whose element type needs GC's prefix: refused by the *element type* check rather
		// than by the field check, so the message names the type rather than the field.
		{`(module (table 1 (ref func)))`, "element type (ref func)"},
		{`(module (table 1 anyref))`, "element type (ref null any)"},
		{`(module (type $t (func)) (table 1 (ref null $t)))`, "element type (ref null 0)"},
		// The same table with its type defined **after** it, which is what `defineTable`'s deferral is
		// for: `table_fields` runs in `module_fields1`'s second `fun () ->` (parser.mly:1341-1347), so
		// the element type may forward-reference. Both orders refuse today, so the *verdict* cannot see
		// the deferral — replacing it with eager resolution keeps every row green. What it changes is
		// the testimony: the message becomes `element type (ref )`, an unresolved index rendered as
		// nothing, which is the fabricated-evidence class (grave #36) — right refusal, invented type.
		// So this row pins the message, and it is the only instrument that can: the deferral's effect
		// on this module is entirely inside a string the suite never reads.
		{`(module (table 1 (ref null $t)) (type $t (func)))`, "element type (ref null 0)"},

		// # A global's *type* frontier, which needed an empty initializer to be reachable at all
		//
		// **These five rows exist because the check they exercise was live and untested, and the
		// falsification is what said so** — disabling `encodableOrErr`'s per-global `valTypeByte` loop
		// changed no verdict anywhere in this file. Not stillborn, which is the other failure mode: the
		// check fires on real input, as a probe confirmed. It was simply asserted by nothing, which is the
		// same board and the same blindness.
		//
		// **What the re-run mutation then established is that this frontier is not the last line, and
		// the discovery is a dividend of running the mutation rather than reasoning about it.** With the
		// loop disabled, `(module (global anyref))` fails here — but the other four *panic* in
		// `w.valType`, which carries a backstop reading "encodableOrErr and valType disagree about a
		// table they share". So the type check's job is not to prevent a wrong byte; the emitter already
		// refuses to write one. Its job is to turn a panic into a **frontier message with a field number
		// and a type in it**, which is a report a reader can act on. That reframes what these rows
		// assert, and it is why the two index rows are pinned on the *message* text: dropping `g.typ.val`
		// from the format string fails exactly those two and nothing else in the file — the verdict half
		// is held up by a panic the mutation would otherwise let pass for a pass.
		//
		// The reason is an ordering neither mechanism's code would suggest: a global has two unencodable
		// halves and the *initializer* is refused first, at the cursor, so `(global anyref (ref.null any))`
		// reports `the heap type any` and never reaches the type loop. Every spelling that exercises the
		// type check therefore has to omit the initializer — which `constexpr` being `instr_list` makes
		// well-formed — and that is why `(global anyref)` is a row here rather than a curiosity. The
		// general shape is worth the paragraph: **when a construct has two frontiers, the rows for the
		// later one must defeat the earlier one, and which is earlier is a fact about execution order that
		// only running it establishes.**
		//
		// Both mutability spellings, because `globaltype`'s arms are `valtype` and `(mut valtype)`
		// (parser.mly:1075-1077) and a check reached through one arm alone would pass the other.
		{`(module (global anyref))`, "needs a parameterized reference"},
		{`(module (global (mut anyref)))`, "needs a parameterized reference"},
		{`(module (global (ref func)))`, "needs a parameterized reference"},
		// A **type index** valtype, and the pair that makes `defineGlobal`'s deferral testable at all:
		// both orders refuse, so the verdict cannot see the deferral — replacing it with eager resolution
		// keeps both rows green. What it changes is the message, which becomes `type (ref null )` on the
		// second row: an unresolved index rendered as nothing, the fabricated-evidence class (grave #36).
		// Same argument as the table rows above, and the same reason it is the message that is pinned.
		{`(module (type $t (func)) (global (ref null $t)))`, "type (ref null 0) needs"},
		{`(module (global (ref null $t)) (type $t (func)))`, "type (ref null 0) needs"},

		// # An encodable field *after* an unencodable one — the withdrawal's identity check
		//
		// **These four rows exist because the check they falsify was unfalsifiable without them, and
		// the comment on `clearNonTypeField` had already claimed otherwise.** Removing that function's
		// `firstNonType.Offset == kw.Offset` comparison passes every other row in this file: each of
		// them puts the unencodable field first *and last*, so there is no later encodable field to
		// withdraw the wrong record. With the comparison gone, `(module (func) (memory 1))` encodes —
		// the memory arm withdraws the *func's* refusal — and emits a module whose function is
		// silently dropped. That is the accept-direction defect arriving through the mechanism built
		// to prevent it, and until these rows existed nothing could see it.
		//
		// Two orders and two kinds, because the defect is about *which* record gets cleared: the
		// unencodable field is the one whose name must appear in the message, never the encodable
		// field that follows it.
		//
		// **The leader was `(func)`, then `(data "abc")`, then `(elem)`, then `(global …)`, and is now
		// `(start 0)` — re-pointed four times for the same reason.** This is the tripwire-re-pointing
		// rule (#33) at test-row scale, and by the fourth application it is routine rather than
		// remarkable: the rows name a *risk* — a later field withdrawing an earlier field's refusal — and
		// each landing section dissolves whichever leader it implements without touching the risk. `func`
		// moved to follower when the code section landed; `data` when section 11 did; `elem` when section
		// 9 did; `global` now that section 6 does. Deleting the rows instead would have retired a live
		// control four times on a technicality, and the instruction the previous version of this comment
		// left — "re-point again, not delete" — is what is being carried out here.
		//
		// **`(start 0)` and `(tag)` are the only leaders left, and that is now worth stating as a
		// countdown rather than as an instruction.** Two fields remain unencodable; when the second of
		// them lands there will be *no* unencodable field to lead with, and this control's subject
		// dissolves with no re-pointing available. The risk does not: `clearNonTypeField`'s offset
		// comparison will still be the only thing standing between a later field and an earlier field's
		// record. So the re-pointing that will be required then is not to another field but to another
		// *frontier* — the per-arm rows above (`(table 1 (ref func))`, the typeuse rows below) are
		// refusals that outlive every section, and the leader becomes one of those. Named here because a
		// tripwire whose next re-pointing is not obvious is one that gets closed instead.
		{`(module (start 0) (memory 1) (func))`, "(start …) field"},
		{`(module (start 0) (table 1 funcref) (func))`, "(start …) field"},
		{`(module (tag) (memory 1) (table 1 funcref))`, "(tag …) field"},
		// **A `(data …)` field as the *follower*, which is where its departure makes it a better
		// witness than it was as a leader.** `dataField` has three arms and every one of them calls
		// `clearNonTypeField` after its closing paren, so it is the newest candidate for clearing a
		// record that is not its own — and the sugar arm clears one too. Falsified by dropping the
		// offset comparison in `clearNonTypeField`: these three encode, emitting a module whose start or
		// tag is gone.
		{`(module (start 0) (data "abc") (func))`, "(start …) field"},
		{`(module (tag) (memory 1) (data (i32.const 0) "x"))`, "(tag …) field"},
		{`(module (start 0) (memory (data "x")) (func))`, "(start …) field"},
		// **`(elem …)` as a follower, and it is the sharpest of them**, which is the dividend of its
		// promotion out of this table. Five arms, each calling `clearNonTypeField` after its own closing
		// paren — more distinct withdrawal sites than any other field has — plus the `(table … (elem …))`
		// sugar, whose arm withdraws once for two definitions. Every one of those is a place to clear a
		// record belonging to the `(start 0)` in front of it.
		{`(module (start 0) (elem func) (func))`, "(start …) field"},
		{`(module (start 0) (elem (i32.const 0)) (func))`, "(start …) field"},
		{`(module (start 0) (elem declare func) (func))`, "(start …) field"},
		{`(module (start 0) (table funcref (elem)) (func))`, "(start …) field"},
		// **`(global …)` as a follower, which is where its promotion out of this table makes it a
		// witness.** `globalField`'s defining arm is the newest `clearNonTypeField` caller in the file,
		// and it is the one whose withdrawal now runs on every well-formed global rather than never —
		// so the record it could clear wrongly is the `(start 0)`'s in front of it. Both spellings,
		// because the import arm withdraws through `importField`'s path and the defining arm through its
		// own tail, and they are different call sites.
		{`(module (start 0) (global i32 (i32.const 0)) (func))`, "(start …) field"},
		{`(module (start 0) (global (import "m" "g") i32) (func))`, "(start …) field"},
		// `func` as a follower, unchanged: `funcField`'s tail calls `noteDefined` and
		// `clearNonTypeField` after retaining its body, and unlike the memory and table arms it reaches
		// that call on every well-formed func.
		{`(module (start 0) (func))`, "(start …) field"},
		{`(module (tag) (func) (memory 1))`, "(tag …) field"},

		// # The typeuse frontier, which is a *wrong index* rather than a missing section
		//
		// Every row above refuses a field with no emitter. These two refuse a func the emitter can
		// otherwise write completely, because one immediate inside it would be **wrong**: a typeuse
		// with no re-stated signature contributes its referenced type's params as anonymous locals
		// (parser.mly:241-244), the count is unknowable at the cursor (the type may be defined later),
		// and `p.ctx.locals` is therefore short by it. `(local.get $var)` resolved to slot 0 where the
		// param owns 0 — 77 bytes agreeing with wabt everywhere except `20 00` against `20 01`, found
		// by the corpus and invisible to all 4162 vectors.
		//
		// So the assertion is the same in shape and different in kind: refusing here is not "no
		// section yet", it is "this index would be a lie". The tracking issue is #77's rather than
		// #8's, which is why the loop below accepts either.
		{
			`(module (type $sig (func (param i32))) (func (type $sig) (local $var i32) (local.get $var) drop))`,
			"typeuse supplies its params",
		},
		// The **over-refusal**, stated as a row rather than left in a comment. `$t` has no params, so
		// `$v` really is slot 0 and this module is encodable — and it is refused, because "does the
		// type have params" is the same unanswerable question at this cursor as "how many". A frontier
		// that declines an encodable module is a cost; one that writes a wrong index is a defect no
		// vector can see. If #77 lands and this row still refuses, the fix did not reach the predicate.
		{
			`(module (type $t (func)) (func (type $t) (local $v i32) (local.get $v) drop))`,
			"typeuse supplies its params",
		},
	} {
		t.Run(tc.src, func(t *testing.T) {
			if err := ReadModule([]byte(tc.src)); err != nil {
				t.Fatalf("the parser rejects this module, so it is the wrong vector for a frontier "+
					"test — a frontier is about well-formed input: %v", err)
			}
			b, err := EncodeModule([]byte(tc.src))
			if err == nil {
				t.Fatalf("EncodeModule produced % x for a module it cannot fully write: emitting a "+
					"module with a section silently dropped is an accept-direction defect no suite "+
					"vector can see (§9 G-3)", b)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("refusal says %q, want it to name %q", err, tc.contains)
			}
			// **Either tracking issue, not a substring that any `#` satisfies.** The section frontiers
			// are #8's; the typeuse rows are #77's, that gap being a wrong *index* rather than a
			// missing emitter. Spelled as two accepted numbers rather than as `strings.Contains(err,
			// "#")` — a predicate matching any hash would pass on a message citing #0 or on the word
			// "channel #", which is a citation nobody can resolve wearing a tracked deferral's clothes.
			if !strings.Contains(err.Error(), "#8") && !strings.Contains(err.Error(), "#77") {
				t.Errorf("refusal says %q, want it to cite #8 or #77: an unexplained gap is the "+
					"declared-and-tracked ruling's silent half (#6)", err)
			}
			for _, spec := range []string{"malformed", "unexpected", "unknown", "invalid"} {
				if strings.Contains(err.Error(), spec) {
					t.Errorf("refusal says %q, which contains the spec word %q: reporting a "+
						"malformedness for a module the spec calls well-formed lies about the "+
						"input to conceal a gap in the engine (#5)", err, spec)
				}
			}
		})
	}
}

// TestEncodeRejectsWhatTheParserRejects asserts the encoder adds no acceptance.
//
// `EncodeModule` runs the same front end as `ReadModule`, so a module the parser rejects must not
// come out the other side as bytes. The trailing-token row is the one that motivated sharing
// `parseModule`: an encoder that forgot the EOF check would read `(module) (module)` as one module
// and emit the first, accepting input the parser refuses.
func TestEncodeRejectsWhatTheParserRejects(t *testing.T) {
	for _, src := range []string{
		`(module) (module)`,
		`(module (type (func))) trailing`,
		`(module (type (func (param $x i32 i32))))`, // sugar allows exactly one type after a name
		`(module (type (func (param nosuchtype))))`,
		`(module (type (func (param (ref null $undefined)))))`,
		`(module (type $a (func)) (type $a (func)))`,
		`(module`,
	} {
		t.Run(src, func(t *testing.T) {
			readErr := ReadModule([]byte(src))
			if readErr == nil {
				t.Fatalf("the parser accepts this, so it is the wrong vector for this test")
			}
			b, err := EncodeModule([]byte(src))
			if err == nil {
				t.Fatalf("EncodeModule produced % x for input ReadModule rejects (%v): the encoder "+
					"must add no acceptance", b, readErr)
			}
			if err.Error() != readErr.Error() {
				t.Errorf("EncodeModule says %q where ReadModule says %q: the two share one front "+
					"end and must give one verdict", err, readErr)
			}
		})
	}
}

// TestEveryAbsoluteHeaptypeRoundTripsThroughTheKeywordTable holds `heapWat`'s premise.
//
// The general claim — that lower-casing a keyword *kind* yields its wat spelling — is **false**,
// and measurably so: over the generated table it holds for 96 of 173 kinds, and for `BINARY` it
// yields a literal that lexes to a *different* kind (`BIN`). A kind is a token class, not a
// spelling: `LOCAL_GET` is the class for `local.get`, and `VEC_BINARY` names no literal at all.
//
// `heapWat` is scoped to the twelve absolute heap types, where the derivation does hold, and this
// is what says so — by looking the spelling up in the generated keyword table and requiring it to
// lex back to the kind it came from. The domain is `absoluteHeaptypes` rather than a list retyped
// here, so a thirteenth heap type is covered the day it is added.
func TestEveryAbsoluteHeaptypeRoundTripsThroughTheKeywordTable(t *testing.T) {
	if len(absoluteHeaptypes) != 12 {
		t.Errorf("absoluteHeaptypes has %d entries, want the reference's 12 (parser.mly:361-372): "+
			"if a heap type was added, this control covers it automatically and the count is what "+
			"asks you to confirm the addition was intended", len(absoluteHeaptypes))
	}
	for _, k := range absoluteHeaptypes {
		spelling := heapWat(k)
		got, ok := keywords[spelling]
		if !ok {
			t.Errorf("heapWat(%q) is %q, which the generated keyword table does not contain: the "+
				"spelling would be quoted in a diagnostic for a token the user never wrote "+
				"(grave #36)", k, spelling)
			continue
		}
		if got != k {
			t.Errorf("heapWat(%q) is %q, which lexes to %q — a *wrong* spelling rather than a "+
				"missing one, which is the shape that survives review", k, spelling, got)
		}
	}
}

// TestResolvedValStringNamesTheTypeItDenotes pins the diagnostic renderer, which the frontier
// messages quote.
//
// It matters because a refusal naming the wrong type is a refusal that sends a reader to the wrong
// place, and nothing else checks it: the string appears only in an error the suite never reads —
// which per grave #36 is exactly where message text has to be *printed* rather than trusted.
//
// The nullability rows are the load-bearing ones: `funcref` and `(ref null func)` denote the same
// type and must render alike, while `(ref func)` is a different type and must not be confused with
// either.
func TestResolvedValStringNamesTheTypeItDenotes(t *testing.T) {
	for _, tc := range []struct {
		v    resolvedVal
		want string
	}{
		{resolvedVal{num: "i32"}, "i32"},
		{resolvedVal{num: "v128"}, "v128"},
		{resolvedVal{null: true, abs: kwFunc}, "(ref null func)"},
		{resolvedVal{abs: kwFunc}, "(ref func)"},
		{resolvedVal{null: true, abs: kwExtern}, "(ref null extern)"},
		{resolvedVal{null: true, abs: kwNoextern}, "(ref null noextern)"},
		{resolvedVal{abs: kwI31}, "(ref i31)"},
		{resolvedVal{isIdx: true, idx: 3}, "(ref 3)"},
		{resolvedVal{isIdx: true, null: true, idx: 3}, "(ref null 3)"},
	} {
		if got := tc.v.String(); got != tc.want {
			t.Errorf("resolvedVal%+v renders as %q, want %q", tc.v, got, tc.want)
		}
	}
}

// TestEveryAbbreviatedReftypeExpandsAsItsTableClaims checks the twelve-row pairing in
// abbreviatedReftypes (types.go) against the reader's *observable output*, which grave #112's
// method made possible: the pairing used to be prose beside a table that nothing consumed.
//
// The table asserts that `anyref` is `(Null, AnyHT)`, `nullfuncref` is `(Null, NoFuncHT)`, and so on
// for twelve arms of parser.mly:378-389 — every one nullable, differing only in the heap type. Until
// the encoder existed, none of that was checkable from outside: `reftype` returned a `valType` every
// caller discarded, so a swapped pairing (`structref` → `ArrayHT`) parsed identically and *no*
// spelling of a test could tell the difference. It is checkable now for exactly the reason #112's
// defect became visible — something consumes the value — which is the general method that grave
// records: **a reader that discards cannot be audited by any suite we have.**
//
// Two channels, because two things can be wrong and they fail in different places:
//   - the ten that the emitter refuses quote the resolved heap type in the refusal, so the *message*
//     carries the pairing (grave #36's print-don't-trust: this text is invisible to the suite);
//   - the two the emitter writes (`funcref`, `externref`) reach real bytes, so their pairing is
//     checked against the binary encoding rather than against our own rendering — 0x70 and 0x6F from
//     encode.ml, the one place in this test the authority is not a string we produced.
//
// Scoped to `len(abbreviatedReftypes)` by *iterating the table*, not by listing twelve cases: a
// thirteenth abbreviation added to the table with no expectation here fails the exhaustiveness check
// below rather than being silently unexercised. Derive the domain, never enumerate it.
func TestEveryAbbreviatedReftypeExpandsAsItsTableClaims(t *testing.T) {
	// The spelling and the type it must denote, read off parser.mly:378-389's semantic actions — not
	// off abbreviatedReftypes, which is the thing under test. `(Null, AnyHT)` renders as
	// `(ref null any)` per resolvedVal.String, pinned by the test above.
	wantDenotes := map[string]string{
		"anyref":        "(ref null any)",      // :378  (Null, AnyHT)
		"nullref":       "(ref null none)",     // :379  (Null, NoneHT)
		"eqref":         "(ref null eq)",       // :380  (Null, EqHT)
		"i31ref":        "(ref null i31)",      // :381  (Null, I31HT)
		"structref":     "(ref null struct)",   // :382  (Null, StructHT)
		"arrayref":      "(ref null array)",    // :383  (Null, ArrayHT)
		"funcref":       "(ref null func)",     // :384  (Null, FuncHT)
		"nullfuncref":   "(ref null nofunc)",   // :385  (Null, NoFuncHT)
		"exnref":        "(ref null exn)",      // :386  (Null, ExnHT)
		"nullexnref":    "(ref null noexn)",    // :387  (Null, NoExnHT)
		"externref":     "(ref null extern)",   // :388  (Null, ExternHT)
		"nullexternref": "(ref null noextern)", // :389  (Null, NoExternHT)
	}
	// The two the emitter can write, with the byte encode.ml gives each: `funcref` 0x70 and
	// `externref` 0x6F (encode.ml's reftype cases). These are the rows whose pairing is judged
	// against the binary format rather than against our own String.
	wantByte := map[string]byte{"funcref": 0x70, "externref": 0x6F}

	// Vacuity floor: every assertion below is inside a loop over the table, so an emptied or
	// truncated table would pass them all by iterating nothing.
	if len(abbreviatedReftypes) != len(wantDenotes) {
		t.Fatalf("abbreviatedReftypes has %d entries, expectations cover %d — parser.mly:378-389 is "+
			"twelve arms; a mismatch means the table gained or lost an abbreviation and this "+
			"control was not updated with it", len(abbreviatedReftypes), len(wantDenotes))
	}

	seen := map[string]bool{}
	for _, a := range abbreviatedReftypes {
		// The spelling is recovered from keywords.go rather than typed here, so this control cannot
		// disagree with the generated table about what lexes to the kind: `a.kw` is a *kind*
		// (`ANYREF`), and the source text is the keyword that produces it.
		var spelling string
		for kw, kind := range keywords {
			if kind == a.kw {
				spelling = kw
				break
			}
		}
		if spelling == "" {
			t.Errorf("no keyword in keywords.go lexes to %s — the table names a kind the lexer "+
				"cannot produce, so the arm is unreachable", a.kw)
			continue
		}
		want, ok := wantDenotes[spelling]
		if !ok {
			t.Errorf("abbreviatedReftypes has %s (%s) with no expectation here — a new arm must "+
				"cite its parser.mly line and the type it denotes", a.kw, spelling)
			continue
		}
		seen[spelling] = true

		b, err := EncodeModule([]byte("(module (table 1 " + spelling + "))"))
		if wb, encodable := wantByte[spelling]; encodable {
			if err != nil {
				t.Errorf("EncodeModule(table 1 %s) = %v; this element type is encodable today",
					spelling, err)
				continue
			}
			// Section 4 is the table section; its payload is count, reftype, then limits.
			i := bytesIndex(b, 4)
			if i < 0 {
				t.Errorf("no table section in the encoding of `(table 1 %s)`", spelling)
				continue
			}
			// i, size, count, then the reftype byte.
			if got := b[i+3]; got != wb {
				t.Errorf("`(table 1 %s)` encodes element type %#02x, want %#02x — the pairing "+
					"reaches the binary format here, and this byte is encode.ml's, not ours",
					spelling, got, wb)
			}
			continue
		}
		// The refused ten: the refusal quotes the resolved type, which is where the pairing shows.
		if err == nil {
			t.Errorf("EncodeModule(table 1 %s) succeeded; only funcref and externref have an "+
				"unparameterized encoding today", spelling)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("EncodeModule(table 1 %s) = %q, want it to name %q — a wrong pairing in "+
				"abbreviatedReftypes shows up exactly here, in a message no vector reads",
				spelling, err, want)
		}
	}
	if len(seen) != len(wantDenotes) {
		t.Errorf("exercised %d of %d abbreviations; the loop is over the table, so a spelling "+
			"missing from it is an arm nothing tested", len(seen), len(wantDenotes))
	}
}

// TestRefNullEncodesItsHeapType pins the one byte `wantGlobals` structurally cannot see.
//
// **The gap is a retention gap, not a test-style preference.** `immHeapType` validates the heaptype
// and stages no immediate word (`instr.go:831`), so `ref.null func` and `ref.null extern` decode to
// the *identical* two instructions — `{Op: 0xd0}, {Op: 0x0b}` — and every round-trip row in
// `encodableModules` therefore agrees with an encoder that wrote the wrong heaptype byte, or the same
// byte for both. Filed under 0016; the encoder is not waiting on it, because the byte is checkable
// directly. This is the byte column's own justification applied one field lower down: assert bytes
// exactly where decoding discards a fact, and nowhere else.
//
// **The refusal half is gone and the exhaustive half replaced it (#8).** It used to assert that ten
// of the twelve absolute forms and the index form were *declined*, which was the frontier while
// `heapTypeByte` answered `func` and `extern`; `heapTypeBytes` writes the whole production, so those
// rows now name a boundary that does not exist. Kept as a note rather than deleted, because the reason
// the refusal was asserted is still live one level down: the twelve bytes differ by one nibble and
// nothing in the corpus can see a wrong one, which is what `TestAbsoluteHeaptypeBytesAgreeWithEncodeML`
// is for. The `heapString`-versus-`String` distinction it also pinned has moved to
// `TestRefNullNamesAnUnencodableHeapTypeAsAHeapType`, which reaches the arm through the table.
//
// `encode.ml:414` is the authority for the immediate being a bare `heaptype`:
// `RefNull t -> op 0xd0; heaptype t`.
func TestRefNullEncodesItsHeapType(t *testing.T) {
	// **Every heaptype the production admits, derived from the parser's own list** rather than from a
	// second enumeration here — so a thirteenth form is a red board on the day it is added, which is
	// `heapTypeBytes`' stated contract. The expected byte comes from `absoluteHeaptypeBytes`, which is
	// the engine's table and would agree with itself; what makes that legitimate is that the *table*
	// is checked against encode.ml by TestAbsoluteHeaptypeBytesAgreeWithEncodeML. This row asserts the
	// byte reaches the image, not what the byte is — two questions, two controls, neither standing in
	// for the other.
	//
	// A `funcref` global for `func` and `externref` for the rest is the one column that cannot be
	// derived: `(global anyref …)` needs a *reftype* the encoder still declines (`valTypeByte`), so
	// the global's declared type is deliberately mismatched with its initializer for ten of the
	// twelve. Ill-typed, and irrelevant that it is — the encoder writes what the grammar admits and
	// validation is #9's, exactly as `encodableModules`' two-`ref.func` element row already reasons.
	for _, k := range absoluteHeaptypes {
		want, ok := absoluteHeaptypeBytes[k]
		if !ok {
			t.Errorf("no byte for the absolute heap type %q; every member of absoluteHeaptypes needs "+
				"one — see TestEveryAbsoluteHeaptypeHasAByte, which is the control for this", k)
			continue
		}
		valty := "externref"
		if k == kwFunc {
			valty = "funcref"
		}
		src := fmt.Sprintf(`(module (global %s (ref.null %s)))`, valty, heapWat(k))
		got, err := globalInitBytes(EncodeModule([]byte(src)))
		if err != nil {
			t.Errorf("EncodeModule(%s): %v", src, err)
			continue
		}
		if wantInit := []byte{0xd0, want, 0x0b}; !slices.Equal(got, wantInit) {
			t.Errorf("%s encodes its initializer as % x, want % x — the heap type byte is invisible "+
				"to every structural column in encodableModules, because the decoder retains no "+
				"immediate for it", src, got, wantInit)
		}
	}

	// The **index** form, which is `heaptype`'s first branch and a `typeuse s33` rather than a byte
	// (encode.ml:133). Two indices, and the second is the load-bearing one: 64 is where s33 and u32
	// diverge, so a `u32` writer passes index 0 and writes one byte where two belong. The module
	// declares 65 types to make index 64 legal, which is the same construction
	// TestBlockTypeFormsMatchTheReference uses for the blocktype half of this production.
	for _, tc := range []struct {
		types    int
		idx      uint32
		wantInit []byte
	}{
		{1, 0, []byte{0xd0, 0x00, 0x0b}},
		{65, 64, []byte{0xd0, 0xc0, 0x00, 0x0b}},
	} {
		var src strings.Builder
		src.WriteString("(module")
		for range tc.types {
			src.WriteString(" (type (func))")
		}
		fmt.Fprintf(&src, " (global funcref (ref.null %d)))", tc.idx)
		got, err := globalInitBytes(EncodeModule([]byte(src.String())))
		if err != nil {
			t.Errorf("EncodeModule(… (ref.null %d) …): %v", tc.idx, err)
			continue
		}
		if !slices.Equal(got, tc.wantInit) {
			t.Errorf("(ref.null %d) encodes as % x, want % x — the index form is `typeuse s33`, and "+
				"a u32 writer agrees with it on every index below 64", tc.idx, got, tc.wantInit)
		}
	}
}

// globalInitBytes extracts a single-global module's initializer bytes from an encoding.
//
// Takes `EncodeModule`'s pair so a caller reads as one line; the error is returned rather than
// asserted here, because "the encode failed" and "the section is missing" are different findings and
// the caller's message names which module it was asking about.
func globalInitBytes(b []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	i := bytesIndex(b, secGlobal)
	if i < 0 {
		return nil, errors.New("no global section")
	}
	// i, size, count, valtype, mutability, then the initializer through the section's end.
	size, n := uvarint(b[i+1:])
	if n <= 0 || int(size) < 4 {
		return nil, fmt.Errorf("malformed global section size %d", size)
	}
	return b[i+1+n+3 : i+1+n+int(size)], nil
}

// heaptypeArmRE matches one arm of encode.ml's `heaptype` match, capturing the constructor and the
// signed form it writes: `| AnyHT -> s7 (-0x12)`.
//
// Written to the head only, per keywordgen's wrapped-arm lesson (grave #105) — and the
// `UseHT ut -> typeuse s33 ut` arm is deliberately *not* matched, because it writes no `s7` at all:
// an arm outside the set being compared should be absent rather than matched with an empty capture.
// The count check below is what turns its absence into a stated 12-of-13 rather than a silent one.
var heaptypeArmRE = regexp.MustCompile(`(?m)^\s*\|\s*(\w+)\s*->\s*s7\s*\(-(0x[0-9a-fA-F]+)\)`)

// TestAbsoluteHeaptypeBytesAgreeWithEncodeML machine-checks `absoluteHeaptypeBytes` against the
// authority, which is the only oracle this table has.
//
// **The corpus cannot see a wrong byte in it.** A heaptype byte reaches the image as `ref.null`'s
// immediate, so a swapped pair — `any` for `eq`, one nibble apart — writes a *well-formed* module
// denoting a different type: no `assert_malformed` fires, and every `assert_return` that would notice
// is behind the GC gate this build leaves off. That is §9 G-3's accept direction in its cheapest form,
// and the standing rule is that such a fact is machine-checked against the reference rather than
// hand-trusted (`authority-for-accept-direction-facts`). `TestExternKindByteAgreesForBothSections` is
// the shape copied rather than re-derived (grave #105).
//
// Three assertions, three questions:
//
//   - the extraction found **twelve** arms, which is the vacuity floor — two empty sets agree
//     perfectly, and a moved `let heaptype` or a changed indentation is exactly how that arrives
//     (grave #78's class, and the sibling of the empty-table finding in #106);
//   - every extracted arm is in the engine's table with the same byte, which is the engine's half;
//   - the engine's table has no arm the reference does not, which is the direction a one-sided
//     comparison misses: an invented thirteenth entry would pass the loop above.
//
// The conversion is `-form & 0x7f`, which is `s7`'s own definition (`i land 0x7f` for
// `-64 <= i < 64`, encode.ml:59-61) rather than a re-derivation — all twelve forms are in that range,
// and the test asserts it instead of assuming it, since a form outside it encodes to two bytes and
// this table holds one.
func TestAbsoluteHeaptypeBytesAgreeWithEncodeML(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefEncodeML)

	i := strings.Index(src, "let heaptype = function")
	if i < 0 {
		t.Fatalf("could not locate `let heaptype = function` in encode.ml; it is cited by " +
			"absoluteHeaptypeBytes and heaptypeRetained, and a citation that no longer resolves is " +
			"this drift")
	}
	// Bounded at the next top-level `let`, which is where every arm list in this file ends. `reftype`
	// follows immediately and writes the **same twelve bytes** from differently-named arms, so a
	// region running past the bound is reading two productions while claiming to read one.
	//
	// The sentinel below is what makes that claim checkable, and it is *not* redundant with the count:
	// removing the bound leaves the count at exactly 12 and the test green, because `reftype` spells
	// its arms as tuples (`| (Null, AnyHT) ->`) which `(\w+)` cannot match. Measured, by removing the
	// bound and running it. That is a coincidence of the reference's spelling rather than a property —
	// a future `| AnyRT -> s7 (-0x12)` in some third production would match — so the region asserts
	// its own extent instead of leaning on the arm pattern to be selective. A count floor answers
	// "did the extraction happen"; only this answers "did it read the right arms".
	rest := src[i:]
	if j := strings.Index(rest[1:], "\n  let "); j >= 0 {
		rest = rest[:j+1]
	}
	if strings.Contains(rest, "(Null, AnyHT)") {
		t.Fatalf("the heaptype region extends into `reftype`; the two write the same twelve bytes " +
			"from different constructors, so the extracted arms are not the production this table " +
			"claims to follow")
	}

	found := map[string]byte{}
	for _, m := range heaptypeArmRE.FindAllStringSubmatch(rest, -1) {
		ctor, hex := m[1], m[2]
		var form int
		if _, err := fmt.Sscanf(hex, "0x%x", &form); err != nil {
			t.Errorf("could not parse the form %q in heaptype's %s arm: %v", hex, ctor, err)
			continue
		}
		if form >= 64 {
			t.Errorf("heaptype's %s arm writes s7 (-%#x), which is outside s7's single-byte range; "+
				"absoluteHeaptypeBytes holds one byte per form and cannot represent it", ctor, form)
			continue
		}
		found[ctor] = byte(-form & 0x7f)
	}
	if len(found) != 12 {
		t.Fatalf("extracted %d heaptype arms from encode.ml, want 12 (the thirteenth, UseHT, writes "+
			"a typeuse rather than an s7 and is deliberately unmatched); an empty or short "+
			"extraction agrees with anything, so this is a broken reader rather than a finding: %v",
			len(found), found)
	}

	// The reference's constructor names against the parser's kinds. Spelled out because the two
	// vocabularies genuinely differ — `NoFuncHT` is `nofunc`, `NoneHT` is `none` — and a derivation
	// (strip `HT`, lower-case) would be a *third* claim about naming with nothing checking it, which
	// is `heapWat`'s own measured lesson: lower-casing a name is right often enough to be trusted and
	// wrong often enough to be a defect.
	kindOf := map[string]keywordKind{
		"AnyHT": kwAny, "EqHT": kwEq, "I31HT": kwI31, "StructHT": kwStruct, "ArrayHT": kwArray,
		"NoneHT": kwNone, "FuncHT": kwFunc, "NoFuncHT": kwNofunc, "ExnHT": kwExn,
		"NoExnHT": kwNoexn, "ExternHT": kwExtern, "NoExternHT": kwNoextern,
	}
	matched := map[keywordKind]bool{}
	for ctor, want := range found {
		k, ok := kindOf[ctor]
		if !ok {
			t.Errorf("encode.ml's heaptype has a %s arm this test cannot name a kind for; a "+
				"thirteenth heap type needs absoluteHeaptypeBytes, absoluteHeaptypes and this map "+
				"widened together", ctor)
			continue
		}
		got, ok := absoluteHeaptypeBytes[k]
		if !ok {
			t.Errorf("encode.ml writes %s as %#02x and absoluteHeaptypeBytes has no entry for %q",
				ctor, want, k)
			continue
		}
		if got != want {
			t.Errorf("absoluteHeaptypeBytes[%q] = %#02x, encode.ml's %s arm writes %#02x — a wrong "+
				"byte here emits a well-formed module denoting a different heap type, which no "+
				"assert_malformed in the corpus can report", k, got, ctor, want)
		}
		matched[k] = true
	}
	// The other direction: an entry the reference has no arm for. Without this the comparison is
	// one-sided, and an invented thirteenth row would sit in the table unremarked.
	for k := range absoluteHeaptypeBytes {
		if !matched[k] {
			t.Errorf("absoluteHeaptypeBytes has an entry for %q that no encode.ml heaptype arm "+
				"matched; the table claims a form the reference does not encode", k)
		}
	}
}

// TestEveryAbsoluteHeaptypeHasAByte is the control `heapTypeBytes`' remaining `false` cites.
//
// A thirteenth heap type added to `absoluteHeaptypes` (types.go) without a byte in
// `absoluteHeaptypeBytes` would make `(ref.null <it>)` a *refused module* — a frontier report for a
// form the encoder simply forgot, which reads as a deferral and is an omission. So the two tables are
// held together the way `atHeaptypeStart` and `heaptype` are: over the parser's own list, never over a
// fresh enumeration, so coverage grows with the thing controlled (*derive the domain, never enumerate
// it*).
//
// The floor is the vacuity check: a `range` over an empty list asserts nothing, and this file's other
// twelve-row controls read the same list.
func TestEveryAbsoluteHeaptypeHasAByte(t *testing.T) {
	if len(absoluteHeaptypes) != 12 {
		t.Fatalf("absoluteHeaptypes holds %d kinds, want 12; a range over a short list is a control "+
			"that agrees by asking less", len(absoluteHeaptypes))
	}
	for _, k := range absoluteHeaptypes {
		if _, ok := absoluteHeaptypeBytes[k]; !ok {
			t.Errorf("absoluteHeaptypes holds %q and absoluteHeaptypeBytes has no byte for it, so "+
				"(ref.null %s) is refused as unencodable rather than emitted — an omission wearing "+
				"a frontier's clothes", k, heapWat(k))
		}
	}
}

// TestRefNullNamesAnUnencodableHeapTypeAsAHeapType keeps grave #36's rendering lesson reachable now
// that no *spelling* of a heap type is refused.
//
// The old refusal rows in TestRefNullEncodesItsHeapType were the only thing exercising
// `heapString`-versus-`String`, and they are gone with the frontier. The arm they covered survives —
// `heaptypeBytesOf` reports it for a kind with no byte — so the message is checked by reaching that
// arm the only way left: through the table, with an entry removed. That is the falsification method
// used as a *control*, and it is legitimate here for the same reason the birth requirement is: the arm
// is unreachable from any source text, so a test that could not construct the state would be
// asserting a message nothing produces.
//
// What is asserted is the *level*: `func` and not `(ref null func)`. A heaptype has no nullability
// field, so reftype spelling in this position asserts a fact the grammar never supplied — the right
// verdict quoting evidence the input did not contain, in the half of an error no expected string
// covers.
func TestRefNullNamesAnUnencodableHeapTypeAsAHeapType(t *testing.T) {
	saved, ok := absoluteHeaptypeBytes[kwI31]
	if !ok {
		t.Fatal("absoluteHeaptypeBytes has no i31 entry to remove; this test constructs the " +
			"no-byte state by deleting one, and TestEveryAbsoluteHeaptypeHasAByte is the control " +
			"that would already be red")
	}
	delete(absoluteHeaptypeBytes, kwI31)
	defer func() { absoluteHeaptypeBytes[kwI31] = saved }()

	const src = `(module (global externref (ref.null i31)))`
	_, err := EncodeModule([]byte(src))
	if err == nil {
		t.Fatalf("EncodeModule(%s) succeeded with i31 removed from absoluteHeaptypeBytes; the "+
			"no-byte arm in heaptypeBytesOf is not reached, so this test asserts nothing", src)
	}
	if want := "the heap type i31"; !strings.Contains(err.Error(), want) {
		t.Errorf("EncodeModule(%s) = %q, want it to name %q — a heap type rendered in reftype "+
			"spelling (`(ref null i31)`) claims a nullability the grammar has no field for "+
			"(grave #36)", src, err, want)
	}
}

// TestRefNullHeaptypeResolvesInBothFieldOrders is the derived vector for the deferral
// `heaptypeRetained` grew, and it is derived because the corpus cannot state it.
//
// # Provenance: derived
//
// Premises, both checkable:
//
//   - `heaptype`'s index arm is `UseHT (Idx ($1 c type_).it)` inside a `fun c ->`
//     (parser.mly:373), so it resolves in the enclosing thunk — the reference's stage 2, after every
//     name is bound. `imports.wast:62-64` is the *cited* corpus for that staging in another position
//     (a func's typeuse forward-referencing its type), which typetable.go's header quotes.
//   - `module_fields` admits any field order in the source (parser.mly:1309), so a `(type $t …)`
//     after a use of `$t` is well-formed text.
//
// Inference: `(module (func (drop (ref.null $t))) (type $t (func)))` is a valid module, and an
// encoder resolving `$t` at the cursor rejects it. **Measured** at the commit before this one:
// `unknown type $t`.
//
// The corpus cannot supply this row — a sweep of all 254 vendored `.wast` files finds zero
// `ref.null $t` preceding its `(type $t …)` — which is grave #130's shape exactly: an
// accept-direction defect in field ordering, invisible because every vector happens to declare
// before it uses. Both orders are asserted, and they must produce the **identical** image: the two
// spellings denote the same module, so a difference is a defect whichever direction it points.
func TestRefNullHeaptypeResolvesInBothFieldOrders(t *testing.T) {
	const (
		declFirst = `(module (type $t (func)) (func (drop (ref.null $t))))`
		useFirst  = `(module (func (drop (ref.null $t))) (type $t (func)))`
	)
	a, err := EncodeModule([]byte(declFirst))
	if err != nil {
		t.Fatalf("EncodeModule(%s) = %v; the declare-first order was already encodable", declFirst, err)
	}
	b, err := EncodeModule([]byte(useFirst))
	if err != nil {
		t.Fatalf("EncodeModule(%s) = %v — a valid module rejected: `module_fields` admits any field "+
			"order, and the type space permits forward references by construction, which is why "+
			"heaptype returns its token unresolved (grave #130's class)", useFirst, err)
	}
	if !slices.Equal(a, b) {
		t.Errorf("the two field orders encode differently:\n\tdecl-first % x\n\tuse-first  % x\n"+
			"they denote the same module, so any difference is a defect whichever way it points", a, b)
	}
	// A numeric index takes the cursor path rather than the deferred one, so both arms of
	// `heaptypeRetained`'s split are exercised — and it must agree with the symbolic spelling, the
	// two being the same index after resolution.
	n, err := EncodeModule([]byte(`(module (type (func)) (func (drop (ref.null 0))))`))
	if err != nil {
		t.Fatalf("EncodeModule with a numeric heaptype index = %v", err)
	}
	if !slices.Equal(a, n) {
		t.Errorf("the symbolic and numeric spellings of type index 0 encode differently:\n"+
			"\t$t % x\n\t0  % x\nthe deferred arm and the cursor arm must produce one encoding", a, n)
	}
}

// externArmRE matches one arm of an encode.ml `extern*` match, capturing the constructor and the
// byte it writes: `| ExternFuncT ut -> byte 0x00; …` and `| FuncX x -> byte 0x00; idx x`.
//
// Anchored on `byte 0x` rather than on the arrow alone, because an arm that writes something other
// than a literal kind byte is not a member of the set being compared and should be *absent* rather
// than matched with an empty capture. Written to the head only, per keywordgen's wrapped-arm lesson
// (grave #105): the arms here are single-line at this revision and the regexp says so by requiring
// both halves on one line, so a future wrapped arm is a missing row the count check reports rather
// than a silently truncated one.
var externArmRE = regexp.MustCompile(`(?m)^\s*\|\s*(\w+)\s+\w+\s*->\s*byte\s+(0x[0-9a-fA-F]{2})`)

// TestExternKindByteAgreesForBothSections holds the derivation `textExport` and `encodeExports`
// are built on: that an export's kind byte and an import's are **one fact**.
//
// Reusing `externKindByte` across the import and export sections is only sound while encode.ml's
// two arm lists assign the same five bytes in the same order — `externtype` (:201-208) for the
// import section's descriptor, `externidx` (:1001-1007) for the export section's target. That is
// true at this revision and it is not a law: they are separate `match` expressions in separate
// parts of the file, and nothing upstream forces them to move together. So this is the tripwire the
// shared type is licensed by, and grave #105's lesson is the reason it exists rather than a second
// hand-copied table — a same-shaped fact next door is a place to read, not a place to invent.
//
// Three assertions, and they are three different questions:
//
//   - the two arm lists agree with each other, which is the *reuse's* premise;
//   - `externKindByte` agrees with them, which is the engine's half;
//   - both lists have five arms, which is the vacuity floor — two empty extractions agree
//     perfectly, and a moved `match` or a changed indentation is exactly how that arrives.
//
// The comparison is by *byte per constructor* rather than by position, because a positional check
// on two lists that were reordered together would pass while every byte moved.
func TestExternKindByteAgreesForBothSections(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefEncodeML)

	// `externtype`'s arms are `ExternFuncT` etc.; `externidx`'s are `FuncX` etc. Both are keyed
	// here by the *space* they name, which is the only vocabulary the two share.
	typeArms := externKindBytes(t, src, "let externtype = function", "ExternFuncT", "T")
	idxArms := externKindBytes(t, src, "let externidx xx =", "FuncX", "X")

	if len(typeArms) != 5 || len(idxArms) != 5 {
		t.Fatalf("extracted %d externtype arms and %d externidx arms, want 5 and 5; an empty or "+
			"short extraction agrees with anything, so this is a broken reader (the match heads, "+
			"or externArmRE) rather than a finding.\n\ttypes=%v idxs=%v",
			len(typeArms), len(idxArms), typeArms, idxArms)
	}

	// The reuse's premise, asserted directly. If this fails, `textExport.kind` may no longer be an
	// `importKind` and `encodeExports` needs its own mapping — read the failure as a design
	// question, not as a wrong constant.
	for space, tb := range typeArms {
		ib, ok := idxArms[space]
		if !ok {
			t.Errorf("encode.ml's externtype has a %s arm and externidx does not; the two grammars "+
				"no longer name the same five spaces, so externKindByte cannot serve both sections",
				space)
			continue
		}
		if tb != ib {
			t.Errorf("encode.ml writes %s as %#02x in externtype (import section) and %#02x in "+
				"externidx (export section); they have diverged upstream, and every export of a %s "+
				"is now written with the import section's byte", space, tb, ib, space)
		}
	}

	// The engine's half. The domain is every `importKind` value, derived from the type's extent
	// rather than listed, so a sixth kind is covered the day it is added — and `importFunc` being
	// last is what makes the extent knowable (context.go's defCount array depends on it too).
	wantSpace := map[importKind]string{
		importFunc: "Func", importTable: "Table", importMemory: "Memory",
		importGlobal: "Global", importTag: "Tag",
	}
	for k := importTag; k <= importFunc; k++ {
		space, ok := wantSpace[k]
		if !ok {
			t.Errorf("importKind %d has no expected encode.ml constructor here; a new kind must "+
				"name the arm it maps to", int(k))
			continue
		}
		want, ok := idxArms[space]
		if !ok {
			t.Errorf("encode.ml's externidx has no %s arm, but externKindByte maps a kind to it", space)
			continue
		}
		if got := externKindByte(k); got != want {
			t.Errorf("externKindByte(%s) = %#02x, want %#02x (encode.ml's %sX arm) — the mapping is "+
				"the guard against a kind byte that points the decoder at the wrong payload "+
				"grammar, and every byte it can write is a *legal* one", k, got, want, space)
		}
	}
}

// externKindBytes extracts one `extern*` match's arms, keyed by space name.
//
// `head` bounds the search at the match's own text and `sample` is a constructor that must appear
// inside those bounds — a presence check on the *region*, not just on the file, because an
// unbounded reader finds the other match's arms and the comparison this feeds becomes a tautology
// (grave #106: a premise measured over the same sample the code reads is an echo). `suffix` is the
// constructor's grammar-specific tail (`T` for externtype, `X` for externidx), stripped so the two
// lists share a key vocabulary.
func externKindBytes(tb testing.TB, src, head, sample, suffix string) map[string]byte {
	tb.Helper()

	i := strings.Index(src, head)
	if i < 0 {
		tb.Fatalf("could not locate %q in encode.ml; it is cited by encodeExports and "+
			"externKindByte, and a citation that no longer resolves is this drift", head)
		return nil
	}
	// Bounded at the next top-level `let`, which is where every arm list in this file ends.
	rest := src[i+len(head):]
	if j := strings.Index(rest, "\n  let "); j >= 0 {
		rest = rest[:j]
	}
	if !strings.Contains(rest, sample) {
		tb.Fatalf("the region after %q does not mention %q; the match bound is wrong, and a "+
			"mis-bounded region reads the *other* grammar's arms while claiming to read this "+
			"one", head, sample)
		return nil
	}

	out := map[string]byte{}
	for _, m := range externArmRE.FindAllStringSubmatch(rest, -1) {
		ctor, hex := m[1], m[2]
		space := strings.TrimSuffix(strings.TrimPrefix(ctor, "Extern"), suffix)
		var b byte
		if _, err := fmt.Sscanf(hex, "0x%02x", &b); err != nil {
			tb.Errorf("could not parse the kind byte %q in %q's %s arm: %v", hex, head, ctor, err)
			continue
		}
		if prev, dup := out[space]; dup {
			tb.Errorf("%q has two arms for %s (%#02x and %#02x); the key is not unique and the "+
				"comparison this feeds is meaningless", head, space, prev, b)
		}
		out[space] = b
	}
	return out
}

// TestEncodeMatchesTheReferenceOnElemFlags is section 9's cross-product control, and it is the
// instrument grave #146 was found by: the eleven-row arm table in `encodableModules` asserts whole
// payloads for eleven *modules*, this asserts the **flag** for every cell of mode × reftype ×
// element shape, so an arm that is right for the spellings that table happens to write and wrong for
// a cell it does not is caught here.
//
// # Why the flag alone, and why that is not a weaker assertion
//
// The flag is the whole classification: `elem` (encode.ml:1062-1084) is one `if` over two families
// of four, and every field after the flag follows from which arm was taken. So the flag is where the
// derivation is decided, and it is also the one field the *round trip cannot see* — `ElemSegment`
// retains `{Mode, TableIndex, ByExpr, ElemType, …}`, from which the flag is not recoverable:
// **flags 0 and 2-at-table-0 decode to identical segments, as do 4 and 6-with-funcref-at-table-0**
// (measured, not reasoned — a probe decoding all eight arms printed two pairs of identical structs).
// An encoder that wrote 0x02 with an explicit `00` table index where the reference writes 0x00
// round-trips clean and denotes the same module in *more bytes than the reference emits*, which is
// exactly the kind of divergence #67's corpus comparator would report as a finding.
//
// Asserting bytes past the flag would duplicate `encodableModules` while adding a want column per
// cell — 140 hand-written payloads, which is where a table stops being a second reading and starts
// being a transcription. The eleven rows there carry the payloads; this carries the classification.
//
// # The grid is the two predicates crossed with the mode, and every axis is a predicate's domain
//
// `is_elem_kind rt` (:1044) asks about the reftype's **nullability** and `for_all is_elem_index cs`
// (:1052, folded at :1064) asks about every element's **shape** — so the axes are chosen to be each
// predicate's own partition rather than a sample of spellings:
//
//   - **reftype**: `(ref func)` passes `is_elem_kind`; `(ref null func)` and its abbreviation
//     `funcref` fail it on nullability and pass 0x04's guard (`rt = (Null, FuncHT)`, :1079);
//     `externref` fails both, which is the third case those two guards are not each other's
//     negation over. The two funcref spellings are both here because they must produce the *same*
//     flag and a table listing one could not say so.
//   - **element shape**: empty (vacuously all-index, the inversion a hand-written loop gets wrong),
//     one bare `(ref.func 0)`, an `(item (ref.func 0))` that must encode identically to the bare
//     one, two bare ones so the fold is not a decision on element 0, a single `(item …)` holding
//     *two* instructions (fails `is_elem_index`, whose pattern is a list of length one), a
//     **mixed** segment whose first element passes and second fails — the row that forces `for_all`
//     to be a fold — and an `(item (i32.const 0))`, ill-typed and irrelevant that it is, since the
//     encoder writes what the grammar admits and validation is #9's.
//   - **mode**: passive, active at table 0, active at a non-zero table, declarative. Active-at-0 is
//     spelled `(i32.const 0)` with no tableuse and active-at-1 as `(table $t) (i32.const 0)`, so
//     the discriminator under test is the *resolved* index.
//
// 4 modes × 5 reftypes × 7 shapes = 140 cells, of which 100 have an expected flag and 40 are the #8
// frontier's (below).
//
// # The want column is a switch over the reference's text, not a call to the encoder
//
// `wantFlag` is a second reading of `elem`'s two `match`es, written from the OCaml with each arm's
// line cited. That is the standing rule for this file and it has a specific teeth here: a helper
// that asked `isElemKind`/`allElemIndex` would be an echo of the code under test (grave #106) —
// those are the two predicates whose *conjunction* is the defect #146 was, so a want column
// computed from them would have agreed with the bug.
//
// # The frontier cells are asserted as refusals, not skipped
//
// 40 cells cannot encode today and each is refused with #8's message. **The split is 28 + 12, and
// the arithmetic was got wrong the first time — the counters below are what said so.** `(ref
// extern)` is refused in all 28 of its cells (4 modes × 7 shapes), since it fails `is_elem_kind` and
// therefore always writes a reftype `valTypeByte` has no byte for. The other 12 are `(ref func)` ×
// the three non-index shapes × 4 modes, and they are #146's fix speaking: such a segment *passes*
// `is_elem_kind` and so looks exempt, then routes to the expression family and needs the very byte
// that is missing. Those 12 are exactly the population the pre-#146 exemption wrongly cleared, and
// three of them were the panic.
//
// A cell that changes category — refused becoming encoded, or the reverse — fails here, which is
// what makes this table an account of where the frontier *is* rather than a list of what works.
//
// # What the falsification pass found, including the probe that passed
//
// Three defects were introduced and watched. Two fail here and passed the whole package before this
// test existed, which is the argument for it:
//
//   - **Dropping flag 0x04's `isFuncref` guard** fails seven cells — `externref` at table 0, every
//     shape — because an `externref` segment written as 0x04 has *no reftype field* and decodes as a
//     funcref segment. A wrong image that decodes clean, and the round trip agrees with it.
//   - **Restoring #146** (the frontier exempting on `isElemKind()` alone) fails at
//     `passive (ref func) one item of two ref.func` with the **panic** itself, which is the grave
//     reproduced by the instrument its comment names.
//   - **Making the writer route on `isElemKind()` alone** changes *nothing observable* — and that is
//     a finding rather than a gap. The writer's conjunction differs from its first half on exactly
//     the 12 cells the frontier refuses, so the second half is unreachable today and stays so until
//     the GC gate admits those cells. Stated rather than left as a hole: this control cannot see
//     `encodeElems`' half of the pair, only `encodableOrErr`'s, and the day #8's frontier moves is
//     the day the twelve cells become flag assertions and the writer's half becomes falsifiable.
func TestEncodeMatchesTheReferenceOnElemFlags(t *testing.T) {
	modes := []struct {
		name, prefix string
		mode         binary.ElemMode
		table        uint32
	}{
		{"passive", "", binary.ElemPassive, 0},
		{"active at table 0", "(i32.const 0) ", binary.ElemActive, 0},
		{"active at table 1", "(table $t) (i32.const 0) ", binary.ElemActive, 1},
		{"declarative", "declare ", binary.ElemDeclarative, 0},
	}
	// null and abs are the reference's `rt` pair, stated per spelling so `wantFlag` can ask the
	// reference's own questions of it rather than this package's predicates.
	reftypes := []struct {
		spelling string
		null     bool
		abs      string
	}{
		{"(ref func)", false, "func"},
		{"(ref null func)", true, "func"},
		{"funcref", true, "func"}, // the abbreviation of the row above; must agree with it
		{"externref", true, "extern"},
		{"(ref extern)", false, "extern"},
	}
	// allIndex is `for_all is_elem_index` read off the *text*: whether every element is written as
	// exactly one `ref.func`. Empty is true, which is OCaml's `for_all` over `[]`.
	shapes := []struct {
		name, elems string
		allIndex    bool
	}{
		{"empty", "", true},
		{"one bare ref.func", " (ref.func 0)", true},
		{"one ref.func in an item", " (item (ref.func 0))", true},
		{"two bare ref.func", " (ref.func 0) (ref.func 0)", true},
		{"one item of two ref.func", " (item (ref.func 0) (ref.func 0))", false},
		{"a bare one then an item of two", " (ref.func 0) (item (ref.func 0) (ref.func 0))", false},
		{"one item of i32.const", " (item (i32.const 0))", false},
	}

	// wantFlag is `elem`'s two matches, transcribed. Returns false for a cell this encoder refuses,
	// which is a fact about the frontier rather than about the reference — hence the separate return
	// rather than a sentinel flag value.
	wantFlag := func(mode binary.ElemMode, table uint32, null bool, abs string, allIndex bool) (byte, bool) {
		isElemKind := !null && abs == "func" // encode.ml:1044-1046
		if isElemKind && allIndex {
			switch mode { // :1065-1074
			case binary.ElemPassive:
				return 0x01, true
			case binary.ElemDeclarative:
				return 0x03, true
			default:
				if table == 0 {
					return 0x00, true
				}
				return 0x02, true
			}
		}
		// The expression family writes a reftype, and `valTypeByte` has one only for the two
		// unparameterized reference types — so every cell here whose type is not `funcref` or
		// `externref` is the #8 frontier, including the `(ref func)` cells that fell through the
		// `if` above. Grave #146 is precisely this line having been absent.
		if !null {
			return 0, false
		}
		switch mode { // :1076-1084
		case binary.ElemPassive:
			return 0x05, true
		case binary.ElemDeclarative:
			return 0x07, true
		default:
			if table == 0 && abs == "func" { // the 0x04 guard, :1079
				return 0x04, true
			}
			return 0x06, true
		}
	}

	encoded, refused := 0, 0
	for _, m := range modes {
		for _, rt := range reftypes {
			for _, sh := range shapes {
				// Two tables so index 1 exists, and one func so `(ref.func 0)` names something.
				src := "(module (func) (table 1 funcref) (table $t 1 funcref) (elem " +
					m.prefix + rt.spelling + sh.elems + "))"
				want, encodable := wantFlag(m.mode, m.table, rt.null, rt.abs, sh.allIndex)
				t.Run(m.name+" "+rt.spelling+" "+sh.name, func(t *testing.T) {
					b, err := EncodeModule([]byte(src))
					if !encodable {
						refused++
						if err == nil {
							t.Fatalf("%s encoded; its element type needs a parameterized reference "+
								"byte valTypeByte has no entry for, so this cell is the #8 frontier "+
								"and an encoding of it is a wrong image or a panic away from one", src)
						}
						if !strings.Contains(err.Error(), "parameterized reference") {
							t.Errorf("%s was refused as %q, want #8's frontier message: a cell that "+
								"changes *which* refusal it gets has moved category without saying so",
								src, err)
						}
						return
					}
					encoded++
					if err != nil {
						t.Fatalf("EncodeModule refused %s: %v", src, err)
					}
					i := bytesIndex(b, secElem)
					if i < 0 {
						t.Fatalf("the encoder produced % x with no element section (id %d)", b, secElem)
					}
					// id, size, segment count, then the flag.
					if len(b) < i+4 {
						t.Fatalf("the element section in % x is truncated", b)
					}
					if got, wantCount := b[i+2], byte(0x01); got != wantCount {
						t.Fatalf("the element section in % x declares %d segments, want %d",
							b, got, wantCount)
					}
					if got := b[i+3]; got != want {
						t.Errorf("%s encodes flag %#02x, want %#02x — the flag is the whole family "+
							"choice and it is *not* recoverable from a round trip: flags 0 and 2 at "+
							"table 0 decode to identical ElemSegments, as do 4 and 6 with funcref",
							src, got, want)
					}
				})
			}
		}
	}
	// Vacuity floors, per category rather than one total: 100 encoded cells behind 40 refused ones is
	// the empty-half-behind-a-full-one shape (grave #105), and a frontier that swallowed the whole
	// grid would leave every flag assertion above unrun while the loop still iterated 140 times.
	//
	// **Exact counts rather than floors, because the exact numbers are knowable here** — the grid's
	// extent is 4 × 5 × 7 and the frontier's shape is derivable, so a floor would be the looser
	// instrument used where the sharper one is in hand (grave #105's other half). They earned it
	// immediately: the first version of this check wanted 84 and 28, arithmetic that forgot the
	// twelve `(ref func)` × non-index cells, and the failure is what corrected the paragraph above.
	if encoded != 100 || refused != 40 {
		t.Errorf("exercised %d encoded cells and %d refused ones, want 100 and 40 (4 modes × 5 "+
			"reftypes × 7 shapes = 140; the refusals are (ref extern)'s 28 plus (ref func)'s 12 "+
			"non-index-shape cells): a shifted split means an axis lost a member or the frontier "+
			"moved, and either way the count is the only thing that says so", encoded, refused)
	}
}
