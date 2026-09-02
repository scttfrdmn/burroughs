// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package loopbench measures [ADR 0059]'s safepoint poll on the axis it turns on: how many
// instructions a guest executes per loop back-edge.
//
// # Why this package exists, and what it replaces
//
// #515's pre-registration first named `dropbench` as the effect arm, on the strength of that package's
// header calling its hot loop *"push/pop/branch"*. The sentence is true and it is not about a guest:
// `dropbench` is `import "testing"` and nothing else, modelling a stack shape in plain Go, and **it
// never executes a wasm instruction** — so it cannot see `jumpTo`, `runFrame`, or any part of the
// mechanism. *A pre-registration forecasts the instruments*, and the instrument was the unchecked
// premise. Narrowed on the issue before any figure was taken on either arm, because only that ordering
// distinguishes narrowing a forecast from amending a threshold.
//
// So this drives the real interpreter, the way `membench` does and its four older siblings do not: wat
// through `text.EncodeModule` → `binary.DecodeModule` → `Instantiate` → `Invoke`, so the timed path is
// the engine's own dispatch loop rather than a copy of it (*measure with the instrument, not a proxy*,
// and grave #125 on why a hand-built `binary.Module` measures a module the decoder never produced).
//
// # The two rows are a mechanism check, not a bar
//
// Both arms run the **same number of back-edges** — the trip count is a parameter and both loops are
// the same countdown — and differ only in how much straight-line arithmetic sits in the body. The poll
// therefore costs the same *absolute* amount in both, and its share of the row falls with the density:
//
//	Tight   5 instructions per back-edge
//	Wide   41 instructions per back-edge
//
// **The delta must fall between them by roughly that ratio.** A cost that appears equally in both rows
// is not the back-edge poll — it is something per-instruction, which is [ADR 0059] option A's shape and
// would mean the mechanism is not where the decision says it is. *A count is not a price: decompose by
// mechanism.* That is the whole reason there are two rows instead of one fast loop and a threshold.
//
// **Read it as an intercept and a slope, not as a pass or a fail** — and whether it can be read at all
// is **platform-scoped** ([#590](https://github.com/scttfrdmn/burroughs/issues/590)). Both arms run the
// same back-edge count and differ in runtime, so fitting the two deltas gives a term that does *not*
// scale with runtime — the per-back-edge cost — and one that does. On amd64 both rows resolved to ±0%
// and the fit is ~523 ps per back-edge against ≤20 ps per instruction: the cost is where option B put
// it. On arm64 only `Tight` resolved; `Wide`'s ±1% interval is wider than either hypothesis's predicted
// effect there, so its `~` was the output either way and the fit's second term is not measurable. The
// dilution that makes this a density check is the same operation that puts the effect under the row's
// noise floor, and #590 pre-registers the transpose — equal *total instructions*, varying back-edge
// count — which shares one noise floor between the rows instead of giving the diluted row the worse one.
//
// **The slope is an upper bound and not a measurement**, which is the part easiest to over-read. A
// build-to-build code-layout offset is multiplicative on runtime, so it is *indistinguishable from a
// per-instruction cost* in this instrument and lands entirely in the slope. The intercept is what
// survives that confound, because a fixed offset per binary cannot produce a term independent of
// runtime. So this package licenses "the cost is per-back-edge" and does not license any figure for a
// per-instruction term.
//
// # Why the trip count is large, which is not the usual reason
//
// `Invoke`'s fixed cost — a boundary crossing pair, a stack, an argument slice — does not dilute the two
// arms equally: `Tight` is the shorter row, so a fixed cost is a larger share of it, and it would shrink
// `Tight`'s measured percentage specifically. That does not merely add noise, it biases **the ratio this
// package exists to read**, in the direction that makes the mechanism look correctly placed. So the
// count is sized until the fixed cost is negligible in the *shorter* arm rather than in the average of
// the two — the sibling packages' N of 1000 would not have done it.
//
// [ADR 0059]: ../../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
package loopbench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// trips is how many loop back-edges one Invoke executes.
//
// See the package comment on why this is 100_000 and not the siblings' 1000: the figure being read is a
// *ratio between the arms*, and `Invoke`'s fixed cost biases the shorter arm's percentage downward.
// 100_000 trips is ~5ms in `Tight` on the machine this was written on, against a boundary crossing
// measured in microseconds.
const trips = 100_000

// wideGroups is how many 4-instruction arithmetic groups pad the `Wide` body.
//
// Nine, giving 36 padding instructions on top of the 5 the countdown itself costs — 41 per back-edge
// against `Tight`'s 5, a density ratio of 8.2. The padding accumulates into a local that the function
// returns, so it is not arithmetic a smarter engine could drop; the interpreter does no such thing
// today, and a body whose cost depends on that staying true would be measuring the absence of an
// optimization rather than the poll.
const wideGroups = 9

// tightInstrs is the instruction count of one `Tight` trip, and it is derived from the body below rather
// than counted by hand: `local.get $n`, `i32.const 1`, `i32.sub`, `local.tee $n`, `br_if`.
const tightInstrs = 5

// wideInstrs is the instruction count of one `Wide` trip: the same five, plus four per padding group
// (`local.get $acc`, `i32.const k`, `i32.add`, `local.set $acc`).
const wideInstrs = tightInstrs + 4*wideGroups

// buildModule renders both bodies into one module, so every arm runs against the same instance and no
// arm can differ by an instantiation.
//
// The countdown is written identically in both arms — `br_if` on a `local.tee`'d decrement — so "both
// arms execute the same number of back-edges" is a property of the generator rather than a claim to be
// checked afterwards. TestTheArmsDifferOnlyInBodyLength asserts it anyway, because a generator is only
// as trustworthy as the last edit to it.
func buildModule() string {
	var b strings.Builder
	b.WriteString("(module\n")

	b.WriteString("\t(func (export \"tight\") (param $n i32) (result i32)\n")
	b.WriteString("\t\t(loop $l\n")
	b.WriteString("\t\t\t(br_if $l (local.tee $n (i32.sub (local.get $n) (i32.const 1)))))\n")
	b.WriteString("\t\t(local.get $n))\n")

	b.WriteString("\t(func (export \"wide\") (param $n i32) (result i32) (local $acc i32)\n")
	b.WriteString("\t\t(loop $l\n")
	for i := range wideGroups {
		// Distinct constants per group so the padding is not `wideGroups` copies of one
		// expression — identical operands would make the row partly a fact about how the
		// immediate is decoded rather than about how many instructions ran.
		fmt.Fprintf(&b, "\t\t\t(local.set $acc (i32.add (local.get $acc) (i32.const %d)))\n", i+1)
	}
	b.WriteString("\t\t\t(br_if $l (local.tee $n (i32.sub (local.get $n) (i32.const 1)))))\n")
	b.WriteString("\t\t(local.get $acc))\n")

	b.WriteString(")")
	return b.String()
}

// build takes wat through the whole front end, which is `membench`'s `build` and is duplicated rather
// than shared for the reason the bench packages are separate at all: a helper shared across them would
// make one package's measurement depend on another's edits.
func build(src string) (*interp.Instance, error) {
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	in, trap := interp.Instantiate(m)
	if trap != nil {
		return nil, fmt.Errorf("instantiate: %w", trap)
	}
	return in, nil
}

// run is the timed body for both arms: one Invoke of `trips` back-edges.
//
// The first call is made outside the loop so a failure is reported as a failure rather than folded into
// the first iteration's time — `b.Fatalf` inside the timed loop would report a broken arm as a fast one.
func run(b *testing.B, name string) {
	b.Helper()
	in, err := build(buildModule())
	if err != nil {
		b.Fatalf("building the bench module: %v", err)
	}
	arg := interp.Value{Type: binary.I32, Bits: trips}
	if _, err := in.Invoke(name, arg); err != nil {
		b.Fatalf("invoke %s: %v", name, err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := in.Invoke(name, arg); err != nil {
			b.Fatalf("invoke %s: %v", name, err)
		}
	}
}

func BenchmarkTight(b *testing.B) { run(b, "tight") }
func BenchmarkWide(b *testing.B)  { run(b, "wide") }

// TestTheArmsDifferOnlyInBodyLength is the instrument's own control, and it is a test rather than a
// benchmark because a benchmark cannot fail an assertion — `make bench` is not `make check`, so an arm
// that stopped measuring its subject would go unreported until someone read the numbers and believed
// them.
//
// **The comparison this package makes is only as good as two claims**, and both are asserted here rather
// than trusted to the generator above. *Assert the arms differ, not only that the null matches.*
//
//  1. **The arms execute the same number of back-edges.** If they did not, the ratio between their
//     deltas would be partly a fact about their trip counts, and it is the ratio that carries the
//     mechanism claim. Checked behaviourally, not textually: `Wide` returns its accumulator, whose value
//     is `trips` × the sum of the padding constants, so the arithmetic pins the trip count exactly.
//  2. **The bodies differ by the density this package advertises.** A `Wide` arm that had drifted to the
//     same length as `Tight` would still produce a tidy percentage, and it would be the finding that
//     both arms are tight rather than a confirmation. *A comparison needs a vacuity check.*
//
// **Both watched die**, against a committed baseline (grave #589). Changing `Wide`'s countdown to
// `i32.const 2` — so it runs half the back-edges at the same body length — failed on *two independent
// channels*, the textual countdown check and the accumulator oracle at exactly half the expected total,
// which is what makes the trip-count claim checked rather than asserted. Setting `wideGroups` to 0
// collapsed `Wide` onto `Tight` and failed the density assertion at *"the arms are 5 and 5 instructions
// per back-edge"* — note that the padding count above did **not** fire on that one, correctly: it
// compares the generator against `wideGroups`, so it certifies consistency, and only the density
// assertion certifies the number the forecast is written against.
func TestTheArmsDifferOnlyInBodyLength(t *testing.T) {
	src := buildModule()

	// The instruction counts, by the mnemonics each arm is made of. One `br_if` and one `i32.sub`
	// per arm is the countdown; the `i32.add`s are the padding and belong to `Wide` alone.
	if got := strings.Count(src, "(br_if $l"); got != 2 {
		t.Errorf("the module contains %d `br_if $l`, want 2 — one loop back-edge per arm. Any "+
			"other number means the two arms are not the same loop, so the delta ratio between "+
			"them is a fact about their shapes rather than about back-edge density", got)
	}
	if got := strings.Count(src, "(i32.sub (local.get $n) (i32.const 1))"); got != 2 {
		t.Errorf("the module contains %d identical countdown decrements, want 2. The arms must "+
			"execute the same number of back-edges or the ratio this package reads is meaningless", got)
	}
	if got := strings.Count(src, "(i32.add (local.get $acc)"); got != wideGroups {
		t.Errorf("the module contains %d padding adds, want %d — `Wide` is not %d instructions "+
			"per back-edge, so the advertised density ratio is wrong and the mechanism check "+
			"below it cannot be read", got, wideGroups, wideInstrs)
	}
	// The density claim, stated as arithmetic so the package comment's two numbers cannot drift
	// from the generator. Not a floor: both directions are findings.
	if wideInstrs != 41 || tightInstrs != 5 {
		t.Errorf("the arms are %d and %d instructions per back-edge; the package comment and "+
			"#515's pre-registration both quote 41 and 5, and the forecast is that the delta "+
			"falls between the rows by roughly their ratio. Correct the prose and the "+
			"registration together, or the number a reader checks against is not the number "+
			"being measured", wideInstrs, tightInstrs)
	}

	// And both arms must run, which is the assertion that catches a body the validator rejects.
	in, err := build(src)
	if err != nil {
		t.Fatalf("building the bench module: %v", err)
	}
	arg := interp.Value{Type: binary.I32, Bits: trips}

	// `tight` counts down to zero and returns the counter, so the answer is 0 — weak on its own,
	// which is why the trip count is pinned through `wide`'s accumulator below instead.
	out, err := in.Invoke("tight", arg)
	if err != nil {
		t.Fatalf("invoke tight: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 0 {
		t.Errorf("tight returned %v, want a single 0 — the countdown did not reach zero, so the "+
			"loop ran a number of times this test cannot state", out)
	}

	// `wide` returns `trips` × the sum of 1..wideGroups. This is the trip-count oracle: a loop that
	// ran a different number of times lands on a different total, and an arm that skipped its
	// padding lands on a different total as well.
	var sum uint64
	for i := range wideGroups {
		sum += uint64(i + 1)
	}
	// Truncated to 32 bits deliberately: `$acc` is an i32 and `i32.add` wraps, so the oracle has to
	// wrap the same way or it would assert a value the guest cannot hold.
	want := uint64(uint32(uint64(trips) * sum))
	out, err = in.Invoke("wide", arg)
	if err != nil {
		t.Fatalf("invoke wide: %v", err)
	}
	if len(out) != 1 || out[0].Bits != want {
		t.Errorf("wide returned %v, want a single %d (= %d trips × %d, the sum of its padding "+
			"constants). A wrong total means either the trip count differs from `tight`'s — in "+
			"which case the two rows are not comparable — or the padding did not all execute, in "+
			"which case `Wide`'s advertised density is wrong", out, want, trips, sum)
	}
}
