// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// TestPairingArenaCostOnRealGoModules measures **the missing term** in #136's ledger: what the block-pairing
// arena costs on real Go guests, stated as bytes per module and relative to code size.
//
// # Why this is not a duplicate of the instrument that already exists
//
// `internal/interp`'s `TestEndTablePairingRepresentationsArePriced` prices the same arena at **154520 B,
// +13.2% on bodies, 36.7 B/module** — over the **spec-suite corpus**: 4216 modules, 9393 functions, and
// **2020 openers paired**. That corpus is hand-written `.wast` modules, most of them a few instructions long,
// and 86.5% of its functions open no block at all.
//
// **The chair's ruling on #837 is that this is a complexity question and #136's was a constant-factor one**,
// and the same distinction applies to the memory term: a percentage measured over 4216 tiny modules says
// nothing about one 20 MB Go binary, because the arena scales with **instructions retained**, not with modules.
// So the number this test reports is not a correction to the one above — it is a different population, and the
// two together are the ledger the flip criterion has to be written against.
//
// # The arena's size is computed, not sampled
//
// `moduleEnds` is `[]int32`, `len(Body)` slots per defined body ([ADR 0048]). So its cost is exactly
// `4 × Σ len(Body)` and can be derived from a decoded module on **either** arm — no build tag, no heap
// sampling, no run-to-run noise. A `ReadMemStats` delta would have measured the decoder's garbage as well as
// the arena, which is the wrong subject and a noisier way to get it.
//
// # It asserts a shape, and reports the figures
//
// What it pins is the **relationship**: arena bytes must equal `4 × Σ len(Body)` exactly, which is the claim
// "it scales with code and not with data" stated so it can fail. The magnitudes are logged rather than
// bounded, because a ceiling belongs in the flip's own pre-registration and **this test must not be the place
// that sets it** — a forecast cannot be pre-registered inside the measurement that produces its numbers.
//
// [ADR 0048]: ../../docs/decisions/0048-the-pairing-table-lives-in-a-per-module-arena-reached-by-one-int32-on-func-because-the-per-function-field-dominates-a-measured-bill.md
func TestPairingArenaCostOnRealGoModules(t *testing.T) {
	// Every committed guest is a real Go program with the runtime linked in, which is the population #136's
	// corpus lacks. Derived from the testdata directory rather than listed, so a guest added later is
	// measured without anyone remembering to add it here.
	guests := realGoGuests(t)
	if len(guests) < 4 {
		t.Fatalf("found %d committed guest package(s); this measurement's population is the real Go "+
			"programs in testdata, and a handful is not one", len(guests))
	}

	type row struct {
		name                       string
		funcs, bodies, instrs      int
		arenaB, codeB, importedFns int
		openers                    int
	}
	var rows []row
	for _, g := range guests {
		wasm := buildGuest(t, g)
		// Through the package's own decode path with the guest feature set, so this measures the module
		// the engine would actually retain rather than one decoded under different features.
		m, err := decodeGuest(Config{Wasm: wasm})
		if err != nil {
			// Not every guest decodes under the default feature set — the atomics one does not — and that
			// is a fact about the guest rather than a failure of this measurement.
			t.Logf("ARENA %s: not decodable under the default features (%v); excluded", g, err)
			continue
		}
		r := row{name: g, codeB: len(wasm)}
		for i := range m.Funcs {
			r.funcs++
			b := m.Funcs[i].Body
			if len(b) == 0 {
				r.importedFns++
				continue
			}
			r.bodies++
			r.instrs += len(b)
			for _, ins := range b {
				if ins.Prefix != 0 {
					continue
				}
				// The openers are counted because they are what the arena is *for*: a body with none
				// still costs `len(Body)` slots, which is the density question 0048 settled and the
				// reason a dense arena can be cheap on a corpus and expensive on one big module.
				switch ins.Op {
				case 0x02, 0x03, 0x04, 0x1f: // block, loop, if, try_table
					r.openers++
				}
			}
		}
		r.arenaB = r.instrs * 4
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Fatal("no guest decoded, so every figure below would be about an empty population")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].arenaB > rows[j].arenaB })

	t.Log("the pairing arena on real Go modules — bytes per module, and against code size:")
	var totArena, totCode, totInstr, totOpen int
	for _, r := range rows {
		totArena += r.arenaB
		totCode += r.codeB
		totInstr += r.instrs
		totOpen += r.openers
		t.Logf("  ARENA %-12s arena=%8d B  wasm=%8d B  arena/wasm=%5.1f%%  instrs=%7d  bodies=%5d  "+
			"openers=%6d (%4.1f%% of instrs)  B/opener=%6.1f",
			r.name, r.arenaB, r.codeB, 100*float64(r.arenaB)/float64(r.codeB), r.instrs, r.bodies,
			r.openers, 100*float64(r.openers)/float64(r.instrs), float64(r.arenaB)/float64(max(r.openers, 1)))
	}
	t.Logf("  ARENA %-12s arena=%8d B  wasm=%8d B  arena/wasm=%5.1f%%  instrs=%7d  openers=%6d (%4.1f%%)",
		"TOTAL", totArena, totCode, 100*float64(totArena)/float64(totCode), totInstr, totOpen,
		100*float64(totOpen)/float64(totInstr))

	// **The shape, asserted.** The arena is a function of retained instructions and nothing else; if that
	// stops holding, the "scales with code, not data" claim the flip criterion rests on is false and this is
	// where it should fail rather than in the flip's own numbers.
	for _, r := range rows {
		if r.arenaB != r.instrs*4 {
			t.Errorf("%s: arena %d B is not 4x its %d retained instructions — the arena has stopped being "+
				"a function of code size, which is the premise the flip's memory ceiling would rest on",
				r.name, r.arenaB, r.instrs)
		}
	}
	// And a floor on the population's own size, so a run where the guests failed to build reports a
	// suspiciously small bill rather than a clean one.
	if totInstr < 100000 {
		t.Errorf("the whole population retains only %d instructions; real Go guests link the runtime and "+
			"should be far larger, so this is a build or decode failure wearing a measurement's clothes",
			totInstr)
	}
}

// realGoGuests derives the guest package names from the testdata directory, so a guest added later is measured
// without anyone remembering to add it here — *derive the domain, never enumerate it*.
func realGoGuests(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		// A directory is a Go guest if it holds **any** buildable `.go` file. The first draft looked for
		// `main.go` and found three of nine: this tree names them `cat.go`, `hello.go`, `dirread.go` and so
		// on, so the filename convention was mine rather than the tree's. The floor below is what caught it.
		names, rerr := os.ReadDir("testdata/" + e.Name())
		if rerr != nil {
			continue
		}
		hasGo := false
		for _, n := range names {
			if !n.IsDir() && strings.HasSuffix(n.Name(), ".go") {
				hasGo = true
				break
			}
		}
		if !hasGo {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}
