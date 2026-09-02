// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// There is no hand-aligned scratch buffer here, and its absence is the point.
//
// An earlier draft over-allocated a `[]byte` and sliced forward to make its base 8-byte aligned,
// because `make([]byte, n)` carries no documented alignment (see `checkBaseAlignment`) and a control
// that *skipped* when the base came out misaligned would pass by asking nothing — *a skip is not a
// verdict*. Every control below now runs over a real linear memory instead, which is strictly better
// for the same reason it is more work: the premise those controls need is the one the engine actually
// relies on, and `TestWordAlignedAnswersTheProposalsGuestSpaceCondition` asserts it on the instance's
// own backing array rather than on a fixture built to satisfy it.

// memopWidths returns the distinct access widths in the family, in increasing order.
//
// Derived from `memops` rather than written as `{1, 2, 4, 8}`, because the failure this guards
// against is a width added to the table and not to the controls — *a control scoped to today's
// cases inherits today's blind spot.*
func memopWidths() []uint64 {
	seen := map[uint64]bool{}
	var out []uint64
	for _, m := range memops {
		if !seen[m.width] {
			seen[m.width] = true
			out = append(out, m.width)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// loadMnemonics returns every load opcode in the family with its wat mnemonic, in opcode order.
//
// **Derived from the generated opcode table, not spelled**, by the same two authorities
// `TestMemopTableAgreesWithMnemonics` uses: `binary.OpMnemonic` for the name and
// `parseMemopMnemonic` for whether it is in the family. A list of fourteen mnemonics written by hand
// is a list that stays at fourteen when the family grows, and the row it is missing is the row
// nothing covers — *an issue's list is a registry, not an inventory.* The reference's constructor
// names use `_` where wat uses `.` for the type separator and `_` for the sign suffix, so only the
// first is rewritten: `i64_load16_s` is `i64.load16_s`.
func loadMnemonics(t *testing.T) []struct {
	op   uint32
	name string
} {
	t.Helper()
	var out []struct {
		op   uint32
		name string
	}
	for op := range uint32(256) {
		name, ok := binary.OpMnemonic(op)
		if !ok {
			continue
		}
		if _, isMemop := parseMemopMnemonic(name); !isMemop || isStore(op) {
			continue
		}
		out = append(out, struct {
			op   uint32
			name string
		}{op, strings.Replace(name, "_", ".", 1)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].op < out[j].op })
	// Exactly the loads, 0x28-0x35. Pinned exactly rather than as a floor because the count is
	// knowable, and a floor here would stay green through a 13-of-14 loss (grave #105).
	if len(out) != 14 {
		t.Fatalf("derived %d load mnemonics, want exactly 14 (0x28-0x35): %v — a parser that "+
			"stopped matching would leave this control covering whatever it still recognised",
			len(out), out)
	}
	return out
}

// TestWordAccessAgreesWithTheByteLoop is ADR 0053's load-side agreement control, and the authority is
// the byte loop — the code the whole spec suite has already validated.
//
// **It runs end to end through `Invoke`, and after #557's restructuring it has to.** The branch that
// chooses between the two paths lives in `memAccess`, not inside `loadValue` (see that function for
// the 6.24% that put it there), so a test calling `loadValue` directly reaches the byte loop at every
// address and *no* address can exercise the word path. Such a test still tallies a healthy partition
// and still passes every agreement assertion, which is precisely *a control testing the helper rather
// than the path*. The module below therefore loads through the real dispatch, one exported function
// per load opcode.
//
// **Nothing re-spells the expected value.** The reference is the engine's own byte loop, reached by
// reading the same bytes at an address the predicate rejects: each row loads identical content at
// every placement in a window and every placement must agree. A hand-written reference would add a
// third implementation whose disagreement could be the test's bug rather than the engine's — the
// circularity `TestStoreTruncatesAndIsLittleEndian` names one file over.
//
// Bytes are placed with `i32.store8`, whose width is 1: it cannot be misaligned, so it takes the word
// path's single-byte arm at every address and the placement never depends on the partition under
// test. If that one arm were wrong, `TestAnAlignedStoreWritesTheSameBytesAsAnUnalignedOne`'s width-1
// rows fail first.
//
// Float rows report through `reinterpret`, which is a bit move in this engine (`memop.isFloat`'s
// comment). They are included because `atomicLoadWord`'s 4- and 8-byte arms serve them too, so a byte-order
// defect there is observable on a float row — even though floats carry *no* tearing obligation at all
// (`tearing(fN', N, u32) = ε`).
//
// # The vacuity risk has a name, and it is asserted against
//
// Both paths compute the same value, so this control would be green if the fast path *never fired*.
// That is not hypothetical: on amd64 and arm64 an unaligned typed load returns the same bits as an
// aligned one, so no value anywhere can distinguish which branch ran. Hence the two population counts
// below — placements are classified by the production predicate itself over the instance's real
// memory, and both classes must be non-empty per width.
//
// Watched die three ways: reversing the byte loop's index (a big-endian loop) fails every width above
// 1; reversing `atomicLoadWord`'s 4-byte arm fails the 4-byte loads; and stubbing `wordAligned` to `false`
// fails the aligned-population floor. The first two are value defects, and the point of the third is
// that a value defect is invisible once the partition collapses.
//
// The middle one was **re-observed after ADR 0054 re-pointed the word path**, because re-pointing a
// control at a new identifier leaves its trigger unexercised: wrapping the 4-byte arm's result in
// `bits.ReverseBytes32` fails at `i32.load (opcode 0x28, width 4 …) address 1 read 0x…05060708 but
// address 0 read 0x…08070605`, on the aligned addresses only — which is the arm named and no other.
//
// **And a fourth injection was forecast to kill it and does not, which is a limit worth stating rather
// than a list worth trimming.** Deleting `memAccess`'s branch — so every load takes the byte loop —
// leaves this control green: the tally classifies *addresses* by asking the predicate, and the
// predicate still answers correctly, so a healthy partition is reported for a partition nothing acts
// on. Making the word path unconditional passes here too, since on amd64 and arm64 a misaligned typed
// load returns the right bits. Neither is a value defect and neither is visible from outside the
// dispatch, so **the wiring is not this control's subject**:
// `TestEveryWordAccessSiteIsGuardedByTheAlignmentTest` catches both by AST, and behaviourally the
// spec suite does — reversing *only* `atomicLoadWord` fails `TestPhase1Files`, which it could not do if the
// word path were unreached.
//
// **What it cannot see, and no test in this tree can: `guestWord32`/`64`'s swap.** Deleting the
// swap changes nothing on a little-endian host, where the function is the identity — so this control
// is green on an engine that byte-swaps every access on s390x. That is not a gap this file can close
// and it is not a new one: `guestWord32`'s own comment records it (*"unexercised by CI, like
// everything else in the tree that depends on host byte order"*), and neither CI architecture is
// big-endian. What this control does establish is the part that *is* observable here — that the two
// paths agree — which is what makes the byte loop the authority rather than a second opinion.
func TestWordAccessAgreesWithTheByteLoop(t *testing.T) {
	// Patterns chosen so that a byte-order defect is visible at every width (all bytes distinct in
	// the first two), and so that the sign bit of each access width is set in at least one row —
	// `extendSlot` is shared by both paths, but it consumes their output, so a pattern that never
	// reaches a sign bit would leave that consumption untested.
	patterns := []uint64{
		0x0102030405060708,
		0x8877665544332211,
		0xffffffffffffffff,
		0x0000000000000000,
		0x0000008000008080,
	}

	// window is the address range placements walk. 24 bytes gives every width at least three
	// aligned and several unaligned placements inside one page.
	const window = 24

	loads := loadMnemonics(t)

	// One function per load, all reporting as i64 so a single Invoke signature covers the family.
	// The widening is `extend_i32_u` for i32-slot rows, which is what makes the reported value the
	// slot's own definition — low 32 bits, high bits zero (`extendSlot`'s narrowing step).
	var b strings.Builder
	b.WriteString("(module (memory 1)\n")
	b.WriteString("\t(func (export \"put\") (param i32 i32) (i32.store8 (local.get 0) (local.get 1)))\n")
	for _, l := range loads {
		m := memops[l.op]
		body := fmt.Sprintf("(%s (local.get 0))", l.name)
		switch {
		case m.isFloat && m.is64:
			body = fmt.Sprintf("(i64.reinterpret_f64 %s)", body)
		case m.isFloat:
			body = fmt.Sprintf("(i64.extend_i32_u (i32.reinterpret_f32 %s))", body)
		case !m.is64:
			body = fmt.Sprintf("(i64.extend_i32_u %s)", body)
		}
		fmt.Fprintf(&b, "\t(func (export \"op%02x\") (param i32) (result i64) %s)\n", l.op, body)
	}
	b.WriteString(")")

	in, trap := instantiate1(t, b.String())
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	mem := in.mems[0]

	// Populations tallied across the whole run, per width: the classification is the predicate's
	// own answer over the real memory, never `ea % width`, so a broken predicate cannot report a
	// healthy partition.
	alignedSeen := map[uint64]int{}
	unalignedSeen := map[uint64]int{}

	for _, l := range loads {
		m := memops[l.op]
		fn := fmt.Sprintf("op%02x", l.op)
		for _, pat := range patterns {
			var want uint64
			var wantAt uint64
			haveWant := false
			for ea := uint64(0); ea+m.width <= window; ea++ {
				for i := range m.width {
					if _, err := in.Invoke("put",
						Value{Type: binary.I32, Bits: ea + i},
						Value{Type: binary.I32, Bits: uint64(byte(pat >> (8 * i)))}); err != nil {
						t.Fatalf("placing byte %d of pattern %#016x at %d: %v", i, pat, ea+i, err)
					}
				}
				if wordAligned(mem.view()[ea:ea+m.width], m.width) {
					alignedSeen[m.width]++
				} else {
					unalignedSeen[m.width]++
				}
				out, err := in.Invoke(fn, Value{Type: binary.I32, Bits: ea})
				if err != nil {
					t.Fatalf("%s at %d: %v", l.name, ea, err)
				}
				got := out[0].Bits
				if !haveWant {
					want, wantAt, haveWant = got, ea, true
					continue
				}
				if got != want {
					t.Errorf("%s (opcode %#x, width %d, signed=%t, is64=%t) over pattern %#016x: "+
						"address %d read %#016x but address %d read %#016x. The same bytes are "+
						"being read two different ways, which is a byte-order or width defect in "+
						"whichever path disagrees with the byte loop — and the byte loop is the "+
						"authority (ADR 0053)",
						l.name, l.op, m.width, m.signed, m.is64, pat, ea, got, wantAt, want)
				}
			}
		}
	}

	// Width 1 is aligned at every address — the proposal's `u32 mod N/8 = 0` is vacuously true for
	// N = 8 — so it has no unaligned class and is exempted here rather than silently absent.
	for _, w := range memopWidths() {
		if alignedSeen[w] == 0 {
			t.Errorf("width %d: 0 of the placements classified as aligned, so the word path never "+
				"ran and every agreement assertion above compared the byte loop with itself", w)
		}
		if w > 1 && unalignedSeen[w] == 0 {
			t.Errorf("width %d: 0 of the placements classified as unaligned, so the byte loop never "+
				"ran and there was no authority to agree with", w)
		}
		if w == 1 && unalignedSeen[w] != 0 {
			t.Errorf("width 1 reported %d unaligned placements, and a one-byte access cannot be "+
				"misaligned: `wordAligned` is answering something other than the proposal's "+
				"condition", unalignedSeen[w])
		}
	}
}

// TestWordAlignedAnswersTheProposalsGuestSpaceCondition pins the premise ADR 0053 rests on, and it
// is the one assertion here that is about *conformance* rather than about agreement.
//
// The proposal's condition is on the **guest** effective address, `u32 mod N/8 = 0`
// (`runtime.rst:742-746`). The predicate tests a **host** address. They are the same question only
// because `checkBaseAlignment` refuses to construct a memory whose backing array is not 8-byte
// aligned, and that is a premise about the Go allocator rather than about this code — so it is
// asserted through a real memory, at every width and every address in a window, rather than argued
// from the base once. **A memory this test never grows**, which is the qualification grave #585 adds:
// `newMemory` is the only caller of that check, so `grow`'s reallocating arm publishes an unasserted
// array and the sentence above covers construction rather than the type.
//
// **The direction that matters is one-way.** A false answer only declines the fast path, which is
// always correct; a *true* answer at an address the proposal does not mark `NOTEARS` would be
// harmless too. What would be a conformance defect is a **false** answer where the proposal says
// `NOTEARS`, because then an `i32.load` the specification requires not to tear takes the byte loop.
// The equality below catches both directions, and the message names which one fired.
//
// Watched die by masking the predicate's modulus to `&0` (every address reports aligned — fails the
// unaligned rows) and by returning `false` (fails the aligned rows), and by constructing a memory
// through a deliberately offset backing slice, which fails naming the base rather than the width.
func TestWordAlignedAnswersTheProposalsGuestSpaceCondition(t *testing.T) {
	in, trap := instantiate1(t, `(module (memory 1))`)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if len(in.mems) != 1 || in.mems[0] == nil {
		t.Fatalf("expected one memory, got %d", len(in.mems))
	}
	mem := in.mems[0]
	if base := uintptr(unsafe.Pointer(&mem.view()[0])); base%8 != 0 {
		t.Fatalf("the linear memory's base is %#x, misaligned by %d — `checkBaseAlignment` should "+
			"have refused this construction, and ADR 0053's predicate is answering a different "+
			"question than the proposal's condition on this platform", base, base%8)
	}

	widths := memopWidths()
	if len(widths) == 0 {
		t.Fatal("memops yielded no widths, so every row below is vacuous")
	}
	agree, disagree := 0, 0
	for _, w := range widths {
		// Two full periods of the widest width, so every residue of every width appears twice.
		for ea := range uint64(16) {
			got := wordAligned(mem.view()[ea:ea+w], w)
			want := ea%w == 0 // the proposal's own condition, `u32 mod N/8 = 0`
			if got == want {
				agree++
				continue
			}
			disagree++
			if want {
				t.Errorf("width %d at guest address %d: the proposal marks this NOTEARS "+
					"(%d mod %d == 0) and `wordAligned` says false, so a required-tear-free "+
					"access takes the byte loop", w, ea, ea, w)
			} else {
				t.Errorf("width %d at guest address %d: `wordAligned` says true where "+
					"%d mod %d == %d, so a misaligned typed access is being made — which is "+
					"undefined for `unsafe`, not merely non-conforming", w, ea, ea, w, ea%w)
			}
		}
	}
	if want := len(widths) * 16; agree+disagree != want {
		t.Errorf("checked %d (width, address) pairs, want %d — the loop bounds and the derived "+
			"width set disagree, so the population is not what this control claims", agree+disagree, want)
	}
}

// TestAnAlignedStoreWritesTheSameBytesAsAnUnalignedOne is the store side of the agreement, and it
// runs end to end through the interpreter rather than through `writeNum` directly.
//
// The reason is the shape a sibling control paid for: *a control can test the helper, not the path.*
// Calling `atomicStoreWord` and the fallback from a test proves the two renderings agree while nothing in
// the engine calls either. So the module below stores through `i32.store`/`i64.store` and reads back
// through `i32.load8_u`, one byte at a time — a *narrower* read than the store, which makes byte
// placement observable and avoids the round-trip circularity that a same-width read-back would have.
//
// The aligned placement is the reference and the unaligned placements must match it, so the
// authority is again the byte loop by way of the address.
//
// Watched die two ways: reversing `writeNum`'s fallback shift (the unaligned rows disagree with the
// aligned ones at every width above 1) and byte-swapping `atomicStoreWord`'s 4-byte arm (the aligned rows
// disagree with the unaligned ones). The second was **re-observed after ADR 0054 re-pointed the word
// path**, for the reason its load-side sibling records: `i32.store of 0x1122334455667788 wrote 88 77 66
// 55 at address 1 but 55 66 77 88 at address 0`.
//
// **Deleting `writeNum`'s branch was forecast to fail the partition floor and does not.** Every store
// then takes the fallback, the values are identical, and the floor is computed by asking the predicate
// about *addresses* rather than about which arm ran — so it reports the partition it would have had.
// The same limit its load-side sibling has, for the same reason, and the same control covers it:
// `TestEveryWordAccessSiteIsGuardedByTheAlignmentTest` fails at one site, naming `memAccess` as the
// survivor.
func TestAnAlignedStoreWritesTheSameBytesAsAnUnalignedOne(t *testing.T) {
	// Store mnemonics by width. Spelled because wat is text, and then *checked* against the table
	// so that a width the family gains cannot be covered by omission.
	stores := map[uint64]string{
		1: "i32.store8",
		2: "i32.store16",
		4: "i32.store",
		8: "i64.store",
	}
	widths := memopWidths()
	for _, w := range widths {
		if _, ok := stores[w]; !ok {
			t.Fatalf("memops has a %d-byte access and this control has no store mnemonic for it, "+
				"so that width is untested rather than passing", w)
		}
	}

	var b strings.Builder
	b.WriteString("(module (memory 1)\n")
	for _, w := range widths {
		arg := "(local.get 1)"
		if w != 8 {
			arg = "(i32.wrap_i64 (local.get 1))"
		}
		fmt.Fprintf(&b, "\t(func (export \"w%d\") (param i32 i64) (%s (local.get 0) %s))\n", w, stores[w], arg)
	}
	b.WriteString("\t(func (export \"b\") (param i32) (result i32) (i32.load8_u (local.get 0))))")

	const pat = 0x1122334455667788
	aligned, unaligned := 0, 0
	for _, w := range widths {
		in, trap := instantiate1(t, b.String())
		if trap != nil {
			t.Fatalf("instantiate: %v", trap)
		}
		mem := in.mems[0]
		var want []byte
		var wantAt uint64
		for ea := uint64(0); ea+w <= 16; ea++ {
			if _, err := in.Invoke(fmt.Sprintf("w%d", w),
				Value{Type: binary.I32, Bits: ea}, Value{Type: binary.I64, Bits: pat}); err != nil {
				t.Fatalf("store width %d at %d: %v", w, ea, err)
			}
			got := make([]byte, w)
			for i := range got {
				out, err := in.Invoke("b", Value{Type: binary.I32, Bits: ea + uint64(i)})
				if err != nil {
					t.Fatalf("read back byte %d: %v", ea+uint64(i), err)
				}
				got[i] = byte(out[0].Bits)
			}
			// Classified by the production predicate over the real memory, not by arithmetic, for
			// the reason its sibling control gives: a broken predicate must not be able to report
			// a healthy partition.
			if wordAligned(mem.view()[ea:ea+w], w) {
				aligned++
			} else {
				unaligned++
			}
			if want == nil {
				want, wantAt = got, ea
				continue
			}
			if string(got) != string(want) {
				t.Errorf("%s of %#016x wrote % x at address %d but % x at address %d — the two "+
					"store paths render the value differently, and the byte loop is the authority "+
					"(ADR 0053)", stores[w], uint64(pat), got, ea, want, wantAt)
			}
		}
	}
	if aligned == 0 || unaligned == 0 {
		t.Errorf("the store addresses covered %d aligned and %d unaligned placements; both classes "+
			"must be non-empty or the agreement above is one path compared with itself", aligned, unaligned)
	}
}

// TestEveryWordAccessSiteIsGuardedByTheAlignmentTest is the structural half, and its subject is
// memory safety rather than conformance: `atomicLoadWord` and `atomicStoreWord` make typed accesses through
// `unsafe`, which is undefined at a misaligned address, so **every** call site must be dominated by
// the predicate.
//
// The domain is derived — every call to either function in a non-test file in this package — because
// the failure this exists to catch is *a site added later*, by an author who read the fast path as an
// optimization and reached for it somewhere the address is not known to be aligned. That is the same
// reason `TestEveryStackCreationSiteCrossesTheBoundary` derives its population, and this control
// borrows its enclosing-function granularity for the same reason: the test is at the top of the
// function and the access is inside a branch, so the two are in one body and never in one expression.
//
// **It is also the only control here that can see the wiring, and that is measured rather than
// claimed.** Its two siblings classify addresses by asking the predicate, so both stay green when the
// branch they describe is deleted outright — the partition is reported as healthy because the
// predicate still answers correctly about addresses nothing dispatches on. Both of those injections
// land here instead, which is why this control's floor is load-bearing and not decoration.
//
// Watched die four ways against `loadWord`/`storeWord`, the identifiers this control named before ADR
// 0054. Deleting a whole branch — the guard *and* the word access — fails the floor at `found 1`,
// naming the surviving site (`writeNum` when `memAccess`'s branch goes, `memAccess` when `writeNum`'s
// does). Deleting only the guard, leaving the word access unconditional, fails the bare list naming
// that function — which is the memory-safety half, since the access is then made at misaligned
// addresses. A scratch non-test file with a bare word-access call fails naming the scratch file. And
// blinding the call match fails the floor at `found 0`.
//
// **Re-pointed at `atomicLoadWord`/`atomicStoreWord` by 0054, and re-pointing is not re-witnessing** —
// changing the names a tripwire matches leaves its trigger unexercised, so two of the four were run
// again on the new identifiers rather than assumed to have survived the rename. Deleting `memAccess`'s
// branch: `found 1 call sites of atomicLoadWord/atomicStoreWord, want at least 2`. Deleting only
// `writeNum`'s guard: `1 function(s) make a typed word access without consulting wordAligned:
// [memory.go:NNN writeNum]` — the line number is elided from the quote rather than transcribed, since a
// transcribed one drifts with the next insertion above it and the function name is the part that
// identifies the site. The other two are unchanged in mechanism — the scratch-file and
// blind-the-match injections turn on the file walk and the match switch, neither of which 0054
// touched — and they are recorded as carried from the original witnessing, not as re-observed.
func TestEveryWordAccessSiteIsGuardedByTheAlignmentTest(t *testing.T) {
	// `memAccess` and `writeNum`, both in memory.go — the two linear-memory access paths. A floor
	// rather than an equality because a new site is what this should judge, with the exact count
	// beside it because a floor alone catches a moved file and never a silent partial loss.
	const sitesWhenWritten = 2

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	var guarded, bare []string
	files, sites := 0, 0
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		files++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// The definitions themselves are not call sites, and `wordAligned` is documented as
			// their precondition rather than enforced inside them — putting the test in `atomicLoadWord`
			// would make the fast path pay for it twice and the fallback unreachable.
			switch fn.Name.Name {
			case "atomicLoadWord", "atomicStoreWord":
				continue
			}
			accesses, tested := 0, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				switch id.Name {
				case "atomicLoadWord", "atomicStoreWord":
					accesses++
				case "wordAligned":
					tested = true
				}
				return true
			})
			if accesses == 0 {
				continue
			}
			sites++
			site := fmt.Sprintf("%s:%d %s", name, fset.Position(fn.Pos()).Line, fn.Name.Name)
			if tested {
				guarded = append(guarded, site)
			} else {
				bare = append(bare, site)
			}
		}
	}
	sort.Strings(guarded)
	sort.Strings(bare)

	if files == 0 {
		t.Fatalf("parsed 0 non-test .go files in internal/interp, so every assertion below is "+
			"vacuous: %d directory entries were considered", len(ents))
	}
	if sites < sitesWhenWritten {
		t.Fatalf("found %d call sites of atomicLoadWord/atomicStoreWord, want at least %d (memAccess and "+
			"writeNum, both in memory.go). Below that the fast path is not wired into a memory "+
			"access at all, and every agreement control in this file is comparing the byte loop "+
			"with itself. Guarded: %v", sites, sitesWhenWritten, guarded)
	}
	if len(bare) > 0 {
		t.Errorf("%d function(s) make a typed word access without consulting `wordAligned`: %v. A "+
			"misaligned `*(*uint32)(unsafe.Pointer(…))` is undefined rather than merely "+
			"non-conforming, and `-race` catches it through checkptr only on the paths a test "+
			"happens to reach (ADR 0053)", len(bare), bare)
	}
}
