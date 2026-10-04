// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
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
func (in *Instantiated) CallValues(name string, args ...canon.Value) ([]canon.Value, error) {
	fn, err := in.resolveValueExport(name)
	if err != nil {
		return nil, err
	}
	if fn.sig == nil {
		return nil, fmt.Errorf("%w: export %q has no resolved component signature, so its values cannot "+
			"be checked against it", ErrUnsupportedForm, name)
	}
	// The same refusal `Call` performs, at the same point, for the same reason.
	if k, bad := unmodeledInSig(fn.sig); bad {
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
	resolved, err := fn.invokeWith(flat)
	if err != nil {
		return nil, err
	}
	return liftFlatResult(name, fn.sig, resolved)
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

// liftFlatResult lifts an export's flat core result back to a component value.
//
// `res` is what `task.return` lowered — the async lift captures it on the task, so the result is already
// in hand by the time the loop exits.
func liftFlatResult(name string, sig *FuncType, res []interp.Value) ([]canon.Value, error) {
	if sig.Result == nil {
		if len(res) != 0 {
			return nil, fmt.Errorf("%w: export %q declares no result but the guest returned %d flat value(s)",
				ErrUnsupportedForm, name, len(res))
		}
		return nil, nil
	}
	if sig.Result.Kind != VU32 {
		return nil, fmt.Errorf("%w: export %q returns %s; this slice carries u32 only (#864)",
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
