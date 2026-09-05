// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package invokebench measures `Invoke`'s own fixed cost — the boundary crossing, with as little guest
// work behind it as a module can declare — and exists because [decision 0067][0067] adds two critical
// sections to that boundary and **nothing already here could have priced them**.
//
// # Why a sixth bench package rather than a row in one of the five
//
// Every `Invoke`-driving package in this tree is built to make `Invoke`'s fixed cost *invisible*, on
// purpose, and each says so: `growbench` runs `grows = 1000` grows per call under the note that
// *"`Invoke`'s own fixed cost has to be a small share of the row"*, `membench` and `rmwbench` run
// `accesses = 1000`, and `loopbench` and `globalbench` run `trips = 100_000`. That is correct for their
// subjects — a per-call constant added to a per-operation figure is a bias — and it makes every one of
// them the wrong instrument for a per-call constant. 0067's first draft named `growbench` as the
// *sharpest* arm on a guess about its guest body; the fixture says the opposite, by three orders of
// magnitude. So the dilution is the reason this package exists, and the reason it inverts the
// convention: **one `Invoke` per op, and the smallest guest body a module can export.**
//
// # What the rows are, and what each can and cannot falsify
//
//   - `Empty` — one `Invoke` of `(func (export "nop"))`: no parameters, no results, no locals, an empty
//     body. **This is the sensitive arm.** What it times is export lookup, the stack and frame setup,
//     `run`'s `enterFrame` and its safepoint poll, the `end`, and — after 0067 — `enterCall` and
//     `leaveCall`. The two new pairs are the largest share of a row they will ever be anywhere in this
//     engine, which is the point and also the caveat: **this row is an upper bound on 0067's cost, not a
//     typical one.** A workload that runs a thousand guest operations per call dilutes the same two pairs
//     by a thousand.
//
//   - `EmptyNull` — byte-identical in source to `Empty`, so the pair is the within-run floor.
//     `growbench`'s `ResliceNull` is the precedent and [#580](https://github.com/scttfrdmn/burroughs/issues/580)
//     is the reason it is a row and not a sentence: a semantically inert diff moved unrelated rows 6–9%
//     on amd64, so a null at an unmeasured resolution cannot tell *no effect* from *an effect under the
//     floor*. 0067's criterion reads this arm explicitly — *compare the floor to the bar* — and if the
//     floor is not narrower than the bar, the board does not adjudicate.
//
//   - `TwoUncontendedLockUnlock` — the **bar**, measured here rather than recalled. Two `Lock`/`Unlock`
//     pairs on one uncontended `sync.Mutex`, which is exactly what 0067 adds per call: `enterCall` and
//     `leaveCall` both take `world.mu`. Plain Go, touching no engine code, deliberately — the claim it
//     supports is about what `sync.Mutex` costs on this machine on this run, not about this interpreter.
//     `growbench`'s `UncontendedLockUnlock` is the same device at a different multiplicity, and the
//     multiplicity is the whole reason this is a separate row: **two pairs per op there would be a
//     thousandth of the row and unreadable.**
//
//     **It is not invariant across the arms of one A/B, which is what #580 says arriving on the row a
//     criterion divides by.** Measured on #650's board: −7.53% at p=0.000 with ±0% spreads in the head
//     arm, while base and null agreed at 20.98–20.99 ns, on a diff that cannot reach a function-local
//     mutex and with `--graft` giving all three arms this exact source. So *"on this run"* above is not
//     enough — the value is per **binary**, and a fraction-of-bar figure carries the slop of whichever
//     arm's bar it was divided by ([#653](https://github.com/scttfrdmn/burroughs/issues/653), which is
//     the denominator half of [#580](https://github.com/scttfrdmn/burroughs/issues/580)). Take the bar
//     from the base arm until one of them is decided, and quote the arms that agree.
//
//     **#580's citation four lines above this one is why grave
//     [#654](https://github.com/scttfrdmn/burroughs/issues/654) exists**: the phenomenon was filed as a
//     discovery with its prior filing already cited in this comment. Read the citations in the file
//     before opening an issue about the instrument the file documents.
//
// # Two things a reader has to know before comparing rows
//
// **`Empty` is per `Invoke`, not per guest instruction**, unlike every other bench package here. No
// division. A reader carrying over the habit from `membench` would divide by a trip count that does not
// exist and read the boundary as a thousandth of its cost.
//
// **The bar is not comparable to `growbench`'s bar without rescaling.** That one is `grows = 1000` pairs
// per op; this one is two. The rows differ by ~500× for reasons that are entirely about the fixtures.
//
// # Why the guest body is empty rather than one instruction
//
// An empty body is the floor of what the front end will accept, and the arm wants the *boundary* isolated
// — any instruction at all is guest work that dilutes the subject, which is the mistake this package was
// built to correct. It is not a degenerate module: it decodes, validates, instantiates and runs through
// the same path as any other, so the row is a real `Invoke` and not a harness measuring itself.
//
// [0067]: ../../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md
package invokebench

import (
	"strings"
	"sync"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// src is the whole fixture: one export, no signature, no body.
const src = `(module (func (export "nop")))`

// build takes wat through encode, decode and instantiate.
//
// The threads gate is **off**, and that is not an oversight: this module declares no memory and no
// atomic, so there is nothing for the gate to admit, and leaving it off keeps the arm's decoder
// configuration at the engine's default. 0067's mechanism is not gated — `enterCall` sits on the
// unconditional `Invoke` path — so a measurement of it must not be taken behind a feature flag that
// could later be read as its precondition.
func build(tb testing.TB) *interp.Instance {
	tb.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		tb.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{}).DecodeModule(img)
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}
	in, trap := interp.Instantiate(m)
	if trap != nil {
		tb.Fatalf("instantiate: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		tb.Fatalf("instantiate fell short: %v", derr)
	}
	return in
}

// empty is the timed body for both `Invoke` rows: one instance, reused, since a call that executes no
// instruction leaves it exactly as it found it.
func empty(b *testing.B) {
	b.Helper()
	in := build(b)
	// One call outside the loop, so a failure is a failure rather than folded into the first
	// iteration's time.
	if _, err := in.Invoke("nop"); err != nil {
		b.Fatalf("invoke nop: %v", err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := in.Invoke("nop"); err != nil {
			b.Fatalf("invoke nop: %v", err)
		}
	}
}

// BenchmarkEmpty is the sensitive arm: one boundary crossing per op and no guest work behind it.
func BenchmarkEmpty(b *testing.B) { empty(b) }

// BenchmarkEmptyNull is byte-identical in source to BenchmarkEmpty and is the within-run floor.
func BenchmarkEmptyNull(b *testing.B) { empty(b) }

// BenchmarkTwoUncontendedLockUnlock is 0067's acceptance bar, measured on the same run as the row it
// bounds.
//
// Two pairs and one mutex, matching `enterCall`/`leaveCall`: they take the same `world.mu`, so a bar built
// from two *different* mutexes would measure two cold cache lines where the engine has one.
func BenchmarkTwoUncontendedLockUnlock(b *testing.B) {
	var mu sync.Mutex
	for range b.N {
		mu.Lock()
		mu.Unlock() //nolint:staticcheck // SA2001 is about empty critical sections; an empty one is exactly the subject here.
		mu.Lock()
		mu.Unlock() //nolint:staticcheck // SA2001 is about empty critical sections; an empty one is exactly the subject here.
	}
}

// TestTheArmMeasuresOneInvokeOfAnEmptyBody pins the two fixture properties the rows' readings rest on,
// because both are assertions about the *source* and both are silently falsifiable by an edit.
//
// A trip count that grew to 1000 here would turn this package into the diluted instrument it was built to
// replace, and the row would keep printing a plausible number. *A literal duplicating a type's property
// is correct once* — so the emptiness is derived from the fixture rather than restated.
func TestTheArmMeasuresOneInvokeOfAnEmptyBody(t *testing.T) {
	// The body is empty: the export's parenthesis closes immediately after its name.
	if !strings.Contains(src, `(export "nop")))`) {
		t.Errorf("the fixture's exported function is no longer empty-bodied — the arm's whole claim is\n"+
			"that the row is boundary and not guest work, and any instruction here dilutes it:\n%s", src)
	}
	// And there is exactly one function, so `Invoke` cannot be amortised across calls.
	//
	// No trailing space in the needle, and the space is why this clause was **stillborn** when it was
	// first written: `(func ` misses a bodyless `(func)`, so the mutation that added a second function
	// as `(module (func) (func (export "nop")))` passed a control written to catch exactly it. The
	// watched failure is what found that, not review — *a pattern carries conditions a predicate drops*,
	// and the dropped condition was a character.
	if got := strings.Count(src, "(func"); got != 1 {
		t.Errorf("the fixture declares %d functions, want 1: more than one means a row could be\n"+
			"measuring a call chain rather than one boundary crossing", got)
	}

	// The instance really runs it. A fixture that stopped instantiating would fail every row rather
	// than reporting a wrong number, but a fixture whose export was renamed would fail them at
	// measurement time instead of here.
	in := build(t)
	res, err := in.Invoke("nop")
	if err != nil {
		t.Fatalf("invoke nop: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("nop returned %d results, want 0: the arm's fixture declares no result type, so a\n"+
			"non-empty return means the module under measurement is not the one described", len(res))
	}
}

// hostSrc is the host-call fixture: one imported host function and one exported wrapper whose whole body
// is the call to it. Added by [ADR 0070][0070], which folds a repair into the `defer` `callHost` already
// had — and **nothing in this tree could price that site**, because every row above stops at the guest
// boundary and never crosses back out to an embedder.
//
// [0070]: ../../../docs/decisions/0070-an-embedder-panic-is-repaired-inside-the-defers-that-already-exist.md
const hostSrc = `(module (import "h" "nop" (func $nop)) (func (export "call") (call $nop)))`

// buildHost is `build` with the import supplied. Separate rather than parameterised, because the two
// fixtures differ in the *linking* call as well as the source and a shared helper would have to branch on
// which one it was building.
func buildHost(tb testing.TB, calls *int) *interp.Instance {
	tb.Helper()
	img, err := text.EncodeModule([]byte(hostSrc))
	if err != nil {
		tb.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{}).DecodeModule(img)
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}
	// The cheapest host function that can exist: no parameters, no results, no allocation, and a
	// counter the control below reads. The row is the *boundary*, so anything the embedder's own
	// function does is dilution of exactly the kind this package was built to remove.
	nop := interp.HostExtern(binary.FuncType{}, func(*interp.Caller, []interp.Value) ([]interp.Value, error) {
		*calls++
		return nil, nil
	})
	in, trap, lerr := interp.InstantiateLinked(m, func(module, name string) (interp.Extern, bool) {
		if module == "h" && name == "nop" {
			return nop, true
		}
		return interp.Extern{}, false
	})
	if lerr != nil {
		tb.Fatalf("link: %v", lerr)
	}
	if trap != nil {
		tb.Fatalf("instantiate: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		tb.Fatalf("instantiate fell short: %v", derr)
	}
	return in
}

// hostCall is the timed body for both host-call rows: one `Invoke` and one host call per op.
//
// **The counter is incremented inside the timed loop on purpose, and it is one integer store.** The
// alternative — a host function that does nothing at all — cannot be told apart from a fixture whose call
// never happens, and the control below is what reads it.
func hostCall(b *testing.B) {
	b.Helper()
	calls := 0
	in := buildHost(b, &calls)
	if _, err := in.Invoke("call"); err != nil {
		b.Fatalf("invoke call: %v", err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := in.Invoke("call"); err != nil {
			b.Fatalf("invoke call: %v", err)
		}
	}
	b.StopTimer()
	if calls != b.N+1 {
		b.Fatalf("the host function ran %d times over %d timed ops plus one warm-up: the row is "+
			"priced per host call, so any other count means it is measuring something else", calls, b.N)
	}
}

// BenchmarkHostCall is the sensitive arm for the host-call boundary: one `Invoke` plus one crossing out to
// an embedder and back, with no guest work behind either.
//
// It carries `Empty`'s cost as well as its own, so a reader comparing the two rows is reading the *host
// call's* share as the difference and not this row's absolute.
func BenchmarkHostCall(b *testing.B) { hostCall(b) }

// BenchmarkHostCallNull is byte-identical in source to BenchmarkHostCall and is this row's own within-run
// floor. `EmptyNull`'s floor does not transfer: it was measured on a row of a different magnitude, and a
// resolution is a fraction of what it was taken on.
func BenchmarkHostCallNull(b *testing.B) { hostCall(b) }

// TestTheHostArmMeasuresOneHostCallPerInvoke pins the host fixture's properties, on the same ground as the
// control above: every one of them is an assertion about the *source* that an edit falsifies silently.
func TestTheHostArmMeasuresOneHostCallPerInvoke(t *testing.T) {
	// Exactly one import and one defined function, so the row is one crossing out and one back rather
	// than a chain. Counted as two `(func` — the import's declaration and the definition — with the
	// import counted separately so that deleting the import changes both numbers rather than neither.
	if got := strings.Count(hostSrc, "(func"); got != 2 {
		t.Errorf("the host fixture declares %d `(func`, want 2 (one imported, one defined): more means\n"+
			"a row could be timing a call chain rather than one host boundary crossing", got)
	}
	if got := strings.Count(hostSrc, "(import"); got != 1 {
		t.Errorf("the host fixture has %d imports, want 1", got)
	}
	// And the exported wrapper's body is the call and nothing else: the closing parens follow it
	// immediately. `(export "call")` then `(call $nop)` then three closes — wrapper, func, module.
	if !strings.Contains(hostSrc, `(export "call") (call $nop)))`) {
		t.Errorf("the host fixture's exported body is no longer just the host call, so the row has\n"+
			"guest work in it and stops being a boundary measurement:\n%s", hostSrc)
	}

	// The host function really runs, once per `Invoke`. A fixture whose import resolved to something
	// else, or whose wrapper stopped calling it, would print a plausible number for a row that never
	// crosses the boundary it claims to price.
	calls := 0
	in := buildHost(t, &calls)
	for i := range 3 {
		if _, err := in.Invoke("call"); err != nil {
			t.Fatalf("invoke call: %v", err)
		}
		if calls != i+1 {
			t.Fatalf("after %d invokes the host function has run %d times, want %d", i+1, calls, i+1)
		}
	}
}
