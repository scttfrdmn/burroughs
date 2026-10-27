// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The guest-side spawn door (#1122, v0.12.0; the chair's ruling of 2026-10-27, item 3). A module that imports
// `"burroughs" "spawn"` and exports `mstart` may start a new agent running `mstart(arg)`. This is contract §2 T-1's
// primitive made reachable from a guest. Its text is an append to T-1 that waits on Scott (#1122); the mechanism
// here is what that text would describe.
//
// It is the Phase 4 fork's binding: under `GOEXPERIMENT=burroughsspawn` the fork's `newosproc` calls the import, and
// `mstart` is the M's entry. The clause tests (`clause2_test.go`, `clause3_test.go`) built exactly this door inside
// their own harness. This is the same door on the public WASIP1 path, so a fork-built Go program runs with real
// threads with nothing supplied beyond the module.
const (
	spawnModule = "burroughs"
	spawnName   = "spawn"
	spawnEntry  = "mstart"
)

// errSpawnDuringInstantiation is what the door answers if a guest calls it before instantiation has returned an
// instance. That is a start function running before `_start`, which no Go guest has. The instance it would spawn on
// does not exist yet, so the call is refused rather than dereferencing nothing.
var errSpawnDuringInstantiation = errors.New(`wasi: "burroughs" "spawn" was called during instantiation, before the instance exists`)

// afterStartForTest, when set, is called with the run's instance once `_start` has returned, and before `Close`. It
// is the seam the end-to-end witness reads `PeakLiveAgents` through. It is nil in every non-test build.
var afterStartForTest func(in *interp.Instance)

// beforeStartForTest, when set, is called with the run's instance just before `_start` is invoked. It is where the
// end-to-end witness starts its sampler of `ExecutingAgents`. It is nil in every non-test build.
var beforeStartForTest func(in *interp.Instance)

// spawnDoor is one run's door: the import's declared type, the entry function, and the instance, set once
// instantiation returns.
type spawnDoor struct {
	ft    bin.FuncType
	entry uint32
	in    atomic.Pointer[interp.Instance]
}

// spawnDoorFor reports the module's spawn door, or nil if the module does not import one. A module that imports the
// door with the wrong type, or without exporting an `mstart` of type `(i32) -> ()`, is refused at link, by name.
// Without that check the guest would fail later, at its first spawn, with nothing to say why.
func spawnDoorFor(m *bin.Module) (*spawnDoor, error) {
	var ft bin.FuncType
	imported := false
	importedFuncs := uint32(0)
	for i := range m.Imports {
		im := &m.Imports[i]
		if im.Kind != bin.ExternFunc {
			continue
		}
		importedFuncs++
		if im.Module == spawnModule && im.Name == spawnName {
			if int(im.Index) >= len(m.Types) || m.Types[im.Index].Kind != bin.CompFunc {
				return nil, fmt.Errorf(`wasi: link: "burroughs" "spawn" is imported with a type that is not a function type`)
			}
			ft, imported = m.Types[im.Index].Func, true
		}
	}
	if !imported {
		return nil, nil
	}
	i32 := []bin.ValType{bin.I32}
	if !sameVals(ft.Params, i32) || (len(ft.Results) != 0 && !sameVals(ft.Results, i32)) {
		return nil, fmt.Errorf(`wasi: link: "burroughs" "spawn" must have type (i32) -> () or (i32) -> (i32), not %s`, sig(ft))
	}
	for i := range m.Exports {
		ex := &m.Exports[i]
		if ex.Name != spawnEntry || ex.Kind != bin.ExternFunc {
			continue
		}
		et, ok := funcTypeOf(m, ex.Index, importedFuncs)
		if !ok || !sameVals(et.Params, i32) || len(et.Results) != 0 {
			return nil, fmt.Errorf("wasi: link: the module imports \"burroughs\" \"spawn\", and its %q export must have "+
				"type (i32) -> (), not %s", spawnEntry, sig(et))
		}
		return &spawnDoor{ft: ft, entry: ex.Index}, nil
	}
	return nil, fmt.Errorf("wasi: link: the module imports \"burroughs\" \"spawn\" but exports no function %q, "+
		"so a spawned agent would have no entry", spawnEntry)
}

// extern is the door as a host function: spawn `mstart(arg)` on a new agent, and return its tid if the import
// declares a result.
func (d *spawnDoor) extern() interp.Extern {
	return interp.HostExtern(d.ft, func(_ *interp.Caller, v []interp.Value) ([]interp.Value, error) {
		in := d.in.Load()
		if in == nil {
			return nil, errSpawnDuringInstantiation
		}
		tid, err := in.Spawn(d.entry, v[0].Int32(), 0)
		if err != nil {
			return nil, err
		}
		if len(d.ft.Results) == 0 {
			return nil, nil
		}
		return []interp.Value{interp.I32(int32(tid))}, nil
	})
}

// funcTypeOf is function index idx's type: imported functions occupy the first importedFuncs indices, and defined
// functions follow.
func funcTypeOf(m *bin.Module, idx, importedFuncs uint32) (bin.FuncType, bool) {
	var ti uint32
	switch {
	case idx < importedFuncs:
		n := uint32(0)
		for i := range m.Imports {
			if m.Imports[i].Kind != bin.ExternFunc {
				continue
			}
			if n == idx {
				ti = m.Imports[i].Index
				break
			}
			n++
		}
	case int(idx-importedFuncs) < len(m.Funcs):
		ti = m.Funcs[idx-importedFuncs].TypeIndex
	default:
		return bin.FuncType{}, false
	}
	if int(ti) >= len(m.Types) || m.Types[ti].Kind != bin.CompFunc {
		return bin.FuncType{}, false
	}
	return m.Types[ti].Func, true
}

// sig spells a function type the way the refusals name the wanted one, `(i32) -> ()`.
func sig(ft bin.FuncType) string {
	join := func(vs []bin.ValType) string {
		var b strings.Builder
		b.WriteByte('(')
		for i, v := range vs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(v.String())
		}
		b.WriteByte(')')
		return b.String()
	}
	return join(ft.Params) + " -> " + join(ft.Results)
}

func sameVals(a, b []bin.ValType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
