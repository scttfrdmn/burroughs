// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

// Package scanbench prices the cost that #136's option 3 removes: one matching-`end` scan per
// *dynamic* block entry, which in a loop body is paid per iteration.
//
// # What is under test, and why this is a measurement rather than a port
//
// `internal/interp/control.go` resolves branch targets at **block entry** (`matchEnd`), and its
// own header says what that costs — "a scan per dynamic block entry, which in a loop body is paid
// per iteration … precisely the shape of cost 0002 rejected the side table over". Decision 0002's
// Option B says the build-time form makes branch resolution "free — just an index fixup at build
// time (the benchmark does exactly this)". **0002's benchmark measured three representations
// against each other on a hot loop; it never measured the entry-time scan.** That is the
// unmeasured magnitude, and Scott's ruling on the #499 reconciliation ordered it priced before
// the port: "#136 may falsify the premise rather than discharge the obligation."
//
// The pre-registration — the discriminator, the axis that cannot discriminate, the three
// materiality numbers, and the A/A noise floor with veto power — is a comment on #136, posted
// before this file existed. Read it first; nothing here is interpretable without it.
//
// # Why this runs the engine instead of a replica
//
// `dropbench` and `dispatchbench` measure reimplemented shapes, and for their subjects that is
// sound: a stack's bookkeeping cost is a data-structure question answerable in isolation. **This
// subject is a ratio, not a cost** — the scan's price relative to the dispatch it sits inside —
// and a replica whose executed work is cheaper than the real interpreter's dispatch loop inflates
// the scan's share. The bias runs in the flattering direction, toward "the port is material", so
// the measurement goes through `burroughs.Instantiate` and `Instance.Call`: the public entry
// point, the real decoder, the real dispatch loop.
//
// # The shapes, and what holds constant across them
//
// Executed work is held constant and **scanned distance is varied with padding that is never
// executed**: the padding sits after the loop's back-edge `br_if`, so iterations 1..N-1 jump over
// it from the header and only the final fall-through runs it once. Every iteration's re-entry of
// the loop header still scans the whole body, padding included, because that is what `matchEnd`
// does. So `distance` moves the scan and leaves the interpreted instruction count alone, up to a
// single trailing pass.
//
//   - shapeEntries — the loop padded to `distance`. N dynamic entries, each scanning `distance`.
//     At `distance == 0` this is the pre-registration's shape (ii); at larger distances, (iii).
//   - shapePadOnce — the same loop *unpadded*, wrapped in one outer `block` that carries the
//     padding. The attribution control (shape (i)): identical executed work and identical
//     per-iteration scan to `shapeEntries` at distance 0, plus exactly **one** long scan for the
//     outer block. If a long distance entered once is measurably expensive here, the effect is
//     not per-entry and no other row can be read.
package scanbench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// iters is the loop's trip count: dispatchbench's own constant, taken for the reason it chose it
// and dropbench repeated it — one real loop body's trip count rather than a stress-test extreme.
// It is the *dynamic entry count* here, which is the axis §2 of the pre-registration names as
// unable to discriminate on its own, so it is held fixed and `distance` is what moves.
const iters = 1000

// distances are the scanned-distance points. Three or more is condition 2's requirement (a
// monotone slope in distance, not a single-point win); 0 is shape (ii) and doubles as the
// same-work baseline the other rows are read against.
var distances = []int{0, 64, 512, 4096}

// loopBody is the interpreted work per iteration, and it is the same text at every distance:
// accumulate, decrement, test. Ten instructions, all of them cheap, so the measurement is not
// dominated by one expensive opcode standing in for a loop body.
const loopBody = `
    local.get 1
    local.get 0
    i32.add
    local.set 1
    local.get 0
    i32.const 1
    i32.sub
    local.set 0
    local.get 0
    br_if 0`

// pad emits n `nop`s — an instruction the decoder retains, the scan must step over, and the
// interpreter can execute in the least work any opcode takes. Padding with something expensive
// would confound distance with executed cost on the one trailing pass that does run it.
func pad(n int) string {
	if n == 0 {
		return ""
	}
	return "\n    " + strings.TrimSuffix(strings.Repeat("nop\n    ", n), "\n    ")
}

// shapeEntries is the loop with `distance` nops inside it: N entries, each scanning past them.
func shapeEntries(distance int) string {
	return fmt.Sprintf(`(module
  (func (export "run") (param i32) (result i32)
    (local i32)
    loop%s%s
    end
    local.get 1))
`, loopBody, pad(distance))
}

// shapePadOnce is the attribution control: the loop unpadded, one outer block carrying the
// padding, so the long distance is entered exactly once per call.
func shapePadOnce(distance int) string {
	return fmt.Sprintf(`(module
  (func (export "run") (param i32) (result i32)
    (local i32)
    block
      loop%s
      end%s
    end
    local.get 1))
`, loopBody, pad(distance))
}

// want is the loop's result for `iters`: iters + (iters-1) + … + 1.
const want = iters * (iters + 1) / 2

// straightPairs is the length of the structural-free control below, in `i32.const 1`/`i32.add`
// pairs. 5000 pairs is 10000 interpreted instructions, which is the same order as the loop shapes'
// own executed work (`iters` × the ten instructions of `loopBody`) — matched deliberately, so the
// control and the rows it vetoes are priced against comparable dispatch volume rather than against
// each other's noise.
const straightPairs = 5000

// shapeStraight is condition 3's **corrected** control, registered on #136 before it ran
// (`issues/136#issuecomment-5384227004`) and after the shape actually built was found unable to
// satisfy the condition as written.
//
// **No `block`, no `loop`, no structural instruction of any kind**, so neither lane scans for
// anything: `matchEnd` is never reached in lane A and the table is never indexed in lane B. What is
// left is the seam itself — a nil return per call against a `sync.Map` load per call plus one
// table build per body — and the executed instructions, which are identical.
//
// The prediction registered before the run: **Δ ≈ 0, or slightly against B.** A significant Δ in
// B's favour here means B is winning something that is not the scan, and the Entries rows are then
// uninterpretable, which is exactly what condition 3 was for. The shape that failed kept a
// 1000-iteration loop and moved only the padding out of it, so it paid 1000 short scans of its own
// and its Δ could not have been inside the floor whatever B did — an outcome the design fixed
// before any data existed.
func shapeStraight() string {
	var b strings.Builder
	b.WriteString("(module\n  (func (export \"run\") (param i32) (result i32)\n    local.get 0")
	for range straightPairs {
		b.WriteString("\n    i32.const 1\n    i32.add")
	}
	b.WriteString("))\n")
	return b.String()
}

// straightWant is the control's result: the argument plus one per pair.
const straightWant = iters + straightPairs

// instantiate assembles the text with the engine's own assembler and instantiates through the
// public API, failing rather than skipping — a benchmark whose module did not build measures
// nothing, and a skip there would read as a result.
func instantiate(tb testing.TB, src string) *burroughs.Instance {
	tb.Helper()
	wasm, err := text.EncodeModule([]byte(src))
	if err != nil {
		tb.Fatalf("assembling shape: %v\n%s", err, src)
	}
	in, err := burroughs.Instantiate(wasm)
	if err != nil {
		tb.Fatalf("instantiating shape: %v", err)
	}
	return in
}

// call runs the export once and checks the answer. **The check is inside the timed loop's
// helper on purpose**: a module that stopped computing the sum would benchmark faster, and a
// benchmark that cannot tell a speedup from a wrong answer is the one failure mode a timing
// harness cannot recover from afterwards.
func call(tb testing.TB, in *burroughs.Instance) {
	tb.Helper()
	callWant(tb, in, want)
}

// callWant is call with the shape's own expected result, since the structural-free control computes
// a different sum from the same argument.
func callWant(tb testing.TB, in *burroughs.Instance, expect int32) {
	tb.Helper()
	got, err := in.Call("run", burroughs.I32(iters))
	if err != nil {
		tb.Fatalf("calling run: %v", err)
	}
	if len(got) != 1 || got[0].Int32() != expect {
		tb.Fatalf("run(%d) = %v, want [i32:%d]", iters, got, expect)
	}
}

// TestShapesRunAndAgree is the vacuity check the benchmarks below cannot perform on themselves:
// every shape at every distance assembles, instantiates, and returns the *same* answer. Two
// shapes that disagree are not two readings of one workload, and a distance that changed the
// result would mean the padding is executed work rather than scanned work.
func TestShapesRunAndAgree(t *testing.T) {
	if len(distances) < 3 {
		t.Fatalf("condition 2 needs at least three distances, have %d", len(distances))
	}
	// The control is checked here too, and its own answer: a control that stopped computing would
	// veto every row above by being fast, which is the failure a timing harness cannot detect.
	t.Run("straight", func(t *testing.T) {
		callWant(t, instantiate(t, shapeStraight()), straightWant)
	})
	for _, d := range distances {
		for name, src := range map[string]string{
			"entries": shapeEntries(d),
			"padonce": shapePadOnce(d),
		} {
			t.Run(fmt.Sprintf("%s/d=%d", name, d), func(t *testing.T) {
				call(t, instantiate(t, src))
			})
		}
	}
}

func BenchmarkEntries(b *testing.B) {
	for _, d := range distances {
		in := instantiate(b, shapeEntries(d))
		b.Run(fmt.Sprintf("distance=%d", d), func(b *testing.B) {
			for b.Loop() {
				call(b, in)
			}
		})
	}
}

func BenchmarkPadOnce(b *testing.B) {
	for _, d := range distances {
		in := instantiate(b, shapePadOnce(d))
		b.Run(fmt.Sprintf("distance=%d", d), func(b *testing.B) {
			for b.Loop() {
				call(b, in)
			}
		})
	}
}

// BenchmarkStraight is the corrected attribution control. It has veto power over every row above:
// a significant Δ in B's favour here is B winning something other than the scan.
func BenchmarkStraight(b *testing.B) {
	in := instantiate(b, shapeStraight())
	for b.Loop() {
		callWant(b, in, straightWant)
	}
}
