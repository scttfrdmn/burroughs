// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// A host call of an async-lifted export, answered at the callee's `task.return` (ADR 0136, E2-in-a).
//
// # What it is for
//
// Serving `wasi:http/handler#handle` is a host-initiated call of an export, once per request (contract §6 R-5's
// append on #1382: a call, not a delivery of readiness). Two things the embedder's `CallValues` cannot do are
// needed for it:
//
//   - **the answer arrives at the return, not at the exit.** A handler returns its response and then writes
//     the body into a stream the host reads. `CallValues` returns when the callee's callback loop ends, so a host
//     that waited for the response there would wait for a body nobody reads. The pin's `Task.return_` calls
//     `on_resolve` at the return (def:Task.return_), and the callee runs on in the same callback;
//   - **an own crosses in both directions.** `handle` takes an `own<request>` the host mints and returns a
//     `result<own<response>, error-code>` the host takes.
//
// Neither is public. `Component.Call` keeps refusing resource handles and keeps returning at the exit; E2-in-b's
// `Component.Handler()` is the public surface that will use this.

// newHostTable is a table of host-resource handles that belongs to no instance: the host's own side of a host call
// of an export (ADR 0136). An `own<R>` argument leaves it on the callee's entry, and an `own<R>` in the result
// enters it inside `task.return`, which is the pin's host-as-instance reading (def:lift_own from one table,
// def:lower_own into the other). It has no sockets, files or HTTP registry, since nothing in it is a guest's, so
// only `mint`, `give`, `takeAs` and `closeAll` apply to it.
func newHostTable() *hostResources {
	return &hostResources{table: canon.NewHandleTable(), objs: map[uint32]any{}, types: map[int]*hostResource{}}
}

// callReturning calls an async-lifted export as the host, and answers the host at the callee's `task.return`
// while the callee's task runs on to its exit (ADR 0136).
//
//   - **onReturn is called once, with the lifted result, inside the `task.return` that resolves the task**, on the
//     agent executing the callee and while it holds the instance's run slot. So it must not block on anything
//     the callee's progress decides and must not call into this instance: it hands the result off and returns.
//     It is not called for a task that ends without returning: one that traps first, or is cancelled.
//   - **An `own<R>` crosses through held**, the host's table (newHostTable). A top-level `own<R>` argument is an
//     own value of R's RT whose number is its index in held (canon.Own); it is moved into the callee's table on
//     the callee's entry. An `own<R>` anywhere in the result is moved out of the callee's table into held inside
//     `task.return`, and arrives in onReturn's value as its index in held, which the host takes with takeAs.
//     held may be nil for a signature carrying no own.
//
// It returns when the task has exited, with nil or the error that ended it: a trap after the return included,
// which locks the store as any trap does (lockdown.go). Whether onReturn ran is how a caller tells a failure
// before the answer from one after it. The context is CallValuesCtx's: cancelling it requests the task's
// cancellation.
func (in *Instantiated) callReturning(ctx context.Context, name string, held *hostResources,
	onReturn func(result []canon.Value), args ...canon.Value,
) error {
	if err := in.w.lock.locked(); err != nil {
		return err
	}
	startedLocked := in.w.lock.trap() != nil
	err := in.callReturningBody(ctx, name, held, onReturn, args)
	if ferr := in.w.lock.finish(startedLocked, err); ferr != nil {
		return reportExit(ferr)
	}
	return nil
}

func (in *Instantiated) callReturningBody(ctx context.Context, name string, held *hostResources,
	onReturn func(result []canon.Value), args []canon.Value,
) error {
	fn, err := in.resolveValueExport(name)
	if err != nil {
		return err
	}
	if fn.sig == nil {
		return fmt.Errorf("%w: export %q has no resolved component signature, so its values cannot be checked "+
			"against it", ErrUnsupportedForm, name)
	}
	// **Only an async lift has a `task.return` to be answered at.** A sync lift's result is its core function's
	// return, so the answer and the exit are one event, and CallValues already carries it.
	if !fn.async {
		return fmt.Errorf("%w: export %q is a sync lift; a host call answered at task.return is for an async "+
			"(callback) lift, and CallValues carries a sync one", ErrUnsupportedForm, name)
	}
	if k, bad := canonSigUnmodeled(fn.sig); bad {
		return fmt.Errorf("%w: export %q carries %s, which this engine's Canonical ABI does not model",
			ErrUnsupportedForm, name, valKindName(k))
	}
	flat, pend, err := lowerFlatArgsWith(name, fn.sig, args, hostOwnArg(name, fn, held))
	if err != nil {
		return err
	}
	task := fn.newLiftTask(ctx)
	task.held = held
	// The lifted result is checked against the declared one as CallValues checks it (liftFlatResult). A mismatch
	// is an engine refusal with no reader inside the guest's task.return, so it is kept and returned at the exit,
	// and the host is not answered.
	var answerErr error
	task.onReturn = func(r liftResult, _ *cmThread) {
		vals, lerr := liftFlatResult(name, fn.sig, r)
		if lerr != nil {
			answerErr = lerr
			return
		}
		onReturn(vals)
	}
	_, err = fn.runLiftTask(task, flat, pend)
	if err != nil {
		return err
	}
	return answerErr
}

// carriesOwn reports whether an own appears anywhere in vt.
func carriesOwn(vt ValType) bool {
	found := false
	eachHandle(vt, func(h ValType) { found = found || h.Kind == VOwn }, 0)
	return found
}

// hostOwnArg is the plan for a host call's top-level `own<R>` parameters (ADR 0136): the value must be an own of
// the host resource the declared own names, at an index of held, and it is moved into the callee's table on the
// callee's entry (lowerPending). nil without a table, so an own parameter is refused as CallValues refuses one.
func hostOwnArg(name string, fn *compFunc, held *hostResources) ownArg {
	if held == nil {
		return nil
	}
	return func(p NamedVal, v canon.Value, slot int) (pendingLower, error) {
		t, err := fn.crossOwnType(fmt.Sprintf("parameter %q", p.Name), p.Type)
		if err != nil {
			return pendingLower{}, err
		}
		if v.Type.Kind != canon.KindOwn || v.Type.RT != t.rt {
			return pendingLower{}, fmt.Errorf("component: export %q parameter %q is declared own<%s>, and the "+
				"value given is %s: a host call passes an own as canon.Own of the resource's RT and its index in "+
				"the host's table (a caller error, #1129)", name, p.Name, t.name, v.Type)
		}
		return pendingLower{param: p.Name, val: v, ptrSlot: slot, lenSlot: -1, own: t, held: held}, nil
	}
}
