// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The binding/export refusal (ADR 0084 / #720, Scott's ruling): a value of a kind the Canonical ABI
// codec cannot marshal — error-context, since ADR 0116 gave the codec record/flags/enum/option — is
// refused **by name at the earliest point it would be marshaled**. An *implemented* import (a host lower
// that would run) refuses at **instantiate** (the binding); an *unimplemented* import keeps refusing at
// **call** through the stub (TestStubHostRefusesByName, unchanged); an *exported* function refuses at
// **Call**. `tuple`, `record`, `result`, `variant`, `option`, `flags`, `enum`, and every
// scalar/string/handle are modeled and pass — a modeled container is scanned into, so
// `list<tuple<string,string>>` (get-environment's empty result) is clean while `tuple<error-context>` is
// not.
//
// **The specimens moved from `record` and `option` to `error-context` with ADR 0116**, which made those
// two modeled. `error-context` is unmodeled for a reason unrelated to which compound forms have landed,
// which is what a specimen holding a refusal needs: one chosen because it happened to be unimplemented
// expires when it is implemented, as `record` and `option` just did.

func dummyCanon(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) { return nil, nil }

// TestUnmodeledInSigDiscriminates pins the scan's boundary: the unmodeled kinds are caught (directly, and
// through the modeled containers that nest them), and the modeled shapes — crucially get-environment's
// `list<tuple<string,string>>` — are not. This is the logic a hand check gets wrong (the recursion, and
// which kinds are in the set), so it is asserted directly rather than left to the two path tests.
func TestUnmodeledInSigDiscriminates(t *testing.T) {
	rec := ValType{Kind: VRecord, Fields: []NamedVal{{Name: "x", Type: ValType{Kind: VU32}}}}
	strT := ValType{Kind: VString}
	ecT := ValType{Kind: VErrorContext}
	tupStrStr := ValType{Kind: VTuple, Elems: []ValType{strT, strT}}
	cases := []struct {
		name string
		vt   ValType
		want ValKind
		bad  bool
	}{
		{"error-context", ecT, VErrorContext, true},
		// The modeled containers are scanned into, ADR 0116's among them: each nests the unmodeled kind
		// and must name it rather than itself.
		{"tuple-of-error-context", ValType{Kind: VTuple, Elems: []ValType{ecT}}, VErrorContext, true},
		{"record-of-error-context", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "e", Type: ecT}}}, VErrorContext, true},
		{"option-of-error-context", ValType{Kind: VOption, Elem: &ecT}, VErrorContext, true},
		{"list-of-option-of-error-context", ValType{Kind: VList, Elem: ptr(ValType{Kind: VOption, Elem: &ecT})}, VErrorContext, true},
		// A flags or enum the codec cannot lay out stays refused, by the codec's own constructor.
		{"flags-33", ValType{Kind: VFlags, Labels: nLabels(33)}, VFlags, true},
		{"enum-duplicate", ValType{Kind: VEnum, Labels: []string{"a", "a"}}, VEnum, true},
		{"empty-record", ValType{Kind: VRecord}, VRecord, true},
		// Modeled — must NOT be flagged. The first five were refused until ADR 0116.
		{"record", rec, 0, false},
		{"option", ValType{Kind: VOption, Elem: &strT}, 0, false},
		{"flags", ValType{Kind: VFlags, Labels: []string{"a", "b"}}, 0, false},
		{"enum", ValType{Kind: VEnum, Labels: []string{"a", "b"}}, 0, false},
		{"list-of-option", ValType{Kind: VList, Elem: ptr(ValType{Kind: VOption, Elem: &strT})}, 0, false},
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

// nLabels is n distinct labels, "l0" … "l<n-1>".
func nLabels(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "l" + strconv.Itoa(i)
	}
	return out
}

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
// a host-filled lower whose bound signature carries an unmodeled kind — an `error-context` inside a record,
// so the scan into ADR 0116's record arm is on the path — refuses when the lower is stepped (the binding),
// naming the import and the kind, before any value could be lowered.
func TestBindingRefusesImplementedLowerCarryingUnmodeledKind(t *testing.T) {
	sig := &FuncType{Params: []NamedVal{{Name: "spec", Type: ValType{Kind: VRecord, Fields: []NamedVal{{Name: "e", Type: ValType{Kind: VErrorContext}}}}}}}
	w := bindingWalker(sig, true)
	err := w.step(Def{Space: SpaceCoreFunc, Section: SectionCanon, Item: 0})
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Fatalf("binding error = %v, want ErrUnsupportedForm", err)
	}
	if got := err.Error(); !contains(got, "test:pkg/iface::make-thing") || !contains(got, "error-context") {
		t.Errorf("binding error %q must name the import and the kind", got)
	}
}

// TestBindingAdmitsImplementedLowerOfADR0116Kinds is the narrowing ADR 0116 made, stated as a test: an
// implemented lower whose signature carries a record, an option, flags or an enum used to refuse at
// instantiate, and now binds, because the codec carries all four.
func TestBindingAdmitsImplementedLowerOfADR0116Kinds(t *testing.T) {
	u32 := ValType{Kind: VU32}
	for _, c := range []struct {
		name string
		vt   ValType
	}{
		{"record", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "n", Type: u32}}}},
		{"option", ValType{Kind: VOption, Elem: &u32}},
		{"flags", ValType{Kind: VFlags, Labels: []string{"read", "write"}}},
		{"enum", ValType{Kind: VEnum, Labels: []string{"io", "pipe"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := bindingWalker(&FuncType{Params: []NamedVal{{Name: "p", Type: c.vt}}}, true)
			if err := w.step(Def{Space: SpaceCoreFunc, Section: SectionCanon, Item: 0}); err != nil {
				t.Fatalf("implemented lower carrying %s refused: %v", c.name, err)
			}
			if got := len(w.coreSpace[SpaceCoreFunc]); got != 1 {
				t.Fatalf("core func space grew by %d, want 1 (the bound lower)", got)
			}
		})
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
// carrying an unmodeled kind is NOT refused at instantiate — it binds to the refusing stub and refuses at
// call by name (TestStubHostRefusesByName), so the guests keep instantiating. Only an implemented lower
// refuses at the binding. (The specimen was filesystem-error-code's `option` until ADR 0116 modeled it.)
func TestBindingDoesNotRefuseUnimplementedUnmodeledKind(t *testing.T) {
	sig := &FuncType{Result: ptr(ValType{Kind: VOption, Elem: ptr(ValType{Kind: VErrorContext})})}
	w := bindingWalker(sig, false)
	if err := w.step(Def{Space: SpaceCoreFunc, Section: SectionCanon, Item: 0}); err != nil {
		t.Fatalf("unimplemented unmodeled-kind lower refused at instantiate: %v", err)
	}
	if got := len(w.coreSpace[SpaceCoreFunc]); got != 1 {
		t.Fatalf("core func space grew by %d, want 1 (the stub lower)", got)
	}
}

// TestBindingAdmitsImplementedFilesystemErrorCodeOnRealGuest is the binding end-to-end on p3hello's real
// bytes. filesystem-error-code returns `option<error-code>`; it is unimplemented, so the guest instantiates
// and runs (TestP3HelloWorldMatchesWasmtime). Implement it — the shape a future host would take — and the
// binding resolves its signature through the type-index space (typeSpaceToTypes maps the import's space
// ordinal 15 to c.Types[8]). That exercises lowerSignature + typeSpaceToTypes on real bytes, which the
// synthesized tests (whose indices are aligned by construction) cannot.
//
// **It refused at instantiate until ADR 0116**, naming `option`, and that was this test's assertion. The
// codec carries `option` now, so the same binding admits it: the signature is found, its result is the
// option, and the walk completes. It was named `TestBindingRefusesImplementedFilesystemErrorCodeOnRealGuest`.
func TestBindingAdmitsImplementedFilesystemErrorCodeOnRealGuest(t *testing.T) {
	wasm, err := os.ReadFile("testdata/p3hello.wasm")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(wasm)
	if err != nil {
		t.Fatal(err)
	}
	const name = "wasi:filesystem/types::filesystem-error-code"
	wh := map[string]interp.CanonFunc{name: dummyCanon}
	// The production resource types, since ADR 0118: the import's `err: borrow<error>` must name a resource
	// the host implements, and an empty source implements none, which no real host is.
	w, err := c.walkComponent(stubHost, wh, nil, nil, NewHost(io.Discard, io.Discard, nil).resourceSource())
	if err != nil {
		t.Fatalf("an implemented filesystem-error-code refused at instantiate: %v", err)
	}
	// The signature the binding checked is the real one, reached through the space-to-section map: a nil
	// here would mean the check had nothing to scan, which is the vacuous pass this asserts against.
	var sig *FuncType
	for _, imp := range c.Imports {
		if strings.HasPrefix(imp.Name, "wasi:filesystem/types@") {
			sig = w.lowerSignature(imp.Name + "::filesystem-error-code")
		}
	}
	if sig == nil || sig.Result == nil || sig.Result.Kind != VOption {
		t.Fatalf("filesystem-error-code's resolved signature = %+v, want a result of option", sig)
	}
}

// TestExportRefusesValueCarryingUnmodeledKind is the export → Call refusal: an exported function whose
// lifted signature carries an unmodeled kind refuses at Call, naming the export and the kind, before the
// guest is entered. run() carries no values so it never trips; a value-carrying export does. Synthesized
// for the same reason as the binding test — the guests export only `run`. The specimen is an
// `error-context` in a record field; it was a bare record until ADR 0116 modeled records.
func TestExportRefusesValueCarryingUnmodeledKind(t *testing.T) {
	in := &Instantiated{
		w: &walker{coreSpace: map[Space][]coreDef{}},
		export: &compInstance{exports: map[string]compDef{
			"make-thing": {fn: &compFunc{sig: &FuncType{Params: []NamedVal{{Name: "spec", Type: ValType{Kind: VRecord, Fields: []NamedVal{{Name: "e", Type: ValType{Kind: VErrorContext}}}}}}}}},
		}},
	}
	err := in.Call("make-thing")
	if !errors.Is(err, ErrUnsupportedForm) {
		t.Fatalf("export Call error = %v, want ErrUnsupportedForm", err)
	}
	if got := err.Error(); !contains(got, "make-thing") || !contains(got, "error-context") {
		t.Errorf("export error %q must name the export and the kind", got)
	}
}
