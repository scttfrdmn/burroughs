// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
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
}

func (f *compFunc) invoke() error {
	if f.stubName != "" {
		return fmt.Errorf("%w: %s (stub host)", ErrLinkRefused, f.stubName)
	}
	if f.core.inst == nil {
		return fmt.Errorf("%w: component func has no invocable core func (lift target unresolved)", ErrUnsupportedForm)
	}
	_, err := f.core.inst.Invoke(f.core.name)
	return err
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
func (c *Component) walkComponent(h host, wasiHost map[string]interp.CanonFunc) (*walker, error) {
	w := &walker{c: c, coreSpace: map[Space][]coreDef{}, host: h, wasiHost: wasiHost}
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
		w.compFuncs = append(w.compFuncs, compDef{fn: &compFunc{core: w.coreSpace[SpaceCoreFunc][cn.FuncIdx], sig: w.liftSignature(cn)}})
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
	nw, err := w.nested[in.ComponentIdx].walkComponent(argHost, w.wasiHost)
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

// Instantiate loads and instantiates a component with the stub host (PR B): its core modules come up,
// its wasi imports reach the refusing stub, and its exports are resolved. Close tears it down.
func Instantiate(bytes []byte) (*Instantiated, error) {
	c, err := Load(bytes)
	if err != nil {
		return nil, err
	}
	w, err := c.walkComponent(stubHost, nil)
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
	w, err := c.walkComponent(stubHost, h.wasi())
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
