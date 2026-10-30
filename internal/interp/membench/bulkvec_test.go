// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package membench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The bulk and SIMD rows, for #1167: what making each extent's partial edge words atomic costs ADR 0064's
// plain region. Nothing measured these paths before (the existing rows are typed i32 accesses), so this file is
// the instrument the forecast on #1167 is written against.
//
// **Each pair differs by one byte of address and nothing else**, the package's own convention. An extent at
// a word boundary with a whole number of words has no partial edge word. The same extent shifted by one has
// two: a 3-byte head and a 1-byte tail. So the aligned row is the plain interior's cost, and the unaligned row
// adds exactly the edges. `fill_page` is a page-sized unaligned fill, where the interior dominates: the edges'
// cost relative to a long extent.
//
// Bodies are straight-line, `bulkOps` instructions each, so the figure is the instruction and not a loop.
const bulkOps = 1000

// bulkStride spaces successive extents so none shares a word with the next (12-byte extents, 16 apart).
const bulkStride = 16

// bulkLen is a small extent's length: three whole words when aligned, and a 3-byte head, two whole words and a
// 1-byte tail when shifted by one.
const bulkLen = 12

func buildBulkVecModule() string {
	var b strings.Builder
	// Two pages: copies read the first and write the second, so a copy's source and destination never overlap.
	b.WriteString("(module (memory 2) (data $d \"abcdefghijkl\")\n")
	for _, arm := range []struct {
		name string
		skew uint64
	}{{"aligned", 0}, {"unaligned", 1}} {
		fmt.Fprintf(&b, "\t(func (export \"fill_%s\")\n", arm.name)
		for i := range uint64(bulkOps) {
			fmt.Fprintf(&b, "\t\t(memory.fill (i32.const %d) (i32.const 7) (i32.const %d))\n", i*bulkStride+arm.skew, bulkLen)
		}
		b.WriteString("\t)\n")

		// The destination's skew is the arm's, and the source's is the arm's plus one, so the unaligned arm
		// has partial edges on both sides.
		fmt.Fprintf(&b, "\t(func (export \"copy_%s\")\n", arm.name)
		for i := range uint64(bulkOps) {
			src := i*bulkStride + arm.skew*2
			fmt.Fprintf(&b, "\t\t(memory.copy (i32.const %d) (i32.const %d) (i32.const %d))\n",
				65536+i*bulkStride+arm.skew, src, bulkLen)
		}
		b.WriteString("\t)\n")

		fmt.Fprintf(&b, "\t(func (export \"init_%s\")\n", arm.name)
		for i := range uint64(bulkOps) {
			fmt.Fprintf(&b, "\t\t(memory.init $d (i32.const %d) (i32.const 0) (i32.const %d))\n", i*bulkStride+arm.skew, bulkLen)
		}
		b.WriteString("\t)\n")

		fmt.Fprintf(&b, "\t(func (export \"v128store_%s\")\n", arm.name)
		for i := range uint64(bulkOps) {
			fmt.Fprintf(&b, "\t\t(v128.store (i32.const %d) (v128.const i32x4 1 2 3 4))\n", i*bulkStride*2+arm.skew)
		}
		b.WriteString("\t)\n")

		fmt.Fprintf(&b, "\t(func (export \"v128load_%s\") (result v128) (local v128)\n", arm.name)
		for i := range uint64(bulkOps) {
			fmt.Fprintf(&b, "\t\t(local.set 0 (v128.load (i32.const %d)))\n", i*bulkStride*2+arm.skew)
		}
		b.WriteString("\t\t(local.get 0))\n")
	}
	// A one-byte lane store is always a partial word, so it has no aligned twin: its row is what the edge
	// costs a single-byte SIMD store.
	b.WriteString("\t(func (export \"store8lane\")\n")
	for i := range uint64(bulkOps) {
		fmt.Fprintf(&b, "\t\t(v128.store8_lane 0 (i32.const %d) (v128.const i32x4 1 2 3 4))\n", i*bulkStride)
	}
	b.WriteString("\t)\n")
	// The page-sized fill: 16 of them per call, each 4096 bytes less 2, starting one byte in.
	b.WriteString("\t(func (export \"fill_page\")\n")
	for i := range uint64(16) {
		fmt.Fprintf(&b, "\t\t(memory.fill (i32.const %d) (i32.const 7) (i32.const 4094))\n", i*4096+1)
	}
	b.WriteString("\t)\n")
	// The control's probe, not a measured body.
	b.WriteString("\t(func (export \"peek\") (param i32) (result i32) (i32.load8_u (local.get 0)))\n")
	b.WriteString(")")
	return b.String()
}

func runBulkVec(b *testing.B, name string) {
	b.Helper()
	in, err := build(buildBulkVecModule())
	if err != nil {
		b.Fatalf("building the bulk/vec module: %v", err)
	}
	if _, err := in.Invoke(name); err != nil {
		b.Fatalf("invoke %s: %v", name, err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := in.Invoke(name); err != nil {
			b.Fatalf("invoke %s: %v", name, err)
		}
	}
}

func BenchmarkFillAligned(b *testing.B)        { runBulkVec(b, "fill_aligned") }
func BenchmarkFillUnaligned(b *testing.B)      { runBulkVec(b, "fill_unaligned") }
func BenchmarkFillPage(b *testing.B)           { runBulkVec(b, "fill_page") }
func BenchmarkCopyAligned(b *testing.B)        { runBulkVec(b, "copy_aligned") }
func BenchmarkCopyUnaligned(b *testing.B)      { runBulkVec(b, "copy_unaligned") }
func BenchmarkInitAligned(b *testing.B)        { runBulkVec(b, "init_aligned") }
func BenchmarkInitUnaligned(b *testing.B)      { runBulkVec(b, "init_unaligned") }
func BenchmarkV128StoreAligned(b *testing.B)   { runBulkVec(b, "v128store_aligned") }
func BenchmarkV128StoreUnaligned(b *testing.B) { runBulkVec(b, "v128store_unaligned") }
func BenchmarkV128LoadAligned(b *testing.B)    { runBulkVec(b, "v128load_aligned") }
func BenchmarkV128LoadUnaligned(b *testing.B)  { runBulkVec(b, "v128load_unaligned") }
func BenchmarkStore8Lane(b *testing.B)         { runBulkVec(b, "store8lane") }

// TestTheBulkAndVecArmsLandWhereTheirNamesClaim is this file's instrument control. Every body runs, and the
// aligned and unaligned arms write the same extent one byte apart. An arm that wrote nowhere, or whose skew
// had drifted, would still produce a tidy figure, so each arm runs on a fresh instance and its extent's ends
// are read back.
func TestTheBulkAndVecArmsLandWhereTheirNamesClaim(t *testing.T) {
	fresh := func(name string) *interp.Instance {
		t.Helper()
		in, err := build(buildBulkVecModule())
		if err != nil {
			t.Fatalf("building the bulk/vec module: %v", err)
		}
		if _, err := in.Invoke(name); err != nil {
			t.Fatalf("invoke %s: %v", name, err)
		}
		return in
	}
	peek := func(in *interp.Instance, addr uint32) uint64 {
		t.Helper()
		out, err := in.Invoke("peek", interp.Value{Type: binary.I32, Bits: uint64(addr)})
		if err != nil {
			t.Fatalf("peek %d: %v", addr, err)
		}
		return out[0].Bits
	}
	// The second extent of each arm, so a byte before it belongs to the first extent's gap.
	for _, c := range []struct {
		body       string
		first, end uint32 // the extent's first byte and one past its last
		want       uint64 // the value at its first byte
	}{
		{"fill_aligned", 16, 16 + bulkLen, 7},
		{"fill_unaligned", 17, 17 + bulkLen, 7},
		{"init_aligned", 16, 16 + bulkLen, 'a'},
		{"init_unaligned", 17, 17 + bulkLen, 'a'},
		{"v128store_aligned", 32, 48, 1},
		{"v128store_unaligned", 33, 49, 1},
	} {
		in := fresh(c.body)
		if got := peek(in, c.first); got != c.want {
			t.Errorf("%s: byte %d is %d, want %d: the extent does not start where the arm claims", c.body,
				c.first, got, c.want)
		}
		if got := peek(in, c.first-1); got != 0 {
			t.Errorf("%s: byte %d before the extent is %d, want 0", c.body, c.first-1, got)
		}
		if got := peek(in, c.end); got != 0 {
			t.Errorf("%s: byte %d after the extent is %d, want 0", c.body, c.end, got)
		}
	}
	// The bodies whose result is not a byte in memory still have to run.
	for _, body := range []string{
		"copy_aligned", "copy_unaligned", "v128load_aligned", "v128load_unaligned",
		"store8lane", "fill_page",
	} {
		fresh(body)
	}
}
