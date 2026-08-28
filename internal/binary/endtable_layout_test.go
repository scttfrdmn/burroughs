package binary

import (
	"reflect"
	"testing"
)

// TestEndsOffsetIsFreeInTheLayout asserts the premise that let `Func.EndsOff` be declared
// in every build instead of behind `burroughs_endtable`: it occupies the 4-byte hole that
// `TypeIndex uint32` leaves in front of the first slice header, so `Func` is the same size
// with the field and without it.
//
// # Why this is a control and not a comment
//
// The gate exists so the default build pays nothing for a feature it does not use, and
// this field is the one part of the mechanism that is *not* behind the gate. The whole
// licence for that is a layout coincidence, and a layout coincidence is exactly the kind of
// premise that a later, unrelated edit falsifies silently — reorder `Func`'s fields, or add
// a second small field ahead of `Locals`, and the offset starts costing 8 bytes on every
// function. Nothing would fail, no board would move, and the flip's accounting would be
// wrong by 75144 B over 0048's corpus while still reading as "free, see the doc". *The
// defect stated as the rule*: the doc would assert the property the layout had lost.
//
// So the control checks both directions, because only the pair of them is informative:
//
//   - removing the field does not shrink `Func` — the field is absorbed;
//   - moving the same field to the end *does* grow `Func` — the absorption is positional,
//     not a property of `int32`.
//
// Without the second half the first could pass on a `Func` that had no hole at all and had
// simply grown by a whole word, which is the shape of an unasserted distance: a bound that
// agrees with everything.
func TestEndsOffsetIsFreeInTheLayout(t *testing.T) {
	const field = "EndsOff"

	ft := reflect.TypeOf(Func{})
	var without []reflect.StructField
	var moved []reflect.StructField
	var target reflect.StructField
	found := false
	for i := range ft.NumField() {
		f := ft.Field(i)
		if f.PkgPath != "" {
			// reflect.StructOf cannot mirror an unexported field, so the mirror would be a
			// different struct and every size below would be a claim about something else.
			// Loud rather than skipped: a skip here would retire the control by accident.
			t.Fatalf("Func.%s is unexported, so this control can no longer build a mirror of Func; "+
				"either export it or rewrite the control — it must not silently stop checking", f.Name)
		}
		if f.Name == field {
			found = true
			target = f
			continue
		}
		without = append(without, f)
		moved = append(moved, f)
	}
	if !found {
		t.Fatalf("Func has no %s field. The pairing table's offset is what this control prices; "+
			"if the representation changed, 0048's arithmetic changed with it", field)
	}
	if len(without) != ft.NumField()-1 {
		t.Fatalf("mirror has %d fields, want %d: the mirror is not Func-minus-one", len(without), ft.NumField()-1)
	}
	moved = append(moved, target)

	base := ft.Size()
	absorbed := reflect.StructOf(without).Size()
	appended := reflect.StructOf(moved).Size()

	if base != absorbed {
		t.Errorf("Func is %d B and Func-without-%s is %d B: the offset field is no longer free, "+
			"so the default build pays %d B per function for a gated feature — and %s's doc, "+
			"binary.go's note on moduleEnds, and 0048's bill all say it pays nothing",
			base, field, absorbed, base-absorbed, field)
	}
	if appended <= absorbed {
		t.Errorf("%s costs %d B appended and %d B where it is: an int32 is free at the end of Func too, "+
			"so this control is no longer witnessing anything about position and the padding term "+
			"0048 measured (75144 B over the corpus) is not there to measure",
			field, appended-absorbed, base-absorbed)
	}
	t.Logf("Func is %d B; without %s it is %d B (absorbed); with %s appended it would be %d B (+%d B/func)",
		base, field, absorbed, field, appended, appended-absorbed)
}
