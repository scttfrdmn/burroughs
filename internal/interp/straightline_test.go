// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// SP-1's straight-line reading (the chair's ruling of 2026-10-19, item 4). ADR 0059 leaves straight-line code
// unpolled by design, so a guest's arrival bound includes its longest poll-free stretch: the most instructions
// one agent can execute between two polls. Polls are at a back-edge (a branch to a `loop`, taken) and at every
// frame entry (so a call ends its caller's stretch). A bulk instruction polls inside itself.
//
// This file computes that stretch statically, as a longest path over each body's forward control flow, for the
// committed Go guest corpus and the Rust fixtures. `BenchmarkSP1StraightLine` turns the longest stretch into time
// by executing a straight-line body of that many instructions on the machine it runs on.

// pollFreeStretch is the longest poll-free path, in instructions, through one function body, and where it ends.
//
// The body is a DAG once back-edges are cut: every edge goes forward except a branch to a `loop`, which polls
// in `jumpTo`. A call polls at its callee's frame entry, so the instruction after a call starts a new stretch.
// So one forward pass suffices:
//   - `val[i]` is the longest run of instructions executed since the last poll, up to and including i;
//   - each instruction passes `val[i]` on to its successors;
//   - fallthrough goes to i+1, unless i transfers unconditionally;
//   - a forward branch goes to its block's `end`;
//   - an `if` goes to its `else`'s successor, or to its `end`;
//   - a call, or a taken back-edge, passes on 0.
//
// **Not modelled:** `br_on_cast`/`br_on_cast_fail` (0xfb 24/25) and `try_table`'s handler edges. Both are
// forward branches here, and a missed forward edge can only shorten a path, so the reading would understate for
// a module that uses them. Neither corpus does, which `TestStraightLineCorporaUseNoUnmodelledBranch` checks
// rather than this sentence asserting.
func pollFreeStretch(f *binary.Func) (longest, at int) {
	body := f.Body
	n := len(body)
	// Pass 1: each structured instruction's matching `end` (and an `if`'s `else`), and whether it is a loop.
	const none = -1
	endOf := make([]int, n)
	elseOf := make([]int, n)
	for i := range endOf {
		endOf[i], elseOf[i] = none, none
	}
	var stack []int
	for i, ins := range body {
		if ins.Prefix != 0 {
			continue
		}
		switch ins.Op {
		case 0x02, 0x03, 0x04, 0x06, 0x1f: // block, loop, if, try (legacy), try_table
			stack = append(stack, i)
		case 0x05: // else
			if len(stack) > 0 {
				elseOf[stack[len(stack)-1]] = i
			}
		case 0x0b: // end
			if len(stack) > 0 {
				endOf[stack[len(stack)-1]] = i
				stack = stack[:len(stack)-1]
			}
		}
	}
	// Pass 2: the longest path, forward.
	in := make([]int, n+1) // the best incoming run at each position; n is the function's own end
	reach := make([]bool, n+1)
	reach[0] = true
	give := func(to, v int) {
		if to > n {
			to = n
		}
		reach[to] = true
		in[to] = max(in[to], v)
	}
	stack = stack[:0]
	// target resolves a branch of depth d at the current control stack: the position it continues at, and
	// whether it is a back-edge.
	target := func(d uint64) (int, bool) {
		if int(d) >= len(stack) {
			return n, false // the function's own label: a return
		}
		h := stack[len(stack)-1-int(d)]
		if body[h].Op == 0x03 && body[h].Prefix == 0 {
			return h, true // a loop's label is its header: a back-edge, which polls
		}
		return endOf[h], false
	}
	for i, ins := range body {
		if !reach[i] {
			// Unreachable code: no path runs it. Its structure still has to be tracked for the stack.
			if ins.Prefix == 0 {
				switch ins.Op {
				case 0x02, 0x03, 0x04, 0x06, 0x1f:
					stack = append(stack, i)
				case 0x0b:
					if len(stack) > 0 {
						stack = stack[:len(stack)-1]
					}
				}
			}
			continue
		}
		v := in[i] + 1
		if v > longest {
			longest, at = v, i
		}
		out, falls := v, true
		if ins.Prefix == 0 {
			switch ins.Op {
			case 0x00, 0x0f: // unreachable, return
				falls = false
			case 0x10, 0x11, 0x14: // call, call_indirect, call_ref: the callee polls at entry
				out = 0
			case 0x12, 0x13, 0x15: // return_call*: the callee polls, and nothing here follows
				falls = false
			case 0x08, 0x0a: // throw, rethrow
				falls = false
			case 0x02, 0x03, 0x06, 0x1f:
				stack = append(stack, i)
			case 0x04: // if: the false edge to the else's successor, or to the end
				stack = append(stack, i)
				if e := elseOf[i]; e != none {
					give(e+1, out)
				} else if endOf[i] != none {
					give(endOf[i], out)
				}
			case 0x05: // else: the true arm's fallthrough jumps to the end
				if len(stack) > 0 {
					give(endOf[stack[len(stack)-1]], out)
				}
				falls = false
			case 0x0b: // end
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			case 0x0c, 0x0d, 0xd5, 0xd6: // br, br_if, br_on_null, br_on_non_null
				to, back := target(ins.Imm0)
				if back {
					give(to, 0)
				} else {
					give(to, out)
				}
				falls = ins.Op != 0x0c
			case 0x0e: // br_table: the vector, then the default in Imm0
				for _, d := range append(append([]uint32(nil), f.Labels[i]...), uint32(ins.Imm0)) {
					to, back := target(uint64(d))
					if back {
						give(to, 0)
					} else {
						give(to, out)
					}
				}
				falls = false
			}
		}
		if falls {
			give(i+1, out)
		}
	}
	return longest, at
}

// coreModulesOf returns the bytes of every core module a component embeds, recursively, or the file itself when
// it is a core module. It walks the component binary's sections and does not interpret them: a core module
// is section 1, and a nested component is section 4.
func coreModulesOf(b []byte) ([][]byte, error) {
	if len(b) < 8 {
		return nil, fmt.Errorf("too short for a preamble")
	}
	if b[4] == 0x01 && b[5] == 0x00 && b[6] == 0x00 && b[7] == 0x00 {
		return [][]byte{b}, nil // core module, version 1
	}
	var out [][]byte
	p := 8
	for p < len(b) {
		id := b[p]
		p++
		size, k := uleb(b[p:])
		p += k
		if p+int(size) > len(b) {
			return nil, fmt.Errorf("a section runs past the end")
		}
		payload := b[p : p+int(size)]
		switch id {
		case 1:
			out = append(out, payload)
		case 4:
			nested, err := coreModulesOf(payload)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
		}
		p += int(size)
	}
	return out, nil
}

func uleb(b []byte) (uint64, int) {
	var v uint64
	for i, c := range b {
		v |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return v, i + 1
		}
	}
	return v, len(b)
}

// straightLineCorpora are the guests item 4 names: the Go guest corpus (the fork's clause binaries) and the
// Rust fixtures (the p3 components and the async-lift parent).
var straightLineCorpora = []struct{ name, path string }{
	{"go/clause1", "../component/testdata/clause1/fork_hello_component.wasm"},
	{"go/clause2", "../wasi/testdata/clause2/c2_present.wasm"},
	{"go/clause3", "../wasi/testdata/clause3/c3_stopped.wasm"},
	{"rust/p3hello", "../component/testdata/p3hello.wasm"},
	{"rust/p3echo", "../component/testdata/p3echo.wasm"},
	{"rust/p3async-cancel", "../component/testdata/p3async-cancel.wasm"},
	{"rust/p3async-hello", "../component/testdata/p3async-hello.wasm"},
	{"rust/cancel-rust-parent", "../component/testdata/asynclift/cancel-rust-parent/composed.wasm"},
}

// longestStretchIn is a corpus file's longest poll-free stretch over every function of every core module.
func longestStretchIn(tb testing.TB, path string) (longest, funcs, fn int) {
	tb.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("the committed corpus file is missing: %v", err)
	}
	mods, err := coreModulesOf(b)
	if err != nil {
		tb.Fatalf("%s: %v", path, err)
	}
	all := binary.Features{
		SIMD: true, RelaxedSIMD: true, Threads: true, GC: true, TailCall: true,
		ExceptionHandling: true, Memory64: true, MultiMemory: true, ExtendedConst: true,
	}
	for _, mb := range mods {
		m, err := (&binary.Decoder{Features: all}).DecodeModule(mb)
		if err != nil {
			tb.Fatalf("%s: decoding a core module: %v", path, err)
		}
		for i := range m.Funcs {
			funcs++
			if s, _ := pollFreeStretch(&m.Funcs[i]); s > longest {
				longest, fn = s, i
			}
		}
	}
	return longest, funcs, fn
}

// TestStraightLineCorporaUseNoUnmodelledBranch: no corpus function contains a branch `pollFreeStretch` does not
// model (`br_on_cast`/`br_on_cast_fail`, or `try_table`), so the stretch it reports for them is not understated.
func TestStraightLineCorporaUseNoUnmodelledBranch(t *testing.T) {
	all := binary.Features{
		SIMD: true, RelaxedSIMD: true, Threads: true, GC: true, TailCall: true,
		ExceptionHandling: true, Memory64: true, MultiMemory: true, ExtendedConst: true,
	}
	for _, c := range straightLineCorpora {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("the committed corpus file is missing: %v", err)
		}
		mods, err := coreModulesOf(b)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		for _, mb := range mods {
			m, err := (&binary.Decoder{Features: all}).DecodeModule(mb)
			if err != nil {
				t.Fatalf("%s: %v", c.path, err)
			}
			for fi := range m.Funcs {
				for _, ins := range m.Funcs[fi].Body {
					if (ins.Prefix == 0xfb && (ins.Op == 24 || ins.Op == 25)) || (ins.Prefix == 0 && ins.Op == 0x1f) {
						t.Errorf("%s: function %d uses a branch the stretch reading does not model (prefix %#x, op %#x)",
							c.name, fi, ins.Prefix, ins.Op)
					}
				}
			}
		}
	}
}

// TestPollFreeStretchReadsKnownShapes holds the stretch computation to bodies whose answer is known by hand, so
// the corpus figure rests on a checked instrument.
func TestPollFreeStretchReadsKnownShapes(t *testing.T) {
	for _, c := range []struct {
		name string
		body []binary.Instr
		want int
	}{
		// i32.const, i32.const, i32.add, drop, end
		{"straight", []binary.Instr{{Op: 0x41}, {Op: 0x41}, {Op: 0x6a}, {Op: 0x1a}, {Op: 0x0b}}, 5},
		// a call resets: const, call, const, const, drop, drop, end. The run after the call is 5.
		{"a call ends a stretch", []binary.Instr{
			{Op: 0x41},
			{Op: 0x10},
			{Op: 0x41},
			{Op: 0x41},
			{Op: 0x1a},
			{Op: 0x1a},
			{Op: 0x0b},
		}, 5},
		// loop, const, br_if 0, end, end. The back-edge resets, and the longest run is loop..end, 5.
		{"a back-edge ends a stretch", []binary.Instr{
			{Op: 0x03},
			{Op: 0x41},
			{Op: 0x0d, Imm0: 0},
			{Op: 0x0b},
			{Op: 0x0b},
		}, 5},
		// if (const) else (const, const, const) end, end: the longer arm. if, const×3, end, end = 6.
		{"the longer if arm", []binary.Instr{
			{Op: 0x41},
			{Op: 0x04},
			{Op: 0x41},
			{Op: 0x05},
			{Op: 0x41},
			{Op: 0x41},
			{Op: 0x41},
			{Op: 0x0b},
			{Op: 0x0b},
		}, 7},
	} {
		got, _ := pollFreeStretch(&binary.Func{Body: c.body})
		if got != c.want {
			t.Errorf("%s: stretch %d, want %d", c.name, got, c.want)
		}
	}
}

// arrivalForStretch times `stretch` straight-line instructions on this machine. It builds a function whose body
// is that many instructions with no branch and no call, calls it in a loop, stops the guest `trials` times at
// random points, and reports arrival p50, p99 and max: `BenchmarkSP1Arrival`'s straight class at a given length.
func arrivalForStretch(b *testing.B, stretch int) {
	b.Helper()
	// `(local.set 0 (i32.add (local.get 0) (i32.const 1)))` is four instructions.
	reps := max(1, stretch/4)
	body := strings.Repeat("(local.set 0 (i32.add (local.get 0) (i32.const 1)))\n", reps)
	src := `(module
		(func $body (param i32) (result i32) ` + body + ` (local.get 0))
		(func (export "run") (param i32) (result i32) (local i32)
			(loop (local.set 1 (call $body (local.get 1)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(local.get 1)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		b.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		b.Fatalf("decode: %v", err)
	}
	in, trap := mustInst(b, m)
	if trap != nil {
		b.Fatalf("instantiate: %v", trap)
	}
	goInvoke(b, in, "run", I32(2_000_000_000)) // and waits for it, after Close (#1193)
	const trials = 200
	var arrivals []time.Duration
	rng := rand.New(rand.NewPCG(1, uint64(stretch)))
	for b.Loop() {
		arrivals = arrivals[:0]
		for i := range trials {
			until := time.Now().Add(time.Duration(rng.Int64N(int64(4 * time.Millisecond))))
			for time.Now().Before(until) {
			}
			t0 := time.Now()
			if err := in.Stop(30 * time.Second); err != nil {
				b.Fatalf("trial %d: Stop: %v", i, err)
			}
			d := time.Since(t0)
			in.Resume()
			if i > 0 {
				arrivals = append(arrivals, d)
			}
		}
	}
	slices.Sort(arrivals)
	pct := func(p float64) time.Duration { return arrivals[int(p*float64(len(arrivals)-1))] }
	b.ReportMetric(float64(pct(0.50).Nanoseconds()), "arrival-p50-ns")
	b.ReportMetric(float64(pct(0.99).Nanoseconds()), "arrival-p99-ns")
	b.ReportMetric(float64(arrivals[len(arrivals)-1].Nanoseconds()), "arrival-max-ns")
}

// BenchmarkSP1StraightLine is item 4's reading. For each corpus file it reports the longest poll-free stretch
// statically (`stretch-instrs`). It then times a straight-line body of that many instructions on this machine,
// as `BenchmarkSP1Arrival`'s straight class does for its 80,000: stopped at random points, arrival p50, p99 and
// max. Run it with `-benchtime=1x` on janus's `measured` group, where the figures are read against #515's
// forecast.
//
// **The time is a reading of a stretch, not of the guest.** A body of `local.set` adds is the cheapest
// straight-line code an interpreter runs, so a real stretch of the same length may take longer per
// instruction. The static count is the bound's subject, and the timed body is its scale.
func BenchmarkSP1StraightLine(b *testing.B) {
	for _, c := range straightLineCorpora {
		b.Run("corpus="+c.name, func(b *testing.B) {
			longest, funcs, fn := longestStretchIn(b, c.path)
			arrivalForStretch(b, longest)
			// After the timed part, because `b.Loop` discards metrics reported before it.
			b.ReportMetric(float64(longest), "stretch-instrs")
			b.ReportMetric(float64(funcs), "funcs")
			b.ReportMetric(float64(fn), "at-func")
		})
	}
}
