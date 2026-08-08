package binary

import "testing"

// TestDecodeCompTypeRetainsFields is decision 0021's implementation control, following
// TestDecodeRefTypeRetainsGCShapedValues's precedent (0018): the point of the wider
// CompType/FieldType/StorageType shape is that decodeCompType's struct and array branches
// stop discarding what decodeFieldType/decodeStorageType already read, and this asserts
// they actually do it.
//
// Every row here is reachable only in the all-gates-on lane, per decision 0008's gate
// boundary — this is a representation control, not an acceptance one, matching 0021's own
// consequences section ("a representation decision, not a gate decision").
func TestDecodeCompTypeRetainsFields(t *testing.T) {
	on := &Decoder{Features: Features{GC: true}}

	t.Run("struct: mixed packed and full-valtype fields, mixed mutability", func(t *testing.T) {
		// 1 rectype; structtype (0x5f); 4 fields:
		//   i8  (0x78) immutable
		//   i16 (0x77) mutable
		//   i32 (0x7f) immutable
		//   f64 (0x7c) mutable
		body := []byte{
			0x01, 0x5F, 0x04,
			0x78, 0x00,
			0x77, 0x01,
			0x7F, 0x00,
			0x7C, 0x01,
		}
		m, err := on.DecodeModule(typeSection(body))
		if err != nil {
			t.Fatalf("GC on: got %v, want accept", err)
		}
		if len(m.Types) != 1 {
			t.Fatalf("got %d types, want 1", len(m.Types))
		}
		ct := m.Types[0]
		if ct.Kind != CompStruct {
			t.Fatalf("Kind = %v, want CompStruct", ct.Kind)
		}
		want := []FieldType{
			{Storage: StorageType{Packed: true, Width: 8}, Mutable: false},
			{Storage: StorageType{Packed: true, Width: 16}, Mutable: true},
			{Storage: StorageType{Val: I32}, Mutable: false},
			{Storage: StorageType{Val: F64}, Mutable: true},
		}
		if len(ct.Fields) != len(want) {
			t.Fatalf("got %d fields, want %d — struct.Fields = %+v", len(ct.Fields), len(want), ct.Fields)
		}
		for i, w := range want {
			if ct.Fields[i] != w {
				t.Errorf("field %d = %+v, want %+v", i, ct.Fields[i], w)
			}
		}
	})

	t.Run("array: exactly one field", func(t *testing.T) {
		// 1 rectype; arraytype (0x5e); one fieldtype: i8, mutable.
		body := []byte{0x01, 0x5E, 0x78, 0x01}
		m, err := on.DecodeModule(typeSection(body))
		if err != nil {
			t.Fatalf("GC on: got %v, want accept", err)
		}
		if len(m.Types) != 1 {
			t.Fatalf("got %d types, want 1", len(m.Types))
		}
		ct := m.Types[0]
		if ct.Kind != CompArray {
			t.Fatalf("Kind = %v, want CompArray", ct.Kind)
		}
		if len(ct.Fields) != 1 {
			t.Fatalf("got %d fields, want exactly 1 — arraytype is one fieldtype, never a "+
				"vector (decode.ml:257-258); Fields = %+v", len(ct.Fields), ct.Fields)
		}
		want := FieldType{Storage: StorageType{Packed: true, Width: 8}, Mutable: true}
		if ct.Fields[0] != want {
			t.Errorf("field 0 = %+v, want %+v", ct.Fields[0], want)
		}
	})

	t.Run("struct with zero fields retains an empty, non-nil-required Fields", func(t *testing.T) {
		body := []byte{0x01, 0x5F, 0x00}
		m, err := on.DecodeModule(typeSection(body))
		if err != nil {
			t.Fatalf("GC on: got %v, want accept", err)
		}
		if len(m.Types) != 1 {
			t.Fatalf("got %d types, want 1", len(m.Types))
		}
		if len(m.Types[0].Fields) != 0 {
			t.Errorf("got %d fields, want 0 for a struct with no declared fields", len(m.Types[0].Fields))
		}
	})
}
