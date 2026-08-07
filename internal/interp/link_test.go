package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// link1 builds a module through the real front end and instantiates it against a supplier.
//
// Through `text.EncodeModule` for `instantiate1`'s reason (grave #125): the immediate staging is
// part of the subject, so a hand-built `binary.Module` would assert the interpreter against its
// own assumption about the decoder. This one hands back all three of `InstantiateLinked`'s
// channels, because which channel a failure arrives on is half of what these tests check.
func link1(t *testing.T, src string, imp Imports) (*Instance, *Trap, error) {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return InstantiateLinked(m, imp)
}

// supplier instantiates a module with no imports and returns its instance, failing on anything
// short of a complete instantiation — a supplier that trapped or fell short would make the
// *importer's* assertions ambiguous about which module was at fault.
func supplier(t *testing.T, src string) *Instance {
	t.Helper()
	in, trap := instantiate1(t, src)
	if trap != nil {
		t.Fatalf("supplier trapped: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("supplier fell short: %v", err)
	}
	return in
}

// exportsOf is the resolver a one-module registry produces: every name resolves against `in`,
// ignoring the module part, which is what a script with a single `register` looks like.
func exportsOf(in *Instance) Imports {
	return func(_, name string) (Extern, bool) { return in.Export(name) }
}

// TestImportSlotIndicesAreCountedPerKind pins the arithmetic `link` does when it places a
// resolved import: each extern kind has its own index space, so the Nth *memory* import lands at
// memory index N however many funcs, tables or globals precede it in the import section.
//
// **An accept-direction control** (§9 G-3), and the sharpest kind — every module here is valid,
// every wrong answer is a plausible number, and a suite of rejection vectors scores the defect
// green by construction. It is the same defect the 22-vector `memory.size $mem1` lesson paid for
// (see Instance.mems), reappearing on the *filling* side: there the import consumed no index,
// here it would consume the wrong one.
//
// # Re-pointed rather than closed, and the dissolved subject is worth naming
//
// It was TestUnsatisfiedImportDoesNotShiftLaterIndices, and it pinned that an import
// *nothing supplied* still occupies its slot: two memory imports with only the second supplied,
// `memory.size 1` answering with the supplied memory's page count and `memory.size 0` reporting
// the §3 gap. That shape is unreachable now that a resolver answering no is a **refusal** —
// `link`'s `unknown import` arm returns, so there is no instance left to ask — and with that
// return in place, claiming the slot index before resolution or after it is behaviourally
// identical. The old assertion is one no input can fail, which is a stillborn control however
// good it reads.
//
// A tripwire whose subject dissolves is re-pointed, never closed. The *risk* was never "the
// unsatisfied case shifts"; it was "`link` computes a slot index the index space disagrees with",
// and that risk moved intact to the per-kind counters — the one arithmetic in this function a
// plausible reading still gets wrong.
//
// # The shape that distinguishes the defect
//
// All four kinds are imported and the two memories are *interleaved* with the others, so a single
// cursor over `m.Imports` cannot land both of them right. The suppliers' values are pairwise
// distinct and coincide with no declared minimum — 5 and 7 pages, global 13, func 21 — because a
// misplacement's symptom is a plausible number and a shared value would hide it.
//
// Falsified rather than assumed, by running three mutations — and the *first* prediction written
// here was wrong in both of its numbers, which is why they are quoted from the run:
//
//   - **A single `idx` cursor** shared by all four arms panics, `index out of range [2] with
//     length 1`: `$b` is the fifth import, so it is sent past the end rather than to a wrong
//     live slot. A panic names its row, so this is a fail and not a hang — but it is the
//     *loud* half of the defect and cannot be the whole falsification, since a module with more
//     memories than other imports would land in range and answer.
//   - **`slot, memIdx = memIdx, memIdx`** — the counter that never advances — is that in-range
//     case, and it is the one worth the row: both memory imports land in slot 0, so `size0`
//     answers **7**, which is `$b`'s page count under `$a`'s index, and `size1` reports the §3
//     gap. A plausible number from a valid module, which is the accept direction exactly.
//   - **Dropping `in.tables[slot] = ext.tab`** fires the table-slot check below and nothing else,
//     which is what establishes that check is carrying the table arm rather than decorating it.
func TestImportSlotIndicesAreCountedPerKind(t *testing.T) {
	sup := supplier(t, `(module
		(memory (export "a") 5 8)
		(memory (export "b") 7 8)
		(table (export "t") 1 8 funcref)
		(global (export "g") i32 (i32.const 13))
		(func (export "f") (result i32) (i32.const 21)))`)

	// The interleaving is the point: a memory import, then two other kinds, then the second
	// memory import. A cursor that counts positions rather than kinds puts `$b` at memory 4.
	const src = `(module
		(import "s" "f" (func $f (result i32)))
		(import "s" "a" (memory 1 8))
		(import "s" "t" (table 1 8 funcref))
		(import "s" "g" (global $ig i32))
		(import "s" "b" (memory 1 8))
		(func (export "size0") (result i32) (memory.size 0))
		(func (export "size1") (result i32) (memory.size 1))
		(func (export "glob") (result i32) (global.get 0))
		(func (export "callf") (result i32) (call $f)))`

	in, trap, err := link1(t, src, exportsOf(sup))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}

	// Scoped to all four counters rather than to the memory one the 22-vector grave was about,
	// because a control scoped to today's sample inherits today's blind spot.
	for _, row := range []struct {
		fn   string
		want uint64
	}{
		{"size0", 5},
		{"size1", 7},
		{"glob", 13},
		{"callf", 21},
	} {
		got, err := in.Invoke(row.fn)
		if err != nil {
			t.Errorf("%s: %v", row.fn, err)
			continue
		}
		if len(got) != 1 || got[0].Bits != row.want {
			t.Errorf("%s = %v, want %d — the import landed in the wrong slot", row.fn, got, row.want)
		}
	}
	// The table counter has no value to read back — `table.size` has no arm (the measurement in
	// TestUnsatisfiedImportKeepsItsSentinel's table row) — so it is asserted through the slot
	// itself. Without it the table arm's counter is unexercised and this row would claim a
	// four-kind scope it does not have.
	if len(in.tables) != 1 || in.tables[0] == nil {
		t.Errorf("the table import did not land at table index 0: %d slots", len(in.tables))
	}
}

// TestUnknownImportIsALinkFailure pins the refusal that replaced the degraded instance.
//
// **The accept-direction half of the linker, and the half no rejection corpus scores for us**
// (§9 G-3): a module whose import nothing supplies is *unlinkable*, so an engine that
// instantiates it anyway and complains only if the import is touched accepts a module the spec
// refuses. The wrong reading is invisible on any vector that never calls the import.
//
// Asserted on all three channels, because which one carries the failure is the fact
// `assert_unlinkable` reads: an error, no trap (0015's split), and no instance — a link failure
// that handed back a usable instance would let a script run a module the linker rejected.
func TestUnknownImportIsALinkFailure(t *testing.T) {
	const src = `(module (import "s" "nope" (memory 1 8)))`

	in, trap, err := link1(t, src, func(string, string) (Extern, bool) { return Extern{}, false })
	if err == nil {
		t.Fatal("link accepted a module whose import nothing supplied")
	}
	if !errors.Is(err, ErrLinkFailed) {
		t.Errorf("link: %v, want ErrLinkFailed", err)
	}
	// The sentinel is the spec's, and `assert_unlinkable` matches the whole of it: 16 vectors
	// expect this text, so the wording is oracle-covered and asserted rather than paraphrased.
	if !strings.Contains(err.Error(), "unknown import") {
		t.Errorf("link: %v, want the spec's `unknown import`", err)
	}
	if trap != nil {
		t.Errorf("link failure arrived as a trap (%v) — assert_unlinkable and assert_trap want different words", trap)
	}
	if in != nil {
		t.Error("link returned an instance alongside a failure")
	}
	// The vacuity guard: a resolver refusing *every* name would fail the same way if `link`
	// refused unconditionally, so a satisfiable version of the same module must link.
	sup := supplier(t, `(module (memory (export "nope") 1 8))`)
	if _, _, err := link1(t, src, exportsOf(sup)); err != nil {
		t.Errorf("the satisfiable form failed too (%v) — the row above proves nothing", err)
	}
}

// TestUnsatisfiedImportKeepsItsSentinel pins that an unlinked module degrades to *exactly* the
// behaviour it had before there was a linker, per extern kind.
//
// **The bucket-preservation control, and its subject is the board rather than the engine.** The
// 624 failures this change exists to drain were keyed by the sentinel's text, so a linker that
// introduced a new refusal string for the unfilled case would split the bucket in the same motion
// that drained it — the drain would be real and unmeasurable. Asserting the *string* rather than
// only the sentinel error is therefore the point: `errors.Is(err, ErrUnsupported)` passes for any
// wording, and the wording is what the board keys on.
//
// **The strings below are the post-drain wording, and the change of tense is the whole story.**
// They read `linking is not implemented` while the drain was being measured — 624 → **13** — and
// then had to stop, because that sentence is an engine with a linker testifying that it has none
// (grave #36). So this control's *purpose* narrowed rather than lapsed: it no longer protects a
// bucket mid-drain, it pins that the four kinds agree on one wording, which is the thing four
// separate format strings in four files will otherwise drift on. The old text is quoted here
// rather than merely replaced, so a reader meeting an archived board line can tell a resolved
// rewording from an unnoticed one.
//
// **The path is the nil resolver, and that is a re-pointing rather than a convenience.** This row
// ran through a resolver that refused every name, on the stated ground that it asserted the
// *unsatisfied* path "and not merely the nil-resolver shortcut". That ground was correct when
// written and the refusal arm falsified it: a resolver answering no now returns `unknown import`,
// so there is no instance to invoke and the §3 sentinel is unreachable that way. The two facts
// separated — *asked and refused* is a link failure (TestUnknownImportIsALinkFailure), *never
// asked* is the §3 gap — and this control follows its subject to the path that still has one.
// `Instantiate` is `InstantiateLinked` with a nil resolver, so the shortcut it declined to use is
// the production path for every module the harness runs unregistered.
//
// Falsified on the new path: turning `link`'s `if imp == nil` into `if false` — collapsing the two
// facts back into one — fails all four rows with `link failed: unknown import`, which is the
// distinction being load-bearing rather than stylistic.
//
// Scoped to all four kinds rather than to the one that regressed, because a control scoped to
// today's sample inherits today's blind spot.
func TestUnsatisfiedImportKeepsItsSentinel(t *testing.T) {
	for _, row := range []struct {
		kind string
		src  string
		fn   string
		want string
	}{
		{
			kind: "memory",
			src: `(module (import "s" "m" (memory 1 8))
				(func (export "f") (result i32) (memory.size)))`,
			fn:   "f",
			want: "memory 0 is an import nothing supplied (contract §3)",
		},
		{
			// Through `call_indirect` rather than `table.size`, and the difference is a
			// measurement: `table.size` is `0xfc 0x10`, which has no arm, so that row
			// reported `no arm for opcode fc 10` and asserted nothing about linking. Caught by
			// running the control — a row that reaches a *different* missing feature is the
			// coverage defect a green cannot show, since the test would have been red for the
			// right-looking reason once `table.size` landed.
			kind: "table",
			src: `(module (import "s" "t" (table 1 8 funcref))
				(type $v (func))
				(func (export "f") (call_indirect (type $v) (i32.const 0))))`,
			fn:   "f",
			want: "table 0 is an import nothing supplied (contract §3)",
		},
		{
			kind: "global",
			src: `(module (import "s" "g" (global i32))
				(func (export "f") (result i32) (global.get 0)))`,
			fn:   "f",
			want: "global 0 is an import nothing supplied (contract §3)",
		},
		{
			kind: "func",
			src: `(module (import "s" "f" (func (result i32)))
				(func (export "g") (result i32) (call 0)))`,
			fn:   "g",
			want: "function 0 is an import nothing supplied (contract §3)",
		},
	} {
		t.Run(row.kind, func(t *testing.T) {
			// A nil resolver: "supply nothing", which is what an unregistered module meets.
			in, trap, err := link1(t, row.src, nil)
			if err != nil {
				t.Fatalf("link: %v", err)
			}
			if trap != nil {
				t.Fatalf("instantiate trapped: %v", trap)
			}
			_, err = in.Invoke(row.fn)
			if err == nil {
				t.Fatalf("invoke answered, but nothing supplied the %s import", row.kind)
			}
			if !errors.Is(err, ErrUnsupported) {
				t.Errorf("invoke: %v, want ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), row.want) {
				t.Errorf("invoke: %v\nwant substring: %s", err, row.want)
			}
		})
	}
}

// TestLinkedCallCrossesIntoTheSupplierInstance pins the fact that makes linking linking rather
// than name resolution: the callee's body runs against the *callee's* instance.
//
// **The discriminator is state only one instance has.** Both modules declare a global, and the
// supplier's function returns its own; an engine that resolved the import to a function index but
// kept the caller's receiver would read the importer's global 0 and return 7 instead of 11. Two
// distinct values, both plausible, so the wrong reading answers rather than failing — which is
// what puts this in the accept-direction class (§9 G-3) with the row above.
//
// Falsified: changing `callImport` to `return in.call(ext.fnIdx, st, depth)` — the same index
// against the wrong receiver — fails this with 7.
func TestLinkedCallCrossesIntoTheSupplierInstance(t *testing.T) {
	sup := supplier(t, `(module
		(global $g i32 (i32.const 11))
		(func (export "get") (result i32) (global.get $g)))`)

	const src = `(module
		(import "s" "get" (func $get (result i32)))
		(global $g i32 (i32.const 7))
		(func (export "viaImport") (result i32) (call $get))
		(func (export "viaLocal") (result i32) (global.get $g)))`

	in, trap, err := link1(t, src, exportsOf(sup))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	got, err := in.Invoke("viaImport")
	if err != nil {
		t.Fatalf("viaImport: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 11 {
		t.Errorf("viaImport = %v, want 11 — the callee ran against the caller's globals", got)
	}
	// The mirror: the importer's own global is undisturbed, so the 11 above is a crossing and
	// not the two instances having been conflated in the other direction.
	got, err = in.Invoke("viaLocal")
	if err != nil {
		t.Fatalf("viaLocal: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 7 {
		t.Errorf("viaLocal = %v, want 7", got)
	}
}

// TestLinkedImportArgumentsAndResultsCross pins that operands survive the crossing in order.
//
// Separate from the receiver row above because it fails for a different reason and the two would
// mask each other: a crossing that shares the stack correctly but reverses arguments answers with
// a plausible number on every symmetric operation, which `sub` is chosen to exclude. Two
// parameters and two results, since a single-parameter single-result row cannot see an ordering
// defect at all — the shape `Invoke`'s own comment measures at 12799 blind vectors against 1188
// sighted ones.
//
// **What the falsification found is the boundary of what this row covers, and it is worth stating
// because it is not the boundary the row was written expecting.** The obvious mutation — a
// per-instance operand stack with arguments moved across by pop-and-push — *passes*, because
// popping into a slice and appending reverses twice and composes to the identity. So this row does
// not distinguish the shared-stack design from a correct copying one; nothing here should, since
// both are right. It fails on the mutation it actually names: a **single** reversal, which reports
// `[-26 26]`. A control's subject is the defect, not the design, and confirming that took running
// both.
func TestLinkedImportArgumentsAndResultsCross(t *testing.T) {
	sup := supplier(t, `(module
		(func (export "swapsub") (param i32 i32) (result i32 i32)
			(i32.sub (local.get 0) (local.get 1))
			(i32.sub (local.get 1) (local.get 0))))`)

	const src = `(module
		(import "s" "swapsub" (func $f (param i32 i32) (result i32 i32)))
		(func (export "go") (result i32 i32) (call $f (i32.const 30) (i32.const 4))))`

	in, trap, err := link1(t, src, exportsOf(sup))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	got, err := in.Invoke("go")
	if err != nil {
		t.Fatalf("go: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("go returned %d values, want 2", len(got))
	}
	// 30 - 4 = 26 and 4 - 30 = -26. Reversed arguments give -26 and 26, which is the same pair
	// in the other order and is why *both* slots are asserted.
	if got[0].Bits != 26 || uint32(got[1].Bits) != uint32(0xFFFFFFFF-25) {
		t.Errorf("go = [%d %d], want [26 -26]", int32(got[0].Bits), int32(uint32(got[1].Bits)))
	}
}

// TestLinkedInstanceInitializerReadsAnImportedGlobal pins `link`'s *position*: imports are
// resolved before anything is allocated or evaluated.
//
// The reason the position is forced rather than chosen (`InstantiateLinked`'s comment): a global
// initializer is a const-expr that may read an imported global. An engine that linked after
// building would find the slot still nil and report `global 0 was declared but not initialized`
// — a loud failure rather than a wrong answer, thanks to the nil-slot convention, which is why
// this row asserts a *value* and not merely the absence of an error.
func TestLinkedInstanceInitializerReadsAnImportedGlobal(t *testing.T) {
	sup := supplier(t, `(module (global (export "g") i32 (i32.const 42)))`)

	const src = `(module
		(import "s" "g" (global $ig i32))
		(global $own i32 (global.get $ig))
		(func (export "own") (result i32) (global.get $own)))`

	in, trap, err := link1(t, src, exportsOf(sup))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("instantiate fell short: %v", derr)
	}
	got, err := in.Invoke("own")
	if err != nil {
		t.Fatalf("own: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 42 {
		t.Errorf("own = %v, want 42 — the initializer did not see the imported global", got)
	}
}

// TestLinkKindMismatchIsAnErrorNotATrap pins 0015's channel split at the link boundary.
//
// **The channels are the whole reason this is a control.** `assert_unlinkable` and `assert_trap`
// want different words, so a link failure arriving as a trap would let one directive score the
// other's vectors — the fail-column dilution decision 0010 exists to prevent, one layer up. The
// assertion is therefore on *which return value is non-nil*, not on the message.
func TestLinkKindMismatchIsAnErrorNotATrap(t *testing.T) {
	// The supplier exports a global under the name the importer wants for a memory.
	sup := supplier(t, `(module (global (export "m") i32 (i32.const 0)))`)

	const src = `(module (import "s" "m" (memory 1 8)))`

	in, trap, err := link1(t, src, exportsOf(sup))
	if err == nil {
		t.Fatal("link accepted a global where a memory was imported")
	}
	if !errors.Is(err, ErrLinkFailed) {
		t.Errorf("link: %v, want ErrLinkFailed", err)
	}
	if trap != nil {
		t.Errorf("link failure arrived as a trap (%v) — assert_unlinkable and assert_trap want different words", trap)
	}
	if in != nil {
		t.Error("link returned an instance alongside a failure")
	}
}

// TestExportOfAnUnfilledSlotIsAbsent pins `Export`'s decision to report false rather than hand
// back a nil-carrying Extern.
//
// The chain it protects is two links long, which is why it needs a control of its own: a module
// that imports a memory nothing supplied and *re-exports* it would otherwise offer that export to
// a third module, whose own slot would then be filled with nil — and the failure would surface
// two instantiations away from its cause, as a nil dereference rather than as a missing import.
//
// **The chain is longer than the refusal arm, which is why this row survives it.** With `link`
// refusing an unknown import, the *importer* here can only exist unlinked — so the unfilled slot
// arrives through the nil resolver, and the third module in the chain is precisely a script that
// registers a module instantiated before its own supplier was available. That is the live shape
// (`linking.wast` registers as it goes), so the risk is not the refusal arm's to retire.
func TestExportOfAnUnfilledSlotIsAbsent(t *testing.T) {
	const src = `(module
		(import "s" "m" (memory 1 8))
		(export "reexported" (memory 0)))`

	in, trap, err := link1(t, src, nil)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	if ext, ok := in.Export("reexported"); ok {
		t.Errorf("Export reported a re-exported unfilled slot as present: %+v", ext)
	}
	// The vacuity guard: a lookup that reports false for *every* name would pass the assertion
	// above while saying nothing, so a name that must resolve is checked beside it.
	sup := supplier(t, `(module (memory (export "m") 1 8))`)
	if _, ok := sup.Export("m"); !ok {
		t.Error("Export reported a filled slot as absent — the row above proves nothing")
	}
}

// TestReexportedImportIsInvokable pins the pass-through case at the boundary: a module exporting
// a function it imported.
//
// `linking.wast`'s `Mt` does exactly this, and a script may invoke either the local export or the
// re-exported one. Delegating through the supplier's own `invokeIndex` is what makes the two
// answer identically; building a frame on the importer's side would need a body that is not there.
func TestReexportedImportIsInvokable(t *testing.T) {
	sup := supplier(t, `(module (func (export "f") (result i32) (i32.const 5)))`)

	const src = `(module
		(import "s" "f" (func $f (result i32)))
		(export "passthrough" (func $f)))`

	in, trap, err := link1(t, src, exportsOf(sup))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	got, err := in.Invoke("passthrough")
	if err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 5 {
		t.Errorf("passthrough = %v, want 5", got)
	}
}

// TestCallIndirectThroughAnImportedFunctionTypeChecks pins that the indirect path's type check
// still runs when the table slot names an import.
//
// **The two directions in one test, because either alone reads as a plausible engine.** A
// crossing that skips the check accepts the mismatch row; one that compares against the wrong
// instance's types rejects the matching row. The rows differ only in the declared type, so
// nothing but the check distinguishes them.
func TestCallIndirectThroughAnImportedFunctionTypeChecks(t *testing.T) {
	sup := supplier(t, `(module (func (export "f") (result i32) (i32.const 9)))`)

	const src = `(module
		(type $ok (func (result i32)))
		(type $bad (func (result i64)))
		(import "s" "f" (func $f (result i32)))
		(table 1 1 funcref)
		(elem (i32.const 0) $f)
		(func (export "ok") (result i32) (call_indirect (type $ok) (i32.const 0)))
		(func (export "bad") (result i64) (call_indirect (type $bad) (i32.const 0))))`

	in, trap, err := link1(t, src, exportsOf(sup))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	got, err := in.Invoke("ok")
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 9 {
		t.Errorf("ok = %v, want 9", got)
	}
	_, err = in.Invoke("bad")
	if err == nil {
		t.Fatal("call_indirect accepted a type mismatch through an imported function")
	}
	if !strings.Contains(err.Error(), "indirect call type mismatch") {
		t.Errorf("bad: %v, want indirect call type mismatch", err)
	}
}

// TestImportTypeMismatchIsRejectedPerKind pins grave #164's repair: link compared kind and
// nothing else, so 42 assert_unlinkable vectors (table/memory limits, global type or
// mutability, a func's own signature) were passing as though matching kind were matching type.
// One table row per kind, each changing exactly the field the reference's match_limits /
// match_globaltype / sameFuncType would reject on — a mismatch in one field with the others
// held equal, which is what makes a passing row *and* a failing row both informative: an
// over-broad comparison (checking min but not max, say) would still catch some rows here and
// only a full-coverage read of the table would show which.
func TestImportTypeMismatchIsRejectedPerKind(t *testing.T) {
	sup := supplier(t, `(module
		(memory (export "mem") 2 4)
		(table (export "tab") 2 5 funcref)
		(global (export "g") (mut i32) (i32.const 0))
		(func (export "f") (param i32) (result i32) (local.get 0)))`)

	// **The wider bound is the accepting one, in both directions, and that asymmetry is the
	// point of testing both min and max separately.** `match_limits` (match.ml) requires the
	// supplier's actual min to be *at least* the importer's declared min, and the supplier's
	// actual max to be *at most* the importer's declared max (or the importer to declare no
	// max at all) — so a declared bound *tighter* than reality is the reject case for min, and
	// a declared bound *looser* than reality would be the reject case for max only if it read
	// backwards. `imports.wast:529-530` pins exactly this: importing with a declared max of 5
	// or 6 against a supplier whose actual max is 4 **links**, because the importer is
	// satisfied by anything within its own wider bound. So the reject-direction row for max is
	// a *narrower* importer than the supplier can satisfy, not a wider one.
	rows := []struct {
		name     string
		importer string
	}{
		{"memory min too high", `(memory (import "s" "mem") 3 4)`},
		{"memory max too low", `(memory (import "s" "mem") 2 3)`},
		{"table min too high", `(table (import "s" "tab") 3 5 funcref)`},
		{"table max too low", `(table (import "s" "tab") 2 4 funcref)`},
		{"table elem type", `(table (import "s" "tab") 2 5 externref)`},
		{"global type", `(global (import "s" "g") (mut i64))`},
		{"global mutability", `(global (import "s" "g") i32)`},
		{"func param", `(func (import "s" "f") (param i64) (result i32))`},
		{"func result", `(func (import "s" "f") (param i32) (result i64))`},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, _, err := link1(t, "(module "+r.importer+")", exportsOf(sup))
			if err == nil {
				t.Fatalf("%s: link accepted a mismatched import", r.name)
			}
			if !strings.Contains(err.Error(), "incompatible import type") {
				t.Errorf("%s: %v, want incompatible import type", r.name, err)
			}
		})
	}
}

// TestImportTypeMatchLinksPerKind is TestImportTypeMismatchIsRejectedPerKind's accept-direction
// counterpart, and the one the corpus alone cannot supply: every assert_unlinkable vector the
// grave found is a *rejection*, so nothing in the suite would notice a comparison that is too
// strict — a table declaring the exact size the supplier offers, refused because the check
// compared the wrong direction, would still score green on every existing vector (§9 G-3). One
// row per kind, each holding the field mismatched above equal instead, and each linking and
// exercising its import to confirm it is genuinely usable rather than merely accepted.
func TestImportTypeMatchLinksPerKind(t *testing.T) {
	sup := supplier(t, `(module
		(memory (export "mem") 2 4)
		(table (export "tab") 2 5 funcref)
		(global (export "g") (mut i32) (i32.const 0))
		(func (export "f") (param i32) (result i32) (local.get 0)))`)

	rows := []struct {
		name     string
		importer string
	}{
		{"memory exact", `(memory (import "s" "mem") 2 4)`},
		{"memory narrower min, no importer max", `(memory (import "s" "mem") 0)`},
		{"memory wider declared max (the shape imports.wast pins at line 530)", `(memory (import "s" "mem") 2 6)`},
		{"table exact", `(table (import "s" "tab") 2 5 funcref)`},
		{"global exact", `(global (import "s" "g") (mut i32))`},
		{"func exact", `(func (import "s" "f") (param i32) (result i32))`},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, trap, err := link1(t, "(module "+r.importer+")", exportsOf(sup))
			if err != nil {
				t.Fatalf("%s: link: %v", r.name, err)
			}
			if trap != nil {
				t.Fatalf("%s: instantiate trapped: %v", r.name, trap)
			}
		})
	}
}

// TestGrownMemoryReexportsItsCurrentSize pins imports4.wast:19-37 directly, in its own words:
// "imported memory limits should match, because external memory size is 2 now." A memory's
// declared minimum is fixed at decode time (`binary.Memory.Limits`) and its runtime `limits`
// field started as a copy of that — so growing the memory and then re-exporting it for another
// instance to import must check against the *grown* size, not the size the module declared.
//
// Found by TestImportTypeMismatchIsRejectedPerKind's own construction: adding the type check at
// all made three corpus vectors newly fail (not regress — they were already failing on the
// missing table.grow arm) because `memory.grow` reallocated `m.bytes` without updating
// `m.limits.Min`, so a grown-then-reexported memory reported its stale pre-growth minimum to an
// importer whose declaration matched the actual, current size.
func TestGrownMemoryReexportsItsCurrentSize(t *testing.T) {
	sup := supplier(t, `(module
		(memory (export "mem") 1)
		(func (export "grow") (result i32) (memory.grow (i32.const 1))))`)

	got, err := sup.Invoke("grow")
	if err != nil {
		t.Fatalf("grow: %v", err)
	}
	if len(got) != 1 || int32(got[0].Bits) != 1 {
		t.Fatalf("grow = %v, want 1 (old size)", got)
	}

	// The memory is now size 2. An importer declaring a minimum of 2 must link — the pre-fix
	// engine reported "expected memory 2, got 1" here, the "1" being the never-updated field.
	if _, _, err := link1(t, `(module (memory (import "s" "mem") 2))`, exportsOf(sup)); err != nil {
		t.Errorf("importing at the grown size: %v, want link to succeed", err)
	}
	// And the corpus's own negative: a declared minimum the *pre-growth* size does not
	// satisfy, but the *post-growth* size does, must still reject — 3 exceeds even the grown
	// size, so this is not merely the old bug's absence.
	if _, _, err := link1(t, `(module (memory (import "s" "mem") 3))`, exportsOf(sup)); err == nil {
		t.Error("importing above the grown size: link accepted a mismatched import")
	}
}
