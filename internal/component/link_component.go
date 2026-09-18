// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
	// `h` holds the durable lift task. Step 1 is the loop skeleton (first-call -> EXIT -> resolve); the
	// callback core func for the re-entry/park is threaded in step 2.
	async  bool
	h      *asyncHandles
	result []interp.Value // the async lift's last resolution (what task.return lowered), for the caller/oracle
}

func (f *compFunc) invoke() error {
	if f.stubName != "" {
		return fmt.Errorf("%w: %s (stub host)", ErrLinkRefused, f.stubName)
	}
	if f.core.inst == nil {
		return fmt.Errorf("%w: component func has no invocable core func (lift target unresolved)", ErrUnsupportedForm)
	}
	if f.async {
		return f.invokeAsyncLift()
	}
	_, err := f.core.inst.Invoke(f.core.name)
	return err
}

// invokeAsyncLift runs the stackless (callback) async-lift loop (canon_lift def:2126-2151). Step 1 is the
// loop skeleton: create the durable lift task, invoke the callee ONCE with the lifted params, decode the
// packed return, and on EXIT confirm the task was resolved by task.return. The WAIT/YIELD re-entry and park
// are step 2. The first-call path is explicit here — a shared helper would blur the callee (params) and the
// callback (event), which #788's multi-cycle pin exists to catch.
func (f *compFunc) invokeAsyncLift() error {
	// At-most-one lift task per agent, asserted: an unbound host call into an async-lifted export is a shape
	// the engine permits, so a nested entry onto an agent already hosting a lift traps by name rather than
	// resolving the wrong task (witnessed on synth bytes, #732).
	task := &liftTask{}
	f.h.mu.Lock()
	if f.h.lift != nil {
		f.h.mu.Unlock()
		return &interp.Trap{Reason: "async canon lift entered while another lift task is already in flight on this agent"}
	}
	f.h.lift = task
	f.h.mu.Unlock()
	// Teardown keys on the task, not the loop: clear the current-task pointer however invoke exits
	// (normal EXIT, a trap, or — step 3 — cancellation resolving without a normal EXIT).
	defer func() {
		f.h.mu.Lock()
		f.h.lift = nil
		f.h.mu.Unlock()
	}()

	// First call: the callee, carrying the lifted params (none for run()). Its packed return drives dispatch.
	res, err := f.core.inst.Invoke(f.core.name)
	if err != nil {
		return err
	}
	if len(res) != 1 {
		return fmt.Errorf("%w: async lift callee returned %d core values, want 1 (packed)", ErrUnsupportedForm, len(res))
	}
	code, _, err := unpackCallbackResult(uint32(res[0].Bits))
	if err != nil {
		return err
	}
	switch code {
	case callbackExit:
		f.h.mu.Lock()
		resolved := f.h.lift.resolved
		f.h.mu.Unlock()
		if !resolved {
			return &interp.Trap{Reason: "async lift returned EXIT without resolving its task via task.return"}
		}
		f.result = task.result // the resolution, for the caller/oracle to read
		return nil
	default:
		// WAIT / YIELD: the callback re-entry and the park — step 2, not the EXIT-only skeleton.
		return fmt.Errorf("%w: async-lift dispatch code %d (park/yield) is step 2, not yet built", ErrAsyncNotImplemented, uint32(code))
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
			// Stackless (callback) async lift: invoke() runs the loop over the durable lift task. Step 1 is
			// the loop skeleton; the callback core func for re-entry is threaded in step 2. A no-callback
			// (stackful) async lift is not bound here — it refuses as unbuilt (unbuiltAsyncSurface).
			lf.async = true
			lf.h = w.async
		}
		w.compFuncs = append(w.compFuncs, compDef{fn: lf})
	case SectionImport:
		cd, ok := w.host(w.c.Imports[d.Item].Name)
		if !ok {
			return fmt.Errorf("%w: import %q", ErrLinkRefused, w.c.Imports[d.Item].Name)
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

// asyncEnabled reports whether `gate:async` is on. Off is the default: an async component decodes but is
// refused at bind, by name.
func asyncEnabled() bool { return os.Getenv(asyncGateEnv) == "1" }

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
		return fmt.Errorf("%w: this component uses the async component-model ABI (%s); set %s=1 to opt in "+
			"(the async tier's mechanism is not yet implemented)", ErrAsyncGated, what, asyncGateEnv)
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

// isBuiltAsyncBuiltin reports whether an async canon built-in opcode is one this engine executes: the
// waitable-set loop (gate:async 2a-i-B-2) — waitable-set.new (0x1f), .wait (0x20), .drop (0x22),
// waitable.join (0x23); the future.read slice (increment 3) — future.read (0x16), future.drop-readable
// (0x1a); the stream write-side slice (increment 3) — stream.write (0x10); stream.new (0x0e), the stream
// drops — drop-readable (0x13), drop-writable (0x14) — and the stream cancels — cancel-read (0x11),
// cancel-write (0x12) — (increment 4); and context.get/set (increment 4, 0x0a/0x0b). Every other async
// built-in — waitable-set.poll (0x21), future.write/new/cancel-read, and stream.read (0x0f) — stays refused
// by name until a guest binds it; the subtask ops subtask.cancel (0x06) and subtask.drop (0x0d) are built
// (increment 4). stream.read stays refused: the guest writes, the host reads (an internal path).
func isBuiltAsyncBuiltin(op byte) bool {
	switch op {
	case 0x1f, 0x20, 0x21, 0x22, 0x23, 0x16, 0x1a, 0x10, 0x0e, 0x11, 0x12, 0x13, 0x14, 0x06, 0x0d, 0x0a, 0x0b, 0x09, 0x15, 0x17, 0x19:
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
	c, err := Load(bytes)
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
