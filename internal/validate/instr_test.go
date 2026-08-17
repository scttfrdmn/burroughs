package validate

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestCallIndirectIndexTypeComesFromTheTable is the witness for #343's cause 2, and it exists in
// all four cells because **the corpus samples exactly one of them**.
//
// `valid.ml:537,542` reads the address type off the table and appends it to the callee's params:
// `let TableT (at, _lim, t) = table c x` … `(ts1 @ [NumT (numtype_of_addrtype at)])`. This validator
// hardcoded `i32`, which refused four valid table64 modules — over-rejections that produced no error
// for any bucket to catch until #341 gave the accept direction a witness.
//
// **One of these four rows is the whole point, and which one was measured rather than assumed.**
// Three mutations were run against the repaired validator:
//
//   - **hardcoded `i32` again** (the original defect) — this test fails on `i64 on a table64`, and the
//     board fails too: over-rejections go 9 → 13.
//   - **fully permissive, accept either width** — this test fails on both wrong-width rows, and the
//     board *does* catch it, but by exactly **one vector**: `call_indirect.wast:862`
//     (`$type-func-num-vs-i32`, an i64 index on a 32-bit table) flips to an admission and
//     `allOnPassFloor` drops one, 64567 → 64566.
//   - **strict on 32-bit tables, permissive on 64-bit ones** — `go test ./internal/spec/` is
//     **entirely green**, and the only thing in the repository that objects is the `i32 index on a
//     64-bit table` row below.
//
// So the corpus witnesses the 32-bit direction and nothing witnesses the 64-bit one, which makes that
// single row the whole marginal value of this test: *an unasserted distance is the vacuum*. The first
// draft of this comment claimed the board could not tell a permissive fix from a correct one at all —
// that was wrong by one vector, and the mutations are recorded here because the difference between
// "the board is blind" and "the board sees this by a single needle in a pass floor" is the difference
// between a test that is load-bearing and a test that is decoration.
func TestCallIndirectIndexTypeComesFromTheTable(t *testing.T) {
	mem64 := func(f *binary.Features) { f.Memory64 = true }
	for _, c := range []struct {
		name  string
		wat   string
		valid bool
	}{
		{
			name:  "i32 index on a 32-bit table",
			wat:   `(module (type $t (func)) (table 1 funcref) (func (call_indirect (type $t) (i32.const 0))))`,
			valid: true,
		},
		{
			name:  "i64 index on a 32-bit table",
			wat:   `(module (type $t (func)) (table 1 funcref) (func (call_indirect (type $t) (i64.const 0))))`,
			valid: false,
		},
		{
			name:  "i64 index on a 64-bit table",
			wat:   `(module (type $t (func)) (table i64 1 funcref) (func (call_indirect (type $t) (i64.const 0))))`,
			valid: true,
		},
		{
			// The row the suite does not have. Without it, "accept either width" passes everything.
			name:  "i32 index on a 64-bit table",
			wat:   `(module (type $t (func)) (table i64 1 funcref) (func (call_indirect (type $t) (i32.const 0))))`,
			valid: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, mem64)
			switch {
			case c.valid && err != nil:
				t.Errorf("valid module refused: %v\n%s\nAn over-rejection: the accept direction "+
					"produces no expected-text mismatch, so this row is its only witness here.", err, c.wat)
			case !c.valid && err == nil:
				t.Errorf("invalid module accepted\n%s\nThe index operand's width is the table's, "+
					"not either width — a check that reads the table but then accepts anything "+
					"would pass every other row in this table.", c.wat)
			case !c.valid && !errors.Is(err, ErrTypeMismatch):
				t.Errorf("refused with the wrong sentinel: want %v, got %v", ErrTypeMismatch, err)
			}
		})
	}
}
