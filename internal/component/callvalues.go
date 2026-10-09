// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The internal value-carrying export call (gate:async 2a-ii, #864).
//
// # What this adds, and what already existed
//
// `canon.Value` is already ADR 0085's tagged union, and values already cross the IMPORT boundary — an
// `asyncLowerImpl` answers a guest through `onResolve(canon.Value)`. What was missing is the other
// direction: no way to pass values INTO a guest export or read values back out. `Instantiated.Call` takes
// a name and nothing else, and said so: *"Value-carrying exports are a later slice (run moves none)."*
//
// # Scope: the kinds the committed guest drives
//
// `u32` in, `u32` out, which is what `single/component.wasm`'s `compute: async func(x: u32) -> u32`
// needs. A kind outside that set **refuses by name** rather than being marshalled approximately — ADR
// 0085's guest-driven scope rule, where a kind arrives when a guest needs it.
//
// This is deliberately NOT a general marshalling surface. The codec that does the general case
// (`internal/component/canon`) is reached through memory and a realloc for anything compound; a `u32` is a
// single flat scalar in the Canonical ABI and needs neither, so the narrow path is also the honest one —
// it does not pretend to a generality it has not been measured at.
//
// # Public exposure is out of scope
//
// ADR 0085's consumer-triggered landing governs the PUBLIC type, and its amendment 1 draws the line at
// exposure. #858 adds `LoadComponent`/`*Component` and the boundary conversion; it cannot replace the
// shape, because the shape is `canon.Value` and that already is the union.

// CallValues invokes a component export with component values and returns its results.
//
// It is the value-carrying counterpart of [Instantiated.Call], which moves none. The refusal for an
// export whose signature carries an unmodelled kind is `Call`'s — `unmodeledInSig`, reached at the call
// rather than at load, because *the boundary is where the value moves* (ADR 0084). Reusing it rather than
// adding a second refusal is deliberate: two refusals for one condition drift apart, and the one nobody
// reads becomes the one that is wrong.
// CallValues is [Instantiated.CallValuesCtx] with no cancellation, which is what every test that is not
// *about* cancellation wants.
//
// **Two entry points rather than one, deliberately.** The usual rule here is one entry point with the
// optional thing as a nil argument — `invokeWith(nil)` over a second name. It does not apply: a context
// is not an argument a reader can ignore, and threading `context.Background()` through seventeen call
// sites whose subject is the lift loop would put ceremony in front of what each one is actually asserting.
// Both names have live callers, so neither is the dead wrapper that rule exists to prevent.
func (in *Instantiated) CallValues(name string, args ...canon.Value) ([]canon.Value, error) {
	return in.CallValuesCtx(context.Background(), name, args...)
}

// CallValuesCtx is CallValues carrying the call's context (#880, #858): cancelling it **requests the
// task's cancellation** rather than abandoning the call, so the guest runs its own cancellation path and
// the call returns `ErrCancelled` from a task that actually ended. It reaches the two places a lift task
// waits — the park and the entry semaphore.
func (in *Instantiated) CallValuesCtx(ctx context.Context, name string, args ...canon.Value) ([]canon.Value, error) {
	fn, err := in.resolveValueExport(name)
	if err != nil {
		return nil, err
	}
	if fn.sig == nil {
		return nil, fmt.Errorf("%w: export %q has no resolved component signature, so its values cannot "+
			"be checked against it", ErrUnsupportedForm, name)
	}
	// **The refusal is the bridge's, where it was `unmodeledInSig`'s until #903.**
	//
	// Both answer "can the codec carry this signature", and this file already records why two refusals for
	// one condition are wrong — *"they drift apart, and the one nobody reads becomes the one that is
	// wrong."* So the export-call path asks the thing that actually builds the codec types, rather than a
	// predicate that agrees with it by inspection.
	//
	// It is also strictly better here, on the one case where the two disagree. `unmodeledValKind`'s default
	// arm counts a `VRef` **modeled** — true of a handle, which is an i32 whatever it references — and it
	// does not follow the reference. So an export whose parameter was a reference to a record passed the
	// old check without anything looking at what the reference named, and was refused further down by the
	// `VU32` guard, which reported the wrong reason. Lift signatures are resolved now, so the bridge sees
	// through the reference and names the record.
	//
	// `unmodeledInSig` is deliberately NOT retired: it is consulted at **instantiate** for every
	// implemented lower, so changing its verdict changes which components load. That is its own slice with
	// its own witness, and `TestTheBridgeAndTheUnmodeledPredicateAgree` pins the two together until then.
	if k, bad := canonSigUnmodeled(fn.sig); bad {
		return nil, fmt.Errorf("%w: export %q carries %s, which this engine's Canonical ABI does not model",
			ErrUnsupportedForm, name, valKindName(k))
	}

	// `pend` is the arguments whose bytes must land in guest memory. They are **not** lowered here: the
	// model lowers a lift's parameters inside the callee's task, on the entry the callee runs on, and
	// nothing holds that entry at this point (lowerargs.go has the argument). Empty for every signature
	// that carries only scalars, which is every committed guest but one.
	flat, pend, err := lowerFlatArgs(name, fn.sig, args)
	if err != nil {
		return nil, err
	}
	// **The resolution comes back as a return value, not from a field on `fn`** (#869). `fn` is shared by
	// every concurrent caller of this export, so reading a resolution off it handed two callers one slot —
	// a data race `-race` reported on the concurrent acceptance arm, which passed without it.
	resolved, err := fn.invokeWithPending(ctx, flat, pend)
	if err != nil {
		return nil, err
	}
	return liftFlatResult(name, fn.sig, resolved)
}

// liftedOrFlat is the rule `liftResult` exists to state: **an eagerly-lifted value wins.**
//
// It was read at the only moment it was readable — inside `task.return`, before the guest resumed — so
// preferring the flat words would reintroduce exactly the staleness the eager lift removes. The flat
// words remain the answer where there was nothing to lift eagerly: a sync lift, which has no
// `task.return` at all.
func liftFlatResult(name string, sig *FuncType, res liftResult) ([]canon.Value, error) {
	if res.lifted != nil {
		// The declared result still has to agree with what arrived — a lifted value of the wrong type
		// would be the mis-typing this whole path exists to refuse, just sourced from the guest.
		if sig == nil || sig.Result == nil {
			return nil, fmt.Errorf("%w: export %q declares no result but its task.return lifted a %s",
				ErrUnsupportedForm, name, res.lifted.Type)
		}
		want, werr := canonTypeOf(*sig.Result)
		if werr != nil {
			return nil, fmt.Errorf("%w: export %q's declared result cannot be carried: %w",
				ErrUnsupportedForm, name, werr)
		}
		// **Structural, like the parameter check** — this was `Kind != want.Kind` until the same defect
		// was noticed in the same file in the other direction. Nothing reaches it today that a kind
		// comparison would miss, because the eager lift refuses every compound result, so this is a
		// repair made while the arm is still unreachable rather than after a wrong value crossed.
		//
		// That is the whole argument for doing it now: when the lift arm lands, a `list<string>` lifted
		// against a declared `list<u32>` is a real engine bug, and a check that compared kinds would pass
		// it through to an embedder as data. The cost of being early is one line; the cost of being late
		// is a plausible wrong value in the direction nobody is watching.
		if !canon.TypeEqual(res.lifted.Type, want) {
			return nil, fmt.Errorf("%w: export %q declares a %s result but its task.return lifted a %s",
				ErrUnsupportedForm, name, want, res.lifted.Type)
		}
		return []canon.Value{*res.lifted}, nil
	}
	return liftFlatCoreResult(name, sig, res.flat)
}

// resolveValueExport finds the callable function an export name denotes.
//
// # Why the path form, and only the path form
//
// A WIT world that exports an INTERFACE exports an instance, not a function: the committed `single` guest
// exports `test:probe/ops@0.1.0`, and `compute` lives inside it. So a name has to be able to say which.
// The form is `interface#function` — the component model's own spelling, and the one the lift's core func
// already carries (`[async-lift]test:probe/ops@0.1.0#compute`).
//
// **A bare name is not resolved by searching the instances**, even though wasmtime's CLI does exactly that
// and it would have made the witness shorter. A search picks silently when two interfaces export the same
// function name, and *a first-match pick declines to ask*. If a convenience resolver is ever wanted it is
// a layer over this, which is the A-then-D shape ADR 0085 uses for its value constructors — the explicit
// total form first, never instead of.
func (in *Instantiated) resolveValueExport(name string) (*compFunc, error) {
	iface, fname, isPath := strings.Cut(name, "#")
	if !isPath {
		cd, ok := in.export.exports[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrNoRun, name)
		}
		if cd.fn == nil {
			return nil, fmt.Errorf("%w: export %q is an instance, not a function; name the function as "+
				"%q#<function>", ErrUnsupportedForm, name, name)
		}
		return cd.fn, nil
	}
	cd, ok := in.export.exports[iface]
	if !ok {
		return nil, fmt.Errorf("%w: no export %q (from %q)", ErrNoRun, iface, name)
	}
	if cd.inst == nil {
		return nil, fmt.Errorf("%w: export %q is not an instance, so %q names nothing inside it",
			ErrUnsupportedForm, iface, name)
	}
	rd, ok := cd.inst.export(fname)
	if !ok {
		return nil, fmt.Errorf("%w: instance %q has no export %q", ErrNoRun, iface, fname)
	}
	if rd.fn == nil {
		return nil, fmt.Errorf("%w: %q is not a callable function", ErrUnsupportedForm, name)
	}
	return rd.fn, nil
}

// lowerFlatArgs lowers component values to the flat core args an export's lifted callee takes.
//
// Arity is checked against the SIGNATURE, not against the arguments: a call with the wrong count is a
// caller error that must be named, and lowering whatever arrived would hand the guest a short frame.
// A `string` parameter yields **two placeholder slots and a `pendingLower`**, because its bytes cannot be
// placed here: the model lowers a lift's parameters inside the callee's task, on the entry the callee then
// runs on (definitions.py:2097-2107), and nothing holds that entry at this point. See `lowerargs.go`.
func lowerFlatArgs(name string, sig *FuncType, args []canon.Value) ([]interp.Value, []pendingLower, error) {
	if len(args) != len(sig.Params) {
		return nil, nil, fmt.Errorf("%w: export %q takes %d parameter(s), got %d",
			ErrUnsupportedForm, name, len(sig.Params), len(args))
	}
	flat := make([]interp.Value, 0, len(args))
	var pend []pendingLower
	for i, p := range sig.Params {
		// **The value's whole type must equal the declared type, structurally** — checked here, for every
		// parameter, before any arm looks at the kind.
		//
		// Comparing kinds was a correctness hole and not a shortcut: `Kind` says `list` for both
		// `list<u32>` and `list<string>`, so a `list<string>` passed where a `list<u32>` was declared got
		// through, and the lowering then wrote strings at the **value's** element stride into a buffer the
		// guest reads as 4-byte integers. A plausible wrong value, not an error. (Caught by the chair on
		// the #924 review; it is ADR 0097's first change, one layer below where that change was written.)
		//
		// `canon.TypeEqual` is the one structural comparison, shared with the public constructors'
		// element check, so the two cannot drift about what "the same type" means. `canon.Type.String`
		// is the matching structural **rendering**, and the pair belongs together: comparing kinds is
		// the defect, and reporting kinds is how the defect hides. Both now live beside the type they
		// are about, so the `%s` verbs below name `list<u32>` rather than `list` with no help from here.
		want, berr := canonTypeOf(p.Type)
		if berr != nil {
			return nil, nil, fmt.Errorf("%w: export %q parameter %q: %w",
				ErrUnsupportedForm, name, p.Name, berr)
		}
		if !canon.TypeEqual(args[i].Type, want) {
			return nil, nil, fmt.Errorf("%w: export %q parameter %q is declared %s but the value given is "+
				"%s; the types must match structurally, so a list of the wrong element type is refused "+
				"here rather than written at the wrong stride",
				ErrUnsupportedForm, name, p.Name, want, args[i].Type)
		}
		// The lowering is one recursive walk, because **a record's flat form is the concatenation of
		// its fields'** (CanonicalABI.md `lower_flat_record`). So a record of scalars is words, a
		// record with a string field is words plus a deferred allocation, and a nested record is
		// neither special nor a second arm — which is why the record parameter arrived here as a
		// refactor rather than as a fourth `if`.
		if err := lowerOneArg(name, p.Name, args[i], &flat, &pend); err != nil {
			return nil, nil, err
		}
	}
	return flat, pend, nil
}

// lowerOneArg appends one value's flat words to `flat`, and a `pendingLower` for each piece of it whose
// bytes must land in guest memory.
//
// # Why this recurses rather than switching per parameter
//
// It was a flat `if string || list { … } else if u32 { … }` over the parameter's own kind. A record
// broke that shape rather than extending it: a record's flat form is its fields' flat forms
// concatenated, so a `record { name: string, n: u32 }` is three words of which the first two are a
// deferred `(ptr, len)` pair and the third is a scalar. Handling that with a per-parameter switch means
// the record arm reimplements the string arm, which is how a codec grows two of itself.
//
// **Each memory-resident piece gets its own `pendingLower`**, with its own pair of slot indices into
// `flat`. The existing machinery already supported this — `pendingLower` carries slot *indices* rather
// than pointers precisely so `flat` can keep growing underneath it — so a record's string field needed
// no new mechanism, only the walk.
//
// The declared type is **not** passed down: the structural comparison in the caller already established
// that this value's type equals the declared one, which is strictly stronger than anything a per-field
// kind check could assert, so the value's own type is the authority from here on.
func lowerOneArg(export, param string, v canon.Value, flat *[]interp.Value, pend *[]pendingLower) error {
	switch v.Type.Kind {
	case canon.KindU32:
		// The accessor's boolean is **unreachable** given the caller's structural comparison. Checked
		// anyway: the accessor is the authority on whether the kind and the payload agree, and trusting
		// the type tag alone is the mis-read `U32`'s own doc comment exists to prevent.
		u, ok := v.U32()
		if !ok {
			return fmt.Errorf("%w: export %q parameter %q is declared u32 and its type says so, but the "+
				"value did not read as one", ErrUnsupportedForm, export, param)
		}
		// A u32 is one flat i32: the low 32 bits, carried as two's complement. No memory, no realloc.
		*flat = append(*flat, interp.I32(int32(u)))
		return nil

	case canon.KindString, canon.KindList:
		// **Both flatten to `(i32 ptr, i32 len)` and both need guest memory**, so they take one arm:
		// reserve two placeholder slots and defer the lowering to the entry where the codec can run
		// against the guest's realloc. The placeholders are a null pointer and a zero length on
		// purpose — a lowering that forgot to patch them traps in any guest that reads the value,
		// rather than silently passing an empty one.
		ptrSlot, lenSlot := len(*flat), len(*flat)+1
		*flat = append(*flat, interp.I32(0), interp.I32(0))
		*pend = append(*pend, pendingLower{param: param, val: v, ptrSlot: ptrSlot, lenSlot: lenSlot})
		return nil

	case canon.KindRecord:
		// `lower_flat_record` is the fields' flat lowerings concatenated, in **declared** order — no
		// discriminant and no join, unlike a variant. The field order is the layout, and it comes from
		// the type's own field list; `canon.Record` already matched the caller's values to it by label,
		// so from here the order is positional and no name is looked up.
		fields, ok := v.Record()
		if !ok {
			return fmt.Errorf("%w: export %q parameter %q is tagged record but does not read as one",
				ErrUnsupportedForm, export, param)
		}
		for i, fv := range fields {
			// The parameter name is qualified per field, so a refusal three levels into a nested record
			// says which field rather than just which parameter.
			if err := lowerOneArg(export, param+"."+v.Type.Fields[i].Name, fv, flat, pend); err != nil {
				return err
			}
		}
		return nil

	default:
		return fmt.Errorf("%w: export %q parameter %q is %s; this engine lowers u32, string, list and "+
			"record arguments", ErrUnsupportedForm, export, param, v.Type)
	}
}

// liftFlatCoreResult lifts an export's flat core result back to a component value.
//
// **This is the path for a result that was NOT lifted eagerly** — a sync lift's own returns. It was the
// only path until #903, and its doc comment said `res` "is what task.return lowered — the async lift
// captures it on the task, so the result is already in hand by the time the loop exits." That was true
// of a `u32`, whose value *is* its flat word, and it is the precise sentence that hid the defect: for
// anything whose payload lives in guest memory, "already in hand" was false. The words were in hand; the
// bytes they pointed at were the guest's to overwrite.
func liftFlatCoreResult(name string, sig *FuncType, res []interp.Value) ([]canon.Value, error) {
	if sig.Result == nil {
		if len(res) != 0 {
			return nil, fmt.Errorf("%w: export %q declares no result but the guest returned %d flat value(s)",
				ErrUnsupportedForm, name, len(res))
		}
		return nil, nil
	}
	// **A SYNC lift carries `u32` only, and the reason is `post-return` rather than the lift.**
	//
	// The message here said *"this slice carries u32 only (#864)"*, which was true of both paths when it
	// was written and is now true of only this one — the async path lifts a `string` eagerly inside
	// `task.return` (#903). A refusal that describes the engine as narrower than it is sends a reader
	// looking for a limit in the wrong place.
	//
	// A sync `string` result needs **two** things this engine does not have, and the first is the sharper
	// one:
	//
	//  1. **It does not arrive in flat words at all.** The sync path lifts with `MAX_FLAT_RESULTS`, which
	//     is **1** (definitions.py:2109, and the constant at def:1784), so anything flattening wider than
	//     one word — a string is two — **spills to a return pointer**: the caller passes a pointer and the
	//     callee writes the `(ptr, len)` pair there. The async path lifts with `MAX_FLAT_PARAMS` (16,
	//     def:2336), which is why a string crosses `task.return` flat and crosses here not at all.
	//  2. **`post-return`.** The guest allocated the buffer and nothing has freed it; the ABI's answer is
	//     the lift's `post-return`, called **after** the lift (def:2111-2116, confirmed at the model rather
	//     than assumed). This engine neither captures that canonopt for a lift nor calls it, so lifting
	//     the bytes without it would leak the guest's allocation on every call.
	//
	// So this is a refusal **by name** and not a fallthrough. Reaching the flat-word path below with a
	// string result would read the **return pointer** as though it were the number — a plausible wrong
	// value rather than an error, which is the outcome the refusal exists to prevent.
	if sig.Result.Kind != VU32 {
		// The closing hint names what DOES work without claiming it for the kind in hand. An earlier
		// draft said "an async export returning %s works", which is true for `string` and **false for
		// `list` and `record`** — the eager lift refuses those too. A refusal that misdirects is worse
		// than one that only declines.
		return nil, fmt.Errorf("%w: export %q is a sync lift returning %s; a sync lift carries u32 only. "+
			"A result flattening wider than one word returns through a return pointer rather than in flat "+
			"values, and freeing the guest's buffer needs the lift's post-return — this engine does "+
			"neither. A string result does cross from an async export, through task.return",
			ErrUnsupportedForm, name, valKindName(sig.Result.Kind))
	}
	// A declared result the guest never produced is a distinct failure from a wrong value, and is named as
	// one: the async lift leaves `result` empty when `task.return` was never reached.
	if len(res) != 1 {
		return nil, fmt.Errorf("%w: export %q declares a u32 result but the guest returned %d flat "+
			"value(s); one is expected", ErrUnsupportedForm, name, len(res))
	}
	return []canon.Value{canon.U32(uint32(res[0].Int32()))}, nil
}
