// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// `is_const`'s GlobalGet arm has four properties the board cannot see, and this file is those four.
//
// The slice's own falsification bill, both lanes, `checkConstGlobals` in place:
//
//	M1   drop the global-initializer loop                default 60832/193   all-on 64723/323   −4 / −4
//	M2   global loop sees whole-module scope              default 60834/191   all-on 64725/321   −2 / −2
//	M3   segment offsets see no defined globals           default 60836/189   all-on 64727/319   — no change
//	M4   match on Op alone, ignoring Prefix               default 60836/189   all-on 64727/319   — no change
//	M5   mutable globals accepted in const exprs          default 60831/194   all-on 64722/324   −5 / −5
//	M6   unresolved index reported as non-const           default 60829/196   all-on 64720/326   −7 / −7
//	M7   drop the element-expression loop                 default 60836/189   all-on 64727/319   — no change
//	M8   index moved out of the message head              default 60832/193   all-on 64723/323   −4 / −4
//	M9   drop the data-offset check                       default 60832/193   all-on 64723/323   −4 / −4
//	M10  a global's initializer sees itself (i+1)          default 60835/190   all-on 64726/320   −1 / −1
//	M11  one global too few (i-1), accept direction        default 60836/189   all-on 64727/319   — no change
//
// #419 adds a **fifth** const-expr call site — `check_table`'s — and with it three rows, measured on
// this branch's own base of default 60909/23 and all-on 64969/77:
//
//	M12  drop the table-initializer check                 default 60909/23    all-on 64959/87    — / −10
//	M13  table initializer sees whole-module scope        default 60909/23    all-on 64968/78    — / −1
//	M14  table initializer sees the tables' own index (i) default 60909/23    all-on 64969/77    — no change
//
// **All three dashes in the default column are one fact and it is a gate, not a sample gap.** A table
// that spells an initializer encodes to the `0x40` wire form, which `decodeTableForm` holds behind GC,
// so the default lane's every table carries the *synthesized* `ref.null` — an expression that reads no
// global, names no index and validates under any scope. The rule is unaskable there rather than
// unasked, which is the distinction M3's and M7's rows do not get to make.
//
// M13's −1 is the row that corrects this file's opening sentence for the new site. The four properties
// above are ones the board cannot see; this one it can, in one lane, at `global.wast:674-680` — and the
// draft that said otherwise had argued it from a `grep` for `unknown global`, which found four files
// and no table site because the vector's text says `(table $t 10 funcref (global.get $g))` and the
// grep was looking for the expected-error string in the wrong one of the two. Mutate and read the
// board; do not enumerate.
//
// M1 + M9 + M5 + M6 partition the twelve converted vectors twice over, from two directions: by
// *site* (4 global initializers, 4 data offsets, 4 element-segment offsets) and by *message* (5
// `constant expression required`, 7 `unknown global`). M5 loses exactly the 5 and M6 turns exactly
// the 7 into wrong-message rows, which is the pair worth reading — the two halves of one reference
// line have disjoint corpus populations, so each is witnessed alone rather than jointly.
//
// **M3, M4, M7 and M11 moved neither lane**, and each gets a row below. Their reasons differ and
// the difference matters:
//
//   - M3 and M7 are *sample* gaps. The corpus's data and element vectors reach their globals through
//     an **import** (`(import "M" "g" (global (mut i32)))`), so the defined-global scope those call
//     sites pass is never exercised, and the expression-form element vector does not exist at all.
//   - M4 is a gap no corpus could close, because the input it needs is one the decoder refuses to
//     build (see TestConstExprIgnoresAPrefixedOpcodeSharingGlobalGetsByte).
//   - M11 is a *harness* gap and is filed as #341: the accept-direction witness exists in the
//     corpus at `global.wast:373-374`, and a bare `(module …)` command's pass is scored on the text
//     reader rather than on validation, so a validator that rejects a valid module is invisible
//     there. Confirmed by measurement, not inferred: a `modulePre` that refuses *every* module
//     leaves all 2143 `KindModuleText` commands green.
//
// M8's −4 is worth one more line, because it is a partial catch that reads like a full one. The
// corpus pins the index for **0 and 1 only** — the bucket keys are `unknown global`, `unknown global
// 0` and `unknown global 1`, and harness matching is substring (0003) — so the four rows it moves
// are the two 0s and the two 1s, while the three bare-key rows keep passing on a message with the
// index anywhere in it. TestUnknownGlobalMessagePinsTheIndexAndTheScope holds the rest of the
// format.

// c0 is `(i32.const 0)`, and gg is `(global.get n)` — both terminated, as the internal form has
// them. Written as helpers because every fixture below is one of the two.
func c0() []binary.Instr {
	return []binary.Instr{{Op: 0x41}, {Op: opEnd}}
}

func gg(idx uint64) []binary.Instr {
	return []binary.Instr{{Op: opGlobalGet, Imm0: idx}, {Op: opEnd}}
}

// decodedTable is a table as the *decoder* hands one over: a tabletype plus the `ref.null ht` initializer
// `decode.ml:1058-1063` synthesizes for the plain wire form, cast row included.
//
// Every fixture in this package that needs a table for some other rule's sake goes through here,
// because as of #419 a `binary.Table{ElemType: …}` literal is not a module the decoder can produce
// — it has an *empty* initializer, and `check_table`'s const check refuses one with `1 block(s)
// still open`. Six rows across three tests read that way before this existed, each reporting a
// table's defect while asserting something about an element segment or an export.
//
// It transcribes `binary.synthesizedRefNull` rather than calling it, that function being unexported,
// and **the transcription cannot drift silently**: the rule that now reads this field types the
// expression against the element type, so a fixture that stops matching what the decoder builds
// fails its own test loudly rather than quietly describing a module no decoder emits.
//
// `size` rather than `min`, which shadows the builtin — `revive`'s `redefines-builtin-id`, and the
// rename is the fix rather than a suppression because `Limits.Min` is what the parameter fills and
// the field's own name is the one a caller is thinking in.
func decodedTable(elem binary.ValType, size uint64) binary.Table {
	return binary.Table{
		ElemType:  elem,
		Limits:    binary.Limits{Min: size},
		Init:      []binary.Instr{{Op: opRefNull}, {Op: opEnd}},
		InitCasts: map[int][]binary.ValType{0: {elem.WithNull(true)}},
	}
}

// TestGlobalInitializerSeesTheGlobalsDeclaredBeforeIt is the M11 row: `check_global` folds one
// global into the context at a time (`{c with globals = c.globals @ [gt]}`, valid.ml:1059), so the
// scope a global's initializer sees is *exactly* the globals ahead of it — not fewer, and not
// itself.
//
// Three modules, and the three are one test because the rule is a boundary and a boundary needs
// both sides. The corpus has all three (`global.wast:358`, `:363`, `:373`), and only the two
// invalid ones are scored: the valid one is a bare `(module …)` command whose pass comes from the
// text reader (#341), so the too-narrow scope that rejects it moves neither lane.
func TestGlobalInitializerSeesTheGlobalsDeclaredBeforeIt(t *testing.T) {
	tests := []struct {
		name    string
		globals []binary.Global
		want    error // nil means the module must validate
	}{
		{
			// global.wast:373 — the accept direction, and the one #341 makes invisible.
			name: "a later global reads an earlier one",
			globals: []binary.Global{
				{Type: binary.I32, Init: c0()},
				{Type: binary.I32, Init: gg(0)},
			},
			want: nil,
		},
		{
			// global.wast:358. The module *has* a global 0; it is the initializer's own, and the
			// fold has not added it yet. A whole-module scope accepts this.
			name: "the sole global reads itself",
			globals: []binary.Global{
				{Type: binary.I32, Init: gg(0)},
			},
			want: ErrUnknownGlobal,
		},
		{
			// global.wast:363. Same shape one index out: the target exists and is declared *after*.
			name: "an earlier global reads a later one",
			globals: []binary.Global{
				{Type: binary.I32, Init: gg(1)},
				{Type: binary.I32, Init: c0()},
			},
			want: ErrUnknownGlobal,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &binary.Module{Globals: tc.globals}
			err := modulePre(m, declaredFuncs(m))
			switch {
			case tc.want == nil && err != nil:
				t.Errorf("modulePre refused a valid module: %v — the scope is narrower than the "+
					"reference's fold, which rejects modules the spec accepts and is the direction "+
					"the board cannot see (#341, G-3)", err)
			case tc.want != nil && err == nil:
				t.Errorf("modulePre accepted a module whose global initializer names a global not "+
					"yet in scope, want %v — the scope is wider than the reference's fold", tc.want)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Errorf("modulePre reported %v, want %v", err, tc.want)
			}
		})
	}
}

// TestTableInitializerSeesNoDefinedGlobal is the M12 row, and it is the third distinct answer this
// file gives to one question: `check_module` folds tables *before* globals (valid.ml:1160-1161), so a
// table's initializer is typed in a context holding the imported globals and no defined one.
//
// **The corpus has this rule in one lane and the row list was written on the premise that it had it in
// neither — the premise being wrong is what the last two rows are for.** M13 in the bill above is
// `len(m.Globals)`, and it costs exactly 1 all-on pass: `global.wast:674-680`, which is
// `(global $g funcref (ref.null func))` followed by `(table $t 10 funcref (global.get $g))` asserted
// `unknown global`. Found by mutating the argument and reading the board rather than by enumerating
// files, after a draft of this comment argued from a `grep` for `unknown global` that no table site
// existed. It is an all-on row and not a default one because a table that *spells* an initializer
// encodes to the `0x40` wire form, which `decodeTableForm` gates behind GC — so the whole rule is
// unaskable in the default lane, where every table's initializer is the synthesized `ref.null` that
// reads no global at all.
//
// **The scope argument still needs a unit witness, and M14 is why: `i` moves neither lane.** With one
// table `i` is 0, so a per-table fold and no fold at all agree on every corpus module and on the first
// four rows below. The fifth row is a module with *two* tables whose second initializer reads the
// defined global, where `i` is 1 and admits it — the protection-by-coincidence shape, caught by writing
// the mutation before trusting the row list.
//
// The accept direction is `table.wast:94` importing a global and `:102-103` reading it from two table
// initializers, and it passes under all three candidates, imports being counted by `globalTypeAt`
// outside this argument entirely. Rows 3 and 4 are the discriminating pair for `len(m.Globals)`: with
// an import *and* a definition, index 0 resolves and index 1 must not, where a single global of either
// kind leaves two of the three candidates agreeing.
func TestTableInitializerSeesNoDefinedGlobal(t *testing.T) {
	// An imported immutable funcref global, legal to read in any const expression.
	imported := []binary.Import{{Kind: binary.ExternGlobal, GlobalType: binary.FuncRef}}
	// A defined one of the same type, so nothing but the scope can refuse a read of it.
	defined := []binary.Global{{
		Type: binary.FuncRef, Init: []binary.Instr{{Op: opRefNull}, {Op: opEnd}},
		InitCasts: map[int][]binary.ValType{0: {binary.FuncRef}},
	}}

	// The table under test in every row: funcref, initialized by reading the global at `idx`.
	tab := func(idx uint64) []binary.Table {
		return []binary.Table{{
			ElemType: binary.FuncRef,
			Limits:   binary.Limits{Min: 1},
			Init:     gg(idx),
		}}
	}

	for _, tc := range []struct {
		name    string
		mod     binary.Module
		wantErr bool
	}{
		{
			// table.wast:102 — the corpus's own shape, and the one every candidate accepts.
			name: "an imported global",
			mod:  binary.Module{Imports: imported, Tables: tab(0)},
		},
		{
			// The module has a global 0. It is a *defined* global, and no defined global is in
			// scope at `check_table`. A scope of `i` accepts this, and `i` is 0 here.
			name:    "the sole global, defined",
			mod:     binary.Module{Globals: defined, Tables: tab(0)},
			wantErr: true,
		},
		{
			// Both index spaces populated: 0 is the import and 1 is the definition.
			name: "index 0 with an import ahead of a definition",
			mod:  binary.Module{Imports: imported, Globals: defined, Tables: tab(0)},
		},
		{
			// Same module, one index along. `len(m.Globals)` accepts this one.
			name:    "index 1, the defined half of the same space",
			mod:     binary.Module{Imports: imported, Globals: defined, Tables: tab(1)},
			wantErr: true,
		},
		{
			// M14: two tables, and it is the *second* one that reads the defined global. A scope of
			// `i` is 1 here and admits it, which is the reading no corpus module can refuse — every
			// vector on the board has one table, where `i` and 0 are the same number.
			//
			// **The first table has to be innocent, and the first draft's was not.** Written with
			// both tables reading the global, the row passed under `i` for the wrong reason: table 0
			// is refused at `i` = 0 and the loop returns, so the assertion was satisfied by a table
			// whose scope every candidate gets right and the row proved nothing about the second one.
			name: "the second table reads a defined global",
			mod: binary.Module{
				Globals: defined,
				Tables:  append([]binary.Table{decodedTable(binary.FuncRef, 1)}, tab(0)...),
			},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := modulePre(&tc.mod, declaredFuncs(&tc.mod))
			switch {
			case tc.wantErr && err == nil:
				t.Errorf("modulePre accepted a table initializer reading a global that is not in "+
					"scope at check_table, want %v — the scope is wider than the reference's fold, "+
					"which accepts modules the spec rejects and is a direction no corpus vector "+
					"reaches at this site", ErrUnknownGlobal)
			case tc.wantErr && !errors.Is(err, ErrUnknownGlobal):
				t.Errorf("modulePre = %v, want %v", err, ErrUnknownGlobal)
			case !tc.wantErr && err != nil:
				t.Errorf("modulePre refused a table initializer reading an imported global: %v — "+
					"imports are counted outside the scope argument, and this is the direction "+
					"`table.wast:102-103` witnesses", err)
			}
		})
	}
}

// TestSegmentOffsetsSeeEveryDefinedGlobal is the M3 row: a data or element segment's offset is
// checked after `check_module` has folded in every global (`valid.ml:1162-1163` run after `:1161`),
// so the scope those call sites pass is the full defined count and not a partial one.
//
// Unwitnessed by the corpus because of *how* its vectors are written, which is worth stating
// precisely rather than as "the suite doesn't cover it": `data.wast` and `elem.wast` reach their
// globals through an import, so every one of the eight vectors this slice converts resolves in the
// imported half of the index space and none of them exercises `definedInScope` at all. Passing 0
// there costs nothing on the board and is still wrong.
func TestSegmentOffsetsSeeEveryDefinedGlobal(t *testing.T) {
	// One defined, immutable global — so a segment offset reading it is a legal const expression,
	// and the only thing that can refuse it is a scope that does not include it.
	globals := []binary.Global{{Type: binary.I32, Init: c0()}}

	t.Run("data segment offset", func(t *testing.T) {
		m := &binary.Module{
			Memories: []binary.Memory{{}},
			Globals:  globals,
			Datas:    []binary.DataSegment{{Offset: gg(0)}},
		}
		if err := modulePre(m, declaredFuncs(m)); err != nil {
			t.Errorf("modulePre refused a data segment whose offset reads a defined immutable "+
				"global: %v — the offset's scope must be every global, not the count a global's "+
				"own initializer would see", err)
		}
	})
	t.Run("element segment offset", func(t *testing.T) {
		m := &binary.Module{
			Tables:  []binary.Table{decodedTable(binary.FuncRef, 0)},
			Globals: globals,
			Elems:   []binary.ElemSegment{{Mode: binary.ElemActive, ElemType: binary.FuncRef, Offset: gg(0)}},
		}
		if err := modulePre(m, declaredFuncs(m)); err != nil {
			t.Errorf("modulePre refused an element segment whose offset reads a defined immutable "+
				"global: %v", err)
		}
	})
}

// TestElementExpressionsAreCheckedInEveryMode is the M7 row: `check_elem` runs `check_const` over
// the segment's elements at `valid.ml:1100`, *before* `check_elemmode` at `:1101` decides anything
// about a table. So the element expressions of a passive segment are checked, and an active
// segment's elements are checked before its table index resolves.
//
// Two things the corpus holds neither of. It has no expression-form element segment whose element
// reads a mutable global at all — the four `elem.wast` vectors this slice converts are *offsets* —
// so dropping this loop entirely moves neither lane.
func TestElementExpressionsAreCheckedInEveryMode(t *testing.T) {
	// An imported mutable global: legal to read in a function body, never in a const expression.
	mut := []binary.Import{{
		Kind: binary.ExternGlobal, GlobalType: binary.I32, GlobalMutable: true,
	}}

	t.Run("passive segment, which names no table", func(t *testing.T) {
		m := &binary.Module{
			Imports: mut,
			Elems: []binary.ElemSegment{{
				Mode: binary.ElemPassive, ElemType: binary.FuncRef,
				ByExpr: true, Exprs: [][]binary.Instr{gg(0)},
			}},
		}
		err := modulePre(m, declaredFuncs(m))
		if !errors.Is(err, binary.ErrConstExprRequired) {
			t.Errorf("modulePre answered %v for a passive element segment whose element reads a "+
				"mutable global, want %v — a segment that names no table still has its elements "+
				"checked, the mode arm being reached after them",
				err, binary.ErrConstExprRequired)
		}
	})
	t.Run("active segment, elements before the table index", func(t *testing.T) {
		// Two defects: the elements are non-constant *and* the table index does not resolve. The
		// reference reports the elements, `check_const` at :1100 running before `check_elemmode`
		// at :1101 — so this also pins the ordering, which is why the module has no table.
		m := &binary.Module{
			Imports: mut,
			Elems: []binary.ElemSegment{{
				Mode: binary.ElemActive, TableIndex: 7, ElemType: binary.FuncRef,
				ByExpr: true, Exprs: [][]binary.Instr{gg(0)}, Offset: c0(),
			}},
		}
		err := modulePre(m, declaredFuncs(m))
		switch {
		case errors.Is(err, ErrUnknownTable):
			t.Errorf("modulePre reported %v, want %v: the table index was resolved before the "+
				"segment's elements were checked, inverting valid.ml:1100-1101 on every module "+
				"where both are wrong", err, binary.ErrConstExprRequired)
		case !errors.Is(err, binary.ErrConstExprRequired):
			t.Errorf("modulePre answered %v, want %v", err, binary.ErrConstExprRequired)
		}
	})
}

// TestConstExprIgnoresAPrefixedOpcodeSharingGlobalGetsByte is the M4 row, and it is the one gap on
// this list that no corpus vector could ever close.
//
// `Instr.Op` is the *sub*-opcode for a prefixed instruction, so `0xfd 0x23` and `global.get` share a
// value in that field and are told apart only by `Prefix`. The validator's instruction loop never
// has to care: it reaches `globalOp` through a switch that has already split the prefixed regions
// (`instr.go:64-80`), so `Op == opGlobalGet` is unambiguous by the time it is asked. Scanning a raw
// expression, that precondition is gone — **a helper reused outside the dispatch that supplied its
// invariant does not inherit the invariant** — and matching on `Op` alone would resolve a SIMD
// instruction's first immediate as a global index.
//
// The corpus cannot witness this because the decoder's own const-expression table refuses a
// non-const opcode before a `Module` exists (`internal/binary/instr.go:588`), so no board vector can
// deliver the input. That refusal is a declared layering debt, though, and *a debt is not an
// invariant*: it is one gate flip or one new const instruction away from not holding. This test
// builds the module directly, which is the only level the question can be asked at.
func TestConstExprIgnoresAPrefixedOpcodeSharingGlobalGetsByte(t *testing.T) {
	// Imm0 is a global index that does not exist, so an Op-only match cannot pass by luck: it would
	// have to report `unknown global 99`.
	simd := []binary.Instr{{Prefix: 0xfd, Op: opGlobalGet, Imm0: 99}, {Op: opEnd}}
	err := checkConstGlobals(&binary.Module{}, simd, 0)
	if err != nil {
		t.Errorf("checkConstGlobals reported %v for a 0xfd-prefixed instruction whose sub-opcode is "+
			"global.get's byte — the scan matched on Op without Prefix, so it read a SIMD "+
			"instruction as a global reference and its immediate as a global index", err)
	}
}

// TestUnknownGlobalMessagePinsTheIndexAndTheScope holds the part of the message the corpus does not.
//
// Harness matching is substring (0003) and the three bucket keys are `unknown global`, `unknown
// global 0` and `unknown global 1`, so the corpus constrains the index for **two values** and says
// nothing about any other, nor about the `(N in scope)` tail. M8 moving only 4 of the 7 rows is that
// gap measured: three vectors keep passing on a message with the index anywhere in it.
//
// The format is `indexInScope`'s verbatim, and that is the property under test — one rule reads the
// same way whichever phase reports it.
func TestUnknownGlobalMessagePinsTheIndexAndTheScope(t *testing.T) {
	m := &binary.Module{Globals: []binary.Global{
		{Type: binary.I32, Init: c0()},
		{Type: binary.I32, Init: c0()},
		{Type: binary.I32, Init: gg(9)},
	}}
	err := modulePre(m, declaredFuncs(m))
	if err == nil {
		t.Fatal("modulePre accepted a global initializer naming global 9 of a 3-global module")
	}
	// The index immediately after the category, and the scope count as the reference's `lookup`
	// renders it. Two globals are in scope at index 2, not three: the fold has not added the third.
	const want = "unknown global 9 (2 in scope)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("modulePre said %q, want a message containing %q — the corpus pins this format for "+
			"indices 0 and 1 only, so any other index and the scope tail are held here or nowhere",
			err, want)
	}
}
