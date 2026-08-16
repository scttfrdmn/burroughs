package binary

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math/bits"
	"strconv"
	"strings"
	"testing"
)

// TestIsRefPartitionsTheValTypeSpace is the control 0002 asks for on the reference/numeric
// split.
//
// # Why this is not a String-method test wearing a partition's name
//
// `IsRef` decides which of the interpreter's **two parallel arrays** a value lives in, and
// 0002 pins that as a consequence rather than a detail: pure Go offers no way to make the
// garbage collector see a pointer stored in a `uint64`, so a reference misfiled as numeric is
// a pointer the GC cannot trace — a use-after-free reachable from valid input, and one that
// reproduces only under collection pressure. There is no board that can see it. That is the
// accept-direction class (§9 G-3) at its worst: the defect is not a wrong answer, it is a
// wrong *storage class*, and the suite has no vector whose expected string mentions where a
// value was kept.
//
// So the predicate is checked before an opcode touches the stack, which is the ordering 0002
// gives — "adding it later means auditing every stack-touching opcode".
//
// # Scoped to the space, not to today's members
//
// The membership rows are derived from the `ValType` const block by AST walk, so a form added
// to the enum without a decision about which array it belongs in fails here rather than
// defaulting to numeric. Enumerating the eight current members would be a sample of the
// vocabulary as of authorship (the scope-controls-to-the-space law), and this vocabulary is
// *expected* to grow: the twelve GC reference forms arrive parameterized when that gate flips,
// and every one of them is a reference.
func TestIsRefPartitionsTheValTypeSpace(t *testing.T) {
	// The reference forms, by name rather than by value: the naming convention is the
	// spec's (`funcref`, `externref`, and GC's `anyref`/`eqref`/`structref`/…), so a form
	// whose name ends in "ref" and whose predicate says numeric is a real disagreement
	// between this file's two halves.
	//
	// Not the whole assertion — a value's *name* is not authority for its storage class —
	// which is why the explicit table below carries the spec citation and this only
	// catches the case the explicit table has not been updated for.
	for name, vt := range declaredValTypes(t) {
		if name == "NoValType" {
			// The sentinel is neither: it means *unrepresentable*, and nothing storable
			// ever holds it. Its arm is asserted separately below.
			continue
		}
		wantRef := strings.HasSuffix(strings.ToLower(name), "ref")
		if got := vt.IsRef(); got != wantRef {
			t.Errorf("%s (kind %#02x).IsRef() = %v, want %v: a reference stored in the "+
				"numeric array is a pointer the Go collector cannot trace (0002), and a "+
				"number in the reference array is a word it will try to follow",
				name, vt.kind, got, wantRef)
		}
	}

	// The sentinel, which the loop above skips and which must not report as a reference:
	// its zero value is what an unwritten field reads as, so `NoValType.IsRef() == true`
	// would put every field nobody wrote into the pointer array.
	if NoValType.IsRef() {
		t.Errorf("NoValType.IsRef() = true; it is the zero value, so a field the decoder " +
			"never wrote would be filed as a traceable pointer")
	}

	// The vacuity floor. An AST walk that matches nothing leaves the loop above asserting
	// over an empty map — the empty-set agreement (#29) — which is what a renamed type or a
	// moved file produces. Eight is the measured membership: NoValType, i32, i64, f32, f64,
	// v128, funcref, externref.
	if n := len(declaredValTypes(t)); n < 8 {
		t.Fatalf("derived %d ValType constants, want ≥8 (measured 8): the partition check "+
			"above is inside that loop and agrees with an empty set", n)
	}
}

// TestValTypeStringsAreDistinctAndNamed pins the two String methods the retained form's
// diagnostics read.
//
// # The defect direction, which is not "a label is ugly"
//
// These are what every retention error message names a type *by* — `internal/spec`'s control
// reports "function %d names type %d, which is a %s and not a func" — and a String method that
// collapses two cases into one label makes such a message name the wrong thing while the
// verdict stays right. That is grave #36's class exactly (an engine lying about its input),
// and #38's refinement says where the suite reaches it: nowhere, because no expected string in
// the corpus contains a valtype's or an extern kind's name. So the whole burden is here.
//
// The specific collapse this was written against: `NoValType` returning "unknown" alongside a
// byte the type genuinely has no name for. *Unrepresentable* is a limitation of this engine's
// representation and *unknown* is a claim about the module — reporting the former as the latter
// blames the input for the engine's own gate posture.
func TestValTypeStringsAreDistinctAndNamed(t *testing.T) {
	seen := map[string]string{}
	for name, vt := range declaredValTypes(t) {
		s := vt.String()
		if s == "" {
			t.Errorf("%s (kind %#02x).String() is empty", name, vt.kind)
			continue
		}
		// "unknown" is the default arm's label, so a *declared* constant reaching it means
		// the switch is missing an arm — which is the shape the exhaustive linter caught
		// once already and which it cannot catch for a constant added to the block without
		// a case.
		if s == "unknown" {
			t.Errorf("%s (kind %#02x).String() = %q: every declared form has a name, and "+
				"the default arm is for bytes this type does not define", name, vt.kind, s)
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("%s and %s both stringify to %q; a diagnostic naming a type cannot "+
				"distinguish them, so an error about one reads as an error about the other",
				prev, name, s)
		}
		seen[s] = name
	}

	// The extern kinds, same property and the same reason: `Import.Kind` and `Export.Kind`
	// are retained, and a collapsed label makes a diagnostic about an imported memory read
	// as one about an imported table.
	kinds := map[ExternKind]string{
		ExternFunc:   "func",
		ExternTable:  "table",
		ExternMemory: "memory",
		ExternGlobal: "global",
		ExternTag:    "tag",
	}
	kseen := map[string]bool{}
	for k, want := range kinds {
		got := k.String()
		if got != want {
			t.Errorf("ExternKind(%#02x).String() = %q, want %q", byte(k), got, want)
		}
		if kseen[got] {
			t.Errorf("ExternKind(%#02x) stringifies to %q, already used", byte(k), got)
		}
		kseen[got] = true
	}
	// The kind byte is read straight out of the image (`ExternKind(kind)` at sections.go:921
	// and :987), so an out-of-range value is reachable — from an *export*, whose kind byte
	// the decoder does not range-check. It must say so rather than name a kind.
	if s := ExternKind(0x7F).String(); s != "unknown" {
		t.Errorf("ExternKind(0x7f).String() = %q, want %q: the export path converts the "+
			"image's byte without a range check, so an undefined kind is reachable and "+
			"must not be reported as a defined one", s, "unknown")
	}
}

// TestValTypeNamedConstantsAreNotAlias asserts the eight named ValType package variables
// (NoValType, I32, I64, F32, F64, V128, FuncRef, ExternRef) still hold their defining values
// after this package's decoder has done a representative amount of work.
//
// **Why this is a real risk and not a formality.** 0018 moved these eight from `const` to
// package-level `var`, forced by the type itself: a struct value is not a Go constant. That
// widens what could go wrong — nothing stops a future edit from writing `I32.kind = 0` inside
// this package (an exported `var` in another package would additionally be writable from
// outside it, though every consumer of these is `internal/`), and such a write would silently
// change what every `t == I32`-style comparison across `internal/binary`, `internal/text`, and
// `internal/interp` means, everywhere, for the rest of the process — a single mutable global
// standing in for what used to be eight compiler-enforced constants.
//
// So this drives a representative decode — a module exercising every one of the eight kinds,
// through the real decodeValType/decodeRefType/decodeHeapType paths rather than by calling a
// constructor directly — and then re-checks each variable against a snapshot taken before the
// decode ran. A version of this package that assigned through one of them would decode
// correctly on this one module (nothing here depends on order) and then fail this comparison,
// which is the discriminating property a plain "does DecodeModule still work" test does not
// have.
func TestValTypeNamedConstantsAreNotAlias(t *testing.T) {
	type snapshot struct {
		name string
		ptr  *ValType
		want ValType
	}
	before := []snapshot{
		{"NoValType", &NoValType, NoValType},
		{"I32", &I32, I32},
		{"I64", &I64, I64},
		{"F32", &F32, F32},
		{"F64", &F64, F64},
		{"V128", &V128, V128},
		{"FuncRef", &FuncRef, FuncRef},
		{"ExternRef", &ExternRef, ExternRef},
	}

	// A funcref table's element type, then a global of every numeric/vector kind, then a
	// GC-gated global naming an abstract heaptype and the indexed form — one decode
	// touching decodeRefType's Wasm-2.0 branch, decodeNumType, decodeVecType, and
	// decodeRefType's GC branches, each of which reads one of these variables' kind byte
	// or builds a value that could in principle be confused with one.
	d := &Decoder{Features: featuresAllOn(t)}
	mods := [][]byte{
		funcTypeParam(0x70), // funcref parameter — decodeRefType's Wasm 2.0 branch
		funcTypeParam(0x7F), // i32 — decodeNumType
		funcTypeParam(0x7E), // i64
		funcTypeParam(0x7D), // f32
		funcTypeParam(0x7C), // f64
		funcTypeParam(0x7B), // v128 — decodeVecType, SIMD gate on
		funcTypeParam(0x6F), // externref
		funcTypeParam(0x6E), // anyref — decodeRefType's GC abstract branch
		refNullGlobal(0x00), // ref.null 0 — decodeHeapType's indexed branch
	}
	for _, m := range mods {
		if _, err := d.DecodeModule(m); err != nil {
			t.Fatalf("decoding a representative module failed: %v", err)
		}
	}

	for _, s := range before {
		if *s.ptr != s.want {
			t.Errorf("%s changed from %+v to %+v after decoding — a package variable "+
				"standing in for a constant must not be written through by any code path",
				s.name, s.want, *s.ptr)
		}
	}
}

// TestFuncRefIdentitiesMatchTheReference is grave #180's pinning control: FuncRef/ExternRef are
// the reference's *(Null, FuncHT)*/*(Null, ExternHT)* abbreviations, so decodeRefType's bare-byte
// branch must decode to the same ValType as the general parameterized-nullable spelling, and to a
// *different* ValType than the general non-null spelling — the exact relation #179 shipped
// backwards. Falsified by reverting FuncRef/ExternRef to null:false (the pre-fix value): the first
// two subtests fail (funcref no longer equals its own expansion, or wrongly equals the non-null
// one) while the abstract-kind and indexed-form subtests are unaffected, which is what confirms
// the defect was scoped to exactly the two Wasm 2.0 forms.
func TestFuncRefIdentitiesMatchTheReference(t *testing.T) {
	gc := Features{GC: true}
	decodeGlobalType := func(t *testing.T, b []byte) ValType {
		t.Helper()
		img := append([]byte{
			0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
			0x06, byte(len(b) + 5), 0x01,
		}, b...)
		img = append(img, 0x00, 0x41, 0x00, 0x0B)
		m, err := (&Decoder{Features: gc}).DecodeModule(img)
		if err != nil {
			t.Fatalf("decoding a global of type % x: %v", b, err)
		}
		return m.Globals[0].Type
	}

	t.Run("funcref equals its own (ref null func) expansion", func(t *testing.T) {
		bare := decodeGlobalType(t, []byte{0x70})
		expanded := decodeGlobalType(t, []byte{0x63, 0x70})
		if bare != FuncRef {
			t.Errorf("bare funcref decoded to %+v, want FuncRef (%+v)", bare, FuncRef)
		}
		if bare != expanded {
			t.Errorf("funcref (%+v) != its own (ref null func) expansion (%+v); they are the "+
				"same spec type spelled two ways", bare, expanded)
		}
	})

	t.Run("funcref differs from (ref func)", func(t *testing.T) {
		bare := decodeGlobalType(t, []byte{0x70})
		nonNull := decodeGlobalType(t, []byte{0x64, 0x70})
		if bare == nonNull {
			t.Errorf("funcref (%+v) == (ref func) (%+v); they are different spec types "+
				"(one nullable, one not)", bare, nonNull)
		}
		if nonNull.Null() {
			t.Errorf("(ref func) decoded with Null() == true, want false — it is the " +
				"explicit non-null spelling")
		}
	})

	t.Run("externref, the same two relations", func(t *testing.T) {
		bare := decodeGlobalType(t, []byte{0x6F})
		expanded := decodeGlobalType(t, []byte{0x63, 0x6F})
		nonNull := decodeGlobalType(t, []byte{0x64, 0x6F})
		if bare != ExternRef || bare != expanded {
			t.Errorf("externref (%+v) should equal ExternRef (%+v) and its expansion (%+v)",
				bare, ExternRef, expanded)
		}
		if bare == nonNull {
			t.Errorf("externref (%+v) == (ref extern) (%+v); different spec types", bare, nonNull)
		}
	})

	// An abstract GC kind and the indexed form already spell their own real nullability
	// (unaffected by this fix — #179's collision was scoped to Wasm 2.0's two abbreviations
	// only), pinned here so a regression that widened the fix's scope would be caught too.
	t.Run("an abstract GC kind is unaffected", func(t *testing.T) {
		null := decodeGlobalType(t, []byte{0x6E})          // anyref, the nullable abbreviation
		nonNull := decodeGlobalType(t, []byte{0x64, 0x6E}) // (ref any), explicit non-null
		if !null.Null() || nonNull.Null() {
			t.Errorf("anyref.Null()=%v (ref any).Null()=%v, want true/false", null.Null(), nonNull.Null())
		}
		if null == nonNull {
			t.Error("anyref == (ref any); different spec types")
		}
	})
}

// TestValTypeStringMatchesTheReferenceOnFuncExternNonNull pins a sibling gap swept up alongside
// grave #180: `(ref func)`/`(ref extern)` (kind 0x70/0x6F, null:false — the genuinely non-null
// spelling, distinct from FuncRef/ExternRef which are the nullable abbreviation) fell through
// String's abstractHeapNames lookup to "unknown", because that map had no 0x70/0x6F entries at
// all — present since 0018's implementation (#179), not introduced by #180's fix, but the same
// class of gap (a well-formed type this type declines to name) found in the same sweep.
func TestValTypeStringMatchesTheReferenceOnFuncExternNonNull(t *testing.T) {
	cases := []struct {
		name string
		t    ValType
		want string
	}{
		{"(ref func)", refKind(0x70, false), "(ref func)"},
		{"(ref extern)", refKind(0x6F, false), "(ref extern)"},
	}
	for _, c := range cases {
		if got := c.t.String(); got != c.want {
			t.Errorf("%s.String() = %q, want %q", c.name, got, c.want)
		}
		if c.t.String() == "unknown" {
			t.Errorf("%s.String() = %q: a well-formed non-null func/extern reference type "+
				"has no name", c.name, c.t.String())
		}
	}
}

// declaredValTypes reads the eight named ValType values out of this package's own source.
//
// By AST rather than by a literal table, for `immVocabulary`'s reason (instr_width_test.go):
// a hand-written list freezes the domain at the moment of authorship, and this domain grows
// when the GC gate flips. The parse target is module.go because that is where the block lives;
// a moved block yields an empty map, which the callers' floors catch.
//
// **Walks composite literals inside a `var` block, not `BasicLit`s inside a `const` block, as
// of 0018's implementation.** `ValType` is a struct now, so its eight named values are
// `ValType{kind: 0x7F}`-shaped `*ast.CompositeLit`s in a `var (...)` group rather than bare
// integer literals in a `const (...)` one — the AST shape this control reads changed exactly
// the way the type it reads did, and a walker still looking for `token.CONST`/`*ast.BasicLit`
// would find nothing and pass vacuously (the empty-set agreement, #29), which is why this was
// rewritten rather than left to degrade quietly. `NoValType`'s zero-elements literal (`ValType{}`)
// is read as kind 0, matching its zero-value definition.
func declaredValTypes(t *testing.T) map[string]ValType {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "module.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing module.go: %v", err)
	}
	out := map[string]ValType{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, val := range vs.Values {
				cl, ok := val.(*ast.CompositeLit)
				if !ok {
					continue
				}
				id, ok := cl.Type.(*ast.Ident)
				if !ok || id.Name != "ValType" {
					continue
				}
				var vt ValType
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "kind" {
						continue
					}
					lit, ok := kv.Value.(*ast.BasicLit)
					if !ok || lit.Kind != token.INT {
						continue
					}
					n, err := strconv.ParseUint(lit.Value, 0, 8)
					if err != nil {
						continue
					}
					vt.kind = byte(n)
				}
				out[vs.Names[i].Name] = vt
			}
		}
	}
	return out
}

// TestMemargPackingRoundTrips is the control the writer/reader pair owes, and it is deliberately
// narrow about what it can prove.
//
// `StageMemarg` and `Memarg` are two halves of one bit layout, so this test cannot show that the
// layout is *right* — it agrees with itself by construction, and the fields' real meanings are
// checked where they come from (`decodeMemop`'s vectors) and where they are used (`checkAlignment`,
// `memAccess`). What it can show is the one failure the pair has on its own: a shift or mask that
// moves in one half and not the other. That was a live near-miss rather than a hypothetical — see
// Memarg's comment on the two unmasked readers #306's six bits would have broken — and the fix put
// the layout in one place precisely so this check has a subject.
//
// The lane index is in the table because it is the third tenant of the same word: `stageLaneIdx`
// writes bits 32-39, and a memarg composer that widened into them would corrupt an immediate
// belonging to eight of the 45 rows while every memarg assertion here still passed.
//
// **The mask was watched *not* die twice, and the second time is the interesting one.** Deleting
// `StageMemarg`'s `&memargAlignMask` left every round-trip row green, first because the table only
// passed exponents that fit — the fix for which is the two `in: 0x4…` rows, since the sole caller
// hands over a whole memarg *flags byte* whose bit 6 is multi-memory's `has_idx` — and then *still*
// green with those rows present, because **`Memarg` masks on the way out as well**. The two masks
// make the pair insensitive to either one alone.
//
// So what the writer's mask actually protects is not the exponent, which the reader would clean up
// anyway: it is the bits **above** the field. An unmasked `0x42` sets bit 46, which belongs to no
// tenant today and to the fourth one tomorrow — precisely the latent break `Memarg`'s comment
// describes #306's six bits nearly making live in the other direction. `wantNoSpill` is the
// assertion that has that subject, and it is the only one of the four here that fails when the mask
// goes. A round trip cannot see a bit that no reader reads; only a claim about the word can.
func TestMemargPackingRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset uint64
		memIdx uint64
		in     uint32 // what the caller hands StageMemarg — a flags byte at the real call site
		want   uint32 // what Memarg must read back
		lane   uint8
	}{
		{name: "all zero"},
		{name: "natural i32 align", in: 2, want: 2},
		{name: "widest exponent", in: memargAlignMask, want: memargAlignMask},
		{name: "explicit memory index", memIdx: 1, in: 2, want: 2},
		{name: "wide offset past a LEB byte", offset: 4294967296, in: 3, want: 3},
		{name: "every tenant at once", offset: 128, memIdx: 3, in: 4, want: 4, lane: 15},
		// `0x42` is the flags byte the multi-memory row in internal/text asserts: align 2, bit 6
		// set. The exponent read back must be 2, and bit 6 must not have moved into the word.
		{name: "flags byte with the multi-memory bit set", memIdx: 1, in: 0x42, want: 2},
		{name: "flags byte at the widest legal value", in: 0x7f, want: memargAlignMask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imm1 := StageMemarg(tc.memIdx, tc.in) | uint64(tc.lane)<<memargLaneShift
			offset, memIdx, alignExp := Memarg(tc.offset, imm1)
			if offset != tc.offset {
				t.Errorf("offset = %d, want %d", offset, tc.offset)
			}
			if uint64(memIdx) != tc.memIdx {
				t.Errorf("memIdx = %d, want %d — a memarg reader that stopped masking would "+
					"report the alignment or the lane as a memory index", memIdx, tc.memIdx)
			}
			if alignExp != tc.want {
				t.Errorf("StageMemarg(_, %#02x) reads back as alignExp %d, want %d — either the "+
					"two halves disagree about where the exponent sits, which silently changes "+
					"every alignment verdict, or the writer stopped masking the flags byte's "+
					"non-exponent bits", tc.in, alignExp, tc.want)
			}
			if got := MemargLane(imm1); got != tc.lane {
				t.Errorf("MemargLane = %d, want %d — a memarg field widened into the lane's bits",
					got, tc.lane)
			}
			// The word's own boundary: nothing above the exponent field. Written against the
			// declared shift and mask rather than a literal 46, so widening the field is a
			// deliberate edit here and not a silent re-interpretation.
			wantNoSpill := memargAlignShift + bits.Len64(memargAlignMask)
			if spill := imm1 >> wantNoSpill; spill != 0 {
				t.Errorf("StageMemarg(%d, %#02x) sets %#x above bit %d, where no tenant lives yet "+
					"and the next one will: the flags byte's non-exponent bits were not masked off",
					tc.memIdx, tc.in, spill, wantNoSpill-1)
			}
		})
	}
}
