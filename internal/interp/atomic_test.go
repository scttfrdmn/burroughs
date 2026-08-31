package interp

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// TestAtomicopsCoversTheRegionExactly checks the derivation is total over the generated table.
//
// `buildAtomicops` panics on a row it cannot parse, so a *parse* failure is already a build-time
// event, and that panic covers more than it looks like it does: pointing the accessor at a
// *different* region (0xfc) panics on its first row, because a bulk-memory mnemonic is not an
// atomic one. That was measured, not assumed.
//
// The case it cannot catch is the region answering **empty** — a `prefixRegion` that stopped
// recognising 0xfe, or a table that lost the block. Deriving from an empty list succeeds and
// produces an empty map, every atomic then reports `no arm for opcode fe NN`, and that reads
// exactly like the state before this file existed. So the vacuity guard below is the load-bearing
// half, and it is asserted with `t.Fatal` because the two domain sweeps after it agree perfectly
// when both sides are empty.
//
// The count is pinned beside them: *a floor catches a moved file, never a silent 6% loss.*
func TestAtomicopsCoversTheRegionExactly(t *testing.T) {
	ops := binary.PrefixedRegionOpcodes(0xfe)
	if len(ops) == 0 {
		t.Fatal("the 0xfe region reports no opcodes at all, so every derived-domain assertion " +
			"below would pass vacuously; the table or prefixRegion has moved")
	}
	if len(ops) != 67 {
		t.Errorf("the 0xfe region has %d opcodes, want 67 (4 wait/notify/fence + 14 load/store "+
			"+ 42 rmw + 7 cmpxchg). A changed count is a regenerated table, and the parser in "+
			"atomic.go may not know the new shape", len(ops))
	}
	if len(atomicops) != len(ops) {
		t.Errorf("atomicops has %d rows and the region has %d: the derivation dropped rows "+
			"without panicking", len(atomicops), len(ops))
	}
	for _, op := range ops {
		if _, ok := atomicops[op]; !ok {
			mnemonic, _, _ := binary.PrefixedOp(0xfe, op)
			t.Errorf("fe %#02x (%s) has a table row and no atomicop", op, mnemonic)
		}
	}
	for op := range atomicops {
		if _, _, ok := binary.PrefixedOp(0xfe, op); !ok {
			t.Errorf("atomicops has a row for fe %#02x, which the table does not define", op)
		}
	}
}

// TestAtomicopParsesTheRegionsThreeHardPairs pins the rows where two mnemonics differ in one
// character and the wrong reading produces a plausible value.
//
// Not a transcription of all 67 rows — that is what the corpus does, and it does it better, with
// exact expected values for 66 of them. These are the pairs where a parser bug would be a *silent*
// wrong answer rather than a fail, chosen by that property rather than by being interesting:
//
//   - `i64_atomic_rmw32_u` and `i64_atomic_rmw32_u_cmpxchg`: the suffix strip has to happen before
//     the family word is read, or the second parses as an rmw and computes an unconditional store.
//   - `i32_atomic_rmw` at `RmwAdd` and at `RmwSub`: identical mnemonics, and the operator is the
//     only thing between a sum and a difference.
//   - `memory_atomic_wait64`: an 8-byte compare whose *result* is an i32, which is the one place
//     `is64` does not select the push.
func TestAtomicopParsesTheRegionsThreeHardPairs(t *testing.T) {
	for _, tc := range []struct {
		op   uint32
		want atomicop
	}{
		{0x24, atomicop{kind: atomicRmw, width: 4, is64: true, rmw: rmwAdd}}, // i64.atomic.rmw32.add_u
		{0x4e, atomicop{kind: atomicCmpxchg, width: 4, is64: true}},          // i64.atomic.rmw32.cmpxchg_u
		{0x1e, atomicop{kind: atomicRmw, width: 4, rmw: rmwAdd}},             // i32.atomic.rmw.add
		{0x25, atomicop{kind: atomicRmw, width: 4, rmw: rmwSub}},             // i32.atomic.rmw.sub
		{0x02, atomicop{kind: atomicWait, width: 8, is64: true}},             // memory.atomic.wait64
		{0x01, atomicop{kind: atomicWait, width: 4}},                         // memory.atomic.wait32
		{0x03, atomicop{kind: atomicFence}},                                  // atomic.fence
		{0x12, atomicop{kind: atomicLoad, width: 1}},                         // i32.atomic.load8_u
		{0x1d, atomicop{kind: atomicStore, width: 4, is64: true}},            // i64.atomic.store32
	} {
		mnemonic, _, ok := binary.PrefixedOp(0xfe, tc.op)
		if !ok {
			t.Errorf("fe %#02x is not in the table, so this row asserts nothing", tc.op)
			continue
		}
		if got := atomicops[tc.op]; got != tc.want {
			t.Errorf("fe %#02x (%s): got %+v, want %+v", tc.op, mnemonic, got, tc.want)
		}
	}
}

// atomicGated encodes, decodes under the threads gate, instantiates and invokes `f`.
//
// A local helper rather than `instantiate1`, for `link1Threads`'s reason: an atomic in the source
// needs `binary.Features{Threads: true}` on the *decoder*, and threading a gate set through
// `instantiate1`'s call sites would make every one of them assert something about gates it does not
// care about. It returns the invoke error rather than failing on it, because half of what is under
// test here is *which* modules trap.
func atomicGated(t *testing.T, src string) ([]Value, error) {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	return in.Invoke("f")
}

// TestAtomicAlignmentIsCheckedOnTheEffectiveAddress pins the rule atomic.go's header takes from the
// normative prose, with the vector the corpus does not contain.
//
// **This is the control that makes a choice rather than watching one.** Every atomic in both corpora
// carries a zero static offset, so `ea == i` on all 187 rows and *any* reading of which address is
// aligned scores 297/297. Identical boards are the corpus declining to choose, so the rows below are
// hand-built to separate the readings, and each arm is asked in **both directions** — which is what
// stops the test being satisfiable by an engine that traps on everything or on nothing:
//
//	offset=1, addr=3  → ea 4 aligned      → succeeds   (the dynamic reading would trap: 3)
//	offset=1, addr=4  → ea 5 misaligned   → traps      (the dynamic reading would accept: 4)
//
// **It runs through the front end, not against `checkAlign`.** The first version of this test called
// the predicate directly, which is *a control can test the helper, not the path*: it would keep
// passing if `execFE` stopped calling `checkAlign`, or called it with the offset dropped at one of
// six sites. Going through `text.EncodeModule` → `DecodeModule` → `Instantiate` → `Invoke` also
// means the `offset=1` immediate has to survive the reader and the validator to reach the executor,
// so the vector asserts the rule rather than the arithmetic.
//
// It does not prove the reading correct — the expectations are this project's reading of the prose,
// asserted against this project's own decoder, and no independent oracle is involved. What it buys
// is that the reading is **observable and pinned**: a future change to the alignment base flips a
// named test instead of silently altering behaviour on a population no vector covers.
//
// Six arms, because there are six `checkAlign` call sites and a per-arm sweep is what catches one of
// them being threaded wrongly. `wait32` needs a shared memory (`check_shared` is reached from the
// wait rows and from nothing else) and is asked with a mismatched expected value, so it answers
// not-equal immediately rather than reaching the suspend path #543 tracks.
func TestAtomicAlignmentIsCheckedOnTheEffectiveAddress(t *testing.T) {
	rows := 0
	// Each arm is one instruction with `offset=1`, taking its address from a parameter-free
	// `i32.const` so the two directions differ in exactly one character.
	for _, arm := range []struct {
		name string
		// body renders the instruction sequence for a given address; result is the function's
		// result type, empty for store.
		body   func(addr int) string
		result string
		shared bool
	}{
		{
			name:   "i32.atomic.load",
			body:   func(a int) string { return fmt.Sprintf("(i32.atomic.load offset=1 (i32.const %d))", a) },
			result: "(result i32)",
		},
		{
			name: "i32.atomic.store",
			body: func(a int) string {
				return fmt.Sprintf("(i32.atomic.store offset=1 (i32.const %d) (i32.const 7))", a)
			},
		},
		{
			name: "i32.atomic.rmw.add",
			body: func(a int) string {
				return fmt.Sprintf("(i32.atomic.rmw.add offset=1 (i32.const %d) (i32.const 7))", a)
			},
			result: "(result i32)",
		},
		{
			name: "i32.atomic.rmw.cmpxchg",
			body: func(a int) string {
				return fmt.Sprintf("(i32.atomic.rmw.cmpxchg offset=1 (i32.const %d) (i32.const 0) (i32.const 7))", a)
			},
			result: "(result i32)",
		},
		{
			name: "memory.atomic.notify",
			body: func(a int) string {
				return fmt.Sprintf("(memory.atomic.notify offset=1 (i32.const %d) (i32.const 0))", a)
			},
			result: "(result i32)",
		},
		{
			name: "memory.atomic.wait32",
			body: func(a int) string {
				// expected = 999 against a zero cell, so this returns 1 (not-equal) without
				// suspending; the timeout is irrelevant on that path and is written -1 anyway.
				return fmt.Sprintf(
					"(memory.atomic.wait32 offset=1 (i32.const %d) (i32.const 999) (i64.const -1))", a)
			},
			result: "(result i32)",
			shared: true,
		},
	} {
		for _, dir := range []struct {
			addr          int
			wantUnaligned bool
			dynamicWould  string
		}{
			{
				addr: 3, wantUnaligned: false,
				dynamicWould: "aligning the dynamic address would trap here, because the popped " +
					"operand 3 is not 4-byte aligned even though ea = 3+1 = 4 is",
			},
			{
				addr: 4, wantUnaligned: true,
				dynamicWould: "aligning the dynamic address would accept this, because the popped " +
					"operand 4 is 4-byte aligned even though ea = 4+1 = 5 is not",
			},
		} {
			mem := "(memory 1)"
			if arm.shared {
				mem = "(memory 1 1 shared)"
			}
			src := fmt.Sprintf(`(module %s (func (export "f") %s %s))`,
				mem, arm.result, arm.body(dir.addr))

			_, err := atomicGated(t, src)
			rows++
			gotUnaligned := err != nil && errors.Is(err, trapUnalignedAtomic)
			switch {
			case err != nil && !gotUnaligned:
				t.Errorf("%s addr=%d: failed with an unrelated error %v.\n"+
					"Wanted either success or the unaligned trap; anything else means the vector "+
					"never reached the alignment check and this row asserts nothing about it",
					arm.name, dir.addr, err)
			case gotUnaligned != dir.wantUnaligned:
				t.Errorf("%s addr=%d offset=1: unaligned=%v, want %v.\n%s.\n"+
					"The engine checks `ea = i + memarg.offset` per the proposal's normative prose "+
					"(document/core/exec/instructions.rst, six sites); eval.ml's six check_align "+
					"calls pass the popped operand alone and disagree with it. No corpus vector "+
					"separates them; this row does. If the base is ever re-ruled, invert this "+
					"table rather than deleting it (#546).",
					arm.name, dir.addr, gotUnaligned, dir.wantUnaligned, dir.dynamicWould)
			}
		}
	}
	// The count is printed and floored rather than trusted: an arm table drained to empty, or a
	// `continue` added above, passes every assertion by asking nothing. 6 arms x 2 directions.
	t.Logf("alignment base pinned over %d vectors (6 arms x 2 directions), none of them in either "+
		"corpus", rows)
	if rows != 12 {
		t.Errorf("ran %d vectors, want 12: the arm table or the direction table changed size, and "+
			"a shrunken sweep is a quieter test rather than a passing engine", rows)
	}

	// The trap text has to satisfy the corpus's prefix rule, and the corpus's expectation is
	// shorter than the reference's phrase. Asserted here rather than trusted, because a message
	// trimmed to the assertion would pass the suite and stop matching the day upstream lengthens
	// its expected text.
	const corpusExpects = "unaligned atomic"
	if !strings.HasPrefix(trapUnalignedAtomic.Reason, corpusExpects) {
		t.Errorf("trap reason %q is not prefixed by the corpus's expected %q, so all 45 "+
			"assert_trap rows in atomic.wast would report a wrong-message fail",
			trapUnalignedAtomic.Reason, corpusExpects)
	}
}

// TestAtomicFenceNeedsNoMemory is the 67th row's only witness, and it exists because the corpus
// cannot be one.
//
// `atomic.fence` is the one opcode in the region that no vector in either corpus mentions, and it
// cannot be reached through wat either: its wire form ends in a reserved byte no immediate shape
// writes, so `text.EncodeModule` refuses it (#532). The board therefore scores identically whether
// the fence works, is a no-op for the wrong reason, or is broken — *the shape of what survives names
// the bug*, and here nothing survives because nothing asks.
//
// So the assertion is at the dispatch level, and it is the property that actually distinguishes the
// fence from all 66 others: **it must execute with no memory in the instance at all.** Every other
// row reads a memarg and resolves a memory; the fence's immediate is `immZeroByte`, so `Imm0` holds
// a reserved byte rather than an offset. Deleting `execFE`'s early return sends it through `Memarg`
// and `memoryFor`, which on a memory-less module fails — and on a module *with* a memory would
// silently succeed, which is why the module below deliberately has none. A test with a memory would
// pass under the mutation and prove nothing.
func TestAtomicFenceNeedsNoMemory(t *testing.T) {
	// No `(memory ...)`, deliberately. This is the discriminating condition, not an omission.
	in, trap := instantiate1(t, `(module (func (export "f")))`)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if got, ok := atomicops[0x03]; !ok || got.kind != atomicFence {
		t.Fatalf("fe 0x03 parsed as %+v (present=%v), want kind atomicFence — this test asserts "+
			"nothing if the derivation put the fence somewhere else", got, ok)
	}

	st := &stack{}
	if err := in.execFE(binary.Instr{Prefix: 0xfe, Op: 0x03}, st); err != nil {
		t.Errorf("atomic.fence in a module with no memory: %v.\n"+
			"The fence takes no memarg (`immZeroByte`) and orders nothing in a single-threaded "+
			"engine, so `AtomicFence, vs -> vs, [], NoAction` is the whole arm. An error here "+
			"means it fell through to the memarg path, which read its reserved byte as an offset "+
			"and then looked for a memory that does not exist", err)
	}
	if len(st.num) != 0 || len(st.refs) != 0 {
		t.Errorf("atomic.fence left %d numeric and %d reference slots on the stack, want 0 and 0: "+
			"the reference's arm is `vs -> vs`, so it is a no-op on the operand stack too",
			len(st.num), len(st.refs))
	}
}

// TestAtomicsArePlainWhileTheInterpreterIsSingleThreaded is #542's tripwire.
//
// `atomic.go` implements all 67 rows as plain read-then-write: no lock, no intrinsic, no fence.
// That is observationally complete **only** while nothing can run concurrently with a function
// body, and the threads proposal's own suite cannot witness the difference either way — it is
// single-agent by construction, so no vector will ever fail when this stops being true.
//
// The event that makes the debt real is the first goroutine in this package. So that is what is
// watched, over a domain derived from the parser rather than a list of files someone remembered:
// *a design debt is discharged by a tripwire, never by an intention.*
//
// It fails **loudly and by design**. The fix is not to delete the check or to add this file to an
// exception list; it is to give the 67 rows a memory model, which is contract §4's work and #542's
// subject.
func TestAtomicsArePlainWhileTheInterpreterIsSingleThreaded(t *testing.T) {
	// `os.ReadDir` plus `ParseFile` rather than `parser.ParseDir`, which is deprecated *and* for a
	// reason that matters to a tripwire: it does not consider build tags when grouping files into
	// packages. Walking the directory takes every `.go` file regardless of tag, which is the safe
	// direction here — a goroutine behind a build tag is still a goroutine in this package.
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	files := 0
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files++
		ast.Inspect(file, func(n ast.Node) bool {
			g, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			t.Errorf("%s:%d launches a goroutine.\n"+
				"The 67 atomics in atomic.go are plain read-then-write with no "+
				"synchronisation, which is correct only while nothing can run "+
				"concurrently with a function body. This is the event #542 was filed "+
				"for: the atomics now need a memory model (contract §4), and no vector "+
				"in the threads suite will fail to tell you so — it is single-agent by "+
				"construction. Do not exempt this file; discharge #542.",
				name, fset.Position(g.Pos()).Line)
			return true
		})
	}
	// The domain has to be non-empty or the sweep above proves nothing — an empty parse is a
	// clean bill of health from an instrument that read nothing.
	if files < 20 {
		t.Errorf("parsed %d non-test files in internal/interp, expected at least 20; "+
			"the walk is reading the wrong directory and its silence means nothing", files)
	}
}
