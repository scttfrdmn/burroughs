// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// The bridge from a decoded component value type to the codec's (#903).
//
// # Why this did not exist, and why it has to now
//
// Every `canon.Type` in the engine today is **hand-built by a WASI impl that knows its own signature
// statically** — `canon.TupleType(canon.Type{Kind: canon.KindString}, …)` in `host.go`, written out
// because that one function's type is a constant. That works for a fixed set of imports and not at all
// for an export call, which is driven by whatever signature the component *declared*. So a general
// value-carrying call needs the decoded `ValType` translated into `canon.Type`, which is this file.
//
// # The refusal is by name, and it is the engine's limit rather than the type's
//
// A kind the codec does not model refuses **naming the kind**, at the call, which is where the value
// would move (ADR 0084). It is not a malformedness: the component is well-formed and the type is
// well-formed WIT: what is missing is this engine's implementation of it. Grave #301 settled that
// distinction for the loader — reporting a well-formed-but-unimplemented thing as malformed
// re-manufactures at one boundary the malformedness the decoder declined to manufacture at another — and
// the same reading applies here.
//
// # Input must be RESOLVED
//
// `canonTypeOf` refuses `VRef` by name rather than following it, because following a type index requires
// the index space the reference belongs to and a `ValType` does not carry which one it is (see `typeAt`).
// Resolution is the caller's, done once at bind: `liftSignature` and `lowerSignature` both resolve now.
// A `VRef` arriving here is therefore a *bug in a caller*, and the refusal says so rather than quietly
// producing a type for whatever the index happened to name.

// canonTypeOf converts a resolved component value type to the codec's type.
//
// The returned error names the first kind the codec cannot carry. `canonUnmodeled` is the same walk
// without the construction, so the two cannot disagree about what is carryable.
func canonTypeOf(vt ValType) (canon.Type, error) {
	t, k, ok := canonTypeWalk(vt, 0)
	if !ok {
		return canon.Type{}, fmt.Errorf("%w: %s is not modeled by this engine's Canonical ABI",
			ErrUnsupportedForm, valKindName(k))
	}
	return t, nil
}

// canonUnmodeled reports the first value kind in vt that the codec cannot carry.
//
// **It is the same walk as [canonTypeOf] and deliberately not a second predicate.** `callvalues.go`
// already records why for the refusal it reuses: *"two refusals for one condition drift apart, and the
// one nobody reads becomes the one that is wrong."* A predicate that answers "can this be carried?"
// beside a converter that answers "carry it" is exactly that pair, so there is one walk and this is a
// projection of it.
func canonUnmodeled(vt ValType) (ValKind, bool) {
	_, k, ok := canonTypeWalk(vt, 0)
	return k, !ok
}

// The structural renderer that used to live here is now `canon.Type.String()`.
//
// It was written here, one layer above the type it renders, because the #924 review needed it for the
// parameter refusal and this was where that refusal was built. That was the wrong home: the public
// boundary needs the same rendering for `ComponentType.String()`, and `canon.List`'s own element refusal
// needed it too and did not have it — it printed `.Kind` on both sides, so a mismatched element type was
// reported as "element 0 is list, want list". Two more consumers, each of which would otherwise grow its
// own version of a function whose entire purpose is that there be one of it.
//
// Moving it also makes `%s` on a `canon.Type` correct by default, which is the form of the repair that
// does not depend on the next author knowing the lesson.

// canonSigUnmodeled reports the first value kind anywhere in a function signature that the codec cannot
// carry — [canonUnmodeled] over the parameters and the result.
//
// It is the export-call path's refusal (`CallValuesCtx`), standing where `unmodeledInSig` stood. The
// difference between them is one kind and is recorded at the call site.
func canonSigUnmodeled(ft *FuncType) (ValKind, bool) {
	if ft == nil {
		return 0, false
	}
	for _, p := range ft.Params {
		if k, bad := canonUnmodeled(p.Type); bad {
			return k, true
		}
	}
	if ft.Result != nil {
		return canonUnmodeled(*ft.Result)
	}
	return 0, false
}

// canonTypeWalk is the one walk. It returns the codec type, and on failure the kind that defeated it.
//
// The depth bound is belt-and-braces behind `resolveVal`'s cycle guard: a resolved type is acyclic by
// construction (a cycle resolves to VUnresolvedAlias, refused below), so this bound is reached only by a
// type built directly in a test or by a resolver bug, and 32 is `unmodeledValKind`'s existing figure
// rather than a new one to justify.
func canonTypeWalk(vt ValType, depth int) (canon.Type, ValKind, bool) {
	if depth > 32 {
		return canon.Type{}, vt.Kind, false
	}
	switch vt.Kind {
	case VBool:
		return canon.Type{Kind: canon.KindBool}, 0, true
	case VU8:
		return canon.Type{Kind: canon.KindU8}, 0, true
	case VU16:
		return canon.Type{Kind: canon.KindU16}, 0, true
	case VU32:
		return canon.Type{Kind: canon.KindU32}, 0, true
	case VU64:
		return canon.Type{Kind: canon.KindU64}, 0, true
	case VS8:
		return canon.Type{Kind: canon.KindS8}, 0, true
	case VS16:
		return canon.Type{Kind: canon.KindS16}, 0, true
	case VS32:
		return canon.Type{Kind: canon.KindS32}, 0, true
	case VS64:
		return canon.Type{Kind: canon.KindS64}, 0, true
	case VF32:
		return canon.Type{Kind: canon.KindF32}, 0, true
	case VF64:
		return canon.Type{Kind: canon.KindF64}, 0, true
	case VChar:
		return canon.Type{Kind: canon.KindChar}, 0, true
	case VString:
		return canon.Type{Kind: canon.KindString}, 0, true

	case VList:
		// A list with no element type is malformed rather than unmodeled, and is refused as its own kind
		// rather than dereferenced — the decoder should never produce one, which is why this is a guard and
		// not a case the error text explains.
		if vt.Elem == nil {
			return canon.Type{}, VList, false
		}
		e, k, ok := canonTypeWalk(*vt.Elem, depth+1)
		if !ok {
			return canon.Type{}, k, false
		}
		return canon.Type{Kind: canon.KindList, Elem: &e}, 0, true

	case VTuple:
		// `tuple` is modeled for size and alignment only — enough to lower an *empty* `list<tuple>`, which
		// is what the WASI getters return. Its element lowering is #904's, and the codec refuses it at
		// marshal rather than here, so the TYPE is buildable and a value of it is not. That split is
		// deliberate and is the reason this arm succeeds: refusing the type would refuse
		// `get-environment`'s empty list, which works today.
		fields := make([]canon.Type, len(vt.Elems))
		for i, e := range vt.Elems {
			ft, k, ok := canonTypeWalk(e, depth+1)
			if !ok {
				return canon.Type{}, k, false
			}
			fields[i] = ft
		}
		return canon.TupleType(fields...), 0, true

	case VVariant:
		cases := make([]canon.Case, len(vt.Cases))
		for i, c := range vt.Cases {
			cases[i] = canon.Case{Name: c.Name}
			if c.Type == nil {
				continue
			}
			ct, k, ok := canonTypeWalk(*c.Type, depth+1)
			if !ok {
				return canon.Type{}, k, false
			}
			cases[i].Type = &ct
		}
		return canon.VariantType(cases...), 0, true

	case VResult:
		// The model despecializes `result` to a two-case variant named "ok"/"error" (definitions.py:1136),
		// and `canon.ResultType` is that despecialization. Built through it rather than by hand so the case
		// names come from one place.
		var okT, errT *canon.Type
		if vt.Ok != nil {
			t, k, ok := canonTypeWalk(*vt.Ok, depth+1)
			if !ok {
				return canon.Type{}, k, false
			}
			okT = &t
		}
		if vt.Err != nil {
			t, k, ok := canonTypeWalk(*vt.Err, depth+1)
			if !ok {
				return canon.Type{}, k, false
			}
			errT = &t
		}
		return canon.ResultType(okT, errT), 0, true

	case VOwn:
		return canon.OwnType(int(vt.Ref)), 0, true
	case VBorrow:
		return canon.BorrowType(int(vt.Ref)), 0, true

	default:
		// VRecord, VFlags, VEnum, VOption, VErrorContext, VUnresolvedAlias — not modeled by the codec.
		//
		// **VStream and VFuture are refused here even though `canon.KindStream`/`KindFuture` exist**, and
		// that is not an inconsistency: those two are minted by the async lower/lift wrapper into the
		// per-instance handle table, which the codec cannot reach, and `canon`'s own doc says they must
		// never reach the codec's Kind switches. A type the codec would panic on is not a type this bridge
		// may hand it.
		//
		// **VRef is refused for a different reason than the rest** — see the file header. It is unmodeled
		// here not because the codec lacks the kind but because this function cannot know which index space
		// the reference belongs to, and resolving against the wrong one yields a different type that exists.
		return canon.Type{}, vt.Kind, false
	}
}
