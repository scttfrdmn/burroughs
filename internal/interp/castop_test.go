// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestBrOnCastBranchesOnTheCastResult is `branchCastTargetAt`'s falsification, and it is built
// around a witness that *discriminates* rather than around a mutation-and-diff.
//
// # Why a diff check would not have been enough here
//
// The defect this control exists for is reading `rt1` where `rt2` was meant. Swapping the index is a
// clean, non-empty, plausible diff — and on most modules it changes nothing observable, because
// `rt1` and `rt2` are frequently the same type or differ only in ways that do not change the
// verdict. A row where `rt1 == rt2` would pass under the mutation and read as a stillborn control,
// which is the trichotomy's second answer standing in for its first (#250's M6, in reverse: there a
// real diff mutated nothing, here a real mutation needs a row that can see it).
//
// So the operand is chosen to sit *between* the two types: a `struct` reference matches `anyref`
// (rt1) and does not match `(ref i31)` (rt2). Correct behaviour falls through and answers **7**;
// reading rt1 finds a match, branches, and answers **9**. One row, two distinguishable numbers, and
// the difference is exactly the bug.
//
// # Provenance: derived
//
// Not transcribed from a vector. The premises are `br_on_cast.wast:7-9`, whose `br_on_i31` function
// is this shape with the operand supplied by a table (`(br_on_cast $l anyref (ref i31))` — rt1
// nullable, rt2 not, so flags `0x01`), and `:77`'s `(assert_return (invoke "br_on_i31" (i32.const 0))
// (i32.const -1))`, which asserts the non-branching answer for a non-i31 operand. The inference is
// that a locally-allocated struct is a non-i31 `anyref` exactly as that vector's table slot is, so
// the fall-through is the reference's answer here too. Written locally rather than run from the
// vector because the vector's module cannot be instantiated yet — its table initializer ends in
// `any.convert_extern`, which is rung 5's *third* slice, and the trap that produces would make every
// read-back in the file a shadowed mismatch rather than a verdict about this arm.
func TestBrOnCastBranchesOnTheCastResult(t *testing.T) {
	const src = `(module
	  (type $s (struct))
	  (func (export "c") (result i32)
	    (block $l (result (ref i31))
	      (struct.new $s)
	      (br_on_cast $l anyref (ref i31))
	      (drop)
	      (i32.const 7)
	      (return))
	    (drop)
	    (i32.const 9)))`
	out := runGC(t, src)
	if got := out[0].Int32(); got != 7 {
		t.Errorf("got %d, want 7 — a struct reference matches `anyref` (rt1) and not `(ref i31)` "+
			"(rt2), so the branch is not taken; 9 is the answer produced by testing against the "+
			"input type instead of the cast target, which is always satisfied and always branches",
			got)
	}
}

// TestBrOnCastFailIsTheExactInversion pins the second opcode against the first with the *same*
// operand and the same types, so the only thing the two rows differ by is the verdict — which is all
// `eval.ml:253-258` differs by.
//
// A separate row rather than a table entry because the shared-arm implementation makes the two
// opcodes structurally incapable of disagreeing about the stack, and a control that ran them through
// one helper would be asserting a property of the helper rather than of the pair. Derived from the
// same premises as the test above, plus `br_on_cast.wast:38-46`, whose `br_on_non_i31` is the
// mirrored function.
func TestBrOnCastFailIsTheExactInversion(t *testing.T) {
	const src = `(module
	  (type $s (struct))
	  (func (export "c") (result i32)
	    (block $l (result anyref)
	      (struct.new $s)
	      (br_on_cast_fail $l anyref (ref i31))
	      (drop)
	      (i32.const 7)
	      (return))
	    (drop)
	    (i32.const 9)))`
	out := runGC(t, src)
	if got := out[0].Int32(); got != 9 {
		t.Errorf("got %d, want 9 — the same operand and types that leave `br_on_cast` falling "+
			"through must make `br_on_cast_fail` branch; 7 means the two opcodes share a verdict "+
			"as well as an arm", got)
	}
}

// TestBrOnCastLeavesTheReferenceOnBothPaths is the four-paths property stated as a test:
// `eval.ml:246-258` rebuilds `Ref r :: vs'` on every one of its four arms, which is *not* what
// `br_on_null` and `br_on_non_null` do, and a reader generalizing from those two writes a
// conditional push.
//
// Both directions in one function, because the shape of the bug is asymmetric: a missing push on the
// branching path starves the target label, and a missing push on the falling-through path starves
// whatever follows. Either is a stack underflow or a wrong value, and neither is visible from a row
// that only checks the branch was taken.
func TestBrOnCastLeavesTheReferenceOnBothPaths(t *testing.T) {
	// Branching path: the target block's result is the reference, and `ref.test` proves the
	// operand survived the transfer as the same kind of value.
	taken := `(module
	  (func (export "c") (result i32)
	    (block $l (result (ref i31))
	      (ref.i31 (i32.const 5))
	      (br_on_cast $l anyref (ref i31))
	      (unreachable))
	    (i31.get_u)))`
	if got := runGC(t, taken)[0].Int32(); got != 5 {
		t.Errorf("branching path: got %d, want 5 — the reference is `Ref r :: vs'` on the "+
			"branch arm too, so the target label receives it as an operand", got)
	}

	// Falling-through path: the operand is still there to be dropped and replaced.
	fell := `(module
	  (type $s (struct))
	  (func (export "c") (result i32)
	    (block $l (result (ref i31))
	      (struct.new $s)
	      (br_on_cast $l anyref (ref i31))
	      (i31.get_u (ref.i31 (i32.const 3)))
	      (return))
	    (drop)
	    (i32.const 9)))`
	if got := runGC(t, fell)[0].Int32(); got != 3 {
		t.Errorf("falling-through path: got %d, want 3 — a body that leaves the operand in "+
			"place under its own result needs the operand still to be there", got)
	}
}

// TestBrOnCastSlotsAreFlagsThenLabel is 0027 decision 1's *printed, never reasoned about* rule
// converted from a one-off measurement into a control, which is the only form in which the next
// reader inherits it.
//
// The bytes are assembled here rather than encoded from wat, because the claim is about which
// `Instr` field each immediate lands in and going through the text front end would put a second
// authority between the assertion and the wire. Flags `0x03` and label `0x02` are deliberately
// *different values*: with flags `0x01` and label `0x00` — the natural minimal case — a reversed
// slot assignment reads as `Imm0=0 Imm1=1` versus `Imm0=1 Imm1=0`, which is distinguishable, but
// with flags `0x00` it would not be, and the minimal case is what a later editor shrinks to.
//
// **The pair also pins `brOnCastLabel` itself**, so the accessor and the decoder cannot drift: the
// hazard the accessor exists for is that every other branching instruction's label is `Imm0`, and a
// decoder change that reordered the staging would make the accessor silently read the flags.
func TestBrOnCastSlotsAreFlagsThenLabel(t *testing.T) {
	const (
		flags = 0x03
		label = 0x02
	)
	// no locals; block(void); br_on_cast flags label any i31; end; end
	body := []byte{
		0x00,
		0x02, 0x40,
		0xFB, 0x18, flags, label, binary.HeapAny, binary.HeapI31,
		0x0B,
		0x0B,
	}
	img := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
		0x03, 0x02, 0x01, 0x00, // function: one func, type 0
	}
	code := append([]byte{0x01, byte(len(body))}, body...)
	img = append(img, append([]byte{0x0a, byte(len(code))}, code...)...)

	d := &binary.Decoder{Features: gcGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	fn := &m.Funcs[0]
	pc := -1
	for i, ins := range fn.Body {
		if ins.Prefix == 0xfb && ins.Op == opBrOnCast {
			pc = i
			break
		}
	}
	if pc < 0 {
		t.Fatalf("no br_on_cast in the decoded body (%d instructions) — the assembly above is "+
			"wrong, and every assertion below would be about an instruction that is not there",
			len(fn.Body))
	}
	ins := fn.Body[pc]
	t.Logf("br_on_cast at [%d]: Imm0=%d Imm1=%d", pc, ins.Imm0, ins.Imm1)
	if ins.Imm0 != flags {
		t.Errorf("Imm0 is %d, want the flags byte %d", ins.Imm0, flags)
	}
	if ins.Imm1 != label {
		t.Errorf("Imm1 is %d, want the label %d", ins.Imm1, label)
	}
	if got := brOnCastLabel(ins); got != label {
		t.Errorf("brOnCastLabel returned %d, want %d — the accessor and the decoder's staging "+
			"order have drifted, and the arm is branching to the flags byte", got, label)
	}

	// The pair, in the reference's order, with nullability out of the flags bits rather than out
	// of the opcode: flags 0x03 sets both, so both sides are nullable.
	v, ok := fn.CastTypes(pc)
	if !ok || len(v) != 2 {
		t.Fatalf("CastTypes staged %d types (ok=%v), want exactly 2", len(v), ok)
	}
	t.Logf("casts=%v", v)
	if got := v[0].String(); got != "(ref null any)" {
		t.Errorf("rt1 is %s, want (ref null any) — bit 0 of the flags byte is rt1's null bit", got)
	}
	if got := v[1].String(); got != "(ref null i31)" {
		t.Errorf("rt2 is %s, want (ref null i31) — bit 1 is rt2's", got)
	}
	if got, err := branchCastTargetAt(fn, pc, "br_on_cast"); err != nil {
		t.Errorf("branchCastTargetAt: %v", err)
	} else if got.String() != v[1].String() {
		t.Errorf("branchCastTargetAt returned %s, want rt2 (%s)", got, v[1])
	}
}

// TestBrOnCastIsNotInTheFBSwitch pins the *other* half of the split `execFB`'s doc comment now
// describes: the pair is answered by `runFrame` before delegation, so this switch must still decline
// them.
//
// Without this, an arm added to `execFB` for either opcode would sit permanently dead behind the
// interception — unreachable code that no board can see, since the interception answers first and
// answers correctly. That is the silent-unreachability shape, and the only thing that distinguishes
// it from working code is an assertion that the switch does *not* handle these two.
func TestBrOnCastIsNotInTheFBSwitch(t *testing.T) {
	const src = `(module (func (export "c")))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d := &binary.Decoder{Features: gcGate}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	for _, op := range []uint32{opBrOnCast, opBrOnCastFal} {
		st := &stack{}
		err := in.execFB(binary.Instr{Prefix: 0xfb, Op: op}, st, &m.Funcs[0], 0)
		if err == nil {
			t.Errorf("execFB answered fb %#02x — it must decline the pair, which runFrame "+
				"intercepts before delegating; an arm here is dead code the board cannot see", op)
			continue
		}
		if !strings.Contains(err.Error(), "fb") {
			t.Errorf("execFB refused fb %#02x with %v, want the `unsupported` rendering that "+
				"keeps the opcode visible as a board bucket", op, err)
		}
	}
}

// TestBranchCastTargetArityIsExact is the arity half of the structural pin, and it is the direction
// the behavioural rows above cannot reach: they exercise a correctly-staged pair, so they say
// nothing about what the accessor does when the decoder stages a different number of types.
//
// A synthetic `Func` rather than a decoded one, because the condition is one the decoder cannot
// currently produce — which is exactly `castTypeAt`'s reasoning for reporting an unreachable case at
// all. `ErrNotValidated` and not a trap: a side table the two packages disagree about is an engine
// bug, not a property of the guest.
func TestBranchCastTargetArityIsExact(t *testing.T) {
	// The type's identity is irrelevant here — the subject is the *count* — so the cheapest
	// exported constructor serves, and using one rather than a literal keeps this test out of the
	// business of knowing ValType's field layout.
	one := binary.RefType(0, true)
	for _, tc := range []struct {
		name  string
		casts map[int][]binary.ValType
	}{
		{"absent", nil},
		{"one type", map[int][]binary.ValType{0: {one}}},
		{"three types", map[int][]binary.ValType{0: {one, one, one}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := &binary.Func{Casts: tc.casts}
			_, err := branchCastTargetAt(fn, 0, "br_on_cast")
			if err == nil {
				t.Fatalf("no error for a %s cast vector — the accessor's whole contract is "+
					"that it returns rt2 or says why it cannot", tc.name)
			}
			if !errors.Is(err, ErrNotValidated) {
				t.Errorf("got %v, want ErrNotValidated", err)
			}
		})
	}
}

// TestConcreteSubtypingIsDeclaredNotStructural is grave #261's control: `match_deftype`'s
// disjunct 2 is `subst_deftype s dt1 = subst_deftype s dt2` (match.ml:152), an **equality**, and
// implementing its supertype half with the subtyping relation makes it transitively loose.
//
// # Why the whole matrix and not the two cells that were wrong
//
// The defect presented as exactly two false positives, `$t2 -> $t3` and `$t4 -> $t3`. A control
// asserting those two cells are zero would be scoped to the sample that happened to expose the bug
// and would say nothing about the next pair — the blind spot this project keeps re-paying for. So
// all 25 cells are pinned, which makes this a statement about the *relation* (a concrete type
// matches exactly the reflexive-transitive closure of its declared `sub` chain, and nothing
// structural) rather than about two repairs.
//
// The type hierarchy is `br_on_cast.wast:104-112`'s, chosen because it is the one the corpus uses
// to separate declared from structural subtyping: `$t2`, `$t3` and `$t4` all have comptype
// `(struct (field i32 i32))` and three *different* declared parents, so a structural reading
// collapses them and a declared reading keeps them apart. `$t0'` is in the module because `$t4`
// reaches `$t0` only through it — a two-link chain, which a one-level check would get wrong in the
// accept direction while every one-link pair still passed.
//
// # Asked through ref.test, deliberately, and this is what makes the row an attribution
//
// `ref.test` is slice 1's arm (#258) and `br_on_cast` is slice 2's; they share `matchRefType`. The
// defect was found via `br_on_cast.wast:205`'s `test-sub` and is **not** `br_on_cast`'s — asking
// through the older arm is what establishes that, and keeps this row from being retired if the
// branching arms are ever reworked.
func TestConcreteSubtypingIsDeclaredNotStructural(t *testing.T) {
	const types = `
    (type $t0 (sub (struct)))
    (type $t1 (sub $t0 (struct (field i32))))
    (type $t2 (sub $t1 (struct (field i32 i32))))
    (type $t3 (sub $t0 (struct (field i32 i32))))
    (type $t0p (sub $t0 (struct)))
    (type $t4 (sub $t0p (struct (field i32 i32))))
`
	// Declared chains: t1<:t0, t2<:t1<:t0, t3<:t0, t0p<:t0, t4<:t0p<:t0.
	names := []string{"$t0", "$t1", "$t2", "$t3", "$t0p", "$t4"}
	want := map[string][]string{
		//        t0    t1     t2     t3     t0p    t4
		"$t0":  {"1", "0", "0", "0", "0", "0"},
		"$t1":  {"1", "1", "0", "0", "0", "0"},
		"$t2":  {"1", "1", "1", "0", "0", "0"},
		"$t3":  {"1", "0", "0", "1", "0", "0"},
		"$t0p": {"1", "0", "0", "0", "1", "0"},
		"$t4":  {"1", "0", "0", "0", "1", "1"},
	}

	// Vacuity floor: an empty or shrunken matrix agrees with itself. 36 cells, and the count is
	// derived from the two lists rather than typed, so adding a type cannot silently narrow it.
	if got, exp := len(want)*len(names), 36; got != exp {
		t.Fatalf("expectation table is %d cells, want %d — the matrix and the type list disagree", got, exp)
	}

	asked := 0
	for _, src := range names {
		for j, tgt := range names {
			mod := fmt.Sprintf(`(module %s
  (func (export "c") (result i32)
    (ref.test (ref %s) (struct.new_default %s))))`, types, tgt, src)
			out, err := runGCErr(mod)
			if err != nil {
				t.Errorf("ref.test (ref %s) on a %s instance: %v", tgt, src, err)
				continue
			}
			if len(out) != 1 {
				t.Errorf("ref.test (ref %s) on a %s instance: %d results, want 1", tgt, src, len(out))
				continue
			}
			asked++
			got := strconv.FormatUint(out[0].Bits, 10)
			if got != want[src][j] {
				t.Errorf("ref.test (ref %s) on a %s instance = %s, want %s\n"+
					"\ta concrete type matches exactly its declared sub-chain; %s and %s share a comptype only if that is a coincidence",
					tgt, src, got, want[src][j], src, tgt)
			}
		}
	}
	if asked != 36 {
		t.Errorf("answered %d of 36 cells; a matrix that skips cells agrees with itself on the rest", asked)
	}
}

// TestCyclicSupertypeChainTerminatesWithTheReferenceVerdict witnesses the termination guards a
// cyclic declared-supertype chain reaches, which nothing else does.
//
// # Re-pointed by 0042, and the mechanism under it changed
//
// It used to name `matchDeftype`/`sameDeftype`'s two cycle guards. Those functions are deleted and
// the rows now run through `internal/validate`, whose guards are **not the same mechanism**: a
// `depth` bound against the two type spaces' combined size (`match.go:370` in `sameDefType`,
// `match.go:556` in `matchDeclaredSupertypes`) rather than repeat detection. The verdicts are
// unchanged, which is the only reason this reads as a re-pointing at all.
//
// **Which disjunct each row reaches was re-measured with a probe, not re-derived by reading**, and
// all three moved — the `guard` column below is that measurement:
//
//   - row 1 reaches disjunct **1**, same-module index identity (`match.go:305`). The cycle is never
//     entered: source and target are the same index in the same module, so the short-circuit
//     answers before any walk starts. It witnesses no guard at all now, and says so.
//   - row 2 reaches `sameDefType`'s **depth bound** (which returns *false*, so disjunct 2 no longer
//     answers this row) and then disjunct **3**'s `c.same() && sup == want` (`match.go:564`), which
//     is what returns true.
//   - row 3 reaches `matchDeclaredSupertypes`'s **depth bound** and returns false.
//
// Row 2 carries a fact about `internal/validate` worth stating here because this is where it was
// found: the rolled-form story — *"the cycles have become ordinals"* (`match.go:323`) — covers
// **intra-group** cycles only. Two separately-declared singleton types whose supertypes point at
// each other are cross-group references at every rung, so `sameDefType` recurses and it is the
// `depth` bound, not the rolling, that stops it. And **that bound's only witness in the tree is
// this row**: probing every disjunct across `internal/validate`'s own suite, `internal/interp`, and
// the all-gates-on corpus lane, `sameDefType`'s bound is reached exactly once and it is here.
//
// # Provenance: synthetic, and the corpus cannot supply these
//
// Every row here is hand-built, and the reason is measurable rather than asserted: instrumenting
// the guards with a `println` and running the **entire all-gates-on lane** produces **zero** hits —
// re-measured against the new guards for 0042 rather than carried over, since a provenance claim
// about a mechanism that was replaced is a claim about nothing.
// That is not an accident of coverage — a cyclic declared-supertype chain is exactly what
// `check_subtype_sub`'s forward-reference and finality rules (`valid.ml:169-174`) reject, so the only
// vectors that would exercise it are `assert_invalid` ones, and with no validator (#9) those score
// `unsupported` rather than running. The guards therefore exist for input the suite is structurally
// incapable of asking about, which is the definition of a control that has to be written by hand.
//
// The modules are accepted here because the decoder has no validator behind it — the same layering
// debt `internal/validate`'s own scope notes cite (`match.go:542-551` — the bound is there because
// the property that makes the walk finite is established by a check this build may not have run
// yet), reached from the direction that demonstrates it.
//
// # What is falsifiable and what is not
//
// The guards' *direction* is falsifiable and is what this test pins: flipping either one leaves the
// engine terminating and answering differently, so each row fails with a number rather than
// wedging. Guard **presence** is a termination property, and its falsification hangs by nature —
// removing a guard makes these modules loop forever, which would take the test binary down without
// naming a row (the rule against a control that hangs rather than fails). So presence is asserted by
// this test *returning at all*, and direction is asserted by the verdicts below. Stated rather than
// left as a gap, because the two halves have different strengths.
//
// # Why each direction is the one the reference implies
//
// Disjunct 2 answers **true** on a repeat: it stands in for `match_deftype`'s own
// `dt1 == dt2` pointer-identity short-circuit (`match.ml:151`), which an engine comparing indices
// rather than OCaml values cannot spell. Disjunct 3 answers **false**: it is a *search* — "have A's
// supertypes any path to B" — and a search that has returned to its own start has found nothing on
// that path. Answering true there would let a cycle manufacture a subtype relation out of itself,
// which row three shows concretely: `$a` and `$c` share no declared ancestor, and `true` would make
// an unrelated cast succeed.
func TestCyclicSupertypeChainTerminatesWithTheReferenceVerdict(t *testing.T) {
	for _, c := range []struct {
		name  string
		guard string
		src   string
		want  uint64
		why   string
	}{
		{
			name:  "self-referential type, equal comptypes",
			guard: "disjunct 1, same-module index identity (match.go:305) — measured",
			src: `(module (type $t (sub $t (struct)))
  (func (export "c") (result i32) (ref.test (ref $t) (struct.new_default $t))))`,
			want: 1,
			why: "a type matches itself, and it never reaches the cycle: source and target " +
				"are one index in one module, so disjunct 1 answers first",
		},
		{
			name: "two-cycle, equal comptypes",
			guard: "disjunct 3's sup == want (match.go:564), after sameDefType's depth bound " +
				"returns false (match.go:370) — measured",
			src: `(module (type $a (sub $b (struct))) (type $b (sub $a (struct)))
  (func (export "c") (result i32) (ref.test (ref $b) (struct.new_default $a))))`,
			want: 1,
			why: "shape equality gives up at its depth bound here — the cross-group " +
				"references recurse rather than roll — and the upward walk then finds that " +
				"$a's declared supertype is $b outright",
		},
		{
			name:  "two-cycle with unequal comptypes, unrelated target",
			guard: "matchDeclaredSupertypes's depth bound (match.go:556) — measured",
			src: `(module (type $a (sub $b (struct (field i32)))) (type $b (sub $a (struct)))
  (type $c (sub (struct (field f64))))
  (func (export "c") (result i32) (ref.test (ref $c) (struct.new_default $a))))`,
			want: 0,
			why: "disjunct 2 fails at every pair, so only the upward walk is left; $a has no " +
				"declared path to $c, the cycle must not invent one, and the depth bound is " +
				"what ends the climb",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := runGCErr(c.src)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(out) != 1 {
				t.Fatalf("%d results, want 1", len(out))
			}
			if out[0].Bits != c.want {
				t.Errorf("ref.test = %d, want %d\n\tguard: %s\n\t%s",
					out[0].Bits, c.want, c.guard, c.why)
			}
		})
	}
}

// TestFieldStorageClassDisagreementIsRefused is #248's control: the storage-class agreement check in
// `fieldStorage`, whose subject rung 5 created and whose witness the corpus does not supply.
//
// # Provenance: synthetic, by necessity, and the necessity is measured
//
// Re-running #248's instrumented counter over the whole all-gates-on lane with the rung-5 arms
// landed gives **32 same-pointer, 0 differing, 0 storage-differs** — the figure the issue asked for,
// and it did not move off zero. The corpus cannot produce a differing pair: `test-sub` and
// `test-canon` cast without reading fields, and every field-reading vector casts to exactly the type
// whose accessor it uses, so nothing reads through a *supertype's* immediate.
//
// Nor could a **valid** module produce one — `match_fieldtype` (match.ml:134-138) forbids a subtype
// from changing a field's storage class. So the input is a module #9 would reject, hand-built here,
// which is precisely what the check exists for: it is in the `undefined field` class, a crash-class
// case the validator's absence lets through.
//
// # What it catches, and why the failure is silent without it
//
// `$t4` declares `$t0` as its supertype while giving field 0 **i32** storage where `$t0` gives it
// **v128**. The cast to `(ref $t0)` succeeds — correctly, by `match_deftype` disjunct 3, since `$t4`
// really does declare `$t0` — and then `struct.get $t0 0` reads a field that was written from the
// numeric stack as though it were on the v128 stack. Measured without the check: **no error, and the
// function returns 99**. That is grave #243's shape with nothing to report it, which is why a
// green board could never have found this.
func TestFieldStorageClassDisagreementIsRefused(t *testing.T) {
	const src = `(module
  (type $t0 (sub (struct (field (mut v128)))))
  (type $t4 (sub $t0 (struct (field (mut i32)))))
  (func (export "c") (result i32)
    (local $r (ref null $t0))
    (local.set $r (struct.new $t4 (i32.const 7)))
    (drop (struct.get $t0 0 (local.get $r)))
    (i32.const 99)))`

	out, err := runGCErr(src)
	if err == nil {
		t.Fatalf("a numeric field read through a v128 immediate was accepted, returning %v\n"+
			"\tthe wrong stack array is read and nothing reports it — grave #243's shape", out)
	}
	if !errors.Is(err, ErrNotValidated) {
		t.Fatalf("got %v, want ErrNotValidated", err)
	}
	// The message names both classes, per #248: "numeric storage in the object's" is the half that
	// tells a reader which side to look at, and an error saying only that the two disagree would
	// leave that to be re-derived.
	for _, want := range []string{"v128 storage in the instruction's type", "numeric storage in the object's"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestCovariantImmutableFieldIsNotADisagreement is the accept-direction half of the control above,
// and it is the row that keeps #248's check from being written as `Storage` equality.
//
// `match_fieldtype` requires equality only for a `Var` field; a `Cons` field is **covariant**, so a
// valid subtype may narrow field 0 from `(ref null $t0)` to `(ref null $sub)`. Both are references,
// both use the reference array, and the module is legal — a storage-equality check would refuse it.
// No vector exercises this, so without this row the stricter check would pass every board and be
// wrong in the direction §9 G-3 names as worse.
func TestCovariantImmutableFieldIsNotADisagreement(t *testing.T) {
	const src = `(module
  (type $s0 (sub (struct)))
  (type $sub (sub $s0 (struct)))
  (type $t0 (sub (struct (field (ref null $s0)))))
  (type $t4 (sub $t0 (struct (field (ref null $sub)))))
  (func (export "c") (result i32)
    (local $r (ref null $t0))
    (local.set $r (struct.new $t4 (ref.null $sub)))
    (ref.is_null (struct.get $t0 0 (local.get $r)))))`

	out, err := runGCErr(src)
	if err != nil {
		t.Fatalf("a covariant immutable reference field was refused: %v\n"+
			"\tboth sides use the reference array, so this is a legal module and the check must not "+
			"compare Storage for equality", err)
	}
	if len(out) != 1 || out[0].Bits != 1 {
		t.Errorf("got %v, want a single i32 1 (the field holds a null reference)", out)
	}
}
