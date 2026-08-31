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

// TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded is #557's tripwire, and it
// used to be #542's.
//
// **Re-pointed rather than retired, because its subject narrowed and did not dissolve.** It was named
// `TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded` and it watched for the first goroutine in
// this package on the ground that the 67 atomics were plain read-then-write. ADR 0051 discharged
// that: they are sequentially consistent now, and the old name would be *asserting a property the
// code no longer has*, which is the review-confirms-the-bug shape wearing a test name.
//
// What survives is the other half of the same risk. The plain accesses — `i32.load`, `i32.store` and
// every narrower integer width, in memop.go, not in this file — are still a byte-at-a-time loop and a
// `copy`, and the threads proposal requires a naturally aligned integer access of 32 bits or fewer
// **not to tear** (`runtime.rst:742-746`, called from the ordinary load and store at
// `instructions.rst:1763` and `instructions.rst:2315`). That is a weaker property than atomicity and it is still
// unmet: #557. §4's boundary model and its litmus battery are the rest of it: #516.
//
// So the *event* being watched is unchanged — the first goroutine in a non-test file in this package
// — and only what it points the reader at has moved. *A tripwire whose subject dissolves is
// re-pointed*; closing one as no-longer-applicable retires a live risk, and this one's risk is live
// twice over.
//
// It stays in this file rather than moving to memop_test.go with its new subject. The domain is the
// whole package, so no file is its natural home, and moving it would re-point five citations twice
// for no gain — thread.go and thread_test.go both cite it as the reason `Spawn` is withheld, which is
// a fact about the package rather than about either file.
//
// It fails **loudly and by design**. The fix is not to delete the check or to add a file to an
// exception list; it is to make the aligned plain accesses tear-free.
func TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded(t *testing.T) {
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
				"memop.go's plain load is a byte-at-a-time loop and its plain store is "+
				"a `copy`, so an aligned i32 access can tear where the threads proposal "+
				"forbids it (`runtime.rst:742-746`, `tearing(iN, N, u32) = NOTEARS` for "+
				"N <= 32, called from the ordinary load and store). Tear-freedom is "+
				"weaker than atomicity — it asks that the access not decompose, not "+
				"that it be ordered — so ADR 0051's atomics do not cover it: different "+
				"opcodes, different path. No vector in the threads suite will fail to "+
				"tell you, because it is single-agent by construction. Do not exempt "+
				"this file; discharge #557, and #516 for §4's boundary model.",
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

// TestAtomicCellAgreesWithTheByteLoop checks ADR 0051's word arithmetic against the authority that
// can settle it: `loadValue`, the byte-at-a-time little-endian loop the whole spec suite has already
// validated.
//
// **This is the only place the host-endianness normalization is checked at all**, and it is checked
// against a second mechanism rather than against itself. memop.go's own comment says why that
// matters: *"A big-endian host reading through unsafe would produce byte-swapped values for every
// vector, which dual-platform CI would catch only if one of its arches were big-endian — and neither
// is."* Reading a word through `unsafe` is exactly what `atomicCell` does, so on a big-endian host
// every atomic in the region would be byte-swapped and both CI arches would stay green. This test
// does not fix that — it cannot manufacture a big-endian host — but it makes the two mechanisms
// disagree loudly wherever they disagree, which is the strongest thing available from here.
//
// Three directions, because they fail differently. The load direction catches a wrong shift or mask;
// the store direction catches the same in reverse *plus* a write that clobbers the bytes it should
// not, which is the failure mode the 1- and 2-byte compare-and-swap emulation is usually suspected
// of; and cmpxchg is asked in both of its branches, since a version that always wrote and a version
// that never wrote would both pass a one-branch test.
func TestAtomicCellAgreesWithTheByteLoop(t *testing.T) {
	mem, err := newMemory(binary.Memory{Limits: binary.Limits{Min: 1}})
	if err != nil {
		t.Fatalf("newMemory: %v", err)
	}
	// Every byte distinct, and none of them zero: a swapped pair, a dropped high byte and a
	// mask that reads one byte too many all produce a different number against this pattern,
	// where a run of zeros would hide all three.
	const span = 32
	pattern := func() {
		for i := range mem.bytes[:span] {
			mem.bytes[i] = byte(0x80 + i)
		}
	}

	rows := 0
	for _, width := range []uint64{1, 2, 4, 8} {
		for ea := uint64(0); ea+width <= span; ea += width {
			pattern()
			c, cerr := mem.cell(ea, 0, width)
			if cerr != nil {
				t.Fatalf("cell(%d, 0, %d): %v", ea, 0, cerr)
			}
			byteLoop := func() uint64 {
				return loadValue(mem.bytes[ea:ea+width], memop{width: width})
			}

			// Load.
			if got, want := c.load(), byteLoop(); got != want {
				t.Errorf("width %d at ea %d: cell.load() = %#x, byte loop reads %#x.\n"+
					"The word cast disagrees with the little-endian assembly the suite "+
					"validated, so either the shift, the mask or the host-order "+
					"normalization is wrong (ADR 0051)", width, ea, got, want)
			}

			// Store, and the neighbours it must not touch.
			word := ea &^ 3
			before := append([]byte(nil), mem.bytes[word:word+8]...)
			v := uint64(0x1122334455667788) & c.mask
			c.store(v)
			if got := byteLoop(); got != v {
				t.Errorf("width %d at ea %d: stored %#x, byte loop reads back %#x",
					width, ea, v, got)
			}
			for i, b := range mem.bytes[word : word+8] {
				at := word + uint64(i)
				if at >= ea && at < ea+width {
					continue // inside the field, expected to have changed
				}
				if b != before[i] {
					t.Errorf("width %d at ea %d: storing the field changed byte %d "+
						"of the containing word from %#x to %#x.\n"+
						"A narrow atomic is a read-modify-write of the whole 32-bit "+
						"word, so a wrong mask silently rewrites its neighbours — "+
						"which are separate locations in the model and must survive",
						width, ea, at, before[i], b)
				}
			}

			// Compare-exchange, both branches.
			pattern()
			live := byteLoop()
			if got := c.compareAndSwap(live^c.mask, v); got != live {
				t.Errorf("width %d at ea %d: mismatching cmpxchg returned %#x, want the "+
					"value that was there, %#x", width, ea, got, live)
			}
			if got := byteLoop(); got != live {
				t.Errorf("width %d at ea %d: a mismatching cmpxchg wrote %#x over %#x; "+
					"the spec stores only on equality", width, ea, got, live)
			}
			if got := c.compareAndSwap(live, v); got != live {
				t.Errorf("width %d at ea %d: matching cmpxchg returned %#x, want the old "+
					"value %#x — the result is the old value either way", width, ea, got, live)
			}
			if got := byteLoop(); got != v {
				t.Errorf("width %d at ea %d: matching cmpxchg left %#x, want %#x",
					width, ea, got, v)
			}
			rows++
		}
	}

	// 32 + 16 + 8 + 4 aligned positions across the four widths. Printed and pinned because a
	// loop bound edited to `ea < width` would test one position per width and pass everything.
	t.Logf("cell arithmetic checked against loadValue at %d aligned positions, four widths, "+
		"three directions each", rows)
	if rows != 60 {
		t.Errorf("covered %d positions, want 60: the loop bounds changed, and a shrunken "+
			"sweep is a quieter test rather than a correct engine", rows)
	}
}

// TestAtomicRmwIsNotObservablyTornAcrossThreads is #542's deliverable, and the first test in this
// tree where two agents touch one linear memory.
//
// **It is a witness, not a forecast.** Before ADR 0051 this exact program yielded 3392 of 4000 —
// `atomicRmw` was `read`, `applyRmw`, `write` as three plain steps, so an update landing between any
// two of them was lost. Nothing in either corpus can see that: `atomic.wast`'s 297 vectors exercise
// all 67 opcodes and every one of them single-threaded, which is why they scored 297/297 both before
// and after this repair. *A zero-fail board is a lost instrument* — the file agreed with the engine
// while the engine was losing three quarters of a thousand updates.
//
// The `go` statement is what made the defect reachable, and it lives here rather than in the engine
// on purpose: `TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded` scans non-test files only, so
// this test does not trip the tripwire that was watching for exactly this event. That is not a
// loophole being exploited — T-1's `Spawn` is #514's, still unlanded, and this test needs none of it.
// Two goroutines each calling `Invoke` get their own frames and stacks and share `in.mems[0]`, which
// is the whole of what §4's model is about.
func TestAtomicRmwIsNotObservablyTornAcrossThreads(t *testing.T) {
	const (
		agents = 2
		adds   = 2000
	)
	src := fmt.Sprintf(`(module
	  (memory 1 1 shared)
	  (func (export "bump") (local $i i32)
	    (block $done (loop $l
	      (br_if $done (i32.eq (local.get $i) (i32.const %d)))
	      (drop (i32.atomic.rmw.add (i32.const 0) (i32.const 1)))
	      (local.set $i (i32.add (local.get $i) (i32.const 1)))
	      (br $l))))
	  (func (export "read") (result i32) (i32.atomic.load (i32.const 0))))`, adds)

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
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("instantiate fell short: %v", derr)
	}

	errs := make(chan error, agents)
	done := make(chan struct{})
	for range agents {
		go func() {
			_, ierr := in.Invoke("bump")
			errs <- ierr
			done <- struct{}{}
		}()
	}
	for range agents {
		<-done
	}
	close(errs)
	for ierr := range errs {
		if ierr != nil {
			t.Fatalf("invoke: %v", ierr)
		}
	}

	got, err := in.Invoke("read")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read returned %d values, want 1", len(got))
	}
	want := uint64(agents * adds)
	if v := uint64(uint32(got[0].Int32())); v != want {
		t.Errorf("%d agents x %d i32.atomic.rmw.add on one cell left %d, want %d — %d updates "+
			"were lost.\n"+
			"The region's read-modify-write must be one atomic operation, which the threads "+
			"proposal fixes at sequential consistency unconditionally (`relaxed.rst:35`, "+
			"`ordact(ARMW ...) = SEQCST`, a function with one case; `relaxed.rst:244`). No corpus "+
			"vector "+
			"can witness this, so this test is the oracle (#542, ADR 0051)",
			agents, adds, v, want, want-v)
	}
}
