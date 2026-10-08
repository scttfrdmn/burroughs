// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// Witnesses for the decoded-type → codec-type bridge (#903).
//
// Four claims, failing for unrelated reasons, which is why they are four tests and not one: the mapping
// is right; the domain is total so a new `ValKind` cannot be silently unclassified; the codec itself
// accepts what the bridge builds; and the bridge's verdict agrees with the pre-existing unmodeled
// predicate except where it is *documented* not to.

// bridgeHeap is a `canon.Heap` over a byte slice with a rising watermark — the same shape as the codec's
// own differential heap, reimplemented here because that one is unexported.
//
// **It exists so the codec is the oracle for this test rather than a second opinion.** A table asserting
// "VList maps to KindList" only checks that the bridge agrees with the table's author; handing the built
// type to `canon.StoreVia` makes the codec's own `size`/`alignment`/`flatten` arms answer, and those are
// what panic on a type the codec cannot carry.
type bridgeHeap struct {
	mem  []byte
	next int
}

func (h *bridgeHeap) Realloc(_, _, align, newSize int) (int, error) {
	if align > 0 {
		h.next = (h.next + align - 1) / align * align
	}
	p := h.next
	if p+newSize > len(h.mem) {
		return 0, fmt.Errorf("bridgeHeap: out of memory at %d+%d of %d", p, newSize, len(h.mem))
	}
	h.next = p + newSize
	return p, nil
}

func (h *bridgeHeap) WriteBytes(ptr int, data []byte) error {
	if ptr < 0 || ptr+len(data) > len(h.mem) {
		return fmt.Errorf("bridgeHeap: write out of range at %d+%d", ptr, len(data))
	}
	copy(h.mem[ptr:], data)
	return nil
}

func (h *bridgeHeap) StoreInt(v uint64, ptr, nbytes int) error {
	if ptr < 0 || ptr+nbytes > len(h.mem) {
		return fmt.Errorf("bridgeHeap: storeInt out of range at %d+%d", ptr, nbytes)
	}
	for i := range nbytes {
		h.mem[ptr+i] = byte(v >> (8 * i))
	}
	return nil
}

// bridgeAccepted is every ValKind the bridge carries, with the codec kind it must produce. Field by
// field rather than by a single equality, because `canon.Type` holds pointers and slices and a `==`
// comparison of two of them compares addresses.
var bridgeAccepted = []struct {
	name string
	in   ValType
	want canon.Kind
}{
	{"bool", ValType{Kind: VBool}, canon.KindBool},
	{"u8", ValType{Kind: VU8}, canon.KindU8},
	{"u16", ValType{Kind: VU16}, canon.KindU16},
	{"u32", ValType{Kind: VU32}, canon.KindU32},
	{"u64", ValType{Kind: VU64}, canon.KindU64},
	{"s8", ValType{Kind: VS8}, canon.KindS8},
	{"s16", ValType{Kind: VS16}, canon.KindS16},
	{"s32", ValType{Kind: VS32}, canon.KindS32},
	{"s64", ValType{Kind: VS64}, canon.KindS64},
	{"f32", ValType{Kind: VF32}, canon.KindF32},
	{"f64", ValType{Kind: VF64}, canon.KindF64},
	{"char", ValType{Kind: VChar}, canon.KindChar},
	{"string", ValType{Kind: VString}, canon.KindString},
	{"list", ValType{Kind: VList, Elem: &ValType{Kind: VU32}}, canon.KindList},
	// **KindRecord, not KindTuple** — `canon.TupleType` despecializes (definitions.py:1133), so the
	// codec has one field-carrying arm. The WIT distinction survives in `ValKind`, which is where it
	// belongs; the ABI does not have it.
	{"tuple", ValType{Kind: VTuple, Elems: []ValType{{Kind: VU32}, {Kind: VString}}}, canon.KindRecord},
	{"record", ValType{Kind: VRecord, Fields: []NamedVal{{Name: "x", Type: ValType{Kind: VU32}}}}, canon.KindRecord},
	{"variant", ValType{Kind: VVariant, Cases: []VarCase{{Name: "a"}, {Name: "b", Type: &ValType{Kind: VU32}}}}, canon.KindVariant},
	{"result", ValType{Kind: VResult, Ok: &ValType{Kind: VU32}}, canon.KindVariant},
	{"own", ValType{Kind: VOwn, Ref: 3}, canon.KindOwn},
	{"borrow", ValType{Kind: VBorrow, Ref: 4}, canon.KindBorrow},
}

// bridgeRefused is every ValKind the bridge refuses. VRef is refused for a different reason than the
// rest and the comment on it says which, because a reader counting "unmodeled kinds" would otherwise
// count one that the codec models perfectly well.
var bridgeRefused = []struct {
	name string
	in   ValType
}{
	// `record` moved to bridgeAccepted when #904 landed the codec arm. What stays refused is an EMPTY
	// one: the model gives it no size (definitions.py:1256 asserts elem_size > 0), so there is no
	// layout to match. Same for an empty tuple, which despecializes to an empty record.
	{"empty-record", ValType{Kind: VRecord}},
	{"empty-tuple", ValType{Kind: VTuple}},
	{"flags", ValType{Kind: VFlags, Labels: []string{"a"}}},
	{"enum", ValType{Kind: VEnum, Labels: []string{"a"}}},
	{"option", ValType{Kind: VOption, Elem: &ValType{Kind: VU32}}},
	{"error-context", ValType{Kind: VErrorContext}},
	{"stream", ValType{Kind: VStream}},
	{"future", ValType{Kind: VFuture}},
	{"unresolved-alias", ValType{Kind: VUnresolvedAlias}},
	// Not an unmodeled KIND: the codec has no trouble with whatever this names. The bridge cannot follow
	// it, because a ValType does not record which index space its Ref belongs to and resolving against the
	// wrong one yields a different type that exists. Resolution is the caller's, at bind.
	{"typeidx-ref", ValType{Kind: VRef, Ref: 1}},
}

func TestBridgeMapsEveryCarryableKindToItsCodecKind(t *testing.T) {
	for _, c := range bridgeAccepted {
		t.Run(c.name, func(t *testing.T) {
			got, err := canonTypeOf(c.in)
			if err != nil {
				t.Fatalf("canonTypeOf(%s) refused a kind it carries: %v", c.name, err)
			}
			if got.Kind != c.want {
				t.Fatalf("canonTypeOf(%s) = %s, want %s", c.name, got.Kind, c.want)
			}
		})
	}

	// The nesting, not just the outer kind. A bridge that returned the right outer kind with a dropped
	// element type would pass every row above and then lower a list of the wrong thing.
	t.Run("list-element-is-carried-through", func(t *testing.T) {
		got, err := canonTypeOf(ValType{Kind: VList, Elem: &ValType{Kind: VString}})
		if err != nil {
			t.Fatalf("list<string> refused: %v", err)
		}
		if got.Elem == nil {
			t.Fatal("list<string> bridged to a list with a nil element type")
		}
		if got.Elem.Kind != canon.KindString {
			t.Fatalf("list<string> element = %s, want string", got.Elem.Kind)
		}
	})

	t.Run("result-despecializes-to-the-models-case-names", func(t *testing.T) {
		got, err := canonTypeOf(ValType{Kind: VResult, Ok: &ValType{Kind: VU32}, Err: &ValType{Kind: VString}})
		if err != nil {
			t.Fatalf("result<u32,string> refused: %v", err)
		}
		// definitions.py:1136 despecializes result to a variant with cases "ok"/"error".
		if len(got.Cases) != 2 || got.Cases[0].Name != "ok" || got.Cases[1].Name != "error" {
			t.Fatalf("result bridged to cases %+v, want ok/error", got.Cases)
		}
		if got.Cases[0].Type == nil || got.Cases[0].Type.Kind != canon.KindU32 {
			t.Fatalf("result ok payload = %+v, want u32", got.Cases[0].Type)
		}
		if got.Cases[1].Type == nil || got.Cases[1].Type.Kind != canon.KindString {
			t.Fatalf("result error payload = %+v, want string", got.Cases[1].Type)
		}
	})

	t.Run("own-and-borrow-carry-the-resource-type-id", func(t *testing.T) {
		got, err := canonTypeOf(ValType{Kind: VOwn, Ref: 7})
		if err != nil {
			t.Fatalf("own refused: %v", err)
		}
		if got.RT != 7 {
			t.Fatalf("own<rt=7> bridged to RT %d, want 7", got.RT)
		}
	})

	t.Run("a-refusal-names-the-kind", func(t *testing.T) {
		for _, c := range bridgeRefused {
			_, err := canonTypeOf(c.in)
			if err == nil {
				t.Fatalf("canonTypeOf(%s) succeeded; it is not carryable", c.name)
			}
			if !errors.Is(err, ErrUnsupportedForm) {
				t.Fatalf("canonTypeOf(%s) error is not ErrUnsupportedForm: %v", c.name, err)
			}
			// The kind's own spelling must appear. A refusal reading only "unsupported" is the failure the
			// enumerated kind names exist to prevent.
			want := valKindName(c.in.Kind)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("canonTypeOf(%s) refusal %q does not name %q", c.name, err, want)
			}
		}
	})

	// An unmodeled kind NESTED inside a carryable one must refuse, naming the inner kind rather than the
	// outer one — `list<record>` is refused because of the record, and saying "list" would send a reader
	// to the wrong place.
	t.Run("a-nested-refusal-names-the-inner-kind", func(t *testing.T) {
		_, err := canonTypeOf(ValType{Kind: VList, Elem: &ValType{Kind: VRecord}})
		if err == nil {
			t.Fatal("list<record> was carried; record is not modeled")
		}
		if !strings.Contains(err.Error(), "record") {
			t.Fatalf("list<record> refusal %q does not name record", err)
		}
	})
}

// TestBridgeClassifiesEveryValKind is the totality control: every ValKind in the enum is either carried
// or refused by name, and the domain is **derived from the enum's own extent** rather than listed.
//
// A new kind added to `ValKind` without a decision about the bridge fails here. That is the failure the
// `valKindName` completion (#885) was dug for one level over: an enum that grows past its switch returns
// a plausible answer for a kind nobody considered.
func TestBridgeClassifiesEveryValKind(t *testing.T) {
	classified := map[ValKind]string{}
	for _, c := range bridgeAccepted {
		classified[c.in.Kind] = "carried"
	}
	for _, c := range bridgeRefused {
		classified[c.in.Kind] = "refused"
	}

	// The extent comes from valKindName's own domain: every kind it names is a declared kind, and the
	// first number it renders as `valkind(N)` is one past the end. That derivation is why adding a kind
	// to the enum — and to valKindName, which its own control already forces — widens this domain too.
	var n int
	for k := range 256 {
		if strings.HasPrefix(valKindName(ValKind(k)), "valkind(") {
			n = k
			break
		}
	}
	if n == 0 {
		t.Fatal("derived a ValKind extent of 0; valKindName named no kind, so this control has no domain")
	}
	// A floor against the derivation silently collapsing: the enum has well over a dozen members, so a
	// domain of two or three means the extent walk stopped early rather than that the enum shrank.
	if n < 20 {
		t.Fatalf("derived a ValKind extent of %d, which is below the floor; the enum has more kinds than "+
			"that, so the derivation is wrong rather than the enum small", n)
	}

	var missing []string
	for k := range n {
		if _, ok := classified[ValKind(k)]; !ok {
			missing = append(missing, fmt.Sprintf("%s (%d)", valKindName(ValKind(k)), k))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d of %d ValKind(s) are classified by neither bridgeAccepted nor bridgeRefused: %s\n"+
			"A kind the bridge has not been decided about must be added to one of the two tables, which is "+
			"the decision. It must not be left out: canonTypeWalk's default arm refuses it, so leaving it "+
			"unlisted means the refusal is untested and the choice unrecorded.",
			len(missing), n, strings.Join(missing, ", "))
	}
	t.Logf("BRIDGE-DOMAIN %d ValKind(s): %d carried, %d refused", n, len(bridgeAccepted), len(bridgeRefused))
}

// TestTheCodecAcceptsEveryTypeTheBridgeBuilds hands each built type to the codec and stores a value of
// it, so the codec's own size/alignment/flatten arms are the oracle.
//
// **Its limit, stated rather than left to a coverage report**: it covers only the kinds for which
// `canon` has an exported **value** constructor, and three carryable kinds do not have one.
//
//   - `f32`/`f64` — the codec sizes, aligns and flattens them, but there is no `canon.F32`/`F64`, so no
//     value of one can be built to store. (Found by writing `canon.F64` here and failing to compile,
//     which is the honest way to discover it.)
//   - `tuple` — a type constructor with no value constructor on purpose: its element lowering is #904's.
//   - `own`/`borrow` — need an instance handle table the codec builds internally.
//
// Those are covered by the mapping test alone, which is weaker because it compares the bridge against a
// table rather than against the codec. The gap closes for each when its value side lands.
func TestTheCodecAcceptsEveryTypeTheBridgeBuilds(t *testing.T) {
	cases := []struct {
		name string
		vt   ValType
		val  func(canon.Type) (canon.Value, error)
	}{
		{"u32", ValType{Kind: VU32}, func(canon.Type) (canon.Value, error) { return canon.U32(7), nil }},
		{"u64", ValType{Kind: VU64}, func(canon.Type) (canon.Value, error) { return canon.U64(9), nil }},
		{"s32", ValType{Kind: VS32}, func(canon.Type) (canon.Value, error) { return canon.S32(-3), nil }},
		{"bool", ValType{Kind: VBool}, func(canon.Type) (canon.Value, error) { return canon.Bool(true), nil }},
		{"char", ValType{Kind: VChar}, func(canon.Type) (canon.Value, error) { return canon.Char('é') }},
		{"string", ValType{Kind: VString}, func(canon.Type) (canon.Value, error) { return canon.Str("hello"), nil }},
		{
			"list<u32>",
			ValType{Kind: VList, Elem: &ValType{Kind: VU32}},
			func(t canon.Type) (canon.Value, error) { return canon.List(*t.Elem, canon.U32(1), canon.U32(2)) },
		},
		{
			"list<string>",
			ValType{Kind: VList, Elem: &ValType{Kind: VString}},
			func(t canon.Type) (canon.Value, error) { return canon.List(*t.Elem, canon.Str("a"), canon.Str("bb")) },
		},
		{
			"empty list<u32>",
			ValType{Kind: VList, Elem: &ValType{Kind: VU32}},
			func(t canon.Type) (canon.Value, error) { return canon.List(*t.Elem) },
		},
		{
			"result<u32,_>",
			ValType{Kind: VResult, Ok: &ValType{Kind: VU32}},
			func(t canon.Type) (canon.Value, error) { p := canon.U32(5); return canon.Variant(t, "ok", &p) },
		},
		{
			"variant",
			ValType{Kind: VVariant, Cases: []VarCase{{Name: "a"}, {Name: "b", Type: &ValType{Kind: VU32}}}},
			func(t canon.Type) (canon.Value, error) { return canon.Variant(t, "a", nil) },
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ct, err := canonTypeOf(c.vt)
			if err != nil {
				t.Fatalf("bridge refused %s: %v", c.name, err)
			}
			v, err := c.val(ct)
			if err != nil {
				t.Fatalf("could not build a %s value against the bridged type: %v", c.name, err)
			}
			h := &bridgeHeap{mem: make([]byte, 1<<16), next: 64}
			// StoreVia reaches size/alignment/store for the type; a kind the codec cannot carry panics in
			// one of them, which is the thing being checked. A returned error is a different outcome and is
			// reported as one.
			if err := canon.StoreVia(h, v, 0); err != nil {
				t.Fatalf("the codec refused to store a %s the bridge built: %v", c.name, err)
			}
		})
	}

	if len(cases) < 10 {
		t.Fatalf("this control ran %d case(s); it is a vacuity floor against the table being emptied", len(cases))
	}
}

// TestCanonSigUnmodeledIsTheExportCallRefusal covers the signature-level projection that replaced
// `unmodeledInSig` in `CallValuesCtx`, including the one case where the two genuinely differ.
func TestCanonSigUnmodeledIsTheExportCallRefusal(t *testing.T) {
	t.Run("a-u32-signature-is-carryable", func(t *testing.T) {
		ft := &FuncType{Params: []NamedVal{{Name: "x", Type: ValType{Kind: VU32}}}, Result: &ValType{Kind: VU32}}
		if k, bad := canonSigUnmodeled(ft); bad {
			t.Fatalf("a u32->u32 signature was refused, naming %s", valKindName(k))
		}
	})

	t.Run("nil-signature-is-not-a-refusal", func(t *testing.T) {
		if _, bad := canonSigUnmodeled(nil); bad {
			t.Fatal("a nil signature refused; the caller checks for nil separately and reports it differently")
		}
	})

	t.Run("an-unmodeled-parameter-is-named", func(t *testing.T) {
		ft := &FuncType{Params: []NamedVal{
			{Name: "ok", Type: ValType{Kind: VU32}},
			{Name: "bad", Type: ValType{Kind: VRecord}},
		}}
		k, bad := canonSigUnmodeled(ft)
		if !bad {
			t.Fatal("a record parameter was accepted")
		}
		if k != VRecord {
			t.Fatalf("the refusal named %s, want record", valKindName(k))
		}
	})

	t.Run("an-unmodeled-result-is-named", func(t *testing.T) {
		ft := &FuncType{Result: &ValType{Kind: VFlags}}
		k, bad := canonSigUnmodeled(ft)
		if !bad || k != VFlags {
			t.Fatalf("a flags result gave (%s, %v), want (flags, true)", valKindName(k), bad)
		}
	})

	// The behaviour change this replacement buys, stated as a test rather than as a claim in a comment.
	//
	// A parameter that is a *reference* to a record: `unmodeledInSig` counts it modeled, because its
	// default arm treats VRef as a handle and never follows the reference. The bridge refuses. Before
	// #903 a lift signature was not resolved either, so this shape reached the `VU32` guard and was
	// refused there, reporting a reference where the real obstacle was a record.
	t.Run("a-reference-is-where-the-two-differ", func(t *testing.T) {
		ref := &FuncType{Params: []NamedVal{{Name: "x", Type: ValType{Kind: VRef, Ref: 4}}}}
		if _, bad := unmodeledInSig(ref); bad {
			t.Fatal("unmodeledInSig refused a bare VRef; this subtest's premise is that it does not, so " +
				"the predicate has changed and the comment at the CallValuesCtx call site is now wrong")
		}
		k, bad := canonSigUnmodeled(ref)
		if !bad {
			t.Fatal("the bridge accepted a bare VRef; it cannot know which index space the ref belongs to")
		}
		if k != VRef {
			t.Fatalf("the bridge's refusal named %s, want the reference itself", valKindName(k))
		}

		// And once resolved — which is what the bind sites now do — the obstacle is named properly.
		//
		// **The obstacle behind the reference used to be a `record`, and had to change**: #904 gave the
		// codec a record arm, so a reference to a record now resolves to a type the bridge *carries*,
		// and the subtest would have been asserting that a working type is an obstacle. `flags` is the
		// replacement — unmodeled for a reason unrelated to which compound forms have landed, which is
		// what a specimen holding this assertion needs. (The same lesson as `componentlistconv_test`'s
		// `tuple`-to-`char` swap, one package over and in the same slice: a specimen chosen because it
		// happens to be unimplemented expires when it gets implemented.)
		at := sliceTypeAt([]TypeDef{
			{},
			{},
			{},
			{},
			{Kind: TDVal, Val: ValType{Kind: VFlags, Labels: []string{"a", "b"}}},
		})
		resolved := resolveFunc(ref, at)
		k, bad = canonSigUnmodeled(resolved)
		if !bad || k != VFlags {
			t.Fatalf("after resolution the refusal gave (%s, %v), want (flags, true) — the point of "+
				"resolving at bind is that the refusal names the real obstacle", valKindName(k), bad)
		}
	})
}

// TestTheBridgeAndTheUnmodeledPredicateAgree pins the two verdicts together.
//
// **There are two walks over the same question and this control is why that is tolerable.**
// `callvalues.go` records the rule — *"two refusals for one condition drift apart, and the one nobody
// reads becomes the one that is wrong"* — and the honest repair is one authority. It is not taken in
// #903's slice because `unmodeledValKind` is consulted at **instantiate** for every implemented lower,
// so changing its verdict changes which components load, and that blast radius needs its own witness
// rather than riding a bridge slice. So they stay two, and the divergence is pinned instead of assumed.
//
// **One divergence is expected and declared.** `VRef`: the predicate counts it modeled (its default arm
// says so — a handle is an i32 whatever it references), the bridge refuses it because it cannot know the
// index space. After this slice both resolution sites inline their VRefs, so a VRef reaching either is a
// caller bug and the two answering differently about it costs nothing — but it is declared here so that
// the *next* divergence fails this test instead of hiding behind it.
func TestTheBridgeAndTheUnmodeledPredicateAgree(t *testing.T) {
	declaredDivergence := map[ValKind]string{
		VRef: "the predicate counts a typeidx reference modeled (a handle is an i32); the bridge cannot " +
			"follow one because a ValType does not record its index space",
		// Declared when #904 gave the codec its record arm. The bridge builds a record now; the
		// predicate still refuses one, and **that asymmetry is deliberate rather than lag**.
		//
		// `unmodeledValKind` is consulted at **instantiate**, for every implemented lower, so widening
		// its verdict changes which components load — a different blast radius from the bridge's, which
		// is consulted at the call. `callvalues.go` records the rule this follows: the predicate is not
		// retired in the slice that outgrows it, because changing what instantiates is its own slice
		// with its own witness.
		//
		// The consequence for a reader: a component whose *signature* carries a record still fails to
		// instantiate, and a record that reaches the codec does so through a `task.return` result or a
		// value built in-process. Lifting that restriction is the next slice, not this one.
		VRecord: "the bridge builds a record (#904's codec arm); the predicate still refuses one, and " +
			"is consulted at instantiate where widening it changes which components load — its own " +
			"slice, per callvalues.go's rule about not retiring it in the slice that outgrows it",
		// **There is no VTuple entry, and the history of this one is the useful part.**
		//
		// It was declared twice and is now retired, each time on a measurement this control forced:
		//
		//  1. First declared on the theory that despecialization would make the two disagree about a
		//     tuple the way they do about a record. False — `unmodeledValKind`'s tuple arm recurses
		//     into the elements and otherwise falls through as *modeled*, so they agree on every
		//     non-empty tuple. The control's stale-declaration arm caught it.
		//  2. Re-declared on the real divergence: `tuple<>`, which the bridge refused (no layout in the
		//     model) and the predicate accepted (a loop over zero elements finds nothing to refuse).
		//  3. Retired, because the divergence was **closed** rather than merely described. The decoder
		//     now refuses an empty tuple outright, matching `wasm-tools validate`, and the predicate
		//     refuses one too — so nothing disagrees and there is nothing to declare. A divergence
		//     nobody needs is one a later reader takes for intentional.
	}

	all := append([]ValType(nil), func() []ValType {
		var out []ValType
		for _, c := range bridgeAccepted {
			out = append(out, c.in)
		}
		for _, c := range bridgeRefused {
			out = append(out, c.in)
		}
		return out
	}()...)

	var compared, diverged int
	for _, vt := range all {
		_, bridgeBad := canonUnmodeled(vt)
		_, predBad := unmodeledValKind(vt, 0)
		if bridgeBad == predBad {
			compared++
			continue
		}
		diverged++
		why, declared := declaredDivergence[vt.Kind]
		if !declared {
			t.Fatalf("%s: the bridge says unmodeled=%v and unmodeledValKind says %v, and this divergence "+
				"is not declared. Either the two have drifted — which is the thing this control exists to "+
				"catch — or the new divergence is intended and belongs in declaredDivergence with its "+
				"reason.", valKindName(vt.Kind), bridgeBad, predBad)
		}
		t.Logf("DECLARED-DIVERGENCE %s: %s", valKindName(vt.Kind), why)
	}

	// Both floors matter and for different reasons. Zero comparisons means the loop did not run and the
	// agreement is unmeasured; zero divergences means the declared one stopped happening, which makes the
	// declaration stale and the next reader believe a divergence exists that does not.
	if compared == 0 {
		t.Fatal("compared 0 kind(s): this control measured nothing")
	}
	if diverged != len(declaredDivergence) {
		t.Fatalf("%d divergence(s) occurred but %d are declared; a declaration nothing exercises is a "+
			"claim about the code that has stopped being true", diverged, len(declaredDivergence))
	}
	t.Logf("BRIDGE-AGREEMENT %d kind(s) agree, %d declared divergence(s)", compared, diverged)
}
