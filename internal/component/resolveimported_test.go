// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// Witnesses for ADR 0118 (#1248, #1252): an imported instance's exported types resolve to their
// definitions, a guest's own and borrow carry their resource's identity and are checked against the host's
// resource types, and the owns a guest still holds at Close are destroyed.

// loadFixture loads a committed component fixture.
func loadFixture(t *testing.T, name string) *Component {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := Load(b)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return c
}

// TestAUsedTypeDecodesAsItsDefinition is #1248's decode witness, on p3async-hello's own bytes.
// `write-via-stream` returns `future<result<_, error-code>>`, and `error-code` reaches the stdout interface
// from `wasi:cli/types` through `use`: a type export bound `(eq 0)` over an enum, a top-level alias export of
// it, and an outer alias into stdout's instance type. It decoded as `future<ref>`, because every type export
// was a resource placeholder. Now the enum is there, labels and all.
//
// NEUTER, run: exportedTypeDef returning the sub-resource placeholder for an eq bound too. The payload's
// error reads `a typeidx reference` and the test fails.
func TestAUsedTypeDecodesAsItsDefinition(t *testing.T) {
	c := loadFixture(t, "p3async-hello.wasm")
	sig := (&walker{c: c}).lowerSignature("wasi:cli/stdout@0.3.0::write-via-stream")
	if sig == nil || sig.Result == nil {
		t.Fatalf("write-via-stream's signature = %+v, want one with a result", sig)
	}
	fut := *sig.Result
	if fut.Kind != VFuture || fut.Elem == nil {
		t.Fatalf("write-via-stream returns %s, want a future with a payload", valKindName(fut.Kind))
	}
	res := *fut.Elem
	if res.Kind != VResult || res.Ok != nil || res.Err == nil {
		t.Fatalf("the future's payload is %s (ok %v, err %v), want result<_, error-code>",
			valKindName(res.Kind), res.Ok, res.Err)
	}
	if res.Err.Kind != VEnum {
		t.Fatalf("error-code decodes as %s, want the enum wasi:cli/types declares", valKindName(res.Err.Kind))
	}
	if want := []string{"io", "illegal-byte-sequence", "pipe"}; !slices.Equal(res.Err.Labels, want) {
		t.Errorf("error-code's labels are %q, want %q", res.Err.Labels, want)
	}
}

// TestAUsedCompoundTypeIsResolvedInItsOwnScope: a type export copied out of its instance type must take its
// references' meaning with it. use-compound-type's `b` is a record whose field names the enum `a` by its
// local index in `test:t/types`, and `test:t/use`'s `get` returns `b`. The field must be the enum.
//
// NEUTER, run: instanceType not resolving its type exports against its own space, and the field is
// resolved in the component's scope instead, where its index names something else.
func TestAUsedCompoundTypeIsResolvedInItsOwnScope(t *testing.T) {
	c := loadFixture(t, "use-compound-type.wasm")
	sig := (&walker{c: c}).lowerSignature("test:t/use@0.1.0::get")
	if sig == nil || sig.Result == nil || sig.Result.Kind != VRecord || len(sig.Result.Fields) != 1 {
		t.Fatalf("get's signature = %+v, want a result that is the record b", sig)
	}
	f := sig.Result.Fields[0].Type
	if f.Kind != VEnum || !slices.Equal(f.Labels, []string{"x", "y"}) {
		t.Errorf("b's field is %s %q, want the enum a with labels x and y", valKindName(f.Kind), f.Labels)
	}
}

// TestUsedTypesResolveInP3HelloAndEveryImportStillBinds is the hazard the three causes of #1248 share:
// resolving the type exports alone puts the outer aliases that name them in reach, so each must resolve or
// it becomes VUnresolvedAlias, and a lowered signature carrying one is refused at instantiate.
//
// p3hello's `[method]descriptor.stat` returns `descriptor-stat`, a record whose three timestamps are
// `option<datetime>`, and `datetime` is `use`d from `wasi:clocks/wall-clock`. Each timestamp must be the
// record `datetime` is. And every import p3hello and p3echo declare must still resolve with no unresolved
// alias, and each guest must instantiate against the real host.
//
// NEUTER, run, twice:
//   - Component.typeOrdinal leaving an alias export unresolved: every timestamp is VUnresolvedAlias, and
//     p3hello is refused at instantiate. Against the real host the resource check names `get-stderr` first,
//     whose `own<output-stream>` loses its identity with the alias; against the stub host
//     (TestP3HelloRunIsCallableAndImportsReachTheStub) the refusal names `[method]descriptor.stat`.
//   - resolveVal keeping an index in `seen` after its chain returns: the first timestamp resolves and the
//     second and third read as a cycle.
func TestUsedTypesResolveInP3HelloAndEveryImportStillBinds(t *testing.T) {
	c := loadFixture(t, "p3hello.wasm")
	sig := (&walker{c: c}).lowerSignature("wasi:filesystem/types@0.2.3::[method]descriptor.stat")
	if sig == nil || sig.Result == nil || sig.Result.Ok == nil || sig.Result.Ok.Kind != VRecord {
		t.Fatalf("descriptor.stat's signature = %+v, want result<descriptor-stat, error-code> with the record", sig)
	}
	stamps := 0
	for _, f := range sig.Result.Ok.Fields {
		if !strings.HasSuffix(f.Name, "-timestamp") {
			continue
		}
		stamps++
		if f.Type.Kind != VOption || f.Type.Elem == nil || f.Type.Elem.Kind != VRecord {
			t.Errorf("%s is %s, want option<datetime> with datetime's record", f.Name, valKindName(f.Type.Kind))
			continue
		}
		var names []string
		for _, df := range f.Type.Elem.Fields {
			names = append(names, df.Name)
		}
		if !slices.Equal(names, []string{"seconds", "nanoseconds"}) {
			t.Errorf("%s's datetime has fields %q, want seconds and nanoseconds", f.Name, names)
		}
	}
	if stamps != 3 {
		t.Fatalf("descriptor-stat has %d timestamp field(s), want 3", stamps)
	}

	for _, fx := range []string{"p3hello.wasm", "p3echo.wasm"} {
		c := loadFixture(t, fx)
		w := &walker{c: c}
		checked := 0
		for _, imp := range c.Imports {
			td, ok := c.sectionTypeAt()(imp.TypeIndex)
			if imp.Kind != ExternInstance || !ok || td.Inst == nil {
				continue
			}
			for _, e := range td.Inst.Exports {
				if e.Func == nil {
					continue
				}
				checked++
				if s := w.lowerSignature(imp.Name + "::" + e.Name); s == nil || sigHasUnresolvedAlias(s) {
					t.Errorf("%s: %s::%s resolves to %+v, want a signature with no unresolved alias", fx, imp.Name, e.Name, s)
				}
			}
		}
		if checked == 0 {
			t.Fatalf("%s: no imported function was checked, so this asserts nothing", fx)
		}
		b, _ := os.ReadFile("testdata/" + fx)
		in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
		if err != nil {
			t.Fatalf("%s no longer instantiates against the real host: %v", fx, err)
		}
		in.Close()
	}
}

// TestAWorldLevelTypeImportResolvesThroughItsBound covers the top-level type imports (ADR 0118):
// world-type-imports imports `u` bound `(eq $u)` over a record, and `r` bound `(sub resource)`, and its
// `[method]r.peek` takes `borrow<r>` and returns `u`. The self's identity is the import's name, and the
// result is the record.
//
// NEUTER, run: typeOrdinal answering nothing for an imported type, and the self has no identity and the
// result stays a reference.
func TestAWorldLevelTypeImportResolvesThroughItsBound(t *testing.T) {
	c := loadFixture(t, "world-type-imports.wasm")
	sig := (&walker{c: c}).lowerSignature("[method]r.peek")
	if sig == nil || len(sig.Params) != 1 || sig.Result == nil {
		t.Fatalf("[method]r.peek's signature = %+v, want one param and a result", sig)
	}
	if self := sig.Params[0].Type; self.Kind != VBorrow || self.Resource != "r" {
		t.Errorf("the self is %s of %q, want borrow of the import r", valKindName(self.Kind), self.Resource)
	}
	if r := sig.Result; r.Kind != VRecord || len(r.Fields) != 1 || r.Fields[0].Name != "a" {
		t.Errorf("the result is %s, want the record u is bound equal to", valKindName(r.Kind))
	}
}

// TestAGuestHandleCarriesItsHostResourcesIdentity is #1252.1 on p3hello's bytes. `[method]output-stream.write`
// takes `self: borrow<output-stream>`, a resource the streams instance type declares itself, so its identity
// is the import's: `wasi:io/streams@0.2.3::output-stream`. Its result's `stream-error` carries an
// `own<error>` that reaches the streams instance type from `wasi:io/error` through an outer alias, so that
// identity is the error interface's. And through the host's resource types the borrow's canon type carries
// the host output-stream's RT, where it carried the local type index.
//
// NEUTER, run, twice: resolveVal not filling Resource fails the identity assertions; canonTypeWalk ignoring
// its resolver gives the local index 0 for the RT.
func TestAGuestHandleCarriesItsHostResourcesIdentity(t *testing.T) {
	c := loadFixture(t, "p3hello.wasm")
	w := &walker{c: c, resourceSrc: NewHost(io.Discard, io.Discard, nil).resourceSource()}
	sig := w.lowerSignature("wasi:io/streams@0.2.3::[method]output-stream.write")
	if sig == nil || len(sig.Params) == 0 {
		t.Fatalf("output-stream.write's signature = %+v", sig)
	}
	self := sig.Params[0].Type
	if self.Kind != VBorrow || self.Resource != "wasi:io/streams@0.2.3::output-stream" {
		t.Fatalf("output-stream.write's self is %s of %q, want borrow of wasi:io/streams@0.2.3::output-stream",
			valKindName(self.Kind), self.Resource)
	}
	var errOwn *ValType
	if r := sig.Result; r != nil && r.Err != nil && r.Err.Kind == VVariant {
		for _, cs := range r.Err.Cases {
			if cs.Name == "last-operation-failed" {
				errOwn = cs.Type
			}
		}
	}
	if errOwn == nil || errOwn.Kind != VOwn || errOwn.Resource != "wasi:io/error@0.2.3::error" {
		t.Fatalf("stream-error's last-operation-failed carries %+v, want own of wasi:io/error@0.2.3::error", errOwn)
	}
	// The premise: the two numbers differ, or the RT assertion below could not tell a resolved handle from
	// one that kept its index.
	if int(self.Ref) == outputStreamResource.rt {
		t.Fatalf("the self's local index and the host output-stream's RT are both %d, so this cannot tell them apart", self.Ref)
	}
	ct, err := canonTypeIn(self, w.hostRT)
	if err != nil {
		t.Fatal(err)
	}
	if ct.RT != outputStreamResource.rt {
		t.Errorf("the self's canon type carries RT %d, want the host output-stream's %d (the local index is %d)",
			ct.RT, outputStreamResource.rt, self.Ref)
	}
}

// TestAResolvedHandleKeepsItsIdentityInAnotherScope pins resolveVal's keep rule. A resolved type copied into
// another scope (an outer alias of a `use`d type) is resolved again there, and its own's Ref is still an
// ordinal of the scope it came from. Here that ordinal names another resource in the new scope, which is
// the case the rule exists for: the identity found in the first scope must survive.
//
// NEUTER, run: resolveVal re-asking the scope when Resource is already set, and the own names b's resource.
func TestAResolvedHandleKeepsItsIdentityInAnotherScope(t *testing.T) {
	first := sliceTypeAt([]TypeDef{{Kind: TDResource, Resource: "test:a/i@0.1.0::x"}})
	second := sliceTypeAt([]TypeDef{{Kind: TDResource, Resource: "test:b/i@0.1.0::y"}})
	once := resolveVal(ValType{Kind: VOwn, Ref: 0}, first, nil)
	if once.Resource != "test:a/i@0.1.0::x" {
		t.Fatalf("own<0> in the first scope resolved to %q, want test:a/i@0.1.0::x", once.Resource)
	}
	if again := resolveVal(once, second, nil); again.Resource != once.Resource {
		t.Errorf("resolved again in another scope, the own names %q, want the %q it was resolved to", again.Resource, once.Resource)
	}
}

// TestASharedTypeChainIsCutAtTheSizeBudget is grave #1256's bound. With the chain set repaired, a type
// shared by others is expanded at every use: in a local space where t0 is u32 and t_k is
// tuple<t_{k-1}, t_{k-1}>, t_n inlines to 2^(n+1)-1 nodes. A crafted component picks n, so one resolve is
// bounded at maxResolvedTypeSize nodes and cut to VUnresolvedAlias past it, which the instantiate-time
// refusal names. At 16 levels (131071 nodes) the chain resolves in full; at 24 (33554431) it is cut.
//
// Hand-built, because the space is the subject: wasm-tools would refuse such a component's type as too
// large, so no fixture of it can be assembled.
//
// NEUTER, run: the budget check removed in resolveVal, and the 24-level chain resolves in full, with no alias.
func TestASharedTypeChainIsCutAtTheSizeBudget(t *testing.T) {
	chain := func(n int) typeAt {
		local := []TypeDef{{Kind: TDVal, Val: ValType{Kind: VU32}}}
		for k := 1; k <= n; k++ {
			ref := ValType{Kind: VRef, Ref: uint32(k - 1)}
			local = append(local, TypeDef{Kind: TDVal, Val: ValType{Kind: VTuple, Elems: []ValType{ref, ref}}})
		}
		return sliceTypeAt(local)
	}
	var contains func(vt ValType) bool
	contains = func(vt ValType) bool {
		if vt.Kind == VUnresolvedAlias {
			return true
		}
		for _, e := range vt.Elems {
			if contains(e) {
				return true
			}
		}
		return false
	}
	if full := resolveVal(ValType{Kind: VRef, Ref: 16}, chain(16), nil); full.Kind != VTuple || contains(full) {
		t.Errorf("the 16-level chain, 131071 nodes, did not resolve in full under the budget")
	}
	if cut := resolveVal(ValType{Kind: VRef, Ref: 24}, chain(24), nil); !contains(cut) {
		t.Errorf("the 24-level chain, 33554431 nodes, resolved with no VUnresolvedAlias: the budget did not cut it")
	}
}

// TestAnUnresolvedAliasIsSeenInsideAFutureAndARecord pins valHasUnresolvedAlias's ADR 0118 arms. The
// instantiate-time refusal of an unresolved alias asks it, and a future's payload and a record's fields are
// where a `use`d type sits since #1248, so an unresolved one there must be seen, as one in a list is.
//
// NEUTER, run: the future/stream and record arms removed, and both specimens read clean.
func TestAnUnresolvedAliasIsSeenInsideAFutureAndARecord(t *testing.T) {
	bad := ValType{Kind: VUnresolvedAlias}
	for name, vt := range map[string]ValType{
		"future": {Kind: VFuture, Elem: &ValType{Kind: VResult, Err: &bad}},
		"stream": {Kind: VStream, Elem: &bad},
		"record": {Kind: VRecord, Fields: []NamedVal{{Name: "a", Type: ValType{Kind: VU32}}, {Name: "b", Type: bad}}},
	} {
		if !valHasUnresolvedAlias(vt, 0) {
			t.Errorf("an unresolved alias inside a %s was not seen", name)
		}
	}
}

// gadgetHost is thingHost serving a second resource type, `test:res/gadgets` `gadget`, which nothing mints.
func gadgetHost() *thingHost {
	th := newThingHost()
	th.resourceTypeImpls["test:res/gadgets::gadget"] = newHostResource("test:res/gadgets::gadget", nil)
	return th
}

// TestHostMethodWhoseSelfIsAnotherResourceIsRefused is #1252.1's method rule. host-resource-self-mismatch
// declares `test:res/things`' `thing` as gadgets' `gadget`, so `[method]thing.poke`'s self is a borrow of a
// gadget. The host serves both types, so every handle in the signature names a host resource, and its poke
// lends a thing, so each call would trap on the type check. It is refused at instantiate, naming the import
// and both resources.
//
// NEUTER, run: checkMethodSelf returning nil, and the guest instantiates.
func TestHostMethodWhoseSelfIsAnotherResourceIsRefused(t *testing.T) {
	b, err := os.ReadFile("testdata/host-resource-self-mismatch.wasm")
	if err != nil {
		t.Fatal(err)
	}
	in, err := InstantiateWithHost(b, gadgetHost().Host)
	if err == nil {
		in.Close()
		t.Fatal("a guest whose [method]thing.poke lends a gadget instantiated")
	}
	if !errors.Is(err, ErrLinkRefused) {
		t.Fatalf("refused with %v, want ErrLinkRefused", err)
	}
	for _, want := range []string{"[method]thing.poke", "test:res/things@0.1.0::thing", "test:res/gadgets@0.1.0::gadget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
}

// TestHostImportOverAResourceTheHostDoesNotImplementIsRefused is #1252.1's identity rule.
// host-resource-unknown declares `consume`'s argument as an own of `widget`, which the host does not
// implement, while the host serves `consume` over its things. It is refused at instantiate, naming both.
//
// NEUTER, run: checkHostResources' visit accepting an identity the host does not serve, and the guest
// instantiates.
func TestHostImportOverAResourceTheHostDoesNotImplementIsRefused(t *testing.T) {
	b, err := os.ReadFile("testdata/host-resource-unknown.wasm")
	if err != nil {
		t.Fatal(err)
	}
	in, err := InstantiateWithHost(b, newThingHost().Host)
	if err == nil {
		in.Close()
		t.Fatal("a guest whose consume takes a widget the host does not implement instantiated")
	}
	if !errors.Is(err, ErrLinkRefused) {
		t.Fatalf("refused with %v, want ErrLinkRefused", err)
	}
	for _, want := range []string{"test:res/things@0.1.0::consume", "own<test:res/things@0.1.0::widget>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
}

// TestWriteViaStreamPayloadMustAgreeWithTheHosts is #1248's consumer. The host lowers write-via-stream's
// completion at its interface's `result<_, error-code>` (ADR 0116), and the guest reads it at its own
// declaration, which #1248 made readable. The two fixtures differ in one label of `error-code`: the one
// that declares the host's three instantiates, and the one that declares `io` and `pipe` is refused at
// instantiate, naming both types.
//
// NEUTER, run: checkConsumerPayload returning nil, and write-via-stream-other-error-code instantiates.
func TestWriteViaStreamPayloadMustAgreeWithTheHosts(t *testing.T) {
	t.Setenv(asyncGateEnv, "") // the default, which is on
	inst := func(fx string) error {
		b, err := os.ReadFile("testdata/" + fx)
		if err != nil {
			t.Fatal(err)
		}
		in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
		if err == nil {
			in.Close()
		}
		return err
	}
	if err := inst("write-via-stream-error-code.wasm"); err != nil {
		t.Fatalf("the control, whose error-code is the host's, is refused: %v", err)
	}
	err := inst("write-via-stream-other-error-code.wasm")
	if err == nil {
		t.Fatal("a guest whose error-code has other labels than the host's instantiated")
	}
	if !errors.Is(err, ErrLinkRefused) {
		t.Fatalf("refused with %v, want ErrLinkRefused", err)
	}
	for _, want := range []string{"write-via-stream", "enum{io, pipe}", "enum{io, illegal-byte-sequence, pipe}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
}

// TestCloseDestroysTheOwnsTheGuestStillHolds is #1252.2. A guest that mints an own and never drops it has
// the type's destructor run once when its instance closes, and a second Close runs it no more. ADR 0117
// left this to the slice that needed it: a socket or a file the guest never dropped must still close.
//
// NEUTER, run: walker.close without its closeAllHostResources call, and the count after Close is 0.
func TestCloseDestroysTheOwnsTheGuestStillHolds(t *testing.T) {
	th := newThingHost()
	in := instantiateThings(t, th)
	for range 2 {
		if _, err := callU32(in, "make"); err != nil {
			t.Fatalf("make: %v", err)
		}
	}
	h, err := callU32(in, "make")
	if err != nil {
		t.Fatalf("make: %v", err)
	}
	if _, err := callU32(in, "drop", h); err != nil {
		t.Fatalf("drop(%d): %v", h, err)
	}
	if got := th.drops.Load(); got != 1 {
		t.Fatalf("after one guest drop the destructor ran %d time(s), want 1", got)
	}
	in.Close()
	if got := th.drops.Load(); got != 3 {
		t.Fatalf("after Close the destructor ran %d time(s), want 3: the one dropped and the two still held", got)
	}
	if n := liveObjectsOf[*thing](in.w.hostRes); n != 0 {
		t.Errorf("the closed instance still holds %d thing(s)", n)
	}
	in.Close()
	if got := th.drops.Load(); got != 3 {
		t.Errorf("a second Close ran the destructor again: %d runs, want 3", got)
	}
}

// TestCloseReachesTheOwnsANestedInstanceHolds: each component instance has its own table of host resources
// (ADR 0117), so an own minted for a nested instance is in the nested table, and the outer instance's Close
// must reach it. host-resource-nested calls `make` from a nested component and re-exports it.
//
// NEUTER, run: closeAllHostResources not recursing into the children, and the count after Close is 0.
func TestCloseReachesTheOwnsANestedInstanceHolds(t *testing.T) {
	th := newThingHost()
	b, err := os.ReadFile("testdata/host-resource-nested.wasm")
	if err != nil {
		t.Fatal(err)
	}
	in, err := InstantiateWithHost(b, th.Host)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	if _, err := callU32(in, "make"); err != nil {
		in.Close()
		t.Fatalf("make: %v", err)
	}
	// The premise: the own is in the nested table and not the outer one, or this would pass on the outer
	// table's teardown alone.
	if n := liveObjectsOf[*thing](in.w.hostRes); n != 0 || len(in.w.children) != 1 ||
		liveObjectsOf[*thing](in.w.children[0].hostRes) != 1 {
		in.Close()
		t.Fatalf("the own is not where this test needs it: outer holds %d, %d nested instance(s)", n, len(in.w.children))
	}
	in.Close()
	if got := th.drops.Load(); got != 1 {
		t.Errorf("after Close the destructor ran %d time(s) for the own the nested instance held, want 1", got)
	}
}

// TestCloseLeavesAnOwnLentToACallInProgress is closeAll's lend rule. A teardown that meets an own lent to a
// call still running does not destroy it under the call. The teardown runs from inside `poke`, standing in
// for a Close whose wait for the host calls in flight expired, as
// TestHostResourceBorrowIsLentForTheCallAndReleasedAtReturn stands a drop in for another agent. Once the
// call returns, Close destroys it.
//
// NEUTER, run: closeAll destroying a lent own too, and the destructor runs inside the call.
func TestCloseLeavesAnOwnLentToACallInProgress(t *testing.T) {
	th := newThingHost()
	in := instantiateThings(t, th)
	h, err := callU32(in, "make")
	if err != nil {
		t.Fatalf("make: %v", err)
	}
	inside := int32(-1)
	th.during = func(res *hostResources) {
		res.closeAll()
		inside = th.drops.Load()
	}
	if got, err := callU32(in, "poke", h); err != nil || got != 101 {
		t.Fatalf("poke(%d) = %d, %v; want 101", h, got, err)
	}
	if inside != 0 {
		t.Fatalf("a teardown during the call ran the destructor %d time(s) on the own the call borrows, want 0", inside)
	}
	th.during = nil
	in.Close()
	if got := th.drops.Load(); got != 1 {
		t.Errorf("after the call returned, Close ran the destructor %d time(s), want 1", got)
	}
}
