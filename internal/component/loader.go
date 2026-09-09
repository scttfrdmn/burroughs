// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package component is the WebAssembly component-model binary loader (contract §6, the p3 track).
//
// This slice is **structural, not behavioural** (ADR 0084, Phase 2 slice 1): it parses the component
// binary format — the preamble, the top-level section sequence, the embedded core modules, and the
// import/export names and their kinds — so a loaded component can be enumerated and its core modules
// reached. It does **not** lift or lower values across the Canonical ABI; that is slice 2.
//
// The grammar is read at the pinned spec commit `WebAssembly/component-model @ 2bed77e` (ADR 0084).
// The internal contents of the type, canon, instance, alias, core-instance/core-type, and value
// sections are walked at the framing level and recorded, not interpreted — their semantics belong to
// later slices. An undefined section id refuses at load with the id named, the unknown-opcode
// discipline (contract §9).
package component

import (
	"encoding/binary"
	"errors"
	"fmt"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// ErrNotComponent is returned when the bytes are not a component: bad magic, or the preamble's layer
// is not 1 (a core module is layer 0). It is a distinct sentinel so a caller can tell "not a component"
// from a malformed one.
var ErrNotComponent = errors.New("component: not a component binary")

// SectionKind is a top-level component section id (Binary.md @ 2bed77e).
type SectionKind uint8

// The thirteen top-level section kinds the pin defines. An id past [SectionValue] is undefined and
// refused at load.
const (
	SectionCustom       SectionKind = 0
	SectionCoreModule   SectionKind = 1
	SectionCoreInstance SectionKind = 2
	SectionCoreType     SectionKind = 3
	SectionComponent    SectionKind = 4
	SectionInstance     SectionKind = 5
	SectionAlias        SectionKind = 6
	SectionType         SectionKind = 7
	SectionCanon        SectionKind = 8
	SectionStart        SectionKind = 9
	SectionImport       SectionKind = 10
	SectionExport       SectionKind = 11
	SectionValue        SectionKind = 12
)

func (k SectionKind) String() string {
	switch k {
	case SectionCustom:
		return "custom"
	case SectionCoreModule:
		return "core:module"
	case SectionCoreInstance:
		return "core:instance"
	case SectionCoreType:
		return "core:type"
	case SectionComponent:
		return "component"
	case SectionInstance:
		return "instance"
	case SectionAlias:
		return "alias"
	case SectionType:
		return "type"
	case SectionCanon:
		return "canon"
	case SectionStart:
		return "start"
	case SectionImport:
		return "import"
	case SectionExport:
		return "export"
	case SectionValue:
		return "value"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// Sort is a component sort — the kind an export names and a sortidx carries (Binary.md `sort`).
type Sort uint8

// The component-level sorts, plus [SortCore] for a core:sortidx (its sub-sort byte is not modelled at
// this slice; an export naming a core sort records [SortCore]).
const (
	SortCore      Sort = iota // 0x00 <core:sort>
	SortFunc                  // 0x01
	SortValue                 // 0x02
	SortType                  // 0x03
	SortComponent             // 0x04
	SortInstance              // 0x05
)

func (s Sort) String() string {
	switch s {
	case SortCore:
		return "core"
	case SortFunc:
		return "func"
	case SortValue:
		return "value"
	case SortType:
		return "type"
	case SortComponent:
		return "component"
	case SortInstance:
		return "instance"
	default:
		return fmt.Sprintf("sort(%d)", uint8(s))
	}
}

// ExternKind is the kind an import's externtype declares (Binary.md `externtype`).
type ExternKind uint8

// The externtype cases. The leading discriminant selects the kind; [ExternCoreModule] is the two-byte
// `0x00 0x11` case (a core module).
const (
	ExternCoreModule ExternKind = iota
	ExternFunc
	ExternValue
	ExternType
	ExternComponent
	ExternInstance
)

func (k ExternKind) String() string {
	switch k {
	case ExternCoreModule:
		return "core:module"
	case ExternFunc:
		return "func"
	case ExternValue:
		return "value"
	case ExternType:
		return "type"
	case ExternComponent:
		return "component"
	case ExternInstance:
		return "instance"
	default:
		return fmt.Sprintf("extern(%d)", uint8(k))
	}
}

// Import is one component-level import: its name and the kind of thing it imports.
type Import struct {
	Name string
	Kind ExternKind
}

// Export is one component-level export: its name and the sort it names.
type Export struct {
	Name string
	Kind Sort
}

// Section records one top-level section's kind and byte size, in file order — the structural spine an
// enumeration reports.
type Section struct {
	Kind SectionKind
	Size uint32
}

// Component is a loaded component's structure: the section sequence, the decoded embedded core modules,
// and the component-level imports and exports. It is the structural view slice 1 produces; it holds no
// lift/lower state (slice 2).
type Component struct {
	Version     uint16
	Sections    []Section
	CoreModules []*bin.Module
	Imports     []Import
	Exports     []Export
}

// componentVersion is the format version at the pin (Binary.md @ 2bed77e: version 0x000d, layer
// 0x0001). A different version is a different format and refuses at load, named — the same discipline
// as an unknown opcode, because the loader was written against exactly one spec state.
const (
	componentVersion = 0x000d
	componentLayer   = 0x0001
)

// Load parses a component binary into its structure. It validates the preamble, walks every top-level
// section, decodes the embedded core modules through the existing core decoder, and extracts the
// import and export names and kinds. An undefined section id, an unexpected version, or a section whose
// structured body does not consume exactly its declared size is refused, named.
func Load(b []byte) (*Component, error) {
	if len(b) < 8 || string(b[0:4]) != "\x00asm" {
		return nil, fmt.Errorf("%w: bad magic", ErrNotComponent)
	}
	version := binary.LittleEndian.Uint16(b[4:6])
	layer := binary.LittleEndian.Uint16(b[6:8])
	if layer != componentLayer {
		return nil, fmt.Errorf("%w: layer %d (a core module is layer 0)", ErrNotComponent, layer)
	}
	if version != componentVersion {
		return nil, fmt.Errorf("component: version %#x, loader is pinned to %#x", version, componentVersion)
	}

	c := &Component{Version: version}
	r := &reader{b: b, pos: 8}
	for r.pos < len(b) {
		id, err := r.byte()
		if err != nil {
			return nil, fmt.Errorf("component: section id: %w", err)
		}
		size, err := r.u32()
		if err != nil {
			return nil, fmt.Errorf("component: section %d size: %w", id, err)
		}
		kind := SectionKind(id)
		if kind > SectionValue {
			return nil, fmt.Errorf("component: undefined section id %d (size %d)", id, size)
		}
		body, err := r.take(int(size))
		if err != nil {
			return nil, fmt.Errorf("component: %s section body: %w", kind, err)
		}
		c.Sections = append(c.Sections, Section{Kind: kind, Size: size})

		switch kind {
		case SectionCoreModule:
			m, derr := (&bin.Decoder{Features: bin.DefaultFeatures()}).DecodeModule(body)
			if derr != nil {
				return nil, fmt.Errorf("component: core module %d: %w", len(c.CoreModules), derr)
			}
			c.CoreModules = append(c.CoreModules, m)
		case SectionImport:
			if perr := c.parseImports(body); perr != nil {
				return nil, perr
			}
		case SectionExport:
			if perr := c.parseExports(body); perr != nil {
				return nil, perr
			}
		default:
			// Framed and recorded; its contents are a later slice's subject.
		}
	}
	return c, nil
}

// parseImports reads an import section body: a vec of (nameattributes, externtype). It requires the
// body to be consumed exactly, so a mis-parse is a loud error rather than a silent desync.
func (c *Component) parseImports(body []byte) error {
	r := &reader{b: body}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: import count: %w", err)
	}
	for i := range n {
		name, err := r.name()
		if err != nil {
			return fmt.Errorf("component: import %d name: %w", i, err)
		}
		kind, err := r.externType()
		if err != nil {
			return fmt.Errorf("component: import %d (%q) externtype: %w", i, name, err)
		}
		c.Imports = append(c.Imports, Import{Name: name, Kind: kind})
	}
	if r.pos != len(body) {
		return fmt.Errorf("component: import section: consumed %d of %d bytes", r.pos, len(body))
	}
	return nil
}

// parseExports reads an export section body: a vec of (nameattributes, sortidx, externtype?). The
// optional externtype is an option-encoded field (0x00 none, 0x01 present). The body must be consumed
// exactly.
func (c *Component) parseExports(body []byte) error {
	r := &reader{b: body}
	n, err := r.u32()
	if err != nil {
		return fmt.Errorf("component: export count: %w", err)
	}
	for i := range n {
		name, err := r.name()
		if err != nil {
			return fmt.Errorf("component: export %d name: %w", i, err)
		}
		sort, err := r.sortIdx()
		if err != nil {
			return fmt.Errorf("component: export %d (%q) sortidx: %w", i, name, err)
		}
		present, err := r.byte()
		if err != nil {
			return fmt.Errorf("component: export %d (%q) type-ascription flag: %w", i, name, err)
		}
		switch present {
		case 0x00:
			// no ascribed externtype
		case 0x01:
			if _, terr := r.externType(); terr != nil {
				return fmt.Errorf("component: export %d (%q) ascribed externtype: %w", i, name, terr)
			}
		default:
			return fmt.Errorf("component: export %d (%q): type-ascription flag %#x is not 0x00/0x01", i, name, present)
		}
		c.Exports = append(c.Exports, Export{Name: name, Kind: sort})
	}
	if r.pos != len(body) {
		return fmt.Errorf("component: export section: consumed %d of %d bytes", r.pos, len(body))
	}
	return nil
}
