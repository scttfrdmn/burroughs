// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The binding/export refusal (ADR 0084 / #720, Scott's ruling): a value of a kind the Canonical ABI
// codec cannot marshal — record/flags/enum/option/error-context — is refused **by name at the earliest
// point it would be marshaled**. An *implemented* import (a host lower that would run) refuses at
// **instantiate** (the binding); an *unimplemented* import keeps refusing at **call** through the stub
// (TestStubHostRefusesByName, unchanged); an *exported* function refuses at **Call**. `tuple`, `result`,
// `variant`, and every scalar/string/handle are modeled and pass — a modeled container is scanned into,
// so `list<tuple<string,string>>` (get-environment's empty result) is clean while `tuple<record>` is not.

func dummyCanon(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) { return nil, nil }

// TestUnmodeledInSigDiscriminates pins the scan's boundary: the unmodeled kinds are caught (directly, and
// through the modeled containers that nest them), and the modeled shapes — crucially get-environment's
// `list<tuple<string,string>>` — are not. This is the logic a hand check gets wrong (the recursion, and
// which kinds are in the set), so it is asserted directly rather than left to the two path tests.
func TestUnmodeledInSigDiscriminates(t *testing.T) {
	rec := ValType{Kind: VRecord, Fields: []NamedVal{{Name: "x", Type: ValType{Kind: VU32}}}}
	strT := ValType{Kind: VString}
	tupStrStr := ValType{Kind: VTuple, Elems: []ValType{strT, strT}}
	cases := []struct {
		name string
		vt   ValType
		want ValKind
		bad  bool
	}{
		{"record", rec, VRecord, true},
		{"option", ValType{Kind: VOption, Elem: &strT}, VOption, true},
		{"flags", ValType{Kind: VFlags, Labels: []string{"a", "b"}}, VFlags, true},
		{"enum", ValType{Kind: VEnum, Labels: []string{"a", "b"}}, VEnum, true},
		{"error-context", ValType{Kind: VErrorContext}, VErrorContext, true},
		{"tuple-of-record", ValType{Kind: VTuple, Elems: []ValType{rec}}, VRecord, true},
		{"list-of-option", ValType{Kind: VList, Elem: ptr(ValType{Kind: VOption, Elem: &strT})}, VOption, true},
		// Modeled — must NOT be flagged.
		{"string", strT, 0, false},
		{"list-u8", ValType{Kind: VList, Elem: ptr(ValType{Kind: VU8})}, 0, false},
		{"tuple-string-string", tupStrStr, 0, false},
		{"list-tuple-string-string", ValType{Kind: VList, Elem: &tupStrStr}, 0, false}, // get-environment's result
		{"own", ValType{Kind: VOwn, Ref: 0}, 0, false},
		{"borrow", ValType{Kind: VBorrow, Ref: 0}, 0, false},
		{"result-variant-err", ValType{Kind: VResult, Err: ptr(ValType{Kind: VVariant, Cases: []VarCase{{Name: "closed"}, {Name: "last-operation", Type: &strT}}})}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, bad := unmodeledInSig(&FuncType{Params: []NamedVal{{Name: "p", Type: c.vt}}})
			if bad != c.bad || (bad && got != c.want) {
				t.Fatalf("param %s: got (%s,%v), want (%s,%v)", c.name, valKindName(got), bad, valKindName(c.want), c.bad)
			}
			// The result position is scanned too.
			got, bad = unmodeledInSig(&FuncType{Result: &c.vt})
			if bad != c.bad || (bad && got != c.want) {
				t.Fatalf("result %s: got (%s,%v), want (%s,%v)", c.name, valKindName(got), bad, valKindName(c.want), c.bad)
			}
		})
	}
}

func ptr(vt ValType) *ValType { return &vt }

// bindingWalker builds a walker positioned at a single canon lower of `iface::export`, whose import
// instance type declares `export` with the given signature, and whose lower is implemented (or not) by
// the host. Driving w.step over the lower's Def is the exact instantiate-time path a real component
// takes; the inputs are synthesized because no implemented lower among the guests' ten carries an
// unmodeled kind (they are all own/list/string/result/variant/tuple), so a real guest cannot witness the
// refusal — only the guard against a future one.
func bindingWalker(sig *FuncType, implemented bool) *walker {
	w := &walker{
		c: &Component{
			Types:   []TypeDef{{Kind: TDInstance, Inst: &InstanceType{Exports: []InstExport{{Name: "make-thing", Func: sig}}}}},
			Imports: []Import{{Name: "test:pkg/iface", Kind: ExternInstance, TypeIndex: 0}},
			Canons:  []Canon{{Kind: CanonLower, FuncIdx: 0}},
			// The type section is the type index space's ordinal 0 here (no interleaved aliases), so the
			// import's TypeIndex 0 maps straight to c.Types[0] — the def stream typeSpaceToTypes reads.
			Defs: []Def{{Space: SpaceType, Section: SectionType, Item: 0}},
		},
		coreSpace: map[Space][]coreDef{},
		compFuncs: []compDef{{fn: &compFunc{stubName: "test:pkg/iface::make-thing"}}},
	}
	if implemented {
		w.wasiHost = map[string]interp.CanonFunc{"test:pkg/iface::make-thing": dummyCanon}
	}
	return w
}

// TestBindingRefusesImplementedLowerCarryingUnmodeledKind is the implemented-import → instantiate refusal:
// a host-filled lower whose bound signature carries a record refuses when the lower is stepped (the
// binding), naming the import and the kind, before any value could be lowered.
func TestBindingRefusesImplementedLowerCarryingUnmodeledKind(t *testing.T) {
	sig := &FuncType{Params: []NamedVal{{Name: "spec", Type: ValType{Kind: VRecord, Fields: []NamedVal{{Name: "n", Type: ValType{Kind: VU32}}}}}}}
	w := bindingWalker(sig, true)
	err := w.step(Def{Space: SpaceCoreFunc, Section: SectionCanon, Item: 0})
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Fatalf("binding error = %v, want ErrUnsupportedForm", err)
	}
	if got := err.Error(); !contains(got, "test:pkg/iface::make-thing") || !contains(got, "record") {
		t.Errorf("binding error %q must name the import and the kind", got)
	}
}

// TestBindingAllowsImplementedLowerOfModeledKind is the negative arm: an implemented lower whose signature
// is fully modeled (string result, as error.to-debug-string) is not refused — it binds, appending a
// lowered core func rather than an error. Without this the refusal could be vacuously firing on every
// implemented lower.
func TestBindingAllowsImplementedLowerOfModeledKind(t *testing.T) {
	sig := &FuncType{Result: ptr(ValType{Kind: VString})}
	w := bindingWalker(sig, true)
	if err := w.step(Def{Space: SpaceCoreFunc, Section: SectionCanon, Item: 0}); err != nil {
		t.Fatalf("modeled implemented lower refused: %v", err)
	}
	if got := len(w.coreSpace[SpaceCoreFunc]); got != 1 {
		t.Fatalf("core func space grew by %d, want 1 (the bound lower)", got)
	}
}

// TestBindingDoesNotRefuseUnimplementedUnmodeledKind is Scott's second point: an *unimplemented* lower
// carrying an unmodeled kind (filesystem-error-code's `option`, the guests' real shape) is NOT refused at
// instantiate — it binds to the refusing stub and refuses at call by name (TestStubHostRefusesByName), so
// the guests keep instantiating. Only an implemented lower refuses at the binding.
func TestBindingDoesNotRefuseUnimplementedUnmodeledKind(t *testing.T) {
	sig := &FuncType{Result: ptr(ValType{Kind: VOption, Elem: ptr(ValType{Kind: VU32})})}
	w := bindingWalker(sig, false)
	if err := w.step(Def{Space: SpaceCoreFunc, Section: SectionCanon, Item: 0}); err != nil {
		t.Fatalf("unimplemented unmodeled-kind lower refused at instantiate: %v", err)
	}
	if got := len(w.coreSpace[SpaceCoreFunc]); got != 1 {
		t.Fatalf("core func space grew by %d, want 1 (the stub lower)", got)
	}
}

// TestBindingRefusesImplementedFilesystemErrorCodeOnRealGuest is the binding refusal end-to-end on
// p3hello's real bytes. filesystem-error-code is bound with result `option` but unimplemented, so the
// guest instantiates and runs (TestP3HelloWorldMatchesWasmtime). Implement it — the shape a future host
// would take — and the binding resolves its signature through the type-index space (typeSpaceToTypes maps
// the import's space ordinal 15 to c.Types[8]) and refuses at instantiate, naming the import and `option`,
// before the guest comes up. This exercises lowerSignature + typeSpaceToTypes on real bytes, which the
// synthesized tests (whose indices are aligned by construction) cannot.
func TestBindingRefusesImplementedFilesystemErrorCodeOnRealGuest(t *testing.T) {
	wasm, err := os.ReadFile("testdata/p3hello.wasm")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(wasm)
	if err != nil {
		t.Fatal(err)
	}
	wh := map[string]interp.CanonFunc{"wasi:filesystem/types::filesystem-error-code": dummyCanon}
	if _, err := c.walkComponent(stubHost, wh); !errors.Is(err, ErrUnsupportedForm) {
		t.Fatalf("binding error = %v, want ErrUnsupportedForm", err)
	} else if got := err.Error(); !contains(got, "filesystem-error-code") || !contains(got, "option") {
		t.Errorf("binding error %q must name the import and the kind", got)
	}
}

// TestExportRefusesValueCarryingUnmodeledKind is the export → Call refusal: an exported function whose
// lifted signature carries a record refuses at Call, naming the export and the kind, before the guest is
// entered. run() carries no values so it never trips; a value-carrying export does. Synthesized for the
// same reason as the binding test — the guests export only `run`.
func TestExportRefusesValueCarryingUnmodeledKind(t *testing.T) {
	in := &Instantiated{
		w: &walker{coreSpace: map[Space][]coreDef{}},
		export: &compInstance{exports: map[string]compDef{
			"make-thing": {fn: &compFunc{sig: &FuncType{Params: []NamedVal{{Name: "spec", Type: ValType{Kind: VRecord, Fields: []NamedVal{{Name: "n", Type: ValType{Kind: VU32}}}}}}}}},
		}},
	}
	err := in.Call("make-thing")
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Fatalf("export Call error = %v, want ErrUnsupportedForm", err)
	}
	if got := err.Error(); !contains(got, "make-thing") || !contains(got, "record") {
		t.Errorf("export error %q must name the export and the kind", got)
	}
}
