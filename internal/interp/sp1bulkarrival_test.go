// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// The element counts of the table and array arrival readings. A table slot and an array element are one `ref`
// or one `gcField` wide (tens of bytes), so 2 Mi of them is the same order of bytes as `BenchmarkSP1Arrival`'s
// 64 MiB fill. The segment-sourced classes use what a segment can reasonably hold in a text module.
const (
	bulkElems = 2 << 20  // table and array length
	bulkSeg   = 64 << 10 // element-segment length
	bulkData  = 1 << 20  // data-segment length, in bytes
)

// sp1BulkModule is the instruction classes of the chair's ruling of 2026-10-19 (item 3), one export each, each
// a loop running its instruction over the whole operand per trip.
func sp1BulkModule(tb testing.TB) *binary.Module {
	tb.Helper()
	elems := strings.Repeat(" $f", bulkSeg)
	data := strings.Repeat(`\01`, bulkData)
	src := `(module
		(type $a (array (mut i64)))
		(type $b (array (mut i8)))
		(type $r (array (mut funcref)))
		(table $t ` + itoa(bulkElems) + ` funcref)
		(func $f)
		(elem $e func` + elems + `)
		(data $d "` + data + `")
		(func (export "table-fill") (param i32) (result i32)
			(loop (table.fill $t (i32.const 0) (ref.func $f) (i32.const ` + itoa(bulkElems) + `))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "table-copy") (param i32) (result i32)
			(loop (table.copy $t $t (i32.const 0) (i32.const ` + itoa(bulkElems/2) + `) (i32.const ` + itoa(bulkElems/2) + `))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "table-init") (param i32) (result i32)
			(loop (table.init $t $e (i32.const 0) (i32.const 0) (i32.const ` + itoa(bulkSeg) + `))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-new") (param i32) (result i32)
			(loop (drop (array.new $a (i64.const 7) (i32.const ` + itoa(bulkElems) + `)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-new-default") (param i32) (result i32)
			(loop (drop (array.new_default $a (i32.const ` + itoa(bulkElems) + `)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-new-data") (param i32) (result i32)
			(loop (drop (array.new_data $b $d (i32.const 0) (i32.const ` + itoa(bulkData) + `)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-new-elem") (param i32) (result i32)
			(loop (drop (array.new_elem $r $e (i32.const 0) (i32.const ` + itoa(bulkSeg) + `)))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-fill") (param i32) (result i32) (local $x (ref $a))
			(local.set $x (array.new_default $a (i32.const ` + itoa(bulkElems) + `)))
			(loop (array.fill $a (local.get $x) (i32.const 0) (i64.const 7) (i32.const ` + itoa(bulkElems) + `))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-copy") (param i32) (result i32) (local $x (ref $a))
			(local.set $x (array.new_default $a (i32.const ` + itoa(bulkElems) + `)))
			(loop (array.copy $a $a (local.get $x) (i32.const 0) (local.get $x) (i32.const ` + itoa(bulkElems/2) + `)
					(i32.const ` + itoa(bulkElems/2) + `))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-init-data") (param i32) (result i32) (local $x (ref $b))
			(local.set $x (array.new_default $b (i32.const ` + itoa(bulkData) + `)))
			(loop (array.init_data $b $d (local.get $x) (i32.const 0) (i32.const 0) (i32.const ` + itoa(bulkData) + `))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0))
		(func (export "array-init-elem") (param i32) (result i32) (local $x (ref $r))
			(local.set $x (array.new_default $r (i32.const ` + itoa(bulkSeg) + `)))
			(loop (array.init_elem $r $e (local.get $x) (i32.const 0) (i32.const 0) (i32.const ` + itoa(bulkSeg) + `))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		tb.Fatalf("encoding the measurement module: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{GC: true}}).DecodeModule(img)
	if err != nil {
		tb.Fatalf("decoding the measurement module: %v", err)
	}
	return m
}

// BenchmarkSP1BulkArrival is SP-1's instrument for the table and GC-array bulk instructions (the chair's ruling of
// 2026-10-19, item 3), `BenchmarkSP1Arrival`'s protocol over the classes those instructions make: one guest per
// class, stopped `trials` times at random points, reporting `arrival-p50-ns`, `arrival-p99-ns` and
// `arrival-max-ns`. It asserts nothing. Run it with `-benchtime=1x` on janus's `measured` group.
func BenchmarkSP1BulkArrival(b *testing.B) {
	const trials = 200
	m := sp1BulkModule(b)
	for _, class := range []string{
		"table-fill", "table-copy", "table-init",
		"array-new", "array-new-default", "array-new-data", "array-new-elem",
		"array-fill", "array-copy", "array-init-data", "array-init-elem",
	} {
		b.Run("class="+class, func(b *testing.B) {
			in, trap := mustInst(b, m)
			if trap != nil {
				b.Fatalf("instantiate: %v", trap)
			}
			go func() { _, _ = in.Invoke(class, I32(1_000_000)) }()
			defer func() { _ = in.Close() }()
			var arrivals []time.Duration
			rng := rand.New(rand.NewPCG(1, uint64(len(class))))
			for b.Loop() {
				arrivals = arrivals[:0]
				for i := range trials {
					// The host's clock, spun: a point in the guest's run, not a wait for a signal.
					until := time.Now().Add(time.Duration(rng.Int64N(int64(50 * time.Millisecond))))
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
		})
	}
}
