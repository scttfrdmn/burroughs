// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The instantiation walk (ADR 0084's instantiation clause, PR B.2). It walks the definition stream in
// index order, growing the resolved index spaces, so each reference resolves against exactly the
// definitions that precede it. A real core module is instantiated through the core interpreter with a
// resolver that supplies its imports from earlier instances' exports (by reference) and canon-lowered
// funcs (a refusing stub host, typed from the importing module's own declaration). A synthetic
// inline-export instance is a projection over already-defined entries and does no interpreter work.

// ErrUnsupportedForm reports an instantiation form the engine does not model (refuse-at-instantiate by
// name).
var ErrUnsupportedForm = errors.New("component: unsupported instantiation form")

// coreDef is a resolved core index-space entry: a real extern (a core instance's export, or an aliased
// core export — carried by reference), or a canon-lowered stub whose core type is deferred to the
// module that imports it (materialized in the resolver from that module's declaration, since B.2 does
// not model component function types).
type coreDef struct {
	extern interp.Extern
	stub   bool
	// inst and name let a canon lift invoke an aliased core func through its owning instance
	// (interp exposes invocation only by export name, so the alias's name is retained).
	inst *interp.Instance
	name string
}

func (d coreDef) isStub() bool { return d.stub }

// coreInstance resolves a core instance's exports to core defs by name.
type coreInstance interface {
	export(name string) (coreDef, bool)
}

// realInst wraps an instantiated core module; its exports are carried by reference (Instance.Export).
type realInst struct{ in *interp.Instance }

func (r realInst) export(n string) (coreDef, bool) {
	e, ok := r.in.Export(n)
	if !ok {
		return coreDef{}, false
	}
	return coreDef{extern: e, inst: r.in, name: n}, true
}

// synthInst is an inline-export instance: a projection over already-defined core entries, no
// interpreter work.
type synthInst struct{ exports map[string]coreDef }

func (s synthInst) export(n string) (coreDef, bool) {
	d, ok := s.exports[n]
	return d, ok
}

// walker holds the resolved index spaces as the walk proceeds. Only the spaces the engine resolves this
// slice are kept; the type spaces are not modeled (a canon lift's type is not resolved).
type walker struct {
	c             *Component
	coreInstances []coreInstance
	// coreSpace holds the core index spaces the walk resolves — func (canon lowers/resources as stubs,
	// aliased core funcs), and memory/global/table (aliased core exports, by reference). Each grows in
	// stream order, so an inline export or module import resolves against earlier definitions.
	coreSpace map[Space][]coreDef
	toClose   []*interp.Instance
	// Component-space resolutions (link_component.go): component funcs (canon lifts, func imports,
	// aliased exports), component instances (recursive instantiations, instance imports, aliased
	// exports), and Load'd nested components. host supplies this component's imports.
	compFuncs     []compDef
	compInstances []compDef
	nested        []*Component
	host          host
}

func (w *walker) appendCore(s Space, d coreDef) { w.coreSpace[s] = append(w.coreSpace[s], d) }

// instantiate walks the definition stream and instantiates the component's real core modules. It
// returns the walker (holding the instantiated instances) or refuses by name at the first form it does
// not model. Canon lift, the component instance, and run-reachability are the next increment; this one
// brings p3hello's four core modules up with their imports resolved (by-reference exports; a refusing
// stub host for the canon-lowered wasi funcs).
func (c *Component) instantiate() (*walker, error) {
	w := &walker{c: c, coreSpace: map[Space][]coreDef{}}
	for _, d := range c.Defs {
		if err := w.step(d); err != nil {
			w.close()
			return nil, err
		}
	}
	return w, nil
}

func (w *walker) step(d Def) error {
	switch d.Section {
	case SectionCoreInstance:
		return w.coreInstanceStep(w.c.CoreInstances[d.Item])
	case SectionCanon:
		if d.Space == SpaceCoreFunc {
			// lower and the resource built-ins add a core func the engine fills with a refusing stub;
			// their real semantics (value marshaling, resource discipline) are B.3.
			w.appendCore(SpaceCoreFunc, coreDef{stub: true})
		}
		return nil
	case SectionAlias:
		return w.aliasStep(w.c.Aliases[d.Item])
	default:
		// core:module, import, and the SpaceFunc canon lifts / component instance are not resolved by
		// this increment's walk (they are the next one's); they add no core instance to bring up.
		return nil
	}
}

// coreInstanceStep brings up one core instance: a real module instantiation, or a synthetic
// inline-export projection.
func (w *walker) coreInstanceStep(ci CoreInstance) error {
	if !ci.Instantiate {
		exports := make(map[string]coreDef, len(ci.InlineExports))
		for _, e := range ci.InlineExports {
			d, err := w.coreExportRef(e.Sort, e.Idx)
			if err != nil {
				return err
			}
			exports[e.Name] = d
		}
		w.coreInstances = append(w.coreInstances, synthInst{exports: exports})
		return nil
	}
	if int(ci.ModuleIdx) >= len(w.c.CoreModules) {
		return fmt.Errorf("%w: core instance names module %d of %d", ErrUnsupportedForm, ci.ModuleIdx, len(w.c.CoreModules))
	}
	m := w.c.CoreModules[ci.ModuleIdx]
	in, trap, err := interp.InstantiateLinked(m, w.resolverFor(m, ci.Args))
	if err != nil {
		return fmt.Errorf("component: instantiate core module %d: %w", ci.ModuleIdx, err)
	}
	if trap != nil {
		return fmt.Errorf("component: instantiate core module %d trapped: %w", ci.ModuleIdx, trap)
	}
	w.toClose = append(w.toClose, in)
	w.coreInstances = append(w.coreInstances, realInst{in: in})
	return nil
}

// coreExportRef resolves an inline export's core:sortidx to a core def against the matching core space
// (func/memory/global/table), which earlier aliases and canons populated in stream order.
func (w *walker) coreExportRef(s CoreSort, idx uint32) (coreDef, error) {
	space := coreSortSpace(s)
	entries := w.coreSpace[space]
	if int(idx) >= len(entries) {
		return coreDef{}, fmt.Errorf("component: inline export names %s %d of %d", spaceName(space), idx, len(entries))
	}
	return entries[idx], nil
}

// aliasStep resolves a core-export alias (of any core sort) into the matching core space, by reference.
// Component-space aliases feed spaces this walk does not build yet (the next increment's).
func (w *walker) aliasStep(a Alias) error {
	if a.Kind != AliasCoreExport {
		return nil // component-space aliases are the next increment's
	}
	if int(a.InstanceIdx) >= len(w.coreInstances) {
		return fmt.Errorf("component: alias core-export names instance %d of %d", a.InstanceIdx, len(w.coreInstances))
	}
	d, ok := w.coreInstances[a.InstanceIdx].export(a.Name)
	if !ok {
		return fmt.Errorf("%w: alias core-export %q not found in instance %d", ErrLinkRefused, a.Name, a.InstanceIdx)
	}
	w.appendCore(coreSortSpace(a.CoreSort), d)
	return nil
}

// ErrLinkRefused reports an import or alias that names nothing the engine can supply.
var ErrLinkRefused = errors.New("component: link refused")

// resolverFor builds the interp resolver for a real core module: it maps the module's import
// module-string through the instantiation args to the supplying core instance, and returns that
// instance's export by reference. A canon-lowered func import is materialized as a HostExtern whose
// type is the module's own import declaration — refusing by name if called (the stub host).
func (w *walker) resolverFor(m *bin.Module, args []CoreInstantiateArg) interp.Imports {
	argMap := make(map[string]uint32, len(args))
	for _, a := range args {
		argMap[a.Name] = a.InstanceIdx
	}
	return func(mod, name string) (interp.Extern, bool) {
		ci, ok := argMap[mod]
		if !ok || int(ci) >= len(w.coreInstances) {
			return interp.Extern{}, false
		}
		d, ok := w.coreInstances[ci].export(name)
		if !ok {
			return interp.Extern{}, false
		}
		if !d.isStub() {
			return d.extern, true // a real export, by reference (memory/global/func sharing)
		}
		ft, ok := funcImportType(m, mod, name)
		if !ok {
			return interp.Extern{}, false
		}
		return interp.HostExtern(ft, refuse(mod, name)), true
	}
}

// funcImportType finds a module's declared type for a function import.
func funcImportType(m *bin.Module, mod, name string) (bin.FuncType, bool) {
	for i := range m.Imports {
		imp := &m.Imports[i]
		if imp.Module == mod && imp.Name == name && imp.Kind == bin.ExternFunc {
			if int(imp.Index) < len(m.Types) {
				return m.Types[imp.Index].Func, true
			}
		}
	}
	return bin.FuncType{}, false
}

// refuse is the stub host function: called, it refuses by name — the "imports reach a stub host that
// refuses" boundary (PR B). The real host is PR C.
func refuse(mod, name string) interp.HostFunc {
	return func(_ *interp.Caller, _ []interp.Value) ([]interp.Value, error) {
		return nil, fmt.Errorf("%w: import %s::%s is not provided (stub host)", ErrLinkRefused, mod, name)
	}
}

// close tears down every instantiated core instance.
func (w *walker) close() {
	for _, in := range w.toClose {
		_ = in.Close()
	}
}
