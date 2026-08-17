package interp

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestCallStackExhaustionIsReportedNotCrashed is the control `callBudget` cites, and its subject is
// the *shape* of the failure rather than the number: unbounded recursion must arrive as a value the
// harness can read, not as a host fault the process cannot survive.
//
// # Why the crash direction needs its own control
//
// `assert_exhaustion` is a spec outcome — `eval.ml:1115`'s `Exhaustion.error … "call stack
// exhausted"` — so the reject direction is oracle-covered: four vectors want this string
// (`call.wast:337-338`, `call_indirect.wast:585-586`), plus `fac.wast:109` and ten in
// `skip-stack-guard-page.wast`. But **the way an engine without a budget fails is not a fail**: `run`
// recurses into itself per call, so a missing check overflows the *Go* goroutine stack, and that is a
// `fatal error` no `recover` catches. Measured by deleting each check in turn: the run does exit
// non-zero, so CI goes red — and it goes red the way a build fault does, killing the process with a
// stack dump, no per-test verdict, and nothing after it in the package run at all. **The board loses
// its count rather than reporting a number**, which is a different event from a failing vector and
// wants a control that names it. So the suite's vectors say what the string is, and this says that a
// string *arrives*.
//
// # The rows are a partition over *how* a budget can be wrong
//
//   - **direct** — `call.wast:150`'s `$runaway`, self-recursion through `opCall`. The base case.
//   - **mutual** — `call.wast:152-153`'s pair. This is not a spelling variant of the first: an engine
//     counting recursion *per function* rather than per frame would bound `$runaway` and let two
//     functions calling each other run forever, and that is a plausible implementation, because
//     "am I already on the stack" is the question a naive cycle check asks.
//   - **indirect** — `call_indirect.wast:339`'s shape, reaching the budget through a table. There are
//     two checks in this package, not one (`call.go`'s `call` and its `callIndirect`), because the
//     two arms reach `invoke` by different routes; a fix applied to one leaves the other unbounded.
//     Falsified separately, and the pair is why both rows exist: deleting either check crashes only
//     its own row, and the surviving one still passes.
//   - **the boundary, both signs** — see below.
//
// # The boundary is the accept direction, and it is the half no vector can fail
//
// A budget that is *too low* refuses a program the spec permits, and every such module is one the
// suite expects to pass — §9 G-3's blind spot exactly. So the last two rows bracket the ceiling with
// a countdown of measured depth: `callBudget-1` nested calls must **return**, and `callBudget` must
// trap. Either alone is satisfied by an off-by-one in its own direction; together they pin the
// comparison as well as the constant.
//
// The depths are written as expressions in `callBudget` rather than as literals, because the
// constant is a tuning decision that may move and a hard-coded 9999 would then fail while
// describing nothing. What is asserted is *the relationship*: the ceiling is exactly where the
// constant says it is.
//
// # What was measured, and where the margin claim stands
//
// `callBudget`'s comment claims the figure is low enough that the host stack does not overflow
// first. The mechanical half of that claim is the accept row: `callBudget-1` frames complete, so the
// ceiling is reachable on this host. The rest was measured by raising the constant to 10^8 and
// bisecting — 400,000 frames complete; 800,000 aborts with `goroutine stack exceeds 1000000000-byte
// limit` — and by sampling `/memory/classes/heap/stacks:bytes` around a deep call, which puts a
// frame at ~1.7-2.1 KiB and the full 10,000 at 16 MiB. **One host, so the numbers are weather and
// the shape is the finding**: the margin is one to two orders of magnitude, not a few percent. A
// figure that needed the margin to be tight would need a per-host measurement, which is what
// `callBudget`'s comment declines for `maxFrameLocals`' reason.
func TestCallStackExhaustionIsReportedNotCrashed(t *testing.T) {
	for _, c := range []struct {
		what string
		src  string
	}{
		{
			// call.wast:150 — `(func $runaway (export "runaway") (call $runaway))`.
			"direct self-recursion",
			`(module (func $r (export "c") (call $r)))`,
		},
		{
			// call.wast:152 — `$mutual-runaway1` calls `$mutual-runaway2` calls back.
			"mutual recursion",
			`(module (func $a (export "c") (call $b)) (func $b (call $a)))`,
		},
		{
			// call_indirect.wast:339 — `(call_indirect (type $proc) (i32.const 16))` on a
			// table slot holding the calling function.
			"indirect self-recursion through a table",
			`(module
			   (type $t (func))
			   (table 1 funcref)
			   (elem (i32.const 0) $r)
			   (func $r (export "c") (call_indirect (type $t) (i32.const 0))))`,
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := encodeDecodeInvoke(c.src)
			wantExhaustion(t, err)
		})
	}

	// The countdown's depth is its i64 argument: `$d` calls itself n times and returns 0, so
	// invoking with n asks for n nested frames. Written with `if`/`else` rather than `br_if`
	// because the recursive arm has to be the one that is *not* taken at the bottom, and a
	// blocktype result makes the shape a single expression.
	const countdown = `(module
	  (func $d (param i64) (result i64)
	    (if (result i64) (i64.eqz (local.get 0))
	      (then (i64.const 0))
	      (else (call $d (i64.sub (local.get 0) (i64.const 1))))))
	  (func (export "c") (param i64) (result i64) (call $d (local.get 0))))`

	in, trap := instantiate1(t, countdown)
	if trap != nil {
		t.Fatalf("instantiate the countdown: %v", trap)
	}
	t.Run("the deepest permitted call returns", func(t *testing.T) {
		out, err := in.Invoke("c", Value{Type: binary.I64, Bits: callBudget - 1})
		if err != nil {
			t.Fatalf("%d nested calls were refused, one short of the budget (%d): %v\n"+
				"\tthis is the accept direction: a budget one too low refuses a program the "+
				"spec permits, and every such module is one the suite expects to pass",
				callBudget-1, callBudget, err)
		}
		if len(out) != 1 || out[0].Bits != 0 {
			t.Errorf("got %v, want [0]", out)
		}
	})
	t.Run("one deeper is exhaustion", func(t *testing.T) {
		_, err := in.Invoke("c", Value{Type: binary.I64, Bits: callBudget})
		wantExhaustion(t, err)
	})
}

// wantExhaustion asserts err is the exhaustion trap, checking the *type* as well as the text.
//
// Both halves earn their place. The text is what `assert_exhaustion` matches, so a trap with the
// wrong reason is right about the verdict and wrong about the evidence. The type is what keeps the
// verdict itself honest: `ErrUnsupported` carrying this sentence would read identically to a reader
// of the message and would land in the harness's *unsupported* column instead of its pass column —
// an engine claiming a gap where the spec gives an outcome, which is decision 0010's dilution in
// reverse.
func wantExhaustion(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("unbounded recursion returned without error")
	}
	var tr *Trap
	if !errors.As(err, &tr) {
		t.Fatalf("got %T (%v), want a *Trap: exhaustion is a spec outcome, not an engine gap", err, err)
	}
	if tr.Reason != trapExhaustion.Reason {
		t.Errorf("trap reason %q, want %q — the string is what assert_exhaustion matches",
			tr.Reason, trapExhaustion.Reason)
	}
}

// TestCallIndirectResolvesThroughTheTable is `callIndirect`'s accept-and-reject control, and its
// subject is the *resolution*: which table, which type, which failure, in which order.
//
// **The three trap strings are oracle-covered and the rest of this test is not**, which is why the
// rows are chosen the way they are. `undefined element` has 6 vectors in `call_indirect.wast`,
// `uninitialized element` 5 in `elem.wast`, and `indirect call type mismatch` 25 across five files
// — so the *sentinels* are the suite's business. What no vector can fail is the accept direction
// (§9 G-3): a resolution that picks the wrong table or the wrong type and finds a *plausible*
// function there returns a number, and the board scores it green. So the answer-bearing rows below
// are the ones that matter most, and they are built so a wrong reading gives a different answer
// rather than a trap.
//
// # The ordering rows, and why swapping the pair fails both in opposite directions
//
// The reference checks bounds before nullness — `func_ref` (`eval.ml:126-131`) calls `any_ref`
// (`:122-124`) and only then looks at the value — and this engine's `tab.load`/`r.Null` split
// mirrors it. Checking nullness first would report `uninitialized element` for an out-of-range
// index on **every table this engine builds**, because `newTable` null-fills. The two rows here are
// the same table and differ only in the index: 7 is past a size-1 table, 0 is in bounds and null.
// A swapped pair turns the first row's answer into the second's string while leaving the second
// unchanged, so one row alone could not tell a swap from a typo.
//
// The type check comes third, after a non-null slot has been resolved to a function, and the
// mismatch row's slot is filled — so it reaches the comparison only if the two prior checks passed.
//
// # The immediate order is pinned by *answers*, not by a trap
//
// `encode.ml:275` is `op 0x11; idx y; idx x` with `y` the type and `x` the table, so `Imm0` is the
// type and `Imm1` the table — the reverse of how the text reads them. A test that pinned this with
// a trap would be satisfied by an inverted reading whenever the wrong table happened to be empty,
// which is the common case. So `twoFilledTables` has **two tables, both filled, and two
// structurally identical types**: under an inverted reading every row still succeeds and returns
// the *other* function's number. Measured — with `Imm0`/`Imm1` swapped in `callIndirect`, `a`
// returns 20 and `b` returns 10, exactly transposed, and no trap anywhere.
//
// Identical types are the point rather than an accident: distinct ones would make an inverted
// reading trap on the type check and hide behind the mismatch string, which is a fail for the wrong
// reason. Structural equality is what makes them interchangeable, which the last row also asserts
// directly.
func TestCallIndirectResolvesThroughTheTable(t *testing.T) {
	// One table of one slot, one type, and three exported entries that differ only in what
	// they ask of it — so the three failures are reached from an identical starting state.
	const three = `(module
	  (type $wantI32 (func (result i32)))
	  (table 2 funcref)
	  (elem (i32.const 1) func $givesI64)
	  (func $givesI64 (result i64) (i64.const 1))
	  (func (export "oob")      (result i32) (call_indirect (type $wantI32) (i32.const 7)))
	  (func (export "null")     (result i32) (call_indirect (type $wantI32) (i32.const 0)))
	  (func (export "mismatch") (result i32) (call_indirect (type $wantI32) (i32.const 1))))`

	for _, c := range []struct {
		what, fn, reason string
	}{
		{
			// call_indirect.wast:270 — `(assert_trap (invoke "dispatch" (i32.const 22) …)
			// "undefined element")`, whose table has 10 slots.
			"an index past the table's end is undefined, not uninitialized",
			"oob", "undefined element 7",
		},
		{
			// elem.wast has 5 of these. The slot exists and holds the null every table
			// this engine builds is filled with, so this is the row a bounds-check-second
			// reading would steal.
			"a null slot in bounds is uninitialized",
			"null", "uninitialized element 0",
		},
		{
			// call_indirect.wast:508 — `dispatch-structural-i64`. The tail past the
			// sentinel is un-oracle-covered and is asserted here in full, on grave #36's
			// rule: a message naming a value must name the value the engine used, and the
			// spelling is `funcTypeString`'s authority-checked one.
			"a filled slot of the wrong type is a mismatch, and the trap names both types",
			"mismatch", "indirect call type mismatch, expected func [] -> [i32] but got func [] -> [i64]",
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			in, trap := instantiate1(t, three)
			if trap != nil {
				t.Fatalf("instantiate: %v", trap)
			}
			_, err := in.Invoke(c.fn)
			var got *Trap
			if !errors.As(err, &got) {
				t.Fatalf("err = %v (%T), want a *Trap", err, err)
			}
			if got.Reason != c.reason {
				t.Errorf("trap reason\n\tgot  %q\n\twant %q", got.Reason, c.reason)
			}
		})
	}

	// Two tables, both filled with functions that answer differently, and two structurally
	// identical types so the type check cannot distinguish them. `a` names table 1 and `b`
	// table 0; an inverted immediate reading transposes the answers without trapping.
	const twoFilledTables = `(module
	  (type $t0 (func (result i32)))
	  (type $t1 (func (result i32)))
	  (table 1 funcref)
	  (table 1 funcref)
	  (elem (i32.const 0) func $inTable0)
	  (elem (table 1) (i32.const 0) func $inTable1)
	  (func $inTable0 (result i32) (i32.const 10))
	  (func $inTable1 (result i32) (i32.const 20))
	  (func (export "a") (result i32) (call_indirect 1 (type $t0) (i32.const 0)))
	  (func (export "b") (result i32) (call_indirect 0 (type $t1) (i32.const 0))))`

	t.Run("the table immediate is the second one on the wire", func(t *testing.T) {
		in, trap := instantiate1(t, twoFilledTables)
		if trap != nil {
			t.Fatalf("instantiate: %v", trap)
		}
		for _, c := range []struct {
			fn   string
			want uint64
		}{{"a", 20}, {"b", 10}} {
			out, err := in.Invoke(c.fn)
			if err != nil {
				t.Fatalf("%s: %v", c.fn, err)
			}
			if len(out) != 1 || out[0].Bits != c.want {
				t.Errorf("%s = %v, want [%d] — a transposed pair (a=10, b=20) is an "+
					"inverted immediate reading, which no trap would show because "+
					"both tables are filled and both types match", c.fn, out, c.want)
			}
		}
	})

	// call_indirect.wast:508-516's `dispatch-structural-*` shape: the segment's function is
	// declared with `$a` and the call names `$b`, and they are distinct type-section entries
	// with identical structure — measured, the decoder does not intern them (three types
	// decoded, `$a` at 0 and `$b` at 1). `eval.ml:275` is `Match.match_deftype`, a subtyping
	// test, so this must **succeed**; comparing type *indices* would refuse it, and refusing a
	// valid module is the direction no rejection corpus can see.
	t.Run("structurally identical types at different indices match", func(t *testing.T) {
		out := invoke1(t, `(module
		  (type $a (func (param i32) (result i32)))
		  (type $b (func (param i32) (result i32)))
		  (table 1 funcref)
		  (elem (i32.const 0) func $id)
		  (func $id (type $a) (local.get 0))
		  (func (export "c") (result i32)
		    (call_indirect (type $b) (i32.const 42) (i32.const 0))))`, "c")
		if len(out) != 1 || out[0].Bits != 42 {
			t.Errorf("got %v, want [42]: `match_deftype` is structural, so a call naming "+
				"$b must accept a function declared $a", out)
		}
	})

	// The index operand is read **unsigned**, which `addr_of_num` does by widening an i32
	// without sign extension. `call_indirect.wast:271` is exactly this vector —
	// `(invoke "dispatch" (i32.const -1) …)` wanting `undefined element` — and the number in
	// the message is what says the reading was unsigned: a sign-extended -1 on a 64-bit
	// widening would print 18446744073709551615, and a truncated one would print -1.
	t.Run("a negative index reads unsigned", func(t *testing.T) {
		out, err := encodeDecodeInvoke(`(module
		  (type $t (func (result i32)))
		  (table 1 funcref)
		  (elem (i32.const 0) func $g)
		  (func $g (result i32) (i32.const 1))
		  (func (export "c") (result i32) (call_indirect (type $t) (i32.const -1))))`)
		var got *Trap
		if !errors.As(err, &got) {
			t.Fatalf("out = %v, err = %v (%T), want a *Trap", out, err, err)
		}
		if got.Reason != "undefined element 4294967295" {
			t.Errorf("trap reason %q; 4294967295 is the unsigned reading, "+
				"18446744073709551615 a sign-extended one, -1 a signed print", got.Reason)
		}
	})
}

// TestFuncTypeStringIsTheReferenceSpelling pins the mismatch trap's un-oracle-covered tail against
// its external authority.
//
// **Every one of the 25 `indirect call type mismatch` vectors stops at the sentinel**, so nothing
// in the suite reads a character of what this renders — which is precisely why it gets a control:
// grave #36's rule is that the half of a message the oracle cannot see is the half nothing else
// will check, and #38's refinement is that the doctrine is "the oracle reads exactly as far as its
// expected string does". Here it reads no further than the comma.
//
// The authority is `types.ml`, reduced for an MVP functype: `string_of_deftype`'s
// `DefT (RecT [st], 0l)` arm (`:399`) → `string_of_subtype`'s `SubT (Final, [], ct)` arm (`:386`)
// → `string_of_comptype`'s `FuncT` arm (`:382-383`), which is
// `"func " ^ string_of_resulttype ts1 ^ " -> " ^ string_of_resulttype ts2`. And
// `string_of_resulttype` (`:361-362`) is `"[" ^ concat " " ^ "]"` — **unconditionally bracketed**,
// so `func [] -> []` is what the empty functype renders as.
//
// # The empty row is the one that caught a defect, and it caught it in the prose
//
// The first version of `funcTypeString` rendered the *wat* spelling — `(func (param i32) (result
// i32))` — and its doc comment asserted that was the reference's and that empty clauses were
// "dropped". Both halves were false: the reference brackets rather than parenthesizes, and it omits
// nothing. The mistake is the obvious one to make, because wat is what a person writes a functype
// in; what let it stand is that no vector reads the string, so there was nothing to disagree with.
// The empty row below is the discriminating one — under the old rendering it produced `(func)`,
// which is a *legal wat functype* and therefore looks right to a reader who does not have
// `types.ml` open. Grave #147.
//
// The row list is a partition over the arms of the two functions, not a sample of shapes: empty and
// non-empty on each side (`string_of_resulttype`'s bracket), and a two-element side (its
// separator), which is where a `strings.Join`-shaped mistake would show as `func [i32i64] -> []`.
func TestFuncTypeStringIsTheReferenceSpelling(t *testing.T) {
	for _, c := range []struct {
		params, results []binary.ValType
		want            string
	}{
		// The empty functype. `string_of_resulttype []` is `"[" ^ "" ^ "]"`, so both sides
		// bracket — this is the row the wat rendering rendered as `(func)`.
		{nil, nil, "func [] -> []"},
		{[]binary.ValType{binary.I32}, nil, "func [i32] -> []"},
		{nil, []binary.ValType{binary.I32}, "func [] -> [i32]"},
		{[]binary.ValType{binary.I32}, []binary.ValType{binary.I64}, "func [i32] -> [i64]"},
		// Two on each side: the separator is a single space *between* entries, so a
		// trailing-separator or missing-separator error shows here and nowhere above.
		{
			[]binary.ValType{binary.I32, binary.F64},
			[]binary.ValType{binary.I64, binary.F32},
			"func [i32 f64] -> [i64 f32]",
		},
	} {
		got := funcTypeString(&binary.FuncType{Params: c.params, Results: c.results})
		if got != c.want {
			t.Errorf("funcTypeString(%v -> %v) = %q, want %q",
				c.params, c.results, got, c.want)
		}
	}
}

// TestSameFuncTypeDeclaredSupertypeWalk pins 0019's own named widening — `sameFuncType` climbing
// a declared supertype chain rather than only comparing structure — against the cases the task
// that landed it names explicitly.
//
// **The "two structurally identical, nominally unrelated types" case needs care about what
// "unrelated" actually means, and getting it wrong was the first draft's own mistake.** Two
// independently-declared bare types with the *same* `Final` and the *same* (empty) supertype
// list are not a counter-example to fix — under the reference's own isorecursive semantics
// (`match_deftype`'s disjunct 2, `subst_deftype s dt1 = subst_deftype s dt2`), two declarations
// that canonicalize to the same shape genuinely *are* the same type, structural equality being
// how Wasm GC's recursive type equivalence is defined, not an approximation of nominal identity.
// So the real counter-example — and the one `type-subtyping.wast:602/610` actually turns on — is
// two declarations that agree on the comptype and the (empty) supertype list but disagree on
// `Final`: `$t1 (sub (func))` (non-final) versus `$t2 (sub final (func))` (final), the exact M2
// shape. That pair must compare unequal, and did not before `Final` was retained.
func TestSameFuncTypeDeclaredSupertypeWalk(t *testing.T) {
	// type-subtyping.wast:596-599/602-608's own shape: $t1 non-final, $t2 final, both `(func)`.
	finalityDiffers := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: false}, // idx 0: $t1 = (sub (func))
			{Kind: binary.CompFunc, Final: true},  // idx 1: $t2 = (sub final (func))
		},
	}
	if sameFuncType(finalityDiffers, 0, finalityDiffers, 1) {
		t.Error("sameFuncType($t1, $t2) = true, want false: identical comptype and supertype " +
			"list but opposite finality must not match — the defect #164 left open " +
			"(type-subtyping.wast:602,610)")
	}
	// And two declarations that agree on everything, Final included, ARE the same type — the
	// isorecursive-canonicalization case above, stated as its own row so the finality check is
	// not mistaken for a general nominal-identity requirement.
	finalityAgrees := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: true},
			{Kind: binary.CompFunc, Final: true},
		},
	}
	if !sameFuncType(finalityAgrees, 0, finalityAgrees, 1) {
		t.Error("sameFuncType(finalityAgrees 0, 1) = false, want true: two declarations " +
			"agreeing on finality, comptype, and (empty) supertypes canonicalize to the same " +
			"type under the reference's own structural-equivalence semantics")
	}

	// A type declared as its own supertype's subtype: idx 1 is `(sub 0 (func))`, so 0's own
	// declared-supertype walk (disjunct 3) should find that 1 matches 0 — the sub-is-a direction,
	// `call_indirect`'s own use (the callee's *actual* type walks its own supertypes looking for
	// the *declared* type at the call site).
	subOf := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: true},                          // idx 0: (func), the supertype
			{Kind: binary.CompFunc, Final: true, Supertypes: []uint32{0}}, // idx 1: (sub 0 (func))
		},
	}
	if !sameFuncType(subOf, 1, subOf, 0) {
		t.Error("sameFuncType(subOf 1, subOf 0) = false, want true: idx 1 declares idx 0 as its " +
			"supertype, so a call site declaring idx 0 must accept a callee actually typed idx 1")
	}
	// And the reverse direction must NOT hold: declaring a supertype does not make the supertype
	// itself walk down to its subtypes (subtyping is not symmetric).
	if sameFuncType(subOf, 0, subOf, 1) {
		t.Error("sameFuncType(subOf 0, subOf 1) = true, want false: a supertype does not match " +
			"by declaring itself as its own subtype's subtype — subtyping is directional")
	}

	// The pre-existing MVP case: two functypes with the same params/results and no `sub` at all
	// (Final's zero-ish default true, no declared supertypes) — must still compare equal, unchanged
	// by this widening.
	plain := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: true, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
			{Kind: binary.CompFunc, Final: true, Func: binary.FuncType{Params: []binary.ValType{binary.I32}}},
		},
	}
	if !sameFuncType(plain, 0, plain, 1) {
		t.Error("sameFuncType(plain 0, plain 1) = false, want true: two identical declared " +
			"types with no sub involved must keep comparing equal — the pre-0019 MVP case, " +
			"which this widening must not regress")
	}
}

// TestSameFuncTypeDeclaredSupertypeWalkFalsification is the birth requirement (CLAUDE.md — "a
// control isn't born until it has been watched die") for the unrelated-types row above, run as an
// actual test rather than a comment claiming the result: it calls `structFuncTypeEqual` directly,
// which is exactly what `sameFuncType` reduced to before 0019's widening, and confirms the
// defect's own shape — two nominally-unrelated, structurally-identical types comparing *equal*
// under the old reduction, which is the wrong verdict #164 left open and the reason this task
// exists.
func TestSameFuncTypeDeclaredSupertypeWalkFalsification(t *testing.T) {
	a := &binary.FuncType{}
	b := &binary.FuncType{}
	if !structFuncTypeEqual(a, b) {
		t.Fatal("structFuncTypeEqual(a, b) = false; the falsification premise requires the old " +
			"reduction to agree on structure alone, which it does by construction here")
	}
	// The point: structFuncTypeEqual alone (the pre-widening reduction) cannot distinguish this
	// from the genuinely-equal case, which is exactly why sameFuncType had to widen past it.
}

// TestSameFuncTypeCorpusScope pins the measured scope boundary `sameFuncType`'s own doc comment
// states: this reduction resolves #164's four vectors down to two, and the other two
// (`type-subtyping.wast:752,767`, the M10/M11 pair) stay unresolved — not silently, but as a
// documented, tested false positive within the stated scope.
//
// **The mechanism is subtler than "different absolute index, same shape", and getting that wrong
// was this test's own first draft.** The reference's deftype identity is over the *whole rec
// group*, not over one member's own declared-supertype list in isolation: `roll_rectype`
// (types.ml:255-261) converts an *intra-group* supertype reference to a group-relative `Rec i`,
// while a *cross-group* reference stays an absolute `Idx`. M10's exporter declares `$f21`/`$f22`
// as their own two-member rec group, where `$f22`'s supertype `$f11` is defined in an *earlier*
// group — a cross-group reference, staying `Idx`. The importer's `$f11`/`$f12` are one rec group
// where `$f12`'s supertype `$f11` is its *own* group's first member — an intra-group reference,
// becoming `Rec 0`. So the two groups' canonical shapes genuinely differ (one member's supertype
// is `Idx`, the other's is `Rec`), even though the *specific member being compared* (`$f21`,
// bare, no supertypes) is byte-for-byte identical to the importer's `$f11` in isolation.
//
// This decoder retains no rec-group boundary at all, so `matchDeftype` cannot see the
// difference — it compares `$f21` and `$f11` as isolated CompTypes, finds them identical (both
// non-final, no supertypes, empty functype), and reports a match the reference refuses. That is
// the measured false positive, asserted here as what currently happens — not as what should
// happen — so a future rec-group-tracking fix has a red test turning green as its own evidence,
// rather than this gap silently persisting under a green board.
//
// **The board coupling this test used to assert is struck, and the assertion is not (#368).** The
// original error message promised that `type-subtyping.wast:752` and this assertion would flip
// together. `:752` flipped and this did not, because the grave routed the *linker* through
// `internal/validate`'s rolled relation — which retains `RecStart`/`RecLen` — while `matchDeftype`
// kept serving `call_indirect`/`call_ref`/`ref.cast` unchanged. Two claims were bundled in one
// sentence: "the gap is still here" (true, and still asserted below) and "the corpus will tell you
// when it goes" (false as of #368, because the corpus row moved for an unrelated reason). *A
// tripwire whose subject dissolves is re-pointed, never closed* — the risk is live and now has no
// corpus witness at all, which makes this direct assertion the only instrument left on it.
func TestSameFuncTypeCorpusScope(t *testing.T) {
	exporter := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: false},                          // idx 0: $f11 = (sub (func))
			{Kind: binary.CompFunc, Final: false, Supertypes: []uint32{0}}, // idx 1: $f12 = (sub $f11 (func)), same rec group as $f11
			{Kind: binary.CompFunc, Final: false},                          // idx 2: $f21 = (sub (func)), its own rec group with $f22
			{Kind: binary.CompFunc, Final: false, Supertypes: []uint32{0}}, // idx 3: $f22 = (sub $f11 (func)) — supertype is idx 0, a CROSS-group reference (type-subtyping.wast:748)
		},
	}
	importer := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: false},                          // idx 0: $f11 = (sub (func))
			{Kind: binary.CompFunc, Final: false, Supertypes: []uint32{0}}, // idx 1: $f12 = (sub $f11 (func)), same rec group
		},
	}
	// The corpus's actual question (type-subtyping.wast:752-757): does the exporter's export
	// (typed $f21, idx 2) match the importer's declared type ($f11, idx 0)? The reference says
	// no (`assert_unlinkable`, "incompatible import type") because the two rec groups' shapes
	// differ per the mechanism above.
	if !sameFuncType(exporter, 2, importer, 0) {
		t.Error("sameFuncType(exporter $f21, importer $f11) = false, want true (the KNOWN false " +
			"positive): this reduction has no rec-group boundary to distinguish $f21's group " +
			"(whose sibling $f22 cross-references an earlier group) from $f11's group (whose " +
			"sibling $f12 self-references within the group) — if this assertion starts failing, " +
			"a rec-group fix landed in matchDeftype itself and this test should flip to the " +
			"correct 'false'. It no longer predicts a board row: #368 moved the linker onto " +
			"internal/validate's rolled relation, so type-subtyping.wast:752 already passes " +
			"while this gap remains, and call_indirect/call_ref/ref.cast are the live consumers " +
			"with no corpus vector of this shape reaching them")
	}
}
