// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"testing"
)

// The instantiation-graph oracle: wasm-tools' reading of p3hello's instantiation sections, committed as
// counts the parser must reproduce (the loader precedent — an external oracle read once, its reading
// committed, no tool in CI). `wasm-tools dump p3hello.wasm` reports these.
const (
	oracleCoreInstances   = 17
	oracleAliasExport     = 27
	oracleAliasCoreExport = 24
	oracleCanonLift       = 1
	oracleCanonLower      = 15
	oracleCanonResDrop    = 4
	oracleInstances       = 1
)

// TestInstantiationSectionsMatchOracle is B.1's exit: the loader parses p3hello's core:instance, alias,
// canon, and instance sections, and the parsed graph's shape matches wasm-tools' reading. A mis-parse
// is caught either here or by the consumed-equals-section-size self-check in each parser.
func TestInstantiationSectionsMatchOracle(t *testing.T) {
	b, err := os.ReadFile(fixtureWasm)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(c.CoreInstances) != oracleCoreInstances {
		t.Errorf("core instances = %d, want %d", len(c.CoreInstances), oracleCoreInstances)
	}
	if len(c.Instances) != oracleInstances {
		t.Errorf("component instances = %d, want %d", len(c.Instances), oracleInstances)
	}

	var aliasExport, aliasCore, aliasOuter int
	for _, a := range c.Aliases {
		switch a.Kind {
		case AliasExport:
			aliasExport++
		case AliasCoreExport:
			aliasCore++
		case AliasOuter:
			aliasOuter++
		}
	}
	if aliasExport != oracleAliasExport || aliasCore != oracleAliasCoreExport || aliasOuter != 0 {
		t.Errorf("aliases = export %d / core %d / outer %d, want %d / %d / 0",
			aliasExport, aliasCore, aliasOuter, oracleAliasExport, oracleAliasCoreExport)
	}

	canon := map[CanonKind]int{}
	for _, cn := range c.Canons {
		canon[cn.Kind]++
	}
	if canon[CanonLift] != oracleCanonLift || canon[CanonLower] != oracleCanonLower || canon[CanonResourceDrop] != oracleCanonResDrop {
		t.Errorf("canons = lift %d / lower %d / drop %d, want %d / %d / %d",
			canon[CanonLift], canon[CanonLower], canon[CanonResourceDrop], oracleCanonLift, oracleCanonLower, oracleCanonResDrop)
	}

	// The canon lift names the run function's component type; a lift with no type index would mean the
	// lift/lower tail parse desynced.
	for i, cn := range c.Canons {
		if cn.Kind == CanonLift && cn.TypeIdx == 0 && cn.FuncIdx == 0 {
			t.Errorf("canon %d is a lift with zero func and type index — a likely tail desync", i)
		}
	}
}

// componentPreamble is a valid component header (magic + version 0x0d + layer 1) for hand-built refusal
// fixtures.
var componentPreamble = []byte{0x00, 'a', 's', 'm', 0x0d, 0x00, 0x01, 0x00}

// section frames one section: id, a single size byte (bodies here are < 128), then the body.
func section(id byte, body ...byte) []byte {
	return append([]byte{id, byte(len(body))}, body...)
}

// TestCanonBuiltinOutsideScopeIsRefused witnesses the refuse-at-parse-by-name discipline for the canon
// section: a canon built-in this slice does not model (here 0x08, the async/thread family) is refused
// with the byte named, not silently skipped.
func TestCanonBuiltinOutsideScopeIsRefused(t *testing.T) {
	// canon section: count 1, then built-in 0x08 (outside lift/lower/resource.*).
	comp := append(append([]byte(nil), componentPreamble...), section(byte(SectionCanon), 0x01, 0x08)...)
	if _, err := Load(comp); err == nil {
		t.Fatal("Load accepted an unmodeled canon built-in")
	}
}

// TestConsumedEqualsSizeCatchesADesync witnesses the self-check: an alias section whose declared size
// exceeds what its one alias consumes is refused, rather than leaving trailing bytes unparsed.
func TestConsumedEqualsSizeCatchesADesync(t *testing.T) {
	// alias section: count 1, one outer alias (sort 0x03 type, disc 0x02, ct 0, idx 0) = 4 bytes of
	// content after the count; declare the section one byte too long so a trailing byte is left over.
	aliasBody := []byte{0x01, 0x03, 0x02, 0x00, 0x00, 0x00} // count=1, sort=type, outer, ct=0, idx=0, + stray 0x00
	comp := append(append([]byte(nil), componentPreamble...), section(byte(SectionAlias), aliasBody...)...)
	_, err := Load(comp)
	if err == nil {
		t.Fatal("Load accepted an alias section with a trailing unparsed byte")
	}
}

// TestForwardReferenceRefusedOnTheStream is the B.1-finding fix witnessed on the right structure: the
// ordering rule is enforced across sorts on the definition stream, so a reference to an index not yet
// defined is refused — including a cross-sort case (a canon lift, which defines a component func,
// referencing a core func that no earlier definition provides). p3hello, valid, passes.
func TestForwardReferenceRefusedOnTheStream(t *testing.T) {
	b, err := os.ReadFile(fixtureWasm)
	if err != nil {
		t.Fatal(err)
	}
	if _, e := Load(b); e != nil {
		t.Fatalf("valid p3hello refused: %v", e)
	}

	// A component whose only definition is a `canon lift` referencing core func 0 — a cross-sort
	// forward reference (the lift is a component-func definition; the core-func space is empty). Encoding:
	// canon section, count 1, then 0x00 (lift) 0x00 (func sort) 0x00 (corefuncidx) 0x00 (opts count)
	// 0x00 (typeidx).
	canonBody := []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00}
	comp := append(append([]byte(nil), componentPreamble...), section(byte(SectionCanon), canonBody...)...)
	_, err = Load(comp)
	if err == nil {
		t.Fatal("Load accepted a canon lift referencing an undefined core func — a cross-sort forward reference")
	}
}
