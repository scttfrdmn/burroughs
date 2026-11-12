// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// liveObjectsOf counts the objects of type T a table still holds for the guest.
func liveObjectsOf[T any](r *hostResources) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, o := range r.objs {
		if _, ok := o.(T); ok {
			n++
		}
	}
	return n
}

// thing is the object behind host-resource-synth's `thing` handles.
type thing struct{ id uint32 }

// thingHost is a Host serving host-resource-synth's test interface (ADR 0117): `make` mints a thing whose id
// counts up from 101 across every instance the host serves, `poke` returns the borrowed thing's id, and
// `consume` takes one and returns its id. The type's destructor counts its runs into drops. during, when
// set, runs inside `poke` while its self is lent.
type thingHost struct {
	*Host
	res    *hostResource
	drops  atomic.Int32
	during func(res *hostResources)
}

func newThingHost() *thingHost {
	th := &thingHost{Host: NewHost(io.Discard, io.Discard, nil)}
	th.res = newHostResource("test:res/things::thing", func(any) error {
		th.drops.Add(1)
		return nil
	})
	var next atomic.Uint32
	th.resourceTypeImpls = map[string]*hostResource{"test:res/things::thing": th.res}
	th.resourceImpls = map[string]resourceMethod{
		"test:res/things::make": mints(th.res, func() any { return &thing{id: 100 + next.Add(1)} }),
		"test:res/things::[method]thing.poke": method(th.res, func(res *hostResources, _ *interp.CanonCaller, self *thing, _ []interp.Value) ([]interp.Value, error) {
			if th.during != nil {
				th.during(res)
			}
			return []interp.Value{interp.I32(int32(self.id))}, nil
		}),
		"test:res/things::consume": func(res *hostResources) interp.CanonFunc {
			return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
				t, err := takeAs[*thing](res, th.res, args[0].Int32())
				if err != nil {
					return nil, err
				}
				return []interp.Value{interp.I32(int32(t.id))}, nil
			}
		},
	}
	return th
}

// instantiateThings brings up one instance of host-resource-synth against th.
func instantiateThings(t *testing.T, th *thingHost) *Instantiated {
	t.Helper()
	wasm, err := os.ReadFile("testdata/host-resource-synth.wasm")
	if err != nil {
		t.Fatal(err)
	}
	in, err := InstantiateWithHost(wasm, th.Host)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(in.Close)
	return in
}

// callU32 calls an export taking zero or one u32 and returning at most one, and returns the result.
func callU32(in *Instantiated, name string, args ...uint32) (uint32, error) {
	vs := make([]canon.Value, len(args))
	for i, a := range args {
		vs[i] = canon.U32(a)
	}
	res, err := in.CallValues(name, vs...)
	if err != nil || len(res) == 0 {
		return 0, err
	}
	v, _ := res[0].U32()
	return v, nil
}

// isResourceTrap reports whether a call failed as an *interp.Trap, the form resourceTrap gives the
// reference's trap_ifs — narrower than lockdown's isTrap, which counts any failed host function.
func isResourceTrap(err error) bool {
	var tr *interp.Trap
	return errors.As(err, &tr)
}

// TestHostResourceHandleIsRefusedInAnotherInstance is #1137's R-4 on real instances: two instances of one
// component, served by one Host, and a handle index minted in the first used in the second. def:Table.get
// traps on an index the instance's table does not hold, so the second instance must trap. Before ADR 0117
// the handles lived in maps on the Host, which every instance it served shared, so the index named the
// first instance's stream in both.
//
// A third instance mints its own handle, gets the same index, and reads its own thing back through it, so
// the refusal is about whose table the index is in and not about the number.
//
// NEUTER, run: one hostResources shared by every walker (walkComponent's `newHostResources()` replaced by
// a package-level table), and the second instance's poke reads the first instance's thing.
func TestHostResourceHandleIsRefusedInAnotherInstance(t *testing.T) {
	th := newThingHost()
	a := instantiateThings(t, th)
	b := instantiateThings(t, th)
	c := instantiateThings(t, th)

	ha, err := callU32(a, "make")
	if err != nil {
		t.Fatalf("a.make: %v", err)
	}
	if got, cerr := callU32(a, "poke", ha); cerr != nil || got != 101 {
		t.Fatalf("a.poke(%d) = %d, %v; want a's own thing, 101", ha, got, cerr)
	}

	got, err := callU32(b, "poke", ha)
	if err == nil {
		t.Fatalf("b.poke(%d) = %d: an index minted in instance a resolved in instance b", ha, got)
	}
	if !isResourceTrap(err) || !strings.Contains(err.Error(), "is not live") {
		t.Errorf("b.poke(%d) = %v; want the trap def:Table.get raises for an index the table does not hold", ha, err)
	}

	hc, err := callU32(c, "make")
	if err != nil {
		t.Fatalf("c.make: %v", err)
	}
	if hc != ha {
		t.Fatalf("c's first handle is %d and a's was %d; each table starts at 1, so they should coincide", hc, ha)
	}
	if got, cerr := callU32(c, "poke", hc); cerr != nil || got != 102 {
		t.Errorf("c.poke(%d) = %d, %v; want c's own thing, 102, not a's 101 at the same index", hc, got, cerr)
	}
}

// TestHostResourceDropRunsTheDestructorOnce is the real drop (ADR 0117): the guest's `canon resource.drop`
// of an own<thing> removes the handle and runs the type's destructor exactly once, the freed slot is the
// next one minted (def:Table.add), the dropped index no longer resolves, and a second drop of it is refused
// rather than running the destructor again.
//
// NEUTER, run: resolverFor binding dropNoop for every type, and the destructor count after the drop is 0.
func TestHostResourceDropRunsTheDestructorOnce(t *testing.T) {
	th := newThingHost()
	in := instantiateThings(t, th)

	h1, err := callU32(in, "make")
	if err != nil {
		t.Fatalf("make: %v", err)
	}
	if _, err = callU32(in, "drop", h1); err != nil {
		t.Fatalf("drop(%d): %v", h1, err)
	}
	if got := th.drops.Load(); got != 1 {
		t.Fatalf("the destructor ran %d time(s) after the guest dropped its own, want 1", got)
	}
	if n := liveObjectsOf[*thing](in.w.hostRes); n != 0 {
		t.Errorf("the instance still holds %d thing(s) after the drop", n)
	}

	h2, err := callU32(in, "make")
	if err != nil {
		t.Fatalf("make: %v", err)
	}
	if h2 != h1 {
		t.Errorf("the next handle minted is %d; the reference reuses the freed slot %d first", h2, h1)
	}
	if got, cerr := callU32(in, "poke", h2); cerr != nil || got != 102 {
		t.Errorf("poke(%d) = %d, %v; want the new thing, 102", h2, got, cerr)
	}
	if _, err = callU32(in, "drop", h2); err != nil {
		t.Fatalf("drop(%d): %v", h2, err)
	}
	if got := th.drops.Load(); got != 2 {
		t.Fatalf("the destructor ran %d time(s) after two drops, want 2", got)
	}

	// Last, because a trap locks the store: the dropped index is gone, so a second drop traps and the
	// destructor does not run again.
	if _, err = callU32(in, "drop", h2); err == nil || !isResourceTrap(err) {
		t.Errorf("a second drop of %d = %v; want a trap", h2, err)
	}
	if got := th.drops.Load(); got != 2 {
		t.Errorf("the destructor ran %d time(s); a refused second drop must not run it", got)
	}
}

// TestHostResourceBorrowIsLentForTheCallAndReleasedAtReturn: a `self: borrow<thing>` is lent while the
// method runs and released when it returns (def:lift_borrow, def:Subtask.deliver_resolve), and an own with
// a lend outstanding cannot be dropped (def:canon_resource_drop:"trap_if(h.num_lends != 0)"). The drop is
// attempted from inside the call, standing in for another agent of the instance, since a single guest
// cannot run while its own sync call is in the host.
func TestHostResourceBorrowIsLentForTheCallAndReleasedAtReturn(t *testing.T) {
	th := newThingHost()
	in := instantiateThings(t, th)
	h, err := callU32(in, "make")
	if err != nil {
		t.Fatalf("make: %v", err)
	}

	var lendsDuring int
	var dropDuring error
	th.during = func(res *hostResources) {
		res.mu.Lock()
		hnd, gerr := res.table.Get(int(h))
		res.mu.Unlock()
		if gerr == nil {
			lendsDuring = hnd.Lends()
		}
		dropDuring = res.drop(th.res, int32(h))
	}
	if _, err = callU32(in, "poke", h); err != nil {
		t.Fatalf("poke(%d): %v", h, err)
	}
	th.during = nil

	if lendsDuring != 1 {
		t.Errorf("lends while poke ran = %d, want 1", lendsDuring)
	}
	if dropDuring == nil || !isResourceTrap(dropDuring) || !strings.Contains(dropDuring.Error(), "active lends") {
		t.Errorf("dropping the own while poke borrowed it = %v; want the outstanding-lend trap", dropDuring)
	}
	if got := th.drops.Load(); got != 0 {
		t.Errorf("the destructor ran %d time(s) for a refused drop", got)
	}
}

// TestHostResourceOwnArgumentTransfersToTheHost: an own<thing> argument leaves the guest's table
// (def:lift_own) and passes to the host, so the guest's index no longer resolves and no destructor runs,
// since the host now holds the object.
func TestHostResourceOwnArgumentTransfersToTheHost(t *testing.T) {
	th := newThingHost()
	in := instantiateThings(t, th)
	h, err := callU32(in, "make")
	if err != nil {
		t.Fatalf("make: %v", err)
	}
	if got, cerr := callU32(in, "consume", h); cerr != nil || got != 101 {
		t.Fatalf("consume(%d) = %d, %v; want the thing's id, 101", h, got, cerr)
	}
	if n := liveObjectsOf[*thing](in.w.hostRes); n != 0 {
		t.Errorf("the instance still holds %d thing(s) after transferring its own", n)
	}
	if got := th.drops.Load(); got != 0 {
		t.Errorf("the destructor ran %d time(s); a transferred own is the host's to release", got)
	}
	if _, err = callU32(in, "poke", h); err == nil || !isResourceTrap(err) {
		t.Errorf("poke(%d) after the transfer = %v; want a trap, the index is no longer held", h, err)
	}
}

// TestHostResourceSlotReuseMatchesWasmtime is the differential arm (ADR 0117): host-resource-reuse prints
// the index `get-stdout` hands it three times — once, again after dropping the first, and a third time
// with the second still held. The committed reading is wasmtime 49.0.3's, parsed rather than restated so
// the expectation cannot drift from what the oracle said.
//
// Burroughs read `1 2 3` before ADR 0117: the Host minted from a counter that never reused a number,
// because its drop was a no-op.
func TestHostResourceSlotReuseMatchesWasmtime(t *testing.T) {
	want := readingLine(t, "testdata/host-resource-reuse.reading", "wasmtime 49.0.3, 10 of 10")
	wasm, err := os.ReadFile("testdata/host-resource-reuse.wasm")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	in, err := InstantiateWithHost(wasm, NewHost(&out, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	if err = in.Call("wasi:cli/run@0.2.3"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(strings.Fields(out.String()), " "); got != want {
		t.Errorf("printed handle indices = %q, want wasmtime's %q", got, want)
	}
}

// readingLine returns the reading recorded beside a reader's label in a committed .reading file, failing
// rather than defaulting when the line is absent: a witness that falls back to a built-in expectation is
// checking itself.
func readingLine(t *testing.T, path, reader string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, "# "+reader); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("%s records no reading for %q", path, reader)
	return ""
}

// TestHostResourceTypesAreDistinct is the RT clash's control (ADR 0117). `errorRT` was 0, and so was the
// `own<descriptor>` element `get-directories` built, so a table could not tell an error handle from a
// descriptor. Every production resource type must carry a number no other has, and a borrow of one type
// presented as another is refused.
func TestHostResourceTypesAreDistinct(t *testing.T) {
	src := NewHost(io.Discard, io.Discard, nil).resourceSource()
	seen := map[int]string{}
	for k, r := range src.types {
		if prev, dup := seen[r.rt]; dup && prev != r.name {
			t.Errorf("%s and %s share resource-type number %d", prev, r.name, r.rt)
		}
		seen[r.rt] = r.name
		if r.rt == 0 {
			t.Errorf("%s (%s) has resource-type number 0, the number errorRT and get-directories shared", r.name, k)
		}
	}
	if len(seen) < 4 {
		t.Fatalf("only %d production resource type(s); the four wasi:io/filesystem types are expected", len(seen))
	}

	res := newHostResources()
	i := res.mint(errorResource, errors.New("boom"))
	if _, _, err := lendAs[*outputStream](res, outputStreamResource, i); err == nil {
		t.Error("an error handle was lent as an output-stream")
	}
	if _, err := res.table.Get(int(i)); err != nil {
		t.Errorf("a refused borrow removed the handle: %v", err)
	}
}
