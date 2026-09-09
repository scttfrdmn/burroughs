// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"testing"
)

// The fixture and its oracle golden. `p3hello.wasm` is the Step-0 Rust component built under the #694
// pinned toolchain (rustc 1.91.1, cargo-component 0.21.1); `p3hello.wit` is verbatim
// `wasm-tools component wit p3hello.wasm` from the pinned wasm-tools 1.258.0. Both are committed
// because the project runs external oracles once and commits their reading rather than installing them
// in CI (the wabt precedent, Makefile `spec-images`) — the golden was captured from the live tool and
// confirmed equal to it at capture; testdata/README.md records how to regenerate it, the drift check
// the wabt model likewise leaves to regeneration rather than a CI dependency.
const (
	fixtureWasm      = "testdata/p3hello.wasm"
	fixtureWIT       = "testdata/p3hello.wit"
	oracleCoreModule = 4 // `wasm-tools print p3hello.wasm` reports exactly 4 (core module ...) definitions
)

// worldExterns parses the top-level `import NAME;` / `export NAME;` declarations of the first `world`
// block in a WIT file, returning the imported and exported names. It reads only the world's own
// declarations, not the interface-definition packages that follow it.
func worldExterns(t *testing.T, witPath string) (imports, exports []string) {
	t.Helper()
	f, err := os.Open(witPath)
	if err != nil {
		t.Fatalf("open %s: %v", witPath, err)
	}
	defer f.Close()

	depth := 0
	inWorld := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "world ") && strings.HasSuffix(line, "{"):
			inWorld = true
			depth = 1
			continue
		case !inWorld:
			continue
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth <= 0 {
			break // end of the world block
		}
		decl, ok := strings.CutSuffix(line, ";")
		if !ok || strings.Contains(decl, "{") {
			continue
		}
		if name, ok := strings.CutPrefix(decl, "import "); ok {
			imports = append(imports, strings.TrimSpace(name))
		} else if name, ok := strings.CutPrefix(decl, "export "); ok {
			exports = append(exports, strings.TrimSpace(name))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", witPath, err)
	}
	return imports, exports
}

// TestComponentLoaderMatchesTheWITOracle is slice 1's exit condition: the loader loads the Step-0
// component, reaches its core modules, and enumerates its imports and exports, and that enumeration
// matches the oracle's committed reading (`wasm-tools component wit` for the extern names,
// `wasm-tools print` for the core-module count). It runs in CI against the committed golden — no
// external tool required — which is why the golden is committed rather than derived at test time.
func TestComponentLoaderMatchesTheWITOracle(t *testing.T) {
	b, err := os.ReadFile(fixtureWasm)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Version != componentVersion {
		t.Errorf("version = %#x, want %#x", c.Version, componentVersion)
	}
	if len(c.CoreModules) != oracleCoreModule {
		t.Errorf("core modules = %d, want %d (wasm-tools print)", len(c.CoreModules), oracleCoreModule)
	}
	for i, m := range c.CoreModules {
		if m == nil {
			t.Errorf("core module %d decoded to nil", i)
		}
	}

	wantImports, wantExports := worldExterns(t, fixtureWIT)
	assertNamesMatch(t, "import", loaderNames(c.Imports, nil), wantImports)
	assertNamesMatch(t, "export", loaderNames(nil, c.Exports), wantExports)

	// Every recorded section is a defined kind — an undefined id would have refused at load, so this
	// is the standing assertion that the whole section sequence stayed inside the pinned grammar.
	for _, s := range c.Sections {
		if s.Kind > SectionValue {
			t.Errorf("section kind %s is undefined", s.Kind)
		}
	}

	// The WIT oracle presents every import and export as an interface, which the binary encodes as an
	// instance externtype/sort. So the loader's kinds are all `instance` — asserted as a positive
	// statement, not left implicit, because a mis-decoded sort is exactly the silent-wrong the
	// enumeration exists to catch.
	for _, im := range c.Imports {
		if im.Kind != ExternInstance {
			t.Errorf("import %q kind = %s, want instance", im.Name, im.Kind)
		}
	}
	for _, ex := range c.Exports {
		if ex.Kind != SortInstance {
			t.Errorf("export %q kind = %s, want instance", ex.Name, ex.Kind)
		}
	}
}

func loaderNames(imports []Import, exports []Export) []string {
	var names []string
	for _, im := range imports {
		names = append(names, im.Name)
	}
	for _, ex := range exports {
		names = append(names, ex.Name)
	}
	return names
}

func assertNamesMatch(t *testing.T, what string, got, want []string) {
	t.Helper()
	g, w := append([]string(nil), got...), append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, "\n") != strings.Join(w, "\n") {
		t.Errorf("%s names differ:\n loader: %v\n oracle: %v", what, g, w)
	}
}

// TestLoadRefusesACoreModule is the "not a component" channel: a core module (preamble layer 0) is
// refused with [ErrNotComponent], distinct from a malformed component, so a caller can route it
// elsewhere rather than treat it as broken.
func TestLoadRefusesACoreModule(t *testing.T) {
	core := []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00} // core magic + version 1, layer 0
	if _, err := Load(core); err == nil {
		t.Fatal("Load accepted a core module as a component")
	} else if !strings.Contains(err.Error(), "layer 0") {
		t.Errorf("error = %v, want it to name layer 0", err)
	}
}

// TestLoadRefusesAnUndefinedSectionId is the unknown-opcode discipline (contract §9) at the section
// level: a section id past the pinned grammar refuses at load with the id named, rather than being
// silently skipped.
func TestLoadRefusesAnUndefinedSectionId(t *testing.T) {
	// A valid component preamble, then section id 13 (one past `value`) with a zero-length body.
	comp := []byte{0x00, 'a', 's', 'm', 0x0d, 0x00, 0x01, 0x00, 13, 0x00}
	_, err := Load(comp)
	if err == nil {
		t.Fatal("Load accepted an undefined section id")
	}
	if !strings.Contains(err.Error(), "undefined section id 13") {
		t.Errorf("error = %v, want it to name undefined section id 13", err)
	}
}
