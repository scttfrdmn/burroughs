// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// forkP3Dir holds F1's components, their sources, their wasmtime readings and their PROVENANCE. The bytes
// are committed, so this test needs no fork and runs wherever the package's other tests do.
const forkP3Dir = "testdata/forkp3"

// TestForkWasip3MatchesWasmtime is F1's witness (#1236, ADR 0114): Go programs built for the fork's
// GOOS=wasip3, formed into WASI 0.3 components by `wasm-tools component new` with no adapter, run on
// Burroughs as they run on wasmtime 49.0.3.
//
//   - p3exit0, `func main() {}`, exits 0: main returns, the runtime calls wasi:cli/exit@0.3.0's exit with ok.
//   - p3panic, `func main() { panic("x") }`, traps `unreachable`: F1 lowers no stdout and no clock, so the
//     runtime's fatal path traps at its first refused host call (forkp3.reading names the frame).
//
// The expected outcomes are wasmtime's, read from the committed forkp3.reading, not chosen here. The runs go
// through CallRun, the call ComponentConfig.Run makes. The core-module subtest asserts F1's other claim:
// the Go module imports only Canonical ABI `wasi:` names, so no adapter stands between it and the host.
//
// Neutered, each run:
//   - p3exit0.wasm replaced by p3panic.wasm: p3exit0's run traps `unreachable` where wasmtime's reading is
//     exit 0.
//   - p3exit0.wasm replaced by a component built from the fork's wasip1 `_start` (GOEXPERIMENT=componentabi,
//     the same source) under an empty world: its run is refused, "no wasi:cli/run export", and its core
//     module imports nothing, which the core-module subtest refuses as vacuous. `wasm-tools component new`
//     refuses that module against wasi:cli/command@0.3.0 outright: "failed to find export of interface
//     `wasi:cli/run@0.3.0` function `run`".
//   - p3panic.wasm replaced by p3exit0.wasm: p3panic's run returns (0, nil) where wasmtime traps.
//   - the prefix asked of each import narrowed to "wasi:http": the subtest names "wasi:cli/exit@0.3.0"
//     "exit", so the loop reads the import it is about.
func TestForkWasip3MatchesWasmtime(t *testing.T) {
	for _, c := range []string{"p3exit0", "p3panic"} {
		t.Run(c, func(t *testing.T) {
			want := readingLine(t, filepath.Join(forkP3Dir, "forkp3.reading"), "wasmtime 49.0.3 on "+c+", 10 of 10")
			in, err := InstantiateWithHost(readForkP3(t, c), NewHost(io.Discard, io.Discard, nil))
			if err != nil {
				t.Fatalf("%s did not instantiate: %v", c, err)
			}
			defer in.Close()
			code, err := in.CallRun()
			switch {
			case strings.HasPrefix(want, "exit "):
				wantCode, cerr := strconv.Atoi(strings.TrimPrefix(want, "exit "))
				if cerr != nil {
					t.Fatalf("forkp3.reading's %s line %q: %v", c, want, cerr)
				}
				if err != nil {
					t.Fatalf("%s's run failed where wasmtime exits %d: %v", c, wantCode, err)
				}
				if code != wantCode {
					t.Errorf("%s exited %d, want wasmtime's %d", c, code, wantCode)
				}
			case strings.HasPrefix(want, "trap "):
				var tr *interp.Trap
				if !errors.As(err, &tr) {
					t.Fatalf("%s's run = (%d, %v), want a trap, as wasmtime traps", c, code, err)
				}
				if reason := strings.TrimPrefix(want, "trap "); tr.Reason != reason {
					t.Errorf("%s trapped %q, want wasmtime's %q", c, tr.Reason, reason)
				}
			default:
				t.Fatalf("forkp3.reading's %s line %q is neither an exit nor a trap", c, want)
			}
		})
	}

	t.Run("core module imports only wasi names", func(t *testing.T) {
		comp, err := Load(readForkP3(t, "p3exit0"))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		// One core module is the no-adapter shape: an adapted component carries the adapter's module too.
		if len(comp.CoreModules) != 1 {
			t.Fatalf("p3exit0 carries %d core modules, want 1, the Go module and no adapter", len(comp.CoreModules))
		}
		imports := comp.CoreModules[0].Imports
		// An empty list would pass the loop below for no reason, so the vacuous case is refused by name.
		if len(imports) == 0 {
			t.Fatal("p3exit0's core module imports nothing, so the Canonical ABI names it should import were not asked about")
		}
		for _, im := range imports {
			if !strings.HasPrefix(im.Module, "wasi:") {
				t.Errorf("p3exit0's core module imports %q %q, which is not a wasi: interface name", im.Module, im.Name)
			}
		}
	})
}

func readForkP3(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(forkP3Dir, name+".wasm"))
	if err != nil {
		t.Fatalf("%s.wasm is missing (it is committed; PROVENANCE records how it was built): %v", name, err)
	}
	return b
}
