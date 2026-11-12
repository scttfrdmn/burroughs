// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"io"
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
}

func newHostResources() *hostResources {
	return &hostResources{table: canon.NewHandleTable(), objs: map[uint32]any{}}
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
	if int(typeIdx) >= len(w.typeIdents) || w.typeIdents[typeIdx] == "" {
		return nil
	}
	t, ok, _ := lookupHost(w.resourceSrc.types, w.typeIdents[typeIdx])
	if !ok {
		return nil
	}
	return t
}
