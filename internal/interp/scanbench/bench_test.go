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
	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// iters is the loop's trip count: dispatchbench's own constant, taken for the reason it chose it
// and dropbench repeated it — one real loop body's trip count rather than a stress-test extreme.
// It is the *dynamic entry count* here, which is the axis §2 of the pre-registration names as
// unable to discriminate on its own, so it is held fixed and `distance` is what moves.
const iters = 1000

// distances are the scanned-distance points of the **original** sweep, kept as they were run. Three
// or more is condition 2's requirement (a monotone slope in distance, not a single-point win); 0 is
// shape (ii) and doubles as the same-work baseline the other rows are read against.
//
// **512 and 4096 are retired distances and are left here deliberately.** The suite's own opener
// distribution (`TestSuiteScanDistanceDistributionIsMeasured`) has max 276 slots and **zero**
// openers at ≥512, so those two rows priced distances no module in the corpus contains. They stay
// because they are the rows #502 published and conditions 1 and 2 were read on them — deleting the
// shapes would leave an issue's tables citing code that no longer exists — and they are not re-run.
// The relocated sweep is `spans` below.
var distances = []int{0, 64, 512, 4096}

// spans are the relocated distances, and the unit changes with them: a **span** is the whole loop
// body's length in decoded slots, which is what `matchEnd` walks, where `distances` above counts
// only the padding added to a 10-instruction body. The four are the census's percentiles — p50 5,
// p90 13, p99 67 — plus the corpus maximum 276.
//
// **276 is outside condition 1 by registration.** It is one opener out of 2020; a materiality
// threshold read on a single corpus member would be its own materiality failure. Condition 1 is read
// at **67**, the largest distance Scott named, and 276 is the corpus ceiling reported as such.
var spans = []int{5, 13, 67, 276}

// coreSlots is the length of `loopCore` below. The two relocated shapes are built by adding
// `span - coreSlots` slots of padding, so a span of exactly 5 — the corpus median — has to be
// expressible with no padding at all, and that is why this core exists rather than reusing
// `loopBody`: ten instructions cannot express a five-slot body.
const coreSlots = 5

// loopCore is a counting loop in exactly five slots, using `local.tee` to decrement, store and test
// in one instruction. Placed **after** any padding in the coupled shape and **before** it in the
// decoupled one, which is the entire difference between them.
const loopCore = `
    local.get 0
    i32.const 1
    i32.sub
    local.tee 0
    br_if 0`

// arithUnit is one stack-neutral unit of executed padding: read the accumulator, add one, store it.
// Four slots, so a span whose padding is not a multiple of four is filled out with `nop`s and the
// exact composition is reported rather than rounded.
const arithUnitSlots = 4

// padCoupled emits n slots of padding that every iteration **executes** as well as scans.
//
// The mix is the free parameter that sets the answer, which is why there are two and why the
// registration says so: `nop` is the cheapest arm the interpreter has, so it minimises the executed
// term in the denominator and **maximises** Δ% — that row is a ceiling, not an estimate. The
// arithmetic unit is the mix `shapeStraight` already uses, as the realistic figure. Padding with
// `nop` and reporting it as the real-code number would be a flattering choice wearing a neutral one.
func padCoupled(n int, mix string) string {
	if n <= 0 {
		return ""
	}
	if mix == "nop" {
		return pad(n)
	}
	units, rem := n/arithUnitSlots, n%arithUnitSlots
	var b strings.Builder
	for range units {
		b.WriteString("\n    local.get 1\n    i32.const 1\n    i32.add\n    local.set 1")
	}
	b.WriteString(pad(rem))
	return b.String()
}

// coupledWant is the accumulator's value after the run: one increment per arithmetic unit per
// iteration, and zero for the `nop` mix, which accumulates nothing.
//
// **What this check can and cannot catch.** For the arithmetic mix it is a real work check: the
// padding must have executed `iters` times over. For `nop` the expected value is 0, which a loop
// that never ran would also produce for the accumulator — but not for the counter, since the
// function returns after `local 0` has been driven to zero and a loop that did not run leaves it at
// `iters`. So the weaker check still separates "ran to completion" from "did not run", which is the
// failure a timing harness cannot detect afterwards, and it is stated rather than implied.
func coupledWant(span int, mix string) int32 {
	if mix == "nop" {
		return 0
	}
	return int32(iters * ((span - coreSlots) / arithUnitSlots))
}

// shapeCoupled is the relocated A/B's subject: scanned distance **equals** executed length, which is
// what a real block body does and what the original sweep deliberately did not do.
//
// #502's shapes pad after the back-edge, so the padding is scanned every iteration and executed
// once — at d=64 that is 64 slots scanned against ~10 executed, **6.4:1**, where a real 64-slot loop
// body scans 64 and executes 64, **1:1**. Its own closing comment recorded the consequence: the
// mechanism and the slope transfer, the percentage does not. So relocating the distances without
// recoupling would have moved the x-axis and left the y-axis measuring the wrong workload.
func shapeCoupled(span int, mix string) string {
	return fmt.Sprintf(`(module
  (func (export "run") (param i32) (result i32)
    (local i32)
    loop%s%s
    end
    local.get 1))
`, padCoupled(span-coreSlots, mix), loopCore)
}

// shapeDecoupled is `shapeCoupled`'s pair at the same span, with the padding moved **after** the
// back-edge so it is scanned every iteration and executed once. Same span, same scan, same core, and
// the executed work held at five instructions per iteration.
//
// It is here so the pair differs in exactly one variable. Δ on this shape divided by the span is the
// scan's **marginal cost per slot** at a distance real code has — the quantity #502 could only
// measure at 74 slots and above, where its long-row slope (0.537 ns/slot) is twice what its single
// short row implies (0.290 ns/slot at 10 slots). Which of those two holds at 5–276 slots is the
// registered second question, and this shape is what answers it.
func shapeDecoupled(span int) string {
	return fmt.Sprintf(`(module
  (func (export "run") (param i32) (result i32)
    (local i32)
    loop%s%s
    end
    local.get 1))
`, loopCore, pad(span-coreSlots))
}

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

// mixes are the two padding compositions, in a fixed order so two runs' rows line up for benchstat.
var mixes = []string{"nop", "arith"}

// TestRelocatedSpansAreTheLengthsClaimed is the vacuity check the benchmarks cannot perform on
// themselves, and it checks the **claim in the unit the whole measurement is denominated in**: that a
// shape built for span N has a decoded body of exactly N slots.
//
// A span that is off by a few slots would move every per-slot figure silently, and *measure with the
// instrument, not a regex* — so this decodes each shape through the real pipeline and counts the
// loop's body from its `loop` slot to the matching `end`, using `matchEnd`'s own arithmetic rather
// than counting instructions in the generated text.
func TestRelocatedSpansAreTheLengthsClaimed(t *testing.T) {
	if len(spans) < 3 {
		t.Fatalf("condition 2 needs at least three distances, have %d", len(spans))
	}
	for _, span := range spans {
		for _, mix := range mixes {
			t.Run(fmt.Sprintf("coupled/span=%d/%s", span, mix), func(t *testing.T) {
				assertSpan(t, shapeCoupled(span, mix), span)
			})
		}
		t.Run(fmt.Sprintf("decoupled/span=%d", span), func(t *testing.T) {
			assertSpan(t, shapeDecoupled(span), span)
		})
	}
}

// assertSpan decodes one shape and checks its loop body's length against the span it was built for.
func assertSpan(t *testing.T, src string, span int) {
	t.Helper()
	wasm, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("assembling: %v\n%s", err, src)
	}
	m, err := binary.DecodeModule(wasm)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(m.Funcs) != 1 {
		t.Fatalf("shape has %d functions, want 1", len(m.Funcs))
	}
	body := m.Funcs[0].Body
	opener := -1
	for i, ins := range body {
		if ins.Prefix == 0x00 && ins.Op == 0x03 { // loop
			opener = i
			break
		}
	}
	if opener < 0 {
		t.Fatalf("no loop opener in the decoded body of %d slots", len(body))
	}
	// The body is the slots strictly between the opener and its END, which is `end - opener - 1`.
	end := -1
	depth := 0
	for i := opener; i < len(body); i++ {
		if body[i].Prefix != 0x00 {
			continue
		}
		switch body[i].Op {
		case 0x02, 0x03, 0x04, 0x1f: // block, loop, if, try_table
			depth++
		case 0x0b: // end
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("loop at %d has no matching end in %d slots", opener, len(body))
	}
	if got := end - opener - 1; got != span {
		t.Errorf("loop body is %d slots, want span %d — every per-slot figure is denominated in this "+
			"number, so an off-by-n here is an off-by-n in the result", got, span)
	}
}

// TestRelocatedShapesRunAndAgree checks that every relocated shape computes its registered answer.
// The coupled arithmetic rows carry a real work check — the padding must have executed `iters` times
// over — and the `nop` rows the weaker one `coupledWant` documents.
func TestRelocatedShapesRunAndAgree(t *testing.T) {
	for _, span := range spans {
		for _, mix := range mixes {
			t.Run(fmt.Sprintf("coupled/span=%d/%s", span, mix), func(t *testing.T) {
				callWant(t, instantiate(t, shapeCoupled(span, mix)), coupledWant(span, mix))
			})
		}
		t.Run(fmt.Sprintf("decoupled/span=%d", span), func(t *testing.T) {
			callWant(t, instantiate(t, shapeDecoupled(span)), 0)
		})
	}
	// At the median span the two mixes are the same module by construction — there is no padding to
	// compose — so they must be byte-identical. A built-in consistency check on the generator: if
	// these ever differ, `padCoupled` is emitting something at n<=0.
	if a, b := shapeCoupled(coreSlots, "nop"), shapeCoupled(coreSlots, "arith"); a != b {
		t.Errorf("at span=%d the two mixes must be the same module, got:\n%s\nand\n%s", coreSlots, a, b)
	}
}

// BenchmarkCoupled is the relocated A/B: scanned distance equals executed length, at the spans the
// suite actually contains. Condition 1 is read on the `arith` row at span 67.
func BenchmarkCoupled(b *testing.B) {
	for _, span := range spans {
		for _, mix := range mixes {
			in := instantiate(b, shapeCoupled(span, mix))
			want := coupledWant(span, mix)
			b.Run(fmt.Sprintf("span=%d/%s", span, mix), func(b *testing.B) {
				for b.Loop() {
					callWant(b, in, want)
				}
			})
		}
	}
}

// BenchmarkDecoupled is the pair that isolates the scan at the same spans: Δ divided by the span is
// the marginal cost per scanned slot, which is the registered second question.
func BenchmarkDecoupled(b *testing.B) {
	for _, span := range spans {
		in := instantiate(b, shapeDecoupled(span))
		b.Run(fmt.Sprintf("span=%d", span), func(b *testing.B) {
			for b.Loop() {
				callWant(b, in, 0)
			}
		})
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
