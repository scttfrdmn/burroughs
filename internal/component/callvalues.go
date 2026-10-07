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

	flat, err := lowerFlatArgs(name, fn.sig, args)
	if err != nil {
		return nil, err
	}
	// **The resolution comes back as a return value, not from a field on `fn`** (#869). `fn` is shared by
	// every concurrent caller of this export, so reading a resolution off it handed two callers one slot —
	// a data race `-race` reported on the concurrent acceptance arm, which passed without it.
	resolved, err := fn.invokeWith(ctx, flat)
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
		// The declared result still has to agree with what arrived — a lifted value of the wrong kind
		// would be the mis-typing this whole path exists to refuse, just sourced from the guest.
		if sig == nil || sig.Result == nil {
			return nil, fmt.Errorf("%w: export %q declares no result but its task.return lifted a %s",
				ErrUnsupportedForm, name, res.lifted.Type.Kind)
		}
		want, werr := canonTypeOf(*sig.Result)
		if werr != nil {
			return nil, fmt.Errorf("%w: export %q's declared result cannot be carried: %w",
				ErrUnsupportedForm, name, werr)
		}
		if res.lifted.Type.Kind != want.Kind {
			return nil, fmt.Errorf("%w: export %q declares a %s result but its task.return lifted a %s",
				ErrUnsupportedForm, name, want.Kind, res.lifted.Type.Kind)
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
func lowerFlatArgs(name string, sig *FuncType, args []canon.Value) ([]interp.Value, error) {
	if len(args) != len(sig.Params) {
		return nil, fmt.Errorf("%w: export %q takes %d parameter(s), got %d",
			ErrUnsupportedForm, name, len(sig.Params), len(args))
	}
	flat := make([]interp.Value, 0, len(args))
	for i, p := range sig.Params {
		// The declared kind drives the lowering, and the value's own kind must agree with it. Trusting
		// the value alone would let a caller smuggle a kind past the signature; trusting the signature
		// alone would lower a mismatched payload as though it were the declared type.
		if p.Type.Kind != VU32 {
			return nil, fmt.Errorf("%w: export %q parameter %q is %s; this slice carries u32 only (#864)",
				ErrUnsupportedForm, name, p.Name, valKindName(p.Type.Kind))
		}
		// `U32` is kind-checked, so the value's agreement with the declared type is the accessor's answer
		// rather than a separate test that could drift from it.
		u, ok := args[i].U32()
		if !ok {
			return nil, fmt.Errorf("%w: export %q parameter %q is declared u32 but the value given is %v",
				ErrUnsupportedForm, name, p.Name, args[i].Type.Kind)
		}
		// A u32 is one flat i32 in the Canonical ABI: the low 32 bits, carried as two's complement. No
		// memory and no realloc are involved, which is why this slice needs neither.
		flat = append(flat, interp.I32(int32(u)))
	}
	return flat, nil
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
