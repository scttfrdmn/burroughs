// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
	"github.com/scttfrdmn/burroughs/internal/validate"
)

// BenchmarkShortBulkOps is the table and array bulk polls' cost reading (the chair's ruling of 2026-10-19, item
// 3), read with `make lab-ab` against ADR 0059's bar. The instructions are short, 64 elements, so each is one
// piece: what a short instruction pays for the pieces is what this measures. Each iteration runs 1000 of the
// instruction in a guest loop, so the call's own cost does not dominate.
func BenchmarkShortBulkOps(b *testing.B) {
	src := `(module
		(type $a (array (mut i64)))
		(table $t 256 funcref)
		(func $f) (elem declare func $f)
		(func (export "table-fill") (local $i i32)
			(loop (table.fill $t (i32.const 0) (ref.func $f) (i32.const 64))
				(local.set $i (i32.add (local.get $i) (i32.const 1))) (br_if 0 (i32.lt_u (local.get $i) (i32.const 1000)))))
		(func (export "table-copy") (local $i i32)
			(loop (table.copy $t $t (i32.const 0) (i32.const 64) (i32.const 64))
				(local.set $i (i32.add (local.get $i) (i32.const 1))) (br_if 0 (i32.lt_u (local.get $i) (i32.const 1000)))))
		(func (export "array-fill") (local $i i32) (local $x (ref $a))
			(local.set $x (array.new_default $a (i32.const 128)))
			(loop (array.fill $a (local.get $x) (i32.const 0) (i64.const 7) (i32.const 64))
				(local.set $i (i32.add (local.get $i) (i32.const 1))) (br_if 0 (i32.lt_u (local.get $i) (i32.const 1000)))))
		(func (export "array-copy") (local $i i32) (local $x (ref $a))
			(local.set $x (array.new_default $a (i32.const 128)))
			(loop (array.copy $a $a (local.get $x) (i32.const 0) (local.get $x) (i32.const 64) (i32.const 64))
				(local.set $i (i32.add (local.get $i) (i32.const 1))) (br_if 0 (i32.lt_u (local.get $i) (i32.const 1000)))))
		(func (export "array-new") (local $i i32)
			(loop (drop (array.new $a (i64.const 7) (i32.const 64)))
				(local.set $i (i32.add (local.get $i) (i32.const 1))) (br_if 0 (i32.lt_u (local.get $i) (i32.const 1000))))))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		b.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{GC: true}}).DecodeModule(img)
	if err != nil {
		b.Fatalf("decode: %v", err)
	}
	if _, err := validate.Module(m); err != nil {
		b.Fatalf("the benchmark module does not validate: %v", err)
	}
	in, trap := mustInst(b, m)
	if trap != nil {
		b.Fatalf("instantiate: %v", trap)
	}
	defer func() { _ = in.Close() }()
	for _, op := range []string{"table-fill", "table-copy", "array-fill", "array-copy", "array-new"} {
		b.Run("op="+op, func(b *testing.B) {
			for b.Loop() {
				if _, err := in.Invoke(op); err != nil {
					b.Fatalf("%s: %v", op, err)
				}
			}
		})
	}
}
