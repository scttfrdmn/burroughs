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
// GC stays **off** on purpose: it is the frontier `encodableOrErr` refuses at, so turning it on
// would test bytes this encoder does not emit. When the GC gate flips for the encoder, this is one
// field and the tables grow.
func decodeForTest(t *testing.T, b []byte) *binary.Module {
	t.Helper()
	d := &binary.Decoder{Features: binary.Features{SIMD: true}}
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
var encodableModules = []struct {
	src  string
	want []binary.CompType
}{
	{`(module)`, nil},
	{`(module (type (func)))`, []binary.CompType{
		{Kind: binary.CompFunc},
	}},
	{`(module (type (func (param i32))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
	}},
	{`(module (type (func (result i64))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{binary.I64}}},
	}},
	// Param order and result order, both, in one signature — a reversal in either would still
	// round-trip if the writer and reader reversed together, and the want column is what catches it.
	{`(module (type (func (param i32 f32) (result f64 i64))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{
			Params:  []binary.ValType{binary.I32, binary.F32},
			Results: []binary.ValType{binary.F64, binary.I64},
		}},
	}},
	// A named type. The name is a parse-time binding and must leave no trace in the image.
	{`(module (type $t (func (param f32))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.F32}}},
	}},
	// All five number types, in one place, so a transposed byte in the table is a single failure
	// rather than a missing row.
	{`(module (type (func (param i32 i64 f32 f64 v128))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{
			binary.I32, binary.I64, binary.F32, binary.F64, binary.V128,
		}}},
	}},
	// The two ungated reference types, and the two abbreviations that denote them. `funcref` and
	// `(ref null func)` are the *same type* — the parser normalizes the abbreviation — so all four
	// params here are exactly two distinct types, and an encoder that treated the spellings as
	// distinct would produce four different bytes.
	{
		`(module (type (func (param funcref externref (ref null func) (ref null extern)))))`,
		[]binary.CompType{{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{
			binary.FuncRef, binary.ExternRef, binary.FuncRef, binary.ExternRef,
		}}}},
	},
	// Multiple results, which is a Wasm 2.0 feature the encoder gets for free from `vec` and which
	// a one-result assumption would silently truncate.
	{`(module (type (func (result i32 i32 i32))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Results: []binary.ValType{
			binary.I32, binary.I32, binary.I32,
		}}},
	}},
	// Two explicit types, so index order is asserted rather than assumed.
	{`(module (type (func (param i32))) (type (func (param i64))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I64}}},
	}},
	// A `(rec …)` group. Its members occupy ordinary type indices, and with GC off a rec group of
	// functypes is still a legal spelling — so this asserts the group does not become one slot.
	{`(module (rec (type (func (param i32))) (type (func (param i64)))))`, []binary.CompType{
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I64}}},
	}},
	// `(sub …)`'s supertype list is read and discarded (subtype's comment), so the encoded type is
	// the inner comptype and the parents leave no bytes.
	{`(module (type (func)) (type (sub 0 (func (param i32)))))`, []binary.CompType{
		{Kind: binary.CompFunc},
		{Kind: binary.CompFunc, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
	}},
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
		{`(module (memory 1))`, "(memory …) field"},
		{`(module (table 1 funcref))`, "(table …) field"},
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
