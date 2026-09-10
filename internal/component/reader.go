// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
)

// errShort is returned when a read runs past the end of its buffer.
var errShort = errors.New("unexpected end of section")

// reader is a forward byte cursor over a section (or the whole file). It carries no feature or type
// state — this slice reads structure, not semantics.
type reader struct {
	b   []byte
	pos int
}

func (r *reader) byte() (byte, error) {
	if r.pos >= len(r.b) {
		return 0, errShort
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

// u32 reads an unsigned LEB128 in at most five bytes, the core encoding the component format reuses.
func (r *reader) u32() (uint32, error) {
	var result uint32
	var shift uint
	for i := range 5 {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		if i == 4 && b&0xf0 != 0 {
			return 0, fmt.Errorf("u32 LEB128 overflows 32 bits")
		}
		result |= uint32(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("u32 LEB128 unterminated")
}

// take returns the next n bytes as a sub-slice and advances the cursor.
func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, errShort
	}
	s := r.b[r.pos : r.pos+n]
	r.pos += n
	return s, nil
}

// name reads a `nameattributes` (Binary.md @ 2bed77e): a discriminant byte, a u32 length, and the
// UTF-8 bytes. The 0x02 case carries a trailing attribute vector this slice does not model; it is
// refused, named, rather than skipped (no fixture exercises it, and a silent skip would desync the
// enclosing vec).
func (r *reader) name() (string, error) {
	disc, err := r.byte()
	if err != nil {
		return "", err
	}
	switch disc {
	case 0x00, 0x01:
		// plain name
	case 0x02:
		return "", fmt.Errorf("nameattributes 0x02 (with attributes) not handled this slice")
	default:
		return "", fmt.Errorf("nameattributes discriminant %#x is not 0x00/0x01/0x02", disc)
	}
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	s, err := r.take(int(n))
	if err != nil {
		return "", err
	}
	return string(s), nil
}

// externType reads an `externtype` and returns its kind, consuming the whole production so a caller's
// vec stays in sync. The value case's `0x01 valtype` form (an inline value type) is not modelled this
// slice and is refused, named — no in-scope fixture imports a value.
func (r *reader) externType() (ExternKind, error) {
	disc, err := r.byte()
	if err != nil {
		return 0, err
	}
	switch disc {
	case 0x00: // core:* — only `core module` (0x11) is a valid externtype
		sub, serr := r.byte()
		if serr != nil {
			return 0, serr
		}
		if sub != 0x11 {
			return 0, fmt.Errorf("externtype 0x00: core sort %#x is not module (0x11)", sub)
		}
		if _, ierr := r.u32(); ierr != nil {
			return 0, ierr
		}
		return ExternCoreModule, nil
	case 0x01: // func (type i)
		_, err = r.u32()
		return ExternFunc, err
	case 0x02: // value b
		return ExternValue, r.valueBound()
	case 0x03: // type b
		return ExternType, r.typeBound()
	case 0x04: // component (type i)
		_, err = r.u32()
		return ExternComponent, err
	case 0x05: // instance (type i)
		_, err = r.u32()
		return ExternInstance, err
	default:
		return 0, fmt.Errorf("externtype discriminant %#x is undefined", disc)
	}
}

// typeBound reads a `typebound`: 0x00 typeidx (eq) or 0x01 (sub resource, no operand).
func (r *reader) typeBound() error {
	disc, err := r.byte()
	if err != nil {
		return err
	}
	switch disc {
	case 0x00:
		_, err = r.u32()
		return err
	case 0x01:
		return nil
	default:
		return fmt.Errorf("typebound discriminant %#x is not 0x00/0x01", disc)
	}
}

// valueBound reads a `valuebound`: 0x00 valueidx (eq). The 0x01 (inline valtype) form is a later
// slice's — refused, named.
func (r *reader) valueBound() error {
	disc, err := r.byte()
	if err != nil {
		return err
	}
	switch disc {
	case 0x00:
		_, err = r.u32()
		return err
	case 0x01:
		return fmt.Errorf("valuebound 0x01 (inline valtype) not handled this slice")
	default:
		return fmt.Errorf("valuebound discriminant %#x is not 0x00/0x01", disc)
	}
}

// sort reads a `sort` (Binary.md): a discriminant, with the core case carrying a second core:sort byte.
func (r *reader) sort() (Sort, error) {
	disc, err := r.byte()
	if err != nil {
		return 0, err
	}
	switch disc {
	case 0x00: // core cs — consume the core:sort sub-byte
		if _, serr := r.byte(); serr != nil {
			return 0, serr
		}
		return SortCore, nil
	case 0x01:
		return SortFunc, nil
	case 0x02:
		return SortValue, nil
	case 0x03:
		return SortType, nil
	case 0x04:
		return SortComponent, nil
	case 0x05:
		return SortInstance, nil
	default:
		return 0, fmt.Errorf("sort discriminant %#x is undefined", disc)
	}
}

// sortIdx reads a `sortidx` and returns the sort, discarding the index — the export enumeration reports
// the kind, not the target.
func (r *reader) sortIdx() (Sort, error) {
	s, err := r.sort()
	if err != nil {
		return 0, err
	}
	if _, err = r.u32(); err != nil {
		return 0, err
	}
	return s, nil
}

// sortIdxFull reads a `sortidx` and returns both the sort and the index, for instantiation args that
// must resolve the target.
func (r *reader) sortIdxFull() (Sort, uint32, error) {
	s, err := r.sort()
	if err != nil {
		return 0, 0, err
	}
	idx, err := r.u32()
	if err != nil {
		return 0, 0, err
	}
	return s, idx, nil
}

// coreName reads a `core:name`: a u32 length and that many UTF-8 bytes (no discriminant, unlike a
// component `nameattributes`).
func (r *reader) coreName() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	s, err := r.take(int(n))
	if err != nil {
		return "", err
	}
	return string(s), nil
}

// coreSortIdx reads a `core:sortidx`: a core:sort byte and a u32 index.
func (r *reader) coreSortIdx() (CoreSort, uint32, error) {
	b, err := r.byte()
	if err != nil {
		return 0, 0, err
	}
	switch CoreSort(b) {
	case CoreSortFunc, CoreSortTable, CoreSortMemory, CoreSortGlobal, CoreSortTag,
		CoreSortType, CoreSortModule, CoreSortInstance:
	default:
		return 0, 0, fmt.Errorf("core:sort byte %#x is undefined", b)
	}
	idx, err := r.u32()
	if err != nil {
		return 0, 0, err
	}
	return CoreSort(b), idx, nil
}
