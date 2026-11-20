// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
)

// TestATagIsNotCountedAsACoreInstance is #1258's core tag space: a core tag alias adds to the tag space, not the
// core instance space. Both fixtures decode with exception handling on, which the default feature set gates off.
// tag-as-instance names `(instance 1)` where one core instance precedes it, and wasmtime 49.0.3 refuses it at parse,
// 10 of 10 (tag-as-instance.reading); Burroughs refuses it at load, as a forward reference. tag-inline-export, the
// control, hands the aliased tag over through an inline-export instance; wasmtime parses it, and Burroughs walks it.
//
// Neutered, run: coreSortSpace without its tag case (the branch before the fix), and tag-as-instance loads, the tag
// counted as core instance 1, to fail at instantiate as `unknown import: "x" "t"`. The control still walks.
func TestATagIsNotCountedAsACoreInstance(t *testing.T) {
	raw, err := os.ReadFile("testdata/tag-as-instance.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	feats := bin.DefaultFeatures()
	feats.ExceptionHandling = true
	rows := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		fx, reading, ok := strings.Cut(ln, ": ")
		if !ok {
			t.Fatalf("tag-as-instance.reading: row %q has no `: `", ln)
		}
		rows++
		t.Run(fx, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + fx + ".wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			c, err := LoadWithFeatures(b, feats)
			switch {
			case strings.HasPrefix(reading, "refused: "):
				if err == nil || !strings.Contains(err.Error(), "references core:instance index 1") {
					t.Fatalf("Load = %v; want the forward reference to core:instance 1 refused (the oracle: %s)", err, reading)
				}
			case strings.HasPrefix(reading, "parsed: "):
				if err != nil {
					t.Fatalf("Load: %v; the oracle parses it", err)
				}
				w, werr := c.walkComponent(stubHost, nil, nil, nil, hostResourceSource{})
				if werr != nil {
					t.Fatalf("walk: %v; the oracle parses it", werr)
				}
				w.close()
			default:
				t.Fatalf("tag-as-instance.reading: reading %q is neither refused nor parsed", reading)
			}
		})
	}
	if rows != 2 {
		t.Fatalf("tag-as-instance.reading has %d rows, want 2", rows)
	}
}

// TestAnExportAddsAnIndexToItsSort is #1258's witness: an export introduces a new index in its sort, so every
// ordinal after it counts it. export-index exports a type and then defines two function types after it, and
// re-exports an exported func's index. Each row of export-index.reading is wasmtime 49.0.3's, 10 of 10: `inc 5` and
// `inc2 5` are 6 and `k` is 42.
//
// Neutered, run: parseExports recording no Def (main's code before #1258), and all three rows fail. `inc`'s lift
// resolves type 2 to `$g`, so `inc 5` is refused as taking no parameters; `k`'s lift resolves type 3 to nothing, so
// `k` has no resolved signature; and `inc2` is no callable export. With the Def kept and only funcStep's export arm
// dropped, `inc2` alone fails.
func TestAnExportAddsAnIndexToItsSort(t *testing.T) {
	b, err := os.ReadFile("testdata/export-index.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	raw, err := os.ReadFile("testdata/export-index.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(nil, nil, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(in.Close)
	rows := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		call, reading, ok := strings.Cut(ln, ": ")
		if !ok {
			t.Fatalf("export-index.reading: row %q has no `: `", ln)
		}
		rows++
		t.Run(call, func(t *testing.T) {
			fields := strings.Fields(call)
			var args []uint32
			for _, f := range fields[1:] {
				a, perr := strconv.ParseUint(f, 10, 32)
				if perr != nil {
					t.Fatalf("export-index.reading: argument %q is not a u32", f)
				}
				args = append(args, uint32(a))
			}
			want, perr := strconv.ParseUint(reading, 10, 32)
			if perr != nil {
				t.Fatalf("export-index.reading: reading %q is not a u32", reading)
			}
			got, err := callU32(in, fields[0], args...)
			if err != nil {
				t.Fatalf("%s: %v; the oracle reads %d", call, err, want)
			}
			if uint64(got) != want {
				t.Fatalf("%s = %d; the oracle reads %d", call, got, want)
			}
		})
	}
	if rows != 3 {
		t.Fatalf("export-index.reading has %d rows, want 3", rows)
	}
}

// TestAnExportOfACoreFuncIsRefused is the other half of #1258's loader change: an export of a core sort other than
// `core module` is invalid (Binary.md), and since an export now adds an index to its sort, it is refused at load
// rather than counted into a core space the walk does not grow for it. export-core-module exports a core module,
// which loads; the same bytes with the export's core sort patched to func are refused, naming the sort.
//
// **No wasmtime reading exists, and the patch is in memory, because no committed bytes could be made.**
// `wasm-tools parse` 1.258.0 refuses the text before it assembles anything: `(export "f" (core func $f))` and
// `(export "f" (core func 0))` both fail with "expected keyword `module`", since its grammar admits only a core
// module after `core` in an export. A scratch file patched by an inline interpreter was refused by the editroute
// hook, and that route was not pursued.
//
// Neutered, run: the refusal dropped from parseExports, and the patched component loads.
func TestAnExportOfACoreFuncIsRefused(t *testing.T) {
	b, err := os.ReadFile("testdata/export-core-module.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	if _, err = Load(b); err != nil {
		t.Fatalf("the control, which exports a core module, is refused: %v", err)
	}
	// The export section is `0b <size> 01 <name "m"> 00 11 00 00`: one export, named m, of core sort module, index
	// 0, with no ascription. Patched to core sort func, index 0, which the alias defines.
	sec := []byte{0x0b, 0x08, 0x01, 0x00, 0x01, 'm', 0x00, 0x11, 0x00, 0x00}
	at := bytes.Index(b, sec)
	if at < 0 || bytes.Contains(b[at+1:], sec) {
		t.Fatalf("export-core-module.wasm does not hold its export section exactly once")
	}
	patched := append([]byte(nil), b...)
	patched[at+7] = byte(CoreSortFunc)
	_, err = Load(patched)
	if err == nil || !strings.Contains(err.Error(), "only a core module may be exported") {
		t.Fatalf("Load of a core func export = %v; want the refusal naming the sort", err)
	}
}

// TestACoreInstanceNamesItsModuleByIndexSpace is #1258 on the core side: a core instance's `instantiate N` names an
// ordinal of the core-module index space, which an exported core module and an outer alias of one grow, and not a
// position among the section's modules. Each row of core-module-index.reading is wasmtime 49.0.3's, 10 of 10: both
// fixtures' `g` is 7, the module the index space names, where the section position names another or none.
//
// Neutered, run: coreInstanceStep indexing w.c.CoreModules again, and both rows are refused at instantiate,
// core-module-export as naming module 2 of 2 and core-module-outer as naming module 1 of 1. With coreModuleStep's
// outer-alias arm dropped, core-module-outer alone is refused, as an alias this engine does not resolve.
func TestACoreInstanceNamesItsModuleByIndexSpace(t *testing.T) {
	gRowsMatchTheOracle(t, "core-module-index.reading", 2)
}

// TestAComponentIsNamedByItsIndexSpace is #1258 on the component side: a component instance's `instantiate N`
// names an ordinal of the component index space, which an outer alias grows, and an outer alias inside a component
// names the scope that defines that component. Each row of component-index.reading is wasmtime 49.0.3's, 10 of 10:
// both fixtures' `g` is 7.
//
// Neutered, run: componentStep reading every entry as a nested component section entry again (and origin/main,
// which does), and both rows panic with an index out of range, which this test reports as failures. With
// the defining scope replaced by the instantiating walker, component-outer-scope's `g` is 5, the middle
// component's module.
func TestAComponentIsNamedByItsIndexSpace(t *testing.T) {
	gRowsMatchTheOracle(t, "component-index.reading", 2)
}

// gRowsMatchTheOracle runs each row of a reading, `<fixture>: <u32>`, as testdata/<fixture>.wasm's `g()`. A panic
// on the way is a failure of the row, not of the test binary.
func gRowsMatchTheOracle(t *testing.T, reading string, wantRows int) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + reading)
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	rows := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		fx, val, ok := strings.Cut(ln, ": ")
		if !ok {
			t.Fatalf("%s: row %q has no `: `", reading, ln)
		}
		rows++
		t.Run(fx, func(t *testing.T) {
			want, perr := strconv.ParseUint(val, 10, 32)
			if perr != nil {
				t.Fatalf("%s: reading %q is not a u32", reading, val)
			}
			b, err := os.ReadFile("testdata/" + fx + ".wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("panicked: %v; the oracle reads g() = %d", p, want)
				}
			}()
			in, err := InstantiateWithHost(b, NewHost(nil, nil, nil))
			if err != nil {
				t.Fatalf("instantiate: %v; the oracle reads g() = %d", err, want)
			}
			t.Cleanup(in.Close)
			if got, err := callU32(in, "g"); err != nil || uint64(got) != want {
				t.Fatalf("g() = %d, %v; the oracle reads %d", got, err, want)
			}
		})
	}
	if rows != wantRows {
		t.Fatalf("%s has %d rows, want %d", reading, rows, wantRows)
	}
}
