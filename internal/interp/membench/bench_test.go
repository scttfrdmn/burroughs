// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package membench measures ADR 0053's tear-freedom mechanism on the axis it turns on: whether the
// access is naturally aligned.
//
// **It exists because nothing in the tree could ask the question.** No benchmark here executed a wasm
// load or store before #557 — every `load`/`store` hit across `dispatchbench`, `dropbench`, `scanbench`
// and `vecbench` is prose in a comment, and `scanbench`'s module builder emits functions with no memory
// at all. That absence was measured while writing the ADR's pre-registration rather than discovered
// afterwards, which is the only reason a *forecast* about these rows was possible.
//
// **It drives the real interpreter, and it is the first bench package here that does.** Its four
// siblings all re-implement a stack shape locally, which is right when the subject is a data structure
// and wrong when the subject is a code path: a hand-copied `loadValue` would measure the copy. So the
// module below is parsed, validated and instantiated through the front end and the body runs under
// `Invoke` — *measure with the instrument, not a proxy.*
//
// The arms differ by **one byte of address** and nothing else. Same instruction, same count, same
// stride, same page: the unaligned arm's window is the aligned arm's shifted by one, so the two touch
// the same cache lines ±1 and the difference between them is the branch and the access, not the
// footprint.
//
// # What each arm is for
//
//   - `Aligned` rows take the word path. They are what the mechanism was written to speed up, and a
//     regression in them falsifies it.
//   - `Unaligned` rows **cannot** take it, and they are the within-instrument control: they pay the
//     predicate and gain nothing, so they are the rows most likely to embarrass the change. *An
//     unmeasured complement is not an empty one* — if these move, something other than the fast path
//     changed and the aligned figure is measuring that instead.
//
// Bodies are straight-line rather than looped, so the figure is the access and not a guest loop's
// bookkeeping. `accesses` is `dispatchbench`'s own N for its reason: one real loop body's trip count
// rather than a stress-test extreme.
package membench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// build takes wat through the whole front end — encode, decode, instantiate — which is the same chain
// `instantiate1` uses in the engine's own tests, and for the same reason (grave #125): a hand-built
// `binary.Module` would let the measurement run against a module the decoder never produced.
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
		return nil, fmt.Errorf("instantiate: %v", trap)
	}
	return in, nil
}

// accesses is how many loads or stores one Invoke performs.
//
// Large enough that `Invoke`'s own fixed cost — a boundary crossing pair, a stack, an argument slice
// — is a small share of the row rather than the row itself, and matched to the siblings' N so a reader
// comparing across bench packages is comparing the same trip count.
const accesses = 1000

// stride is the distance between successive addresses, in bytes.
//
// Four, so the aligned arm's addresses are exactly the 4-byte-aligned ones and the unaligned arm's are
// each one byte past them. `accesses * stride` is 4000 bytes, inside one page, so neither arm pays for
// a page it did not touch.
const stride = 4

// buildModule renders one module with all four bodies, so every arm runs against the same instance and
// no arm can differ by an instantiation.
func buildModule() string {
	var b strings.Builder
	b.WriteString("(module (memory 1)\n")
	for _, arm := range []struct {
		name string
		skew uint64
	}{{"aligned", 0}, {"unaligned", 1}} {
		fmt.Fprintf(&b, "\t(func (export \"load_%s\") (result i32) (local i32)\n", arm.name)
		for i := uint64(0); i < accesses; i++ {
			fmt.Fprintf(&b, "\t\t(local.set 0 (i32.add (local.get 0) (i32.load (i32.const %d))))\n",
				i*stride+arm.skew)
		}
		b.WriteString("\t\t(local.get 0))\n")

		fmt.Fprintf(&b, "\t(func (export \"store_%s\") (param i32)\n", arm.name)
		for i := uint64(0); i < accesses; i++ {
			fmt.Fprintf(&b, "\t\t(i32.store (i32.const %d) (local.get 0))\n", i*stride+arm.skew)
		}
		b.WriteString("\t\t)\n")
	}
	b.WriteString(")")
	return b.String()
}

// instance builds the module once per benchmark run, outside the timed loop.
//
// `b.Fatalf` rather than a returned error because a benchmark that cannot instantiate has no figure to
// report, and a zero would be indistinguishable from a fast one.
func instance(b *testing.B) *interp.Instance {
	b.Helper()
	in, err := build(buildModule())
	if err != nil {
		b.Fatalf("building the bench module: %v", err)
	}
	return in
}

// run is the timed body for every arm: one Invoke of `accesses` accesses.
//
// The result is consumed by the error check rather than discarded, so nothing here depends on whether
// Go's compiler can see through `Invoke` to eliminate a load — it cannot, since the accesses happen
// inside the interpreter's own dispatch loop over data the compiler has no view of.
func run(b *testing.B, name string, args ...interp.Value) {
	in := instance(b)
	// One call outside the loop, so a failure is reported as a failure rather than folded into the
	// first iteration's time.
	if _, err := in.Invoke(name, args...); err != nil {
		b.Fatalf("invoke %s: %v", name, err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := in.Invoke(name, args...); err != nil {
			b.Fatalf("invoke %s: %v", name, err)
		}
	}
}

func BenchmarkLoadAligned(b *testing.B)   { run(b, "load_aligned") }
func BenchmarkLoadUnaligned(b *testing.B) { run(b, "load_unaligned") }

func BenchmarkStoreAligned(b *testing.B) {
	run(b, "store_aligned", interp.Value{Type: binary.I32, Bits: 0x55667788})
}

func BenchmarkStoreUnaligned(b *testing.B) {
	run(b, "store_unaligned", interp.Value{Type: binary.I32, Bits: 0x55667788})
}

// TestTheArmsDifferOnlyInAlignment is the instrument's own control, and it is a test rather than a
// benchmark because a benchmark cannot fail an assertion.
//
// **The comparison this package makes is only as good as the claim that the arms are matched.** So the
// four bodies are checked to contain the same instruction count, and the address sets to differ by
// exactly the one-byte skew. *A comparison needs a vacuity check*: two arms that had drifted into
// different instruction counts would still produce a tidy percentage, and it would be a fact about the
// drift.
func TestTheArmsDifferOnlyInAlignment(t *testing.T) {
	src := buildModule()
	for _, arm := range []string{"load_aligned", "load_unaligned", "store_aligned", "store_unaligned"} {
		if !strings.Contains(src, `"`+arm+`"`) {
			t.Errorf("the module exports no %q, so that benchmark row would fail to invoke rather "+
				"than measure anything", arm)
		}
	}
	// Instruction counts per arm, by the access mnemonic each arm is made of.
	if got := strings.Count(src, "(i32.load (i32.const "); got != 2*accesses {
		t.Errorf("the module contains %d i32.loads, want %d — the two load arms are not the same "+
			"length, so any ratio between them is partly a fact about their sizes", got, 2*accesses)
	}
	if got := strings.Count(src, "(i32.store (i32.const "); got != 2*accesses {
		t.Errorf("the module contains %d i32.stores, want %d", got, 2*accesses)
	}
	// The skew, checked at both ends of the address range rather than by re-deriving the whole set:
	// address 0 and the last aligned address must appear, and each must have a +1 twin.
	last := uint64((accesses - 1) * stride)
	for _, addr := range []uint64{0, last} {
		for _, want := range []string{fmt.Sprintf("(i32.const %d)", addr), fmt.Sprintf("(i32.const %d)", addr+1)} {
			if !strings.Contains(src, want) {
				t.Errorf("the module never mentions %s, so the two arms do not cover the same "+
					"window offset by one byte", want)
			}
		}
	}
	// And the whole thing must actually run, which is the assertion that would catch a body the
	// validator rejects — a benchmark's `b.Fatalf` is invisible until someone runs `make bench`.
	in, err := build(src)
	if err != nil {
		t.Fatalf("building the bench module: %v", err)
	}
	for _, arm := range []string{"load_aligned", "load_unaligned"} {
		if _, err := in.Invoke(arm); err != nil {
			t.Errorf("invoke %s: %v", arm, err)
		}
	}
	for _, arm := range []string{"store_aligned", "store_unaligned"} {
		if _, err := in.Invoke(arm, interp.Value{Type: binary.I32, Bits: 1}); err != nil {
			t.Errorf("invoke %s: %v", arm, err)
		}
	}
}
