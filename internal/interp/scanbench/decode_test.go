// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package scanbench

// This file prices the side of #136's trade that nothing in the campaign asked about: what the
// pairing pass costs **at decode**, once per function, whether or not the function is ever
// entered.
//
// Every figure in `bench_test.go` is an execution-side figure, and so is [the flip's
// pre-registration](https://github.com/scttfrdmn/burroughs/issues/136#issuecomment-5446893104) —
// four spans of `Coupled`, the three unit costs, the `Straight` bias floor. All of it prices a
// module that is decoded once and then called a million times, which is the shape a benchmark
// naturally has and **is not the shape a flip has**. Flipping the table on makes every decoded
// module pay the pairing pass, including a module whose exported function is called once, and
// including every function in it that is never called at all. `matchEnd`'s cost is paid on
// entries; the table's cost is paid on *functions*, so the two sides are denominated in
// different populations and neither figure bounds the other.
//
// ADR 0048 named the transient allocation (~2× the final extent, reclaimed before `DecodeModule`
// returns) and said in as many words that it is a decode-time allocation figure rather than a
// retention figure. It gave no *time*. This file is that number.
//
// # Why three cases and not one
//
//   - `funcs=1/openers=1` is the corpus's own shape (0048: 2.23 functions per module, 1.59
//     openers in the 13.5% of bodies that have any), and it is dominated by fixed instantiate
//     cost — which is the honest answer for corpus-shaped modules and is reported as such rather
//     than discarded for being small.
//   - `funcs=200/openers=1` magnifies the per-function term so it can be *measured* rather than
//     inferred from a difference of two noisy fixed costs. The figure that transfers is the
//     marginal cost per function, derived by dividing; the Δ% of this row transfers to nothing,
//     because no corpus module has 200 functions.
//   - `funcs=200/openers=0` is the decode-side floor, and it is the same instrument as
//     `BenchmarkStraight` one phase earlier: with no structural opener anywhere, `fileFuncEnds`
//     files nothing, allocates nothing and sets no offset, so the table lane does strictly less
//     work than it does in the row above and cannot legitimately be slower. A Δ here is
//     build-boundary bias in the decode path, and it is subtracted from the row above rather
//     than assumed absent.
//
// The modules are memory-free and start-function-free so that `Instantiate` is decode plus
// validate plus wiring, not page allocation.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// decodeFuncs is the magnified function count. 200 is chosen to be large enough that the
// per-function term dominates instantiate's fixed cost and small enough that the module still
// assembles in well under a millisecond, so a `-count=10` sweep is minutes rather than hours.
const decodeFuncs = 200

// decodeCases are the three rows, named exactly as the file comment names them.
var decodeCases = []struct {
	funcs, openers int
}{
	{funcs: 1, openers: 1},
	{funcs: decodeFuncs, openers: 1},
	{funcs: decodeFuncs, openers: 0},
}

// modFuncs builds a module of n functions, each carrying `openers` loops of the corpus's median
// span. Every function is exported under its own name because an unexported, uncalled function is
// exactly the population this measurement is about — but the *decoder* does not care whether a
// function is exported, and the export section keeps the module's shape checkable from outside.
func modFuncs(n, openers int) string {
	var b strings.Builder
	b.WriteString("(module\n")
	for i := range n {
		fmt.Fprintf(&b, "  (func (export \"f%d\") (param i32) (result i32)\n    (local i32)", i)
		for range openers {
			fmt.Fprintf(&b, "\n    loop%s\n    end", loopCore)
		}
		b.WriteString("\n    local.get 1)\n")
	}
	b.WriteString(")\n")
	return b.String()
}

// BenchmarkInstantiate prices decode-plus-validate for the three shapes. The module is assembled
// once outside the timed loop, because assembling is text-reader work and has nothing to do with
// either lane.
func BenchmarkInstantiate(b *testing.B) {
	for _, c := range decodeCases {
		wasm, err := text.EncodeModule([]byte(modFuncs(c.funcs, c.openers)))
		if err != nil {
			b.Fatalf("assembling funcs=%d openers=%d: %v", c.funcs, c.openers, err)
		}
		b.Run(fmt.Sprintf("funcs=%d/openers=%d", c.funcs, c.openers), func(b *testing.B) {
			for b.Loop() {
				in, err := burroughs.Instantiate(wasm)
				if err != nil {
					b.Fatalf("instantiating: %v", err)
				}
				if in == nil {
					b.Fatal("instantiate returned a nil instance and no error")
				}
			}
		})
	}
}

// TestDecodeShapesHaveTheFunctionsAndOpenersClaimed is the vacuity check the benchmark cannot
// perform on itself, and it is the whole reason the openers are counted off the *decoded* form
// rather than off the text: a builder that silently emitted no `loop` would make the two lanes
// identical, both rows would agree, and the agreement would read as "the pairing pass is free".
//
// The count is over `Prefix == 0x00 && Op` in the structural set, the same technique
// `assertSpan` uses, and it runs in both builds because it asks nothing about `EndsOff`.
func TestDecodeShapesHaveTheFunctionsAndOpenersClaimed(t *testing.T) {
	for _, c := range decodeCases {
		t.Run(fmt.Sprintf("funcs=%d/openers=%d", c.funcs, c.openers), func(t *testing.T) {
			wasm, err := text.EncodeModule([]byte(modFuncs(c.funcs, c.openers)))
			if err != nil {
				t.Fatalf("assembling: %v", err)
			}
			m, err := binary.DecodeModule(wasm)
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if len(m.Funcs) != c.funcs {
				t.Fatalf("decoded %d functions, want %d", len(m.Funcs), c.funcs)
			}
			for i := range m.Funcs {
				if got := countOpeners(m.Funcs[i].Body); got != c.openers {
					t.Fatalf("function %d has %d openers, want %d — the openers=0 row is the "+
						"floor and the openers=1 row is the subject, so a shape that lost its "+
						"loops would make them the same measurement",
						i, got, c.openers)
				}
			}
			// The instance must also run, since a module that fails to instantiate would
			// benchmark as a fast error return.
			if _, err := burroughs.Instantiate(wasm); err != nil {
				t.Fatalf("instantiating: %v", err)
			}
		})
	}
}

// countOpeners counts structural block openers in a decoded body: `block`, `loop`, `if`,
// `try_table`. The same four opcodes `assertSpan` walks, and the same four the decoder's pairing
// pass files an entry for.
func countOpeners(body []binary.Instr) int {
	n := 0
	for _, ins := range body {
		if ins.Prefix != 0x00 {
			continue
		}
		switch ins.Op {
		case 0x02, 0x03, 0x04, 0x1f:
			n++
		}
	}
	return n
}
