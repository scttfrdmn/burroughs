// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"strings"
	"sync/atomic"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The component spawn door (#1337, ADR 0131), contract §2 T-1's component paragraph, on Scott's ruling 2 on #1332
// (D-F4): a component may import
//
//	burroughs:threads/spawn@0.1.0#spawn: func(arg: u32)
//
// and a call starts a new agent running the core export `mstart(arg)`, an OS-locked `interp` thread with its own
// T-6 globals, as a thread of the task that called the import (`asyncHandles.spawnAgent`, ADR 0130). It is the
// component twin of the preview-1 door (`internal/wasi/spawn.go`), and it is Burroughs-only by construction.
//
// # Which `mstart`
//
// **The core instance that imports the lowered door is the one whose `mstart` runs** (ADR 0131). The text says *"the
// core export `mstart` of type `(i32) -> ()`, resolved by the engine"*, so it is a core instance's export and not a
// component-level one, and which instance's is the engine's to say. The importer is the preview-1 shape one level
// in (*"a module that imports it … and exports `mstart`"*), it is decided the moment that instance exists, and it is
// the instance whose code calls the door. T-1 also says the agent *"shares the component's shared canonical
// memory"*, so the importer must use one of the component's canonical memories, checked when the walk ends.
//
// # The refusals, all at link
//
//   - **gate:cm-shared-memory off**: at the lower, since without the gate no canonical memory can be shared;
//   - **a component type other than `func(arg: u32)`**: at the lower;
//   - **a core import type other than `(i32) -> ()`**: when the importer is linked;
//   - **no `mstart` of type `(i32) -> ()` in the importer**: when the importer is instantiated;
//   - **an instance that is not free** (`asyncHandles.agentsRunFree`: an unshared canonical memory, or none), or an
//     importer that uses no canonical memory: when the walk ends, since a later canon can still change either.
//
// A call that fails to start an agent is a host trap of its caller, as the preview-1 door's is.

// isSpawnDoor reports whether a lowered import's identity is the spawn door's, under ADR 0115's class keying.
func isSpawnDoor(lowerName string) bool {
	_, ok, _ := lookupHost(spawnDoors, lowerName)
	return ok
}

// spawnDoor is one core instance's binding of the door.
type spawnDoor struct {
	name string           // the lowered identity, as the guest imported it
	inst *interp.Instance // the importer, set when its instantiation returns; written and read by the walk alone
	// core is the importer once the walk has accepted the door. **A call before that is refused**: a start function
	// would spawn into an instance whose walk could still refuse it, or whose free predicate a later canon could
	// still change.
	core atomic.Pointer[interp.Instance]
}

// spawnDoorStep is the canon lower of the door: refuse it with the gate off or at a type other than T-1's, and
// record a core func the resolver binds to the importer.
func (w *walker) spawnDoorStep(name string, sig *FuncType) error {
	if !cmSharedMemoryEnabled() {
		return fmt.Errorf("%w: the component imports %s, which is offered only with gate:cm-shared-memory on: its "+
			"agents share the component's shared canonical memory, which the Canonical ABI forbids and Burroughs admits "+
			"only behind that gate (contract §2 T-1, ADR 0129). Set %s=1 to admit it (#1337, ADR 0131)",
			ErrUnsupportedForm, name, cmSharedMemoryGateEnv)
	}
	if sig == nil || sig.Async || sig.Result != nil || len(sig.Params) != 1 || sig.Params[0].Type.Kind != VU32 {
		return fmt.Errorf("%w: the component imports %s with type %s; the door's type is func(arg: u32) "+
			"(contract §2 T-1, #1337, ADR 0131)", ErrLinkRefused, name, spawnSigName(sig))
	}
	if w.doorLowered == "" {
		w.doorLowered = name
	}
	w.appendCore(SpaceCoreFunc, coreDef{lowerName: name, spawnDoor: true})
	return nil
}

// spawnSigName spells a lowered door's component type for its refusal.
func spawnSigName(sig *FuncType) string {
	if sig == nil {
		return "unresolved"
	}
	var b strings.Builder
	if sig.Async {
		b.WriteString("async ")
	}
	b.WriteString("func(")
	for i, p := range sig.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name + ": " + valKindName(p.Type.Kind))
	}
	b.WriteByte(')')
	if sig.Result != nil {
		b.WriteString(" -> " + valKindName(sig.Result.Kind))
	}
	return b.String()
}

// bindSpawnDoor binds the door into the core module being linked, at the module's own import type, which must be
// `(i32) -> ()`, the flattening of `func(arg: u32)`. A wrong one is recorded for coreInstanceStep to report, since
// a resolver can only decline.
func (w *walker) bindSpawnDoor(ft bin.FuncType, name string) (interp.Extern, bool) {
	if len(ft.Params) != 1 || ft.Params[0] != bin.I32 || len(ft.Results) != 0 {
		w.doorErr = fmt.Errorf("%w: a core module imports %s as a core function with %d params and %d results; "+
			"the door lowers to (i32) -> () (contract §2 T-1, #1337, ADR 0131)", ErrLinkRefused, name,
			len(ft.Params), len(ft.Results))
		return interp.Extern{}, false
	}
	d := &spawnDoor{name: name}
	w.pendingDoors = append(w.pendingDoors, d)
	return w.lowerExtern(ft, mayLeaveGuard(name, d.call(w.async)), interp.CanonOptions{}), true
}

// adoptSpawnDoors gives the doors bound while linking `in` their instance, and refuses it at link if it exports no
// `mstart` of type `(i32) -> ()` with a guest body: a spawned agent would have no entry, and the guest would fail
// at its first spawn with nothing to say why. `ci` is the instance's index, for the refusal.
func (w *walker) adoptSpawnDoors(in *interp.Instance, ci int) error {
	pending := w.pendingDoors
	w.pendingDoors = nil
	if len(pending) == 0 {
		return nil
	}
	ext, ok := in.Export(spawnEntry)
	ft, host, typed := ext.FuncSignature()
	switch {
	case !ok || ext.Kind != bin.ExternFunc:
		return fmt.Errorf("%w: core instance %d imports %s but exports no function %q, so a spawned agent would "+
			"have no entry (contract §2 T-1, #1337, ADR 0131)", ErrLinkRefused, ci, pending[0].name, spawnEntry)
	case !typed || len(ft.Params) != 1 || ft.Params[0] != bin.I32 || len(ft.Results) != 0:
		return fmt.Errorf("%w: core instance %d imports %s, and its %q export must have type (i32) -> (), not %d "+
			"params and %d results (contract §2 T-1, #1337, ADR 0131)", ErrLinkRefused, ci, pending[0].name,
			spawnEntry, len(ft.Params), len(ft.Results))
	case host:
		return fmt.Errorf("%w: core instance %d imports %s, and its %q export is a host function, which runs no "+
			"guest body (contract §5, ADR 0069; #1337, ADR 0131)", ErrLinkRefused, ci, pending[0].name, spawnEntry)
	}
	for _, d := range pending {
		d.inst = in
	}
	w.doors = append(w.doors, pending...)
	return nil
}

// finishSpawnDoors is the door's check once the walk has seen every canon: the instance must be free, and each
// importer must use one of its canonical memories, so the agents share it (contract §2 T-1). Then the doors open.
func (w *walker) finishSpawnDoors() error {
	if w.doorLowered == "" {
		return nil
	}
	// NEUTER, run: drop this check, and TestTheSpawnDoorIsRefusedByName's unshared-canon row instantiates.
	if !w.async.agentsRunFree {
		why := "names no canonical memory"
		if w.async.canonMemories.unshared > 0 {
			why = "names an unshared canonical memory"
		}
		return fmt.Errorf("%w: the component imports %s, which is offered only to an instance whose canonical memory "+
			"is shared, and this one %s: its agents would run in parallel over a memory they do not share "+
			"(contract §2 T-1, ADR 0130's §1; #1337, ADR 0131)", ErrUnsupportedForm, w.doorLowered, why)
	}
	for _, d := range w.doors {
		shares := false
		for _, m := range w.canonMems {
			if d.inst.UsesMemory(m) {
				shares = true
				break
			}
		}
		if !shares {
			return fmt.Errorf("%w: a core instance imports %s and uses none of the component's canonical memories, "+
				"so its agents would not share the canonical memory (contract §2 T-1, #1337, ADR 0131)",
				ErrLinkRefused, d.name)
		}
	}
	for _, d := range w.doors {
		d.core.Store(d.inst)
	}
	return nil
}

// call is the door as a canon-lowered sync import: start an agent at the importer's `mstart` with the argument, as
// a thread of the calling Thread's task. A spawn that fails is the caller's host trap, as the preview-1 door's is.
func (d *spawnDoor) call(h *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		core := d.core.Load()
		if core == nil {
			return nil, fmt.Errorf("%w: %s was called while its component was being instantiated, before the "+
				"walk accepted the door (#1337, ADR 0131)", ErrUnsupportedForm, d.name)
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("component: %s: got %d core args, want 1 (arg)", d.name, len(args))
		}
		// NEUTER, run: invoke mstart here, on the caller's own goroutine (`core.Invoke(spawnEntry, arg)` for
		// spawnAgent), and TestAComponentStartsItsOwnAgentsThroughTheSpawnDoor's peak reads 1, its agents print
		// `released=00000000` after their 2s bound, and they share run's globals: agent 1 reads 0x77 at entry.
		if err := h.spawnAgent(c, core, spawnEntry, int32(uint32(args[0].Bits))); err != nil {
			return nil, fmt.Errorf("component: %s: %w", d.name, err)
		}
		return nil, nil
	}
}

// spawnEntry is the export a spawned agent starts at, T-1's `mstart`, as the preview-1 door's.
const spawnEntry = "mstart"
