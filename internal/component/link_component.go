// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"os"
	"strings"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The component-space half of the instantiation walk (PR B, to run-callable): canon lift, component
// instances (a nested component is the same walk applied to its own stream, its imports supplied by the
// instantiation args), and the component-level exports. Value marshaling through the codec and the
// borrow-lifetime discipline are the remaining exit work; run() moves no values, so it is callable
// without them.

// compFunc is a component-level function. Invoking it runs the lifted core func through its owning
// instance; a stub func (from an import the engine does not fill) refuses by name. run() has no
// parameters and an empty result, so no value marshaling is needed here.
type compFunc struct {
	core     coreDef
	stubName string    // non-empty: a refusing host func naming the unfilled import
	sig      *FuncType // the component signature of a canon lift (its export type), for the Call refusal

	// Async (callback) lift fields (2nd async guest, #785): when async is set, invoke() runs the callback
	// loop rather than a single sync call. `core` is the callee (the first call, with the lifted params);
	// `h` holds the current lift task. `cb` is the callback core func for re-entry, threaded in #871 —
	// which is also when it was first captured at all: before that, `cn.Opts.Callback` was only
	// nil-checked to set `async`, so nothing held the func the loop had to call.
	async  bool
	h      *asyncHandles
	cb     coreDef        // the `(callback $f)` canonopt's core func — the re-entry target
	result []interp.Value // the async lift's last resolution (what task.return lowered), for the caller/oracle
}

func (f *compFunc) invoke() error { return f.invokeWith(nil) }

// invokeWith is invoke carrying flat core params (#864's value-carrying export call). `invoke()` is this
// with none, which is what run() needs — kept as the name every existing caller uses, so adding params
// did not touch any of them.
func (f *compFunc) invokeWith(params []interp.Value) error {
	if f.stubName != "" {
		return fmt.Errorf("%w: %s (stub host)", ErrLinkRefused, f.stubName)
	}
	if f.core.inst == nil {
		return fmt.Errorf("%w: component func has no invocable core func (lift target unresolved)", ErrUnsupportedForm)
	}
	if f.async {
		return f.invokeAsyncLiftWith(params)
	}
	_, err := f.core.inst.Invoke(f.core.name, params...)
	return err
}

// invokeAsyncLift runs the stackless (callback) async-lift loop (canon_lift def:2126-2151). Step 1 is the
// loop skeleton: create the durable lift task, invoke the callee ONCE with the lifted params, decode the
// packed return, and on EXIT confirm the task was resolved by task.return. The WAIT/YIELD re-entry and park
// are step 2. The first-call path is explicit here — a shared helper would blur the callee (params) and the
// callback (event), which #788's multi-cycle pin exists to catch.
// invokeAsyncLiftWith carries the lifted params (#864). The comment on the first call below already
// anticipated them — "carrying the lifted params (none for run())" — and this supplies them.
//
// There is no no-params wrapper beside it: `invokeWith(nil)` reaches here with an empty slice, so a
// `invokeAsyncLift()` delegating to this was dead the moment it was written, and the `unused` linter said
// so. One entry point, and the params-free case is a nil argument rather than a second name for it.
func (f *compFunc) invokeAsyncLiftWith(params []interp.Value) error {
	// At-most-one lift task per component INSTANCE, asserted: an unbound host call into an async-lifted
	// export is a shape the engine permits, so an entry while the instance already hosts a lift traps by
	// name rather than resolving the wrong task (witnessed on synth bytes, #732).
	//
	// **The scope is the instance, not the agent**, and this comment said "agent" until #857's recon
	// measured it (see testdata/asynclift/PARITY-BLOCKERS.md): `asyncHandles` is per-instance, so a second
	// agent's lift traps here too. That is the blocker the concurrent parity arm hits, and #869 is where
	// the assertion becomes per-agent — which is what this comment always claimed.
	task := &liftTask{cb: f.cb}
	f.h.mu.Lock()
	if f.h.liftInFlight {
		f.h.mu.Unlock()
		return &interp.Trap{Reason: "async canon lift entered while another lift task is already in flight in this component instance"}
	}
	f.h.liftInFlight = true
	f.h.mu.Unlock()
	// Teardown keys on the task, not the loop: release the in-flight marker however invoke exits
	// (normal EXIT, a trap, a park's bound expiring, or — step 3 — cancellation resolving without a
	// normal EXIT).
	//
	// **This clears `liftInFlight`, not `lift`.** The current-task slot is restored by each entry's own
	// leaveTask, so by the time this runs it already holds whatever it held before the loop. Clearing it
	// here too would overwrite an OUTER task's slot in the nested case — the bug the two fields were
	// separated to make impossible.
	defer func() {
		f.h.mu.Lock()
		f.h.liftInFlight = false
		f.h.mu.Unlock()
	}()

	// First call: the callee, carrying the lifted params (none for run()). Its packed return drives dispatch.
	res, err := f.enterAndInvoke(task, f.core, params)
	if err != nil {
		return err
	}

	// The loop. Each iteration decodes one packed return and either resolves, re-enters immediately
	// (YIELD), or parks and re-enters with an event (WAIT). The callee's return and every callback's
	// return go through the SAME decode — a second decode for the re-entry path is how the two could
	// drift, which is what #788's multi-cycle pin exists to catch.
	for {
		if len(res) != 1 {
			return fmt.Errorf("%w: async lift callee returned %d core values, want 1 (packed)", ErrUnsupportedForm, len(res))
		}
		code, si, uerr := unpackCallbackResult(uint32(res[0].Bits))
		if uerr != nil {
			return uerr
		}
		switch code {
		case callbackExit:
			// **Read the LOCAL task, never the shared slot.** This was `f.h.lift.resolved`, which is
			// correct only while one task exists: with interleaving it asks whether *whatever task is
			// current* resolved, and the answer can be another task's. A wrong answer here is silent —
			// the caller gets the wrong result or a spurious trap — which is worse than the visible
			// at-most-one assertion the same slice had to split in two.
			if !task.resolved {
				return &interp.Trap{Reason: "async lift returned EXIT without resolving its task via task.return"}
			}
			f.result = task.result // the resolution, for the caller/oracle to read
			return nil

		case callbackYield:
			// A cooperative yield: no set, no event. Re-enter at once with EVENT_NONE. There is nothing
			// to wait for, so a yield that parked would deadlock on a set the guest never named — and
			// `si` is not read here for exactly that reason (the model packs no index with YIELD).
			task.waitSet = 0
			res, err = f.enterAndInvoke(task, task.cb, eventArgs(event{code: eventNone}))
			if err != nil {
				return err
			}

		case callbackWait:
			// Park until set `si` has an event, then re-enter carrying it. The park is in Go on this
			// goroutine — the guest's agent was released when it returned WAIT, which is the whole
			// difference from waitableSetWait's blocking excursion.
			task.waitSet = si
			ev, aerr := f.h.awaitEvent(si, liftParkBound)
			if aerr != nil {
				return aerr
			}
			res, err = f.enterAndInvoke(task, task.cb, eventArgs(ev))
			if err != nil {
				return err
			}
		}
	}
}

// enterAndInvoke makes task current, invokes one core func, and restores the previous current task (#871).
//
// # Why the set/restore is here and not at the loop's edges
//
// A built-in must act on the task whose code is running, and in the stackless model that changes on every
// entry and every return. Pairing them in one function means **no entry can be added that forgets the
// restore** — the loop above cannot invoke a core func except through this.
//
// The restore is not deferred to the loop's exit, because between a WAIT return and the next re-entry the
// task's code is NOT running and `h.lift` must be nil: a `task.return` arriving then is a guest error, and
// leaving the slot populated would make it silently succeed against a parked task.
func (f *compFunc) enterAndInvoke(task *liftTask, target coreDef, args []interp.Value) ([]interp.Value, error) {
	if target.inst == nil {
		return nil, fmt.Errorf("%w: async lift has no invocable core func for this entry (callback unresolved)",
			ErrUnsupportedForm)
	}
	prev := f.h.enterTask(task)
	defer f.h.leaveTask(prev)
	return target.inst.Invoke(target.name, args...)
}

// eventArgs flattens an event into the callback's three i32 parameters — `__callback(ev0, ev1, ev2) -> u32`
// (definitions.py canon_lift's callback invocation). EVENT_NONE is the all-zero triple, which is what a
// YIELD re-entry carries.
func eventArgs(ev event) []interp.Value {
	return []interp.Value{
		interp.I32(int32(ev.code)),
		interp.I32(int32(ev.p1)),
		interp.I32(int32(ev.p2)),
	}
}

// compInstance is a component instance: its exports by name. A stub instance (a host-filled import the
// engine does not model) answers any export with a refusing func, so aliases into it resolve — the
// import's declared type is unmodeled this slice, so the stub is permissive.
type compInstance struct {
	exports map[string]compDef
	stub    bool
	name    string // the import name, for the refusal message
}

func (ci *compInstance) export(name string) (compDef, bool) {
	if cd, ok := ci.exports[name]; ok {
		return cd, true
	}
	if ci.stub {
		return compDef{fn: &compFunc{stubName: ci.name + "::" + name}}, true
	}
	return compDef{}, false
}

// compDef is a component index-space entry: a function or an instance.
type compDef struct {
	fn   *compFunc
	inst *compInstance
}

// host supplies a component's imports by name (the stub host for the outer component's wasi instances;
// the instantiation args for a nested component).
type host func(name string) (compDef, bool)

// stubHost fills every import with a permissive refusing instance (PR B): aliases into it resolve to
// refusing funcs, and the canon-lowered core funcs the modules actually import are refused at the core
// boundary. The real host is PR C.
func stubHost(name string) (compDef, bool) {
	return compDef{inst: &compInstance{stub: true, name: name}}, true
}

// walkComponent runs the whole walk (core spaces then component spaces) for a component with a given
// import host, and returns a walker holding the resolved spaces. A nested component recurses through
// this same function.
func (c *Component) walkComponent(h host, wasiHost map[string]interp.CanonFunc, asyncWasiHost map[string]asyncLowerImpl, streamConsumers map[string]streamConsumer) (*walker, error) {
	w := &walker{c: c, coreSpace: map[Space][]coreDef{}, host: h, wasiHost: wasiHost, asyncWasiHost: asyncWasiHost, streamConsumers: streamConsumers, async: newAsyncHandles()}
	for _, d := range c.Defs {
		if err := w.stepAll(d); err != nil {
			w.close()
			return nil, err
		}
	}
	return w, nil
}

// stepAll dispatches a definition to the core-space step (walk.go) or the component-space step.
func (w *walker) stepAll(d Def) error {
	switch d.Space {
	case SpaceCoreModule, SpaceCoreInstance, SpaceCoreFunc, SpaceCoreTable, SpaceCoreMemory, SpaceCoreGlobal:
		return w.step(d)
	case SpaceFunc:
		return w.funcStep(d)
	case SpaceInstance:
		return w.instanceStep(d)
	case SpaceComponent:
		return w.componentStep(d)
	default:
		return nil // value/type spaces are not resolved this slice
	}
}

// funcStep grows the component func space: a canon lift wraps a core func; a func import comes from the
// host; an alias export of a func projects from a component instance.
func (w *walker) funcStep(d Def) error {
	switch d.Section {
	case SectionCanon: // a lift (lowers land in the core space, handled by step)
		cn := w.c.Canons[d.Item]
		if int(cn.FuncIdx) >= len(w.coreSpace[SpaceCoreFunc]) {
			return fmt.Errorf("component: canon lift names core func %d of %d", cn.FuncIdx, len(w.coreSpace[SpaceCoreFunc]))
		}
		lf := &compFunc{core: w.coreSpace[SpaceCoreFunc][cn.FuncIdx], sig: w.liftSignature(cn)}
		if cn.Opts.Async && cn.Kind == CanonLift && cn.Opts.Callback != nil {
			// Stackless (callback) async lift: invoke() runs the loop over the durable lift task. A
			// no-callback (stackful) async lift is not bound here — it refuses as unbuilt
			// (unbuiltAsyncSurface).
			//
			// **The callback's core func is resolved here** (#871). Until this slice the canonopt was only
			// nil-checked, so the loop had nothing to re-enter and every WAIT refused by name. Resolved at
			// bind rather than at first park, because an out-of-range callback index is a malformed
			// component and that is a refusal the loop should never have to make mid-park.
			cbIdx := *cn.Opts.Callback
			if int(cbIdx) >= len(w.coreSpace[SpaceCoreFunc]) {
				return fmt.Errorf("component: canon lift names callback core func %d of %d",
					cbIdx, len(w.coreSpace[SpaceCoreFunc]))
			}
			lf.async = true
			lf.h = w.async
			lf.cb = w.coreSpace[SpaceCoreFunc][cbIdx]
		}
		w.compFuncs = append(w.compFuncs, compDef{fn: lf})
	case SectionImport:
		imp := w.c.Imports[d.Item]
		cd, ok := w.host(imp.Name)
		if !ok {
			return fmt.Errorf("%w: import %q", ErrLinkRefused, imp.Name)
		}
		// **A FUNCTION import needs a function-shaped def** (#870). `stubHost` answers every import with a
		// stub *instance*, so for a bare world-level function import — `(import "tick" (func …))`, which
		// `wasi:cli/run` worlds do not have but an async guest does — `cd.fn` was nil. The impl lookup in
		// `walk.go` is guarded on `fn != nil`, so it **never ran at all**: not for a provided import, and
		// not for an unprovided one either.
		//
		// The defect was the KIND, not the provided case, so it is fixed here where the sort is known
		// rather than by adding a second dispatch path. With a function-shaped stub the existing lookup
		// runs, keyed on the import's own name — and the refusal for an unprovided import now says `tick`
		// instead of `actual::0`, which is wit-component's index-shaped indirection and told a reader
		// nothing.
		//
		// Keyed on the bare name, with both premises checked against the spec at CANON_PIN: import
		// `externname`s are strongly-unique in a component (Binary.md), and a `plainname`'s charset is
		// alphanumerics and `-` only (Explainer.md), so no bare name can collide with an
		// `instance::export` key.
		if cd.fn == nil && cd.inst != nil && cd.inst.stub {
			cd = compDef{fn: &compFunc{stubName: imp.Name}}
		}
		w.compFuncs = append(w.compFuncs, cd)
	case SectionAlias:
		cd, err := w.aliasExport(w.c.Aliases[d.Item])
		if err != nil {
			return err
		}
		w.compFuncs = append(w.compFuncs, cd)
	default:
		// no other section contributes to the func space
	}
	return nil
}

// instanceStep grows the component instance space: a component instance (recursive instantiate, or
// inline-export projection), an instance import (from the host), or an alias export of an instance.
func (w *walker) instanceStep(d Def) error {
	switch d.Section {
	case SectionInstance:
		in := w.c.Instances[d.Item]
		ci, err := w.componentInstance(in)
		if err != nil {
			return err
		}
		w.compInstances = append(w.compInstances, ci)
	case SectionImport:
		cd, ok := w.host(w.c.Imports[d.Item].Name)
		if !ok {
			return fmt.Errorf("%w: import %q", ErrLinkRefused, w.c.Imports[d.Item].Name)
		}
		w.compInstances = append(w.compInstances, cd)
	case SectionAlias:
		cd, err := w.aliasExport(w.c.Aliases[d.Item])
		if err != nil {
			return err
		}
		w.compInstances = append(w.compInstances, cd)
	default:
		// no other section contributes to the instance space
	}
	return nil
}

// componentStep records a nested component's parsed form (Load'd from its stored bytes) for a later
// instantiate to recurse into.
func (w *walker) componentStep(d Def) error {
	nc, err := Load(w.c.NestedComponents[d.Item])
	if err != nil {
		return fmt.Errorf("component: nested component %d: %w", d.Item, err)
	}
	w.nested = append(w.nested, nc)
	return nil
}

// componentInstance builds a component instance: an inline-export projection, or a recursive
// instantiation of a nested component whose imports are supplied by the args.
func (w *walker) componentInstance(in Instance) (compDef, error) {
	if !in.Instantiate {
		exports := make(map[string]compDef, len(in.InlineExports))
		for _, e := range in.InlineExports {
			cd, err := w.compExportRef(e.Sort, e.Idx)
			if err != nil {
				return compDef{}, err
			}
			exports[e.Name] = cd
		}
		return compDef{inst: &compInstance{exports: exports}}, nil
	}
	if int(in.ComponentIdx) >= len(w.nested) {
		return compDef{}, fmt.Errorf("component: instance names component %d of %d", in.ComponentIdx, len(w.nested))
	}
	// Recurse: the nested component's imports are supplied by the args (name -> the outer def).
	argHost := func(name string) (compDef, bool) {
		for _, a := range in.Args {
			if a.Name == name {
				return w.compArgRef(a)
			}
		}
		return compDef{}, false
	}
	nw, err := w.nested[in.ComponentIdx].walkComponent(argHost, w.wasiHost, w.asyncWasiHost, w.streamConsumers)
	if err != nil {
		return compDef{}, err
	}
	w.toClose = append(w.toClose, nw.toClose...)
	return compDef{inst: nw.exportInstance()}, nil
}

// compArgRef resolves a component instantiate arg to the outer def it names.
func (w *walker) compArgRef(a InstantiateArg) (compDef, bool) {
	switch a.Sort {
	case SortFunc:
		if int(a.Idx) < len(w.compFuncs) {
			return w.compFuncs[a.Idx], true
		}
	case SortInstance:
		if int(a.Idx) < len(w.compInstances) {
			return w.compInstances[a.Idx], true
		}
	default:
	}
	return compDef{}, false
}

// compExportRef resolves an inline export's sortidx to the component def it projects.
func (w *walker) compExportRef(s Sort, idx uint32) (compDef, error) {
	switch s {
	case SortFunc:
		if int(idx) < len(w.compFuncs) {
			return w.compFuncs[idx], nil
		}
	case SortInstance:
		if int(idx) < len(w.compInstances) {
			return w.compInstances[idx], nil
		}
	default:
	}
	return compDef{}, fmt.Errorf("%w: inline export of %s %d", ErrUnsupportedForm, s, idx)
}

// aliasExport resolves an export alias (of a func or instance) to the def it names in the target
// instance's exports.
func (w *walker) aliasExport(a Alias) (compDef, error) {
	if a.Kind != AliasExport {
		return compDef{}, fmt.Errorf("%w: alias kind %d in component space", ErrUnsupportedForm, a.Kind)
	}
	if int(a.InstanceIdx) >= len(w.compInstances) {
		return compDef{}, fmt.Errorf("component: alias export names instance %d of %d", a.InstanceIdx, len(w.compInstances))
	}
	inst := w.compInstances[a.InstanceIdx].inst
	if inst == nil {
		return compDef{}, fmt.Errorf("%w: alias export from a non-instance", ErrUnsupportedForm)
	}
	cd, ok := inst.export(a.Name)
	if !ok {
		return compDef{}, fmt.Errorf("%w: alias export %q not found", ErrLinkRefused, a.Name)
	}
	return cd, nil
}

// exportInstance projects the component's top-level exports into a compInstance (name -> def).
func (w *walker) exportInstance() *compInstance {
	exports := make(map[string]compDef, len(w.c.Exports))
	for i, e := range w.c.Exports {
		cd, ok := w.exportRef(e, i)
		if ok {
			exports[e.Name] = cd
		}
	}
	return &compInstance{exports: exports}
}

// exportRef resolves a top-level export's sortidx to the def it names. The loader's parseExports
// discards the index (it reported only the kind), so this resolves by the export's position among
// same-sort exports — sufficient for the single func/instance export shapes this slice reaches.
func (w *walker) exportRef(e Export, _ int) (compDef, bool) {
	// This slice resolves an export naming a func or an instance by scanning for the matching sort in
	// order; p3hello's `run` export is the sole component-func-or-instance export.
	switch e.Kind {
	case SortFunc:
		if len(w.compFuncs) > 0 {
			return w.compFuncs[len(w.compFuncs)-1], true
		}
	case SortInstance:
		if len(w.compInstances) > 0 {
			return w.compInstances[len(w.compInstances)-1], true
		}
	default:
	}
	return compDef{}, false
}

// ErrNoRun reports a component with no callable run export.
var ErrNoRun = errors.New("component: no callable run export")

// Instantiated is an instantiated component. Call invokes a component-level export (run() this slice).
type Instantiated struct {
	w      *walker
	export *compInstance
}

// asyncGateEnv is the config gate for the async component-model ABI (`gate:async`, ADR 0086). Off by
// default — set to "1" to opt in — the second of the two component gates (`gate:components` on by default
// is the first). The async tier's mechanism is slice 1's; this shell only recognizes the async surface at
// decode and refuses it by name at bind while the gate is off.
const asyncGateEnv = "BURROUGHS_ASYNC"

// asyncEnabled reports whether `gate:async` is on. **On is the default as of the 2026-09-18 flip** (ADR
// 0086's amendment, forecast and stamp on #792; behaviour 4's stamp-tier event). The gate remains present as
// the rollback: an explicit `BURROUGHS_ASYNC=0` refuses an async component by name through the same path —
// only the default differs. The form matches `componentsEnabled` (the 2026-09-11 gate:components flip), so
// both gates read their opt-out the same way rather than each inventing one.
func asyncEnabled() bool { return os.Getenv(asyncGateEnv) != "0" }

// ErrAsyncGated is returned at bind when a component uses the async component-model ABI and `gate:async`
// is off. The root `ComponentConfig.Run` wraps it as `burroughs.ErrGated` so the CLI classifies it exit 6
// (the module is well-formed; this build has the gate off — grave #301), the same shape `gate:components`
// has. Kept in this package because the refusal fires at bind (`InstantiateWithHost`), reachable by an
// internal caller directly, not only through the root entry.
var ErrAsyncGated = errors.New("gate:async is off in this build")

// gateAsync refuses a component that carries async surface, by name, naming what tripped it (ADR 0086).
// The condition keys on the `async` canonopt in any canon lift or lower — the thing the async ABI turns on
// — read from the canon section, not on a world name or a 0.3 import. Because the first guest is
// sync-lifted with async-lowered imports (#734), the lower arm is covered as much as the lift; an async
// canon built-in, an async functype, and a stream/future value type are refused too (defense-in-depth
// against the same permissiveness hole).
//
// **Two refusals, one detector, so gate-on is never a silent no-op** (#739 slice 1). Gate **off** →
// `ErrAsyncGated`, the #738 refusal ("set BURROUGHS_ASYNC=1"). Gate **on** → `ErrAsyncNotImplemented`: the
// async tier's execution is being built slice by slice, and until it lands an async component refuses **by
// name here** rather than instantiating successfully and trapping obscurely at run on a missing async
// runtime import (the nil no-op Scott's rule forbids — an unimplemented sub-path refuses by name, never
// appears to succeed). As increments land, this gate-on refusal narrows to the sub-paths still unbuilt and
// is finally lifted when the guest runs (increment 4).
func gateAsync(c *Component) error {
	what, ok := asyncSurface(c)
	if !ok {
		return nil // no async surface — sync components (gate:components) are unaffected
	}
	if !asyncEnabled() {
		return fmt.Errorf("%w: this component uses the async component-model ABI (%s); unset %s to run it "+
			"(the gate is on by default as of the 2026-09-18 flip)", ErrAsyncGated, what, asyncGateEnv)
	}
	// Gate ON: the async tier lands incrementally, so this refusal NARROWS to the sub-paths still unbuilt
	// (#739). 2a-i-A executes the async **lower**'s sync-resolving arm, so an async-lower-only component is
	// permitted through to the walk (its binding drives the adapter, and the blocking arm refuses by name
	// at runtime). Everything the arm does not execute — an async **lift**, an async canon **built-in**,
	// a stream/future value type — still refuses **by name here**, so the narrowing is not a #732 no-op:
	// the refusal for the unbuilt rest fires (witnessed on a blocking-arm/lift/built-in case), not merely
	// the sync-resolving lower passing.
	if unbuilt, ok := unbuiltAsyncSurface(c); ok {
		return fmt.Errorf("%w: this component uses %s, and gate:async is on, but that async surface's "+
			"execution is not yet implemented — it lands incrementally (#739 slice 1)",
			ErrAsyncNotImplemented, unbuilt)
	}
	return nil // only async lowers (2a-i-A's built surface) — permit; the walk binds the adapter
}

// unbuiltAsyncSurface reports the first async marker gate:async slice-1 2a-i-A does NOT execute: an async
// **lift** (the export lift is deferred), an async canon **built-in** (the waitable-set loop is 2b), or a
// **stream/future** value type (unmodeled by the codec). It deliberately does NOT report an async **lower**
// (2a-i-A executes its sync-resolving arm) or an async **functype** (that is the *type* of a lower/lift —
// refusing it would refuse a permitted async-lower-only component). A lower whose result carries a
// stream/future is caught by the value-type check, not the lower itself.
func unbuiltAsyncSurface(c *Component) (string, bool) {
	for _, cn := range c.Canons {
		// The STACKLESS (callback) async lift is built (2nd async guest, #785): invoke() runs the callback
		// loop, so a lift WITH a callback is permitted (a guest whose loop parks reaches the step-2 not-yet at
		// invoke, not at bind). The STACKFUL arm — an async lift with NO callback — stays deferred (ADR 0086's
		// guest-driven choice: no guest lifts stackfully), so it still refuses as unbuilt here.
		if cn.Kind == CanonLift && cn.Opts.Async && cn.Opts.Callback == nil {
			return "an async canon lift without a callback (the stackful arm)", true
		}
		if cn.Kind == CanonAsyncBuiltin && !isBuiltAsyncBuiltin(cn.AsyncOp) {
			return fmt.Sprintf("an async canon built-in (%#x)", cn.AsyncOp), true
		}
	}
	// Neither a future nor a stream value type is refused here anymore: future.read is built (increment 3)
	// and stream.write is built (increment 3, write side), and both future and stream handles are direction
	// -agnostic value types. The unbuilt *operations* on them — future.write/new/cancel-read, stream.read/
	// new/cancel/drop — refuse at the built-in check above (isBuiltAsyncBuiltin), and an async lift refuses
	// there too. A value type using an unbuilt op is caught where the op is, not at the type.
	return "", false
}

// isBuiltAsyncBuiltin reports whether an async canon built-in opcode is one this engine executes.
//
// **The authoritative list is not this comment.** It is `testdata/canon-builtins.tsv` — every canon
// production in Binary.md at CANON_PIN — paired with the `canonStatus` classification beside the witness in
// canonbuiltins_test.go, which asserts those two and this switch agree **in both directions**: an opcode
// added here with nothing reclassified fails, and so does a status claiming one this switch does not permit
// (ADR 0092).
//
// It is written that way because of grave #850: the prose list that used to stand here had drifted from the
// switch below and was twice read as the engine's actual coverage. It said waitable-set.poll (0x21) "stays
// refused by name" while 0x21 sat in the case list, and it named none of 0x09/0x15/0x17/0x19 among the built.
// A comment duplicating a property the code already carries is correct exactly once; what a reader needs is
// which instrument to ask, so that is what this says.
//
// The shape, which does not drift: the waitable-set loop, the stream and future operations the earlier
// increments built, the subtask ops, task.return, and the context slots. What stays refused is the rest of
// the 🔀 family — including stream.read, where the guest writes and the host reads over an internal path,
// and task.cancel (0x05), which the committed suspending guest DOES import and is therefore slice 2's engine
// work rather than a deferral waiting for a consumer.
//
// # This predicate answers BOUND, not EXECUTES, and the two were conflated (#871)
//
// Every op listed here is *bound* to something. Until this slice one of them — `waitable-set.drop` (0x22)
// — was bound to a **call-time refusal**, so it was listed here while the engine did not execute it. The
// 47-row classification's `claimsBuilt()` is compared against this predicate and its status means *"this
// engine executes"*, so the table asserted something false about 0x22 and stayed green, because the
// comparator cannot see the difference between bound-to-an-impl and bound-to-a-refusal.
//
// 0x22 now has a real impl, and nothing else is bound to a refusal (`refuseAtCall` was deleted for want of
// callers), so no row is currently wrong. **A future actor binding another call-time refusal re-opens
// it**, and the fix then is not to add a row here but to give the classification a third claim —
// bound-but-refusing — distinct from built and from refused-at-bind.
func isBuiltAsyncBuiltin(op byte) bool {
	switch op {
	case 0x1f, 0x20, 0x21, 0x22, 0x23, 0x16, 0x1a, 0x10, 0x0e, 0x11, 0x12, 0x13, 0x14, 0x05, 0x06, 0x0d, 0x0a, 0x0b, 0x09, 0x15, 0x17, 0x19:
		return true
	}
	return false
}

// ErrAsyncNotImplemented is returned at instantiate when gate:async is ON but the async tier's execution
// is not yet built (slice 1 is landing incrementally, #739). It is distinct from ErrAsyncGated (the gate
// being off): the gate is open, the mechanism is not there yet — so it refuses by name rather than
// instantiating and failing obscurely at run. Not wrapped as ErrGated: a gate that is on but unbuilt is
// an unsupported operation, not a gate decline (grave #301's classification is for a well-formed module a
// gate turns away, which is the gate-off case).
var ErrAsyncNotImplemented = errors.New("component: gate:async is on but the async tier's execution is not yet implemented")

// asyncSurface reports the first async component-model marker in the component and a name for it, in the
// refusal's priority order: the `async` canonopt (the gate's key) first, then an async canon built-in, an
// async functype, and a stream/future value type anywhere in a type.
func asyncSurface(c *Component) (string, bool) {
	for _, cn := range c.Canons {
		if cn.Opts.Async {
			if cn.Kind == CanonLift {
				return "an async canon lift", true
			}
			return "an async canon lower", true
		}
	}
	for _, cn := range c.Canons {
		if cn.Kind == CanonAsyncBuiltin {
			return fmt.Sprintf("an async canon built-in (%#x)", cn.AsyncOp), true
		}
	}
	for _, td := range c.Types {
		switch td.Kind {
		case TDFunc:
			if td.Func != nil && td.Func.Async {
				return "an async function type", true
			}
		case TDVal:
			if n, ok := asyncValName(td.Val, 0); ok {
				return "a " + n + " value type", true
			}
		default:
			// TDInstance/TDResource carry no top-level async value type of their own.
		}
	}
	return "", false
}

// asyncValName reports a stream/future value type anywhere in vt (recursing the modeled containers), for
// the gate refusal. error-context is not reported here — it is a separate 📝 feature already refused as an
// unmodeled kind at marshal, not part of the 🔀 async gate.
func asyncValName(vt ValType, depth int) (string, bool) {
	if depth > 32 {
		return "", false
	}
	switch vt.Kind {
	case VStream:
		return "stream", true
	case VFuture:
		return "future", true
	case VList, VOption:
		if vt.Elem != nil {
			return asyncValName(*vt.Elem, depth+1)
		}
	case VResult:
		if vt.Ok != nil {
			if n, ok := asyncValName(*vt.Ok, depth+1); ok {
				return n, true
			}
		}
		if vt.Err != nil {
			return asyncValName(*vt.Err, depth+1)
		}
	case VVariant:
		for _, ca := range vt.Cases {
			if ca.Type != nil {
				if n, ok := asyncValName(*ca.Type, depth+1); ok {
					return n, true
				}
			}
		}
	case VTuple:
		for _, e := range vt.Elems {
			if n, ok := asyncValName(e, depth+1); ok {
				return n, true
			}
		}
	default:
		// scalars, string, char, own/borrow, error-context, ref, record/flags/enum — not stream/future.
	}
	return "", false
}

// Instantiate loads and instantiates a component with the stub host (PR B): its core modules come up,
// its wasi imports reach the refusing stub, and its exports are resolved. Close tears it down.
func Instantiate(bytes []byte) (*Instantiated, error) {
	c, err := Load(bytes)
	if err != nil {
		return nil, err
	}
	if gerr := gateAsync(c); gerr != nil {
		return nil, gerr
	}
	w, err := c.walkComponent(stubHost, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	return &Instantiated{w: w, export: w.exportInstance()}, nil
}

// InstantiateWithHost loads and instantiates a component against a real preview-2 host (PR C.2): the
// wasi imports the guest-driven world reaches are marshaled to the host's impls, and any it does not
// reach still meet the refusing stub. The component-import host stays the stub (it supplies the named
// import instances the aliases project through); the host's impls are consulted at the core boundary
// where the canon-lowered funcs are filled.
func InstantiateWithHost(bytes []byte, h *Host) (*Instantiated, error) {
	return InstantiateWithHostFeatures(bytes, h, bin.DefaultFeatures())
}

// InstantiateWithHostFeatures is InstantiateWithHost with the caller's feature set for the
// component's core modules (ADR 0088).
func InstantiateWithHostFeatures(bytes []byte, h *Host, feats bin.Features) (*Instantiated, error) {
	c, err := LoadWithFeatures(bytes, feats)
	if err != nil {
		return nil, err
	}
	if gerr := gateAsync(c); gerr != nil {
		return nil, gerr
	}
	w, err := c.walkComponent(stubHost, h.wasi(), h.asyncWasi(), h.streamConsumers())
	if err != nil {
		return nil, err
	}
	return &Instantiated{w: w, export: w.exportInstance()}, nil
}

// Call invokes a component export by name. For run() (no params, empty result) this drives the guest;
// with the stub host, reaching a wasi import returns the refusal. Value-carrying exports are a later
// slice (run moves none).
func (in *Instantiated) Call(name string) error {
	cd, ok := in.export.exports[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoRun, name)
	}
	fn := cd.fn
	// A wasi world export is an interface *instance* (e.g. wasi:cli/run) whose sole function is `run`;
	// reach it.
	if fn == nil && cd.inst != nil {
		if rd, ok := cd.inst.export("run"); ok {
			fn = rd.fn
		}
	}
	if fn == nil {
		return fmt.Errorf("%w: export %q is not a callable function", ErrUnsupportedForm, name)
	}
	// The export refusal (ADR 0084 / #720): an exported function whose signature carries a value kind the
	// codec cannot marshal refuses **by name at Call** — the earliest point a value crosses. `run` carries
	// no values, so it never trips; a value-taking export does. Refused here, not at LoadComponent or
	// instantiate, for the same reason decode stays permissive: the boundary is where the value moves.
	if fn.sig != nil {
		if k, bad := unmodeledInSig(fn.sig); bad {
			return fmt.Errorf("%w: export %q carries %s, which this engine's Canonical ABI does not model",
				ErrUnsupportedForm, name, valKindName(k))
		}
	}
	return fn.invoke()
}

// liftSignature resolves a canon lift's type index to the component function type it lifts, for the
// export refusal. A lift names its type (`ft:typeidx`, unlike a lower); nil when the index is out of
// range or names a non-function type.
func (w *walker) liftSignature(cn Canon) *FuncType {
	if int(cn.TypeIdx) >= len(w.c.Types) {
		return nil
	}
	td := w.c.Types[cn.TypeIdx]
	if td.Kind != TDFunc {
		return nil
	}
	return td.Func
}

// CallRun invokes the component's `wasi:cli/run` export, whatever version it names
// (`wasi:cli/run@0.2.3`, `@0.2.0`, …), so a caller need not know the world's exact version. There is
// one such export in a `wasi:cli/run` world; if none, ErrNoRun.
func (in *Instantiated) CallRun() error {
	for name := range in.export.exports {
		if strings.HasPrefix(name, "wasi:cli/run") {
			return in.Call(name)
		}
	}
	return fmt.Errorf("%w: no wasi:cli/run export", ErrNoRun)
}

// Close tears down the instantiated core instances.
func (in *Instantiated) Close() { in.w.close() }
