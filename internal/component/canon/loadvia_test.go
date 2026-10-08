// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"strings"
	"testing"
)

// twos returns v's two's-complement bits, which is how `Value` stores a signed integer.
//
// A function and not `uint64(int64(-3))` written inline: that is a **constant** conversion of a negative
// value and does not compile, whatever the branch. The same shape as the 32-bit `int(ReallocI32Max)`
// repair one slice back — a conversion that must happen at runtime cannot be spelled as a constant.
func twos(v int64) uint64 { return uint64(v) }

// Witnesses for [LoadVia] and [LoadListAt] — the composable load and the header-reading list framing
// (#902), added so `task.return`'s list arm could inject an element load the way the lowering side
// injects `StoreVia`.

// TestLoadViaIsStoreViasMirrorOverTheSameKinds checks the claim `LoadVia`'s doc comment makes, rather
// than leaving it as a comment: **the two are composable over the same set of kinds.**
//
// The claim matters because asymmetry here is invisible until it bites. A kind `StoreVia` writes and
// `LoadVia` cannot read is a value this engine hands a guest and cannot read back — which makes a round
// trip untestable, and makes the untestability the thing nobody notices. So the domain is **derived**
// from the kind enum's own extent and each kind is put to both functions, rather than a list being
// maintained here.
func TestLoadViaIsStoreViasMirrorOverTheSameKinds(t *testing.T) {
	// The enum's extent, asked rather than written: the first number that renders as `kind(N)` is one
	// past the end. The same derivation `canonSigUnmodeled`'s control uses one package up.
	var extent int
	for k := range 256 {
		if strings.HasPrefix(Kind(k).String(), "kind(") {
			extent = k
			break
		}
	}
	if extent < 15 {
		t.Fatalf("derived a Kind extent of %d, below the floor; the enum has more kinds than that, so "+
			"the derivation is wrong rather than the enum small", extent)
	}

	// A representative value per kind. Only the kinds a `Value` can actually hold are built; the two
	// wrapper-minted kinds (future, stream) must never reach the codec at all, which is why they are
	// named here as excluded rather than silently skipped.
	sample := func(k Kind) (Value, Type, bool) {
		t := Type{Kind: k}
		switch k {
		case KindBool:
			return Bool(true), t, true
		case KindU8, KindU16, KindU32, KindU64:
			return Value{Type: t, u: 7}, t, true
		case KindS8, KindS16, KindS32, KindS64:
			return Value{Type: t, u: twos(-3)}, t, true
		case KindChar:
			c, err := Char('x')
			return c, t, err == nil
		case KindString:
			return Str("hi"), t, true
		case KindList:
			e := Type{Kind: KindU32}
			lt := Type{Kind: KindList, Elem: &e}
			v, err := List(e, U32(1), U32(2))
			return v, lt, err == nil
		case KindFuture, KindStream:
			// Wrapper-minted, never codec-lowered. `canon`'s own doc says they must not reach the Kind
			// switches, so excluding them here is the documented contract and not a convenience.
			return Value{}, t, false
		default:
			// f32/f64, variant, own/borrow, tuple: either not composable in both directions or needing a
			// type this helper cannot build generically. Handled by the asymmetry check below, which
			// asks both functions rather than needing a value.
			return Value{}, t, false
		}
	}

	var both, neither, asymmetric []string
	for k := range extent {
		kind := Kind(k)
		v, ty, buildable := sample(kind)

		h := newHeap(1 << 12)
		ptr, err := h.Realloc(0, 0, 8, 64)
		if err != nil {
			t.Fatalf("heap realloc: %v", err)
		}

		// A kind with no buildable sample is still asked: hand `StoreVia` a zero-payload value of the
		// kind and see whether it refuses. A refusal naming heap-composability is the answer; the probe
		// cannot distinguish that from a payload-shaped failure, which is why the kinds it matters for
		// (`own`, `variant`) are the ones carrying a documented allowance below.
		probe := v
		if !buildable {
			probe = Value{Type: ty}
		}
		storeOK := StoreVia(h, probe, ptr) == nil

		_, lerr := LoadVia(h, ty, ptr)
		loadOK := lerr == nil

		switch {
		case storeOK && loadOK:
			both = append(both, kind.String())
		case !storeOK && !loadOK:
			neither = append(neither, kind.String())
		default:
			asymmetric = append(asymmetric, kind.String())
		}
	}

	// **The known asymmetries, keyed by kind name with the reason each is allowed.**
	//
	// An allow-set rather than an assertion that there are none, because `LoadVia`'s first doc comment
	// claimed there were none and this control found that false on its first run. Keyed by **content**
	// (the kind's own name) and not by position or count, so reordering the enum cannot silently move an
	// allowance onto a different kind.
	//
	// The two standings are different and are recorded as different, because an exemption that does not
	// say why is an exemption nobody can retire:
	//
	//   - `own` is structural: the store side writes a handle the component layer minted, and reading one
	//     back needs that layer's handle table, which this package cannot reach by design.
	//   - `variant` is unwritten, not impossible. Its mirror is a `LoadVariant` with an injected element
	//     load; it arrives with the consumer that needs one.
	allowedAsymmetric := map[string]string{
		"own":     "the store side writes a component-minted handle; lifting one needs the instance's handle table, which canon cannot reach",
		"variant": "LoadVariant is unwritten and declined on spec — it arrives with its consumer",
	}
	for _, k := range asymmetric {
		if _, ok := allowedAsymmetric[k]; !ok {
			t.Errorf("%s is composable in one direction only, and is not one of the two documented "+
				"exceptions. StoreVia and LoadVia are mirrors apart from those: a kind this engine can "+
				"hand a guest and cannot read back makes a round trip untestable, and untestability is "+
				"the property nobody notices. Either write the missing side or document the asymmetry "+
				"with its reason in LoadVia's doc comment and here", k)
		}
	}
	// And the other direction: an allowance that no longer applies is a control looking away from
	// nothing. *An exemption teaches an instrument to look away*, so a stale one has to fail too —
	// otherwise writing `LoadVariant` would leave this map quietly excusing a kind that needs no excuse.
	for k, why := range allowedAsymmetric {
		found := false
		for _, a := range asymmetric {
			if a == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is allowed to be one-directional (%q) but is no longer asymmetric. Delete the "+
				"allowance: an exemption for a condition that has been fixed is the instrument looking "+
				"away from nothing", k, why)
		}
	}
	// A floor on the derivation rather than on the result: an empty `both` means the probe stopped
	// reaching the functions, which is the failure mode a derived domain is prone to.
	if len(both) < 10 {
		t.Errorf("only %d kind(s) are composable in both directions (%v); the integers, bool, char, "+
			"string and list are expected, so either an arm was removed or this probe stopped reaching "+
			"them", len(both), both)
	}
	t.Logf("COMPOSABLE-BOTH %d: %v", len(both), both)
	t.Logf("COMPOSABLE-NEITHER %d: %v", len(neither), neither)
}

// TestLoadViaRoundTripsEveryCompositeItAccepts is the behavioural half: a value stored through `StoreVia`
// and read back through `LoadVia` is the value that went in. The symmetry test above says the two accept
// the same kinds; this says they agree about what the bytes mean.
func TestLoadViaRoundTripsEveryCompositeItAccepts(t *testing.T) {
	u32 := Type{Kind: KindU32}
	nested := Type{Kind: KindList, Elem: &u32}

	three, err := List(u32, U32(10), U32(20), U32(30))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	inner, err := List(u32, U32(7))
	if err != nil {
		t.Fatalf("List(inner): %v", err)
	}
	outer, err := List(nested, inner)
	if err != nil {
		t.Fatalf("List(outer): %v", err)
	}
	empty, err := List(u32)
	if err != nil {
		t.Fatalf("List(empty): %v", err)
	}

	for _, c := range []struct {
		name string
		v    Value
	}{
		{"u32", U32(42)},
		{"s32 negative", Value{Type: Type{Kind: KindS32}, u: twos(-5)}},
		{"bool", Bool(true)},
		{"string", Str("burroughs")},
		{"list<u32>", three},
		// The empty list is the case whose type cannot be recovered from its contents, so a round trip
		// that lost the element type would still look right by length.
		{"empty list<u32>", empty},
		// Nesting, so the recursion in both directions is exercised rather than extrapolated.
		{"list<list<u32>>", outer},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHeap(1 << 14)
			ptr, rerr := h.Realloc(0, 0, 8, 128)
			if rerr != nil {
				t.Fatalf("heap realloc: %v", rerr)
			}
			if serr := StoreVia(h, c.v, ptr); serr != nil {
				t.Fatalf("StoreVia: %v", serr)
			}
			got, lerr := LoadVia(h, c.v.Type, ptr)
			if lerr != nil {
				t.Fatalf("LoadVia: %v", lerr)
			}
			// **The type is compared structurally**, not by kind: a `list<u32>` read back as a
			// `list<string>` of the right length is exactly the defect the structural comparison exists
			// for, and a kind check here would pass it.
			if !TypeEqual(got.Type, c.v.Type) {
				t.Fatalf("round-tripped as %s, want %s", got.Type, c.v.Type)
			}
			if !valuesEqualForTest(got, c.v) {
				t.Errorf("round-tripped %v, want %v", got, c.v)
			}
		})
	}
}

// valuesEqualForTest compares two values by payload, recursing into a list.
//
// Deliberately **not** an exported `Value.Equal`: nothing in the engine needs value equality, and adding
// it would be speculative API with no consumer (`deadcode` would say so). A test helper is the honest
// scope — it exists for this round trip and says so.
func valuesEqualForTest(a, b Value) bool {
	if !TypeEqual(a.Type, b.Type) {
		return false
	}
	switch a.Type.Kind {
	case KindString:
		as, _ := a.Str()
		bs, _ := b.Str()
		return as == bs
	case KindList:
		al, _ := a.List()
		bl, _ := b.List()
		if len(al) != len(bl) {
			return false
		}
		for i := range al {
			if !valuesEqualForTest(al[i], bl[i]) {
				return false
			}
		}
		return true
	default:
		return a.u == b.u
	}
}

// TestLoadListAtRefusesAHeaderTheHostCannotHold covers the guard on the two header words, which are
// guest-supplied and so are the one place a value this host cannot represent arrives from outside.
func TestLoadListAtRefusesAHeaderTheHostCannotHold(t *testing.T) {
	u32 := Type{Kind: KindU32}
	lt := Type{Kind: KindList, Elem: &u32}
	load := func(at int) (Value, error) { return LoadVia(newHeap(1<<8), u32, at) }

	// A count whose byte product cannot be addressed is `ListByteLength`'s refusal, reached through the
	// header rather than through a direct call — so the guard is checked on the path a guest takes.
	h := newHeap(1 << 12)
	ptr, err := h.Realloc(0, 0, 4, 8)
	if err != nil {
		t.Fatalf("heap realloc: %v", err)
	}
	if err := h.StoreInt(0, ptr, ptrSize); err != nil {
		t.Fatalf("StoreInt: %v", err)
	}
	// 0xFFFFFFFF elements of 4 bytes each is past REALLOC_I32_MAX.
	if err := h.StoreInt(0xFFFFFFFF, ptr+ptrSize, ptrSize); err != nil {
		t.Fatalf("StoreInt: %v", err)
	}
	if _, err := LoadListAt(h, lt, ptr, load); err == nil {
		t.Error("a header naming 0xFFFFFFFF u32 elements was accepted; the product is past the ABI's " +
			"32-bit pointer space")
	}

	// A list type with no element type cannot be lifted, and is refused before the header is read —
	// which is what keeps `*t.Elem` from being a nil dereference.
	if _, err := LoadListAt(h, Type{Kind: KindList}, ptr, load); err == nil {
		t.Error("a list type with no element type was lifted")
	} else if !strings.Contains(err.Error(), "no element type") {
		t.Errorf("the refusal %q does not name the missing element type", err)
	}
}
