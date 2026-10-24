// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package stackbench measures whether the interpreter's branch-heavy hot loop depends on where its goroutine's
// stack lands (#1036).
//
// #1036's readings, on janus (x86): the same work ran about 9% slower or faster depending only on where the
// interpreter's frame landed, and all of the difference was in `interp.(*Instance).branch`, on two plain memory
// accesses. This benchmark runs one branch-heavy guest loop at a ladder of stack offsets. It pads the calling
// goroutine's stack by `pad` bytes of frames before invoking, so the interpreter's frames start at a different
// address mod 4096 in each sub-benchmark. The quantity of interest is the SPREAD across pads, not any one row:
// a hot loop that does not depend on stack placement reads the same at every pad.
package stackbench

import (
	"fmt"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// trips is the guest loop's iteration count per Invoke.
const trips = 200_000

// guest is a loop whose every turn takes branches through `branch`: a taken `br_if` out of an inner block on
// odd turns, and the taken `br` back to the loop head on every turn.
const guest = `(module
	(func (export "run") (param $n i32) (result i32) (local $i i32) (local $acc i32)
		(block $out
			(loop $l
				(br_if $out (i32.ge_u (local.get $i) (local.get $n)))
				(block $b
					(br_if $b (i32.and (local.get $i) (i32.const 1)))
					(local.set $acc (i32.add (local.get $acc) (i32.const 3))))
				(local.set $i (i32.add (local.get $i) (i32.const 1)))
				(br $l)))
		(local.get $acc)))`

// frame is 256 bytes of stack per level of padding.
const frame = 256

var sink byte

// padded consumes `levels` frames of `frame` bytes before running f, so f's frames start `levels*frame` bytes
// further down the goroutine's stack.
//
//go:noinline
func padded(levels int, f func()) {
	var buf [frame]byte
	buf[levels%frame] = byte(levels)
	if levels == 0 {
		f()
	} else {
		padded(levels-1, f)
	}
	sink += buf[levels%frame]
}

func BenchmarkBranchAtStackOffset(b *testing.B) {
	img, err := text.EncodeModule([]byte(guest))
	if err != nil {
		b.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		b.Fatalf("decode: %v", err)
	}
	in, trap, lerr := interp.Instantiate(m)
	if lerr != nil || trap != nil {
		b.Fatalf("instantiate: %v %v", lerr, trap)
	}
	defer func() { _ = in.Close() }()
	want := int32(3 * (trips / 2))

	// 16 pads of 256 B cover one 4 KiB period, the aliasing distance #1036 suspects.
	for levels := range 16 {
		b.Run(fmt.Sprintf("pad=%04d", levels*frame), func(b *testing.B) {
			var got []interp.Value
			var ierr error
			for b.Loop() {
				padded(levels, func() { got, ierr = in.Invoke("run", interp.I32(trips)) })
			}
			if ierr != nil || len(got) != 1 || got[0].Int32() != want {
				b.Fatalf("run returned %v, %v; want %d", got, ierr, want)
			}
		})
	}
}
