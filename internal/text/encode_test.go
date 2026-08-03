package text

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
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
func decodeForTest(t *testing.T, b []byte) *binary.Module {
	t.Helper()
	d := &binary.Decoder{Features: binary.Features{SIMD: true, Memory64: true}}
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
var encodableModules = []struct {
	src      string
	want     []binary.CompType
	wantTabs []binary.Table
	wantMems []binary.Memory
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
	// see decodeForTest for the measurement. `Memory` carries no address-type field of its own, so
	// what the want column can state is the limits — and the *bit* is asserted by the two probes
	// (0x02 and no bit at all) failing on this row rather than by a field comparison.
	{src: `(module (memory i64 1))`, wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}}},
	{src: `(module (memory i64 1 4))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 1, Max: 4, HasMax: true}},
	}},
	// A minimum above 2^32, which *only* an i64 memory can have — and which is the reason `limits`
	// reads `nat64`. A 32-bit read would have truncated this silently.
	{src: `(module (memory i64 4294967296))`, wantMems: []binary.Memory{
		{Limits: binary.Limits{Min: 4294967296}},
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
		{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
	}},
	// Two tables, index order, with different element types — a transposed element would swap them.
	{src: `(module (table 1 funcref) (table 2 externref))`, wantTabs: []binary.Table{
		{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}},
		{ElemType: binary.ExternRef, Limits: binary.Limits{Min: 2}},
	}},

	// # The three sections together, so section *order* is asserted
	//
	// `checkSectionOrder` rejects an image whose ids do not ascend, and the text field order here is
	// deliberately the reverse of the binary section order (memory before table, table id 4 before
	// memory id 5) — so an emitter that wrote sections in field order rather than id order fails.
	{
		src: `(module (memory 1) (table 1 funcref) (type (func (param i32))))`,
		want: []binary.CompType{
			{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		},
		wantTabs: []binary.Table{{ElemType: binary.FuncRef, Limits: binary.Limits{Min: 1}}},
		wantMems: []binary.Memory{{Limits: binary.Limits{Min: 1}}},
	},
}

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
	if len(encodableModules) < 12 {
		t.Fatalf("encodableModules has %d rows: a table this check reads is a table whose size is "+
			"part of the assertion, since a comparison over an empty set succeeds",
			len(encodableModules))
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
			p, err := parseModule([]byte(tc.src))
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
		{`(module (func))`, "(func …) field"},
		{`(module (import "m" "f" (func)))`, "(import …) field"},
		{`(module (export "a" (func 0)) (func))`, "(export …) field"},
		{`(module (global i32 (i32.const 0)))`, "(global …) field"},
		{`(module (start 0) (func))`, "(start …) field"},
		{`(module (data "abc"))`, "(data …) field"},
		{`(module (elem))`, "(elem …) field"},
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
		// The inline-import rows are the population split: an imported memory belongs to the import
		// section, so emitting it as a memory section entry would declare it twice.
		{`(module (memory (data "abc")))`, "(memory …) field"},       // + an implicit data segment
		{`(module (memory i64 (data "")))`, "(memory …) field"},      // the sugar's addrtype arm
		{`(module (memory (import "m" "x") 1))`, "(memory …) field"}, // an import, not a definition
		{`(module (memory (export "a") 1))`, "(memory …) field"},     // + an export section
		{`(module (table (import "m" "x") 1 funcref))`, "(table …) field"},
		{`(module (table (export "a") 1 funcref))`, "(table …) field"},
		{`(module (table funcref (elem)))`, "(table …) field"}, // + an implicit elem segment
		{`(module (table i64 funcref (elem)))`, "(table …) field"},
		{`(module (func $f) (table 1 funcref (ref.func $f)))`, "(func …) field"}, // an initializer expr
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
		{`(module (func) (memory 1))`, "(func …) field"},
		{`(module (data "abc") (table 1 funcref))`, "(data …) field"},
		{`(module (memory (data "x")) (memory 1))`, "(memory …) field"},
		{`(module (tag) (memory 1) (table 1 funcref))`, "(tag …) field"},
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
			if !strings.Contains(err.Error(), "#8") {
				t.Errorf("refusal says %q, want a tracking issue: an unexplained gap is the "+
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
