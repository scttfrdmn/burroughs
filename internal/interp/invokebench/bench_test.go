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
//   - `EmptyNull` — byte-identical in source to `Empty`, so the pair is the within-run floor.
//     `growbench`'s `ResliceNull` is the precedent and [#580](https://github.com/scttfrdmn/burroughs/issues/580)
//     is the reason it is a row and not a sentence: a semantically inert diff moved unrelated rows 6–9%
//     on amd64, so a null at an unmeasured resolution cannot tell *no effect* from *an effect under the
//     floor*. 0067's criterion reads this arm explicitly — *compare the floor to the bar* — and if the
//     floor is not narrower than the bar, the board does not adjudicate.
//   - `TwoUncontendedLockUnlock` — the **bar**, measured here rather than recalled. Two `Lock`/`Unlock`
//     pairs on one uncontended `sync.Mutex`, which is exactly what 0067 adds per call: `enterCall` and
//     `leaveCall` both take `world.mu`. Plain Go, touching no engine code, deliberately — the claim it
//     supports is about what `sync.Mutex` costs on this machine on this run, not about this interpreter.
//     `growbench`'s `UncontendedLockUnlock` is the same device at a different multiplicity, and the
//     multiplicity is the whole reason this is a separate row: **two pairs per op there would be a
//     thousandth of the row and unreadable.**
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
