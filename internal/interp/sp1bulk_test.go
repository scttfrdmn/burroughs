// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
	"github.com/scttfrdmn/burroughs/internal/validate"
)

// The table and GC-array bulk instructions' structural witnesses (contract §3 SP-1, ADR 0104; the chair's
// ruling of 2026-10-19, item 3). Each instruction runs in `elemChunk` pieces with a poll between them
// (`eachPiece`), and each witness holds it to item 1's boundary rule: a stop can park the agent inside the
// instruction, every observed stop point is a multiple of `elemChunk`, and some point is not a multiple of
// twice it.
//
// NEUTERS (run): `eachPiece` with its poll removed, and no stop lands inside any instruction; `eachPiece`
// stepping `elemChunk*64`, and stops still land inside, but the interval check fails.

// bulkWitnessLen is each observable destination's length in elements: 1 Mi, so 256 pieces per instruction.
const bulkWitnessLen = 1 << 20

// bulkWitness is one instruction class's guest: an export looping the instruction over a destination of
// bulkWitnessLen elements, alternating between two sources (or two fill values) per trip, so each trip
// overwrites the destination with something different. `read` returns a comparable key for destination
// element k, read while the agent is parked. Every guest counts its trips in the exported global "it".
type bulkWitness struct {
	name string
	wat  string
	read func(in *Instance, k int) uint64
}

// funcAddr is a table slot's or a funcref element's function index, the key the funcref witnesses compare.
func funcAddr(r ref) uint64 {
	if r.Null {
		return 1 << 63
	}
	return uint64(r.Addr)
}

// arrayGlobal is the array the exported global "x" holds, on the host thread, which is the thread
// `Invoke` runs the guest on.
func arrayGlobal(t *testing.T, in *Instance) *gcObj {
	t.Helper()
	idx, ok := in.exportedGlobal("x")
	if !ok {
		t.Fatal(`the witness module exports no global "x"`)
	}
	g, err := in.globalFor(&in.host, "the witness", uint64(idx))
	if err != nil {
		t.Fatalf("global x: %v", err)
	}
	return g.loadRef().Obj
}

func bulkWitnesses(t *testing.T) []bulkWitness {
	n := itoa(bulkWitnessLen)
	seg0 := strings.Repeat(" $f0", bulkWitnessLen)
	seg1 := strings.Repeat(" $f1", bulkWitnessLen)
	data0 := strings.Repeat(`\07`, bulkWitnessLen)
	data1 := strings.Repeat(`\08`, bulkWitnessLen)
	// trip is the loop body's tail every guest shares: count the trip, then loop.
	const trip = `(global.set $it (i32.add (global.get $it) (i32.const 1)))
		(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0))`
	// parity is 0 or 1 by trip, choosing which of two sources this trip writes.
	const parity = `(i32.and (global.get $it) (i32.const 1))`
	// `elem declare` makes `ref.func $f0`/`$f1` valid in a body. The first draft lacked it, and the validator
	// `gcInst` now runs refused table.fill's and table.copy's modules.
	common := `(global $it (export "it") (mut i32) (i32.const 0)) (func $f0) (func $f1) (elem declare func $f0 $f1)`
	tableRead := func(in *Instance, k int) uint64 { return funcAddr(in.tables[0].view()[k]) }
	return []bulkWitness{
		{"table.fill", `(module ` + common + ` (table $t ` + n + ` funcref)
			(func (export "run") (param i32) (result i32)
				(loop (table.fill $t (i32.const 0)
						(select (result funcref) (ref.func $f0) (ref.func $f1) ` + parity + `) (i32.const ` + n + `))
					` + trip + `)
				(i32.const 0)))`, tableRead},
		{"table.copy", `(module ` + common + ` (table $t ` + itoa(3*bulkWitnessLen) + ` funcref)
			(func (export "run") (param i32) (result i32)
				(table.fill $t (i32.const ` + n + `) (ref.func $f0) (i32.const ` + n + `))
				(table.fill $t (i32.const ` + itoa(2*bulkWitnessLen) + `) (ref.func $f1) (i32.const ` + n + `))
				(loop (table.copy $t $t (i32.const 0)
						(i32.mul (i32.add (i32.const 1) ` + parity + `) (i32.const ` + n + `)) (i32.const ` + n + `))
					` + trip + `)
				(i32.const 0)))`, tableRead},
		{"table.init", `(module ` + common + ` (table $t ` + n + ` funcref)
			(elem $e0 func` + seg0 + `) (elem $e1 func` + seg1 + `)
			(func (export "run") (param i32) (result i32)
				(loop (if ` + parity + `
						(then (table.init $t $e1 (i32.const 0) (i32.const 0) (i32.const ` + n + `)))
						(else (table.init $t $e0 (i32.const 0) (i32.const 0) (i32.const ` + n + `))))
					` + trip + `)
				(i32.const 0)))`, tableRead},
		{"array.fill", `(module ` + common + ` (type $a (array (mut i64)))
			(global $x (export "x") (mut (ref null $a)) (ref.null $a))
			(func (export "run") (param i32) (result i32)
				(global.set $x (array.new_default $a (i32.const ` + n + `)))
				(loop (array.fill $a (global.get $x) (i32.const 0)
						(i64.add (i64.const 7) (i64.extend_i32_u ` + parity + `)) (i32.const ` + n + `))
					` + trip + `)
				(i32.const 0)))`, func(in *Instance, k int) uint64 { return arrayGlobal(t, in).fields[k].num }},
		{"array.copy", `(module ` + common + ` (type $a (array (mut i64)))
			(global $x (export "x") (mut (ref null $a)) (ref.null $a))
			(func (export "run") (param i32) (result i32)
				(global.set $x (array.new_default $a (i32.const ` + itoa(3*bulkWitnessLen) + `)))
				(array.fill $a (global.get $x) (i32.const ` + n + `) (i64.const 7) (i32.const ` + n + `))
				(array.fill $a (global.get $x) (i32.const ` + itoa(2*bulkWitnessLen) + `) (i64.const 8) (i32.const ` + n + `))
				(loop (array.copy $a $a (global.get $x) (i32.const 0) (global.get $x)
						(i32.mul (i32.add (i32.const 1) ` + parity + `) (i32.const ` + n + `)) (i32.const ` + n + `))
					` + trip + `)
				(i32.const 0)))`, func(in *Instance, k int) uint64 { return arrayGlobal(t, in).fields[k].num }},
		{"array.init_data", `(module ` + common + ` (type $b (array (mut i8)))
			(global $x (export "x") (mut (ref null $b)) (ref.null $b))
			(data $d0 "` + data0 + `") (data $d1 "` + data1 + `")
			(func (export "run") (param i32) (result i32)
				(global.set $x (array.new_default $b (i32.const ` + n + `)))
				(loop (if ` + parity + `
						(then (array.init_data $b $d1 (global.get $x) (i32.const 0) (i32.const 0) (i32.const ` + n + `)))
						(else (array.init_data $b $d0 (global.get $x) (i32.const 0) (i32.const 0) (i32.const ` + n + `))))
					` + trip + `)
				(i32.const 0)))`, func(in *Instance, k int) uint64 { return arrayGlobal(t, in).fields[k].num }},
		{"array.init_elem", `(module ` + common + ` (type $r (array (mut funcref)))
			(global $x (export "x") (mut (ref null $r)) (ref.null $r))
			(elem $e0 func` + seg0 + `) (elem $e1 func` + seg1 + `)
			(func (export "run") (param i32) (result i32)
				(global.set $x (array.new_default $r (i32.const ` + n + `)))
				(loop (if ` + parity + `
						(then (array.init_elem $r $e1 (global.get $x) (i32.const 0) (i32.const 0) (i32.const ` + n + `)))
						(else (array.init_elem $r $e0 (global.get $x) (i32.const 0) (i32.const 0) (i32.const ` + n + `))))
					` + trip + `)
				(i32.const 0)))`, func(in *Instance, k int) uint64 { return funcAddr(arrayGlobal(t, in).fields[k].r) }},
	}
}

// gcInst encodes, decodes with GC on, and instantiates a witness module.
func gcInst(t *testing.T, src string) *Instance {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{GC: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// **Validated, because `mustInst` does not validate.** This file's first draft set an i64 array's elements
	// from i32 values. The interpreter ran it, and a direction test passed over a module the validator refuses.
	// A witness over an invalid module is evidence about nothing.
	if _, err := validate.Module(m); err != nil {
		t.Fatalf("the witness module does not validate: %v", err)
	}
	in, trap := mustInst(t, m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	return in
}

// tripsSoFar reads the exported trip counter while the agent is stopped.
func tripsSoFar(t *testing.T, in *Instance) int32 {
	t.Helper()
	v, err := in.Global("it")
	if err != nil {
		t.Fatalf("global it: %v", err)
	}
	return v.Int32()
}

// TestAStopLandsInsideATableOrArrayBulkInstruction: for each of table.fill, table.copy, table.init, array.fill,
// array.copy, array.init_data and array.init_elem, a stop parks the agent inside the instruction, at an
// `elemChunk` boundary, at every chunk.
func TestAStopLandsInsideATableOrArrayBulkInstruction(t *testing.T) {
	for _, w := range bulkWitnesses(t) {
		t.Run(w.name, func(t *testing.T) {
			in := gcInst(t, w.wat)
			go func() { _, _ = in.Invoke("run", I32(1_000_000)) }()
			defer func() { _ = in.Close() }()
			// Three trips first, so both ends of the destination hold a written value.
			for primed := false; !primed; {
				if err := in.Stop(30 * time.Second); err != nil {
					t.Fatalf("waiting for the first trips: %v", err)
				}
				primed = tripsSoFar(t, in) >= 3
				in.Resume()
			}
			const trials = 80
			rng := rand.New(rand.NewPCG(1, uint64(len(w.name))))
			var points []uint64
			for range trials {
				until := time.Now().Add(time.Duration(rng.Int64N(int64(10 * time.Millisecond))))
				for time.Now().Before(until) {
				}
				if err := in.Stop(30 * time.Second); err != nil {
					t.Fatalf("Stop: %v", err)
				}
				// Read only while parked (SP-6). The destination is a new-value prefix and an old-value suffix.
				first := w.read(in, 0)
				if w.read(in, bulkWitnessLen-1) != first {
					lo, hi := 0, bulkWitnessLen-1
					for hi-lo > 1 {
						mid := lo + (hi-lo)/2
						if w.read(in, mid) == first {
							lo = mid
						} else {
							hi = mid
						}
					}
					points = append(points, uint64(hi))
				}
				in.Resume()
			}
			t.Logf("%d of %d stops parked the agent inside %s", len(points), trials, w.name)
			if len(points) == 0 {
				t.Fatalf("no stop of %d parked the agent inside %s: the destination's two ends always agreed, "+
					"so the instruction ran unpolled", trials, w.name)
			}
			checkBulkPollLattice(t, points, elemChunk, w.name)
		})
	}
}

// TestABulkPieceCopyKeepsItsDirection: a self-overlapping `table.copy` or `array.copy` of more than one piece
// copies memmove-correctly in both directions. One `copy` call was a memmove; pieces are not, so `eachPiece`'s
// direction is what keeps them correct. Each row copies 3·elemChunk+5 elements of a ramp by 7 places, up and
// down, and reads every element back: element k of the destination must hold the source's k-th original value.
//
// NEUTER (run): `backward` forced false in `execTableCopy` or `execArrayCopy`, and the upward rows read a
// smeared ramp.
func TestABulkPieceCopyKeepsItsDirection(t *testing.T) {
	const count = 3*elemChunk + 5
	const shift = 7
	size := itoa(count + shift)
	for _, c := range []struct {
		name string
		up   bool
		wat  string
	}{
		{"array.copy up", true, ""},
		{"array.copy down", false, ""},
		{"array.copy packed up", true, "i8"},
		{"array.copy packed down", false, "i8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			elem := "i64"
			ramp := `(i64.extend_i32_u (i32.and (local.get $i) (i32.const 127)))`
			if c.wat != "" {
				elem = c.wat
				ramp = `(i32.and (local.get $i) (i32.const 127))`
			}
			dst, src := shift, 0
			if !c.up {
				dst, src = 0, shift
			}
			in := gcInst(t, `(module (type $a (array (mut `+elem+`)))
				(global $x (export "x") (mut (ref null $a)) (ref.null $a))
				(func (export "run") (result i32) (local $i i32)
					(global.set $x (array.new_default $a (i32.const `+size+`)))
					(loop (array.set $a (global.get $x) (local.get $i) `+ramp+`)
						(local.set $i (i32.add (local.get $i) (i32.const 1)))
						(br_if 0 (i32.lt_u (local.get $i) (i32.const `+size+`))))
					(array.copy $a $a (global.get $x) (i32.const `+itoa(uint64(dst))+`) (global.get $x)
						(i32.const `+itoa(uint64(src))+`) (i32.const `+itoa(count)+`))
					(i32.const 0)))`)
			defer func() { _ = in.Close() }()
			if _, err := in.Invoke("run"); err != nil {
				t.Fatalf("run: %v", err)
			}
			got := arrayGlobal(t, in).fields
			for k := range count {
				want := uint64((src + k) & 127)
				if got[dst+k].num != want {
					t.Fatalf("element %d of the destination holds %d, want the source's original %d: a piece "+
						"overwrote a later piece's source", dst+k, got[dst+k].num, want)
				}
			}
		})
	}
	// table.copy, over an i31ref table, so each slot can carry its own index.
	for _, up := range []bool{true, false} {
		t.Run(map[bool]string{true: "table.copy up", false: "table.copy down"}[up], func(t *testing.T) {
			dst, src := shift, 0
			if !up {
				dst, src = 0, shift
			}
			in := gcInst(t, `(module (table $t `+size+` i31ref)
				(func (export "run") (result i32) (local $i i32)
					(loop (table.set $t (local.get $i) (ref.i31 (local.get $i)))
						(local.set $i (i32.add (local.get $i) (i32.const 1)))
						(br_if 0 (i32.lt_u (local.get $i) (i32.const `+size+`))))
					(table.copy $t $t (i32.const `+itoa(uint64(dst))+`) (i32.const `+itoa(uint64(src))+`)
						(i32.const `+itoa(count)+`))
					(i32.const 0)))`)
			defer func() { _ = in.Close() }()
			if _, err := in.Invoke("run"); err != nil {
				t.Fatalf("run: %v", err)
			}
			slots := in.tables[0].view()
			for k := range count {
				if got, want := slots[dst+k].I31, uint32(src+k); got != want {
					t.Fatalf("slot %d holds %d, want the source's original %d: a piece overwrote a later piece's "+
						"source", dst+k, got, want)
				}
			}
		})
	}
}

// TestAStopLandsInsideAnArrayNew: for each `array.new*`, a stop can park the agent inside the instruction's
// element loop, at least every 64 Ki elements.
//
// **This witness counts parks, because it cannot read where they landed.** An array is not published until
// `array.new` returns, so a stop point inside one is in a Go slice no guest state reaches. The guest instead
// counts its trips in "it". The test stops and resumes as fast as it can, and records the counter at each
// stop.
//   - **Unpolled,** the agent parks only at the loop's back-edge, after the count, so every stop sees a new
//     value.
//   - **Polled every `elemChunk` elements**, the many stops inside one instruction all see the same value.
//   - **Polled every 64·`elemChunk`**, the neuter, one value can be seen by at most 1 Mi/(64·4096) + 1 = 5 stops:
//     the instruction's interior polls and the back-edge before it.
//
// So more than 16 stops on one value bounds the interval below 64 Ki elements. That is coarser than the lattice
// rule the readable instructions meet, and it is what an unpublished array allows.
func TestAStopLandsInsideAnArrayNew(t *testing.T) {
	n := itoa(bulkWitnessLen)
	data := strings.Repeat(`\07`, bulkWitnessLen)
	segment := strings.Repeat(" $f0", bulkWitnessLen)
	common := `(global $it (export "it") (mut i32) (i32.const 0)) (func $f0)
		(type $a (array (mut i64))) (type $b (array (mut i8))) (type $r (array (mut funcref)))
		(data $d "` + data + `") (elem $e func` + segment + `)`
	for _, c := range []struct{ name, expr string }{
		{"array.new", `(array.new $a (i64.const 7) (i32.const ` + n + `))`},
		{"array.new_default", `(array.new_default $a (i32.const ` + n + `))`},
		{"array.new_data", `(array.new_data $b $d (i32.const 0) (i32.const ` + n + `))`},
		{"array.new_elem", `(array.new_elem $r $e (i32.const 0) (i32.const ` + n + `))`},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := gcInst(t, `(module `+common+`
				(func (export "run") (param i32) (result i32)
					(loop (drop `+c.expr+`)
						(global.set $it (i32.add (global.get $it) (i32.const 1)))
						(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
					(i32.const 0)))`)
			go func() { _, _ = in.Invoke("run", I32(1_000_000)) }()
			defer func() { _ = in.Close() }()
			// **The floor: the guest is inside its loop.** Until `Invoke` has entered, no caller of the instance
			// exists, so every `Stop` is satisfied at once and every one sees the count 0. The first draft of
			// this witness counted those as stops inside one instruction, and passed with the poll removed: 85 to
			// 141 "inside", every one of them before the guest started.
			for primed := false; !primed; {
				if err := in.Stop(30 * time.Second); err != nil {
					t.Fatalf("waiting for the first trips: %v", err)
				}
				primed = tripsSoFar(t, in) >= 2
				in.Resume()
			}
			const stops = 2000
			run, longest := 0, 0
			last := int32(-1)
			for range stops {
				if err := in.Stop(30 * time.Second); err != nil {
					t.Fatalf("Stop: %v", err)
				}
				v := tripsSoFar(t, in)
				in.Resume()
				if v == last {
					run++
				} else {
					run, last = 1, v
				}
				longest = max(longest, run)
			}
			t.Logf("%s: at most %d of %d back-to-back stops saw one trip count", c.name, longest, stops)
			const want = 16
			if longest <= want {
				t.Fatalf("at most %d back-to-back stops landed inside one %s, not more than %d: the element loop "+
					"polls less often than once per %d elements, or not at all", longest, c.name, want, 64*elemChunk)
			}
		})
	}
}
