// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestTaskReturnLiftsAVariantAsWasmtimeDoes is #1320's witness for the variant family: every export of
// task-return-variant.wasm calls `task.return` with a variant-family value, and each lifts to the value
// wasmtime 49.0.3 prints for it (task-return-variant.reading, 10 of 10; the pin agrees on every row). Burroughs
// trapped on all ten at d8076a1, because liftFlatValue had no arm for a variant, an option or an enum.
//
// The rows are chosen so each names one rule of def:lift_flat_variant. `mixed-*` is a variant whose cases have
// different payload types, so its payload slot is the join i64 (def:join): `mixed-a` wraps a u32 out of an i64
// slot whose high bits are set, `mixed-b` reinterprets an f64's bits out of it, and `mixed-c` reads a string's
// pointer from it. `opt-none` and `nested-ok-none` put garbage in a slot the chosen case does not fill, which the
// model drains unread. `nested-*` is a `result<option<u8>, string>`, so a payload is itself a variant lifted from
// the joined slots, and `bad-case` is the case-index trap.
//
// The value Burroughs lifts is compared with `reflect.DeepEqual` against one built with the codec's constructors.
// Each row's `wave` is wasmtime's text, asserted equal to the reading's row, so the two sides of a row are read
// against each other here and the reading cannot drift from the table unnoticed.
//
// **Neuters, run:**
//   - liftFlatValue's variant arm removed: all ten rows trap "liftFlatValue has no arm for kind variant" (option
//     for the opt-* rows, enum for enum-y), the d8076a1 reading.
//   - `coerce`'s i64→f64 arm converting the integer rather than reinterpreting its bits: mixed-b lifts
//     variant(1, f64(4.614256656552046e+18)) where wasmtime reads b(3.141592653589793), and the codec's
//     differential fails on variant-mixed-flat. Skipping the coercion outright fails nothing, because a word is
//     already its bits and every 32-bit consumer masks; canon.LiftFlatVariant's comment says why.
//   - the case-index check removed from canon.LiftFlatVariant: bad-case indexes past the cases and panics.
func TestTaskReturnLiftsAVariantAsWasmtimeDoes(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/task-return-variant.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	reading := readTaskReturnVariantReading(t)

	u32, u64, u8, f64, str := canon.Type{Kind: canon.KindU32}, canon.Type{Kind: canon.KindU64},
		canon.Type{Kind: canon.KindU8}, canon.Type{Kind: canon.KindF64}, canon.Type{Kind: canon.KindString}
	ou64 := canon.OptionType(u64)
	vT := canon.VariantType(canon.Case{Name: "a", Type: &u32}, canon.Case{Name: "b", Type: &f64},
		canon.Case{Name: "c", Type: &str})
	ou8 := canon.OptionType(u8)
	nT := canon.ResultType(&ou8, &str)
	eT, err := canon.EnumType("x", "y", "z")
	if err != nil {
		t.Fatal(err)
	}
	must := func(v canon.Value, err error) canon.Value {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ptr := func(v canon.Value) *canon.Value { return &v }
	pi := must(canon.LiftFlatScalar(f64, math.Float64bits(3.141592653589793)))

	rows := []struct {
		export, wave string
		want         canon.Value // zero for the trap row
	}{
		{"opt-some", "some(4294967298)", must(canon.Some(ou64, canon.U64(0x1_0000_0002)))},
		{"opt-none", "none", must(canon.None(ou64))},
		{"mixed-a", "a(" + strconv.FormatUint(0xDEADBEEF, 10) + ")", must(canon.Variant(vT, "a", ptr(canon.U32(0xDEADBEEF))))},
		{"mixed-b", "b(3.141592653589793)", must(canon.Variant(vT, "b", &pi))},
		{"mixed-c", `c("burroughs")`, must(canon.Variant(vT, "c", ptr(canon.Str("burroughs"))))},
		{"nested-ok-some", "ok(some(42))", must(canon.Variant(nT, "ok", ptr(must(canon.Some(ou8, canon.U8(42))))))},
		{"nested-ok-none", "ok(none)", must(canon.Variant(nT, "ok", ptr(must(canon.None(ou8)))))},
		{"nested-err", `err("burroughs")`, must(canon.Variant(nT, "error", ptr(canon.Str("burroughs"))))},
		{"enum-y", "y", must(canon.Enum(eT, "y"))},
		{"bad-case", "trap: discriminant 3 out of range [0..3)", canon.Value{}},
	}
	if len(rows) != len(reading) {
		t.Fatalf("the table has %d rows and task-return-variant.reading %d; a row was added to one only",
			len(rows), len(reading))
	}
	for _, r := range rows {
		t.Run(r.export, func(t *testing.T) {
			if got := reading[r.export]; got != r.wave {
				t.Fatalf("task-return-variant.reading reads %q for %s, and this row was written against %q",
					got, r.export, r.wave)
			}
			res, err := callTaskReturnVariantRow(t, b, r.export)
			if strings.HasPrefix(r.wave, "trap") {
				var trap *interp.Trap
				if !errors.As(err, &trap) || !strings.Contains(err.Error(), "case index 3 out of range") {
					t.Fatalf("%s() = %v, %v; want the case-index trap (the oracle: %s)", r.export, res, err, r.wave)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s(): %v; wasmtime reads %s", r.export, err, r.wave)
			}
			if len(res) != 1 {
				t.Fatalf("%s() returned %d value(s), want 1", r.export, len(res))
			}
			if !canon.TypeEqual(res[0].Type, r.want.Type) {
				t.Fatalf("%s() lifted a %s, want a %s", r.export, res[0].Type, r.want.Type)
			}
			if !reflect.DeepEqual(res[0], r.want) {
				t.Fatalf("%s() lifted %s, want the value wasmtime prints as %s (%s)",
					r.export, describeValue(res[0]), r.wave, describeValue(r.want))
			}
		})
	}
}

// readTaskReturnVariantReading reads task-return-variant.reading's `<export>: <reading>` rows.
func readTaskReturnVariantReading(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/task-return-variant.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	out := map[string]string{}
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		export, reading, ok := strings.Cut(ln, ": ")
		if !ok {
			t.Fatalf("task-return-variant.reading: row %q has no `: `", ln)
		}
		out[export] = reading
	}
	return out
}

// describeValue renders a lifted value for a failure message only: its kind, its word (a case index, or an f64
// as a float), its string and its payload, followed. canon.Value's payload is unexported and has no accessor
// for a variant, so the fields are read by reflection, which can read and not write them. Nothing is asserted
// on this text; the assertion is reflect.DeepEqual.
func describeValue(v canon.Value) string {
	var walk func(rv reflect.Value) string
	walk = func(rv reflect.Value) string {
		kind := canon.Kind(rv.FieldByName("Type").FieldByName("Kind").Uint())
		u := rv.FieldByName("u").Uint()
		out := kind.String() + "("
		switch kind {
		case canon.KindF64:
			out += strconv.FormatFloat(math.Float64frombits(u), 'g', -1, 64)
		case canon.KindString:
			out += strconv.Quote(rv.FieldByName("s").String())
		default:
			out += strconv.FormatUint(u, 10)
		}
		if p := rv.FieldByName("payload"); !p.IsNil() {
			out += ", " + walk(p.Elem())
		}
		return out + ")"
	}
	return walk(reflect.ValueOf(v))
}

// callTaskReturnVariantRow calls one export on a fresh instance, since the trap row locks its store.
func callTaskReturnVariantRow(t *testing.T, b []byte, export string) ([]canon.Value, error) {
	t.Helper()
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(in.Close)
	return in.CallValues(export)
}

// TestARunReturningAResultThroughTaskReturnExitsAsWasmtimeDoes is #1320's specimen: wasi:cli/run@0.3.0's
// `run: async func() -> result`, lifted async with a callback, returning ok and then err through `task.return`.
// wasmtime 49.0.3 exits 0 and 1 (task-return-run.reading, 10 of 10). Burroughs trapped at the `task.return` on
// both, because liftFlatValue had no arm for a variant.
//
// **Neuter, run:** liftFlatValue's variant arm removed, and both subtests fail with "task.return declares a
// variant{ok, error} value, which this engine does not lift from flat values: liftFlatValue has no arm for kind
// variant".
func TestARunReturningAResultThroughTaskReturnExitsAsWasmtimeDoes(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	for _, c := range []struct{ name, reader string }{
		{"ok", "wasmtime 49.0.3 on ok, 10 of 10"},
		{"err", "wasmtime 49.0.3 on err, 10 of 10"},
	} {
		t.Run(c.name, func(t *testing.T) {
			want, err := strconv.Atoi(readingLine(t, "testdata/task-return-run.reading", c.reader))
			if err != nil {
				t.Fatalf("task-return-run.reading's %q line: %v", c.reader, err)
			}
			b, err := os.ReadFile("testdata/task-return-run-" + c.name + ".wasm")
			if err != nil {
				t.Fatal(err)
			}
			in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			code, err := in.CallRun()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if code != want {
				t.Errorf("run returning %s through task.return exited %d, want wasmtime's %d", c.name, code, want)
			}
		})
	}
}
