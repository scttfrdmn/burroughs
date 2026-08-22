package interp

import (
	"errors"
	"slices"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/validate"
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

// TestCallIndirectComparisonDeclaredSupertypeWalk pins the properties `call_indirect`'s type check
// depends on, asserted of the relation it actually calls — `validate.MatchDefType` as of 0042,
// `sameFuncType` before it. The rows are 0019's: that ADR named the declared-supertype walk as its
// widening and listed the cases the walk had to get right. They outlive their subject's deletion
// because they were always properties of the *relation*, never of the function, and **a tripwire
// whose subject dissolves is re-pointed rather than quietly dropped** — 0042's own bound on itself.
//
// One row is not merely preserved but newly *correct*, and it is separated out rather than folded
// in: see TestCallIndirectComparisonRecGroupBoundary, which asserted a known-wrong verdict and has
// flipped.
//
// **`RecStart`/`RecLen` are set on every fixture, and a fixture that omitted them would not be a
// weaker test but a vacuous one.** A deftype is a whole rec group plus the member's ordinal
// (`binary.CompType`'s own note on why those two fields are the type's identity), so
// `MatchDefType`'s equality disjunct compares group length and ordinal *before* it compares
// anything structural (`internal/validate/match.go:379`). Leaving both fields zero gives every type
// a group of length zero, the member loop never runs, and the disjunct degenerates to `got ==
// want` — the fixtures would then agree with these assertions for a reason having nothing to do
// with what they assert. A bare declaration is a singleton group (`RecStart` = own index, `RecLen`
// = 1, `decode.ml:276`'s `RecT [st]`), which is what `labelRecGroup` stamps and what these set.
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
func TestCallIndirectComparisonDeclaredSupertypeWalk(t *testing.T) {
	// type-subtyping.wast:596-599/602-608's own shape: $t1 non-final, $t2 final, both `(func)`.
	finalityDiffers := &binary.Module{
		Types: []binary.CompType{
			// idx 0: $t1 = (sub (func)), its own singleton rec group
			{Kind: binary.CompFunc, Final: false, RecStart: 0, RecLen: 1},
			// idx 1: $t2 = (sub final (func)), its own singleton rec group
			{Kind: binary.CompFunc, Final: true, RecStart: 1, RecLen: 1},
		},
	}
	if validate.MatchDefType(finalityDiffers, 0, finalityDiffers, 1) {
		t.Error("MatchDefType($t1, $t2) = true, want false: identical comptype and supertype " +
			"list but opposite finality must not match — the defect #164 left open " +
			"(type-subtyping.wast:602,610)")
	}
	// And two declarations that agree on everything, Final included, ARE the same type — the
	// isorecursive-canonicalization case above, stated as its own row so the finality check is
	// not mistaken for a general nominal-identity requirement.
	finalityAgrees := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: true, RecStart: 0, RecLen: 1},
			{Kind: binary.CompFunc, Final: true, RecStart: 1, RecLen: 1},
		},
	}
	if !validate.MatchDefType(finalityAgrees, 0, finalityAgrees, 1) {
		t.Error("MatchDefType(finalityAgrees 0, 1) = false, want true: two declarations " +
			"agreeing on finality, comptype, and (empty) supertypes canonicalize to the same " +
			"type under the reference's own structural-equivalence semantics")
	}

	// A type declared as its own supertype's subtype: idx 1 is `(sub 0 (func))`, so 1's own
	// declared-supertype walk (disjunct 3) should find that 1 matches 0 — the sub-is-a direction,
	// `call_indirect`'s own use (the callee's *actual* type walks its own supertypes looking for
	// the *declared* type at the call site).
	subOf := &binary.Module{
		Types: []binary.CompType{
			// idx 0: (func), the supertype
			{Kind: binary.CompFunc, Final: true, RecStart: 0, RecLen: 1},
			// idx 1: (sub 0 (func)) — a cross-group supertype reference, which stays an
			// absolute index under rolling and so is compared as a definition rather than
			// as an ordinal.
			{Kind: binary.CompFunc, Final: true, Supertypes: []uint32{0}, RecStart: 1, RecLen: 1},
		},
	}
	if !validate.MatchDefType(subOf, 1, subOf, 0) {
		t.Error("MatchDefType(subOf 1, subOf 0) = false, want true: idx 1 declares idx 0 as its " +
			"supertype, so a call site declaring idx 0 must accept a callee actually typed idx 1")
	}
	// And the reverse direction must NOT hold: declaring a supertype does not make the supertype
	// itself walk down to its subtypes (subtyping is not symmetric).
	if validate.MatchDefType(subOf, 0, subOf, 1) {
		t.Error("MatchDefType(subOf 0, subOf 1) = true, want false: a supertype does not match " +
			"by declaring itself as its own subtype's subtype — subtyping is directional")
	}

	// The pre-existing MVP case: two functypes with the same params/results and no `sub` at all
	// (Final's zero-ish default true, no declared supertypes) — must still compare equal, unchanged
	// by 0019's widening and unchanged by 0042's replacement of the whole reduction.
	plain := &binary.Module{
		Types: []binary.CompType{
			{
				Kind: binary.CompFunc, Final: true, RecStart: 0, RecLen: 1,
				Func: binary.FuncType{Params: []binary.ValType{binary.I32}},
			},
			{
				Kind: binary.CompFunc, Final: true, RecStart: 1, RecLen: 1,
				Func: binary.FuncType{Params: []binary.ValType{binary.I32}},
			},
		},
	}
	if !validate.MatchDefType(plain, 0, plain, 1) {
		t.Error("MatchDefType(plain 0, plain 1) = false, want true: two identical declared " +
			"types with no sub involved must keep comparing equal — the pre-0019 MVP case, " +
			"which neither the widening nor the deletion may regress")
	}
}

// TestCallIndirectComparisonDeclaredSupertypeWalkFalsification is the birth requirement (CLAUDE.md
// — "a control isn't born until it has been watched die") for the finality row above, and it is
// **re-pointed rather than retired**, which is the harder half of what 0042 asked of itself.
//
// It used to call `structFuncTypeEqual` — the pre-0019 reduction, deleted with the rest of the
// second comparator — and show that reduction *agreeing* on the finality-differing pair, which is
// what made the row above assert something. Deleting the function removed the probe's callee, and
// **a falsification retired because the thing it falsifies moved is how a guard dies quietly**
// (Scott, on the #478 review). So the premise is asserted directly instead: every field pure
// structural equality could see is compared here, in this test, and shown identical — and then
// `MatchDefType` is shown to disagree anyway.
//
// That is a stronger probe than the deleted call rather than a substitute for it, because it no
// longer needs a second implementation to exist in order to say what the surviving one adds. The
// list of fields is the whole of what the deleted `compTypeEqual`/`structFuncTypeEqual` pair
// compared — kind, both functype sides, the field list, the declared supertype list — so if a
// future reader wants to know what "structure alone" meant, it is this expression and not a
// citation to a function that is gone.
func TestCallIndirectComparisonDeclaredSupertypeWalkFalsification(t *testing.T) {
	mod := &binary.Module{
		Types: []binary.CompType{
			{Kind: binary.CompFunc, Final: false, RecStart: 0, RecLen: 1},
			{Kind: binary.CompFunc, Final: true, RecStart: 1, RecLen: 1},
		},
	}
	a, b := &mod.Types[0], &mod.Types[1]

	if a.Kind != b.Kind ||
		!slices.Equal(a.Func.Params, b.Func.Params) ||
		!slices.Equal(a.Func.Results, b.Func.Results) ||
		!slices.Equal(a.Fields, b.Fields) ||
		!slices.Equal(a.Supertypes, b.Supertypes) {
		t.Fatal("the falsification premise requires the two comptypes to be identical in " +
			"everything structural equality can see (kind, params, results, fields, declared " +
			"supertypes); they are not, so this test is no longer probing what it claims")
	}
	// Identical by that measure, and still not the same type — `Final` differs, which the deleted
	// reduction's innermost comparison did not look at and which `MatchDefType` compares as part
	// of the rolled subtype.
	if validate.MatchDefType(mod, 0, mod, 1) {
		t.Error("MatchDefType(mod, 0, 1) = true, want false: the pair is structurally identical " +
			"by the pre-0019 measure asserted just above, so a relation that reduced to " +
			"structural equality would say true here — the row above would then be asserting " +
			"nothing")
	}
}

// TestCallIndirectComparisonRecGroupBoundary asserts the verdict the reference gives on
// `type-subtyping.wast`'s M10/M11 pair, and **the flip is this test's whole content.**
//
// Its predecessor (`TestSameFuncTypeCorpusScope`, which no longer exists — 0042 deleted the
// function it was named for and this test is its replacement) asserted the *wrong* answer
// deliberately, as a
// documented false positive: `matchDeftype` compared the exporter's `$f21` and the importer's
// `$f11` as isolated CompTypes, found them identical, and reported a match the reference refuses
// (`assert_unlinkable`, "incompatible import type"). Its error message named its own successor
// condition — *"if this assertion starts failing, a rec-group fix landed … and this test should
// flip to the correct 'false'"* — and 0042 is that landing, by replacement rather than by repair.
//
// **The mechanism is subtler than "different absolute index, same shape", and getting that wrong
// was the predecessor's own first draft.** Deftype identity is over the *whole rec group*, not over
// one member's declared-supertype list in isolation: `roll_rectype` (types.ml:255-261) converts an
// *intra-group* supertype reference to a group-relative `Rec i`, while a *cross-group* reference
// stays an absolute `Idx`. M10's exporter declares `$f21`/`$f22` as their own two-member group,
// where `$f22`'s supertype `$f11` is defined in an *earlier* group — cross-group, staying `Idx`.
// The importer's `$f11`/`$f12` are one group where `$f12`'s supertype `$f11` is its own group's
// first member — intra-group, becoming `Rec 0`. So the two groups' canonical shapes genuinely
// differ, even though the *specific member being compared* (`$f21`, bare, no supertypes) is
// byte-for-byte identical to the importer's `$f11` in isolation.
//
// That is exactly what `RecStart`/`RecLen` on these fixtures buy, and it is the reason the
// assertion could flip without anyone touching the comparison at this call site: `MatchDefType`
// walks both groups member by member (`match.go:384`) and `sameRef` asks, of each supertype
// reference, whether it points inside its own group — one in, one out is a mismatch
// (`match.go:443-449`). The old reduction had nothing to ask that question with.
//
// # Birth requirement, and the mutation that did not fire
//
// Two mutations were run, each over the whole `internal/interp` package and over the all-on corpus
// lane, and **the first one was the wrong line**:
//
//   - `sameDefType`'s ordinal-and-group-length condition (`match.go:379-381`) replaced by `if
//     false`: `internal/interp` stays green — this test does **not** fire. It cannot: M10/M11's two
//     groups are both length 2 and the two compared members are both ordinal 0, so that condition
//     is satisfied either way and never decides this fixture. The corpus does see it (all-on
//     7 → 11 fails), so the condition is covered — just not here.
//   - the cross-group refusal (`match.go:445-447`, with the intra-group arm's guard widened so the
//     variable stays live) removed, letting a mismatched-membership pair fall through to structural
//     comparison: this test fails with the message below, and it is the **only** failure in
//     `internal/interp`. The corpus goes 7 → 18.
//
// So the birth requirement is discharged against `sameRef`, and the record of the mutation that
// failed to fire is kept rather than dropped — **a mutation that does not kill is a measurement**,
// and here it measures that the condition a doc comment finds easiest to name is not the condition
// the fixture turns on. `RecStart`/`RecLen` are still load-bearing on these fixtures: they are what
// `recScope` is built from, and it is the scopes, not the length check, that make `sameRef`'s
// question answerable at all.
//
// **The risk this test was left standing for is discharged, not retired.** #368 had already moved
// the *linker* onto this relation, which is why `type-subtyping.wast:752` passed while the gap
// remained; the gap's remaining consumers were `call_indirect` and the cast arms, and they now use
// the same relation. So the direct assertion stays — as the instrument that says the consumers
// really did move, since no corpus row reaches this shape through them.
func TestCallIndirectComparisonRecGroupBoundary(t *testing.T) {
	exporter := &binary.Module{
		Types: []binary.CompType{
			// idx 0/1: $f11, $f12 — one rec group, $f12's supertype intra-group.
			{Kind: binary.CompFunc, Final: false, RecStart: 0, RecLen: 2},
			{Kind: binary.CompFunc, Final: false, Supertypes: []uint32{0}, RecStart: 0, RecLen: 2},
			// idx 2/3: $f21, $f22 — their own rec group; $f22's supertype is idx 0, a
			// CROSS-group reference (type-subtyping.wast:748).
			{Kind: binary.CompFunc, Final: false, RecStart: 2, RecLen: 2},
			{Kind: binary.CompFunc, Final: false, Supertypes: []uint32{0}, RecStart: 2, RecLen: 2},
		},
	}
	importer := &binary.Module{
		Types: []binary.CompType{
			// idx 0/1: $f11, $f12 — one rec group, $f12's supertype intra-group.
			{Kind: binary.CompFunc, Final: false, RecStart: 0, RecLen: 2},
			{Kind: binary.CompFunc, Final: false, Supertypes: []uint32{0}, RecStart: 0, RecLen: 2},
		},
	}
	// The corpus's actual question (type-subtyping.wast:752-757): does the exporter's export
	// (typed $f21, idx 2) match the importer's declared type ($f11, idx 0)? The reference says
	// no, because the two rec groups' rolled shapes differ per the mechanism above.
	if validate.MatchDefType(exporter, 2, importer, 0) {
		t.Error("MatchDefType(exporter $f21, importer $f11) = true, want false: $f21's group " +
			"has a sibling whose supertype is a cross-group reference while $f11's group has a " +
			"sibling whose supertype is intra-group, so the two rolled forms differ and the " +
			"reference refuses the import (type-subtyping.wast:752, assert_unlinkable). A true " +
			"here means the rec-group facts stopped reaching this comparison — which is what the " +
			"predecessor of this test asserted, deliberately, for as long as call_indirect and " +
			"the cast arms had their own comparator")
	}
}
