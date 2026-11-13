// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// Host-defined resources (#1231, ADR 0117). A guest holds `own`/`borrow` handles to objects the host
// implements — `wasi:io`'s streams and errors today, `wasi:sockets`' `tcp-socket` and `wasi:filesystem`'s
// `descriptor` next. Each handle is an entry in the **holding instance's** handle table, which is the codec's
// exported `canon.HandleTable`, so production uses the table the reference fixtures test:
//
//   - a host import returning `own<T>` lowers it with `Add` (def:lower_own);
//   - a `self: borrow<T>` argument is lent for the call with `LiftBorrow` and released at return
//     (def:lift_borrow, def:Subtask.deliver_resolve);
//   - an `own<T>` argument transfers to the host with `LiftOwn` (def:lift_own);
//   - `canon resource.drop` of a host type removes the handle with `Drop`, trapping on an outstanding lend,
//     and runs the type's destructor for an own (def:canon_resource_drop).
//
// The table holds the reference's `rep`, a number; the Go object it stands for lives beside it in the same
// per-instance record, keyed by that rep. The reference's `ResourceType` is an object compared by identity
// (def:canon_resource_drop:"trap_if(h.rt is not rt)"), so a host type's `rt` is a number minted once per
// declared type, and two declarations never share one.
//
// # What this does not do (ADR 0117's out-of-scope list)
//
//   - Guest-defined resources: `resource.new` and `resource.rep` still bind the refusing stub, and a drop of a
//     type the host does not implement keeps `dropNoop`.
//   - One table for everything: the reference keeps resources and waitables in one per-instance table
//     (def:ComponentInstance), and this engine keeps `asyncHandles` separate, so a guest's resource handle
//     numbers come from their own index space. A guest cannot observe the split unless it relies on a
//     resource handle and a waitable never sharing a number, which nothing in the ABI promises either way.

// hostResource is one resource type the host implements: the number its handles carry in a table, its
// version-free WASI identity for messages, and its destructor.
type hostResource struct {
	rt   int
	name string
	// dtor runs once when the guest drops an own of this type. Nil means: close the object if it is an
	// io.Closer, and otherwise release nothing.
	dtor func(obj any) error
}

// hostResourceNext mints resource-type numbers. Atomic because a test can declare a type while another
// test's instance runs; the numbers carry identity only.
var hostResourceNext atomic.Int64

// newHostResource declares a host resource type with a number no other declaration has.
func newHostResource(name string, dtor func(obj any) error) *hostResource {
	return &hostResource{rt: int(hostResourceNext.Add(1)), name: name, dtor: dtor}
}

// The production resource types. **Distinct numbers are the repair for an RT clash**: `errorRT` was 0, and so
// was the `own<descriptor>` element `get-directories` declares, so the two types were one as far as a table
// could tell.
var (
	outputStreamResource = newHostResource("wasi:io/streams::output-stream", nil)
	inputStreamResource  = newHostResource("wasi:io/streams::input-stream", nil)
	errorResource        = newHostResource("wasi:io/error::error", nil)
	descriptorResource   = newHostResource("wasi:filesystem/types::descriptor", nil)
	// WASI 0.3's terminal types (ADR 0119). Nothing mints one: every `get-terminal-*` answers none.
	terminalInputResource  = newHostResource("wasi:cli/terminal-input::terminal-input", nil)
	terminalOutputResource = newHostResource("wasi:cli/terminal-output::terminal-output", nil)
)

// destroy runs the type's destructor over a dropped own's object.
func (t *hostResource) destroy(obj any) error {
	if t.dtor != nil {
		return t.dtor(obj)
	}
	if c, ok := obj.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// hostResources is one component instance's handles to host resources: the codec's table and the Go object
// behind each rep. It is created per component instance, in walkComponent beside `async`, so a nested
// instance has its own, and the host functions bound for that instance capture it.
//
// The mutex covers the table and the object map together. A handler runs **outside** it, since a blocking
// read must not hold every other agent of the instance off its streams; what keeps a borrowed object alive
// across the handler is the lend, which a concurrent drop refuses.
type hostResources struct {
	mu      sync.Mutex
	table   *canon.HandleTable
	objs    map[uint32]any
	nextRep uint32
	// types maps each RT this table has minted an own of to its type, so teardown can find the destructor of
	// an own the guest never dropped (closeAll).
	types map[int]*hostResource
}

func newHostResources() *hostResources {
	return &hostResources{table: canon.NewHandleTable(), objs: map[uint32]any{}, types: map[int]*hostResource{}}
}

// closeAll destroys every own the guest still holds, once, when its instance closes (#1252, ADR 0118): a
// socket or a file a guest never dropped is closed rather than left to the Go collector, which runs no
// destructor. Each handle leaves the table under the lock and its destructor runs outside it, as drop's
// does, so a second closeAll finds nothing and destroys nothing more.
//
// **An own lent to a call still in progress is left alone**, since its object is in use. walker.close calls
// this after the instance's core instances have closed, and `interp.Instance.Close` waits for the host calls
// in flight, so a lend outstanding here is one whose host function did not return within that wait. Its
// object is not destroyed, which leaks it, and that is preferred to destroying it under the call.
//
// The reference has no instance teardown to follow. wasmtime drops a store's host resources with the store.
//
// **A destructor's error is dropped**, because it has no reader: Close returns none, and a guest that could
// be told has already gone. wasmtime's teardown is a Rust `Drop`, which cannot fail either.
func (r *hostResources) closeAll() {
	type doomed struct {
		t   *hostResource
		obj any
	}
	r.mu.Lock()
	var held []int
	r.table.Each(func(i int, h *canon.ResourceHandle) {
		// NEUTER THIS lend test, and TestCloseLeavesAnOwnLentToACallInProgress runs the destructor inside the
		// call that borrows the object.
		if h.Own() && h.Lends() == 0 {
			held = append(held, i)
		}
	})
	ds := make([]doomed, 0, len(held))
	for _, i := range held {
		h, err := r.table.Remove(i)
		if err != nil {
			continue // Each just reported it live under this lock; unreachable
		}
		ds = append(ds, doomed{t: r.types[h.RT()], obj: r.objs[h.Rep()]})
		delete(r.objs, h.Rep())
	}
	r.mu.Unlock()
	for _, d := range ds {
		if d.t == nil {
			continue // an RT no mint recorded; mint is the only way an own enters this table
		}
		_ = d.t.destroy(d.obj) //nolint:errcheck // no reader: Close returns no error, and the guest that could be told is gone (ADR 0118)
	}
}

// resourceTrap renders a refused handle operation as the trap the reference raises for it.
func resourceTrap(op string, t *hostResource, idx int32, err error) error {
	return &interp.Trap{Reason: fmt.Sprintf("%s of %s handle %d: %v", op, t.name, idx, err)}
}

// mint lowers a host object as a fresh `own<t>` in this instance's table and returns the guest's index
// (def:lower_own).
func (r *hostResources) mint(t *hostResource, obj any) int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextRep++
	r.objs[r.nextRep] = obj
	r.types[t.rt] = t
	return int32(r.table.Add(t.rt, r.nextRep, true))
}

// lendAs lifts a `borrow<t>` for the length of one call (def:lift_borrow) and returns the object as a T,
// with the release the caller defers to the call's return. An index this instance does not hold, or one
// naming another type, is refused as the reference refuses it.
func lendAs[T any](r *hostResources, t *hostResource, idx int32) (obj T, release func(), err error) {
	var zero T
	r.mu.Lock()
	h, err := r.table.LiftBorrow(int(idx), t.rt)
	if err != nil {
		r.mu.Unlock()
		return zero, nil, resourceTrap("borrow", t, idx, err)
	}
	held := r.objs[h.Rep()]
	r.mu.Unlock()
	release = func() {
		r.mu.Lock()
		h.EndLend()
		r.mu.Unlock()
	}
	obj, ok := held.(T)
	if !ok {
		release()
		return zero, nil, fmt.Errorf("component: %s handle %d holds a %T, not the %T its methods take — "+
			"a host type was minted over the wrong object", t.name, idx, held, zero)
	}
	return obj, release, nil
}

// takeAs lifts an `own<t>` argument (def:lift_own): the handle leaves the guest's table and the object
// passes to the host, whose responsibility its release now is.
func takeAs[T any](r *hostResources, t *hostResource, idx int32) (T, error) {
	var zero T
	r.mu.Lock()
	defer r.mu.Unlock()
	rep, err := r.table.LiftOwn(int(idx), t.rt)
	if err != nil {
		return zero, resourceTrap("own transfer", t, idx, err)
	}
	obj := r.objs[rep]
	delete(r.objs, rep)
	v, ok := obj.(T)
	if !ok {
		return zero, fmt.Errorf("component: %s handle %d held a %T, not a %T", t.name, idx, obj, zero)
	}
	return v, nil
}

// drop is `canon resource.drop` of a host resource type (def:canon_resource_drop): the handle is removed,
// refused if it is not held, names another type, or is lent to a call in progress, and an own's destructor
// runs once, outside the lock. A borrow handle needs nothing more, since no borrow scope is modeled.
func (r *hostResources) drop(t *hostResource, idx int32) error {
	r.mu.Lock()
	h, err := r.table.Drop(int(idx), t.rt)
	if err != nil {
		r.mu.Unlock()
		return resourceTrap("resource.drop", t, idx, err)
	}
	if !h.Own() {
		r.mu.Unlock()
		return nil
	}
	obj := r.objs[h.Rep()]
	delete(r.objs, h.Rep())
	r.mu.Unlock()
	if derr := t.destroy(obj); derr != nil {
		return fmt.Errorf("component: resource.drop of %s handle %d: destructor: %w", t.name, idx, derr)
	}
	return nil
}

// dropFunc is the `canon resource.drop` built-in bound for one host type in one instance.
func (r *hostResources) dropFunc(t *hostResource) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("component: resource.drop of %s: got %d core args, want 1 (handle)", t.name, len(args))
		}
		return nil, r.drop(t, args[0].Int32())
	}
}

// resourceMethod is a host import whose arguments or results are handles to host resources. It is a
// factory the walk calls with the instance's table, the shape `streamConsumer` has for the async table, and
// it is kept separate from wasi() by type for the same reason: an impl that needs a table cannot be bound
// from a source that has none.
type resourceMethod func(res *hostResources) interp.CanonFunc

// method adapts a typed handler over a `self: borrow<T>` first argument. The adapter lends `self` for the
// call, hands the handler the object as a T, and releases the lend when the handler returns, whatever it
// returns; the handler sees the remaining core arguments and the instance's table, for an own it mints.
func method[T any](t *hostResource, f func(res *hostResources, c *interp.CanonCaller, self T, rest []interp.Value) ([]interp.Value, error)) resourceMethod {
	return func(res *hostResources) interp.CanonFunc {
		return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("component: a method of %s got no self argument", t.name)
			}
			self, release, err := lendAs[T](res, t, args[0].Int32())
			if err != nil {
				return nil, err
			}
			defer release()
			return f(res, c, self, args[1:])
		}
	}
}

// mints adapts a constructor-shaped import, `() -> own<T>`, over a function building the object. The
// object is built at the call, so it sees the host's state as it is then.
func mints(t *hostResource, build func() any) resourceMethod {
	return func(res *hostResources) interp.CanonFunc {
		return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
			return []interp.Value{interp.I32(res.mint(t, build()))}, nil
		}
	}
}

// hostResourceSource is the host's resource half, threaded through the walk beside the other impl sources:
// the resource types it implements and the imports over them, both keyed by name and compatibility class
// (`interface@class::resource`, `interface@class::export`) and read through lookupHost (ADR 0115).
type hostResourceSource struct {
	types   map[string]*hostResource
	methods map[string]resourceMethod
}

// typeStep records the host-resource identity of one entry of the component type index space (ADR 0117),
// so a `canon resource.drop` naming the entry by ordinal can find the host type it drops. Only an alias of
// a type export of a host-filled instance has one — `(alias export $streams "output-stream" (type))` over
// the import `wasi:io/streams@0.2.3` is `wasi:io/streams@0.2.3::output-stream`. Every other entry records
// "", which keeps today's handling wherever the type is used. A nested instance reaches the same identity,
// because its instance argument is the parent's host-filled instance.
func (w *walker) typeStep(d Def) {
	ident := ""
	if d.Section == SectionAlias {
		a := w.c.Aliases[d.Item]
		if a.Kind == AliasExport && a.Sort == SortType && int(a.InstanceIdx) < len(w.compInstances) {
			if ci := w.compInstances[a.InstanceIdx].inst; ci != nil && ci.stub && ci.name != "" {
				ident = ci.name + "::" + a.Name
			}
		}
	}
	w.typeIdents = append(w.typeIdents, ident)
}

// hostResourceAt resolves a type-space ordinal to the host resource type it names, or nil when it names
// none the host implements for the guest's track.
func (w *walker) hostResourceAt(typeIdx uint32) *hostResource {
	if int(typeIdx) >= len(w.typeIdents) {
		return nil
	}
	return w.hostResourceNamed(w.typeIdents[typeIdx])
}

// hostResourceNamed resolves a resource identity (`interface@version::resource`) to the host resource type
// serving it for the identity's track, or nil.
func (w *walker) hostResourceNamed(identity string) *hostResource {
	if identity == "" {
		return nil
	}
	t, ok, _ := lookupHost(w.resourceSrc.types, identity)
	if !ok {
		return nil
	}
	return t
}

// hostRT is the resolver canonTypeIn takes (ADR 0118): a resource identity the host implements carries
// that resource's RT, the number the instance's table entries hold.
func (w *walker) hostRT(identity string) (int, bool) {
	if t := w.hostResourceNamed(identity); t != nil {
		return t.rt, true
	}
	return 0, false
}

// checkHostResources checks a host-served import's signature against the host's resource types (#1252,
// ADR 0118), so a guest whose declaration disagrees with the host's is refused at instantiate rather than
// trapping at its first call, or worse, being answered over a handle of another type.
//
//   - **Every own and borrow must name a resource the host implements.** The host's handlers read a handle
//     as an index into the instance's table of host resources; a handle the host does not implement is not
//     in it, and one with no identity is a resource this engine did not resolve (a guest-defined one, #1251).
//   - **A `[method]R.m` lends its self as `borrow<R>`.** The host's adapter lends self at R's type (method
//     in hostresource.go), so a self of another type would trap at every call.
//
// It is checked for each import a host table serves, whichever table, since each marshals its signature.
func (w *walker) checkHostResources(lowerName string, sig *FuncType) error {
	if sig == nil {
		return nil
	}
	var bad error
	visit := func(vt ValType) {
		if bad != nil {
			return
		}
		if vt.Resource == "" {
			bad = fmt.Errorf("%w: import %s takes or returns %s of a resource this engine did not resolve "+
				"to a host resource type (ADR 0118)", ErrLinkRefused, lowerName, valKindName(vt.Kind))
			return
		}
		// NEUTER THIS check, and TestHostImportOverAResourceTheHostDoesNotImplementIsRefused instantiates a
		// guest whose consume takes a widget.
		if w.hostResourceNamed(vt.Resource) == nil {
			bad = fmt.Errorf("%w: import %s takes or returns %s<%s>, a resource the host does not implement "+
				"(ADR 0118)", ErrLinkRefused, lowerName, valKindName(vt.Kind), vt.Resource)
		}
	}
	for _, p := range sig.Params {
		eachHandle(p.Type, visit, 0)
	}
	if sig.Result != nil {
		eachHandle(*sig.Result, visit, 0)
	}
	if bad != nil {
		return bad
	}
	return w.checkMethodSelf(lowerName, sig)
}

// checkMethodSelf is checkHostResources' method rule. NEUTER THIS (return nil), and
// TestHostMethodWhoseSelfIsAnotherResourceIsRefused instantiates a guest whose `[method]thing.poke` lends
// a gadget.
func (w *walker) checkMethodSelf(lowerName string, sig *FuncType) error {
	iface, export, qualified := strings.Cut(lowerName, "::")
	if !qualified {
		iface, export = "", lowerName
	}
	rest, isMethod := strings.CutPrefix(export, "[method]")
	if !isMethod {
		return nil
	}
	resource, _, _ := strings.Cut(rest, ".")
	identity := resource
	if qualified {
		identity = iface + "::" + resource
	}
	want := w.hostResourceNamed(identity)
	if len(sig.Params) == 0 || sig.Params[0].Type.Kind != VBorrow || want == nil ||
		w.hostResourceNamed(sig.Params[0].Type.Resource) != want {
		self := "nothing"
		if len(sig.Params) > 0 {
			self = valKindName(sig.Params[0].Type.Kind) + "<" + sig.Params[0].Type.Resource + ">"
		}
		return fmt.Errorf("%w: import %s is a method of %s, and the guest declares its self as %s; the host "+
			"lends borrow<%s> (ADR 0118)", ErrLinkRefused, lowerName, identity, self, identity)
	}
	return nil
}

// eachHandle calls f on every own and borrow in a resolved value type.
func eachHandle(vt ValType, f func(ValType), depth int) {
	if depth > 32 {
		return // a resolved type is acyclic; unmodeledValKind's bound, for a hand-built one
	}
	sub := func(p *ValType) {
		if p != nil {
			eachHandle(*p, f, depth+1)
		}
	}
	switch vt.Kind {
	case VOwn, VBorrow:
		f(vt)
	case VList, VOption, VFuture, VStream:
		sub(vt.Elem)
	case VResult:
		sub(vt.Ok)
		sub(vt.Err)
	case VVariant:
		for _, c := range vt.Cases {
			sub(c.Type)
		}
	case VRecord:
		for _, fd := range vt.Fields {
			eachHandle(fd.Type, f, depth+1)
		}
	case VTuple:
		for _, e := range vt.Elems {
			eachHandle(e, f, depth+1)
		}
	default:
	}
}

// checkConsumerPayload is #1248's consumer (ADR 0118): the future a stream consumer returns is lowered at
// the host's declared payload type (stdioWriteResultType), and the guest reads the completion at the type
// its own signature declares, so the two must be one type. The guest's is resolved through its `use`d types
// and its resources through the host's (canonTypeIn), then compared structurally (canon.TypeEqual).
//
// NEUTER THIS (return nil), and TestWriteViaStreamPayloadMustAgreeWithTheHosts instantiates a guest whose
// `error-code` has other labels, which would read the host's discriminants as its own.
func (w *walker) checkConsumerPayload(lowerName string, sig *FuncType, sc *streamConsumer) error {
	if sig == nil || sc.payload == nil {
		return nil
	}
	want, err := sc.payload()
	if err != nil {
		return err
	}
	if sig.Result == nil || sig.Result.Kind != VFuture || sig.Result.Elem == nil {
		return fmt.Errorf("%w: import %s must return future<%s>, the host's declaration; the guest's signature "+
			"returns no future payload (ADR 0118)", ErrLinkRefused, lowerName, want)
	}
	got, err := canonTypeIn(*sig.Result.Elem, w.hostRT)
	if err != nil {
		return fmt.Errorf("%w: import %s's future payload: %w", ErrLinkRefused, lowerName, err)
	}
	if !canon.TypeEqual(got, want) {
		return fmt.Errorf("%w: import %s returns future<%s> in the guest's signature and future<%s> in the "+
			"host's declaration, which the host lowers its completion at (ADR 0118)", ErrLinkRefused, lowerName, got, want)
	}
	return nil
}
